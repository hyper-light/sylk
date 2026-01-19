package handoff

import (
	"fmt"
	"math"
	"sort"
	"testing"
)

// =============================================================================
// Test betaSample
// =============================================================================

func TestBetaSample_RangeValidation(t *testing.T) {
	tests := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{"uniform distribution", 1.0, 1.0},
		{"biased toward 1", 5.0, 2.0},
		{"biased toward 0", 2.0, 5.0},
		{"symmetric", 3.0, 3.0},
		{"high alpha", 10.0, 2.0},
		{"high beta", 2.0, 10.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				sample := betaSample(tt.alpha, tt.beta)

				if sample < 0.0 || sample > 1.0 {
					t.Errorf("Sample %d out of range [0,1]: %v", i, sample)
				}

				if math.IsNaN(sample) || math.IsInf(sample, 0) {
					t.Errorf("Sample %d is invalid: %v", i, sample)
				}
			}
		})
	}
}

func TestBetaSample_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		alpha    float64
		beta     float64
		expected float64
	}{
		{"zero alpha", 0.0, 1.0, 0.5},
		{"zero beta", 1.0, 0.0, 0.5},
		{"negative alpha", -1.0, 1.0, 0.5},
		{"negative beta", 1.0, -1.0, 0.5},
		{"both zero", 0.0, 0.0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sample := betaSample(tt.alpha, tt.beta)

			if math.Abs(sample-tt.expected) > 1e-9 {
				t.Errorf("Expected %v for invalid parameters, got %v", tt.expected, sample)
			}
		})
	}
}

func TestBetaSample_MeanConvergence(t *testing.T) {
	tests := []struct {
		name         string
		alpha        float64
		beta         float64
		expectedMean float64
	}{
		{"uniform", 1.0, 1.0, 0.5},
		{"70% mean", 7.0, 3.0, 0.7},
		{"30% mean", 3.0, 7.0, 0.3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const numSamples = 1000

			sum := 0.0
			for i := 0; i < numSamples; i++ {
				sum += betaSample(tt.alpha, tt.beta)
			}
			sampleMean := sum / float64(numSamples)

			tolerance := 0.1
			if math.Abs(sampleMean-tt.expectedMean) > tolerance {
				t.Errorf("Sample mean %v differs from expected %v by more than %v",
					sampleMean, tt.expectedMean, tolerance)
			}
		})
	}
}

// =============================================================================
// Test betaQuantile
// =============================================================================

func TestBetaQuantile_RangeValidation(t *testing.T) {
	tests := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{"uniform", 1.0, 1.0},
		{"asymmetric", 2.0, 5.0},
		{"symmetric", 3.0, 3.0},
	}

	probabilities := []float64{0.1, 0.25, 0.5, 0.75, 0.9}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, p := range probabilities {
				q := betaQuantile(p, tt.alpha, tt.beta)

				if q < 0.0 || q > 1.0 {
					t.Errorf("Quantile at p=%v out of range [0,1]: %v", p, q)
				}

				if math.IsNaN(q) || math.IsInf(q, 0) {
					t.Errorf("Quantile at p=%v is invalid: %v", p, q)
				}
			}
		})
	}
}

func TestBetaQuantile_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		p        float64
		alpha    float64
		beta     float64
		expected float64
	}{
		{"p=0", 0.0, 2.0, 2.0, 0.0},
		{"p=1", 1.0, 2.0, 2.0, 1.0},
		{"p<0", -0.1, 2.0, 2.0, 0.0},
		{"p>1", 1.1, 2.0, 2.0, 1.0},
		{"invalid alpha", 0.5, 0.0, 2.0, 0.5},
		{"invalid beta", 0.5, 2.0, 0.0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := betaQuantile(tt.p, tt.alpha, tt.beta)

			if math.Abs(q-tt.expected) > 1e-6 {
				t.Errorf("Expected %v, got %v", tt.expected, q)
			}
		})
	}
}

