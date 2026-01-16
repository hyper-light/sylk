package guide

import (
	"hash/fnv"
	"sync"
)

// =============================================================================
// Sharded Map
// =============================================================================
//
// ShardedMap provides high-performance concurrent map access by splitting
// the map into N shards, each with its own lock. This dramatically reduces
// lock contention compared to a single mutex or sync.Map.
//
// Performance characteristics:
// - Read:  O(1) average, locks only one shard
// - Write: O(1) average, locks only one shard
// - Iteration: O(n), locks all shards sequentially
//
// Shard count should be a power of 2 for optimal hash distribution.

const (
	// DefaultShardCount is the default number of shards (must be power of 2)
	DefaultShardCount = 64
)

// ShardedMap is a concurrent map split into shards for reduced lock contention
type ShardedMap[K comparable, V any] struct {
	shards    []*mapShard[K, V]
	shardMask uint64
	hashFunc  func(K) uint64
}

// mapShard is a single shard of the map
type mapShard[K comparable, V any] struct {
	mu    sync.RWMutex
	items map[K]V
}

// NewShardedMap creates a new sharded map with the specified shard count.
// shardCount must be a power of 2; if not, it will be rounded up.
func NewShardedMap[K comparable, V any](shardCount int, hashFunc func(K) uint64) *ShardedMap[K, V] {
	if shardCount <= 0 {
		shardCount = DefaultShardCount
	}

	// Round up to power of 2
	shardCount = nextPowerOf2(shardCount)

	shards := make([]*mapShard[K, V], shardCount)
	for i := range shards {
		shards[i] = &mapShard[K, V]{
			items: make(map[K]V),
		}
	}

	return &ShardedMap[K, V]{
		shards:    shards,
		shardMask: uint64(shardCount - 1),
		hashFunc:  hashFunc,
	}
}

// getShard returns the shard for a given key
func (m *ShardedMap[K, V]) getShard(key K) *mapShard[K, V] {
	hash := m.hashFunc(key)
	return m.shards[hash&m.shardMask]
}

// Get retrieves a value from the map
func (m *ShardedMap[K, V]) Get(key K) (V, bool) {
	shard := m.getShard(key)
	shard.mu.RLock()
	val, ok := shard.items[key]
	shard.mu.RUnlock()
	return val, ok
}

// Set stores a value in the map
func (m *ShardedMap[K, V]) Set(key K, value V) {
	shard := m.getShard(key)
	shard.mu.Lock()
	shard.items[key] = value
	shard.mu.Unlock()
}

// Delete removes a value from the map
func (m *ShardedMap[K, V]) Delete(key K) {
	shard := m.getShard(key)
	shard.mu.Lock()
	delete(shard.items, key)
	shard.mu.Unlock()
}

// GetOrSet returns the existing value for a key, or sets and returns the new value
func (m *ShardedMap[K, V]) GetOrSet(key K, value V) (V, bool) {
	shard := m.getShard(key)
	shard.mu.Lock()
	existing, ok := shard.items[key]
	if ok {
		shard.mu.Unlock()
		return existing, true
	}
	shard.items[key] = value
	shard.mu.Unlock()
	return value, false
}

// SetIf sets the value only if the condition function returns true.
// The condition is evaluated under the write lock.
func (m *ShardedMap[K, V]) SetIf(key K, value V, condition func(existing V, exists bool) bool) bool {
	shard := m.getShard(key)
	shard.mu.Lock()
	existing, exists := shard.items[key]
	if condition(existing, exists) {
		shard.items[key] = value
		shard.mu.Unlock()
		return true
	}
	shard.mu.Unlock()
	return false
}

// Update atomically updates a value using the provided function.
// Returns the new value and whether the key existed.
func (m *ShardedMap[K, V]) Update(key K, updateFunc func(existing V, exists bool) V) V {
	shard := m.getShard(key)
	shard.mu.Lock()
	existing, exists := shard.items[key]
	newVal := updateFunc(existing, exists)
	shard.items[key] = newVal
	shard.mu.Unlock()
	return newVal
}

// DeleteIf deletes a key if the condition function returns true.
// Returns the deleted value and whether it was deleted.
func (m *ShardedMap[K, V]) DeleteIf(key K, condition func(V) bool) (V, bool) {
	shard := m.getShard(key)
	shard.mu.Lock()
	val, ok := shard.items[key]
	if ok && condition(val) {
		delete(shard.items, key)
		shard.mu.Unlock()
		return val, true
	}
	shard.mu.Unlock()
	var zero V
	return zero, false
}

