package guide

import (
	"math/rand/v2"
	"testing"
)

func TestQualityAwareSelector_EmptyEndpoints(t *testing.T) {
	sel := NewQualityAwareSelector(rand.NewPCG(42, 0))
	ep := sel.Select(nil)
	if ep.AgentID != "" {
		t.Fatalf("expected zero value, got %q", ep.AgentID)
	}
}

func TestQualityAwareSelector_SingleEndpoint(t *testing.T) {
	sel := NewQualityAwareSelector(rand.NewPCG(42, 0))
	endpoints := []QualityEndpoint{
		{AgentID: "a1", Weight: 1.0},
	}
	ep := sel.Select(endpoints)
	if ep.AgentID != "a1" {
		t.Fatalf("expected a1, got %s", ep.AgentID)
	}
}

func TestQualityAwareSelector_WeightedDistribution(t *testing.T) {
	// Fixed seed for deterministic results.
	sel := NewQualityAwareSelector(rand.NewPCG(12345, 0))

	endpoints := []QualityEndpoint{
		{AgentID: "heavy", Weight: 0.9},
		{AgentID: "light", Weight: 0.1},
	}

	counts := map[string]int{"heavy": 0, "light": 0}
	const iterations = 10000

	for range iterations {
		ep := sel.Select(endpoints)
		counts[ep.AgentID]++
	}

	// With 90/10 split, heavy should get roughly 9000 of 10000.
	heavyRatio := float64(counts["heavy"]) / float64(iterations)
	if heavyRatio < 0.85 || heavyRatio > 0.95 {
		t.Fatalf("expected heavy ratio ~0.9, got %.3f (heavy=%d, light=%d)",
			heavyRatio, counts["heavy"], counts["light"])
	}
}

func TestQualityAwareSelector_AllZeroWeights(t *testing.T) {
	sel := NewQualityAwareSelector(rand.NewPCG(42, 0))
	endpoints := []QualityEndpoint{
		{AgentID: "a1", Weight: 0},
		{AgentID: "a2", Weight: 0},
	}

	// Should return first endpoint when all weights are zero.
	ep := sel.Select(endpoints)
	if ep.AgentID != "a1" {
		t.Fatalf("expected a1 for zero weights, got %s", ep.AgentID)
	}
}

func TestQualityAwareSelector_EqualWeights(t *testing.T) {
	sel := NewQualityAwareSelector(rand.NewPCG(42, 0))
	endpoints := []QualityEndpoint{
		{AgentID: "a1", Weight: 0.5},
		{AgentID: "a2", Weight: 0.5},
	}

	counts := map[string]int{"a1": 0, "a2": 0}
	const iterations = 10000

	for range iterations {
		ep := sel.Select(endpoints)
		counts[ep.AgentID]++
	}

	// Equal weights — each should get roughly 50%.
	a1Ratio := float64(counts["a1"]) / float64(iterations)
	if a1Ratio < 0.45 || a1Ratio > 0.55 {
		t.Fatalf("expected a1 ratio ~0.5, got %.3f", a1Ratio)
	}
}

func TestQualityAwareSelector_NilSource(t *testing.T) {
	// nil source should not panic.
	sel := NewQualityAwareSelector(nil)
	ep := sel.Select([]QualityEndpoint{{AgentID: "a1", Weight: 1.0}})
	if ep.AgentID != "a1" {
		t.Fatalf("expected a1, got %s", ep.AgentID)
	}
}
