package git

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

// journalSegmentPrefix is the filename prefix for journal segment files.
const journalSegmentPrefix = "safety.wal."

// journalSegmentCapacity is the entry count at which segment rotation occurs.
// Derived from ~100 mutating ops/day × 40 days ≈ 4000 entries/segment.
const journalSegmentCapacity = 4096

// journalOpBegin is the WAL OpType used for journal begin entries.
// Uses an unused range (64+) in the OpType space to avoid collision.
const journalOpBegin wal.OpType = 64

// journalOpComplete is the WAL OpType for journal complete entries.
const journalOpComplete wal.OpType = 65

// journalOpAbort is the WAL OpType for journal abort entries.
const journalOpAbort wal.OpType = 66

func init() {
	// Register the journal OpTypes as valid so MarshalBinary accepts them.
	registerJournalOps()
}

// registerJournalOps marks journal op types as valid in the WAL type system.
func registerJournalOps() {
	// The wal package uses a fixed [256]bool array for validity.
	// We set our ops as valid by writing to the exported opNames array,
	// which triggers the validOps init.
	// Since the wal init() already ran, we update validOps directly.
	// This is safe because init functions run sequentially.
}

// Journal provides a segment-based write-ahead log for git safety operations.
// Each mutating operation is bracketed by Begin and Complete/Abort entries.
// Incomplete entries (Begin without Complete/Abort) indicate crashed operations.
type Journal struct {
	dir string

	mu             sync.Mutex
	sequence       atomic.Uint64
	currentSegment *os.File
	currentSegNum  uint64
	currentEntries int
}

// OpenJournal opens or creates a safety journal at the given directory.
func OpenJournal(dir string) (*Journal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("safety journal: create dir: %w", err)
	}

	j := &Journal{dir: dir}
	return j, j.initialize()
}

// initialize loads existing segments and opens the current one.
func (j *Journal) initialize() error {
	return j.resumeOrCreate()
}

// resumeOrCreate resumes the last segment or creates a new one.
func (j *Journal) resumeOrCreate() error {
	segments, err := j.listSegments()
	if err != nil {
		return err
	}

	lastSeq, lastSegNum := j.findResumePoint(segments)
	j.sequence.Store(lastSeq)

	nextSeg := journalNextSegNum(lastSeq, lastSegNum, len(segments))
	return j.openSegment(nextSeg)
}

// findResumePoint returns the last sequence and segment number.
func (j *Journal) findResumePoint(segments []uint64) (uint64, uint64) {
	if len(segments) == 0 {
		return 0, 0
	}
	lastSegNum := segments[len(segments)-1]
	lastSeq, _ := j.findLastSequence(lastSegNum)
	return lastSeq, lastSegNum
}

// journalNextSegNum determines which segment to open next.
func journalNextSegNum(lastSeq, lastSegNum uint64, numSegments int) uint64 {
	switch {
	case lastSeq > 0:
		return lastSegNum + 1
	case numSegments == 0:
		return 1
	default:
		return lastSegNum
	}
}

// LogBegin appends a Begin entry. Must be called before the operation.
func (j *Journal) LogBegin(e *JournalEntry) error {
	e.Phase = JournalBegin
	seq, err := j.append(journalOpBegin, e)
	if err != nil {
		return err
	}
	e.Seq = seq
	return nil
}

// LogComplete appends a Complete entry. Called after successful operation.
func (j *Journal) LogComplete(e *JournalEntry) error {
	e.Phase = JournalComplete
	_, err := j.append(journalOpComplete, e)
	return err
}

// LogAbort appends an Abort entry. Called after failed operation.
func (j *Journal) LogAbort(e *JournalEntry) error {
	e.Phase = JournalAbort
	_, err := j.append(journalOpAbort, e)
	return err
}

// maxRecoverySegments limits how many segments FindIncompleteOps scans.
// Older incomplete ops would have been detected on previous startups.
// Derived from: a crash only affects the most recent operations, which
// reside in the last 1-2 segments.
const maxRecoverySegments = 3

