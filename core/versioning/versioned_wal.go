package versioning

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRetainedMajors  = 10
	DefaultMaxIndexEntries = 1000
)

var (
	ErrVersionedWALClosed   = errors.New("versioned WAL is closed")
	ErrVersionNotFoundInWAL = errors.New("version not found in WAL")
)

// VersionedWALConfig configures a disk-backed versioned WAL.
type VersionedWALConfig struct {
	Dir             string // .sylk/sessions/{sid}/wal/
	RetainedMajors  int    // default DefaultRetainedMajors
	MaxIndexEntries int    // default DefaultMaxIndexEntries
}

// VersionIndex is a lightweight in-memory entry pointing to a WAL file on disk.
type VersionIndex struct {
	Version    SemanticVersion `json:"version"`
	Filename   string          `json:"filename"`
	Kind       WALEntryKind    `json:"kind"`
	PipelineID string          `json:"pipeline_id,omitempty"`
	Timestamp  time.Time       `json:"timestamp"`
}

// VersionedWAL is a disk-backed write-ahead log with semantic versioning.
// Files are written atomically (write-temp-rename). The in-memory index
// is capped at MaxIndexEntries; older entries are evicted from memory
// but files remain on disk until Compact().
type VersionedWAL struct {
	mu       sync.Mutex
	dir      string
	index    []VersionIndex
	current  SemanticVersion
	nextSeq  uint64
	closed   bool
	retained int
	maxIndex int
}

var _ SemanticWAL = (*VersionedWAL)(nil)

// OpenVersionedWAL opens or creates a versioned WAL at the configured directory.
// If index.json exists it is loaded; otherwise the index is rebuilt from a dir scan.
func OpenVersionedWAL(cfg VersionedWALConfig) (*VersionedWAL, error) {
	retained := cfg.RetainedMajors
	if retained <= 0 {
		retained = DefaultRetainedMajors
	}
	maxIdx := cfg.MaxIndexEntries
	if maxIdx <= 0 {
		maxIdx = DefaultMaxIndexEntries
	}

	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, fmt.Errorf("versioned wal: mkdir %s: %w", cfg.Dir, err)
	}

	w := &VersionedWAL{
		dir:      cfg.Dir,
		retained: retained,
		maxIndex: maxIdx,
	}

	if err := w.loadIndex(); err != nil {
		return nil, err
	}

	return w, nil
}

// AppendDelta bumps the minor version and writes a delta entry to disk.
func (w *VersionedWAL) AppendDelta(pipelineID string, deltas []WALFileDelta) (SemanticVersion, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return SemanticVersion{}, ErrVersionedWALClosed
	}

	newVer := w.current.BumpMinor()
	return w.appendEntry(WALEntryKindDelta, newVer, pipelineID, deltas)
}

// AppendCheckpoint bumps the major version (minor=0) and writes a checkpoint entry.
func (w *VersionedWAL) AppendCheckpoint(deltas []WALFileDelta) (SemanticVersion, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return SemanticVersion{}, ErrVersionedWALClosed
	}

	newVer := w.current.BumpMajor()
	return w.appendEntry(WALEntryKindCheckpoint, newVer, "", deltas)
}

// GetEntry reads and decodes the WAL entry for the given version.
func (w *VersionedWAL) GetEntry(ver SemanticVersion) (*VersionedWALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrVersionedWALClosed
	}

	idx, ok := w.findIndex(ver)
	if !ok {
		return nil, ErrVersionNotFoundInWAL
	}

	return w.readEntryFile(idx.Filename)
}

// GetDeltasSince returns all entries with version strictly greater than the given version.
func (w *VersionedWAL) GetDeltasSince(ver SemanticVersion) ([]VersionedWALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrVersionedWALClosed
	}

	var result []VersionedWALEntry
	for _, idx := range w.index {
		if idx.Version.Compare(ver) > 0 {
			entry, err := w.readEntryFile(idx.Filename)
			if err != nil {
				return nil, err
			}
			result = append(result, *entry)
		}
	}
	return result, nil
}

// GetDeltasInRange returns entries with version in [from, to] inclusive.
func (w *VersionedWAL) GetDeltasInRange(from, to SemanticVersion) ([]VersionedWALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrVersionedWALClosed
	}

	var result []VersionedWALEntry
	for _, idx := range w.index {
		if idx.Version.Compare(from) >= 0 && idx.Version.Compare(to) <= 0 {
			entry, err := w.readEntryFile(idx.Filename)
			if err != nil {
				return nil, err
			}
			result = append(result, *entry)
		}
	}
	return result, nil
}

// CurrentVersion returns the latest version in the WAL.
func (w *VersionedWAL) CurrentVersion() SemanticVersion {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current
}

// LatestCheckpoint returns the most recent checkpoint version, if any.
func (w *VersionedWAL) LatestCheckpoint() (SemanticVersion, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := len(w.index) - 1; i >= 0; i-- {
		if w.index[i].Kind == WALEntryKindCheckpoint {
			return w.index[i].Version, true
		}
	}
	return SemanticVersion{}, false
}

// Compact deletes WAL files for versions before (current.Major - retained) checkpoint.
func (w *VersionedWAL) Compact() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrVersionedWALClosed
	}

	if w.current.Major <= uint32(w.retained) {
		return nil
	}

	cutoffMajor := w.current.Major - uint32(w.retained)
	kept := make([]VersionIndex, 0, len(w.index))

	for _, idx := range w.index {
		if idx.Version.Major < cutoffMajor {
			os.Remove(filepath.Join(w.dir, idx.Filename))
		} else {
			kept = append(kept, idx)
		}
	}

	w.index = kept
	return w.persistIndex()
}

