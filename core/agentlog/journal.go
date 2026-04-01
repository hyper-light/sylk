package agentlog

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultSegmentCap   = 4096
	defaultSyncInterval = 100 * time.Millisecond
	recoverySegments    = 3
)

// JournalConfig configures an AgentJournal instance.
type JournalConfig struct {
	SessionDir   string        // .sylk/sessions/<sid>/
	AgentName    string        // "orchestrator"
	WALName      string        // "dag", "events", "routing", etc.
	SegmentCap   int           // 0 = default 4096
	SyncInterval time.Duration // 0 = default 100ms
}

// AgentJournal is a segment-based, CRC-protected write-ahead log.
// One journal per WAL name — different concerns get different instances.
type AgentJournal struct {
	dir        string
	name       string
	segmentCap int

	mu            sync.Mutex
	sequence      atomic.Uint64
	currentSeg    *os.File
	currentSegNum uint64
	currentCount  int

	checkpointSeq uint64

	syncInterval time.Duration
	syncTimer    *time.Timer
	dirty        atomic.Bool
	closed       atomic.Bool
}

// OpenJournal opens or creates a segment-based WAL journal.
func OpenJournal(cfg JournalConfig) (*AgentJournal, error) {
	dir := resolveJournalDir(cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("agentlog: create journal dir: %w", err)
	}

	segCap := cfg.SegmentCap
	if segCap <= 0 {
		segCap = defaultSegmentCap
	}
	syncInt := cfg.SyncInterval
	if syncInt <= 0 {
		syncInt = defaultSyncInterval
	}

	j := &AgentJournal{
		dir:          dir,
		name:         cfg.WALName,
		segmentCap:   segCap,
		syncInterval: syncInt,
	}

	if err := j.initialize(); err != nil {
		return nil, err
	}

	j.startSyncTimer()
	return j, nil
}

// Append writes a raw-bytes payload with the given event type.
// Returns the assigned sequence number. Fire-and-forget: write failures
// are logged via slog but never propagated to the caller.
func (j *AgentJournal) Append(eventType EventType, payload []byte) (uint64, error) {
	if j.closed.Load() {
		return 0, fmt.Errorf("agentlog: journal closed")
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if err := j.ensureCapacity(); err != nil {
		slog.Warn("agentlog: rotate failed", "err", err, "journal", j.name)
		return 0, nil
	}

	seq := j.sequence.Add(1)
	entry := &Entry{
		Seq:       seq,
		EventType: eventType,
		Timestamp: time.Now(),
		Data:      payload,
	}

	buf := MarshalEntry(entry)
	if _, err := j.currentSeg.Write(buf); err != nil {
		slog.Warn("agentlog: write failed", "err", err, "journal", j.name, "seq", seq)
		return 0, nil
	}

	j.currentCount++
	j.dirty.Store(true)
	return seq, nil
}

// AppendJSON marshals v as JSON and appends it.
func (j *AgentJournal) AppendJSON(eventType EventType, v any) (uint64, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return 0, fmt.Errorf("agentlog: marshal payload: %w", err)
	}
	return j.Append(eventType, data)
}

// Checkpoint records the last fully-processed sequence.
// GC uses this to determine which segments are safe to remove.
func (j *AgentJournal) Checkpoint(seq uint64) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.checkpointSeq = seq

	path := filepath.Join(j.dir, j.name+".wal.checkpoint")
	data := []byte(strconv.FormatUint(seq, 10))
	return os.WriteFile(path, data, 0o644)
}

// Replay reads all entries with sequence > afterSeq and calls fn for each.
// Scans the most recent recoverySegments segments.
func (j *AgentJournal) Replay(afterSeq uint64, fn func(Entry) error) error {
	return j.replay(afterSeq, recoverySegments, fn)
}

// ReplayAll reads all entries with sequence > afterSeq and calls fn for each.
// Use this for durable reducers that must reconstruct complete state.
func (j *AgentJournal) ReplayAll(afterSeq uint64, fn func(Entry) error) error {
	return j.replay(afterSeq, 0, fn)
}

func (j *AgentJournal) replay(afterSeq uint64, segmentLimit int, fn func(Entry) error) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	segments, err := j.listSegments()
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		return nil
	}

	start := 0
	if segmentLimit > 0 {
		start = max(0, len(segments)-segmentLimit)
	}
	for _, segNum := range segments[start:] {
		entries, err := j.readSegmentEntries(segNum)
		if err != nil {
			slog.Warn("agentlog: replay segment error", "seg", segNum, "err", err)
			continue
		}
		for _, e := range entries {
			if e.Seq <= afterSeq {
				continue
			}
			if err := fn(e); err != nil {
				return err
			}
		}
	}

	return nil
}

