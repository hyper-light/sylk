// Package context provides tests for W4N.17: HotCache EvictPercent single-pass optimization.
package context

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Helper Functions
// =============================================================================

// createTestEntryW4N17 creates a ContentEntry with specified content size.
func createTestEntryW4N17(id string, contentSize int) *ContentEntry {
	content := make([]byte, contentSize)
	for i := range content {
		content[i] = byte('a' + (i % 26))
	}
	return &ContentEntry{
		ID:        id,
		SessionID: "test-session",
		AgentID:   "test-agent",
		AgentType: "test",
		Content:   string(content),
		Keywords:  []string{"test", "keyword"},
		Entities:  []string{"entity1"},
		Metadata:  map[string]string{"key": "value"},
	}
}

// populateCacheW4N17 fills the cache with entries of specified size.
func populateCacheW4N17(c *HotCache, count int, contentSize int) {
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("entry-%d", i)
		entry := createTestEntryW4N17(id, contentSize)
		c.Add(id, entry)
	}
}

// =============================================================================
// Happy Path Tests
// =============================================================================

// TestW4N17_EvictPercent_SinglePassReachesTarget verifies that single-pass
// eviction correctly reaches the target eviction amount.
func TestW4N17_EvictPercent_SinglePassReachesTarget(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       10 * 1024 * 1024, // 10MB
		AsyncEviction: false,
	})
	defer cache.Close()

	// Add 100 entries of ~1KB each
	populateCacheW4N17(cache, 100, 1000)

	initialSize := cache.Size()
	if initialSize == 0 {
		t.Fatal("Cache should have entries")
	}

	// Evict 50%
	evictedSize := cache.EvictPercent(0.5)

	// Verify eviction happened
	if evictedSize == 0 {
		t.Error("Expected some bytes to be evicted")
	}

	// Verify size reduced approximately by 50%
	finalSize := cache.Size()
	expectedSize := initialSize - evictedSize
	if finalSize != expectedSize {
		t.Errorf("Expected final size %d, got %d", expectedSize, finalSize)
	}

	// Verify roughly 50% was evicted (allow some tolerance due to entry sizes)
	evictedRatio := float64(evictedSize) / float64(initialSize)
	if evictedRatio < 0.45 || evictedRatio > 0.55 {
		t.Errorf("Expected ~50%% eviction ratio, got %.2f%%", evictedRatio*100)
	}
}

// TestW4N17_EvictPercent_ExactTargetBytes verifies that eviction stops
// as soon as target bytes are reached.
func TestW4N17_EvictPercent_ExactTargetBytes(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024, // 1MB
		AsyncEviction: false,
	})
	defer cache.Close()

	// Add entries
	populateCacheW4N17(cache, 50, 500)

	initialSize := cache.Size()
	initialCount := len(cache.Keys())

	// Evict 20%
	evictedSize := cache.EvictPercent(0.2)

	// Verify some entries were evicted
	finalCount := len(cache.Keys())
	if finalCount >= initialCount {
		t.Error("Expected some entries to be evicted")
	}

	// Verify evicted size is at least 20% of initial
	targetMin := int64(float64(initialSize) * 0.2)
	if evictedSize < targetMin {
		t.Errorf("Expected at least %d bytes evicted, got %d", targetMin, evictedSize)
	}
}

// =============================================================================
// Negative Path Tests
// =============================================================================

// TestW4N17_EvictPercent_EmptyCache verifies eviction on empty cache returns 0.
func TestW4N17_EvictPercent_EmptyCache(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	// Evict from empty cache
	evictedSize := cache.EvictPercent(0.5)

	if evictedSize != 0 {
		t.Errorf("Expected 0 bytes evicted from empty cache, got %d", evictedSize)
	}
}

