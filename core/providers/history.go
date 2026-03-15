package providers

import (
	"encoding/json"
	"strings"
)

// ToolLoopAssistantMessage builds a history-safe assistant message for a
// tool-using turn. Invalid tool arguments are preserved as structured payloads
// so later provider requests cannot fail while marshalling tool_use blocks.
func ToolLoopAssistantMessage(resp *Response) Message {
	if resp == nil {
		return Message{Role: RoleAssistant}
	}
	return Message{
		Role:      RoleAssistant,
		Content:   strings.TrimSpace(resp.Content),
		ToolCalls: SanitizeToolCallsForHistory(resp.ToolCalls),
		Metadata:  sanitizeProviderMetadataForHistory(resp.ProviderMetadata),
	}
}

// SanitizeToolCallsForHistory clones tool calls and replaces malformed JSON
// arguments with a valid JSON object that preserves the raw payload.
func SanitizeToolCallsForHistory(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	safe := make([]ToolCall, len(calls))
	for i, call := range calls {
		safe[i] = call
		safe[i].Arguments = sanitizeToolArgumentsForHistory(call.Arguments)
	}
	return safe
}

func sanitizeToolArgumentsForHistory(arguments string) string {
	raw := strings.TrimSpace(arguments)
	if raw == "" {
		return "{}"
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	payload, err := json.Marshal(map[string]any{
		"_invalid_tool_arguments": true,
		"raw_arguments":           raw,
	})
	if err != nil {
		return `{"_invalid_tool_arguments":true}`
	}
	return string(payload)
}

func sanitizeProviderMetadataForHistory(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	if raw, ok := cloned[anthropicRawContentKey]; ok {
		if blocks := decodeAnthropicBlocks(raw); len(blocks) > 0 {
			safeBlocks := make([]anthropicContentBlock, len(blocks))
			copy(safeBlocks, blocks)
			for i := range safeBlocks {
				if safeBlocks[i].Type == "tool_use" {
					safeBlocks[i].ToolInput = sanitizeToolArgumentsForHistory(safeBlocks[i].ToolInput)
				}
			}
			cloned[anthropicRawContentKey] = safeBlocks
		}
	}
	return cloned
}
