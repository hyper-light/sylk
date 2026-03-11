package sylkdir

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/wal"
)

// walSegmentCapacity is the entry count at which segment rotation occurs.
// Derived from the shard addressing bit width (16 bits → 65536 entries),
// keeping segment sizes aligned with the IVF shard structure.
const walSegmentCapacity = 1 << 16

const (
	sessionWALPrefix      = "session.wal."
	sessionCheckpointFile = "session.checkpoint"
)

// SessionWAL provides a segment-based write-ahead log for session mutations.
// Each log entry uses the shared WAL wire format (wal.WALEntry) and is
// dispatched by OpType during replay.
type SessionWAL struct {
	dir         string
	syncOnWrite bool

	mu              sync.Mutex
	sequence        atomic.Uint64
	currentSegment  *os.File
	currentSegNum   uint64
	currentEntries  int
	checkpointedSeq uint64
}

// SessionWALConfig configures a SessionWAL.
type SessionWALConfig struct {
	Dir         string
	SyncOnWrite bool
}

// OpenSessionWAL opens or creates a session WAL at the given directory.
func OpenSessionWAL(cfg SessionWALConfig) (*SessionWAL, error) {
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, fmt.Errorf("session wal: create dir: %w", err)
	}

	w := &SessionWAL{
		dir:         cfg.Dir,
		syncOnWrite: cfg.SyncOnWrite,
	}

	return w, w.initialize()
}

// initialize loads checkpoint, lists segments, GCs stale ones, and opens current.
func (w *SessionWAL) initialize() error {
	checkpointedSeq, err := w.loadCheckpoint()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session wal: load checkpoint: %w", err)
	}
	w.checkpointedSeq = checkpointedSeq

	segments, err := w.listSegments()
	if err != nil {
		return fmt.Errorf("session wal: list segments: %w", err)
	}

	if err := w.gcSegments(segments); err != nil {
		return fmt.Errorf("session wal: gc: %w", err)
	}

	return w.resumeOrCreate()
}

// resumeOrCreate resumes the last segment or creates a new one.
func (w *SessionWAL) resumeOrCreate() error {
	segments, err := w.listSegments()
	if err != nil {
		return err
	}

	var lastSeq, lastSegNum uint64
	if len(segments) > 0 {
		lastSegNum = segments[len(segments)-1]
		lastSeq, err = w.findLastSequence(lastSegNum)
		if err != nil {
			return err
		}
	}

	w.sequence.Store(lastSeq)

	nextSeg := lastSegNum
	switch {
	case lastSeq > 0:
		nextSeg = lastSegNum + 1
	case len(segments) == 0:
		nextSeg = 1
	}

	return w.openSegment(nextSeg)
}

// --- Log methods (one per session OpType) ---

// LogNodeInsert logs a node insertion.
func (w *SessionWAL) LogNodeInsert(d *WALNodeData) (uint64, error) {
	data, err := EncodeNodeData(d)
	if err != nil {
		return 0, err
	}
	return w.append(wal.OpNodeInsert, data)
}

// LogNodeDelete logs a node deletion.
func (w *SessionWAL) LogNodeDelete(d *WALNodeData) (uint64, error) {
	data, err := EncodeNodeData(d)
	if err != nil {
		return 0, err
	}
	return w.append(wal.OpNodeDelete, data)
}

// LogEdgeInsert logs an edge insertion.
func (w *SessionWAL) LogEdgeInsert(d *WALEdgeData) (uint64, error) {
	return w.append(wal.OpEdgeInsert, EncodeEdgeData(d))
}

// LogEdgeUpdate logs an edge update.
func (w *SessionWAL) LogEdgeUpdate(d *WALEdgeData) (uint64, error) {
	return w.append(wal.OpEdgeUpdate, EncodeEdgeData(d))
}

// LogEdgeDelete logs an edge deletion.
func (w *SessionWAL) LogEdgeDelete(d *WALEdgeData) (uint64, error) {
	return w.append(wal.OpEdgeDelete, EncodeEdgeData(d))
}

