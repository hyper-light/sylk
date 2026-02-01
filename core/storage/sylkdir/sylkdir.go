// Package sylkdir manages the .sylk directory structure for Sylk knowledge graph storage.
// This package provides initialization, validation, and locking for the filesystem layout
// described in DB.md.
package sylkdir

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// Directory and file constants for the .sylk layout.
const (
	// RootDir is the name of the .sylk directory.
	RootDir = ".sylk"
	// ConfigFile is the name of the configuration file.
	ConfigFile = "config.yaml"
	// LockFile is the name of the lock file for concurrent access prevention.
	LockFile = "lock"

	// MetaFile is the global metadata file.
	MetaFile = "meta.json"

	// BleveDir contains Bleve full-text search index.
	BleveDir = "bleve"
	// BleveIndexDir contains the actual Bleve index.
	BleveIndexDir = "index"

	// SessionsDir contains per-session storage.
	SessionsDir = "sessions"

	// VersionsDir contains versioned snapshots (for both sessions and global).
	VersionsDir = "versions"
)

// ErrLocked is returned when attempting to lock an already locked directory.
var ErrLocked = errors.New("sylkdir: directory is locked by another process")

// ErrNotInitialized is returned when operations are performed on an uninitialized directory.
var ErrNotInitialized = errors.New("sylkdir: directory is not initialized")

// ValidationError represents a validation failure with details.
type ValidationError struct {
	Path   string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("sylkdir validation: %s: %s", e.Path, e.Reason)
}

// ValidationErrors is a collection of validation errors.
type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no validation errors"
	}
	if len(e) == 1 {
		return e[0].Error()
	}
	return fmt.Sprintf("sylkdir validation: %d errors (first: %s)", len(e), e[0].Error())
}

// SylkDir manages the .sylk directory structure.
type SylkDir struct {
	// projectPath is the root project directory containing .sylk.
	projectPath string
	// lockFile holds the file descriptor for the lock file.
	lockFile *os.File
}

// New creates a new SylkDir instance without initializing or validating.
// Call Init() to create the directory structure or Validate() to check an existing one.
func New(projectPath string) *SylkDir {
	return &SylkDir{
		projectPath: projectPath,
	}
}

// Init creates the full .sylk directory structure.
// All data is stored in versioned directories under .sylk/versions/.
// No flat/legacy directories are created.
func (s *SylkDir) Init() error {
	// Create root, sessions, WAL, and shared data directories.
	dirs := []string{
		s.RootPath(),
		s.SessionsPath(),
		s.CommitWALPath(),
		filepath.Join(s.GlobalDataPath(), "nodes"),
		filepath.Join(s.GlobalDataPath(), "vectors"),
		filepath.Join(s.GlobalDataPath(), "docs"),
		s.GlobalEdgeDataPath(),
		s.GlobalCanonicalIndexPath(),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("sylkdir: failed to create directory %s: %w", dir, err)
		}
	}

	// Create default config.yaml if it doesn't exist
	configPath := s.ConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := s.writeDefaultConfig(); err != nil {
			return fmt.Errorf("sylkdir: failed to create config: %w", err)
		}
	}

	// Create global versions directory
	versionsPath := s.GlobalVersionsPath()
	if err := os.MkdirAll(versionsPath, 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create global versions dir: %w", err)
	}

	// Initialize global meta.json with manifest if it doesn't exist.
	// meta.json is the single source of truth for the version manifest.
	metaPath := s.MetaPath()
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		initialVersion := SemanticVersion{Major: 1, Minor: 0, Patch: 0}

		// Create initial version directory
		if err := s.CreateGlobalVersion(initialVersion); err != nil {
			return fmt.Errorf("sylkdir: failed to create initial global version: %w", err)
		}

		// Create global meta.json with embedded manifest
		gm := NewGlobalMeta(metaPath)
		if err := gm.initWithManifest(initialVersion); err != nil {
			return fmt.Errorf("sylkdir: failed to init global meta: %w", err)
		}
	}

	// Create default session if it doesn't exist
	// Uses session ID 0 (reserved for system/default session)
	defaultSessionPath := s.SessionPath("default")
	if _, err := os.Stat(defaultSessionPath); os.IsNotExist(err) {
		sessionStore := NewSessionStore(s)
		sess, err := sessionStore.CreateNamed(0, "default", nil)
		if err != nil {
			return fmt.Errorf("sylkdir: failed to create default session: %w", err)
		}

		// Close the session's Bleve (it will be reopened when needed)
		if sess.BleveStore != nil {
			sess.BleveStore.CloseAll()
		}
	}

	return nil
}

