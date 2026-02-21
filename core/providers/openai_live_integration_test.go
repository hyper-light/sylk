package providers

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenAILive_Codex53Generate(t *testing.T) {
	if os.Getenv("OPENAI_LIVE_TESTS") != "1" {
		t.Skip("set OPENAI_LIVE_TESTS=1 to run live OpenAI compatibility tests")
	}

	cfg := DefaultOpenAIConfig()
	cfg.AuthMode = "api_key"
	if v := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); v != "" {
		cfg.Model = v
	}
	provider, err := NewOpenAIProvider(cfg)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := provider.Generate(ctx, &Request{
		Messages: []Message{{Role: RoleUser, Content: "Reply with exactly: sylk-live-ok"}},
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if !strings.Contains(strings.ToLower(resp.Content), "sylk-live-ok") {
		t.Fatalf("unexpected live response content: %q", resp.Content)
	}
}

func TestOpenAILive_ChatGPTCodex53Generate(t *testing.T) {
	if os.Getenv("OPENAI_LIVE_TESTS") != "1" || os.Getenv("OPENAI_LIVE_CHATGPT") != "1" {
		t.Skip("set OPENAI_LIVE_TESTS=1 and OPENAI_LIVE_CHATGPT=1 to run chatgpt auth live tests")
	}

	cfg := DefaultOpenAIConfig()
	cfg.AuthMode = "chatgpt"
	cfg.APIKey = strings.TrimSpace(os.Getenv("OPENAI_ACCESS_TOKEN"))
	cfg.ChatGPTAccountID = strings.TrimSpace(os.Getenv("OPENAI_CHATGPT_ACCOUNT_ID"))
	if cfg.APIKey == "" || cfg.ChatGPTAccountID == "" {
		t.Skip("OPENAI_ACCESS_TOKEN and OPENAI_CHATGPT_ACCOUNT_ID are required for chatgpt live test")
	}
	if v := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); v != "" {
		cfg.Model = v
	}
	provider, err := NewOpenAIProvider(cfg)
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := provider.Generate(ctx, &Request{
		Messages: []Message{{Role: RoleUser, Content: "Reply with exactly: sylk-chatgpt-live-ok"}},
	})
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}
	if !strings.Contains(strings.ToLower(resp.Content), "sylk-chatgpt-live-ok") {
		t.Fatalf("unexpected live response content: %q", resp.Content)
	}
}
