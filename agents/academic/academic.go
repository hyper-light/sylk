// Package academic implements the Academic agent for external knowledge research.
// The Academic researches best practices, papers, and external sources, always
// validating recommendations against codebase reality via the Librarian.
package academic

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge"
	knowledgequery "github.com/adalundhe/sylk/core/knowledge/query"
	"github.com/adalundhe/sylk/core/knowledgeruntime"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// Default configuration values.
const (
	DefaultModel              = "gpt-5.4-pro"
	DefaultMaxToolRuns        = 32
	DefaultReplicaCount       = 4
	DefaultReplicaBacklog     = 16
	defaultResearchCacheLimit = 128
	defaultSourceIndexLimit   = 2048
)

const academicChildProgressMessage = "Researching external sources and codebase fit."

var academicChildProgressKeepaliveInterval = 30 * time.Second

// academicProvider is the minimal interface the Academic needs from its LLM.
// Satisfied by *providers.AnthropicProvider.
type academicProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

type committedKnowledgeSearcher interface {
	Search(ctx context.Context, req *search.SearchRequest) (*knowledgeruntime.CommittedSearchResult, error)
}

// Academic is the main agent for researching external knowledge and best practices.
// It uses Claude Opus 4.5 for complex reasoning and synthesis of research findings.
type Academic struct {
	id           string
	config       Config
	domainFilter *AcademicDomainFilter
	logger       *slog.Logger
	identity     *identity.AgentIdentity
	factory      *identity.Factory

	// LLM provider
	provider   academicProvider
	providerMu sync.RWMutex
	refresher  container.ProviderRefresher

	// Skills system
	skills        *skills.Registry
	skillLoader   *skills.Loader
	hooks         *skills.HookRegistry
	tools         *toolruntime.Runtime
	toolDefsDirty bool

	// Activity publisher for UI agent-panel updates.
	activityPub events.ActivityPublisher

	// Event bus integration
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Request-scoped context lifecycle (mirrors engineer pattern).
	runCtx         context.Context
	runCancel      context.CancelFunc
	requestMu      sync.Mutex
	requestCancels map[string]context.CancelFunc

	// Steering ledger management.
	steering *shared.SteeringManager

	// Forwarded-request admission for bounded parallel knowledge replicas.
	requestPool *shared.RequestReplicaPool

	// Synchronous consultation bus (Librarian responses).
	pendingMu       sync.Mutex
	pendingConsults map[string]*shared.PendingSyncWait

	// Research state
	mu            sync.RWMutex
	researchCache map[string]*ResearchResult
	sourceIndex   map[string]*Source

	// Knowledge retrieval state.
	knowledgeMu      sync.RWMutex
	knowledgeStore   *knowledge.KnowledgeStore
	knowledgeBackend committedKnowledgeSearcher
	queryCoordinator *knowledgequery.HybridQueryCoordinator

	// Outcome tracking for maturity-aware recommendations
	outcomeHistory *OutcomeHistory
	webSearchModel *academicWebSearchLearner

	// External fetch pipeline — wired via SetFetchPipeline after construction.
	fetchPipeline *fetch.Pipeline

	forestTracker *shared.MemoryForestTracker

	workspaceViews versioning.WorkspaceViewAccess

	// Handoff integration
	handoffBridge *handoff.HandoffBridge
}

// routeHopsKey carries the incoming ForwardedRequest's hop count through the
// Academic request context so child consult RouteRequests preserve the full
// routing chain just like Architect.
type routeHopsKey struct{}

func withRouteHops(ctx context.Context, hops int) context.Context {
	return context.WithValue(ctx, routeHopsKey{}, hops)
}

func routeHopsFromContext(ctx context.Context) int {
	hops, _ := ctx.Value(routeHopsKey{}).(int)
	return hops
}

// Config holds configuration for the Academic agent.
type Config struct {
	// Canonical agent ID. If empty, defaults to "academic".
	ID string

	// Anthropic API configuration
	AnthropicAPIKey string
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Model configuration
	Model       string // LLM model ID (default: gpt-5.4-pro)
	MaxToolRuns int    // Maximum tool loop iterations (default: 12)

	// ActivityPub publishes activity events so the UI agent panel tracks
	// this agent's lifecycle. Nil-safe (events silently dropped).
	ActivityPub events.ActivityPublisher

	// RequestGuard is called at handler entry to prevent activation demotion
	// during in-flight processing. Returns a release function. Nil-safe.
	RequestGuard func() func()

	// Session context
	SessionID string

	// Research configuration
	MaxSources                  int           // Max sources to consult per query (default: 10)
	CacheExpiry                 time.Duration // How long to cache results (default: 30m)
	ResearchCacheLimit          int           // Maximum retained research results (default: 128)
	SourceIndexLimit            int           // Maximum retained source records (default: 2048)
	LibrarianTimeout            time.Duration // Timeout for Librarian consultation (default: 30s)
	RequireLibrarian            bool          // Require Librarian validation (default: true)
	DisableSearchDiscipline     bool          // When true, allows Academic to answer without an observed native web search.
	SearchDisciplineMaxAttempts int           // Extra reminder turns before failing a required-search request (default: 2)
	MaxNativeWebSearchCalls     int           // Maximum provider-native web_search calls allowed in a single Academic turn (default: 6)
	MemoryThreshold             MemoryThreshold
	DefaultConfidence           ConfidenceLevel
	MinApplicability            float64 // Minimum applicability score to include (default: 0.3)
	OutcomeHistoryLimit         int     // Max outcomes to track (default: 1000)

	// Logger
	Logger *slog.Logger

	// WorkspaceViews provides explicit disk/global/pipeline read access for
	// codebase-aware research grounding.
	WorkspaceViews versioning.WorkspaceViewAccess

	// Forwarded-request admission controls for bounded replica concurrency.
	MaxConcurrentForwarded int
	MaxQueuedForwarded     int

	// ContextQuota exposes aggregate runtime pressure so autoscaling can
	// respect system headroom without user configuration.
	ContextQuota *container.ResourceQuota

	// Forest exposes role-shaped Memory Forest skills to the Academic.
	Forest shared.MemoryForestService

	// Factory mints the Academic's AgentIdentity at New() time.
	// Academic is an on-demand agent created after phase4, so Factory
	// should be populated at construction.
	Factory *identity.Factory
}