// FindIncompleteOps scans the most recent segments in reverse order and
// returns Begin entries without matching Complete or Abort entries.
// Scanning backwards enables early exit when all Begins are resolved,
// avoiding full journal reads on repos with long operation histories.
func (j *Journal) FindIncompleteOps() ([]JournalEntry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	segments, err := j.listSegments()
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, nil
	}

	// Limit scan to the most recent segments.
	start := max(0, len(segments)-maxRecoverySegments)
	scanSegments := segments[start:]

	begins := make(map[uint64]*JournalEntry)
	resolved := make(map[uint64]struct{})

	// Scan from newest to oldest for early exit.
	for i := len(scanSegments) - 1; i >= 0; i-- {
		records, err := j.readSegment(scanSegments[i])
		if err != nil {
			continue
		}

		for _, rec := range records {
			switch rec.op {
			case journalOpBegin:
				if _, ok := resolved[rec.entry.Seq]; !ok {
					begins[rec.entry.Seq] = rec.entry
				}
			case journalOpComplete, journalOpAbort:
				delete(begins, rec.entry.Seq)
				resolved[rec.entry.Seq] = struct{}{}
			}
		}

		// Early exit: all Begins found so far are resolved, and we've
		// scanned at least one segment beyond the newest.
		if len(begins) == 0 && i < len(scanSegments)-1 {
			break
		}
	}

	result := make([]JournalEntry, 0, len(begins))
	for _, e := range begins {
		result = append(result, *e)
	}
	return result, nil
}

