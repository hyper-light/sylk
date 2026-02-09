package editor

import (
	"testing"

	"github.com/adalundhe/sylk/ui/editor/buffer"
)

// makeWarm builds a WarmState with the given path, content, and modified flag.
func makeWarm(path, content string, modified bool) *WarmState {
	pt := buffer.NewPieceTable(content)
	return &WarmState{
		FilePath:  path,
		Language:  "go",
		Modified:  modified,
		Buf:       pt,
		LineIndex: buffer.NewLineIndex(pt),
		UndoTree:  buffer.NewUndoTree(0),
	}
}

func TestEditorCache_PutTakeRoundTrip(t *testing.T) {
	c := NewEditorCache(CacheConfig{})
	ws := makeWarm("/a.go", "hello", false)
	c.Put(ws)

	entry, ok := c.Take("/a.go")
	if !ok {
		t.Fatal("expected entry for /a.go")
	}
	if !entry.IsWarm() {
		t.Fatal("expected warm entry")
	}
	if entry.Warm().Buf.Content() != "hello" {
		t.Fatalf("content mismatch: %q", entry.Warm().Buf.Content())
	}

	// After Take, entry should be gone from cache.
	if c.Len() != 0 {
		t.Fatalf("expected empty cache, got %d", c.Len())
	}
}

func TestEditorCache_TakeMissing(t *testing.T) {
	c := NewEditorCache(CacheConfig{})
	_, ok := c.Take("/nonexistent")
	if ok {
		t.Fatal("expected miss for nonexistent path")
	}
}

func TestEditorCache_WarmEvictionDemotesToCold(t *testing.T) {
	c := NewEditorCache(CacheConfig{WarmCap: 2, ColdCap: 10})

	c.Put(makeWarm("/a.go", "aaa", false))
	c.Put(makeWarm("/b.go", "bbb", false))
	c.Put(makeWarm("/c.go", "ccc", false))

	// Warm cap is 2, so /a.go should be demoted to cold.
	if c.WarmCount() != 2 {
		t.Fatalf("expected 2 warm, got %d", c.WarmCount())
	}
	if c.ColdCount() != 1 {
		t.Fatalf("expected 1 cold, got %d", c.ColdCount())
	}

	entry, ok := c.Take("/a.go")
	if !ok {
		t.Fatal("expected /a.go in cache (cold)")
	}
	if entry.IsWarm() {
		t.Fatal("expected /a.go to be cold")
	}
	if entry.Cold().Content != "aaa" {
		t.Fatalf("cold content mismatch: %q", entry.Cold().Content)
	}
}

func TestEditorCache_ColdEviction(t *testing.T) {
	c := NewEditorCache(CacheConfig{WarmCap: 1, ColdCap: 1})

	c.Put(makeWarm("/a.go", "aaa", false))
	c.Put(makeWarm("/b.go", "bbb", false))
	// /a.go demoted to cold.
	c.Put(makeWarm("/c.go", "ccc", false))
	// /b.go demoted to cold → cold has /a.go and /b.go → cold cap 1 → /a.go evicted.

	_, ok := c.Take("/a.go")
	if ok {
		t.Fatal("expected /a.go to be evicted")
	}

	entry, ok := c.Take("/b.go")
	if !ok {
		t.Fatal("expected /b.go in cache")
	}
	if entry.IsWarm() {
		t.Fatal("expected /b.go to be cold")
	}
}

func TestEditorCache_ModifiedNeverEvicted(t *testing.T) {
	c := NewEditorCache(CacheConfig{WarmCap: 1, ColdCap: 1})

	c.Put(makeWarm("/a.go", "aaa", true))  // modified
	c.Put(makeWarm("/b.go", "bbb", true))  // modified
	c.Put(makeWarm("/c.go", "ccc", false)) // not modified

	// All three should still exist — modified entries are never evicted.
	if c.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", c.Len())
	}
}

func TestEditorCache_LRUOrdering(t *testing.T) {
	c := NewEditorCache(CacheConfig{WarmCap: 2, ColdCap: 10})

	c.Put(makeWarm("/a.go", "aaa", false))
	c.Put(makeWarm("/b.go", "bbb", false))

	// Touch /a.go by re-putting (simulates switching back to it).
	c.Put(makeWarm("/a.go", "aaa2", false))

	// Now add /c.go — should demote /b.go (oldest), not /a.go.
	c.Put(makeWarm("/c.go", "ccc", false))

	entryB, ok := c.Take("/b.go")
	if !ok {
		t.Fatal("expected /b.go in cache")
	}
	if entryB.IsWarm() {
		t.Fatal("expected /b.go to be cold (oldest)")
	}

	entryA, ok := c.Take("/a.go")
	if !ok {
		t.Fatal("expected /a.go in cache")
	}
	if !entryA.IsWarm() {
		t.Fatal("expected /a.go to still be warm (recently accessed)")
	}
}

