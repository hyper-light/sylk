// Package storage provides mmap-based storage types for the Vamana index.
package storage

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"
)

// VectorStore provides mmap-backed storage for dense vectors with zero-copy access.
//
// File format:
//
//	[Header: 16B][Vector0: dim*4B][Vector1: dim*4B]...
//
// Thread safety: concurrent reads are safe; writes require external synchronization.
// The store uses RWMutex internally for read/write coordination.
type VectorStore struct {
	region   *MmapRegion
	header   *VectorHeader
	dim      int
	capacity uint64

	mu sync.RWMutex
}

// Vector storage errors.
var (
	ErrVectorDimensionMismatch = errors.New("vector dimension mismatch")
	ErrVectorCapacityExceeded  = errors.New("vector capacity exceeded")
	ErrVectorIndexOutOfBounds  = errors.New("vector index out of bounds")
	ErrVectorStoreNotWritable  = errors.New("vector store is read-only")
	ErrVectorStoreEmpty        = errors.New("vector store is empty")
	ErrInvalidVectorDimension  = errors.New("invalid vector dimension")
	ErrInvalidVectorCapacity   = errors.New("invalid vector capacity")
	ErrVectorStoreClosed       = errors.New("vector store is closed")
)

// vectorSize returns the byte size of a single vector with the given dimension.
func vectorSize(dim int) int64 {
	return int64(dim) * 4 // 4 bytes per float32
}

// vectorOffset returns the byte offset of vector N in the file.
func vectorOffset(dim int, n uint64) int64 {
	return HeaderSize + int64(n)*vectorSize(dim)
}

// OpenVectorStore opens an existing vector store file for read-write access.
func OpenVectorStore(path string) (*VectorStore, error) {
	return openVectorStore(path, false)
}

// OpenVectorStoreReadOnly opens an existing vector store file for read-only access.
func OpenVectorStoreReadOnly(path string) (*VectorStore, error) {
	return openVectorStore(path, true)
}

func openVectorStore(path string, readonly bool) (*VectorStore, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat vector store: %w", err)
	}

	size := info.Size()
	if size < HeaderSize {
		return nil, fmt.Errorf("vector store file too small: %d bytes", size)
	}

	region, err := MapFile(path, size, readonly)
	if err != nil {
		return nil, fmt.Errorf("map vector store: %w", err)
	}

	header := &VectorHeader{}
	if err := header.UnmarshalBinary(region.Data()[:HeaderSize]); err != nil {
		_ = region.Close()
		return nil, fmt.Errorf("unmarshal vector header: %w", err)
	}

	if header.Dim == 0 {
		_ = region.Close()
		return nil, ErrInvalidVectorDimension
	}

	// Calculate capacity from file size.
	dataSize := size - HeaderSize
	vecSize := vectorSize(int(header.Dim))
	capacity := uint64(dataSize / vecSize)

	return &VectorStore{
		region:   region,
		header:   header,
		dim:      int(header.Dim),
		capacity: capacity,
	}, nil
}

// CreateVectorStore creates a new vector store file with the specified dimension and capacity.
func CreateVectorStore(path string, dim int, capacity int) (*VectorStore, error) {
	if dim <= 0 {
		return nil, ErrInvalidVectorDimension
	}
	if capacity <= 0 {
		return nil, ErrInvalidVectorCapacity
	}

	// Calculate total file size: header + capacity * vectorSize.
	totalSize := HeaderSize + int64(capacity)*vectorSize(dim)

	// Create and size the file.
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create vector store file: %w", err)
	}

	if err := f.Truncate(totalSize); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("truncate vector store file: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close vector store file: %w", err)
	}

	// Map the file.
	region, err := MapFile(path, totalSize, false)
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("map vector store: %w", err)
	}

	// Initialize header.
	header := &VectorHeader{
		Dim:   uint32(dim),
		Count: 0,
		Flags: 0,
	}

	headerBytes, err := header.MarshalBinary()
	if err != nil {
		_ = region.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("marshal vector header: %w", err)
	}

	copy(region.Data()[:HeaderSize], headerBytes)

	return &VectorStore{
		region:   region,
		header:   header,
		dim:      dim,
		capacity: uint64(capacity),
	}, nil
}

// Get returns the vector at the given internal ID using zero-copy access.
// The returned slice points directly into the mmap region and must not be modified.
// The slice is valid only as long as the VectorStore is open.
func (s *VectorStore) Get(internalID uint32) []float32 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.region == nil {
		return nil
	}

	if uint64(internalID) >= s.header.Count {
		return nil
	}

	data := s.region.Data()
	if data == nil {
		return nil
	}

	offset := vectorOffset(s.dim, uint64(internalID))
	ptr := unsafe.Pointer(&data[offset])
	return unsafe.Slice((*float32)(ptr), s.dim)
}

// Append adds a new vector to the store and returns its internal ID.
// Returns an error if the vector dimension doesn't match or capacity is exceeded.
func (s *VectorStore) Append(vector []float32) (uint32, error) {
	if len(vector) != s.dim {
		return 0, ErrVectorDimensionMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.region == nil {
		return 0, ErrVectorStoreClosed
	}

	if s.region.Readonly() {
		return 0, ErrVectorStoreNotWritable
	}

	if s.header.Count >= s.capacity {
		return 0, ErrVectorCapacityExceeded
	}

	data := s.region.Data()
	if data == nil {
		return 0, ErrVectorStoreClosed
	}

	internalID := uint32(s.header.Count)
	offset := vectorOffset(s.dim, s.header.Count)

	dst := data[offset : offset+vectorSize(s.dim)]
	for i, v := range vector {
		binary.LittleEndian.PutUint32(dst[i*4:], floatToUint32(v))
	}

	s.header.Count++
	binary.LittleEndian.PutUint64(data[4:12], s.header.Count)

	return internalID, nil
}

// Count returns the number of vectors currently stored.
func (s *VectorStore) Count() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.header == nil {
		return 0
	}

	return s.header.Count
}

// Dimension returns the dimensionality of vectors in this store.
func (s *VectorStore) Dimension() int {
	return s.dim
}

// Capacity returns the maximum number of vectors this store can hold.
func (s *VectorStore) Capacity() uint64 {
	return s.capacity
}

// Sync flushes all changes to disk.
func (s *VectorStore) Sync() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.region == nil {
		return ErrVectorStoreClosed
	}

	return s.region.Sync()
}

// Close unmaps the file and releases resources.
func (s *VectorStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.region == nil {
		return nil
	}

	err := s.region.Close()
	s.region = nil
	s.header = nil
	return err
}

// floatToUint32 converts a float32 to its IEEE 754 binary representation.
func floatToUint32(f float32) uint32 {
	return *(*uint32)(unsafe.Pointer(&f))
}
