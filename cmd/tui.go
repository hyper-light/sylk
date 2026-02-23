package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/agents/academic"
	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/archivalist"
	"github.com/adalundhe/sylk/agents/designer"
	"github.com/adalundhe/sylk/agents/engineer"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/inspector"
	"github.com/adalundhe/sylk/agents/librarian"
	"github.com/adalundhe/sylk/agents/orchestrator"
	"github.com/adalundhe/sylk/agents/tester"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/activation"
	"github.com/adalundhe/sylk/core/container/daemon"
	"github.com/adalundhe/sylk/core/container/network"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui"
	"github.com/spf13/cobra"
)

var (
	tuiTheme string
	tuiMock  bool
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive terminal UI",
	Long:  `Launch Sylk's terminal UI with multi-agent chat, session management, and code viewing.`,
	RunE:  runTUI,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
	tuiCmd.Flags().StringVar(&tuiTheme, "theme", "dark", "Color theme (dark or light)")
	tuiCmd.Flags().BoolVar(&tuiMock, "mock", false, "Run with mock backend (no real agents)")
}

func runTUI(_ *cobra.Command, _ []string) error {
	restoreStdLog := installTUIStdLogSink()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	deps, cleanup, err := bootstrapDeps(ctx, tuiMock)
	if err != nil {
		stop()
		restoreErr := restoreStdLog()
		return errors.Join(fmt.Errorf("bootstrap: %w", err), restoreErr)
	}
	// Pass stop to ui.Run so it can restore default signal handling
	// between p.Run() returning and app.Shutdown() — allowing a second
	// Ctrl+C to force-kill the process during slow shutdown.
	deps.SignalStop = stop

	cfg := ui.DefaultConfig()
	cfg.ThemeMode = parseThemeMode(tuiTheme)
	cfg.MockMode = tuiMock

	runErr := ui.Run(ctx, cfg, deps)
	stop()
	cleanupErr := cleanup()
	restoreErr := restoreStdLog()
	return errors.Join(runErr, cleanupErr, restoreErr)
}

// activityBusBuffer is the channel size for the activity event bus.
const activityBusBuffer = 1000

// shutdownTimeout bounds total cleanup time. Derived from the longest
// possible graceful stop (60s for max-context agents) + headroom.
const shutdownTimeout = 90 * time.Second

