// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.5.1: HotCache - Tier 0 in-memory cache with LRU eviction.
package context

import (
	"container/list"
	"sync"
	"sync/atomic"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultHotCacheMaxSize is the default max size in bytes (32MB).
const DefaultHotCacheMaxSize = 32 * 1024 * 1024

// DefaultEvictionBatchSize is the default number of entries to evict per batch.
const DefaultEvictionBatchSize = 100

// MaxEvictionsPerAdd is the maximum number of entries to evict per Add() call.
// This bounds latency spikes during hot-path operations (W3M.10 fix).
const MaxEvictionsPerAdd = 10

// HotCacheName is the name used for EvictableCache identification.
const HotCacheName = "adaptive-hot-cache"

// EvictionCallback is called when an entry is evicted from the cache.
// The callback is invoked outside the lock, so it's safe to perform
// potentially expensive cleanup operations.
type EvictionCallback func(id string, entry *ContentEntry)

// =============================================================================
// LRU Entry
// =============================================================================

// lruEntry holds a cache entry with LRU tracking metadata.
type lruEntry struct {
	id      string
	entry   *ContentEntry
	size    int64
	element *list.Element
}

// =============================================================================
// Hot Cache
// =============================================================================

// HotCache provides Tier 0 (<1ms) in-memory caching for frequently accessed content.
// Implements resources.EvictableCache interface for PressureController integration.
//
// Eviction is performed in batches to minimize lock contention. Eviction callbacks
// are invoked outside the lock to allow expensive cleanup operations.
//
// Async eviction mode (default) uses a background goroutine to process evictions,
// minimizing lock time during hot-path Add operations. The evictCh channel triggers
// async eviction when the cache exceeds its size limit.
type HotCache struct {
	mu sync.RWMutex

	// entries maps content ID to lruEntry
	entries map[string]*lruEntry

	// lruList maintains access order (front = most recent, back = least recent)
	lruList *list.List

	// currentSize tracks total size of cached entries (atomic for quick checks)
	currentSize atomic.Int64

	// maxSize is the maximum cache size in bytes (atomic for quick checks)
	maxSize atomic.Int64

	// defaultMaxSize is the configured default max size
	defaultMaxSize int64

	// evictionBatchSize is the max entries to evict per batch
	evictionBatchSize int

	// onEvict is called when an entry is evicted (outside lock)
	onEvict EvictionCallback

	// evictCh triggers async eviction when cache exceeds limit
	evictCh chan struct{}

	// stopCh signals the eviction goroutine to stop
	stopCh chan struct{}

	// wg tracks the eviction goroutine for graceful shutdown
	wg sync.WaitGroup

	// asyncEviction enables async eviction mode (default: true)
	asyncEviction bool

	// closed indicates whether Close() has been called (atomic for safe checking)
	closed atomic.Bool

	// stats
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
}

// HotCacheConfig holds configuration for the hot cache.
type HotCacheConfig struct {
	// MaxSize is the maximum cache size in bytes.
	MaxSize int64

	// EvictionBatchSize is the maximum number of entries to evict per batch.
	// Default: 100
	EvictionBatchSize int

	// OnEvict is called when an entry is evicted from the cache.
	// The callback is invoked outside the lock.
	OnEvict EvictionCallback

	// AsyncEviction enables async eviction mode. When true, eviction is
	// triggered via a channel and processed by a background goroutine,
	// minimizing lock time during Add operations.
	// Default: true
	AsyncEviction bool
}

// NewHotCache creates a new hot cache with the given configuration.
func NewHotCache(config HotCacheConfig) *HotCache {
	if config.MaxSize <= 0 {
		config.MaxSize = DefaultHotCacheMaxSize
	}
	if config.EvictionBatchSize <= 0 {
		config.EvictionBatchSize = DefaultEvictionBatchSize
	}

	c := &HotCache{
		entries:           make(map[string]*lruEntry),
		lruList:           list.New(),
		defaultMaxSize:    config.MaxSize,
		evictionBatchSize: config.EvictionBatchSize,
		onEvict:           config.OnEvict,
		evictCh:           make(chan struct{}, 1),
		stopCh:            make(chan struct{}),
		asyncEviction:     config.AsyncEviction,
	}
	c.maxSize.Store(config.MaxSize)
	c.currentSize.Store(0)

	// Start async eviction goroutine if enabled
	if config.AsyncEviction {
		c.wg.Add(1)
		go c.evictionLoop()
	}

	return c
}

// NewDefaultHotCache creates a hot cache with default configuration.
// Note: Async eviction is disabled by default for backward compatibility.
// Use NewHotCache with AsyncEviction: true for async eviction mode.
func NewDefaultHotCache() *HotCache {
	return NewHotCache(HotCacheConfig{AsyncEviction: false})
}

// evictionLoop is the background goroutine that processes async eviction requests.
func (c *HotCache) evictionLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stopCh:
			return
		case <-c.evictCh:
			c.processAsyncEviction()
		}
	}
}