// Validate checks that the directory structure is complete and valid.
// Returns nil if valid, or ValidationErrors containing all issues found.
func (s *SylkDir) Validate() error {
	var errs ValidationErrors

	// Check required root directories exist
	requiredDirs := map[string]string{
		s.RootPath():           "root .sylk directory",
		s.SessionsPath():       "sessions directory",
		s.GlobalVersionsPath(): "versions directory",
	}

	for path, desc := range requiredDirs {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			errs = append(errs, ValidationError{Path: path, Reason: desc + " missing"})
		} else if err != nil {
			errs = append(errs, ValidationError{Path: path, Reason: fmt.Sprintf("stat error: %v", err)})
		} else if !info.IsDir() {
			errs = append(errs, ValidationError{Path: path, Reason: desc + " is not a directory"})
		}
	}

	// Check required files exist
	requiredFiles := map[string]string{
		s.ConfigPath(): "config file",
		s.MetaPath():   "meta file",
	}

	for path, desc := range requiredFiles {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			errs = append(errs, ValidationError{Path: path, Reason: desc + " missing"})
		} else if err != nil {
			errs = append(errs, ValidationError{Path: path, Reason: fmt.Sprintf("stat error: %v", err)})
		} else if info.IsDir() {
			errs = append(errs, ValidationError{Path: path, Reason: desc + " is a directory, expected file"})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Lock acquires an exclusive lock on the .sylk directory.
// This prevents concurrent access from multiple processes.
// Returns ErrLocked if another process holds the lock.
// The lock is automatically released when Close() is called.
func (s *SylkDir) Lock() error {
	lockPath := s.LockPath()

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return fmt.Errorf("sylkdir: failed to create lock directory: %w", err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("sylkdir: failed to open lock file: %w", err)
	}

	// Try to acquire exclusive lock (non-blocking)
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLocked
		}
		return fmt.Errorf("sylkdir: failed to acquire lock: %w", err)
	}

	// Write PID to lock file for debugging
	if err := f.Truncate(0); err != nil {
		s.releaseLock(f)
		return fmt.Errorf("sylkdir: failed to truncate lock file: %w", err)
	}
	if _, err := f.WriteString(fmt.Sprintf("%d\n", os.Getpid())); err != nil {
		s.releaseLock(f)
		return fmt.Errorf("sylkdir: failed to write lock file: %w", err)
	}

	s.lockFile = f
	return nil
}

// Unlock releases the directory lock.
func (s *SylkDir) Unlock() error {
	if s.lockFile == nil {
		return nil
	}
	return s.releaseLock(s.lockFile)
}

func (s *SylkDir) releaseLock(f *os.File) error {
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	err := f.Close()
	if f == s.lockFile {
		s.lockFile = nil
	}
	return err
}

// Close releases resources including any held lock.
func (s *SylkDir) Close() error {
	return s.Unlock()
}

// IsLocked returns true if this instance holds the lock.
func (s *SylkDir) IsLocked() bool {
	return s.lockFile != nil
}

// Exists returns true if the .sylk directory exists.
func (s *SylkDir) Exists() bool {
	info, err := os.Stat(s.RootPath())
	return err == nil && info.IsDir()
}

// Path accessors

// RootPath returns the path to the .sylk directory.
func (s *SylkDir) RootPath() string {
	return filepath.Join(s.projectPath, RootDir)
}

