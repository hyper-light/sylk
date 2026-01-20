package hnsw

// W4P.26: Tests for neighbor link validation after node deletion.
// These tests ensure that when a node is deleted, ALL references to it
// are cleaned up, preventing orphaned references and search failures.

import (
	"math/rand"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// TestDeleteCleansUpAllReferences verifies that deleting a node removes
// all references to it from other nodes (happy path).
func TestDeleteCleansUpAllReferences(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes that will be connected
	vec1 := []float32{1, 0, 0, 0}
	vec2 := []float32{0.9, 0.1, 0, 0} // Similar to vec1
	vec3 := []float32{0.8, 0.2, 0, 0} // Similar to vec1 and vec2
	vec4 := []float32{0.7, 0.3, 0, 0} // Similar to all above

	idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", vec2, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node3", vec3, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node4", vec4, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Delete node2
	err := idx.Delete("node2")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify node2 is gone
	if idx.Contains("node2") {
		t.Error("node2 should not exist after deletion")
	}

	// Verify no other node has node2 as a neighbor
	idx.mu.RLock()
	for _, l := range idx.layers {
		for _, nodeID := range l.allNodeIDs() {
			neighbors := l.getNeighbors(nodeID)
			for _, neighbor := range neighbors {
				if neighbor == "node2" {
					t.Errorf("node %s still has deleted node2 as neighbor", nodeID)
				}
			}
		}
	}
	idx.mu.RUnlock()
}

// TestDeleteNeighborCleanup verifies that neighbors no longer point to deleted node.
func TestDeleteNeighborCleanup(t *testing.T) {
	idx := New(Config{
		M:           4, // Small M to ensure tight clustering
		EfConstruct: 50,
		EfSearch:    50,
		LevelMult:   0.36067977499789996,
		Dimension:   4,
	})

	// Create a cluster of similar vectors
	baseVec := []float32{1, 0, 0, 0}
	for i := 0; i < 10; i++ {
		vec := []float32{
			1.0 - float32(i)*0.05,
			float32(i) * 0.05,
			0,
			0,
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Delete a middle node that likely has many connections
	targetNode := randomNodeID(5)
	err := idx.Delete(targetNode)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify no node points to the deleted node
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	for _, l := range idx.layers {
		pointing := l.findNodesPointingTo(targetNode)
		if len(pointing) > 0 {
			t.Errorf("Found %d nodes still pointing to deleted node: %v", len(pointing), pointing)
		}
	}

	// Search should work without errors
	results := idx.Search(baseVec, 10, nil)
	if len(results) == 0 {
		t.Error("Search returned no results after deletion")
	}

	// Deleted node should not appear in results
	for _, r := range results {
		if r.ID == targetNode {
			t.Errorf("Deleted node %s appeared in search results", targetNode)
		}
	}
}

// TestSearchAfterDeleteNoOrphanedRefs ensures search works correctly after deletion
// with no errors from orphaned references.
func TestSearchAfterDeleteNoOrphanedRefs(t *testing.T) {
	idx := New(Config{
		M:           16,
		EfConstruct: 100,
		EfSearch:    50,
		LevelMult:   0.36067977499789996,
		Dimension:   64,
	})

	// Insert many nodes
	numNodes := 100
	for i := 0; i < numNodes; i++ {
		vec := make([]float32, 64)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Delete half the nodes
	for i := 0; i < numNodes/2; i++ {
		err := idx.Delete(randomNodeID(i))
		if err != nil {
			t.Fatalf("Delete node %d failed: %v", i, err)
		}
	}

	// Perform many searches to stress-test for orphaned references
	for i := 0; i < 50; i++ {
		query := make([]float32, 64)
		for j := range query {
			query[j] = rand.Float32()
		}

		// Should not panic or return errors
		results := idx.Search(query, 10, nil)

		// Deleted nodes should never appear in results
		for _, r := range results {
			for j := 0; j < numNodes/2; j++ {
				if r.ID == randomNodeID(j) {
					t.Errorf("Search %d: deleted node %s appeared in results", i, r.ID)
				}
			}
		}
	}
}

// TestDeleteBatchCleansUpAllReferences verifies batch deletion cleans all references.
func TestDeleteBatchCleansUpAllReferences(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes
	for i := 0; i < 20; i++ {
		vec := make([]float32, 8)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Batch delete several nodes
	toDelete := []string{randomNodeID(3), randomNodeID(7), randomNodeID(12), randomNodeID(15)}
	err := idx.DeleteBatch(toDelete)
	if err != nil {
		t.Fatalf("DeleteBatch failed: %v", err)
	}

	// Verify no references to deleted nodes remain
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	for _, deletedID := range toDelete {
		for _, l := range idx.layers {
			pointing := l.findNodesPointingTo(deletedID)
			if len(pointing) > 0 {
				t.Errorf("Nodes %v still point to deleted node %s", pointing, deletedID)
			}
		}
	}
}

// TestFindNodesPointingTo verifies the layer helper function works correctly.
func TestFindNodesPointingTo(t *testing.T) {
	l := newLayer()

	// Create nodes
	l.addNode("node1")
	l.addNode("node2")
	l.addNode("node3")
	l.addNode("target")

	// Set up connections: node1 and node2 point to target
	l.addNeighbor("node1", "target", 0.1, 10)
	l.addNeighbor("node2", "target", 0.2, 10)
	l.addNeighbor("node3", "node1", 0.3, 10)

	// Find nodes pointing to target
	pointing := l.findNodesPointingTo("target")

	if len(pointing) != 2 {
		t.Errorf("Expected 2 nodes pointing to target, got %d: %v", len(pointing), pointing)
	}

	// Verify the correct nodes are found
	foundNode1 := false
	foundNode2 := false
	for _, id := range pointing {
		if id == "node1" {
			foundNode1 = true
		}
		if id == "node2" {
			foundNode2 = true
		}
	}

	if !foundNode1 {
		t.Error("node1 should be in pointing list")
	}
	if !foundNode2 {
		t.Error("node2 should be in pointing list")
	}

	// node3 should not point to target
	for _, id := range pointing {
		if id == "node3" {
			t.Error("node3 should not point to target")
		}
	}
}

// TestDeleteEntryPointCleansReferences ensures deleting entry point cleans references.
func TestDeleteEntryPointCleansReferences(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes
	idx.Insert("entry", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node3", []float32{0.8, 0.2, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get original entry point
	idx.mu.RLock()
	originalEntry := idx.entryPoint
	idx.mu.RUnlock()

	// Delete the entry point
	err := idx.Delete(originalEntry)
	if err != nil {
		t.Fatalf("Delete entry point failed: %v", err)
	}

	// Verify no references to deleted entry point
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	for _, l := range idx.layers {
		pointing := l.findNodesPointingTo(originalEntry)
		if len(pointing) > 0 {
			t.Errorf("Nodes %v still point to deleted entry point %s", pointing, originalEntry)
		}
	}

	// Verify new entry point is set
	if idx.entryPoint == "" || idx.entryPoint == originalEntry {
		t.Errorf("Entry point not updated after deletion: %s", idx.entryPoint)
	}
}

// TestConcurrentDeleteAndSearch ensures deletion cleanup is thread-safe.
func TestConcurrentDeleteAndSearch(t *testing.T) {
	idx := New(Config{
		M:           16,
		EfConstruct: 100,
		EfSearch:    50,
		LevelMult:   0.36067977499789996,
		Dimension:   32,
	})

	// Insert nodes
	numNodes := 100
	for i := 0; i < numNodes; i++ {
		vec := make([]float32, 32)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup

	// Concurrent deletes
	for i := 0; i < numNodes/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Delete(randomNodeID(id))
		}(i)
	}

	// Concurrent searches
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			query := make([]float32, 32)
			for j := range query {
				query[j] = rand.Float32()
			}
			// Should not panic
			idx.Search(query, 10, nil)
		}()
	}

	wg.Wait()

	// Final verification: no orphaned references
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	for i := 0; i < numNodes/2; i++ {
		deletedID := randomNodeID(i)
		if _, exists := idx.vectors[deletedID]; exists {
			continue // Node wasn't actually deleted (race condition in test setup is ok)
		}
		for _, l := range idx.layers {
			pointing := l.findNodesPointingTo(deletedID)
			if len(pointing) > 0 {
				t.Errorf("After concurrent ops: nodes %v still point to deleted %s", pointing, deletedID)
			}
		}
	}
}

// TestDeleteAllNodesNoOrphanedRefs ensures deleting all nodes leaves clean state.
func TestDeleteAllNodesNoOrphanedRefs(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes
	nodeIDs := []string{"a", "b", "c", "d", "e"}
	for i, id := range nodeIDs {
		vec := make([]float32, 4)
		for j := range vec {
			vec[j] = float32(i+1) * 0.1 * float32(j+1)
		}
		idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Delete all nodes
	for _, id := range nodeIDs {
		err := idx.Delete(id)
		if err != nil {
			t.Fatalf("Delete %s failed: %v", id, err)
		}
	}

	// Index should be empty
	if idx.Size() != 0 {
		t.Errorf("Index should be empty, has size %d", idx.Size())
	}

	// No layers should have any nodes
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	for i, l := range idx.layers {
		count := l.nodeCount()
		if count != 0 {
			t.Errorf("Layer %d should be empty, has %d nodes", i, count)
		}
	}
}

// TestDeleteAndReinsert ensures a node can be deleted and reinserted cleanly.
func TestDeleteAndReinsert(t *testing.T) {
	idx := New(DefaultConfig())

	vec := []float32{1, 0, 0, 0}

	// Insert
	err := idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Delete
	err = idx.Delete("node1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Reinsert
	err = idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Reinsert failed: %v", err)
	}

	// Should be findable
	if !idx.Contains("node1") {
		t.Error("node1 should exist after reinsert")
	}

	// Search should find it
	results := idx.Search(vec, 1, nil)
	if len(results) == 0 || results[0].ID != "node1" {
		t.Error("Search should find reinserted node1")
	}
}

// TestMultiLayerDeleteCleanup ensures deletion cleans all layers properly.
func TestMultiLayerDeleteCleanup(t *testing.T) {
	// Use high levelMult to increase chance of multi-layer nodes
	idx := New(Config{
		M:           8,
		EfConstruct: 50,
		EfSearch:    50,
		LevelMult:   1.5, // Higher value = more layers
		Dimension:   8,
	})

	// Insert enough nodes to create multiple layers
	for i := 0; i < 50; i++ {
		vec := make([]float32, 8)
		for j := range vec {
			vec[j] = rand.Float32()
		}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Get layer count before deletion
	idx.mu.RLock()
	numLayers := len(idx.layers)
	idx.mu.RUnlock()

	if numLayers <= 1 {
		t.Skip("Need multiple layers for this test")
	}

	// Delete some nodes
	for i := 10; i < 20; i++ {
		idx.Delete(randomNodeID(i))
	}

	// Verify cleanup across all layers
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	for i := 10; i < 20; i++ {
		deletedID := randomNodeID(i)
		for layerIdx, l := range idx.layers {
			pointing := l.findNodesPointingTo(deletedID)
			if len(pointing) > 0 {
				t.Errorf("Layer %d: nodes %v still point to deleted %s", layerIdx, pointing, deletedID)
			}
		}
	}
}
