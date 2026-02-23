package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/dag"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/storage/sylkdir"
	"github.com/google/uuid"
)

// Orchestrator is a read-only workflow observer and coordinator.
// Identity: Gemini 3 Flash — observational nervous system
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
	healthCache   *HealthCache

	knownAgents map[string]*guide.AgentAnnouncement

	// LLM integration
	provider  *providers.GoogleProvider // Gemini Flash provider (nil = fallback mode)
	eventCh   chan *busEvent            // buffered channel for LLM event loop
	bootGate  *bootstrapGate           // signal-based readiness gate for LLM loop
	llmCtx    context.Context
	llmCancel context.CancelFunc
	llmWg     sync.WaitGroup // tracks the LLM loop goroutine

	// Activity event publishing for UI agent panel visibility
	activityBus *events.ActivityEventBus

	// Data plane: WAL, SQLite, BufferRegistry, DAG Bridge
	store          *Store
	journal        *OrchestratorJournal
	bufferRegistry *BufferRegistry
	dagBridge      *DAGBridge
	scope          *concurrency.GoroutineScope

	// Pipeline subscriptions
	pipelineSubs []guide.Subscription
	dagSubs      []guide.Subscription

	// Handoff bridge for context-aware agent lifecycle management
	handoffBridge *handoff.HandoffBridge

	// Task router for DAG→container dispatch
	taskRouter *TaskRouter

	mu sync.RWMutex
}

// New creates a new Orchestrator agent. The optional GoogleProvider enables
// LLM-driven event analysis. When nil, the orchestrator runs in deterministic
// fallback mode (critical events auto-escalate without model involvement).
// The optional ActivityEventBus enables UI agent panel visibility.
// The optional SylkDir enables persistent storage (WAL, SQLite, BufferRegistry).
func New(cfg Config, provider *providers.GoogleProvider, activityBus *events.ActivityEventBus, sd *sylkdir.SylkDir) (*Orchestrator, error) {
	cfg = applyConfigDefaults(cfg)

	skillsRegistry := skills.NewRegistry()
	skillsLoaderCfg := skills.DefaultLoaderConfig()
	skillsLoaderCfg.CoreSkills = orchestratorCoreSkillNames()
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
		activityBus: activityBus,
	}

	if provider != nil && cfg.EnableLLM {
		o.provider = provider
		o.eventCh = make(chan *busEvent, llmEventBufferSize)
		o.bootGate = newBootstrapGate(cfg.BootstrapSafetyDeadline)
	}

	o.healthMonitor = NewHealthMonitor(o, cfg.HealthConfig)

	healthCache, err := NewHealthCache(DefaultHealthCacheConfig(cfg.HealthConfig))
	if err != nil {
		return nil, fmt.Errorf("create health cache: %w", err)
	}
	o.healthCache = healthCache
	o.healthMonitor.SetResultCallback(o.onHealthCheckResult)

	// Initialize data plane if SylkDir is available
	if sd != nil {
		if err := o.initDataPlane(cfg, sd, activityBus); err != nil {
			return nil, err
		}
	}

	o.registerCoreSkills()

	return o, nil
}