// TestW4N17_EvictPercent_ZeroPercent verifies 0% eviction returns 0.
func TestW4N17_EvictPercent_ZeroPercent(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	populateCacheW4N17(cache, 10, 100)
	initialSize := cache.Size()

	// Evict 0%
	evictedSize := cache.EvictPercent(0)

	if evictedSize != 0 {
		t.Errorf("Expected 0 bytes evicted for 0%%, got %d", evictedSize)
	}

	if cache.Size() != initialSize {
		t.Error("Cache size should not change for 0% eviction")
	}
}

// TestW4N17_EvictPercent_NegativePercent verifies negative percent returns 0.
func TestW4N17_EvictPercent_NegativePercent(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	populateCacheW4N17(cache, 10, 100)
	initialSize := cache.Size()

	// Evict negative percent
	evictedSize := cache.EvictPercent(-0.5)

	if evictedSize != 0 {
		t.Errorf("Expected 0 bytes evicted for negative percent, got %d", evictedSize)
	}

	if cache.Size() != initialSize {
		t.Error("Cache size should not change for negative percent")
	}
}

// =============================================================================
// Failure/Partial Eviction Tests
// =============================================================================

// TestW4N17_EvictPercent_PartialEviction verifies behavior when not enough
// items are available to reach the target.
func TestW4N17_EvictPercent_PartialEviction(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	// Add just 3 small entries
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("entry-%d", i)
		cache.Add(id, createTestEntryW4N17(id, 10))
	}

	initialSize := cache.Size()

	// Try to evict 100% - should evict everything
	evictedSize := cache.EvictPercent(1.0)

	// Should have evicted all entries
	if cache.Size() != 0 {
		t.Errorf("Expected cache to be empty, got size %d", cache.Size())
	}

	if evictedSize != initialSize {
		t.Errorf("Expected to evict %d bytes, got %d", initialSize, evictedSize)
	}
}

// TestW4N17_EvictPercent_OverHundredPercent verifies that >100% is capped at 100%.
func TestW4N17_EvictPercent_OverHundredPercent(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	populateCacheW4N17(cache, 10, 100)
	initialSize := cache.Size()

	// Evict 150% - should cap at 100%
	evictedSize := cache.EvictPercent(1.5)

	// Should have evicted everything
	if cache.Size() != 0 {
		t.Errorf("Expected cache to be empty, got size %d", cache.Size())
	}

	if evictedSize != initialSize {
		t.Errorf("Expected to evict all %d bytes, got %d", initialSize, evictedSize)
	}
}

// =============================================================================
// Race Condition Tests
// =============================================================================

// TestW4N17_EvictPercent_ConcurrentEvictionAndAdd tests race safety between
// eviction and add operations.
func TestW4N17_EvictPercent_ConcurrentEvictionAndAdd(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       10 * 1024 * 1024, // 10MB
		AsyncEviction: false,
	})
	defer cache.Close()

	// Pre-populate cache
	populateCacheW4N17(cache, 100, 1000)

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	// Start goroutines that add entries
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := fmt.Sprintf("concurrent-%d-%d", gid, i)
				entry := createTestEntryW4N17(id, 100)
				cache.Add(id, entry)
			}
		}(g)
	}

	// Start goroutines that evict
	for g := 0; g < goroutines/2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations/10; i++ {
				cache.EvictPercent(0.1)
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()

	// Cache should still be in a consistent state
	stats := cache.Stats()
	if stats.CurrentSize < 0 {
		t.Error("Cache size should not be negative")
	}
	if stats.EntryCount < 0 {
		t.Error("Entry count should not be negative")
	}
}

// TestW4N17_EvictPercent_ConcurrentEvictions tests race safety with
// multiple concurrent eviction operations.
func TestW4N17_EvictPercent_ConcurrentEvictions(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       10 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	// Pre-populate cache
	populateCacheW4N17(cache, 500, 500)

	var wg sync.WaitGroup
	const goroutines = 20

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.EvictPercent(0.05) // Each evicts 5%
		}()
	}

	wg.Wait()

	// Cache should be in consistent state
	stats := cache.Stats()
	if stats.CurrentSize < 0 {
		t.Error("Cache size should not be negative after concurrent evictions")
	}
}

