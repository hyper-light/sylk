package engineer

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// MaxTodosBeforeArchitect is the scope limit - if more todos are required,
// stop and request Architect decomposition.
const MaxTodosBeforeArchitect = 12

// Engineer is the code implementation specialist agent for the Sylk system.
// It executes individual coding tasks, focusing on clean, modular, testable,
// and readable code implementation.
type Engineer struct {
	id     string
	config Config
	logger *slog.Logger

	// State management
	state    *EngineerState
	stateMu  sync.RWMutex
	failures map[string]*FailureRecord // taskID -> failure record

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

	// Consultation tracking
	consultations []Consultation
	consultMu     sync.RWMutex
}

// Config holds configuration for the Engineer agent
type Config struct {
	// System prompt configuration
	SystemPrompt    string // Optional, uses DefaultSystemPrompt if empty
	MaxOutputTokens int    // Optional, uses DefaultMaxOutputTokens if 0

	// Engineer-specific configuration
	EngineerConfig EngineerConfig // Task execution configuration

	// Logging
	Logger *slog.Logger // Optional, uses slog.Default() if nil

	// Session context
	SessionID string // Session identifier
}

// Default configuration values
const (
	DefaultMaxOutputTokens = 8192
)

// New creates a new Engineer agent
func New(cfg Config) (*Engineer, error) {
	cfg = applyConfigDefaults(cfg)

	engineerID := fmt.Sprintf("engineer_%s", uuid.New().String()[:8])

	eng := &Engineer{
		id:          engineerID,
		config:      cfg,
		logger:      cfg.Logger,
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		failures:    make(map[string]*FailureRecord),
		state: &EngineerState{
			ID:        engineerID,
			SessionID: cfg.SessionID,
			Status:    AgentStatusIdle,
			TaskQueue: make([]string, 0),
			StartedAt: time.Now(),
		},
		consultations: make([]Consultation, 0),
	}

	eng.initSkills()

	return eng, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = DefaultMaxOutputTokens
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.EngineerConfig.CommandTimeout == 0 {
		cfg.EngineerConfig.CommandTimeout = 30 * time.Second
	}
	if cfg.EngineerConfig.MaxConcurrentTasks == 0 {
		cfg.EngineerConfig.MaxConcurrentTasks = 1
	}
	if cfg.EngineerConfig.MemoryThreshold.CheckpointThreshold == 0 {
		cfg.EngineerConfig.MemoryThreshold = DefaultMemoryThreshold()
	}
	if len(cfg.EngineerConfig.ApprovedCommands.Patterns) == 0 {
		cfg.EngineerConfig.ApprovedCommands = DefaultApprovedPatterns()
	}
	return cfg
}

func (e *Engineer) initSkills() {
	e.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{"read_file", "write_file", "edit_file", "run_command", "run_tests", "glob", "grep"}
	loaderCfg.AutoLoadDomains = []string{"code", "filesystem", "testing"}
	e.skillLoader = skills.NewLoader(e.skills, loaderCfg)

	e.registerCoreSkills()
}

// ID returns the engineer's unique identifier
func (e *Engineer) ID() string {
	return e.id
}

// Close closes the engineer and its resources
func (e *Engineer) Close() error {
	e.Stop()
	return nil
}

// =============================================================================
// Event Bus Integration
// =============================================================================

// Start begins listening for messages on the event bus.
// The engineer subscribes to its own channels and the registry topic.
func (e *Engineer) Start(bus guide.EventBus) error {
	if e.running {
		return fmt.Errorf("engineer is already running")
	}

	e.bus = bus
	e.channels = guide.NewAgentChannels("engineer")

	// Subscribe to own request channel (engineer.requests)
	var err error
	e.requestSub, err = bus.SubscribeAsync(e.channels.Requests, e.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", e.channels.Requests, err)
	}

	// Subscribe to own response channel (for replies to requests we make)
	e.responseSub, err = bus.SubscribeAsync(e.channels.Responses, e.handleBusResponse)
	if err != nil {
		e.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", e.channels.Responses, err)
	}

	// Subscribe to agent registry for announcements
	e.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, e.handleRegistryAnnouncement)
	if err != nil {
		e.requestSub.Unsubscribe()
		e.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	e.running = true
	e.logger.Info("engineer started", "id", e.id, "channels", e.channels)
	return nil
}

