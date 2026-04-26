package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/events"
	coordination "github.com/adalundhe/sylk/core/pipeline/coordination"
)

type taskDispatchContext struct {
	data map[string]any

	sessionID  string
	taskID     string
	taskSlug   string
	workflowID string
	name       string
	agentID    string
	targetID   string

	nodeID        string
	agentType     string
	prompt        string
	nodeCtx       map[string]any
	parentResults map[string]any
	dagID         string

	pipelineTaskID   string
	pipelineTaskSlug string
	pipelineStage    string
	pipelineParentID string
	coordinationTask string
	precedentTask    string
	coAgents         []string
	now              time.Time
}

func parseTaskDispatchMessage(msg *guide.Message) (*taskDispatchContext, bool) {
	data, ok := msg.Payload.(map[string]any)
	if !ok {
		return nil, false
	}

	dispatch := &taskDispatchContext{
		data:          data,
		sessionID:     firstNonEmpty(stringValue(data["session_id"]), stringMapValue(msg.Metadata, "session_id")),
		taskID:        stringValue(data["task_id"]),
		taskSlug:      stringValue(data["task_slug"]),
		workflowID:    stringValue(data["workflow_id"]),
		name:          stringValue(data["name"]),
		agentID:       stringValue(data["agent_id"]),
		targetID:      stringValue(data["target_agent_id"]),
		nodeID:        stringValue(data["node_id"]),
		agentType:     stringValue(data["agent_type"]),
		prompt:        stringValue(data["prompt"]),
		nodeCtx:       mapValue(data["context"]),
		parentResults: mapValue(data["parent_results"]),
		dagID:         stringValue(data["dag_id"]),
		coAgents:      decodeDispatchAgentTypes(data["co_agents"]),
		now:           time.Now(),
	}
	if dispatch.nodeID == "" || dispatch.agentType == "" || dispatch.dagID == "" {
		return nil, false
	}

	dispatch.ensureRoutingContext()
	dispatch.canonicalizePipelineIdentity()
	return dispatch, true
}

func (d *taskDispatchContext) ensureRoutingContext() {
	if d.nodeCtx == nil {
		d.nodeCtx = make(map[string]any)
	}
	if ackTopic := stringValue(d.data["ack_topic"]); ackTopic != "" {
		d.nodeCtx["ack_topic"] = ackTopic
	}
	if d.dagID != "" {
		d.nodeCtx["dag_id"] = d.dagID
	}
	if d.nodeID != "" {
		d.nodeCtx["node_id"] = d.nodeID
	}
}

func (d *taskDispatchContext) canonicalizePipelineIdentity() {
	pipelineTaskID, pipelineTaskSlug := canonicalPipelineTaskIdentity(d.taskID, d.taskSlug, d.nodeCtx, d.nodeID)
	if pipelineTaskID != "" {
		d.taskID = pipelineTaskID
		d.pipelineTaskID = pipelineTaskID
	}
	if pipelineTaskSlug != "" {
		d.taskSlug = pipelineTaskSlug
		d.pipelineTaskSlug = pipelineTaskSlug
	}
	d.pipelineStage = stringValue(d.nodeCtx["pipeline_stage"])
	d.pipelineParentID = stringValue(d.nodeCtx["pipeline_parent_id"])

	ctxTaskName := strings.TrimSpace(stringValue(d.nodeCtx["task_name"]))
	trimmedName := strings.TrimSpace(d.name)
	d.coordinationTask = firstNonEmpty(ctxTaskName, trimmedName, d.pipelineTaskSlug)
	d.precedentTask = firstNonEmpty(ctxTaskName, trimmedName)
}

func (d *taskDispatchContext) initialStatus() TaskStatus {
	if d.agentID != "" {
		return TaskStatusRunning
	}
	return TaskStatusQueued
}

func (d *taskDispatchContext) taskRecord(sessionID string) *TaskRecord {
	return &TaskRecord{
		ID:               d.taskID,
		WorkflowID:       d.workflowID,
		Name:             d.name,
		Status:           d.initialStatus(),
		AssignedAgentID:  d.agentID,
		AssignedAt:       &d.now,
		CreatedAt:        d.now,
		StartedAt:        &d.now,
		SessionID:        sessionID,
		PipelineStage:    d.pipelineStage,
		PipelineParentID: d.pipelineParentID,
	}
}

func (d *taskDispatchContext) effectiveSessionID(fallbacks ...string) string {
	candidates := make([]string, 0, len(fallbacks)+1)
	candidates = append(candidates, strings.TrimSpace(d.sessionID))
	for _, fallback := range fallbacks {
		candidates = append(candidates, strings.TrimSpace(fallback))
	}
	candidates = append(candidates, "default")
	return firstNonEmpty(candidates...)
}

