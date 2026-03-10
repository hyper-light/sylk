package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/adalundhe/sylk/agents/guardian"
	"github.com/adalundhe/sylk/agents/guide"
	inspectorGlobal "github.com/adalundhe/sylk/agents/inspector/global"
	inspectorPipeline "github.com/adalundhe/sylk/agents/inspector/pipeline"
	inspectorShared "github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/agents/librarian"
	"github.com/adalundhe/sylk/agents/orchestrator"
	"github.com/adalundhe/sylk/agents/scribe"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	globaltester "github.com/adalundhe/sylk/agents/tester/global"
	pipelinetester "github.com/adalundhe/sylk/agents/tester/pipeline"
	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/activation"
	"github.com/adalundhe/sylk/core/container/daemon"
	"github.com/adalundhe/sylk/core/container/network"
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/security"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/adalundhe/sylk/ui"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/fonts"
	"github.com/blevesearch/bleve/v2"
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

// busReadinessPublisher adapts knowledge.ReadinessPublisher to the guide EventBus.
type busReadinessPublisher struct {
	bus guide.EventBus
}

func (p *busReadinessPublisher) PublishKnowledgeReady(event knowledge.ReadinessEvent) {
	msg := guide.NewKnowledgeReadyMessage(&guide.KnowledgeReadyPayload{
		Level:     int(event.Level),
		Searchers: event.Searchers,
	})
	_ = p.bus.Publish(guide.TopicKnowledgeReady, msg)
}

// globalBleveAdapter implements query.BleveIndex by bridging to the
// GlobalVersionBleveStore's raw bleve.Index.
type globalBleveAdapter struct {
	store *sylkdir.GlobalVersionBleveStore
}

// SearchInContext implements query.BleveIndex.
func (a *globalBleveAdapter) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	idx := a.store.RawIndex()
	if idx == nil {
		return &bleve.SearchResult{}, nil
	}
	return idx.SearchInContext(ctx, req)
}

// bleveStoreCloser wraps GlobalVersionBleveStore.CloseAll as io.Closer.
type bleveStoreCloser struct {
	store *sylkdir.GlobalVersionBleveStore
}

func (c *bleveStoreCloser) Close() error { return c.store.CloseAll() }

