package context

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	cerrors "github.com/adalundhe/sylk/core/errors"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/coordinator"
)

// =============================================================================
// Test Helpers
// =============================================================================

func createTestTieredSearcher(t *testing.T) (*TieredSearcher, *MockBleveSearcher, *MockVectorSearcher, *MockEmbedder) {
	t.Helper()

	hotCache := NewDefaultHotCache()
	bleve := NewMockBleveSearcher()
	vector := NewMockVectorSearcher()
	embedder := NewMockEmbedder()

	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
		Vector:   vector,
		Embedder: embedder,
	})

	return ts, bleve, vector, embedder
}

func addTestEntryToCache(t *testing.T, cache *HotCache, id string, keywords []string) {
	t.Helper()
	cache.Add(id, &ContentEntry{
		ID:       id,
		Content:  "test content for " + id,
		Keywords: keywords,
	})
}

// =============================================================================
// SearchTier Tests
// =============================================================================

func TestSearchTier_String(t *testing.T) {
	tests := []struct {
		tier     SearchTier
		expected string
	}{
		{TierHotCache, "hot_cache"},
		{TierWarmIndex, "warm_index"},
		{TierFullSearch, "full_search"},
		{SearchTier(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.tier.String()
		if got != tt.expected {
			t.Errorf("SearchTier(%d).String() = %q, want %q", tt.tier, got, tt.expected)
		}
	}
}

func TestTierBudget(t *testing.T) {
	tests := []struct {
		tier     SearchTier
		expected time.Duration
	}{
		{TierHotCache, TierHotBudget},
		{TierWarmIndex, TierWarmBudget},
		{TierFullSearch, TierFullBudget},
		{SearchTier(99), TierFullBudget},
	}

	for _, tt := range tests {
		got := TierBudget(tt.tier)
		if got != tt.expected {
			t.Errorf("TierBudget(%d) = %v, want %v", tt.tier, got, tt.expected)
		}
	}
}

// =============================================================================
// TieredSearchResult Tests
// =============================================================================

func TestTieredSearchResult_HasResults(t *testing.T) {
	result := &TieredSearchResult{Results: nil}
	if result.HasResults() {
		t.Error("HasResults() should return false for nil results")
	}

	result.Results = []*ContentEntry{}
	if result.HasResults() {
		t.Error("HasResults() should return false for empty results")
	}

	result.Results = []*ContentEntry{{ID: "test"}}
	if !result.HasResults() {
		t.Error("HasResults() should return true when results exist")
	}
}

// =============================================================================
// TieredSearcher Construction Tests
// =============================================================================

func TestNewTieredSearcher_DefaultConfig(t *testing.T) {
	ts := NewTieredSearcher(TieredSearcherConfig{})

	if ts.MaxTier() != TierFullSearch {
		t.Errorf("Default MaxTier = %v, want %v", ts.MaxTier(), TierFullSearch)
	}

	if ts.defaultLimit != DefaultWarmLimit {
		t.Errorf("defaultLimit = %d, want %d", ts.defaultLimit, DefaultWarmLimit)
	}
}

func TestNewTieredSearcher_CustomConfig(t *testing.T) {
	config := TieredSearcherConfig{
		HotCache:     NewDefaultHotCache(),
		DefaultLimit: 50,
		CircuitBreakerConfig: cerrors.CircuitBreakerConfig{
			ConsecutiveFailures: 5,
		},
	}

	ts := NewTieredSearcher(config)

	if ts.defaultLimit != 50 {
		t.Errorf("defaultLimit = %d, want 50", ts.defaultLimit)
	}
}

// =============================================================================
// Tier 0 (Hot Cache) Search Tests
// =============================================================================

func TestSearchWithBudget_TierHot_DirectIDMatch(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	// Add entry to hot cache
	addTestEntryToCache(t, ts.hotCache, "test-id-123", []string{"golang", "testing"})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test-id-123", TierHotBudget)

	if !result.HasResults() {
		t.Fatal("Expected results from hot cache direct ID match")
	}

	if result.HotHits != 1 {
		t.Errorf("HotHits = %d, want 1", result.HotHits)
	}

	if result.Tier != TierHotCache {
		t.Errorf("Tier = %v, want %v", result.Tier, TierHotCache)
	}
}

func TestSearchWithBudget_TierHot_KeywordMatch(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	// Add entries with keywords
	addTestEntryToCache(t, ts.hotCache, "entry-1", []string{"golang", "testing"})
	addTestEntryToCache(t, ts.hotCache, "entry-2", []string{"python", "testing"})
	addTestEntryToCache(t, ts.hotCache, "entry-3", []string{"rust", "systems"})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "testing", TierHotBudget)

	if result.HotHits != 2 {
		t.Errorf("HotHits = %d, want 2 (entries with 'testing' keyword)", result.HotHits)
	}
}

func TestSearchWithBudget_TierHot_NoMatch(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	addTestEntryToCache(t, ts.hotCache, "entry-1", []string{"golang"})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "nonexistent", TierHotBudget)

	if result.HotHits != 0 {
		t.Errorf("HotHits = %d, want 0", result.HotHits)
	}
}

