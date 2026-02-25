package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// =============================================================================
// Architect Agent
// =============================================================================

// Architect is the system design and planning agent for the Sylk system.
// It handles Pre-Delegation Planning Protocol and Atomic Task Generation.
// The Architect consults with Librarian for codebase patterns and creates
// workflow DAGs for task orchestration.
type Architect struct {
	config Config
	logger *slog.Logger
	logWAL io.Closer

	// Cross-domain handling
	crossDomainHandler *CrossDomainHandler
	synthesizer        *ResultSynthesizer

	// Skills system
	skills      *skills.Registry
	skillLoader *skills.Loader
	hooks       *skills.HookRegistry
	planner     planningLLM
	plannerMu   sync.RWMutex

	// Activity event bus for UI agent-panel updates
	activityBus *events.ActivityEventBus

	// Event bus integration
	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	// Planning state
	activePlans map[string]*DesignPlan
	planModes   map[string]*PlanModeState

	runMu         sync.RWMutex
	runCtx        context.Context
	runCancel     context.CancelFunc
	knownAgentsMu sync.RWMutex
	activePlansMu sync.RWMutex
	planModesMu   sync.RWMutex
	pendingMu     sync.Mutex
	pendingBus    map[string]chan *guide.Message
	inFlightMu    sync.Mutex
	inFlight      map[string]context.CancelFunc

	// Handoff bridge integration
	handoffBridge *handoff.HandoffBridge

	// File access abstraction (DiskFileAccess, set at creation).
	fileAccess versioning.FileAccess
}

// Config holds configuration for the Architect agent
type Config struct {
	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// LLM planning configuration
	EnableLLM          bool
	AnthropicAPIKey    string
	Model              string
	ThinkingBudget     int
	LLMRequestTimeout  time.Duration
	LLMRetryMax        int
	DisablePromptCache bool
	PromptCacheTTL     time.Duration

	// Cross-domain configuration
	CrossDomainTimeout time.Duration // Optional, defaults to 30s
	MaxConcurrent      int           // Optional, defaults to 3

	// Synthesis configuration
	SimilarityThreshold float64 // Optional, defaults to 0.8
	ConflictThreshold   float64 // Optional, defaults to 0.3
	MaxContentLength    int     // Optional, defaults to 10000
	WorkingDirectory    string  // Optional, defaults to current working directory

	// Consultation/delegation policy
	MandatoryConsultation            bool          // Optional, defaults to true
	AllowPlanningWithoutConsultation bool          // Optional, defaults to false
	ConsultationTimeout              time.Duration // Optional, defaults to 20s
	ConsultationMaxAge               time.Duration // Optional, defaults to 5m

	// File access (optional — if nil, Architect uses direct disk I/O).
	FileAccess versioning.FileAccess

	// PlannerProviderWrapper wraps the raw Anthropic provider for rate limiting.
	// The returned value must implement PlannerStreamProvider (e.g. *gateway.GatewayProvider).
	// Called each time the planner is lazily created, preserving auth refresh semantics.
	PlannerProviderWrapper func(*providers.AnthropicProvider) PlannerStreamProvider

	// ActivityBus for publishing agent activity events to the UI panel.
	ActivityBus *events.ActivityEventBus

	// Tool dispatch loop
	MaxToolRuns int // Maximum tool-call turns per conversation request. Defaults to DefaultMaxToolRuns (12).

	// Logging
	Logger *slog.Logger // Optional, defaults to per-agent WAL logger if nil.

	logWAL io.Closer
}

// Default configuration values
const (
	DefaultMaxOutputTokens   = 16384
	DefaultThinkingBudget    = 8192
	DefaultArchitectModel    = "claude-opus-4-6"
	DefaultLLMRequestTimeout = 120 * time.Second
	DefaultLLMRetryMax       = 3
	DefaultPromptCacheTTL    = 1 * time.Hour
	DefaultCrossDomainTimeout  = 30 * time.Second
	DefaultMaxConcurrent       = 3
	DefaultSimilarityThreshold = 0.8
	DefaultConflictThreshold   = 0.3
	DefaultMaxContentLength    = 10000
	DefaultConsultationTimeout = 20 * time.Second
	DefaultConsultationMaxAge  = 5 * time.Minute
	DefaultSkillMaxLoaded      = 16
	DefaultSkillTokenBudget    = 3200
	DefaultMaxToolRuns         = 12
)

// logInfo logs at Info level, safe to call when a.logger is nil.
func (a *Architect) logInfo(msg string, args ...any) {
	if a != nil && a.logger != nil {
		a.logger.Info(msg, args...)
	}
}

// logWarn logs at Warn level, safe to call when a.logger is nil.
func (a *Architect) logWarn(msg string, args ...any) {
	if a != nil && a.logger != nil {
		a.logger.Warn(msg, args...)
	}
}

func (a *Architect) publishActivity(eventType events.EventType, content string) {
	a.publishActivityWithVisibility(eventType, events.VisibilityUser, content)
}

func (a *Architect) publishActivityWithVisibility(eventType events.EventType, visibility events.EventVisibility, content string) {
	if a.activityBus == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, "default", content)
	evt.AgentID = a.AgentID()
	evt.Visibility = visibility
	evt.Data["agent_type"] = "architect"
	evt.Data["agent_name"] = "Architect"
	a.activityBus.Publish(evt)
}

// New creates a new Architect agent
func New(cfg Config) (*Architect, error) {
	cfg = applyConfigDefaults(cfg)
	if err := ensureArchitectLogger(&cfg); err != nil {
		return nil, err
	}

	architect := &Architect{
		config:      cfg,
		logger:      cfg.Logger,
		logWAL:      cfg.logWAL,
		activityBus: cfg.ActivityBus,
		fileAccess:  cfg.FileAccess,
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		activePlans: make(map[string]*DesignPlan),
		planModes:   make(map[string]*PlanModeState),
		pendingBus:  make(map[string]chan *guide.Message),
		inFlight:    make(map[string]context.CancelFunc),
	}

	architect.initCrossDomain(cfg)
	architect.initSynthesizer(cfg)
	architect.initSkills()
	if err := architect.initPlanner(cfg); err != nil {
		return nil, err
	}
	if err := architect.restorePersistedPlans(); err != nil {
		architect.logger.Warn("failed to restore persisted plans", "error", err)
	}

	return architect, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.Model == "" {
		cfg.Model = DefaultArchitectModel
	}
	if cfg.ThinkingBudget == 0 {
		cfg.ThinkingBudget = DefaultThinkingBudget
	}
	if cfg.LLMRequestTimeout == 0 {
		cfg.LLMRequestTimeout = DefaultLLMRequestTimeout
	}
	if cfg.LLMRetryMax == 0 {
		cfg.LLMRetryMax = DefaultLLMRetryMax
	}
	if cfg.PromptCacheTTL == 0 {
		cfg.PromptCacheTTL = DefaultPromptCacheTTL
	}
	if cfg.CrossDomainTimeout == 0 {
		cfg.CrossDomainTimeout = DefaultCrossDomainTimeout
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.SimilarityThreshold == 0 {
		cfg.SimilarityThreshold = DefaultSimilarityThreshold
	}
	if cfg.ConflictThreshold == 0 {
		cfg.ConflictThreshold = DefaultConflictThreshold
	}
	if cfg.MaxContentLength == 0 {
		cfg.MaxContentLength = DefaultMaxContentLength
	}
	if cfg.WorkingDirectory == "" {
		if wd, err := os.Getwd(); err == nil && wd != "" {
			cfg.WorkingDirectory = wd
		} else {
			cfg.WorkingDirectory = "."
		}
	}
	if cfg.ConsultationTimeout == 0 {
		cfg.ConsultationTimeout = DefaultConsultationTimeout
	}
	if cfg.ConsultationMaxAge == 0 {
		cfg.ConsultationMaxAge = DefaultConsultationMaxAge
	}
	if cfg.MaxToolRuns == 0 {
		cfg.MaxToolRuns = DefaultMaxToolRuns
	}
	if cfg.AllowPlanningWithoutConsultation {
		cfg.MandatoryConsultation = false
	}
	if !cfg.MandatoryConsultation && !cfg.AllowPlanningWithoutConsultation {
		cfg.MandatoryConsultation = true
	}
	return cfg
}

func ensureArchitectLogger(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("architect config is nil")
	}
	if cfg.Logger != nil {
		return nil
	}
	logger, closer, err := agentlog.NewWALLogger("architect")
	if err != nil {
		return fmt.Errorf("create architect wal logger: %w", err)
	}
	cfg.Logger = logger
	cfg.logWAL = closer
	return nil
}

