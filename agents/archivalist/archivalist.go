package archivalist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/memory"
	"github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// archivalistProvider is the minimal interface the Archivalist needs from its LLM.
// Satisfied by *providers.AnthropicProvider and *gateway.GatewayProvider.
type archivalistProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

const (
	defaultReplicaCount   = 4
	defaultReplicaBacklog = 16
)

type committedKnowledgeSearcher interface {
	Search(ctx context.Context, req *search.SearchRequest) (*knowledgeruntime.CommittedSearchResult, error)
}

// Archivalist is the main agent for managing AI-generated content and conversation memory
type Archivalist struct {
	id           string
	store        *Store
	archive      *Archive
	agentContext *AgentContext
	config       Config
	logger       *slog.Logger

	// LLM provider (replaces direct anthropic.Client)
	provider archivalistProvider
	client   *Client

	// Containerization
	runMu       sync.RWMutex
	runCtx      context.Context
	runCancel   context.CancelFunc
	steering    *shared.SteeringManager
	requestPool *shared.RequestReplicaPool
	activityPub events.ActivityPublisher

	// Request lifecycle
	requestMu      sync.Mutex
	requestCancels map[string]context.CancelFunc

	registry         *Registry
	eventLog         *EventLog
	conflictDetector *ConflictDetector
	conflictHistory  *ConflictHistory

	queryCache  *QueryCache
	embeddings  *EmbeddingStore
	retriever   *SemanticRetriever
	synthesizer *Synthesizer
	memory      *MemoryManager
	toolHandler *ToolHandler

	workIntentsMu sync.RWMutex
	workIntents   []*WorkIntent

	// Knowledge subsystems
	knowledgeStore    *knowledge.KnowledgeStore
	knowledgeBackend  committedKnowledgeSearcher
	queryCoordinator  *query.HybridQueryCoordinator
	memoryScorer      *memory.MemoryWeightedScorer
	hybridMemory      *memory.HybridQueryWithMemory
	crossAgentWeights *CrossAgentWeightManager
	spacingAnalyzer   *memory.SpacingAnalyzer

	bus           guide.EventBus
	channels      *guide.AgentChannels
	requestSub    guide.Subscription
	responseSub   guide.Subscription
	registrySub   guide.Subscription
	logIngestSub  guide.Subscription
	knowledgeSub  guide.Subscription
	running       bool
	knownAgentsMu sync.RWMutex
	knownAgents   map[string]*guide.AgentAnnouncement

	// Synchronous consultation bus
	pendingMu  sync.Mutex
	pendingBus map[string]chan *guide.Message

	skills        *skills.Registry
	skillLoader   *skills.Loader
	hooks         *skills.HookRegistry
	tools         *toolruntime.Runtime
	toolDefsDirty bool

	defaultSessionID string
	sessionStores    map[string]*SessionStore
	crossSession     *CrossSessionIndex
	workflowStore    *WorkflowStore

	// Handoff integration
	handoffBridge *handoff.HandoffBridge

	// Log ingest store: bounded per-agent ring buffers for cross-agent log querying.
	logIngest *LogIngestStore

	workspaceViews versioning.WorkspaceViewAccess
}

// Config holds configuration for the Archivalist agent
type Config struct {
	// Canonical agent ID. If empty, generates a UUID8.
	ID string

	// LLM provider wrapper — wraps an AnthropicProvider with gateway rate limiting.
	// If nil, LLM features (synthesis, summary generation) are disabled.
	ProviderWrapper func(*providers.AnthropicProvider) providers.ProviderAdapter

	// Model to use for LLM calls (default: claude-sonnet-4-6).
	Model string

	// System prompt and output configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Activity publisher for UI agent-panel updates. Nil-safe.
	ActivityPub events.ActivityPublisher

	// RequestGuard is called at handler entry to prevent activation demotion
	// during in-flight processing. Returns a release function. Nil-safe.
	RequestGuard func() func()

	// Logging
	Logger *slog.Logger

	// Storage configuration
	ArchivePath    string // Path to SQLite archive, defaults to .sylk/archive.db
	TokenThreshold int    // Token threshold for archiving, defaults to 750K

	// Feature flags
	EnableLLM         bool // Enable LLM-driven conversation path (tool loop)
	EnableArchive     bool // Enable SQLite archive (L2 storage)
	EnableRAG         bool // Enable RAG components (query cache, embeddings, synthesis)
	EnableACTR        bool // Enable ACT-R memory scoring
	EnableHybridQuery bool // Enable HybridQueryCoordinator

	// Forwarded-request admission controls for bounded knowledge replicas.
	MaxConcurrentForwarded int
	MaxQueuedForwarded     int
	ContextQuota           *container.ResourceQuota
	EnableKnowledgeGraph   bool // Enable knowledge graph traversal

	// Concurrency configuration
	MaxEvents           int           // Max events in event log (default: 10000)
	IdleTimeout         time.Duration // Time before agent marked idle (default: 5m)
	InactiveTimeout     time.Duration // Time before agent marked inactive (default: 30m)
	ConflictHistorySize int           // Max conflicts to track (default: 100)

	// RAG configuration
	EmbeddingsPath      string  // Path to embeddings database
	QueryCacheSize      int     // Max cached queries (default: 10000)
	SimilarityThreshold float64 // Query similarity threshold (default: 0.95)

	// WorkspaceViews provides explicit disk/global/pipeline read access for
	// session-memory grounding.
	WorkspaceViews versioning.WorkspaceViewAccess
}

