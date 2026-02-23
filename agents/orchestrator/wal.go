package orchestrator

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

// WALOp identifies the type of orchestrator WAL operation.
type WALOp uint16

const (
	walOpDAGStart    WALOp = 80
	walOpDAGComplete WALOp = 81
	walOpDAGAbort    WALOp = 82
	walOpNodeDispatch WALOp = 83
	walOpNodeResult  WALOp = 84
	walOpDAGModify   WALOp = 85
	walOpDAGCancel   WALOp = 86
)

const (
	walSegmentPrefix    = "orch.wal."
	walSegmentCapacity  = 4096 // entries per segment (matches safety_journal)
	walRecoverySegments = 3   // scan depth for crash recovery
)

// WALEntry is a single orchestrator WAL record.
type WALEntry struct {
	Seq       uint64
	Op        WALOp
	Timestamp time.Time
	DAGID     string
	NodeID    string // empty for DAG-level ops
	Layer     int
	State     string // current state at time of write
	Payload   string // JSON payload (operation-specific)
	Error     string
}

// OrchestratorJournal provides a segment-based write-ahead log for DAG operations.
// Pattern mirrors core/search/git/safety_journal.go exactly.
type OrchestratorJournal struct {
	dir string

	mu             sync.Mutex
	sequence       atomic.Uint64
	currentSegment *os.File
	currentSegNum  uint64
	currentEntries int
}

// OpenOrchestratorJournal opens or creates an orchestrator WAL journal.
func OpenOrchestratorJournal(dir string) (*OrchestratorJournal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("orchestrator journal: create dir: %w", err)
	}

	j := &OrchestratorJournal{dir: dir}
	return j, j.initialize()
}

func (j *OrchestratorJournal) initialize() error {
	return j.resumeOrCreate()
}

func (j *OrchestratorJournal) resumeOrCreate() error {
	segments, err := j.listSegments()
	if err != nil {
		return err
	}

	lastSeq, lastSegNum := j.findResumePoint(segments)
	j.sequence.Store(lastSeq)

	nextSeg := walNextSegNum(lastSeq, lastSegNum, len(segments))
	return j.openSegment(nextSeg)
}

func (j *OrchestratorJournal) findResumePoint(segments []uint64) (uint64, uint64) {
	if len(segments) == 0 {
		return 0, 0
	}
	lastSegNum := segments[len(segments)-1]
	lastSeq, _ := j.findLastSequence(lastSegNum)
	return lastSeq, lastSegNum
}

func walNextSegNum(lastSeq, lastSegNum uint64, numSegments int) uint64 {
	switch {
	case lastSeq > 0:
		return lastSegNum + 1
	case numSegments == 0:
		return 1
	default:
		return lastSegNum
	}
}

// --- Public Log Methods ---

// LogDAGStart records the start of a DAG execution.
func (j *OrchestratorJournal) LogDAGStart(dagID, dagJSON string) error {
	return j.appendEntry(walOpDAGStart, &WALEntry{
		DAGID:     dagID,
		State:     "running",
		Payload:   dagJSON,
		Timestamp: time.Now(),
	})
}

// LogDAGComplete records successful or failed DAG completion.
func (j *OrchestratorJournal) LogDAGComplete(dagID, state string) error {
	return j.appendEntry(walOpDAGComplete, &WALEntry{
		DAGID:     dagID,
		State:     state,
		Timestamp: time.Now(),
	})
}

// LogDAGAbort records a DAG abort (crash recovery).
func (j *OrchestratorJournal) LogDAGAbort(dagID, errMsg string) error {
	return j.appendEntry(walOpDAGAbort, &WALEntry{
		DAGID:     dagID,
		State:     "aborted",
		Error:     errMsg,
		Timestamp: time.Now(),
	})
}

// LogNodeDispatch records a node being dispatched to a pipeline agent.
func (j *OrchestratorJournal) LogNodeDispatch(dagID, nodeID, agentID string) error {
	return j.appendEntry(walOpNodeDispatch, &WALEntry{
		DAGID:     dagID,
		NodeID:    nodeID,
		State:     "dispatched",
		Payload:   agentID,
		Timestamp: time.Now(),
	})
}

