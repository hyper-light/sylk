package quantization

import (
	"context"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// CentroidID Tests
// =============================================================================

func TestCentroidID_String(t *testing.T) {
	tests := []struct {
		name     string
		id       CentroidID
		expected string
	}{
		{
			name:     "global codebook",
			id:       CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 5},
			expected: "global:sub0:c5",
		},
		{
			name:     "lopq codebook",
			id:       CentroidID{CodebookType: "lopq_123", SubspaceIndex: 3, CentroidIndex: 42},
			expected: "lopq_123:sub3:c42",
		},
		{
			name:     "empty codebook type",
			id:       CentroidID{CodebookType: "", SubspaceIndex: 1, CentroidIndex: 2},
			expected: ":sub1:c2",
		},
		{
			name:     "large indices",
			id:       CentroidID{CodebookType: "test", SubspaceIndex: 255, CentroidIndex: 999},
			expected: "test:sub255:c999",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.id.String()
			if result != tc.expected {
				t.Errorf("String() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestCentroidID_Equal(t *testing.T) {
	tests := []struct {
		name     string
		a        CentroidID
		b        CentroidID
		expected bool
	}{
		{
			name:     "identical",
			a:        CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 5},
			b:        CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 5},
			expected: true,
		},
		{
			name:     "different codebook type",
			a:        CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 5},
			b:        CentroidID{CodebookType: "lopq_1", SubspaceIndex: 1, CentroidIndex: 5},
			expected: false,
		},
		{
			name:     "different subspace index",
			a:        CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 5},
			b:        CentroidID{CodebookType: "global", SubspaceIndex: 2, CentroidIndex: 5},
			expected: false,
		},
		{
			name:     "different centroid index",
			a:        CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 5},
			b:        CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 6},
			expected: false,
		},
		{
			name:     "zero values",
			a:        CentroidID{},
			b:        CentroidID{},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.a.Equal(tc.b)
			if result != tc.expected {
				t.Errorf("Equal() = %v, want %v", result, tc.expected)
			}
			// Symmetry check
			reverse := tc.b.Equal(tc.a)
			if reverse != tc.expected {
				t.Errorf("Equal() not symmetric: a.Equal(b)=%v, b.Equal(a)=%v", result, reverse)
			}
		})
	}
}

// =============================================================================
// CentroidUtilization Tests
// =============================================================================

func TestCentroidUtilization_IsDead(t *testing.T) {
	tests := []struct {
		name      string
		count     int64
		threshold int64
		expected  bool
	}{
		{
			name:      "below threshold",
			count:     3,
			threshold: 5,
			expected:  true,
		},
		{
			name:      "at threshold",
			count:     5,
			threshold: 5,
			expected:  false,
		},
		{
			name:      "above threshold",
			count:     10,
			threshold: 5,
			expected:  false,
		},
		{
			name:      "zero count",
			count:     0,
			threshold: 5,
			expected:  true,
		},
		{
			name:      "zero threshold",
			count:     0,
			threshold: 0,
			expected:  false,
		},
		{
			name:      "count exactly one below threshold",
			count:     4,
			threshold: 5,
			expected:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := CentroidUtilization{
				ID:    CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 0},
				Count: tc.count,
			}
			result := u.IsDead(tc.threshold)
			if result != tc.expected {
				t.Errorf("IsDead(%d) with count=%d: got %v, want %v",
					tc.threshold, tc.count, result, tc.expected)
			}
		})
	}
}

func TestCentroidUtilization_IsOverloaded(t *testing.T) {
	tests := []struct {
		name      string
		count     int64
		threshold int64
		expected  bool
	}{
		{
			name:      "below threshold",
			count:     5000,
			threshold: 10000,
			expected:  false,
		},
		{
			name:      "at threshold",
			count:     10000,
			threshold: 10000,
			expected:  false,
		},
		{
			name:      "above threshold",
			count:     15000,
			threshold: 10000,
			expected:  true,
		},
		{
			name:      "exactly one above threshold",
			count:     10001,
			threshold: 10000,
			expected:  true,
		},
		{
			name:      "zero count zero threshold",
			count:     0,
			threshold: 0,
			expected:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := CentroidUtilization{
				ID:    CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 0},
				Count: tc.count,
			}
			result := u.IsOverloaded(tc.threshold)
			if result != tc.expected {
				t.Errorf("IsOverloaded(%d) with count=%d: got %v, want %v",
					tc.threshold, tc.count, result, tc.expected)
			}
		})
	}
}

func TestCentroidUtilization_String(t *testing.T) {
	now := time.Date(2025, 1, 20, 12, 0, 0, 0, time.UTC)
	u := CentroidUtilization{
		ID:          CentroidID{CodebookType: "global", SubspaceIndex: 2, CentroidIndex: 15},
		Count:       1234,
		LastUpdated: now,
	}

	result := u.String()

	// Check that the string contains key components
	if result == "" {
		t.Error("String() returned empty string")
	}
	expectedSubstrings := []string{"global:sub2:c15", "1234", "2025-01-20"}
	for _, sub := range expectedSubstrings {
		if !containsStr(result, sub) {
			t.Errorf("String() = %q, expected to contain %q", result, sub)
		}
	}
}

