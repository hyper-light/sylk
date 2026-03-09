package providers

import (
	"encoding/json"
	"testing"
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
