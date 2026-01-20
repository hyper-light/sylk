package coordinator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers for W4N.9
// =============================================================================

// newTestBudget creates a GoroutineBudget for testing with generous limits.
func newTestBudget() *concurrency.GoroutineBudget {
	pressure := &atomic.Int32{}
	budget := concurrency.NewGoroutineBudget(pressure)
	budget.RegisterAgent("coordinator-test", "engineer")
	return budget
}

// newTestScope creates a GoroutineScope for testing.
func newTestScope(ctx context.Context) *concurrency.GoroutineScope {
	budget := newTestBudget()
	return concurrency.NewGoroutineScope(ctx, "coordinator-test", budget)
}

// =============================================================================
// W4N.9 Happy Path Tests
// =============================================================================

func TestW4N9_SearchParallel_WithScope_TracksGoroutines(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	bleveSearched := make(chan struct{})
	vectorSearched := make(chan struct{})

	bleve := &mockBleveSearcherWithCallback{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
		delay:    10 * time.Millisecond,
		onSearch: func() { close(bleveSearched) },
	}

	vector := &mockVectorSearcherWithCallback{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
		delay:    10 * time.Millisecond,
		onSearch: func() { close(vectorSearched) },
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query:   "test query",
		Limit:   10,
		Timeout: 5 * time.Second,
	}

	result, err := coord.Search(ctx, req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.FusedResults)

	// Verify both searches were executed
	select {
	case <-bleveSearched:
	case <-time.After(time.Second):
		t.Fatal("bleve search was not executed")
	}

	select {
	case <-vectorSearched:
	case <-time.After(time.Second):
		t.Fatal("vector search was not executed")
	}

	// After search completes, workers should be cleaned up
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, scope.WorkerCount(), "workers should be cleaned up after search")
}

func TestW4N9_SearchParallel_WithScope_BothSearchersSucceed(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

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
			{ID: "doc2", Score: 0.95},
			{ID: "doc3", Score: 0.7},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1, 0.2}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query:        "test",
		Limit:        10,
		FusionMethod: FusionRRF,
	}

	result, err := coord.Search(ctx, req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.False(t, result.Metadata.BleveFailed)
	assert.False(t, result.Metadata.VectorFailed)
	assert.Equal(t, 2, result.Metadata.BleveHits)
	assert.Equal(t, 2, result.Metadata.VectorHits)
}

// =============================================================================
// W4N.9 Negative Path Tests - Scope nil fallback
// =============================================================================

func TestW4N9_SearchParallel_NilScope_FallsBackToWaitGroup(t *testing.T) {
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
	config.Scope = nil // Explicitly nil

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query: "test",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotEmpty(t, result.FusedResults)
}

func TestW4N9_SearchParallel_NilScope_BothSearchersSucceed(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "a"}, Score: 1.0},
				{Document: search.Document{ID: "b"}, Score: 0.5},
			},
		},
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "b", Score: 0.9},
			{ID: "c", Score: 0.6},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	// No scope configured (default)
	coord := NewSearchCoordinator(bleve, vector, embedder)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query: "fallback test",
		Limit: 10,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.NotEmpty(t, result.FusedResults)
	assert.Equal(t, 2, result.Metadata.BleveHits)
	assert.Equal(t, 2, result.Metadata.VectorHits)
}

// =============================================================================
// W4N.9 Failure Path Tests - Timeout Propagation
// =============================================================================

func TestW4N9_SearchTimeout_PropagatesCorrectly_WithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	bleve := &mockBleveSearcher{
		isOpen: true,
		delay:  500 * time.Millisecond, // Slow
	}

	vector := &mockVectorSearcher{
		delay: 500 * time.Millisecond, // Slow
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope
	config.EnableFallback = true

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query:   "timeout test",
		Limit:   10,
		Timeout: 50 * time.Millisecond, // Short timeout
	}

	result, err := coord.Search(ctx, req)

	// With fallback enabled, we get a result with error metadata
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Metadata.BleveFailed)
	assert.True(t, result.Metadata.VectorFailed)
	assert.Contains(t, result.Metadata.BleveError, "deadline exceeded")
	assert.Contains(t, result.Metadata.VectorError, "deadline exceeded")
}