// =============================================================================
// AdaptationConfig Tests
// =============================================================================

func TestDefaultAdaptationConfig(t *testing.T) {
	config := DefaultAdaptationConfig()

	// Verify default values as documented
	if config.DeathThreshold != 5 {
		t.Errorf("DeathThreshold = %d, want 5", config.DeathThreshold)
	}
	if config.BirthThreshold != 10000 {
		t.Errorf("BirthThreshold = %d, want 10000", config.BirthThreshold)
	}
	if config.CheckInterval != time.Minute {
		t.Errorf("CheckInterval = %v, want 1 minute", config.CheckInterval)
	}
	if config.MinCentroidsPerSubspace != 16 {
		t.Errorf("MinCentroidsPerSubspace = %d, want 16", config.MinCentroidsPerSubspace)
	}
	if config.MaxCentroidsPerSubspace != 256 {
		t.Errorf("MaxCentroidsPerSubspace = %d, want 256", config.MaxCentroidsPerSubspace)
	}
	if !config.Enabled {
		t.Error("Enabled = false, want true")
	}

	// Default config should be valid
	if err := config.Validate(); err != nil {
		t.Errorf("DefaultAdaptationConfig() failed validation: %v", err)
	}
}

func TestAdaptationConfig_Validate(t *testing.T) {
	validConfig := DefaultAdaptationConfig()

	tests := []struct {
		name      string
		modify    func(*AdaptationConfig)
		expectErr error
	}{
		{
			name:      "valid default config",
			modify:    func(c *AdaptationConfig) {},
			expectErr: nil,
		},
		{
			name: "negative death threshold",
			modify: func(c *AdaptationConfig) {
				c.DeathThreshold = -1
			},
			expectErr: ErrAdaptationInvalidDeathThreshold,
		},
		{
			name: "zero death threshold is valid",
			modify: func(c *AdaptationConfig) {
				c.DeathThreshold = 0
			},
			expectErr: nil,
		},
		{
			name: "birth threshold equal to death threshold",
			modify: func(c *AdaptationConfig) {
				c.DeathThreshold = 100
				c.BirthThreshold = 100
			},
			expectErr: ErrAdaptationInvalidBirthThreshold,
		},
		{
			name: "birth threshold less than death threshold",
			modify: func(c *AdaptationConfig) {
				c.DeathThreshold = 100
				c.BirthThreshold = 50
			},
			expectErr: ErrAdaptationInvalidBirthThreshold,
		},
		{
			name: "check interval too short",
			modify: func(c *AdaptationConfig) {
				c.CheckInterval = 500 * time.Millisecond
			},
			expectErr: ErrAdaptationInvalidCheckInterval,
		},
		{
			name: "check interval exactly 1 second is valid",
			modify: func(c *AdaptationConfig) {
				c.CheckInterval = time.Second
			},
			expectErr: nil,
		},
		{
			name: "zero min centroids",
			modify: func(c *AdaptationConfig) {
				c.MinCentroidsPerSubspace = 0
			},
			expectErr: ErrAdaptationInvalidMinCentroids,
		},
		{
			name: "negative min centroids",
			modify: func(c *AdaptationConfig) {
				c.MinCentroidsPerSubspace = -1
			},
			expectErr: ErrAdaptationInvalidMinCentroids,
		},
		{
			name: "max centroids less than min",
			modify: func(c *AdaptationConfig) {
				c.MinCentroidsPerSubspace = 100
				c.MaxCentroidsPerSubspace = 50
			},
			expectErr: ErrAdaptationInvalidMaxCentroids,
		},
		{
			name: "max centroids equal to min is valid",
			modify: func(c *AdaptationConfig) {
				c.MinCentroidsPerSubspace = 100
				c.MaxCentroidsPerSubspace = 100
			},
			expectErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := validConfig
			tc.modify(&config)
			err := config.Validate()

			if tc.expectErr == nil {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
			} else {
				if err != tc.expectErr {
					t.Errorf("Validate() = %v, want %v", err, tc.expectErr)
				}
			}
		})
	}
}

func TestAdaptationConfig_String(t *testing.T) {
	config := AdaptationConfig{
		DeathThreshold:          10,
		BirthThreshold:          5000,
		CheckInterval:           2 * time.Minute,
		MinCentroidsPerSubspace: 32,
		MaxCentroidsPerSubspace: 128,
		Enabled:                 true,
	}

	result := config.String()
	expectedSubstrings := []string{"Death:10", "Birth:5000", "Enabled:true"}
	for _, sub := range expectedSubstrings {
		if !containsStr(result, sub) {
			t.Errorf("String() = %q, expected to contain %q", result, sub)
		}
	}
}

