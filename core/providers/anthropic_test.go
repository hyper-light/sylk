package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestHTTPClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func jsonResponse(status int, body string, req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: req,
	}
}

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

func TestAnthropicBuildParams_AlwaysUsesAdaptiveThinking(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				Model:     "claude-sonnet-4-6",
				MaxTokens: 512,
			},
		},
	}

	params := p.buildParams(&Request{
		Messages:       []Message{{Role: RoleUser, Content: "hello"}},
		MaxTokens:      512,
		ThinkingBudget: 256,
	})

	body := marshalAnthropicParams(t, params)
	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %#v, want adaptive object", body["thinking"])
	}
	if got := thinking["type"]; got != "adaptive" {
		t.Fatalf("thinking.type = %#v, want \"adaptive\"", got)
	}
	if _, leaked := thinking["budget_tokens"]; leaked {
		t.Fatalf("adaptive thinking must not carry budget_tokens: %#v", thinking)
	}
}

func TestAnthropicBuildParams_StampsOutputConfigEffortMax(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				Model:     "claude-sonnet-4-6",
				MaxTokens: 2048,
			},
		},
	}

	params := p.buildParams(&Request{
		Messages:       []Message{{Role: RoleUser, Content: "hello"}},
		MaxTokens:      2048,
		ThinkingBudget: 128,
	})

	body := marshalAnthropicParams(t, params)
	outputCfg, ok := body["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("output_config = %#v, want object", body["output_config"])
	}
	if got := outputCfg["effort"]; got != "max" {
		t.Fatalf("output_config.effort = %#v, want \"max\"", got)
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
			client := newTestHTTPClient(func(r *http.Request) (*http.Response, error) {
				attempts++
				if attempts <= anthropicTransientMaxRetries {
					return jsonResponse(http.StatusInternalServerError, `{"type":"error","error":{"type":"api_error","message":"internal server error"},"request_id":"req_test"}`, r), nil
				}
				return jsonResponse(http.StatusOK, `{
					"id":"msg_test",
					"type":"message",
					"role":"assistant",
					"model":"claude-sonnet-4-6",
					"content":[{"type":"text","text":"ok"}],
					"stop_reason":"end_turn",
					"usage":{"input_tokens":1,"output_tokens":1}
				}`, r), nil
			})

			cfg := AnthropicConfig{
				BaseConfig: BaseConfig{
					APIKey:         "test-key",
					Model:          "claude-sonnet-4-6",
					MaxTokens:      64,
					Timeout:        time.Second,
					RetryBaseDelay: time.Millisecond,
					RetryMaxDelay:  10 * time.Millisecond,
				},
				AuthMode:   tt.authMode,
				HTTPClient: client,
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
			if attempts != anthropicTransientMaxRetries+1 {
				t.Fatalf("expected %d attempts, got %d", anthropicTransientMaxRetries+1, attempts)
			}
			if len(ctx.events) != anthropicTransientMaxRetries {
				t.Fatalf("expected %d retry events, got %d", anthropicTransientMaxRetries, len(ctx.events))
			}
			for i, event := range ctx.events {
				if event.Attempt != i+1 {
					t.Fatalf("event %d attempt = %d, want %d", i, event.Attempt, i+1)
				}
				if event.MaxAttempts != anthropicTransientMaxRetries {
					t.Fatalf("event %d max attempts = %d, want %d", i, event.MaxAttempts, anthropicTransientMaxRetries)
				}
			}
		})
	}
}

