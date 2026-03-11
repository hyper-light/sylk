package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	coordination "github.com/adalundhe/sylk/core/pipeline/coordination"
)

type taskDispatchContext struct {
	data map[string]any

	taskID     string
	taskSlug   string
	workflowID string
	name       string
	agentID    string

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
		taskID:        stringValue(data["task_id"]),
		taskSlug:      stringValue(data["task_slug"]),
		workflowID:    stringValue(data["workflow_id"]),
		name:          stringValue(data["name"]),
		agentID:       stringValue(data["agent_id"]),
		nodeID:        stringValue(data["node_id"]),
		agentType:     stringValue(data["agent_type"]),
		prompt:        stringValue(data["prompt"]),
		nodeCtx:       mapValue(data["context"]),
		parentResults: mapValue(data["parent_results"]),
		dagID:         stringValue(data["dag_id"]),
		coAgents:      decodeDispatchAgentTypes(data["co_agents"]),
		now:           time.Now(),
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
	return &PipelineTask{
		NodeID:        d.nodeID,
		DAGID:         d.dagID,
		TaskID:        d.pipelineTaskID,
		AgentType:     d.agentType,
		TargetAgentID: pipelineWorkerTargetAgentID(d.pipelineTaskID, d.agentType),
		Prompt:        d.prompt,
		Context:       d.nodeCtx,
		ParentResults: d.parentResults,
		SessionID:     sessionID,
	}
}

func (d *taskDispatchContext) compoundPipelineTask(sessionID string) *CompoundPipelineTask {
	base := d.pipelineTask(sessionID)
	return &CompoundPipelineTask{
		PipelineTask:      *base,
		CoAgents:          d.coAgents,
		CollaborationMode: parseDispatchCollaborationMode(d.data["collaboration_mode"]),
		MaxReviewRounds:   intValue(d.data["max_review_rounds"]),
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

	o.state.Tasks[dispatch.taskID] = dispatch.taskRecord(o.config.SessionID)
	o.healthMonitor.RecordTaskStart(dispatch.agentType, dispatch.taskID)

	if dispatch.workflowID != "" {
		if workflow, ok := o.state.Workflows[dispatch.workflowID]; ok {
			workflow.TaskIDs = append(workflow.TaskIDs, dispatch.taskID)
		}
	}

	return o.taskRouter
}

func (o *Orchestrator) publishTaskDispatchPipelineState(dispatch *taskDispatchContext) string {
	pipelineStatus := pipelinePhaseStatus(dispatch.pipelineStage)
	if dispatch.pipelineTaskID != "" && pipelineStatus != "" {
		publishTaskPipelineState(o.bus, o.config.AgentID, dispatch.pipelineTaskID, dispatch.pipelineTaskSlug, pipelineStatus, dispatch.agentType)
	}
	return string(pipelineStatus)
}

func (o *Orchestrator) enrichTaskDispatchCoordination(dispatch *taskDispatchContext) {
	if o.coordination == nil || dispatch.pipelineTaskID == "" {
		return
	}

	packet, err := o.coordination.QueryView(context.Background(), coordination.QueryViewInput{
		TaskID:     dispatch.pipelineTaskID,
		TaskName:   dispatch.coordinationTask,
		WorkerType: strings.TrimSpace(dispatch.agentType),
	})
	if err != nil {
		o.logWarnMsg("build coordination packet", "task_id", dispatch.pipelineTaskID, "agent_type", dispatch.agentType, "error", err)
		return
	}
	if packet == nil {
		return
	}

	if err := o.addTaskDispatchPrecedents(dispatch, packet); err != nil {
		o.logWarnMsg("build coordination precedents", "task_id", dispatch.pipelineTaskID, "agent_type", dispatch.agentType, "error", err)
	}

	dispatch.nodeCtx["coordination_view"] = packet.View
	if packet.Packet != nil {
		dispatch.nodeCtx["coordination_packet"] = packet.Packet
	}
}

func (o *Orchestrator) addTaskDispatchPrecedents(dispatch *taskDispatchContext, packet *coordination.QueryViewResult) error {
	if packet.Packet == nil || !o.config.ArchivalistEnabled {
		return nil
	}

	precedents, err := o.queryCoordinationPrecedents(
		context.Background(),
		dispatch.precedentTask,
		dispatch.pipelineTaskSlug,
		strings.TrimSpace(dispatch.agentType),
	)
	if err != nil {
		return err
	}
	if len(precedents) == 0 {
		return nil
	}

	packet.Packet.HistoricalPrecedents = precedents
	packet.Packet.Summary = buildWorkerSummary(strings.TrimSpace(dispatch.agentType), packet.Packet)
	return nil
}

func (o *Orchestrator) publishTaskDispatchAgents(dispatch *taskDispatchContext, pipelineStatus string) {
	if dispatch.nodeID == "" || dispatch.agentType == "" {
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
	if router == nil || dispatch.nodeID == "" {
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
		"dag_id":     dispatch.dagID,
		"node_id":    dispatch.nodeID,
		"task_id":    dispatch.taskID,
		"agent_type": dispatch.agentType,
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
		"dag_id":     dispatch.dagID,
		"node_id":    dispatch.nodeID,
		"task_id":    dispatch.taskID,
		"agent_type": dispatch.agentType,
	})

	if o.dagBridge != nil {
		o.dagBridge.AcknowledgeDispatch(dispatch.dagID, dispatch.nodeID, dispatch.agentID, dispatch.agentType)
	}
}

func (o *Orchestrator) routeDispatchedPipelineTask(router *TaskRouter, dispatch *taskDispatchContext, done <-chan struct{}) error {
	if dispatch.pipelineStage == string(StageExecute) && len(dispatch.coAgents) > 0 {
		return router.RouteCompoundWithLifecycle(context.Background(), dispatch.compoundPipelineTask(o.config.SessionID), done)
	}
	return router.RouteWithLifecycle(dispatch.pipelineTask(o.config.SessionID), done)
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
