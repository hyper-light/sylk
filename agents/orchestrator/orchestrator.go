package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// OrchestratorProvider is the minimal LLM interface required by the Orchestrator.
// Satisfied by any providers.ProviderAdapter (e.g. Google, Anthropic, gateway-wrapped).
type OrchestratorProvider interface {
	Complete(ctx context.Context, req *providers.CompletionRequest) (*providers.CompletionResponse, error)
}

// Orchestrator is a read-only workflow observer and coordinator.
// Identity: observational nervous system (provider-agnostic)
// Role: Monitor workflows, track task health, submit events to Archivalist
type Orchestrator struct {
	config Config
	state  *State

	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool

	skills         *skills.Registry
	skillLoader    *skills.Loader
	hooks          *skills.HookRegistry
	tools          *toolruntime.Runtime
	toolDefsDirty  bool
	workspaceViews versioning.WorkspaceViewAccess

	healthMonitor *HealthMonitor
	healthCache   *HealthCache

	knownAgents map[string]*guide.AgentAnnouncement

	// LLM integration
	provider  OrchestratorProvider // LLM provider (nil = fallback mode)
	eventCh   chan *busEvent       // buffered channel for LLM event loop
	bootGate  *bootstrapGate       // signal-based readiness gate for LLM loop
	llmCtx    context.Context
	llmCancel context.CancelFunc
	llmWg     sync.WaitGroup // tracks the LLM loop goroutine

	// Activity publisher for UI agent panel visibility
	activityPub events.ActivityPublisher

	// Data plane: WAL, SQLite, BufferRegistry, DAG Bridge
	store          *Store
	journal        *OrchestratorJournal
	bufferRegistry *BufferRegistry
	dagBridge      *DAGBridge
	coordination   *CoordinationService
	scope          *concurrency.GoroutineScope

	// Pipeline subscriptions
	pipelineSubs []guide.Subscription
	dagSubs      []guide.Subscription
	taskSubs     []guide.Subscription

	// Handoff bridge for context-aware agent lifecycle management
	handoffBridge *handoff.HandoffBridge

	// Agent pod for cross-agent coordination (Scribe feed, etc.).
	agentPod *shared.AgentPod

	// Task router for DAG→container dispatch
	taskRouter   *TaskRouter
	dispatchGate *dispatchHoldGate

	// Per-session VFS infrastructure (maps sessionID → SessionVFS).
	sessionVFS   map[string]*versioning.SessionVFS
	sessionVFSMu sync.RWMutex

	// Auth credential change subscription.
	authSub guide.Subscription

	// Sync RPC (for Guardian preflight and other direct consultations).
	pendingMu  sync.Mutex
	pendingBus map[string]*shared.PendingSyncWait

	// refreshProvider re-resolves the LLM provider on auth changes.
	// Set via SetProviderRefresher from bootstrap. The authMethod parameter
	// carries the canonical auth mode from the AuthRegistry event.
	refreshProvider func(ctx context.Context, authMethod string)

	// File-backed WAL logger for debug tracing (visible via tail).
	logger *slog.Logger
	logWAL io.Closer

	mu sync.RWMutex

	pipelinePanelMu         sync.Mutex
	pipelinePanelState      map[string]pipelinePanelSnapshot
	pipelinePanelRegistered map[string]struct{}

	// Request lifecycle: runCtx is cancelled in Stop() and serves as parent
	// for per-request contexts, enabling graceful cancellation.
	runCtx    context.Context
	runCancel context.CancelFunc

	// Steering ledger management.
	steering *shared.SteeringManager

	// Request serialization: ensures at most one forwarded request
	// executes at a time, preventing cancel/new-request interleaving.
	requestSerializer *shared.RequestSerializer
}

type pipelinePanelSnapshot struct {
	EventType      events.EventType
	NodeID         string
	PipelineStatus string
}

// logInfo logs at Info level, safe to call when o.logger is nil.
func (o *Orchestrator) logInfo(msg string, args ...any) {
	if o != nil && o.logger != nil {
		o.logger.Info(msg, args...)
	}
}

// logWarnMsg logs at Warn level, safe to call when o.logger is nil.
func (o *Orchestrator) logWarnMsg(msg string, args ...any) {
	if o != nil && o.logger != nil {
		o.logger.Warn(msg, args...)
	}
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (o *Orchestrator) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	o.workspaceViews = authority.RestrictWorkspaceViews("orchestrator", views)
}

func (o *Orchestrator) SessionID() string {
	if o == nil {
		return "default"
	}
	return firstNonEmpty(strings.TrimSpace(o.config.SessionID), orchestratorStateSessionID(o), "default")
}

// New creates a new Orchestrator agent. The optional GoogleProvider enables
// LLM-driven event analysis. When nil, the orchestrator runs in deterministic
// fallback mode (critical events auto-escalate without model involvement).
// The optional ActivityPublisher enables UI agent panel visibility.
// The optional SylkDir enables persistent storage (WAL, SQLite, BufferRegistry).
func New(cfg Config, provider OrchestratorProvider, activityPub events.ActivityPublisher, sd *sylkdir.SylkDir) (*Orchestrator, error) {
	cfg = applyConfigDefaults(cfg)

	logger, logCloser, logErr := agentlog.NewWALLogger("orchestrator")
	if logErr != nil {
		slog.Warn("orchestrator: WAL logger unavailable, debug tracing disabled", "error", logErr)
	}

	skillsRegistry := skills.NewRegistry()
	hookRegistry := skills.NewHookRegistry()

	o := &Orchestrator{
		config:                  cfg,
		state:                   NewState(cfg.SessionID),
		skills:                  skillsRegistry,
		hooks:                   hookRegistry,
		knownAgents:             make(map[string]*guide.AgentAnnouncement),
		activityPub:             activityPub,
		sessionVFS:              make(map[string]*versioning.SessionVFS),
		logger:                  logger,
		logWAL:                  logCloser,
		steering:                shared.NewSteeringManager(),
		requestSerializer:       shared.NewRequestSerializer(),
		pendingBus:              make(map[string]*shared.PendingSyncWait),
		dispatchGate:            newDispatchHoldGate(),
		pipelinePanelState:      make(map[string]pipelinePanelSnapshot),
		pipelinePanelRegistered: make(map[string]struct{}),
	}

	if provider != nil {
		o.provider = provider
	}
	if cfg.EnableLLM {
		o.eventCh = make(chan *busEvent, llmEventBufferSize)
		o.bootGate = newBootstrapGate(cfg.BootstrapSafetyDeadline)
	}

	o.healthMonitor = NewHealthMonitor(o, cfg.HealthConfig)

	healthCache, err := NewHealthCache(DefaultHealthCacheConfig(cfg.HealthConfig))
	if err != nil {
		return nil, fmt.Errorf("create health cache: %w", err)
	}
	o.healthCache = healthCache
	o.healthMonitor.SetResultCallback(o.onHealthCheckResult)

	// Initialize data plane if SylkDir is available
	if sd != nil {
		if err := o.initDataPlane(cfg, sd, activityPub); err != nil {
			return nil, err
		}
		o.steering.InitLazy("orchestrator", activityPub)
		// Orchestrator has SylkDir at construction — pre-bind using legacy path
		// until session binding occurs on first request.
		o.steering.InitJournal("orchestrator", activityPub, sd.AgentSteeringPath("orchestrator"))
	}

	o.registerCoreSkills()
	if err := shared.RegisterMemoryForestSkills(o.skills, "orchestrator", cfg.Forest, nil); err != nil {
		return nil, fmt.Errorf("register orchestrator forest skills: %w", err)
	}
	skillsLoaderCfg := skills.DefaultLoaderConfig()
	skillsLoaderCfg.CoreSkills = orchestratorPinnedSkillNames()
	skillsLoaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	o.skillLoader = skills.NewLoader(skillsRegistry, skillsLoaderCfg)
	tools, err := toolruntime.New(toolruntime.Config{
		Registry: o.skills,
		Hooks:    o.hooks,
		Manifest: orchestratorToolManifest(o.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize orchestrator tool runtime: %w", err)
	}
	o.tools = tools
	o.tools.SyncActiveFromLoaded()

	return o, nil
}

// initDataPlane initializes the persistent data plane: SQLite, WAL, BufferRegistry, DAG Bridge.
func (o *Orchestrator) initDataPlane(cfg Config, sd *sylkdir.SylkDir, activityPub events.ActivityPublisher) error {
	// SQLite store
	store, err := OpenStore(DefaultStoreConfig(sd.OrchestratorDBPath()))
	if err != nil {
		return fmt.Errorf("orchestrator: open store: %w", err)
	}
	if err := store.Migrate(); err != nil {
		store.Close()
		return fmt.Errorf("orchestrator: migrate store: %w", err)
	}
	o.store = store

	// WAL journal
	journal, err := OpenOrchestratorJournal(sd.OrchestratorWALPath())
	if err != nil {
		store.Close()
		return fmt.Errorf("orchestrator: open journal: %w", err)
	}
	o.journal = journal

	// GoroutineScope
	// Budget derived from: maxDAGs * maxConcurrencyPerDAG
	pressure := &atomic.Int32{}
	budget := concurrency.NewGoroutineBudget(pressure)
	budget.RegisterAgent("orchestrator", "orchestrator")
	scope := concurrency.NewGoroutineScope(context.Background(), "orchestrator", budget)
	// Orchestrator-owned DAG work is asynchronous and can legitimately outlive
	// the request that submitted it by many minutes or hours.
	scope.SetMaxLifetime(24 * time.Hour)
	o.scope = scope

	// BufferRegistry
	buffers, err := NewBufferRegistry(
		DefaultBufferRegistryConfig(cfg.DAGConfig.MaxConcurrencyPerDAG),
		store, scope,
	)
	if err != nil {
		journal.Close()
		store.Close()
		return fmt.Errorf("orchestrator: create buffer registry: %w", err)
	}
	o.bufferRegistry = buffers

	coordSvc, err := NewCoordinationService(store, DefaultCoordinationServiceConfig())
	if err != nil {
		journal.Close()
		store.Close()
		return fmt.Errorf("orchestrator: create coordination service: %w", err)
	}
	coordSvc.SetArchiveEmitter(func(ctx context.Context, event *coordination.ArchivalEvent) {
		o.submitCoordinationEventAsync(event)
	})
	o.coordination = coordSvc

	// DAG Bridge
	o.dagBridge = NewDAGBridge(cfg.DAGConfig, DAGBridgeDeps{
		Store:        store,
		Journal:      journal,
		Buffers:      buffers,
		Scope:        scope,
		Orchestrator: o,
		ActivityPub:  activityPub,
		SessionID:    cfg.SessionID,
		AgentID:      cfg.AgentID,
	})
	o.dagBridge.SetDispatchPermitWaiter(func(ctx context.Context, sessionID, dagID string) error {
		if o.dispatchGate == nil {
			return nil
		}
		return o.dispatchGate.wait(ctx, sessionID, dagID)
	})
	o.dagBridge.SetExecutionHoldChecker(func(sessionID, dagID, nodeID string) bool {
		if o.dispatchGate == nil {
			return false
		}
		return o.dispatchGate.isHeld(sessionID, dagID)
	})

	return nil
}

// SetProvider hot-swaps the LLM provider. When called after Start with a
// non-nil provider and the LLM event loop has not yet started, the loop
// is started lazily. This supports deferred authorization — the user may
// configure credentials after the orchestrator is already running.
func (o *Orchestrator) SetProvider(provider OrchestratorProvider) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.provider = provider

	// Start the LLM event loop lazily when a provider arrives post-Start.
	if provider != nil && o.running && o.llmCtx == nil && o.eventCh != nil {
		o.llmCtx, o.llmCancel = context.WithCancel(context.Background())
		o.llmWg.Add(1)
		go func() {
			defer o.llmWg.Done()
			o.runLLMLoop(o.llmCtx)
		}()
		if o.bootGate != nil {
			o.bootGate.SignalReady()
		}
		o.publishActivity(events.EventTypeSuccess, "LLM provider activated (deferred auth)")
	}
}

// SetProviderRefresher registers a callback that re-resolves the LLM
// provider from credentials. Called from bootstrap to wire the
// orchestrator into the auth refresh flow.
func (o *Orchestrator) SetProviderRefresher(fn func(ctx context.Context, authMethod string)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.refreshProvider = fn
}

// SwapModel implements container.ModelSwappable.
// Installs the pre-built, gateway-wrapped provider and updates config.Model.
func (o *Orchestrator) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	o.SetProvider(provider)
	o.mu.Lock()
	o.config.Model = modelID
	o.mu.Unlock()
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (o *Orchestrator) CurrentModel() string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.config.Model
}

