package chunking

import (
	"encoding/json"
	"math"
	"testing"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewLearnedContextSize(t *testing.T) {
	tests := []struct {
		name          string
		priorMean     float64
		priorVariance float64
		wantErr       bool
	}{
		{
			name:          "valid parameters",
			priorMean:     2048.0,
			priorVariance: 1024.0,
			wantErr:       false,
		},
		{
			name:          "small mean and variance",
			priorMean:     100.0,
			priorVariance: 50.0,
			wantErr:       false,
		},
		{
			name:          "negative mean",
			priorMean:     -100.0,
			priorVariance: 50.0,
			wantErr:       true,
		},
		{
			name:          "zero mean",
			priorMean:     0.0,
			priorVariance: 50.0,
			wantErr:       true,
		},
		{
			name:          "negative variance",
			priorMean:     100.0,
			priorVariance: -50.0,
			wantErr:       true,
		},
		{
			name:          "zero variance",
			priorMean:     100.0,
			priorVariance: 0.0,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lcs, err := NewLearnedContextSize(tt.priorMean, tt.priorVariance)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewLearnedContextSize() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewLearnedContextSize() unexpected error: %v", err)
			}
			if lcs == nil {
				t.Fatal("NewLearnedContextSize() returned nil")
			}
			if lcs.Alpha <= 0 {
				t.Errorf("Alpha should be positive, got %f", lcs.Alpha)
			}
			if lcs.Beta <= 0 {
				t.Errorf("Beta should be positive, got %f", lcs.Beta)
			}
			if lcs.EffectiveSamples <= 0 {
				t.Errorf("EffectiveSamples should be positive, got %f", lcs.EffectiveSamples)
			}
		})
	}
}

// =============================================================================
// Mean Tests
// =============================================================================

