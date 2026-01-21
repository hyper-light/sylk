// Package storage provides mmap-based storage types for the Vamana index.
package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Snapshot file names.
const (
	VectorsFile  = "vectors.bin"
	GraphFile    = "graph.bin"
	LabelsFile   = "labels.bin"
	IDMapFile    = "idmap.json"
	MetadataFile = "metadata.json"
)

// Directory naming conventions.
const (
	CurrentSymlink = "current"
	SnapshotPrefix = "snapshot_v"
	PendingSuffix  = "_pending"
)

// Snapshot errors.
var (
	ErrNoCurrentSnapshot     = errors.New("no current snapshot exists")
	ErrSnapshotNotOpen       = errors.New("snapshot is not open")
	ErrSnapshotAlreadyOpen   = errors.New("snapshot is already open")
	ErrPendingSnapshotExists = errors.New("a pending snapshot already exists")
	ErrNoPendingSnapshot     = errors.New("no pending snapshot to commit or abort")
	ErrInvalidSnapshotDir    = errors.New("invalid snapshot directory name")
	ErrSnapshotClosed        = errors.New("snapshot is closed")
)

// Snapshot represents a point-in-time state of the Vamana index.
// It contains all the stores needed to represent the index state.
type Snapshot struct {
	Vectors  *VectorStore
	Graph    *GraphStore
	Labels   *LabelStore
	IDMap    *IDMap
	Metadata *SnapshotMetadata

	dir    string
	closed bool
	mu     sync.Mutex
}

// Sync flushes all stores to disk.
func (s *Snapshot) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrSnapshotClosed
	}

	var errs []error

	if s.Vectors != nil {
		if err := s.Vectors.Sync(); err != nil {
			errs = append(errs, fmt.Errorf("sync vectors: %w", err))
		}
	}

	if s.Graph != nil {
		if err := s.Graph.Sync(); err != nil {
			errs = append(errs, fmt.Errorf("sync graph: %w", err))
		}
	}

	if s.Labels != nil {
		if err := s.Labels.Sync(); err != nil {
			errs = append(errs, fmt.Errorf("sync labels: %w", err))
		}
	}

	// IDMap and Metadata are JSON files, sync by re-saving them.
	if s.IDMap != nil {
		if err := s.IDMap.Save(filepath.Join(s.dir, IDMapFile)); err != nil {
			errs = append(errs, fmt.Errorf("sync idmap: %w", err))
		}
	}

	if s.Metadata != nil {
		if err := saveMetadata(filepath.Join(s.dir, MetadataFile), s.Metadata); err != nil {
			errs = append(errs, fmt.Errorf("sync metadata: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Close releases all resources associated with the snapshot.
func (s *Snapshot) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	var errs []error

	if s.Vectors != nil {
		if err := s.Vectors.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close vectors: %w", err))
		}
		s.Vectors = nil
	}

	if s.Graph != nil {
		if err := s.Graph.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close graph: %w", err))
		}
		s.Graph = nil
	}

	if s.Labels != nil {
		if err := s.Labels.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close labels: %w", err))
		}
		s.Labels = nil
	}

	// IDMap has no Close method, just nil it.
	s.IDMap = nil
	s.Metadata = nil

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// Dir returns the directory path of the snapshot.
func (s *Snapshot) Dir() string {
	return s.dir
}

// SnapshotManager manages snapshot lifecycle including creation, commits, and recovery.
// It provides atomic commit semantics using directory renames and symlinks.
//
// Directory structure:
//
//	baseDir/
//	  current/           <- symlink to active snapshot
//	  snapshot_v1/       <- versioned snapshot directory
//	  snapshot_v2/
//	  snapshot_v3_pending/  <- new snapshot being built
type SnapshotManager struct {
	baseDir string
	current *Snapshot
	mu      sync.RWMutex
}

// NewSnapshotManager creates a new snapshot manager for the specified base directory.
// It performs recovery by cleaning up any incomplete pending snapshots.
func NewSnapshotManager(baseDir string) *SnapshotManager {
	sm := &SnapshotManager{
		baseDir: baseDir,
	}

	// Perform recovery: clean up any pending snapshots from previous crashes.
	sm.cleanupPendingSnapshots()

	return sm
}

// cleanupPendingSnapshots removes any directories with the "_pending" suffix.
// This is called on startup to recover from incomplete writes.
func (sm *SnapshotManager) cleanupPendingSnapshots() {
	entries, err := os.ReadDir(sm.baseDir)
	if err != nil {
		// Directory might not exist yet, which is fine.
		return
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), PendingSuffix) {
			pendingPath := filepath.Join(sm.baseDir, entry.Name())
			_ = os.RemoveAll(pendingPath)
		}
	}
}

