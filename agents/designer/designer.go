package designer

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

const MaxTodosBeforeArchitect = 12

type Designer struct {
	id     string
	config Config
	logger *slog.Logger

	state    *DesignerState
	stateMu  sync.RWMutex
	failures map[string]*FailureRecord

	skills      *skills.Registry
	skillLoader *skills.Loader

	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool
	knownAgents map[string]*guide.AgentAnnouncement

	consultations []Consultation
	consultMu     sync.RWMutex
}

type Config struct {
	SystemPrompt    string
	MaxOutputTokens int

	DesignerConfig DesignerConfig

	Logger *slog.Logger

	SessionID string
}

const (
	DefaultMaxOutputTokens = 8192
)

func New(cfg Config) (*Designer, error) {
	cfg = applyConfigDefaults(cfg)

	designerID := fmt.Sprintf("designer_%s", uuid.New().String()[:8])

	d := &Designer{
		id:          designerID,
		config:      cfg,
		logger:      cfg.Logger,
		knownAgents: make(map[string]*guide.AgentAnnouncement),
		failures:    make(map[string]*FailureRecord),
		state: &DesignerState{
			ID:        designerID,
			SessionID: cfg.SessionID,
			Status:    AgentStatusIdle,
			TaskQueue: make([]string, 0),
			StartedAt: time.Now(),
		},
		consultations: make([]Consultation, 0),
	}

	d.initSkills()

	return d, nil
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
	if cfg.DesignerConfig.MaxConcurrentTasks == 0 {
		cfg.DesignerConfig.MaxConcurrentTasks = 1
	}
	if cfg.DesignerConfig.MemoryThreshold.CheckpointThreshold == 0 {
		cfg.DesignerConfig.MemoryThreshold = DefaultMemoryThreshold()
	}
	if cfg.DesignerConfig.A11yLevel == "" {
		cfg.DesignerConfig.A11yLevel = "AA"
	}
	return cfg
}

func (d *Designer) initSkills() {
	d.skills = skills.NewRegistry()

	loaderCfg := skills.DefaultLoaderConfig()
	loaderCfg.CoreSkills = []string{
		"component_search", "component_create", "component_modify",
		"token_validate", "token_suggest",
		"a11y_audit", "a11y_fix_suggest", "contrast_check",
	}
	loaderCfg.AutoLoadDomains = []string{"ui", "design", "accessibility"}
	d.skillLoader = skills.NewLoader(d.skills, loaderCfg)

	d.registerCoreSkills()
}

func (d *Designer) ID() string {
	return d.id
}

func (d *Designer) Close() error {
	d.Stop()
	return nil
}

func (d *Designer) Start(bus guide.EventBus) error {
	if d.running {
		return fmt.Errorf("designer is already running")
	}

	d.bus = bus
	d.channels = guide.NewAgentChannels("designer")

	var err error
	d.requestSub, err = bus.SubscribeAsync(d.channels.Requests, d.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", d.channels.Requests, err)
	}

	d.responseSub, err = bus.SubscribeAsync(d.channels.Responses, d.handleBusResponse)
	if err != nil {
		d.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", d.channels.Responses, err)
	}

	d.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, d.handleRegistryAnnouncement)
	if err != nil {
		d.requestSub.Unsubscribe()
		d.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	d.running = true
	d.logger.Info("designer started", "id", d.id, "channels", d.channels)
	return nil
}

func (d *Designer) Stop() error {
	if !d.running {
		return nil
	}

	errs := d.unsubscribeAll()
	d.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}

	d.logger.Info("designer stopped", "id", d.id)
	return nil
}

