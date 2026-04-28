package purevfs

import (
	"errors"
	"fmt"
	"sync"
)

// chunkArena is the off-heap, RAM-only backing store for ChunkStore bytes.
// Allocations are routed to size-classed slabs; freed slots return to a
// per-class free-list and are reused on the next allocation that fits.
//
// On unix builds the slab memory comes from anonymous mmap (MAP_ANON |
// MAP_PRIVATE, no fd) — RAM-backed virtual memory invisible to the Go GC.
// This honors the VFS's strict no-disk-write rule (anonymous mmap never
// emits a write syscall) while removing chunk bytes from the live heap so
// they neither pay GC scan cost nor inflate heap profile inuse_space.
//
// On windows / unsupported platforms the slab memory comes from the Go
// heap (a single make([]byte, slabSize) per slab). Functionally identical;
// loses the off-heap GC win but keeps the dedup/free-list machinery intact.
//
// Size classes are derived from the codebase's chunk-size anchor
// (NewChunkedFileBody default = 64 KiB) — class boundaries cover the
// expected distribution of full and tail chunks; anything larger than the
// top class falls back to a one-shot mmap region sized to the request.
//
// Concurrency: each size class has its own mutex so writers across
// different classes do not contend; allocators within a class share that
// class's lock. The arena's slab list grows under the class lock.

var (
	errArenaSlotInvalid = errors.New("purevfs: arena slot is invalid")
	errArenaSlotTooBig  = errors.New("purevfs: arena slot exceeds class capacity")
)

// arenaSizeClassCount is the number of size classes; matches the length
// of arenaSizeClasses below. Used as the array bound on chunkArena so
// the class table is allocated inline with the arena struct.
const arenaSizeClassCount = 5

// arenaSizeClasses defines slot sizes the arena can serve from a slab.
// The top class matches the typical chunking unit (64 KiB). Tail chunks
// fall through to the smallest class that fits. Any allocation larger
// than the top class is served from a dedicated single-slot region.
//
// Slab size per class is computed at construction so each slab fits a
// fixed slot count (slabSlotsPerClass), keeping per-class slab metadata
// proportional to active class usage rather than a global literal.
var arenaSizeClasses = [arenaSizeClassCount]int{
	1 << 12, // 4 KiB
	1 << 14, // 16 KiB
	1 << 16, // 64 KiB (matches default chunk size)
	1 << 18, // 256 KiB (covers larger chunk-size configs)
	1 << 20, // 1 MiB
}

// slabSlotsPerClass is the number of slots packed into a single slab for
// every size class. Picked so a fresh class allocates one slab worth of
// virtual memory up-front; further demand grows by adding more slabs.
// Derived from the anchor "fits a small write burst without growing": at
// 16 slots a 64 KiB class slab is 1 MiB, which absorbs the default
// chunking pass for a multi-megabyte file before it must extend.
const slabSlotsPerClass = 16

// chunkSlot is the durable identifier for an allocation in the arena.
// It points to a region of bytes that lives until Free is called.
//
// The bytes returned by Bytes are a slice into the arena slab; mutating
// them is permitted ONLY between Allocate and the slot being committed
// to ChunkStore (i.e. before another goroutine can observe the chunk via
// its content hash). Once committed, content-addressed chunks are
// immutable by contract; mutation would corrupt the dedup invariant.
type chunkSlot struct {
	class    int    // index into arenaSizeClasses, or -1 for oversize
	slabIdx  int    // index of the slab in class.slabs (or oversize.regions)
	slotIdx  int    // index of the slot within the slab
	size     int    // logical byte length used inside the slot
	capacity int    // physical slot capacity (>= size, == class size or rounded)
	bytes    []byte // slice into the slab covering [0:size] of the slot's bytes
}

// Bytes returns the writable byte slice the slot occupies. Length equals
// the size requested at Allocate time; capacity equals the class slot
// capacity. After Free the slice is invalid and must not be accessed.
func (s *chunkSlot) Bytes() []byte {
	if s == nil {
		return nil
	}
	return s.bytes
}

// Size returns the logical length of bytes stored in the slot.
func (s *chunkSlot) Size() int {
	if s == nil {
		return 0
	}
	return s.size
}