// buildBleveSearcher opens Bleve at the HEAD version and returns a ready-to-use
// BleveSearcher plus a Closer for cleanup. Called inside the boot goroutine.
func buildBleveSearcher(projectRoot string) (*query.BleveSearcher, io.Closer, error) {
	sd := sylkdir.New(projectRoot)
	meta := sylkdir.NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		return nil, nil, fmt.Errorf("load global meta: %w", err)
	}
	head := meta.GetHead()

	bleveStore := sylkdir.NewGlobalVersionBleveStore(sd, head)
	if err := bleveStore.OpenHead(); err != nil {
		return nil, nil, fmt.Errorf("open head bleve: %w", err)
	}

	adapter := &globalBleveAdapter{store: bleveStore}
	searcher := query.NewBleveSearcher(adapter)
	closer := &bleveStoreCloser{store: bleveStore}

	return searcher, closer, nil
}

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

	guideBus := guide.NewChannelBus(guide.DefaultChannelBusConfig())

	// Route all activity through a metadata-caching wrapper so follow-on LLM/tool
	// telemetry can be attached to the correct agent row even when the source
	// event omits type or pipeline metadata.
	activityPub := events.NewMetadataCachingPublisher(
		guide.NewBusActivityPublisher(guideBus),
		map[string]events.AgentIdentityMetadata{
			"guide":              {AgentType: "guide", AgentName: "Guide"},
			"architect":          {AgentType: "architect", AgentName: "Architect"},
			"guardian":           {AgentType: "guardian", AgentName: "Guardian"},
			"orchestrator":       {AgentType: "orchestrator", AgentName: "Orchestrator"},
			"inspector":          {AgentType: "inspector", AgentName: "Inspector"},
			"tester":             {AgentType: "tester", AgentName: "Tester"},
			"librarian":          {AgentType: "librarian", AgentName: "Librarian"},
			"archivalist":        {AgentType: "archivalist", AgentName: "Archivalist"},
			"academic":           {AgentType: "academic", AgentName: "Academic"},
			"engineer":           {AgentType: "engineer", AgentName: "Engineer"},
			"designer":           {AgentType: "designer", AgentName: "Designer"},
			"inspector-pipeline": {AgentType: "inspector-pipeline", AgentName: "Inspector"},
			"tester-pipeline":    {AgentType: "tester-pipeline", AgentName: "Tester"},
		},
	)
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

	// AuthRegistry is the centralized runtime auth source. Seed it before
	// provider bootstrap so factories read canonical provider auth modes
	// from the registry instead of direct config-file lookups.
	authResolver := buildAuthResolver()
	authPublisher := chainPublishers(
		buildAuthPublisher(guideBus),
		buildOnDemandAuthRefresher(containerReg),
	)
	authRegistry := credentials.NewAuthRegistry(authResolver, nil, authPublisher, slog.Default())
	authRegistry.PrimeAll()

	guideCfg := defaultGuideGoogleConfig(authRegistry)
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

	// Wire LLM usage telemetry: every provider call that flows through a
	// gateway publishes an ActivityEvent so the Guardian's HealthMonitor
	// can track token consumption and cost.
	llmEventHook := providers.NewLLMEventPublisherHook(
		providers.NewLLMEventPublisher(activityPub),
	)
	googleGateway.SetEventHook(llmEventHook)
	anthropicGateway.SetEventHook(llmEventHook)
	openaiGateway.SetEventHook(llmEventHook)

	// Identity registry: generates canonical UUID8s per agent type at
	// registration time so IDs are sticky across spin-up/spin-down cycles.
	identityReg := container.NewAgentIdentityRegistry([]string{
		"architect", "engineer", "designer", "inspector", "tester",
		"inspector-pipeline", "tester-pipeline",
		"librarian", "archivalist", "academic", "orchestrator",
	})

	// actCtrlRef holds the activation controller pointer for lazy factory
	// closures. It is set after the controller is created in Phase 2;
	// factory closures read it when the agent is first activated.
	var actCtrlRef atomic.Pointer[activation.ActivationController]

	// gitSubsRef holds git subsystem results for the Guardian factory
	// closure. On initial boot (Phase 2), the git goroutine hasn't
	// completed yet so the factory reads nil → Guardian starts without
	// git. On daemon restart (reconciler-driven), the reference is
	// populated → Guardian starts fully wired immediately.
	var gitSubsRef atomic.Pointer[gitBootResult]

	// Plan store — process-lifetime singleton surviving architect demotion/re-promotion.
	planLeaseManager := architect.NewPlanLeaseManager(architect.DefaultLLMRequestTimeout, architect.ReadyPlanMaxAge)
	planStore := architect.NewPlanStore(projectRoot, planLeaseManager, slog.Default())

	// Knowledge store — process-lifetime singleton that owns the
	// HybridQueryCoordinator. Created with nil searchers; backends
	// are hot-swapped as boot phases complete.
	knowledgeStore := knowledge.NewKnowledgeStore(
		&busReadinessPublisher{bus: guideBus},
		slog.Default(),
	)

	// guideRef stores the live Guide instance for the Guardian factory —
	// on daemon restart the Guardian needs to seed its agent registry.
	var guideRef atomic.Pointer[guide.Guide]

	// guardianRef stores the live Guardian instance for Academic fetch
	// inspection wiring and post-boot daemon restarts.
	var guardianRef atomic.Pointer[guardian.Guardian]

	// orchRef stores the live Orchestrator instance for Guardian VFS
	// observability — the VFS adapters query active session VFS sets.
	var orchRef atomic.Pointer[orchestrator.Orchestrator]

	// quarantineRef stores the shared external-content quarantine buffer.
	// Academic fetch pipelines reuse this so Guardian inspection and
	// quarantine status observe the same entries.
	var quarantineRef atomic.Pointer[fetch.QuarantineBuffer]

	// Register agent creators. During initial boot, Guide/Orch factories
	// call hydrateOnce.result() to wait for the shared hydration. After
	// boot, daemon restarts read hydratedRef atomically (already stored).
	registerAgentCreators(creatorReg, identityReg, guideBus, activityPub, projectRoot, hydrateOnce, &hydratedRef, googleGateway, anthropicGateway, openaiGateway, authRegistry, &actCtrlRef, &gitSubsRef, planStore, knowledgeStore, daemonCtrl, &guideRef, &guardianRef, &orchRef, &quarantineRef, budget)

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
	guardianCh := make(chan daemonContainerResult, 1)
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
		c, err := runtime.CreateContainer(ctx, spec)
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
		c, err := runtime.CreateContainer(ctx, spec)
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

	// B2: Guardian container (non-critical)
	go func() {
		spec, ok := daemonSpecMap["guardian"]
		if !ok {
			guardianCh <- daemonContainerResult{err: fmt.Errorf("no daemon spec for guardian")}
			return
		}
		c, err := runtime.CreateContainer(ctx, spec)
		if err != nil {
			guardianCh <- daemonContainerResult{err: err}
			return
		}
		if err := runtime.StartContainer(ctx, c); err != nil {
			_ = runtime.RemoveContainer(ctx, c)
			guardianCh <- daemonContainerResult{err: err}
			return
		}
		guardianCh <- daemonContainerResult{c: c}
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
		gitSubsRef.Store(&result)
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
	// all 8 channels. Critical errors cancel remaining goroutines.
	var (
		guideResult    daemonContainerResult
		orchResult     daemonContainerResult
		guardianResult daemonContainerResult
		actResult      activationCtrlResult
		fontRes        fontResult
		gitRes         gitBootResult
		criticalErr    error
	)

	for completed := 0; completed < 8; completed++ {
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

		case r := <-guardianCh:
			guardianResult = r
			if r.err != nil {
				slog.Warn("guardian container failed during parallel bootstrap", "error", r.err)
			}
			guardianCh = nil

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
		cleanupInfra(runtime, containerReg, namespace, guideBus)
		return ui.Deps{}, nil, criticalErr
	}

	// Inject pre-created containers into the DaemonSetController so the
	// periodic reconciler manages them going forward.
	daemonCtrl.InjectInstance("guide", guideResult.c)
	daemonCtrl.InjectInstance("orchestrator", orchResult.c)
	if guardianResult.err == nil && guardianResult.c != nil {
		daemonCtrl.InjectInstance("guardian", guardianResult.c)
	}

	slog.Info("bootstrap phase 2 complete", "elapsed", time.Since(phase2Start))

	// =================================================================
	// Phase 3: Wiring (sequential, depends on Phase 2 results)
	// =================================================================

	phase3Start := time.Now()

	g, err := extractAgent[*guide.Guide](containerReg, "guide")
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus)
		return ui.Deps{}, nil, fmt.Errorf("extract guide: %w", err)
	}
	guideRef.Store(g) // Publish for Guardian factory on daemon restart.
	orch, err := extractAgent[*orchestrator.Orchestrator](containerReg, "orchestrator")
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus)
		return ui.Deps{}, nil, fmt.Errorf("extract orchestrator: %w", err)
	}
	orchRef.Store(orch) // Publish for Guardian VFS adapter queries.

	hookMut.SetGuide(g)
	probeFact.SetIsReady(g.IsAgentReady)

	if err := registerOrchestratorWithGuide(g, orch); err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus)
		return ui.Deps{}, nil, fmt.Errorf("register orchestrator: %w", err)
	}

	// Wire Guardian: extract from container, register with Guide, wire git subsystems.
	if guardianResult.err == nil && guardianResult.c != nil {
		if grd, grdErr := extractAgent[*guardian.Guardian](containerReg, "guardian"); grdErr == nil {
			guardianRef.Store(grd)
			_ = registerAgentWithGuide(g, grd, "guardian")
			if gitRes.bus != nil {
				grd.SetGitSubsystems(gitRes.bus, gitRes.watcher)
			}
		}
	}

	// Pre-register routing metadata for all non-pipeline agents so the
	// classifier can see them before their containers spin up.
	preRegisterAgentRouting(g, identityReg)

	// Seed Guardian's known-agents from the Guide's current registry so it
	// doesn't start with total=0. Handles both initial boot (where
	// pre-registered agents have no live announcements) and daemon restart
	// (where prior announcements were missed).
	if guardianResult.err == nil && guardianResult.c != nil {
		if grd, grdErr := extractAgent[*guardian.Guardian](containerReg, "guardian"); grdErr == nil {
			grd.SeedKnownAgents(g.RegisteredAgentInfos())
		}
	}

	activationCtrl := actResult.ctrl
	if activationCtrl != nil {
		actCtrlRef.Store(activationCtrl)
	}

	// Adopt bootstrap daemon containers so their activation entries start
	// at TierHot. Without this, EnsureActive sees TierCold and cold-starts
	// a duplicate instance.
	if activationCtrl != nil {
		if _, err := activationCtrl.AdoptContainer("guide", guideResult.c); err != nil {
			slog.Warn("adopt guide container", "error", err)
		}
		if _, err := activationCtrl.AdoptContainer("orchestrator", orchResult.c); err != nil {
			slog.Warn("adopt orchestrator container", "error", err)
		}
		if guardianResult.err == nil && guardianResult.c != nil {
			if _, err := activationCtrl.AdoptContainer("guardian", guardianResult.c); err != nil {
				slog.Warn("adopt guardian container", "error", err)
			}
		}
	}

	// Wire guardian observability dependencies (activation + daemon controllers).
	if guardianResult.err == nil && guardianResult.c != nil {
		if grd, grdErr := extractAgent[*guardian.Guardian](containerReg, "guardian"); grdErr == nil {
			var acQuerier guardian.ActivationQuerier
			var amQuerier guardian.ActivationMetricsQuerier
			if activationCtrl != nil {
				acQuerier = activationCtrl
				amQuerier = &activationMetricsAdapter{m: activationCtrl.Metrics()}
			}
			grd.SetObservabilityDeps(acQuerier, amQuerier, &daemonQuerierAdapter{dc: daemonCtrl})
			grd.SetVFSDeps(
				&orchestratorCVSAdapter{orchRef: &orchRef},
				&orchestratorVFSAdapter{orchRef: &orchRef},
			)
			grd.SetExtendedObservabilityDeps(
				&pipelineQuerierAdapter{orchRef: &orchRef},
				&gatewayQuerierAdapter{gateways: []*gateway.ProviderGateway{googleGateway, anthropicGateway, openaiGateway}},
				&knowledgeQuerierAdapter{store: knowledgeStore},
				&concurrencyQuerierAdapter{budget: budget},
			)
			grd.SetCostCalculator(buildCostCalculator())
		}
	}

	var activator guide.PodActivator
	if activationCtrl != nil {
		activator = activation.NewControllerPodActivator(activationCtrl)
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

	// Wire orchestrator to refresh its own Google provider on auth events.
	orch.SetProviderRefresher(func(refreshCtx context.Context, authMethod string) {
		refreshOrchestratorProvider(refreshCtx, orch, googleGateway, authRegistry, authMethod)
	})

	orch.SetTaskRouter(orchestrator.NewTaskRouter(orchestrator.TaskRouterConfig{
		Bus:       guideBus,
		Scope:     scope,
		AgentID:   orch.AgentID(),
		SessionID: "default",
	}))
	if activator != nil {
		orch.SetActivator(activator)
	}

	// PipelineRegistrar: when a PipelinePod activates agents for a DAG,
	// this callback extracts each agent from the container registry and
	// registers its routing info with the Guide.
	orch.SetRegistrar(func(ctx context.Context, podID, agentType string) error {
		c := findRegisteredPodContainer(containerReg, podID, agentType)
		if c == nil {
			return fmt.Errorf("no container for pod %s agent type %s after activation", podID, agentType)
		}
		router, ok := c.Agent().(guide.AgentRouter)
		if !ok {
			return nil
		}
		info := router.GetRoutingInfo()
		if isTaskScopedPipelineWorker(podID, agentType) {
			info = taskScopedWorkerRoutingInfo(info, podID, agentType)
		}
		return registerRoutingInfoWithGuide(g, info)
	})
	orch.SetTaskPodInfra(runtime, specReg, func(sessionID string) *versioning.SessionVFS {
		return orch.EnsureSessionVFS(sessionID, projectRoot)
	})

	// ScribeFactory: creates Gemini 3 Flash sidecars for pipeline pods.
	// The factory captures the Google gateway, bus, and scope — each
	// Scribe gets a gateway-wrapped provider for rate limiting.
	scribeFactory := buildScribeFactory(ctx, googleGateway, authRegistry, guideBus, scope)
	if scribeFactory != nil {
		orch.SetScribeFactory(scribeFactory)
	}

	slog.Info("bootstrap phase 3 complete", "elapsed", time.Since(phase3Start))

	// =================================================================
	// Phase 4: Critical path (session + seeds) + background activation
	// =================================================================

	phase4Start := time.Now()

	defaultSession, err := sessionMgr.Create(ctx, session.DefaultConfig())
	if err != nil {
		cleanupInfra(runtime, containerReg, namespace, guideBus)
		return ui.Deps{}, nil, fmt.Errorf("default session: %w", err)
	}
	_ = sessionMgr.Switch(defaultSession.ID())

	orch.SignalReady()

	// Load persisted agent model selections from the config file.
	configPath := filepath.Join(projectRoot, ".sylk", "config.yaml")
	modelStore := agentpkg.NewAgentModelStore(configPath)

	// Seeds use placeholder IDs (agent type names). The UI's
	// ensureAgent/promoteSeededAgent re-keys to UUIDs when activity
	// events arrive from pre-activated agents.
	seeds := []ui.AgentSeed{
		{ID: "guide", AgentType: "guide", Name: "Guide"},
		{ID: "architect", AgentType: "architect", Name: "Architect"},
		{ID: "guardian", AgentType: "guardian", Name: "Guardian"},
		{ID: "inspector", AgentType: "inspector", Name: "Inspector"},
		{ID: "tester", AgentType: "tester", Name: "Tester"},
		{ID: "librarian", AgentType: "librarian", Name: "Librarian"},
		{ID: "archivalist", AgentType: "archivalist", Name: "Archivalist"},
		{ID: "academic", AgentType: "academic", Name: "Academic"},
	}

	// Populate per-agent supported models from the container registry.
	populateSeedModels(seeds, containerReg)

	// Enrich seeds with persisted model+provider selections so the UI shows the
	// user's previous choice immediately on boot.
	for i := range seeds {
		entry := modelStore.EntryFor(seeds[i].AgentType)
		seeds[i].PersistedModelID = resolvePersistedModelForCurrentAuth(entry.Model, entry.Provider, authRegistry)
		seeds[i].PersistedProviderID = entry.Provider
	}

	// Write defaults for any agent not yet in the config file.
	agentDefaults := make(map[string]agentpkg.AgentConfigEntry, len(seeds))
	for _, s := range seeds {
		if dflt := agentpkg.DefaultModelForAgentType(s.AgentType); dflt != "" {
			agentDefaults[s.AgentType] = agentpkg.AgentConfigEntry{
				Provider: agentpkg.DeriveProvider(dflt),
				Model:    dflt,
			}
		}
	}
	if err := modelStore.EnsureDefaults(agentDefaults); err != nil {
		slog.Warn("agent model config: ensure defaults", "error", err)
	}

	// Build the model swapper early so phase4 goroutines can apply
	// persisted models after activation.
	modelSwapper := buildModelSwapper(containerReg, authRegistry, googleGateway, anthropicGateway, openaiGateway)

	// Background Phase 4: agent pre-activations, handoff supervisor,
	// and auth probe run after deps are returned. Each child is a
	// tracked scope.Go — no coordinator goroutine, no WaitGroup. An
	// atomic counter tracks completion; the last goroutine out performs
	// the late-register step and closes phase4Done.
	var supervisorRef atomic.Pointer[handoff.HandoffSupervisor]
	phase4Done := make(chan struct{})

	// Atomic counter: each goroutine decrements on exit (via defer).
	// The last one to finish (counter reaches 0) runs late-register
	// and closes phase4Done. No blocking Wait — each goroutine
	// responds to context cancellation independently.
	var phase4Remaining atomic.Int32

	phase4Finish := func() {
		if phase4Remaining.Add(-1) > 0 {
			return
		}
		// Last goroutine out — run late-register then signal done.
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
		close(phase4Done)
	}

	// A: Agent pre-activations (6 tracked goroutines)
	//
	// Each goroutine gets an explicit timeout via scope.Go so the worker
	// context carries a bounded deadline. If activation exceeds the
	// budget, the context cancels and EnsureActive returns immediately.
	const phase4ActivationTimeout = 45 * time.Second

	// activationsDone is closed after all activation goroutines finish,
	// so the boot-model-swap goroutine can wait without deadlocking phase4Done.
	activationsDone := make(chan struct{})
	var activationsRemaining atomic.Int32

	if activationCtrl != nil {
		activationsRemaining.Add(6)
		phase4Remaining.Add(6)
		_ = scope.Go("phase4-activate-architect", phase4ActivationTimeout, func(bgCtx context.Context) error {
			defer phase4Finish()
			defer func() {
				if activationsRemaining.Add(-1) == 0 {
					close(activationsDone)
				}
			}()
			p4Start := time.Now()
			deadline, hasDL := bgCtx.Deadline()
			slog.Info("phase4: architect activation start", "has_deadline", hasDL, "deadline", deadline)
			if _, err := activationCtrl.EnsureActive(bgCtx, "architect"); err != nil {
				slog.Warn("phase4: architect activation failed", "error", err, "elapsed_ms", time.Since(p4Start).Milliseconds())
				return nil
			}
			slog.Info("phase4: architect activation done", "elapsed_ms", time.Since(p4Start).Milliseconds())
			if bgCtx.Err() != nil {
				return nil
			}
			arch, err := extractAgent[*architect.Architect](containerReg, "architect")
			if err != nil {
				return nil
			}
			_ = registerArchitectWithGuide(g, arch)
			wireGlobalAgentPod(arch, "architect", scribeFactory, activator, activityPub, slog.Default())
			return nil
		})
		_ = scope.Go("phase4-activate-inspector", phase4ActivationTimeout, func(bgCtx context.Context) error {
			defer phase4Finish()
			defer func() {
				if activationsRemaining.Add(-1) == 0 {
					close(activationsDone)
				}
			}()
			if _, err := activationCtrl.EnsureActive(bgCtx, "inspector"); err != nil {
				slog.Warn("phase4: inspector activation failed", "error", err)
				return nil
			}
			if bgCtx.Err() != nil {
				return nil
			}
			gi, err := extractAgent[*inspectorGlobal.GlobalInspector](containerReg, "inspector")
			if err != nil {
				return nil
			}
			_ = registerAgentWithGuide(g, gi, "inspector")
			wireGlobalAgentPod(gi, "inspector", scribeFactory, activator, activityPub, slog.Default())
			return nil
		})
		_ = scope.Go("phase4-activate-tester", phase4ActivationTimeout, func(bgCtx context.Context) error {
			defer phase4Finish()
			defer func() {
				if activationsRemaining.Add(-1) == 0 {
					close(activationsDone)
				}
			}()
			if _, err := activationCtrl.EnsureActive(bgCtx, "tester"); err != nil {
				slog.Warn("phase4: tester activation failed", "error", err)
				return nil
			}
			if bgCtx.Err() != nil {
				return nil
			}
			gt, err := extractAgent[*globaltester.GlobalTester](containerReg, "tester")
			if err != nil {
				return nil
			}
			_ = registerAgentWithGuide(g, gt, "tester")
			wireGlobalAgentPod(gt, "tester", scribeFactory, activator, activityPub, slog.Default())
			return nil
		})
		_ = scope.Go("phase4-activate-librarian", phase4ActivationTimeout, func(bgCtx context.Context) error {
			defer phase4Finish()
			defer func() {
				if activationsRemaining.Add(-1) == 0 {
					close(activationsDone)
				}
			}()
			if _, err := activationCtrl.EnsureActive(bgCtx, "librarian"); err != nil {
				slog.Warn("phase4: librarian activation failed", "error", err)
				return nil
			}
			if bgCtx.Err() != nil {
				return nil
			}
			lib, err := extractAgent[*librarian.Librarian](containerReg, "librarian")
			if err != nil {
				return nil
			}
			_ = registerAgentWithGuide(g, lib, "librarian")
			wireGlobalAgentPod(lib, "librarian", scribeFactory, activator, activityPub, slog.Default())
			return nil
		})
		_ = scope.Go("phase4-activate-archivalist", phase4ActivationTimeout, func(bgCtx context.Context) error {
			defer phase4Finish()
			defer func() {
				if activationsRemaining.Add(-1) == 0 {
					close(activationsDone)
				}
			}()
			if _, err := activationCtrl.EnsureActive(bgCtx, "archivalist"); err != nil {
				slog.Warn("phase4: archivalist activation failed", "error", err)
				return nil
			}
			if bgCtx.Err() != nil {
				return nil
			}
			arch, err := extractAgent[*archivalist.Archivalist](containerReg, "archivalist")
			if err != nil {
				return nil
			}
			_ = registerAgentWithGuide(g, arch, "archivalist")
			return nil
		})
		_ = scope.Go("phase4-activate-academic", phase4ActivationTimeout, func(bgCtx context.Context) error {
			defer phase4Finish()
			defer func() {
				if activationsRemaining.Add(-1) == 0 {
					close(activationsDone)
				}
			}()
			if _, err := activationCtrl.EnsureActive(bgCtx, "academic"); err != nil {
				slog.Warn("phase4: academic activation failed", "error", err)
				return nil
			}
			return nil
		})
	} else {
		close(activationsDone)
	}

	// B: Handoff supervisor (tolerates nil arch)
	phase4Remaining.Add(1)
	_ = scope.Go("phase4-handoff-supervisor", 0, func(_ context.Context) error {
		defer phase4Finish()
		sup := bootstrapHandoffSupervisor(g, nil, orch, serviceReg, containerReg, activationCtrl)
		if sup != nil {
			supervisorRef.Store(sup)
		}
		return nil
	})

	// C: Auth probe
	phase4Remaining.Add(1)
	_ = scope.Go("phase4-auth-probe", 0, func(_ context.Context) error {
		defer phase4Finish()
		authRegistry.ProbeAll()
		return nil
	})

	// D: Apply persisted model+provider to agent backends.
	// Runs after activations so SwapModel targets live agents.
	phase4Remaining.Add(1)
	_ = scope.Go("phase4-boot-model-swap", phase4ActivationTimeout, func(bgCtx context.Context) error {
		defer phase4Finish()
		// Wait for activations to complete before swapping.
		select {
		case <-activationsDone:
		case <-bgCtx.Done():
			return nil
		}
		for _, s := range seeds {
			if bgCtx.Err() != nil {
				return nil
			}
			dflt := agentpkg.DefaultModelForAgentType(s.AgentType)
			persisted := modelStore.ModelFor(s.AgentType)
			if persisted == "" || persisted == dflt {
				continue
			}
			if err := modelSwapper(bgCtx, s.AgentType, persisted); err != nil {
				slog.Warn("boot model swap failed", "agent", s.AgentType, "model", persisted, "error", err)
			} else {
				slog.Info("boot model swap applied", "agent", s.AgentType, "model", persisted)
			}
		}
		return nil
	})

	// E: Knowledge boot — runs boot.Boot and promotes partial knowledge.
	// Non-critical and potentially slow; fires fully in background.
	//
	// BootEventLogger is created before boot so pre-session events
	// are durably staged. BindSession drains into the session log.
	bootLogger, bootLogErr := agentlog.NewBootEventLogger(
		filepath.Join(projectRoot, ".sylk"),
	)
	if bootLogErr != nil {
		slog.Warn("boot logger init failed (non-critical)", "error", bootLogErr)
	}
	knowledgeStore.SetBootLogger(bootLogger)

	phase4Remaining.Add(1)
	_ = scope.Go("phase4-knowledge-boot", 0, func(bgCtx context.Context) error {
		defer phase4Finish()

		// Boot is non-critical but potentially slow. Run via channel so the
		// scope worker exits promptly on shutdown (Ctrl+C). The boot goroutine
		// is bounded: pipeline.Run checks ctx.Err() between phases and
		// CommitToGlobal threads ctx through internal errgroups, so it exits
		// shortly after bgCtx cancellation. At process exit the OS reclaims.
		type bootOut struct {
			result *boot.PipelineResult
			err    error
		}
		ch := make(chan bootOut, 1)
		go func() {
			r, err := boot.BootWithConfig(bgCtx, boot.PipelineConfig{
				ProjectRoot: projectRoot,
				Logger:      bootLogger,
				OnProgress:  knowledgeStore.NotifyProgress,
			})
			ch <- bootOut{r, err}
		}()

		select {
		case <-bgCtx.Done():
			return nil
		case out := <-ch:
			if out.err != nil {
				slog.Warn("knowledge boot failed (non-critical)", "error", out.err)
				return nil
			}
			// Build BleveSearcher from the BackgroundIndexer's already-open
			// store when available. Opening a second store at the same path
			// would block on bolt.DB's exclusive file lock held by the indexer.
			var bleveSearcher *query.BleveSearcher
			var bleveCloser io.Closer
			if bgIdx := out.result.BackgroundIndexer; bgIdx != nil && bgIdx.BleveStore() != nil {
				store := bgIdx.BleveStore()
				adapter := &globalBleveAdapter{store: store}
				bleveSearcher = query.NewBleveSearcher(adapter)
				bleveCloser = &bleveStoreCloser{store: store}
			} else {
				var buildErr error
				bleveSearcher, bleveCloser, buildErr = buildBleveSearcher(projectRoot)
				if buildErr != nil {
					slog.Warn("knowledge bleve open failed (non-critical)", "error", buildErr)
					return nil
				}
			}
			knowledgeStore.PromotePartial(bleveSearcher, out.result.BackgroundIndexer, bleveCloser)
			return nil
		}
	})

	// F: Wait for BackgroundIndexer to finish, then promote to full knowledge.
	phase4Remaining.Add(1)
	_ = scope.Go("phase4-bleve-ready", 0, func(bgCtx context.Context) error {
		defer phase4Finish()
		if err := knowledgeStore.WaitForPartial(bgCtx); err != nil {
			return nil
		}
		bgWaiter := knowledgeStore.BackgroundWaiter()
		if bgWaiter == nil {
			knowledgeStore.PromoteFull()
			return nil
		}
		select {
		case <-bgWaiter.Ready():
			knowledgeStore.PromoteFull()
		case <-bgCtx.Done():
		}
		return nil
	})

	slog.Info("bootstrap critical path complete", "elapsed", time.Since(start))

	// =================================================================
	// Cleanup — ordered teardown
	// =================================================================

	cleanup := func() error {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()

		// Phase 4 goroutines are tracked by the scope — scope.Shutdown
		// (called from app.Shutdown) already cancels their contexts and
		// waits for completion. A non-blocking drain avoids a redundant
		// blocking wait that would stall exit.
		select {
		case <-phase4Done:
		default:
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

		planStore.Close()

		_ = bootLogger.Close()

		if ksErr := knowledgeStore.Close(); ksErr != nil {
			errs = append(errs, ksErr)
		}

		googleGateway.Stop()
		anthropicGateway.Stop()
		openaiGateway.Stop()

		if busErr := guideBus.Close(); busErr != nil {
			errs = append(errs, busErr)
		}
		return errors.Join(errs...)
	}

	deps := ui.Deps{
		ActivityPub:        activityPub,
		SessionManager:     sessionMgr,
		GuideBus:           guideBus,
		StreamManager:      streamMgr,
		Guide:              g,
		Scope:              scope,
		AuthRegistry:       authRegistry,
		InterruptAllAgents: makeInterruptAllAgentsFn(activationCtrl, guideBus),
		NerdFontsDetected:  fontRes.detected,
		GitClient:          gitRes.client,
		GitWatcher:         gitRes.watcher,
		GitBus:             gitRes.bus,
		SafetyGuard:        gitRes.guard,
		SeedAgents:         seeds,
		ModelSwap:          modelSwapper,
		ModelSave: func(agentType, provider, modelID string) {
			if err := modelStore.SetEntry(agentType, provider, modelID); err != nil {
				slog.Warn("agent model config: save", "agent", agentType, "provider", provider, "model", modelID, "error", err)
			}
		},
		AgentModelStore: modelStore,
		KnowledgeStore:  knowledgeStore,
	}

	return deps, cleanup, nil
}

func makeInterruptAllAgentsFn(
	activationCtrl *activation.ActivationController,
	bus guide.EventBus,
) func(sessionID, reason string) error {
	if activationCtrl == nil && bus == nil {
		return nil
	}
	return func(sessionID, reason string) error {
		var errs []error
		reason = strings.TrimSpace(reason)
		if activationCtrl != nil && bus != nil {
			actionData := map[string]any{
				"session_id": sessionID,
				"scope":      "session",
			}
			if reason != "" {
				actionData["reason"] = reason
			}
			for _, agentType := range activationCtrl.ActiveAgentTypes() {
				if strings.TrimSpace(agentType) == "" || agentType == "guide" {
					continue
				}
				action := &guide.ActionRequest{
					SourceAgentID: "tui",
					TargetAgentID: agentType,
					Action:        "cancel",
					Data:          actionData,
					FireAndForget: true,
					Timestamp:     time.Now(),
				}
				if err := bus.Publish(
					guide.TopicRequests(agentType, agentType),
					guide.NewActionMessage("", action),
				); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if bus != nil && strings.TrimSpace(sessionID) != "" {
			interruptReq := &guide.UserInterruptRequest{
				SessionID:     sessionID,
				SourceAgentID: "tui",
				Scope:         guide.UserInterruptScopeSession,
				Reason:        reason,
				Timestamp:     time.Now(),
			}
			if err := bus.Publish(
				guide.TopicGuideRequests,
				guide.NewUserInterruptMessage("", interruptReq),
			); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
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
	ids *container.AgentIdentityRegistry,
	bus guide.EventBus,
	actPub events.ActivityPublisher,
	projectRoot string,
	hydrateOnce *hydrateOnceCell,
	hydratedRef *atomic.Pointer[providers.HydratedGoogleAuth],
	googleGw *gateway.ProviderGateway,
	anthropicGw *gateway.ProviderGateway,
	openaiGw *gateway.ProviderGateway,
	authRegistry *credentials.AuthRegistry,
	actCtrlRef *atomic.Pointer[activation.ActivationController],
	gitSubsRef *atomic.Pointer[gitBootResult],
	planStore *architect.PlanStore,
	knowledgeStore *knowledge.KnowledgeStore,
	daemonCtrl *daemon.DaemonSetController,
	guideRef *atomic.Pointer[guide.Guide],
	guardianRef *atomic.Pointer[guardian.Guardian],
	orchRef *atomic.Pointer[orchestrator.Orchestrator],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
	budget *concurrency.GoroutineBudget,
) {
	// Guide — Gemini with rule-based fallback.
	// First call blocks on hydrateOnce; subsequent calls (daemon restart)
	// read hydratedRef which is already populated.
	reg.Register("guide", func(ctx context.Context) (container.ContainerAgent, error) {
		h := hydrateOnce.result()
		if h == nil {
			h = hydratedRef.Load()
		}
		return bootstrapLiveGuide(ctx, bus, actPub, h, googleGw, authRegistry, projectRoot, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		})
	})

	// Architect — Anthropic LLM planner. Reads through session-scoped global
	// overlay when a session is active, falling back to read-only disk access.
	architectID, _ := ids.Get("architect")
	reg.Register("architect", func(ctx context.Context) (container.ContainerAgent, error) {
		return bootstrapArchitect(ctx, architectID, bus, actPub, projectRoot, anthropicGw, authRegistry, actCtrlRef, planStore, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		})
	})

	// Orchestrator — pipeline coordinator.
	orchestratorID, _ := ids.Get("orchestrator")
	reg.Register("orchestrator", func(ctx context.Context) (container.ContainerAgent, error) {
		h := hydrateOnce.result()
		if h == nil {
			h = hydratedRef.Load()
		}
		return bootstrapOrchestrator(ctx, orchestratorID, bus, actPub, projectRoot, h, authRegistry, googleGw)
	})

	// Guardian — safety sidecar daemon.
	// On daemon restart the factory must re-wire post-boot deps (observability,
	// git, agent seeding) that Phase 3 originally set up.
	reg.Register("guardian", func(ctx context.Context) (container.ContainerAgent, error) {
		grd, err := bootstrapGuardian(ctx, bus, actPub, projectRoot, googleGw, anthropicGw, openaiGw, authRegistry, gitSubsRef, quarantineRef, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		guardianRef.Store(grd)
		wireGuardianPostBootDeps(grd, actCtrlRef, daemonCtrl, guideRef, orchRef, authRegistry, openaiGw,
			[]*gateway.ProviderGateway{googleGw, anthropicGw, openaiGw}, knowledgeStore, budget)
		return grd, nil
	})

	// On-demand agents — created lazily by the ActivationController.
	registerOnDemandAgentCreators(reg, ids, bus, actPub, projectRoot, googleGw, anthropicGw, openaiGw, authRegistry, actCtrlRef, knowledgeStore, guardianRef, orchRef, quarantineRef)
}

// registerOnDemandAgentCreators registers factories for knowledge and pipeline agents.
// These are created lazily by the ActivationController when the Guide routes to them.
func registerOnDemandAgentCreators(
	reg *container.AgentCreatorRegistry,
	ids *container.AgentIdentityRegistry,
	bus guide.EventBus,
	actPub events.ActivityPublisher,
	projectRoot string,
	googleGw *gateway.ProviderGateway,
	anthropicGw *gateway.ProviderGateway,
	openaiGw *gateway.ProviderGateway,
	authRegistry *credentials.AuthRegistry,
	actCtrlRef *atomic.Pointer[activation.ActivationController],
	knowledgeStore *knowledge.KnowledgeStore,
	guardianRef *atomic.Pointer[guardian.Guardian],
	orchRef *atomic.Pointer[orchestrator.Orchestrator],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
) {
	// Librarian — codebase search specialist with LLM-driven conversation.
	librarianID, _ := ids.Get("librarian")
	reg.Register("librarian", func(ctx context.Context) (container.ContainerAgent, error) {
		model := librarian.DefaultLibrarianModel
		wrapped, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("librarian provider: %w", err)
		}

		libCfg := librarian.Config{
			ID:               librarianID,
			EnableLLM:        true,
			Model:            model,
			AnthropicAPIKey:  providers.ResolveAnthropicAPIKey(""),
			ActivityPub:      actPub,
			WorkingDirectory: projectRoot,
		}
		if ac := actCtrlRef.Load(); ac != nil {
			libCfg.RequestGuard = func() func() {
				return ac.AcquireRequestGuard("librarian")
			}
		}
		l, err := librarian.New(libCfg, wrapped)
		if err != nil {
			return nil, err
		}
		l.SetProviderRefresher(buildProviderRefresher(authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution))
		l.SetKnowledgeStore(knowledgeStore)

		// Create a SessionVFS for the librarian's clone store.
		svfs := versioning.NewSessionVFS(versioning.SessionVFSConfig{
			SessionID:  versioning.SessionID("librarian-" + librarianID),
			WorkingDir: projectRoot,
		})
		l.SetSessionVFS(svfs)

		if startErr := l.Start(bus); startErr != nil {
			svfs.Close()
			return nil, startErr
		}
		return l, nil
	})

	// Archivalist — historical data, patterns, failures.
	archivalistID, _ := ids.Get("archivalist")
	reg.Register("archivalist", func(ctx context.Context) (container.ContainerAgent, error) {
		model := archivalist.ModelSonnet45
		wrapped, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("archivalist provider: %w", err)
		}

		archCfg := archivalist.Config{
			ID:                archivalistID,
			ActivityPub:       actPub,
			EnableHybridQuery: true,
			EnableACTR:        true,
			WorkspaceViews: versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
				DefaultView: versioning.WorkspaceViewGlobal,
				WorkingDir:  projectRoot,
				SessionLookup: func(sessionID string) *versioning.SessionVFS {
					if orch := orchRef.Load(); orch != nil {
						return orch.GetSessionVFS(sessionID)
					}
					return nil
				},
				DiskFallback: versioning.NewDiskFileAccess(projectRoot, true),
			}),
		}
		if ac := actCtrlRef.Load(); ac != nil {
			archCfg.RequestGuard = func() func() {
				return ac.AcquireRequestGuard("archivalist")
			}
		}
		a, err := archivalist.New(ctx, archCfg)
		if err != nil {
			return nil, err
		}
		a.SetProvider(wrapped)
		a.SetKnowledgeStore(knowledgeStore)
		if startErr := a.Start(bus); startErr != nil {
			return nil, startErr
		}
		return a, nil
	})

	// Academic — research, academic papers, best practices.
	academicID, _ := ids.Get("academic")
	reg.Register("academic", func(ctx context.Context) (container.ContainerAgent, error) {
		model := effectiveModelForCurrentAuth(authRegistry, academic.DefaultModel)
		wrapped, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("academic provider: %w", err)
		}
		acaCfg := academic.Config{
			ID:          academicID,
			Model:       model,
			ActivityPub: actPub,
			WorkspaceViews: versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
				DefaultView: versioning.WorkspaceViewDisk,
				WorkingDir:  projectRoot,
				SessionLookup: func(sessionID string) *versioning.SessionVFS {
					if orch := orchRef.Load(); orch != nil {
						return orch.GetSessionVFS(sessionID)
					}
					return nil
				},
				DiskFallback: versioning.NewDiskFileAccess(projectRoot, true),
			}),
		}
		if ac := actCtrlRef.Load(); ac != nil {
			acaCfg.RequestGuard = func() func() {
				return ac.AcquireRequestGuard("academic")
			}
		}
		a, err := academic.New(acaCfg, wrapped)
		if err != nil {
			return nil, err
		}
		a.SetProviderRefresher(buildProviderRefresher(authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution))
		a.SetFetchPipeline(buildAcademicFetchPipeline(projectRoot, guardianRef, quarantineRef))
		if startErr := a.Start(bus); startErr != nil {
			return nil, startErr
		}
		return a, nil
	})

	// Global Inspector — cross-file architectural quality auditor.
	inspectorID, _ := ids.Get("inspector")
	reg.Register("inspector", func(ctx context.Context) (container.ContainerAgent, error) {
		model := "claude-opus-4-6"
		wrapped, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityValidation)
		if err != nil {
			return nil, fmt.Errorf("global inspector provider: %w", err)
		}
		gi, err := inspectorGlobal.New(inspectorShared.GlobalInspectorConfig{AgentID: inspectorID, Model: model}, wrapped)
		if err != nil {
			return nil, err
		}
		gi.SetFileAccess(versioning.NewSessionRoutingFileAccess(
			true,
			func(sessionID string) *versioning.SessionVFS {
				if orch := orchRef.Load(); orch != nil {
					return orch.GetSessionVFS(sessionID)
				}
				return nil
			},
			versioning.NewDiskFileAccess(projectRoot, true),
		))
		gi.SetWorkspaceViews(versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView: versioning.WorkspaceViewGlobal,
			WorkingDir:  projectRoot,
			SessionLookup: func(sessionID string) *versioning.SessionVFS {
				if orch := orchRef.Load(); orch != nil {
					return orch.GetSessionVFS(sessionID)
				}
				return nil
			},
			DiskFallback: versioning.NewDiskFileAccess(projectRoot, true),
		}))
		gi.SetProviderRefresher(buildProviderRefresher(authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityValidation))
		if startErr := gi.Start(bus); startErr != nil {
			return nil, startErr
		}
		return gi, nil
	})

	// Pipeline Inspector — per-task quality validation.
	pipelineInspectorID, _ := ids.Get("inspector-pipeline")
	reg.Register("inspector-pipeline", func(ctx context.Context) (container.ContainerAgent, error) {
		agentID := pipelineInspectorID
		if workerID := pipelineWorkerAgentID(ctx, "inspector-pipeline"); workerID != "" {
			agentID = workerID
		}
		model := "claude-opus-4-6"
		wrapped, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityValidation)
		if err != nil {
			return nil, fmt.Errorf("pipeline inspector provider: %w", err)
		}
		pi, err := inspectorPipeline.New(inspectorShared.PipelineInspectorConfig{AgentID: agentID}, wrapped)
		if err != nil {
			return nil, err
		}
		pi.SetActivityPublisher(actPub)
		if startErr := pi.Start(bus); startErr != nil {
			return nil, startErr
		}
		return pi, nil
	})

	// Global Tester — cross-pipeline SDET.
	// Provider creation is best-effort: when the API key is missing the
	// tester starts in degraded mode — static conversation replies still work,
	// LLM-dependent paths return a clear error.
	testerID, _ := ids.Get("tester")
	reg.Register("tester", func(ctx context.Context) (container.ContainerAgent, error) {
		model := effectiveModelForCurrentAuth(authRegistry, "gpt-5.4-pro")
		var wrapped providers.Provider
		if provider, provErr := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityValidation); provErr != nil {
			slog.Warn("tester: provider unavailable, LLM features disabled", "error", provErr)
		} else {
			wrapped = provider
		}
		gt, err := globaltester.New(shared.GlobalTesterConfig{AgentID: testerID, Model: model}, wrapped)
		if err != nil {
			return nil, err
		}
		gt.SetFileAccess(versioning.NewSessionRoutingFileAccess(
			false,
			func(sessionID string) *versioning.SessionVFS {
				if orch := orchRef.Load(); orch != nil {
					return orch.GetSessionVFS(sessionID)
				}
				return nil
			},
			versioning.NewDiskFileAccess(projectRoot, false),
		))
		gt.SetWorkspaceViews(versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView: versioning.WorkspaceViewGlobal,
			WorkingDir:  projectRoot,
			SessionLookup: func(sessionID string) *versioning.SessionVFS {
				if orch := orchRef.Load(); orch != nil {
					return orch.GetSessionVFS(sessionID)
				}
				return nil
			},
			DiskFallback: versioning.NewDiskFileAccess(projectRoot, true),
		}))
		gt.SetProviderRefresher(buildProviderRefresher(authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityValidation))
		if startErr := gt.Start(bus); startErr != nil {
			return nil, startErr
		}
		return gt, nil
	})

	// Pipeline Tester — per-task QE.
	pipelineTesterID, _ := ids.Get("tester-pipeline")
	reg.Register("tester-pipeline", func(ctx context.Context) (container.ContainerAgent, error) {
		agentID := pipelineTesterID
		if workerID := pipelineWorkerAgentID(ctx, "tester-pipeline"); workerID != "" {
			agentID = workerID
		}
		model := effectiveModelForCurrentAuth(authRegistry, "gpt-5.4-pro")
		wrapped, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityValidation)
		if err != nil {
			return nil, fmt.Errorf("pipeline tester provider: %w", err)
		}
		pt, err := pipelinetester.New(shared.PipelineTesterConfig{AgentID: agentID, Model: model}, wrapped)
		if err != nil {
			return nil, err
		}
		pt.SetActivityPublisher(actPub)
		if startErr := pt.Start(bus); startErr != nil {
			return nil, startErr
		}
		return pt, nil
	})

	// Engineer — code implementation.
	engineerID, _ := ids.Get("engineer")
	reg.Register("engineer", func(ctx context.Context) (container.ContainerAgent, error) {
		agentID := engineerID
		if workerID := pipelineWorkerAgentID(ctx, "engineer"); workerID != "" {
			agentID = workerID
		}
		model := effectiveModelForCurrentAuth(authRegistry, "gpt-5.4-pro")
		wrapped, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("engineer provider: %w", err)
		}
		engCfg := engineer.Config{ID: agentID, ActivityPub: actPub}
		engCfg.EngineerConfig.Model = model
		if ac := actCtrlRef.Load(); ac != nil {
			engCfg.RequestGuard = func() func() {
				return ac.AcquireRequestGuard("engineer")
			}
		}
		e, err := engineer.New(engCfg, wrapped)
		if err != nil {
			return nil, err
		}
		e.SetProviderRefresher(buildProviderRefresher(authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution))
		if startErr := e.Start(bus); startErr != nil {
			return nil, startErr
		}
		return e, nil
	})

	// Designer — LLM-driven design implementation.
	designerID, _ := ids.Get("designer")
	reg.Register("designer", func(ctx context.Context) (container.ContainerAgent, error) {
		agentID := designerID
		if workerID := pipelineWorkerAgentID(ctx, "designer"); workerID != "" {
			agentID = workerID
		}
		model := string(providers.Gemini31Pro)
		wrapped, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("designer provider: %w", err)
		}
		desCfg := designer.Config{ID: agentID, ActivityPub: actPub}
		if ac := actCtrlRef.Load(); ac != nil {
			desCfg.RequestGuard = func() func() {
				return ac.AcquireRequestGuard("designer")
			}
		}
		d, err := designer.New(desCfg, wrapped)
		if err != nil {
			return nil, err
		}
		d.SetProviderRefresher(buildProviderRefresher(authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityExecution))
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

// bootstrapLiveGuide creates and starts a Guide with LLM classification.
// When hydrated is non-nil, it reuses pre-resolved auth (skipping duplicate
// OAuth + Code Assist setup). If provider auth is unavailable, it falls back
// to a local rule-based classifier so the UI can launch without authorization.
func bootstrapLiveGuide(ctx context.Context, bus guide.EventBus, actPub events.ActivityPublisher, hydrated *providers.HydratedGoogleAuth, googleGw *gateway.ProviderGateway, authRegistry *credentials.AuthRegistry, projectRoot string, sessionVFSLookup func(string) *versioning.SessionVFS) (*guide.Guide, error) {
	googleCfg := defaultGuideGoogleConfig(authRegistry)
	cfg := guide.Config{
		Bus:          bus,
		ActivityPub:  actPub,
		AgentID:      "guide",
		SessionID:    "default",
		GoogleConfig: &googleCfg,
		WorkspaceViews: versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:      versioning.WorkspaceViewDisk,
			DefaultSessionID: "default",
			WorkingDir:       projectRoot,
			SessionLookup:    sessionVFSLookup,
			DiskFallback:     versioning.NewDiskFileAccess(projectRoot, true),
		}),
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
		g, newErr := guide.NewWithProvider(wrapped, googleCfg.Model, cfg)
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

func bootstrapArchitect(ctx context.Context, canonicalID string, bus guide.EventBus, actPub events.ActivityPublisher, projectRoot string, anthropicGw *gateway.ProviderGateway, authRegistry *credentials.AuthRegistry, actCtrlRef *atomic.Pointer[activation.ActivationController], planStore *architect.PlanStore, sessionVFSLookup func(string) *versioning.SessionVFS) (*architect.Architect, error) {
	bootstrapStart := time.Now()
	deadline, hasDL := ctx.Deadline()
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_start", "has_deadline", hasDL, "deadline", deadline)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	authMode := registryAuthMethod(authRegistry, "anthropic", providers.ResolveAnthropicAuthMode(""))
	apiKey := ""
	if authMode == providers.AnthropicAuthModeAPIKey {
		apiKey = providers.ResolveAnthropicAPIKey("")
	}
	guide.DebugFileLog().Info(
		"DEBUG: bootstrap_architect_auth_resolved",
		"auth_mode", authMode,
		"has_key", apiKey != "",
		"elapsed_ms", time.Since(bootstrapStart).Milliseconds(),
	)
	cfg := architect.Config{
		ID:                canonicalID,
		EnableLLM:         true,
		Model:             architect.DefaultArchitectModel,
		AnthropicAuthMode: authMode,
		AnthropicAPIKey:   apiKey,
		FileAccess: versioning.NewSessionRoutingFileAccess(
			true,
			sessionVFSLookup,
			versioning.NewDiskFileAccess(projectRoot, true),
		),
		WorkspaceViews: versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:   versioning.WorkspaceViewGlobal,
			WorkingDir:    projectRoot,
			SessionLookup: sessionVFSLookup,
			DiskFallback:  versioning.NewDiskFileAccess(projectRoot, true),
		}),
		ActivityPub: actPub,
		PlanStore:   planStore,
		PlannerProviderWrapper: func(p *providers.AnthropicProvider) architect.PlannerStreamProvider {
			return anthropicGw.WrapProvider(p, gateway.PriorityPlanning)
		},
	}
	if ac := actCtrlRef.Load(); ac != nil {
		cfg.RequestGuard = func() func() {
			return ac.AcquireRequestGuard("architect")
		}
	}
	newStart := time.Now()
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_new_start")
	a, err := architect.New(ctx, cfg)
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_new_done", "elapsed_ms", time.Since(newStart).Milliseconds(), "error", err)
	if err != nil {
		return nil, err
	}
	startStart := time.Now()
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_bus_start")
	if err := a.Start(bus); err != nil {
		return nil, err
	}
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_bus_done", "elapsed_ms", time.Since(startStart).Milliseconds())
	guide.DebugFileLog().Info("DEBUG: bootstrap_architect_complete", "total_elapsed_ms", time.Since(bootstrapStart).Milliseconds())
	return a, nil
}

