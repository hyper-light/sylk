package hnsw

import (
	"fmt"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

func TestMNRUUpdater_UpdateVector(t *testing.T) {
	// Create an index with known vectors
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Insert several vectors
	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0},
		"b": {0.9, 0.1, 0.0},
		"c": {0.8, 0.2, 0.0},
		"d": {0.0, 1.0, 0.0},
	}
	for id, vec := range vectors {
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("failed to insert %s: %v", id, err)
		}
	}

	updater := NewMNRUUpdater(idx)

	// Update vector "b" to be more similar to "d"
	newVec := []float32{0.1, 0.9, 0.0}
	if err := updater.UpdateVector("b", newVec); err != nil {
		t.Fatalf("failed to update vector: %v", err)
	}

	// Verify the vector was updated
	got, err := idx.GetVector("b")
	if err != nil {
		t.Fatalf("failed to get vector: %v", err)
	}
	for i := range newVec {
		if got[i] != newVec[i] {
			t.Errorf("vector mismatch at index %d: got %f, want %f", i, got[i], newVec[i])
		}
	}

	// Verify connectivity is maintained
	unreachable := updater.ValidateConnectivity()
	if len(unreachable) > 0 {
		t.Errorf("found unreachable nodes after update: %v", unreachable)
	}
}

func TestMNRUUpdater_UpdateVector_NotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Dimension = 3
	idx := New(cfg)

	if err := idx.Insert("a", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatal(err)
	}

	updater := NewMNRUUpdater(idx)

	err := updater.UpdateVector("nonexistent", []float32{0, 1, 0})
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestMNRUUpdater_UpdateVector_EmptyIndex(t *testing.T) {
	updater := NewMNRUUpdater(nil)

	err := updater.UpdateVector("a", []float32{1, 0, 0})
	if err != ErrIndexEmpty {
		t.Errorf("expected ErrIndexEmpty, got %v", err)
	}
}

func TestMNRUUpdater_DeleteVector(t *testing.T) {
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Insert a small graph
	vectors := map[string][]float32{
		"a": {1.0, 0.0, 0.0},
		"b": {0.9, 0.1, 0.0},
		"c": {0.8, 0.2, 0.0},
		"d": {0.7, 0.3, 0.0},
		"e": {0.6, 0.4, 0.0},
	}
	for id, vec := range vectors {
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("failed to insert %s: %v", id, err)
		}
	}

	updater := NewMNRUUpdater(idx)

	// Delete a node in the middle
	if err := updater.DeleteVector("c"); err != nil {
		t.Fatalf("failed to delete vector: %v", err)
	}

	// Verify the node was deleted
	if idx.Contains("c") {
		t.Error("deleted node still exists in index")
	}

	// Verify size decreased
	if idx.Size() != 4 {
		t.Errorf("expected size 4, got %d", idx.Size())
	}

	// Verify connectivity is maintained
	unreachable := updater.ValidateConnectivity()
	if len(unreachable) > 0 {
		t.Errorf("found unreachable nodes after delete: %v", unreachable)
	}
}

func TestMNRUUpdater_DeleteVector_NotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Dimension = 3
	idx := New(cfg)

	if err := idx.Insert("a", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatal(err)
	}

	updater := NewMNRUUpdater(idx)

	err := updater.DeleteVector("nonexistent")
	if err != ErrNodeNotFound {
		t.Errorf("expected ErrNodeNotFound, got %v", err)
	}
}

func TestMNRUUpdater_DeleteVector_EmptyIndex(t *testing.T) {
	updater := NewMNRUUpdater(nil)

	err := updater.DeleteVector("a")
	if err != ErrIndexEmpty {
		t.Errorf("expected ErrIndexEmpty, got %v", err)
	}
}

