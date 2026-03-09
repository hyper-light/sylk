// Package academic implements the Academic agent for external knowledge research.
// The Academic researches best practices, papers, and external sources, always
// validating recommendations against codebase reality via the Librarian.
package academic

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/container"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/fetch"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/toolruntime"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// Default configuration values.
const (
	DefaultModel       = "gpt-5.4-pro"
	DefaultMaxToolRuns = 32
)

// academicProvider is the minimal interface the Academic needs from its LLM.
// Satisfied by *providers.AnthropicProvider.
type academicProvider interface {
	Complete(ctx context.Context, req *providers.Request) (*providers.Response, error)
}

// Academic is the main agent for researching external knowledge and best practices.
// It uses Claude Opus 4.5 for complex reasoning and synthesis of research findings.
type Academic struct {
	id           string
	config       Config
	domainFilter *AcademicDomainFilter
	logger       *slog.Logger

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

	// Request serialization: ensures at most one forwarded request
	// executes at a time.
	requestSerializer *shared.RequestSerializer

	// Synchronous consultation bus (Librarian responses).
	pendingMu       sync.Mutex
	pendingConsults map[string]chan *guide.Message

	// Research state
	mu            sync.RWMutex
	researchCache map[string]*ResearchResult
	sourceIndex   map[string]*Source

	// Outcome tracking for maturity-aware recommendations
	outcomeHistory *OutcomeHistory

	// External fetch pipeline — wired via SetFetchPipeline after construction.
	fetchPipeline *fetch.Pipeline

	workspaceViews versioning.WorkspaceViewAccess

	// Handoff integration
	handoffBridge *handoff.HandoffBridge
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
	MaxSources          int           // Max sources to consult per query (default: 10)
	CacheExpiry         time.Duration // How long to cache results (default: 30m)
	LibrarianTimeout    time.Duration // Timeout for Librarian consultation (default: 30s)
	RequireLibrarian    bool          // Require Librarian validation (default: true)
	MemoryThreshold     MemoryThreshold
	DefaultConfidence   ConfidenceLevel
	MinApplicability    float64 // Minimum applicability score to include (default: 0.3)
	OutcomeHistoryLimit int     // Max outcomes to track (default: 1000)

	// Logger
	Logger *slog.Logger

	// WorkspaceViews provides explicit disk/global/pipeline read access for
	// codebase-aware research grounding.
	WorkspaceViews versioning.WorkspaceViewAccess
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
		id:                academicID,
		config:            cfg,
		domainFilter:      NewAcademicDomainFilter(logger),
		logger:            logger,
		provider:          provider,
		activityPub:       cfg.ActivityPub,
		skills:            skillsRegistry,
		hooks:             hookRegistry,
		knownAgents:       make(map[string]*guide.AgentAnnouncement),
		pendingConsults:   make(map[string]chan *guide.Message),
		researchCache:     make(map[string]*ResearchResult),
		sourceIndex:       make(map[string]*Source),
		outcomeHistory:    NewOutcomeHistory(cfg.OutcomeHistoryLimit),
		steering:          shared.NewSteeringManager(),
		requestSerializer: shared.NewRequestSerializer(),
		workspaceViews:    cfg.WorkspaceViews,
	}

	a.steering.InitLazy("academic", cfg.ActivityPub)

	a.registerCoreSkills()
	a.registerExtendedSkills()
	a.registerFetchSkills()

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
	cfg.SystemPrompt = shared.AppendWorkspaceViewContext(cfg.SystemPrompt, shared.WorkspacePromptOptions{
		DefaultView:     versioning.WorkspaceViewDisk,
		IncludePipeline: true,
	})
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
	if cfg.LibrarianTimeout == 0 {
		cfg.LibrarianTimeout = 30 * time.Second
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

	if !a.requestSerializer.Acquire(a.runCtx) {
		return nil // parent context done, agent shutting down
	}
	defer a.requestSerializer.Release()

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	a.steering.BindSession(filepath.Join(".sylk", "sessions", fwd.SessionID), fwd.SessionID)
	shared.LogIncomingRequest(a.steering.EventLogger(), fwd, "academic")

	shared.EmitDispatchACK(a.bus, fwd.Metadata, "academic", "academic", fwd.CorrelationID)
	a.publishActivity(events.EventTypeAgentAction, "Processing research request")

	if a.config.RequestGuard != nil {
		release := a.config.RequestGuard()
		defer release()
	}

	// Process the request with a cancellable request-scoped context.
	reqCtx, cancel := context.WithCancel(a.runCtx)
	a.registerRequestCancel(fwd.CorrelationID, cancel)
	a.steering.RegisterCancel(fwd.CorrelationID, fwd.SessionID, cancel)
	defer a.clearRequestCancel(fwd.CorrelationID)
	defer cancel()

	// Create steering ledger for this request.
	ledger := a.steering.Create(fwd.CorrelationID, "academic", fwd.SessionID, nil, nil)
	defer a.steering.Close(fwd.CorrelationID, reqCtx.Err() != nil)

	startTime := time.Now()

	// Wire tool call emitter for inline visualization.
	emitter := shared.NewToolCallEmitter(a.bus, a.channels, a.id, fwd.CorrelationID, fwd.SourceAgentID)
	ctx := shared.WithStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx, usageAcc := shared.WithUsageAccumulator(ctx)
	ctx = shared.WithToolCallEmitter(ctx, emitter)
	ctx = shared.WithSteeringLedger(ctx, ledger)
	ctx = shared.WithLogMeta(ctx, shared.LogMeta{
		EventLogger: a.steering.EventLogger(),
		CorrID:      fwd.CorrelationID,
		AgentID:     "academic",
		SessionID:   fwd.SessionID,
	})
	ctx = versioning.WithSessionID(ctx, fwd.SessionID)
	gov := shared.NewContextGovernor(a.config.Model, a.config.MaxOutputTokens, 0)
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
	if !fwd.FireAndForget {
		shared.PublishStreamStart(a.bus, a.channels, ctx, a.id)
	}

	result, err := a.processForwardedRequest(ctx, fwd)
	shared.LogResponse(a.steering.EventLogger(), fwd.CorrelationID, "academic", fwd.SessionID, time.Since(startTime), err)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   a.id,
		RespondingAgentName: "Academic",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: fmt.Sprintf("request failed: %v", err)})
		}
		shared.PublishStreamError(a.bus, a.channels, ctx, a.id, err)
		shared.PublishStreamComplete(a.bus, a.channels, ctx, a.id, "", usageAcc.Total())
		resp.Error = err.Error()
		a.publishActivity(events.EventTypeAgentError, fmt.Sprintf("Research failed: %s", err.Error()))
		errMsg := guide.NewErrorMessage(
			generateMessageID(),
			fwd.CorrelationID,
			"academic",
			err.Error(),
		)
		return a.bus.Publish(a.channels.Errors, errMsg)
	}

	resp.Data = result
	shared.PublishStreamComplete(a.bus, a.channels, ctx, a.id, "", usageAcc.Total())
	a.publishActivity(events.EventTypeSuccess, "Research task completed")

	respMsg := guide.NewResponseMessage(generateMessageID(), resp)
	return a.bus.Publish(a.channels.Responses, respMsg)
}

