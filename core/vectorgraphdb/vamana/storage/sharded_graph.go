package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// DefaultGraphShardCapacity is the default number of nodes per shard.
// 64K nodes * (2 + 64*4) bytes = ~16.5MB per shard with R=64.
const DefaultGraphShardCapacity = 65536

// ShardedGraphStore provides dynamic-growth graph storage via multiple fixed-size shards.
// Each shard is a separate mmap file that never resizes. Growth adds new shards.
//
// Thread safety:
//   - Concurrent reads are safe (GetNeighbors, GetNeighborCount)
//   - Writes use per-shard locking for thread safety
//   - Shard creation is serialized via shardMu
type ShardedGraphStore struct {
	dir           string
	R             int
	shardCapacity int

	shards  []*graphShard
	shardMu sync.RWMutex

	count atomic.Uint64
}

// graphShard wraps a single GraphStore shard with its own write lock.
type graphShard struct {
	store *GraphStore
	mu    sync.RWMutex
}

// OpenShardedGraphStore opens an existing sharded graph store from the given directory.
// Returns an error if the directory doesn't exist or contains no valid shards.
func OpenShardedGraphStore(dir string) (*ShardedGraphStore, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("sharded graph: open dir: %w", err)
	}

	var shards []*graphShard
	var R, shardCapacity int
	var maxCount uint64

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".bin" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		store, err := OpenGraphStore(path)
		if err != nil {
			for _, s := range shards {
				s.store.Close()
			}
			return nil, fmt.Errorf("sharded graph: open shard %s: %w", entry.Name(), err)
		}

		if R == 0 {
			R = store.R
			shardCapacity = int(store.Capacity())
		}

		shards = append(shards, &graphShard{store: store})

		shardIdx := len(shards) - 1
		shardMaxID := uint64(shardIdx)*uint64(shardCapacity) + store.Count()
		if shardMaxID > maxCount {
			maxCount = shardMaxID
		}
	}

	if len(shards) == 0 {
		return nil, fmt.Errorf("sharded graph: no shards found in %s", dir)
	}

	s := &ShardedGraphStore{
		dir:           dir,
		R:             R,
		shardCapacity: shardCapacity,
		shards:        shards,
	}
	s.count.Store(maxCount)

	return s, nil
}

// CreateShardedGraphStore creates a new sharded graph store in the given directory.
// R is the maximum degree per node. shardCapacity is nodes per shard (0 = default 64K).
func CreateShardedGraphStore(dir string, R int, shardCapacity int) (*ShardedGraphStore, error) {
	if R <= 0 {
		return nil, fmt.Errorf("sharded graph: invalid R=%d", R)
	}

	if shardCapacity <= 0 {
		shardCapacity = DefaultGraphShardCapacity
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("sharded graph: create dir: %w", err)
	}

	s := &ShardedGraphStore{
		dir:           dir,
		R:             R,
		shardCapacity: shardCapacity,
		shards:        make([]*graphShard, 0, 8),
	}

	if err := s.createShard(); err != nil {
		return nil, err
	}

	return s, nil
}

// createShard adds a new shard. Caller must hold shardMu write lock.
func (s *ShardedGraphStore) createShard() error {
	shardIdx := len(s.shards)
	path := filepath.Join(s.dir, fmt.Sprintf("shard_%04d.bin", shardIdx))

	store, err := CreateGraphStore(path, s.R, s.shardCapacity)
	if err != nil {
		return fmt.Errorf("sharded graph: create shard %d: %w", shardIdx, err)
	}

	s.shards = append(s.shards, &graphShard{store: store})
	return nil
}

// ensureShard ensures the shard for the given node ID exists.
func (s *ShardedGraphStore) ensureShard(nodeID uint32) error {
	shardIdx := int(nodeID) / s.shardCapacity

	s.shardMu.RLock()
	if shardIdx < len(s.shards) {
		s.shardMu.RUnlock()
		return nil
	}
	s.shardMu.RUnlock()

	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	// Double-check after acquiring write lock
	for shardIdx >= len(s.shards) {
		if err := s.createShard(); err != nil {
			return err
		}
	}

	return nil
}