func TestEditorCache_Delete(t *testing.T) {
	c := NewEditorCache(CacheConfig{})
	c.Put(makeWarm("/a.go", "aaa", false))

	c.Delete("/a.go")
	if c.Len() != 0 {
		t.Fatalf("expected empty cache after delete, got %d", c.Len())
	}

	// Deleting a non-existent key is a no-op.
	c.Delete("/nonexistent")
}

func TestEditorCache_DeleteColdEntry(t *testing.T) {
	c := NewEditorCache(CacheConfig{WarmCap: 1, ColdCap: 10})

	c.Put(makeWarm("/a.go", "aaa", false))
	c.Put(makeWarm("/b.go", "bbb", false))
	// /a.go is now cold.

	c.Delete("/a.go")
	if c.ColdCount() != 0 {
		t.Fatalf("expected 0 cold after delete, got %d", c.ColdCount())
	}
	if c.Len() != 1 {
		t.Fatalf("expected 1 total entry, got %d", c.Len())
	}
}

func TestEditorCache_IsModified(t *testing.T) {
	c := NewEditorCache(CacheConfig{})

	c.Put(makeWarm("/a.go", "aaa", true))
	c.Put(makeWarm("/b.go", "bbb", false))

	if !c.IsModified("/a.go") {
		t.Fatal("expected /a.go to be modified")
	}
	if c.IsModified("/b.go") {
		t.Fatal("expected /b.go to not be modified")
	}
	if c.IsModified("/nonexistent") {
		t.Fatal("expected nonexistent to not be modified")
	}
}

func TestEditorCache_IsModifiedCold(t *testing.T) {
	c := NewEditorCache(CacheConfig{WarmCap: 1, ColdCap: 10})

	c.Put(makeWarm("/a.go", "aaa", true)) // modified
	c.Put(makeWarm("/b.go", "bbb", false))
	// /a.go demoted to cold, still modified.

	if !c.IsModified("/a.go") {
		t.Fatal("expected cold /a.go to report modified")
	}
}

func TestEditorCache_DemotionPreservesUndoTree(t *testing.T) {
	c := NewEditorCache(CacheConfig{WarmCap: 1, ColdCap: 10})

	ws := makeWarm("/a.go", "aaa", false)
	ut := ws.UndoTree
	// Record an edit so we can verify identity.
	ut.Record(buffer.EditOp{Type: buffer.EditInsert, Pos: 0, Text: "x"})

	c.Put(ws)
	c.Put(makeWarm("/b.go", "bbb", false))
	// /a.go demoted to cold.

	entry, ok := c.Take("/a.go")
	if !ok {
		t.Fatal("expected /a.go in cache")
	}
	if entry.IsWarm() {
		t.Fatal("expected cold")
	}
	// Verify pointer identity — same UndoTree object, not a copy.
	if entry.Cold().UndoTree != ut {
		t.Fatal("expected same UndoTree pointer after demotion")
	}
	if !entry.Cold().UndoTree.CanUndo() {
		t.Fatal("expected undo history to survive demotion")
	}
}

func TestEditorCache_OverwriteEntry(t *testing.T) {
	c := NewEditorCache(CacheConfig{})

	c.Put(makeWarm("/a.go", "v1", false))
	c.Put(makeWarm("/a.go", "v2", true))

	if c.Len() != 1 {
		t.Fatalf("expected 1 entry after overwrite, got %d", c.Len())
	}

	entry, ok := c.Take("/a.go")
	if !ok {
		t.Fatal("expected /a.go")
	}
	if entry.Warm().Buf.Content() != "v2" {
		t.Fatalf("expected v2, got %q", entry.Warm().Buf.Content())
	}
	if !entry.Warm().Modified {
		t.Fatal("expected modified flag to be updated")
	}
}

func TestEditorCache_OverwriteColdEntry(t *testing.T) {
	c := NewEditorCache(CacheConfig{WarmCap: 1, ColdCap: 10})

	c.Put(makeWarm("/a.go", "v1", false))
	c.Put(makeWarm("/b.go", "bbb", false))
	// /a.go is now cold.

	// Re-put /a.go as warm — should replace the cold entry.
	c.Put(makeWarm("/a.go", "v2", false))

	if c.ColdCount() != 1 {
		t.Fatalf("expected 1 cold (/b.go), got %d", c.ColdCount())
	}
	if c.WarmCount() != 1 {
		t.Fatalf("expected 1 warm (/a.go), got %d", c.WarmCount())
	}

	entry, ok := c.Take("/a.go")
	if !ok {
		t.Fatal("expected /a.go")
	}
	if !entry.IsWarm() {
		t.Fatal("expected /a.go to be warm after re-put")
	}
	if entry.Warm().Buf.Content() != "v2" {
		t.Fatalf("expected v2, got %q", entry.Warm().Buf.Content())
	}
}