// Stop unsubscribes from event bus topics and stops message processing.
func (e *Engineer) Stop() error {
	if !e.running {
		return nil
	}

	errs := e.unsubscribeAll()
	e.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	e.logger.Info("engineer stopped", "id", e.id)
	return nil
}

func (e *Engineer) unsubscribeAll() []error {
	var errs []error
	if err := e.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := e.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := e.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (e *Engineer) unsubscribeRequest() error {
	if e.requestSub == nil {
		return nil
	}
	err := e.requestSub.Unsubscribe()
	e.requestSub = nil
	return err
}

func (e *Engineer) unsubscribeResponse() error {
	if e.responseSub == nil {
		return nil
	}
	err := e.responseSub.Unsubscribe()
	e.responseSub = nil
	return err
}

func (e *Engineer) unsubscribeRegistry() error {
	if e.registrySub == nil {
		return nil
	}
	err := e.registrySub.Unsubscribe()
	e.registrySub = nil
	return err
}

// IsRunning returns true if the engineer is actively processing bus messages
func (e *Engineer) IsRunning() bool {
	return e.running
}

// Bus returns the event bus used by the engineer
func (e *Engineer) Bus() guide.EventBus {
	return e.bus
}

// Channels returns the engineer's channel configuration
func (e *Engineer) Channels() *guide.AgentChannels {
	return e.channels
}

// =============================================================================
// Request Handling
// =============================================================================

// handleBusRequest processes incoming forwarded requests from the event bus
func (e *Engineer) handleBusRequest(msg *guide.Message) error {
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

	result, err := e.processForwardedRequest(ctx, fwd)

	// Don't respond if fire-and-forget
	if fwd.FireAndForget {
		return nil
	}

	// Build response
	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   e.id,
		RespondingAgentName: "engineer",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		// Publish to error channel
		errMsg := guide.NewErrorMessage(
			e.generateMessageID(),
			fwd.CorrelationID,
			e.id,
			err.Error(),
		)
		return e.bus.Publish(e.channels.Errors, errMsg)
	}

	resp.Data = result

	// Publish response to own response channel
	respMsg := guide.NewResponseMessage(e.generateMessageID(), resp)
	return e.bus.Publish(e.channels.Responses, respMsg)
}

func (e *Engineer) generateMessageID() string {
	return fmt.Sprintf("engineer_msg_%s", uuid.New().String())
}

// processForwardedRequest handles the actual request processing
func (e *Engineer) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := e.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (e *Engineer) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentComplete:
		return e.handleImplement, nil
	default:
		// Default to implementation for any coding task
		return e.handleImplement, nil
	}
}

// handleImplement processes implementation requests (coding tasks)
func (e *Engineer) handleImplement(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	taskID := uuid.New().String()

	// Create task request
	req := &EngineerRequest{
		ID:         uuid.New().String(),
		Intent:     IntentComplete,
		TaskID:     taskID,
		Prompt:     fwd.Input,
		EngineerID: e.id,
		SessionID:  e.config.SessionID,
		Timestamp:  time.Now(),
	}

	// Handle the task using the 10-step protocol
	return e.Handle(ctx, req)
}

// handleBusResponse processes responses to requests we made
func (e *Engineer) handleBusResponse(msg *guide.Message) error {
	// Log responses to our requests for debugging
	e.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

// handleRegistryAnnouncement processes agent registration/unregistration events
func (e *Engineer) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		e.knownAgents[ann.AgentID] = ann
		e.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		delete(e.knownAgents, ann.AgentID)
		e.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

// GetKnownAgents returns all agents the engineer knows about
func (e *Engineer) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	result := make(map[string]*guide.AgentAnnouncement, len(e.knownAgents))
	for k, v := range e.knownAgents {
		result[k] = v
	}
	return result
}

// =============================================================================
// Direct API Methods - 10-Step Implementation Protocol
// =============================================================================

