// Package memory provides ACT-R based memory management for adaptive retrieval.
// PF.1.4: Activation Cache for avoiding redundant ACT-R calculations.
// This file provides an LRU-based cache with TTL invalidation for storing
// computed activation values to avoid repeated expensive calculations.
package memory

import (
	"container/list"
	"sync"
	"time"
)

// =============================================================================
// ActivationCache Configuration
// =============================================================================

// ActivationCacheConfig configures the activation cache behavior.
type ActivationCacheConfig struct {
	// MaxSize is the maximum number of entries in the cache.
	// When exceeded, least recently used entries are evicted.
	MaxSize int

	// TTL is the time-to-live for cached activations.
	// After this duration, entries are considered stale and will be recalculated.
	// This is important because activations change over time as memories decay.
	TTL time.Duration

	// CleanupInterval determines how often expired entries are proactively removed.
	// If zero, no background cleanup occurs (entries expire lazily on access).
	CleanupInterval time.Duration
}

// DefaultActivationCacheConfig returns a sensible default configuration.
func DefaultActivationCacheConfig() ActivationCacheConfig {
	return ActivationCacheConfig{
		MaxSize:         10000,              // 10K entries
		TTL:             5 * time.Minute,    // 5 minute TTL
		CleanupInterval: 1 * time.Minute,    // Cleanup every minute
	}
}

// SmallActivationCacheConfig returns a configuration suitable for low-memory environments.
func SmallActivationCacheConfig() ActivationCacheConfig {
	return ActivationCacheConfig{
		MaxSize:         1000,               // 1K entries
		TTL:             2 * time.Minute,    // 2 minute TTL
		CleanupInterval: 30 * time.Second,   // Cleanup every 30 seconds
	}
}

// LargeActivationCacheConfig returns a configuration for high-throughput scenarios.
func LargeActivationCacheConfig() ActivationCacheConfig {
	return ActivationCacheConfig{
		MaxSize:         100000,             // 100K entries
		TTL:             10 * time.Minute,   // 10 minute TTL
		CleanupInterval: 2 * time.Minute,    // Cleanup every 2 minutes
	}
}

// =============================================================================
// ActivationCacheKey
// =============================================================================

// ActivationCacheKey uniquely identifies a cached activation value.
// Activations are keyed by node ID and the timestamp used for calculation,
// rounded to the nearest TTL bucket for efficient lookup.
type ActivationCacheKey struct {
	NodeID     string
	TimeBucket int64 // Unix timestamp rounded to TTL bucket
}

// =============================================================================
// ActivationCacheEntry
// =============================================================================

// ActivationCacheEntry represents a cached activation value with metadata.
type ActivationCacheEntry struct {
	// Key is the cache key for this entry.
	Key ActivationCacheKey

	// Activation is the cached activation value.
	Activation float64

	// ComputedAt is when this activation was calculated.
	ComputedAt time.Time

	// ExpiresAt is when this cache entry should be considered stale.
	ExpiresAt time.Time

	// element is a pointer to the LRU list element for O(1) removal.
	element *list.Element
}