func TestSearchWithBudget_TierHot_CircuitBreakerOpen(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	addTestEntryToCache(t, ts.hotCache, "test-id", []string{"keyword"})

	// Trip the circuit breaker
	for i := 0; i < 10; i++ {
		ts.cbHot.RecordResult(false)
	}

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "keyword", TierHotBudget)

	// Should skip hot cache due to open circuit
	if result.HotHits != 0 {
		t.Errorf("HotHits = %d, want 0 (circuit breaker open)", result.HotHits)
	}
}

// =============================================================================
// Tier 1 (Warm/Bleve) Search Tests
// =============================================================================

func TestSearchWithBudget_TierWarm_Success(t *testing.T) {
	ts, bleve, _, _ := createTestTieredSearcher(t)

	bleve.SetResults(&search.SearchResult{
		Documents: []search.ScoredDocument{
			{Document: search.Document{ID: "doc-1", Content: "content 1"}, Score: 0.9},
			{Document: search.Document{ID: "doc-2", Content: "content 2"}, Score: 0.8},
		},
		TotalHits: 2,
	})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test query", TierWarmBudget)

	if result.WarmHits != 2 {
		t.Errorf("WarmHits = %d, want 2", result.WarmHits)
	}

	if result.Tier != TierWarmIndex {
		t.Errorf("Tier = %v, want %v", result.Tier, TierWarmIndex)
	}

	if bleve.CallCount() != 1 {
		t.Errorf("Bleve call count = %d, want 1", bleve.CallCount())
	}
}

func TestSearchWithBudget_TierWarm_BleveError(t *testing.T) {
	ts, bleve, _, _ := createTestTieredSearcher(t)

	bleve.SetError(errors.New("bleve search failed"))

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test query", TierWarmBudget)

	if result.WarmHits != 0 {
		t.Errorf("WarmHits = %d, want 0", result.WarmHits)
	}

	if result.Errors[TierWarmIndex] == nil {
		t.Error("Expected error in Errors[TierWarmIndex]")
	}
}

func TestSearchWithBudget_TierWarm_BleveNotOpen(t *testing.T) {
	ts, bleve, _, _ := createTestTieredSearcher(t)

	bleve.SetOpen(false)

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test query", TierWarmBudget)

	if bleve.CallCount() != 0 {
		t.Errorf("Bleve should not be called when not open, call count = %d", bleve.CallCount())
	}

	if result.WarmHits != 0 {
		t.Errorf("WarmHits = %d, want 0", result.WarmHits)
	}
}

