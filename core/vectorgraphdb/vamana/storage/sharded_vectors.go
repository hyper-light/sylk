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

	shards  atomic.Value // []*vectorShard - atomic for lock-free reads
	shardMu sync.Mutex   // Only for shard creation

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
	}
	s.shards.Store(shards)
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
	}
	s.shards.Store(make([]*vectorShard, 0, 8))

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

func (s *ShardedVectorStore) loadShards() []*vectorShard {
	return s.shards.Load().([]*vectorShard)
}

func (s *ShardedVectorStore) createShard() error {
	shards := s.loadShards()
	shardIdx := len(shards)
	path := filepath.Join(s.dir, fmt.Sprintf("shard_%04d.bin", shardIdx))

	store, err := CreateVectorStore(path, s.dim, s.shardCapacity)
	if err != nil {
		return fmt.Errorf("sharded vectors: create shard %d: %w", shardIdx, err)
	}

	newShards := make([]*vectorShard, len(shards)+1)
	copy(newShards, shards)
	newShards[shardIdx] = &vectorShard{store: store}
	s.shards.Store(newShards)
	return nil
}

func (s *ShardedVectorStore) ensureShard(vectorID uint32) error {
	var shardIdx int
	if s.useBitOps {
		shardIdx = int(vectorID >> shardShift)
	} else {
		shardIdx = int(vectorID) / s.shardCapacity
	}

	if shardIdx < len(s.loadShards()) {
		return nil
	}

	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	for shardIdx >= len(s.loadShards()) {
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

	shards := s.loadShards()
	if shardIdx >= len(shards) {
		return nil, 0
	}

	return shards[shardIdx], localID
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

	for shardIdx >= len(s.loadShards()) {
		if err := s.createShard(); err != nil {
			return 0, err
		}
	}

	currentShard := s.loadShards()[shardIdx]
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

		for shardIdx >= len(s.loadShards()) {
			if err := s.createShard(); err != nil {
				return 0, err
			}
		}

		take := min(space, len(remaining))
		batch := remaining[:take]
		remaining = remaining[take:]

		currentShard := s.loadShards()[shardIdx]
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
	return uint64(len(s.loadShards())) * uint64(s.shardCapacity)
}

func (s *ShardedVectorStore) Dimension() int {
	return s.dim
}

func (s *ShardedVectorStore) ShardCount() int {
	return len(s.loadShards())
}

type ShardedVectorSnapshot struct {
	shards        []*VectorSnapshot
	shardCapacity int
	useBitOps     bool
	count         uint64
	dim           int
}

func (s *ShardedVectorStore) Snapshot() *ShardedVectorSnapshot {
	shards := s.loadShards()
	snapshots := make([]*VectorSnapshot, len(shards))
	for i, shard := range shards {
		snapshots[i] = shard.store.Snapshot()
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
	shards := s.loadShards()
	for i, shard := range shards {
		if err := shard.store.Sync(); err != nil {
			return fmt.Errorf("sharded vectors: sync shard %d: %w", i, err)
		}
	}

	return s.saveMeta()
}

func (s *ShardedVectorStore) Close() error {
	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	shards := s.loadShards()
	var firstErr error
	for i, shard := range shards {
		shard.mu.Lock()
		err := shard.store.Close()
		shard.mu.Unlock()

		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sharded vectors: close shard %d: %w", i, err)
		}
	}

	s.shards.Store([]*vectorShard(nil))
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
