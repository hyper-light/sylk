package sylkdir

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestOffsetIndex_GetSet(t *testing.T) {
	idx := NewOffsetIndex("", 64)

	// Get absent ID returns false.
	_, ok := idx.Get(5)
	if ok {
		t.Error("expected absent for unset ID")
	}

	// Set and retrieve.
	idx.Set(5, 1024)
	offset, ok := idx.Get(5)
	if !ok || offset != 1024 {
		t.Errorf("Get(5) = (%d, %v), want (1024, true)", offset, ok)
	}

	// Overwrite.
	idx.Set(5, 2048)
	offset, ok = idx.Get(5)
	if !ok || offset != 2048 {
		t.Errorf("after overwrite: Get(5) = (%d, %v), want (2048, true)", offset, ok)
	}

	// Count should be 1 (overwrite doesn't double-count).
	if idx.Count() != 1 {
		t.Errorf("Count = %d, want 1", idx.Count())
	}
}

func TestOffsetIndex_Delete(t *testing.T) {
	idx := NewOffsetIndex("", 64)

	idx.Set(10, 500)
	idx.Delete(10)

	_, ok := idx.Get(10)
	if ok {
		t.Error("expected absent after delete")
	}
	if idx.Count() != 0 {
		t.Errorf("Count = %d after delete, want 0", idx.Count())
	}

	// Deleting absent ID is a no-op.
	idx.Delete(999)
	if idx.Count() != 0 {
		t.Errorf("Count = %d after deleting absent, want 0", idx.Count())
	}
}

func TestOffsetIndex_Grow(t *testing.T) {
	idx := NewOffsetIndex("", 64)

	// Set beyond initial capacity.
	idx.Set(200, 9999)
	offset, ok := idx.Get(200)
	if !ok || offset != 9999 {
		t.Errorf("Get(200) = (%d, %v), want (9999, true)", offset, ok)
	}

	if idx.Capacity() < 201 {
		t.Errorf("Capacity = %d, want >= 201", idx.Capacity())
	}

	// Earlier entries should still be absent.
	_, ok = idx.Get(100)
	if ok {
		t.Error("expected ID 100 absent after grow")
	}
}

func TestOffsetIndex_ForEach(t *testing.T) {
	idx := NewOffsetIndex("", 64)

	idx.Set(1, 100)
	idx.Set(5, 500)
	idx.Set(10, 1000)

	collected := make(map[uint32]int64)
	idx.ForEach(func(id uint32, offset int64) bool {
		collected[id] = offset
		return true
	})

	if len(collected) != 3 {
		t.Fatalf("ForEach visited %d entries, want 3", len(collected))
	}
	if collected[1] != 100 || collected[5] != 500 || collected[10] != 1000 {
		t.Errorf("unexpected entries: %v", collected)
	}
}

func TestOffsetIndex_ForEachEarlyStop(t *testing.T) {
	idx := NewOffsetIndex("", 64)
	for i := uint32(0); i < 10; i++ {
		idx.Set(i, int64(i*100))
	}

	visited := 0
	idx.ForEach(func(_ uint32, _ int64) bool {
		visited++
		return visited < 3
	})

	if visited != 3 {
		t.Errorf("ForEach visited %d, want 3 (early stop)", visited)
	}
}

func TestOffsetIndex_Clone(t *testing.T) {
	idx := NewOffsetIndex("", 64)
	idx.Set(1, 100)
	idx.Set(5, 500)

	cloned := idx.Clone("/tmp/cloned.bin")

	// Cloned has same data.
	offset, ok := cloned.Get(1)
	if !ok || offset != 100 {
		t.Errorf("clone Get(1) = (%d, %v)", offset, ok)
	}
	if cloned.Count() != 2 {
		t.Errorf("clone Count = %d, want 2", cloned.Count())
	}

	// Mutation isolation: modifying clone doesn't affect original.
	cloned.Set(1, 999)
	offset, _ = idx.Get(1)
	if offset != 100 {
		t.Errorf("original mutated: Get(1) = %d, want 100", offset)
	}
}

