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

func TestAnthropicProviderConvertResponse_ExtractsNativeWebSearchResults(t *testing.T) {
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
			},
			{
				"type":"web_search_tool_result",
				"tool_use_id":"srvtoolu_1",
				"caller":{"type":"direct"},
				"content":[
					{
						"type":"web_search_result",
						"title":"PEP 621",
						"url":"https://peps.python.org/pep-0621/",
						"page_age":"30d",
						"encrypted_content":"enc_1"
					},
					{
						"type":"web_search_result",
						"title":"Python Packaging User Guide",
						"url":"https://packaging.python.org/en/latest/specifications/declaring-project-metadata/",
						"page_age":"10d",
						"encrypted_content":"enc_2"
					}
				]
			}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`), &msg); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	resp := p.convertResponse(&msg)
	results := DecodeNativeWebSearchResults(resp.ProviderMetadata)
	if len(results) != 2 {
		t.Fatalf("native web search result count = %d, want 2", len(results))
	}

	byURL := make(map[string]NativeWebSearchResult, len(results))
	for _, item := range results {
		byURL[item.URL] = item
	}

	pep := byURL["https://peps.python.org/pep-0621/"]
	if pep.Query != "python packaging pep 621" {
		t.Fatalf("PEP 621 query = %q, want python packaging pep 621", pep.Query)
	}
	if pep.Source != "search_result" {
		t.Fatalf("PEP 621 source = %q, want search_result", pep.Source)
	}
	if pep.PageAge != "30d" {
		t.Fatalf("PEP 621 page age = %q, want 30d", pep.PageAge)
	}

	packaging := byURL["https://packaging.python.org/en/latest/specifications/declaring-project-metadata/"]
	if packaging.Title != "Python Packaging User Guide" {
		t.Fatalf("packaging title = %q, want Python Packaging User Guide", packaging.Title)
	}
	if packaging.SearchID != "srvtoolu_1" {
		t.Fatalf("packaging search ID = %q, want srvtoolu_1", packaging.SearchID)
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
