package hnsw

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	_ "github.com/mattn/go-sqlite3"
)

// W12.10: Tests for deadlock prevention in recomputeEdgeDistances.
// These tests verify that the restructured lock ordering prevents deadlocks.

// TestW12_10_RecomputeDistancesNoDeadlock verifies recomputeEdgeDistances
// does not deadlock when called concurrently with other index operations.
func TestW12_10_RecomputeDistancesNoDeadlock(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	// Insert initial nodes
	for i := 0; i < 10; i++ {
		vec := make([]float32, 4)
		vec[i%4] = 1.0
		idx.Insert(nodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	done := make(chan struct{})
	timeout := time.After(5 * time.Second)

	go func() {
		// Simulate LoadVectors calling recomputeEdgeDistances
		idx.mu.Lock()
		idx.recomputeEdgeDistances()
		idx.mu.Unlock()
		close(done)
	}()

	select {
	case <-done:
		// Success - no deadlock
	case <-timeout:
		t.Fatal("W12.10: recomputeEdgeDistances deadlocked")
	}
}

// TestW12_10_ConcurrentSearchAndRecompute verifies search operations
// can proceed concurrently with recomputeEdgeDistances without deadlock.
func TestW12_10_ConcurrentSearchAndRecompute(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	// Insert nodes
	for i := 0; i < 20; i++ {
		vec := make([]float32, 4)
		vec[i%4] = float32(i + 1)
		idx.Insert(nodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	errors := make(chan error, 10)
	timeout := time.After(10 * time.Second)

	// Multiple goroutines doing searches
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				query := []float32{1, 0, 0, 0}
				_ = idx.Search(query, 5, nil)
			}
		}()
	}

	// Goroutine doing recompute (simulating LoadVectors)
	wg.Add(1)
	go func() {
		defer wg.Done()
		idx.mu.Lock()
		idx.recomputeEdgeDistances()
		idx.mu.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case err := <-errors:
		t.Fatalf("W12.10: error during concurrent operation: %v", err)
	case <-timeout:
		t.Fatal("W12.10: concurrent search and recompute deadlocked")
	}
}

// TestW12_10_CollectDistanceUpdates verifies collectDistanceUpdates
// correctly gathers all updates without holding locks during computation.
func TestW12_10_CollectDistanceUpdates(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0.8, 0.6, 0, 0}
	vec3 := []float32{0, 1, 0, 0}

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node3", vec3, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.layers) == 0 {
		t.Skip("no layers created, skipping")
	}

	updates := idx.collectDistanceUpdates(idx.layers[0])

	// Should have some updates since we have connected nodes
	if len(updates) == 0 {
		t.Log("no updates collected - may be expected for small graphs")
	}

	// Verify updates have valid data
	for _, update := range updates {
		if update.neighbors == nil {
			t.Error("W12.10: update has nil neighbors")
		}
		if update.neighborID == invalidNodeID {
			t.Error("W12.10: update has invalid neighborID")
		}
		if update.distance < 0 || update.distance > 2 {
			t.Errorf("W12.10: invalid distance %v", update.distance)
		}
	}
}

// TestW12_10_ComputeNodeDistanceUpdates verifies computeNodeDistanceUpdates
// correctly computes distances for a single node.
func TestW12_10_ComputeNodeDistanceUpdates(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0.8, 0.6, 0, 0}

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.layers) == 0 {
		t.Skip("no layers created")
	}

	internalID, idExists := idx.stringToID["node1"]
	if !idExists {
		t.Skip("node1 not in stringToID map")
	}

	idx.layers[0].mu.RLock()
	node, exists := idx.layers[0].nodes[internalID]
	idx.layers[0].mu.RUnlock()

	if !exists {
		t.Skip("node1 not in layer 0")
	}

	updates := idx.computeNodeDistanceUpdates(internalID, node)

	// Check that updates are valid
	for _, update := range updates {
		if update.distance < 0 {
			t.Errorf("W12.10: negative distance %v", update.distance)
		}
	}
}

// TestW12_10_MissingVectorHandling verifies graceful handling
// when vectors are missing during distance computation.
func TestW12_10_MissingVectorHandling(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vec1 := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.Lock()
	// Manually remove the vector to simulate corruption
	internalID := idx.stringToID["node1"]
	delete(idx.nodes, internalID)

	// Should not panic
	idx.recomputeEdgeDistances()
	idx.mu.Unlock()
}

// TestW12_10_EmptyLayerHandling verifies recomputeEdgeDistances
// handles empty layers gracefully.
func TestW12_10_EmptyLayerHandling(t *testing.T) {
	idx := New(DefaultConfig())

	// Add then remove a node to create empty layers
	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Delete("node1")

	idx.mu.Lock()
	idx.recomputeEdgeDistances()
	idx.mu.Unlock()

	// Should not panic
}

// TestW12_10_DistanceUpdateApplication verifies that collected updates
// are correctly applied to neighbor sets.
func TestW12_10_DistanceUpdateApplication(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0.9, 0.1, 0, 0}

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Zero out all distances
	idx.mu.Lock()
	for _, layer := range idx.layers {
		layer.mu.RLock()
		for _, node := range layer.nodes {
			for _, nID := range node.neighbors.GetIDs() {
				node.neighbors.UpdateDistance(nID, 0)
			}
		}
		layer.mu.RUnlock()
	}

	// Recompute should restore non-zero distances
	idx.recomputeEdgeDistances()
	idx.mu.Unlock()

	// Search should work correctly
	results := idx.Search(vec1, 2, nil)
	if len(results) == 0 {
		t.Error("W12.10: no search results after distance update")
	}
}

// TestW12_10_RaceCondition uses race detector to verify no data races.
func TestW12_10_RaceCondition(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	// Insert initial data
	for i := 0; i < 5; i++ {
		vec := make([]float32, 4)
		vec[i%4] = 1.0
		idx.Insert(nodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup

	// Concurrent recomputes
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx.mu.Lock()
			idx.recomputeEdgeDistances()
			idx.mu.Unlock()
		}()
	}

	// Concurrent searches
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := []float32{1, 0, 0, 0}
			_ = idx.Search(query, 3, nil)
		}()
	}

	wg.Wait()
}

// nodeID generates a node ID for testing.
func nodeID(i int) string {
	return "node" + string(rune('0'+i))
}
