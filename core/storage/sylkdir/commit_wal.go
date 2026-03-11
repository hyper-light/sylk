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

const (
	commitWALPrefix      = "commit.wal."
	commitCheckpointFile = "commit.checkpoint"
)

// CommitWAL provides a segment-based write-ahead log for global commit operations.
// Each commit is bracketed by OpCommitBegin and OpCommitEnd entries. On recovery,
// incomplete commits (begin without end) indicate partial global writes that
// need cleanup.
type CommitWAL struct {
	dir         string
	syncOnWrite bool

	mu              sync.Mutex
	sequence        atomic.Uint64
	currentSegment  *os.File
	currentSegNum   uint64
	currentEntries  int
	checkpointedSeq uint64
}

// CommitWALConfig configures a CommitWAL.
type CommitWALConfig struct {
	Dir         string
	SyncOnWrite bool
}

// IncompleteCommit describes a commit that began but did not finish.
type IncompleteCommit struct {
	SessionID     uint32
	GlobalVersion SemanticVersion
	BeginSeq      uint64
}

// commitRecord tracks a begin entry during incomplete commit scanning.
type commitRecord struct {
	seq  uint64
	data *WALCommitData
}

// OpenCommitWAL opens or creates a commit WAL at the given directory.
func OpenCommitWAL(cfg CommitWALConfig) (*CommitWAL, error) {
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, fmt.Errorf("commit wal: create dir: %w", err)
	}

	w := &CommitWAL{
		dir:         cfg.Dir,
		syncOnWrite: cfg.SyncOnWrite,
	}

	return w, w.initialize()
}

// initialize loads checkpoint, GCs stale segments, and opens the current segment.
func (w *CommitWAL) initialize() error {
	if err := w.recoverCheckpoint(); err != nil {
		return err
	}
	if err := w.gcStaleSegments(); err != nil {
		return err
	}
	return w.resumeOrCreate()
}

// recoverCheckpoint loads the persisted checkpoint sequence.
func (w *CommitWAL) recoverCheckpoint() error {
	seq, err := w.loadCheckpoint()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("commit wal: load checkpoint: %w", err)
	}
	w.checkpointedSeq = seq
	return nil
}

// gcStaleSegments removes segments fully before the checkpoint.
func (w *CommitWAL) gcStaleSegments() error {
	segments, err := w.listSegments()
	if err != nil {
		return fmt.Errorf("commit wal: list segments: %w", err)
	}
	return w.gcSegments(segments)
}

// resumeOrCreate resumes the last segment or creates a new one.
func (w *CommitWAL) resumeOrCreate() error {
	segments, err := w.listSegments()
	if err != nil {
		return err
	}

	lastSeq, lastSegNum, err := w.findResumePoint(segments)
	if err != nil {
		return err
	}

	w.sequence.Store(lastSeq)
	return w.openSegment(commitNextSegNum(lastSeq, lastSegNum, len(segments)))
}

// findResumePoint returns the last sequence and segment number.
func (w *CommitWAL) findResumePoint(segments []uint64) (uint64, uint64, error) {
	if len(segments) == 0 {
		return 0, 0, nil
	}
	lastSegNum := segments[len(segments)-1]
	lastSeq, err := w.findLastSequence(lastSegNum)
	return lastSeq, lastSegNum, err
}

// commitNextSegNum determines which segment to open next.
func commitNextSegNum(lastSeq, lastSegNum uint64, numSegments int) uint64 {
	switch {
	case lastSeq > 0:
		return lastSegNum + 1
	case numSegments == 0:
		return 1
	default:
		return lastSegNum
	}
}

// --- Log methods ---

// LogCommitBegin logs the start of a global commit.
func (w *CommitWAL) LogCommitBegin(d *WALCommitData) (uint64, error) {
	return w.append(wal.OpCommitBegin, EncodeCommitData(d))
}

// LogCommitEnd logs the successful completion of a global commit.
func (w *CommitWAL) LogCommitEnd(d *WALCommitData) (uint64, error) {
	return w.append(wal.OpCommitEnd, EncodeCommitData(d))
}

// --- Recovery ---

// FindIncompleteCommits scans all WAL entries and returns commits that
// have a Begin entry without a matching End entry.
func (w *CommitWAL) FindIncompleteCommits() ([]IncompleteCommit, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	entries, err := w.readAllLocked()
	if err != nil {
		return nil, err
	}

	return findUnpairedBegins(entries), nil
}

