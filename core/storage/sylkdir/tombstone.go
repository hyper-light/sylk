// Package sylkdir provides tombstone bitmap tracking for superseded knowledge graph nodes.
package sylkdir

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// tombstoneMagic identifies a tombstone bitmap file ("TOMB" in ASCII).
	tombstoneMagic uint32 = 0x544F4D42

	// tombstoneFormatVersion is the current binary format version.
	tombstoneFormatVersion uint32 = 1

	// tombstoneHeaderSize is the fixed header size in bytes.
	// [Magic:4][FormatVersion:4][Capacity:4][Count:4] = 16 bytes.
	tombstoneHeaderSize = 16

	// TombstoneFile is the filename for the tombstone bitmap.
	TombstoneFile = "tombstones.bin"
)

// TombstoneBitmap tracks dead (superseded) node IDs using a bitset.
// Bit i set to 1 means node i is dead. All operations are O(1).
// Thread-safe via sync.RWMutex.
type TombstoneBitmap struct {
	words    []uint64
	count    uint32
	capacity uint32
	path     string
	mu       sync.RWMutex
}

// NewTombstoneBitmap creates an empty tombstone bitmap that can track
// node IDs up to maxNodeID. Capacity is rounded up to the next 64-bit
// word boundary.
func NewTombstoneBitmap(path string, maxNodeID uint32) *TombstoneBitmap {
	cap := wordAlignedCapacity(maxNodeID)
	return &TombstoneBitmap{
		words:    make([]uint64, cap/64),
		count:    0,
		capacity: cap,
		path:     path,
	}
}

// MarkDead marks a node ID as dead (superseded). Idempotent.
func (tb *TombstoneBitmap) MarkDead(nodeID uint32) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.ensureCapacity(nodeID)
	word := nodeID >> 6
	bit := nodeID & 63
	if tb.words[word]&(1<<bit) == 0 {
		tb.words[word] |= 1 << bit
		tb.count++
	}
}

// IsDead returns true if the node ID has been marked as dead.
func (tb *TombstoneBitmap) IsDead(nodeID uint32) bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	if nodeID >= tb.capacity {
		return false
	}
	return tb.words[nodeID>>6]&(1<<(nodeID&63)) != 0
}

// IsEdgeAlive returns true if both source and target nodes are alive.
// An edge is dead if either endpoint is dead.
func (tb *TombstoneBitmap) IsEdgeAlive(sourceID, targetID uint32) bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	return !tb.isDeadUnlocked(sourceID) && !tb.isDeadUnlocked(targetID)
}

// Count returns the number of dead nodes.
func (tb *TombstoneBitmap) Count() uint32 {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.count
}

// Capacity returns the maximum trackable node ID.
func (tb *TombstoneBitmap) Capacity() uint32 {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return tb.capacity
}

// DeadRatio returns the ratio of dead nodes to total nodes.
// Returns 0 if totalNodes is 0.
func (tb *TombstoneBitmap) DeadRatio(totalNodes uint32) float64 {
	if totalNodes == 0 {
		return 0
	}
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return float64(tb.count) / float64(totalNodes)
}

// Save persists the bitmap to disk using write-to-temp + fsync + rename
// for atomic updates.
func (tb *TombstoneBitmap) Save() error {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(tb.path), 0755); err != nil {
		return fmt.Errorf("tombstone: create dir: %w", err)
	}

	tmpPath := tb.path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("tombstone: create temp: %w", err)
	}

	// Write header
	header := make([]byte, tombstoneHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], tombstoneMagic)
	binary.LittleEndian.PutUint32(header[4:8], tombstoneFormatVersion)
	binary.LittleEndian.PutUint32(header[8:12], tb.capacity)
	binary.LittleEndian.PutUint32(header[12:16], tb.count)
	if _, err := f.Write(header); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("tombstone: write header: %w", err)
	}

	// Write words
	wordBuf := make([]byte, 8)
	for _, w := range tb.words {
		binary.LittleEndian.PutUint64(wordBuf, w)
		if _, err := f.Write(wordBuf); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("tombstone: write words: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("tombstone: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("tombstone: close: %w", err)
	}

	return durableRename(tmpPath, tb.path)
}