func TestSearchWithBudget_TierWarm_SkippedWithSmallBudget(t *testing.T) {
	ts, bleve, _, _ := createTestTieredSearcher(t)

	bleve.SetResults(&search.SearchResult{
		Documents: []search.ScoredDocument{{Document: search.Document{ID: "doc-1"}}},
	})

	ctx := context.Background()
	// Use budget smaller than TierWarmBudget
	result := ts.SearchWithBudget(ctx, "test query", 1*time.Millisecond)

	if bleve.CallCount() != 0 {
		t.Errorf("Bleve should not be called with small budget, call count = %d", bleve.CallCount())
	}

	if result.WarmHits != 0 {
		t.Errorf("WarmHits = %d, want 0", result.WarmHits)
	}
}

// =============================================================================
// Tier 2 (Full/Vector) Search Tests
// =============================================================================

func TestSearchWithBudget_TierFull_Success(t *testing.T) {
	ts, _, vector, embedder := createTestTieredSearcher(t)

	vector.SetResults([]coordinator.ScoredVectorResult{
		{ID: "vec-1", Content: "vector content 1", Score: 0.95},
		{ID: "vec-2", Content: "vector content 2", Score: 0.85},
	})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test query", TierFullBudget)

	if result.FullHits != 2 {
		t.Errorf("FullHits = %d, want 2", result.FullHits)
	}

	if result.Tier != TierFullSearch {
		t.Errorf("Tier = %v, want %v", result.Tier, TierFullSearch)
	}

	if embedder.CallCount() != 1 {
		t.Errorf("Embedder call count = %d, want 1", embedder.CallCount())
	}

	if vector.CallCount() != 1 {
		t.Errorf("Vector call count = %d, want 1", vector.CallCount())
	}
}

func TestSearchWithBudget_TierFull_EmbeddingError(t *testing.T) {
	ts, _, vector, embedder := createTestTieredSearcher(t)

	embedder.SetError(errors.New("embedding generation failed"))

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test query", TierFullBudget)

	if result.FullHits != 0 {
		t.Errorf("FullHits = %d, want 0", result.FullHits)
	}

	if result.Errors[TierFullSearch] == nil {
		t.Error("Expected error in Errors[TierFullSearch]")
	}

	if vector.CallCount() != 0 {
		t.Errorf("Vector should not be called on embedding error, call count = %d", vector.CallCount())
	}
}

func TestSearchWithBudget_TierFull_VectorError(t *testing.T) {
	ts, _, vector, _ := createTestTieredSearcher(t)

	vector.SetError(errors.New("vector search failed"))

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test query", TierFullBudget)

	if result.FullHits != 0 {
		t.Errorf("FullHits = %d, want 0", result.FullHits)
	}

	if result.Errors[TierFullSearch] == nil {
		t.Error("Expected error in Errors[TierFullSearch]")
	}
}

func TestSearchWithBudget_TierFull_SkippedWithSmallBudget(t *testing.T) {
	ts, _, vector, _ := createTestTieredSearcher(t)

	vector.SetResults([]coordinator.ScoredVectorResult{{ID: "vec-1"}})

	ctx := context.Background()
	// Use budget smaller than TierFullBudget
	result := ts.SearchWithBudget(ctx, "test query", TierWarmBudget)

	if vector.CallCount() != 0 {
		t.Errorf("Vector should not be called with warm budget, call count = %d", vector.CallCount())
	}

	if result.FullHits != 0 {
		t.Errorf("FullHits = %d, want 0", result.FullHits)
	}
}

func TestSearchWithBudget_TierFull_SkippedWithEnoughResults(t *testing.T) {
	ts, bleve, vector, _ := createTestTieredSearcher(t)
	ts.defaultLimit = 2

	// Bleve returns enough results
	docs := make([]search.ScoredDocument, 3)
	for i := range docs {
		docs[i] = search.ScoredDocument{Document: search.Document{ID: "doc"}}
	}
	bleve.SetResults(&search.SearchResult{Documents: docs})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test query", TierFullBudget)

	if vector.CallCount() != 0 {
		t.Errorf("Vector should not be called when enough results, call count = %d", vector.CallCount())
	}

	if result.FullHits != 0 {
		t.Errorf("FullHits = %d, want 0", result.FullHits)
	}
}

