package hnsw

import (
	"math"
	"sync"
	"testing"
)

func TestNewFINGERAccelerator(t *testing.T) {
	tests := []struct {
		name            string
		dim             int
		lowRank         int
		expectedLowRank int
	}{
		{"default low rank for 128 dim", 128, 0, 32},
		{"default low rank for 64 dim", 64, 0, 16},
		{"default low rank for small dim", 8, 0, 2},
		{"explicit low rank", 128, 16, 16},
		{"minimum low rank", 4, -1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acc := NewFINGERAccelerator(tt.dim, tt.lowRank)
			if acc.lowRank != tt.expectedLowRank {
				t.Errorf("expected lowRank %d, got %d", tt.expectedLowRank, acc.lowRank)
			}
			if acc.skipThreshold != 0.7 {
				t.Errorf("expected default skipThreshold 0.7, got %f", acc.skipThreshold)
			}
			if acc.edgeResiduals == nil {
				t.Error("edgeResiduals map should be initialized")
			}
		})
	}
}

func TestFINGERAccelerator_PrecomputeResiduals(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0, 0.0},
		"b": {0.0, 1.0, 0.0, 0.0},
		"c": {0.0, 0.0, 1.0, 0.0},
		"d": {0.0, 0.0, 0.0, 1.0},
		"e": {0.5, 0.5, 0.0, 0.0},
	}

	neighbors := map[string][]string{
		"a": {"b", "c"},
		"b": {"a", "d"},
		"c": {"a", "e"},
	}

	acc.PrecomputeResiduals(vectors, neighbors)

	if acc.GetEdgeResidualCount() != 6 {
		t.Errorf("expected 6 edge residuals, got %d", acc.GetEdgeResidualCount())
	}

	if !acc.HasProjectionMatrix() {
		t.Error("projection matrix should be computed")
	}
}

func TestFINGERAccelerator_PrecomputeResidualsEmptyInput(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	acc.PrecomputeResiduals(nil, nil)
	if acc.HasProjectionMatrix() {
		t.Error("projection matrix should not be computed for empty input")
	}

	acc.PrecomputeResiduals(map[string][]float32{}, map[string][]string{})
	if acc.HasProjectionMatrix() {
		t.Error("projection matrix should not be computed for empty maps")
	}
}

func TestFINGERAccelerator_PrecomputeResidualsSkipsMissingVectors(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0, 0.0},
	}

	neighbors := map[string][]string{
		"a": {"b", "c"},
		"x": {"a"},
	}

	acc.PrecomputeResiduals(vectors, neighbors)

	if acc.GetEdgeResidualCount() != 0 {
		t.Errorf("expected 0 edge residuals for missing vectors, got %d", acc.GetEdgeResidualCount())
	}
}

func TestFINGERAccelerator_Project(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0, 0.0},
		"b": {0.0, 1.0, 0.0, 0.0},
		"c": {0.0, 0.0, 1.0, 0.0},
		"d": {0.0, 0.0, 0.0, 1.0},
		"e": {0.5, 0.5, 0.5, 0.5},
	}

	neighbors := map[string][]string{
		"a": {"b", "c", "d"},
		"b": {"a", "c", "d"},
		"c": {"a", "b", "d"},
	}

	acc.PrecomputeResiduals(vectors, neighbors)

	query := []float32{0.25, 0.25, 0.25, 0.25}

	acc.mu.RLock()
	projected := acc.project(query)
	acc.mu.RUnlock()

	if len(projected) != 2 {
		t.Errorf("expected projected length 2, got %d", len(projected))
	}
}

func TestFINGERAccelerator_ProjectWithoutMatrix(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	query := []float32{1.0, 0.0, 0.0, 0.0}

	acc.mu.RLock()
	projected := acc.project(query)
	acc.mu.RUnlock()

	if projected != nil {
		t.Error("project should return nil when no projection matrix")
	}
}

