package concurrency

import (
	"bufio"
	"errors"
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
)

var (
	ErrWALClosed        = errors.New("WAL is closed")
	ErrSegmentFull      = errors.New("segment is full")
	ErrInvalidSync      = errors.New("invalid sync mode")
	ErrNoSegments       = errors.New("no segments found")
	ErrSequenceNotFound = errors.New("sequence not found")
)

type SyncMode int

const (
	SyncEveryWrite SyncMode = iota
	SyncBatched
	SyncPeriodic
)

type WALConfig struct {
	Dir            string
	MaxSegmentSize int64
	SyncMode       SyncMode
	SyncInterval   time.Duration
}

func DefaultWALConfig() WALConfig {
	return WALConfig{
		Dir:            ".sylk/wal",
		MaxSegmentSize: 64 * 1024 * 1024,
		SyncMode:       SyncBatched,
		SyncInterval:   100 * time.Millisecond,
	}
}

type WriteAheadLog struct {
	config WALConfig

	mu           sync.RWMutex
	currentFile  *os.File
	writer       *bufio.Writer
	sequence     uint64
	segmentSeq   uint64
	segmentSize  int64
	closed       atomic.Bool
	pendingSync  atomic.Bool
	lastSyncTime time.Time
	stopSync     chan struct{}
	syncDone     chan struct{}
}

func NewWriteAheadLog(config WALConfig) (*WriteAheadLog, error) {
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create WAL directory: %w", err)
	}

	wal := &WriteAheadLog{
		config:   config,
		stopSync: make(chan struct{}),
		syncDone: make(chan struct{}),
	}

	if err := wal.initialize(); err != nil {
		return nil, err
	}

	wal.startPeriodicSyncIfNeeded()

	return wal, nil
}

func (w *WriteAheadLog) initialize() error {
	if err := w.loadSequence(); err != nil {
		return err
	}
	return w.openCurrentSegment()
}

func (w *WriteAheadLog) startPeriodicSyncIfNeeded() {
	if w.config.SyncMode == SyncPeriodic {
		go w.periodicSync()
	}
}

func (w *WriteAheadLog) loadSequence() error {
	segments, err := w.listSegments()
	if err != nil {
		return err
	}

	if len(segments) == 0 {
		return w.initializeEmptySequence()
	}

	return w.loadSequenceFromSegments(segments)
}

func (w *WriteAheadLog) initializeEmptySequence() error {
	w.sequence = 0
	w.segmentSeq = 1
	return nil
}

func (w *WriteAheadLog) loadSequenceFromSegments(segments []uint64) error {
	lastSegment := segments[len(segments)-1]
	w.segmentSeq = lastSegment + 1

	entries, err := w.readSegment(lastSegment)
	if err != nil {
		return err
	}

	if len(entries) > 0 {
		w.sequence = entries[len(entries)-1].Sequence
	}

	return nil
}

func (w *WriteAheadLog) listSegments() ([]uint64, error) {
	entries, err := os.ReadDir(w.config.Dir)
	if err != nil {
		return w.handleListError(err)
	}

	segments := w.extractSegmentSequences(entries)
	sort.Slice(segments, func(i, j int) bool {
		return segments[i] < segments[j]
	})

	return segments, nil
}

func (w *WriteAheadLog) handleListError(err error) ([]uint64, error) {
	if os.IsNotExist(err) {
		return nil, nil
	}
	return nil, err
}

func (w *WriteAheadLog) extractSegmentSequences(entries []os.DirEntry) []uint64 {
	var segments []uint64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if seq, ok := parseSegmentName(entry.Name()); ok {
			segments = append(segments, seq)
		}
	}
	return segments
}

func parseSegmentName(name string) (uint64, bool) {
	if !isValidSegmentName(name) {
		return 0, false
	}
	seqStr := name[4 : len(name)-4]
	seq, err := strconv.ParseUint(seqStr, 10, 64)
	return seq, err == nil
}

func isValidSegmentName(name string) bool {
	return strings.HasPrefix(name, "wal-") && strings.HasSuffix(name, ".log")
}

func (w *WriteAheadLog) segmentPath(seq uint64) string {
	return filepath.Join(w.config.Dir, fmt.Sprintf("wal-%d.log", seq))
}