// Load reads the bitmap from disk. If the file does not exist,
// the bitmap remains empty (no error).
func (tb *TombstoneBitmap) Load() error {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	data, err := os.ReadFile(tb.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("tombstone: read: %w", err)
	}

	if len(data) < tombstoneHeaderSize {
		return fmt.Errorf("tombstone: file too small: %d bytes", len(data))
	}

	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != tombstoneMagic {
		return fmt.Errorf("tombstone: invalid magic: 0x%08X", magic)
	}

	version := binary.LittleEndian.Uint32(data[4:8])
	if version != tombstoneFormatVersion {
		return fmt.Errorf("tombstone: unsupported format version: %d", version)
	}

	capacity := binary.LittleEndian.Uint32(data[8:12])
	count := binary.LittleEndian.Uint32(data[12:16])

	numWords := capacity / 64
	expectedSize := tombstoneHeaderSize + int(numWords)*8
	if len(data) < expectedSize {
		return fmt.Errorf("tombstone: truncated: expected %d bytes, got %d", expectedSize, len(data))
	}

	words := make([]uint64, numWords)
	for i := range words {
		offset := tombstoneHeaderSize + i*8
		words[i] = binary.LittleEndian.Uint64(data[offset : offset+8])
	}

	tb.words = words
	tb.count = count
	tb.capacity = capacity

	return nil
}

// Path returns the file path of this tombstone bitmap.
func (tb *TombstoneBitmap) Path() string {
	return tb.path
}

// isDeadUnlocked checks liveness without acquiring the lock.
// Caller must hold at least a read lock.
func (tb *TombstoneBitmap) isDeadUnlocked(nodeID uint32) bool {
	if nodeID >= tb.capacity {
		return false
	}
	return tb.words[nodeID>>6]&(1<<(nodeID&63)) != 0
}

// ensureCapacity grows the bitmap to accommodate nodeID.
// Caller must hold the write lock.
func (tb *TombstoneBitmap) ensureCapacity(nodeID uint32) {
	needed := wordAlignedCapacity(nodeID + 1)
	if needed <= tb.capacity {
		return
	}
	newWords := make([]uint64, needed/64)
	copy(newWords, tb.words)
	tb.words = newWords
	tb.capacity = needed
}

// wordAlignedCapacity rounds n up to the next multiple of 64.
func wordAlignedCapacity(n uint32) uint32 {
	return (n + 63) &^ 63
}

// LoadTombstoneBitmap loads a tombstone bitmap from a version directory.
// Returns an empty bitmap if the file doesn't exist.
func LoadTombstoneBitmap(versionPath string) (*TombstoneBitmap, error) {
	path := filepath.Join(versionPath, TombstoneFile)
	tb := &TombstoneBitmap{
		words:    make([]uint64, 0),
		count:    0,
		capacity: 0,
		path:     path,
	}
	if err := tb.Load(); err != nil {
		return nil, err
	}
	return tb, nil
}

// LoadOrCreateTombstoneBitmap loads an existing bitmap or creates a new one
// with capacity derived from maxNodeID.
func LoadOrCreateTombstoneBitmap(versionPath string, maxNodeID uint32) (*TombstoneBitmap, error) {
	path := filepath.Join(versionPath, TombstoneFile)

	// Try loading existing
	if _, err := os.Stat(path); err == nil {
		tb := &TombstoneBitmap{path: path}
		if err := tb.Load(); err != nil {
			return nil, err
		}
		return tb, nil
	}

	// Create new
	return NewTombstoneBitmap(path, maxNodeID), nil
}
