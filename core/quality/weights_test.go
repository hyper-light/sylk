package quality

import (
	"math"
	"testing"
)

// TestLearnedWeight_MeanMatchesBetaFormula verifies α/(α+β) for a range of
// (α, β) pairs. This is the core posterior-mean invariant — a regression
// here would misrank every retrieval result.
func TestLearnedWeight_MeanMatchesBetaFormula(t *testing.T) {
	cases := []struct {
		alpha, beta, want float64
	}{
		{1, 1, 0.5},
		{3, 7, 0.3},
		{5, 15, 0.25},
		{0.5, 9.5, 0.05},
	}
	for _, tc := range cases {
		w := NewLearnedWeight(tc.alpha, tc.beta)
		if got := w.Mean(); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("Mean(α=%v, β=%v) = %v, want %v", tc.alpha, tc.beta, got, tc.want)
		}
	}
}

// TestLearnedWeight_NilMeanIsZero verifies the nil-safe contract: scoring
// code that hits an unitialized weight field must see zero, not a nil
// dereference.
func TestLearnedWeight_NilMeanIsZero(t *testing.T) {
	var w *LearnedWeight
	if got := w.Mean(); got != 0 {
		t.Errorf("nil Mean = %v, want 0", got)
	}
	if got := w.Variance(); got != 0 {
		t.Errorf("nil Variance = %v, want 0", got)
	}
	if got := w.Sample(); got != 0 {
		t.Errorf("nil Sample = %v, want 0", got)
	}
	if got := w.Confidence(); got != 0 {
		t.Errorf("nil Confidence = %v, want 0", got)
	}
	if w.Update(true, 1) != nil {
		t.Error("nil Update should return nil")
	}
	if w.Decay(0.1) != nil {
		t.Error("nil Decay should return nil")
	}
}

// TestLearnedWeight_UpdateIsImmutable verifies the RCU contract: Update
// returns a new value without mutating the receiver. Concurrent readers
// must continue observing the pre-update distribution.
func TestLearnedWeight_UpdateIsImmutable(t *testing.T) {
	w := NewLearnedWeight(1, 1)
	before := *w
	_ = w.Update(true, 1)
	if *w != before {
		t.Errorf("Update mutated receiver: before=%+v after=%+v", before, *w)
	}
}

// TestLearnedWeight_UpdateFoldsObservation verifies Beta-posterior update:
// success adds to α, failure adds to β, and EffectiveSamples tracks total.
func TestLearnedWeight_UpdateFoldsObservation(t *testing.T) {
	w := NewLearnedWeight(2, 8)
	next := w.Update(true, 1)
	if next.Alpha != 3 {
		t.Errorf("success: Alpha = %v, want 3", next.Alpha)
	}
	if next.Beta != 8 {
		t.Errorf("success: Beta = %v, want 8 (unchanged)", next.Beta)
	}
	if next.EffectiveSamples != 1 {
		t.Errorf("EffectiveSamples = %v, want 1", next.EffectiveSamples)
	}

	after := next.Update(false, 1)
	if after.Alpha != 3 || after.Beta != 9 {
		t.Errorf("failure: (α, β) = (%v, %v), want (3, 9)", after.Alpha, after.Beta)
	}
	if after.EffectiveSamples != 2 {
		t.Errorf("EffectiveSamples = %v, want 2", after.EffectiveSamples)
	}
}

// TestLearnedWeight_DecayBlendsTowardPrior verifies Decay(r) blends the
// current posterior toward the prior by fraction r. A full decay (r=1)
// snaps back to the prior; r=0 is a no-op.
func TestLearnedWeight_DecayBlendsTowardPrior(t *testing.T) {
	w := NewLearnedWeight(10, 2) // Far from prior (1, 1) after some updates
	w.PriorAlpha = 1
	w.PriorBeta = 1
	w.EffectiveSamples = 11

	// Full decay → snaps to prior.
	full := w.Decay(1)
	if math.Abs(full.Alpha-1) > 1e-9 || math.Abs(full.Beta-1) > 1e-9 {
		t.Errorf("full decay: (α, β) = (%v, %v), want (1, 1)", full.Alpha, full.Beta)
	}
	if full.EffectiveSamples != 0 {
		t.Errorf("full decay EffectiveSamples = %v, want 0", full.EffectiveSamples)
	}

	// 50% decay → midpoint.
	half := w.Decay(0.5)
	if math.Abs(half.Alpha-5.5) > 1e-9 {
		t.Errorf("half decay Alpha = %v, want 5.5", half.Alpha)
	}
}

