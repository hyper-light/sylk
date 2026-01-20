package vectorgraphdb

import (
	"sync"
)

// BatchLoaderFunc is a callback function that loads multiple items by their IDs.
// It returns a map of ID to item, allowing callers to efficiently batch database
// queries instead of loading items one-by-one (N+1 query pattern).
type BatchLoaderFunc[T any] func(ids []string) (map[string]T, error)

// BatchLoader provides efficient batch loading of items to eliminate N+1 query patterns.
// It is designed to be used when you have multiple IDs and need to load the corresponding
// items, replacing code that loops and loads one item at a time.
//
// Example usage:
//
//	loader := NewBatchLoader(func(ids []string) (map[string]*GraphNode, error) {
//	    return nodeStore.GetNodesBatch(ids)
//	}, BatchLoaderConfig{MaxBatchSize: 100})
//
//	// Preload all IDs at once
//	loader.Preload([]string{"id1", "id2", "id3"})
//
//	// Then get individual items from cache
//	node1, ok := loader.Get("id1")
type BatchLoader[T any] struct {
	mu           sync.RWMutex
	loadFn       BatchLoaderFunc[T]
	cache        map[string]T
	errors       map[string]error
	maxBatchSize int
}

// BatchLoaderConfig configures the BatchLoader behavior.
type BatchLoaderConfig struct {
	// MaxBatchSize limits the number of IDs loaded in a single batch.
	// If more IDs are requested, they will be split into multiple batches.
	// Default is 100 if not specified.
	MaxBatchSize int
}

// DefaultBatchLoaderConfig returns the default configuration.
func DefaultBatchLoaderConfig() BatchLoaderConfig {
	return BatchLoaderConfig{
		MaxBatchSize: 100,
	}
}

// NewBatchLoader creates a new BatchLoader with the given load function and configuration.
// The loadFn is called to load items by their IDs in batches.
func NewBatchLoader[T any](loadFn BatchLoaderFunc[T], config BatchLoaderConfig) *BatchLoader[T] {
	if config.MaxBatchSize <= 0 {
		config.MaxBatchSize = 100
	}
	return &BatchLoader[T]{
		loadFn:       loadFn,
		cache:        make(map[string]T),
		errors:       make(map[string]error),
		maxBatchSize: config.MaxBatchSize,
	}
}

// LoadBatch loads multiple items by their IDs in a single batch operation.
// Returns a map of ID to item for all successfully loaded items.
// Items that failed to load or don't exist will not be in the returned map.
//
// This method is thread-safe and can be called concurrently.
func (bl *BatchLoader[T]) LoadBatch(ids []string) (map[string]T, error) {
	if len(ids) == 0 {
		return make(map[string]T), nil
	}

	// Deduplicate IDs
	uniqueIDs := bl.deduplicateIDs(ids)

	// Check cache and load uncached in a single coordinated operation
	uncachedIDs := bl.filterUncachedIDs(uniqueIDs)

	// Load uncached items in batches
	if len(uncachedIDs) > 0 {
		if err := bl.loadInBatches(uncachedIDs); err != nil {
			return nil, err
		}
	}

	// Collect results from cache
	return bl.collectResults(uniqueIDs), nil
}

// deduplicateIDs removes duplicate IDs from the slice.
func (bl *BatchLoader[T]) deduplicateIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

// filterUncachedIDs returns IDs that are not in the cache.
func (bl *BatchLoader[T]) filterUncachedIDs(ids []string) []string {
	bl.mu.RLock()
	defer bl.mu.RUnlock()

	uncached := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, cached := bl.cache[id]; !cached {
			uncached = append(uncached, id)
		}
	}
	return uncached
}

// loadInBatches loads items in batches respecting maxBatchSize.
func (bl *BatchLoader[T]) loadInBatches(ids []string) error {
	for i := 0; i < len(ids); i += bl.maxBatchSize {
		end := i + bl.maxBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[i:end]

		items, err := bl.loadFn(batch)
		if err != nil {
			return err
		}

		bl.mu.Lock()
		for id, item := range items {
			bl.cache[id] = item
		}
		bl.mu.Unlock()
	}
	return nil
}

// collectResults collects results from cache for the given IDs.
func (bl *BatchLoader[T]) collectResults(ids []string) map[string]T {
	bl.mu.RLock()
	defer bl.mu.RUnlock()

	results := make(map[string]T, len(ids))
	for _, id := range ids {
		if item, ok := bl.cache[id]; ok {
			results[id] = item
		}
	}
	return results
}

// Preload eagerly loads items for the given IDs into the cache.
// This is useful when you know you'll need certain items later
// and want to batch the database query upfront.
//
// This method is thread-safe and can be called concurrently.
func (bl *BatchLoader[T]) Preload(ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	// Deduplicate and filter already cached in a single lock cycle
	uniqueIDs := bl.deduplicateIDs(ids)
	uncachedIDs := bl.filterUncachedIDs(uniqueIDs)

	if len(uncachedIDs) == 0 {
		return nil
	}

	return bl.loadInBatches(uncachedIDs)
}

// Get retrieves a single item from the cache.
// Returns the item and true if found, or zero value and false if not cached.
// Use LoadBatch or Preload first to populate the cache.
func (bl *BatchLoader[T]) Get(id string) (T, bool) {
	bl.mu.RLock()
	defer bl.mu.RUnlock()
	item, ok := bl.cache[id]
	return item, ok
}

// GetOrLoad retrieves an item from cache or loads it if not cached.
// This is a convenience method for single item access but should be
// avoided in loops - use LoadBatch instead.
func (bl *BatchLoader[T]) GetOrLoad(id string) (T, error) {
	// Check cache first with read lock
	bl.mu.RLock()
	if item, ok := bl.cache[id]; ok {
		bl.mu.RUnlock()
		return item, nil
	}
	bl.mu.RUnlock()

	// Load single item (outside lock to avoid holding lock during I/O)
	items, err := bl.loadFn([]string{id})
	if err != nil {
		var zero T
		return zero, err
	}

	// Single lock cycle: update cache and retrieve result atomically
	bl.mu.Lock()
	for loadedID, item := range items {
		bl.cache[loadedID] = item
	}
	item, ok := bl.cache[id]
	bl.mu.Unlock()

	if !ok {
		var zero T
		return zero, ErrNodeNotFound
	}
	return item, nil
}

// Clear removes all cached items.
func (bl *BatchLoader[T]) Clear() {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	bl.cache = make(map[string]T)
	bl.errors = make(map[string]error)
}

// Size returns the number of cached items.
func (bl *BatchLoader[T]) Size() int {
	bl.mu.RLock()
	defer bl.mu.RUnlock()
	return len(bl.cache)
}

// CachedIDs returns a slice of all cached item IDs.
func (bl *BatchLoader[T]) CachedIDs() []string {
	bl.mu.RLock()
	defer bl.mu.RUnlock()
	ids := make([]string, 0, len(bl.cache))
	for id := range bl.cache {
		ids = append(ids, id)
	}
	return ids
}

// Invalidate removes specific items from the cache.
// Use this when you know certain items have been updated.
func (bl *BatchLoader[T]) Invalidate(ids ...string) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	for _, id := range ids {
		delete(bl.cache, id)
		delete(bl.errors, id)
	}
}