// bootstrapGuardian creates and starts a Guardian agent. Provider creation is
// best-effort: when the OpenAI API key is missing the guardian starts in
// degraded mode — deterministic safety checks still work, LLM-dependent
// escalation paths return a clear error. Git subsystems are wired from the
// atomic ref if available (daemon restart case); on initial boot they are
// nil and wired later via SetGitSubsystems in Phase 3.
func bootstrapGuardian(
	ctx context.Context,
	bus guide.EventBus,
	actPub events.ActivityPublisher,
	projectRoot string,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	authRegistry *credentials.AuthRegistry,
	gitSubsRef *atomic.Pointer[gitBootResult],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
	sessionVFSLookup func(string) *versioning.SessionVFS,
) (*guardian.Guardian, error) {
	model := effectiveModelForCurrentAuth(authRegistry, guardian.DefaultGuardianModel)
	var wrapped providers.Provider
	if p, err := createSwapProvider(ctx, model, authRegistry, googleGw, anthropicGw, openaiGw, gateway.PriorityValidation); err != nil {
		slog.Warn("guardian: provider unavailable, LLM features disabled", "error", err)
	} else {
		wrapped = p
	}

	cfg := guardian.Config{
		ActivityPub: actPub,
		FileAccess: versioning.NewSessionRoutingFileAccess(
			true,
			sessionVFSLookup,
			versioning.NewDiskFileAccess(projectRoot, true),
		),
		WorkspaceViews: versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:   versioning.WorkspaceViewGlobal,
			WorkingDir:    projectRoot,
			SessionLookup: sessionVFSLookup,
			DiskFallback:  versioning.NewDiskFileAccess(projectRoot, true),
		}),
		Sanitizer: security.NewSecretSanitizer(),
	}

	// Wire git subsystems from atomic ref (available on daemon restart,
	// nil on initial boot — Phase 3 wires via SetGitSubsystems).
	if gs := gitSubsRef.Load(); gs != nil {
		cfg.GitBus = gs.bus
		cfg.GitWatcher = gs.watcher
	}

	g, err := guardian.New(cfg, wrapped)
	if err != nil {
		return nil, err
	}
	if wrapped != nil && model != guardian.DefaultGuardianModel {
		if err := g.SwapModel(ctx, model, wrapped); err != nil {
			return nil, err
		}
	}

	// Wire quarantine buffer for external content inspection.
	q := fetch.NewQuarantineBuffer(
		guardian.DefaultQuarantineMaxItems,
		guardian.DefaultQuarantineMaxBytes,
	)
	g.SetQuarantine(q)
	if quarantineRef != nil {
		quarantineRef.Store(q)
	}

	if err := g.Start(bus); err != nil {
		return nil, err
	}
	return g, nil
}

