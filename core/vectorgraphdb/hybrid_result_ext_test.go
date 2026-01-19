package vectorgraphdb

import (
	"testing"
	"time"
)

func TestParseTierSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected TierSource
	}{
		{"hot", TierSourceHot},
		{"warm", TierSourceWarm},
		{"full", TierSourceFull},
		{"", TierSourceNone},
		{"invalid", TierSourceNone},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			result := ParseTierSource(tc.input)
			if result != tc.expected {
				t.Errorf("ParseTierSource(%q) = %v, want %v", tc.input, result, tc.expected)
			}
		})
	}
}

func TestNewExtendedHybridResult(t *testing.T) {
	t.Parallel()

	base := HybridResult{
		Node:            &GraphNode{ID: "test-node"},
		VectorScore:     0.8,
		GraphScore:      0.5,
		CombinedScore:   0.7,
		ConnectionCount: 3,
	}

	before := time.Now()
	result := NewExtendedHybridResult(base)
	after := time.Now()

	if result.Node.ID != "test-node" {
		t.Error("base result not preserved")
	}
	if result.RetrievalCost <= 0 {
		t.Error("retrieval cost should be positive")
	}
	if result.TierSource != TierSourceNone {
		t.Error("tier source should be none by default")
	}
	if result.AccessedAt.Before(before) || result.AccessedAt.After(after) {
		t.Error("accessed at should be set to current time")
	}
}

func TestExtendedHybridResult_WithMethods(t *testing.T) {
	t.Parallel()

	base := HybridResult{Node: &GraphNode{ID: "test"}}
	result := NewExtendedHybridResult(base)

	result.WithBleveScore(0.9).
		WithTierSource(TierSourceHot).
		WithRetrievalCost(0.5).
		WithFusionRank(3)

	if result.BleveScore != 0.9 {
		t.Errorf("BleveScore = %v, want 0.9", result.BleveScore)
	}
	if result.TierSource != TierSourceHot {
		t.Errorf("TierSource = %v, want hot", result.TierSource)
	}
	if result.RetrievalCost != 0.5 {
		t.Errorf("RetrievalCost = %v, want 0.5", result.RetrievalCost)
	}
	if result.FusionRank != 3 {
		t.Errorf("FusionRank = %v, want 3", result.FusionRank)
	}
}

func TestExtendedHybridResult_MarkAccessed(t *testing.T) {
	t.Parallel()

	base := HybridResult{Node: &GraphNode{ID: "test"}}
	result := NewExtendedHybridResult(base)

	originalTime := result.AccessedAt
	time.Sleep(1 * time.Millisecond)
	result.MarkAccessed()

	if !result.AccessedAt.After(originalTime) {
		t.Error("MarkAccessed should update timestamp")
	}
}

func TestExtendResults(t *testing.T) {
	t.Parallel()

	baseResults := []HybridResult{
		{Node: &GraphNode{ID: "a"}, CombinedScore: 0.9},
		{Node: &GraphNode{ID: "b"}, CombinedScore: 0.8},
		{Node: &GraphNode{ID: "c"}, CombinedScore: 0.7},
	}

	extended := ExtendResults(baseResults)

	if len(extended) != 3 {
		t.Fatalf("len(extended) = %d, want 3", len(extended))
	}

	for i, r := range extended {
		if r.Node.ID != baseResults[i].Node.ID {
			t.Errorf("result[%d].Node.ID = %s, want %s", i, r.Node.ID, baseResults[i].Node.ID)
		}
	}
}

func TestToHybridResults(t *testing.T) {
	t.Parallel()

	extended := []*ExtendedHybridResult{
		NewExtendedHybridResult(HybridResult{Node: &GraphNode{ID: "a"}}),
		NewExtendedHybridResult(HybridResult{Node: &GraphNode{ID: "b"}}),
	}

	base := ToHybridResults(extended)

	if len(base) != 2 {
		t.Fatalf("len(base) = %d, want 2", len(base))
	}

	if base[0].Node.ID != "a" {
		t.Errorf("base[0].Node.ID = %s, want a", base[0].Node.ID)
	}
}

