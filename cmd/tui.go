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
	"github.com/adalundhe/sylk/core/agents/identity"
	identityregistries "github.com/adalundhe/sylk/core/agents/identity/registries"
	"github.com/adalundhe/sylk/core/boot"
	"github.com/adalundhe/sylk/core/commandapproval"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/container/activation"
	"github.com/adalundhe/sylk/core/container/daemon"
	"github.com/adalundhe/sylk/core/container/network"
	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/fetch"
	forestsvc "github.com/adalundhe/sylk/core/forest"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/llm/accounting"
	"github.com/adalundhe/sylk/core/oauth"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/search/git"
	"github.com/adalundhe/sylk/core/security"
	"github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/core/storage"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/adalundhe/sylk/ui"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/fonts"
	"github.com/blevesearch/bleve/v2"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
)

var (
	tuiTheme string
	tuiMock  bool
)

func init() {
	rootCmd.Flags().StringVar(&tuiTheme, "theme", "dark", "Color theme (dark or light)")
	rootCmd.Flags().BoolVar(&tuiMock, "mock", false, "Run with mock backend (no real agents)")
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

type modelSwapperFunc func(ctx context.Context, agentType, modelID string) error

type modelSwapActivator interface {
	EnsureActive(ctx context.Context, agentType string) (*container.Container, error)
}

var errNoSwappableContainer = errors.New("no model-swappable agent found")

type bootstrapPhase1 struct {
	ctx         context.Context
	projectRoot string
	start       time.Time

	scope          *concurrency.GoroutineScope
	guideBus       guide.EventBus
	activityPub    events.ActivityPublisher
	streamMgr      *guide.StreamManager
	sessionMgr     *session.Manager
	defaultSession *session.Session
	descriptors    *handoff.DescriptorRegistry
	budget        *concurrency.GoroutineBudget
	containerReg  *container.ContainerRegistry
	creatorReg    *container.AgentCreatorRegistry
	serviceReg    *network.ServiceRegistry
	specReg       *container.AgentSpecRegistry
	quota         *container.ResourceQuota
	hookMut       *lifecycleHookMutator
	probeFact     *probeFactoryHolder
	runtime       *container.DefaultRuntime
	namespace     *network.NetworkNamespace
	daemonCtrl    *daemon.DaemonSetController
	daemonSpecMap map[string]container.ContainerSpec

	authRegistry *credentials.AuthRegistry
	guideCfg     providers.GoogleConfig
	hydrateOnce  *hydrateOnceCell
	hydratedRef  atomic.Pointer[providers.HydratedGoogleAuth]

	googleGateway    *gateway.ProviderGateway
	anthropicGateway *gateway.ProviderGateway
	openaiGateway    *gateway.ProviderGateway

	// Typed identity + accounting surface. Both are constructed at
	// phase4 once the default session id is known — phase1 leaves
	// them nil and the gateway hook is the plain activity publisher
	// until phase4 swaps in the MultiHook that fans out to the
	// accountant as well.
	identityFactory atomic.Pointer[identity.Factory]
	accountant      atomic.Pointer[accounting.Accountant]
	llmEventHookRef atomic.Pointer[providers.MultiHook]

	identityReg *container.AgentIdentityRegistry
	actCtrlRef  atomic.Pointer[activation.ActivationController]
	gitSubsRef  atomic.Pointer[gitBootResult]

	planStore        *architect.PlanStore
	knowledgeStore   *knowledge.KnowledgeStore
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend
	forest           *forestsvc.MemoryForest
	forestContent    *ctxpkg.UniversalContentStore
	forestVectorDB   *vectorgraphdb.VectorGraphDB

	guideRef      atomic.Pointer[guide.Guide]
	guardianRef   atomic.Pointer[guardian.Guardian]
	orchRef       atomic.Pointer[orchestrator.Orchestrator]
	quarantineRef atomic.Pointer[fetch.QuarantineBuffer]
	handoffRef    atomic.Pointer[handoff.HandoffSupervisor]
}

type bootstrapPhase2 struct {
	guideResult    daemonContainerResult
	orchResult     daemonContainerResult
	guardianResult daemonContainerResult
	actResult      activationCtrlResult
	fontRes        fontResult
	gitRes         gitBootResult
}

type bootstrapPhase3 struct {
	guide          *guide.Guide
	orch           *orchestrator.Orchestrator
	activationCtrl *activation.ActivationController
	activator      guide.PodActivator
	scribeFactory  agentShared.ScribeFactory
}

type bootstrapPhase4 struct {
	seeds         []ui.AgentSeed
	modelStore    *agentpkg.AgentModelStore
	modelSwapper  modelSwapperFunc
	knowledgeSync *librarian.KnowledgeSyncService
	phase4Done    chan struct{}
	supervisorRef atomic.Pointer[handoff.HandoffSupervisor]
	bootLogger    *agentlog.BootEventLogger
}

// busReadinessPublisher adapts knowledge.ReadinessPublisher to the guide EventBus.
type busReadinessPublisher struct {
	bus guide.EventBus
}

func (p *busReadinessPublisher) PublishKnowledgeReady(event knowledge.ReadinessEvent) {
	msg := guide.NewKnowledgeReadyMessage(&guide.KnowledgeReadyPayload{
		Level:     int(event.Level),
		Searchers: event.Searchers,
	})
	if err := p.bus.Publish(guide.TopicKnowledgeReady, msg); err != nil {
		slog.Warn("tui_knowledge_ready_publish_failed",
			"level", int(event.Level),
			"searchers", event.Searchers,
			"error", err.Error(),
		)
	}
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

func buildMemoryForest(projectRoot string, budget *concurrency.GoroutineBudget) (*forestsvc.MemoryForest, *ctxpkg.UniversalContentStore, *vectorgraphdb.VectorGraphDB, error) {
	sd := sylkdir.New(projectRoot)
	if err := sd.Init(); err != nil {
		return nil, nil, nil, fmt.Errorf("init sylk dir for memory forest: %w", err)
	}

	basePath := filepath.Join(sd.SessionPath("default"), "state", "memory_forest")
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, nil, nil, fmt.Errorf("create memory forest state dir: %w", err)
	}

	// The forest projection and content store both depend on the vectorgraph
	// nodes table, so they share a single session-scoped graph DB.
	contentVectorDB, err := vectorgraphdb.Open(filepath.Join(basePath, "content.sqlite"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open memory forest content vector db: %w", err)
	}

	forestLogger := slog.Default().With("component", "memory_forest")
	forest, err := forestsvc.New(forestsvc.Config{
		DB:        contentVectorDB.DB(),
		Logger:    forestLogger,
		MaxTraces: 2048,
	})
	if err != nil {
		_ = contentVectorDB.Close()
		return nil, nil, nil, fmt.Errorf("create memory forest: %w", err)
	}

	contentStore, err := ctxpkg.NewUniversalContentStore(ctxpkg.ContentStoreConfig{
		BlevePath:       filepath.Join(basePath, "documents.bleve"),
		VectorDB:        contentVectorDB,
		GoroutineBudget: budget,
		Logger:          forestLogger,
		Observer:        forest,
	})
	if err != nil {
		_ = forest.Close()
		_ = contentVectorDB.Close()
		return nil, nil, nil, fmt.Errorf("create memory forest content store: %w", err)
	}

	searcher := ctxpkg.NewTieredSearcher(ctxpkg.TieredSearcherConfig{
		HotCache: ctxpkg.NewDefaultHotCache(),
		Bleve:    contentStore.BleveIndex(),
	})
	forest.SetContentStore(contentStore)
	forest.SetSearcher(searcher)

	return forest, contentStore, contentVectorDB, nil
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
	loadBootstrapDotenv(projectRoot)

	phase1, err := buildBootstrapPhase1(ctx, projectRoot, start)
	if err != nil {
		if phase1 != nil {
			cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
		}
		return ui.Deps{}, nil, err
	}
	phase2, err := runBootstrapPhase2(phase1)
	if err != nil {
		return ui.Deps{}, nil, err
	}
	phase3, err := wireBootstrapPhase3(phase1, phase2)
	if err != nil {
		cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
		return ui.Deps{}, nil, err
	}
	phase4, err := startBootstrapPhase4(phase1, phase2, phase3)
	if err != nil {
		cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
		return ui.Deps{}, nil, err
	}
	slog.Info("bootstrap critical path complete", "elapsed", time.Since(start))
	return buildBootstrapDeps(phase1, phase2, phase3, phase4), buildBootstrapCleanup(phase1, phase3, phase4), nil
}

func loadBootstrapDotenv(projectRoot string) {
	if err := boot.LoadDotenv(projectRoot); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to load .env.local", "error", err)
	}
}

func buildBootstrapPhase1(ctx context.Context, projectRoot string, start time.Time) (*bootstrapPhase1, error) {
	phase1 := &bootstrapPhase1{
		ctx:         ctx,
		projectRoot: projectRoot,
		start:       start,
	}

	phase1.scope = concurrency.NewGoroutineScope(ctx, "tui", nil)
	phase1.scope.SetMaxLifetime(24 * time.Hour)

	phase1.guideBus = guide.NewChannelBus(guide.DefaultChannelBusConfig())
	phase1.activityPub = events.NewMetadataCachingPublisher(
		guide.NewBusActivityPublisher(phase1.guideBus),
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
	phase1.streamMgr = guide.NewStreamManager(guide.DefaultStreamConfig())
	phase1.sessionMgr = session.NewManager(session.ManagerConfig{Scope: phase1.scope})
	phase1.descriptors = handoff.NewDescriptorRegistry()

	// Create the default session eagerly so the identity Factory can
	// bind to it at phase1. Every agent constructor — daemon or
	// on-demand — receives a non-nil Factory; there is no late-bind
	// path. If session.Manager.Create fails, the whole bootstrap
	// fails loud rather than silently leaving Factory nil.
	defaultSession, err := phase1.sessionMgr.Create(ctx, session.BootstrapDefaultConfig())
	if err != nil {
		return phase1, fmt.Errorf("create default session: %w", err)
	}
	phase1.defaultSession = defaultSession
	_ = phase1.sessionMgr.Switch(defaultSession.ID())
	factory, err := buildIdentityFactory(phase1.descriptors, defaultSession.ID())
	if err != nil {
		return phase1, fmt.Errorf("identity factory: %w", err)
	}
	phase1.identityFactory.Store(factory)

	pressureLevel := new(atomic.Int32)
	phase1.budget = concurrency.NewGoroutineBudget(pressureLevel)
	phase1.budget.RegisterAgent("tui", "tui")
	phase1.scope.SetBudget(phase1.budget)
	phase1.containerReg = container.NewContainerRegistry()
	phase1.serviceReg = network.NewServiceRegistry()
	phase1.specReg = container.NewAgentSpecRegistry(phase1.descriptors)
	phase1.quota = container.NewResourceQuota(quotaFromSpecs(phase1.specReg))
	phase1.quota.SetContextArchetypeMatcher(activation.NewActivationPredictor(activation.PredictorConfig{}))
	if observer := agentShared.NewContextBudgetObserver("tui.context_budget", phase1.guideBus, phase1.quota); observer != nil {
		if err := observer.Start(); err != nil {
			slog.Warn("failed to start context budget observer", "error", err)
		}
	}

	creatorReg := container.NewAgentCreatorRegistry()
	phase1.creatorReg = creatorReg
	phase1.hookMut = &lifecycleHookMutator{serviceReg: phase1.serviceReg}
	admission := container.NewAdmissionController(nil, []container.SpecMutator{phase1.hookMut})
	phase1.probeFact = &probeFactoryHolder{}

	phase1.runtime = container.NewDefaultRuntime(container.DefaultRuntimeConfig{
		Budget:       phase1.budget,
		Registry:     phase1.containerReg,
		Quota:        phase1.quota,
		Admission:    admission,
		CreateAgent:  creatorReg.Creator(),
		ProbeFactory: phase1.probeFact.Build,
		ParentCtx:    ctx,
	})

	policies := container.BuildNetworkPolicies(phase1.descriptors.All())
	busBridge := network.NewBusBridge(func(topic, sourceAgent, targetAgent string, payload []byte) error {
		msg := guide.NewBridgeMessage(sourceAgent, targetAgent, payload)
		return phase1.guideBus.Publish(topic, msg)
	})
	phase1.namespace = network.NewNetworkNamespace(network.NetworkNamespaceConfig{
		PodID:    "system",
		Policies: policies,
		Sink:     busBridge,
	})

	phase1.daemonCtrl = daemon.NewDaemonSetController(phase1.runtime, phase1.containerReg)
	daemonSpecs := daemon.AgentDaemonSetSpecs(phase1.specReg)
	for _, spec := range daemonSpecs {
		phase1.daemonCtrl.Apply(spec)
	}
	phase1.daemonSpecMap = make(map[string]container.ContainerSpec, len(daemonSpecs))
	for _, spec := range daemonSpecs {
		phase1.daemonSpecMap[spec.Name] = spec.ContainerSpec
	}

	authResolver := buildAuthResolver()
	authPublisher := chainPublishers(
		buildAuthPublisher(phase1.guideBus),
		buildOnDemandAuthRefresher(phase1.containerReg),
	)
	phase1.authRegistry = credentials.NewAuthRegistry(authResolver, nil, authPublisher, slog.Default())
	phase1.authRegistry.PrimeAll()

	phase1.guideCfg = defaultGuideGoogleConfig(phase1.authRegistry)
	phase1.hydrateOnce = newHydrateOnce()

	googleGatewayCfg := gateway.DefaultGoogleAPIKeyConfig()
	if phase1.guideCfg.AuthMode == providers.GoogleAuthModeOAuth {
		googleGatewayCfg = gateway.DefaultGoogleOAuthConfig()
	}
	phase1.googleGateway = gateway.NewProviderGateway(googleGatewayCfg, slog.Default())
	phase1.anthropicGateway = gateway.NewProviderGateway(gateway.DefaultAnthropicConfig(), slog.Default())
	phase1.openaiGateway = gateway.NewProviderGateway(gateway.DefaultOpenAIConfig(), slog.Default())

	llmEventHook := providers.NewLLMEventPublisherHook(
		providers.NewLLMEventPublisher(phase1.activityPub),
	)
	phase1.googleGateway.SetEventHook(llmEventHook)
	phase1.anthropicGateway.SetEventHook(llmEventHook)
	phase1.openaiGateway.SetEventHook(llmEventHook)

	phase1.identityReg = container.NewAgentIdentityRegistry([]string{
		"architect", "engineer", "designer", "inspector", "tester",
		"inspector-pipeline", "tester-pipeline",
		"librarian", "archivalist", "academic", "orchestrator",
	})
	planLeaseManager := architect.NewPlanLeaseManager(architect.DefaultLLMRequestTimeout, architect.ReadyPlanMaxAge)
	phase1.planStore = architect.NewPlanStore(projectRoot, planLeaseManager, slog.Default())
	phase1.knowledgeStore = knowledge.NewKnowledgeStore(
		&busReadinessPublisher{bus: phase1.guideBus},
		slog.Default(),
	)
	phase1.knowledgeBackend = knowledgeruntime.NewCommittedKnowledgeBackend(projectRoot, slog.Default())
	forest, forestContent, forestVectorDB, err := buildMemoryForest(projectRoot, phase1.budget)
	if err != nil {
		return phase1, fmt.Errorf("bootstrap memory forest: %w", err)
	}
	phase1.forest = forest
	phase1.forestContent = forestContent
	phase1.forestVectorDB = forestVectorDB

	registerAgentCreators(
		creatorReg,
		phase1.identityReg,
		phase1.guideBus,
		phase1.activityPub,
		projectRoot,
		phase1.hydrateOnce,
		&phase1.hydratedRef,
		phase1.googleGateway,
		phase1.anthropicGateway,
		phase1.openaiGateway,
		phase1.authRegistry,
		&phase1.actCtrlRef,
		&phase1.gitSubsRef,
		phase1.planStore,
		phase1.knowledgeStore,
		phase1.knowledgeBackend,
		phase1.forest,
		phase1.daemonCtrl,
		&phase1.guideRef,
		&phase1.guardianRef,
		&phase1.orchRef,
		&phase1.quarantineRef,
		phase1.quota,
		phase1.budget,
		&phase1.identityFactory,
	)

	slog.Info("bootstrap phase 1 complete", "elapsed", time.Since(start))
	return phase1, nil
}

func startDaemonContainerBootstrap(
	ctx context.Context,
	runtime *container.DefaultRuntime,
	specMap map[string]container.ContainerSpec,
	name string,
	ch chan<- daemonContainerResult,
) {
	go func() {
		spec, ok := specMap[name]
		if !ok {
			ch <- daemonContainerResult{err: fmt.Errorf("no daemon spec for %s", name)}
			return
		}
		c, err := runtime.CreateContainer(ctx, spec)
		if err != nil {
			ch <- daemonContainerResult{err: err}
			return
		}
		if err := runtime.StartContainer(ctx, c); err != nil {
			_ = runtime.RemoveContainer(ctx, c)
			ch <- daemonContainerResult{err: err}
			return
		}
		ch <- daemonContainerResult{c: c}
	}()
}

func startActivationControllerBootstrap(phase1 *bootstrapPhase1, ch chan<- activationCtrlResult) {
	go func() {
		activationPolicies, err := activation.AgentActivationPolicies(phase1.descriptors.All())
		if err != nil {
			ch <- activationCtrlResult{err: err}
			return
		}
		storageDir, err := activationStorageDir()
		if err != nil {
			ch <- activationCtrlResult{err: err}
			return
		}
		ctrl, err := activation.NewActivationController(activation.ActivationControllerConfig{
			Runtime:    phase1.runtime,
			Registry:   phase1.containerReg,
			Scope:      phase1.scope,
			Policies:   activationPolicies,
			StorageDir: storageDir,
		})
		if err != nil {
			ch <- activationCtrlResult{err: err}
			return
		}
		if err := ctrl.Start(phase1.budget, phase1.quota); err != nil {
			ch <- activationCtrlResult{err: err}
			return
		}
		ch <- activationCtrlResult{ctrl: ctrl}
	}()
}

func startHealthSyncerBootstrap(phase1 *bootstrapPhase1, ch chan<- error) {
	go func() {
		syncer := container.NewHealthSyncer(container.HealthSyncerConfig{
			ContainerRegistry: phase1.containerReg,
			ServiceRegistry:   phase1.serviceReg,
			Scope:             phase1.scope,
		})
		ch <- syncer.Start()
	}()
}

func startFontBootstrap(ch chan<- fontResult) {
	go func() {
		ch <- fontResult{detected: fonts.Detected()}
	}()
}

func startGitBootstrap(projectRoot string, gitSubsRef *atomic.Pointer[gitBootResult], ch chan<- gitBootResult) {
	go func() {
		var result gitBootResult
		gc, err := git.NewGitClient(projectRoot)
		if err != nil || !gc.IsGitRepo() {
			ch <- result
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
		ch <- result
	}()
}

func startHydrationBootstrap(
	ctx context.Context,
	guideCfg providers.GoogleConfig,
	hydrateOnce *hydrateOnceCell,
	hydratedRef *atomic.Pointer[providers.HydratedGoogleAuth],
	ch chan<- hydrateResult,
) {
	const (
		hydrateMaxAttempts = 3
		hydrateRetryDelay  = 500 * time.Millisecond
	)

	go func() {
		var hydrated *providers.HydratedGoogleAuth
		for attempt := 1; attempt <= hydrateMaxAttempts; attempt++ {
			result, err := providers.HydrateGoogleAuth(ctx, guideCfg)
			if err == nil {
				hydrated = result
				hydratedRef.Store(hydrated)
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
		hydrateOnce.resolve(hydrated)
		ch <- hydrateResult{hydrated: hydrated}
	}()
}

func cleanupPhase2Critical(phase1 *bootstrapPhase1, phase2 bootstrapPhase2) {
	for _, c := range []*container.Container{phase2.guideResult.c, phase2.orchResult.c} {
		if c != nil {
			_ = phase1.runtime.StopContainer(context.Background(), c)
			_ = phase1.runtime.RemoveContainer(context.Background(), c)
		}
	}
	cleanupInfra(phase1.runtime, phase1.containerReg, phase1.namespace, phase1.guideBus)
}

func runBootstrapPhase2(phase1 *bootstrapPhase1) (bootstrapPhase2, error) {
	phase2Start := time.Now()
	_, parallelCancel := context.WithCancel(phase1.ctx)
	defer parallelCancel()

	guideCh := make(chan daemonContainerResult, 1)
	orchCh := make(chan daemonContainerResult, 1)
	guardianCh := make(chan daemonContainerResult, 1)
	activationCh := make(chan activationCtrlResult, 1)
	healthCh := make(chan error, 1)
	fontCh := make(chan fontResult, 1)
	gitCh := make(chan gitBootResult, 1)
	hydrateCh := make(chan hydrateResult, 1)

	startDaemonContainerBootstrap(phase1.ctx, phase1.runtime, phase1.daemonSpecMap, "guide", guideCh)
	startDaemonContainerBootstrap(phase1.ctx, phase1.runtime, phase1.daemonSpecMap, "orchestrator", orchCh)
	startDaemonContainerBootstrap(phase1.ctx, phase1.runtime, phase1.daemonSpecMap, "guardian", guardianCh)
	startActivationControllerBootstrap(phase1, activationCh)
	startHealthSyncerBootstrap(phase1, healthCh)
	startFontBootstrap(fontCh)
	startGitBootstrap(phase1.projectRoot, &phase1.gitSubsRef, gitCh)
	startHydrationBootstrap(phase1.ctx, phase1.guideCfg, phase1.hydrateOnce, &phase1.hydratedRef, hydrateCh)

	var (
		phase2      bootstrapPhase2
		criticalErr error
	)

	for completed := 0; completed < 8; completed++ {
		select {
		case r := <-guideCh:
			phase2.guideResult = r
			if r.err != nil && criticalErr == nil {
				criticalErr = fmt.Errorf("guide container: %w", r.err)
				parallelCancel()
			}
			guideCh = nil
		case r := <-orchCh:
			phase2.orchResult = r
			if r.err != nil && criticalErr == nil {
				criticalErr = fmt.Errorf("orchestrator container: %w", r.err)
				parallelCancel()
			}
			orchCh = nil
		case r := <-guardianCh:
			phase2.guardianResult = r
			if r.err != nil {
				slog.Warn("guardian container failed during parallel bootstrap", "error", r.err)
			}
			guardianCh = nil
		case r := <-activationCh:
			phase2.actResult = r
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
			phase2.fontRes = r
			fontCh = nil
		case r := <-gitCh:
			phase2.gitRes = r
			gitCh = nil
		case <-hydrateCh:
			hydrateCh = nil
		}
	}

	if criticalErr != nil {
		cleanupPhase2Critical(phase1, phase2)
		return phase2, criticalErr
	}

	phase1.daemonCtrl.InjectInstance("guide", phase2.guideResult.c)
	phase1.daemonCtrl.InjectInstance("orchestrator", phase2.orchResult.c)
	if phase2.guardianResult.err == nil && phase2.guardianResult.c != nil {
		phase1.daemonCtrl.InjectInstance("guardian", phase2.guardianResult.c)
	}

	slog.Info("bootstrap phase 2 complete", "elapsed", time.Since(phase2Start))
	return phase2, nil
}

func wireBootstrapPhase3(phase1 *bootstrapPhase1, phase2 bootstrapPhase2) (bootstrapPhase3, error) {
	phase3Start := time.Now()

	g, err := extractAgent[*guide.Guide](phase1.containerReg, "guide")
	if err != nil {
		return bootstrapPhase3{}, fmt.Errorf("extract guide: %w", err)
	}
	phase1.guideRef.Store(g)

	orch, err := extractAgent[*orchestrator.Orchestrator](phase1.containerReg, "orchestrator")
	if err != nil {
		return bootstrapPhase3{}, fmt.Errorf("extract orchestrator: %w", err)
	}
	phase1.orchRef.Store(orch)

	phase1.hookMut.SetGuide(g)
	phase1.probeFact.SetIsReady(g.IsAgentReady)
	if err := registerOrchestratorWithGuide(g, orch); err != nil {
		return bootstrapPhase3{}, fmt.Errorf("register orchestrator: %w", err)
	}

	phase3 := bootstrapPhase3{
		guide:          g,
		orch:           orch,
		activationCtrl: phase2.actResult.ctrl,
	}
	if phase3.activationCtrl != nil {
		phase1.actCtrlRef.Store(phase3.activationCtrl)
		if _, err := phase3.activationCtrl.AdoptContainer("guide", phase2.guideResult.c); err != nil {
			slog.Warn("adopt guide container", "error", err)
		}
		if _, err := phase3.activationCtrl.AdoptContainer("orchestrator", phase2.orchResult.c); err != nil {
			slog.Warn("adopt orchestrator container", "error", err)
		}
		if phase2.guardianResult.err == nil && phase2.guardianResult.c != nil {
			if _, err := phase3.activationCtrl.AdoptContainer("guardian", phase2.guardianResult.c); err != nil {
				slog.Warn("adopt guardian container", "error", err)
			}
		}
	}

	preRegisterAgentRouting(g, phase1.identityReg)

	if phase2.guardianResult.err == nil && phase2.guardianResult.c != nil {
		if grd, grdErr := extractAgent[*guardian.Guardian](phase1.containerReg, "guardian"); grdErr == nil {
			phase1.guardianRef.Store(grd)
			if phase2.gitRes.bus != nil {
				grd.SetGitSubsystems(phase2.gitRes.bus, phase2.gitRes.watcher)
			}
			wireGuardianPostBootDeps(
				grd,
				&phase1.actCtrlRef,
				phase1.daemonCtrl,
				&phase1.guideRef,
				&phase1.orchRef,
				phase1.authRegistry,
				phase1.openaiGateway,
				[]*gateway.ProviderGateway{phase1.googleGateway, phase1.anthropicGateway, phase1.openaiGateway},
				phase1.knowledgeStore,
				phase1.budget,
			)
		}
	}

	if phase3.activationCtrl != nil {
		phase3.activator = activation.NewControllerPodActivator(phase3.activationCtrl)
		g.SetActivator(phase3.activator)
	}
	g.SetServiceRegistry(phase1.serviceReg)
	g.SetProviderWrapper(phase1.googleGateway.Wrapper(gateway.PriorityUserInteractive))
	g.SetAgentRegistrar(func(podID, agentType string) {
		c := findRegisteredPodContainer(phase1.containerReg, podID, agentType)
		if c == nil {
			return
		}
		agent := c.Agent()
		if router, ok := agent.(guide.AgentRouter); ok {
			_ = registerAgentWithGuide(g, router, agentType)
		}
	})

	orch.SetProviderRefresher(func(refreshCtx context.Context, authMethod string) {
		refreshOrchestratorProvider(refreshCtx, orch, phase1.googleGateway, phase1.authRegistry, authMethod)
	})
	orch.SetTaskRouter(orchestrator.NewTaskRouter(orchestrator.TaskRouterConfig{
		Bus:                    phase1.guideBus,
		Scope:                  phase1.scope,
		AgentID:                orch.AgentID(),
		SessionID:              "default",
		OnVisibleRoutePublish:  orch.ActivatePublishedReviewCandidate,
		OnVisibleRouteTerminal: orch.HandleCheckpointReviewTerminal,
	}))
	if phase3.activator != nil {
		orch.SetActivator(phase3.activator)
	}
	orch.SetRegistrar(func(ctx context.Context, podID, agentType string) error {
		c := findRegisteredPodContainer(phase1.containerReg, podID, agentType)
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
	orch.SetTaskPodInfra(phase1.runtime, phase1.specReg, func(sessionID string) *versioning.SessionVFS {
		return orch.EnsureSessionVFS(sessionID, phase1.projectRoot)
	})
	orch.SetContextQuota(phase1.quota)

	phase3.scribeFactory = buildScribeFactory(
		phase1.ctx,
		phase1.googleGateway,
		phase1.authRegistry,
		phase1.guideBus,
		phase1.scope,
		phase1.forest,
		&phase1.handoffRef,
	)
	if phase3.scribeFactory != nil {
		orch.SetScribeFactory(phase3.scribeFactory)
	}

	slog.Info("bootstrap phase 3 complete", "elapsed", time.Since(phase3Start))
	return phase3, nil
}

func schedulePhase4Task(
	scope *concurrency.GoroutineScope,
	name string,
	timeout time.Duration,
	remaining *atomic.Int32,
	onDone func(),
	task func(context.Context) error,
) {
	remaining.Add(1)
	_ = scope.Go(name, timeout, func(bgCtx context.Context) error {
		defer onDone()
		return task(bgCtx)
	})
}

func waitForBootBleveReady(ctx context.Context, store *knowledge.KnowledgeStore) error {
	if store == nil {
		return nil
	}
	if err := store.WaitForPartial(ctx); err != nil {
		return nil
	}
	bgWaiter := store.BackgroundWaiter()
	if bgWaiter == nil {
		if ctx.Err() != nil {
			return nil
		}
		store.PromoteFull()
		return nil
	}

	select {
	case <-bgWaiter.Ready():
		if ctx.Err() != nil {
			return nil
		}
		// Full promotion is the only user-visible transition needed here.
		// Reopening/adopting a fresh owned Bleve store during shutdown can block
		// on repository-sized I/O, so cleanup relies on backend.Close instead.
		store.PromoteFull()
	case <-ctx.Done():
	}
	return nil
}

func startLibrarianKnowledgeSync(phase1 *bootstrapPhase1, phase4 *bootstrapPhase4, initialSync bool) {
	if phase4 == nil || phase4.knowledgeSync != nil || phase1 == nil || phase1.knowledgeStore == nil || phase1.knowledgeBackend == nil {
		return
	}
	gitRes := phase1.gitSubsRef.Load()
	if gitRes == nil || gitRes.watcher == nil {
		slog.Warn("librarian knowledge sync unavailable", "reason", "git watcher unavailable")
		return
	}
	syncSvc, err := librarian.NewKnowledgeSyncService(librarian.KnowledgeSyncConfig{
		ProjectRoot: phase1.projectRoot,
		Watcher:     gitRes.watcher,
		Store:       phase1.knowledgeStore,
		Backend:     phase1.knowledgeBackend,
		Scope:       phase1.scope,
		Logger:      slog.Default(),
		InitialSync: initialSync,
	})
	if err != nil {
		slog.Warn("librarian knowledge sync setup failed", "error", err)
		return
	}
	if err := syncSvc.Start(); err != nil {
		slog.Warn("librarian knowledge sync start failed", "error", err)
		return
	}
	phase4.knowledgeSync = syncSvc
}

func schedulePhase4Activation(
	phase1 *bootstrapPhase1,
	phase3 bootstrapPhase3,
	handoffReady <-chan struct{},
	name string,
	timeout time.Duration,
	remaining *atomic.Int32,
	activationsRemaining *atomic.Int32,
	activationsDone chan struct{},
	onDone func(),
	logTiming bool,
	onReady func(context.Context) error,
) {
	activationsRemaining.Add(1)
	schedulePhase4Task(phase1.scope, "phase4-activate-"+name, timeout, remaining, onDone, func(bgCtx context.Context) error {
		defer func() {
			if activationsRemaining.Add(-1) == 0 {
				close(activationsDone)
			}
		}()

		if handoffReady != nil {
			select {
			case <-handoffReady:
			case <-bgCtx.Done():
				return nil
			}
		}

		start := time.Now()
		if logTiming {
			deadline, hasDeadline := bgCtx.Deadline()
			slog.Info("phase4: "+name+" activation start", "has_deadline", hasDeadline, "deadline", deadline)
		}
		if _, err := phase3.activationCtrl.EnsureActive(bgCtx, name); err != nil {
			attrs := []any{"error", err}
			if logTiming {
				attrs = append(attrs, "elapsed_ms", time.Since(start).Milliseconds())
			}
			slog.Warn("phase4: "+name+" activation failed", attrs...)
			return nil
		}
		if logTiming {
			slog.Info("phase4: "+name+" activation done", "elapsed_ms", time.Since(start).Milliseconds())
		}
		if bgCtx.Err() != nil || onReady == nil {
			return nil
		}
		return onReady(bgCtx)
	})
}

func registerPhase4Architect(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	arch, err := extractAgent[*architect.Architect](phase1.containerReg, "architect")
	if err != nil {
		return nil
	}
	_ = registerArchitectWithGuide(phase3.guide, arch)
	wireGlobalAgentPod(arch, "architect", phase3.scribeFactory, phase3.activator, phase1.activityPub, slog.Default())
	return nil
}

func registerPhase4Inspector(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	inspectorAgent, err := extractAgent[*inspectorGlobal.GlobalInspector](phase1.containerReg, "inspector")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, inspectorAgent, "inspector")
	wireGlobalAgentPod(inspectorAgent, "inspector", phase3.scribeFactory, phase3.activator, phase1.activityPub, slog.Default())
	return nil
}

func registerPhase4Tester(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	testerAgent, err := extractAgent[*globaltester.GlobalTester](phase1.containerReg, "tester")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, testerAgent, "tester")
	wireGlobalAgentPod(testerAgent, "tester", phase3.scribeFactory, phase3.activator, phase1.activityPub, slog.Default())
	return nil
}

func registerPhase4Librarian(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	lib, err := extractAgent[*librarian.Librarian](phase1.containerReg, "librarian")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, lib, "librarian")
	wireGlobalAgentPod(lib, "librarian", phase3.scribeFactory, phase3.activator, phase1.activityPub, slog.Default())
	return nil
}

func registerPhase4Archivalist(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	archivalistAgent, err := extractAgent[*archivalist.Archivalist](phase1.containerReg, "archivalist")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, archivalistAgent, "archivalist")
	return nil
}

func registerPhase4Academic(phase1 *bootstrapPhase1, phase3 bootstrapPhase3) error {
	academicAgent, err := extractAgent[*academic.Academic](phase1.containerReg, "academic")
	if err != nil {
		return nil
	}
	_ = registerAgentWithGuide(phase3.guide, academicAgent, "academic")
	return nil
}

// buildIdentityFactory constructs the session-scoped identity.Factory.
// Called from phase1 (before any agent is created) so every agent
// constructor receives a non-nil Factory at Config time. The factory
// is session-scoped — one per session — aligned with the K8s
// namespace model (spec FIX_ID_AND_TOKENS.md).
func buildIdentityFactory(descriptors *handoff.DescriptorRegistry, sessionID string) (*identity.Factory, error) {
	ns := identity.Namespace(sessionID)
	models := identityregistries.NewHandoffModelRegistry(descriptors)
	pods := identityregistries.NewStaticPodRegistry(
		map[identity.PodID]identity.PodType{
			"guide":        identity.PodTypeDaemon,
			"orchestrator": identity.PodTypeDaemon,
			"guardian":     identity.PodTypeDaemon,
			"architect":    identity.PodTypeSingleton,
			"inspector":    identity.PodTypeSingleton,
			"tester":       identity.PodTypeSingleton,
			"librarian":    identity.PodTypeSingleton,
			"archivalist":  identity.PodTypeSingleton,
			"academic":     identity.PodTypeSingleton,
			"pipeline":     identity.PodTypePipeline,
		},
		"pipeline",
	)
	return identity.NewFactory(identity.FactoryConfig{
		Namespace: ns,
		Models:    models,
		Pods:      pods,
	})
}

// wireAccounting constructs the session-scoped accounting.Accountant,
// builds the MultiHook that fans every provider event to the
// accountant + activity publisher, and swaps the gateway hooks.
// Called from phase4 — accountant depends on the per-session WAL
// path and doesn't need to exist until after daemons are up.
func wireAccounting(phase1 *bootstrapPhase1, sessionID string) error {
	factory := phase1.identityFactory.Load()
	if factory == nil {
		return fmt.Errorf("identity factory not initialized")
	}
	ns := identity.Namespace(sessionID)

	walPath := filepath.Join(phase1.projectRoot, ".sylk", "sessions", sessionID, "accounting", "wal.jsonl")
	wal, err := accounting.OpenFileWAL(walPath)
	if err != nil {
		return fmt.Errorf("accounting wal: %w", err)
	}
	acc, err := accounting.New(accounting.Config{
		Namespace: ns,
		WAL:       wal,
		Logger:    slog.Default(),
	})
	if err != nil {
		_ = wal.Close()
		return fmt.Errorf("accountant: %w", err)
	}

	activityHook := providers.NewLLMEventPublisherHook(
		providers.NewLLMEventPublisher(phase1.activityPub),
	)
	accountantHook := accounting.NewHook(acc, slog.Default())
	multi := providers.NewMultiHook(accountantHook, activityHook)
	phase1.googleGateway.SetEventHook(multi)
	phase1.anthropicGateway.SetEventHook(multi)
	phase1.openaiGateway.SetEventHook(multi)

	phase1.accountant.Store(acc)
	phase1.llmEventHookRef.Store(multi)
	_ = factory // factory already stored at phase1
	return nil
}

func startBootstrapPhase4(
	phase1 *bootstrapPhase1,
	phase2 bootstrapPhase2,
	phase3 bootstrapPhase3,
) (*bootstrapPhase4, error) {
	phase4Start := time.Now()
	// The default session + identity.Factory were created eagerly at
	// phase1 so daemon agents receive a non-nil Factory at New() time
	// (see buildBootstrapPhase1). phase4 just wires the Accountant
	// and the gateway MultiHook against the already-live factory.
	defaultSession := phase1.defaultSession
	if defaultSession == nil {
		return nil, fmt.Errorf("default session not initialized at phase1")
	}
	if err := wireAccounting(phase1, defaultSession.ID()); err != nil {
		return nil, fmt.Errorf("accounting: %w", err)
	}
	phase3.orch.SignalReady()

	modelStore := agentpkg.NewAgentModelStore(filepath.Join(phase1.projectRoot, ".sylk", "config.yaml"))
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
	populateSeedModels(seeds, phase1.containerReg)
	for i := range seeds {
		entry := modelStore.EntryFor(seeds[i].AgentType)
		seeds[i].PersistedModelID = resolvePersistedModelForCurrentAuth(entry.Model, entry.Provider, phase1.authRegistry)
		seeds[i].PersistedProviderID = entry.Provider
	}
	agentDefaults := make(map[string]agentpkg.AgentConfigEntry, len(seeds))
	for _, seed := range seeds {
		if dflt := agentpkg.DefaultModelForAgentType(seed.AgentType); dflt != "" {
			agentDefaults[seed.AgentType] = agentpkg.AgentConfigEntry{
				Provider: agentpkg.DeriveProvider(dflt),
				Model:    dflt,
			}
		}
	}
	if err := modelStore.EnsureDefaults(agentDefaults); err != nil {
		slog.Warn("agent model config: ensure defaults", "error", err)
	}

	phase4 := &bootstrapPhase4{
		seeds:        seeds,
		modelStore:   modelStore,
		modelSwapper: buildModelSwapper(phase1.containerReg, phase3.activationCtrl, phase1.authRegistry, phase1.googleGateway, phase1.anthropicGateway, phase1.openaiGateway),
		phase4Done:   make(chan struct{}),
	}

	bootLogger, bootLogErr := agentlog.NewBootEventLogger(filepath.Join(phase1.projectRoot, ".sylk"))
	if bootLogErr != nil {
		slog.Warn("boot logger init failed (non-critical)", "error", bootLogErr)
	}
	phase4.bootLogger = bootLogger
	phase1.knowledgeStore.SetBootLogger(bootLogger)

	var (
		phase4Remaining      atomic.Int32
		activationsRemaining atomic.Int32
	)
	activationsDone := make(chan struct{})
	handoffReady := make(chan struct{})
	phase4Finish := func() {
		if phase4Remaining.Add(-1) > 0 {
			return
		}
		slog.Info("bootstrap phase 4 background complete", "elapsed", time.Since(phase4Start))
		close(phase4.phase4Done)
	}

	const phase4ActivationTimeout = 45 * time.Second
	if phase3.activationCtrl != nil {
		schedulePhase4Activation(phase1, phase3, handoffReady, "architect", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, true, func(context.Context) error {
			return registerPhase4Architect(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "inspector", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Inspector(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "tester", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Tester(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "librarian", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Librarian(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "archivalist", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Archivalist(phase1, phase3)
		})
		schedulePhase4Activation(phase1, phase3, handoffReady, "academic", phase4ActivationTimeout, &phase4Remaining, &activationsRemaining, activationsDone, phase4Finish, false, func(context.Context) error {
			return registerPhase4Academic(phase1, phase3)
		})
	} else {
		close(activationsDone)
	}

	schedulePhase4Task(phase1.scope, "phase4-handoff-supervisor", 0, &phase4Remaining, phase4Finish, func(context.Context) error {
		sup := bootstrapHandoffSupervisor(
			phase3.guide,
			phase1.guideBus,
			phase1.serviceReg,
			phase1.containerReg,
			phase1.creatorReg,
			phase3.activationCtrl,
		)
		if sup != nil {
			phase4.supervisorRef.Store(sup)
			phase1.handoffRef.Store(sup)
			if phase3.activationCtrl != nil {
				phase3.activationCtrl.SetLifecycleCallbacks(
					func(c *container.Container) { registerHandoffContainer(sup, c) },
					func(c *container.Container) { unregisterHandoffContainer(sup, c) },
				)
			}
		}
		close(handoffReady)
		return nil
	})
	schedulePhase4Task(phase1.scope, "phase4-auth-probe", 0, &phase4Remaining, phase4Finish, func(context.Context) error {
		phase1.authRegistry.ProbeAll()
		return nil
	})
	schedulePhase4Task(phase1.scope, "phase4-boot-model-swap", phase4ActivationTimeout, &phase4Remaining, phase4Finish, func(bgCtx context.Context) error {
		select {
		case <-activationsDone:
		case <-bgCtx.Done():
			return nil
		}
		for _, seed := range phase4.seeds {
			if bgCtx.Err() != nil {
				return nil
			}
			dflt := agentpkg.DefaultModelForAgentType(seed.AgentType)
			persisted := phase4.modelStore.ModelFor(seed.AgentType)
			if persisted == "" || persisted == dflt {
				continue
			}
			if err := phase4.modelSwapper(bgCtx, seed.AgentType, persisted); err != nil {
				slog.Warn("boot model swap failed", "agent", seed.AgentType, "model", persisted, "error", err)
			} else {
				slog.Info("boot model swap applied", "agent", seed.AgentType, "model", persisted)
			}
		}
		return nil
	})
	schedulePhase4Task(phase1.scope, "phase4-knowledge-boot", 0, &phase4Remaining, phase4Finish, func(bgCtx context.Context) error {
		result, err := boot.BootWithConfig(bgCtx, boot.PipelineConfig{
			ProjectRoot: phase1.projectRoot,
			Logger:      phase4.bootLogger,
			OnProgress:  phase1.knowledgeStore.NotifyProgress,
			Scope:       phase1.scope,
		})
		if err != nil {
			slog.Warn("knowledge boot failed (non-critical)", "error", err)
			startLibrarianKnowledgeSync(phase1, phase4, true)
			return nil
		}
		backend := phase1.knowledgeBackend
		if backend == nil {
			slog.Warn("knowledge backend unavailable (non-critical)")
			startLibrarianKnowledgeSync(phase1, phase4, false)
			return nil
		}
		var refreshErr error
		if bgIdx := result.BackgroundIndexer; bgIdx != nil && bgIdx.BleveStore() != nil {
			refreshErr = backend.RefreshWithBleveStore(bgCtx, bgIdx.BleveStore())
		} else {
			refreshErr = backend.RefreshFromDisk(bgCtx)
		}
		if refreshErr != nil {
			slog.Warn("knowledge backend refresh failed (non-critical)", "error", refreshErr)
			startLibrarianKnowledgeSync(phase1, phase4, true)
			return nil
		}
		phase1.knowledgeStore.PromotePartial(query.NewBleveSearcher(backend), result.BackgroundIndexer, backend)
		startLibrarianKnowledgeSync(phase1, phase4, false)
		return nil
	})
	schedulePhase4Task(phase1.scope, "phase4-bleve-ready", 0, &phase4Remaining, phase4Finish, func(bgCtx context.Context) error {
		return waitForBootBleveReady(bgCtx, phase1.knowledgeStore)
	})

	return phase4, nil
}

func buildBootstrapCleanup(
	phase1 *bootstrapPhase1,
	phase3 bootstrapPhase3,
	phase4 *bootstrapPhase4,
) func() error {
	return func() error {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()

		select {
		case <-phase4.phase4Done:
		default:
		}

		var errs []error
		if sup := phase4.supervisorRef.Load(); sup != nil {
			if stopErr := sup.Stop(); stopErr != nil {
				errs = append(errs, stopErr)
			}
		}
		if phase3.activationCtrl != nil {
			if err := phase3.activationCtrl.Shutdown(shutdownCtx); err != nil {
				errs = append(errs, err)
			}
		}
		for _, c := range phase1.containerReg.All() {
			if c.IsRunning() {
				if err := phase1.runtime.StopContainer(shutdownCtx, c); err != nil {
					errs = append(errs, err)
				}
			}
			if err := phase1.runtime.RemoveContainer(shutdownCtx, c); err != nil {
				errs = append(errs, err)
			}
		}

		phase1.namespace.Close()
		phase1.runtime.Close()
		phase1.planStore.Close()
		if phase4.bootLogger != nil {
			_ = phase4.bootLogger.Close()
		}
		if phase4.knowledgeSync != nil {
			if err := phase4.knowledgeSync.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := phase1.knowledgeStore.Close(); err != nil {
			errs = append(errs, err)
		}
		if phase1.forest != nil {
			if err := phase1.forest.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if phase1.forestContent != nil {
			if err := phase1.forestContent.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if phase1.forestVectorDB != nil {
			if err := phase1.forestVectorDB.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if phase1.knowledgeBackend != nil {
			if err := phase1.knowledgeBackend.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		phase1.googleGateway.Stop()
		phase1.anthropicGateway.Stop()
		phase1.openaiGateway.Stop()
		if err := phase1.guideBus.Close(); err != nil {
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	}
}

func buildBootstrapDeps(
	phase1 *bootstrapPhase1,
	phase2 bootstrapPhase2,
	phase3 bootstrapPhase3,
	phase4 *bootstrapPhase4,
) ui.Deps {
	return ui.Deps{
		ActivityPub:        phase1.activityPub,
		SessionManager:     phase1.sessionMgr,
		GuideBus:           phase1.guideBus,
		StreamManager:      phase1.streamMgr,
		Guide:              phase3.guide,
		Scope:              phase1.scope,
		AuthRegistry:       phase1.authRegistry,
		Accountant:         phase1.accountant.Load(),
		InterruptAllAgents: makeInterruptAllAgentsFn(phase3.activationCtrl, phase1.guideBus),
		NerdFontsDetected:  phase2.fontRes.detected,
		GitClient:          phase2.gitRes.client,
		GitWatcher:         phase2.gitRes.watcher,
		GitBus:             phase2.gitRes.bus,
		SafetyGuard:        phase2.gitRes.guard,
		SeedAgents:         phase4.seeds,
		ModelSwap:          phase4.modelSwapper,
		ModelSave: func(agentType, provider, modelID string) {
			if err := phase4.modelStore.SetEntry(agentType, provider, modelID); err != nil {
				slog.Warn("agent model config: save", "agent", agentType, "provider", provider, "model", modelID, "error", err)
			}
		},
		AgentModelStore: phase4.modelStore,
		KnowledgeStore:  phase1.knowledgeStore,
		Forest:          phase1.forest,
	}
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
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend,
	forest agentShared.MemoryForestService,
	daemonCtrl *daemon.DaemonSetController,
	guideRef *atomic.Pointer[guide.Guide],
	guardianRef *atomic.Pointer[guardian.Guardian],
	orchRef *atomic.Pointer[orchestrator.Orchestrator],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
	quota *container.ResourceQuota,
	budget *concurrency.GoroutineBudget,
	factoryRef *atomic.Pointer[identity.Factory],
) {
	// Guide — Gemini with rule-based fallback.
	// First call blocks on hydrateOnce; subsequent calls (daemon restart)
	// read hydratedRef which is already populated.
	reg.Register("guide", func(ctx context.Context) (container.ContainerAgent, error) {
		h := hydrateOnce.result()
		if h == nil {
			h = hydratedRef.Load()
		}
		return bootstrapLiveGuide(ctx, bus, actPub, h, googleGw, authRegistry, projectRoot, forest, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		}, factoryRef.Load())
	})

	// Architect — Anthropic LLM planner. Reads through session-scoped global
	// overlay when a session is active, falling back to read-only disk access.
	architectID, _ := ids.Get("architect")
	reg.Register("architect", func(ctx context.Context) (container.ContainerAgent, error) {
		return bootstrapArchitect(ctx, architectID, bus, actPub, projectRoot, anthropicGw, authRegistry, actCtrlRef, planStore, forest, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		}, factoryRef)
	})

	// Orchestrator — pipeline coordinator.
	orchestratorID, _ := ids.Get("orchestrator")
	reg.Register("orchestrator", func(ctx context.Context) (container.ContainerAgent, error) {
		h := hydrateOnce.result()
		if h == nil {
			h = hydratedRef.Load()
		}
		return bootstrapOrchestrator(ctx, orchestratorID, bus, actPub, projectRoot, h, authRegistry, googleGw, forest, factoryRef.Load())
	})

	// Guardian — safety sidecar daemon.
	// On daemon restart the factory must re-wire post-boot deps (observability,
	// git, agent seeding) that Phase 3 originally set up.
	reg.Register("guardian", func(ctx context.Context) (container.ContainerAgent, error) {
		grd, err := bootstrapGuardian(ctx, bus, actPub, projectRoot, googleGw, anthropicGw, openaiGw, authRegistry, gitSubsRef, quarantineRef, forest, func(sessionID string) *versioning.SessionVFS {
			if orch := orchRef.Load(); orch != nil {
				return orch.GetSessionVFS(sessionID)
			}
			return nil
		}, factoryRef.Load())
		if err != nil {
			return nil, err
		}
		guardianRef.Store(grd)
		wireGuardianPostBootDeps(grd, actCtrlRef, daemonCtrl, guideRef, orchRef, authRegistry, openaiGw,
			[]*gateway.ProviderGateway{googleGw, anthropicGw, openaiGw}, knowledgeStore, budget)
		return grd, nil
	})

	// On-demand agents — created lazily by the ActivationController.
	registerOnDemandAgentCreators(reg, ids, bus, actPub, projectRoot, googleGw, anthropicGw, openaiGw, authRegistry, actCtrlRef, knowledgeStore, knowledgeBackend, forest, guardianRef, orchRef, quarantineRef, quota, factoryRef)
}

// registerOnDemandAgentCreators registers factories for knowledge and pipeline agents.
// These are created lazily by the ActivationController when the Guide routes to them.
type onDemandAgentCreatorDeps struct {
	reg              *container.AgentCreatorRegistry
	ids              *container.AgentIdentityRegistry
	bus              guide.EventBus
	actPub           events.ActivityPublisher
	projectRoot      string
	googleGw         *gateway.ProviderGateway
	anthropicGw      *gateway.ProviderGateway
	openaiGw         *gateway.ProviderGateway
	authRegistry     *credentials.AuthRegistry
	actCtrlRef       *atomic.Pointer[activation.ActivationController]
	knowledgeStore   *knowledge.KnowledgeStore
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend
	forest           agentShared.MemoryForestService
	guardianRef      *atomic.Pointer[guardian.Guardian]
	orchRef          *atomic.Pointer[orchestrator.Orchestrator]
	quarantineRef    *atomic.Pointer[fetch.QuarantineBuffer]
	quota            *container.ResourceQuota
	factoryRef       *atomic.Pointer[identity.Factory]
}

// factory returns the session-scoped identity.Factory if it has been
// wired by wireIdentityAccounting; otherwise nil. On-demand agents
// created after phase4 should always see a non-nil value.
func (d onDemandAgentCreatorDeps) factory() *identity.Factory {
	if d.factoryRef == nil {
		return nil
	}
	return d.factoryRef.Load()
}

func (d onDemandAgentCreatorDeps) configuredModel(agentType, fallback string) string {
	fallback = strings.TrimSpace(fallback)
	store := agentpkg.NewAgentModelStore(filepath.Join(d.projectRoot, ".sylk", "config.yaml"))
	entry := store.EntryFor(agentType)
	if entry.Model == "" {
		return effectiveModelForCurrentAuth(d.authRegistry, fallback)
	}
	return resolvePersistedModelForCurrentAuth(entry.Model, entry.Provider, d.authRegistry)
}

func (d onDemandAgentCreatorDeps) sessionLookup(sessionID string) *versioning.SessionVFS {
	if orch := d.orchRef.Load(); orch != nil {
		return orch.GetSessionVFS(sessionID)
	}
	return nil
}

func (d onDemandAgentCreatorDeps) defaultSessionID() string {
	if orch := d.orchRef.Load(); orch != nil {
		return orch.SessionID()
	}
	return "default"
}

func (d onDemandAgentCreatorDeps) workspaceViews(defaultView versioning.WorkspaceView) *versioning.SessionWorkspaceViews {
	return d.workspaceViewsWithGlobalSource(defaultView, versioning.WorkspaceGlobalSourceCheckpoint)
}

func (d onDemandAgentCreatorDeps) workspaceViewsWithGlobalSource(
	defaultView versioning.WorkspaceView,
	globalSource versioning.WorkspaceGlobalSource,
) *versioning.SessionWorkspaceViews {
	return versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:      defaultView,
		DefaultSessionID: d.defaultSessionID(),
		GlobalSource:     globalSource,
		WorkingDir:       d.projectRoot,
		SessionLookup:    d.sessionLookup,
		DiskFallback:     versioning.NewDiskFileAccess(d.projectRoot, true),
	})
}

func (d onDemandAgentCreatorDeps) requestGuard(agentName string) func() func() {
	if d.actCtrlRef == nil {
		return nil
	}
	if ac := d.actCtrlRef.Load(); ac != nil {
		return func() func() {
			return ac.AcquireRequestGuard(agentName)
		}
	}
	return nil
}

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
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend,
	forest agentShared.MemoryForestService,
	guardianRef *atomic.Pointer[guardian.Guardian],
	orchRef *atomic.Pointer[orchestrator.Orchestrator],
	quarantineRef *atomic.Pointer[fetch.QuarantineBuffer],
	quota *container.ResourceQuota,
	factoryRef *atomic.Pointer[identity.Factory],
) {
	deps := onDemandAgentCreatorDeps{
		reg:              reg,
		ids:              ids,
		bus:              bus,
		actPub:           actPub,
		projectRoot:      projectRoot,
		googleGw:         googleGw,
		anthropicGw:      anthropicGw,
		openaiGw:         openaiGw,
		authRegistry:     authRegistry,
		actCtrlRef:       actCtrlRef,
		knowledgeStore:   knowledgeStore,
		knowledgeBackend: knowledgeBackend,
		forest:           forest,
		guardianRef:      guardianRef,
		orchRef:          orchRef,
		quarantineRef:    quarantineRef,
		quota:            quota,
		factoryRef:       factoryRef,
	}
	registerOnDemandKnowledgeAgentCreators(deps)
	registerOnDemandQualityAgentCreators(deps)
	registerOnDemandImplementationAgentCreators(deps)
}

func registerOnDemandKnowledgeAgentCreators(deps onDemandAgentCreatorDeps) {
	registerLibrarianAgentCreator(deps)
	registerArchivalistAgentCreator(deps)
	registerAcademicAgentCreator(deps)
}

func registerLibrarianAgentCreator(deps onDemandAgentCreatorDeps) {
	librarianID, _ := deps.ids.Get("librarian")
	deps.reg.Register("librarian", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("librarian", librarian.DefaultLibrarianModel)
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("librarian provider: %w", err)
		}

		libCfg := librarian.Config{
			ID:               librarianID,
			EnableLLM:        true,
			Model:            model,
			AnthropicAPIKey:  providers.ResolveAnthropicAPIKey(""),
			ActivityPub:      deps.actPub,
			WorkingDirectory: deps.projectRoot,
			SearchSystem:     librarian.NewCommittedKnowledgeSearchSystem(deps.knowledgeBackend),
			KnowledgeBackend: deps.knowledgeBackend,
			ContextQuota:     deps.quota,
			Forest:           deps.forest,
			Factory:          deps.factory(),
		}
		if guard := deps.requestGuard("librarian"); guard != nil {
			libCfg.RequestGuard = guard
		}
		l, err := librarian.New(libCfg, wrapped)
		if err != nil {
			return nil, err
		}
		l.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		l.SetKnowledgeStore(deps.knowledgeStore)

		if startErr := l.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return l, nil
	})
}

func registerArchivalistAgentCreator(deps onDemandAgentCreatorDeps) {
	archivalistID, _ := deps.ids.Get("archivalist")
	deps.reg.Register("archivalist", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("archivalist", archivalist.ModelSonnet45)
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("archivalist provider: %w", err)
		}

		archCfg := buildOnDemandArchivalistConfig(deps, archivalistID, model)
		a, err := archivalist.New(ctx, archCfg)
		if err != nil {
			return nil, err
		}
		a.SetProvider(wrapped)
		a.SetKnowledgeStore(deps.knowledgeStore)
		a.SetKnowledgeBackend(deps.knowledgeBackend)
		if startErr := a.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return a, nil
	})
}

func buildOnDemandArchivalistConfig(deps onDemandAgentCreatorDeps, agentID, model string) archivalist.Config {
	archCfg := archivalist.Config{
		ID:                   agentID,
		Model:                model,
		ActivityPub:          deps.actPub,
		EnableLLM:            true,
		EnableArchive:        true,
		EnableRAG:            true,
		EnableKnowledgeGraph: true,
		EnableHybridQuery:    true,
		EnableACTR:           true,
		ContextQuota:         deps.quota,
		Forest:               deps.forest,
		Factory:              deps.factory(),
	}
	if guard := deps.requestGuard("archivalist"); guard != nil {
		archCfg.RequestGuard = guard
	}
	return archCfg
}

func registerAcademicAgentCreator(deps onDemandAgentCreatorDeps) {
	academicID, _ := deps.ids.Get("academic")
	deps.reg.Register("academic", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("academic", academic.DefaultModel)
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("academic provider: %w", err)
		}
		acaCfg := academic.Config{
			ID:           academicID,
			Model:        model,
			ActivityPub:  deps.actPub,
			ContextQuota: deps.quota,
			Forest:       deps.forest,
			Factory:      deps.factory(),
		}
		if guard := deps.requestGuard("academic"); guard != nil {
			acaCfg.RequestGuard = guard
		}
		a, err := academic.New(acaCfg, wrapped)
		if err != nil {
			return nil, err
		}
		a.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		a.SetKnowledgeStore(deps.knowledgeStore)
		a.SetKnowledgeBackend(deps.knowledgeBackend)
		a.SetFetchPipeline(buildAcademicFetchPipeline(deps.projectRoot, deps.guardianRef, deps.quarantineRef, deps.knowledgeBackend))
		if startErr := a.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return a, nil
	})
}

func registerOnDemandQualityAgentCreators(deps onDemandAgentCreatorDeps) {
	registerGlobalInspectorAgentCreator(deps)
	registerPipelineInspectorAgentCreator(deps)
	registerGlobalTesterAgentCreator(deps)
	registerPipelineTesterAgentCreator(deps)
}

func registerGlobalInspectorAgentCreator(deps onDemandAgentCreatorDeps) {
	inspectorID, _ := deps.ids.Get("inspector")
	deps.reg.Register("inspector", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("inspector", "claude-opus-4-6")
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation)
		if err != nil {
			return nil, fmt.Errorf("global inspector provider: %w", err)
		}
		gi, err := inspectorGlobal.New(inspectorShared.GlobalInspectorConfig{
			AgentID:     inspectorID,
			SessionID:   deps.defaultSessionID(),
			Model:       model,
			ActivityPub: deps.actPub,
			Forest:      deps.forest,
			Factory:     deps.factory(),
		}, wrapped)
		if err != nil {
			return nil, err
		}
		gi.SetFileAccess(versioning.NewSessionReviewRoutingFileAccess(
			false,
			deps.sessionLookup,
			versioning.NewDiskFileAccess(deps.projectRoot, false),
		))
		gi.SetWorkspaceViews(deps.workspaceViewsWithGlobalSource(
			versioning.WorkspaceViewGlobal,
			versioning.WorkspaceGlobalSourceReview,
		))
		gi.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation))
		if startErr := gi.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return gi, nil
	})
}

