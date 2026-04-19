package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guide"
	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/versioning"
)

const otGlobalFollowupSource = "ot_global_followup"

const (
	globalReviewStageCheckpoint = "checkpoint"
	globalReviewStageFinal      = "final"
)

type globalReviewProgress struct {
	Stage            string
	TotalTasks       int
	CompletedTasks   int
	FailedTasks      int
	RemainingTasks   int
	CompletedTaskIDs []string
	RemainingTaskIDs []string
}

func (o *Orchestrator) publishOTGlobalFollowupRequest(
	ctx context.Context,
	task *TaskRecord,
	update *PipelineUpdate,
	checkpointVersion versioning.SemanticVersion,
	hadDraft bool,
	reviewCandidateID string,
) error {
	if o == nil || task == nil || update == nil {
		return nil
	}
	if o.bus == nil {
		return fmt.Errorf("guide bus is not configured")
	}
	reviewerType := "inspector"
	req := o.buildOTGlobalFollowupRequest(task, update, reviewerType, checkpointVersion, hadDraft, reviewCandidateID)
	if req == nil {
		return nil
	}
	// Announce `dispatching_to_peer` on the supervisor session BEFORE the
	// route publishes. The peer correlation is the request's CID so when
	// the inspector's stream eventually starts and publishes its own
	// `receiving` state on that same correlation, the chat panel seamlessly
	// transitions from the bridge row to the streaming entry without a
	// silent gap. This is the fix for the 13+ second disappearance
	// between a pipeline's terminal update and the next agent's stream.
	if session := agentshared.ActivitySessionFromContext(ctx); session != nil {
		peer := &guide.AgentStateEvent{
			PeerAgentType:     reviewerType,
			PeerCorrelationID: strings.TrimSpace(req.CorrelationID),
		}
		detail := fmt.Sprintf("dispatching %s follow-up to %s", strings.TrimSpace(task.ID), reviewerType)
		if err := agentshared.PublishAgentState(o.bus, o.channels, ctx, o.config.AgentID, "orchestrator",
			guide.AgentStateDispatchingToPeer, detail, peer); err != nil {
			o.logWarnMsg("orchestrator_ot_followup_dispatch_state_publish_failed",
				"task_id", task.ID,
				"reviewer", reviewerType,
				"error", err.Error())
		}
	}
	if err := o.publishUserVisibleFollowupRoute(req); err != nil {
		return fmt.Errorf("publish OT global follow-up for %s: %w", task.ID, err)
	}
	o.publishStandaloneAgentActivity(
		reviewerType,
		fmt.Sprintf("Operational Transform queued follow-up for task %s", firstNonEmpty(strings.TrimSpace(task.Name), strings.TrimSpace(task.ID))),
		events.VisibilityUser,
		map[string]any{
			"task_id":     strings.TrimSpace(task.ID),
			"task_name":   strings.TrimSpace(task.Name),
			"source":      otGlobalFollowupSource,
			"reviewer":    reviewerType,
			"pipeline_id": strings.TrimSpace(task.ID),
		},
	)
	return nil
}

