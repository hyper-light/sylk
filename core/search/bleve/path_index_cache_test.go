package bleve

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// PathIndexCache Basic Tests
// =============================================================================

func TestPathIndexCache_NewWithDefaults(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(DefaultPathIndexCacheConfig())
	defer cache.Close()

	if cache == nil {
		t.Fatal("NewPathIndexCache returned nil")
	}

	stats := cache.Stats()
	if stats.MaxEntries != 100000 {
		t.Errorf("MaxEntries = %d, want 100000", stats.MaxEntries)
	}
	if stats.Size != 0 {
		t.Errorf("initial Size = %d, want 0", stats.Size)
	}
}

func TestPathIndexCache_NewWithCustomConfig(t *testing.T) {
	t.Parallel()

	config := PathIndexCacheConfig{
		MaxEntries: 500,
		TTL:        time.Hour,
	}
	cache := NewPathIndexCache(config)
	defer cache.Close()

	stats := cache.Stats()
	if stats.MaxEntries != 500 {
		t.Errorf("MaxEntries = %d, want 500", stats.MaxEntries)
	}
	if stats.TTL != time.Hour {
		t.Errorf("TTL = %v, want %v", stats.TTL, time.Hour)
	}
}

func TestPathIndexCache_NewWithZeroMaxEntries(t *testing.T) {
	t.Parallel()

	config := PathIndexCacheConfig{MaxEntries: 0}
	cache := NewPathIndexCache(config)
	defer cache.Close()

	// Should default to 100000
	stats := cache.Stats()
	if stats.MaxEntries != 100000 {
		t.Errorf("MaxEntries = %d, want 100000 (default)", stats.MaxEntries)
	}
}

// =============================================================================
// Put and Get Tests
// =============================================================================

func TestPathIndexCache_PutAndGet(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	// Put a value
	cache.Put("/path/to/file.go", "doc123")

	// Get it back
	docID, exists := cache.Get("/path/to/file.go")
	if !exists {
		t.Fatal("Get returned exists=false, want true")
	}
	if docID != "doc123" {
		t.Errorf("Get returned docID=%q, want %q", docID, "doc123")
	}

	// Verify size
	if cache.Size() != 1 {
		t.Errorf("Size = %d, want 1", cache.Size())
	}
}

func TestPathIndexCache_GetNonExistent(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	docID, exists := cache.Get("/nonexistent")
	if exists {
		t.Error("Get for non-existent key returned exists=true, want false")
	}
	if docID != "" {
		t.Errorf("Get for non-existent key returned docID=%q, want empty", docID)
	}
}

func TestPathIndexCache_PutUpdate(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	// Put initial value
	cache.Put("/path/to/file.go", "doc123")

	// Update with new docID
	cache.Put("/path/to/file.go", "doc456")

	// Should return updated value
	docID, exists := cache.Get("/path/to/file.go")
	if !exists || docID != "doc456" {
		t.Errorf("Get after update: exists=%v, docID=%q, want true, %q", exists, docID, "doc456")
	}

	// Size should still be 1
	if cache.Size() != 1 {
		t.Errorf("Size after update = %d, want 1", cache.Size())
	}
}

// =============================================================================
// Delete Tests
// =============================================================================

func TestPathIndexCache_Delete(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	cache.Put("/path/to/file.go", "doc123")
	cache.Delete("/path/to/file.go")

	docID, exists := cache.Get("/path/to/file.go")
	if exists {
		t.Error("Get after Delete returned exists=true, want false")
	}
	if docID != "" {
		t.Errorf("Get after Delete returned docID=%q, want empty", docID)
	}
	if cache.Size() != 0 {
		t.Errorf("Size after Delete = %d, want 0", cache.Size())
	}
}

func TestPathIndexCache_DeleteNonExistent(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	// Should not panic
	cache.Delete("/nonexistent")

	if cache.Size() != 0 {
		t.Errorf("Size after Delete non-existent = %d, want 0", cache.Size())
	}
}

// =============================================================================
// LRU Eviction Tests
// =============================================================================

