// Package cache implements the substrate's tiered blob storage.
//
// Three orthogonal mechanisms govern blob storage cost:
//
//   1. Refcount-driven existence (in core/substrate/blob): a blob with
//      refcount zero is deterministically dropped — content-addressing
//      means a re-fetch is bit-identical to the original.
//
//   2. S3-FIFO-driven representation tiering (this package): while
//      refcount > 0, blobs live in one of three tiers — Hot
//      (uncompressed `[]byte`, served via FUSE passthrough), Warm
//      (zstd-compressed in RAM with a per-ecosystem trained
//      dictionary), Cold (manifest-only — bytes evicted, source URL
//      retained for re-fetch). S3-FIFO governs Hot↔Warm↔Cold
//      transitions; the small/main/ghost queues map onto recently-
//      promoted/stable-hot/recently-demoted respectively.
//
//   3. TinyLFU admission control (this package): a 4-bit count-min
//      sketch governs Cold → Warm promotions. Touched <2 times in the
//      last 1000 reads → don't promote, serve from re-fetch. Kills
//      the failure mode where an agent does `glob('**/*.py')` over
//      the substrate and trashes the working set.
//
// S3-FIFO is the SOTA cache eviction algorithm shipped in production
// at Google, VMware, Redpanda. It beats LRU/LFU/TinyLFU on real
// workloads at lower implementation complexity. The substrate's
// workload is bursty + scan-heavy (test runs touch many blobs in
// rapid succession then idle); S3-FIFO's small/ghost queues are
// explicitly designed to resist scan pollution.
//
// Reference: Yang et al., "FIFO Queues are All You Need for Cache
// Eviction", SOSP 2023.
package cache

import (
	"errors"

	"github.com/adalundhe/sylk/core/substrate/blob"
)

// Tier names a blob's representation tier.
type Tier int

const (
	// TierUnknown is the zero value, returned when a blob hash is not
	// known to the cache.
	TierUnknown Tier = iota

	// TierHot is the uncompressed []byte representation. Reads are
	// nanosecond-scale; the FUSE projection serves directly out of
	// the underlying byte slice.
	TierHot

	// TierWarm is zstd-compressed bytes with a per-ecosystem trained
	// dictionary. Reads decompress on demand into a temporary buffer
	// (microsecond cost).
	TierWarm

	// TierCold is manifest-only — the cache retains the blob hash and
	// source URL but not the bytes. Reads must re-fetch from the
	// original source. Substrate WAL replay restores the manifest
	// metadata on process restart.
	TierCold
)

// String returns the tier's human name.
func (t Tier) String() string {
	switch t {
	case TierHot:
		return "hot"
	case TierWarm:
		return "warm"
	case TierCold:
		return "cold"
	}
	return "unknown"
}

// Stats summarizes the cache's contents at a point in time.
type Stats struct {
	HotBlobCount    int64
	WarmBlobCount   int64
	ColdBlobCount   int64
	HotBytes        int64 // sum of uncompressed bytes in Hot tier
	WarmBytes       int64 // sum of compressed bytes in Warm tier
	UncompressedBytesAtRest int64 // sum of pre-compression sizes for Warm blobs

	// Counters since cache creation.
	HotHits     int64
	WarmHits    int64
	ColdMisses  int64 // Cold reads that had to re-fetch
	Promotions  int64 // Warm/Cold → Hot transitions
	Demotions   int64 // Hot → Warm and Warm → Cold transitions
	Admissions  int64 // TinyLFU-admitted Cold → Warm promotions
	Rejections  int64 // TinyLFU-rejected Cold → Warm promotions
}

// Errors.
var (
	// ErrNotInCache: the requested blob hash is not in any tier.
	ErrNotInCache = errors.New("cache: blob not in cache")

	// ErrTierFull: a promotion was rejected because the target tier
	// was at capacity and no eviction candidate could be selected.
	// Should not happen in normal operation; surfaced for diagnostics.
	ErrTierFull = errors.New("cache: tier at capacity, no eviction candidate")

	// ErrInvalidConfig: cache configuration was invalid (e.g. zero or
	// negative tier capacity).
	ErrInvalidConfig = errors.New("cache: invalid config")
)

// nopBlobHash is a sentinel value used internally where a blob.Hash is
// required but the operation does not care about the value. Defined as
// a package-level so the indirection is free.
var nopBlobHash = blob.Hash{}