func registerPipelineInspectorAgentCreator(deps onDemandAgentCreatorDeps) {
	deps.reg.Register("inspector-pipeline", func(ctx context.Context) (container.ContainerAgent, error) {
		agentID := pipelineWorkerAgentID(ctx, "inspector-pipeline")
		model := deps.configuredModel("inspector-pipeline", "claude-opus-4-6")
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation)
		if err != nil {
			return nil, fmt.Errorf("pipeline inspector provider: %w", err)
		}
		pi, err := inspectorPipeline.New(inspectorShared.PipelineInspectorConfig{AgentID: agentID, Forest: deps.forest, Factory: deps.factory()}, wrapped)
		if err != nil {
			return nil, err
		}
		pi.SetActivityPublisher(deps.actPub)
		// Inspector-owned pipeline VFS authority — handoff_to_ot and
		// discard_pipeline call this committer instead of the orchestrator
		// reacting to "succeeded" / "failed" pipeline broadcasts. The
		// session is resolved per-call via ctx so a single inspector pod
		// can correctly serve work from multiple session contexts.
		pi.SetPipelineCommitter(agentShared.NewSessionVFSPipelineCommitter(func(sessionID string) agentShared.SessionVFSPipelineCommitterBackend {
			return deps.sessionLookup(sessionID)
		}))
		if startErr := pi.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return pi, nil
	})
}

