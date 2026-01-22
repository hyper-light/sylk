package hnsw

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultGCInterval is the default interval for garbage collection.
	DefaultGCInterval = 30 * time.Second
	// DefaultRetention is the default retention period for old snapshots.
	DefaultRetention = 5 * time.Minute
)

// HNSWSnapshotManager provides consistent read views of HNSW index.
// It implements copy-on-write semantics where readers get immutable
// snapshots while writers continue to modify the live index.
type HNSWSnapshotManager struct {
	index         *Index
	currentSeqNum atomic.Uint64
	snapshotID    atomic.Uint64 // unique ID for each snapshot
	snapshots     sync.Map      // snapshotID → *HNSWSnapshot
	gcInterval    time.Duration
	retention     time.Duration
	mu            sync.RWMutex
}

// SnapshotManagerConfig holds configuration for the snapshot manager.
type SnapshotManagerConfig struct {
	GCInterval time.Duration
	Retention  time.Duration
}

// DefaultSnapshotManagerConfig returns default configuration values.
func DefaultSnapshotManagerConfig() SnapshotManagerConfig {
	return SnapshotManagerConfig{
		GCInterval: DefaultGCInterval,
		Retention:  DefaultRetention,
	}
}

// NewSnapshotManager creates a new snapshot manager for the given index.
func NewSnapshotManager(index *Index, cfg SnapshotManagerConfig) *HNSWSnapshotManager {
	return &HNSWSnapshotManager{
		index:      index,
		gcInterval: cfg.GCInterval,
		retention:  cfg.Retention,
	}
}

// CreateSnapshot captures the current HNSW state for consistent reads.
// The snapshot is a deep copy that remains immutable.
// W12.12: Uses write lock to prevent TOCTOU race between ID generation and storage.
func (sm *HNSWSnapshotManager) CreateSnapshot() *HNSWSnapshot {
	// W12.12: Use write lock to make snapshot creation atomic.
	// This prevents race between snapshotID.Add and snapshots.Store.
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.index.RLock()
	defer sm.index.RUnlock()

	// W12.12: Atomically capture both ID and seqNum under the same lock
	// to ensure consistency between snapshot metadata and content.
	id := sm.snapshotID.Add(1)
	seqNum := sm.currentSeqNum.Load()

	snap := sm.buildSnapshot(id, seqNum)
	snap.AcquireReader()
	sm.snapshots.Store(id, snap)
	return snap
}

// buildSnapshot creates an HNSWSnapshot from the current index state.
// Caller must hold read locks on both manager and index.
func (sm *HNSWSnapshotManager) buildSnapshot(id, seqNum uint64) *HNSWSnapshot {
	layers := sm.index.GetLayers()
	vectors := sm.index.GetVectors()
	magnitudes := sm.index.GetMagnitudes()
	domains := sm.index.GetDomains()
	nodeTypes := sm.index.GetNodeTypes()

	return &HNSWSnapshot{
		ID:         id,
		SeqNum:     seqNum,
		CreatedAt:  time.Now(),
		EntryPoint: sm.index.GetEntryPoint(),
		MaxLevel:   sm.index.GetMaxLevel(),
		Layers:     sm.copyAllLayers(layers),
		Vectors:    copyVectors(vectors),
		Magnitudes: copyMagnitudes(magnitudes),
		Domains:    copyDomains(domains),
		NodeTypes:  copyNodeTypes(nodeTypes),
	}
}

// copyAllLayers creates deep copies of all layers.
func (sm *HNSWSnapshotManager) copyAllLayers(layers []*layer) []LayerSnapshot {
	snapshots := make([]LayerSnapshot, len(layers))
	idToString := sm.index.idToString
	for i, l := range layers {
		snapshots[i] = NewLayerSnapshot(l, idToString)
	}
	return snapshots
}

// ReleaseSnapshot decrements the reader count for GC eligibility.
// Returns true if the release was valid, false if over-released (underflow prevented).
// Logs a warning on over-release attempts to help identify lifecycle bugs.
// W12.12: Uses read lock to prevent race with concurrent GC or snapshot creation.
func (sm *HNSWSnapshotManager) ReleaseSnapshot(id uint64) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	val, ok := sm.snapshots.Load(id)
	if !ok {
		return false
	}
	snap := val.(*HNSWSnapshot)
	_, released := snap.ReleaseReader()
	if !released {
		slog.Warn("snapshot over-release attempt detected",
			slog.Uint64("snapshot_id", id),
			slog.Int64("reader_count", int64(snap.ReaderCount())),
		)
	}
	return released
}

// OnInsert increments the sequence number so new snapshots see new data.
func (sm *HNSWSnapshotManager) OnInsert() {
	sm.currentSeqNum.Add(1)
}

// CurrentSeqNum returns the current sequence number.
func (sm *HNSWSnapshotManager) CurrentSeqNum() uint64 {
	return sm.currentSeqNum.Load()
}

// GCLoop runs garbage collection at regular intervals until ctx is done.
func (sm *HNSWSnapshotManager) GCLoop(ctx context.Context) {
	ticker := time.NewTicker(sm.gcInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sm.collectGarbage()
		}
	}
}

// collectGarbage removes old snapshots with no active readers.
// W12.12: Uses write lock to prevent race with concurrent operations.
func (sm *HNSWSnapshotManager) collectGarbage() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cutoff := time.Now().Add(-sm.retention)
	sm.snapshots.Range(func(key, value any) bool {
		snap := value.(*HNSWSnapshot)
		if sm.canCollect(snap, cutoff) {
			sm.snapshots.Delete(key)
		}
		return true
	})
}

// canCollect returns true if the snapshot is eligible for GC.
func (sm *HNSWSnapshotManager) canCollect(snap *HNSWSnapshot, cutoff time.Time) bool {
	return snap.ReaderCount() <= 0 && snap.CreatedAt.Before(cutoff)
}

// GetSnapshot retrieves a snapshot by ID if it exists.
func (sm *HNSWSnapshotManager) GetSnapshot(id uint64) (*HNSWSnapshot, bool) {
	val, ok := sm.snapshots.Load(id)
	if !ok {
		return nil, false
	}
	return val.(*HNSWSnapshot), true
}

// SnapshotCount returns the number of active snapshots.
func (sm *HNSWSnapshotManager) SnapshotCount() int {
	count := 0
	sm.snapshots.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}
