package shared

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/google/uuid"
)

const yieldedToolArtifactOutputMax = 4096

// CompleteYieldedToolFromContinuation closes the original tool call
// that yielded for a consult/challenge continuation. The resume path
// injects output as the LLM-facing tool result; this helper mirrors
// that result onto the existing tool-call and claims-artifact
// lifecycles so the terminal UI sees the yielded row complete through
// the same channels as ordinary tools.
func CompleteYieldedToolFromContinuation(
	ctx context.Context,
	snapshot *TurnSnapshot,
	results map[string]*claims.ConsultResolvedDelta,
	output string,
) bool {
	if snapshot == nil {
		return false
	}
	call := yieldedToolCallFromSnapshot(snapshot)
	if strings.TrimSpace(call.ID) == "" {
		return false
	}
	acc := claims.AccumulatorFromContext(ctx)
	started := yieldedToolStartedArtifact(snapshot, acc, call)
	if started == nil || strings.TrimSpace(started.ID) == "" {
		return false
	}
	startedID := strings.TrimSpace(started.ID)
	if yieldedToolAlreadyCompleted(snapshot, acc, startedID) {
		return false
	}

	success, outcome, errorMsg := yieldedToolCompletionOutcome(results)
	startedAt := yieldedToolStartedAt(started, snapshot.AccumulatorState.Started)
	completedAt := time.Now().UTC()
	duration := completedAt.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	agentID := yieldedToolAgentID(snapshot, started, acc)
	toolName := strings.TrimSpace(call.Name)
	if toolName == "" {
		toolName = strings.TrimSpace(snapshot.AwaitToolName)
	}

	_ = EmitToolCall(ctx, ToolCallEvent{
		ToolCallKey: call.ID,
		Phase:       ToolCallComplete,
		ToolName:    toolName,
		ArgsSummary: SummarizeToolArgs(toolName, call.Arguments),
		FullArgs:    PrettyPrintArgs(call.Arguments),
		Output:      TruncateOutput(output, maxOutputBytes),
		AgentID:     agentID,
		StartedAt:   startedAt,
		Duration:    duration,
		Success:     success,
		ErrorMsg:    errorMsg,
		InterAgent:  yieldedToolInterAgentCompletion(toolName, results, success, errorMsg),
	})

	if acc == nil {
		return true
	}
	metadata := map[string]any{
		"outcome":     outcome,
		"call_id":     call.ID,
		"tool_name":   toolName,
		"started_at":  startedAt.UTC().Format(time.RFC3339Nano),
		"ended_at":    completedAt.Format(time.RFC3339Nano),
		"duration_ms": duration.Milliseconds(),
	}
	if trimmed := strings.TrimSpace(output); trimmed != "" {
		metadata["output"] = truncateYieldedToolOutput(trimmed, yieldedToolArtifactOutputMax)
	}
	if errorMsg != "" {
		metadata["error"] = errorMsg
	}
	acc.RecordArtifact(&claims.Artifact{
		ID:        uuid.NewString(),
		AgentID:   agentID,
		Kind:      "tool_completed",
		Reference: toolName,
		Metadata:  metadata,
		Relations: []claims.Relation{{
			Related:      startedID,
			RelatedType:  claims.RelatedTypeArtifact,
			Relationship: claims.RelationshipCompletes,
		}},
		Ephemeral: true,
	})
	return true
}

func yieldedToolCallFromSnapshot(snapshot *TurnSnapshot) providers.ToolCall {
	callID := strings.TrimSpace(snapshot.AwaitToolCallID)
	toolName := strings.TrimSpace(snapshot.AwaitToolName)
	if snapshot.Request != nil {
		for i := len(snapshot.Request.Messages) - 1; i >= 0; i-- {
			msg := snapshot.Request.Messages[i]
			for j := range msg.ToolCalls {
				call := msg.ToolCalls[j]
				if strings.TrimSpace(call.ID) == callID {
					if strings.TrimSpace(call.Name) == "" {
						call.Name = toolName
					}
					return call
				}
			}
		}
	}
	return providers.ToolCall{ID: callID, Name: toolName}
}

func yieldedToolStartedArtifact(snapshot *TurnSnapshot, acc *claims.TestamentAccumulator, call providers.ToolCall) *claims.Artifact {
	if acc != nil {
		if art := findYieldedToolStartedArtifact(acc.Artifacts(), call); art != nil {
			return art
		}
	}
	return findYieldedToolStartedArtifact(snapshot.AccumulatorState.Artifacts, call)
}

func findYieldedToolStartedArtifact(artifacts []*claims.Artifact, call providers.ToolCall) *claims.Artifact {
	callID := strings.TrimSpace(call.ID)
	toolName := strings.TrimSpace(call.Name)
	var fallback *claims.Artifact
	for i := len(artifacts) - 1; i >= 0; i-- {
		art := artifacts[i]
		if art == nil || strings.TrimSpace(art.Kind) != "tool_started" {
			continue
		}
		if yieldedToolMetadataString(art.Metadata, "call_id") != callID {
			continue
		}
		if toolName != "" {
			if got := yieldedToolMetadataString(art.Metadata, "tool_name"); got != "" && got != toolName {
				if fallback == nil {
					fallback = art
				}
				continue
			}
		}
		return art
	}
	return fallback
}