// =============================================================================
// percentile Tests
// =============================================================================

func TestPercentile(t *testing.T) {
	tests := []struct {
		name     string
		data     []int64
		p        float64
		expected int64
	}{
		{
			name:     "empty slice returns 0",
			data:     []int64{},
			p:        0.5,
			expected: 0,
		},
		{
			name:     "single element - 0th percentile",
			data:     []int64{42},
			p:        0.0,
			expected: 42,
		},
		{
			name:     "single element - 50th percentile",
			data:     []int64{42},
			p:        0.5,
			expected: 42,
		},
		{
			name:     "single element - 100th percentile",
			data:     []int64{42},
			p:        1.0,
			expected: 42,
		},
		{
			name:     "0th percentile is min",
			data:     []int64{10, 20, 30, 40, 50},
			p:        0.0,
			expected: 10,
		},
		{
			name:     "100th percentile is max",
			data:     []int64{10, 20, 30, 40, 50},
			p:        1.0,
			expected: 50,
		},
		{
			name:     "50th percentile is median (odd count)",
			data:     []int64{10, 20, 30, 40, 50},
			p:        0.5,
			expected: 30,
		},
		{
			name:     "50th percentile interpolates (even count)",
			data:     []int64{10, 20, 30, 40},
			p:        0.5,
			expected: 25, // interpolates between 20 and 30
		},
		{
			name:     "all same values",
			data:     []int64{100, 100, 100, 100},
			p:        0.5,
			expected: 100,
		},
		{
			name:     "5th percentile",
			data:     []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
			p:        0.05,
			expected: 1, // 0.05 * 19 = 0.95, interpolates between index 0 and 1
		},
		{
			name:     "95th percentile",
			data:     []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
			p:        0.95,
			expected: 19, // 0.95 * 19 = 18.05, interpolates between index 18 and 19
		},
		{
			name:     "unsorted input still works",
			data:     []int64{50, 10, 40, 20, 30},
			p:        0.5,
			expected: 30,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := percentile(tc.data, tc.p)
			if result != tc.expected {
				t.Errorf("percentile(%v, %.2f) = %d, want %d", tc.data, tc.p, result, tc.expected)
			}
		})
	}
}

func TestPercentile_DoesNotModifyInput(t *testing.T) {
	original := []int64{50, 10, 40, 20, 30}
	data := make([]int64, len(original))
	copy(data, original)

	_ = percentile(data, 0.5)

	for i := range data {
		if data[i] != original[i] {
			t.Errorf("percentile modified input: data[%d] = %d, original = %d",
				i, data[i], original[i])
		}
	}
}

// =============================================================================
// DeriveAdaptationConfig Tests
// =============================================================================

func TestDeriveAdaptationConfig_EmptySlice(t *testing.T) {
	config := DeriveAdaptationConfig([]int64{})
	defaultConfig := DefaultAdaptationConfig()

	if config.DeathThreshold != defaultConfig.DeathThreshold {
		t.Errorf("DeathThreshold = %d, want %d (default)", config.DeathThreshold, defaultConfig.DeathThreshold)
	}
	if config.BirthThreshold != defaultConfig.BirthThreshold {
		t.Errorf("BirthThreshold = %d, want %d (default)", config.BirthThreshold, defaultConfig.BirthThreshold)
	}
}

func TestDeriveAdaptationConfig_SingleValue(t *testing.T) {
	config := DeriveAdaptationConfig([]int64{100})

	// Death threshold should be minimum of p5 and 1
	if config.DeathThreshold < 1 {
		t.Errorf("DeathThreshold = %d, want >= 1", config.DeathThreshold)
	}

	// Birth threshold should be greater than death threshold
	if config.BirthThreshold <= config.DeathThreshold {
		t.Errorf("BirthThreshold (%d) must be > DeathThreshold (%d)",
			config.BirthThreshold, config.DeathThreshold)
	}
}

func TestDeriveAdaptationConfig_Percentiles(t *testing.T) {
	// Create a dataset where percentiles are predictable
	// 100 values from 1 to 100
	sizes := make([]int64, 100)
	for i := 0; i < 100; i++ {
		sizes[i] = int64(i + 1)
	}

	config := DeriveAdaptationConfig(sizes)

	// 5th percentile of 1-100 should be around 5-6
	if config.DeathThreshold < 1 || config.DeathThreshold > 10 {
		t.Errorf("DeathThreshold = %d, expected in range [1, 10] for p5", config.DeathThreshold)
	}

	// 95th percentile of 1-100 is ~95, so BirthThreshold should be ~190
	expectedBirthMin := int64(150)
	expectedBirthMax := int64(250)
	if config.BirthThreshold < expectedBirthMin || config.BirthThreshold > expectedBirthMax {
		t.Errorf("BirthThreshold = %d, expected in range [%d, %d] for 2x p95",
			config.BirthThreshold, expectedBirthMin, expectedBirthMax)
	}
}

