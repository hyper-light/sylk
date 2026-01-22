package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

const DefaultVectorShardCapacity = 65536

type ShardedVectorStore struct {
	dir           string
	dim           int
	shardCapacity int

	shards  []*vectorShard
	shardMu sync.RWMutex

	count atomic.Uint64
}

type vectorShard struct {
	store *VectorStore
	mu    sync.RWMutex
}

func OpenShardedVectorStore(dir string) (*ShardedVectorStore, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("sharded vectors: open dir: %w", err)
	}

	var shards []*vectorShard
	var dim, shardCapacity int
	var maxCount uint64

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".bin" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		store, err := OpenVectorStore(path)
		if err != nil {
			for _, s := range shards {
				s.store.Close()
			}
			return nil, fmt.Errorf("sharded vectors: open shard %s: %w", entry.Name(), err)
		}

		if dim == 0 {
			dim = store.Dimension()
			shardCapacity = int(store.Capacity())
		}

		shards = append(shards, &vectorShard{store: store})

		shardIdx := len(shards) - 1
		shardMaxID := uint64(shardIdx)*uint64(shardCapacity) + store.Count()
		if shardMaxID > maxCount {
			maxCount = shardMaxID
		}
	}

	if len(shards) == 0 {
		return nil, fmt.Errorf("sharded vectors: no shards found in %s", dir)
	}

	s := &ShardedVectorStore{
		dir:           dir,
		dim:           dim,
		shardCapacity: shardCapacity,
		shards:        shards,
	}
	s.count.Store(maxCount)

	return s, nil
}

func CreateShardedVectorStore(dir string, dim int, shardCapacity int) (*ShardedVectorStore, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("sharded vectors: invalid dim=%d", dim)
	}

	if shardCapacity <= 0 {
		shardCapacity = DefaultVectorShardCapacity
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("sharded vectors: create dir: %w", err)
	}

	s := &ShardedVectorStore{
		dir:           dir,
		dim:           dim,
		shardCapacity: shardCapacity,
		shards:        make([]*vectorShard, 0, 8),
	}

	if err := s.createShard(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *ShardedVectorStore) createShard() error {
	shardIdx := len(s.shards)
	path := filepath.Join(s.dir, fmt.Sprintf("shard_%04d.bin", shardIdx))

	store, err := CreateVectorStore(path, s.dim, s.shardCapacity)
	if err != nil {
		return fmt.Errorf("sharded vectors: create shard %d: %w", shardIdx, err)
	}

	s.shards = append(s.shards, &vectorShard{store: store})
	return nil
}

func (s *ShardedVectorStore) ensureShard(vectorID uint32) error {
	shardIdx := int(vectorID) / s.shardCapacity

	s.shardMu.RLock()
	if shardIdx < len(s.shards) {
		s.shardMu.RUnlock()
		return nil
	}
	s.shardMu.RUnlock()

	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	for shardIdx >= len(s.shards) {
		if err := s.createShard(); err != nil {
			return err
		}
	}

	return nil
}

func (s *ShardedVectorStore) getShard(vectorID uint32) (*vectorShard, uint32) {
	shardIdx := int(vectorID) / s.shardCapacity
	localID := uint32(int(vectorID) % s.shardCapacity)

	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	if shardIdx >= len(s.shards) {
		return nil, 0
	}

	return s.shards[shardIdx], localID
}

func (s *ShardedVectorStore) Get(vectorID uint32) []float32 {
	shard, localID := s.getShard(vectorID)
	if shard == nil {
		return nil
	}

	shard.mu.RLock()
	defer shard.mu.RUnlock()

	return shard.store.Get(localID)
}

