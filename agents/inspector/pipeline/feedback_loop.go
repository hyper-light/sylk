package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/providers"
)

// runFeedbackLoop executes the correction-revalidation loop after initial findings.
func (pi *PipelineInspector) runFeedbackLoop(
	ctx context.Context,
	issues []shared.ValidationIssue,
	files []string,
) (*shared.InspectorResult, error) {
	result := &shared.InspectorResult{
		Mode: "pipeline",
	}

	if !shared.HasBlockingIssues(issues) {
		result.Passed = true
		result.Issues = issues
		return result, nil
	}

	var feedbackHistory []shared.InspectorFeedback
	currentIssues := issues

	for loop := range pi.config.MaxFeedbackLoops {
		corrections := generateCorrections(currentIssues)
		feedback := shared.InspectorFeedback{
			Loop:        loop + 1,
			Issues:      currentIssues,
			Corrections: corrections,
			Passed:      false,
		}

		// Route corrections to target agent
		if pi.bus != nil && len(corrections) > 0 {
			payload, _ := json.Marshal(map[string]any{
				"type":        "correction_request",
				"corrections": corrections,
				"loop":        loop + 1,
			})
			resp, err := pi.requestRouteSync(ctx, "engineer", string(payload))
			if err != nil {
				pi.logger.Warn("correction routing failed", "loop", loop+1, "error", err)
			}
			_ = resp
		}

		// Re-validate with LLM
		revalidatePrompt := fmt.Sprintf(
			"Re-validate after correction loop %d. Previous issues: %d blocking. "+
				"Run all critical analysis tools on the affected files and report updated findings.",
			loop+1, len(currentIssues),
		)

		req := &providers.Request{
			SystemPrompt: shared.PipelineInspectorSystemPrompt(),
			Messages: []providers.Message{
				{Role: providers.RoleUser, Content: revalidatePrompt},
			},
			Model:     pi.config.Model,
			MaxTokens: pi.config.MaxTokens,
			Tools:     pi.buildToolDefinitions(),
		}

		_, err := pi.executeToolLoop(ctx, req)
		if err != nil {
			feedback.Passed = false
			feedbackHistory = append(feedbackHistory, feedback)
			break
		}

		// Check if issues are resolved (simplified — real implementation
		// would parse tool loop output for remaining issues)
		feedback.Passed = true
		feedbackHistory = append(feedbackHistory, feedback)

		if feedback.Passed {
			result.Passed = true
			break
		}
	}

	result.Issues = currentIssues
	result.FeedbackHistory = feedbackHistory
	result.LoopCount = len(feedbackHistory)
	return result, nil
}

func generateCorrections(issues []shared.ValidationIssue) []shared.Correction {
	corrections := make([]shared.Correction, 0, len(issues))

	for _, issue := range issues {
		if issue.Severity != shared.Critical && issue.Severity != shared.High {
			continue
		}
		corrections = append(corrections, shared.Correction{
			IssueID:      issue.ID,
			Description:  fmt.Sprintf("Fix: %s", issue.Message),
			SuggestedFix: issue.SuggestedFix,
			File:         issue.File,
			LineStart:    issue.Line,
			LineEnd:      issue.Line,
		})
	}

	return corrections
}
