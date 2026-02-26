package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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
	inspectorGlobal "github.com/adalundhe/sylk/agents/inspector/global"
	inspectorPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspectorShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/librarian"
	"github.com/adalundhe/sylk/agents/orchestrator"
	globaltester "github.com/adalundhe/sylk/agents/tester/global"
	pipelinetester "github.com/adalundhe/sylk/agents/tester/pipeline"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/activation"
	"github.com/adalundhe/sylk/core/container/daemon"
	"github.com/adalundhe/sylk/core/container/network"
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/adalundhe/sylk/ui"
	"github.com/adalundhe/sylk/ui/fonts"
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

	// Resolve project root early so the parallel bootstrap can initialize
	// git resources concurrently with agent container creation.
	projectRoot := resolveProjectRoot()

	deps, cleanup, err := bootstrapDeps(ctx, tuiMock, projectRoot)
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
	cfg.ProjectRoot = projectRoot

	runErr := ui.Run(ctx, cfg, deps)
	stop()
	cleanupErr := cleanup()
	restoreErr := restoreStdLog()
	return errors.Join(runErr, cleanupErr, restoreErr)
}

// resolveProjectRoot finds the project root: git root or CWD.
func resolveProjectRoot() string {
	cwd, _ := os.Getwd()
	if root, err := boot.FindGitRoot(cwd); err == nil {
		return root
	}
	return cwd
}

// activityBusBuffer is the channel size for the activity event bus.
const activityBusBuffer = 1000

// shutdownTimeout bounds total cleanup time. Derived from the longest
// possible graceful stop (60s for max-context agents) + headroom.
const shutdownTimeout = 90 * time.Second

// =============================================================================
// Typed result structs for Phase 2 parallel bootstrap goroutines.
// =============================================================================

type daemonContainerResult struct {
	c   *container.Container
	err error
}

type activationCtrlResult struct {
	ctrl *activation.ActivationController
	err  error
}

type fontResult struct {
	detected bool
}

type gitBootResult struct {
	client  *git.GitClient
	watcher *git.StatusWatcher
	bus     *git.GitBus
	guard   *git.SafetyGuard
}

type hydrateResult struct{ hydrated *providers.HydratedGoogleAuth }

// hydrateOnceCell is a one-shot rendezvous: the hydration goroutine calls
// resolve() exactly once; consumers call result() and block until the value
// is available. After the first resolve, result() returns immediately.
type hydrateOnceCell struct {
	ch  chan struct{}
	val atomic.Pointer[providers.HydratedGoogleAuth]
}

func newHydrateOnce() *hydrateOnceCell {
	return &hydrateOnceCell{ch: make(chan struct{})}
}

func (h *hydrateOnceCell) resolve(v *providers.HydratedGoogleAuth) {
	h.val.Store(v)
	close(h.ch)
}

func (h *hydrateOnceCell) result() *providers.HydratedGoogleAuth {
	<-h.ch
	return h.val.Load()
}

