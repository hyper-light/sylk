package storage

import (
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"sync"
)

// IDMap provides bidirectional O(1) mapping between external string IDs
// and internal sequential uint32 IDs. Thread-safe via sync.RWMutex.
//
// Internal IDs are assigned sequentially starting from 0 with no gaps.
// The mapping is persisted to JSON format for debuggability.
type IDMap struct {
	toInternal map[string]uint32 // external ID -> internal ID
	toExternal []string          // internal ID (index) -> external ID
	mu         sync.RWMutex
}

// idMapJSON is the JSON serialization format for IDMap.
// We store only the toExternal slice; toInternal is rebuilt on load.
type idMapJSON struct {
	ExternalIDs []string `json:"external_ids"`
}

// NewIDMap creates a new empty IDMap.
func NewIDMap() *IDMap {
	return &IDMap{
		toInternal: make(map[string]uint32),
		toExternal: make([]string, 0),
	}
}

// Assign assigns an internal ID to an external ID. If the external ID
// is already assigned, returns the existing internal ID (idempotent).
// Returns the internal ID assigned to this external ID.
func (m *IDMap) Assign(externalID string) uint32 {
	// Fast path: check if already assigned with read lock
	m.mu.RLock()
	if id, exists := m.toInternal[externalID]; exists {
		m.mu.RUnlock()
		return id
	}
	m.mu.RUnlock()

	// Slow path: acquire write lock and assign
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if id, exists := m.toInternal[externalID]; exists {
		return id
	}

	// Assign next sequential ID (no gaps)
	internalID := uint32(len(m.toExternal))
	m.toInternal[externalID] = internalID
	m.toExternal = append(m.toExternal, externalID)

	return internalID
}

// ToInternal returns the internal ID for the given external ID.
// Returns (id, true) if found, (0, false) otherwise.
func (m *IDMap) ToInternal(externalID string) (uint32, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id, exists := m.toInternal[externalID]
	return id, exists
}

// ToExternal returns the external ID for the given internal ID.
// Returns (id, true) if found, ("", false) otherwise.
func (m *IDMap) ToExternal(internalID uint32) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if int(internalID) >= len(m.toExternal) {
		return "", false
	}
	return m.toExternal[internalID], true
}

// Contains returns true if the external ID is already assigned.
func (m *IDMap) Contains(externalID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.toInternal[externalID]
	return exists
}

// Size returns the number of ID mappings.
func (m *IDMap) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.toExternal)
}

// Save persists the IDMap to a JSON file at the given path.
func (m *IDMap) Save(path string) error {
	m.mu.RLock()
	// Copy data while holding lock to avoid issues
	data := idMapJSON{
		ExternalIDs: make([]string, len(m.toExternal)),
	}
	copy(data.ExternalIDs, m.toExternal)
	m.mu.RUnlock()

	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, encoded, 0o644)
}

// Load loads the IDMap from a JSON file at the given path, replacing
// any existing data.
func (m *IDMap) Load(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var data idMapJSON
	if err := json.Unmarshal(content, &data); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Rebuild both maps from the external IDs slice
	m.toExternal = data.ExternalIDs
	m.toInternal = make(map[string]uint32, len(m.toExternal))

	for i, extID := range m.toExternal {
		m.toInternal[extID] = uint32(i)
	}

	return nil
}

// LoadIDMap loads an IDMap from a JSON file at the given path.
// This is the static factory function for loading persisted IDMaps.
func LoadIDMap(path string) (*IDMap, error) {
	m := NewIDMap()
	if err := m.Load(path); err != nil {
		return nil, err
	}
	return m, nil
}

var idmapMagic = [4]byte{'I', 'D', 'M', 'P'}

const idmapVersion uint32 = 1

func (m *IDMap) SaveBinary(path string) error {
	m.mu.RLock()
	externalIDs := make([]string, len(m.toExternal))
	copy(externalIDs, m.toExternal)
	m.mu.RUnlock()

	var totalLen int
	for _, id := range externalIDs {
		if len(id) > 65535 {
			return ErrIDTooLong
		}
		totalLen += 2 + len(id)
	}

	buf := make([]byte, 12+totalLen+4)
	copy(buf[0:4], idmapMagic[:])
	binary.LittleEndian.PutUint32(buf[4:8], idmapVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(externalIDs)))

	offset := 12
	for _, extID := range externalIDs {
		binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(extID)))
		offset += 2
		copy(buf[offset:offset+len(extID)], extID)
		offset += len(extID)
	}

	checksum := crc32.ChecksumIEEE(buf[:offset])
	binary.LittleEndian.PutUint32(buf[offset:offset+4], checksum)

	return os.WriteFile(path, buf, 0o644)
}

func (m *IDMap) LoadBinary(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if len(data) < 16 {
		return ErrInvalidMagic
	}

	if [4]byte(data[0:4]) != idmapMagic {
		return ErrInvalidMagic
	}

	version := binary.LittleEndian.Uint32(data[4:8])
	if version != idmapVersion {
		return ErrUnsupportedVersion
	}

	count := binary.LittleEndian.Uint32(data[8:12])

	storedChecksum := binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(data[:len(data)-4]) != storedChecksum {
		return ErrChecksumMismatch
	}

	externalIDs := make([]string, count)
	offset := 12
	for i := range count {
		if offset+2 > len(data)-4 {
			return ErrChecksumMismatch
		}
		length := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if offset+length > len(data)-4 {
			return ErrChecksumMismatch
		}
		externalIDs[i] = string(data[offset : offset+length])
		offset += length
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.toExternal = externalIDs
	m.toInternal = make(map[string]uint32, len(externalIDs))
	for i, extID := range externalIDs {
		m.toInternal[extID] = uint32(i)
	}

	return nil
}

func LoadIDMapBinary(path string) (*IDMap, error) {
	m := NewIDMap()
	if err := m.LoadBinary(path); err != nil {
		return nil, err
	}
	return m, nil
}