// =============================================================================
// SetMaxTier Tests
// =============================================================================

func TestSetMaxTier_LimitsSearch(t *testing.T) {
	ts, bleve, vector, _ := createTestTieredSearcher(t)

	bleve.SetResults(&search.SearchResult{
		Documents: []search.ScoredDocument{{Document: search.Document{ID: "doc-1"}}},
	})
	vector.SetResults([]coordinator.ScoredVectorResult{{ID: "vec-1"}})

	// Limit to warm tier
	ts.SetMaxTier(TierWarmIndex)

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test query", TierFullBudget)

	if vector.CallCount() != 0 {
		t.Errorf("Vector should not be called when maxTier is Warm, call count = %d", vector.CallCount())
	}

	if result.FullHits != 0 {
		t.Errorf("FullHits = %d, want 0", result.FullHits)
	}
}

func TestSetMaxTier_HotOnly(t *testing.T) {
	ts, bleve, vector, _ := createTestTieredSearcher(t)

	addTestEntryToCache(t, ts.hotCache, "test-id", []string{"query"})

	bleve.SetResults(&search.SearchResult{
		Documents: []search.ScoredDocument{{Document: search.Document{ID: "doc-1"}}},
	})

	// Limit to hot tier only
	ts.SetMaxTier(TierHotCache)

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "query", TierFullBudget)

	if bleve.CallCount() != 0 {
		t.Errorf("Bleve should not be called when maxTier is Hot, call count = %d", bleve.CallCount())
	}

	if vector.CallCount() != 0 {
		t.Errorf("Vector should not be called when maxTier is Hot, call count = %d", vector.CallCount())
	}

	if result.HotHits != 1 {
		t.Errorf("HotHits = %d, want 1", result.HotHits)
	}
}

func TestResetMaxTier(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	ts.SetMaxTier(TierHotCache)
	if ts.MaxTier() != TierHotCache {
		t.Errorf("MaxTier = %v, want %v", ts.MaxTier(), TierHotCache)
	}

	ts.ResetMaxTier()
	if ts.MaxTier() != TierFullSearch {
		t.Errorf("MaxTier after reset = %v, want %v", ts.MaxTier(), TierFullSearch)
	}
}

// =============================================================================
// Circuit Breaker Tests
// =============================================================================

func TestCircuitBreaker_GetByTier(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	if ts.CircuitBreaker(TierHotCache) == nil {
		t.Error("CircuitBreaker(TierHotCache) should not be nil")
	}

	if ts.CircuitBreaker(TierWarmIndex) == nil {
		t.Error("CircuitBreaker(TierWarmIndex) should not be nil")
	}

	if ts.CircuitBreaker(TierFullSearch) == nil {
		t.Error("CircuitBreaker(TierFullSearch) should not be nil")
	}

	if ts.CircuitBreaker(SearchTier(99)) != nil {
		t.Error("CircuitBreaker(unknown tier) should be nil")
	}
}