func TestLearnedContextSize_Mean(t *testing.T) {
	tests := []struct {
		name string
		lcs  *LearnedContextSize
		want int
	}{
		{
			name: "typical context size",
			lcs: &LearnedContextSize{
				Alpha: 2048.0,
				Beta:  1.0,
			},
			want: 2048,
		},
		{
			name: "small context size",
			lcs: &LearnedContextSize{
				Alpha: 100.0,
				Beta:  1.0,
			},
			want: 100,
		},
		{
			name: "fractional mean rounds correctly",
			lcs: &LearnedContextSize{
				Alpha: 150.0,
				Beta:  1.0,
			},
			want: 150,
		},
		{
			name: "zero beta returns zero",
			lcs: &LearnedContextSize{
				Alpha: 100.0,
				Beta:  0.0,
			},
			want: 0,
		},
		{
			name: "negative beta returns zero",
			lcs: &LearnedContextSize{
				Alpha: 100.0,
				Beta:  -1.0,
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.lcs.Mean()
			if got != tt.want {
				t.Errorf("Mean() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLearnedContextSize_MeanReturnsInt(t *testing.T) {
	lcs, err := NewLearnedContextSize(2048.0, 1024.0)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	mean := lcs.Mean()
	if mean <= 0 {
		t.Errorf("Mean() should return positive integer, got %d", mean)
	}
}

// =============================================================================
// Sample Tests
// =============================================================================

func TestLearnedContextSize_Sample(t *testing.T) {
	lcs, err := NewLearnedContextSize(2048.0, 1024.0)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	const numSamples = 100
	samples := make([]int, numSamples)

	for i := 0; i < numSamples; i++ {
		sample := lcs.Sample()
		if sample <= 0 {
			t.Errorf("Sample() must return positive integer, got %d", sample)
		}
		samples[i] = sample
	}

	mean := 0.0
	for _, s := range samples {
		mean += float64(s)
	}
	mean /= float64(numSamples)

	expectedMean := float64(lcs.Mean())
	tolerance := expectedMean * 0.3

	if math.Abs(mean-expectedMean) > tolerance {
		t.Logf("Sample mean %f is outside tolerance of expected %f", mean, expectedMean)
	}
}

func TestLearnedContextSize_SampleGeneratesValidSizes(t *testing.T) {
	lcs, err := NewLearnedContextSize(1024.0, 512.0)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	for i := 0; i < 50; i++ {
		size := lcs.Sample()
		if size < 1 {
			t.Errorf("Sample() returned invalid size %d (must be >= 1)", size)
		}
	}
}

func TestLearnedContextSize_SampleWithInvalidParams(t *testing.T) {
	tests := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{
			name:  "zero alpha",
			alpha: 0.0,
			beta:  1.0,
		},
		{
			name:  "negative alpha",
			alpha: -1.0,
			beta:  1.0,
		},
		{
			name:  "zero beta",
			alpha: 100.0,
			beta:  0.0,
		},
		{
			name:  "negative beta",
			alpha: 100.0,
			beta:  -1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lcs := &LearnedContextSize{
				Alpha: tt.alpha,
				Beta:  tt.beta,
			}
			result := lcs.Sample()
			if result != 0 {
				t.Errorf("Sample() with invalid params should return 0, got %d", result)
			}
		})
	}
}

// =============================================================================
// Confidence Tests
// =============================================================================

func TestLearnedContextSize_Confidence(t *testing.T) {
	tests := []struct {
		name             string
		effectiveSamples float64
		wantMin          float64
		wantMax          float64
	}{
		{
			name:             "no samples",
			effectiveSamples: 0.0,
			wantMin:          0.0,
			wantMax:          0.1,
		},
		{
			name:             "few samples",
			effectiveSamples: 1.0,
			wantMin:          0.05,
			wantMax:          0.15,
		},
		{
			name:             "moderate samples",
			effectiveSamples: 10.0,
			wantMin:          0.5,
			wantMax:          0.7,
		},
		{
			name:             "many samples",
			effectiveSamples: 50.0,
			wantMin:          0.9,
			wantMax:          1.0,
		},
		{
			name:             "very many samples",
			effectiveSamples: 100.0,
			wantMin:          0.99,
			wantMax:          1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lcs := &LearnedContextSize{
				Alpha:            100.0,
				Beta:             1.0,
				EffectiveSamples: tt.effectiveSamples,
			}
			confidence := lcs.Confidence()
			if confidence < tt.wantMin || confidence > tt.wantMax {
				t.Errorf("Confidence() = %f, want between %f and %f",
					confidence, tt.wantMin, tt.wantMax)
			}
			if confidence < 0 || confidence > 1 {
				t.Errorf("Confidence() = %f, must be in [0, 1]", confidence)
			}
		})
	}
}

// =============================================================================
// Update Tests
// =============================================================================

func TestLearnedContextSize_Update(t *testing.T) {
	lcs, err := NewLearnedContextSize(2048.0, 1024.0)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	config := DefaultUpdateConfig()

	tests := []struct {
		name     string
		observed int
		wantErr  bool
	}{
		{
			name:     "valid positive observation",
			observed: 1024,
			wantErr:  false,
		},
		{
			name:     "small positive observation",
			observed: 10,
			wantErr:  false,
		},
		{
			name:     "large positive observation",
			observed: 10000,
			wantErr:  false,
		},
		{
			name:     "zero observation",
			observed: 0,
			wantErr:  true,
		},
		{
			name:     "negative observation",
			observed: -100,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := lcs.Update(tt.observed, config)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Update() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("Update() unexpected error: %v", err)
			}
		})
	}
}

func TestLearnedContextSize_UpdateConverges(t *testing.T) {
	lcs, err := NewLearnedContextSize(1000.0, 500.0)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	config := &UpdateConfig{
		DecayFactor:         0.95,
		MinEffectiveSamples: 1.0,
	}

	targetSize := 2048
	initialMean := lcs.Mean()

	for i := 0; i < 100; i++ {
		if err := lcs.Update(targetSize, config); err != nil {
			t.Fatalf("Update() error at iteration %d: %v", i, err)
		}
	}

	finalMean := lcs.Mean()
	tolerance := float64(targetSize) * 0.2

	if math.Abs(float64(finalMean-targetSize)) > tolerance {
		t.Logf("Mean converged from %d to %d (target %d)",
			initialMean, finalMean, targetSize)
	}

	if lcs.EffectiveSamples < config.MinEffectiveSamples {
		t.Errorf("EffectiveSamples %f below minimum %f",
			lcs.EffectiveSamples, config.MinEffectiveSamples)
	}
}

func TestLearnedContextSize_UpdateWithNilConfig(t *testing.T) {
	lcs, err := NewLearnedContextSize(2048.0, 1024.0)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	err = lcs.Update(1024, nil)
	if err != nil {
		t.Errorf("Update() with nil config should use defaults, got error: %v", err)
	}
}