// OpenCurrent opens the current snapshot for reading.
// Returns ErrNoCurrentSnapshot if no snapshot exists.
func (sm *SnapshotManager) OpenCurrent() (*Snapshot, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Close any previously opened snapshot.
	if sm.current != nil {
		if err := sm.current.Close(); err != nil {
			return nil, fmt.Errorf("close previous snapshot: %w", err)
		}
		sm.current = nil
	}

	// Read the current symlink to find the active snapshot directory.
	currentLink := filepath.Join(sm.baseDir, CurrentSymlink)
	target, err := os.Readlink(currentLink)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoCurrentSnapshot
		}
		return nil, fmt.Errorf("read current symlink: %w", err)
	}

	// Resolve relative symlink target to absolute path.
	snapshotDir := target
	if !filepath.IsAbs(target) {
		snapshotDir = filepath.Join(sm.baseDir, target)
	}

	// Open the snapshot.
	snap, err := openSnapshot(snapshotDir, false)
	if err != nil {
		return nil, fmt.Errorf("open snapshot: %w", err)
	}

	sm.current = snap
	return snap, nil
}

// BeginWrite creates a new pending snapshot for writing.
// The pending snapshot must be committed with CommitWrite or aborted with AbortWrite.
// Returns ErrPendingSnapshotExists if a pending snapshot already exists.
func (sm *SnapshotManager) BeginWrite(dim, R, capacity int) (*Snapshot, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check for existing pending snapshots.
	if sm.hasPendingSnapshot() {
		return nil, ErrPendingSnapshotExists
	}

	// Determine the next version number.
	nextVersion := sm.nextVersion()

	// Create the pending directory.
	pendingName := fmt.Sprintf("%s%d%s", SnapshotPrefix, nextVersion, PendingSuffix)
	pendingDir := filepath.Join(sm.baseDir, pendingName)

	if err := os.MkdirAll(pendingDir, 0755); err != nil {
		return nil, fmt.Errorf("create pending directory: %w", err)
	}

	// Create the stores in the pending directory.
	snap, err := createSnapshot(pendingDir, dim, R, capacity, nextVersion)
	if err != nil {
		// Cleanup on failure.
		_ = os.RemoveAll(pendingDir)
		return nil, fmt.Errorf("create snapshot stores: %w", err)
	}

	return snap, nil
}

