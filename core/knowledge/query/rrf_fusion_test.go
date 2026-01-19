package query

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// QueryWeights Tests
// =============================================================================

func TestQueryWeights_DefaultQueryWeights(t *testing.T) {
	weights := DefaultQueryWeights()

	assert.NotNil(t, weights)
	assert.InDelta(t, 0.33, weights.TextWeight, 0.01)
	assert.InDelta(t, 0.34, weights.SemanticWeight, 0.01)
	assert.InDelta(t, 0.33, weights.GraphWeight, 0.01)
	assert.Equal(t, 0.0, weights.MemoryWeight)

	// Should sum to 1.0
	sum := weights.TextWeight + weights.SemanticWeight + weights.GraphWeight + weights.MemoryWeight
	assert.InDelta(t, 1.0, sum, 0.01)
}

func TestQueryWeights_DefaultQueryWeightsWithMemory(t *testing.T) {
	weights := DefaultQueryWeightsWithMemory()

	assert.NotNil(t, weights)
	assert.Equal(t, 0.25, weights.TextWeight)
	assert.Equal(t, 0.25, weights.SemanticWeight)
	assert.Equal(t, 0.25, weights.GraphWeight)
	assert.Equal(t, 0.25, weights.MemoryWeight)

	// Should sum to 1.0
	sum := weights.TextWeight + weights.SemanticWeight + weights.GraphWeight + weights.MemoryWeight
	assert.Equal(t, 1.0, sum)
}

