package hnsw

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSnapshotManager(t *testing.T) {
	idx := New(DefaultConfig())
	cfg := DefaultSnapshotManagerConfig()
	sm := NewSnapshotManager(idx, cfg)

	assert.NotNil(t, sm)
	assert.Equal(t, DefaultGCInterval, sm.gcInterval)
	assert.Equal(t, DefaultRetention, sm.retention)
	assert.Equal(t, uint64(0), sm.CurrentSeqNum())
}

func TestSnapshotManager_CreateSnapshot_EmptyIndex(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	snap := sm.CreateSnapshot()

	assert.NotNil(t, snap)
	assert.Equal(t, uint64(0), snap.SeqNum)
	assert.True(t, snap.IsEmpty())
	assert.Equal(t, int32(1), snap.ReaderCount())
}

func TestSnapshotManager_CreateSnapshot_Consistency(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	// Insert vectors
	err := idx.Insert("v1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)
	err = idx.Insert("v2", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	require.NoError(t, err)

	sm.OnInsert()
	sm.OnInsert()

	snap := sm.CreateSnapshot()

	assert.Equal(t, 2, snap.Size())
	assert.True(t, snap.ContainsVector("v1"))
	assert.True(t, snap.ContainsVector("v2"))

	// Snapshot vectors should be independent of index
	vec := snap.GetVector("v1")
	assert.Equal(t, []float32{1, 0, 0}, vec)
}

func TestSnapshotManager_CreateSnapshot_IsolatedFromWrites(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	err := idx.Insert("v1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)
	sm.OnInsert()

	snap := sm.CreateSnapshot()
	assert.Equal(t, 1, snap.Size())

	// Insert more after snapshot
	err = idx.Insert("v2", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	require.NoError(t, err)
	sm.OnInsert()

	// Original snapshot should not see new vector
	assert.Equal(t, 1, snap.Size())
	assert.False(t, snap.ContainsVector("v2"))

	// New snapshot should see both
	snap2 := sm.CreateSnapshot()
	assert.Equal(t, 2, snap2.Size())
	assert.True(t, snap2.ContainsVector("v2"))
}

func TestSnapshotManager_MultipleConcurrentSnapshots(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	// Insert initial data
	for i := 0; i < 10; i++ {
		vec := make([]float32, 3)
		vec[i%3] = 1
		err := idx.Insert(string(rune('a'+i)), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		require.NoError(t, err)
		sm.OnInsert()
	}

	// Create multiple concurrent snapshots
	const numSnapshots = 5
	var wg sync.WaitGroup
	snapshots := make([]*HNSWSnapshot, numSnapshots)
	errors := make([]error, numSnapshots)

	for i := 0; i < numSnapshots; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			snapshots[idx] = sm.CreateSnapshot()
		}(i)
	}

	wg.Wait()

	// Verify all snapshots were created successfully
	for i, snap := range snapshots {
		assert.Nil(t, errors[i])
		assert.NotNil(t, snap)
		assert.Equal(t, 10, snap.Size())
	}

	assert.Equal(t, numSnapshots, sm.SnapshotCount())
}

func TestSnapshotManager_ReleaseSnapshot(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	err := idx.Insert("v1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)

	snap := sm.CreateSnapshot()
	assert.Equal(t, int32(1), snap.ReaderCount())

	sm.ReleaseSnapshot(snap.ID)
	assert.Equal(t, int32(0), snap.ReaderCount())

	// Releasing non-existent snapshot should not panic
	sm.ReleaseSnapshot(99999)
}

func TestSnapshotManager_OnInsert_IncreasesSeqNum(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	assert.Equal(t, uint64(0), sm.CurrentSeqNum())

	sm.OnInsert()
	assert.Equal(t, uint64(1), sm.CurrentSeqNum())

	sm.OnInsert()
	assert.Equal(t, uint64(2), sm.CurrentSeqNum())

	sm.OnInsert()
	sm.OnInsert()
	sm.OnInsert()
	assert.Equal(t, uint64(5), sm.CurrentSeqNum())
}

func TestSnapshotManager_GCLoop_ExitsOnContextCancel(t *testing.T) {
	idx := New(DefaultConfig())
	cfg := SnapshotManagerConfig{
		GCInterval: 10 * time.Millisecond,
		Retention:  5 * time.Millisecond,
	}
	sm := NewSnapshotManager(idx, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		sm.GCLoop(ctx)
		close(done)
	}()

	// Let it run a few cycles
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("GCLoop did not exit after context cancel")
	}
}

func TestSnapshotManager_GCLoop_CleansOldSnapshots(t *testing.T) {
	idx := New(DefaultConfig())
	cfg := SnapshotManagerConfig{
		GCInterval: 10 * time.Millisecond,
		Retention:  20 * time.Millisecond,
	}
	sm := NewSnapshotManager(idx, cfg)

	err := idx.Insert("v1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)

	snap := sm.CreateSnapshot()
	sm.ReleaseSnapshot(snap.ID)

	assert.Equal(t, 1, sm.SnapshotCount())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.GCLoop(ctx)

	// Wait for retention period plus GC interval
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 0, sm.SnapshotCount())
}

func TestSnapshotManager_GCLoop_PreservesActiveReaders(t *testing.T) {
	idx := New(DefaultConfig())
	cfg := SnapshotManagerConfig{
		GCInterval: 10 * time.Millisecond,
		Retention:  5 * time.Millisecond,
	}
	sm := NewSnapshotManager(idx, cfg)

	err := idx.Insert("v1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)

	snap := sm.CreateSnapshot()
	// DO NOT release - keep reader active

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go sm.GCLoop(ctx)

	// Wait past retention
	time.Sleep(30 * time.Millisecond)

	// Snapshot should still exist because it has active reader
	assert.Equal(t, 1, sm.SnapshotCount())
	_, exists := sm.GetSnapshot(snap.ID)
	assert.True(t, exists)

	// Now release and wait for GC
	sm.ReleaseSnapshot(snap.ID)
	time.Sleep(30 * time.Millisecond)

	// Should be gone now
	assert.Equal(t, 0, sm.SnapshotCount())
}

func TestSnapshotManager_GetSnapshot(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	err := idx.Insert("v1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)

	snap := sm.CreateSnapshot()

	retrieved, exists := sm.GetSnapshot(snap.ID)
	assert.True(t, exists)
	assert.Equal(t, snap, retrieved)

	_, exists = sm.GetSnapshot(99999)
	assert.False(t, exists)
}

func TestSnapshotManager_ConcurrentReadsAndWrites(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	const numWriters = 3
	const numReaders = 5

	// Writers continuously insert
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-ctx.Done():
					return
				default:
					id := string(rune('A'+writerID)) + string(rune('0'+counter%10))
					vec := []float32{float32(writerID), float32(counter), 0}
					_ = idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
					sm.OnInsert()
					counter++
					time.Sleep(time.Millisecond)
				}
			}
		}(i)
	}

	// Readers continuously create and release snapshots
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					snap := sm.CreateSnapshot()
					// Read from snapshot
					_ = snap.Size()
					_ = snap.LayerCount()
					time.Sleep(2 * time.Millisecond)
					sm.ReleaseSnapshot(snap.ID)
				}
			}
		}()
	}

	wg.Wait()

	// Should not have panicked or deadlocked
	assert.True(t, sm.CurrentSeqNum() > 0)
}

