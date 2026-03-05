package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/steering"
)

// executeToolLoop runs the model tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.MaxToolRuns.
func (o *Orchestrator) executeToolLoop(ctx context.Context, req *providers.Request, ledger *steering.SteeringLedger) (string, error) {
	seen := make(map[toolCallSignature]int, o.config.MaxToolRuns)
	consecutiveErrors := 0

	for turn := 0; turn <= o.config.MaxToolRuns; turn++ {
		// ── STEERING CHECKPOINT ──
		sc := shared.DrainAndCheckpoint(ledger, req, turn, "orchestrating", nil)
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
			seen = make(map[toolCallSignature]int, o.config.MaxToolRuns)
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
		if err := shared.ApplyContextBudget(ctx, turn, o.config.MaxToolRuns, req); err != nil {
			return "", err
		}

		turnStart := time.Now()
		resp, err := o.provider.Complete(ctx, req)
		shared.LogLLMCallFromContext(ctx, req.Model, resp, time.Since(turnStart), err)
		if err != nil {
			return "", fmt.Errorf("orchestrator llm: %w", err)
		}

		if gov := shared.ContextGovernorFromContext(ctx); gov != nil {
			gov.Calibrate(ctx, resp, req.Messages)
		}

		if len(resp.ToolCalls) == 0 {
			o.recordTurn(req, resp, turn, 0, 0, turnStart)
			return strings.TrimSpace(resp.Content), nil
		}



		if dup, sig := detectToolCallDuplicate(resp.ToolCalls, seen); dup {
			return "", fmt.Errorf("orchestrator repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := o.applyToolCalls(ctx, req, resp)
		o.recordTurn(req, resp, turn, len(resp.ToolCalls), errCount, turnStart)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		consecutiveErrors = updateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			return "", fmt.Errorf("orchestrator tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("orchestrator exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
// Returns the error count and whether a reroute was requested.
func (o *Orchestrator) applyToolCalls(
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

	errCount := 0
	rerouted := false
	for _, call := range resp.ToolCalls {
		o.publishActivity(events.EventTypeToolResult, call.Name)
		result, err := shared.TimedToolCall(ctx, "orchestrator", call, func() (string, error) {
			return o.executeToolCall(ctx, call)
		})
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
			} else {
				result = toolErrorPayload(err)
				isError = true
				errCount++
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
		if rerouted {
			break
		}
	}
	return errCount, rerouted
}

// executeToolCall invokes a skill by name with JSON arguments.
func (o *Orchestrator) executeToolCall(ctx context.Context, call providers.ToolCall) (string, error) {
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

	result := o.skills.Invoke(ctx, name, json.RawMessage(raw))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	return marshalToolOutput(result.Data)
}

// buildToolDefinitions converts loaded skills to provider tool format.
func (o *Orchestrator) buildToolDefinitions() []providers.Tool {
	loaded := o.skills.GetLoaded()
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
		parameters := coerceMap(def["input_schema"])
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

func marshalToolOutput(data any) (string, error) {
	switch typed := data.(type) {
	case string:
		return typed, nil
	case fmt.Stringer:
		return typed.String(), nil
	default:
		payload, err := json.Marshal(data)
		if err != nil {
			return "", fmt.Errorf("marshal tool output: %w", err)
		}
		return string(payload), nil
	}
}

func toolErrorPayload(err error) string {
	if err == nil {
		return ""
	}
	payload, marshalErr := json.Marshal(map[string]any{
		"error": strings.TrimSpace(err.Error()),
	})
	if marshalErr != nil {
		return `{"error":"tool execution failed"}`
	}
	return string(payload)
}

func coerceMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var mapped map[string]any
	if err := json.Unmarshal(payload, &mapped); err != nil {
		return nil
	}
	return mapped
}

type toolCallSignature struct {
	Name      string
	Arguments string
}

func detectToolCallDuplicate(calls []providers.ToolCall, seen map[toolCallSignature]int) (bool, toolCallSignature) {
	batch := make([]toolCallSignature, 0, len(calls))
	for _, call := range calls {
		sig := toolCallSignature{
			Name:      strings.TrimSpace(call.Name),
			Arguments: strings.TrimSpace(call.Arguments),
		}
		batch = append(batch, sig)
	}

	allDup := true
	var firstDup toolCallSignature
	for _, sig := range batch {
		seen[sig]++
		if seen[sig] <= 1 {
			allDup = false
		} else if firstDup.Name == "" {
			firstDup = sig
		}
	}
	return allDup, firstDup
}

func updateToolErrors(current, errCount, totalCalls int) int {
	if totalCalls > 0 && errCount == totalCalls {
		return current + 1
	}
	return 0
}

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (o *Orchestrator) recordTurn(
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if o.handoffBridge == nil {
		return
	}

	o.handoffBridge.RecordTurn(handoff.TurnRecord{
		InputTokens:      resp.Usage.InputTokens,
		OutputTokens:     resp.Usage.OutputTokens,
		ContextSize:      shared.EstimateContextSize(req.Messages),
		ToolCalls:        toolCalls,
		ToolSuccesses:    toolCalls - errCount,
		TurnNumber:       turn + 1,
		Duration:         time.Since(turnStart),
		Timestamp:        time.Now(),
		StopReason:       resp.StopReason,
		CacheReadTokens:  resp.Usage.CacheReadTokens,
		CacheWriteTokens: resp.Usage.CacheWriteTokens,
	})
}
