package engineer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
)

// RefactorLoop manages the bounded red/green refactor cycle between
// the Engineer and a Tester agent.
type RefactorLoop struct {
	engineer *Engineer
	config   shared.RefactorLoopConfig
	logger   *slog.Logger
}

// NewRefactorLoop creates a refactor loop bound to the given engineer.
func NewRefactorLoop(engineer *Engineer, config shared.RefactorLoopConfig, logger *slog.Logger) *RefactorLoop {
	if logger == nil {
		logger = slog.Default()
	}
	return &RefactorLoop{
		engineer: engineer,
		config:   config,
		logger:   logger,
	}
}

// Run executes the red/green refactor loop. On each iteration it requests
// tester validation and, if tests fail, re-enters the engineer's tool loop
// with structured feedback. Bounded by config.MaxIterations.
func (rl *RefactorLoop) Run(ctx context.Context, taskID, initialResult string) (string, error) {
	result := initialResult

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationStarted,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.GenerationPayload{Phase: "started"})
	}

	for iteration := range rl.config.MaxIterations {
		feedback, err := rl.requestTesterValidation(ctx, taskID, result)
		if err != nil {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: err.Error()})
			}
			rl.logger.Warn("tester validation failed, returning current result",
				"error", err, "iteration", iteration)
			return result, nil // Graceful degradation
		}

		if !feedback.ShouldContinueLoop(rl.config.MinQualityComposite) {
			rl.logger.Info("quality threshold met", "iteration", iteration,
				"composite", rl.compositeOrFallback(feedback))
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.GenerationPayload{Phase: "completed", ToolRuns: iteration})
			}
			return result, nil
		}

		// Red path: re-implement with feedback (quality below threshold)
		rl.logger.Info("quality below threshold, re-implementing",
			"iteration", iteration,
			"composite", rl.compositeOrFallback(feedback),
			"fail_count", feedback.FailCount,
			"pass_count", feedback.PassCount,
		)

		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationStarted,
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.GenerationPayload{Phase: "reimplementing"})
		}

		result, err = rl.reimplementWithFeedback(ctx, taskID, result, feedback)
		if err != nil {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: err.Error()})
			}
			return "", fmt.Errorf("refactor loop iteration %d: %w", iteration, err)
		}
	}

	return result, fmt.Errorf("refactor loop exhausted after %d iterations", rl.config.MaxIterations)
}

// requestTesterValidation sends the implementation to the tester via
// synchronous consultation and parses the structured feedback.
func (rl *RefactorLoop) requestTesterValidation(
	ctx context.Context,
	taskID, result string,
) (*shared.TestFeedback, error) {
	if rl.engineer.bus == nil || !rl.engineer.running {
		return nil, fmt.Errorf("engineer bus is unavailable")
	}

	query := fmt.Sprintf("Validate the following implementation for task %s:\n\n%s", taskID, result)

	ctx, cancel := context.WithTimeout(ctx, rl.config.TesterTimeout)
	defer cancel()

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventConsultationSent,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.ConsultPayload{Target: "tester-pipeline"})
	}
	evidence, err := rl.engineer.requestConsultation(ctx, "tester-pipeline", query, "", rl.engineer.config.SessionID)
	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventConsultationRecv,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.ConsultPayload{Target: "tester-pipeline", Success: err == nil})
	}
	if err != nil {
		return nil, fmt.Errorf("tester consultation: %w", err)
	}

	return parseTesterFeedback(evidence)
}

// reimplementWithFeedback builds a feedback prompt from test results and
// re-enters the engineer's tool loop.
func (rl *RefactorLoop) reimplementWithFeedback(
	ctx context.Context,
	taskID, previousResult string,
	feedback *shared.TestFeedback,
) (string, error) {
	feedbackPrompt := buildFeedbackPrompt(feedback)

	rl.engineer.prepareSkillsForInput(feedbackPrompt)
	req := &providers.Request{
		SystemPrompt: rl.engineer.config.SystemPrompt,
		Messages: []providers.Message{
			{Role: providers.RoleAssistant, Content: previousResult},
			{Role: providers.RoleUser, Content: feedbackPrompt},
		},
		Tools:     rl.engineer.buildToolDefinitions(),
		Model:     rl.engineer.config.EngineerConfig.Model,
		MaxTokens: rl.engineer.config.EngineerConfig.MaxTokens,
	}
	llmruntime.Apply(req, rl.engineer.llmRuntimeProfile())

	return rl.engineer.executeToolLoop(ctx, req, shared.SteeringLedgerFromContext(ctx))
}

