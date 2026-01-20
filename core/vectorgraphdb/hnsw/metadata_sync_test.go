package hnsw

import (
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// W4P.32: Tests for metadata synchronization with layer membership

// TestMetadataSyncedCorrectly verifies metadata is properly synced during insertion.
// Happy path: metadata should be consistent after normal insertion.
func TestMetadataSyncedCorrectly(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert a node with specific metadata
	vec := []float32{1, 0, 0, 0}
	err := idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Verify metadata is correctly stored
	domain, nodeType, err := idx.GetMetadata("node1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if domain != vectorgraphdb.DomainCode {
		t.Errorf("Domain mismatch: got %v, want %v", domain, vectorgraphdb.DomainCode)
	}
	if nodeType != vectorgraphdb.NodeTypeFile {
		t.Errorf("NodeType mismatch: got %v, want %v", nodeType, vectorgraphdb.NodeTypeFile)
	}

	// Verify level is tracked
	level, err := idx.GetNodeLevel("node1")
	if err != nil {
		t.Fatalf("GetNodeLevel failed: %v", err)
	}
	if level < 0 {
		t.Errorf("Invalid level: %d", level)
	}

	// Validate consistency
	issues := idx.ValidateMetadataConsistency()
	if len(issues) > 0 {
		t.Errorf("Metadata inconsistencies found: %v", issues)
	}
}

// TestMetadataUpdatedDuringInsert verifies metadata is updated when re-inserting.
func TestMetadataUpdatedDuringInsert(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert initial node
	vec1 := []float32{1, 0, 0, 0}
	err := idx.Insert("node1", vec1, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	if err != nil {
		t.Fatalf("Initial insert failed: %v", err)
	}

	// Get initial level
	initialLevel, err := idx.GetNodeLevel("node1")
	if err != nil {
		t.Fatalf("GetNodeLevel failed: %v", err)
	}

	// Update with new metadata (same ID, different metadata)
	vec2 := []float32{0, 1, 0, 0}
	err = idx.Insert("node1", vec2, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)
	if err != nil {
		t.Fatalf("Update insert failed: %v", err)
	}

	// Verify metadata was updated
	domain, nodeType, err := idx.GetMetadata("node1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if domain != vectorgraphdb.DomainHistory {
		t.Errorf("Domain not updated: got %v, want %v", domain, vectorgraphdb.DomainHistory)
	}
	if nodeType != vectorgraphdb.NodeTypeSession {
		t.Errorf("NodeType not updated: got %v, want %v", nodeType, vectorgraphdb.NodeTypeSession)
	}

	// Verify level is preserved (not changed on update)
	updatedLevel, err := idx.GetNodeLevel("node1")
	if err != nil {
		t.Fatalf("GetNodeLevel failed after update: %v", err)
	}
	if updatedLevel != initialLevel {
		t.Errorf("Level changed on update: got %d, want %d", updatedLevel, initialLevel)
	}

	// Verify vector was updated
	vec, err := idx.GetVector("node1")
	if err != nil {
		t.Fatalf("GetVector failed: %v", err)
	}
	if vec[0] != 0 || vec[1] != 1 {
		t.Errorf("Vector not updated: got %v", vec)
	}

	// Validate consistency
	issues := idx.ValidateMetadataConsistency()
	if len(issues) > 0 {
		t.Errorf("Metadata inconsistencies found after update: %v", issues)
	}
}

// TestSearchResultsReturnCorrectMetadata verifies search returns current metadata.
func TestSearchResultsReturnCorrectMetadata(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes with different metadata
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)
	idx.Insert("node3", []float32{0.8, 0.2, 0, 0}, vectorgraphdb.DomainAcademic, vectorgraphdb.NodeTypePaper)

	// Search for similar vectors
	results := idx.Search([]float32{1, 0, 0, 0}, 3, nil)

	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}

	// Verify each result has correct metadata
	for _, r := range results {
		expectedDomain, expectedType, err := idx.GetMetadata(r.ID)
		if err != nil {
			t.Fatalf("GetMetadata failed for %s: %v", r.ID, err)
		}
		if r.Domain != expectedDomain {
			t.Errorf("Result %s: domain mismatch in search result: got %v, want %v",
				r.ID, r.Domain, expectedDomain)
		}
		if r.NodeType != expectedType {
			t.Errorf("Result %s: nodeType mismatch in search result: got %v, want %v",
				r.ID, r.NodeType, expectedType)
		}
	}
}