func TestOffsetIndex_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.idx")

	// Create and populate.
	idx := NewOffsetIndex(path, 128)
	idx.Set(0, 0)
	idx.Set(42, 42000)
	idx.Set(127, 127000)

	if err := idx.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load into new instance.
	loaded, err := LoadOffsetIndex(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.Count() != 3 {
		t.Errorf("loaded Count = %d, want 3", loaded.Count())
	}

	cases := []struct {
		id     uint32
		offset int64
	}{
		{0, 0},
		{42, 42000},
		{127, 127000},
	}
	for _, tc := range cases {
		got, ok := loaded.Get(tc.id)
		if !ok || got != tc.offset {
			t.Errorf("loaded Get(%d) = (%d, %v), want (%d, true)", tc.id, got, ok, tc.offset)
		}
	}
}

func TestOffsetIndex_MergeFrom(t *testing.T) {
	base := NewOffsetIndex("", 64)
	base.Set(1, 100)
	base.Set(5, 500)

	other := NewOffsetIndex("", 64)
	other.Set(5, 555) // Overwrite
	other.Set(10, 1000)

	base.MergeFrom(other)

	if base.Count() != 3 {
		t.Errorf("Count = %d after merge, want 3", base.Count())
	}

	offset, _ := base.Get(5)
	if offset != 555 {
		t.Errorf("Get(5) = %d after merge, want 555 (overwritten)", offset)
	}

	offset, ok := base.Get(10)
	if !ok || offset != 1000 {
		t.Errorf("Get(10) = (%d, %v), want (1000, true)", offset, ok)
	}
}

func TestOffsetIndex_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.idx")

	// Write garbage.
	if err := os.WriteFile(path, []byte("garbage"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadOffsetIndex(path)
	if err == nil {
		t.Error("expected error loading corrupt file")
	}
}

func TestOffsetIndex_EmptySaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.idx")

	idx := NewOffsetIndex(path, 64)
	if err := idx.Save(); err != nil {
		t.Fatalf("Save empty: %v", err)
	}

	loaded, err := LoadOffsetIndex(path)
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if loaded.Count() != 0 {
		t.Errorf("Count = %d, want 0", loaded.Count())
	}
}

func TestOffsetIndex_ConcurrentAccess(t *testing.T) {
	idx := NewOffsetIndex("", 1024)

	var wg sync.WaitGroup
	writers := 8
	idsPerWriter := 100

	for w := range writers {
		wg.Add(1)
		go func(base uint32) {
			defer wg.Done()
			for i := uint32(0); i < uint32(idsPerWriter); i++ {
				idx.Set(base+i, int64(base+i)*10)
			}
		}(uint32(w * idsPerWriter))
	}
	wg.Wait()

	if idx.Count() != uint32(writers*idsPerWriter) {
		t.Errorf("Count = %d, want %d", idx.Count(), writers*idsPerWriter)
	}

	// Concurrent reads.
	for w := range writers {
		wg.Add(1)
		go func(base uint32) {
			defer wg.Done()
			for i := uint32(0); i < uint32(idsPerWriter); i++ {
				offset, ok := idx.Get(base + i)
				if !ok {
					t.Errorf("Get(%d) absent after concurrent write", base+i)
				}
				if offset != int64(base+i)*10 {
					t.Errorf("Get(%d) = %d, want %d", base+i, offset, int64(base+i)*10)
				}
			}
		}(uint32(w * idsPerWriter))
	}
	wg.Wait()
}

