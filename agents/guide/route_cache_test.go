package guide_test

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRouteCache_NewRouteCache tests creating route cache
func TestRouteCache_NewRouteCache(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())
	require.NotNil(t, cache)

	stats := cache.Stats()
	assert.Equal(t, 0, stats.Size)
	assert.Equal(t, 10000, stats.MaxSize)
	assert.Equal(t, int64(0), stats.Hits)
	assert.Equal(t, int64(0), stats.Misses)
}

// TestRouteCache_Set tests setting cache entries
func TestRouteCache_Set(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	result := &guide.RouteResult{
		TargetAgent: "archivalist",
		Intent:      guide.IntentRecall,
		Domain:      guide.DomainPatterns,
		Confidence:  0.95,
	}

	cache.Set("what patterns do we use?", result)

	stats := cache.Stats()
	assert.Equal(t, 1, stats.Size)
}

// TestRouteCache_Get tests getting cached entries
func TestRouteCache_Get(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	result := &guide.RouteResult{
		TargetAgent: "archivalist",
		Intent:      guide.IntentRecall,
		Domain:      guide.DomainPatterns,
		Confidence:  0.95,
	}

	cache.Set("what patterns do we use?", result)

	// Get existing entry
	cached := cache.Get("what patterns do we use?")
	require.NotNil(t, cached)
	assert.Equal(t, "archivalist", cached.TargetAgentID)
	assert.Equal(t, guide.IntentRecall, cached.Intent)
	assert.Equal(t, guide.DomainPatterns, cached.Domain)
	assert.Equal(t, 0.95, cached.Confidence)

	// Get non-existent entry
	cached = cache.Get("unknown query")
	assert.Nil(t, cached)
}

// TestRouteCache_CaseInsensitive tests case-insensitive matching
func TestRouteCache_CaseInsensitive(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.Set("What PATTERNS do we use?", &guide.RouteResult{
		TargetAgent: "archivalist",
	})

	// Should match regardless of case
	cached := cache.Get("what patterns do we use?")
	require.NotNil(t, cached)

	cached = cache.Get("WHAT PATTERNS DO WE USE?")
	require.NotNil(t, cached)
}

// TestRouteCache_WhitespaceNormalization tests whitespace handling
func TestRouteCache_WhitespaceNormalization(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.Set("  test query  ", &guide.RouteResult{
		TargetAgent: "archivalist",
	})

	// Should match with different whitespace
	cached := cache.Get("test query")
	require.NotNil(t, cached)

	cached = cache.Get("  test query")
	require.NotNil(t, cached)

	cached = cache.Get("test query  ")
	require.NotNil(t, cached)
}

// TestRouteCache_Expiration tests entry expiration
func TestRouteCache_Expiration(t *testing.T) {
	cache := guide.NewRouteCache(guide.RouteCacheConfig{
		MaxSize: 10000,
		TTL:     50 * time.Millisecond,
	})

	cache.Set("test query", &guide.RouteResult{
		TargetAgent: "archivalist",
	})

	// Should exist immediately
	cached := cache.Get("test query")
	require.NotNil(t, cached)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired now
	cached = cache.Get("test query")
	assert.Nil(t, cached)
}

// TestRouteCache_Invalidate tests invalidating entries
func TestRouteCache_Invalidate(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.Set("test query", &guide.RouteResult{TargetAgent: "archivalist"})
	assert.NotNil(t, cache.Get("test query"))

	cache.Invalidate("test query")
	assert.Nil(t, cache.Get("test query"))
}

// TestRouteCache_InvalidateForAgent tests invalidating all entries for an agent
func TestRouteCache_InvalidateForAgent(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.Set("query1", &guide.RouteResult{TargetAgent: "archivalist"})
	cache.Set("query2", &guide.RouteResult{TargetAgent: "archivalist"})
	cache.Set("query3", &guide.RouteResult{TargetAgent: "guide"})

	assert.Equal(t, 3, cache.Stats().Size)

	cache.InvalidateForAgent("archivalist")

	assert.Equal(t, 1, cache.Stats().Size)
	assert.Nil(t, cache.Get("query1"))
	assert.Nil(t, cache.Get("query2"))
	assert.NotNil(t, cache.Get("query3"))
}

// TestRouteCache_Clear tests clearing all entries
func TestRouteCache_Clear(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.Set("query1", &guide.RouteResult{TargetAgent: "archivalist"})
	cache.Set("query2", &guide.RouteResult{TargetAgent: "guide"})
	assert.Equal(t, 2, cache.Stats().Size)

	cache.Clear()
	assert.Equal(t, 0, cache.Stats().Size)
}

// TestRouteCache_SetFromRoute tests setting from explicit route
func TestRouteCache_SetFromRoute(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.SetFromRoute("test query", "archivalist", guide.IntentRecall, guide.DomainPatterns)

	cached := cache.Get("test query")
	require.NotNil(t, cached)
	assert.Equal(t, "archivalist", cached.TargetAgentID)
	assert.Equal(t, 1.0, cached.Confidence) // Learned routes have max confidence
}

