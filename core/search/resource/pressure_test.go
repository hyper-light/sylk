package resource

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/resources"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/coordinator"
)

// =============================================================================
// Mock Implementations
// =============================================================================

// mockSearcher implements the Searcher interface for testing.
type mockSearcher struct {
	mu           sync.Mutex
	searchCalls  int
	searchDelay  time.Duration
	searchResult *coordinator.HybridSearchResult
	searchErr    error
	closed       bool
}

func newMockSearcher() *mockSearcher {
	return &mockSearcher{
		searchResult: &coordinator.HybridSearchResult{
			FusedResults: []search.ScoredDocument{
				{Document: search.Document{ID: "1", Path: "/test.go"}},
			},
		},
	}
}

func (m *mockSearcher) Search(
	ctx context.Context,
	req *coordinator.HybridSearchRequest,
) (*coordinator.HybridSearchResult, error) {
	m.mu.Lock()
	m.searchCalls++
	delay := m.searchDelay
	result := m.searchResult
	err := m.searchErr
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return result, err
}

func (m *mockSearcher) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *mockSearcher) SetClosed(closed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = closed
}

func (m *mockSearcher) GetSearchCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.searchCalls
}

// mockIndexer implements the Indexer interface for testing.
type mockIndexer struct {
	mu         sync.Mutex
	indexCalls int
	batchCalls int
	indexErr   error
}

func newMockIndexer() *mockIndexer {
	return &mockIndexer{}
}

func (m *mockIndexer) Index(ctx context.Context, doc *search.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.indexCalls++
	return m.indexErr
}

func (m *mockIndexer) IndexBatch(ctx context.Context, docs []*search.Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batchCalls++
	return m.indexErr
}

func (m *mockIndexer) GetIndexCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.indexCalls
}

func (m *mockIndexer) GetBatchCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.batchCalls
}

// mockCache implements the CacheProvider interface for testing.
type mockCache struct {
	mu    sync.Mutex
	store map[string]*coordinator.HybridSearchResult
	gets  int
	sets  int
}

func newMockCache() *mockCache {
	return &mockCache{
		store: make(map[string]*coordinator.HybridSearchResult),
	}
}

func (m *mockCache) Get(key string) (*coordinator.HybridSearchResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	result, ok := m.store[key]
	return result, ok
}

func (m *mockCache) Set(key string, result *coordinator.HybridSearchResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sets++
	m.store[key] = result
}

func (m *mockCache) GetStats() (gets, sets int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gets, m.sets
}

// =============================================================================
// DegradationLevel Tests
// =============================================================================