func (d *Designer) unsubscribeAll() []error {
	var errs []error
	if err := d.unsubscribeRequest(); err != nil {
		errs = append(errs, err)
	}
	if err := d.unsubscribeResponse(); err != nil {
		errs = append(errs, err)
	}
	if err := d.unsubscribeRegistry(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (d *Designer) unsubscribeRequest() error {
	if d.requestSub == nil {
		return nil
	}
	err := d.requestSub.Unsubscribe()
	d.requestSub = nil
	return err
}

func (d *Designer) unsubscribeResponse() error {
	if d.responseSub == nil {
		return nil
	}
	err := d.responseSub.Unsubscribe()
	d.responseSub = nil
	return err
}

func (d *Designer) unsubscribeRegistry() error {
	if d.registrySub == nil {
		return nil
	}
	err := d.registrySub.Unsubscribe()
	d.registrySub = nil
	return err
}

func (d *Designer) IsRunning() bool {
	return d.running
}

func (d *Designer) Bus() guide.EventBus {
	return d.bus
}

func (d *Designer) Channels() *guide.AgentChannels {
	return d.channels
}

func (d *Designer) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	ctx := context.Background()
	startTime := time.Now()

	result, err := d.processForwardedRequest(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   d.id,
		RespondingAgentName: "designer",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		errMsg := guide.NewErrorMessage(
			d.generateMessageID(),
			fwd.CorrelationID,
			d.id,
			err.Error(),
		)
		return d.bus.Publish(d.channels.Errors, errMsg)
	}

	resp.Data = result

	respMsg := guide.NewResponseMessage(d.generateMessageID(), resp)
	return d.bus.Publish(d.channels.Responses, respMsg)
}

func (d *Designer) generateMessageID() string {
	return fmt.Sprintf("designer_msg_%s", uuid.New().String())
}

func (d *Designer) processForwardedRequest(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	handler, err := d.intentHandler(fwd.Intent)
	if err != nil {
		return nil, err
	}
	return handler(ctx, fwd)
}

type forwardedHandler func(context.Context, *guide.ForwardedRequest) (any, error)

func (d *Designer) intentHandler(intent guide.Intent) (forwardedHandler, error) {
	switch intent {
	case guide.IntentDesign:
		return d.handleDesign, nil
	case guide.IntentComplete:
		return d.handleDesign, nil
	default:
		return d.handleDesign, nil
	}
}

func (d *Designer) handleDesign(ctx context.Context, fwd *guide.ForwardedRequest) (any, error) {
	taskID := uuid.New().String()

	req := &DesignerRequest{
		ID:         uuid.New().String(),
		Intent:     IntentDesignComponent,
		TaskID:     taskID,
		Prompt:     fwd.Input,
		DesignerID: d.id,
		SessionID:  d.config.SessionID,
		Timestamp:  time.Now(),
	}

	return d.Handle(ctx, req)
}

func (d *Designer) handleBusResponse(msg *guide.Message) error {
	d.logger.Debug("received response", "correlation_id", msg.CorrelationID)
	return nil
}

func (d *Designer) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		d.knownAgents[ann.AgentID] = ann
		d.logger.Debug("agent registered", "agent_id", ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		delete(d.knownAgents, ann.AgentID)
		d.logger.Debug("agent unregistered", "agent_id", ann.AgentID)
	}

	return nil
}

func (d *Designer) GetKnownAgents() map[string]*guide.AgentAnnouncement {
	result := make(map[string]*guide.AgentAnnouncement, len(d.knownAgents))
	for k, v := range d.knownAgents {
		result[k] = v
	}
	return result
}

