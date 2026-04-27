package knowledgeruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	bolt "go.etcd.io/bbolt"
)

// committedMetaStore persists path → committedPathMeta on disk so the
// derived enrichment index does not need to be held entirely in heap.
// The query path is `Lookup(path) → committedPathMeta`; the cold-path
// reads from bbolt (mmap, OS page-cached), and a small LRU on top
// serves repeated lookups in the hot working set without going to the
// kernel.
//
// Memory shape:
//   - bbolt: lives in OS page cache, not Go heap. Working set bounded
//     by access pattern, not by total path count.
//   - LRU: bounded; size derived from observed working-set anchor
//     (see committedMetaCacheSize) rather than picked.
//
// Lifecycle:
//   - Built once per refresh by buildCommittedMetadataIndex. Refresh
//     opens a new store, populates it, swaps it into the live state,
//     and closes the previous store.
//   - The on-disk file is rewritten in full on every refresh — the
//     index is a pure derivation of the underlying nodes/edges and
//     has no incremental update path here (Phase 4 territory).
type committedMetaStore struct {
	mu     sync.RWMutex
	closed bool

	dbPath string
	db     *bolt.DB
	cache  *lru.Cache[string, *committedPathMeta]
}

// sharedCommittedMetaStores is the process-wide registry guaranteeing
// one *committedMetaStore per canonical dbPath. bbolt holds an
// exclusive flock on the file; without sharing, two callers — for
// example, two committedKnowledgeState instances during a refresh
// transition — would deadlock on the second open.
var (
	sharedCommittedMetaStoresMu sync.Mutex
	sharedCommittedMetaStores   = make(map[string]*sharedCommittedMetaStore)
)

type sharedCommittedMetaStore struct {
	store    *committedMetaStore
	refCount int
}

// committedMetaBucket is the single bbolt bucket all entries live in.
// Single-character name keeps internal B+tree page overhead minimal
// at scale (matches the EdgeShardStore convention).
var committedMetaBucket = []byte("p")

// committedMetaCacheSize bounds the LRU. Anchored to the typical
// query fan-out of a single agent search request times concurrent
// agents in flight — derived rather than picked.
//
// Empirical anchor: a typical search request returns ~10–50 hits,
// each enriching a distinct path. With up to ~14 active agents
// concurrently querying, peak in-flight unique paths is ~14 × 50 ≈
// 700. Doubling for headroom (different recent queries still warm)
// yields ~1500. Round to a power of two: 2048.
//
// At ~500 bytes per cached committedPathMeta the cache caps at
// ~1 MiB resident — the bound that lets us drop tens-to-hundreds of
// MiB of in-heap byPath at scale.
const committedMetaCacheSize = 2048

// committedMetaPersistTimeout bounds how long we wait for an exclusive
// lock on the bolt file. Same shape as boltOpenTimeout in sylkdir;
// avoids an infinite hang on a stale lock from a crashed peer process.
const committedMetaPersistTimeout = 5 * time.Second

// newCommittedMetaStore returns the process-wide *committedMetaStore
// for dbPath, sharing the bolt handle and LRU among callers that
// resolve to the same file. Refcounted via sharedCommittedMetaStores
// so Close releases the file lock only when the last caller departs.
//
// PersistAll on a shared store replaces the bucket atomically inside
// a bolt write transaction; concurrent readers either see the prior
// snapshot (if their View tx began before the swap commits) or the
// new one — never partial state.
func newCommittedMetaStore(dbPath string) (*committedMetaStore, error) {
	canonical := canonicalCommittedMetaPath(dbPath)

	sharedCommittedMetaStoresMu.Lock()
	defer sharedCommittedMetaStoresMu.Unlock()

	if entry, ok := sharedCommittedMetaStores[canonical]; ok {
		entry.refCount++
		return entry.store, nil
	}

	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		return nil, fmt.Errorf("committed meta: mkdir: %w", err)
	}
	db, err := bolt.Open(canonical, 0o644, &bolt.Options{Timeout: committedMetaPersistTimeout})
	if err != nil {
		return nil, fmt.Errorf("committed meta: open bolt: %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(committedMetaBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("committed meta: create bucket: %w", err)
	}

	cache, err := lru.New[string, *committedPathMeta](committedMetaCacheSize)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("committed meta: lru: %w", err)
	}

	store := &committedMetaStore{
		dbPath: canonical,
		db:     db,
		cache:  cache,
	}
	sharedCommittedMetaStores[canonical] = &sharedCommittedMetaStore{store: store, refCount: 1}
	return store, nil
}

// canonicalCommittedMetaPath returns a stable absolute symlink-resolved
// key for sharedCommittedMetaStores. Resolves symlinks on the parent
// directory (which always exists across the file's lifecycle) and
// joins the basename — never EvalSymlinks the file itself, since the
// file's existence flips between calls (created by the first
// PersistAll) and EvalSymlinks errors on missing paths, which would
// produce different canonical keys before vs. after the file exists
// and break the singleton.
func canonicalCommittedMetaPath(dbPath string) string {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return dbPath
	}
	dir, base := filepath.Split(abs)
	if dir == "" {
		return abs
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return filepath.Join(resolved, base)
	}
	return abs
}

