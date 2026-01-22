package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

type shardedVectorMeta struct {
	ShardCapacity int `yaml:"shard_capacity"`
	Dim           int `yaml:"dim"`
	Total         int `yaml:"total"`
}

const (
	VectorsPerShard = 65536
	shardShift      = 16            // log2(65536)
	shardMask       = (1 << 16) - 1 // 0xFFFF
)

type ShardedVectorStore struct {
	dir           string
	dim           int
	shardCapacity int
	useBitOps     bool

	shards  []*vectorShard
	shardMu sync.RWMutex

	total atomic.Uint64
}

type vectorShard struct {
	store *VectorStore
	mu    sync.RWMutex
}

func OpenShardedVectorStore(dir string) (*ShardedVectorStore, error) {
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.yaml"))
	if err != nil {
		return nil, fmt.Errorf("sharded vectors: read meta: %w", err)
	}

	var meta shardedVectorMeta
	if err := yaml.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("sharded vectors: parse meta: %w", err)
	}

	numShards := (meta.Total + meta.ShardCapacity - 1) / meta.ShardCapacity
	if numShards == 0 {
		numShards = 1
	}

	shards := make([]*vectorShard, numShards)
	for i := range numShards {
		path := filepath.Join(dir, fmt.Sprintf("shard_%04d.bin", i))
		store, err := OpenVectorStore(path)
		if err != nil {
			for j := range i {
				shards[j].store.Close()
			}
			return nil, fmt.Errorf("sharded vectors: open shard %d: %w", i, err)
		}
		shards[i] = &vectorShard{store: store}
	}

	s := &ShardedVectorStore{
		dir:           dir,
		dim:           meta.Dim,
		shardCapacity: meta.ShardCapacity,
		useBitOps:     meta.ShardCapacity == VectorsPerShard,
		shards:        shards,
	}
	s.total.Store(uint64(meta.Total))
	return s, nil
}

func CreateShardedVectorStore(dir string, dim int, shardCapacity int) (*ShardedVectorStore, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("sharded vectors: invalid dim=%d", dim)
	}

	if shardCapacity <= 0 {
		shardCapacity = VectorsPerShard
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("sharded vectors: create dir: %w", err)
	}

	s := &ShardedVectorStore{
		dir:           dir,
		dim:           dim,
		shardCapacity: shardCapacity,
		useBitOps:     shardCapacity == VectorsPerShard,
		shards:        make([]*vectorShard, 0, 8),
	}

	if err := s.createShard(); err != nil {
		return nil, err
	}

	if err := s.saveMeta(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *ShardedVectorStore) saveMeta() error {
	meta := shardedVectorMeta{
		ShardCapacity: s.shardCapacity,
		Dim:           s.dim,
		Total:         int(s.total.Load()),
	}
	data, err := yaml.Marshal(&meta)
	if err != nil {
		return fmt.Errorf("sharded vectors: marshal meta: %w", err)
	}
	return os.WriteFile(filepath.Join(s.dir, "meta.yaml"), data, 0644)
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
	var shardIdx int
	if s.useBitOps {
		shardIdx = int(vectorID >> shardShift)
	} else {
		shardIdx = int(vectorID) / s.shardCapacity
	}

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
	var shardIdx int
	var localID uint32
	if s.useBitOps {
		shardIdx = int(vectorID >> shardShift)
		localID = vectorID & shardMask
	} else {
		shardIdx = int(vectorID) / s.shardCapacity
		localID = uint32(int(vectorID) % s.shardCapacity)
	}

	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	if shardIdx >= len(s.shards) {
		return nil, 0
	}

	return s.shards[shardIdx], localID
}

// Get returns the vector at the given ID.
// No lock needed: mmap reads are thread-safe, and Get returns a zero-copy
// slice into mmap memory. Concurrent writes to different vectors are safe.
func (s *ShardedVectorStore) Get(vectorID uint32) []float32 {
	shard, localID := s.getShard(vectorID)
	if shard == nil {
		return nil
	}
	return shard.store.Get(localID)
}

func (s *ShardedVectorStore) Append(vector []float32) (uint32, error) {
	if len(vector) != s.dim {
		return 0, ErrVectorDimensionMismatch
	}

	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	total := int(s.total.Load())
	var shardIdx int
	if s.useBitOps {
		shardIdx = total >> shardShift
	} else {
		shardIdx = total / s.shardCapacity
	}

	for shardIdx >= len(s.shards) {
		if err := s.createShard(); err != nil {
			return 0, err
		}
	}

	currentShard := s.shards[shardIdx]
	currentShard.mu.Lock()
	_, err := currentShard.store.Append(vector)
	currentShard.mu.Unlock()
	if err != nil {
		return 0, err
	}

	s.total.Add(1)
	return uint32(total), nil
}

func (s *ShardedVectorStore) AppendBatch(vectors [][]float32) (startID uint32, err error) {
	if len(vectors) == 0 {
		return 0, nil
	}

	for _, v := range vectors {
		if len(v) != s.dim {
			return 0, ErrVectorDimensionMismatch
		}
	}

	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	total := int(s.total.Load())
	startID = uint32(total)
	remaining := vectors

	for len(remaining) > 0 {
		var shardIdx, localID, space int
		if s.useBitOps {
			shardIdx = total >> shardShift
			localID = total & shardMask
			space = VectorsPerShard - localID
		} else {
			shardIdx = total / s.shardCapacity
			localID = total % s.shardCapacity
			space = s.shardCapacity - localID
		}

		for shardIdx >= len(s.shards) {
			if err := s.createShard(); err != nil {
				return 0, err
			}
		}

		take := min(space, len(remaining))
		batch := remaining[:take]
		remaining = remaining[take:]

		currentShard := s.shards[shardIdx]
		currentShard.mu.Lock()
		_, err := currentShard.store.AppendBatch(batch)
		currentShard.mu.Unlock()
		if err != nil {
			return 0, err
		}

		total += take
	}

	s.total.Store(uint64(total))
	return startID, nil
}

func (s *ShardedVectorStore) Count() uint64 {
	return s.total.Load()
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
	useBitOps     bool
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
		useBitOps:     s.useBitOps,
		count:         s.total.Load(),
		dim:           s.dim,
	}
}

func (snap *ShardedVectorSnapshot) Get(vectorID uint32) []float32 {
	if snap == nil || uint64(vectorID) >= snap.count {
		return nil
	}

	var shardIdx int
	var localID uint32
	if snap.useBitOps {
		shardIdx = int(vectorID >> shardShift)
		localID = vectorID & shardMask
	} else {
		shardIdx = int(vectorID) / snap.shardCapacity
		localID = uint32(int(vectorID) % snap.shardCapacity)
	}

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

	return s.saveMeta()
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
		current := s.total.Load()
		newTotal := uint64(vectorID) + 1
		if newTotal <= current {
			break
		}
		if s.total.CompareAndSwap(current, newTotal) {
			break
		}
	}

	return nil
}
