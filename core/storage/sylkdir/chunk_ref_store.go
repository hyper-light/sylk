package sylkdir

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// ErrChunkRefNotFound is returned when a chunk reference doesn't exist.
var ErrChunkRefNotFound = errors.New("sylkdir: chunk ref not found")

// ChunkRefStore writes chunk position references to the session's shared data file
// with per-version offset indexes for O(1) lookups and zero-copy checkpoints.
//
// Binary format per record: [NodeID:4][ChunkSpan:24] = 28 bytes.
//
// Concurrency model: single-writer-multiple-reader.
// Reads (Read, ReadAll, Count) are lock-free using sync.Map and OffsetIndex.
// Writes (Write, WriteBatch) are serialized via writeMu.
type ChunkRefStore struct {
	session *Session
	indexes sync.Map   // string -> *OffsetIndex
	writeMu sync.Mutex
}

// NewChunkRefStore creates a chunk ref store for the given session.
func NewChunkRefStore(sess *Session) *ChunkRefStore {
	s := &ChunkRefStore{session: sess}
	sess.RegisterChunkRefStore(s)
	return s
}

// Write writes a chunk reference to the current HEAD version.
func (s *ChunkRefStore) Write(ref *ChunkRef) error {
	return s.WriteToVersion(s.session.Manifest.Head, ref)
}

// WriteToVersion writes a chunk reference to a specific version.
func (s *ChunkRefStore) WriteToVersion(version SemanticVersion, ref *ChunkRef) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// WAL log before data write for crash safety.
	if s.session.WAL != nil {
		if _, err := s.session.WAL.LogChunkRefInsert(chunkRefToWALData(ref)); err != nil {
			return fmt.Errorf("wal log chunk ref: %w", err)
		}
	}

	record := ref.MarshalBinary()
	offset, err := s.session.ChunkDataFile.Append(record)
	if err != nil {
		return fmt.Errorf("append chunk ref: %w", err)
	}

	s.chunkIndex(version).Set(ref.NodeID, offset)
	return nil
}

// WriteBatch writes multiple chunk references to the current HEAD version.
func (s *ChunkRefStore) WriteBatch(refs []*ChunkRef) error {
	return s.WriteBatchToVersion(s.session.Manifest.Head, refs)
}

// WriteBatchToVersion writes multiple chunk references to a specific version.
// WAL entries are batched with a single fsync for the entire batch.
func (s *ChunkRefStore) WriteBatchToVersion(version SemanticVersion, refs []*ChunkRef) error {
	if len(refs) == 0 {
		return nil
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if err := s.walLogChunkRefBatch(refs); err != nil {
		return err
	}
	return s.appendChunkRefBatch(version, refs)
}

// walLogChunkRefBatch writes all chunk ref WAL entries with a single sync.
func (s *ChunkRefStore) walLogChunkRefBatch(refs []*ChunkRef) error {
	if s.session.WAL == nil {
		return nil
	}
	walData := make([]*WALChunkRefData, len(refs))
	for i, ref := range refs {
		walData[i] = chunkRefToWALData(ref)
	}
	return s.session.WAL.LogChunkRefBatch(walData)
}

// appendChunkRefBatch pre-marshals all refs, writes them in a single I/O via
// AppendBatch, and updates the index with SetBatch.
func (s *ChunkRefStore) appendChunkRefBatch(version SemanticVersion, refs []*ChunkRef) error {
	idx := s.chunkIndex(version)
	records := make([][]byte, len(refs))
	ids := make([]uint32, len(refs))
	for i, ref := range refs {
		records[i] = ref.MarshalBinary()
		ids[i] = ref.NodeID
	}
	offsets, err := s.session.ChunkDataFile.AppendBatch(records)
	if err != nil {
		return fmt.Errorf("append chunk refs: %w", err)
	}
	idx.SetBatch(ids, offsets)
	return nil
}

// Read reads a chunk reference from a specific version.
func (s *ChunkRefStore) ReadFromVersion(version SemanticVersion, nodeID uint32) (*ChunkRef, error) {
	idx := s.loadIndex(version)
	if idx == nil {
		return nil, ErrChunkRefNotFound
	}

	offset, ok := idx.Get(nodeID)
	if !ok {
		return nil, ErrChunkRefNotFound
	}

	return readChunkRefAtOffset(s.session.ChunkDataFile, offset)
}

// ReadFromAncestorChain reads a chunk reference from HEAD version.
func (s *ChunkRefStore) ReadFromAncestorChain(nodeID uint32) (*ChunkRef, error) {
	return s.ReadFromVersion(s.session.Manifest.Head, nodeID)
}

// ReadAllFromVersion reads all chunk references from a specific version.
func (s *ChunkRefStore) ReadAllFromVersion(version SemanticVersion) ([]*ChunkRef, error) {
	idx := s.loadIndex(version)
	if idx == nil {
		return []*ChunkRef{}, nil
	}

	refs := make([]*ChunkRef, 0, idx.Count())
	var readErr error
	idx.ForEach(func(_ uint32, offset int64) bool {
		ref, err := readChunkRefAtOffset(s.session.ChunkDataFile, offset)
		if err != nil {
			readErr = err
			return false
		}
		refs = append(refs, ref)
		return true
	})

	return refs, readErr
}

// ReadAllFromAncestorChain reads all chunk references from HEAD version.
func (s *ChunkRefStore) ReadAllFromAncestorChain() ([]*ChunkRef, error) {
	return s.ReadAllFromVersion(s.session.Manifest.Head)
}

// Count returns the number of chunk references in HEAD version.
func (s *ChunkRefStore) Count() int {
	idx := s.loadIndex(s.session.Manifest.Head)
	if idx == nil {
		return 0
	}
	return int(idx.Count())
}

// SaveIndexes saves all in-memory offset indexes to disk.
func (s *ChunkRefStore) SaveIndexes() error {
	var saveErr error
	s.indexes.Range(func(_, val any) bool {
		idx, ok := val.(*OffsetIndex)
		if !ok {
			return true
		}
		if err := idx.Save(); err != nil {
			saveErr = err
			return false
		}
		return true
	})
	return saveErr
}

// RegisterIndex stores an OffsetIndex in the in-memory map for a version.
func (s *ChunkRefStore) RegisterIndex(version SemanticVersion, idx *OffsetIndex) {
	s.indexes.Store(version.String(), idx)
}

// getInMemoryIndex returns the in-memory OffsetIndex for a version, or nil.
func (s *ChunkRefStore) getInMemoryIndex(version SemanticVersion) *OffsetIndex {
	if val, ok := s.indexes.Load(version.String()); ok {
		return val.(*OffsetIndex)
	}
	return nil
}

// chunkIndex returns the OffsetIndex for a version, creating if needed.
// Called under writeMu; uses sync.Map for storage.
func (s *ChunkRefStore) chunkIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.session.ChunkIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		idx = NewOffsetIndex(path, offsetIndexMinCapacity)
	}
	s.indexes.Store(vKey, idx)
	return idx
}

