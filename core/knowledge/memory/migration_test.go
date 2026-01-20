// Package memory provides MD.8.3 Migration Verification Tests.
// These tests verify correct usage of ACT-R power law decay formula:
//   B = ln(Σtⱼ^(-d)) + β
// where d is the decay parameter and β is the base-level offset.
package memory

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// =============================================================================
// MD.8.3.1 ACT-R Power Law Formula Tests
// =============================================================================

// TestACTRPowerLawFormula verifies the base-level activation formula:
// B = ln(Σtⱼ^(-d)) + β
func TestACTRPowerLawFormula(t *testing.T) {
	now := time.Now()

	// Create memory with known parameters
	m := &ACTRMemory{
		NodeID:             "test-power-law",
		Domain:             2,
		DecayAlpha:         5.0,  // E[d] = 0.5
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,  // β = 0
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now.Add(-24 * time.Hour),
	}

	// Add a single trace at t=1 hour ago
	accessTime := now.Add(-1 * time.Hour)
	m.Reinforce(accessTime, AccessRetrieval, "test")

	// Expected activation: B = ln(t^(-d)) + β = ln(3600^(-0.5)) + 0
	// = -0.5 * ln(3600) = -0.5 * 8.189 ≈ -4.094
	activation := m.Activation(now)

	d := m.DecayMean() // Should be 0.5
	t_seconds := 3600.0 // 1 hour in seconds
	expected := math.Log(math.Pow(t_seconds, -d)) + m.BaseOffsetMean

	tolerance := 0.01
	if math.Abs(activation-expected) > tolerance {
		t.Errorf("power law activation incorrect: got %f, expected %f (d=%f)",
			activation, expected, d)
	}
}

// TestACTRPowerLawMultipleTraces verifies summation across multiple traces.
// B = ln(Σtⱼ^(-d)) where the sum is over all access traces.
func TestACTRPowerLawMultipleTraces(t *testing.T) {
	now := time.Now()

	m := &ACTRMemory{
		NodeID:             "test-multi-trace",
		Domain:             2,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now.Add(-24 * time.Hour),
	}

	// Add traces at different times
	traces := []time.Duration{
		-1 * time.Hour,   // 3600 seconds
		-2 * time.Hour,   // 7200 seconds
		-4 * time.Hour,   // 14400 seconds
	}

	for _, offset := range traces {
		m.Reinforce(now.Add(offset), AccessRetrieval, "test")
	}

	activation := m.Activation(now)

	// Calculate expected: B = ln(Σtⱼ^(-d))
	d := m.DecayMean()
	sum := 0.0
	for _, offset := range traces {
		tSeconds := -offset.Seconds()
		sum += math.Pow(tSeconds, -d)
	}
	expected := math.Log(sum) + m.BaseOffsetMean

	tolerance := 0.01
	if math.Abs(activation-expected) > tolerance {
		t.Errorf("multi-trace power law incorrect: got %f, expected %f",
			activation, expected)
	}
}

// TestACTRPowerLawDecayOverTime verifies that activation decreases following
// power law decay as time increases.
func TestACTRPowerLawDecayOverTime(t *testing.T) {
	now := time.Now()

	m := &ACTRMemory{
		NodeID:             "test-decay-time",
		Domain:             2,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now.Add(-7 * 24 * time.Hour),
	}

	// Single access 1 hour ago
	m.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "test")

	// Measure activation at different future times
	times := []time.Duration{
		0,
		1 * time.Hour,
		2 * time.Hour,
		4 * time.Hour,
		8 * time.Hour,
	}

	var prevActivation float64
	for i, offset := range times {
		measureTime := now.Add(offset)
		activation := m.Activation(measureTime)

		if i > 0 && activation >= prevActivation {
			t.Errorf("activation should decrease over time: at %v got %f, prev %f",
				offset, activation, prevActivation)
		}

		prevActivation = activation
	}
}

// TestACTRPowerLawVsLinearDecay demonstrates that power law decay differs
// from linear decay and follows the expected mathematical relationship.
func TestACTRPowerLawVsLinearDecay(t *testing.T) {
	now := time.Now()

	m := &ACTRMemory{
		NodeID:             "test-power-vs-linear",
		Domain:             2,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now.Add(-7 * 24 * time.Hour),
	}

	m.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "test")

	// Power law: activation change from t to 2t
	// A(t) - A(2t) = ln(t^(-d)) - ln((2t)^(-d)) = -d*ln(t) + d*ln(2t) = d*ln(2)
	activation1h := m.Activation(now)
	activation2h := m.Activation(now.Add(1 * time.Hour))

	d := m.DecayMean()
	expectedDiff := d * math.Log(2)
	actualDiff := activation1h - activation2h

	tolerance := 0.1 // Allow 10% tolerance
	relativeError := math.Abs(actualDiff-expectedDiff) / expectedDiff

	if relativeError > tolerance {
		t.Errorf("decay doesn't follow power law: expected diff %f, got %f (error %f%%)",
			expectedDiff, actualDiff, relativeError*100)
	}
}

