package context

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Helper Functions
// =============================================================================

func makeTestEntry(id string, contentSize int) *ContentEntry {
	content := make([]byte, contentSize)
	for i := range content {
		content[i] = byte('a' + (i % 26))
	}
	return &ContentEntry{
		ID:          id,
		SessionID:   "session-1",
		AgentID:     "agent-1",
		AgentType:   "coder",
		ContentType: ContentTypeCodeFile,
		Content:     string(content),
		TokenCount:  contentSize / 4,
		Timestamp:   time.Now(),
	}
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewHotCache_DefaultConfig(t *testing.T) {
	cache := NewDefaultHotCache()

	if cache.MaxSize() != DefaultHotCacheMaxSize {
		t.Errorf("expected max size %d, got %d", DefaultHotCacheMaxSize, cache.MaxSize())
	}
	if cache.Size() != 0 {
		t.Error("expected empty cache")
	}
}

func TestNewHotCache_CustomConfig(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 1024 * 1024})

	if cache.MaxSize() != 1024*1024 {
		t.Errorf("expected max size 1MB, got %d", cache.MaxSize())
	}
}

func TestNewHotCache_InvalidConfigUsesDefault(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: -1})

	if cache.MaxSize() != DefaultHotCacheMaxSize {
		t.Errorf("expected default max size, got %d", cache.MaxSize())
	}
}

// =============================================================================
// EvictableCache Interface Tests
// =============================================================================

func TestName_ReturnsCorrectName(t *testing.T) {
	cache := NewDefaultHotCache()

	if cache.Name() != HotCacheName {
		t.Errorf("expected '%s', got '%s'", HotCacheName, cache.Name())
	}
}

func TestSize_ReturnsCurrentSize(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	if cache.Size() != 0 {
		t.Error("expected 0 for empty cache")
	}

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	size1 := cache.Size()
	if size1 <= 0 {
		t.Error("expected positive size after add")
	}

	cache.Add("entry-2", makeTestEntry("entry-2", 1000))
	size2 := cache.Size()
	if size2 <= size1 {
		t.Error("expected size to increase")
	}
}

func TestEvictPercent_EvictsLRUEntries(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	// Add entries
	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))
	cache.Add("entry-3", makeTestEntry("entry-3", 1000))

	sizeBefore := cache.Size()

	// Evict 50%
	evicted := cache.EvictPercent(0.5)
	if evicted <= 0 {
		t.Error("expected positive evicted bytes")
	}

	sizeAfter := cache.Size()
	if sizeAfter >= sizeBefore {
		t.Error("expected size to decrease after eviction")
	}
}

func TestEvictPercent_EvictsOldestFirst(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	// Add entries in order (entry-1 is oldest)
	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))
	cache.Add("entry-3", makeTestEntry("entry-3", 1000))

	// Evict ~33% (should remove entry-1)
	cache.EvictPercent(0.33)

	// entry-1 should be gone (oldest/LRU)
	if cache.Contains("entry-1") {
		t.Error("expected entry-1 to be evicted (oldest)")
	}

	// entry-3 should still exist (newest)
	if !cache.Contains("entry-3") {
		t.Error("expected entry-3 to exist (newest)")
	}
}

func TestEvictPercent_ZeroPercent(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	sizeBefore := cache.Size()

	evicted := cache.EvictPercent(0)
	if evicted != 0 {
		t.Error("expected 0 evicted for 0%")
	}

	if cache.Size() != sizeBefore {
		t.Error("expected no change for 0%")
	}
}

func TestEvictPercent_OverHundredPercent(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))

	// Evict > 100% should clear all
	cache.EvictPercent(1.5)

	stats := cache.Stats()
	if stats.EntryCount != 0 {
		t.Error("expected empty cache after >100% eviction")
	}
}

// =============================================================================
// SetMaxSize Tests
// =============================================================================

func TestSetMaxSize_TriggersEviction(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	// Add entries
	cache.Add("entry-1", makeTestEntry("entry-1", 5000))
	cache.Add("entry-2", makeTestEntry("entry-2", 5000))
	cache.Add("entry-3", makeTestEntry("entry-3", 5000))

	sizeBefore := cache.Size()

	// Reduce max size to less than current
	cache.SetMaxSize(sizeBefore / 2)

	sizeAfter := cache.Size()
	if sizeAfter > cache.MaxSize() {
		t.Error("expected size to be at or below new max after SetMaxSize")
	}
}

