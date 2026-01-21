// Package storage provides mmap-based storage types for the Vamana index.
package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

// LabelSize is the number of bytes per label entry.
// Layout: domain (1 byte) + nodeType (2 bytes) = 3 bytes.
const LabelSize = 3

// Errors for label store operations.
var (
	ErrLabelStoreClosed   = errors.New("label store is closed")
	ErrLabelOutOfBounds   = errors.New("label index out of bounds")
	ErrLabelStoreReadonly = errors.New("label store is readonly")
)

// LabelStore provides memory-mapped storage for node labels.
// Each label consists of a domain (uint8) and nodeType (uint16), packed into 3 bytes.
//
// Binary layout:
//
//	[Header: 16 bytes][Label0: 3 bytes][Label1: 3 bytes]...
//
// Label N is located at offset: HeaderSize + N * LabelSize
//
// Thread-safety: concurrent reads are safe; writes require external synchronization.
type LabelStore struct {
	region   *MmapRegion
	header   *LabelHeader
	capacity uint64
	mu       sync.RWMutex
	closed   atomic.Bool
}

// OpenLabelStore opens an existing label store file for reading and writing.
// Returns an error if the file does not exist or has an invalid header.
func OpenLabelStore(path string) (*LabelStore, error) {
	return openLabelStore(path, false)
}

// OpenLabelStoreReadOnly opens an existing label store file for reading only.
// Returns an error if the file does not exist or has an invalid header.
func OpenLabelStoreReadOnly(path string) (*LabelStore, error) {
	return openLabelStore(path, true)
}

// openLabelStore opens a label store with the specified readonly mode.
func openLabelStore(path string, readonly bool) (*LabelStore, error) {
	// Map just the header first to read metadata.
	region, err := MapFile(path, HeaderSize, readonly)
	if err != nil {
		return nil, fmt.Errorf("label store: open header: %w", err)
	}

	// Parse the header.
	var header LabelHeader
	if err := header.UnmarshalBinary(region.Data()[:HeaderSize]); err != nil {
		region.Close()
		return nil, fmt.Errorf("label store: parse header: %w", err)
	}

	// Close the small mapping and remap the full file.
	region.Close()

	// Calculate capacity from file size.
	capacity := header.Count
	if capacity == 0 {
		// Empty store, use minimum capacity.
		capacity = 1
	}

	fullSize := int64(HeaderSize) + int64(capacity)*int64(LabelSize)
	region, err = MapFile(path, fullSize, readonly)
	if err != nil {
		return nil, fmt.Errorf("label store: open full: %w", err)
	}

	return &LabelStore{
		region:   region,
		header:   &header,
		capacity: capacity,
	}, nil
}

// CreateLabelStore creates a new label store file with the specified capacity.
func CreateLabelStore(path string, capacity int) (*LabelStore, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("label store: capacity must be positive, got %d", capacity)
	}

	// Calculate total file size: header + labels.
	totalSize := int64(HeaderSize) + int64(capacity)*int64(LabelSize)

	// Create and map the file.
	region, err := MapFile(path, totalSize, false)
	if err != nil {
		return nil, fmt.Errorf("label store: create: %w", err)
	}

	// Initialize the header.
	header := &LabelHeader{
		DomainBits: 8,  // Full 8 bits for domain.
		TypeBits:   16, // Full 16 bits for nodeType.
		Count:      0,
		Flags:      0,
	}

	// Write the header to the mapped region.
	headerBytes, err := header.MarshalBinary()
	if err != nil {
		region.Close()
		return nil, fmt.Errorf("label store: marshal header: %w", err)
	}
	copy(region.Data()[:HeaderSize], headerBytes)

	return &LabelStore{
		region:   region,
		header:   header,
		capacity: uint64(capacity),
	}, nil
}

// labelOffset calculates the byte offset for a label at the given index.
func labelOffset(internalID uint32) int {
	return HeaderSize + int(internalID)*LabelSize
}

