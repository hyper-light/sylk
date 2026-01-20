package hnsw

import (
	"sync"
	"testing"
)

func TestNewNeighborSet(t *testing.T) {
	ns := NewNeighborSet()
	if ns.Size() != 0 {
		t.Errorf("expected empty set, got size %d", ns.Size())
	}
}

func TestNewNeighborSetWithCapacity(t *testing.T) {
	ns := NewNeighborSetWithCapacity(100)
	if ns.Size() != 0 {
		t.Errorf("expected empty set, got size %d", ns.Size())
	}
}

func TestNeighborSet_Add(t *testing.T) {
	ns := NewNeighborSet()

	ns.Add("node1", 0.5)
	if !ns.Contains("node1") {
		t.Error("expected node1 to be in set")
	}
	if ns.Size() != 1 {
		t.Errorf("expected size 1, got %d", ns.Size())
	}

	// Update existing
	ns.Add("node1", 0.3)
	if ns.Size() != 1 {
		t.Errorf("expected size 1 after update, got %d", ns.Size())
	}
	dist, ok := ns.GetDistance("node1")
	if !ok {
		t.Error("expected node1 to exist")
	}
	if dist != 0.3 {
		t.Errorf("expected distance 0.3, got %f", dist)
	}
}

func TestNeighborSet_Remove(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node1", 0.5)
	ns.Add("node2", 0.6)

	ns.Remove("node1")
	if ns.Contains("node1") {
		t.Error("expected node1 to be removed")
	}
	if !ns.Contains("node2") {
		t.Error("expected node2 to still exist")
	}
	if ns.Size() != 1 {
		t.Errorf("expected size 1, got %d", ns.Size())
	}

	// Remove non-existent - should not panic
	ns.Remove("nonexistent")
	if ns.Size() != 1 {
		t.Errorf("expected size 1 after removing non-existent, got %d", ns.Size())
	}
}

func TestNeighborSet_Contains(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node1", 0.5)

	if !ns.Contains("node1") {
		t.Error("expected node1 to be contained")
	}
	if ns.Contains("node2") {
		t.Error("expected node2 to not be contained")
	}
}

func TestNeighborSet_GetDistance(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node1", 0.5)

	dist, ok := ns.GetDistance("node1")
	if !ok {
		t.Error("expected node1 to exist")
	}
	if dist != 0.5 {
		t.Errorf("expected distance 0.5, got %f", dist)
	}

	_, ok = ns.GetDistance("nonexistent")
	if ok {
		t.Error("expected nonexistent to not exist")
	}
}

func TestNeighborSet_GetSortedNeighbors(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node3", 0.8)
	ns.Add("node1", 0.2)
	ns.Add("node2", 0.5)

	sorted := ns.GetSortedNeighbors()
	if len(sorted) != 3 {
		t.Errorf("expected 3 neighbors, got %d", len(sorted))
	}

	// Should be sorted by distance ascending
	if sorted[0].ID != "node1" || sorted[0].Distance != 0.2 {
		t.Errorf("expected node1 with 0.2 first, got %s with %f", sorted[0].ID, sorted[0].Distance)
	}
	if sorted[1].ID != "node2" || sorted[1].Distance != 0.5 {
		t.Errorf("expected node2 with 0.5 second, got %s with %f", sorted[1].ID, sorted[1].Distance)
	}
	if sorted[2].ID != "node3" || sorted[2].Distance != 0.8 {
		t.Errorf("expected node3 with 0.8 third, got %s with %f", sorted[2].ID, sorted[2].Distance)
	}
}

func TestNeighborSet_GetSortedNeighborsDescending(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node3", 0.8)
	ns.Add("node1", 0.2)
	ns.Add("node2", 0.5)

	sorted := ns.GetSortedNeighborsDescending()
	if len(sorted) != 3 {
		t.Errorf("expected 3 neighbors, got %d", len(sorted))
	}

	// Should be sorted by distance descending
	if sorted[0].ID != "node3" || sorted[0].Distance != 0.8 {
		t.Errorf("expected node3 with 0.8 first, got %s with %f", sorted[0].ID, sorted[0].Distance)
	}
	if sorted[1].ID != "node2" || sorted[1].Distance != 0.5 {
		t.Errorf("expected node2 with 0.5 second, got %s with %f", sorted[1].ID, sorted[1].Distance)
	}
	if sorted[2].ID != "node1" || sorted[2].Distance != 0.2 {
		t.Errorf("expected node1 with 0.2 third, got %s with %f", sorted[2].ID, sorted[2].Distance)
	}
}