func TestAnthropicProviderGenerate_RetriesOverloadedError(t *testing.T) {
	attempts := 0
	client := newTestHTTPClient(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts <= anthropicTransientMaxRetries {
			return jsonResponse(529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"},"request_id":"req_test"}`, r), nil
		}
		return jsonResponse(http.StatusOK, `{
			"id":"msg_test",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-6",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`, r), nil
	})

	cfg := AnthropicConfig{
		BaseConfig: BaseConfig{
			APIKey:         "test-key",
			Model:          "claude-sonnet-4-6",
			MaxTokens:      64,
			Timeout:        time.Second,
			RetryBaseDelay: time.Millisecond,
			RetryMaxDelay:  10 * time.Millisecond,
		},
		AuthMode:   AnthropicAuthModeAPIKey,
		HTTPClient: client,
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
	if attempts != anthropicTransientMaxRetries+1 {
		t.Fatalf("expected %d attempts, got %d", anthropicTransientMaxRetries+1, attempts)
	}
	if len(ctx.events) != anthropicTransientMaxRetries {
		t.Fatalf("expected %d retry events, got %d", anthropicTransientMaxRetries, len(ctx.events))
	}
}

func TestAnthropicProviderGenerate_DoesNotRetryNonTransientStatus(t *testing.T) {
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
			client := newTestHTTPClient(func(r *http.Request) (*http.Response, error) {
				attempts++
				return jsonResponse(tt.status, `{"type":"error","error":{"type":"api_error","message":"request failed"},"request_id":"req_test"}`, r), nil
			})

			cfg := AnthropicConfig{
				BaseConfig: BaseConfig{
					APIKey:         "test-key",
					Model:          "claude-sonnet-4-6",
					MaxTokens:      64,
					Timeout:        time.Second,
					RetryBaseDelay: time.Millisecond,
					RetryMaxDelay:  10 * time.Millisecond,
				},
				AuthMode:   AnthropicAuthModeAPIKey,
				HTTPClient: client,
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
	var gotQueryBeta string
	var gotUserAgent string

	client := newTestHTTPClient(func(r *http.Request) (*http.Response, error) {
		gotAuthorization = r.Header.Get("Authorization")
		gotBetaHeader = r.Header.Get("anthropic-beta")
		gotQueryBeta = r.URL.Query().Get("beta")
		gotUserAgent = r.Header.Get("User-Agent")
		return jsonResponse(http.StatusOK, `{
			"id":"msg_test",
			"type":"message",
			"role":"assistant",
			"model":"claude-opus-4-6",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`, r), nil
	})

	cfg := AnthropicConfig{
		BaseConfig: BaseConfig{
			APIKey:    "oauth-access-token",
			Model:     "claude-opus-4-6",
			MaxTokens: 64,
			Timeout:   time.Second,
		},
		AuthMode:   AnthropicAuthModeOAuth,
		HTTPClient: client,
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
	if gotQueryBeta != "true" {
		t.Fatalf("beta query = %q, want %q", gotQueryBeta, "true")
	}
	for _, required := range []string{
		string(anthropic.AnthropicBetaInterleavedThinking2025_05_14),
		"oauth-2025-04-20",
		"fine-grained-tool-streaming-2025-05-14",
	} {
		if !strings.Contains(gotBetaHeader, required) {
			t.Fatalf("anthropic-beta header = %q, want it to include %q on messages requests", gotBetaHeader, required)
		}
	}
	for _, unexpected := range []string{
		"claude-code-20250219",
	} {
		if strings.Contains(gotBetaHeader, unexpected) {
			t.Fatalf("anthropic-beta header = %q, should not include %q in oauth mode", gotBetaHeader, unexpected)
		}
	}
	if !strings.Contains(gotUserAgent, "claude-cli/2.1.2") {
		t.Fatalf("user-agent header = %q, want claude-cli oauth user-agent", gotUserAgent)
	}
}

func TestAnthropicProviderGenerate_APIKeyDoesNotUseOAuthRequestShims(t *testing.T) {
	var gotAuthorization string
	var gotAPIKey string
	var gotBetaHeader string
	var gotQueryBeta string
	var gotUserAgent string

	client := newTestHTTPClient(func(r *http.Request) (*http.Response, error) {
		gotAuthorization = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotBetaHeader = r.Header.Get("anthropic-beta")
		gotQueryBeta = r.URL.Query().Get("beta")
		gotUserAgent = r.Header.Get("User-Agent")
		return jsonResponse(http.StatusOK, `{
			"id":"msg_test",
			"type":"message",
			"role":"assistant",
			"model":"claude-opus-4-6",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`, r), nil
	})

	cfg := AnthropicConfig{
		BaseConfig: BaseConfig{
			APIKey:    "api-key-token",
			Model:     "claude-opus-4-6",
			MaxTokens: 64,
			Timeout:   time.Second,
		},
		AuthMode:   AnthropicAuthModeAPIKey,
		HTTPClient: client,
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
	if gotAuthorization != "" {
		t.Fatalf("authorization header = %q, want empty for api-key mode", gotAuthorization)
	}
	if gotAPIKey != "api-key-token" {
		t.Fatalf("x-api-key header = %q, want %q", gotAPIKey, "api-key-token")
	}
	if gotQueryBeta != "" {
		t.Fatalf("beta query = %q, want empty for api-key mode", gotQueryBeta)
	}
	if strings.Contains(gotBetaHeader, "oauth-2025-04-20") {
		t.Fatalf("anthropic-beta header = %q, should not include oauth beta in api-key mode", gotBetaHeader)
	}
	if strings.Contains(gotUserAgent, "claude-cli/2.1.2") {
		t.Fatalf("user-agent header = %q, should not use oauth user-agent in api-key mode", gotUserAgent)
	}
}

func TestAnthropicBuildParams_OAuthPrefixesToolNames(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				APIKey:    "oauth-access-token",
				Model:     "claude-sonnet-4-6",
				MaxTokens: 2048,
			},
			AuthMode: AnthropicAuthModeOAuth,
		},
	}

	params := p.buildParams(&Request{
		Messages: []Message{
			{Role: RoleUser, Content: "hello"},
			{
				Role:    RoleAssistant,
				Content: "replay",
				Metadata: map[string]any{
					anthropicRawContentKey: []anthropicContentBlock{
						{Type: "tool_use", ToolID: "raw_tool", ToolName: "bash", ToolInput: `{"cmd":"pwd"}`},
					},
				},
			},
			{Role: RoleTool, ToolCallID: "raw_tool", Content: "/tmp"},
			{
				Role:    RoleAssistant,
				Content: "working",
				ToolCalls: []ToolCall{
					{ID: "live_tool", Name: "bash", Arguments: `{"cmd":"ls"}`},
				},
			},
		},
		Tools: []Tool{
			{
				Name:        "bash",
				Description: "run shell commands",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		ToolChoice: "bash",
	})

	body := marshalAnthropicParams(t, params)
	tools := body["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["name"] != "mcp_bash" {
		t.Fatalf("tool name = %#v, want mcp_bash", tool["name"])
	}

	toolChoice := body["tool_choice"].(map[string]any)
	if toolChoice["name"] != "mcp_bash" {
		t.Fatalf("tool_choice.name = %#v, want mcp_bash", toolChoice["name"])
	}

	var toolUseNames []string
	for _, message := range body["messages"].([]any) {
		msg := message.(map[string]any)
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, block := range content {
			cb := block.(map[string]any)
			if cb["type"] == "tool_use" {
				toolUseNames = append(toolUseNames, cb["name"].(string))
			}
		}
	}

	if len(toolUseNames) != 2 {
		t.Fatalf("tool_use names = %#v, want 2 tool_use blocks", toolUseNames)
	}
	for _, name := range toolUseNames {
		if name != "mcp_bash" {
			t.Fatalf("tool_use name = %q, want mcp_bash", name)
		}
	}
}

func TestAnthropicBuildParams_APIKeyLeavesToolNamesUntouched(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				APIKey:    "api-key-token",
				Model:     "claude-sonnet-4-6",
				MaxTokens: 2048,
			},
			AuthMode: AnthropicAuthModeAPIKey,
		},
	}

	params := p.buildParams(&Request{
		Messages: []Message{
			{Role: RoleUser, Content: "hello"},
			{
				Role:    RoleAssistant,
				Content: "replay",
				Metadata: map[string]any{
					anthropicRawContentKey: []anthropicContentBlock{
						{Type: "tool_use", ToolID: "raw_tool", ToolName: "bash", ToolInput: `{"cmd":"pwd"}`},
					},
				},
			},
			{Role: RoleTool, ToolCallID: "raw_tool", Content: "/tmp"},
			{
				Role:    RoleAssistant,
				Content: "working",
				ToolCalls: []ToolCall{
					{ID: "live_tool", Name: "bash", Arguments: `{"cmd":"ls"}`},
				},
			},
		},
		Tools: []Tool{
			{
				Name:        "bash",
				Description: "run shell commands",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		ToolChoice: "bash",
	})

	body := marshalAnthropicParams(t, params)
	tools := body["tools"].([]any)
	tool := tools[0].(map[string]any)
	if tool["name"] != "bash" {
		t.Fatalf("tool name = %#v, want bash", tool["name"])
	}

	toolChoice := body["tool_choice"].(map[string]any)
	if toolChoice["name"] != "bash" {
		t.Fatalf("tool_choice.name = %#v, want bash", toolChoice["name"])
	}

	var toolUseNames []string
	for _, message := range body["messages"].([]any) {
		msg := message.(map[string]any)
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, block := range content {
			cb := block.(map[string]any)
			if cb["type"] == "tool_use" {
				toolUseNames = append(toolUseNames, cb["name"].(string))
			}
		}
	}

	if len(toolUseNames) != 2 {
		t.Fatalf("tool_use names = %#v, want 2 tool_use blocks", toolUseNames)
	}
	for _, name := range toolUseNames {
		if name != "bash" {
			t.Fatalf("tool_use name = %q, want bash", name)
		}
	}
}

func TestAnthropicBuildParams_OAuthInjectsClaudeCodeSystemPrompt(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				APIKey:    "oauth-access-token",
				Model:     "claude-sonnet-4-6",
				MaxTokens: 2048,
			},
			AuthMode: AnthropicAuthModeOAuth,
		},
	}

	params := p.buildParams(&Request{
		SystemPrompt: "You are OpenCode. Keep OpenCode responses concise.",
		Messages:     []Message{{Role: RoleUser, Content: "hello"}},
	})

	body := marshalAnthropicParams(t, params)
	systemBlocks, ok := body["system"].([]any)
	if !ok || len(systemBlocks) != 3 {
		t.Fatalf("system = %#v, want shim, boundary, and dynamic prompt blocks", body["system"])
	}
	if system := systemBlocks[0].(map[string]any)["text"].(string); system != anthropicOAuthSystemShim {
		t.Fatalf("system shim block = %q, want %q", system, anthropicOAuthSystemShim)
	}
	if boundary := systemBlocks[1].(map[string]any)["text"].(string); boundary != anthropicOAuthSystemBoundary {
		t.Fatalf("system boundary block = %q, want %q", boundary, anthropicOAuthSystemBoundary)
	}
	dynamic := systemBlocks[2].(map[string]any)["text"].(string)
	if strings.Contains(dynamic, "OpenCode") {
		t.Fatalf("dynamic system prompt = %q, should scrub OpenCode branding in oauth mode", dynamic)
	}
	if !strings.Contains(dynamic, "Claude Code") {
		t.Fatalf("dynamic system prompt = %q, want Claude Code branding in oauth mode", dynamic)
	}
}