// LogVectorInsert logs a vector insertion.
func (w *SessionWAL) LogVectorInsert(d *WALVectorData) (uint64, error) {
	return w.append(wal.OpVectorInsert, EncodeVectorData(d))
}

// LogDocInsert logs a document insertion.
func (w *SessionWAL) LogDocInsert(d *WALDocData) (uint64, error) {
	data, err := EncodeDocData(d)
	if err != nil {
		return 0, err
	}
	return w.append(wal.OpDocInsert, data)
}

// LogChunkRefInsert logs a chunk reference insertion.
func (w *SessionWAL) LogChunkRefInsert(d *WALChunkRefData) (uint64, error) {
	return w.append(wal.OpChunkRefInsert, d.MarshalBinary())
}

// --- Batch log methods (single sync per batch) ---

// LogNodeBatch logs multiple node insertions with a single sync.
func (w *SessionWAL) LogNodeBatch(nodes []*WALNodeData) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, d := range nodes {
		if err := w.logNodeInsertLocked(d); err != nil {
			return err
		}
	}
	return w.syncLocked()
}

// logNodeInsertLocked encodes and appends a single node entry. Caller holds w.mu.
func (w *SessionWAL) logNodeInsertLocked(d *WALNodeData) error {
	data, err := EncodeNodeData(d)
	if err != nil {
		return err
	}
	_, err = w.appendLocked(wal.OpNodeInsert, data)
	return err
}

// LogEdgeBatch logs multiple edge insertions with a single sync.
func (w *SessionWAL) LogEdgeBatch(edges []*WALEdgeData) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, d := range edges {
		if _, err := w.appendLocked(wal.OpEdgeInsert, EncodeEdgeData(d)); err != nil {
			return err
		}
	}
	return w.syncLocked()
}

// LogDocBatch logs multiple document insertions with a single sync.
func (w *SessionWAL) LogDocBatch(docs []*WALDocData) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, d := range docs {
		if err := w.logDocInsertLocked(d); err != nil {
			return err
		}
	}
	return w.syncLocked()
}

// logDocInsertLocked encodes and appends a single doc entry. Caller holds w.mu.
func (w *SessionWAL) logDocInsertLocked(d *WALDocData) error {
	data, err := EncodeDocData(d)
	if err != nil {
		return err
	}
	_, err = w.appendLocked(wal.OpDocInsert, data)
	return err
}

// LogVectorBatch logs multiple vector insertions with a single sync.
func (w *SessionWAL) LogVectorBatch(vectors []*WALVectorData) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, d := range vectors {
		if _, err := w.appendLocked(wal.OpVectorInsert, EncodeVectorData(d)); err != nil {
			return err
		}
	}
	return w.syncLocked()
}

// LogChunkRefBatch logs multiple chunk ref insertions with a single sync.
func (w *SessionWAL) LogChunkRefBatch(refs []*WALChunkRefData) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, d := range refs {
		if _, err := w.appendLocked(wal.OpChunkRefInsert, d.MarshalBinary()); err != nil {
			return err
		}
	}
	return w.syncLocked()
}

// --- Replay ---

// SessionReplayHandler holds optional callbacks for each session OpType.
// Nil callbacks are silently skipped during replay.
type SessionReplayHandler struct {
	OnNodeInsert     func(*WALNodeData) error
	OnNodeDelete     func(*WALNodeData) error
	OnEdgeInsert     func(*WALEdgeData) error
	OnEdgeUpdate     func(*WALEdgeData) error
	OnEdgeDelete     func(*WALEdgeData) error
	OnVectorInsert   func(*WALVectorData) error
	OnDocInsert      func(*WALDocData) error
	OnChunkRefInsert func(*WALChunkRefData) error
}

// SessionReplayResult summarizes a replay.
type SessionReplayResult struct {
	EntriesRead  int
	LastSequence uint64
	Counts       map[wal.OpType]int
}