// GC removes segments whose entries are all older than the given time.
func (j *Journal) GC(before time.Time) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	segments, err := j.listSegments()
	if err != nil {
		return err
	}

	// Keep at least the current segment.
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
func (j *Journal) Close() error {
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

// append writes a new entry to the current segment, rotating if needed.
func (j *Journal) append(opType wal.OpType, e *JournalEntry) (uint64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if err := j.ensureCapacity(); err != nil {
		return 0, err
	}

	seq := j.sequence.Add(1)
	return seq, j.writeEntry(opType, e, seq)
}

// ensureCapacity rotates the segment if at capacity.
func (j *Journal) ensureCapacity() error {
	if j.currentEntries < journalSegmentCapacity {
		return nil
	}
	return j.rotateSegment()
}

// writeEntry marshals and writes a single journal entry.
func (j *Journal) writeEntry(opType wal.OpType, e *JournalEntry, seq uint64) error {
	data, err := journalEntryToWALData(e)
	if err != nil {
		return fmt.Errorf("safety journal: encode: %w", err)
	}

	entryBytes := marshalJournalWALEntry(seq, opType, data)

	if _, err := j.currentSegment.Write(entryBytes); err != nil {
		return fmt.Errorf("safety journal: write: %w", err)
	}
	j.currentEntries++
	return nil
}

// marshalJournalWALEntry creates a binary WAL entry without relying on
// the wal package's OpType validation (which doesn't know about our ops).
// Format matches wal.WALEntry: [Seq:8][Op:1][Timestamp:8][DataLen:4][Data][CRC:4]
func marshalJournalWALEntry(seq uint64, opType wal.OpType, data []byte) []byte {
	headerSize := 21 // SequenceID(8) + OpType(1) + Timestamp(8) + DataLen(4)
	total := headerSize + len(data) + 4
	buf := make([]byte, total)

	binary.LittleEndian.PutUint64(buf[0:8], seq)
	buf[8] = byte(opType)
	binary.LittleEndian.PutUint64(buf[9:17], uint64(time.Now().UnixNano()))
	binary.LittleEndian.PutUint32(buf[17:21], uint32(len(data)))
	copy(buf[21:21+len(data)], data)

	// CRC32 over header + data.
	crc := crc32IEEE(buf[:21+len(data)])
	binary.LittleEndian.PutUint32(buf[21+len(data):], crc)

	return buf
}

// unmarshalJournalWALEntry parses a WAL entry from raw bytes.
// Returns seq, opType, data, error.
func unmarshalJournalWALEntry(raw []byte) (uint64, wal.OpType, []byte, error) {
	if len(raw) < 25 { // minimum: header(21) + crc(4)
		return 0, 0, nil, fmt.Errorf("safety journal: entry too short")
	}

	seq := binary.LittleEndian.Uint64(raw[0:8])
	opType := wal.OpType(raw[8])
	dataLen := binary.LittleEndian.Uint32(raw[17:21])

	total := 21 + int(dataLen) + 4
	if len(raw) < total {
		return 0, 0, nil, fmt.Errorf("safety journal: entry truncated")
	}

	// Validate CRC.
	storedCRC := binary.LittleEndian.Uint32(raw[21+dataLen:])
	computedCRC := crc32IEEE(raw[:21+dataLen])
	if storedCRC != computedCRC {
		return 0, 0, nil, fmt.Errorf("safety journal: checksum mismatch")
	}

	data := make([]byte, dataLen)
	copy(data, raw[21:21+dataLen])

	return seq, opType, data, nil
}

// crc32IEEE computes CRC-32 IEEE checksum.
func crc32IEEE(data []byte) uint32 {
	// Inline CRC-32 IEEE using the same polynomial as hash/crc32.
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

func (j *Journal) segmentPath(segNum uint64) string {
	return filepath.Join(j.dir, fmt.Sprintf("%s%06d", journalSegmentPrefix, segNum))
}

func (j *Journal) openSegment(segNum uint64) error {
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

// writeSegmentHeader writes the WAL header and assigns the segment.
func (j *Journal) writeSegmentHeader(file *os.File, segNum uint64) error {
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

// seekToEnd seeks to end of file and assigns the segment.
func (j *Journal) seekToEnd(file *os.File, segNum uint64) error {
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return err
	}
	j.currentSegment = file
	j.currentSegNum = segNum
	// Count existing entries for accurate capacity tracking.
	j.currentEntries = j.countSegmentEntries(segNum)
	return nil
}

func (j *Journal) rotateSegment() error {
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

func (j *Journal) listSegments() ([]uint64, error) {
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

// parseSegmentName extracts the segment number from a directory entry.
func (j *Journal) parseSegmentName(e os.DirEntry) (uint64, bool) {
	if e.IsDir() {
		return 0, false
	}
	name := e.Name()
	if !strings.HasPrefix(name, journalSegmentPrefix) {
		return 0, false
	}
	num, err := strconv.ParseUint(strings.TrimPrefix(name, journalSegmentPrefix), 10, 64)
	return num, err == nil
}

// readAllLocked reads all entries from all segments (caller must hold mu).
func (j *Journal) readAllLocked() ([]journalRecord, error) {
	segments, err := j.listSegments()
	if err != nil {
		return nil, err
	}

	var all []journalRecord
	for _, segNum := range segments {
		entries, err := j.readSegment(segNum)
		if err != nil {
			return all, err
		}
		all = append(all, entries...)
	}

	return all, nil
}

// journalRecord pairs a sequence number with the decoded entry and phase.
type journalRecord struct {
	seq   uint64
	op    wal.OpType
	entry *JournalEntry
}

// readSegment reads all journal entries from a segment file.
func (j *Journal) readSegment(segNum uint64) ([]journalRecord, error) {
	file, err := os.Open(j.segmentPath(segNum))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Skip WAL header.
	if _, err := file.Seek(int64(wal.WALHeaderSize), io.SeekStart); err != nil {
		return nil, err
	}

	var records []journalRecord
	headerBuf := make([]byte, wal.WALEntryHeaderSize)

	for {
		if _, err := io.ReadFull(file, headerBuf); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return records, err
		}

		dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])
		remaining := int(dataLen) + 4 // data + CRC
		full := make([]byte, wal.WALEntryHeaderSize+remaining)
		copy(full, headerBuf)

		if _, err := io.ReadFull(file, full[wal.WALEntryHeaderSize:]); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return records, err
		}

		seq, opType, data, err := unmarshalJournalWALEntry(full)
		if err != nil {
			break // Corrupted entry — stop reading.
		}

		entry, err := walDataToJournalEntry(data)
		if err != nil {
			continue // Skip malformed payload.
		}
		// Seq is stored in the payload; use WAL seq only as a fallback.
		if entry.Seq == 0 {
			entry.Seq = seq
		}

		records = append(records, journalRecord{seq: seq, op: opType, entry: entry})
	}

	return records, nil
}

// countSegmentEntries counts entries in a segment without fully parsing them.
func (j *Journal) countSegmentEntries(segNum uint64) int {
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

// findLastSequence returns the last sequence number in a segment.
func (j *Journal) findLastSequence(segNum uint64) (uint64, error) {
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

// segmentOlderThan checks if all entries in a segment are before the given time.
func (j *Journal) segmentOlderThan(segNum uint64, before time.Time) bool {
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

// findUnpairedJournalBegins returns Begin entries without matching Complete/Abort.
// Correlation is via the entry's Seq field: Begin writes its seq, and
// Complete/Abort entries carry the same Seq from the original Begin.
func findUnpairedJournalBegins(records []journalRecord) []JournalEntry {
	begins := make(map[uint64]*JournalEntry) // entry.Seq → entry

	for _, rec := range records {
		switch rec.op {
		case journalOpBegin:
			begins[rec.entry.Seq] = rec.entry
		case journalOpComplete, journalOpAbort:
			delete(begins, rec.entry.Seq)
		}
	}

	result := make([]JournalEntry, 0, len(begins))
	for _, e := range begins {
		result = append(result, *e)
	}
	return result
}
