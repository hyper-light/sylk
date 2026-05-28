package librarian

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/authority"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// Default configuration values.
const (
	DefaultLibrarianModel    = "claude-sonnet-4-6"
	DefaultMaxToolRuns       = 32
	DefaultMaxTokens         = 8192
	DefaultMaxOutputTokens   = 8192
	DefaultLLMRequestTimeout = 60 * time.Second
	DefaultLLMRetryMax       = 3
	DefaultPromptCacheTTL    = 1 * time.Hour
	DefaultReplicaCount      = 6
	DefaultReplicaBacklog    = 24
)

// LibrarianProvider is the minimal interface the Librarian needs from its LLM.
// Satisfied by any provider wrapped through gateway.GatewayProvider.
// LibrarianProvider is the LLM interface the librarian requires.
// Streaming is mandatory — live chunks are how a chattable agent
// surfaces thought and text to the user; the tool loop always uses
// Stream. Complete is retained because the non-LLM callers on this
// interface (validation harnesses, some fabric consult paths) still
// use it; the tool loop itself does not.
type LibrarianProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
	Stream(ctx context.Context, req *providers.Request) (<-chan *providers.StreamChunk, error)
}

// Librarian is the code search and pattern detection agent for the Sylk system.
// It serves as the SINGLE SOURCE OF TRUTH for formatters, linters, test frameworks,
// and coding patterns in a codebase. Uses an LLM-driven tool loop for rich
// contextual responses when EnableLLM is true.
type Librarian struct {
	id       string
	config   Config
	logger   *slog.Logger
	identity *identity.AgentIdentity
	factory  *identity.Factory

	// LLM provider
	provider   LibrarianProvider
	providerMu sync.RWMutex
	refresher  container.ProviderRefresher

	// Search and indexing
	searchHandler *SearchHandler
	domainFilter  *LibrarianDomainFilter

	// Skills system
	skills        *skills.Registry
	skillLoader   *skills.Loader
	tools         *toolruntime.Runtime
	toolDefsDirty bool

	// Activity publisher for UI agent-panel updates.
	activityPub events.ActivityPublisher

	// Knowledge integration
	knowledgeStore   *knowledge.KnowledgeStore
	knowledgeBackend committedKnowledgeSearcher

	// Event bus integration
	bus          guide.EventBus
	channels     *guide.AgentChannels
	requestSub   guide.Subscription
	responseSub  guide.Subscription
	registrySub  guide.Subscription
	knowledgeSub guide.Subscription
	running      bool
	knownAgents  map[string]*guide.AgentAnnouncement

	// Request-scoped context lifecycle (mirrors academic pattern).
	runCtx         context.Context
	runCancel      context.CancelFunc
	requestMu      sync.Mutex
	requestCancels map[string]context.CancelFunc

	// Steering ledger management.
	steering *shared.SteeringManager

	// Request admission for forwarded work. Knowledge agents can serve multiple
	// forwarded consultations concurrently via isolated per-request replicas.
	requestPool *shared.RequestReplicaPool

	// Agent pod for Scribe feed and cross-agent coordination.
	agentPod *shared.AgentPod

	// Clone store for remote packages.
	cloneStore     *CloneStore
	sessionVFS     *versioning.SessionVFS
	workspaceViews versioning.WorkspaceViewAccess
	// fileAccess is a read-only session-routing FileAccess used by
	// ops that need overlay-modification introspection (list_changes,
	// prepare_write, etc.). Populated by SetSessionVFS; nil until a
	// SessionVFS is attached. The authority profile
	// (core/authority/profile.go) enforces read-only across all
	// three layers, so even a writable underlying FileAccess is
	// prevented from mutating anything through this handle.
	fileAccess versioning.FileAccess

	// State mutex (guards id during handoff swap).
	mu sync.Mutex

	// Handoff integration
	handoffBridge *handoff.HandoffBridge

	// Tracked goroutine scope for async claims dispatch.
	scope *concurrency.GoroutineScope

	claimsInbox *claims.ClaimsInbox

	// activeSessionID is the session ID from the most recent forwarded
	// request. Set at request entry, read by the claims board provider
	// closure when config.SessionID is unset (multi-session scenarios).
	activeSessionID atomic.Value // string — set per-request from fwd.SessionID
}

// SetScope injects the goroutine scope for async claims dispatch.
func (l *Librarian) SetScope(scope *concurrency.GoroutineScope) {
	l.scope = scope
}

