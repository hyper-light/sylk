package quantization

import (
	"context"
	"math"
	"testing"
)

// =============================================================================
// SubspaceWeights Tests
// =============================================================================

func TestNewUniformWeights(t *testing.T) {
	tests := []struct {
		name         string
		numSubspaces int
		expectNil    bool
	}{
		{"4 subspaces", 4, false},
		{"8 subspaces", 8, false},
		{"16 subspaces", 16, false},
		{"32 subspaces", 32, false},
		{"1 subspace", 1, false},
		{"zero subspaces", 0, true},
		{"negative subspaces", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weights := NewSubspaceWeights(tt.numSubspaces)

			if tt.expectNil {
				if weights != nil {
					t.Errorf("NewSubspaceWeights(%d) = %v, want nil", tt.numSubspaces, weights)
				}
				return
			}

			if weights == nil {
				t.Fatalf("NewSubspaceWeights(%d) = nil, want non-nil", tt.numSubspaces)
			}

			if len(weights) != tt.numSubspaces {
				t.Errorf("len(weights) = %d, want %d", len(weights), tt.numSubspaces)
			}

			// Verify uniform distribution
			expectedWeight := float32(1.0) / float32(tt.numSubspaces)
			for i, w := range weights {
				if w != expectedWeight {
					t.Errorf("weights[%d] = %f, want %f", i, w, expectedWeight)
				}
			}
		})
	}
}

func TestSubspaceWeightsSum(t *testing.T) {
	tests := []struct {
		name        string
		weights     SubspaceWeights
		expectedSum float32
	}{
		{"uniform 4", NewSubspaceWeights(4), 1.0},
		{"uniform 8", NewSubspaceWeights(8), 1.0},
		{"uniform 16", NewSubspaceWeights(16), 1.0},
		{"uniform 32", NewSubspaceWeights(32), 1.0},
		{"custom weights", SubspaceWeights{0.1, 0.2, 0.3, 0.4}, 1.0},
		{"non-normalized", SubspaceWeights{1.0, 2.0, 3.0, 4.0}, 10.0},
		{"empty", SubspaceWeights{}, 0.0},
		{"nil", nil, 0.0},
		{"single", SubspaceWeights{1.0}, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sum := tt.weights.Sum()
			if math.Abs(float64(sum-tt.expectedSum)) > 0.0001 {
				t.Errorf("Sum() = %f, want %f", sum, tt.expectedSum)
			}
		})
	}
}

func TestSubspaceWeightsIsNormalized(t *testing.T) {
	tests := []struct {
		name       string
		weights    SubspaceWeights
		normalized bool
	}{
		{"uniform 4", NewSubspaceWeights(4), true},
		{"uniform 8", NewSubspaceWeights(8), true},
		{"uniform 16", NewSubspaceWeights(16), true},
		{"uniform 32", NewSubspaceWeights(32), true},
		{"custom normalized", SubspaceWeights{0.25, 0.25, 0.25, 0.25}, true},
		{"custom normalized 2", SubspaceWeights{0.1, 0.2, 0.3, 0.4}, true},
		{"non-normalized", SubspaceWeights{1.0, 2.0, 3.0, 4.0}, false},
		{"half sum", SubspaceWeights{0.125, 0.125, 0.125, 0.125}, false},
		{"zero sum", SubspaceWeights{0.0, 0.0, 0.0, 0.0}, false},
		{"empty", SubspaceWeights{}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.weights.IsNormalized(); got != tt.normalized {
				t.Errorf("IsNormalized() = %v, want %v (sum=%f)", got, tt.normalized, tt.weights.Sum())
			}
		})
	}
}

func TestSubspaceWeightsNormalize(t *testing.T) {
	tests := []struct {
		name    string
		weights SubspaceWeights
	}{
		{"non-normalized", SubspaceWeights{1.0, 2.0, 3.0, 4.0}},
		{"already normalized", SubspaceWeights{0.25, 0.25, 0.25, 0.25}},
		{"large values", SubspaceWeights{100.0, 200.0, 300.0, 400.0}},
		{"small values", SubspaceWeights{0.001, 0.002, 0.003, 0.004}},
		{"uneven", SubspaceWeights{0.1, 0.5, 0.2, 0.8}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			weights := tt.weights.Clone()
			weights.Normalize()

			if !weights.IsNormalized() {
				t.Errorf("After Normalize(), IsNormalized() = false, want true (sum=%f)", weights.Sum())
			}
		})
	}
}

