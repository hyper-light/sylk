package sylkdir

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Edge shard storage constants.
const (
	// DefaultEdgeShardSize is the node ID range per shard.
	DefaultEdgeShardSize = 65536

	// EdgeRecordSize is the fixed size of a binary edge record.
	// Layout: SourceID(4) + TargetID(4) + Type(1) + Weight(4) + SessionID(4) + AgentID(2) + CreatedAt(8) + UpdatedAt(8)
	EdgeRecordSize = 35
)

// ErrEdgeNotFound is returned when an edge doesn't exist.
var ErrEdgeNotFound = errors.New("sylkdir: edge not found")

// Edge represents an edge in the knowledge graph.
type Edge struct {
	// Identity (compound key)
	SourceID uint32 // Source node ID
	TargetID uint32 // Target node ID
	Type     uint8  // Edge type enum

	// Payload
	Weight float32 // Computed weight

	// Provenance
	SessionID uint32 // Session that created/updated
	AgentID   uint16 // Agent that created/updated
	CreatedAt uint64 // Unix nano (immutable)
	UpdatedAt uint64 // Unix nano (mutable)
}

// EdgeKey returns the compound key for this edge.
func (e *Edge) EdgeKey() EdgeKey {
	return EdgeKey{
		SourceID: e.SourceID,
		TargetID: e.TargetID,
		Type:     e.Type,
	}
}

// EdgeKey is the compound key that uniquely identifies an edge.
type EdgeKey struct {
	SourceID uint32
	TargetID uint32
	Type     uint8
}

// MarshalBinary encodes the edge to a fixed-size binary record.
func (e *Edge) MarshalBinary() []byte {
	buf := make([]byte, EdgeRecordSize)
	e.MarshalBinaryTo(buf)
	return buf
}

// MarshalBinaryTo encodes the edge into the provided buffer (must be >= EdgeRecordSize).
func (e *Edge) MarshalBinaryTo(buf []byte) {
	binary.LittleEndian.PutUint32(buf[0:4], e.SourceID)
	binary.LittleEndian.PutUint32(buf[4:8], e.TargetID)
	buf[8] = e.Type
	binary.LittleEndian.PutUint32(buf[9:13], math.Float32bits(e.Weight))
	binary.LittleEndian.PutUint32(buf[13:17], e.SessionID)
	binary.LittleEndian.PutUint16(buf[17:19], e.AgentID)
	binary.LittleEndian.PutUint64(buf[19:27], e.CreatedAt)
	binary.LittleEndian.PutUint64(buf[27:35], e.UpdatedAt)
}

// UnmarshalBinary decodes the edge from a binary record.
func (e *Edge) UnmarshalBinary(data []byte) error {
	if len(data) < EdgeRecordSize {
		return fmt.Errorf("sylkdir: edge data too short: %d < %d", len(data), EdgeRecordSize)
	}

	e.SourceID = binary.LittleEndian.Uint32(data[0:4])
	e.TargetID = binary.LittleEndian.Uint32(data[4:8])
	e.Type = data[8]
	e.Weight = math.Float32frombits(binary.LittleEndian.Uint32(data[9:13]))
	e.SessionID = binary.LittleEndian.Uint32(data[13:17])
	e.AgentID = binary.LittleEndian.Uint16(data[17:19])
	e.CreatedAt = binary.LittleEndian.Uint64(data[19:27])
	e.UpdatedAt = binary.LittleEndian.Uint64(data[27:35])

	return nil
}

