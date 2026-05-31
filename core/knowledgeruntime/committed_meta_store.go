package knowledgeruntime

import (
	"encoding/binary"
	"encoding/json"
	"errors"
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
	shared bool
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

// committedMetaBucket holds path → committedPathMeta entries.
// committedMetaNodeBucket holds nodeID(BE-uint32) → path entries —
// the reverse index that lets incremental refresh resolve removed
// nodes back to the path whose metadata they contributed to,
// without scanning every path's NodeIDs list.
// committedMetaInfoBucket holds index-level metadata (last-built
// version, build time, schema marker). Single-character bucket
// names keep the B+tree internal page overhead minimal at scale.
var (
	committedMetaBucket     = []byte("p")
	committedMetaNodeBucket = []byte("n")
	committedMetaInfoBucket = []byte("m")
	infoBuiltVersion        = []byte("built_version")
	infoBuiltTimestamp      = []byte("built_timestamp")
)

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
		for _, name := range [][]byte{committedMetaBucket, committedMetaNodeBucket, committedMetaInfoBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("committed meta: create buckets: %w", err)
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
		shared: true,
	}
	sharedCommittedMetaStores[canonical] = &sharedCommittedMetaStore{store: store, refCount: 1}
	return store, nil
}

func newExistingCommittedMetaStore(dbPath string) (*committedMetaStore, error) {
	canonical := canonicalCommittedMetaPath(dbPath)

	sharedCommittedMetaStoresMu.Lock()
	if entry, ok := sharedCommittedMetaStores[canonical]; ok {
		entry.refCount++
		sharedCommittedMetaStoresMu.Unlock()
		return entry.store, nil
	}
	sharedCommittedMetaStoresMu.Unlock()

	if _, err := os.Stat(canonical); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrCommittedMetadataUnavailable
		}
		return nil, fmt.Errorf("%w: stat committed meta: %v", ErrCommittedMetadataUnavailable, err)
	}
	db, err := bolt.Open(canonical, 0o444, &bolt.Options{
		ReadOnly: true,
		Timeout:  committedMetaPersistTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: open committed meta readonly: %v", ErrCommittedMetadataUnavailable, err)
	}

	cache, err := lru.New[string, *committedPathMeta](committedMetaCacheSize)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("committed meta: lru: %w", err)
	}

	return &committedMetaStore{
		dbPath: canonical,
		db:     db,
		cache:  cache,
	}, nil
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
	if !s.shared {
		return s.shutdown()
	}
	sharedCommittedMetaStoresMu.Lock()
	entry, ok := sharedCommittedMetaStores[s.dbPath]
	if !ok {
		sharedCommittedMetaStoresMu.Unlock()
		return s.shutdown()
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

// BuiltVersion returns the version recorded by the most recent
// successful PersistAll/ApplyDelta, or "" + false if none has been
// recorded.
//
// Callers use this to short-circuit refresh: if the live HEAD equals
// BuiltVersion, the bbolt store already holds the correct derived
// index and no rebuild is needed.
func (s *committedMetaStore) BuiltVersion() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	if s.closed || s.db == nil {
		s.mu.RUnlock()
		return "", false
	}
	db := s.db
	s.mu.RUnlock()

	var version string
	if err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(committedMetaInfoBucket)
		if b == nil {
			return nil
		}
		val := b.Get(infoBuiltVersion)
		if val == nil {
			return nil
		}
		version = string(val)
		return nil
	}); err != nil {
		return "", false
	}
	if version == "" {
		return "", false
	}
	return version, true
}

// BuiltTimestamp returns the wall-clock UnixNano at which the last
// successful build/incremental commit completed, or (0, false) if
// none has been recorded.
//
// Used by incremental refresh to filter "edges added since last
// build" via Edge.CreatedAt.
func (s *committedMetaStore) BuiltTimestamp() (int64, bool) {
	if s == nil {
		return 0, false
	}
	s.mu.RLock()
	if s.closed || s.db == nil {
		s.mu.RUnlock()
		return 0, false
	}
	db := s.db
	s.mu.RUnlock()

	var ts int64
	var found bool
	if err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(committedMetaInfoBucket)
		if b == nil {
			return nil
		}
		val := b.Get(infoBuiltTimestamp)
		if len(val) != 8 {
			return nil
		}
		ts = int64(binary.LittleEndian.Uint64(val))
		found = true
		return nil
	}); err != nil {
		return 0, false
	}
	return ts, found
}

// PathCount returns the number of paths currently persisted. O(1) —
// reads bbolt's KeyN bucket statistic. Used by incremental refresh
// to decide whether the affected-path set is small enough that
// per-path re-aggregation beats a full streaming rebuild.
func (s *committedMetaStore) PathCount() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	if s.closed || s.db == nil {
		s.mu.RUnlock()
		return 0
	}
	db := s.db
	s.mu.RUnlock()

	count := 0
	_ = db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket(committedMetaBucket); b != nil {
			count = b.Stats().KeyN
		}
		return nil
	})
	return count
}