// findUnpairedBegins returns Begin entries that lack a matching End.
func findUnpairedBegins(entries []wal.WALEntry) []IncompleteCommit {
	begins := collectCommitBegins(entries)
	removeCommitEnds(entries, begins)
	return beginsToIncomplete(begins)
}

// collectCommitBegins extracts OpCommitBegin entries keyed by session ID.
func collectCommitBegins(entries []wal.WALEntry) map[uint32]commitRecord {
	begins := make(map[uint32]commitRecord)
	for _, e := range entries {
		addIfBegin(e, begins)
	}
	return begins
}

// addIfBegin records the entry if it is an OpCommitBegin.
func addIfBegin(e wal.WALEntry, begins map[uint32]commitRecord) {
	if e.OpType != wal.OpCommitBegin {
		return
	}
	d, err := DecodeCommitData(e.Data)
	if err != nil {
		return
	}
	begins[d.SessionID] = commitRecord{seq: e.SequenceID, data: d}
}

// removeCommitEnds removes entries from begins that have a matching End.
func removeCommitEnds(entries []wal.WALEntry, begins map[uint32]commitRecord) {
	for _, e := range entries {
		deleteIfEnd(e, begins)
	}
}

// deleteIfEnd removes the begin record if this entry is its matching end.
func deleteIfEnd(e wal.WALEntry, begins map[uint32]commitRecord) {
	if e.OpType != wal.OpCommitEnd {
		return
	}
	d, err := DecodeCommitData(e.Data)
	if err != nil {
		return
	}
	delete(begins, d.SessionID)
}

// beginsToIncomplete converts remaining begin records to IncompleteCommit slice.
func beginsToIncomplete(begins map[uint32]commitRecord) []IncompleteCommit {
	result := make([]IncompleteCommit, 0, len(begins))
	for _, rec := range begins {
		result = append(result, commitRecordToIncomplete(rec))
	}
	return result
}

// commitRecordToIncomplete converts a single begin record.
func commitRecordToIncomplete(rec commitRecord) IncompleteCommit {
	return IncompleteCommit{
		SessionID: rec.data.SessionID,
		GlobalVersion: SemanticVersion{
			Major: rec.data.Major,
			Minor: rec.data.Minor,
			Patch: rec.data.Patch,
		},
		BeginSeq: rec.seq,
	}
}

// --- Replay ---

// CommitReplayHandler holds optional callbacks for commit OpTypes.
// Nil callbacks are silently skipped during replay.
type CommitReplayHandler struct {
	OnCommitBegin func(*WALCommitData) error
	OnCommitEnd   func(*WALCommitData) error
}

// CommitReplayResult summarizes a commit WAL replay.
type CommitReplayResult struct {
	EntriesRead  int
	LastSequence uint64
	Counts       map[wal.OpType]int
}

// Replay reads all WAL entries and dispatches them through the handler.
func (w *CommitWAL) Replay(handler CommitReplayHandler) (*CommitReplayResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	dispatch := buildCommitDispatch(handler)

	entries, err := w.readAllLocked()
	if err != nil {
		return nil, err
	}

	result := &CommitReplayResult{Counts: make(map[wal.OpType]int)}
	for _, entry := range entries {
		if err := dispatchCommitEntry(entry, dispatch, result); err != nil {
			return result, err
		}
	}

	return result, nil
}

// buildCommitDispatch builds the [256] dispatch table for commit operations.
func buildCommitDispatch(h CommitReplayHandler) [256]func([]byte) error {
	var table [256]func([]byte) error
	table[wal.OpCommitBegin] = wrapDecoder(DecodeCommitData, h.OnCommitBegin)
	table[wal.OpCommitEnd] = wrapDecoder(DecodeCommitData, h.OnCommitEnd)
	return table
}

// dispatchCommitEntry dispatches a single entry through the table.
func dispatchCommitEntry(entry wal.WALEntry, dispatch [256]func([]byte) error, result *CommitReplayResult) error {
	if fn := dispatch[entry.OpType]; fn != nil {
		if err := fn(entry.Data); err != nil {
			return fmt.Errorf("commit wal: replay seq %d: %w", entry.SequenceID, err)
		}
	}
	result.EntriesRead++
	result.Counts[entry.OpType]++
	result.LastSequence = entry.SequenceID
	return nil
}

// --- Core WAL infrastructure ---

// append writes a new entry to the current segment, rotating if needed.
func (w *CommitWAL) append(opType wal.OpType, data []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.ensureCapacity(); err != nil {
		return 0, err
	}

	seq := w.sequence.Add(1)
	return seq, w.writeEntry(opType, data, seq)
}

