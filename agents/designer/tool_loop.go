package designer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// executeToolLoop runs the model tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.DesignerConfig.MaxToolRuns.
// After each Complete call it records a TurnRecord on the handoff bridge (if set)
// and accumulates usage for streaming.
func (d *Designer) executeToolLoop(ctx context.Context, req *providers.Request) (string, error) {
	seen := make(map[shared.ToolCallSignature]int, d.config.DesignerConfig.MaxToolRuns)
	consecutiveErrors := 0

	p := d.getProvider()
	if p == nil {
		return "", fmt.Errorf("designer: no LLM provider configured")
	}

	for turn := 0; turn <= d.config.DesignerConfig.MaxToolRuns; turn++ {
		turnStart := time.Now()

		resp, err := p.Complete(ctx, req)
		if err != nil {
			return "", fmt.Errorf("designer llm: %w", err)
		}

		d.accumulateUsage(resp)

		if len(resp.ToolCalls) == 0 {
			d.recordTurn(req, resp, turn, 0, 0, turnStart)
			return strings.TrimSpace(resp.Content), nil
		}

		if turn == d.config.DesignerConfig.MaxToolRuns {
			return "", fmt.Errorf("designer exceeded tool-call limit (%d)", d.config.DesignerConfig.MaxToolRuns)
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen); dup {
			return "", fmt.Errorf("designer repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := d.applyToolCalls(ctx, req, resp)

		d.recordTurn(req, resp, turn, len(resp.ToolCalls), errCount, turnStart)

		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			return "", fmt.Errorf("designer tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("designer exhausted tool-call loop")
}

// applyToolCalls appends the assistant message (preserving ProviderMetadata for
// Google thought signatures) and tool results to the request.
func (d *Designer) applyToolCalls(
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
		if ctx.Err() != nil {
			break
		}
		result, err := shared.TimedToolCall(ctx, "designer", call, func() (string, error) {
			return d.executeToolCall(ctx, call)
		})
		isError := false
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
			} else {
				result = shared.ToolErrorPayload(err)
				isError = true
				errCount++
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
	return errCount, rerouted
}

// executeToolCall invokes a skill by name with JSON arguments.
func (d *Designer) executeToolCall(ctx context.Context, call providers.ToolCall) (string, error) {
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

	result := d.skills.Invoke(ctx, name, json.RawMessage(raw))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	return shared.MarshalToolOutput(result.Data)
}

// buildToolDefinitions converts loaded skills to provider tool format.
func (d *Designer) buildToolDefinitions() []providers.Tool {
	loaded := d.skills.GetLoaded()
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

// recordTurn feeds the handoff bridge with turn metrics from this LLM call.
func (d *Designer) recordTurn(
	req *providers.Request,
	resp *providers.Response,
	turn, toolCalls, errCount int,
	turnStart time.Time,
) {
	if d.handoffBridge == nil {
		return
	}

	contextSize := estimateContextSize(req.Messages)

	d.handoffBridge.RecordTurn(handoff.TurnRecord{
		InputTokens:      resp.Usage.InputTokens,
		OutputTokens:     resp.Usage.OutputTokens,
		ContextSize:      contextSize,
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

// estimateContextSize approximates total context tokens from accumulated messages.
// Uses ~4 characters per token plus overhead per message, matching core/llm/context.go.
func estimateContextSize(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)/4 + 4
	}
	return total
}