func (o *Orchestrator) buildOTGlobalFollowupRequest(
	task *TaskRecord,
	update *PipelineUpdate,
	reviewerType string,
	checkpointVersion versioning.SemanticVersion,
	hadDraft bool,
	reviewCandidateID string,
) *guide.RouteRequest {
	if o == nil || task == nil || update == nil {
		return nil
	}
	reviewerType = strings.TrimSpace(reviewerType)
	if reviewerType == "" {
		return nil
	}
	planText, planFilePath := "", ""
	if reviewerType == "inspector" {
		planText, planFilePath = o.globalInspectorPlanContext(task)
	}
	progress := o.globalReviewProgress(task)
	sessionID := firstNonEmpty(strings.TrimSpace(task.SessionID), strings.TrimSpace(stringMapValue(task.Metadata, "session_id")), orchestratorStateSessionID(o), o.SessionID())
	metadata := map[string]any{
		"session_id":                    sessionID,
		"task_id":                       strings.TrimSpace(task.ID),
		"task_name":                     strings.TrimSpace(task.Name),
		"task_slug":                     strings.TrimSpace(stringMapValue(task.Metadata, "task_slug")),
		"plan_id":                       strings.TrimSpace(stringMapValue(task.Metadata, "plan_id")),
		"plan_file_path":                strings.TrimSpace(stringMapValue(task.Metadata, "plan_file_path")),
		"agent_type":                    reviewerType,
		"reviewer_type":                 reviewerType,
		"handoff_source":                otGlobalFollowupSource,
		"pipeline_agent_type":           strings.TrimSpace(update.AgentType),
		"pipeline_node_id":              strings.TrimSpace(update.NodeID),
		"pipeline_task":                 false,
		"global_followup":               true,
		"ot_handoff_followup":           true,
		"serialized_followup_queue_key": globalReviewSerializedQueueKey(sessionID),
		"global_vfs_version":            mergeVersionString(checkpointVersion, hadDraft),
		"affected_files":                taskAffectedPaths(task),
		"acceptance_evidence":           updateEvidenceRefs(update),
		"acceptance_summary":            strings.TrimSpace(updateSummary(update)),
		"task_description":              strings.TrimSpace(task.Description),
		"global_review_stage":           progress.Stage,
		"workflow_total_tasks":          progress.TotalTasks,
		"workflow_completed_tasks":      progress.CompletedTasks,
		"workflow_failed_tasks":         progress.FailedTasks,
		"workflow_remaining_tasks":      progress.RemainingTasks,
		"workflow_completed_task_ids":   progress.CompletedTaskIDs,
		"workflow_remaining_task_ids":   progress.RemainingTaskIDs,
	}
	if candidateID := strings.TrimSpace(reviewCandidateID); candidateID != "" {
		metadata["review_candidate_id"] = candidateID
	}
	if sessionDir := strings.TrimSpace(stringMapValue(task.Metadata, "session_dir")); sessionDir != "" {
		metadata["session_dir"] = sessionDir
	}
	if strings.TrimSpace(planText) != "" {
		metadata[workflowPlanSnapshotKey] = strings.TrimSpace(planText)
	}
	if criteriaSnapshot := strings.TrimSpace(stringMapValue(task.Metadata, "task_criteria_snapshot")); criteriaSnapshot != "" {
		metadata["task_criteria_snapshot"] = criteriaSnapshot
	}
	reviewID := strings.TrimSpace(fmt.Sprintf("global-review-%s", sanitizePipelineIdentityPart(task.ID)))
	metadata = agentshared.GlobalReviewMetadata(metadata, &agentshared.GlobalReviewSnapshot{
		ReviewID:       reviewID,
		RequestedBy:    "orchestrator",
		CurrentRequest: otGlobalReviewCurrentRequest(task, progress),
	})
	return &guide.RouteRequest{
		CorrelationID:   otGlobalFollowupCorrelationID(task, reviewerType, update),
		Input:           otGlobalFollowupPrompt(task, update, reviewerType, checkpointVersion, hadDraft, planText, planFilePath, progress),
		TargetAgentID:   reviewerType,
		ExplicitTarget:  true,
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		FireAndForget:   false,
		SessionID:       sessionID,
		Timestamp:       otGlobalFollowupTimestamp(update),
		Metadata:        metadata,
	}
}

func globalReviewSerializedQueueKey(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	return "global_review:" + sessionID
}

func (o *Orchestrator) publishUserVisibleFollowupRoute(req *guide.RouteRequest) error {
	if o == nil || req == nil {
		return nil
	}
	o.mu.RLock()
	router := o.taskRouter
	o.mu.RUnlock()
	if router != nil {
		return router.PublishUserVisibleRoute(req)
	}
	if o.bus == nil {
		return fmt.Errorf("guide bus is not configured")
	}
	return o.bus.Publish(guide.TopicGuideRequests, guide.NewRequestMessage(generateMessageID(), req))
}

func orchestratorStateSessionID(o *Orchestrator) string {
	if o == nil || o.state == nil {
		return ""
	}
	return strings.TrimSpace(o.state.SessionID)
}

