package providers

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go/responses"
)

func TestOpenAIProviderConvertResponse_ExtractsNativeWebSearchCalls(t *testing.T) {
	p := &OpenAIProvider{config: OpenAIConfig{BaseConfig: BaseConfig{Model: "gpt-5.4-pro"}}}

	var result responses.Response
	if err := json.Unmarshal([]byte(`{
		"id":"resp_test",
		"model":"gpt-5.4-pro",
		"status":"completed",
		"output":[
			{
				"id":"ws_1",
				"type":"web_search_call",
				"status":"completed",
				"action":{"type":"search","query":"python packaging pep 621"}
			}
		],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`), &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	resp := p.convertResponse(&result)
	calls := DecodeNativeWebSearchCalls(resp.ProviderMetadata)
	if len(calls) != 1 {
		t.Fatalf("native web search call count = %d, want 1", len(calls))
	}
	if calls[0].Action != "search" {
		t.Fatalf("action = %q, want search", calls[0].Action)
	}
	if calls[0].Query != "python packaging pep 621" {
		t.Fatalf("query = %q, want python packaging pep 621", calls[0].Query)
	}
}

func TestOpenAIProviderConvertResponse_ExtractsNativeWebSearchResults(t *testing.T) {
	p := &OpenAIProvider{config: OpenAIConfig{BaseConfig: BaseConfig{Model: "gpt-5.4-pro"}}}

	var result responses.Response
	if err := json.Unmarshal([]byte(`{
		"id":"resp_test",
		"model":"gpt-5.4-pro",
		"status":"completed",
		"output":[
			{
				"id":"ws_1",
				"type":"web_search_call",
				"status":"completed",
				"action":{"type":"open_page","url":"https://docs.python.org/3/py-modindex.html"}
			},
			{
				"id":"msg_1",
				"type":"message",
				"role":"assistant",
				"status":"completed",
				"content":[
					{
						"type":"output_text",
						"text":"PEP 621 and the Python module index are relevant.",
						"annotations":[
							{
								"type":"url_citation",
								"start_index":0,
								"end_index":7,
								"title":"PEP 621",
								"url":"https://peps.python.org/pep-0621/"
							},
							{
								"type":"url_citation",
								"start_index":12,
								"end_index":30,
								"title":"Python Module Index",
								"url":"https://docs.python.org/3/py-modindex.html"
							}
						]
					}
				]
			}
		],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`), &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	resp := p.convertResponse(&result)
	results := DecodeNativeWebSearchResults(resp.ProviderMetadata)
	if len(results) != 2 {
		t.Fatalf("native web search result count = %d, want 2", len(results))
	}

	byURL := make(map[string]NativeWebSearchResult, len(results))
	for _, item := range results {
		byURL[item.URL] = item
	}

	pep := byURL["https://peps.python.org/pep-0621/"]
	if pep.Source != "url_citation" {
		t.Fatalf("PEP 621 source = %q, want url_citation", pep.Source)
	}
	if pep.Title != "PEP 621" {
		t.Fatalf("PEP 621 title = %q, want PEP 621", pep.Title)
	}

	docs := byURL["https://docs.python.org/3/py-modindex.html"]
	if docs.Source != "open_page" {
		t.Fatalf("docs source = %q, want open_page", docs.Source)
	}
	if docs.SearchID != "ws_1" {
		t.Fatalf("docs search ID = %q, want ws_1", docs.SearchID)
	}
	if docs.Title != "Python Module Index" {
		t.Fatalf("docs title = %q, want Python Module Index", docs.Title)
	}
}

func TestOpenAIResponseStreamRunner_WebSearchOutputItemsEmitToolChunks(t *testing.T) {
	var chunks []*StreamChunk
	runner := &openAIResponseStreamRunner{
		handler:          func(chunk *StreamChunk) error { chunks = append(chunks, chunk); return nil },
		toolCallBuilders: make(map[string]*streamToolCallState),
		toolCallStarted:  make(map[string]bool),
		toolCallEnded:    make(map[string]bool),
	}

	var added responses.ResponseOutputItemAddedEvent
	if err := json.Unmarshal([]byte(`{
		"type":"response.output_item.added",
		"output_index":0,
		"sequence_number":1,
		"item":{
			"id":"ws_1",
			"type":"web_search_call",
			"status":"in_progress",
			"action":{"type":"search","query":"python packaging pep 621"}
		}
	}`), &added); err != nil {
		t.Fatalf("json.Unmarshal(added) error = %v", err)
	}
	if err := runner.handleOutputItemAddedEvent(added); err != nil {
		t.Fatalf("handleOutputItemAddedEvent() error = %v", err)
	}

	var done responses.ResponseOutputItemDoneEvent
	if err := json.Unmarshal([]byte(`{
		"type":"response.output_item.done",
		"output_index":0,
		"sequence_number":2,
		"item":{
			"id":"ws_1",
			"type":"web_search_call",
			"status":"completed",
			"action":{"type":"search","query":"python packaging pep 621"}
		}
	}`), &done); err != nil {
		t.Fatalf("json.Unmarshal(done) error = %v", err)
	}
	if err := runner.handleOutputItemDoneEvent(done); err != nil {
		t.Fatalf("handleOutputItemDoneEvent() error = %v", err)
	}

	if len(chunks) != 2 {
		t.Fatalf("chunk count = %d, want 2", len(chunks))
	}
	if chunks[0].Type != ChunkTypeToolStart {
		t.Fatalf("start chunk type = %q, want %q", chunks[0].Type, ChunkTypeToolStart)
	}
	if chunks[1].Type != ChunkTypeToolEnd {
		t.Fatalf("end chunk type = %q, want %q", chunks[1].Type, ChunkTypeToolEnd)
	}
	if chunks[0].ToolCall == nil || chunks[0].ToolCall.Name != "web_search" {
		t.Fatalf("start tool call = %#v, want web_search", chunks[0].ToolCall)
	}
	if got := chunks[0].ToolCall.ArgumentsDelta; got == "" || got == "{}" {
		t.Fatalf("start arguments = %q, want populated JSON", got)
	}
}
