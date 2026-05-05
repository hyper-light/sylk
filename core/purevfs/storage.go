package purevfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const HashSize = 32

var (
	ErrChunkNotFound    = errors.New("purevfs: chunk not found")
	ErrMemoryPressure   = errors.New("purevfs: memory pressure")
	ErrInvalidRange     = errors.New("purevfs: invalid range")
	ErrNegativeOffset   = errors.New("purevfs: negative offset")
	ErrMissingRealRead  = errors.New("purevfs: missing real file reader")
	ErrExtentCorruption = errors.New("purevfs: extent layout is corrupt")
)

type Hash [HashSize]byte

func SumHash(content []byte) Hash {
	return Hash(sha256.Sum256(content))
}

func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

func (h Hash) Short() string {
	return hex.EncodeToString(h[:4])
}

func (h Hash) IsZero() bool {
	for _, b := range h {
		if b != 0 {
			return false
		}
	}
	return true
}

type ChunkStoreStats struct {
	Chunks           int
	Bytes            int64
	MemoryLimit      int64
	Refs             int64
	UniquePutCount   int64
	DedupHitCount    int64
	RejectedPutCount int64
}

// chunkEntry is the per-chunk metadata held by ChunkStore. The bytes
// themselves live in the arena slot; the entry only carries the slot
// pointer and refcount. Keeping bytes off the Go heap is the primary
// inuse_space win — the heap profile sees only this small struct per
// chunk, not the chunk content.
//
// refs is atomic so Acquire can increment under a shard read lock
// (parallel with concurrent Get/CopyInto). Release takes the shard
// write lock so the decrement and the slot.Free that follows
// refs == 0 are observed as a single transition.
type chunkEntry struct {
	slot *chunkSlot
	refs atomic.Int32
}

// chunkShardBits picks the number of shards as a function of the SHA-256
// hash's first byte: every distinct value of hash[0] gets its own shard,
// so reads/writes for chunks with different hash[0] never contend.
// 8 bits → 256 shards: the natural cardinality of the chosen index, not
// a hand-picked constant.
const chunkShardBits = 8
const chunkShardCount = 1 << chunkShardBits

// chunkShard owns the chunk metadata for one slice of the hash space.
// Reads take RLock; mutations take Lock. The arena slot's bytes are
// dereferenced by CopyInto/Get under RLock — Release cannot Free the
// slot until every concurrent reader has released its read lock, so the
// shard lock is also the use-after-free guard.
//
// Refcount mutations on existing entries (Acquire, dedup hits in Put)
// run atomically under RLock so writers don't serialize on hot chunks.
// Slot reclamation (refs → 0) runs under Lock, which excludes any
// concurrent reader on the same shard.
type chunkShard struct {
	mu     sync.RWMutex
	chunks map[Hash]*chunkEntry
}

type ChunkStore struct {
	shards      [chunkShardCount]*chunkShard
	arena       *chunkArena
	memoryLimit int64
	bytes       atomic.Int64
	uniquePuts  atomic.Int64
	dedupHits   atomic.Int64
	rejected    atomic.Int64
	closed      atomic.Bool
}

func NewChunkStore(memoryLimit int64) *ChunkStore {
	s := &ChunkStore{
		arena:       newChunkArena(),
		memoryLimit: memoryLimit,
	}
	for i := range s.shards {
		s.shards[i] = &chunkShard{chunks: make(map[Hash]*chunkEntry)}
	}
	return s
}

// shardFor selects the shard owning hash. The hash is uniformly random
// (SHA-256 output), so hash[0] has uniform distribution across the
// 256 shards — no skew correction needed.
func (s *ChunkStore) shardFor(hash Hash) *chunkShard {
	return s.shards[hash[0]]
}

// reserveBytes claims n bytes against the global memory budget atomically.
// On rejection it rolls back and increments the rejected counter. On
// success the caller owns those bytes until they call releaseBytes.
// The claim happens before arena.Allocate so a failed allocation can
// release the budget without holding any shard lock.
func (s *ChunkStore) reserveBytes(n int64) error {
	if s.memoryLimit <= 0 {
		s.bytes.Add(n)
		return nil
	}
	proposed := s.bytes.Add(n)
	if proposed > s.memoryLimit {
		s.bytes.Add(-n)
		s.rejected.Add(1)
		return ErrMemoryPressure
	}
	return nil
}

func (s *ChunkStore) releaseBytes(n int64) {
	s.bytes.Add(-n)
}