func generateMessageID() string {
	return fmt.Sprintf("academic_msg_%s", uuid.New().String()[:8])
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
	if a.shouldHandleConversation(fwd) {
		return a.handleConversation(ctx, fwd)
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
	query := strings.TrimSpace(fwd.Input)
	if query == "" {
		return &ConversationResult{
			Response: "Tell me what you want researched or checked, and I’ll work through it with you.",
			Intent:   fwd.Intent,
		}, nil
	}

	handoff := academicConversationHandoffFromForwarded(fwd)
	toolSurface := toolruntime.Surface(a.toolRuntime())
	if handoff != nil && a.toolRuntime() != nil {
		if view, err := a.toolRuntime().RequestView("reroute_request"); err == nil {
			toolSurface = view
		}
	}
	a.prepareSkillsForInput(query)
	llmReq := &providers.Request{
		SystemPrompt: buildAcademicConversationSystemPrompt(a.config.SystemPrompt, handoff),
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: query}},
		Tools:        a.buildToolDefinitionsWithSurface(toolSurface),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(llmReq, "conversation")
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

func buildAcademicConversationSystemPrompt(base string, handoff *academicConversationHandoff) string {
	sections := []string{
		strings.TrimSpace(base),
		strings.TrimSpace(AcademicConversationPrompt),
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
	fetchPrompt := fmt.Sprintf(
		"The user wants to fetch or download external content. "+
			"If the request involves a git repository, use clone_via_librarian to have the Librarian clone it into the searchable package store. "+
			"If the request involves a web page or document and you do not already know the URL, use web_search first. "+
			"Once you know the URL, use web_fetch or fetch_document. "+
			"Provide a clear summary of what was fetched.\n\n%s",
		fwd.Input,
	)

	a.prepareSkillsForInput(fetchPrompt)
	llmReq := &providers.Request{
		SystemPrompt: a.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: fetchPrompt}},
		Tools:        a.buildToolDefinitions(),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(llmReq, "fetch")

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, a.toolRuntime())
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
	systemPrompt := a.config.SystemPrompt

	a.prepareSkillsForInput(fwd.Input)
	llmReq := &providers.Request{
		SystemPrompt: systemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: fwd.Input}},
		Tools:        a.buildToolDefinitions(),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(llmReq, "recall")

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, a.toolRuntime())
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
	checkPrompt := fmt.Sprintf(
		"Verify the following claim against your knowledge of best practices, "+
			"research, and technical standards. Use web_search when fresh external sources are needed, and use your tools to consult the "+
			"Librarian for codebase context. Provide a structured assessment:\n\n%s",
		fwd.Input,
	)

	a.prepareSkillsForInput(checkPrompt)
	llmReq := &providers.Request{
		SystemPrompt: a.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: checkPrompt}},
		Tools:        a.buildToolDefinitions(),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(llmReq, "check")

	shared.PrependHistoryMessages(llmReq, fwd.ConversationHistory)

	ledger := shared.SteeringLedgerFromContext(ctx)
	result, err := shared.ExecuteTurnLoop(ledger, llmReq, func() (string, error) {
		return a.executeToolLoop(ctx, llmReq, ledger, a.toolRuntime())
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

// routeSyncTimeout bounds how long the academic waits for a bus response.
var routeSyncTimeout = shared.DefaultConsultationTimeout

func (a *Academic) registerPendingConsult(correlationID string) <-chan *guide.Message {
	ch := make(chan *guide.Message, 1)
	a.pendingMu.Lock()
	a.pendingConsults[correlationID] = ch
	a.pendingMu.Unlock()
	return ch
}

func (a *Academic) clearPendingConsult(correlationID string) {
	a.pendingMu.Lock()
	delete(a.pendingConsults, correlationID)
	a.pendingMu.Unlock()
}

// deliverConsultResponse delivers terminal messages (response or error) to
// synchronous waiters. Stream events are filtered out.
func (a *Academic) deliverConsultResponse(msg *guide.Message) {
	if msg == nil || msg.CorrelationID == "" {
		return
	}
	if msg.Type != guide.MessageTypeResponse && msg.Type != guide.MessageTypeError {
		return
	}
	a.pendingMu.Lock()
	ch := a.pendingConsults[msg.CorrelationID]
	a.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

func (a *Academic) publishConsultRequest(req *guide.RouteRequest) error {
	if req == nil {
		return fmt.Errorf("route request is required")
	}
	if req.CorrelationID == "" {
		req.CorrelationID = "corr_" + uuid.NewString()
	}
	req.SourceAgentID = "academic"
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
	if req.CorrelationID == "" {
		req.CorrelationID = "corr_" + uuid.NewString()
	}

	waitCh := a.registerPendingConsult(req.CorrelationID)
	defer a.clearPendingConsult(req.CorrelationID)

	if err := a.publishConsultRequest(req); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, routeSyncTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("consultation to %q timed out after %s: %w",
			req.TargetAgentID, routeSyncTimeout, ctx.Err())
	case response := <-waitCh:
		return response, nil
	}
}

// requestConsultation is the high-level consultation helper that builds a
// RouteRequest, calls requestConsultSync, and returns ConsultationEvidence.
func (a *Academic) requestConsultation(
	ctx context.Context,
	target, query, scope, sessionID string,
) (*shared.ConsultationEvidence, error) {
	req := &guide.RouteRequest{
		Input:         query,
		TargetAgentID: target,
		SessionID:     sessionID,
	}
	response, err := a.requestConsultSync(ctx, req)
	if err != nil {
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

	req.SourceAgentID = "academic"
	req.SourceAgentName = "academic"

	msg := guide.NewRequestMessage(generateMessageID(), req)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Core Research Methods (LLM-driven)
// =============================================================================

// Research performs research on a topic via the LLM tool loop.
func (a *Academic) Research(ctx context.Context, query *ResearchQuery) (*ResearchResult, error) {
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
			"Use web_search to discover external sources when you do not already know the right URLs, then fetch primary sources before relying on specifics.\n\n"+
			"Topic: %s", query.Query,
	)
	if query.Domain != "" {
		researchPrompt += fmt.Sprintf("\nDomain: %s", query.Domain)
	}
	if query.LanguageFilter != "" {
		researchPrompt += fmt.Sprintf("\nLanguage: %s", query.LanguageFilter)
	}

	a.prepareSkillsForInput(researchPrompt)
	llmReq := &providers.Request{
		SystemPrompt: a.config.SystemPrompt,
		Messages:     []providers.Message{{Role: providers.RoleUser, Content: researchPrompt}},
		Tools:        a.buildToolDefinitions(),
		Model:        a.config.Model,
		MaxTokens:    a.config.MaxOutputTokens,
	}
	a.applyLLMRuntimeProfile(llmReq, "research")

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
	if a.activityPub == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, a.config.SessionID, content)
	evt.AgentID = "academic"
	evt.Visibility = events.VisibilityUser
	evt.Data["agent_type"] = "academic"
	evt.Data["agent_name"] = "Academic"
	a.activityPub.PublishActivity(evt)
}

// =============================================================================
// Caching
// =============================================================================

func (a *Academic) cacheKey(query *ResearchQuery) string {
	return fmt.Sprintf("%s:%s:%s", query.Query, query.Domain, query.LanguageFilter)
}

func (a *Academic) getCached(key string) *ResearchResult {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result, exists := a.researchCache[key]
	if !exists {
		return nil
	}

	// Check expiry
	if result.CachedAt != nil && time.Since(*result.CachedAt) > a.config.CacheExpiry {
		return nil
	}

	return result
}

func (a *Academic) setCached(key string, result *ResearchResult) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	result.CachedAt = &now
	a.researchCache[key] = result
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
}

// SetFetchPipeline wires the external fetch pipeline for web research.
// Called during Phase 3 wiring after Guardian and quarantine are ready.
func (a *Academic) SetFetchPipeline(p *fetch.Pipeline) {
	a.fetchPipeline = p
}

// SetWorkspaceViews injects explicit disk/global/pipeline read access.
func (a *Academic) SetWorkspaceViews(views versioning.WorkspaceViewAccess) {
	a.workspaceViews = views
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