// Config holds configuration for the Librarian agent.
type Config struct {
	// Canonical agent ID. If empty, defaults to "librarian".
	ID string

	// LLM configuration
	EnableLLM        bool   // Enable LLM-driven conversation
	Model            string // LLM model ID (default: gemini-3.1-pro-preview)
	AnthropicAPIKey  string // Anthropic API key
	MaxToolRuns      int    // Maximum tool loop iterations (default: 12)
	MaxTokens        int    // Maximum output tokens for LLM (default: 8192)
	WorkingDirectory string // Root directory for file operations

	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Search configuration
	SearchSystem     SearchSystem // Optional: the search backend (nil when LLM is enabled)
	KnowledgeBackend committedKnowledgeSearcher

	// ActivityPub publishes activity events so the UI agent panel tracks
	// this agent's lifecycle. Nil-safe (events silently dropped).
	ActivityPub events.ActivityPublisher

	// RequestGuard is called at handler entry to prevent activation demotion
	// during in-flight processing. Returns a release function. Nil-safe.
	RequestGuard func() func()

	// Session context
	SessionID string

	// Logging
	Logger *slog.Logger // Optional, uses slog.Default() if nil

	// Forwarded-request replica admission. These bound concurrent consults and
	// the waiting backlog so knowledge-agent overload degrades gracefully
	// instead of silently timing out upstream callers.
	MaxConcurrentForwarded int
	MaxQueuedForwarded     int

	// ContextQuota exposes global runtime pressure so autoscaling can stop
	// scaling up when aggregate context or goroutine headroom is exhausted.
	ContextQuota *container.ResourceQuota

	// Forest exposes Memory Forest recall/prediction skills.
	Forest shared.MemoryForestService

	// Factory mints the Librarian's AgentIdentity at New() time.
	// On-demand agent, created after phase4.
	Factory *identity.Factory
}

