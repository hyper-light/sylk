package graph

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestIDAllocator_NextID(t *testing.T) {
	alloc, err := NewIDAllocator("")
	if err != nil {
		t.Fatal(err)
	}

	id0 := alloc.NextID()
	id1 := alloc.NextID()
	id2 := alloc.NextID()

	if id0 != 0 || id1 != 1 || id2 != 2 {
		t.Errorf("expected 0,1,2, got %d,%d,%d", id0, id1, id2)
	}

	if alloc.Current() != 3 {
		t.Errorf("expected current=3, got %d", alloc.Current())
	}
}

func TestIDAllocator_ConcurrentAllocation(t *testing.T) {
	alloc, err := NewIDAllocator("")
	if err != nil {
		t.Fatal(err)
	}

	const numGoroutines = 100
	const allocsPerGoroutine = 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	ids := make(chan uint32, numGoroutines*allocsPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < allocsPerGoroutine; i++ {
				ids <- alloc.NextID()
			}
		}()
	}

	wg.Wait()
	close(ids)

	seen := make(map[uint32]bool)
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate ID: %d", id)
		}
		seen[id] = true
	}

	expected := numGoroutines * allocsPerGoroutine
	if len(seen) != expected {
		t.Errorf("expected %d unique IDs, got %d", expected, len(seen))
	}
}

func TestIDAllocator_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "id_counter")

	alloc1, err := NewIDAllocator(filePath)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		alloc1.NextID()
	}

	if err := alloc1.Persist(); err != nil {
		t.Fatal(err)
	}

	alloc2, err := NewIDAllocator(filePath)
	if err != nil {
		t.Fatal(err)
	}

	if alloc2.Current() != 100 {
		t.Errorf("expected restored current=100, got %d", alloc2.Current())
	}

	nextID := alloc2.NextID()
	if nextID != 100 {
		t.Errorf("expected next ID=100, got %d", nextID)
	}
}

func TestIDAllocator_PersistenceNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "nonexistent", "id_counter")

	_, err := NewIDAllocator(filePath)
	if err != nil {
		t.Fatal(err)
	}
}

func TestIDAllocator_Reset(t *testing.T) {
	alloc, err := NewIDAllocator("")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 50; i++ {
		alloc.NextID()
	}

	alloc.Reset()

	if alloc.Current() != 0 {
		t.Errorf("expected current=0 after reset, got %d", alloc.Current())
	}

	if alloc.NextID() != 0 {
		t.Error("expected first ID after reset to be 0")
	}
}

func TestIDAllocator_EmptyFilePath(t *testing.T) {
	alloc, err := NewIDAllocator("")
	if err != nil {
		t.Fatal(err)
	}

	if err := alloc.Persist(); err != nil {
		t.Errorf("persist with empty path should be no-op, got error: %v", err)
	}
}

func TestIDAllocator_CorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "id_counter")

	if err := os.WriteFile(filePath, []byte{0x01, 0x02}, 0644); err != nil {
		t.Fatal(err)
	}

	alloc, err := NewIDAllocator(filePath)
	if err != nil {
		t.Fatal(err)
	}

	if alloc.Current() != 0 {
		t.Errorf("expected current=0 for corrupted file, got %d", alloc.Current())
	}
}

func BenchmarkIDAllocator_NextID(b *testing.B) {
	alloc, _ := NewIDAllocator("")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alloc.NextID()
	}
}

func BenchmarkIDAllocator_NextIDParallel(b *testing.B) {
	alloc, _ := NewIDAllocator("")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			alloc.NextID()
		}
	})
}