func (s *ChunkStore) Put(content []byte) (Hash, bool, error) {
	if s.closed.Load() {
		return Hash{}, false, errArenaClosed
	}
	hash := SumHash(content)
	shard := s.shardFor(hash)

	// Fast path: dedup hit. Increment refs atomically while holding only
	// the read lock so concurrent dedup hits on the same shard don't
	// serialize.
	shard.mu.RLock()
	if entry, ok := shard.chunks[hash]; ok {
		entry.refs.Add(1)
		shard.mu.RUnlock()
		s.dedupHits.Add(1)
		return hash, true, nil
	}
	shard.mu.RUnlock()

	// Slow path: reserve the budget, allocate the slot, then commit
	// under the shard write lock. We allocate before taking the write
	// lock so the (potentially slow) arena allocation does not extend
	// the critical section.
	if err := s.reserveBytes(int64(len(content))); err != nil {
		return Hash{}, false, err
	}
	slot, err := s.arena.Allocate(len(content))
	if err != nil {
		s.releaseBytes(int64(len(content)))
		return Hash{}, false, err
	}
	copy(slot.bytes, content)

	shard.mu.Lock()
	if existing, ok := shard.chunks[hash]; ok {
		// Concurrent inserter won the race. Drop our slot and adopt
		// theirs. The Acquire-equivalent (refs += 1) happens under
		// the write lock here; correctness is the same as the fast
		// path above.
		existing.refs.Add(1)
		shard.mu.Unlock()
		s.arena.Free(slot)
		s.releaseBytes(int64(len(content)))
		s.dedupHits.Add(1)
		return hash, true, nil
	}
	entry := &chunkEntry{slot: slot}
	entry.refs.Store(1)
	shard.chunks[hash] = entry
	shard.mu.Unlock()
	s.uniquePuts.Add(1)
	return hash, false, nil
}

func (s *ChunkStore) Acquire(hash Hash) error {
	shard := s.shardFor(hash)
	shard.mu.RLock()
	entry, ok := shard.chunks[hash]
	if !ok {
		shard.mu.RUnlock()
		return ErrChunkNotFound
	}
	entry.refs.Add(1)
	shard.mu.RUnlock()
	return nil
}

func (s *ChunkStore) Release(hash Hash) error {
	shard := s.shardFor(hash)
	shard.mu.Lock()
	entry, ok := shard.chunks[hash]
	if !ok {
		shard.mu.Unlock()
		return ErrChunkNotFound
	}
	if entry.refs.Load() <= 1 {
		size := int64(entry.slot.Size())
		delete(shard.chunks, hash)
		shard.mu.Unlock()
		s.arena.Free(entry.slot)
		s.releaseBytes(size)
		return nil
	}
	entry.refs.Add(-1)
	shard.mu.Unlock()
	return nil
}

// Get returns a heap-allocated copy of the chunk's bytes. Callers that
// only need to copy bytes into their own buffer should prefer CopyInto,
// which avoids the per-call allocation by writing directly into the
// caller's destination slice.
func (s *ChunkStore) Get(hash Hash) ([]byte, error) {
	shard := s.shardFor(hash)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	entry, ok := shard.chunks[hash]
	if !ok {
		return nil, ErrChunkNotFound
	}
	out := make([]byte, entry.slot.Size())
	copy(out, entry.slot.bytes)
	return out, nil
}

// CopyInto writes the chunk's bytes [off:off+len(dst)] directly into
// dst, without allocating an intermediate heap buffer. Returns the
// number of bytes written. This is the preferred read API on the
// VFS read hot path; FileBody.ReadAt uses it to drain a chunk into the
// caller's destination buffer in one copy.
func (s *ChunkStore) CopyInto(dst []byte, hash Hash, off int) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	shard := s.shardFor(hash)
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	entry, ok := shard.chunks[hash]
	if !ok {
		return 0, ErrChunkNotFound
	}
	if off < 0 || off > entry.slot.Size() {
		return 0, ErrInvalidRange
	}
	avail := entry.slot.Size() - off
	n := len(dst)
	if n > avail {
		n = avail
	}
	copy(dst[:n], entry.slot.bytes[off:off+n])
	return n, nil
}

func (s *ChunkStore) Has(hash Hash) bool {
	shard := s.shardFor(hash)
	shard.mu.RLock()
	_, ok := shard.chunks[hash]
	shard.mu.RUnlock()
	return ok
}

func (s *ChunkStore) RefCount(hash Hash) int32 {
	shard := s.shardFor(hash)
	shard.mu.RLock()
	entry, ok := shard.chunks[hash]
	shard.mu.RUnlock()
	if !ok {
		return 0
	}
	return entry.refs.Load()
}

