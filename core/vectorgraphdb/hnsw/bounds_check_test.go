package hnsw

import (
	"math/rand"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// W4H.5: Test bounds checking for map access

// TestBoundsCheckHappyPath verifies normal operations work correctly
// with proper bounds checking in place.
func TestBoundsCheckHappyPath(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert multiple nodes
	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0, 1, 0, 0}
	vec3 := []float32{0.5, 0.5, 0, 0}

	if err := idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatalf("Insert node1 failed: %v", err)
	}
	if err := idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction); err != nil {
		t.Fatalf("Insert node2 failed: %v", err)
	}
	if err := idx.Insert("node3", vec3, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession); err != nil {
		t.Fatalf("Insert node3 failed: %v", err)
	}

	// Verify all nodes are present
	if idx.Size() != 3 {
		t.Errorf("Expected size 3, got %d", idx.Size())
	}

	// Search should work correctly
	results := idx.Search(vec1, 3, nil)
	if len(results) == 0 {
		t.Error("Search returned no results")
	}

	// First result should be node1 (exact match) or node3 (similar)
	if results[0].ID != "node1" && results[0].ID != "node3" {
		t.Errorf("Expected node1 or node3 as best match, got %s", results[0].ID)
	}
}

// TestBoundsCheckGetVectorAndMagnitudeExisting verifies helper function
// returns correct data for existing nodes.
func TestBoundsCheckGetVectorAndMagnitudeExisting(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a vector with known magnitude (3-4-5 triangle)
	idx.Insert("node1", []float32{3, 4, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	vec, mag, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if !ok {
		t.Fatal("getVectorAndMagnitude should find existing node")
	}
	if vec == nil {
		t.Fatal("Vector should not be nil")
	}
	if len(vec) != 3 {
		t.Errorf("Vector length should be 3, got %d", len(vec))
	}
	if vec[0] != 3 || vec[1] != 4 || vec[2] != 0 {
		t.Errorf("Vector values incorrect: got %v", vec)
	}
	if mag != 5.0 {
		t.Errorf("Magnitude should be 5.0, got %v", mag)
	}
}

// TestBoundsCheckGetVectorAndMagnitudeMissing verifies helper function
// returns false for non-existent nodes.
func TestBoundsCheckGetVectorAndMagnitudeMissing(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert one node
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.mu.RLock()
	vec, mag, ok := idx.getVectorAndMagnitude("nonexistent")
	idx.mu.RUnlock()

	if ok {
		t.Error("getVectorAndMagnitude should not find non-existent node")
	}
	if vec != nil {
		t.Error("Vector should be nil for non-existent node")
	}
	if mag != 0 {
		t.Error("Magnitude should be 0 for non-existent node")
	}
}

// TestBoundsCheckPartialDataVectorMissing tests scenario where vector
// is missing but magnitude might exist (simulated inconsistent state).
func TestBoundsCheckPartialDataVectorMissing(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a node normally first
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Simulate inconsistent state: remove vector but keep magnitude
	idx.mu.Lock()
	delete(idx.vectors, "node1")
	idx.mu.Unlock()

	idx.mu.RLock()
	_, _, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if ok {
		t.Error("getVectorAndMagnitude should return false when vector is missing")
	}
}

// TestBoundsCheckPartialDataMagnitudeMissing tests scenario where magnitude
// is missing but vector might exist (simulated inconsistent state).
func TestBoundsCheckPartialDataMagnitudeMissing(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a node normally first
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Simulate inconsistent state: remove magnitude but keep vector
	idx.mu.Lock()
	delete(idx.magnitudes, "node1")
	idx.mu.Unlock()

	idx.mu.RLock()
	_, _, ok := idx.getVectorAndMagnitude("node1")
	idx.mu.RUnlock()

	if ok {
		t.Error("getVectorAndMagnitude should return false when magnitude is missing")
	}
}

// TestBoundsCheckSearchLockedWithMissingEntryPoint tests that searchLocked
// handles missing entry point data gracefully.
func TestBoundsCheckSearchLockedWithMissingEntryPoint(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes normally
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Corrupt the entry point's data
	idx.mu.Lock()
	ep := idx.entryPoint
	delete(idx.vectors, ep)
	idx.mu.Unlock()

	// Search should not panic, but may return nil
	results := idx.Search([]float32{1, 0, 0, 0}, 10, nil)
	// The result depends on which node is entry point
	// Either returns nil or returns results from other nodes
	_ = results // Just verify no panic
}

// TestBoundsCheckGreedySearchWithCorruptedNeighbor tests that greedy search
// skips neighbors with missing data.
func TestBoundsCheckGreedySearchWithCorruptedNeighbor(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert several nodes to build a graph
	for i := 0; i < 10; i++ {
		vec := make([]float32, 4)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeIDForBoundsTest(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Remove data for one node that's likely a neighbor
	idx.mu.Lock()
	delete(idx.vectors, randomNodeIDForBoundsTest(5))
	delete(idx.magnitudes, randomNodeIDForBoundsTest(5))
	idx.mu.Unlock()

	// Search should still work, skipping the corrupted neighbor
	query := []float32{0.5, 0.5, 0.5, 0.5}
	results := idx.Search(query, 5, nil)
	// Should return results (possibly fewer due to corrupted node)
	// Main assertion is no panic
	if len(results) > 5 {
		t.Errorf("Results should be at most 5, got %d", len(results))
	}
}

// TestBoundsCheckInsertWithConnectionsCorruptedEntryPoint tests insertion
// when entry point data is missing.
func TestBoundsCheckInsertWithConnectionsCorruptedEntryPoint(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert first node
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Corrupt entry point data
	idx.mu.Lock()
	delete(idx.vectors, idx.entryPoint)
	delete(idx.magnitudes, idx.entryPoint)
	idx.mu.Unlock()

	// Insert another node - should not panic
	err := idx.Insert("node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Errorf("Insert should not fail: %v", err)
	}
}

// TestBoundsCheckConcurrentAccess tests that bounds checking is thread-safe.
func TestBoundsCheckConcurrentAccess(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert initial nodes
	for i := 0; i < 50; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeIDForBoundsTest(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	numGoroutines := 20

	// Concurrent searches
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				query := make([]float32, 128)
				for k := range query {
					query[k] = rand.Float32()
				}
				idx.Search(query, 5, nil)
			}
		}()
	}

	// Concurrent inserts
	for i := 50; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			vec := make([]float32, 128)
			for j := range vec {
				vec[j] = rand.Float32()
			}
			idx.Insert(randomNodeIDForBoundsTest(id), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		}(i)
	}

	// Concurrent deletes
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Delete(randomNodeIDForBoundsTest(id))
		}(i)
	}

	wg.Wait()

	// Verify index is still functional
	query := make([]float32, 128)
	for i := range query {
		query[i] = rand.Float32()
	}
	results := idx.Search(query, 10, nil)
	// Should return some results (exact count depends on timing)
	_ = results // Just verify no panic or race
}