func (a *Architect) initCrossDomain(cfg Config) {
	a.crossDomainHandler = NewCrossDomainHandler(&CrossDomainHandlerConfig{
		Timeout:       cfg.CrossDomainTimeout,
		MaxConcurrent: cfg.MaxConcurrent,
		Logger:        cfg.Logger,
		QueryHandler:  a.handleDomainQuery,
	})
}

func (a *Architect) initSynthesizer(cfg Config) {
	a.synthesizer = NewResultSynthesizer(&SynthesizerConfig{
		SimilarityThreshold: cfg.SimilarityThreshold,
		ConflictThreshold:   cfg.ConflictThreshold,
		MaxContentLength:    cfg.MaxContentLength,
	})
}

func (a *Architect) initSkills() {
	a.skills = skills.NewRegistry()
	a.hooks = skills.NewHookRegistry()
	a.registerCoreSkills()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.MaxLoadedSkills = DefaultSkillMaxLoaded
	loaderCfg.TokenBudget = DefaultSkillTokenBudget
	loaderCfg.CoreSkills = architectCoreSkillNames()
	loaderCfg.AutoLoadDomains = nil
	a.skillLoader = skills.NewLoader(a.skills, loaderCfg)
	registerArchitectSafetyHook(a.hooks, architectAllSkillNames())
}

