package providers

import (
	"strings"
	"testing"

	promptskills "github.com/adalundhe/sylk/skills"
)

func TestCombineGoogleSystemPrompts(t *testing.T) {
	got := combineGoogleSystemPrompts("config prompt", "request prompt")
	want := "config prompt\n\nrequest prompt"
	if got != want {
		t.Fatalf("combineGoogleSystemPrompts() = %q, want %q", got, want)
	}

	onlyRequest := combineGoogleSystemPrompts("   ", "request")
	if onlyRequest != "request" {
		t.Fatalf("combineGoogleSystemPrompts() request-only = %q, want request", onlyRequest)
	}
}

func TestGoogleBuildGenerateConfig_IncludesRequestPromptAndSkills(t *testing.T) {
	provider := &GoogleProvider{
		config: GoogleConfig{
			BaseConfig: BaseConfig{
				MaxTokens: 1024,
			},
			SystemPrompt: "base guide prompt",
		},
		skills: []promptskills.Skill{
			{
				Name:        "route",
				Description: "Route a request",
				Path:        "agents/guide/skills.go",
			},
		},
	}

	req := &Request{
		SystemPrompt: "classification prompt",
	}
	cfg := provider.buildGenerateConfig(req)

	if cfg.SystemInstruction == nil {
		t.Fatal("expected SystemInstruction to be set")
	}
	if len(cfg.SystemInstruction.Parts) != 2 {
		t.Fatalf("expected 2 system parts (prompt + skills), got %d", len(cfg.SystemInstruction.Parts))
	}
	if !strings.Contains(cfg.SystemInstruction.Parts[0].Text, "base guide prompt") {
		t.Fatalf("first system part missing config prompt: %q", cfg.SystemInstruction.Parts[0].Text)
	}
	if !strings.Contains(cfg.SystemInstruction.Parts[0].Text, "classification prompt") {
		t.Fatalf("first system part missing request prompt: %q", cfg.SystemInstruction.Parts[0].Text)
	}
	if !strings.Contains(cfg.SystemInstruction.Parts[1].Text, "<available_skills>") {
		t.Fatalf("second system part missing skills prompt envelope: %q", cfg.SystemInstruction.Parts[1].Text)
	}
	if !strings.Contains(cfg.SystemInstruction.Parts[1].Text, "route") {
		t.Fatalf("second system part missing skill name: %q", cfg.SystemInstruction.Parts[1].Text)
	}
}

func TestGoogleBuildGenerateConfig_IncludesRequestTools(t *testing.T) {
	provider := &GoogleProvider{
		config: GoogleConfig{
			BaseConfig: BaseConfig{
				MaxTokens: 1024,
			},
		},
	}

	req := &Request{
		Tools: []Tool{
			{
				Name:        "classify_query",
				Description: "Classify an incoming request",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"intent": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	cfg := provider.buildGenerateConfig(req)
	if len(cfg.Tools) != 1 {
		t.Fatalf("expected 1 tool in config, got %d", len(cfg.Tools))
	}
}
