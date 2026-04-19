package global

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// executeToolLoop runs the model tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the orchestrator tool_loop.go pattern exactly.
func (gt *GlobalTester) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	seen := make(map[agentshared.ToolCallSignature]int, gt.config.MaxToolRuns)
	consecutiveErrors := 0
	reportShapeGraceTurns := 0

	provider := gt.getProvider()
	if provider == nil {
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: "no LLM provider configured"})
		}
		return "", fmt.Errorf("global tester: %w: LLM provider not yet wired", agentshared.ErrAgentNotReady)
	}

	for turn := 0; turn <= gt.config.MaxToolRuns+reportShapeGraceTurns; turn++ {
		if gt.toolDefsDirty {
			req.Tools = gt.buildToolDefinitions()
			gt.toolDefsDirty = false
		}

		// ── STEERING CHECKPOINT ──
		sc := agentshared.DrainAndCheckpoint(ledger, req, turn, "testing", nil)
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				action := "rollback"
				if sc.EditReplay != nil {
					action = "edit_replay"
				}
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventSteeringCheckpoint,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("%s at turn %d", action, cp.Turn)})
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: sc.EditText})
			}
			turn = cp.Turn
			seen = make(map[agentshared.ToolCallSignature]int, gt.config.MaxToolRuns)
			consecutiveErrors = 0
			continue
		}
		if sc.ShouldPause {
			if err := ledger.WaitForResume(ctx); err != nil {
				return "", err
			}
			continue
		}
		// ── END STEERING ──

		// ── CONTEXT BUDGET ──
		if err := agentshared.ApplyContextBudget(ctx, turn, gt.config.MaxToolRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := agentshared.CompleteWithWatchdog(ctx, provider, req, agentshared.AgentDisplayName("tester"))
		if err != nil {
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			return "", fmt.Errorf("global tester llm: %w", err)
		}

		if gov := agentshared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}
		agentshared.PublishIntermediateToolTurn(gt.bus, gt.channels, ctx, gt.id, resp)

		if len(resp.ToolCalls) == 0 {
			if err := agentshared.ValidateGlobalReviewCompletion(ctx, "tester"); err != nil {
				gt.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				req.Messages = append(req.Messages, providers.Message{
					Role:     providers.RoleAssistant,
					Content:  strings.TrimSpace(resp.Content),
					Metadata: resp.ProviderMetadata,
				})
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser,
					Content: err.Error() +
						"\nUse the global review protocol now. End ordinary top-level turns with handoff_next and active challenge turns with validate_work instead of ending narratively.",
				})
				continue
			}
			// Enforce the tester report shape contract on the terminal text.
			// Same mechanism as the pipeline tester: re-prompt up to a bounded
			// grace budget if the model emits unstructured prose, then accept
			// the model's output rather than holding the user's turn hostage.
			if err := agentshared.ValidateAgentReportShape(resp.Content, agentshared.TesterTurnReportShapeProfile); err != nil {
				if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.ErrorPayload{Error: fmt.Sprintf("report_shape_invalid: %v", err)})
				}
				gt.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				req.Messages = append(req.Messages, providers.Message{
					Role:     providers.RoleAssistant,
					Content:  strings.TrimSpace(resp.Content),
					Metadata: resp.ProviderMetadata,
				})
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser,
					Content: err.Error() +
						"\nRe-emit your terminal response strictly using the report format defined in the system prompt: start with a `## Tester Turn Report` heading, then include the required sections (`### Summary`, `### Findings`, `### Next`) using bullet/numbered lists and code spans for paths. Do not write narrative prose paragraphs.",
				})
				reportShapeGraceTurns = agentshared.ExtendReportShapeGrace(reportShapeGraceTurns)
				continue
			}
			gt.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventSuiteCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.TestSuitePayload{Phase: "tool_loop_completed"})
			}
			return strings.TrimSpace(resp.Content), nil
		}

		if err := gt.toolRuntime().ValidateBatch(gt.toolInvocations(ctx, resp.ToolCalls)); err != nil {
			return "", err
		}

		if dup, sig := agentshared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s", sig.Name)})
			}
			return "", fmt.Errorf("global tester repeated tool call: %s", sig.Name)
		}

		errCount, rerouted, delegated, delegatedMessage := gt.applyToolCalls(ctx, req, resp)
		gt.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if agentshared.GlobalReviewTurnTerminated(ctx) {
			return "", nil
		}
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		if delegated {
			return strings.TrimSpace(delegatedMessage), nil
		}
		consecutiveErrors = agentshared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", consecutiveErrors)})
			}
			return "", fmt.Errorf("global tester tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("global tester exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
func (gt *GlobalTester) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
) (int, bool, bool, string) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	errCount := 0
	rerouted := false
	delegated := false
	delegatedMessage := ""
	for idx, call := range resp.ToolCalls {
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := agentshared.WithActiveToolCall(ctx, call)
		result, err := agentshared.TimedToolCall(execCtx, "tester", call, func() (string, error) {
			execResult, execErr = gt.executeToolCall(execCtx, call)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			gt.toolDefsDirty = true
		}
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
			} else if errors.Is(err, skills.ErrDelegatedRequested) {
				delegated = true
				delegatedMessage = agentshared.DelegatedToolMessage(result, err)
			} else {
				result = agentshared.ToolErrorPayload(err)
				isError = true
				if agentshared.ToolErrorCountsTowardAbort(err) {
					errCount++
				}
				if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventSkillFailed,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.ToolPayload{ToolName: call.Name, Success: false, Err: err.Error()})
				}
			}
		}
		if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentshared.LogAgentEvent(lm.EventLogger, agentshared.ToolNameToEventType(call.Name),
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
		}
		if gov := agentshared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		// Activity Fabric ambient_context envelope.
		result = agentshared.AppendAmbientContext(ctx, agentshared.AmbientEnvelopeConfig{
			SessionID: func() string { return gt.config.SessionID },
			AgentID:   func() string { return gt.id },
			AgentType: func() string { return "tester" },
		}, result)
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if rerouted || delegated || agentshared.GlobalReviewTurnTerminated(ctx) {
			agentshared.AppendSkippedToolResults(req, resp.ToolCalls[idx+1:], "a previous tool call in this assistant turn already completed or redirected the global review decision")
			break
		}
	}
	return errCount, rerouted, delegated, delegatedMessage
}