func TestTierCost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source   TierSource
		expected float64
	}{
		{TierSourceHot, TierCostHot},
		{TierSourceWarm, TierCostWarm},
		{TierSourceFull, TierCostFull},
		{TierSourceNone, TierCostFull},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(string(tc.source), func(t *testing.T) {
			t.Parallel()
			cost := TierCost(tc.source)
			if cost != tc.expected {
				t.Errorf("TierCost(%v) = %v, want %v", tc.source, cost, tc.expected)
			}
		})
	}
}

func TestComputeMetrics_Empty(t *testing.T) {
	t.Parallel()

	metrics := ComputeMetrics(nil)

	if metrics.TotalResults != 0 {
		t.Errorf("TotalResults = %d, want 0", metrics.TotalResults)
	}
	if metrics.AverageScore != 0 {
		t.Errorf("AverageScore = %v, want 0", metrics.AverageScore)
	}
}

func TestComputeMetrics(t *testing.T) {
	t.Parallel()

	results := []*ExtendedHybridResult{
		NewExtendedHybridResult(HybridResult{
			Node:          &GraphNode{ID: "a"},
			CombinedScore: 0.9,
		}).WithTierSource(TierSourceHot).WithRetrievalCost(0.1),
		NewExtendedHybridResult(HybridResult{
			Node:          &GraphNode{ID: "b"},
			CombinedScore: 0.7,
		}).WithTierSource(TierSourceWarm).WithRetrievalCost(0.5).WithBleveScore(0.8),
		NewExtendedHybridResult(HybridResult{
			Node:          &GraphNode{ID: "c"},
			CombinedScore: 0.5,
		}).WithTierSource(TierSourceFull).WithRetrievalCost(1.0),
	}

	metrics := ComputeMetrics(results)

	if metrics.TotalResults != 3 {
		t.Errorf("TotalResults = %d, want 3", metrics.TotalResults)
	}
	if metrics.HotCount != 1 {
		t.Errorf("HotCount = %d, want 1", metrics.HotCount)
	}
	if metrics.WarmCount != 1 {
		t.Errorf("WarmCount = %d, want 1", metrics.WarmCount)
	}
	if metrics.FullCount != 1 {
		t.Errorf("FullCount = %d, want 1", metrics.FullCount)
	}
	if metrics.BleveResultCount != 1 {
		t.Errorf("BleveResultCount = %d, want 1", metrics.BleveResultCount)
	}

	expectedTotalCost := 0.1 + 0.5 + 1.0
	if metrics.TotalCost != expectedTotalCost {
		t.Errorf("TotalCost = %v, want %v", metrics.TotalCost, expectedTotalCost)
	}

	expectedAvgScore := (0.9 + 0.7 + 0.5) / 3.0
	if diff := metrics.AverageScore - expectedAvgScore; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("AverageScore = %v, want %v", metrics.AverageScore, expectedAvgScore)
	}
}

func TestEstimateCost(t *testing.T) {
	t.Parallel()

	// Higher score should result in lower cost
	highScore := HybridResult{CombinedScore: 0.9, ConnectionCount: 0}
	lowScore := HybridResult{CombinedScore: 0.1, ConnectionCount: 0}

	highCost := estimateCost(highScore)
	lowCost := estimateCost(lowScore)

	if highCost >= lowCost {
		t.Error("higher score should have lower cost")
	}

	// More connections should increase cost
	fewConnections := HybridResult{CombinedScore: 0.5, ConnectionCount: 1}
	manyConnections := HybridResult{CombinedScore: 0.5, ConnectionCount: 10}

	fewCost := estimateCost(fewConnections)
	manyCost := estimateCost(manyConnections)

	if fewCost >= manyCost {
		t.Error("more connections should increase cost")
	}
}

func TestTierSourceConstants(t *testing.T) {
	t.Parallel()

	if TierSourceHot != "hot" {
		t.Error("TierSourceHot should be 'hot'")
	}
	if TierSourceWarm != "warm" {
		t.Error("TierSourceWarm should be 'warm'")
	}
	if TierSourceFull != "full" {
		t.Error("TierSourceFull should be 'full'")
	}
	if TierSourceNone != "" {
		t.Error("TierSourceNone should be empty string")
	}
}

func TestTierCostConstants(t *testing.T) {
	t.Parallel()

	if TierCostHot >= TierCostWarm {
		t.Error("hot tier should be cheaper than warm")
	}
	if TierCostWarm >= TierCostFull {
		t.Error("warm tier should be cheaper than full")
	}
}