// New creates a new Academic agent with the given LLM provider.
func New(cfg Config, provider academicProvider) (*Academic, error) {
	cfg = applyConfigDefaults(cfg)

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create hook registry
	hookRegistry := skills.NewHookRegistry()

	academicID := cfg.ID
	if academicID == "" {
		academicID = uuid.New().String()[:8]
	}

	// Create skills registry — register skills BEFORE creating the loader.
	// NewLoader calls loadCoreSkills() immediately, so skills must exist
	// in the registry for the load-by-name to succeed.
	skillsRegistry := skills.NewRegistry()

	a := &Academic{
		id:              academicID,
		config:          cfg,
		domainFilter:    NewAcademicDomainFilter(logger),
		logger:          logger,
		provider:        provider,
		activityPub:     cfg.ActivityPub,
		skills:          skillsRegistry,
		hooks:           hookRegistry,
		knownAgents:     make(map[string]*guide.AgentAnnouncement),
		pendingConsults: make(map[string]*shared.PendingSyncWait),
		researchCache:   make(map[string]*ResearchResult),
		sourceIndex:     make(map[string]*Source),
		outcomeHistory:  NewOutcomeHistory(cfg.OutcomeHistoryLimit),
		webSearchModel:  newAcademicWebSearchLearner(),
		steering:        shared.NewSteeringManager(),
		forestTracker:   shared.NewMemoryForestTracker(),
		workspaceViews:  authority.RestrictWorkspaceViews("academic", cfg.WorkspaceViews),
	}
	a.requestPool = shared.NewAutoscalingRequestReplicaPool(shared.RequestReplicaPoolConfig{
		Name:              "academic",
		HardMaxActive:     cfg.MaxConcurrentForwarded,
		HardMaxQueued:     cfg.MaxQueuedForwarded,
		CurrentModel:      a.CurrentModel,
		ProviderTelemetry: a.currentProviderAutoscaleSnapshot,
		ContextQuota:      cfg.ContextQuota,
	})

	a.factory = cfg.Factory
	academicIdentity, err := cfg.Factory.Mint(identity.MintOptions{
		Kind: identity.AgentTypeAcademic,
		Pod:  identity.PodRef{ID: "academic", Type: identity.PodTypeSingleton},
	})
	if err != nil {
		return nil, fmt.Errorf("academic: mint identity: %w", err)
	}
	a.identity = academicIdentity

	a.steering.InitLazy("academic", cfg.ActivityPub)

	a.registerCoreSkills()
	a.registerExtendedSkills()
	a.registerFetchSkills()
	if err := shared.RegisterMemoryForestSkills(a.skills, "academic", cfg.Forest, a.forestTracker); err != nil {
		return nil, fmt.Errorf("register academic forest skills: %w", err)
	}

	skillsLoaderCfg := skills.DefaultLoaderConfig()
	skillsLoaderCfg.CoreSkills = academicVisibleSkillNames()
	skillsLoaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	a.skillLoader = skills.NewLoader(skillsRegistry, skillsLoaderCfg)

	tools, err := toolruntime.New(toolruntime.Config{
		Registry: a.skills,
		Hooks:    a.hooks,
		Manifest: academicToolManifest(a.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize academic tool runtime: %w", err)
	}
	a.tools = tools
	a.tools.SyncActiveFromLoaded()

	return a, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	cfg.SystemPrompt = shared.AppendNoFilesystemContext(cfg.SystemPrompt)
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.MaxToolRuns == 0 {
		cfg.MaxToolRuns = DefaultMaxToolRuns
	}
	if cfg.MaxSources == 0 {
		cfg.MaxSources = 10
	}
	if cfg.CacheExpiry == 0 {
		cfg.CacheExpiry = 30 * time.Minute
	}
	if cfg.ResearchCacheLimit <= 0 {
		cfg.ResearchCacheLimit = defaultResearchCacheLimit
	}
	if cfg.SourceIndexLimit <= 0 {
		cfg.SourceIndexLimit = defaultSourceIndexLimit
	}
	if cfg.LibrarianTimeout == 0 {
		cfg.LibrarianTimeout = 30 * time.Second
	}
	if cfg.SearchDisciplineMaxAttempts <= 0 {
		cfg.SearchDisciplineMaxAttempts = 2
	}
	if cfg.MaxNativeWebSearchCalls <= 0 {
		cfg.MaxNativeWebSearchCalls = 6
	}
	if cfg.MemoryThreshold.CheckpointThreshold == 0 {
		cfg.MemoryThreshold = DefaultMemoryThreshold()
	}
	if cfg.DefaultConfidence == "" {
		cfg.DefaultConfidence = ConfidenceLevelMedium
	}
	if cfg.MinApplicability == 0 {
		cfg.MinApplicability = 0.3
	}
	if cfg.OutcomeHistoryLimit == 0 {
		cfg.OutcomeHistoryLimit = 1000
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

// Close closes the Academic agent and its resources.
func (a *Academic) Close() error {
	if a.tools != nil {
		a.tools.Close()
		a.tools = nil
	}
	a.Stop()
	if a.requestPool != nil {
		a.requestPool.Close()
	}
	return nil
}

// =============================================================================
// Provider Management
// =============================================================================

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (a *Academic) SetProvider(p academicProvider) {
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	a.provider = p
}

// getProvider returns the current provider under read lock.
func (a *Academic) getProvider() academicProvider {
	a.providerMu.RLock()
	defer a.providerMu.RUnlock()
	return a.provider
}

// Ready implements shared.ReadinessReporter.
func (a *Academic) Ready() (bool, string) {
	if a.getProvider() == nil {
		return false, "LLM provider not yet wired (post-init / pre-auth window)"
	}
	return true, ""
}

// SetProviderRefresher stores a callback that creates a fresh provider for
// the current model and auth method. Set by cmd/tui.go at bootstrap.
func (a *Academic) SetProviderRefresher(fn container.ProviderRefresher) {
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	a.refresher = fn
}

// ProviderType implements container.AuthRefreshable.
func (a *Academic) ProviderType() string {
	return string(container.ProviderForModel(a.CurrentModel()))
}

// RefreshProvider implements container.AuthRefreshable.
func (a *Academic) RefreshProvider(ctx context.Context, authMethod string) error {
	a.providerMu.RLock()
	fn := a.refresher
	a.providerMu.RUnlock()
	if fn == nil {
		return nil
	}
	p, err := fn(ctx, a.CurrentModel(), authMethod)
	if err != nil {
		return fmt.Errorf("academic refresh provider: %w", err)
	}
	a.SetProvider(p)
	a.logger.Info("provider refreshed", "model", a.CurrentModel(), "auth_method", authMethod)
	return nil
}

// SwapModel implements container.ModelSwappable.
func (a *Academic) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	a.SetProvider(provider)
	a.providerMu.Lock()
	a.config.Model = modelID
	a.providerMu.Unlock()
	a.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (a *Academic) CurrentModel() string {
	a.providerMu.RLock()
	defer a.providerMu.RUnlock()
	if a.config.Model != "" {
		return a.config.Model
	}
	return DefaultModel
}

func (a *Academic) currentProviderAutoscaleSnapshot() shared.RequestReplicaProviderSnapshot {
	return shared.RequestReplicaProviderSnapshotFromProvider(a.getProvider())
}

// SupportedModels implements container.ModelSwappable.
func (a *Academic) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
		{ID: "gpt-5.4-pro", DisplayName: "GPT-5.4 Pro"},
	}
}

// Compile-time interface checks.
var (
	_ container.AuthRefreshable = (*Academic)(nil)
	_ container.ModelSwappable  = (*Academic)(nil)
)

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The Academic subscribes to its own channels and the registry topic.
func (a *Academic) Start(bus guide.EventBus) error {
	if a.running {
		return fmt.Errorf("academic is already running")
	}

	a.bus = bus
	a.channels = guide.NewAgentChannels("academic", a.id)

	// Subscribe to own request channel (academic.requests)
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

	a.runCtx, a.runCancel = context.WithCancel(context.Background())
	a.requestCancels = make(map[string]context.CancelFunc)
	a.running = true
	a.logger.Info("academic agent started",
		"request_channel", a.channels.Requests,
		"response_channel", a.channels.Responses,
	)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (a *Academic) Stop() error {
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
	a.logger.Info("academic agent stopped")
	return nil
}

func (a *Academic) unsubscribeAll() []error {
	var errs []error
	if err := a.unsubscribeSafe(a.requestSub); err != nil {
		errs = append(errs, err)
	}
	a.requestSub = nil
	if err := a.unsubscribeSafe(a.responseSub); err != nil {
		errs = append(errs, err)
	}
	a.responseSub = nil
	if err := a.unsubscribeSafe(a.registrySub); err != nil {
		errs = append(errs, err)
	}
	a.registrySub = nil
	return errs
}

func (a *Academic) unsubscribeSafe(sub guide.Subscription) error {
	if sub == nil {
		return nil
	}
	return sub.Unsubscribe()
}

// IsRunning returns true if the Academic is actively processing bus messages.
func (a *Academic) IsRunning() bool {
	return a.running
}

// Bus returns the event bus used by the Academic.
func (a *Academic) Bus() guide.EventBus {
	return a.bus
}

// Channels returns the Academic's channel configuration.
func (a *Academic) Channels() *guide.AgentChannels {
	return a.channels
}

// =============================================================================
// Request Handling
// =============================================================================

// handleBusRequest processes incoming forwarded requests from the event bus.
func (a *Academic) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		return a.handleActionMessage(msg)
	}
	if msg.Type != guide.MessageTypeForward {
		return nil // Ignore non-forward messages
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	a.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(a.steering.EventLogger(), fwd, "academic")

	shared.EmitDispatchACK(a.bus, fwd.Metadata, "academic", "academic", fwd.CorrelationID)
	a.publishActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentAction, "Processing research request")

	if a.config.RequestGuard != nil {
		release := a.config.RequestGuard()
		defer release()
	}

	// Process the request with a cancellable request-scoped context.
	reqCtx, cancel := context.WithCancel(a.runCtx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	reqCtx = shared.WithForwardedTaskScope(reqCtx, fwd.Metadata)
	reqCtx = shared.WithGuardianCommandGate(reqCtx, shared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return a.bus },
		SourceAgentID:   func() string { return a.id },
		SourceAgentType: "academic",
		SourceAgentName: "Academic",
	})
	a.registerRequestCancel(fwd.CorrelationID, cancel)
	a.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer a.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	if a.factory != nil && a.identity != nil {
		task, taskErr := a.factory.NewTask(identity.TaskOptions{
			DisplayID:   fwd.CorrelationID,
			Correlation: identity.CorrelationID(fwd.CorrelationID),
		})
		if taskErr != nil {
			return fmt.Errorf("academic: mint task: %w", taskErr)
		}
		reqCtx = identity.WithIdentity(reqCtx, a.identity)
		reqCtx = identity.WithTask(reqCtx, task)
	}

	// Create steering ledger for this request.
	ledger := a.steering.Create(fwd.CorrelationID, "academic", fwd.SessionID, nil, nil)
	defer a.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	startTime := time.Now()

	// Wire tool call emitter for inline visualization.
	emitter := shared.NewToolCallEmitter(a.bus, a.channels, a.id, fwd.CorrelationID, fwd.SourceAgentID)
	reqCtx = withRouteHops(reqCtx, fwd.Hops)
	ctx := shared.WithForwardedStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, fwd.Metadata)
	ctx = shared.WithOwnedStreamIdentity(ctx, "academic", "Academic")
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	ctx = shared.WithToolCallEmitter(ctx, emitter)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: a.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     "academic",
		SessionID:   fwd.SessionID,
	})
	ctx = shared.WithAutomaticHandoffEnabled(ctx, shared.AutomaticHandoffAllowedForForwardedRequest(fwd))
	gov := shared.NewContextGovernor(a.config.Model, a.config.MaxOutputTokens, 0)
	if a.handoffBridge != nil && shared.AutomaticHandoffEnabled(ctx) {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			bridge := shared.EffectiveHandoffBridge(bctx, a.handoffBridge)
			if bridge == nil {
				return shared.ErrContextBudgetExhausted
			}
			return bridge.ForceHandoff(bctx, "context budget exhausted")
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
			a.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentAction, "Waiting for an available research replica", snapshot)
			if pp := shared.ProgressPublisherFromContext(ctx); pp != nil {
				pp.PublishState(events.AgentUIStateSearching, shared.KnowledgeQueueProgressMessage("academic", snapshot, queuePosition))
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
			a.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentAction, "Processing research request", snapshot)
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
				Data:                shared.BusyRouteResponseData("academic", busyErr.Snapshot, busyErr.Error()),
				Error:               busyErr.Error(),
				RespondingAgentID:   a.id,
				RespondingAgentName: "Academic",
			}
			return a.bus.Publish(a.channels.Responses, guide.NewResponseMessage(generateMessageID(), resp))
		}
		return acquireErr
	}

	if a.handoffBridge != nil && shared.AutomaticHandoffEnabled(ctx) {
		var replicaErr error
		var cleanupReplicaBridge func()
		ctx, cleanupReplicaBridge, _, replicaErr = shared.AttachReplicaHandoffBridge(ctx, a, a.handoffBridge, shared.KnowledgeReplicaHandoffOptions{
			SessionID:         fwd.SessionID,
			CorrelationID:     fwd.CorrelationID,
			ActivityPublisher: a.activityPub,
			Factory:           a.factory,
			ParentIdentity:    a.identity,
		})
		if replicaErr != nil {
			lease.Release()
			return replicaErr
		}
		defer cleanupReplicaBridge()
	}
	ctx = handoff.WithTransportRetryHandoff(ctx, handoff.TransportRetryHandoffConfig{
		Enabled:       shared.AutomaticHandoffEnabled(ctx),
		Bridge:        shared.EffectiveHandoffBridge(ctx, a.handoffBridge),
		AgentID:       a.id,
		AgentType:     "academic",
		UserRequest:   fwd.Input,
		CorrelationID: fwd.CorrelationID,
		SessionID:     fwd.SessionID,
		EventLogger:   a.steering.EventLogger(),
	})

	bundle, err := a.newForwardedToolBundle()
	if err != nil {
		lease.Release()
		return err
	}
	defer bundle.Close()

	ctx = withAcademicToolBundle(ctx, bundle)
	stopProgressKeepalive := a.startChildProgressKeepalive(ctx, fwd)
	defer stopProgressKeepalive()

	result, err := a.processForwardedRequest(ctx, fwd)
	snapshot := lease.ReleaseWithObservation(shared.RequestReplicaObservation{
		Duration:    time.Since(startTime),
		TotalTokens: shared.TotalUsageTokens(usageAcc.Total()),
		Successful:  err == nil,
	})
	shared.LogResponse(a.steering.EventLogger(), fwd.CorrelationID, "academic", fwd.SessionID, time.Since(startTime), err)

	if err != nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		if publishStreamLifecycle {
			shared.PublishStreamError(a.bus, a.channels, ctx, a.id, err)
			shared.PublishStreamComplete(a.bus, a.channels, ctx, a.id, "", usageAcc.Total())
		}
		if fwd.FireAndForget {
			return nil
		}
		a.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentError, fmt.Sprintf("Research failed: %s", err.Error()), snapshot)
		errMsg := guide.NewErrorMessage(
			generateMessageID(),
			fwd.CorrelationID,
			"academic",
			err.Error(),
		)
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
		RespondingAgentName: "Academic",
		ProcessingTime:      time.Since(startTime),
		Data:                result,
	}
	a.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeSuccess, "Research task completed", snapshot)

	respMsg := guide.NewResponseMessage(generateMessageID(), resp)
	return a.bus.Publish(a.channels.Responses, respMsg)
}

