package coordinator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock Implementations
// =============================================================================

type mockBleveSearcher struct {
	isOpen  bool
	results *search.SearchResult
	err     error
	delay   time.Duration
}

func (m *mockBleveSearcher) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.results, m.err
}

func (m *mockBleveSearcher) IsOpen() bool {
	return m.isOpen
}

type mockVectorSearcher struct {
	results []ScoredVectorResult
	err     error
	delay   time.Duration
}

func (m *mockVectorSearcher) Search(ctx context.Context, vector []float32, limit int) ([]ScoredVectorResult, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.results, m.err
}

type mockEmbeddingGenerator struct {
	embedding []float32
	err       error
}

func (m *mockEmbeddingGenerator) Generate(ctx context.Context, text string) ([]float32, error) {
	return m.embedding, m.err
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewSearchCoordinator(t *testing.T) {
	bleve := &mockBleveSearcher{isOpen: true}
	vector := &mockVectorSearcher{}
	embedder := &mockEmbeddingGenerator{}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	assert.NotNil(t, coord)
	assert.False(t, coord.IsClosed())
	assert.Equal(t, DefaultRRFK, coord.Config().RRFK)
}

func TestNewSearchCoordinatorWithConfig(t *testing.T) {
	bleve := &mockBleveSearcher{isOpen: true}
	vector := &mockVectorSearcher{}
	embedder := &mockEmbeddingGenerator{}

	config := CoordinatorConfig{
		MaxConcurrentSearches: 10,
		RRFK:                  100,
		EnableFallback:        true,
	}

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)

	assert.NotNil(t, coord)
	assert.Equal(t, 100, coord.Config().RRFK)
	assert.True(t, coord.Config().EnableFallback)
}

func TestNewSearchCoordinator_NilDependencies(t *testing.T) {
	// Should not panic with nil dependencies
	coord := NewSearchCoordinator(nil, nil, nil)
	assert.NotNil(t, coord)
}

// =============================================================================
// Search Tests
// =============================================================================

func TestSearchCoordinator_Search_BothSucceed(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
				{Document: search.Document{ID: "doc2"}, Score: 0.8},
			},
		},
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
			{ID: "doc3", Score: 0.7},
		},
	}

	embedder := &mockEmbeddingGenerator{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query:       "test query",
		Limit:       10,
		BleveWeight: 0.5,
		VectorWeight: 0.5,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.FusedResults)
	assert.False(t, result.Metadata.BleveFailed)
	assert.False(t, result.Metadata.VectorFailed)
}

func TestSearchCoordinator_Search_BleveFails_VectorSucceeds(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		err:    errors.New("bleve error"),
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc1", Score: 0.9},
		},
	}

	embedder := &mockEmbeddingGenerator{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	config := DefaultCoordinatorConfig()
	config.EnableFallback = true

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)

	req := &HybridSearchRequest{
		Query: "test",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.True(t, result.Metadata.BleveFailed)
	assert.False(t, result.Metadata.VectorFailed)
}