func (w *WriteAheadLog) openCurrentSegment() error {
	path := w.segmentPath(w.segmentSeq)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open segment: %w", err)
	}

	return w.initializeSegment(file)
}

func (w *WriteAheadLog) initializeSegment(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to stat segment: %w", err)
	}

	w.currentFile = file
	w.writer = bufio.NewWriterSize(file, 64*1024)
	w.segmentSize = info.Size()

	return nil
}

func (w *WriteAheadLog) Append(entry *WALEntry) (uint64, error) {
	if w.closed.Load() {
		return 0, ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.prepareEntry(entry)
	data := entry.Encode()

	if err := w.rotateIfNeeded(len(data)); err != nil {
		return 0, err
	}

	if err := w.writeEntry(data); err != nil {
		return 0, err
	}

	return w.sequence, w.syncIfNeeded()
}

func (w *WriteAheadLog) prepareEntry(entry *WALEntry) {
	w.sequence++
	entry.Sequence = w.sequence
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
}

func (w *WriteAheadLog) rotateIfNeeded(dataLen int) error {
	if w.segmentSize+int64(dataLen) <= w.config.MaxSegmentSize {
		return nil
	}
	return w.rotateSegment()
}

func (w *WriteAheadLog) writeEntry(data []byte) error {
	if _, err := w.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write entry: %w", err)
	}
	w.segmentSize += int64(len(data))
	w.pendingSync.Store(true)
	return nil
}

func (w *WriteAheadLog) syncIfNeeded() error {
	switch w.config.SyncMode {
	case SyncEveryWrite:
		return w.syncLocked()
	case SyncBatched:
		return w.syncIfIntervalElapsed()
	}
	return nil
}

func (w *WriteAheadLog) syncIfIntervalElapsed() error {
	if time.Since(w.lastSyncTime) >= w.config.SyncInterval {
		return w.syncLocked()
	}
	return nil
}

func (w *WriteAheadLog) rotateSegment() error {
	if err := w.syncLocked(); err != nil {
		return err
	}

	if err := w.currentFile.Close(); err != nil {
		return err
	}

	w.segmentSeq++
	w.segmentSize = 0

	return w.openCurrentSegment()
}

func (w *WriteAheadLog) Sync() error {
	if w.closed.Load() {
		return ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	return w.syncLocked()
}

func (w *WriteAheadLog) syncLocked() error {
	if !w.pendingSync.Load() {
		return nil
	}

	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush writer: %w", err)
	}

	if err := w.currentFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	w.pendingSync.Store(false)
	w.lastSyncTime = time.Now()

	return nil
}

func (w *WriteAheadLog) periodicSync() {
	ticker := time.NewTicker(w.config.SyncInterval)
	defer ticker.Stop()
	defer close(w.syncDone)

	for {
		select {
		case <-w.stopSync:
			return
		case <-ticker.C:
			w.trySyncPending()
		}
	}
}

func (w *WriteAheadLog) trySyncPending() {
	if !w.pendingSync.Load() {
		return
	}
	w.mu.Lock()
	_ = w.syncLocked()
	w.mu.Unlock()
}