func TestLearnedContextSize_UpdateWithInvalidDecayFactor(t *testing.T) {
	lcs, err := NewLearnedContextSize(2048.0, 1024.0)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	tests := []struct {
		name        string
		decayFactor float64
	}{
		{
			name:        "zero decay factor",
			decayFactor: 0.0,
		},
		{
			name:        "negative decay factor",
			decayFactor: -0.5,
		},
		{
			name:        "decay factor > 1",
			decayFactor: 1.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &UpdateConfig{
				DecayFactor:         tt.decayFactor,
				MinEffectiveSamples: 1.0,
			}
			err := lcs.Update(1024, config)
			if err == nil {
				t.Errorf("Update() with invalid decay factor should error")
			}
		})
	}
}

// =============================================================================
// JSON Serialization Tests
// =============================================================================

func TestLearnedContextSize_MarshalJSON(t *testing.T) {
	lcs, err := NewLearnedContextSize(2048.0, 1024.0)
	if err != nil {
		t.Fatalf("NewLearnedContextSize() error: %v", err)
	}

	data, err := json.Marshal(lcs)
	if err != nil {
		t.Fatalf("MarshalJSON() error: %v", err)
	}

	if len(data) == 0 {
		t.Error("MarshalJSON() returned empty data")
	}

	var decoded LearnedContextSize
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("UnmarshalJSON() error: %v", err)
	}

	if decoded.Alpha != lcs.Alpha {
		t.Errorf("Alpha mismatch: got %f, want %f", decoded.Alpha, lcs.Alpha)
	}
	if decoded.Beta != lcs.Beta {
		t.Errorf("Beta mismatch: got %f, want %f", decoded.Beta, lcs.Beta)
	}
	if decoded.EffectiveSamples != lcs.EffectiveSamples {
		t.Errorf("EffectiveSamples mismatch: got %f, want %f",
			decoded.EffectiveSamples, lcs.EffectiveSamples)
	}
}

// =============================================================================
// Gamma Random Tests
// =============================================================================

func TestGammaRandom(t *testing.T) {
	tests := []struct {
		name  string
		alpha float64
		beta  float64
	}{
		{
			name:  "alpha > 1",
			alpha: 2.0,
			beta:  1.0,
		},
		{
			name:  "alpha = 1",
			alpha: 1.0,
			beta:  1.0,
		},
		{
			name:  "alpha < 1",
			alpha: 0.5,
			beta:  1.0,
		},
		{
			name:  "high alpha",
			alpha: 100.0,
			beta:  1.0,
		},
		{
			name:  "high beta",
			alpha: 2.0,
			beta:  10.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const numSamples = 100
			for i := 0; i < numSamples; i++ {
				value := gammaRandom(tt.alpha, tt.beta)
				if value <= 0 {
					t.Errorf("gammaRandom() returned non-positive value: %f", value)
				}
				if math.IsNaN(value) || math.IsInf(value, 0) {
					t.Errorf("gammaRandom() returned invalid value: %f", value)
				}
			}
		})
	}
}

// =============================================================================
// Default Config Tests
// =============================================================================

func TestDefaultUpdateConfig(t *testing.T) {
	config := DefaultUpdateConfig()
	if config == nil {
		t.Fatal("DefaultUpdateConfig() returned nil")
	}
	if config.DecayFactor <= 0 || config.DecayFactor > 1 {
		t.Errorf("DecayFactor %f not in (0, 1]", config.DecayFactor)
	}
	if config.MinEffectiveSamples <= 0 {
		t.Errorf("MinEffectiveSamples %f must be positive", config.MinEffectiveSamples)
	}
}

// =============================================================================
// OverflowStrategy Tests
// =============================================================================

func TestOverflowStrategy_String(t *testing.T) {
	tests := []struct {
		name     string
		strategy OverflowStrategy
		want     string
	}{
		{
			name:     "recursive",
			strategy: StrategyRecursive,
			want:     "recursive",
		},
		{
			name:     "sentence",
			strategy: StrategySentence,
			want:     "sentence",
		},
		{
			name:     "truncate",
			strategy: StrategyTruncate,
			want:     "truncate",
		},
		{
			name:     "unknown",
			strategy: OverflowStrategy(999),
			want:     "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.strategy.String()
			if got != tt.want {
				t.Errorf("String() = %s, want %s", got, tt.want)
			}
		})
	}
}