// ensureCapacity rotates the segment if at capacity.
func (w *CommitWAL) ensureCapacity() error {
	if w.currentEntries < walSegmentCapacity {
		return nil
	}
	return w.rotateSegment()
}

// writeEntry marshals and writes a single WAL entry.
func (w *CommitWAL) writeEntry(opType wal.OpType, data []byte, seq uint64) error {
	entry := wal.WALEntry{
		SequenceID: seq,
		OpType:     opType,
		Timestamp:  time.Now().UnixNano(),
		Data:       data,
	}

	entryBytes, err := entry.MarshalBinary()
	if err != nil {
		return fmt.Errorf("commit wal: marshal: %w", err)
	}

	return w.writeAndSync(entryBytes)
}

// writeAndSync writes bytes and optionally fsyncs.
func (w *CommitWAL) writeAndSync(entryBytes []byte) error {
	if _, err := w.currentSegment.Write(entryBytes); err != nil {
		return fmt.Errorf("commit wal: write: %w", err)
	}
	w.currentEntries++
	if w.syncOnWrite {
		return w.currentSegment.Sync()
	}
	return nil
}

// Checkpoint saves the checkpoint sequence and GCs old segments.
func (w *CommitWAL) Checkpoint(seq uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.syncAndSaveCheckpoint(seq); err != nil {
		return err
	}
	w.checkpointedSeq = seq
	return w.gcStaleSegments()
}

// syncAndSaveCheckpoint fsyncs the current segment then persists the checkpoint.
func (w *CommitWAL) syncAndSaveCheckpoint(seq uint64) error {
	if err := w.currentSegment.Sync(); err != nil {
		return fmt.Errorf("commit wal: sync before checkpoint: %w", err)
	}
	return w.saveCheckpoint(seq)
}

// Truncate removes all WAL segments and resets state.
func (w *CommitWAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.closeCurrent()

	if err := w.removeAllSegments(); err != nil {
		return err
	}
	w.resetCounters()
	return w.removeCheckpointAndReopen()
}

// closeCurrent closes the current segment file.
func (w *CommitWAL) closeCurrent() {
	if w.currentSegment != nil {
		w.currentSegment.Close()
		w.currentSegment = nil
	}
}

// removeAllSegments deletes all segment files.
func (w *CommitWAL) removeAllSegments() error {
	segments, err := w.listSegments()
	if err != nil {
		return err
	}
	return w.removeSegmentFiles(segments)
}

// removeSegmentFiles removes each segment file.
func (w *CommitWAL) removeSegmentFiles(segments []uint64) error {
	for _, segNum := range segments {
		if err := removeIfExists(w.segmentPath(segNum)); err != nil {
			return err
		}
	}
	return nil
}

