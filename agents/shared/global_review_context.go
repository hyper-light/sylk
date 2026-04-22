package shared

import (
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/versioning"
	"github.com/google/uuid"
)

// GlobalReviewStage indicates whether the review is a progressive
// checkpoint or the final whole-plan audit.
const (
	GlobalReviewStageCheckpoint = "checkpoint"
	GlobalReviewStageFinal      = "final"
)

// GlobalReviewContext carries everything needed to construct the global
// review route request — prompt, metadata, and snapshot. This decouples
// the review dispatch from the orchestrator: any agent that has the
// necessary context (pipeline inspector, orchestrator, or test harness)
// can build and dispatch a global review.
type GlobalReviewContext struct {
	// Task identity.
	TaskID    string
	TaskName  string
	TaskSlug  string
	NodeID    string
	DAGID     string
	SessionID string

	// VFS state.
	ReviewCandidateID string
	HadDraft          bool
	CheckpointVersion versioning.SemanticVersion
	SessionDir        string

	// Pipeline outcome.
	AcceptanceSummary   string
	EvidenceRefs        []string
	PipelineAgentType   string

	// Plan context (may be empty if unavailable to caller).
	PlanID              string
	PlanFilePath        string
	PlanSnapshot        string
	TaskDescription     string
	TaskCriteriaSnapshot string
	AcceptanceCriteria  []string
	SuccessCriteria     []string
	TestRequirements    []string
	AffectedFiles       []string

	// Workflow progress (caller computes from whatever state it has).
	Progress GlobalReviewProgress
}

// GlobalReviewProgress summarizes workflow completion state for the
// review prompt. The caller is responsible for computing this from
// its available state (orchestrator workflows, task metadata, etc.).
type GlobalReviewProgress struct {
	Stage            string
	TotalTasks       int
	CompletedTasks   int
	FailedTasks      int
	RemainingTasks   int
	CompletedTaskIDs []string
	RemainingTaskIDs []string
}

// BuildGlobalReviewRouteRequest constructs the complete guide.RouteRequest
// for dispatching a global review. The caller publishes this directly via
// the bus or RequestGuideRouteSync.
func BuildGlobalReviewRouteRequest(ctx GlobalReviewContext, sourceAgentID string) *guide.RouteRequest {
	if strings.TrimSpace(ctx.TaskID) == "" {
		return nil
	}
	reviewID := strings.TrimSpace(fmt.Sprintf("global-review-%s", sanitizeGlobalReviewPart(ctx.TaskID)))
	metadata := buildGlobalReviewContextMetadata(ctx, reviewID)
	metadata = GlobalReviewMetadata(metadata, &GlobalReviewSnapshot{
		ReviewID:       reviewID,
		ActiveAgents:   []string{GlobalReviewAgentInspector, GlobalReviewAgentTester},
		RequestedBy:    strings.TrimSpace(sourceAgentID),
		CurrentRequest: globalReviewCurrentRequest(ctx),
	})
	return &guide.RouteRequest{
		CorrelationID:  globalReviewCorrelationID(ctx),
		Input:          buildGlobalReviewContextPrompt(ctx, "inspector"),
		TargetAgentID:  GlobalReviewAgentInspector,
		ExplicitTarget: true,
		SourceAgentID:  strings.TrimSpace(sourceAgentID),
		SourceAgentName: strings.TrimSpace(sourceAgentID),
		SessionID:      strings.TrimSpace(ctx.SessionID),
		Timestamp:      time.Now().UTC(),
		Metadata:       metadata,
	}
}