func TestSetMaxSize_NoEvictionIfUnderLimit(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	countBefore := cache.Stats().EntryCount

	// Increase max size
	cache.SetMaxSize(200000)

	countAfter := cache.Stats().EntryCount
	if countAfter != countBefore {
		t.Error("expected no change when increasing max size")
	}
}

func TestDefaultMaxSize_ReturnsConfiguredDefault(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 5000000})

	if cache.DefaultMaxSize() != 5000000 {
		t.Errorf("expected 5000000, got %d", cache.DefaultMaxSize())
	}

	// Change max size
	cache.SetMaxSize(1000000)

	// DefaultMaxSize should still return original
	if cache.DefaultMaxSize() != 5000000 {
		t.Error("expected DefaultMaxSize to remain unchanged")
	}
}

// =============================================================================
// Add/Get Tests
// =============================================================================

func TestAdd_AddsEntry(t *testing.T) {
	cache := NewDefaultHotCache()

	entry := makeTestEntry("test-1", 1000)
	cache.Add("test-1", entry)

	if !cache.Contains("test-1") {
		t.Error("expected entry to exist after add")
	}
}

func TestAdd_UpdatesExistingEntry(t *testing.T) {
	cache := NewDefaultHotCache()

	entry1 := makeTestEntry("test-1", 1000)
	cache.Add("test-1", entry1)

	entry2 := makeTestEntry("test-1", 2000)
	entry2.Content = "updated content"
	cache.Add("test-1", entry2)

	result := cache.Get("test-1")
	if result.Content != "updated content" {
		t.Error("expected entry to be updated")
	}

	// Should still be 1 entry
	stats := cache.Stats()
	if stats.EntryCount != 1 {
		t.Errorf("expected 1 entry, got %d", stats.EntryCount)
	}
}

func TestAdd_NilEntry(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("test-1", nil)

	if cache.Contains("test-1") {
		t.Error("expected nil entry to not be added")
	}
}

func TestGet_ReturnsEntry(t *testing.T) {
	cache := NewDefaultHotCache()

	entry := makeTestEntry("test-1", 1000)
	cache.Add("test-1", entry)

	result := cache.Get("test-1")
	if result == nil {
		t.Fatal("expected entry to be returned")
	}
	if result.ID != "test-1" {
		t.Errorf("expected ID 'test-1', got '%s'", result.ID)
	}
}

func TestGet_UpdatesLRU(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	// Add entries
	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))
	cache.Add("entry-3", makeTestEntry("entry-3", 1000))

	// Access entry-1 (moves to front)
	cache.Get("entry-1")

	// LRU order should now be: entry-1, entry-3, entry-2
	order := cache.LRUOrder()
	if len(order) < 3 {
		t.Fatalf("expected 3 entries, got %d", len(order))
	}
	if order[0] != "entry-1" {
		t.Errorf("expected entry-1 at front after Get, got %s", order[0])
	}
}

func TestGet_ReturnNilForMissing(t *testing.T) {
	cache := NewDefaultHotCache()

	result := cache.Get("nonexistent")
	if result != nil {
		t.Error("expected nil for missing entry")
	}
}

func TestContains_ChecksExistence(t *testing.T) {
	cache := NewDefaultHotCache()

	if cache.Contains("test-1") {
		t.Error("expected false for missing entry")
	}

	cache.Add("test-1", makeTestEntry("test-1", 1000))

	if !cache.Contains("test-1") {
		t.Error("expected true after add")
	}
}

func TestContains_DoesNotUpdateLRU(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))
	cache.Add("entry-3", makeTestEntry("entry-3", 1000))

	orderBefore := cache.LRUOrder()

	// Contains should not update LRU
	cache.Contains("entry-1")

	orderAfter := cache.LRUOrder()

	// Order should be unchanged
	for i := range orderBefore {
		if orderBefore[i] != orderAfter[i] {
			t.Error("Contains should not update LRU order")
			break
		}
	}
}

// =============================================================================
// Remove Tests
// =============================================================================

func TestRemove_RemovesEntry(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("test-1", makeTestEntry("test-1", 1000))

	if !cache.Remove("test-1") {
		t.Error("expected Remove to return true")
	}

	if cache.Contains("test-1") {
		t.Error("expected entry to be removed")
	}
}

func TestRemove_ReturnsFalseForMissing(t *testing.T) {
	cache := NewDefaultHotCache()

	if cache.Remove("nonexistent") {
		t.Error("expected Remove to return false for missing entry")
	}
}

