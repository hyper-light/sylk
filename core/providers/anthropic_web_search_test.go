package providers

import (
	"encoding/json"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func TestAnthropicProviderConvertResponse_ExtractsNativeWebSearchCalls(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{Model: "claude-sonnet-4-6"},
		},
	}

	var msg anthropic.Message
	if err := json.Unmarshal([]byte(`{
		"id":"msg_test",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-6",
		"content":[
			{
				"type":"server_tool_use",
				"id":"srvtoolu_1",
				"name":"web_search",
				"caller":{"type":"direct"},
				"input":{"query":"python packaging pep 621"}
			}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`), &msg); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	resp := p.convertResponse(&msg)
	calls := DecodeNativeWebSearchCalls(resp.ProviderMetadata)
	if len(calls) != 1 {
		t.Fatalf("native web search call count = %d, want 1", len(calls))
	}
	if calls[0].Query != "python packaging pep 621" {
		t.Fatalf("query = %q, want python packaging pep 621", calls[0].Query)
	}
}

func TestAnthropicProviderConvertStreamEvent_ServerWebSearchEmitsToolChunks(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{Model: "claude-sonnet-4-6"},
		},
	}
	toolIDs := map[int64]string{}

	var start anthropic.MessageStreamEventUnion
	if err := json.Unmarshal([]byte(`{
		"type":"content_block_start",
		"index":0,
		"content_block":{
			"type":"server_tool_use",
			"id":"srvtoolu_1",
			"name":"web_search",
			"caller":{"type":"direct"},
			"input":{"query":"python packaging pep 621"}
		}
	}`), &start); err != nil {
		t.Fatalf("json.Unmarshal(start) error = %v", err)
	}
	startChunk := p.convertStreamEvent(start, 1, toolIDs)
	if startChunk == nil || startChunk.Type != ChunkTypeToolStart {
		t.Fatalf("start chunk = %#v, want tool_start", startChunk)
	}
	if startChunk.ToolCall == nil || startChunk.ToolCall.Name != "web_search" {
		t.Fatalf("start tool call = %#v, want web_search", startChunk.ToolCall)
	}

	var stop anthropic.MessageStreamEventUnion
	if err := json.Unmarshal([]byte(`{
		"type":"content_block_stop",
		"index":0
	}`), &stop); err != nil {
		t.Fatalf("json.Unmarshal(stop) error = %v", err)
	}
	stopChunk := p.convertStreamEvent(stop, 2, toolIDs)
	if stopChunk == nil || stopChunk.Type != ChunkTypeToolEnd {
		t.Fatalf("stop chunk = %#v, want tool_end", stopChunk)
	}
}