// New creates a new Archivalist agent with provider-based LLM.
func New(ctx context.Context, cfg Config) (*Archivalist, error) {
	cfg = applyConfigDefaults(cfg)
	components, err := createComponents(cfg)
	if err != nil {
		return nil, err
	}
	archivalist := assembleArchivalist(cfg, components)
	if err := archivalist.initToolRuntime(); err != nil {
		return nil, err
	}
	if cfg.EnableRAG {
		if err := archivalist.initRAG(cfg); err != nil {
			return nil, fmt.Errorf("failed to initialize RAG: %w", err)
		}
	}
	return archivalist, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	cfg.SystemPrompt = shared.AppendNoFilesystemContext(cfg.SystemPrompt)
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.TokenThreshold == 0 {
		cfg.TokenThreshold = DefaultTokenThreshold
	}
	if cfg.MaxEvents == 0 {
		cfg.MaxEvents = 10000
	}
	if cfg.ConflictHistorySize == 0 {
		cfg.ConflictHistorySize = 100
	}
	if cfg.Model == "" {
		cfg.Model = ModelSonnet45
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

// archivalistComponents holds intermediate components for assembly
type archivalistComponents struct {
	archive          *Archive
	store            *Store
	agentContext     *AgentContext
	registry         *Registry
	eventLog         *EventLog
	conflictDetector *ConflictDetector
	conflictHistory  *ConflictHistory
}

func createComponents(cfg Config) (*archivalistComponents, error) {
	archive, err := createArchive(cfg)
	if err != nil {
		return nil, err
	}

	agentContext := NewAgentContext()
	registry := NewRegistry(RegistryConfig{
		IdleTimeout:     cfg.IdleTimeout,
		InactiveTimeout: cfg.InactiveTimeout,
	})

	return &archivalistComponents{
		archive:          archive,
		store:            NewStore(StoreConfig{TokenThreshold: cfg.TokenThreshold, Archive: archive}),
		agentContext:     agentContext,
		registry:         registry,
		eventLog:         NewEventLog(EventLogConfig{MaxEvents: cfg.MaxEvents}),
		conflictDetector: NewConflictDetector(agentContext, registry),
		conflictHistory:  NewConflictHistory(cfg.ConflictHistorySize),
	}, nil
}

func createArchive(cfg Config) (*Archive, error) {
	if !cfg.EnableArchive {
		return nil, nil
	}
	archive, err := NewArchive(ArchiveConfig{Path: cfg.ArchivePath})
	if err != nil {
		return nil, fmt.Errorf("failed to create archive: %w", err)
	}
	return archive, nil
}

func assembleArchivalist(cfg Config, c *archivalistComponents) *Archivalist {
	agentID := cfg.ID
	if agentID == "" {
		agentID = uuid.New().String()[:8]
	}

	skillsRegistry := skills.NewRegistry()
	hookRegistry := skills.NewHookRegistry()

	a := &Archivalist{
		id:               agentID,
		store:            c.store,
		archive:          c.archive,
		agentContext:     c.agentContext,
		config:           cfg,
		logger:           cfg.Logger,
		activityPub:      cfg.ActivityPub,
		registry:         c.registry,
		eventLog:         c.eventLog,
		conflictDetector: c.conflictDetector,
		conflictHistory:  c.conflictHistory,
		knownAgents:      make(map[string]*guide.AgentAnnouncement),
		pendingBus:       make(map[string]chan *guide.Message),
		requestCancels:   make(map[string]context.CancelFunc),
		skills:           skillsRegistry,
		hooks:            hookRegistry,
		steering:         shared.NewSteeringManager(),
		workIntents:      make([]*WorkIntent, 0),
		workspaceViews:   authority.RestrictWorkspaceViews("archivalist", cfg.WorkspaceViews),
	}
	a.requestPool = shared.NewAutoscalingRequestReplicaPool(shared.RequestReplicaPoolConfig{
		Name:              "archivalist",
		HardMaxActive:     cfg.MaxConcurrentForwarded,
		HardMaxQueued:     cfg.MaxQueuedForwarded,
		CurrentModel:      a.CurrentModel,
		ProviderTelemetry: a.currentProviderAutoscaleSnapshot,
		ContextQuota:      cfg.ContextQuota,
	})

	a.steering.InitLazy("archivalist", cfg.ActivityPub)

	a.defaultSessionID = c.store.GetCurrentSession().ID
	a.sessionStores = make(map[string]*SessionStore)
	a.crossSession = NewCrossSessionIndex(c.store, c.archive, c.eventLog)
	a.workflowStore = NewWorkflowStore(a)
	a.logIngest = NewLogIngestStore()
	a.crossAgentWeights = NewCrossAgentWeightManager()

	// Register skills BEFORE creating the loader — NewLoader calls
	// loadCoreSkills() immediately and skills must already exist.
	a.registerCoreSkills()
	a.registerExtendedSkills()

	skillsLoaderCfg := skills.DefaultLoaderConfig()
	skillsLoaderCfg.CoreSkills = archivalistVisibleSkillNames()
	skillsLoaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	a.skillLoader = skills.NewLoader(skillsRegistry, skillsLoaderCfg)

	return a
}

func (a *Archivalist) initToolRuntime() error {
	tools, err := toolruntime.New(toolruntime.Config{
		Registry: a.skills,
		Hooks:    a.hooks,
		Manifest: archivalistToolManifest(a.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize archivalist tool runtime: %w", err)
	}
	a.tools = tools
	a.tools.SyncActiveFromLoaded()
	return nil
}

// initRAG initializes RAG components
func (a *Archivalist) initRAG(cfg Config) error {
	// Create embedder (mock for now, can be replaced with real embedder)
	embedder := NewMockEmbedder(1536)

	// Create query cache
	cacheSize := cfg.QueryCacheSize
	if cacheSize == 0 {
		cacheSize = 10000
	}
	threshold := cfg.SimilarityThreshold
	if threshold == 0 {
		threshold = 0.95
	}
	a.queryCache = NewQueryCache(QueryCacheConfig{
		HitThreshold:  threshold,
		MaxQueries:    cacheSize,
		UseEmbeddings: true,
	}, embedder)

	// Create embedding store
	embStore, err := NewEmbeddingStore(EmbeddingStoreConfig{
		DBPath:      cfg.EmbeddingsPath,
		MaxInMemory: 10000,
		Dimension:   1536,
	})
	if err != nil {
		return fmt.Errorf("failed to create embedding store: %w", err)
	}
	a.embeddings = embStore

	// Create memory manager
	a.memory = NewMemoryManager(DefaultTokenBudget())

	// Wire HybridQueryCoordinator if enabled.
	// When a KnowledgeStore is set, its coordinator is already wired via
	// SetKnowledgeStore — skip building a local one.
	if cfg.EnableHybridQuery && a.knowledgeStore == nil {
		a.queryCoordinator = buildQueryCoordinator(QueryCoordinatorConfig{
			EmbeddingStore: a.embeddings,
		})
	}

	// Wire ACT-R memory scoring if enabled
	if cfg.EnableACTR {
		mi, err := buildMemoryIntegration(MemoryIntegrationConfig{
			ArchivePath: cfg.ArchivePath,
		})
		if err != nil {
			return fmt.Errorf("failed to create memory integration: %w", err)
		}
		a.memoryScorer = mi.scorer
		a.hybridMemory = mi.hybrid
		a.spacingAnalyzer = memory.NewSpacingAnalyzer(memory.DefaultTargetRetention)
	}

	// Create semantic retriever
	retriever, err := NewSemanticRetriever(SemanticRetrieverConfig{
		DBPath:       cfg.ArchivePath,
		Embeddings:   a.embeddings,
		Embedder:     embedder,
		AgentContext: a.agentContext,
	})
	if err != nil {
		return fmt.Errorf("failed to create retriever: %w", err)
	}
	a.retriever = retriever

	// Create synthesizer with provider-based LLM
	a.synthesizer = NewSynthesizer(SynthesizerConfig{
		Provider:   a.provider,
		Model:      cfg.Model,
		Retriever:  a.retriever,
		QueryCache: a.queryCache,
	})

	// Create tool handler
	a.toolHandler = NewToolHandler(a, a.synthesizer)

	return nil
}

// Close closes the archivalist and its resources
func (a *Archivalist) Close() error {
	if a.tools != nil {
		a.tools.Close()
		a.tools = nil
	}
	// Stop event bus subscriptions first
	a.Stop()
	if a.requestPool != nil {
		a.requestPool.Close()
	}

	// Close knowledge subsystems
	if a.queryCoordinator != nil {
		a.queryCoordinator.Close()
	}
	if a.memoryScorer != nil {
		a.memoryScorer.Stop()
	}

	// Close event log to flush any pending writes
	if a.eventLog != nil {
		a.eventLog.Close()
	}

	// Close RAG components
	if a.embeddings != nil {
		a.embeddings.Close()
	}
	if a.retriever != nil {
		a.retriever.Close()
	}

	// Close archive
	if a.archive != nil {
		return a.archive.Close()
	}
	return nil
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The archivalist subscribes to its own channels and the registry topic.
func (a *Archivalist) Start(bus guide.EventBus) error {
	if a.running {
		return fmt.Errorf("archivalist is already running")
	}

	a.bus = bus
	a.channels = guide.NewAgentChannels("archivalist", a.id)
	a.runCtx, a.runCancel = context.WithCancel(context.Background())

	// Subscribe to own request channel (archivalist.requests)
	var err error
	a.requestSub, err = bus.SubscribeAsync(a.channels.Requests, a.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	a.responseSub, err = bus.SubscribeAsync(a.channels.Responses, a.handleBusResponse)
	if err != nil {
		a.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	a.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, a.handleRegistryAnnouncement)
	if err != nil {
		a.requestSub.Unsubscribe()
		a.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	// Subscribe to log ingest topic for cross-agent log aggregation.
	if a.logIngest != nil {
		a.logIngestSub, _ = bus.SubscribeAsync(guide.TopicLogIngest, a.handleLogIngest)
	}

	// Subscribe to knowledge readiness for telemetry and cache warming.
	a.knowledgeSub, _ = bus.SubscribeAsync(guide.TopicKnowledgeReady, a.handleKnowledgeReady)

	a.running = true
	a.logger.Info("archivalist started", "id", a.id, "channels", a.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (a *Archivalist) Stop() error {
	if !a.running {
		return nil
	}

	a.steering.CloseAll()
	if a.runCancel != nil {
		a.runCancel()
	}

	errs := a.unsubscribeAll()
	a.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	a.logger.Info("archivalist stopped", "id", a.id)
	return nil
}

func (a *Archivalist) unsubscribeAll() []error {
	var errs []error
	if err := a.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := a.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := a.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	if a.logIngestSub != nil {
		if err := a.logIngestSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
		a.logIngestSub = nil
	}
	if a.knowledgeSub != nil {
		if err := a.knowledgeSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
		a.knowledgeSub = nil
	}
	return errs
}

func (a *Archivalist) unsubscribeRequest() error {
	if a.requestSub == nil {
		return nil
	}
	err := a.requestSub.Unsubscribe()
	a.requestSub = nil
	return err
}

func (a *Archivalist) unsubscribeResponse() error {
	if a.responseSub == nil {
		return nil
	}
	err := a.responseSub.Unsubscribe()
	a.responseSub = nil
	return err
}

func (a *Archivalist) unsubscribeRegistry() error {
	if a.registrySub == nil {
		return nil
	}
	err := a.registrySub.Unsubscribe()
	a.registrySub = nil
	return err
}

// handleKnowledgeReady is called when a knowledge layer is promoted.
// The coordinator already has the new searchers set atomically — this
// handler is for awareness and telemetry side-effects only.
func (a *Archivalist) handleKnowledgeReady(msg *guide.Message) error {
	payload, ok := msg.GetKnowledgeReadyPayload()
	if !ok {
		return nil
	}
	a.logger.Info("knowledge layer promoted",
		"level", payload.Level,
		"searchers", payload.Searchers,
	)
	a.publishSystemEvent(events.EventTypeAgentAction,
		fmt.Sprintf("knowledge ready: searchers %v", payload.Searchers))
	return nil
}

// IsRunning returns true if the archivalist is actively processing bus messages
func (a *Archivalist) IsRunning() bool {
	return a.running
}

// Bus returns the event bus used by the archivalist
func (a *Archivalist) Bus() guide.EventBus {
	return a.bus
}

// Channels returns the archivalist's channel configuration
func (a *Archivalist) Channels() *guide.AgentChannels {
	return a.channels
}

// handleLogIngest processes log entries broadcast by agents for cross-agent aggregation.
func (a *Archivalist) handleLogIngest(msg *guide.Message) error {
	if a.logIngest == nil {
		return nil
	}
	entry, ok := msg.Payload.(agentlog.JSONLEntry)
	if !ok {
		return nil
	}
	a.logIngest.Ingest(entry)
	return nil
}

// handleBusRequest processes incoming forwarded requests from the event bus
func (a *Archivalist) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		return a.handleActionMessage(msg)
	}
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	a.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(a.steering.EventLogger(), fwd, a.id)
	shared.EmitDispatchACK(a.bus, fwd.Metadata, a.id, "archivalist", fwd.CorrelationID)
	if archivalistPublishesUserActivity(fwd) {
		a.publishActivity(events.EventTypeAgentAction, "Processing archivalist request")
	} else {
		a.publishSystemEvent(events.EventTypeAgentAction, "Processing archivalist request")
	}

	if a.config.RequestGuard != nil {
		release := a.config.RequestGuard()
		defer release()
	}

	// Request-scoped cancellable context.
	reqCtx, cancel := context.WithCancel(a.runCtx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	a.registerRequestCancel(fwd.CorrelationID, cancel)
	a.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer a.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	// Create steering ledger for this request.
	ledger := a.steering.Create(fwd.CorrelationID, a.id, fwd.SessionID, nil, nil)
	defer a.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	startTime := time.Now()

	emitter := shared.NewToolCallEmitter(a.bus, a.channels, a.id, fwd.CorrelationID, fwd.SourceAgentID)
	ctx := shared.WithToolCallEmitter(reqCtx, emitter)
	ctx = shared.WithForwardedStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, fwd.Metadata)
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: a.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     a.id,
		SessionID:   fwd.SessionID,
	})
	gov := shared.NewContextGovernor(a.CurrentModel(), a.config.MaxOutputTokens, 0)
	if a.handoffBridge != nil {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			return a.handoffBridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = shared.WithContextGovernor(ctx, gov)
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus: a.bus, Channels: a.channels,
		AgentID: a.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	publishStreamLifecycle := guide.ShouldPublishForwardedStreamLifecycle(fwd)
	streamStarted := false
	var stopQueueKeepalive func()
	lease, acquireErr := a.requestPool.Acquire(reqCtx, shared.RequestReplicaAcquireOptions{
		SourceKey:         shared.RequestReplicaSourceKeyForForwardedRequest(fwd),
		Priority:          shared.RequestReplicaPriorityForForwardedRequest(fwd),
		PromptBytes:       shared.RequestReplicaPromptBytesForForwardedRequest(fwd),
		ContextFieldCount: shared.RequestReplicaContextFieldCount(fwd),
		OnQueued: func(snapshot shared.RequestReplicaPoolSnapshot, queuePosition int) {
			if publishStreamLifecycle && !streamStarted {
				shared.PublishStreamStart(a.bus, a.channels, ctx, a.id)
				streamStarted = true
			}
			a.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentAction, "Waiting for an available archival replica", snapshot)
			if pp := shared.ProgressPublisherFromContext(ctx); pp != nil {
				pp.PublishState(events.AgentUIStateSearching, shared.KnowledgeQueueProgressMessage("archivalist", snapshot, queuePosition))
			}
			stopQueueKeepalive = a.startQueueKeepalive(ctx, queuePosition)
		},
		OnGranted: func(snapshot shared.RequestReplicaPoolSnapshot, _ bool) {
			if stopQueueKeepalive != nil {
				stopQueueKeepalive()
				stopQueueKeepalive = nil
			}
			if publishStreamLifecycle && !streamStarted {
				shared.PublishStreamStart(a.bus, a.channels, ctx, a.id)
				streamStarted = true
			}
			a.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentAction, "Processing archivalist request", snapshot)
		},
	})
	if stopQueueKeepalive != nil {
		defer stopQueueKeepalive()
	}
	if acquireErr != nil {
		if errors.Is(acquireErr, context.Canceled) || errors.Is(acquireErr, context.DeadlineExceeded) {
			return nil
		}
		if fwd.FireAndForget {
			return nil
		}
		var busyErr *shared.AgentBusyError
		if errors.As(acquireErr, &busyErr) {
			resp := &guide.RouteResponse{
				CorrelationID:       fwd.CorrelationID,
				Success:             false,
				Data:                shared.BusyRouteResponseData("archivalist", busyErr.Snapshot, busyErr.Error()),
				Error:               busyErr.Error(),
				RespondingAgentID:   a.id,
				RespondingAgentName: "Archivalist",
			}
			return a.bus.Publish(a.channels.Responses, guide.NewResponseMessage(a.generateMessageID(), resp))
		}
		return acquireErr
	}

	bundle, err := a.newForwardedToolBundle()
	if err != nil {
		lease.Release()
		return err
	}
	defer bundle.Close()

	result, err := a.processForwardedRequestWithBundle(ctx, fwd, bundle)
	snapshot := lease.ReleaseWithObservation(shared.RequestReplicaObservation{
		Duration:    time.Since(startTime),
		TotalTokens: shared.TotalUsageTokens(usageAcc.Total()),
		Successful:  err == nil,
	})
	shared.LogResponse(a.steering.EventLogger(), fwd.CorrelationID, a.id, fwd.SessionID, time.Since(startTime), err)

	if err != nil {
		if publishStreamLifecycle {
			shared.PublishStreamError(a.bus, a.channels, ctx, a.id, err)
			shared.PublishStreamComplete(a.bus, a.channels, ctx, a.id, "", usageAcc.Total())
		}
		if fwd.FireAndForget {
			return nil
		}
		a.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentError, fmt.Sprintf("Request failed: %s", err.Error()), snapshot)
		errMsg := guide.NewErrorMessage(a.generateMessageID(), fwd.CorrelationID, a.id, err.Error())
		return a.bus.Publish(a.channels.Errors, errMsg)
	}

	if publishStreamLifecycle {
		shared.PublishStreamComplete(a.bus, a.channels, ctx, a.id, "", usageAcc.Total())
	}
	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		RespondingAgentID:   a.id,
		RespondingAgentName: "Archivalist",
		ProcessingTime:      time.Since(startTime),
		Data:                result,
	}
	a.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeSuccess, "Request completed", snapshot)

	respMsg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(a.channels.Responses, respMsg)
}

func (a *Archivalist) handleActionMessage(msg *guide.Message) error {
	action, ok := msg.GetActionRequest()
	if !ok || action == nil {
		return nil
	}
	if a.steering.HandleAction(action) {
		return nil
	}
	switch action.Action {
	case "cancel":
		a.cancelRequest(action.CorrelationID)
	case archivalistStorePaperAction:
		return a.handleStoreResearchPaperAction(action)
	}
	return nil
}

func (a *Archivalist) generateMessageID() string {
	return fmt.Sprintf("archivalist_msg_%s", uuid.New().String())
}

func (a *Archivalist) registerRequestCancel(correlationID string, cancel context.CancelFunc) {
	a.requestMu.Lock()
	a.requestCancels[correlationID] = cancel
	a.requestMu.Unlock()
}

func (a *Archivalist) clearRequestCancel(correlationID string) {
	a.requestMu.Lock()
	delete(a.requestCancels, correlationID)
	a.requestMu.Unlock()
}

func (a *Archivalist) cancelRequest(correlationID string) {
	a.requestMu.Lock()
	cancel := a.requestCancels[correlationID]
	delete(a.requestCancels, correlationID)
	a.requestMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// publishActivity emits a user-visible activity event.
func (a *Archivalist) publishActivity(eventType events.EventType, content string) {
	if a.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, a.defaultSessionID, content)
	evt.AgentID = a.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "archivalist"
	evt.Data["agent_name"] = "Archivalist"
	a.activityPub.PublishActivity(evt)
}

// publishSystemEvent emits a system-level activity event for telemetry.
// System events are recorded but do not change the UI panel status.
func (a *Archivalist) publishSystemEvent(eventType events.EventType, content string) {
	if a.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, a.defaultSessionID, content)
	evt.AgentID = a.id
	evt.Visibility = events.VisibilitySystem
	evt.Data["agent_type"] = "archivalist"
	evt.Data["agent_name"] = "Archivalist"
	a.activityPub.PublishActivity(evt)
}