// Close closes the architect and its resources
func (a *Architect) Close() error {
	stopErr := a.Stop()
	if a.logWAL == nil {
		return stopErr
	}
	closeErr := a.logWAL.Close()
	a.logWAL = nil
	return errors.Join(stopErr, closeErr)
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The architect subscribes to its own channels and the registry topic.
func (a *Architect) Start(bus guide.EventBus) error {
	if a.running {
		return fmt.Errorf("architect is already running")
	}

	a.setRunContext(context.Background())

	a.bus = bus
	a.channels = guide.NewAgentChannels("architect", "architect")

	// Subscribe to own request channel (architect.requests)
	var err error
	a.requestSub, err = bus.SubscribeAsync(a.channels.Requests, a.handleBusRequest)
	if err != nil {
		a.cancelRunContext()
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	a.responseSub, err = bus.SubscribeAsync(a.channels.Responses, a.handleBusResponse)
	if err != nil {
		a.requestSub.Unsubscribe()
		a.cancelRunContext()
		return fmt.Errorf("failed to subscribe to %s: %w", a.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	a.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, a.handleRegistryAnnouncement)
	if err != nil {
		a.requestSub.Unsubscribe()
		a.responseSub.Unsubscribe()
		a.cancelRunContext()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	a.running = true
	a.logger.Info("architect started", "channels", a.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (a *Architect) Stop() error {
	if !a.running {
		return nil
	}

	a.cancelRunContext()
	errs := a.unsubscribeAll()
	a.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	a.logger.Info("architect stopped")
	return nil
}

func (a *Architect) unsubscribeAll() []error {
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
	return errs
}

func (a *Architect) unsubscribeRequest() error {
	if a.requestSub == nil {
		return nil
	}
	err := a.requestSub.Unsubscribe()
	a.requestSub = nil
	return err
}

func (a *Architect) unsubscribeResponse() error {
	if a.responseSub == nil {
		return nil
	}
	err := a.responseSub.Unsubscribe()
	a.responseSub = nil
	return err
}

func (a *Architect) unsubscribeRegistry() error {
	if a.registrySub == nil {
		return nil
	}
	err := a.registrySub.Unsubscribe()
	a.registrySub = nil
	return err
}

// IsRunning returns true if the architect is actively processing bus messages
func (a *Architect) IsRunning() bool {
	return a.running
}

// Bus returns the event bus used by the architect
func (a *Architect) Bus() guide.EventBus {
	return a.bus
}

// Channels returns the architect's channel configuration
func (a *Architect) Channels() *guide.AgentChannels {
	return a.channels
}

// =============================================================================
// Request Handling
// =============================================================================

// handleBusRequest processes incoming forwarded requests from the event bus
func (a *Architect) handleBusRequest(msg *guide.Message) error {
	ctx := a.processingContext()
	if err := ctx.Err(); err != nil {
		return nil
	}
	return a.dispatchBusRequest(ctx, msg)
}

func (a *Architect) dispatchBusRequest(ctx context.Context, msg *guide.Message) error {
	if msg == nil {
		return nil
	}
	if msg.Type == guide.MessageTypeForward {
		return a.handleForwardBusRequest(ctx, msg)
	}
	if msg.Type == guide.MessageTypeAction {
		return a.handleActionBusRequest(ctx, msg)
	}
	if msg.Type == guide.MessageTypeProposal {
		return a.handleProposalBusRequest(ctx, msg)
	}
	return nil
}

func (a *Architect) handleForwardBusRequest(ctx context.Context, msg *guide.Message) error {
	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	startTime := time.Now()
	reqCtx, cancel := context.WithCancel(ctx)
	reqCtx = withArchitectStreamContext(reqCtx, fwd.CorrelationID, fwd.SourceAgentID)
	reqCtx, usageAcc := withArchitectUsageAccumulator(reqCtx)
	reqCtx = withArchitectEarlyUsageEmitter(reqCtx, func(inputTokens int) {
		a.publishPlanStreamEarlyUsage(reqCtx, inputTokens)
	})
	reqCtx = withStreamRetryResetEmitter(reqCtx, func() {
		a.publishPlanStreamStart(reqCtx)
	})
	a.registerInFlight(fwd.CorrelationID, cancel)
	defer a.clearInFlight(fwd.CorrelationID)
	defer cancel()
	if !fwd.FireAndForget {
		a.publishPlanStreamStart(reqCtx)
	}
	result, err := a.processForwardedRequest(reqCtx, fwd)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   "architect",
		RespondingAgentName: "architect",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		if a.isInterruptError(err) {
			// Publish a stream complete FIRST so the TUI receives
			// StreamCompleteMsg and clears the activeRoute before the
			// GuideResponseMsg arrives. Without this, the stale
			// activeRoute causes subsequent messages to be enqueued
			// without a thinking indicator. Using StreamComplete
			// (rather than StreamError) means:
			//  1. For user Ctrl+C: shouldRenderStreamEvent returns
			//     false (CID in interruptedCorrelations) so no
			//     duplicate display.
			//  2. For system interrupts: the thinking entry resolves
			//     cleanly with the interrupted text.
			//  3. The subsequent GuideResponseMsg error is suppressed
			//     by shouldSuppressErrorAfterSuccess.
			a.publishPlanStreamComplete(reqCtx, "(interrupted)", usageAcc.Total(), nil)
			// Then publish a minimal response so the Guide records
			// something in conversation history rather than leaving
			// an empty agent reply that causes context loss.
			a.publishInterruptResponse(fwd)
			return nil
		}
		a.publishPlanStreamError(reqCtx, err)
		resp.Error = err.Error()
		// Publish to error channel
		errMsg := guide.NewErrorMessage(
			a.generateMessageID(),
			fwd.CorrelationID,
			"architect",
			err.Error(),
		)
		return a.bus.Publish(a.channels.Errors, errMsg)
	}

	resp.Data = result

	// Handoff reroute events are emitted INSIDE dispatchPlanExecution and
	// stepAutoHandoff — before the synchronous requestRouteSync call — so
	// the TUI switches to the orchestrator while it is actively processing.
	// No reroute emission here; it already happened at the right time.

	// Always include the authoritative response text in the completion
	// event. The bridge stores it as AuthoritativeText on StreamCompleteMsg
	// so the chat model can correct dropped or reordered stream chunks.
	directive := extractResponseDirective(result)
	completeText := extractUserResponse(result)
	a.publishPlanStreamComplete(reqCtx, completeText, usageAcc.Total(), directive)

	// Publish response to own response channel
	respMsg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(a.channels.Responses, respMsg)
}

// publishInterruptResponse sends a minimal response to the Guide when a
// request is interrupted (context canceled). Without this, the Guide's
// conversation history records the user input but no agent reply, causing
// context loss on subsequent turns.
//
// Success is set to true so the TUI's shouldSuppressStreamedRouteResponse
// suppresses this GuideResponseMsg (the stream-complete event already
// delivered the "(interrupted)" text and cleared the activeRoute).
func (a *Architect) publishInterruptResponse(fwd *guide.ForwardedRequest) {
	if a == nil || a.bus == nil || a.channels == nil || fwd == nil {
		return
	}
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             true,
		RespondingAgentID:   "architect",
		RespondingAgentName: "architect",
		Data:                &ConversationResult{Response: "(interrupted)", Intent: IntentConverse},
	}
	msg := guide.NewResponseMessage(a.generateMessageID(), resp)
	_ = a.bus.Publish(a.channels.Responses, msg)
}

func (a *Architect) handleActionBusRequest(ctx context.Context, msg *guide.Message) error {
	req, ok := msg.GetActionRequest()
	if !ok {
		return fmt.Errorf("invalid action request payload")
	}
	if req == nil {
		return nil
	}
	if strings.EqualFold(req.Action, "proposal") {
		return a.handleProposalAction(ctx, req)
	}
	if strings.EqualFold(req.Action, "read_research_paper") {
		return a.handleReadResearchAction(ctx, req)
	}
	if isCancelAction(req.Action) {
		return a.handleCancelAction(req)
	}
	return nil
}

func (a *Architect) handleProposalBusRequest(ctx context.Context, msg *guide.Message) error {
	req := &guide.ActionRequest{
		CorrelationID: msg.CorrelationID,
		SourceAgentID: msg.SourceAgentID,
		Action:        "proposal",
		Data:          msg.Payload,
	}
	return a.handleProposalAction(ctx, req)
}

func (a *Architect) generateMessageID() string {
	return fmt.Sprintf("architect_msg_%s", uuid.New().String())
}

func (a *Architect) registerInFlight(correlationID string, cancel context.CancelFunc) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" || cancel == nil {
		return
	}
	a.inFlightMu.Lock()
	a.inFlight[correlationID] = cancel
	a.inFlightMu.Unlock()
}

func (a *Architect) clearInFlight(correlationID string) {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	a.inFlightMu.Lock()
	delete(a.inFlight, correlationID)
	a.inFlightMu.Unlock()
}

func (a *Architect) cancelInFlight(correlationID string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	var cancel context.CancelFunc
	a.inFlightMu.Lock()
	cancel = a.inFlight[correlationID]
	delete(a.inFlight, correlationID)
	a.inFlightMu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func isCancelAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "cancel", "interrupt", "stop":
		return true
	default:
		return false
	}
}

// processForwardedRequest handles the actual request processing
func (a *Architect) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := a.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

func (a *Architect) setRunContext(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	runCtx, cancel := context.WithCancel(parent)

	a.runMu.Lock()
	a.runCtx = runCtx
	a.runCancel = cancel
	a.runMu.Unlock()
}

func (a *Architect) processingContext() context.Context {
	a.runMu.RLock()
	ctx := a.runCtx
	a.runMu.RUnlock()
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

func (a *Architect) cancelRunContext() {
	a.runMu.Lock()
	cancel := a.runCancel
	a.runCancel = nil
	a.runMu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (a *Architect) isInterruptError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (a *Architect) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentExecute:
		return a.handleExecute, nil
	case guide.IntentPlan, guide.IntentDesign:
		return a.handleConversation, nil
	case guide.IntentRecall:
		return a.handleRecall, nil
	case guide.IntentCheck:
		return a.handleCheck, nil
	case guide.IntentHelp, guide.IntentChat, guide.IntentUnknown:
		return a.handleConversation, nil
	default:
		return a.handleConversation, nil
	}
}

// handleRecall processes recall requests
func (a *Architect) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:                  uuid.New().String(),
		Intent:              IntentRecall,
		Query:               fwd.Input,
		SessionID:           sessionIDFromForwarded(fwd),
		Timestamp:           time.Now(),
		Params:              forwardedRequestParams(fwd),
		ConversationHistory: fwd.ConversationHistory,
	}

	return a.Handle(ctx, req)
}

// handleCheck processes check/verification requests
func (a *Architect) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:                  uuid.New().String(),
		Intent:              IntentCheck,
		Query:               fwd.Input,
		SessionID:           sessionIDFromForwarded(fwd),
		Timestamp:           time.Now(),
		Params:              forwardedRequestParams(fwd),
		ConversationHistory: fwd.ConversationHistory,
	}

	return a.Handle(ctx, req)
}

// handleConversation processes conversational requests from the event bus.
// Both IntentPlan and IntentDesign route here so the architect converses
// naturally before formalizing a plan.
func (a *Architect) handleConversation(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.logInfo("handleConversation: entry",
		"input", truncateString(fwd.Input, 80),
		"intent", string(fwd.Intent))

	// When a ready plan exists, check whether the user is approving it
	// (affirmative response) or giving feedback. Approval routes to
	// handleExecute which dispatches to the orchestrator. Feedback
	// routes to handlePlanFeedback for the LLM to address concerns.
	//
	// This check is the primary approval gate when chatting directly
	// with the architect. The Guide's phase gate (tryPhaseClassification)
	// provides a secondary path for Guide-mediated routing.
	if plan := a.latestReadyPlan(); plan != nil {
		if isAffirmativeResponse(fwd.Input) {
			a.logInfo("handleConversation: affirmative response detected, routing to execute",
				"plan_id", plan.ID)
			return a.handleExecute(ctx, fwd)
		}
		return a.handlePlanFeedback(ctx, fwd, plan)
	}

	// Check if user is confirming plan formalization after conversation.
	if plan, ok := a.tryFormalizePlan(ctx, fwd); ok {
		a.logInfo("handleConversation: formalized plan")
		return plan, nil
	}

	req := &ArchitectRequest{
		ID:                  uuid.New().String(),
		Intent:              mapGuideIntentToArchitect(fwd.Intent),
		Query:               fwd.Input,
		SessionID:           sessionIDFromForwarded(fwd),
		Timestamp:           time.Now(),
		Params:              forwardedRequestParams(fwd),
		ConversationHistory: fwd.ConversationHistory,
	}

	a.logInfo("handleConversation: routing to Handle",
		"intent", string(req.Intent))
	return a.Handle(ctx, req)
}