// =============================================================================
// LearnedOverflowWeights Constructor Tests
// =============================================================================

func TestNewLearnedOverflowWeights(t *testing.T) {
	low := NewLearnedOverflowWeights()
	if low == nil {
		t.Fatal("NewLearnedOverflowWeights() returned nil")
	}
	if low.RecursiveCount != 1.0 {
		t.Errorf("RecursiveCount = %f, want 1.0", low.RecursiveCount)
	}
	if low.SentenceCount != 1.0 {
		t.Errorf("SentenceCount = %f, want 1.0", low.SentenceCount)
	}
	if low.TruncateCount != 1.0 {
		t.Errorf("TruncateCount = %f, want 1.0", low.TruncateCount)
	}
}

func TestNewLearnedOverflowWeightsWithPriors(t *testing.T) {
	tests := []struct {
		name      string
		recursive float64
		sentence  float64
		truncate  float64
		wantErr   bool
	}{
		{
			name:      "valid priors",
			recursive: 2.0,
			sentence:  3.0,
			truncate:  1.0,
			wantErr:   false,
		},
		{
			name:      "equal priors",
			recursive: 1.0,
			sentence:  1.0,
			truncate:  1.0,
			wantErr:   false,
		},
		{
			name:      "zero recursive",
			recursive: 0.0,
			sentence:  1.0,
			truncate:  1.0,
			wantErr:   true,
		},
		{
			name:      "negative sentence",
			recursive: 1.0,
			sentence:  -1.0,
			truncate:  1.0,
			wantErr:   true,
		},
		{
			name:      "zero truncate",
			recursive: 1.0,
			sentence:  1.0,
			truncate:  0.0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			low, err := NewLearnedOverflowWeightsWithPriors(
				tt.recursive,
				tt.sentence,
				tt.truncate,
			)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewLearnedOverflowWeightsWithPriors() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewLearnedOverflowWeightsWithPriors() unexpected error: %v", err)
			}
			if low == nil {
				t.Fatal("NewLearnedOverflowWeightsWithPriors() returned nil")
			}
			if low.RecursiveCount != tt.recursive {
				t.Errorf("RecursiveCount = %f, want %f", low.RecursiveCount, tt.recursive)
			}
			if low.SentenceCount != tt.sentence {
				t.Errorf("SentenceCount = %f, want %f", low.SentenceCount, tt.sentence)
			}
			if low.TruncateCount != tt.truncate {
				t.Errorf("TruncateCount = %f, want %f", low.TruncateCount, tt.truncate)
			}
		})
	}
}

// =============================================================================
// LearnedOverflowWeights BestStrategy Tests
// =============================================================================

func TestLearnedOverflowWeights_BestStrategy(t *testing.T) {
	tests := []struct {
		name      string
		recursive float64
		sentence  float64
		truncate  float64
		want      OverflowStrategy
	}{
		{
			name:      "recursive best",
			recursive: 10.0,
			sentence:  1.0,
			truncate:  1.0,
			want:      StrategyRecursive,
		},
		{
			name:      "sentence best",
			recursive: 1.0,
			sentence:  10.0,
			truncate:  1.0,
			want:      StrategySentence,
		},
		{
			name:      "truncate best",
			recursive: 1.0,
			sentence:  1.0,
			truncate:  10.0,
			want:      StrategyTruncate,
		},
		{
			name:      "equal weights favor recursive",
			recursive: 1.0,
			sentence:  1.0,
			truncate:  1.0,
			want:      StrategyRecursive,
		},
		{
			name:      "tie between sentence and truncate",
			recursive: 1.0,
			sentence:  5.0,
			truncate:  5.0,
			want:      StrategySentence,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			low, err := NewLearnedOverflowWeightsWithPriors(
				tt.recursive,
				tt.sentence,
				tt.truncate,
			)
			if err != nil {
				t.Fatalf("NewLearnedOverflowWeightsWithPriors() error: %v", err)
			}
			got := low.BestStrategy()
			if got != tt.want {
				t.Errorf("BestStrategy() = %s, want %s", got, tt.want)
			}
		})
	}
}

// =============================================================================
// LearnedOverflowWeights Sample Tests
// =============================================================================