func (d *taskDispatchContext) event() *busEvent {
	return &busEvent{
		Topic:     "tasks.dispatch",
		Timestamp: d.now,
		Severity:  severityInfo,
		Summary:   fmt.Sprintf("Task %q dispatched to agent %s", d.name, d.agentID),
		Data: map[string]any{
			"task_id":     d.taskID,
			"agent_id":    d.agentID,
			"workflow_id": d.workflowID,
		},
	}
}

func (d *taskDispatchContext) pipelineTask(sessionID string) *PipelineTask {
	contextData := d.nodeCtx
	if len(d.coAgents) > 0 {
		if contextData == nil {
			contextData = make(map[string]any)
		}
		contextData["co_agents"] = append([]string(nil), d.coAgents...)
	}
	return &PipelineTask{
		NodeID:        d.nodeID,
		DAGID:         d.dagID,
		TaskID:        d.pipelineTaskID,
		AgentType:     d.agentType,
		TargetAgentID: firstNonEmpty(d.targetID, pipelineWorkerTargetAgentID(d.pipelineTaskID, d.agentType)),
		Prompt:        d.prompt,
		Context:       contextData,
		ParentResults: d.parentResults,
		SessionID:     sessionID,
	}
}

func stringValue(value any) string {
	typed, _ := value.(string)
	return typed
}

func mapValue(value any) map[string]any {
	typed, _ := value.(map[string]any)
	return typed
}

func (o *Orchestrator) registerTaskDispatch(dispatch *taskDispatchContext) *TaskRouter {
	o.mu.Lock()
	defer o.mu.Unlock()

	sessionID := dispatch.effectiveSessionID(o.SessionID())
	if existing, ok := o.state.Tasks[dispatch.taskID]; ok && existing != nil {
		o.applyTaskDispatch(existing, dispatch, sessionID)
	} else {
		o.state.Tasks[dispatch.taskID] = dispatch.taskRecord(sessionID)
	}
	if o.healthMonitor != nil {
		o.healthMonitor.RecordTaskStart(dispatch.agentType, dispatch.taskID)
	}

	if dispatch.workflowID != "" {
		if workflow, ok := o.state.Workflows[dispatch.workflowID]; ok {
			workflow.TaskIDs = append(workflow.TaskIDs, dispatch.taskID)
		}
	}

	return o.taskRouter
}

func (o *Orchestrator) applyTaskDispatch(task *TaskRecord, dispatch *taskDispatchContext, sessionID string) {
	if task == nil || dispatch == nil {
		return
	}
	if strings.TrimSpace(task.ID) == "" {
		task.ID = dispatch.taskID
	}
	if strings.TrimSpace(task.WorkflowID) == "" {
		task.WorkflowID = dispatch.workflowID
	}
	if strings.TrimSpace(task.Name) == "" {
		task.Name = dispatch.name
	}
	task.Status = dispatch.initialStatus()
	task.AssignedAgentID = dispatch.agentID
	if dispatch.agentID != "" {
		task.AssignedAt = &dispatch.now
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = dispatch.now
	}
	task.StartedAt = &dispatch.now
	task.SessionID = firstNonEmpty(strings.TrimSpace(task.SessionID), sessionID)
	if dispatch.pipelineStage != "" {
		task.PipelineStage = dispatch.pipelineStage
	}
	if dispatch.pipelineParentID != "" {
		task.PipelineParentID = dispatch.pipelineParentID
	}
}

func (o *Orchestrator) publishTaskDispatchPipelineState(dispatch *taskDispatchContext) string {
	stage := strings.TrimSpace(dispatch.pipelineStage)
	if pipelineProtocolEligible(dispatch) {
		stage = string(StageInspect)
	}
	pipelineStatus := pipelinePhaseStatus(stage)
	if dispatch.pipelineTaskID != "" && pipelineStatus != "" {
		workerType := dispatch.agentType
		if pipelineProtocolEligible(dispatch) {
			workerType = agentshared.PipelineAgentInspector
		}
		publishTaskPipelineState(o.bus, o.config.AgentID, dispatch.pipelineTaskID, dispatch.pipelineTaskSlug, pipelineStatus, workerType)
	}
	return string(pipelineStatus)
}

func (o *Orchestrator) enrichTaskDispatchCoordination(dispatch *taskDispatchContext) {
	if o.coordination == nil || dispatch.pipelineTaskID == "" {
		return
	}

	packet, ok, err := o.coordination.GetCachedQueryView(coordination.QueryViewInput{
		TaskID:     dispatch.pipelineTaskID,
		TaskName:   dispatch.coordinationTask,
		WorkerType: strings.TrimSpace(dispatch.agentType),
	})
	if err != nil {
		o.logWarnMsg("build cached coordination packet", "task_id", dispatch.pipelineTaskID, "agent_type", dispatch.agentType, "error", err)
		return
	}
	if !ok || packet == nil {
		return
	}

	dispatch.nodeCtx["coordination_view"] = packet.View
	if packet.Packet != nil {
		o.addCachedTaskDispatchPrecedents(dispatch, packet.Packet)
		dispatch.nodeCtx["coordination_packet"] = packet.Packet
	}
}