func TestDeriveAdaptationConfig_MinCentroidsFromNonEmpty(t *testing.T) {
	// 20 non-empty clusters
	sizes := make([]int64, 20)
	for i := 0; i < 20; i++ {
		sizes[i] = int64(100 + i*10)
	}

	config := DeriveAdaptationConfig(sizes)

	// MinCentroidsPerSubspace should be at least 20 (non-empty count)
	if config.MinCentroidsPerSubspace < 20 {
		t.Errorf("MinCentroidsPerSubspace = %d, expected >= 20 (non-empty count)",
			config.MinCentroidsPerSubspace)
	}
}

func TestDeriveAdaptationConfig_MinCentroidsFloor(t *testing.T) {
	// Only 3 non-empty clusters (below floor of 8)
	sizes := []int64{100, 200, 300}

	config := DeriveAdaptationConfig(sizes)

	// MinCentroidsPerSubspace should be floored at 8
	if config.MinCentroidsPerSubspace < 8 {
		t.Errorf("MinCentroidsPerSubspace = %d, expected >= 8 (floor)",
			config.MinCentroidsPerSubspace)
	}
}

func TestDeriveAdaptationConfig_DeathThresholdMinimumOne(t *testing.T) {
	// All zeros - p5 would be 0, but should be clamped to 1
	sizes := []int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}

	config := DeriveAdaptationConfig(sizes)

	if config.DeathThreshold < 1 {
		t.Errorf("DeathThreshold = %d, expected >= 1 (minimum)", config.DeathThreshold)
	}
}

func TestDeriveAdaptationConfig_Determinism(t *testing.T) {
	sizes := []int64{5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 100, 200, 500, 1000, 5000}

	// Run multiple times and ensure identical output
	config1 := DeriveAdaptationConfig(sizes)
	config2 := DeriveAdaptationConfig(sizes)
	config3 := DeriveAdaptationConfig(sizes)

	if config1.DeathThreshold != config2.DeathThreshold || config2.DeathThreshold != config3.DeathThreshold {
		t.Errorf("DeathThreshold not deterministic: %d, %d, %d",
			config1.DeathThreshold, config2.DeathThreshold, config3.DeathThreshold)
	}
	if config1.BirthThreshold != config2.BirthThreshold || config2.BirthThreshold != config3.BirthThreshold {
		t.Errorf("BirthThreshold not deterministic: %d, %d, %d",
			config1.BirthThreshold, config2.BirthThreshold, config3.BirthThreshold)
	}
	if config1.MinCentroidsPerSubspace != config2.MinCentroidsPerSubspace || config2.MinCentroidsPerSubspace != config3.MinCentroidsPerSubspace {
		t.Errorf("MinCentroidsPerSubspace not deterministic: %d, %d, %d",
			config1.MinCentroidsPerSubspace, config2.MinCentroidsPerSubspace, config3.MinCentroidsPerSubspace)
	}
}

func TestDeriveAdaptationConfig_BirthAlwaysGreaterThanDeath(t *testing.T) {
	testCases := [][]int64{
		{1},
		{1, 1, 1, 1},
		{0, 0, 1, 1},
		{1, 2},
		{1, 1, 1, 1, 1, 1, 1, 1, 1, 100},
	}

	for i, sizes := range testCases {
		config := DeriveAdaptationConfig(sizes)
		if config.BirthThreshold <= config.DeathThreshold {
			t.Errorf("Case %d: BirthThreshold (%d) <= DeathThreshold (%d) for sizes=%v",
				i, config.BirthThreshold, config.DeathThreshold, sizes)
		}
	}
}

func TestDeriveAdaptationConfig_Validation(t *testing.T) {
	// Any derived config should pass validation
	testCases := [][]int64{
		{},
		{1},
		{100, 200, 300},
		{0, 0, 0, 100, 200},
	}

	for i, sizes := range testCases {
		config := DeriveAdaptationConfig(sizes)
		if err := config.Validate(); err != nil {
			t.Errorf("Case %d: DeriveAdaptationConfig(%v) produced invalid config: %v",
				i, sizes, err)
		}
	}
}

// =============================================================================
// DeathEvent Tests
// =============================================================================

func TestDeathEvent_Fields(t *testing.T) {
	now := time.Now()
	centroid := CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 5}
	replacement := CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 6}

	event := DeathEvent{
		Centroid:            centroid,
		FinalCount:          3,
		Threshold:           5,
		OccurredAt:          now,
		ReplacementCentroid: replacement,
	}

	if !event.Centroid.Equal(centroid) {
		t.Error("Centroid field not set correctly")
	}
	if event.FinalCount != 3 {
		t.Errorf("FinalCount = %d, want 3", event.FinalCount)
	}
	if event.Threshold != 5 {
		t.Errorf("Threshold = %d, want 5", event.Threshold)
	}
	if !event.OccurredAt.Equal(now) {
		t.Errorf("OccurredAt = %v, want %v", event.OccurredAt, now)
	}
	if !event.ReplacementCentroid.Equal(replacement) {
		t.Error("ReplacementCentroid field not set correctly")
	}
}