// ConfigPath returns the path to config.yaml.
func (s *SylkDir) ConfigPath() string {
	return filepath.Join(s.RootPath(), ConfigFile)
}

// LockPath returns the path to the lock file.
func (s *SylkDir) LockPath() string {
	return filepath.Join(s.RootPath(), LockFile)
}

// MetaPath returns the path to the global meta.json file.
func (s *SylkDir) MetaPath() string {
	return filepath.Join(s.RootPath(), MetaFile)
}

// BlevePath returns the path to the Bleve directory.
func (s *SylkDir) BlevePath() string {
	return filepath.Join(s.RootPath(), BleveDir)
}

// BleveIndexPath returns the path to the Bleve index directory.
func (s *SylkDir) BleveIndexPath() string {
	return filepath.Join(s.BlevePath(), BleveIndexDir)
}

// SessionsPath returns the path to the sessions directory.
func (s *SylkDir) SessionsPath() string {
	return filepath.Join(s.RootPath(), SessionsDir)
}

// SessionPath returns the path to a specific session directory.
func (s *SylkDir) SessionPath(sessionID string) string {
	return filepath.Join(s.SessionsPath(), sessionID)
}

// GlobalVersionsPath returns the path to the global versions directory.
// Global versions are stored at .sylk/versions/ (root level, not inside knowledge).
func (s *SylkDir) GlobalVersionsPath() string {
	return filepath.Join(s.RootPath(), VersionsDir)
}

// GlobalVersionPath returns the path to a specific global version directory.
func (s *SylkDir) GlobalVersionPath(version SemanticVersion) string {
	return filepath.Join(s.GlobalVersionsPath(), version.DirName())
}


// CommitWALPath returns the path to the global commit WAL directory.
func (s *SylkDir) CommitWALPath() string {
	return filepath.Join(s.RootPath(), "wal")
}

// GlobalDataPath returns the path to the shared global data directory.
func (s *SylkDir) GlobalDataPath() string {
	return filepath.Join(s.RootPath(), "data")
}

// GlobalNodeDataPath returns the path to the shared global node data file.
func (s *SylkDir) GlobalNodeDataPath() string {
	return filepath.Join(s.GlobalDataPath(), "nodes", "data.bin")
}

// GlobalVectorDataPath returns the path to the shared global vector data file.
func (s *SylkDir) GlobalVectorDataPath() string {
	return filepath.Join(s.GlobalDataPath(), "vectors", "data.bin")
}

// GlobalDocDataPath returns the path to the shared global doc data file.
func (s *SylkDir) GlobalDocDataPath() string {
	return filepath.Join(s.GlobalDataPath(), "docs", "data.bin")
}

// GlobalDocIDMapPath returns the path to the global doc ID map file.
func (s *SylkDir) GlobalDocIDMapPath() string {
	return filepath.Join(s.GlobalDataPath(), "docs", "id_map.bin")
}

// GlobalChunkDataPath returns the path to the shared global chunk data file.
func (s *SylkDir) GlobalChunkDataPath() string {
	return filepath.Join(s.GlobalDataPath(), "chunks", "data.bin")
}

// GlobalPath returns the root path of the global store (same as RootPath).
func (s *SylkDir) GlobalPath() string {
	return s.RootPath()
}

// GlobalEdgeDataPath returns the path to the shared global edge shard directory.
func (s *SylkDir) GlobalEdgeDataPath() string {
	return filepath.Join(s.GlobalDataPath(), "edges")
}

// GlobalCanonicalIndexPath returns the path to the canonical key index directory.
func (s *SylkDir) GlobalCanonicalIndexPath() string {
	return filepath.Join(s.GlobalDataPath(), "canonical")
}

// GlobalIVFPath returns the path to the IVF vector index directory.
func (s *SylkDir) GlobalIVFPath() string {
	return filepath.Join(s.GlobalDataPath(), "ivf")
}