func buildGlobalReviewContextMetadata(ctx GlobalReviewContext, reviewID string) map[string]any {
	metadata := map[string]any{
		"session_id":               strings.TrimSpace(ctx.SessionID),
		"task_id":                  strings.TrimSpace(ctx.TaskID),
		"task_name":                strings.TrimSpace(ctx.TaskName),
		"task_slug":                strings.TrimSpace(ctx.TaskSlug),
		"node_id":                  strings.TrimSpace(ctx.NodeID),
		"dag_id":                   strings.TrimSpace(ctx.DAGID),
		"agent_type":               GlobalReviewAgentInspector,
		"reviewer_type":            GlobalReviewAgentInspector,
		"pipeline_agent_type":      strings.TrimSpace(ctx.PipelineAgentType),
		"pipeline_task":            false,
		"global_followup":          true,
		"ot_handoff_followup":      true,
		"global_review_stage":      firstNonEmptyGR(strings.TrimSpace(ctx.Progress.Stage), GlobalReviewStageCheckpoint),
		"acceptance_summary":       strings.TrimSpace(ctx.AcceptanceSummary),
		"task_description":         strings.TrimSpace(ctx.TaskDescription),
		"workflow_total_tasks":     ctx.Progress.TotalTasks,
		"workflow_completed_tasks": ctx.Progress.CompletedTasks,
		"workflow_failed_tasks":    ctx.Progress.FailedTasks,
		"workflow_remaining_tasks": ctx.Progress.RemainingTasks,
	}
	if v := globalReviewMergeVersionString(ctx.CheckpointVersion, ctx.HadDraft); v != "" {
		metadata["global_vfs_version"] = v
	}
	if candidateID := strings.TrimSpace(ctx.ReviewCandidateID); candidateID != "" {
		metadata["review_candidate_id"] = candidateID
	}
	if sessionDir := strings.TrimSpace(ctx.SessionDir); sessionDir != "" {
		metadata["session_dir"] = sessionDir
	}
	if planID := strings.TrimSpace(ctx.PlanID); planID != "" {
		metadata["plan_id"] = planID
	}
	if strings.TrimSpace(ctx.PlanFilePath) != "" {
		metadata["plan_file_path"] = strings.TrimSpace(ctx.PlanFilePath)
	}
	if strings.TrimSpace(ctx.PlanSnapshot) != "" {
		metadata["plan_snapshot"] = strings.TrimSpace(ctx.PlanSnapshot)
	}
	if strings.TrimSpace(ctx.TaskCriteriaSnapshot) != "" {
		metadata["task_criteria_snapshot"] = strings.TrimSpace(ctx.TaskCriteriaSnapshot)
	}
	if len(ctx.AffectedFiles) > 0 {
		metadata["affected_files"] = ctx.AffectedFiles
	}
	if len(ctx.EvidenceRefs) > 0 {
		metadata["acceptance_evidence"] = ctx.EvidenceRefs
	}
	if len(ctx.Progress.CompletedTaskIDs) > 0 {
		metadata["workflow_completed_task_ids"] = ctx.Progress.CompletedTaskIDs
	}
	if len(ctx.Progress.RemainingTaskIDs) > 0 {
		metadata["workflow_remaining_task_ids"] = ctx.Progress.RemainingTaskIDs
	}
	serializedQueueKey := "global_review:" + firstNonEmptyGR(strings.TrimSpace(ctx.SessionID), "default")
	metadata["serialized_followup_queue_key"] = serializedQueueKey
	return metadata
}

func buildGlobalReviewContextPrompt(ctx GlobalReviewContext, reviewerType string) string {
	taskLabel := firstNonEmptyGR(strings.TrimSpace(ctx.TaskName), strings.TrimSpace(ctx.TaskID), "this task")
	lines := []string{
		globalReviewContextLead(reviewerType, taskLabel, ctx.Progress),
		"Operational Transform has accepted this completed pipeline. Work from the merged global state, not the pipeline draft.",
	}
	lines = append(lines, globalReviewContextStageLines(ctx.Progress)...)
	if description := strings.TrimSpace(ctx.TaskDescription); description != "" {
		lines = append(lines, "Task description: "+description)
	}
	if summary := strings.TrimSpace(ctx.AcceptanceSummary); summary != "" {
		lines = append(lines, "Pipeline acceptance summary: "+summary)
	}
	if version := globalReviewMergeVersionString(ctx.CheckpointVersion, ctx.HadDraft); version != "" {
		lines = append(lines, "Global VFS version: "+version)
	}
	if len(ctx.AffectedFiles) > 0 {
		lines = append(lines, "Affected files:")
		for _, path := range ctx.AffectedFiles {
			lines = append(lines, "- "+path)
		}
	}
	if reviewerType == GlobalReviewAgentInspector {
		if strings.TrimSpace(ctx.PlanSnapshot) != "" {
			if strings.TrimSpace(ctx.PlanFilePath) != "" {
				lines = append(lines, "Architect plan file: "+strings.TrimSpace(ctx.PlanFilePath))
			}
			lines = append(lines, "Architect plan (entire published plan):", strings.TrimSpace(ctx.PlanSnapshot))
		}
		if planID := strings.TrimSpace(ctx.PlanID); planID != "" {
			lines = append(lines, "Architect plan ID: "+planID)
		}
		if strings.TrimSpace(ctx.TaskCriteriaSnapshot) != "" {
			lines = append(lines, "Task criteria snapshot:", strings.TrimSpace(ctx.TaskCriteriaSnapshot))
		}
		if len(ctx.AcceptanceCriteria) > 0 {
			lines = append(lines, "Acceptance criteria:")
			for _, criterion := range ctx.AcceptanceCriteria {
				lines = append(lines, "- "+criterion)
			}
		}
		if len(ctx.SuccessCriteria) > 0 {
			lines = append(lines, "Success criteria:")
			for _, criterion := range ctx.SuccessCriteria {
				lines = append(lines, "- "+criterion)
			}
		}
		lines = append(lines,
			"If the architect plan or criteria context appears incomplete, immediately call `audit(aspect=context_load)` using the provided plan metadata before concluding.",
			"This follow-up is scoped to the just-accepted pipeline only. Do not branch into other completed pipelines in this turn; additional completed pipelines, if any, will arrive as separate follow-ups in merge receipt order.",
			"Consult `consult_peer(target_agent_type=librarian, query=…)` to verify codebase style, naming, layering, and established local patterns.",
			"Consult `consult_peer(target_agent_type=academic, query=…)` to challenge the current implementation and architect approach against stronger or more elegant alternatives.",
			"Consult `consult_peer(target_agent_type=archivalist, query=…)` to check past failure modes, preserved user preferences, and earlier remediation history before signing off.",
			"Use `ask_user_clarification` proactively whenever user intent, preserved behavior, or desired tradeoffs remain ambiguous.",
			"Be adversarial: treat the merged work as guilty until it proves correctness, robustness, elegance, performance, and clear alignment with the entire plan.",
		)
	} else if len(ctx.TestRequirements) > 0 {
		lines = append(lines, "Test requirements:")
		for _, requirement := range ctx.TestRequirements {
			lines = append(lines, "- "+requirement)
		}
	}
	if len(ctx.EvidenceRefs) > 0 {
		lines = append(lines, "Acceptance evidence:")
		for _, ref := range ctx.EvidenceRefs {
			lines = append(lines, "- "+ref)
		}
	}
	lines = append(lines, globalReviewContextDirective(reviewerType, ctx.Progress))
	return strings.Join(lines, "\n")
}