// EdgeShardStore manages edge storage in sharded binary files with
// **memory-mapped on-disk indexes** (bbolt B+tree) per shard.
//
// Layout per shard (`shard_NNNN/`):
//   - edges.bin: packed edge records (append-only, fixed-size records)
//   - index.bolt: bbolt file with three buckets:
//       - "k" — EdgeKey (9B) → local offset (8B): exact lookup for dedup/update
//       - "o" — sourceID (4B BE) → packed []int64 local offsets: outgoing list
//       - "i" — targetID (4B BE) → packed []int64 local offsets: incoming list
//       - "m" — meta: "high_water" → uint64 byte-cursor into edges.bin
//
// All stored offsets are LOCAL (within the shard's edges.bin). The shard
// is implicit in the file path; public APIs encode/decode the global
// offset (`shardNum<<48 | localOffset`) at the boundary.
//
// Why bbolt:
//   - mmap → OS page cache, NOT Go heap. At Kubernetes/JDK scale (10⁶–10⁷
//     edges) the previous in-heap maps held GB of resident memory; with
//     bbolt the heap stays small (a few MB of Go-side bookkeeping) and
//     the OS pages indexes in/out as the working set demands.
//   - B+tree → exact lookups in O(log N) page reads. Warm: ~1µs.
//     Cold-page first-touch: ~ms — the unavoidable cost of NOT keeping
//     all data resident; bounded by the working set of any one query.
//   - Single-writer-many-reader matches our existing concurrency model.
//
// Crash recovery: every Write does (append edge to edges.bin) THEN (bbolt
// tx that updates buckets + advances meta.high_water). If a crash lands
// between those, on next Init the high_water cursor lags edges.bin file
// size; we replay from cursor → end and the bbolt indexes catch up. No
// reachable inconsistency window.
type EdgeShardStore struct {
	basePath      string // canonical absolute path; key into sharedEdgeStores
	originalPath  string // raw path the caller passed in (for diagnostics)
	shardSize     uint32

	mu       sync.RWMutex
	shardDBs map[uint32]*bolt.DB // lazily opened, populated by openShardLocked
	closed   bool
}

// sharedEdgeStores guarantees one *EdgeShardStore per canonical basePath
// per process. bbolt holds an exclusive flock on each index.bolt file
// it opens; without sharing, two callers constructing edge stores
// against the same directory would deadlock on the second open. Refcount
// the singleton so Close() does the right thing for callers that
// expected create/use/close ownership semantics.
var (
	sharedEdgeStoresMu sync.Mutex
	sharedEdgeStores   = make(map[string]*sharedEdgeStore)
)

type sharedEdgeStore struct {
	store    *EdgeShardStore
	refCount int
}

// Bbolt bucket identifiers. Single-byte names keep the bucket-index
// pages compact — at 10M edges the difference between 1-char and 8-char
// bucket names is several KB across the B+tree internal pages.
var (
	bucketKeys     = []byte("k")
	bucketOutgoing = []byte("o")
	bucketIncoming = []byte("i")
	bucketMeta     = []byte("m")
	metaHighWater  = []byte("high_water")
)

// boltOpenTimeout bounds how long we wait for an exclusive lock on an
// index.bolt file. With one process per project working tree this should
// always be immediate; the timeout exists so a stuck stale lock from a
// previous crashed sylk surfaces as an explicit error rather than an
// infinite hang.
const boltOpenTimeout = 5 * time.Second

// NewEdgeShardStore returns the process-wide *EdgeShardStore for
// basePath. Multiple callers passing the same logical path receive
// the same singleton with shared bbolt handles; the refcount inside
// sharedEdgeStores tracks ownership so Close() releases the file
// locks only when the last caller departs.
//
// Init() must be called before any Read/Write call to populate shard
// handles. Init() is idempotent — subsequent callers can call it
// safely; it's a no-op when the singleton is already initialised.
func NewEdgeShardStore(basePath string) *EdgeShardStore {
	canonical := canonicalEdgeStorePath(basePath)

	sharedEdgeStoresMu.Lock()
	defer sharedEdgeStoresMu.Unlock()

	if entry, ok := sharedEdgeStores[canonical]; ok {
		entry.refCount++
		return entry.store
	}
	store := &EdgeShardStore{
		basePath:     canonical,
		originalPath: basePath,
		shardSize:    DefaultEdgeShardSize,
		shardDBs:     make(map[uint32]*bolt.DB),
	}
	sharedEdgeStores[canonical] = &sharedEdgeStore{store: store, refCount: 1}
	return store
}

// canonicalEdgeStorePath returns a stable absolute symlink-resolved
// key for sharedEdgeStores. Resolves symlinks on the deepest existing
// ancestor and joins the unresolved tail — never EvalSymlinks the
// directory itself, since EvalSymlinks errors on missing paths and
// would produce different canonical keys before vs. after the
// directory exists, breaking the singleton on first-Init.
func canonicalEdgeStorePath(basePath string) string {
	abs, err := filepath.Abs(basePath)
	if err != nil {
		return basePath
	}
	// Walk up parents until we find one that exists, then EvalSymlinks
	// that and re-join the unresolved tail. This handles the case
	// where basePath is "/tmp/test-XYZ/.sylk/global/edges" and only
	// "/tmp" exists at canonicalization time.
	tail := ""
	dir := abs
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent, base := filepath.Split(dir)
		if parent == "" || parent == dir {
			return abs
		}
		dir = filepath.Clean(parent)
		if tail == "" {
			tail = base
		} else {
			tail = filepath.Join(base, tail)
		}
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		if tail == "" {
			return resolved
		}
		return filepath.Join(resolved, tail)
	}
	return abs
}