// initDataPlane initializes the persistent data plane: SQLite, WAL, BufferRegistry, DAG Bridge.
func (o *Orchestrator) initDataPlane(cfg Config, sd *sylkdir.SylkDir, activityBus *events.ActivityEventBus) error {
	// SQLite store
	store, err := OpenStore(DefaultStoreConfig(sd.OrchestratorDBPath()))
	if err != nil {
		return fmt.Errorf("orchestrator: open store: %w", err)
	}
	if err := store.Migrate(); err != nil {
		store.Close()
		return fmt.Errorf("orchestrator: migrate store: %w", err)
	}
	o.store = store

	// WAL journal
	journal, err := OpenOrchestratorJournal(sd.OrchestratorWALPath())
	if err != nil {
		store.Close()
		return fmt.Errorf("orchestrator: open journal: %w", err)
	}
	o.journal = journal

	// GoroutineScope
	// Budget derived from: maxDAGs * maxConcurrencyPerDAG
	pressure := &atomic.Int32{}
	budget := concurrency.NewGoroutineBudget(pressure)
	budget.RegisterAgent("orchestrator", "orchestrator")
	scope := concurrency.NewGoroutineScope(context.Background(), "orchestrator", budget)
	o.scope = scope

	// BufferRegistry
	buffers, err := NewBufferRegistry(
		DefaultBufferRegistryConfig(cfg.DAGConfig.MaxConcurrencyPerDAG),
		store, scope,
	)
	if err != nil {
		journal.Close()
		store.Close()
		return fmt.Errorf("orchestrator: create buffer registry: %w", err)
	}
	o.bufferRegistry = buffers

	// DAG Bridge
	o.dagBridge = NewDAGBridge(cfg.DAGConfig, DAGBridgeDeps{
		Store:       store,
		Journal:     journal,
		Buffers:     buffers,
		Scope:       scope,
		ActivityBus: activityBus,
		SessionID:   cfg.SessionID,
		AgentID:     cfg.AgentID,
	})

	return nil
}

// SetProvider hot-swaps the Google provider (used for auth refresh).
func (o *Orchestrator) SetProvider(provider *providers.GoogleProvider) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.provider = provider
}

// SetTaskRouter attaches the task router for DAG→container dispatch.
// Called after the activation controller and container registry are ready.
func (o *Orchestrator) SetTaskRouter(router *TaskRouter) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.taskRouter = router
}

// SignalReady marks bootstrap as complete, unblocking the LLM event loop.
// No-op when the LLM is disabled (bootGate is nil). Idempotent.
func (o *Orchestrator) SignalReady() {
	if o.bootGate == nil {
		return
	}
	o.bootGate.SignalReady()
}

