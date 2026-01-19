package handoff

import (
	"encoding/json"
	"math"
	"testing"
)

// =============================================================================
// Test Mean and Variance Calculations
// =============================================================================

func TestLearnedWeight_Mean(t *testing.T) {
	tests := []struct {
		name     string
		alpha    float64
		beta     float64
		expected float64
	}{
		{
			name:     "uniform prior",
			alpha:    1.0,
			beta:     1.0,
			expected: 0.5,
		},
		{
			name:     "biased toward 1",
			alpha:    9.0,
			beta:     1.0,
			expected: 0.9,
		},
		{
			name:     "biased toward 0",
			alpha:    1.0,
			beta:     9.0,
			expected: 0.1,
		},
		{
			name:     "equal parameters",
			alpha:    5.0,
			beta:     5.0,
			expected: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lw := NewLearnedWeight(tt.alpha, tt.beta)
			mean := lw.Mean()

			if math.Abs(mean-tt.expected) > 1e-9 {
				t.Errorf("Mean() = %v, expected %v", mean, tt.expected)
			}

			if math.IsNaN(mean) || math.IsInf(mean, 0) {
				t.Errorf("Mean() returned invalid value: %v", mean)
			}
		})
	}
}

func TestLearnedWeight_Variance(t *testing.T) {
	tests := []struct {
		name        string
		alpha       float64
		beta        float64
		expectLower bool
	}{
		{
			name:        "low confidence",
			alpha:       1.0,
			beta:        1.0,
			expectLower: false,
		},
		{
			name:        "high confidence",
			alpha:       100.0,
			beta:        100.0,
			expectLower: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lw := NewLearnedWeight(tt.alpha, tt.beta)
			variance := lw.Variance()

			if variance < 0 {
				t.Errorf("Variance() = %v, must be non-negative", variance)
			}

			if math.IsNaN(variance) || math.IsInf(variance, 0) {
				t.Errorf("Variance() returned invalid value: %v", variance)
			}

			if tt.expectLower && variance > 0.01 {
				t.Errorf("Expected low variance, got %v", variance)
			}
		})
	}
}

func TestLearnedWeight_Confidence(t *testing.T) {
	tests := []struct {
		name            string
		alpha           float64
		beta            float64
		expectHighConf  bool
	}{
		{
			name:           "low confidence",
			alpha:          1.0,
			beta:           1.0,
			expectHighConf: false,
		},
		{
			name:           "high confidence",
			alpha:          50.0,
			beta:           50.0,
			expectHighConf: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lw := NewLearnedWeight(tt.alpha, tt.beta)
			conf := lw.Confidence()

			if conf < 0.0 || conf > 1.0 {
				t.Errorf("Confidence() = %v, must be in [0,1]", conf)
			}

			if math.IsNaN(conf) || math.IsInf(conf, 0) {
				t.Errorf("Confidence() returned invalid value: %v", conf)
			}

			if tt.expectHighConf && conf < 0.9 {
				t.Errorf("Expected high confidence, got %v", conf)
			}

			if !tt.expectHighConf && conf > 0.5 {
				t.Errorf("Expected low confidence, got %v", conf)
			}
		})
	}
}

func TestLearnedWeight_CredibleInterval(t *testing.T) {
	lw := NewLearnedWeight(10.0, 10.0)

	lower, upper := lw.CredibleInterval(0.95)

	if lower < 0.0 || lower > 1.0 {
		t.Errorf("Lower bound %v out of range [0,1]", lower)
	}

	if upper < 0.0 || upper > 1.0 {
		t.Errorf("Upper bound %v out of range [0,1]", upper)
	}

	if lower > upper {
		t.Errorf("Lower bound %v > upper bound %v", lower, upper)
	}

	mean := lw.Mean()
	if mean < lower || mean > upper {
		t.Errorf("Mean %v not in credible interval [%v, %v]", mean, lower, upper)
	}

	if math.IsNaN(lower) || math.IsInf(lower, 0) {
		t.Errorf("Lower bound invalid: %v", lower)
	}

	if math.IsNaN(upper) || math.IsInf(upper, 0) {
		t.Errorf("Upper bound invalid: %v", upper)
	}
}

// =============================================================================
// Test Update Converges
// =============================================================================