func TestSearchCoordinator_Search_VectorFails_BleveSucceeds(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	vector := &mockVectorSearcher{
		err: errors.New("vector error"),
	}

	embedder := &mockEmbeddingGenerator{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	config := DefaultCoordinatorConfig()
	config.EnableFallback = true

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)

	req := &HybridSearchRequest{
		Query: "test",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.False(t, result.Metadata.BleveFailed)
	assert.True(t, result.Metadata.VectorFailed)
}

func TestSearchCoordinator_Search_BothFail_NoFallback(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		err:    errors.New("bleve error"),
	}

	vector := &mockVectorSearcher{
		err: errors.New("vector error"),
	}

	embedder := &mockEmbeddingGenerator{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	config := DefaultCoordinatorConfig()
	config.EnableFallback = false

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)

	req := &HybridSearchRequest{
		Query: "test",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	assert.ErrorIs(t, err, ErrBothSearchersFailed)
	assert.Nil(t, result)
}

func TestSearchCoordinator_Search_EmptyQuery(t *testing.T) {
	bleve := &mockBleveSearcher{isOpen: true}
	vector := &mockVectorSearcher{}
	embedder := &mockEmbeddingGenerator{}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query: "",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	assert.ErrorIs(t, err, ErrEmptyQuery)
	assert.Nil(t, result)
}

func TestSearchCoordinator_Search_Closed(t *testing.T) {
	bleve := &mockBleveSearcher{isOpen: true}
	vector := &mockVectorSearcher{}
	embedder := &mockEmbeddingGenerator{}

	coord := NewSearchCoordinator(bleve, vector, embedder)
	coord.Close()

	req := &HybridSearchRequest{
		Query: "test",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	assert.ErrorIs(t, err, ErrCoordinatorClosed)
	assert.Nil(t, result)
}

func TestSearchCoordinator_Search_Timeout(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		delay:  500 * time.Millisecond,
	}

	vector := &mockVectorSearcher{
		delay: 500 * time.Millisecond,
	}

	embedder := &mockEmbeddingGenerator{
		embedding: []float32{0.1, 0.2, 0.3},
	}

	// Default config has fallback enabled, so timeouts result in
	// a result with error metadata rather than an error return
	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query:   "test",
		Limit:   10,
		Timeout: 50 * time.Millisecond,
	}

	result, err := coord.Search(context.Background(), req)

	// With fallback enabled, we get a result with error metadata
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Metadata.BleveFailed)
	assert.True(t, result.Metadata.VectorFailed)
	assert.Contains(t, result.Metadata.BleveError, "deadline exceeded")
	assert.Contains(t, result.Metadata.VectorError, "deadline exceeded")
}

func TestSearchCoordinator_Search_WithQueryVector(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
	}

	// Embedder should not be called when QueryVector is provided
	embedder := &mockEmbeddingGenerator{
		err: errors.New("should not be called"),
	}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query:       "test",
		QueryVector: []float32{0.1, 0.2, 0.3},
		Limit:       10,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.NotNil(t, result)
}

// =============================================================================
// Fusion Method Tests
// =============================================================================

func TestSearchCoordinator_Search_FusionRRF(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
				{Document: search.Document{ID: "doc2"}, Score: 0.8},
			},
		},
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
			{ID: "doc3", Score: 0.7},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query:        "test",
		Limit:        10,
		FusionMethod: FusionRRF,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, FusionRRF, result.FusionMethod)
}

func TestSearchCoordinator_Search_FusionLinear(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query:        "test",
		Limit:        10,
		FusionMethod: FusionLinear,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, FusionLinear, result.FusionMethod)
}

func TestSearchCoordinator_Search_FusionMax(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query:        "test",
		Limit:        10,
		FusionMethod: FusionMax,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, FusionMax, result.FusionMethod)
}

// =============================================================================
// Include Results Tests
// =============================================================================

func TestSearchCoordinator_Search_IncludeBleveResults(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	vector := &mockVectorSearcher{}
	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query:              "test",
		Limit:              10,
		IncludeBleveResults: true,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.NotEmpty(t, result.BleveResults)
}

func TestSearchCoordinator_Search_IncludeVectorResults(t *testing.T) {
	bleve := &mockBleveSearcher{isOpen: true, results: &search.SearchResult{}}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc1", Score: 0.9},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query:               "test",
		Limit:               10,
		IncludeVectorResults: true,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.NotEmpty(t, result.VectorResults)
}

// =============================================================================
// Lifecycle Tests
// =============================================================================

func TestSearchCoordinator_Close(t *testing.T) {
	coord := NewSearchCoordinator(nil, nil, nil)

	err := coord.Close()
	assert.NoError(t, err)
	assert.True(t, coord.IsClosed())
}

func TestSearchCoordinator_Close_Idempotent(t *testing.T) {
	coord := NewSearchCoordinator(nil, nil, nil)

	err1 := coord.Close()
	err2 := coord.Close()

	assert.NoError(t, err1)
	assert.NoError(t, err2)
}