// IsExpired returns true if this cache entry has expired.
func (e *ActivationCacheEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// =============================================================================
// ActivationCache
// =============================================================================

// ActivationCache provides an LRU cache with TTL-based invalidation for
// computed ACT-R activation values. It is thread-safe for concurrent access.
//
// The cache is designed to avoid redundant activation calculations by caching
// results keyed by (nodeID, timeBucket). Time bucketing allows multiple requests
// within the same time window to share cached values while ensuring activations
// are recalculated as they naturally decay over time.
type ActivationCache struct {
	mu sync.RWMutex

	// config holds the cache configuration.
	config ActivationCacheConfig

	// entries maps cache keys to their entries.
	entries map[ActivationCacheKey]*ActivationCacheEntry

	// lru is a doubly-linked list for LRU eviction tracking.
	// The front of the list is the most recently used, back is least recently used.
	lru *list.List

	// timeBucketSize is the size of time buckets in nanoseconds.
	// This determines how time is quantized for cache key generation.
	timeBucketSize int64

	// stats tracks cache performance metrics.
	stats ActivationCacheStats

	// stopCleanup is a channel to signal the cleanup goroutine to stop.
	stopCleanup chan struct{}

	// cleanupDone signals when cleanup goroutine has stopped.
	cleanupDone chan struct{}
}

// ActivationCacheStats tracks cache performance metrics.
type ActivationCacheStats struct {
	Hits       int64
	Misses     int64
	Evictions  int64
	Expirations int64
}

// NewActivationCache creates a new ActivationCache with the given configuration.
func NewActivationCache(config ActivationCacheConfig) *ActivationCache {
	if config.MaxSize <= 0 {
		config.MaxSize = DefaultActivationCacheConfig().MaxSize
	}
	if config.TTL <= 0 {
		config.TTL = DefaultActivationCacheConfig().TTL
	}

	cache := &ActivationCache{
		config:         config,
		entries:        make(map[ActivationCacheKey]*ActivationCacheEntry),
		lru:            list.New(),
		timeBucketSize: config.TTL.Nanoseconds(),
		stopCleanup:    make(chan struct{}),
		cleanupDone:    make(chan struct{}),
	}

	// Start background cleanup if configured
	if config.CleanupInterval > 0 {
		go cache.cleanupLoop()
	} else {
		close(cache.cleanupDone)
	}

	return cache
}

// NewDefaultActivationCache creates a cache with default configuration.
func NewDefaultActivationCache() *ActivationCache {
	return NewActivationCache(DefaultActivationCacheConfig())
}

// =============================================================================
// Cache Operations
// =============================================================================

// Get retrieves a cached activation value for the given node and access time.
// Returns the cached activation and true if found and not expired,
// or 0.0 and false if not found or expired.
func (c *ActivationCache) Get(nodeID string, accessTime time.Time) (float64, bool) {
	key := c.makeKey(nodeID, accessTime)

	c.mu.RLock()
	entry, found := c.entries[key]
	c.mu.RUnlock()

	if !found {
		c.recordMiss()
		return 0.0, false
	}

	// Check expiration
	if entry.IsExpired() {
		c.recordExpiration()
		// Don't remove here (would need write lock), let it be cleaned up later
		return 0.0, false
	}

	// Update LRU position (requires write lock)
	c.mu.Lock()
	c.lru.MoveToFront(entry.element)
	c.mu.Unlock()

	c.recordHit()
	return entry.Activation, true
}

// Set stores an activation value in the cache.
// If the cache is at capacity, the least recently used entry is evicted.
func (c *ActivationCache) Set(nodeID string, accessTime time.Time, activation float64) {
	key := c.makeKey(nodeID, accessTime)
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if entry already exists
	if existing, found := c.entries[key]; found {
		// Update existing entry
		existing.Activation = activation
		existing.ComputedAt = now
		existing.ExpiresAt = now.Add(c.config.TTL)
		c.lru.MoveToFront(existing.element)
		return
	}

	// Create new entry
	entry := &ActivationCacheEntry{
		Key:        key,
		Activation: activation,
		ComputedAt: now,
		ExpiresAt:  now.Add(c.config.TTL),
	}

	// Add to LRU list
	entry.element = c.lru.PushFront(entry)
	c.entries[key] = entry

	// Evict if over capacity
	for len(c.entries) > c.config.MaxSize {
		c.evictOldest()
	}
}

// GetOrCompute retrieves a cached activation or computes it using the provided function.
// This is the preferred method for using the cache as it handles the get-or-compute
// pattern atomically.
func (c *ActivationCache) GetOrCompute(
	nodeID string,
	accessTime time.Time,
	compute func() float64,
) float64 {
	// Try to get from cache first
	if activation, found := c.Get(nodeID, accessTime); found {
		return activation
	}

	// Compute the activation
	activation := compute()

	// Store in cache
	c.Set(nodeID, accessTime, activation)

	return activation
}

// Invalidate removes a specific entry from the cache.
func (c *ActivationCache) Invalidate(nodeID string, accessTime time.Time) {
	key := c.makeKey(nodeID, accessTime)

	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, found := c.entries[key]; found {
		c.lru.Remove(entry.element)
		delete(c.entries, key)
	}
}

// InvalidateNode removes all cached entries for a specific node.
// Use this when a node's memory traces have been updated.
func (c *ActivationCache) InvalidateNode(nodeID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find and remove all entries for this node
	for key, entry := range c.entries {
		if key.NodeID == nodeID {
			c.lru.Remove(entry.element)
			delete(c.entries, key)
		}
	}
}

// Clear removes all entries from the cache.
func (c *ActivationCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[ActivationCacheKey]*ActivationCacheEntry)
	c.lru = list.New()
}

// Size returns the current number of entries in the cache.
func (c *ActivationCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Stats returns a copy of the cache statistics.
func (c *ActivationCache) Stats() ActivationCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats
}

// ResetStats resets all statistics counters to zero.
func (c *ActivationCache) ResetStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stats = ActivationCacheStats{}
}

// HitRate returns the cache hit rate as a percentage (0.0 to 1.0).
func (c *ActivationCache) HitRate() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := c.stats.Hits + c.stats.Misses
	if total == 0 {
		return 0.0
	}
	return float64(c.stats.Hits) / float64(total)
}

// =============================================================================
// Lifecycle Management
// =============================================================================

// Stop stops the background cleanup goroutine and releases resources.
// The cache can still be used after stopping, but expired entries will only
// be removed lazily on access.
func (c *ActivationCache) Stop() {
	select {
	case <-c.stopCleanup:
		// Already stopped
		return
	default:
		close(c.stopCleanup)
	}

	// Wait for cleanup goroutine to finish
	<-c.cleanupDone
}

