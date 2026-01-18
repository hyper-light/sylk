package vectorgraphdb

import (
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb/migrations"
)

// setupVersionedDB sets up a test database with the version column migration applied.
func setupVersionedDB(t *testing.T) (*VectorGraphDB, *VersionedNodeStore) {
	t.Helper()
	db, _ := setupTestDB(t)

	// Apply the version column migration
	migration := migrations.NewAddVersionColumnMigration(db.DB())
	if err := migration.Apply(); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	ns := NewNodeStore(db, nil)
	vns := NewVersionedNodeStore(ns)

	return db, vns
}

// insertTestNode inserts a test node and returns it.
func insertTestNode(t *testing.T, vns *VersionedNodeStore, id string) *GraphNode {
	t.Helper()
	node := &GraphNode{
		ID:       id,
		Domain:   DomainCode,
		NodeType: NodeTypeFile,
		Name:     id,
		Metadata: map[string]any{"test": "data"},
	}
	embedding := []float32{1, 0, 0, 0}

	if err := vns.InsertNode(node, embedding); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	return node
}

func TestVersionedNodeStore_GetNodeWithVersion(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	insertTestNode(t, vns, "node1")

	node, err := vns.GetNodeWithVersion("node1")
	if err != nil {
		t.Fatalf("GetNodeWithVersion: %v", err)
	}

	if node.ID != "node1" {
		t.Errorf("expected ID 'node1', got %q", node.ID)
	}
	if node.Version != 1 {
		t.Errorf("expected version 1 (default), got %d", node.Version)
	}
}

func TestVersionedNodeStore_GetNodeWithVersion_NotFound(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	_, err := vns.GetNodeWithVersion("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestVersionedNodeStore_GetNodeVersion_CacheHit(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	insertTestNode(t, vns, "node1")

	// First call populates cache via GetNodeWithVersion
	_, err := vns.GetNodeWithVersion("node1")
	if err != nil {
		t.Fatalf("GetNodeWithVersion: %v", err)
	}

	// Second call should hit cache
	version, err := vns.GetNodeVersion("node1")
	if err != nil {
		t.Fatalf("GetNodeVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("expected version 1, got %d", version)
	}

	// Verify cache contains the value
	cached, ok := vns.versionCache.Load("node1")
	if !ok {
		t.Error("expected value in cache")
	}
	if cached.(uint64) != 1 {
		t.Errorf("expected cached version 1, got %v", cached)
	}
}

func TestVersionedNodeStore_GetNodeVersion_CacheMiss(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	insertTestNode(t, vns, "node1")

	// Call GetNodeVersion directly (cache miss)
	version, err := vns.GetNodeVersion("node1")
	if err != nil {
		t.Fatalf("GetNodeVersion: %v", err)
	}
	if version != 1 {
		t.Errorf("expected version 1, got %d", version)
	}

	// Verify cache is now populated
	cached, ok := vns.versionCache.Load("node1")
	if !ok {
		t.Error("expected value in cache after cache miss")
	}
	if cached.(uint64) != 1 {
		t.Errorf("expected cached version 1, got %v", cached)
	}
}

func TestVersionedNodeStore_GetNodeVersion_NotFound(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	_, err := vns.GetNodeVersion("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestVersionedNodeStore_InvalidateVersionCache(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	insertTestNode(t, vns, "node1")

	// Populate cache
	_, err := vns.GetNodeWithVersion("node1")
	if err != nil {
		t.Fatalf("GetNodeWithVersion: %v", err)
	}

	// Verify cache is populated
	if _, ok := vns.versionCache.Load("node1"); !ok {
		t.Fatal("expected cache to be populated")
	}

	// Invalidate cache
	vns.InvalidateVersionCache("node1")

	// Verify cache is cleared
	if _, ok := vns.versionCache.Load("node1"); ok {
		t.Error("expected cache to be cleared after invalidation")
	}
}

func TestVersionedNodeStore_InvalidateVersionCache_NonExistent(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	// Should not panic when invalidating non-existent key
	vns.InvalidateVersionCache("nonexistent")
}

func TestVersionedNodeStore_InvalidateAllVersionCache(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	insertTestNode(t, vns, "node1")
	insertTestNode(t, vns, "node2")
	insertTestNode(t, vns, "node3")

	// Populate cache for all nodes
	for _, id := range []string{"node1", "node2", "node3"} {
		_, err := vns.GetNodeWithVersion(id)
		if err != nil {
			t.Fatalf("GetNodeWithVersion(%s): %v", id, err)
		}
	}

	// Verify all are cached
	for _, id := range []string{"node1", "node2", "node3"} {
		if _, ok := vns.versionCache.Load(id); !ok {
			t.Errorf("expected %s in cache", id)
		}
	}

	// Clear all
	vns.InvalidateAllVersionCache()

	// Verify all are cleared
	cacheCount := 0
	vns.versionCache.Range(func(key, value any) bool {
		cacheCount++
		return true
	})
	if cacheCount != 0 {
		t.Errorf("expected empty cache, got %d entries", cacheCount)
	}
}

func TestVersionedNodeStore_ConcurrentAccess(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	// Create multiple nodes
	for i := 0; i < 10; i++ {
		insertTestNode(t, vns, nodeID(i))
	}

	// Concurrently access versions
	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	for i := 0; i < 10; i++ {
		wg.Add(3)

		// Concurrent GetNodeWithVersion
		go func(idx int) {
			defer wg.Done()
			_, err := vns.GetNodeWithVersion(nodeID(idx))
			if err != nil {
				errCh <- err
			}
		}(i)

		// Concurrent GetNodeVersion
		go func(idx int) {
			defer wg.Done()
			_, err := vns.GetNodeVersion(nodeID(idx))
			if err != nil {
				errCh <- err
			}
		}(i)

		// Concurrent invalidation
		go func(idx int) {
			defer wg.Done()
			vns.InvalidateVersionCache(nodeID(idx))
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestVersionedNodeStore_ConcurrentReadWrite(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	insertTestNode(t, vns, "shared-node")

	var wg sync.WaitGroup
	iterations := 50

	// Multiple readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _ = vns.GetNodeVersion("shared-node")
			}
		}()
	}

	// Multiple invalidators
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				vns.InvalidateVersionCache("shared-node")
			}
		}()
	}

	wg.Wait()
}

func TestVersionedNodeStore_VersionAfterCacheInvalidation(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	insertTestNode(t, vns, "node1")

	// Get and cache version
	version1, err := vns.GetNodeVersion("node1")
	if err != nil {
		t.Fatalf("first GetNodeVersion: %v", err)
	}

	// Invalidate cache
	vns.InvalidateVersionCache("node1")

	// Get version again (should fetch from DB)
	version2, err := vns.GetNodeVersion("node1")
	if err != nil {
		t.Fatalf("second GetNodeVersion: %v", err)
	}

	if version1 != version2 {
		t.Errorf("versions should match: %d != %d", version1, version2)
	}
}

func TestNewVersionedNodeStore(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	ns := NewNodeStore(db, nil)
	vns := NewVersionedNodeStore(ns)

	if vns == nil {
		t.Fatal("expected non-nil VersionedNodeStore")
	}
	if vns.NodeStore != ns {
		t.Error("expected embedded NodeStore to match")
	}
}