// chunkArena owns the size-classed slabs that back chunk bytes. A single
// arena instance serves one ChunkStore.
type chunkArena struct {
	classes  [arenaSizeClassCount]*arenaSizeClass
	oversize *oversizeArena
	stats    arenaStats
}

type arenaStats struct {
	mu         sync.Mutex
	allocBytes int64 // sum of slab capacities currently mapped
	liveBytes  int64 // sum of slot sizes currently allocated (== ChunkStore.bytes)
	slabCount  int   // total slabs across all classes (excluding oversize)
	oversize   int64 // bytes mapped in oversize regions
}

func newChunkArena() *chunkArena {
	a := &chunkArena{oversize: newOversizeArena()}
	for i, slotSize := range arenaSizeClasses {
		a.classes[i] = newArenaSizeClass(i, slotSize, slabSlotsPerClass)
	}
	return a
}

// Allocate returns a slot able to hold size bytes. Caller must initialize
// slot.Bytes() (typically via copy) and either commit the slot to a
// ChunkStore (which assumes ownership of the eventual Free) or call Free
// directly to return the slot.
func (a *chunkArena) Allocate(size int) (*chunkSlot, error) {
	if size <= 0 {
		return &chunkSlot{class: -2, size: 0, bytes: nil}, nil
	}
	classIdx := a.pickClass(size)
	if classIdx < 0 {
		// Oversize path — one mmap region per allocation, freed on release.
		region, err := a.oversize.allocate(size)
		if err != nil {
			return nil, err
		}
		a.stats.recordAlloc(size, 0, 0)
		a.stats.recordOversize(int64(region.capacity))
		return &chunkSlot{
			class:    -1,
			slabIdx:  region.slabIdx,
			size:     size,
			capacity: region.capacity,
			bytes:    region.bytes[:size],
		}, nil
	}
	cls := a.classes[classIdx]
	slot, grew, err := cls.allocate(size)
	if err != nil {
		return nil, err
	}
	delta := int64(0)
	if grew {
		delta = int64(cls.slabCapacity())
	}
	a.stats.recordAlloc(size, delta, boolToInt(grew))
	return slot, nil
}

// Free returns slot's storage to the arena. It is safe to call Free on a
// nil or zero-size slot. Slots must not be used after Free.
func (a *chunkArena) Free(slot *chunkSlot) {
	if slot == nil || slot.bytes == nil {
		return
	}
	switch {
	case slot.class == -1:
		a.oversize.release(slot.slabIdx)
		a.stats.recordFree(slot.size, 0, 0)
		a.stats.recordOversize(-int64(slot.capacity))
	case slot.class >= 0 && slot.class < len(a.classes):
		a.classes[slot.class].release(slot)
		a.stats.recordFree(slot.size, 0, 0)
	}
	slot.bytes = nil
}

// LiveBytes returns the sum of bytes stored in committed slots. Matches
// the public Bytes counter on ChunkStore for cross-checking.
func (a *chunkArena) LiveBytes() int64 {
	a.stats.mu.Lock()
	defer a.stats.mu.Unlock()
	return a.stats.liveBytes
}

// MappedBytes returns the total virtual memory mapped by the arena
// (sum of all slab capacities + oversize regions). Useful for telemetry
// to detect arena growth pressure.
func (a *chunkArena) MappedBytes() int64 {
	a.stats.mu.Lock()
	defer a.stats.mu.Unlock()
	return a.stats.allocBytes + a.stats.oversize
}

// pickClass returns the index of the smallest size class that fits size,
// or -1 if size exceeds every class (caller falls back to oversize).
func (a *chunkArena) pickClass(size int) int {
	for i, slot := range arenaSizeClasses {
		if size <= slot {
			return i
		}
	}
	return -1
}

// arenaSizeClass tracks one tier of the arena: a uniform slot size and a
// list of slabs that have been mapped on demand. Free slots are tracked
// by (slabIdx, slotIdx) pairs; allocations consume from the head.
type arenaSizeClass struct {
	classIdx  int
	slotSize  int
	slabSlots int

	mu      sync.Mutex
	slabs   []arenaSlab
	freeIdx []slotRef
}

type arenaSlab struct {
	bytes  []byte // backing memory (mmap or heap depending on platform)
	mapped bool   // true if bytes came from anonymous mmap
}

type slotRef struct {
	slabIdx int
	slotIdx int
}

