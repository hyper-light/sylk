package purevfs

import (
	"container/list"
	"io/fs"
	"os"
	"sync"
	"time"
)

// hostReadCache is the process-wide, content-addressable read cache for
// hostNamespace reads. Toolchain files (Python interpreter, shared libs,
// stdlib Python modules, node_modules binaries, gcc headers, etc.) are
// read hundreds of times across a session as commands spawn and relink;
// without a cache, every read is a trip through the FUSE daemon to the
// OS read path on the real disk. With a cache, repeated reads of the
// same immutable file are served from memory.
//
// Correctness model:
//   - Keyed by (absolute path, size, modtime). On lookup, we re-stat to
//     detect changes — a file whose size or mtime differs from the cached
//     entry is evicted and re-read. The stat syscall is ~3 orders of
//     magnitude cheaper than a multi-megabyte read, so this trade is a
//     clear win for toolchain workloads.
//   - The cache is *never* applied to writable namespaces. Only hostNamespace
//     routes reads through it, and hostNamespace is read-only by contract.
//   - LRU eviction bounds memory use. Default 128 MiB; configurable via
//     SetHostReadCacheBudget. When the budget would be exceeded, the oldest
//     entries are evicted until there is room.
//
// Concurrency: every cache op takes a single mutex. The critical section
// is bounded (map lookup + list move + optional removal), so contention is
// low even under heavy parallel test workers. The backing-file read itself
// happens outside the lock — so two goroutines racing on a cold file path
// do a redundant OS read but both get correct bytes.
type hostReadCache struct {
	mu       sync.Mutex
	entries  map[string]*hostReadCacheEntry
	order    *list.List // MRU at front; eviction from back
	curBytes int64
	maxBytes int64
}

type hostReadCacheEntry struct {
	path    string
	size    int64
	modTime time.Time
	content []byte
	node    *list.Element // position in the LRU list
}

// defaultHostReadCacheBudget is the per-process cache ceiling. 128 MiB is
// enough to hold a typical Python runtime (~30 MiB across interpreter +
// stdlib) plus a node_modules hotset, with headroom for other toolchains.
// Can be tuned via SetHostReadCacheBudget.
const defaultHostReadCacheBudget = 128 * 1024 * 1024

var defaultHostCache = newHostReadCache(defaultHostReadCacheBudget)

func newHostReadCache(maxBytes int64) *hostReadCache {
	return &hostReadCache{
		entries:  make(map[string]*hostReadCacheEntry),
		order:    list.New(),
		maxBytes: maxBytes,
	}
}

// SetHostReadCacheBudget resets the default cache ceiling. Intended for
// callers that profile a specific workload and need a larger or smaller
// working set; the default is chosen for a typical agent session.
func SetHostReadCacheBudget(bytes int64) {
	defaultHostCache.setBudget(bytes)
}

// HostReadCacheStats returns current cache footprint. Exported for tests
// and diagnostics; not part of the broker's contract with agents.
func HostReadCacheStats() (entries int, bytes int64, maxBytes int64) {
	return defaultHostCache.stats()
}

// ResetHostReadCache clears all entries. Used by tests; not a runtime
// operation — there is no scenario in production where we want to flush
// the cache and pay the disk-read cost for already-loaded toolchain files.
func ResetHostReadCache() {
	defaultHostCache.reset()
}

func (c *hostReadCache) setBudget(bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxBytes = bytes
	c.evictToBudgetLocked()
}

func (c *hostReadCache) stats() (int, int64, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.curBytes, c.maxBytes
}

func (c *hostReadCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*hostReadCacheEntry)
	c.order = list.New()
	c.curBytes = 0
}

// lookup returns cached content if the (path, size, modTime) triple matches
// an existing entry. A stale entry (same path, different size or mtime) is
// evicted and the caller re-reads from disk.
func (c *hostReadCache) lookup(path string, size int64, modTime time.Time) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[path]
	if !ok {
		return nil, false
	}
	if entry.size != size || !entry.modTime.Equal(modTime) {
		c.removeLocked(entry)
		return nil, false
	}
	c.order.MoveToFront(entry.node)
	return entry.content, true
}

// store records content for (path, size, modTime). If the resulting footprint
// exceeds maxBytes, older entries are evicted first. If the entry itself is
// larger than maxBytes, it is stored (trusting the caller) but nothing else
// fits alongside — rare enough not to warrant further policy.
func (c *hostReadCache) store(path string, size int64, modTime time.Time, content []byte) {
	if c.maxBytes <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.entries[path]; ok {
		c.removeLocked(existing)
	}
	entry := &hostReadCacheEntry{
		path:    path,
		size:    size,
		modTime: modTime,
		content: content,
	}
	entry.node = c.order.PushFront(entry)
	c.entries[path] = entry
	c.curBytes += int64(len(content))
	c.evictToBudgetLocked()
}

func (c *hostReadCache) removeLocked(entry *hostReadCacheEntry) {
	c.order.Remove(entry.node)
	delete(c.entries, entry.path)
	c.curBytes -= int64(len(entry.content))
}

func (c *hostReadCache) evictToBudgetLocked() {
	for c.curBytes > c.maxBytes {
		back := c.order.Back()
		if back == nil {
			return
		}
		entry := back.Value.(*hostReadCacheEntry)
		c.removeLocked(entry)
	}
}

// cachedReadFile is the read path hostNamespace routes through. It checks
// the cache, re-stats to detect staleness, and falls back to the raw
// os.ReadFile on miss — storing the result for subsequent calls. The stat
// call deliberately precedes the cache lookup so an invalidation detects
// change-since-last-read even when the cached entry is otherwise valid.
func cachedReadFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		// Directories don't flow through ReadFile in the host path; defensive
		// early return so callers that accidentally ask for a dir see the
		// correct error rather than an empty cache entry.
		return nil, fs.ErrInvalid
	}
	if content, ok := defaultHostCache.lookup(path, info.Size(), info.ModTime()); ok {
		return content, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defaultHostCache.store(path, info.Size(), info.ModTime(), content)
	return content, nil
}
