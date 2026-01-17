package versioning

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"sync"
	"time"
)

var (
	ErrWALCorrupted    = errors.New("WAL entry corrupted")
	ErrWALClosed       = errors.New("WAL is closed")
	ErrInvalidChecksum = errors.New("invalid checksum")
)

type WALEntryType uint8

const (
	WALEntryOperation WALEntryType = iota
	WALEntryVersion
	WALEntryCommit
	WALEntryRollback
	WALEntryCheckpoint
)

type WALEntry struct {
	Type       WALEntryType
	SequenceID uint64
	Timestamp  time.Time
	PipelineID string
	SessionID  SessionID
	Data       []byte
	Checksum   uint32
}

type WriteAheadLog interface {
	Append(entry WALEntry) (uint64, error)
	Get(sequenceID uint64) (*WALEntry, error)
	GetRange(start, end uint64) ([]*WALEntry, error)
	GetSince(sequenceID uint64) ([]*WALEntry, error)
	Checkpoint() (uint64, error)
	Truncate(beforeSequenceID uint64) error
	LastSequenceID() uint64
	Close() error
}

type MemoryWAL struct {
	mu           sync.RWMutex
	entries      []WALEntry
	sequenceID   uint64
	checkpointID uint64
	closed       bool
	maxEntries   int
	onCheckpoint func(uint64)
}

type WALOption func(*MemoryWAL)

func WithMaxEntries(max int) WALOption {
	return func(w *MemoryWAL) {
		w.maxEntries = max
	}
}

func WithCheckpointCallback(cb func(uint64)) WALOption {
	return func(w *MemoryWAL) {
		w.onCheckpoint = cb
	}
}

func NewMemoryWAL(opts ...WALOption) *MemoryWAL {
	wal := &MemoryWAL{
		entries:    make([]WALEntry, 0),
		maxEntries: 10000,
	}
	for _, opt := range opts {
		opt(wal)
	}
	return wal
}

func (w *MemoryWAL) Append(entry WALEntry) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrWALClosed
	}

	w.sequenceID++
	entry.SequenceID = w.sequenceID
	entry.Timestamp = time.Now()
	entry.Checksum = computeChecksum(entry.Data)

	w.entries = append(w.entries, entry)
	w.maybeAutoTruncate()

	return entry.SequenceID, nil
}

func computeChecksum(data []byte) uint32 {
	return crc32.ChecksumIEEE(data)
}

func (w *MemoryWAL) maybeAutoTruncate() {
	if w.maxEntries > 0 && len(w.entries) > w.maxEntries*2 {
		cutoff := len(w.entries) - w.maxEntries
		w.entries = w.entries[cutoff:]
	}
}

func (w *MemoryWAL) Get(sequenceID uint64) (*WALEntry, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.closed {
		return nil, ErrWALClosed
	}

	entry := w.findEntry(sequenceID)
	if entry == nil {
		return nil, nil
	}

	return w.cloneEntry(entry), nil
}

func (w *MemoryWAL) findEntry(sequenceID uint64) *WALEntry {
	for i := range w.entries {
		if w.entries[i].SequenceID == sequenceID {
			return &w.entries[i]
		}
	}
	return nil
}

func (w *MemoryWAL) cloneEntry(entry *WALEntry) *WALEntry {
	clone := *entry
	clone.Data = make([]byte, len(entry.Data))
	copy(clone.Data, entry.Data)
	return &clone
}

func (w *MemoryWAL) GetRange(start, end uint64) ([]*WALEntry, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.closed {
		return nil, ErrWALClosed
	}

	return w.collectEntriesInRange(start, end), nil
}

func (w *MemoryWAL) collectEntriesInRange(start, end uint64) []*WALEntry {
	entries := make([]*WALEntry, 0)
	for i := range w.entries {
		seq := w.entries[i].SequenceID
		if seq >= start && seq <= end {
			entries = append(entries, w.cloneEntry(&w.entries[i]))
		}
	}
	return entries
}

func (w *MemoryWAL) GetSince(sequenceID uint64) ([]*WALEntry, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.closed {
		return nil, ErrWALClosed
	}

	return w.collectEntriesSince(sequenceID), nil
}

func (w *MemoryWAL) collectEntriesSince(sequenceID uint64) []*WALEntry {
	entries := make([]*WALEntry, 0)
	for i := range w.entries {
		if w.entries[i].SequenceID > sequenceID {
			entries = append(entries, w.cloneEntry(&w.entries[i]))
		}
	}
	return entries
}

func (w *MemoryWAL) Checkpoint() (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrWALClosed
	}

	w.checkpointID = w.sequenceID
	if w.onCheckpoint != nil {
		w.onCheckpoint(w.checkpointID)
	}
	return w.checkpointID, nil
}

func (w *MemoryWAL) Truncate(beforeSequenceID uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrWALClosed
	}

	w.entries = w.filterEntriesAfter(beforeSequenceID)
	return nil
}

func (w *MemoryWAL) filterEntriesAfter(sequenceID uint64) []WALEntry {
	filtered := make([]WALEntry, 0)
	for _, entry := range w.entries {
		if entry.SequenceID >= sequenceID {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (w *MemoryWAL) LastSequenceID() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.sequenceID
}

func (w *MemoryWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.closed = true
	return nil
}

func (w *MemoryWAL) IsClosed() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.closed
}

func (w *MemoryWAL) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.entries)
}

func EncodeOperation(op *Operation) []byte {
	data := make([]byte, 0, 256)
	data = append(data, op.ID[:]...)
	data = append(data, op.BaseVersion[:]...)
	data = appendLengthPrefixed(data, []byte(op.FilePath))
	data = appendLengthPrefixed(data, []byte(op.PipelineID))
	data = appendLengthPrefixed(data, []byte(op.SessionID))
	data = appendLengthPrefixed(data, op.Content)
	return data
}

func appendLengthPrefixed(data, value []byte) []byte {
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(value)))
	data = append(data, lenBuf...)
	data = append(data, value...)
	return data
}

func ValidateChecksum(entry *WALEntry) bool {
	return entry.Checksum == computeChecksum(entry.Data)
}