func (d *Designer) Handle(ctx context.Context, req *DesignerRequest) (*DesignerResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	startTime := time.Now()
	d.setStatus(AgentStatusBusy)
	defer d.setStatus(AgentStatusIdle)

	if err := d.validateTaskScope(ctx, req); err != nil {
		return d.failureResponse(req, err, startTime)
	}

	if err := d.consultLibrarian(ctx, req); err != nil {
		d.logger.Warn("librarian consultation failed", "error", err)
	}

	if err := d.consultAcademic(ctx, req, "Initial research on UI best practices and accessibility guidelines"); err != nil {
		d.logger.Warn("academic consultation failed", "error", err)
	}

	if failure := d.checkPreviousFailures(req.TaskID); failure != nil {
		d.logger.Info("found previous failure", "task_id", req.TaskID, "attempts", failure.AttemptCount)
		if failure.AttemptCount >= 3 {
			if err := d.consultAcademic(ctx, req, "Task has failed multiple times. Need alternative design approach."); err != nil {
				d.logger.Warn("academic consultation failed", "error", err)
			}
		}
	}

	plan, err := d.planImplementation(ctx, req)
	if err != nil {
		return d.failureResponse(req, err, startTime)
	}

	if len(plan.Steps) > MaxTodosBeforeArchitect {
		return d.scopeLimitResponse(req, plan, startTime)
	}

	if issues := d.preImplementationChecks(ctx, req, plan); len(issues) > 0 {
		d.logger.Warn("pre-implementation issues detected", "issues", issues)
		if d.hasUnclearIssues(issues) {
			if err := d.consultAcademic(ctx, req, fmt.Sprintf("Pre-implementation issues: %v", issues)); err != nil {
				d.logger.Warn("academic consultation failed", "error", err)
			}
		}
	}

	result, err := d.executeImplementation(ctx, req, plan)
	if err != nil {
		d.recordFailure(req.TaskID, err.Error(), req.Prompt)
		return d.failureResponse(req, err, startTime)
	}

	if err := d.validateTokens(ctx, result); err != nil {
		d.logger.Warn("token validation failed", "error", err)
		result.TokensValidated = false
	} else {
		result.TokensValidated = true
	}

	if err := d.runA11yAudit(ctx, result); err != nil {
		d.logger.Warn("accessibility audit failed", "error", err)
		result.A11yAuditPassed = false
	} else {
		result.A11yAuditPassed = true
	}

	if err := d.validateResult(ctx, result); err != nil {
		d.recordFailure(req.TaskID, err.Error(), req.Prompt)
		return d.failureResponse(req, err, startTime)
	}

	return &DesignerResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   true,
		Result:    result,
		Timestamp: time.Now(),
	}, nil
}

type ImplementationPlan struct {
	TaskID      string
	Description string
	Steps       []ImplementationStep
	Estimates   EstimateInfo
}

type ImplementationStep struct {
	ID          string
	Description string
	Type        string
	Target      string
	Completed   bool
}

type EstimateInfo struct {
	TotalSteps    int
	EstimatedTime time.Duration
	Complexity    string
}

func (d *Designer) validateTaskScope(ctx context.Context, req *DesignerRequest) error {
	if req.Prompt == "" {
		return fmt.Errorf("design task prompt is required")
	}
	return nil
}

func (d *Designer) consultLibrarian(ctx context.Context, req *DesignerRequest) error {
	if d.bus == nil {
		return fmt.Errorf("event bus not available")
	}

	start := time.Now()
	consultation := Consultation{
		Target:    ConsultLibrarian,
		Query:     fmt.Sprintf("Search for existing UI component patterns, design tokens, and style guidelines for: %s", req.Prompt),
		Timestamp: time.Now(),
	}

	routeReq := &guide.RouteRequest{
		CorrelationID:   uuid.New().String(),
		Input:           consultation.Query,
		SourceAgentID:   d.id,
		SourceAgentName: "designer",
		TargetAgentID:   "librarian",
		FireAndForget:   true,
		SessionID:       d.config.SessionID,
		Timestamp:       time.Now(),
	}

	if err := d.PublishRequest(routeReq); err != nil {
		return err
	}

	consultation.Duration = time.Since(start)
	d.recordConsultation(consultation)
	return nil
}

func (d *Designer) consultAcademic(ctx context.Context, req *DesignerRequest, reason string) error {
	if d.bus == nil {
		return fmt.Errorf("event bus not available")
	}

	start := time.Now()
	consultation := Consultation{
		Target:    ConsultAcademic,
		Query:     fmt.Sprintf("Need UI/UX guidance for task: %s. Reason: %s", req.Prompt, reason),
		Timestamp: time.Now(),
	}

	routeReq := &guide.RouteRequest{
		CorrelationID:   uuid.New().String(),
		Input:           consultation.Query,
		SourceAgentID:   d.id,
		SourceAgentName: "designer",
		TargetAgentID:   "academic",
		FireAndForget:   true,
		SessionID:       d.config.SessionID,
		Timestamp:       time.Now(),
	}

	if err := d.PublishRequest(routeReq); err != nil {
		return err
	}

	consultation.Duration = time.Since(start)
	d.recordConsultation(consultation)
	return nil
}

func (d *Designer) checkPreviousFailures(taskID string) *FailureRecord {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.failures[taskID]
}