// bootstrapDeps initializes the core systems needed by the TUI.
// Agents run inside containers managed by the container runtime.
// DaemonSets keep Guide and Orchestrator always-hot; on-demand agents
// activate via the ActivationController when the Guide routes requests.
func bootstrapDeps(ctx context.Context, mockMode bool) (ui.Deps, func() error, error) {
	_ = mockMode

	// ---------------------------------------------------------------
	// 1. Shared infrastructure — no inter-dependencies
	// ---------------------------------------------------------------

	scope := concurrency.NewGoroutineScope(ctx, "tui", nil)
	scope.SetMaxLifetime(24 * time.Hour)

	activityBus := events.NewActivityEventBus(activityBusBuffer)
	activityBus.Start()

	guideBus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	streamMgr := guide.NewStreamManager(guide.DefaultStreamConfig())

	sessionMgr := session.NewManager(session.ManagerConfig{
		Scope: scope,
	})

	// ---------------------------------------------------------------
	// 2. Container infrastructure
	// ---------------------------------------------------------------

	descriptors := handoff.NewDescriptorRegistry()
	pressureLevel := new(atomic.Int32)
	budget := concurrency.NewGoroutineBudget(pressureLevel)
	containerReg := container.NewContainerRegistry()
	serviceReg := network.NewServiceRegistry()
	specReg := container.NewAgentSpecRegistry(descriptors)

	quota := container.NewResourceQuota(quotaFromSpecs(specReg))

	// ---------------------------------------------------------------
	// 3. Agent creator registry — maps type → factory
	// ---------------------------------------------------------------

	creatorReg := container.NewAgentCreatorRegistry()
	registerAgentCreators(creatorReg, guideBus, activityBus)

	// ---------------------------------------------------------------
	// 4. Admission — hook mutator attaches lifecycle hooks to every
	//    container spec. The Guide and ServiceRegistry are set after
	//    the Guide container is created (circular dependency resolved
	//    by deferred SetGuide call).
	// ---------------------------------------------------------------

	hookMut := &lifecycleHookMutator{
		serviceReg: serviceReg,
	}
	admission := container.NewAdmissionController(nil, []container.SpecMutator{hookMut})

	// ---------------------------------------------------------------
	// 5. Container runtime
	// ---------------------------------------------------------------

	runtime := container.NewDefaultRuntime(container.DefaultRuntimeConfig{
		Budget:      budget,
		Registry:    containerReg,
		Quota:       quota,
		Admission:   admission,
		CreateAgent: creatorReg.Creator(),
		ParentCtx:   ctx,
	})

	// ---------------------------------------------------------------
	// 6. Network namespace — policy enforcement on bus traffic
	// ---------------------------------------------------------------

	policies := container.BuildNetworkPolicies(descriptors.All())
	busBridge := network.NewBusBridge(func(topic, sourceAgent, targetAgent string, payload []byte) error {
		msg := guide.NewBridgeMessage(sourceAgent, targetAgent, payload)
		return guideBus.Publish(topic, msg)
	})
	namespace := network.NewNetworkNamespace(network.NetworkNamespaceConfig{
		PodID:    "system",
		Policies: policies,
		Sink:     busBridge,
	})

	// ---------------------------------------------------------------
	// 7. DaemonSets — creates Guide + Orchestrator (always-hot)
	// ---------------------------------------------------------------

	daemonCtrl := daemon.NewDaemonSetController(runtime, containerReg)
	for _, spec := range daemon.AgentDaemonSetSpecs(specReg) {
		daemonCtrl.Apply(spec)
	}
	if err := daemonCtrl.Reconcile(ctx); err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("daemon reconcile: %w", err)
	}

	// Extract Guide and Orchestrator from their containers.
	g, err := extractAgent[*guide.Guide](containerReg, "guide")
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("extract guide: %w", err)
	}
	orch, err := extractAgent[*orchestrator.Orchestrator](containerReg, "orchestrator")
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("extract orchestrator: %w", err)
	}

	// Now wire the Guide into the hook mutator so future containers
	// get PostStart/PreStop hooks that register with the Guide.
	hookMut.SetGuide(g)

	// Register orchestrator as a router with the Guide.
	if err := registerOrchestratorWithGuide(g, orch); err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("register orchestrator: %w", err)
	}

	// ---------------------------------------------------------------
	// 8. Activation controller — on-demand agent lifecycle
	// ---------------------------------------------------------------

	activationPolicies := activation.AgentActivationPolicies(descriptors.All())
	storageDir, err := activationStorageDir()
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("activation dir: %w", err)
	}

	activationCtrl, err := activation.NewActivationController(activation.ActivationControllerConfig{
		Runtime:    runtime,
		Registry:   containerReg,
		Scope:      scope,
		Policies:   activationPolicies,
		StorageDir: storageDir,
	})
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("activation controller: %w", err)
	}
	if err := activationCtrl.Start(budget, quota); err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("activation start: %w", err)
	}

	// Wire activation hooks into Guide:
	// - Activation hook ensures target agent containers are hot before forwarding.
	// - Touch activity hook resets idle timers on active conversation agents,
	//   preventing demotion during ongoing interactions (pause over terminate).
	g.SetActivationHook(func(fwdCtx context.Context, agentType string) error {
		_, activateErr := activationCtrl.EnsureActive(fwdCtx, agentType)
		return activateErr
	})
	g.SetTouchActivityHook(func(agentType string) {
		activationCtrl.TouchActivity(agentType)
	})
	g.SetServiceRegistry(serviceReg)

	// Wire task router into orchestrator for DAG→container dispatch.
	// Routes through guide.requests for policy, audit, and activation enforcement.
	orch.SetTaskRouter(orchestrator.NewTaskRouter(orchestrator.TaskRouterConfig{
		Bus:       guideBus,
		Scope:     scope,
		AgentID:   "orchestrator",
		SessionID: "default",
	}))

	// ---------------------------------------------------------------
	// 9. Health sync — probe results → ServiceRegistry
	// ---------------------------------------------------------------

	healthSyncer := container.NewHealthSyncer(container.HealthSyncerConfig{
		ContainerRegistry: containerReg,
		ServiceRegistry:   serviceReg,
		Scope:             scope,
	})
	if err := healthSyncer.Start(); err != nil {
		slog.Warn("health syncer start failed", "error", err)
	}

	// ---------------------------------------------------------------
	// 10. Pre-activate Architect (commonly used first)
	// ---------------------------------------------------------------

	if _, err := activationCtrl.EnsureActive(ctx, "architect"); err != nil {
		slog.Warn("architect pre-activation failed", "error", err)
	} else {
		arch, archErr := extractAgent[*architect.Architect](containerReg, "architect")
		if archErr == nil {
			_ = registerArchitectWithGuide(g, arch)
		}
	}

	// ---------------------------------------------------------------
	// 11. Handoff supervisor — context management + agent lifecycle
	// ---------------------------------------------------------------

	// Extract the architect reference (may be nil if pre-activation failed).
	arch, _ := extractAgent[*architect.Architect](containerReg, "architect")
	supervisor := bootstrapHandoffSupervisor(g, arch, orch)

	// ---------------------------------------------------------------
	// 12. Default session
	// ---------------------------------------------------------------

	defaultSession, err := sessionMgr.Create(ctx, session.DefaultConfig())
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("default session: %w", err)
	}
	_ = sessionMgr.Switch(defaultSession.ID())

	// Signal orchestrator that bootstrap is complete — unblocks the LLM
	// event loop so it only processes events arriving after readiness.
	orch.SignalReady()

	// ---------------------------------------------------------------
	// Cleanup — ordered teardown
	// ---------------------------------------------------------------

	cleanup := func() error {
		// Shutdown timeout prevents hung agents from blocking process exit.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()

		var errs []error

		// Supervisor first (stops handoff tracking).
		if supervisor != nil {
			if stopErr := supervisor.Stop(); stopErr != nil {
				errs = append(errs, stopErr)
			}
		}

		// Activation controller — demotes all active agents, pausing where
		// possible (preferring pause over terminate per lifecycle policy).
		if shutErr := activationCtrl.Shutdown(shutdownCtx); shutErr != nil {
			errs = append(errs, shutErr)
		}

		// Stop all remaining containers.
		for _, c := range containerReg.All() {
			if c.IsRunning() {
				if stopErr := runtime.StopContainer(shutdownCtx, c); stopErr != nil {
					errs = append(errs, stopErr)
				}
			}
			if removeErr := runtime.RemoveContainer(shutdownCtx, c); removeErr != nil {
				errs = append(errs, removeErr)
			}
		}

		namespace.Close()
		runtime.Close()

		if busErr := guideBus.Close(); busErr != nil {
			errs = append(errs, busErr)
		}
		activityBus.Close()

		return errors.Join(errs...)
	}

	deps := ui.Deps{
		ActivityBus:    activityBus,
		SessionManager: sessionMgr,
		GuideBus:       guideBus,
		StreamManager:  streamMgr,
		Guide:          g,
		Scope:          scope,
		AuthRefresh:    buildAuthRefreshHook(g, arch, orch),
	}

	return deps, cleanup, nil
}