// CommitWrite atomically commits a pending snapshot, making it the current snapshot.
// It performs:
// 1. Sync all stores to disk
// 2. Rename pending directory to final versioned name
// 3. Update the "current" symlink to point to the new snapshot
func (sm *SnapshotManager) CommitWrite(pending *Snapshot) error {
	if pending == nil {
		return ErrNoPendingSnapshot
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Verify this is a pending snapshot.
	if !strings.HasSuffix(pending.dir, PendingSuffix) {
		return fmt.Errorf("%w: %s", ErrInvalidSnapshotDir, pending.dir)
	}

	// Sync all data to disk before commit.
	if err := pending.Sync(); err != nil {
		return fmt.Errorf("sync pending snapshot: %w", err)
	}

	// Close the snapshot to release file handles before rename.
	if err := pending.Close(); err != nil {
		return fmt.Errorf("close pending snapshot: %w", err)
	}

	// Calculate the final directory name (remove _pending suffix).
	finalDir := strings.TrimSuffix(pending.dir, PendingSuffix)
	finalName := filepath.Base(finalDir)

	// Atomic rename: pending → final.
	if err := os.Rename(pending.dir, finalDir); err != nil {
		return fmt.Errorf("rename pending to final: %w", err)
	}

	// Update the current symlink atomically.
	// We create a temporary symlink and rename it to ensure atomicity.
	if err := sm.updateCurrentSymlink(finalName); err != nil {
		// The snapshot is committed but symlink update failed.
		// This is recoverable on next startup.
		return fmt.Errorf("update current symlink: %w", err)
	}

	return nil
}

// AbortWrite discards a pending snapshot, removing all its data.
func (sm *SnapshotManager) AbortWrite(pending *Snapshot) error {
	if pending == nil {
		return ErrNoPendingSnapshot
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Verify this is a pending snapshot.
	if !strings.HasSuffix(pending.dir, PendingSuffix) {
		return fmt.Errorf("%w: %s", ErrInvalidSnapshotDir, pending.dir)
	}

	// Close the snapshot first.
	if err := pending.Close(); err != nil {
		// Continue with removal even if close fails.
	}

	// Remove the pending directory.
	if err := os.RemoveAll(pending.dir); err != nil {
		return fmt.Errorf("remove pending directory: %w", err)
	}

	return nil
}

// Current returns the currently open snapshot, or nil if none is open.
func (sm *SnapshotManager) Current() *Snapshot {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

// Close closes the current snapshot and releases resources.
func (sm *SnapshotManager) Close() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.current != nil {
		if err := sm.current.Close(); err != nil {
			return err
		}
		sm.current = nil
	}

	return nil
}

// hasPendingSnapshot checks if any pending snapshot directory exists.
func (sm *SnapshotManager) hasPendingSnapshot() bool {
	entries, err := os.ReadDir(sm.baseDir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), PendingSuffix) {
			return true
		}
	}

	return false
}

// nextVersion determines the next version number for a new snapshot.
func (sm *SnapshotManager) nextVersion() uint32 {
	entries, err := os.ReadDir(sm.baseDir)
	if err != nil {
		return 1
	}

	var maxVersion uint32

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Strip pending suffix if present.
		name = strings.TrimSuffix(name, PendingSuffix)

		// Check if it's a snapshot directory.
		if !strings.HasPrefix(name, SnapshotPrefix) {
			continue
		}

		// Extract version number.
		versionStr := strings.TrimPrefix(name, SnapshotPrefix)
		version, err := strconv.ParseUint(versionStr, 10, 32)
		if err != nil {
			continue
		}

		if uint32(version) > maxVersion {
			maxVersion = uint32(version)
		}
	}

	return maxVersion + 1
}

// updateCurrentSymlink atomically updates the "current" symlink to point to the new snapshot.
func (sm *SnapshotManager) updateCurrentSymlink(targetName string) error {
	currentLink := filepath.Join(sm.baseDir, CurrentSymlink)
	tempLink := filepath.Join(sm.baseDir, fmt.Sprintf(".current_%d", time.Now().UnixNano()))

	// Create a new symlink with a temporary name.
	if err := os.Symlink(targetName, tempLink); err != nil {
		return fmt.Errorf("create temp symlink: %w", err)
	}

	// Atomic rename: temp symlink → current symlink.
	if err := os.Rename(tempLink, currentLink); err != nil {
		_ = os.Remove(tempLink)
		return fmt.Errorf("rename temp symlink: %w", err)
	}

	return nil
}

