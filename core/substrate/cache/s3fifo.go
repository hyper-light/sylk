package cache

import (
	"container/list"
	"sync"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

// S3FIFO is the substrate's tier transition policy.
//
// From "FIFO Queues are All You Need for Cache Eviction" (SOSP 2023):
// S3-FIFO uses three FIFO queues — Small (probationary, ~10% capacity),
// Main (stable, ~90% capacity), Ghost (recently-evicted hashes, no
// data) — to achieve hit rates competitive with W-TinyLFU and ARC at
// substantially lower implementation complexity and better scan
// resistance.
//
// Mapping onto the substrate's three-tier representation:
//
//   - Small queue ≈ blobs that just entered Hot tier (probationary).
//     If they're touched again before eviction, they graduate to Main.
//   - Main queue ≈ stable Hot tier. LRU-ish eviction at the tail.
//   - Ghost queue ≈ Warm tier admission gate. A blob recently demoted
//     out of Hot lives in the Ghost set; if it's referenced again
//     while in Ghost, the cache promotes it directly back to Hot
//     (skipping the Small probationary period).
//
// This implementation is the Hot-tier-only S3-FIFO; the substrate's
// Warm/Cold transitions are governed by simpler "demote on Hot
// eviction, evict from Warm via FIFO" rules layered on top.
//
// Safe for concurrent use.
type S3FIFO struct {
	mu sync.Mutex

	smallCap int
	mainCap  int
	ghostCap int

	small *list.List
	main  *list.List
	ghost *list.List

	smallIndex map[blob.Hash]*list.Element
	mainIndex  map[blob.Hash]*list.Element
	ghostIndex map[blob.Hash]*list.Element

	// freqs holds the small/main queue per-blob recent-access count
	// (S3-FIFO's "freq" field). Distinct from the TinyLFU sketch — that
	// gates promotions; this tracks "should this blob graduate from
	// Small to Main / survive eviction in Main".
	freqs map[blob.Hash]uint8
}

const (
	// s3FIFOSmallShare: fraction of total capacity allocated to the
	// Small (probationary) queue. The paper recommends 10%.
	s3FIFOSmallShare = 0.1

	// s3FIFOGhostShare: fraction of total capacity reserved for Ghost
	// entries. Ghost holds hashes only (no bytes), so the share can be
	// larger; the paper uses 90% (matching Main).
	s3FIFOGhostShare = 1.0

	// s3FIFOMaxFreq: cap on per-blob freq counter. The paper's reference
	// implementation uses 3.
	s3FIFOMaxFreq uint8 = 3
)

// S3FIFODecision describes what S3FIFO recommends after an admission
// or access call.
type S3FIFODecision struct {
	// Promoted, when non-zero, names a blob whose access caused it to
	// be re-promoted from Ghost back to Hot.
	Promoted blob.Hash

	// Evicted, when non-zero, names a blob that was evicted from Hot
	// to make room. The cache should demote it to Warm.
	Evicted blob.Hash

	// EvictedFromGhost names a Ghost-queue entry that was dropped to
	// make room for a new Ghost entry. The cache should demote any
	// Warm representation of it to Cold (or fully evict if Cold cap
	// is also exceeded).
	EvictedFromGhost blob.Hash
}

// NewS3FIFO constructs an S3FIFO sized for the given Hot tier capacity.
//
// The Small queue gets s3FIFOSmallShare of the capacity; Main gets the
// rest. Ghost gets up to s3FIFOGhostShare × Main capacity.
func NewS3FIFO(hotCapacity int) *S3FIFO {
	if hotCapacity <= 0 {
		hotCapacity = s3FIFOMinCapacity
	}
	smallCap := int(float64(hotCapacity)*s3FIFOSmallShare) + 1
	mainCap := hotCapacity - smallCap
	if mainCap < 1 {
		mainCap = 1
	}
	ghostCap := int(float64(mainCap) * s3FIFOGhostShare)
	if ghostCap < 1 {
		ghostCap = 1
	}
	return &S3FIFO{
		smallCap:   smallCap,
		mainCap:    mainCap,
		ghostCap:   ghostCap,
		small:      list.New(),
		main:       list.New(),
		ghost:      list.New(),
		smallIndex: make(map[blob.Hash]*list.Element),
		mainIndex:  make(map[blob.Hash]*list.Element),
		ghostIndex: make(map[blob.Hash]*list.Element),
		freqs:      make(map[blob.Hash]uint8),
	}
}

const s3FIFOMinCapacity = 4

// Touch records an access to the given blob hash.
//
// If the hash is in Small or Main, its freq counter is bumped (capped
// at s3FIFOMaxFreq).
//
// If the hash is in Ghost, the cache should re-promote it from Warm
// to Hot — Ghost membership means "recently evicted from Hot, but
// touched again", which is the strongest promotion signal S3-FIFO
// recognizes. The Decision.Promoted field carries the hash.
func (s *S3FIFO) Touch(hash blob.Hash) S3FIFODecision {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.smallIndex[hash]; ok {
		s.bumpFreq(hash)
		return S3FIFODecision{}
	}
	if _, ok := s.mainIndex[hash]; ok {
		s.bumpFreq(hash)
		return S3FIFODecision{}
	}
	if elem, ok := s.ghostIndex[hash]; ok {
		s.removeGhost(hash, elem)
		return S3FIFODecision{Promoted: hash}
	}
	return S3FIFODecision{}
}

// Insert records that a blob was just put into Hot tier.
//
// Returns Decision.Evicted (and optionally EvictedFromGhost) when the
// insertion required making room. The cache caller demotes evicted
// blobs to Warm and evicted-from-ghost blobs to Cold.
func (s *S3FIFO) Insert(hash blob.Hash) S3FIFODecision {
	s.mu.Lock()
	defer s.mu.Unlock()

	decision := S3FIFODecision{}
	if _, ok := s.smallIndex[hash]; ok {
		return decision
	}
	if _, ok := s.mainIndex[hash]; ok {
		return decision
	}
	// Insert into Small. If Small is full, evict from Small (which
	// might cascade into Main + Ghost). The cache caller demotes the
	// evicted blob to Warm.
	if s.small.Len() >= s.smallCap {
		evicted, ghostEvicted := s.evictFromSmall()
		decision.Evicted = evicted
		decision.EvictedFromGhost = ghostEvicted
	}
	elem := s.small.PushFront(hash)
	s.smallIndex[hash] = elem
	s.freqs[hash] = 0
	return decision
}

// Remove unconditionally drops the hash from any queue it's in. Used
// when the underlying blob's refcount drops to zero — there's no point
// keeping S3FIFO bookkeeping for a blob that no longer exists.
func (s *S3FIFO) Remove(hash blob.Hash) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if elem, ok := s.smallIndex[hash]; ok {
		s.small.Remove(elem)
		delete(s.smallIndex, hash)
	}
	if elem, ok := s.mainIndex[hash]; ok {
		s.main.Remove(elem)
		delete(s.mainIndex, hash)
	}
	if elem, ok := s.ghostIndex[hash]; ok {
		s.ghost.Remove(elem)
		delete(s.ghostIndex, hash)
	}
	delete(s.freqs, hash)
}

