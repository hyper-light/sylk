package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/pipeline/taskstate"
	"github.com/adalundhe/sylk/core/versioning"
)

func pipelineProtocolEligible(dispatch *taskDispatchContext) bool {
	if dispatch == nil {
		return false
	}
	return protocolPipelineWorkerEligible(dispatch.agentType) &&
		protocolPipelineStageEligible(dispatch.pipelineStage)
}

func protocolPipelineTaskEligible(task *PipelineTask) bool {
	if task == nil {
		return false
	}
	if !protocolPipelineWorkerEligible(task.AgentType) {
		return false
	}
	expectedTarget := pipelineWorkerTargetAgentID(task.TaskID, task.AgentType)
	if strings.TrimSpace(task.TargetAgentID) != expectedTarget {
		return false
	}
	return protocolPipelineStageEligible(taskContextString(task.Context, "pipeline_stage"))
}

func protocolPipelineWorkerEligible(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case agentshared.PipelineAgentEngineer, agentshared.PipelineAgentDesigner:
		return true
	default:
		return false
	}
}

func protocolPipelineStageEligible(stage string) bool {
	switch strings.TrimSpace(stage) {
	case "", string(StageExecute):
		return true
	default:
		return false
	}
}

func (r *TaskRouter) routeProtocolPipelineTask(task *PipelineTask, _ <-chan struct{}) error {
	if r == nil {
		return fmt.Errorf("task router is not configured")
	}
	if r.bus == nil {
		return fmt.Errorf("task router bus is not configured")
	}
	if task == nil {
		return fmt.Errorf("pipeline task is required")
	}

	initialTask, err := buildInitialProtocolPipelineTask(task)
	if err != nil {
		publishProtocolPipelineFailure(r.bus, r.agentID, task, err)
		return nil
	}
	payload, err := json.Marshal(initialTask)
	if err != nil {
		publishProtocolPipelineFailure(r.bus, r.agentID, task, fmt.Errorf("encode protocol pipeline task: %w", err))
		return nil
	}

	req := &guide.RouteRequest{
		CorrelationID:   "pipe_" + generateMessageID(),
		Input:           string(payload),
		TargetAgentID:   strings.TrimSpace(initialTask.TargetAgentID),
		ExplicitTarget:  true,
		SourceAgentID:   r.agentID,
		SourceAgentName: "orchestrator",
		SessionID:       strings.TrimSpace(initialTask.SessionID),
		Timestamp:       time.Now().UTC(),
		Metadata:        protocolPipelineRouteMetadata(initialTask),
	}
	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = map[string]any{
		"pipeline_task": true,
		"dag_id":        task.DAGID,
		"node_id":       task.NodeID,
		"task_id":       task.TaskID,
	}
	if err := r.bus.Publish(guide.TopicGuideRequests, msg); err != nil {
		publishProtocolPipelineFailure(r.bus, r.agentID, task, fmt.Errorf("publish protocol pipeline request: %w", err))
		return nil
	}

	publishPipelineUpdateMessage(r.bus, r.agentID, &PipelineUpdate{
		DAGID:     task.DAGID,
		NodeID:    task.NodeID,
		TaskID:    task.TaskID,
		AgentID:   strings.TrimSpace(initialTask.TargetAgentID),
		AgentType: agentshared.PipelineAgentInspector,
		Status:    "running",
		Stage:     string(StageInspect),
		Progress:  0.15,
		Message:   initialProtocolPipelineRequest,
		Attempt:   0,
		Timestamp: time.Now().UTC(),
	})
	if r.onNodeActivity != nil {
		r.onNodeActivity(task.DAGID, task.NodeID)
	}
	return nil
}

const initialProtocolPipelineRequest = "Inspect the task, define or refine the criteria, and decide who should act next."

