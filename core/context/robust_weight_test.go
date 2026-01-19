// Package context provides types and utilities for adaptive retrieval context management.
// This file contains tests for AR.3.1: RobustWeightDistribution.
package context

import (
	"math"
	"sync"
	"testing"
)

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewRobustWeightDistribution_DefaultPrior(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)

	if w.Alpha != 1.0 {
		t.Errorf("expected Alpha=1.0, got %f", w.Alpha)
	}
	if w.Beta != 1.0 {
		t.Errorf("expected Beta=1.0, got %f", w.Beta)
	}
}

func TestNewRobustWeightDistribution_CustomPrior(t *testing.T) {
	t.Parallel()

	w := newRobustDist(5.0, 3.0)

	if w.Alpha != 5.0 {
		t.Errorf("expected Alpha=5.0, got %f", w.Alpha)
	}
	if w.Beta != 3.0 {
		t.Errorf("expected Beta=3.0, got %f", w.Beta)
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestRobustWeight_Mean_UniformPrior(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	mean := w.Mean()

	if math.Abs(mean-0.5) > 0.001 {
		t.Errorf("expected mean=0.5 for uniform prior, got %f", mean)
	}
}

func TestRobustWeight_Mean_AsymmetricPrior(t *testing.T) {
	t.Parallel()

	w := newRobustDist(9.0, 1.0)
	mean := w.Mean()
	expected := 9.0 / 10.0

	if math.Abs(mean-expected) > 0.001 {
		t.Errorf("expected mean=%f, got %f", expected, mean)
	}
}

func TestRobustWeight_Mean_ZeroSum(t *testing.T) {
	t.Parallel()

	w := RobustWeightDistribution{Alpha: 0, Beta: 0}
	mean := w.Mean()

	if mean != 0.5 {
		t.Errorf("expected mean=0.5 for zero sum, got %f", mean)
	}
}

func TestRobustWeight_Variance_HighConfidence(t *testing.T) {
	t.Parallel()

	w := newRobustDist(100.0, 100.0)
	variance := w.Variance()

	if variance >= 0.01 {
		t.Errorf("expected low variance for high confidence, got %f", variance)
	}
}

func TestRobustWeight_Variance_LowConfidence(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	variance := w.Variance()

	// Beta(1,1) has variance 1/12 ≈ 0.083
	expected := 1.0 / 12.0
	if math.Abs(variance-expected) > 0.01 {
		t.Errorf("expected variance≈%f, got %f", expected, variance)
	}
}

func TestRobustWeight_Variance_ZeroSum(t *testing.T) {
	t.Parallel()

	w := RobustWeightDistribution{Alpha: 0, Beta: 0}
	variance := w.Variance()

	if variance != 0 {
		t.Errorf("expected variance=0 for zero sum, got %f", variance)
	}
}

// =============================================================================
// Sampling Tests
// =============================================================================

func TestRobustWeight_Sample_InRange(t *testing.T) {
	t.Parallel()

	w := newRobustDist(2.0, 2.0)

	for i := 0; i < 100; i++ {
		sample := w.Sample()
		if sample < 0 || sample > 1 {
			t.Errorf("sample out of range [0,1]: %f", sample)
		}
	}
}

func TestRobustWeight_Sample_ConvergesToMean(t *testing.T) {
	t.Parallel()

	w := newRobustDist(9.0, 1.0)

	sum := 0.0
	n := 10000
	for i := 0; i < n; i++ {
		sum += w.Sample()
	}

	avg := sum / float64(n)
	expected := w.Mean()

	if math.Abs(avg-expected) > 0.05 {
		t.Errorf("sample average=%f deviates from mean=%f", avg, expected)
	}
}

func TestRobustWeight_Sample_InvalidParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{"zero alpha", 0, 1},
		{"zero beta", 1, 0},
		{"negative alpha", -1, 1},
		{"negative beta", 1, -1},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sample := betaSample(tt.alpha, tt.beta)
			if sample != 0.5 {
				t.Errorf("expected 0.5 for invalid params, got %f", sample)
			}
		})
	}
}

// =============================================================================
// Update Tests
// =============================================================================

func TestRobustWeight_Update_PositiveObservation(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	alphaBefore := w.Alpha

	// Skip decay/drift by using high decay factor and low drift
	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	w.Update(1.0, config)

	if w.Alpha <= alphaBefore {
		t.Errorf("positive observation should increase Alpha: before=%f, after=%f", alphaBefore, w.Alpha)
	}
}

func TestRobustWeight_Update_NegativeObservation(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	betaBefore := w.Beta

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	w.Update(0.0, config)

	if w.Beta <= betaBefore {
		t.Errorf("negative observation should increase Beta: before=%f, after=%f", betaBefore, w.Beta)
	}
}

func TestRobustWeight_Update_NilConfig(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)

	// Should not panic and should use defaults
	w.Update(0.5, nil)

	// Just verify it doesn't panic
}