func TestDegradationLevel_String(t *testing.T) {
	tests := []struct {
		level    DegradationLevel
		expected string
	}{
		{DegradationNone, "none"},
		{DegradationReduced, "reduced"},
		{DegradationMinimal, "minimal"},
		{DegradationPaused, "paused"},
		{DegradationLevel(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.level.String()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewPressureSearchController_Success(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()

	controller, err := NewPressureSearchController(config, searcher)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if controller == nil {
		t.Fatal("expected non-nil controller")
	}
	if controller.CurrentDegradation() != DegradationNone {
		t.Errorf("expected DegradationNone, got %v", controller.CurrentDegradation())
	}
}

func TestNewPressureSearchController_NilSearcher(t *testing.T) {
	config := DefaultPressureSearchConfig()

	controller, err := NewPressureSearchController(config, nil)

	if err != ErrNilSearcher {
		t.Errorf("expected ErrNilSearcher, got %v", err)
	}
	if controller != nil {
		t.Error("expected nil controller")
	}
}

func TestNewPressureSearchControllerWithCache_Success(t *testing.T) {
	searcher := newMockSearcher()
	cache := newMockCache()
	config := DefaultPressureSearchConfig()

	controller, err := NewPressureSearchControllerWithCache(config, searcher, cache)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if controller == nil {
		t.Fatal("expected non-nil controller")
	}
}

func TestNewPressureSearchControllerFull_Success(t *testing.T) {
	searcher := newMockSearcher()
	indexer := newMockIndexer()
	cache := newMockCache()
	config := DefaultPressureSearchConfig()

	controller, err := NewPressureSearchControllerFull(config, searcher, indexer, cache)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if controller == nil {
		t.Fatal("expected non-nil controller")
	}
}

// =============================================================================
// Config Defaults Tests
// =============================================================================

func TestDefaultPressureSearchConfig(t *testing.T) {
	config := DefaultPressureSearchConfig()

	if config.NormalConcurrency != DefaultNormalConcurrency {
		t.Errorf("expected NormalConcurrency %d, got %d",
			DefaultNormalConcurrency, config.NormalConcurrency)
	}
	if config.HighConcurrency != DefaultHighConcurrency {
		t.Errorf("expected HighConcurrency %d, got %d",
			DefaultHighConcurrency, config.HighConcurrency)
	}
	if config.CriticalConcurrency != DefaultCriticalConcurrency {
		t.Errorf("expected CriticalConcurrency %d, got %d",
			DefaultCriticalConcurrency, config.CriticalConcurrency)
	}
	if config.SearchTimeout != DefaultSearchTimeout {
		t.Errorf("expected SearchTimeout %v, got %v",
			DefaultSearchTimeout, config.SearchTimeout)
	}
	if !config.PreferCacheAtHigh {
		t.Error("expected PreferCacheAtHigh to be true")
	}
	if !config.PauseIndexingAtCritical {
		t.Error("expected PauseIndexingAtCritical to be true")
	}
}

func TestConfigDefaults_AppliedToZeroValues(t *testing.T) {
	searcher := newMockSearcher()
	config := PressureSearchConfig{} // All zero values

	controller, err := NewPressureSearchController(config, searcher)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Defaults should have been applied
	if controller.CurrentConcurrency() != DefaultNormalConcurrency {
		t.Errorf("expected default concurrency %d, got %d",
			DefaultNormalConcurrency, controller.CurrentConcurrency())
	}
}

// =============================================================================
// Pressure Response Tests
// =============================================================================

func TestOnPressureChange_Normal(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	controller.OnPressureChange(resources.PressureNormal)

	if controller.CurrentPressure() != resources.PressureNormal {
		t.Errorf("expected PressureNormal, got %v", controller.CurrentPressure())
	}
	if controller.CurrentDegradation() != DegradationNone {
		t.Errorf("expected DegradationNone, got %v", controller.CurrentDegradation())
	}
	if controller.CurrentConcurrency() != config.NormalConcurrency {
		t.Errorf("expected concurrency %d, got %d",
			config.NormalConcurrency, controller.CurrentConcurrency())
	}
}

func TestOnPressureChange_Elevated(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	controller.OnPressureChange(resources.PressureElevated)

	if controller.CurrentPressure() != resources.PressureElevated {
		t.Errorf("expected PressureElevated, got %v", controller.CurrentPressure())
	}
	// Elevated still uses normal degradation
	if controller.CurrentDegradation() != DegradationNone {
		t.Errorf("expected DegradationNone, got %v", controller.CurrentDegradation())
	}
}

func TestOnPressureChange_High(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	controller.OnPressureChange(resources.PressureHigh)

	if controller.CurrentPressure() != resources.PressureHigh {
		t.Errorf("expected PressureHigh, got %v", controller.CurrentPressure())
	}
	if controller.CurrentDegradation() != DegradationReduced {
		t.Errorf("expected DegradationReduced, got %v", controller.CurrentDegradation())
	}
	if controller.CurrentConcurrency() != config.HighConcurrency {
		t.Errorf("expected concurrency %d, got %d",
			config.HighConcurrency, controller.CurrentConcurrency())
	}
}

func TestOnPressureChange_Critical(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	controller.OnPressureChange(resources.PressureCritical)

	if controller.CurrentPressure() != resources.PressureCritical {
		t.Errorf("expected PressureCritical, got %v", controller.CurrentPressure())
	}
	if controller.CurrentDegradation() != DegradationPaused {
		t.Errorf("expected DegradationPaused, got %v", controller.CurrentDegradation())
	}
	if controller.CurrentConcurrency() != config.CriticalConcurrency {
		t.Errorf("expected concurrency %d, got %d",
			config.CriticalConcurrency, controller.CurrentConcurrency())
	}
}

func TestOnPressureChange_Critical_NoPauseIndexing(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	config.PauseIndexingAtCritical = false
	controller, _ := NewPressureSearchController(config, searcher)

	controller.OnPressureChange(resources.PressureCritical)

	if controller.CurrentDegradation() != DegradationMinimal {
		t.Errorf("expected DegradationMinimal, got %v", controller.CurrentDegradation())
	}
}

func TestIsIndexingPaused(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	// Initially not paused
	if controller.IsIndexingPaused() {
		t.Error("expected indexing not paused initially")
	}

	// Pause at critical
	controller.OnPressureChange(resources.PressureCritical)
	if !controller.IsIndexingPaused() {
		t.Error("expected indexing paused at critical")
	}

	// Resume at normal
	controller.OnPressureChange(resources.PressureNormal)
	if controller.IsIndexingPaused() {
		t.Error("expected indexing not paused at normal")
	}
}

// =============================================================================
// Search Tests
// =============================================================================

func TestSearch_Success(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	req := &coordinator.HybridSearchRequest{Query: "test query"}
	result, err := controller.Search(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if searcher.GetSearchCalls() != 1 {
		t.Errorf("expected 1 search call, got %d", searcher.GetSearchCalls())
	}
}

func TestSearch_ClosedController(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	controller.Close()

	req := &coordinator.HybridSearchRequest{Query: "test"}
	_, err := controller.Search(context.Background(), req)

	if err != ErrSearchPaused {
		t.Errorf("expected ErrSearchPaused, got %v", err)
	}
}

func TestSearch_ClosedSearcher(t *testing.T) {
	searcher := newMockSearcher()
	searcher.SetClosed(true)
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	req := &coordinator.HybridSearchRequest{Query: "test"}
	_, err := controller.Search(context.Background(), req)

	if err != ErrSearchPaused {
		t.Errorf("expected ErrSearchPaused, got %v", err)
	}
}

func TestSearch_ContextCancelled(t *testing.T) {
	searcher := newMockSearcher()
	searcher.searchDelay = 100 * time.Millisecond
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	req := &coordinator.HybridSearchRequest{Query: "test"}
	_, err := controller.Search(ctx, req)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context deadline exceeded, got %v", err)
	}
}

// =============================================================================
// SearchWithDegradation Tests
// =============================================================================

func TestSearchWithDegradation_Normal(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	req := &coordinator.HybridSearchRequest{Query: "test"}
	result, degradation, err := controller.SearchWithDegradation(
		context.Background(), req, resources.PressureNormal,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if degradation != DegradationNone {
		t.Errorf("expected DegradationNone, got %v", degradation)
	}
}

func TestSearchWithDegradation_High(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	req := &coordinator.HybridSearchRequest{Query: "test"}
	result, degradation, err := controller.SearchWithDegradation(
		context.Background(), req, resources.PressureHigh,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if degradation != DegradationReduced {
		t.Errorf("expected DegradationReduced, got %v", degradation)
	}
}

// =============================================================================
// Cache Tests
// =============================================================================

func TestSearch_CachePreferredAtHighPressure(t *testing.T) {
	searcher := newMockSearcher()
	cache := newMockCache()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchControllerWithCache(config, searcher, cache)

	// Pre-populate cache
	cachedResult := &coordinator.HybridSearchResult{
		FusedResults: []search.ScoredDocument{
			{Document: search.Document{ID: "cached"}},
		},
	}
	cache.Set("test query", cachedResult)

	// Set high pressure
	controller.OnPressureChange(resources.PressureHigh)

	req := &coordinator.HybridSearchRequest{Query: "test query"}
	result, err := controller.Search(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.FusedResults) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.FusedResults))
	}
	if result.FusedResults[0].ID != "cached" {
		t.Errorf("expected cached result, got %s", result.FusedResults[0].ID)
	}
	// Searcher should not have been called
	if searcher.GetSearchCalls() != 0 {
		t.Errorf("expected 0 search calls, got %d", searcher.GetSearchCalls())
	}
}

func TestSearch_CacheNotPreferredAtNormalPressure(t *testing.T) {
	searcher := newMockSearcher()
	cache := newMockCache()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchControllerWithCache(config, searcher, cache)

	// Pre-populate cache
	cache.Set("test query", &coordinator.HybridSearchResult{})

	// Keep normal pressure
	req := &coordinator.HybridSearchRequest{Query: "test query"}
	_, err := controller.Search(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Searcher should have been called
	if searcher.GetSearchCalls() != 1 {
		t.Errorf("expected 1 search call, got %d", searcher.GetSearchCalls())
	}
}

func TestSearch_CacheResultsStored(t *testing.T) {
	searcher := newMockSearcher()
	cache := newMockCache()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchControllerWithCache(config, searcher, cache)

	req := &coordinator.HybridSearchRequest{Query: "new query"}
	_, err := controller.Search(context.Background(), req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, sets := cache.GetStats()
	if sets != 1 {
		t.Errorf("expected 1 cache set, got %d", sets)
	}
}

// =============================================================================
// Indexing Tests
// =============================================================================

func TestIndex_Success(t *testing.T) {
	searcher := newMockSearcher()
	indexer := newMockIndexer()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchControllerFull(config, searcher, indexer, nil)

	doc := &search.Document{ID: "1", Path: "/test.go"}
	err := controller.Index(context.Background(), doc)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if indexer.GetIndexCalls() != 1 {
		t.Errorf("expected 1 index call, got %d", indexer.GetIndexCalls())
	}
}

func TestIndex_NoIndexer(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	doc := &search.Document{ID: "1", Path: "/test.go"}
	err := controller.Index(context.Background(), doc)

	if err != ErrNilIndexer {
		t.Errorf("expected ErrNilIndexer, got %v", err)
	}
}

func TestIndex_PausedAtCriticalPressure(t *testing.T) {
	searcher := newMockSearcher()
	indexer := newMockIndexer()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchControllerFull(config, searcher, indexer, nil)

	controller.OnPressureChange(resources.PressureCritical)

	doc := &search.Document{ID: "1", Path: "/test.go"}
	err := controller.Index(context.Background(), doc)

	if err != ErrIndexingPaused {
		t.Errorf("expected ErrIndexingPaused, got %v", err)
	}
	if indexer.GetIndexCalls() != 0 {
		t.Errorf("expected 0 index calls, got %d", indexer.GetIndexCalls())
	}
}

func TestIndexBatch_Success(t *testing.T) {
	searcher := newMockSearcher()
	indexer := newMockIndexer()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchControllerFull(config, searcher, indexer, nil)

	docs := []*search.Document{
		{ID: "1", Path: "/test1.go"},
		{ID: "2", Path: "/test2.go"},
	}
	err := controller.IndexBatch(context.Background(), docs)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if indexer.GetBatchCalls() != 1 {
		t.Errorf("expected 1 batch call, got %d", indexer.GetBatchCalls())
	}
}

func TestIndexBatch_PausedAtCriticalPressure(t *testing.T) {
	searcher := newMockSearcher()
	indexer := newMockIndexer()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchControllerFull(config, searcher, indexer, nil)

	controller.OnPressureChange(resources.PressureCritical)

	docs := []*search.Document{{ID: "1"}}
	err := controller.IndexBatch(context.Background(), docs)

	if err != ErrIndexingPaused {
		t.Errorf("expected ErrIndexingPaused, got %v", err)
	}
}

// =============================================================================
// Lifecycle Tests
// =============================================================================

func TestPressureController_Close_Idempotent(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	// First close
	err1 := controller.Close()
	if err1 != nil {
		t.Fatalf("unexpected error on first close: %v", err1)
	}

	// Second close should succeed
	err2 := controller.Close()
	if err2 != nil {
		t.Fatalf("unexpected error on second close: %v", err2)
	}

	if !controller.IsClosed() {
		t.Error("expected controller to be closed")
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestConcurrentSearches(t *testing.T) {
	searcher := newMockSearcher()
	searcher.searchDelay = 10 * time.Millisecond
	config := DefaultPressureSearchConfig()
	config.NormalConcurrency = 2
	controller, _ := NewPressureSearchController(config, searcher)

	var wg sync.WaitGroup
	var completed atomic.Int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &coordinator.HybridSearchRequest{Query: "test"}
			_, err := controller.Search(context.Background(), req)
			if err == nil {
				completed.Add(1)
			}
		}()
	}

	wg.Wait()

	if completed.Load() != 10 {
		t.Errorf("expected 10 completed searches, got %d", completed.Load())
	}
}

func TestConcurrentPressureChanges(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	var wg sync.WaitGroup
	levels := []resources.PressureLevel{
		resources.PressureNormal,
		resources.PressureElevated,
		resources.PressureHigh,
		resources.PressureCritical,
	}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			level := levels[idx%len(levels)]
			controller.OnPressureChange(level)
		}(i)
	}

	wg.Wait()

	// Should not panic and should have a valid state
	level := controller.CurrentPressure()
	if level < resources.PressureNormal || level > resources.PressureCritical {
		t.Errorf("unexpected pressure level: %v", level)
	}
}

func TestConcurrentSearchAndPressureChange(t *testing.T) {
	searcher := newMockSearcher()
	config := DefaultPressureSearchConfig()
	controller, _ := NewPressureSearchController(config, searcher)

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start searches - use the cancellable context for searches
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					req := &coordinator.HybridSearchRequest{Query: "test"}
					// Use ctx so search can be cancelled
					searchCtx, searchCancel := context.WithTimeout(ctx, 20*time.Millisecond)
					controller.Search(searchCtx, req)
					searchCancel()
				}
			}
		}()
	}

	// Start pressure changes
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			levels := []resources.PressureLevel{
				resources.PressureNormal,
				resources.PressureHigh,
				resources.PressureCritical,
			}
			for {
				select {
				case <-ctx.Done():
					return
				default:
					for _, level := range levels {
						select {
						case <-ctx.Done():
							return
						default:
							controller.OnPressureChange(level)
							time.Sleep(5 * time.Millisecond)
						}
					}
				}
			}
		}()
	}

	wg.Wait()
	// Test passes if no panics or deadlocks occur
}
