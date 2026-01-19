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