func TestResetCircuitBreakers(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	// Trip all circuit breakers
	for i := 0; i < 10; i++ {
		ts.cbHot.RecordResult(false)
		ts.cbWarm.RecordResult(false)
		ts.cbFull.RecordResult(false)
	}

	// Verify they're open
	if ts.cbHot.State() == cerrors.CircuitClosed {
		t.Error("Hot circuit breaker should be open")
	}

	ts.ResetCircuitBreakers()

	// Verify they're reset
	if ts.cbHot.State() != cerrors.CircuitClosed {
		t.Errorf("Hot circuit breaker state = %v, want Closed", ts.cbHot.State())
	}
	if ts.cbWarm.State() != cerrors.CircuitClosed {
		t.Errorf("Warm circuit breaker state = %v, want Closed", ts.cbWarm.State())
	}
	if ts.cbFull.State() != cerrors.CircuitClosed {
		t.Errorf("Full circuit breaker state = %v, want Closed", ts.cbFull.State())
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestStats_RecordsTierHits(t *testing.T) {
	ts, bleve, vector, _ := createTestTieredSearcher(t)

	addTestEntryToCache(t, ts.hotCache, "hot-entry", []string{"keyword"})
	bleve.SetResults(&search.SearchResult{
		Documents: []search.ScoredDocument{{Document: search.Document{ID: "doc"}}},
	})
	vector.SetResults([]coordinator.ScoredVectorResult{{ID: "vec"}})

	ctx := context.Background()

	// Hot search
	ts.SearchWithBudget(ctx, "keyword", TierHotBudget)

	// Warm search
	ts.SearchWithBudget(ctx, "other", TierWarmBudget)

	// Full search
	ts.SearchWithBudget(ctx, "another", TierFullBudget)

	stats := ts.Stats()

	if stats.SearchCount != 3 {
		t.Errorf("SearchCount = %d, want 3", stats.SearchCount)
	}

	if stats.TierHits[TierHotCache] != 1 {
		t.Errorf("TierHits[Hot] = %d, want 1", stats.TierHits[TierHotCache])
	}
}

func TestStats_ResetStats(t *testing.T) {
	ts, bleve, _, _ := createTestTieredSearcher(t)

	bleve.SetResults(&search.SearchResult{
		Documents: []search.ScoredDocument{{Document: search.Document{ID: "doc"}}},
	})

	ctx := context.Background()
	ts.SearchWithBudget(ctx, "test", TierWarmBudget)

	stats := ts.Stats()
	if stats.SearchCount != 1 {
		t.Errorf("SearchCount = %d, want 1", stats.SearchCount)
	}

	ts.ResetStats()

	stats = ts.Stats()
	if stats.SearchCount != 0 {
		t.Errorf("SearchCount after reset = %d, want 0", stats.SearchCount)
	}
}

func TestStats_CBStates(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	stats := ts.Stats()

	if stats.CBStates[TierHotCache] != cerrors.CircuitClosed {
		t.Errorf("CBStates[Hot] = %v, want Closed", stats.CBStates[TierHotCache])
	}
	if stats.CBStates[TierWarmIndex] != cerrors.CircuitClosed {
		t.Errorf("CBStates[Warm] = %v, want Closed", stats.CBStates[TierWarmIndex])
	}
	if stats.CBStates[TierFullSearch] != cerrors.CircuitClosed {
		t.Errorf("CBStates[Full] = %v, want Closed", stats.CBStates[TierFullSearch])
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestSearchWithBudget_ConcurrentAccess(t *testing.T) {
	ts, bleve, _, _ := createTestTieredSearcher(t)

	bleve.SetResults(&search.SearchResult{
		Documents: []search.ScoredDocument{{Document: search.Document{ID: "doc"}}},
	})

	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	var successCount atomic.Int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for j := 0; j < iterations; j++ {
				result := ts.SearchWithBudget(ctx, "test", TierWarmBudget)
				if result != nil {
					successCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	expected := int64(goroutines * iterations)
	if successCount.Load() != expected {
		t.Errorf("Success count = %d, want %d", successCount.Load(), expected)
	}

	stats := ts.Stats()
	if stats.SearchCount != expected {
		t.Errorf("SearchCount = %d, want %d", stats.SearchCount, expected)
	}
}

func TestSetMaxTier_ConcurrentAccess(t *testing.T) {
	ts, _, _, _ := createTestTieredSearcher(t)

	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				tier := SearchTier(j % 3)
				ts.SetMaxTier(tier)
				_ = ts.MaxTier()
			}
		}(i)
	}

	wg.Wait()
	// No race conditions should occur
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestSearchWithBudget_ContextCancellation(t *testing.T) {
	ts, bleve, _, _ := createTestTieredSearcher(t)

	bleve.SetDelay(100 * time.Millisecond)
	bleve.SetResults(&search.SearchResult{
		Documents: []search.ScoredDocument{{Document: search.Document{ID: "doc"}}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := ts.SearchWithBudget(ctx, "test", TierWarmBudget)

	// Should complete without results due to cancellation
	if result.WarmHits != 0 {
		t.Logf("WarmHits = %d (may vary based on timing)", result.WarmHits)
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestSearchWithBudget_NilHotCache(t *testing.T) {
	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: nil,
		Bleve:    NewMockBleveSearcher(),
	})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test", TierHotBudget)

	// Should not panic, just skip hot tier
	if result.HotHits != 0 {
		t.Errorf("HotHits = %d, want 0 (no hot cache)", result.HotHits)
	}
}

func TestSearchWithBudget_NilBleve(t *testing.T) {
	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: NewDefaultHotCache(),
		Bleve:    nil,
	})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test", TierWarmBudget)

	// Should not panic, just skip warm tier
	if result.WarmHits != 0 {
		t.Errorf("WarmHits = %d, want 0 (no bleve)", result.WarmHits)
	}
}

func TestSearchWithBudget_NilVector(t *testing.T) {
	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: NewDefaultHotCache(),
		Bleve:    NewMockBleveSearcher(),
		Vector:   nil,
		Embedder: NewMockEmbedder(),
	})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test", TierFullBudget)

	// Should not panic, just skip full tier
	if result.FullHits != 0 {
		t.Errorf("FullHits = %d, want 0 (no vector)", result.FullHits)
	}
}

func TestSearchWithBudget_NilEmbedder(t *testing.T) {
	ts := NewTieredSearcher(TieredSearcherConfig{
		HotCache: NewDefaultHotCache(),
		Bleve:    NewMockBleveSearcher(),
		Vector:   NewMockVectorSearcher(),
		Embedder: nil,
	})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "test", TierFullBudget)

	// Should not panic, just skip full tier
	if result.FullHits != 0 {
		t.Errorf("FullHits = %d, want 0 (no embedder)", result.FullHits)
	}
}

func TestSearchWithBudget_EmptyQuery(t *testing.T) {
	ts, bleve, _, _ := createTestTieredSearcher(t)

	bleve.SetResults(&search.SearchResult{})

	ctx := context.Background()
	result := ts.SearchWithBudget(ctx, "", TierWarmBudget)

	// Should handle empty query gracefully
	if result == nil {
		t.Error("Result should not be nil for empty query")
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestContainsIgnoreCase(t *testing.T) {
	tests := []struct {
		s        string
		substr   string
		expected bool
	}{
		{"Hello World", "world", true},
		{"Hello World", "WORLD", true},
		{"Hello World", "hello", true},
		{"Hello World", "xyz", false},
		{"", "", true},
		{"abc", "", true},
		{"", "abc", false},
		{"Go", "golang", false},
		{"GOLANG", "go", true},
	}

	for _, tt := range tests {
		got := containsIgnoreCase(tt.s, tt.substr)
		if got != tt.expected {
			t.Errorf("containsIgnoreCase(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.expected)
		}
	}
}

func TestCharsEqualIgnoreCase(t *testing.T) {
	tests := []struct {
		a, b     byte
		expected bool
	}{
		{'a', 'a', true},
		{'a', 'A', true},
		{'A', 'a', true},
		{'A', 'A', true},
		{'a', 'b', false},
		{'1', '1', true},
		{'1', '2', false},
	}

	for _, tt := range tests {
		got := charsEqualIgnoreCase(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("charsEqualIgnoreCase(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.expected)
		}
	}
}
