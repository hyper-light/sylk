package sylkdir

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// indexState holds the dense entries array. Swapped atomically on grow.
type indexState struct {
	entries  []int64
	capacity uint32
}

// OffsetIndex is a dense int64 array indexed by uint32 entity IDs.
// Each entry maps an ID to a byte offset in a shared data file.
// Negative entries (-1) indicate absent IDs.
//
// Concurrency model: single-writer-multiple-reader.
// Reads (Get, ForEach, Count, Capacity, Clone) are completely lock-free
// using atomic loads on the state pointer and individual entries.
// Writes (Set, Delete, MergeFrom, Save, Load) are serialized via writeMu.
//
// Binary format: [Magic:4][Version:4][Capacity:4][Count:4][Entries:Capacity*8]
type OffsetIndex struct {
	state   atomic.Pointer[indexState]
	count   atomic.Uint32
	dirty   atomic.Bool
	path    string
	writeMu sync.Mutex
}

const (
	// offsetIndexMagic identifies an OffsetIndex file.
	offsetIndexMagic uint32 = 0x5849444F // "OIDX" little-endian

	// offsetIndexVersion is the current binary format version.
	offsetIndexVersion uint32 = 1

	// offsetIndexHeaderSize is the fixed header size in bytes.
	offsetIndexHeaderSize = 16

	// offsetIndexAbsent is the sentinel value for absent entries.
	offsetIndexAbsent int64 = -1

	// offsetIndexMinCapacity is the minimum allocation size.
	offsetIndexMinCapacity uint32 = 64
)

// ErrCorruptIndex is returned when an index file has invalid magic or version.
var ErrCorruptIndex = errors.New("sylkdir: corrupt offset index")

// NewOffsetIndex creates a new empty OffsetIndex at the given path.
func NewOffsetIndex(path string, capacity uint32) *OffsetIndex {
	cap := max(capacity, offsetIndexMinCapacity)
	entries := make([]int64, cap)
	fillAbsent(entries)
	idx := &OffsetIndex{path: path}
	idx.state.Store(&indexState{entries: entries, capacity: cap})
	return idx
}

// Get returns the offset for the given ID. Lock-free.
func (idx *OffsetIndex) Get(id uint32) (int64, bool) {
	s := idx.state.Load()
	if id >= s.capacity {
		return 0, false
	}
	offset := atomic.LoadInt64(&s.entries[id])
	return offset, offset != offsetIndexAbsent
}

// Set stores an offset for the given ID, growing the array if needed.
func (idx *OffsetIndex) Set(id uint32, offset int64) {
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	s := idx.state.Load()
	if id >= s.capacity {
		newCap := s.capacity
		for newCap <= id {
			newCap *= 2
		}
		grown := make([]int64, newCap)
		copy(grown, s.entries)
		fillAbsent(grown[s.capacity:])
		s = &indexState{entries: grown, capacity: newCap}
		idx.state.Store(s)
	}

	wasAbsent := s.entries[id] == offsetIndexAbsent
	atomic.StoreInt64(&s.entries[id], offset)
	if wasAbsent {
		idx.count.Add(1)
	}
	idx.dirty.Store(true)
}

// SetBatch stores offsets for multiple IDs. Single writeMu acquisition.
// ids and offsets must be parallel slices of equal length.
func (idx *OffsetIndex) SetBatch(ids []uint32, offsets []int64) {
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	// Find max ID for single grow.
	var maxID uint32
	for _, id := range ids {
		if id > maxID {
			maxID = id
		}
	}

	s := idx.state.Load()
	if maxID >= s.capacity {
		newCap := s.capacity
		for newCap <= maxID {
			newCap *= 2
		}
		grown := make([]int64, newCap)
		copy(grown, s.entries)
		fillAbsent(grown[s.capacity:])
		s = &indexState{entries: grown, capacity: newCap}
		idx.state.Store(s)
	}

	var added uint32
	for i, id := range ids {
		wasAbsent := s.entries[id] == offsetIndexAbsent
		atomic.StoreInt64(&s.entries[id], offsets[i])
		if wasAbsent {
			added++
		}
	}
	if added > 0 {
		idx.count.Add(added)
	}
	idx.dirty.Store(true)
}

// Delete marks the given ID as absent.
func (idx *OffsetIndex) Delete(id uint32) {
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	s := idx.state.Load()
	if id >= s.capacity {
		return
	}
	if s.entries[id] == offsetIndexAbsent {
		return
	}
	atomic.StoreInt64(&s.entries[id], offsetIndexAbsent)
	idx.count.Add(^uint32(0)) // atomic decrement
	idx.dirty.Store(true)
}

// ForEach iterates over all present entries. Lock-free.
// The callback receives the ID and offset. Return false to stop.
func (idx *OffsetIndex) ForEach(fn func(id uint32, offset int64) bool) {
	s := idx.state.Load()
	for i := range s.capacity {
		offset := atomic.LoadInt64(&s.entries[i])
		if offset == offsetIndexAbsent {
			continue
		}
		if !fn(uint32(i), offset) {
			return
		}
	}
}

// Count returns the number of present entries. Lock-free.
func (idx *OffsetIndex) Count() uint32 {
	return idx.count.Load()
}

// Capacity returns the current array capacity. Lock-free.
func (idx *OffsetIndex) Capacity() uint32 {
	return idx.state.Load().capacity
}

// Clone creates a deep copy of the index with a new file path. Lock-free read.
func (idx *OffsetIndex) Clone(newPath string) *OffsetIndex {
	s := idx.state.Load()
	entries := make([]int64, s.capacity)
	for i := range s.capacity {
		entries[i] = atomic.LoadInt64(&s.entries[i])
	}

	clone := &OffsetIndex{path: newPath}
	clone.state.Store(&indexState{entries: entries, capacity: s.capacity})
	clone.count.Store(idx.count.Load())
	clone.dirty.Store(true)
	return clone
}