// Replay reads all WAL entries after the checkpoint and dispatches
// them through the handler callbacks.
func (w *SessionWAL) Replay(handler SessionReplayHandler) (*SessionReplayResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	dispatch := buildSessionDispatch(handler)

	segments, err := w.listSegments()
	if err != nil {
		return nil, err
	}

	result := &SessionReplayResult{Counts: make(map[wal.OpType]int)}

	for _, segNum := range segments {
		if err := w.replaySegment(segNum, dispatch, result); err != nil {
			return result, err
		}
	}

	return result, nil
}

// replaySegment reads and dispatches entries from a single segment.
func (w *SessionWAL) replaySegment(segNum uint64, dispatch [256]func([]byte) error, result *SessionReplayResult) error {
	entries, err := w.readSegment(segNum)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if fn := dispatch[entry.OpType]; fn != nil {
			if err := fn(entry.Data); err != nil {
				return fmt.Errorf("session wal: replay seq %d: %w", entry.SequenceID, err)
			}
		}
		result.EntriesRead++
		result.Counts[entry.OpType]++
		result.LastSequence = entry.SequenceID
	}

	return nil
}

// wrapDecoder creates a dispatch function by composing a decoder with a handler.
// Returns nil if handle is nil (used to skip unregistered ops).
func wrapDecoder[T any](decode func([]byte) (*T, error), handle func(*T) error) func([]byte) error {
	if handle == nil {
		return nil
	}
	return func(data []byte) error {
		d, err := decode(data)
		if err != nil {
			return err
		}
		return handle(d)
	}
}

// buildSessionDispatch builds a [256] dispatch table from a SessionReplayHandler.
func buildSessionDispatch(h SessionReplayHandler) [256]func([]byte) error {
	var table [256]func([]byte) error
	table[wal.OpNodeInsert] = wrapDecoder(DecodeNodeData, h.OnNodeInsert)
	table[wal.OpNodeDelete] = wrapDecoder(DecodeNodeData, h.OnNodeDelete)
	table[wal.OpEdgeInsert] = wrapDecoder(DecodeEdgeData, h.OnEdgeInsert)
	table[wal.OpEdgeUpdate] = wrapDecoder(DecodeEdgeData, h.OnEdgeUpdate)
	table[wal.OpEdgeDelete] = wrapDecoder(DecodeEdgeData, h.OnEdgeDelete)
	table[wal.OpVectorInsert] = wrapDecoder(DecodeVectorData, h.OnVectorInsert)
	table[wal.OpDocInsert] = wrapDecoder(DecodeDocData, h.OnDocInsert)
	table[wal.OpChunkRefInsert] = wrapDecoder(decodeChunkRefData, h.OnChunkRefInsert)
	return table
}

// decodeChunkRefData decodes WALChunkRefData from binary.
func decodeChunkRefData(data []byte) (*WALChunkRefData, error) {
	d := &WALChunkRefData{}
	return d, d.UnmarshalBinary(data)
}

// --- Core WAL infrastructure ---

// append writes a new entry to the current segment, rotating if needed.
func (w *SessionWAL) append(opType wal.OpType, data []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	seq, err := w.appendLocked(opType, data)
	if err != nil {
		return 0, err
	}
	return seq, w.syncLocked()
}

// appendLocked writes a single WAL entry without acquiring the lock or syncing.
// Caller must hold w.mu and call syncLocked after the batch completes.
func (w *SessionWAL) appendLocked(opType wal.OpType, data []byte) (uint64, error) {
	if w.currentEntries >= walSegmentCapacity {
		if err := w.rotateSegment(); err != nil {
			return 0, fmt.Errorf("session wal: rotate: %w", err)
		}
	}

	seq := w.sequence.Add(1)
	entryBytes, err := w.marshalEntry(seq, opType, data)
	if err != nil {
		return 0, err
	}

	if _, err := w.currentSegment.Write(entryBytes); err != nil {
		return 0, fmt.Errorf("session wal: write: %w", err)
	}

	w.currentEntries++
	return seq, nil
}

