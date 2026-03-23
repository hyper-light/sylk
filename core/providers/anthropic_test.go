package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestAnthropicBuildParams_IncludesNativeWebSearchTool(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				Model:     "claude-sonnet-4-6",
				MaxTokens: 2048,
			},
		},
	}

	params := p.buildParams(&Request{
		SystemPrompt: "research thoroughly",
		Messages:     []Message{{Role: RoleUser, Content: "research Go error handling"}},
		Tools: []Tool{
			{
				Kind: ToolKindNativeWebSearch,
				Name: "web_search",
				WebSearch: &WebSearchOptions{
					AllowedDomains: []string{"go.dev", "pkg.go.dev"},
					BlockedDomains: []string{"reddit.com"},
					MaxUses:        3,
					Strict:         true,
					UserLocation: &WebSearchUserLocation{
						Country:  "US",
						Timezone: "America/Chicago",
					},
				},
			},
		},
	})

	body := marshalAnthropicParams(t, params)
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool in marshaled params, got %#v", body["tools"])
	}

	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("expected tool object, got %#v", tools[0])
	}
	if tool["type"] != "web_search_20260209" {
		t.Fatalf("expected native web search type, got %#v", tool["type"])
	}
	if tool["name"] != "web_search" {
		t.Fatalf("expected tool name web_search, got %#v", tool["name"])
	}
	if got := tool["max_uses"]; got != float64(3) {
		t.Fatalf("expected max_uses 3, got %#v", got)
	}
	if got := tool["strict"]; got != true {
		t.Fatalf("expected strict=true, got %#v", got)
	}
}

func marshalAnthropicParams(t *testing.T, params any) map[string]any {
	t.Helper()

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return body
}

func TestResolveAnthropicAuthMode(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		pref       string
		hasAPIKey  bool
		hasOAuth   bool
		want       string
	}{
		{
			name:       "configured apikey normalizes",
			configured: "apikey",
			want:       AnthropicAuthModeAPIKey,
		},
		{
			name:       "configured oauth wins",
			configured: AnthropicAuthModeOAuth,
			pref:       AnthropicAuthModeAPIKey,
			hasAPIKey:  true,
			want:       AnthropicAuthModeOAuth,
		},
		{
			name:      "stored preference wins",
			pref:      AnthropicAuthModeOAuth,
			hasAPIKey: true,
			want:      AnthropicAuthModeOAuth,
		},
		{
			name:      "api key preferred without preference",
			hasAPIKey: true,
			hasOAuth:  true,
			want:      AnthropicAuthModeAPIKey,
		},
		{
			name:     "oauth used when only oauth exists",
			hasOAuth: true,
			want:     AnthropicAuthModeOAuth,
		},
		{
			name: "defaults to api key",
			want: AnthropicAuthModeAPIKey,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveAnthropicAuthMode(tc.configured, tc.pref, tc.hasAPIKey, tc.hasOAuth)
			if got != tc.want {
				t.Fatalf("resolveAnthropicAuthMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAnthropicProviderGenerate_Retries500ForAPIKeyAndOAuth(t *testing.T) {
	tests := []struct {
		name     string
		authMode string
	}{
		{name: "api_key", authMode: AnthropicAuthModeAPIKey},
		{name: "oauth", authMode: AnthropicAuthModeOAuth},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.Header().Set("Content-Type", "application/json")
				if attempts <= anthropicInternalServerMaxRetries {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"internal server error"},"request_id":"req_test"}`))
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{
					"id":"msg_test",
					"type":"message",
					"role":"assistant",
					"model":"claude-sonnet-4-6",
					"content":[{"type":"text","text":"ok"}],
					"stop_reason":"end_turn",
					"usage":{"input_tokens":1,"output_tokens":1}
				}`))
			}))
			defer server.Close()

			cfg := AnthropicConfig{
				BaseConfig: BaseConfig{
					APIKey:         "test-key",
					Model:          "claude-sonnet-4-6",
					MaxTokens:      64,
					Timeout:        time.Second,
					RetryBaseDelay: time.Millisecond,
					RetryMaxDelay:  10 * time.Millisecond,
				},
				AuthMode: tt.authMode,
				BaseURL:  server.URL,
			}

			ctx := contextWithRetryRecorder(t)
			p, err := NewAnthropicProviderWithAuthService(ctx.ctx, cfg, nil)
			if err != nil {
				t.Fatalf("NewAnthropicProviderWithAuthService() error = %v", err)
			}

			resp, err := p.Generate(ctx.ctx, &Request{
				Messages:  []Message{{Role: RoleUser, Content: "hello"}},
				MaxTokens: 16,
			})
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if resp == nil || resp.Content != "ok" {
				t.Fatalf("unexpected response: %#v", resp)
			}
			if attempts != anthropicInternalServerMaxRetries+1 {
				t.Fatalf("expected %d attempts, got %d", anthropicInternalServerMaxRetries+1, attempts)
			}
			if len(ctx.events) != anthropicInternalServerMaxRetries {
				t.Fatalf("expected %d retry events, got %d", anthropicInternalServerMaxRetries, len(ctx.events))
			}
			for i, event := range ctx.events {
				if event.Attempt != i+1 {
					t.Fatalf("event %d attempt = %d, want %d", i, event.Attempt, i+1)
				}
				if event.MaxAttempts != anthropicInternalServerMaxRetries {
					t.Fatalf("event %d max attempts = %d, want %d", i, event.MaxAttempts, anthropicInternalServerMaxRetries)
				}
			}
		})
	}
}