// New creates a new Librarian agent with optional LLM provider.
func New(cfg Config, provider ...LibrarianProvider) (*Librarian, error) {
	cfg = applyConfigDefaults(cfg)

	// SearchSystem is required when LLM is not enabled.
	if !cfg.EnableLLM && cfg.SearchSystem == nil {
		return nil, fmt.Errorf("search system is required when LLM is disabled")
	}

	librarianID := cfg.ID
	if librarianID == "" {
		librarianID = uuid.New().String()[:8]
	}

	l := &Librarian{
		id:               librarianID,
		config:           cfg,
		logger:           cfg.Logger,
		activityPub:      cfg.ActivityPub,
		domainFilter:     NewLibrarianDomainFilter(cfg.Logger),
		knownAgents:      make(map[string]*guide.AgentAnnouncement),
		knowledgeBackend: cfg.KnowledgeBackend,
		steering:         shared.NewSteeringManager(),
		// workspaceViews and fileAccess are bootstrapped here with a
		// disk-only fallback; SetSessionVFS swaps them for the real
		// session-routing implementations once the VFS is attached.
		// Pre-bind: view=global / view=pipeline fall back to disk
		// (silently) and list_changes returns "file access
		// unavailable" until SetSessionVFS runs. That's expected
		// until the orchestrator wires the session.
		workspaceViews: authority.RestrictWorkspaceViews("librarian", versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
			DefaultView:      versioning.WorkspaceViewDisk,
			DefaultSessionID: cfg.SessionID,
			WorkingDir:       cfg.WorkingDirectory,
			DiskFallback:     versioning.NewDiskFileAccess(cfg.WorkingDirectory, true),
		})),
	}
	l.requestPool = shared.NewAutoscalingRequestReplicaPool(shared.RequestReplicaPoolConfig{
		Name:              "librarian",
		HardMaxActive:     cfg.MaxConcurrentForwarded,
		HardMaxQueued:     cfg.MaxQueuedForwarded,
		CurrentModel:      l.CurrentModel,
		ProviderTelemetry: l.currentProviderAutoscaleSnapshot,
		ContextQuota:      cfg.ContextQuota,
	})

	if cfg.SearchSystem != nil {
		l.searchHandler = NewSearchHandler(cfg.SearchSystem)
	}

	if len(provider) > 0 && provider[0] != nil {
		l.provider = provider[0]
	}

	// Initialize clone store for remote package fetching.
	// SessionVFS is wired later via SetSessionVFS.
	l.cloneStore = NewCloneStore(cfg.WorkingDirectory)

	l.factory = cfg.Factory
	id, err := cfg.Factory.Mint(identity.MintOptions{
		Kind: identity.AgentTypeLibrarian,
		Pod:  identity.PodRef{ID: "librarian", Type: identity.PodTypeSingleton},
	})
	if err != nil {
		return nil, fmt.Errorf("librarian: mint identity: %w", err)
	}
	l.identity = id

	l.steering.InitLazy("librarian", cfg.ActivityPub)
	if err := l.initSkills(); err != nil {
		return nil, err
	}

	return l, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	cfg.SystemPrompt = shared.AppendDiskOnlyContext(cfg.SystemPrompt)
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.Model == "" {
		cfg.Model = DefaultLibrarianModel
	}
	if cfg.MaxToolRuns == 0 {
		cfg.MaxToolRuns = DefaultMaxToolRuns
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	if cfg.WorkingDirectory == "" {
		if wd, err := os.Getwd(); err == nil && wd != "" {
			cfg.WorkingDirectory = wd
		} else {
			cfg.WorkingDirectory = "."
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

func (l *Librarian) initSkills() error {
	l.skills = skills.NewRegistry()

	// Register skills BEFORE creating the loader. NewLoader calls
	// loadCoreSkills() immediately, which loads by name from the registry.
	// If skills aren't registered yet, the load silently fails and
	// buildToolDefinitions() returns nil — the LLM never sees tool
	// definitions and outputs tool calls as text instead of structured calls.
	l.registerCoreSkills()
	if err := shared.RegisterMemoryForestSkills(l.skills, "librarian", l.config.Forest, nil); err != nil {
		return fmt.Errorf("register librarian forest skills: %w", err)
	}

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = librarianVisibleSkillNames()
	loaderCfg.AutoLoadDomains = nil // progressive loading — no blanket domain loading
	l.skillLoader = skills.NewLoader(l.skills, loaderCfg)

	tools, err := toolruntime.New(toolruntime.Config{
		Registry: l.skills,
		Manifest: librarianToolManifest(l.skills),
		State:    toolruntime.NewState(),
	})
	if err != nil {
		return fmt.Errorf("initialize librarian tool runtime: %w", err)
	}
	l.tools = tools
	l.tools.SyncActiveFromLoaded()
	return nil
}

// Close closes the librarian and its resources.
func (l *Librarian) Close() error {
	if l.tools != nil {
		l.tools.Close()
		l.tools = nil
	}
	l.Stop()
	if l.requestPool != nil {
		l.requestPool.Close()
	}
	if l.cloneStore != nil {
		l.cloneStore.Close()
	}
	return nil
}

// =============================================================================
// Provider Management
// =============================================================================

// SetProvider sets or replaces the LLM provider at runtime. Thread-safe.
func (l *Librarian) SetProvider(p LibrarianProvider) {
	l.providerMu.Lock()
	defer l.providerMu.Unlock()
	l.provider = p
}

// getProvider returns the current provider under read lock.
func (l *Librarian) getProvider() LibrarianProvider {
	l.providerMu.RLock()
	defer l.providerMu.RUnlock()
	return l.provider
}

// Ready implements shared.ReadinessReporter.
func (l *Librarian) Ready() (bool, string) {
	if l.getProvider() == nil {
		return false, "LLM provider not yet wired (post-init / pre-auth window)"
	}
	return true, ""
}

// SetProviderRefresher stores a callback that creates a fresh provider for
// the current model and auth method. Set by cmd/tui.go at bootstrap.
func (l *Librarian) SetProviderRefresher(fn container.ProviderRefresher) {
	l.providerMu.Lock()
	defer l.providerMu.Unlock()
	l.refresher = fn
}

// ProviderType implements container.AuthRefreshable.
func (l *Librarian) ProviderType() string {
	return string(container.ProviderForModel(l.CurrentModel()))
}

// RefreshProvider implements container.AuthRefreshable.
func (l *Librarian) RefreshProvider(ctx context.Context, authMethod string) error {
	l.providerMu.RLock()
	fn := l.refresher
	l.providerMu.RUnlock()
	if fn == nil {
		return nil
	}
	p, err := fn(ctx, l.CurrentModel(), authMethod)
	if err != nil {
		return fmt.Errorf("librarian refresh provider: %w", err)
	}
	l.SetProvider(p)
	l.logger.Info("provider refreshed", "model", l.CurrentModel(), "auth_method", authMethod)
	return nil
}

// SwapModel implements container.ModelSwappable.
func (l *Librarian) SwapModel(_ context.Context, modelID string, provider providers.ProviderAdapter) error {
	l.SetProvider(provider)
	l.providerMu.Lock()
	l.config.Model = modelID
	l.providerMu.Unlock()
	l.logger.Info("model swapped", "model", modelID)
	return nil
}

// CurrentModel implements container.ModelSwappable.
func (l *Librarian) CurrentModel() string {
	l.providerMu.RLock()
	defer l.providerMu.RUnlock()
	if l.config.Model != "" {
		return l.config.Model
	}
	return DefaultLibrarianModel
}

func (l *Librarian) currentProviderAutoscaleSnapshot() shared.RequestReplicaProviderSnapshot {
	return shared.RequestReplicaProviderSnapshotFromProvider(l.getProvider())
}

// SupportedModels implements container.ModelSwappable.
func (l *Librarian) SupportedModels() []container.ModelOption {
	return []container.ModelOption{
		{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
		{ID: "gemini-3.1-pro-preview", DisplayName: "Gemini 3.1 Pro"},
	}
}

// Compile-time interface checks.
var (
	_ container.AuthRefreshable = (*Librarian)(nil)
	_ container.ModelSwappable  = (*Librarian)(nil)
)

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The librarian subscribes to its own channels and the registry topic.
func (l *Librarian) Start(bus guide.EventBus) error {
	if l.running {
		return fmt.Errorf("librarian is already running")
	}

	l.bus = bus
	l.channels = guide.NewAgentChannels("librarian", l.id)

	// Subscribe to own request channel (librarian.requests)
	var err error
	l.requestSub, err = bus.SubscribeAsync(l.channels.Requests, l.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", l.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	l.responseSub, err = bus.SubscribeAsync(l.channels.Responses, l.handleBusResponse)
	if err != nil {
		l.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", l.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	l.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, l.handleRegistryAnnouncement)
	if err != nil {
		l.requestSub.Unsubscribe()
		l.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	// Subscribe to knowledge readiness for telemetry.
	l.knowledgeSub, _ = bus.SubscribeAsync(guide.TopicKnowledgeReady, l.handleKnowledgeReady)

	// Claims intake: event-driven delta processing.
	if inbox := shared.WireClaimsIntake(shared.ClaimsIntakeConfig{
		AgentID:             l.id,
		SessionID:           l.config.SessionID,
		Bus:                 bus,
		Board:               l.librarianBoard(),
		Scope:               l.scope,
		ProcessEntry:        l.processClaimsEntry,
		Identity:            l.identity,
		Factory:             l.factory,
		ExpectedToolRuntime: l.toolRuntime(),
	}); inbox != nil {
		if err := inbox.Start(nil); err != nil {
			slog.Warn("librarian_claims_inbox_start_failed", "error", err.Error())
		}
		l.claimsInbox = inbox
	}

	l.runCtx, l.runCancel = context.WithCancel(context.Background())
	l.requestCancels = make(map[string]context.CancelFunc)
	l.running = true
	l.logger.Info("librarian started", "channels", l.channels, "llm_enabled", l.config.EnableLLM)

	// Lifecycle testament: started.
	l.librarianSubmitTestament(l.runCtx, l.librarianTestament(
		"Librarian agent started", "committed",
		[]*claims.Artifact{
			l.librarianArtifact("session_id", l.config.SessionID),
			l.librarianArtifact("agent_id", l.id),
			l.librarianArtifact("llm_enabled", fmt.Sprintf("%t", l.config.EnableLLM)),
		},
	))

	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (l *Librarian) Stop() error {
	if !l.running {
		return nil
	}

	// Lifecycle testament: stopping.
	l.librarianSubmitTestament(context.Background(), l.librarianTestament(
		"Librarian agent stopped", "committed",
		[]*claims.Artifact{l.librarianArtifact("session_id", l.config.SessionID)},
	))

	if l.claimsInbox != nil {
		_ = l.claimsInbox.Close()
		l.claimsInbox = nil
	}

	l.steering.CloseAll()
	if l.runCancel != nil {
		l.runCancel()
	}
	errs := l.unsubscribeAll()
	l.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	l.logger.Info("librarian stopped")
	return nil
}

func (l *Librarian) unsubscribeAll() []error {
	var errs []error
	if err := l.unsubscribeSafe(l.requestSub); err != nil {
		errs = append(errs, err)
	}
	l.requestSub = nil
	if err := l.unsubscribeSafe(l.responseSub); err != nil {
		errs = append(errs, err)
	}
	l.responseSub = nil
	if err := l.unsubscribeSafe(l.registrySub); err != nil {
		errs = append(errs, err)
	}
	l.registrySub = nil
	if err := l.unsubscribeSafe(l.knowledgeSub); err != nil {
		errs = append(errs, err)
	}
	l.knowledgeSub = nil
	return errs
}

func (l *Librarian) unsubscribeSafe(sub guide.Subscription) error {
	if sub == nil {
		return nil
	}
	return sub.Unsubscribe()
}

// IsRunning returns true if the librarian is actively processing bus messages.
func (l *Librarian) IsRunning() bool {
	return l.running
}

// Bus returns the event bus used by the librarian.
func (l *Librarian) Bus() guide.EventBus {
	return l.bus
}

// Channels returns the librarian's channel configuration.
func (l *Librarian) Channels() *guide.AgentChannels {
	return l.channels
}

// =============================================================================
// Request Handling
// =============================================================================

// handleBusRequest processes incoming forwarded requests from the event bus.
func (l *Librarian) handleBusRequest(msg *guide.Message) error {
	if msg.Type == guide.MessageTypeAction {
		return l.handleActionMessage(msg)
	}
	if msg.Type != guide.MessageTypeForward {
		return nil // Ignore non-forward messages
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	if strings.TrimSpace(fwd.SessionID) != "" {
		l.activeSessionID.Store(fwd.SessionID)
	}
	l.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(l.steering.EventLogger(), fwd, l.id)

	shared.EmitDispatchACK(l.bus, fwd.Metadata, l.id, "librarian", fwd.CorrelationID)
	reqCtx, cancel := context.WithCancel(l.runCtx)
	reqCtx = versioning.WithSessionID(reqCtx, fwd.SessionID)
	reqCtx = shared.WithForwardedTaskScope(reqCtx, fwd.Metadata)
	reqCtx = shared.WithGuardianCommandGate(reqCtx, shared.GuardianCommandGateConfig{
		BusProvider:     func() guide.EventBus { return l.bus },
		SourceAgentID:   func() string { return l.id },
		SourceAgentType: "librarian",
		SourceAgentName: "Librarian",
	})
	l.registerRequestCancel(fwd.CorrelationID, cancel)
	l.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer l.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	// Create steering ledger for this request.
	ledger := l.steering.Create(fwd.CorrelationID, l.id, fwd.SessionID, nil, nil)
	defer l.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	startTime := time.Now()

	// Request-scoped testament accumulator: collects per-search,
	// per-tool, and per-event observations. Flushed as ONE composite
	// testament when the request completes.
	acc := claims.NewTestamentAccumulator("librarian", fwd.SessionID)
	defer func() {
		acc.Flush(reqCtx, l.librarianBoard(), l.librarianScope())
	}()

	// Wire tool call emitter for inline visualization.
	emitter := shared.NewToolCallEmitter(l.bus, l.channels, l.id, fwd.CorrelationID, fwd.SourceAgentID)
	ctx := shared.WithForwardedStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID, fwd.ParentCorrelationID, fwd.Metadata)
	ctx = claims.WithTestamentAccumulator(ctx, acc)
	ctx = shared.WithOwnedStreamIdentity(ctx, "librarian", "Librarian")
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	ctx = shared.WithToolCallEmitter(ctx, emitter)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: l.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     l.id,
		SessionID:   fwd.SessionID,
	})
	if l.factory != nil && l.identity != nil {
		task, taskErr := l.factory.NewTask(identity.TaskOptions{
			DisplayID:   fwd.CorrelationID,
			Correlation: identity.CorrelationID(fwd.CorrelationID),
		})
		if taskErr != nil {
			return fmt.Errorf("librarian: mint task: %w", taskErr)
		}
		ctx = identity.WithIdentity(ctx, l.identity)
		ctx = identity.WithTask(ctx, task)
	}
	allowedHandoff := shared.AutomaticHandoffAllowedForForwardedRequest(fwd)
	ctx = shared.WithAutomaticHandoffEnabled(ctx, allowedHandoff)
	gov := shared.NewContextGovernor(l.CurrentModel(), l.config.MaxTokens, 0)
	if l.handoffBridge != nil && shared.AutomaticHandoffEnabled(ctx) {
		gov.OnBudgetExhausted = func(bctx context.Context) error {
			bridge := shared.EffectiveHandoffBridge(bctx, l.handoffBridge)
			if bridge == nil {
				return shared.ErrContextBudgetExhausted
			}
			return bridge.ForceHandoff(bctx, "context budget exhausted")
		}
	}
	ctx = shared.WithContextGovernor(ctx, gov)
	ctx = shared.WithProgressPublisher(ctx, &shared.ProgressPublisher{
		Bus: l.bus, Channels: l.channels,
		AgentID: l.id, CorrelationID: fwd.CorrelationID, SourceAgentID: fwd.SourceAgentID,
	})
	publishStreamLifecycle := guide.ShouldPublishForwardedStreamLifecycle(fwd)
	streamStarted := false
	var stopQueueKeepalive func()
	lease, acquireErr := l.requestPool.Acquire(reqCtx, shared.RequestReplicaAcquireOptions{
		SourceKey:         shared.RequestReplicaSourceKeyForForwardedRequest(fwd),
		Priority:          shared.RequestReplicaPriorityForForwardedRequest(fwd),
		PromptBytes:       shared.RequestReplicaPromptBytesForForwardedRequest(fwd),
		ContextFieldCount: shared.RequestReplicaContextFieldCount(fwd),
		OnQueued: func(snapshot shared.RequestReplicaPoolSnapshot, queuePosition int) {
			if publishStreamLifecycle && !streamStarted {
				shared.PublishStreamStart(l.bus, l.channels, ctx, l.id)
				streamStarted = true
			}
			l.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentAction, "Waiting for an available search replica", snapshot)
			if pp := shared.ProgressPublisherFromContext(ctx); pp != nil {
				pp.PublishState(events.AgentUIStateSearching, shared.KnowledgeQueueProgressMessage("librarian", snapshot, queuePosition))
			}
			acc.Record("replica_queued", fmt.Sprintf("position=%d active=%d/%d queued=%d/%d",
				queuePosition, snapshot.Active, snapshot.MaxActive, snapshot.Queued, snapshot.MaxQueued))
			acc.Note(fmt.Sprintf("Queued at position %d", queuePosition))
			stopQueueKeepalive = l.startQueueKeepalive(ctx, queuePosition)
		},
		OnGranted: func(snapshot shared.RequestReplicaPoolSnapshot, _ bool) {
			if stopQueueKeepalive != nil {
				stopQueueKeepalive()
				stopQueueKeepalive = nil
			}
			if publishStreamLifecycle && !streamStarted {
				shared.PublishStreamStart(l.bus, l.channels, ctx, l.id)
				streamStarted = true
			}
			l.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentAction, "Processing search request", snapshot)
			acc.Record("replica_granted", fmt.Sprintf("active=%d/%d", snapshot.Active, snapshot.MaxActive))
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
				Data:                shared.BusyRouteResponseData("librarian", busyErr.Snapshot, busyErr.Error()),
				Error:               busyErr.Error(),
				RespondingAgentID:   l.id,
				RespondingAgentName: "Librarian",
			}
			return l.bus.Publish(l.channels.Responses, guide.NewResponseMessage(l.generateMessageID(), resp))
		}
		return acquireErr
	}

	if l.config.RequestGuard != nil {
		release := l.config.RequestGuard()
		defer release()
	}

	if l.handoffBridge != nil {
		var replicaErr error
		var cleanupReplicaBridge func()
		ctx, cleanupReplicaBridge, _, replicaErr = shared.AttachReplicaHandoffBridge(ctx, l, l.handoffBridge, shared.KnowledgeReplicaHandoffOptions{
			Factory:           l.factory,
			ParentIdentity:    l.identity,
			SessionID:         fwd.SessionID,
			CorrelationID:     fwd.CorrelationID,
			ActivityPublisher: l.activityPub,
		})
		if replicaErr != nil {
			lease.Release()
			return replicaErr
		}
		defer cleanupReplicaBridge()
	}
	ctx = handoff.WithTransportRetryHandoff(ctx, handoff.TransportRetryHandoffConfig{
		Enabled:       shared.AutomaticHandoffEnabled(ctx),
		Bridge:        shared.EffectiveHandoffBridge(ctx, l.handoffBridge),
		AgentID:       l.id,
		AgentType:     "librarian",
		UserRequest:   fwd.Input,
		CorrelationID: fwd.CorrelationID,
		SessionID:     fwd.SessionID,
		EventLogger:   l.steering.EventLogger(),
		Scribe:        l.agentPod,
	})

	bundle, err := l.newForwardedToolBundle()
	if err != nil {
		lease.Release()
		return err
	}
	defer bundle.Close()

	// Bind to the issuer's parent claim. For consult / challenge
	// fulfillments the dispatcher (consult_peer / challenge_peer)
	// stamps parent_claim_id on the envelope so this binds to the
	// consultation/challenge claim, not the guide's route claim.
	// Bridge can then nest this agent's artifact tree under the
	// issuer's claim-backed consult/challenge row. UI_DESIGN.md
	// §4.7.3 — Layer 3 cycle-context propagation.
	var flushAccumulator func()
	var beginErr error
	ctx, flushAccumulator, beginErr = shared.BeginForwardedRequestCycle(ctx, l.id, fwd, l.scope)
	if beginErr != nil {
		lease.Release()
		return beginErr
	}
	defer flushAccumulator()

	result, err := l.processForwardedRequest(ctx, fwd, bundle)
	snapshot := lease.ReleaseWithObservation(shared.RequestReplicaObservation{
		Duration:    time.Since(startTime),
		TotalTokens: shared.TotalUsageTokens(usageAcc.Total()),
		Successful:  err == nil,
	})
	shared.LogResponse(l.steering.EventLogger(), fwd.CorrelationID, l.id, fwd.SessionID, time.Since(startTime), err)
	flushAccumulator()

	if err != nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		if publishStreamLifecycle {
			shared.PublishStreamError(l.bus, l.channels, ctx, l.id, err)
			shared.PublishStreamComplete(l.bus, l.channels, ctx, l.id, "", usageAcc.Total())
		}
		if fwd.FireAndForget {
			return nil
		}
		l.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeAgentError, fmt.Sprintf("Search failed: %s", err.Error()), snapshot)
		errMsg := guide.NewErrorMessage(
			l.generateMessageID(),
			fwd.CorrelationID,
			l.id,
			err.Error(),
		)
		return l.bus.Publish(l.channels.Errors, errMsg)
	}

	if publishStreamLifecycle {
		shared.PublishStreamComplete(l.bus, l.channels, ctx, l.id, "", usageAcc.Total())
	}
	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		RespondingAgentID:   l.id,
		RespondingAgentName: "Librarian",
		ProcessingTime:      time.Since(startTime),
		Data:                result,
	}
	l.publishReplicaActivityForRequest(fwd.SessionID, fwd.CorrelationID, events.EventTypeSuccess, "Search task completed", snapshot)

	if l.agentPod != nil {
		l.agentPod.FeedScribe("librarian", fwd.Input, fmt.Sprintf("%v", result), fwd.CorrelationID)
	}

	respMsg := guide.NewResponseMessage(l.generateMessageID(), resp)
	return l.bus.Publish(l.channels.Responses, respMsg)
}

func (l *Librarian) generateMessageID() string {
	return fmt.Sprintf("librarian_msg_%s", uuid.New().String()[:8])
}

func (l *Librarian) registerRequestCancel(correlationID string, cancel context.CancelFunc) {
	l.requestMu.Lock()
	if l.requestCancels != nil {
		l.requestCancels[correlationID] = cancel
	}
	l.requestMu.Unlock()
}

func (l *Librarian) clearRequestCancel(correlationID string) {
	l.requestMu.Lock()
	delete(l.requestCancels, correlationID)
	l.requestMu.Unlock()
}

func (l *Librarian) cancelRequest(correlationID string) {
	l.requestMu.Lock()
	cancel := l.requestCancels[correlationID]
	delete(l.requestCancels, correlationID)
	l.requestMu.Unlock()
	l.logger.Info("INTERRUPT_DEBUG: librarian_cancel_request_lookup",
		"correlation_id", correlationID,
		"found", cancel != nil,
	)
	if cancel != nil {
		cancel()
	}
}

func (l *Librarian) handleActionMessage(msg *guide.Message) error {
	action, ok := msg.GetActionRequest()
	if !ok || action == nil {
		return nil
	}
	if l.steering.HandleAction(action) {
		return nil
	}
	if action.Action == "cancel" {
		l.logger.Info("INTERRUPT_DEBUG: librarian_cancel_action_received",
			"correlation_id", action.CorrelationID,
			"target_agent", action.TargetAgentID,
			"source_agent", action.SourceAgentID,
		)
		shared.LogAgentEvent(l.steering.EventLogger(), agentlog.EventError,
			l.id, "", action.CorrelationID, "warn",
			&agentlog.ErrorPayload{Error: "request cancelled via action"})
		l.cancelRequest(action.CorrelationID)
	}
	return nil
}

// handleBusResponse processes responses to requests we made.
func (l *Librarian) handleBusResponse(msg *guide.Message) error {
	l.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events.
func (l *Librarian) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	// Skip self-announcements — the librarian must not register itself
	// in its own known-agents map. Self-registration causes incorrect
	// routing feedback and inflates the agent panel.
	if ann.AgentID == l.id || ann.AgentType == "librarian" {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		l.knownAgents[ann.AgentID] = ann
		l.logger.Debug("agent registered", "agent_id", ann.AgentID)
		shared.LogAgentEvent(l.steering.EventLogger(), agentlog.EventRegistryEvent,
			l.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "registered",
			})
	case guide.MessageTypeAgentUnregistered:
		delete(l.knownAgents, ann.AgentID)
		l.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
		shared.LogAgentEvent(l.steering.EventLogger(), agentlog.EventRegistryEvent,
			l.id, "", "", "info", &agentlog.RegistryPayload{
				AgentID: ann.AgentID, AgentType: ann.AgentType, Action: "unregistered",
			})
	}

	return nil
}