// wireGuardianPostBootDeps re-wires observability, git, and agent-seeding
// dependencies on the Guardian. Called from the factory closure so that
// daemon-restart instances receive the same wiring as the initial boot's
// Phase 3. All parameters are read from atomic refs; nil-safe.
func wireGuardianPostBootDeps(
	grd *guardian.Guardian,
	actCtrlRef *atomic.Pointer[activation.ActivationController],
	daemonCtrl *daemon.DaemonSetController,
	guideRef *atomic.Pointer[guide.Guide],
	orchRef *atomic.Pointer[orchestrator.Orchestrator],
	authRegistry *credentials.AuthRegistry,
	openaiGw *gateway.ProviderGateway,
	allGateways []*gateway.ProviderGateway,
	knowledgeStore *knowledge.KnowledgeStore,
	budget *concurrency.GoroutineBudget,
) {
	var acQuerier guardian.ActivationQuerier
	var amQuerier guardian.ActivationMetricsQuerier
	if ac := actCtrlRef.Load(); ac != nil {
		acQuerier = ac
		amQuerier = &activationMetricsAdapter{m: ac.Metrics()}
	}
	var dcQuerier guardian.DaemonQuerier
	if daemonCtrl != nil {
		dcQuerier = &daemonQuerierAdapter{dc: daemonCtrl}
	}
	if ac := actCtrlRef.Load(); ac != nil {
		grd.SetRequestGuard(func() func() {
			return ac.AcquireRequestGuard("guardian")
		})
	}
	grd.SetObservabilityDeps(acQuerier, amQuerier, dcQuerier)
	grd.SetProviderRefresher(buildProviderRefresher(authRegistry, allGateways[0], allGateways[1], allGateways[2], gateway.PriorityValidation))

	// VFS observability — aggregate CVS/VFS stats across all sessions.
	grd.SetVFSDeps(
		&orchestratorCVSAdapter{orchRef: orchRef},
		&orchestratorVFSAdapter{orchRef: orchRef},
	)

	// Extended observability — pipeline, gateway, knowledge, concurrency.
	grd.SetExtendedObservabilityDeps(
		&pipelineQuerierAdapter{orchRef: orchRef},
		&gatewayQuerierAdapter{gateways: allGateways},
		&knowledgeQuerierAdapter{store: knowledgeStore},
		&concurrencyQuerierAdapter{budget: budget},
	)

	// Cost calculator — derives USD-cents per LLM call from model pricing.
	grd.SetCostCalculator(buildCostCalculator())

	// Seed agent registry from the Guide's current state so the Guardian
	// doesn't start with total=0 after daemon restart.
	if g := guideRef.Load(); g != nil {
		// Reap agents stuck in not-ready state before seeding — prevents
		// stale registrations from persisting across daemon restarts.
		if reaped := g.ReapStaleRegistrations(); reaped > 0 {
			slog.Info("guardian post-boot: reaped stale agent registrations", "count", reaped)
		}
		grd.SeedKnownAgents(g.RegisteredAgentInfos())
		_ = registerAgentWithGuide(g, grd, "guardian")
	}
}

