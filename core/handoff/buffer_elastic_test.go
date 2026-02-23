package handoff

import (
	"testing"
)

func TestElasticSizer_FractionDerivation200K(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	s := NewElasticSizer(desc)

	// nominalFraction = 1.0 / (200_000 / 20_000) = 0.10
	if got := s.nominalFraction; got != 0.1 {
		t.Errorf("nominalFraction: got %g, want 0.1", got)
	}
	// expandedFraction = 0.10 * 1.5 ≈ 0.15 (IEEE 754)
	if got := s.expandedFraction; got < 0.149 || got > 0.151 {
		t.Errorf("expandedFraction: got %g, want ~0.15", got)
	}
	// hysteresisDepth = max(3, 200_000 / 50_000) = 4
	if got := s.hysteresisDepth; got != 4 {
		t.Errorf("hysteresisDepth: got %d, want 4", got)
	}
	// TokenBudget = 0.10 * 200_000 = 20_000
	if got := s.TokenBudget(); got != 20_000 {
		t.Errorf("TokenBudget: got %d, want 20000", got)
	}
}

func TestElasticSizer_FractionDerivation1M(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 1_000_000, AgentType: "test", ModelID: "test"}
	s := NewElasticSizer(desc)

	// nominalFraction = 1.0 / (1_000_000 / 20_000) = 0.02
	if got := s.nominalFraction; got != 0.02 {
		t.Errorf("nominalFraction: got %f, want 0.02", got)
	}
	// hysteresisDepth = max(3, 1_000_000 / 50_000) = 20
	if got := s.hysteresisDepth; got != 20 {
		t.Errorf("hysteresisDepth: got %d, want 20", got)
	}
}

func TestElasticSizer_BaselineLearning(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	s := NewElasticSizer(desc)

	// hysteresisDepth=4, so baselineTarget=8
	if s.baselineLearned {
		t.Fatal("baseline should not be learned yet")
	}

	// Feed 7 observations — still learning.
	for range 7 {
		budget, changed := s.Evaluate(&GPPrediction{StdDev: 0.1})
		if changed {
			t.Fatal("state should not change during baseline learning")
		}
		if budget != 20_000 {
			t.Errorf("budget should stay nominal during learning: got %d", budget)
		}
	}
	if s.baselineLearned {
		t.Fatal("baseline should not be learned with only 7 observations")
	}

	// 8th observation completes baseline.
	_, _ = s.Evaluate(&GPPrediction{StdDev: 0.1})
	if !s.baselineLearned {
		t.Fatal("baseline should be learned after 8 observations")
	}
}

func TestElasticSizer_NominalToExpandedCycle(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	s := NewElasticSizer(desc)

	// Learn baseline with StdDev=0.1
	for range 8 {
		s.Evaluate(&GPPrediction{StdDev: 0.1})
	}
	if s.State() != ElasticNominal {
		t.Fatalf("expected Nominal, got %s", s.State())
	}

	// expandThreshold = 0.1 * 2.0 = 0.2
	// Need hysteresisDepth=4 consecutive ticks above threshold.
	for range 3 {
		_, changed := s.Evaluate(&GPPrediction{StdDev: 0.3})
		if changed {
			t.Fatal("should not transition with fewer than 4 consecutive ticks")
		}
	}
	if s.State() != ElasticNominal {
		t.Fatalf("expected Nominal after 3 ticks, got %s", s.State())
	}

	// 4th tick should transition to Expanding.
	_, changed := s.Evaluate(&GPPrediction{StdDev: 0.3})
	if !changed {
		t.Fatal("expected state change on 4th tick")
	}
	if s.State() != ElasticExpanding {
		t.Fatalf("expected Expanding, got %s", s.State())
	}

	// Continue evaluating — fraction interpolates toward expanded.
	for s.State() == ElasticExpanding {
		s.Evaluate(&GPPrediction{StdDev: 0.3})
	}
	if s.State() != ElasticExpanded {
		t.Fatalf("expected Expanded, got %s", s.State())
	}

	// Budget should be at expanded level: 0.15 * 200_000 = 30_000
	if got := s.TokenBudget(); got != 30_000 {
		t.Errorf("expanded budget: got %d, want 30000", got)
	}

	// Now shrink: StdDev drops below shrinkThreshold (0.1 * 1.2 = 0.12).
	for range 3 {
		s.Evaluate(&GPPrediction{StdDev: 0.05})
	}
	if s.State() != ElasticExpanded {
		t.Fatalf("expected Expanded after 3 shrink ticks, got %s", s.State())
	}

	// 4th tick triggers Shrinking.
	_, changed = s.Evaluate(&GPPrediction{StdDev: 0.05})
	if !changed {
		t.Fatal("expected state change to Shrinking")
	}
	if s.State() != ElasticShrinking {
		t.Fatalf("expected Shrinking, got %s", s.State())
	}

	// Continue until back to Nominal.
	for s.State() == ElasticShrinking {
		s.Evaluate(&GPPrediction{StdDev: 0.05})
	}
	if s.State() != ElasticNominal {
		t.Fatalf("expected Nominal after shrinking, got %s", s.State())
	}
	if got := s.TokenBudget(); got != 20_000 {
		t.Errorf("nominal budget after shrink: got %d, want 20000", got)
	}
}