// bootstrapDeps initializes the core systems needed by the TUI.
// Agents run inside containers managed by the container runtime.
// DaemonSets keep Guide and Orchestrator always-hot; on-demand agents
// activate via the ActivationController when the Guide routes requests.
//
// Startup is structured in 4 phases to maximize parallelism:
//
//	Phase 1: Infrastructure (sequential, ~15ms)
//	Phase 2: Parallel creation (Guide, Orch, ActivationCtrl, HealthSync, Fonts, Git)
//	Phase 3: Wiring (sequential, depends on Phase 2 results)
//	Phase 4: Post-wiring (Architect pre-activation, handoff, session)
func bootstrapDeps(ctx context.Context, mockMode bool, projectRoot string) (ui.Deps, func() error, error) {
	_ = mockMode
	start := time.Now()

	// Load .env.local before anything reads environment variables.
	// Existing env vars take priority — LoadDotenv never overrides.
	if err := boot.LoadDotenv(projectRoot); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to load .env.local", "error", err)
	}

	// =================================================================
	// Phase 1: Infrastructure (sequential, ~15ms)
	// =================================================================

	scope := concurrency.NewGoroutineScope(ctx, "tui", nil)
	scope.SetMaxLifetime(24 * time.Hour)

	activityBus := events.NewActivityEventBus(activityBusBuffer)
	activityBus.Start()

	guideBus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	streamMgr := guide.NewStreamManager(guide.DefaultStreamConfig())

	sessionMgr := session.NewManager(session.ManagerConfig{
		Scope: scope,
	})

	descriptors := handoff.NewDescriptorRegistry()
	pressureLevel := new(atomic.Int32)
	budget := concurrency.NewGoroutineBudget(pressureLevel)
	containerReg := container.NewContainerRegistry()
	serviceReg := network.NewServiceRegistry()
	specReg := container.NewAgentSpecRegistry(descriptors)
	quota := container.NewResourceQuota(quotaFromSpecs(specReg))

	creatorReg := container.NewAgentCreatorRegistry()

	hookMut := &lifecycleHookMutator{serviceReg: serviceReg}
	admission := container.NewAdmissionController(nil, []container.SpecMutator{hookMut})

	probeFact := &probeFactoryHolder{}

	runtime := container.NewDefaultRuntime(container.DefaultRuntimeConfig{
		Budget:       budget,
		Registry:     containerReg,
		Quota:        quota,
		Admission:    admission,
		CreateAgent:  creatorReg.Creator(),
		ProbeFactory: probeFact.Build,
		ParentCtx:    ctx,
	})

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

	// Prepare daemon specs for parallel container creation.
	daemonCtrl := daemon.NewDaemonSetController(runtime, containerReg)
	daemonSpecs := daemon.AgentDaemonSetSpecs(specReg)
	for _, spec := range daemonSpecs {
		daemonCtrl.Apply(spec)
	}
	daemonSpecMap := make(map[string]container.ContainerSpec, len(daemonSpecs))
	for _, spec := range daemonSpecs {
		daemonSpecMap[spec.Name] = spec.ContainerSpec
	}

	// =================================================================
	// Phase 1.5: Google config + gateways (hydration deferred to Phase 2)
	// =================================================================
	// Hydration runs as a Phase 2 goroutine. Guide and Orchestrator
	// factories block on hydrateOnce.result() — exactly one hydration
	// call, shared by all consumers. Non-daemon factories (restart /
	// on-demand) read hydratedRef atomically after the initial boot.

	guideCfg := defaultGuideGoogleConfig()
	hydrateOnce := newHydrateOnce()
	var hydratedRef atomic.Pointer[providers.HydratedGoogleAuth]

	// Create provider gateways for cross-agent rate limiting coordination.
	// Select rate limit profile based on actual auth mode, not hydration status.
	var googleGwCfg gateway.GatewayConfig
	if guideCfg.AuthMode == providers.GoogleAuthModeOAuth {
		googleGwCfg = gateway.DefaultGoogleOAuthConfig()
	} else {
		googleGwCfg = gateway.DefaultGoogleAPIKeyConfig()
	}
	googleGateway := gateway.NewProviderGateway(googleGwCfg, slog.Default())
	anthropicGateway := gateway.NewProviderGateway(gateway.DefaultAnthropicConfig(), slog.Default())
	openaiGateway := gateway.NewProviderGateway(gateway.DefaultOpenAIConfig(), slog.Default())

	// Register agent creators. During initial boot, Guide/Orch factories
	// call hydrateOnce.result() to wait for the shared hydration. After
	// boot, daemon restarts read hydratedRef atomically (already stored).
	registerAgentCreators(creatorReg, guideBus, activityBus, projectRoot, hydrateOnce, &hydratedRef, googleGateway, anthropicGateway, openaiGateway)

	slog.Info("bootstrap phase 1 complete", "elapsed", time.Since(start))

	// =================================================================
	// Phase 2: Parallel creation
	// =================================================================
	// Guide and Orchestrator are critical — any failure aborts startup.
	// ActivationCtrl, HealthSyncer, Fonts, and Git are non-critical.

	phase2Start := time.Now()

	// parallelCancel is a cancellation handle used to signal early abort
	// when a critical goroutine fails. Agent factories receive the
	// long-lived `ctx` (signal context) so their run-contexts survive
	// Phase 2 — the cancel context is never passed to them.
	_, parallelCancel := context.WithCancel(ctx)

	const (
		hydrateMaxAttempts = 3
		hydrateRetryDelay  = 500 * time.Millisecond
	)

	guideCh := make(chan daemonContainerResult, 1)
	orchCh := make(chan daemonContainerResult, 1)
	activationCh := make(chan activationCtrlResult, 1)
	healthCh := make(chan error, 1)
	fontCh := make(chan fontResult, 1)
	gitCh := make(chan gitBootResult, 1)
	hydrateCh := make(chan hydrateResult, 1)

	// A: Guide container — uses the long-lived `ctx` for CreateContainer
	// so the agent's run-context outlives Phase 2.
	go func() {
		spec, ok := daemonSpecMap["guide"]
		if !ok {
			guideCh <- daemonContainerResult{err: fmt.Errorf("no daemon spec for guide")}
			return
		}
		c, err := runtime.CreateContainer(ctx, spec, nil)
		if err != nil {
			guideCh <- daemonContainerResult{err: err}
			return
		}
		if err := runtime.StartContainer(ctx, c); err != nil {
			_ = runtime.RemoveContainer(ctx, c)
			guideCh <- daemonContainerResult{err: err}
			return
		}
		guideCh <- daemonContainerResult{c: c}
	}()

	// B: Orchestrator container
	go func() {
		spec, ok := daemonSpecMap["orchestrator"]
		if !ok {
			orchCh <- daemonContainerResult{err: fmt.Errorf("no daemon spec for orchestrator")}
			return
		}
		c, err := runtime.CreateContainer(ctx, spec, nil)
		if err != nil {
			orchCh <- daemonContainerResult{err: err}
			return
		}
		if err := runtime.StartContainer(ctx, c); err != nil {
			_ = runtime.RemoveContainer(ctx, c)
			orchCh <- daemonContainerResult{err: err}
			return
		}
		orchCh <- daemonContainerResult{c: c}
	}()

	// C: ActivationController
	go func() {
		activationPolicies := activation.AgentActivationPolicies(descriptors.All())
		storageDir, err := activationStorageDir()
		if err != nil {
			activationCh <- activationCtrlResult{err: err}
			return
		}
		ctrl, err := activation.NewActivationController(activation.ActivationControllerConfig{
			Runtime:    runtime,
			Registry:   containerReg,
			Scope:      scope,
			Policies:   activationPolicies,
			StorageDir: storageDir,
		})
		if err != nil {
			activationCh <- activationCtrlResult{err: err}
			return
		}
		if err := ctrl.Start(budget, quota); err != nil {
			activationCh <- activationCtrlResult{err: err}
			return
		}
		activationCh <- activationCtrlResult{ctrl: ctrl}
	}()

	// D: HealthSyncer
	go func() {
		syncer := container.NewHealthSyncer(container.HealthSyncerConfig{
			ContainerRegistry: containerReg,
			ServiceRegistry:   serviceReg,
			Scope:             scope,
		})
		healthCh <- syncer.Start()
	}()

	// E: Fonts
	go func() {
		fontCh <- fontResult{detected: fonts.Detected()}
	}()

	// F: Git
	go func() {
		var result gitBootResult
		gc, err := git.NewGitClient(projectRoot)
		if err != nil || !gc.IsGitRepo() {
			gitCh <- result
			return
		}
		result.client = gc
		result.bus = git.NewGitBus(gc)
		if sw, err := git.NewStatusWatcher(gc); err == nil {
			result.watcher = sw
		}
		if sg, err := git.NewSafetyGuard(gc, result.bus, git.DefaultSafetyConfig(), result.watcher); err == nil {
			result.guard = sg
		}
		gitCh <- result
	}()

	// G: Hydration — runs concurrently with all other Phase 2 goroutines.
	// Guide and Orch factories block on hydrateOnce.result() so all
	// three share a single hydration call. hydratedRef is stored for
	// daemon restart factories that read it atomically after boot.
	go func() {
		var h *providers.HydratedGoogleAuth
		for attempt := 1; attempt <= hydrateMaxAttempts; attempt++ {
			result, err := providers.HydrateGoogleAuth(ctx, guideCfg)
			if err == nil {
				h = result
				hydratedRef.Store(h)
				break
			}
			slog.Warn("google auth hydration attempt failed",
				"attempt", attempt,
				"max", hydrateMaxAttempts,
				"error", err)
			if attempt < hydrateMaxAttempts {
				select {
				case <-ctx.Done():
				case <-time.After(hydrateRetryDelay):
				}
			}
		}
		hydrateOnce.resolve(h)
		hydrateCh <- hydrateResult{hydrated: h}
	}()

	// Collect results. Use select with nil-channel disabling to drain
	// all 7 channels. Critical errors cancel remaining goroutines.
	var (
		guideResult daemonContainerResult
		orchResult  daemonContainerResult
		actResult   activationCtrlResult
		fontRes     fontResult
		gitRes      gitBootResult
		criticalErr error
	)

	for completed := 0; completed < 7; completed++ {
		select {
		case r := <-guideCh:
			guideResult = r
			if r.err != nil && criticalErr == nil {
				criticalErr = fmt.Errorf("guide container: %w", r.err)
				parallelCancel()
			}
			guideCh = nil

		case r := <-orchCh:
			orchResult = r
			if r.err != nil && criticalErr == nil {
				criticalErr = fmt.Errorf("orchestrator container: %w", r.err)
				parallelCancel()
			}
			orchCh = nil

		case r := <-activationCh:
			actResult = r
			if r.err != nil {
				slog.Warn("activation controller failed during parallel bootstrap", "error", r.err)
			}
			activationCh = nil

		case err := <-healthCh:
			if err != nil {
				slog.Warn("health syncer start failed", "error", err)
			}
			healthCh = nil

		case r := <-fontCh:
			fontRes = r
			fontCh = nil

		case r := <-gitCh:
			gitRes = r
			gitCh = nil

		case <-hydrateCh:
			hydrateCh = nil
		}
	}
	parallelCancel()

	if criticalErr != nil {
		// Clean up any containers that were successfully created.
		for _, c := range []*container.Container{guideResult.c, orchResult.c} {
			if c != nil {
				_ = runtime.StopContainer(context.Background(), c)
				_ = runtime.RemoveContainer(context.Background(), c)
			}
		}
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, criticalErr
	}

	// Inject pre-created containers into the DaemonSetController so the
	// periodic reconciler manages them going forward.
	daemonCtrl.InjectInstance("guide", guideResult.c)
	daemonCtrl.InjectInstance("orchestrator", orchResult.c)

	slog.Info("bootstrap phase 2 complete", "elapsed", time.Since(phase2Start))

	// =================================================================
	// Phase 3: Wiring (sequential, depends on Phase 2 results)
	// =================================================================

	phase3Start := time.Now()

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

	hookMut.SetGuide(g)
	probeFact.SetIsReady(g.IsAgentReady)

	if err := registerOrchestratorWithGuide(g, orch); err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("register orchestrator: %w", err)
	}

	activationCtrl := actResult.ctrl
	var activator guide.AgentActivator
	if activationCtrl != nil {
		activator = activation.NewControllerActivator(activationCtrl)
		g.SetActivator(activator)
	}
	g.SetServiceRegistry(serviceReg)
	g.SetProviderWrapper(googleGateway.Wrapper(gateway.PriorityUserInteractive))

	// Agent registrar: bridges on-demand activation → routing registration.
	// When the Guide activates an agent lazily (via ensureExplicitTargetReady),
	// the PostStartHook only marks readiness. This callback extracts the agent
	// from the container registry and registers its routing info (capabilities,
	// intents, channels) with the Guide.
	g.SetAgentRegistrar(func(agentType string) {
		containers := containerReg.ListByType(agentType)
		if len(containers) == 0 {
			return
		}
		agent := containers[0].Agent()
		if router, ok := agent.(guide.AgentRouter); ok {
			_ = registerAgentWithGuide(g, router, agentType)
		}
	})

	// AuthRegistry: centralized credential lifecycle. Publishes events to
	// the bus (consumed by Guide + Orchestrator) and walks the container
	// registry for on-demand agents (tester, engineer, designer, inspector).
	authProbe := buildAuthProbe()
	authPublisher := chainPublishers(
		buildAuthPublisher(guideBus),
		buildOnDemandAuthRefresher(containerReg),
	)
	authRegistry := credentials.NewAuthRegistry(authProbe, authPublisher, slog.Default())

	// Wire orchestrator to refresh its own Google provider on auth events.
	orch.SetProviderRefresher(func(refreshCtx context.Context) {
		refreshOrchestratorProvider(refreshCtx, orch, googleGateway)
	})

	orch.SetTaskRouter(orchestrator.NewTaskRouter(orchestrator.TaskRouterConfig{
		Bus:       guideBus,
		Scope:     scope,
		AgentID:   "orchestrator",
		SessionID: "default",
	}))
	if activator != nil {
		orch.SetActivator(activator)
	}

	slog.Info("bootstrap phase 3 complete", "elapsed", time.Since(phase3Start))

	// =================================================================
	// Phase 4: Critical path (session + seeds) + background activation
	// =================================================================

	phase4Start := time.Now()

	defaultSession, err := sessionMgr.Create(ctx, session.DefaultConfig())
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus, activityBus)
		return ui.Deps{}, nil, fmt.Errorf("default session: %w", err)
	}
	_ = sessionMgr.Switch(defaultSession.ID())

	orch.SignalReady()

	// Seeds use placeholder IDs (agent type names). The UI's
	// ensureAgent/promoteSeededAgent re-keys to UUIDs when activity
	// events arrive from pre-activated agents.
	seeds := []ui.AgentSeed{
		{ID: "architect", AgentType: "architect", Name: "Architect"},
		{ID: "inspector", AgentType: "inspector", Name: "Inspector"},
		{ID: "tester", AgentType: "tester", Name: "Tester"},
	}

	// Background Phase 4: agent pre-activations, handoff supervisor,
	// and auth probe run after deps are returned. Tracked by scope.Go
	// so the goroutine is bounded and cancelable.
	var supervisorRef atomic.Pointer[handoff.HandoffSupervisor]
	phase4Done := make(chan struct{})

	_ = scope.Go("phase4-background", 0, func(bgCtx context.Context) error {
		defer close(phase4Done)

		var wg sync.WaitGroup

		// A: Agent pre-activations (3 parallel goroutines)
		if activationCtrl != nil {
			wg.Add(3)
			go func() {
				defer wg.Done()
				if _, err := activationCtrl.EnsureActive(bgCtx, "architect"); err != nil {
					return
				}
				arch, err := extractAgent[*architect.Architect](containerReg, "architect")
				if err != nil {
					return
				}
				_ = registerArchitectWithGuide(g, arch)
			}()
			go func() {
				defer wg.Done()
				if _, err := activationCtrl.EnsureActive(bgCtx, "inspector"); err != nil {
					return
				}
				gi, err := extractAgent[*inspectorGlobal.GlobalInspector](containerReg, "inspector")
				if err != nil {
					return
				}
				_ = registerAgentWithGuide(g, gi, "inspector")
			}()
			go func() {
				defer wg.Done()
				if _, err := activationCtrl.EnsureActive(bgCtx, "tester"); err != nil {
					return
				}
				gt, err := extractAgent[*globaltester.GlobalTester](containerReg, "tester")
				if err != nil {
					return
				}
				_ = registerAgentWithGuide(g, gt, "tester")
			}()
		}

		// B: Handoff supervisor (tolerates nil arch)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sup := bootstrapHandoffSupervisor(g, nil, orch, serviceReg)
			if sup != nil {
				supervisorRef.Store(sup)
			}
		}()

		// C: Auth probe
		wg.Add(1)
		go func() {
			defer wg.Done()
			authRegistry.ProbeAll()
		}()

		wg.Wait()

		// Late-register architect with handoff supervisor if both succeeded.
		// HandoffAgentFactory uses sync.RWMutex — thread-safe after Start().
		if sup := supervisorRef.Load(); sup != nil {
			if arch, archErr := extractAgent[*architect.Architect](containerReg, "architect"); archErr == nil {
				sup.Factory().RegisterCreator("architect",
					func(_ context.Context) (handoff.HandoffableAgent, error) {
						return arch, nil
					})
				registerHandoffAgent(sup, arch)
			}
		}

		slog.Info("bootstrap phase 4 background complete", "elapsed", time.Since(phase4Start))
		return nil
	})

	slog.Info("bootstrap critical path complete", "elapsed", time.Since(start))

	// =================================================================
	// Cleanup — ordered teardown
	// =================================================================

	cleanup := func() error {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()

		// Wait for background Phase 4 before teardown.
		select {
		case <-phase4Done:
		case <-shutdownCtx.Done():
		}

		var errs []error

		if sup := supervisorRef.Load(); sup != nil {
			if stopErr := sup.Stop(); stopErr != nil {
				errs = append(errs, stopErr)
			}
		}

		if activationCtrl != nil {
			if shutErr := activationCtrl.Shutdown(shutdownCtx); shutErr != nil {
				errs = append(errs, shutErr)
			}
		}

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

		googleGateway.Stop()
		anthropicGateway.Stop()
		openaiGateway.Stop()

		if busErr := guideBus.Close(); busErr != nil {
			errs = append(errs, busErr)
		}
		activityBus.Close()

		return errors.Join(errs...)
	}

	deps := ui.Deps{
		ActivityBus:       activityBus,
		SessionManager:    sessionMgr,
		GuideBus:          guideBus,
		StreamManager:     streamMgr,
		Guide:             g,
		Scope:             scope,
		AuthRegistry:      authRegistry,
		NerdFontsDetected: fontRes.detected,
		GitClient:         gitRes.client,
		GitWatcher:        gitRes.watcher,
		GitBus:            gitRes.bus,
		SafetyGuard:       gitRes.guard,
		SeedAgents:        seeds,
	}

	return deps, cleanup, nil
}