func TestLearnedWeight_UpdateConverges(t *testing.T) {
	lw := NewLearnedWeight(1.0, 1.0)
	config := DefaultUpdateConfig()

	target := 0.7
	for i := 0; i < 1000; i++ {
		lw.Update(target, config)
	}

	mean := lw.Mean()
	if math.Abs(mean-target) > 0.1 {
		t.Errorf("After 1000 updates with value %v, mean = %v (diff = %v)",
			target, mean, math.Abs(mean-target))
	}

	if math.IsNaN(mean) || math.IsInf(mean, 0) {
		t.Errorf("Mean converged to invalid value: %v", mean)
	}
}

func TestLearnedWeight_UpdateWithAlternatingValues(t *testing.T) {
	lw := NewLearnedWeight(1.0, 1.0)
	config := DefaultUpdateConfig()

	for i := 0; i < 500; i++ {
		lw.Update(1.0, config)
		lw.Update(0.0, config)
	}

	mean := lw.Mean()
	if math.Abs(mean-0.5) > 0.15 {
		t.Errorf("After alternating updates, mean = %v, expected ~0.5", mean)
	}

	if math.IsNaN(mean) || math.IsInf(mean, 0) {
		t.Errorf("Mean is invalid: %v", mean)
	}
}

func TestLearnedWeight_UpdateDriftProtection(t *testing.T) {
	lw := NewLearnedWeight(1.0, 1.0)
	config := &UpdateConfig{
		LearningRate:   0.2,
		DriftThreshold: 0.9,
		PriorBlendRate: 0.3,
	}

	for i := 0; i < 100; i++ {
		lw.Update(1.0, config)
	}

	alpha := lw.Alpha
	beta := lw.Beta

	if math.IsNaN(alpha) || math.IsInf(alpha, 0) {
		t.Errorf("Alpha became invalid: %v", alpha)
	}

	if math.IsNaN(beta) || math.IsInf(beta, 0) {
		t.Errorf("Beta became invalid: %v", beta)
	}

	mean := lw.Mean()
	if mean < 0.0 || mean > 1.0 {
		t.Errorf("Mean %v out of range [0,1]", mean)
	}
}

// =============================================================================
// Test No NaN/Inf Values
// =============================================================================

func TestLearnedWeight_NoInvalidValues(t *testing.T) {
	testCases := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{"small values", 0.1, 0.1},
		{"equal values", 5.0, 5.0},
		{"large values", 1000.0, 1000.0},
		{"unequal values", 2.0, 8.0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lw := NewLearnedWeight(tc.alpha, tc.beta)

			mean := lw.Mean()
			assertValid(t, "Mean", mean)

			variance := lw.Variance()
			assertValid(t, "Variance", variance)

			conf := lw.Confidence()
			assertValid(t, "Confidence", conf)

			lower, upper := lw.CredibleInterval(0.95)
			assertValid(t, "CredibleInterval lower", lower)
			assertValid(t, "CredibleInterval upper", upper)

			sample := lw.Sample()
			assertValid(t, "Sample", sample)
		})
	}
}

func TestLearnedWeight_NoInvalidValuesAfterUpdates(t *testing.T) {
	lw := NewLearnedWeight(1.0, 1.0)
	config := DefaultUpdateConfig()

	observations := []float64{0.0, 0.25, 0.5, 0.75, 1.0}

	for i := 0; i < 200; i++ {
		for _, obs := range observations {
			lw.Update(obs, config)

			assertValid(t, "Alpha", lw.Alpha)
			assertValid(t, "Beta", lw.Beta)
			assertValid(t, "EffectiveSamples", lw.EffectiveSamples)
			assertValid(t, "Mean", lw.Mean())
			assertValid(t, "Variance", lw.Variance())
		}
	}
}

func TestLearnedWeight_EdgeCases(t *testing.T) {
	lw := NewLearnedWeight(1.0, 1.0)
	config := DefaultUpdateConfig()

	lw.Update(0.0, config)
	assertAllValid(t, lw)

	lw.Update(1.0, config)
	assertAllValid(t, lw)

	lw.Update(0.5, config)
	assertAllValid(t, lw)

	lw.Update(-1.0, config)
	assertAllValid(t, lw)

	lw.Update(2.0, config)
	assertAllValid(t, lw)
}