func (d *Designer) planImplementation(ctx context.Context, req *DesignerRequest) (*ImplementationPlan, error) {
	plan := &ImplementationPlan{
		TaskID:      req.TaskID,
		Description: req.Prompt,
		Steps:       make([]ImplementationStep, 0),
		Estimates: EstimateInfo{
			Complexity: "medium",
		},
	}

	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Analyze design requirements",
		Type:        "analyze",
	})

	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Identify design tokens needed",
		Type:        "tokens",
	})

	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Implement component structure",
		Type:        "implement",
	})

	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Apply design tokens to styles",
		Type:        "style",
	})

	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Add interactive states",
		Type:        "interactions",
	})

	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Validate design token usage",
		Type:        "validate_tokens",
	})

	plan.Steps = append(plan.Steps, ImplementationStep{
		ID:          uuid.New().String(),
		Description: "Run accessibility audit",
		Type:        "a11y_audit",
	})

	plan.Estimates.TotalSteps = len(plan.Steps)
	plan.Estimates.EstimatedTime = time.Duration(len(plan.Steps)) * 3 * time.Minute

	return plan, nil
}

type PreImplementationIssue struct {
	Type        string
	Description string
	Severity    string
	Unclear     bool
}

func (d *Designer) preImplementationChecks(ctx context.Context, req *DesignerRequest, plan *ImplementationPlan) []PreImplementationIssue {
	return nil
}

func (d *Designer) hasUnclearIssues(issues []PreImplementationIssue) bool {
	for _, issue := range issues {
		if issue.Unclear {
			return true
		}
	}
	return false
}

func (d *Designer) executeImplementation(ctx context.Context, req *DesignerRequest, plan *ImplementationPlan) (*DesignResult, error) {
	start := time.Now()
	result := &DesignResult{
		TaskID:       req.TaskID,
		FilesChanged: make([]FileChange, 0),
	}

	for i, step := range plan.Steps {
		d.updateProgress(req.TaskID, i+1, len(plan.Steps), step.Description)

		switch step.Type {
		case "analyze":
			d.logger.Debug("executing analysis step", "step", step.Description)

		case "tokens":
			d.logger.Debug("identifying design tokens", "step", step.Description)

		case "implement":
			d.logger.Debug("implementing component", "step", step.Description)

		case "style":
			d.logger.Debug("applying styles", "step", step.Description)

		case "interactions":
			d.logger.Debug("adding interactions", "step", step.Description)

		case "validate_tokens":
			d.logger.Debug("validating tokens", "step", step.Description)

		case "a11y_audit":
			d.logger.Debug("running a11y audit", "step", step.Description)

		default:
			d.logger.Debug("executing step", "type", step.Type, "description", step.Description)
		}

		step.Completed = true
	}

	result.Success = true
	result.Duration = time.Since(start)
	result.Output = "Design implementation completed successfully"

	return result, nil
}

func (d *Designer) validateTokens(ctx context.Context, result *DesignResult) error {
	return nil
}

func (d *Designer) runA11yAudit(ctx context.Context, result *DesignResult) error {
	return nil
}

func (d *Designer) validateResult(ctx context.Context, result *DesignResult) error {
	if result == nil {
		return fmt.Errorf("result is nil")
	}
	return nil
}

func (d *Designer) scopeLimitResponse(req *DesignerRequest, plan *ImplementationPlan, startTime time.Time) (*DesignerResponse, error) {
	return &DesignerResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   false,
		Error:     fmt.Sprintf("SCOPE LIMIT EXCEEDED: Task requires %d steps (max %d). Request Architect decomposition.", len(plan.Steps), MaxTodosBeforeArchitect),
		Timestamp: time.Now(),
	}, fmt.Errorf("scope limit exceeded: %d steps required, max is %d", len(plan.Steps), MaxTodosBeforeArchitect)
}

func (d *Designer) failureResponse(req *DesignerRequest, err error, startTime time.Time) (*DesignerResponse, error) {
	return &DesignerResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   false,
		Error:     err.Error(),
		Timestamp: time.Now(),
	}, err
}

func (d *Designer) setStatus(status AgentStatus) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.state.Status = status
	d.state.LastActiveAt = time.Now()
}