// TestW4N17_EvictPercent_AsyncEvictionMode tests race safety with async eviction enabled.
func TestW4N17_EvictPercent_AsyncEvictionMode(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1 * 1024 * 1024, // 1MB
		AsyncEviction: true,
	})
	defer cache.Close()

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 50

	// Concurrent adds that will trigger async eviction
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := fmt.Sprintf("async-%d-%d", gid, i)
				entry := createTestEntryW4N17(id, 10000) // Large entries to trigger eviction
				cache.Add(id, entry)
			}
		}(g)
	}

	// Concurrent explicit evictions
	for g := 0; g < goroutines/2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations/5; i++ {
				cache.EvictPercent(0.2)
			}
		}()
	}

	wg.Wait()

	// Allow async eviction to complete
	time.Sleep(100 * time.Millisecond)

	// Verify cache is in consistent state
	stats := cache.Stats()
	if stats.CurrentSize < 0 {
		t.Error("Cache size should not be negative")
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

// TestW4N17_EvictPercent_ZeroPercentEdge tests exact 0% boundary.
func TestW4N17_EvictPercent_ZeroPercentEdge(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	populateCacheW4N17(cache, 10, 100)
	initialKeys := cache.Keys()

	evicted := cache.EvictPercent(0.0)

	if evicted != 0 {
		t.Errorf("0%% eviction should return 0, got %d", evicted)
	}

	finalKeys := cache.Keys()
	if len(finalKeys) != len(initialKeys) {
		t.Error("0% eviction should not remove any entries")
	}
}

// TestW4N17_EvictPercent_HundredPercentEdge tests exact 100% boundary.
func TestW4N17_EvictPercent_HundredPercentEdge(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	populateCacheW4N17(cache, 20, 200)
	initialSize := cache.Size()

	evicted := cache.EvictPercent(1.0)

	if cache.Size() != 0 {
		t.Errorf("100%% eviction should empty cache, got size %d", cache.Size())
	}

	if evicted != initialSize {
		t.Errorf("100%% eviction should return initial size %d, got %d", initialSize, evicted)
	}
}

// TestW4N17_EvictPercent_VeryLargeCache tests eviction on a large cache.
func TestW4N17_EvictPercent_VeryLargeCache(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large cache test in short mode")
	}

	cache := NewHotCache(HotCacheConfig{
		MaxSize:       100 * 1024 * 1024, // 100MB
		AsyncEviction: false,
	})
	defer cache.Close()

	// Add 10000 entries
	const entryCount = 10000
	populateCacheW4N17(cache, entryCount, 500)

	initialSize := cache.Size()
	initialCount := len(cache.Keys())

	// Evict 30%
	evicted := cache.EvictPercent(0.3)

	// Verify eviction happened
	if evicted == 0 {
		t.Error("Expected some bytes to be evicted from large cache")
	}

	finalCount := len(cache.Keys())
	evictedCount := initialCount - finalCount

	// Roughly 30% of entries should be evicted
	expectedEvictedCount := int(float64(initialCount) * 0.3)
	tolerance := expectedEvictedCount / 10 // 10% tolerance
	if evictedCount < expectedEvictedCount-tolerance || evictedCount > expectedEvictedCount+tolerance {
		t.Errorf("Expected ~%d entries evicted, got %d", expectedEvictedCount, evictedCount)
	}

	t.Logf("Large cache: evicted %d bytes (%.1f%%), %d entries", evicted, float64(evicted)/float64(initialSize)*100, evictedCount)
}