// LookupNodePath returns the path that nodeID was associated with at
// the time of the last build/delta apply. Returns "" + false if the
// node is unknown to this store. Used by incremental refresh to
// resolve removed nodes back to the path whose meta they
// contributed to.
func (s *committedMetaStore) LookupNodePath(nodeID uint32) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	if s.closed || s.db == nil {
		s.mu.RUnlock()
		return "", false
	}
	db := s.db
	s.mu.RUnlock()

	var path string
	if err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(committedMetaNodeBucket)
		if b == nil {
			return nil
		}
		val := b.Get(encodeNodeIDKey(nodeID))
		if val == nil {
			return nil
		}
		path = string(val)
		return nil
	}); err != nil {
		return "", false
	}
	if path == "" {
		return "", false
	}
	return path, true
}

// PersistAll replaces the entire derived index atomically with the
// given path → meta entries and records the built version. Called
// on full rebuild paths (cold start, divergent history, oversized
// delta).
//
// All pathMeta.finalize() calls must have run before this — the
// stored representation is the post-finalize sorted-slices form;
// the build-time set fields are not serialized.
//
// Inside the single bolt write transaction:
//   - bucket "p" is dropped and recreated, populated with the new
//     path → committedPathMeta entries
//   - bucket "n" is dropped and recreated, populated with the
//     reverse nodeID → path entries derived from each meta.NodeIDs
//   - bucket "m" records built_version + built_timestamp
//
// Concurrent readers in their own View tx see either the prior
// snapshot or the new one — never partial state.
func (s *committedMetaStore) PersistAll(byPath map[string]*committedPathMeta, builtVersion string) error {
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

	now := time.Now().UnixNano()
	return db.Update(func(tx *bolt.Tx) error {
		// Replace path bucket atomically.
		if err := tx.DeleteBucket(committedMetaBucket); err != nil && err != bolt.ErrBucketNotFound {
			return fmt.Errorf("delete path bucket: %w", err)
		}
		paths, err := tx.CreateBucket(committedMetaBucket)
		if err != nil {
			return fmt.Errorf("create path bucket: %w", err)
		}

		// Replace node-id reverse bucket atomically.
		if err := tx.DeleteBucket(committedMetaNodeBucket); err != nil && err != bolt.ErrBucketNotFound {
			return fmt.Errorf("delete node bucket: %w", err)
		}
		nodes, err := tx.CreateBucket(committedMetaNodeBucket)
		if err != nil {
			return fmt.Errorf("create node bucket: %w", err)
		}

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
			if err := paths.Put([]byte(path), raw); err != nil {
				return fmt.Errorf("put path %q: %w", path, err)
			}
			pathBytes := []byte(path)
			for _, id := range meta.NodeIDs {
				if err := nodes.Put(encodeNodeIDKey(id), pathBytes); err != nil {
					return fmt.Errorf("put node %d→%q: %w", id, path, err)
				}
			}
		}

		return writeBuiltStamp(tx, builtVersion, now)
	})
}

// PathDelta describes one path-scoped change to apply atomically.
// Set Meta to a non-nil committedPathMeta to insert/replace the
// path's entry; leave Meta nil to delete the path entirely. NodeIDs
// changes (additions and removals from the n bucket) are derived
// from the difference between the existing on-disk NodeIDs and the
// supplied Meta.NodeIDs.
type PathDelta struct {
	Path string
	Meta *committedPathMeta // nil → delete path
}