// =============================================================================
// Agent Creator Registration
// =============================================================================

// registerAgentCreators populates the creator registry with factory closures
// for every agent type. Each factory creates and starts the agent on the bus.
func registerAgentCreators(
	reg *container.AgentCreatorRegistry,
	bus guide.EventBus,
	actBus *events.ActivityEventBus,
) {
	// Guide — Gemini with rule-based fallback.
	reg.Register("guide", func(ctx context.Context) (container.ContainerAgent, error) {
		return bootstrapLiveGuide(ctx, bus)
	})

	// Architect — Anthropic LLM planner.
	reg.Register("architect", func(_ context.Context) (container.ContainerAgent, error) {
		return bootstrapArchitect(bus)
	})

	// Orchestrator — pipeline coordinator.
	reg.Register("orchestrator", func(ctx context.Context) (container.ContainerAgent, error) {
		return bootstrapOrchestrator(ctx, bus, actBus)
	})

	// On-demand agents — created lazily by the ActivationController.
	registerOnDemandAgentCreators(reg, bus)
}

// registerOnDemandAgentCreators registers factories for knowledge and pipeline agents.
// These are created lazily by the ActivationController when the Guide routes to them.
func registerOnDemandAgentCreators(reg *container.AgentCreatorRegistry, bus guide.EventBus) {
	// Librarian — codebase search specialist.
	// Requires a SearchSystem at construction. Since Librarian's search backend
	// is not yet initialized at bootstrap, the factory returns an error until
	// the search system is wired (tracked separately).
	reg.Register("librarian", func(_ context.Context) (container.ContainerAgent, error) {
		l, err := librarian.New(librarian.Config{})
		if err != nil {
			return nil, err
		}
		if startErr := l.Start(bus); startErr != nil {
			return nil, startErr
		}
		return l, nil
	})

	// Archivalist — historical data, patterns, failures.
	reg.Register("archivalist", func(_ context.Context) (container.ContainerAgent, error) {
		a, err := archivalist.New(archivalist.Config{
			AnthropicAPIKey: providers.ResolveAnthropicAPIKey(""),
		})
		if err != nil {
			return nil, err
		}
		if startErr := a.Start(bus); startErr != nil {
			return nil, startErr
		}
		return a, nil
	})

	// Academic — research, academic papers, best practices.
	reg.Register("academic", func(_ context.Context) (container.ContainerAgent, error) {
		a, err := academic.New(academic.Config{
			AnthropicAPIKey: providers.ResolveAnthropicAPIKey(""),
		})
		if err != nil {
			return nil, err
		}
		if startErr := a.Start(bus); startErr != nil {
			return nil, startErr
		}
		return a, nil
	})

	// Inspector — code review, validation.
	reg.Register("inspector", func(_ context.Context) (container.ContainerAgent, error) {
		i, err := inspector.New(inspector.InspectorConfig{})
		if err != nil {
			return nil, err
		}
		if startErr := i.Start(bus); startErr != nil {
			return nil, startErr
		}
		return i, nil
	})

	// Tester — test creation, execution.
	reg.Register("tester", func(_ context.Context) (container.ContainerAgent, error) {
		t, err := tester.New(tester.TesterConfig{})
		if err != nil {
			return nil, err
		}
		if startErr := t.Start(bus); startErr != nil {
			return nil, startErr
		}
		return t, nil
	})

	// Engineer — code implementation.
	reg.Register("engineer", func(_ context.Context) (container.ContainerAgent, error) {
		e, err := engineer.New(engineer.Config{})
		if err != nil {
			return nil, err
		}
		if startErr := e.Start(bus); startErr != nil {
			return nil, startErr
		}
		return e, nil
	})

	// Designer — design implementation.
	reg.Register("designer", func(_ context.Context) (container.ContainerAgent, error) {
		d, err := designer.New(designer.Config{})
		if err != nil {
			return nil, err
		}
		if startErr := d.Start(bus); startErr != nil {
			return nil, startErr
		}
		return d, nil
	})
}