func yieldedToolAlreadyCompleted(snapshot *TurnSnapshot, acc *claims.TestamentAccumulator, startedID string) bool {
	if startedID == "" {
		return false
	}
	if acc != nil && yieldedToolArtifactsContainCompletion(acc.Artifacts(), startedID) {
		return true
	}
	return yieldedToolArtifactsContainCompletion(snapshot.AccumulatorState.Artifacts, startedID)
}

func yieldedToolArtifactsContainCompletion(artifacts []*claims.Artifact, startedID string) bool {
	for _, art := range artifacts {
		if art == nil || strings.TrimSpace(art.Kind) != "tool_completed" {
			continue
		}
		if claims.CompletesArtifactID(art.Relations) == startedID {
			return true
		}
		if yieldedToolMetadataString(art.Metadata, "started_artifact_id", "start_artifact_id", "completes") == startedID {
			return true
		}
	}
	return false
}

func yieldedToolStartedAt(started *claims.Artifact, fallback time.Time) time.Time {
	if started != nil {
		if value := yieldedToolMetadataString(started.Metadata, "started_at"); value != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
				return parsed.UTC()
			}
		}
		if !started.Created.IsZero() {
			return started.Created.UTC()
		}
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}

func yieldedToolAgentID(snapshot *TurnSnapshot, started *claims.Artifact, acc *claims.TestamentAccumulator) string {
	if started != nil {
		if agentID := strings.TrimSpace(started.AgentID); agentID != "" {
			return agentID
		}
	}
	if acc != nil {
		if agentID := strings.TrimSpace(acc.AgentID()); agentID != "" {
			return agentID
		}
	}
	return strings.TrimSpace(snapshot.AccumulatorState.AgentID)
}

func yieldedToolCompletionOutcome(results map[string]*claims.ConsultResolvedDelta) (success bool, outcome, errorMsg string) {
	if len(results) == 0 {
		return false, "failure", "no consult results received"
	}
	outcome = "success"
	for id, result := range results {
		if result == nil {
			outcome = yieldedToolWorseOutcome(outcome, "failure")
			if errorMsg == "" {
				errorMsg = fmt.Sprintf("consult %s resolved with nil result", id)
			}
			continue
		}
		switch strings.TrimSpace(result.Status) {
		case "", claims.ConsultStatusCompleted:
			continue
		case claims.ConsultStatusCancelled:
			outcome = yieldedToolWorseOutcome(outcome, "cancelled")
			if errorMsg == "" {
				errorMsg = firstYieldedToolString(result.ErrorMessage, "consult cancelled")
			}
		case claims.ConsultStatusTimeout:
			outcome = yieldedToolWorseOutcome(outcome, "timeout")
			if errorMsg == "" {
				errorMsg = firstYieldedToolString(result.ErrorMessage, "consult timed out")
			}
		case claims.ConsultStatusError:
			outcome = yieldedToolWorseOutcome(outcome, "failure")
			if errorMsg == "" {
				errorMsg = firstYieldedToolString(result.ErrorMessage, result.ResponseSummary, "consult failed")
			}
		default:
			outcome = yieldedToolWorseOutcome(outcome, "failure")
			if errorMsg == "" {
				errorMsg = "consult resolved with unknown status: " + result.Status
			}
		}
	}
	return outcome == "success", outcome, errorMsg
}

func yieldedToolInterAgentCompletion(
	toolName string,
	results map[string]*claims.ConsultResolvedDelta,
	success bool,
	errorMsg string,
) *InterAgentToolEvent {
	kind := ""
	switch strings.TrimSpace(toolName) {
	case "consult_peer":
		kind = InterAgentToolEventKindConsult
	case "challenge_peer":
		kind = InterAgentToolEventKindChallenge
	default:
		return nil
	}
	status := InterAgentToolEventStatusDone
	if !success {
		status = InterAgentToolEventStatusFailed
	}
	return &InterAgentToolEvent{
		Kind:       kind,
		AgentTypes: yieldedToolResponderAgentTypes(results),
		Summary:    yieldedToolResultSummary(results, errorMsg),
		Status:     status,
	}
}

func yieldedToolResponderAgentTypes(results map[string]*claims.ConsultResolvedDelta) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		agentID := strings.TrimSpace(result.ResponderAgentID)
		if agentID == "" {
			continue
		}
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		out = append(out, agentID)
	}
	return out
}

func yieldedToolResultSummary(results map[string]*claims.ConsultResolvedDelta, errorMsg string) string {
	if strings.TrimSpace(errorMsg) != "" {
		return strings.TrimSpace(errorMsg)
	}
	for _, result := range results {
		if result == nil {
			continue
		}
		if summary := strings.TrimSpace(result.ResponseSummary); summary != "" {
			return summary
		}
	}
	return ""
}

func yieldedToolWorseOutcome(current, next string) string {
	rank := map[string]int{
		"success":   0,
		"timeout":   1,
		"failure":   2,
		"cancelled": 3,
	}
	if rank[next] > rank[current] {
		return next
	}
	return current
}

func firstYieldedToolString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func yieldedToolMetadataString(md map[string]any, keys ...string) string {
	if md == nil {
		return ""
	}
	for _, key := range keys {
		switch value := md[key].(type) {
		case string:
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		case fmt.Stringer:
			if trimmed := strings.TrimSpace(value.String()); trimmed != "" {
				return trimmed
			}
		case int:
			return strconv.Itoa(value)
		case int64:
			return strconv.FormatInt(value, 10)
		case float64:
			return strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
	return ""
}

func truncateYieldedToolOutput(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "...(truncated)"
}