// processAsyncEviction handles batch eviction triggered asynchronously.
func (c *HotCache) processAsyncEviction() {
	for {
		// Quick check without lock
		if c.currentSize.Load() <= c.maxSize.Load() {
			return
		}

		c.mu.Lock()
		// Re-check under lock
		currentSize := c.currentSize.Load()
		maxSize := c.maxSize.Load()
		if currentSize <= maxSize {
			c.mu.Unlock()
			return
		}

		targetBytes := currentSize - maxSize
		evicted, _ := c.evictBatch(c.evictionBatchSize, targetBytes)
		c.mu.Unlock()

		// Invoke callbacks outside lock
		c.invokeEvictionCallbacks(evicted)

		// If we evicted less than a full batch, we're done
		if len(evicted) < c.evictionBatchSize {
			return
		}
	}
}

// triggerAsyncEviction sends a signal to the eviction goroutine if needed.
// This is a non-blocking operation.
func (c *HotCache) triggerAsyncEviction() {
	// Quick check without lock
	if c.currentSize.Load() <= c.maxSize.Load() {
		return
	}

	// Non-blocking send to trigger eviction
	select {
	case c.evictCh <- struct{}{}:
	default:
		// Eviction already pending
	}
}

// Close stops the async eviction goroutine and cleans up resources.
// After Close is called, the cache should not be used.
// Close is safe to call multiple times; subsequent calls are no-ops.
func (c *HotCache) Close() {
	// Use atomic swap to ensure Close() is only executed once
	if !c.closed.CompareAndSwap(false, true) {
		return // Already closed
	}

	if c.asyncEviction {
		close(c.stopCh)
		c.wg.Wait()
	}
}

// =============================================================================
// EvictableCache Interface
// =============================================================================

// Name returns the cache identifier for PressureController.
func (c *HotCache) Name() string {
	return HotCacheName
}

// Size returns the current cache size in bytes.
func (c *HotCache) Size() int64 {
	return c.currentSize.Load()
}

// EvictPercent evicts the given percentage of entries (by size).
// Returns the number of bytes evicted. Uses batch eviction with callbacks.
func (c *HotCache) EvictPercent(percent float64) int64 {
	if percent <= 0 {
		return 0
	}
	if percent > 1.0 {
		percent = 1.0
	}

	c.mu.Lock()
	currentSize := c.currentSize.Load()
	if currentSize == 0 {
		c.mu.Unlock()
		return 0
	}

	targetEviction := int64(float64(currentSize) * percent)
	evicted, evictedSize := c.evictBatch(c.evictionBatchSize, targetEviction)

	// If we haven't evicted enough and there are more entries, keep evicting
	for evictedSize < targetEviction && c.lruList.Len() > 0 {
		moreEvicted, moreSize := c.evictBatch(c.evictionBatchSize, targetEviction-evictedSize)
		evicted = append(evicted, moreEvicted...)
		evictedSize += moreSize
		if len(moreEvicted) == 0 {
			break
		}
	}
	c.mu.Unlock()

	// Invoke callbacks outside lock
	c.invokeEvictionCallbacks(evicted)

	return evictedSize
}

