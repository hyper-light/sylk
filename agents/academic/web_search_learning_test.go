package academic

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/providers"
)

func TestAdaptiveNativeWebSearchLimit_DepthConditioned(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	state := newAcademicResearchExecutionState("sess-web-depth")

	minimalCtx := WithAcademicResearchDepth(context.Background(), shared.ResearchDepthMinimal)
	comprehensiveCtx := WithAcademicResearchDepth(context.Background(), shared.ResearchDepthComprehensive)

	minimal := a.adaptiveNativeWebSearchLimit(minimalCtx, state, 6)
	comprehensive := a.adaptiveNativeWebSearchLimit(comprehensiveCtx, state, 6)
	if minimal != 1 {
		t.Fatalf("minimal limit = %d, want 1", minimal)
	}
	if comprehensive <= minimal {
		t.Fatalf("comprehensive limit = %d, want > minimal limit %d", comprehensive, minimal)
	}
}

func TestAdaptiveNativeWebSearchLimit_CollapsesAfterUnproductiveSearches(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	state := newAcademicResearchExecutionState("sess-web-thrash")
	ctx := WithAcademicResearchDepth(context.Background(), shared.ResearchDepthMinimal)

	state.observeNativeSearchCall(ctx, a, providers.NativeWebSearchCall{
		ID:    "search-1",
		Query: "python cli package backend",
	})
	state.observeNativeSearchCall(ctx, a, providers.NativeWebSearchCall{
		ID:    "search-2",
		Query: "python console scripts backend",
	})

	if got := a.adaptiveNativeWebSearchLimit(ctx, state, 6); got != 0 {
		t.Fatalf("limit after low-yield search thrash = %d, want 0", got)
	}
}

func TestApplyAdaptiveWebSearchBudget_RemovesWebSearchWhenMarginalValueFallsBelowCost(t *testing.T) {
	a, err := New(Config{ID: "academic-custom"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	state := newAcademicResearchExecutionState("sess-web-budget")
	ctx := WithAcademicResearchDepth(context.Background(), shared.ResearchDepthMinimal)
	state.observeNativeSearchCall(ctx, a, providers.NativeWebSearchCall{
		ID:    "search-1",
		Query: "python cli package backend",
	})
	state.observeNativeSearchCall(ctx, a, providers.NativeWebSearchCall{
		ID:    "search-2",
		Query: "python console scripts backend",
	})

	tools := a.buildToolDefinitions()
	adjusted := a.applyAdaptiveWebSearchBudget(ctx, tools, state)
	for _, tool := range adjusted {
		if tool.Name == "web_search" {
			t.Fatalf("expected web_search to be removed after marginal value collapse, got %#v", tool.WebSearch)
		}
	}
}
