package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/pipeline/coordination"
)

const coordinationReviewWakeDAGID = "coordination-review"

func (o *Orchestrator) publishReviewWakeBestEffort(ctx context.Context, review *coordination.Review) {
	if o == nil || review == nil {
		return
	}
	if skipPipelineReviewWake(review) {
		o.logTrace("coordination_review_wakeup_skipped", agentlog.EventRegistryEvent, map[string]any{
			"task_id":       strings.TrimSpace(review.TaskID),
			"review_id":     strings.TrimSpace(review.ID),
			"artifact_id":   strings.TrimSpace(review.ArtifactID),
			"reviewer_type": strings.TrimSpace(review.ReviewerType),
			"reason":        "pipeline dag execute stage owns implementation handoff",
		})
		return
	}
	if err := o.publishReviewWake(ctx, review); err != nil {
		o.logTrace("coordination_review_wakeup_failed", agentlog.EventError, map[string]any{
			"task_id":       strings.TrimSpace(review.TaskID),
			"review_id":     strings.TrimSpace(review.ID),
			"artifact_id":   strings.TrimSpace(review.ArtifactID),
			"reviewer_type": strings.TrimSpace(review.ReviewerType),
			"error":         err.Error(),
		})
		return
	}
	o.publishStandaloneAgentActivity(
		review.ReviewerType,
		fmt.Sprintf("Pending coordination review for task %s", strings.TrimSpace(review.TaskID)),
		events.VisibilityAgent,
		map[string]any{
			"task_id":     strings.TrimSpace(review.TaskID),
			"review_id":   strings.TrimSpace(review.ID),
			"artifact_id": strings.TrimSpace(review.ArtifactID),
			"source":      "coordination_review_wakeup",
		},
	)
	o.logTrace("coordination_review_wakeup_published", agentlog.EventTaskDispatched, map[string]any{
		"task_id":         strings.TrimSpace(review.TaskID),
		"review_id":       strings.TrimSpace(review.ID),
		"artifact_id":     strings.TrimSpace(review.ArtifactID),
		"reviewer_type":   strings.TrimSpace(review.ReviewerType),
		"target_agent_id": pipelineWorkerTargetAgentID(o.config.SessionID, review.TaskID, review.ReviewerType),
	})
}

func skipPipelineReviewWake(review *coordination.Review) bool {
	if review == nil || strings.TrimSpace(review.TaskID) == "" {
		return false
	}
	switch strings.TrimSpace(review.ReviewerType) {
	case "engineer", "designer", "tester-pipeline", "inspector-pipeline":
		return true
	default:
		return false
	}
}

func (o *Orchestrator) publishReviewWake(ctx context.Context, review *coordination.Review) error {
	if err := validateReviewWakeDependencies(o, review); err != nil {
		return err
	}
	task, err := o.buildReviewWakePipelineTask(ctx, review)
	if err != nil {
		return err
	}
	return o.bus.Publish(guide.TopicGuideRequests, reviewWakeMessage(task, review, o.config.AgentID))
}

func validateReviewWakeDependencies(o *Orchestrator, review *coordination.Review) error {
	if o == nil || review == nil {
		return fmt.Errorf("review wakeup dependencies unavailable")
	}
	if o.bus == nil || o.coordination == nil {
		return fmt.Errorf("review wakeup dependencies unavailable")
	}
	return nil
}

func reviewWakeMessage(task *PipelineTask, review *coordination.Review, sourceAgentID string) *guide.Message {
	req := &guide.RouteRequest{
		CorrelationID:   "coord_review_" + generateMessageID(),
		Input:           encodeTaskInput(task),
		TargetAgentID:   routeTargetAgentID(task),
		ExplicitTarget:  true,
		SourceAgentID:   sourceAgentID,
		SourceAgentName: "orchestrator",
		FireAndForget:   true,
		SessionID:       task.SessionID,
		Timestamp:       time.Now(),
		Metadata: map[string]any{
			"task_id":                    task.TaskID,
			"dag_id":                     task.DAGID,
			"node_id":                    task.NodeID,
			"agent_type":                 task.AgentType,
			"review_id":                  strings.TrimSpace(review.ID),
			"artifact_id":                strings.TrimSpace(review.ArtifactID),
			"coordination_review_wakeup": true,
		},
	}
	msg := guide.NewRequestMessage(generateMessageID(), req)
	msg.Metadata = map[string]any{
		"pipeline_task":              true,
		"task_id":                    task.TaskID,
		"review_id":                  strings.TrimSpace(review.ID),
		"artifact_id":                strings.TrimSpace(review.ArtifactID),
		"coordination_review_wakeup": true,
	}
	return msg
}

func (o *Orchestrator) buildReviewWakePipelineTask(
	ctx context.Context,
	review *coordination.Review,
) (*PipelineTask, error) {
	viewResult, err := o.coordination.QueryView(ctx, coordination.QueryViewInput{
		TaskID:     strings.TrimSpace(review.TaskID),
		WorkerType: strings.TrimSpace(review.ReviewerType),
	})
	if err != nil {
		return nil, fmt.Errorf("load coordination view for review wake: %w", err)
	}
	if viewResult == nil {
		return nil, fmt.Errorf("coordination view unavailable for review wake")
	}
	artifact := coordinationArtifactByID(viewResult.View.Artifacts, review.ArtifactID)
	contextData := reviewWakeContextData(viewResult, review, artifact)
	return &PipelineTask{
		NodeID:        reviewWakeNodeID(review),
		DAGID:         coordinationReviewWakeDAGID,
		TaskID:        strings.TrimSpace(review.TaskID),
		AgentType:     strings.TrimSpace(review.ReviewerType),
		TargetAgentID: pipelineWorkerTargetAgentID(o.config.SessionID, review.TaskID, review.ReviewerType),
		Prompt:        reviewWakePrompt(review, artifact),
		Context:       contextData,
		SessionID:     o.config.SessionID,
	}, nil
}