// Get retrieves the domain and nodeType for the specified internal ID.
// Returns (0, 0) if the ID is out of bounds or the store is closed.
func (s *LabelStore) Get(internalID uint32) (domain uint8, nodeType uint16) {
	if s.closed.Load() {
		return 0, 0
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if uint64(internalID) >= s.header.Count {
		return 0, 0
	}

	data := s.region.Data()
	if data == nil {
		return 0, 0
	}

	offset := labelOffset(internalID)
	if offset+LabelSize > len(data) {
		return 0, 0
	}

	domain = data[offset]
	nodeType = binary.LittleEndian.Uint16(data[offset+1 : offset+3])
	return domain, nodeType
}

// Set stores the domain and nodeType for the specified internal ID.
// Returns an error if the ID exceeds capacity or the store is closed/readonly.
func (s *LabelStore) Set(internalID uint32, domain uint8, nodeType uint16) error {
	if s.closed.Load() {
		return ErrLabelStoreClosed
	}

	if s.region.Readonly() {
		return ErrLabelStoreReadonly
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if uint64(internalID) >= s.capacity {
		return fmt.Errorf("%w: id %d >= capacity %d", ErrLabelOutOfBounds, internalID, s.capacity)
	}

	data := s.region.Data()
	if data == nil {
		return ErrLabelStoreClosed
	}

	offset := labelOffset(internalID)
	if offset+LabelSize > len(data) {
		return fmt.Errorf("%w: offset %d exceeds data length %d", ErrLabelOutOfBounds, offset+LabelSize, len(data))
	}

	// Write the label.
	data[offset] = domain
	binary.LittleEndian.PutUint16(data[offset+1:offset+3], nodeType)

	// Update count if this is a new label.
	if uint64(internalID) >= s.header.Count {
		s.header.Count = uint64(internalID) + 1
		// Write updated count to the header in the mapped region.
		headerBytes, err := s.header.MarshalBinary()
		if err != nil {
			return fmt.Errorf("label store: marshal header: %w", err)
		}
		copy(data[:HeaderSize], headerBytes)
	}

	return nil
}

// MatchesFilter checks if a label matches the given filter criteria.
// If domains is empty, all domains match. If nodeTypes is empty, all types match.
func MatchesFilter(domain uint8, nodeType uint16, domains []uint8, nodeTypes []uint16) bool {
	// Check domain filter.
	if len(domains) > 0 {
		domainMatch := false
		for _, d := range domains {
			if d == domain {
				domainMatch = true
				break
			}
		}
		if !domainMatch {
			return false
		}
	}

	// Check nodeType filter.
	if len(nodeTypes) > 0 {
		typeMatch := false
		for _, t := range nodeTypes {
			if t == nodeType {
				typeMatch = true
				break
			}
		}
		if !typeMatch {
			return false
		}
	}

	return true
}

// FilterMask generates a boolean mask for all labels matching the filter criteria.
// Returns mask[i] = true if the label at index i matches the filter.
// If domains is empty, all domains match. If nodeTypes is empty, all types match.
// Returns nil if the store is closed.
func (s *LabelStore) FilterMask(domains []uint8, nodeTypes []uint16) []bool {
	if s.closed.Load() {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	count := s.header.Count
	if count == 0 {
		return []bool{}
	}

	data := s.region.Data()
	if data == nil {
		return nil
	}

	mask := make([]bool, count)

	// Build lookup sets for O(1) membership testing when filters are large.
	var domainSet map[uint8]struct{}
	var typeSet map[uint16]struct{}

	if len(domains) > 0 {
		domainSet = make(map[uint8]struct{}, len(domains))
		for _, d := range domains {
			domainSet[d] = struct{}{}
		}
	}

	if len(nodeTypes) > 0 {
		typeSet = make(map[uint16]struct{}, len(nodeTypes))
		for _, t := range nodeTypes {
			typeSet[t] = struct{}{}
		}
	}

	// Iterate all labels and check against filters.
	for i := uint64(0); i < count; i++ {
		offset := HeaderSize + int(i)*LabelSize
		if offset+LabelSize > len(data) {
			break
		}

		domain := data[offset]
		nodeType := binary.LittleEndian.Uint16(data[offset+1 : offset+3])

		// Check domain filter.
		if domainSet != nil {
			if _, ok := domainSet[domain]; !ok {
				continue
			}
		}

		// Check nodeType filter.
		if typeSet != nil {
			if _, ok := typeSet[nodeType]; !ok {
				continue
			}
		}

		mask[i] = true
	}

	return mask
}

// Count returns the number of labels stored.
func (s *LabelStore) Count() uint64 {
	if s.closed.Load() {
		return 0
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.header.Count
}

// Capacity returns the maximum number of labels the store can hold.
func (s *LabelStore) Capacity() uint64 {
	if s.closed.Load() {
		return 0
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.capacity
}

// Sync flushes any pending writes to disk.
// Returns an error if the store is readonly or closed.
func (s *LabelStore) Sync() error {
	if s.closed.Load() {
		return ErrLabelStoreClosed
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.region.Sync()
}

// Close releases all resources associated with the label store.
// It is safe to call Close multiple times; subsequent calls are no-ops.
func (s *LabelStore) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.region != nil {
		return s.region.Close()
	}

	return nil
}