// LogNodeResult records a node execution result.
func (j *OrchestratorJournal) LogNodeResult(dagID, nodeID, state, resultJSON string) error {
	return j.appendEntry(walOpNodeResult, &WALEntry{
		DAGID:     dagID,
		NodeID:    nodeID,
		State:     state,
		Payload:   resultJSON,
		Timestamp: time.Now(),
	})
}

// LogDAGModify records a mid-flight DAG modification.
func (j *OrchestratorJournal) LogDAGModify(dagID string, revision int, diffJSON string) error {
	return j.appendEntry(walOpDAGModify, &WALEntry{
		DAGID:     dagID,
		Layer:     revision,
		State:     "modified",
		Payload:   diffJSON,
		Timestamp: time.Now(),
	})
}

// LogDAGCancel records a DAG cancellation.
func (j *OrchestratorJournal) LogDAGCancel(dagID, reason string) error {
	return j.appendEntry(walOpDAGCancel, &WALEntry{
		DAGID:     dagID,
		State:     "cancelled",
		Payload:   reason,
		Timestamp: time.Now(),
	})
}

// FindIncompleteDAGs scans the most recent segments for unpaired DAGStart
// entries (no matching Complete/Abort).
func (j *OrchestratorJournal) FindIncompleteDAGs() ([]WALEntry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	segments, err := j.listSegments()
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, nil
	}

	start := max(0, len(segments)-walRecoverySegments)
	scanSegments := segments[start:]

	begins := make(map[string]*WALEntry)   // dagID → entry
	resolved := make(map[string]struct{})

	for i := len(scanSegments) - 1; i >= 0; i-- {
		records, err := j.readSegment(scanSegments[i])
		if err != nil {
			continue
		}

		for _, rec := range records {
			switch rec.op {
			case walOpDAGStart:
				if _, ok := resolved[rec.entry.DAGID]; !ok {
					begins[rec.entry.DAGID] = rec.entry
				}
			case walOpDAGComplete, walOpDAGAbort, walOpDAGCancel:
				delete(begins, rec.entry.DAGID)
				resolved[rec.entry.DAGID] = struct{}{}
			}
		}

		if len(begins) == 0 && i < len(scanSegments)-1 {
			break
		}
	}

	result := make([]WALEntry, 0, len(begins))
	for _, e := range begins {
		result = append(result, *e)
	}
	return result, nil
}

// GC removes segments whose entries are all older than the given time.
func (j *OrchestratorJournal) GC(before time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	segments, err := j.listSegments()
	if err != nil {
		return err
	}

	for _, segNum := range segments {
		if segNum >= j.currentSegNum {
			continue
		}
		if j.segmentOlderThan(segNum, before) {
			_ = os.Remove(j.segmentPath(segNum))
		}
	}

	return nil
}

// Close syncs and closes the journal.
func (j *OrchestratorJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.currentSegment == nil {
		return nil
	}

	if err := j.currentSegment.Sync(); err != nil {
		j.currentSegment.Close()
		return err
	}
	return j.currentSegment.Close()
}

// --- Core WAL infrastructure ---

func (j *OrchestratorJournal) appendEntry(opType WALOp, e *WALEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if err := j.ensureCapacity(); err != nil {
		return err
	}

	seq := j.sequence.Add(1)
	e.Seq = seq
	e.Op = opType
	return j.writeEntry(opType, e)
}

func (j *OrchestratorJournal) ensureCapacity() error {
	if j.currentEntries < walSegmentCapacity {
		return nil
	}
	return j.rotateSegment()
}

func (j *OrchestratorJournal) writeEntry(opType WALOp, e *WALEntry) error {
	data, err := EncodeWALEntry(e)
	if err != nil {
		return fmt.Errorf("orchestrator journal: encode: %w", err)
	}

	entryBytes := marshalOrchestratorWALEntry(e.Seq, opType, data)

	if _, err := j.currentSegment.Write(entryBytes); err != nil {
		return fmt.Errorf("orchestrator journal: write: %w", err)
	}
	j.currentEntries++
	return nil
}