func reviewWakeContextData(
	viewResult *coordination.QueryViewResult,
	review *coordination.Review,
	artifact *coordination.Artifact,
) map[string]any {
	contextData := map[string]any{
		"pipeline_stage":    "review",
		"agent_type":        strings.TrimSpace(review.ReviewerType),
		"task_name":         firstNonEmptyTaskName(viewResult.View.TaskName),
		"review_request":    review,
		"coordination_view": viewResult.View,
	}
	if viewResult.Packet != nil {
		contextData["coordination_packet"] = viewResult.Packet
	}
	if artifact != nil {
		contextData["review_artifact"] = artifact
		addReviewWakeWorkspaceScope(contextData, *artifact)
	}
	return contextData
}

func reviewWakeNodeID(review *coordination.Review) string {
	if review == nil {
		return "coord_review"
	}
	if trimmed := sanitizePipelineIdentityPart(review.ID); trimmed != "" {
		return "coord_review_" + trimmed
	}
	return "coord_review"
}

func reviewWakePrompt(review *coordination.Review, artifact *coordination.Artifact) string {
	if review == nil {
		return "Consume the pending coordination review from the task ledger and resolve it when the requested work is complete."
	}
	lines := []string{
		"A pending coordination review for this task requires your attention.",
		"Review the task-scoped ledger, inspect the relevant evidence, address the request, and resolve the review when the requested work is actually complete.",
	}
	lines = appendReviewWakeLine(lines, "Review request: ", review.Summary)
	if artifact != nil {
		lines = appendReviewWakeLine(lines, "Artifact kind: ", artifact.Kind)
		lines = appendReviewWakeLine(lines, "Artifact summary: ", artifact.Summary)
	}
	return strings.Join(lines, "\n")
}

func appendReviewWakeLine(lines []string, prefix, value string) []string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return append(lines, prefix+trimmed)
	}
	return lines
}

func coordinationArtifactByID(artifacts []coordination.Artifact, artifactID string) *coordination.Artifact {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return nil
	}
	for i := range artifacts {
		if strings.TrimSpace(artifacts[i].ID) == artifactID {
			return &artifacts[i]
		}
	}
	return nil
}

func addReviewWakeWorkspaceScope(contextData map[string]any, artifact coordination.Artifact) {
	paths := coordinationArtifactPaths(artifact)
	if len(paths) == 0 {
		return
	}
	contextData["affected_files"] = reviewWakeAffectedFiles(paths)
	contextData["workspace"] = map[string]any{
		"read_set":  append([]string(nil), paths...),
		"write_set": append([]string(nil), paths...),
	}
}

func reviewWakeAffectedFiles(paths []string) []map[string]any {
	files := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			files = append(files, map[string]any{
				"path":      trimmed,
				"operation": "modify",
				"reason":    "pending coordination review scope",
			})
		}
	}
	return files
}

func coordinationArtifactPaths(artifact coordination.Artifact) []string {
	paths := make([]string, 0, len(artifact.Evidence)+4)
	for _, evidence := range artifact.Evidence {
		if strings.EqualFold(strings.TrimSpace(evidence.Kind), "file") && strings.TrimSpace(evidence.Value) != "" {
			paths = append(paths, strings.TrimSpace(evidence.Value))
		}
	}
	paths = append(paths, artifactPayloadPaths(artifact.Payload)...)
	return uniqueNonEmptyPaths(paths)
}

func artifactPayloadPaths(payload map[string]any) []string {
	if len(payload) == 0 {
		return nil
	}
	paths := []string{
		stringMapValue(payload, "file"),
		stringMapValue(payload, "path"),
		stringMapValue(payload, "output_file"),
	}
	paths = append(paths, stringListMapValue(payload, "files")...)
	paths = append(paths, stringListMapValue(payload, "files_changed")...)
	paths = append(paths, stringListMapValue(payload, "affected_files")...)
	return uniqueNonEmptyPaths(paths)
}

func stringMapValue(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func stringListMapValue(values map[string]any, key string) []string {
	if len(values) == 0 {
		return nil
	}
	switch typed := values[key].(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		return stringListFromAnySlice(typed)
	default:
		return nil
	}
}

func stringListFromAnySlice(values []any) []string {
	items := make([]string, 0, len(values))
	for _, entry := range values {
		if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
			items = append(items, strings.TrimSpace(text))
		}
	}
	return items
}

func uniqueNonEmptyPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		if !appendUniqueNonEmptyPath(&unique, seen, path) {
			continue
		}
	}
	return unique
}

func appendUniqueNonEmptyPath(unique *[]string, seen map[string]struct{}, path string) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	if _, ok := seen[trimmed]; ok {
		return false
	}
	seen[trimmed] = struct{}{}
	*unique = append(*unique, trimmed)
	return true
}
