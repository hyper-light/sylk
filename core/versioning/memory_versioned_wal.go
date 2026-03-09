package versioning

import (
	"errors"
	"sync"
	"time"
)

// MemoryVersionedWAL keeps semantic WAL state entirely in memory for the
// lifetime of a live SessionVFS. It intentionally does not persist or recover.
type MemoryVersionedWAL struct {
	mu      sync.Mutex
	index   []VersionIndex
	entries map[SemanticVersion]VersionedWALEntry
	current SemanticVersion
	nextSeq uint64
	closed  bool
}

// NewMemoryVersionedWAL creates an empty in-memory semantic WAL.
func NewMemoryVersionedWAL() *MemoryVersionedWAL {
	return &MemoryVersionedWAL{
		entries: make(map[SemanticVersion]VersionedWALEntry),
	}
}

func (w *MemoryVersionedWAL) AppendDelta(pipelineID string, deltas []WALFileDelta) (SemanticVersion, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return SemanticVersion{}, ErrVersionedWALClosed
	}

	newVer := w.current.BumpMinor()
	return w.appendEntryLocked(WALEntryKindDelta, newVer, pipelineID, deltas)
}

func (w *MemoryVersionedWAL) AppendCheckpoint(deltas []WALFileDelta) (SemanticVersion, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return SemanticVersion{}, ErrVersionedWALClosed
	}

	newVer := w.current.BumpMajor()
	return w.appendEntryLocked(WALEntryKindCheckpoint, newVer, "", deltas)
}

func (w *MemoryVersionedWAL) GetEntry(ver SemanticVersion) (*VersionedWALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrVersionedWALClosed
	}
	entry, ok := w.entries[ver]
	if !ok {
		return nil, ErrVersionNotFoundInWAL
	}
	cloned := cloneVersionedWALEntry(entry)
	return &cloned, nil
}

func (w *MemoryVersionedWAL) GetDeltasSince(ver SemanticVersion) ([]VersionedWALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrVersionedWALClosed
	}

	result := make([]VersionedWALEntry, 0, len(w.index))
	for _, idx := range w.index {
		if idx.Version.Compare(ver) <= 0 {
			continue
		}
		entry, ok := w.entries[idx.Version]
		if !ok {
			return nil, errors.New("memory semantic wal: missing indexed entry")
		}
		result = append(result, cloneVersionedWALEntry(entry))
	}
	return result, nil
}

func (w *MemoryVersionedWAL) GetDeltasInRange(from, to SemanticVersion) ([]VersionedWALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil, ErrVersionedWALClosed
	}

	result := make([]VersionedWALEntry, 0, len(w.index))
	for _, idx := range w.index {
		if idx.Version.Compare(from) < 0 || idx.Version.Compare(to) > 0 {
			continue
		}
		entry, ok := w.entries[idx.Version]
		if !ok {
			return nil, errors.New("memory semantic wal: missing indexed entry")
		}
		result = append(result, cloneVersionedWALEntry(entry))
	}
	return result, nil
}

func (w *MemoryVersionedWAL) CurrentVersion() SemanticVersion {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current
}

func (w *MemoryVersionedWAL) LatestCheckpoint() (SemanticVersion, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := len(w.index) - 1; i >= 0; i-- {
		if w.index[i].Kind == WALEntryKindCheckpoint {
			return w.index[i].Version, true
		}
	}
	return SemanticVersion{}, false
}

// Compact is a no-op for the in-memory live semantic WAL.
func (w *MemoryVersionedWAL) Compact() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrVersionedWALClosed
	}
	return nil
}

func (w *MemoryVersionedWAL) IndexLen() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.index)
}

func (w *MemoryVersionedWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return nil
}

func (w *MemoryVersionedWAL) appendEntryLocked(kind WALEntryKind, ver SemanticVersion, pipelineID string, deltas []WALFileDelta) (SemanticVersion, error) {
	w.nextSeq++
	entry := VersionedWALEntry{
		Kind:       kind,
		Version:    ver,
		PipelineID: pipelineID,
		Timestamp:  time.Now(),
		Deltas:     cloneWALFileDeltas(deltas),
	}

	w.current = ver
	w.index = append(w.index, VersionIndex{
		Version:    ver,
		Filename:   "",
		Kind:       kind,
		PipelineID: pipelineID,
		Timestamp:  entry.Timestamp,
	})
	w.entries[ver] = entry
	return ver, nil
}

func cloneVersionedWALEntry(entry VersionedWALEntry) VersionedWALEntry {
	cloned := entry
	cloned.Deltas = cloneWALFileDeltas(entry.Deltas)
	return cloned
}

func cloneWALFileDeltas(deltas []WALFileDelta) []WALFileDelta {
	if len(deltas) == 0 {
		return nil
	}
	cloned := make([]WALFileDelta, len(deltas))
	for i, delta := range deltas {
		cloned[i] = delta
		cloned[i].NewContent = cloneBytes(delta.NewContent)
		cloned[i].OldContent = cloneBytes(delta.OldContent)
	}
	return cloned
}

var _ SemanticWAL = (*MemoryVersionedWAL)(nil)