// marshalOrchestratorWALEntry creates a binary WAL entry.
// Format: [Seq:8][Op:1][Timestamp:8][DataLen:4][Data][CRC:4]
func marshalOrchestratorWALEntry(seq uint64, opType WALOp, data []byte) []byte {
	headerSize := 21 // Seq(8) + Op(1) + Timestamp(8) + DataLen(4)
	total := headerSize + len(data) + 4
	buf := make([]byte, total)

	binary.LittleEndian.PutUint64(buf[0:8], seq)
	buf[8] = byte(opType)
	binary.LittleEndian.PutUint64(buf[9:17], uint64(time.Now().UnixNano()))
	binary.LittleEndian.PutUint32(buf[17:21], uint32(len(data)))
	copy(buf[21:21+len(data)], data)

	crc := walCRC32IEEE(buf[:21+len(data)])
	binary.LittleEndian.PutUint32(buf[21+len(data):], crc)

	return buf
}

// unmarshalOrchestratorWALEntry parses a WAL entry from raw bytes.
func unmarshalOrchestratorWALEntry(raw []byte) (uint64, WALOp, []byte, error) {
	if len(raw) < 25 { // minimum: header(21) + crc(4)
		return 0, 0, nil, fmt.Errorf("orchestrator journal: entry too short")
	}

	seq := binary.LittleEndian.Uint64(raw[0:8])
	opType := WALOp(raw[8])
	dataLen := binary.LittleEndian.Uint32(raw[17:21])

	total := 21 + int(dataLen) + 4
	if len(raw) < total {
		return 0, 0, nil, fmt.Errorf("orchestrator journal: entry truncated")
	}

	storedCRC := binary.LittleEndian.Uint32(raw[21+dataLen:])
	computedCRC := walCRC32IEEE(raw[:21+dataLen])
	if storedCRC != computedCRC {
		return 0, 0, nil, fmt.Errorf("orchestrator journal: checksum mismatch")
	}

	data := make([]byte, dataLen)
	copy(data, raw[21:21+dataLen])

	return seq, opType, data, nil
}

// walCRC32IEEE computes CRC-32 IEEE checksum (inline, same as safety_journal).
func walCRC32IEEE(data []byte) uint32 {
	var crc uint32 = 0xFFFFFFFF
	for _, b := range data {
		crc ^= uint32(b)
		for range 8 {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// --- Segment management ---

func (j *OrchestratorJournal) segmentPath(segNum uint64) string {
	return filepath.Join(j.dir, fmt.Sprintf("%s%06d", walSegmentPrefix, segNum))
}

func (j *OrchestratorJournal) openSegment(segNum uint64) error {
	path := j.segmentPath(segNum)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}

	if stat.Size() == 0 {
		return j.writeSegmentHeader(file, segNum)
	}

	return j.seekToEnd(file, segNum)
}

func (j *OrchestratorJournal) writeSegmentHeader(file *os.File, segNum uint64) error {
	header := wal.NewWALHeader()
	headerBytes, _ := header.MarshalBinary()
	if _, err := file.Write(headerBytes); err != nil {
		file.Close()
		return err
	}
	j.currentSegment = file
	j.currentSegNum = segNum
	j.currentEntries = 0
	return nil
}

func (j *OrchestratorJournal) seekToEnd(file *os.File, segNum uint64) error {
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return err
	}
	j.currentSegment = file
	j.currentSegNum = segNum
	j.currentEntries = j.countSegmentEntries(segNum)
	return nil
}

func (j *OrchestratorJournal) rotateSegment() error {
	if j.currentSegment != nil {
		if err := j.currentSegment.Sync(); err != nil {
			return err
		}
		if err := j.currentSegment.Close(); err != nil {
			return err
		}
	}
	return j.openSegment(j.currentSegNum + 1)
}

func (j *OrchestratorJournal) listSegments() ([]uint64, error) {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return nil, err
	}

	var segments []uint64
	for _, e := range entries {
		if segNum, ok := j.parseSegmentName(e); ok {
			segments = append(segments, segNum)
		}
	}

	sort.Slice(segments, func(i, k int) bool { return segments[i] < segments[k] })
	return segments, nil
}