func TestDeathEvent_String(t *testing.T) {
	event := DeathEvent{
		Centroid:   CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 10},
		FinalCount: 2,
		Threshold:  5,
		OccurredAt: time.Date(2025, 1, 20, 15, 30, 0, 0, time.UTC),
	}

	result := event.String()
	expectedSubstrings := []string{"Death", "global:sub0:c10", "Count:2", "Thresh:5"}
	for _, sub := range expectedSubstrings {
		if !containsStr(result, sub) {
			t.Errorf("String() = %q, expected to contain %q", result, sub)
		}
	}
}

// =============================================================================
// BirthEvent Tests
// =============================================================================

func TestBirthEvent_Fields(t *testing.T) {
	now := time.Now()
	parent := CentroidID{CodebookType: "global", SubspaceIndex: 2, CentroidIndex: 10}
	child := CentroidID{CodebookType: "global", SubspaceIndex: 2, CentroidIndex: 256}

	event := BirthEvent{
		ParentCentroid:    parent,
		ChildCentroid:     child,
		ParentCountBefore: 15000,
		Threshold:         10000,
		OccurredAt:        now,
	}

	if !event.ParentCentroid.Equal(parent) {
		t.Error("ParentCentroid field not set correctly")
	}
	if !event.ChildCentroid.Equal(child) {
		t.Error("ChildCentroid field not set correctly")
	}
	if event.ParentCountBefore != 15000 {
		t.Errorf("ParentCountBefore = %d, want 15000", event.ParentCountBefore)
	}
	if event.Threshold != 10000 {
		t.Errorf("Threshold = %d, want 10000", event.Threshold)
	}
}

func TestBirthEvent_String(t *testing.T) {
	event := BirthEvent{
		ParentCentroid:    CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 5},
		ChildCentroid:     CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 128},
		ParentCountBefore: 12000,
		Threshold:         10000,
		OccurredAt:        time.Date(2025, 1, 20, 16, 0, 0, 0, time.UTC),
	}

	result := event.String()
	expectedSubstrings := []string{"Birth", "Parent:", "Child:", "12000"}
	for _, sub := range expectedSubstrings {
		if !containsStr(result, sub) {
			t.Errorf("String() = %q, expected to contain %q", result, sub)
		}
	}
}

func TestBirthEvent_ParentChildRelationship(t *testing.T) {
	parent := CentroidID{CodebookType: "global", SubspaceIndex: 3, CentroidIndex: 50}
	child := CentroidID{CodebookType: "global", SubspaceIndex: 3, CentroidIndex: 100}

	event := BirthEvent{
		ParentCentroid:    parent,
		ChildCentroid:     child,
		ParentCountBefore: 20000,
		Threshold:         10000,
	}

	// Parent and child should be in the same subspace
	if event.ParentCentroid.SubspaceIndex != event.ChildCentroid.SubspaceIndex {
		t.Errorf("Parent subspace (%d) != child subspace (%d)",
			event.ParentCentroid.SubspaceIndex, event.ChildCentroid.SubspaceIndex)
	}

	// Parent and child should have same codebook type
	if event.ParentCentroid.CodebookType != event.ChildCentroid.CodebookType {
		t.Errorf("Parent codebook (%s) != child codebook (%s)",
			event.ParentCentroid.CodebookType, event.ChildCentroid.CodebookType)
	}

	// Parent and child should have different centroid indices
	if event.ParentCentroid.CentroidIndex == event.ChildCentroid.CentroidIndex {
		t.Error("Parent and child have same centroid index")
	}
}

// =============================================================================
// AdaptationEventType Tests
// =============================================================================

func TestAdaptationEventType_String(t *testing.T) {
	tests := []struct {
		eventType AdaptationEventType
		expected  string
	}{
		{AdaptationEventDeath, "Death"},
		{AdaptationEventBirth, "Birth"},
		{AdaptationEventMerge, "Merge"},
		{AdaptationEventType(999), "Unknown(999)"},
	}

	for _, tc := range tests {
		result := tc.eventType.String()
		if result != tc.expected {
			t.Errorf("AdaptationEventType(%d).String() = %q, want %q",
				tc.eventType, result, tc.expected)
		}
	}
}

// =============================================================================
// AdaptationStats Tests
// =============================================================================

