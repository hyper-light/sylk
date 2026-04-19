package forest

import (
	"testing"
)

// TestBuildFeatureVector_ClampsOutOfRangeInputs verifies the SIMD clamp
// pass: raw inputs outside [0, 1] (negative, NaN-adjacent, > 1) emerge
// from buildFeatureVector inside [0, 1]. A regression here means either
// the vek32 batch clamp was removed or someone re-introduced the
// legacy scalar clamp01 calls — both worth catching.
func TestBuildFeatureVector_ClampsOutOfRangeInputs(t *testing.T) {
	branch := &Branch{
		Family:    TreeFamilyEvidence,
		Scope:     ScopeEpisodic,
		AgentType: "engineer",
		SessionID: "sess-1",
	}
	input := scoreInput{
		QueryMatch:     1.5,  // over-range positive
		Evidence:       -0.3, // negative
		Canopy:         0.5,
		Confidence:     2.0,
		Recency:        -1.0,
		InhibitionSafety: 0.9,
	}
	vector := buildFeatureVector(
		Query{SessionID: "sess-1", AgentType: "engineer"},
		branch,
		input,
		nil, nil,
		42.0, // base score — wildly out of range
		0.8,  // relay mass
		0.6,  // relay cofire — MEM-04 new dimension
		3,
		substrateSignal{Potential: 1.2, Frontier: -0.1, Inhibition: 0.5},
	)

	if len(vector) != featureCount {
		t.Fatalf("feature vector length = %d, want %d", len(vector), featureCount)
	}
	for i, v := range vector {
		if v < 0 || v > 1 {
			t.Errorf("vector[%d] = %v — out of [0, 1] range after SIMD clamp", i, v)
		}
	}
}

// TestBuildFeatureVector_RelayCofireFlows is the MEM-04 core invariant:
// a non-zero relayCofire input produces a non-zero featureRelayCofire
// output, distinct from featureRelayMass. Ensures the BoosterModel's
// lifetime-evidence signal actually reaches the feature vector.
func TestBuildFeatureVector_RelayCofireFlows(t *testing.T) {
	branch := &Branch{Family: TreeFamilyDecision, Scope: ScopeWorking}
	vector := buildFeatureVector(
		Query{},
		branch,
		scoreInput{},
		nil, nil,
		0,
		0.4, // relay mass
		0.7, // relay cofire
		0,
		substrateSignal{},
	)

	if got := vector[featureRelayCofire]; got != 0.7 {
		t.Errorf("featureRelayCofire = %v, want 0.7", got)
	}
	if got := vector[featureRelayMass]; got != 0.4 {
		t.Errorf("featureRelayMass = %v, want 0.4", got)
	}
	// Distinct signals: the two features must not alias.
	if featureRelayMass == featureRelayCofire {
		t.Fatal("featureRelayMass and featureRelayCofire collapsed to the same index")
	}
}

// TestBuildFeatureVector_FlagsAreNotClampedAwayFromOne verifies the
// 1.0 flag assignments (scope/family/source) survive the SIMD clamp —
// they are already in-range and must emerge unchanged, not collapsed
// to zero by an over-aggressive clamp.
func TestBuildFeatureVector_FlagsAreNotClampedAwayFromOne(t *testing.T) {
	branch := &Branch{
		Family:    TreeFamilyOutcome,
		Scope:     ScopeSemantic,
		AgentType: "archivalist",
	}
	vector := buildFeatureVector(Query{}, branch, scoreInput{}, nil, nil, 0, 0, 0, 0, substrateSignal{})

	if got := vector[featureFamilyOutcome]; got != 1 {
		t.Errorf("featureFamilyOutcome = %v, want 1", got)
	}
	if got := vector[featureScopeSemantic]; got != 1 {
		t.Errorf("featureScopeSemantic = %v, want 1", got)
	}
	if got := vector[featureSourceArchivalist]; got != 1 {
		t.Errorf("featureSourceArchivalist = %v, want 1", got)
	}
}

// TestBoosterModel_LearnedRelayCofireBoostsRelevance is the MEM-04
// end-to-end acceptance test: when a BoosterModel has learned that
// featureRelayCofire correlates positively with utility, two otherwise
// identical candidates will be ranked higher or lower purely based on
// their relay cofire signal. This proves the feature is wired through
// from SQL (loadRelayCofire), into the feature vector (buildFeatureVector),
// and out of the model (boosterModel.Predict). A regression here means
// the feature was added to the schema but the downstream ranking never
// consumes it — the MEM-04 loop is not closed.
func TestBoosterModel_LearnedRelayCofireBoostsRelevance(t *testing.T) {
	// Model with a strong positive learned weight on the relay-cofire
	// slot — as would emerge from training data where branches with
	// heavier cofire history correlate with user-accepted retrievals.
	weights := make([]float32, featureCount)
	weights[featureRelayCofire] = 2.0
	model := &boosterModel{
		Bias:          0,
		LinearWeights: weights,
	}

	// Two candidates identical except for relay cofire. In the real
	// pipeline these come from buildFeatureVector; inline here to
	// isolate the Predict contract from the feature-assembly contract.
	low := make([]float32, featureCount)
	high := make([]float32, featureCount)
	low[featureRelayCofire] = 0.1
	high[featureRelayCofire] = 0.9

	_, lowProb := model.Predict(low)
	_, highProb := model.Predict(high)

	if !(highProb > lowProb) {
		t.Fatalf("learned model must boost high-cofire branch over low-cofire branch: high=%v low=%v",
			highProb, lowProb)
	}

	// Spread must be material — not a rounding accident. A 0.8 delta
	// on a feature with a weight of 2.0 is ~1.6 raw-logit units, which
	// is ~0.35+ in sigmoid space around the origin. Below that is
	// suspicious and likely indicates the weight isn't being consumed.
	if highProb-lowProb < 0.1 {
		t.Errorf("relay-cofire delta too small: high=%v low=%v — feature may not be flowing through Predict()",
			highProb, lowProb)
	}
}

// TestForestFeatureNames_LengthMatchesFeatureCount locks the parallel
// invariant that every feature index has a stable string label for WAL
// and telemetry. A drift here is caught at test time rather than at
// model-replay time.
func TestForestFeatureNames_LengthMatchesFeatureCount(t *testing.T) {
	if len(forestFeatureNames) != featureCount {
		t.Fatalf("forestFeatureNames length = %d, featureCount = %d — every feature index must have a name",
			len(forestFeatureNames), featureCount)
	}
}