// SupportedModels implements container.ModelSwappable.
func (o *Orchestrator) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gemini-3-flash-preview", DisplayName: "Gemini 3 Flash"},
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
	}
}

// handleAuthChanged processes credential change events from the bus.
// Currently reacts to "google" provider changes for credential refresh.
func (o *Orchestrator) handleAuthChanged(msg *guide.Message) error {
	ev, ok := msg.GetAuthEvent()
	if !ok || ev == nil {
		return nil
	}
	if ev.ProviderType != "google" || !ev.Available {
		return nil
	}
	o.mu.RLock()
	refreshFn := o.refreshProvider
	o.mu.RUnlock()

	if refreshFn != nil {
		refreshFn(context.Background(), ev.AuthMethod)
	}
	return nil
}

// SetTaskRouter attaches the task router for DAG→container dispatch.
// Called after the activation controller and container registry are ready.
func (o *Orchestrator) SetTaskRouter(router *TaskRouter) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if router != nil && o.steering != nil {
		router.SetEventLogger(o.steering.EventLogger())
	}
	o.taskRouter = router
}

// SetActivator installs the on-demand agent activator, threading it to the
// DAG bridge so BusNodeDispatchers can activate cold agents before dispatch.
func (o *Orchestrator) SetActivator(a guide.PodActivator) {
	if o.dagBridge != nil {
		o.dagBridge.SetActivator(a)
	}
}

// SetRegistrar installs the pipeline registrar, threading it to the DAG
// bridge so task-scoped AgentPods can register activated agents with the Guide.
func (o *Orchestrator) SetRegistrar(fn PipelineRegistrar) {
	if o.dagBridge != nil {
		o.dagBridge.SetRegistrar(fn)
	}
	if o.logger != nil {
		o.dagBridge.SetLogger(o.logger)
	}
	if o.steering != nil {
		o.dagBridge.SetEventLogger(o.steering.EventLogger())
	}
}

// SetScribeFactory installs the Scribe factory, threading it to the DAG
// bridge so task-scoped AgentPods can create Scribe sidecars for pipeline agents.
func (o *Orchestrator) SetScribeFactory(f shared.ScribeFactory) {
	if o.dagBridge != nil {
		o.dagBridge.SetScribeFactory(f)
	}
}

// SetTaskPodInfra wires the runtime/spec/session-VFS dependencies needed for
// real task-scoped pipeline pods.
func (o *Orchestrator) SetTaskPodInfra(
	runtime container.ContainerRuntime,
	specReg *container.AgentSpecRegistry,
	sessionVFS func(sessionID string) *versioning.SessionVFS,
) {
	if o.dagBridge != nil {
		o.dagBridge.SetTaskPodInfra(runtime, specReg, sessionVFS)
	}
}

// SetContextQuota threads the live session token budget into DAG dispatch.
func (o *Orchestrator) SetContextQuota(quota *container.ResourceQuota) {
	if o.dagBridge != nil {
		o.dagBridge.SetContextQuota(quota)
	}
}

// SetSessionVFS associates a SessionVFS with the given session. Called when
// the Orchestrator creates a new session's CVS infrastructure.
func (o *Orchestrator) SetSessionVFS(sessionID string, svfs *versioning.SessionVFS) {
	o.sessionVFSMu.Lock()
	defer o.sessionVFSMu.Unlock()
	o.sessionVFS[sessionID] = svfs
}

// GetSessionVFS returns the SessionVFS for the given session, or nil.
func (o *Orchestrator) GetSessionVFS(sessionID string) *versioning.SessionVFS {
	o.sessionVFSMu.RLock()
	defer o.sessionVFSMu.RUnlock()
	return o.sessionVFS[sessionID]
}

// EnsureSessionVFS returns the session VFS for sessionID, creating it on first
// use with the provided working directory.
func (o *Orchestrator) EnsureSessionVFS(sessionID, workingDir string) *versioning.SessionVFS {
	if svfs := o.GetSessionVFS(sessionID); svfs != nil {
		return svfs
	}

	o.sessionVFSMu.Lock()
	defer o.sessionVFSMu.Unlock()
	if svfs := o.sessionVFS[sessionID]; svfs != nil {
		return svfs
	}
	svfs, err := versioning.NewSessionVFS(versioning.SessionVFSConfig{
		SessionID:       versioning.SessionID(sessionID),
		WorkingDir:      workingDir,
		AllowDiskExport: true,
	})
	if err != nil {
		o.logWarnMsg("initialize session VFS", "session_id", sessionID, "error", err)
		return nil
	}
	o.sessionVFS[sessionID] = svfs
	return svfs
}

// CloseSessionVFS tears down the VFS infrastructure for a session.
func (o *Orchestrator) CloseSessionVFS(sessionID string) error {
	o.sessionVFSMu.Lock()
	svfs, ok := o.sessionVFS[sessionID]
	if ok {
		delete(o.sessionVFS, sessionID)
	}
	o.sessionVFSMu.Unlock()

	if !ok || svfs == nil {
		return nil
	}
	return svfs.Close()
}

// AllSessionVFS returns a snapshot of all active SessionVFS instances.
// Used by the Guardian for cross-session VFS observability.
func (o *Orchestrator) AllSessionVFS() []*versioning.SessionVFS {
	o.sessionVFSMu.RLock()
	defer o.sessionVFSMu.RUnlock()
	result := make([]*versioning.SessionVFS, 0, len(o.sessionVFS))
	for _, svfs := range o.sessionVFS {
		result = append(result, svfs)
	}
	return result
}

// SignalReady marks bootstrap as complete, unblocking the LLM event loop.
// No-op when the LLM is disabled (bootGate is nil). Idempotent.
func (o *Orchestrator) SignalReady() {
	if o.bootGate == nil {
		return
	}
	o.bootGate.SignalReady()
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.Model == "" {
		cfg.Model = "gemini-3-flash-preview"
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = 2048
	}
	if cfg.MaxToolRuns == 0 {
		cfg.MaxToolRuns = 32
	}
	if cfg.LLMTimeout == 0 {
		cfg.LLMTimeout = 90 * time.Second
	}
	if cfg.AgentID == "" {
		cfg.AgentID = "orchestrator"
	}
	if cfg.HealthConfig.TaskTimeout == 0 {
		cfg.HealthConfig = DefaultHealthConfig()
	}
	if cfg.BufferConfig.MaxUpdates == 0 {
		cfg.BufferConfig = DefaultUpdateBufferConfig()
	}
	if cfg.SummaryConfig.MaxTokens == 0 {
		cfg.SummaryConfig = DefaultSummaryConfig()
	}
	if cfg.DAGConfig.MaxConcurrentDAGs == 0 {
		cfg.DAGConfig = DefaultDAGBridgeConfig()
	}
	if cfg.BootstrapSafetyDeadline == 0 {
		cfg.BootstrapSafetyDeadline = defaultBootstrapSafetyDur
	}
	return cfg
}