// handlePlanFeedback processes user feedback on a ready plan. The phase gate
// classified the input as negative polarity (feedback/rejection), so we route
// to the LLM to address their concerns directly.
func (a *Architect) handlePlanFeedback(ctx context.Context, fwd *guide.ForwardedRequest, plan *DesignPlan) (any, error) {
	a.logInfo("handlePlanFeedback: entry",
		"input", truncateString(fwd.Input, 80),
		"plan_id", plan.ID)

	ctx = withPlannerThoughtCallback(ctx, func(stage string, thought string) {
		a.publishPlanThought(ctx, stage, thought)
	})

	request := plannerConversationRequest{
		Mode:                plannerConversationModeFeedback,
		UserQuery:           fwd.Input,
		PlanSummary:         formatPlanForChat(plan),
		ConversationHistory: fwd.ConversationHistory,
		OnChunk: func(text string) {
			a.publishPlanStreamChunk(ctx, text)
		},
	}

	response, err := a.composeUserFacingResponse(ctx, request)
	if err != nil {
		a.logWarn("handlePlanFeedback: compose failed", "error", err)
		return &ConversationResult{
			Response:  "I couldn't process your feedback right now. Could you rephrase?",
			Intent:    IntentConverse,
			Directive: a.feedbackReadyDirective(plan),
		}, nil
	}

	return &ConversationResult{
		Response:  response,
		Intent:    IntentConverse,
		Directive: a.feedbackReadyDirective(plan),
	}, nil
}

// feedbackReadyDirective returns a ResponseDirective that re-arms the Guide's
// plan-approval phase gate after the architect addresses user feedback. Returns
// nil if the plan is not in Ready status (e.g. already executing or expired).
func (a *Architect) feedbackReadyDirective(plan *DesignPlan) *guide.ResponseDirective {
	if plan == nil || plan.Status != PlanStatusReady {
		return nil
	}
	return &guide.ResponseDirective{
		Phase:    guide.PhasePlanApproval,
		AgentID:  "architect",
		Metadata: map[string]any{"plan_id": plan.ID},
		TTL:      readyPlanMaxAge,
	}
}

// handleExecute processes explicit execution intents classified by the Guide.
// The classifier already determined the user wants to execute a plan, so no
// phrase matching is needed — just look for a ready plan and dispatch it.
func (a *Architect) handleExecute(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	a.logInfo("handleExecute: entry",
		"input", truncateString(fwd.Input, 80))

	plan := a.latestReadyPlan()
	if plan == nil {
		a.logInfo("handleExecute: no ready plan found")
		return &ConversationResult{
			Response: "There's no ready plan to execute. Describe what you'd like to build and I'll create one.",
			Intent:   IntentConverse,
		}, nil
	}

	a.logInfo("handleExecute: dispatching plan",
		"plan_id", plan.ID,
		"tasks", len(plan.Tasks))

	req := &ArchitectRequest{
		Query:     fwd.Input,
		SessionID: sessionIDFromForwarded(fwd),
	}
	result, _ := a.dispatchPlanExecution(ctx, req, plan)
	return result, nil
}

// mapGuideIntentToArchitect preserves the original guide intent as an
// ArchitectIntent hint so the LLM receives the right context (e.g. "design"
// vs "plan" vs general conversation).
func mapGuideIntentToArchitect(intent guide.Intent) ArchitectIntent {
	switch intent {
	case guide.IntentDesign:
		return IntentDesign
	case guide.IntentPlan:
		return IntentPlan
	default:
		return IntentConverse
	}
}