func TestQueryWeights_Validate_Valid(t *testing.T) {
	tests := []struct {
		name    string
		weights *QueryWeights
	}{
		{
			name:    "default weights",
			weights: DefaultQueryWeights(),
		},
		{
			name:    "default with memory",
			weights: DefaultQueryWeightsWithMemory(),
		},
		{
			name: "text heavy",
			weights: &QueryWeights{
				TextWeight:     0.6,
				SemanticWeight: 0.2,
				GraphWeight:    0.2,
			},
		},
		{
			name: "single source",
			weights: &QueryWeights{
				TextWeight:     1.0,
				SemanticWeight: 0.0,
				GraphWeight:    0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.weights.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestQueryWeights_Validate_Invalid(t *testing.T) {
	tests := []struct {
		name        string
		weights     *QueryWeights
		errContains string
	}{
		{
			name: "negative text weight",
			weights: &QueryWeights{
				TextWeight:     -0.1,
				SemanticWeight: 0.5,
				GraphWeight:    0.5,
			},
			errContains: "text weight",
		},
		{
			name: "text weight over 1",
			weights: &QueryWeights{
				TextWeight:     1.5,
				SemanticWeight: 0.25,
				GraphWeight:    0.25,
			},
			errContains: "text weight",
		},
		{
			name: "negative semantic weight",
			weights: &QueryWeights{
				TextWeight:     0.5,
				SemanticWeight: -0.1,
				GraphWeight:    0.5,
			},
			errContains: "semantic weight",
		},
		{
			name: "negative graph weight",
			weights: &QueryWeights{
				TextWeight:     0.5,
				SemanticWeight: 0.5,
				GraphWeight:    -0.1,
			},
			errContains: "graph weight",
		},
		{
			name: "sum too low",
			weights: &QueryWeights{
				TextWeight:     0.1,
				SemanticWeight: 0.1,
				GraphWeight:    0.1,
			},
			errContains: "sum to ~1.0",
		},
		{
			name: "sum too high",
			weights: &QueryWeights{
				TextWeight:     0.5,
				SemanticWeight: 0.5,
				GraphWeight:    0.5,
			},
			errContains: "sum to ~1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.weights.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}
}

func TestQueryWeights_Normalize(t *testing.T) {
	tests := []struct {
		name     string
		weights  *QueryWeights
		expected *QueryWeights
	}{
		{
			name: "already normalized",
			weights: &QueryWeights{
				TextWeight:     0.33,
				SemanticWeight: 0.34,
				GraphWeight:    0.33,
			},
			expected: &QueryWeights{
				TextWeight:     0.33,
				SemanticWeight: 0.34,
				GraphWeight:    0.33,
			},
		},
		{
			name: "needs normalization",
			weights: &QueryWeights{
				TextWeight:     2.0,
				SemanticWeight: 2.0,
				GraphWeight:    1.0,
			},
			expected: &QueryWeights{
				TextWeight:     0.4,
				SemanticWeight: 0.4,
				GraphWeight:    0.2,
			},
		},
		{
			name: "zero weights",
			weights: &QueryWeights{
				TextWeight:     0.0,
				SemanticWeight: 0.0,
				GraphWeight:    0.0,
			},
			expected: DefaultQueryWeights(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := tt.weights.Normalize()

			assert.InDelta(t, tt.expected.TextWeight, normalized.TextWeight, 0.01)
			assert.InDelta(t, tt.expected.SemanticWeight, normalized.SemanticWeight, 0.01)
			assert.InDelta(t, tt.expected.GraphWeight, normalized.GraphWeight, 0.01)

			// Verify normalization
			sum := normalized.TextWeight + normalized.SemanticWeight + normalized.GraphWeight + normalized.MemoryWeight
			assert.InDelta(t, 1.0, sum, 0.01)
		})
	}
}

func TestQueryWeights_Clone(t *testing.T) {
	original := &QueryWeights{
		TextWeight:     0.4,
		SemanticWeight: 0.3,
		GraphWeight:    0.2,
		MemoryWeight:   0.1,
	}

	cloned := original.Clone()

	assert.Equal(t, original.TextWeight, cloned.TextWeight)
	assert.Equal(t, original.SemanticWeight, cloned.SemanticWeight)
	assert.Equal(t, original.GraphWeight, cloned.GraphWeight)
	assert.Equal(t, original.MemoryWeight, cloned.MemoryWeight)

	// Modify clone and verify original unchanged
	cloned.TextWeight = 0.9
	assert.Equal(t, 0.4, original.TextWeight)
}

func TestQueryWeights_IsZero(t *testing.T) {
	assert.True(t, (&QueryWeights{}).IsZero())
	assert.False(t, DefaultQueryWeights().IsZero())
	assert.False(t, (&QueryWeights{TextWeight: 0.1}).IsZero())
}

// =============================================================================
// RRFFusion Tests
// =============================================================================

func TestRRFFusion_NewRRFFusion(t *testing.T) {
	tests := []struct {
		name     string
		k        int
		expectedK int
	}{
		{"default k", 60, 60},
		{"custom k", 30, 30},
		{"zero k defaults", 0, 60},
		{"negative k defaults", -10, 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rrf := NewRRFFusion(tt.k)
			assert.Equal(t, tt.expectedK, rrf.K())
		})
	}
}

func TestRRFFusion_DefaultRRFFusion(t *testing.T) {
	rrf := DefaultRRFFusion()
	assert.NotNil(t, rrf)
	assert.Equal(t, 60, rrf.K())
}

func TestRRFFusion_SetK(t *testing.T) {
	rrf := NewRRFFusion(60)

	rrf.SetK(30)
	assert.Equal(t, 30, rrf.K())

	rrf.SetK(0) // Should default to 60
	assert.Equal(t, 60, rrf.K())

	rrf.SetK(-5) // Should default to 60
	assert.Equal(t, 60, rrf.K())
}

func TestRRFFusion_Fuse_EmptyInputs(t *testing.T) {
	rrf := NewRRFFusion(60)

	results := rrf.Fuse(nil, nil, nil, nil)
	assert.Empty(t, results)

	results = rrf.Fuse([]TextResult{}, []VectorResult{}, []GraphResult{}, nil)
	assert.Empty(t, results)
}

func TestRRFFusion_Fuse_SingleSource_Text(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0, Content: "Document 1"},
		{ID: "doc2", Score: 0.8, Content: "Document 2"},
		{ID: "doc3", Score: 0.5, Content: "Document 3"},
	}

	results := rrf.Fuse(textResults, nil, nil, &QueryWeights{
		TextWeight:     1.0,
		SemanticWeight: 0.0,
		GraphWeight:    0.0,
	})

	require.Len(t, results, 3)

	// Verify order is preserved (by RRF score)
	assert.Equal(t, "doc1", results[0].ID)
	assert.Equal(t, "doc2", results[1].ID)
	assert.Equal(t, "doc3", results[2].ID)

	// Verify source is text
	assert.Equal(t, SourceBleve, results[0].Source)

	// Verify text scores are preserved
	assert.Equal(t, 1.0, results[0].TextScore)
	assert.Equal(t, 0.8, results[1].TextScore)
}

func TestRRFFusion_Fuse_SingleSource_Semantic(t *testing.T) {
	rrf := NewRRFFusion(60)

	semanticResults := []VectorResult{
		{ID: "doc1", Score: 0.95},
		{ID: "doc2", Score: 0.85},
	}

	results := rrf.Fuse(nil, semanticResults, nil, &QueryWeights{
		TextWeight:     0.0,
		SemanticWeight: 1.0,
		GraphWeight:    0.0,
	})

	require.Len(t, results, 2)
	assert.Equal(t, "doc1", results[0].ID)
	assert.Equal(t, SourceHNSW, results[0].Source)
	assert.Equal(t, 0.95, results[0].SemanticScore)
}

func TestRRFFusion_Fuse_SingleSource_Graph(t *testing.T) {
	rrf := NewRRFFusion(60)

	graphResults := []GraphResult{
		{
			ID:    "doc1",
			Score: 0.9,
			Path:  []string{"start", "middle", "doc1"},
			MatchedEdges: []EdgeMatch{
				{EdgeID: 1, EdgeType: "uses", Weight: 0.5},
			},
		},
	}

	results := rrf.Fuse(nil, nil, graphResults, &QueryWeights{
		TextWeight:     0.0,
		SemanticWeight: 0.0,
		GraphWeight:    1.0,
	})

	require.Len(t, results, 1)
	assert.Equal(t, "doc1", results[0].ID)
	assert.Equal(t, SourceGraph, results[0].Source)
	assert.Equal(t, 0.9, results[0].GraphScore)
	assert.Equal(t, []string{"start", "middle", "doc1"}, results[0].TraversalPath)
	require.Len(t, results[0].MatchedEdges, 1)
}

func TestRRFFusion_Fuse_MultipleSourcesNoOverlap(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "text1", Score: 1.0},
		{ID: "text2", Score: 0.5},
	}

	semanticResults := []VectorResult{
		{ID: "sem1", Score: 0.9},
		{ID: "sem2", Score: 0.7},
	}

	graphResults := []GraphResult{
		{ID: "graph1", Score: 0.8},
	}

	results := rrf.Fuse(textResults, semanticResults, graphResults, nil)

	require.Len(t, results, 5)

	// Verify all IDs are present
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	assert.True(t, ids["text1"])
	assert.True(t, ids["text2"])
	assert.True(t, ids["sem1"])
	assert.True(t, ids["sem2"])
	assert.True(t, ids["graph1"])
}

func TestRRFFusion_Fuse_MultipleSourcesWithOverlap(t *testing.T) {
	rrf := NewRRFFusion(60)

	// Same document appears in all three sources
	textResults := []TextResult{
		{ID: "common", Score: 0.9, Content: "Common document"},
		{ID: "text_only", Score: 0.5},
	}

	semanticResults := []VectorResult{
		{ID: "common", Score: 0.85},
		{ID: "sem_only", Score: 0.7},
	}

	graphResults := []GraphResult{
		{ID: "common", Score: 0.8},
	}

	results := rrf.Fuse(textResults, semanticResults, graphResults, nil)

	// Find the common result
	var commonResult *HybridResult
	for i := range results {
		if results[i].ID == "common" {
			commonResult = &results[i]
			break
		}
	}

	require.NotNil(t, commonResult)

	// Should be ranked first due to appearing in all sources
	assert.Equal(t, "common", results[0].ID)

	// Should have combined source
	assert.Equal(t, SourceCombined, commonResult.Source)

	// Should have all three component scores
	assert.Equal(t, 0.9, commonResult.TextScore)
	assert.Equal(t, 0.85, commonResult.SemanticScore)
	assert.Equal(t, 0.8, commonResult.GraphScore)

	// RRF score should be higher than single-source results
	assert.Greater(t, commonResult.Score, results[len(results)-1].Score)
}

func TestRRFFusion_Fuse_RRFScoreCalculation(t *testing.T) {
	// Use k=60 (standard RRF)
	rrf := NewRRFFusion(60)

	// Single text result at rank 0
	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
	}

	weights := &QueryWeights{
		TextWeight:     1.0,
		SemanticWeight: 0.0,
		GraphWeight:    0.0,
	}

	results := rrf.Fuse(textResults, nil, nil, weights)

	require.Len(t, results, 1)

	// Expected RRF score: weight / (k + rank + 1) = 1.0 / (60 + 0 + 1) = 1/61
	expectedScore := 1.0 / 61.0
	assert.InDelta(t, expectedScore, results[0].Score, 0.001)
}