func generateMessageID() string {
	return fmt.Sprintf("academic_msg_%s", uuid.New().String()[:8])
}

func (a *Academic) startChildProgressKeepalive(ctx context.Context, fwd *guide.ForwardedRequest) func() {
	if fwd == nil || fwd.FireAndForget || llmruntime.IsUserFacingSource(fwd.SourceAgentID) {
		return func() {}
	}
	pp := shared.ProgressPublisherFromContext(ctx)
	if pp == nil {
		return func() {}
	}

	pp.PublishState(events.AgentUIStateSearching, academicChildProgressMessage)

	interval := academicChildProgressKeepaliveInterval
	if interval <= 0 {
		return func() {}
	}

	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				pp.PublishState(events.AgentUIStateSearching, "")
			}
		}
	}()

	return func() {
		once.Do(func() { close(stop) })
	}
}

func (a *Academic) registerRequestCancel(correlationID string, cancel context.CancelFunc) {
	a.requestMu.Lock()
	if a.requestCancels != nil {
		a.requestCancels[correlationID] = cancel
	}
	a.requestMu.Unlock()
}

func (a *Academic) clearRequestCancel(correlationID string) {
	a.requestMu.Lock()
	delete(a.requestCancels, correlationID)
	a.requestMu.Unlock()
}

func (a *Academic) cancelRequest(correlationID string) {
	a.requestMu.Lock()
	cancel := a.requestCancels[correlationID]
	delete(a.requestCancels, correlationID)
	a.requestMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *Academic) handleActionMessage(msg *guide.Message) error {
	action, ok := msg.GetActionRequest()
	if !ok || action == nil {
		return nil
	}
	if a.steering.HandleAction(action) {
		return nil
	}
	if action.Action == "cancel" {
		shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventError,
			"academic", "", action.CorrelationID, "warn",
			&agentlog.ErrorPayload{Error: "request cancelled via action"})
		a.cancelRequest(action.CorrelationID)
	}
	return nil
}

// processForwardedRequest handles the actual request processing.
func (a *Academic) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	ctx = WithAcademicResearchDepth(ctx, academicResearchDepthFromForwarded(fwd))
	if a.shouldHandleConversation(fwd) {
		return a.handleConversation(ctx, fwd)
	}
	if a.shouldHandleChildConsultation(fwd) {
		return a.handleConsultation(ctx, fwd)
	}
	handler, err := a.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

func (a *Academic) shouldHandleConversation(fwd *guide.ForwardedRequest) bool {
	if fwd == nil {
		return false
	}
	if academicConversationHandoffFromForwarded(fwd) == nil {
		if strings.TrimSpace(strings.ToLower(fwd.SourceAgentID)) != "tui" {
			return false
		}
	}
	switch fwd.Intent {
	case guide.IntentRecall, guide.IntentCheck, guide.IntentFetch, guide.IntentHelp, guide.IntentChat, guide.IntentUnknown:
		return true
	default:
		return false
	}
}

func (a *Academic) shouldHandleChildConsultation(fwd *guide.ForwardedRequest) bool {
	if fwd == nil {
		return false
	}
	if academicConversationHandoffFromForwarded(fwd) != nil {
		return false
	}
	if strings.TrimSpace(strings.ToLower(fwd.SourceAgentID)) == "tui" {
		return false
	}
	switch fwd.Intent {
	case guide.IntentRecall, guide.IntentCheck, guide.IntentFetch, guide.IntentChat, guide.IntentUnknown:
		return true
	default:
		return false
	}
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (a *Academic) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentRecall:
		return a.handleRecall, nil
	case guide.IntentCheck:
		return a.handleCheck, nil
	case guide.IntentFetch:
		return a.handleFetch, nil
	case guide.IntentHelp:
		return a.handleHelp, nil
	case guide.IntentChat, guide.IntentUnknown:
		return a.handleConversation, nil
	default:
		return nil, fmt.Errorf("unsupported intent for academic: %s", intent)
	}
}

func (a *Academic) handleConversation(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	ctx = a.withRequestResearchExecutionState(ctx, fwd)
	if contract := academicCompletionContractForForwardedRequest(fwd); contract != nil {
		ctx = WithAcademicCompletionContract(ctx, contract)
	}
	query := strings.TrimSpace(fwd.Input)
	if query == "" {
		return &ConversationResult{
			Response: "Tell me what you want researched or checked, and I’ll work through it with you.",
			Intent:   fwd.Intent,
		}, nil
	}

	handoff := academicConversationHandoffFromForwarded(fwd)
	toolSurface := a.requestToolSurfaceForForwardedRequest(ctx, fwd, academicConversationExtraTools(handoff)...)
	academicPrepareSkillsForInput(a, ctx, query)
	llmReq := &providers.Request{
		SystemPrompt: buildAcademicConversationSystemPrompt(a.config.SystemPrompt, handoff, AcademicResearchDepthFromContext(ctx)),
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: query}},
		Tools:        a.buildToolDefinitionsWithSurface(toolSurface),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(ctx, llmReq, "conversation")
	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, toolSurface)
	})
	if err != nil {
		return nil, fmt.Errorf("academic conversation failed: %w", err)
	}
	return &ConversationResult{
		Response: result,
		Intent:   fwd.Intent,
	}, nil
}

func (a *Academic) handleConsultation(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	ctx = a.withConsultationTurnState(ctx, fwd)
	query := strings.TrimSpace(fwd.Input)
	if query == "" {
		return &shared.ConsultResponsePayload{
			Summary: "Tell me what you want researched or checked, and I’ll work through it.",
		}, nil
	}

	toolSurface := a.requestToolSurfaceForForwardedRequest(ctx, fwd, academicForwardedResearchExtraTools(fwd)...)
	academicPrepareSkillsForInput(a, ctx, query)
	llmReq := &providers.Request{
		SystemPrompt: buildAcademicConsultationSystemPrompt(a.config.SystemPrompt, fwd, AcademicResearchDepthFromContext(ctx)),
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: query}},
		Tools:        a.buildToolDefinitionsWithSurface(toolSurface),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(ctx, llmReq, "conversation")
	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, toolSurface)
	})
	if err != nil {
		return nil, fmt.Errorf("academic consultation failed: %w", err)
	}
	// Typed inter-agent response: parse the LLM output into the canonical
	// ConsultResponsePayload struct so downstream consumers (chat UI row
	// renderer, protocol validators, archivalist loggers) read declared
	// fields instead of defensively parsing JSON. Falls back to Summary=
	// raw text when the LLM emitted prose rather than JSON — that case
	// still renders meaningfully in the chat row and keeps the migration
	// backwards-compatible with older prompts.
	return shared.DecodeConsultResponsePayloadFromLLM(result), nil
}

type academicConversationHandoff struct {
	Kind             string
	From             string
	Reason           string
	ResearchGoal     string
	ConversationHist string
	KnownContext     []string
	MissingQuestions []string
}

func academicConversationHandoffFromForwarded(fwd *guide.ForwardedRequest) *academicConversationHandoff {
	if fwd == nil || fwd.Metadata == nil {
		return nil
	}
	userFacing, _ := fwd.Metadata["user_facing_handoff"].(bool)
	if !userFacing {
		return nil
	}
	handoff := &academicConversationHandoff{
		Kind:             metadataTrimmedString(fwd.Metadata, "handoff_kind"),
		From:             metadataTrimmedString(fwd.Metadata, "handoff_from"),
		Reason:           metadataTrimmedString(fwd.Metadata, "handoff_reason"),
		ResearchGoal:     metadataTrimmedString(fwd.Metadata, "handoff_research_goal"),
		ConversationHist: metadataTrimmedString(fwd.Metadata, "handoff_conversation_history"),
		KnownContext:     metadataStringSlice(fwd.Metadata, "handoff_known_context"),
		MissingQuestions: metadataStringSlice(fwd.Metadata, "handoff_missing_questions"),
	}
	if handoff.Kind == "" {
		handoff.Kind = "conversation_handoff"
	}
	if handoff.From == "" {
		handoff.From = strings.TrimSpace(fwd.SourceAgentID)
	}
	return handoff
}

func buildAcademicConversationSystemPrompt(base string, handoff *academicConversationHandoff, depth shared.ResearchDepth) string {
	sections := []string{
		strings.TrimSpace(base),
		strings.TrimSpace(AcademicConversationPrompt),
		strings.TrimSpace(academicExternalResearchDisciplinePrompt()),
		strings.TrimSpace(academicResearchDepthPrompt(depth)),
	}
	if handoff != nil {
		sections = append(sections, academicConversationHandoffPrompt(handoff))
	}
	filtered := make([]string, 0, len(sections))
	for _, section := range sections {
		if section != "" {
			filtered = append(filtered, section)
		}
	}
	return strings.Join(filtered, "\n\n---\n\n")
}

func buildAcademicConsultationSystemPrompt(base string, fwd *guide.ForwardedRequest, depth shared.ResearchDepth) string {
	sections := []string{
		strings.TrimSpace(base),
		strings.TrimSpace(AcademicConversationPrompt),
		strings.TrimSpace(academicExternalResearchDisciplinePrompt()),
		strings.TrimSpace(academicResearchDepthPrompt(depth)),
		strings.TrimSpace(academicConsultationPrompt(fwd)),
	}
	filtered := make([]string, 0, len(sections))
	for _, section := range sections {
		if section != "" {
			filtered = append(filtered, section)
		}
	}
	return strings.Join(filtered, "\n\n---\n\n")
}

func academicConversationExtraTools(handoff *academicConversationHandoff) []string {
	if handoff == nil {
		return nil
	}
	return []string{"reroute_request"}
}

func academicConsultationPrompt(fwd *guide.ForwardedRequest) string {
	if fwd == nil {
		return ""
	}
	sections := []string{
		"You are handling a non-user-facing consultation for another agent. Stay in normal Academic mode: research, use tools when useful, and answer the requesting agent directly instead of switching into a rigid worker protocol.",
	}
	if academicForwardedSourceMatches(fwd, "architect") {
		sections = append(sections,
			"This consultation informs Architect planning. Produce a reusable deliverable rather than transient prose.",
			"Before ending the consultation, invoke `author_research_paper` exactly once so the Architect receives a durable research artifact.",
			"Once `author_research_paper` succeeds, end the consultation immediately. Do not make any further tool calls, fetches, or consultations after invoking it.",
			"Use `clone_via_librarian` or `crawl_links` when they materially improve codebase fit or source quality.",
		)
	}
	return strings.Join(sections, "\n")
}