// TestMetadataConsistencyAfterUpdate verifies no stale metadata after updates.
func TestMetadataConsistencyAfterUpdate(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert initial node
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Update metadata multiple times
	domains := []vectorgraphdb.Domain{
		vectorgraphdb.DomainHistory,
		vectorgraphdb.DomainAcademic,
		vectorgraphdb.DomainCode,
	}
	nodeTypes := []vectorgraphdb.NodeType{
		vectorgraphdb.NodeTypeSession,
		vectorgraphdb.NodeTypePaper,
		vectorgraphdb.NodeTypeFunction,
	}

	for i, d := range domains {
		err := idx.Insert("node1", []float32{1, 0, 0, 0}, d, nodeTypes[i])
		if err != nil {
			t.Fatalf("Update %d failed: %v", i, err)
		}

		// Verify metadata is updated
		domain, nodeType, err := idx.GetMetadata("node1")
		if err != nil {
			t.Fatalf("GetMetadata failed at update %d: %v", i, err)
		}
		if domain != d {
			t.Errorf("Update %d: domain not synced: got %v, want %v", i, domain, d)
		}
		if nodeType != nodeTypes[i] {
			t.Errorf("Update %d: nodeType not synced: got %v, want %v", i, nodeType, nodeTypes[i])
		}

		// Validate consistency after each update
		issues := idx.ValidateMetadataConsistency()
		if len(issues) > 0 {
			t.Errorf("Update %d: inconsistencies found: %v", i, issues)
		}
	}
}

// TestMetadataConsistencyAfterDelete verifies metadata is cleaned up on delete.
func TestMetadataConsistencyAfterDelete(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert multiple nodes
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)
	idx.Insert("node3", []float32{0, 0, 1, 0}, vectorgraphdb.DomainAcademic, vectorgraphdb.NodeTypePaper)

	// Delete a node
	err := idx.Delete("node2")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify node is gone
	_, err = idx.GetNodeLevel("node2")
	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("Expected ErrNodeNotFound for deleted node, got: %v", err)
	}

	_, _, err = idx.GetMetadata("node2")
	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("Expected ErrNodeNotFound for deleted node metadata, got: %v", err)
	}

	// Validate consistency - should have no orphaned metadata
	issues := idx.ValidateMetadataConsistency()
	if len(issues) > 0 {
		t.Errorf("Inconsistencies found after delete: %v", issues)
	}

	// Remaining nodes should still be consistent
	if idx.Size() != 2 {
		t.Errorf("Expected 2 nodes after delete, got %d", idx.Size())
	}
}

// TestConcurrentMetadataUpdates verifies thread safety of metadata operations.
func TestConcurrentMetadataUpdates(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert initial nodes
	for i := 0; i < 10; i++ {
		vec := []float32{float32(i), float32(i + 1), 0, 0}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	numUpdates := 50

	// Concurrent updates
	for i := 0; i < numUpdates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeIdx := i % 10
			vec := []float32{float32(i), float32(i + 1), float32(i % 3), 0}
			domain := vectorgraphdb.Domain(i % 3)
			nodeType := vectorgraphdb.NodeType(i % 9)
			idx.Insert(randomNodeID(nodeIdx), vec, domain, nodeType)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numUpdates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodeIdx := i % 10
			idx.GetMetadata(randomNodeID(nodeIdx))
			idx.GetNodeLevel(randomNodeID(nodeIdx))
		}(i)
	}

	wg.Wait()

	// Validate consistency after concurrent operations
	issues := idx.ValidateMetadataConsistency()
	if len(issues) > 0 {
		t.Errorf("Inconsistencies found after concurrent operations: %v", issues)
	}
}

// TestGetNodeLevelForNonexistent verifies error handling for missing nodes.
func TestGetNodeLevelForNonexistent(t *testing.T) {
	idx := New(DefaultConfig())

	// Try to get level for nonexistent node
	_, err := idx.GetNodeLevel("nonexistent")
	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("Expected ErrNodeNotFound, got: %v", err)
	}
}

// TestNodeLevelsMapAccessor verifies GetNodeLevels returns correct copy.
func TestNodeLevelsMapAccessor(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)

	// Get copy of node levels
	idx.RLock()
	levels := idx.GetNodeLevels()
	idx.RUnlock()

	// Verify it contains our nodes
	if len(levels) != 2 {
		t.Errorf("Expected 2 node levels, got %d", len(levels))
	}

	if _, ok := levels["node1"]; !ok {
		t.Error("node1 missing from levels map")
	}
	if _, ok := levels["node2"]; !ok {
		t.Error("node2 missing from levels map")
	}

	// Verify it's a copy (modifying shouldn't affect index)
	levels["node3"] = 5
	if idx.Contains("node3") {
		t.Error("Modifying returned map should not affect index")
	}
}

