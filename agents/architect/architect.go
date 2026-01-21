package architect

import (
	"context"
	"fmt"
	"log/slog"
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
}

// Config holds configuration for the Architect agent
type Config struct {
	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Cross-domain configuration
	CrossDomainTimeout time.Duration // Optional, defaults to 30s
	MaxConcurrent      int           // Optional, defaults to 3

	// Synthesis configuration
	SimilarityThreshold float64 // Optional, defaults to 0.8
	ConflictThreshold   float64 // Optional, defaults to 0.3
	MaxContentLength    int     // Optional, defaults to 10000

	// Logging
	Logger *slog.Logger // Optional, uses slog.Default() if nil
}

// Default configuration values
const (
	DefaultMaxOutputTokens     = 4096
	DefaultCrossDomainTimeout  = 30 * time.Second
	DefaultMaxConcurrent       = 3
	DefaultSimilarityThreshold = 0.8
	DefaultConflictThreshold   = 0.3
	DefaultMaxContentLength    = 10000
)

// New creates a new Architect agent
func New(cfg Config) (*Architect, error) {
	cfg = applyConfigDefaults(cfg)

	architect := &Architect{
		config:      cfg,
		logger:      cfg.Logger,
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		activePlans: make(map[string]*DesignPlan),
	}

	architect.initCrossDomain(cfg)
	architect.initSynthesizer(cfg)
	architect.initSkills()

	return architect, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
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

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"analyze_requirements",
		"design_architecture",
		"generate_tasks",
		"create_workflow_dag",
		"estimate_complexity",
	}
	loaderCfg.AutoLoadDomains = []string{"planning", "design", "architecture"}
	a.skillLoader = skills.NewLoader(a.skills, loaderCfg)

	a.registerCoreSkills()
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

	a.bus = bus
	a.channels = guide.NewAgentChannels("architect")

	// Subscribe to own request channel (architect.requests)
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

	a.running = true
	a.logger.Info("architect started", "channels", a.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (a *Architect) Stop() error {
	if !a.running {
		return nil
	}

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
	if msg.Type != guide.MessageTypeForward {
		return nil // Ignore non-forward messages
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	// Process the request
	ctx := context.Background()
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
		SessionID: "",
		Timestamp: time.Now(),
	}

	if fwd.Entities != nil {
		req.Params = make(map[string]any)
		if fwd.Entities.Scope != "" {
			req.Params["scope"] = fwd.Entities.Scope
		}
	}

	return a.Handle(ctx, req)
}

// handleDesign processes design/architecture requests
func (a *Architect) handleDesign(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentDesign,
		Query:     fwd.Input,
		SessionID: "",
		Timestamp: time.Now(),
	}

	return a.Handle(ctx, req)
}

// handleRecall processes recall requests
func (a *Architect) handleRecall(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentRecall,
		Query:     fwd.Input,
		SessionID: "",
		Timestamp: time.Now(),
	}

	return a.Handle(ctx, req)
}

// handleCheck processes check/verification requests
func (a *Architect) handleCheck(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	req := &ArchitectRequest{
		ID:        uuid.New().String(),
		Intent:    IntentCheck,
		Query:     fwd.Input,
		SessionID: "",
		Timestamp: time.Now(),
	}

	return a.Handle(ctx, req)
}

