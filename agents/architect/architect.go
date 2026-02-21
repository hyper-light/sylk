package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/domain"
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
}

// Config holds configuration for the Architect agent
type Config struct {
	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// LLM planning configuration
	EnableLLM         bool
	AnthropicAPIKey   string
	Model             string
	LLMRequestTimeout time.Duration
	LLMRetryMax       int

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
	Logger *slog.Logger // Optional, uses slog.Default() if nil
}

// Default configuration values
const (
	DefaultMaxOutputTokens     = 4096
	DefaultArchitectModel      = "claude-opus-4-6"
	DefaultLLMRequestTimeout   = 45 * time.Second
	DefaultLLMRetryMax         = 3
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

	architect := &Architect{
		config:      cfg,
		logger:      cfg.Logger,
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		activePlans: make(map[string]*DesignPlan),
		planModes:   make(map[string]*PlanModeState),
		pendingBus:  make(map[string]chan *guide.Message),
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
	if cfg.LLMRequestTimeout == 0 {
		cfg.LLMRequestTimeout = DefaultLLMRequestTimeout
	}
	if cfg.LLMRetryMax == 0 {
		cfg.LLMRetryMax = DefaultLLMRetryMax
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
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
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
	a.Stop()
	return nil
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

	result, err := a.processForwardedRequest(ctx, fwd)

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
	case guide.IntentPlan:
		return a.handlePlan, nil
	case guide.IntentDesign:
		return a.handleDesign, nil
	case guide.IntentRecall:
		return a.handleRecall, nil
	case guide.IntentCheck:
		return a.handleCheck, nil
	default:
		return nil, fmt.Errorf("unsupported intent: %s", intent)
	}
}

// handlePlan processes planning requests using the Pre-Delegation Planning Protocol
func (a *Architect) handlePlan(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentPlan,
		Query:     fwd.Input,
		SessionID: sessionIDFromForwarded(fwd),
		Timestamp: time.Now(),
		Params:    forwardedRequestParams(fwd),
	}

	return a.Handle(ctx, req)
}

// handleDesign processes design/architecture requests
func (a *Architect) handleDesign(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentDesign,
		Query:     fwd.Input,
		SessionID: sessionIDFromForwarded(fwd),
		Timestamp: time.Now(),
		Params:    forwardedRequestParams(fwd),
	}

	return a.Handle(ctx, req)
}

// handleRecall processes recall requests
func (a *Architect) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentRecall,
		Query:     fwd.Input,
		SessionID: sessionIDFromForwarded(fwd),
		Timestamp: time.Now(),
		Params:    forwardedRequestParams(fwd),
	}

	return a.Handle(ctx, req)
}

// handleCheck processes check/verification requests
func (a *Architect) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentCheck,
		Query:     fwd.Input,
		SessionID: sessionIDFromForwarded(fwd),
		Timestamp: time.Now(),
		Params:    forwardedRequestParams(fwd),
	}

	return a.Handle(ctx, req)
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
	case IntentPlan:
		result, err = a.executePlanningProtocol(ctx, req)
	case IntentDesign:
		result, err = a.executeDesignArchitecture(ctx, req)
	case IntentGenerateTasks:
		result, err = a.executeGenerateTasks(ctx, req)
	case IntentCreateDAG:
		result, err = a.executeCreateDAG(ctx, req)
	case IntentRecall:
		result, err = a.executeRecall(ctx, req)
	case IntentCheck:
		result, err = a.executeCheck(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported intent: %s", req.Intent)
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
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   true,
		Data:      result,
		Took:      time.Since(start),
		Timestamp: time.Now(),
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
		Components:  []ComponentSpec{},
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
		}
		tasks = append(tasks, task)
	}

	return normalizeTaskGraph(tasks), nil
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