// NewEdgeShardStoreFromSylkDir creates an EdgeShardStore from a SylkDir.
func NewEdgeShardStoreFromSylkDir(sd *SylkDir) *EdgeShardStore {
	return NewEdgeShardStore(sd.GlobalEdgeDataPath())
}

// Init opens (and migrates if necessary) every existing shard's bbolt
// index. Cheap at any scale: bbolt files are mmap'd, no heap-resident
// index materialization. The cost of "first lookup after fresh boot"
// is paid lazily as queries fault pages in — same I/O cost as before
// but spread across actual access rather than concentrated at startup.
//
// Idempotent: a second Init() on an already-initialised singleton is
// a no-op (each shard handle is opened exactly once). Safe to call
// from every caller that received the singleton from NewEdgeShardStore.
func (s *EdgeShardStore) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New("sylkdir: edge store is closed")
	}

	if err := os.MkdirAll(s.basePath, 0755); err != nil {
		return fmt.Errorf("sylkdir: create edges directory: %w", err)
	}

	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		return fmt.Errorf("sylkdir: read edges directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		shardNum, ok := parseShardName(entry.Name())
		if !ok {
			continue
		}
		if _, err := s.openShardLocked(shardNum); err != nil {
			return fmt.Errorf("sylkdir: open shard %d: %w", shardNum, err)
		}
	}
	return nil
}

// Write writes an edge to the appropriate shard. Update if the
// EdgeKey already exists; insert otherwise.
func (s *EdgeShardStore) Write(edge *Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("sylkdir: edge store is closed")
	}
	return s.writeEdgeLocked(edge)
}

// writeEdgeLocked is the lock-held write path. It first checks the
// shard for an existing EdgeKey across ALL shards (an update can
// target an edge stored in a different shard than SourceID/shardSize
// would predict, e.g. after WriteBatch with mixed source IDs). For a
// new edge we append to the SourceID-derived shard.
func (s *EdgeShardStore) writeEdgeLocked(edge *Edge) error {
	key := edge.EdgeKey()
	if existingShard, existingOffset, ok, err := s.lookupKeyLocked(key); err != nil {
		return err
	} else if ok {
		// In-place rewrite at known offset; bbolt index unchanged.
		return s.writeEdgeAtOffset(existingShard, existingOffset, edge)
	}

	shardNum := edge.SourceID / s.shardSize
	db, err := s.openShardLocked(shardNum)
	if err != nil {
		return err
	}
	return s.appendEdgeToShard(db, shardNum, edge)
}

// WriteBatch writes multiple edges, batching per-shard work into a
// single bbolt transaction per shard. Updates write in-place; inserts
// append to edges.bin and update bbolt indexes atomically.
func (s *EdgeShardStore) WriteBatch(edges []*Edge) error {
	if len(edges) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("sylkdir: edge store is closed")
	}

	// Bucket new vs. update. For updates we need to know which shard
	// the existing edge lives in (since Source-derived shard may differ
	// after history). For inserts the destination is SourceID/shardSize.
	type pending struct {
		edge   *Edge
		offset int64 // for updates only
	}
	updatesByShard := make(map[uint32][]pending)
	insertsByShard := make(map[uint32][]*Edge)

	for _, edge := range edges {
		key := edge.EdgeKey()
		existingShard, existingOffset, ok, err := s.lookupKeyLocked(key)
		if err != nil {
			return err
		}
		if ok {
			updatesByShard[existingShard] = append(updatesByShard[existingShard], pending{edge: edge, offset: existingOffset})
		} else {
			shardNum := edge.SourceID / s.shardSize
			insertsByShard[shardNum] = append(insertsByShard[shardNum], edge)
		}
	}

	// In-place updates: ordered file rewrites, no bbolt mutation.
	for shardNum, batch := range updatesByShard {
		for _, p := range batch {
			if err := s.writeEdgeAtOffset(shardNum, p.offset, p.edge); err != nil {
				return err
			}
		}
	}

	// Inserts: per-shard, single edges.bin append + single bbolt tx.
	for shardNum, batch := range insertsByShard {
		db, err := s.openShardLocked(shardNum)
		if err != nil {
			return err
		}
		if err := s.appendEdgesBatchToShard(db, shardNum, batch); err != nil {
			return err
		}
	}
	return nil
}

