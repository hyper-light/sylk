package hnsw

import (
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// W12.11: Tests for bounds checking in insertWithConnections and related functions.
// These tests verify that invalid indices and neighbors are handled gracefully.

// TestW12_11_InsertWithConnectionsValidLayerAccess verifies normal insertion works.
func TestW12_11_InsertWithConnectionsValidLayerAccess(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	// Insert multiple nodes
	for i := 0; i < 10; i++ {
		vec := make([]float32, 4)
		vec[i%4] = 1.0
		err := idx.Insert(boundsTestNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		if err != nil {
			t.Fatalf("W12.11: Insert failed for node %d: %v", i, err)
		}
	}

	// Verify all nodes were inserted
	if idx.Size() != 10 {
		t.Errorf("W12.11: Expected 10 nodes, got %d", idx.Size())
	}
}

// TestW12_11_IsValidLayerIndex verifies layer index validation.
func TestW12_11_IsValidLayerIndex(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	tests := []struct {
		name  string
		level int
		want  bool
	}{
		{"valid_level_0", 0, true},
		{"negative_level", -1, false},
		{"level_beyond_layers", 1000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.isValidLayerIndex(tt.level)
			if got != tt.want {
				t.Errorf("W12.11: isValidLayerIndex(%d) = %v, want %v", tt.level, got, tt.want)
			}
		})
	}
}

// TestW12_11_IsValidNeighbor verifies neighbor validation.
func TestW12_11_IsValidNeighbor(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{1, 0, 0, 0}
	idx.Insert("existing", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	existingID := idx.stringToID["existing"]

	tests := []struct {
		name       string
		neighborID uint32
		want       bool
	}{
		{"existing_neighbor", existingID, true},
		{"nonexistent_neighbor", 99999, false},
		{"invalid_node_id", invalidNodeID, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idx.isValidNeighbor(tt.neighborID)
			if got != tt.want {
				t.Errorf("W12.11: isValidNeighbor(%d) = %v, want %v", tt.neighborID, got, tt.want)
			}
		})
	}
}

// TestW12_11_GreedySearchLayerInvalidLevel verifies greedySearchLayer handles invalid levels.
func TestW12_11_GreedySearchLayerInvalidLevel(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	internalID := idx.stringToID["node1"]

	// Should not panic with invalid level
	query := []float32{1, 0, 0, 0}
	ep, dist := idx.greedySearchLayer(query, Magnitude(query), internalID, 0.5, -1)

	// Should return unchanged entry point
	if ep != internalID {
		t.Errorf("W12.11: greedySearchLayer with invalid level changed entry point")
	}
	if dist != 0.5 {
		t.Errorf("W12.11: greedySearchLayer with invalid level changed distance")
	}

	// Test with level beyond bounds
	ep, dist = idx.greedySearchLayer(query, Magnitude(query), internalID, 0.5, 1000)
	if ep != internalID {
		t.Errorf("W12.11: greedySearchLayer with out-of-bounds level changed entry point")
	}
}

// TestW12_11_SearchLayerInvalidLevel verifies searchLayer handles invalid levels.
func TestW12_11_SearchLayerInvalidLevel(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	internalID := idx.stringToID["node1"]
	query := []float32{1, 0, 0, 0}

	// Should return nil for invalid level
	results := idx.searchLayer(query, Magnitude(query), internalID, 10, -1)
	if results != nil {
		t.Errorf("W12.11: searchLayer with invalid level should return nil")
	}

	// Test with level beyond bounds
	results = idx.searchLayer(query, Magnitude(query), internalID, 10, 1000)
	if results != nil {
		t.Errorf("W12.11: searchLayer with out-of-bounds level should return nil")
	}
}

// TestW12_11_ConnectNodeInvalidNeighbor verifies connectNode skips invalid neighbors.
func TestW12_11_ConnectNodeInvalidNeighbor(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vec1 := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.Lock()
	defer idx.mu.Unlock()

	node1ID := idx.stringToID["node1"]
	// Use a new internal ID that doesn't exist in stringToID
	newNodeID := idx.nextID
	idx.nextID++

	// Create search candidates with a mix of valid and invalid neighbors
	neighbors := []searchCandidate{
		{id: node1ID, distance: 0.1},
		{id: 99999, distance: 0.2}, // nonexistent internal ID
	}

	// Should not panic and should skip invalid neighbor
	idx.connectNode(newNodeID, neighbors, 0)
}

// TestW12_11_InsertWithCorruptedLayerState verifies insertion handles corrupted state.
func TestW12_11_InsertWithCorruptedLayerState(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Corrupt the index state by setting maxLevel to an invalid value
	idx.mu.Lock()
	originalMaxLevel := idx.maxLevel
	idx.maxLevel = 1000 // Set to invalid high value
	idx.mu.Unlock()

	// Insert should handle invalid maxLevel gracefully
	vec2 := []float32{0, 1, 0, 0}
	err := idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Logf("W12.11: Insert with corrupted state returned error: %v", err)
	}

	// Restore original state for cleanup
	idx.mu.Lock()
	idx.maxLevel = originalMaxLevel
	idx.mu.Unlock()
}