// executeToolCall invokes a skill by name with JSON arguments.
func (gt *GlobalTester) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}

	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool arguments for %q are not valid JSON", name)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool arguments for %q are not valid JSON", name)
	}
	correlationID := agentshared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = gt.id + "-local"
	}
	result, err := gt.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         gt.toolRuntime().AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: gt.toolRuntime().CapabilityScope(),
	})
	if err == nil {
		agentshared.RecordGlobalExecutionSuccess(ctx, name, input, result.Output)
	}
	return result, err
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (gt *GlobalTester) prepareSkillsForInput(input string) {
	if gt.skillLoader == nil {
		return
	}
	gt.skillLoader.LoadForInput(input)
	gt.skillLoader.OptimizeForBudget()
}

// buildToolDefinitions converts loaded skills to provider tool format.
func (gt *GlobalTester) buildToolDefinitions() []providers.Tool {
	gt.toolRuntime().SyncActiveFromLoaded()
	return gt.toolRuntime().BuildToolDefinitions()
}

// --- Tool loop helpers (identical to orchestrator pattern) ---

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (gt *GlobalTester) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if gt.handoffBridge == nil {
		return
	}
	gt.handoffBridge.RecordTurn(agentshared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}

func (gt *GlobalTester) toolRuntime() *toolruntime.Runtime {
	return gt.tools
}

func (gt *GlobalTester) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	correlationID := agentshared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = gt.id + "-local"
	}
	scope := gt.toolRuntime().CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         gt.toolRuntime().AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}