// Start begins listening for messages on the event bus
func (o *Orchestrator) Start(bus guide.EventBus) error {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return fmt.Errorf("orchestrator is already running")
	}

	o.bus = bus
	o.channels = guide.NewAgentChannels(o.config.AgentID, o.config.AgentID)
	o.runCtx, o.runCancel = context.WithCancel(context.Background())

	var err error
	o.requestSub, err = bus.SubscribeAsync(o.channels.Requests, o.handleBusRequest)
	if err != nil {
		o.mu.Unlock()
		return fmt.Errorf("failed to subscribe to %s: %w", o.channels.Requests, err)
	}

	o.responseSub, err = bus.SubscribeAsync(o.channels.Responses, o.handleBusResponse)
	if err != nil {
		o.requestSub.Unsubscribe()
		o.mu.Unlock()
		return fmt.Errorf("failed to subscribe to %s: %w", o.channels.Responses, err)
	}

	o.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, o.handleRegistryAnnouncement)
	if err != nil {
		o.requestSub.Unsubscribe()
		o.responseSub.Unsubscribe()
		o.mu.Unlock()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	o.subscribeToTaskEvents()

	// Subscribe to credential changes so the Orchestrator refreshes its own
	// Google provider when the user authenticates.
	authSub, authErr := bus.SubscribeAsync(guide.TopicAuthCredentials, o.handleAuthChanged)
	if authErr != nil {
		slog.Warn("orchestrator: failed to subscribe to auth credentials topic", "error", authErr)
	} else {
		o.authSub = authSub
	}

	o.healthMonitor.Start(context.Background())

	if o.provider != nil && o.eventCh != nil {
		o.llmCtx, o.llmCancel = context.WithCancel(context.Background())
		o.llmWg.Add(1)
		go func() {
			defer o.llmWg.Done()
			o.runLLMLoop(o.llmCtx)
		}()
	}

	// Wire data plane if available
	if o.dagBridge != nil {
		if o.store != nil && o.dispatchGate != nil {
			if hold, holdErr := o.store.GetActiveExecutionHold(o.config.SessionID); holdErr == nil && hold != nil {
				o.dispatchGate.activate(o.config.SessionID)
			}
		}
		o.dagBridge.SetBus(bus)
		o.subscribePipelineTopics()
		o.subscribeDAGTopics()
		o.dagBridge.RecoverFromWAL(context.Background())
		o.reconcilePlanHandoffReceipts()
		o.bufferRegistry.StartGC(context.Background())
		o.startWALGC()
	}

	o.running = true
	hasProvider := o.provider != nil
	o.mu.Unlock()

	llmStatus := "deterministic fallback"
	if hasProvider {
		llmStatus = "Gemini Flash active"
	}
	o.publishActivity(events.EventTypeSuccess, "Orchestrator started ("+llmStatus+")")

	return nil
}

func (o *Orchestrator) subscribeToTaskEvents() {
	topics := []struct {
		topic   string
		handler guide.MessageHandler
	}{
		{"tasks.dispatch", o.handleTaskDispatch},
		{"tasks.complete", o.handleTaskComplete},
		{"tasks.failed", o.handleTaskFailed},
		{"workflows.status", o.handleWorkflowStatus},
	}
	for _, t := range topics {
		if sub, err := o.bus.SubscribeAsync(t.topic, t.handler); err == nil {
			o.taskSubs = append(o.taskSubs, sub)
			o.logTrace("task_topic_subscribed", agentlog.EventRegistryEvent, map[string]any{
				"topic": t.topic,
			})
		} else {
			o.logTrace("task_topic_subscribe_failed", agentlog.EventError, map[string]any{
				"topic": t.topic,
				"error": err.Error(),
			})
		}
	}
}

