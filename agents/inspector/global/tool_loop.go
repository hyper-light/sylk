package global

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

func (gi *GlobalInspector) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	seen := make(map[shared.ToolCallSignature]int, gi.config.MaxToolRuns)
	consecutiveErrors := 0
	requiredActionGraceTurns := 0

	p := gi.getProvider()
	if p == nil {
		return "", fmt.Errorf("global inspector: %w: LLM provider not yet wired", agentShared.ErrAgentNotReady)
	}

	for turn := 0; turn <= gi.config.MaxToolRuns+requiredActionGraceTurns; turn++ {
		if gi.toolDefsDirty {
			req.Tools = gi.buildToolDefinitions()
			gi.toolDefsDirty = false
		}

		// ── STEERING CHECKPOINT ──
		sc := agentShared.DrainAndCheckpoint(ledger, req, turn, "inspecting", nil,
			agentShared.WithBoardProvider(func() *claims.ClaimsBoard { return gi.globalInspectorBoardOrNil() }, gi.id))
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				action := "rollback"
				if sc.EditReplay != nil {
					action = "edit_replay"
				}
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventSteeringCheckpoint,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("%s at turn %d", action, cp.Turn)})
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: sc.EditText})
			}
			turn = cp.Turn
			seen = make(map[shared.ToolCallSignature]int, gi.config.MaxToolRuns)
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
		if err := agentShared.ApplyContextBudget(ctx, turn, gi.config.MaxToolRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := gi.runTurn(ctx, p, req)
		if err != nil {
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			return "", fmt.Errorf("global inspector llm: %w", err)
		}

		if gov := agentShared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}

		shared.AccumulateUsage(ctx, &resp.Usage)

		if len(resp.ToolCalls) == 0 {
			if auditDepthRequired(ctx) {
				gi.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				req.Messages = append(req.Messages, providers.Message{
					Role:     providers.RoleAssistant,
					Content:  strings.TrimSpace(resp.Content),
					Metadata: resp.ProviderMetadata,
				})
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser,
					Content: fmt.Sprintf(
						"Before any other global audit work, call `%s` first and use its returned depth for your own assessment and for any compatible knowledge consults.",
						determineAuditDepthToolName,
					),
				})
				continue
			}
			if err := agentShared.ValidateGlobalReviewCompletion(ctx, "inspector"); err != nil {
				gi.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				req.Messages = append(req.Messages, providers.Message{
					Role:     providers.RoleAssistant,
					Content:  strings.TrimSpace(resp.Content),
					Metadata: resp.ProviderMetadata,
				})
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser,
					Content: err.Error() +
						"\nUse the global review protocol now. If a challenge response arrived, call process_validation before choosing challenge_global_agent(target=…), handoff_next, global_review(action=finalize), or global_review(action=commit).",
				})
				requiredActionGraceTurns = agentShared.ExtendRequiredProtocolGrace(ctx, requiredActionGraceTurns)
				continue
			}
			gi.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventAuditCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.AuditPayload{Phase: "tool_loop_completed"})
			}
			return strings.TrimSpace(resp.Content), nil
		}

		if err := gi.toolRuntime().ValidateBatch(gi.toolInvocations(ctx, resp.ToolCalls)); err != nil {
			return "", err
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s", sig.Name)})
			}
			return "", fmt.Errorf("global inspector repeated tool call: %s", sig.Name)
		}

		errCount, rerouted, delegated, delegatedMessage := gi.applyToolCalls(ctx, req, resp)
		gi.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if agentShared.GlobalReviewTurnTerminated(ctx) {
			return "", nil
		}
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		if delegated {
			return strings.TrimSpace(delegatedMessage), nil
		}
		requiredActionGraceTurns = agentShared.ExtendRequiredProtocolGrace(ctx, requiredActionGraceTurns)
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", consecutiveErrors)})
			}
			return "", fmt.Errorf("global inspector tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("global inspector exhausted tool-call loop")
}

