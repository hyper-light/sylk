package architect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/llmruntime"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
	"github.com/adalundhe/sylk/core/toolruntime"
)

// toolRunsOverrideKey allows protocol code to override the default MaxToolRuns
// for a specific executeToolLoop invocation via the context.
type toolRunsOverrideKey struct{}

// withToolRunsOverride returns a context that overrides the max tool runs
// for executeToolLoop. Used by the planning protocol to set a higher budget
// (protocolMaxToolRuns) than the default config.MaxToolRuns.
func withToolRunsOverride(ctx context.Context, maxRuns int) context.Context {
	return context.WithValue(ctx, toolRunsOverrideKey{}, maxRuns)
}

// executeToolLoop runs the LLM tool-call loop: stream → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the engineer/designer tool loop pattern, using the streaming
// CompleteForToolLoop instead of a synchronous Complete.
func (a *Architect) executeToolLoop(
	ctx context.Context,
	req *providers.Request,
	stage string,
	onChunk func(string),
	ledger *steering.SteeringLedger,
) (string, error) {
	maxRuns := a.config.MaxToolRuns
	if override, ok := ctx.Value(toolRunsOverrideKey{}).(int); ok && override > 0 {
		maxRuns = override
	}
	seen := make(map[shared.ToolCallSignature]int, maxRuns)
	consecutiveErrors := 0

	planner := a.ensurePlanner(ctx)
	if planner == nil {
		return "", fmt.Errorf("architect: no LLM planner configured")
	}

	// Debug: log tool definitions available for this loop.
	toolNames := make([]string, len(req.Tools))
	for i, t := range req.Tools {
		toolNames[i] = t.Name
	}
	a.logDebug("tool_loop: START",
		"stage", stage,
		"max_runs", maxRuns,
		"tools_available", strings.Join(toolNames, ","),
		"tools_count", len(req.Tools),
		"messages_count", len(req.Messages),
		"max_tokens", req.MaxTokens,
		"thinking_budget", req.ThinkingBudget,
		"system_prompt_len", len(req.SystemPrompt))

	a.logInfo("executeToolLoop: START",
		"stage", stage,
		"max_runs", maxRuns,
		"ctx_deadline", contextDeadlineString(ctx),
		"tools_count", len(req.Tools),
		"messages_count", len(req.Messages))

	loopStart := time.Now()
	for turn := 0; turn <= maxRuns; turn++ {
		if ctx.Err() != nil {
			a.logWarn("executeToolLoop: context cancelled before turn",
				"stage", stage, "turn", turn, "ctx_err", ctx.Err())
			a.logDebug("tool_loop: CONTEXT_CANCELLED",
				"stage", stage, "turn", turn, "ctx_err", ctx.Err().Error(),
				"elapsed", time.Since(loopStart).String())
			return "", ctx.Err()
		}

		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, stage, nil)
		if sc.Rollback != nil || sc.EditReplay != nil {
			cp := sc.Rollback
			if cp == nil {
				cp = sc.EditReplay
			}
			req.Messages = req.Messages[:cp.MessageCount]
			if sc.EditReplay != nil {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: sc.EditText})
			}
			turn = cp.Turn
			seen = make(map[shared.ToolCallSignature]int, maxRuns)
			consecutiveErrors = 0
			a.logInfo("executeToolLoop: steering rollback",
				"stage", stage, "to_turn", cp.Turn, "to_msgcount", cp.MessageCount)
			continue
		}
		if sc.ShouldPause {
			a.logInfo("executeToolLoop: steering pause", "stage", stage, "turn", turn)
			if err := ledger.WaitForResume(ctx); err != nil {
				return "", err
			}
			continue
		}
		// ── END STEERING ──

		if a.toolDefsDirty {
			req.Tools = a.buildToolDefinitions()
			a.toolDefsDirty = false
			a.logDebug("tool_loop: TOOLS_REBUILT",
				"stage", stage, "turn", turn,
				"new_tools_count", len(req.Tools))
		}

		// Debug: log message history size entering this turn.
		msgSummary := make([]string, len(req.Messages))
		for i, m := range req.Messages {
			tcInfo := ""
			if len(m.ToolCalls) > 0 {
				tcNames := make([]string, len(m.ToolCalls))
				for j, tc := range m.ToolCalls {
					tcNames[j] = tc.Name
				}
				tcInfo = fmt.Sprintf("+tools[%s]", strings.Join(tcNames, ","))
			}
			if m.ToolCallID != "" {
				tcInfo = fmt.Sprintf("+result[%s]", m.ToolName)
			}
			msgSummary[i] = fmt.Sprintf("%s(%d)%s", m.Role, len(m.Content), tcInfo)
		}
		a.logDebug("tool_loop: LLM_CALL_START",
			"stage", stage,
			"turn", turn,
			"messages", strings.Join(msgSummary, ","),
			"loop_elapsed", time.Since(loopStart).String())
		architectDebugLog().Info("handoff: TOOL_LOOP_TURN",
			"stage", stage,
			"turn", turn,
			"messages_count", len(req.Messages),
			"messages_detail", strings.Join(msgSummary, " | "),
			"tools_count", len(req.Tools))

		// ── CONTEXT BUDGET ──
		if err := shared.ApplyContextBudget(ctx, turn, maxRuns, req); err != nil {
			return "", err
		}

		a.logInfo("executeToolLoop: LLM call",
			"stage", stage,
			"turn", turn,
			"loop_elapsed", time.Since(loopStart).String(),
			"ctx_deadline", contextDeadlineString(ctx))
		turnStart := time.Now()
		resp, err := planner.CompleteForToolLoop(ctx, req, stage, onChunk)
		shared.LogLLMCallFromContext(ctx, req.Model, resp, time.Since(turnStart), err)

		// Debug: thorough response logging.
		if resp != nil {
			a.logDebug("tool_loop: LLM_RESPONSE",
				"stage", stage,
				"turn", turn,
				"elapsed", time.Since(turnStart).String(),
				"content_len", len(resp.Content),
				"thinking_len", len(resp.Thinking),
				"tool_call_count", len(resp.ToolCalls),
				"stop_reason", string(resp.StopReason),
				"input_tokens", resp.Usage.InputTokens,
				"output_tokens", resp.Usage.OutputTokens,
				"total_tokens", resp.Usage.TotalTokens,
				"content_preview", truncateString(resp.Content, 200),
				"thinking_preview", truncateString(resp.Thinking, 300))
		} else {
			a.logDebug("tool_loop: LLM_RESPONSE_NIL",
				"stage", stage, "turn", turn, "err", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && resp != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}

		if err != nil {
			a.logWarn("executeToolLoop: LLM error",
				"stage", stage, "turn", turn, "err", err)
			a.logDebug("tool_loop: LLM_ERROR",
				"stage", stage, "turn", turn, "err", err.Error(),
				"elapsed", time.Since(turnStart).String())
			return "", fmt.Errorf("architect llm: %w", err)
		}

		a.logInfo("executeToolLoop: LLM response",
			"stage", stage,
			"turn", turn,
			"elapsed", time.Since(turnStart).String(),
			"tool_calls", len(resp.ToolCalls))

		if len(resp.ToolCalls) == 0 {
			a.logDebug("tool_loop: COMPLETE_TEXT_ONLY",
				"stage", stage,
				"turn", turn,
				"content_len", len(resp.Content),
				"thinking_len", len(resp.Thinking),
				"stop_reason", string(resp.StopReason),
				"total_elapsed", time.Since(loopStart).String())
			a.logInfo("executeToolLoop: COMPLETE (text only)",
				"stage", stage,
				"turn", turn,
				"content_len", len(resp.Content),
				"total_elapsed", time.Since(loopStart).String())
			architectDebugLog().Warn("handoff: TOOL_LOOP_TEXT_ONLY_EXIT",
				"stage", stage,
				"turn", turn,
				"stop_reason", string(resp.StopReason),
				"content_len", len(resp.Content),
				"content_preview", truncateString(resp.Content, 500),
				"thinking_preview", truncateString(resp.Thinking, 300),
				"model", resp.Model,
				"tools_were_available", len(req.Tools) > 0)
			a.recordTurn(req, resp, turn, 0, 0, turnStart)
			return strings.TrimSpace(resp.Content), nil
		}

		respToolNames := make([]string, len(resp.ToolCalls))
		for i, tc := range resp.ToolCalls {
			respToolNames[i] = tc.Name
		}

		// Debug: log each tool call's arguments.
		for i, tc := range resp.ToolCalls {
			a.logDebug("tool_loop: TOOL_CALL_DETAIL",
				"stage", stage,
				"turn", turn,
				"tool_index", i,
				"tool_name", tc.Name,
				"tool_id", tc.ID,
				"args_len", len(tc.Arguments),
				"args_preview", truncateString(tc.Arguments, 500))
		}

		a.logInfo("executeToolLoop: tool calls",
			"stage", stage,
			"turn", turn,
			"tools", strings.Join(respToolNames, ","))

		if err := a.tools.ValidateBatch(a.toolInvocations(ctx, resp.ToolCalls)); err != nil {
			return "", err
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen, req.Messages); dup {
			a.logWarn("executeToolLoop: duplicate tool call detected",
				"stage", stage, "tool", sig.Name)
			a.logDebug("tool_loop: DUPLICATE_TOOL_CALL",
				"stage", stage, "tool", sig.Name, "turn", turn)
			return "", fmt.Errorf("architect repeated tool call: %s", sig.Name)
		}

		toolStart := time.Now()
		errCount, rerouted, delegated, delegatedMessage := a.applyToolCalls(ctx, req, resp)
		a.logInfo("executeToolLoop: tool calls applied",
			"stage", stage,
			"turn", turn,
			"tool_elapsed", time.Since(toolStart).String(),
			"err_count", errCount,
			"rerouted", rerouted,
			"delegated", delegated)
		a.logDebug("tool_loop: TOOLS_APPLIED",
			"stage", stage,
			"turn", turn,
			"tool_elapsed", time.Since(toolStart).String(),
			"err_count", errCount,
			"rerouted", rerouted,
			"messages_after", len(req.Messages))
		a.recordTurn(req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if rerouted {
			a.logInfo("executeToolLoop: REROUTED",
				"stage", stage,
				"turn", turn,
				"total_elapsed", time.Since(loopStart).String())
			return "", skills.ErrRerouteRequested
		}
		if delegated {
			a.logInfo("executeToolLoop: DELEGATED",
				"stage", stage,
				"turn", turn,
				"message_len", len(delegatedMessage),
				"total_elapsed", time.Since(loopStart).String())
			return strings.TrimSpace(delegatedMessage), nil
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			a.logWarn("executeToolLoop: consecutive tool errors exceeded threshold",
				"stage", stage, "consecutive_errors", consecutiveErrors)
			return "", fmt.Errorf("architect tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("architect exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
// When demand-paged skills are loaded during execution, sets toolDefsDirty
// so the next LLM turn rebuilds tool definitions.
func (a *Architect) applyToolCalls(
	ctx context.Context,
	req *providers.Request,
	resp *providers.Response,
) (int, bool, bool, string) {
	req.Messages = append(req.Messages, providers.Message{
		Role:      providers.RoleAssistant,
		Content:   strings.TrimSpace(resp.Content),
		ToolCalls: resp.ToolCalls,
		Metadata:  resp.ProviderMetadata,
	})

	errCount := 0
	rerouted := false
	delegated := false
	delegatedMessage := ""
	for i, call := range resp.ToolCalls {
		if ctx.Err() != nil {
			a.logWarn("applyToolCalls: context cancelled mid-loop",
				"tool_index", i, "ctx_err", ctx.Err())
			break
		}
		a.logInfo("applyToolCalls: executing tool",
			"tool_name", call.Name,
			"tool_index", i,
			"total_tools", len(resp.ToolCalls),
			"ctx_deadline", contextDeadlineString(ctx))
		a.logDebug("tool_apply: EXECUTE_START",
			"tool_name", call.Name,
			"tool_index", i,
			"tool_id", call.ID,
			"args_preview", truncateString(call.Arguments, 500))
		callStart := time.Now()
		var execResult toolruntime.ExecutionResult
		var execErr error
		result, err := shared.TimedToolCall(ctx, "architect", call, func() (string, error) {
			execResult, execErr = a.executeToolCall(ctx, call)
			return execResult.Output, execErr
		})
		if execResult.ToolDefsDirty {
			a.toolDefsDirty = true
		}
		a.logInfo("applyToolCalls: tool returned",
			"tool_name", call.Name,
			"tool_index", i,
			"elapsed", time.Since(callStart).String(),
			"result_len", len(result),
			"err", err)
		a.logDebug("tool_apply: EXECUTE_DONE",
			"tool_name", call.Name,
			"tool_index", i,
			"elapsed", time.Since(callStart).String(),
			"result_len", len(result),
			"result_preview", truncateString(result, 500),
			"err", err)
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
				a.logInfo("applyToolCalls: reroute requested by tool",
					"tool_name", call.Name)
			} else if errors.Is(err, skills.ErrDelegatedRequested) {
				delegated = true
				delegatedMessage = toolOutputUserMessage(result)
				if delegatedMessage == "" {
					delegatedMessage = strings.TrimSpace(result)
				}
				if delegatedMessage == "" {
					delegatedMessage = skills.DelegatedMessage(err)
				}
				a.logInfo("applyToolCalls: delegated operation requested by tool",
					"tool_name", call.Name,
					"message", delegatedMessage)
			} else {
				result = shared.ToolErrorPayload(err)
				isError = true
				errCount++
				a.logWarn("applyToolCalls: tool error",
					"tool_name", call.Name,
					"err", err)
				a.logDebug("tool_apply: TOOL_ERROR",
					"tool_name", call.Name,
					"err", err.Error(),
					"error_payload", truncateString(result, 300))
			}
		}
		if gov := shared.ContextGovernorFromContext(ctx); gov != nil && !isError {
			result = gov.LimitToolOutput(ctx, result, call.Name)
		}
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if rerouted || delegated {
			break
		}
	}

	return errCount, rerouted, delegated, delegatedMessage
}

// executeToolCall invokes a skill by name with JSON arguments.
// Uses InvokeSkill (not skills.Invoke directly) to enforce pre/post hooks
// and the safety catch from skills_api.go.
//
// The context MUST be the request context so that tool calls are cancelled
// when the parent request is interrupted. Using context.Background() here
// would cause bus-based skills (consult_librarian, etc.) to block in
// requestRouteSync for up to 60s after the request is cancelled, preventing
// bus shutdown (the handler goroutine holds a WaitGroup reference).
func (a *Architect) executeToolCall(ctx context.Context, call providers.ToolCall) (toolruntime.ExecutionResult, error) {
	name := strings.TrimSpace(call.Name)
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}

	a.logInfo("executeToolCall: invoking tool",
		"tool", name,
		"args_len", len(raw),
		"ctx_deadline", contextDeadlineString(ctx))
	a.logDebug("tool_exec: INVOKE",
		"tool", name,
		"args", truncateString(raw, 1000))
	invokeStart := time.Now()
	result, err := a.toolRuntime().Execute(ctx, toolruntime.Invocation{
		ToolCall:        call,
		AgentID:         a.toolRuntime().AgentID(),
		CorrelationID:   shared.LogMetaFromContext(ctx).CorrID,
		CapabilityScope: a.toolRuntime().CapabilityScope(),
	})
	elapsed := time.Since(invokeStart)
	a.logInfo("executeToolCall: tool returned",
		"tool", name,
		"elapsed", elapsed.String(),
		"tool_defs_dirty", result.ToolDefsDirty,
		"activated_skills", strings.Join(result.ActivatedSkills, ","),
		"err", err)
	a.logDebug("tool_exec: RESULT",
		"tool", name,
		"elapsed", elapsed.String(),
		"tool_defs_dirty", result.ToolDefsDirty,
		"activated_skills", strings.Join(result.ActivatedSkills, ","),
		"error", err)
	return result, err
}

// filterToolsByNames returns only the tools whose names are in the allowed set.
// When allowed is nil or empty, returns all tools unchanged.
func filterToolsByNames(tools []providers.Tool, allowed []string) []providers.Tool {
	if len(allowed) == 0 {
		return tools
	}
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	filtered := make([]providers.Tool, 0, len(allowed))
	for _, t := range tools {
		if _, ok := set[t.Name]; ok {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// buildToolDefinitions converts loaded skills to provider Tool format.
func (a *Architect) buildToolDefinitions() []providers.Tool {
	a.toolRuntime().SyncActiveFromLoaded()
	return a.toolRuntime().BuildToolDefinitions()
}

func (a *Architect) toolRuntime() *toolruntime.Runtime {
	return a.tools
}

func (a *Architect) toolInvocations(ctx context.Context, calls []providers.ToolCall) []toolruntime.Invocation {
	if len(calls) == 0 {
		return nil
	}
	correlationID := shared.LogMetaFromContext(ctx).CorrID
	scope := a.toolRuntime().CapabilityScope()
	invocations := make([]toolruntime.Invocation, 0, len(calls))
	for _, call := range calls {
		invocations = append(invocations, toolruntime.Invocation{
			ToolCall:        call,
			AgentID:         a.toolRuntime().AgentID(),
			CorrelationID:   correlationID,
			CapabilityScope: scope,
		})
	}
	return invocations
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (a *Architect) recordTurn(
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if a.handoffBridge == nil {
		return
	}

	a.handoffBridge.RecordTurn(handoff.TurnRecord{
		InputTokens:      resp.Usage.InputTokens,
		OutputTokens:     resp.Usage.OutputTokens,
		ContextSize:      shared.EstimateContextSize(req.Messages),
		ToolCalls:        toolCalls,
		ToolSuccesses:    toolCalls - errCount,
		TurnNumber:       turn + 1,
		Duration:         time.Since(turnStart),
		Timestamp:        time.Now(),
		Stage:            llmruntime.StageFromRequest(req),
		RuntimeProfile:   llmruntime.ProfileNameFromRequest(req),
		StopReason:       resp.StopReason,
		CacheReadTokens:  resp.Usage.CacheReadTokens,
		CacheWriteTokens: resp.Usage.CacheWriteTokens,
	})
}
