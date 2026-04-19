package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// executeToolLoop runs the model tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the orchestrator tool_loop.go pattern exactly.
func (pt *PipelineTester) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	return pt.executeToolLoopWithSurface(ctx, req, ledger, pt.toolRuntime())
}

func (pt *PipelineTester) executeToolLoopWithSurface(
	ctx context.Context,
	req *providers.Request,
	ledger *steering.SteeringLedger,
	surface toolruntime.Surface,
) (string, error) {
	seen := make(map[shared.ToolCallSignature]int, pt.config.MaxToolRuns)
	consecutiveErrors := 0
	reportShapeGraceTurns := 0
	if surface == nil {
		surface = pt.toolRuntime()
	}

	for turn := 0; turn <= pt.config.MaxToolRuns+reportShapeGraceTurns; turn++ {
		if pt.toolDefsDirty {
			req.Tools = pt.buildToolDefinitionsWithSurface(surface)
			pt.toolDefsDirty = false
		}

		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, "testing", nil)
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
			seen = make(map[shared.ToolCallSignature]int, pt.config.MaxToolRuns)
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
		if err := shared.ApplyContextBudget(ctx, turn, pt.config.MaxToolRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := shared.CompleteWithWatchdog(ctx, pt.getProvider(), req, shared.AgentDisplayName("tester-pipeline"))
		if err != nil {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("llm: %v", err)})
			}
			return "", fmt.Errorf("pipeline tester llm: %w", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}
		shared.AccumulateUsage(ctx, &resp.Usage)
		shared.PublishIntermediateToolTurn(pt.bus, pt.channels, ctx, pt.id, resp)
		logPipelineTesterTurn(ctx, turn, resp)

		if len(resp.ToolCalls) == 0 {
			if err := shared.ValidatePipelineProtocolCompletion(ctx, "tester-pipeline"); err != nil {
				logPipelineTesterRetry(ctx, turn, "protocol_incomplete", err, resp)
				pt.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
				req.Messages = append(req.Messages, providers.Message{
					Role:     providers.RoleAssistant,
					Content:  strings.TrimSpace(resp.Content),
					Metadata: resp.ProviderMetadata,
				})
				req.Messages = append(req.Messages, providers.Message{
					Role: providers.RoleUser,
					Content: err.Error() +
						"\nRespond to the active challenge with validate_work, or use challenge_agent or handoff_next explicitly.",
				})
				continue
			}
			if err := shared.ValidateTaskExecutionCompletion(ctx, "tester-pipeline"); err != nil {
				logPipelineTesterRetry(ctx, turn, "task_execution_incomplete", err, resp)
				pt.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
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
						"\nDo not conclude or release the scope yet. Continue the required testing protocol now.",
				})
				continue
			}
			// Enforce the tester report shape contract. The terminal
			// assistant text must follow the structured markdown format
			// defined in the system prompt — first line a heading, the
			// required sections present, and no wall-of-text paragraphs.
			// Re-prompt up to the bounded grace budget if the model drifts;
			// graceful degradation after that (return whatever the model
			// produced rather than holding the user's turn hostage).
			if err := shared.ValidateAgentReportShape(resp.Content, shared.TesterTurnReportShapeProfile); err != nil {
				logPipelineTesterRetry(ctx, turn, "report_shape_invalid", err, resp)
				pt.recordTurn(ctx, req, resp, turn, 0, 1, turnStart)
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
				reportShapeGraceTurns = shared.ExtendReportShapeGrace(reportShapeGraceTurns)
				continue
			}
			pt.recordTurn(ctx, req, resp, turn, 0, 0, turnStart)
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventSuiteCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.TestSuitePayload{Phase: "tool_loop_completed"})
			}
			return strings.TrimSpace(resp.Content), nil
		}

		if err := surface.ValidateBatch(pt.toolInvocationsWithSurface(ctx, resp.ToolCalls, surface)); err != nil {
			return "", err
		}
		logPipelineTesterToolBatch(ctx, turn, resp.ToolCalls)

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
					lm.AgentID, lm.SessionID, lm.CorrID, "error",
					&agentlog.ErrorPayload{Error: fmt.Sprintf("repeated tool call: %s", sig.Name)})
			}
			return "", fmt.Errorf("pipeline tester repeated tool call: %s", sig.Name)
		}

		errCount, controlErr, delegatedMessage := pt.applyToolCalls(ctx, req, resp, surface)
		pt.recordTurn(ctx, req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if controlErr != nil {
			if errors.Is(controlErr, skills.ErrDelegatedRequested) {
				return strings.TrimSpace(delegatedMessage), nil
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
			return "", fmt.Errorf("pipeline tester tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	if lm := shared.LogMetaFromContext(ctx); lm.EventLogger != nil {
		shared.LogAgentEvent(lm.EventLogger, agentlog.EventError,
			lm.AgentID, lm.SessionID, lm.CorrID, "error",
			&agentlog.ErrorPayload{Error: "exhausted tool-call loop"})
	}
	return "", fmt.Errorf("pipeline tester exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
func (pt *PipelineTester) applyToolCalls(
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
		execCtx := shared.WithActiveToolCall(ctx, call)
		result, err := shared.TimedToolCall(execCtx, "tester-pipeline", call, func() (string, error) {
			execResult, execErr = pt.executeToolCallWithSurface(execCtx, call, surface)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			pt.toolDefsDirty = true
		}
		isError := false
		if err != nil {
			switch {
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
			shared.LogAgentEvent(lm.EventLogger, shared.ToolNameToEventType(call.Name),
				lm.AgentID, lm.SessionID, lm.CorrID, "info",
				&agentlog.ToolPayload{ToolName: call.Name, Success: !isError})
		}
		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		// Activity Fabric: append the ambient_context envelope so
		// the LLM sees peer activity, inbound disputes, and
		// advisories on every tool result. Bounded; rate-limited;
		// best-effort. See docs/SCRIBE_FABRIC.md and
		// agents/shared/ambient_envelope.go.
		result = shared.AppendAmbientContext(ctx, shared.AmbientEnvelopeConfig{
			SessionID:  func() string { return pt.config.SessionID },
			AgentID:    func() string { return pt.id },
			AgentType:  func() string { return "tester-pipeline" },
			PipelineID: func() string { return pt.pipelineID },
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
	return errCount, controlErr, delegatedMessage
}

// executeToolCall invokes a skill by name with JSON arguments.
func (pt *PipelineTester) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	return pt.executeToolCallWithSurface(ctx, call, pt.toolRuntime())
}

func (pt *PipelineTester) executeToolCallWithSurface(
	ctx context.Context,
	call providers.ToolCall,
	surface toolruntime.Surface,
) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return toolruntime.ExecutionResult{}, fmt.Errorf("tool name is required")
	}
	if surface == nil {
		surface = pt.toolRuntime()
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
	if err := shared.ValidateTaskExecutionCall(ctx, "tester-pipeline", name, input); err != nil {
		return toolruntime.ExecutionResult{}, err
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = pt.id + "-local"
	}
	result, err := surface.Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         surface.AgentID(),
		CorrelationID:   correlationID,
		CapabilityScope: surface.CapabilityScope(),
	})
	if err == nil {
		shared.RecordTaskExecutionSuccess(ctx, name, input, result.Output)
		pt.recordSuccessfulToolOutput(name, result.Output)
	}
	return result, err
}

// prepareSkillsForInput progressively loads skills relevant to the user's
// input and optimizes to stay within the tool-definition token budget.
func (pt *PipelineTester) prepareSkillsForInput(input string) {
	if pt.skillLoader == nil {
		return
	}
	pt.skillLoader.LoadForInput(input)
	pt.skillLoader.OptimizeForBudget()
}

// buildToolDefinitions converts loaded skills to provider tool format.
func (pt *PipelineTester) buildToolDefinitions() []providers.Tool {
	return pt.buildToolDefinitionsWithSurface(pt.toolRuntime())
}

func (pt *PipelineTester) buildToolDefinitionsWithSurface(surface toolruntime.Surface) []providers.Tool {
	if surface == nil {
		return nil
	}
	surface.SyncActiveFromLoaded()
	return surface.BuildToolDefinitions()
}

func (pt *PipelineTester) toolRuntime() *toolruntime.Runtime {
	return pt.tools
}

func (pt *PipelineTester) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	return pt.toolInvocationsWithSurface(ctx, calls, pt.toolRuntime())
}

func (pt *PipelineTester) toolInvocationsWithSurface(
	ctx context.Context,
	calls []providers.ToolCall,
	surface toolruntime.Surface,
) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	if surface == nil {
		surface = pt.toolRuntime()
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	if correlationID == "" {
		correlationID = pt.id + "-local"
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

func logPipelineTesterTurn(ctx context.Context, turn int, resp *providers.Response) {
	lm := shared.LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	data := pipelineTesterTurnLogData(turn, resp)
	lm.EventLogger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "debug",
		Agent:     lm.AgentID,
		SessionID: lm.SessionID,
		Event:     "tester_turn_response",
		CorrID:    lm.CorrID,
		Data:      data,
	})
}

func logPipelineTesterRetry(ctx context.Context, turn int, reason string, retryErr error, resp *providers.Response) {
	lm := shared.LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	data := pipelineTesterTurnLogData(turn, resp)
	data["retry_reason"] = reason
	if retryErr != nil {
		data["retry_error"] = retryErr.Error()
	}
	lm.EventLogger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "debug",
		Agent:     lm.AgentID,
		SessionID: lm.SessionID,
		Event:     "tester_turn_retry",
		CorrID:    lm.CorrID,
		Data:      data,
	})
}

func logPipelineTesterToolBatch(ctx context.Context, turn int, calls []providers.ToolCall) {
	lm := shared.LogMetaFromContext(ctx)
	if lm.EventLogger == nil {
		return
	}
	lm.EventLogger.LogEvent(agentlog.JSONLEntry{
		Timestamp: time.Now(),
		Level:     "debug",
		Agent:     lm.AgentID,
		SessionID: lm.SessionID,
		Event:     "tester_tool_batch",
		CorrID:    lm.CorrID,
		Data: map[string]any{
			"turn":       turn,
			"tool_count": len(calls),
			"tool_names": pipelineTesterToolNames(calls),
		},
	})
}

func pipelineTesterTurnLogData(turn int, resp *providers.Response) map[string]any {
	data := map[string]any{
		"turn": turn,
	}
	if resp == nil {
		data["response_nil"] = true
		return data
	}
	data["stop_reason"] = resp.StopReason
	data["tool_call_count"] = len(resp.ToolCalls)
	data["tool_call_names"] = pipelineTesterToolNames(resp.ToolCalls)
	data["content_len"] = len(resp.Content)
	data["thinking_len"] = len(resp.Thinking)
	if preview := pipelineTesterPreview(resp.Content); preview != "" {
		data["content_preview"] = preview
	}
	if preview := pipelineTesterPreview(resp.Thinking); preview != "" {
		data["thinking_preview"] = preview
	}
	if metadata := resp.ProviderMetadata; len(metadata) > 0 {
		if itemTypes, ok := metadata["output_item_types"]; ok {
			data["output_item_types"] = itemTypes
		}
		if itemCount, ok := metadata["output_item_count"]; ok {
			data["output_item_count"] = itemCount
		}
		if typeCounts, ok := metadata["output_item_type_counts"]; ok {
			data["output_item_type_counts"] = typeCounts
		}
		if streamFallback, ok := metadata["stream_fallback"]; ok {
			data["stream_fallback"] = streamFallback
		}
		if eventCount, ok := metadata["openai_stream_event_count"]; ok {
			data["openai_stream_event_count"] = eventCount
		}
		if eventTypes, ok := metadata["openai_stream_event_types"]; ok {
			data["openai_stream_event_types"] = eventTypes
		}
		if typeCounts, ok := metadata["openai_stream_event_type_counts"]; ok {
			data["openai_stream_event_type_counts"] = typeCounts
		}
		if outputCount, ok := metadata["openai_stream_completion_output_count"]; ok {
			data["openai_stream_completion_output_count"] = outputCount
		}
		if recovered, ok := metadata["stream_completion_recovered"]; ok {
			data["stream_completion_recovered"] = recovered
		}
	}
	return data
}

func pipelineTesterToolNames(calls []providers.ToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			name = "<unnamed>"
		}
		names = append(names, name)
	}
	return names
}

func pipelineTesterPreview(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if len(text) <= 240 {
		return text
	}
	return text[:240] + "..."
}

// Tool loop helpers (marshalToolOutput, toolErrorPayload, coerceMap,
// detectToolCallDuplicate, updateToolErrors) are in agents/shared/toolloop.go.

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (pt *PipelineTester) recordTurn(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if pt.handoffBridge == nil {
		return
	}
	pt.handoffBridge.RecordTurn(shared.BuildHandoffTurnRecord(ctx, req, resp, turn, toolCalls, errCount, turnStart))
}