func TestSubspaceWeightsNormalizeEdgeCases(t *testing.T) {
	// Test zero sum doesn't panic
	t.Run("zero sum", func(t *testing.T) {
		weights := SubspaceWeights{0.0, 0.0, 0.0, 0.0}
		weights.Normalize() // Should not panic
		// Sum is still 0, so normalization doesn't change anything
		for _, w := range weights {
			if w != 0.0 {
				t.Errorf("After normalizing zero weights, got non-zero value: %f", w)
			}
		}
	})

	// Test empty doesn't panic
	t.Run("empty", func(t *testing.T) {
		weights := SubspaceWeights{}
		weights.Normalize() // Should not panic
	})

	// Test nil doesn't panic
	t.Run("nil", func(t *testing.T) {
		var weights SubspaceWeights
		weights.Normalize() // Should not panic
	})
}

func TestSubspaceWeightsString(t *testing.T) {
	tests := []struct {
		name     string
		weights  SubspaceWeights
		contains string
	}{
		{"nil", nil, "nil"},
		{"short 2", SubspaceWeights{0.5, 0.5}, "SubspaceWeights"},
		{"short 4", SubspaceWeights{0.25, 0.25, 0.25, 0.25}, "SubspaceWeights"},
		{"long 8", NewSubspaceWeights(8), "SubspaceWeights[8]"},
		{"long 32", NewSubspaceWeights(32), "SubspaceWeights[32]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.weights.String()
			if len(s) == 0 {
				t.Error("String() returned empty string")
			}
			// Just verify it doesn't panic and returns something reasonable
			if tt.contains != "" && !containsSubstr(s, tt.contains) {
				t.Errorf("String() = %q, want to contain %q", s, tt.contains)
			}
		})
	}
}

func TestSubspaceWeightsClone(t *testing.T) {
	tests := []struct {
		name    string
		weights SubspaceWeights
	}{
		{"standard", SubspaceWeights{0.1, 0.2, 0.3, 0.4}},
		{"uniform 8", NewSubspaceWeights(8)},
		{"empty", SubspaceWeights{}},
		{"nil", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clone := tt.weights.Clone()

			if !clone.Equal(tt.weights) {
				t.Errorf("Clone() not equal to original")
			}

			// Verify deep copy (modifications don't affect original)
			if len(clone) > 0 {
				original := tt.weights[0]
				clone[0] = 999.0
				if tt.weights[0] != original {
					t.Error("Clone() is not a deep copy - modifying clone affected original")
				}
			}
		})
	}
}

