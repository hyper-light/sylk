package coordinator

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSearchCache_DefaultConfig(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	assert.NotNil(t, cache)
	assert.Equal(t, 0, cache.Size())
	assert.True(t, cache.IsEnabled())
}

func TestNewSearchCache_CustomConfig(t *testing.T) {
	cfg := CacheConfig{
		TTL:     10 * time.Minute,
		MaxSize: 500,
	}

	cache := NewSearchCache(cfg)

	assert.NotNil(t, cache)
	stats := cache.Stats()
	assert.Equal(t, 500, stats.MaxSize)
}

func TestNewSearchCache_InvalidConfig(t *testing.T) {
	// Zero/negative values should use defaults
	cfg := CacheConfig{
		TTL:     0,
		MaxSize: -1,
	}

	cache := NewSearchCache(cfg)

	assert.NotNil(t, cache)
	stats := cache.Stats()
	assert.Equal(t, DefaultCacheMaxSize, stats.MaxSize)
}

func TestSearchCache_GetSet(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	result := &search.SearchResult{
		Query:     "test query",
		TotalHits: 5,
		Documents: []search.ScoredDocument{
			{Score: 0.95},
			{Score: 0.85},
		},
	}

	cache.Set("key1", result)

	cached, found := cache.Get("key1")

	assert.True(t, found)
	require.NotNil(t, cached)
	assert.Equal(t, "test query", cached.Query)
	assert.Equal(t, int64(5), cached.TotalHits)
	assert.Len(t, cached.Documents, 2)
}

func TestSearchCache_GetMissing(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	cached, found := cache.Get("nonexistent")

	assert.False(t, found)
	assert.Nil(t, cached)
}

func TestSearchCache_TTLExpiry(t *testing.T) {
	cfg := CacheConfig{
		TTL:     50 * time.Millisecond,
		MaxSize: 100,
	}
	cache := NewSearchCache(cfg)

	result := &search.SearchResult{Query: "test"}
	cache.Set("key1", result)

	// Should be found immediately
	cached, found := cache.Get("key1")
	assert.True(t, found)
	assert.NotNil(t, cached)

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	cached, found = cache.Get("key1")
	assert.False(t, found)
	assert.Nil(t, cached)
}

func TestSearchCache_LRUEviction(t *testing.T) {
	cfg := CacheConfig{
		TTL:     5 * time.Minute,
		MaxSize: 3,
	}
	cache := NewSearchCache(cfg)

	// Fill cache to capacity
	cache.Set("key1", &search.SearchResult{Query: "query1"})
	cache.Set("key2", &search.SearchResult{Query: "query2"})
	cache.Set("key3", &search.SearchResult{Query: "query3"})

	assert.Equal(t, 3, cache.Size())

	// Access key1 to make it recently used
	cache.Get("key1")

	// Add new entry, should evict key2 (oldest not accessed)
	cache.Set("key4", &search.SearchResult{Query: "query4"})

	assert.Equal(t, 3, cache.Size())

	// key1 should still exist (recently accessed)
	_, found := cache.Get("key1")
	assert.True(t, found)

	// key4 should exist (just added)
	_, found = cache.Get("key4")
	assert.True(t, found)

	// key2 should be evicted
	_, found = cache.Get("key2")
	assert.False(t, found)
}

func TestSearchCache_Delete(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	cache.Set("key1", &search.SearchResult{Query: "test"})
	cache.Delete("key1")

	_, found := cache.Get("key1")
	assert.False(t, found)
}

func TestSearchCache_DeleteNonexistent(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	// Should not panic
	cache.Delete("nonexistent")

	assert.Equal(t, 0, cache.Size())
}