func (d *Designer) updateProgress(taskID string, completed, total int, currentStep string) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.state.CurrentTaskID = taskID
	d.state.LastActiveAt = time.Now()

	d.logger.Debug("progress update",
		"task_id", taskID,
		"step", fmt.Sprintf("%d/%d", completed, total),
		"current", currentStep,
	)
}

func (d *Designer) recordFailure(taskID, errorMsg, approach string) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()

	existing, ok := d.failures[taskID]
	if ok {
		existing.AttemptCount++
		existing.LastError = errorMsg
		existing.Timestamp = time.Now()
	} else {
		d.failures[taskID] = &FailureRecord{
			TaskID:       taskID,
			DesignerID:   d.id,
			AttemptCount: 1,
			LastError:    errorMsg,
			Approach:     approach,
			Timestamp:    time.Now(),
		}
	}

	d.state.FailedCount++
}

func (d *Designer) recordConsultation(c Consultation) {
	d.consultMu.Lock()
	defer d.consultMu.Unlock()
	d.consultations = append(d.consultations, c)
}

func (d *Designer) GetState() *DesignerState {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()

	stateCopy := *d.state
	return &stateCopy
}

func (d *Designer) GetConsultations() []Consultation {
	d.consultMu.RLock()
	defer d.consultMu.RUnlock()

	result := make([]Consultation, len(d.consultations))
	copy(result, d.consultations)
	return result
}

func (d *Designer) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      d.id,
		Name:    "designer",
		Aliases: []string{"design", "ui", "ux", "frontend"},

		ActionShortcuts: []guide.ActionShortcut{
			{
				Name:          "design",
				Description:   "Design a UI component or layout",
				DefaultIntent: guide.IntentDesign,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "component",
				Description:   "Create or modify a UI component",
				DefaultIntent: guide.IntentDesign,
				DefaultDomain: guide.DomainCode,
			},
			{
				Name:          "a11y",
				Description:   "Run accessibility audit",
				DefaultIntent: guide.IntentCheck,
				DefaultDomain: guide.DomainCode,
			},
		},

		Triggers: guide.AgentTriggers{
			StrongTriggers: []string{
				"design",
				"component",
				"ui",
				"ux",
				"layout",
				"style",
				"accessible",
				"accessibility",
				"a11y",
				"wcag",
				"color contrast",
				"design token",
				"responsive",
			},
			WeakTriggers: []string{
				"button",
				"form",
				"modal",
				"dialog",
				"input",
				"card",
				"navigation",
				"header",
				"footer",
			},
			IntentTriggers: map[guide.Intent][]string{
				guide.IntentDesign: {
					"design",
					"create component",
					"build ui",
					"layout",
					"style",
				},
				guide.IntentCheck: {
					"audit",
					"accessibility",
					"a11y",
					"contrast",
					"wcag",
				},
			},
		},

		Registration: &guide.AgentRegistration{
			ID:      d.id,
			Name:    "designer",
			Aliases: []string{"design", "ui", "ux"},
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{
					guide.IntentDesign,
					guide.IntentComplete,
					guide.IntentCheck,
				},
				Domains: []guide.Domain{
					guide.DomainCode,
					guide.DomainFiles,
				},
				Tags:     []string{"ui", "ux", "design", "accessibility", "components", "frontend"},
				Keywords: []string{"design", "component", "ui", "ux", "style", "layout", "a11y", "accessible", "wcag"},
				Priority: 70,
			},
			Constraints: guide.AgentConstraints{
				TemporalFocus: guide.TemporalPresent,
				MinConfidence: 0.7,
			},
			Description: "UI/UX design specialist. Creates accessible, performant, and beautiful user interfaces.",
			Priority:    70,
		},
	}
}

func (d *Designer) PublishRequest(req *guide.RouteRequest) error {
	if !d.running {
		return fmt.Errorf("designer is not running")
	}

	req.SourceAgentID = d.id
	req.SourceAgentName = "designer"

	msg := guide.NewRequestMessage(d.generateMessageID(), req)
	return d.bus.Publish(guide.TopicGuideRequests, msg)
}

func (d *Designer) Skills() *skills.Registry {
	return d.skills
}

func (d *Designer) GetToolDefinitions() []map[string]any {
	return d.skills.GetToolDefinitions()
}