func TestNeighborSet_GetTopK(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node3", 0.8)
	ns.Add("node1", 0.2)
	ns.Add("node2", 0.5)
	ns.Add("node4", 0.9)
	ns.Add("node5", 0.1)

	t.Run("k less than size", func(t *testing.T) {
		topK := ns.GetTopK(3)
		if len(topK) != 3 {
			t.Errorf("expected 3 neighbors, got %d", len(topK))
		}
		if topK[0].ID != "node5" || topK[0].Distance != 0.1 {
			t.Errorf("expected node5 first, got %s", topK[0].ID)
		}
	})

	t.Run("k greater than size", func(t *testing.T) {
		topK := ns.GetTopK(10)
		if len(topK) != 5 {
			t.Errorf("expected 5 neighbors, got %d", len(topK))
		}
	})
}

func TestNeighborSet_GetIDs(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node1", 0.2)
	ns.Add("node2", 0.5)
	ns.Add("node3", 0.8)

	ids := ns.GetIDs()
	if len(ids) != 3 {
		t.Errorf("expected 3 IDs, got %d", len(ids))
	}

	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}
	if !idSet["node1"] || !idSet["node2"] || !idSet["node3"] {
		t.Error("expected all node IDs to be present")
	}
}

func TestNeighborSet_Size(t *testing.T) {
	ns := NewNeighborSet()
	if ns.Size() != 0 {
		t.Errorf("expected size 0, got %d", ns.Size())
	}

	ns.Add("node1", 0.5)
	if ns.Size() != 1 {
		t.Errorf("expected size 1, got %d", ns.Size())
	}

	ns.Add("node2", 0.6)
	if ns.Size() != 2 {
		t.Errorf("expected size 2, got %d", ns.Size())
	}
}

func TestNeighborSet_Clear(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node1", 0.5)
	ns.Add("node2", 0.6)

	ns.Clear()
	if ns.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", ns.Size())
	}
	if ns.Contains("node1") {
		t.Error("expected node1 to be cleared")
	}
}

func TestNeighborSet_Clone(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node1", 0.5)
	ns.Add("node2", 0.6)

	clone := ns.Clone()
	if clone.Size() != 2 {
		t.Errorf("expected clone size 2, got %d", clone.Size())
	}
	if !clone.Contains("node1") || !clone.Contains("node2") {
		t.Error("expected clone to contain all nodes")
	}

	// Modify original
	ns.Add("node3", 0.7)
	if clone.Contains("node3") {
		t.Error("clone should not be affected by original modification")
	}
}

func TestNeighborSet_Merge(t *testing.T) {
	ns1 := NewNeighborSet()
	ns1.Add("node1", 0.5)
	ns1.Add("node2", 0.6)

	ns2 := NewNeighborSet()
	ns2.Add("node2", 0.3) // Different distance
	ns2.Add("node3", 0.7)

	ns1.Merge(ns2)
	if ns1.Size() != 3 {
		t.Errorf("expected size 3 after merge, got %d", ns1.Size())
	}

	// Distance should be from ns2
	dist, _ := ns1.GetDistance("node2")
	if dist != 0.3 {
		t.Errorf("expected distance 0.3 from merge, got %f", dist)
	}
}

func TestNeighborSet_TrimToSize(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node3", 0.8)
	ns.Add("node1", 0.2)
	ns.Add("node2", 0.5)
	ns.Add("node4", 0.9)
	ns.Add("node5", 0.1)

	ns.TrimToSize(3)
	if ns.Size() != 3 {
		t.Errorf("expected size 3 after trim, got %d", ns.Size())
	}

	// Should keep the 3 closest
	if !ns.Contains("node5") || !ns.Contains("node1") || !ns.Contains("node2") {
		t.Error("expected closest 3 nodes to be kept")
	}
	if ns.Contains("node3") || ns.Contains("node4") {
		t.Error("expected furthest 2 nodes to be removed")
	}
}

func TestNeighborSet_TrimToSize_NoOp(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node1", 0.2)
	ns.Add("node2", 0.5)

	ns.TrimToSize(5)
	if ns.Size() != 2 {
		t.Errorf("expected size 2 (no trim needed), got %d", ns.Size())
	}
}

func TestNeighborSet_ForEach(t *testing.T) {
	ns := NewNeighborSet()
	ns.Add("node1", 0.5)
	ns.Add("node2", 0.6)

	visited := make(map[string]float32)
	ns.ForEach(func(id string, dist float32) {
		visited[id] = dist
	})

	if len(visited) != 2 {
		t.Errorf("expected 2 visited, got %d", len(visited))
	}
	if visited["node1"] != 0.5 || visited["node2"] != 0.6 {
		t.Error("expected correct distances in ForEach")
	}
}