func TestRRFFusion_Fuse_RRFScoreWithMultipleRanks(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0}, // rank 0
		{ID: "doc2", Score: 0.9}, // rank 1
		{ID: "doc3", Score: 0.8}, // rank 2
	}

	weights := &QueryWeights{
		TextWeight:     1.0,
		SemanticWeight: 0.0,
		GraphWeight:    0.0,
	}

	results := rrf.Fuse(textResults, nil, nil, weights)

	require.Len(t, results, 3)

	// Verify RRF scores decrease with rank
	assert.Greater(t, results[0].Score, results[1].Score)
	assert.Greater(t, results[1].Score, results[2].Score)

	// Verify specific scores: weight / (k + rank + 1)
	assert.InDelta(t, 1.0/61.0, results[0].Score, 0.001)  // rank 0
	assert.InDelta(t, 1.0/62.0, results[1].Score, 0.001)  // rank 1
	assert.InDelta(t, 1.0/63.0, results[2].Score, 0.001)  // rank 2
}

func TestRRFFusion_Fuse_WeightsAffectScores(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
	}

	// Higher text weight
	weightsHighText := &QueryWeights{
		TextWeight:     0.8,
		SemanticWeight: 0.1,
		GraphWeight:    0.1,
	}

	// Lower text weight
	weightsLowText := &QueryWeights{
		TextWeight:     0.2,
		SemanticWeight: 0.4,
		GraphWeight:    0.4,
	}

	resultsHigh := rrf.Fuse(textResults, nil, nil, weightsHighText)
	resultsLow := rrf.Fuse(textResults, nil, nil, weightsLowText)

	require.Len(t, resultsHigh, 1)
	require.Len(t, resultsLow, 1)

	// Higher text weight should result in higher score
	assert.Greater(t, resultsHigh[0].Score, resultsLow[0].Score)
}

