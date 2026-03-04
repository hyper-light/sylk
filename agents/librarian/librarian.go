package librarian

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/providers/gateway"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// Default configuration values.
const (
	DefaultLibrarianModel    = "claude-sonnet-4-5-20251001"
	DefaultMaxToolRuns       = 12
	DefaultMaxTokens         = 8192
	DefaultMaxOutputTokens   = 8192
	DefaultLLMRequestTimeout = 60 * time.Second
	DefaultLLMRetryMax       = 3
	DefaultPromptCacheTTL    = 1 * time.Hour
)

// LibrarianProvider is the minimal interface the Librarian needs from its LLM.
// Satisfied by *providers.AnthropicProvider and *gateway.GatewayProvider.
type LibrarianProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// Librarian is the code search and pattern detection agent for the Sylk system.
// It serves as the SINGLE SOURCE OF TRUTH for formatters, linters, test frameworks,
// and coding patterns in a codebase. Uses an LLM-driven tool loop for rich
// contextual responses when EnableLLM is true.
type Librarian struct {
	id     string
	config Config
	logger *slog.Logger

	// LLM provider
	provider        LibrarianProvider
	providerMu      sync.RWMutex
	providerWrapper gateway.ProviderWrapper

	// Search and indexing
	searchHandler *SearchHandler
	domainFilter  *LibrarianDomainFilter

	// Skills system
	skills      *skills.Registry
	skillLoader *skills.Loader

	// Activity publisher for UI agent-panel updates.
	activityPub events.ActivityPublisher

	// Knowledge integration
	knowledgeStore *knowledge.KnowledgeStore

	// Event bus integration
	bus           guide.EventBus
	channels      *guide.AgentChannels
	requestSub    guide.Subscription
	responseSub   guide.Subscription
	registrySub   guide.Subscription
	knowledgeSub  guide.Subscription
	running       bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Request-scoped context lifecycle (mirrors academic pattern).
	runCtx         context.Context
	runCancel      context.CancelFunc
	requestMu      sync.Mutex
	requestCancels map[string]context.CancelFunc

	// Steering ledger management.
	steering *shared.SteeringManager

	// Request serialization: ensures at most one forwarded request
	// executes at a time.
	requestSerializer *shared.RequestSerializer

	// State mutex (guards id during handoff swap).
	mu sync.Mutex

	// Handoff integration
	handoffBridge *handoff.HandoffBridge
}

// Config holds configuration for the Librarian agent.
type Config struct {
	// Canonical agent ID. If empty, defaults to "librarian".
	ID string

	// LLM configuration
	EnableLLM         bool   // Enable LLM-driven conversation
	Model             string // LLM model ID (default: claude-sonnet-4-5-20251001)
	AnthropicAPIKey   string // Anthropic API key
	MaxToolRuns       int    // Maximum tool loop iterations (default: 12)
	MaxTokens         int    // Maximum output tokens for LLM (default: 8192)
	WorkingDirectory  string // Root directory for file operations

	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Search configuration
	SearchSystem SearchSystem // Optional: the search backend (nil when LLM is enabled)

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
		librarianID = "librarian"
	}

	l := &Librarian{
		id:                librarianID,
		config:            cfg,
		logger:            cfg.Logger,
		activityPub:       cfg.ActivityPub,
		domainFilter:      NewLibrarianDomainFilter(cfg.Logger),
		knownAgents:       make(map[string]*guide.AgentAnnouncement),
		steering:          shared.NewSteeringManager(),
		requestSerializer: shared.NewRequestSerializer(),
	}

	if cfg.SearchSystem != nil {
		l.searchHandler = NewSearchHandler(cfg.SearchSystem)
	}

	if len(provider) > 0 && provider[0] != nil {
		l.provider = provider[0]
	}

	l.steering.InitLazy("librarian", cfg.ActivityPub)
	l.initSkills()

	return l, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
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

func (l *Librarian) initSkills() {
	l.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"search_codebase", "find_pattern", "locate_symbol",
		"read_file", "glob", "grep", "git", "lsp", "ast_grep_search",
	}
	loaderCfg.AutoLoadDomains = []string{"code", "search", "filesystem", "git_read", "lsp", "ast"}
	l.skillLoader = skills.NewLoader(l.skills, loaderCfg)

	l.registerCoreSkills()
}