// =============================================================================
// Internal Methods
// =============================================================================

// makeKey creates a cache key from a node ID and access time.
// Time is quantized to buckets based on the TTL to allow cache hits
// for requests within the same time window.
func (c *ActivationCache) makeKey(nodeID string, accessTime time.Time) ActivationCacheKey {
	timeBucket := accessTime.UnixNano() / c.timeBucketSize
	return ActivationCacheKey{
		NodeID:     nodeID,
		TimeBucket: timeBucket,
	}
}

// evictOldest removes the least recently used entry from the cache.
// Must be called with mu held.
func (c *ActivationCache) evictOldest() {
	oldest := c.lru.Back()
	if oldest == nil {
		return
	}

	entry := oldest.Value.(*ActivationCacheEntry)
	c.lru.Remove(oldest)
	delete(c.entries, entry.Key)
	c.stats.Evictions++
}

// cleanupLoop runs periodic cleanup of expired entries.
func (c *ActivationCache) cleanupLoop() {
	defer close(c.cleanupDone)

	ticker := time.NewTicker(c.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCleanup:
			return
		case <-ticker.C:
			c.cleanupExpired()
		}
	}
}

// cleanupExpired removes all expired entries from the cache.
func (c *ActivationCache) cleanupExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			c.lru.Remove(entry.element)
			delete(c.entries, key)
			c.stats.Expirations++
		}
	}
}

// recordHit increments the hit counter.
func (c *ActivationCache) recordHit() {
	c.mu.Lock()
	c.stats.Hits++
	c.mu.Unlock()
}

// recordMiss increments the miss counter.
func (c *ActivationCache) recordMiss() {
	c.mu.Lock()
	c.stats.Misses++
	c.mu.Unlock()
}

// recordExpiration increments the expiration counter.
func (c *ActivationCache) recordExpiration() {
	c.mu.Lock()
	c.stats.Expirations++
	c.mu.Unlock()
}

// =============================================================================
// Batch Operations
// =============================================================================

// GetBatch retrieves multiple activation values from the cache.
// Returns a map of nodeID -> activation for all found (non-expired) entries.
func (c *ActivationCache) GetBatch(nodeIDs []string, accessTime time.Time) map[string]float64 {
	result := make(map[string]float64, len(nodeIDs))

	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, nodeID := range nodeIDs {
		key := c.makeKey(nodeID, accessTime)
		if entry, found := c.entries[key]; found && !entry.IsExpired() {
			result[nodeID] = entry.Activation
		}
	}

	return result
}

// SetBatch stores multiple activation values in the cache.
func (c *ActivationCache) SetBatch(activations map[string]float64, accessTime time.Time) {
	if len(activations) == 0 {
		return
	}

	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	for nodeID, activation := range activations {
		key := c.makeKey(nodeID, accessTime)

		// Check if entry already exists
		if existing, found := c.entries[key]; found {
			existing.Activation = activation
			existing.ComputedAt = now
			existing.ExpiresAt = now.Add(c.config.TTL)
			c.lru.MoveToFront(existing.element)
			continue
		}

		// Create new entry
		entry := &ActivationCacheEntry{
			Key:        key,
			Activation: activation,
			ComputedAt: now,
			ExpiresAt:  now.Add(c.config.TTL),
		}

		entry.element = c.lru.PushFront(entry)
		c.entries[key] = entry
	}

	// Evict if over capacity
	for len(c.entries) > c.config.MaxSize {
		c.evictOldest()
	}
}

// =============================================================================
// Diagnostic Methods
// =============================================================================

// Entries returns the number of cached entries per node.
// This is useful for debugging and monitoring cache distribution.
func (c *ActivationCache) EntriesPerNode() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	counts := make(map[string]int)
	for key := range c.entries {
		counts[key.NodeID]++
	}
	return counts
}

// ExpiredCount returns the number of expired entries still in the cache.
func (c *ActivationCache) ExpiredCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	count := 0
	now := time.Now()
	for _, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			count++
		}
	}
	return count
}

// OldestEntry returns the age of the oldest entry in the cache.
func (c *ActivationCache) OldestEntry() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.lru.Len() == 0 {
		return 0
	}

	oldest := c.lru.Back()
	if oldest == nil {
		return 0
	}

	entry := oldest.Value.(*ActivationCacheEntry)
	return time.Since(entry.ComputedAt)
}

// MemoryEstimate returns an estimated memory usage in bytes.
// This is approximate and doesn't include Go runtime overhead.
func (c *ActivationCache) MemoryEstimate() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Rough estimate per entry:
	// - Key: ~50 bytes (nodeID string + int64)
	// - Value: ~80 bytes (float64 + 3 time.Time + pointer)
	// - Map entry overhead: ~16 bytes
	// - List element overhead: ~48 bytes
	// Total: ~194 bytes per entry
	const bytesPerEntry = 200
	return int64(len(c.entries) * bytesPerEntry)
}