// =============================================================================
// Agent Creator Registration
// =============================================================================

// registerAgentCreators populates the creator registry with factory closures
// for every agent type. Guide and Orchestrator block on hydrateOnce.result()
// during initial boot so all share a single hydration. After boot, daemon
// restart factories read hydratedRef atomically (already populated).
func registerAgentCreators(
	reg *container.AgentCreatorRegistry,
	bus guide.EventBus,
	actBus *events.ActivityEventBus,
	projectRoot string,
	hydrateOnce *hydrateOnceCell,
	hydratedRef *atomic.Pointer[providers.HydratedGoogleAuth],
	googleGw *gateway.ProviderGateway,
	anthropicGw *gateway.ProviderGateway,
	openaiGw *gateway.ProviderGateway,
) {
	// Guide — Gemini with rule-based fallback.
	// First call blocks on hydrateOnce; subsequent calls (daemon restart)
	// read hydratedRef which is already populated.
	reg.Register("guide", func(ctx context.Context) (container.ContainerAgent, error) {
		h := hydrateOnce.result()
		if h == nil {
			h = hydratedRef.Load()
		}
		return bootstrapLiveGuide(ctx, bus, actBus, h, googleGw)
	})

	// Architect — Anthropic LLM planner. Gets read-only DiskFileAccess.
	reg.Register("architect", func(_ context.Context) (container.ContainerAgent, error) {
		return bootstrapArchitect(bus, actBus, projectRoot, anthropicGw)
	})

	// Orchestrator — pipeline coordinator.
	reg.Register("orchestrator", func(ctx context.Context) (container.ContainerAgent, error) {
		h := hydrateOnce.result()
		if h == nil {
			h = hydratedRef.Load()
		}
		return bootstrapOrchestrator(ctx, bus, actBus, projectRoot, h, googleGw)
	})

	// On-demand agents — created lazily by the ActivationController.
	registerOnDemandAgentCreators(reg, bus, googleGw, anthropicGw, openaiGw)
}