// Stop unsubscribes from event bus topics and shuts down the LLM loop.
//
// Shutdown order matters to avoid deadlocks:
//  1. Mark not running (under lock) so handlers become no-ops
//  2. Release lock before blocking operations
//  3. Cancel LLM context and wait for goroutine exit (needs RLock internally)
//  4. Stop health monitor, flush buffers, unsubscribe
func (o *Orchestrator) Stop() error {
	o.mu.Lock()
	if !o.running {
		o.mu.Unlock()
		return nil
	}
	o.running = false
	llmCancel := o.llmCancel
	o.mu.Unlock()

	o.steering.CloseAll()

	// Cancel the request lifecycle context so in-flight requests unwind.
	if o.runCancel != nil {
		o.runCancel()
	}

	// Cancel the LLM loop context and wait for the goroutine to finish.
	// This must happen outside o.mu because the LLM loop acquires RLock
	// for state snapshots.
	if llmCancel != nil {
		llmCancel()
	}
	o.llmWg.Wait()

	// Drain any remaining events after the LLM loop has exited.
	if o.eventCh != nil {
		for len(o.eventCh) > 0 {
			<-o.eventCh
		}
	}

	o.healthMonitor.Stop()
	o.healthCache.Close()

	// Cancel all active DAGs before flushing buffers
	if o.dagBridge != nil {
		o.dagBridge.CancelAll("orchestrator shutting down")
	}

	// flushUpdateBuffer acquires o.mu internally — safe now that we
	// released it above.
	o.flushUpdateBuffer()

	// Close data plane: flush buffers → close journal → close store → shutdown scope
	if o.bufferRegistry != nil {
		o.bufferRegistry.Close()
	}
	if o.coordination != nil {
		o.coordination.Close()
	}
	if o.dagBridge != nil {
		o.dagBridge.Close()
	}
	if o.journal != nil {
		o.journal.Close()
	}
	if o.store != nil {
		o.store.Close()
	}
	if o.scope != nil {
		o.scope.Shutdown(5*time.Second, 10*time.Second)
	}
	if o.tools != nil {
		o.tools.Close()
		o.tools = nil
	}

	o.mu.Lock()
	errs := o.unsubscribeAll()
	o.mu.Unlock()

	if o.logWAL != nil {
		o.logWAL.Close()
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

func (o *Orchestrator) unsubscribeAll() []error {
	var errs []error
	for _, sub := range []guide.Subscription{o.requestSub, o.responseSub, o.registrySub, o.authSub} {
		if sub != nil {
			if err := sub.Unsubscribe(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for _, sub := range o.taskSubs {
		if err := sub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	o.taskSubs = nil
	for _, sub := range o.pipelineSubs {
		if err := sub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	o.pipelineSubs = nil
	for _, sub := range o.dagSubs {
		if err := sub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	o.dagSubs = nil
	return errs
}

// Handle processes workflow coordination requests
func (o *Orchestrator) Handle(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	o.publishActivity(events.EventTypeAgentAction, "Processing request...")
	o.logInfo("Handle: entry",
		"intent", string(req.Intent),
		"correlation_id", req.CorrelationID,
		"input_prefix", truncateForLog(req.Input, 120))

	if controlResult, handled, controlErr := o.handleControlPlaneForward(ctx, req); handled {
		return controlResult, controlErr
	}

	// Detect structured plan handoff payloads from the architect.
	// On match, ingest mechanically then route through the conversation
	// pipeline so the LLM produces a natural language acknowledgment.
	if result, ok := o.tryIngestPlanFromInput(ctx, req.Input); ok {
		o.publishActivity(events.EventTypeAgentAction, "Ingesting execution plan...")
		o.logInfo("Handle: plan ingested, generating response",
			"correlation_id", req.CorrelationID)
		return o.respondToIngestion(ctx, req, result)
	}

	o.logInfo("Handle: routing by intent",
		"intent", string(req.Intent))

	switch req.Intent {
	case guide.IntentStatus:
		return o.handleStatusQuery(ctx, req)
	case guide.IntentRecall:
		return o.handleRecallQuery(ctx, req)
	case guide.IntentHelp, guide.IntentChat, guide.IntentUnknown:
		return o.handleConversation(ctx, req)
	default:
		return o.handleConversation(ctx, req)
	}
}

func (o *Orchestrator) handleStatusQuery(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	o.mu.RLock()
	text := o.formatStatusText(req)
	o.mu.RUnlock()

	o.publishStreamChunk(ctx, text)
	return &ConversationResult{Response: text, Intent: "status"}, nil
}

// formatStatusText produces human-readable text for the requested status
// domain. Must be called under o.mu.RLock().
func (o *Orchestrator) formatStatusText(req *guide.ForwardedRequest) string {
	switch req.Domain {
	case guide.DomainTasks:
		if req.Entities == nil || req.Entities.Query == "" {
			return formatTasksAsText(o.state.Tasks)
		}
		task, ok := o.state.Tasks[req.Entities.Query]
		if !ok {
			return fmt.Sprintf("Task not found: %s", req.Entities.Query)
		}
		return formatSingleTaskAsText(task)

	case "workflow", "workflows":
		if req.Entities == nil || req.Entities.Query == "" {
			return formatWorkflowsAsText(o.state.Workflows)
		}
		wf, ok := o.state.Workflows[req.Entities.Query]
		if !ok {
			return fmt.Sprintf("Workflow not found: %s", req.Entities.Query)
		}
		return formatSingleWorkflowAsText(wf)

	default:
		return o.generateOverview()
	}
}

func (o *Orchestrator) handleRecallQuery(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	if req.Domain == guide.DomainFailures {
		patterns, err := o.queryFailurePatterns(ctx, req.Entities)
		if err != nil {
			return nil, err
		}
		text := formatFailurePatternsAsText(patterns)
		o.publishStreamChunk(ctx, text)
		return &ConversationResult{Response: text, Intent: "recall"}, nil
	}

	o.mu.RLock()
	text := formatStateOverviewAsText(o.state)
	o.mu.RUnlock()

	o.publishStreamChunk(ctx, text)
	return &ConversationResult{Response: text, Intent: "recall"}, nil
}

func (o *Orchestrator) queryFailurePatterns(ctx context.Context, entities *guide.ExtractedEntities) ([]FailurePattern, error) {
	query := FailureQuery{Limit: 10}
	if entities != nil && entities.AgentID != "" {
		query.AgentIDs = []string{entities.AgentID}
	}
	return o.QueryArchivalistForFailures(ctx, query)
}

// handleBusRequest processes incoming requests from the event bus
func (o *Orchestrator) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		action, ok := msg.GetActionRequest()
		if ok && action != nil {
			if handled, err := o.handleCoordinationAction(o.runCtx, action); handled {
				return err
			}
			o.steering.HandleAction(action)
		}
		return nil
	}
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	if !o.requestSerializer.Acquire(o.runCtx) {
		return nil // runCtx cancelled, agent shutting down
	}
	defer o.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	o.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(o.steering.EventLogger(), fwd, o.config.AgentID)

	reqCtx, cancel := context.WithCancel(o.runCtx)
	cancelEntry := o.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer cancel()

	// Cascading cancel: when this request is interrupted, cancel all
	// in-flight pipeline agents and running DAGs.
	cancelEntry.AddHook(func() {
		o.mu.RLock()
		router := o.taskRouter
		o.mu.RUnlock()
		if router != nil {
			router.CancelAllPending("request interrupted")
		}
		if o.dagBridge != nil {
			o.dagBridge.CancelAll("request interrupted")
		}
	})

	ctx := reqCtx
	startTime := time.Now()

	o.logInfo("handleBusRequest: received",
		"correlation_id", fwd.CorrelationID,
		"source_agent", fwd.SourceAgentID,
		"intent", string(fwd.Intent),
		"fire_and_forget", fwd.FireAndForget,
		"input_len", len(fwd.Input))

	// Always set up stream context — the Guide may promote IntentChat to
	// IntentHelp, so we cannot predicate streaming on the incoming intent.
	// This mirrors the architect's handleForwardBusRequest pattern.
	ctx = withOrchestratorStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = shared.WithForwardedStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, fwd.Metadata)
	ctx, usageAcc := withOrchestratorUsageAccumulator(ctx)

	toolEmitter := shared.NewToolCallEmitter(o.bus, o.channels, o.config.AgentID, fwd.CorrelationID, fwd.SourceAgentID)
	ctx = shared.WithToolCallEmitter(ctx, toolEmitter)

	// Create steering ledger for this request.
	ledger := o.steering.Create(fwd.CorrelationID, o.config.AgentID, fwd.SessionID, o.activityPub, nil)
	defer o.steering.Close(fwd.CorrelationID, ctx.Err() != nil)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: o.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     o.config.AgentID,
		SessionID:   fwd.SessionID,
	})
	gov := shared.NewContextGovernor(o.config.Model, o.config.MaxOutputTokens, 0)
	if o.handoffBridge != nil {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			return o.handoffBridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = shared.WithContextGovernor(ctx, gov)
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus: o.bus, Channels: o.channels,
		AgentID: o.config.AgentID, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})

	publishStreamLifecycle := guide.ShouldPublishForwardedStreamLifecycle(fwd)
	if publishStreamLifecycle {
		o.logInfo("handleBusRequest: publishing StreamStart",
			"correlation_id", fwd.CorrelationID)
		o.publishStreamStart(ctx)
	}

	result, err := o.Handle(ctx, fwd)
	shared.LogResponse(o.steering.EventLogger(), fwd.CorrelationID, o.config.AgentID, fwd.SessionID, time.Since(startTime), err)

	o.logInfo("handleBusRequest: Handle returned",
		"correlation_id", fwd.CorrelationID,
		"has_result", result != nil,
		"has_error", err != nil,
		"duration", time.Since(startTime))

	if err != nil {
		if publishStreamLifecycle {
			o.publishStreamError(ctx, err)
			o.publishStreamComplete(ctx, "", usageAcc.Total())
		}
		if fwd.FireAndForget {
			return nil
		}
		resp := &guide.RouteResponse{
			CorrelationID:       fwd.CorrelationID,
			Success:             false,
			RespondingAgentID:   o.config.AgentID,
			RespondingAgentName: "Orchestrator",
			ProcessingTime:      time.Since(startTime),
			Error:               err.Error(),
		}
		// Publish to BOTH error and response channels. The response channel
		// is what the Guide relays to the source agent's synchronous waiter;
		// the error channel is for observability/logging subscribers.
		respMsg := guide.NewResponseMessage(generateMessageID(), resp)
		_ = o.bus.Publish(o.channels.Responses, respMsg)
		errMsg := guide.NewErrorMessage(
			generateMessageID(),
			fwd.CorrelationID,
			o.config.AgentID,
			err.Error(),
		)
		return o.bus.Publish(o.channels.Errors, errMsg)
	}

	// Conversation text is already streamed via chunks — send complete with
	// empty text so the bridge doesn't duplicate content.
	completeText := extractOrchestratorUserResponse(result)
	if isStreamedOrchestratorConversation(result) {
		completeText = ""
	}
	if publishStreamLifecycle {
		o.publishStreamComplete(ctx, completeText, usageAcc.Total())
	}
	if fwd.FireAndForget {
		return nil
	}

	if o.agentPod != nil {
		o.agentPod.FeedScribe("orchestrator", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		RespondingAgentID:   o.config.AgentID,
		RespondingAgentName: "Orchestrator",
		ProcessingTime:      time.Since(startTime),
		Data:                result,
	}
	respMsg := guide.NewResponseMessage(generateMessageID(), resp)
	return o.bus.Publish(o.channels.Responses, respMsg)
}

func (o *Orchestrator) handleBusResponse(msg *guide.Message) error {
	if msg == nil {
		o.logTrace("bus_response_nil", agentlog.EventError, nil)
		return nil
	}
	o.logTrace("bus_response_received", agentlog.EventTaskDispatched, map[string]any{
		"correlation_id":  msg.CorrelationID,
		"message_type":    string(msg.Type),
		"source_agent_id": msg.SourceAgentID,
		"target_agent_id": msg.TargetAgentID,
	})
	if o.deliverPendingMessage(msg) {
		o.logTrace("bus_response_sync_waiter_delivered", agentlog.EventTaskDispatched, map[string]any{
			"correlation_id": msg.CorrelationID,
			"message_type":   string(msg.Type),
		})
		return nil
	}

	o.mu.RLock()
	router := o.taskRouter
	o.mu.RUnlock()

	if router != nil {
		if router.DeliverResponse(msg) {
			o.logTrace("bus_response_task_router_delivered", agentlog.EventTaskDispatched, map[string]any{
				"correlation_id": msg.CorrelationID,
				"message_type":   string(msg.Type),
			})
		} else {
			o.logTrace("bus_response_unhandled", agentlog.EventError, map[string]any{
				"correlation_id":  msg.CorrelationID,
				"message_type":    string(msg.Type),
				"source_agent_id": msg.SourceAgentID,
				"target_agent_id": msg.TargetAgentID,
			})
		}
	} else {
		slog.Warn("response dropped: task router not yet wired", "correlation_id", msg.CorrelationID)
		o.logTrace("bus_response_task_router_missing", agentlog.EventError, map[string]any{
			"correlation_id": msg.CorrelationID,
			"message_type":   string(msg.Type),
		})
	}
	return nil
}

func (o *Orchestrator) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		o.knownAgents[ann.AgentID] = ann
		o.healthMonitor.RegisterAgent(ann.AgentID)
		o.pushEvent(&busEvent{
			Topic:     guide.TopicAgentRegistry,
			Timestamp: time.Now(),
			Severity:  severityInfo,
			Summary:   fmt.Sprintf("Agent %q registered", ann.AgentID),
			Data:      map[string]any{"agent_id": ann.AgentID},
		})
	case guide.MessageTypeAgentUnregistered:
		delete(o.knownAgents, ann.AgentID)
		o.healthMonitor.UnregisterAgent(ann.AgentID)
		o.pushEvent(&busEvent{
			Topic:     guide.TopicAgentRegistry,
			Timestamp: time.Now(),
			Severity:  severityInfo,
			Summary:   fmt.Sprintf("Agent %q unregistered", ann.AgentID),
			Data:      map[string]any{"agent_id": ann.AgentID},
		})
	}

	return nil
}

// Task event handlers
func (o *Orchestrator) handleTaskDispatch(msg *guide.Message) error {
	o.logTrace("task_dispatch_received", agentlog.EventTaskDispatched, map[string]any{
		"source_agent_id": msg.SourceAgentID,
		"message_type":    msg.Type,
	})
	dispatch, ok := parseTaskDispatchMessage(msg)
	if !ok {
		o.logTrace("task_dispatch_parse_failed", agentlog.EventError, map[string]any{
			"source_agent_id": msg.SourceAgentID,
		})
		return nil
	}
	o.logTrace("task_dispatch_parsed", agentlog.EventTaskDispatched, map[string]any{
		"dag_id":      dispatch.dagID,
		"node_id":     dispatch.nodeID,
		"task_id":     dispatch.taskID,
		"agent_type":  dispatch.agentType,
		"task_slug":   dispatch.taskSlug,
		"task_stage":  dispatch.pipelineStage,
		"co_agents":   dispatch.coAgents,
		"workflow_id": dispatch.workflowID,
	})

	router := o.registerTaskDispatch(dispatch)
	o.logTrace("task_dispatch_registered", agentlog.EventTaskDispatched, map[string]any{
		"dag_id":     dispatch.dagID,
		"node_id":    dispatch.nodeID,
		"task_id":    dispatch.taskID,
		"agent_type": dispatch.agentType,
	})
	pipelineStatus := o.publishTaskDispatchPipelineState(dispatch)
	o.logTrace("task_dispatch_pipeline_state_published", agentlog.EventPipelineStateChange, map[string]any{
		"dag_id":          dispatch.dagID,
		"node_id":         dispatch.nodeID,
		"task_id":         dispatch.taskID,
		"pipeline_status": pipelineStatus,
	})
	o.acknowledgeTaskDispatch(router, dispatch)
	o.pushEvent(dispatch.event())
	o.publishTaskDispatchAgents(dispatch, pipelineStatus)
	o.logTrace("task_dispatch_agents_published", agentlog.EventTaskDispatched, map[string]any{
		"dag_id":          dispatch.dagID,
		"node_id":         dispatch.nodeID,
		"task_id":         dispatch.taskID,
		"agent_type":      dispatch.agentType,
		"pipeline_status": pipelineStatus,
	})
	o.enrichTaskDispatchCoordination(dispatch)
	o.routeTaskDispatch(router, dispatch)
	o.warmTaskDispatchCoordination(dispatch)
	o.logTrace("task_dispatch_handled", agentlog.EventTaskDispatched, map[string]any{
		"dag_id":     dispatch.dagID,
		"node_id":    dispatch.nodeID,
		"task_id":    dispatch.taskID,
		"agent_type": dispatch.agentType,
	})
	return nil
}

func decodeDispatchAgentTypes(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, entry := range typed {
			if s, ok := entry.(string); ok && strings.TrimSpace(s) != "" {
				result = append(result, strings.TrimSpace(s))
			}
		}
		return result
	default:
		return nil
	}
}