func (s *ChunkStore) Stats() ChunkStoreStats {
	var chunks int
	var refs int64
	for _, shard := range s.shards {
		shard.mu.RLock()
		chunks += len(shard.chunks)
		for _, entry := range shard.chunks {
			refs += int64(entry.refs.Load())
		}
		shard.mu.RUnlock()
	}
	return ChunkStoreStats{
		Chunks:           chunks,
		Bytes:            s.bytes.Load(),
		MemoryLimit:      s.memoryLimit,
		Refs:             refs,
		UniquePutCount:   s.uniquePuts.Load(),
		DedupHitCount:    s.dedupHits.Load(),
		RejectedPutCount: s.rejected.Load(),
	}
}

// ArenaMappedBytes returns the total virtual memory currently mapped by
// the chunk arena. Useful for telemetry: ArenaMappedBytes - Bytes is
// the slack capacity (free slots in mapped slabs). Growth in this gap
// indicates fragmentation across size classes.
func (s *ChunkStore) ArenaMappedBytes() int64 {
	return s.arena.MappedBytes()
}

// Close releases every mmap region the chunk arena holds. Callers must
// invoke Close when the ChunkStore (and the Workspace that owns it) is
// being torn down — without it, anonymous mmap regions stay mapped
// until process exit, which leaks virtual memory across short-lived
// stores (tests, transient workspace images).
//
// After Close, every operation on the store is undefined behavior; the
// caller must guarantee no FileBody still holds extents that point at
// chunks owned by this store.
//
// Close is idempotent. A finalizer on the underlying arena calls Close
// as a safety net for callers that drop the store without explicit
// teardown, but explicit Close is strongly preferred for deterministic
// reclaim.
func (s *ChunkStore) Close() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	for _, shard := range s.shards {
		shard.mu.Lock()
		shard.chunks = nil
		shard.mu.Unlock()
	}
	s.bytes.Store(0)
	s.arena.Close()
}

type ExtentKind uint8

const (
	ExtentBlob ExtentKind = iota
	ExtentRealFile
	ExtentZero
)

type Extent struct {
	LogicalStart int64
	Length       int64
	Kind         ExtentKind
	BlobHash     Hash
	BlobOffset   int64
	RealPath     string
	RealOffset   int64
}

func (e Extent) End() int64 {
	return e.LogicalStart + e.Length
}

func (e Extent) shift(delta int64) Extent {
	e.LogicalStart += delta
	return e
}

func (e Extent) subrange(start, end int64) Extent {
	out := e
	delta := start - e.LogicalStart
	out.LogicalStart = start
	out.Length = end - start
	switch e.Kind {
	case ExtentBlob:
		out.BlobOffset += delta
	case ExtentRealFile:
		out.RealOffset += delta
	}
	return out
}

type RealFileReader interface {
	ReadAt(path string, p []byte, off int64) (int, error)
}

type OSRealFileReader struct{}

func (OSRealFileReader) ReadAt(path string, p []byte, off int64) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return f.ReadAt(p, off)
}

type FileBody struct {
	size    int64
	extents []Extent
}

func NewEmptyFileBody() *FileBody {
	return &FileBody{}
}

func NewBlobFileBody(hash Hash, size int64) *FileBody {
	if size <= 0 {
		return NewEmptyFileBody()
	}
	return &FileBody{
		size: size,
		extents: []Extent{{
			LogicalStart: 0,
			Length:       size,
			Kind:         ExtentBlob,
			BlobHash:     hash,
		}},
	}
}

func NewChunkedFileBody(content []byte, store *ChunkStore, chunkSize int) (*FileBody, error) {
	if len(content) == 0 {
		return NewEmptyFileBody(), nil
	}
	if store == nil {
		return nil, ErrChunkNotFound
	}
	if chunkSize <= 0 {
		chunkSize = 64 << 10
	}
	extents := make([]Extent, 0, (len(content)+chunkSize-1)/chunkSize)
	for offset := 0; offset < len(content); offset += chunkSize {
		end := offset + chunkSize
		if end > len(content) {
			end = len(content)
		}
		hash, _, err := store.Put(content[offset:end])
		if err != nil {
			return nil, err
		}
		extents = append(extents, Extent{
			LogicalStart: int64(offset),
			Length:       int64(end - offset),
			Kind:         ExtentBlob,
			BlobHash:     hash,
		})
	}
	return &FileBody{
		size:    int64(len(content)),
		extents: extents,
	}, nil
}

