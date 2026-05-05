package engineer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// executeToolLoop runs the LLM tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.EngineerConfig.MaxToolRuns.
// Follows the pipeline tester tool loop pattern exactly.
func (e *Engineer) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	return e.executeToolLoopWithSurface(ctx, req, ledger, e.toolRuntime())
}

func (e *Engineer) executeToolLoopWithSurface(
	ctx context.Context,
	req *providers.Request,
	ledger *steering.SteeringLedger,
	surface toolruntime.Surface,
) (string, error) {
	maxRuns := e.config.EngineerConfig.MaxToolRuns
	seen := make(map[shared.ToolCallSignature]int, maxRuns)
	consecutiveErrors := 0
	if surface == nil {
		surface = e.toolRuntime()
	}

	p := e.getProvider()
	if p == nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: "no LLM provider configured"})
		}
		return "", fmt.Errorf("engineer: %w: LLM provider not yet wired", shared.ErrAgentNotReady)
	}

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationStarted,
			lm.AgentID, lm.SessionID, lm.CorrID, "info",
			&agentlog.GenerationPayload{Phase: "started"})
	}

	for turn := 0; turn <= maxRuns; turn++ {
		if e.toolDefsDirty {
			req.Tools = e.buildToolDefinitionsWithSurface(surface)
			e.toolDefsDirty = false
		}

		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, "executing", nil,
			shared.WithBoardProvider(func() *claims.ClaimsBoard { return e.engineerBoard() }, e.id))
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				action := "rollback"
				if sc.EditReplay != nil {
					action = "edit_replay"
				}
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventSteeringCheckpoint,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("%s at turn %d", action, cp.Turn)})
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: sc.EditText})
			}
			turn = cp.Turn
			seen = make(map[shared.ToolCallSignature]int, maxRuns)
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
		if err := shared.ApplyContextBudget(ctx, turn, maxRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := shared.CompleteWithWatchdog(ctx, p, req, shared.AgentDisplayName("engineer"))
		if err != nil {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			shared.PostCorrectiveClaimFromContext(ctx, e.id, "LLM completion failed", fmt.Sprintf("engineer llm: %v", err), nil)
			return "", fmt.Errorf("engineer llm: %w", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}
		shared.AccumulateUsage(ctx, &resp.Usage)
		shared.PublishIntermediateToolTurn(e.bus, e.channels, ctx, e.id, resp)

		if len(resp.ToolCalls) == 0 {
			if err := shared.ValidateTaskExecutionCompletion(ctx, "engineer"); err != nil {
				e.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
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
						"\nDo not conclude or release the scope yet. Continue the required task-scoped review or implementation work now.",
				})
				continue
			}
			e.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventGenerationCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.GenerationPayload{Phase: "completed", ToolRuns: turn})
			}
			return strings.TrimSpace(resp.Content), nil
		}

		if err := surface.ValidateBatch(e.toolInvocationsWithSurface(ctx, resp.ToolCalls, surface)); err != nil {
			return "", err
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s", sig.Name)})
			}
			return "", fmt.Errorf("engineer repeated tool call: %s", sig.Name)
		}

		errCount, controlErr, delegatedMessage := e.applyToolCalls(ctx, req, resp, surface)
		e.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if controlErr != nil {
			if errors.Is(controlErr, skills.ErrDelegatedRequested) {
				if acc := claims.AccumulatorFromContext(ctx); acc != nil {
					acc.Record("delegation", truncateEngineer(delegatedMessage, 100))
					acc.Note("Delegated to peer agent")
				}
				return strings.TrimSpace(delegatedMessage), nil
			}
			return "", controlErr
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", consecutiveErrors)})
			}
			shared.PostCorrectiveClaimFromContext(ctx, e.id, "Consecutive tool errors", fmt.Sprintf("engineer tool calls failed %d consecutive turns", consecutiveErrors), nil)
			return "", fmt.Errorf("engineer tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("engineer exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
func (e *Engineer) applyToolCalls(
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
		if ctx.Err() != nil {
			break
		}
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "engineer", call, func() (string, error) {
			execResult, execErr = e.executeToolCallWithSurface(execCtx, call, surface)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			e.toolDefsDirty = true
		}
		isError := false
		if err != nil {
			switch {
			case shared.IsConsultYielded(err):
				// LLM yielded mid-turn awaiting peer consults.
				// Surface the sentinel so the outer loop exits
				// without appending a tool result message.
				return errCount, shared.ErrConsultYielded, delegatedMessage
			case errors.Is(err, skills.ErrRerouteRequested):
				controlErr = skills.ErrRerouteRequested
				result = `{"rerouted": true}`
			case errors.Is(err, skills.ErrDelegatedRequested):
				controlErr = err
				delegatedMessage = shared.DelegatedToolMessage(result, err)
			default:
				result = shared.ToolErrorPayload(err)
				isError = true
				if shared.ToolErrorCountsTowardAbort(err) {
					errCount++
				}
				if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
					shared.LogAgentEvent(lm.EventLogger, agentlog.EventSkillFailed,
						lm.AgentID, lm.SessionID, lm.CorrID, "warn",
						&agentlog.ToolPayload{ToolName: call.Name, Success: false, Err: err.Error()})
				}
			}
		}
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			toolEvent := shared.ToolNameToEventType(call.Name)
			shared.LogAgentEvent(lm.EventLogger, toolEvent,
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
		}
		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		// Activity Fabric ambient_context envelope.
		result = fabric.AppendAmbientContext(ctx, fabric.AmbientEnvelopeConfig{
			SessionID:  func() string { return e.config.SessionID },
			AgentID:    func() string { return e.id },
			AgentType:  func() string { return "engineer" },
			PipelineID: func() string { return e.pipelineID },
		}, result)
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if controlErr != nil {
			shared.AppendSkippedToolResults(req, resp.ToolCalls[idx+1:], "a previous tool call in this assistant turn already completed or redirected the pipeline decision")
			break
		}
	}
	return errCount, controlErr, delegatedMessage
}