// IterateEdges streams every edge across every open shard through
// visit, in shard-then-file order. Heap residency is one *Edge at a
// time — the foundation for caller-side aggregation that needs to
// scale beyond what fits in memory.
//
// visit returning a non-nil error stops iteration and propagates the
// error to the caller.
func (s *EdgeShardStore) IterateEdges(visit func(*Edge) error) error {
	s.mu.RLock()
	shards := make([]uint32, 0, len(s.shardDBs))
	for shardNum := range s.shardDBs {
		shards = append(shards, shardNum)
	}
	s.mu.RUnlock()

	for _, shardNum := range shards {
		if err := s.streamShardEdges(shardNum, nil, visit); err != nil {
			return err
		}
	}
	return nil
}

// ReadAll returns all edges across all shards. Materializes the full
// slice in heap; for scaled workloads use IterateEdges.
func (s *EdgeShardStore) ReadAll() ([]*Edge, error) {
	return s.ReadAllFiltered(nil)
}

// ReadAllFiltered returns all edges where filter returns true. A nil
// filter accepts every edge. Streams edges.bin sequentially per shard
// (single open per shard, sequential read) and applies the filter in
// the loop, so heap residency is one decoded edge at a time times
// number of shards.
func (s *EdgeShardStore) ReadAllFiltered(filter func(sourceID, targetID uint32) bool) ([]*Edge, error) {
	s.mu.RLock()
	shards := make([]uint32, 0, len(s.shardDBs))
	for shardNum := range s.shardDBs {
		shards = append(shards, shardNum)
	}
	s.mu.RUnlock()

	var result []*Edge
	for _, shardNum := range shards {
		if err := s.streamShardEdges(shardNum, filter, func(edge *Edge) error {
			result = append(result, edge)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// GetOutgoing returns all edges with the given source node ID.
func (s *EdgeShardStore) GetOutgoing(sourceID uint32) ([]*Edge, error) {
	return s.getDirectional(sourceID, bucketOutgoing)
}

// GetIncoming returns all edges with the given target node ID.
func (s *EdgeShardStore) GetIncoming(targetID uint32) ([]*Edge, error) {
	return s.getDirectional(targetID, bucketIncoming)
}

// getDirectional reads the offset list for nodeID from the named
// bucket across every open shard, then reads each referenced edge.
// Outgoing: nodeID is in shard `nodeID/shardSize`, so we only need
// to look in that one shard. Incoming: edges to nodeID can come from
// any shard, so we scan all shards' incoming buckets.
func (s *EdgeShardStore) getDirectional(nodeID uint32, bucket []byte) ([]*Edge, error) {
	s.mu.RLock()
	shards := make([]uint32, 0, len(s.shardDBs))
	if string(bucket) == string(bucketOutgoing) {
		// Outgoing edges live where SourceID lives — one shard.
		shardNum := nodeID / s.shardSize
		if _, ok := s.shardDBs[shardNum]; ok {
			shards = append(shards, shardNum)
		}
	} else {
		// Incoming edges may originate from any source shard.
		for shardNum := range s.shardDBs {
			shards = append(shards, shardNum)
		}
	}
	s.mu.RUnlock()

	if len(shards) == 0 {
		return nil, nil
	}

	var result []*Edge
	keyBytes := encodeNodeIDKey(nodeID)
	for _, shardNum := range shards {
		s.mu.RLock()
		db := s.shardDBs[shardNum]
		s.mu.RUnlock()
		if db == nil {
			continue
		}
		var localOffsets []int64
		err := db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucket)
			if b == nil {
				return nil
			}
			val := b.Get(keyBytes)
			if val == nil {
				return nil
			}
			localOffsets = decodeOffsetList(val)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("sylkdir: read directional index shard=%d: %w", shardNum, err)
		}
		if len(localOffsets) == 0 {
			continue
		}
		edges, err := s.readEdgesAtLocalOffsets(shardNum, localOffsets)
		if err != nil {
			return nil, err
		}
		result = append(result, edges...)
	}
	return result, nil
}

// GetEdge returns a specific edge by its compound key.
func (s *EdgeShardStore) GetEdge(sourceID, targetID uint32, edgeType uint8) (*Edge, error) {
	key := EdgeKey{SourceID: sourceID, TargetID: targetID, Type: edgeType}
	s.mu.RLock()
	defer s.mu.RUnlock()
	shardNum, localOffset, ok, err := s.lookupKeyLocked(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrEdgeNotFound
	}
	edges, err := s.readEdgesAtLocalOffsets(shardNum, []int64{localOffset})
	if err != nil {
		return nil, err
	}
	if len(edges) == 0 {
		return nil, ErrEdgeNotFound
	}
	return edges[0], nil
}

// Count returns the total number of edges across all shards. O(shards)
// not O(edges) — bbolt's Stats() returns bucket KeyN in O(1).
func (s *EdgeShardStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := 0
	for _, db := range s.shardDBs {
		_ = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucketKeys)
			if b == nil {
				return nil
			}
			total += b.Stats().KeyN
			return nil
		})
	}
	return total
}