// processForwardedRequest handles the actual request processing.
// When LLM is enabled and a provider is available, builds a providers.Request
// and runs the tool loop. Falls back to direct intent-dispatch otherwise.
func (a *Archivalist) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	return a.processForwardedRequestWithBundle(ctx, fwd, nil)
}

func (a *Archivalist) processForwardedRequestWithBundle(ctx context.Context, fwd *guide.ForwardedRequest, bundle *archivalistToolBundle) (any, error) {
	if result, ok, err := a.maybeHandleBriefingRequest(fwd); ok {
		return result, err
	}
	if result, ok, err := a.maybeHandleCoordinationPrecedentQuery(ctx, fwd); ok {
		return result, err
	}
	if archivalistBypassesLLM(fwd) {
		return a.processDeterministicForwardedRequest(ctx, fwd)
	}
	if a.config.EnableLLM && a.getProvider() != nil {
		return a.processViaLLM(ctx, fwd, bundle)
	}
	return a.processDeterministicForwardedRequest(ctx, fwd)
}

func (a *Archivalist) maybeHandleBriefingRequest(fwd *guide.ForwardedRequest) (any, bool, error) {
	if fwd == nil || len(fwd.Metadata) == 0 {
		return nil, false, nil
	}
	requestType, _ := fwd.Metadata["request_type"].(string)
	toolName, _ := fwd.Metadata["tool_name"].(string)
	if !strings.EqualFold(strings.TrimSpace(requestType), "briefing") && strings.TrimSpace(toolName) != ToolGetBriefing {
		return nil, false, nil
	}

	format, _ := fwd.Metadata["brief_format"].(string)
	tier, _ := fwd.Metadata["brief_tier"].(string)
	if strings.EqualFold(strings.TrimSpace(format), "context_brief") {
		agentType, _ := fwd.Metadata["agent_type"].(string)
		contextSize := intFromAnyMap(fwd.Metadata, "context_size")
		turnNumber := intFromAnyMap(fwd.Metadata, "turn_number")
		return a.buildContextBrief(agentType, contextSize, turnNumber), true, nil
	}

	data, err := a.getBriefingToolData(tier)
	if err != nil {
		return nil, true, err
	}
	return data, true, nil
}

func archivalistBypassesLLM(fwd *guide.ForwardedRequest) bool {
	if fwd == nil {
		return false
	}
	switch fwd.Intent {
	case guide.IntentStore, guide.IntentDeclare, guide.IntentComplete:
		return true
	default:
		return false
	}
}

func archivalistPublishesUserActivity(fwd *guide.ForwardedRequest) bool {
	if fwd == nil {
		return true
	}
	if !fwd.FireAndForget {
		return true
	}
	return !archivalistBypassesLLM(fwd)
}

func (a *Archivalist) processDeterministicForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := a.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

func (a *Archivalist) maybeHandleCoordinationPrecedentQuery(ctx context.Context, fwd *guide.ForwardedRequest) (any, bool, error) {
	if fwd == nil || len(fwd.Metadata) == 0 {
		return nil, false, nil
	}
	flag, _ := fwd.Metadata["coordination_precedent_query"].(bool)
	if !flag {
		return nil, false, nil
	}

	taskName, _ := fwd.Metadata["task_name"].(string)
	taskSlug, _ := fwd.Metadata["task_slug"].(string)
	workerType, _ := fwd.Metadata["worker_type"].(string)
	limit := 4
	switch value := fwd.Metadata["limit"].(type) {
	case int:
		if value > 0 {
			limit = value
		}
	case float64:
		if value > 0 {
			limit = int(value)
		}
	}

	searchText := strings.TrimSpace(taskName)
	if searchText == "" {
		searchText = strings.TrimSpace(taskSlug)
	}
	query := ArchiveQuery{
		Categories:       []Category{CategoryDecision, CategoryInsight, CategoryIssue, CategoryGeneral},
		SearchText:       searchText,
		Limit:            limit,
		IncludeArchived:  true,
		CrossAgentPolicy: CrossAgentPolicyInclude,
	}
	results, err := a.QueryCrossSession(ctx, query)
	if err != nil {
		return nil, true, err
	}

	precedents := make([]map[string]any, 0, len(results))
	for _, result := range results {
		if result.Entry == nil {
			continue
		}
		summary := strings.TrimSpace(result.Entry.Content)
		if summary == "" {
			summary = strings.TrimSpace(result.Entry.Title)
		}
		precedents = append(precedents, map[string]any{
			"id":         result.Entry.ID,
			"session_id": result.SessionID,
			"category":   result.Entry.Category,
			"title":      result.Entry.Title,
			"summary":    summary,
			"metadata":   result.Entry.Metadata,
		})
		if len(precedents) >= limit {
			break
		}
	}
	return map[string]any{"precedents": precedents, "worker_type": workerType}, true, nil
}

// processViaLLM builds an LLM request with tools and runs the tool loop.
func (a *Archivalist) processViaLLM(ctx context.Context, fwd *guide.ForwardedRequest, bundle *archivalistToolBundle) (any, error) {
	if bundle != nil {
		bundle.prepareSkillsForInput(fwd.Input)
	} else {
		a.prepareSkillsForInput(fwd.Input)
	}
	llmReq := &providers.Request{
		SystemPrompt: a.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: fwd.Input}},
		Tools:        a.buildToolDefinitionsWithBundle(bundle),
		Model:        a.CurrentModel(),
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyConversationRuntimeProfile(llmReq)

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoopWithBundle(ctx, llmReq, ledger, bundle)
	})
	if err != nil {
		return nil, fmt.Errorf("archivalist failed: %w", err)
	}

	return result, nil
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (a *Archivalist) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentRecall:
		return a.handleRecall, nil
	case guide.IntentStore:
		return a.handleStore, nil
	case guide.IntentCheck:
		return a.handleCheck, nil
	case guide.IntentDeclare:
		return a.handleDeclare, nil
	case guide.IntentComplete:
		return a.handleComplete, nil
	case guide.IntentHelp:
		return a.handleHelp, nil
	default:
		return nil, fmt.Errorf("unsupported intent: %s", intent)
	}
}

// handleRecall processes recall (query) requests
func (a *Archivalist) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	if fwd.Domain == guide.DomainFiles {
		return a.agentContext.GetModifiedFiles(), nil
	}

	query := ArchiveQuery{}
	applyDomainFilters(&query, fwd.Domain)
	applyEntityFilters(&query, fwd.Entities)
	ensureQueryLimit(&query, 10)
	return a.Query(ctx, query)
}

func applyDomainFilters(query *ArchiveQuery, domain guide.Domain) {
	switch domain {
	case guide.DomainPatterns:
		query.Categories = []Category{CategoryGeneral}
	case guide.DomainFailures:
		query.Categories = []Category{CategoryIssue}
	case guide.DomainDecisions:
		query.Categories = []Category{CategoryDecision}
	case guide.DomainLearnings:
		query.Categories = []Category{CategoryInsight}
	}
}

func applyEntityFilters(query *ArchiveQuery, entities *guide.ExtractedEntities) {
	if entities == nil {
		return
	}
	if entities.Scope != "" {
		query.SearchText = entities.Scope
	}
	if entities.Limit > 0 {
		query.Limit = entities.Limit
	}
}

func ensureQueryLimit(query *ArchiveQuery, fallback int) {
	if query.Limit == 0 {
		query.Limit = fallback
	}
}

// handleStore processes store requests
func (a *Archivalist) handleStore(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	metadata := normalizeCrossAgentMetadata(fwd.Metadata, fwd.SourceAgentID, fwd.SourceAgentName)
	entry := &Entry{
		Content:  archivalistStoreContent(fwd),
		Source:   extractEntrySourceModel(metadata),
		Metadata: metadata,
	}

	// Map domain to category
	switch fwd.Domain {
	case guide.DomainPatterns:
		entry.Category = CategoryGeneral
	case guide.DomainFailures:
		entry.Category = CategoryIssue
	case guide.DomainDecisions:
		entry.Category = CategoryDecision
	case guide.DomainLearnings:
		entry.Category = CategoryInsight
	default:
		entry.Category = CategoryGeneral
	}

	return a.StoreEntry(ctx, entry), nil
}

func archivalistStoreContent(fwd *guide.ForwardedRequest) string {
	if fwd == nil {
		return ""
	}
	if fwd.Entities != nil {
		if fwd.Entities.Data != nil {
			if content, ok := fwd.Entities.Data["content"].(string); ok && strings.TrimSpace(content) != "" {
				return content
			}
		}
		if strings.TrimSpace(fwd.Entities.Query) != "" {
			return strings.TrimSpace(fwd.Entities.Query)
		}
	}
	return fwd.Input
}

// handleCheck processes check (verification) requests
func (a *Archivalist) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	// Search for matching entries
	entries, err := a.SearchText(ctx, fwd.Input, true, 5)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"found":   len(entries) > 0,
		"count":   len(entries),
		"entries": entries,
	}, nil
}

// handleDeclare processes declare (intent announcement) requests
func (a *Archivalist) handleDeclare(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.agentContext.SetCurrentTask(fwd.Input, "", SourceModelClaudeOpus)
	return map[string]any{"declared": true, "task": fwd.Input}, nil
}

// handleComplete processes complete (task completion) requests
func (a *Archivalist) handleComplete(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.agentContext.CompleteStep(fwd.Input)
	return map[string]any{"completed": true, "step": fwd.Input}, nil
}

func (a *Archivalist) handleHelp(_ context.Context, _ *guide.ForwardedRequest) (any, error) {
	return map[string]any{
		"agent":              "archivalist",
		"description":        "Historical memory, decisions, failures, and declared intents.",
		"supported_intents":  []guide.Intent{guide.IntentRecall, guide.IntentStore, guide.IntentCheck, guide.IntentDeclare, guide.IntentComplete, guide.IntentHelp},
		"supported_domains":  []guide.Domain{guide.DomainPatterns, guide.DomainFailures, guide.DomainDecisions, guide.DomainFiles, guide.DomainLearnings, guide.DomainIntents},
		"recommended_routes": []string{"@archivalist:recall:history", "@archivalist:store:history", "@archivalist:check:history"},
	}, nil
}

// handleBusResponse processes responses to requests we made
func (a *Archivalist) handleBusResponse(msg *guide.Message) error {
	// For now, just log responses to our requests
	// Future: implement callback handling for async sub-requests
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events
func (a *Archivalist) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	a.knownAgentsMu.Lock()
	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		a.knownAgents[ann.AgentID] = ann
	case guide.MessageTypeAgentUnregistered:
		delete(a.knownAgents, ann.AgentID)
	}
	a.knownAgentsMu.Unlock()

	return nil
}