func TestRemove_UpdatesSize(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("test-1", makeTestEntry("test-1", 1000))
	sizeBefore := cache.Size()

	cache.Remove("test-1")

	if cache.Size() >= sizeBefore {
		t.Error("expected size to decrease after remove")
	}
}

// =============================================================================
// Automatic Eviction Tests
// =============================================================================

func TestAdd_EvictsWhenOverLimit(t *testing.T) {
	// Very small cache
	cache := NewHotCache(HotCacheConfig{MaxSize: 5000})

	// Add entries until over limit
	for i := 0; i < 10; i++ {
		cache.Add("entry-"+string(rune('a'+i)), makeTestEntry("entry-"+string(rune('a'+i)), 1000))
	}

	// Should be at or under max size
	if cache.Size() > cache.MaxSize() {
		t.Error("expected size to be at or under max after adds")
	}

	// Oldest entries should be evicted
	stats := cache.Stats()
	if stats.EntryCount == 10 {
		t.Error("expected some entries to be evicted")
	}
}

// =============================================================================
// Batch Operations Tests
// =============================================================================

func TestAddBatch_AddsMultiple(t *testing.T) {
	cache := NewDefaultHotCache()

	entries := map[string]*ContentEntry{
		"entry-1": makeTestEntry("entry-1", 1000),
		"entry-2": makeTestEntry("entry-2", 1000),
		"entry-3": makeTestEntry("entry-3", 1000),
	}

	cache.AddBatch(entries)

	for id := range entries {
		if !cache.Contains(id) {
			t.Errorf("expected %s to exist", id)
		}
	}
}

func TestGetBatch_ReturnsMultiple(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))
	cache.Add("entry-3", makeTestEntry("entry-3", 1000))

	result := cache.GetBatch([]string{"entry-1", "entry-3", "nonexistent"})

	if len(result) != 2 {
		t.Errorf("expected 2 results, got %d", len(result))
	}
	if _, ok := result["entry-1"]; !ok {
		t.Error("expected entry-1 in results")
	}
	if _, ok := result["nonexistent"]; ok {
		t.Error("expected nonexistent to not be in results")
	}
}

func TestClear_RemovesAllEntries(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))

	cache.Clear()

	stats := cache.Stats()
	if stats.EntryCount != 0 {
		t.Error("expected empty cache after clear")
	}
	if cache.Size() != 0 {
		t.Error("expected 0 size after clear")
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestStats_TracksHitsAndMisses(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))

	// 2 hits
	cache.Get("entry-1")
	cache.Get("entry-1")

	// 3 misses
	cache.Get("nonexistent-1")
	cache.Get("nonexistent-2")
	cache.Get("nonexistent-3")

	stats := cache.Stats()
	if stats.Hits != 2 {
		t.Errorf("expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 3 {
		t.Errorf("expected 3 misses, got %d", stats.Misses)
	}
	if stats.HitRate < 0.39 || stats.HitRate > 0.41 {
		t.Errorf("expected hit rate ~0.4, got %f", stats.HitRate)
	}
}

func TestResetStats_ClearsCounters(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Get("entry-1")
	cache.Get("nonexistent")

	cache.ResetStats()

	stats := cache.Stats()
	if stats.Hits != 0 || stats.Misses != 0 {
		t.Error("expected 0 hits and misses after reset")
	}
}

// =============================================================================
// Iteration Tests
// =============================================================================

func TestKeys_ReturnsAllKeys(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))
	cache.Add("entry-3", makeTestEntry("entry-3", 1000))

	keys := cache.Keys()
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}
}

func TestEntries_ReturnsAllEntries(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))

	entries := cache.Entries()
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestLRUOrder_ReturnsCorrectOrder(t *testing.T) {
	cache := NewDefaultHotCache()

	cache.Add("entry-1", makeTestEntry("entry-1", 1000))
	cache.Add("entry-2", makeTestEntry("entry-2", 1000))
	cache.Add("entry-3", makeTestEntry("entry-3", 1000))

	order := cache.LRUOrder()

	// Most recent first
	if order[0] != "entry-3" {
		t.Errorf("expected entry-3 first (most recent), got %s", order[0])
	}
	if order[2] != "entry-1" {
		t.Errorf("expected entry-1 last (oldest), got %s", order[2])
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestHotCache_ConcurrentAddGet(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 1000000})

	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cache.Add("entry-"+string(rune('a'+(id%26))), makeTestEntry("entry-"+string(rune('a'+(id%26))), 100))
		}(i)
	}

	// Concurrent gets
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cache.Get("entry-" + string(rune('a'+(id%26))))
		}(i)
	}

	wg.Wait()

	// Should not panic
	stats := cache.Stats()
	if stats.EntryCount == 0 {
		t.Error("expected some entries")
	}
}

