package guide

import (
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

func TestPrefetchDomainFilter_Filter_NilContext(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)

	candidates := []PrefetchCandidate{
		{AgentType: concurrency.AgentLibrarian, QueryHash: "q1"},
		{AgentType: concurrency.AgentAcademic, QueryHash: "q2"},
	}

	result := filter.Filter(candidates, nil)

	if len(result) != len(candidates) {
		t.Errorf("Filter with nil context should return all candidates, got %d", len(result))
	}
}

func TestPrefetchDomainFilter_Filter_EmptyContext(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)
	ctx := domain.NewDomainContext("test query")

	candidates := []PrefetchCandidate{
		{AgentType: concurrency.AgentLibrarian, QueryHash: "q1"},
	}

	result := filter.Filter(candidates, ctx)

	if len(result) != len(candidates) {
		t.Errorf("Filter with empty context should return all candidates, got %d", len(result))
	}
}

func TestPrefetchDomainFilter_Filter_PrimaryDomainMatch(t *testing.T) {
	filter := NewPrefetchDomainFilter(&PrefetchFilterConfig{MinRelevance: 0.3})

	ctx := domain.NewDomainContext("find the function")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, []string{"find"})

	candidates := []PrefetchCandidate{
		{AgentType: concurrency.AgentLibrarian, QueryHash: "q1"},
		{AgentType: concurrency.AgentAcademic, QueryHash: "q2"},
	}

	result := filter.Filter(candidates, ctx)

	if len(result) != 1 {
		t.Errorf("Expected 1 relevant candidate, got %d", len(result))
	}
	if result[0].AgentType != concurrency.AgentLibrarian {
		t.Errorf("Expected librarian, got %s", result[0].AgentType)
	}
	if result[0].Relevance != 0.9 {
		t.Errorf("Expected relevance 0.9, got %f", result[0].Relevance)
	}
}

func TestPrefetchDomainFilter_Filter_CrossDomain(t *testing.T) {
	filter := NewPrefetchDomainFilter(&PrefetchFilterConfig{
		MinRelevance:      0.3,
		EnableCrossDomain: true,
	})

	ctx := domain.NewDomainContext("research and find code")
	ctx.AddDomain(domain.DomainLibrarian, 0.8, []string{"find"})
	ctx.AddDomain(domain.DomainAcademic, 0.7, []string{"research"})
	ctx.SetCrossDomain(true)

	candidates := []PrefetchCandidate{
		{AgentType: concurrency.AgentLibrarian, QueryHash: "q1"},
		{AgentType: concurrency.AgentAcademic, QueryHash: "q2"},
		{AgentType: concurrency.AgentArchitect, QueryHash: "q3"},
	}

	result := filter.Filter(candidates, ctx)

	if len(result) != 2 {
		t.Errorf("Expected 2 relevant candidates for cross-domain, got %d", len(result))
	}
}

func TestPrefetchDomainFilter_Filter_MaxCandidates(t *testing.T) {
	filter := NewPrefetchDomainFilter(&PrefetchFilterConfig{
		MinRelevance:  0.1,
		MaxCandidates: 2,
	})

	ctx := domain.NewDomainContext("broad query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)
	ctx.AddDomain(domain.DomainArchitect, 0.7, nil)
	ctx.SetCrossDomain(true)

	candidates := []PrefetchCandidate{
		{AgentType: concurrency.AgentLibrarian, QueryHash: "q1"},
		{AgentType: concurrency.AgentAcademic, QueryHash: "q2"},
		{AgentType: concurrency.AgentArchitect, QueryHash: "q3"},
	}

	result := filter.Filter(candidates, ctx)

	if len(result) != 2 {
		t.Errorf("Expected max 2 candidates, got %d", len(result))
	}

	if result[0].AgentType != concurrency.AgentLibrarian {
		t.Errorf("First candidate should be highest relevance (librarian)")
	}
}

func TestPrefetchDomainFilter_FilterByDomains(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)

	candidates := []PrefetchCandidate{
		{AgentType: concurrency.AgentLibrarian, QueryHash: "q1"},
		{AgentType: concurrency.AgentAcademic, QueryHash: "q2"},
		{AgentType: concurrency.AgentArchitect, QueryHash: "q3"},
	}

	allowed := []domain.Domain{domain.DomainLibrarian, domain.DomainArchitect}
	result := filter.FilterByDomains(candidates, allowed)

	if len(result) != 2 {
		t.Errorf("Expected 2 candidates, got %d", len(result))
	}
}