func buildInitialProtocolPipelineTask(task *PipelineTask) (*agentshared.PipelineTaskInput, error) {
	if task == nil {
		return nil, fmt.Errorf("pipeline task is required")
	}
	workerType := strings.TrimSpace(task.AgentType)
	switch workerType {
	case agentshared.PipelineAgentEngineer, agentshared.PipelineAgentDesigner:
	default:
		return nil, fmt.Errorf("unsupported pipeline worker type %q", task.AgentType)
	}

	ctx := clonePipelineTaskContext(task.Context)
	if ctx == nil {
		ctx = map[string]any{}
	}
	ctx["agent_type"] = workerType
	ctx["pipeline_stage"] = string(StageInspect)
	ctx["pipeline_protocol"] = agentshared.PipelineProtocolSnapshotMap(initialProtocolSnapshot(task))

	return &agentshared.PipelineTaskInput{
		NodeID:        strings.TrimSpace(task.NodeID),
		DAGID:         strings.TrimSpace(task.DAGID),
		TaskID:        strings.TrimSpace(task.TaskID),
		AgentType:     agentshared.PipelineAgentInspector,
		TargetAgentID: pipelineWorkerTargetAgentID(task.TaskID, agentshared.PipelineAgentInspector),
		Prompt:        strings.TrimSpace(task.Prompt),
		Context:       ctx,
		ParentResults: clonePipelineParentResults(task.ParentResults),
		SessionID:     strings.TrimSpace(task.SessionID),
	}, nil
}

func initialProtocolSnapshot(task *PipelineTask) *agentshared.PipelineProtocolSnapshot {
	return &agentshared.PipelineProtocolSnapshot{
		Roster:         initialProtocolRoster(task),
		ActiveAgents:   []string{agentshared.PipelineAgentInspector},
		RequestedBy:    agentshared.PipelineAgentInspector,
		Mode:           string(agentshared.PipelineTurnModeSingle),
		CurrentRequest: initialProtocolPipelineRequest,
	}
}

func initialProtocolRoster(task *PipelineTask) []agentshared.PipelineProtocolAgent {
	roster := []agentshared.PipelineProtocolAgent{
		{AgentType: agentshared.PipelineAgentInspector, Role: "entrypoint and final acceptance"},
		{AgentType: agentshared.PipelineAgentTester, Role: "test authoring and execution"},
	}
	seen := map[string]struct{}{
		agentshared.PipelineAgentInspector: {},
		agentshared.PipelineAgentTester:    {},
	}

	appendAgent := func(agentType, role string) {
		agentType = strings.TrimSpace(agentType)
		if agentType == "" {
			return
		}
		if _, ok := seen[agentType]; ok {
			return
		}
		seen[agentType] = struct{}{}
		roster = append(roster, agentshared.PipelineProtocolAgent{
			AgentType: agentType,
			Role:      role,
		})
	}

	appendAgent(strings.TrimSpace(task.AgentType), "implementation")
	for _, agentType := range decodeDispatchAgentTypes(task.Context["co_agents"]) {
		appendAgent(agentType, "execute cohort peer")
	}
	return roster
}

func protocolPipelineRouteMetadata(task *agentshared.PipelineTaskInput) map[string]any {
	if task == nil {
		return nil
	}
	metadata := map[string]any{
		"pipeline_task": true,
		"task_id":       strings.TrimSpace(task.TaskID),
		"task_slug":     taskContextString(task.Context, "task_slug"),
		"task_name":     taskContextString(task.Context, "task_name"),
		"agent_type":    strings.TrimSpace(task.AgentType),
	}
	if dagID := strings.TrimSpace(task.DAGID); dagID != "" {
		metadata["dag_id"] = dagID
	}
	if nodeID := strings.TrimSpace(task.NodeID); nodeID != "" {
		metadata["node_id"] = nodeID
	}
	if ackTopic := taskContextString(task.Context, "ack_topic"); ackTopic != "" {
		metadata["ack_topic"] = ackTopic
	}
	return metadata
}

func clonePipelineTaskContext(ctx map[string]any) map[string]any {
	if len(ctx) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(ctx))
	for key, value := range ctx {
		cloned[key] = value
	}
	return cloned
}

func clonePipelineParentResults(results map[string]any) map[string]any {
	if len(results) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(results))
	for key, value := range results {
		cloned[key] = value
	}
	return cloned
}

