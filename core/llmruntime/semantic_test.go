package llmruntime

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestApplyStageOpenAIGuardianAssess(t *testing.T) {
	req := &providers.Request{
		SystemPrompt: "guardian",
		MaxTokens:    4096,
	}

	ApplyStage(req, ResolveAgentStageProfile("guardian", "conversation_assess"), ApplyOptions{
		Model:     "gpt-5.4-pro",
		MaxTokens: req.MaxTokens,
		AgentID:   "guardian",
		SessionID: "sess-1",
	})

	if req.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", req.ReasoningEffort)
	}
	if req.Verbosity != "medium" {
		t.Fatalf("Verbosity = %q, want medium", req.Verbosity)
	}
	if req.ReasoningSummary != "none" {
		t.Fatalf("ReasoningSummary = %q, want none", req.ReasoningSummary)
	}
	if req.ParallelToolCalls == nil || !*req.ParallelToolCalls {
		t.Fatalf("ParallelToolCalls = %#v, want true", req.ParallelToolCalls)
	}
	if req.PromptCacheKey != "guardian:sess-1" {
		t.Fatalf("PromptCacheKey = %q, want guardian:sess-1", req.PromptCacheKey)
	}
	if req.PromptCacheRetention != "in_memory" {
		t.Fatalf("PromptCacheRetention = %q, want in_memory", req.PromptCacheRetention)
	}
	if EmitsThoughts(req) {
		t.Fatal("EmitsThoughts(req) = true, want false")
	}
	if got := StageFromRequest(req); got != "conversation_assess" {
		t.Fatalf("StageFromRequest(req) = %q, want conversation_assess", got)
	}
}

func TestCompileStageAnthropicPlanningProtocol(t *testing.T) {
	profile := CompileStage(ResolveAgentStageProfile("architect", "planning_protocol"), ApplyOptions{
		Model:     "claude-opus-4-5",
		MaxTokens: 8000,
		AgentID:   "architect",
		SessionID: "sess-1",
	})

	if profile.ThinkingBudget == nil || *profile.ThinkingBudget <= 0 {
		t.Fatalf("ThinkingBudget = %#v, want > 0", profile.ThinkingBudget)
	}
	if profile.DisableParallelTools == nil || !*profile.DisableParallelTools {
		t.Fatalf("DisableParallelTools = %#v, want true", profile.DisableParallelTools)
	}
	if profile.UsePromptCache == nil || !*profile.UsePromptCache {
		t.Fatalf("UsePromptCache = %#v, want true", profile.UsePromptCache)
	}
}

func TestApplyStageGoogleDesignerAddsPromptDirectives(t *testing.T) {
	req := &providers.Request{
		SystemPrompt: "designer",
		MaxTokens:    4096,
	}

	ApplyStage(req, ResolveAgentStageProfile("designer", "design"), ApplyOptions{
		Model:     "gemini-3.1-pro-preview",
		MaxTokens: req.MaxTokens,
		AgentID:   "designer",
		SessionID: "sess-1",
	})

	if req.IncludeThoughts == nil || *req.IncludeThoughts {
		t.Fatalf("IncludeThoughts = %#v, want false", req.IncludeThoughts)
	}
	if !strings.Contains(req.SystemPrompt, "Response style: provide a fuller explanation") {
		t.Fatalf("SystemPrompt missing detail directive: %q", req.SystemPrompt)
	}
	if !strings.Contains(req.SystemPrompt, "Tool execution policy: call at most one tool at a time") {
		t.Fatalf("SystemPrompt missing tool policy directive: %q", req.SystemPrompt)
	}
}