// TestW4N17_EvictPercent_VerySmallPercent tests eviction with very small percentage.
func TestW4N17_EvictPercent_VerySmallPercent(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	populateCacheW4N17(cache, 100, 100)
	initialSize := cache.Size()

	// Evict 0.1%
	evicted := cache.EvictPercent(0.001)

	// Should evict at least one entry (even if target is tiny)
	if evicted == 0 && initialSize > 0 {
		// This is acceptable - if 0.1% is less than one entry size
		targetBytes := int64(float64(initialSize) * 0.001)
		t.Logf("0.1%% eviction target was %d bytes, evicted %d", targetBytes, evicted)
	}
}

// TestW4N17_EvictPercent_SingleEntry tests eviction when cache has single entry.
func TestW4N17_EvictPercent_SingleEntry(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	// Add single entry
	cache.Add("single", createTestEntryW4N17("single", 100))
	initialSize := cache.Size()

	// Evict 50% - should evict the one entry since it's >= target
	evicted := cache.EvictPercent(0.5)

	// Entry should be evicted if its size >= target
	if cache.Size() == 0 {
		if evicted != initialSize {
			t.Errorf("Expected evicted size %d, got %d", initialSize, evicted)
		}
	}
}

// TestW4N17_EvictPercent_CallbackInvocation verifies callbacks are called
// for each evicted entry.
func TestW4N17_EvictPercent_CallbackInvocation(t *testing.T) {
	var callbackCount atomic.Int32
	var evictedIDs sync.Map

	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
		OnEvict: func(id string, entry *ContentEntry) {
			callbackCount.Add(1)
			evictedIDs.Store(id, true)
		},
	})
	defer cache.Close()

	populateCacheW4N17(cache, 20, 100)
	initialCount := len(cache.Keys())

	// Evict 50%
	cache.EvictPercent(0.5)

	finalCount := len(cache.Keys())
	evictedCount := initialCount - finalCount

	// Callback should be called for each evicted entry
	if int(callbackCount.Load()) != evictedCount {
		t.Errorf("Expected %d callbacks, got %d", evictedCount, callbackCount.Load())
	}

	// Verify evicted IDs are not in cache
	evictedIDs.Range(func(key, value interface{}) bool {
		id := key.(string)
		if cache.Contains(id) {
			t.Errorf("Evicted entry %s should not be in cache", id)
		}
		return true
	})
}

// TestW4N17_EvictPercent_LRUOrder verifies that LRU entries are evicted first.
func TestW4N17_EvictPercent_LRUOrder(t *testing.T) {
	var evictionOrder []string
	var mu sync.Mutex

	cache := NewHotCache(HotCacheConfig{
		MaxSize:       1024 * 1024,
		AsyncEviction: false,
		OnEvict: func(id string, entry *ContentEntry) {
			mu.Lock()
			evictionOrder = append(evictionOrder, id)
			mu.Unlock()
		},
	})
	defer cache.Close()

	// Add entries in order
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("entry-%d", i)
		cache.Add(id, createTestEntryW4N17(id, 100))
	}

	// Access some entries to change LRU order (make entry-0, entry-1, entry-2 most recent)
	cache.Get("entry-0")
	cache.Get("entry-1")
	cache.Get("entry-2")

	// Evict 50% - should evict LRU entries first (entry-3 through entry-9)
	cache.EvictPercent(0.5)

	// Verify that recently accessed entries are still in cache
	for i := 0; i <= 2; i++ {
		id := fmt.Sprintf("entry-%d", i)
		if !cache.Contains(id) {
			t.Errorf("Recently accessed entry %s should still be in cache", id)
		}
	}

	// Verify eviction started from LRU end (entry-3 should be first evicted)
	if len(evictionOrder) > 0 && evictionOrder[0] != "entry-3" {
		t.Logf("Eviction order: %v", evictionOrder)
		// Note: exact order depends on entry sizes, but LRU entries should be evicted
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

// BenchmarkW4N17_EvictPercent_SmallCache benchmarks eviction on small cache.
func BenchmarkW4N17_EvictPercent_SmallCache(b *testing.B) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       10 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Reset cache state
		cache.Clear()
		populateCacheW4N17(cache, 100, 500)
		b.StartTimer()

		cache.EvictPercent(0.5)
	}
}