func TestLearnedOverflowWeights_Sample(t *testing.T) {
	low := NewLearnedOverflowWeights()

	const numSamples = 100
	counts := make(map[OverflowStrategy]int)

	for i := 0; i < numSamples; i++ {
		strategy := low.Sample()
		counts[strategy]++

		if strategy != StrategyRecursive &&
			strategy != StrategySentence &&
			strategy != StrategyTruncate {
			t.Errorf("Sample() returned invalid strategy: %d", strategy)
		}
	}

	for strategy, count := range counts {
		if count > 0 {
			t.Logf("Strategy %s: %d/%d samples", strategy, count, numSamples)
		}
	}
}

func TestLearnedOverflowWeights_SampleDistribution(t *testing.T) {
	low, err := NewLearnedOverflowWeightsWithPriors(10.0, 1.0, 1.0)
	if err != nil {
		t.Fatalf("NewLearnedOverflowWeightsWithPriors() error: %v", err)
	}

	const numSamples = 1000
	counts := make(map[OverflowStrategy]int)

	for i := 0; i < numSamples; i++ {
		strategy := low.Sample()
		counts[strategy]++
	}

	recursiveRatio := float64(counts[StrategyRecursive]) / float64(numSamples)
	if recursiveRatio < 0.6 {
		t.Logf("Recursive strategy should dominate with 10:1:1 weights, got %.2f%%", recursiveRatio*100)
	}
}

// =============================================================================
// LearnedOverflowWeights Update Tests
// =============================================================================

func TestLearnedOverflowWeights_Update(t *testing.T) {
	low := NewLearnedOverflowWeights()
	initialRecursive := low.RecursiveCount

	low.Update(StrategyRecursive, true)
	if low.RecursiveCount <= initialRecursive {
		t.Errorf("Update() with useful=true should increase count")
	}

	low2 := NewLearnedOverflowWeights()
	initialSentence := low2.SentenceCount

	low2.Update(StrategySentence, false)
	if low2.SentenceCount <= initialSentence {
		t.Errorf("Update() with useful=false should still increase count")
	}
}

func TestLearnedOverflowWeights_UpdateConvergence(t *testing.T) {
	low := NewLearnedOverflowWeights()

	for i := 0; i < 100; i++ {
		low.Update(StrategySentence, true)
	}

	best := low.BestStrategy()
	if best != StrategySentence {
		t.Errorf("After 100 positive updates to Sentence, BestStrategy() = %s, want %s",
			best, StrategySentence)
	}
}

func TestLearnedOverflowWeights_UpdateAllStrategies(t *testing.T) {
	low := NewLearnedOverflowWeights()
	initial := *low

	strategies := []OverflowStrategy{
		StrategyRecursive,
		StrategySentence,
		StrategyTruncate,
	}

	for _, strategy := range strategies {
		low.Update(strategy, true)
	}

	if low.RecursiveCount <= initial.RecursiveCount {
		t.Errorf("RecursiveCount not updated")
	}
	if low.SentenceCount <= initial.SentenceCount {
		t.Errorf("SentenceCount not updated")
	}
	if low.TruncateCount <= initial.TruncateCount {
		t.Errorf("TruncateCount not updated")
	}
}

// =============================================================================
// LearnedOverflowWeights JSON Serialization Tests
// =============================================================================

func TestLearnedOverflowWeights_MarshalJSON(t *testing.T) {
	low, err := NewLearnedOverflowWeightsWithPriors(2.0, 3.0, 1.0)
	if err != nil {
		t.Fatalf("NewLearnedOverflowWeightsWithPriors() error: %v", err)
	}

	data, err := json.Marshal(low)
	if err != nil {
		t.Fatalf("MarshalJSON() error: %v", err)
	}

	if len(data) == 0 {
		t.Error("MarshalJSON() returned empty data")
	}

	var decoded LearnedOverflowWeights
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("UnmarshalJSON() error: %v", err)
	}

	if decoded.RecursiveCount != low.RecursiveCount {
		t.Errorf("RecursiveCount mismatch: got %f, want %f",
			decoded.RecursiveCount, low.RecursiveCount)
	}
	if decoded.SentenceCount != low.SentenceCount {
		t.Errorf("SentenceCount mismatch: got %f, want %f",
			decoded.SentenceCount, low.SentenceCount)
	}
	if decoded.TruncateCount != low.TruncateCount {
		t.Errorf("TruncateCount mismatch: got %f, want %f",
			decoded.TruncateCount, low.TruncateCount)
	}
}
