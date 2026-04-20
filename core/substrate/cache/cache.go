package cache

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

// Cache is the substrate's tiered blob storage, layered on top of the
// underlying blob.Store.
//
// The Cache does not own blob storage; it advises tier transitions.
// The blob.Store remains the canonical bytes store. The Cache:
//
//   - Tracks which blobs are in which tier (Hot/Warm/Cold)
//   - For Warm: holds the compressed bytes alongside the blob's
//     ecosystem hint (so decompression can use the right dictionary)
//   - For Cold: holds the source URL the blob was fetched from
//     (so re-fetch knows where to look)
//   - Records read metrics for tier policy
//
// The Cache is intentionally separate from the blob.Store so the
// production substrate can swap blob backends (in-memory, mmap-backed,
// distributed) without rewriting tier logic. A Cache instance wraps
// one blob.Store and is the "tier-aware view" of that store.
//
// Safe for concurrent use.
type Cache struct {
	blobs *blob.Store
	dicts *DictionaryStore

	tinyLFU *TinyLFU
	policy  *S3FIFO

	mu sync.RWMutex
	// hotIndex: the cache considers a blob "hot" if it is in the
	// underlying blob.Store and present here. Removal from hotIndex
	// during demotion does NOT remove from blob.Store — the blob's
	// Warm representation lives in warmStore, and the Hot bytes are
	// reclaimed by the blob.Store's compaction.
	hotIndex  map[blob.Hash]struct{}
	warmStore map[blob.Hash]warmEntry
	coldStore map[blob.Hash]coldEntry

	// Per-blob ecosystem hint, used to pick the right dictionary on
	// compress/decompress. Captured at first promotion to Warm.
	ecosystem map[blob.Hash]string

	stats Stats
	// counters mirror Stats fields that increment monotonically; using
	// atomics avoids holding the cache mutex on every read.
	hotHits     atomic.Int64
	warmHits    atomic.Int64
	coldMisses  atomic.Int64
	promotions  atomic.Int64
	demotions   atomic.Int64
	admissions  atomic.Int64
	rejections  atomic.Int64
}

type warmEntry struct {
	compressed []byte
	rawSize    int
}

type coldEntry struct {
	sourceURL string
	rawSize   int
}

// Config wires the Cache's collaborators.
type Config struct {
	// Blobs is the underlying blob store the cache layers on top of.
	// Required.
	Blobs *blob.Store

	// HotCapacity is the maximum number of blobs the Hot tier holds.
	// Defaults to defaultHotCapacity. The capacity is in blob count,
	// not bytes; per-blob byte caps are enforced by the underlying
	// blob.Store's budget.
	HotCapacity int

	// Dictionaries optionally supplies the ecosystem dictionary store.
	// When nil, all compression uses the default no-dict encoder.
	Dictionaries *DictionaryStore
}

// New constructs a Cache.
func New(cfg Config) (*Cache, error) {
	if cfg.Blobs == nil {
		return nil, fmt.Errorf("cache: blob store required")
	}
	hotCap := resolveHotCapacity(cfg.HotCapacity)
	dicts := cfg.Dictionaries
	if dicts == nil {
		dicts = NewDictionaryStore()
	}
	return &Cache{
		blobs:     cfg.Blobs,
		dicts:     dicts,
		tinyLFU:   NewTinyLFU(hotCap * tinyLFUOversize),
		policy:    NewS3FIFO(hotCap),
		hotIndex:  make(map[blob.Hash]struct{}),
		warmStore: make(map[blob.Hash]warmEntry),
		coldStore: make(map[blob.Hash]coldEntry),
		ecosystem: make(map[blob.Hash]string),
	}, nil
}

const (
	defaultHotCapacity = 1024
	tinyLFUOversize    = 4 // sketch capacity = hot cap × this; gives stable estimates
)

func resolveHotCapacity(n int) int {
	if n <= 0 {
		return defaultHotCapacity
	}
	return n
}

// Note records that a blob has been Put into the underlying store.
// The cache inserts it into Hot tier and runs the S3-FIFO admission
// rule (which may demote some other blob to Warm).
//
// The ecosystem hint is captured for future compression with the right
// dictionary.
func (c *Cache) Note(hash blob.Hash, ecosystem string) error {
	c.tinyLFU.Increment(hash[:])
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, alreadyHot := c.hotIndex[hash]; alreadyHot {
		return nil
	}
	delete(c.warmStore, hash)
	delete(c.coldStore, hash)
	c.hotIndex[hash] = struct{}{}
	c.ecosystem[hash] = ecosystem
	decision := c.policy.Insert(hash)
	return c.applyDecision(decision)
}

// Touch records a read of the blob. The Cache uses this to:
//   - bump the TinyLFU sketch (drives Cold → Warm admission)
//   - bump the S3-FIFO freq counter (drives Hot tier eviction order)
//   - re-promote from Ghost back to Hot when applicable
func (c *Cache) Touch(hash blob.Hash) error {
	c.tinyLFU.Increment(hash[:])
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, hot := c.hotIndex[hash]; hot {
		c.hotHits.Add(1)
		c.policy.Touch(hash)
		return nil
	}
	if _, warm := c.warmStore[hash]; warm {
		c.warmHits.Add(1)
		decision := c.policy.Touch(hash)
		if !decision.Promoted.IsZero() {
			c.promotions.Add(1)
			return c.promoteWarmToHot(decision.Promoted)
		}
		return nil
	}
	if _, cold := c.coldStore[hash]; cold {
		c.coldMisses.Add(1)
		if c.tinyLFU.AdmitForPromotion(hash[:]) {
			c.admissions.Add(1)
			// Cold-to-Hot direct promotion would require re-fetching
			// the bytes here. The substrate's read path does the
			// re-fetch through provision.Runtime; the cache observes
			// the resulting blobs.Put via Note and gets back into the
			// canonical insertion path.
			return nil
		}
		c.rejections.Add(1)
		return nil
	}
	return ErrNotInCache
}