func TestOffsetIndex_RemapOffsets(t *testing.T) {
	idx := NewOffsetIndex("", 64)
	idx.Set(1, 100)
	idx.Set(5, 500)
	idx.Set(10, 1000)

	remap := map[int64]int64{
		100:  0,
		500:  8,
		1000: 16,
	}
	idx.RemapOffsets(remap)

	cases := []struct {
		id     uint32
		offset int64
	}{
		{1, 0},
		{5, 8},
		{10, 16},
	}
	for _, tc := range cases {
		got, ok := idx.Get(tc.id)
		if !ok || got != tc.offset {
			t.Errorf("after remap: Get(%d) = (%d, %v), want (%d, true)", tc.id, got, ok, tc.offset)
		}
	}

	// Count unchanged
	if idx.Count() != 3 {
		t.Errorf("Count = %d, want 3", idx.Count())
	}
}

func TestOffsetIndex_RemapOffsetsPartial(t *testing.T) {
	idx := NewOffsetIndex("", 64)
	idx.Set(1, 100)
	idx.Set(5, 500)

	// Only remap one entry; the other stays unchanged
	remap := map[int64]int64{100: 0}
	idx.RemapOffsets(remap)

	got1, _ := idx.Get(1)
	if got1 != 0 {
		t.Errorf("Get(1) = %d, want 0 (remapped)", got1)
	}

	got5, _ := idx.Get(5)
	if got5 != 500 {
		t.Errorf("Get(5) = %d, want 500 (unchanged)", got5)
	}
}

func TestOffsetIndex_ZeroOffset(t *testing.T) {
	idx := NewOffsetIndex("", 64)

	// Offset 0 is a valid offset (first record in a file).
	idx.Set(1, 0)
	offset, ok := idx.Get(1)
	if !ok || offset != 0 {
		t.Errorf("Get(1) = (%d, %v), want (0, true)", offset, ok)
	}
	if idx.Count() != 1 {
		t.Errorf("Count = %d, want 1", idx.Count())
	}
}

func TestSetBatch(t *testing.T) {
	dir := t.TempDir()
	idx := NewOffsetIndex(filepath.Join(dir, "batch.idx"), 64)

	ids := []uint32{10, 20, 30, 50}
	offsets := []int64{100, 200, 300, 500}
	idx.SetBatch(ids, offsets)

	// Verify all entries set.
	if idx.Count() != 4 {
		t.Fatalf("Count = %d, want 4", idx.Count())
	}
	for i, id := range ids {
		got, ok := idx.Get(id)
		if !ok || got != offsets[i] {
			t.Errorf("Get(%d) = (%d, %v), want (%d, true)", id, got, ok, offsets[i])
		}
	}

	// Absent IDs still absent.
	if _, ok := idx.Get(15); ok {
		t.Error("Get(15) should be absent")
	}
}

func TestSetBatchGrow(t *testing.T) {
	dir := t.TempDir()
	idx := NewOffsetIndex(filepath.Join(dir, "grow.idx"), 64)

	// Set an ID beyond initial capacity to force grow.
	ids := []uint32{5, 200}
	offsets := []int64{50, 2000}
	idx.SetBatch(ids, offsets)

	if idx.Count() != 2 {
		t.Fatalf("Count = %d, want 2", idx.Count())
	}
	if idx.Capacity() <= 200 {
		t.Errorf("Capacity = %d, should be > 200 after grow", idx.Capacity())
	}

	got, ok := idx.Get(200)
	if !ok || got != 2000 {
		t.Errorf("Get(200) = (%d, %v), want (2000, true)", got, ok)
	}
}

func TestSetBatchOverwrite(t *testing.T) {
	dir := t.TempDir()
	idx := NewOffsetIndex(filepath.Join(dir, "overwrite.idx"), 64)

	// Set ID 10 initially.
	idx.Set(10, 100)
	if idx.Count() != 1 {
		t.Fatalf("Count = %d after initial Set, want 1", idx.Count())
	}

	// Batch set overwrites ID 10 and adds ID 20.
	idx.SetBatch([]uint32{10, 20}, []int64{999, 200})

	if idx.Count() != 2 {
		t.Errorf("Count = %d, want 2", idx.Count())
	}
	got, _ := idx.Get(10)
	if got != 999 {
		t.Errorf("Get(10) = %d, want 999 (overwritten)", got)
	}
}