// openSnapshot opens an existing snapshot directory for reading or writing.
func openSnapshot(dir string, readonly bool) (*Snapshot, error) {
	snap := &Snapshot{
		dir: dir,
	}

	var errs []error

	// Open vector store.
	vectorPath := filepath.Join(dir, VectorsFile)
	if _, err := os.Stat(vectorPath); err == nil {
		var vs *VectorStore
		var err error
		if readonly {
			vs, err = OpenVectorStoreReadOnly(vectorPath)
		} else {
			vs, err = OpenVectorStore(vectorPath)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("open vectors: %w", err))
		} else {
			snap.Vectors = vs
		}
	}

	// Open graph store.
	graphPath := filepath.Join(dir, GraphFile)
	if _, err := os.Stat(graphPath); err == nil {
		var gs *GraphStore
		var err error
		if readonly {
			gs, err = OpenGraphStoreReadOnly(graphPath)
		} else {
			gs, err = OpenGraphStore(graphPath)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("open graph: %w", err))
		} else {
			snap.Graph = gs
		}
	}

	// Open label store.
	labelPath := filepath.Join(dir, LabelsFile)
	if _, err := os.Stat(labelPath); err == nil {
		var ls *LabelStore
		var err error
		if readonly {
			ls, err = OpenLabelStoreReadOnly(labelPath)
		} else {
			ls, err = OpenLabelStore(labelPath)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("open labels: %w", err))
		} else {
			snap.Labels = ls
		}
	}

	// Load ID map.
	idmapPath := filepath.Join(dir, IDMapFile)
	if _, err := os.Stat(idmapPath); err == nil {
		idmap, err := LoadIDMap(idmapPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("load idmap: %w", err))
		} else {
			snap.IDMap = idmap
		}
	}

	// Load metadata.
	metaPath := filepath.Join(dir, MetadataFile)
	if _, err := os.Stat(metaPath); err == nil {
		meta, err := loadMetadata(metaPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("load metadata: %w", err))
		} else {
			snap.Metadata = meta
		}
	}

	if len(errs) > 0 {
		_ = snap.Close()
		return nil, errors.Join(errs...)
	}

	return snap, nil
}

// createSnapshot creates a new snapshot with fresh stores.
func createSnapshot(dir string, dim, R, capacity int, version uint32) (*Snapshot, error) {
	snap := &Snapshot{
		dir: dir,
	}

	var err error

	// Create vector store.
	vectorPath := filepath.Join(dir, VectorsFile)
	snap.Vectors, err = CreateVectorStore(vectorPath, dim, capacity)
	if err != nil {
		_ = snap.Close()
		return nil, fmt.Errorf("create vectors: %w", err)
	}

	// Create graph store.
	graphPath := filepath.Join(dir, GraphFile)
	snap.Graph, err = CreateGraphStore(graphPath, R, capacity)
	if err != nil {
		_ = snap.Close()
		return nil, fmt.Errorf("create graph: %w", err)
	}

	// Create label store.
	labelPath := filepath.Join(dir, LabelsFile)
	snap.Labels, err = CreateLabelStore(labelPath, capacity)
	if err != nil {
		_ = snap.Close()
		return nil, fmt.Errorf("create labels: %w", err)
	}

	// Create empty ID map.
	snap.IDMap = NewIDMap()

	// Save initial ID map.
	idmapPath := filepath.Join(dir, IDMapFile)
	if err := snap.IDMap.Save(idmapPath); err != nil {
		_ = snap.Close()
		return nil, fmt.Errorf("save idmap: %w", err)
	}

	// Create initial metadata.
	snap.Metadata = &SnapshotMetadata{
		Version:       version,
		Timestamp:     uint64(time.Now().UnixNano()),
		VectorCount:   0,
		GraphChecksum: 0,
	}

	// Save initial metadata.
	metaPath := filepath.Join(dir, MetadataFile)
	if err := saveMetadata(metaPath, snap.Metadata); err != nil {
		_ = snap.Close()
		return nil, fmt.Errorf("save metadata: %w", err)
	}

	return snap, nil
}

// metadataJSON is the JSON serialization format for SnapshotMetadata.
type metadataJSON struct {
	Version       uint32 `json:"version"`
	Timestamp     uint64 `json:"timestamp"`
	VectorCount   uint64 `json:"vector_count"`
	GraphChecksum uint32 `json:"graph_checksum"`
}

// loadMetadata loads snapshot metadata from a JSON file.
func loadMetadata(path string) (*SnapshotMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var mj metadataJSON
	if err := json.Unmarshal(data, &mj); err != nil {
		return nil, err
	}

	return &SnapshotMetadata{
		Version:       mj.Version,
		Timestamp:     mj.Timestamp,
		VectorCount:   mj.VectorCount,
		GraphChecksum: mj.GraphChecksum,
	}, nil
}

// saveMetadata saves snapshot metadata to a JSON file.
func saveMetadata(path string, meta *SnapshotMetadata) error {
	mj := metadataJSON{
		Version:       meta.Version,
		Timestamp:     meta.Timestamp,
		VectorCount:   meta.VectorCount,
		GraphChecksum: meta.GraphChecksum,
	}

	data, err := json.MarshalIndent(mj, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