func TestRRFFusion_FuseWithMemory(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc2", Score: 0.8},
	}

	memoryScores := map[string]float64{
		"doc2": 0.9, // doc2 has high memory activation
		"doc3": 0.7, // doc3 only in memory
	}

	weights := &QueryWeights{
		TextWeight:     0.4,
		SemanticWeight: 0.0,
		GraphWeight:    0.0,
		MemoryWeight:   0.6,
	}

	results := rrf.FuseWithMemory(textResults, nil, nil, weights, memoryScores)

	require.Len(t, results, 3) // doc1, doc2, doc3

	// doc2 should rank higher due to strong memory activation
	var doc2Idx int
	for i, r := range results {
		if r.ID == "doc2" {
			doc2Idx = i
			break
		}
	}

	// doc2 should be in top positions due to combined text + memory
	assert.LessOrEqual(t, doc2Idx, 1)
}

func TestRRFFusion_FuseWithMemory_NilMemory(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
	}

	// Should work with nil memoryScores
	results := rrf.FuseWithMemory(textResults, nil, nil, nil, nil)

	require.Len(t, results, 1)
	assert.Equal(t, "doc1", results[0].ID)
}

func TestRRFFusion_Fuse_TieBreaking(t *testing.T) {
	rrf := NewRRFFusion(60)

	// Two results with same RRF score but different source counts
	textResults := []TextResult{
		{ID: "multi", Score: 1.0},
		{ID: "single", Score: 0.9},
	}

	semanticResults := []VectorResult{
		{ID: "multi", Score: 0.95},
	}

	weights := &QueryWeights{
		TextWeight:     0.5,
		SemanticWeight: 0.5,
		GraphWeight:    0.0,
	}

	results := rrf.Fuse(textResults, semanticResults, nil, weights)

	require.Len(t, results, 2)

	// "multi" should be first due to appearing in more sources
	assert.Equal(t, "multi", results[0].ID)
}

