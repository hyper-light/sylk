package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/versioning"
)

const otGlobalFollowupSource = "ot_global_followup"

func (o *Orchestrator) publishOTGlobalFollowupRequestsBestEffort(
	_ context.Context,
	task *TaskRecord,
	update *PipelineUpdate,
	mergeVersion versioning.SemanticVersion,
	hadDraft bool,
) {
	if o == nil || o.bus == nil || task == nil || update == nil {
		return
	}
	for _, reviewerType := range []string{"inspector", "tester"} {
		req := o.buildOTGlobalFollowupRequest(task, update, reviewerType, mergeVersion, hadDraft)
		if req == nil {
			continue
		}
		if err := o.publishUserVisibleFollowupRoute(req); err != nil {
			o.logWarnMsg("publish OT global follow-up", "task_id", task.ID, "reviewer_type", reviewerType, "error", err)
			continue
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
	}
}

func (o *Orchestrator) buildOTGlobalFollowupRequest(
	task *TaskRecord,
	update *PipelineUpdate,
	reviewerType string,
	mergeVersion versioning.SemanticVersion,
	hadDraft bool,
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
	return &guide.RouteRequest{
		CorrelationID:   otGlobalFollowupCorrelationID(task, reviewerType, update),
		Input:           otGlobalFollowupPrompt(task, update, reviewerType, mergeVersion, hadDraft, planText, planFilePath),
		TargetAgentID:   reviewerType,
		ExplicitTarget:  true,
		SourceAgentID:   o.config.AgentID,
		SourceAgentName: "orchestrator",
		FireAndForget:   false,
		SessionID:       firstNonEmpty(strings.TrimSpace(task.SessionID), o.config.SessionID, orchestratorStateSessionID(o)),
		Timestamp:       time.Now().UTC(),
		Metadata: map[string]any{
			"task_id":             strings.TrimSpace(task.ID),
			"task_name":           strings.TrimSpace(task.Name),
			"task_slug":           strings.TrimSpace(stringMapValue(task.Metadata, "task_slug")),
			"plan_id":             strings.TrimSpace(stringMapValue(task.Metadata, "plan_id")),
			"plan_file_path":      strings.TrimSpace(stringMapValue(task.Metadata, "plan_file_path")),
			"agent_type":          reviewerType,
			"reviewer_type":       reviewerType,
			"handoff_source":      otGlobalFollowupSource,
			"pipeline_agent_type": strings.TrimSpace(update.AgentType),
			"pipeline_node_id":    strings.TrimSpace(update.NodeID),
			"pipeline_task":       false,
			"global_followup":     true,
			"ot_handoff_followup": true,
			"global_vfs_version":  mergeVersionString(mergeVersion, hadDraft),
			"affected_files":      taskAffectedPaths(task),
			"acceptance_evidence": updateEvidenceRefs(update),
			"acceptance_summary":  strings.TrimSpace(updateSummary(update)),
			"task_description":    strings.TrimSpace(task.Description),
		},
	}
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
) string {
	taskLabel := firstNonEmpty(strings.TrimSpace(task.Name), strings.TrimSpace(task.ID), "this task")
	lines := []string{
		otGlobalFollowupLead(reviewerType, taskLabel),
		"Operational Transform has accepted this completed pipeline. Work from the merged global state, not the pipeline draft.",
	}
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
	lines = append(lines, otGlobalFollowupDirective(reviewerType))
	return strings.Join(lines, "\n")
}

func otGlobalFollowupLead(reviewerType, taskLabel string) string {
	switch strings.TrimSpace(reviewerType) {
	case "tester":
		return fmt.Sprintf("Global tester follow-up is required for %s. Validate regressions, integration risk, and cross-pipeline behavior on the merged result.", taskLabel)
	default:
		return fmt.Sprintf("Global inspector follow-up is required for %s. Audit cross-file quality, correctness, and plan adherence on the merged result.", taskLabel)
	}
}

func otGlobalFollowupDirective(reviewerType string) string {
	switch strings.TrimSpace(reviewerType) {
	case "tester":
		return "Accept this as a direct orchestrator follow-up request. Build the needed global validation work from the merged state and continue through the normal tester workflow."
	default:
		return "Accept this as a direct orchestrator follow-up request. Perform the needed global audit from the merged state and continue through the normal inspector workflow."
	}
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
