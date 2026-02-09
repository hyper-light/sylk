package sylkdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDocIDMapGetOrAssign(t *testing.T) {
	m := NewDocIDMap(filepath.Join(t.TempDir(), "doc_ids.bin"))

	id1 := m.GetOrAssign("file_a.go")
	id2 := m.GetOrAssign("file_b.go")
	id1Again := m.GetOrAssign("file_a.go")

	if id1 != 1 {
		t.Errorf("first ID: got %d, want 1", id1)
	}
	if id2 != 2 {
		t.Errorf("second ID: got %d, want 2", id2)
	}
	if id1Again != id1 {
		t.Errorf("idempotent: got %d, want %d", id1Again, id1)
	}
}

func TestDocIDMapGet(t *testing.T) {
	m := NewDocIDMap(filepath.Join(t.TempDir(), "doc_ids.bin"))

	_, ok := m.Get("missing")
	if ok {
		t.Error("Get on missing key should return false")
	}

	m.GetOrAssign("exists")
	id, ok := m.Get("exists")
	if !ok || id != 1 {
		t.Errorf("Get after assign: got (%d, %v), want (1, true)", id, ok)
	}
}

func TestDocIDMapReverse(t *testing.T) {
	m := NewDocIDMap(filepath.Join(t.TempDir(), "doc_ids.bin"))

	m.GetOrAssign("alpha")
	m.GetOrAssign("beta")

	if got := m.Reverse(1); got != "alpha" {
		t.Errorf("Reverse(1): got %q, want %q", got, "alpha")
	}
	if got := m.Reverse(2); got != "beta" {
		t.Errorf("Reverse(2): got %q, want %q", got, "beta")
	}
	if got := m.Reverse(0); got != "" {
		t.Errorf("Reverse(0): got %q, want empty", got)
	}
	if got := m.Reverse(999); got != "" {
		t.Errorf("Reverse(999): got %q, want empty", got)
	}
}

func TestDocIDMapCount(t *testing.T) {
	m := NewDocIDMap(filepath.Join(t.TempDir(), "doc_ids.bin"))

	if m.Count() != 0 {
		t.Errorf("initial count: got %d, want 0", m.Count())
	}

	m.GetOrAssign("a")
	m.GetOrAssign("b")
	m.GetOrAssign("a") // duplicate

	if m.Count() != 2 {
		t.Errorf("after 2 unique assigns: got %d, want 2", m.Count())
	}
}

func TestDocIDMapSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "doc_ids.bin")

	// Create and populate
	m1 := NewDocIDMap(path)
	m1.GetOrAssign("file_a.go")
	m1.GetOrAssign("file_b.go")
	m1.GetOrAssign("file_c.go")

	if err := m1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load into fresh map
	m2, err := LoadDocIDMap(path)
	if err != nil {
		t.Fatalf("LoadDocIDMap: %v", err)
	}

	if m2.Count() != 3 {
		t.Errorf("loaded count: got %d, want 3", m2.Count())
	}

	// Verify forward lookup
	for _, key := range []string{"file_a.go", "file_b.go", "file_c.go"} {
		id1, ok1 := m1.Get(key)
		id2, ok2 := m2.Get(key)
		if !ok1 || !ok2 || id1 != id2 {
			t.Errorf("key %q: original=(%d,%v) loaded=(%d,%v)", key, id1, ok1, id2, ok2)
		}
	}

	// Verify reverse lookup
	for id := uint32(1); id <= 3; id++ {
		r1 := m1.Reverse(id)
		r2 := m2.Reverse(id)
		if r1 != r2 {
			t.Errorf("reverse(%d): original=%q loaded=%q", id, r1, r2)
		}
	}

	// Verify next ID continues correctly
	id4 := m2.GetOrAssign("file_d.go")
	if id4 != 4 {
		t.Errorf("next ID after load: got %d, want 4", id4)
	}
}

func TestDocIDMapLoadMissing(t *testing.T) {
	m, err := LoadDocIDMap(filepath.Join(t.TempDir(), "nonexistent.bin"))
	if err != nil {
		t.Fatalf("LoadDocIDMap on missing file: %v", err)
	}
	if m.Count() != 0 {
		t.Errorf("missing file should yield empty map, got count=%d", m.Count())
	}
}

func TestDocIDMapLoadCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.bin")

	// Too small
	if err := os.WriteFile(path, make([]byte, 4), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadDocIDMap(path)
	if err == nil {
		t.Error("expected error for truncated file")
	}

	// Wrong magic
	bad := make([]byte, docIDMapHeaderSize)
	bad[0] = 0xFF
	if err := os.WriteFile(path, bad, 0644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadDocIDMap(path)
	if err == nil {
		t.Error("expected error for wrong magic")
	}
}

func TestDocIDMapManyToOne(t *testing.T) {
	m := NewDocIDMap(filepath.Join(t.TempDir(), "doc_ids.bin"))

	// Multiple nodes reference the same document
	docID := m.GetOrAssign("src/main.go")

	// Simulate 3 different nodes all referencing the same doc
	nodes := []*Node{
		{ID: 1, DocRef: docID},
		{ID: 2, DocRef: docID},
		{ID: 3, DocRef: docID},
	}

	for _, n := range nodes {
		if n.DocRef != docID {
			t.Errorf("node %d DocRef: got %d, want %d", n.ID, n.DocRef, docID)
		}
	}

	// Verify reverse resolves correctly
	if got := m.Reverse(docID); got != "src/main.go" {
		t.Errorf("reverse: got %q, want %q", got, "src/main.go")
	}
}