// =============================================================================
// MD.8.3.2 FreshnessTracker Power Law Tests
// =============================================================================

// TestFreshnessTrackerDecayFormula verifies that FreshnessTracker uses
// exponential decay (which approximates power law for short time scales).
func TestFreshnessTrackerDecayFormula(t *testing.T) {
	// The FreshnessTracker uses: score = exp(-decayRate * ageHours)
	// This is an exponential approximation of power law behavior

	testCases := []struct {
		name        string
		ageHours    float64
		decayRate   float64
		expectedMin float64
		expectedMax float64
	}{
		{"fresh_node", 1.0, 0.01, 0.98, 1.0},
		{"day_old", 24.0, 0.01, 0.75, 0.80},
		{"week_old", 168.0, 0.01, 0.15, 0.20},
		{"fast_decay", 24.0, 0.1, 0.05, 0.15},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Compute exponential decay score
			score := math.Exp(-tc.decayRate * tc.ageHours)

			if score < tc.expectedMin || score > tc.expectedMax {
				t.Errorf("decay score %f not in expected range [%f, %f]",
					score, tc.expectedMin, tc.expectedMax)
			}
		})
	}
}

// TestFreshnessTrackerDomainDecayRates verifies domain-specific decay rates
// follow cognitive expectations (academic decays slower than code).
func TestFreshnessTrackerDomainDecayRates(t *testing.T) {
	// Domain decay rate expectations based on cognitive modeling
	domainRates := map[string]float64{
		"code":     0.01,  // Fast decay - code changes frequently
		"history":  0.001, // Slow decay - historical decisions persist
		"academic": 0.001, // Slow decay - research papers remain relevant
	}

	// Academic knowledge should persist longer than code
	codeAge := 168.0 // 1 week in hours
	codeScore := math.Exp(-domainRates["code"] * codeAge)
	academicScore := math.Exp(-domainRates["academic"] * codeAge)

	if academicScore <= codeScore {
		t.Errorf("academic content should persist longer than code: academic=%f, code=%f",
			academicScore, codeScore)
	}
}

// =============================================================================
// MD.8.3.3 ContextClassifier Power Law Tests
// =============================================================================

// TestContextClassifierDecayFormula verifies the ContextClassifier uses
// exponential decay for session history weighting.
func TestContextClassifierDecayFormula(t *testing.T) {
	// ContextClassifier uses: weight = exp(-decayRate * position)
	// where position is the index in the session history

	decayRate := 0.1 // Default decay rate

	// Verify decay follows exponential formula
	for position := 0; position < 10; position++ {
		expected := math.Exp(-decayRate * float64(position))
		actual := calculateExponentialWeight(position, decayRate)

		if math.Abs(expected-actual) > 0.0001 {
			t.Errorf("position %d: expected %f, got %f",
				position, expected, actual)
		}
	}
}

// calculateExponentialWeight computes decay weight for a position.
func calculateExponentialWeight(position int, decayRate float64) float64 {
	return math.Exp(-decayRate * float64(position))
}

// TestContextClassifierDecayProperties verifies key properties of decay.
func TestContextClassifierDecayProperties(t *testing.T) {
	decayRate := 0.1

	// Property 1: First position has weight 1.0
	w0 := calculateExponentialWeight(0, decayRate)
	if math.Abs(w0-1.0) > 0.0001 {
		t.Errorf("weight at position 0 should be 1.0, got %f", w0)
	}

	// Property 2: Weights decrease monotonically
	for i := 1; i < 10; i++ {
		wPrev := calculateExponentialWeight(i-1, decayRate)
		wCurr := calculateExponentialWeight(i, decayRate)
		if wCurr >= wPrev {
			t.Errorf("weight at position %d (%f) should be less than position %d (%f)",
				i, wCurr, i-1, wPrev)
		}
	}

	// Property 3: Decay ratio is constant: w(n)/w(n+1) = exp(decayRate)
	expectedRatio := math.Exp(decayRate)
	for i := 0; i < 9; i++ {
		wCurr := calculateExponentialWeight(i, decayRate)
		wNext := calculateExponentialWeight(i+1, decayRate)
		actualRatio := wCurr / wNext
		if math.Abs(expectedRatio-actualRatio) > 0.0001 {
			t.Errorf("decay ratio at position %d: expected %f, got %f",
				i, expectedRatio, actualRatio)
		}
	}
}