func publishProtocolPipelineFailure(bus guide.EventBus, sourceAgentID string, task *PipelineTask, err error) {
	if task == nil || err == nil {
		return
	}
	publishPipelineUpdateMessage(bus, sourceAgentID, &PipelineUpdate{
		DAGID:     task.DAGID,
		NodeID:    task.NodeID,
		TaskID:    task.TaskID,
		AgentID:   pipelineWorkerTargetAgentID(task.TaskID, agentshared.PipelineAgentInspector),
		AgentType: agentshared.PipelineAgentInspector,
		Status:    "failed",
		Stage:     string(StageInspect),
		Progress:  1,
		Message:   err.Error(),
		Error:     err.Error(),
		Timestamp: time.Now().UTC(),
	})
}

func publishPipelineUpdateMessage(bus guide.EventBus, sourceAgentID string, update *PipelineUpdate) {
	if bus == nil || update == nil || strings.TrimSpace(update.AgentType) == "" {
		return
	}
	msg := &guide.Message{
		ID:            generateMessageID(),
		Type:          guide.MessageTypePipelineUpdate,
		SourceAgentID: sourceAgentID,
		Payload:       update,
		Timestamp:     time.Now().UTC(),
	}
	_ = bus.Publish("pipeline.update."+update.AgentType, msg)
}

func (o *Orchestrator) recordDispatchActivity(dagID, nodeID string) {
	if o == nil || o.dagBridge == nil {
		return
	}
	o.dagBridge.RecordDispatchActivity(dagID, nodeID)
}

func (o *Orchestrator) recordPipelineDispatchActivity(update *PipelineUpdate) {
	if update == nil || isTerminalStatus(update.Status) {
		return
	}
	o.recordDispatchActivity(update.DAGID, update.NodeID)
}

func pipelineTaskStateForUpdate(status, stage string) taskstate.Status {
	switch strings.TrimSpace(status) {
	case "running":
		return pipelinePhaseStatus(stage)
	case "succeeded":
		return taskstate.StatusCompleted
	case "failed", "timed_out":
		return taskstate.StatusFailed
	case "cancelled":
		return taskstate.StatusCancelled
	default:
		return ""
	}
}

func (o *Orchestrator) finalizePipelineUpdate(update *PipelineUpdate) {
	if update == nil || strings.TrimSpace(update.TaskID) == "" || !isPipelineCommitAgent(update.AgentType) {
		return
	}

	task := o.lookupTask(update.TaskID)
	if task == nil {
		return
	}

	var (
		mergeVersion versioning.SemanticVersion
		hadDraft     bool
	)

	switch update.Status {
	case "succeeded":
		if strings.TrimSpace(update.AgentType) == agentshared.PipelineAgentInspector {
			o.publishTaskDraftMergeStarted(task)
		}
		var err error
		mergeVersion, hadDraft, err = o.commitTaskDraft(context.Background(), task)
		if err != nil {
			o.publishTaskDraftMergeFailure(task, err)
			update.Status = "failed"
			update.Error = err.Error()
			update.Message = firstNonEmpty(update.Message, "draft merge failed")
			publishTaskPipelineState(o.bus, o.config.AgentID, update.TaskID, "", taskstate.StatusFailed, update.AgentType)
		} else if hadDraft {
			o.publishTaskDraftMergeSuccess(task, mergeVersion)
		}
	case "failed", "timed_out", "cancelled":
		_ = o.rollbackTaskDraft(task)
	}

	if update.Status == "succeeded" && strings.TrimSpace(update.AgentType) == agentshared.PipelineAgentInspector {
		o.publishOTGlobalFollowupRequestsBestEffort(context.Background(), task, update, mergeVersion, hadDraft)
	}

	if o.coordination != nil {
		_ = o.coordination.ReleaseTaskClaims(context.Background(), update.TaskID)
	}
}

func isPipelineCommitAgent(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case "engineer", "designer", agentshared.PipelineAgentInspector:
		return true
	default:
		return false
	}
}

func (o *Orchestrator) lookupTask(taskID string) *TaskRecord {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.state.Tasks[taskID]
}