// TestW12_11_ConcurrentInsertWithBoundsCheck verifies thread safety of bounds checking.
func TestW12_11_ConcurrentInsertWithBoundsCheck(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	var wg sync.WaitGroup
	numGoroutines := 10
	nodesPerGoroutine := 5

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < nodesPerGoroutine; i++ {
				vec := make([]float32, 4)
				vec[(gid+i)%4] = float32(gid + i + 1)
				nodeID := boundsTestNodeID(gid*nodesPerGoroutine + i)
				_ = idx.Insert(nodeID, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
			}
		}(g)
	}

	wg.Wait()

	// Verify index is still functional
	query := []float32{1, 0, 0, 0}
	results := idx.Search(query, 5, nil)
	if len(results) == 0 {
		t.Error("W12.11: No search results after concurrent insertions")
	}
}

// TestW12_11_SearchWithDeletedNeighbor verifies search handles deleted neighbors.
func TestW12_11_SearchWithDeletedNeighbor(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	// Insert nodes
	for i := 0; i < 5; i++ {
		vec := make([]float32, 4)
		vec[i%4] = 1.0
		idx.Insert(boundsTestNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Delete a node that may be a neighbor
	idx.Delete(boundsTestNodeID(2))

	// Search should still work
	query := []float32{1, 0, 0, 0}
	results := idx.Search(query, 5, nil)

	// Should have results (excluding deleted node)
	for _, r := range results {
		if r.ID == boundsTestNodeID(2) {
			t.Error("W12.11: Deleted node should not appear in search results")
		}
	}
}

// TestW12_11_EmptyIndexBoundsCheck verifies bounds checking on empty index.
func TestW12_11_EmptyIndexBoundsCheck(t *testing.T) {
	idx := New(DefaultConfig())

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// All layer indices should be invalid on empty index
	if idx.isValidLayerIndex(0) {
		t.Error("W12.11: Empty index should have no valid layer indices")
	}

	// All neighbors should be invalid on empty index
	if idx.isValidNeighbor(1) {
		t.Error("W12.11: Empty index should have no valid neighbors")
	}
}

// TestW12_11_InsertionOrderIndependence verifies bounds checking works regardless of order.
func TestW12_11_InsertionOrderIndependence(t *testing.T) {
	idx1 := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})
	idx2 := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vecs := [][]float32{
		{1, 0, 0, 0},
		{0, 1, 0, 0},
		{0, 0, 1, 0},
		{0, 0, 0, 1},
	}

	// Insert in forward order
	for i, vec := range vecs {
		idx1.Insert(boundsTestNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Insert in reverse order
	for i := len(vecs) - 1; i >= 0; i-- {
		idx2.Insert(boundsTestNodeID(i), vecs[i], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Both should have same size
	if idx1.Size() != idx2.Size() {
		t.Errorf("W12.11: Size mismatch: forward=%d, reverse=%d", idx1.Size(), idx2.Size())
	}

	// Both should return valid search results
	query := []float32{1, 0, 0, 0}
	r1 := idx1.Search(query, 4, nil)
	r2 := idx2.Search(query, 4, nil)

	if len(r1) == 0 || len(r2) == 0 {
		t.Error("W12.11: Search failed on one of the indices")
	}
}

// boundsTestNodeID generates a node ID for bounds check tests.
func boundsTestNodeID(i int) string {
	return "bounds_node_" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}
