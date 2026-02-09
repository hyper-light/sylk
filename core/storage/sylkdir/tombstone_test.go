package sylkdir

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestTombstoneBitmapMarkAndCheck(t *testing.T) {
	tb := NewTombstoneBitmap("", 128)

	if tb.IsDead(0) {
		t.Fatal("fresh bitmap should report all nodes alive")
	}
	if tb.Count() != 0 {
		t.Fatalf("expected count 0, got %d", tb.Count())
	}

	tb.MarkDead(5)
	if !tb.IsDead(5) {
		t.Fatal("node 5 should be dead after MarkDead")
	}
	if tb.IsDead(4) {
		t.Fatal("node 4 should still be alive")
	}
	if tb.Count() != 1 {
		t.Fatalf("expected count 1, got %d", tb.Count())
	}
}

func TestTombstoneBitmapIdempotent(t *testing.T) {
	tb := NewTombstoneBitmap("", 128)

	tb.MarkDead(10)
	tb.MarkDead(10)
	tb.MarkDead(10)

	if tb.Count() != 1 {
		t.Fatalf("idempotent MarkDead should not increment count: got %d", tb.Count())
	}
}

func TestTombstoneBitmapWordBoundaries(t *testing.T) {
	tb := NewTombstoneBitmap("", 256)

	// Test at word boundaries: 0, 63, 64, 65, 127, 128
	ids := []uint32{0, 63, 64, 65, 127, 128}
	for _, id := range ids {
		tb.MarkDead(id)
	}

	if tb.Count() != uint32(len(ids)) {
		t.Fatalf("expected count %d, got %d", len(ids), tb.Count())
	}

	for _, id := range ids {
		if !tb.IsDead(id) {
			t.Fatalf("node %d should be dead", id)
		}
	}

	// Check nodes between boundaries are alive
	aliveIDs := []uint32{1, 32, 62, 66, 100, 126, 129}
	for _, id := range aliveIDs {
		if tb.IsDead(id) {
			t.Fatalf("node %d should be alive", id)
		}
	}
}

func TestTombstoneBitmapEdgeLiveness(t *testing.T) {
	tb := NewTombstoneBitmap("", 128)

	tb.MarkDead(5)

	// Both alive
	if !tb.IsEdgeAlive(1, 2) {
		t.Fatal("edge (1,2) should be alive")
	}

	// Source dead
	if tb.IsEdgeAlive(5, 2) {
		t.Fatal("edge (5,2) should be dead (source dead)")
	}

	// Target dead
	if tb.IsEdgeAlive(1, 5) {
		t.Fatal("edge (1,5) should be dead (target dead)")
	}

	// Both dead
	tb.MarkDead(10)
	if tb.IsEdgeAlive(5, 10) {
		t.Fatal("edge (5,10) should be dead (both dead)")
	}
}

func TestTombstoneBitmapCapacityGrowth(t *testing.T) {
	tb := NewTombstoneBitmap("", 64)

	if tb.Capacity() != 64 {
		t.Fatalf("expected capacity 64, got %d", tb.Capacity())
	}

	// Mark beyond initial capacity
	tb.MarkDead(200)

	if !tb.IsDead(200) {
		t.Fatal("node 200 should be dead after growth")
	}
	if tb.Capacity() < 201 {
		t.Fatalf("capacity should have grown: got %d", tb.Capacity())
	}

	// Verify capacity is word-aligned
	if tb.Capacity()%64 != 0 {
		t.Fatalf("capacity should be word-aligned: got %d", tb.Capacity())
	}
}

func TestTombstoneBitmapBeyondCapacity(t *testing.T) {
	tb := NewTombstoneBitmap("", 64)

	// Query beyond capacity should return alive (not dead)
	if tb.IsDead(1000) {
		t.Fatal("node beyond capacity should not be dead")
	}
}

func TestTombstoneBitmapDeadRatio(t *testing.T) {
	tb := NewTombstoneBitmap("", 128)

	if tb.DeadRatio(0) != 0 {
		t.Fatal("dead ratio with 0 total should be 0")
	}

	tb.MarkDead(1)
	tb.MarkDead(2)

	ratio := tb.DeadRatio(10)
	if ratio != 0.2 {
		t.Fatalf("expected ratio 0.2, got %f", ratio)
	}
}

func TestTombstoneBitmapPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TombstoneFile)

	// Create and populate
	tb := NewTombstoneBitmap(path, 256)
	tb.MarkDead(0)
	tb.MarkDead(63)
	tb.MarkDead(64)
	tb.MarkDead(100)
	tb.MarkDead(255)

	if err := tb.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	expectedSize := tombstoneHeaderSize + int(tb.Capacity()/64)*8
	if info.Size() != int64(expectedSize) {
		t.Fatalf("expected file size %d, got %d", expectedSize, info.Size())
	}

	// Load into new bitmap
	tb2 := &TombstoneBitmap{path: path}
	if err := tb2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify all dead nodes preserved
	if tb2.Count() != 5 {
		t.Fatalf("expected count 5, got %d", tb2.Count())
	}
	for _, id := range []uint32{0, 63, 64, 100, 255} {
		if !tb2.IsDead(id) {
			t.Fatalf("node %d should be dead after reload", id)
		}
	}

	// Verify alive nodes preserved
	for _, id := range []uint32{1, 32, 65, 99, 200} {
		if tb2.IsDead(id) {
			t.Fatalf("node %d should be alive after reload", id)
		}
	}
}

