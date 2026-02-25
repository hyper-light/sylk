package engineer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// executeToolLoop runs the LLM tool-call loop: Complete → check ToolCalls →
// execute → append results → repeat, bounded by config.EngineerConfig.MaxToolRuns.
// Follows the pipeline tester tool loop pattern exactly.
func (e *Engineer) executeToolLoop(ctx context.Context, req *providers.Request) (string, error) {
	maxRuns := e.config.EngineerConfig.MaxToolRuns
	seen := make(map[shared.ToolCallSignature]int, maxRuns)
	consecutiveErrors := 0

	p := e.getProvider()
	if p == nil {
		return "", fmt.Errorf("engineer: no LLM provider configured")
	}

	for turn := 0; turn <= maxRuns; turn++ {
		resp, err := p.Complete(ctx, req)
		if err != nil {
			return "", fmt.Errorf("engineer llm: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			return strings.TrimSpace(resp.Content), nil
		}

		if turn == maxRuns {
			return "", fmt.Errorf("engineer exceeded tool-call limit (%d)", maxRuns)
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen); dup {
			return "", fmt.Errorf("engineer repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := e.applyToolCalls(ctx, req, resp)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= shared.MaxConsecutiveToolErrors {
			return "", fmt.Errorf("engineer tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("engineer exhausted tool-call loop")
}

// applyToolCalls appends the assistant message and tool results to the request.
func (e *Engineer) applyToolCalls(
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
		result, err := shared.TimedToolCall(ctx, "engineer", call, func() (string, error) {
			return e.executeToolCall(ctx, call)
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
func (e *Engineer) executeToolCall(_ context.Context, call providers.ToolCall) (string, error) {
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

	result := e.skills.Invoke(context.Background(), name, json.RawMessage(raw))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	return shared.MarshalToolOutput(result.Data)
}

// buildToolDefinitions converts loaded skills to provider Tool format.
func (e *Engineer) buildToolDefinitions() []providers.Tool {
	loaded := e.skills.GetLoaded()
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
