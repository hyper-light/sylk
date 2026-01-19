package query

import (
	"context"
	"errors"
	"testing"
	"time"
)

// =============================================================================
// Mock HNSW Index
// =============================================================================

// mockHNSWIndex implements HNSWIndex for testing.
type mockHNSWIndex struct {
	searchFunc func(vector []float32, k int) (ids []string, distances []float32, err error)
}

func (m *mockHNSWIndex) Search(vector []float32, k int) (ids []string, distances []float32, err error) {
	if m.searchFunc != nil {
		return m.searchFunc(vector, k)
	}
	return nil, nil, nil
}

// =============================================================================
// VectorSearcher Constructor Tests
// =============================================================================

func TestNewVectorSearcher(t *testing.T) {
	mock := &mockHNSWIndex{}
	searcher := NewVectorSearcher(mock)

	if searcher == nil {
		t.Fatal("NewVectorSearcher returned nil")
	}

	if !searcher.IsReady() {
		t.Error("expected IsReady() to return true")
	}
}

func TestNewVectorSearcher_NilIndex(t *testing.T) {
	searcher := NewVectorSearcher(nil)

	if searcher == nil {
		t.Fatal("NewVectorSearcher returned nil")
	}

	if searcher.IsReady() {
		t.Error("expected IsReady() to return false for nil index")
	}
}

// =============================================================================
// VectorSearcher Execute Tests
// =============================================================================

func TestVectorSearcher_Execute_Success(t *testing.T) {
	mock := &mockHNSWIndex{
		searchFunc: func(vector []float32, k int) ([]string, []float32, error) {
			return []string{"node1", "node2", "node3"},
				[]float32{0.1, 0.2, 0.5},
				nil
		},
	}

	searcher := NewVectorSearcher(mock)
	ctx := context.Background()
	vector := []float32{0.1, 0.2, 0.3}

	results, err := searcher.Execute(ctx, vector, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Check first result
	if results[0].ID != "node1" {
		t.Errorf("results[0].ID = %v, want node1", results[0].ID)
	}
	if results[0].Distance != 0.1 {
		t.Errorf("results[0].Distance = %v, want 0.1", results[0].Distance)
	}
	// Score should be 1/(1+0.1) = 0.909... (use tolerance for float comparison)
	expectedScore := 1.0 / (1.0 + 0.1)
	scoreDiff := results[0].Score - expectedScore
	if scoreDiff < 0 {
		scoreDiff = -scoreDiff
	}
	if scoreDiff > 0.0001 {
		t.Errorf("results[0].Score = %v, want ~%v", results[0].Score, expectedScore)
	}

	// Check second result
	if results[1].ID != "node2" {
		t.Errorf("results[1].ID = %v, want node2", results[1].ID)
	}
	if results[1].Distance != 0.2 {
		t.Errorf("results[1].Distance = %v, want 0.2", results[1].Distance)
	}
}

func TestVectorSearcher_Execute_NilIndex(t *testing.T) {
	searcher := NewVectorSearcher(nil)
	ctx := context.Background()
	vector := []float32{0.1, 0.2, 0.3}

	results, err := searcher.Execute(ctx, vector, 10)
	if err != nil {
		t.Fatalf("Execute() should not return error for nil index, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for nil index, got %d", len(results))
	}
}

func TestVectorSearcher_Execute_EmptyVector(t *testing.T) {
	mock := &mockHNSWIndex{}
	searcher := NewVectorSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, []float32{}, 10)
	if err != nil {
		t.Fatalf("Execute() should not return error for empty vector, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for empty vector, got %d", len(results))
	}
}

func TestVectorSearcher_Execute_NilVector(t *testing.T) {
	mock := &mockHNSWIndex{}
	searcher := NewVectorSearcher(mock)
	ctx := context.Background()

	results, err := searcher.Execute(ctx, nil, 10)
	if err != nil {
		t.Fatalf("Execute() should not return error for nil vector, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results for nil vector, got %d", len(results))
	}
}

func TestVectorSearcher_Execute_ZeroLimit(t *testing.T) {
	searchCalled := false
	mock := &mockHNSWIndex{
		searchFunc: func(vector []float32, k int) ([]string, []float32, error) {
			searchCalled = true
			// With zero limit, should default to 10
			if k != 10 {
				t.Errorf("expected default limit of 10, got %d", k)
			}
			return nil, nil, nil
		},
	}

	searcher := NewVectorSearcher(mock)
	ctx := context.Background()
	vector := []float32{0.1, 0.2, 0.3}

	_, err := searcher.Execute(ctx, vector, 0)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !searchCalled {
		t.Error("expected search to be called")
	}
}

func TestVectorSearcher_Execute_NegativeLimit(t *testing.T) {
	searchCalled := false
	mock := &mockHNSWIndex{
		searchFunc: func(vector []float32, k int) ([]string, []float32, error) {
			searchCalled = true
			// With negative limit, should default to 10
			if k != 10 {
				t.Errorf("expected default limit of 10, got %d", k)
			}
			return nil, nil, nil
		},
	}

	searcher := NewVectorSearcher(mock)
	ctx := context.Background()
	vector := []float32{0.1, 0.2, 0.3}

	_, err := searcher.Execute(ctx, vector, -5)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if !searchCalled {
		t.Error("expected search to be called")
	}
}

func TestVectorSearcher_Execute_SearchError_GracefulDegradation(t *testing.T) {
	mock := &mockHNSWIndex{
		searchFunc: func(vector []float32, k int) ([]string, []float32, error) {
			return nil, nil, errors.New("HNSW search failed")
		},
	}

	searcher := NewVectorSearcher(mock)
	ctx := context.Background()
	vector := []float32{0.1, 0.2, 0.3}

	results, err := searcher.Execute(ctx, vector, 10)

	// Should return empty results, not error (graceful degradation)
	if err != nil {
		t.Errorf("Execute() should return nil error for graceful degradation, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results on search error, got %d", len(results))
	}
}