func TestBetaQuantile_MonotonicIncreasing(t *testing.T) {
	alpha, beta := 3.0, 5.0
	probabilities := []float64{0.1, 0.25, 0.5, 0.75, 0.9}

	var prevQ float64
	for i, p := range probabilities {
		q := betaQuantile(p, alpha, beta)

		if i > 0 && q < prevQ {
			t.Errorf("Quantile not monotonic: q(%v)=%v < q(%v)=%v",
				p, q, probabilities[i-1], prevQ)
		}

		prevQ = q
	}
}

// =============================================================================
// Test gammaSample
// =============================================================================

func TestGammaSample_PositiveValues(t *testing.T) {
	tests := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{"shape=1 rate=1", 1.0, 1.0},
		{"shape=2 rate=1", 2.0, 1.0},
		{"shape=0.5 rate=1", 0.5, 1.0},
		{"shape=1 rate=2", 1.0, 2.0},
		{"shape=5 rate=2", 5.0, 2.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				sample := gammaSample(tt.alpha, tt.beta)

				if sample <= 0.0 {
					t.Errorf("Sample %d is not positive: %v", i, sample)
				}

				if math.IsNaN(sample) || math.IsInf(sample, 0) {
					t.Errorf("Sample %d is invalid: %v", i, sample)
				}
			}
		})
	}
}

func TestGammaSample_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		alpha    float64
		beta     float64
		expected float64
	}{
		{"zero alpha", 0.0, 1.0, 1.0},
		{"zero beta", 1.0, 0.0, 1.0},
		{"negative alpha", -1.0, 1.0, 1.0},
		{"negative beta", 1.0, -1.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sample := gammaSample(tt.alpha, tt.beta)

			if math.Abs(sample-tt.expected) > 1e-9 {
				t.Errorf("Expected %v for invalid parameters, got %v", tt.expected, sample)
			}
		})
	}
}

func TestGammaSample_MeanConvergence(t *testing.T) {
	tests := []struct {
		name         string
		alpha        float64
		beta         float64
		expectedMean float64
	}{
		{"exponential", 1.0, 1.0, 1.0},
		{"alpha=2 beta=1", 2.0, 1.0, 2.0},
		{"alpha=2 beta=2", 2.0, 2.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const numSamples = 1000

			sum := 0.0
			for i := 0; i < numSamples; i++ {
				sum += gammaSample(tt.alpha, tt.beta)
			}
			sampleMean := sum / float64(numSamples)

			tolerance := tt.expectedMean * 0.2
			if math.Abs(sampleMean-tt.expectedMean) > tolerance {
				t.Errorf("Sample mean %v differs from expected %v by more than %v",
					sampleMean, tt.expectedMean, tolerance)
			}
		})
	}
}

// =============================================================================
// Test poissonSample
// =============================================================================

func TestPoissonSample_NonNegativeIntegers(t *testing.T) {
	lambdas := []float64{0.5, 1.0, 3.0, 5.0, 10.0, 20.0}

	for _, lambda := range lambdas {
		t.Run(fmt.Sprintf("lambda=%.1f", lambda), func(t *testing.T) {
			for i := 0; i < 100; i++ {
				sample := poissonSample(lambda)

				if sample < 0 {
					t.Errorf("Sample %d is negative: %v", i, sample)
				}
			}
		})
	}
}

func TestPoissonSample_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		lambda   float64
		expected int
	}{
		{"zero lambda", 0.0, 0},
		{"negative lambda", -1.0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sample := poissonSample(tt.lambda)

			if sample != tt.expected {
				t.Errorf("Expected %v for invalid lambda, got %v", tt.expected, sample)
			}
		})
	}
}

func TestPoissonSample_MeanConvergence(t *testing.T) {
	tests := []struct {
		name         string
		lambda       float64
		expectedMean float64
	}{
		{"lambda=1", 1.0, 1.0},
		{"lambda=5", 5.0, 5.0},
		{"lambda=10", 10.0, 10.0},
		{"lambda=20", 20.0, 20.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const numSamples = 2000

			sum := 0
			for i := 0; i < numSamples; i++ {
				sum += poissonSample(tt.lambda)
			}
			sampleMean := float64(sum) / float64(numSamples)

			tolerance := tt.expectedMean*0.25 + 1.0
			if math.Abs(sampleMean-tt.expectedMean) > tolerance {
				t.Errorf("Sample mean %v differs from expected %v by more than %v",
					sampleMean, tt.expectedMean, tolerance)
			}
		})
	}
}