// GetKnownAgents returns all agents the librarian knows about.
func (l *Librarian) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make(map[string]*guide.AgentAnnouncement, len(l.knownAgents))
	for k, v := range l.knownAgents {
		result[k] = v
	}
	return result
}

// =============================================================================
// Direct API Methods
// =============================================================================

// Handle processes a LibrarianRequest directly (without event bus).
func (l *Librarian) Handle(ctx context.Context, req *LibrarianRequest) (*LibrarianResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	if l.searchHandler == nil {
		return nil, fmt.Errorf("search handler not configured")
	}

	switch req.Domain {
	case DomainCode:
		return l.searchHandler.Handle(ctx, req)
	case DomainPatterns:
		return l.handlePatternRequest(ctx, req)
	case DomainTooling:
		return l.handleToolingRequest(ctx, req)
	default:
		return l.searchHandler.Handle(ctx, req)
	}
}

// handlePatternRequest handles pattern detection requests.
func (l *Librarian) handlePatternRequest(ctx context.Context, req *LibrarianRequest) (*LibrarianResponse, error) {
	codeReq := &LibrarianRequest{
		ID:        req.ID,
		Intent:    req.Intent,
		Domain:    DomainCode,
		Query:     req.Query,
		Params:    req.Params,
		SessionID: req.SessionID,
		Timestamp: req.Timestamp,
	}

	resp, err := l.searchHandler.Handle(ctx, codeReq)
	if err != nil {
		return nil, err
	}

	if resp.Success && resp.Data != nil {
		if data, ok := resp.Data.(map[string]any); ok {
			data["domain"] = "patterns"
			data["analysis_type"] = "pattern_detection"
		}
	}

	return resp, nil
}