func forwardedRequestParams(fwd *guide.ForwardedRequest) map[string]any {
	if fwd == nil {
		return nil
	}
	params := map[string]any{}
	if fwd.Entities != nil {
		if fwd.Entities.Scope != "" {
			params["scope"] = fwd.Entities.Scope
		}
	}
	if fwd.CrossDomain != nil {
		params["cross_domain"] = fwd.CrossDomain
		params["is_multi_agent"] = fwd.CrossDomain.IsMultiAgent
		params["primary_agent"] = fwd.CrossDomain.PrimaryAgent
		params["subtask_count"] = len(fwd.CrossDomain.SubTasks)
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

// handleBusResponse processes responses to requests we made
func (a *Architect) handleBusResponse(msg *guide.Message) error {
	a.deliverPendingBusMessage(msg)
	a.logger.Debug("received response", "correlation_id", msg.CorrelationID, "type", msg.Type)
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events
func (a *Architect) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		a.knownAgentsMu.Lock()
		a.knownAgents[ann.AgentID] = ann
		a.knownAgentsMu.Unlock()
		a.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		a.knownAgentsMu.Lock()
		delete(a.knownAgents, ann.AgentID)
		a.knownAgentsMu.Unlock()
		a.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

// GetKnownAgents returns all agents the architect knows about
func (a *Architect) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	a.knownAgentsMu.RLock()
	defer a.knownAgentsMu.RUnlock()

	result := make(map[string]*guide.AgentAnnouncement, len(a.knownAgents))
	for k, v := range a.knownAgents {
		result[k] = v
	}
	return result
}

// =============================================================================
// Direct API Methods
// =============================================================================

// Handle processes an ArchitectRequest directly (without event bus)
func (a *Architect) Handle(ctx context.Context, req *ArchitectRequest) (*ArchitectResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}
	a.prepareSkillsForRequest(req)

	start := time.Now()
	var result any
	var err error

	switch req.Intent {
	case IntentPlan, IntentDesign:
		result, err = a.executeConversation(ctx, req)
	case IntentGenerateTasks:
		result, err = a.executeGenerateTasks(ctx, req)
	case IntentCreateDAG:
		result, err = a.executeCreateDAG(ctx, req)
	case IntentRecall:
		result, err = a.executeRecall(ctx, req)
	case IntentCheck:
		result, err = a.executeCheck(ctx, req)
	case IntentHelp, IntentConsult, IntentEstimate, IntentConverse:
		result, err = a.executeConversation(ctx, req)
	default:
		result, err = a.executeConversation(ctx, req)
	}

	if err != nil {
		resp := &ArchitectResponse{
			ID:        uuid.New().String(),
			RequestID: req.ID,
			Success:   false,
			Error:     err.Error(),
			Took:      time.Since(start),
			Timestamp: time.Now(),
		}
		return resp, err
	}

	return &ArchitectResponse{
		ID:           uuid.New().String(),
		RequestID:    req.ID,
		Success:      true,
		Data:         result,
		UserResponse: extractUserResponse(result),
		Took:         time.Since(start),
		Timestamp:    time.Now(),
	}, nil
}

// =============================================================================
// Pre-Delegation Planning Protocol
// =============================================================================

// executePlanningProtocol implements the Pre-Delegation Planning Protocol
// Steps:
// 1. Understand requirements
// 2. Consult Librarian for codebase patterns
// 3. Design solution architecture
// 4. Generate atomic tasks
// 5. Create workflow DAG
func (a *Architect) executePlanningProtocol(ctx context.Context, req *ArchitectRequest) (*DesignPlan, error) {
	return a.runPlanningProtocol(ctx, req)
}

func failPlan(plan *DesignPlan, err error) (*DesignPlan, error) {
	plan.Status = PlanStatusFailed
	plan.Error = err.Error()
	return plan, err
}

func extractConstraints(params map[string]any) *PlanConstraints {
	constraints := &PlanConstraints{
		MaxTasksPerAgent: 5, // Default
	}

	if params == nil {
		return constraints
	}

	if maxTasks, ok := params["max_tasks_per_agent"].(int); ok {
		constraints.MaxTasksPerAgent = maxTasks
	}
	if scope, ok := params["scope"].(string); ok {
		constraints.Scope = scope
	}
	if parallel, ok := params["allow_parallel"].(bool); ok {
		constraints.AllowParallel = parallel
	} else {
		constraints.AllowParallel = true // Default to allowing parallel
	}

	return constraints
}

// analyzeRequirements extracts and structures requirements from the query
func (a *Architect) analyzeRequirements(ctx context.Context, query string, params map[string]any) (*Requirements, error) {
	if requirements, ok := a.tryAnalyzeRequirementsWithLLM(ctx, query, params); ok {
		a.logInfo("analyzeRequirements: LLM path used", "goals", len(requirements.Goals))
		return requirements, nil
	}
	a.logInfo("analyzeRequirements: deterministic fallback")

	requirements := &Requirements{
		Query:        query,
		Goals:        []string{},
		Constraints:  []string{},
		Dependencies: []string{},
		Scope:        "project",
	}

	// Extract scope if provided
	if params != nil {
		if scope, ok := params["scope"].(string); ok {
			requirements.Scope = scope
		}
		if goals, ok := params["goals"].([]string); ok {
			requirements.Goals = goals
		}
		if constraints, ok := params["constraints"].([]string); ok {
			requirements.Constraints = constraints
		}
	}

	// If no explicit goals, derive from query
	if len(requirements.Goals) == 0 {
		requirements.Goals = []string{query}
	}

	return requirements, nil
}

// consultLibrarian queries the Librarian for relevant codebase patterns
func (a *Architect) consultLibrarian(ctx context.Context, requirements *Requirements, sessionID string) (*CodebasePatterns, error) {
	if requirements == nil {
		return emptyCodebasePatterns(), nil
	}
	query := fmt.Sprintf("Find patterns related to: %s", requirements.Query)
	evidence, err := a.requestConsultation(ctx, "librarian", query, requirements.Scope, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to consult librarian: %w", err)
	}
	if evidence == nil || !evidence.Success {
		return nil, fmt.Errorf("librarian consultation did not return success")
	}
	return codebasePatternsFromEvidence(evidence), nil
}

// designArchitecture creates a solution architecture based on requirements
func (a *Architect) designArchitecture(ctx context.Context, requirements *Requirements, patterns *CodebasePatterns) (*SolutionArchitecture, error) {
	if architecture, ok := a.tryDesignArchitectureWithLLM(ctx, requirements, patterns); ok {
		a.logInfo("designArchitecture: LLM path used", "components", len(architecture.Components))
		return architecture, nil
	}
	a.logInfo("designArchitecture: deterministic fallback",
		"goals", len(requirements.Goals))

	architecture := &SolutionArchitecture{
		Name:        fmt.Sprintf("Architecture for: %s", truncateString(requirements.Query, 50)),
		Description: requirements.Query,
		Components:  deriveComponentsFromGoals(requirements),
		Interfaces:  []InterfaceSpec{},
		Patterns:    []string{},
	}

	// Add patterns from codebase analysis
	if patterns != nil {
		for _, p := range patterns.Patterns {
			architecture.Patterns = append(architecture.Patterns, p.Name)
		}
	}

	a.logInfo("designArchitecture: deterministic result",
		"components", len(architecture.Components))
	return architecture, nil
}

// deriveComponentsFromGoals creates one component per requirement goal
// so the deterministic task fallback has meaningful structure.
func deriveComponentsFromGoals(requirements *Requirements) []ComponentSpec {
	if requirements == nil || len(requirements.Goals) == 0 {
		return nil
	}
	components := make([]ComponentSpec, 0, len(requirements.Goals))
	for i, goal := range requirements.Goals {
		goal = strings.TrimSpace(goal)
		if goal == "" {
			continue
		}
		components = append(components, ComponentSpec{
			Name:        fmt.Sprintf("goal_%d", i+1),
			Type:        "backend",
			Description: goal,
		})
	}
	return components
}

// =============================================================================
// Atomic Task Generation System
// =============================================================================

// generateAtomicTasks creates atomic tasks from the architecture
// Rules:
// - Each task should be completable by a single agent
// - Tasks should have clear success criteria
// - Dependencies must be explicit
func (a *Architect) generateAtomicTasks(ctx context.Context, architecture *SolutionArchitecture, constraints *PlanConstraints) ([]*AtomicTask, error) {
	if tasks, ok := a.tryGenerateTasksWithLLM(ctx, architecture, constraints); ok {
		a.logInfo("generateAtomicTasks: LLM path used", "tasks", len(tasks))
		return tasks, nil
	}
	a.logInfo("generateAtomicTasks: deterministic fallback",
		"components", len(architecture.Components))

	tasks := make([]*AtomicTask, 0)

	// Generate tasks for each component
	for i, component := range architecture.Components {
		assignment := determineTaskAgents(component)
		task := &AtomicTask{
			ID:                fmt.Sprintf("task_%d", i+1),
			Name:              fmt.Sprintf("Implement %s", component.Name),
			Description:       component.Description,
			AgentType:         assignment.Primary,
			CoAgents:          assignment.CoAgents,
			CollaborationMode: assignment.Mode,
			SuccessCriteria:   generateSuccessCriteria(component),
			Dependencies:      component.Dependencies,
			EstimatedTokens:   estimateTaskTokens(component),
			Complexity:        estimateComplexity(component),
			Status:            TaskStatusPending,
			AcceptanceCriteria: []AcceptanceCriterion{{
				Given:    "the codebase is in a clean state",
				When:     fmt.Sprintf("the %s component is implemented", component.Name),
				Then:     "all success criteria are met and tests pass",
				Priority: "must",
			}},
			ImplementationGuide: fmt.Sprintf("Implement the %s component as described. Follow existing codebase patterns.", component.Name),
			AffectedFiles:       deterministicAffectedFiles(component),
		}
		tasks = append(tasks, task)
	}

	// If no components defined, create a single task from the architecture description
	if len(tasks) == 0 {
		task := &AtomicTask{
			ID:              "task_1",
			Name:            architecture.Name,
			Description:     architecture.Description,
			AgentType:       "engineer",
			SuccessCriteria: []string{"Implementation complete", "Tests passing"},
			Dependencies:    []string{},
			EstimatedTokens: 5000,
			Complexity:      ComplexityMedium,
			Status:          TaskStatusPending,
			AcceptanceCriteria: []AcceptanceCriterion{{
				Given:    "the codebase is in a clean state",
				When:     "the implementation is complete",
				Then:     "all tests pass and the feature works as described",
				Priority: "must",
			}},
			ImplementationGuide: fmt.Sprintf("Implement %s as described in the architecture.", architecture.Name),
			AffectedFiles:       []TaskFileTarget{{Path: "TBD", Operation: "create", Reason: "primary implementation file"}},
		}
		tasks = append(tasks, task)
	}

	return normalizeTaskGraph(tasks), nil
}

func deterministicAffectedFiles(component ComponentSpec) []TaskFileTarget {
	if component.FilePath != "" {
		return []TaskFileTarget{{
			Path:      component.FilePath,
			Operation: "modify",
			Reason:    fmt.Sprintf("primary file for %s component", component.Name),
		}}
	}
	return []TaskFileTarget{{
		Path:      fmt.Sprintf("%s.go", strings.ToLower(strings.ReplaceAll(component.Name, " ", "_"))),
		Operation: "create",
		Reason:    fmt.Sprintf("implementation file for %s component", component.Name),
	}}
}

// taskAgentAssignment describes the primary agent and optional co-tenants for
// a task derived from a component specification.
type taskAgentAssignment struct {
	Primary  string
	CoAgents []string
	Mode     dag.CollaborationMode
}

// determineTaskAgents inspects a component's type and metadata to decide
// whether it needs single-agent or compound-node execution.
func determineTaskAgents(component ComponentSpec) taskAgentAssignment {
	ctype := strings.ToLower(strings.TrimSpace(component.Type))
	hasDeps := len(component.Dependencies) > 0

	switch ctype {
	case "test", "testing":
		return taskAgentAssignment{Primary: "tester"}
	case "design", "ui":
		return taskAgentAssignment{Primary: "designer"}
	case "fullstack", "full-stack", "full_stack":
		return taskAgentAssignment{
			Primary:  "engineer",
			CoAgents: []string{"designer"},
			Mode:     dag.CollaborationAdversarial,
		}
	}

	// Heuristic: components with UI-related metadata that also touch backend
	// concerns benefit from Engineer+Designer co-tenancy.
	meta := component.Metadata
	if meta != nil {
		if hasUIIndicator(meta) && hasBackendIndicator(meta, hasDeps) {
			return taskAgentAssignment{
				Primary:  "engineer",
				CoAgents: []string{"designer"},
				Mode:     dag.CollaborationAdversarial,
			}
		}
	}

	return taskAgentAssignment{Primary: "engineer"}
}

func hasUIIndicator(meta map[string]any) bool {
	for _, key := range []string{"ui", "frontend", "design", "visual", "layout"} {
		if _, ok := meta[key]; ok {
			return true
		}
	}
	return false
}

func hasBackendIndicator(meta map[string]any, hasDeps bool) bool {
	if hasDeps {
		return true
	}
	for _, key := range []string{"backend", "api", "database", "server", "infra"} {
		if _, ok := meta[key]; ok {
			return true
		}
	}
	return false
}

func generateSuccessCriteria(component ComponentSpec) []string {
	criteria := []string{
		fmt.Sprintf("Component %s is implemented", component.Name),
		"Code compiles without errors",
		"Tests pass",
	}
	return criteria
}

// baseTokensPerInterface is the estimated token cost of implementing a single
// interface method or component interface. Derived empirically from observed
// LLM codegen: ~500 tokens per non-trivial function/method.
const baseTokensPerInterface = 500

// estimateTaskTokens computes a token budget from the component's structural
// complexity. Each interface and dependency adds surface area for the LLM.
func estimateTaskTokens(component ComponentSpec) int {
	interfaceCount := max(1, len(component.Interfaces))
	depCount := len(component.Dependencies)
	return interfaceCount*baseTokensPerInterface + depCount*baseTokensPerInterface
}

// estimateComplexity maps structural signals (dependency count, interface
// count) to the TaskComplexity enum. Thresholds are derived from the enum
// cardinality (4 levels) — each level spans ceil(maxDeps/4) dependencies.
func estimateComplexity(component ComponentSpec) TaskComplexity {
	// Composite signal: dependencies + interfaces
	signal := len(component.Dependencies) + len(component.Interfaces)
	switch {
	case signal >= int(ComplexityCritical)*2:
		return ComplexityCritical
	case signal >= int(ComplexityHigh)*2:
		return ComplexityHigh
	case signal >= int(ComplexityMedium)*2:
		return ComplexityMedium
	default:
		return ComplexityLow
	}
}

// reviewBudget computes the adversarial review round budget for a compound
// node from the task's structural entropy. Inspired by RL exploration budgets:
// tasks with more design surface area (interfaces, dependencies, files) have
// higher uncertainty and benefit from more review rounds.
//
// The budget uses the log2 of the total structural signal count, which gives
// diminishing returns: doubling the surface area adds only one more round.
// This mirrors the information-theoretic insight that marginal information
// gain decreases with each additional review pass.
//
// Floor is 1 (every compound node gets at least one review). Ceiling is
// ComplexityCritical (prevents runaway review chains).
func reviewBudget(task *AtomicTask) int {
	// Structural signals: each adds design surface area
	signal := len(task.Dependencies) + len(task.SuccessCriteria) + len(task.AffectedFiles)
	if task.ImplementationGuide != "" {
		signal++ // non-trivial task
	}
	signal += int(task.Complexity)

	// log2(signal+1) gives: signal=0→0, 1→1, 3→2, 7→3, 15→4
	// +1 prevents log2(0); the floor/ceil clamp handles the rest.
	rounds := int(math.Log2(float64(signal + 1)))
	rounds = max(1, rounds)
	return min(rounds, int(ComplexityCritical))
}

// =============================================================================
// Workflow DAG Creation
// =============================================================================

// createWorkflowDAG creates a workflow DAG for task orchestration
func (a *Architect) createWorkflowDAG(ctx context.Context, tasks []*AtomicTask) (*WorkflowDAG, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no tasks to create workflow from")
	}

	// Create the DAG using the builder
	builder := dag.NewBuilder(fmt.Sprintf("Workflow with %d tasks", len(tasks)))

	// Add nodes for each task, populating compound node fields when present.
	for _, task := range tasks {
		nodeConfig := dag.NodeConfig{
			ID:                task.ID,
			AgentType:         task.AgentType,
			Prompt:            task.Description,
			Dependencies:      task.Dependencies,
			Priority:          taskPriority(task),
			CoAgents:          task.CoAgents,
			CollaborationMode: task.CollaborationMode,
			Context: map[string]any{
				"task_name":        task.Name,
				"success_criteria": task.SuccessCriteria,
				"complexity":       task.Complexity.String(),
			},
			Metadata: map[string]any{
				"estimated_tokens": task.EstimatedTokens,
			},
		}
		if len(task.CoAgents) > 0 && task.CollaborationMode == dag.CollaborationAdversarial {
			nodeConfig.MaxReviewRounds = reviewBudget(task)
		}
		builder.AddNode(nodeConfig)
	}

	// Build and validate the DAG
	d, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build workflow DAG: %w", err)
	}

	// Wrap in WorkflowDAG
	workflow := &WorkflowDAG{
		DAG:             d,
		Tasks:           tasks,
		TotalTasks:      len(tasks),
		EstimatedTokens: calculateTotalTokens(tasks),
		CreatedAt:       time.Now(),
	}

	return workflow, nil
}