// ConcurrentNeighborSet tests

func TestNewConcurrentNeighborSet(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	if cns.Size() != 0 {
		t.Errorf("expected empty set, got size %d", cns.Size())
	}
}

func TestNewConcurrentNeighborSetWithCapacity(t *testing.T) {
	cns := NewConcurrentNeighborSetWithCapacity(100)
	if cns.Size() != 0 {
		t.Errorf("expected empty set, got size %d", cns.Size())
	}
}

func TestConcurrentNeighborSet_Add(t *testing.T) {
	cns := NewConcurrentNeighborSet()

	cns.Add("node1", 0.5)
	if !cns.Contains("node1") {
		t.Error("expected node1 to be in set")
	}
	if cns.Size() != 1 {
		t.Errorf("expected size 1, got %d", cns.Size())
	}
}

func TestConcurrentNeighborSet_Remove(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node1", 0.5)
	cns.Add("node2", 0.6)

	cns.Remove("node1")
	if cns.Contains("node1") {
		t.Error("expected node1 to be removed")
	}
	if !cns.Contains("node2") {
		t.Error("expected node2 to still exist")
	}
}

func TestConcurrentNeighborSet_Contains(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node1", 0.5)

	if !cns.Contains("node1") {
		t.Error("expected node1 to be contained")
	}
	if cns.Contains("node2") {
		t.Error("expected node2 to not be contained")
	}
}

func TestConcurrentNeighborSet_GetSortedNeighbors(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node3", 0.8)
	cns.Add("node1", 0.2)
	cns.Add("node2", 0.5)

	sorted := cns.GetSortedNeighbors()
	if len(sorted) != 3 {
		t.Errorf("expected 3 neighbors, got %d", len(sorted))
	}
	if sorted[0].ID != "node1" {
		t.Errorf("expected node1 first, got %s", sorted[0].ID)
	}
}

func TestConcurrentNeighborSet_Clone(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node1", 0.5)
	cns.Add("node2", 0.6)

	clone := cns.Clone()
	if clone.Size() != 2 {
		t.Errorf("expected clone size 2, got %d", clone.Size())
	}

	// Modify original
	cns.Add("node3", 0.7)
	if clone.Contains("node3") {
		t.Error("clone should not be affected by original modification")
	}
}

func TestConcurrentNeighborSet_Merge(t *testing.T) {
	cns1 := NewConcurrentNeighborSet()
	cns1.Add("node1", 0.5)

	cns2 := NewConcurrentNeighborSet()
	cns2.Add("node2", 0.6)

	cns1.Merge(cns2)
	if cns1.Size() != 2 {
		t.Errorf("expected size 2 after merge, got %d", cns1.Size())
	}
}

func TestConcurrentNeighborSet_TrimToSize(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node3", 0.8)
	cns.Add("node1", 0.2)
	cns.Add("node2", 0.5)

	cns.TrimToSize(2)
	if cns.Size() != 2 {
		t.Errorf("expected size 2 after trim, got %d", cns.Size())
	}
	if !cns.Contains("node1") || !cns.Contains("node2") {
		t.Error("expected closest 2 nodes to be kept")
	}
}

func TestConcurrentNeighborSet_AddIfAbsent(t *testing.T) {
	cns := NewConcurrentNeighborSet()

	// First add should succeed
	if !cns.AddIfAbsent("node1", 0.5) {
		t.Error("expected first add to succeed")
	}

	// Second add should fail
	if cns.AddIfAbsent("node1", 0.3) {
		t.Error("expected second add to fail")
	}

	// Distance should be unchanged
	dist, _ := cns.GetDistance("node1")
	if dist != 0.5 {
		t.Errorf("expected distance 0.5, got %f", dist)
	}
}

func TestConcurrentNeighborSet_UpdateDistance(t *testing.T) {
	cns := NewConcurrentNeighborSet()

	// Update non-existent should fail
	if cns.UpdateDistance("node1", 0.5) {
		t.Error("expected update of non-existent to fail")
	}

	cns.Add("node1", 0.5)

	// Update existing should succeed
	if !cns.UpdateDistance("node1", 0.3) {
		t.Error("expected update to succeed")
	}

	dist, _ := cns.GetDistance("node1")
	if dist != 0.3 {
		t.Errorf("expected distance 0.3, got %f", dist)
	}
}