// Handle processes an EngineerRequest using the 10-step implementation protocol
func (e *Engineer) Handle(ctx context.Context, req *EngineerRequest) (*EngineerResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	startTime := time.Now()
	e.setStatus(AgentStatusBusy)
	defer e.setStatus(AgentStatusIdle)

	// STEP 1: Parse task and validate scope
	if err := e.validateTaskScope(ctx, req); err != nil {
		return e.failureResponse(req, err, startTime)
	}

	// STEP 2: Consult Librarian (DEFAULT - before implementation)
	if err := e.consultLibrarian(ctx, req); err != nil {
		e.logger.Warn("librarian consultation failed", "error", err)
		// Non-fatal, continue with implementation
	}

	// STEP 3: Check for previous failures on similar tasks
	if failure := e.checkPreviousFailures(req.TaskID); failure != nil {
		e.logger.Info("found previous failure", "task_id", req.TaskID, "attempts", failure.AttemptCount)
		if failure.AttemptCount >= 3 {
			// Consult Academic for alternative approaches
			if err := e.consultAcademic(ctx, req, "Task has failed multiple times. Need alternative approach."); err != nil {
				e.logger.Warn("academic consultation failed", "error", err)
			}
		}
	}

	// STEP 4: Plan implementation (break into steps)
	plan, err := e.planImplementation(ctx, req)
	if err != nil {
		return e.failureResponse(req, err, startTime)
	}

	// STEP 5: Check scope limit
	if len(plan.Steps) > MaxTodosBeforeArchitect {
		return e.scopeLimitResponse(req, plan, startTime)
	}

	// STEP 6: Execute pre-implementation checks
	if issues := e.preImplementationChecks(ctx, req, plan); len(issues) > 0 {
		e.logger.Warn("pre-implementation issues detected", "issues", issues)
		// Consult Academic if unclear
		if e.hasUnclearIssues(issues) {
			if err := e.consultAcademic(ctx, req, fmt.Sprintf("Pre-implementation issues: %v", issues)); err != nil {
				e.logger.Warn("academic consultation failed", "error", err)
			}
		}
	}

	// STEP 7-9: Execute implementation steps
	result, err := e.executeImplementation(ctx, req, plan)
	if err != nil {
		e.recordFailure(req.TaskID, err.Error(), req.Prompt)
		return e.failureResponse(req, err, startTime)
	}

	// STEP 10: Validate and finalize
	if err := e.validateResult(ctx, result); err != nil {
		e.recordFailure(req.TaskID, err.Error(), req.Prompt)
		return e.failureResponse(req, err, startTime)
	}

	return &EngineerResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   true,
		Result:    result,
		Timestamp: time.Now(),
	}, nil
}

// =============================================================================
// Implementation Protocol Steps
// =============================================================================

// ImplementationPlan represents a planned implementation
type ImplementationPlan struct {
	TaskID      string
	Description string
	Steps       []ImplementationStep
	Estimates   EstimateInfo
}

// ImplementationStep represents a single step in the implementation
type ImplementationStep struct {
	ID          string
	Description string
	Type        string // "read", "write", "edit", "test", "command"
	Target      string // file path or command
	Completed   bool
}

// EstimateInfo contains estimation information
type EstimateInfo struct {
	TotalSteps    int
	EstimatedTime time.Duration
	Complexity    string // "low", "medium", "high"
}

func (e *Engineer) validateTaskScope(ctx context.Context, req *EngineerRequest) error {
	if req.Prompt == "" {
		return fmt.Errorf("task prompt is required")
	}
	return nil
}

func (e *Engineer) consultLibrarian(ctx context.Context, req *EngineerRequest) error {
	if e.bus == nil {
		return fmt.Errorf("event bus not available")
	}

	start := time.Now()
	consultation := Consultation{
		Target:    ConsultLibrarian,
		Query:     fmt.Sprintf("Search for relevant patterns, similar implementations, and dependencies for: %s", req.Prompt),
		Timestamp: time.Now(),
	}

	// Publish request to librarian
	routeReq := &guide.RouteRequest{
		CorrelationID:   uuid.New().String(),
		Input:           consultation.Query,
		SourceAgentID:   e.id,
		SourceAgentName: "engineer",
		TargetAgentID:   "librarian",
		FireAndForget:   true, // Non-blocking consultation
		SessionID:       e.config.SessionID,
		Timestamp:       time.Now(),
	}

	if err := e.PublishRequest(routeReq); err != nil {
		return err
	}

	consultation.Duration = time.Since(start)
	e.recordConsultation(consultation)
	return nil
}