func registerGlobalTesterAgentCreator(deps onDemandAgentCreatorDeps) {
	testerID, _ := deps.ids.Get("tester")
	deps.reg.Register("tester", func(ctx context.Context) (container.ContainerAgent, error) {
		model := deps.configuredModel("tester", "gpt-5.4-pro")
		var wrapped providers.Provider
		if provider, provErr := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation); provErr != nil {
			slog.Warn("tester: provider unavailable, LLM features disabled", "error", provErr)
		} else {
			wrapped = provider
		}
		gt, err := globaltester.New(shared.GlobalTesterConfig{
			AgentID:     testerID,
			SessionID:   deps.defaultSessionID(),
			Model:       model,
			ActivityPub: deps.actPub,
			Forest:      deps.forest,
			Factory:     deps.factory(),
		}, wrapped)
		if err != nil {
			return nil, err
		}
		gt.SetFileAccess(versioning.NewSessionReviewRoutingFileAccess(
			false,
			deps.sessionLookup,
			versioning.NewDiskFileAccess(deps.projectRoot, false),
		))
		gt.SetWorkspaceViews(deps.workspaceViewsWithGlobalSource(
			versioning.WorkspaceViewGlobal,
			versioning.WorkspaceGlobalSourceReview,
		))
		gt.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation))
		if startErr := gt.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return gt, nil
	})
}