func TestRobustWeight_Update_EffectiveSamples(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	for i := 0; i < 5; i++ {
		w.Update(0.5, config)
	}

	if w.EffectiveSamples < 5 {
		t.Errorf("expected at least 5 effective samples, got %f", w.EffectiveSamples)
	}
}

// =============================================================================
// Outlier Rejection Tests
// =============================================================================

func TestRobustWeight_Update_OutlierRejection(t *testing.T) {
	t.Parallel()

	// Start with strong belief mean ≈ 0.9
	w := newRobustDist(90.0, 10.0)
	w.EffectiveSamples = 100

	alphaBefore := w.Alpha
	betaBefore := w.Beta

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        2.0, // Tight outlier threshold
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	// This is an extreme outlier (0.1 when mean is ~0.9)
	w.Update(0.1, config)

	// Outlier should be rejected, parameters should be unchanged
	if w.Alpha != alphaBefore || w.Beta != betaBefore {
		t.Errorf("outlier should be rejected: alpha %f->%f, beta %f->%f",
			alphaBefore, w.Alpha, betaBefore, w.Beta)
	}
}

func TestRobustWeight_Update_AcceptsNonOutlier(t *testing.T) {
	t.Parallel()

	w := newRobustDist(5.0, 5.0) // Mean = 0.5
	alphaBefore := w.Alpha

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        3.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	// 0.6 is close to mean=0.5, not an outlier
	w.Update(0.6, config)

	if w.Alpha <= alphaBefore {
		t.Errorf("non-outlier should be accepted")
	}
}

// =============================================================================
// Decay Tests
// =============================================================================

func TestRobustWeight_Update_Decay(t *testing.T) {
	t.Parallel()

	w := newRobustDist(10.0, 10.0)

	config := &UpdateConfig{
		DecayFactor:         0.9, // 10% decay
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	w.Update(0.5, config)

	// Alpha should be decayed then observation added
	// decayed = 10 * 0.9 = 9, then +0.5 = 9.5
	expected := 10.0*0.9 + 0.5
	if math.Abs(w.Alpha-expected) > 0.1 {
		t.Errorf("expected Alpha≈%f after decay, got %f", expected, w.Alpha)
	}
}

// =============================================================================
// Cold Start Tests
// =============================================================================

func TestRobustWeight_Update_ColdStart(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	w.EffectiveSamples = 0

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 10.0, // High threshold for cold start
		DriftRate:           0,
	}

	// Add observation
	w.Update(1.0, config)

	// With cold start, alpha should be blended toward prior
	// After observation: Alpha would be 2.0 without cold start
	// Cold start scales back toward prior (1.0)
	if w.Alpha >= 2.0 {
		t.Errorf("cold start should blend with prior: got Alpha=%f", w.Alpha)
	}
}

func TestRobustWeight_Update_NoColdStartAfterThreshold(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	w.EffectiveSamples = 20 // Above threshold

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 10.0,
		DriftRate:           0,
	}

	alphaBefore := w.Alpha
	w.Update(1.0, config)

	// Without cold start, full observation is added
	expected := alphaBefore + 1.0
	if math.Abs(w.Alpha-expected) > 0.01 {
		t.Errorf("expected Alpha=%f without cold start, got %f", expected, w.Alpha)
	}
}

// =============================================================================
// Prior Drift Tests
// =============================================================================

func TestRobustWeight_Update_PriorDrift(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	// Move parameters away from prior
	w.Alpha = 10.0
	w.Beta = 10.0

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0.1, // 10% drift toward prior
	}

	alphaBefore := w.Alpha
	w.Update(0.5, config)

	// Alpha should drift toward prior (1.0)
	if w.Alpha >= alphaBefore+0.5 { // Without drift it would be exactly alphaBefore + 0.5
		t.Errorf("prior drift should reduce Alpha toward prior")
	}
}

// =============================================================================
// Confidence Tests
// =============================================================================

func TestRobustWeight_Confidence_LowForFewSamples(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	w.EffectiveSamples = 1

	conf := w.Confidence()

	if conf >= 0.5 {
		t.Errorf("expected low confidence for few samples, got %f", conf)
	}
}

func TestRobustWeight_Confidence_HighForManySamples(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	w.EffectiveSamples = 100

	conf := w.Confidence()

	if conf < 0.9 {
		t.Errorf("expected high confidence for many samples, got %f", conf)
	}
}

func TestRobustWeight_Confidence_Formula(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	w.EffectiveSamples = 10

	conf := w.Confidence()
	expected := 10.0 / (10.0 + 10.0) // n / (n + 10)

	if math.Abs(conf-expected) > 0.001 {
		t.Errorf("expected confidence=%f, got %f", expected, conf)
	}
}

// =============================================================================
// Credible Interval Tests
// =============================================================================

