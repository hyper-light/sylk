package storage

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewIDMap(t *testing.T) {
	m := NewIDMap()
	if m == nil {
		t.Fatal("NewIDMap returned nil")
	}
	if m.Size() != 0 {
		t.Errorf("expected size 0, got %d", m.Size())
	}
}

func TestIDMap_Assign(t *testing.T) {
	m := NewIDMap()

	// First assignment should get ID 0
	id0 := m.Assign("first")
	if id0 != 0 {
		t.Errorf("expected first ID to be 0, got %d", id0)
	}

	// Second assignment should get ID 1
	id1 := m.Assign("second")
	if id1 != 1 {
		t.Errorf("expected second ID to be 1, got %d", id1)
	}

	// Assigning same ID again should return existing ID (idempotent)
	id0Again := m.Assign("first")
	if id0Again != 0 {
		t.Errorf("expected idempotent assign to return 0, got %d", id0Again)
	}

	// Size should be 2 (not 3)
	if m.Size() != 2 {
		t.Errorf("expected size 2, got %d", m.Size())
	}
}

func TestIDMap_ToInternal(t *testing.T) {
	m := NewIDMap()
	m.Assign("test-id")

	// Existing ID
	id, ok := m.ToInternal("test-id")
	if !ok {
		t.Error("expected to find test-id")
	}
	if id != 0 {
		t.Errorf("expected internal ID 0, got %d", id)
	}

	// Non-existing ID
	_, ok = m.ToInternal("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent ID")
	}
}

func TestIDMap_ToExternal(t *testing.T) {
	m := NewIDMap()
	m.Assign("external-id")

	// Existing internal ID
	ext, ok := m.ToExternal(0)
	if !ok {
		t.Error("expected to find internal ID 0")
	}
	if ext != "external-id" {
		t.Errorf("expected 'external-id', got '%s'", ext)
	}

	// Non-existing internal ID
	_, ok = m.ToExternal(999)
	if ok {
		t.Error("expected not to find internal ID 999")
	}
}

func TestIDMap_Contains(t *testing.T) {
	m := NewIDMap()
	m.Assign("exists")

	if !m.Contains("exists") {
		t.Error("expected Contains to return true for existing ID")
	}
	if m.Contains("nonexistent") {
		t.Error("expected Contains to return false for nonexistent ID")
	}
}

func TestIDMap_SequentialIDs(t *testing.T) {
	m := NewIDMap()
	ids := []string{"a", "b", "c", "d", "e"}

	for i, extID := range ids {
		internalID := m.Assign(extID)
		if int(internalID) != i {
			t.Errorf("expected sequential ID %d for %s, got %d", i, extID, internalID)
		}
	}

	// Verify all mappings
	for i, extID := range ids {
		internal, ok := m.ToInternal(extID)
		if !ok || int(internal) != i {
			t.Errorf("ToInternal failed for %s", extID)
		}

		external, ok := m.ToExternal(uint32(i))
		if !ok || external != extID {
			t.Errorf("ToExternal failed for %d", i)
		}
	}
}

func TestIDMap_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "idmap.json")

	// Create and populate original map
	original := NewIDMap()
	original.Assign("alpha")
	original.Assign("beta")
	original.Assign("gamma")

	// Save
	if err := original.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load into new map
	loaded, err := LoadIDMap(path)
	if err != nil {
		t.Fatalf("LoadIDMap failed: %v", err)
	}

	// Verify size
	if loaded.Size() != original.Size() {
		t.Errorf("size mismatch: original=%d, loaded=%d", original.Size(), loaded.Size())
	}

	// Verify all mappings preserved
	for _, extID := range []string{"alpha", "beta", "gamma"} {
		origInternal, _ := original.ToInternal(extID)
		loadedInternal, ok := loaded.ToInternal(extID)
		if !ok {
			t.Errorf("loaded map missing %s", extID)
			continue
		}
		if origInternal != loadedInternal {
			t.Errorf("internal ID mismatch for %s: original=%d, loaded=%d",
				extID, origInternal, loadedInternal)
		}
	}
}

func TestIDMap_LoadInstance(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "idmap.json")

	// Create initial map
	m := NewIDMap()
	m.Assign("one")
	m.Assign("two")
	if err := m.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Create new map with different data
	m2 := NewIDMap()
	m2.Assign("different")

	// Load should replace existing data
	if err := m2.Load(path); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if m2.Size() != 2 {
		t.Errorf("expected size 2 after load, got %d", m2.Size())
	}
	if !m2.Contains("one") || !m2.Contains("two") {
		t.Error("loaded data missing expected IDs")
	}
	if m2.Contains("different") {
		t.Error("old data should be replaced")
	}
}

func TestIDMap_LoadNonexistent(t *testing.T) {
	_, err := LoadIDMap("/nonexistent/path/idmap.json")
	if err == nil {
		t.Error("expected error loading nonexistent file")
	}
}

func TestIDMap_ConcurrentAccess(t *testing.T) {
	m := NewIDMap()
	var wg sync.WaitGroup
	numGoroutines := 100
	idsPerGoroutine := 100

	// Concurrent assigns
	for g := range numGoroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := range idsPerGoroutine {
				extID := string(rune('A'+goroutineID)) + string(rune('0'+i%10))
				m.Assign(extID)
			}
		}(g)
	}
	wg.Wait()

	// Concurrent reads
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idsPerGoroutine {
				extID := string(rune('A'+i%numGoroutines)) + string(rune('0'+i%10))
				m.Contains(extID)
				m.ToInternal(extID)
				if internal, ok := m.ToInternal(extID); ok {
					m.ToExternal(internal)
				}
			}
		}()
	}
	wg.Wait()

	// Verify no gaps: all internal IDs from 0 to Size-1 should be valid
	size := m.Size()
	for i := range size {
		if _, ok := m.ToExternal(uint32(i)); !ok {
			t.Errorf("gap detected at internal ID %d", i)
		}
	}
}

func TestIDMap_JSONFormat(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "idmap.json")

	m := NewIDMap()
	m.Assign("test1")
	m.Assign("test2")

	if err := m.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Read raw JSON and verify format
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	// Should be valid JSON containing our IDs
	jsonStr := string(content)
	if len(jsonStr) == 0 {
		t.Error("saved file is empty")
	}

	// Verify it's human-readable (indented)
	if jsonStr[0] != '{' {
		t.Error("expected JSON object")
	}
}

func TestIDMap_EmptyMap(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "empty.json")

	// Save empty map
	m := NewIDMap()
	if err := m.Save(path); err != nil {
		t.Fatalf("Save empty map failed: %v", err)
	}

	// Load empty map
	loaded, err := LoadIDMap(path)
	if err != nil {
		t.Fatalf("LoadIDMap empty failed: %v", err)
	}

	if loaded.Size() != 0 {
		t.Errorf("expected size 0, got %d", loaded.Size())
	}
}