// Has returns true if the key exists in the map
func (m *ShardedMap[K, V]) Has(key K) bool {
	shard := m.getShard(key)
	shard.mu.RLock()
	_, ok := shard.items[key]
	shard.mu.RUnlock()
	return ok
}

// Len returns the total number of items across all shards
func (m *ShardedMap[K, V]) Len() int {
	total := 0
	for _, shard := range m.shards {
		shard.mu.RLock()
		total += len(shard.items)
		shard.mu.RUnlock()
	}
	return total
}

// Keys returns all keys in the map
func (m *ShardedMap[K, V]) Keys() []K {
	keys := make([]K, 0)
	for _, shard := range m.shards {
		shard.mu.RLock()
		for k := range shard.items {
			keys = append(keys, k)
		}
		shard.mu.RUnlock()
	}
	return keys
}

// Values returns all values in the map
func (m *ShardedMap[K, V]) Values() []V {
	values := make([]V, 0)
	for _, shard := range m.shards {
		shard.mu.RLock()
		for _, v := range shard.items {
			values = append(values, v)
		}
		shard.mu.RUnlock()
	}
	return values
}

// Range iterates over all key-value pairs.
// If the callback returns false, iteration stops.
// Note: Locks shards sequentially, not atomically across all shards.
func (m *ShardedMap[K, V]) Range(fn func(key K, value V) bool) {
	for _, shard := range m.shards {
		shard.mu.RLock()
		for k, v := range shard.items {
			if !fn(k, v) {
				shard.mu.RUnlock()
				return
			}
		}
		shard.mu.RUnlock()
	}
}

// RangeWithFilter iterates over key-value pairs that match the filter.
// More efficient than Range + filter when you need to skip many entries.
func (m *ShardedMap[K, V]) RangeWithFilter(filter func(key K, value V) bool, fn func(key K, value V) bool) {
	for _, shard := range m.shards {
		shard.mu.RLock()
		for k, v := range shard.items {
			if filter(k, v) {
				if !fn(k, v) {
					shard.mu.RUnlock()
					return
				}
			}
		}
		shard.mu.RUnlock()
	}
}

// Clear removes all items from the map
func (m *ShardedMap[K, V]) Clear() {
	for _, shard := range m.shards {
		shard.mu.Lock()
		shard.items = make(map[K]V)
		shard.mu.Unlock()
	}
}

// Snapshot returns a copy of all items in the map.
// This is an atomic snapshot of each shard, but not atomic across shards.
func (m *ShardedMap[K, V]) Snapshot() map[K]V {
	result := make(map[K]V)
	for _, shard := range m.shards {
		shard.mu.RLock()
		for k, v := range shard.items {
			result[k] = v
		}
		shard.mu.RUnlock()
	}
	return result
}

// =============================================================================
// Hash Functions
// =============================================================================

// HashString returns a hash function for string keys
func HashString(key string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return h.Sum64()
}

// HashInt64 returns a hash function for int64 keys
func HashInt64(key int64) uint64 {
	// FNV-1a for int64
	const prime = 1099511628211
	const offset = 14695981039346656037
	h := uint64(offset)
	for i := 0; i < 8; i++ {
		h ^= uint64(byte(key >> (i * 8)))
		h *= prime
	}
	return h
}

// HashUint64 returns a hash function for uint64 keys
func HashUint64(key uint64) uint64 {
	// FNV-1a for uint64
	const prime = 1099511628211
	const offset = 14695981039346656037
	h := uint64(offset)
	for i := 0; i < 8; i++ {
		h ^= uint64(byte(key >> (i * 8)))
		h *= prime
	}
	return h
}

// =============================================================================
// Helpers
// =============================================================================

// nextPowerOf2 returns the smallest power of 2 >= n
func nextPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	return n + 1
}

// =============================================================================
// Specialized Sharded Maps
// =============================================================================

// NewStringMap creates a sharded map with string keys
func NewStringMap[V any](shardCount int) *ShardedMap[string, V] {
	return NewShardedMap[string, V](shardCount, HashString)
}

// NewAgentMap creates a sharded map for agent announcements (keyed by agent ID)
func NewAgentMap(shardCount int) *ShardedMap[string, *AgentAnnouncement] {
	return NewStringMap[*AgentAnnouncement](shardCount)
}

// NewPendingMap creates a sharded map for pending requests
func NewPendingMap[V any](shardCount int) *ShardedMap[string, V] {
	return NewStringMap[V](shardCount)
}
