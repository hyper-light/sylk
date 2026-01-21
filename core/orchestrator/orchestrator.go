package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

// Orchestrator is a read-only workflow observer and coordinator.
// Identity: Claude Haiku 4.5
// Role: Monitor workflows, track task health, submit events to Archivalist
type Orchestrator struct {
	config Config
	state  *State

	bus         guide.EventBus
	channels    *guide.AgentChannels
	requestSub  guide.Subscription
	responseSub guide.Subscription
	registrySub guide.Subscription
	running     bool

	skills      *skills.Registry
	skillLoader *skills.Loader
	hooks       *skills.HookRegistry

	healthMonitor *HealthMonitor

	knownAgents map[string]*guide.AgentAnnouncement

	mu sync.RWMutex
}

// New creates a new Orchestrator agent
func New(cfg Config) (*Orchestrator, error) {
	cfg = applyConfigDefaults(cfg)

	skillsRegistry := skills.NewRegistry()
	skillsLoaderCfg := skills.DefaultLoaderConfig()
	skillsLoaderCfg.CoreSkills = []string{
		"query_task", "query_workflow", "push_status",
		"generate_summary", "report_failure",
		"submit_task_event", "archivalist_request",
	}
	skillsLoaderCfg.AutoLoadDomains = []string{"orchestration", "monitoring"}
	skillLoader := skills.NewLoader(skillsRegistry, skillsLoaderCfg)
	hookRegistry := skills.NewHookRegistry()

	o := &Orchestrator{
		config:      cfg,
		state:       NewState(cfg.SessionID),
		skills:      skillsRegistry,
		skillLoader: skillLoader,
		hooks:       hookRegistry,
		knownAgents: make(map[string]*guide.AgentAnnouncement),
	}

	o.healthMonitor = NewHealthMonitor(o, cfg.HealthConfig)
	o.registerCoreSkills()

	return o, nil
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.Model == "" {
		cfg.Model = "claude-haiku-4-5-20251001"
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = 1024
	}
	if cfg.AgentID == "" {
		cfg.AgentID = "orchestrator"
	}
	if cfg.HealthConfig.TaskTimeout == 0 {
		cfg.HealthConfig = DefaultHealthConfig()
	}
	if cfg.BufferConfig.MaxUpdates == 0 {
		cfg.BufferConfig = DefaultUpdateBufferConfig()
	}
	if cfg.SummaryConfig.MaxTokens == 0 {
		cfg.SummaryConfig = DefaultSummaryConfig()
	}
	return cfg
}

// Start begins listening for messages on the event bus
func (o *Orchestrator) Start(bus guide.EventBus) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.running {
		return fmt.Errorf("orchestrator is already running")
	}

	o.bus = bus
	o.channels = guide.NewAgentChannels(o.config.AgentID)

	var err error
	o.requestSub, err = bus.SubscribeAsync(o.channels.Requests, o.handleBusRequest)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", o.channels.Requests, err)
	}

	o.responseSub, err = bus.SubscribeAsync(o.channels.Responses, o.handleBusResponse)
	if err != nil {
		o.requestSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", o.channels.Responses, err)
	}

	o.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, o.handleRegistryAnnouncement)
	if err != nil {
		o.requestSub.Unsubscribe()
		o.responseSub.Unsubscribe()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	o.subscribeToTaskEvents()
	o.healthMonitor.Start(context.Background())

	o.running = true
	return nil
}

func (o *Orchestrator) subscribeToTaskEvents() {
	o.bus.SubscribeAsync("tasks.dispatch", o.handleTaskDispatch)
	o.bus.SubscribeAsync("tasks.complete", o.handleTaskComplete)
	o.bus.SubscribeAsync("tasks.failed", o.handleTaskFailed)
	o.bus.SubscribeAsync("workflows.status", o.handleWorkflowStatus)
}

// Stop unsubscribes from event bus topics
func (o *Orchestrator) Stop() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.running {
		return nil
	}

	o.healthMonitor.Stop()
	o.flushUpdateBuffer()

	errs := o.unsubscribeAll()
	o.running = false

	if len(errs) > 0 {
		return fmt.Errorf("errors during stop: %v", errs)
	}
	return nil
}

