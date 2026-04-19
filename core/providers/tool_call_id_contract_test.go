package providers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/genai"
)

// TestAnthropicProvider_ToolCallsAlwaysCarryID verifies that even when the
// upstream Anthropic message somehow omits a tool_use ID, the conversion path
// synthesizes one at the adapter boundary. This protects the tool-call
// lifecycle ID invariant that downstream code relies on for UI pairing.
func TestAnthropicProvider_ToolCallsAlwaysCarryID(t *testing.T) {
	p := &AnthropicProvider{config: AnthropicConfig{BaseConfig: BaseConfig{Model: "claude-sonnet-4-6"}}}

	var msg anthropic.Message
	if err := json.Unmarshal([]byte(`{
		"id":"msg_test",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-6",
		"content":[
			{"type":"tool_use","id":"","name":"read_file","input":{"path":"a.go"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`), &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	resp := p.convertResponse(&msg)
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(resp.ToolCalls))
	}
	if strings.TrimSpace(resp.ToolCalls[0].ID) == "" {
		t.Fatalf("Anthropic convertResponse emitted a ToolCall with an empty ID: %#v", resp.ToolCalls[0])
	}
}

// TestAnthropicProvider_StreamChunkToolCallsAlwaysCarryID does the same for the
// streaming conversion path, where ContentBlockStart events for tool_use blocks
// must produce a non-empty ID on the resulting ToolCallChunk.
func TestAnthropicProvider_StreamChunkToolCallsAlwaysCarryID(t *testing.T) {
	p := &AnthropicProvider{config: AnthropicConfig{BaseConfig: BaseConfig{Model: "claude-sonnet-4-6"}}}

	var event anthropic.MessageStreamEventUnion
	if err := json.Unmarshal([]byte(`{
		"type":"content_block_start",
		"index":0,
		"content_block":{"type":"tool_use","id":"","name":"read_file","input":{}}
	}`), &event); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	idMap := map[int64]string{}
	chunk := p.convertStreamEvent(event, 1, idMap)
	if chunk == nil || chunk.ToolCall == nil {
		t.Fatalf("chunk = %#v, want tool start chunk", chunk)
	}
	if strings.TrimSpace(chunk.ToolCall.ID) == "" {
		t.Fatalf("Anthropic stream ToolStart emitted empty ToolCallChunk.ID: %#v", chunk.ToolCall)
	}
	// The index→ID map must also carry the minted ID so subsequent delta/stop
	// chunks refer to the same lifecycle identity.
	if strings.TrimSpace(idMap[0]) == "" {
		t.Fatalf("toolCallIDForIndex missing minted ID; map=%#v", idMap)
	}
	if idMap[0] != chunk.ToolCall.ID {
		t.Fatalf("toolCallIDForIndex[0] = %q, want %q (chunk ID)", idMap[0], chunk.ToolCall.ID)
	}
}

// TestOpenAIStreamAssembler_EnsureToolCallMintsEmptyIDs verifies that the
// OpenAI streaming assembler produces non-empty IDs even if an event arrives
// without one. ensureToolCall is the single chokepoint for all tool-call
// state built during streaming, so this guards the whole OpenAI stream path.
func TestOpenAIStreamAssembler_EnsureToolCallMintsEmptyIDs(t *testing.T) {
	a := &streamResponseAssembler{
		toolCalls: map[string]*ToolCall{},
	}
	tc := a.ensureToolCall("")
	if tc == nil {
		t.Fatal("ensureToolCall returned nil for empty id")
	}
	if strings.TrimSpace(tc.ID) == "" {
		t.Fatalf("ensureToolCall synthesized empty ID: %#v", tc)
	}
	// Re-calling with a provider-supplied ID must yield a distinct slot.
	other := a.ensureToolCall("tc_real")
	if other == nil || other == tc {
		t.Fatalf("ensureToolCall('tc_real') did not allocate a fresh record: %#v", other)
	}
	if strings.TrimSpace(other.ID) == "" {
		t.Fatalf("ensureToolCall passed-through ID became empty: %#v", other)
	}
}

// TestGoogleProvider_ConvertResponseToolCallsAlwaysCarryID verifies that the
// Google conversion path mints IDs for function calls Google did not tag.
func TestGoogleProvider_ConvertResponseToolCallsAlwaysCarryID(t *testing.T) {
	p := &GoogleProvider{}

	result := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{{
					FunctionCall: &genai.FunctionCall{
						// Deliberately leave ID empty — adapter must synthesize.
						Name: "search_code",
						Args: map[string]any{"query": "needle"},
					},
				}},
			},
		}},
	}

	resp := p.convertResponse(result, "gemini-test")
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1: %#v", len(resp.ToolCalls), resp.ToolCalls)
	}
	if strings.TrimSpace(resp.ToolCalls[0].ID) == "" {
		t.Fatalf("Google convertResponse emitted a ToolCall with an empty ID: %#v", resp.ToolCalls[0])
	}
}
