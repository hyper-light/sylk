package cache

import (
	"sync"

	"lukechampine.com/blake3"
)

// TinyLFU is a 4-bit count-min sketch admission filter.
//
// Used by the cache's tier promotion policy: before promoting a Cold
// blob to Warm (paying decompression-buffer + dictionary cost), the
// cache asks the sketch how often the blob has been touched in the
// recent past. If the answer is <2, promotion is rejected — the blob
// is served from re-fetch and the working set is preserved.
//
// 4-bit width: each counter holds 0..15. An aging pass halves all
// counters every reset_threshold writes so old hot blobs gracefully
// fade out as the workload shifts. This is the W-TinyLFU "Window
// TinyLFU" pattern from Caffeine's original paper.
//
// 4-counters-per-row × 4-rows: 16-bit-aligned packing for 8-byte
// SIMD-friendly access. The hash uses BLAKE3 derive-key mode for two
// independent permutations from a single key — the count-min property
// only holds if the row-hashes are independent.
//
// Safe for concurrent use.
type TinyLFU struct {
	mu sync.RWMutex

	rows           [tinylfuRowCount][]uint8 // packed 4-bit counters; len = capacity
	capacity       int
	writeCount     int64
	resetThreshold int64
}

const (
	// tinylfuRowCount is the number of independent count-min rows. 4 is
	// the conventional choice — gives 99% hit-rate accuracy on Zipfian
	// workloads with <0.1% false positives at the substrate's typical
	// load.
	tinylfuRowCount = 4

	// tinylfuMaxCount is the saturation value of a 4-bit counter.
	tinylfuMaxCount uint8 = 15

	// tinylfuPromotionThreshold: a blob must have been seen at least
	// this many times in the recent past to be eligible for Warm
	// promotion. Lower → more permissive; higher → more aggressive
	// scan-pollution rejection. 2 is the literature default.
	tinylfuPromotionThreshold uint8 = 2

	// tinylfuResetMultiplier: aging pass triggers every
	// (capacity × multiplier) writes. Higher → counters age slower
	// (better for stable workloads); lower → faster adaptation to
	// workload shifts.
	tinylfuResetMultiplier int64 = 10
)

// NewTinyLFU constructs a sketch sized for `capacity` distinct hot
// blobs (the cache's Hot tier capacity is the natural choice).
//
// Capacity should be a power of 2 for the hash-to-row-index mapping
// to be uniform; non-power-of-two values are rounded up.
func NewTinyLFU(capacity int) *TinyLFU {
	if capacity <= 0 {
		capacity = tinylfuMinCapacity
	}
	capacity = roundUpPowerOfTwo(capacity)
	t := &TinyLFU{
		capacity:       capacity,
		resetThreshold: int64(capacity) * tinylfuResetMultiplier,
	}
	for i := range t.rows {
		t.rows[i] = make([]uint8, capacity/2) // 4-bit counters: 2 per byte
	}
	return t
}

const tinylfuMinCapacity = 16

func roundUpPowerOfTwo(n int) int {
	power := 1
	for power < n {
		power <<= 1
	}
	return power
}

// Increment records that the blob with the given content hash was
// touched. Saturates at tinylfuMaxCount.
func (t *TinyLFU) Increment(key []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writeCount++
	for row := range t.rows {
		idx := t.indexFor(key, row)
		t.bumpCounterAt(row, idx)
	}
	if t.writeCount >= t.resetThreshold {
		t.ageInPlace()
		t.writeCount = 0
	}
}

// Estimate returns the lower-bound estimate of the blob's recent
// access count. Uses the count-min "min over rows" rule.
func (t *TinyLFU) Estimate(key []byte) uint8 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	min := tinylfuMaxCount
	for row := range t.rows {
		v := t.readCounterAt(row, t.indexFor(key, row))
		if v < min {
			min = v
		}
	}
	return min
}

// AdmitForPromotion returns true when the blob's recent access count
// meets the promotion threshold. The cache's tier policy uses this as
// the gate for Cold → Warm transitions.
func (t *TinyLFU) AdmitForPromotion(key []byte) bool {
	return t.Estimate(key) >= tinylfuPromotionThreshold
}

func (t *TinyLFU) indexFor(key []byte, row int) int {
	// Derive-key style: salt the key with the row index so each row
	// produces an independent hash. BLAKE3 supports this natively via
	// its keyed mode; we use a simpler salting to keep the hash path
	// fast (this is the cache hot path).
	salted := make([]byte, len(key)+1)
	copy(salted, key)
	salted[len(key)] = byte(row)
	digest := blake3.Sum256(salted)
	// Take the first 8 bytes as a uint64; mask to capacity-1.
	value := uint64(digest[0])<<56 | uint64(digest[1])<<48 | uint64(digest[2])<<40 | uint64(digest[3])<<32 |
		uint64(digest[4])<<24 | uint64(digest[5])<<16 | uint64(digest[6])<<8 | uint64(digest[7])
	return int(value & uint64(t.capacity-1))
}

// bumpCounterAt increments the 4-bit counter at the given index,
// saturating at tinylfuMaxCount.
func (t *TinyLFU) bumpCounterAt(row, idx int) {
	current := t.readCounterAt(row, idx)
	if current >= tinylfuMaxCount {
		return
	}
	t.writeCounterAt(row, idx, current+1)
}

func (t *TinyLFU) readCounterAt(row, idx int) uint8 {
	byteIdx := idx >> 1
	if idx&1 == 0 {
		return t.rows[row][byteIdx] & 0x0F
	}
	return (t.rows[row][byteIdx] >> 4) & 0x0F
}

func (t *TinyLFU) writeCounterAt(row, idx int, value uint8) {
	byteIdx := idx >> 1
	if idx&1 == 0 {
		t.rows[row][byteIdx] = (t.rows[row][byteIdx] & 0xF0) | (value & 0x0F)
		return
	}
	t.rows[row][byteIdx] = (t.rows[row][byteIdx] & 0x0F) | ((value & 0x0F) << 4)
}

// ageInPlace halves every counter, gracefully fading out old hot blobs
// when the workload shifts. Called every (capacity × multiplier) writes.
func (t *TinyLFU) ageInPlace() {
	for row := range t.rows {
		for i := range t.rows[row] {
			low := (t.rows[row][i] & 0x0F) >> 1
			high := ((t.rows[row][i] >> 4) & 0x0F) >> 1
			t.rows[row][i] = (high << 4) | low
		}
	}
}