// evictBytes evicts entries until targetBytes have been evicted.
// Must be called with mu held. Returns the number of bytes evicted.
func (c *HotCache) evictBytes(targetBytes int64) int64 {
	var evictedSize int64

	for evictedSize < targetBytes && c.lruList.Len() > 0 {
		// Remove from back (LRU)
		back := c.lruList.Back()
		if back == nil {
			break
		}

		entry := back.Value.(*lruEntry)
		evictedSize += c.removeEntryLocked(entry)
	}

	return evictedSize
}

// evictedEntry holds information about an evicted entry for callback invocation.
// This struct is used internally to pass eviction data between the locked
// eviction phase and the unlocked callback invocation phase, allowing
// expensive cleanup operations to run without holding the cache lock.
type evictedEntry struct {
	id    string        // Cache key of the evicted entry
	entry *ContentEntry // The evicted content entry (may be nil for deleted entries)
}

// evictBatch evicts up to batchSize entries from the cache.
// Returns the entries that were evicted for callback processing.
// Must be called with mu held.
//
// Performance optimization: Pre-allocates the evicted slice to reduce allocations
// during the critical section. Uses a single pass through the LRU list tail.
func (c *HotCache) evictBatch(batchSize int, targetBytes int64) ([]evictedEntry, int64) {
	// Pre-allocate with estimated capacity to reduce allocations under lock
	estimatedCount := min(batchSize, c.lruList.Len())
	evicted := make([]evictedEntry, 0, estimatedCount)
	var evictedSize int64

	// Single pass through LRU list tail - O(batchSize) operations
	for len(evicted) < batchSize && evictedSize < targetBytes {
		back := c.lruList.Back()
		if back == nil {
			break
		}

		entry := back.Value.(*lruEntry)

		// Collect entry info before modifying data structures
		evicted = append(evicted, evictedEntry{
			id:    entry.id,
			entry: entry.entry,
		})
		entrySize := entry.size

		// Remove from LRU list and map (O(1) operations)
		c.lruList.Remove(entry.element)
		delete(c.entries, entry.id)

		// Update atomics outside the map/list operations
		c.currentSize.Add(-entrySize)
		evictedSize += entrySize
		c.evictions.Add(1)
	}

	return evicted, evictedSize
}

// removeEntryLocked removes an entry from the cache. Must be called with mu held.
func (c *HotCache) removeEntryLocked(entry *lruEntry) int64 {
	c.lruList.Remove(entry.element)
	delete(c.entries, entry.id)
	c.currentSize.Add(-entry.size)
	c.evictions.Add(1)
	return entry.size
}

// invokeEvictionCallbacks calls the eviction callback for each evicted entry.
// This is called outside the lock to allow expensive cleanup operations.
func (c *HotCache) invokeEvictionCallbacks(evicted []evictedEntry) {
	if c.onEvict == nil {
		return
	}
	for _, e := range evicted {
		c.onEvict(e.id, e.entry)
	}
}

// =============================================================================
// Cache Size Management
// =============================================================================

// SetMaxSize sets the maximum cache size and evicts if necessary.
// Uses batch eviction with minimal lock time.
func (c *HotCache) SetMaxSize(size int64) {
	c.maxSize.Store(size)

	if c.asyncEviction {
		// Trigger async eviction if over limit
		c.triggerAsyncEviction()
	} else {
		// Synchronous eviction
		c.mu.Lock()
		evicted := c.evictIfOverLimitLocked()
		c.mu.Unlock()
		c.invokeEvictionCallbacks(evicted)
	}
}

// MaxSize returns the current max size.
func (c *HotCache) MaxSize() int64 {
	return c.maxSize.Load()
}

// DefaultMaxSize returns the configured default max size.
func (c *HotCache) DefaultMaxSize() int64 {
	return c.defaultMaxSize
}

// =============================================================================
// Cache Operations
// =============================================================================