func parseDispatchCollaborationMode(value any) dag.CollaborationMode {
	raw, _ := value.(string)
	return parseHandoffCollaborationMode(raw)
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func canonicalPipelineTaskIdentity(taskID, taskSlug string, nodeCtx map[string]any, nodeID string) (string, string) {
	canonicalTaskID := strings.TrimSpace(taskID)
	canonicalTaskSlug := strings.TrimSpace(taskSlug)

	if nodeCtx != nil {
		if canonicalTaskSlug == "" {
			if ctxSlug, ok := nodeCtx["task_slug"].(string); ok {
				canonicalTaskSlug = strings.TrimSpace(ctxSlug)
			}
		}
		if canonicalTaskID == "" {
			if ctxTaskID, ok := nodeCtx["task_id"].(string); ok {
				canonicalTaskID = strings.TrimSpace(ctxTaskID)
			}
		}
	}
	if canonicalTaskID == "" {
		canonicalTaskID = strings.TrimSpace(nodeID)
	}

	return canonicalTaskID, canonicalTaskSlug
}

func pipelineWorkerTargetAgentID(taskID, agentType string) string {
	return PipelineWorkerRoutingTarget(taskID, agentType)
}

func (o *Orchestrator) handleTaskComplete(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		o.logTrace("task_complete_payload_invalid", agentlog.EventError, map[string]any{
			"message_type": string(msg.Type),
		})
		return nil
	}

	taskID, _ := data["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		o.logTrace("task_complete_missing_task_id", agentlog.EventError, map[string]any{
			"node_id": data["node_id"],
		})
		return nil
	}
	result := data["result"]

	o.mu.Lock()
	task, ok := o.state.Tasks[taskID]
	if !ok {
		o.mu.Unlock()
		o.logTrace("task_complete_unknown_task", agentlog.EventError, map[string]any{
			"task_id": taskID,
		})
		return nil
	}
	o.mu.Unlock()

	mergeVersion, hadDraft, mergeErr := o.commitTaskDraft(context.Background(), task)
	if mergeErr != nil {
		o.publishTaskDraftMergeFailure(task, mergeErr)
	} else if hadDraft {
		o.publishTaskDraftMergeSuccess(task, mergeVersion)
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	task = o.state.Tasks[taskID]
	if task == nil {
		return nil
	}
	now := time.Now()
	if mergeErr != nil {
		task.Status = TaskStatusFailed
		task.CompletedAt = &now
		task.Error = mergeErr.Error()
		o.state.Stats.FailedTasks++

		o.healthMonitor.RecordTaskFailed(task.AssignedAgentID, taskID, mergeErr.Error())
		o.updateWorkflowProgress(task.WorkflowID)

		if o.dagBridge != nil {
			if nodeID, hasNode := data["node_id"].(string); hasNode && nodeID != "" {
				o.dagBridge.NotifyNodeComplete(nodeID, convertTaskFailedToNodeResult(task))
			}
		}

		o.submitTaskEventAsync(task)
		if o.coordination != nil {
			_ = o.coordination.ReleaseTaskClaims(context.Background(), taskID)
		}

		o.pushEvent(&busEvent{
			Topic:     "tasks.failed",
			Timestamp: now,
			Severity:  severityCritical,
			Summary:   fmt.Sprintf("Task %q failed during draft merge on agent %s: %s", task.Name, task.AssignedAgentID, mergeErr.Error()),
			Data:      map[string]any{"task_id": taskID, "agent_id": task.AssignedAgentID, "error": mergeErr.Error()},
		})
		return nil
	}

	task.Status = TaskStatusCompleted
	task.CompletedAt = &now
	task.Result = result
	o.state.Stats.CompletedTasks++

	o.healthMonitor.RecordTaskComplete(task.AssignedAgentID, taskID)
	o.updateWorkflowProgress(task.WorkflowID)

	// Notify DAG bridge for DAG-originated task completions.
	if o.dagBridge != nil {
		if nodeID, hasNode := data["node_id"].(string); hasNode && nodeID != "" {
			o.dagBridge.NotifyNodeComplete(nodeID, convertTaskCompleteToNodeResult(task))
		}
	}

	o.submitTaskEventAsync(task)
	if o.coordination != nil {
		_ = o.coordination.ReleaseTaskClaims(context.Background(), taskID)
	}

	o.pushEvent(&busEvent{
		Topic:     "tasks.complete",
		Timestamp: now,
		Severity:  severityInfo,
		Summary:   fmt.Sprintf("Task %q completed on agent %s", task.Name, task.AssignedAgentID),
		Data:      map[string]any{"task_id": taskID, "agent_id": task.AssignedAgentID},
	})

	return nil
}

func (o *Orchestrator) handleTaskFailed(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		o.logTrace("task_failed_payload_invalid", agentlog.EventError, map[string]any{
			"message_type": string(msg.Type),
		})
		return nil
	}

	taskID, _ := data["task_id"].(string)
	errorMsg, _ := data["error"].(string)
	if strings.TrimSpace(taskID) == "" {
		o.logTrace("task_failed_missing_task_id", agentlog.EventError, map[string]any{
			"error": errorMsg,
		})
		return nil
	}

	o.mu.Lock()
	task, ok := o.state.Tasks[taskID]
	if !ok {
		o.mu.Unlock()
		o.logTrace("task_failed_unknown_task", agentlog.EventError, map[string]any{
			"task_id": taskID,
			"error":   errorMsg,
		})
		return nil
	}
	o.mu.Unlock()

	if rollbackErr := o.rollbackTaskDraft(task); rollbackErr != nil {
		errorMsg = firstNonEmpty(errorMsg, rollbackErr.Error())
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	task = o.state.Tasks[taskID]
	if task == nil {
		return nil
	}
	now := time.Now()
	task.Status = TaskStatusFailed
	task.CompletedAt = &now
	task.Error = errorMsg
	o.state.Stats.FailedTasks++

	o.healthMonitor.RecordTaskFailed(task.AssignedAgentID, taskID, errorMsg)
	o.updateWorkflowProgress(task.WorkflowID)

	// Notify DAG bridge for DAG-originated task failures.
	if o.dagBridge != nil {
		if nodeID, hasNode := data["node_id"].(string); hasNode && nodeID != "" {
			o.dagBridge.NotifyNodeComplete(nodeID, convertTaskFailedToNodeResult(task))
		}
	}

	o.submitTaskEventAsync(task)
	if o.coordination != nil {
		_ = o.coordination.ReleaseTaskClaims(context.Background(), taskID)
	}

	o.pushEvent(&busEvent{
		Topic:     "tasks.failed",
		Timestamp: now,
		Severity:  severityCritical,
		Summary:   fmt.Sprintf("Task %q failed on agent %s: %s", task.Name, task.AssignedAgentID, errorMsg),
		Data:      map[string]any{"task_id": taskID, "agent_id": task.AssignedAgentID, "error": errorMsg},
	})

	return nil
}

func (o *Orchestrator) handleWorkflowStatus(msg *guide.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	data, ok := msg.Payload.(map[string]any)
	if !ok {
		o.logTrace("workflow_status_payload_invalid", agentlog.EventError, map[string]any{
			"message_type": string(msg.Type),
		})
		return nil
	}

	workflowID, _ := data["workflow_id"].(string)
	statusStr, _ := data["status"].(string)
	phase, _ := data["phase"].(string)
	if strings.TrimSpace(workflowID) == "" {
		o.logTrace("workflow_status_missing_workflow_id", agentlog.EventError, map[string]any{
			"status": statusStr,
			"phase":  phase,
		})
		return nil
	}

	workflow, ok := o.state.Workflows[workflowID]
	if !ok {
		workflow = &WorkflowState{
			ID:        workflowID,
			Status:    WorkflowStatusPending,
			StartedAt: time.Now(),
			SessionID: o.config.SessionID,
		}
		o.state.Workflows[workflowID] = workflow
		o.state.Stats.TotalWorkflows++
	}

	workflow.Status = WorkflowStatus(statusStr)
	workflow.Phase = phase
	workflow.UpdatedAt = time.Now()

	sev := severityInfo
	if workflow.Status.IsTerminal() {
		now := time.Now()
		workflow.CompletedAt = &now
		o.state.Stats.ActiveWorkflows--
		sev = severityWarning
	}

	o.pushEvent(&busEvent{
		Topic:     "workflows.status",
		Timestamp: time.Now(),
		Severity:  sev,
		Summary:   fmt.Sprintf("Workflow %q status: %s (phase: %s)", workflowID, statusStr, phase),
		Data:      map[string]any{"workflow_id": workflowID, "status": statusStr, "phase": phase},
	})

	return nil
}

func (o *Orchestrator) updateWorkflowProgress(workflowID string) {
	if workflowID == "" {
		return
	}

	workflow, ok := o.state.Workflows[workflowID]
	if !ok {
		return
	}

	completed := 0
	failed := 0
	for _, taskID := range workflow.TaskIDs {
		if task, ok := o.state.Tasks[taskID]; ok {
			switch task.Status {
			case TaskStatusCompleted:
				completed++
			case TaskStatusFailed, TaskStatusTimedOut, TaskStatusCancelled:
				failed++
			}
		}
	}

	workflow.CompletedIDs = workflow.CompletedIDs[:0]
	workflow.FailedIDs = workflow.FailedIDs[:0]
	for _, taskID := range workflow.TaskIDs {
		if task, ok := o.state.Tasks[taskID]; ok {
			if task.Status == TaskStatusCompleted {
				workflow.CompletedIDs = append(workflow.CompletedIDs, taskID)
			} else if task.Status.IsTerminal() {
				workflow.FailedIDs = append(workflow.FailedIDs, taskID)
			}
		}
	}

	total := len(workflow.TaskIDs)
	if total > 0 {
		workflow.Progress = float64(completed+failed) / float64(total)
	}
}

// submitTaskEvent submits a task event to Archivalist for terminal states
const submitTaskEventTimeout = 30 * time.Second

