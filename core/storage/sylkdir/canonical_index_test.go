package sylkdir

import (
	"fmt"
	"testing"
)

func TestCanonicalKeyIndexLookupSet(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx.Init(); err != nil {
		t.Fatalf("Index init failed: %v", err)
	}

	// Lookup non-existent key
	_, err := idx.Lookup("repo:file.go:Func:func")
	if err != ErrKeyNotFound {
		t.Errorf("Expected ErrKeyNotFound, got %v", err)
	}

	// Set and lookup
	idx.Set("repo:file.go:Func:func", 42)

	id, err := idx.Lookup("repo:file.go:Func:func")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if id != 42 {
		t.Errorf("ID = %d, want 42", id)
	}
}

func TestCanonicalKeyIndexSupersession(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx.Init(); err != nil {
		t.Fatalf("Index init failed: %v", err)
	}

	// Original node
	idx.Set("repo:file.go:Func:func", 1)

	// Supersede with new node
	idx.Set("repo:file.go:Func:func", 2)

	// Should return new node ID
	id, err := idx.Lookup("repo:file.go:Func:func")
	if err != nil {
		t.Fatalf("Lookup failed: %v", err)
	}
	if id != 2 {
		t.Errorf("ID = %d, want 2 (superseded)", id)
	}
}

func TestCanonicalKeyIndexSetIfNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx.Init(); err != nil {
		t.Fatalf("Index init failed: %v", err)
	}

	// First set succeeds
	existingID, wasSet := idx.SetIfNotExists("repo:file.go:Func:func", 1)
	if !wasSet {
		t.Error("First SetIfNotExists should return wasSet=true")
	}
	if existingID != 1 {
		t.Errorf("existingID = %d, want 1", existingID)
	}

	// Second set fails (key exists)
	existingID, wasSet = idx.SetIfNotExists("repo:file.go:Func:func", 2)
	if wasSet {
		t.Error("Second SetIfNotExists should return wasSet=false")
	}
	if existingID != 1 {
		t.Errorf("existingID = %d, want 1 (original)", existingID)
	}

	// Verify key still points to original
	id, _ := idx.Lookup("repo:file.go:Func:func")
	if id != 1 {
		t.Errorf("ID = %d, want 1", id)
	}
}

func TestCanonicalKeyIndexDelete(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx.Init(); err != nil {
		t.Fatalf("Index init failed: %v", err)
	}

	idx.Set("key1", 1)
	idx.Set("key2", 2)

	if !idx.Has("key1") {
		t.Error("key1 should exist")
	}

	idx.Delete("key1")

	if idx.Has("key1") {
		t.Error("key1 should not exist after delete")
	}
	if !idx.Has("key2") {
		t.Error("key2 should still exist")
	}

	// Delete non-existent key is no-op
	idx.Delete("nonexistent")
}

func TestCanonicalKeyIndexPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Create index and add keys
	idx1 := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx1.Init(); err != nil {
		t.Fatalf("Index init failed: %v", err)
	}

	idx1.Set("repo:file1.go:Func1:func", 1)
	idx1.Set("repo:file2.go:Func2:func", 2)
	idx1.Set("doi:10.1234/paper", 3)

	if err := idx1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Load index in new instance
	idx2 := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx2.Init(); err != nil {
		t.Fatalf("Index2 init failed: %v", err)
	}

	// Verify all keys loaded
	id, err := idx2.Lookup("repo:file1.go:Func1:func")
	if err != nil || id != 1 {
		t.Errorf("file1 lookup: id=%d, err=%v", id, err)
	}

	id, err = idx2.Lookup("repo:file2.go:Func2:func")
	if err != nil || id != 2 {
		t.Errorf("file2 lookup: id=%d, err=%v", id, err)
	}

	id, err = idx2.Lookup("doi:10.1234/paper")
	if err != nil || id != 3 {
		t.Errorf("paper lookup: id=%d, err=%v", id, err)
	}
}

func TestCanonicalKeyIndexKeys(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx.Init(); err != nil {
		t.Fatalf("Index init failed: %v", err)
	}

	idx.Set("c:key", 3)
	idx.Set("a:key", 1)
	idx.Set("b:key", 2)

	keys := idx.Keys()
	if len(keys) != 3 {
		t.Fatalf("Keys() length = %d, want 3", len(keys))
	}

	// Should be sorted
	if keys[0] != "a:key" || keys[1] != "b:key" || keys[2] != "c:key" {
		t.Errorf("Keys not sorted: %v", keys)
	}
}

func TestCanonicalKeyIndexCount(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx.Init(); err != nil {
		t.Fatalf("Index init failed: %v", err)
	}

	if idx.Count() != 0 {
		t.Errorf("Initial count = %d, want 0", idx.Count())
	}

	idx.Set("key1", 1)
	idx.Set("key2", 2)
	idx.Set("key3", 3)

	if idx.Count() != 3 {
		t.Errorf("Count = %d, want 3", idx.Count())
	}

	idx.Delete("key2")

	if idx.Count() != 2 {
		t.Errorf("Count after delete = %d, want 2", idx.Count())
	}
}