func TestElasticSizer_HysteresisResetOnDrop(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	s := NewElasticSizer(desc)

	// Learn baseline.
	for range 8 {
		s.Evaluate(&GPPrediction{StdDev: 0.1})
	}

	// 3 ticks above threshold, then 1 below — resets hysteresis.
	for range 3 {
		s.Evaluate(&GPPrediction{StdDev: 0.3})
	}
	s.Evaluate(&GPPrediction{StdDev: 0.05}) // Drops below — reset.
	if s.hysteresisCounter != 0 {
		t.Errorf("hysteresis should reset on drop: got %d", s.hysteresisCounter)
	}
	if s.State() != ElasticNominal {
		t.Fatalf("expected Nominal, got %s", s.State())
	}
}

func TestElasticSizer_Reset(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	s := NewElasticSizer(desc)

	// Learn baseline and expand.
	for range 8 {
		s.Evaluate(&GPPrediction{StdDev: 0.1})
	}
	for range 4 {
		s.Evaluate(&GPPrediction{StdDev: 0.3})
	}
	if s.State() != ElasticExpanding {
		t.Fatalf("expected Expanding, got %s", s.State())
	}

	s.Reset()
	if s.State() != ElasticNominal {
		t.Fatalf("expected Nominal after Reset, got %s", s.State())
	}
	if got := s.TokenBudget(); got != 20_000 {
		t.Errorf("budget after Reset: got %d, want 20000", got)
	}
	if s.Frozen() {
		t.Error("should not be frozen after Reset")
	}
}

func TestElasticSizer_FreezePreventTransitions(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	s := NewElasticSizer(desc)

	// Learn baseline.
	for range 8 {
		s.Evaluate(&GPPrediction{StdDev: 0.1})
	}

	s.Freeze()
	if !s.Frozen() {
		t.Fatal("should be frozen")
	}

	// High StdDev should not cause transition while frozen.
	for range 10 {
		_, changed := s.Evaluate(&GPPrediction{StdDev: 1.0})
		if changed {
			t.Fatal("no transitions should occur while frozen")
		}
	}
	if s.State() != ElasticNominal {
		t.Fatalf("expected Nominal while frozen, got %s", s.State())
	}
}

func TestElasticSizer_NilPrediction(t *testing.T) {
	desc := AgentDescriptor{ContextWindow: 200_000, AgentType: "test", ModelID: "test"}
	s := NewElasticSizer(desc)

	budget, changed := s.Evaluate(nil)
	if changed {
		t.Fatal("nil prediction should not change state")
	}
	if budget != 20_000 {
		t.Errorf("budget: got %d, want 20000", budget)
	}
}
