package ivf

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/wal"
)

const (
	walFilePrefix     = "ivf.wal."
	walCheckpointFile = "ivf.checkpoint"
)

type WAL struct {
	dir         string
	syncOnWrite bool

	mu              sync.Mutex
	sequence        atomic.Uint64
	currentSegment  *os.File
	currentSegNum   uint64
	currentEntries  int
	checkpointedSeq uint64
}

type WALConfig struct {
	Dir         string
	SyncOnWrite bool
}

func OpenWAL(config WALConfig) (*WAL, error) {
	if err := os.MkdirAll(config.Dir, 0755); err != nil {
		return nil, fmt.Errorf("wal: create dir: %w", err)
	}

	w := &WAL{
		dir:         config.Dir,
		syncOnWrite: config.SyncOnWrite,
	}

	checkpointedSeq, err := w.loadCheckpoint()
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("wal: load checkpoint: %w", err)
	}
	w.checkpointedSeq = checkpointedSeq

	segments, err := w.listSegments()
	if err != nil {
		return nil, fmt.Errorf("wal: list segments: %w", err)
	}

	if err := w.gcSegments(segments); err != nil {
		return nil, fmt.Errorf("wal: gc segments: %w", err)
	}

	segments, err = w.listSegments()
	if err != nil {
		return nil, fmt.Errorf("wal: list segments after gc: %w", err)
	}

	var lastSeq uint64
	var lastSegNum uint64

	if len(segments) > 0 {
		lastSegNum = segments[len(segments)-1]
		lastSeq, err = w.findLastSequenceInSegment(lastSegNum)
		if err != nil {
			return nil, fmt.Errorf("wal: find last sequence: %w", err)
		}
	}

	w.sequence.Store(lastSeq)

	nextSegNum := lastSegNum
	if lastSeq > 0 {
		nextSegNum = lastSegNum + 1
	} else if len(segments) == 0 {
		nextSegNum = 1
	}

	if err := w.openSegment(nextSegNum); err != nil {
		return nil, fmt.Errorf("wal: open segment: %w", err)
	}

	return w, nil
}

func (w *WAL) segmentPath(segNum uint64) string {
	return filepath.Join(w.dir, fmt.Sprintf("%s%06d", walFilePrefix, segNum))
}

func (w *WAL) checkpointPath() string {
	return filepath.Join(w.dir, walCheckpointFile)
}

func (w *WAL) listSegments() ([]uint64, error) {
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
		if !strings.HasPrefix(name, walFilePrefix) {
			continue
		}
		numStr := strings.TrimPrefix(name, walFilePrefix)
		num, err := strconv.ParseUint(numStr, 10, 64)
		if err != nil {
			continue
		}
		segments = append(segments, num)
	}

	sort.Slice(segments, func(i, j int) bool { return segments[i] < segments[j] })
	return segments, nil
}

func (w *WAL) openSegment(segNum uint64) error {
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

func (w *WAL) rotateSegment() error {
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

func (w *WAL) loadCheckpoint() (uint64, error) {
	data, err := os.ReadFile(w.checkpointPath())
	if err != nil {
		return 0, err
	}
	if len(data) < 8 {
		return 0, nil
	}
	return binary.LittleEndian.Uint64(data), nil
}

func (w *WAL) saveCheckpoint(seq uint64) error {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, seq)

	tmpPath := w.checkpointPath() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, w.checkpointPath())
}

func (w *WAL) gcSegments(segments []uint64) error {
	if w.checkpointedSeq == 0 || len(segments) == 0 {
		return nil
	}

	for _, segNum := range segments {
		maxSeq, err := w.findLastSequenceInSegment(segNum)
		if err != nil {
			continue
		}

		if maxSeq <= w.checkpointedSeq {
			path := w.segmentPath(segNum)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove segment %d: %w", segNum, err)
			}
		}
	}

	return nil
}