func globalReviewContextLead(reviewerType, taskLabel string, progress GlobalReviewProgress) string {
	switch strings.TrimSpace(reviewerType) {
	case GlobalReviewAgentTester:
		if progress.Stage == GlobalReviewStageFinal {
			return fmt.Sprintf("Global tester follow-up is required for %s. Validate the final merged result for regressions, integration risk, and whole-plan completion.", taskLabel)
		}
		return fmt.Sprintf("Global tester follow-up is required for %s. Validate this merged checkpoint for regressions, integration risk, and whether the remaining plan can proceed safely.", taskLabel)
	default:
		if progress.Stage == GlobalReviewStageFinal {
			return fmt.Sprintf("Global inspector follow-up is required for %s. Run the final whole-plan audit over the merged result.", taskLabel)
		}
		return fmt.Sprintf("Global inspector follow-up is required for %s. Run a progressive checkpoint audit over the merged result.", taskLabel)
	}
}

func globalReviewContextDirective(reviewerType string, progress GlobalReviewProgress) string {
	switch strings.TrimSpace(reviewerType) {
	case GlobalReviewAgentTester:
		if progress.Stage == GlobalReviewStageFinal {
			return "Accept this as a direct follow-up request. Validate the final merged result against the whole plan and continue through the normal tester workflow."
		}
		return "Accept this as a direct follow-up request. Validate the current merged checkpoint against the work that should exist now, and continue through the normal tester workflow without treating future unmerged tasks as defects."
	default:
		if progress.Stage == GlobalReviewStageFinal {
			return "Accept this as a direct follow-up request. Perform the final whole-plan audit from the merged state and continue through the normal inspector workflow."
		}
		return "Accept this as a direct follow-up request. Perform a progressive checkpoint audit from the merged state: future planned work may remain pending, but current plan drift, regressions, slop, or design choices that endanger the remaining plan are defects."
	}
}

func globalReviewCurrentRequest(ctx GlobalReviewContext) string {
	taskLabel := firstNonEmptyGR(strings.TrimSpace(ctx.TaskName), strings.TrimSpace(ctx.TaskID), "this task")
	if ctx.Progress.Stage == GlobalReviewStageFinal {
		return fmt.Sprintf("Run the final whole-plan global review for %s and decide the next strict global review action.", taskLabel)
	}
	return fmt.Sprintf("Run a progressive checkpoint global review for %s and decide the next strict global review action for the current merged state.", taskLabel)
}

func globalReviewContextStageLines(progress GlobalReviewProgress) []string {
	lines := []string{
		fmt.Sprintf("Review stage: %s", firstNonEmptyGR(strings.TrimSpace(progress.Stage), GlobalReviewStageCheckpoint)),
	}
	if progress.TotalTasks > 0 {
		lines = append(lines, fmt.Sprintf("Workflow progress: %d/%d tasks completed, %d failed, %d remaining.", progress.CompletedTasks, progress.TotalTasks, progress.FailedTasks, progress.RemainingTasks))
	}
	if progress.Stage == GlobalReviewStageFinal {
		lines = append(lines, "This is the final whole-plan review. Missing planned work is a defect unless the plan was explicitly revised.")
	} else {
		lines = append(lines, "This is a progressive checkpoint review. Future planned work that has not been merged yet is pending, not missing. Judge whether the current merged state is correct, robust, stylistically sound, and on track for the remaining plan.")
	}
	return lines
}