func TestSnapshotManager_SnapshotPreservesLayers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.M = 4
	idx := New(cfg)
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	// Insert enough vectors to create layers
	vectors := [][]float32{
		{1, 0, 0, 0},
		{0, 1, 0, 0},
		{0, 0, 1, 0},
		{0, 0, 0, 1},
		{1, 1, 0, 0},
		{0, 1, 1, 0},
	}

	for i, vec := range vectors {
		err := idx.Insert(string(rune('a'+i)), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
		require.NoError(t, err)
		sm.OnInsert()
	}

	snap := sm.CreateSnapshot()

	// Layer structure should be preserved
	assert.GreaterOrEqual(t, snap.LayerCount(), 1)

	// Check layer 0 has all nodes
	layer0 := snap.GetLayer(0)
	if layer0 != nil {
		assert.GreaterOrEqual(t, layer0.NodeCount(), len(vectors))
	}
}

func TestSnapshotManager_SnapshotMagnitudes(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	err := idx.Insert("v1", []float32{3, 4, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)
	sm.OnInsert()

	snap := sm.CreateSnapshot()

	mag, exists := snap.GetMagnitude("v1")
	assert.True(t, exists)
	assert.InDelta(t, 5.0, mag, 0.0001) // 3-4-5 triangle
}

func TestSnapshotManager_DefaultConfig(t *testing.T) {
	cfg := DefaultSnapshotManagerConfig()

	assert.Equal(t, DefaultGCInterval, cfg.GCInterval)
	assert.Equal(t, DefaultRetention, cfg.Retention)
}

func TestSnapshotManager_SnapshotCount_Empty(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	assert.Equal(t, 0, sm.SnapshotCount())
}

func TestSnapshotManager_MultipleSnapshotsAtDifferentSeqNums(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	// Insert and snapshot at seqNum 0
	err := idx.Insert("v1", []float32{1, 0, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)
	snap1 := sm.CreateSnapshot()
	assert.Equal(t, uint64(0), snap1.SeqNum)
	assert.Equal(t, uint64(1), snap1.ID)

	// Increment and snapshot at seqNum 1
	sm.OnInsert()
	err = idx.Insert("v2", []float32{0, 1, 0}, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	require.NoError(t, err)
	snap2 := sm.CreateSnapshot()
	assert.Equal(t, uint64(1), snap2.SeqNum)
	assert.Equal(t, uint64(2), snap2.ID)

	// Verify snapshots are independent
	assert.Equal(t, 1, snap1.Size())
	assert.Equal(t, 2, snap2.Size())

	// Both should be stored by ID
	_, exists1 := sm.GetSnapshot(snap1.ID)
	_, exists2 := sm.GetSnapshot(snap2.ID)
	assert.True(t, exists1)
	assert.True(t, exists2)
}