// taskPriority derives scheduling priority from task structure. Tasks with
// fewer dependencies are scheduled first (higher priority). The complexity
// enum also factors in — more complex tasks get slight priority to keep the
// critical path from stalling.
func taskPriority(task *AtomicTask) int {
	depPenalty := len(task.Dependencies) * int(ComplexityCritical)
	complexityBoost := int(task.Complexity)
	return complexityBoost - depPenalty
}

func calculateTotalTokens(tasks []*AtomicTask) int {
	total := 0
	for _, t := range tasks {
		total += t.EstimatedTokens
	}
	return total
}

// =============================================================================
// Additional Handlers
// =============================================================================

func (a *Architect) executeDesignArchitecture(ctx context.Context, req *ArchitectRequest) (*SolutionArchitecture, error) {
	requirements := &Requirements{
		Query: req.Query,
		Goals: []string{req.Query},
		Scope: "project",
	}
	return a.designArchitecture(ctx, requirements, nil)
}

func (a *Architect) executeGenerateTasks(ctx context.Context, req *ArchitectRequest) ([]*AtomicTask, error) {
	// Get architecture from params or create minimal one
	architecture := &SolutionArchitecture{
		Name:        "Task Generation",
		Description: req.Query,
		Components:  []ComponentSpec{},
	}

	if req.Params != nil {
		if arch, ok := req.Params["architecture"].(*SolutionArchitecture); ok {
			architecture = arch
		}
	}

	constraints := extractConstraints(req.Params)
	return a.generateAtomicTasks(ctx, architecture, constraints)
}