func TestRRFFusion_FuseBatch(t *testing.T) {
	rrf := NewRRFFusion(60)

	queries := []FusionQuery{
		{
			Text: []TextResult{{ID: "q1_doc1", Score: 1.0}},
		},
		{
			Text:     []TextResult{{ID: "q2_doc1", Score: 0.9}},
			Semantic: []VectorResult{{ID: "q2_doc2", Score: 0.8}},
		},
		{
			Graph: []GraphResult{{ID: "q3_doc1", Score: 0.7}},
		},
	}

	results := rrf.FuseBatch(queries, nil)

	require.Len(t, results, 3)
	require.Len(t, results[0], 1)
	require.Len(t, results[1], 2)
	require.Len(t, results[2], 1)

	assert.Equal(t, "q1_doc1", results[0][0].ID)
	assert.Equal(t, "q3_doc1", results[2][0].ID)
}

func TestRRFFusion_Fuse_ContentPreservation(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0, Content: "This is the document content"},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	require.Len(t, results, 1)
	assert.Equal(t, "This is the document content", results[0].Content)
}

func TestRRFFusion_Fuse_GraphDataPreservation(t *testing.T) {
	rrf := NewRRFFusion(60)

	graphResults := []GraphResult{
		{
			ID:    "doc1",
			Score: 0.9,
			Path:  []string{"a", "b", "c"},
			MatchedEdges: []EdgeMatch{
				{EdgeID: 1, EdgeType: "calls", Weight: 0.5},
				{EdgeID: 2, EdgeType: "uses", Weight: 0.3},
			},
		},
	}

	results := rrf.Fuse(nil, nil, graphResults, nil)

	require.Len(t, results, 1)
	assert.Equal(t, []string{"a", "b", "c"}, results[0].TraversalPath)
	require.Len(t, results[0].MatchedEdges, 2)
	assert.Equal(t, "calls", results[0].MatchedEdges[0].EdgeType)
}

func TestRRFFusion_Fuse_LargeK(t *testing.T) {
	// Large k makes ranking differences smaller
	rrf := NewRRFFusion(1000)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc2", Score: 0.9},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	require.Len(t, results, 2)

	// With large k, score differences should be very small
	scoreDiff := results[0].Score - results[1].Score
	assert.Less(t, scoreDiff, 0.0001)
}