func TestW4N9_SearchTimeout_PropagatesCorrectly_WithoutScope(t *testing.T) {
	bleve := &mockBleveSearcher{
		isOpen: true,
		delay:  500 * time.Millisecond,
	}

	vector := &mockVectorSearcher{
		delay: 500 * time.Millisecond,
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.EnableFallback = true

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query:   "timeout test",
		Limit:   10,
		Timeout: 50 * time.Millisecond,
	}

	result, err := coord.Search(context.Background(), req)

	require.NoError(t, err)
	assert.True(t, result.Metadata.BleveFailed)
	assert.True(t, result.Metadata.VectorFailed)
}

// =============================================================================
// W4N.9 Race Condition Tests - Concurrent Searches with Scope
// =============================================================================

func TestW4N9_ConcurrentSearches_WithScope_NoRace(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
		delay: 5 * time.Millisecond,
	}

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
		delay: 5 * time.Millisecond,
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope
	config.MaxConcurrentSearches = 20

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	var wg sync.WaitGroup
	numSearches := 10
	results := make([]*HybridSearchResult, numSearches)
	errs := make([]error, numSearches)

	for i := 0; i < numSearches; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &HybridSearchRequest{
				Query:   "concurrent test",
				Limit:   10,
				Timeout: 5 * time.Second,
			}
			results[idx], errs[idx] = coord.Search(ctx, req)
		}(i)
	}

	wg.Wait()

	// All searches should succeed
	for i := 0; i < numSearches; i++ {
		assert.NoError(t, errs[i], "search %d should not fail", i)
		assert.NotNil(t, results[i], "search %d should have result", i)
	}

	// Workers should be cleaned up
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, scope.WorkerCount())
}

func TestW4N9_ConcurrentSearches_WithScope_MixedResults(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	callCount := &atomic.Int32{}

	bleve := &mockBleveSearcherWithCounter{
		isOpen:    true,
		callCount: callCount,
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
	config.Scope = scope

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	var wg sync.WaitGroup
	numSearches := 5

	for i := 0; i < numSearches; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &HybridSearchRequest{
				Query: "concurrent mixed",
				Limit: 10,
			}
			coord.Search(ctx, req)
		}()
	}

	wg.Wait()

	// Verify all searches were executed
	assert.Equal(t, int32(numSearches), callCount.Load())
}

// =============================================================================
// W4N.9 Edge Case Tests
// =============================================================================

func TestW4N9_ScopeShutdown_DuringSearch(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)

	searchStarted := make(chan struct{})
	bleve := &mockBleveSearcherWithCallback{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
		delay:    200 * time.Millisecond,
		onSearch: func() { close(searchStarted) },
	}

	vector := &mockVectorSearcherWithCallback{
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
		delay: 200 * time.Millisecond,
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope
	config.EnableFallback = true

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	// Start search in background
	searchDone := make(chan struct{})
	var searchResult *HybridSearchResult
	var searchErr error
	go func() {
		defer close(searchDone)
		req := &HybridSearchRequest{
			Query:   "shutdown test",
			Limit:   10,
			Timeout: 5 * time.Second,
		}
		searchResult, searchErr = coord.Search(ctx, req)
	}()

	// Wait for search to start
	<-searchStarted

	// Shutdown scope while search is in progress
	err := scope.Shutdown(50*time.Millisecond, 100*time.Millisecond)
	// Shutdown may return nil or a leak error depending on timing
	_ = err

	// Wait for search to complete
	<-searchDone

	// Search should complete (possibly with errors due to cancellation)
	// With fallback enabled, it should return a result even if searches failed
	if searchErr == nil {
		assert.NotNil(t, searchResult)
	}
}

func TestW4N9_NilSearchers_WithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope
	config.EnableFallback = true

	// Both searchers are nil
	coord := NewSearchCoordinatorWithConfig(nil, nil, embedder, config)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query:       "nil searchers",
		QueryVector: []float32{0.1, 0.2}, // Provide vector to skip embedder call
		Limit:       10,
	}

	result, err := coord.Search(ctx, req)

	// With fallback enabled, should return result with both failed
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Metadata.BleveFailed)
	assert.True(t, result.Metadata.VectorFailed)
}