// TestMetadataSyncMultipleNodes verifies sync works with many nodes.
func TestMetadataSyncMultipleNodes(t *testing.T) {
	idx := New(DefaultConfig())
	numNodes := 100

	// Insert many nodes
	for i := 0; i < numNodes; i++ {
		vec := make([]float32, 128)
		for j := range vec {
			vec[j] = float32(i+j) / float32(numNodes)
		}
		domain := vectorgraphdb.Domain(i % 10)
		nodeType := vectorgraphdb.NodeType(i % 9)
		err := idx.Insert(randomNodeID(i), vec, domain, nodeType)
		if err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}

	// Verify all nodes have consistent metadata
	issues := idx.ValidateMetadataConsistency()
	if len(issues) > 0 {
		t.Errorf("Inconsistencies found with %d nodes: %v", numNodes, issues)
	}

	// Verify size matches
	if idx.Size() != numNodes {
		t.Errorf("Size mismatch: got %d, want %d", idx.Size(), numNodes)
	}

	// Check each node has a valid level
	for i := 0; i < numNodes; i++ {
		level, err := idx.GetNodeLevel(randomNodeID(i))
		if err != nil {
			t.Errorf("Node %d: GetNodeLevel failed: %v", i, err)
		}
		if level < 0 {
			t.Errorf("Node %d: invalid level %d", i, level)
		}
	}
}

// TestSearchWithFilterReturnsCorrectMetadata verifies filtered search returns synced metadata.
func TestSearchWithFilterReturnsCorrectMetadata(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes with specific domains
	idx.Insert("code1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("code2", []float32{0.9, 0.1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	idx.Insert("history1", []float32{0.8, 0.2, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)

	// Search with domain filter
	filter := &SearchFilter{
		Domains: []vectorgraphdb.Domain{vectorgraphdb.DomainCode},
	}
	results := idx.Search([]float32{1, 0, 0, 0}, 10, filter)

	// All results should have correct domain
	for _, r := range results {
		if r.Domain != vectorgraphdb.DomainCode {
			t.Errorf("Result %s: expected DomainCode, got %v", r.ID, r.Domain)
		}

		// Verify metadata matches stored metadata
		domain, nodeType, err := idx.GetMetadata(r.ID)
		if err != nil {
			t.Fatalf("GetMetadata failed for %s: %v", r.ID, err)
		}
		if r.Domain != domain || r.NodeType != nodeType {
			t.Errorf("Result %s: metadata mismatch between search result and stored",
				r.ID)
		}
	}
}

// TestEntryPointMetadataAfterDeletion verifies metadata sync when entry point changes.
func TestEntryPointMetadataAfterDeletion(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes - first becomes entry point
	idx.Insert("ep", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)
	idx.Insert("node3", []float32{0, 0, 1, 0}, vectorgraphdb.DomainAcademic, vectorgraphdb.NodeTypePaper)

	// Delete entry point
	err := idx.Delete("ep")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Validate consistency - new entry point should have proper metadata
	issues := idx.ValidateMetadataConsistency()
	if len(issues) > 0 {
		t.Errorf("Inconsistencies after entry point deletion: %v", issues)
	}

	// Search should still work and return correct metadata
	results := idx.Search([]float32{0, 1, 0, 0}, 10, nil)
	if len(results) == 0 {
		t.Fatal("Search returned no results after entry point deletion")
	}

	for _, r := range results {
		domain, nodeType, err := idx.GetMetadata(r.ID)
		if err != nil {
			t.Errorf("GetMetadata failed for %s: %v", r.ID, err)
		}
		if r.Domain != domain || r.NodeType != nodeType {
			t.Errorf("Result %s: metadata not synced", r.ID)
		}
	}
}

// TestValidateMetadataConsistencyEmpty verifies validation works on empty index.
func TestValidateMetadataConsistencyEmpty(t *testing.T) {
	idx := New(DefaultConfig())

	issues := idx.ValidateMetadataConsistency()
	if len(issues) > 0 {
		t.Errorf("Empty index should have no inconsistencies: %v", issues)
	}
}

// TestMetadataSyncWithBatchDelete verifies metadata stays consistent with batch deletes.
func TestMetadataSyncWithBatchDelete(t *testing.T) {
	idx := New(DefaultConfig())

	// Insert nodes
	for i := 0; i < 20; i++ {
		vec := []float32{float32(i), float32(i + 1), 0, 0}
		idx.Insert(randomNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	// Batch delete half
	idsToDelete := make([]string, 10)
	for i := 0; i < 10; i++ {
		idsToDelete[i] = randomNodeID(i)
	}
	err := idx.DeleteBatch(idsToDelete)
	if err != nil {
		t.Fatalf("DeleteBatch failed: %v", err)
	}

	// Validate consistency
	issues := idx.ValidateMetadataConsistency()
	if len(issues) > 0 {
		t.Errorf("Inconsistencies after batch delete: %v", issues)
	}

	// Verify size
	if idx.Size() != 10 {
		t.Errorf("Expected 10 nodes after batch delete, got %d", idx.Size())
	}

	// Verify deleted nodes have no metadata
	for _, id := range idsToDelete {
		_, err := idx.GetNodeLevel(id)
		if !errors.Is(err, ErrNodeNotFound) {
			t.Errorf("Deleted node %s should not have level", id)
		}
	}
}