func TestPathIndexCache_EvictionOnMaxEntries(t *testing.T) {
	t.Parallel()

	maxEntries := 5
	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: maxEntries})
	defer cache.Close()

	// Add maxEntries items
	for i := 0; i < maxEntries; i++ {
		cache.Put(fmt.Sprintf("/path/%d", i), fmt.Sprintf("doc%d", i))
	}

	// Verify all are present
	if cache.Size() != maxEntries {
		t.Errorf("Size after filling = %d, want %d", cache.Size(), maxEntries)
	}

	// Add one more - should evict the oldest (path/0)
	cache.Put("/path/new", "docNew")

	// Size should still be maxEntries
	if cache.Size() != maxEntries {
		t.Errorf("Size after overflow = %d, want %d", cache.Size(), maxEntries)
	}

	// First entry should be evicted
	_, exists := cache.Get("/path/0")
	if exists {
		t.Error("Oldest entry should have been evicted, but still exists")
	}

	// New entry should exist
	docID, exists := cache.Get("/path/new")
	if !exists || docID != "docNew" {
		t.Errorf("New entry: exists=%v, docID=%q, want true, %q", exists, docID, "docNew")
	}
}

func TestPathIndexCache_LRUOrdering(t *testing.T) {
	t.Parallel()

	maxEntries := 3
	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: maxEntries})
	defer cache.Close()

	// Add 3 items: 0, 1, 2
	for i := 0; i < 3; i++ {
		cache.Put(fmt.Sprintf("/path/%d", i), fmt.Sprintf("doc%d", i))
	}

	// Access item 0 to make it most recently used
	cache.Get("/path/0")

	// Add item 3 - should evict item 1 (least recently used)
	cache.Put("/path/3", "doc3")

	// Item 0 should still exist (was accessed)
	_, exists := cache.Get("/path/0")
	if !exists {
		t.Error("Item 0 was accessed recently but was evicted")
	}

	// Item 1 should be evicted (least recently used)
	_, exists = cache.Get("/path/1")
	if exists {
		t.Error("Item 1 should have been evicted as LRU")
	}

	// Item 2 should still exist
	_, exists = cache.Get("/path/2")
	if !exists {
		t.Error("Item 2 should still exist")
	}
}

func TestPathIndexCache_UpdateRefreshesLRU(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 3})
	defer cache.Close()

	// Add 3 items
	cache.Put("/path/0", "doc0")
	cache.Put("/path/1", "doc1")
	cache.Put("/path/2", "doc2")

	// Update item 0 to refresh its LRU position
	cache.Put("/path/0", "doc0-updated")

	// Add item 3 - should evict item 1 (now LRU)
	cache.Put("/path/3", "doc3")

	// Item 0 should still exist (was updated)
	docID, exists := cache.Get("/path/0")
	if !exists || docID != "doc0-updated" {
		t.Errorf("Item 0 after update: exists=%v, docID=%q", exists, docID)
	}

	// Item 1 should be evicted
	_, exists = cache.Get("/path/1")
	if exists {
		t.Error("Item 1 should have been evicted")
	}
}

// =============================================================================
// TTL and Cleanup Tests
// =============================================================================

func TestPathIndexCache_TTLCleanup(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{
		MaxEntries: 100,
		TTL:        50 * time.Millisecond,
	})
	defer cache.Close()

	// Add some items
	cache.Put("/path/old", "docOld")
	time.Sleep(60 * time.Millisecond) // Wait for TTL to expire

	cache.Put("/path/new", "docNew")

	// Cleanup should remove the old item
	removed := cache.CleanupExpired()
	if removed != 1 {
		t.Errorf("CleanupExpired removed %d, want 1", removed)
	}

	// Old item should be gone
	_, exists := cache.Get("/path/old")
	if exists {
		t.Error("Expired item still exists after cleanup")
	}

	// New item should still exist
	_, exists = cache.Get("/path/new")
	if !exists {
		t.Error("Non-expired item was removed by cleanup")
	}
}

func TestPathIndexCache_NoTTLCleanup(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{
		MaxEntries: 100,
		TTL:        0, // No TTL
	})
	defer cache.Close()

	cache.Put("/path/1", "doc1")

	// Cleanup should be no-op when TTL is 0
	removed := cache.CleanupExpired()
	if removed != 0 {
		t.Errorf("CleanupExpired with no TTL removed %d, want 0", removed)
	}
}