func TestW4N9_NilBleveSearcher_WithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	vector := &mockVectorSearcher{
		results: []ScoredVectorResult{
			{ID: "doc1", Score: 0.9},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope
	config.EnableFallback = true

	coord := NewSearchCoordinatorWithConfig(nil, vector, embedder, config)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query: "nil bleve",
		Limit: 10,
	}

	result, err := coord.Search(ctx, req)

	require.NoError(t, err)
	assert.True(t, result.Metadata.BleveFailed)
	assert.False(t, result.Metadata.VectorFailed)
	assert.NotEmpty(t, result.FusedResults)
}

func TestW4N9_NilVectorSearcher_WithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	bleve := &mockBleveSearcher{
		isOpen: true,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope
	config.EnableFallback = true

	coord := NewSearchCoordinatorWithConfig(bleve, nil, embedder, config)
	defer coord.Close()

	req := &HybridSearchRequest{
		Query: "nil vector",
		Limit: 10,
	}

	result, err := coord.Search(ctx, req)

	require.NoError(t, err)
	assert.False(t, result.Metadata.BleveFailed)
	assert.True(t, result.Metadata.VectorFailed)
	assert.NotEmpty(t, result.FusedResults)
}

func TestW4N9_SearchAfterClose_WithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	bleve := &mockBleveSearcher{isOpen: true}
	vector := &mockVectorSearcher{}
	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := DefaultCoordinatorConfig()
	config.Scope = scope

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	coord.Close()

	req := &HybridSearchRequest{
		Query: "after close",
		Limit: 10,
	}

	result, err := coord.Search(ctx, req)

	assert.ErrorIs(t, err, ErrCoordinatorClosed)
	assert.Nil(t, result)
}

// =============================================================================
// W4N.9 Configuration Tests
// =============================================================================

func TestW4N9_CoordinatorConfig_WithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	config := CoordinatorConfig{
		DefaultTimeout:        10 * time.Second,
		RRFK:                  100,
		EnableFallback:        true,
		MaxConcurrentSearches: 5,
		EnableBackpressure:    false,
		Scope:                 scope,
	}

	bleve := &mockBleveSearcher{isOpen: true, results: &search.SearchResult{}}
	vector := &mockVectorSearcher{}
	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	assert.Equal(t, 100, coord.Config().RRFK)
	assert.True(t, coord.Config().EnableFallback)
	assert.Equal(t, 5, coord.Config().MaxConcurrentSearches)
	assert.NotNil(t, coord.Config().Scope)
}

func TestW4N9_DefaultConfig_HasNilScope(t *testing.T) {
	config := DefaultCoordinatorConfig()
	assert.Nil(t, config.Scope)
}

// =============================================================================
// Extended Mock Types for W4N.9 Tests
// =============================================================================

// mockBleveSearcherWithCounter tracks call count
type mockBleveSearcherWithCounter struct {
	isOpen    bool
	results   *search.SearchResult
	err       error
	callCount *atomic.Int32
}

func (m *mockBleveSearcherWithCounter) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	m.callCount.Add(1)
	return m.results, m.err
}

func (m *mockBleveSearcherWithCounter) IsOpen() bool {
	return m.isOpen
}

// mockBleveSearcherWithCallback extends mockBleveSearcher with callback
type mockBleveSearcherWithCallback struct {
	isOpen   bool
	results  *search.SearchResult
	err      error
	delay    time.Duration
	onSearch func()
}

func (m *mockBleveSearcherWithCallback) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	if m.onSearch != nil {
		m.onSearch()
	}
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.results, m.err
}

func (m *mockBleveSearcherWithCallback) IsOpen() bool {
	return m.isOpen
}

// mockVectorSearcherWithCallback has callback on search
type mockVectorSearcherWithCallback struct {
	results  []ScoredVectorResult
	err      error
	delay    time.Duration
	onSearch func()
}

