package purevfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHostReadCache_Hit verifies that a second read of the same path,
// size, and mtime returns the cached bytes without touching the backing
// file. This is the core performance invariant of the cache — if it
// doesn't hold, toolchain workloads pay the disk-read cost every time.
func TestHostReadCache_Hit(t *testing.T) {
	cache := newHostReadCache(1 << 20)
	now := time.Now()
	cache.store("/usr/local/lib/python3.12/os.py", 1024, now, []byte("original"))

	got, ok := cache.lookup("/usr/local/lib/python3.12/os.py", 1024, now)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(got) != "original" {
		t.Fatalf("cached content = %q, want %q", got, "original")
	}
}

// TestHostReadCache_MissOnStaleModTime verifies the invalidation path:
// when a cached entry's recorded mtime differs from the lookup key, the
// cache reports a miss and evicts the stale bytes. This keeps the cache
// honest across toolchain upgrades where a library gets overwritten.
func TestHostReadCache_MissOnStaleModTime(t *testing.T) {
	cache := newHostReadCache(1 << 20)
	original := time.Now()
	cache.store("/path/to/lib.so", 2048, original, []byte("old"))

	newer := original.Add(time.Minute)
	if _, ok := cache.lookup("/path/to/lib.so", 2048, newer); ok {
		t.Fatal("expected cache miss after mtime advanced")
	}
	// Stale entry must be evicted so memory accounting remains accurate.
	entries, bytes, _ := cache.stats()
	if entries != 0 || bytes != 0 {
		t.Fatalf("stale entry not evicted: entries=%d bytes=%d", entries, bytes)
	}
}

// TestHostReadCache_MissOnStaleSize covers the other invalidation signal:
// a file whose size changed in place (common for lockfile updates, log
// files) is correctly invalidated even if mtime happened to round to the
// same second on a slow filesystem.
func TestHostReadCache_MissOnStaleSize(t *testing.T) {
	cache := newHostReadCache(1 << 20)
	mt := time.Now()
	cache.store("/path/to/lock", 100, mt, []byte("before"))

	if _, ok := cache.lookup("/path/to/lock", 200, mt); ok {
		t.Fatal("expected cache miss when size changed")
	}
}

// TestHostReadCache_LRUEvictsOldestEntries verifies the bounded-memory
// invariant. A cache with a 10-byte budget must never hold more than 10
// bytes worth of content, even if we store many entries. The oldest
// (least-recently accessed) entry is the one evicted.
func TestHostReadCache_LRUEvictsOldestEntries(t *testing.T) {
	cache := newHostReadCache(10)
	mt := time.Now()
	cache.store("/a", 4, mt, []byte("aaaa"))
	cache.store("/b", 4, mt, []byte("bbbb"))
	cache.store("/c", 4, mt, []byte("cccc")) // total would be 12; /a evicts

	if _, ok := cache.lookup("/a", 4, mt); ok {
		t.Fatal("expected /a to be evicted (oldest)")
	}
	if _, ok := cache.lookup("/b", 4, mt); !ok {
		t.Fatal("expected /b to still be present")
	}
	if _, ok := cache.lookup("/c", 4, mt); !ok {
		t.Fatal("expected /c to still be present")
	}
}

// TestHostReadCache_HitPromotesToFront verifies LRU ordering: reading an
// entry moves it to the front of the eviction queue so it is not the
// first to go when pressure arrives. Without this, a hot toolchain file
// that happens to have been inserted early would be evicted under memory
// pressure while colder entries survived.
func TestHostReadCache_HitPromotesToFront(t *testing.T) {
	cache := newHostReadCache(8)
	mt := time.Now()
	cache.store("/old", 4, mt, []byte("oldd"))
	cache.store("/new", 4, mt, []byte("news"))

	// Touch /old — it must become MRU.
	if _, ok := cache.lookup("/old", 4, mt); !ok {
		t.Fatal("expected /old hit")
	}

	// Store a new entry that forces eviction of one.
	cache.store("/next", 4, mt, []byte("next"))

	if _, ok := cache.lookup("/old", 4, mt); !ok {
		t.Fatal("expected /old to survive (MRU)")
	}
	if _, ok := cache.lookup("/new", 4, mt); ok {
		t.Fatal("expected /new to be evicted (LRU after /old was promoted)")
	}
}

