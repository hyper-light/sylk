package pipeline

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

// executeToolLoop runs the LLM tool-call loop bounded by config.MaxToolRuns.
func (pi *PipelineInspector) executeToolLoop(ctx context.Context, req *providers.Request) (string, error) {
	seen := make(map[shared.ToolCallSignature]int, pi.config.MaxToolRuns)
	consecutiveErrors := 0

	for turn := 0; turn <= pi.config.MaxToolRuns; turn++ {
		resp, err := pi.provider.Complete(ctx, req)
		if err != nil {
			return "", fmt.Errorf("pipeline inspector llm: %w", err)
		}

		shared.AccumulateUsage(ctx, &resp.Usage)

		if len(resp.ToolCalls) == 0 {
			return strings.TrimSpace(resp.Content), nil
		}

		if turn == pi.config.MaxToolRuns {
			return "", fmt.Errorf("pipeline inspector exceeded tool-call limit (%d)", pi.config.MaxToolRuns)
		}

		if dup, sig := shared.DetectToolCallDuplicate(resp.ToolCalls, seen); dup {
			return "", fmt.Errorf("pipeline inspector repeated tool call: %s", sig.Name)
		}

		errCount, rerouted := pi.applyToolCalls(ctx, req, resp)
		if rerouted {
			return "", skills.ErrRerouteRequested
		}
		consecutiveErrors = shared.UpdateToolErrors(consecutiveErrors, errCount, len(resp.ToolCalls))
		if consecutiveErrors >= 2 {
			return "", fmt.Errorf("pipeline inspector tool calls failed %d consecutive turns", consecutiveErrors)
		}
	}

	return "", fmt.Errorf("pipeline inspector exhausted tool-call loop")
}

func (pi *PipelineInspector) applyToolCalls(
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
		result, err := pi.executeToolCall(ctx, call)
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

func (pi *PipelineInspector) executeToolCall(_ context.Context, call providers.ToolCall) (string, error) {
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

	result := pi.skills.Invoke(context.Background(), name, json.RawMessage(raw))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	return shared.MarshalToolOutput(result.Data)
}

func (pi *PipelineInspector) buildToolDefinitions() []providers.Tool {
	return shared.BuildToolDefinitions(pi.skills.GetLoaded())
}