func TestPrefetchDomainFilter_IsRelevantAgent_NilContext(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)

	if !filter.IsRelevantAgent(concurrency.AgentLibrarian, nil) {
		t.Error("All agents should be relevant with nil context")
	}
}

func TestPrefetchDomainFilter_IsRelevantAgent_WithContext(t *testing.T) {
	filter := NewPrefetchDomainFilter(&PrefetchFilterConfig{MinRelevance: 0.5})

	ctx := domain.NewDomainContext("find function")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)

	if !filter.IsRelevantAgent(concurrency.AgentLibrarian, ctx) {
		t.Error("Librarian should be relevant")
	}

	if filter.IsRelevantAgent(concurrency.AgentAcademic, ctx) {
		t.Error("Academic should not be relevant")
	}
}

func TestPrefetchDomainFilter_GetRelevantAgents(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)

	ctx := domain.NewDomainContext("test")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)

	agents := filter.GetRelevantAgents(ctx)

	if len(agents) != 1 {
		t.Errorf("Expected 1 agent (primary only, not cross-domain), got %d", len(agents))
	}
}

func TestPrefetchDomainFilter_GetRelevantAgents_CrossDomain(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)

	ctx := domain.NewDomainContext("test")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)
	ctx.SetCrossDomain(true)

	agents := filter.GetRelevantAgents(ctx)

	if len(agents) != 2 {
		t.Errorf("Expected 2 agents for cross-domain, got %d", len(agents))
	}
}

func TestPrefetchDomainFilter_GetRelevantAgents_NilContext(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)

	agents := filter.GetRelevantAgents(nil)

	if agents != nil {
		t.Error("Expected nil for nil context")
	}
}

func TestPrefetchDomainFilter_Defaults(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)

	if filter.MinRelevance() != 0.3 {
		t.Errorf("Default min relevance should be 0.3, got %f", filter.MinRelevance())
	}

	if filter.MaxCandidates() != 5 {
		t.Errorf("Default max candidates should be 5, got %d", filter.MaxCandidates())
	}

	if !filter.IsCrossDomainEnabled() {
		t.Error("Cross-domain should be enabled by default")
	}
}

func TestPrefetchDomainFilter_UnknownAgentType(t *testing.T) {
	filter := NewPrefetchDomainFilter(nil)

	ctx := domain.NewDomainContext("test")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)

	candidates := []PrefetchCandidate{
		{AgentType: "unknown-agent", QueryHash: "q1"},
	}

	result := filter.Filter(candidates, ctx)

	if len(result) != 0 {
		t.Errorf("Unknown agent should be filtered out, got %d", len(result))
	}
}

func TestPrefetchDomainFilter_SecondaryDomainDecay(t *testing.T) {
	filter := NewPrefetchDomainFilter(&PrefetchFilterConfig{
		MinRelevance:      0.1,
		EnableCrossDomain: true,
	})

	ctx := domain.NewDomainContext("complex query")
	ctx.AddDomain(domain.DomainLibrarian, 0.9, nil)
	ctx.AddDomain(domain.DomainAcademic, 0.8, nil)
	ctx.AddDomain(domain.DomainArchitect, 0.7, nil)
	ctx.SetCrossDomain(true)

	candidates := []PrefetchCandidate{
		{AgentType: concurrency.AgentAcademic, QueryHash: "q1"},
	}

	result := filter.Filter(candidates, ctx)

	if len(result) != 1 {
		t.Fatal("Expected 1 candidate")
	}

	expectedRelevance := 0.8 * 1.0
	if result[0].Relevance != expectedRelevance {
		t.Errorf("First secondary should have relevance %f, got %f", expectedRelevance, result[0].Relevance)
	}
}

func TestPrefetchCandidate_Fields(t *testing.T) {
	candidate := PrefetchCandidate{
		AgentType:  concurrency.AgentLibrarian,
		QueryHash:  "abc123",
		Priority:   1,
		Relevance:  0.85,
		IsRelevant: true,
	}

	if candidate.AgentType != concurrency.AgentLibrarian {
		t.Error("AgentType mismatch")
	}
	if candidate.QueryHash != "abc123" {
		t.Error("QueryHash mismatch")
	}
	if candidate.Priority != 1 {
		t.Error("Priority mismatch")
	}
	if candidate.Relevance != 0.85 {
		t.Error("Relevance mismatch")
	}
	if !candidate.IsRelevant {
		t.Error("IsRelevant mismatch")
	}
}