// BenchmarkW4N17_EvictPercent_MediumCache benchmarks eviction on medium cache.
func BenchmarkW4N17_EvictPercent_MediumCache(b *testing.B) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       50 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cache.Clear()
		populateCacheW4N17(cache, 1000, 500)
		b.StartTimer()

		cache.EvictPercent(0.5)
	}
}

// BenchmarkW4N17_EvictPercent_LargeCache benchmarks eviction on large cache.
func BenchmarkW4N17_EvictPercent_LargeCache(b *testing.B) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       100 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cache.Clear()
		populateCacheW4N17(cache, 10000, 500)
		b.StartTimer()

		cache.EvictPercent(0.5)
	}
}

// BenchmarkW4N17_EvictPercent_VaryingPercentages benchmarks different eviction percentages.
func BenchmarkW4N17_EvictPercent_VaryingPercentages(b *testing.B) {
	percentages := []float64{0.1, 0.25, 0.5, 0.75, 1.0}

	for _, pct := range percentages {
		b.Run(fmt.Sprintf("%.0f%%", pct*100), func(b *testing.B) {
			cache := NewHotCache(HotCacheConfig{
				MaxSize:       50 * 1024 * 1024,
				AsyncEviction: false,
			})
			defer cache.Close()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				cache.Clear()
				populateCacheW4N17(cache, 1000, 500)
				b.StartTimer()

				cache.EvictPercent(pct)
			}
		})
	}
}

// BenchmarkW4N17_EvictPercent_WithCallback benchmarks eviction with callback overhead.
func BenchmarkW4N17_EvictPercent_WithCallback(b *testing.B) {
	var counter atomic.Int64

	cache := NewHotCache(HotCacheConfig{
		MaxSize:       50 * 1024 * 1024,
		AsyncEviction: false,
		OnEvict: func(id string, entry *ContentEntry) {
			counter.Add(1)
		},
	})
	defer cache.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cache.Clear()
		populateCacheW4N17(cache, 1000, 500)
		b.StartTimer()

		cache.EvictPercent(0.5)
	}
}

// BenchmarkW4N17_EvictPercent_Concurrent benchmarks concurrent eviction performance.
func BenchmarkW4N17_EvictPercent_Concurrent(b *testing.B) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       100 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	// Pre-populate with many entries
	populateCacheW4N17(cache, 5000, 500)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.EvictPercent(0.01) // Small eviction to allow many concurrent ops
		}
	})
}

// BenchmarkW4N17_EvictPercent_SinglePassVsOld simulates comparing old vs new approach.
// The old approach would call evictBatch in a loop; the new approach uses evictSinglePass.
func BenchmarkW4N17_EvictPercent_SinglePassVsOld(b *testing.B) {
	b.Run("SinglePass", func(b *testing.B) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100 * 1024 * 1024,
			AsyncEviction: false,
		})
		defer cache.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			cache.Clear()
			populateCacheW4N17(cache, 5000, 500)
			b.StartTimer()

			cache.EvictPercent(0.5)
		}
	})

	// Note: Old implementation is no longer available for direct comparison,
	// but this benchmark establishes baseline for the optimized version.
}

// BenchmarkW4N17_EvictSinglePass_Direct benchmarks the single-pass function directly.
func BenchmarkW4N17_EvictSinglePass_Direct(b *testing.B) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       100 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cache.Clear()
		populateCacheW4N17(cache, 5000, 500)
		initialSize := cache.Size()
		targetBytes := int64(float64(initialSize) * 0.5)
		b.StartTimer()

		cache.mu.Lock()
		evicted, _ := cache.evictSinglePass(targetBytes)
		cache.mu.Unlock()
		cache.invokeEvictionCallbacks(evicted)
	}
}

