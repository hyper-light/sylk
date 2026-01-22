package storage

import (
	"path/filepath"
	"testing"
)

func TestShardedVectorStore_AppendBatch_CrossesBoundary(t *testing.T) {
	tmpDir := t.TempDir()
	const dim = 4
	const smallShardCap = 100

	store, err := CreateShardedVectorStore(tmpDir, dim, smallShardCap)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer store.Close()

	makeVec := func(seed float32) []float32 {
		return []float32{seed, seed + 0.1, seed + 0.2, seed + 0.3}
	}

	batch1 := make([][]float32, 80)
	for i := range batch1 {
		batch1[i] = makeVec(float32(i))
	}
	startID1, err := store.AppendBatch(batch1)
	if err != nil {
		t.Fatalf("batch1: %v", err)
	}
	if startID1 != 0 {
		t.Errorf("startID1: got %d, want 0", startID1)
	}
	if store.ShardCount() != 1 {
		t.Errorf("after batch1: shards=%d, want 1", store.ShardCount())
	}

	batch2 := make([][]float32, 50)
	for i := range batch2 {
		batch2[i] = makeVec(float32(80 + i))
	}
	startID2, err := store.AppendBatch(batch2)
	if err != nil {
		t.Fatalf("batch2: %v", err)
	}
	if startID2 != 80 {
		t.Errorf("startID2: got %d, want 80", startID2)
	}
	if store.ShardCount() != 2 {
		t.Errorf("after batch2: shards=%d, want 2", store.ShardCount())
	}

	if store.Count() != 130 {
		t.Errorf("count: got %d, want 130", store.Count())
	}

	for i := uint32(0); i < 130; i++ {
		vec := store.Get(i)
		if vec == nil {
			t.Errorf("vec %d: nil", i)
			continue
		}
		expected := makeVec(float32(i))
		if vec[0] != expected[0] {
			t.Errorf("vec %d: got %v, want %v", i, vec[0], expected[0])
		}
	}
}

func TestShardedGraphStore_SetNeighborsBatch_CrossesBoundary(t *testing.T) {
	tmpDir := t.TempDir()
	const R = 4
	const smallShardCap = 100

	store, err := CreateShardedGraphStore(tmpDir, R, smallShardCap)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer store.Close()

	batch := make([]NodeNeighbors, 150)
	for i := range batch {
		batch[i] = NodeNeighbors{
			NodeID:    uint32(i),
			Neighbors: []uint32{uint32((i + 1) % 150), uint32((i + 2) % 150)},
		}
	}

	if err := store.SetNeighborsBatch(batch); err != nil {
		t.Fatalf("SetNeighborsBatch: %v", err)
	}

	if store.ShardCount() != 2 {
		t.Errorf("shards: got %d, want 2", store.ShardCount())
	}

	if store.Count() != 150 {
		t.Errorf("count: got %d, want 150", store.Count())
	}

	for i := uint32(0); i < 150; i++ {
		neighbors := store.GetNeighbors(i)
		if len(neighbors) != 2 {
			t.Errorf("node %d: got %d neighbors, want 2", i, len(neighbors))
			continue
		}
		expected0 := uint32((i + 1) % 150)
		expected1 := uint32((i + 2) % 150)
		if neighbors[0] != expected0 || neighbors[1] != expected1 {
			t.Errorf("node %d: got %v, want [%d, %d]", i, neighbors, expected0, expected1)
		}
	}
}

func TestShardedVectorStore_Snapshot_CrossesShards(t *testing.T) {
	tmpDir := t.TempDir()
	const dim = 4
	const smallShardCap = 50

	store, err := CreateShardedVectorStore(tmpDir, dim, smallShardCap)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer store.Close()

	makeVec := func(seed float32) []float32 {
		return []float32{seed, seed + 0.1, seed + 0.2, seed + 0.3}
	}

	batch := make([][]float32, 120)
	for i := range batch {
		batch[i] = makeVec(float32(i))
	}
	if _, err := store.AppendBatch(batch); err != nil {
		t.Fatalf("append: %v", err)
	}

	if store.ShardCount() != 3 {
		t.Errorf("shards: got %d, want 3", store.ShardCount())
	}

	snap := store.Snapshot()
	if snap.Len() != 120 {
		t.Errorf("snapshot len: got %d, want 120", snap.Len())
	}

	for i := 0; i < 120; i++ {
		vec := snap.Get(uint32(i))
		if vec == nil {
			t.Errorf("snap vec %d: nil", i)
			continue
		}
		expected := makeVec(float32(i))
		if vec[0] != expected[0] {
			t.Errorf("snap vec %d: got %v, want %v", i, vec[0], expected[0])
		}
	}
}

func TestShardedVectorStore_ReopenMultipleShards(t *testing.T) {
	tmpDir := t.TempDir()
	const dim = 4
	const smallShardCap = 50

	store, err := CreateShardedVectorStore(tmpDir, dim, smallShardCap)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	makeVec := func(seed float32) []float32 {
		return []float32{seed, seed + 0.1, seed + 0.2, seed + 0.3}
	}

	batch := make([][]float32, 120)
	for i := range batch {
		batch[i] = makeVec(float32(i))
	}
	if _, err := store.AppendBatch(batch); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	store.Close()

	reopened, err := OpenShardedVectorStore(tmpDir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	if reopened.ShardCount() != 3 {
		t.Errorf("reopened shards: got %d, want 3", reopened.ShardCount())
	}

	if reopened.Count() != 120 {
		t.Errorf("reopened count: got %d, want 120", reopened.Count())
	}

	for i := uint32(0); i < 120; i++ {
		vec := reopened.Get(i)
		if vec == nil {
			t.Errorf("reopened vec %d: nil", i)
			continue
		}
		expected := makeVec(float32(i))
		if vec[0] != expected[0] {
			t.Errorf("reopened vec %d: got %v, want %v", i, vec[0], expected[0])
		}
	}
}

func TestShardedGraphStore_ReopenMultipleShards(t *testing.T) {
	tmpDir := t.TempDir()
	const R = 4
	const smallShardCap = 50

	store, err := CreateShardedGraphStore(tmpDir, R, smallShardCap)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	batch := make([]NodeNeighbors, 120)
	for i := range batch {
		batch[i] = NodeNeighbors{
			NodeID:    uint32(i),
			Neighbors: []uint32{uint32((i + 1) % 120)},
		}
	}
	if err := store.SetNeighborsBatch(batch); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if err := store.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	store.Close()

	reopened, err := OpenShardedGraphStore(filepath.Dir(filepath.Join(tmpDir, "dummy")))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	if reopened.ShardCount() != 3 {
		t.Errorf("reopened shards: got %d, want 3", reopened.ShardCount())
	}

	for i := uint32(0); i < 120; i++ {
		neighbors := reopened.GetNeighbors(i)
		if len(neighbors) != 1 {
			t.Errorf("reopened node %d: got %d neighbors, want 1", i, len(neighbors))
			continue
		}
		expected := uint32((i + 1) % 120)
		if neighbors[0] != expected {
			t.Errorf("reopened node %d: got %d, want %d", i, neighbors[0], expected)
		}
	}
}