func TestHotCache_ConcurrentAddEvict(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 10000})

	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cache.Add("entry-"+string(rune(id)), makeTestEntry("entry-"+string(rune(id)), 500))
		}(i)
	}

	// Concurrent evictions
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.EvictPercent(0.1)
		}()
	}

	wg.Wait()

	// Should be at or under max size
	if cache.Size() > cache.MaxSize() {
		t.Error("cache size should not exceed max")
	}
}

func TestHotCache_ConcurrentMixedOperations(t *testing.T) {
	cache := NewHotCache(HotCacheConfig{MaxSize: 100000})

	var wg sync.WaitGroup

	// Adds
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cache.Add("entry-"+string(rune('a'+(id%26))), makeTestEntry("entry-"+string(rune('a'+(id%26))), 100))
		}(i)
	}

	// Gets
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cache.Get("entry-" + string(rune('a'+(id%26))))
		}(i)
	}

	// Contains
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cache.Contains("entry-" + string(rune('a'+(id%26))))
		}(i)
	}

	// Stats
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Stats()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestHotCache_EmptyCache(t *testing.T) {
	cache := NewDefaultHotCache()

	// Operations on empty cache should not panic
	cache.Get("nonexistent")
	cache.Contains("nonexistent")
	cache.Remove("nonexistent")
	cache.EvictPercent(0.5)
	cache.Clear()
	cache.Keys()
	cache.LRUOrder()
	cache.Stats()
}

func TestHotCache_SingleEntry(t *testing.T) {
	cache := NewDefaultHotCache()

	entry := makeTestEntry("single", 1000)
	cache.Add("single", entry)

	if cache.Stats().EntryCount != 1 {
		t.Error("expected 1 entry")
	}

	cache.EvictPercent(1.0)

	if cache.Stats().EntryCount != 0 {
		t.Error("expected 0 entries after full eviction")
	}
}

// =============================================================================
// Async Eviction Tests (PF.5.10)
// =============================================================================

func TestHotCache_AsyncEvictionMode(t *testing.T) {
	t.Run("async eviction triggered on add", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       5000,
			AsyncEviction: true,
		})
		defer cache.Close()

		// Add entries to trigger eviction
		for i := 0; i < 20; i++ {
			cache.Add("entry-"+string(rune('a'+(i%26))), makeTestEntry("entry-"+string(rune('a'+(i%26))), 1000))
		}

		// Wait for async eviction to process
		time.Sleep(100 * time.Millisecond)

		// Should be at or under max size
		if cache.Size() > cache.MaxSize() {
			t.Errorf("expected size <= %d, got %d", cache.MaxSize(), cache.Size())
		}
	})

	t.Run("async eviction with callback", func(t *testing.T) {
		var evictedIDs []string
		var mu sync.Mutex

		cache := NewHotCache(HotCacheConfig{
			MaxSize:       3000,
			AsyncEviction: true,
			OnEvict: func(id string, entry *ContentEntry) {
				mu.Lock()
				evictedIDs = append(evictedIDs, id)
				mu.Unlock()
			},
		})
		defer cache.Close()

		// Add entries to trigger eviction
		for i := 0; i < 10; i++ {
			cache.Add("entry-"+string(rune('a'+i)), makeTestEntry("entry-"+string(rune('a'+i)), 1000))
		}

		// Wait for async eviction
		time.Sleep(100 * time.Millisecond)

		mu.Lock()
		evictCount := len(evictedIDs)
		mu.Unlock()

		if evictCount == 0 {
			t.Error("expected some entries to be evicted")
		}
	})

	t.Run("async eviction concurrent safety", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       10000,
			AsyncEviction: true,
		})
		defer cache.Close()

		var wg sync.WaitGroup

		// Concurrent adds
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				cache.Add("entry-"+string(rune(id)), makeTestEntry("entry-"+string(rune(id)), 500))
			}(i)
		}

		wg.Wait()

		// Wait for async eviction
		time.Sleep(100 * time.Millisecond)

		// Should not exceed max size
		if cache.Size() > cache.MaxSize() {
			t.Errorf("cache size %d exceeds max %d", cache.Size(), cache.MaxSize())
		}
	})

	t.Run("sync mode still works", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       3000,
			AsyncEviction: false,
		})

		// Add entries
		for i := 0; i < 10; i++ {
			cache.Add("entry-"+string(rune('a'+i)), makeTestEntry("entry-"+string(rune('a'+i)), 1000))
		}

		// Should immediately be at or under max size
		if cache.Size() > cache.MaxSize() {
			t.Errorf("expected size <= %d, got %d", cache.MaxSize(), cache.Size())
		}
	})
}