// removeIfExists removes a file, ignoring not-found errors.
func removeIfExists(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// resetCounters zeroes all mutable state.
func (w *CommitWAL) resetCounters() {
	w.sequence.Store(0)
	w.checkpointedSeq = 0
	w.currentEntries = 0
}

// removeCheckpointAndReopen removes the checkpoint file and opens segment 1.
func (w *CommitWAL) removeCheckpointAndReopen() error {
	if err := removeIfExists(w.checkpointPath()); err != nil {
		return err
	}
	return w.openSegment(1)
}

// Sync fsyncs the current segment.
func (w *CommitWAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentSegment != nil {
		return w.currentSegment.Sync()
	}
	return nil
}

// Close syncs and closes the WAL.
func (w *CommitWAL) Close() error {
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
func (w *CommitWAL) LastSequence() uint64 {
	return w.sequence.Load()
}

// CheckpointedSequence returns the checkpointed sequence.
func (w *CommitWAL) CheckpointedSequence() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.checkpointedSeq
}

// WALBytes returns the total size of all WAL segment files in bytes.
func (w *CommitWAL) WALBytes() int64 {
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
func (w *CommitWAL) ReadAll() ([]wal.WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.readAllLocked()
}

// readAllLocked reads all entries (caller must hold mu).
func (w *CommitWAL) readAllLocked() ([]wal.WALEntry, error) {
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

func (w *CommitWAL) segmentPath(segNum uint64) string {
	return filepath.Join(w.dir, fmt.Sprintf("%s%06d", commitWALPrefix, segNum))
}

func (w *CommitWAL) checkpointPath() string {
	return filepath.Join(w.dir, commitCheckpointFile)
}

func (w *CommitWAL) openSegment(segNum uint64) error {
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
		return w.writeSegmentHeader(file, segNum)
	}

	return w.seekToEnd(file, segNum)
}

// writeSegmentHeader writes the WAL header and assigns the segment.
func (w *CommitWAL) writeSegmentHeader(file *os.File, segNum uint64) error {
	header := wal.NewWALHeader()
	headerBytes, _ := header.MarshalBinary()
	if _, err := file.Write(headerBytes); err != nil {
		file.Close()
		return err
	}
	w.currentSegment = file
	w.currentSegNum = segNum
	w.currentEntries = 0
	return nil
}

// seekToEnd seeks to end of file and assigns the segment.
func (w *CommitWAL) seekToEnd(file *os.File, segNum uint64) error {
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return err
	}
	w.currentSegment = file
	w.currentSegNum = segNum
	w.currentEntries = 0
	return nil
}

func (w *CommitWAL) rotateSegment() error {
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

func (w *CommitWAL) listSegments() ([]uint64, error) {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return nil, err
	}

	var segments []uint64
	for _, e := range entries {
		if segNum, ok := w.parseSegmentName(e); ok {
			segments = append(segments, segNum)
		}
	}

	sort.Slice(segments, func(i, j int) bool { return segments[i] < segments[j] })
	return segments, nil
}

// parseSegmentName extracts the segment number from a directory entry.
func (w *CommitWAL) parseSegmentName(e os.DirEntry) (uint64, bool) {
	if e.IsDir() {
		return 0, false
	}
	name := e.Name()
	if !strings.HasPrefix(name, commitWALPrefix) {
		return 0, false
	}
	num, err := strconv.ParseUint(strings.TrimPrefix(name, commitWALPrefix), 10, 64)
	return num, err == nil
}

func (w *CommitWAL) readSegment(segNum uint64) ([]wal.WALEntry, error) {
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

		entry, done, err := w.parseEntry(file, headerBuf, &buf)
		if done {
			break
		}
		if err != nil {
			return entries, err
		}

		if entry.SequenceID > w.checkpointedSeq {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// parseEntry reads the data+checksum portion of an entry after its header.
func (w *CommitWAL) parseEntry(file *os.File, headerBuf []byte, buf *[]byte) (wal.WALEntry, bool, error) {
	dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])
	totalSize := wal.WALEntryHeaderSize + int(dataLen) + wal.WALChecksumSize

	if cap(*buf) < totalSize {
		*buf = make([]byte, totalSize)
	}
	entryBuf := (*buf)[:totalSize]
	copy(entryBuf, headerBuf)

	if _, err := io.ReadFull(file, entryBuf[wal.WALEntryHeaderSize:]); err == io.EOF || err == io.ErrUnexpectedEOF {
		return wal.WALEntry{}, true, nil
	} else if err != nil {
		return wal.WALEntry{}, false, err
	}

	var entry wal.WALEntry
	if err := entry.UnmarshalBinary(entryBuf); err != nil {
		return wal.WALEntry{}, true, nil // Corrupted entry — stop
	}

	return entry, false, nil
}

func (w *CommitWAL) findLastSequence(segNum uint64) (uint64, error) {
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

func (w *CommitWAL) loadCheckpoint() (uint64, error) {
	data, err := os.ReadFile(w.checkpointPath())
	if err != nil {
		return 0, err
	}
	if len(data) < 8 {
		return 0, nil
	}
	return binary.LittleEndian.Uint64(data), nil
}

func (w *CommitWAL) saveCheckpoint(seq uint64) error {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, seq)

	tmpPath := w.checkpointPath() + ".tmp"
	if err := writeFileSync(tmpPath, data, 0o644); err != nil {
		return err
	}
	return durableRename(tmpPath, w.checkpointPath())
}

func (w *CommitWAL) gcSegments(segments []uint64) error {
	if w.checkpointedSeq == 0 || len(segments) == 0 {
		return nil
	}

	for _, segNum := range segments {
		if err := w.gcOneSegment(segNum); err != nil {
			return err
		}
	}

	return nil
}

// gcOneSegment removes a segment if all its entries are checkpointed.
func (w *CommitWAL) gcOneSegment(segNum uint64) error {
	maxSeq, err := w.findLastSequence(segNum)
	if err != nil {
		return nil // Non-fatal: skip this segment
	}
	if maxSeq > w.checkpointedSeq {
		return nil
	}
	if err := os.Remove(w.segmentPath(segNum)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove segment %d: %w", segNum, err)
	}
	return nil
}
