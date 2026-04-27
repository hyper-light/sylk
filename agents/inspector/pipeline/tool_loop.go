package pipeline

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

// executeToolLoop runs the LLM tool-call loop bounded by config.MaxToolRuns.
func (pi *PipelineInspector) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	return pi.executeToolLoopWithSurface(ctx, req, ledger, pi.toolRuntime())
}

func (pi *PipelineInspector) executeToolLoopWithSurface(
	ctx context.Context,
	req *providers.Request,
	ledger *steering.SteeringLedger,
	surface toolruntime.Surface,
) (string, error) {
	seen := make(map[shared.ToolCallSignature]int, pi.config.MaxToolRuns)
	consecutiveErrors := 0
	requiredActionGraceTurns := 0
	if surface == nil {
		surface = pi.toolRuntime()
	}

	for turn := 0; turn <= pi.config.MaxToolRuns+requiredActionGraceTurns; turn++ {
		if pi.toolDefsDirty {
			req.Tools = pi.buildToolDefinitionsWithSurface(surface)
			pi.toolDefsDirty = false
		}

		// ── STEERING CHECKPOINT ──
		sc := agentShared.DrainAndCheckpoint(ledger, req, turn, "inspecting", nil,
			agentShared.WithBoardProvider(func() *claims.ClaimsBoard { return pi.inspectorBoard() }, pi.id))
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
			seen = make(map[shared.ToolCallSignature]int, pi.config.MaxToolRuns)
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
		if err := agentShared.ApplyContextBudget(ctx, turn, pi.config.MaxToolRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := agentShared.CompleteWithWatchdog(ctx, pi.getProvider(), req, agentShared.AgentDisplayName("inspector-pipeline"))
		if err != nil {
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			return "", fmt.Errorf("pipeline inspector llm: %w", err)
		}

		if gov := agentShared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}

		shared.AccumulateUsage(ctx, &resp.Usage)
		agentShared.PublishIntermediateToolTurn(pi.bus, pi.channels, ctx, pi.id, resp)

		if len(resp.ToolCalls) == 0 {
			if err := agentShared.ValidateTaskExecutionCompletion(ctx, "inspector-pipeline"); err != nil {
				pi.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.ErrorPayload{Error: err.Error()})
				}
				req.Messages = append(req.Messages, providers.Message{
					Role:     providers.RoleAssistant,
					Content:  strings.TrimSpace(resp.Content),
					Metadata: resp.ProviderMetadata,
				})
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser,
					Content: err.Error() +
						"\nDo not conclude or release the scope yet. Continue the required inspection contract now.",
				})
				continue
			}
			pi.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventValidationResult,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.ValidationPayload{Phase: "tool_loop_completed", Success: true})
			}
			return strings.TrimSpace(resp.Content), nil
		}

		if err := surface.ValidateBatch(pi.toolInvocationsWithSurface(ctx, resp.ToolCalls, surface)); err != nil {
			return "", err
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s", sig.Name)})
			}
			return "", fmt.Errorf("pipeline inspector repeated tool call: %s", sig.Name)
		}

		errCount, controlErr, delegatedMessage := pi.applyToolCalls(ctx, req, resp, surface)
		pi.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if controlErr != nil {
			if errors.Is(controlErr, skills.ErrDelegatedRequested) {
				return strings.TrimSpace(delegatedMessage), nil
			}
			return "", controlErr
		}
		if agentShared.PipelineTurnTerminated(ctx) {
			return "", nil
		}
		requiredActionGraceTurns = agentShared.ExtendRequiredProtocolGrace(ctx, requiredActionGraceTurns)
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", consecutiveErrors)})
			}
			return "", fmt.Errorf("pipeline inspector tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := agentShared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		agentShared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("pipeline inspector exhausted tool-call loop")
}

func (pi *PipelineInspector) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	surface toolruntime.Surface,
) (int, error, string) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	errCount := 0
	var controlErr error
	delegatedMessage := ""
	for idx, call := range resp.ToolCalls {
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := agentShared.WithActiveToolCall(ctx, call)
		result, err := agentShared.TimedToolCall(execCtx, "inspector-pipeline", call, func() (string, error) {
			execResult, execErr = pi.executeToolCallWithSurface(execCtx, call, surface)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			pi.toolDefsDirty = true
		}
		isError := false
		if err != nil {
			switch {
			case errors.Is(err, skills.ErrRerouteRequested):
				controlErr = skills.ErrRerouteRequested
				result = `{"rerouted": true}`
			case errors.Is(err, skills.ErrDelegatedRequested):
				controlErr = err
				delegatedMessage = agentShared.DelegatedToolMessage(result, err)
			default:
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
			SessionID:  func() string { return pi.config.SessionID },
			AgentID:    func() string { return pi.id },
			AgentType:  func() string { return "inspector-pipeline" },
			PipelineID: func() string { return pi.pipelineID },
		}, result)
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if controlErr != nil || agentShared.PipelineTurnTerminated(ctx) {
			agentShared.AppendSkippedToolResults(req, resp.ToolCalls[idx+1:], "a previous tool call in this assistant turn already completed or redirected the pipeline decision")
			break
		}
	}
	return errCount, controlErr, delegatedMessage
}

func (pi *PipelineInspector) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	return pi.executeToolCallWithSurface(ctx, call, pi.toolRuntime())
}

func (pi *PipelineInspector) executeToolCallWithSurface(
	ctx context.Context,
	call providers.ToolCall,
	surface toolruntime.Surface,
) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}
	if surface == nil {
		surface = pi.toolRuntime()
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
	if err := agentShared.ValidateTaskExecutionCall(ctx, "inspector-pipeline", name, input); err != nil {
		return toolruntime.ExecutionResult{}, err
	}
	correlationID := agentShared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = pi.id + "-local"
	}
	result, err := surface.Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         surface.AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: surface.CapabilityScope(),
	})
	if err == nil {
		agentShared.RecordTaskExecutionSuccess(ctx, name, input, result.Output)
	}
	return result, err
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (pi *PipelineInspector) prepareSkillsForInput(input string) {
	if pi.skillLoader == nil {
		return
	}
	pi.skillLoader.LoadForInput(input)
	pi.skillLoader.OptimizeForBudget()
}

func (pi *PipelineInspector) buildToolDefinitions() []providers.Tool {
	return pi.buildToolDefinitionsWithSurface(pi.toolRuntime())
}

func (pi *PipelineInspector) buildToolDefinitionsWithSurface(surface toolruntime.Surface) []providers.Tool {
	if surface == nil {
		return nil
	}
	surface.SyncActiveFromLoaded()
	return surface.BuildToolDefinitions()
}

func (pi *PipelineInspector) toolRuntime() *toolruntime.Runtime {
	return pi.tools
}

func (pi *PipelineInspector) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	return pi.toolInvocationsWithSurface(ctx, calls, pi.toolRuntime())
}

func (pi *PipelineInspector) toolInvocationsWithSurface(
	ctx context.Context,
	calls []providers.ToolCall,
	surface toolruntime.Surface,
) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	if surface == nil {
		surface = pi.toolRuntime()
	}
	correlationID := agentShared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = pi.id + "-local"
	}
	scope := surface.CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         surface.AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (pi *PipelineInspector) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if pi.handoffBridge == nil {
		return
	}
	pi.handoffBridge.RecordTurn(agentShared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