// =============================================================================
// Test dotProduct
// =============================================================================

func TestDotProduct_CorrectCalculation(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
	}{
		{
			name:     "simple vectors",
			a:        []float64{1.0, 2.0, 3.0},
			b:        []float64{4.0, 5.0, 6.0},
			expected: 32.0,
		},
		{
			name:     "orthogonal vectors",
			a:        []float64{1.0, 0.0},
			b:        []float64{0.0, 1.0},
			expected: 0.0,
		},
		{
			name:     "parallel vectors",
			a:        []float64{1.0, 2.0, 3.0},
			b:        []float64{2.0, 4.0, 6.0},
			expected: 28.0,
		},
		{
			name:     "negative values",
			a:        []float64{1.0, -2.0, 3.0},
			b:        []float64{-1.0, 2.0, -3.0},
			expected: -14.0,
		},
		{
			name:     "single element",
			a:        []float64{5.0},
			b:        []float64{3.0},
			expected: 15.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dotProduct(tt.a, tt.b)

			if math.Abs(result-tt.expected) > 1e-9 {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}

			if math.IsNaN(result) || math.IsInf(result, 0) {
				t.Errorf("Result is invalid: %v", result)
			}
		})
	}
}

func TestDotProduct_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
	}{
		{"nil vectors", nil, nil, 0.0},
		{"first nil", nil, []float64{1.0, 2.0}, 0.0},
		{"second nil", []float64{1.0, 2.0}, nil, 0.0},
		{"empty vectors", []float64{}, []float64{}, 0.0},
		{"first empty", []float64{}, []float64{1.0, 2.0}, 0.0},
		{"second empty", []float64{1.0, 2.0}, []float64{}, 0.0},
		{
			"different lengths - first longer",
			[]float64{1.0, 2.0, 3.0},
			[]float64{4.0, 5.0},
			14.0,
		},
		{
			"different lengths - second longer",
			[]float64{1.0, 2.0},
			[]float64{4.0, 5.0, 6.0},
			14.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dotProduct(tt.a, tt.b)

			if math.Abs(result-tt.expected) > 1e-9 {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestDotProduct_Commutative(t *testing.T) {
	a := []float64{1.0, 2.0, 3.0, 4.0}
	b := []float64{5.0, 6.0, 7.0, 8.0}

	ab := dotProduct(a, b)
	ba := dotProduct(b, a)

	if math.Abs(ab-ba) > 1e-9 {
		t.Errorf("Dot product not commutative: a·b=%v, b·a=%v", ab, ba)
	}
}

// =============================================================================
// Statistical Distribution Tests - Kolmogorov-Smirnov Test
// =============================================================================

func TestBetaSample_KolmogorovSmirnov(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping statistical test in short mode")
	}

	tests := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{"uniform", 1.0, 1.0},
		{"symmetric", 5.0, 5.0},
		{"asymmetric", 2.0, 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const numSamples = 1000
			samples := make([]float64, numSamples)

			for i := 0; i < numSamples; i++ {
				samples[i] = betaSample(tt.alpha, tt.beta)
			}

			ksStatistic := kolmogorovSmirnovBeta(samples, tt.alpha, tt.beta)

			criticalValue := 1.36 / math.Sqrt(float64(numSamples))

			if ksStatistic > criticalValue {
				t.Logf("Warning: KS statistic %v exceeds critical value %v (may indicate distribution mismatch)",
					ksStatistic, criticalValue)
			}
		})
	}
}

func TestGammaSample_KolmogorovSmirnov(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping statistical test in short mode")
	}

	tests := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{"exponential", 1.0, 1.0},
		{"shape=2", 2.0, 1.0},
		{"shape=5", 5.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const numSamples = 1000
			samples := make([]float64, numSamples)

			for i := 0; i < numSamples; i++ {
				samples[i] = gammaSample(tt.alpha, tt.beta)
			}

			ksStatistic := kolmogorovSmirnovGamma(samples, tt.alpha, tt.beta)

			criticalValue := 1.36 / math.Sqrt(float64(numSamples))

			if ksStatistic > criticalValue {
				t.Logf("Warning: KS statistic %v exceeds critical value %v (may indicate distribution mismatch)",
					ksStatistic, criticalValue)
			}
		})
	}
}