func (o *Orchestrator) unsubscribeAll() []error {
	var errs []error
	if o.requestSub != nil {
		if err := o.requestSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	if o.responseSub != nil {
		if err := o.responseSub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	if o.registrySub != nil {
		if err := o.registrySub.Unsubscribe(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Handle processes workflow coordination requests
func (o *Orchestrator) Handle(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	switch req.Intent {
	case guide.IntentStatus:
		return o.handleStatusQuery(ctx, req)
	case guide.IntentRecall:
		return o.handleRecallQuery(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported intent: %s", req.Intent)
	}
}

func (o *Orchestrator) handleStatusQuery(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	switch req.Domain {
	case guide.DomainTasks:
		return o.getTaskStatus(req.Entities)
	case "workflow", "workflows":
		return o.getWorkflowStatus(req.Entities)
	default:
		return o.GetSummary(ctx)
	}
}

func (o *Orchestrator) handleRecallQuery(ctx context.Context, req *guide.ForwardedRequest) (any, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if req.Domain == guide.DomainFailures {
		return o.queryFailurePatterns(ctx, req.Entities)
	}

	return o.state, nil
}

func (o *Orchestrator) getTaskStatus(entities *guide.ExtractedEntities) (any, error) {
	if entities == nil || entities.Query == "" {
		return o.state.Tasks, nil
	}

	taskID := entities.Query
	task, ok := o.state.Tasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	return task, nil
}

func (o *Orchestrator) getWorkflowStatus(entities *guide.ExtractedEntities) (any, error) {
	if entities == nil || entities.Query == "" {
		return o.state.Workflows, nil
	}

	workflowID := entities.Query
	workflow, ok := o.state.Workflows[workflowID]
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}
	return workflow, nil
}

func (o *Orchestrator) queryFailurePatterns(ctx context.Context, entities *guide.ExtractedEntities) ([]FailurePattern, error) {
	query := FailureQuery{Limit: 10}
	if entities != nil && entities.AgentID != "" {
		query.AgentIDs = []string{entities.AgentID}
	}
	return o.QueryArchivalistForFailures(ctx, query)
}

// handleBusRequest processes incoming requests from the event bus
func (o *Orchestrator) handleBusRequest(msg *guide.Message) error {
	if msg.Type != guide.MessageTypeForward {
		return nil
	}

	fwd, ok := msg.GetForwardedRequest()
	if !ok {
		return fmt.Errorf("invalid forward request payload")
	}

	ctx := context.Background()
	startTime := time.Now()

	result, err := o.Handle(ctx, fwd)

	if fwd.FireAndForget {
		return nil
	}

	resp := &guide.RouteResponse{
		CorrelationID:       fwd.CorrelationID,
		Success:             err == nil,
		RespondingAgentID:   o.config.AgentID,
		RespondingAgentName: "orchestrator",
		ProcessingTime:      time.Since(startTime),
	}

	if err != nil {
		resp.Error = err.Error()
		errMsg := guide.NewErrorMessage(
			generateMessageID(),
			fwd.CorrelationID,
			o.config.AgentID,
			err.Error(),
		)
		return o.bus.Publish(o.channels.Errors, errMsg)
	}

	resp.Data = result
	respMsg := guide.NewResponseMessage(generateMessageID(), resp)
	return o.bus.Publish(o.channels.Responses, respMsg)
}

func (o *Orchestrator) handleBusResponse(msg *guide.Message) error {
	return nil
}

func (o *Orchestrator) handleRegistryAnnouncement(msg *guide.Message) error {
	ann, ok := msg.GetAgentAnnouncement()
	if !ok {
		return nil
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	switch msg.Type {
	case guide.MessageTypeAgentRegistered:
		o.knownAgents[ann.AgentID] = ann
		o.healthMonitor.RegisterAgent(ann.AgentID)
	case guide.MessageTypeAgentUnregistered:
		delete(o.knownAgents, ann.AgentID)
		o.healthMonitor.UnregisterAgent(ann.AgentID)
	}

	return nil
}

// Task event handlers
func (o *Orchestrator) handleTaskDispatch(msg *guide.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	taskID, _ := data["task_id"].(string)
	workflowID, _ := data["workflow_id"].(string)
	name, _ := data["name"].(string)
	agentID, _ := data["agent_id"].(string)

	now := time.Now()
	task := &TaskRecord{
		ID:              taskID,
		WorkflowID:      workflowID,
		Name:            name,
		Status:          TaskStatusRunning,
		AssignedAgentID: agentID,
		AssignedAt:      &now,
		CreatedAt:       now,
		StartedAt:       &now,
		SessionID:       o.config.SessionID,
	}

	o.state.Tasks[taskID] = task
	o.healthMonitor.RecordTaskStart(agentID, taskID)

	if workflowID != "" {
		if wf, ok := o.state.Workflows[workflowID]; ok {
			wf.TaskIDs = append(wf.TaskIDs, taskID)
		}
	}

	return nil
}

func (o *Orchestrator) handleTaskComplete(msg *guide.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	taskID, _ := data["task_id"].(string)
	result := data["result"]

	task, ok := o.state.Tasks[taskID]
	if !ok {
		return nil
	}

	now := time.Now()
	task.Status = TaskStatusCompleted
	task.CompletedAt = &now
	task.Result = result
	o.state.Stats.CompletedTasks++

	o.healthMonitor.RecordTaskComplete(task.AssignedAgentID, taskID)
	o.updateWorkflowProgress(task.WorkflowID)

	go o.submitTaskEvent(task)

	return nil
}

func (o *Orchestrator) handleTaskFailed(msg *guide.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	taskID, _ := data["task_id"].(string)
	errorMsg, _ := data["error"].(string)

	task, ok := o.state.Tasks[taskID]
	if !ok {
		return nil
	}

	now := time.Now()
	task.Status = TaskStatusFailed
	task.CompletedAt = &now
	task.Error = errorMsg
	o.state.Stats.FailedTasks++

	o.healthMonitor.RecordTaskFailed(task.AssignedAgentID, taskID, errorMsg)
	o.updateWorkflowProgress(task.WorkflowID)

	go o.submitTaskEvent(task)

	return nil
}

func (o *Orchestrator) handleWorkflowStatus(msg *guide.Message) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	workflowID, _ := data["workflow_id"].(string)
	statusStr, _ := data["status"].(string)
	phase, _ := data["phase"].(string)

	workflow, ok := o.state.Workflows[workflowID]
	if !ok {
		workflow = &WorkflowState{
			ID:        workflowID,
			Status:    WorkflowStatusPending,
			StartedAt: time.Now(),
			SessionID: o.config.SessionID,
		}
		o.state.Workflows[workflowID] = workflow
		o.state.Stats.TotalWorkflows++
	}

	workflow.Status = WorkflowStatus(statusStr)
	workflow.Phase = phase
	workflow.UpdatedAt = time.Now()

	if workflow.Status.IsTerminal() {
		now := time.Now()
		workflow.CompletedAt = &now
		o.state.Stats.ActiveWorkflows--
	}

	return nil
}

func (o *Orchestrator) updateWorkflowProgress(workflowID string) {
	if workflowID == "" {
		return
	}

	workflow, ok := o.state.Workflows[workflowID]
	if !ok {
		return
	}

	completed := 0
	failed := 0
	for _, taskID := range workflow.TaskIDs {
		if task, ok := o.state.Tasks[taskID]; ok {
			switch task.Status {
			case TaskStatusCompleted:
				completed++
			case TaskStatusFailed, TaskStatusTimedOut, TaskStatusCancelled:
				failed++
			}
		}
	}

	workflow.CompletedIDs = workflow.CompletedIDs[:0]
	workflow.FailedIDs = workflow.FailedIDs[:0]
	for _, taskID := range workflow.TaskIDs {
		if task, ok := o.state.Tasks[taskID]; ok {
			if task.Status == TaskStatusCompleted {
				workflow.CompletedIDs = append(workflow.CompletedIDs, taskID)
			} else if task.Status.IsTerminal() {
				workflow.FailedIDs = append(workflow.FailedIDs, taskID)
			}
		}
	}

	total := len(workflow.TaskIDs)
	if total > 0 {
		workflow.Progress = float64(completed+failed) / float64(total)
	}
}

// submitTaskEvent submits a task event to Archivalist for terminal states
func (o *Orchestrator) submitTaskEvent(task *TaskRecord) {
	if !o.config.ArchivalistEnabled {
		return
	}
	if task.EventSubmitted {
		return
	}
	if !task.Status.IsTerminal() {
		return
	}

	event := &TaskEvent{
		ID:          generateMessageID(),
		Type:        taskEventType(task.Status),
		Timestamp:   time.Now(),
		TaskID:      task.ID,
		TaskName:    task.Name,
		WorkflowID:  task.WorkflowID,
		Status:      task.Status,
		AgentID:     task.AssignedAgentID,
		Result:      task.Result,
		Error:       task.Error,
		CompletedAt: time.Now(),
		SessionID:   o.config.SessionID,
		Metadata:    task.Metadata,
	}

	if task.StartedAt != nil {
		event.StartedAt = task.StartedAt
		event.Duration = time.Since(*task.StartedAt)
	}

	o.SubmitEventToArchivalist(context.Background(), event)

	o.mu.Lock()
	task.EventSubmitted = true
	now := time.Now()
	task.SubmittedAt = &now
	o.state.Stats.EventsSubmitted++
	o.mu.Unlock()
}

func taskEventType(status TaskStatus) string {
	switch status {
	case TaskStatusCompleted:
		return "task_completed"
	case TaskStatusFailed:
		return "task_failed"
	case TaskStatusTimedOut:
		return "task_timed_out"
	case TaskStatusCancelled:
		return "task_cancelled"
	default:
		return "task_terminal"
	}
}

// SubmitEventToArchivalist sends a task event to Archivalist
func (o *Orchestrator) SubmitEventToArchivalist(ctx context.Context, event *TaskEvent) error {
	if o.bus == nil || !o.running {
		return fmt.Errorf("orchestrator not running")
	}

	req := &guide.RouteRequest{
		Input:           fmt.Sprintf("store task event: %s", event.Type),
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		TargetAgentID:   "archivalist",
		FireAndForget:   true,
		SessionID:       o.config.SessionID,
		Timestamp:       time.Now(),
	}

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = map[string]any{
		"event_type": event.Type,
		"event_data": event,
	}

	return o.bus.Publish(guide.TopicGuideRequests, msg)
}

// QueryArchivalistForFailures queries Archivalist for failure patterns
func (o *Orchestrator) QueryArchivalistForFailures(ctx context.Context, query FailureQuery) ([]FailurePattern, error) {
	// In a full implementation, this would make an async request to Archivalist
	// and await the response. For now, return empty slice.
	return []FailurePattern{}, nil
}

// PushStatusUpdate adds a status update to the buffer
func (o *Orchestrator) PushStatusUpdate(update *StatusUpdate) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.state.UpdateBuffer.Add(update) {
		go o.flushUpdateBuffer()
	}
}

func (o *Orchestrator) flushUpdateBuffer() {
	o.mu.Lock()
	updates := o.state.UpdateBuffer.Flush()
	o.mu.Unlock()

	if len(updates) == 0 {
		return
	}

	for _, update := range updates {
		o.processStatusUpdate(update)
	}
}

func (o *Orchestrator) processStatusUpdate(update *StatusUpdate) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if task, ok := o.state.Tasks[update.TaskID]; ok {
		task.Status = update.Status
		if update.Status.IsTerminal() {
			now := time.Now()
			task.CompletedAt = &now
			go o.submitTaskEvent(task)
		}
	}
}

// GetSummary generates an orchestrator summary
func (o *Orchestrator) GetSummary(ctx context.Context) (*OrchestratorSummary, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	summary := &OrchestratorSummary{
		ID:          generateMessageID(),
		GeneratedAt: time.Now(),
		SessionID:   o.config.SessionID,
		Overview:    o.generateOverview(),
		Workflows:   o.summarizeWorkflows(),
		Tasks:       o.summarizeTasks(),
		Health:      o.healthMonitor.GetSummary(),
	}

	summary.TokenEstimate = estimateTokens(summary.Overview)
	o.state.Stats.SummariesCreated++

	return summary, nil
}

func (o *Orchestrator) generateOverview() string {
	return fmt.Sprintf(
		"Orchestrator monitoring %d workflows with %d active tasks. "+
			"%d tasks completed, %d failed. %d events submitted to Archivalist.",
		o.state.Stats.ActiveWorkflows,
		len(o.state.Tasks),
		o.state.Stats.CompletedTasks,
		o.state.Stats.FailedTasks,
		o.state.Stats.EventsSubmitted,
	)
}

func (o *Orchestrator) summarizeWorkflows() WorkflowsSummary {
	summary := WorkflowsSummary{}

	for _, wf := range o.state.Workflows {
		summary.Total++
		switch wf.Status {
		case WorkflowStatusRunning:
			summary.Running++
			summary.ActiveWorkflows = append(summary.ActiveWorkflows, WorkflowBrief{
				ID:       wf.ID,
				Name:     wf.Name,
				Status:   wf.Status,
				Progress: wf.Progress,
				Phase:    wf.Phase,
			})
		case WorkflowStatusCompleted:
			summary.Completed++
		case WorkflowStatusFailed:
			summary.Failed++
		case WorkflowStatusPaused:
			summary.Paused++
		}
	}

	return summary
}

func (o *Orchestrator) summarizeTasks() TasksSummary {
	summary := TasksSummary{}

	for _, task := range o.state.Tasks {
		summary.Total++
		switch task.Status {
		case TaskStatusPending, TaskStatusQueued:
			summary.Pending++
		case TaskStatusRunning:
			summary.Running++
		case TaskStatusCompleted:
			summary.Completed++
		case TaskStatusFailed:
			summary.Failed++
			summary.RecentFailures = append(summary.RecentFailures, TaskBrief{
				ID:      task.ID,
				Name:    task.Name,
				Status:  task.Status,
				AgentID: task.AssignedAgentID,
				Error:   task.Error,
			})
		case TaskStatusTimedOut:
			summary.TimedOut++
		}
	}

	if len(summary.RecentFailures) > 5 {
		summary.RecentFailures = summary.RecentFailures[:5]
	}

	return summary
}

// GetRoutingInfo returns routing info for Guide registration
func (o *Orchestrator) GetRoutingInfo() *guide.AgentRoutingInfo {
	return &guide.AgentRoutingInfo{
		ID:      o.config.AgentID,
		Name:    "orchestrator",
		Aliases: []string{"orch"},
		Registration: &guide.AgentRegistration{
			ID:          o.config.AgentID,
			Name:        "orchestrator",
			Aliases:     []string{"orch"},
			Description: "Workflow observer and coordinator. Monitors task health, submits events to Archivalist.",
			Priority:    80,
			Capabilities: guide.AgentCapabilities{
				Intents: []guide.Intent{guide.IntentStatus, guide.IntentRecall},
				Domains: []guide.Domain{guide.DomainTasks, "workflow", "health"},
			},
		},
		ActionShortcuts: []guide.ActionShortcut{
			{Name: "status", DefaultIntent: guide.IntentStatus, DefaultDomain: "workflow"},
			{Name: "tasks", DefaultIntent: guide.IntentStatus, DefaultDomain: guide.DomainTasks},
		},
	}
}

// Skills returns the skill registry
func (o *Orchestrator) Skills() *skills.Registry {
	return o.skills
}

// IsRunning returns true if the orchestrator is running
func (o *Orchestrator) IsRunning() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.running
}

// State returns the current state (read-only)
func (o *Orchestrator) State() *State {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.state
}

func generateMessageID() string {
	return fmt.Sprintf("msg_%s", uuid.New().String()[:8])
}

func estimateTokens(s string) int {
	return len(s) / 4
}