// Close closes the librarian and its resources.
func (l *Librarian) Close() error {
	l.Stop()
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

// SetProviderWrapper stores a callback that re-applies gateway rate limiting
// to fresh providers created during credential refresh.
func (l *Librarian) SetProviderWrapper(w gateway.ProviderWrapper) {
	l.providerWrapper = w
}

// ProviderType implements container.AuthRefreshable.
func (l *Librarian) ProviderType() string { return "anthropic" }

// RefreshProvider implements container.AuthRefreshable.
// Re-resolves Anthropic credentials and replaces the provider.
func (l *Librarian) RefreshProvider(ctx context.Context, authMethod string) error {
	cfg := providers.AnthropicConfig{
		BaseConfig: providers.BaseConfig{
			Model:     l.CurrentModel(),
			MaxTokens: l.config.MaxTokens,
		},
		AuthMode: authMethod,
	}
	p, err := providers.NewAnthropicProvider(ctx, cfg)
	if err != nil {
		return fmt.Errorf("librarian refresh provider: %w", err)
	}
	var wrapped LibrarianProvider = p
	if l.providerWrapper != nil {
		wrapped = l.providerWrapper(p)
	}
	l.SetProvider(wrapped)
	l.logger.Info("provider refreshed", "auth_method", authMethod)
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

	l.runCtx, l.runCancel = context.WithCancel(context.Background())
	l.requestCancels = make(map[string]context.CancelFunc)
	l.running = true
	l.logger.Info("librarian started", "channels", l.channels, "llm_enabled", l.config.EnableLLM)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (l *Librarian) Stop() error {
	if !l.running {
		return nil
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

	if !l.requestSerializer.Acquire(l.runCtx) {
		return nil // parent context done, agent shutting down
	}
	defer l.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	l.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(l.steering.EventLogger(), fwd, l.id)

	shared.EmitDispatchACK(l.bus, fwd.Metadata, l.id, "librarian", fwd.CorrelationID)
	l.publishActivity(events.EventTypeAgentAction, "Processing search request")

	if l.config.RequestGuard != nil {
		release := l.config.RequestGuard()
		defer release()
	}

	// Process the request with a cancellable request-scoped context.
	reqCtx, cancel := context.WithCancel(l.runCtx)
	l.registerRequestCancel(fwd.CorrelationID, cancel)
	l.steering.RegisterCancel(fwd.CorrelationID, cancel)
	defer l.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	// Create steering ledger for this request.
	ledger := l.steering.Create(fwd.CorrelationID, l.id, fwd.SessionID, nil, nil)
	defer l.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	startTime := time.Now()

	// Wire tool call emitter for inline visualization.
	emitter := shared.NewToolCallEmitter(l.bus, l.channels, "librarian", fwd.CorrelationID, fwd.SourceAgentID)
	ctx := shared.WithToolCallEmitter(reqCtx, emitter)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: l.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     l.id,
		SessionID:   fwd.SessionID,
	})
	ctx = shared.WithContextGovernor(ctx, shared.NewContextGovernor(
		l.config.Model, l.config.MaxTokens, 0,
	))

	result, err := l.processForwardedRequest(ctx, fwd)
	shared.LogResponse(l.steering.EventLogger(), fwd.CorrelationID, l.id, fwd.SessionID, time.Since(startTime), err)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "librarian",
		RespondingAgentName: "librarian",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		resp.Error = err.Error()
		l.publishActivity(events.EventTypeAgentError, fmt.Sprintf("Search failed: %s", err.Error()))
		errMsg := guide.NewErrorMessage(
			l.generateMessageID(),
			fwd.CorrelationID,
			l.id,
			err.Error(),
		)
		return l.bus.Publish(l.channels.Errors, errMsg)
	}

	resp.Data = result
	l.publishActivity(events.EventTypeAgentAction, "Search task completed")

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
	if l.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, l.config.SessionID, content)
	evt.AgentID = l.id
	evt.Visibility = events.VisibilityUser
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
	return l.skills.GetToolDefinitions()
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
	return handoff.AgentDescriptor{
		AgentType:     "librarian",
		ModelID:       l.CurrentModel(),
		ContextWindow: 200000,
		Category:      handoff.CategoryKnowledge,
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
	l.publishActivity(events.EventTypeAgentAction,
		fmt.Sprintf("knowledge ready: searchers %v", payload.Searchers))
	return nil
}

// SetHandoffBridge assigns the handoff bridge for this agent.
func (l *Librarian) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	l.handoffBridge = bridge
}

// ExtractArchivableState returns the agent's current state for handoff persistence.
func (l *Librarian) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   l.AgentID(),
		AgentType: l.AgentType(),
	}
}
