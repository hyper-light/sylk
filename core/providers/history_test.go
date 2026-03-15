package providers

import (
	"encoding/json"
	"testing"
)

func TestToolLoopAssistantMessage_SanitizesInvalidToolArguments(t *testing.T) {
	msg := ToolLoopAssistantMessage(&Response{
		Content: "handoff",
		ToolCalls: []ToolCall{{
			ID:        "tc_1",
			Name:      "handoff_next",
			Arguments: `{"target_agents":["tester"],`,
		}},
	})

	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(msg.ToolCalls))
	}
	if !json.Valid([]byte(msg.ToolCalls[0].Arguments)) {
		t.Fatalf("sanitized arguments are not valid JSON: %q", msg.ToolCalls[0].Arguments)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(msg.ToolCalls[0].Arguments), &payload); err != nil {
		t.Fatalf("unmarshal sanitized arguments: %v", err)
	}
	if payload["_invalid_tool_arguments"] != true {
		t.Fatalf("missing invalid marker in %#v", payload)
	}
	if payload["raw_arguments"] != `{"target_agents":["tester"],` {
		t.Fatalf("raw_arguments = %#v", payload["raw_arguments"])
	}
}

func TestToolLoopAssistantMessage_SanitizesAnthropicRawContent(t *testing.T) {
	msg := ToolLoopAssistantMessage(&Response{
		ToolCalls: []ToolCall{{
			ID:        "tc_1",
			Name:      "handoff_next",
			Arguments: `{"target_agents":["tester"]}`,
		}},
		ProviderMetadata: map[string]any{
			anthropicRawContentKey: []anthropicContentBlock{
				{
					Type:      "tool_use",
					ToolID:    "tc_1",
					ToolName:  "handoff_next",
					ToolInput: `{"target_agents":["tester"],`,
				},
			},
		},
	})

	blocks := decodeAnthropicBlocks(msg.Metadata[anthropicRawContentKey])
	if len(blocks) != 1 {
		t.Fatalf("block count = %d, want 1", len(blocks))
	}
	if !json.Valid([]byte(blocks[0].ToolInput)) {
		t.Fatalf("sanitized tool_input is not valid JSON: %q", blocks[0].ToolInput)
	}
}