// handleToolingRequest handles tooling configuration requests.
func (l *Librarian) handleToolingRequest(_ context.Context, req *LibrarianRequest) (*LibrarianResponse, error) {
	start := time.Now()

	toolingPatterns := []string{
		".golangci.yml", ".golangci.yaml", "golangci.toml",
		".eslintrc", ".eslintrc.js", ".eslintrc.json",
		"prettier.config.js", ".prettierrc",
		"pyproject.toml", "setup.cfg", ".flake8",
		"Makefile", "justfile",
		"go.mod", "package.json", "Cargo.toml", "requirements.txt",
	}

	results := make([]map[string]any, 0)
	for _, pattern := range toolingPatterns {
		if l.domainFilter.IsCodeContent(pattern) {
			results = append(results, map[string]any{
				"pattern": pattern,
				"type":    "tooling_config",
			})
		}
	}

	return &LibrarianResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   true,
		Data: map[string]any{
			"tooling_patterns": toolingPatterns,
			"domain":           "tooling",
		},
		Took:      time.Since(start),
		Timestamp: time.Now(),
	}, nil
}

// =============================================================================
// Activity Publishing
// =============================================================================

// publishActivity emits a user-visible activity event so the UI agent panel
// tracks this librarian's lifecycle.
func (l *Librarian) publishActivity(eventType events.EventType, content string) {
	l.publishActivityForSession(l.config.SessionID, eventType, content)
}