// SaveIndexes is now a no-op: bbolt persists every Write/WriteBatch
// transaction synchronously, so there is no in-memory dirty state to
// flush. Retained for API compatibility with prior versions of sylk
// that called SaveIndexes() at checkpoint boundaries.
func (s *EdgeShardStore) SaveIndexes() error { return nil }

// Close decrements the singleton refcount. The actual bbolt handles
// are closed only when the last caller departs — this lets callers
// follow conventional create/use/Close ownership semantics without
// the second-open file-lock conflict that would arise if every
// caller held its own *bolt.DB on the shared file.
func (s *EdgeShardStore) Close() error {
	sharedEdgeStoresMu.Lock()
	entry, ok := sharedEdgeStores[s.basePath]
	if !ok {
		// Not in the registry — either already fully closed, or this
		// instance was never registered (defensive: do nothing).
		sharedEdgeStoresMu.Unlock()
		return nil
	}
	entry.refCount--
	if entry.refCount > 0 {
		sharedEdgeStoresMu.Unlock()
		return nil
	}
	delete(sharedEdgeStores, s.basePath)
	sharedEdgeStoresMu.Unlock()

	return s.shutdown()
}

// shutdown closes every open bbolt handle. Called by Close() when
// the last reference departs.
func (s *EdgeShardStore) shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	var firstErr error
	for shardNum, db := range s.shardDBs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("sylkdir: close shard %d bolt: %w", shardNum, err)
		}
	}
	s.shardDBs = make(map[uint32]*bolt.DB)
	return firstErr
}

// EdgeStats summarises across all shards. The OutgoingNodes /
// IncomingNodes counts now come from bbolt bucket key counts, so
// they're exact even at scale (no in-memory map to walk).
type EdgeStats struct {
	TotalEdges    int
	ShardCount    int
	OutgoingNodes int
	IncomingNodes int
}

// Stats returns aggregated statistics across shards.
func (s *EdgeShardStore) Stats() EdgeStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := EdgeStats{ShardCount: len(s.shardDBs)}
	for _, db := range s.shardDBs {
		_ = db.View(func(tx *bolt.Tx) error {
			if b := tx.Bucket(bucketKeys); b != nil {
				stats.TotalEdges += b.Stats().KeyN
			}
			if b := tx.Bucket(bucketOutgoing); b != nil {
				stats.OutgoingNodes += b.Stats().KeyN
			}
			if b := tx.Bucket(bucketIncoming); b != nil {
				stats.IncomingNodes += b.Stats().KeyN
			}
			return nil
		})
	}
	return stats
}

// ─── shard handle management ─────────────────────────────────────────

// openShardLocked opens (or migrates+opens) the bbolt index for
// shardNum. Must be called with s.mu held in write mode. Idempotent —
// returns the cached handle on subsequent calls.
func (s *EdgeShardStore) openShardLocked(shardNum uint32) (*bolt.DB, error) {
	if db, ok := s.shardDBs[shardNum]; ok {
		return db, nil
	}

	shardPath := s.shardPath(shardNum)
	if err := os.MkdirAll(shardPath, 0755); err != nil {
		return nil, fmt.Errorf("create shard dir: %w", err)
	}

	boltPath := filepath.Join(shardPath, "index.bolt")
	db, err := bolt.Open(boltPath, 0o644, &bolt.Options{Timeout: boltOpenTimeout})
	if err != nil {
		return nil, fmt.Errorf("open index.bolt: %w", err)
	}

	// Ensure buckets exist + recover any uncommitted edges (edges.bin
	// content past meta.high_water).
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketKeys, bucketOutgoing, bucketIncoming, bucketMeta} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensure buckets: %w", err)
	}

	if err := s.recoverShard(db, shardNum); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("recover shard %d: %w", shardNum, err)
	}

	s.shardDBs[shardNum] = db
	return db, nil
}