func TestSearchCache_Invalidate(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	cache.Set("key1", &search.SearchResult{Query: "query1"})
	cache.Set("key2", &search.SearchResult{Query: "query2"})

	initialVersion := cache.Version()

	cache.Invalidate()

	assert.Equal(t, 0, cache.Size())
	assert.Equal(t, initialVersion+1, cache.Version())

	_, found := cache.Get("key1")
	assert.False(t, found)

	_, found = cache.Get("key2")
	assert.False(t, found)
}

func TestSearchCache_Clear(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	cache.Set("key1", &search.SearchResult{Query: "query1"})
	cache.Set("key2", &search.SearchResult{Query: "query2"})

	initialVersion := cache.Version()

	cache.Clear()

	// Clear doesn't increment version
	assert.Equal(t, 0, cache.Size())
	assert.Equal(t, initialVersion, cache.Version())
}

func TestSearchCache_DisableEnable(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	cache.Set("key1", &search.SearchResult{Query: "test"})

	cache.Disable()

	// Get should miss when disabled
	_, found := cache.Get("key1")
	assert.False(t, found)

	// Set should be no-op when disabled
	cache.Set("key2", &search.SearchResult{Query: "test2"})

	cache.Enable()

	// key1 should still exist (was added before disable)
	_, found = cache.Get("key1")
	assert.True(t, found)

	// key2 should not exist (was added while disabled)
	_, found = cache.Get("key2")
	assert.False(t, found)
}

func TestSearchCache_IsEnabled(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	assert.True(t, cache.IsEnabled())

	cache.Disable()
	assert.False(t, cache.IsEnabled())

	cache.Enable()
	assert.True(t, cache.IsEnabled())
}

func TestSearchCache_ConcurrentAccess(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				key := "key" + string(rune('0'+id)) + string(rune('0'+j))
				cache.Set(key, &search.SearchResult{Query: key})
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				key := "key" + string(rune('0'+id)) + string(rune('0'+j))
				cache.Get(key)
			}
		}(i)
	}

	// Concurrent deleters
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				key := "key" + string(rune('0'+id)) + string(rune('0'+j))
				cache.Delete(key)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestSearchCache_UpdateExistingKey(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	cache.Set("key1", &search.SearchResult{Query: "original"})
	cache.Set("key1", &search.SearchResult{Query: "updated"})

	cached, found := cache.Get("key1")

	assert.True(t, found)
	assert.Equal(t, "updated", cached.Query)
	assert.Equal(t, 1, cache.Size())
}

func TestSearchCache_Stats(t *testing.T) {
	cfg := CacheConfig{
		TTL:     5 * time.Minute,
		MaxSize: 100,
	}
	cache := NewSearchCache(cfg)

	cache.Set("key1", &search.SearchResult{})
	cache.Set("key2", &search.SearchResult{})

	stats := cache.Stats()

	assert.Equal(t, 2, stats.Size)
	assert.Equal(t, 100, stats.MaxSize)
	assert.Equal(t, uint64(0), stats.Version)
	assert.True(t, stats.Enabled)
}

func TestSearchCache_CleanExpired(t *testing.T) {
	cfg := CacheConfig{
		TTL:     50 * time.Millisecond,
		MaxSize: 100,
	}
	cache := NewSearchCache(cfg)

	cache.Set("key1", &search.SearchResult{})
	cache.Set("key2", &search.SearchResult{})

	assert.Equal(t, 2, cache.Size())

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	cleaned := cache.CleanExpired()

	assert.Equal(t, 2, cleaned)
	assert.Equal(t, 0, cache.Size())
}

func TestSearchCache_CleanExpiredPartial(t *testing.T) {
	cfg := CacheConfig{
		TTL:     200 * time.Millisecond,
		MaxSize: 100,
	}
	cache := NewSearchCache(cfg)

	cache.Set("key1", &search.SearchResult{})

	// Wait half the TTL
	time.Sleep(100 * time.Millisecond)

	cache.Set("key2", &search.SearchResult{})

	// Wait for key1 to expire but not key2
	time.Sleep(150 * time.Millisecond)

	cleaned := cache.CleanExpired()

	assert.Equal(t, 1, cleaned)
	assert.Equal(t, 1, cache.Size())

	_, found := cache.Get("key2")
	assert.True(t, found)
}

