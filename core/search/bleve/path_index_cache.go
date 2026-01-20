// Package bleve provides Bleve index management for the Sylk Document Search System.
package bleve

import (
	"container/list"
	"sync"
	"time"
)

// =============================================================================
// Path Index Cache Configuration
// =============================================================================

// PathIndexCacheConfig holds configuration for the path index cache.
type PathIndexCacheConfig struct {
	// MaxEntries is the maximum number of entries to keep in the cache.
	// When exceeded, least recently used entries are evicted.
	// Default: 100000
	MaxEntries int

	// TTL is the time-to-live for cache entries. Entries older than this
	// are eligible for eviction during cleanup. Zero means no TTL.
	// Default: 0 (no TTL)
	TTL time.Duration

	// CleanupInterval is how often to run background cleanup.
	// Zero disables background cleanup. Default: 0 (disabled)
	CleanupInterval time.Duration
}

// DefaultPathIndexCacheConfig returns sensible defaults for path index cache.
func DefaultPathIndexCacheConfig() PathIndexCacheConfig {
	return PathIndexCacheConfig{
		MaxEntries:      100000,
		TTL:             0,
		CleanupInterval: 0,
	}
}

// =============================================================================
// Path Index Cache Entry
// =============================================================================

// pathCacheEntry stores a cached path-to-docID mapping with metadata.
type pathCacheEntry struct {
	path      string
	docID     string
	createdAt time.Time
	element   *list.Element // Pointer to LRU list element
}

// =============================================================================
// Path Index Cache
// =============================================================================

// PathIndexCache provides a thread-safe LRU cache for path-to-docID mappings.
// It bounds memory growth by evicting least recently used entries when the
// configured maximum is exceeded.
type PathIndexCache struct {
	config   PathIndexCacheConfig
	entries  map[string]*pathCacheEntry
	lruList  *list.List // Front = most recent, Back = least recent
	mu       sync.RWMutex
	closed   bool
	stopCh   chan struct{}
	cleanupWg sync.WaitGroup
}

// NewPathIndexCache creates a new path index cache with the given configuration.
func NewPathIndexCache(config PathIndexCacheConfig) *PathIndexCache {
	if config.MaxEntries <= 0 {
		config.MaxEntries = 100000
	}

	cache := &PathIndexCache{
		config:  config,
		entries: make(map[string]*pathCacheEntry),
		lruList: list.New(),
		stopCh:  make(chan struct{}),
	}

	if config.CleanupInterval > 0 {
		cache.startBackgroundCleanup()
	}

	return cache
}

// =============================================================================
// Cache Operations
// =============================================================================

// Put adds or updates a path-to-docID mapping in the cache.
// If the cache is at capacity, the least recently used entry is evicted.
func (c *PathIndexCache) Put(path, docID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	c.putLocked(path, docID)
}

// putLocked adds or updates an entry. Must be called with lock held.
func (c *PathIndexCache) putLocked(path, docID string) {
	// Update existing entry
	if entry, exists := c.entries[path]; exists {
		entry.docID = docID
		entry.createdAt = time.Now()
		c.lruList.MoveToFront(entry.element)
		return
	}

	// Evict if at capacity
	if len(c.entries) >= c.config.MaxEntries {
		c.evictOldestLocked()
	}

	// Add new entry
	entry := &pathCacheEntry{
		path:      path,
		docID:     docID,
		createdAt: time.Now(),
	}
	entry.element = c.lruList.PushFront(entry)
	c.entries[path] = entry
}

// Get retrieves the docID for a path. Returns empty string and false if not found.
// Accessing an entry moves it to the front of the LRU list.
func (c *PathIndexCache) Get(path string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return "", false
	}

	entry, exists := c.entries[path]
	if !exists {
		return "", false
	}

	// Move to front (most recently used)
	c.lruList.MoveToFront(entry.element)
	return entry.docID, true
}