// GC removes segments whose entries are all older than the given time
// and whose maximum sequence <= checkpoint sequence.
func (j *AgentJournal) GC(before time.Time) error {
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

// Close flushes pending writes and closes the journal.
func (j *AgentJournal) Close() error {
	if !j.closed.CompareAndSwap(false, true) {
		return nil
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.syncTimer != nil {
		j.syncTimer.Stop()
	}

	if j.currentSeg == nil {
		return nil
	}

	syncErr := j.currentSeg.Sync()
	closeErr := j.currentSeg.Close()
	j.currentSeg = nil

	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// LastSequence returns the highest sequence number written.
func (j *AgentJournal) LastSequence() uint64 {
	return j.sequence.Load()
}

// --- Initialization ---

func (j *AgentJournal) initialize() error {
	segments, err := j.listSegments()
	if err != nil {
		return err
	}

	lastSeq, lastSegNum := j.findResumePoint(segments)
	j.sequence.Store(lastSeq)
	j.loadCheckpoint()

	nextSeg := nextSegNum(lastSeq, lastSegNum, len(segments))
	return j.openSegment(nextSeg)
}

func (j *AgentJournal) findResumePoint(segments []uint64) (uint64, uint64) {
	if len(segments) == 0 {
		return 0, 0
	}
	lastSegNum := segments[len(segments)-1]
	lastSeq := j.findLastSequence(lastSegNum)
	return lastSeq, lastSegNum
}

func nextSegNum(lastSeq, lastSegNum uint64, numSegments int) uint64 {
	switch {
	case lastSeq > 0:
		return lastSegNum + 1
	case numSegments == 0:
		return 1
	default:
		return lastSegNum
	}
}

func (j *AgentJournal) loadCheckpoint() {
	path := filepath.Join(j.dir, j.name+".wal.checkpoint")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	seq, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return
	}
	j.checkpointSeq = seq
}

// --- Segment management ---

func (j *AgentJournal) segmentPrefix() string {
	return j.name + ".wal."
}

func (j *AgentJournal) segmentPath(segNum uint64) string {
	return filepath.Join(j.dir, fmt.Sprintf("%s%06d", j.segmentPrefix(), segNum))
}

func (j *AgentJournal) openSegment(segNum uint64) error {
	path := j.segmentPath(segNum)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("agentlog: open segment: %w", err)
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

func (j *AgentJournal) writeSegmentHeader(file *os.File, segNum uint64) error {
	var header [WALHeaderSize]byte
	MarshalFileHeader(header[:])
	if _, err := file.Write(header[:]); err != nil {
		file.Close()
		return err
	}
	j.currentSeg = file
	j.currentSegNum = segNum
	j.currentCount = 0
	return nil
}

func (j *AgentJournal) seekToEnd(file *os.File, segNum uint64) error {
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		file.Close()
		return err
	}
	j.currentSeg = file
	j.currentSegNum = segNum
	j.currentCount = j.countSegmentEntries(segNum)
	return nil
}

func (j *AgentJournal) ensureCapacity() error {
	if j.currentCount < j.segmentCap {
		return nil
	}
	return j.rotateSegment()
}

func (j *AgentJournal) rotateSegment() error {
	if j.currentSeg != nil {
		if err := j.currentSeg.Sync(); err != nil {
			return err
		}
		if err := j.currentSeg.Close(); err != nil {
			return err
		}
	}
	return j.openSegment(j.currentSegNum + 1)
}

func (j *AgentJournal) listSegments() ([]uint64, error) {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	prefix := j.segmentPrefix()
	var segments []uint64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		if suffix == "checkpoint" {
			continue
		}
		num, err := strconv.ParseUint(suffix, 10, 64)
		if err != nil {
			continue
		}
		segments = append(segments, num)
	}

	sort.Slice(segments, func(i, k int) bool { return segments[i] < segments[k] })
	return segments, nil
}

// --- Segment reading ---

func (j *AgentJournal) readSegmentEntries(segNum uint64) ([]Entry, error) {
	file, err := os.Open(j.segmentPath(segNum))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if _, err := file.Seek(int64(WALHeaderSize), io.SeekStart); err != nil {
		return nil, err
	}

	var entries []Entry
	headerBuf := make([]byte, EntryHeaderSize)

	for {
		if _, err := io.ReadFull(file, headerBuf); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return entries, err
		}

		dataLen := int(binary.LittleEndian.Uint32(headerBuf[18:22]))
		remaining := dataLen + CRCSize
		full := make([]byte, EntryHeaderSize+remaining)
		copy(full, headerBuf)

		if _, err := io.ReadFull(file, full[EntryHeaderSize:]); err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		} else if err != nil {
			return entries, err
		}

		entry, _, err := UnmarshalEntry(full)
		if err != nil {
			break // corrupted entry — stop
		}

		entries = append(entries, *entry)
	}

	return entries, nil
}