func (a *Architect) executeCreateDAG(ctx context.Context, req *ArchitectRequest) (*WorkflowDAG, error) {
	// Get tasks from params or create minimal tasks
	var tasks []*AtomicTask

	if req.Params != nil {
		if t, ok := req.Params["tasks"].([]*AtomicTask); ok {
			tasks = t
		}
	}

	if len(tasks) == 0 {
		// Create a single task from the query
		tasks = []*AtomicTask{
			{
				ID:              "task_1",
				Name:            "Execute",
				Description:     req.Query,
				AgentType:       "engineer",
				SuccessCriteria: []string{"Task completed"},
				Dependencies:    []string{},
				EstimatedTokens: 3000,
				Complexity:      ComplexityMedium,
				Status:          TaskStatusPending,
			},
		}
	}

	return a.createWorkflowDAG(ctx, tasks)
}

func (a *Architect) executeRecall(ctx context.Context, req *ArchitectRequest) (any, error) {
	// Return active plans matching query
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()

	matchingPlans := make([]*DesignPlan, 0)
	for _, plan := range a.activePlans {
		if containsIgnoreCase(plan.Query, req.Query) {
			matchingPlans = append(matchingPlans, plan)
		}
	}
	return matchingPlans, nil
}

func (a *Architect) executeCheck(ctx context.Context, req *ArchitectRequest) (any, error) {
	// Check if a plan exists
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()

	for _, plan := range a.activePlans {
		if containsIgnoreCase(plan.Query, req.Query) {
			return map[string]any{
				"found":  true,
				"plan":   plan,
				"status": plan.Status.String(),
			}, nil
		}
	}
	return map[string]any{
		"found": false,
	}, nil
}

// executeConversation handles conversational intents (help, consult, estimate,
// converse, plan, design, and unclassified) with a single LLM call instead of
// the full planning protocol.
func (a *Architect) executeConversation(ctx context.Context, req *ArchitectRequest) (any, error) {
	a.logInfo("executeConversation: entry",
		"intent", string(req.Intent),
		"query", truncateString(req.Query, 80))
	ctx = withPlannerThoughtCallback(ctx, func(stage string, thought string) {
		a.publishPlanThought(ctx, stage, thought)
	})
	request := plannerConversationRequest{
		Mode:                plannerConversationModeConverse,
		UserQuery:           req.Query,
		IntentHint:          string(req.Intent),
		ConversationHistory: req.ConversationHistory,
		OnChunk: func(text string) {
			a.publishPlanStreamChunk(ctx, text)
		},
	}
	// Inject session plan context so the LLM has continuity even when
	// conversation history is degraded (e.g. prior protocols hung and
	// never delivered agent replies back to the Guide).
	a.enrichConversationWithPlanContext(&request, req.SessionID)
	response, composeErr := a.composeUserFacingResponse(ctx, request)
	if composeErr == nil {
		a.logInfo("executeConversation: LLM compose succeeded",
			"response_len", len(response))
		result := &ConversationResult{
			Response: response,
			Intent:   req.Intent,
		}
		// If a ready plan now exists (e.g. the tool loop called planning
		// skills that created one), arm the Guide's phase gate so the
		// next user message gets plan-approval classification.
		if plan := a.latestReadyPlan(); plan != nil {
			result.Directive = a.feedbackReadyDirective(plan)
		}
		return result, nil
	}
	a.logWarn("executeConversation: compose failed, using domain fallback",
		"intent", string(req.Intent), "error", composeErr)
	return a.conversationFallback(ctx, req, composeErr)
}

// enrichConversationWithPlanContext adds the latest plan summary and prior
// query to a conversation request when an active plan exists for the session.
// This prevents context loss when previous protocol runs hung before the
// Guide could record the architect's response in conversation history.
func (a *Architect) enrichConversationWithPlanContext(request *plannerConversationRequest, sessionID string) {
	plan := a.latestHistoricalPlanForSession(sessionID)
	if plan == nil {
		return
	}
	if summary := formatPlanForChat(plan); summary != "" {
		request.PlanSummary = summary
	}
	request.PriorQuery = plan.Query
	if plan.Requirements != nil {
		request.Scope = plan.Requirements.Scope
	}
}

// conversationFallback runs the domain-specific execution path when the
// conversational LLM is unavailable. Only starts the planning protocol
// for plan/design intents — conversational intents (chat, help, etc.)
// get a graceful text response instead of an inappropriate planning run.
func (a *Architect) conversationFallback(ctx context.Context, req *ArchitectRequest, composeErr error) (any, error) {
	a.logInfo("conversationFallback: entry",
		"intent", string(req.Intent),
		"planner_available", a.ensurePlanner() != nil)
	if a.ensurePlanner() == nil {
		a.logWarn("conversationFallback: no planner configured")
		return &ConversationResult{
			Response: "I can't generate a detailed plan right now — my LLM planner is not configured. " +
				"Please ensure an Anthropic API key is available (ANTHROPIC_API_KEY environment variable or the secure credential store).",
			Intent: req.Intent,
		}, nil
	}
	if !isConversationFallbackPlanningIntent(req.Intent) {
		a.logInfo("conversationFallback: non-planning intent, returning text response",
			"intent", string(req.Intent))
		return &ConversationResult{
			Response: "I'm having trouble processing that right now. Could you rephrase or try again?",
			Intent:   req.Intent,
		}, nil
	}
	if req.Intent == IntentDesign {
		return a.executeDesignArchitecture(ctx, req)
	}
	plan, err := a.executePlanningProtocol(ctx, req)
	if err != nil {
		// Surface both the protocol error and the original compose error
		// so the user can diagnose why the LLM path failed.
		return nil, fmt.Errorf("planning protocol: %w (conversation unavailable: %v)", err, composeErr)
	}
	return plan, nil
}

// isConversationFallbackPlanningIntent returns true for intents where
// starting a planning protocol is an appropriate fallback when the LLM
// conversation path fails. Conversational intents (chat, help, etc.)
// should never fall back to planning — that misinterprets the user's
// message and can trigger long-running protocol hangs.
func isConversationFallbackPlanningIntent(intent ArchitectIntent) bool {
	switch intent {
	case IntentPlan, IntentDesign, IntentGenerateTasks, IntentCreateDAG:
		return true
	default:
		return false
	}
}