func applyConfigDefaults(cfg Config) Config {
	if cfg.Model == "" {
		cfg.Model = "gemini-3-flash-preview"
	}
	if cfg.MaxOutputTokens == 0 {
		cfg.MaxOutputTokens = 2048
	}
	if cfg.MaxToolRuns == 0 {
		cfg.MaxToolRuns = 8
	}
	if cfg.LLMTimeout == 0 {
		cfg.LLMTimeout = 90 * time.Second
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
	if cfg.DAGConfig.MaxConcurrentDAGs == 0 {
		cfg.DAGConfig = DefaultDAGBridgeConfig()
	}
	if cfg.BootstrapSafetyDeadline == 0 {
		cfg.BootstrapSafetyDeadline = defaultBootstrapSafetyDur
	}
	return cfg
}

// Start begins listening for messages on the event bus
func (o *Orchestrator) Start(bus guide.EventBus) error {
	o.mu.Lock()
	if o.running {
		o.mu.Unlock()
		return fmt.Errorf("orchestrator is already running")
	}

	o.bus = bus
	o.channels = guide.NewAgentChannels(o.config.AgentID, o.config.AgentID)

	var err error
	o.requestSub, err = bus.SubscribeAsync(o.channels.Requests, o.handleBusRequest)
	if err != nil {
		o.mu.Unlock()
		return fmt.Errorf("failed to subscribe to %s: %w", o.channels.Requests, err)
	}

	o.responseSub, err = bus.SubscribeAsync(o.channels.Responses, o.handleBusResponse)
	if err != nil {
		o.requestSub.Unsubscribe()
		o.mu.Unlock()
		return fmt.Errorf("failed to subscribe to %s: %w", o.channels.Responses, err)
	}

	o.registrySub, err = bus.SubscribeAsync(guide.TopicAgentRegistry, o.handleRegistryAnnouncement)
	if err != nil {
		o.requestSub.Unsubscribe()
		o.responseSub.Unsubscribe()
		o.mu.Unlock()
		return fmt.Errorf("failed to subscribe to %s: %w", guide.TopicAgentRegistry, err)
	}

	o.subscribeToTaskEvents()
	o.healthMonitor.Start(context.Background())

	if o.provider != nil && o.eventCh != nil {
		o.llmCtx, o.llmCancel = context.WithCancel(context.Background())
		o.llmWg.Add(1)
		go func() {
			defer o.llmWg.Done()
			o.runLLMLoop(o.llmCtx)
		}()
	}

	// Wire data plane if available
	if o.dagBridge != nil {
		o.dagBridge.SetBus(bus)
		o.subscribePipelineTopics()
		o.subscribeDAGTopics()
		o.dagBridge.RecoverFromWAL(context.Background())
		o.bufferRegistry.StartGC(context.Background())
		o.startWALGC()
	}

	o.running = true
	hasProvider := o.provider != nil
	o.mu.Unlock()

	llmStatus := "deterministic fallback"
	if hasProvider {
		llmStatus = "Gemini Flash active"
	}
	o.publishActivity(events.EventTypeSuccess, "Orchestrator started ("+llmStatus+")")

	return nil
}

func (o *Orchestrator) subscribeToTaskEvents() {
	o.bus.SubscribeAsync("tasks.dispatch", o.handleTaskDispatch)
	o.bus.SubscribeAsync("tasks.complete", o.handleTaskComplete)
	o.bus.SubscribeAsync("tasks.failed", o.handleTaskFailed)
	o.bus.SubscribeAsync("workflows.status", o.handleWorkflowStatus)
}

// Stop unsubscribes from event bus topics and shuts down the LLM loop.
//
// Shutdown order matters to avoid deadlocks:
//  1. Mark not running (under lock) so handlers become no-ops
//  2. Release lock before blocking operations
//  3. Cancel LLM context and wait for goroutine exit (needs RLock internally)
//  4. Stop health monitor, flush buffers, unsubscribe
func (o *Orchestrator) Stop() error {
	o.mu.Lock()
	if !o.running {
		o.mu.Unlock()
		return nil
	}
	o.running = false
	llmCancel := o.llmCancel
	o.mu.Unlock()

	// Cancel the LLM loop context and wait for the goroutine to finish.
	// This must happen outside o.mu because the LLM loop acquires RLock
	// for state snapshots.
	if llmCancel != nil {
		llmCancel()
	}
	o.llmWg.Wait()

	// Drain any remaining events after the LLM loop has exited.
	if o.eventCh != nil {
		for len(o.eventCh) > 0 {
			<-o.eventCh
		}
	}

	o.healthMonitor.Stop()
	o.healthCache.Close()

	// Cancel all active DAGs before flushing buffers
	if o.dagBridge != nil {
		o.dagBridge.CancelAll("orchestrator shutting down")
	}

	// flushUpdateBuffer acquires o.mu internally — safe now that we
	// released it above.
	o.flushUpdateBuffer()

	// Close data plane: flush buffers → close journal → close store → shutdown scope
	if o.bufferRegistry != nil {
		o.bufferRegistry.Close()
	}
	if o.dagBridge != nil {
		o.dagBridge.Close()
	}
	if o.journal != nil {
		o.journal.Close()
	}
	if o.store != nil {
		o.store.Close()
	}
	if o.scope != nil {
		o.scope.Shutdown(5*time.Second, 10*time.Second)
	}

	o.mu.Lock()
	errs := o.unsubscribeAll()
	o.mu.Unlock()

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
	// Detect structured plan handoff payloads from the architect.
	if result, ok := o.tryIngestPlanFromInput(ctx, req.Input); ok {
		return result, nil
	}

	switch req.Intent {
	case guide.IntentStatus:
		return o.handleStatusQuery(ctx, req)
	case guide.IntentRecall:
		return o.handleRecallQuery(ctx, req)
	case guide.IntentHelp, guide.IntentChat, guide.IntentUnknown:
		return o.handleConversation(ctx, req)
	default:
		return o.handleConversation(ctx, req)
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

func (o *Orchestrator) handleHelpQuery(_ context.Context, _ *guide.ForwardedRequest) (any, error) {
	return map[string]any{
		"agent":              "orchestrator",
		"description":        "Workflow observer and coordinator for task and workflow state.",
		"supported_intents":  []guide.Intent{guide.IntentStatus, guide.IntentRecall, guide.IntentHelp, guide.IntentChat},
		"supported_domains":  []guide.Domain{guide.DomainTasks, "workflow", "health"},
		"recommended_routes": []string{"@orchestrator:status:tasks", "@orchestrator:status:workflow", "@orchestrator:recall:failures", "@orchestrator:chat"},
	}, nil
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

	// Always set up stream context — the Guide may promote IntentChat to
	// IntentHelp, so we cannot predicate streaming on the incoming intent.
	// This mirrors the architect's handleForwardBusRequest pattern.
	ctx = withOrchestratorStreamContext(ctx, fwd.CorrelationID, fwd.SourceAgentID)
	ctx, usageAcc := withOrchestratorUsageAccumulator(ctx)
	if !fwd.FireAndForget {
		o.publishStreamStart(ctx)
	}

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
		o.publishStreamError(ctx, err)
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

	// Conversation text is already streamed via chunks — send complete with
	// empty text so the bridge doesn't duplicate content.
	completeText := extractOrchestratorUserResponse(result)
	if isStreamedOrchestratorConversation(result) {
		completeText = ""
	}
	o.publishStreamComplete(ctx, completeText, usageAcc.Total())

	respMsg := guide.NewResponseMessage(generateMessageID(), resp)
	return o.bus.Publish(o.channels.Responses, respMsg)
}

func (o *Orchestrator) handleBusResponse(msg *guide.Message) error {
	o.mu.RLock()
	router := o.taskRouter
	o.mu.RUnlock()

	if router != nil {
		router.DeliverResponse(msg)
	}
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
		o.pushEvent(&busEvent{
			Topic:    guide.TopicAgentRegistry,
			Timestamp: time.Now(),
			Severity: severityInfo,
			Summary:  fmt.Sprintf("Agent %q registered", ann.AgentID),
			Data:     map[string]any{"agent_id": ann.AgentID},
		})
	case guide.MessageTypeAgentUnregistered:
		delete(o.knownAgents, ann.AgentID)
		o.healthMonitor.UnregisterAgent(ann.AgentID)
		o.pushEvent(&busEvent{
			Topic:    guide.TopicAgentRegistry,
			Timestamp: time.Now(),
			Severity: severityInfo,
			Summary:  fmt.Sprintf("Agent %q unregistered", ann.AgentID),
			Data:     map[string]any{"agent_id": ann.AgentID},
		})
	}

	return nil
}

// Task event handlers
func (o *Orchestrator) handleTaskDispatch(msg *guide.Message) error {
	o.mu.Lock()

	data, ok := msg.Payload.(map[string]any)
	if !ok {
		o.mu.Unlock()
		return nil
	}

	taskID, _ := data["task_id"].(string)
	workflowID, _ := data["workflow_id"].(string)
	name, _ := data["name"].(string)
	agentID, _ := data["agent_id"].(string)

	// DAG-specific fields from BusNodeDispatcher
	nodeID, _ := data["node_id"].(string)
	agentType, _ := data["agent_type"].(string)
	prompt, _ := data["prompt"].(string)
	nodeCtx, _ := data["context"].(map[string]any)
	parentResults, _ := data["parent_results"].(map[string]any)
	dagID, _ := data["dag_id"].(string)

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

	router := o.taskRouter
	o.mu.Unlock()

	o.pushEvent(&busEvent{
		Topic:    "tasks.dispatch",
		Timestamp: now,
		Severity: severityInfo,
		Summary:  fmt.Sprintf("Task %q dispatched to agent %s", name, agentID),
		Data:     map[string]any{"task_id": taskID, "agent_id": agentID, "workflow_id": workflowID},
	})

	// Route to containerized pipeline agent for DAG-originated dispatches.
	if router != nil && nodeID != "" {
		pipelineTask := &PipelineTask{
			NodeID:        nodeID,
			DAGID:         dagID,
			TaskID:        taskID,
			AgentType:     agentType,
			Prompt:        prompt,
			Context:       nodeCtx,
			ParentResults: parentResults,
			SessionID:     o.config.SessionID,
		}
		if routeErr := router.Route(pipelineTask); routeErr != nil {
			o.pushEvent(&busEvent{
				Topic:     "tasks.dispatch",
				Timestamp: time.Now(),
				Severity:  severityCritical,
				Summary:   fmt.Sprintf("Route failed for node %s: %s", nodeID, routeErr),
			})
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

	// Notify DAG bridge for DAG-originated task completions.
	if o.dagBridge != nil {
		if nodeID, hasNode := data["node_id"].(string); hasNode && nodeID != "" {
			o.dagBridge.NotifyNodeComplete(nodeID, convertTaskCompleteToNodeResult(task))
		}
	}

	go o.submitTaskEvent(task)

	o.pushEvent(&busEvent{
		Topic:    "tasks.complete",
		Timestamp: now,
		Severity: severityInfo,
		Summary:  fmt.Sprintf("Task %q completed on agent %s", task.Name, task.AssignedAgentID),
		Data:     map[string]any{"task_id": taskID, "agent_id": task.AssignedAgentID},
	})

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

	o.pushEvent(&busEvent{
		Topic:    "tasks.failed",
		Timestamp: now,
		Severity: severityCritical,
		Summary:  fmt.Sprintf("Task %q failed on agent %s: %s", task.Name, task.AssignedAgentID, errorMsg),
		Data:     map[string]any{"task_id": taskID, "agent_id": task.AssignedAgentID, "error": errorMsg},
	})

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

	sev := severityInfo
	if workflow.Status.IsTerminal() {
		now := time.Now()
		workflow.CompletedAt = &now
		o.state.Stats.ActiveWorkflows--
		sev = severityWarning
	}

	o.pushEvent(&busEvent{
		Topic:    "workflows.status",
		Timestamp: time.Now(),
		Severity: sev,
		Summary:  fmt.Sprintf("Workflow %q status: %s (phase: %s)", workflowID, statusStr, phase),
		Data:     map[string]any{"workflow_id": workflowID, "status": statusStr, "phase": phase},
	})

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
				Intents: []guide.Intent{guide.IntentStatus, guide.IntentRecall, guide.IntentHelp, guide.IntentChat},
				Domains: []guide.Domain{guide.DomainTasks, "workflow", "health"},
			},
		},
		ActionShortcuts: []guide.ActionShortcut{
			{Name: "status", DefaultIntent: guide.IntentStatus, DefaultDomain: "workflow"},
			{Name: "tasks", DefaultIntent: guide.IntentStatus, DefaultDomain: guide.DomainTasks},
			{Name: "help", DefaultIntent: guide.IntentHelp, DefaultDomain: guide.DomainSystem},
			{Name: "chat", DefaultIntent: guide.IntentChat, DefaultDomain: guide.DomainSystem},
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

// publishActivity sends an activity event to the UI agent panel.
func (o *Orchestrator) publishActivity(eventType events.EventType, content string) {
	if o.activityBus == nil {
		return
	}
	evt := events.NewActivityEvent(eventType, o.config.SessionID, content)
	evt.AgentID = o.config.AgentID
	evt.Data["agent_type"] = "orchestrator"
	evt.Data["agent_name"] = "Orchestrator"
	o.activityBus.Publish(evt)
}

// retryObserver returns a provider RetryObserver that publishes retry status
// via the activity event bus, giving the UI visibility into backoff waits.
func (o *Orchestrator) retryObserver() providers.RetryObserver {
	return func(event providers.RetryEvent) {
		o.publishActivity(events.EventTypeAgentError,
			fmt.Sprintf("Rate limited, retrying (%d/%d) after %s",
				event.Attempt, event.MaxAttempts, event.Delay.Truncate(time.Second)))
	}
}

// onHealthCheckResult is the HealthMonitor callback. It fires outside m.mu
// and does NOT acquire o.mu, preventing deadlocks. It only touches:
// - healthCache (Ristretto, internally lock-free)
// - bus.Publish (has its own internal locking)
// - doEscalateToArchitect (publishes to bus, no o.mu)
func (o *Orchestrator) onHealthCheckResult(result *HealthCheckResult) {
	// 1. Cache in Ristretto for fast skill retrieval.
	o.healthCache.SetLatest(result)
	for i := range result.AgentResults {
		ar := &result.AgentResults[i]
		o.healthCache.SetAgent(ar.AgentID, ar)
	}

	// 2. Forward to Archivalist for history.
	o.forwardHealthToArchivalist(result)

	// 3. Auto-escalate critical transitions.
	o.escalateCriticalHealth(result)
}

// forwardHealthToArchivalist sends the health check result to Archivalist for
// historical storage. Fire-and-forget, same pattern as SubmitEventToArchivalist.
func (o *Orchestrator) forwardHealthToArchivalist(result *HealthCheckResult) {
	if o.bus == nil || !o.config.ArchivalistEnabled {
		return
	}

	req := &guide.RouteRequest{
		Input:           "store health check result",
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		TargetAgentID:   "archivalist",
		FireAndForget:   true,
		SessionID:       o.config.SessionID,
		Timestamp:       time.Now(),
	}

	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = map[string]any{
		"event_type": "health_check_result",
		"event_data": result,
	}

	o.bus.Publish(guide.TopicGuideRequests, msg)
}

// escalateCriticalHealth deterministically escalates agents that transitioned
// to critical level. Only fires on the transition (PreviousLevel != Critical)
// to prevent re-escalation every check cycle.
func (o *Orchestrator) escalateCriticalHealth(result *HealthCheckResult) {
	for i := range result.AgentResults {
		ar := &result.AgentResults[i]
		if ar.Level != HealthLevelCritical {
			continue
		}
		if ar.PreviousLevel == HealthLevelCritical {
			continue
		}
		o.doEscalateToArchitect(
			fmt.Sprintf("Agent %q transitioned to critical health", ar.AgentID),
			"critical",
			fmt.Sprintf("Error rate: %.2f, missed heartbeats: %d, active alerts: %d",
				ar.ErrorRate, ar.MissedHeartbeats, ar.ActiveAlertCount),
		)
	}
}

func generateMessageID() string {
	return fmt.Sprintf("msg_%s", uuid.New().String()[:8])
}

func estimateTokens(s string) int {
	return len(s) / 4
}

// --- Pipeline and DAG topic subscriptions ---

func (o *Orchestrator) subscribePipelineTopics() {
	topics := []struct {
		topic   string
		handler guide.MessageHandler
	}{
		{"pipeline.update.*", o.handlePipelineUpdate},
		{"pipeline.state.*", o.handlePipelineState},
		{"pipeline.query.response.orchestrator", o.handlePipelineQueryResponse},
	}
	for _, t := range topics {
		if sub, err := o.bus.SubscribeAsync(t.topic, t.handler); err == nil {
			o.pipelineSubs = append(o.pipelineSubs, sub)
		}
	}
}

func (o *Orchestrator) subscribeDAGTopics() {
	topics := []struct {
		topic   string
		handler guide.MessageHandler
	}{
		{"dag.execute", o.handleDAGExecuteRequest},
		{"dag.modify", o.handleDAGModifyRequest},
		{"dag.cancel", o.handleDAGCancelRequest},
	}
	for _, t := range topics {
		if sub, err := o.bus.SubscribeAsync(t.topic, t.handler); err == nil {
			o.dagSubs = append(o.dagSubs, sub)
		}
	}
}

func (o *Orchestrator) handlePipelineUpdate(msg *guide.Message) error {
	update, ok := msg.Payload.(*PipelineUpdate)
	if !ok {
		// Try map extraction for deserialized payloads
		data, ok := msg.Payload.(map[string]any)
		if !ok {
			return nil
		}
		update = extractPipelineUpdate(data)
		if update == nil {
			return nil
		}
	}

	entry := TaskUpdateEntry{
		ID:        msg.ID,
		DAGID:     update.DAGID,
		TaskID:    update.TaskID,
		NodeID:    update.NodeID,
		AgentID:   update.AgentID,
		AgentType: update.AgentType,
		Status:    update.Status,
		Progress:  update.Progress,
		Message:   update.Message,
		Output:    update.Output,
		Error:     update.Error,
		Attempt:   update.Attempt,
		Timestamp: update.Timestamp,
	}
	o.bufferRegistry.Push(entry)

	if isTerminalStatus(update.Status) {
		result := convertPipelineToNodeResult(update)
		o.dagBridge.NotifyNodeComplete(update.NodeID, result)
	}

	o.pushEvent(&busEvent{
		Topic:     "pipeline.update",
		Timestamp: update.Timestamp,
		Severity:  severityInfo,
		Summary:   fmt.Sprintf("Pipeline %s: %s (%s %.0f%%)", update.AgentType, update.Status, update.NodeID, update.Progress*100),
	})

	return nil
}

func (o *Orchestrator) handlePipelineState(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	agentID, _ := data["agent_id"].(string)
	dagID, _ := data["dag_id"].(string)
	nodeID, _ := data["node_id"].(string)
	stateJSON, _ := data["state_json"].(string)

	if o.store != nil && agentID != "" {
		o.store.UpsertPipelineState(agentID, dagID, nodeID, stateJSON)
	}
	return nil
}

func (o *Orchestrator) handlePipelineQueryResponse(msg *guide.Message) error {
	resp, ok := msg.Payload.(*PipelineQueryResponse)
	if !ok {
		return nil
	}

	if o.store != nil {
		stateJSON, _ := json.Marshal(resp.State)
		o.store.UpsertPipelineState(resp.AgentID, "", "", string(stateJSON))
	}
	return nil
}

func (o *Orchestrator) handleDAGExecuteRequest(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	dagJSON, _ := data["dag_json"].(string)
	planID, _ := data["plan_id"].(string)

	if dagJSON == "" || planID == "" {
		return nil
	}

	d := &dag.DAG{}
	if err := d.UnmarshalJSON([]byte(dagJSON)); err != nil {
		o.publishActivity(events.EventTypeAgentError, "DAG execute: invalid dag_json: "+err.Error())
		return nil
	}

	dagID, err := o.dagBridge.Execute(context.Background(), d, planID, o.config.SessionID)
	if err != nil {
		o.publishActivity(events.EventTypeAgentError, "DAG execution failed: "+err.Error())
		return nil
	}
	o.publishActivity(events.EventTypeSuccess, "DAG "+dagID+" started for plan "+planID)
	return nil
}

func (o *Orchestrator) handleDAGModifyRequest(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	dagID, _ := data["dag_id"].(string)
	modJSON, _ := data["modification_json"].(string)
	reason, _ := data["reason"].(string)

	var mod DAGModification
	if err := json.Unmarshal([]byte(modJSON), &mod); err != nil {
		return nil
	}
	mod.Reason = reason

	return o.dagBridge.Modify(dagID, &mod)
}

func (o *Orchestrator) handleDAGCancelRequest(msg *guide.Message) error {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil
	}

	dagID, _ := data["dag_id"].(string)
	reason, _ := data["reason"].(string)

	return o.dagBridge.Cancel(dagID, reason)
}

func (o *Orchestrator) startWALGC() {
	if o.scope == nil || o.journal == nil {
		return
	}

	// 7-day retention, 24h GC interval
	const walRetention = 7 * 24 * time.Hour
	const walGCInterval = 24 * time.Hour

	o.scope.Go("wal-gc", 0, func(ctx context.Context) error {
		// Run once at startup
		o.journal.GC(time.Now().Add(-walRetention))

		ticker := time.NewTicker(walGCInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				o.journal.GC(time.Now().Add(-walRetention))
			}
		}
	})
}

func extractPipelineUpdate(data map[string]any) *PipelineUpdate {
	u := &PipelineUpdate{}
	u.DAGID, _ = data["dag_id"].(string)
	u.NodeID, _ = data["node_id"].(string)
	u.TaskID, _ = data["task_id"].(string)
	u.AgentID, _ = data["agent_id"].(string)
	u.AgentType, _ = data["agent_type"].(string)
	u.Status, _ = data["status"].(string)
	if p, ok := data["progress"].(float64); ok {
		u.Progress = p
	}
	u.Message, _ = data["message"].(string)
	u.Output = data["output"]
	u.Error, _ = data["error"].(string)
	if a, ok := data["attempt"].(float64); ok {
		u.Attempt = int(a)
	}
	u.Timestamp = time.Now()
	if u.Status == "" {
		return nil
	}
	return u
}

func convertTaskCompleteToNodeResult(task *TaskRecord) *dag.NodeResult {
	state := dag.NodeStateSucceeded
	if task.Status == TaskStatusFailed {
		state = dag.NodeStateFailed
	}
	return &dag.NodeResult{
		NodeID:  task.ID,
		State:   state,
		Output:  task.Result,
		EndTime: time.Now(),
	}
}

func convertPipelineToNodeResult(update *PipelineUpdate) *dag.NodeResult {
	state := dag.NodeStateSucceeded
	var resultErr error
	if update.Status == "failed" || update.Status == "timed_out" {
		state = dag.NodeStateFailed
		resultErr = fmt.Errorf("%s", update.Error)
	} else if update.Status == "cancelled" {
		state = dag.NodeStateCancelled
	}

	return &dag.NodeResult{
		NodeID:  update.NodeID,
		State:   state,
		Output:  update.Output,
		Error:   resultErr,
		EndTime: update.Timestamp,
	}
}

// =============================================================================
// HandoffInjectable Implementation
// =============================================================================

// SetHandoffBridge attaches the handoff bridge for context-aware lifecycle management.
func (o *Orchestrator) SetHandoffBridge(bridge *handoff.HandoffBridge) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.handoffBridge = bridge
}

// AgentID returns the orchestrator's agent identifier.
func (o *Orchestrator) AgentID() string {
	return o.config.AgentID
}

// AgentType returns the orchestrator's agent type classification.
func (o *Orchestrator) AgentType() string {
	return "orchestrator"
}

// Descriptor returns the immutable agent descriptor for handoff decisions.
func (o *Orchestrator) Descriptor() handoff.AgentDescriptor {
	return handoff.AgentDescriptor{
		AgentType:     "orchestrator",
		ModelID:       "haiku-4.5-200k",
		ContextWindow: 200_000,
		Category:      handoff.CategoryStandalone,
	}
}

// ExtractArchivableState captures the orchestrator's current state for handoff persistence.
func (o *Orchestrator) ExtractArchivableState() *handoff.ArchivableState {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return &handoff.ArchivableState{
		AgentID:   o.config.AgentID,
		AgentType: "orchestrator",
		State: map[string]string{
			"session_id":       o.config.SessionID,
			"running":          fmt.Sprintf("%t", o.running),
			"active_workflows": fmt.Sprintf("%d", o.state.Stats.ActiveWorkflows),
			"completed_tasks":  fmt.Sprintf("%d", o.state.Stats.CompletedTasks),
			"failed_tasks":     fmt.Sprintf("%d", o.state.Stats.FailedTasks),
		},
		Timestamp: time.Now(),
	}
}

// Terminate gracefully shuts down the orchestrator, delegating to Stop.
func (o *Orchestrator) Terminate(_ context.Context) error {
	return o.Stop()
}

// InjectPreparedContext receives prepared context from a handoff.
// The orchestrator is a lightweight observer and does not require context
// injection beyond what Start() provides, so this is a no-op.
func (o *Orchestrator) InjectPreparedContext(_ *handoff.PreparedContext) error {
	return nil
}