// loadIndex returns the OffsetIndex for a version (read path). Lock-free.
func (s *ChunkRefStore) loadIndex(version SemanticVersion) *OffsetIndex {
	vKey := version.String()
	if val, ok := s.indexes.Load(vKey); ok {
		return val.(*OffsetIndex)
	}
	path := s.session.ChunkIndexPath(version)
	idx, err := LoadOffsetIndex(path)
	if err != nil {
		return nil
	}
	actual, _ := s.indexes.LoadOrStore(vKey, idx)
	return actual.(*OffsetIndex)
}

// readChunkRefAtOffset reads a chunk reference from a shared data file at the given offset.
func readChunkRefAtOffset(sdf *SharedDataFile, offset int64) (*ChunkRef, error) {
	buf := make([]byte, ChunkRefRecordSize)
	if _, err := sdf.ReadAt(buf, offset); err != nil {
		return nil, fmt.Errorf("read chunk ref: %w", err)
	}

	ref := &ChunkRef{}
	if err := ref.UnmarshalBinary(buf); err != nil {
		return nil, err
	}
	return ref, nil
}

// chunkRefToWALData converts a ChunkRef to WAL data.
func chunkRefToWALData(ref *ChunkRef) *WALChunkRefData {
	return &WALChunkRefData{
		NodeID:    ref.NodeID,
		DocRef:    ref.Span.DocRef,
		ChunkSeq:  ref.Span.ChunkSeq,
		ByteStart: ref.Span.ByteStart,
		ByteEnd:   ref.Span.ByteEnd,
		LineStart: ref.Span.LineStart,
		LineEnd:   ref.Span.LineEnd,
		Flags:     ref.Span.Flags,
	}
}

// WALChunkRefData is the WAL payload for chunk ref insert operations.
type WALChunkRefData struct {
	NodeID    uint32
	DocRef    uint32
	ChunkSeq  uint16
	ByteStart uint32
	ByteEnd   uint32
	LineStart uint32
	LineEnd   uint32
	Flags     uint16
}

// walChunkRefFixedSize is the wire size of a WALChunkRefData record.
const walChunkRefFixedSize = ChunkRefRecordSize

// MarshalBinary encodes the WAL chunk ref data.
func (w *WALChunkRefData) MarshalBinary() []byte {
	buf := make([]byte, walChunkRefFixedSize)
	binary.LittleEndian.PutUint32(buf[0:4], w.NodeID)
	binary.LittleEndian.PutUint32(buf[4:8], w.DocRef)
	binary.LittleEndian.PutUint16(buf[8:10], w.ChunkSeq)
	binary.LittleEndian.PutUint32(buf[10:14], w.ByteStart)
	binary.LittleEndian.PutUint32(buf[14:18], w.ByteEnd)
	binary.LittleEndian.PutUint32(buf[18:22], w.LineStart)
	binary.LittleEndian.PutUint32(buf[22:26], w.LineEnd)
	binary.LittleEndian.PutUint16(buf[26:28], w.Flags)
	return buf
}

// UnmarshalBinary decodes the WAL chunk ref data.
func (w *WALChunkRefData) UnmarshalBinary(data []byte) error {
	if len(data) < walChunkRefFixedSize {
		return fmt.Errorf("sylkdir: wal chunk ref data too short: %d < %d", len(data), walChunkRefFixedSize)
	}
	w.NodeID = binary.LittleEndian.Uint32(data[0:4])
	w.DocRef = binary.LittleEndian.Uint32(data[4:8])
	w.ChunkSeq = binary.LittleEndian.Uint16(data[8:10])
	w.ByteStart = binary.LittleEndian.Uint32(data[10:14])
	w.ByteEnd = binary.LittleEndian.Uint32(data[14:18])
	w.LineStart = binary.LittleEndian.Uint32(data[18:22])
	w.LineEnd = binary.LittleEndian.Uint32(data[22:26])
	w.Flags = binary.LittleEndian.Uint16(data[26:28])
	return nil
}