func TestVectorSearcher_Execute_ContextCancelled(t *testing.T) {
	mock := &mockHNSWIndex{}
	searcher := NewVectorSearcher(mock)
	vector := []float32{0.1, 0.2, 0.3}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	results, err := searcher.Execute(ctx, vector, 10)

	if err != context.Canceled {
		t.Errorf("Execute() should return context.Canceled, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results on context cancel, got %d", len(results))
	}
}

func TestVectorSearcher_Execute_ContextTimeout(t *testing.T) {
	mock := &mockHNSWIndex{
		searchFunc: func(vector []float32, k int) ([]string, []float32, error) {
			// Simulate slow search
			time.Sleep(100 * time.Millisecond)
			return []string{"node1"}, []float32{0.1}, nil
		},
	}

	searcher := NewVectorSearcher(mock)
	vector := []float32{0.1, 0.2, 0.3}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// The search may complete or timeout depending on timing
	results, err := searcher.Execute(ctx, vector, 10)

	// Either timeout error or normal result is acceptable
	if err != nil && err != context.DeadlineExceeded {
		t.Errorf("unexpected error: %v", err)
	}

	// Results should be empty on timeout or normal otherwise
	_ = results
}

func TestVectorSearcher_Execute_EmptyResults(t *testing.T) {
	mock := &mockHNSWIndex{
		searchFunc: func(vector []float32, k int) ([]string, []float32, error) {
			return []string{}, []float32{}, nil
		},
	}

	searcher := NewVectorSearcher(mock)
	ctx := context.Background()
	vector := []float32{0.1, 0.2, 0.3}

	results, err := searcher.Execute(ctx, vector, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestVectorSearcher_Execute_MismatchedArrays(t *testing.T) {
	mock := &mockHNSWIndex{
		searchFunc: func(vector []float32, k int) ([]string, []float32, error) {
			// Return mismatched arrays - more IDs than distances
			return []string{"node1", "node2", "node3"}, []float32{0.1}, nil
		},
	}

	searcher := NewVectorSearcher(mock)
	ctx := context.Background()
	vector := []float32{0.1, 0.2, 0.3}

	results, err := searcher.Execute(ctx, vector, 10)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Should handle gracefully
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First result should have correct distance
	if results[0].Distance != 0.1 {
		t.Errorf("results[0].Distance = %v, want 0.1", results[0].Distance)
	}

	// Second and third should have zero distance
	if results[1].Distance != 0 {
		t.Errorf("results[1].Distance = %v, want 0", results[1].Distance)
	}
	if results[2].Distance != 0 {
		t.Errorf("results[2].Distance = %v, want 0", results[2].Distance)
	}
}

// =============================================================================
// VectorResult Tests
// =============================================================================

func TestVectorResult_Fields(t *testing.T) {
	result := VectorResult{
		ID:       "test-id",
		Score:    0.909,
		Distance: 0.1,
	}

	if result.ID != "test-id" {
		t.Errorf("ID = %v, want test-id", result.ID)
	}
	if result.Score != 0.909 {
		t.Errorf("Score = %v, want 0.909", result.Score)
	}
	if result.Distance != 0.1 {
		t.Errorf("Distance = %v, want 0.1", result.Distance)
	}
}

// =============================================================================
// VectorSearcher Distance-to-Score Conversion Tests
// =============================================================================

func TestVectorSearcher_DistanceToScore(t *testing.T) {
	searcher := NewVectorSearcher(nil)

	tests := []struct {
		name           string
		distance       float32
		expectedScore  float64
		toleranceDelta float64
	}{
		{"zero_distance", 0, 1.0, 0.001},
		{"small_distance", 0.1, 0.909, 0.001},
		{"medium_distance", 0.5, 0.667, 0.001},
		{"large_distance", 1.0, 0.5, 0.001},
		{"very_large_distance", 9.0, 0.1, 0.001},
		{"negative_distance_clamped", -0.5, 1.0, 0.001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := searcher.distanceToScore(tt.distance)
			diff := score - tt.expectedScore
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.toleranceDelta {
				t.Errorf("distanceToScore(%v) = %v, want ~%v", tt.distance, score, tt.expectedScore)
			}
		})
	}
}

// =============================================================================
// VectorSearcher IsReady Tests
// =============================================================================

func TestVectorSearcher_IsReady(t *testing.T) {
	tests := []struct {
		name     string
		hnsw     HNSWIndex
		expected bool
	}{
		{"with_index", &mockHNSWIndex{}, true},
		{"nil_index", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			searcher := NewVectorSearcher(tt.hnsw)
			if got := searcher.IsReady(); got != tt.expected {
				t.Errorf("IsReady() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// VectorSearcher Limit Propagation Tests
// =============================================================================

func TestVectorSearcher_Execute_LimitPropagation(t *testing.T) {
	tests := []struct {
		name          string
		requestLimit  int
		expectedLimit int
	}{
		{"normal_limit", 20, 20},
		{"small_limit", 5, 5},
		{"zero_limit", 0, 10},
		{"negative_limit", -5, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockHNSWIndex{
				searchFunc: func(vector []float32, k int) ([]string, []float32, error) {
					if k != tt.expectedLimit {
						t.Errorf("k = %d, want %d", k, tt.expectedLimit)
					}
					return nil, nil, nil
				},
			}

			searcher := NewVectorSearcher(mock)
			ctx := context.Background()
			vector := []float32{0.1, 0.2, 0.3}

			_, _ = searcher.Execute(ctx, vector, tt.requestLimit)
		})
	}
}