// executeToolCall invokes a skill by name with JSON arguments.
func (e *Engineer) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	return e.executeToolCallWithSurface(ctx, call, e.toolRuntime())
}

func (e *Engineer) executeToolCallWithSurface(
	ctx context.Context,
	call providers.ToolCall,
	surface toolruntime.Surface,
) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}
	if surface == nil {
		surface = e.toolRuntime()
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
	if err := shared.ValidateTaskExecutionCall(ctx, "engineer", name, input); err != nil {
		return toolruntime.ExecutionResult{}, err
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = e.id + "-local"
	}
	result, err := surface.Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         surface.AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: surface.CapabilityScope(),
	})
	if err == nil {
		shared.RecordTaskExecutionSuccess(ctx, name, input, result.Output)
	}
	return result, err
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (e *Engineer) prepareSkillsForInput(input string) {
	if e.skillLoader == nil {
		return
	}
	e.skillLoader.LoadForInput(input)
	e.skillLoader.OptimizeForBudget()
}

// buildToolDefinitions converts loaded skills to provider Tool format.
func (e *Engineer) buildToolDefinitions() []providers.Tool {
	return e.buildToolDefinitionsWithSurface(e.toolRuntime())
}

func (e *Engineer) buildToolDefinitionsWithSurface(surface toolruntime.Surface) []providers.Tool {
	if surface == nil {
		return nil
	}
	surface.SyncActiveFromLoaded()
	return surface.BuildToolDefinitions()
}

func (e *Engineer) toolRuntime() *toolruntime.Runtime {
	return e.tools
}

func (e *Engineer) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	return e.toolInvocationsWithSurface(ctx, calls, e.toolRuntime())
}

func (e *Engineer) toolInvocationsWithSurface(
	ctx context.Context,
	calls []providers.ToolCall,
	surface toolruntime.Surface,
) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	if surface == nil {
		surface = e.toolRuntime()
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = e.id + "-local"
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
func (e *Engineer) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if e.handoffBridge == nil {
		return
	}
	e.handoffBridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
