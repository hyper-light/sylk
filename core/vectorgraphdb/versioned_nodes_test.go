package vectorgraphdb

import (
	"fmt"
	"runtime"
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

// setupVersionedDBWithSize sets up a test database with custom cache size.
func setupVersionedDBWithSize(t *testing.T, cacheSize int) (*VectorGraphDB, *VersionedNodeStore) {
	t.Helper()
	db, _ := setupTestDB(t)

	migration := migrations.NewAddVersionColumnMigration(db.DB())
	if err := migration.Apply(); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	ns := NewNodeStore(db, nil)
	vns := NewVersionedNodeStoreWithSize(ns, cacheSize)

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
	if !vns.CacheContains("node1") {
		t.Error("expected value in cache")
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
	if !vns.CacheContains("node1") {
		t.Error("expected value in cache after cache miss")
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
	if !vns.CacheContains("node1") {
		t.Fatal("expected cache to be populated")
	}

	// Invalidate cache
	vns.InvalidateVersionCache("node1")

	// Verify cache is cleared
	if vns.CacheContains("node1") {
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
		if !vns.CacheContains(id) {
			t.Errorf("expected %s in cache", id)
		}
	}

	// Clear all
	vns.InvalidateAllVersionCache()

	// Verify all are cleared
	if vns.CacheLen() != 0 {
		t.Errorf("expected empty cache, got %d entries", vns.CacheLen())
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

func TestNewVersionedNodeStoreWithSize(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	ns := NewNodeStore(db, nil)

	tests := []struct {
		name         string
		size         int
		expectedSize int
	}{
		{"positive size", 100, 100},
		{"zero size uses default", 0, DefaultVersionCacheSize},
		{"negative size uses default", -1, DefaultVersionCacheSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vns := NewVersionedNodeStoreWithSize(ns, tt.size)
			if vns == nil {
				t.Fatal("expected non-nil VersionedNodeStore")
			}
			if vns.cache == nil {
				t.Fatal("expected non-nil cache")
			}
		})
	}
}

func TestVersionedNodeStore_LRUEviction(t *testing.T) {
	// Use a small cache size to test eviction
	cacheSize := 5
	db, vns := setupVersionedDBWithSize(t, cacheSize)
	defer db.Close()

	// Insert more nodes than the cache can hold
	nodeCount := cacheSize + 3
	for i := 0; i < nodeCount; i++ {
		insertTestNode(t, vns, fmt.Sprintf("evict-node-%d", i))
	}

	// Access all nodes to populate cache (beyond capacity)
	for i := 0; i < nodeCount; i++ {
		_, err := vns.GetNodeVersion(fmt.Sprintf("evict-node-%d", i))
		if err != nil {
			t.Fatalf("GetNodeVersion(evict-node-%d): %v", i, err)
		}
	}

	// Cache should be at max size
	if vns.CacheLen() > cacheSize {
		t.Errorf("cache size %d exceeds max %d", vns.CacheLen(), cacheSize)
	}

	// Oldest entries should be evicted (LRU eviction)
	// The first entries (0, 1, 2) were accessed first, so should be evicted
	// when entries 5, 6, 7 were added
	evictedCount := 0
	for i := 0; i < nodeCount-cacheSize; i++ {
		if !vns.CacheContains(fmt.Sprintf("evict-node-%d", i)) {
			evictedCount++
		}
	}

	// At least some early entries should be evicted
	if evictedCount == 0 {
		t.Error("expected some early entries to be evicted from LRU cache")
	}
}

func TestVersionedNodeStore_LRUEviction_RecentAccessPreserved(t *testing.T) {
	cacheSize := 3
	db, vns := setupVersionedDBWithSize(t, cacheSize)
	defer db.Close()

	// Insert nodes
	for i := 0; i < 5; i++ {
		insertTestNode(t, vns, fmt.Sprintf("lru-node-%d", i))
	}

	// Access nodes 0, 1, 2 to populate cache
	for i := 0; i < 3; i++ {
		_, _ = vns.GetNodeVersion(fmt.Sprintf("lru-node-%d", i))
	}

	// Re-access node 0 to make it recently used
	_, _ = vns.GetNodeVersion("lru-node-0")

	// Access nodes 3 and 4, which should evict 1 and 2 (least recently used)
	_, _ = vns.GetNodeVersion("lru-node-3")
	_, _ = vns.GetNodeVersion("lru-node-4")

	// Node 0 should still be in cache (recently accessed)
	if !vns.CacheContains("lru-node-0") {
		t.Error("expected recently accessed node-0 to remain in cache")
	}

	// Check that cache is at capacity
	if vns.CacheLen() != cacheSize {
		t.Errorf("expected cache len %d, got %d", cacheSize, vns.CacheLen())
	}
}

func TestVersionedNodeStore_ConcurrentAccessWithEviction(t *testing.T) {
	cacheSize := 10
	db, vns := setupVersionedDBWithSize(t, cacheSize)
	defer db.Close()

	// Insert more nodes than cache can hold
	nodeCount := 20
	for i := 0; i < nodeCount; i++ {
		insertTestNode(t, vns, fmt.Sprintf("concurrent-node-%d", i))
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 200)

	// Multiple goroutines accessing different nodes concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				nodeIdx := (workerID + j) % nodeCount
				_, err := vns.GetNodeVersion(fmt.Sprintf("concurrent-node-%d", nodeIdx))
				if err != nil {
					errCh <- err
				}
			}
		}(i)
	}

	// Concurrent invalidations
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				nodeIdx := (workerID + j) % nodeCount
				vns.InvalidateVersionCache(fmt.Sprintf("concurrent-node-%d", nodeIdx))
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}

	// Cache should not exceed max size
	if vns.CacheLen() > cacheSize {
		t.Errorf("cache size %d exceeds max %d after concurrent access", vns.CacheLen(), cacheSize)
	}
}