// TestLearnedWeight_SampleStaysInRange verifies Sample() returns values in
// [0, 1]. A Beta(α, β) sample is mathematically in (0, 1) for positive
// parameters; this test catches any regression that substituted a buggy
// sampler.
func TestLearnedWeight_SampleStaysInRange(t *testing.T) {
	w := NewLearnedWeight(5, 5)
	for i := 0; i < 100; i++ {
		s := w.Sample()
		if s < 0 || s > 1 {
			t.Fatalf("Sample returned %v, want [0, 1]", s)
		}
	}
}

// TestLearnedWeight_ConfidenceGrowsWithSamples verifies the confidence
// monotonicity: more observations → higher confidence, asymptotic to 1.
func TestLearnedWeight_ConfidenceGrowsWithSamples(t *testing.T) {
	w := NewLearnedWeight(1, 1)
	prior := w.Confidence()
	for i := 0; i < 5; i++ {
		w = w.Update(true, 1)
	}
	after := w.Confidence()
	if after <= prior {
		t.Errorf("confidence did not grow: before=%v after=%v", prior, after)
	}
	if after >= 1 {
		t.Errorf("confidence = %v, should be < 1 (asymptotic)", after)
	}
}

// TestLearnedWeight_CloneIsDeep verifies Clone does not share the receiver's
// backing memory — modifying the clone via Update must not change the
// original even though Update itself is immutable. This is a belt-and-
// braces test: the invariant holds even if Update's immutability regresses.
func TestLearnedWeight_CloneIsDeep(t *testing.T) {
	w := NewLearnedWeight(3, 7)
	clone := w.Clone()
	clone.Alpha = 99 // direct mutation of clone
	if w.Alpha == 99 {
		t.Error("Clone is shallow — original was mutated")
	}
}

// TestQualityWeights_MeanVectorOrderingMatchesSignalCount locks the
// canonical ordering contract: MeanVector has exactly SignalCount
// entries, each matching a SignalKind index. Linear scorers rely on
// this alignment.
func TestQualityWeights_MeanVectorOrderingMatchesSignalCount(t *testing.T) {
	q := PriorQualityWeights(DomainCode)
	means := q.MeanVector()
	if len(means) != SignalCount() {
		t.Fatalf("MeanVector length = %d, want %d", len(means), SignalCount())
	}
}

// TestQualityWeights_CloneIsIndependent verifies session forks cannot
// accidentally mutate the shared prior by updating their clone. This is
// the foundation of SCORE-05 session isolation.
func TestQualityWeights_CloneIsIndependent(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	clone := base.Clone()
	clone.VectorWeight = clone.VectorWeight.Update(true, 10)
	if base.VectorWeight.EffectiveSamples != 0 {
		t.Errorf("base mutated after clone update: EffectiveSamples = %v", base.VectorWeight.EffectiveSamples)
	}
}

// TestLearnedDecayRate_GammaMean verifies E[λ] = α/β.
func TestLearnedDecayRate_GammaMean(t *testing.T) {
	cases := []struct {
		alpha, beta, want float64
	}{
		{1, 20, 0.05},
		{1, 1000, 0.001},
		{2, 10, 0.2},
	}
	for _, tc := range cases {
		r := NewLearnedDecayRate(tc.alpha, tc.beta)
		if got := r.Mean(); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("Mean(α=%v, β=%v) = %v, want %v", tc.alpha, tc.beta, got, tc.want)
		}
	}
}

// TestLearnedDecayRate_UpdateIncrementsParameters verifies count-domain
// Bayesian update: α += eventCount, β += intervalLength.
func TestLearnedDecayRate_UpdateIncrementsParameters(t *testing.T) {
	r := NewLearnedDecayRate(1, 10)
	next := r.Update(3, 100)
	if next.Alpha != 4 {
		t.Errorf("Alpha = %v, want 4", next.Alpha)
	}
	if next.Beta != 110 {
		t.Errorf("Beta = %v, want 110", next.Beta)
	}
	if next.EffectiveSamples != 100 {
		t.Errorf("EffectiveSamples = %v, want 100", next.EffectiveSamples)
	}
}
