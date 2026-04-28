package purevfs

import (
	"bytes"
	"crypto/rand"
	"sync"
	"testing"
)

// These tests pin the off-heap chunk arena's correctness contract:
//   - allocate/free returns slots that hold the right bytes
//   - freed slots are reused (no growth-only behavior)
//   - size classes route by ceiling, not floor
//   - oversize allocations bypass the class table cleanly
//   - concurrent allocate/free is safe under -race
//
// Together with chunk_store_arena_test.go (the integration tests against
// ChunkStore.Put/Get/Release) these prove A+B+C is correct end-to-end.

func TestChunkArena_AllocateFreeRoundTrip(t *testing.T) {
	a := newChunkArena()
	slot, err := a.Allocate(100)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if slot.Size() != 100 {
		t.Fatalf("size = %d, want 100", slot.Size())
	}
	if len(slot.Bytes()) != 100 {
		t.Fatalf("len(bytes) = %d, want 100", len(slot.Bytes()))
	}
	for i := range slot.Bytes() {
		slot.Bytes()[i] = byte(i % 251)
	}
	for i, b := range slot.Bytes() {
		if b != byte(i%251) {
			t.Fatalf("byte %d = %d, want %d", i, b, byte(i%251))
		}
	}
	a.Free(slot)
	if a.LiveBytes() != 0 {
		t.Fatalf("live bytes after free = %d, want 0", a.LiveBytes())
	}
}

func TestChunkArena_PicksSmallestFittingClass(t *testing.T) {
	cases := []struct {
		size      int
		wantClass int
	}{
		{1, 0},                  // smallest class
		{1 << 12, 0},            // exact 4 KiB
		{(1 << 12) + 1, 1},      // one over → next class
		{1 << 14, 1},            // exact 16 KiB
		{1 << 16, 2},            // exact 64 KiB (default chunk size)
		{1 << 20, 4},            // exact 1 MiB (top class)
		{(1 << 20) + 1, -1},     // oversize
	}
	for _, tc := range cases {
		a := newChunkArena()
		got := a.pickClass(tc.size)
		if got != tc.wantClass {
			t.Fatalf("pickClass(%d) = %d, want %d", tc.size, got, tc.wantClass)
		}
	}
}

func TestChunkArena_SlotReuseAfterFree(t *testing.T) {
	a := newChunkArena()
	first, err := a.Allocate(1000)
	if err != nil {
		t.Fatalf("allocate first: %v", err)
	}
	firstClass := first.class
	firstSlab := first.slabIdx
	firstSlot := first.slotIdx
	a.Free(first)

	// Next allocation in the same class should reuse the freed slot.
	second, err := a.Allocate(1000)
	if err != nil {
		t.Fatalf("allocate second: %v", err)
	}
	if second.class != firstClass {
		t.Fatalf("second class = %d, want %d", second.class, firstClass)
	}
	if second.slabIdx != firstSlab || second.slotIdx != firstSlot {
		t.Fatalf("slot not reused: first (%d,%d) vs second (%d,%d)",
			firstSlab, firstSlot, second.slabIdx, second.slotIdx)
	}
	a.Free(second)
}

func TestChunkArena_ZeroSizeAllocateReturnsEmpty(t *testing.T) {
	a := newChunkArena()
	slot, err := a.Allocate(0)
	if err != nil {
		t.Fatalf("allocate(0): %v", err)
	}
	if slot.Size() != 0 || len(slot.Bytes()) != 0 {
		t.Fatalf("size = %d, len = %d, want 0/0", slot.Size(), len(slot.Bytes()))
	}
	// Free of a zero slot is a no-op; must not panic.
	a.Free(slot)
}

func TestChunkArena_OversizeAllocationFreesIndependently(t *testing.T) {
	a := newChunkArena()
	// 2 MiB exceeds the 1 MiB top class.
	const size = 2 << 20
	slot, err := a.Allocate(size)
	if err != nil {
		t.Fatalf("oversize allocate: %v", err)
	}
	if slot.class != -1 {
		t.Fatalf("class = %d, want -1 (oversize)", slot.class)
	}
	if slot.Size() != size {
		t.Fatalf("size = %d, want %d", slot.Size(), size)
	}
	a.Free(slot)
	if a.LiveBytes() != 0 {
		t.Fatalf("live bytes after oversize free = %d, want 0", a.LiveBytes())
	}
}