// =============================================================================
// Test Sampling
// =============================================================================

func TestLearnedWeight_Sample(t *testing.T) {
	lw := NewLearnedWeight(5.0, 5.0)

	samples := make([]float64, 1000)
	for i := 0; i < 1000; i++ {
		sample := lw.Sample()

		if sample < 0.0 || sample > 1.0 {
			t.Errorf("Sample %d out of range: %v", i, sample)
		}

		if math.IsNaN(sample) || math.IsInf(sample, 0) {
			t.Errorf("Sample %d invalid: %v", i, sample)
		}

		samples[i] = sample
	}

	sampleMean := 0.0
	for _, s := range samples {
		sampleMean += s
	}
	sampleMean /= float64(len(samples))

	expectedMean := lw.Mean()
	if math.Abs(sampleMean-expectedMean) > 0.1 {
		t.Errorf("Sample mean %v differs from expected %v by more than 0.1",
			sampleMean, expectedMean)
	}
}

// =============================================================================
// Test JSON Marshaling
// =============================================================================

func TestLearnedWeight_JSONMarshal(t *testing.T) {
	original := NewLearnedWeight(5.0, 3.0)
	original.Update(0.8, DefaultUpdateConfig())

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded LearnedWeight
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if math.Abs(decoded.Alpha-original.Alpha) > 1e-9 {
		t.Errorf("Alpha mismatch: got %v, want %v", decoded.Alpha, original.Alpha)
	}

	if math.Abs(decoded.Beta-original.Beta) > 1e-9 {
		t.Errorf("Beta mismatch: got %v, want %v", decoded.Beta, original.Beta)
	}

	if math.Abs(decoded.EffectiveSamples-original.EffectiveSamples) > 1e-9 {
		t.Errorf("EffectiveSamples mismatch: got %v, want %v",
			decoded.EffectiveSamples, original.EffectiveSamples)
	}

	if math.Abs(decoded.PriorAlpha-original.PriorAlpha) > 1e-9 {
		t.Errorf("PriorAlpha mismatch: got %v, want %v",
			decoded.PriorAlpha, original.PriorAlpha)
	}

	if math.Abs(decoded.PriorBeta-original.PriorBeta) > 1e-9 {
		t.Errorf("PriorBeta mismatch: got %v, want %v",
			decoded.PriorBeta, original.PriorBeta)
	}
}

// =============================================================================
// Test UpdateConfig
// =============================================================================

func TestDefaultUpdateConfig(t *testing.T) {
	config := DefaultUpdateConfig()

	if config.LearningRate <= 0.0 || config.LearningRate >= 1.0 {
		t.Errorf("LearningRate %v not in (0,1)", config.LearningRate)
	}

	if config.DriftThreshold <= 0.0 || config.DriftThreshold > 1.0 {
		t.Errorf("DriftThreshold %v not in (0,1]", config.DriftThreshold)
	}

	if config.PriorBlendRate <= 0.0 || config.PriorBlendRate >= 1.0 {
		t.Errorf("PriorBlendRate %v not in (0,1)", config.PriorBlendRate)
	}
}

func TestLearnedWeight_UpdateWithNilConfig(t *testing.T) {
	lw := NewLearnedWeight(1.0, 1.0)

	lw.Update(0.7, nil)

	mean := lw.Mean()
	if math.IsNaN(mean) || math.IsInf(mean, 0) {
		t.Errorf("Update with nil config produced invalid mean: %v", mean)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func assertValid(t *testing.T, name string, value float64) {
	t.Helper()
	if math.IsNaN(value) {
		t.Errorf("%s is NaN", name)
	}
	if math.IsInf(value, 0) {
		t.Errorf("%s is Inf", name)
	}
}

func assertAllValid(t *testing.T, lw *LearnedWeight) {
	t.Helper()
	assertValid(t, "Alpha", lw.Alpha)
	assertValid(t, "Beta", lw.Beta)
	assertValid(t, "EffectiveSamples", lw.EffectiveSamples)
	assertValid(t, "Mean", lw.Mean())
	assertValid(t, "Variance", lw.Variance())
	assertValid(t, "Confidence", lw.Confidence())
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkLearnedWeight_Mean(b *testing.B) {
	lw := NewLearnedWeight(5.0, 3.0)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = lw.Mean()
	}
}

func BenchmarkLearnedWeight_Update(b *testing.B) {
	lw := NewLearnedWeight(1.0, 1.0)
	config := DefaultUpdateConfig()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lw.Update(0.5, config)
	}
}

func BenchmarkLearnedWeight_Sample(b *testing.B) {
	lw := NewLearnedWeight(5.0, 5.0)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = lw.Sample()
	}
}

