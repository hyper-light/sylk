package hnsw

import (
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// W4N.13: Tests for Get* methods returning copies to protect internal state

// =============================================================================
// Happy Path Tests: Verify Get methods return usable copies
// =============================================================================

func TestW4N13_GetVectors_ReturnsUsableCopy(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)

	idx.RLock()
	vectors := idx.GetVectors()
	idx.RUnlock()

	// Verify we got the expected data
	if len(vectors) != 2 {
		t.Errorf("Expected 2 vectors, got %d", len(vectors))
	}

	if vec, ok := vectors["node1"]; !ok || len(vec) != 3 {
		t.Errorf("Expected node1 vector with 3 elements, got %v", vec)
	}

	if vec, ok := vectors["node2"]; !ok || len(vec) != 3 {
		t.Errorf("Expected node2 vector with 3 elements, got %v", vec)
	}
}

func TestW4N13_GetMagnitudes_ReturnsUsableCopy(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{3, 4, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)

	idx.RLock()
	magnitudes := idx.GetMagnitudes()
	idx.RUnlock()

	if len(magnitudes) != 2 {
		t.Errorf("Expected 2 magnitudes, got %d", len(magnitudes))
	}

	if mag, ok := magnitudes["node1"]; !ok || mag != 5.0 {
		t.Errorf("Expected node1 magnitude 5.0, got %v", mag)
	}

	if mag, ok := magnitudes["node2"]; !ok || mag != 1.0 {
		t.Errorf("Expected node2 magnitude 1.0, got %v", mag)
	}
}

func TestW4N13_GetDomains_ReturnsUsableCopy(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)

	idx.RLock()
	domains := idx.GetDomains()
	idx.RUnlock()

	if len(domains) != 2 {
		t.Errorf("Expected 2 domains, got %d", len(domains))
	}

	if domain, ok := domains["node1"]; !ok || domain != vectorgraphdb.DomainCode {
		t.Errorf("Expected node1 domain DomainCode, got %v", domain)
	}

	if domain, ok := domains["node2"]; !ok || domain != vectorgraphdb.DomainHistory {
		t.Errorf("Expected node2 domain DomainHistory, got %v", domain)
	}
}

func TestW4N13_GetNodeTypes_ReturnsUsableCopy(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)

	idx.RLock()
	nodeTypes := idx.GetNodeTypes()
	idx.RUnlock()

	if len(nodeTypes) != 2 {
		t.Errorf("Expected 2 node types, got %d", len(nodeTypes))
	}

	if nt, ok := nodeTypes["node1"]; !ok || nt != vectorgraphdb.NodeTypeFile {
		t.Errorf("Expected node1 type NodeTypeFile, got %v", nt)
	}

	if nt, ok := nodeTypes["node2"]; !ok || nt != vectorgraphdb.NodeTypeFunction {
		t.Errorf("Expected node2 type NodeTypeFunction, got %v", nt)
	}
}

func TestW4N13_GetLayers_ReturnsUsableCopy(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	idx.RLock()
	layers := idx.GetLayers()
	idx.RUnlock()

	// Layers should be non-nil and contain at least one layer (layer 0)
	if layers == nil {
		t.Error("Expected non-nil layers slice")
	}

	if len(layers) == 0 {
		t.Error("Expected at least one layer")
	}
}

// =============================================================================
// Negative Path Tests: Modifying returned map doesn't affect internal state
// =============================================================================