func (s *S3FIFO) bumpFreq(hash blob.Hash) {
	if s.freqs[hash] < s3FIFOMaxFreq {
		s.freqs[hash]++
	}
}

// evictFromSmall handles the "Small is full, need to make room" case.
// The S3-FIFO algorithm:
//   - If the tail of Small has freq > 0, graduate it to Main (which
//     may itself need to evict).
//   - Otherwise (freq == 0), evict it directly to Ghost.
//
// Returns (evicted_for_demotion, ghost_evicted_for_full_demotion).
func (s *S3FIFO) evictFromSmall() (blob.Hash, blob.Hash) {
	tail := s.small.Back()
	if tail == nil {
		return blob.Hash{}, blob.Hash{}
	}
	hash := tail.Value.(blob.Hash)
	s.small.Remove(tail)
	delete(s.smallIndex, hash)
	if s.freqs[hash] > 0 {
		// Graduate to Main. May cause Main to evict.
		ghostEvicted := s.insertIntoMain(hash)
		return blob.Hash{}, ghostEvicted
	}
	// Direct evict to Ghost. Returns hash as Evicted (cache demotes
	// to Warm); Ghost insertion may evict an older Ghost entry (cache
	// demotes that to Cold).
	ghostEvicted := s.insertIntoGhost(hash)
	delete(s.freqs, hash)
	return hash, ghostEvicted
}

// insertIntoMain places hash at the head of Main, evicting from the
// tail when capacity is exceeded. Returns any Ghost-evicted hash.
func (s *S3FIFO) insertIntoMain(hash blob.Hash) blob.Hash {
	elem := s.main.PushFront(hash)
	s.mainIndex[hash] = elem
	if s.main.Len() <= s.mainCap {
		return blob.Hash{}
	}
	tail := s.main.Back()
	if tail == nil {
		return blob.Hash{}
	}
	evicted := tail.Value.(blob.Hash)
	s.main.Remove(tail)
	delete(s.mainIndex, evicted)
	if s.freqs[evicted] > 0 {
		// Re-circulate: evicted-from-Main with freq>0 goes back to
		// Main head. The freq is decremented to age the entry.
		s.freqs[evicted]--
		recircled := s.main.PushFront(evicted)
		s.mainIndex[evicted] = recircled
		return blob.Hash{}
	}
	// freq==0, true eviction to Ghost.
	return s.insertIntoGhost(evicted)
}

// insertIntoGhost adds hash to Ghost. If Ghost is full, evicts from
// Ghost tail and returns the evicted hash (cache demotes its Warm
// representation to Cold).
func (s *S3FIFO) insertIntoGhost(hash blob.Hash) blob.Hash {
	elem := s.ghost.PushFront(hash)
	s.ghostIndex[hash] = elem
	if s.ghost.Len() <= s.ghostCap {
		return blob.Hash{}
	}
	tail := s.ghost.Back()
	if tail == nil {
		return blob.Hash{}
	}
	ghostEvicted := tail.Value.(blob.Hash)
	s.ghost.Remove(tail)
	delete(s.ghostIndex, ghostEvicted)
	delete(s.freqs, ghostEvicted)
	return ghostEvicted
}

func (s *S3FIFO) removeGhost(hash blob.Hash, elem *list.Element) {
	s.ghost.Remove(elem)
	delete(s.ghostIndex, hash)
}

// SmallLen, MainLen, GhostLen return the current queue sizes. Used by
// tests and metrics.
func (s *S3FIFO) SmallLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.small.Len()
}

func (s *S3FIFO) MainLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.main.Len()
}

func (s *S3FIFO) GhostLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ghost.Len()
}