func TestSearchCoordinator_IsClosed(t *testing.T) {
	coord := NewSearchCoordinator(nil, nil, nil)

	assert.False(t, coord.IsClosed())
	coord.Close()
	assert.True(t, coord.IsClosed())
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestSearchCoordinator_ConcurrentSearches(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.MaxConcurrentSearches = 10

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)

	var wg sync.WaitGroup
	results := make([]*HybridSearchResult, 20)
	errors := make([]error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &HybridSearchRequest{
				Query: "test",
				Limit: 10,
			}
			results[idx], errors[idx] = coord.Search(context.Background(), req)
		}(i)
	}

	wg.Wait()

	successCount := 0
	for i := 0; i < 20; i++ {
		if errors[i] == nil && results[i] != nil {
			successCount++
		}
	}

	assert.Greater(t, successCount, 0)
}

func TestSearchCoordinator_ConcurrentClose(t *testing.T) {
	coord := NewSearchCoordinator(nil, nil, nil)

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			coord.Close()
		}()
	}

	wg.Wait()
	assert.True(t, coord.IsClosed())
}

// =============================================================================
// Metadata Tests
// =============================================================================

func TestSearchCoordinator_Search_MetadataPopulated(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query: "test",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.Greater(t, result.Metadata.TotalTime, time.Duration(0))
	assert.Equal(t, 1, result.Metadata.BleveHits)
	assert.Equal(t, 1, result.Metadata.VectorHits)
}

func TestSearchCoordinator_Search_QueryInResult(t *testing.T) {
	bleve := &mockBleveSearcher{isOpen: true, results: &search.SearchResult{}}
	vector := &mockVectorSearcher{}
	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	req := &HybridSearchRequest{
		Query: "my test query",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, "my test query", result.Query)
}

// =============================================================================
// Fusion Limit Tests (PF.3.9)
// =============================================================================

func TestCapFusionLimit_Normal(t *testing.T) {
	// Normal case: limit * multiplier is under max
	result := capFusionLimit(10)
	assert.Equal(t, 10*DefaultFusionMultiplier, result)
}

func TestCapFusionLimit_CapsAtMax(t *testing.T) {
	// When limit * multiplier exceeds max, cap at max
	hugeLimit := MaxFusionOverfetch + 1000
	result := capFusionLimit(hugeLimit)
	assert.Equal(t, MaxFusionOverfetch, result)
}

func TestCapFusionLimit_ExactlyAtMax(t *testing.T) {
	// When limit * multiplier equals max exactly
	exactLimit := MaxFusionOverfetch / DefaultFusionMultiplier
	result := capFusionLimit(exactLimit)
	assert.Equal(t, MaxFusionOverfetch, result)
}

func TestCapFusionLimit_JustBelowMax(t *testing.T) {
	// When limit * multiplier is just below max
	justBelowLimit := (MaxFusionOverfetch / DefaultFusionMultiplier) - 1
	result := capFusionLimit(justBelowLimit)
	expected := justBelowLimit * DefaultFusionMultiplier
	assert.Equal(t, expected, result)
}

func TestSelectTopK_SmallSlice(t *testing.T) {
	// When items <= k, should return all items sorted
	items := []search.ScoredDocument{
		{Document: search.Document{ID: "a"}, Score: 0.5},
		{Document: search.Document{ID: "b"}, Score: 0.9},
		{Document: search.Document{ID: "c"}, Score: 0.3},
	}

	result := selectTopK(items, 5)

	assert.Len(t, result, 3)
	assert.Equal(t, "b", result[0].ID)
	assert.Equal(t, "a", result[1].ID)
	assert.Equal(t, "c", result[2].ID)
}

func TestSelectTopK_LargeSlice(t *testing.T) {
	// When items > k, should return top k items sorted
	items := []search.ScoredDocument{
		{Document: search.Document{ID: "a"}, Score: 0.5},
		{Document: search.Document{ID: "b"}, Score: 0.9},
		{Document: search.Document{ID: "c"}, Score: 0.3},
		{Document: search.Document{ID: "d"}, Score: 0.7},
		{Document: search.Document{ID: "e"}, Score: 0.8},
	}

	result := selectTopK(items, 3)

	assert.Len(t, result, 3)
	assert.Equal(t, "b", result[0].ID)
	assert.Equal(t, 0.9, result[0].Score)
	assert.Equal(t, "e", result[1].ID)
	assert.Equal(t, 0.8, result[1].Score)
	assert.Equal(t, "d", result[2].ID)
	assert.Equal(t, 0.7, result[2].Score)
}