// runTurn streams one LLM turn. Thinking + text chunks reach the
// chat panel as they arrive; tool calls are surfaced by the helper
// via PublishIntermediateToolTurn. inspectorProvider requires the
// Stream method, so the compiler enforces this path — no runtime
// fallback.
func (gi *GlobalInspector) runTurn(ctx context.Context, p inspectorProvider, req *providers.Request) (*providers.Response, error) {
	return agentShared.StreamLLMTurn(ctx, agentShared.StreamingLLMTurnConfig{
		Bus:              gi.bus,
		Channels:         gi.channels,
		AgentID:          gi.id,
		AgentDisplayName: agentShared.AgentDisplayName("inspector"),
	}, p, req)
}

func (gi *GlobalInspector) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
) (int, bool, bool, string) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))
	initialDepthRequired := auditDepthRequired(ctx)
	if initialDepthRequired && len(resp.ToolCalls) > 0 && strings.TrimSpace(resp.ToolCalls[0].Name) != determineAuditDepthToolName {
		first := resp.ToolCalls[0]
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: first.ID,
			ToolName:   first.Name,
			Content:    shared.ToolErrorPayload(auditDepthGateViolation(resp)),
			IsError:    true,
		})
		agentShared.AppendSkippedToolResults(req, resp.ToolCalls[1:], "determine_audit_depth must run first on a new global audit branch")
		return 1, false, false, ""
	}

	errCount := 0
	rerouted := false
	delegated := false
	delegatedMessage := ""
	for idx, call := range resp.ToolCalls {
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := agentShared.WithActiveToolCall(ctx, call)
		result, err := agentShared.TimedToolCall(execCtx, "inspector", call, func() (string, error) {
			execResult, execErr = gi.executeToolCall(execCtx, call)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			gi.toolDefsDirty = true
		}
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
			} else if errors.Is(err, skills.ErrDelegatedRequested) {
				delegated = true
				delegatedMessage = agentShared.DelegatedToolMessage(result, err)
			} else {
				result = shared.ToolErrorPayload(err)
				isError = true
				if agentShared.ToolErrorCountsTowardAbort(err) {
					errCount++
				}
				if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventSkillFailed,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.ToolPayload{ToolName: call.Name, Success: false, Err: err.Error()})
				}
			}
		}
		if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			agentShared.LogAgentEvent(lm.EventLogger, agentShared.ToolNameToEventType(call.Name),
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
		}
		if gov := agentShared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		// Activity Fabric ambient_context envelope.
		result = fabric.AppendAmbientContext(ctx, fabric.AmbientEnvelopeConfig{
			SessionID: func() string { return gi.config.SessionID },
			AgentID:   func() string { return gi.id },
			AgentType: func() string { return "inspector" },
		}, result)
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if initialDepthRequired && idx == 0 && strings.TrimSpace(call.Name) == determineAuditDepthToolName && len(resp.ToolCalls) > 1 {
			agentShared.AppendSkippedToolResults(req, resp.ToolCalls[idx+1:], "wait for determine_audit_depth to resolve the branch audit contract before taking further audit actions")
			break
		}
		if rerouted || delegated || agentShared.GlobalReviewTurnTerminated(ctx) {
			agentShared.AppendSkippedToolResults(req, resp.ToolCalls[idx+1:], "a previous tool call in this assistant turn already completed or redirected the global review decision")
			break
		}
	}
	return errCount, rerouted, delegated, delegatedMessage
}

func (gi *GlobalInspector) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
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
	correlationID := agentShared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = gi.id + "-local"
	}
	return gi.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         gi.toolRuntime().AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: gi.toolRuntime().CapabilityScope(),
	})
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (gi *GlobalInspector) prepareSkillsForInput(input string) {
	if gi.skillLoader == nil {
		return
	}
	gi.skillLoader.LoadForInput(input)
	gi.skillLoader.OptimizeForBudget()
}

func (gi *GlobalInspector) buildToolDefinitions() []providers.Tool {
	gi.toolRuntime().SyncActiveFromLoaded()
	return gi.toolRuntime().BuildToolDefinitions()
}

func (gi *GlobalInspector) toolRuntime() *toolruntime.Runtime {
	return gi.tools
}

func (gi *GlobalInspector) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	correlationID := agentShared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = gi.id + "-local"
	}
	scope := gi.toolRuntime().CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         gi.toolRuntime().AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (gi *GlobalInspector) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if gi.handoffBridge == nil {
		return
	}
	gi.handoffBridge.RecordTurn(agentShared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