func TestFINGERAccelerator_EstimateAngle(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	tests := []struct {
		name          string
		a             []float64
		b             []float64
		expectedAngle float64
		tolerance     float64
	}{
		{
			name:          "parallel vectors",
			a:             []float64{1.0, 0.0, 0.0},
			b:             []float64{2.0, 0.0, 0.0},
			expectedAngle: 0.0,
			tolerance:     0.01,
		},
		{
			name:          "perpendicular vectors",
			a:             []float64{1.0, 0.0, 0.0},
			b:             []float64{0.0, 1.0, 0.0},
			expectedAngle: 0.5,
			tolerance:     0.01,
		},
		{
			name:          "opposite vectors",
			a:             []float64{1.0, 0.0, 0.0},
			b:             []float64{-1.0, 0.0, 0.0},
			expectedAngle: 1.0,
			tolerance:     0.01,
		},
		{
			name:          "45 degree angle",
			a:             []float64{1.0, 0.0},
			b:             []float64{1.0, 1.0},
			expectedAngle: 0.25,
			tolerance:     0.01,
		},
		{
			name:          "empty vectors",
			a:             []float64{},
			b:             []float64{},
			expectedAngle: 0.0,
			tolerance:     0.01,
		},
		{
			name:          "zero magnitude vector",
			a:             []float64{0.0, 0.0, 0.0},
			b:             []float64{1.0, 0.0, 0.0},
			expectedAngle: 0.0,
			tolerance:     0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			angle := acc.estimateAngle(tt.a, tt.b)
			if math.Abs(angle-tt.expectedAngle) > tt.tolerance {
				t.Errorf("expected angle ~%f, got %f", tt.expectedAngle, angle)
			}
		})
	}
}

func TestFINGERAccelerator_ShouldSkipCandidate(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0, 0.0},
		"b": {0.0, 1.0, 0.0, 0.0},
		"c": {0.0, 0.0, 1.0, 0.0},
		"d": {0.0, 0.0, 0.0, 1.0},
		"e": {0.5, 0.5, 0.0, 0.0},
		"f": {0.0, 0.5, 0.5, 0.0},
	}

	neighbors := map[string][]string{
		"a": {"b", "c", "d"},
		"b": {"a", "c", "d", "e", "f"},
		"c": {"a", "b", "d"},
	}

	acc.PrecomputeResiduals(vectors, neighbors)

	skip := acc.ShouldSkipCandidate(vectors["a"], 0.5, "a", "b")
	t.Logf("ShouldSkipCandidate(a->b) = %v", skip)

	skip = acc.ShouldSkipCandidate(vectors["a"], 0.5, "x", "y")
	if skip {
		t.Error("should not skip unknown edge")
	}
}

func TestFINGERAccelerator_ShouldSkipWithoutMatrix(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	query := []float32{1.0, 0.0, 0.0, 0.0}
	skip := acc.ShouldSkipCandidate(query, 0.5, "a", "b")

	if skip {
		t.Error("should not skip without projection matrix")
	}
}

func TestFINGERAccelerator_SetSkipThreshold(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	if acc.GetSkipThreshold() != 0.7 {
		t.Errorf("expected default threshold 0.7, got %f", acc.GetSkipThreshold())
	}

	acc.SetSkipThreshold(0.5)
	if acc.GetSkipThreshold() != 0.5 {
		t.Errorf("expected threshold 0.5, got %f", acc.GetSkipThreshold())
	}

	acc.SetSkipThreshold(0.9)
	if acc.GetSkipThreshold() != 0.9 {
		t.Errorf("expected threshold 0.9, got %f", acc.GetSkipThreshold())
	}
}

func TestFINGERAccelerator_Clear(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0, 0.0},
		"b": {0.0, 1.0, 0.0, 0.0},
		"c": {0.0, 0.0, 1.0, 0.0},
		"d": {0.0, 0.0, 0.0, 1.0},
	}

	neighbors := map[string][]string{
		"a": {"b", "c", "d"},
		"b": {"a", "c"},
	}

	acc.PrecomputeResiduals(vectors, neighbors)

	if !acc.HasProjectionMatrix() || acc.GetEdgeResidualCount() == 0 {
		t.Fatal("accelerator should have data before clear")
	}

	acc.Clear()

	if acc.HasProjectionMatrix() {
		t.Error("projection matrix should be nil after clear")
	}
	if acc.GetEdgeResidualCount() != 0 {
		t.Errorf("edge residuals should be empty after clear, got %d", acc.GetEdgeResidualCount())
	}
}