// getShard returns the shard and local ID for the given global node ID.
func (s *ShardedGraphStore) getShard(nodeID uint32) (*graphShard, uint32) {
	shardIdx := int(nodeID) / s.shardCapacity
	localID := uint32(int(nodeID) % s.shardCapacity)

	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	if shardIdx >= len(s.shards) {
		return nil, 0
	}

	return s.shards[shardIdx], localID
}

// GetNeighbors returns the neighbor list for the given node.
// Returns nil if the node doesn't exist or has no neighbors.
func (s *ShardedGraphStore) GetNeighbors(nodeID uint32) []uint32 {
	shard, localID := s.getShard(nodeID)
	if shard == nil {
		return nil
	}

	shard.mu.RLock()
	defer shard.mu.RUnlock()

	return shard.store.GetNeighbors(localID)
}

// GetNeighborCount returns the number of neighbors for the given node.
func (s *ShardedGraphStore) GetNeighborCount(nodeID uint32) uint16 {
	shard, localID := s.getShard(nodeID)
	if shard == nil {
		return 0
	}

	shard.mu.RLock()
	defer shard.mu.RUnlock()

	return shard.store.GetNeighborCount(localID)
}

// SetNeighbors sets the neighbor list for the given node.
// Automatically creates new shards as needed.
func (s *ShardedGraphStore) SetNeighbors(nodeID uint32, neighbors []uint32) error {
	if err := s.ensureShard(nodeID); err != nil {
		return err
	}

	shard, localID := s.getShard(nodeID)
	if shard == nil {
		return fmt.Errorf("sharded graph: shard not found for node %d", nodeID)
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()

	if err := shard.store.SetNeighbors(localID, neighbors); err != nil {
		return err
	}

	for {
		current := s.count.Load()
		newCount := uint64(nodeID) + 1
		if newCount <= current {
			break
		}
		if s.count.CompareAndSwap(current, newCount) {
			break
		}
	}

	return nil
}

// AddNeighbor appends a neighbor if not present and list not full.
// Automatically creates new shards as needed. Returns true if added.
func (s *ShardedGraphStore) AddNeighbor(nodeID, neighborID uint32) bool {
	if err := s.ensureShard(nodeID); err != nil {
		return false
	}

	shard, localID := s.getShard(nodeID)
	if shard == nil {
		return false
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()

	return shard.store.AddNeighbor(localID, neighborID)
}

// Count returns the total number of nodes across all shards.
func (s *ShardedGraphStore) Count() uint64 {
	return s.count.Load()
}

// Capacity returns the current total capacity across all shards.
// This grows dynamically as new shards are added.
func (s *ShardedGraphStore) Capacity() uint64 {
	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	return uint64(len(s.shards)) * uint64(s.shardCapacity)
}

// MaxDegree returns the maximum number of neighbors per node (R).
func (s *ShardedGraphStore) MaxDegree() int {
	return s.R
}

// ShardCount returns the number of shards.
func (s *ShardedGraphStore) ShardCount() int {
	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	return len(s.shards)
}

// Sync flushes all shards to disk.
func (s *ShardedGraphStore) Sync() error {
	s.shardMu.RLock()
	defer s.shardMu.RUnlock()

	for i, shard := range s.shards {
		shard.mu.RLock()
		err := shard.store.Sync()
		shard.mu.RUnlock()

		if err != nil {
			return fmt.Errorf("sharded graph: sync shard %d: %w", i, err)
		}
	}

	return nil
}

// Close closes all shards.
func (s *ShardedGraphStore) Close() error {
	s.shardMu.Lock()
	defer s.shardMu.Unlock()

	var firstErr error
	for i, shard := range s.shards {
		shard.mu.Lock()
		err := shard.store.Close()
		shard.mu.Unlock()

		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sharded graph: close shard %d: %w", i, err)
		}
	}

	s.shards = nil
	return firstErr
}
