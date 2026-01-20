package inference

import "sync"

// =============================================================================
// W4P.13: Edge Index Cache
// =============================================================================

// EdgeIndexCache provides a thread-safe, version-tracked cache for the edge index.
// It rebuilds the index only when edges are added or removed, avoiding the O(n)
// rebuild cost on every rule evaluation.
//
// Thread-safety is ensured via a RWMutex, allowing concurrent read access while
// serializing write operations (invalidation and rebuilding).
type EdgeIndexCache struct {
	mu      sync.RWMutex
	index   *EdgeIndex
	version uint64
	count   int // track edge count for quick invalidation check
}

// NewEdgeIndexCache creates a new edge index cache.
func NewEdgeIndexCache() *EdgeIndexCache {
	return &EdgeIndexCache{
		version: 0,
	}
}

// GetOrBuild returns the cached edge index, rebuilding it if necessary.
// This method is thread-safe for concurrent access.
func (c *EdgeIndexCache) GetOrBuild(edges []Edge) *EdgeIndex {
	c.mu.RLock()
	if c.isValid(edges) {
		idx := c.index
		c.mu.RUnlock()
		return idx
	}
	c.mu.RUnlock()

	// Need to rebuild - acquire write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.isValid(edges) {
		return c.index
	}

	// Rebuild the index
	c.index = buildEdgeIndex(edges)
	c.count = len(edges)
	c.version++
	return c.index
}

// Invalidate marks the cache as invalid, forcing a rebuild on next access.
// Call this when edges are added or removed.
func (c *EdgeIndexCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.index = nil
	c.count = 0
}

// InvalidateIfChanged checks if the edge count changed and invalidates if so.
// Returns true if the cache was invalidated.
func (c *EdgeIndexCache) InvalidateIfChanged(newEdgeCount int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.count != newEdgeCount {
		c.index = nil
		c.count = 0
		return true
	}
	return false
}

// Version returns the current cache version.
// The version increments each time the cache is rebuilt.
func (c *EdgeIndexCache) Version() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// isValid checks if the cached index is still valid for the given edges.
// Must be called with at least a read lock held.
func (c *EdgeIndexCache) isValid(edges []Edge) bool {
	return c.index != nil && c.count == len(edges)
}

// buildEdgeIndex creates an EdgeIndex from a slice of edges.
// This is a standalone function to avoid method receiver on EdgeIndexCache.
func buildEdgeIndex(edges []Edge) *EdgeIndex {
	idx := &EdgeIndex{
		bySource:     make(map[string][]Edge),
		byTarget:     make(map[string][]Edge),
		byPredicate:  make(map[string][]Edge),
		bySourcePred: make(map[string][]Edge),
		byTargetPred: make(map[string][]Edge),
		allEdges:     edges,
	}

	for _, e := range edges {
		idx.bySource[e.Source] = append(idx.bySource[e.Source], e)
		idx.byTarget[e.Target] = append(idx.byTarget[e.Target], e)
		idx.byPredicate[e.Predicate] = append(idx.byPredicate[e.Predicate], e)

		sourcePredKey := e.Source + ":" + e.Predicate
		idx.bySourcePred[sourcePredKey] = append(idx.bySourcePred[sourcePredKey], e)

		targetPredKey := e.Target + ":" + e.Predicate
		idx.byTargetPred[targetPredKey] = append(idx.byTargetPred[targetPredKey], e)
	}

	return idx
}
