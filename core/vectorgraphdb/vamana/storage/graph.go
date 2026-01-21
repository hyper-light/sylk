package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"
	"unsafe"
)

// Graph storage errors.
var (
	ErrGraphClosed      = errors.New("graph store is closed")
	ErrNodeOutOfBounds  = errors.New("node ID exceeds graph capacity")
	ErrNeighborOverflow = errors.New("neighbor count exceeds maximum degree")
	ErrInvalidDegree    = errors.New("maximum degree must be positive")
	ErrInvalidCapacity  = errors.New("capacity must be positive")
	ErrHeaderCorrupt    = errors.New("graph header is corrupt or uninitialized")
)

// GraphStore provides fixed-degree graph storage backed by memory-mapped files.
//
// Binary layout:
//
//	[Header: 16 bytes][Node0][Node1]...[NodeN-1]
//
// Each node slot has a fixed size of (2 + R*4) bytes:
//
//	[count: 2 bytes (uint16)][neighbors: R * 4 bytes (R × uint32)]
//
// Thread safety:
//   - Concurrent reads are safe (GetNeighbors, GetNeighborCount, Count)
//   - Writes require external synchronization (SetNeighbors)
//   - Close must not be called while operations are in progress
type GraphStore struct {
	region   *MmapRegion
	header   *GraphHeader
	R        int
	nodeSize int64  // 2 + R*4 bytes per node
	capacity uint64 // Maximum number of nodes
	closed   atomic.Bool
}

// nodeSlotSize calculates the byte size of a single node slot.
// Each slot contains: count (2 bytes) + neighbors (R * 4 bytes).
func nodeSlotSize(R int) int64 {
	return 2 + int64(R)*4
}

// fileSize calculates the total file size for the given capacity and degree.
func fileSize(R int, capacity int) int64 {
	return HeaderSize + int64(capacity)*nodeSlotSize(R)
}

// OpenGraphStore opens an existing graph store from the specified path.
// The file must have been created with CreateGraph.
// Returns an error if the file doesn't exist or has an invalid header.
func OpenGraphStore(path string) (*GraphStore, error) {
	return openGraphStore(path, false)
}

// OpenGraphStoreReadOnly opens an existing graph store from the specified path for read-only access.
// The file must have been created with CreateGraph.
// Returns an error if the file doesn't exist or has an invalid header.
func OpenGraphStoreReadOnly(path string) (*GraphStore, error) {
	return openGraphStore(path, true)
}

// openGraphStore opens a graph store with the specified readonly mode.
func openGraphStore(path string, readonly bool) (*GraphStore, error) {
	region, err := MapFile(path, PageSize, readonly)
	if err != nil {
		return nil, fmt.Errorf("graph: open: %w", err)
	}

	data := region.Data()
	if len(data) < HeaderSize {
		region.Close()
		return nil, ErrHeaderCorrupt
	}

	var header GraphHeader
	if err := header.UnmarshalBinary(data[:HeaderSize]); err != nil {
		region.Close()
		return nil, fmt.Errorf("graph: %w: %v", ErrHeaderCorrupt, err)
	}

	if header.R == 0 {
		region.Close()
		return nil, fmt.Errorf("graph: %w: R=0", ErrHeaderCorrupt)
	}

	R := int(header.R)
	nodeSize := nodeSlotSize(R)
	capacity := (region.Size() - HeaderSize) / nodeSize

	region.Close()

	requiredSize := fileSize(R, int(capacity))
	region, err = MapFile(path, requiredSize, readonly)
	if err != nil {
		return nil, fmt.Errorf("graph: remap: %w", err)
	}

	return &GraphStore{
		region:   region,
		header:   &header,
		R:        R,
		nodeSize: nodeSize,
		capacity: uint64(capacity),
	}, nil
}