// Add adds or updates an entry in the cache.
// Uses batch eviction with minimal lock time when the cache exceeds max size.
// In async eviction mode, eviction is triggered in a background goroutine.
// Returns silently if the cache has been closed (W4N.8 thread-safety).
func (c *HotCache) Add(id string, entry *ContentEntry) {
	// Check closed flag first to prevent operations after Close() (W4N.8)
	if c.closed.Load() {
		return
	}

	if entry == nil {
		return
	}

	size := c.estimateSize(entry)

	c.mu.Lock()

	// Update existing entry
	if existing, ok := c.entries[id]; ok {
		sizeDiff := size - existing.size
		c.currentSize.Add(sizeDiff)
		existing.entry = entry
		existing.size = size
		c.lruList.MoveToFront(existing.element)
		c.mu.Unlock()

		if c.asyncEviction {
			c.triggerAsyncEviction()
		} else {
			c.evictIfOverLimit()
		}
		return
	}

	// Add new entry
	lru := &lruEntry{
		id:    id,
		entry: entry,
		size:  size,
	}
	lru.element = c.lruList.PushFront(lru)
	c.entries[id] = lru
	c.currentSize.Add(size)
	c.mu.Unlock()

	if c.asyncEviction {
		c.triggerAsyncEviction()
	} else {
		c.evictIfOverLimit()
	}
}

// evictIfOverLimitLocked evicts entries if the cache is over the size limit.
// Eviction is bounded by MaxEvictionsPerAdd to prevent latency spikes (W3M.10).
// Must be called with mu held. Returns entries to invoke callbacks on.
func (c *HotCache) evictIfOverLimitLocked() []evictedEntry {
	currentSize := c.currentSize.Load()
	maxSize := c.maxSize.Load()
	if currentSize <= maxSize {
		return nil
	}

	targetBytes := currentSize - maxSize
	// Bound eviction count to MaxEvictionsPerAdd to prevent latency spikes
	evicted, _ := c.evictBatch(MaxEvictionsPerAdd, targetBytes)
	return evicted
}

// evictIfOverLimit evicts entries if the cache is over the size limit.
// Uses batch eviction with minimal lock time and invokes callbacks outside the lock.
func (c *HotCache) evictIfOverLimit() {
	c.mu.Lock()
	evicted := c.evictIfOverLimitLocked()
	c.mu.Unlock()

	// Invoke callbacks outside lock
	c.invokeEvictionCallbacks(evicted)
}

func (c *HotCache) estimateSize(entry *ContentEntry) int64 {
	// Estimate: content length + metadata overhead
	size := int64(len(entry.Content))
	size += int64(len(entry.ID)) * 2 // ID stored multiple places
	size += int64(len(entry.SessionID))
	size += int64(len(entry.AgentID))
	size += int64(len(entry.AgentType))

	// Keywords and entities
	for _, kw := range entry.Keywords {
		size += int64(len(kw))
	}
	for _, ent := range entry.Entities {
		size += int64(len(ent))
	}

	// Embedding (float32 = 4 bytes each)
	size += int64(len(entry.Embedding)) * 4

	// Metadata
	for k, v := range entry.Metadata {
		size += int64(len(k)) + int64(len(v))
	}

	// Fixed overhead for struct fields
	size += 200

	return size
}

// Get retrieves an entry from the cache, updating LRU order.
func (c *HotCache) Get(id string) *ContentEntry {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok {
		c.misses.Add(1)
		return nil
	}

	c.hits.Add(1)
	c.lruList.MoveToFront(entry.element)
	return entry.entry
}

// Contains checks if an entry exists without updating LRU order.
func (c *HotCache) Contains(id string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.entries[id]
	return ok
}

// Remove removes an entry from the cache.
// The eviction callback is invoked if one is configured.
func (c *HotCache) Remove(id string) bool {
	c.mu.Lock()
	entry, ok := c.entries[id]
	if !ok {
		c.mu.Unlock()
		return false
	}

	// Extract info for callback before removing
	evictedInfo := evictedEntry{
		id:    entry.id,
		entry: entry.entry,
	}

	c.lruList.Remove(entry.element)
	delete(c.entries, entry.id)
	c.currentSize.Add(-entry.size)
	c.mu.Unlock()

	// Invoke callback outside lock
	if c.onEvict != nil {
		c.onEvict(evictedInfo.id, evictedInfo.entry)
	}

	return true
}