// recoverShard replays edges.bin entries that were written but not
// yet indexed (the cursor lags the file size). Also performs a
// one-time migration from the legacy outgoing.idx / incoming.idx
// format if no high_water cursor exists yet.
func (s *EdgeShardStore) recoverShard(db *bolt.DB, shardNum uint32) error {
	edgesPath := filepath.Join(s.shardPath(shardNum), "edges.bin")
	fi, err := os.Stat(edgesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat edges.bin: %w", err)
	}
	fileSize := fi.Size()

	var cursor int64
	if err := db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(bucketMeta)
		if meta == nil {
			return nil
		}
		if val := meta.Get(metaHighWater); len(val) == 8 {
			cursor = int64(binary.LittleEndian.Uint64(val))
		}
		return nil
	}); err != nil {
		return fmt.Errorf("read high_water: %w", err)
	}

	if cursor >= fileSize {
		return nil
	}

	// Replay [cursor, fileSize). Read in one shot — file size is
	// bounded by shardSize × EdgeRecordSize; a full shard at K8s scale
	// is ~22MB which is fine for a single ReadAt.
	missed := fileSize - cursor
	buf := make([]byte, missed)
	f, err := os.Open(edgesPath)
	if err != nil {
		return fmt.Errorf("open edges.bin: %w", err)
	}
	if _, err := f.ReadAt(buf, cursor); err != nil {
		_ = f.Close()
		return fmt.Errorf("read edges.bin tail: %w", err)
	}
	_ = f.Close()

	return db.Update(func(tx *bolt.Tx) error {
		keys := tx.Bucket(bucketKeys)
		out := tx.Bucket(bucketOutgoing)
		in := tx.Bucket(bucketIncoming)
		meta := tx.Bucket(bucketMeta)

		applied := int64(0)
		for off := int64(0); off+EdgeRecordSize <= int64(len(buf)); off += EdgeRecordSize {
			edge := &Edge{}
			if err := edge.UnmarshalBinary(buf[off : off+EdgeRecordSize]); err != nil {
				continue // corrupt record — skip; cursor will advance past it
			}
			localOffset := cursor + off
			if err := indexEdgeInTx(keys, out, in, edge, localOffset); err != nil {
				return err
			}
			applied = off + EdgeRecordSize
		}

		// Advance cursor past the contiguous valid prefix we just
		// indexed. If applied == 0 (no valid records), cursor stays
		// at original value; on next open we'll retry — bounded by
		// the corrupt-region size.
		if applied > 0 {
			newCursor := cursor + applied
			var bytes [8]byte
			binary.LittleEndian.PutUint64(bytes[:], uint64(newCursor))
			if err := meta.Put(metaHighWater, bytes[:]); err != nil {
				return err
			}
		}
		return nil
	})
}

// appendEdgeToShard appends one edge to edges.bin and updates the
// shard's bbolt indexes atomically. The edges.bin write happens
// before the bbolt tx; if we crash between, recoverShard will replay.
func (s *EdgeShardStore) appendEdgeToShard(db *bolt.DB, shardNum uint32, edge *Edge) error {
	edgesPath := filepath.Join(s.shardPath(shardNum), "edges.bin")
	f, err := os.OpenFile(edgesPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open edges.bin for append: %w", err)
	}
	localOffset, err := f.Seek(0, 2)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("seek end: %w", err)
	}
	if _, err := f.Write(edge.MarshalBinary()); err != nil {
		_ = f.Close()
		return fmt.Errorf("write edge: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close edges.bin: %w", err)
	}

	return db.Update(func(tx *bolt.Tx) error {
		keys := tx.Bucket(bucketKeys)
		out := tx.Bucket(bucketOutgoing)
		in := tx.Bucket(bucketIncoming)
		meta := tx.Bucket(bucketMeta)

		if err := indexEdgeInTx(keys, out, in, edge, localOffset); err != nil {
			return err
		}
		return advanceHighWater(meta, localOffset+int64(EdgeRecordSize))
	})
}