func buildAcademicRecallPrompt(fwd *guide.ForwardedRequest) string {
	if fwd == nil {
		return academicExternalResearchDisciplinePrompt()
	}
	request := strings.TrimSpace(fwd.Input)
	if request == "" {
		return academicExternalResearchDisciplinePrompt()
	}

	var b strings.Builder
	b.WriteString("Research the following request using evidence-backed external sources and codebase validation.\n")
	if fwd.Domain != "" && fwd.Domain != guide.DomainUnknown {
		b.WriteString("Domain: ")
		b.WriteString(string(fwd.Domain))
		b.WriteString("\n")
	}
	if contract := academicCompletionContractForForwardedRequest(fwd); contract != nil {
		b.WriteString(contract.guidancePrompt())
		b.WriteString("\n")
	}
	b.WriteString(academicExternalResearchDisciplinePrompt())
	if depthPrompt := strings.TrimSpace(academicResearchDepthPrompt(academicResearchDepthFromForwarded(fwd))); depthPrompt != "" {
		b.WriteString("\n\n")
		b.WriteString(depthPrompt)
	}
	if extra := academicForwardedResearchPrompt(fwd); extra != "" {
		b.WriteString("\n\n")
		b.WriteString(extra)
	}
	b.WriteString("\n\nRequest:\n")
	b.WriteString(request)
	return b.String()
}

func academicExternalResearchDisciplinePrompt() string {
	return strings.TrimSpace(
		"Use an explicit research workflow. " +
			"1. Plan: identify the concrete sub-questions that must be answered. " +
			"Before choosing a winner, define the decision frame: what is being decided, which criteria matter most, which evidence classes are relevant, what would falsify each major hypothesis, and which uncertainties could change the outcome. " +
			"Identify the underlying domain or domains behind the request and build enough domain knowledge to reason inside them: map the core concepts, standard terminology, governing metrics, canonical source types, and what counts as strong evidence in that field. " +
			"For unfamiliar, specialized, or cross-domain topics, research from the ground up before recommending anything: start with authoritative primers, survey papers, review articles, standards, textbooks, professional society guidance, analyst or institutional research, and then drill into primary studies, technical reports, benchmarks, and direct sources. " +
			"Use early searches to learn the field itself, not just to collect candidate options. Expand the domain vocabulary, identify the canonical debates and failure modes, and learn how practitioners and researchers in that field evaluate claims. " +
			"2. Internal recall: when prior Sylk-grounded sources, fetched documents, stored research, or internal architectural knowledge may already matter, call `knowledge_query` early to search the knowledge graph and grounded document index before duplicating work on the public web. Do not treat `knowledge_query` as a fallback after broad web search when existing grounded evidence could already change the answer. " +
			"3. Search: use `web_search` to discover authoritative candidates for each sub-question and keep searching only while you are still surfacing materially better, more relevant, well-sourced leads. Provider-native `web_search` is not just a title/snippet lookup; it may search, open pages, and inspect source content in-context. " +
			"4. Ground: once a candidate looks promising, stop broad searching and examine it. Sylk may automatically secure-fetch the decisive surfaced exact URL coming out of native `web_search` so it can be Guardian-inspected, ingested, and marked grounded. Do not reflexively call `ground_source` after every `web_search`. Use `ground_source`, `web_fetch`, `fetch_document`, or one bounded `crawl_links` follow-up when you already know the exact URL, need a specific fetch mode, need a bounded crawl, or need to force grounding of an exact URL that has not yet gone through the secured fetch path. When you do need an explicit fetch, use `ground_source` when you want the runtime to choose the fetch mode, `web_fetch` for exact web pages or documentation pages, `fetch_document` for PDFs, papers, benchmark reports, standards, or other methodology-heavy documents, and one bounded `crawl_links` follow-up when the authoritative page obviously fans out to a small set of official subpages you need. `ground_source` returns grounded content immediately; background persistence to the knowledge graph and document store usually does not need to block synthesis. " +
			"Never present a bare search hit, title, snippet, or unexamined URL as a reference. Before finalizing an answer with citations, perform a source audit and confirm whether you are carrying forward a decisive surfaced exact URL from `web_search` that still needs the secured fetch path. " +
			"Before finalizing, enumerate the exact URLs you intend to cite. Confirm which cited URL is the decisive surfaced exact URL coming out of `web_search`, and whether Sylk already secured-fetched that exact URL or whether you still need `ground_source`, `web_fetch`, or `fetch_document` before citing it. A successful `web_fetch` or `fetch_document` on the exact URL already satisfies that grounding obligation; do not redundantly call `ground_source` afterward for the same URL unless you need a different fetch mode. Do not imply that every intermediate searched URL also received an extra secured fetch. Do not examine or ground one page and then cite a different URL from the same site as if it were covered. " +
			"Triangulate material claims across multiple grounded sources whenever feasible. If a conclusion rests on only one grounded source, say so explicitly and treat the confidence as lower unless that source is uniquely authoritative. " +
			"5. Consult: use Librarian or Archivalist only when codebase fit or historical precedent materially changes the answer, and do not repeat the same consultation question to the same agent in one run. " +
			"6. Synthesize: stop only when more searching is unlikely to materially change the conclusion. " +
			"For recommendation, comparison, and design questions, investigate multiple credible options or counterexamples before choosing one. Do not stop after the first plausible answer unless the space is genuinely trivial. " +
			"Do not answer from memory alone when source-backed or current evidence matters, and do not keep issuing broad `web_search` calls once you have promising sources that should be grounded. " +
			"Choose the evidence classes that matter for the domain instead of assuming one source type is enough. Common classes include primary or authoritative sources, empirical or observational evidence, counterevidence or failure cases, methodological or limitations evidence, implementation or operational burden, institutional or ecosystem or market context, and formal or academic or regulatory or standards evidence. Not every class applies to every question, but every relevant class must be checked or explicitly marked unavailable. " +
			"Actively research the strongest negative case: criticisms, failure modes, conflicting studies, lock-in, adoption barriers, migration burden, safety risks, or other reasons the leading option could fail in practice. " +
			"Treat self-authored, vendor-authored, or project-authored material as capability evidence first. It can show what an option claims to support, but it does not by itself prove comparative superiority on speed, simplicity, quality, safety, maturity, or adoption. " +
			"When comparative conclusions depend on claims made by the compared options themselves, seek independent corroboration or explicitly mark the conclusion as provisional and incomplete. " +
			"For claims about performance, latency, throughput, reliability, security impact, cost, scale, adoption, or comparative advantage, prefer primary empirical evidence and grounded quantitative data whenever feasible. " +
			"When measurable criteria matter, surface the decision-relevant measurable criteria for this domain and quantify them when credible evidence exists. " +
			"Do not repeat benchmark numbers, percentages, or study findings unless you actually inspected the source itself in the current run, either through native `web_search` or through the secured fetch path. If the exact cited URL still needed the secured fetch path, ground it before quoting from it. When the numbers matter, note the date, study or workload context, and major measurement caveats if available. " +
			"If strong quantitative evidence is unavailable, say the evidence is qualitative, limited, or stale instead of implying false precision. " +
			"For any material recommendation, ranking, or justification, identify whether the support comes from measured data, official guidance, peer-reviewed research, direct observation, standards or regulation, or only qualitative evidence. Do not justify decisions with vague claims like best, strongest, popular, fast, reliable, secure, scalable, industry-standard, simpler, or mature unless the supporting criteria and evidence are named. " +
			"When relevant studies, papers, systematic reviews, meta-analyses, professional research, institutional reports, or other field-defining material exist, actively look for them rather than relying only on vendor docs, product pages, or commentary. " +
			"When the question is a substantial comparison or architectural recommendation, build an explicit evaluation matrix across the criteria that actually matter, digest the strongest literature or empirical evidence, surface counterarguments, and include an evidence table, methodology digest, risk register, architecture fragment, or flow sketch when that improves clarity. " +
			"Perform your own technical analysis rather than only relaying source claims: validate assumptions and hypotheses against the evidence, sanity-check important math, inspect datasets and methodology when relevant, and call out bias, incentives, sampling problems, and threats to validity. " +
			"Treat substantial externally sourced answers as incomplete unless they contain the relevant structured artifact for the question, such as an evidence table, comparison matrix, methodology digest, risk register, math walkthrough, algorithm sketch, or architecture or flow model. " +
			"Do not include a Short Answer, practical shortlist, or compressed winner section before the evidence and comparison are established. " +
			"Do not produce a shallow roundup that only recommends one option, lists alternatives, and appends sources. Replace that pattern with evidence synthesis, analysis, and explicit caveats. " +
			"Before finalizing, run a rigor audit over all relevant dimensions: link-by-link source examination and grounding checks where still needed, corroboration, competing hypotheses, counterarguments, disconfirming evidence, math and unit checks, sample size and date checks, methodology review, dataset quality review, bias and incentive review, evidence-class coverage, confidence calibration, threats to validity, and the presence of the right structured artifact. If any relevant rigor check fails, do not finalize. " +
			"Aim for the depth of a top-tier internal research memo rather than a high-level roundup or promotional summary. " +
			"Consult the Librarian before finalizing any recommendation so the result matches the codebase reality.",
	)
}