// =============================================================================
// Test LearnedContextSize
// =============================================================================

func TestLearnedContextSize_Mean(t *testing.T) {
	tests := []struct {
		name     string
		alpha    float64
		beta     float64
		expected int
	}{
		{
			name:     "mean of 100",
			alpha:    100.0,
			beta:     1.0,
			expected: 100,
		},
		{
			name:     "mean of 50",
			alpha:    50.0,
			beta:     1.0,
			expected: 50,
		},
		{
			name:     "mean of 200",
			alpha:    200.0,
			beta:     2.0,
			expected: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lcs := NewLearnedContextSize(tt.alpha, tt.beta)
			mean := lcs.Mean()

			if mean != tt.expected {
				t.Errorf("Mean() = %v, expected %v", mean, tt.expected)
			}

			if mean < 1 {
				t.Errorf("Mean() = %v, must be >= 1", mean)
			}
		})
	}
}

func TestLearnedContextSize_Sample(t *testing.T) {
	lcs := NewLearnedContextSize(100.0, 1.0)

	for i := 0; i < 100; i++ {
		sample := lcs.Sample()

		if sample < 1 {
			t.Errorf("Sample %d = %v, must be >= 1", i, sample)
		}

		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			t.Errorf("Sample %d invalid: %v", i, sample)
		}
	}
}

func TestLearnedContextSize_UpdateConverges(t *testing.T) {
	lcs := NewLearnedContextSize(50.0, 1.0)
	config := &UpdateConfig{
		LearningRate:   0.2,
		DriftThreshold: 0.99,
		PriorBlendRate: 0.1,
	}

	target := 120
	for i := 0; i < 2000; i++ {
		lcs.Update(target, config)
	}

	mean := lcs.Mean()
	diff := math.Abs(float64(mean - target))

	if diff > float64(target)*0.3 {
		t.Errorf("After 2000 updates with value %v, mean = %v (diff = %v)",
			target, mean, diff)
	}

	assertValid(t, "Alpha", lcs.Alpha)
	assertValid(t, "Beta", lcs.Beta)
}

func TestLearnedContextSize_Confidence(t *testing.T) {
	tests := []struct {
		name           string
		alpha          float64
		beta           float64
		expectHighConf bool
	}{
		{
			name:           "low confidence",
			alpha:          1.0,
			beta:           1.0,
			expectHighConf: false,
		},
		{
			name:           "high confidence",
			alpha:          100.0,
			beta:           1.0,
			expectHighConf: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lcs := NewLearnedContextSize(tt.alpha, tt.beta)
			conf := lcs.Confidence()

			if conf < 0.0 || conf > 1.0 {
				t.Errorf("Confidence() = %v, must be in [0,1]", conf)
			}

			assertValid(t, "Confidence", conf)

			if tt.expectHighConf && conf < 0.9 {
				t.Errorf("Expected high confidence, got %v", conf)
			}

			if !tt.expectHighConf && conf > 0.5 {
				t.Errorf("Expected low confidence, got %v", conf)
			}
		})
	}
}

func TestLearnedContextSize_NoInvalidValues(t *testing.T) {
	lcs := NewLearnedContextSize(100.0, 1.0)
	config := DefaultUpdateConfig()

	observations := []int{50, 100, 150, 200}

	for i := 0; i < 100; i++ {
		for _, obs := range observations {
			lcs.Update(obs, config)

			assertValid(t, "Alpha", lcs.Alpha)
			assertValid(t, "Beta", lcs.Beta)
			assertValid(t, "EffectiveSamples", lcs.EffectiveSamples)

			mean := lcs.Mean()
			if mean < 1 {
				t.Errorf("Mean %v must be >= 1", mean)
			}
		}
	}
}