func otGlobalFollowupPrompt(
	task *TaskRecord,
	update *PipelineUpdate,
	reviewerType string,
	mergeVersion versioning.SemanticVersion,
	hadDraft bool,
	planText string,
	planFilePath string,
	progress globalReviewProgress,
) string {
	taskLabel := firstNonEmpty(strings.TrimSpace(task.Name), strings.TrimSpace(task.ID), "this task")
	lines := []string{
		otGlobalFollowupLead(reviewerType, taskLabel, progress),
		"Operational Transform has accepted this completed pipeline. Work from the merged global state, not the pipeline draft.",
	}
	lines = append(lines, otGlobalFollowupStageLines(progress)...)
	if description := strings.TrimSpace(task.Description); description != "" {
		lines = append(lines, "Task description: "+description)
	}
	if summary := strings.TrimSpace(updateSummary(update)); summary != "" {
		lines = append(lines, "Pipeline acceptance summary: "+summary)
	}
	if version := mergeVersionString(mergeVersion, hadDraft); version != "" {
		lines = append(lines, "Global VFS version: "+version)
	}
	if paths := taskAffectedPaths(task); len(paths) > 0 {
		lines = append(lines, "Affected files:")
		for _, path := range paths {
			lines = append(lines, "- "+path)
		}
	}
	if reviewerType == "inspector" {
		if strings.TrimSpace(planText) != "" {
			if strings.TrimSpace(planFilePath) != "" {
				lines = append(lines, "Architect plan file: "+strings.TrimSpace(planFilePath))
			}
			lines = append(lines, "Architect plan (entire published plan):", strings.TrimSpace(planText))
		}
		if planID := strings.TrimSpace(stringMapValue(task.Metadata, "plan_id")); planID != "" {
			lines = append(lines, "Architect plan ID: "+planID)
		}
		if criteriaSnapshot := strings.TrimSpace(stringMapValue(task.Metadata, "task_criteria_snapshot")); criteriaSnapshot != "" {
			lines = append(lines, "Task criteria snapshot:", criteriaSnapshot)
		}
		if criteria := taskMetadataStringList(task, "acceptance_criteria"); len(criteria) > 0 {
			lines = append(lines, "Acceptance criteria:")
			for _, criterion := range criteria {
				lines = append(lines, "- "+criterion)
			}
		}
		if criteria := taskMetadataStringList(task, "success_criteria"); len(criteria) > 0 {
			lines = append(lines, "Success criteria:")
			for _, criterion := range criteria {
				lines = append(lines, "- "+criterion)
			}
		}
		lines = append(lines,
			"If the architect plan or criteria context appears incomplete, immediately call `load_plan_context` using the provided plan metadata before concluding.",
			"This follow-up is scoped to the just-accepted pipeline only. Do not branch into other completed pipelines in this turn; additional completed pipelines, if any, will arrive as separate follow-ups in merge receipt order.",
			"Consult `consult_librarian_style` to verify codebase style, naming, layering, and established local patterns.",
			"Consult `consult_academic_approach` to challenge the current implementation and architect approach against stronger or more elegant alternatives.",
			"Consult `consult_archivalist_context` to check past failure modes, preserved user preferences, and earlier remediation history before signing off.",
			"Use `request_user_clarification` proactively whenever user intent, preserved behavior, or desired tradeoffs remain ambiguous.",
			"Be adversarial: treat the merged work as guilty until it proves correctness, robustness, elegance, performance, and clear alignment with the entire plan.",
		)
	} else if requirements := taskMetadataStringList(task, "test_requirements"); len(requirements) > 0 {
		lines = append(lines, "Test requirements:")
		for _, requirement := range requirements {
			lines = append(lines, "- "+requirement)
		}
	}
	if refs := updateEvidenceRefs(update); len(refs) > 0 {
		lines = append(lines, "Acceptance evidence:")
		for _, ref := range refs {
			lines = append(lines, "- "+ref)
		}
	}
	lines = append(lines, otGlobalFollowupDirective(reviewerType, progress))
	return strings.Join(lines, "\n")
}