// handleDomainQuery handles cross-domain queries
func (a *Architect) handleDomainQuery(ctx context.Context, d domain.Domain, query string) (*DomainResult, error) {
	target := crossDomainTarget(d)
	if target == "" || target == "architect" {
		return localDomainResult(d, query), nil
	}
	if !a.running || a.bus == nil {
		return localDomainResult(d, query), nil
	}
	evidence, err := a.requestConsultation(ctx, target, query, "", "")
	if err != nil {
		return nil, err
	}
	return consultedDomainResult(d, query, target, evidence), nil
}

func crossDomainTarget(d domain.Domain) string {
	switch d {
	case domain.DomainLibrarian, domain.DomainAcademic, domain.DomainArchivalist:
		return d.String()
	case domain.DomainEngineer, domain.DomainDesigner, domain.DomainInspector, domain.DomainTester:
		return d.String()
	case domain.DomainOrchestrator:
		return "orchestrator"
	default:
		return ""
	}
}

func localDomainResult(d domain.Domain, query string) *DomainResult {
	return &DomainResult{
		Domain:      d,
		Query:       query,
		Content:     "",
		Score:       0,
		Source:      "architect",
		RetrievedAt: time.Now(),
	}
}

func consultedDomainResult(
	d domain.Domain,
	query string,
	target string,
	evidence *ConsultationEvidence,
) *DomainResult {
	if evidence == nil {
		return localDomainResult(d, query)
	}
	return &DomainResult{
		Domain:      d,
		Query:       query,
		Content:     consultationContent(evidence.Data),
		Score:       consultationScore(evidence),
		Source:      target,
		RetrievedAt: evidence.ReceivedAt,
		ErrorMsg:    evidence.Error,
	}
}

func consultationContent(data any) string {
	if data == nil {
		return ""
	}
	if text, ok := data.(string); ok {
		return text
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprintf("%v", data)
	}
	return string(encoded)
}

func consultationScore(evidence *ConsultationEvidence) float64 {
	if evidence == nil {
		return 0
	}
	if evidence.Success {
		return 1
	}
	return 0
}

// =============================================================================
// Guide Registration
// =============================================================================

// GetRoutingInfo returns the architect's routing information for Guide registration
func (a *Architect) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      "architect",
		Type:    "architect",
		Name:    "architect",
		Aliases: []string{"arch", "planner", "designer"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "plan",
				Description:   "Create a design plan with atomic tasks and workflow DAG",
				DefaultIntent: guide.IntentPlan,
				DefaultDomain: guide.DomainDesign,
			},
			{
				Name:          "design",
				Description:   "Design system architecture",
				DefaultIntent: guide.IntentDesign,
				DefaultDomain: guide.DomainDesign,
			},
			{
				Name:          "decompose",
				Description:   "Decompose requirements into atomic tasks",
				DefaultIntent: guide.IntentPlan,
				DefaultDomain: guide.DomainTasks,
			},
			{
				Name:          "execute",
				Description:   "Execute the current plan",
				DefaultIntent: guide.IntentPlan,
				DefaultDomain: guide.DomainTasks,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"plan",
				"design",
				"architect",
				"decompose",
				"break down",
				"create workflow",
				"task generation",
				"orchestrate",
				"coordinate",
				"structure",
				"execute plan",
				"go ahead",
				"start execution",
				"run the plan",
			},
			WeakTriggers: []string{
				"implement",
				"build",
				"create",
				"develop",
				"organize",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentPlan: {
					"plan",
					"design",
					"create workflow",
					"break down",
					"decompose",
					"execute plan",
					"go ahead",
					"start execution",
					"run the plan",
				},
				guide.IntentDesign: {
					"architect",
					"structure",
					"design",
					"organize",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      "architect",
			Name:    "architect",
			Aliases: []string{"arch", "planner", "designer"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentPlan,
					guide.IntentDesign,
					guide.IntentExecute,
					guide.IntentRecall,
					guide.IntentCheck,
					guide.IntentHelp,
				},
				Domains: []guide.Domain{
					guide.DomainDesign,
					guide.DomainTasks,
				},
				Tags:     []string{"planning", "design", "architecture", "tasks", "workflow"},
				Keywords: []string{"plan", "design", "architect", "decompose", "workflow", "dag", "tasks"},
				Priority: 90,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalFuture,
				MinConfidence: 0.6,
			},
			Description: "System design and planning specialist. Creates atomic tasks and workflow DAGs using Pre-Delegation Planning Protocol.",
			Priority:    90,
		},
	}
}

// PublishRequest publishes a request to the Guide for routing
func (a *Architect) PublishRequest(req *guide.RouteRequest) error {
	if !a.running {
		return fmt.Errorf("architect is not running")
	}

	req.SourceAgentID = "architect"
	req.SourceAgentName = "architect"

	msg := guide.NewRequestMessage(a.generateMessageID(), req)
	return a.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Helper Functions
// =============================================================================

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// unwrapArchitectResult extracts the inner result from an ArchitectResponse
// wrapper. Returns the input unchanged for all other types.
func unwrapArchitectResult(data any) any {
	if v, ok := data.(*ArchitectResponse); ok && v != nil {
		return v.Data
	}
	return data
}

// extractUserResponse returns the human-readable UserResponse from a planning,
// conversation, or architect response result.
func extractUserResponse(data any) string {
	switch v := data.(type) {
	case *ArchitectResponse:
		if v != nil {
			return v.UserResponse
		}
	case *DesignPlan:
		if v != nil {
			return v.UserResponse
		}
	case *ConversationResult:
		if v != nil {
			return v.Response
		}
	case *SolutionArchitecture:
		if v != nil {
			return v.Description
		}
	}
	return ""
}

// GetActivePlan returns an active plan by ID
func (a *Architect) GetActivePlan(id string) (*DesignPlan, bool) {
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()

	plan, ok := a.activePlans[id]
	return plan, ok
}

// GetAllActivePlans returns all active plans
func (a *Architect) GetAllActivePlans() []*DesignPlan {
	a.activePlansMu.RLock()
	defer a.activePlansMu.RUnlock()

	plans := make([]*DesignPlan, 0, len(a.activePlans))
	for _, plan := range a.activePlans {
		plans = append(plans, plan)
	}
	return plans
}

// =============================================================================
// HandoffableAgent + ContextEvictable Implementation
// =============================================================================

// SetHandoffBridge sets the handoff bridge for the architect agent.
func (a *Architect) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	a.handoffBridge = bridge
}

// AgentID returns the unique identifier for this architect instance.
func (a *Architect) AgentID() string {
	return "architect"
}

// AgentType returns the agent type classification.
func (a *Architect) AgentType() string {
	return "architect"
}

// Descriptor returns the immutable agent descriptor for handoff participation.
func (a *Architect) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:     "architect",
		ModelID:       "opus-4.5-200k",
		ContextWindow: 200000,
		Category:      handoff.CategoryKnowledge,
	}
}

// ExtractArchivableState captures the architect's state for handoff persistence.
func (a *Architect) ExtractArchivableState() *handoff.ArchivableState {
	return &handoff.ArchivableState{
		AgentID:   a.AgentID(),
		AgentType: a.AgentType(),
		Timestamp: time.Now(),
	}
}

// Terminate gracefully shuts down the architect agent.
func (a *Architect) Terminate(ctx context.Context) error {
	return a.Stop()
}

// EvictEntries frees context by evicting the given candidates.
// Returns the total number of tokens freed across all evicted entries.
func (a *Architect) EvictEntries(candidates []handoff.EvictionCandidate) (freedTokens int, err error) {
	for _, c := range candidates {
		freedTokens += c.Entry.GetTokenCount()
	}
	return freedTokens, nil
}