func TestAnthropicBuildParams_APIKeyLeavesSystemPromptUntouched(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				APIKey:    "api-key-token",
				Model:     "claude-sonnet-4-6",
				MaxTokens: 2048,
			},
			AuthMode: AnthropicAuthModeAPIKey,
		},
	}

	params := p.buildParams(&Request{
		SystemPrompt: "You are OpenCode. Keep OpenCode responses concise.",
		Messages:     []Message{{Role: RoleUser, Content: "hello"}},
	})

	body := marshalAnthropicParams(t, params)
	systemBlocks, ok := body["system"].([]any)
	if !ok || len(systemBlocks) != 1 {
		t.Fatalf("system = %#v, want one text block", body["system"])
	}
	system := systemBlocks[0].(map[string]any)["text"].(string)
	if system != "You are OpenCode. Keep OpenCode responses concise." {
		t.Fatalf("system prompt = %q, want original prompt in api-key mode", system)
	}
}

func TestAnthropicBuildParams_OAuthShimOnlySystemPromptOmitsDynamicBoundary(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				APIKey:    "oauth-access-token",
				Model:     "claude-sonnet-4-6",
				MaxTokens: 2048,
			},
			AuthMode: AnthropicAuthModeOAuth,
		},
	}

	params := p.buildParams(&Request{
		SystemPrompt: anthropicOAuthSystemShim,
		Messages:     []Message{{Role: RoleUser, Content: "hello"}},
	})

	body := marshalAnthropicParams(t, params)
	systemBlocks, ok := body["system"].([]any)
	if !ok || len(systemBlocks) != 1 {
		t.Fatalf("system = %#v, want one shim block", body["system"])
	}
	if system := systemBlocks[0].(map[string]any)["text"].(string); system != anthropicOAuthSystemShim {
		t.Fatalf("system prompt = %q, want %q", system, anthropicOAuthSystemShim)
	}
}