func academicForwardedResearchPrompt(fwd *guide.ForwardedRequest) string {
	if fwd == nil || academicConversationHandoffFromForwarded(fwd) != nil {
		return ""
	}
	if !academicForwardedSourceMatches(fwd, "architect") {
		return ""
	}

	var b strings.Builder
	b.WriteString("This research will inform Architect planning. Prefer durable, cited output over transient prose.\n")
	b.WriteString("Do not begin with a naked winner. First make the decision frame legible: what is being decided, which criteria matter most, which evidence classes are relevant, and how strong the evidence is.\n")
	b.WriteString("Identify the underlying domain or domains behind the request and build enough domain knowledge to reason inside them: map the core concepts, standard terminology, governing metrics, canonical source types, and what counts as strong evidence in that field.\n")
	b.WriteString("For unfamiliar, specialized, or cross-domain topics, research from the ground up before recommending anything: start with authoritative primers, survey papers, review articles, standards, textbooks, professional society guidance, analyst or institutional research, and then drill into primary studies, technical reports, benchmarks, and direct sources.\n")
	b.WriteString("Use early searches to learn the field itself, not just to collect candidate options. Expand the domain vocabulary, identify the canonical debates and failure modes, and learn how practitioners and researchers in that field evaluate claims.\n")
	b.WriteString("If prior Sylk-grounded sources, fetched documents, or stored research could materially help, call `knowledge_query` early to search the knowledge graph and grounded document index before duplicating work on the public web.\n")
	b.WriteString("Provider-native `web_search` is not just a title/snippet lookup; it may search, open pages, and inspect source content in-context. When `web_search` surfaces a promising source, do not keep searching instead of examining it.\n")
	b.WriteString("When native `web_search` surfaces a clearly relevant, high-quality exact URL that you are about to carry forward as the decisive surfaced source, Sylk may automatically secure-fetch that URL through the local pipeline so it can be Guardian-inspected, ingested, and marked grounded.\n")
	b.WriteString("Do not reflexively call `ground_source`, `web_fetch`, or `fetch_document` after every `web_search`. Use explicit fetch tools when you already know the exact URL, need a specific fetch mode, need a bounded crawl, or need to force grounding of an exact URL that has not yet gone through the secured fetch path.\n")
	b.WriteString("When you do need an explicit fetch, use `ground_source` when you want the runtime to choose the fetch mode, `web_fetch` for exact web pages or documentation pages, `fetch_document` for PDFs, papers, benchmark reports, standards, or other methodology-heavy documents, and one bounded `crawl_links` follow-up when the authoritative page obviously fans out to a small set of official subpages you need.\n")
	b.WriteString("Never hand Architect a bare search hit, title, snippet, or unexamined URL as a reference.\n")
	b.WriteString("Before finalizing the response, audit the cited URLs and confirm whether you are carrying forward a decisive surfaced exact URL from `web_search` that still needs the secured fetch path.\n")
	b.WriteString("Before finalizing, enumerate the exact URLs you intend to cite. Confirm which cited URL is the decisive surfaced exact URL coming out of `web_search`, and whether Sylk already secured-fetched that exact URL or whether you still need `ground_source`, `web_fetch`, or `fetch_document` before citation. A successful `web_fetch` or `fetch_document` on the exact URL already satisfies that grounding obligation. Do not imply that every intermediate searched URL also received an extra secured fetch.\n")
	b.WriteString("Corroborate important conclusions across multiple grounded sources whenever feasible. If a conclusion depends on one grounded source, say that plainly and lower the confidence unless the source is uniquely authoritative.\n")
	b.WriteString("Map the relevant evidence classes for this domain and say which are strong, weak, or unavailable: primary or authoritative sources, empirical or observational evidence, counterevidence or failure cases, methodological or limitations evidence, implementation or operational burden, institutional or ecosystem or market context, and formal or academic or regulatory evidence.\n")
	b.WriteString("For recommendation or design-space questions, research multiple credible options before settling on one, then explain why the preferred option beats the strongest alternative.\n")
	b.WriteString("Research the strongest downside case too: criticisms, failure modes, conflicting evidence, lock-in, adoption barriers, migration burden, or other reasons the leading option could fail in practice.\n")
	b.WriteString("Treat self-authored, vendor-authored, or project-authored material as capability evidence first. It can establish what an option claims to support, but it does not by itself prove comparative superiority on speed, simplicity, quality, safety, maturity, or adoption.\n")
	b.WriteString("When comparative conclusions depend on claims made by the compared options themselves, seek independent corroboration or explicitly mark the conclusion as provisional and incomplete.\n")
	b.WriteString("When the recommendation depends on performance, cost, reliability, security impact, scale, or adoption, back it with grounded statistics, benchmark context, standards, or study results whenever credible data exists. If the evidence is only qualitative, limited, or stale, say that plainly.\n")
	b.WriteString("When measurable criteria matter, surface the decision-relevant metrics for this domain and quantify them when credible evidence exists.\n")
	b.WriteString("Do not cite benchmark numbers, percentages, or study conclusions unless you actually inspected the source in the current run, and if the exact cited URL still needed the secured fetch path, made sure it went through that path first. Note the date and measurement context when they materially affect the decision.\n")
	b.WriteString("For every material recommendation or ranking, state whether the support comes from measured data, official guidance, peer-reviewed research, direct observation, standards or regulation, or only qualitative evidence.\n")
	b.WriteString("Do not use unsupported adjectives like best, strongest, safer, simpler, faster, mature, or standard unless you tie them to explicit criteria and grounded evidence.\n")
	b.WriteString("When relevant studies, papers, systematic reviews, meta-analyses, professional research, institutional reports, or other field-defining material exist, actively look for them rather than relying only on vendor docs, product pages, or commentary.\n")
	b.WriteString("When relevant literature, benchmarks, or empirical studies exist, digest them instead of just citing them, and use evidence tables, comparison matrices, methodology digests, risk registers, or architecture and flow sketches when they materially improve the result.\n")
	b.WriteString("Do your own technical analysis: validate assumptions, inspect methodology and dataset quality, sanity-check important math, and call out bias or threats to validity instead of only relaying source claims.\n")
	b.WriteString("Treat the response as incomplete if it lacks the relevant structured artifact for the question, such as an evidence table, comparison matrix, methodology digest, risk register, math walkthrough, algorithm sketch, or architecture or flow model.\n")
	b.WriteString("Do not include a Short Answer, practical shortlist, or compressed winner section before the evidence and comparison are established.\n")
	b.WriteString("Do not give Architect a shallow roundup that just recommends a winner, lists alternatives, and appends sources.\n")
	b.WriteString("Before finalizing, run a rigor audit across all relevant dimensions: link-by-link source examination and grounding checks where still needed, corroboration, alternatives, counterevidence, math checks, methodology review, dataset quality, bias, evidence-class coverage, confidence calibration, threats to validity, and the right structured artifact. If any relevant check fails, keep researching or explicitly mark the result incomplete.\n")
	b.WriteString("Write at the depth of a top-tier internal research memo, not a promotional or blog-style summary.\n")
	b.WriteString("If you gather enough evidence to produce a reusable artifact, use `author_research_paper` so Architect receives structured research instead of only a chat answer.\n")
	b.WriteString("Use `clone_via_librarian` or `crawl_links` when they materially improve source quality or implementation fit.\n")
	b.WriteString("You may consult the Librarian for codebase fit and the Archivalist for historical precedent, but do not repeat the same consultation question to the same agent in one run.")
	return strings.TrimSpace(b.String())
}

func academicForwardedResearchExtraTools(fwd *guide.ForwardedRequest) []string {
	if fwd == nil || academicConversationHandoffFromForwarded(fwd) != nil {
		return nil
	}
	if !academicForwardedSourceMatches(fwd, "architect") {
		return nil
	}
	switch fwd.Intent {
	case guide.IntentRecall, guide.IntentCheck, guide.IntentFetch:
		return []string{"author_research_paper", "clone_via_librarian", "crawl_links"}
	default:
		return nil
	}
}

func (a *Academic) withRequestResearchExecutionState(ctx context.Context, fwd *guide.ForwardedRequest) context.Context {
	if ctx == nil || fwd == nil {
		return ctx
	}
	if academicConversationHandoffFromForwarded(fwd) != nil {
		return ctx
	}
	if academicResearchExecutionStateFromContext(ctx) != nil {
		return ctx
	}
	sessionID := strings.TrimSpace(fwd.SessionID)
	if sessionID == "" {
		sessionID = versioningSessionIDOrDefault(ctx, a.config.SessionID)
	}
	state := newAcademicResearchExecutionState(sessionID)
	if academicForwardedSourceMatches(fwd, "architect") {
		state.setResearchPaperRequired(true)
	}
	return WithAcademicResearchExecutionState(ctx, state)
}

func (a *Academic) withConsultationTurnState(ctx context.Context, fwd *guide.ForwardedRequest) context.Context {
	if ctx == nil || fwd == nil {
		return ctx
	}
	if academicConversationHandoffFromForwarded(fwd) != nil {
		return ctx
	}
	if !academicForwardedSourceMatches(fwd, "architect") {
		return ctx
	}
	ctx = a.withRequestResearchExecutionState(ctx, fwd)
	if AcademicTurnStateFromContext(ctx) == nil {
		ctx = WithAcademicTurnState(ctx, newAcademicTurnState(
			academicTurnActionResearchPaper,
			"This Architect consultation must terminate immediately after `author_research_paper` succeeds.",
		))
	}
	return ctx
}

func academicConversationHandoffPrompt(handoff *academicConversationHandoff) string {
	if handoff == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Architect Handoff\n\n")
	b.WriteString("You are taking over a live user conversation from the Architect because the request is not yet specific enough for safe implementation planning.\n")
	b.WriteString("Your job is to help the user make the problem concrete enough for planning: clarify scope, constraints, environments, success criteria, decision points, and any missing research-backed defaults.\n")
	b.WriteString("Stay conversational and user-facing. Do not mention hidden routing mechanics unless the user explicitly asks.\n")
	if reason := strings.TrimSpace(handoff.Reason); reason != "" {
		b.WriteString("\nHandoff reason: ")
		b.WriteString(reason)
		b.WriteString("\n")
	}
	if goal := strings.TrimSpace(handoff.ResearchGoal); goal != "" {
		b.WriteString("Research goal: ")
		b.WriteString(goal)
		b.WriteString("\n")
	}
	if len(handoff.KnownContext) > 0 {
		b.WriteString("Known context:\n")
		for _, item := range handoff.KnownContext {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}
	if history := strings.TrimSpace(handoff.ConversationHist); history != "" {
		b.WriteString("Conversation so far:\n")
		b.WriteString(history)
		b.WriteString("\n")
	}
	if len(handoff.MissingQuestions) > 0 {
		b.WriteString("Missing requirements to resolve:\n")
		for _, item := range handoff.MissingQuestions {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}
	b.WriteString("When the request is concrete enough for implementation planning, invoke reroute_request with suggested_target=\"architect\" so the conversation returns through the Guide. In the handoff reason, summarize why the requirements are now concrete enough to plan.")
	return strings.TrimSpace(b.String())
}

func metadataTrimmedString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return ""
	}
	trimmed := strings.TrimSpace(fmt.Sprint(raw))
	if trimmed == "<nil>" {
		return ""
	}
	return trimmed
}

func metadataStringSlice(metadata map[string]any, key string) []string {
	if metadata == nil {
		return nil
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return trimAndFilterStrings(values)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			trimmed := strings.TrimSpace(fmt.Sprint(value))
			if trimmed == "" || trimmed == "<nil>" {
				continue
			}
			result = append(result, trimmed)
		}
		return result
	default:
		trimmed := strings.TrimSpace(fmt.Sprint(values))
		if trimmed == "" || trimmed == "<nil>" {
			return nil
		}
		return []string{trimmed}
	}
}

func trimAndFilterStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