// registerOnDemandAgentCreators registers factories for knowledge and pipeline agents.
// These are created lazily by the ActivationController when the Guide routes to them.
func registerOnDemandAgentCreators(
	reg *container.AgentCreatorRegistry,
	bus guide.EventBus,
	googleGw *gateway.ProviderGateway,
	anthropicGw *gateway.ProviderGateway,
	openaiGw *gateway.ProviderGateway,
) {
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

	// Global Inspector — cross-file architectural quality auditor.
	reg.Register("inspector", func(_ context.Context) (container.ContainerAgent, error) {
		anthropicCfg := providers.AnthropicConfig{
			BaseConfig: providers.BaseConfig{
				Model:     "claude-opus-4-6",
				MaxTokens: 16384,
			},
			AuthMode: resolveAnthropicAuthMode(),
		}
		provider, err := providers.NewAnthropicProvider(anthropicCfg)
		if err != nil {
			return nil, fmt.Errorf("global inspector provider: %w", err)
		}
		wrapped := anthropicGw.WrapProvider(provider, gateway.PriorityValidation)
		gi, err := inspectorGlobal.New(inspectorShared.GlobalInspectorConfig{}, wrapped)
		if err != nil {
			return nil, err
		}
		gi.SetProviderWrapper(anthropicGw.Wrapper(gateway.PriorityValidation))
		if startErr := gi.Start(bus); startErr != nil {
			return nil, startErr
		}
		return gi, nil
	})

	// Pipeline Inspector — per-task quality validation.
	reg.Register("inspector-pipeline", func(_ context.Context) (container.ContainerAgent, error) {
		anthropicCfg := providers.AnthropicConfig{
			BaseConfig: providers.BaseConfig{
				Model:     "claude-opus-4-6",
				MaxTokens: 16384,
			},
			AuthMode: resolveAnthropicAuthMode(),
		}
		provider, err := providers.NewAnthropicProvider(anthropicCfg)
		if err != nil {
			return nil, fmt.Errorf("pipeline inspector provider: %w", err)
		}
		wrapped := anthropicGw.WrapProvider(provider, gateway.PriorityValidation)
		pi, err := inspectorPipeline.New(inspectorShared.PipelineInspectorConfig{}, wrapped)
		if err != nil {
			return nil, err
		}
		if startErr := pi.Start(bus); startErr != nil {
			return nil, startErr
		}
		return pi, nil
	})

	// Global Tester — cross-pipeline SDET.
	// Provider creation is best-effort: when the OpenAI API key is missing the
	// tester starts in degraded mode — static conversation replies still work,
	// LLM-dependent paths return a clear error.
	reg.Register("tester", func(_ context.Context) (container.ContainerAgent, error) {
		openaiCfg := providers.OpenAIConfig{
			BaseConfig: providers.BaseConfig{
				Model:     "gpt-5.3-codex",
				MaxTokens: 16384,
			},
			ReasoningEffort: "xhigh",
			AuthMode:        "api_key",
		}
		var wrapped providers.Provider
		if provider, provErr := providers.NewOpenAIProvider(openaiCfg); provErr != nil {
			slog.Warn("tester: OpenAI provider unavailable, LLM features disabled", "error", provErr)
		} else {
			wrapped = openaiGw.WrapProvider(provider, gateway.PriorityValidation)
		}
		gt, err := globaltester.New(shared.GlobalTesterConfig{}, wrapped)
		if err != nil {
			return nil, err
		}
		gt.SetProviderWrapper(openaiGw.Wrapper(gateway.PriorityValidation))
		if startErr := gt.Start(bus); startErr != nil {
			return nil, startErr
		}
		return gt, nil
	})

	// Pipeline Tester — per-task QE.
	reg.Register("tester-pipeline", func(_ context.Context) (container.ContainerAgent, error) {
		openaiCfg := providers.OpenAIConfig{
			BaseConfig: providers.BaseConfig{
				Model:     "gpt-5.3-codex",
				MaxTokens: 16384,
			},
			ReasoningEffort: "xhigh",
			AuthMode:        "api_key",
		}
		provider, err := providers.NewOpenAIProvider(openaiCfg)
		if err != nil {
			return nil, fmt.Errorf("pipeline tester provider: %w", err)
		}
		wrapped := openaiGw.WrapProvider(provider, gateway.PriorityValidation)
		pt, err := pipelinetester.New(shared.PipelineTesterConfig{}, wrapped)
		if err != nil {
			return nil, err
		}
		if startErr := pt.Start(bus); startErr != nil {
			return nil, startErr
		}
		return pt, nil
	})

	// Engineer — code implementation.
	reg.Register("engineer", func(_ context.Context) (container.ContainerAgent, error) {
		openaiCfg := providers.OpenAIConfig{
			BaseConfig: providers.BaseConfig{
				Model:     "gpt-5.3-codex",
				MaxTokens: 16384,
			},
			ReasoningEffort: "xhigh",
			AuthMode:        "api_key",
		}
		engProvider, engProvErr := providers.NewOpenAIProvider(openaiCfg)
		if engProvErr != nil {
			return nil, fmt.Errorf("engineer provider: %w", engProvErr)
		}
		wrapped := openaiGw.WrapProvider(engProvider, gateway.PriorityExecution)
		e, err := engineer.New(engineer.Config{}, wrapped)
		if err != nil {
			return nil, err
		}
		e.SetProviderWrapper(openaiGw.Wrapper(gateway.PriorityExecution))
		if startErr := e.Start(bus); startErr != nil {
			return nil, startErr
		}
		return e, nil
	})

	// Designer — LLM-driven design implementation via Gemini 3.1 Pro Preview.
	reg.Register("designer", func(ctx context.Context) (container.ContainerAgent, error) {
		googleCfg := providers.DefaultGoogleConfig()
		googleCfg.Model = string(providers.Gemini31Pro)
		googleCfg.MaxTokens = 16384
		provider, err := providers.NewGoogleProvider(ctx, googleCfg)
		if err != nil {
			return nil, fmt.Errorf("designer google provider: %w", err)
		}
		wrapped := googleGw.WrapProvider(provider, gateway.PriorityExecution)
		d, err := designer.New(designer.Config{}, wrapped)
		if err != nil {
			return nil, err
		}
		d.SetProviderWrapper(googleGw.Wrapper(gateway.PriorityExecution))
		if startErr := d.Start(bus); startErr != nil {
			return nil, startErr
		}
		return d, nil
	})
}

