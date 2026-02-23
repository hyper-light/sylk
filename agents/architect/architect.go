package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/skills"
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
)

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

	// Always include the authoritative response text in the completion
	// event. The bridge stores it as AuthoritativeText on StreamCompleteMsg
	// so the chat model can correct dropped or reordered stream chunks.
	completeText := extractUserResponse(result)
	a.publishPlanStreamComplete(reqCtx, completeText, usageAcc.Total())

	// Publish response to own response channel
	respMsg := guide.NewResponseMessage(a.generateMessageID(), resp)
	return a.bus.Publish(a.channels.Responses, respMsg)
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
	// Check if user is approving a ready plan for execution.
	preReq := &ArchitectRequest{
		Query:     fwd.Input,
		SessionID: sessionIDFromForwarded(fwd),
	}
	if result, ok := a.tryExecutePlan(ctx, preReq); ok {
		return result, nil
	}

	// Check if user is confirming plan formalization after conversation.
	if plan, ok := a.tryFormalizePlan(ctx, fwd); ok {
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

	return a.Handle(ctx, req)
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
		return requirements, nil
	}

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
		return architecture, nil
	}

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
		return tasks, nil
	}

	tasks := make([]*AtomicTask, 0)

	// Generate tasks for each component
	for i, component := range architecture.Components {
		task := &AtomicTask{
			ID:              fmt.Sprintf("task_%d", i+1),
			Name:            fmt.Sprintf("Implement %s", component.Name),
			Description:     component.Description,
			AgentType:       determineAgentType(component),
			SuccessCriteria: generateSuccessCriteria(component),
			Dependencies:    component.Dependencies,
			EstimatedTokens: estimateTaskTokens(component),
			Complexity:      estimateComplexity(component),
			Status:          TaskStatusPending,
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

func determineAgentType(component ComponentSpec) string {
	// Determine best agent type based on component characteristics
	switch component.Type {
	case "test", "testing":
		return "tester"
	case "design", "ui":
		return "designer"
	case "docs", "documentation":
		return "engineer"
	default:
		return "engineer"
	}
}

func generateSuccessCriteria(component ComponentSpec) []string {
	criteria := []string{
		fmt.Sprintf("Component %s is implemented", component.Name),
		"Code compiles without errors",
		"Tests pass",
	}
	return criteria
}

func estimateTaskTokens(component ComponentSpec) int {
	// Basic estimation based on component complexity
	base := 2000
	if len(component.Dependencies) > 2 {
		base += 1000
	}
	return base
}

func estimateComplexity(component ComponentSpec) TaskComplexity {
	// Simple heuristic based on dependencies
	depCount := len(component.Dependencies)
	switch {
	case depCount > 3:
		return ComplexityHigh
	case depCount > 1:
		return ComplexityMedium
	default:
		return ComplexityLow
	}
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

	// Add nodes for each task
	for _, task := range tasks {
		nodeConfig := dag.NodeConfig{
			ID:           task.ID,
			AgentType:    task.AgentType,
			Prompt:       task.Description,
			Dependencies: task.Dependencies,
			Priority:     taskPriority(task),
			Context: map[string]any{
				"task_name":        task.Name,
				"success_criteria": task.SuccessCriteria,
				"complexity":       task.Complexity.String(),
			},
			Metadata: map[string]any{
				"estimated_tokens": task.EstimatedTokens,
			},
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

func taskPriority(task *AtomicTask) int {
	// Higher priority for tasks with fewer dependencies (they can start earlier)
	base := 100
	return base - len(task.Dependencies)*10
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
	response, composeErr := a.composeUserFacingResponse(ctx, request)
	if composeErr == nil {
		return &ConversationResult{
			Response: response,
			Intent:   req.Intent,
		}, nil
	}
	a.logger.Warn("conversation compose failed, using domain fallback",
		"intent", req.Intent, "error", composeErr)
	// LLM unavailable — fall back to domain-specific execution.
	return a.conversationFallback(ctx, req, composeErr)
}

// conversationFallback runs the domain-specific execution path when the
// conversational LLM is unavailable. If the planner is entirely missing
// (no API key), returns a clear error instead of running the planning
// protocol with deterministic-only fallbacks that produce generic tasks.
func (a *Architect) conversationFallback(ctx context.Context, req *ArchitectRequest, composeErr error) (any, error) {
	if a.ensurePlanner() == nil {
		return &ConversationResult{
			Response: "I can't generate a detailed plan right now — my LLM planner is not configured. " +
				"Please ensure an Anthropic API key is available (ANTHROPIC_API_KEY environment variable or the secure credential store).",
			Intent: req.Intent,
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