// handleFetch processes fetch requests. For repository clones, delegates to the
// Librarian via bus consultation. For web URLs, uses the fetch pipeline directly.
func (a *Academic) handleFetch(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	ctx = a.withRequestResearchExecutionState(ctx, fwd)
	fetchPrompt := fmt.Sprintf(
		"The user wants to fetch or download external content. "+
			"If the request involves a git repository, use clone_via_librarian to have the Librarian clone it into the searchable package store. "+
			"If the request involves a web page or document and you do not already know the URL, use web_search first. "+
			"Once you know the exact URL, use the explicit fetch tool that fits the job: ground_source when you want Sylk to choose the fetch mode, web_fetch for exact pages, and fetch_document for exact documents. Native web_search may already inspect pages in-context, and Sylk may also auto-ground surfaced high-quality exact URLs. "+
			"Provide a clear summary of what was fetched.\n\n%s",
		fwd.Input,
	)
	if contract := academicCompletionContractForForwardedRequest(fwd); contract != nil {
		fetchPrompt = contract.guidancePrompt() + "\n\n" + fetchPrompt
	}
	if depthPrompt := strings.TrimSpace(academicResearchDepthPrompt(AcademicResearchDepthFromContext(ctx))); depthPrompt != "" {
		fetchPrompt = depthPrompt + "\n\n" + fetchPrompt
	}
	if extra := academicForwardedResearchPrompt(fwd); extra != "" {
		fetchPrompt = extra + "\n\n" + fetchPrompt
	}

	surface := a.requestToolSurfaceForForwardedRequest(ctx, fwd, academicForwardedResearchExtraTools(fwd)...)
	academicPrepareSkillsForInput(a, ctx, fetchPrompt)
	llmReq := &providers.Request{
		SystemPrompt: a.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: fetchPrompt}},
		Tools:        a.buildToolDefinitionsWithSurface(surface),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(ctx, llmReq, "fetch")

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, surface)
	})
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}

	return map[string]any{
		"type":    "fetch",
		"content": result,
	}, nil
}

// handleRecall processes recall (query) requests using the LLM tool loop.
func (a *Academic) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	ctx = a.withRequestResearchExecutionState(ctx, fwd)
	recallPrompt := buildAcademicRecallPrompt(fwd)
	systemPrompt := a.config.SystemPrompt

	surface := a.requestToolSurfaceForForwardedRequest(ctx, fwd, academicForwardedResearchExtraTools(fwd)...)
	if contract := academicCompletionContractForForwardedRequest(fwd); contract != nil {
		ctx = WithAcademicCompletionContract(ctx, contract)
	}
	academicPrepareSkillsForInput(a, ctx, recallPrompt)
	llmReq := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: recallPrompt}},
		Tools:        a.buildToolDefinitionsWithSurface(surface),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(ctx, llmReq, "recall")

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, surface)
	})
	if err != nil {
		return nil, fmt.Errorf("research recall failed: %w", err)
	}

	return map[string]any{
		"type":    "recall",
		"content": result,
	}, nil
}

// handleCheck processes check (verification) requests using the LLM tool loop.
func (a *Academic) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	ctx = a.withRequestResearchExecutionState(ctx, fwd)
	checkPrompt := fmt.Sprintf(
		"Verify the following claim against your knowledge of best practices, "+
			"research, and technical standards. Use web_search when fresh external sources are needed, and use your tools to consult the "+
			"Librarian for codebase context. Provide a structured assessment:\n\n%s",
		fwd.Input,
	)
	if contract := academicCompletionContractForForwardedRequest(fwd); contract != nil {
		checkPrompt = contract.guidancePrompt() + "\n\n" + checkPrompt
		ctx = WithAcademicCompletionContract(ctx, contract)
	}
	if depthPrompt := strings.TrimSpace(academicResearchDepthPrompt(AcademicResearchDepthFromContext(ctx))); depthPrompt != "" {
		checkPrompt = depthPrompt + "\n\n" + checkPrompt
	}
	if extra := academicForwardedResearchPrompt(fwd); extra != "" {
		checkPrompt = extra + "\n\n" + checkPrompt
	}

	surface := a.requestToolSurfaceForForwardedRequest(ctx, fwd, academicForwardedResearchExtraTools(fwd)...)
	academicPrepareSkillsForInput(a, ctx, checkPrompt)
	llmReq := &providers.Request{
		SystemPrompt: a.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: checkPrompt}},
		Tools:        a.buildToolDefinitionsWithSurface(surface),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(ctx, llmReq, "check")

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, surface)
	})
	if err != nil {
		return nil, fmt.Errorf("research check failed: %w", err)
	}

	return map[string]any{
		"type":    "check",
		"content": result,
	}, nil
}

func (a *Academic) handleHelp(_ context.Context, _ *guide.ForwardedRequest) (any, error) {
	return map[string]any{
		"agent":              "academic",
		"description":        "External research, best practices, evidence-backed recommendations, and content fetching.",
		"supported_intents":  []guide.Intent{guide.IntentRecall, guide.IntentCheck, guide.IntentFetch, guide.IntentHelp},
		"supported_domains":  []guide.Domain{guide.DomainPatterns, guide.DomainDecisions, guide.DomainLearnings, guide.DomainResearch},
		"recommended_routes": []string{"@academic:recall:research", "@academic:check:research", "@academic:fetch:research"},
	}, nil
}

// =============================================================================
// Synchronous Consultation (Bus-based)
// =============================================================================

// routeSyncTimeout overrides the default target-aware consultation timeout in
// tests. When left at the shared default, Academic uses the target-aware
// consultation timeout policy.
var routeSyncTimeout = shared.DefaultConsultationTimeout

func academicConsultationTimeout(target string) time.Duration {
	if routeSyncTimeout != shared.DefaultConsultationTimeout {
		return routeSyncTimeout
	}
	return shared.ConsultationInactivityTimeout(target)
}

func (a *Academic) registerPendingConsult(correlationID string) *shared.PendingSyncWait {
	wait := shared.NewPendingSyncWait()
	a.pendingMu.Lock()
	a.pendingConsults[correlationID] = wait
	a.pendingMu.Unlock()
	return wait
}

func (a *Academic) clearPendingConsult(correlationID string) {
	a.pendingMu.Lock()
	delete(a.pendingConsults, correlationID)
	a.pendingMu.Unlock()
}

// deliverConsultResponse delivers terminal messages and stream activity to
// synchronous waiters.
func (a *Academic) deliverConsultResponse(msg *guide.Message) {
	if msg == nil || msg.CorrelationID == "" {
		return
	}
	switch msg.Type {
	case guide.MessageTypeResponse, guide.MessageTypeError:
		a.pendingMu.Lock()
		wait := a.pendingConsults[msg.CorrelationID]
		a.pendingMu.Unlock()
		if wait == nil {
			return
		}
		select {
		case wait.Response <- msg:
		default:
		}
	case guide.MessageTypeStream:
		for _, correlationID := range shared.PendingSyncActivityCorrelations(msg) {
			a.pendingMu.Lock()
			wait := a.pendingConsults[correlationID]
			a.pendingMu.Unlock()
			if wait == nil {
				continue
			}
			select {
			case wait.Activity <- struct{}{}:
			default:
			}
		}
	default:
		return
	}
}

func (a *Academic) publishConsultRequest(req *guide.RouteRequest) error {
	if req == nil {
		return fmt.Errorf("route request is required")
	}
	if req.CorrelationID == "" {
		req.CorrelationID = "corr_" + uuid.NewString()
	}
	req.SourceAgentID = strings.TrimSpace(a.id)
	req.SourceAgentName = "academic"
	if req.Timestamp.IsZero() {
		req.Timestamp = time.Now()
	}
	msg := guide.NewRequestMessage(generateMessageID(), req)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

// requestConsultSync publishes a RouteRequest and waits synchronously for
// the response, bounded by routeSyncTimeout.
func (a *Academic) requestConsultSync(ctx context.Context, req *guide.RouteRequest) (*guide.Message, error) {
	if a.bus == nil || !a.running {
		return nil, fmt.Errorf("academic bus is unavailable")
	}
	if req == nil {
		return nil, fmt.Errorf("route request is required")
	}
	waitCtx, release := shared.WithoutDeadlineCancellation(ctx)
	defer release()

	req.Metadata = shared.InheritedBranchMetadata(waitCtx, req.Metadata)
	if req.ParentCorrelationID == "" {
		if stream, ok := shared.StreamMetadataFromContext(waitCtx); ok {
			req.ParentCorrelationID = stream.CorrelationID
		}
	}
	req.Metadata = shared.RouteMetadataWithInterAgentBranch(waitCtx, req.Metadata)
	if req.TargetAgentID != "" {
		req.ExplicitTarget = true
	}
	if req.Hops == 0 {
		req.Hops = routeHopsFromContext(waitCtx)
	}
	response, err := shared.RetryBusyRouteRequest(waitCtx, req.TargetAgentID, shared.DefaultBusyRetryPolicy(req.TargetAgentID), func(attemptCtx context.Context, _ int) (*guide.Message, error) {
		req.CorrelationID = "corr_" + uuid.NewString()
		wait := a.registerPendingConsult(req.CorrelationID)
		defer a.clearPendingConsult(req.CorrelationID)

		if err := a.publishConsultRequest(req); err != nil {
			return nil, err
		}

		response, err := shared.WaitForPendingSyncResponse(
			attemptCtx,
			fmt.Sprintf("consultation to %q", req.TargetAgentID),
			academicConsultationTimeout(req.TargetAgentID),
			wait,
		)
		if err != nil {
			return nil, err
		}
		if busyErr, ok := shared.BusyRouteResponseMessage(response, req.TargetAgentID); ok {
			return nil, busyErr
		}
		return response, nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// requestConsultation is the high-level consultation helper that builds a
// RouteRequest, calls requestConsultSync, and returns ConsultationEvidence.
func (a *Academic) requestConsultation(
	ctx context.Context,
	target, query, scope, sessionID string,
) (*shared.ConsultationEvidence, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = versioning.SessionIDFromContext(ctx)
	}
	if sessionID == "" {
		sessionID = strings.TrimSpace(a.config.SessionID)
	}
	req := &guide.RouteRequest{
		Input:         query,
		TargetAgentID: target,
		SessionID:     sessionID,
	}
	admission := shared.AdmitConsultation(ctx, target, query, req.Metadata)
	if !admission.Allowed {
		return failedConsultEvidence(target, query, scope, "", shared.ConsultationAdmissionError(admission)), nil
	}
	spec := shared.InterAgentBranchSpec{
		Kind:       shared.InterAgentToolEventKindConsult,
		ToolName:   "consult_" + strings.ReplaceAll(strings.TrimSpace(target), "-", "_"),
		AgentTypes: []string{target},
		Summary:    query,
		Args: map[string]any{
			"target": target,
			"query":  query,
			"scope":  scope,
		},
	}
	response, err := shared.WithInterAgentBranchMessage(ctx, spec, func(branchCtx context.Context, branch shared.InterAgentBranchHandle) (*guide.Message, error) {
		req.Metadata = branch.ApplyMetadata(branchCtx, admission.Metadata)
		resp, err := a.requestConsultSync(branchCtx, req)
		shared.RecordConsultationOutcome(branchCtx, admission.AttemptID, err == nil, shared.ConsultationDataFromMessage(resp), err)
		return resp, err
	})
	if err != nil {
		if shared.IsAgentBusyError(err) {
			return failedConsultEvidence(target, query, scope, req.CorrelationID, err), shared.ConsultationBusyDelegatedError(target, err)
		}
		return failedConsultEvidence(target, query, scope, req.CorrelationID, err), err
	}
	return buildConsultEvidence(target, query, scope, req.CorrelationID, response), nil
}

func buildConsultEvidence(
	target, query, scope, correlationID string,
	msg *guide.Message,
) *shared.ConsultationEvidence {
	evidence := &shared.ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: correlationID,
		RequestedAt: time.Now(),
		ReceivedAt:  time.Now(),
	}
	if msg == nil {
		evidence.Success = false
		evidence.Error = "empty consultation response"
		return evidence
	}
	if resp, ok := msg.GetRouteResponse(); ok && resp != nil {
		evidence.Success = resp.Success
		evidence.Data = resp.Data
		evidence.Error = resp.Error
		return evidence
	}
	if errStr, ok := msg.GetError(); ok {
		evidence.Success = false
		evidence.Error = errStr
		return evidence
	}
	evidence.Success = false
	evidence.Error = "unsupported consultation payload"
	return evidence
}

func failedConsultEvidence(target, query, scope, corr string, err error) *shared.ConsultationEvidence {
	evidence := &shared.ConsultationEvidence{
		Target:      target,
		Query:       query,
		Scope:       scope,
		Correlation: corr,
		Success:     false,
		RequestedAt: time.Now(),
		ReceivedAt:  time.Now(),
	}
	if err != nil {
		evidence.Error = err.Error()
	}
	return evidence
}

// handleBusResponse processes responses to requests we made.
// Delivers to synchronous consultation waiters.
func (a *Academic) handleBusResponse(msg *guide.Message) error {
	a.deliverConsultResponse(msg)
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events.
func (a *Academic) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		a.knownAgents[ann.AgentID] = ann
		a.logger.Debug("agent registered", "agent_id", ann.AgentID, "agent_name", ann.AgentName)
		shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventRegistryEvent,
			"academic", "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "registered",
			})
	case guide.MessageTypeAgentUnregistered:
		delete(a.knownAgents, ann.AgentID)
		a.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
		shared.LogAgentEvent(a.steering.EventLogger(), agentlog.EventRegistryEvent,
			"academic", "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "unregistered",
			})
	}

	return nil
}