// MergeFrom copies all present entries from other into this index.
// Reads other lock-free; serializes writes to this index via writeMu.
func (idx *OffsetIndex) MergeFrom(other *OffsetIndex) {
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	otherState := other.state.Load()
	s := idx.state.Load()

	for i := range otherState.capacity {
		offset := atomic.LoadInt64(&otherState.entries[i])
		if offset == offsetIndexAbsent {
			continue
		}
		id := uint32(i)
		if id >= s.capacity {
			newCap := s.capacity
			for newCap <= id {
				newCap *= 2
			}
			grown := make([]int64, newCap)
			copy(grown, s.entries)
			fillAbsent(grown[s.capacity:])
			s = &indexState{entries: grown, capacity: newCap}
			idx.state.Store(s)
		}
		wasAbsent := s.entries[id] == offsetIndexAbsent
		atomic.StoreInt64(&s.entries[id], offset)
		if wasAbsent {
			idx.count.Add(1)
		}
	}
	idx.dirty.Store(true)
}

// RemapOffsets translates all present entries using the given old→new offset map.
// Entries not in the remap map are left unchanged.
func (idx *OffsetIndex) RemapOffsets(remap map[int64]int64) {
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	s := idx.state.Load()
	for i := range s.capacity {
		old := atomic.LoadInt64(&s.entries[i])
		if old == offsetIndexAbsent {
			continue
		}
		if newOffset, ok := remap[old]; ok {
			atomic.StoreInt64(&s.entries[i], newOffset)
		}
	}
	idx.dirty.Store(true)
}

// RemapOffsetsCompact remaps offsets and clears entries not in the remap.
// Entries whose old offset has a mapping are updated; entries without a mapping
// are set to absent (removed). Used during data file compaction where only
// live records survive and stale references must be cleared.
func (idx *OffsetIndex) RemapOffsetsCompact(remap map[int64]int64) {
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	s := idx.state.Load()
	var cleared uint32
	for i := range s.capacity {
		old := atomic.LoadInt64(&s.entries[i])
		if old == offsetIndexAbsent {
			continue
		}
		if newOffset, ok := remap[old]; ok {
			atomic.StoreInt64(&s.entries[i], newOffset)
		} else {
			atomic.StoreInt64(&s.entries[i], offsetIndexAbsent)
			cleared++
		}
	}
	if cleared > 0 {
		idx.count.Add(^uint32(cleared - 1)) // atomic subtract cleared
	}
	idx.dirty.Store(true)
}

// Save writes the index to its configured path using atomic rename.
func (idx *OffsetIndex) Save() error {
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	s := idx.state.Load()
	tmpPath := idx.path + ".tmp"
	if err := writeIndexTo(tmpPath, s, idx.count.Load()); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return durableRename(tmpPath, idx.path)
}

// Load reads the index from its configured path.
func (idx *OffsetIndex) Load() error {
	idx.writeMu.Lock()
	defer idx.writeMu.Unlock()

	data, err := os.ReadFile(idx.path)
	if err != nil {
		return fmt.Errorf("read offset index: %w", err)
	}

	s, count, err := unmarshalIndexState(data)
	if err != nil {
		return err
	}

	idx.state.Store(s)
	idx.count.Store(count)
	idx.dirty.Store(false)
	return nil
}

// LoadOffsetIndex loads an OffsetIndex from disk at the given path.
func LoadOffsetIndex(path string) (*OffsetIndex, error) {
	idx := &OffsetIndex{path: path}
	if err := idx.Load(); err != nil {
		return nil, err
	}
	return idx, nil
}

// Path returns the configured file path.
func (idx *OffsetIndex) Path() string {
	return idx.path
}

// writeIndexTo serializes the index state to the given path.
func writeIndexTo(path string, s *indexState, count uint32) error {
	size := offsetIndexHeaderSize + int(s.capacity)*8
	buf := make([]byte, size)

	binary.LittleEndian.PutUint32(buf[0:4], offsetIndexMagic)
	binary.LittleEndian.PutUint32(buf[4:8], offsetIndexVersion)
	binary.LittleEndian.PutUint32(buf[8:12], s.capacity)
	binary.LittleEndian.PutUint32(buf[12:16], count)

	for i, offset := range s.entries {
		binary.LittleEndian.PutUint64(buf[offsetIndexHeaderSize+i*8:], uint64(offset))
	}

	return writeFileSync(path, buf, 0o644)
}

// unmarshalIndexState decodes binary data into an indexState and count.
func unmarshalIndexState(data []byte) (*indexState, uint32, error) {
	if len(data) < offsetIndexHeaderSize {
		return nil, 0, ErrCorruptIndex
	}

	magic := binary.LittleEndian.Uint32(data[0:4])
	version := binary.LittleEndian.Uint32(data[4:8])
	if magic != offsetIndexMagic || version != offsetIndexVersion {
		return nil, 0, ErrCorruptIndex
	}

	capacity := binary.LittleEndian.Uint32(data[8:12])
	count := binary.LittleEndian.Uint32(data[12:16])

	expectedSize := offsetIndexHeaderSize + int(capacity)*8
	if len(data) < expectedSize {
		return nil, 0, ErrCorruptIndex
	}

	entries := make([]int64, capacity)
	for i := range entries {
		entries[i] = int64(binary.LittleEndian.Uint64(data[offsetIndexHeaderSize+i*8:]))
	}

	return &indexState{entries: entries, capacity: capacity}, count, nil
}

// fillAbsent sets all entries in the slice to the absent sentinel.
func fillAbsent(entries []int64) {
	for i := range entries {
		entries[i] = offsetIndexAbsent
	}
}