// =============================================================================
// Bulk Operations
// =============================================================================

// AddBatch adds multiple entries to the cache.
func (c *HotCache) AddBatch(entries map[string]*ContentEntry) {
	for id, entry := range entries {
		c.Add(id, entry)
	}
}

// GetBatch retrieves multiple entries from the cache.
func (c *HotCache) GetBatch(ids []string) map[string]*ContentEntry {
	result := make(map[string]*ContentEntry)
	for _, id := range ids {
		if entry := c.Get(id); entry != nil {
			result[id] = entry
		}
	}
	return result
}

// Clear removes all entries from the cache.
// The eviction callback is invoked for each entry if configured.
func (c *HotCache) Clear() {
	c.mu.Lock()

	// Collect all entries for callback
	var evicted []evictedEntry
	if c.onEvict != nil {
		evicted = make([]evictedEntry, 0, len(c.entries))
		for id, entry := range c.entries {
			evicted = append(evicted, evictedEntry{
				id:    id,
				entry: entry.entry,
			})
		}
	}

	c.entries = make(map[string]*lruEntry)
	c.lruList = list.New()
	c.currentSize.Store(0)
	c.mu.Unlock()

	// Invoke callbacks outside lock
	c.invokeEvictionCallbacks(evicted)
}

// SetEvictionCallback sets or replaces the eviction callback.
// Pass nil to disable the callback.
func (c *HotCache) SetEvictionCallback(callback EvictionCallback) {
	c.mu.Lock()
	c.onEvict = callback
	c.mu.Unlock()
}

// SetEvictionBatchSize sets the maximum entries to evict per batch.
func (c *HotCache) SetEvictionBatchSize(size int) {
	if size <= 0 {
		size = DefaultEvictionBatchSize
	}
	c.mu.Lock()
	c.evictionBatchSize = size
	c.mu.Unlock()
}

// EvictionBatchSize returns the current eviction batch size.
func (c *HotCache) EvictionBatchSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.evictionBatchSize
}

// =============================================================================
// Statistics
// =============================================================================

// HotCacheStats holds cache statistics.
type HotCacheStats struct {
	EntryCount        int
	CurrentSize       int64
	MaxSize           int64
	Hits              int64
	Misses            int64
	Evictions         int64
	EvictionBatchSize int
	HitRate           float64
}

// Stats returns current cache statistics.
func (c *HotCache) Stats() HotCacheStats {
	c.mu.RLock()
	entryCount := len(c.entries)
	evictionBatchSize := c.evictionBatchSize
	c.mu.RUnlock()

	hits := c.hits.Load()
	misses := c.misses.Load()
	evictions := c.evictions.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return HotCacheStats{
		EntryCount:        entryCount,
		CurrentSize:       c.currentSize.Load(),
		MaxSize:           c.maxSize.Load(),
		Hits:              hits,
		Misses:            misses,
		Evictions:         evictions,
		EvictionBatchSize: evictionBatchSize,
		HitRate:           hitRate,
	}
}

// ResetStats resets hit/miss/eviction counters.
func (c *HotCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.evictions.Store(0)
}

// =============================================================================
// Iteration
// =============================================================================

// Keys returns all cached entry IDs.
func (c *HotCache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0, len(c.entries))
	for id := range c.entries {
		keys = append(keys, id)
	}
	return keys
}

// Entries returns all cached entries (for debugging/testing).
func (c *HotCache) Entries() map[string]*ContentEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*ContentEntry, len(c.entries))
	for id, lru := range c.entries {
		result[id] = lru.entry
	}
	return result
}

// LRUOrder returns entry IDs in LRU order (most recent first).
func (c *HotCache) LRUOrder() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	order := make([]string, 0, c.lruList.Len())
	for e := c.lruList.Front(); e != nil; e = e.Next() {
		order = append(order, e.Value.(*lruEntry).id)
	}
	return order
}