func otGlobalFollowupLead(reviewerType, taskLabel string, progress globalReviewProgress) string {
	switch strings.TrimSpace(reviewerType) {
	case "tester":
		if progress.Stage == globalReviewStageFinal {
			return fmt.Sprintf("Global tester follow-up is required for %s. Validate the final merged result for regressions, integration risk, and whole-plan completion.", taskLabel)
		}
		return fmt.Sprintf("Global tester follow-up is required for %s. Validate this merged checkpoint for regressions, integration risk, and whether the remaining plan can proceed safely.", taskLabel)
	default:
		if progress.Stage == globalReviewStageFinal {
			return fmt.Sprintf("Global inspector follow-up is required for %s. Run the final whole-plan audit over the merged result.", taskLabel)
		}
		return fmt.Sprintf("Global inspector follow-up is required for %s. Run a progressive checkpoint audit over the merged result.", taskLabel)
	}
}

func otGlobalFollowupDirective(reviewerType string, progress globalReviewProgress) string {
	switch strings.TrimSpace(reviewerType) {
	case "tester":
		if progress.Stage == globalReviewStageFinal {
			return "Accept this as a direct orchestrator follow-up request. Validate the final merged result against the whole plan and continue through the normal tester workflow."
		}
		return "Accept this as a direct orchestrator follow-up request. Validate the current merged checkpoint against the work that should exist now, and continue through the normal tester workflow without treating future unmerged tasks as defects."
	default:
		if progress.Stage == globalReviewStageFinal {
			return "Accept this as a direct orchestrator follow-up request. Perform the final whole-plan audit from the merged state and continue through the normal inspector workflow."
		}
		return "Accept this as a direct orchestrator follow-up request. Perform a progressive checkpoint audit from the merged state: future planned work may remain pending, but current plan drift, regressions, slop, or design choices that endanger the remaining plan are defects."
	}
}

func otGlobalReviewCurrentRequest(task *TaskRecord, progress globalReviewProgress) string {
	taskLabel := firstNonEmpty(strings.TrimSpace(task.Name), strings.TrimSpace(task.ID), "this task")
	if progress.Stage == globalReviewStageFinal {
		return fmt.Sprintf("Run the final whole-plan global review for %s and decide the next strict global review action.", taskLabel)
	}
	return fmt.Sprintf("Run a progressive checkpoint global review for %s and decide the next strict global review action for the current merged state.", taskLabel)
}

func otGlobalFollowupStageLines(progress globalReviewProgress) []string {
	lines := []string{
		fmt.Sprintf("Review stage: %s", firstNonEmpty(strings.TrimSpace(progress.Stage), globalReviewStageCheckpoint)),
	}
	if progress.TotalTasks > 0 {
		lines = append(lines, fmt.Sprintf("Workflow progress: %d/%d tasks completed, %d failed, %d remaining.", progress.CompletedTasks, progress.TotalTasks, progress.FailedTasks, progress.RemainingTasks))
	}
	if progress.Stage == globalReviewStageFinal {
		lines = append(lines, "This is the final whole-plan review. Missing planned work is a defect unless the plan was explicitly revised.")
	} else {
		lines = append(lines, "This is a progressive checkpoint review. Future planned work that has not been merged yet is pending, not missing. Judge whether the current merged state is correct, robust, stylistically sound, and on track for the remaining plan.")
	}
	return lines
}

func otGlobalFollowupTimestamp(update *PipelineUpdate) time.Time {
	if update != nil && !update.Timestamp.IsZero() {
		return update.Timestamp.UTC()
	}
	return time.Now().UTC()
}