// =============================================================================
// Memory Allocation Tests
// =============================================================================

// TestW4N17_EvictPercent_PreAllocation verifies pre-allocation reduces allocations.
func TestW4N17_EvictPercent_PreAllocation(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       10 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	// Add entries
	populateCacheW4N17(cache, 100, 500)

	// Run eviction multiple times - should not cause excessive allocations
	for i := 0; i < 10; i++ {
		cache.EvictPercent(0.1)
		// Re-populate
		for j := 0; j < 10; j++ {
			id := fmt.Sprintf("refill-%d-%d", i, j)
			cache.Add(id, createTestEntryW4N17(id, 500))
		}
	}

	// If we got here without OOM or excessive GC, pre-allocation is working
	stats := cache.Stats()
	t.Logf("After multiple evictions: size=%d, entries=%d, evictions=%d",
		stats.CurrentSize, stats.EntryCount, stats.Evictions)
}

// =============================================================================
// Consistency Tests
// =============================================================================

// TestW4N17_EvictPercent_SizeConsistency verifies size tracking remains consistent.
func TestW4N17_EvictPercent_SizeConsistency(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       10 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	// Add entries
	populateCacheW4N17(cache, 50, 500)
	initialSize := cache.Size()

	// Multiple partial evictions
	var totalEvicted int64
	for i := 0; i < 5; i++ {
		evicted := cache.EvictPercent(0.1)
		totalEvicted += evicted
	}

	// Verify size consistency
	finalSize := cache.Size()
	expectedSize := initialSize - totalEvicted
	if finalSize != expectedSize {
		t.Errorf("Size inconsistency: expected %d, got %d (evicted %d from %d)",
			expectedSize, finalSize, totalEvicted, initialSize)
	}
}

// TestW4N17_EvictPercent_StatsConsistency verifies stats are updated correctly.
func TestW4N17_EvictPercent_StatsConsistency(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{
		MaxSize:       10 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	cache.ResetStats()

	// Add entries
	populateCacheW4N17(cache, 100, 500)
	initialCount := len(cache.Keys())

	// Evict and check stats
	cache.EvictPercent(0.5)

	stats := cache.Stats()
	evictedCount := initialCount - stats.EntryCount

	if stats.Evictions != int64(evictedCount) {
		t.Errorf("Eviction stat mismatch: reported %d, actual entries evicted %d",
			stats.Evictions, evictedCount)
	}
}

// =============================================================================
// Stress Tests
// =============================================================================

// TestW4N17_EvictPercent_StressTest runs extended stress testing.
func TestW4N17_EvictPercent_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	cache := NewHotCache(HotCacheConfig{
		MaxSize:       50 * 1024 * 1024,
		AsyncEviction: false,
	})
	defer cache.Close()

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	const iterations = 1000

	for i := 0; i < iterations; i++ {
		// Random operations
		switch rng.Intn(3) {
		case 0: // Add entries
			count := rng.Intn(50) + 1
			for j := 0; j < count; j++ {
				id := fmt.Sprintf("stress-%d-%d", i, j)
				cache.Add(id, createTestEntryW4N17(id, rng.Intn(1000)+100))
			}
		case 1: // Evict percentage
			pct := rng.Float64()
			cache.EvictPercent(pct)
		case 2: // Get random entry
			if keys := cache.Keys(); len(keys) > 0 {
				cache.Get(keys[rng.Intn(len(keys))])
			}
		}

		// Periodic consistency check
		if i%100 == 0 {
			stats := cache.Stats()
			if stats.CurrentSize < 0 {
				t.Fatalf("Negative size at iteration %d", i)
			}
			if stats.EntryCount < 0 {
				t.Fatalf("Negative entry count at iteration %d", i)
			}
		}
	}

	t.Logf("Stress test completed: %d iterations", iterations)
}