// submitTaskEventAsync dispatches submitTaskEventCtx via the GoroutineScope,
// replacing all former bare `go o.submitTaskEvent(task)` call sites.
func (o *Orchestrator) submitTaskEventAsync(task *TaskRecord) {
	if o.scope == nil {
		return
	}
	o.scope.Go("submit-task-event", submitTaskEventTimeout, func(ctx context.Context) error {
		o.submitTaskEventCtx(ctx, task)
		return nil
	})
}

// submitTaskEventCtx submits a terminal task event to Archivalist using the
// provided context (from scope) instead of context.Background(). Acquires
// o.mu to read task fields atomically, fixing the prior data-race on
// task.EventSubmitted.
func (o *Orchestrator) submitTaskEventCtx(ctx context.Context, task *TaskRecord) {
	if !o.config.ArchivalistEnabled {
		return
	}

	o.mu.RLock()
	alreadySubmitted := task.EventSubmitted
	isTerminal := task.Status.IsTerminal()
	o.mu.RUnlock()

	if alreadySubmitted || !isTerminal {
		return
	}

	event := &TaskEvent{
		ID:          generateMessageID(),
		Type:        taskEventType(task.Status),
		Timestamp:   time.Now(),
		TaskID:      task.ID,
		TaskName:    task.Name,
		WorkflowID:  task.WorkflowID,
		Status:      task.Status,
		AgentID:     task.AssignedAgentID,
		Result:      task.Result,
		Error:       task.Error,
		CompletedAt: time.Now(),
		SessionID:   o.config.SessionID,
		Metadata:    task.Metadata,
	}

	if task.StartedAt != nil {
		event.StartedAt = task.StartedAt
		event.Duration = time.Since(*task.StartedAt)
	}

	o.SubmitEventToArchivalist(ctx, event)

	o.mu.Lock()
	task.EventSubmitted = true
	now := time.Now()
	task.SubmittedAt = &now
	o.state.Stats.EventsSubmitted++
	o.mu.Unlock()
}

func taskEventType(status TaskStatus) string {
	switch status {
	case TaskStatusCompleted:
		return "task_completed"
	case TaskStatusFailed:
		return "task_failed"
	case TaskStatusTimedOut:
		return "task_timed_out"
	case TaskStatusCancelled:
		return "task_cancelled"
	default:
		return "task_terminal"
	}
}

// SubmitEventToArchivalist sends a task event to Archivalist
func (o *Orchestrator) SubmitEventToArchivalist(ctx context.Context, event *TaskEvent) error {
	if o.bus == nil || !o.running {
		return fmt.Errorf("orchestrator not running")
	}
	input, err := guide.ArchivalistStoreRouteInput(fmt.Sprintf("stored task event: %s", event.Type))
	if err != nil {
		return err
	}
	branchCtx, branch := shared.BeginArchivalistStoreBranch(ctx, "stored task event", map[string]any{
		"event_type": event.Type,
		"task_id":    event.TaskID,
		"session_id": o.config.SessionID,
	})
	metadata := branch.ApplyMetadata(branchCtx, map[string]any{
		"event_type": event.Type,
		"event_data": event,
	})

	req := &guide.RouteRequest{
		Input:           input,
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		FireAndForget:   true,
		SessionID:       o.config.SessionID,
		Timestamp:       time.Now(),
		Metadata:        metadata,
	}
	if req.ParentCorrelationID == "" {
		if stream, ok := shared.StreamMetadataFromContext(branchCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = metadata

	if err := o.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		branch.Complete(branchCtx, "", "", err)
		return err
	}
	branch.Complete(branchCtx, "stored task event", "", nil)
	return nil
}

// QueryArchivalistForFailures queries Archivalist for failure patterns
func (o *Orchestrator) QueryArchivalistForFailures(ctx context.Context, query FailureQuery) ([]FailurePattern, error) {
	// In a full implementation, this would make an async request to Archivalist
	// and await the response. For now, return empty slice.
	return []FailurePattern{}, nil
}

// PushStatusUpdate adds a status update to the buffer
func (o *Orchestrator) PushStatusUpdate(update *StatusUpdate) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.state.UpdateBuffer.Add(update) {
		o.flushUpdateBufferAsync()
	}
}

const flushUpdateBufferTimeout = 10 * time.Second

// flushUpdateBufferAsync dispatches flushUpdateBuffer via the GoroutineScope.
func (o *Orchestrator) flushUpdateBufferAsync() {
	if o.scope == nil {
		return
	}
	o.scope.Go("flush-update-buffer", flushUpdateBufferTimeout, func(ctx context.Context) error {
		o.flushUpdateBuffer()
		return nil
	})
}

func (o *Orchestrator) flushUpdateBuffer() {
	o.mu.Lock()
	updates := o.state.UpdateBuffer.Flush()
	o.mu.Unlock()

	if len(updates) == 0 {
		return
	}

	for _, update := range updates {
		o.processStatusUpdate(update)
	}
}

func (o *Orchestrator) processStatusUpdate(update *StatusUpdate) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if task, ok := o.state.Tasks[update.TaskID]; ok {
		task.Status = update.Status
		if update.Status.IsTerminal() {
			now := time.Now()
			task.CompletedAt = &now
			o.submitTaskEventAsync(task)
		}
	}
}

// GetSummary generates an orchestrator summary
func (o *Orchestrator) GetSummary(ctx context.Context) (*OrchestratorSummary, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	summary := &OrchestratorSummary{
		ID:          generateMessageID(),
		GeneratedAt: time.Now(),
		SessionID:   o.config.SessionID,
		Overview:    o.generateOverview(),
		Workflows:   o.summarizeWorkflows(),
		Tasks:       o.summarizeTasks(),
		Health:      o.healthMonitor.GetSummary(),
	}

	summary.TokenEstimate = estimateTokens(summary.Overview)
	o.state.Stats.SummariesCreated++

	return summary, nil
}

// GetDAGSnapshots returns point-in-time DAG execution snapshots.
// Returns nil when no DAG bridge is configured.
func (o *Orchestrator) GetDAGSnapshots(limit int) []DAGSnap {
	if o.dagBridge == nil {
		return nil
	}
	return o.dagBridge.DAGSnapshots(limit)
}

func (o *Orchestrator) generateOverview() string {
	return fmt.Sprintf(
		"Orchestrator monitoring %d workflows with %d active tasks. "+
			"%d tasks completed, %d failed. %d events submitted to Archivalist.",
		o.state.Stats.ActiveWorkflows,
		len(o.state.Tasks),
		o.state.Stats.CompletedTasks,
		o.state.Stats.FailedTasks,
		o.state.Stats.EventsSubmitted,
	)
}

func (o *Orchestrator) summarizeWorkflows() WorkflowsSummary {
	summary := WorkflowsSummary{}

	for _, wf := range o.state.Workflows {
		summary.Total++
		switch wf.Status {
		case WorkflowStatusRunning:
			summary.Running++
			summary.ActiveWorkflows = append(summary.ActiveWorkflows, WorkflowBrief{
				ID:       wf.ID,
				Name:     wf.Name,
				Status:   wf.Status,
				Progress: wf.Progress,
				Phase:    wf.Phase,
			})
		case WorkflowStatusCompleted:
			summary.Completed++
		case WorkflowStatusFailed:
			summary.Failed++
		case WorkflowStatusPaused:
			summary.Paused++
		}
	}

	return summary
}

func (o *Orchestrator) summarizeTasks() TasksSummary {
	summary := TasksSummary{}

	for _, task := range o.state.Tasks {
		summary.Total++
		switch task.Status {
		case TaskStatusPending, TaskStatusQueued:
			summary.Pending++
		case TaskStatusRunning:
			summary.Running++
		case TaskStatusCompleted:
			summary.Completed++
		case TaskStatusFailed:
			summary.Failed++
			summary.RecentFailures = append(summary.RecentFailures, TaskBrief{
				ID:      task.ID,
				Name:    task.Name,
				Status:  task.Status,
				AgentID: task.AssignedAgentID,
				Error:   task.Error,
			})
		case TaskStatusTimedOut:
			summary.TimedOut++
		}
	}

	if len(summary.RecentFailures) > 5 {
		summary.RecentFailures = summary.RecentFailures[:5]
	}

	return summary
}

// GetRoutingInfo returns routing info for Guide registration
func (o *Orchestrator) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      o.config.AgentID,
		Type:    "orchestrator",
		Name:    "Orchestrator",
		Aliases: []string{"orch"},
		Registration: &guide.AgentRegistration{
			ID:          o.config.AgentID,
			Name:        "orchestrator",
			Aliases:     []string{"orch"},
			Description: "Workflow observer and coordinator. Monitors task health, submits events to Archivalist.",
			Priority:    80,
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{guide.IntentChat, guide.IntentStatus, guide.IntentRecall, guide.IntentHelp},
				Domains: []guide.Domain{guide.DomainTasks, "workflow", "health"},
			},
			RuntimeProfiles:       orchestratorRuntimeProfiles(),
			DefaultRuntimeProfile: orchestratorDefaultRuntimeProfile(),
		},
		ActionShortcuts: []guide.ActionShortcut{
			{Name: "status", DefaultIntent: guide.IntentStatus, DefaultDomain: "workflow"},
			{Name: "tasks", DefaultIntent: guide.IntentStatus, DefaultDomain: guide.DomainTasks},
			{Name: "help", DefaultIntent: guide.IntentHelp, DefaultDomain: guide.DomainSystem},
			{Name: "chat", DefaultIntent: guide.IntentChat, DefaultDomain: guide.DomainSystem},
		},
	}
}

// Skills returns the skill registry
func (o *Orchestrator) Skills() *skills.Registry {
	return o.skills
}

// IsRunning returns true if the orchestrator is running
func (o *Orchestrator) IsRunning() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.running
}

// State returns the current state (read-only)
func (o *Orchestrator) State() *State {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.state
}

// publishActivity sends a user-visible activity event to the UI agent panel.
func (o *Orchestrator) publishActivity(eventType events.EventType, content string) {
	o.publishActivityWithVisibility(eventType, events.VisibilityUser, content)
}

