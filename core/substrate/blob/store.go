package blob

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Store is the substrate's content-addressed blob store.
//
// The store is sharded by the first byte of the hash to avoid global
// mutex contention under concurrent Put/Get from many pipelines. Each
// shard owns its own RWMutex; lookups under hash collision (rare) take
// the read lock; mutations take the write lock.
//
// All operations are safe for concurrent use.
//
// Refcount semantics:
//   - Put(data) stores the bytes if absent and increments the refcount.
//     Putting the same content twice yields the same Hash and a refcount
//     of 2.
//   - Acquire(hash) increments the refcount of an existing blob. View
//     attachments call Acquire when a manifest is mounted onto a view.
//   - Release(hash) decrements. Refcount reaching zero marks the blob as
//     eligible for deterministic drop.
//
// In Phase 1 the store keeps refcount-zero blobs in memory until an
// explicit Compact call. Phase 7 (tiered cache) replaces this with
// S3-FIFO-driven demotion across Hot/Warm/Cold tiers.
type Store struct {
	shards   []*shard
	shardsZ  uint8 // shard count - 1, used as mask (only valid because count is power of 2)
}

type shard struct {
	mu      sync.RWMutex
	entries map[Hash]*entry
}

type entry struct {
	data     []byte
	refcount atomic.Int64
}

// Option configures a Store at construction.
type Option func(*storeConfig)

type storeConfig struct {
	shardCount int
}

// WithShardCount overrides the auto-derived shard count. Pass a power of
// two; non-power-of-two values are rounded up. Primarily for tests that
// want deterministic shard distribution.
func WithShardCount(n int) Option {
	return func(c *storeConfig) {
		c.shardCount = roundUpPow2(n)
	}
}

// New constructs a blob Store.
func New(opts ...Option) *Store {
	cfg := &storeConfig{shardCount: defaultShardCount()}
	for _, opt := range opts {
		opt(cfg)
	}
	return newWithShardCount(cfg.shardCount)
}

func newWithShardCount(count int) *Store {
	shards := make([]*shard, count)
	for i := range shards {
		shards[i] = &shard{entries: make(map[Hash]*entry)}
	}
	return &Store{shards: shards, shardsZ: uint8(count - 1)}
}

func (s *Store) shardFor(hash Hash) *shard {
	return s.shards[hash[0]&s.shardsZ]
}

// Put stores data and returns its Hash with refcount incremented by one.
// Identical content already in the store is deduplicated (no copy is
// made beyond the one stored on first insert) and its refcount is
// incremented.
//
// The data slice must not be mutated by the caller after Put returns;
// the store retains a reference for as long as the blob is live. Callers
// that may mutate their input should pass a copy.
func (s *Store) Put(data []byte) (Hash, error) {
	if data == nil {
		return Hash{}, ErrNilData
	}
	hash := Sum(data)
	sh := s.shardFor(hash)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if existing, ok := sh.entries[hash]; ok {
		existing.refcount.Add(1)
		return hash, nil
	}
	e := &entry{data: data}
	e.refcount.Store(1)
	sh.entries[hash] = e
	return hash, nil
}

// Get returns a reader over the blob's bytes. Returns ErrNotFound when
// the blob is not in the store.
//
// The returned ReadCloser reads from the underlying immutable bytes
// without copying. Close is a no-op; it exists to satisfy the
// io.ReadCloser contract used by the FUSE projection.
func (s *Store) Get(hash Hash) (io.ReadCloser, error) {
	sh := s.shardFor(hash)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, ok := sh.entries[hash]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	return readerOver(e.data), nil
}

// Bytes returns a read-only view of the blob's bytes. Equivalent to
// Get + io.ReadAll without the intermediate ReadCloser, for callers that
// know they want the full byte slice. The returned slice must not be
// mutated by the caller.
//
// Returns ErrNotFound when the blob is not in the store.
func (s *Store) Bytes(hash Hash) ([]byte, error) {
	sh := s.shardFor(hash)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, ok := sh.entries[hash]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	return e.data, nil
}