// IndexLen returns the number of entries in the in-memory index.
func (w *VersionedWAL) IndexLen() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.index)
}

// Close closes the WAL.
func (w *VersionedWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true
	return w.persistIndex()
}

// --- internal ---

func (w *VersionedWAL) appendEntry(kind WALEntryKind, ver SemanticVersion, pipelineID string, deltas []WALFileDelta) (SemanticVersion, error) {
	w.nextSeq++
	filename := w.entryFilename(w.nextSeq, kind)

	entry := &VersionedWALEntry{
		Kind:       kind,
		Version:    ver,
		PipelineID: pipelineID,
		Timestamp:  time.Now(),
		Deltas:     deltas,
	}

	if err := w.writeEntryFile(filename, entry); err != nil {
		w.nextSeq--
		return SemanticVersion{}, err
	}

	w.current = ver
	w.index = append(w.index, VersionIndex{
		Version:    ver,
		Filename:   filename,
		Kind:       kind,
		PipelineID: pipelineID,
		Timestamp:  entry.Timestamp,
	})

	w.evictOldIndexEntries()

	if err := w.persistIndex(); err != nil {
		return ver, err
	}

	return ver, nil
}

func (w *VersionedWAL) entryFilename(seq uint64, kind WALEntryKind) string {
	ext := "delta"
	if kind == WALEntryKindCheckpoint {
		ext = "chkpt"
	}
	return fmt.Sprintf("%06d.%s", seq, ext)
}

func (w *VersionedWAL) writeEntryFile(filename string, entry *VersionedWALEntry) error {
	data := EncodeWALEntry(entry)
	tmpPath := filepath.Join(w.dir, filename+".tmp")
	finalPath := filepath.Join(w.dir, filename)

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("versioned wal: write tmp: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("versioned wal: rename: %w", err)
	}
	return nil
}

func (w *VersionedWAL) readEntryFile(filename string) (*VersionedWALEntry, error) {
	data, err := os.ReadFile(filepath.Join(w.dir, filename))
	if err != nil {
		return nil, fmt.Errorf("versioned wal: read %s: %w", filename, err)
	}
	return DecodeWALEntry(data)
}

func (w *VersionedWAL) findIndex(ver SemanticVersion) (VersionIndex, bool) {
	for _, idx := range w.index {
		if idx.Version == ver {
			return idx, true
		}
	}
	return VersionIndex{}, false
}

func (w *VersionedWAL) evictOldIndexEntries() {
	if len(w.index) <= w.maxIndex {
		return
	}
	excess := len(w.index) - w.maxIndex
	w.index = w.index[excess:]
}

// loadIndex tries to load index.json; falls back to dir scan.
func (w *VersionedWAL) loadIndex() error {
	indexPath := filepath.Join(w.dir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return w.rebuildIndexFromDir()
	}

	var entries []VersionIndex
	if err := json.Unmarshal(data, &entries); err != nil {
		return w.rebuildIndexFromDir()
	}

	w.index = entries
	w.recoverStateFromIndex()
	return nil
}

func (w *VersionedWAL) rebuildIndexFromDir() error {
	dirEntries, err := os.ReadDir(w.dir)
	if err != nil {
		return fmt.Errorf("versioned wal: readdir: %w", err)
	}

	var files []walFileInfo
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		info, ok := parseWALFilename(de.Name())
		if !ok {
			continue
		}
		files = append(files, info)
	}

	sort.Slice(files, func(i, j int) bool { return files[i].seq < files[j].seq })

	w.index = make([]VersionIndex, 0, len(files))
	for _, f := range files {
		entry, err := w.readEntryFile(f.name)
		if err != nil {
			continue
		}
		w.index = append(w.index, VersionIndex{
			Version:    entry.Version,
			Filename:   f.name,
			Kind:       entry.Kind,
			PipelineID: entry.PipelineID,
			Timestamp:  entry.Timestamp,
		})
	}

	w.recoverStateFromIndex()
	return w.persistIndex()
}

func (w *VersionedWAL) recoverStateFromIndex() {
	w.nextSeq = 0
	w.current = SemanticVersion{}

	for _, idx := range w.index {
		if idx.Version.Compare(w.current) > 0 {
			w.current = idx.Version
		}
		seq := seqFromFilename(idx.Filename)
		if seq > w.nextSeq {
			w.nextSeq = seq
		}
	}
}

func (w *VersionedWAL) persistIndex() error {
	data, err := json.Marshal(w.index)
	if err != nil {
		return fmt.Errorf("versioned wal: marshal index: %w", err)
	}

	tmpPath := filepath.Join(w.dir, "index.json.tmp")
	finalPath := filepath.Join(w.dir, "index.json")

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("versioned wal: write index tmp: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("versioned wal: rename index: %w", err)
	}
	return nil
}

type walFileInfo struct {
	name string
	seq  uint64
	kind WALEntryKind
}

func parseWALFilename(name string) (walFileInfo, bool) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return walFileInfo{}, false
	}

	var kind WALEntryKind
	switch parts[1] {
	case "delta":
		kind = WALEntryKindDelta
	case "chkpt":
		kind = WALEntryKindCheckpoint
	default:
		return walFileInfo{}, false
	}

	seq, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return walFileInfo{}, false
	}

	return walFileInfo{name: name, seq: seq, kind: kind}, true
}

func seqFromFilename(name string) uint64 {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return 0
	}
	seq, _ := strconv.ParseUint(parts[0], 10, 64)
	return seq
}