func TestAnthropicProviderConvertResponse_OAuthCanonicalizesToolNamesAndRawContent(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				Model: "claude-sonnet-4-6",
			},
			AuthMode: AnthropicAuthModeOAuth,
		},
	}

	var msg anthropic.Message
	if err := json.Unmarshal([]byte(`{
		"id":"msg_test",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-6",
		"content":[
			{"type":"text","text":"ok"},
			{"type":"tool_use","id":"tool_1","name":"mcp_bash","input":{"cmd":"pwd"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`), &msg); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	resp := p.convertResponse(&msg)
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v, want 1 tool call", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Name != "bash" {
		t.Fatalf("tool call name = %q, want bash", resp.ToolCalls[0].Name)
	}

	raw, ok := resp.ProviderMetadata[anthropicRawContentKey].([]anthropicContentBlock)
	if !ok {
		t.Fatalf("provider metadata raw content = %#v, want []anthropicContentBlock", resp.ProviderMetadata[anthropicRawContentKey])
	}
	if len(raw) != 2 {
		t.Fatalf("raw content = %#v, want 2 blocks", raw)
	}
	if raw[1].ToolName != "bash" {
		t.Fatalf("raw tool name = %q, want bash", raw[1].ToolName)
	}
}

func TestAnthropicProviderConvertStreamEvent_OAuthCanonicalizesToolNames(t *testing.T) {
	p := &AnthropicProvider{
		config: AnthropicConfig{
			BaseConfig: BaseConfig{
				Model: "claude-sonnet-4-6",
			},
			AuthMode: AnthropicAuthModeOAuth,
		},
	}

	var event anthropic.MessageStreamEventUnion
	if err := json.Unmarshal([]byte(`{
		"type":"content_block_start",
		"index":0,
		"content_block":{"type":"tool_use","id":"tool_1","name":"mcp_bash","input":{"cmd":"pwd"}}
	}`), &event); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	chunk := p.convertStreamEvent(event, 1, map[int64]string{})
	if chunk == nil || chunk.ToolCall == nil {
		t.Fatalf("chunk = %#v, want tool start chunk", chunk)
	}
	if chunk.ToolCall.Name != "bash" {
		t.Fatalf("tool call name = %q, want bash", chunk.ToolCall.Name)
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
