package architect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// executeToolLoop runs the LLM tool-call loop: stream → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
// Follows the engineer/designer tool loop pattern, using the streaming
// CompleteForToolLoop instead of a synchronous Complete.
func (a *Architect) executeToolLoop(
	ctx context.Context,
	req *providers.Request,
	stage string,
	onChunk func(string),
) (string, error) {
	maxRuns := a.config.MaxToolRuns
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
			msgSummary[i] = fmt.Sprintf("%s(%d)", m.Role, len(m.Content))
		}
		a.logDebug("tool_loop: LLM_CALL_START",
			"stage", stage,
			"turn", turn,
			"messages", strings.Join(msgSummary, ","),
			"loop_elapsed", time.Since(loopStart).String())

		a.logInfo("executeToolLoop: LLM call",
			"stage", stage,
			"turn", turn,
			"loop_elapsed", time.Since(loopStart).String(),
			"ctx_deadline", contextDeadlineString(ctx))
		turnStart := time.Now()
		resp, err := planner.CompleteForToolLoop(ctx, req, stage, onChunk)

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

		if turn == maxRuns {
			a.logWarn("executeToolLoop: tool-call limit exceeded",
				"stage", stage, "max_runs", maxRuns)
			a.logDebug("tool_loop: TOOL_LIMIT_EXCEEDED",
				"stage", stage, "max_runs", maxRuns,
				"total_elapsed", time.Since(loopStart).String())
			return "", fmt.Errorf("architect exceeded tool-call limit (%d)", maxRuns)
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen); dup {
			a.logWarn("executeToolLoop: duplicate tool call detected",
				"stage", stage, "tool", sig.Name)
			a.logDebug("tool_loop: DUPLICATE_TOOL_CALL",
				"stage", stage, "tool", sig.Name, "turn", turn)
			return "", fmt.Errorf("architect repeated tool call: %s", sig.Name)
		}

		toolStart := time.Now()
		errCount, rerouted := a.applyToolCalls(ctx, req, resp)
		a.logInfo("executeToolLoop: tool calls applied",
			"stage", stage,
			"turn", turn,
			"tool_elapsed", time.Since(toolStart).String(),
			"err_count", errCount,
			"rerouted", rerouted)
		a.logDebug("tool_loop: TOOLS_APPLIED",
			"stage", stage,
			"turn", turn,
			"tool_elapsed", time.Since(toolStart).String(),
			"err_count", errCount,
			"rerouted", rerouted,
			"messages_after", len(req.Messages))
		if rerouted {
			a.logInfo("executeToolLoop: REROUTED",
				"stage", stage,
				"turn", turn,
				"total_elapsed", time.Since(loopStart).String())
			return "", skills.ErrRerouteRequested
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
) (int, bool) {
	req.Messages = append(req.Messages, providers.Message{
		Role:      providers.RoleAssistant,
		Content:   strings.TrimSpace(resp.Content),
		ToolCalls: resp.ToolCalls,
		Metadata:  resp.ProviderMetadata,
	})

	loadedBefore := len(a.skills.GetLoaded())

	errCount := 0
	rerouted := false
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
		result, err := shared.TimedToolCall(ctx, "architect", call, func() (string, error) {
			return a.executeToolCall(ctx, call)
		})
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
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
			IsError:    isError,
		})
		if rerouted {
			break
		}
	}

	// Detect demand-paged skills loaded during this turn.
	if len(a.skills.GetLoaded()) > loadedBefore {
		a.toolDefsDirty = true
		a.logDebug("tool_apply: SKILLS_DEMAND_PAGED",
			"loaded_before", loadedBefore,
			"loaded_after", len(a.skills.GetLoaded()))
	}

	return errCount, rerouted
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
func (a *Architect) executeToolCall(ctx context.Context, call providers.ToolCall) (string, error) {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return "", fmt.Errorf("tool name is required")
	}

	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return "", fmt.Errorf("tool arguments for %q are not valid JSON", name)
	}

	a.logInfo("executeToolCall: invoking skill",
		"skill", name,
		"args_len", len(raw),
		"ctx_deadline", contextDeadlineString(ctx))
	a.logDebug("tool_exec: INVOKE",
		"skill", name,
		"args", truncateString(raw, 1000))
	invokeStart := time.Now()
	result := a.InvokeSkill(ctx, name, json.RawMessage(raw))
	elapsed := time.Since(invokeStart)
	a.logInfo("executeToolCall: skill returned",
		"skill", name,
		"elapsed", elapsed.String(),
		"success", result != nil && result.Success,
		"has_result", result != nil)
	a.logDebug("tool_exec: RESULT",
		"skill", name,
		"elapsed", elapsed.String(),
		"success", result != nil && result.Success,
		"error", resultError(result))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	return shared.MarshalToolOutput(result.Data)
}

// resultError extracts the error string from a skill result, or empty string.
func resultError(r *skills.Result) string {
	if r == nil {
		return "<nil result>"
	}
	return r.Error
}

// buildToolDefinitions converts loaded skills to provider Tool format.
func (a *Architect) buildToolDefinitions() []providers.Tool {
	loaded := a.skills.GetLoaded()
	if len(loaded) == 0 {
		return nil
	}

	tools := make([]providers.Tool, 0, len(loaded))
	for _, skill := range loaded {
		def := skill.ToToolDefinition()
		name, _ := def["name"].(string)
		if name == "" {
			continue
		}
		description, _ := def["description"].(string)
		parameters := shared.CoerceMap(def["input_schema"])
		if len(parameters) == 0 {
			parameters = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		tools = append(tools, providers.Tool{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		})
	}
	return tools
}