func (l *Librarian) publishActivityForSession(sessionID string, eventType events.EventType, content string) {
	if l.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, strings.TrimSpace(sessionID), content)
	evt.AgentID = l.id
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "librarian"
	evt.Data["agent_name"] = "Librarian"
	l.activityPub.PublishActivity(evt)
}

// publishSystemEvent emits a system-level activity event for telemetry.
// System events are recorded but do not change the UI panel status.
func (l *Librarian) publishSystemEvent(eventType events.EventType, content string) {
	if l.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, l.config.SessionID, content)
	evt.AgentID = l.id
	evt.Visibility = events.VisibilitySystem
	evt.Data["agent_type"] = "librarian"
	evt.Data["agent_name"] = "Librarian"
	l.activityPub.PublishActivity(evt)
}

// =============================================================================
// Guide Registration
// =============================================================================

// GetRoutingInfo returns the librarian's routing information for Guide registration.
func (l *Librarian) GetRoutingInfo() *guide.AgentRoutingInfo {
	return LibrarianRoutingInfo(l.id)
}

// PublishRequest publishes a request to the Guide for routing.
func (l *Librarian) PublishRequest(req *guide.RouteRequest) error {
	if !l.running {
		return fmt.Errorf("librarian is not running")
	}

	req.SourceAgentID = l.id
	req.SourceAgentName = "librarian"

	msg := guide.NewRequestMessage(l.generateMessageID(), req)
	return l.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Skills System
// =============================================================================

// Skills returns the librarian's skill registry.
func (l *Librarian) Skills() *skills.Registry {
	return l.skills
}

// GetToolDefinitions returns tool definitions for all loaded skills.
func (l *Librarian) GetToolDefinitions() []map[string]any {
	return shared.ProviderToolsToDefinitions(l.buildToolDefinitions())
}

// =============================================================================
// Handoff Interface (ContextEvictable)
// =============================================================================

// AgentID returns the unique identifier for this agent instance.
func (l *Librarian) AgentID() string {
	return l.id
}

// SetCanonicalID overwrites the librarian's internal ID. Used during
// handoff swap so the new instance assumes the canonical identity.
func (l *Librarian) SetCanonicalID(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.id = id
}

// AgentType returns the type classification for this agent.
func (l *Librarian) AgentType() string {
	return "librarian"
}

// Descriptor returns the immutable metadata describing this agent type.
func (l *Librarian) Descriptor() handoff.AgentDescriptor {
	modelID := l.CurrentModel()
	return handoff.AgentDescriptor{
		AgentType:             "librarian",
		ModelID:               modelID,
		ContextWindow:         handoff.ContextWindowForModel(modelID),
		Category:              handoff.CategoryKnowledge,
		RuntimeProfiles:       librarianRuntimeProfiles(),
		DefaultRuntimeProfile: librarianDefaultRuntimeProfile(),
	}
}

// EvictEntries frees context by removing low-value entries from the working set.
func (l *Librarian) EvictEntries(candidates []handoff.EvictionCandidate) (freedTokens int, err error) {
	total := 0
	for _, candidate := range candidates {
		total += candidate.Entry.GetTokenCount()
	}
	return total, nil
}

// Terminate gracefully shuts down the agent.
func (l *Librarian) Terminate(_ context.Context) error {
	return l.Stop()
}

// SetSessionVFS wires the librarian's workspace-view and file-access
// layers to the live SessionVFS. Unlike earlier behavior (which
// deliberately discarded the argument and collapsed every read to
// disk), the librarian now genuinely honors its authority profile:
//
//   - WorkspaceViews permits reads across disk / global / pipeline
//     (core/authority/profile.go:96) but blocks writes.
//   - The attached FileAccess is session-routing + read-only, wrapped
//     by authority.RestrictFileAccess so list_changes /
//     prepare_write and similar ops work, while writes remain
//     blocked at the authority layer.
//
// Called with nil to tear the wiring down (session close, test
// cleanup); subsequent `workspace_read(op=…)` calls that need
// overlay access then surface the standard "file access is
// unavailable" error until a live SessionVFS is reattached.
func (l *Librarian) SetSessionVFS(svfs *versioning.SessionVFS) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sessionVFS = svfs
	diskFallback := versioning.NewDiskFileAccess(l.config.WorkingDirectory, true)
	l.workspaceViews = authority.RestrictWorkspaceViews("librarian", versioning.NewSessionWorkspaceViews(versioning.SessionWorkspaceViewsConfig{
		DefaultView:      versioning.WorkspaceViewDisk,
		DefaultSessionID: l.config.SessionID,
		WorkingDir:       l.config.WorkingDirectory,
		Session:          svfs,
		DiskFallback:     diskFallback,
	}))
	if svfs != nil {
		// The read-only global overlay FileAccess: VFSFileAccess
		// implements the overlayModificationReader interface the
		// list_changes skill looks for, so
		// workspace_read(op=list_changes) returns the session's
		// staged-but-uncommitted modifications. A routing wrapper
		// (e.g. NewSessionRoutingFileAccess) would re-route the API
		// but NOT expose Modifications(), so list_changes would
		// silently return an empty summary. We pick the overlay
		// handle directly and wrap it in the librarian authority
		// profile so writes stay blocked (RestrictFileAccess
		// enforces FileScopeDiskRead at the plumbing layer — even a
		// writable underlying FA cannot mutate through this handle).
		overlay := svfs.NewGlobalFileAccess(true)
		l.fileAccess = authority.RestrictFileAccess("librarian", overlay)
	} else {
		l.fileAccess = nil
	}
}

