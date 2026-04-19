package quality

import (
	"math"
	"testing"
)

// TestComputeScalarWeights_ProjectsHierarchicalPriors verifies the
// posterior → 5-float schema projection. Mapping:
//   Relevance = Vector + Bleve + Graph + CrossValidation
//   Freshness = Staleness
//   Trust     = TrustLevel + Authority
//   Density   = UsageFeedback
//   Redundancy = 0
func TestComputeScalarWeights_ProjectsHierarchicalPriors(t *testing.T) {
	q := PriorQualityWeights(DomainCode)
	got := ComputeScalarWeights(q)

	wantRelevance := q.VectorWeight.Mean() + q.BleveWeight.Mean() + q.GraphWeight.Mean() + q.CrossValidationBonus.Mean()
	if math.Abs(got.Relevance-wantRelevance) > 1e-9 {
		t.Errorf("Relevance = %v, want %v", got.Relevance, wantRelevance)
	}
	if got.Freshness != q.StalenessWeight.Mean() {
		t.Errorf("Freshness = %v, want %v", got.Freshness, q.StalenessWeight.Mean())
	}
	if math.Abs(got.Trust-(q.TrustLevelWeight.Mean()+q.AuthorityWeight.Mean())) > 1e-9 {
		t.Errorf("Trust = %v, want sum of TrustLevel + Authority", got.Trust)
	}
	if got.Density != q.UsageFeedbackWeight.Mean() {
		t.Errorf("Density = %v, want %v", got.Density, q.UsageFeedbackWeight.Mean())
	}
	if got.Redundancy != 0 {
		t.Errorf("Redundancy = %v, want 0 (scorer-populated)", got.Redundancy)
	}
}

// TestComputeScalarWeights_NilReturnsDefaults locks the nil-safe
// contract: callers without a manager get sensible defaults.
func TestComputeScalarWeights_NilReturnsDefaults(t *testing.T) {
	got := ComputeScalarWeights(nil)
	defaults := DefaultScalarWeights()
	if got != defaults {
		t.Errorf("nil projection = %+v, want defaults %+v", got, defaults)
	}
}

// TestSampleScalarWeights_DrawsInRange verifies the Thompson-sampled
// variant produces non-negative weights.
func TestSampleScalarWeights_DrawsInRange(t *testing.T) {
	q := PriorQualityWeights(DomainCode)
	for i := 0; i < 50; i++ {
		got := SampleScalarWeights(q)
		if got.Relevance < 0 || got.Freshness < 0 || got.Trust < 0 || got.Density < 0 {
			t.Fatalf("sample produced negative weight: %+v", got)
		}
	}
}

// TestScalarWeights_NormalizeSumsToOne verifies Normalize scales to a
// probability simplex and does not mutate the receiver.
func TestScalarWeights_NormalizeSumsToOne(t *testing.T) {
	w := ScalarWeights{Relevance: 2, Freshness: 1, Trust: 1, Density: 0.5, Redundancy: 0.5}
	normalized := w.Normalize()
	sum := normalized.Relevance + normalized.Freshness + normalized.Trust + normalized.Density + normalized.Redundancy
	if math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("normalized sum = %v, want 1.0", sum)
	}
	// Original unchanged.
	if w.Relevance != 2 {
		t.Errorf("Normalize mutated receiver: Relevance = %v, want 2", w.Relevance)
	}
}
