package cmd

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/credentials"
	"github.com/adalundhe/sylk/core/providers"
)

func TestDefaultGuideGoogleConfig(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "AIzaValidLookingGoogleKeyValue1234567890")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_OAUTH_ACCESS_TOKEN", "")
	t.Setenv("GOOGLE_OAUTH_REFRESH_TOKEN", "")

	cfg := defaultGuideGoogleConfig(nil)
	if cfg.Model != "gemini-3.1-pro-preview" {
		t.Fatalf("model = %q, want %q", cfg.Model, "gemini-3.1-pro-preview")
	}
	provider, err := providers.NewGoogleProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewGoogleProvider: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider")
	}
}

func TestEffectiveModelForCurrentAuth_OpenAIChatGPTUsesGPT54(t *testing.T) {
	reg := credentials.NewAuthRegistry(func(providerType string) map[string]bool {
		if providerType == "openai" {
			return map[string]bool{"chatgpt": true}
		}
		return nil
	}, nil, nil, nil)
	reg.PrimeAll()

	if got := effectiveModelForCurrentAuth(reg, "gpt-5.4-pro"); got != "gpt-5.4" {
		t.Fatalf("effectiveModelForCurrentAuth(chatgpt, gpt-5.4-pro) = %q, want gpt-5.4", got)
	}
	if got := effectiveModelForCurrentAuth(reg, "claude-opus-4-6"); got != "claude-opus-4-6" {
		t.Fatalf("effectiveModelForCurrentAuth(chatgpt, claude-opus-4-6) = %q, want claude-opus-4-6", got)
	}
}

func TestEffectiveModelForCurrentAuth_OpenAIAPIKeyKeepsGPT54Pro(t *testing.T) {
	reg := credentials.NewAuthRegistry(func(providerType string) map[string]bool {
		if providerType == "openai" {
			return map[string]bool{"api_key": true}
		}
		return nil
	}, nil, nil, nil)
	reg.PrimeAll()

	if got := effectiveModelForCurrentAuth(reg, "gpt-5.4-pro"); got != "gpt-5.4-pro" {
		t.Fatalf("effectiveModelForCurrentAuth(api_key, gpt-5.4-pro) = %q, want gpt-5.4-pro", got)
	}
}

func TestResolvePersistedModelForCurrentAuth_OnlyOpenAIChangesForChatGPT(t *testing.T) {
	reg := credentials.NewAuthRegistry(func(providerType string) map[string]bool {
		if providerType == "openai" {
			return map[string]bool{"chatgpt": true}
		}
		return nil
	}, nil, nil, nil)
	reg.PrimeAll()

	if got := resolvePersistedModelForCurrentAuth("gpt-5.4-pro", "openai", reg); got != "gpt-5.4" {
		t.Fatalf("resolvePersistedModelForCurrentAuth(openai) = %q, want gpt-5.4", got)
	}
	if got := resolvePersistedModelForCurrentAuth("claude-opus-4-6", "anthropic", reg); got != "claude-opus-4-6" {
		t.Fatalf("resolvePersistedModelForCurrentAuth(anthropic) = %q, want claude-opus-4-6", got)
	}
}
