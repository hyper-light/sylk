package hnsw

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
)

// W12.12: Tests for race condition prevention in SnapshotManager.
// These tests verify that TOCTOU races are prevented through proper locking.

// TestW12_12_ConcurrentSnapshotCreation verifies multiple goroutines
// can safely create snapshots without race conditions.
func TestW12_12_ConcurrentSnapshotCreation(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	// Insert test data
	for i := 0; i < 10; i++ {
		vec := make([]float32, 4)
		vec[i%4] = 1.0
		idx.Insert(snapshotRaceTestNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	var wg sync.WaitGroup
	numGoroutines := 10
	snapshotsCreated := make(chan *HNSWSnapshot, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := sm.CreateSnapshot()
			snapshotsCreated <- snap
		}()
	}

	wg.Wait()
	close(snapshotsCreated)

	// Verify all snapshots have unique IDs
	seenIDs := make(map[uint64]bool)
	for snap := range snapshotsCreated {
		if seenIDs[snap.ID] {
			t.Errorf("W12.12: Duplicate snapshot ID %d detected", snap.ID)
		}
		seenIDs[snap.ID] = true
	}

	// Verify snapshot count
	if sm.SnapshotCount() != numGoroutines {
		t.Errorf("W12.12: Expected %d snapshots, got %d", numGoroutines, sm.SnapshotCount())
	}
}

// TestW12_12_ConcurrentCreateAndRelease verifies create and release
// operations don't race with each other.
func TestW12_12_ConcurrentCreateAndRelease(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	for i := 0; i < 5; i++ {
		vec := make([]float32, 4)
		vec[i%4] = 1.0
		idx.Insert(snapshotRaceTestNodeID(i), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
	}

	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	var wg sync.WaitGroup
	snapshots := make([]*HNSWSnapshot, 0, 20)
	var mu sync.Mutex

	// Create snapshots concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := sm.CreateSnapshot()
			mu.Lock()
			snapshots = append(snapshots, snap)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Release snapshots concurrently
	for _, snap := range snapshots {
		wg.Add(1)
		go func(s *HNSWSnapshot) {
			defer wg.Done()
			sm.ReleaseSnapshot(s.ID)
		}(snap)
	}

	wg.Wait()
}

// TestW12_12_ConcurrentCreateAndGC verifies create and garbage collection
// don't race with each other.
func TestW12_12_ConcurrentCreateAndGC(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	// Use very short retention for testing
	cfg := SnapshotManagerConfig{
		GCInterval: 10 * time.Millisecond,
		Retention:  1 * time.Millisecond,
	}
	sm := NewSnapshotManager(idx, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup

	// Start GC loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		sm.GCLoop(ctx)
	}()

	// Create snapshots concurrently with GC
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := sm.CreateSnapshot()
			// Release immediately to make eligible for GC
			sm.ReleaseSnapshot(snap.ID)
		}()
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for context to expire
	<-ctx.Done()
	wg.Wait()
}

// TestW12_12_SnapshotIDMonotonicity verifies snapshot IDs are strictly increasing.
func TestW12_12_SnapshotIDMonotonicity(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	var prevID uint64
	for i := 0; i < 100; i++ {
		snap := sm.CreateSnapshot()
		if snap.ID <= prevID && i > 0 {
			t.Errorf("W12.12: Snapshot ID not monotonically increasing: prev=%d, curr=%d", prevID, snap.ID)
		}
		prevID = snap.ID
	}
}

// TestW12_12_ConcurrentInsertAndSnapshot verifies inserts and snapshots
// are properly synchronized.
func TestW12_12_ConcurrentInsertAndSnapshot(t *testing.T) {
	idx := New(Config{M: 4, EfConstruct: 16, EfSearch: 16, LevelMult: 0.36067977499789996, Dimension: 4})

	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	var wg sync.WaitGroup

	// Insert nodes and trigger seqNum updates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			vec := make([]float32, 4)
			vec[n%4] = float32(n + 1)
			idx.Insert(snapshotRaceTestNodeID(n), vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)
			sm.OnInsert()
		}(i)
	}

	// Create snapshots concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sm.CreateSnapshot()
		}()
	}

	wg.Wait()

	// Verify manager state is consistent
	if sm.SnapshotCount() != 10 {
		t.Errorf("W12.12: Expected 10 snapshots, got %d", sm.SnapshotCount())
	}
}

// TestW12_12_ReleaseNonexistentSnapshot verifies releasing a non-existent
// snapshot is handled gracefully.
func TestW12_12_ReleaseNonexistentSnapshot(t *testing.T) {
	idx := New(DefaultConfig())
	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	// Should return false and not panic
	released := sm.ReleaseSnapshot(99999)
	if released {
		t.Error("W12.12: Releasing non-existent snapshot should return false")
	}
}

// TestW12_12_DoubleRelease verifies double release is handled gracefully.
func TestW12_12_DoubleRelease(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())
	snap := sm.CreateSnapshot()

	// First release should succeed
	released1 := sm.ReleaseSnapshot(snap.ID)
	if !released1 {
		t.Error("W12.12: First release should succeed")
	}

	// Second release should fail (underflow prevention)
	released2 := sm.ReleaseSnapshot(snap.ID)
	if released2 {
		t.Error("W12.12: Second release should fail (over-release)")
	}
}

// TestW12_12_SnapshotSeqNumConsistency verifies seqNum consistency.
func TestW12_12_SnapshotSeqNumConsistency(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	sm := NewSnapshotManager(idx, DefaultSnapshotManagerConfig())

	// Create snapshot before insert
	snap1 := sm.CreateSnapshot()
	seqNum1 := snap1.SeqNum

	// Trigger insert
	sm.OnInsert()

	// Create snapshot after insert
	snap2 := sm.CreateSnapshot()
	seqNum2 := snap2.SeqNum

	// seqNum should be higher for snap2
	if seqNum2 <= seqNum1 {
		t.Errorf("W12.12: SeqNum should increase after insert: snap1=%d, snap2=%d", seqNum1, seqNum2)
	}
}

// TestW12_12_GarbageCollectionSafety verifies GC doesn't collect active snapshots.
func TestW12_12_GarbageCollectionSafety(t *testing.T) {
	idx := New(DefaultConfig())
	vec := []float32{1, 0, 0, 0}
	idx.Insert("node1", vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile)

	cfg := SnapshotManagerConfig{
		GCInterval: 10 * time.Millisecond,
		Retention:  1 * time.Millisecond,
	}
	sm := NewSnapshotManager(idx, cfg)

	// Create snapshot and keep it active
	snap := sm.CreateSnapshot()
	snapID := snap.ID

	// Wait for potential GC
	time.Sleep(50 * time.Millisecond)
	sm.collectGarbage()

	// Snapshot should still exist (has active reader)
	_, exists := sm.GetSnapshot(snapID)
	if !exists {
		t.Error("W12.12: Active snapshot was incorrectly collected")
	}

	// Release and wait for retention period
	sm.ReleaseSnapshot(snapID)
	time.Sleep(50 * time.Millisecond)
	sm.collectGarbage()

	// Now it should be collected
	_, exists = sm.GetSnapshot(snapID)
	if exists {
		t.Error("W12.12: Released snapshot should be collected after retention period")
	}
}

// snapshotRaceTestNodeID generates a node ID for snapshot race tests.
func snapshotRaceTestNodeID(i int) string {
	return "snap_race_node_" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}