func TestAdaptationStats_String(t *testing.T) {
	stats := AdaptationStats{
		TotalCentroids:      1000,
		DeadCentroids:       5,
		OverloadedCentroids: 3,
		TotalDeaths:         10,
		TotalBirths:         8,
		LastCheckAt:         time.Now(),
	}

	result := stats.String()
	expectedSubstrings := []string{"Total:1000", "Dead:5", "Overloaded:3", "Deaths:10", "Births:8"}
	for _, sub := range expectedSubstrings {
		if !containsStr(result, sub) {
			t.Errorf("String() = %q, expected to contain %q", result, sub)
		}
	}
}

// =============================================================================
// Mock AdaptationTracker for testing
// =============================================================================

type mockAdaptationTracker struct {
	mu             sync.RWMutex
	utilizations   map[string]CentroidUtilization
	deaths         []DeathEvent
	births         []BirthEvent
	deathThreshold int64
	birthThreshold int64
}

func newMockTracker(deathThreshold, birthThreshold int64) *mockAdaptationTracker {
	return &mockAdaptationTracker{
		utilizations:   make(map[string]CentroidUtilization),
		deaths:         make([]DeathEvent, 0),
		births:         make([]BirthEvent, 0),
		deathThreshold: deathThreshold,
		birthThreshold: birthThreshold,
	}
}

func (m *mockAdaptationTracker) key(id CentroidID) string {
	return id.String()
}

func (m *mockAdaptationTracker) Increment(id CentroidID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(id)
	u := m.utilizations[k]
	u.ID = id
	u.Count++
	u.LastUpdated = time.Now()
	m.utilizations[k] = u
	return nil
}

func (m *mockAdaptationTracker) Decrement(id CentroidID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(id)
	u := m.utilizations[k]
	u.ID = id
	if u.Count > 0 {
		u.Count--
	}
	u.LastUpdated = time.Now()
	m.utilizations[k] = u
	return nil
}

func (m *mockAdaptationTracker) GetUtilization(id CentroidID) (CentroidUtilization, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.utilizations[m.key(id)]
	if !ok {
		return CentroidUtilization{ID: id}, nil
	}
	return u, nil
}

func (m *mockAdaptationTracker) GetDeadCentroids(codebookType string) ([]CentroidID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var dead []CentroidID
	for _, u := range m.utilizations {
		if u.ID.CodebookType == codebookType && u.IsDead(m.deathThreshold) {
			dead = append(dead, u.ID)
		}
	}
	return dead, nil
}

func (m *mockAdaptationTracker) GetOverloadedCentroids(codebookType string) ([]CentroidID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var overloaded []CentroidID
	for _, u := range m.utilizations {
		if u.ID.CodebookType == codebookType && u.IsOverloaded(m.birthThreshold) {
			overloaded = append(overloaded, u.ID)
		}
	}
	return overloaded, nil
}

func (m *mockAdaptationTracker) RecordDeath(event DeathEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deaths = append(m.deaths, event)
	return nil
}

func (m *mockAdaptationTracker) RecordBirth(event BirthEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.births = append(m.births, event)
	return nil
}

func (m *mockAdaptationTracker) GetRecentEvents(since time.Time) ([]DeathEvent, []BirthEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var deaths []DeathEvent
	var births []BirthEvent
	for _, d := range m.deaths {
		if d.OccurredAt.After(since) {
			deaths = append(deaths, d)
		}
	}
	for _, b := range m.births {
		if b.OccurredAt.After(since) {
			births = append(births, b)
		}
	}
	return deaths, births, nil
}

func (m *mockAdaptationTracker) GetStats() AdaptationStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var dead, overloaded int
	for _, u := range m.utilizations {
		if u.IsDead(m.deathThreshold) {
			dead++
		}
		if u.IsOverloaded(m.birthThreshold) {
			overloaded++
		}
	}
	return AdaptationStats{
		TotalCentroids:      len(m.utilizations),
		DeadCentroids:       dead,
		OverloadedCentroids: overloaded,
		TotalDeaths:         int64(len(m.deaths)),
		TotalBirths:         int64(len(m.births)),
		LastCheckAt:         time.Now(),
	}
}

// =============================================================================
// AdaptationTracker Tests (using mock)
// =============================================================================

func TestAdaptationTracker_IncrementDecrement(t *testing.T) {
	tracker := newMockTracker(5, 10000)
	id := CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 1}

	// Initial count should be 0
	u, err := tracker.GetUtilization(id)
	if err != nil {
		t.Fatalf("GetUtilization: %v", err)
	}
	if u.Count != 0 {
		t.Errorf("Initial count = %d, want 0", u.Count)
	}

	// Increment 10 times
	for i := 0; i < 10; i++ {
		if err := tracker.Increment(id); err != nil {
			t.Fatalf("Increment: %v", err)
		}
	}

	u, _ = tracker.GetUtilization(id)
	if u.Count != 10 {
		t.Errorf("Count after 10 increments = %d, want 10", u.Count)
	}

	// Decrement 3 times
	for i := 0; i < 3; i++ {
		if err := tracker.Decrement(id); err != nil {
			t.Fatalf("Decrement: %v", err)
		}
	}

	u, _ = tracker.GetUtilization(id)
	if u.Count != 7 {
		t.Errorf("Count after 3 decrements = %d, want 7", u.Count)
	}
}