// =============================================================================
// Lifecycle Hook Mutator
// =============================================================================

// lifecycleHookMutator is a SpecMutator that attaches PostStart registration
// and PreStop deregistration hooks to every container spec. The Guide reference
// is set after the Guide container is created (breaking the circular dep).
// Protected by sync.Mutex for the deferred Guide wiring.
type lifecycleHookMutator struct {
	mu         sync.Mutex
	guide      container.AgentRouter
	serviceReg *network.ServiceRegistry
}

// SetGuide sets the Guide reference for hook creation. Called after the Guide
// container is created by the DaemonSet controller. Thread-safe.
func (m *lifecycleHookMutator) SetGuide(g container.AgentRouter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.guide = g
}

// Mutate attaches lifecycle hooks to the container spec. Hooks use
// ContainerAwareHook to read the real agent ID at execution time,
// avoiding the spec-time vs runtime ID mismatch.
func (m *lifecycleHookMutator) Mutate(spec *container.ContainerSpec) error {
	m.mu.Lock()
	g := m.guide
	reg := m.serviceReg
	m.mu.Unlock()

	// PostStart — register with Guide routing + ServiceRegistry.
	// The hook implements ContainerAwareHook, so the HookRunner injects
	// the Container before Execute. This provides the real agent ID.
	spec.Hooks.PostStart = container.HookAction{
		Handler: container.NewPostStartRegistrationHook(container.PostStartRegistrationHookConfig{
			Guide:    g,
			Registry: reg,
		}),
	}

	// PreStop — deregister from Guide + ServiceRegistry.
	spec.Hooks.PreStop = container.HookAction{
		Handler: container.NewPreStopDeregistrationHook(g, reg),
	}

	return nil
}