func (o *Orchestrator) addCachedTaskDispatchPrecedents(dispatch *taskDispatchContext, packet *coordination.WorkerPacket) {
	if o == nil || dispatch == nil || packet == nil || o.coordination == nil || !o.config.ArchivalistEnabled {
		return
	}
	precedents, ok := o.coordination.GetCachedPrecedents(
		dispatch.precedentTask,
		dispatch.pipelineTaskSlug,
		strings.TrimSpace(dispatch.agentType),
	)
	if !ok {
		return
	}
	packet.HistoricalPrecedents = precedents
	packet.Summary = buildWorkerSummary(strings.TrimSpace(dispatch.agentType), packet)
}

func (o *Orchestrator) warmTaskDispatchCoordination(dispatch *taskDispatchContext) {
	if o == nil || dispatch == nil || o.coordination == nil || strings.TrimSpace(dispatch.pipelineTaskID) == "" {
		return
	}

	taskID := strings.TrimSpace(dispatch.pipelineTaskID)
	taskName := strings.TrimSpace(dispatch.coordinationTask)
	taskSlug := strings.TrimSpace(dispatch.pipelineTaskSlug)
	workerType := strings.TrimSpace(dispatch.agentType)

	warm := func(ctx context.Context) error {
		if _, err := o.coordination.QueryView(ctx, coordination.QueryViewInput{
			TaskID:     taskID,
			TaskName:   taskName,
			WorkerType: workerType,
		}); err != nil && ctx.Err() == nil {
			o.logWarnMsg("warm coordination packet", "task_id", taskID, "agent_type", workerType, "error", err)
		}
		if _, err := o.queryCoordinationPrecedents(ctx, taskName, taskSlug, workerType); err != nil && ctx.Err() == nil {
			o.logWarnMsg(
				"warm coordination precedents",
				"task_name", taskName,
				"task_slug", taskSlug,
				"agent_type", workerType,
				"error", err,
			)
		}
		return nil
	}

	if o.scope != nil {
		if err := o.scope.Go("warm-task-dispatch-coordination", coordinationPrecedentTimeout+time.Second, warm); err == nil {
			return
		}
	}

	parentCtx := context.Background()
	if o.runCtx != nil {
		parentCtx = o.runCtx
	}
	if o.scope != nil {
		_ = o.scope.Go("warm-coordination-precedents", coordinationPrecedentTimeout+time.Second, func(gctx context.Context) error {
			_ = warm(gctx)
			return nil
		})
	} else {
		go func() {
			ctx, cancel := context.WithTimeout(parentCtx, coordinationPrecedentTimeout+time.Second)
			defer cancel()
			_ = warm(ctx)
		}()
	}
}

func (o *Orchestrator) publishTaskDispatchAgents(dispatch *taskDispatchContext, pipelineStatus string) {
	if dispatch.nodeID == "" || dispatch.agentType == "" {
		return
	}
	if strings.TrimSpace(dispatch.pipelineTaskID) == "" {
		dispatched := make(map[string]struct{}, len(dispatch.coAgents)+1)
		publishGlobalDispatch := func(agentType string) {
			agentType = strings.TrimSpace(agentType)
			if agentType == "" {
				return
			}
			if _, seen := dispatched[agentType]; seen {
				return
			}
			dispatched[agentType] = struct{}{}
			o.publishStandaloneAgentActivity(
				agentType,
				fmt.Sprintf("Processing task dispatch: %s", dispatch.nodeID),
				events.VisibilityAgent,
				map[string]any{
					"task_id":   strings.TrimSpace(dispatch.taskID),
					"task_slug": strings.TrimSpace(dispatch.taskSlug),
					"node_id":   strings.TrimSpace(dispatch.nodeID),
					"source":    "orchestrator_task_dispatch",
				},
			)
		}
		publishGlobalDispatch(dispatch.agentType)
		for _, coType := range dispatch.coAgents {
			publishGlobalDispatch(coType)
		}
		return
	}
	if pipelineProtocolEligible(dispatch) {
		o.publishPipelineAgentActivity(agentshared.PipelineAgentInspector, dispatch.pipelineTaskID, dispatch.nodeID, dispatch.pipelineTaskSlug, pipelineStatus)
		for _, pipelineType := range PipelinePanelAgentTypes {
			if pipelineType == agentshared.PipelineAgentInspector {
				continue
			}
			o.publishPipelineAgentRegistration(pipelineType, dispatch.pipelineTaskID, dispatch.pipelineTaskSlug, pipelineStatus)
		}
		return
	}

	dispatched := make(map[string]struct{}, len(dispatch.coAgents)+1)
	o.publishPipelineAgentActivity(dispatch.agentType, dispatch.pipelineTaskID, dispatch.nodeID, dispatch.pipelineTaskSlug, pipelineStatus)
	dispatched[dispatch.agentType] = struct{}{}

	for _, coType := range dispatch.coAgents {
		o.publishPipelineAgentActivity(coType, dispatch.pipelineTaskID, dispatch.nodeID, dispatch.pipelineTaskSlug, pipelineStatus)
		dispatched[coType] = struct{}{}
	}

	for _, pipelineType := range PipelinePanelAgentTypes {
		if _, active := dispatched[pipelineType]; active {
			continue
		}
		o.publishPipelineAgentRegistration(pipelineType, dispatch.pipelineTaskID, dispatch.pipelineTaskSlug, pipelineStatus)
	}
}