// marshalEntry creates and serializes a WAL entry.
func (w *SessionWAL) marshalEntry(seq uint64, opType wal.OpType, data []byte) ([]byte, error) {
	entry := wal.WALEntry{
		SequenceID: seq,
		OpType:     opType,
		Timestamp:  time.Now().UnixNano(),
		Data:       data,
	}
	return entry.MarshalBinary()
}

// syncLocked syncs the current segment if syncOnWrite is enabled.
// Caller must hold w.mu.
func (w *SessionWAL) syncLocked() error {
	if w.syncOnWrite && w.currentSegment != nil {
		return w.currentSegment.Sync()
	}
	return nil
}

// Checkpoint saves the checkpoint sequence and GCs old segments.
func (w *SessionWAL) Checkpoint(seq uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.currentSegment.Sync(); err != nil {
		return fmt.Errorf("session wal: sync before checkpoint: %w", err)
	}

	if err := w.saveCheckpoint(seq); err != nil {
		return fmt.Errorf("session wal: save checkpoint: %w", err)
	}

	w.checkpointedSeq = seq

	segments, err := w.listSegments()
	if err != nil {
		return fmt.Errorf("session wal: list segments: %w", err)
	}

	return w.gcSegments(segments)
}

// Truncate removes all WAL segments and resets state.
func (w *SessionWAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentSegment != nil {
		w.currentSegment.Close()
		w.currentSegment = nil
	}

	segments, err := w.listSegments()
	if err != nil {
		return err
	}

	for _, segNum := range segments {
		if err := os.Remove(w.segmentPath(segNum)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	w.sequence.Store(0)
	w.checkpointedSeq = 0
	w.currentEntries = 0

	if err := os.Remove(w.checkpointPath()); err != nil && !os.IsNotExist(err) {
		return err
	}

	return w.openSegment(1)
}

// Sync fsyncs the current segment.
func (w *SessionWAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentSegment != nil {
		return w.currentSegment.Sync()
	}
	return nil
}

// Close syncs and closes the WAL.
func (w *SessionWAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentSegment == nil {
		return nil
	}

	if err := w.currentSegment.Sync(); err != nil {
		w.currentSegment.Close()
		return err
	}
	return w.currentSegment.Close()
}

// LastSequence returns the last written sequence number.
func (w *SessionWAL) LastSequence() uint64 {
	return w.sequence.Load()
}

// CheckpointedSequence returns the checkpointed sequence.
func (w *SessionWAL) CheckpointedSequence() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.checkpointedSeq
}

// WALBytes returns the total size of all WAL segment files in bytes.
func (w *SessionWAL) WALBytes() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	segments, err := w.listSegments()
	if err != nil {
		return 0
	}

	var total int64
	for _, segNum := range segments {
		if info, err := os.Stat(w.segmentPath(segNum)); err == nil {
			total += info.Size()
		}
	}
	return total
}

// ReadAll reads all entries after the checkpoint.
func (w *SessionWAL) ReadAll() ([]wal.WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	segments, err := w.listSegments()
	if err != nil {
		return nil, err
	}

	var all []wal.WALEntry
	for _, segNum := range segments {
		entries, err := w.readSegment(segNum)
		if err != nil {
			return all, err
		}
		all = append(all, entries...)
	}

	return all, nil
}

// --- Segment management ---

func (w *SessionWAL) segmentPath(segNum uint64) string {
	return filepath.Join(w.dir, fmt.Sprintf("%s%06d", sessionWALPrefix, segNum))
}

func (w *SessionWAL) checkpointPath() string {
	return filepath.Join(w.dir, sessionCheckpointFile)
}

func (w *SessionWAL) openSegment(segNum uint64) error {
	path := w.segmentPath(segNum)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}

	if stat.Size() == 0 {
		header := wal.NewWALHeader()
		headerBytes, _ := header.MarshalBinary()
		if _, err := file.Write(headerBytes); err != nil {
			file.Close()
			return err
		}
	} else {
		if _, err := file.Seek(0, io.SeekEnd); err != nil {
			file.Close()
			return err
		}
	}

	w.currentSegment = file
	w.currentSegNum = segNum
	w.currentEntries = 0
	return nil
}