// GlobalNodeBlocksPath returns the path to the node blocks directory.
func (s *SylkDir) GlobalNodeBlocksPath() string {
	return filepath.Join(s.GlobalDataPath(), "node_blocks")
}

// CreateGlobalVersion creates a global version directory with subdirectories.
// Structure: .sylk/versions/v1.0.0/{nodes,edges,vectors,docs,bleve}
func (s *SylkDir) CreateGlobalVersion(version SemanticVersion) error {
	versionPath := s.GlobalVersionPath(version)

	dirs := []string{
		versionPath,
		filepath.Join(versionPath, "nodes"),
		filepath.Join(versionPath, "vectors"),
		filepath.Join(versionPath, "docs"),
		filepath.Join(versionPath, "bleve"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("sylkdir: create global version dir %s: %w", dir, err)
		}
	}

	// Create version meta.json
	vMeta := map[string]interface{}{
		"version":    version.String(),
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(vMeta, "", "  ")
	if err != nil {
		return fmt.Errorf("sylkdir: marshal version meta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(versionPath, "meta.json"), data, 0644); err != nil {
		return fmt.Errorf("sylkdir: write version meta: %w", err)
	}

	// Create empty tombstone bitmap
	tb := NewTombstoneBitmap(filepath.Join(versionPath, TombstoneFile), 64)
	if err := tb.Save(); err != nil {
		return fmt.Errorf("sylkdir: create tombstones: %w", err)
	}

	// Create empty offset indexes for nodes, vectors, and docs (shared data files).
	nodeIdx := NewOffsetIndex(filepath.Join(versionPath, "nodes", "index.bin"), offsetIndexMinCapacity)
	if err := nodeIdx.Save(); err != nil {
		return fmt.Errorf("sylkdir: create node index: %w", err)
	}
	vecIdx := NewOffsetIndex(filepath.Join(versionPath, "vectors", "index.bin"), offsetIndexMinCapacity)
	if err := vecIdx.Save(); err != nil {
		return fmt.Errorf("sylkdir: create vector index: %w", err)
	}
	docIdx := NewOffsetIndex(filepath.Join(versionPath, "docs", "index.bin"), offsetIndexMinCapacity)
	if err := docIdx.Save(); err != nil {
		return fmt.Errorf("sylkdir: create doc index: %w", err)
	}

	return nil
}

// SnapshotGlobalData copies parent version's index/data files into the target version.
// Nodes and vectors use offset index files (cloned, not data copied).
// Edges and docs use per-version data files (physical copy).
func (s *SylkDir) SnapshotGlobalData(parent, target SemanticVersion) error {
	parentPath := s.GlobalVersionPath(parent)
	targetPath := s.GlobalVersionPath(target)

	// Index files for nodes, vectors, and docs (offset indexes into shared data files).
	indexFiles := []string{
		"nodes/index.bin",
		"vectors/index.bin",
		"docs/index.bin",
	}
	for _, file := range indexFiles {
		src := filepath.Join(parentPath, file)
		dst := filepath.Join(targetPath, file)
		if err := copyFileIfExists(src, dst); err != nil {
			return fmt.Errorf("sylkdir: copy index %s: %w", file, err)
		}
	}

	// Per-version data files: tombstones (edges use shared EdgeShardStore,
	// nodes/vectors/docs use shared data files with per-version offset indexes).
	dataFiles := []string{
		TombstoneFile,
	}
	for _, file := range dataFiles {
		src := filepath.Join(parentPath, file)
		dst := filepath.Join(targetPath, file)
		if err := copyFileIfExists(src, dst); err != nil {
			return fmt.Errorf("sylkdir: copy %s: %w", file, err)
		}
	}

	return nil
}

// CompactGlobalData compacts parent version's data into the target version,
// filtering out dead nodes/edges/vectors/docs via the tombstone bitmap.
// Nodes and vectors use index-level compaction (remove dead entries from offset index).
// Edges and docs use record-level compaction (read, filter, rewrite).
// The target version starts with an empty tombstone bitmap.
func (s *SylkDir) CompactGlobalData(parent, target SemanticVersion) error {
	parentPath := s.GlobalVersionPath(parent)
	targetPath := s.GlobalVersionPath(target)

	// Load parent tombstones
	tb, err := LoadTombstoneBitmap(parentPath)
	if err != nil {
		return fmt.Errorf("sylkdir: load parent tombstones: %w", err)
	}

	// Compact nodes: index-level (remove dead entries, no data rewrite)
	if err := s.compactNodeIndex(parentPath, targetPath, tb); err != nil {
		return fmt.Errorf("sylkdir: compact node index: %w", err)
	}

	// Compact vectors: index-level (remove dead entries, no data rewrite)
	if err := s.compactVectorIndex(parentPath, targetPath, tb); err != nil {
		return fmt.Errorf("sylkdir: compact vector index: %w", err)
	}

	// Edges use shared EdgeShardStore — no per-version compaction needed.

	// Compact docs: index-level (remove dead entries via DocRef from live nodes)
	if err := s.compactDocIndex(parent, target, tb); err != nil {
		return fmt.Errorf("sylkdir: compact doc index: %w", err)
	}

	// Copy parent tombstone to target. Node/vector indexes are compacted (dead
	// entries removed), so the tombstone is redundant for those. However, edges
	// reside in the shared EdgeShardStore and still require tombstone filtering.
	// The tombstone is preserved to ensure ReadAllFromVersion excludes dead edges.
	src := filepath.Join(parentPath, TombstoneFile)
	dst := filepath.Join(targetPath, TombstoneFile)
	if err := copyFileIfExists(src, dst); err != nil {
		return err
	}

	// Data file compaction: rewrite shared data files to remove unreferenced records.
	// Collect live offsets from ALL global version indexes, compact each data file,
	// and remap all version indexes to the new offsets.
	if err := s.compactDataFiles(target); err != nil {
		return fmt.Errorf("sylkdir: compact data files: %w", err)
	}

	return nil
}

// compactNodeIndex removes dead entries from the parent's node offset index.
func (s *SylkDir) compactNodeIndex(parentDir, targetDir string, tb *TombstoneBitmap) error {
	return compactOffsetIndex(
		filepath.Join(parentDir, "nodes", "index.bin"),
		filepath.Join(targetDir, "nodes", "index.bin"),
		tb,
	)
}

// compactVectorIndex removes dead entries from the parent's vector offset index.
func (s *SylkDir) compactVectorIndex(parentDir, targetDir string, tb *TombstoneBitmap) error {
	return compactOffsetIndex(
		filepath.Join(parentDir, "vectors", "index.bin"),
		filepath.Join(targetDir, "vectors", "index.bin"),
		tb,
	)
}

// compactOffsetIndex loads a parent offset index, removes dead entries, and saves to target.
func compactOffsetIndex(parentPath, targetPath string, tb *TombstoneBitmap) error {
	parentIdx, err := LoadOffsetIndex(parentPath)
	if err != nil {
		// No index to compact.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	targetIdx := parentIdx.Clone(targetPath)

	// Collect dead IDs first, then delete in a second pass.
	var deadIDs []uint32
	targetIdx.ForEach(func(id uint32, _ int64) bool {
		if tb.IsDead(id) {
			deadIDs = append(deadIDs, id)
		}
		return true
	})
	for _, id := range deadIDs {
		targetIdx.Delete(id)
	}

	return targetIdx.Save()
}



// compactDocIndex removes dead entries from the parent's doc offset index.
// Uses DocRef from live nodes to determine which docs to keep.
func (s *SylkDir) compactDocIndex(parent, target SemanticVersion, tb *TombstoneBitmap) error {
	parentPath := filepath.Join(s.GlobalVersionPath(parent), "docs", "index.bin")
	targetPath := filepath.Join(s.GlobalVersionPath(target), "docs", "index.bin")

	parentIdx, err := LoadOffsetIndex(parentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Build set of DocRef values from live nodes.
	liveDocRefs, allResolved, refErr := collectGlobalLiveDocRefs(s, parent, tb)
	if refErr != nil || !allResolved {
		// Cannot filter: clone as-is.
		cloned := parentIdx.Clone(targetPath)
		return cloned.Save()
	}

	targetIdx := parentIdx.Clone(targetPath)

	var deadIDs []uint32
	targetIdx.ForEach(func(docID uint32, _ int64) bool {
		if !liveDocRefs[docID] {
			deadIDs = append(deadIDs, docID)
		}
		return true
	})
	for _, id := range deadIDs {
		targetIdx.Delete(id)
	}

	return targetIdx.Save()
}

// compactDataFiles rewrites shared data files to reclaim space from dead records.
// Live offsets are determined from the target version's index (already index-level
// compacted). All version indexes are then strict-remapped: valid entries get new
// offsets, stale entries are cleared.
func (s *SylkDir) compactDataFiles(target SemanticVersion) error {
	versions, err := s.listGlobalVersions()
	if err != nil {
		return err
	}

	if err := s.compactSharedFile(versions, target, "nodes", s.GlobalNodeDataPath(), sizedRecordSize); err != nil {
		return fmt.Errorf("compact node data: %w", err)
	}
	if err := s.compactSharedFile(versions, target, "vectors", s.GlobalVectorDataPath(), vectorRecordSize); err != nil {
		return fmt.Errorf("compact vector data: %w", err)
	}
	if err := s.compactSharedFile(versions, target, "docs", s.GlobalDocDataPath(), sizedRecordSize); err != nil {
		return fmt.Errorf("compact doc data: %w", err)
	}
	return nil
}

// compactSharedFile compacts a single shared data file. Live offsets come from
// the target version's index (already index-level compacted, dead entries removed).
// After compaction, ALL version indexes are strict-remapped: entries with a new
// offset are updated, entries not in the remap are cleared.
func (s *SylkDir) compactSharedFile(
	versions []SemanticVersion,
	target SemanticVersion,
	subdir string,
	dataPath string,
	sizeFn func(*SharedDataFile, int64) (int, error),
) error {
	// Load the target version's index — already index-level compacted.
	targetIdxPath := filepath.Join(s.GlobalVersionPath(target), subdir, "index.bin")
	targetIdx, err := LoadOffsetIndex(targetIdxPath)
	if err != nil {
		return nil // No index for this data type in target.
	}

	// Collect live offsets from target index only.
	offsetSet := make(map[int64]bool)
	targetIdx.ForEach(func(_ uint32, offset int64) bool {
		offsetSet[offset] = true
		return true
	})
	if len(offsetSet) == 0 {
		return nil
	}

	// Sort offsets for sequential I/O during compaction.
	liveOffsets := make([]int64, 0, len(offsetSet))
	for off := range offsetSet {
		liveOffsets = append(liveOffsets, off)
	}
	sort.Slice(liveOffsets, func(i, j int) bool { return liveOffsets[i] < liveOffsets[j] })

	// Open data file and compact to a temporary file.
	sdf, err := OpenSharedDataFile(dataPath)
	if err != nil {
		return err
	}

	compactPath := dataPath + ".compact"
	remap, err := sdf.CompactTo(compactPath, liveOffsets, sizeFn)
	if err != nil {
		sdf.Close()
		return err
	}

	// Remap the target version's index (all entries are in the remap).
	targetIdx.RemapOffsets(remap)
	if err := targetIdx.Save(); err != nil {
		sdf.Close()
		os.Remove(compactPath)
		return fmt.Errorf("save remapped target index: %w", err)
	}

	// Strict-remap older version indexes: mapped entries get new offsets,
	// unmapped entries (dead records) are cleared to absent.
	for _, ver := range versions {
		if ver.Equal(target) {
			continue // Already remapped above.
		}
		idxPath := filepath.Join(s.GlobalVersionPath(ver), subdir, "index.bin")
		idx, err := LoadOffsetIndex(idxPath)
		if err != nil {
			continue
		}
		idx.RemapOffsetsCompact(remap)
		if err := idx.Save(); err != nil {
			sdf.Close()
			os.Remove(compactPath)
			return fmt.Errorf("save remapped index: %w", err)
		}
	}

	// Replace original data file with compacted file.
	if err := sdf.ReplaceFile(compactPath); err != nil {
		sdf.Close()
		return err
	}
	sdf.Close()
	return nil
}

// listGlobalVersions returns all global version IDs from the manifest
// embedded in meta.json (the authoritative source).
func (s *SylkDir) listGlobalVersions() ([]SemanticVersion, error) {
	data, err := os.ReadFile(s.MetaPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var meta GlobalMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.Manifest == nil {
		return nil, nil
	}
	versions := make([]SemanticVersion, len(meta.Manifest.Versions))
	for i, gv := range meta.Manifest.Versions {
		versions[i] = gv.ID
	}
	return versions, nil
}

// sizedRecordSize returns the total size of a record with a 4-byte size prefix.
// Format: [size:4][data:size] → total = 4 + size.
// Used for node and doc records.
func sizedRecordSize(sdf *SharedDataFile, offset int64) (int, error) {
	var buf [4]byte
	if _, err := sdf.ReadAt(buf[:], offset); err != nil {
		return 0, err
	}
	size := binary.LittleEndian.Uint32(buf[:])
	return 4 + int(size), nil
}

// vectorRecordSize returns the total size of a vector record.
// Format: [nodeID:4][dim:4][float32 x dim] → total = 8 + dim*4.
func vectorRecordSize(sdf *SharedDataFile, offset int64) (int, error) {
	var buf [8]byte
	if _, err := sdf.ReadAt(buf[:], offset); err != nil {
		return 0, err
	}
	dim := binary.LittleEndian.Uint32(buf[4:8])
	return 8 + int(dim)*4, nil
}

// ShouldCompact returns true if the tombstone ratio warrants compaction
// rather than simple snapshot copy. The threshold is derived from the
// total node count: 1/log2(totalNodes).
func ShouldCompact(deadCount, totalNodes uint32) bool {
	if totalNodes == 0 {
		return false
	}
	logN := math.Log2(float64(totalNodes))
	if logN < 1 {
		return false
	}
	return float64(deadCount)/float64(totalNodes) > 1.0/logN
}

// HasGlobalVersions returns true if the global versions directory exists.
func (s *SylkDir) HasGlobalVersions() bool {
	info, err := os.Stat(s.GlobalVersionsPath())
	return err == nil && info.IsDir()
}

// writeDefaultConfig creates an initial config.yaml with defaults.
func (s *SylkDir) writeDefaultConfig() error {
	defaultConfig := `# Sylk Knowledge Graph Configuration
# Generated automatically - modify as needed

version: 1

embedding:
  provider: "voyage"
  model: "voyage-code-3"
  batch_size: 64

indexing:
  include_patterns:
    - "**/*.go"
    - "**/*.py"
    - "**/*.ts"
    - "**/*.js"
    - "**/*.rs"
    - "**/*.md"
  exclude_patterns:
    - "**/vendor/**"
    - "**/node_modules/**"
    - "**/.git/**"
    - "**/testdata/**"
  concurrency: 4

storage:
  node_block_size: 4096
  edge_shard_size: 65536
  vector_shard_size: 65536
`
	return os.WriteFile(s.ConfigPath(), []byte(defaultConfig), 0644)
}

// writeDefaultMeta creates an initial meta.json with defaults.
func (s *SylkDir) writeDefaultMeta() error {
	defaultMeta := `{
  "schema_version": 2,
  "version": {"major": 0, "minor": 0, "patch": 0},
  "next_node_id": 1,
  "next_session_id": 1,
  "committed_sessions": []
}
`
	return os.WriteFile(s.MetaPath(), []byte(defaultMeta), 0644)
}