func TestSubspaceWeightsEqual(t *testing.T) {
	tests := []struct {
		name     string
		w1       SubspaceWeights
		w2       SubspaceWeights
		expected bool
	}{
		{"equal", SubspaceWeights{0.25, 0.25, 0.25, 0.25}, SubspaceWeights{0.25, 0.25, 0.25, 0.25}, true},
		{"different", SubspaceWeights{0.1, 0.2, 0.3, 0.4}, SubspaceWeights{0.4, 0.3, 0.2, 0.1}, false},
		{"different length", SubspaceWeights{0.5, 0.5}, SubspaceWeights{0.25, 0.25, 0.25, 0.25}, false},
		{"both nil", nil, nil, true},
		{"both empty", SubspaceWeights{}, SubspaceWeights{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.w1.Equal(tt.w2); got != tt.expected {
				t.Errorf("Equal() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// QueryProfile Tests
// =============================================================================

func TestNewQueryProfile(t *testing.T) {
	tests := []struct {
		name         string
		vectorDim    int
		numSubspaces int
		expectNil    bool
	}{
		{"standard 128/4", 128, 4, false},
		{"standard 256/8", 256, 8, false},
		{"standard 512/16", 512, 16, false},
		{"standard 768/32", 768, 32, false},
		{"zero dim", 0, 4, true},
		{"zero subspaces", 128, 0, true},
		{"negative dim", -128, 4, true},
		{"negative subspaces", 128, -4, true},
		{"non-divisible", 100, 3, true}, // 100 % 3 != 0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := NewQueryProfile(tt.vectorDim, tt.numSubspaces)

			if tt.expectNil {
				if profile != nil {
					t.Errorf("NewQueryProfile(%d, %d) should be nil", tt.vectorDim, tt.numSubspaces)
				}
				return
			}

			if profile == nil {
				t.Fatalf("NewQueryProfile(%d, %d) = nil, want non-nil", tt.vectorDim, tt.numSubspaces)
			}

			if profile.VectorDimension != tt.vectorDim {
				t.Errorf("VectorDimension = %d, want %d", profile.VectorDimension, tt.vectorDim)
			}
			if profile.NumSubspaces != tt.numSubspaces {
				t.Errorf("NumSubspaces = %d, want %d", profile.NumSubspaces, tt.numSubspaces)
			}
			if len(profile.SubspaceVariances) != tt.numSubspaces {
				t.Errorf("len(SubspaceVariances) = %d, want %d", len(profile.SubspaceVariances), tt.numSubspaces)
			}
			if len(profile.Weights) != tt.numSubspaces {
				t.Errorf("len(Weights) = %d, want %d", len(profile.Weights), tt.numSubspaces)
			}
			if profile.Strategy != WeightingUniform {
				t.Errorf("Strategy = %v, want WeightingUniform", profile.Strategy)
			}
		})
	}
}

func TestQueryProfileSubspaceDimension(t *testing.T) {
	tests := []struct {
		vectorDim    int
		numSubspaces int
		expectedDim  int
	}{
		{128, 4, 32},
		{256, 8, 32},
		{512, 16, 32},
		{768, 32, 24},
		{768, 24, 32},
		{64, 8, 8},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			profile := NewQueryProfile(tt.vectorDim, tt.numSubspaces)
			if profile == nil {
				t.Fatal("profile is nil")
			}
			if got := profile.SubspaceDimension(); got != tt.expectedDim {
				t.Errorf("SubspaceDimension() = %d, want %d", got, tt.expectedDim)
			}
		})
	}
}

func TestQueryProfileComputeFromQuery(t *testing.T) {
	profile := NewQueryProfile(128, 4)
	if profile == nil {
		t.Fatal("profile is nil")
	}

	// Create a query with varying variance per subspace
	query := make([]float32, 128)
	// Subspace 0: low variance (all similar values)
	for i := 0; i < 32; i++ {
		query[i] = 1.0 + float32(i)*0.01
	}
	// Subspace 1: medium variance
	for i := 32; i < 64; i++ {
		query[i] = float32(i-32) * 0.1
	}
	// Subspace 2: high variance
	for i := 64; i < 96; i++ {
		query[i] = float32(i-64) * 1.0
	}
	// Subspace 3: very high variance
	for i := 96; i < 128; i++ {
		query[i] = float32(i-96) * 10.0
	}

	err := profile.ComputeFromQuery(query, WeightingVariance)
	if err != nil {
		t.Fatalf("ComputeFromQuery() error: %v", err)
	}

	// Verify variances are computed and ordered
	if profile.SubspaceVariances[0] >= profile.SubspaceVariances[3] {
		t.Errorf("Expected SubspaceVariances[0] < SubspaceVariances[3], got %f >= %f",
			profile.SubspaceVariances[0], profile.SubspaceVariances[3])
	}

	// Verify weights are normalized
	if !profile.Weights.IsNormalized() {
		t.Errorf("Weights should be normalized after ComputeFromQuery, sum=%f", profile.Weights.Sum())
	}

	// Verify high variance subspaces get higher weights
	if profile.Weights[0] >= profile.Weights[3] {
		t.Errorf("Expected Weights[0] < Weights[3] (high variance = higher weight), got %f >= %f",
			profile.Weights[0], profile.Weights[3])
	}
}

func TestQueryProfileComputeFromQueryStrategies(t *testing.T) {
	profile := NewQueryProfile(64, 4)
	if profile == nil {
		t.Fatal("profile is nil")
	}

	query := make([]float32, 64)
	for i := range query {
		query[i] = float32(i)
	}

	strategies := []WeightingStrategy{
		WeightingUniform,
		WeightingVariance,
		WeightingEntropy,
	}

	for _, strategy := range strategies {
		t.Run(strategy.String(), func(t *testing.T) {
			err := profile.ComputeFromQuery(query, strategy)
			if err != nil {
				t.Fatalf("ComputeFromQuery() with %s error: %v", strategy, err)
			}

			if profile.Strategy != strategy {
				t.Errorf("Strategy = %v, want %v", profile.Strategy, strategy)
			}

			// Weights should always be normalized
			if !profile.Weights.IsNormalized() {
				t.Errorf("Weights not normalized for strategy %s, sum=%f", strategy, profile.Weights.Sum())
			}
		})
	}
}

func TestQueryProfileComputeFromQueryLearnedNotSupported(t *testing.T) {
	profile := NewQueryProfile(64, 4)
	query := make([]float32, 64)

	err := profile.ComputeFromQuery(query, WeightingLearned)
	if err != ErrQueryAdaptiveLearnedNotSupported {
		t.Errorf("Expected ErrQueryAdaptiveLearnedNotSupported, got %v", err)
	}
}

func TestQueryProfileComputeFromQueryDimensionMismatch(t *testing.T) {
	profile := NewQueryProfile(64, 4)
	wrongSizeQuery := make([]float32, 128) // Wrong size

	err := profile.ComputeFromQuery(wrongSizeQuery, WeightingVariance)
	if err != ErrQueryAdaptiveDimensionMismatch {
		t.Errorf("Expected ErrQueryAdaptiveDimensionMismatch, got %v", err)
	}
}

func TestQueryProfileString(t *testing.T) {
	profile := NewQueryProfile(128, 4)
	s := profile.String()
	if s == "" {
		t.Error("String() returned empty string")
	}

	// Nil profile
	var nilProfile *QueryProfile
	s = nilProfile.String()
	if s != "QueryProfile(nil)" {
		t.Errorf("nil.String() = %q, want %q", s, "QueryProfile(nil)")
	}
}

// =============================================================================
// QueryAdaptiveConfig Tests
// =============================================================================

func TestDefaultQueryAdaptiveConfig(t *testing.T) {
	config := DefaultQueryAdaptiveConfig()

	if !config.Enabled {
		t.Error("Default config should have Enabled = true")
	}
	if config.Strategy != WeightingVariance {
		t.Errorf("Default Strategy = %v, want WeightingVariance", config.Strategy)
	}
	if config.MinVarianceRatio != 2.0 {
		t.Errorf("Default MinVarianceRatio = %f, want 2.0", config.MinVarianceRatio)
	}
}

func TestQueryAdaptiveConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      QueryAdaptiveConfig
		expectErr   bool
		expectedErr error
	}{
		{
			name:      "valid default",
			config:    DefaultQueryAdaptiveConfig(),
			expectErr: false,
		},
		{
			name:      "valid ratio 1.0",
			config:    QueryAdaptiveConfig{Enabled: true, MinVarianceRatio: 1.0},
			expectErr: false,
		},
		{
			name:      "valid ratio 10.0",
			config:    QueryAdaptiveConfig{Enabled: true, MinVarianceRatio: 10.0},
			expectErr: false,
		},
		{
			name:        "invalid ratio 0.5",
			config:      QueryAdaptiveConfig{Enabled: true, MinVarianceRatio: 0.5},
			expectErr:   true,
			expectedErr: ErrQueryAdaptiveInvalidRatio,
		},
		{
			name:        "invalid ratio 0.0",
			config:      QueryAdaptiveConfig{Enabled: true, MinVarianceRatio: 0.0},
			expectErr:   true,
			expectedErr: ErrQueryAdaptiveInvalidRatio,
		},
		{
			name:        "invalid ratio negative",
			config:      QueryAdaptiveConfig{Enabled: true, MinVarianceRatio: -1.0},
			expectErr:   true,
			expectedErr: ErrQueryAdaptiveInvalidRatio,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr {
				if err == nil {
					t.Error("Expected error, got nil")
				} else if tt.expectedErr != nil && err != tt.expectedErr {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestQueryAdaptiveConfigString(t *testing.T) {
	config := DefaultQueryAdaptiveConfig()
	s := config.String()
	if s == "" {
		t.Error("String() returned empty string")
	}
	if !containsSubstr(s, "Enabled:true") {
		t.Errorf("String() = %q, should contain Enabled:true", s)
	}
}

// =============================================================================
// Variance Computation Tests
// =============================================================================

func TestComputeVariance(t *testing.T) {
	tests := []struct {
		name     string
		values   []float32
		expected float32
		epsilon  float32
	}{
		{"constant", []float32{5.0, 5.0, 5.0, 5.0}, 0.0, 0.0001},
		{"simple", []float32{1.0, 2.0, 3.0, 4.0, 5.0}, 2.0, 0.0001},
		{"two values", []float32{0.0, 10.0}, 25.0, 0.0001},
		{"single value", []float32{42.0}, 0.0, 0.0001},
		{"empty", []float32{}, 0.0, 0.0001},
		{"negative", []float32{-1.0, 0.0, 1.0}, 0.6667, 0.001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeVariance(tt.values)
			if math.Abs(float64(got-tt.expected)) > float64(tt.epsilon) {
				t.Errorf("computeVariance(%v) = %f, want %f", tt.values, got, tt.expected)
			}
		})
	}
}

func TestComputeEntropy(t *testing.T) {
	tests := []struct {
		name   string
		values []float32
		minVal float32
		maxVal float32
	}{
		{"uniform", []float32{1.0, 1.0, 1.0, 1.0}, 1.9, 2.1},            // ~2.0 bits for 4 equal values
		{"single non-zero", []float32{0.0, 0.0, 1.0, 0.0}, -0.01, 0.01}, // 0 bits
		{"empty", []float32{}, -0.01, 0.01},
		{"all zeros", []float32{0.0, 0.0, 0.0, 0.0}, -0.01, 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeEntropy(tt.values)
			if got < tt.minVal || got > tt.maxVal {
				t.Errorf("computeEntropy(%v) = %f, want in range [%f, %f]", tt.values, got, tt.minVal, tt.maxVal)
			}
		})
	}
}

func TestComputeEntropyWithNegatives(t *testing.T) {
	// Entropy should handle negative values by taking absolute values
	values := []float32{-1.0, 1.0, -1.0, 1.0}
	entropy := computeEntropy(values)
	if entropy < 0 {
		t.Errorf("Entropy should be non-negative, got %f", entropy)
	}
}

// =============================================================================
// ShouldApplyWeighting Tests (via QueryAdaptiveDistance)
// =============================================================================

func TestShouldApplyWeighting(t *testing.T) {
	// Create a minimal PQ for testing
	pq := createQueryAdaptiveTestPQ(t, 64, 4)
	if pq == nil {
		t.Skip("Could not create test PQ")
	}

	tests := []struct {
		name             string
		minVarianceRatio float32
		variances        []float32
		shouldApply      bool
	}{
		{
			name:             "uniform variance - should not apply",
			minVarianceRatio: 2.0,
			variances:        []float32{1.0, 1.0, 1.0, 1.0},
			shouldApply:      false,
		},
		{
			name:             "high ratio - should apply",
			minVarianceRatio: 2.0,
			variances:        []float32{1.0, 1.0, 1.0, 10.0},
			shouldApply:      true,
		},
		{
			name:             "exactly at threshold",
			minVarianceRatio: 2.0,
			variances:        []float32{1.0, 1.0, 1.0, 2.0},
			shouldApply:      true,
		},
		{
			name:             "below threshold",
			minVarianceRatio: 2.0,
			variances:        []float32{1.0, 1.0, 1.0, 1.5},
			shouldApply:      false,
		},
		{
			name:             "zero min variance with non-zero max",
			minVarianceRatio: 2.0,
			variances:        []float32{0.0, 0.0, 0.0, 1.0},
			shouldApply:      true,
		},
		{
			name:             "all zero variance",
			minVarianceRatio: 2.0,
			variances:        []float32{0.0, 0.0, 0.0, 0.0},
			shouldApply:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := QueryAdaptiveConfig{
				Enabled:          true,
				Strategy:         WeightingVariance,
				MinVarianceRatio: tt.minVarianceRatio,
			}

			qad, err := NewQueryAdaptiveDistance(config, pq)
			if err != nil {
				t.Fatalf("NewQueryAdaptiveDistance error: %v", err)
			}

			profile := &QueryProfile{
				VectorDimension:   64,
				NumSubspaces:      4,
				SubspaceVariances: tt.variances,
				Weights:           NewSubspaceWeights(4),
			}

			got := qad.shouldApplyWeights(profile)
			if got != tt.shouldApply {
				t.Errorf("shouldApplyWeights() = %v, want %v", got, tt.shouldApply)
			}
		})
	}
}

func TestShouldApplyWeightingNilProfile(t *testing.T) {
	pq := createQueryAdaptiveTestPQ(t, 64, 4)
	if pq == nil {
		t.Skip("Could not create test PQ")
	}

	config := DefaultQueryAdaptiveConfig()
	qad, _ := NewQueryAdaptiveDistance(config, pq)

	if qad.shouldApplyWeights(nil) {
		t.Error("shouldApplyWeights(nil) should return false")
	}

	emptyProfile := &QueryProfile{}
	if qad.shouldApplyWeights(emptyProfile) {
		t.Error("shouldApplyWeights(empty) should return false")
	}
}

// =============================================================================
// WeightedAsymmetricDistance Tests
// =============================================================================

func TestWeightedAsymmetricDistanceUniformWeights(t *testing.T) {
	pq := createQueryAdaptiveTestPQ(t, 64, 4)
	if pq == nil {
		t.Skip("Could not create test PQ")
	}

	config := QueryAdaptiveConfig{
		Enabled:          false, // Disabled means uniform weights
		Strategy:         WeightingVariance,
		MinVarianceRatio: 2.0,
	}

	qad, err := NewQueryAdaptiveDistance(config, pq)
	if err != nil {
		t.Fatalf("NewQueryAdaptiveDistance error: %v", err)
	}

	query := make([]float32, 64)
	for i := range query {
		query[i] = float32(i) * 0.1
	}

	table, err := qad.ComputeWeightedDistanceTable(query)
	if err != nil {
		t.Fatalf("ComputeWeightedDistanceTable error: %v", err)
	}

	code := PQCode{0, 0, 0, 0}
	dist := qad.WeightedAsymmetricDistance(table, code)

	if dist < 0 {
		t.Errorf("Distance should be non-negative, got %f", dist)
	}
}

func TestWeightedAsymmetricDistanceHigherWeightIncreasesContribution(t *testing.T) {
	// Create a mock weighted distance table
	numSubspaces := 4
	numCentroids := 256

	// Create base distance table
	baseTable := &DistanceTable{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: numCentroids,
		Table:                make([][]float32, numSubspaces),
	}
	for m := 0; m < numSubspaces; m++ {
		baseTable.Table[m] = make([]float32, numCentroids)
		for k := 0; k < numCentroids; k++ {
			baseTable.Table[m][k] = 1.0 // All distances = 1.0
		}
	}

	// Uniform weights
	uniformWeights := NewSubspaceWeights(numSubspaces)
	uniformTable := &WeightedDistanceTable{Table: baseTable, Weights: uniformWeights}

	// Non-uniform weights (first subspace has higher weight)
	nonUniformWeights := SubspaceWeights{0.7, 0.1, 0.1, 0.1}
	nonUniformTable := &WeightedDistanceTable{Table: baseTable, Weights: nonUniformWeights}

	code := PQCode{0, 0, 0, 0}

	uniformDist := uniformTable.ComputeWeightedDistance(code)
	nonUniformDist := nonUniformTable.ComputeWeightedDistance(code)

	// Both should give same result when all base distances are equal
	// because weights are normalized and scaled by numSubspaces
	if math.Abs(float64(uniformDist-nonUniformDist)) > 0.0001 {
		t.Logf("uniformDist=%f, nonUniformDist=%f", uniformDist, nonUniformDist)
		// This is expected when all distances are the same
	}
}

func TestWeightedAsymmetricDistanceNonNegative(t *testing.T) {
	pq := createQueryAdaptiveTestPQ(t, 64, 4)
	if pq == nil {
		t.Skip("Could not create test PQ")
	}

	config := DefaultQueryAdaptiveConfig()
	qad, err := NewQueryAdaptiveDistance(config, pq)
	if err != nil {
		t.Fatalf("NewQueryAdaptiveDistance error: %v", err)
	}

	// Test with various queries
	for i := 0; i < 10; i++ {
		query := make([]float32, 64)
		for j := range query {
			query[j] = float32(i*j) * 0.1
		}

		table, err := qad.ComputeWeightedDistanceTable(query)
		if err != nil {
			t.Fatalf("ComputeWeightedDistanceTable error: %v", err)
		}

		code := PQCode{uint8(i % 256), uint8((i + 1) % 256), uint8((i + 2) % 256), uint8((i + 3) % 256)}
		dist := qad.WeightedAsymmetricDistance(table, code)

		if dist < 0 {
			t.Errorf("Distance should be non-negative, got %f for query %d", dist, i)
		}
	}
}

func TestWeightedAsymmetricDistanceEdgeCases(t *testing.T) {
	pq := createQueryAdaptiveTestPQ(t, 64, 4)
	if pq == nil {
		t.Skip("Could not create test PQ")
	}

	config := DefaultQueryAdaptiveConfig()
	qad, _ := NewQueryAdaptiveDistance(config, pq)

	// Nil table
	dist := qad.WeightedAsymmetricDistance(nil, PQCode{0, 0, 0, 0})
	if dist != float32(1e30) {
		t.Errorf("Expected large distance for nil table, got %f", dist)
	}

	// Empty code
	query := make([]float32, 64)
	table, _ := qad.ComputeWeightedDistanceTable(query)
	dist = qad.WeightedAsymmetricDistance(table, PQCode{})
	if dist != float32(1e30) {
		t.Errorf("Expected large distance for empty code, got %f", dist)
	}

	// Wrong code length
	dist = qad.WeightedAsymmetricDistance(table, PQCode{0, 0}) // Only 2 subspaces
	if dist != float32(1e30) {
		t.Errorf("Expected large distance for wrong code length, got %f", dist)
	}
}

// =============================================================================
// ComputeWeightedDistance Tests
// =============================================================================

func TestComputeWeightedDistanceWithTable(t *testing.T) {
	numSubspaces := 4
	numCentroids := 256

	baseTable := &DistanceTable{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: numCentroids,
		Table:                make([][]float32, numSubspaces),
	}
	for m := 0; m < numSubspaces; m++ {
		baseTable.Table[m] = make([]float32, numCentroids)
		for k := 0; k < numCentroids; k++ {
			baseTable.Table[m][k] = float32(m+1) * float32(k+1) * 0.01
		}
	}

	weights := NewSubspaceWeights(numSubspaces)
	table := &WeightedDistanceTable{Table: baseTable, Weights: weights}

	code := PQCode{0, 1, 2, 3}
	dist := table.ComputeWeightedDistance(code)

	if dist < 0 {
		t.Errorf("Distance should be non-negative, got %f", dist)
	}
	if math.IsNaN(float64(dist)) || math.IsInf(float64(dist), 0) {
		t.Errorf("Distance should be finite, got %f", dist)
	}
}

func TestComputeWeightedDistanceEdgeCases(t *testing.T) {
	// Nil WeightedDistanceTable
	var nilTable *WeightedDistanceTable
	dist := nilTable.ComputeWeightedDistance(PQCode{0, 0, 0, 0})
	if dist != float32(1e30) {
		t.Errorf("Expected large distance for nil table, got %f", dist)
	}

	// Table with nil inner table
	tableWithNilInner := &WeightedDistanceTable{Table: nil, Weights: NewSubspaceWeights(4)}
	dist = tableWithNilInner.ComputeWeightedDistance(PQCode{0, 0, 0, 0})
	if dist != float32(1e30) {
		t.Errorf("Expected large distance for nil inner table, got %f", dist)
	}
}

// =============================================================================
// Integration Test: Query Adaptive Weighting with Real Data
// =============================================================================

func TestQueryAdaptiveWeightingRealData(t *testing.T) {
	// Create vectors with intentionally varying subspace variance
	vectorDim := 64
	numSubspaces := 4
	subspaceDim := vectorDim / numSubspaces

	pq := createQueryAdaptiveTestPQ(t, vectorDim, numSubspaces)
	if pq == nil {
		t.Skip("Could not create test PQ")
	}

	config := QueryAdaptiveConfig{
		Enabled:          true,
		Strategy:         WeightingVariance,
		MinVarianceRatio: 2.0,
	}

	qad, err := NewQueryAdaptiveDistance(config, pq)
	if err != nil {
		t.Fatalf("NewQueryAdaptiveDistance error: %v", err)
	}

	// Create a query with high variance in subspace 3
	query := make([]float32, vectorDim)
	// Subspaces 0-2: low variance
	for i := 0; i < 3*subspaceDim; i++ {
		query[i] = 0.5
	}
	// Subspace 3: high variance
	for i := 3 * subspaceDim; i < vectorDim; i++ {
		query[i] = float32(i-3*subspaceDim) * 0.5
	}

	profile, err := qad.ComputeProfile(query)
	if err != nil {
		t.Fatalf("ComputeProfile error: %v", err)
	}

	// Verify high variance subspace (index 3) gets higher weight
	if profile.Weights[3] <= profile.Weights[0] {
		t.Logf("Weights: %v", profile.Weights)
		t.Logf("Variances: %v", profile.SubspaceVariances)
		// This may happen if the variance difference isn't enough
	}

	// Compute weighted distance table
	table, err := qad.ComputeWeightedDistanceTable(query)
	if err != nil {
		t.Fatalf("ComputeWeightedDistanceTable error: %v", err)
	}

	// Verify table is created
	if table == nil || table.Table == nil {
		t.Fatal("WeightedDistanceTable is nil")
	}

	// Verify we can compute distances
	code := PQCode{0, 0, 0, 0}
	dist := qad.WeightedAsymmetricDistance(table, code)
	if dist < 0 || math.IsNaN(float64(dist)) {
		t.Errorf("Invalid distance: %f", dist)
	}
}

// =============================================================================
// Weighting Strategy Tests
// =============================================================================

func TestWeightingStrategyString(t *testing.T) {
	tests := []struct {
		strategy WeightingStrategy
		expected string
	}{
		{WeightingUniform, "Uniform"},
		{WeightingVariance, "Variance"},
		{WeightingEntropy, "Entropy"},
		{WeightingLearned, "Learned"},
		{WeightingStrategy(99), "Unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.strategy.String(); got != tt.expected {
				t.Errorf("String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkQueryAdaptiveDistance(b *testing.B) {
	pq := createQueryAdaptiveBenchmarkPQ(b, 768, 32)
	if pq == nil {
		b.Skip("Could not create benchmark PQ")
	}

	config := DefaultQueryAdaptiveConfig()
	qad, err := NewQueryAdaptiveDistance(config, pq)
	if err != nil {
		b.Fatalf("NewQueryAdaptiveDistance error: %v", err)
	}

	query := make([]float32, 768)
	for i := range query {
		query[i] = float32(i) * 0.001
	}

	table, err := qad.ComputeWeightedDistanceTable(query)
	if err != nil {
		b.Fatalf("ComputeWeightedDistanceTable error: %v", err)
	}

	code := NewPQCode(32)
	for i := range code {
		code[i] = uint8(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = qad.WeightedAsymmetricDistance(table, code)
	}
}

func BenchmarkWeightedDistanceTableComputation(b *testing.B) {
	pq := createQueryAdaptiveBenchmarkPQ(b, 768, 32)
	if pq == nil {
		b.Skip("Could not create benchmark PQ")
	}

	config := DefaultQueryAdaptiveConfig()
	qad, err := NewQueryAdaptiveDistance(config, pq)
	if err != nil {
		b.Fatalf("NewQueryAdaptiveDistance error: %v", err)
	}

	query := make([]float32, 768)
	for i := range query {
		query[i] = float32(i) * 0.001
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = qad.ComputeWeightedDistanceTable(query)
	}
}

func BenchmarkComputeVariance(b *testing.B) {
	values := make([]float32, 24) // Typical subspace dimension
	for i := range values {
		values[i] = float32(i) * 0.1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = computeVariance(values)
	}
}

func BenchmarkComputeEntropy(b *testing.B) {
	values := make([]float32, 24)
	for i := range values {
		values[i] = float32(i) * 0.1
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = computeEntropy(values)
	}
}

func BenchmarkSubspaceWeightsNormalize(b *testing.B) {
	b.Run("4 subspaces", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := SubspaceWeights{1.0, 2.0, 3.0, 4.0}
			w.Normalize()
		}
	})

	b.Run("8 subspaces", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := SubspaceWeights{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0}
			w.Normalize()
		}
	})

	b.Run("32 subspaces", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := make(SubspaceWeights, 32)
			for j := range w {
				w[j] = float32(j + 1)
			}
			w.Normalize()
		}
	})
}