func registerPipelineTesterAgentCreator(deps onDemandAgentCreatorDeps) {
	deps.reg.Register("tester-pipeline", func(ctx context.Context) (container.ContainerAgent, error) {
		agentID := pipelineWorkerAgentID(ctx, "tester-pipeline")
		model := deps.configuredModel("tester-pipeline", "gpt-5.4-pro")
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityValidation)
		if err != nil {
			return nil, fmt.Errorf("pipeline tester provider: %w", err)
		}
		pt, err := pipelinetester.New(shared.PipelineTesterConfig{AgentID: agentID, Model: model, Forest: deps.forest, Factory: deps.factory()}, wrapped)
		if err != nil {
			return nil, err
		}
		pt.SetActivityPublisher(deps.actPub)
		if startErr := pt.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return pt, nil
	})
}

func registerOnDemandImplementationAgentCreators(deps onDemandAgentCreatorDeps) {
	registerEngineerAgentCreator(deps)
	registerDesignerAgentCreator(deps)
}

func registerEngineerAgentCreator(deps onDemandAgentCreatorDeps) {
	engineerID, _ := deps.ids.Get("engineer")
	deps.reg.Register("engineer", func(ctx context.Context) (container.ContainerAgent, error) {
		agentID := taskScopedCreationAgentID(ctx, "engineer", engineerID)
		model := deps.configuredModel("engineer", "gpt-5.4-pro")
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("engineer provider: %w", err)
		}
		engCfg := engineer.Config{ID: agentID, ActivityPub: deps.actPub, Forest: deps.forest, Factory: deps.factory()}
		engCfg.EngineerConfig.Model = model
		if guard := deps.requestGuard("engineer"); guard != nil {
			engCfg.RequestGuard = guard
		}
		e, err := engineer.New(engCfg, wrapped)
		if err != nil {
			return nil, err
		}
		e.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		if startErr := e.Start(deps.bus); startErr != nil {
			return nil, startErr
		}
		return e, nil
	})
}

