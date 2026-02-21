package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestOpenAIProviderGenerate_ChatGPTUsesCompletedResponse(t *testing.T) {
	if !canListenLocalTCP() {
		t.Skip("local TCP listeners are not permitted in this environment")
	}

	var (
		mu        sync.Mutex
		sawStream bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid json payload", http.StatusBadRequest)
			return
		}
		if stream, ok := payload["stream"].(bool); ok && stream {
			mu.Lock()
			sawStream = true
			mu.Unlock()
		}

		writeSSE(t, w,
			`{"type":"response.text.delta","delta":"partial ","item_id":"msg_1","output_index":0,"content_index":0,"sequence_number":1}`,
			`{"type":"response.text.delta","delta":"text","item_id":"msg_1","output_index":0,"content_index":0,"sequence_number":2}`,
			`{"type":"response.completed","sequence_number":3,"response":{"id":"resp_123","model":"gpt-5.3-codex","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"canonical answer"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
		)
	}))
	defer server.Close()

	cfg := DefaultOpenAIConfig()
	cfg.APIKey = "test-api-key"
	cfg.BaseURL = server.URL
	cfg.AuthMode = openAIAuthModeChatGPT
	cfg.ChatGPTAccountID = "acct_123"

	provider, err := NewOpenAIProvider(cfg)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	resp, err := provider.Generate(context.Background(), &Request{
		Messages: []Message{{Role: RoleUser, Content: "say hello"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if resp.Content != "canonical answer" {
		t.Fatalf("expected canonical completed response content, got %q", resp.Content)
	}
	if resp.Model != "gpt-5.3-codex" {
		t.Fatalf("expected model gpt-5.3-codex, got %q", resp.Model)
	}
	if resp.Usage.TotalTokens != 6 {
		t.Fatalf("expected total_tokens=6, got %d", resp.Usage.TotalTokens)
	}

	mu.Lock()
	streamEnabled := sawStream
	mu.Unlock()
	if !streamEnabled {
		t.Fatal("expected streaming request payload to set stream=true")
	}
}

func TestOpenAIProviderStreamWithHandler_EmitsToolAndTextChunks(t *testing.T) {
	if !canListenLocalTCP() {
		t.Skip("local TCP listeners are not permitted in this environment")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		writeSSE(t, w,
			`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"tool_1","type":"function_call","name":"run_test"}}`,
			`{"type":"response.function_call_arguments.delta","delta":"{\"path\":","item_id":"tool_1","output_index":0,"sequence_number":2}`,
			`{"type":"response.function_call_arguments.done","arguments":"{\"path\":\"/tmp\"}","item_id":"tool_1","output_index":0,"sequence_number":3}`,
			`{"type":"response.text.delta","delta":"done","item_id":"msg_1","output_index":1,"content_index":0,"sequence_number":4}`,
			`{"type":"response.completed","sequence_number":5,"response":{"id":"resp_456","model":"gpt-5.3-codex","output":[{"id":"tool_1","type":"function_call","name":"run_test","arguments":"{\"path\":\"/tmp\"}"},{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
		)
	}))
	defer server.Close()

	cfg := DefaultOpenAIConfig()
	cfg.APIKey = "test-api-key"
	cfg.BaseURL = server.URL

	provider, err := NewOpenAIProvider(cfg)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	var (
		startCount int
		text       string
		toolStarts int
		toolDeltas int
		toolEnds   int
		endUsage   Usage
		endSeen    bool
	)

	err = provider.StreamWithHandler(context.Background(), &Request{
		Messages: []Message{{Role: RoleUser, Content: "run tool"}},
	}, func(chunk *StreamChunk) error {
		switch chunk.Type {
		case ChunkTypeStart:
			startCount++
		case ChunkTypeText:
			text += chunk.Text
		case ChunkTypeToolStart:
			toolStarts++
		case ChunkTypeToolDelta:
			toolDeltas++
		case ChunkTypeToolEnd:
			toolEnds++
		case ChunkTypeEnd:
			endSeen = true
			if chunk.Usage != nil {
				endUsage = *chunk.Usage
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamWithHandler() error = %v", err)
	}

	if startCount != 1 {
		t.Fatalf("expected exactly one start chunk, got %d", startCount)
	}
	if text != "done" {
		t.Fatalf("expected streamed text %q, got %q", "done", text)
	}
	if toolStarts != 1 {
		t.Fatalf("expected one tool_start chunk, got %d", toolStarts)
	}
	if toolDeltas == 0 {
		t.Fatal("expected at least one tool_delta chunk")
	}
	if toolEnds != 1 {
		t.Fatalf("expected one tool_end chunk, got %d", toolEnds)
	}
	if !endSeen {
		t.Fatal("expected end chunk")
	}
	if endUsage.TotalTokens != 8 {
		t.Fatalf("expected end usage total_tokens=8, got %d", endUsage.TotalTokens)
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, events ...string) {
	t.Helper()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("expected http.ResponseWriter to implement http.Flusher")
	}

	for _, event := range events {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
			t.Fatalf("failed to write sse event: %v", err)
		}
		flusher.Flush()
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		t.Fatalf("failed to write sse done marker: %v", err)
	}
	flusher.Flush()
}

func canListenLocalTCP() bool {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