func TestMNRUUpdater_DeleteVector_EntryPoint(t *testing.T) {
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Insert nodes - first one becomes entry point
	if err := idx.Insert("entry", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert("other", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatal(err)
	}

	updater := NewMNRUUpdater(idx)

	// Delete the entry point
	if err := updater.DeleteVector("entry"); err != nil {
		t.Fatalf("failed to delete entry point: %v", err)
	}

	// Verify a new entry point was selected
	if idx.Size() != 1 {
		t.Errorf("expected size 1, got %d", idx.Size())
	}

	// Search should still work
	results := idx.Search([]float32{0, 1, 0}, 1, nil)
	if len(results) != 1 || results[0].ID != "other" {
		t.Errorf("search failed after entry point deletion: %v", results)
	}
}

func TestMNRUUpdater_FindIncomingNodes(t *testing.T) {
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Insert vectors in a line (each should connect to nearby ones)
	for i := 0; i < 5; i++ {
		vec := make([]float32, 3)
		vec[0] = float32(i) / 5.0
		vec[1] = 1.0 - float32(i)/5.0
		id := fmt.Sprintf("node%d", i)
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("failed to insert %s: %v", id, err)
		}
	}

	updater := NewMNRUUpdater(idx)

	// Find incoming nodes for a middle node
	incoming := updater.FindIncomingNodes("node2")
	if len(incoming) == 0 {
		t.Error("expected incoming nodes for middle node")
	}

	// Verify the incoming nodes exist
	for _, id := range incoming {
		if !idx.Contains(id) {
			t.Errorf("incoming node %s does not exist in index", id)
		}
	}
}

func TestMNRUUpdater_ValidateConnectivity(t *testing.T) {
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Build a connected graph
	for i := 0; i < 10; i++ {
		vec := []float32{float32(i) / 10.0, 1.0 - float32(i)/10.0, 0.5}
		id := fmt.Sprintf("n%d", i)
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("failed to insert %s: %v", id, err)
		}
	}

	updater := NewMNRUUpdater(idx)

	// All nodes should be reachable
	unreachable := updater.ValidateConnectivity()
	if len(unreachable) > 0 {
		t.Errorf("found unreachable nodes in normal graph: %v", unreachable)
	}
}

func TestMNRUUpdater_ValidateConnectivity_EmptyIndex(t *testing.T) {
	updater := NewMNRUUpdater(nil)
	unreachable := updater.ValidateConnectivity()
	if unreachable != nil {
		t.Errorf("expected nil for nil index, got %v", unreachable)
	}
}

func TestMNRUUpdater_ValidateConnectivity_EmptyGraph(t *testing.T) {
	cfg := DefaultConfig()
	idx := New(cfg)

	updater := NewMNRUUpdater(idx)
	unreachable := updater.ValidateConnectivity()
	if unreachable != nil {
		t.Errorf("expected nil for empty graph, got %v", unreachable)
	}
}

func TestMNRUUpdater_NoOrphanNodes(t *testing.T) {
	// This test ensures that after multiple updates, no nodes become orphaned
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Create a cluster of similar vectors
	for i := 0; i < 10; i++ {
		vec := []float32{
			0.8 + float32(i)*0.01,
			0.2 - float32(i)*0.01,
			0.1,
		}
		id := fmt.Sprintf("cluster%d", i)
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatal(err)
		}
	}

	updater := NewMNRUUpdater(idx)

	// Perform multiple updates, moving vectors around
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("cluster%d", i)
		// Move to a different part of the space
		newVec := []float32{
			0.2 - float32(i)*0.02,
			0.8 + float32(i)*0.02,
			0.1,
		}
		if err := updater.UpdateVector(id, newVec); err != nil {
			t.Fatalf("update %d failed: %v", i, err)
		}

		// Check connectivity after each update
		unreachable := updater.ValidateConnectivity()
		if len(unreachable) > 0 {
			t.Errorf("found orphan nodes after update %d: %v", i, unreachable)
		}
	}
}