// GetKnownAgents returns all agents the archivalist knows about
func (a *Archivalist) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	a.knownAgentsMu.RLock()
	defer a.knownAgentsMu.RUnlock()
	result := make(map[string]*guide.AgentAnnouncement, len(a.knownAgents))
	for k, v := range a.knownAgents {
		result[k] = v
	}
	return result
}

// PublishRequest publishes a request to the Guide for routing
func (a *Archivalist) PublishRequest(req *guide.RouteRequest) error {
	if !a.running {
		return fmt.Errorf("archivalist is not running")
	}

	req.SourceAgentID = "archivalist"
	req.SourceAgentName = "archivalist"

	msg := guide.NewRequestMessage(
		fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		req,
	)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

// StoreEntry stores a chronicle entry
func (a *Archivalist) StoreEntry(ctx context.Context, entry *Entry) SubmissionResult {
	if err := validateStoreEntry(entry); err != nil {
		return SubmissionResult{Success: false, Error: err}
	}

	storeData, result := a.runPreStoreHooks(ctx, entry)
	if result != nil {
		return *result
	}

	storeEntry, err := entryFromStoreData(storeData)
	if err != nil {
		return SubmissionResult{Success: false, Error: err}
	}

	storeResult, err := a.insertStoreEntry(storeEntry)
	hookErr := a.runPostStoreHooks(ctx, storeData, storeResult, err)
	return finalizeStoreResult(storeResult, err, hookErr)
}

func validateStoreEntry(entry *Entry) error {
	if IsValidSource(entry.Source) || entry.Source == SourceModelUser || entry.Source == SourceModelArchivalist {
		return nil
	}
	return fmt.Errorf("invalid source model: %s", entry.Source)
}

func (a *Archivalist) runPreStoreHooks(ctx context.Context, entry *Entry) (*skills.StoreHookData, *SubmissionResult) {
	storeData := &skills.StoreHookData{Entry: entry}
	if a.hooks == nil {
		return storeData, nil
	}
	updated, result, err := a.hooks.ExecutePreStoreHooks(ctx, storeData)
	if err != nil {
		return nil, &SubmissionResult{Success: false, Error: err}
	}
	if result.SkipExecution {
		return nil, &SubmissionResult{Success: true}
	}
	return updated, nil
}

func entryFromStoreData(storeData *skills.StoreHookData) (*Entry, error) {
	storeEntry, ok := storeData.Entry.(*Entry)
	if !ok || storeEntry == nil {
		return nil, fmt.Errorf("invalid store entry")
	}
	return storeEntry, nil
}

func (a *Archivalist) insertStoreEntry(storeEntry *Entry) (SubmissionResult, error) {
	id, err := a.store.InsertEntryInSession(a.GetDefaultSession(), storeEntry)
	return SubmissionResult{Success: err == nil, Error: err, ID: id}, err
}

func (a *Archivalist) runPostStoreHooks(ctx context.Context, storeData *skills.StoreHookData, result SubmissionResult, err error) error {
	storeData.Result = result
	storeData.Error = err
	if a.hooks == nil {
		return nil
	}
	_, _, hookErr := a.hooks.ExecutePostStoreHooks(ctx, storeData)
	return hookErr
}

func finalizeStoreResult(result SubmissionResult, err error, hookErr error) SubmissionResult {
	if hookErr != nil && err == nil {
		return SubmissionResult{Success: false, Error: hookErr}
	}
	return result
}

// StoreTaskState stores a task state entry (convenience wrapper)
func (a *Archivalist) StoreTaskState(ctx context.Context, content string, source SourceModel) SubmissionResult {
	return a.StoreEntry(ctx, &Entry{
		Category: CategoryTaskState,
		Content:  content,
		Source:   source,
	})
}

// StoreDecision stores a decision entry (convenience wrapper)
func (a *Archivalist) StoreDecision(ctx context.Context, choice, rationale string, source SourceModel) SubmissionResult {
	return a.StoreEntry(ctx, &Entry{
		Category: CategoryDecision,
		Title:    choice,
		Content:  rationale,
		Source:   source,
	})
}

// StoreIssue stores an issue entry (convenience wrapper)
func (a *Archivalist) StoreIssue(ctx context.Context, problem string, source SourceModel) SubmissionResult {
	return a.StoreEntry(ctx, &Entry{
		Category: CategoryIssue,
		Content:  problem,
		Source:   source,
	})
}

// StoreTimelineEvent stores a timeline event (convenience wrapper)
func (a *Archivalist) StoreTimelineEvent(ctx context.Context, content string, source SourceModel) SubmissionResult {
	return a.StoreEntry(ctx, &Entry{
		Category:  CategoryTimeline,
		Content:   content,
		Source:    source,
		CreatedAt: time.Now(),
	})
}

// StoreInsight stores an insight
func (a *Archivalist) StoreInsight(ctx context.Context, content string, source SourceModel) SubmissionResult {
	entry := &Entry{
		Category: CategoryInsight,
		Content:  content,
		Source:   source,
	}
	return a.StoreEntry(ctx, entry)
}

// StoreUserVoice stores a user preference or quote
func (a *Archivalist) StoreUserVoice(ctx context.Context, content string) SubmissionResult {
	entry := &Entry{
		Category: CategoryUserVoice,
		Content:  content,
		Source:   SourceModelUser,
	}
	return a.StoreEntry(ctx, entry)
}

// StoreHypothesis stores an untested assumption
func (a *Archivalist) StoreHypothesis(ctx context.Context, content string, source SourceModel) SubmissionResult {
	entry := &Entry{
		Category: CategoryHypothesis,
		Content:  content,
		Source:   source,
	}
	return a.StoreEntry(ctx, entry)
}

// StoreOpenThread stores an unfinished item to revisit
func (a *Archivalist) StoreOpenThread(ctx context.Context, content string, source SourceModel) SubmissionResult {
	entry := &Entry{
		Category: CategoryOpenThread,
		Content:  content,
		Source:   source,
	}
	return a.StoreEntry(ctx, entry)
}

// UpdateEntry updates an existing entry
func (a *Archivalist) UpdateEntry(ctx context.Context, id string, updates func(*Entry)) error {
	return a.store.UpdateEntry(id, updates)
}

// Query retrieves entries matching the query parameters
func (a *Archivalist) Query(ctx context.Context, query ArchiveQuery) ([]*Entry, error) {
	a.ensureQuerySession(&query)

	queryData, result := a.runPreQueryHooks(ctx, query)
	if result != nil {
		return result.entries, result.err
	}

	queryValue, err := queryDataArchiveQuery(queryData)
	if err != nil {
		return nil, err
	}

	entries, err := a.store.Query(a.expandQueryForBoundedInfluence(queryValue))
	if err == nil {
		entries = a.applyBoundedCrossAgentInfluence(queryValue, entries)
		entries = a.mergeCommittedKnowledgeRecall(ctx, queryValue, entries)
	}
	hookErr := a.runPostQueryHooks(ctx, queryData, entries, err)
	if hookErr != nil && err == nil {
		return nil, hookErr
	}
	return entries, err
}

type queryHookResult struct {
	entries []*Entry
	err     error
}

func (a *Archivalist) ensureQuerySession(query *ArchiveQuery) {
	if len(query.SessionIDs) > 0 {
		return
	}
	if sessionID := a.GetDefaultSession(); sessionID != "" {
		query.SessionIDs = []string{sessionID}
	}
}

func (a *Archivalist) runPreQueryHooks(ctx context.Context, query ArchiveQuery) (*skills.QueryHookData, *queryHookResult) {
	queryData := &skills.QueryHookData{Query: query}
	if a.hooks == nil {
		return queryData, nil
	}
	updated, result, err := a.hooks.ExecutePreQueryHooks(ctx, queryData)
	if err != nil {
		return nil, &queryHookResult{err: err}
	}
	if result.SkipExecution {
		return nil, &queryHookResult{entries: nil, err: nil}
	}
	return updated, nil
}

func queryDataArchiveQuery(queryData *skills.QueryHookData) (ArchiveQuery, error) {
	queryValue, ok := queryData.Query.(ArchiveQuery)
	if !ok {
		return ArchiveQuery{}, fmt.Errorf("invalid query")
	}
	return queryValue, nil
}

func (a *Archivalist) runPostQueryHooks(ctx context.Context, queryData *skills.QueryHookData, entries []*Entry, err error) error {
	queryData.Result = entries
	queryData.Error = err
	if a.hooks == nil {
		return nil
	}
	_, _, hookErr := a.hooks.ExecutePostQueryHooks(ctx, queryData)
	return hookErr
}

func (a *Archivalist) mergeCommittedKnowledgeRecall(ctx context.Context, query ArchiveQuery, archiveEntries []*Entry) []*Entry {
	knowledgeEntries := a.collectKnowledgeRecallEntries(ctx, query)
	if len(knowledgeEntries) == 0 {
		return archiveEntries
	}
	limit := query.Limit
	if limit <= 0 {
		limit = len(knowledgeEntries) + len(archiveEntries)
	}
	return interleaveRecallEntries(knowledgeEntries, archiveEntries, limit)
}

func knowledgeEntryCategory(docType search.DocumentType) Category {
	switch docType {
	case search.DocTypeSourceCode, search.DocTypeConfig:
		return CategoryCodebaseMap
	case search.DocTypeWebFetch, search.DocTypeMarkdown, search.DocTypeNote:
		return CategoryInsight
	default:
		return CategoryGeneral
	}
}

func committedKnowledgeExcerpt(queryText, content string, limit int) string {
	content = strings.TrimSpace(content)
	if limit <= 0 || len(content) <= limit {
		return content
	}
	queryText = strings.TrimSpace(strings.ToLower(queryText))
	if queryText == "" {
		return content[:limit] + "..."
	}
	lower := strings.ToLower(content)
	idx := strings.Index(lower, queryText)
	if idx < 0 {
		return content[:limit] + "..."
	}
	start := idx - limit/3
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > len(content) {
		end = len(content)
		start = max(0, end-limit)
	}
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(content) {
		suffix = "..."
	}
	return prefix + content[start:end] + suffix
}

func interleaveRecallEntries(primary, secondary []*Entry, limit int) []*Entry {
	if limit <= 0 {
		limit = len(primary) + len(secondary)
	}
	out := make([]*Entry, 0, min(limit, len(primary)+len(secondary)))
	for i := 0; len(out) < limit && (i < len(primary) || i < len(secondary)); i++ {
		if i < len(primary) {
			out = append(out, primary[i])
			if len(out) == limit {
				break
			}
		}
		if i < len(secondary) {
			out = append(out, secondary[i])
		}
	}
	return out
}

// QueryByCategory retrieves entries in a specific category
func (a *Archivalist) QueryByCategory(ctx context.Context, category Category, limit int) []*Entry {
	return a.store.QueryByCategory(category, limit)
}

// SearchText performs text search across all storage
func (a *Archivalist) SearchText(ctx context.Context, text string, includeArchived bool, limit int) ([]*Entry, error) {
	query := ArchiveQuery{
		SearchText:      text,
		Limit:           limit,
		IncludeArchived: includeArchived,
	}
	if sessionID := a.GetDefaultSession(); sessionID != "" {
		query.SessionIDs = []string{sessionID}
	}
	return a.Query(ctx, query)
}

// GetEntry retrieves a single entry by ID
func (a *Archivalist) GetEntry(ctx context.Context, id string) (*Entry, bool) {
	return a.store.GetEntry(id)
}

// RestoreFromArchive pulls entries from archive back into hot memory
func (a *Archivalist) RestoreFromArchive(ctx context.Context, ids []string) error {
	return a.store.RestoreFromArchive(ids)
}

// GenerateSummary creates a summary using Claude Sonnet 4.5
func (a *Archivalist) GenerateSummary(ctx context.Context, content string) (*Entry, error) {
	generated, err := a.client.GenerateSummary(ctx, content)
	if err != nil {
		return nil, err
	}

	entry := &Entry{
		Category:       CategoryGeneral,
		Title:          "Generated Summary",
		Content:        generated.Content,
		Source:         SourceModelArchivalist,
		TokensEstimate: generated.TokensUsed,
	}

	_, err = a.store.InsertEntryInSession(a.GetDefaultSession(), entry)
	if err != nil {
		return nil, fmt.Errorf("failed to store generated summary: %w", err)
	}

	return entry, nil
}

// GenerateSummaryFromEntries creates a summary from stored entries matching the query
func (a *Archivalist) GenerateSummaryFromEntries(ctx context.Context, query ArchiveQuery) (*Entry, error) {
	entries, err := a.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no entries found matching query")
	}

	// Convert entries to submissions for the client
	var submissions []Submission
	for _, e := range entries {
		submissions = append(submissions, Submission{
			Type: SubmissionTypeSummary,
			Summary: &Summary{
				ID:        e.ID,
				Content:   e.Content,
				Source:    e.Source,
				CreatedAt: e.CreatedAt,
			},
		})
	}

	generated, err := a.client.GenerateSummaryFromSubmissions(ctx, submissions)
	if err != nil {
		return nil, err
	}

	entry := &Entry{
		Category:       CategoryGeneral,
		Title:          "Generated Summary",
		Content:        generated.Content,
		Source:         SourceModelArchivalist,
		TokensEstimate: generated.TokensUsed,
		RelatedIDs:     generated.SourceIDs,
	}

	_, err = a.store.InsertEntryInSession(a.GetDefaultSession(), entry)
	if err != nil {
		return nil, fmt.Errorf("failed to store generated summary: %w", err)
	}

	return entry, nil
}