func (o *Orchestrator) routeTaskDispatch(router *TaskRouter, dispatch *taskDispatchContext) {
	if dispatch.nodeID == "" || router == nil {
		o.logTrace("task_dispatch_route_skipped", agentlog.EventTaskDispatched, map[string]any{
			"dag_id":     dispatch.dagID,
			"node_id":    dispatch.nodeID,
			"task_id":    dispatch.taskID,
			"agent_type": dispatch.agentType,
		})
		return
	}

	done := o.dispatchDoneChannel(dispatch.dagID, dispatch.nodeID)
	o.logTrace("task_dispatch_route_begin", agentlog.EventTaskDispatched, map[string]any{
		"dag_id":          dispatch.dagID,
		"node_id":         dispatch.nodeID,
		"task_id":         dispatch.taskID,
		"agent_type":      dispatch.agentType,
		"task_slug":       dispatch.pipelineTaskSlug,
		"co_agents":       append([]string(nil), dispatch.coAgents...),
		"ack_topic":       stringValue(dispatch.nodeCtx["ack_topic"]),
		"target_agent_id": firstNonEmpty(dispatch.targetID, pipelineWorkerTargetAgentID(dispatch.pipelineTaskID, dispatch.agentType)),
	})
	if err := o.routeDispatchedPipelineTask(router, dispatch, done); err != nil {
		o.logTrace("task_dispatch_route_failed", agentlog.EventError, map[string]any{
			"dag_id":     dispatch.dagID,
			"node_id":    dispatch.nodeID,
			"task_id":    dispatch.taskID,
			"agent_type": dispatch.agentType,
			"error":      err.Error(),
		})
		o.pushEvent(&busEvent{
			Topic:     "tasks.dispatch",
			Timestamp: time.Now(),
			Severity:  severityCritical,
			Summary:   fmt.Sprintf("Route failed for node %s: %s", dispatch.nodeID, err),
		})
		return
	}
	o.logTrace("task_dispatch_route_ok", agentlog.EventTaskDispatched, map[string]any{
		"dag_id":          dispatch.dagID,
		"node_id":         dispatch.nodeID,
		"task_id":         dispatch.taskID,
		"agent_type":      dispatch.agentType,
		"task_slug":       dispatch.pipelineTaskSlug,
		"target_agent_id": firstNonEmpty(dispatch.targetID, pipelineWorkerTargetAgentID(dispatch.pipelineTaskID, dispatch.agentType)),
	})
}

func (o *Orchestrator) routeDispatchedPipelineTask(router *TaskRouter, dispatch *taskDispatchContext, done <-chan struct{}) error {
	return router.RouteWithLifecycle(dispatch.pipelineTask(dispatch.effectiveSessionID(o.SessionID())), done)
}

func (o *Orchestrator) acknowledgeTaskDispatch(router *TaskRouter, dispatch *taskDispatchContext) {
	if o == nil || o.dagBridge == nil || dispatch == nil || router == nil || strings.TrimSpace(dispatch.nodeID) == "" {
		return
	}
	o.dagBridge.AcknowledgeDispatch(dispatch.dagID, dispatch.nodeID, dispatch.agentID, dispatch.agentType)
	o.logTrace("task_dispatch_acked", agentlog.EventNodeAcked, map[string]any{
		"dag_id":     dispatch.dagID,
		"node_id":    dispatch.nodeID,
		"task_id":    dispatch.taskID,
		"agent_type": dispatch.agentType,
	})
}

func (o *Orchestrator) dispatchDoneChannel(dagID, nodeID string) <-chan struct{} {
	if o.dagBridge == nil {
		return nil
	}
	dispatcher := o.dagBridge.GetDispatcherForDAG(dagID)
	if dispatcher == nil {
		return nil
	}
	return dispatcher.DispatchDone(nodeID)
}