func TestGenerateCacheKey(t *testing.T) {
	key1 := GenerateCacheKey("query", nil, 20, 0)
	key2 := GenerateCacheKey("query", nil, 20, 0)
	key3 := GenerateCacheKey("different", nil, 20, 0)

	// Same query should produce same key
	assert.Equal(t, key1, key2)

	// Different query should produce different key
	assert.NotEqual(t, key1, key3)
}

func TestGenerateCacheKey_WithFilters(t *testing.T) {
	filters := map[string]string{
		"type": "source_code",
		"path": "/src",
	}

	key1 := GenerateCacheKey("query", filters, 20, 0)
	key2 := GenerateCacheKey("query", nil, 20, 0)

	// Filters should change the key
	assert.NotEqual(t, key1, key2)
}

func TestGenerateCacheKey_WithPagination(t *testing.T) {
	// Same query, same limit, same offset -> same key
	key1 := GenerateCacheKey("query", nil, 20, 0)
	key2 := GenerateCacheKey("query", nil, 20, 0)
	assert.Equal(t, key1, key2)

	// Same query, different limit -> different key
	key3 := GenerateCacheKey("query", nil, 10, 0)
	assert.NotEqual(t, key1, key3)

	// Same query, different offset -> different key
	key4 := GenerateCacheKey("query", nil, 20, 10)
	assert.NotEqual(t, key1, key4)

	// Same query, same limit different offset -> different key
	key5 := GenerateCacheKey("query", nil, 10, 0)
	key6 := GenerateCacheKey("query", nil, 10, 5)
	assert.NotEqual(t, key5, key6)
}

func TestGenerateCacheKey_DeterministicFilterOrder(t *testing.T) {
	// Filters should produce the same key regardless of map iteration order
	filters1 := map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
	}
	filters2 := map[string]string{
		"c": "3",
		"a": "1",
		"b": "2",
	}

	key1 := GenerateCacheKey("query", filters1, 20, 0)
	key2 := GenerateCacheKey("query", filters2, 20, 0)

	assert.Equal(t, key1, key2, "Cache keys should be deterministic regardless of filter order")
}

func TestCacheKeyFromRequest(t *testing.T) {
	req := &search.SearchRequest{
		Query:  "test query",
		Limit:  10,
		Offset: 5,
	}

	key1 := CacheKeyFromRequest(req)
	key2 := CacheKeyFromRequest(req)

	// Same request should produce same key
	assert.Equal(t, key1, key2)

	// Different limit should produce different key
	req2 := &search.SearchRequest{
		Query:  "test query",
		Limit:  20,
		Offset: 5,
	}
	key3 := CacheKeyFromRequest(req2)
	assert.NotEqual(t, key1, key3)

	// Different offset should produce different key
	req3 := &search.SearchRequest{
		Query:  "test query",
		Limit:  10,
		Offset: 10,
	}
	key4 := CacheKeyFromRequest(req3)
	assert.NotEqual(t, key1, key4)
}

func TestCacheKeyFromRequest_WithFilters(t *testing.T) {
	req1 := &search.SearchRequest{
		Query:      "test",
		Limit:      20,
		Type:       search.DocTypeSourceCode,
		PathFilter: "/src",
	}

	req2 := &search.SearchRequest{
		Query: "test",
		Limit: 20,
	}

	key1 := CacheKeyFromRequest(req1)
	key2 := CacheKeyFromRequest(req2)

	// Filters should change the key
	assert.NotEqual(t, key1, key2)
}

