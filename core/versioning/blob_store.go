package versioning

import (
	"errors"
	"sync"
)

var (
	ErrBlobNotFound = errors.New("blob not found")
	ErrBlobExists   = errors.New("blob already exists")
)

type BlobStore interface {
	Get(hash ContentHash) ([]byte, error)
	Put(content []byte) (ContentHash, error)
	Has(hash ContentHash) bool
	Delete(hash ContentHash) error
	Size() int64
	Count() int
}

type MemoryBlobStore struct {
	mu    sync.RWMutex
	blobs map[ContentHash][]byte
	size  int64
}

func NewMemoryBlobStore() *MemoryBlobStore {
	return &MemoryBlobStore{
		blobs: make(map[ContentHash][]byte),
	}
}

func (s *MemoryBlobStore) Get(hash ContentHash) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	content, ok := s.blobs[hash]
	if !ok {
		return nil, ErrBlobNotFound
	}
	return cloneBlobContent(content), nil
}

func cloneBlobContent(content []byte) []byte {
	result := make([]byte, len(content))
	copy(result, content)
	return result
}

func (s *MemoryBlobStore) Put(content []byte) (ContentHash, error) {
	hash := ComputeContentHash(content)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.blobs[hash]; exists {
		return hash, nil
	}

	s.blobs[hash] = cloneBlobContent(content)
	s.size += int64(len(content))
	return hash, nil
}

func (s *MemoryBlobStore) Has(hash ContentHash) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.blobs[hash]
	return ok
}

func (s *MemoryBlobStore) Delete(hash ContentHash) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, ok := s.blobs[hash]
	if !ok {
		return ErrBlobNotFound
	}

	s.size -= int64(len(content))
	delete(s.blobs, hash)
	return nil
}

func (s *MemoryBlobStore) Size() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
}

func (s *MemoryBlobStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.blobs)
}

func (s *MemoryBlobStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs = make(map[ContentHash][]byte)
	s.size = 0
}

func (s *MemoryBlobStore) Hashes() []ContentHash {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hashes := make([]ContentHash, 0, len(s.blobs))
	for hash := range s.blobs {
		hashes = append(hashes, hash)
	}
	return hashes
}