// appendEdgesBatchToShard appends a batch of new edges to one shard
// in a single bbolt transaction.
func (s *EdgeShardStore) appendEdgesBatchToShard(db *bolt.DB, shardNum uint32, edges []*Edge) error {
	edgesPath := filepath.Join(s.shardPath(shardNum), "edges.bin")
	f, err := os.OpenFile(edgesPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open edges.bin for append: %w", err)
	}
	startOffset, err := f.Seek(0, 2)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("seek end: %w", err)
	}
	type placed struct {
		edge        *Edge
		localOffset int64
	}
	placements := make([]placed, 0, len(edges))
	for i, edge := range edges {
		off := startOffset + int64(i)*int64(EdgeRecordSize)
		placements = append(placements, placed{edge: edge, localOffset: off})
		if _, err := f.Write(edge.MarshalBinary()); err != nil {
			_ = f.Close()
			return fmt.Errorf("write edge: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close edges.bin: %w", err)
	}

	return db.Update(func(tx *bolt.Tx) error {
		keys := tx.Bucket(bucketKeys)
		out := tx.Bucket(bucketOutgoing)
		in := tx.Bucket(bucketIncoming)
		meta := tx.Bucket(bucketMeta)

		for _, p := range placements {
			if err := indexEdgeInTx(keys, out, in, p.edge, p.localOffset); err != nil {
				return err
			}
		}
		final := startOffset + int64(len(edges))*int64(EdgeRecordSize)
		return advanceHighWater(meta, final)
	})
}

// writeEdgeAtOffset rewrites the edge bytes at a known offset. Index
// is unchanged — same key, same offset; this is a record update only.
func (s *EdgeShardStore) writeEdgeAtOffset(shardNum uint32, localOffset int64, edge *Edge) error {
	edgesPath := filepath.Join(s.shardPath(shardNum), "edges.bin")
	f, err := os.OpenFile(edgesPath, os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open edges.bin for update: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteAt(edge.MarshalBinary(), localOffset); err != nil {
		return fmt.Errorf("write edge at offset: %w", err)
	}
	return nil
}

// lookupKeyLocked finds the (shardNum, localOffset) for an EdgeKey by
// scanning every open shard's "k" bucket. Updates can target an edge
// in any shard — we cannot derive shardNum from the key alone.
//
// Performance note: in steady state this is one bbolt Get per shard
// (page-cached) — for a typical project with O(10) shards open this
// is ~10µs warm. To accelerate negative lookups under heavy write
// volume, a per-shard bloom filter is the natural follow-up.
func (s *EdgeShardStore) lookupKeyLocked(key EdgeKey) (uint32, int64, bool, error) {
	keyBytes := encodeEdgeKey(key)
	for shardNum, db := range s.shardDBs {
		var (
			found       bool
			localOffset int64
		)
		err := db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(bucketKeys)
			if b == nil {
				return nil
			}
			val := b.Get(keyBytes)
			if val == nil {
				return nil
			}
			if len(val) != 8 {
				return fmt.Errorf("corrupt key offset: len=%d", len(val))
			}
			localOffset = int64(binary.LittleEndian.Uint64(val))
			found = true
			return nil
		})
		if err != nil {
			return 0, 0, false, fmt.Errorf("lookup key shard %d: %w", shardNum, err)
		}
		if found {
			return shardNum, localOffset, true, nil
		}
	}
	return 0, 0, false, nil
}

// ─── edge file readers ────────────────────────────────────────────────

// readEdgesAtLocalOffsets reads a batch of edges from a single shard
// at the given local offsets. One file open, sorted offsets give
// sequential read order.
func (s *EdgeShardStore) readEdgesAtLocalOffsets(shardNum uint32, localOffsets []int64) ([]*Edge, error) {
	if len(localOffsets) == 0 {
		return nil, nil
	}
	edgesPath := filepath.Join(s.shardPath(shardNum), "edges.bin")
	f, err := os.Open(edgesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open edges.bin: %w", err)
	}
	defer f.Close()

	result := make([]*Edge, 0, len(localOffsets))
	buf := make([]byte, EdgeRecordSize)
	for _, off := range localOffsets {
		if _, err := f.ReadAt(buf, off); err != nil {
			continue // best-effort: skip torn / past-EOF reads
		}
		edge := &Edge{}
		if err := edge.UnmarshalBinary(buf); err != nil {
			continue
		}
		result = append(result, edge)
	}
	return result, nil
}

// streamShardEdges sequentially reads every edge in a shard, applying
// the optional filter, invoking visit per accepted edge. Bounded
// heap: one record buffer reused across iterations.
func (s *EdgeShardStore) streamShardEdges(shardNum uint32, filter func(sourceID, targetID uint32) bool, visit func(*Edge) error) error {
	edgesPath := filepath.Join(s.shardPath(shardNum), "edges.bin")
	f, err := os.Open(edgesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open edges.bin: %w", err)
	}
	defer f.Close()
	buf := make([]byte, EdgeRecordSize)
	for {
		n, err := f.Read(buf)
		if n == 0 {
			break
		}
		if err != nil && n < EdgeRecordSize {
			break
		}
		if n < EdgeRecordSize {
			break
		}
		edge := &Edge{}
		if err := edge.UnmarshalBinary(buf); err != nil {
			continue
		}
		if filter != nil && !filter(edge.SourceID, edge.TargetID) {
			continue
		}
		if err := visit(edge); err != nil {
			return err
		}
	}
	return nil
}