// =============================================================================
// Agent Extraction
// =============================================================================

// extractAgent retrieves the first container of the given agent type from the
// registry and asserts its agent to the requested concrete type.
func extractAgent[T container.ContainerAgent](reg *container.ContainerRegistry, agentType string) (T, error) {
	var zero T
	containers := reg.ListByType(agentType)
	if len(containers) == 0 {
		return zero, fmt.Errorf("no container for agent type %q", agentType)
	}
	agent, ok := containers[0].Agent().(T)
	if !ok {
		return zero, fmt.Errorf("container agent type assertion failed for %q", agentType)
	}
	return agent, nil
}

// =============================================================================
// Quota Derivation
// =============================================================================

// asyncHookGoroutinesPerContainer is the number of goroutines the HookRunner
// spawns asynchronously per container start. Only RunPostStart uses runHookAsync
// (hooks.go:77-90); all other hooks run synchronously.
const asyncHookGoroutinesPerContainer int64 = 1

// runtimeOverhead returns the goroutine overhead the container runtime adds
// per container, derived from the container's spec:
//   - 1 goroutine per probe (ProbeRunner.startProbeLoop in probe.go)
//   - 1 goroutine for the async post-start hook (HookRunner.runHookAsync)
func runtimeOverhead(spec container.ContainerSpec) int64 {
	return int64(len(spec.Probes)) + asyncHookGoroutinesPerContainer
}

// quotaFromSpecs derives resource quota limits from the AgentSpecRegistry.
// Every value is computed from per-agent ContainerSpec data; no magic numbers.
//
// The quota must accommodate the worst-case concurrent resource usage:
//   - All agents simultaneously active (Hot tier)
//   - Restart overlap: agents with RestartOnFailure/RestartAlways may have
//     a dying container + a new container alive concurrently during restart
//   - Runtime overhead per container (probes + async hooks, from spec data)
func quotaFromSpecs(specReg *container.AgentSpecRegistry) container.ResourceQuotaConfig {
	all := specReg.Descriptors().All()
	agentCount := int64(len(all))

	var (
		totalGoroutines    int64
		totalContextWindow int64
		restartableCount   int64 // agents that may restart (concurrent old+new overlap)
		maxGoroutineLimit  int64 // largest single-agent goroutine budget (incl overhead)
		maxContextWindow   int64 // largest single-agent context window
	)

	for _, d := range all {
		spec, err := specReg.SpecForAgent(d.AgentType)
		if err != nil {
			continue
		}

		overhead := runtimeOverhead(spec)
		goroutines := spec.Resources.GoroutineLimit + overhead
		ctxWindow := int64(spec.Resources.ContextWindowLimit)

		totalGoroutines += goroutines
		totalContextWindow += ctxWindow

		if goroutines > maxGoroutineLimit {
			maxGoroutineLimit = goroutines
		}
		if ctxWindow > maxContextWindow {
			maxContextWindow = ctxWindow
		}

		if spec.RestartPolicy == container.RestartOnFailure || spec.RestartPolicy == container.RestartAlways {
			restartableCount++
		}
	}

	// Restart headroom: during restart, at most restartableCount agents
	// have overlapping old+new containers. Each overlap adds one extra
	// container's worth of goroutines (incl. runtime overhead). Use the
	// max per-agent total as the conservative upper bound per overlap slot.
	restartGoroutineHeadroom := restartableCount * maxGoroutineLimit

	// Context window: sum of all agent windows + one restart overlap.
	// During restart, at most one agent has both old (draining) and new
	// (starting) containers. The overlap is bounded by the single largest
	// context window.
	contextHeadroom := maxContextWindow

	return container.ResourceQuotaConfig{
		GoroutineLimit:     totalGoroutines + restartGoroutineHeadroom,
		ContextWindowLimit: totalContextWindow + contextHeadroom,
		ContainerLimit:     agentCount + restartableCount,
	}
}

// =============================================================================
// Agent Bootstrap Helpers (unchanged from before)
// =============================================================================

