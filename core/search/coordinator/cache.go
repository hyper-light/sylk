package coordinator

import (
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// DefaultCacheTTL is the default time-to-live for cache entries.
const DefaultCacheTTL = 5 * time.Minute

// DefaultCacheMaxSize is the default maximum number of entries in the cache.
const DefaultCacheMaxSize = 1000

// CacheConfig configures the SearchCache behavior.
type CacheConfig struct {
	TTL     time.Duration
	MaxSize int
}

// DefaultCacheConfig returns the default cache configuration.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		TTL:     DefaultCacheTTL,
		MaxSize: DefaultCacheMaxSize,
	}
}

// cacheEntry represents a cached search result with metadata.
type cacheEntry struct {
	key       string
	result    *search.SearchResult
	expiresAt time.Time
	element   *list.Element
}

// SearchCache provides LRU caching with TTL for search results.
// It is thread-safe and supports invalidation on index updates.
type SearchCache struct {
	mu       sync.RWMutex
	entries  map[string]*cacheEntry
	lru      *list.List
	ttl      time.Duration
	maxSize  int
	version  uint64
	disabled bool
}

// NewSearchCache creates a new SearchCache with the given configuration.
func NewSearchCache(cfg CacheConfig) *SearchCache {
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultCacheTTL
	}
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = DefaultCacheMaxSize
	}
	return &SearchCache{
		entries: make(map[string]*cacheEntry),
		lru:     list.New(),
		ttl:     cfg.TTL,
		maxSize: cfg.MaxSize,
	}
}

// NewSearchCacheWithDefaults creates a SearchCache with default settings.
func NewSearchCacheWithDefaults() *SearchCache {
	return NewSearchCache(DefaultCacheConfig())
}

// Get retrieves a cached search result by key with single-lock LRU promotion.
// Returns nil and false if the entry is not found or expired.
// This uses a single write lock for the entire operation (check + promote)
// to avoid the overhead of two separate lock acquisitions (PF.3.7).
func (c *SearchCache) Get(key string) (*search.SearchResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.disabled {
		return nil, false
	}

	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}

	// Check TTL
	if c.isExpired(entry) {
		c.removeEntryLocked(key)
		return nil, false
	}

	// Promote to front (LRU) while holding lock
	c.promoteEntryLocked(entry)

	return entry.result, true
}

// isExpired checks if a cache entry has expired.
func (c *SearchCache) isExpired(entry *cacheEntry) bool {
	return time.Now().After(entry.expiresAt)
}

// promoteEntryLocked moves entry to front of LRU list (assumes lock held).
func (c *SearchCache) promoteEntryLocked(entry *cacheEntry) {
	if entry.element != nil {
		c.lru.MoveToFront(entry.element)
	}
}

// removeEntryLocked removes entry from cache (assumes lock held).
func (c *SearchCache) removeEntryLocked(key string) {
	entry, exists := c.entries[key]
	if !exists {
		return
	}
	c.removeLRUElement(entry)
	delete(c.entries, key)
}

// Set stores a search result in the cache.
func (c *SearchCache) Set(key string, result *search.SearchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.disabled {
		return
	}

	c.setLocked(key, result)
}

// setLocked stores a result without acquiring the lock.
func (c *SearchCache) setLocked(key string, result *search.SearchResult) {
	if existing, exists := c.entries[key]; exists {
		c.removeLRUElement(existing)
	}

	c.evictIfNeeded()

	entry := &cacheEntry{
		key:       key,
		result:    result,
		expiresAt: time.Now().Add(c.ttl),
	}
	entry.element = c.lru.PushFront(key)
	c.entries[key] = entry
}

// evictIfNeeded removes the oldest entry if cache is at capacity.
func (c *SearchCache) evictIfNeeded() {
	for len(c.entries) >= c.maxSize && c.lru.Len() > 0 {
		c.evictOldest()
	}
}

// evictOldest removes the least recently used entry.
func (c *SearchCache) evictOldest() {
	oldest := c.lru.Back()
	if oldest == nil {
		return
	}

	key, ok := oldest.Value.(string)
	if !ok {
		c.lru.Remove(oldest)
		return
	}

	delete(c.entries, key)
	c.lru.Remove(oldest)
}

// removeLRUElement removes an entry's element from the LRU list.
func (c *SearchCache) removeLRUElement(entry *cacheEntry) {
	if entry.element != nil {
		c.lru.Remove(entry.element)
		entry.element = nil
	}
}

// delete removes an entry from the cache.
func (c *SearchCache) delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.entries[key]
	if !exists {
		return
	}

	c.removeLRUElement(entry)
	delete(c.entries, key)
}

// Delete removes an entry from the cache by key.
func (c *SearchCache) Delete(key string) {
	c.delete(key)
}

// Invalidate clears all cache entries and increments the version.
// This should be called when the search index is updated.
func (c *SearchCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
	c.lru = list.New()
	c.version++
}

// Clear removes all entries from the cache without incrementing version.
func (c *SearchCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
	c.lru = list.New()
}

// Size returns the current number of entries in the cache.
func (c *SearchCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Version returns the current cache version.
// This increments on each invalidation.
func (c *SearchCache) Version() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// Disable turns off caching. Get returns miss, Set is a no-op.
func (c *SearchCache) Disable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disabled = true
}

// Enable turns on caching.
func (c *SearchCache) Enable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disabled = false
}

// IsEnabled returns whether the cache is enabled.
func (c *SearchCache) IsEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.disabled
}

// GenerateCacheKey creates a unique cache key including pagination parameters.
// The key is deterministic: same inputs always produce the same key.
func GenerateCacheKey(query string, filters map[string]string, limit, offset int) string {
	h := sha256.New()
	h.Write([]byte(query))

	// Sort filter keys for deterministic ordering
	keys := make([]string, 0, len(filters))
	for k := range filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(filters[k]))
	}

	// Include pagination parameters
	binary.Write(h, binary.LittleEndian, int32(limit))
	binary.Write(h, binary.LittleEndian, int32(offset))

	return hex.EncodeToString(h.Sum(nil))
}

// CacheKeyFromRequest builds a cache key from a SearchRequest.
// This is a convenience function that extracts the relevant fields from the request.
func CacheKeyFromRequest(req *search.SearchRequest) string {
	filters := make(map[string]string)
	if req.Type != "" {
		filters["type"] = string(req.Type)
	}
	if req.PathFilter != "" {
		filters["path"] = req.PathFilter
	}
	return GenerateCacheKey(req.Query, filters, req.Limit, req.Offset)
}

// CleanExpired removes all expired entries from the cache.
func (c *SearchCache) CleanExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	now := time.Now()

	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			c.removeLRUElement(entry)
			delete(c.entries, key)
			count++
		}
	}

	return count
}

// Stats returns cache statistics.
type CacheStats struct {
	Size    int
	MaxSize int
	Version uint64
	Enabled bool
}

// Stats returns the current cache statistics.
func (c *SearchCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return CacheStats{
		Size:    len(c.entries),
		MaxSize: c.maxSize,
		Version: c.version,
		Enabled: !c.disabled,
	}
}