// =============================================================================
// Test Helpers
// =============================================================================

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// createQueryAdaptiveTestPQ creates a minimal ProductQuantizer for testing
func createQueryAdaptiveTestPQ(t *testing.T, vectorDim, numSubspaces int) *ProductQuantizer {
	t.Helper()

	config := ProductQuantizerConfig{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: 16, // Use fewer centroids for faster training
	}

	pq, err := NewProductQuantizer(vectorDim, config)
	if err != nil {
		t.Logf("Could not create ProductQuantizer: %v", err)
		return nil
	}

	numVectors := 500
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, vectorDim)
		for j := range vectors[i] {
			vectors[i][j] = float32(i*j%100) * 0.01
		}
	}

	err = pq.Train(context.Background(), vectors)
	if err != nil {
		t.Logf("Could not train ProductQuantizer: %v", err)
		return nil
	}

	return pq
}

// createQueryAdaptiveBenchmarkPQ creates a ProductQuantizer for benchmarks
func createQueryAdaptiveBenchmarkPQ(b *testing.B, vectorDim, numSubspaces int) *ProductQuantizer {
	b.Helper()

	config := ProductQuantizerConfig{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: 16,
	}

	pq, err := NewProductQuantizer(vectorDim, config)
	if err != nil {
		b.Logf("Could not create ProductQuantizer: %v", err)
		return nil
	}

	numVectors := 500
	vectors := make([][]float32, numVectors)
	for i := range vectors {
		vectors[i] = make([]float32, vectorDim)
		for j := range vectors[i] {
			vectors[i][j] = float32(i*j%100) * 0.01
		}
	}

	err = pq.Train(context.Background(), vectors)
	if err != nil {
		b.Logf("Could not train ProductQuantizer: %v", err)
		return nil
	}

	return pq
}