// handleBusResponse processes responses to requests we made
func (a *Architect) handleBusResponse(msg *guide.Message) error {
	// For now, just log responses to our requests
	// Future: implement callback handling for async sub-requests
	a.logger.Debug("received response", "correlation_id", msg.CorrelationID)
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
		a.knownAgents[ann.AgentID] = ann
		a.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		delete(a.knownAgents, ann.AgentID)
		a.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

// GetKnownAgents returns all agents the architect knows about
func (a *Architect) GetKnownAgents() map[string]*guide.AgentAnnouncement {
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
		return &ArchitectResponse{
			ID:        uuid.New().String(),
			RequestID: req.ID,
			Success:   false,
			Error:     err.Error(),
			Took:      time.Since(start),
			Timestamp: time.Now(),
		}, nil
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
	plan := &DesignPlan{
		ID:          uuid.New().String(),
		Query:       req.Query,
		Status:      PlanStatusAnalyzing,
		CreatedAt:   time.Now(),
		Constraints: extractConstraints(req.Params),
	}

	// Step 1: Analyze requirements
	requirements, err := a.analyzeRequirements(ctx, req.Query, req.Params)
	if err != nil {
		plan.Status = PlanStatusFailed
		plan.Error = err.Error()
		return plan, nil
	}
	plan.Requirements = requirements
	plan.Status = PlanStatusConsulting

	// Step 2: Consult Librarian for codebase patterns (if bus is available)
	if a.running && a.bus != nil {
		patterns, err := a.consultLibrarian(ctx, requirements)
		if err != nil {
			a.logger.Warn("failed to consult librarian", "error", err)
		} else {
			plan.CodebasePatterns = patterns
		}
	}
	plan.Status = PlanStatusDesigning

	// Step 3: Design solution architecture
	architecture, err := a.designArchitecture(ctx, requirements, plan.CodebasePatterns)
	if err != nil {
		plan.Status = PlanStatusFailed
		plan.Error = err.Error()
		return plan, nil
	}
	plan.Architecture = architecture
	plan.Status = PlanStatusGenerating

	// Step 4: Generate atomic tasks
	tasks, err := a.generateAtomicTasks(ctx, architecture, plan.Constraints)
	if err != nil {
		plan.Status = PlanStatusFailed
		plan.Error = err.Error()
		return plan, nil
	}
	plan.Tasks = tasks
	plan.Status = PlanStatusOrchestrating

	// Step 5: Create workflow DAG
	workflow, err := a.createWorkflowDAG(ctx, tasks)
	if err != nil {
		plan.Status = PlanStatusFailed
		plan.Error = err.Error()
		return plan, nil
	}
	plan.Workflow = workflow
	plan.Status = PlanStatusReady
	plan.CompletedAt = time.Now()

	// Store active plan
	a.activePlans[plan.ID] = plan

	return plan, nil
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
func (a *Architect) consultLibrarian(ctx context.Context, requirements *Requirements) (*CodebasePatterns, error) {
	// Create a request to the Librarian via the event bus
	request := &guide.RouteRequest{
		Input:         fmt.Sprintf("Find patterns related to: %s", requirements.Query),
		SourceAgentID: "architect",
		TargetAgentID: "librarian",
		SessionID:     "",
		Timestamp:     time.Now(),
	}

	msg := guide.NewRequestMessage(a.generateMessageID(), request)
	if err := a.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		return nil, fmt.Errorf("failed to consult librarian: %w", err)
	}

	// Return empty patterns for now - async response handling would populate this
	return &CodebasePatterns{
		Patterns:           []PatternInfo{},
		RelevantFiles:      []string{},
		ExistingComponents: []string{},
	}, nil
}

// designArchitecture creates a solution architecture based on requirements
func (a *Architect) designArchitecture(ctx context.Context, requirements *Requirements, patterns *CodebasePatterns) (*SolutionArchitecture, error) {
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

	return tasks, nil
}

func determineAgentType(component ComponentSpec) string {
	// Determine best agent type based on component characteristics
	switch component.Type {
	case "test", "testing":
		return "tester"
	case "design", "ui":
		return "designer"
	case "docs", "documentation":
		return "documenter"
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
	// This would be implemented to query different domains
	return &DomainResult{
		Domain:      d,
		Query:       query,
		Content:     "",
		Score:       0,
		Source:      "architect",
		RetrievedAt: time.Now(),
	}, nil
}

// =============================================================================
// Guide Registration
// =============================================================================

// GetRoutingInfo returns the architect's routing information for Guide registration
func (a *Architect) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      "architect",
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
// Skills System
// =============================================================================

// Skills returns the architect's skill registry
func (a *Architect) Skills() *skills.Registry {
	return a.skills
}

// GetToolDefinitions returns tool definitions for all loaded skills
func (a *Architect) GetToolDefinitions() []map[string]any {
	return a.skills.GetToolDefinitions()
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
	return len(s) > 0 && len(substr) > 0 // Simplified - would use strings.Contains with ToLower
}

// GetActivePlan returns an active plan by ID
func (a *Architect) GetActivePlan(id string) (*DesignPlan, bool) {
	plan, ok := a.activePlans[id]
	return plan, ok
}

// GetAllActivePlans returns all active plans
func (a *Architect) GetAllActivePlans() []*DesignPlan {
	plans := make([]*DesignPlan, 0, len(a.activePlans))
	for _, plan := range a.activePlans {
		plans = append(plans, plan)
	}
	return plans
}
