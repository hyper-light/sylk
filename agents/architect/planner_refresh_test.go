package architect

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestRefreshPlannerAuthClearsPlannerAndUpdatesMode(t *testing.T) {
	a := &Architect{
		config: Config{
			AnthropicAuthMode: providers.AnthropicAuthModeAPIKey,
			AnthropicAPIKey:   "sk-ant-test",
		},
		planner: &anthropicPlanner{},
	}

	a.RefreshPlannerAuth("oauth")

	if a.config.AnthropicAPIKey != "" {
		t.Fatalf("AnthropicAPIKey = %q, want empty", a.config.AnthropicAPIKey)
	}
	if a.config.AnthropicAuthMode != providers.AnthropicAuthModeOAuth {
		t.Fatalf("AnthropicAuthMode = %q, want %q", a.config.AnthropicAuthMode, providers.AnthropicAuthModeOAuth)
	}
	if a.planner != nil {
		t.Fatal("planner should be cleared")
	}
}

func TestConversationFallbackMentionsOAuthWhenPlannerUnavailable(t *testing.T) {
	a := &Architect{config: Config{EnableLLM: false}}

	result, err := a.conversationFallback(context.Background(), &ArchitectRequest{Intent: IntentPlan}, nil)
	if err != nil {
		t.Fatalf("conversationFallback() error = %v", err)
	}
	resp, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("conversationFallback() returned %T, want *ConversationResult", result)
	}
	if !strings.Contains(resp.Response, "OAuth") {
		t.Fatalf("response = %q, want OAuth guidance", resp.Response)
	}
}