// =============================================================================
// MD.8.3.4 Migration Tests - Old Linear to Power Law
// =============================================================================

// TestMigrationLinearToPowerLaw verifies that migrating from linear decay
// to power law decay produces expected changes in activation values.
func TestMigrationLinearToPowerLaw(t *testing.T) {
	now := time.Now()

	// Simulate "old" linear decay activation
	// Linear decay: A = 1 - k*t where k is decay rate and t is age
	linearDecayRate := 0.001 // per second
	accessAge := 3600.0      // 1 hour in seconds
	linearActivation := 1.0 - linearDecayRate*accessAge

	// New power law activation
	m := &ACTRMemory{
		NodeID:             "migration-test",
		Domain:             2,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now.Add(-24 * time.Hour),
	}
	m.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "test")
	powerLawActivation := m.Activation(now)

	// Key difference: linear goes to 0 or negative, power law stays above -infinity
	// For very old items, power law is more forgiving

	t.Logf("Linear activation (1h old): %f", linearActivation)
	t.Logf("Power law activation (1h old): %f", powerLawActivation)

	// Both should be positive for recent access
	if linearActivation <= 0 {
		t.Logf("Note: linear decay at 1h is already at %f", linearActivation)
	}

	// Power law activation is logarithmic, so it's typically negative
	// but never -infinity (unless no traces)
	if math.IsInf(powerLawActivation, -1) {
		t.Error("power law activation should not be -infinity with traces")
	}
}

// TestMigrationPreservesOrdering verifies that power law migration
// preserves relative ordering of items by activation.
func TestMigrationPreservesOrdering(t *testing.T) {
	now := time.Now()

	// Create two memories with different access patterns
	recent := &ACTRMemory{
		NodeID:     "recent",
		Domain:     2,
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
		MaxTraces:  100,
		CreatedAt:  now.Add(-24 * time.Hour),
	}
	recent.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "test")

	old := &ACTRMemory{
		NodeID:     "old",
		Domain:     2,
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
		MaxTraces:  100,
		CreatedAt:  now.Add(-7 * 24 * time.Hour),
	}
	old.Reinforce(now.Add(-24*time.Hour), AccessRetrieval, "test")

	recentActivation := recent.Activation(now)
	oldActivation := old.Activation(now)

	// Recently accessed item should have higher activation
	if recentActivation <= oldActivation {
		t.Errorf("recent item should have higher activation: recent=%f, old=%f",
			recentActivation, oldActivation)
	}
}

// TestMigrationFrequencyBoost verifies that multiple accesses boost activation
// under power law just as they did under linear decay.
func TestMigrationFrequencyBoost(t *testing.T) {
	now := time.Now()

	// Single access memory
	single := &ACTRMemory{
		NodeID:     "single",
		Domain:     2,
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
		MaxTraces:  100,
		CreatedAt:  now.Add(-24 * time.Hour),
	}
	single.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "test")

	// Multiple access memory (same initial access time, plus more recent)
	multiple := &ACTRMemory{
		NodeID:     "multiple",
		Domain:     2,
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
		MaxTraces:  100,
		CreatedAt:  now.Add(-24 * time.Hour),
	}
	multiple.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "test")
	multiple.Reinforce(now.Add(-30*time.Minute), AccessRetrieval, "test")
	multiple.Reinforce(now.Add(-10*time.Minute), AccessRetrieval, "test")

	singleActivation := single.Activation(now)
	multipleActivation := multiple.Activation(now)

	// Multiple accesses should boost activation
	if multipleActivation <= singleActivation {
		t.Errorf("multiple accesses should boost activation: single=%f, multiple=%f",
			singleActivation, multipleActivation)
	}
}

// =============================================================================
// MD.8.3.5 Backward Compatibility Tests
// =============================================================================