func buildAcademicFetchPipeline(
	_ string,
	guardianRef *atomic.Pointer[guardian.Guardian],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
) *fetch.Pipeline {
	autoApprove := make([]string, 0, len(security.DefaultSafeDomains()))
	for domain := range security.DefaultSafeDomains() {
		autoApprove = append(autoApprove, domain)
	}

	policy := fetch.NewFetchPolicy(fetch.DefaultPolicyConfig())
	consent := fetch.NewConsentGate(fetch.ConsentGateConfig{
		AutoApproveDomains: autoApprove,
		Callback: func(_ context.Context, proposal *fetch.FetchProposal) (*fetch.ConsentResult, error) {
			domain := ""
			if proposal != nil {
				domain = proposal.Domain
			}
			return &fetch.ConsentResult{
				Granted: false,
				Reason:  fmt.Sprintf("interactive fetch consent is not configured for %q; only pre-approved documentation and package domains are allowed", domain),
			}, nil
		},
	})

	quarantine := (*fetch.QuarantineBuffer)(nil)
	if quarantineRef != nil {
		quarantine = quarantineRef.Load()
	}
	if quarantine == nil {
		quarantine = fetch.NewQuarantineBuffer(
			guardian.DefaultQuarantineMaxItems,
			guardian.DefaultQuarantineMaxBytes,
		)
	}

	return fetch.NewPipeline(fetch.PipelineConfig{
		Policy:     policy,
		Consent:    consent,
		Quarantine: quarantine,
		Client: fetch.NewClient(fetch.ClientConfig{
			MaxBytes: policy.MaxBytes(),
		}),
		Extractor: fetch.NewContentExtractor(),
		Inspect: func(ctx context.Context, entry *fetch.QuarantineEntry) (fetch.QuarantineVerdict, []fetch.InspectionFinding, error) {
			if guardianRef == nil {
				return fetch.VerdictBlocked, nil, fmt.Errorf("guardian inspection is unavailable")
			}
			grd := guardianRef.Load()
			if grd == nil {
				return fetch.VerdictBlocked, nil, fmt.Errorf("guardian inspection is unavailable")
			}
			return grd.InspectQuarantinedContent(ctx, entry)
		},
		Logger: slog.Default(),
	})
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
	return registerRoutingInfoWithGuide(g, router.GetRoutingInfo())
}