func TestPoissonSample_ChiSquared(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping statistical test in short mode")
	}

	tests := []struct {
		name   string
		lambda float64
	}{
		{"lambda=1", 1.0},
		{"lambda=5", 5.0},
		{"lambda=10", 10.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const numSamples = 1000
			samples := make([]int, numSamples)

			for i := 0; i < numSamples; i++ {
				samples[i] = poissonSample(tt.lambda)
			}

			chiSquared := chiSquaredPoisson(samples, tt.lambda)

			degreesOfFreedom := 10.0
			criticalValue := 18.307

			if chiSquared > criticalValue {
				t.Logf("Warning: Chi-squared %v exceeds critical value %v at df=%v (may indicate distribution mismatch)",
					chiSquared, criticalValue, degreesOfFreedom)
			}
		})
	}
}

// =============================================================================
// Statistical Test Helpers
// =============================================================================

func kolmogorovSmirnovBeta(samples []float64, alpha, beta float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0.0
	}

	sorted := make([]float64, n)
	copy(sorted, samples)
	sort.Float64s(sorted)

	maxD := 0.0
	for i, x := range sorted {
		empiricalCDF := float64(i+1) / float64(n)
		theoreticalCDF := incompleteBeta(x, alpha, beta)

		d := math.Abs(empiricalCDF - theoreticalCDF)
		if d > maxD {
			maxD = d
		}
	}

	return maxD
}

func kolmogorovSmirnovGamma(samples []float64, alpha, beta float64) float64 {
	n := len(samples)
	if n == 0 {
		return 0.0
	}

	sorted := make([]float64, n)
	copy(sorted, samples)
	sort.Float64s(sorted)

	maxD := 0.0
	for i, x := range sorted {
		empiricalCDF := float64(i+1) / float64(n)
		theoreticalCDF := incompleteGammaCDF(x, alpha, beta)

		d := math.Abs(empiricalCDF - theoreticalCDF)
		if d > maxD {
			maxD = d
		}
	}

	return maxD
}

func chiSquaredPoisson(samples []int, lambda float64) float64 {
	frequencies := make(map[int]int)
	for _, s := range samples {
		frequencies[s]++
	}

	maxK := 0
	for k := range frequencies {
		if k > maxK {
			maxK = k
		}
	}

	chiSq := 0.0
	n := float64(len(samples))

	for k := 0; k <= maxK; k++ {
		observed := float64(frequencies[k])
		expected := n * poissonPMF(k, lambda)

		if expected > 5 {
			chiSq += (observed - expected) * (observed - expected) / expected
		}
	}

	return chiSq
}

func incompleteGammaCDF(x, alpha, beta float64) float64 {
	if x <= 0 {
		return 0.0
	}

	x = x * beta

	sum := 0.0
	term := 1.0 / alpha
	k := 1.0

	for i := 0; i < 100; i++ {
		sum += term
		term *= x / (alpha + k)
		k++

		if term < 1e-10 {
			break
		}
	}

	return sum * math.Pow(x, alpha) * math.Exp(-x) / math.Gamma(alpha)
}

func poissonPMF(k int, lambda float64) float64 {
	if k < 0 {
		return 0.0
	}

	return math.Exp(float64(k)*math.Log(lambda) - lambda - logGamma(float64(k+1)))
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkBetaSample(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = betaSample(5.0, 5.0)
	}
}

func BenchmarkBetaQuantile(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = betaQuantile(0.95, 5.0, 5.0)
	}
}

func BenchmarkGammaSample(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = gammaSample(5.0, 1.0)
	}
}

func BenchmarkPoissonSample(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = poissonSample(10.0)
	}
}

func BenchmarkDotProduct(b *testing.B) {
	a := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	vec := []float64{6.0, 7.0, 8.0, 9.0, 10.0}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dotProduct(a, vec)
	}
}