func TestLearnedContextSize_JSONMarshal(t *testing.T) {
	original := NewLearnedContextSize(100.0, 2.0)
	original.Update(120, DefaultUpdateConfig())

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded LearnedContextSize
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if math.Abs(decoded.Alpha-original.Alpha) > 1e-9 {
		t.Errorf("Alpha mismatch: got %v, want %v", decoded.Alpha, original.Alpha)
	}

	if math.Abs(decoded.Beta-original.Beta) > 1e-9 {
		t.Errorf("Beta mismatch: got %v, want %v", decoded.Beta, original.Beta)
	}
}

// =============================================================================
// Test LearnedCount
// =============================================================================

func TestLearnedCount_Mean(t *testing.T) {
	tests := []struct {
		name     string
		alpha    float64
		beta     float64
		expected int
	}{
		{
			name:     "mean of 5",
			alpha:    5.0,
			beta:     1.0,
			expected: 5,
		},
		{
			name:     "mean of 10",
			alpha:    10.0,
			beta:     1.0,
			expected: 10,
		},
		{
			name:     "mean of 3",
			alpha:    6.0,
			beta:     2.0,
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := NewLearnedCount(tt.alpha, tt.beta)
			mean := lc.Mean()

			if mean != tt.expected {
				t.Errorf("Mean() = %v, expected %v", mean, tt.expected)
			}

			if mean < 0 {
				t.Errorf("Mean() = %v, must be >= 0", mean)
			}
		})
	}
}

func TestLearnedCount_Sample(t *testing.T) {
	lc := NewLearnedCount(5.0, 1.0)

	samples := make([]int, 1000)
	for i := 0; i < 1000; i++ {
		sample := lc.Sample()

		if sample < 0 {
			t.Errorf("Sample %d = %v, must be >= 0", i, sample)
		}

		samples[i] = sample
	}

	sampleMean := 0.0
	for _, s := range samples {
		sampleMean += float64(s)
	}
	sampleMean /= float64(len(samples))

	expectedMean := float64(lc.Mean())
	if math.Abs(sampleMean-expectedMean) > expectedMean*0.3 {
		t.Errorf("Sample mean %v differs from expected %v by more than 30%%",
			sampleMean, expectedMean)
	}
}

func TestLearnedCount_UpdateConverges(t *testing.T) {
	lc := NewLearnedCount(5.0, 1.0)
	config := DefaultUpdateConfig()

	target := 12
	for i := 0; i < 1000; i++ {
		lc.Update(target, config)
	}

	mean := lc.Mean()
	diff := math.Abs(float64(mean - target))

	if diff > float64(target)*0.2 {
		t.Errorf("After 1000 updates with value %v, mean = %v (diff = %v)",
			target, mean, diff)
	}

	assertValid(t, "Alpha", lc.Alpha)
	assertValid(t, "Beta", lc.Beta)
}

func TestLearnedCount_Confidence(t *testing.T) {
	tests := []struct {
		name           string
		alpha          float64
		beta           float64
		minConf        float64
		maxConf        float64
	}{
		{
			name:    "low confidence",
			alpha:   1.0,
			beta:    1.0,
			minConf: 0.0,
			maxConf: 0.5,
		},
		{
			name:    "high confidence",
			alpha:   500.0,
			beta:    10.0,
			minConf: 0.9,
			maxConf: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lc := NewLearnedCount(tt.alpha, tt.beta)
			conf := lc.Confidence()

			if conf < 0.0 || conf > 1.0 {
				t.Errorf("Confidence() = %v, must be in [0,1]", conf)
			}

			assertValid(t, "Confidence", conf)

			if conf < tt.minConf || conf > tt.maxConf {
				t.Errorf("Confidence() = %v, expected in range [%v, %v]",
					conf, tt.minConf, tt.maxConf)
			}
		})
	}
}

func TestLearnedCount_NoInvalidValues(t *testing.T) {
	lc := NewLearnedCount(5.0, 1.0)
	config := DefaultUpdateConfig()

	observations := []int{0, 5, 10, 15}

	for i := 0; i < 100; i++ {
		for _, obs := range observations {
			lc.Update(obs, config)

			assertValid(t, "Alpha", lc.Alpha)
			assertValid(t, "Beta", lc.Beta)
			assertValid(t, "EffectiveSamples", lc.EffectiveSamples)

			mean := lc.Mean()
			if mean < 0 {
				t.Errorf("Mean %v must be >= 0", mean)
			}
		}
	}
}