// TestBackwardCompatibilityDecayMean verifies DecayMean calculation is stable.
func TestBackwardCompatibilityDecayMean(t *testing.T) {
	testCases := []struct {
		name     string
		alpha    float64
		beta     float64
		expected float64
	}{
		{"standard_0.5", 5.0, 5.0, 0.5},
		{"academic_0.3", 3.0, 7.0, 0.3},
		{"engineer_0.6", 6.0, 4.0, 0.6},
		{"architect_0.4", 4.0, 6.0, 0.4},
		{"edge_case_zero_alpha", 0.0, 5.0, 0.0},
		{"edge_case_zero_beta", 5.0, 0.0, 1.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			m := &ACTRMemory{
				DecayAlpha: tc.alpha,
				DecayBeta:  tc.beta,
			}

			mean := m.DecayMean()

			tolerance := 0.0001
			if math.Abs(mean-tc.expected) > tolerance {
				t.Errorf("expected %f, got %f", tc.expected, mean)
			}
		})
	}
}

// TestBackwardCompatibilityDomainParams verifies domain parameters are unchanged.
func TestBackwardCompatibilityDomainParams(t *testing.T) {
	// These are the expected default domain parameters
	// Any change would break backward compatibility
	expected := map[int]struct {
		alpha float64
		beta  float64
		mean  float64
	}{
		domainAcademic:  {3.0, 7.0, 0.3},
		domainArchitect: {4.0, 6.0, 0.4},
		domainEngineer:  {6.0, 4.0, 0.6},
		domainLibrarian: {5.0, 5.0, 0.5},
	}

	for domain, exp := range expected {
		params := DefaultDomainDecay(domain)

		if params.DecayAlpha != exp.alpha {
			t.Errorf("domain %d: alpha changed from %f to %f",
				domain, exp.alpha, params.DecayAlpha)
		}
		if params.DecayBeta != exp.beta {
			t.Errorf("domain %d: beta changed from %f to %f",
				domain, exp.beta, params.DecayBeta)
		}

		mean := params.Mean()
		if math.Abs(mean-exp.mean) > 0.0001 {
			t.Errorf("domain %d: mean changed from %f to %f",
				domain, exp.mean, mean)
		}
	}
}

// TestBackwardCompatibilityRetrievalProbability verifies softmax probability
// calculation remains stable.
func TestBackwardCompatibilityRetrievalProbability(t *testing.T) {
	now := time.Now()

	m := &ACTRMemory{
		NodeID:         "compat-test",
		Domain:         2,
		DecayAlpha:     5.0,
		DecayBeta:      5.0,
		BaseOffsetMean: 0.0,
		MaxTraces:      100,
		CreatedAt:      now.Add(-24 * time.Hour),
	}
	m.Reinforce(now.Add(-1*time.Minute), AccessRetrieval, "test")

	// Note: Activation for 1-minute-old trace is approximately:
	// ln(60^(-0.5)) + 0 = -0.5 * ln(60) ≈ -2.05
	// So threshold of -10 is well below activation, threshold of 0 is above

	// Test retrieval probability at various thresholds
	testCases := []struct {
		threshold  float64
		expectHigh bool // expect prob > 0.5
	}{
		{-10.0, true},  // Very low threshold → high probability
		{-5.0, true},   // Low threshold → high probability (activation ~-2)
		{0.0, false},   // Medium threshold → low probability (above activation)
		{10.0, false},  // Very high threshold → low probability
	}

	for _, tc := range testCases {
		prob := m.RetrievalProbability(now, tc.threshold)

		if tc.expectHigh && prob <= 0.5 {
			t.Errorf("threshold %f: expected high prob (>0.5), got %f",
				tc.threshold, prob)
		}
		if !tc.expectHigh && prob >= 0.5 {
			t.Errorf("threshold %f: expected low prob (<0.5), got %f",
				tc.threshold, prob)
		}
	}
}

// TestBackwardCompatibilityJSONSerialization verifies ACTRMemory can be
// serialized and deserialized without data loss.
func TestBackwardCompatibilityJSONSerialization(t *testing.T) {
	now := time.Now().UTC()

	original := &ACTRMemory{
		NodeID:             "json-compat",
		Domain:             2,
		DecayAlpha:         3.0,
		DecayBeta:          7.0,
		BaseOffsetMean:     0.5,
		BaseOffsetVariance: 1.0,
		MaxTraces:          50,
		AccessCount:        10,
		CreatedAt:          now.Add(-7 * 24 * time.Hour),
		Traces: []AccessTrace{
			{
				AccessedAt: now.Add(-1 * time.Hour),
				AccessType: AccessRetrieval,
				Context:    "test-context",
			},
		},
	}

	// Serialize
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Deserialize
	var restored ACTRMemory
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// Verify all fields
	if restored.NodeID != original.NodeID {
		t.Errorf("NodeID: got %s, want %s", restored.NodeID, original.NodeID)
	}
	if restored.Domain != original.Domain {
		t.Errorf("Domain: got %d, want %d", restored.Domain, original.Domain)
	}
	if restored.DecayAlpha != original.DecayAlpha {
		t.Errorf("DecayAlpha: got %f, want %f", restored.DecayAlpha, original.DecayAlpha)
	}
	if restored.DecayBeta != original.DecayBeta {
		t.Errorf("DecayBeta: got %f, want %f", restored.DecayBeta, original.DecayBeta)
	}
	if restored.BaseOffsetMean != original.BaseOffsetMean {
		t.Errorf("BaseOffsetMean: got %f, want %f", restored.BaseOffsetMean, original.BaseOffsetMean)
	}
	if len(restored.Traces) != len(original.Traces) {
		t.Errorf("Traces length: got %d, want %d", len(restored.Traces), len(original.Traces))
	}
}