func (w *SessionWAL) rotateSegment() error {
	if w.currentSegment != nil {
		if err := w.currentSegment.Sync(); err != nil {
			return err
		}
		if err := w.currentSegment.Close(); err != nil {
			return err
		}
	}
	return w.openSegment(w.currentSegNum + 1)
}

func (w *SessionWAL) listSegments() ([]uint64, error) {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return nil, err
	}

	var segments []uint64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, sessionWALPrefix) {
			continue
		}
		numStr := strings.TrimPrefix(name, sessionWALPrefix)
		num, err := strconv.ParseUint(numStr, 10, 64)
		if err != nil {
			continue
		}
		segments = append(segments, num)
	}

	sort.Slice(segments, func(i, j int) bool { return segments[i] < segments[j] })
	return segments, nil
}

func (w *SessionWAL) readSegment(segNum uint64) ([]wal.WALEntry, error) {
	file, err := os.Open(w.segmentPath(segNum))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if _, err := file.Seek(int64(wal.WALHeaderSize), io.SeekStart); err != nil {
		return nil, err
	}

	var entries []wal.WALEntry
	buf := make([]byte, 64*1024)

	for {
		headerBuf := make([]byte, wal.WALEntryHeaderSize)
		if _, err := io.ReadFull(file, headerBuf); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return entries, err
		}

		dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])
		totalSize := wal.WALEntryHeaderSize + int(dataLen) + wal.WALChecksumSize

		if cap(buf) < totalSize {
			buf = make([]byte, totalSize)
		}
		entryBuf := buf[:totalSize]
		copy(entryBuf, headerBuf)

		if _, err := io.ReadFull(file, entryBuf[wal.WALEntryHeaderSize:]); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return entries, err
		}

		var entry wal.WALEntry
		if err := entry.UnmarshalBinary(entryBuf); err != nil {
			return entries, nil // Corrupted entry — stop
		}

		if entry.SequenceID > w.checkpointedSeq {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func (w *SessionWAL) findLastSequence(segNum uint64) (uint64, error) {
	file, err := os.Open(w.segmentPath(segNum))
	if err != nil {
		return 0, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return 0, err
	}

	if stat.Size() <= int64(wal.WALHeaderSize) {
		return 0, nil
	}

	if _, err := file.Seek(int64(wal.WALHeaderSize), io.SeekStart); err != nil {
		return 0, err
	}

	var lastSeq uint64
	headerBuf := make([]byte, wal.WALEntryHeaderSize)

	for {
		if _, err := io.ReadFull(file, headerBuf); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return lastSeq, err
		}

		seq := binary.LittleEndian.Uint64(headerBuf[0:8])
		dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])

		skipLen := int64(dataLen) + int64(wal.WALChecksumSize)
		if _, err := file.Seek(skipLen, io.SeekCurrent); err != nil {
			break
		}

		lastSeq = seq
	}

	return lastSeq, nil
}

// --- Checkpoint persistence ---

func (w *SessionWAL) loadCheckpoint() (uint64, error) {
	data, err := os.ReadFile(w.checkpointPath())
	if err != nil {
		return 0, err
	}
	if len(data) < 8 {
		return 0, nil
	}
	return binary.LittleEndian.Uint64(data), nil
}

func (w *SessionWAL) saveCheckpoint(seq uint64) error {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, seq)

	tmpPath := w.checkpointPath() + ".tmp"
	if err := writeFileSync(tmpPath, data, 0o644); err != nil {
		return err
	}
	return durableRename(tmpPath, w.checkpointPath())
}

func (w *SessionWAL) gcSegments(segments []uint64) error {
	if w.checkpointedSeq == 0 || len(segments) == 0 {
		return nil
	}

	for _, segNum := range segments {
		maxSeq, err := w.findLastSequence(segNum)
		if err != nil {
			continue
		}
		if maxSeq <= w.checkpointedSeq {
			if err := os.Remove(w.segmentPath(segNum)); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove segment %d: %w", segNum, err)
			}
		}
	}

	return nil
}
