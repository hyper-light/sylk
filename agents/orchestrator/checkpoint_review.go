package orchestrator

import (
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/dag"
)

type pendingCheckpointReview struct {
	taskID    string
	sessionID string
	dagID     string
	nodeID    string
	update    *PipelineUpdate
	result    *dag.NodeResult
}

func clonePipelineUpdate(update *PipelineUpdate) *PipelineUpdate {
	if update == nil {
		return nil
	}
	out := *update
	return &out
}

func cloneNodeResult(result *dag.NodeResult) *dag.NodeResult {
	if result == nil {
		return nil
	}
	out := *result
	return &out
}

func (o *Orchestrator) recordPendingCheckpointReview(task *TaskRecord, update *PipelineUpdate, result *dag.NodeResult) {
	if o == nil || task == nil || update == nil {
		return
	}
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		taskID = strings.TrimSpace(update.TaskID)
	}
	if taskID == "" {
		return
	}
	o.checkpointReviewMu.Lock()
	defer o.checkpointReviewMu.Unlock()
	if o.pendingCheckpointReviews == nil {
		o.pendingCheckpointReviews = make(map[string]*pendingCheckpointReview)
	}
	o.pendingCheckpointReviews[taskID] = &pendingCheckpointReview{
		taskID:    taskID,
		sessionID: strings.TrimSpace(task.SessionID),
		dagID:     strings.TrimSpace(update.DAGID),
		nodeID:    strings.TrimSpace(update.NodeID),
		update:    clonePipelineUpdate(update),
		result:    cloneNodeResult(result),
	}
}

func (o *Orchestrator) takePendingCheckpointReview(taskID string) *pendingCheckpointReview {
	if o == nil {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	o.checkpointReviewMu.Lock()
	defer o.checkpointReviewMu.Unlock()
	pending := o.pendingCheckpointReviews[taskID]
	if pending != nil {
		delete(o.pendingCheckpointReviews, taskID)
	}
	return pending
}

func (o *Orchestrator) peekPendingCheckpointReview(taskID string) *pendingCheckpointReview {
	if o == nil {
		return nil
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	o.checkpointReviewMu.Lock()
	defer o.checkpointReviewMu.Unlock()
	return o.pendingCheckpointReviews[taskID]
}

func (o *Orchestrator) HandleCheckpointReviewTerminal(_ string, metadata map[string]any, resp *guide.RouteResponse) {
	if o == nil || resp == nil || !metadataBool(metadata, "global_review") {
		return
	}
	taskID := strings.TrimSpace(metadataString(metadata, "task_id"))
	if taskID == "" {
		return
	}
	pending := o.peekPendingCheckpointReview(taskID)
	if pending == nil {
		return
	}
	if !resp.Success {
		o.failPendingCheckpointReview(taskID, firstNonEmpty(strings.TrimSpace(resp.Error), "global review route failed"))
		return
	}
	turnResp, err := agentshared.DecodeGlobalReviewTurnResponse(resp.Data)
	if err != nil || turnResp == nil || turnResp.Action == nil {
		o.failPendingCheckpointReview(taskID, "global review route returned an invalid terminal action")
		return
	}
	switch turnResp.Action.Type {
	case agentshared.GlobalReviewActionAccept, agentshared.GlobalReviewActionCommit:
		o.completePendingCheckpointReview(taskID)
	case agentshared.GlobalReviewActionRefusal:
		o.failPendingCheckpointReview(taskID, firstNonEmpty(strings.TrimSpace(turnResp.Action.Summary), "global review route refused repeated unchanged selection"))
	}
}

func (o *Orchestrator) completePendingCheckpointReview(taskID string) {
	pending := o.takePendingCheckpointReview(taskID)
	if pending == nil {
		return
	}
	if o.dagBridge != nil && pending.result != nil {
		o.dagBridge.NotifyNodeComplete(pending.nodeID, pending.result)
	}
	o.releaseAcceptedPipelineResources(pending.update)
}

func (o *Orchestrator) failPendingCheckpointReview(taskID, errText string) {
	pending := o.takePendingCheckpointReview(taskID)
	if pending == nil {
		return
	}
	errText = firstNonEmpty(strings.TrimSpace(errText), "global checkpoint review failed")
	if o.dagBridge != nil {
		o.dagBridge.NotifyNodeComplete(pending.nodeID, &dag.NodeResult{
			NodeID:  pending.nodeID,
			State:   dag.NodeStateFailed,
			Error:   dagNodeError(errText),
			EndTime: time.Now().UTC(),
		})
	}
}