// publishPipelineAgentActivity publishes an activity event for a pipeline
// agent so the TUI's ensureAgent creates a pipeline-scoped panel entry.
func (o *Orchestrator) publishPipelineAgentActivity(agentType, pipelineID, nodeID, taskSlug, pipelineStatus string) {
	if o.activityPub == nil || strings.TrimSpace(pipelineID) == "" || !isPipelinePanelAgentType(agentType) {
		return
	}
	panelAgentID := pipelinePanelAgentID(pipelineID, agentType)
	if !o.recordPipelinePanelActivity(panelAgentID, pipelinePanelSnapshot{
		EventType:      events.EventTypeAgentAction,
		NodeID:         nodeID,
		PipelineStatus: pipelineStatus,
	}) {
		return
	}
	displayName := PipelineAgentDisplayNames[agentType]
	if displayName == "" {
		displayName = agentType
	}
	evt := events.NewActivityEvent(events.EventTypeAgentAction, o.config.SessionID,
		fmt.Sprintf("Processing pipeline task: %s", nodeID))
	evt.AgentID = panelAgentID
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = agentType
	evt.Data["agent_name"] = displayName
	evt.Data["pipeline_id"] = pipelineID
	evt.Data["node_id"] = nodeID
	evt.Data["task_id"] = pipelineID
	if taskSlug != "" {
		evt.Data["task_slug"] = taskSlug
	}
	if pipelineStatus != "" {
		evt.Data["pipeline_status"] = pipelineStatus
	}
	o.activityPub.PublishActivity(evt)
}

// publishPipelineAgentRegistration publishes a registration event for a
// pipeline agent that is present but not yet dispatched. Uses
// EventTypeAgentRegistered so the TUI shows the agent as waiting (not active).
func (o *Orchestrator) publishPipelineAgentRegistration(agentType, pipelineID, taskSlug, pipelineStatus string) {
	if o.activityPub == nil || strings.TrimSpace(pipelineID) == "" || !isPipelinePanelAgentType(agentType) {
		return
	}
	panelAgentID := pipelinePanelAgentID(pipelineID, agentType)
	if !o.recordPipelinePanelRegistration(panelAgentID, pipelineStatus) {
		return
	}
	displayName := PipelineAgentDisplayNames[agentType]
	if displayName == "" {
		displayName = agentType
	}
	evt := events.NewActivityEvent(events.EventTypeAgentRegistered, o.config.SessionID,
		fmt.Sprintf("Pipeline agent registered: %s", agentType))
	evt.AgentID = panelAgentID
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = agentType
	evt.Data["agent_name"] = displayName
	evt.Data["pipeline_id"] = pipelineID
	evt.Data["task_id"] = pipelineID
	if taskSlug != "" {
		evt.Data["task_slug"] = taskSlug
	}
	if pipelineStatus != "" {
		evt.Data["pipeline_status"] = pipelineStatus
	}
	o.activityPub.PublishActivity(evt)
}

func pipelinePanelAgentID(pipelineID, agentType string) string {
	if pipelineID == "" {
		return agentType
	}
	return pipelineID + ":" + agentType
}

func (o *Orchestrator) recordPipelinePanelActivity(agentID string, next pipelinePanelSnapshot) bool {
	if agentID == "" {
		return true
	}
	o.pipelinePanelMu.Lock()
	defer o.pipelinePanelMu.Unlock()
	if prev, ok := o.pipelinePanelState[agentID]; ok && prev == next {
		return false
	}
	o.pipelinePanelState[agentID] = next
	return true
}

func (o *Orchestrator) recordPipelinePanelRegistration(agentID, pipelineStatus string) bool {
	if agentID == "" {
		return true
	}
	o.pipelinePanelMu.Lock()
	defer o.pipelinePanelMu.Unlock()
	if _, registered := o.pipelinePanelRegistered[agentID]; registered {
		return false
	}
	o.pipelinePanelRegistered[agentID] = struct{}{}
	o.pipelinePanelState[agentID] = pipelinePanelSnapshot{
		EventType:      events.EventTypeAgentRegistered,
		PipelineStatus: pipelineStatus,
	}
	return true
}

// publishActivityWithVisibility sends an activity event with explicit visibility.
func (o *Orchestrator) publishActivityWithVisibility(eventType events.EventType, visibility events.EventVisibility, content string) {
	if o.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, o.config.SessionID, content)
	evt.AgentID = o.config.AgentID
	evt.Visibility = visibility
	evt.Data["agent_type"] = "orchestrator"
	evt.Data["agent_name"] = "Orchestrator"
	o.activityPub.PublishActivity(evt)
}

// retryObserver returns a provider RetryObserver that publishes retry status
// via the activity event bus, giving the UI visibility into backoff waits.
func (o *Orchestrator) retryObserver() providers.RetryObserver {
	return func(event providers.RetryEvent) {
		detail := providers.FriendlyErrorMessage(event.Err)
		if detail == "" {
			detail = "Transient provider error"
		}
		o.publishActivity(events.EventTypeAgentError,
			fmt.Sprintf("%s — retrying (%d/%d) after %s",
				detail, event.Attempt, event.MaxAttempts, event.Delay.Round(time.Second)))
	}
}

// onHealthCheckResult is the HealthMonitor callback. It fires outside m.mu
// and does NOT acquire o.mu, preventing deadlocks. It only touches:
// - healthCache (Ristretto, internally lock-free)
// - bus.Publish (has its own internal locking)
// - doEscalateToArchitect (publishes to bus, no o.mu)
func (o *Orchestrator) onHealthCheckResult(result *HealthCheckResult) {
	// 1. Cache in Ristretto for fast skill retrieval.
	o.healthCache.SetLatest(result)
	for i := range result.AgentResults {
		ar := &result.AgentResults[i]
		o.healthCache.SetAgent(ar.AgentID, ar)
	}

	// 2. Forward to Archivalist for history.
	o.forwardHealthToArchivalist(result)

	// 3. Auto-escalate critical transitions.
	o.escalateCriticalHealth(result)
}

// forwardHealthToArchivalist sends the health check result to Archivalist for
// historical storage. Fire-and-forget, same pattern as SubmitEventToArchivalist.
func (o *Orchestrator) forwardHealthToArchivalist(result *HealthCheckResult) {
	if o.bus == nil || !o.config.ArchivalistEnabled {
		return
	}
	input, err := guide.ArchivalistStoreRouteInput("store health check result")
	if err != nil {
		return
	}

	req := &guide.RouteRequest{
		Input:           input,
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		FireAndForget:   true,
		SessionID:       o.config.SessionID,
		Timestamp:       time.Now(),
	}

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = map[string]any{
		"event_type": "health_check_result",
		"event_data": result,
	}

	o.bus.Publish(guide.TopicGuideRequests, msg)
}

// escalateCriticalHealth deterministically escalates agents that transitioned
// to critical level. Only fires on the transition (PreviousLevel != Critical)
// to prevent re-escalation every check cycle.
func (o *Orchestrator) escalateCriticalHealth(result *HealthCheckResult) {
	for i := range result.AgentResults {
		ar := &result.AgentResults[i]
		if ar.Level != HealthLevelCritical {
			continue
		}
		if ar.PreviousLevel == HealthLevelCritical {
			continue
		}
		o.doEscalateToArchitect(
			fmt.Sprintf("Agent %q transitioned to critical health", ar.AgentID),
			"critical",
			fmt.Sprintf("Error rate: %.2f, missed heartbeats: %d, active alerts: %d",
				ar.ErrorRate, ar.MissedHeartbeats, ar.ActiveAlertCount),
		)
	}
}

func generateMessageID() string {
	return fmt.Sprintf("msg_%s", uuid.New().String()[:8])
}

func estimateTokens(s string) int {
	return len(s) / 4
}

// --- Pipeline and DAG topic subscriptions ---

func (o *Orchestrator) subscribePipelineTopics() {
	topics := []struct {
		topic   string
		handler guide.MessageHandler
	}{
		{"pipeline.update.*", o.handlePipelineUpdate},
		{"pipeline.state.*", o.handlePipelineState},
		{"pipeline.query.response.orchestrator", o.handlePipelineQueryResponse},
	}
	for _, t := range topics {
		if sub, err := o.bus.SubscribeAsync(t.topic, t.handler); err == nil {
			o.pipelineSubs = append(o.pipelineSubs, sub)
		}
	}
}

func (o *Orchestrator) subscribeDAGTopics() {
	topics := []struct {
		topic   string
		handler guide.MessageHandler
	}{
		{"dag.execute", o.handleDAGExecuteRequest},
		{"dag.modify", o.handleDAGModifyRequest},
		{"dag.cancel", o.handleDAGCancelRequest},
		{"dag.decision.response", o.handleLayerDecisionResponse},
	}
	for _, t := range topics {
		if sub, err := o.bus.SubscribeAsync(t.topic, t.handler); err == nil {
			o.dagSubs = append(o.dagSubs, sub)
		}
	}
}

func (o *Orchestrator) handleLayerDecisionResponse(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	dagID, _ := data["dag_id"].(string)
	decisionStr, _ := data["decision"].(string)

	if dagID == "" || o.dagBridge == nil {
		return nil
	}

	decision := parseDecisionKind(decisionStr)
	if decision == dag.DecisionRetry {
		o.dagBridge.ResetPendingTaskPods(dagID)
	}
	o.dagBridge.ResolveDecision(dagID, decision)
	return nil
}

func parseDecisionKind(s string) dag.DecisionKind {
	switch s {
	case "retry":
		return dag.DecisionRetry
	case "skip":
		return dag.DecisionSkip
	case "abort":
		return dag.DecisionAbort
	default:
		return dag.DecisionAbort
	}
}