// GetSnapshot returns a full snapshot of current state
func (a *Archivalist) GetSnapshot(ctx context.Context) *ChronicleSnapshot {
	stats := a.store.Stats()
	session := a.store.GetCurrentSession()

	return &ChronicleSnapshot{
		Session:        session,
		Tasks:          a.store.QueryByCategory(CategoryTaskState, 10),
		OpenThreads:    a.store.QueryByCategory(CategoryOpenThread, 10),
		RecentTimeline: a.store.QueryByCategory(CategoryTimeline, 20),
		ActiveInsights: a.store.QueryByCategory(CategoryInsight, 10),
		UserVoice:      a.store.QueryByCategory(CategoryUserVoice, 10),
		Hypotheses:     a.store.QueryByCategory(CategoryHypothesis, 10),
		Stats:          stats,
	}
}

// EndSession ends the current session and archives its data
func (a *Archivalist) EndSession(ctx context.Context, summary string, primaryFocus string) error {
	err := a.store.EndSession(summary, primaryFocus)
	if err != nil {
		return err
	}
	a.defaultSessionID = a.store.GetCurrentSession().ID
	return nil
}

// GetCurrentSession returns the current session
func (a *Archivalist) GetCurrentSession() *Session {
	return a.store.GetCurrentSession()
}

func (a *Archivalist) GetDefaultSession() string {
	if a == nil || a.store == nil {
		return a.defaultSessionID
	}
	if a.defaultSessionID == "" {
		if current := a.store.GetCurrentSession(); current != nil {
			return current.ID
		}
	}
	return a.defaultSessionID
}

func (a *Archivalist) SetDefaultSession(sessionID string) {
	a.defaultSessionID = sessionID
}

// GetRecentSessions retrieves recent sessions from the archive
func (a *Archivalist) GetRecentSessions(ctx context.Context, limit int) ([]*Session, error) {
	if a.archive == nil {
		return nil, fmt.Errorf("archive not enabled")
	}
	return a.archive.GetRecentSessions(limit)
}

// Stats returns current storage statistics
func (a *Archivalist) Stats() StorageStats {
	return a.store.Stats()
}

// Legacy compatibility types for backward compatibility

// Summary represents a summary submitted by an external model (legacy)
type Summary struct {
	ID        string         `json:"id"`
	Content   string         `json:"content"`
	Source    SourceModel    `json:"source"`
	CreatedAt time.Time      `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// PromptResponse represents a prompt and its response from an external model (legacy)
type PromptResponse struct {
	ID        string         `json:"id"`
	Prompt    string         `json:"prompt"`
	Response  string         `json:"response"`
	Source    SourceModel    `json:"source"`
	CreatedAt time.Time      `json:"created_at"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// SubmissionType categorizes what kind of data was submitted (legacy)
type SubmissionType string

const (
	SubmissionTypeSummary        SubmissionType = "summary"
	SubmissionTypePromptResponse SubmissionType = "prompt_response"
)

// Submission wraps either a Summary or PromptResponse for unified handling (legacy)
type Submission struct {
	Type           SubmissionType  `json:"type"`
	Summary        *Summary        `json:"summary,omitempty"`
	PromptResponse *PromptResponse `json:"prompt_response,omitempty"`
}

// SubmitSummary accepts a summary from an external AI model (legacy compatibility)
func (a *Archivalist) SubmitSummary(ctx context.Context, content string, source SourceModel, metadata map[string]any) SubmissionResult {
	entry := &Entry{
		Category: CategoryGeneral,
		Content:  content,
		Source:   source,
		Metadata: metadata,
	}
	return a.StoreEntry(ctx, entry)
}

// SubmitPromptResponse accepts a prompt/response pair from an external AI model (legacy compatibility)
func (a *Archivalist) SubmitPromptResponse(ctx context.Context, prompt, response string, source SourceModel, metadata map[string]any) SubmissionResult {
	entry := &Entry{
		Category: CategoryGeneral,
		Title:    prompt,
		Content:  response,
		Source:   source,
		Metadata: metadata,
	}
	return a.StoreEntry(ctx, entry)
}

// =============================================================================
// Agent Context Methods - For agent-to-agent coordination
// =============================================================================

// RecordFileRead records that a file has been read by an agent
func (a *Archivalist) RecordFileRead(path, summary string, agent SourceModel) {
	a.agentContext.RecordFileRead(path, summary, agent)
}

// RecordFileModified records that a file has been modified
func (a *Archivalist) RecordFileModified(path string, startLine, endLine int, description string, agent SourceModel) {
	a.agentContext.RecordFileModified(path, FileChange{
		StartLine:   startLine,
		EndLine:     endLine,
		Description: description,
	}, agent)
}

// RecordFileCreated records that a file has been created
func (a *Archivalist) RecordFileCreated(path, summary string, agent SourceModel) {
	a.agentContext.RecordFileCreated(path, summary, agent)
}

// WasFileRead checks if a file has already been read this session
func (a *Archivalist) WasFileRead(path string) bool {
	return a.agentContext.WasFileRead(path)
}

// GetFileState returns the tracked state of a file
func (a *Archivalist) GetFileState(path string) (*FileState, bool) {
	return a.agentContext.GetFileStateWithCheck(path)
}

// GetModifiedFiles returns all files that have been modified or created
func (a *Archivalist) GetModifiedFiles() []*FileState {
	return a.agentContext.GetModifiedFiles()
}

// RegisterPattern registers a coding pattern to follow
func (a *Archivalist) RegisterPattern(category, name, description, example string, agent SourceModel) {
	a.agentContext.RegisterPattern(&Pattern{
		Category:    category,
		Name:        name,
		Description: description,
		Example:     example,
		Source:      agent,
	})
}

// GetPatterns returns all registered patterns
func (a *Archivalist) GetPatterns() []*Pattern {
	return a.agentContext.GetAllPatterns()
}

// GetPatternsByCategory returns patterns for a specific category
func (a *Archivalist) GetPatternsByCategory(category string) []*Pattern {
	return a.agentContext.GetPatternsByCategory(category)
}

// RecordFailure records an approach that failed
func (a *Archivalist) RecordFailure(approach, reason, taskContext string, agent SourceModel) {
	a.agentContext.RecordFailure(approach, reason, taskContext, agent)
}

// RecordFailureWithResolution records a failure and what worked instead
func (a *Archivalist) RecordFailureWithResolution(approach, reason, taskContext, resolution string, agent SourceModel) {
	a.agentContext.RecordFailureWithResolution(approach, reason, taskContext, resolution, agent)
}

// CheckFailure checks if an approach has been tried and failed
func (a *Archivalist) CheckFailure(approach string) (*Failure, bool) {
	return a.agentContext.CheckFailure(approach)
}

// GetRecentFailures returns recent failures
func (a *Archivalist) GetRecentFailures(limit int) []*Failure {
	return a.agentContext.GetRecentFailures(limit)
}

// RecordUserWants records something the user wants
func (a *Archivalist) RecordUserWants(content, priority, source string) {
	a.agentContext.RecordUserWants(content, priority, source)
}

// RecordUserRejects records something the user rejected
func (a *Archivalist) RecordUserRejects(content, source string) {
	a.agentContext.RecordUserRejects(content, source)
}

// GetUserWants returns all recorded user wants
func (a *Archivalist) GetUserWants() []*Intent {
	return a.agentContext.GetUserWants()
}

// GetUserRejects returns all recorded user rejections
func (a *Archivalist) GetUserRejects() []*Intent {
	return a.agentContext.GetUserRejects()
}

// SetCurrentTask sets the current task being worked on
func (a *Archivalist) SetCurrentTask(task, objective string, agent SourceModel) {
	a.agentContext.SetCurrentTask(task, objective, agent)
}

// CompleteStep marks a step as completed
func (a *Archivalist) CompleteStep(step string) {
	a.agentContext.CompleteStep(step)
}

// SetCurrentStep sets the current step being worked on
func (a *Archivalist) SetCurrentStep(step string) {
	a.agentContext.SetCurrentStep(step)
}

// SetNextSteps sets the upcoming steps
func (a *Archivalist) SetNextSteps(steps []string) {
	a.agentContext.SetNextSteps(steps)
}

// AddBlocker adds a blocker
func (a *Archivalist) AddBlocker(blocker string) {
	a.agentContext.AddBlocker(blocker)
}

// RemoveBlocker removes a blocker
func (a *Archivalist) RemoveBlocker(blocker string) {
	a.agentContext.RemoveBlocker(blocker)
}

// GetResumeState returns the current resume state for handoff
func (a *Archivalist) GetResumeState() *ResumeState {
	return a.agentContext.GetResumeState()
}

// GetAgentBriefing returns everything an agent needs to continue work
func (a *Archivalist) GetAgentBriefing() *AgentBriefing {
	briefing := a.agentContext.GetAgentBriefing()
	if briefing == nil {
		return nil
	}
	briefing.DeclaredIntents = a.ActiveWorkIntents()
	return briefing
}

func (a *Archivalist) buildContextBrief(agentType string, contextSize, turnNumber int) *handoff.ContextBrief {
	briefing := a.GetAgentBriefing()
	now := time.Now()
	result := &handoff.ContextBrief{
		GeneratedAt: now,
		ContextSize: contextSize,
		TurnNumber:  turnNumber,
	}
	if briefing == nil {
		result.TaskSummary = "No archived handoff context is available yet."
		result.KeyDecisions = "No prior decisions recorded."
		result.ActiveState = "No active state recorded."
		result.NextSteps = "Rebuild context from the live conversation."
		result.Blockers = "none"
		return result
	}

	resume := briefing.ResumeState
	if resume != nil {
		taskParts := make([]string, 0, 4)
		if task := strings.TrimSpace(resume.CurrentTask); task != "" {
			taskParts = append(taskParts, task)
		}
		if step := strings.TrimSpace(resume.CurrentStep); step != "" {
			taskParts = append(taskParts, "Current step: "+step)
		}
		if completed := len(resume.CompletedSteps); completed > 0 {
			taskParts = append(taskParts, fmt.Sprintf("%d completed step(s)", completed))
		}
		if len(taskParts) > 0 {
			result.TaskSummary = strings.Join(taskParts, ". ")
		}
		if next := summarizeStrings(resume.NextSteps, 3); next != "" {
			result.NextSteps = next
		}
		if blockers := summarizeStrings(resume.Blockers, 3); blockers != "" {
			result.Blockers = blockers
		}
	}
	if result.TaskSummary == "" {
		if trimmed := strings.TrimSpace(agentType); trimmed != "" {
			result.TaskSummary = fmt.Sprintf("Continue %s work using the archived session context.", trimmed)
		} else {
			result.TaskSummary = "Continue work using the archived session context."
		}
	}
	if result.NextSteps == "" {
		result.NextSteps = "Inspect the archived briefing and resume the highest-priority unfinished work."
	}
	if result.Blockers == "" {
		result.Blockers = "none"
	}

	keyDecisionParts := make([]string, 0, 2)
	if patterns := summarizePatternHeadlines(briefing.Patterns, 3); patterns != "" {
		keyDecisionParts = append(keyDecisionParts, "Patterns: "+patterns)
	}
	if failures := summarizeFailureHeadlines(briefing.RecentFailures, 2); failures != "" {
		keyDecisionParts = append(keyDecisionParts, "Failures: "+failures)
	}
	if len(keyDecisionParts) == 0 {
		result.KeyDecisions = "No major archived decisions or lessons are recorded yet."
	} else {
		result.KeyDecisions = strings.Join(keyDecisionParts, " ")
	}

	activeStateParts := make([]string, 0, 3)
	if files := summarizeFilePaths(briefing.ModifiedFiles, 4); files != "" {
		activeStateParts = append(activeStateParts, "Modified files: "+files)
	}
	if intents := summarizeWorkIntentHeadlines(briefing.DeclaredIntents, 2); intents != "" {
		activeStateParts = append(activeStateParts, "Declared intents: "+intents)
	}
	if wants := summarizeIntentHeadlines(briefing.UserWants, 2); wants != "" {
		activeStateParts = append(activeStateParts, "User wants: "+wants)
	}
	if len(activeStateParts) == 0 {
		result.ActiveState = "No modified files or active intents recorded."
	} else {
		result.ActiveState = strings.Join(activeStateParts, " ")
	}

	return result
}

func summarizeStrings(values []string, limit int) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		items = append(items, trimmed)
		if len(items) >= limit {
			break
		}
	}
	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, "; ")
}

