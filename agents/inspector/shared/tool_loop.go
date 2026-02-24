package shared

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/skills"
)

// ToolCallSignature identifies a unique tool invocation for dedup detection.
type ToolCallSignature struct {
	Name      string
	Arguments string
}

// DetectToolCallDuplicate checks if ALL tool calls in the batch are repeats.
// Returns true only when every call has been seen before (identical batch).
func DetectToolCallDuplicate(calls []providers.ToolCall, seen map[ToolCallSignature]int) (bool, ToolCallSignature) {
	batch := make([]ToolCallSignature, 0, len(calls))
	for _, call := range calls {
		sig := ToolCallSignature{
			Name:      strings.TrimSpace(call.Name),
			Arguments: strings.TrimSpace(call.Arguments),
		}
		batch = append(batch, sig)
	}

	allDup := true
	var firstDup ToolCallSignature
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

// UpdateToolErrors tracks consecutive full-failure turns.
// Returns current+1 if all calls in the turn errored, otherwise resets to 0.
func UpdateToolErrors(current, errCount, totalCalls int) int {
	if totalCalls > 0 && errCount == totalCalls {
		return current + 1
	}
	return 0
}

// MarshalToolOutput converts tool output to a JSON string.
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

// ToolErrorPayload wraps an error into a JSON string for the tool response.
func ToolErrorPayload(err error) string {
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

// CoerceMap converts a value to map[string]any, marshaling through JSON if needed.
func CoerceMap(value any) map[string]any {
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

// BuildToolDefinitions converts loaded skills to the provider tool format.
func BuildToolDefinitions(loaded []*skills.Skill) []providers.Tool {
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
		parameters := CoerceMap(def["input_schema"])
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
