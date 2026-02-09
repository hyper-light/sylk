package ivf

import (
	"math"
	"testing"
)

// mockTombstone implements TombstoneFilter for testing.
type mockTombstone struct {
	dead map[uint32]bool
}

func (m *mockTombstone) IsDead(nodeID uint32) bool {
	return m.dead[nodeID]
}

// TestSearchExcludesDeadVectors verifies that all search paths
// exclude tombstoned vectors from results.
func TestSearchExcludesDeadVectors(t *testing.T) {
	// Build an index with 20 random-ish vectors in 8 dimensions.
	// Mark half of them dead and verify they never appear in results.
	dim := 8
	n := 20
	vectors := make([][]float32, n)
	for i := range n {
		v := make([]float32, dim)
		for d := range dim {
			// Deterministic pseudo-random: sin-based
			v[d] = float32(math.Sin(float64(i*dim+d)*0.7 + 0.3))
		}
		vectors[i] = v
	}

	cfg := Config{
		NumPartitions: 4,
		NumWorkers:    2,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	// Mark even-indexed vectors as dead
	dead := make(map[uint32]bool)
	for i := 0; i < n; i += 2 {
		dead[uint32(i)] = true
	}
	idx.SetTombstones(&mockTombstone{dead: dead})

	// Query with a vector that's close to vector[0] (dead) and vector[1] (alive)
	query := vectors[1] // Use vector[1] directly — it's alive, should be top result

	t.Run("Search", func(t *testing.T) {
		results := idx.Search(query, 5)
		assertNoDeadResults(t, results, dead)
		assertContainsID(t, results, 1) // vector[1] should match itself
	})

	t.Run("SearchBBQ", func(t *testing.T) {
		if idx.bbq == nil {
			t.Skip("BBQ not built for this index size")
		}
		results := idx.SearchBBQ(query, 5)
		assertNoDeadResults(t, results, dead)
	})

	t.Run("SearchIVF", func(t *testing.T) {
		if idx.centroids == nil {
			t.Skip("IVF centroids not built")
		}
		results := idx.SearchIVF(query, 5)
		assertNoDeadResults(t, results, dead)
	})
}

// TestSearchWithNoTombstones verifies that nil tombstones don't affect results.
func TestSearchWithNoTombstones(t *testing.T) {
	dim := 4
	n := 10
	vectors := make([][]float32, n)
	for i := range n {
		v := make([]float32, dim)
		for d := range dim {
			v[d] = float32(math.Sin(float64(i*dim+d)*1.1 + 0.5))
		}
		vectors[i] = v
	}

	cfg := Config{
		NumPartitions: 2,
		NumWorkers:    1,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	// No tombstones set — all results should be available
	results := idx.Search(vectors[0], 5)
	if len(results) == 0 {
		t.Fatal("Search with no tombstones should return results")
	}
	if results[0].ID != 0 {
		t.Errorf("top result should be vector[0] (self-match), got ID=%d", results[0].ID)
	}
}

// TestSearchAllDead verifies that searching when all vectors are dead returns nothing.
func TestSearchAllDead(t *testing.T) {
	dim := 4
	n := 8
	vectors := make([][]float32, n)
	for i := range n {
		v := make([]float32, dim)
		for d := range dim {
			v[d] = float32(i*dim+d+1) * 0.1
		}
		vectors[i] = v
	}

	cfg := Config{
		NumPartitions: 2,
		NumWorkers:    1,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	dead := make(map[uint32]bool)
	for i := range n {
		dead[uint32(i)] = true
	}
	idx.SetTombstones(&mockTombstone{dead: dead})

	results := idx.Search(vectors[0], 5)
	if len(results) != 0 {
		t.Errorf("Search with all dead vectors should return 0 results, got %d", len(results))
	}
}

// TestHealthTrackerTombstoneRatio verifies tombstone ratio measurement.
func TestHealthTrackerTombstoneRatio(t *testing.T) {
	dim := 4
	n := 20
	vectors := make([][]float32, n)
	for i := range n {
		v := make([]float32, dim)
		for d := range dim {
			v[d] = float32(math.Sin(float64(i*dim+d)*0.9 + 0.2))
		}
		vectors[i] = v
	}

	cfg := Config{
		NumPartitions: 4,
		NumWorkers:    2,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	// Mark 5 out of 20 dead = 25%
	dead := make(map[uint32]bool)
	for i := 0; i < 5; i++ {
		dead[uint32(i)] = true
	}
	idx.SetTombstones(&mockTombstone{dead: dead})

	ht := newHealthTracker(idx)
	status := ht.Status()

	if math.Abs(status.TombstoneRatio-0.25) > 0.01 {
		t.Errorf("TombstoneRatio = %f, want ~0.25", status.TombstoneRatio)
	}
}

// TestNeedsPartitionRebuild verifies partition rebuild detection.
func TestNeedsPartitionRebuild(t *testing.T) {
	dim := 4
	n := 100
	vectors := make([][]float32, n)
	for i := range n {
		v := make([]float32, dim)
		for d := range dim {
			v[d] = float32(math.Sin(float64(i*dim+d)*0.5 + 0.1))
		}
		vectors[i] = v
	}

	cfg := Config{
		NumPartitions: 4,
		NumWorkers:    2,
	}
	idx := NewIndex(cfg, dim)
	idx.Build(vectors)

	if len(idx.partitionIDs) == 0 {
		t.Skip("No partition IDs built")
	}

	// Mark all vectors in partition 0 as dead
	dead := make(map[uint32]bool)
	for _, id := range idx.partitionIDs[0] {
		dead[id] = true
	}
	idx.SetTombstones(&mockTombstone{dead: dead})

	ht := newHealthTracker(idx)
	rebuild := ht.NeedsPartitionRebuild()

	found := false
	for _, p := range rebuild {
		if p == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("partition 0 should need rebuild (all members dead), got rebuild=%v", rebuild)
	}
}

func assertNoDeadResults(t *testing.T, results []SearchResult, dead map[uint32]bool) {
	t.Helper()
	for _, r := range results {
		if dead[r.ID] {
			t.Errorf("dead vector ID=%d should not appear in search results", r.ID)
		}
	}
}

func assertContainsID(t *testing.T, results []SearchResult, id uint32) {
	t.Helper()
	for _, r := range results {
		if r.ID == id {
			return
		}
	}
	t.Errorf("expected ID=%d in search results", id)
}
