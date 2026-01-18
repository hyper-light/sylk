package vectorgraphdb

import (
	"database/sql"
	"fmt"
	"sync"
)

// VersionedNodeStore wraps NodeStore with optimistic concurrency control.
// It maintains a version cache for fast validation of node versions.
type VersionedNodeStore struct {
	*NodeStore
	versionCache sync.Map // nodeID -> uint64 (cached for fast validation)
}

// NewVersionedNodeStore creates a new VersionedNodeStore wrapping the given NodeStore.
func NewVersionedNodeStore(ns *NodeStore) *VersionedNodeStore {
	return &VersionedNodeStore{
		NodeStore: ns,
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
func (store *VersionedNodeStore) fetchAndCacheVersion(nodeID string) (uint64, error) {
	var version uint64
	err := store.db.db.QueryRow("SELECT version FROM nodes WHERE id = ?", nodeID).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("get version: %w", err)
	}

	store.versionCache.Store(nodeID, version)
	return version, nil
}

// GetNodeVersion returns just the version for fast validation.
// It checks the cache first before querying the database.
func (store *VersionedNodeStore) GetNodeVersion(nodeID string) (uint64, error) {
	if cached, ok := store.versionCache.Load(nodeID); ok {
		return cached.(uint64), nil
	}

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

	store.versionCache.Store(nodeID, version)
	return version, nil
}

// InvalidateVersionCache removes a node's version from the cache.
// This should be called after any operation that modifies a node's version.
func (store *VersionedNodeStore) InvalidateVersionCache(nodeID string) {
	store.versionCache.Delete(nodeID)
}

// InvalidateAllVersionCache clears the entire version cache.
// This is useful for bulk operations or when cache consistency is uncertain.
func (store *VersionedNodeStore) InvalidateAllVersionCache() {
	store.versionCache.Range(func(key, value any) bool {
		store.versionCache.Delete(key)
		return true
	})
}