// ─── encoding helpers ────────────────────────────────────────────────

// encodeEdgeKey returns the bbolt key bytes for an EdgeKey: 9 bytes
// SourceID(4 LE) + TargetID(4 LE) + Type(1). Little-endian matches
// the on-disk record format; bbolt uses byte ordering for B+tree
// placement, but keys are exact-match only here, so endianness is
// purely a serialization choice.
func encodeEdgeKey(k EdgeKey) []byte {
	buf := make([]byte, 9)
	binary.LittleEndian.PutUint32(buf[0:4], k.SourceID)
	binary.LittleEndian.PutUint32(buf[4:8], k.TargetID)
	buf[8] = k.Type
	return buf
}

// encodeNodeIDKey returns the bbolt key for an outgoing/incoming
// bucket lookup: 4 bytes nodeID big-endian. BE here makes range scans
// (when we add them) numerically ordered — cheap insurance.
func encodeNodeIDKey(nodeID uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, nodeID)
	return buf
}

// decodeOffsetList unpacks a directional bucket value (concatenation
// of int64 little-endian offsets) into a slice.
func decodeOffsetList(val []byte) []int64 {
	if len(val)%8 != 0 {
		return nil
	}
	out := make([]int64, len(val)/8)
	for i := range out {
		out[i] = int64(binary.LittleEndian.Uint64(val[i*8 : i*8+8]))
	}
	return out
}

// encodeOffsetList packs a slice of offsets into a directional bucket
// value. The inverse of decodeOffsetList.
func encodeOffsetList(offsets []int64) []byte {
	buf := make([]byte, len(offsets)*8)
	for i, off := range offsets {
		binary.LittleEndian.PutUint64(buf[i*8:i*8+8], uint64(off))
	}
	return buf
}

// indexEdgeInTx writes one edge's index entries inside an open bbolt
// tx. Idempotent — re-applying the same edge produces the same state
// (used by recoverShard's replay path).
func indexEdgeInTx(keys, out, in *bolt.Bucket, edge *Edge, localOffset int64) error {
	keyBytes := encodeEdgeKey(edge.EdgeKey())
	var offBytes [8]byte
	binary.LittleEndian.PutUint64(offBytes[:], uint64(localOffset))
	if err := keys.Put(keyBytes, offBytes[:]); err != nil {
		return err
	}
	if err := appendOffset(out, encodeNodeIDKey(edge.SourceID), localOffset); err != nil {
		return err
	}
	return appendOffset(in, encodeNodeIDKey(edge.TargetID), localOffset)
}

// appendOffset appends localOffset to the packed offset list under
// key in the given bucket. Read-modify-write within the open tx;
// duplicates are skipped (replay safety).
func appendOffset(b *bolt.Bucket, key []byte, localOffset int64) error {
	existing := b.Get(key)
	offsets := decodeOffsetList(existing)
	for _, off := range offsets {
		if off == localOffset {
			return nil // already indexed
		}
	}
	offsets = append(offsets, localOffset)
	return b.Put(key, encodeOffsetList(offsets))
}

// advanceHighWater monotonically advances the meta high_water cursor
// to newCursor (does nothing if the existing value is already higher).
func advanceHighWater(meta *bolt.Bucket, newCursor int64) error {
	existing := meta.Get(metaHighWater)
	if len(existing) == 8 {
		current := int64(binary.LittleEndian.Uint64(existing))
		if current >= newCursor {
			return nil
		}
	}
	var bytes [8]byte
	binary.LittleEndian.PutUint64(bytes[:], uint64(newCursor))
	return meta.Put(metaHighWater, bytes[:])
}

// ─── path helpers ────────────────────────────────────────────────────

func (s *EdgeShardStore) shardPath(shardNum uint32) string {
	return filepath.Join(s.basePath, fmt.Sprintf("shard_%04d", shardNum))
}

func parseShardName(name string) (uint32, bool) {
	var n uint32
	if _, err := fmt.Sscanf(name, "shard_%04d", &n); err != nil {
		return 0, false
	}
	return n, true
}