// SetKnowledgeStore wires the librarian to the progressive knowledge store.
// The coordinator is used eagerly — if boot already completed, its searchers
// are already set and the librarian starts fully operational.
func (l *Librarian) SetKnowledgeStore(ks *knowledge.KnowledgeStore) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.knowledgeStore = ks
}

// handleKnowledgeReady receives bus events when a knowledge layer is promoted.
// The coordinator's searchers are atomically set — this handler is for
// awareness and telemetry, not for swapping backends.
func (l *Librarian) handleKnowledgeReady(msg *guide.Message) error {
	payload, ok := msg.GetKnowledgeReadyPayload()
	if !ok {
		return nil
	}
	l.logger.Info("knowledge layer promoted",
		"level", payload.Level,
		"searchers", payload.Searchers,
	)
	l.publishSystemEvent(events.EventTypeAgentAction,
		fmt.Sprintf("knowledge ready: searchers %v", payload.Searchers))
	return nil
}

// SetAgentPod injects the agent pod for Scribe feed integration.
func (l *Librarian) SetAgentPod(pod *shared.AgentPod) {
	l.agentPod = pod
}

// SetHandoffBridge assigns the handoff bridge for this agent.
func (l *Librarian) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	l.handoffBridge = bridge
	if bridge != nil {
		bridge.SetActivityPublisher(l.activityPub)
	}
}

// ExtractArchivableState returns the agent's current state for handoff persistence.
func (l *Librarian) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   l.AgentID(),
		AgentType: l.AgentType(),
	}
}