// DemoteHot demotes the Hot blob to Warm. Called by the cache's
// internal eviction path when S3-FIFO selects a victim. Exported for
// callers that want to manually trigger demotion (e.g. memory pressure
// hooks).
func (c *Cache) DemoteHot(hash blob.Hash) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.demoteHotLocked(hash)
}

func (c *Cache) demoteHotLocked(hash blob.Hash) error {
	if _, hot := c.hotIndex[hash]; !hot {
		return nil
	}
	bytes, err := c.blobs.Bytes(hash)
	if err != nil {
		// Blob isn't in the underlying store; just drop the cache
		// bookkeeping. No tier metadata to persist.
		delete(c.hotIndex, hash)
		c.policy.Remove(hash)
		return nil
	}
	compressed, err := c.dicts.Compress(c.ecosystem[hash], bytes)
	if err != nil {
		return fmt.Errorf("compress for warm: %w", err)
	}
	delete(c.hotIndex, hash)
	c.warmStore[hash] = warmEntry{compressed: compressed, rawSize: len(bytes)}
	c.demotions.Add(1)
	return nil
}

// DemoteWarmToCold drops the compressed bytes for hash and retains
// only the source URL. The blob will need a re-fetch to restore.
func (c *Cache) DemoteWarmToCold(hash blob.Hash, sourceURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.warmStore[hash]
	if !ok {
		return nil
	}
	delete(c.warmStore, hash)
	c.coldStore[hash] = coldEntry{sourceURL: sourceURL, rawSize: entry.rawSize}
	c.demotions.Add(1)
	return nil
}

// promoteWarmToHot decompresses the Warm bytes into a fresh Put on
// the underlying blob store. The blob.Store's Put deduplicates against
// any existing identical content (which is the same thing — same hash
// guarantees same bytes), so the call is idempotent.
func (c *Cache) promoteWarmToHot(hash blob.Hash) error {
	entry, ok := c.warmStore[hash]
	if !ok {
		return nil
	}
	bytes, err := c.dicts.Decompress(c.ecosystem[hash], entry.compressed)
	if err != nil {
		return fmt.Errorf("decompress for promote: %w", err)
	}
	if _, err := c.blobs.Put(bytes); err != nil {
		return fmt.Errorf("re-put after promote: %w", err)
	}
	delete(c.warmStore, hash)
	c.hotIndex[hash] = struct{}{}
	return nil
}

// Forget removes the blob from all tier bookkeeping. Called when the
// underlying blob's refcount drops to zero — the blob no longer exists
// so cache state for it would be stale.
func (c *Cache) Forget(hash blob.Hash) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.hotIndex, hash)
	delete(c.warmStore, hash)
	delete(c.coldStore, hash)
	delete(c.ecosystem, hash)
	c.policy.Remove(hash)
}

// TierOf reports the current tier of the blob, or TierUnknown when
// the cache has no record.
func (c *Cache) TierOf(hash blob.Hash) Tier {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if _, ok := c.hotIndex[hash]; ok {
		return TierHot
	}
	if _, ok := c.warmStore[hash]; ok {
		return TierWarm
	}
	if _, ok := c.coldStore[hash]; ok {
		return TierCold
	}
	return TierUnknown
}

// Stat takes a snapshot of cache contents and counters.
func (c *Cache) Stat() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := Stats{
		HotBlobCount:  int64(len(c.hotIndex)),
		WarmBlobCount: int64(len(c.warmStore)),
		ColdBlobCount: int64(len(c.coldStore)),
		HotHits:       c.hotHits.Load(),
		WarmHits:      c.warmHits.Load(),
		ColdMisses:    c.coldMisses.Load(),
		Promotions:    c.promotions.Load(),
		Demotions:     c.demotions.Load(),
		Admissions:    c.admissions.Load(),
		Rejections:    c.rejections.Load(),
	}
	for _, e := range c.warmStore {
		out.WarmBytes += int64(len(e.compressed))
		out.UncompressedBytesAtRest += int64(e.rawSize)
	}
	return out
}

// applyDecision processes the S3-FIFO recommendation that resulted
// from a Note or Touch. Demotes evicted blobs to Warm; demotes
// ghost-evicted blobs to Cold (with empty source URL — the cache
// caller is responsible for supplying the URL via a separate
// DemoteWarmToCold call when full eviction is appropriate).
func (c *Cache) applyDecision(decision S3FIFODecision) error {
	if !decision.Evicted.IsZero() {
		if err := c.demoteHotLocked(decision.Evicted); err != nil {
			return err
		}
	}
	if !decision.EvictedFromGhost.IsZero() {
		// Ghost-eviction: the blob hadn't been touched recently enough
		// to come back to Hot. Drop the Warm representation; the cache
		// caller can later re-promote it from a re-fetched Put.
		entry, ok := c.warmStore[decision.EvictedFromGhost]
		if ok {
			c.coldStore[decision.EvictedFromGhost] = coldEntry{rawSize: entry.rawSize}
			delete(c.warmStore, decision.EvictedFromGhost)
		}
	}
	return nil
}