func summarizePatternHeadlines(patterns []*Pattern, limit int) string {
	items := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == nil {
			continue
		}
		headline := strings.TrimSpace(pattern.Pattern)
		if headline == "" {
			headline = strings.TrimSpace(pattern.Category)
		}
		if headline == "" {
			continue
		}
		items = append(items, headline)
		if len(items) >= limit {
			break
		}
	}
	return summarizeStrings(items, limit)
}

func summarizeFailureHeadlines(failures []*Failure, limit int) string {
	items := make([]string, 0, len(failures))
	for _, failure := range failures {
		if failure == nil {
			continue
		}
		headline := strings.TrimSpace(failure.Resolution)
		if headline == "" {
			headline = strings.TrimSpace(failure.Reason)
		}
		if headline == "" {
			continue
		}
		items = append(items, headline)
		if len(items) >= limit {
			break
		}
	}
	return summarizeStrings(items, limit)
}

func summarizeFilePaths(files []*FileState, limit int) string {
	items := make([]string, 0, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		items = append(items, path)
		if len(items) >= limit {
			break
		}
	}
	return summarizeStrings(items, limit)
}

func summarizeWorkIntentHeadlines(intents []*WorkIntent, limit int) string {
	items := make([]string, 0, len(intents))
	for _, intent := range intents {
		if intent == nil {
			continue
		}
		headline := strings.TrimSpace(intent.Description)
		if headline == "" {
			headline = strings.TrimSpace(intent.Type)
		}
		if headline == "" {
			continue
		}
		items = append(items, headline)
		if len(items) >= limit {
			break
		}
	}
	return summarizeStrings(items, limit)
}

func summarizeIntentHeadlines(intents []*Intent, limit int) string {
	items := make([]string, 0, len(intents))
	for _, intent := range intents {
		if intent == nil {
			continue
		}
		headline := strings.TrimSpace(intent.Content)
		if headline == "" {
			headline = strings.TrimSpace(string(intent.Type))
		}
		if headline == "" {
			continue
		}
		items = append(items, headline)
		if len(items) >= limit {
			break
		}
	}
	return summarizeStrings(items, limit)
}

