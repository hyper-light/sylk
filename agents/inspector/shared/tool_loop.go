package shared

import (
	"encoding/json"
	"fmt"

	agentShared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

type ToolCallSignature = agentShared.ToolCallSignature

const (
	preparePipelineWriteContextTool = "prepare_pipeline_write_context"
	prepareGlobalWriteContextTool   = "prepare_global_write_context"
)

func DetectToolCallDuplicate(
	calls []providers.ToolCall,
	seen map[ToolCallSignature]int,
	history ...[]providers.Message,
) (bool, ToolCallSignature) {
	return agentShared.DetectToolCallDuplicate(calls, seen, history...)
}

func UpdateToolErrors(current, errCount, totalCalls int) int {
	return agentShared.UpdateToolErrors(current, errCount, totalCalls)
}

func MarshalToolOutput(data any) (string, error) {
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

func ToolErrorPayload(err error) string {
	return agentShared.ToolErrorPayload(err)
}

func CoerceMap(value any) map[string]any {
	return agentShared.CoerceMap(value)
}

func BuildToolDefinitions(loaded []*skills.Skill) []providers.Tool {
	if len(loaded) == 0 {
		return nil
	}
	tools := make([]providers.Tool, 0, len(loaded))
	for _, skill := range loaded {
		tool, ok := skillToolDefinition(skill)
		if ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

func skillToolDefinition(skill *skills.Skill) (providers.Tool, bool) {
	def := skill.ToToolDefinition()
	name, _ := def["name"].(string)
	if name == "" {
		return providers.Tool{}, false
	}
	description, _ := def["description"].(string)
	parameters := CoerceMap(def["input_schema"])
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
