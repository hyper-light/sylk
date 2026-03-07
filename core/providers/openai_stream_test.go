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
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/oauth"
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

		if err := writeSSE(w,
			`{"type":"response.output_text.delta","delta":"partial ","item_id":"msg_1","output_index":0,"content_index":0,"sequence_number":1}`,
			`{"type":"response.output_text.delta","delta":"text","item_id":"msg_1","output_index":0,"content_index":0,"sequence_number":2}`,
			`{"type":"response.completed","sequence_number":3,"response":{"id":"resp_123","model":"gpt-5.4-pro","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"canonical answer"}]}],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	cfg := DefaultOpenAIConfig()
	cfg.APIKey = "test-api-key"
	cfg.BaseURL = server.URL
	cfg.AuthMode = openAIAuthModeChatGPT
	cfg.ChatGPTAccountID = "acct_123"

	provider, err := NewOpenAIProvider(context.Background(), cfg)
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
	if resp.Model != "gpt-5.4-pro" {
		t.Fatalf("expected model gpt-5.4-pro, got %q", resp.Model)
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

		if err := writeSSE(w,
			`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"tool_1","type":"function_call","name":"run_test"}}`,
			`{"type":"response.function_call_arguments.delta","delta":"{\"path\":","item_id":"tool_1","output_index":0,"sequence_number":2}`,
			`{"type":"response.function_call_arguments.done","arguments":"{\"path\":\"/tmp\"}","item_id":"tool_1","output_index":0,"sequence_number":3}`,
			`{"type":"response.output_text.delta","delta":"done","item_id":"msg_1","output_index":1,"content_index":0,"sequence_number":4}`,
			`{"type":"response.completed","sequence_number":5,"response":{"id":"resp_456","model":"gpt-5.4-pro","output":[{"id":"tool_1","type":"function_call","name":"run_test","arguments":"{\"path\":\"/tmp\"}"},{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	cfg := DefaultOpenAIConfig()
	cfg.APIKey = "test-api-key"
	cfg.BaseURL = server.URL

	provider, err := NewOpenAIProvider(context.Background(), cfg)
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

func TestOpenAIProviderStreamWithHandler_EmitsReasoningChunks(t *testing.T) {
	if !canListenLocalTCP() {
		t.Skip("local TCP listeners are not permitted in this environment")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := writeSSE(w,
			`{"type":"response.reasoning_summary_text.delta","delta":"considering options","item_id":"rsn_1","output_index":0,"summary_index":0,"sequence_number":1}`,
			`{"type":"response.output_text.delta","delta":"final answer","item_id":"msg_1","output_index":1,"content_index":0,"sequence_number":2}`,
			`{"type":"response.completed","sequence_number":3,"response":{"id":"resp_reasoning","model":"gpt-5.4-pro","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"final answer"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	cfg := DefaultOpenAIConfig()
	cfg.APIKey = "test-api-key"
	cfg.BaseURL = server.URL

	provider, err := NewOpenAIProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	var thought string
	err = provider.StreamWithHandler(context.Background(), &Request{
		Messages: []Message{{Role: RoleUser, Content: "think"}},
	}, func(chunk *StreamChunk) error {
		if chunk.Type == ChunkTypeThought {
			thought += chunk.Text
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamWithHandler() error = %v", err)
	}

	if thought != "considering options" {
		t.Fatalf("thought = %q, want %q", thought, "considering options")
	}
}

type refreshingAuthService struct {
	mu          sync.Mutex
	currentAuth *oauth.OpenAIChatGPTAuth
	resolveN    int
	refreshN    int
	saveN       int
}

func (m *refreshingAuthService) BeginDeviceAuth(context.Context) (*oauth.DeviceCodeChallenge, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *refreshingAuthService) CompleteDeviceAuth(context.Context, *oauth.DeviceCodeChallenge, time.Duration) (*oauth.OpenAIChatGPTAuth, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *refreshingAuthService) Refresh(context.Context, *oauth.OpenAIChatGPTAuth) (*oauth.OpenAIChatGPTAuth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshN++
	updated := *m.currentAuth
	updated.AccessToken = "new_access"
	updated.RefreshToken = "refresh_new"
	updated.ChatGPTAccountID = "acct_123"
	return &updated, nil
}

func (m *refreshingAuthService) Resolve(context.Context) (*oauth.OpenAIChatGPTAuth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolveN++
	auth := *m.currentAuth
	return &auth, nil
}

func (m *refreshingAuthService) Save(_ context.Context, auth *oauth.OpenAIChatGPTAuth) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveN++
	m.currentAuth = auth
	return nil
}

func (m *refreshingAuthService) Load(context.Context) (*oauth.OpenAIChatGPTAuth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.currentAuth == nil {
		return nil, oauth.ErrAuthNotConfigured
	}
	auth := *m.currentAuth
	return &auth, nil
}

func (m *refreshingAuthService) Delete(context.Context) error {
	return nil
}

func TestOpenAIProviderGenerate_ChatGPTRetryOnUnauthorizedRefreshesAuth(t *testing.T) {
	if !canListenLocalTCP() {
		t.Skip("local TCP listeners are not permitted in this environment")
	}

	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		attempt := atomic.AddInt32(&requestCount, 1)
		authHeader := r.Header.Get("Authorization")
		if attempt == 1 {
			if authHeader != "Bearer old_access" {
				http.Error(w, "unexpected auth header on first attempt", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"expired token","code":"invalid_api_key"}}`))
			return
		}

		if authHeader != "Bearer new_access" {
			http.Error(w, "missing refreshed auth header", http.StatusUnauthorized)
			return
		}
		w.Header().Set("x-request-id", "req_after_refresh")
		if err := writeSSE(w,
			`{"type":"response.completed","sequence_number":1,"response":{"id":"resp_refreshed","model":"gpt-5.4-pro","status":"completed","service_tier":"default","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"after refresh"}]}],"usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}}`,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	authSvc := &refreshingAuthService{
		currentAuth: &oauth.OpenAIChatGPTAuth{
			AuthMode:         "chatgpt",
			AccessToken:      "old_access",
			RefreshToken:     "refresh_old",
			ChatGPTAccountID: "acct_123",
		},
	}

	cfg := DefaultOpenAIConfig()
	cfg.AuthMode = openAIAuthModeChatGPT
	cfg.APIKey = "old_access"
	cfg.ChatGPTAccountID = "acct_123"
	cfg.BaseURL = server.URL

	provider, err := NewOpenAIProviderWithAuthService(context.Background(), cfg, authSvc)
	if err != nil {
		t.Fatalf("NewOpenAIProviderWithAuthService() error = %v", err)
	}

	resp, err := provider.Generate(context.Background(), &Request{
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if resp.Content != "after refresh" {
		t.Fatalf("unexpected response content: %q", resp.Content)
	}
	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("expected exactly two attempts, got %d", got)
	}

	authSvc.mu.Lock()
	defer authSvc.mu.Unlock()
	if authSvc.resolveN == 0 || authSvc.refreshN == 0 || authSvc.saveN == 0 {
		t.Fatalf("expected resolve/refresh/save to be called, got resolve=%d refresh=%d save=%d", authSvc.resolveN, authSvc.refreshN, authSvc.saveN)
	}
}

func TestOpenAIProviderStreamWithHandler_ToolEndOrderByOutputIndex(t *testing.T) {
	if !canListenLocalTCP() {
		t.Skip("local TCP listeners are not permitted in this environment")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := writeSSE(w,
			`{"type":"response.output_item.added","sequence_number":1,"output_index":2,"item":{"id":"tool_b","type":"function_call","name":"beta"}}`,
			`{"type":"response.output_item.added","sequence_number":2,"output_index":1,"item":{"id":"tool_a","type":"function_call","name":"alpha"}}`,
			`{"type":"response.completed","sequence_number":3,"response":{"id":"resp_order","model":"gpt-5.4-pro","status":"completed","output":[{"id":"tool_a","type":"function_call","name":"alpha","arguments":"{}"},{"id":"tool_b","type":"function_call","name":"beta","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	cfg := DefaultOpenAIConfig()
	cfg.APIKey = "test-api-key"
	cfg.BaseURL = server.URL

	provider, err := NewOpenAIProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}

	endIDs := make([]string, 0, 2)
	err = provider.StreamWithHandler(context.Background(), &Request{
		Messages: []Message{{Role: RoleUser, Content: "run tools"}},
	}, func(chunk *StreamChunk) error {
		if chunk.Type == ChunkTypeToolEnd && chunk.ToolCall != nil {
			endIDs = append(endIDs, chunk.ToolCall.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamWithHandler() error = %v", err)
	}

	if len(endIDs) != 2 {
		t.Fatalf("expected exactly two tool_end chunks, got %v", endIDs)
	}
	if endIDs[0] != "tool_a" || endIDs[1] != "tool_b" {
		t.Fatalf("expected tool_end ordering by output_index [tool_a tool_b], got %v", endIDs)
	}
}

func writeSSE(w http.ResponseWriter, events ...string) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("expected http.ResponseWriter to implement http.Flusher")
	}

	for _, event := range events {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", event); err != nil {
			return fmt.Errorf("failed to write sse event: %w", err)
		}
		flusher.Flush()
	}
	if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
		return fmt.Errorf("failed to write sse done marker: %w", err)
	}
	flusher.Flush()
	return nil
}

func canListenLocalTCP() bool {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