// GetKnownAgents returns all agents the Academic knows about.
func (a *Academic) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]*guide.AgentAnnouncement, len(a.knownAgents))
	for k, v := range a.knownAgents {
		result[k] = v
	}
	return result
}

// PublishRequest publishes a request to the Guide for routing.
func (a *Academic) PublishRequest(req *guide.RouteRequest) error {
	if !a.running {
		return fmt.Errorf("academic is not running")
	}

	req.SourceAgentID = strings.TrimSpace(a.id)
	req.SourceAgentName = "academic"

	msg := guide.NewRequestMessage(generateMessageID(), req)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Core Research Methods (LLM-driven)
// =============================================================================

// Research performs research on a topic via the LLM tool loop.
func (a *Academic) Research(ctx context.Context, query *ResearchQuery) (*ResearchResult, error) {
	ctx = WithAcademicResearchDepth(ctx, academicResearchDepthFromQuery(query))
	if contract := AcademicCompletionContractFromContext(ctx); contract == nil {
		if derived := academicCompletionContractForResearchQuery(query); derived != nil {
			ctx = WithAcademicCompletionContract(ctx, derived)
		}
	}
	if academicResearchExecutionStateFromContext(ctx) == nil {
		sessionID := strings.TrimSpace(a.config.SessionID)
		if query != nil && strings.TrimSpace(query.SessionID) != "" {
			sessionID = strings.TrimSpace(query.SessionID)
		}
		ctx = WithAcademicResearchExecutionState(ctx, newAcademicResearchExecutionState(versioningSessionIDOrDefault(ctx, sessionID)))
	}
	// Check cache first
	cacheKey := a.cacheKey(query)
	if cached := a.getCached(cacheKey); cached != nil {
		a.logger.Debug("cache hit for research query", "query", query.Query)
		return cached, nil
	}

	// Build LLM request
	researchPrompt := fmt.Sprintf(
		"Research the following topic thoroughly. Use your tools to consult "+
			"the Librarian for codebase context and validate findings. "+
			"Use web_search to discover external sources when you do not already know the right URLs. Native web_search may already inspect pages in-context, and Sylk may auto-ground surfaced high-quality exact URLs; use explicit fetch tools when you need exact-URL control or a secured fetch path that has not happened yet.\n\n"+
			"Topic: %s", query.Query,
	)
	if contract := AcademicCompletionContractFromContext(ctx); contract != nil {
		researchPrompt = contract.guidancePrompt() + "\n\n" + researchPrompt
	}
	if query.Domain != "" {
		researchPrompt += fmt.Sprintf("\nDomain: %s", query.Domain)
	}
	if query.LanguageFilter != "" {
		researchPrompt += fmt.Sprintf("\nLanguage: %s", query.LanguageFilter)
	}
	if depthPrompt := strings.TrimSpace(academicResearchDepthPrompt(AcademicResearchDepthFromContext(ctx))); depthPrompt != "" {
		researchPrompt += "\n" + depthPrompt
	}

	a.prepareSkillsForInput(researchPrompt)
	llmReq := &providers.Request{
		SystemPrompt: a.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: researchPrompt}},
		Tools:        a.buildToolDefinitions(),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(ctx, llmReq, "research")

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, a.toolRuntime())
	})
	if err != nil {
		return nil, fmt.Errorf("research failed: %w", err)
	}

	queryID := uuid.New().String()
	now := time.Now()

	researchResult := &ResearchResult{
		QueryID:     queryID,
		Confidence:  a.config.DefaultConfidence,
		GeneratedAt: now,
		Findings: []Finding{{
			ID:         uuid.New().String(),
			Topic:      query.Query,
			Summary:    result,
			Confidence: a.config.DefaultConfidence,
		}},
	}

	// Cache the result
	a.setCached(cacheKey, researchResult)

	return researchResult, nil
}

// =============================================================================
// Activity Publishing
// =============================================================================

// publishActivity emits a user-visible activity event so the UI agent panel
// tracks this academic's lifecycle.
func (a *Academic) publishActivity(eventType events.EventType, content string) {
	a.publishActivityForSession(a.config.SessionID, eventType, content)
}

func (a *Academic) publishActivityForSession(sessionID string, eventType events.EventType, content string) {
	a.publishActivityForRequest(sessionID, "", eventType, content)
}

func (a *Academic) publishActivityForRequest(sessionID, correlationID string, eventType events.EventType, content string) {
	if a.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, strings.TrimSpace(sessionID), content)
	evt.CorrelationID = strings.TrimSpace(correlationID)
	evt.AgentID = a.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "academic"
	evt.Data["agent_name"] = "Academic"
	a.activityPub.PublishActivity(evt)
}

func (a *Academic) requestToolSurfaceForForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest, extraTools ...string) toolruntime.Surface {
	if bundle := academicToolBundleFromContext(ctx); bundle != nil {
		return bundle.requestSurface(extraTools...)
	}
	base := a.toolRuntime()
	if base == nil {
		return nil
	}
	requestTools := make([]string, 0, len(extraTools))
	requestTools = append(requestTools, extraTools...)
	if len(requestTools) == 0 {
		return base
	}
	view, err := base.RequestView(requestTools...)
	if err != nil {
		return base
	}
	return view
}

func academicForwardedSourceMatches(fwd *guide.ForwardedRequest, target string) bool {
	if fwd == nil {
		return false
	}
	target = strings.ToLower(strings.TrimSpace(target))
	if target == "" {
		return false
	}
	candidates := []string{
		metadataTrimmedString(fwd.Metadata, "source_agent_type"),
		metadataTrimmedString(fwd.Metadata, "source_agent_name"),
		metadataTrimmedString(fwd.Metadata, "source_agent_id"),
		fwd.SourceAgentName,
		fwd.SourceAgentID,
	}
	for _, candidate := range candidates {
		if academicAgentIdentityMatches(candidate, target) {
			return true
		}
	}
	return false
}

func academicAgentIdentityMatches(candidate string, target string) bool {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if candidate == "" || target == "" {
		return false
	}
	if candidate == target {
		return true
	}
	for _, sep := range []string{"-", "_", ":", "/"} {
		if strings.HasPrefix(candidate, target+sep) {
			return true
		}
	}
	return false
}

// =============================================================================
// Caching
// =============================================================================

func (a *Academic) cacheKey(query *ResearchQuery) string {
	return fmt.Sprintf("%s:%s:%s:%s", query.Query, query.Domain, query.LanguageFilter, shared.EffectiveResearchDepth(query.Depth))
}

func (a *Academic) getCached(key string) *ResearchResult {
	now := time.Now()
	a.mu.RLock()
	result, exists := a.researchCache[key]
	expired := academicResearchResultExpired(result, a.config.CacheExpiry, now)
	a.mu.RUnlock()
	if !exists {
		return nil
	}
	if expired {
		a.mu.Lock()
		if current, ok := a.researchCache[key]; ok && academicResearchResultExpired(current, a.config.CacheExpiry, now) {
			delete(a.researchCache, key)
			a.pruneSourceIndexLocked(now)
		}
		a.mu.Unlock()
		return nil
	}
	return result
}

func (a *Academic) setCached(key string, result *ResearchResult) {
	if result == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	if result.GeneratedAt.IsZero() {
		result.GeneratedAt = now
	}
	result.CachedAt = &now
	a.researchCache[key] = result
	a.pruneResearchStateLocked(now)
}

func (a *Academic) pruneResearchStateLocked(now time.Time) {
	if a == nil {
		return
	}
	a.pruneResearchCacheLocked(now)
	a.pruneSourceIndexLocked(now)
}