func registerRoutingInfoWithGuide(g *guide.Guide, info *guide.AgentRoutingInfo) error {
	if g == nil || info == nil {
		return nil
	}
	if err := g.Register(info); err != nil {
		return err
	}
	// Use the agent's own routing ID (UUID) — Register() stores
	// readyAgents keyed by info.ID, not by agentType.
	g.MarkAgentReady(info.ID)
	return nil
}

func findRegisteredPodContainer(reg *container.ContainerRegistry, podID, agentType string) *container.Container {
	if reg == nil {
		return nil
	}
	if podID != "" {
		for _, ctr := range reg.ListByPod(container.PodID(podID)) {
			if ctr == nil {
				continue
			}
			if ctr.Spec().AgentType == agentType {
				return ctr
			}
		}
	}
	containers := reg.ListByType(agentType)
	if len(containers) == 0 {
		return nil
	}
	return containers[0]
}

func isTaskScopedPipelineWorker(podID, agentType string) bool {
	if strings.TrimSpace(podID) == "" || podID == agentType {
		return false
	}
	switch agentType {
	case "engineer", "designer", "inspector-pipeline", "tester-pipeline":
		return true
	default:
		return false
	}
}

func taskScopedWorkerRoutingInfo(info *guide.AgentRoutingInfo, podID, agentType string) *guide.AgentRoutingInfo {
	if info == nil {
		return nil
	}
	cloned := *info
	cloned.Name = orchestrator.TaskScopedRoutingName("", podID, agentType)
	cloned.Aliases = nil
	cloned.ActionShortcuts = nil
	cloned.Triggers = guide.AgentTriggers{}
	if info.Registration != nil {
		reg := *info.Registration
		reg.Name = cloned.Name
		reg.Aliases = nil
		cloned.Registration = &reg
	}
	return &cloned
}

func pipelineWorkerAgentID(ctx context.Context, agentType string) string {
	podID, ok := container.CreationPodIDFromContext(ctx)
	if !ok || !isTaskScopedPipelineWorker(string(podID), agentType) {
		return ""
	}
	return orchestrator.TaskScopedAgentID(string(podID), agentType)
}

// preRegisterAgentRouting pre-registers static routing metadata for all
// non-pipeline agents with the Guide. This makes agents visible to the
// classifier before their containers are activated.
func preRegisterAgentRouting(g *guide.Guide, ids *container.AgentIdentityRegistry) {
	type staticInfo struct {
		agentType string
		fn        func(string) *guide.AgentRoutingInfo
	}
	agents := []staticInfo{
		{"architect", architect.ArchitectRoutingInfo},
		{"inspector", inspectorGlobal.InspectorRoutingInfo},
		{"tester", globaltester.TesterRoutingInfo},
		{"librarian", librarian.LibrarianRoutingInfo},
		{"academic", academic.AcademicRoutingInfo},
	}
	for _, a := range agents {
		canonicalID, ok := ids.Get(a.agentType)
		if !ok {
			continue
		}
		if err := g.PreRegister(a.fn(canonicalID)); err != nil {
			slog.Warn("pre-register agent", "agent", a.agentType, "error", err)
		}
	}

	// Guardian uses a hardcoded ID (daemon, not identity-registered).
	if err := g.PreRegister(guardian.GuardianRoutingInfo("guardian")); err != nil {
		slog.Warn("pre-register guardian", "error", err)
	}

	// Archivalist uses a no-arg routing info function (hardcoded ID).
	if err := g.PreRegister(archivalist.ArchivalistRoutingInfo()); err != nil {
		slog.Warn("pre-register archivalist", "error", err)
	}
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
			refreshCtx, refreshCancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := refreshable.RefreshProvider(refreshCtx, event.AuthMethod)
			refreshCancel()
			if err != nil {
				slog.Warn("on-demand auth refresh failed",
					"agent", c.ID(),
					"provider", event.ProviderType,
					"method", event.AuthMethod,
					"error", err)
			}
		}
	}
}

// populateSeedModels enriches AgentSeed entries with supported models
// from agents in the container registry that implement ModelSwappable.
func populateSeedModels(seeds []ui.AgentSeed, reg *container.ContainerRegistry) {
	for i := range seeds {
		for _, c := range reg.ListByType(seeds[i].AgentType) {
			swappable, ok := c.Agent().(container.ModelSwappable)
			if !ok {
				continue
			}
			opts := swappable.SupportedModels()
			entries := make([]agentpkg.ModelEntry, len(opts))
			for j, opt := range opts {
				entries[j] = agentpkg.ModelEntry{ID: opt.ID, DisplayName: opt.DisplayName}
			}
			seeds[i].SupportedModels = entries
			break // One agent per type is sufficient.
		}
	}
}

func resolvePersistedModelForCurrentAuth(modelID, providerID string, authRegistry *credentials.AuthRegistry) string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return ""
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		providerID = string(container.ProviderForModel(modelID))
	}
	if providerID != string(container.ProviderOpenAI) {
		return modelID
	}
	return effectiveModelForCurrentAuth(authRegistry, modelID)
}

func effectiveModelForCurrentAuth(authRegistry *credentials.AuthRegistry, modelID string) string {
	if container.ProviderForModel(modelID) != container.ProviderOpenAI {
		return modelID
	}
	authMode := registryAuthMethod(authRegistry, "openai", "api_key")
	return providers.ResolveOpenAIModelForAuth(modelID, authMode)
}

// agentSwapPriority maps agent type to the gateway request priority used at
// bootstrap. Derived from registerOnDemandAgentCreators wiring.
var agentSwapPriority = map[string]gateway.RequestPriority{
	"guide":        gateway.PriorityUserInteractive,
	"orchestrator": gateway.PriorityUserInteractive,
	"architect":    gateway.PriorityPlanning,
	"guardian":     gateway.PriorityValidation,
	"inspector":    gateway.PriorityValidation,
	"tester":       gateway.PriorityValidation,
	"engineer":     gateway.PriorityExecution,
	"designer":     gateway.PriorityExecution,
	"librarian":    gateway.PriorityExecution,
	"archivalist":  gateway.PriorityExecution,
	"academic":     gateway.PriorityExecution,
}

// buildModelSwapper creates a closure that creates the correct provider with
// the correct gateway wrapper, then calls SwapModel on the matching agent.
// Provider creation is centralized here because cmd/tui.go owns all gateways.
func buildModelSwapper(
	containerReg *container.ContainerRegistry,
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
) func(ctx context.Context, agentType, modelID string) error {
	return func(ctx context.Context, agentType, modelID string) error {
		modelID = effectiveModelForCurrentAuth(authRegistry, modelID)
		priority := agentSwapPriority[agentType]
		provider, err := createSwapProvider(ctx, modelID, authRegistry, googleGw, anthropicGw, openaiGw, priority)
		if err != nil {
			return fmt.Errorf("model swap provider for %s: %w", agentType, err)
		}
		for _, c := range containerReg.ListByType(agentType) {
			swappable, ok := c.Agent().(container.ModelSwappable)
			if !ok {
				continue
			}
			return swappable.SwapModel(ctx, modelID, provider)
		}
		return fmt.Errorf("no ModelSwappable agent found for type %q", agentType)
	}
}

// buildProviderRefresher returns a ProviderRefresher for the given priority
// that creates a fresh provider for any model/auth-method combination.
func buildProviderRefresher(
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	priority gateway.RequestPriority,
) container.ProviderRefresher {
	return func(ctx context.Context, modelID, authMethod string) (providers.ProviderAdapter, error) {
		return createRefreshProvider(ctx, modelID, authMethod, authRegistry, googleGw, anthropicGw, openaiGw, priority)
	}
}