func intFromAnyMap(data map[string]any, key string) int {
	switch value := data[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

// =============================================================================
// Protocol Methods - Efficient RMC for agent-to-agent communication
// =============================================================================

// RegisterAgent registers a new agent and returns its ID and current version
func (a *Archivalist) RegisterAgent(name, sessionID, parentID string, source SourceModel) (*Response, error) {
	if sessionID == "" {
		sessionID = a.store.GetCurrentSession().ID
	}

	agent, err := a.registry.Register(name, sessionID, parentID, source)
	if err != nil {
		return nil, err
	}

	// Log the registration event
	a.eventLog.Append(&Event{
		Type:      EventTypeAgentRegister,
		Version:   a.registry.GetVersion(),
		AgentID:   agent.ID,
		SessionID: sessionID,
		Data: map[string]any{
			"name":      name,
			"parent_id": parentID,
			"source":    string(source),
		},
	})

	return &Response{
		Status:  StatusOK,
		Version: agent.LastVersion,
		AgentID: agent.ID,
	}, nil
}

// UnregisterAgent removes an agent from the registry
func (a *Archivalist) UnregisterAgent(agentID string) error {
	agent := a.registry.Get(agentID)
	if agent == nil {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Log the event
	a.eventLog.Append(&Event{
		Type:      EventTypeAgentUnregister,
		Version:   a.registry.GetVersion(),
		AgentID:   agentID,
		SessionID: agent.SessionID,
	})

	return a.registry.Unregister(agentID)
}

// HandleRequest processes a protocol request and returns a response
func (a *Archivalist) HandleRequest(ctx context.Context, req *Request) Response {
	handlers := a.requestHandlers(ctx, req)
	handler, ok := handlers[req.GetMessageType()]
	if !ok {
		return ErrorResponse("unknown request type")
	}
	return handler()
}

type requestHandler func() Response

type requestHandlers map[MessageType]requestHandler

func (a *Archivalist) requestHandlers(ctx context.Context, req *Request) requestHandlers {
	return requestHandlers{
		MsgTypeRegister: func() Response { return a.handleRegister(ctx, req) },
		MsgTypeWrite:    func() Response { return a.handleWrite(ctx, req) },
		MsgTypeBatch:    func() Response { return a.handleBatch(ctx, req) },
		MsgTypeRead:     func() Response { return a.handleRead(ctx, req) },
		MsgTypeBriefing: func() Response { return a.handleBriefing(ctx, req) },
	}
}

func (a *Archivalist) handleRegister(ctx context.Context, req *Request) Response {
	if req.Register == nil {
		return ErrorResponse("missing register data")
	}

	resp, err := a.RegisterAgent(
		req.Register.Name,
		req.Register.Session,
		req.Register.ParentID,
		SourceModelClaudeOpus, // Default source
	)
	if err != nil {
		return ErrorResponse(err.Error())
	}
	return *resp
}

func (a *Archivalist) handleWrite(ctx context.Context, req *Request) Response {
	scope, key, resp := a.prepareWrite(req)
	if resp != nil {
		return *resp
	}

	conflictResult := a.detectAndRecordConflict(scope, key, req)
	if !conflictResult.Resolved {
		return ConflictResponse(a.registry.GetVersion(), string(conflictResult.Type), conflictResult.Message)
	}

	return a.executeWrite(ctx, scope, key, req, conflictResult)
}

func (a *Archivalist) prepareWrite(req *Request) (Scope, string, *Response) {
	if err := a.validateWriteRequest(req); err != nil {
		resp := ErrorResponse(err.Error())
		return "", "", &resp
	}

	a.registry.Touch(req.AgentID)
	scope, key := ParseScope(req.Write.Scope)

	if resp := a.checkVersionConflict(req); resp != nil {
		return scope, key, resp
	}

	return scope, key, nil
}

func (a *Archivalist) validateWriteRequest(req *Request) error {
	if req.Write == nil {
		return fmt.Errorf("missing write data")
	}
	if req.AgentID == "" {
		return fmt.Errorf("agent_id required")
	}
	return nil
}

func (a *Archivalist) checkVersionConflict(req *Request) *Response {
	if req.Write.ExpectNoConflict || req.Version == "" {
		return nil
	}
	if a.registry.IsVersionCurrent(req.Version) {
		return nil
	}
	delta, _ := a.registry.GetVersionDelta(req.Version)
	resp := ConflictResponse(a.registry.GetVersion(), string(ConflictTypeVersion), fmt.Sprintf("version behind by %d", delta))
	return &resp
}

func (a *Archivalist) detectAndRecordConflict(scope Scope, key string, req *Request) *ConflictResult {
	result := a.conflictDetector.DetectConflict(scope, key, req.Write.Data, req.Version, req.AgentID)
	if result.Type != ConflictTypeNone {
		a.recordConflict(scope, key, req, result)
	}
	return result
}

func (a *Archivalist) recordConflict(scope Scope, key string, req *Request, result *ConflictResult) {
	a.conflictHistory.Record(&ConflictRecord{
		ID: uuid.New().String(), DetectedAt: time.Now(), Type: result.Type,
		Scope: scope, Key: key, AgentID: req.AgentID, BaseVersion: req.Version,
		Result: result, AutoResolved: result.Resolved,
	})
}

func (a *Archivalist) executeWrite(ctx context.Context, scope Scope, key string, req *Request, result *ConflictResult) Response {
	finalData := a.conflictDetector.Resolve(result, nil, req.Write.Data)
	if err := a.applyWrite(ctx, scope, key, finalData, req.AgentID); err != nil {
		return ErrorResponse(err.Error())
	}

	newVersion := a.registry.IncrementVersion()
	a.registry.UpdateVersion(req.AgentID, newVersion)
	a.logWriteEvent(scope, key, finalData, req.AgentID, newVersion)

	if result.Strategy == ResolutionMerge || result.Strategy == ResolutionCombine {
		return MergedResponse(newVersion, result.Message)
	}
	return OKResponse(newVersion)
}

func (a *Archivalist) logWriteEvent(scope Scope, key string, data map[string]any, agentID, version string) {
	a.eventLog.Append(&Event{
		Type: scopeToEventType(scope, false), Version: version,
		Clock: a.registry.GetGlobalClock(), AgentID: agentID,
		SessionID: a.store.GetCurrentSession().ID, Scope: scope, Key: key, Data: data,
	})
}

func (a *Archivalist) handleBatch(ctx context.Context, req *Request) Response {
	if err := validateBatchRequest(req); err != nil {
		return ErrorResponse(err.Error())
	}

	a.registry.Touch(req.AgentID)

	lastVersion, succeeded, failed := a.processBatchWrites(ctx, req)
	return batchResponse(lastVersion, succeeded, failed)
}

func validateBatchRequest(req *Request) error {
	if req.Batch == nil || len(req.Batch.Writes) == 0 {
		return fmt.Errorf("missing batch data")
	}
	if req.AgentID == "" {
		return fmt.Errorf("agent_id required")
	}
	return nil
}

func (a *Archivalist) processBatchWrites(ctx context.Context, req *Request) (string, int, int) {
	lastVersion := ""
	succeeded := 0
	failed := 0

	for _, write := range req.Batch.Writes {
		resp := a.executeBatchWrite(ctx, req, write)
		lastVersion, succeeded, failed = updateBatchCounters(resp, lastVersion, succeeded, failed)
	}

	return lastVersion, succeeded, failed
}

func (a *Archivalist) executeBatchWrite(ctx context.Context, req *Request, write WriteRequest) Response {
	writeReq := &Request{
		AgentID: req.AgentID,
		Version: req.Version,
		Write:   &write,
	}
	resp := a.handleWrite(ctx, writeReq)
	if resp.Status == StatusOK || resp.Status == StatusMerged {
		req.Version = resp.Version
	}
	return resp
}

func updateBatchCounters(resp Response, lastVersion string, succeeded int, failed int) (string, int, int) {
	if resp.Status == StatusOK || resp.Status == StatusMerged {
		return resp.Version, succeeded + 1, failed
	}
	return lastVersion, succeeded, failed + 1
}

func batchResponse(lastVersion string, succeeded int, failed int) Response {
	if failed == 0 {
		return BatchOKResponse(lastVersion, succeeded)
	}
	return PartialResponse(lastVersion, succeeded, failed)
}

func (a *Archivalist) handleRead(ctx context.Context, req *Request) Response {
	if req.Read == nil {
		return ErrorResponse("missing read data")
	}

	if req.AgentID != "" {
		a.registry.Touch(req.AgentID)
	}

	if req.Read.Since != "" {
		return a.handleDeltaRead(req)
	}

	return a.handleFullRead(req)
}

func (a *Archivalist) handleDeltaRead(req *Request) Response {
	delta := a.eventLog.GetDelta(req.Read.Since, req.Read.Limit)
	return Response{
		Status:  StatusOK,
		Version: a.registry.GetVersion(),
		Delta:   delta,
	}
}

func (a *Archivalist) handleFullRead(req *Request) Response {
	scope, key := ParseScope(req.Read.Scope)
	data := a.readScope(scope, key, req.Read.Limit)

	return Response{
		Status:  StatusOK,
		Version: a.registry.GetVersion(),
		Data:    data,
	}
}

func (a *Archivalist) handleBriefing(ctx context.Context, req *Request) Response {
	if req.Briefing == nil {
		return ErrorResponse("missing briefing data")
	}

	if req.AgentID != "" {
		a.registry.Touch(req.AgentID)
	}

	tier := resolveBriefingTier(req.Briefing.Tier)
	return a.dispatchBriefing(tier)
}

func resolveBriefingTier(tier BriefingTier) BriefingTier {
	if tier == "" {
		return BriefingStandard
	}
	return tier
}

func (a *Archivalist) dispatchBriefing(tier BriefingTier) Response {
	switch tier {
	case BriefingMicro:
		return a.getMicroBriefing()
	case BriefingStandard:
		return a.getStandardBriefing()
	case BriefingFull:
		return a.getFullBriefing()
	default:
		return a.getStandardBriefing()
	}
}

func (a *Archivalist) getMicroBriefing() Response {
	resume := a.agentContext.GetResumeState()
	if resume == nil {
		return Response{
			Status:   StatusOK,
			Version:  a.registry.GetVersion(),
			Briefing: "no-task:0/0:none:block=none",
		}
	}

	modPaths := collectModifiedPaths(a.agentContext.GetModifiedFiles())
	blocker := firstBriefingBlocker(resume.Blockers)
	totalSteps := len(resume.CompletedSteps) + len(resume.NextSteps)
	briefing := MicroBriefing(
		resume.CurrentTask,
		len(resume.CompletedSteps),
		totalSteps,
		modPaths,
		blocker,
	)

	return Response{
		Status:   StatusOK,
		Version:  a.registry.GetVersion(),
		Briefing: briefing,
	}
}

func collectModifiedPaths(files []*FileState) []string {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = file.Path
	}
	return paths
}

func firstBriefingBlocker(blockers []string) string {
	if len(blockers) == 0 {
		return ""
	}
	return blockers[0]
}

func (a *Archivalist) getStandardBriefing() Response {
	briefing := a.GetAgentBriefing()
	return Response{
		Status:  StatusOK,
		Version: a.registry.GetVersion(),
		Data:    briefing,
	}
}

func (a *Archivalist) getFullBriefing() Response {
	briefing := a.GetAgentBriefing()
	snapshot := a.GetSnapshot(context.Background())

	fullBriefing := map[string]any{
		"agent_briefing": briefing,
		"snapshot":       snapshot,
		"registry_stats": a.registry.GetStats(),
		"event_stats":    a.eventLog.Stats(),
		"conflicts":      a.conflictHistory.GetUnresolved(),
	}

	return Response{
		Status:  StatusOK,
		Version: a.registry.GetVersion(),
		Data:    fullBriefing,
	}
}

func (a *Archivalist) applyWrite(ctx context.Context, scope Scope, key string, data map[string]any, agentID string) error {
	agent := a.registry.Get(agentID)
	source := SourceModelArchivalist
	if agent != nil {
		source = agent.Source
	}

	switch scope {
	case ScopeFiles:
		return a.applyFileWrite(key, data, source)
	case ScopePatterns:
		return a.applyPatternWrite(key, data, source, agentID)
	case ScopeFailures:
		return a.applyFailureWrite(data, source)
	case ScopeIntents:
		return a.applyIntentWrite(data)
	case ScopeResume:
		return a.applyResumeWrite(data, source)
	default:
		return fmt.Errorf("unknown scope: %s", scope)
	}
}

func (a *Archivalist) applyFileWrite(path string, data map[string]any, source SourceModel) error {
	status, _ := data["status"].(string)
	summary, _ := data["summary"].(string)

	switch FileStatus(status) {
	case FileStatusRead:
		a.agentContext.RecordFileRead(path, summary, source)
	case FileStatusModified:
		changes, _ := data["changes"].([]any)
		for _, ch := range changes {
			if chMap, ok := ch.(map[string]any); ok {
				startLine, _ := chMap["start_line"].(float64)
				endLine, _ := chMap["end_line"].(float64)
				desc, _ := chMap["description"].(string)
				a.agentContext.RecordFileModified(path, FileChange{
					StartLine:   int(startLine),
					EndLine:     int(endLine),
					Description: desc,
				}, source)
			}
		}
	case FileStatusCreated:
		a.agentContext.RecordFileCreated(path, summary, source)
	}
	return nil
}

func (a *Archivalist) applyPatternWrite(category string, data map[string]any, source SourceModel, agentID string) error {
	name, _ := data["name"].(string)
	description, _ := data["description"].(string)
	example, _ := data["example"].(string)
	pattern, _ := data["pattern"].(string)

	if pattern != "" {
		description = pattern
	}

	a.agentContext.RegisterPattern(&Pattern{
		Category:      category,
		Name:          name,
		Description:   description,
		Example:       example,
		Source:        source,
		EstablishedBy: agentID,
	})
	return nil
}

func (a *Archivalist) applyFailureWrite(data map[string]any, source SourceModel) error {
	approach, _ := data["approach"].(string)
	reason, _ := data["reason"].(string)
	taskContext, _ := data["context"].(string)
	resolution, _ := data["resolution"].(string)

	if resolution != "" {
		a.agentContext.RecordFailureWithResolution(approach, reason, taskContext, resolution, source)
	} else {
		a.agentContext.RecordFailure(approach, reason, taskContext, source)
	}
	return nil
}

func (a *Archivalist) applyIntentWrite(data map[string]any) error {
	intentType, _ := data["type"].(string)
	description, _ := data["description"].(string)
	priority, _ := data["priority"].(string)
	source, _ := data["source"].(string)

	if intentType == "want" {
		a.agentContext.RecordUserWants(description, priority, source)
	} else {
		a.agentContext.RecordUserRejects(description, source)
	}
	return nil
}

func (a *Archivalist) applyResumeWrite(data map[string]any, source SourceModel) error {
	a.applyTaskUpdate(data, source)
	a.applyCompletedSteps(data)
	a.applyNextSteps(data)
	a.applyBlockers(data)
	return nil
}

func (a *Archivalist) applyTaskUpdate(data map[string]any, source SourceModel) {
	task, _ := data["current_task"].(string)
	if task != "" {
		objective, _ := data["objective"].(string)
		a.agentContext.SetCurrentTask(task, objective, source)
	}
}

func (a *Archivalist) applyCompletedSteps(data map[string]any) {
	steps := extractStringSlice(data, "completed_steps")
	for _, step := range steps {
		a.agentContext.CompleteStep(step)
	}
}

func (a *Archivalist) applyNextSteps(data map[string]any) {
	steps := extractStringSlice(data, "next_steps")
	if len(steps) > 0 {
		a.agentContext.SetNextSteps(steps)
	}
}

func (a *Archivalist) applyBlockers(data map[string]any) {
	blockers := extractStringSlice(data, "blockers")
	for _, blocker := range blockers {
		a.agentContext.AddBlocker(blocker)
	}
}

func extractStringSlice(data map[string]any, key string) []string {
	items, ok := data[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func (a *Archivalist) readScope(scope Scope, key string, limit int) any {
	reader := scopeReader(scope)
	if reader == nil {
		return nil
	}
	return reader(a, key, limit)
}

type scopeReaderFunc func(*Archivalist, string, int) any

type scopeReaderSpec struct {
	scope  Scope
	reader scopeReaderFunc
}

func scopeReader(scope Scope) scopeReaderFunc {
	for _, spec := range scopeReaders() {
		if spec.scope == scope {
			return spec.reader
		}
	}
	return nil
}

func scopeReaders() []scopeReaderSpec {
	return []scopeReaderSpec{
		{scope: ScopeFiles, reader: readFilesScope},
		{scope: ScopePatterns, reader: readPatternsScope},
		{scope: ScopeFailures, reader: readFailuresScope},
		{scope: ScopeIntents, reader: readIntentsScope},
		{scope: ScopeResume, reader: readResumeScope},
		{scope: ScopeAll, reader: readAllScope},
	}
}

func readFilesScope(a *Archivalist, key string, limit int) any {
	if key != "" {
		return a.agentContext.GetFileState(key)
	}
	return a.agentContext.GetModifiedFiles()
}

func readPatternsScope(a *Archivalist, key string, limit int) any {
	if key != "" {
		return a.agentContext.GetPattern(key)
	}
	return a.agentContext.GetAllPatterns()
}

func readFailuresScope(a *Archivalist, key string, limit int) any {
	return a.agentContext.GetRecentFailures(limit)
}

func readIntentsScope(a *Archivalist, key string, limit int) any {
	return map[string]any{
		"wants":   a.agentContext.GetUserWants(),
		"rejects": a.agentContext.GetUserRejects(),
	}
}

func readResumeScope(a *Archivalist, key string, limit int) any {
	return a.agentContext.GetResumeState()
}

func readAllScope(a *Archivalist, key string, limit int) any {
	return a.GetAgentBriefing()
}

func scopeToEventType(scope Scope, isNew bool) EventType {
	if scope == ScopeFiles {
		return fileScopeEventType(isNew)
	}
	if scope == ScopePatterns {
		return patternScopeEventType(isNew)
	}
	if scope == ScopeFailures {
		return EventTypeFailureRecord
	}
	if scope == ScopeIntents {
		return EventTypeIntentAdd
	}
	if scope == ScopeResume {
		return EventTypeResumeUpdate
	}
	return EventTypeEntryStore
}

func fileScopeEventType(isNew bool) EventType {
	if isNew {
		return EventTypeFileCreate
	}
	return EventTypeFileModify
}

func patternScopeEventType(isNew bool) EventType {
	if isNew {
		return EventTypePatternAdd
	}
	return EventTypePatternUpdate
}

// =============================================================================
// Concurrency Accessors
// =============================================================================

// GetRegistry returns the agent registry
func (a *Archivalist) GetRegistry() *Registry {
	return a.registry
}

func (a *Archivalist) GetEventLog() *EventLog {
	return a.eventLog
}

func (a *Archivalist) GetQueryCache() QueryCacheService {
	return a.queryCache
}

func (a *Archivalist) GetEmbeddings() EmbeddingStoreService {
	return a.embeddings
}

func (a *Archivalist) GetRetriever() SemanticRetrieverService {
	return a.retriever
}

func (a *Archivalist) GetSynthesizer() SynthesizerService {
	return a.synthesizer
}

func (a *Archivalist) GetMemory() MemoryManagerService {
	return a.memory
}

func (a *Archivalist) GetToolHandler() ToolHandlerService {
	return a.toolHandler
}

// GetConflictHistory returns the conflict history
func (a *Archivalist) GetConflictHistory() *ConflictHistory {
	return a.conflictHistory
}

// GetActiveAgents returns all currently active agents
func (a *Archivalist) GetActiveAgents() []*RegisteredAgent {
	return a.registry.GetActiveAgents()
}

// GetEventsSince returns events since a given version
func (a *Archivalist) GetEventsSince(version string) []*Event {
	return a.eventLog.GetSinceVersion(version)
}

// GetUnresolvedConflicts returns conflicts awaiting human resolution
func (a *Archivalist) GetUnresolvedConflicts() []*ConflictRecord {
	return a.conflictHistory.GetUnresolved()
}

// =============================================================================
// RAG Methods
// =============================================================================

// QueryContext performs a RAG query and returns synthesized answer
func (a *Archivalist) QueryContext(ctx context.Context, query string) (*SynthesisResponse, error) {
	if a.synthesizer == nil {
		return nil, fmt.Errorf("RAG not enabled")
	}

	sessionID := a.store.GetCurrentSession().ID
	queryType := ClassifyQuery(query)

	return a.synthesizer.Answer(ctx, query, sessionID, queryType)
}

// HandleToolCall processes a tool call from an agent
func (a *Archivalist) HandleToolCall(ctx context.Context, toolName string, input []byte) (string, error) {
	if a.toolRuntime() == nil {
		return "", fmt.Errorf("tool runtime is not configured")
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		return "", fmt.Errorf("tool name is required")
	}
	if _, err := a.toolRuntime().Activate(name); err != nil {
		return "", err
	}
	raw := strings.TrimSpace(string(input))
	if raw == "" {
		raw = "{}"
	}
	result, err := a.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall: providers.ToolCall{
			ID:        "archivalist-direct",
			Name:      name,
			Arguments: raw,
		},
		AgentID:         a.toolRuntime().AgentID(),
		CorrelationID:   a.id + "-direct",
		CapabilityScope: a.toolRuntime().CapabilityScope(),
	})
	if result.ToolDefsDirty {
		a.toolDefsDirty = true
	}
	return result.Output, err
}

// GetQueryCacheStats returns query cache statistics
func (a *Archivalist) GetQueryCacheStats() *QueryCacheStats {
	if a.queryCache == nil {
		return nil
	}
	stats := a.queryCache.Stats()
	return &stats
}

func (a *Archivalist) QueryCrossSession(ctx context.Context, query ArchiveQuery) ([]CrossSessionResult, error) {
	if a.crossSession == nil {
		return nil, fmt.Errorf("cross session index not available")
	}
	results, err := a.crossSession.QueryCrossSession(a.expandQueryForBoundedInfluence(query))
	if err != nil {
		return nil, err
	}
	entries := make([]*Entry, 0, len(results))
	for _, result := range results {
		entries = append(entries, result.Entry)
	}
	entries = a.applyBoundedCrossAgentInfluence(query, entries)
	cross := make([]CrossSessionResult, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		cross = append(cross, CrossSessionResult{
			SessionID: entry.SessionID,
			Entry:     entry,
		})
	}
	return cross, nil
}

func (a *Archivalist) QuerySessions(ctx context.Context, query ArchiveQuery) ([]*Session, error) {
	if a.crossSession == nil {
		return nil, fmt.Errorf("cross session index not available")
	}
	return a.crossSession.QuerySessions(query)
}

func (a *Archivalist) GetSessionHistory(ctx context.Context, sessionID string, limit int) []*Event {
	if a.crossSession == nil {
		return nil
	}
	return a.crossSession.GetSessionHistory(sessionID, limit)
}

// GetMemoryStats returns memory manager statistics
func (a *Archivalist) GetMemoryStats() *MemoryStats {
	if a.memory == nil {
		return nil
	}
	stats := a.memory.Stats()
	return &stats
}

// GetToolDefinitions returns all available tool definitions
func (a *Archivalist) GetToolDefinitions() []ToolDefinition {
	tools := a.buildToolDefinitions()
	if len(tools) == 0 {
		return nil
	}
	defs := make([]ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		defs = append(defs, ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.Parameters,
		})
	}
	return defs
}

// IndexContent adds content to the RAG retrieval index
func (a *Archivalist) IndexContent(ctx context.Context, id, content, category, contentType string) error {
	if a.retriever == nil {
		return fmt.Errorf("RAG not enabled")
	}

	sessionID := a.store.GetCurrentSession().ID
	return a.retriever.Index(ctx, id, content, category, contentType, sessionID, nil)
}

// InvalidateQueryCache invalidates cached queries by type
func (a *Archivalist) InvalidateQueryCache(queryType QueryType) {
	if a.queryCache != nil {
		a.queryCache.InvalidateByType(queryType)
	}
}

// CleanupRAG performs cleanup of RAG components
func (a *Archivalist) CleanupRAG() {
	if a.queryCache != nil {
		a.queryCache.Cleanup()
	}
}

// =============================================================================
// Skills and Hooks API
// =============================================================================

// Skills returns the Archivalist's skill registry
func (a *Archivalist) Skills() *skills.Registry {
	return a.skills
}

// SkillLoader returns the Archivalist's skill loader
func (a *Archivalist) SkillLoader() *skills.Loader {
	return a.skillLoader
}

// Hooks returns the Archivalist's hook registry
func (a *Archivalist) Hooks() *skills.HookRegistry {
	return a.hooks
}

// RegisterSkill registers a skill with the Archivalist's skill registry
func (a *Archivalist) RegisterSkill(skill *skills.Skill) error {
	if skill == nil {
		return fmt.Errorf("skill is required")
	}
	name := strings.TrimSpace(skill.Name)
	if name == "" {
		return fmt.Errorf("skill name is required")
	}
	if a.toolRuntime() == nil || !a.toolRuntime().Allows(name) {
		return fmt.Errorf("skill %q is outside archivalist capability scope %q", name, archivalistToolManifest(a.skills).CapabilityScope)
	}
	if err := a.skills.Register(skill); err != nil {
		return err
	}
	a.toolDefsDirty = true
	return nil
}

// LoadSkillsForInput loads skills based on input keywords
func (a *Archivalist) LoadSkillsForInput(input string) []string {
	return a.skillLoader.LoadForInput(input)
}

// GetLoadedSkillDefinitions returns tool definitions for all loaded skills
func (a *Archivalist) GetLoadedSkillDefinitions() []map[string]any {
	return shared.ProviderToolsToDefinitions(a.buildToolDefinitions())
}

// RegisterPrePromptHook registers a hook that runs before LLM prompts
func (a *Archivalist) RegisterPrePromptHook(name string, priority skills.HookPriority, fn skills.PromptHookFunc) {
	a.hooks.RegisterPrePromptHook(name, priority, fn)
}

// RegisterPostPromptHook registers a hook that runs after LLM responses
func (a *Archivalist) RegisterPostPromptHook(name string, priority skills.HookPriority, fn skills.PromptHookFunc) {
	a.hooks.RegisterPostPromptHook(name, priority, fn)
}

// ExecutePrePromptHooks runs all pre-prompt hooks
func (a *Archivalist) ExecutePrePromptHooks(ctx context.Context, data *skills.PromptHookData) (*skills.PromptHookData, error) {
	return a.hooks.ExecutePrePromptHooks(ctx, data)
}

// ExecutePostPromptHooks runs all post-prompt hooks
func (a *Archivalist) ExecutePostPromptHooks(ctx context.Context, data *skills.PromptHookData) (*skills.PromptHookData, error) {
	return a.hooks.ExecutePostPromptHooks(ctx, data)
}

func (a *Archivalist) RegisterPreStoreHook(name string, priority skills.HookPriority, fn skills.StoreHookFunc) {
	a.hooks.RegisterPreStoreHook(name, priority, fn)
}

func (a *Archivalist) RegisterPostStoreHook(name string, priority skills.HookPriority, fn skills.StoreHookFunc) {
	a.hooks.RegisterPostStoreHook(name, priority, fn)
}

func (a *Archivalist) RegisterPreQueryHook(name string, priority skills.HookPriority, fn skills.QueryHookFunc) {
	a.hooks.RegisterPreQueryHook(name, priority, fn)
}

func (a *Archivalist) RegisterPostQueryHook(name string, priority skills.HookPriority, fn skills.QueryHookFunc) {
	a.hooks.RegisterPostQueryHook(name, priority, fn)
}

func (a *Archivalist) ExecutePreStoreHooks(ctx context.Context, data *skills.StoreHookData) (*skills.StoreHookData, skills.HookResult, error) {
	return a.hooks.ExecutePreStoreHooks(ctx, data)
}

func (a *Archivalist) ExecutePostStoreHooks(ctx context.Context, data *skills.StoreHookData) (*skills.StoreHookData, skills.HookResult, error) {
	return a.hooks.ExecutePostStoreHooks(ctx, data)
}

func (a *Archivalist) ExecutePreQueryHooks(ctx context.Context, data *skills.QueryHookData) (*skills.QueryHookData, skills.HookResult, error) {
	return a.hooks.ExecutePreQueryHooks(ctx, data)
}

func (a *Archivalist) ExecutePostQueryHooks(ctx context.Context, data *skills.QueryHookData) (*skills.QueryHookData, skills.HookResult, error) {
	return a.hooks.ExecutePostQueryHooks(ctx, data)
}

// =============================================================================
// Handoff Interface (ContextEvictable)
// =============================================================================

// AgentID returns the unique identifier for this agent instance.
func (a *Archivalist) AgentID() string {
	return a.id
}

// AgentType returns the type classification for this agent.
func (a *Archivalist) AgentType() string {
	return "archivalist"
}

// SetCanonicalID preserves the routing identity across handoff replacement.
func (a *Archivalist) SetCanonicalID(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	a.id = id
}

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (a *Archivalist) SetProvider(p archivalistProvider) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.provider = p
	a.client = NewClient(ClientConfig{
		Provider:        p,
		Model:           a.config.Model,
		SystemPrompt:    a.config.SystemPrompt,
		MaxOutputTokens: a.config.MaxOutputTokens,
	})
}