func TestConcurrentNeighborSet_AddWithLimit(t *testing.T) {
	cns := NewConcurrentNeighborSet()

	// Add up to limit
	cns.AddWithLimit("node1", 0.5, 2)
	cns.AddWithLimit("node2", 0.6, 2)
	if cns.Size() != 2 {
		t.Errorf("expected size 2, got %d", cns.Size())
	}

	// Add better than worst
	if !cns.AddWithLimit("node3", 0.3, 2) {
		t.Error("expected better neighbor to be added")
	}
	if cns.Size() != 2 {
		t.Errorf("expected size 2, got %d", cns.Size())
	}
	if cns.Contains("node2") {
		t.Error("expected worst neighbor (node2) to be replaced")
	}
	if !cns.Contains("node3") {
		t.Error("expected better neighbor (node3) to be added")
	}

	// Add worse than worst
	if cns.AddWithLimit("node4", 0.7, 2) {
		t.Error("expected worse neighbor to not be added")
	}
	if cns.Size() != 2 {
		t.Errorf("expected size 2, got %d", cns.Size())
	}
}

func TestConcurrentNeighborSet_Concurrent(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('A' + i%26))
			cns.Add(id, float32(i)*0.01)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('A' + i%26))
			cns.Contains(id)
			cns.GetSortedNeighbors()
			cns.Size()
		}(i)
	}

	wg.Wait()

	// Should have at least some entries (exact count depends on race)
	if cns.Size() == 0 {
		t.Error("expected some entries after concurrent operations")
	}
}

func TestConcurrentNeighborSet_GetTopK(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node3", 0.8)
	cns.Add("node1", 0.2)
	cns.Add("node2", 0.5)
	cns.Add("node4", 0.9)
	cns.Add("node5", 0.1)

	topK := cns.GetTopK(3)
	if len(topK) != 3 {
		t.Errorf("expected 3 neighbors, got %d", len(topK))
	}
	if topK[0].ID != "node5" {
		t.Errorf("expected node5 first, got %s", topK[0].ID)
	}
}

func TestConcurrentNeighborSet_GetIDs(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node1", 0.2)
	cns.Add("node2", 0.5)

	ids := cns.GetIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 IDs, got %d", len(ids))
	}
}

func TestConcurrentNeighborSet_Clear(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node1", 0.5)
	cns.Add("node2", 0.6)

	cns.Clear()
	if cns.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", cns.Size())
	}
}

func TestConcurrentNeighborSet_ForEach(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node1", 0.5)
	cns.Add("node2", 0.6)

	visited := make(map[string]float32)
	cns.ForEach(func(id string, dist float32) {
		visited[id] = dist
	})

	if len(visited) != 2 {
		t.Errorf("expected 2 visited, got %d", len(visited))
	}
}

func TestConcurrentNeighborSet_GetSortedNeighborsDescending(t *testing.T) {
	cns := NewConcurrentNeighborSet()
	cns.Add("node3", 0.8)
	cns.Add("node1", 0.2)
	cns.Add("node2", 0.5)

	sorted := cns.GetSortedNeighborsDescending()
	if len(sorted) != 3 {
		t.Errorf("expected 3 neighbors, got %d", len(sorted))
	}
	if sorted[0].ID != "node3" {
		t.Errorf("expected node3 first (descending), got %s", sorted[0].ID)
	}
}

// Benchmark tests

func BenchmarkNeighborSet_Contains(b *testing.B) {
	ns := NewNeighborSet()
	for i := 0; i < 100; i++ {
		ns.Add(string(rune('A'+i)), float32(i)*0.01)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ns.Contains("M")
	}
}

func BenchmarkSlice_Contains(b *testing.B) {
	neighbors := make([]string, 100)
	for i := 0; i < 100; i++ {
		neighbors[i] = string(rune('A' + i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target := "M"
		for _, n := range neighbors {
			if n == target {
				break
			}
		}
	}
}

func BenchmarkNeighborSet_GetSortedNeighbors(b *testing.B) {
	ns := NewNeighborSet()
	for i := 0; i < 48; i++ {
		ns.Add(string(rune('A'+i)), float32(i)*0.01)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ns.GetSortedNeighbors()
	}
}

func BenchmarkConcurrentNeighborSet_Contains(b *testing.B) {
	cns := NewConcurrentNeighborSet()
	for i := 0; i < 100; i++ {
		cns.Add(string(rune('A'+i)), float32(i)*0.01)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cns.Contains("M")
	}
}