func TestCanonicalKeyIndexDirty(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx.Init(); err != nil {
		t.Fatalf("Index init failed: %v", err)
	}

	if idx.IsDirty() {
		t.Error("Should not be dirty after init")
	}

	idx.Set("key", 1)
	if !idx.IsDirty() {
		t.Error("Should be dirty after Set")
	}

	idx.Save()
	if idx.IsDirty() {
		t.Error("Should not be dirty after Save")
	}

	idx.Delete("key")
	if !idx.IsDirty() {
		t.Error("Should be dirty after Delete")
	}
}

func TestCanonicalKeyIndexMerge(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx1 := NewCanonicalKeyIndexFromSylkDir(sd)
	idx1.Init()
	idx1.Set("key1", 1)
	idx1.Set("key2", 2)

	idx2 := NewCanonicalKeyIndex(tmpDir + "/other")
	idx2.Set("key2", 20) // Conflict
	idx2.Set("key3", 3)

	// Merge with default behavior (new wins)
	idx1.Merge(idx2, nil)

	id, _ := idx1.Lookup("key1")
	if id != 1 {
		t.Errorf("key1 = %d, want 1", id)
	}

	id, _ = idx1.Lookup("key2")
	if id != 20 {
		t.Errorf("key2 = %d, want 20 (new wins)", id)
	}

	id, _ = idx1.Lookup("key3")
	if id != 3 {
		t.Errorf("key3 = %d, want 3", id)
	}
}

func TestCanonicalKeyIndexMergeWithOverride(t *testing.T) {
	tmpDir := t.TempDir()

	idx1 := NewCanonicalKeyIndex(tmpDir + "/idx1")
	idx1.Set("key", 1)

	idx2 := NewCanonicalKeyIndex(tmpDir + "/idx2")
	idx2.Set("key", 2)

	// Merge with custom override (keep higher ID)
	idx1.Merge(idx2, func(key string, oldID, newID uint32) uint32 {
		if newID > oldID {
			return newID
		}
		return oldID
	})

	id, _ := idx1.Lookup("key")
	if id != 2 {
		t.Errorf("key = %d, want 2 (higher wins)", id)
	}
}

func TestCanonicalKeyIndexLookupPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	idx.Set("repo:file1.go:Func1:func", 1)
	idx.Set("repo:file2.go:Func2:func", 2)
	idx.Set("doi:10.1234/paper", 3)
	idx.Set("url:https://example.com", 4)

	// Lookup by prefix
	repoKeys := idx.LookupPrefix("repo:")
	if len(repoKeys) != 2 {
		t.Errorf("repo: prefix count = %d, want 2", len(repoKeys))
	}

	doiKeys := idx.LookupPrefix("doi:")
	if len(doiKeys) != 1 {
		t.Errorf("doi: prefix count = %d, want 1", len(doiKeys))
	}

	allKeys := idx.LookupPrefix("")
	if len(allKeys) != 4 {
		t.Errorf("empty prefix count = %d, want 4", len(allKeys))
	}

	noKeys := idx.LookupPrefix("nonexistent:")
	if len(noKeys) != 0 {
		t.Errorf("nonexistent: prefix count = %d, want 0", len(noKeys))
	}
}

func TestCanonicalKeyIndexStats(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	idx.Set("key1", 1)
	idx.Set("key12", 2)
	idx.Set("key123", 3)

	stats := idx.Stats()
	if stats.KeyCount != 3 {
		t.Errorf("KeyCount = %d, want 3", stats.KeyCount)
	}
	// 4 + 5 + 6 = 15 bytes
	if stats.TotalKeyBytes != 15 {
		t.Errorf("TotalKeyBytes = %d, want 15", stats.TotalKeyBytes)
	}
	if !stats.IsDirty {
		t.Error("IsDirty should be true")
	}
}

func TestCanonicalKeyIndexLargeDataset(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	idx := NewCanonicalKeyIndexFromSylkDir(sd)
	idx.Init()

	// Add 10000 keys
	numKeys := 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", i, i)
		idx.Set(key, uint32(i))
	}

	if idx.Count() != numKeys {
		t.Errorf("Count = %d, want %d", idx.Count(), numKeys)
	}

	// Save and reload
	idx.Close()

	idx2 := NewCanonicalKeyIndexFromSylkDir(sd)
	if err := idx2.Init(); err != nil {
		t.Fatalf("Reload init failed: %v", err)
	}

	if idx2.Count() != numKeys {
		t.Errorf("Count after reload = %d, want %d", idx2.Count(), numKeys)
	}

	// Verify random lookups
	for _, i := range []int{0, 5000, 9999} {
		key := fmt.Sprintf("repo:path/to/file%d.go:Function%d:func", i, i)
		id, err := idx2.Lookup(key)
		if err != nil {
			t.Errorf("Lookup %d failed: %v", i, err)
		} else if id != uint32(i) {
			t.Errorf("Lookup %d: id=%d, want %d", i, id, i)
		}
	}
}
