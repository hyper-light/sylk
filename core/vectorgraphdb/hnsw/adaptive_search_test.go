package hnsw

import (
	"math"
	"testing"
)

func TestNewAdaptiveSearchConfig(t *testing.T) {
	tests := []struct {
		name                      string
		efSearch                  int
		expectedMinCandidates     int
		expectedPatienceThreshold int
		expectedImprovementRatio  float64
	}{
		{
			name:                      "default efSearch 50",
			efSearch:                  50,
			expectedMinCandidates:     10,
			expectedPatienceThreshold: 3,
			expectedImprovementRatio:  0.001,
		},
		{
			name:                      "large efSearch 200",
			efSearch:                  200,
			expectedMinCandidates:     20,
			expectedPatienceThreshold: 10,
			expectedImprovementRatio:  0.001,
		},
		{
			name:                      "small efSearch 10",
			efSearch:                  10,
			expectedMinCandidates:     10,
			expectedPatienceThreshold: 3,
			expectedImprovementRatio:  0.001,
		},
		{
			name:                      "very large efSearch 1000",
			efSearch:                  1000,
			expectedMinCandidates:     100,
			expectedPatienceThreshold: 50,
			expectedImprovementRatio:  0.001,
		},
		{
			name:                      "zero efSearch uses minimum",
			efSearch:                  0,
			expectedMinCandidates:     10,
			expectedPatienceThreshold: 3,
			expectedImprovementRatio:  0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewAdaptiveSearchConfig(tt.efSearch)

			if cfg.MinCandidates != tt.expectedMinCandidates {
				t.Errorf("MinCandidates = %d, want %d", cfg.MinCandidates, tt.expectedMinCandidates)
			}
			if cfg.PatienceThreshold != tt.expectedPatienceThreshold {
				t.Errorf("PatienceThreshold = %d, want %d", cfg.PatienceThreshold, tt.expectedPatienceThreshold)
			}
			if cfg.ImprovementRatio != tt.expectedImprovementRatio {
				t.Errorf("ImprovementRatio = %f, want %f", cfg.ImprovementRatio, tt.expectedImprovementRatio)
			}
		})
	}
}

func TestAdaptiveSearchConfig_ShouldTerminate_BelowMinCandidates(t *testing.T) {
	cfg := NewAdaptiveSearchConfig(50)

	tests := []struct {
		name           string
		stableCount    int
		candidateCount int
		expected       bool
	}{
		{
			name:           "zero candidates",
			stableCount:    10,
			candidateCount: 0,
			expected:       false,
		},
		{
			name:           "below min candidates",
			stableCount:    10,
			candidateCount: 5,
			expected:       false,
		},
		{
			name:           "exactly at min minus one",
			stableCount:    10,
			candidateCount: 9,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cfg.ShouldTerminate(tt.stableCount, tt.candidateCount, 0.9, 0.8)
			if result != tt.expected {
				t.Errorf("ShouldTerminate() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestAdaptiveSearchConfig_ShouldTerminate_PatienceThreshold(t *testing.T) {
	cfg := NewAdaptiveSearchConfig(50)

	tests := []struct {
		name           string
		stableCount    int
		candidateCount int
		expected       bool
	}{
		{
			name:           "stable count below threshold",
			stableCount:    2,
			candidateCount: 20,
			expected:       false,
		},
		{
			name:           "stable count at threshold",
			stableCount:    3,
			candidateCount: 20,
			expected:       true,
		},
		{
			name:           "stable count above threshold",
			stableCount:    10,
			candidateCount: 20,
			expected:       true,
		},
		{
			name:           "exactly at min candidates and threshold",
			stableCount:    3,
			candidateCount: 10,
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cfg.ShouldTerminate(tt.stableCount, tt.candidateCount, 0.9, 0.8)
			if result != tt.expected {
				t.Errorf("ShouldTerminate() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestAdaptiveSearchConfig_ComputeImprovement(t *testing.T) {
	cfg := NewAdaptiveSearchConfig(50)

	tests := []struct {
		name        string
		currentBest float64
		lastBest    float64
		expected    float64
	}{
		{
			name:        "positive improvement",
			currentBest: 0.9,
			lastBest:    0.8,
			expected:    0.125,
		},
		{
			name:        "no improvement",
			currentBest: 0.8,
			lastBest:    0.8,
			expected:    0.0,
		},
		{
			name:        "negative improvement",
			currentBest: 0.7,
			lastBest:    0.8,
			expected:    -0.125,
		},
		{
			name:        "zero lastBest returns 1.0",
			currentBest: 0.5,
			lastBest:    0.0,
			expected:    1.0,
		},
		{
			name:        "negative lastBest returns 1.0",
			currentBest: 0.5,
			lastBest:    -0.1,
			expected:    1.0,
		},
		{
			name:        "very small lastBest uses floor",
			currentBest: 0.002,
			lastBest:    0.0005,
			expected:    1.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cfg.ComputeImprovement(tt.currentBest, tt.lastBest)
			if math.Abs(result-tt.expected) > 1e-9 {
				t.Errorf("ComputeImprovement() = %f, want %f", result, tt.expected)
			}
		})
	}
}

func TestAdaptiveSearchConfig_IsSignificantImprovement(t *testing.T) {
	cfg := NewAdaptiveSearchConfig(50)

	tests := []struct {
		name        string
		improvement float64
		expected    bool
	}{
		{
			name:        "above threshold",
			improvement: 0.01,
			expected:    true,
		},
		{
			name:        "at threshold",
			improvement: 0.001,
			expected:    true,
		},
		{
			name:        "below threshold",
			improvement: 0.0009,
			expected:    false,
		},
		{
			name:        "zero improvement",
			improvement: 0.0,
			expected:    false,
		},
		{
			name:        "negative improvement",
			improvement: -0.01,
			expected:    false,
		},
		{
			name:        "large improvement",
			improvement: 1.0,
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cfg.IsSignificantImprovement(tt.improvement)
			if result != tt.expected {
				t.Errorf("IsSignificantImprovement(%f) = %v, want %v", tt.improvement, result, tt.expected)
			}
		})
	}
}

func TestAdaptiveSearchConfig_DerivedValuesFromEfSearch(t *testing.T) {
	efValues := []int{10, 25, 50, 100, 200, 500, 1000}

	for _, ef := range efValues {
		cfg := NewAdaptiveSearchConfig(ef)

		expectedMin := max(10, ef/10)
		if cfg.MinCandidates != expectedMin {
			t.Errorf("efSearch=%d: MinCandidates = %d, want %d", ef, cfg.MinCandidates, expectedMin)
		}

		expectedPatience := max(3, ef/20)
		if cfg.PatienceThreshold != expectedPatience {
			t.Errorf("efSearch=%d: PatienceThreshold = %d, want %d", ef, cfg.PatienceThreshold, expectedPatience)
		}
	}
}
