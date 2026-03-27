package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/agents/archivalist"
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

func TestOnDemandConfiguredModel_UsesPersistedLibrarianModel(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, ".sylk")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configPath := filepath.Join(cfgDir, "config.yaml")
	config := "agents:\n  librarian:\n    provider: anthropic\n    model: claude-sonnet-4-6\n"
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	deps := onDemandAgentCreatorDeps{projectRoot: root}
	if got := deps.configuredModel("librarian", "gemini-3.1-pro-preview"); got != "claude-sonnet-4-6" {
		t.Fatalf("configuredModel(librarian) = %q, want claude-sonnet-4-6", got)
	}
}

func TestBuildOnDemandArchivalistConfig_EnablesConsultationRuntime(t *testing.T) {
	cfg := buildOnDemandArchivalistConfig(onDemandAgentCreatorDeps{}, "archivalist", archivalist.ModelSonnet45)
	if !cfg.EnableLLM {
		t.Fatal("expected archivalist LLM mode enabled")
	}
	if !cfg.EnableArchive || !cfg.EnableRAG || !cfg.EnableKnowledgeGraph {
		t.Fatalf("expected archive-backed retrieval enabled, got archive=%v rag=%v graph=%v", cfg.EnableArchive, cfg.EnableRAG, cfg.EnableKnowledgeGraph)
	}
	if !cfg.EnableHybridQuery || !cfg.EnableACTR {
		t.Fatalf("expected hybrid retrieval and ACT-R enabled, got hybrid=%v actr=%v", cfg.EnableHybridQuery, cfg.EnableACTR)
	}
}