func NewRealFileBody(realPath string, size int64) *FileBody {
	if size <= 0 {
		return NewEmptyFileBody()
	}
	return &FileBody{
		size: size,
		extents: []Extent{{
			LogicalStart: 0,
			Length:       size,
			Kind:         ExtentRealFile,
			RealPath:     realPath,
		}},
	}
}

func (b *FileBody) Clone() *FileBody {
	if b == nil {
		return NewEmptyFileBody()
	}
	out := &FileBody{
		size:    b.size,
		extents: make([]Extent, len(b.extents)),
	}
	copy(out.extents, b.extents)
	return out
}

func (b *FileBody) Size() int64 {
	if b == nil {
		return 0
	}
	return b.size
}

func (b *FileBody) Extents() []Extent {
	if b == nil {
		return nil
	}
	out := make([]Extent, len(b.extents))
	copy(out, b.extents)
	return out
}

func (b *FileBody) ReadAt(p []byte, off int64, reader RealFileReader, store *ChunkStore) (int, error) {
	if b == nil || len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, ErrNegativeOffset
	}
	if off >= b.size {
		return 0, io.EOF
	}

	limit := minInt64(int64(len(p)), b.size-off)
	if err := b.ensureCoveredRange(off, off+limit); err != nil {
		return 0, err
	}

	var written int64
	for written < limit {
		pos := off + written
		idx := b.findExtentIndex(pos)
		if idx < 0 {
			return int(written), ErrExtentCorruption
		}
		ex := b.extents[idx]
		extentOffset := pos - ex.LogicalStart
		remaining := limit - written
		chunkLen := minInt64(ex.Length-extentOffset, remaining)
		dst := p[written : written+chunkLen]

		switch ex.Kind {
		case ExtentZero:
			clear(dst)
		case ExtentBlob:
			if store == nil {
				return int(written), ErrChunkNotFound
			}
			n, err := store.CopyInto(dst, ex.BlobHash, int(ex.BlobOffset+extentOffset))
			if err != nil {
				return int(written), err
			}
			if int64(n) != chunkLen {
				return int(written) + n, ErrChunkNotFound
			}
		case ExtentRealFile:
			if reader == nil {
				return int(written), ErrMissingRealRead
			}
			n, err := reader.ReadAt(ex.RealPath, dst, ex.RealOffset+extentOffset)
			if int64(n) != chunkLen {
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				return int(written) + n, err
			}
		default:
			return int(written), ErrExtentCorruption
		}
		written += chunkLen
	}

	if limit < int64(len(p)) {
		return int(limit), io.EOF
	}
	return int(limit), nil
}

func (b *FileBody) WriteAt(off int64, content []byte, store *ChunkStore) error {
	if b == nil {
		return ErrInvalidRange
	}
	if off < 0 {
		return ErrNegativeOffset
	}
	if len(content) == 0 {
		return nil
	}
	if off > b.size {
		b.appendZero(off - b.size)
	}
	end := off + int64(len(content))
	replacedEnd := minInt64(end, b.size)
	repl, replLen, err := blobReplacement(content, store)
	if err != nil {
		return err
	}
	return b.replaceRange(off, replacedEnd, repl, replLen)
}

func (b *FileBody) InsertAt(off int64, content []byte, store *ChunkStore) error {
	if b == nil {
		return ErrInvalidRange
	}
	if off < 0 || off > b.size {
		return ErrInvalidRange
	}
	if len(content) == 0 {
		return nil
	}
	repl, replLen, err := blobReplacement(content, store)
	if err != nil {
		return err
	}
	return b.replaceRange(off, off, repl, replLen)
}

func (b *FileBody) DeleteRange(start, end int64) error {
	if b == nil {
		return ErrInvalidRange
	}
	if start < 0 || end < start || end > b.size {
		return ErrInvalidRange
	}
	return b.replaceRange(start, end, nil, 0)
}

func (b *FileBody) Truncate(size int64) error {
	if b == nil || size < 0 {
		return ErrInvalidRange
	}
	switch {
	case size == b.size:
		return nil
	case size < b.size:
		return b.DeleteRange(size, b.size)
	default:
		b.appendZero(size - b.size)
		return nil
	}
}

func (b *FileBody) appendZero(length int64) {
	if length <= 0 {
		return
	}
	b.extents = appendAndNormalize(b.extents, Extent{
		LogicalStart: b.size,
		Length:       length,
		Kind:         ExtentZero,
	})
	b.size += length
}