// Close decrements the singleton refcount. The bolt handle and LRU
// are released only when the last caller departs.
func (s *committedMetaStore) Close() error {
	if s == nil {
		return nil
	}
	sharedCommittedMetaStoresMu.Lock()
	entry, ok := sharedCommittedMetaStores[s.dbPath]
	if !ok {
		sharedCommittedMetaStoresMu.Unlock()
		return nil
	}
	entry.refCount--
	if entry.refCount > 0 {
		sharedCommittedMetaStoresMu.Unlock()
		return nil
	}
	delete(sharedCommittedMetaStores, s.dbPath)
	sharedCommittedMetaStoresMu.Unlock()

	return s.shutdown()
}

// shutdown closes the bolt handle and clears the cache. Called when
// the last shared reference departs.
func (s *committedMetaStore) shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cache = nil
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// Lookup returns the path meta for the given path, or (nil, false) if
// no entry exists. Hot path: LRU first, then bbolt mmap-backed Get.
//
// On cold-cache first touch a Get pays one bbolt page-fault read
// (~100µs–1ms). Subsequent lookups for the same path are sub-µs.
func (s *committedMetaStore) Lookup(path string) (*committedPathMeta, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	if s.closed || s.db == nil {
		s.mu.RUnlock()
		return nil, false
	}
	if cached, ok := s.cache.Get(path); ok {
		s.mu.RUnlock()
		return cached, true
	}
	db := s.db
	s.mu.RUnlock()

	var raw []byte
	if err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(committedMetaBucket)
		if b == nil {
			return nil
		}
		val := b.Get([]byte(path))
		if val == nil {
			return nil
		}
		// bbolt mmap'd value is only valid within the tx; copy out.
		raw = make([]byte, len(val))
		copy(raw, val)
		return nil
	}); err != nil {
		return nil, false
	}
	if raw == nil {
		return nil, false
	}

	meta, err := decodeCommittedPathMeta(raw)
	if err != nil {
		return nil, false
	}

	s.mu.RLock()
	if !s.closed && s.cache != nil {
		s.cache.Add(path, meta)
	}
	s.mu.RUnlock()
	return meta, true
}

// PersistAll replaces the entire bbolt bucket atomically with the
// given path → meta entries. Called once per refresh to swap the
// derived index into durable storage.
//
// All pathMeta.finalize() calls must have run before this — the
// stored representation is the post-finalize sorted-slices form;
// the build-time set fields are not serialized.
func (s *committedMetaStore) PersistAll(byPath map[string]*committedPathMeta) error {
	if s == nil {
		return fmt.Errorf("committed meta: store is nil")
	}
	s.mu.Lock()
	if s.closed || s.db == nil {
		s.mu.Unlock()
		return fmt.Errorf("committed meta: store is closed")
	}
	db := s.db
	cache := s.cache
	s.mu.Unlock()

	return db.Update(func(tx *bolt.Tx) error {
		// Drop and recreate the bucket — atomic replace within the tx.
		if err := tx.DeleteBucket(committedMetaBucket); err != nil && err != bolt.ErrBucketNotFound {
			return fmt.Errorf("delete prior bucket: %w", err)
		}
		b, err := tx.CreateBucket(committedMetaBucket)
		if err != nil {
			return fmt.Errorf("create bucket: %w", err)
		}

		// Pre-flush LRU so stale entries from a prior version don't
		// bleed into queries against the new state.
		if cache != nil {
			cache.Purge()
		}

		for path, meta := range byPath {
			if path == "" || meta == nil {
				continue
			}
			raw, err := encodeCommittedPathMeta(meta)
			if err != nil {
				return fmt.Errorf("encode %q: %w", path, err)
			}
			if err := b.Put([]byte(path), raw); err != nil {
				return fmt.Errorf("put %q: %w", path, err)
			}
		}
		return nil
	})
}

// committedPathMetaPersist is the on-disk shape — the public slices
// of committedPathMeta minus the build-time dedup sets. Compact JSON
// keys keep the bbolt value bytes small.
type committedPathMetaPersist struct {
	PrimaryNodeID   uint32   `json:"p,omitempty"`
	PrimaryNodeType string   `json:"t,omitempty"`
	Domain          string   `json:"d,omitempty"`
	CanonicalKeys   []string `json:"ck,omitempty"`
	Symbols         []string `json:"sy,omitempty"`
	NodeKinds       []string `json:"nk,omitempty"`
	RelatedPaths    []string `json:"rp,omitempty"`
	RelatedSymbols  []string `json:"rs,omitempty"`
}

func encodeCommittedPathMeta(meta *committedPathMeta) ([]byte, error) {
	persist := committedPathMetaPersist{
		PrimaryNodeID:   meta.PrimaryNodeID,
		PrimaryNodeType: meta.PrimaryNodeType,
		Domain:          meta.Domain,
		CanonicalKeys:   meta.CanonicalKeys,
		Symbols:         meta.Symbols,
		NodeKinds:       meta.NodeKinds,
		RelatedPaths:    meta.RelatedPaths,
		RelatedSymbols:  meta.RelatedSymbols,
	}
	return json.Marshal(persist)
}

func decodeCommittedPathMeta(raw []byte) (*committedPathMeta, error) {
	var persist committedPathMetaPersist
	if err := json.Unmarshal(raw, &persist); err != nil {
		return nil, err
	}
	return &committedPathMeta{
		PrimaryNodeID:   persist.PrimaryNodeID,
		PrimaryNodeType: persist.PrimaryNodeType,
		Domain:          persist.Domain,
		CanonicalKeys:   persist.CanonicalKeys,
		Symbols:         persist.Symbols,
		NodeKinds:       persist.NodeKinds,
		RelatedPaths:    persist.RelatedPaths,
		RelatedSymbols:  persist.RelatedSymbols,
		// dedup sets intentionally nil — only used during build.
	}, nil
}
