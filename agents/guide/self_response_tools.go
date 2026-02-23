package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/providers"
	coreskills "github.com/adalundhe/sylk/core/skills"
)

func (g *Guide) prepareGuideSelfResponseTools(input string) []providers.Tool {
	if g == nil {
		return nil
	}
	defs := g.PrepareToolDefinitionsForInput(input)
	if len(defs) == 0 {
		return nil
	}
	tools := make([]providers.Tool, 0, len(defs))
	for _, def := range defs {
		tool, ok := guideToolDefinitionToProvider(def)
		if !ok {
			continue
		}
		tools = append(tools, tool)
	}
	return tools
}

func guideToolDefinitionToProvider(def map[string]any) (providers.Tool, bool) {
	name := strings.TrimSpace(anyToString(def["name"]))
	if name == "" {
		return providers.Tool{}, false
	}
	description := strings.TrimSpace(anyToString(def["description"]))
	parameters := coerceObjectMap(def["input_schema"])
	if len(parameters) == 0 {
		parameters = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	return providers.Tool{
		Name:        name,
		Description: description,
		Parameters:  parameters,
	}, true
}

func anyToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func coerceObjectMap(value any) map[string]any {
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

func (g *Guide) executeGuideSelfResponseToolCall(ctx context.Context, name string, arguments string) (string, error) {
	if g == nil || g.skills == nil {
		return "", fmt.Errorf("guide skills are not available")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("tool name is required")
	}

	raw := strings.TrimSpace(arguments)
	if raw == "" {
		raw = "{}"
	}
	if !json.Valid([]byte(raw)) {
		return "", fmt.Errorf("tool arguments for %q are not valid JSON", name)
	}

	inputMap := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &inputMap)
	if err := g.runGuidePreToolHooks(ctx, name, inputMap); err != nil {
		return "", err
	}

	result := g.skills.Invoke(ctx, name, json.RawMessage(raw))
	if result == nil {
		return "", fmt.Errorf("tool %q returned nil result", name)
	}
	if !result.Success {
		return "", fmt.Errorf("tool %q failed: %s", name, strings.TrimSpace(result.Error))
	}

	output, err := marshalGuideToolOutput(result.Data)
	if err != nil {
		return "", err
	}
	_ = g.runGuidePostToolHooks(ctx, name, inputMap, result.Data)
	return output, nil
}

func (g *Guide) runGuidePreToolHooks(ctx context.Context, name string, input map[string]any) error {
	if g == nil || g.hooks == nil {
		return nil
	}
	data := &coreskills.ToolCallHookData{
		ToolName: name,
		AgentID:  g.agentID,
		Input:    input,
	}
	_, hookResult, err := g.hooks.ExecutePreToolCallHooks(ctx, data)
	if err != nil {
		return err
	}
	if !hookResult.Continue {
		if hookResult.Error != nil {
			return hookResult.Error
		}
		return fmt.Errorf("tool call blocked: %s", name)
	}
	return nil
}

func (g *Guide) runGuidePostToolHooks(ctx context.Context, name string, input map[string]any, output any) error {
	if g == nil || g.hooks == nil {
		return nil
	}
	data := &coreskills.ToolCallHookData{
		ToolName: name,
		AgentID:  g.agentID,
		Input:    input,
		Output:   output,
	}
	_, _, err := g.hooks.ExecutePostToolCallHooks(ctx, data)
	return err
}

func marshalGuideToolOutput(data any) (string, error) {
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
