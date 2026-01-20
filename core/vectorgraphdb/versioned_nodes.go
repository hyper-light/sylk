package vectorgraphdb

import (
	"database/sql"
	"fmt"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

// DefaultVersionCacheSize is the default maximum number of entries in the version cache.
const DefaultVersionCacheSize = 10000

// VersionedNodeStore wraps NodeStore with optimistic concurrency control.
// It maintains an LRU version cache for fast validation of node versions
// with bounded memory usage.
type VersionedNodeStore struct {
	*NodeStore
	cache *lru.Cache[string, uint64]
	mu    sync.RWMutex
}

// NewVersionedNodeStore creates a new VersionedNodeStore wrapping the given NodeStore.
// Uses DefaultVersionCacheSize for the cache size.
func NewVersionedNodeStore(ns *NodeStore) *VersionedNodeStore {
	return NewVersionedNodeStoreWithSize(ns, DefaultVersionCacheSize)
}

// NewVersionedNodeStoreWithSize creates a new VersionedNodeStore with a custom cache size.
// If maxCacheSize <= 0, DefaultVersionCacheSize is used.
func NewVersionedNodeStoreWithSize(ns *NodeStore, maxCacheSize int) *VersionedNodeStore {
	if maxCacheSize <= 0 {
		maxCacheSize = DefaultVersionCacheSize
	}
	cache, _ := lru.New[string, uint64](maxCacheSize)
	return &VersionedNodeStore{
		NodeStore: ns,
		cache:     cache,
	}
}

// GetNodeWithVersion returns a node with its version field populated.
// The version is fetched from the database and cached for future lookups.
func (store *VersionedNodeStore) GetNodeWithVersion(nodeID string) (*GraphNode, error) {
	node, err := store.GetNode(nodeID)
	if err != nil {
		return nil, err
	}

	version, err := store.fetchAndCacheVersion(nodeID)
	if err != nil {
		return nil, err
	}

	node.Version = version
	return node, nil
}

// fetchAndCacheVersion fetches the version from DB and caches it.
// Uses mutex to ensure thread-safe cache operations.
func (store *VersionedNodeStore) fetchAndCacheVersion(nodeID string) (uint64, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	if version, ok := store.cache.Get(nodeID); ok {
		return version, nil
	}

	var version uint64
	err := store.db.db.QueryRow("SELECT version FROM nodes WHERE id = ?", nodeID).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("get version: %w", err)
	}

	store.cache.Add(nodeID, version)
	return version, nil
}

// GetNodeVersion returns just the version for fast validation.
// It checks the cache first before querying the database.
func (store *VersionedNodeStore) GetNodeVersion(nodeID string) (uint64, error) {
	store.mu.Lock()
	if version, ok := store.cache.Get(nodeID); ok {
		store.mu.Unlock()
		return version, nil
	}
	store.mu.Unlock()

	return store.fetchVersionFromDB(nodeID)
}

// fetchVersionFromDB fetches the version directly from the database.
func (store *VersionedNodeStore) fetchVersionFromDB(nodeID string) (uint64, error) {
	var version uint64
	err := store.db.db.QueryRow("SELECT version FROM nodes WHERE id = ?", nodeID).Scan(&version)
	if err == sql.ErrNoRows {
		return 0, ErrNodeNotFound
	}
	if err != nil {
		return 0, err
	}

	store.mu.Lock()
	store.cache.Add(nodeID, version)
	store.mu.Unlock()
	return version, nil
}

// InvalidateVersionCache removes a node's version from the cache.
// This should be called after any operation that modifies a node's version.
func (store *VersionedNodeStore) InvalidateVersionCache(nodeID string) {
	store.mu.Lock()
	store.cache.Remove(nodeID)
	store.mu.Unlock()
}

// InvalidateAllVersionCache clears the entire version cache.
// This is useful for bulk operations or when cache consistency is uncertain.
func (store *VersionedNodeStore) InvalidateAllVersionCache() {
	store.mu.Lock()
	store.cache.Purge()
	store.mu.Unlock()
}

// CacheLen returns the current number of entries in the version cache.
// This is primarily useful for testing.
func (store *VersionedNodeStore) CacheLen() int {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.cache.Len()
}

// CacheContains checks if a node ID is in the cache.
// This is primarily useful for testing.
func (store *VersionedNodeStore) CacheContains(nodeID string) bool {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.cache.Contains(nodeID)
}