func (o *Orchestrator) globalReviewProgress(task *TaskRecord) globalReviewProgress {
	progress := globalReviewProgress{Stage: globalReviewStageCheckpoint}
	if o == nil || task == nil {
		return progress
	}
	workflowID := strings.TrimSpace(task.WorkflowID)
	if workflowID == "" {
		progress.Stage = globalReviewStageFinal
		return progress
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	workflow := o.state.Workflows[workflowID]
	if workflow == nil {
		progress.Stage = globalReviewStageFinal
		return progress
	}
	progress.TotalTasks = len(workflow.TaskIDs)
	for _, taskID := range workflow.TaskIDs {
		record := o.state.Tasks[taskID]
		if record == nil {
			progress.RemainingTasks++
			progress.RemainingTaskIDs = append(progress.RemainingTaskIDs, taskID)
			continue
		}
		switch record.Status {
		case TaskStatusCompleted:
			progress.CompletedTasks++
			progress.CompletedTaskIDs = append(progress.CompletedTaskIDs, taskID)
		case TaskStatusFailed, TaskStatusTimedOut, TaskStatusCancelled:
			progress.FailedTasks++
		default:
			progress.RemainingTasks++
			progress.RemainingTaskIDs = append(progress.RemainingTaskIDs, taskID)
		}
	}
	if progress.TotalTasks == 0 || progress.RemainingTasks == 0 {
		progress.Stage = globalReviewStageFinal
	}
	return progress
}

func otGlobalFollowupCorrelationID(task *TaskRecord, reviewerType string, update *PipelineUpdate) string {
	taskID := ""
	if task != nil {
		taskID = sanitizePipelineIdentityPart(task.ID)
	}
	reviewerType = sanitizePipelineIdentityPart(reviewerType)
	eventKey := "ot"
	if update != nil {
		if !update.Timestamp.IsZero() {
			eventKey = sanitizePipelineIdentityPart(update.Timestamp.UTC().Format("20060102T150405.000000000"))
		} else if nodeID := sanitizePipelineIdentityPart(update.NodeID); nodeID != "" {
			eventKey = nodeID
		}
	}
	return firstNonEmpty(strings.TrimSpace(fmt.Sprintf("ot_%s_%s_%s", taskID, reviewerType, eventKey)), "ot_followup")
}

func mergeVersionString(version versioning.SemanticVersion, hadDraft bool) string {
	if hadDraft && !version.IsZero() {
		return version.String()
	}
	return ""
}

func updateSummary(update *PipelineUpdate) string {
	if update == nil {
		return ""
	}
	if payload, ok := update.Output.(map[string]any); ok {
		if summary := stringMapValue(payload, "summary"); summary != "" {
			return summary
		}
	}
	return strings.TrimSpace(update.Message)
}

func updateEvidenceRefs(update *PipelineUpdate) []string {
	if update == nil {
		return nil
	}
	payload, ok := update.Output.(map[string]any)
	if !ok {
		return nil
	}
	return uniqueNonEmptyStrings(stringListMapValue(payload, "evidence_refs"))
}

func taskAffectedPaths(task *TaskRecord) []string {
	if task == nil || len(task.Metadata) == 0 {
		return nil
	}
	switch typed := task.Metadata["affected_files"].(type) {
	case []string:
		return uniqueNonEmptyStrings(typed)
	case []architect.TaskFileTarget:
		paths := make([]string, 0, len(typed))
		for _, target := range typed {
			if trimmed := strings.TrimSpace(target.Path); trimmed != "" {
				paths = append(paths, trimmed)
			}
		}
		return uniqueNonEmptyStrings(paths)
	case []any:
		paths := make([]string, 0, len(typed))
		for _, entry := range typed {
			switch value := entry.(type) {
			case string:
				if trimmed := strings.TrimSpace(value); trimmed != "" {
					paths = append(paths, trimmed)
				}
			case architect.TaskFileTarget:
				if trimmed := strings.TrimSpace(value.Path); trimmed != "" {
					paths = append(paths, trimmed)
				}
			case map[string]any:
				if trimmed := strings.TrimSpace(stringMapValue(value, "path")); trimmed != "" {
					paths = append(paths, trimmed)
				}
			}
		}
		return uniqueNonEmptyStrings(paths)
	default:
		return nil
	}
}

func taskMetadataStringList(task *TaskRecord, key string) []string {
	if task == nil || len(task.Metadata) == 0 {
		return nil
	}
	switch typed := task.Metadata[key].(type) {
	case []string:
		return uniqueNonEmptyStrings(typed)
	case []any:
		return uniqueNonEmptyStrings(stringListFromAnySlice(typed))
	default:
		return nil
	}
}

func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			unique = append(unique, trimmed)
		}
	}
	return unique
}