// TestBoundsCheckSearchLayerWithMissingNeighbor tests searchLayer
// handles missing neighbor data.
func TestBoundsCheckSearchLayerWithMissingNeighbor(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node3", []float32{0.8, 0.2, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Remove data for one node
	idx.mu.Lock()
	delete(idx.vectors, "node2")
	delete(idx.magnitudes, "node2")
	idx.mu.Unlock()

	// Search should skip node2 and return other results
	results := idx.Search([]float32{1, 0, 0, 0}, 10, nil)
	// Should not panic and should return some results
	for _, r := range results {
		if r.ID == "node2" {
			t.Error("node2 should not appear in results (data was deleted)")
		}
	}
}

// TestBoundsCheckMultipleCorruptedNodes tests handling of multiple
// nodes with missing data.
func TestBoundsCheckMultipleCorruptedNodes(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert 20 nodes
	for i := 0; i < 20; i++ {
		vec := make([]float32, 4)
		vec[0] = float32(i) / 20.0
		vec[1] = 1.0 - float32(i)/20.0
		vec[2] = 0.1
		vec[3] = 0.1
		idx.Insert(randomNodeIDForBoundsTest(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Corrupt half the nodes
	idx.mu.Lock()
	for i := 0; i < 10; i++ {
		id := randomNodeIDForBoundsTest(i)
		delete(idx.vectors, id)
		delete(idx.magnitudes, id)
	}
	idx.mu.Unlock()

	// Search should still work
	query := []float32{0.5, 0.5, 0.1, 0.1}
	results := idx.Search(query, 5, nil)

	// Results should only contain non-corrupted nodes
	for _, r := range results {
		idx.mu.RLock()
		_, hasVec := idx.vectors[r.ID]
		_, hasMag := idx.magnitudes[r.ID]
		idx.mu.RUnlock()
		if !hasVec || !hasMag {
			t.Errorf("Result %s should have valid vector and magnitude", r.ID)
		}
	}
}

// TestBoundsCheckEmptyIndex tests bounds checking on empty index.
func TestBoundsCheckEmptyIndex(t *testing.T) {
	idx := New(DefaultConfig())

	// Search on empty index
	results := idx.Search([]float32{1, 0, 0}, 10, nil)
	if results != nil {
		t.Error("Search on empty index should return nil")
	}

	// getVectorAndMagnitude on empty index
	idx.mu.RLock()
	_, _, ok := idx.getVectorAndMagnitude("any")
	idx.mu.RUnlock()
	if ok {
		t.Error("getVectorAndMagnitude should return false on empty index")
	}
}

// TestBoundsCheckAfterDelete tests that deleted nodes are properly
// handled during search.
func TestBoundsCheckAfterDelete(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node3", []float32{0, 0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Delete node1
	if err := idx.Delete("node1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Search should not return deleted node
	results := idx.Search([]float32{1, 0, 0, 0}, 10, nil)
	for _, r := range results {
		if r.ID == "node1" {
			t.Error("Deleted node should not appear in results")
		}
	}
}

// TestBoundsCheckConcurrentSearchAndDelete tests thread safety of
// search during concurrent deletes.
func TestBoundsCheckConcurrentSearchAndDelete(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert 100 nodes
	for i := 0; i < 100; i++ {
		vec := make([]float32, 64)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeIDForBoundsTest(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup

	// Start concurrent searches
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				query := make([]float32, 64)
				for k := range query {
					query[k] = rand.Float32()
				}
				idx.Search(query, 10, nil)
			}
		}()
	}

	// Concurrent deletes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Delete(randomNodeIDForBoundsTest(id))
		}(i)
	}

	wg.Wait()
	// Test passes if no panic or race detected
}

// Helper function to generate node IDs for bounds check tests
func randomNodeIDForBoundsTest(i int) string {
	return "bnode" + string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
}