// =============================================================================
// Probe Factory Holder
// =============================================================================

// probesPerContainer is the number of probes wired per container. Used by
// quota derivation in place of len(spec.Probes) because probes are attached
// by the ProbeFactory after spec mutation (not present at quota time).
const probesPerContainer int64 = 2

// probeFactoryHolder builds probe specs for containers. The Guide's
// IsAgentReady callback is set after the Guide container is created,
// breaking the circular dependency between probe construction and Guide.
type probeFactoryHolder struct {
	mu      sync.Mutex
	isReady func(string) bool
}

// SetIsReady wires the Guide's readiness callback. Thread-safe.
func (pf *probeFactoryHolder) SetIsReady(fn func(string) bool) {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	pf.isReady = fn
}

// Build constructs liveness and readiness probe specs for a container.
// Always produces a liveness probe. Produces a readiness probe only when
// the Guide's IsAgentReady callback has been wired.
func (pf *probeFactoryHolder) Build(agent container.ContainerAgent, spec *container.ContainerSpec) []container.ProbeSpec {
	probes := make([]container.ProbeSpec, 0, 2)
	probes = append(probes, container.LivenessProbeSpec(agent, spec.GracefulStop))

	pf.mu.Lock()
	isReady := pf.isReady
	pf.mu.Unlock()

	if isReady != nil {
		probes = append(probes, container.ReadinessProbeSpec(agent.AgentID(), isReady))
	}
	return probes
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
// per container:
//   - 1 goroutine per probe (ProbeRunner.startProbeLoop in probe.go)
//   - 1 goroutine for the async post-start hook (HookRunner.runHookAsync)
//
// Uses the constant probesPerContainer because the ProbeFactory attaches
// probes after spec mutation — spec.Probes is empty at quota derivation time.
func runtimeOverhead(_ container.ContainerSpec) int64 {
	return probesPerContainer + asyncHookGoroutinesPerContainer
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
// When hydrated is non-nil, it reuses pre-resolved auth (skipping duplicate
// OAuth + Code Assist setup). If Gemini auth is unavailable, it falls back to
// a local rule-based classifier so the UI can launch without authorization.
func bootstrapLiveGuide(ctx context.Context, bus guide.EventBus, actBus *events.ActivityEventBus, hydrated *providers.HydratedGoogleAuth, googleGw *gateway.ProviderGateway) (*guide.Guide, error) {
	googleCfg := defaultGuideGoogleConfig()
	cfg := guide.Config{
		Bus:          bus,
		ActivityBus:  actBus,
		AgentID:      "guide",
		SessionID:    "default",
		GoogleConfig: &googleCfg,
	}

	promptSkills := guide.DiscoverGuidePromptSkills(guide.GuideGoSkills())

	var provider *providers.GoogleProvider
	var err error
	if hydrated != nil {
		provider, err = providers.NewGoogleProviderFromHydrated(ctx, googleCfg, hydrated, promptSkills...)
	} else {
		provider, err = providers.NewGoogleProvider(ctx, googleCfg, promptSkills...)
	}
	if err == nil && provider != nil {
		wrapped := googleGw.WrapProvider(provider, gateway.PriorityUserInteractive)
		g, newErr := guide.NewWithGeminiClient(wrapped, cfg)
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

func bootstrapArchitect(bus guide.EventBus, actBus *events.ActivityEventBus, projectRoot string, anthropicGw *gateway.ProviderGateway) (*architect.Architect, error) {
	cfg := architect.Config{
		EnableLLM:       true,
		Model:           architect.DefaultArchitectModel,
		AnthropicAPIKey: resolveArchitectAPIKey(),
		FileAccess:      versioning.NewDiskFileAccess(projectRoot, true),
		ActivityBus:     actBus,
		PlannerProviderWrapper: func(p *providers.AnthropicProvider) architect.PlannerStreamProvider {
			return anthropicGw.WrapProvider(p, gateway.PriorityPlanning)
		},
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
	g.MarkAgentReady(a.GetRoutingInfo().ID)
	return nil
}

// registerAgentWithGuide registers any AgentRouter with the Guide and marks it ready.
func registerAgentWithGuide(g *guide.Guide, router guide.AgentRouter, _ string) error {
	if g == nil || router == nil {
		return nil
	}
	if err := g.RegisterRouter(router); err != nil {
		return err
	}
	// Use the agent's own routing ID (UUID) — Register() stores
	// readyAgents keyed by info.ID, not by agentType.
	g.MarkAgentReady(router.GetRoutingInfo().ID)
	return nil
}

// buildAuthPublisher creates an AuthPublisher that broadcasts credential
// changes over the event bus. DaemonSet agents (Guide, Orchestrator)
// subscribe to this topic directly.
func buildAuthPublisher(bus guide.EventBus) credentials.AuthPublisher {
	return func(event credentials.AuthEvent) {
		msg := guide.NewAuthChangedMessage("", guide.AuthEventPayload{
			ProviderType: event.ProviderType,
			AuthMethod:   event.AuthMethod,
			Available:    event.Available,
		})
		_ = bus.Publish(guide.TopicAuthCredentials, msg)
	}
}

// buildOnDemandAuthRefresher creates a publisher that walks the container
// registry and refreshes on-demand agents matching the event's provider type.
func buildOnDemandAuthRefresher(containerReg *container.ContainerRegistry) credentials.AuthPublisher {
	return func(event credentials.AuthEvent) {
		if !event.Available {
			return
		}
		for _, c := range containerReg.All() {
			agent := c.Agent()
			refreshable, ok := agent.(container.AuthRefreshable)
			if !ok || refreshable.ProviderType() != event.ProviderType {
				continue
			}
			if err := refreshable.RefreshProvider(context.Background()); err != nil {
				slog.Warn("on-demand auth refresh failed",
					"agent", c.ID(),
					"provider", event.ProviderType,
					"error", err)
			}
		}
	}
}

// buildAuthProbe creates a probe function that checks whether credentials
// are available for a given provider type using environment variables and
// the secure credential store. Mirrors the resolution logic used by each
// provider's hydration path.
func buildAuthProbe() credentials.AuthProbe {
	// Pre-resolve the credential manager so the closure can check the
	// secure store (keychain / encrypted file) in addition to env vars.
	var credManager *credentials.Manager
	if dirs, dirErr := storage.ResolveDirs(); dirErr == nil && dirs != nil {
		credManager, _ = credentials.NewManager(dirs, "default")
	}

	return func(providerType string) bool {
		switch providerType {
		case "google":
			if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "" {
				return true
			}
			return probeSecureKey(credManager, "google")
		case "anthropic":
			return providers.ResolveAnthropicAPIKey("") != ""
		case "openai":
			if os.Getenv("OPENAI_API_KEY") != "" {
				return true
			}
			return probeSecureKey(credManager, "openai")
		default:
			return false
		}
	}
}

// probeSecureKey checks the secure credential store for a stored API key.
func probeSecureKey(mgr *credentials.Manager, provider string) bool {
	if mgr == nil {
		return false
	}
	key, err := mgr.GetAPIKey(provider)
	return err == nil && key != ""
}

// chainPublishers combines multiple AuthPublishers into one.
func chainPublishers(publishers ...credentials.AuthPublisher) credentials.AuthPublisher {
	return func(event credentials.AuthEvent) {
		for _, pub := range publishers {
			pub(event)
		}
	}
}

func resolveAnthropicAuthMode() string {
	if pref := credentials.LoadAuthPref("anthropic"); pref != "" {
		return normalizeAuthPref(pref)
	}
	return providers.AnthropicAuthModeAPIKey
}

func defaultGuideGoogleConfig() providers.GoogleConfig {
	cfg := providers.DefaultGoogleConfig()
	cfg.Model = "gemini-3.1-pro-preview"
	if pref := credentials.LoadAuthPref("google"); pref != "" {
		cfg.AuthMode = normalizeAuthPref(pref)
	}
	return cfg
}

func defaultOrchestratorGoogleConfig() providers.GoogleConfig {
	cfg := providers.DefaultGoogleConfig()
	cfg.Model = "gemini-3-flash-preview"
	if pref := credentials.LoadAuthPref("google"); pref != "" {
		cfg.AuthMode = normalizeAuthPref(pref)
	}
	return cfg
}

// normalizeAuthPref maps login panel method labels to provider auth mode
// constants. The login panel stores "apikey" (no underscore) but the
// provider constants use "api_key" (with underscore).
func normalizeAuthPref(pref string) string {
	switch pref {
	case "apikey":
		return "api_key"
	default:
		return pref
	}
}

func bootstrapOrchestrator(ctx context.Context, bus guide.EventBus, actBus *events.ActivityEventBus, projectRoot string, hydrated *providers.HydratedGoogleAuth, googleGw *gateway.ProviderGateway) (*orchestrator.Orchestrator, error) {
	googleCfg := defaultOrchestratorGoogleConfig()

	// Best-effort provider creation. If Google auth isn't available yet,
	// the orchestrator starts in LLM-ready mode and activates when the
	// user authorizes later (via SetProvider from the auth refresh hook).
	provider, provErr := resolveOrchestratorGoogleProvider(ctx, googleCfg, hydrated)
	if provErr != nil {
		slog.Warn("orchestrator google provider deferred — will activate on auth",
			"error", provErr,
			"auth_mode", googleCfg.AuthMode)
	}

	cfg := orchestrator.DefaultConfig()
	cfg.EnableLLM = true
	if provErr == nil && provider != nil {
		cfg.GoogleConfig = &googleCfg
	}

	sd := sylkdir.New(projectRoot)
	if err := sd.Init(); err != nil {
		return nil, fmt.Errorf("orchestrator sylkdir init: %w", err)
	}

	var orchProvider orchestrator.OrchestratorProvider
	if provider != nil {
		orchProvider = googleGw.WrapProvider(provider, gateway.PriorityUserInteractive)
	}
	orch, err := orchestrator.New(cfg, orchProvider, actBus, sd)
	if err != nil {
		return nil, err
	}
	if err := orch.Start(bus); err != nil {
		return nil, err
	}
	return orch, nil
}

// resolveOrchestratorGoogleProvider creates the Google provider for the
// orchestrator. Tries the hydrated auth first, then the configured auth mode,
// then falls back to API key mode if the configured mode fails.
func resolveOrchestratorGoogleProvider(ctx context.Context, cfg providers.GoogleConfig, hydrated *providers.HydratedGoogleAuth) (*providers.GoogleProvider, error) {
	// Best case: shared hydrated auth from Phase 1.5.
	if hydrated != nil {
		provider, err := providers.NewGoogleProviderFromHydrated(ctx, cfg, hydrated)
		if err == nil {
			return provider, nil
		}
		slog.Debug("orchestrator hydrated provider failed, trying fresh", "error", err)
	}

	// Try the configured auth mode (typically OAuth).
	provider, err := providers.NewGoogleProvider(ctx, cfg)
	if err == nil {
		return provider, nil
	}

	// If the configured mode was not API key, retry with API key mode.
	// The user may have GEMINI_API_KEY or GOOGLE_API_KEY set.
	if cfg.AuthMode != providers.GoogleAuthModeAPIKey {
		apiKeyCfg := cfg
		apiKeyCfg.AuthMode = providers.GoogleAuthModeAPIKey
		provider, apiKeyErr := providers.NewGoogleProvider(ctx, apiKeyCfg)
		if apiKeyErr == nil {
			return provider, nil
		}
		// Return the original error — it's more informative.
	}

	return nil, err
}

func registerOrchestratorWithGuide(g *guide.Guide, orch *orchestrator.Orchestrator) error {
	if g == nil || orch == nil {
		return nil
	}
	if err := g.RegisterRouter(orch); err != nil {
		return err
	}
	g.MarkAgentReady(orch.GetRoutingInfo().ID)
	return nil
}

func refreshOrchestratorProvider(ctx context.Context, orch *orchestrator.Orchestrator, googleGw *gateway.ProviderGateway) {
	if orch == nil {
		return
	}
	googleCfg := defaultOrchestratorGoogleConfig()
	provider, err := resolveOrchestratorGoogleProvider(ctx, googleCfg, nil)
	if err != nil || provider == nil {
		slog.Warn("orchestrator provider refresh failed", "error", err)
		return
	}
	orch.SetProvider(googleGw.WrapProvider(provider, gateway.PriorityUserInteractive))
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
	serviceReg *network.ServiceRegistry,
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

	// Wire agent replacement: during gradual shift, the new agent is already
	// registered by onShiftBegin — only unregister the old one.
	supervisor.SetAgentReplacedCallback(func(oldID, _ string, _ handoff.HandoffableAgent) error {
		g.UnregisterAgent(oldID)
		return nil
	})

	// Wire quality publisher: GP quality flows to the service registry.
	supervisor.SetQualityPublisher(func(agentID string, quality, stdDev float64) {
		serviceReg.UpdateQuality(agentID, quality, stdDev)
	})

	// Wire traffic shift callbacks.
	supervisor.SetTrafficShiftCallbacks(
		func(oldID string, newAgent handoff.HandoffableAgent) {
			// Register new agent alongside old in Guide's routing table.
			if router, ok := newAgent.(guide.AgentRouter); ok {
				_ = g.RegisterRouter(router)
				g.MarkAgentReady(newAgent.AgentID())
			}
			// Start with zero weight — the shift controller sets the initial.
			serviceReg.UpdateWeight(newAgent.AgentID(), 0)
		},
		func(agentID string, weight float64) {
			serviceReg.UpdateWeight(agentID, weight)
		},
	)

	// Wire Guide's quality checker via adapter.
	g.SetServiceQualityRegistry(&serviceRegistryQualityAdapter{reg: serviceReg})

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

// serviceRegistryQualityAdapter adapts ServiceRegistry to the Guide's
// ServiceQualityChecker interface, avoiding circular imports.
type serviceRegistryQualityAdapter struct {
	reg *network.ServiceRegistry
}

func (a *serviceRegistryQualityAdapter) HasHealthyEndpoints(agentType string) bool {
	return a.reg.HasHealthyEndpoints(agentType)
}

func (a *serviceRegistryQualityAdapter) GetWeightedEndpoints(agentType string) []guide.QualityEndpoint {
	endpoints := a.reg.GetWeightedEndpoints(agentType)
	result := make([]guide.QualityEndpoint, len(endpoints))
	for i := range endpoints {
		result[i] = guide.QualityEndpoint{
			AgentID:   endpoints[i].AgentID,
			AgentType: endpoints[i].AgentType,
			Quality:   endpoints[i].Quality,
			StdDev:    endpoints[i].StdDev,
			Weight:    endpoints[i].Weight,
		}
	}
	return result
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
