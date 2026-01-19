package memory

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// =============================================================================
// MD.2.1 AccessTrace Tests
// =============================================================================

func TestAccessType_String(t *testing.T) {
	tests := []struct {
		name     string
		at       AccessType
		expected string
	}{
		{"retrieval", AccessRetrieval, "retrieval"},
		{"reinforcement", AccessReinforcement, "reinforcement"},
		{"creation", AccessCreation, "creation"},
		{"reference", AccessReference, "reference"},
		{"invalid", AccessType(99), "access_type(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.at.String()
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestParseAccessType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected AccessType
		ok       bool
	}{
		{"retrieval", "retrieval", AccessRetrieval, true},
		{"reinforcement", "reinforcement", AccessReinforcement, true},
		{"creation", "creation", AccessCreation, true},
		{"reference", "reference", AccessReference, true},
		{"invalid", "invalid", AccessType(0), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := ParseAccessType(tt.input)
			if ok != tt.ok {
				t.Errorf("expected ok=%v, got %v", tt.ok, ok)
			}
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestAccessType_JSON(t *testing.T) {
	tests := []struct {
		name     string
		at       AccessType
		expected string
	}{
		{"retrieval", AccessRetrieval, `"retrieval"`},
		{"reinforcement", AccessReinforcement, `"reinforcement"`},
		{"creation", AccessCreation, `"creation"`},
		{"reference", AccessReference, `"reference"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.at)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			if string(data) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, string(data))
			}

			var decoded AccessType
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if decoded != tt.at {
				t.Errorf("expected %v, got %v", tt.at, decoded)
			}
		})
	}
}

func TestAccessTrace_JSON(t *testing.T) {
	now := time.Now().UTC()
	trace := AccessTrace{
		AccessedAt: now,
		AccessType: AccessRetrieval,
		Context:    "test-context",
	}

	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded AccessTrace
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.AccessType != trace.AccessType {
		t.Errorf("access type mismatch")
	}
	if decoded.Context != trace.Context {
		t.Errorf("context mismatch")
	}
}

// =============================================================================
// MD.2.2 ACTRMemory Tests
// =============================================================================

func TestACTRMemory_Activation(t *testing.T) {
	now := time.Now()

	m := &ACTRMemory{
		NodeID:             "test-node",
		Domain:             2,
		DecayAlpha:         3.0,
		DecayBeta:          7.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now.Add(-24 * time.Hour),
	}

	// Add traces at different times
	m.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "context1")
	m.Reinforce(now.Add(-10*time.Minute), AccessRetrieval, "context2")
	m.Reinforce(now.Add(-1*time.Minute), AccessRetrieval, "context3")

	activation := m.Activation(now)

	// Activation should be a finite number
	if math.IsInf(activation, 0) || math.IsNaN(activation) {
		t.Errorf("activation should be finite, got %f", activation)
	}

	// Recent accesses should yield higher activation
	if activation < -10 {
		t.Errorf("activation too low for recent accesses: %f", activation)
	}
}

func TestACTRMemory_Activation_PowerLawDecay(t *testing.T) {
	now := time.Now()

	m := &ACTRMemory{
		NodeID:             "test-node",
		Domain:             2,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now.Add(-24 * time.Hour),
	}

	// Add a single trace and measure activation at different times
	m.Reinforce(now.Add(-1*time.Hour), AccessRetrieval, "initial")

	// Activation at t=1 hour
	activation1 := m.Activation(now)

	// Activation at t=2 hours
	activation2 := m.Activation(now.Add(1 * time.Hour))

	// Activation should decrease over time (power law decay)
	if activation2 >= activation1 {
		t.Errorf("activation should decay over time: t1=%f, t2=%f",
			activation1, activation2)
	}

	// Check that decay follows power law (approximately)
	// For power law: A(2t) ≈ A(t) - d*ln(2)
	d := m.DecayMean()
	expectedDiff := d * math.Log(2)
	actualDiff := activation1 - activation2

	// Allow 20% tolerance due to discrete time sampling
	if math.Abs(actualDiff-expectedDiff)/expectedDiff > 0.2 {
		t.Errorf("decay doesn't follow power law: expected diff %f, got %f",
			expectedDiff, actualDiff)
	}
}

func TestACTRMemory_DecayMean(t *testing.T) {
	tests := []struct {
		name     string
		alpha    float64
		beta     float64
		expected float64
	}{
		{"academic", 3.0, 7.0, 0.3},
		{"architect", 4.0, 6.0, 0.4},
		{"engineer", 6.0, 4.0, 0.6},
		{"standard", 5.0, 5.0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &ACTRMemory{
				DecayAlpha: tt.alpha,
				DecayBeta:  tt.beta,
			}

			mean := m.DecayMean()
			if math.Abs(mean-tt.expected) > 0.01 {
				t.Errorf("expected %f, got %f", tt.expected, mean)
			}
		})
	}
}

func TestACTRMemory_DecaySample(t *testing.T) {
	m := &ACTRMemory{
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
	}

	// Sample multiple times and verify they're in valid range
	samples := make([]float64, 100)
	for i := 0; i < 100; i++ {
		sample := m.DecaySample()
		if sample < 0 || sample > 1 {
			t.Errorf("sample out of range [0,1]: %f", sample)
		}
		samples[i] = sample
	}

	// Verify samples have reasonable mean (should be close to 0.5)
	sum := 0.0
	for _, s := range samples {
		sum += s
	}
	mean := sum / float64(len(samples))

	if math.Abs(mean-0.5) > 0.15 {
		t.Errorf("sample mean too far from expected 0.5: %f", mean)
	}
}

func TestACTRMemory_RetrievalProbability(t *testing.T) {
	now := time.Now()

	m := &ACTRMemory{
		NodeID:             "test-node",
		Domain:             2,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now.Add(-24 * time.Hour),
	}

	// Add recent trace
	m.Reinforce(now.Add(-1*time.Minute), AccessRetrieval, "recent")

	// High activation → high retrieval probability
	prob := m.RetrievalProbability(now, -5.0)
	if prob < 0.5 {
		t.Errorf("recent access should have high retrieval prob: %f", prob)
	}

	// Test with high threshold → low probability
	probHighThreshold := m.RetrievalProbability(now, 10.0)
	if probHighThreshold > 0.5 {
		t.Errorf("high threshold should yield low prob: %f", probHighThreshold)
	}
}

func TestACTRMemory_Reinforce(t *testing.T) {
	now := time.Now()

	m := &ACTRMemory{
		NodeID:             "test-node",
		Domain:             2,
		DecayAlpha:         5.0,
		DecayBeta:          5.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          5,
		CreatedAt:          now.Add(-24 * time.Hour),
		Traces:             []AccessTrace{},
	}

	// Add traces
	for i := 0; i < 10; i++ {
		m.Reinforce(now.Add(time.Duration(i)*time.Minute), AccessRetrieval, "")
	}

	// Should maintain max traces limit
	if len(m.Traces) != 5 {
		t.Errorf("expected 5 traces, got %d", len(m.Traces))
	}

	// Access count should be total, not limited
	if m.AccessCount != 10 {
		t.Errorf("expected access count 10, got %d", m.AccessCount)
	}

	// Most recent traces should be kept
	lastTrace := m.Traces[len(m.Traces)-1]
	expectedTime := now.Add(9 * time.Minute)
	if !lastTrace.AccessedAt.Equal(expectedTime) {
		t.Errorf("expected most recent trace to be kept")
	}
}

func TestACTRMemory_UpdateDecay(t *testing.T) {
	m := &ACTRMemory{
		DecayAlpha: 5.0,
		DecayBeta:  5.0,
	}

	initialMean := m.DecayMean()

	// Successful retrieval after long age → slower decay
	m.UpdateDecay(3600.0, true)
	newMeanSuccess := m.DecayMean()

	if newMeanSuccess >= initialMean {
		t.Errorf("successful retrieval should decrease decay mean")
	}

	// Reset
	m.DecayAlpha = 5.0
	m.DecayBeta = 5.0

	// Failed retrieval → faster decay
	m.UpdateDecay(3600.0, false)
	newMeanFailure := m.DecayMean()

	if newMeanFailure <= initialMean {
		t.Errorf("failed retrieval should increase decay mean")
	}
}

func TestACTRMemory_JSON(t *testing.T) {
	now := time.Now().UTC()

	m := &ACTRMemory{
		NodeID:             "test-node",
		Domain:             2,
		DecayAlpha:         3.0,
		DecayBeta:          7.0,
		BaseOffsetMean:     0.0,
		BaseOffsetVariance: 1.0,
		MaxTraces:          100,
		CreatedAt:          now,
		AccessCount:        5,
		Traces: []AccessTrace{
			{
				AccessedAt: now.Add(-1 * time.Hour),
				AccessType: AccessRetrieval,
				Context:    "context1",
			},
		},
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded ACTRMemory
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.NodeID != m.NodeID {
		t.Errorf("node ID mismatch")
	}
	if decoded.Domain != m.Domain {
		t.Errorf("domain mismatch")
	}
	if len(decoded.Traces) != len(m.Traces) {
		t.Errorf("traces count mismatch")
	}
}

// =============================================================================
// MD.2.3 DomainDecayParams Tests
// =============================================================================

func TestDefaultDomainDecay(t *testing.T) {
	tests := []struct {
		name         string
		domain       int
		expectedMean float64
		alpha        float64
		beta         float64
	}{
		{"academic", 2, 0.3, 3.0, 7.0},
		{"architect", 3, 0.4, 4.0, 6.0},
		{"engineer", 4, 0.6, 6.0, 4.0},
		{"standard", 0, 0.5, 5.0, 5.0},
		{"code", 1, 0.5, 5.0, 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := DefaultDomainDecay(tt.domain)

			if params.DecayAlpha != tt.alpha {
				t.Errorf("expected alpha %f, got %f", tt.alpha, params.DecayAlpha)
			}
			if params.DecayBeta != tt.beta {
				t.Errorf("expected beta %f, got %f", tt.beta, params.DecayBeta)
			}

			mean := params.Mean()
			if math.Abs(mean-tt.expectedMean) > 0.01 {
				t.Errorf("expected mean %f, got %f", tt.expectedMean, mean)
			}
		})
	}
}

func TestDomainDecayParams_Mean(t *testing.T) {
	params := DomainDecayParams{
		DecayAlpha: 3.0,
		DecayBeta:  7.0,
	}

	mean := params.Mean()
	expected := 0.3

	if math.Abs(mean-expected) > 0.01 {
		t.Errorf("expected %f, got %f", expected, mean)
	}
}

func TestDomainDecayParams_CognitiveExpectations(t *testing.T) {
	// Academic knowledge should decay slower than engineering tasks
	academic := DefaultDomainDecay(2)
	engineer := DefaultDomainDecay(4)

	if academic.Mean() >= engineer.Mean() {
		t.Errorf("academic decay (%f) should be slower than engineer (%f)",
			academic.Mean(), engineer.Mean())
	}

	// Architect should be between academic and engineer
	architect := DefaultDomainDecay(3)

	if architect.Mean() <= academic.Mean() || architect.Mean() >= engineer.Mean() {
		t.Errorf("architect decay (%f) should be between academic (%f) and engineer (%f)",
			architect.Mean(), academic.Mean(), engineer.Mean())
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestACTRMemory_Integration(t *testing.T) {
	now := time.Now()

	// Create memory with academic domain parameters
	params := DefaultDomainDecay(2)
	m := &ACTRMemory{
		NodeID:             "integration-test",
		Domain:             2,
		DecayAlpha:         params.DecayAlpha,
		DecayBeta:          params.DecayBeta,
		BaseOffsetMean:     params.BaseOffsetMean,
		BaseOffsetVariance: params.BaseOffsetVar,
		MaxTraces:          100,
		CreatedAt:          now.Add(-7 * 24 * time.Hour),
		Traces:             []AccessTrace{},
	}

	// Simulate access pattern over time
	for i := 0; i < 5; i++ {
		age := time.Duration(i*24) * time.Hour
		m.Reinforce(now.Add(-age), AccessRetrieval, "")
	}

	// Verify activation is reasonable
	activation := m.Activation(now)
	if math.IsInf(activation, 0) || math.IsNaN(activation) {
		t.Errorf("activation should be finite: %f", activation)
	}

	// Verify retrieval probability
	prob := m.RetrievalProbability(now, 0.0)
	if prob < 0 || prob > 1 {
		t.Errorf("probability should be in [0,1]: %f", prob)
	}

	// Test decay learning
	initialMean := m.DecayMean()
	m.UpdateDecay(3600.0, true)
	newMean := m.DecayMean()

	if newMean >= initialMean {
		t.Errorf("decay mean should decrease after successful retrieval")
	}
}

func TestGammaRand(t *testing.T) {
	// Test that gamma random samples are positive
	for alpha := 0.1; alpha <= 10.0; alpha += 0.5 {
		sample := gammaRand(alpha)
		if sample < 0 {
			t.Errorf("gamma sample should be positive for alpha=%f: got %f",
				alpha, sample)
		}
	}

	// Test that samples from Gamma(1,1) approximate exponential(1)
	samples := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		samples[i] = gammaRand(1.0)
	}

	sum := 0.0
	for _, s := range samples {
		sum += s
	}
	mean := sum / float64(len(samples))

	// Exponential(1) has mean 1.0
	if math.Abs(mean-1.0) > 0.2 {
		t.Errorf("gamma(1) samples should have mean ~1.0, got %f", mean)
	}
}