func parseTesterFeedback(evidence *shared.ConsultationEvidence) (*shared.TestFeedback, error) {
	if evidence == nil {
		return nil, fmt.Errorf("nil consultation evidence")
	}
	if !evidence.Success {
		return nil, fmt.Errorf("tester consultation failed: %s", evidence.Error)
	}
	if evidence.Data == nil {
		return nil, fmt.Errorf("tester returned no data")
	}

	// Try direct type assertion first
	if fb, ok := evidence.Data.(*shared.TestFeedback); ok {
		return fb, nil
	}

	// Fall back to JSON round-trip
	data, err := json.Marshal(evidence.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal tester data: %w", err)
	}
	var feedback shared.TestFeedback
	if err := json.Unmarshal(data, &feedback); err != nil {
		return nil, fmt.Errorf("parse tester feedback: %w", err)
	}
	return &feedback, nil
}

func buildFeedbackPrompt(feedback *shared.TestFeedback) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("The following tests failed (%d passed, %d failed). Fix the issues and resubmit.\n\n",
		feedback.PassCount, feedback.FailCount))

	for i, f := range feedback.Failures {
		b.WriteString(fmt.Sprintf("Failure %d: %s\n", i+1, f.TestName))
		b.WriteString(fmt.Sprintf("  Error: %s\n", f.ErrorMsg))
		if f.File != "" {
			b.WriteString(fmt.Sprintf("  File: %s:%d\n", f.File, f.Line))
		}
		if f.Category != "" {
			b.WriteString(fmt.Sprintf("  Category: %s\n", f.Category))
		}
		if f.SuggestFix != "" {
			b.WriteString(fmt.Sprintf("  Suggestion: %s\n", f.SuggestFix))
		}
		b.WriteString("\n")
	}

	if feedback.Diagnosis != nil {
		b.WriteString("Diagnosis:\n")
		b.WriteString(fmt.Sprintf("  Root cause: %s\n", feedback.Diagnosis.RootCause))
		b.WriteString(fmt.Sprintf("  Category: %s\n", feedback.Diagnosis.Category))
		if feedback.Diagnosis.IsTestBug {
			b.WriteString("  Note: This may be a test bug, not an implementation bug.\n")
		}
		if feedback.Diagnosis.Suggestion != "" {
			b.WriteString(fmt.Sprintf("  Suggestion: %s\n", feedback.Diagnosis.Suggestion))
		}
	}

	// Include quality dimension scores when available
	if feedback.Quality != nil && len(feedback.Quality.Scores) > 0 {
		b.WriteString("\nQuality Scores:\n")
		for _, dim := range shared.AllQualityDimensions() {
			if score, ok := feedback.Quality.Scores[dim]; ok {
				b.WriteString(fmt.Sprintf("  %s: %.2f\n", dim, score))
			}
		}
		b.WriteString(fmt.Sprintf("  composite: %.2f\n", feedback.Quality.Composite))
		if feedback.Quality.Reasoning != "" {
			b.WriteString(fmt.Sprintf("  reasoning: %s\n", feedback.Quality.Reasoning))
		}

		// Include per-dimension issues
		for dim, issues := range feedback.Quality.Issues {
			for _, issue := range issues {
				b.WriteString(fmt.Sprintf("  [%s/%s] %s", dim, issue.Severity, issue.Description))
				if issue.SuggestFix != "" {
					b.WriteString(fmt.Sprintf(" — Fix: %s", issue.SuggestFix))
				}
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

func (rl *RefactorLoop) compositeOrFallback(feedback *shared.TestFeedback) float64 {
	if feedback.Quality != nil {
		return feedback.Quality.Composite
	}
	return feedback.Confidence
}