// bootstrapLiveGuide creates and starts a Guide with Gemini classification.
// If Gemini auth is unavailable, it falls back to a local rule-based classifier
// so the UI can launch without interactive authorization.
func bootstrapLiveGuide(ctx context.Context, bus guide.EventBus) (*guide.Guide, error) {
	googleCfg := defaultGuideGoogleConfig()
	cfg := guide.Config{
		Bus:          bus,
		AgentID:      "guide",
		SessionID:    "default",
		GoogleConfig: &googleCfg,
	}

	promptSkills := guide.DiscoverGuidePromptSkills()
	provider, err := providers.NewGoogleProvider(ctx, googleCfg, promptSkills...)
	if err == nil && provider != nil {
		g, newErr := guide.NewWithGeminiClient(provider, cfg)
		if newErr != nil {
			return nil, newErr
		}
		if startErr := g.Start(ctx); startErr != nil {
			return nil, startErr
		}
		return g, nil
	}

	g, newErr := guide.NewWithClassifier(guide.NewRuleClassifierClient(), cfg)
	if newErr != nil {
		return nil, newErr
	}
	if startErr := g.Start(ctx); startErr != nil {
		return nil, startErr
	}
	return g, nil
}

func bootstrapArchitect(bus guide.EventBus) (*architect.Architect, error) {
	cfg := architect.Config{
		EnableLLM:       true,
		Model:           architect.DefaultArchitectModel,
		AnthropicAPIKey: resolveArchitectAPIKey(),
	}
	a, err := architect.New(cfg)
	if err != nil {
		return nil, err
	}
	if err := a.Start(bus); err != nil {
		return nil, err
	}
	return a, nil
}

// resolveArchitectAPIKey resolves the Anthropic API key for the architect
// from the same credential chain the provider layer uses.
func resolveArchitectAPIKey() string {
	return providers.ResolveAnthropicAPIKey("")
}

func registerArchitectWithGuide(g *guide.Guide, a *architect.Architect) error {
	if g == nil || a == nil {
		return nil
	}
	if err := g.RegisterRouter(a); err != nil {
		return err
	}
	g.MarkAgentReady("architect")
	return nil
}

func buildAuthRefreshHook(g *guide.Guide, arch *architect.Architect, orch *orchestrator.Orchestrator) func(ctx context.Context, provider, method string) error {
	return func(ctx context.Context, provider, method string) error {
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "google":
			refreshErr := g.RefreshAuthWithMethod(ctx, method)
			refreshOrchestratorProvider(ctx, orch)
			return refreshErr
		case "anthropic":
			if arch != nil {
				arch.RefreshPlannerAuth()
			}
			return nil
		default:
			return nil
		}
	}
}

func defaultGuideGoogleConfig() providers.GoogleConfig {
	cfg := providers.DefaultGoogleConfig()
	cfg.Model = "gemini-3.1-pro-preview"
	return cfg
}

func defaultOrchestratorGoogleConfig() providers.GoogleConfig {
	cfg := providers.DefaultGoogleConfig()
	cfg.Model = "gemini-3-flash-preview"
	return cfg
}

func bootstrapOrchestrator(ctx context.Context, bus guide.EventBus, actBus *events.ActivityEventBus) (*orchestrator.Orchestrator, error) {
	googleCfg := defaultOrchestratorGoogleConfig()
	provider, provErr := providers.NewGoogleProvider(ctx, googleCfg)

	cfg := orchestrator.DefaultConfig()
	cfg.EnableLLM = provErr == nil && provider != nil
	if cfg.EnableLLM {
		cfg.GoogleConfig = &googleCfg
	}

	orch, err := orchestrator.New(cfg, provider, actBus, nil)
	if err != nil {
		return nil, err
	}
	if err := orch.Start(bus); err != nil {
		return nil, err
	}
	return orch, nil
}

func registerOrchestratorWithGuide(g *guide.Guide, orch *orchestrator.Orchestrator) error {
	if g == nil || orch == nil {
		return nil
	}
	if err := g.RegisterRouter(orch); err != nil {
		return err
	}
	g.MarkAgentReady("orchestrator")
	return nil
}

func refreshOrchestratorProvider(ctx context.Context, orch *orchestrator.Orchestrator) {
	if orch == nil {
		return
	}
	googleCfg := defaultOrchestratorGoogleConfig()
	provider, err := providers.NewGoogleProvider(ctx, googleCfg)
	if err != nil || provider == nil {
		return
	}
	orch.SetProvider(provider)
}