func (s *ShardedVectorStore) Append(vector []float32) (uint32, error) {
	if len(vector) != s.dim {
		return 0, ErrVectorDimensionMismatch
	}

	s.shardMu.Lock()

	currentShard := s.shards[len(s.shards)-1]
	currentShard.mu.Lock()

	if currentShard.store.Count() >= currentShard.store.Capacity() {
		currentShard.mu.Unlock()
		if err := s.createShard(); err != nil {
			s.shardMu.Unlock()
			return 0, err
		}
		currentShard = s.shards[len(s.shards)-1]
		currentShard.mu.Lock()
	}

	s.shardMu.Unlock()
	defer currentShard.mu.Unlock()

	localID, err := currentShard.store.Append(vector)
	if err != nil {
		return 0, err
	}

	s.shardMu.RLock()
	shardIdx := len(s.shards) - 1
	s.shardMu.RUnlock()

	globalID := uint32(shardIdx*s.shardCapacity) + localID

	for {
		current := s.count.Load()
		newCount := uint64(globalID) + 1
		if newCount <= current {
			break
		}
		if s.count.CompareAndSwap(current, newCount) {
			break
		}
	}

	return globalID, nil
}

func (s *ShardedVectorStore) Count() uint64 {
	return s.count.Load()
}

func (s *ShardedVectorStore) Capacity() uint64 {
	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	return uint64(len(s.shards)) * uint64(s.shardCapacity)
}

func (s *ShardedVectorStore) Dimension() int {
	return s.dim
}

func (s *ShardedVectorStore) ShardCount() int {
	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	return len(s.shards)
}

type ShardedVectorSnapshot struct {
	shards        []*VectorSnapshot
	shardCapacity int
	count         uint64
	dim           int
}

func (s *ShardedVectorStore) Snapshot() *ShardedVectorSnapshot {
	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	snapshots := make([]*VectorSnapshot, len(s.shards))
	for i, shard := range s.shards {
		shard.mu.RLock()
		snapshots[i] = shard.store.Snapshot()
		shard.mu.RUnlock()
	}

	return &ShardedVectorSnapshot{
		shards:        snapshots,
		shardCapacity: s.shardCapacity,
		count:         s.count.Load(),
		dim:           s.dim,
	}
}

func (snap *ShardedVectorSnapshot) Get(vectorID uint32) []float32 {
	if snap == nil || uint64(vectorID) >= snap.count {
		return nil
	}

	shardIdx := int(vectorID) / snap.shardCapacity
	localID := uint32(int(vectorID) % snap.shardCapacity)

	if shardIdx >= len(snap.shards) {
		return nil
	}

	return snap.shards[shardIdx].Get(localID)
}

func (snap *ShardedVectorSnapshot) Len() int {
	if snap == nil {
		return 0
	}
	return int(snap.count)
}

func (s *ShardedVectorStore) Sync() error {
	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	for i, shard := range s.shards {
		shard.mu.RLock()
		err := shard.store.Sync()
		shard.mu.RUnlock()

		if err != nil {
			return fmt.Errorf("sharded vectors: sync shard %d: %w", i, err)
		}
	}

	return nil
}

func (s *ShardedVectorStore) Close() error {
	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	var firstErr error
	for i, shard := range s.shards {
		shard.mu.Lock()
		err := shard.store.Close()
		shard.mu.Unlock()

		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sharded vectors: close shard %d: %w", i, err)
		}
	}

	s.shards = nil
	return firstErr
}

func (snap *ShardedVectorSnapshot) Dimension() int {
	return snap.dim
}

func (s *ShardedVectorStore) Set(vectorID uint32, vector []float32) error {
	if len(vector) != s.dim {
		return ErrVectorDimensionMismatch
	}

	if err := s.ensureShard(vectorID); err != nil {
		return err
	}

	shard, localID := s.getShard(vectorID)
	if shard == nil {
		return fmt.Errorf("sharded vectors: shard not found for vector %d", vectorID)
	}

	shard.mu.Lock()
	err := shard.store.Set(localID, vector)
	shard.mu.Unlock()

	if err != nil {
		return err
	}

	for {
		current := s.count.Load()
		newCount := uint64(vectorID) + 1
		if newCount <= current {
			break
		}
		if s.count.CompareAndSwap(current, newCount) {
			break
		}
	}

	return nil
}