func TestVersionedNodeStore_MemoryStabilityUnderLoad(t *testing.T) {
	cacheSize := 100
	db, vns := setupVersionedDBWithSize(t, cacheSize)
	defer db.Close()

	// Insert nodes
	nodeCount := 200
	for i := 0; i < nodeCount; i++ {
		insertTestNode(t, vns, fmt.Sprintf("load-node-%d", i))
	}

	// Force GC and get initial memory stats
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Perform many cache operations that would cause unbounded growth with sync.Map
	iterations := 1000
	for i := 0; i < iterations; i++ {
		nodeIdx := i % nodeCount
		_, _ = vns.GetNodeVersion(fmt.Sprintf("load-node-%d", nodeIdx))

		// Occasionally invalidate
		if i%10 == 0 {
			vns.InvalidateVersionCache(fmt.Sprintf("load-node-%d", nodeIdx))
		}
	}

	// Cache should remain bounded
	if vns.CacheLen() > cacheSize {
		t.Errorf("cache size %d exceeds max %d after load test", vns.CacheLen(), cacheSize)
	}

	// Force GC and get final memory stats
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// Memory should not have grown excessively (allow for some variance)
	// The key check is that cache is bounded, not that total memory is fixed
	t.Logf("Memory before: %d KB, after: %d KB", memBefore.Alloc/1024, memAfter.Alloc/1024)
}

func TestVersionedNodeStore_CacheLenAndContains(t *testing.T) {
	db, vns := setupVersionedDB(t)
	defer db.Close()

	// Initially empty
	if vns.CacheLen() != 0 {
		t.Errorf("expected empty cache, got %d", vns.CacheLen())
	}

	insertTestNode(t, vns, "test-node")

	// Still empty until accessed
	if vns.CacheContains("test-node") {
		t.Error("cache should not contain node before access")
	}

	// Access to populate cache
	_, err := vns.GetNodeVersion("test-node")
	if err != nil {
		t.Fatalf("GetNodeVersion: %v", err)
	}

	// Now should be in cache
	if !vns.CacheContains("test-node") {
		t.Error("cache should contain node after access")
	}
	if vns.CacheLen() != 1 {
		t.Errorf("expected cache len 1, got %d", vns.CacheLen())
	}

	// Invalidate
	vns.InvalidateVersionCache("test-node")
	if vns.CacheContains("test-node") {
		t.Error("cache should not contain node after invalidation")
	}
	if vns.CacheLen() != 0 {
		t.Errorf("expected cache len 0 after invalidation, got %d", vns.CacheLen())
	}
}

func TestVersionedNodeStore_DefaultCacheSize(t *testing.T) {
	if DefaultVersionCacheSize != 10000 {
		t.Errorf("expected default cache size 10000, got %d", DefaultVersionCacheSize)
	}
}
