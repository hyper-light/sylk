package llmruntime

import (
	"testing"

	"github.com/adalundhe/sylk/core/providers"
)

func TestApplyCopiesRuntimeFields(t *testing.T) {
	req := &providers.Request{}
	Apply(req, Profile{
		ReasoningEffort:      "high",
		ReasoningSummary:     "none",
		Verbosity:            "medium",
		PromptCacheKey:       "guardian:sess-1",
		PromptCacheRetention: "24h",
		ParallelToolCalls:    Bool(true),
		ThinkingBudget:       Int(123),
		DisableParallelTools: Bool(false),
	})

	if req.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want %q", req.ReasoningEffort, "high")
	}
	if req.ReasoningSummary != "none" {
		t.Fatalf("ReasoningSummary = %q, want %q", req.ReasoningSummary, "none")
	}
	if req.Verbosity != "medium" {
		t.Fatalf("Verbosity = %q, want %q", req.Verbosity, "medium")
	}
	if req.PromptCacheKey != "guardian:sess-1" {
		t.Fatalf("PromptCacheKey = %q, want %q", req.PromptCacheKey, "guardian:sess-1")
	}
	if req.PromptCacheRetention != "24h" {
		t.Fatalf("PromptCacheRetention = %q, want %q", req.PromptCacheRetention, "24h")
	}
	if req.ParallelToolCalls == nil || !*req.ParallelToolCalls {
		t.Fatalf("ParallelToolCalls = %#v, want true", req.ParallelToolCalls)
	}
	if req.ThinkingBudget != 123 {
		t.Fatalf("ThinkingBudget = %d, want %d", req.ThinkingBudget, 123)
	}
	if req.DisableParallelToolUse {
		t.Fatal("DisableParallelToolUse = true, want false")
	}
}

func TestSessionPromptCacheKey(t *testing.T) {
	if got := SessionPromptCacheKey("guardian", "sess-1"); got != "guardian:sess-1" {
		t.Fatalf("SessionPromptCacheKey() = %q, want %q", got, "guardian:sess-1")
	}
	if got := SessionPromptCacheKey("guardian", ""); got != "" {
		t.Fatalf("SessionPromptCacheKey() = %q, want empty string", got)
	}
}
