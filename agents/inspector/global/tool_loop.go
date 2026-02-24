package global

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/agents/inspector/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

func (gi *GlobalInspector) executeToolLoop(ctx context.Context, req *providers.Request) (string, error) {
	seen := make(map[shared.ToolCallSignature]int, gi.config.MaxToolRuns)
	consecutiveErrors := 0

	for turn := 0; turn <= gi.config.MaxToolRuns; turn++ {
		resp, err := gi.provider.Complete(ctx, req)
		if err != nil {
			return "", fmt.Errorf("global inspector llm: %w", err)
		}

		shared.AccumulateUsage(ctx, &resp.Usage)

		if len(resp.ToolCalls) == 0 {
			return strings.TrimSpace(resp.Content), nil
		}

		if turn == gi.config.MaxToolRuns {
			return "", fmt.Errorf("global inspector exceeded tool-call limit (%d)", gi.config.MaxToolRuns)
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen); dup {
			return "", fmt.Errorf("global inspector repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := gi.applyToolCalls(ctx, req, resp)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			return "", fmt.Errorf("global inspector tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("global inspector exhausted tool-call loop")
}

func (gi *GlobalInspector) applyToolCalls(
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
		result, err := gi.executeToolCall(ctx, call)
		if err != nil {
			if errors.Is(err, skills.ErrRerouteRequested) {
				rerouted = true
				result = `{"rerouted": true}`
			} else {
				result = shared.ToolErrorPayload(err)
				errCount++
			}
		}
		req.Messages = append(req.Messages, providers.Message{
			Role:       providers.RoleTool,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    result,
		})
		if rerouted {
			break
		}
	}
	return errCount, rerouted
}

func (gi *GlobalInspector) executeToolCall(_ context.Context, call providers.ToolCall) (string, error) {
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

	result := gi.skills.Invoke(context.Background(), name, json.RawMessage(raw))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	return shared.MarshalToolOutput(result.Data)
}

func (gi *GlobalInspector) buildToolDefinitions() []providers.Tool {
	return shared.BuildToolDefinitions(gi.skills.GetLoaded())
}