func TestAnthropicProviderGenerate_DoesNotRetryNon500(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "429", status: http.StatusTooManyRequests},
		{name: "503", status: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"request failed"},"request_id":"req_test"}`))
			}))
			defer server.Close()

			cfg := AnthropicConfig{
				BaseConfig: BaseConfig{
					APIKey:         "test-key",
					Model:          "claude-sonnet-4-6",
					MaxTokens:      64,
					Timeout:        time.Second,
					RetryBaseDelay: time.Millisecond,
					RetryMaxDelay:  10 * time.Millisecond,
				},
				AuthMode: AnthropicAuthModeAPIKey,
				BaseURL:  server.URL,
			}

			ctx := contextWithRetryRecorder(t)
			p, err := NewAnthropicProviderWithAuthService(ctx.ctx, cfg, nil)
			if err != nil {
				t.Fatalf("NewAnthropicProviderWithAuthService() error = %v", err)
			}

			_, err = p.Generate(ctx.ctx, &Request{
				Messages:  []Message{{Role: RoleUser, Content: "hello"}},
				MaxTokens: 16,
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if attempts != 1 {
				t.Fatalf("expected 1 attempt, got %d", attempts)
			}
			if len(ctx.events) != 0 {
				t.Fatalf("expected no retry events, got %d", len(ctx.events))
			}
		})
	}
}

func TestAnthropicProviderGenerate_OAuthSendsRequiredHeadersOnMessages(t *testing.T) {
	var gotAuthorization string
	var gotBetaHeader string
	var gotUserAgent string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotBetaHeader = r.Header.Get("anthropic-beta")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":"msg_test",
			"type":"message",
			"role":"assistant",
			"model":"claude-opus-4-6",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer server.Close()

	cfg := AnthropicConfig{
		BaseConfig: BaseConfig{
			APIKey:    "oauth-access-token",
			Model:     "claude-opus-4-6",
			MaxTokens: 64,
			Timeout:   time.Second,
		},
		AuthMode: AnthropicAuthModeOAuth,
		BaseURL:  server.URL,
	}

	p, err := NewAnthropicProviderWithAuthService(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("NewAnthropicProviderWithAuthService() error = %v", err)
	}

	resp, err := p.Generate(context.Background(), &Request{
		Messages:  []Message{{Role: RoleUser, Content: "hello"}},
		MaxTokens: 16,
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if resp == nil || resp.Content != "ok" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if gotAuthorization != "Bearer oauth-access-token" {
		t.Fatalf("authorization header = %q, want %q", gotAuthorization, "Bearer oauth-access-token")
	}
	for _, required := range []string{
		string(anthropic.AnthropicBetaInterleavedThinking2025_05_14),
		"oauth-2025-04-20",
		"claude-code-20250219",
		"fine-grained-tool-streaming-2025-05-14",
	} {
		if !strings.Contains(gotBetaHeader, required) {
			t.Fatalf("anthropic-beta header = %q, want it to include %q on messages requests", gotBetaHeader, required)
		}
	}
	if !strings.Contains(gotUserAgent, "claude-cli/2.1.2") {
		t.Fatalf("user-agent header = %q, want claude-cli oauth user-agent", gotUserAgent)
	}
}

type retryRecorder struct {
	ctx    context.Context
	events []RetryEvent
}

func contextWithRetryRecorder(t *testing.T) *retryRecorder {
	t.Helper()
	rec := &retryRecorder{}
	rec.ctx = WithRetryObserver(context.Background(), func(event RetryEvent) {
		rec.events = append(rec.events, event)
	})
	return rec
}