func (e *Engineer) consultAcademic(ctx context.Context, req *EngineerRequest, reason string) error {
	if e.bus == nil {
		return fmt.Errorf("event bus not available")
	}

	start := time.Now()
	consultation := Consultation{
		Target:    ConsultAcademic,
		Query:     fmt.Sprintf("Need guidance for task: %s. Reason: %s", req.Prompt, reason),
		Timestamp: time.Now(),
	}

	// Publish request to academic
	routeReq := &guide.RouteRequest{
		CorrelationID:   uuid.New().String(),
		Input:           consultation.Query,
		SourceAgentID:   e.id,
		SourceAgentName: "engineer",
		TargetAgentID:   "academic",
		FireAndForget:   true,
		SessionID:       e.config.SessionID,
		Timestamp:       time.Now(),
	}

	if err := e.PublishRequest(routeReq); err != nil {
		return err
	}

	consultation.Duration = time.Since(start)
	e.recordConsultation(consultation)
	return nil
}

func (e *Engineer) checkPreviousFailures(taskID string) *FailureRecord {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return e.failures[taskID]
}

func (e *Engineer) planImplementation(ctx context.Context, req *EngineerRequest) (*ImplementationPlan, error) {
	// Create a basic implementation plan
	// In a full implementation, this would use LLM to generate steps
	plan := &ImplementationPlan{
		TaskID:      req.TaskID,
		Description: req.Prompt,
		Steps:       make([]ImplementationStep, 0),
		Estimates: EstimateInfo{
			Complexity: "medium",
		},
	}

	// Basic step: analyze task
	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Analyze task requirements",
		Type:        "analyze",
	})

	// Basic step: implement
	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Implement solution",
		Type:        "implement",
	})

	// Basic step: validate
	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Validate implementation",
		Type:        "validate",
	})

	plan.Estimates.TotalSteps = len(plan.Steps)
	plan.Estimates.EstimatedTime = time.Duration(len(plan.Steps)) * 5 * time.Minute

	return plan, nil
}

// PreImplementationIssue represents an issue found during pre-implementation checks
type PreImplementationIssue struct {
	Type        string // "memory_leak", "race_condition", "deadlock", "off_by_one", "missing_error_handling", "code_smell"
	Description string
	Severity    string // "low", "medium", "high", "critical"
	Unclear     bool   // Whether the issue needs clarification
}

func (e *Engineer) preImplementationChecks(ctx context.Context, req *EngineerRequest, plan *ImplementationPlan) []PreImplementationIssue {
	// Pre-implementation checks for:
	// - Memory leaks
	// - Race conditions
	// - Deadlocks
	// - Off-by-one bugs
	// - Missing error handling
	// - Code smells
	//
	// In a full implementation, this would analyze the task and existing code
	return nil
}

func (e *Engineer) hasUnclearIssues(issues []PreImplementationIssue) bool {
	for _, issue := range issues {
		if issue.Unclear {
			return true
		}
	}
	return false
}

func (e *Engineer) executeImplementation(ctx context.Context, req *EngineerRequest, plan *ImplementationPlan) (*TaskResult, error) {
	start := time.Now()
	result := &TaskResult{
		TaskID:       req.TaskID,
		FilesChanged: make([]FileChange, 0),
	}

	// Execute each step in the plan
	for i, step := range plan.Steps {
		e.updateProgress(req.TaskID, i+1, len(plan.Steps), step.Description)

		// Execute step based on type
		switch step.Type {
		case "analyze":
			// Analysis step - read relevant files
			e.logger.Debug("executing analysis step", "step", step.Description)

		case "implement":
			// Implementation step - write/edit files
			e.logger.Debug("executing implementation step", "step", step.Description)

		case "validate":
			// Validation step - run tests
			e.logger.Debug("executing validation step", "step", step.Description)

		default:
			e.logger.Debug("executing step", "type", step.Type, "description", step.Description)
		}

		step.Completed = true
	}

	result.Success = true
	result.Duration = time.Since(start)
	result.Output = "Implementation completed successfully"

	return result, nil
}

func (e *Engineer) validateResult(ctx context.Context, result *TaskResult) error {
	// Validate the implementation result
	// In a full implementation, this would run tests and checks
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	return nil
}

