package vectorgraphdb

import (
	"math"
	"sync"
	"testing"
)

func TestNewTripleRRFFusion_DefaultK(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(0, 1.0, 1.0, 1.0)
	if fusion.GetK() != 60 {
		t.Errorf("expected default k=60, got %d", fusion.GetK())
	}
}

func TestNewTripleRRFFusion_CustomK(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(100, 1.0, 1.0, 1.0)
	if fusion.GetK() != 100 {
		t.Errorf("expected k=100, got %d", fusion.GetK())
	}
}

func TestNewTripleRRFFusion_WeightNormalization(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 2.0, 3.0, 5.0)
	v, text, g := fusion.GetWeights()

	if !floatEquals(v, 0.2, 0.0001) {
		t.Errorf("vector weight = %v, want 0.2", v)
	}
	if !floatEquals(text, 0.3, 0.0001) {
		t.Errorf("text weight = %v, want 0.3", text)
	}
	if !floatEquals(g, 0.5, 0.0001) {
		t.Errorf("graph weight = %v, want 0.5", g)
	}
}

func TestNewTripleRRFFusion_ZeroWeights(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 0, 0, 0)
	v, text, g := fusion.GetWeights()

	expectedWeight := 1.0 / 3.0
	if !floatEquals(v, expectedWeight, 0.0001) {
		t.Errorf("vector weight = %v, want %v", v, expectedWeight)
	}
	if !floatEquals(text, expectedWeight, 0.0001) {
		t.Errorf("text weight = %v, want %v", text, expectedWeight)
	}
	if !floatEquals(g, expectedWeight, 0.0001) {
		t.Errorf("graph weight = %v, want %v", g, expectedWeight)
	}
}

func TestTripleRRFFusion_Fuse_Empty(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)
	results := fusion.Fuse(nil, nil, nil)

	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestTripleRRFFusion_Fuse_VectorOnly(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)
	vectorResults := []VectorResult{
		{ID: "a", Similarity: 0.9},
		{ID: "b", Similarity: 0.8},
	}

	results := fusion.Fuse(vectorResults, nil, nil)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("first result ID = %s, want a", results[0].ID)
	}
	if results[1].ID != "b" {
		t.Errorf("second result ID = %s, want b", results[1].ID)
	}
}

func TestTripleRRFFusion_Fuse_TextOnly(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)
	textResults := []TextResult{
		{ID: "x", Score: 0.95},
		{ID: "y", Score: 0.85},
	}

	results := fusion.Fuse(nil, textResults, nil)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "x" {
		t.Errorf("first result ID = %s, want x", results[0].ID)
	}
}

func TestTripleRRFFusion_Fuse_GraphOnly(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)
	graphResults := []GraphResult{
		{ID: "g1", Distance: 1, Relation: "calls"},
		{ID: "g2", Distance: 2, Relation: "imports"},
	}

	results := fusion.Fuse(nil, nil, graphResults)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "g1" {
		t.Errorf("first result ID = %s, want g1", results[0].ID)
	}
}

func TestTripleRRFFusion_Fuse_AllSources(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)

	vectorResults := []VectorResult{
		{ID: "a", Similarity: 0.9},
		{ID: "b", Similarity: 0.8},
	}
	textResults := []TextResult{
		{ID: "b", Score: 0.95},
		{ID: "c", Score: 0.85},
	}
	graphResults := []GraphResult{
		{ID: "b", Distance: 1, Relation: "calls"},
		{ID: "d", Distance: 2, Relation: "imports"},
	}

	results := fusion.Fuse(vectorResults, textResults, graphResults)

	if len(results) != 4 {
		t.Fatalf("expected 4 results (a,b,c,d), got %d", len(results))
	}

	// "b" appears in all 3 sources, should rank highest
	if results[0].ID != "b" {
		t.Errorf("first result ID = %s, want b (appears in all sources)", results[0].ID)
	}
}

func TestTripleRRFFusion_Fuse_RRFFormula(t *testing.T) {
	t.Parallel()

	k := 60
	fusion := NewTripleRRFFusion(k, 1.0, 1.0, 1.0)

	vectorResults := []VectorResult{
		{ID: "a", Similarity: 0.9},
	}

	results := fusion.Fuse(vectorResults, nil, nil)

	// Expected: (1/3) / (0 + 60) = 1/3 / 60 = 1/180
	expectedScore := (1.0 / 3.0) / float64(0+k)
	if !floatEquals(results[0].Score, expectedScore, 0.0001) {
		t.Errorf("score = %v, want %v", results[0].Score, expectedScore)
	}
}

func TestTripleRRFFusion_Fuse_TieBreaking(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)

	vectorResults := []VectorResult{
		{ID: "b", Similarity: 0.9},
	}
	textResults := []TextResult{
		{ID: "a", Score: 0.9},
	}

	results := fusion.Fuse(vectorResults, textResults, nil)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Both have same score, tie-break by ID (alphabetical)
	if results[0].ID != "a" {
		t.Errorf("first result ID = %s, want a (tie-break by ID)", results[0].ID)
	}
	if results[1].ID != "b" {
		t.Errorf("second result ID = %s, want b", results[1].ID)
	}
}