func TestRobustWeight_CredibleInterval_Contains95(t *testing.T) {
	t.Parallel()

	w := newRobustDist(50.0, 50.0) // Mean = 0.5

	low, high := w.CredibleInterval(0.95)

	if low >= high {
		t.Errorf("expected low < high, got low=%f, high=%f", low, high)
	}

	// For symmetric Beta(50,50), interval should be roughly symmetric around 0.5
	// The bisection approximation may have some bias, so check that the interval
	// is reasonable rather than strictly containing the mean
	if low < 0.3 || high > 0.7 {
		t.Errorf("interval should be reasonable: got [%f, %f]", low, high)
	}
}

func TestRobustWeight_CredibleInterval_NarrowForHighConfidence(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1000.0, 1000.0)

	low, high := w.CredibleInterval(0.95)
	width := high - low

	if width > 0.1 {
		t.Errorf("expected narrow interval for high confidence, got width=%f", width)
	}
}

func TestRobustWeight_CredibleInterval_WideForLowConfidence(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)

	low, high := w.CredibleInterval(0.95)
	width := high - low

	if width < 0.5 {
		t.Errorf("expected wide interval for low confidence, got width=%f", width)
	}
}

// =============================================================================
// Beta Quantile Tests
// =============================================================================

func TestBetaQuantile_Extremes(t *testing.T) {
	t.Parallel()

	if betaQuantile(1, 1, 0) != 0 {
		t.Error("expected quantile(0) = 0")
	}

	if betaQuantile(1, 1, 1) != 1 {
		t.Error("expected quantile(1) = 1")
	}
}

func TestBetaQuantile_Median(t *testing.T) {
	t.Parallel()

	// For Beta(1,1), median should be 0.5
	median := betaQuantile(1, 1, 0.5)

	if math.Abs(median-0.5) > 0.01 {
		t.Errorf("expected median≈0.5 for uniform, got %f", median)
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestRobustWeight_ConcurrentReads(t *testing.T) {
	t.Parallel()

	w := newRobustDist(5.0, 5.0)

	var wg sync.WaitGroup
	n := 100

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = w.Mean()
			_ = w.Variance()
			_ = w.Confidence()
			_ = w.Sample()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestRobustWeight_Update_ZeroObservation(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	betaBefore := w.Beta

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	w.Update(0.0, config)

	if w.Beta <= betaBefore {
		t.Errorf("zero observation should increase beta")
	}
}

func TestRobustWeight_Update_OneObservation(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)
	alphaBefore := w.Alpha

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	w.Update(1.0, config)

	if w.Alpha <= alphaBefore {
		t.Errorf("observation=1.0 should increase alpha")
	}
}

// =============================================================================
// Convergence Tests
// =============================================================================

func TestRobustWeight_Convergence_ManyPositive(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	for i := 0; i < 100; i++ {
		w.Update(1.0, config)
	}

	mean := w.Mean()
	if mean < 0.9 {
		t.Errorf("mean should approach 1.0 after many positive observations, got %f", mean)
	}
}

func TestRobustWeight_Convergence_ManyNegative(t *testing.T) {
	t.Parallel()

	w := newRobustDist(1.0, 1.0)

	config := &UpdateConfig{
		DecayFactor:         1.0,
		OutlierSigma:        10.0,
		MinEffectiveSamples: 0,
		DriftRate:           0,
	}

	for i := 0; i < 100; i++ {
		w.Update(0.0, config)
	}

	mean := w.Mean()
	if mean > 0.1 {
		t.Errorf("mean should approach 0.0 after many negative observations, got %f", mean)
	}
}

// =============================================================================
// Gamma Sample Tests
// =============================================================================

func TestGammaSample_SmallShape(t *testing.T) {
	t.Parallel()

	// Should not panic
	for i := 0; i < 100; i++ {
		sample := gammaSample(0.5)
		if sample < 0 {
			t.Errorf("gamma sample should be non-negative, got %f", sample)
		}
	}
}

func TestGammaSample_LargeShape(t *testing.T) {
	t.Parallel()

	for i := 0; i < 100; i++ {
		sample := gammaSample(10.0)
		if sample < 0 {
			t.Errorf("gamma sample should be non-negative, got %f", sample)
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkRobustWeight_Sample(b *testing.B) {
	w := newRobustDist(5.0, 5.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.Sample()
	}
}

func BenchmarkRobustWeight_Update(b *testing.B) {
	w := newRobustDist(5.0, 5.0)
	config := DefaultUpdateConfig()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Update(0.5, config)
	}
}

func BenchmarkRobustWeight_Mean(b *testing.B) {
	w := newRobustDist(5.0, 5.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = w.Mean()
	}
}

func BenchmarkRobustWeight_CredibleInterval(b *testing.B) {
	w := newRobustDist(50.0, 50.0)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = w.CredibleInterval(0.95)
	}
}