func TestTombstoneBitmapLoadNonexistent(t *testing.T) {
	tb := &TombstoneBitmap{path: "/nonexistent/path/tombstones.bin"}
	if err := tb.Load(); err != nil {
		t.Fatalf("Load of nonexistent file should not error: %v", err)
	}
	if tb.Count() != 0 {
		t.Fatal("empty load should have count 0")
	}
}

func TestTombstoneBitmapEmptyPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TombstoneFile)

	tb := NewTombstoneBitmap(path, 128)
	if err := tb.Save(); err != nil {
		t.Fatalf("Save empty: %v", err)
	}

	tb2 := &TombstoneBitmap{path: path}
	if err := tb2.Load(); err != nil {
		t.Fatalf("Load empty: %v", err)
	}

	if tb2.Count() != 0 {
		t.Fatalf("expected count 0, got %d", tb2.Count())
	}
	if tb2.Capacity() != 128 {
		t.Fatalf("expected capacity 128, got %d", tb2.Capacity())
	}
}

func TestTombstoneBitmapInvalidMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TombstoneFile)

	// Write garbage
	if err := os.WriteFile(path, make([]byte, 32), 0644); err != nil {
		t.Fatal(err)
	}

	tb := &TombstoneBitmap{path: path}
	if err := tb.Load(); err == nil {
		t.Fatal("Load should fail with invalid magic")
	}
}

func TestTombstoneBitmapTruncatedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, TombstoneFile)

	// Write valid header but truncated words
	data := make([]byte, tombstoneHeaderSize)
	binary.LittleEndian.PutUint32(data[0:4], tombstoneMagic)
	binary.LittleEndian.PutUint32(data[4:8], tombstoneFormatVersion)
	binary.LittleEndian.PutUint32(data[8:12], 128) // capacity = 128, needs 2 words = 16 bytes
	binary.LittleEndian.PutUint32(data[12:16], 0)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	tb := &TombstoneBitmap{path: path}
	if err := tb.Load(); err == nil {
		t.Fatal("Load should fail with truncated file")
	}
}

func TestTombstoneBitmapConcurrent(t *testing.T) {
	tb := NewTombstoneBitmap("", 1024)

	var wg sync.WaitGroup
	// Writers
	for w := range 4 {
		wg.Add(1)
		go func(base uint32) {
			defer wg.Done()
			for i := range uint32(100) {
				tb.MarkDead(base*100 + i)
			}
		}(uint32(w))
	}

	// Concurrent readers
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range uint32(400) {
				tb.IsDead(i)
				tb.IsEdgeAlive(i, i+1)
			}
		}()
	}

	wg.Wait()

	if tb.Count() != 400 {
		t.Fatalf("expected 400 dead nodes, got %d", tb.Count())
	}
}

func TestTombstoneBitmapWordAlignedCapacity(t *testing.T) {
	tests := []struct {
		input    uint32
		expected uint32
	}{
		{0, 0},
		{1, 64},
		{63, 64},
		{64, 64},
		{65, 128},
		{128, 128},
		{129, 192},
	}

	for _, tt := range tests {
		got := wordAlignedCapacity(tt.input)
		if got != tt.expected {
			t.Errorf("wordAlignedCapacity(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestLoadTombstoneBitmap(t *testing.T) {
	dir := t.TempDir()

	// No file exists — should return empty bitmap
	tb, err := LoadTombstoneBitmap(dir)
	if err != nil {
		t.Fatalf("LoadTombstoneBitmap: %v", err)
	}
	if tb.Count() != 0 {
		t.Fatalf("expected count 0, got %d", tb.Count())
	}
}

func TestLoadOrCreateTombstoneBitmap(t *testing.T) {
	dir := t.TempDir()

	// Create new
	tb, err := LoadOrCreateTombstoneBitmap(dir, 256)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if tb.Capacity() != 256 {
		t.Fatalf("expected capacity 256, got %d", tb.Capacity())
	}

	// Mark and save
	tb.MarkDead(42)
	if err := tb.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load existing
	tb2, err := LoadOrCreateTombstoneBitmap(dir, 256)
	if err != nil {
		t.Fatalf("LoadOrCreate existing: %v", err)
	}
	if !tb2.IsDead(42) {
		t.Fatal("node 42 should be dead after reload")
	}
}

func BenchmarkTombstoneIsDead(b *testing.B) {
	tb := NewTombstoneBitmap("", 100_000)
	for i := range uint32(50_000) {
		tb.MarkDead(i * 2) // Mark even IDs dead
	}

	b.ResetTimer()
	for i := range b.N {
		tb.IsDead(uint32(i) % 100_000)
	}
}

func BenchmarkTombstoneIsEdgeAlive(b *testing.B) {
	tb := NewTombstoneBitmap("", 100_000)
	for i := range uint32(10_000) {
		tb.MarkDead(i)
	}

	b.ResetTimer()
	for i := range b.N {
		id := uint32(i) % 100_000
		tb.IsEdgeAlive(id, id+1)
	}
}