// =============================================================================
// Infrastructure Cleanup
// =============================================================================

// cleanupInfra tears down infrastructure on early bootstrap failure.
// Errors are logged rather than returned because this runs during error
// recovery — the caller already has a primary error to report.
func cleanupInfra(
	rt *container.DefaultRuntime,
	reg *container.ContainerRegistry,
	ns *network.NetworkNamespace,
	bus guide.EventBus,
	actBus *events.ActivityEventBus,
) {
	for _, c := range reg.All() {
		if c.IsRunning() {
			if err := rt.StopContainer(context.Background(), c); err != nil {
				slog.Warn("cleanup: stop container failed", "container", c.ID(), "error", err)
			}
		}
		if err := rt.RemoveContainer(context.Background(), c); err != nil {
			slog.Warn("cleanup: remove container failed", "container", c.ID(), "error", err)
		}
	}
	ns.Close()
	rt.Close()
	if err := bus.Close(); err != nil {
		slog.Warn("cleanup: bus close failed", "error", err)
	}
	actBus.Close()
}

// activationStorageDir returns the directory for Cool tier state files.
func activationStorageDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sylk", "activation"), nil
}

// =============================================================================
// Theme + Misc
// =============================================================================

func parseThemeMode(s string) ui.ThemeMode {
	if s == "light" {
		return ui.ThemeLight
	}
	return ui.ThemeDark
}

// =============================================================================
// Handoff Supervisor
// =============================================================================

// bootstrapHandoffSupervisor creates and starts the handoff supervisor, registers
// agent creators and live agents, and wires the agent-replacement callback.
// Returns nil if initialization fails (non-fatal — handoff is optional).
func bootstrapHandoffSupervisor(
	g *guide.Guide,
	arch *architect.Architect,
	orch *orchestrator.Orchestrator,
) *handoff.HandoffSupervisor {
	walDir, err := handoffWALDir()
	if err != nil {
		return nil
	}

	supervisorCfg := handoff.DefaultSupervisorConfig()
	supervisorCfg.WALDir = walDir
	supervisor := handoff.NewHandoffSupervisor(supervisorCfg)

	// Register agent creators so the supervisor can spawn replacements on handoff.
	supervisor.Factory().RegisterCreator("guide", func(_ context.Context) (handoff.HandoffableAgent, error) {
		return g, nil
	})
	if arch != nil {
		supervisor.Factory().RegisterCreator("architect", func(_ context.Context) (handoff.HandoffableAgent, error) {
			return arch, nil
		})
	}
	supervisor.Factory().RegisterCreator("orchestrator", func(_ context.Context) (handoff.HandoffableAgent, error) {
		return orch, nil
	})

	// Wire agent replacement: when handoff creates a new agent, re-register it
	// with the Guide's routing table.
	supervisor.SetAgentReplacedCallback(func(oldID, _ string, newAgent handoff.HandoffableAgent) error {
		g.UnregisterAgent(oldID)
		if router, ok := newAgent.(guide.AgentRouter); ok {
			if regErr := g.RegisterRouter(router); regErr != nil {
				return regErr
			}
			g.MarkAgentReady(newAgent.AgentID())
		}
		return nil
	})

	if startErr := supervisor.Start(); startErr != nil {
		return nil
	}

	// Register live agents — each gets a per-agent bridge.
	registerHandoffAgent(supervisor, g)
	if arch != nil {
		registerHandoffAgent(supervisor, arch)
	}
	registerHandoffAgent(supervisor, orch)

	return supervisor
}

// handoffWALDir returns the WAL directory path under the user's home .sylk directory.
func handoffWALDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sylk", "handoff"), nil
}

// handoffBridgeSetter is implemented by agents that accept a handoff bridge.
type handoffBridgeSetter interface {
	SetHandoffBridge(bridge *handoff.HandoffBridge)
}

// registerHandoffAgent registers a single agent with the supervisor and
// sets the bridge on the agent if it supports it.
func registerHandoffAgent(supervisor *handoff.HandoffSupervisor, agent handoff.HandoffableAgent) {
	bridge, err := supervisor.RegisterAgent(agent)
	if err != nil {
		return
	}
	if setter, ok := agent.(handoffBridgeSetter); ok {
		setter.SetHandoffBridge(bridge)
	}
}