func newArenaSizeClass(classIdx, slotSize, slabSlots int) *arenaSizeClass {
	return &arenaSizeClass{
		classIdx:  classIdx,
		slotSize:  slotSize,
		slabSlots: slabSlots,
	}
}

func (c *arenaSizeClass) slabCapacity() int {
	return c.slotSize * c.slabSlots
}

func (c *arenaSizeClass) allocate(size int) (*chunkSlot, bool, error) {
	if size > c.slotSize {
		return nil, false, errArenaSlotTooBig
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	grew := false
	if len(c.freeIdx) == 0 {
		if err := c.growLocked(); err != nil {
			return nil, false, err
		}
		grew = true
	}
	ref := c.freeIdx[len(c.freeIdx)-1]
	c.freeIdx = c.freeIdx[:len(c.freeIdx)-1]
	slab := &c.slabs[ref.slabIdx]
	start := ref.slotIdx * c.slotSize
	return &chunkSlot{
		class:    c.classIdx,
		slabIdx:  ref.slabIdx,
		slotIdx:  ref.slotIdx,
		size:     size,
		capacity: c.slotSize,
		bytes:    slab.bytes[start : start+size : start+c.slotSize],
	}, grew, nil
}

func (c *arenaSizeClass) release(slot *chunkSlot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slot.slabIdx < 0 || slot.slabIdx >= len(c.slabs) {
		return
	}
	c.freeIdx = append(c.freeIdx, slotRef{slabIdx: slot.slabIdx, slotIdx: slot.slotIdx})
}

func (c *arenaSizeClass) growLocked() error {
	cap := c.slabCapacity()
	bytes, mapped, err := allocSlabMemory(cap)
	if err != nil {
		return fmt.Errorf("purevfs: arena class %d grow: %w", c.classIdx, err)
	}
	idx := len(c.slabs)
	c.slabs = append(c.slabs, arenaSlab{bytes: bytes, mapped: mapped})
	for slot := c.slabSlots - 1; slot >= 0; slot-- {
		c.freeIdx = append(c.freeIdx, slotRef{slabIdx: idx, slotIdx: slot})
	}
	return nil
}

// oversizeArena handles single-slot allocations for sizes that exceed
// the largest size class. Each region is mapped on demand and freed on
// release; we keep a sparse slice of regions so freed slots leave a hole
// rather than churn slice indexes (the slab index in chunkSlot must
// remain stable until Free).
type oversizeArena struct {
	mu       sync.Mutex
	regions  []*oversizeRegion
	freeIdxs []int // free indexes in regions slice
}

type oversizeRegion struct {
	bytes    []byte
	mapped   bool
	capacity int
	slabIdx  int
}

func newOversizeArena() *oversizeArena {
	return &oversizeArena{}
}

func (o *oversizeArena) allocate(size int) (*oversizeRegion, error) {
	bytes, mapped, err := allocSlabMemory(size)
	if err != nil {
		return nil, fmt.Errorf("purevfs: oversize allocate %d: %w", size, err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	region := &oversizeRegion{bytes: bytes, mapped: mapped, capacity: size}
	if len(o.freeIdxs) > 0 {
		idx := o.freeIdxs[len(o.freeIdxs)-1]
		o.freeIdxs = o.freeIdxs[:len(o.freeIdxs)-1]
		region.slabIdx = idx
		o.regions[idx] = region
		return region, nil
	}
	region.slabIdx = len(o.regions)
	o.regions = append(o.regions, region)
	return region, nil
}

func (o *oversizeArena) release(idx int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if idx < 0 || idx >= len(o.regions) {
		return
	}
	region := o.regions[idx]
	if region == nil {
		return
	}
	o.regions[idx] = nil
	o.freeIdxs = append(o.freeIdxs, idx)
	releaseSlabMemory(region.bytes, region.mapped)
}

func (s *arenaStats) recordAlloc(liveDelta int, mappedDelta int64, slabsAdded int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.liveBytes += int64(liveDelta)
	s.allocBytes += mappedDelta
	s.slabCount += slabsAdded
}

func (s *arenaStats) recordFree(liveDelta int, mappedDelta int64, slabsRemoved int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.liveBytes -= int64(liveDelta)
	s.allocBytes -= mappedDelta
	s.slabCount -= slabsRemoved
}

func (s *arenaStats) recordOversize(delta int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oversize += delta
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