func TestChunkArena_SlabGrowsWhenAllSlotsTaken(t *testing.T) {
	a := newChunkArena()
	// Allocate slabSlotsPerClass + 1 slots in a single class — forces growth.
	const allocSize = 1 << 11 // routes to 4 KiB class
	slots := make([]*chunkSlot, 0, slabSlotsPerClass+1)
	for i := 0; i < slabSlotsPerClass+1; i++ {
		s, err := a.Allocate(allocSize)
		if err != nil {
			t.Fatalf("allocate %d: %v", i, err)
		}
		slots = append(slots, s)
	}
	cls := a.classes[0]
	if len(cls.slabs) < 2 {
		t.Fatalf("class did not grow: slabs = %d, want >= 2", len(cls.slabs))
	}
	for _, s := range slots {
		a.Free(s)
	}
}

func TestChunkArena_ConcurrentAllocateFreeUnderRace(t *testing.T) {
	a := newChunkArena()
	const goroutines = 32
	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int) {
			defer wg.Done()
			held := make([]*chunkSlot, 0, 8)
			for i := 0; i < iterations; i++ {
				size := 64 + (seed*17+i)%4000
				slot, err := a.Allocate(size)
				if err != nil {
					t.Errorf("allocate: %v", err)
					return
				}
				slot.Bytes()[0] = byte(seed)
				held = append(held, slot)
				if len(held) >= 8 {
					for _, s := range held {
						a.Free(s)
					}
					held = held[:0]
				}
			}
			for _, s := range held {
				a.Free(s)
			}
		}(g)
	}
	wg.Wait()
	if a.LiveBytes() != 0 {
		t.Fatalf("live bytes after concurrent test = %d, want 0", a.LiveBytes())
	}
}

// ChunkStore-level integration tests below pin the dedup + arena
// integration: same content → single slot; refcount drives slot release.

func TestChunkStore_PutDedupesIdenticalContent(t *testing.T) {
	store := NewChunkStore(0)
	content := bytes.Repeat([]byte("dedup-me"), 1000)

	h1, hit1, err := store.Put(content)
	if err != nil {
		t.Fatalf("put 1: %v", err)
	}
	if hit1 {
		t.Fatal("first put reported dedup hit")
	}

	h2, hit2, err := store.Put(content)
	if err != nil {
		t.Fatalf("put 2: %v", err)
	}
	if !hit2 {
		t.Fatal("second put did not report dedup hit")
	}
	if h1 != h2 {
		t.Fatalf("hashes differ: %s vs %s", h1, h2)
	}
	if got := store.RefCount(h1); got != 2 {
		t.Fatalf("refcount = %d, want 2", got)
	}
	stats := store.Stats()
	if stats.Chunks != 1 {
		t.Fatalf("chunks = %d, want 1", stats.Chunks)
	}
	if stats.Bytes != int64(len(content)) {
		t.Fatalf("bytes = %d, want %d", stats.Bytes, len(content))
	}
}

func TestChunkStore_ReleaseFreesSlotOnRefZero(t *testing.T) {
	store := NewChunkStore(0)
	content := []byte("release-me")
	hash, _, err := store.Put(content)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	mappedBefore := store.ArenaMappedBytes()
	if err := store.Release(hash); err != nil {
		t.Fatalf("release: %v", err)
	}
	if store.Has(hash) {
		t.Fatal("hash still present after final release")
	}
	if got := store.Stats().Bytes; got != 0 {
		t.Fatalf("bytes after release = %d, want 0", got)
	}
	// Mapped bytes do NOT shrink on release (slabs remain mapped for
	// reuse). The freed slot must still be reusable, though.
	if mappedBefore == 0 {
		t.Fatal("expected non-zero mapped bytes before release")
	}
	// Reallocation should not need a fresh slab.
	_, _, err = store.Put(content)
	if err != nil {
		t.Fatalf("reput: %v", err)
	}
	if mappedAfter := store.ArenaMappedBytes(); mappedAfter != mappedBefore {
		t.Fatalf("mapped grew unexpectedly: %d → %d", mappedBefore, mappedAfter)
	}
}

