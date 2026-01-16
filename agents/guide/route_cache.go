package guide

import (
	"sync"
	"time"
)

// =============================================================================
// Route Cache
// =============================================================================
//
// RouteCache provides fast lookup for previously classified routes.
// This eliminates LLM calls for repeat queries, reducing token costs to zero.
//
// Cache hierarchy:
// 1. Exact match (normalized input string)
// 2. Future: Semantic similarity matching

// RouteCache caches classification results to avoid repeat LLM calls
type RouteCache struct {
	mu      sync.RWMutex
	entries map[string]*CachedRoute
	maxSize int
	ttl     time.Duration

	// Stats
	hits   int64
	misses int64
}

// CachedRoute represents a cached classification result
type CachedRoute struct {
	// Original input (normalized)
	Input string `json:"input"`

	// Classification result
	TargetAgentID string  `json:"target_agent_id"`
	Intent        Intent  `json:"intent"`
	Domain        Domain  `json:"domain"`
	Confidence    float64 `json:"confidence"`

	// Cache metadata
	CachedAt  time.Time `json:"cached_at"`
	ExpiresAt time.Time `json:"expires_at"`
	HitCount  int64     `json:"hit_count"`

	// For promotion to learned pattern
	ClassificationCount int64 `json:"classification_count"`
}

// RouteCacheConfig configures the route cache
type RouteCacheConfig struct {
	MaxSize int           // Maximum entries (default: 10000)
	TTL     time.Duration // Entry lifetime (default: 1 hour)
}

// DefaultRouteCacheConfig returns sensible defaults
func DefaultRouteCacheConfig() RouteCacheConfig {
	return RouteCacheConfig{
		MaxSize: 10000,
		TTL:     1 * time.Hour,
	}
}

// NewRouteCache creates a new route cache
func NewRouteCache(cfg RouteCacheConfig) *RouteCache {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 10000
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 1 * time.Hour
	}

	return &RouteCache{
		entries: make(map[string]*CachedRoute),
		maxSize: cfg.MaxSize,
		ttl:     cfg.TTL,
	}
}

// =============================================================================
// Cache Operations
// =============================================================================

// Get retrieves a cached route for the given input.
// Returns nil if not found or expired.
func (c *RouteCache) Get(input string) *CachedRoute {
	normalized := normalizeInput(input)

	c.mu.RLock()
	entry, ok := c.entries[normalized]
	c.mu.RUnlock()

	if !ok {
		c.recordMiss()
		return nil
	}

	// Check expiration
	if time.Now().After(entry.ExpiresAt) {
		c.mu.Lock()
		delete(c.entries, normalized)
		c.mu.Unlock()
		c.recordMiss()
		return nil
	}

	// Update hit count
	c.mu.Lock()
	entry.HitCount++
	c.mu.Unlock()

	c.recordHit()
	return entry
}

// Set stores a classification result in the cache
func (c *RouteCache) Set(input string, result *RouteResult) {
	normalized := normalizeInput(input)
	now := time.Now()

	entry := &CachedRoute{
		Input:               normalized,
		TargetAgentID:       string(result.TargetAgent),
		Intent:              result.Intent,
		Domain:              result.Domain,
		Confidence:          result.Confidence,
		CachedAt:            now,
		ExpiresAt:           now.Add(c.ttl),
		HitCount:            0,
		ClassificationCount: 1,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we need to evict
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	// Check if updating existing entry
	if existing, ok := c.entries[normalized]; ok {
		entry.ClassificationCount = existing.ClassificationCount + 1
		entry.HitCount = existing.HitCount
	}

	c.entries[normalized] = entry
}

// SetFromRoute stores a route directly (for learned routes from Guide)
func (c *RouteCache) SetFromRoute(input string, targetAgentID string, intent Intent, domain Domain) {
	normalized := normalizeInput(input)
	now := time.Now()

	entry := &CachedRoute{
		Input:               normalized,
		TargetAgentID:       targetAgentID,
		Intent:              intent,
		Domain:              domain,
		Confidence:          1.0, // Learned routes have max confidence
		CachedAt:            now,
		ExpiresAt:           now.Add(c.ttl),
		HitCount:            0,
		ClassificationCount: 1,
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[normalized] = entry
}

// Invalidate removes an entry from the cache
func (c *RouteCache) Invalidate(input string) {
	normalized := normalizeInput(input)

	c.mu.Lock()
	delete(c.entries, normalized)
	c.mu.Unlock()
}

// InvalidateForAgent removes all entries targeting a specific agent
func (c *RouteCache) InvalidateForAgent(agentID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		if entry.TargetAgentID == agentID {
			delete(c.entries, key)
		}
	}
}

// Clear removes all entries from the cache
func (c *RouteCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]*CachedRoute)
	c.mu.Unlock()
}

// =============================================================================
// Cache Maintenance
// =============================================================================

// evictOldest removes the oldest entry (must hold write lock)
func (c *RouteCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range c.entries {
		if oldestKey == "" || entry.CachedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.CachedAt
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

// Cleanup removes expired entries
func (c *RouteCache) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := 0

	for key, entry := range c.entries {
		if now.After(entry.ExpiresAt) {
			delete(c.entries, key)
			removed++
		}
	}

	return removed
}

// =============================================================================
// Stats
// =============================================================================

// RouteCacheStats contains cache statistics
type RouteCacheStats struct {
	Size     int     `json:"size"`
	MaxSize  int     `json:"max_size"`
	Hits     int64   `json:"hits"`
	Misses   int64   `json:"misses"`
	HitRate  float64 `json:"hit_rate"`
	TTL      string  `json:"ttl"`
}

// Stats returns cache statistics
func (c *RouteCache) Stats() RouteCacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := c.hits + c.misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return RouteCacheStats{
		Size:    len(c.entries),
		MaxSize: c.maxSize,
		Hits:    c.hits,
		Misses:  c.misses,
		HitRate: hitRate,
		TTL:     c.ttl.String(),
	}
}

func (c *RouteCache) recordHit() {
	c.mu.Lock()
	c.hits++
	c.mu.Unlock()
}

func (c *RouteCache) recordMiss() {
	c.mu.Lock()
	c.misses++
	c.mu.Unlock()
}

// =============================================================================
// Input Normalization
// =============================================================================

// normalizeInput normalizes input for cache lookup
func normalizeInput(input string) string {
	// Convert to lowercase
	result := make([]byte, len(input))
	for i := 0; i < len(input); i++ {
		c := input[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}

	// Trim whitespace
	start := 0
	end := len(result)
	for start < end && (result[start] == ' ' || result[start] == '\t' || result[start] == '\n') {
		start++
	}
	for end > start && (result[end-1] == ' ' || result[end-1] == '\t' || result[end-1] == '\n') {
		end--
	}

	return string(result[start:end])
}

// =============================================================================
// Bulk Operations (for syncing from Guide)
// =============================================================================

// GetAll returns all cached routes (for syncing to agents)
func (c *RouteCache) GetAll() []*CachedRoute {
	c.mu.RLock()
	defer c.mu.RUnlock()

	routes := make([]*CachedRoute, 0, len(c.entries))
	for _, entry := range c.entries {
		routes = append(routes, entry)
	}
	return routes
}

// SetBulk sets multiple routes at once (for syncing from Guide)
func (c *RouteCache) SetBulk(routes []*CachedRoute) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, route := range routes {
		if len(c.entries) >= c.maxSize {
			c.evictOldest()
		}
		c.entries[route.Input] = route
	}
}
