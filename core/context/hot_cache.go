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

// HotCacheName is the name used for EvictableCache identification.
const HotCacheName = "adaptive-hot-cache"

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
type HotCache struct {
	mu sync.RWMutex

	// entries maps content ID to lruEntry
	entries map[string]*lruEntry

	// lruList maintains access order (front = most recent, back = least recent)
	lruList *list.List

	// currentSize tracks total size of cached entries
	currentSize int64

	// maxSize is the maximum cache size in bytes
	maxSize int64

	// defaultMaxSize is the configured default max size
	defaultMaxSize int64

	// stats
	hits   atomic.Int64
	misses atomic.Int64
}

// HotCacheConfig holds configuration for the hot cache.
type HotCacheConfig struct {
	MaxSize int64
}

// NewHotCache creates a new hot cache with the given configuration.
func NewHotCache(config HotCacheConfig) *HotCache {
	if config.MaxSize <= 0 {
		config.MaxSize = DefaultHotCacheMaxSize
	}

	return &HotCache{
		entries:        make(map[string]*lruEntry),
		lruList:        list.New(),
		maxSize:        config.MaxSize,
		defaultMaxSize: config.MaxSize,
	}
}

// NewDefaultHotCache creates a hot cache with default configuration.
func NewDefaultHotCache() *HotCache {
	return NewHotCache(HotCacheConfig{})
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
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentSize
}

// EvictPercent evicts the given percentage of entries (by size).
// Returns the number of bytes evicted.
func (c *HotCache) EvictPercent(percent float64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	if percent <= 0 || c.currentSize == 0 {
		return 0
	}
	if percent > 1.0 {
		percent = 1.0
	}

	targetEviction := int64(float64(c.currentSize) * percent)
	return c.evictBytes(targetEviction)
}

func (c *HotCache) evictBytes(targetBytes int64) int64 {
	var evicted int64

	for evicted < targetBytes && c.lruList.Len() > 0 {
		// Remove from back (LRU)
		back := c.lruList.Back()
		if back == nil {
			break
		}

		entry := back.Value.(*lruEntry)
		evicted += c.removeEntry(entry)
	}

	return evicted
}

func (c *HotCache) removeEntry(entry *lruEntry) int64 {
	c.lruList.Remove(entry.element)
	delete(c.entries, entry.id)
	c.currentSize -= entry.size
	return entry.size
}

// =============================================================================
// Cache Size Management
// =============================================================================

// SetMaxSize sets the maximum cache size and evicts if necessary.
func (c *HotCache) SetMaxSize(size int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.maxSize = size

	// Evict if over new limit
	if c.currentSize > c.maxSize {
		c.evictBytes(c.currentSize - c.maxSize)
	}
}

// MaxSize returns the current max size.
func (c *HotCache) MaxSize() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxSize
}

// DefaultMaxSize returns the configured default max size.
func (c *HotCache) DefaultMaxSize() int64 {
	return c.defaultMaxSize
}

// =============================================================================
// Cache Operations
// =============================================================================

// Add adds or updates an entry in the cache.
func (c *HotCache) Add(id string, entry *ContentEntry) {
	if entry == nil {
		return
	}

	size := c.estimateSize(entry)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entry
	if existing, ok := c.entries[id]; ok {
		c.currentSize -= existing.size
		c.currentSize += size
		existing.entry = entry
		existing.size = size
		c.lruList.MoveToFront(existing.element)
		c.evictIfOverLimit()
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
	c.currentSize += size

	c.evictIfOverLimit()
}

func (c *HotCache) evictIfOverLimit() {
	for c.currentSize > c.maxSize && c.lruList.Len() > 0 {
		back := c.lruList.Back()
		if back == nil {
			break
		}
		entry := back.Value.(*lruEntry)
		c.removeEntry(entry)
	}
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
func (c *HotCache) Remove(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[id]
	if !ok {
		return false
	}

	c.removeEntry(entry)
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
func (c *HotCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*lruEntry)
	c.lruList = list.New()
	c.currentSize = 0
}

// =============================================================================
// Statistics
// =============================================================================

// HotCacheStats holds cache statistics.
type HotCacheStats struct {
	EntryCount  int
	CurrentSize int64
	MaxSize     int64
	Hits        int64
	Misses      int64
	HitRate     float64
}

// Stats returns current cache statistics.
func (c *HotCache) Stats() HotCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return HotCacheStats{
		EntryCount:  len(c.entries),
		CurrentSize: c.currentSize,
		MaxSize:     c.maxSize,
		Hits:        hits,
		Misses:      misses,
		HitRate:     hitRate,
	}
}

// ResetStats resets hit/miss counters.
func (c *HotCache) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
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