func TestHotCache_EvictionCallback(t *testing.T) {
	t.Run("callback invoked on eviction", func(t *testing.T) {
		var evictedIDs []string
		var mu sync.Mutex

		cache := NewHotCache(HotCacheConfig{
			MaxSize: 3000,
			OnEvict: func(id string, entry *ContentEntry) {
				mu.Lock()
				evictedIDs = append(evictedIDs, id)
				mu.Unlock()
			},
		})

		// Add entries to trigger eviction
		for i := 0; i < 10; i++ {
			cache.Add("entry-"+string(rune('a'+i)), makeTestEntry("entry-"+string(rune('a'+i)), 1000))
		}

		mu.Lock()
		evictCount := len(evictedIDs)
		mu.Unlock()

		if evictCount == 0 {
			t.Error("expected eviction callback to be invoked")
		}
	})

	t.Run("callback on Remove", func(t *testing.T) {
		var evictedID string

		cache := NewHotCache(HotCacheConfig{
			MaxSize: 100000,
			OnEvict: func(id string, entry *ContentEntry) {
				evictedID = id
			},
		})

		cache.Add("test-entry", makeTestEntry("test-entry", 1000))
		cache.Remove("test-entry")

		if evictedID != "test-entry" {
			t.Errorf("expected eviction callback for 'test-entry', got '%s'", evictedID)
		}
	})

	t.Run("callback on Clear", func(t *testing.T) {
		var evictedIDs []string

		cache := NewHotCache(HotCacheConfig{
			MaxSize: 100000,
			OnEvict: func(id string, entry *ContentEntry) {
				evictedIDs = append(evictedIDs, id)
			},
		})

		cache.Add("entry-1", makeTestEntry("entry-1", 1000))
		cache.Add("entry-2", makeTestEntry("entry-2", 1000))
		cache.Add("entry-3", makeTestEntry("entry-3", 1000))

		cache.Clear()

		if len(evictedIDs) != 3 {
			t.Errorf("expected 3 eviction callbacks, got %d", len(evictedIDs))
		}
	})

	t.Run("SetEvictionCallback updates callback", func(t *testing.T) {
		cache := NewDefaultHotCache()

		var called bool
		cache.SetEvictionCallback(func(id string, entry *ContentEntry) {
			called = true
		})

		cache.Add("test", makeTestEntry("test", 1000))
		cache.Remove("test")

		if !called {
			t.Error("expected new callback to be invoked")
		}
	})
}

func TestHotCache_EvictionBatchSize(t *testing.T) {
	t.Run("custom batch size", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:           100000,
			EvictionBatchSize: 50,
		})

		if cache.EvictionBatchSize() != 50 {
			t.Errorf("expected batch size 50, got %d", cache.EvictionBatchSize())
		}
	})

	t.Run("SetEvictionBatchSize updates size", func(t *testing.T) {
		cache := NewDefaultHotCache()

		cache.SetEvictionBatchSize(200)

		if cache.EvictionBatchSize() != 200 {
			t.Errorf("expected batch size 200, got %d", cache.EvictionBatchSize())
		}
	})

	t.Run("invalid batch size uses default", func(t *testing.T) {
		cache := NewDefaultHotCache()

		cache.SetEvictionBatchSize(-1)

		if cache.EvictionBatchSize() != DefaultEvictionBatchSize {
			t.Errorf("expected default batch size, got %d", cache.EvictionBatchSize())
		}
	})
}

func TestHotCache_Close(t *testing.T) {
	t.Run("close stops async eviction", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       5000,
			AsyncEviction: true,
		})

		// Add some entries
		for i := 0; i < 5; i++ {
			cache.Add("entry-"+string(rune('a'+i)), makeTestEntry("entry-"+string(rune('a'+i)), 500))
		}

		// Close should not panic
		cache.Close()

		// Wait briefly
		time.Sleep(50 * time.Millisecond)

		// Should not panic or deadlock
	})
}