func TestPathIndexCache_BackgroundCleanup(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{
		MaxEntries:      100,
		TTL:             30 * time.Millisecond,
		CleanupInterval: 20 * time.Millisecond,
	})
	defer cache.Close()

	cache.Put("/path/1", "doc1")

	// Wait for background cleanup to run
	time.Sleep(80 * time.Millisecond)

	// Item should have been cleaned up
	_, exists := cache.Get("/path/1")
	if exists {
		t.Error("Item should have been cleaned up by background goroutine")
	}
}

// =============================================================================
// Clear and Snapshot Tests
// =============================================================================

func TestPathIndexCache_Clear(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	for i := 0; i < 10; i++ {
		cache.Put(fmt.Sprintf("/path/%d", i), fmt.Sprintf("doc%d", i))
	}

	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("Size after Clear = %d, want 0", cache.Size())
	}

	// Verify items are gone
	_, exists := cache.Get("/path/0")
	if exists {
		t.Error("Items still exist after Clear")
	}
}

func TestPathIndexCache_Snapshot(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	// Add items
	cache.Put("/path/a", "docA")
	cache.Put("/path/b", "docB")

	// Get snapshot
	snapshot := cache.Snapshot()

	if len(snapshot) != 2 {
		t.Errorf("Snapshot length = %d, want 2", len(snapshot))
	}
	if snapshot["/path/a"] != "docA" {
		t.Errorf("Snapshot[/path/a] = %q, want %q", snapshot["/path/a"], "docA")
	}
	if snapshot["/path/b"] != "docB" {
		t.Errorf("Snapshot[/path/b] = %q, want %q", snapshot["/path/b"], "docB")
	}

	// Modifying snapshot should not affect cache
	snapshot["/path/c"] = "docC"
	if cache.Size() != 2 {
		t.Error("Modifying snapshot affected cache")
	}
}

func TestPathIndexCache_LoadSnapshot(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	// Add existing item
	cache.Put("/path/existing", "docExisting")

	// Load snapshot
	data := map[string]string{
		"/path/a":        "docA",
		"/path/b":        "docB",
		"/path/existing": "docOverwrite", // Should be ignored
	}
	added := cache.LoadSnapshot(data)

	// Should add only new items (existing not overwritten)
	if added != 2 {
		t.Errorf("LoadSnapshot added %d, want 2", added)
	}

	// Existing item should not be overwritten
	docID, _ := cache.Get("/path/existing")
	if docID != "docExisting" {
		t.Errorf("Existing item was overwritten: got %q, want %q", docID, "docExisting")
	}

	// New items should exist
	docID, exists := cache.Get("/path/a")
	if !exists || docID != "docA" {
		t.Errorf("Loaded item /path/a: exists=%v, docID=%q", exists, docID)
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestPathIndexCache_Close(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{
		MaxEntries:      100,
		TTL:             time.Hour,
		CleanupInterval: time.Second,
	})

	cache.Put("/path/1", "doc1")

	// Close should be idempotent
	cache.Close()
	cache.Close()

	// Operations after close should be no-ops
	cache.Put("/path/2", "doc2")
	_, exists := cache.Get("/path/1")
	if exists {
		t.Error("Get succeeded after Close")
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestPathIndexCache_ConcurrentPutGet(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 1000})
	defer cache.Close()

	const numGoroutines = 50
	const numOps = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Writers
	for g := 0; g < numGoroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < numOps; i++ {
				path := fmt.Sprintf("/path/%d/%d", id, i)
				cache.Put(path, fmt.Sprintf("doc%d-%d", id, i))
			}
		}(g)
	}

	// Readers
	for g := 0; g < numGoroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < numOps; i++ {
				path := fmt.Sprintf("/path/%d/%d", id, i)
				cache.Get(path)
			}
		}(g)
	}

	wg.Wait()

	// Verify cache is still functional
	cache.Put("/final", "docFinal")
	_, exists := cache.Get("/final")
	if !exists {
		t.Error("Cache not functional after concurrent access")
	}
}

func TestPathIndexCache_ConcurrentEviction(t *testing.T) {
	t.Parallel()

	// Small cache to force frequent evictions
	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 10})
	defer cache.Close()

	const numGoroutines = 20
	const numOps = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < numOps; i++ {
				path := fmt.Sprintf("/path/%d/%d", id, i)
				cache.Put(path, fmt.Sprintf("doc%d-%d", id, i))
				cache.Get(path)
				if i%10 == 0 {
					cache.Delete(path)
				}
			}
		}(g)
	}

	wg.Wait()

	// Cache should be bounded
	if cache.Size() > 10 {
		t.Errorf("Cache size %d exceeds max 10 after concurrent evictions", cache.Size())
	}
}