func TestW4N13_GetVectors_ModifyingCopyDoesNotAffectInternal(t *testing.T) {
	idx := New(DefaultConfig())
	originalVec := []float32{1, 2, 3}
	idx.Insert("node1", originalVec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get a copy
	idx.RLock()
	vectors := idx.GetVectors()
	idx.RUnlock()

	// Modify the returned map
	vectors["node1"][0] = 999.0
	vectors["new_node"] = []float32{4, 5, 6}
	delete(vectors, "node1")

	// Verify internal state is unchanged
	internalVec, err := idx.GetVector("node1")
	if err != nil {
		t.Fatalf("GetVector failed: %v", err)
	}

	if internalVec[0] != 1.0 {
		t.Errorf("Internal vector was modified: expected 1.0, got %v", internalVec[0])
	}

	// Verify new_node was not added internally
	if idx.Contains("new_node") {
		t.Error("new_node should not exist in internal state")
	}
}

func TestW4N13_GetMagnitudes_ModifyingCopyDoesNotAffectInternal(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{3, 4, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get a copy
	idx.RLock()
	magnitudes := idx.GetMagnitudes()
	idx.RUnlock()

	// Modify the returned map
	originalMag := magnitudes["node1"]
	magnitudes["node1"] = 999.0
	magnitudes["new_node"] = 123.0
	delete(magnitudes, "node1")

	// Verify internal state is unchanged
	idx.RLock()
	internalMagnitudes := idx.GetMagnitudes()
	idx.RUnlock()

	if mag, ok := internalMagnitudes["node1"]; !ok || mag != originalMag {
		t.Errorf("Internal magnitude was modified: expected %v, got %v", originalMag, mag)
	}

	if _, ok := internalMagnitudes["new_node"]; ok {
		t.Error("new_node should not exist in internal magnitudes")
	}
}

func TestW4N13_GetDomains_ModifyingCopyDoesNotAffectInternal(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get a copy
	idx.RLock()
	domains := idx.GetDomains()
	idx.RUnlock()

	// Modify the returned map
	domains["node1"] = vectorgraphdb.DomainHistory
	domains["new_node"] = vectorgraphdb.DomainAcademic

	// Verify internal state is unchanged
	domain, _, err := idx.GetMetadata("node1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}

	if domain != vectorgraphdb.DomainCode {
		t.Errorf("Internal domain was modified: expected DomainCode, got %v", domain)
	}
}

func TestW4N13_GetNodeTypes_ModifyingCopyDoesNotAffectInternal(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get a copy
	idx.RLock()
	nodeTypes := idx.GetNodeTypes()
	idx.RUnlock()

	// Modify the returned map
	nodeTypes["node1"] = vectorgraphdb.NodeTypeFunction
	nodeTypes["new_node"] = vectorgraphdb.NodeTypeMethod

	// Verify internal state is unchanged
	_, nodeType, err := idx.GetMetadata("node1")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}

	if nodeType != vectorgraphdb.NodeTypeFile {
		t.Errorf("Internal node type was modified: expected NodeTypeFile, got %v", nodeType)
	}
}

func TestW4N13_GetLayers_ModifyingCopyDoesNotAffectInternal(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get a copy
	idx.RLock()
	layers := idx.GetLayers()
	originalLen := len(layers)
	idx.RUnlock()

	// Modify the returned slice
	layers = append(layers, newLayer())
	layers[0] = nil

	// Verify internal state is unchanged
	idx.RLock()
	internalLayers := idx.GetLayers()
	idx.RUnlock()

	if len(internalLayers) != originalLen {
		t.Errorf("Internal layers length changed: expected %d, got %d", originalLen, len(internalLayers))
	}

	if internalLayers[0] == nil {
		t.Error("Internal layer 0 should not be nil")
	}
}

func TestW4N13_GetVectors_DeepCopyOfVectorSlices(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 2, 3}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get a copy
	idx.RLock()
	vectors := idx.GetVectors()
	idx.RUnlock()

	// Verify that the vector slice itself is a copy
	vectors["node1"][0] = 999.0

	// Get internal vector
	internalVec, _ := idx.GetVector("node1")
	if internalVec[0] == 999.0 {
		t.Error("Internal vector slice was modified - GetVectors did not deep copy vectors")
	}
}

// =============================================================================
// Failure Path Tests: Empty maps handled correctly
// =============================================================================

func TestW4N13_GetVectors_EmptyIndex(t *testing.T) {
	idx := New(DefaultConfig())

	idx.RLock()
	vectors := idx.GetVectors()
	idx.RUnlock()

	if vectors == nil {
		t.Error("Expected non-nil empty map, got nil")
	}

	if len(vectors) != 0 {
		t.Errorf("Expected empty map, got %d elements", len(vectors))
	}
}

func TestW4N13_GetMagnitudes_EmptyIndex(t *testing.T) {
	idx := New(DefaultConfig())

	idx.RLock()
	magnitudes := idx.GetMagnitudes()
	idx.RUnlock()

	if magnitudes == nil {
		t.Error("Expected non-nil empty map, got nil")
	}

	if len(magnitudes) != 0 {
		t.Errorf("Expected empty map, got %d elements", len(magnitudes))
	}
}

func TestW4N13_GetDomains_EmptyIndex(t *testing.T) {
	idx := New(DefaultConfig())

	idx.RLock()
	domains := idx.GetDomains()
	idx.RUnlock()

	if domains == nil {
		t.Error("Expected non-nil empty map, got nil")
	}

	if len(domains) != 0 {
		t.Errorf("Expected empty map, got %d elements", len(domains))
	}
}

func TestW4N13_GetNodeTypes_EmptyIndex(t *testing.T) {
	idx := New(DefaultConfig())

	idx.RLock()
	nodeTypes := idx.GetNodeTypes()
	idx.RUnlock()

	if nodeTypes == nil {
		t.Error("Expected non-nil empty map, got nil")
	}

	if len(nodeTypes) != 0 {
		t.Errorf("Expected empty map, got %d elements", len(nodeTypes))
	}
}

func TestW4N13_GetLayers_EmptyIndex(t *testing.T) {
	idx := New(DefaultConfig())

	idx.RLock()
	layers := idx.GetLayers()
	idx.RUnlock()

	// Even an empty index should return a non-nil slice
	if layers == nil {
		t.Error("Expected non-nil empty slice, got nil")
	}

	// An empty index may have 0 layers
	if len(layers) != 0 {
		t.Errorf("Expected 0 layers for empty index, got %d", len(layers))
	}
}

// =============================================================================
// Race Condition Tests: Concurrent get and write operations
// =============================================================================

func TestW4N13_GetVectors_ConcurrentWithInsert(t *testing.T) {
	idx := New(DefaultConfig())

	// Pre-populate with some data
	for i := 0; i < 10; i++ {
		idx.Insert(randomNodeID(i), []float32{float32(i), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	numGoroutines := 50

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx.RLock()
			vectors := idx.GetVectors()
			idx.RUnlock()
			// Use the copy (modify it to ensure it's truly a copy)
			for id, vec := range vectors {
				if len(vec) > 0 {
					_ = id
					vec[0] = 999.0
				}
			}
		}()
	}

	// Concurrent writes
	for i := 10; i < 10+numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Insert(randomNodeID(id), []float32{float32(id), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		}(i)
	}

	wg.Wait()

	// Verify original data is intact
	vec, err := idx.GetVector("node000")
	if err != nil {
		t.Fatalf("Original node missing: %v", err)
	}
	if vec[0] != 0.0 {
		t.Errorf("Original vector was corrupted: expected 0.0, got %v", vec[0])
	}
}

func TestW4N13_GetMagnitudes_ConcurrentWithInsert(t *testing.T) {
	idx := New(DefaultConfig())

	for i := 0; i < 10; i++ {
		idx.Insert(randomNodeID(i), []float32{float32(i + 1), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx.RLock()
			mags := idx.GetMagnitudes()
			idx.RUnlock()
			// Modify the copy
			for id := range mags {
				mags[id] = 999.0
			}
		}()
	}

	for i := 10; i < 10+numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Insert(randomNodeID(id), []float32{float32(id), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		}(i)
	}

	wg.Wait()
}

func TestW4N13_GetDomains_ConcurrentWithInsert(t *testing.T) {
	idx := New(DefaultConfig())

	for i := 0; i < 10; i++ {
		idx.Insert(randomNodeID(i), []float32{float32(i), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx.RLock()
			domains := idx.GetDomains()
			idx.RUnlock()
			// Modify the copy
			for id := range domains {
				domains[id] = vectorgraphdb.DomainHistory
			}
		}()
	}

	for i := 10; i < 10+numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Insert(randomNodeID(id), []float32{float32(id), 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession)
		}(i)
	}

	wg.Wait()

	// Verify original data domain
	domain, _, err := idx.GetMetadata("node000")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if domain != vectorgraphdb.DomainCode {
		t.Errorf("Domain was corrupted: expected DomainCode, got %v", domain)
	}
}

func TestW4N13_GetNodeTypes_ConcurrentWithInsert(t *testing.T) {
	idx := New(DefaultConfig())

	for i := 0; i < 10; i++ {
		idx.Insert(randomNodeID(i), []float32{float32(i), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx.RLock()
			nodeTypes := idx.GetNodeTypes()
			idx.RUnlock()
			// Modify the copy
			for id := range nodeTypes {
				nodeTypes[id] = vectorgraphdb.NodeTypeFunction
			}
		}()
	}

	for i := 10; i < 10+numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Insert(randomNodeID(id), []float32{float32(id), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
		}(i)
	}

	wg.Wait()

	// Verify original data node type
	_, nodeType, err := idx.GetMetadata("node000")
	if err != nil {
		t.Fatalf("GetMetadata failed: %v", err)
	}
	if nodeType != vectorgraphdb.NodeTypeFile {
		t.Errorf("NodeType was corrupted: expected NodeTypeFile, got %v", nodeType)
	}
}

func TestW4N13_GetLayers_ConcurrentWithInsert(t *testing.T) {
	idx := New(DefaultConfig())

	for i := 0; i < 10; i++ {
		idx.Insert(randomNodeID(i), []float32{float32(i), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup
	numGoroutines := 50

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			idx.RLock()
			layers := idx.GetLayers()
			idx.RUnlock()
			// Modify the copy
			if len(layers) > 0 {
				layers[0] = nil
			}
		}()
	}

	for i := 10; i < 10+numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Insert(randomNodeID(id), []float32{float32(id), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		}(i)
	}

	wg.Wait()

	// Verify layers are still intact
	idx.RLock()
	layers := idx.GetLayers()
	idx.RUnlock()
	if len(layers) > 0 && layers[0] == nil {
		t.Error("Internal layers were corrupted")
	}
}

func TestW4N13_AllGetters_ConcurrentWithDelete(t *testing.T) {
	idx := New(DefaultConfig())

	// Pre-populate
	for i := 0; i < 100; i++ {
		idx.Insert(randomNodeID(i), []float32{float32(i), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	var wg sync.WaitGroup

	// Concurrent reads of all getters
	for i := 0; i < 20; i++ {
		wg.Add(5)
		go func() {
			defer wg.Done()
			idx.RLock()
			_ = idx.GetVectors()
			idx.RUnlock()
		}()
		go func() {
			defer wg.Done()
			idx.RLock()
			_ = idx.GetMagnitudes()
			idx.RUnlock()
		}()
		go func() {
			defer wg.Done()
			idx.RLock()
			_ = idx.GetDomains()
			idx.RUnlock()
		}()
		go func() {
			defer wg.Done()
			idx.RLock()
			_ = idx.GetNodeTypes()
			idx.RUnlock()
		}()
		go func() {
			defer wg.Done()
			idx.RLock()
			_ = idx.GetLayers()
			idx.RUnlock()
		}()
	}

	// Concurrent deletes
	for i := 50; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			idx.Delete(randomNodeID(id))
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// Edge Case Tests: Very large maps
// =============================================================================

func TestW4N13_GetVectors_LargeMap(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large map test in short mode")
	}

	idx := New(DefaultConfig())
	numNodes := 1000

	for i := 0; i < numNodes; i++ {
		idx.Insert(randomNodeID(i), []float32{float32(i), float32(i + 1), float32(i + 2)}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	idx.RLock()
	vectors := idx.GetVectors()
	idx.RUnlock()

	if len(vectors) != numNodes {
		t.Errorf("Expected %d vectors, got %d", numNodes, len(vectors))
	}

	// Modify the copy
	for id, vec := range vectors {
		vec[0] = 999.0
		vectors[id] = vec
	}

	// Verify internal state unchanged
	vec, _ := idx.GetVector("node000")
	if vec[0] == 999.0 {
		t.Error("Internal state was modified by modifying large copy")
	}
}

func TestW4N13_GetMagnitudes_LargeMap(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large map test in short mode")
	}

	idx := New(DefaultConfig())
	numNodes := 1000

	for i := 0; i < numNodes; i++ {
		idx.Insert(randomNodeID(i), []float32{float32(i + 1), 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	idx.RLock()
	magnitudes := idx.GetMagnitudes()
	idx.RUnlock()

	if len(magnitudes) != numNodes {
		t.Errorf("Expected %d magnitudes, got %d", numNodes, len(magnitudes))
	}

	// Modify all values in copy
	for id := range magnitudes {
		magnitudes[id] = 999.0
	}

	// Verify internal state unchanged
	idx.RLock()
	internalMags := idx.GetMagnitudes()
	idx.RUnlock()

	if internalMags["node001"] == 999.0 {
		t.Error("Internal magnitude state was modified")
	}
}

// =============================================================================
// Specific Regression Tests
// =============================================================================

func TestW4N13_GetVectors_CopyIsolation_MultipleGets(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 2, 3}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get two copies
	idx.RLock()
	copy1 := idx.GetVectors()
	copy2 := idx.GetVectors()
	idx.RUnlock()

	// Modify copy1
	copy1["node1"][0] = 100.0

	// copy2 should not be affected
	if copy2["node1"][0] == 100.0 {
		t.Error("Modifying copy1 affected copy2 - copies are not isolated")
	}
}

func TestW4N13_SearchStillWorksAfterGetVectors(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	idx.Insert("node2", []float32{0, 1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)

	// Get and modify vectors copy
	idx.RLock()
	vectors := idx.GetVectors()
	idx.RUnlock()

	vectors["node1"][0] = 0.0
	vectors["node1"][1] = 1.0

	// Search should still work correctly with internal state
	results := idx.Search([]float32{1, 0, 0, 0}, 2, nil)
	if len(results) == 0 {
		t.Error("Search returned no results after GetVectors modification")
	}

	// node1 should be most similar to query [1,0,0,0]
	if results[0].ID != "node1" {
		t.Errorf("Expected node1 as top result, got %s", results[0].ID)
	}
}

func TestW4N13_InsertStillWorksAfterGetVectors(t *testing.T) {
	idx := New(DefaultConfig())
	idx.Insert("node1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Get and modify vectors copy
	idx.RLock()
	vectors := idx.GetVectors()
	idx.RUnlock()

	delete(vectors, "node1")

	// Insert should still work and node1 should still exist
	err := idx.Insert("node2", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	if err != nil {
		t.Errorf("Insert failed: %v", err)
	}

	if !idx.Contains("node1") {
		t.Error("node1 was removed from internal state")
	}

	if !idx.Contains("node2") {
		t.Error("node2 was not added")
	}
}