func (w *WriteAheadLog) ReadFrom(sequence uint64) ([]*WALEntry, error) {
	if w.closed.Load() {
		return nil, ErrWALClosed
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	segments, err := w.listSegments()
	if err != nil {
		return nil, err
	}

	return w.collectEntriesFrom(segments, sequence)
}

func (w *WriteAheadLog) collectEntriesFrom(segments []uint64, minSeq uint64) ([]*WALEntry, error) {
	var result []*WALEntry

	for _, seg := range segments {
		entries := w.readSegmentEntriesFrom(seg, minSeq)
		result = append(result, entries...)
	}

	return result, nil
}

func (w *WriteAheadLog) readSegmentEntriesFrom(seg, minSeq uint64) []*WALEntry {
	entries, err := w.readSegment(seg)
	if err != nil {
		return nil
	}

	var result []*WALEntry
	for _, entry := range entries {
		if entry.Sequence >= minSeq {
			result = append(result, entry)
		}
	}
	return result
}

func (w *WriteAheadLog) ReadAll() ([]*WALEntry, error) {
	return w.ReadFrom(0)
}

func (w *WriteAheadLog) readSegment(seq uint64) ([]*WALEntry, error) {
	path := w.segmentPath(seq)

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return w.readEntriesFromReader(file)
}

func (w *WriteAheadLog) readEntriesFromReader(r io.Reader) ([]*WALEntry, error) {
	var entries []*WALEntry
	reader := bufio.NewReader(r)

	for {
		entry, done := w.readNextEntry(reader)
		if done {
			break
		}
		if entry != nil {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func (w *WriteAheadLog) readNextEntry(reader *bufio.Reader) (*WALEntry, bool) {
	data, ok := w.readEntryData(reader)
	if !ok {
		return nil, true
	}

	entry, err := DecodeWALEntry(data)
	if err != nil {
		return nil, false
	}

	return entry, false
}

func (w *WriteAheadLog) readEntryData(reader *bufio.Reader) ([]byte, bool) {
	header := make([]byte, entryHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, false
	}

	totalSize, err := ReadEntrySize(header)
	if err != nil {
		return nil, false
	}

	data := make([]byte, totalSize)
	copy(data, header)

	if _, err := io.ReadFull(reader, data[entryHeaderSize:]); err != nil {
		return nil, false
	}

	return data, true
}

type WALIterator struct {
	wal      *WriteAheadLog
	segments []uint64
	segIdx   int
	entries  []*WALEntry
	entryIdx int
}

func (w *WriteAheadLog) Iterator() (*WALIterator, error) {
	if w.closed.Load() {
		return nil, ErrWALClosed
	}

	w.mu.RLock()
	defer w.mu.RUnlock()

	segments, err := w.listSegments()
	if err != nil {
		return nil, err
	}

	return &WALIterator{
		wal:      w,
		segments: segments,
		segIdx:   0,
		entryIdx: 0,
	}, nil
}

func (it *WALIterator) Next() (*WALEntry, bool) {
	for {
		if entry, ok := it.tryCurrentEntry(); ok {
			return entry, true
		}

		if !it.loadNextSegment() {
			return nil, false
		}
	}
}

func (it *WALIterator) tryCurrentEntry() (*WALEntry, bool) {
	if len(it.entries) == 0 || it.entryIdx >= len(it.entries) {
		return nil, false
	}
	entry := it.entries[it.entryIdx]
	it.entryIdx++
	return entry, true
}

func (it *WALIterator) loadNextSegment() bool {
	for it.segIdx < len(it.segments) {
		entries, err := it.wal.readSegment(it.segments[it.segIdx])
		it.segIdx++
		if err != nil {
			continue
		}
		it.entries = entries
		it.entryIdx = 0
		return true
	}
	return false
}

func (w *WriteAheadLog) Truncate(beforeSequence uint64) error {
	if w.closed.Load() {
		return ErrWALClosed
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	segments, err := w.listSegments()
	if err != nil {
		return err
	}

	return w.truncateOldSegments(segments, beforeSequence)
}

func (w *WriteAheadLog) truncateOldSegments(segments []uint64, beforeSeq uint64) error {
	for _, seg := range segments {
		if err := w.maybeRemoveSegment(seg, beforeSeq); err != nil {
			return err
		}
	}
	return nil
}

func (w *WriteAheadLog) maybeRemoveSegment(seg, beforeSeq uint64) error {
	if seg >= w.segmentSeq {
		return nil
	}

	lastSeq := w.getSegmentLastSequence(seg)
	if lastSeq == 0 || lastSeq >= beforeSeq {
		return nil
	}

	return os.Remove(w.segmentPath(seg))
}

func (w *WriteAheadLog) getSegmentLastSequence(seg uint64) uint64 {
	entries, err := w.readSegment(seg)
	if err != nil || len(entries) == 0 {
		return 0
	}
	return entries[len(entries)-1].Sequence
}

func (w *WriteAheadLog) CurrentSequence() uint64 {
	return atomic.LoadUint64(&w.sequence)
}

func (w *WriteAheadLog) Close() error {
	if w.closed.Swap(true) {
		return ErrWALClosed
	}

	w.stopPeriodicSync()

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.syncLocked(); err != nil {
		return err
	}

	return w.currentFile.Close()
}

func (w *WriteAheadLog) stopPeriodicSync() {
	if w.config.SyncMode == SyncPeriodic {
		close(w.stopSync)
		<-w.syncDone
	}
}