func (w *WAL) findLastSequenceInSegment(segNum uint64) (uint64, error) {
	path := w.segmentPath(segNum)

	file, err := os.Open(path)
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
		_, err := io.ReadFull(file, headerBuf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
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

func (w *WAL) LogInsert(vectorID uint32, vector []float32) (uint64, error) {
	data := encodeInsertData(vectorID, vector)
	return w.append(wal.OpInsert, data)
}

func (w *WAL) LogCheckpoint(lastAppliedSeq uint64) (uint64, error) {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint64(data, lastAppliedSeq)
	return w.append(wal.OpCheckpoint, data)
}

func (w *WAL) append(opType wal.OpType, data []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentEntries >= storage.VectorsPerShard {
		if err := w.rotateSegment(); err != nil {
			return 0, fmt.Errorf("wal: rotate segment: %w", err)
		}
	}

	seq := w.sequence.Add(1)

	entry := wal.WALEntry{
		SequenceID: seq,
		OpType:     opType,
		Timestamp:  currentNanoTime(),
		Data:       data,
	}

	entryBytes, err := entry.MarshalBinary()
	if err != nil {
		return 0, fmt.Errorf("wal: marshal entry: %w", err)
	}

	if _, err := w.currentSegment.Write(entryBytes); err != nil {
		return 0, fmt.Errorf("wal: write entry: %w", err)
	}

	w.currentEntries++

	if w.syncOnWrite {
		if err := w.currentSegment.Sync(); err != nil {
			return 0, fmt.Errorf("wal: sync: %w", err)
		}
	}

	return seq, nil
}

func (w *WAL) ReadAll() ([]wal.WALEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	segments, err := w.listSegments()
	if err != nil {
		return nil, err
	}

	var allEntries []wal.WALEntry

	for _, segNum := range segments {
		entries, err := w.readSegment(segNum)
		if err != nil {
			return allEntries, err
		}
		allEntries = append(allEntries, entries...)
	}

	return allEntries, nil
}

func (w *WAL) readSegment(segNum uint64) ([]wal.WALEntry, error) {
	path := w.segmentPath(segNum)

	file, err := os.Open(path)
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
		_, err := io.ReadFull(file, headerBuf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return entries, err
		}

		dataLen := binary.LittleEndian.Uint32(headerBuf[17:21])
		totalSize := wal.WALEntryHeaderSize + int(dataLen) + wal.WALChecksumSize

		if cap(buf) < totalSize {
			buf = make([]byte, totalSize)
		}
		entryBuf := buf[:totalSize]
		copy(entryBuf, headerBuf)

		if _, err := io.ReadFull(file, entryBuf[wal.WALEntryHeaderSize:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return entries, err
		}

		var entry wal.WALEntry
		if err := entry.UnmarshalBinary(entryBuf); err != nil {
			return entries, nil
		}

		if entry.SequenceID > w.checkpointedSeq {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

func (w *WAL) Checkpoint(seq uint64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.currentSegment.Sync(); err != nil {
		return fmt.Errorf("wal: sync before checkpoint: %w", err)
	}

	if err := w.saveCheckpoint(seq); err != nil {
		return fmt.Errorf("wal: save checkpoint: %w", err)
	}

	w.checkpointedSeq = seq

	segments, err := w.listSegments()
	if err != nil {
		return fmt.Errorf("wal: list segments: %w", err)
	}

	if err := w.gcSegments(segments); err != nil {
		return fmt.Errorf("wal: gc segments: %w", err)
	}

	return nil
}

func (w *WAL) Truncate() error {
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
		path := w.segmentPath(segNum)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
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

func (w *WAL) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.currentSegment != nil {
		return w.currentSegment.Sync()
	}
	return nil
}

func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentSegment != nil {
		if err := w.currentSegment.Sync(); err != nil {
			w.currentSegment.Close()
			return err
		}
		return w.currentSegment.Close()
	}
	return nil
}

func (w *WAL) LastSequence() uint64 {
	return w.sequence.Load()
}

func (w *WAL) CheckpointedSequence() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.checkpointedSeq
}

func encodeInsertData(vectorID uint32, vector []float32) []byte {
	data := make([]byte, 4+len(vector)*4)
	binary.LittleEndian.PutUint32(data[0:4], vectorID)
	for i, v := range vector {
		binary.LittleEndian.PutUint32(data[4+i*4:], uint32AsFloat32Bits(v))
	}
	return data
}

func DecodeInsertData(data []byte) (vectorID uint32, vector []float32, err error) {
	if len(data) < 4 {
		return 0, nil, fmt.Errorf("wal: insert data too short")
	}

	vectorID = binary.LittleEndian.Uint32(data[0:4])
	numFloats := (len(data) - 4) / 4
	vector = make([]float32, numFloats)
	for i := range numFloats {
		bits := binary.LittleEndian.Uint32(data[4+i*4:])
		vector[i] = float32FromUint32Bits(bits)
	}

	return vectorID, vector, nil
}

type ReplayStats struct {
	EntriesRead    int
	InsertsApplied int
	LastSequence   uint64
}

func (idx *Index) ReplayWAL() (*ReplayStats, error) {
	if idx.wal == nil {
		return &ReplayStats{}, nil
	}

	entries, err := idx.wal.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("wal: read entries: %w", err)
	}

	stats := &ReplayStats{
		EntriesRead: len(entries),
	}

	currentVectors := uint32(idx.NumVectors())

	for _, entry := range entries {
		if entry.OpType != wal.OpInsert {
			continue
		}

		vectorID, vector, err := DecodeInsertData(entry.Data)
		if err != nil {
			return stats, fmt.Errorf("wal: decode insert %d: %w", entry.SequenceID, err)
		}

		if vectorID < currentVectors {
			continue
		}

		if vectorID != currentVectors {
			return stats, fmt.Errorf("wal: gap in vector IDs: expected %d, got %d", currentVectors, vectorID)
		}

		insertMu.Lock()
		_, err = idx.insertInternal(vector, vectorID)
		insertMu.Unlock()
		if err != nil {
			return stats, fmt.Errorf("wal: insert vector %d: %w", vectorID, err)
		}

		stats.InsertsApplied++
		currentVectors++
		stats.LastSequence = entry.SequenceID
	}

	return stats, nil
}

func uint32AsFloat32Bits(f float32) uint32 {
	return math.Float32bits(f)
}

func float32FromUint32Bits(bits uint32) float32 {
	return math.Float32frombits(bits)
}

func currentNanoTime() int64 {
	return time.Now().UnixNano()
}

func (w *WAL) ReadAllAfter(afterSeq uint64) ([]wal.WALEntry, error) {
	entries, err := w.ReadAll()
	if err != nil {
		return nil, err
	}

	idx := sort.Search(len(entries), func(i int) bool {
		return entries[i].SequenceID > afterSeq
	})

	return slices.Clone(entries[idx:]), nil
}