func TestAdaptationTracker_GetDeadCentroids(t *testing.T) {
	tracker := newMockTracker(5, 10000)

	// Create some centroids with varying utilization
	ids := []CentroidID{
		{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 0}, // will be dead (count=2)
		{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 1}, // will be dead (count=3)
		{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 2}, // alive (count=10)
		{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 3}, // alive (count=100)
	}
	counts := []int{2, 3, 10, 100}

	for i, id := range ids {
		for j := 0; j < counts[i]; j++ {
			tracker.Increment(id)
		}
	}

	dead, err := tracker.GetDeadCentroids("global")
	if err != nil {
		t.Fatalf("GetDeadCentroids: %v", err)
	}

	if len(dead) != 2 {
		t.Errorf("GetDeadCentroids returned %d centroids, want 2", len(dead))
	}
}

func TestAdaptationTracker_GetOverloadedCentroids(t *testing.T) {
	tracker := newMockTracker(5, 100)

	ids := []CentroidID{
		{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 0}, // normal (count=50)
		{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 1}, // overloaded (count=150)
		{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 2}, // overloaded (count=200)
	}
	counts := []int{50, 150, 200}

	for i, id := range ids {
		for j := 0; j < counts[i]; j++ {
			tracker.Increment(id)
		}
	}

	overloaded, err := tracker.GetOverloadedCentroids("global")
	if err != nil {
		t.Fatalf("GetOverloadedCentroids: %v", err)
	}

	if len(overloaded) != 2 {
		t.Errorf("GetOverloadedCentroids returned %d centroids, want 2", len(overloaded))
	}
}

func TestAdaptationTracker_RecordEvents(t *testing.T) {
	tracker := newMockTracker(5, 10000)

	deathEvent := DeathEvent{
		Centroid:   CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 1},
		FinalCount: 2,
		Threshold:  5,
		OccurredAt: time.Now(),
	}

	birthEvent := BirthEvent{
		ParentCentroid:    CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 10},
		ChildCentroid:     CentroidID{CodebookType: "global", SubspaceIndex: 1, CentroidIndex: 128},
		ParentCountBefore: 15000,
		Threshold:         10000,
		OccurredAt:        time.Now(),
	}

	if err := tracker.RecordDeath(deathEvent); err != nil {
		t.Fatalf("RecordDeath: %v", err)
	}
	if err := tracker.RecordBirth(birthEvent); err != nil {
		t.Fatalf("RecordBirth: %v", err)
	}

	stats := tracker.GetStats()
	if stats.TotalDeaths != 1 {
		t.Errorf("TotalDeaths = %d, want 1", stats.TotalDeaths)
	}
	if stats.TotalBirths != 1 {
		t.Errorf("TotalBirths = %d, want 1", stats.TotalBirths)
	}
}

// =============================================================================
// RemoveBirthAdapter Tests
// =============================================================================

func TestRemoveBirthAdapter_NewWithValidConfig(t *testing.T) {
	tracker := newMockTracker(5, 10000)
	config := DefaultAdaptationConfig()

	adapter, err := NewRemoveBirthAdapter(config, tracker)
	if err != nil {
		t.Fatalf("NewRemoveBirthAdapter: %v", err)
	}
	if adapter == nil {
		t.Fatal("NewRemoveBirthAdapter returned nil")
	}
}

func TestRemoveBirthAdapter_NewWithInvalidConfig(t *testing.T) {
	tracker := newMockTracker(5, 10000)
	config := AdaptationConfig{
		DeathThreshold:          100,
		BirthThreshold:          50, // Invalid: less than death threshold
		CheckInterval:           time.Minute,
		MinCentroidsPerSubspace: 16,
		MaxCentroidsPerSubspace: 256,
		Enabled:                 true,
	}

	_, err := NewRemoveBirthAdapter(config, tracker)
	if err == nil {
		t.Error("NewRemoveBirthAdapter should fail with invalid config")
	}
	if err != ErrAdaptationInvalidBirthThreshold {
		t.Errorf("Expected ErrAdaptationInvalidBirthThreshold, got %v", err)
	}
}

func TestRemoveBirthAdapter_StartStop(t *testing.T) {
	tracker := newMockTracker(5, 10000)
	config := DefaultAdaptationConfig()
	config.CheckInterval = time.Second // Minimum valid interval for testing

	adapter, err := NewRemoveBirthAdapter(config, tracker)
	if err != nil {
		t.Fatalf("NewRemoveBirthAdapter: %v", err)
	}

	ctx := context.Background()

	// Start the adapter
	if err := adapter.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !adapter.IsRunning() {
		t.Error("IsRunning() = false after Start()")
	}

	// Starting again should be idempotent
	if err := adapter.Start(ctx); err != nil {
		t.Errorf("Second Start() failed: %v", err)
	}

	// Stop the adapter
	adapter.Stop()

	if adapter.IsRunning() {
		t.Error("IsRunning() = true after Stop()")
	}

	// Stopping again should be idempotent
	adapter.Stop()
}