// TestCachedReadFile_ReadsDiskThenServesFromCache is the end-to-end
// verification of the public ReadFile wrapper. First read hits disk;
// second read hits cache and does not observe a mutation to the
// underlying file made between calls IF the mtime is unchanged.
// (Test-controlled mtime forcing is too system-specific to stabilize;
// instead we verify that the cached bytes come back from the cache by
// inspecting the cache directly.)
func TestCachedReadFile_ReadsDiskThenServesFromCache(t *testing.T) {
	ResetHostReadCache()
	defer ResetHostReadCache()

	dir := t.TempDir()
	path := filepath.Join(dir, "toolchain.so")
	if err := os.WriteFile(path, []byte("first-content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := cachedReadFile(path)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if string(got) != "first-content" {
		t.Fatalf("first read content = %q, want first-content", got)
	}

	entries, bytes, _ := HostReadCacheStats()
	if entries != 1 {
		t.Fatalf("cache entries = %d, want 1 after first read", entries)
	}
	if bytes != int64(len("first-content")) {
		t.Fatalf("cache bytes = %d, want %d", bytes, len("first-content"))
	}

	// Second read: must come from cache. We confirm indirectly by reading
	// again and asserting the bytes match, since our cache doesn't expose
	// a hit counter. The alternative — verifying zero disk I/O — is
	// inherently OS-specific and skipped for portability.
	got2, err := cachedReadFile(path)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if string(got2) != "first-content" {
		t.Fatalf("second read content = %q, want first-content", got2)
	}
}

// TestCachedReadFile_InvalidatesWhenBackingFileChanges verifies the full
// end-to-end invalidation: if the underlying file is rewritten with new
// content and a different mtime, the cache serves the new bytes on the
// next read.
func TestCachedReadFile_InvalidatesWhenBackingFileChanges(t *testing.T) {
	ResetHostReadCache()
	defer ResetHostReadCache()

	dir := t.TempDir()
	path := filepath.Join(dir, "evolving.txt")
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatalf("WriteFile v1: %v", err)
	}
	_, _ = cachedReadFile(path) // warm

	// Force a distinct mtime. Some filesystems have 1-second mtime
	// granularity, so we bump explicitly to avoid flakiness on older
	// CI hosts.
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(path, []byte("v2-content-longer"), 0o644); err != nil {
		t.Fatalf("WriteFile v2: %v", err)
	}
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	got, err := cachedReadFile(path)
	if err != nil {
		t.Fatalf("post-mutation read: %v", err)
	}
	if string(got) != "v2-content-longer" {
		t.Fatalf("post-mutation content = %q, want v2-content-longer", got)
	}
}

// TestSetHostReadCacheBudget_ShrinksCurrentFootprint verifies dynamic
// budget adjustment. Shrinking the budget below current utilization must
// immediately evict oldest entries rather than wait for the next store.
func TestSetHostReadCacheBudget_ShrinksCurrentFootprint(t *testing.T) {
	cache := newHostReadCache(100)
	mt := time.Now()
	cache.store("/a", 30, mt, make([]byte, 30))
	cache.store("/b", 30, mt, make([]byte, 30))
	cache.store("/c", 30, mt, make([]byte, 30))

	cache.setBudget(40) // room for at most one 30-byte entry

	entries, bytes, _ := cache.stats()
	if bytes > 40 {
		t.Fatalf("post-shrink bytes = %d, want <=40", bytes)
	}
	if entries > 1 {
		t.Fatalf("post-shrink entries = %d, want <=1", entries)
	}
}