func (o *Orchestrator) handlePipelineUpdate(msg *guide.Message) error {
	update, ok := msg.Payload.(*PipelineUpdate)
	if !ok {
		// Try map extraction for deserialized payloads
		data, ok := msg.Payload.(map[string]any)
		if !ok {
			return nil
		}
		update = extractPipelineUpdate(data)
		if update == nil {
			return nil
		}
	}

	entry := TaskUpdateEntry{
		ID:        msg.ID,
		DAGID:     update.DAGID,
		TaskID:    update.TaskID,
		NodeID:    update.NodeID,
		AgentID:   update.AgentID,
		AgentType: update.AgentType,
		Status:    update.Status,
		Progress:  update.Progress,
		Message:   update.Message,
		Output:    update.Output,
		Error:     update.Error,
		Attempt:   update.Attempt,
		Timestamp: update.Timestamp,
	}
	o.bufferRegistry.Push(entry)
	o.recordPipelineDispatchActivity(update)

	stage := strings.TrimSpace(update.Stage)
	if stage == "" {
		stage = strings.TrimSpace(string(StageFromSubNodeID(update.NodeID)))
	}
	if update.TaskID != "" {
		if status := pipelineTaskStateForUpdate(update.Status, stage); status != "" {
			publishTaskPipelineState(o.bus, o.config.AgentID, update.TaskID, "", status, update.AgentType)
		}
	}

	if isTerminalStatus(update.Status) {
		o.finalizePipelineUpdate(update)
		result := convertPipelineToNodeResult(update)
		o.dagBridge.NotifyNodeComplete(update.NodeID, result)
		o.releaseAcceptedPipelineResources(update)
	}

	o.pushEvent(&busEvent{
		Topic:     "pipeline.update",
		Timestamp: update.Timestamp,
		Severity:  severityInfo,
		Summary:   fmt.Sprintf("Pipeline %s: %s (%s %.0f%%)", update.AgentType, update.Status, update.NodeID, update.Progress*100),
	})

	return nil
}

func (o *Orchestrator) releaseAcceptedPipelineResources(update *PipelineUpdate) {
	if o == nil || o.dagBridge == nil || update == nil {
		return
	}
	if strings.TrimSpace(update.Status) != "succeeded" {
		return
	}
	if strings.TrimSpace(update.AgentType) != shared.PipelineAgentInspector {
		return
	}
	o.dagBridge.ReleaseCompletedTaskResources(update.DAGID, update.TaskID)
}

func (o *Orchestrator) handlePipelineState(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	agentID, _ := data["agent_id"].(string)
	dagID, _ := data["dag_id"].(string)
	nodeID, _ := data["node_id"].(string)
	stateJSON, _ := data["state_json"].(string)

	if o.store != nil && agentID != "" {
		o.store.UpsertPipelineState(agentID, dagID, nodeID, stateJSON)
	}
	return nil
}

func (o *Orchestrator) handlePipelineQueryResponse(msg *guide.Message) error {
	resp, ok := msg.Payload.(*PipelineQueryResponse)
	if !ok {
		return nil
	}

	if o.store != nil {
		stateJSON, _ := json.Marshal(resp.State)
		o.store.UpsertPipelineState(resp.AgentID, "", "", string(stateJSON))
	}
	return nil
}

func (o *Orchestrator) handleDAGExecuteRequest(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	dagJSON, _ := data["dag_json"].(string)
	planID, _ := data["plan_id"].(string)

	if dagJSON == "" || planID == "" {
		return nil
	}

	d := &dag.DAG{}
	if err := d.UnmarshalJSON([]byte(dagJSON)); err != nil {
		o.publishActivity(events.EventTypeAgentError, "DAG execute: invalid dag_json: "+err.Error())
		return nil
	}

	dagID, err := o.dagBridge.Execute(o.runCtx, d, planID, o.config.SessionID)
	if err != nil {
		o.publishActivity(events.EventTypeAgentError, "DAG execution failed: "+err.Error())
		return nil
	}
	o.publishActivity(events.EventTypeSuccess, "DAG "+dagID+" started for plan "+planID)
	return nil
}

func (o *Orchestrator) handleDAGModifyRequest(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	dagID, _ := data["dag_id"].(string)
	modJSON, _ := data["modification_json"].(string)
	reason, _ := data["reason"].(string)

	var mod DAGModification
	if err := json.Unmarshal([]byte(modJSON), &mod); err != nil {
		return nil
	}
	mod.Reason = reason

	return o.dagBridge.Modify(dagID, &mod)
}

func (o *Orchestrator) handleDAGCancelRequest(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	dagID, _ := data["dag_id"].(string)
	reason, _ := data["reason"].(string)

	return o.dagBridge.Cancel(dagID, reason)
}

func (o *Orchestrator) startWALGC() {
	if o.scope == nil || o.journal == nil {
		return
	}

	// 7-day retention, 24h GC interval
	const walRetention = 7 * 24 * time.Hour
	const walGCInterval = 24 * time.Hour

	o.scope.Go("wal-gc", 0, func(ctx context.Context) error {
		// Run once at startup
		o.journal.GC(time.Now().Add(-walRetention))

		ticker := time.NewTicker(walGCInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				o.journal.GC(time.Now().Add(-walRetention))
			}
		}
	})
}

func extractPipelineUpdate(data map[string]any) *PipelineUpdate {
	u := &PipelineUpdate{}
	u.DAGID, _ = data["dag_id"].(string)
	u.NodeID, _ = data["node_id"].(string)
	u.TaskID, _ = data["task_id"].(string)
	u.AgentID, _ = data["agent_id"].(string)
	u.AgentType, _ = data["agent_type"].(string)
	u.Status, _ = data["status"].(string)
	u.Stage, _ = data["stage"].(string)
	if p, ok := data["progress"].(float64); ok {
		u.Progress = p
	} else if p, ok := data["progress"].(int); ok {
		u.Progress = float64(p)
	}
	u.Message, _ = data["message"].(string)
	u.Output = data["output"]
	u.Error, _ = data["error"].(string)
	if a, ok := data["attempt"].(float64); ok {
		u.Attempt = int(a)
	} else if a, ok := data["attempt"].(int); ok {
		u.Attempt = a
	}
	if ts, ok := data["timestamp"].(time.Time); ok {
		u.Timestamp = ts
	} else if raw, ok := data["timestamp"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			u.Timestamp = parsed
		}
	}
	if u.Timestamp.IsZero() {
		u.Timestamp = time.Now()
	}
	if u.Status == "" {
		return nil
	}
	return u
}

func convertTaskCompleteToNodeResult(task *TaskRecord) *dag.NodeResult {
	state := dag.NodeStateSucceeded
	if task.Status == TaskStatusFailed {
		state = dag.NodeStateFailed
	}
	return &dag.NodeResult{
		NodeID:  task.ID,
		State:   state,
		Output:  task.Result,
		EndTime: time.Now(),
	}
}

func convertTaskFailedToNodeResult(task *TaskRecord) *dag.NodeResult {
	resultErr := dagNodeError(firstNonEmpty(strings.TrimSpace(task.Error), "task failed without error details"))
	return &dag.NodeResult{
		NodeID:  task.ID,
		State:   dag.NodeStateFailed,
		Error:   resultErr,
		EndTime: time.Now(),
	}
}

func convertPipelineToNodeResult(update *PipelineUpdate) *dag.NodeResult {
	state := dag.NodeStateSucceeded
	var resultErr error
	if update.Status == "failed" || update.Status == "timed_out" {
		state = dag.NodeStateFailed
		resultErr = dagNodeError(firstNonEmpty(strings.TrimSpace(update.Error), strings.TrimSpace(update.Message), "pipeline task failed without error details"))
	} else if update.Status == "cancelled" {
		state = dag.NodeStateCancelled
	}

	return &dag.NodeResult{
		NodeID:  update.NodeID,
		State:   state,
		Output:  update.Output,
		Error:   resultErr,
		EndTime: update.Timestamp,
	}
}

func dagNodeError(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return fmt.Errorf("%s", text)
}

// =============================================================================
// HandoffInjectable Implementation
// =============================================================================

// SetHandoffBridge attaches the handoff bridge for context-aware lifecycle management.
func (o *Orchestrator) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.handoffBridge = bridge
	if bridge != nil {
		bridge.SetActivityPublisher(o.activityPub)
	}
}

// SetCanonicalID overwrites the orchestrator's internal ID so a replacement
// instance can assume the original routing identity after handoff.
func (o *Orchestrator) SetCanonicalID(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.config.AgentID = id
}

// SetAgentPod assigns the agent pod for cross-agent coordination.
func (o *Orchestrator) SetAgentPod(pod *shared.AgentPod) {
	o.agentPod = pod
}

// AgentID returns the orchestrator's agent identifier.
func (o *Orchestrator) AgentID() string {
	return o.config.AgentID
}

// AgentType returns the orchestrator's agent type classification.
func (o *Orchestrator) AgentType() string {
	return "orchestrator"
}

// Descriptor returns the agent descriptor for handoff decisions.
func (o *Orchestrator) Descriptor() handoff.AgentDescriptor {
	modelID := o.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "orchestrator",
		ModelID:               modelID,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryStandalone,
		RuntimeProfiles:       orchestratorRuntimeProfiles(),
		DefaultRuntimeProfile: orchestratorDefaultRuntimeProfile(),
	}
}

// ExtractArchivableState captures the orchestrator's current state for handoff persistence.
func (o *Orchestrator) ExtractArchivableState() *handoff.ArchivableState {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return &handoff.ArchivableState{
		AgentID:   o.config.AgentID,
		AgentType: "orchestrator",
		State: map[string]string{
			"session_id":       o.config.SessionID,
			"running":          fmt.Sprintf("%t", o.running),
			"active_workflows": fmt.Sprintf("%d", o.state.Stats.ActiveWorkflows),
			"completed_tasks":  fmt.Sprintf("%d", o.state.Stats.CompletedTasks),
			"failed_tasks":     fmt.Sprintf("%d", o.state.Stats.FailedTasks),
		},
		Timestamp: time.Now(),
	}
}

// Terminate gracefully shuts down the orchestrator, delegating to Stop.
func (o *Orchestrator) Terminate(_ context.Context) error {
	return o.Stop()
}

// InjectPreparedContext receives prepared context from a handoff.
// The orchestrator is a lightweight observer and does not require context
// injection beyond what Start() provides, so this is a no-op.
func (o *Orchestrator) InjectPreparedContext(_ *handoff.PreparedContext) error {
	return nil
}