// createRefreshProvider creates a raw provider for the given model ID and
// auth method, then wraps it with the correct gateway at the specified priority.
func createRefreshProvider(
	ctx context.Context,
	modelID, authMethod string,
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	priority gateway.RequestPriority,
) (providers.ProviderAdapter, error) {
	switch container.ProviderForModel(modelID) {
	case container.ProviderAnthropic:
		am := authMethod
		if am == "" {
			am = registryAuthMethod(authRegistry, "anthropic", providers.ResolveAnthropicAuthMode(""))
		}
		raw, err := providers.NewAnthropicProvider(ctx, providers.AnthropicConfig{
			BaseConfig: providers.BaseConfig{
				Model:     modelID,
				MaxTokens: container.SwapMaxTokens,
			},
			AuthMode: am,
		})
		if err != nil {
			return nil, err
		}
		return anthropicGw.WrapProvider(raw, priority), nil
	case container.ProviderGoogle:
		cfg := providers.DefaultGoogleConfig()
		cfg.Model = modelID
		cfg.MaxTokens = container.SwapMaxTokens
		if authMethod != "" {
			cfg.AuthMode = authMethod
		} else {
			cfg.AuthMode = registryAuthMethod(authRegistry, "google", cfg.AuthMode)
		}
		raw, err := providers.NewGoogleProvider(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return googleGw.WrapProvider(raw, priority), nil
	case container.ProviderOpenAI:
		am := authMethod
		if am == "" {
			am = registryAuthMethod(authRegistry, "openai", "api_key")
		}
		modelID = providers.ResolveOpenAIModelForAuth(modelID, am)
		raw, err := providers.NewOpenAIProvider(ctx, providers.OpenAIConfig{
			BaseConfig: providers.BaseConfig{
				Model:     modelID,
				MaxTokens: container.SwapMaxTokens,
			},
			AuthMode: am,
		})
		if err != nil {
			return nil, err
		}
		return openaiGw.WrapProvider(raw, priority), nil
	default:
		return nil, fmt.Errorf("unknown provider for model %q", modelID)
	}
}

// createSwapProvider creates a raw provider for the given model ID, then
// wraps it with the correct gateway at the specified priority.
func createSwapProvider(
	ctx context.Context,
	modelID string,
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
	priority gateway.RequestPriority,
) (providers.ProviderAdapter, error) {
	switch container.ProviderForModel(modelID) {
	case container.ProviderAnthropic:
		raw, err := providers.NewAnthropicProvider(ctx, providers.AnthropicConfig{
			BaseConfig: providers.BaseConfig{
				Model:     modelID,
				MaxTokens: container.SwapMaxTokens,
			},
			AuthMode: registryAuthMethod(authRegistry, "anthropic", providers.ResolveAnthropicAuthMode("")),
		})
		if err != nil {
			return nil, err
		}
		return anthropicGw.WrapProvider(raw, priority), nil
	case container.ProviderGoogle:
		cfg := providers.DefaultGoogleConfig()
		cfg.Model = modelID
		cfg.MaxTokens = container.SwapMaxTokens
		cfg.AuthMode = registryAuthMethod(authRegistry, "google", cfg.AuthMode)
		raw, err := providers.NewGoogleProvider(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return googleGw.WrapProvider(raw, priority), nil
	case container.ProviderOpenAI:
		authMode := registryAuthMethod(authRegistry, "openai", "api_key")
		modelID = providers.ResolveOpenAIModelForAuth(modelID, authMode)
		raw, err := providers.NewOpenAIProvider(ctx, providers.OpenAIConfig{
			BaseConfig: providers.BaseConfig{
				Model:     modelID,
				MaxTokens: container.SwapMaxTokens,
			},
			AuthMode: authMode,
		})
		if err != nil {
			return nil, err
		}
		return openaiGw.WrapProvider(raw, priority), nil
	default:
		return nil, fmt.Errorf("unknown provider for model %q", modelID)
	}
}

// buildAuthResolver creates a provider auth resolver that reports which
// canonical auth methods are currently usable for each provider.
func buildAuthResolver() credentials.AuthMethodResolver {
	// Pre-resolve the credential manager so the closure can check the
	// secure store (keychain / encrypted file) in addition to env vars.
	var credManager *credentials.Manager
	if dirs, dirErr := storage.ResolveDirs(); dirErr == nil && dirs != nil {
		credManager, _ = credentials.NewManager(dirs, "default")
	}

	return func(providerType string) map[string]bool {
		methods := make(map[string]bool)
		switch providerType {
		case "google":
			if os.Getenv("GEMINI_API_KEY") != "" || os.Getenv("GOOGLE_API_KEY") != "" || probeSecureKey(credManager, "google") {
				methods[providers.GoogleAuthModeAPIKey] = true
			}
			if probeOAuthToken("google") {
				methods[providers.GoogleAuthModeOAuth] = true
			}
			if probeGoogleServiceAccount(credManager) {
				methods[providers.GoogleAuthModeServiceAccount] = true
			}
		case "anthropic":
			if providers.ResolveAnthropicAPIKey("") != "" {
				methods[providers.AnthropicAuthModeAPIKey] = true
			}
			if probeOAuthToken("anthropic") {
				methods[providers.AnthropicAuthModeOAuth] = true
			}
		case "openai":
			if os.Getenv("OPENAI_API_KEY") != "" || probeSecureKey(credManager, "openai") {
				methods["api_key"] = true
			}
			if probeOAuthToken("openai") {
				methods["chatgpt"] = true
			}
		}
		return methods
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

func probeGoogleServiceAccount(mgr *credentials.Manager) bool {
	if payload := strings.TrimSpace(os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON")); payload != "" {
		return true
	}
	if mgr == nil {
		return false
	}
	payload, err := mgr.GetAPIKey("google_service_account")
	return err == nil && strings.TrimSpace(payload) != ""
}

// probeOAuthToken checks whether a stored OAuth token exists for the
// given provider by attempting to load from the provider's token store.
func probeOAuthToken(provider string) bool {
	ctx := context.Background()
	switch provider {
	case "google":
		auth, err := oauth.NewGoogleAuthService(oauth.GoogleAuthServiceConfig{}).Load(ctx)
		return err == nil && auth != nil
	case "anthropic":
		auth, err := oauth.NewAnthropicAuthService(oauth.AnthropicAuthServiceConfig{}).Load(ctx)
		return err == nil && auth != nil
	case "openai":
		auth, err := oauth.NewOpenAIAuthService(oauth.OpenAIAuthServiceConfig{}).Load(ctx)
		return err == nil && auth != nil
	default:
		return false
	}
}

// chainPublishers combines multiple AuthPublishers into one.
func chainPublishers(publishers ...credentials.AuthPublisher) credentials.AuthPublisher {
	return func(event credentials.AuthEvent) {
		for _, pub := range publishers {
			pub(event)
		}
	}
}

func registryAuthMethod(reg *credentials.AuthRegistry, providerType, fallback string) string {
	if reg != nil {
		if method := credentials.CanonicalAuthMethod(providerType, reg.ActiveMethod(providerType)); method != "" {
			return method
		}
		if method := credentials.CanonicalAuthMethod(providerType, reg.PreferredMethod(providerType)); method != "" {
			return method
		}
	}
	if method := credentials.CanonicalAuthMethod(providerType, fallback); method != "" {
		return method
	}
	return credentials.DefaultAuthMethod(providerType)
}

func defaultGuideGoogleConfig(authRegistry *credentials.AuthRegistry) providers.GoogleConfig {
	cfg := providers.DefaultGoogleConfig()
	cfg.Model = "gemini-3.1-pro-preview"
	cfg.AuthMode = registryAuthMethod(authRegistry, "google", cfg.AuthMode)
	return cfg
}

func defaultOrchestratorGoogleConfig(authRegistry *credentials.AuthRegistry) providers.GoogleConfig {
	cfg := providers.DefaultGoogleConfig()
	cfg.Model = "gemini-3-flash-preview"
	cfg.AuthMode = registryAuthMethod(authRegistry, "google", cfg.AuthMode)
	return cfg
}

func bootstrapOrchestrator(ctx context.Context, agentID string, bus guide.EventBus, actPub events.ActivityPublisher, projectRoot string, hydrated *providers.HydratedGoogleAuth, authRegistry *credentials.AuthRegistry, googleGw *gateway.ProviderGateway) (*orchestrator.Orchestrator, error) {
	googleCfg := defaultOrchestratorGoogleConfig(authRegistry)

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
	cfg.AgentID = agentID
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
	orch, err := orchestrator.New(cfg, orchProvider, actPub, sd)
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

func refreshOrchestratorProvider(ctx context.Context, orch *orchestrator.Orchestrator, googleGw *gateway.ProviderGateway, authRegistry *credentials.AuthRegistry, authMethod string) {
	if orch == nil {
		return
	}
	googleCfg := defaultOrchestratorGoogleConfig(authRegistry)
	if authMethod != "" {
		googleCfg.AuthMode = authMethod
	}
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
	containerReg *container.ContainerRegistry,
	activationCtrl *activation.ActivationController,
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

	// Wire agent replacement: atomic swap of canonical identity.
	// New agent was invisible to Guide during the traffic shift.
	// At swap time: assign canonical ID → unregister old → register new.
	supervisor.SetAgentReplacedCallback(func(oldID, _ string, newAgent handoff.HandoffableAgent) error {
		if setter, ok := newAgent.(interface{ SetCanonicalID(string) }); ok {
			setter.SetCanonicalID(oldID)
		}
		g.UnregisterAgent(oldID)
		if router, ok := newAgent.(guide.AgentRouter); ok {
			_ = g.RegisterRouter(router)
			g.MarkAgentReady(oldID)
		}

		// Adopt the new container into the ActivationController so the
		// entry points to the live replacement, not the terminated old one.
		if activationCtrl != nil && containerReg != nil {
			containers := containerReg.ListByType(newAgent.AgentType())
			for _, c := range containers {
				if agent := c.Agent(); agent != nil && agent.AgentID() == newAgent.AgentID() {
					if _, err := activationCtrl.AdoptContainer(newAgent.AgentType(), c); err != nil {
						slog.Warn("adopt replacement container", "agent_type", newAgent.AgentType(), "error", err)
					}
					break
				}
			}
		}

		return nil
	})

	// Wire quality publisher: GP quality flows to the service registry.
	supervisor.SetQualityPublisher(func(agentID string, quality, stdDev float64) {
		serviceReg.UpdateQuality(agentID, quality, stdDev)
	})

	// Wire traffic shift callbacks.
	// During gradual shift the new agent stays unregistered with the Guide.
	// The bridge handles overlap routing internally.
	supervisor.SetTrafficShiftCallbacks(
		func(_ string, newAgent handoff.HandoffableAgent) {
			// New agent stays internal to bridge during overlap.
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

// buildScribeFactory creates a ScribeFactory that produces Gemini 3 Flash
// sidecars. Returns nil if the Google provider cannot be created (non-fatal
// — pipelines run without Scribes).
func buildScribeFactory(
	ctx context.Context,
	googleGw *gateway.ProviderGateway,
	authRegistry *credentials.AuthRegistry,
	bus guide.EventBus,
	scope *concurrency.GoroutineScope,
) agentShared.ScribeFactory {
	scribeCfg := providers.DefaultGoogleConfig()
	scribeCfg.Model = "gemini-3-flash-preview"
	scribeCfg.BaseConfig.MaxTokens = 512
	scribeCfg.AuthMode = registryAuthMethod(authRegistry, "google", scribeCfg.AuthMode)

	scribeProvider, err := providers.NewGoogleProvider(ctx, scribeCfg)
	if err != nil {
		slog.Warn("scribe google provider creation failed — scribes disabled", "error", err)
		return nil
	}

	wrapped := googleGw.WrapProvider(scribeProvider, gateway.PriorityBackground)
	return func(parentAgentType string, logger *slog.Logger) (agentShared.Scribe, error) {
		s := scribe.New(scribe.Config{
			ParentAgentType: parentAgentType,
			Provider:        wrapped,
			Model:           "gemini-3-flash-preview",
			Bus:             bus,
			Scope:           scope,
			Logger:          logger,
		})
		return s, nil
	}
}

// agentPodSetter is implemented by agents that accept an AgentPod.
type agentPodSetter interface {
	SetAgentPod(pod *agentShared.AgentPod)
}

// wireGlobalAgentPod creates a single-member AgentPod for a global agent
// (architect, inspector, tester) and sets it on the agent. The pod manages
// Scribe sidecars for the agent. Calls PreActivate to start Scribes.
func wireGlobalAgentPod(
	agent agentPodSetter,
	agentType string,
	scribeFactory agentShared.ScribeFactory,
	activator guide.PodActivator,
	activityPub events.ActivityPublisher,
	logger *slog.Logger,
) {
	pod := agentShared.NewAgentPod(agentShared.AgentPodConfig{
		PodID:         agentType + "-global-pod",
		SessionID:     "default",
		Activator:     activator,
		ActivityPub:   activityPub,
		Logger:        logger,
		MemberTypes:   []string{agentType},
		DisplayNames:  map[string]string{agentType: agentType},
		ScribeFactory: scribeFactory,
	})
	pod.PreActivate(context.Background())
	agent.SetAgentPod(pod)
}

// ---------------------------------------------------------------------------
// Guardian observability adapters
// ---------------------------------------------------------------------------

// activationMetricsAdapter adapts *activation.ActivationMetrics to
// guardian.ActivationMetricsQuerier.
type activationMetricsAdapter struct {
	m *activation.ActivationMetrics
}

func (a *activationMetricsAdapter) Snapshot() map[string]int64 {
	s := a.m.Snapshot()
	return map[string]int64{
		"activations_total":     s.ActivationsTotal,
		"cold_starts":           s.ColdStarts,
		"cool_starts":           s.CoolStarts,
		"warm_starts":           s.WarmStarts,
		"hot_hits":              s.HotHits,
		"coalesced_requests":    s.CoalescedRequests,
		"demotions_to_warm":     s.DemotionsToWarm,
		"demotions_to_cool":     s.DemotionsToCool,
		"demotions_to_cold":     s.DemotionsToCold,
		"predictor_hits":        s.PredictorHits,
		"predictor_misses":      s.PredictorMisses,
		"evictions_by_pressure": s.EvictionsByPressure,
	}
}

// daemonQuerierAdapter adapts *daemon.DaemonSetController to
// guardian.DaemonQuerier.
type daemonQuerierAdapter struct {
	dc *daemon.DaemonSetController
}

func (a *daemonQuerierAdapter) Status() []guardian.DaemonStatusSnapshot {
	statuses := a.dc.Status()
	out := make([]guardian.DaemonStatusSnapshot, len(statuses))
	for i, s := range statuses {
		out[i] = guardian.DaemonStatusSnapshot{
			Name:         s.Name,
			Running:      s.Running,
			ContainerID:  string(s.ContainerID),
			RestartCount: s.RestartCount,
			Healthy:      s.Healthy,
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// VFS adapters — aggregate per-session VFS stats for Guardian
// ---------------------------------------------------------------------------

// orchestratorCVSAdapter implements guardian.CVSQuerier by aggregating live
// SessionVFS stats across all of the Orchestrator's active sessions.
type orchestratorCVSAdapter struct {
	orchRef *atomic.Pointer[orchestrator.Orchestrator]
}

func (a *orchestratorCVSAdapter) Stats() guardian.CVSStatsSnapshot {
	orch := a.orchRef.Load()
	if orch == nil {
		return guardian.CVSStatsSnapshot{}
	}
	var agg guardian.CVSStatsSnapshot
	for _, svfs := range orch.AllSessionVFS() {
		s := svfs.Stats()
		agg.TotalFiles += s.TrackedFiles
		agg.TotalVersions += s.TotalVersions
		agg.TotalOperations += s.TotalOperations
		agg.ActivePipelines += s.ActivePipelines
		agg.ActiveVariants += s.ActiveVariants
		agg.ActiveLocks += s.ActiveLocks
		agg.ActiveSubscribers += s.ActiveSubscribers
		agg.CurrentVersion = s.CurrentVersion.String()
		agg.WALEntries += s.WALEntries
	}
	return agg
}

// orchestratorVFSAdapter implements guardian.VFSManagerQuerier by
// aggregating VFS manager stats across all active sessions.
type orchestratorVFSAdapter struct {
	orchRef *atomic.Pointer[orchestrator.Orchestrator]
}

func (a *orchestratorVFSAdapter) Stats() guardian.VFSManagerSnapshot {
	orch := a.orchRef.Load()
	if orch == nil {
		return guardian.VFSManagerSnapshot{}
	}
	var agg guardian.VFSManagerSnapshot
	for _, svfs := range orch.AllSessionVFS() {
		s := svfs.VFSManager().Stats()
		agg.ActiveVFSes += s.ActiveVFSes
		agg.VariantGroups += s.VariantGroups
		agg.ActiveSessions += s.ActiveSessions
		agg.TotalPipelines += s.TotalPipelines
	}
	return agg
}

// ---------------------------------------------------------------------------
// Extended observability adapters — Pipeline, Gateway, Knowledge, Concurrency
// ---------------------------------------------------------------------------

// pipelineQuerierAdapter implements guardian.PipelineQuerier by delegating
// to the Orchestrator's summary and DAG snapshot methods.
type pipelineQuerierAdapter struct {
	orchRef *atomic.Pointer[orchestrator.Orchestrator]
}

func (a *pipelineQuerierAdapter) Summary() (*guardian.PipelineSummarySnapshot, error) {
	orch := a.orchRef.Load()
	if orch == nil {
		return &guardian.PipelineSummarySnapshot{}, nil
	}
	s, err := orch.GetSummary(context.Background())
	if err != nil {
		return nil, err
	}
	return &guardian.PipelineSummarySnapshot{
		Overview:        s.Overview,
		ActiveWorkflows: s.Workflows.Running,
		TotalWorkflows:  s.Workflows.Total,
		ActiveTasks:     s.Tasks.Running,
		CompletedTasks:  s.Tasks.Completed,
		FailedTasks:     s.Tasks.Failed,
		TotalTasks:      s.Tasks.Total,
	}, nil
}

func (a *pipelineQuerierAdapter) DAGSnapshots(limit int) []guardian.DAGSnapshot {
	orch := a.orchRef.Load()
	if orch == nil {
		return nil
	}
	snaps := orch.GetDAGSnapshots(limit)
	out := make([]guardian.DAGSnapshot, len(snaps))
	for i, s := range snaps {
		out[i] = guardian.DAGSnapshot{
			ID:           s.ID,
			PlanID:       s.PlanID,
			State:        s.State,
			CurrentLayer: s.CurrentLayer,
			TotalLayers:  s.TotalLayers,
			Progress:     s.Progress,
			NodesFailed:  s.NodesFailed,
			Duration:     s.Duration,
		}
	}
	return out
}

// gatewayQuerierAdapter implements guardian.GatewayQuerier by aggregating
// metrics from all provider gateways.
type gatewayQuerierAdapter struct {
	gateways []*gateway.ProviderGateway
}

func (a *gatewayQuerierAdapter) AllMetrics() map[string]guardian.GatewayMetricsSnapshot {
	out := make(map[string]guardian.GatewayMetricsSnapshot, len(a.gateways))
	for _, gw := range a.gateways {
		m := gw.Metrics()
		name := gw.Name()
		out[name] = guardian.GatewayMetricsSnapshot{
			Name:        name,
			Admitted:    m.Admitted,
			Rejected:    m.Rejected,
			Queued:      m.Queued,
			Completed:   m.Completed,
			RateLimited: m.RateLimited,
			Errors429:   m.Errors429,
			Inflight:    m.Inflight,
			TotalWaitMs: m.TotalWaitNs / 1_000_000,
		}
	}
	return out
}

// knowledgeQuerierAdapter implements guardian.KnowledgeQuerier by
// delegating to *knowledge.KnowledgeStore and its coordinator.
type knowledgeQuerierAdapter struct {
	store *knowledge.KnowledgeStore
}

func (a *knowledgeQuerierAdapter) Status() guardian.KnowledgeStatusSnapshot {
	level := a.store.Level()
	labels := [3]string{"none", "partial", "full"}
	label := labels[0]
	if int(level) < len(labels) {
		label = labels[level]
	}

	var searchers []string
	if coord := a.store.Coordinator(); coord != nil {
		searchers = coord.ReadySearchers()
	}

	return guardian.KnowledgeStatusSnapshot{
		ReadinessLevel: int(level),
		ReadinessLabel: label,
		ReadySearchers: searchers,
	}
}

func (a *knowledgeQuerierAdapter) QueryMetrics() guardian.KnowledgeQueryMetrics {
	coord := a.store.Coordinator()
	if coord == nil {
		return guardian.KnowledgeQueryMetrics{}
	}
	m := coord.GetAverageMetrics()
	if m == nil {
		return guardian.KnowledgeQueryMetrics{}
	}
	return guardian.KnowledgeQueryMetrics{
		TextLatencyMs:       m.TextLatency.Milliseconds(),
		SemanticLatencyMs:   m.SemanticLatency.Milliseconds(),
		GraphLatencyMs:      m.GraphLatency.Milliseconds(),
		FusionLatencyMs:     m.FusionLatency.Milliseconds(),
		TotalLatencyMs:      m.TotalLatency.Milliseconds(),
		TextContributed:     m.TextContributed,
		SemanticContributed: m.SemanticContributed,
		GraphContributed:    m.GraphContributed,
	}
}

func (a *knowledgeQuerierAdapter) IngestionProgress() (indexed, total int64) {
	w := a.store.BackgroundWaiter()
	if w == nil {
		return 0, 0
	}
	return w.Progress()
}

// concurrencyQuerierAdapter implements guardian.ConcurrencyQuerier by
// delegating to *concurrency.GoroutineBudget.
type concurrencyQuerierAdapter struct {
	budget *concurrency.GoroutineBudget
}

func (a *concurrencyQuerierAdapter) GoroutineStats() guardian.GoroutineBudgetSnapshot {
	return guardian.GoroutineBudgetSnapshot{
		TotalActive: a.budget.TotalActive(),
		SystemLimit: a.budget.SystemLimit(),
		AgentCount:  a.budget.AgentCount(),
	}
}

func (a *concurrencyQuerierAdapter) LLMGateStats() guardian.LLMGateSnapshot {
	return guardian.LLMGateSnapshot{}
}

// ---------------------------------------------------------------------------
// Cost calculator — derives USD-cents from model pricing
// ---------------------------------------------------------------------------

// buildCostCalculator returns a CostCalculator that looks up per-model
// pricing from a static table. Returns 0 for unknown models. The table
// is built once at boot from a snapshot of known model prices.
func buildCostCalculator() guardian.CostCalculator {
	// Static pricing table (USD per million tokens).
	// Updated at compile-time; runtime registration is not needed because
	// the set of models used by a running instance is fixed at build.
	type pricing struct{ input, output float64 }
	prices := map[string]pricing{
		// Anthropic
		"claude-opus-4-6":           {15.0, 75.0},
		"claude-sonnet-4-6":         {3.0, 15.0},
		"claude-haiku-4-5-20251001": {0.80, 4.0},
		// OpenAI
		"gpt-5.4-pro": {2.50, 10.0},
		// Google
		"gemini-3.1-pro-preview": {1.25, 5.0},
		"gemini-3-flash":         {0.075, 0.30},
		"gemini-2.5-pro":         {1.25, 5.0},
		"gemini-2.5-flash":       {0.075, 0.30},
	}

	return func(model string, usage *providers.Usage) int64 {
		p, ok := prices[model]
		if !ok || usage == nil {
			return 0
		}
		// Cache reads cost 90% less at Anthropic, 50% less at OpenAI/Google.
		// Subtract cache-read tokens from full-price input and price separately.
		fullPriceInput := int64(usage.InputTokens - usage.CacheReadTokens)
		cacheReadInput := int64(usage.CacheReadTokens)
		cacheWriteInput := int64(usage.CacheWriteTokens)

		costUSD := float64(fullPriceInput) / 1_000_000 * p.input
		costUSD += float64(cacheReadInput) / 1_000_000 * (p.input * 0.1)   // 90% discount
		costUSD += float64(cacheWriteInput) / 1_000_000 * (p.input * 1.25) // 25% surcharge
		costUSD += float64(usage.OutputTokens) / 1_000_000 * p.output
		// Convert to cents, rounding down (accumulation converges).
		return int64(costUSD * 100)
	}
}