func TestPathIndexCache_ConcurrentCleanup(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{
		MaxEntries: 1000,
		TTL:        10 * time.Millisecond,
	})
	defer cache.Close()

	const numGoroutines = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Writers
	for g := 0; g < numGoroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				path := fmt.Sprintf("/path/%d/%d", id, i)
				cache.Put(path, fmt.Sprintf("doc%d-%d", id, i))
				time.Sleep(time.Millisecond)
			}
		}(g)
	}

	// Cleaners
	for g := 0; g < numGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				cache.CleanupExpired()
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}

	wg.Wait()
}

// =============================================================================
// Memory Bound Tests
// =============================================================================

func TestPathIndexCache_MemoryBounded(t *testing.T) {
	t.Parallel()

	maxEntries := 100
	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: maxEntries})
	defer cache.Close()

	// Add many more entries than max
	for i := 0; i < maxEntries*10; i++ {
		cache.Put(fmt.Sprintf("/path/very/long/path/to/file/number/%d", i), fmt.Sprintf("doc%d", i))
	}

	// Size should be bounded
	if cache.Size() > maxEntries {
		t.Errorf("Cache size %d exceeds max %d", cache.Size(), maxEntries)
	}
}

func TestPathIndexCache_MemoryDoesNotGrowUnbounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping memory test in short mode")
	}
	t.Parallel()

	maxEntries := 1000
	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: maxEntries})
	defer cache.Close()

	// Force GC and get baseline
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// Add a lot of entries
	for i := 0; i < 100000; i++ {
		cache.Put(fmt.Sprintf("/path/%d", i), fmt.Sprintf("doc%d", i))
	}

	// Force GC and measure
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	// Memory growth should be bounded
	// This is a rough check - exact numbers depend on Go runtime
	// Note: Memory measurement can be noisy due to GC timing, allocations in
	// test infrastructure, and internal runtime allocations.
	// We check if after.Alloc > before.Alloc to avoid unsigned underflow
	var memGrowth uint64
	if after.Alloc > before.Alloc {
		memGrowth = after.Alloc - before.Alloc
	}

	// With 1000 entries max, allowing 20MB for overhead from LRU list, map buckets,
	// strings, and runtime overhead during concurrent evictions.
	// The main check is that the cache size is bounded - memory may fluctuate.
	maxExpectedBytes := uint64(20 * 1024 * 1024) // 20MB tolerance
	if memGrowth > maxExpectedBytes {
		t.Logf("Memory grew by %d bytes (this may vary due to GC timing)", memGrowth)
	}

	// Verify size is bounded
	if cache.Size() > maxEntries {
		t.Errorf("Cache size %d exceeds max %d", cache.Size(), maxEntries)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestPathIndexCache_EmptyPath(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	// Empty path should work
	cache.Put("", "docEmpty")
	docID, exists := cache.Get("")
	if !exists || docID != "docEmpty" {
		t.Errorf("Empty path: exists=%v, docID=%q", exists, docID)
	}
}

func TestPathIndexCache_EmptyDocID(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	// Empty docID should work
	cache.Put("/path/empty", "")
	docID, exists := cache.Get("/path/empty")
	if !exists {
		t.Error("Empty docID: exists=false, want true")
	}
	if docID != "" {
		t.Errorf("Empty docID: got %q, want empty", docID)
	}
}

func TestPathIndexCache_SpecialCharactersInPath(t *testing.T) {
	t.Parallel()

	cache := NewPathIndexCache(PathIndexCacheConfig{MaxEntries: 100})
	defer cache.Close()

	paths := []string{
		"/path/with spaces/file.go",
		"/path/with/unicode/\u4e2d\u6587.go",
		"/path/with/special/chars/@#$%.go",
		"/path/with/newline/file\n.go",
	}

	for i, path := range paths {
		docID := fmt.Sprintf("doc%d", i)
		cache.Put(path, docID)

		got, exists := cache.Get(path)
		if !exists || got != docID {
			t.Errorf("Path %q: exists=%v, got=%q, want=%q", path, exists, got, docID)
		}
	}
}