func TestRRFFusion_Fuse_SmallK(t *testing.T) {
	// Small k makes ranking differences larger
	rrf := NewRRFFusion(1)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc2", Score: 0.9},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	require.Len(t, results, 2)

	// With small k, score differences should be larger
	scoreDiff := results[0].Score - results[1].Score
	assert.Greater(t, scoreDiff, 0.05)
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkRRFFusion_Fuse_Small(b *testing.B) {
	rrf := NewRRFFusion(60)

	textResults := make([]TextResult, 10)
	semanticResults := make([]VectorResult, 10)
	graphResults := make([]GraphResult, 10)

	for i := 0; i < 10; i++ {
		id := string(rune('a' + i))
		textResults[i] = TextResult{ID: id, Score: 1.0 - float64(i)*0.1}
		semanticResults[i] = VectorResult{ID: id, Score: 0.9 - float64(i)*0.1}
		graphResults[i] = GraphResult{ID: id, Score: 0.8 - float64(i)*0.1}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rrf.Fuse(textResults, semanticResults, graphResults, nil)
	}
}

func BenchmarkRRFFusion_Fuse_Large(b *testing.B) {
	rrf := NewRRFFusion(60)

	textResults := make([]TextResult, 100)
	semanticResults := make([]VectorResult, 100)
	graphResults := make([]GraphResult, 100)

	for i := 0; i < 100; i++ {
		id := string(rune('a' + i%26))
		textResults[i] = TextResult{ID: id, Score: float64(100-i) / 100.0}
		semanticResults[i] = VectorResult{ID: id, Score: float64(100-i) / 100.0}
		graphResults[i] = GraphResult{ID: id, Score: float64(100-i) / 100.0}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rrf.Fuse(textResults, semanticResults, graphResults, nil)
	}
}

func BenchmarkRRFFusion_FuseWithMemory(b *testing.B) {
	rrf := NewRRFFusion(60)

	textResults := make([]TextResult, 50)
	memoryScores := make(map[string]float64)

	for i := 0; i < 50; i++ {
		id := string(rune('a' + i%26))
		textResults[i] = TextResult{ID: id, Score: float64(50-i) / 50.0}
		memoryScores[id] = float64(i) / 50.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rrf.FuseWithMemory(textResults, nil, nil, nil, memoryScores)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestRRFFusion_Fuse_DuplicateIDs(t *testing.T) {
	rrf := NewRRFFusion(60)

	// Same ID appears multiple times in text results
	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc1", Score: 0.9}, // Duplicate
		{ID: "doc2", Score: 0.8},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	// Should handle gracefully - later entry may overwrite or combine
	// The important thing is no panic
	assert.NotEmpty(t, results)

	// Count unique IDs
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	// Should have at most 2 unique IDs
	assert.LessOrEqual(t, len(ids), 2)
}

func TestRRFFusion_Fuse_EmptyID(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "", Score: 1.0},
		{ID: "doc1", Score: 0.9},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	// Should handle empty ID gracefully
	assert.NotEmpty(t, results)
}

func TestRRFFusion_Fuse_NegativeScores(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: -0.5},
		{ID: "doc2", Score: 1.0},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	// Should handle negative scores
	require.Len(t, results, 2)
	// RRF score is based on rank, not original score
	// So order should still be doc1 (rank 0), doc2 (rank 1)
	assert.Equal(t, "doc1", results[0].ID)
}

func TestRRFFusion_Fuse_VerySmallScores(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1e-15},
		{ID: "doc2", Score: 1e-16},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	require.Len(t, results, 2)
	// Results should still be properly sorted
	assert.Greater(t, results[0].Score, results[1].Score)
}

func TestRRFFusion_Fuse_InfScore(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: math.Inf(1)},
		{ID: "doc2", Score: 1.0},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	// Should handle infinity gracefully
	assert.NotEmpty(t, results)
}

func TestRRFFusion_Fuse_NaNScore(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: math.NaN()},
		{ID: "doc2", Score: 1.0},
	}

	results := rrf.Fuse(textResults, nil, nil, nil)

	// Should handle NaN gracefully
	assert.NotEmpty(t, results)
}

func TestRRFFusion_ConcurrentAccess(t *testing.T) {
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc2", Score: 0.9},
	}

	// Run multiple goroutines concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				results := rrf.Fuse(textResults, nil, nil, nil)
				assert.NotEmpty(t, results)
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