func (m *mockVectorSearcherWithCallback) Search(ctx context.Context, vector []float32, limit int) ([]ScoredVectorResult, error) {
	if m.onSearch != nil {
		m.onSearch()
	}
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.results, m.err
}

// =============================================================================
// W4N.9 Scope Budget Exhaustion Test
// =============================================================================

func TestW4N9_ScopeBudgetExhausted_HandledGracefully(t *testing.T) {
	ctx := context.Background()

	// Create a budget with very limited capacity
	pressure := &atomic.Int32{}
	cfg := concurrency.GoroutineBudgetConfig{
		SystemLimit:     2, // Very limited
		BurstMultiplier: 1.0,
		TypeWeights:     map[string]float64{"engineer": 1.0},
	}
	budget := concurrency.NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("limited-agent", "engineer")

	scope := concurrency.NewGoroutineScope(ctx, "limited-agent", budget)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	// Block the budget with long-running workers
	blocker := make(chan struct{})
	for i := 0; i < 2; i++ {
		err := scope.Go("blocker", time.Minute, func(ctx context.Context) error {
			<-blocker
			return nil
		})
		if err != nil {
			// Budget might already be exhausted
			break
		}
	}

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
	config.Scope = scope
	config.EnableFallback = true

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	// Search should handle the budget exhaustion scenario
	// The scope.Go() will block waiting for budget, which is expected behavior
	// We use a short timeout to prevent hanging
	searchCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	req := &HybridSearchRequest{
		Query:   "budget test",
		Limit:   10,
		Timeout: 50 * time.Millisecond,
	}

	// This search may fail or timeout due to budget exhaustion
	result, err := coord.Search(searchCtx, req)

	// Release blockers
	close(blocker)

	// The search result depends on whether the budget blocked or the timeout hit first
	// Either outcome is acceptable - we're testing that it doesn't panic or deadlock
	if err != nil {
		// Expected - context timeout or semaphore timeout
		assert.True(t, errors.Is(err, ErrSearchTimeout) || errors.Is(err, context.DeadlineExceeded))
	} else if result != nil {
		// Also acceptable - the search may have completed with errors in metadata
		_ = result.Metadata.BleveFailed || result.Metadata.VectorFailed
	}
}

// =============================================================================
// W4N.9 Integration with Backpressure
// =============================================================================

func TestW4N9_Backpressure_WithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScope(ctx)
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	bleve := &mockBleveSearcher{
		isOpen: true,
		delay:  100 * time.Millisecond,
		results: &search.SearchResult{
			Documents: []search.ScoredDocument{
				{Document: search.Document{ID: "doc1"}, Score: 1.0},
			},
		},
	}

	vector := &mockVectorSearcher{
		delay: 100 * time.Millisecond,
		results: []ScoredVectorResult{
			{ID: "doc2", Score: 0.9},
		},
	}

	embedder := &mockEmbeddingGenerator{embedding: []float32{0.1}}

	config := CoordinatorConfig{
		MaxConcurrentSearches: 2,
		EnableBackpressure:    true,
		DefaultTimeout:        5 * time.Second,
		RRFK:                  60,
		EnableFallback:        true,
		Scope:                 scope,
	}

	coord := NewSearchCoordinatorWithConfig(bleve, vector, embedder, config)
	defer coord.Close()

	var wg sync.WaitGroup
	results := make(chan error, 10)

	// Launch many searches to trigger backpressure
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &HybridSearchRequest{
				Query:   "backpressure",
				Limit:   10,
				Timeout: 5 * time.Second,
			}
			_, err := coord.Search(ctx, req)
			results <- err
		}()
	}

	wg.Wait()
	close(results)

	// Count results
	successCount := 0
	queueFullCount := 0
	for err := range results {
		if err == nil {
			successCount++
		} else if errors.Is(err, ErrSearchQueueFull) {
			queueFullCount++
		}
	}

	// With backpressure and scope, some should succeed, some should be rejected
	assert.Greater(t, successCount, 0, "some searches should succeed")
	assert.Greater(t, queueFullCount, 0, "some searches should be rejected by backpressure")
}