func (b *FileBody) replaceRange(start, end int64, repl []Extent, replLen int64) error {
	if start < 0 || end < start || end > b.size {
		return ErrInvalidRange
	}

	delta := replLen - (end - start)
	result := make([]Extent, 0, len(b.extents)+len(repl)+2)

	for _, ex := range b.extents {
		switch {
		case ex.End() <= start:
			result = append(result, ex)
		case ex.LogicalStart >= end:
			result = append(result, ex.shift(delta))
		default:
			if ex.LogicalStart < start {
				result = append(result, ex.subrange(ex.LogicalStart, start))
			}
			if ex.End() > end {
				right := ex.subrange(end, ex.End()).shift(delta)
				result = append(result, right)
			}
		}
	}

	for _, ex := range repl {
		result = append(result, ex.shift(start))
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].LogicalStart < result[j].LogicalStart
	})

	b.size += delta
	b.extents = normalizeExtents(result)
	return b.ensureCoveredRange(0, b.size)
}

func (b *FileBody) ensureCoveredRange(start, end int64) error {
	if end < start {
		return ErrInvalidRange
	}
	if start == end {
		return nil
	}
	expected := start
	for _, ex := range b.extents {
		if ex.Length <= 0 {
			return ErrExtentCorruption
		}
		if ex.LogicalStart > expected {
			return ErrExtentCorruption
		}
		if ex.End() <= expected {
			continue
		}
		if ex.LogicalStart < expected {
			expected = minInt64(ex.End(), end)
		} else {
			expected = minInt64(ex.End(), end)
		}
		if expected >= end {
			return nil
		}
	}
	if start == 0 && end == 0 {
		return nil
	}
	if end == 0 {
		return nil
	}
	if b.size == 0 && start == 0 && end == 0 {
		return nil
	}
	if expected < end {
		return ErrExtentCorruption
	}
	return nil
}

func (b *FileBody) findExtentIndex(pos int64) int {
	idx := sort.Search(len(b.extents), func(i int) bool {
		return b.extents[i].End() > pos
	})
	if idx >= len(b.extents) {
		return -1
	}
	ex := b.extents[idx]
	if pos < ex.LogicalStart || pos >= ex.End() {
		return -1
	}
	return idx
}

func blobReplacement(content []byte, store *ChunkStore) ([]Extent, int64, error) {
	if len(content) == 0 {
		return nil, 0, nil
	}
	if store == nil {
		return nil, 0, ErrChunkNotFound
	}
	hash, _, err := store.Put(content)
	if err != nil {
		return nil, 0, err
	}
	return []Extent{{
		LogicalStart: 0,
		Length:       int64(len(content)),
		Kind:         ExtentBlob,
		BlobHash:     hash,
	}}, int64(len(content)), nil
}

func appendAndNormalize(extents []Extent, extent Extent) []Extent {
	return normalizeExtents(append(extents, extent))
}

func normalizeExtents(extents []Extent) []Extent {
	if len(extents) == 0 {
		return nil
	}
	sort.SliceStable(extents, func(i, j int) bool {
		return extents[i].LogicalStart < extents[j].LogicalStart
	})
	out := make([]Extent, 0, len(extents))
	for _, ex := range extents {
		if ex.Length <= 0 {
			continue
		}
		if len(out) == 0 {
			out = append(out, ex)
			continue
		}
		last := out[len(out)-1]
		if canMergeExtent(last, ex) {
			last.Length += ex.Length
			out[len(out)-1] = last
			continue
		}
		out = append(out, ex)
	}
	return out
}

func canMergeExtent(a, b Extent) bool {
	if a.Kind != b.Kind || a.End() != b.LogicalStart {
		return false
	}
	switch a.Kind {
	case ExtentBlob:
		return a.BlobHash == b.BlobHash && a.BlobOffset+a.Length == b.BlobOffset
	case ExtentRealFile:
		return a.RealPath == b.RealPath && a.RealOffset+a.Length == b.RealOffset
	case ExtentZero:
		return true
	default:
		return false
	}
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type EntryKind uint8

const (
	EntryFile EntryKind = iota
	EntryDir
	EntrySymlink
)

type Inode struct {
	ID         uint64
	Kind       EntryKind
	Mode       fs.FileMode
	ModTime    time.Time
	Nlink      int
	Body       *FileBody
	LinkTarget string
}

func (i *Inode) Clone() *Inode {
	if i == nil {
		return nil
	}
	out := *i
	if i.Body != nil {
		out.Body = i.Body.Clone()
	}
	return &out
}

func (i *Inode) Size() int64 {
	if i == nil {
		return 0
	}
	switch i.Kind {
	case EntryFile:
		if i.Body == nil {
			return 0
		}
		return i.Body.Size()
	case EntrySymlink:
		return int64(len(i.LinkTarget))
	default:
		return 0
	}
}