// Delete removes a path from the cache.
func (c *PathIndexCache) Delete(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}

	c.deleteLocked(path)
}

// deleteLocked removes an entry. Must be called with lock held.
func (c *PathIndexCache) deleteLocked(path string) {
	entry, exists := c.entries[path]
	if !exists {
		return
	}

	c.lruList.Remove(entry.element)
	delete(c.entries, path)
}

// Size returns the number of entries in the cache.
// Returns 0 if the cache is closed.
func (c *PathIndexCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed {
		return 0
	}
	return len(c.entries)
}

// Clear removes all entries from the cache.
func (c *PathIndexCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*pathCacheEntry)
	c.lruList = list.New()
}

// =============================================================================
// Eviction and Cleanup
// =============================================================================

// evictOldestLocked removes the least recently used entry.
// Must be called with lock held.
func (c *PathIndexCache) evictOldestLocked() {
	oldest := c.lruList.Back()
	if oldest == nil {
		return
	}

	entry := oldest.Value.(*pathCacheEntry)
	c.lruList.Remove(oldest)
	delete(c.entries, entry.path)
}

// CleanupExpired removes entries older than the configured TTL.
// Returns the number of entries removed.
// This is a no-op if TTL is not configured.
func (c *PathIndexCache) CleanupExpired() int {
	if c.config.TTL <= 0 {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0
	}

	return c.cleanupExpiredLocked()
}

// cleanupExpiredLocked removes expired entries. Must be called with lock held.
func (c *PathIndexCache) cleanupExpiredLocked() int {
	if c.config.TTL <= 0 {
		return 0
	}

	cutoff := time.Now().Add(-c.config.TTL)
	removed := 0

	// Iterate from back (oldest) to front (newest)
	for elem := c.lruList.Back(); elem != nil; {
		entry := elem.Value.(*pathCacheEntry)
		prev := elem.Prev()

		if entry.createdAt.Before(cutoff) {
			c.lruList.Remove(elem)
			delete(c.entries, entry.path)
			removed++
		}

		elem = prev
	}

	return removed
}

// startBackgroundCleanup starts a goroutine that periodically cleans up expired entries.
func (c *PathIndexCache) startBackgroundCleanup() {
	c.cleanupWg.Add(1)
	go func() {
		defer c.cleanupWg.Done()
		ticker := time.NewTicker(c.config.CleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.CleanupExpired()
			case <-c.stopCh:
				return
			}
		}
	}()
}

// =============================================================================
// Snapshot and Iteration
// =============================================================================

// Snapshot returns a copy of all path-to-docID mappings.
// This is useful for persistence or bulk operations.
func (c *PathIndexCache) Snapshot() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := make(map[string]string, len(c.entries))
	for path, entry := range c.entries {
		snapshot[path] = entry.docID
	}
	return snapshot
}

// LoadSnapshot loads a map of path-to-docID mappings into the cache.
// Existing entries are preserved; new entries are added.
// Returns the number of entries added.
func (c *PathIndexCache) LoadSnapshot(data map[string]string) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return 0
	}

	added := 0
	for path, docID := range data {
		if _, exists := c.entries[path]; !exists {
			c.putLocked(path, docID)
			added++
		}
	}
	return added
}

// =============================================================================
// Lifecycle
// =============================================================================

// Close stops background cleanup and releases resources.
func (c *PathIndexCache) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.stopCh)
	c.mu.Unlock()

	// Wait for background cleanup to finish
	c.cleanupWg.Wait()
}

// Stats returns cache statistics.
type PathIndexCacheStats struct {
	Size       int
	MaxEntries int
	TTL        time.Duration
}

// Stats returns current cache statistics.
func (c *PathIndexCache) Stats() PathIndexCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return PathIndexCacheStats{
		Size:       len(c.entries),
		MaxEntries: c.config.MaxEntries,
		TTL:        c.config.TTL,
	}
}
