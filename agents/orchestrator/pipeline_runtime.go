package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/claims"
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
// handoff_to_ot and discard_pipeline skills perform the SessionVFS
// extract/rollback themselves via PipelineCommitter so the agent that
// actually decided the lifecycle transition is the one performing it.
// This handler is now purely observational: it routes the inspector's
// post-handoff_to_ot work into the global review followup and clears
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
		// The pipeline inspector's handoff_to_ot skill now routes the
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
	o.orchestratorSubmitTestament(ctx, o.orchestratorTestament(
		"Pipeline update finalized: "+update.AgentType+" "+update.Status, "committed",
		[]*claims.Artifact{
			o.orchestratorArtifact("task_id", update.TaskID),
			o.orchestratorArtifact("agent_type", update.AgentType),
			o.orchestratorArtifact("status", update.Status),
			o.orchestratorArtifact("defer_node_completion", fmt.Sprintf("%t", deferNodeCompletion)),
		},
	))
	return deferNodeCompletion
}

// readInspectorHandoffOutcome extracts the review-candidate id, draft flag,
// and checkpoint version that the inspector's handoff_to_ot skill published
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