// Has reports whether the blob is in the store, regardless of refcount.
func (s *Store) Has(hash Hash) bool {
	sh := s.shardFor(hash)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	_, ok := sh.entries[hash]
	return ok
}

// Acquire increments the refcount of an existing blob.
// Returns ErrNotFound when the blob is not in the store.
func (s *Store) Acquire(hash Hash) error {
	sh := s.shardFor(hash)
	sh.mu.RLock()
	e, ok := sh.entries[hash]
	sh.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	e.refcount.Add(1)
	return nil
}

// Release decrements the refcount of an existing blob.
// Returns ErrNotFound when the blob is not in the store.
// Returns ErrRefcountUnderflow when the refcount would go below zero.
func (s *Store) Release(hash Hash) error {
	sh := s.shardFor(hash)
	sh.mu.RLock()
	e, ok := sh.entries[hash]
	sh.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	return decrementRefcount(e)
}

func decrementRefcount(e *entry) error {
	for {
		current := e.refcount.Load()
		if current <= 0 {
			return ErrRefcountUnderflow
		}
		if e.refcount.CompareAndSwap(current, current-1) {
			return nil
		}
	}
}

// Refcount returns the current refcount of the blob, or 0 if the blob
// is not present.
func (s *Store) Refcount(hash Hash) int64 {
	sh := s.shardFor(hash)
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	e, ok := sh.entries[hash]
	if !ok {
		return 0
	}
	return e.refcount.Load()
}

// Stats summarizes the store's contents at a point in time.
type Stats struct {
	// BlobCount is the number of distinct blobs in the store, regardless
	// of refcount.
	BlobCount int64
	// UniqueBytes is the sum of blob sizes — what the store actually
	// pays for in memory.
	UniqueBytes int64
	// ReferencedBytes is the sum of (blob size × refcount) across all
	// blobs — what callers would pay if there were no deduplication.
	// DedupRatio = ReferencedBytes / UniqueBytes.
	ReferencedBytes int64
	// ShardCount is the number of shards in the store, fixed at
	// construction time.
	ShardCount int
}

// Stat takes a snapshot of the store's contents.
func (s *Store) Stat() Stats {
	out := Stats{ShardCount: len(s.shards)}
	for _, sh := range s.shards {
		accumulateShardStats(sh, &out)
	}
	return out
}

func accumulateShardStats(sh *shard, out *Stats) {
	sh.mu.RLock()
	defer sh.mu.RUnlock()
	for _, e := range sh.entries {
		out.BlobCount++
		size := int64(len(e.data))
		out.UniqueBytes += size
		out.ReferencedBytes += size * e.refcount.Load()
	}
}

// Compact removes all blobs whose refcount has dropped to zero. Returns
// the number of blobs removed. Phase 7 replaces this with S3-FIFO
// driven tiering; in Phase 1 it is the only mechanism for reclaiming
// memory from refcount-zero blobs.
func (s *Store) Compact() int {
	removed := 0
	for _, sh := range s.shards {
		removed += compactShard(sh)
	}
	return removed
}

func compactShard(sh *shard) int {
	sh.mu.Lock()
	defer sh.mu.Unlock()
	removed := 0
	for hash, e := range sh.entries {
		if e.refcount.Load() <= 0 {
			delete(sh.entries, hash)
			removed++
		}
	}
	return removed
}

// readerOver wraps a []byte as an io.ReadCloser whose Close is a no-op.
// The reader sees an immutable view; the underlying bytes belong to the
// store and must not be modified.
func readerOver(data []byte) io.ReadCloser {
	return &noopCloser{Reader: bytes.NewReader(data)}
}

type noopCloser struct {
	*bytes.Reader
}

func (n *noopCloser) Close() error { return nil }

// Errors.
var (
	ErrNilData           = errors.New("blob: nil data")
	ErrNotFound          = errors.New("blob: not found")
	ErrRefcountUnderflow = errors.New("blob: refcount would go below zero")
)