// ApplyDelta applies a set of per-path updates and deletions inside
// a single bolt transaction. The reverse n bucket is updated to
// match: added nodeIDs gain entries pointing at their path; removed
// nodeIDs (those present in the old meta but absent from the new)
// have their entries deleted.
//
// builtVersion + a fresh UnixNano timestamp are written at commit
// time so subsequent refreshes can observe the new state. Concurrent
// readers see the prior snapshot until commit; afterwards, all reads
// see the post-delta state.
//
// LRU entries for affected paths are evicted (not just purged
// wholesale) so unaffected hot entries stay warm across the delta.
func (s *committedMetaStore) ApplyDelta(deltas []PathDelta, builtVersion string) error {
	if s == nil {
		return fmt.Errorf("committed meta: store is nil")
	}
	if len(deltas) == 0 && builtVersion == "" {
		return nil
	}
	s.mu.Lock()
	if s.closed || s.db == nil {
		s.mu.Unlock()
		return fmt.Errorf("committed meta: store is closed")
	}
	db := s.db
	cache := s.cache
	s.mu.Unlock()

	now := time.Now().UnixNano()
	return db.Update(func(tx *bolt.Tx) error {
		paths := tx.Bucket(committedMetaBucket)
		nodes := tx.Bucket(committedMetaNodeBucket)
		if paths == nil || nodes == nil {
			return fmt.Errorf("missing buckets — apply delta on uninitialized store")
		}

		for _, d := range deltas {
			if d.Path == "" {
				continue
			}
			pathBytes := []byte(d.Path)

			// Read the prior persisted NodeIDs (if any) so we can
			// compute the reverse-bucket diff.
			var priorIDs []uint32
			if existing := paths.Get(pathBytes); existing != nil {
				if prior, err := decodeCommittedPathMeta(existing); err == nil {
					priorIDs = prior.NodeIDs
				}
			}

			if d.Meta == nil {
				// Deletion path.
				if err := paths.Delete(pathBytes); err != nil {
					return fmt.Errorf("delete path %q: %w", d.Path, err)
				}
				for _, id := range priorIDs {
					if err := nodes.Delete(encodeNodeIDKey(id)); err != nil {
						return fmt.Errorf("delete node %d: %w", id, err)
					}
				}
				if cache != nil {
					cache.Remove(d.Path)
				}
				continue
			}

			// Update path.
			raw, err := encodeCommittedPathMeta(d.Meta)
			if err != nil {
				return fmt.Errorf("encode %q: %w", d.Path, err)
			}
			if err := paths.Put(pathBytes, raw); err != nil {
				return fmt.Errorf("put path %q: %w", d.Path, err)
			}

			// Reverse-bucket diff: remove ids that left the set,
			// add ids that joined.
			added, removed := diffSortedNodeIDs(priorIDs, d.Meta.NodeIDs)
			for _, id := range removed {
				if err := nodes.Delete(encodeNodeIDKey(id)); err != nil {
					return fmt.Errorf("delete reverse %d: %w", id, err)
				}
			}
			for _, id := range added {
				if err := nodes.Put(encodeNodeIDKey(id), pathBytes); err != nil {
					return fmt.Errorf("put reverse %d→%q: %w", id, d.Path, err)
				}
			}

			if cache != nil {
				cache.Remove(d.Path) // re-populate on next Lookup
			}
		}

		return writeBuiltStamp(tx, builtVersion, now)
	})
}

// writeBuiltStamp records the version + timestamp into bucket "m".
// builtVersion="" leaves the version untouched (used when callers
// only want to refresh the timestamp; not currently exercised but
// kept for symmetry).
func writeBuiltStamp(tx *bolt.Tx, builtVersion string, now int64) error {
	info, err := tx.CreateBucketIfNotExists(committedMetaInfoBucket)
	if err != nil {
		return fmt.Errorf("ensure info bucket: %w", err)
	}
	if builtVersion != "" {
		if err := info.Put(infoBuiltVersion, []byte(builtVersion)); err != nil {
			return fmt.Errorf("write built_version: %w", err)
		}
	}
	var tsBytes [8]byte
	binary.LittleEndian.PutUint64(tsBytes[:], uint64(now))
	if err := info.Put(infoBuiltTimestamp, tsBytes[:]); err != nil {
		return fmt.Errorf("write built_timestamp: %w", err)
	}
	return nil
}

// diffSortedNodeIDs returns (added, removed) where added = next \ prev
// and removed = prev \ next, computed in O(|prev|+|next|) over sorted
// inputs. Both inputs must be ascending. Returns nil slices when
// empty.
func diffSortedNodeIDs(prev, next []uint32) (added, removed []uint32) {
	i, j := 0, 0
	for i < len(prev) && j < len(next) {
		switch {
		case prev[i] == next[j]:
			i++
			j++
		case prev[i] < next[j]:
			removed = append(removed, prev[i])
			i++
		default:
			added = append(added, next[j])
			j++
		}
	}
	if i < len(prev) {
		removed = append(removed, prev[i:]...)
	}
	if j < len(next) {
		added = append(added, next[j:]...)
	}
	return added, removed
}

// committedPathMetaPersist is the on-disk shape — the public slices
// of committedPathMeta minus the build-time dedup sets. Compact JSON
// keys keep the bbolt value bytes small. NodeIDs is the reverse
// index (path → contributing node IDs) that lets incremental refresh
// re-aggregate a path without scanning the entire node store.
type committedPathMetaPersist struct {
	PrimaryNodeID   uint32   `json:"p,omitempty"`
	PrimaryNodeType string   `json:"t,omitempty"`
	Domain          string   `json:"d,omitempty"`
	CanonicalKeys   []string `json:"ck,omitempty"`
	Symbols         []string `json:"sy,omitempty"`
	NodeKinds       []string `json:"nk,omitempty"`
	RelatedPaths    []string `json:"rp,omitempty"`
	RelatedSymbols  []string `json:"rs,omitempty"`
	NodeIDs         []uint32 `json:"ni,omitempty"`
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
		NodeIDs:         meta.NodeIDs,
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
		NodeIDs:         persist.NodeIDs,
		// dedup sets intentionally nil — only used during build.
	}, nil
}

// encodeNodeIDKey returns 4 BE bytes for a node ID — the bbolt key
// format for the n bucket. Big-endian gives natural numeric order,
// useful for any future range-scan over node IDs.
func encodeNodeIDKey(id uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, id)
	return buf
}
