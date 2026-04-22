package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
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
	published, err := r.publishProtocolRoute(req)
	if err != nil {
		publishProtocolPipelineFailure(r.bus, r.agentID, task, fmt.Errorf("publish protocol pipeline request: %w", err))
		return nil
	}
	if !published {
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
		"session_id":    strings.TrimSpace(task.SessionID),
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
	if sessionDir := taskContextString(task.Context, "session_dir"); sessionDir != "" {
		metadata["session_dir"] = sessionDir
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
	if err := bus.Publish("pipeline.update."+update.AgentType, msg); err != nil {
		// Pipeline update drives UI panel state for the agent's pipeline
		// tab. Missing updates leave the UI stuck on stale status. Log
		// unconditionally for observability — this helper has no ctx to
		// attribute to a single correlation.
		slog.Warn("pipeline_update_message_publish_failed",
			"source_agent_id", sourceAgentID,
			"agent_type", update.AgentType,
			"node_id", update.NodeID,
			"status", update.Status,
			"error", err.Error(),
		)
	}
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

// finalizePipelineUpdate observes a terminal pipeline update broadcast.
//
// Authority discipline (post-refactor): the orchestrator NO LONGER mutates
// pipeline VFS state from this handler. The pipeline inspector's
// handoff_to_green and discard_pipeline skills perform the SessionVFS
// extract/rollback themselves via PipelineCommitter so the agent that
// actually decided the lifecycle transition is the one performing it.
// This handler is now purely observational: it routes the inspector's
// post-handoff_to_green work into the global review followup and clears
// coordination claims. Per-agent intermediate "succeeded" / "failed"
// updates from engineer/designer/tester used to commit or rollback the
// pipeline VFS here, which destroyed it while the inspector still had
// follow-up work queued — that path is now removed.
func (o *Orchestrator) finalizePipelineUpdate(update *PipelineUpdate) bool {
	return o.finalizePipelineUpdateCtx(context.Background(), update)
}

// finalizePipelineUpdateCtx mirrors finalizePipelineUpdate but accepts an
// explicit ctx so the orchestrator's pipeline supervisor session (opened
// in handlePipelineUpdate) threads through the dispatch path. Followup
// routes published here then carry the supervisor session's correlation
// and can emit dispatching_to_peer state that the UI bridge renders as
// the "what's happening during the handoff" indicator.
func (o *Orchestrator) finalizePipelineUpdateCtx(ctx context.Context, update *PipelineUpdate) bool {
	if update == nil || strings.TrimSpace(update.TaskID) == "" || !isPipelineCommitAgent(update.AgentType) {
		return false
	}

	task := o.lookupTask(update.TaskID)
	if task == nil {
		return false
	}

	deferNodeCompletion := false

	if update.Status == "succeeded" && strings.TrimSpace(update.AgentType) == agentshared.PipelineAgentInspector {
		// The pipeline inspector's handoff_to_green skill now routes the
		// global review directly to the global inspector via the Guide's
		// agent-to-agent protocol — the orchestrator no longer dispatches
		// the global review or tracks pending checkpoint reviews.
		//
		// The DAG node stays pending (deferNodeCompletion = true). The
		// DAG bridge subscribes to global_review.complete/failed bus
		// events and calls NotifyNodeComplete when the global inspector
		// publishes its outcome.
		deferNodeCompletion = true
	}

	if o.coordination != nil {
		_ = o.coordination.ReleaseTaskClaims(context.Background(), update.TaskID)
	}
	return deferNodeCompletion
}

// readInspectorHandoffOutcome extracts the review-candidate id, draft flag,
// and checkpoint version that the inspector's handoff_to_green skill published
// in its update output. Falls back to the session's current version when
// the output is absent (legacy publishers, test fixtures).
func readInspectorHandoffOutcome(update *PipelineUpdate, svfs SessionVFSCheckpointReader) (versioning.SemanticVersion, string, bool) {
	output, _ := update.Output.(map[string]any)
	candidateID, _ := output["review_candidate_id"].(string)
	hadDraft, _ := output["had_draft"].(bool)
	versionStr, _ := output["checkpoint_version"].(string)

	var version versioning.SemanticVersion
	if versionStr != "" {
		if parsed, ok := parseSemanticVersionString(versionStr); ok {
			version = parsed
		}
	}
	if (version == versioning.SemanticVersion{}) && svfs != nil {
		version = svfs.CurrentVersion()
	}
	return version, strings.TrimSpace(candidateID), hadDraft
}

// parseSemanticVersionString parses the "Major.Minor" format produced by
// SemanticVersion.String. Returns ok=false when the input is malformed; the
// caller falls back to the session's current version in that case.
func parseSemanticVersionString(s string) (versioning.SemanticVersion, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return versioning.SemanticVersion{}, false
	}
	dot := strings.IndexByte(s, '.')
	if dot < 0 {
		return versioning.SemanticVersion{}, false
	}
	major, err := strconv.ParseUint(s[:dot], 10, 32)
	if err != nil {
		return versioning.SemanticVersion{}, false
	}
	minor, err := strconv.ParseUint(s[dot+1:], 10, 32)
	if err != nil {
		return versioning.SemanticVersion{}, false
	}
	return versioning.SemanticVersion{Major: uint32(major), Minor: uint32(minor)}, true
}

// SessionVFSCheckpointReader is the structural subset of *versioning.SessionVFS
// used by readInspectorHandoffOutcome. Defined here so test fixtures can stub
// it without constructing a full session.
type SessionVFSCheckpointReader interface {
	CurrentVersion() versioning.SemanticVersion
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
