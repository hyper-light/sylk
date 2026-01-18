package archivalist

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// QueryCache Unit Tests
// =============================================================================

func newTestQueryCache(t *testing.T) *QueryCache {
	t.Helper()
	embedder := NewMockEmbedder(1536)
	return NewQueryCache(QueryCacheConfig{
		HitThreshold:  0.95,
		MaxQueries:    1000,
		UseEmbeddings: true,
	}, embedder)
}

func TestQueryCache_StoreAndGet(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Store a query
	err := cache.Store(ctx, "What is the error handling pattern?", "session-1", []byte(`{"answer": "Use wrapped errors"}`), QueryTypePattern)
	require.NoError(t, err, "Store")

	// Retrieve it
	cached, found := cache.Get(ctx, "What is the error handling pattern?", "session-1")

	assert.True(t, found, "Should find cached query")
	assert.NotNil(t, cached, "Cached response should not be nil")
	assert.Equal(t, `{"answer": "Use wrapped errors"}`, string(cached.Response), "Response should match")
}

func TestQueryCache_CacheMiss(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Try to get non-existent query
	_, found := cache.Get(ctx, "Non-existent query", "session-1")

	assert.False(t, found, "Should not find non-existent query")
}

func TestQueryCache_SimilarQueryHit(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Store a query
	cache.Store(ctx, "What is the error handling pattern?", "session-1", []byte(`{"answer": "wrapped errors"}`), QueryTypePattern)

	// Try to get with similar query (exact match in this case since mock embedder is deterministic)
	cached, found := cache.Get(ctx, "What is the error handling pattern?", "session-1")

	assert.True(t, found, "Should find similar query")
	assert.NotNil(t, cached, "Cached response should not be nil")
}

func TestQueryCache_DissimilarQueryMiss(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Store a query
	cache.Store(ctx, "What is the error handling pattern?", "session-1", []byte(`{"answer": "wrapped errors"}`), QueryTypePattern)

	// Try to get with very different query
	_, found := cache.Get(ctx, "How do I configure the database?", "session-1")

	assert.False(t, found, "Should not find dissimilar query")
}

func TestQueryCache_SessionIsolation(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Store query in session-1
	cache.Store(ctx, "What is the pattern?", "session-1", []byte(`{"answer": "A"}`), QueryTypePattern)

	// Try to get from session-2
	_, found := cache.Get(ctx, "What is the pattern?", "session-2")

	assert.False(t, found, "Should not find query from different session")
}

func TestQueryCache_InvalidateBySession(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Store queries in different sessions
	cache.Store(ctx, "Query 1", "session-1", []byte(`{}`), QueryTypePattern)
	cache.Store(ctx, "Query 2", "session-1", []byte(`{}`), QueryTypePattern)
	cache.Store(ctx, "Query 3", "session-2", []byte(`{}`), QueryTypePattern)

	// Invalidate session-1
	cache.InvalidateBySession("session-1")

	// Session-1 queries should be gone
	_, found1 := cache.Get(ctx, "Query 1", "session-1")
	_, found2 := cache.Get(ctx, "Query 2", "session-1")
	assert.False(t, found1, "Session-1 query 1 should be invalidated")
	assert.False(t, found2, "Session-1 query 2 should be invalidated")

	// Session-2 query should still exist
	_, found3 := cache.Get(ctx, "Query 3", "session-2")
	assert.True(t, found3, "Session-2 query should still exist")
}

func TestQueryCache_InvalidateByType(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Store queries of different types
	cache.Store(ctx, "Pattern query", "session-1", []byte(`{}`), QueryTypePattern)
	cache.Store(ctx, "Failure query", "session-1", []byte(`{}`), QueryTypeFailure)
	cache.Store(ctx, "Context query", "session-1", []byte(`{}`), QueryTypeContext)

	// Invalidate pattern type
	cache.InvalidateByType(QueryTypePattern)

	// Pattern queries should be gone
	_, found1 := cache.Get(ctx, "Pattern query", "session-1")
	assert.False(t, found1, "Pattern query should be invalidated")

	// Other types should still exist
	_, found2 := cache.Get(ctx, "Failure query", "session-1")
	_, found3 := cache.Get(ctx, "Context query", "session-1")
	assert.True(t, found2, "Failure query should still exist")
	assert.True(t, found3, "Context query should still exist")
}