func (j *AgentJournal) countSegmentEntries(segNum uint64) int {
	file, err := os.Open(j.segmentPath(segNum))
	if err != nil {
		return 0
	}
	defer file.Close()

	if _, err := file.Seek(int64(WALHeaderSize), io.SeekStart); err != nil {
		return 0
	}

	count := 0
	headerBuf := make([]byte, EntryHeaderSize)

	for {
		if _, err := io.ReadFull(file, headerBuf); err != nil {
			break
		}
		dataLen := binary.LittleEndian.Uint32(headerBuf[18:22])
		skipLen := int64(dataLen) + int64(CRCSize)
		if _, err := file.Seek(skipLen, io.SeekCurrent); err != nil {
			break
		}
		count++
	}

	return count
}

func (j *AgentJournal) findLastSequence(segNum uint64) uint64 {
	file, err := os.Open(j.segmentPath(segNum))
	if err != nil {
		return 0
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil || stat.Size() <= int64(WALHeaderSize) {
		return 0
	}

	if _, err := file.Seek(int64(WALHeaderSize), io.SeekStart); err != nil {
		return 0
	}

	var lastSeq uint64
	headerBuf := make([]byte, EntryHeaderSize)

	for {
		if _, err := io.ReadFull(file, headerBuf); err != nil {
			break
		}
		seq := binary.LittleEndian.Uint64(headerBuf[0:8])
		dataLen := binary.LittleEndian.Uint32(headerBuf[18:22])
		if _, err := file.Seek(int64(dataLen)+int64(CRCSize), io.SeekCurrent); err != nil {
			break
		}
		lastSeq = seq
	}

	return lastSeq
}

func (j *AgentJournal) segmentOlderThan(segNum uint64, before time.Time) bool {
	file, err := os.Open(j.segmentPath(segNum))
	if err != nil {
		return false
	}
	defer file.Close()

	if _, err := file.Seek(int64(WALHeaderSize), io.SeekStart); err != nil {
		return false
	}

	headerBuf := make([]byte, EntryHeaderSize)
	beforeNano := before.UnixNano()

	for {
		if _, err := io.ReadFull(file, headerBuf); err != nil {
			break
		}
		ts := int64(binary.LittleEndian.Uint64(headerBuf[10:18]))
		if ts >= beforeNano {
			return false
		}
		dataLen := binary.LittleEndian.Uint32(headerBuf[18:22])
		if _, err := file.Seek(int64(dataLen)+int64(CRCSize), io.SeekCurrent); err != nil {
			break
		}
	}

	return true
}

// --- Batched sync ---

func (j *AgentJournal) startSyncTimer() {
	j.syncTimer = time.AfterFunc(j.syncInterval, j.timerSync)
}

func (j *AgentJournal) timerSync() {
	if j.closed.Load() {
		return
	}

	if j.dirty.CompareAndSwap(true, false) {
		j.mu.Lock()
		if j.currentSeg != nil {
			_ = j.currentSeg.Sync()
		}
		j.mu.Unlock()
	}

	j.syncTimer.Reset(j.syncInterval)
}

// OpenJournalDirect opens a journal in an explicit directory path.
// Used when the caller manages directory layout (e.g. orchestrator backward compat).
func OpenJournalDirect(dir string, cfg JournalConfig) (*AgentJournal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("agentlog: create journal dir: %w", err)
	}

	segCap := cfg.SegmentCap
	if segCap <= 0 {
		segCap = defaultSegmentCap
	}
	syncInt := cfg.SyncInterval
	if syncInt <= 0 {
		syncInt = defaultSyncInterval
	}

	j := &AgentJournal{
		dir:          dir,
		name:         cfg.WALName,
		segmentCap:   segCap,
		syncInterval: syncInt,
	}

	if err := j.initialize(); err != nil {
		return nil, err
	}

	j.startSyncTimer()
	return j, nil
}

// --- Path resolution ---

func resolveJournalDir(cfg JournalConfig) string {
	if cfg.SessionDir != "" {
		return filepath.Join(cfg.SessionDir, "agents", normalizeAgentID(cfg.AgentName), "wal")
	}
	return filepath.Join(".sylk", "agents", normalizeAgentID(cfg.AgentName), "wal")
}