func globalReviewCorrelationID(ctx GlobalReviewContext) string {
	taskPart := sanitizeGlobalReviewPart(ctx.TaskID)
	return firstNonEmptyGR(
		strings.TrimSpace(fmt.Sprintf("global_review_%s_%s", taskPart, uuid.NewString()[:12])),
		"global_review_"+uuid.NewString()[:12],
	)
}

func globalReviewMergeVersionString(version versioning.SemanticVersion, hadDraft bool) string {
	if hadDraft && !version.IsZero() {
		return version.String()
	}
	return ""
}

func sanitizeGlobalReviewPart(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "/", "_")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

func firstNonEmptyGR(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// GlobalReviewCompleteEvent is published on the bus when the global
// inspector accepts a checkpoint. The DAG bridge subscribes to this
// to unblock layer progression.
type GlobalReviewCompleteEvent struct {
	TaskID            string                    `json:"task_id"`
	NodeID            string                    `json:"node_id"`
	DAGID             string                    `json:"dag_id"`
	SessionID         string                    `json:"session_id"`
	CheckpointVersion versioning.SemanticVersion `json:"checkpoint_version"`
	ReviewID          string                    `json:"review_id"`
	Summary           string                    `json:"summary"`
}

// GlobalReviewFailedEvent is published when the global review fails.
type GlobalReviewFailedEvent struct {
	TaskID    string `json:"task_id"`
	NodeID    string `json:"node_id"`
	DAGID     string `json:"dag_id"`
	SessionID string `json:"session_id"`
	ReviewID  string `json:"review_id"`
	Reason    string `json:"reason"`
}

// Bus topics for global review lifecycle events.
const (
	TopicGlobalReviewComplete = "global_review.complete"
	TopicGlobalReviewFailed   = "global_review.failed"
)

// IsGlobalReviewRequest returns true when the forwarded request metadata
// indicates this is a global review dispatch.
func IsGlobalReviewRequest(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	v, ok := metadata[globalReviewMetadataEnabledKey]
	if !ok {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	default:
		return false
	}
}

// BuildGlobalReviewTesterRouteRequest constructs a fire-and-forget route
// request that activates the global tester as a peer of the global
// inspector. The tester works against the same merged checkpoint and
// publishes its findings to the shared GlobalReviewSnapshot.
func BuildGlobalReviewTesterRouteRequest(fwd *guide.ForwardedRequest, inspectorAgentID string) *guide.RouteRequest {
	if fwd == nil {
		return nil
	}
	taskID := strings.TrimSpace(pipelineTaskMetadataString(fwd.Metadata, "task_id"))
	if taskID == "" {
		return nil
	}

	// Build the tester prompt from the same review context.
	testerCtx := GlobalReviewContext{
		TaskID:            taskID,
		TaskName:          pipelineTaskMetadataString(fwd.Metadata, "task_name"),
		NodeID:            pipelineTaskMetadataString(fwd.Metadata, "node_id"),
		DAGID:             pipelineTaskMetadataString(fwd.Metadata, "dag_id"),
		SessionID:         strings.TrimSpace(fwd.SessionID),
		AcceptanceSummary: pipelineTaskMetadataString(fwd.Metadata, "acceptance_summary"),
		TaskDescription:   pipelineTaskMetadataString(fwd.Metadata, "task_description"),
		SessionDir:        pipelineTaskMetadataString(fwd.Metadata, "session_dir"),
	}
	if stage := pipelineTaskMetadataString(fwd.Metadata, "global_review_stage"); stage != "" {
		testerCtx.Progress.Stage = stage
	}

	// Clone metadata for the tester, updating agent-specific fields.
	testerMetadata := make(map[string]any, len(fwd.Metadata))
	for k, v := range fwd.Metadata {
		testerMetadata[k] = v
	}
	testerMetadata["agent_type"] = GlobalReviewAgentTester
	testerMetadata["reviewer_type"] = GlobalReviewAgentTester

	return &guide.RouteRequest{
		CorrelationID:  "global_tester_" + uuid.NewString()[:12],
		Input:          buildGlobalReviewContextPrompt(testerCtx, GlobalReviewAgentTester),
		TargetAgentID:  GlobalReviewAgentTester,
		ExplicitTarget: true,
		FireAndForget:  true,
		SourceAgentID:  strings.TrimSpace(inspectorAgentID),
		SourceAgentName: "Inspector",
		SessionID:      strings.TrimSpace(fwd.SessionID),
		Timestamp:      time.Now().UTC(),
		Metadata:       testerMetadata,
	}
}