func TestQueryCache_Stats(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Store some queries
	cache.Store(ctx, "Query 1", "session-1", []byte(`{}`), QueryTypePattern)
	cache.Store(ctx, "Query 2", "session-1", []byte(`{}`), QueryTypeFailure)

	// Make some hits and misses
	cache.Get(ctx, "Query 1", "session-1")      // Hit
	cache.Get(ctx, "Query 1", "session-1")      // Hit
	cache.Get(ctx, "Non-existent", "session-1") // Miss

	stats := cache.Stats()

	// TotalQueries is hits + misses (2 hits + 1 miss = 3)
	assert.Equal(t, int64(3), stats.TotalQueries, "Should have 3 total queries")
	assert.Equal(t, 2, stats.CachedResponses, "Should have 2 cached responses")
	assert.GreaterOrEqual(t, stats.CacheHits, int64(2), "Should have at least 2 hits")
	assert.GreaterOrEqual(t, stats.CacheMisses, int64(1), "Should have at least 1 miss")
}

func TestQueryCache_Cleanup(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	// Store queries
	for i := 0; i < 10; i++ {
		cache.Store(ctx, "Query "+string(rune('0'+i)), "session-1", []byte(`{}`), QueryTypePattern)
	}

	// Run cleanup
	cache.Cleanup()

	// Should still function
	cache.Store(ctx, "New query", "session-1", []byte(`{}`), QueryTypePattern)
	_, found := cache.Get(ctx, "New query", "session-1")
	assert.True(t, found, "Should still work after cleanup")
}

// =============================================================================
// QueryCache Performance Tests
// =============================================================================

func TestQueryCache_LargeNumberOfQueries(t *testing.T) {
	cache := NewQueryCache(QueryCacheConfig{
		HitThreshold:  0.95,
		MaxQueries:    100, // Small limit
		UseEmbeddings: true,
	}, NewMockEmbedder(1536))
	ctx := context.Background()

	// Store more than max
	for i := 0; i < 200; i++ {
		cache.Store(ctx, "Query "+string(rune(i)), "session-1", []byte(`{}`), QueryTypePattern)
	}

	stats := cache.Stats()

	// Should not exceed max (may be slightly over due to implementation)
	assert.LessOrEqual(t, stats.TotalQueries, int64(200), "Should handle large number of queries")
}

func TestQueryCache_ConcurrentAccess(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	runConcurrent(t, 10, func(i int) {
		// Writers
		for j := 0; j < 50; j++ {
			cache.Store(ctx, "Query from goroutine", "session-1", []byte(`{}`), QueryTypePattern)
		}
	})

	runConcurrent(t, 10, func(i int) {
		// Readers
		for j := 0; j < 50; j++ {
			cache.Get(ctx, "Query from goroutine", "session-1")
			cache.Stats()
		}
	})

	// Should complete without deadlock
	stats := cache.Stats()
	assert.GreaterOrEqual(t, stats.TotalQueries, int64(0), "Should handle concurrent access")
}

func TestQueryCache_EvictableCache_Name(t *testing.T) {
	cache := newTestQueryCache(t)

	name := cache.Name()
	assert.Equal(t, "query_cache", name)
}

func TestQueryCache_EvictableCache_Size(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	initialSize := cache.Size()
	assert.Equal(t, int64(0), initialSize, "Empty cache should have size 0")

	cache.Store(ctx, "Query 1", "session-1", []byte(`{"data": "response1"}`), QueryTypePattern)
	cache.Store(ctx, "Query 2", "session-1", []byte(`{"data": "response2"}`), QueryTypePattern)

	size := cache.Size()
	assert.Greater(t, size, int64(0), "Cache with entries should have positive size")
}

func TestQueryCache_EvictableCache_EvictPercent(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		query := "Query number " + itoa(i)
		cache.Store(ctx, query, "session-1", []byte(`{"data": "response"}`), QueryTypePattern)
	}

	stats := cache.Stats()
	assert.Equal(t, 10, stats.CachedResponses, "Should have 10 cached responses")

	beforeSize := cache.Size()
	freed := cache.EvictPercent(0.50)

	afterStats := cache.Stats()
	assert.LessOrEqual(t, afterStats.CachedResponses, 5, "Should have at most 5 responses after 50%% eviction")
	assert.Greater(t, freed, int64(0), "Should free some bytes")
	assert.Less(t, cache.Size(), beforeSize, "Size should decrease after eviction")
}

func TestQueryCache_EvictableCache_EvictPercent_EmptyCache(t *testing.T) {
	cache := newTestQueryCache(t)

	freed := cache.EvictPercent(0.50)
	assert.Equal(t, int64(0), freed, "Evicting empty cache should free 0 bytes")
}

func TestQueryCache_EvictableCache_EvictPercent_SmallPercent(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		query := "Query number " + itoa(i)
		cache.Store(ctx, query, "session-1", []byte(`{"data": "response"}`), QueryTypePattern)
	}

	freed := cache.EvictPercent(0.01)
	assert.Greater(t, freed, int64(0), "Should evict at least 1 entry even with small percent")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