func TestFINGERAccelerator_ThreadSafety(t *testing.T) {
	acc := NewFINGERAccelerator(8, 4)

	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0},
		"b": {0.0, 1.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0},
		"c": {0.0, 0.0, 1.0, 0.0, 0.0, 0.0, 0.0, 0.0},
		"d": {0.0, 0.0, 0.0, 1.0, 0.0, 0.0, 0.0, 0.0},
		"e": {0.0, 0.0, 0.0, 0.0, 1.0, 0.0, 0.0, 0.0},
		"f": {0.0, 0.0, 0.0, 0.0, 0.0, 1.0, 0.0, 0.0},
	}

	neighbors := map[string][]string{
		"a": {"b", "c", "d", "e", "f"},
		"b": {"a", "c", "d", "e", "f"},
		"c": {"a", "b", "d", "e", "f"},
	}

	acc.PrecomputeResiduals(vectors, neighbors)

	var wg sync.WaitGroup
	numGoroutines := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			query := []float32{float32(id % 8), 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7}
			_ = acc.ShouldSkipCandidate(query, 0.5, "a", "b")
			_ = acc.GetSkipThreshold()
			_ = acc.HasProjectionMatrix()
			_ = acc.GetEdgeResidualCount()

			if id%10 == 0 {
				acc.SetSkipThreshold(0.5 + float64(id%5)*0.1)
			}
		}(i)
	}

	wg.Wait()
}

func TestFINGERAccelerator_LargeGraph(t *testing.T) {
	dim := 64
	numNodes := 100
	lowRank := 16

	acc := NewFINGERAccelerator(dim, lowRank)

	vectors := make(map[string][]float32, numNodes)
	for i := 0; i < numNodes; i++ {
		vec := make([]float32, dim)
		for j := 0; j < dim; j++ {
			vec[j] = float32(i*dim+j) / float32(numNodes*dim)
		}
		vectors[string(rune('a'+i%26))+string(rune('0'+i/26))] = vec
	}

	neighbors := make(map[string][]string)
	ids := make([]string, 0, numNodes)
	for id := range vectors {
		ids = append(ids, id)
	}

	for i, id := range ids {
		neighborList := make([]string, 0, 5)
		for j := 1; j <= 5 && i+j < len(ids); j++ {
			neighborList = append(neighborList, ids[(i+j)%len(ids)])
		}
		neighbors[id] = neighborList
	}

	acc.PrecomputeResiduals(vectors, neighbors)

	if !acc.HasProjectionMatrix() {
		t.Error("projection matrix should be computed for large graph")
	}

	expectedResiduals := 0
	for _, n := range neighbors {
		expectedResiduals += len(n)
	}
	if acc.GetEdgeResidualCount() != expectedResiduals {
		t.Errorf("expected %d residuals, got %d", expectedResiduals, acc.GetEdgeResidualCount())
	}
}

func TestFINGERAccelerator_SkipThresholdBehavior(t *testing.T) {
	acc := NewFINGERAccelerator(4, 2)

	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0, 0.0},
		"b": {-1.0, 0.0, 0.0, 0.0},
		"c": {0.0, 1.0, 0.0, 0.0},
		"d": {0.0, 0.0, 1.0, 0.0},
		"e": {0.0, 0.0, 0.0, 1.0},
	}

	neighbors := map[string][]string{
		"a": {"b", "c", "d", "e"},
		"b": {"a", "c", "d", "e"},
	}

	acc.PrecomputeResiduals(vectors, neighbors)

	acc.SetSkipThreshold(0.0)
	conservativeSkips := 0
	for fromID, toIDs := range neighbors {
		for _, toID := range toIDs {
			if acc.ShouldSkipCandidate(vectors["a"], 0.5, fromID, toID) {
				conservativeSkips++
			}
		}
	}

	acc.SetSkipThreshold(1.0)
	aggressiveSkips := 0
	for fromID, toIDs := range neighbors {
		for _, toID := range toIDs {
			if acc.ShouldSkipCandidate(vectors["a"], 0.5, fromID, toID) {
				aggressiveSkips++
			}
		}
	}

	if aggressiveSkips > conservativeSkips {
		t.Errorf("higher threshold should skip fewer: aggressive=%d, conservative=%d",
			aggressiveSkips, conservativeSkips)
	}
}