func TestLearnedCount_JSONMarshal(t *testing.T) {
	original := NewLearnedCount(10.0, 2.0)
	original.Update(7, DefaultUpdateConfig())

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded LearnedCount
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if math.Abs(decoded.Alpha-original.Alpha) > 1e-9 {
		t.Errorf("Alpha mismatch: got %v, want %v", decoded.Alpha, original.Alpha)
	}

	if math.Abs(decoded.Beta-original.Beta) > 1e-9 {
		t.Errorf("Beta mismatch: got %v, want %v", decoded.Beta, original.Beta)
	}
}

func TestLearnedCount_ZeroCount(t *testing.T) {
	lc := NewLearnedCount(1.0, 1.0)
	config := DefaultUpdateConfig()

	for i := 0; i < 100; i++ {
		lc.Update(0, config)
	}

	mean := lc.Mean()
	if mean < 0 {
		t.Errorf("Mean %v must be >= 0", mean)
	}

	assertValid(t, "Alpha", lc.Alpha)
	assertValid(t, "Beta", lc.Beta)
}

// =============================================================================
// Test Poisson Sampling Helper
// =============================================================================

func TestSamplePoisson_SmallLambda(t *testing.T) {
	lambda := 5.0
	samples := make([]int, 1000)

	for i := 0; i < 1000; i++ {
		sample := samplePoisson(lambda)
		if sample < 0 {
			t.Errorf("Sample %d = %v, must be >= 0", i, sample)
		}
		samples[i] = sample
	}

	sampleMean := 0.0
	for _, s := range samples {
		sampleMean += float64(s)
	}
	sampleMean /= float64(len(samples))

	if math.Abs(sampleMean-lambda) > lambda*0.2 {
		t.Errorf("Sample mean %v differs from lambda %v by more than 20%%",
			sampleMean, lambda)
	}
}

func TestSamplePoisson_LargeLambda(t *testing.T) {
	lambda := 50.0
	samples := make([]int, 1000)

	for i := 0; i < 1000; i++ {
		sample := samplePoisson(lambda)
		if sample < 0 {
			t.Errorf("Sample %d = %v, must be >= 0", i, sample)
		}
		samples[i] = sample
	}

	sampleMean := 0.0
	for _, s := range samples {
		sampleMean += float64(s)
	}
	sampleMean /= float64(len(samples))

	if math.Abs(sampleMean-lambda) > lambda*0.15 {
		t.Errorf("Sample mean %v differs from lambda %v by more than 15%%",
			sampleMean, lambda)
	}
}

func TestSamplePoisson_ZeroLambda(t *testing.T) {
	sample := samplePoisson(0.0)
	if sample != 0 {
		t.Errorf("samplePoisson(0) = %v, expected 0", sample)
	}
}

// =============================================================================
// Benchmarks for New Types
// =============================================================================

func BenchmarkLearnedContextSize_Mean(b *testing.B) {
	lcs := NewLearnedContextSize(100.0, 1.0)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = lcs.Mean()
	}
}

func BenchmarkLearnedContextSize_Update(b *testing.B) {
	lcs := NewLearnedContextSize(100.0, 1.0)
	config := DefaultUpdateConfig()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lcs.Update(120, config)
	}
}

func BenchmarkLearnedContextSize_Sample(b *testing.B) {
	lcs := NewLearnedContextSize(100.0, 1.0)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = lcs.Sample()
	}
}

func BenchmarkLearnedCount_Mean(b *testing.B) {
	lc := NewLearnedCount(10.0, 1.0)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = lc.Mean()
	}
}

func BenchmarkLearnedCount_Update(b *testing.B) {
	lc := NewLearnedCount(10.0, 1.0)
	config := DefaultUpdateConfig()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lc.Update(12, config)
	}
}

func BenchmarkLearnedCount_Sample(b *testing.B) {
	lc := NewLearnedCount(10.0, 1.0)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = lc.Sample()
	}
}
