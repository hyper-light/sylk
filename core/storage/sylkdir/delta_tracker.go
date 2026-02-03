package sylkdir

import (
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

// DeltaTracker provides lock-free atomic counters for mutation tracking.
// Used by the checkpoint controller to evaluate the trigger inequality.
type DeltaTracker struct {
	nodesCreated   atomic.Uint64
	edgesCreated   atomic.Uint64
	edgesModified  atomic.Uint64
	vectorsCreated atomic.Uint64
	docsBytes      atomic.Uint64

	lastCheckpointAt  atomic.Int64  // Unix nanos
	lastCheckpointVer atomic.Uint64 // Encoded: major<<32 | minor<<16 | patch

	path string
}

// DeltaSnapshot is a point-in-time snapshot of the tracker counters.
type DeltaSnapshot struct {
	NodesCreated   uint64 `json:"nodes_created"`
	EdgesCreated   uint64 `json:"edges_created"`
	EdgesModified  uint64 `json:"edges_modified"`
	VectorsCreated uint64 `json:"vectors_created"`
	DocsBytes      uint64 `json:"docs_bytes"`

	LastCheckpointAt  int64  `json:"last_checkpoint_at"`
	LastCheckpointVer uint64 `json:"last_checkpoint_ver"`

	SchemaVersion int `json:"schema_version"`
}

// deltaSchemaVersion is the current schema version for DeltaSnapshot JSON.
const deltaSchemaVersion = 2

// NewDeltaTracker creates a new DeltaTracker that persists to the given path.
func NewDeltaTracker(path string) *DeltaTracker {
	return &DeltaTracker{path: path}
}

// --- Increment methods ---

// IncrNodes adds n to the nodes created counter.
func (d *DeltaTracker) IncrNodes(n uint64) {
	d.nodesCreated.Add(n)
}

// IncrEdges adds n to the edges created counter.
func (d *DeltaTracker) IncrEdges(n uint64) {
	d.edgesCreated.Add(n)
}

// IncrEdgesModified adds n to the edges modified counter.
func (d *DeltaTracker) IncrEdgesModified(n uint64) {
	d.edgesModified.Add(n)
}

// IncrVectors adds n to the vectors created counter.
func (d *DeltaTracker) IncrVectors(n uint64) {
	d.vectorsCreated.Add(n)
}

// IncrDocsBytes adds n to the document bytes counter.
func (d *DeltaTracker) IncrDocsBytes(n uint64) {
	d.docsBytes.Add(n)
}

// --- Snapshot and reset ---

// Snapshot returns a point-in-time copy of all counters.
func (d *DeltaTracker) Snapshot() DeltaSnapshot {
	return DeltaSnapshot{
		NodesCreated:      d.nodesCreated.Load(),
		EdgesCreated:      d.edgesCreated.Load(),
		EdgesModified:     d.edgesModified.Load(),
		VectorsCreated:    d.vectorsCreated.Load(),
		DocsBytes:         d.docsBytes.Load(),
		LastCheckpointAt:  d.lastCheckpointAt.Load(),
		LastCheckpointVer: d.lastCheckpointVer.Load(),
		SchemaVersion:     deltaSchemaVersion,
	}
}

// TotalEntities returns the sum of nodes, edges, and vectors created.
func (d *DeltaTracker) TotalEntities() uint64 {
	return d.nodesCreated.Load() + d.edgesCreated.Load() + d.vectorsCreated.Load()
}

// Reset zeroes all counters and records the checkpoint version and time.
func (d *DeltaTracker) Reset(version uint64) {
	d.nodesCreated.Store(0)
	d.edgesCreated.Store(0)
	d.edgesModified.Store(0)
	d.vectorsCreated.Store(0)
	d.docsBytes.Store(0)
	d.lastCheckpointAt.Store(time.Now().UnixNano())
	d.lastCheckpointVer.Store(version)
}

// --- Persistence ---

// Persist writes the current snapshot to disk as JSON.
func (d *DeltaTracker) Persist() error {
	snap := d.Snapshot()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("delta tracker: marshal: %w", err)
	}

	tmpPath := d.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("delta tracker: write: %w", err)
	}
	return os.Rename(tmpPath, d.path)
}

// Load reads a persisted snapshot from disk and restores counters.
// Returns nil if the file does not exist (fresh start).
func (d *DeltaTracker) Load() error {
	data, err := os.ReadFile(d.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delta tracker: read: %w", err)
	}

	return d.applySnapshot(data)
}

// applySnapshot deserializes JSON and restores counters.
// Handles both v1 (without schema_version) and v2.
func (d *DeltaTracker) applySnapshot(data []byte) error {
	var snap DeltaSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("delta tracker: unmarshal: %w", err)
	}

	d.nodesCreated.Store(snap.NodesCreated)
	d.edgesCreated.Store(snap.EdgesCreated)
	d.edgesModified.Store(snap.EdgesModified)
	d.vectorsCreated.Store(snap.VectorsCreated)
	d.docsBytes.Store(snap.DocsBytes)
	d.lastCheckpointAt.Store(snap.LastCheckpointAt)
	d.lastCheckpointVer.Store(snap.LastCheckpointVer)

	return nil
}