func TestRemoveBirthAdapter_StartDisabled(t *testing.T) {
	tracker := newMockTracker(5, 10000)
	config := DefaultAdaptationConfig()
	config.Enabled = false

	adapter, err := NewRemoveBirthAdapter(config, tracker)
	if err != nil {
		t.Fatalf("NewRemoveBirthAdapter: %v", err)
	}

	ctx := context.Background()
	err = adapter.Start(ctx)
	if err != ErrAdaptationDisabled {
		t.Errorf("Start() with disabled config: got %v, want ErrAdaptationDisabled", err)
	}
}

func TestRemoveBirthAdapter_IncrementDecrement(t *testing.T) {
	tracker := newMockTracker(5, 10000)
	config := DefaultAdaptationConfig()

	adapter, err := NewRemoveBirthAdapter(config, tracker)
	if err != nil {
		t.Fatalf("NewRemoveBirthAdapter: %v", err)
	}

	id := CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 5}

	// Increment through adapter
	for i := 0; i < 5; i++ {
		if err := adapter.IncrementUtilization(id); err != nil {
			t.Fatalf("IncrementUtilization: %v", err)
		}
	}

	// Check via tracker
	u, _ := tracker.GetUtilization(id)
	if u.Count != 5 {
		t.Errorf("Count = %d, want 5", u.Count)
	}

	// Decrement through adapter
	if err := adapter.DecrementUtilization(id); err != nil {
		t.Fatalf("DecrementUtilization: %v", err)
	}

	u, _ = tracker.GetUtilization(id)
	if u.Count != 4 {
		t.Errorf("Count after decrement = %d, want 4", u.Count)
	}
}

func TestRemoveBirthAdapter_GetStats(t *testing.T) {
	tracker := newMockTracker(5, 100)
	config := DefaultAdaptationConfig()

	adapter, err := NewRemoveBirthAdapter(config, tracker)
	if err != nil {
		t.Fatalf("NewRemoveBirthAdapter: %v", err)
	}

	// Add some utilization data
	deadID := CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 0}
	for i := 0; i < 2; i++ {
		tracker.Increment(deadID)
	}

	overloadedID := CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 1}
	for i := 0; i < 150; i++ {
		tracker.Increment(overloadedID)
	}

	normalID := CentroidID{CodebookType: "global", SubspaceIndex: 0, CentroidIndex: 2}
	for i := 0; i < 50; i++ {
		tracker.Increment(normalID)
	}

	stats := adapter.GetStats()
	if stats.TotalCentroids != 3 {
		t.Errorf("TotalCentroids = %d, want 3", stats.TotalCentroids)
	}
	if stats.DeadCentroids != 1 {
		t.Errorf("DeadCentroids = %d, want 1", stats.DeadCentroids)
	}
	if stats.OverloadedCentroids != 1 {
		t.Errorf("OverloadedCentroids = %d, want 1", stats.OverloadedCentroids)
	}
}

func TestRemoveBirthAdapter_SetCallbacks(t *testing.T) {
	tracker := newMockTracker(5, 10000)
	config := DefaultAdaptationConfig()

	adapter, err := NewRemoveBirthAdapter(config, tracker)
	if err != nil {
		t.Fatalf("NewRemoveBirthAdapter: %v", err)
	}

	deathCount := 0
	birthCount := 0

	adapter.OnDeath(func(e DeathEvent) {
		deathCount++
	})
	adapter.OnBirth(func(e BirthEvent) {
		birthCount++
	})

	// Verify callbacks are set (can't easily trigger them without full integration)
	// Just verify they don't panic when set
	if deathCount != 0 || birthCount != 0 {
		t.Error("Callbacks should not have been called yet")
	}
}

func TestRemoveBirthAdapter_ForceCheck(t *testing.T) {
	tracker := newMockTracker(5, 10000)
	config := DefaultAdaptationConfig()

	adapter, err := NewRemoveBirthAdapter(config, tracker)
	if err != nil {
		t.Fatalf("NewRemoveBirthAdapter: %v", err)
	}

	ctx := context.Background()

	// ForceCheck should not panic even without codebook getter
	adapter.ForceCheck(ctx)

	// Set a getter that returns nil (no codebook available)
	adapter.SetCodebookGetter(func(string) *ProductQuantizer {
		return nil
	})

	// ForceCheck should handle nil codebook gracefully
	adapter.ForceCheck(ctx)
}

// =============================================================================
// Helper Functions
// =============================================================================

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstringRB(s, substr)))
}

func findSubstringRB(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