func (a *Academic) pruneResearchCacheLocked(now time.Time) {
	for key, result := range a.researchCache {
		if academicResearchResultExpired(result, a.config.CacheExpiry, now) {
			delete(a.researchCache, key)
		}
	}
	limit := a.config.ResearchCacheLimit
	if limit <= 0 || len(a.researchCache) <= limit {
		return
	}
	type agedResult struct {
		key      string
		cachedAt time.Time
	}
	oldest := make([]agedResult, 0, len(a.researchCache))
	for key, result := range a.researchCache {
		oldest = append(oldest, agedResult{
			key:      key,
			cachedAt: academicResearchResultTime(result),
		})
	}
	sort.Slice(oldest, func(i, j int) bool {
		return oldest[i].cachedAt.Before(oldest[j].cachedAt)
	})
	for _, item := range oldest[:len(oldest)-limit] {
		delete(a.researchCache, item.key)
	}
}

func (a *Academic) pruneSourceIndexLocked(now time.Time) {
	if len(a.sourceIndex) == 0 {
		return
	}

	protected := a.protectedSourceIDsLocked(now)
	maxAge := a.config.CacheExpiry * 2
	for id, source := range a.sourceIndex {
		if _, ok := protected[id]; ok {
			continue
		}
		if academicSourceExpired(source, maxAge, now) {
			delete(a.sourceIndex, id)
		}
	}

	limit := a.config.SourceIndexLimit
	if limit <= 0 || len(a.sourceIndex) <= limit {
		return
	}

	type agedSource struct {
		id        string
		updatedAt time.Time
	}
	unprotected := make([]agedSource, 0, len(a.sourceIndex))
	all := make([]agedSource, 0, len(a.sourceIndex))
	for id, source := range a.sourceIndex {
		item := agedSource{id: id, updatedAt: academicSourceTime(source)}
		all = append(all, item)
		if _, ok := protected[id]; !ok {
			unprotected = append(unprotected, item)
		}
	}
	sort.Slice(unprotected, func(i, j int) bool {
		return unprotected[i].updatedAt.Before(unprotected[j].updatedAt)
	})
	sort.Slice(all, func(i, j int) bool {
		return all[i].updatedAt.Before(all[j].updatedAt)
	})

	overflow := len(a.sourceIndex) - limit
	for _, item := range unprotected {
		if overflow == 0 {
			return
		}
		if _, ok := a.sourceIndex[item.id]; ok {
			delete(a.sourceIndex, item.id)
			overflow--
		}
	}
	for _, item := range all {
		if overflow == 0 {
			return
		}
		if _, ok := a.sourceIndex[item.id]; ok {
			delete(a.sourceIndex, item.id)
			overflow--
		}
	}
}

func (a *Academic) protectedSourceIDsLocked(now time.Time) map[string]struct{} {
	protected := make(map[string]struct{})
	for _, result := range a.researchCache {
		if academicResearchResultExpired(result, a.config.CacheExpiry, now) {
			continue
		}
		for _, id := range result.SourcesConsulted {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				protected[trimmed] = struct{}{}
			}
		}
		for _, finding := range result.Findings {
			for _, id := range finding.SourceIDs {
				if trimmed := strings.TrimSpace(id); trimmed != "" {
					protected[trimmed] = struct{}{}
				}
			}
		}
		for _, recommendation := range result.Recommendations {
			for _, id := range recommendation.SourceIDs {
				if trimmed := strings.TrimSpace(id); trimmed != "" {
					protected[trimmed] = struct{}{}
				}
			}
		}
	}
	return protected
}

func academicResearchResultExpired(result *ResearchResult, expiry time.Duration, now time.Time) bool {
	if result == nil || expiry <= 0 || result.CachedAt == nil {
		return false
	}
	return now.Sub(*result.CachedAt) > expiry
}

func academicResearchResultTime(result *ResearchResult) time.Time {
	if result == nil {
		return time.Time{}
	}
	if result.CachedAt != nil {
		return *result.CachedAt
	}
	return result.GeneratedAt
}

func academicSourceExpired(source *Source, maxAge time.Duration, now time.Time) bool {
	if source == nil || maxAge <= 0 {
		return false
	}
	timestamp := academicSourceTime(source)
	if timestamp.IsZero() {
		return false
	}
	return now.Sub(timestamp) > maxAge
}

func academicSourceTime(source *Source) time.Time {
	if source == nil {
		return time.Time{}
	}
	if !source.UpdatedAt.IsZero() {
		return source.UpdatedAt
	}
	return source.IngestedAt
}

// =============================================================================
// Outcome Tracking
// =============================================================================

// OutcomeRecord tracks the outcome of a recommendation.
type OutcomeRecord struct {
	ID               string    `json:"id"`
	Query            string    `json:"query"`
	RecommendationID string    `json:"recommendation_id"`
	Success          bool      `json:"success"`
	Notes            string    `json:"notes,omitempty"`
	RecordedAt       time.Time `json:"recorded_at"`
}

// OutcomeHistory tracks historical outcomes for maturity-aware recommendations.
type OutcomeHistory struct {
	mu       sync.RWMutex
	outcomes []*OutcomeRecord
	limit    int
}

// NewOutcomeHistory creates a new outcome history tracker.
func NewOutcomeHistory(limit int) *OutcomeHistory {
	return &OutcomeHistory{
		outcomes: make([]*OutcomeRecord, 0),
		limit:    limit,
	}
}

// Record adds an outcome to the history.
func (h *OutcomeHistory) Record(outcome *OutcomeRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	outcome.RecordedAt = time.Now()
	h.outcomes = append(h.outcomes, outcome)

	// Trim if over limit
	if len(h.outcomes) > h.limit {
		h.outcomes = h.outcomes[1:]
	}
}

// GetSimilar returns outcomes for similar queries.
func (h *OutcomeHistory) GetSimilar(query string, limit int) []*OutcomeRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var result []*OutcomeRecord
	queryLower := toLower(query)

	for i := len(h.outcomes) - 1; i >= 0 && len(result) < limit; i-- {
		outcome := h.outcomes[i]
		if containsSubstring(toLower(outcome.Query), queryLower) ||
			containsSubstring(queryLower, toLower(outcome.Query)) {
			result = append(result, outcome)
		}
	}

	return result
}

// Len returns the number of tracked outcomes.
func (h *OutcomeHistory) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.outcomes)
}

// RecordOutcome records the outcome of a recommendation.
func (a *Academic) RecordOutcome(recommendationID string, success bool, notes string) {
	outcome := &OutcomeRecord{
		ID:               uuid.New().String(),
		RecommendationID: recommendationID,
		Success:          success,
		Notes:            notes,
	}
	a.outcomeHistory.Record(outcome)
}

// =============================================================================
// Handoff Interface (ContextEvictable)
// =============================================================================

// AgentID returns the unique identifier for this agent instance.
func (a *Academic) AgentID() string {
	return a.id
}

// AgentType returns the type classification for this agent.
func (a *Academic) AgentType() string {
	return "academic"
}

// SetCanonicalID preserves the routing identity across handoff replacement.
func (a *Academic) SetCanonicalID(id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	a.id = id
}

// Descriptor returns the immutable metadata describing this agent type.
func (a *Academic) Descriptor() handoff.AgentDescriptor {
	modelID := a.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "academic",
		ModelID:               modelID,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryKnowledge,
		RuntimeProfiles:       academicRuntimeProfiles(),
		DefaultRuntimeProfile: academicDefaultRuntimeProfile(),
	}
}

// EvictEntries frees context by removing low-value entries from the working set.
// Returns the total number of tokens freed across all evicted candidates.
func (a *Academic) EvictEntries(candidates []handoff.EvictionCandidate) (freedTokens int, err error) {
	total := 0
	for _, candidate := range candidates {
		total += candidate.Entry.GetTokenCount()
	}
	return total, nil
}

// Terminate gracefully shuts down the agent.
func (a *Academic) Terminate(ctx context.Context) error {
	return a.Stop()
}

// SetHandoffBridge assigns the handoff bridge for this agent.
func (a *Academic) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	a.handoffBridge = bridge
	if bridge != nil {
		bridge.SetActivityPublisher(a.activityPub)
	}
}

// SetFetchPipeline wires the external fetch pipeline for web research.
// Called during Phase 3 wiring after Guardian and quarantine are ready.
func (a *Academic) SetFetchPipeline(p *fetch.Pipeline) {
	a.fetchPipeline = p
}

// SetKnowledgeStore wires the Academic to the shared knowledge-store coordinator.
func (a *Academic) SetKnowledgeStore(ks *knowledge.KnowledgeStore) {
	a.knowledgeMu.Lock()
	defer a.knowledgeMu.Unlock()
	a.knowledgeStore = ks
	if ks != nil {
		a.queryCoordinator = ks.Coordinator()
		return
	}
	a.queryCoordinator = nil
}

// SetKnowledgeBackend wires the Academic to committed knowledge retrieval.
func (a *Academic) SetKnowledgeBackend(backend committedKnowledgeSearcher) {
	a.knowledgeMu.Lock()
	defer a.knowledgeMu.Unlock()
	a.knowledgeBackend = backend
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (a *Academic) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	a.workspaceViews = authority.RestrictWorkspaceViews("academic", views)
}

// ExtractArchivableState returns the agent's current state for handoff persistence.
func (a *Academic) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   a.AgentID(),
		AgentType: a.AgentType(),
	}
}

// =============================================================================
// Guide Registration
// =============================================================================

// Registration returns the agent registration for the Guide.
func (a *Academic) Registration() *guide.AgentRegistration {
	return &guide.AgentRegistration{
		ID:      "academic",
		Name:    "academic",
		Aliases: []string{"research", "scholar"},
		Capabilities: guide.AgentCapabilities{
			Intents: []guide.Intent{guide.IntentRecall, guide.IntentCheck, guide.IntentFetch, guide.IntentHelp, guide.IntentChat},
			Domains: []guide.Domain{guide.DomainPatterns, guide.DomainDecisions, guide.DomainLearnings, guide.DomainResearch},
			Tags:    []string{"research", "best-practices", "external-knowledge", "web-fetch"},
			Keywords: []string{
				"research", "best practice", "recommend", "compare",
				"approach", "pattern", "methodology", "standard",
				"fetch", "download", "url", "web", "crawl", "document",
				"chat", "advise", "recommendation",
			},
			Priority: 50,
		},
		Constraints: guide.AgentConstraints{
			MinConfidence: 0.5,
		},
		Description:           "Researches external knowledge, best practices, and technical approaches. Always validates against codebase reality via Librarian.",
		Priority:              50,
		RuntimeProfiles:       academicRuntimeProfiles(),
		DefaultRuntimeProfile: academicDefaultRuntimeProfile(),
	}
}

// =============================================================================
// Skills System
// =============================================================================

// Skills returns the academic's skill registry.
func (a *Academic) Skills() *skills.Registry {
	return a.skills
}

// suppress unused import warnings — strings is used in tool_loop.go for TrimSpace
var _ = strings.TrimSpace