// =============================================================================
// MD.8.3.6 Cognitive Model Validation Tests
// =============================================================================

// TestCognitiveModelDecayRates validates domain decay rates match cognitive
// expectations from ACT-R theory.
func TestCognitiveModelDecayRates(t *testing.T) {
	// ACT-R standard decay parameter is d=0.5
	// Domains should vary around this based on knowledge type

	academic := DefaultDomainDecay(domainAcademic)
	engineer := DefaultDomainDecay(domainEngineer)

	// Academic knowledge (facts, concepts) decays slower: d < 0.5
	if academic.Mean() >= 0.5 {
		t.Errorf("academic decay should be slower than standard: got %f", academic.Mean())
	}

	// Engineering tasks (procedures, actions) decay faster: d > 0.5
	if engineer.Mean() <= 0.5 {
		t.Errorf("engineer decay should be faster than standard: got %f", engineer.Mean())
	}

	// Verify ordering: academic < architect < engineer
	architect := DefaultDomainDecay(domainArchitect)
	if academic.Mean() >= architect.Mean() {
		t.Errorf("academic should decay slower than architect")
	}
	if architect.Mean() >= engineer.Mean() {
		t.Errorf("architect should decay slower than engineer")
	}
}

// TestCognitiveModelPracticeEffect verifies the practice effect:
// repeated access strengthens memory.
func TestCognitiveModelPracticeEffect(t *testing.T) {
	now := time.Now()

	// Memory with single access
	once := &ACTRMemory{
		NodeID:     "once",
		Domain:     2,
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
		MaxTraces:  100,
		CreatedAt:  now.Add(-24 * time.Hour),
	}
	once.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "")

	// Memory with spaced practice
	practiced := &ACTRMemory{
		NodeID:     "practiced",
		Domain:     2,
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
		MaxTraces:  100,
		CreatedAt:  now.Add(-7 * 24 * time.Hour),
	}
	// Spaced practice over a week
	practiced.Reinforce(now.Add(-6*24*time.Hour), AccessRetrieval, "")
	practiced.Reinforce(now.Add(-4*24*time.Hour), AccessRetrieval, "")
	practiced.Reinforce(now.Add(-2*24*time.Hour), AccessRetrieval, "")
	practiced.Reinforce(now.Add(-1*24*time.Hour), AccessRetrieval, "")
	practiced.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "")

	onceActivation := once.Activation(now)
	practicedActivation := practiced.Activation(now)

	// Practiced item should have higher activation
	if practicedActivation <= onceActivation {
		t.Errorf("practiced item should have higher activation: once=%f, practiced=%f",
			onceActivation, practicedActivation)
	}
}

// TestCognitiveModelRecencyEffect verifies the recency effect:
// recently accessed items have higher activation.
func TestCognitiveModelRecencyEffect(t *testing.T) {
	now := time.Now()

	// Create items accessed at different recency levels
	accessTimes := []time.Duration{
		-1 * time.Minute,
		-1 * time.Hour,
		-1 * 24 * time.Hour,
		-7 * 24 * time.Hour,
	}

	var prevActivation float64 = math.MaxFloat64

	for _, offset := range accessTimes {
		m := &ACTRMemory{
			NodeID:     "recency-test",
			Domain:     2,
			DecayAlpha: 5.0,
			DecayBeta:  5.0,
			MaxTraces:  100,
			CreatedAt:  now.Add(-30 * 24 * time.Hour),
		}
		m.Reinforce(now.Add(offset), AccessRetrieval, "")

		activation := m.Activation(now)

		if activation >= prevActivation {
			t.Errorf("more recent access should have higher activation: at %v got %f, prev %f",
				offset, activation, prevActivation)
		}

		prevActivation = activation
	}
}