func TestTripleRRFFusion_FuseTopK(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)

	vectorResults := []VectorResult{
		{ID: "a", Similarity: 0.9},
		{ID: "b", Similarity: 0.8},
		{ID: "c", Similarity: 0.7},
		{ID: "d", Similarity: 0.6},
	}

	results := fusion.FuseTopK(vectorResults, nil, nil, 2)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ID != "a" {
		t.Errorf("first result ID = %s, want a", results[0].ID)
	}
	if results[1].ID != "b" {
		t.Errorf("second result ID = %s, want b", results[1].ID)
	}
}

func TestTripleRRFFusion_FuseTopK_ZeroK(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)

	vectorResults := []VectorResult{
		{ID: "a", Similarity: 0.9},
		{ID: "b", Similarity: 0.8},
	}

	results := fusion.FuseTopK(vectorResults, nil, nil, 0)

	if len(results) != 2 {
		t.Errorf("expected all results when topK=0, got %d", len(results))
	}
}

func TestTripleRRFFusion_FuseTopK_LargerThanResults(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)

	vectorResults := []VectorResult{
		{ID: "a", Similarity: 0.9},
	}

	results := fusion.FuseTopK(vectorResults, nil, nil, 100)

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestTripleRRFFusion_SetWeights(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)
	fusion.SetWeights(1.0, 2.0, 3.0)

	v, text, g := fusion.GetWeights()

	total := 1.0 + 2.0 + 3.0
	if !floatEquals(v, 1.0/total, 0.0001) {
		t.Errorf("vector weight = %v, want %v", v, 1.0/total)
	}
	if !floatEquals(text, 2.0/total, 0.0001) {
		t.Errorf("text weight = %v, want %v", text, 2.0/total)
	}
	if !floatEquals(g, 3.0/total, 0.0001) {
		t.Errorf("graph weight = %v, want %v", g, 3.0/total)
	}
}

func TestTripleRRFFusion_SetWeights_ZeroTotal(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 2.0, 3.0)
	originalV, originalT, originalG := fusion.GetWeights()

	fusion.SetWeights(0, 0, 0)

	v, text, g := fusion.GetWeights()

	// Weights should not change when all are zero
	if v != originalV || text != originalT || g != originalG {
		t.Errorf("weights changed when all zero: got (%v,%v,%v), want (%v,%v,%v)",
			v, text, g, originalV, originalT, originalG)
	}
}

func TestTripleRRFFusion_DeriveWeightsFromPerformance(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)
	fusion.DeriveWeightsFromPerformance(0.8, 0.6, 0.4)

	v, text, g := fusion.GetWeights()

	total := 0.8 + 0.6 + 0.4
	if !floatEquals(v, 0.8/total, 0.0001) {
		t.Errorf("vector weight = %v, want %v", v, 0.8/total)
	}
	if !floatEquals(text, 0.6/total, 0.0001) {
		t.Errorf("text weight = %v, want %v", text, 0.6/total)
	}
	if !floatEquals(g, 0.4/total, 0.0001) {
		t.Errorf("graph weight = %v, want %v", g, 0.4/total)
	}
}

func TestTripleRRFFusion_DeriveWeightsFromPerformance_ZeroTotal(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 2.0, 3.0)
	originalV, originalT, originalG := fusion.GetWeights()

	fusion.DeriveWeightsFromPerformance(0, 0, 0)

	v, text, g := fusion.GetWeights()

	if v != originalV || text != originalT || g != originalG {
		t.Errorf("weights changed when all recalls zero: got (%v,%v,%v), want (%v,%v,%v)",
			v, text, g, originalV, originalT, originalG)
	}
}

func TestTripleRRFFusion_Concurrency(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 1.0, 1.0)

	vectorResults := []VectorResult{{ID: "a", Similarity: 0.9}}
	textResults := []TextResult{{ID: "b", Score: 0.8}}
	graphResults := []GraphResult{{ID: "c", Distance: 1, Relation: "calls"}}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			_ = fusion.Fuse(vectorResults, textResults, graphResults)
		}()

		go func() {
			defer wg.Done()
			fusion.SetWeights(1.0, 2.0, 3.0)
		}()

		go func() {
			defer wg.Done()
			fusion.GetWeights()
		}()
	}

	wg.Wait()
}

func TestTripleRRFFusion_WeightsSumToOne(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		v, t, g float64
	}{
		{"equal", 1.0, 1.0, 1.0},
		{"unequal", 2.0, 3.0, 5.0},
		{"large", 100.0, 200.0, 300.0},
		{"small", 0.1, 0.2, 0.3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fusion := NewTripleRRFFusion(60, tc.v, tc.t, tc.g)
			v, text, g := fusion.GetWeights()
			sum := v + text + g

			if !floatEquals(sum, 1.0, 0.0001) {
				t.Errorf("weights sum = %v, want 1.0", sum)
			}
		})
	}
}

func TestTripleRRFFusion_RankOrderMatters(t *testing.T) {
	t.Parallel()

	fusion := NewTripleRRFFusion(60, 1.0, 0, 0)

	vectorResults := []VectorResult{
		{ID: "first", Similarity: 0.5},
		{ID: "second", Similarity: 0.9},
	}

	results := fusion.Fuse(vectorResults, nil, nil)

	// "first" appears at rank 0, should have higher RRF score than "second" at rank 1
	if results[0].ID != "first" {
		t.Errorf("expected 'first' to rank higher due to rank 0, got %s", results[0].ID)
	}
}

func floatEquals(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}
