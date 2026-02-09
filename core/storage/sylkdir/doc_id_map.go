// Package sylkdir provides DocIDMap for bidirectional string ↔ uint32 document ID mapping.
package sylkdir

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DocIDMap constants.
const (
	// docIDMapMagic identifies a DocIDMap file ("DMAP" in ASCII).
	docIDMapMagic uint32 = 0x444D4150

	// docIDMapVersion is the current binary format version.
	docIDMapVersion uint32 = 1

	// docIDMapHeaderSize is the fixed header: [Magic:4][Version:4][Count:4][Next:4] = 16 bytes.
	docIDMapHeaderSize = 16
)

// DocIDMap maps string document IDs to dense uint32 IDs for OffsetIndex compatibility.
// IDs start at 1 (0 = no document). Thread-safe via sync.RWMutex.
//
// Binary format: [Magic:4][Version:4][Count:4][Next:4][entries: count × (keyLen:2 + key + id:4)]
type DocIDMap struct {
	forward map[string]uint32 // string ID → uint32
	reverse []string          // uint32 → string ID (index 0 unused, IDs start at 1)
	next    uint32            // next available ID (starts at 1)
	path    string
	mu      sync.RWMutex
}

// NewDocIDMap creates an empty DocIDMap at the given path.
func NewDocIDMap(path string) *DocIDMap {
	return &DocIDMap{
		forward: make(map[string]uint32),
		reverse: []string{""},  // index 0 = sentinel (no document)
		next:    1,
		path:    path,
	}
}

// GetOrAssign returns the uint32 ID for a string key, assigning a new one if absent.
func (m *DocIDMap) GetOrAssign(key string) uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id, ok := m.forward[key]; ok {
		return id
	}
	id := m.next
	m.next++
	m.forward[key] = id
	m.reverse = append(m.reverse, key)
	return id
}

// Get returns the uint32 ID for a string key. Returns 0, false if not found.
func (m *DocIDMap) Get(key string) (uint32, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.forward[key]
	return id, ok
}

// Reverse returns the string key for a uint32 ID. Returns "" for unknown IDs.
func (m *DocIDMap) Reverse(id uint32) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if int(id) >= len(m.reverse) {
		return ""
	}
	return m.reverse[id]
}

// Count returns the number of assigned document IDs.
func (m *DocIDMap) Count() uint32 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return uint32(len(m.forward))
}

// Path returns the file path.
func (m *DocIDMap) Path() string {
	return m.path
}

// Save persists the map to disk using write-to-temp + fsync + rename.
func (m *DocIDMap) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(m.path), 0755); err != nil {
		return fmt.Errorf("doc_id_map: create dir: %w", err)
	}

	tmpPath := m.path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("doc_id_map: create temp: %w", err)
	}

	// Write header
	header := make([]byte, docIDMapHeaderSize)
	binary.LittleEndian.PutUint32(header[0:4], docIDMapMagic)
	binary.LittleEndian.PutUint32(header[4:8], docIDMapVersion)
	binary.LittleEndian.PutUint32(header[8:12], uint32(len(m.forward)))
	binary.LittleEndian.PutUint32(header[12:16], m.next)
	if _, err := f.Write(header); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("doc_id_map: write header: %w", err)
	}

	// Write entries: [keyLen:2][key:keyLen][id:4]
	entryBuf := make([]byte, 6) // reused for keyLen(2) + id(4)
	for key, id := range m.forward {
		binary.LittleEndian.PutUint16(entryBuf[0:2], uint16(len(key)))
		if _, err := f.Write(entryBuf[0:2]); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("doc_id_map: write key len: %w", err)
		}
		if _, err := f.Write([]byte(key)); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("doc_id_map: write key: %w", err)
		}
		binary.LittleEndian.PutUint32(entryBuf[0:4], id)
		if _, err := f.Write(entryBuf[0:4]); err != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("doc_id_map: write id: %w", err)
		}
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("doc_id_map: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("doc_id_map: close: %w", err)
	}

	return os.Rename(tmpPath, m.path)
}

// Load reads the map from disk. Returns error if file is corrupt.
// If the file does not exist, the map remains empty (no error).
func (m *DocIDMap) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("doc_id_map: read: %w", err)
	}

	if len(data) < docIDMapHeaderSize {
		return fmt.Errorf("doc_id_map: file too small: %d bytes", len(data))
	}

	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != docIDMapMagic {
		return fmt.Errorf("doc_id_map: invalid magic: 0x%08X", magic)
	}

	version := binary.LittleEndian.Uint32(data[4:8])
	if version != docIDMapVersion {
		return fmt.Errorf("doc_id_map: unsupported version: %d", version)
	}

	count := binary.LittleEndian.Uint32(data[8:12])
	next := binary.LittleEndian.Uint32(data[12:16])

	forward := make(map[string]uint32, count)
	reverse := make([]string, next) // indices 0..next-1
	reverse[0] = ""                 // sentinel

	offset := docIDMapHeaderSize
	for i := uint32(0); i < count; i++ {
		if offset+2 > len(data) {
			return fmt.Errorf("doc_id_map: truncated at entry %d key length", i)
		}
		keyLen := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
		offset += 2

		if offset+keyLen+4 > len(data) {
			return fmt.Errorf("doc_id_map: truncated at entry %d", i)
		}
		key := string(data[offset : offset+keyLen])
		offset += keyLen

		id := binary.LittleEndian.Uint32(data[offset : offset+4])
		offset += 4

		forward[key] = id
		if int(id) < len(reverse) {
			reverse[id] = key
		}
	}

	m.forward = forward
	m.reverse = reverse
	m.next = next

	return nil
}

// LoadDocIDMap loads a DocIDMap from disk. Returns an empty map if the file doesn't exist.
func LoadDocIDMap(path string) (*DocIDMap, error) {
	m := NewDocIDMap(path)
	if err := m.Load(); err != nil {
		return nil, err
	}
	return m, nil
}