func TestSelectTopK_EmptySlice(t *testing.T) {
	items := []search.ScoredDocument{}
	result := selectTopK(items, 5)
	assert.Empty(t, result)
}

func TestSelectTopK_ZeroK(t *testing.T) {
	items := []search.ScoredDocument{
		{Document: search.Document{ID: "a"}, Score: 0.5},
	}
	result := selectTopK(items, 0)
	assert.Nil(t, result)
}

func TestSelectTopK_DuplicateScores(t *testing.T) {
	// Should handle duplicate scores correctly
	items := []search.ScoredDocument{
		{Document: search.Document{ID: "a"}, Score: 0.5},
		{Document: search.Document{ID: "b"}, Score: 0.5},
		{Document: search.Document{ID: "c"}, Score: 0.9},
		{Document: search.Document{ID: "d"}, Score: 0.5},
	}

	result := selectTopK(items, 3)

	assert.Len(t, result, 3)
	assert.Equal(t, "c", result[0].ID)
	assert.Equal(t, 0.9, result[0].Score)
	// The remaining two should be from {a, b, d} all with score 0.5
	assert.Equal(t, 0.5, result[1].Score)
	assert.Equal(t, 0.5, result[2].Score)
}

func TestSelectTopK_LargeDataset(t *testing.T) {
	// Test with a larger dataset to verify heap efficiency
	items := make([]search.ScoredDocument, 1000)
	for i := 0; i < 1000; i++ {
		items[i] = search.ScoredDocument{
			Document: search.Document{ID: string(rune('0' + i%10))},
			Score:    float64(i) / 1000.0,
		}
	}

	result := selectTopK(items, 10)

	assert.Len(t, result, 10)
	// Top 10 should be indices 990-999 with scores 0.990-0.999
	for i := 0; i < 10; i++ {
		expectedScore := float64(999-i) / 1000.0
		assert.Equal(t, expectedScore, result[i].Score)
	}
}

func TestSearchCoordinator_FusionLimitCapped(t *testing.T) {
	// Test that fusion limits are properly capped in actual search
	var capturedLimit int

	bleve := &mockBleveSearcher{
		isOpen:  true,
		results: &search.SearchResult{},
	}

	vector := &mockVectorSearcherWithCapture{
		results: []ScoredVectorResult{},
		captureLimit: func(limit int) {
			capturedLimit = limit
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinator(bleve, vector, embedder)

	// Use the maximum valid limit (100). With multiplier (2), this becomes 200.
	// Since 200 < MaxFusionOverfetch (1000), verify fusion multiplier is applied.
	req := &HybridSearchRequest{
		Query: "test",
		Limit: MaxLimit, // 100
	}

	_, err := coord.Search(context.Background(), req)
	require.NoError(t, err)

	// Vector search should have received limit * multiplier = 200
	assert.Equal(t, MaxLimit*DefaultFusionMultiplier, capturedLimit)
}

func TestSearchCoordinator_FusionLimitCapped_ViaCapFunction(t *testing.T) {
	// Test capFusionLimit directly for the capping behavior
	// When limit * multiplier would exceed MaxFusionOverfetch,
	// it should be capped

	// This is tested via TestCapFusionLimit_CapsAtMax, but we can verify
	// the behavior through the cap function too
	hugeLimit := MaxFusionOverfetch
	result := capFusionLimit(hugeLimit)
	// hugeLimit * 2 = 2000 which exceeds MaxFusionOverfetch (1000)
	assert.Equal(t, MaxFusionOverfetch, result)
}

// mockVectorSearcherWithCapture captures the limit passed to Search
type mockVectorSearcherWithCapture struct {
	results      []ScoredVectorResult
	err          error
	captureLimit func(int)
}

func (m *mockVectorSearcherWithCapture) Search(ctx context.Context, vector []float32, limit int) ([]ScoredVectorResult, error) {
	if m.captureLimit != nil {
		m.captureLimit(limit)
	}
	return m.results, m.err
}
