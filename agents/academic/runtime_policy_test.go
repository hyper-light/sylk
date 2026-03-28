package academic

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
	"github.com/adalundhe/sylk/core/versioning"
)

func TestApplyLLMRuntimeProfileUsesContextSessionMetadata(t *testing.T) {
	a, err := New(Config{ID: "academic", SessionID: "sess-config"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Model:     "gpt-5.4-pro",
		MaxTokens: 2048,
	}
	ctx := versioning.WithSessionID(context.Background(), "sess-live")

	a.applyLLMRuntimeProfile(ctx, req, "conversation")

	if got := req.Metadata["agent_id"]; got != "academic" {
		t.Fatalf("Metadata[agent_id] = %#v, want academic", got)
	}
	if got := req.Metadata["session_id"]; got != "sess-live" {
		t.Fatalf("Metadata[session_id] = %#v, want sess-live", got)
	}
}

func TestApplyLLMRuntimeProfileQuickResearchDepthLowersResearchDeliberation(t *testing.T) {
	a, err := New(Config{ID: "academic", SessionID: "sess-config"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Model:     "gpt-5.4-pro",
		MaxTokens: 4096,
	}
	ctx := WithAcademicResearchDepth(context.Background(), shared.ResearchDepthQuick)

	a.applyLLMRuntimeProfile(ctx, req, "research")

	if got := req.ReasoningEffort; got != "medium" {
		t.Fatalf("ReasoningEffort = %q, want medium", got)
	}
	if got := req.Metadata["llm_deliberation"]; got != "medium" {
		t.Fatalf("Metadata[llm_deliberation] = %#v, want medium", got)
	}
}

func TestApplyLLMRuntimeProfileComprehensiveResearchDepthRaisesResearchDeliberation(t *testing.T) {
	a, err := New(Config{ID: "academic", SessionID: "sess-config"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	req := &providers.Request{
		Model:     "gpt-5.4-pro",
		MaxTokens: 4096,
	}
	ctx := WithAcademicResearchDepth(context.Background(), shared.ResearchDepthComprehensive)

	a.applyLLMRuntimeProfile(ctx, req, "research")

	if got := req.ReasoningEffort; got != "xhigh" {
		t.Fatalf("ReasoningEffort = %q, want xhigh", got)
	}
	if got := req.Metadata["llm_deliberation"]; got != "max" {
		t.Fatalf("Metadata[llm_deliberation] = %#v, want max", got)
	}
}

func TestAcademicCacheKeyIncludesResearchDepth(t *testing.T) {
	a, err := New(Config{ID: "academic", SessionID: "sess-config"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	quick := a.cacheKey(&ResearchQuery{Query: "oauth guidance", Depth: "quick"})
	comprehensive := a.cacheKey(&ResearchQuery{Query: "oauth guidance", Depth: "comprehensive"})
	if quick == comprehensive {
		t.Fatalf("cacheKey quick=%q comprehensive=%q, want different keys", quick, comprehensive)
	}
}