func (j *OrchestratorJournal) parseSegmentName(e os.DirEntry) (uint64, bool) {
	if e.IsDir() {
		return 0, false
	}
	name := e.Name()
	if !strings.HasPrefix(name, walSegmentPrefix) {
		return 0, false
	}
	num, err := strconv.ParseUint(strings.TrimPrefix(name, walSegmentPrefix), 10, 64)
	return num, err == nil
}

type walRecord struct {
	seq   uint64
	op    WALOp
	entry *WALEntry
}

func (j *OrchestratorJournal) readSegment(segNum uint64) ([]walRecord, error) {
	file, err := os.Open(j.segmentPath(segNum))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if _, err := file.Seek(int64(wal.WALHeaderSize), io.SeekStart); err != nil {
		return nil, err
	}

	var records []walRecord
	headerBuf := make([]byte, wal.WALEntryHeaderSize)

	for {
		if _, err := io.ReadFull(file, headerBuf); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return records, err
		}

		dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])
		remaining := int(dataLen) + 4
		full := make([]byte, wal.WALEntryHeaderSize+remaining)
		copy(full, headerBuf)

		if _, err := io.ReadFull(file, full[wal.WALEntryHeaderSize:]); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return records, err
		}

		seq, opType, data, err := unmarshalOrchestratorWALEntry(full)
		if err != nil {
			break // Corrupted entry — stop reading.
		}

		entry, err := DecodeWALEntry(data)
		if err != nil {
			continue // Skip malformed payload.
		}
		if entry.Seq == 0 {
			entry.Seq = seq
		}

		records = append(records, walRecord{seq: seq, op: opType, entry: entry})
	}

	return records, nil
}

func (j *OrchestratorJournal) countSegmentEntries(segNum uint64) int {
	file, err := os.Open(j.segmentPath(segNum))
	if err != nil {
		return 0
	}
	defer file.Close()

	if _, err := file.Seek(int64(wal.WALHeaderSize), io.SeekStart); err != nil {
		return 0
	}

	count := 0
	headerBuf := make([]byte, wal.WALEntryHeaderSize)

	for {
		if _, err := io.ReadFull(file, headerBuf); err != nil {
			break
		}
		dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])
		skipLen := int64(dataLen) + 4
		if _, err := file.Seek(skipLen, io.SeekCurrent); err != nil {
			break
		}
		count++
	}

	return count
}

func (j *OrchestratorJournal) findLastSequence(segNum uint64) (uint64, error) {
	file, err := os.Open(j.segmentPath(segNum))
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
		if _, err := io.ReadFull(file, headerBuf); err != nil {
			break
		}

		seq := binary.LittleEndian.Uint64(headerBuf[0:8])
		dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])

		skipLen := int64(dataLen) + 4
		if _, err := file.Seek(skipLen, io.SeekCurrent); err != nil {
			break
		}

		lastSeq = seq
	}

	return lastSeq, nil
}

func (j *OrchestratorJournal) segmentOlderThan(segNum uint64, before time.Time) bool {
	file, err := os.Open(j.segmentPath(segNum))
	if err != nil {
		return false
	}
	defer file.Close()

	if _, err := file.Seek(int64(wal.WALHeaderSize), io.SeekStart); err != nil {
		return false
	}

	headerBuf := make([]byte, wal.WALEntryHeaderSize)
	beforeNano := before.UnixNano()

	for {
		if _, err := io.ReadFull(file, headerBuf); err != nil {
			break
		}

		ts := int64(binary.LittleEndian.Uint64(headerBuf[9:17]))
		if ts >= beforeNano {
			return false
		}

		dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])
		if _, err := file.Seek(int64(dataLen)+4, io.SeekCurrent); err != nil {
			break
		}
	}

	return true
}