func TestCachePaginationIndependence(t *testing.T) {
	// Test that different pagination parameters produce different cache entries
	cache := NewSearchCacheWithDefaults()

	result1 := &search.SearchResult{
		Query:     "test",
		TotalHits: 100,
		Documents: []search.ScoredDocument{
			{Score: 0.95},
		},
	}
	result2 := &search.SearchResult{
		Query:     "test",
		TotalHits: 100,
		Documents: []search.ScoredDocument{
			{Score: 0.85},
		},
	}

	// Cache results with different pagination
	key1 := GenerateCacheKey("test", nil, 10, 0)
	key2 := GenerateCacheKey("test", nil, 10, 10)

	cache.Set(key1, result1)
	cache.Set(key2, result2)

	// Verify they're stored separately
	cached1, found1 := cache.Get(key1)
	cached2, found2 := cache.Get(key2)

	assert.True(t, found1)
	assert.True(t, found2)
	assert.NotEqual(t, cached1.Documents[0].Score, cached2.Documents[0].Score)
	assert.Equal(t, 0.95, cached1.Documents[0].Score)
	assert.Equal(t, 0.85, cached2.Documents[0].Score)
}

func TestSearchCache_VersionIncrementsOnInvalidate(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	v1 := cache.Version()
	cache.Invalidate()
	v2 := cache.Version()
	cache.Invalidate()
	v3 := cache.Version()

	assert.Equal(t, uint64(0), v1)
	assert.Equal(t, uint64(1), v2)
	assert.Equal(t, uint64(2), v3)
}

func TestSearchCache_LRUOrder(t *testing.T) {
	cfg := CacheConfig{
		TTL:     5 * time.Minute,
		MaxSize: 3,
	}
	cache := NewSearchCache(cfg)

	cache.Set("key1", &search.SearchResult{Query: "q1"})
	cache.Set("key2", &search.SearchResult{Query: "q2"})
	cache.Set("key3", &search.SearchResult{Query: "q3"})

	// Access key1 and key2, making key3 the LRU
	cache.Get("key1")
	cache.Get("key2")

	// Add key4, should evict key3
	cache.Set("key4", &search.SearchResult{Query: "q4"})

	_, found := cache.Get("key1")
	assert.True(t, found)
	_, found = cache.Get("key2")
	assert.True(t, found)
	_, found = cache.Get("key3")
	assert.False(t, found, "key3 should be evicted as LRU")
	_, found = cache.Get("key4")
	assert.True(t, found)
}

func TestDefaultCacheConfig(t *testing.T) {
	cfg := DefaultCacheConfig()

	assert.Equal(t, DefaultCacheTTL, cfg.TTL)
	assert.Equal(t, DefaultCacheMaxSize, cfg.MaxSize)
}

func TestSearchCache_Size(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	assert.Equal(t, 0, cache.Size())

	cache.Set("key1", &search.SearchResult{})
	assert.Equal(t, 1, cache.Size())

	cache.Set("key2", &search.SearchResult{})
	assert.Equal(t, 2, cache.Size())

	cache.Delete("key1")
	assert.Equal(t, 1, cache.Size())

	cache.Clear()
	assert.Equal(t, 0, cache.Size())
}

func TestSearchCache_ConcurrentInvalidate(t *testing.T) {
	cache := NewSearchCacheWithDefaults()

	var wg sync.WaitGroup

	// Concurrent writers and invalidators
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				key := "key" + string(rune('0'+id))
				cache.Set(key, &search.SearchResult{})
			}
		}(i)
		go func() {
			defer wg.Done()
			cache.Invalidate()
		}()
	}

	wg.Wait()
	// Should not panic and version should be > 0
	assert.True(t, cache.Version() > 0)
}