func registerDesignerAgentCreator(deps onDemandAgentCreatorDeps) {
	designerID, _ := deps.ids.Get("designer")
	deps.reg.Register("designer", func(ctx context.Context) (container.ContainerAgent, error) {
		agentID := taskScopedCreationAgentID(ctx, "designer", designerID)
		model := deps.configuredModel("designer", string(providers.Gemini31Pro))
		wrapped, err := createSwapProvider(ctx, model, deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution)
		if err != nil {
			return nil, fmt.Errorf("designer provider: %w", err)
		}
		desCfg := designer.Config{ID: agentID, ActivityPub: deps.actPub, Forest: deps.forest, Factory: deps.factory()}
		if guard := deps.requestGuard("designer"); guard != nil {
			desCfg.RequestGuard = guard
		}
		d, err := designer.New(desCfg, wrapped)
		if err != nil {
			return nil, err
		}
		d.SetProviderRefresher(buildProviderRefresher(deps.authRegistry, deps.googleGw, deps.anthropicGw, deps.openaiGw, gateway.PriorityExecution))
		if startErr := d.Start(deps.bus); startErr != nil {
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
		totalGoroutines     int64
		totalContextRequest int64
		totalContextWindow  int64
		restartableCount    int64 // agents that may restart (concurrent old+new overlap)
		maxGoroutineLimit   int64 // largest single-agent goroutine budget (incl overhead)
		maxContextRequest   int64 // largest single-agent request window
		maxContextWindow    int64 // largest single-agent context window
	)

	for _, d := range all {
		spec, err := specReg.SpecForAgent(d.AgentType)
		if err != nil {
			continue
		}

		overhead := runtimeOverhead(spec)
		goroutines := spec.Resources.GoroutineLimit + overhead
		ctxRequest := int64(spec.Resources.ContextWindowRequest)
		ctxWindow := int64(spec.Resources.ContextWindowLimit)

		totalGoroutines += goroutines
		totalContextRequest += ctxRequest
		totalContextWindow += ctxWindow

		if goroutines > maxGoroutineLimit {
			maxGoroutineLimit = goroutines
		}
		if ctxRequest > maxContextRequest {
			maxContextRequest = ctxRequest
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
		GoroutineLimit:       totalGoroutines + restartGoroutineHeadroom,
		ContextWindowRequest: totalContextRequest + maxContextRequest,
		ContextWindowLimit:   totalContextWindow + contextHeadroom,
		ContainerLimit:       agentCount + restartableCount,
	}
}

// =============================================================================
// Agent Bootstrap Helpers (unchanged from before)
// =============================================================================

// bootstrapLiveGuide creates and starts a Guide with LLM classification.
// When hydrated is non-nil, it reuses pre-resolved auth (skipping duplicate
// OAuth + Code Assist setup). If provider auth is unavailable, it falls back
// to a local rule-based classifier so the UI can launch without authorization.
func bootstrapLiveGuide(ctx context.Context, bus guide.EventBus, actPub events.ActivityPublisher, hydrated *providers.HydratedGoogleAuth, googleGw *gateway.ProviderGateway, authRegistry *credentials.AuthRegistry, projectRoot string, forest agentShared.MemoryForestService, sessionVFSLookup func(string) *versioning.SessionVFS, factory *identity.Factory) (*guide.Guide, error) {
	if factory == nil {
		return nil, fmt.Errorf("guide bootstrap: nil identity factory")
	}
	googleCfg := defaultGuideGoogleConfig(authRegistry)
	cfg := guide.Config{
		Bus:          bus,
		ActivityPub:  actPub,
		AgentID:      "guide",
		SessionID:    "default",
		GoogleConfig: &googleCfg,
		Forest:       forest,
		Factory:      factory,
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

func bootstrapArchitect(ctx context.Context, canonicalID string, sessionID string, bus guide.EventBus, actPub events.ActivityPublisher, projectRoot string, anthropicGw *gateway.ProviderGateway, authRegistry *credentials.AuthRegistry, actCtrlRef *atomic.Pointer[activation.ActivationController], planStore *architect.PlanStore, forest agentShared.MemoryForestService, sessionVFSLookup func(string) *versioning.SessionVFS, factoryRef *atomic.Pointer[identity.Factory]) (*architect.Architect, error) {
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
	var factory *identity.Factory
	if factoryRef != nil {
		factory = factoryRef.Load()
	}
	cfg := architect.Config{
		ID:                canonicalID,
		SessionID:         sessionID,
		EnableLLM:         true,
		Model:             architect.DefaultArchitectModel,
		AnthropicAuthMode: authMode,
		AnthropicAPIKey:   apiKey,
		ActivityPub:       actPub,
		PlanStore:         planStore,
		Forest:            forest,
		Factory:           factory,
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
	forest agentShared.MemoryForestService,
	sessionVFSLookup func(string) *versioning.SessionVFS,
	factory *identity.Factory,
) (*guardian.Guardian, error) {
	if factory == nil {
		return nil, fmt.Errorf("guardian bootstrap: nil identity factory")
	}
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
		Forest:    forest,
		Factory:   factory,
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
	g.SetCommandApprovalStore(commandapproval.NewRuleStore(filepath.Join(projectRoot, ".sylk", "local", "command_approvals.yaml")))
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
	knowledgeBackend *knowledgeruntime.CommittedKnowledgeBackend,
) *fetch.Pipeline {
	policy := fetch.NewFetchPolicy(fetch.DefaultPolicyConfig())
	consent := newAcademicFetchConsentGate()

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
		Ingest: func(ctx context.Context, entry *fetch.QuarantineEntry, provenance *fetch.Provenance, extracted *fetch.ExtractResult) error {
			if knowledgeBackend == nil {
				return fmt.Errorf("committed knowledge backend is unavailable")
			}
			return knowledgeBackend.IngestFetchedDocument(ctx, entry, provenance, extracted)
		},
		Logger: slog.Default(),
	})
}

func registerArchitectWithGuide(g *guide.Guide, a *architect.Architect) error {
	if g == nil || a == nil {
		return nil
	}
	seedRouterKnownAgents(g, a)
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
	seedRouterKnownAgents(g, router)
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

type guideKnownAgentSeeder interface {
	SeedKnownAgents([]*guide.AgentAnnouncement)
}

func seedRouterKnownAgents(g *guide.Guide, router guide.AgentRouter) {
	if g == nil || router == nil {
		return
	}
	seeder, ok := router.(guideKnownAgentSeeder)
	if !ok {
		return
	}
	seeder.SeedKnownAgents(g.RegisteredAgentInfos())
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
		return nil
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
	taskAlias := orchestrator.TaskScopedRoutingName("", podID, agentType)
	cloned.PodID = podID
	cloned.Name = strings.TrimSpace(info.Name)
	if taskAlias != "" {
		cloned.Name = taskAlias
	}
	cloned.Aliases = nil
	cloned.ActionShortcuts = nil
	cloned.Triggers = guide.AgentTriggers{}
	if info.Registration != nil {
		reg := *info.Registration
		reg.Name = strings.TrimSpace(info.Registration.Name)
		if taskAlias != "" {
			reg.Name = taskAlias
		}
		reg.Aliases = nil
		cloned.Registration = &reg
	}
	return &cloned
}

func pipelineWorkerAgentID(ctx context.Context, agentType string) string {
	if spec, ok := container.CreationSpecFromContext(ctx); ok {
		if spec.Labels != nil {
			if specID, labelOK := spec.Labels["pipeline_worker_id"]; labelOK {
				if specID = strings.TrimSpace(specID); specID != "" {
					return specID
				}
			}
		}
	}
	return ""
}

func taskScopedCreationAgentID(ctx context.Context, agentType, singletonID string) string {
	if workerID := pipelineWorkerAgentID(ctx, agentType); workerID != "" {
		return workerID
	}
	podID, ok := container.CreationPodIDFromContext(ctx)
	if ok && isTaskScopedPipelineWorker(string(podID), agentType) {
		return ""
	}
	return singletonID
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
		if err := bus.Publish(guide.TopicAuthCredentials, msg); err != nil {
			slog.Warn("tui_auth_changed_publish_failed",
				"provider_type", event.ProviderType,
				"auth_method", event.AuthMethod,
				"available", event.Available,
				"error", err.Error(),
			)
		}
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
	activator modelSwapActivator,
	authRegistry *credentials.AuthRegistry,
	googleGw, anthropicGw, openaiGw *gateway.ProviderGateway,
) func(ctx context.Context, agentType, modelID string) error {
	return func(ctx context.Context, agentType, modelID string) error {
		swappableContainer, err := resolveModelSwapContainer(ctx, containerReg, activator, agentType)
		if err != nil {
			if errors.Is(err, errNoSwappableContainer) && !architectModelSwapRequiresLiveActivation(agentType) {
				return nil
			}
			return err
		}
		modelID = effectiveModelForCurrentAuth(authRegistry, modelID)
		priority, ok := agentSwapPriority[agentType]
		if !ok {
			return fmt.Errorf("no model swap priority configured for agent type %q", agentType)
		}
		provider, err := createSwapProvider(ctx, modelID, authRegistry, googleGw, anthropicGw, openaiGw, priority)
		if err != nil {
			return fmt.Errorf("model swap provider for %s: %w", agentType, err)
		}
		swappable, ok := swappableContainer.Agent().(container.ModelSwappable)
		if !ok {
			return fmt.Errorf("container %q for type %q does not implement ModelSwappable", swappableContainer.ID(), agentType)
		}
		return swappable.SwapModel(ctx, modelID, provider)
	}
}

func resolveModelSwapContainer(
	ctx context.Context,
	containerReg *container.ContainerRegistry,
	activator modelSwapActivator,
	agentType string,
) (*container.Container, error) {
	if architectModelSwapRequiresLiveActivation(agentType) && activator != nil {
		ctr, err := activator.EnsureActive(ctx, agentType)
		if err != nil {
			return nil, fmt.Errorf("activate %s for model swap: %w", agentType, err)
		}
		if ctr != nil {
			if _, ok := ctr.Agent().(container.ModelSwappable); ok {
				return ctr, nil
			}
		}
	}
	if ctr := firstRunningSwappableContainerForType(containerReg, agentType); ctr != nil {
		return ctr, nil
	}
	if ctr := firstSwappableContainerForType(containerReg, agentType); ctr != nil {
		return ctr, nil
	}
	if architectModelSwapRequiresLiveActivation(agentType) && activator != nil {
		if _, err := activator.EnsureActive(ctx, agentType); err != nil {
			return nil, fmt.Errorf("activate %s for model swap: %w", agentType, err)
		}
		if ctr := firstSwappableContainerForType(containerReg, agentType); ctr != nil {
			return ctr, nil
		}
	}
	return nil, fmt.Errorf("%w for type %q", errNoSwappableContainer, agentType)
}

func architectModelSwapRequiresLiveActivation(agentType string) bool {
	return agentType == "architect"
}

func firstRunningSwappableContainerForType(containerReg *container.ContainerRegistry, agentType string) *container.Container {
	for _, c := range containerReg.ListByType(agentType) {
		if !c.IsRunning() {
			continue
		}
		if _, ok := c.Agent().(container.ModelSwappable); ok {
			return c
		}
	}
	return nil
}

func firstSwappableContainerForType(containerReg *container.ContainerRegistry, agentType string) *container.Container {
	for _, c := range containerReg.ListByType(agentType) {
		if _, ok := c.Agent().(container.ModelSwappable); ok {
			return c
		}
	}
	return nil
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

func bootstrapOrchestrator(ctx context.Context, agentID string, bus guide.EventBus, actPub events.ActivityPublisher, projectRoot string, hydrated *providers.HydratedGoogleAuth, authRegistry *credentials.AuthRegistry, googleGw *gateway.ProviderGateway, forest agentShared.MemoryForestService, factory *identity.Factory) (*orchestrator.Orchestrator, error) {
	if factory == nil {
		return nil, fmt.Errorf("orchestrator bootstrap: nil identity factory")
	}
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
	cfg.SessionID = "default"
	cfg.EnableLLM = true
	cfg.Forest = forest
	cfg.Factory = factory
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
	orch.SetWorkspaceViews(versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:      versioning.WorkspaceViewGlobal,
		DefaultSessionID: cfg.SessionID,
		WorkingDir:       projectRoot,
		SessionLookup:    orch.GetSessionVFS,
		DiskFallback:     versioning.NewDiskFileAccess(projectRoot, true),
	}))
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
	seedRouterKnownAgents(g, orch)
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
	if rt != nil && reg != nil {
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
	}
	if ns != nil {
		ns.Close()
	}
	if rt != nil {
		rt.Close()
	}
	if bus != nil {
		if err := bus.Close(); err != nil {
			slog.Warn("cleanup: bus close failed", "error", err)
		}
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
	bus guide.EventBus,
	serviceReg *network.ServiceRegistry,
	containerReg *container.ContainerRegistry,
	creatorReg *container.AgentCreatorRegistry,
	activationCtrl *activation.ActivationController,
) *handoff.HandoffSupervisor {
	walDir, err := handoffWALDir()
	if err != nil {
		return nil
	}

	supervisorCfg := handoff.DefaultSupervisorConfig()
	supervisorCfg.WALDir = walDir
	supervisor := handoff.NewHandoffSupervisor(supervisorCfg)
	if bus != nil {
		supervisor.SetBriefSource(agentShared.NewArchivalistBriefSource(bus))
	}

	// Register creators for every known agent type so any handoff-capable
	// agent can spawn a fresh replacement instance.
	registerHandoffCreators(supervisor, creatorReg)

	// Wire agent replacement: atomic swap of canonical identity.
	// New agent was invisible to Guide during the traffic shift.
	// At swap time: assign canonical ID → unregister old → register new.
	supervisor.SetAgentReplacedCallback(func(oldID, _ string, newAgent handoff.HandoffableAgent) error {
		if setter, ok := newAgent.(interface{ SetCanonicalID(string) }); ok {
			setter.SetCanonicalID(oldID)
		}
		if replacement, ok := newAgent.(handoffScribeAgent); ok {
			if pod := replacement.AgentPod(); pod != nil {
				previous := pod.ReplaceScribe(replacement.ParentAgentType(), replacement)
				if previous != nil && previous != replacement {
					if retiring, ok := previous.(handoffRetiringScribe); ok {
						if err := retiring.RetireForHandoff(); err != nil {
							slog.Warn("retire replaced scribe", "agent_id", oldID, "error", err)
						}
					} else if err := previous.Stop(); err != nil {
						slog.Warn("stop replaced scribe", "agent_id", oldID, "error", err)
					}
				}
			}
		}
		inheritHandoffRuntimeBindings(containerReg, oldID, newAgent)
		g.UnregisterAgent(oldID)
		if router, ok := newAgent.(guide.AgentRouter); ok {
			seedRouterKnownAgents(g, router)
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

	// Register all live handoff-capable agents — not just the bootstrap trio.
	registerAllHandoffContainers(supervisor, containerReg)
	registerHandoffAgent(supervisor, g)

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

type handoffPodGetter interface {
	AgentPod() *agentShared.AgentPod
}

type handoffScribeAgent interface {
	agentShared.Scribe
	handoff.HandoffInjectable
	ParentAgentType() string
	AgentPod() *agentShared.AgentPod
}

type handoffRetiringScribe interface {
	RetireForHandoff() error
}

// registerHandoffAgent registers a single agent with the supervisor and
// sets the bridge on the agent if it supports it.
func registerHandoffAgent(supervisor *handoff.HandoffSupervisor, agent handoff.HandoffableAgent) {
	if supervisor == nil || agent == nil {
		return
	}
	bridge, err := supervisor.RegisterAgent(agent)
	if err != nil {
		return
	}
	if setter, ok := agent.(handoffBridgeSetter); ok {
		setter.SetHandoffBridge(bridge)
	}
}

func inheritHandoffRuntimeBindings(containerReg *container.ContainerRegistry, oldID string, newAgent handoff.HandoffableAgent) {
	if containerReg == nil || strings.TrimSpace(oldID) == "" || newAgent == nil {
		return
	}

	var oldAgent handoff.HandoffableAgent
	for _, ctr := range containerReg.All() {
		if ctr == nil || ctr.Agent() == nil {
			continue
		}
		handoffable, ok := ctr.Agent().(handoff.HandoffableAgent)
		if !ok {
			continue
		}
		if handoffable.AgentID() == oldID {
			oldAgent = handoffable
			break
		}
	}
	if oldAgent == nil {
		return
	}

	podSource, ok := oldAgent.(handoffPodGetter)
	if !ok {
		return
	}
	pod := podSource.AgentPod()
	if pod == nil {
		return
	}

	if setter, ok := newAgent.(agentPodSetter); ok {
		setter.SetAgentPod(pod)
	}
	if consumer, ok := newAgent.(versioning.FileAccessConsumer); ok {
		if fa := pod.FileAccessFor(newAgent.AgentType()); fa != nil {
			consumer.SetFileAccess(fa)
		}
	}
	if viewsConsumer, ok := newAgent.(versioning.WorkspaceViewsConsumer); ok {
		if views := pod.WorkspaceViewsFor(newAgent.AgentType()); views != nil {
			viewsConsumer.SetWorkspaceViews(views)
		}
	}
}

func registerHandoffCreators(supervisor *handoff.HandoffSupervisor, creatorReg *container.AgentCreatorRegistry) {
	if supervisor == nil || creatorReg == nil {
		return
	}
	for _, agentType := range creatorReg.Types() {
		agentType := agentType
		supervisor.Factory().RegisterCreator(agentType, func(ctx context.Context) (handoff.HandoffableAgent, error) {
			agent, err := creatorReg.Create(applyHandoffCreationContext(ctx, agentType), agentType)
			if err != nil {
				return nil, err
			}
			handoffable, ok := agent.(handoff.HandoffableAgent)
			if !ok {
				return nil, fmt.Errorf("agent %q does not implement handoff support", agentType)
			}
			return handoffable, nil
		})
	}
}

func applyHandoffCreationContext(ctx context.Context, agentType string) context.Context {
	metadata, ok := handoff.FactoryCreationMetadataFromContext(ctx)
	if !ok {
		return ctx
	}

	spec := container.ContainerSpec{
		AgentType: agentType,
		Labels:    make(map[string]string, 4),
	}
	if strings.TrimSpace(metadata.AgentID) != "" {
		spec.Labels["pipeline_worker_id"] = metadata.AgentID
	}
	if strings.TrimSpace(metadata.TaskID) != "" {
		spec.Labels["task_id"] = metadata.TaskID
		if strings.TrimSpace(metadata.TaskSlug) != "" {
			spec.Labels["task_slug"] = metadata.TaskSlug
		}
		return container.WithCreationContext(ctx, spec, container.PodID(metadata.TaskID))
	}
	if strings.TrimSpace(metadata.AgentType) != "" {
		spec.AgentType = metadata.AgentType
	}
	return container.WithCreationContext(ctx, spec, "")
}

func registerAllHandoffContainers(supervisor *handoff.HandoffSupervisor, reg *container.ContainerRegistry) {
	if supervisor == nil || reg == nil {
		return
	}
	for _, c := range reg.All() {
		registerHandoffContainer(supervisor, c)
	}
}

func registerHandoffContainer(supervisor *handoff.HandoffSupervisor, c *container.Container) {
	if supervisor == nil || c == nil || c.Agent() == nil {
		return
	}
	handoffable, ok := c.Agent().(handoff.HandoffableAgent)
	if !ok {
		return
	}
	registerHandoffAgent(supervisor, handoffable)
}

func unregisterHandoffContainer(supervisor *handoff.HandoffSupervisor, c *container.Container) {
	if supervisor == nil || c == nil || c.Agent() == nil {
		return
	}
	handoffable, ok := c.Agent().(handoff.HandoffableAgent)
	if !ok {
		return
	}
	_ = supervisor.UnregisterAgent(handoffable.AgentID())
	if setter, ok := handoffable.(handoffBridgeSetter); ok {
		setter.SetHandoffBridge(nil)
	}
}

type handoffManagedScribe struct {
	inner           *scribe.Scribe
	supervisorRef   *atomic.Pointer[handoff.HandoffSupervisor]
	logger          *slog.Logger
	parentAgentType string
	pod             *agentShared.AgentPod
	registry        *handoffManagedScribeRegistry
	newManaged      func(parentAgentType string, logger *slog.Logger, autoRegister bool) (*handoffManagedScribe, error)
	autoRegister    bool

	mu             sync.Mutex
	registeredSup  *handoff.HandoffSupervisor
	bridgeAssigned bool
}

func (s *handoffManagedScribe) Start() error {
	if err := s.inner.Start(); err != nil {
		return err
	}
	if s.registry != nil {
		s.registry.Track(s)
	}
	s.ensureHandoffRegistration()
	return nil
}

func (s *handoffManagedScribe) Stop() error {
	agentID := s.AgentID()
	if s.registry != nil {
		s.registry.DeleteIf(agentID, s)
	}

	s.mu.Lock()
	sup := s.registeredSup
	shouldUnregister := s.registeredSup != nil || s.bridgeAssigned
	s.registeredSup = nil
	s.bridgeAssigned = false
	s.mu.Unlock()

	if shouldUnregister {
		if sup == nil && s.supervisorRef != nil {
			sup = s.supervisorRef.Load()
		}
		if sup != nil {
			_ = sup.UnregisterAgent(agentID)
		}
		s.SetHandoffBridge(nil)
	}

	return s.inner.Stop()
}

func (s *handoffManagedScribe) RetireForHandoff() error {
	agentID := s.AgentID()
	if s.registry != nil {
		s.registry.DeleteIf(agentID, s)
	}
	s.mu.Lock()
	s.registeredSup = nil
	s.bridgeAssigned = false
	s.mu.Unlock()
	s.SetHandoffBridge(nil)
	return s.inner.Stop()
}

func (s *handoffManagedScribe) Feed(feed agentShared.ScribeFeed) {
	s.ensureHandoffRegistration()
	s.inner.Feed(feed)
}

func (s *handoffManagedScribe) AgentID() string { return s.inner.AgentID() }

func (s *handoffManagedScribe) AgentType() string { return s.inner.AgentType() }

func (s *handoffManagedScribe) Descriptor() handoff.AgentDescriptor { return s.inner.Descriptor() }

func (s *handoffManagedScribe) ExtractArchivableState() *handoff.ArchivableState {
	return s.inner.ExtractArchivableState()
}

func (s *handoffManagedScribe) Terminate(ctx context.Context) error { return s.Stop() }

func (s *handoffManagedScribe) InjectPreparedContext(pc *handoff.PreparedContext) error {
	return s.inner.InjectPreparedContext(pc)
}

func (s *handoffManagedScribe) SetCanonicalID(id string) {
	oldID := s.AgentID()
	s.inner.SetCanonicalID(id)
	if s.registry != nil {
		s.registry.Rename(oldID, s.AgentID(), s)
	}
}

func (s *handoffManagedScribe) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	s.mu.Lock()
	s.bridgeAssigned = bridge != nil
	s.mu.Unlock()
	s.inner.SetHandoffBridge(bridge)
}

func (s *handoffManagedScribe) SetAgentPod(pod *agentShared.AgentPod) {
	s.pod = pod
}

func (s *handoffManagedScribe) AgentPod() *agentShared.AgentPod {
	return s.pod
}

func (s *handoffManagedScribe) ParentAgentType() string {
	return s.parentAgentType
}

func (s *handoffManagedScribe) ensureHandoffRegistration() {
	if s == nil || !s.autoRegister || s.supervisorRef == nil {
		return
	}
	sup := s.supervisorRef.Load()
	if sup == nil {
		return
	}

	if s.registry != nil && s.newManaged != nil {
		agentType := s.AgentType()
		sup.Factory().RegisterCreator(agentType, func(ctx context.Context) (handoff.HandoffableAgent, error) {
			metadata, ok := handoff.FactoryCreationMetadataFromContext(ctx)
			if !ok || strings.TrimSpace(metadata.AgentID) == "" {
				return nil, fmt.Errorf("scribe handoff for %q missing source agent id", agentType)
			}
			source := s.registry.Get(metadata.AgentID)
			if source == nil {
				return nil, fmt.Errorf("no live scribe source found for agent id %q", metadata.AgentID)
			}
			replacement, err := source.newManaged(source.parentAgentType, source.logger, false)
			if err != nil {
				return nil, err
			}
			replacement.SetAgentPod(source.AgentPod())
			if err := replacement.Start(); err != nil {
				return nil, err
			}
			return replacement, nil
		})
	}

	s.mu.Lock()
	if s.registeredSup != nil || s.bridgeAssigned {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	registerHandoffAgent(sup, s)

	s.mu.Lock()
	if s.bridgeAssigned {
		s.registeredSup = sup
	}
	s.mu.Unlock()
}

type handoffManagedScribeRegistry struct {
	mu   sync.Mutex
	byID map[string]*handoffManagedScribe
}

func newHandoffManagedScribeRegistry() *handoffManagedScribeRegistry {
	return &handoffManagedScribeRegistry{
		byID: make(map[string]*handoffManagedScribe),
	}
}

func (r *handoffManagedScribeRegistry) Track(s *handoffManagedScribe) {
	if r == nil || s == nil {
		return
	}
	id := strings.TrimSpace(s.AgentID())
	if id == "" {
		return
	}
	r.mu.Lock()
	r.byID[id] = s
	r.mu.Unlock()
}

func (r *handoffManagedScribeRegistry) DeleteIf(id string, target *handoffManagedScribe) {
	if r == nil || target == nil {
		return
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	r.mu.Lock()
	if existing := r.byID[id]; existing == target {
		delete(r.byID, id)
	}
	r.mu.Unlock()
}

func (r *handoffManagedScribeRegistry) Rename(oldID, newID string, target *handoffManagedScribe) {
	if r == nil || target == nil {
		return
	}
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	r.mu.Lock()
	if oldID != "" {
		if existing := r.byID[oldID]; existing == target {
			delete(r.byID, oldID)
		}
	}
	if newID != "" {
		r.byID[newID] = target
	}
	r.mu.Unlock()
}

func (r *handoffManagedScribeRegistry) Get(id string) *handoffManagedScribe {
	if r == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[id]
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
	forest agentShared.MemoryForestService,
	supervisorRef *atomic.Pointer[handoff.HandoffSupervisor],
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
	registry := newHandoffManagedScribeRegistry()
	var newManaged func(parentAgentType string, logger *slog.Logger, autoRegister bool) (*handoffManagedScribe, error)
	newManaged = func(parentAgentType string, logger *slog.Logger, autoRegister bool) (*handoffManagedScribe, error) {
		managed := &handoffManagedScribe{
			inner: scribe.New(scribe.Config{
				ParentAgentType: parentAgentType,
				Provider:        wrapped,
				Model:           "gemini-3-flash-preview",
				Bus:             bus,
				Scope:           scope,
				Logger:          logger,
				Forest:          forest,
			}),
			supervisorRef:   supervisorRef,
			logger:          logger,
			parentAgentType: parentAgentType,
			registry:        registry,
			autoRegister:    autoRegister,
		}
		managed.newManaged = newManaged
		return managed, nil
	}
	return func(parentAgentType string, logger *slog.Logger) (agentShared.Scribe, error) {
		return newManaged(parentAgentType, logger, true)
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