func TestChunkStore_GetAndCopyIntoReturnExactBytes(t *testing.T) {
	store := NewChunkStore(0)
	content := make([]byte, 8192)
	if _, err := rand.Read(content); err != nil {
		t.Fatalf("rand: %v", err)
	}
	hash, _, err := store.Put(content)
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := store.Get(hash)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("Get returned different bytes")
	}

	dst := make([]byte, 4096)
	n, err := store.CopyInto(dst, hash, 1024)
	if err != nil {
		t.Fatalf("copy into: %v", err)
	}
	if n != 4096 {
		t.Fatalf("copy n = %d, want 4096", n)
	}
	if !bytes.Equal(dst, content[1024:1024+4096]) {
		t.Fatalf("CopyInto returned wrong slice")
	}

	// Boundary: requesting more than remaining returns the remainder only.
	tail := make([]byte, 5000)
	n, err = store.CopyInto(tail, hash, 5000)
	if err != nil {
		t.Fatalf("copy tail: %v", err)
	}
	if n != len(content)-5000 {
		t.Fatalf("tail n = %d, want %d", n, len(content)-5000)
	}
	if !bytes.Equal(tail[:n], content[5000:]) {
		t.Fatalf("tail copy mismatch")
	}
}

func TestChunkStore_ConcurrentPutGetReleaseUnderRace(t *testing.T) {
	store := NewChunkStore(0)
	const writers = 16
	const items = 200
	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < items; i++ {
				content := bytes.Repeat([]byte{byte(seed), byte(i), byte(seed ^ i)}, 100+i)
				hash, _, err := store.Put(content)
				if err != nil {
					t.Errorf("put: %v", err)
					return
				}
				dst := make([]byte, len(content))
				if _, err := store.CopyInto(dst, hash, 0); err != nil {
					t.Errorf("copy: %v", err)
					return
				}
				if !bytes.Equal(dst, content) {
					t.Errorf("content mismatch")
					return
				}
				if err := store.Release(hash); err != nil {
					t.Errorf("release: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if got := store.Stats().Bytes; got != 0 {
		t.Fatalf("residual bytes = %d, want 0", got)
	}
	if got := store.Stats().Chunks; got != 0 {
		t.Fatalf("residual chunks = %d, want 0", got)
	}
}

func TestChunkStore_MemoryLimitRejectsOversizedPut(t *testing.T) {
	store := NewChunkStore(1024)
	if _, _, err := store.Put(bytes.Repeat([]byte("x"), 512)); err != nil {
		t.Fatalf("put 512: %v", err)
	}
	// This put would exceed the 1024-byte limit.
	_, _, err := store.Put(bytes.Repeat([]byte("y"), 1024))
	if err != ErrMemoryPressure {
		t.Fatalf("err = %v, want ErrMemoryPressure", err)
	}
	if got := store.Stats().RejectedPutCount; got != 1 {
		t.Fatalf("rejected = %d, want 1", got)
	}
}

func BenchmarkChunkStore_PutUniqueChunks(b *testing.B) {
	store := NewChunkStore(0)
	content := make([]byte, 64<<10)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		content[0] = byte(i)
		content[1] = byte(i >> 8)
		content[2] = byte(i >> 16)
		_, _, err := store.Put(content)
		if err != nil {
			b.Fatalf("put: %v", err)
		}
	}
}

func BenchmarkChunkStore_PutDedupedChunks(b *testing.B) {
	store := NewChunkStore(0)
	content := bytes.Repeat([]byte("identical"), 7281) // ~64 KiB
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, err := store.Put(content)
		if err != nil {
			b.Fatalf("put: %v", err)
		}
	}
}

func BenchmarkChunkStore_CopyInto(b *testing.B) {
	store := NewChunkStore(0)
	content := bytes.Repeat([]byte("benchmark"), 7281)
	hash, _, _ := store.Put(content)
	dst := make([]byte, 64<<10)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := store.CopyInto(dst, hash, 0)
		if err != nil {
			b.Fatalf("copy: %v", err)
		}
	}
}