func TestSearchCache_GetPromotion(t *testing.T) {
	cfg := CacheConfig{
		TTL:     5 * time.Minute,
		MaxSize: 2,
	}
	cache := NewSearchCache(cfg)

	cache.Set("key1", &search.SearchResult{Query: "q1"})
	cache.Set("key2", &search.SearchResult{Query: "q2"})

	// Get key1 to promote it
	cache.Get("key1")

	// Add key3, should evict key2 (not key1 since we promoted it)
	cache.Set("key3", &search.SearchResult{Query: "q3"})

	_, found := cache.Get("key1")
	assert.True(t, found, "key1 should exist after promotion")

	_, found = cache.Get("key2")
	assert.False(t, found, "key2 should be evicted")
}

// =============================================================================
// Test: Single-Lock LRU Promotion (PF.3.7)
// =============================================================================

func TestSearchCache_SingleLockPromotion(t *testing.T) {
	t.Run("get promotes entry with single lock acquisition", func(t *testing.T) {
		cfg := CacheConfig{
			TTL:     5 * time.Minute,
			MaxSize: 3,
		}
		cache := NewSearchCache(cfg)

		// Add entries
		cache.Set("key1", &search.SearchResult{Query: "q1"})
		cache.Set("key2", &search.SearchResult{Query: "q2"})
		cache.Set("key3", &search.SearchResult{Query: "q3"})

		// Access key1 to promote it
		result, found := cache.Get("key1")
		assert.True(t, found)
		assert.Equal(t, "q1", result.Query)

		// Add key4, should evict key2 (LRU after key1 was promoted)
		cache.Set("key4", &search.SearchResult{Query: "q4"})

		// key1 should still exist (promoted)
		_, found = cache.Get("key1")
		assert.True(t, found, "key1 should exist after promotion")

		// key2 should be evicted (was LRU)
		_, found = cache.Get("key2")
		assert.False(t, found, "key2 should be evicted")

		// key3 and key4 should exist
		_, found = cache.Get("key3")
		assert.True(t, found, "key3 should exist")
		_, found = cache.Get("key4")
		assert.True(t, found, "key4 should exist")
	})

	t.Run("get removes expired entry atomically", func(t *testing.T) {
		cfg := CacheConfig{
			TTL:     50 * time.Millisecond,
			MaxSize: 100,
		}
		cache := NewSearchCache(cfg)

		cache.Set("key1", &search.SearchResult{Query: "test"})

		// Wait for expiry
		time.Sleep(100 * time.Millisecond)

		// Get should return miss and remove the entry atomically
		result, found := cache.Get("key1")
		assert.False(t, found)
		assert.Nil(t, result)

		// Entry should be removed from cache
		assert.Equal(t, 0, cache.Size())
	})
}

func TestSearchCache_ConcurrentGetPromotion(t *testing.T) {
	t.Run("concurrent gets promote entries safely", func(t *testing.T) {
		cfg := CacheConfig{
			TTL:     5 * time.Minute,
			MaxSize: 100,
		}
		cache := NewSearchCache(cfg)

		// Add entries
		for i := 0; i < 10; i++ {
			key := "key" + string(rune('0'+i))
			cache.Set(key, &search.SearchResult{Query: key})
		}

		// Concurrent gets
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				key := "key" + string(rune('0'+(id%10)))
				result, found := cache.Get(key)
				if found {
					// Just access the result to ensure it's valid
					_ = result.Query
				}
			}(i)
		}

		wg.Wait()

		// All entries should still exist
		for i := 0; i < 10; i++ {
			key := "key" + string(rune('0'+i))
			_, found := cache.Get(key)
			assert.True(t, found, "key %s should still exist", key)
		}
	})
}

func TestSearchCache_GetDisabled(t *testing.T) {
	t.Run("get returns miss when disabled", func(t *testing.T) {
		cache := NewSearchCacheWithDefaults()

		cache.Set("key1", &search.SearchResult{Query: "test"})
		cache.Disable()

		result, found := cache.Get("key1")
		assert.False(t, found)
		assert.Nil(t, result)

		cache.Enable()

		// Should be found again after enabling
		result, found = cache.Get("key1")
		assert.True(t, found)
		assert.Equal(t, "test", result.Query)
	})
}
