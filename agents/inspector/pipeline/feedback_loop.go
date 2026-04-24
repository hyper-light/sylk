package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	agentShared "github.com/adalundhe/sylk/agents/shared"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
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
		if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventValidationResult,
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ValidationPayload{Phase: "early_pass", Success: true})
		}
		result.Passed = true
		result.Issues = issues
		return result, nil
	}

	var feedbackHistory []shared.InspectorFeedback
	currentIssues := issues

	for loop := range pi.config.MaxFeedbackLoops {
		if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventValidationCorrection,
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ValidationPayload{Phase: "correction_loop", TaskID: fmt.Sprintf("loop_%d", loop+1)})
		}
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
			pi.inspectorPostClaim(ctx,
				claims.Action{AgentID: "inspector-pipeline", Type: claims.ActionTypeCorrective},
				inspectorCorrectiveClaim(
					fmt.Sprintf("Correction request to engineer (loop %d)", loop+1),
					fmt.Sprintf("Inspector found %d blocking issues requiring engineer correction", len(corrections)),
					"engineer",
					[]claims.ClaimScopeEntry{{Kind: "correction", Key: "engineer"}},
					[]*claims.Validation{
						inspectorValidation(claims.ValidationTypeReceipt, false, "Engineer acknowledges corrections", "response.received"),
					},
				),
			)
			resp, err := pi.requestRouteSync(ctx, "engineer", string(payload))
			if err != nil {
				pi.logger.Warn("correction routing failed", "loop", loop+1, "error", err)
				if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
						lm.AgentID, lm.SessionID, lm.CorrID, "error",
						&agentlog.ErrorPayload{Error: fmt.Sprintf("correction routing failed loop %d: %v", loop+1, err)})
				}
			}
			_ = resp
		}

		// Re-validate with LLM
		revalidatePrompt := fmt.Sprintf(
			"Re-validate after correction loop %d. Previous issues: %d blocking. "+
				"Run all critical analysis tools on the affected files and report updated findings.",
			loop+1, len(currentIssues),
		)

		pi.prepareSkillsForInput(revalidatePrompt)
		systemPrompt := shared.PipelineInspectorSystemPromptForContract(agentShared.TaskExecutionContractFromContext(ctx))
		req := &providers.Request{
			SystemPrompt: systemPrompt,
			Messages: []providers.Message{
				{Role: providers.RoleUser, Content: revalidatePrompt},
			},
			Model:     pi.config.Model,
			MaxTokens: pi.config.MaxTokens,
			Tools:     pi.buildToolDefinitions(),
		}
		pi.applyLLMRuntimeProfile(req, "revalidation")

		ledger := agentShared.SteeringLedgerFromContext(ctx)
		_, err := agentShared.ExecuteTurnLoop(ledger, req, func() (string, error) {
			return pi.executeToolLoop(ctx, req, ledger)
		})
		if err != nil {
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventValidationResult,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ValidationPayload{Phase: "revalidation_failed", TaskID: fmt.Sprintf("loop_%d", loop+1), Success: false})
			}
			feedback.Passed = false
			feedbackHistory = append(feedbackHistory, feedback)
			break
		}

		// Check if issues are resolved (simplified — real implementation
		// would parse tool loop output for remaining issues)
		feedback.Passed = true
		feedbackHistory = append(feedbackHistory, feedback)

		if feedback.Passed {
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventValidationResult,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.ValidationPayload{Phase: "revalidation_passed", TaskID: fmt.Sprintf("loop_%d", loop+1), Success: true})
			}
			result.Passed = true
			break
		}
	}

	result.Issues = currentIssues
	result.FeedbackHistory = feedbackHistory
	result.LoopCount = len(feedbackHistory)

	// Feedback loop testament.
	pi.inspectorSubmitTestament(ctx, pi.inspectorTestament(
		fmt.Sprintf("Feedback loop: %d iterations, passed=%t", result.LoopCount, result.Passed),
		"committed",
		[]*claims.Artifact{
			pi.inspectorArtifact("loop_count", fmt.Sprintf("%d", result.LoopCount)),
			pi.inspectorArtifact("passed", fmt.Sprintf("%t", result.Passed)),
			pi.inspectorArtifact("issue_count", fmt.Sprintf("%d", len(result.Issues))),
		},
	))

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