// CreateGraphStore creates a new graph store at the specified path.
// R is the maximum number of neighbors per node (degree).
// capacity is the maximum number of nodes the graph can hold.
// Returns an error if the file already exists.
func CreateGraphStore(path string, R int, capacity int) (*GraphStore, error) {
	if R <= 0 {
		return nil, fmt.Errorf("graph: create: %w: R=%d", ErrInvalidDegree, R)
	}
	if capacity <= 0 {
		return nil, fmt.Errorf("graph: create: %w: capacity=%d", ErrInvalidCapacity, capacity)
	}

	size := fileSize(R, capacity)

	region, err := MapFile(path, size, false)
	if err != nil {
		return nil, fmt.Errorf("graph: create: %w", err)
	}

	header := GraphHeader{
		R:     uint32(R),
		Count: 0,
		Flags: 0,
	}

	headerBytes, _ := header.MarshalBinary()
	copy(region.Data()[:HeaderSize], headerBytes)

	return &GraphStore{
		region:   region,
		header:   &header,
		R:        R,
		nodeSize: nodeSlotSize(R),
		capacity: uint64(capacity),
	}, nil
}

// GetNeighbors returns the neighbor list for the given node.
// The returned slice is a zero-copy view into the memory-mapped region.
// The caller must not modify the returned slice or retain it after Close.
// Returns nil if the node has no neighbors or the store is closed.
func (s *GraphStore) GetNeighbors(internalID uint32) []uint32 {
	if s.closed.Load() {
		return nil
	}

	if uint64(internalID) >= s.capacity {
		return nil
	}

	data := s.region.Data()
	offset := s.nodeOffset(internalID)
	count := binary.LittleEndian.Uint16(data[offset : offset+2])

	if count == 0 {
		return nil
	}

	neighborsPtr := unsafe.Pointer(&data[offset+2])
	return unsafe.Slice((*uint32)(neighborsPtr), int(count))
}

// SetNeighbors sets the neighbor list for the given node.
// If len(neighbors) > R, the list is truncated to R elements.
// Returns an error if the node ID exceeds capacity or the store is closed.
func (s *GraphStore) SetNeighbors(internalID uint32, neighbors []uint32) error {
	if s.closed.Load() {
		return ErrGraphClosed
	}

	if uint64(internalID) >= s.capacity {
		return fmt.Errorf("graph: set neighbors: %w: id=%d, capacity=%d",
			ErrNodeOutOfBounds, internalID, s.capacity)
	}

	count := len(neighbors)
	if count > s.R {
		count = s.R
	}

	data := s.region.Data()
	offset := s.nodeOffset(internalID)

	binary.LittleEndian.PutUint16(data[offset:offset+2], uint16(count))

	if count > 0 {
		neighborBytes := unsafe.Slice((*byte)(unsafe.Pointer(&neighbors[0])), count*4)
		copy(data[offset+2:], neighborBytes)
	}

	currentCount := s.header.Count
	if uint64(internalID) >= currentCount {
		s.header.Count = uint64(internalID) + 1
		headerBytes, _ := s.header.MarshalBinary()
		copy(data[:HeaderSize], headerBytes)
	}

	return nil
}

// GetNeighborCount returns the number of neighbors for the given node.
// Returns 0 if the node has no neighbors, the ID is out of bounds, or the store is closed.
func (s *GraphStore) GetNeighborCount(internalID uint32) uint16 {
	if s.closed.Load() {
		return 0
	}

	if uint64(internalID) >= s.capacity {
		return 0
	}

	data := s.region.Data()
	offset := s.nodeOffset(internalID)
	return binary.LittleEndian.Uint16(data[offset : offset+2])
}

// Count returns the number of nodes that have been written to the graph.
// This is the highest node ID + 1 that has been set.
func (s *GraphStore) Count() uint64 {
	if s.closed.Load() {
		return 0
	}
	return s.header.Count
}

// MaxDegree returns the maximum number of neighbors per node (R).
func (s *GraphStore) MaxDegree() int {
	return s.R
}

// Capacity returns the maximum number of nodes the graph can hold.
func (s *GraphStore) Capacity() uint64 {
	return s.capacity
}

// Sync flushes all pending writes to disk.
func (s *GraphStore) Sync() error {
	if s.closed.Load() {
		return ErrGraphClosed
	}
	return s.region.Sync()
}

// Close releases all resources associated with the graph store.
// It is safe to call Close multiple times.
func (s *GraphStore) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.region.Close()
}

// nodeOffset calculates the byte offset for a node's data.
// Offset = HeaderSize + internalID * nodeSize
func (s *GraphStore) nodeOffset(internalID uint32) int64 {
	return HeaderSize + int64(internalID)*s.nodeSize
}