// TestRouteCache_HitMissStats tests hit/miss statistics
func TestRouteCache_HitMissStats(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.Set("test query", &guide.RouteResult{TargetAgent: "archivalist"})

	// Cache miss
	cache.Get("unknown")
	stats := cache.Stats()
	assert.Equal(t, int64(0), stats.Hits)
	assert.Equal(t, int64(1), stats.Misses)

	// Cache hit
	cache.Get("test query")
	stats = cache.Stats()
	assert.Equal(t, int64(1), stats.Hits)
	assert.Equal(t, int64(1), stats.Misses)
	assert.Equal(t, 0.5, stats.HitRate)
}

// TestRouteCache_HitCountTracking tests hit count per entry
func TestRouteCache_HitCountTracking(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.Set("test query", &guide.RouteResult{TargetAgent: "archivalist"})

	// Get multiple times
	for i := 0; i < 5; i++ {
		cached := cache.Get("test query")
		require.NotNil(t, cached)
	}

	cached := cache.Get("test query")
	require.NotNil(t, cached)
	assert.Equal(t, int64(6), cached.HitCount)
}

// TestRouteCache_ClassificationCountTracking tests classification count
func TestRouteCache_ClassificationCountTracking(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	// Set multiple times for same query
	for i := 0; i < 3; i++ {
		cache.Set("test query", &guide.RouteResult{TargetAgent: "archivalist"})
	}

	cached := cache.Get("test query")
	require.NotNil(t, cached)
	assert.Equal(t, int64(3), cached.ClassificationCount)
}

// TestRouteCache_Eviction tests eviction when max size reached
func TestRouteCache_Eviction(t *testing.T) {
	cache := guide.NewRouteCache(guide.RouteCacheConfig{
		MaxSize: 3,
		TTL:     time.Hour,
	})

	// Fill cache
	cache.Set("query1", &guide.RouteResult{TargetAgent: "a"})
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	cache.Set("query2", &guide.RouteResult{TargetAgent: "b"})
	time.Sleep(10 * time.Millisecond)
	cache.Set("query3", &guide.RouteResult{TargetAgent: "c"})

	assert.Equal(t, 3, cache.Stats().Size)

	// Add one more - should evict oldest
	cache.Set("query4", &guide.RouteResult{TargetAgent: "d"})

	assert.Equal(t, 3, cache.Stats().Size)
	assert.Nil(t, cache.Get("query1")) // Oldest should be evicted
}

// TestRouteCache_Cleanup tests cleanup of expired entries
func TestRouteCache_Cleanup(t *testing.T) {
	cache := guide.NewRouteCache(guide.RouteCacheConfig{
		MaxSize: 10000,
		TTL:     50 * time.Millisecond,
	})

	cache.Set("query1", &guide.RouteResult{TargetAgent: "a"})
	cache.Set("query2", &guide.RouteResult{TargetAgent: "b"})
	assert.Equal(t, 2, cache.Stats().Size)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Run cleanup
	removed := cache.Cleanup()
	assert.Equal(t, 2, removed)
	assert.Equal(t, 0, cache.Stats().Size)
}

// TestRouteCache_GetAll tests getting all cached routes
func TestRouteCache_GetAll(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	cache.Set("query1", &guide.RouteResult{TargetAgent: "a"})
	cache.Set("query2", &guide.RouteResult{TargetAgent: "b"})
	cache.Set("query3", &guide.RouteResult{TargetAgent: "c"})

	all := cache.GetAll()
	assert.Len(t, all, 3)
}

// TestRouteCache_SetBulk tests setting multiple routes at once
func TestRouteCache_SetBulk(t *testing.T) {
	cache := guide.NewRouteCache(guide.DefaultRouteCacheConfig())

	routes := []*guide.CachedRoute{
		{
			Input:         "query1",
			TargetAgentID: "archivalist",
			Intent:        guide.IntentRecall,
			Domain:        guide.DomainPatterns,
		},
		{
			Input:         "query2",
			TargetAgentID: "guide",
			Intent:        guide.IntentHelp,
			Domain:        guide.DomainSystem,
		},
	}

	cache.SetBulk(routes)
	assert.Equal(t, 2, cache.Stats().Size)
}

// TestRouteCache_DefaultConfig tests default config values
func TestRouteCache_DefaultConfig(t *testing.T) {
	cfg := guide.DefaultRouteCacheConfig()
	assert.Equal(t, 10000, cfg.MaxSize)
	assert.Equal(t, time.Hour, cfg.TTL)
}

// TestRouteCache_ZeroConfig tests handling of zero config values
func TestRouteCache_ZeroConfig(t *testing.T) {
	// Should use defaults for zero values
	cache := guide.NewRouteCache(guide.RouteCacheConfig{})
	require.NotNil(t, cache)

	stats := cache.Stats()
	assert.Equal(t, 10000, stats.MaxSize)
	assert.Equal(t, "1h0m0s", stats.TTL)
}
