package designer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/fabric"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// executeToolLoop runs the model tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.DesignerConfig.MaxToolRuns.
// After each Complete call it records a TurnRecord on the handoff bridge (if set)
// and accumulates usage for streaming.
func (d *Designer) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	return d.executeToolLoopWithSurface(ctx, req, ledger, d.toolRuntime())
}

func (d *Designer) executeToolLoopWithSurface(
	ctx context.Context,
	req *providers.Request,
	ledger *steering.SteeringLedger,
	surface toolruntime.Surface,
) (string, error) {
	seen := make(map[shared.ToolCallSignature]int, d.config.DesignerConfig.MaxToolRuns)
	consecutiveErrors := 0
	if surface == nil {
		surface = d.toolRuntime()
	}

	p := d.getProvider()
	if p == nil {
		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
				lm.AgentID, lm.SessionID, lm.CorrID, "error",
				&agentlog.ErrorPayload{Error: "no LLM provider configured"})
		}
		return "", fmt.Errorf("designer: %w: LLM provider not yet wired", shared.ErrAgentNotReady)
	}

	for turn := 0; turn <= d.config.DesignerConfig.MaxToolRuns; turn++ {
		if d.toolDefsDirty {
			req.Tools = d.buildToolDefinitionsWithSurface(surface)
			d.toolDefsDirty = false
		}

		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, "designing", nil)
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
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventDesignIteration,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.DesignPayload{Phase: action})
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: sc.EditText})
			}
			turn = cp.Turn
			seen = make(map[shared.ToolCallSignature]int, d.config.DesignerConfig.MaxToolRuns)
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
		if err := shared.ApplyContextBudget(ctx, turn, d.config.DesignerConfig.MaxToolRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()

		resp, err := shared.CompleteWithWatchdog(ctx, p, req, shared.AgentDisplayName("designer"))
		if err != nil {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			return "", fmt.Errorf("designer llm: %w", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}

		shared.AccumulateUsage(ctx, &resp.Usage)
		d.accumulateUsage(resp)
		shared.PublishIntermediateToolTurn(d.bus, d.channels, ctx, d.id, resp)

		if len(resp.ToolCalls) == 0 {
			if err := shared.ValidatePipelineProtocolCompletion(ctx, "designer"); err != nil {
				d.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				req.Messages = append(req.Messages, providers.Message{
					Role:     providers.RoleAssistant,
					Content:  strings.TrimSpace(resp.Content),
					Metadata: resp.ProviderMetadata,
				})
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser,
					Content: err.Error() +
						"\nIf the design criteria or tests are unclear, use challenge_agent against Inspector or Tester before ending the turn.",
				})
				continue
			}
			if err := shared.ValidateTaskExecutionCompletion(ctx, "designer"); err != nil {
				d.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
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
						"\nDo not conclude or release the scope yet. Continue the required task-scoped review or design work now.",
				})
				continue
			}
			d.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventDesignGenerated,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.DesignPayload{Phase: "completed", DurNs: time.Since(turnStart).Nanoseconds()})
			}
			return strings.TrimSpace(resp.Content), nil
		}

		if err := surface.ValidateBatch(d.toolInvocationsWithSurface(ctx, resp.ToolCalls, surface)); err != nil {
			return "", err
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s", sig.Name)})
			}
			return "", fmt.Errorf("designer repeated tool call: %s", sig.Name)
		}

		errCount, controlErr := d.applyToolCalls(ctx, req, resp, surface)

		d.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)

		if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
			shared.LogAgentEvent(lm.EventLogger, agentlog.EventDesignIteration,
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.DesignPayload{Phase: "iteration", DurNs: time.Since(turnStart).Nanoseconds()})
		}

		if controlErr != nil {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventDesignGenerated,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.DesignPayload{Phase: "rerouted"})
			}
			return "", controlErr
		}
		if shared.PipelineTurnTerminated(ctx) {
			return "", nil
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("tool calls failed %d consecutive turns", consecutiveErrors)})
			}
			return "", fmt.Errorf("designer tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("designer exhausted tool-call loop")
}

// applyToolCalls appends the assistant message (preserving ProviderMetadata for
// Google thought signatures) and tool results to the request.
func (d *Designer) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	surface toolruntime.Surface,
) (int, error) {
	req.Messages = append(req.Messages, providers.ToolLoopAssistantMessage(resp))

	errCount := 0
	var controlErr error
	for idx, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			break
		}
		var execResult toolruntime.ExecutionResult
		var execErr error
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "designer", call, func() (string, error) {
			execResult, execErr = d.executeToolCallWithSurface(execCtx, call, surface)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			d.toolDefsDirty = true
		}
		isError := false
		if err != nil {
			switch {
			case errors.Is(err, skills.ErrRerouteRequested):
				controlErr = skills.ErrRerouteRequested
				result = `{"rerouted": true}`
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
			shared.LogAgentEvent(lm.EventLogger, shared.ToolNameToEventType(call.Name),
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
		}
		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		// Activity Fabric ambient_context envelope.
		result = fabric.AppendAmbientContext(ctx, fabric.AmbientEnvelopeConfig{
			SessionID:  func() string { return d.config.SessionID },
			AgentID:    func() string { return d.id },
			AgentType:  func() string { return "designer" },
			PipelineID: func() string { return d.pipelineID },
		}, result)
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if controlErr != nil || shared.PipelineTurnTerminated(ctx) {
			shared.AppendSkippedToolResults(req, resp.ToolCalls[idx+1:], "a previous tool call in this assistant turn already completed or redirected the pipeline decision")
			break
		}
	}
	return errCount, controlErr
}

// executeToolCall invokes a skill by name with JSON arguments.
func (d *Designer) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	return d.executeToolCallWithSurface(ctx, call, d.toolRuntime())
}

func (d *Designer) executeToolCallWithSurface(
	ctx context.Context,
	call providers.ToolCall,
	surface toolruntime.Surface,
) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}
	if surface == nil {
		surface = d.toolRuntime()
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
	if err := shared.ValidateTaskExecutionCall(ctx, "designer", name, input); err != nil {
		return toolruntime.ExecutionResult{}, err
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = d.id + "-local"
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
func (d *Designer) prepareSkillsForInput(input string) {
	if d.skillLoader == nil {
		return
	}
	d.skillLoader.LoadForInput(input)
	d.skillLoader.OptimizeForBudget()
}

// buildToolDefinitions converts loaded skills to provider tool format.
func (d *Designer) buildToolDefinitions() []providers.Tool {
	return d.buildToolDefinitionsWithSurface(d.toolRuntime())
}

func (d *Designer) buildToolDefinitionsWithSurface(surface toolruntime.Surface) []providers.Tool {
	if surface == nil {
		return nil
	}
	surface.SyncActiveFromLoaded()
	return surface.BuildToolDefinitions()
}

func (d *Designer) toolRuntime() *toolruntime.Runtime {
	return d.tools
}

func (d *Designer) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	return d.toolInvocationsWithSurface(ctx, calls, d.toolRuntime())
}

func (d *Designer) toolInvocationsWithSurface(
	ctx context.Context,
	calls []providers.ToolCall,
	surface toolruntime.Surface,
) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	if surface == nil {
		surface = d.toolRuntime()
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = d.id + "-local"
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
func (d *Designer) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if d.handoffBridge == nil {
		return
	}

	d.handoffBridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