func TestMNRUUpdater_ReconnectionAfterUpdate(t *testing.T) {
	// Test that connections are properly re-established after an update
	cfg := Config{
		M:           2, // Small M to make replacement more likely
		EfConstruct: 8,
		EfSearch:    8,
		Dimension:   3,
	}
	idx := New(cfg)

	// Insert 4 vectors in a specific pattern
	if err := idx.Insert("center", []float32{0.5, 0.5, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert("left", []float32{0.0, 0.5, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert("right", []float32{1.0, 0.5, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert("top", []float32{0.5, 1.0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
		t.Fatal(err)
	}

	updater := NewMNRUUpdater(idx)

	// Move center far away
	if err := updater.UpdateVector("center", []float32{0.5, -10.0, 0}); err != nil {
		t.Fatal(err)
	}

	// All nodes should still be reachable
	unreachable := updater.ValidateConnectivity()
	if len(unreachable) > 0 {
		t.Errorf("found orphan nodes after moving center: %v", unreachable)
	}

	// Search should still work for all original vectors
	for _, id := range []string{"left", "right", "top"} {
		vec, _ := idx.GetVector(id)
		results := idx.Search(vec, 1, nil)
		if len(results) == 0 {
			t.Errorf("search failed for %s after update", id)
		}
	}
}

func TestMNRUUpdater_ConcurrentUpdates(t *testing.T) {
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Insert initial vectors
	for i := 0; i < 20; i++ {
		vec := []float32{float32(i) / 20.0, 1.0 - float32(i)/20.0, 0.5}
		id := fmt.Sprintf("v%d", i)
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatal(err)
		}
	}

	updater := NewMNRUUpdater(idx)

	// Perform concurrent updates
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("v%d", i)
			newVec := []float32{1.0 - float32(i)/20.0, float32(i) / 20.0, 0.5}
			if err := updater.UpdateVector(id, newVec); err != nil {
				errors <- fmt.Errorf("update %s failed: %w", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}

	// Verify connectivity after concurrent updates
	unreachable := updater.ValidateConnectivity()
	if len(unreachable) > 0 {
		t.Errorf("found orphan nodes after concurrent updates: %v", unreachable)
	}
}

func TestMNRUUpdater_DeleteAndUpdate(t *testing.T) {
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Insert vectors
	for i := 0; i < 10; i++ {
		vec := []float32{float32(i) / 10.0, 1.0 - float32(i)/10.0, 0.5}
		id := fmt.Sprintf("v%d", i)
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatal(err)
		}
	}

	updater := NewMNRUUpdater(idx)

	// Delete some nodes
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("v%d", i)
		if err := updater.DeleteVector(id); err != nil {
			t.Fatalf("delete %s failed: %v", id, err)
		}
	}

	// Update remaining nodes
	for i := 3; i < 7; i++ {
		id := fmt.Sprintf("v%d", i)
		newVec := []float32{1.0 - float32(i)/10.0, float32(i) / 10.0, 0.5}
		if err := updater.UpdateVector(id, newVec); err != nil {
			t.Fatalf("update %s failed: %v", id, err)
		}
	}

	// Verify connectivity
	unreachable := updater.ValidateConnectivity()
	if len(unreachable) > 0 {
		t.Errorf("found orphan nodes after delete+update: %v", unreachable)
	}

	// Verify size
	expectedSize := 7 // 10 - 3 deleted
	if idx.Size() != expectedSize {
		t.Errorf("expected size %d, got %d", expectedSize, idx.Size())
	}
}

func TestMNRUUpdater_UpdatePreservesMetadata(t *testing.T) {
	cfg := Config{
		M:           4,
		EfConstruct: 16,
		EfSearch:    16,
		Dimension:   3,
	}
	idx := New(cfg)

	// Insert with specific metadata (using History domain and Session node type)
	if err := idx.Insert("test", []float32{1, 0, 0}, vectorgraphdb.DomainHistory, vectorgraphdb.NodeTypeSession); err != nil {
		t.Fatal(err)
	}

	updater := NewMNRUUpdater(idx)

	// Update vector
	if err := updater.UpdateVector("test", []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}

	// Verify metadata is preserved
	domain, nodeType, err := idx.GetMetadata("test")
	if err != nil {
		t.Fatal(err)
	}
	if domain != vectorgraphdb.DomainHistory {
		t.Errorf("domain changed: expected %v, got %v", vectorgraphdb.DomainHistory, domain)
	}
	if nodeType != vectorgraphdb.NodeTypeSession {
		t.Errorf("nodeType changed: expected %v, got %v", vectorgraphdb.NodeTypeSession, nodeType)
	}
}