// SetKnowledgeStore wires the archivalist to use the KnowledgeStore's
// coordinator instead of a locally-built one. The coordinator's atomic
// searchers are set progressively as boot phases complete.
func (a *Archivalist) SetKnowledgeStore(ks *knowledge.KnowledgeStore) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.knowledgeStore = ks
	a.queryCoordinator = ks.Coordinator()
}

// SetKnowledgeBackend wires the archivalist to the committed-global retrieval
// backend used for repository head search and fetched-document recall.
func (a *Archivalist) SetKnowledgeBackend(backend committedKnowledgeSearcher) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.knowledgeBackend = backend
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (a *Archivalist) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	a.workspaceViews = authority.RestrictWorkspaceViews("archivalist", views)
}

// SwapModel implements container.ModelSwappable.
func (a *Archivalist) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.provider = provider
	a.config.Model = modelID
	a.client = NewClient(ClientConfig{
		Provider:        provider,
		Model:           modelID,
		SystemPrompt:    a.config.SystemPrompt,
		MaxOutputTokens: a.config.MaxOutputTokens,
	})
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (a *Archivalist) CurrentModel() string {
	a.runMu.RLock()
	defer a.runMu.RUnlock()
	return a.config.Model
}

func (a *Archivalist) currentProviderAutoscaleSnapshot() shared.RequestReplicaProviderSnapshot {
	a.runMu.RLock()
	provider := a.provider
	a.runMu.RUnlock()
	return shared.RequestReplicaProviderSnapshotFromProvider(provider)
}

// SupportedModels implements container.ModelSwappable.
func (a *Archivalist) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
	}
}

// Compile-time interface check.
var _ container.ModelSwappable = (*Archivalist)(nil)

// Descriptor returns the immutable metadata describing this agent type.
func (a *Archivalist) Descriptor() handoff.AgentDescriptor {
	modelID := a.config.Model
	return handoff.AgentDescriptor{
		AgentType:             "archivalist",
		ModelID:               modelID,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryKnowledge,
		RuntimeProfiles:       archivalistRuntimeProfiles(),
		DefaultRuntimeProfile: archivalistDefaultRuntimeProfile(),
	}
}

// EvictEntries frees context by removing low-value entries from the working set.
// Returns the total number of tokens freed across all evicted candidates.
func (a *Archivalist) EvictEntries(candidates []handoff.EvictionCandidate) (freedTokens int, err error) {
	total := 0
	for _, candidate := range candidates {
		total += candidate.Entry.GetTokenCount()
	}
	return total, nil
}

// Terminate gracefully shuts down the agent.
func (a *Archivalist) Terminate(ctx context.Context) error {
	return a.Stop()
}

// SetHandoffBridge assigns the handoff bridge for this agent.
func (a *Archivalist) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	a.handoffBridge = bridge
	if bridge != nil {
		bridge.SetActivityPublisher(a.activityPub)
	}
}

// ExtractArchivableState returns the agent's current state for handoff persistence.
func (a *Archivalist) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   a.AgentID(),
		AgentType: a.AgentType(),
	}
}