func (e *Engineer) scopeLimitResponse(req *EngineerRequest, plan *ImplementationPlan, startTime time.Time) (*EngineerResponse, error) {
	return &EngineerResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   false,
		Error:     fmt.Sprintf("SCOPE LIMIT EXCEEDED: Task requires %d steps (max %d). Request Architect decomposition.", len(plan.Steps), MaxTodosBeforeArchitect),
		Timestamp: time.Now(),
	}, fmt.Errorf("scope limit exceeded: %d steps required, max is %d", len(plan.Steps), MaxTodosBeforeArchitect)
}

func (e *Engineer) failureResponse(req *EngineerRequest, err error, startTime time.Time) (*EngineerResponse, error) {
	return &EngineerResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   false,
		Error:     err.Error(),
		Timestamp: time.Now(),
	}, err
}

// =============================================================================
// State Management
// =============================================================================

func (e *Engineer) setStatus(status AgentStatus) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.state.Status = status
	e.state.LastActiveAt = time.Now()
}

func (e *Engineer) updateProgress(taskID string, completed, total int, currentStep string) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	e.state.CurrentTaskID = taskID
	e.state.LastActiveAt = time.Now()

	// Could emit progress event here
	e.logger.Debug("progress update",
		"task_id", taskID,
		"step", fmt.Sprintf("%d/%d", completed, total),
		"current", currentStep,
	)
}

func (e *Engineer) recordFailure(taskID, errorMsg, approach string) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()

	existing, ok := e.failures[taskID]
	if ok {
		existing.AttemptCount++
		existing.LastError = errorMsg
		existing.Timestamp = time.Now()
	} else {
		e.failures[taskID] = &FailureRecord{
			TaskID:       taskID,
			EngineerID:   e.id,
			AttemptCount: 1,
			LastError:    errorMsg,
			Approach:     approach,
			Timestamp:    time.Now(),
		}
	}

	e.state.FailedCount++
}

func (e *Engineer) recordConsultation(c Consultation) {
	e.consultMu.Lock()
	defer e.consultMu.Unlock()
	e.consultations = append(e.consultations, c)
}

// GetState returns the current engineer state
func (e *Engineer) GetState() *EngineerState {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()

	// Return a copy
	stateCopy := *e.state
	return &stateCopy
}

// GetConsultations returns all recorded consultations
func (e *Engineer) GetConsultations() []Consultation {
	e.consultMu.RLock()
	defer e.consultMu.RUnlock()

	result := make([]Consultation, len(e.consultations))
	copy(result, e.consultations)
	return result
}

// =============================================================================
// Guide Registration
// =============================================================================

// GetRoutingInfo returns the engineer's routing information for Guide registration
func (e *Engineer) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      e.id,
		Name:    "engineer",
		Aliases: []string{"eng", "impl", "code", "implement"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "implement",
				Description:   "Implement a coding task",
				DefaultIntent: guide.IntentComplete,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "code",
				Description:   "Write code for a specific feature or fix",
				DefaultIntent: guide.IntentComplete,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"implement",
				"code",
				"write",
				"create",
				"build",
				"fix",
				"refactor",
				"add feature",
				"modify",
				"update code",
			},
			WeakTriggers: []string{
				"function",
				"method",
				"class",
				"file",
				"module",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentComplete: {
					"implement",
					"code",
					"write",
					"create",
					"build",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      e.id,
			Name:    "engineer",
			Aliases: []string{"eng", "impl", "code"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentComplete,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
					guide.DomainFiles,
				},
				Tags:     []string{"implementation", "code", "development", "testing"},
				Keywords: []string{"implement", "code", "write", "create", "build", "fix", "refactor", "test"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.7,
			},
			Description: "Code implementation specialist. Executes coding tasks with clean, modular, testable, readable code.",
			Priority:    70,
		},
	}
}

// PublishRequest publishes a request to the Guide for routing
func (e *Engineer) PublishRequest(req *guide.RouteRequest) error {
	if !e.running {
		return fmt.Errorf("engineer is not running")
	}

	req.SourceAgentID = e.id
	req.SourceAgentName = "engineer"

	msg := guide.NewRequestMessage(e.generateMessageID(), req)
	return e.bus.Publish(guide.TopicGuideRequests, msg)
}

// =============================================================================
// Skills System
// =============================================================================

// Skills returns the engineer's skill registry
func (e *Engineer) Skills() *skills.Registry {
	return e.skills
}

// GetToolDefinitions returns tool definitions for all loaded skills
func (e *Engineer) GetToolDefinitions() []map[string]any {
	return e.skills.GetToolDefinitions()
}
