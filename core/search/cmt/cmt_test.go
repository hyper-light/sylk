package cmt

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// makeCMTTestFileInfo creates a FileInfo for CMT testing with the given path.
func makeCMTTestFileInfo(path string) *FileInfo {
	return &FileInfo{
		Path:        path,
		ContentHash: makeCMTTestHash(path),
		Size:        100,
		ModTime:     time.Now(),
		Permissions: 0644,
		Indexed:     true,
		IndexedAt:   time.Now(),
	}
}

// makeCMTTestHash creates a deterministic test hash from a string.
func makeCMTTestHash(s string) Hash {
	var h Hash
	for i := 0; i < len(h) && i < len(s); i++ {
		h[i] = s[i]
	}
	return h
}

// newTestCMT creates a CMT with a temporary WAL for testing.
func newTestCMT(t *testing.T) (*CMT, string) {
	t.Helper()
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	cmt, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}

	return cmt, walPath
}

// =============================================================================
// NewCMT Tests
// =============================================================================

func TestNewCMT_Success(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	if cmt == nil {
		t.Fatal("expected non-nil CMT")
	}
	if cmt.Size() != 0 {
		t.Errorf("expected size 0, got %d", cmt.Size())
	}
	if !cmt.RootHash().IsZero() {
		t.Error("expected zero root hash for empty tree")
	}
}

func TestNewCMT_EmptyWALPath(t *testing.T) {
	_, err := NewCMT(CMTConfig{WALPath: ""})
	if err != ErrWALPathEmpty {
		t.Errorf("expected ErrWALPathEmpty, got %v", err)
	}
}

func TestNewCMTWithWAL(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	wal, err := NewWAL(WALConfig{Path: walPath})
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	defer wal.Close()

	cmt := NewCMTWithWAL(wal)
	if cmt == nil {
		t.Fatal("expected non-nil CMT")
	}
	if cmt.Size() != 0 {
		t.Errorf("expected size 0, got %d", cmt.Size())
	}
}

// =============================================================================
// Insert Tests
// =============================================================================

func TestCMT_Insert_NewEntry(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	info := makeCMTTestFileInfo("/path/to/file.go")
	err := cmt.Insert("/path/to/file.go", info)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	if cmt.Size() != 1 {
		t.Errorf("expected size 1, got %d", cmt.Size())
	}

	retrieved := cmt.Get("/path/to/file.go")
	if retrieved == nil {
		t.Fatal("expected to retrieve inserted entry")
	}
	if retrieved.Path != info.Path {
		t.Errorf("expected path %q, got %q", info.Path, retrieved.Path)
	}
}

func TestCMT_Insert_UpdatesExisting(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Insert initial entry
	info1 := makeCMTTestFileInfo("/path/to/file.go")
	info1.Size = 100
	if err := cmt.Insert("/path/to/file.go", info1); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	// Update with new info
	info2 := makeCMTTestFileInfo("/path/to/file.go")
	info2.Size = 200
	if err := cmt.Insert("/path/to/file.go", info2); err != nil {
		t.Fatalf("second insert failed: %v", err)
	}

	// Size should remain 1 (upsert behavior)
	if cmt.Size() != 1 {
		t.Errorf("expected size 1, got %d", cmt.Size())
	}

	// Should have updated value
	retrieved := cmt.Get("/path/to/file.go")
	if retrieved == nil {
		t.Fatal("expected to retrieve entry")
	}
	if retrieved.Size != 200 {
		t.Errorf("expected size 200, got %d", retrieved.Size)
	}
}

func TestCMT_Insert_MultipleEntries(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	keys := []string{
		"/a/file1.go",
		"/b/file2.go",
		"/c/file3.go",
		"/a/file4.go",
		"/z/file5.go",
	}

	for _, key := range keys {
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert %q failed: %v", key, err)
		}
	}

	if cmt.Size() != int64(len(keys)) {
		t.Errorf("expected size %d, got %d", len(keys), cmt.Size())
	}

	for _, key := range keys {
		if cmt.Get(key) == nil {
			t.Errorf("expected to find key %q", key)
		}
	}
}

func TestCMT_Insert_NilInfo(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	err := cmt.Insert("/path/to/file.go", nil)
	if err != ErrWALFileInfoNil {
		t.Errorf("expected ErrWALFileInfoNil, got %v", err)
	}
}

func TestCMT_Insert_EmptyKey(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	err := cmt.Insert("", makeCMTTestFileInfo("/path"))
	if err != ErrWALKeyEmpty {
		t.Errorf("expected ErrWALKeyEmpty, got %v", err)
	}
}

// =============================================================================
// Delete Tests
// =============================================================================

func TestCMT_Delete_ExistingEntry(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	key := "/path/to/file.go"
	if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	if err := cmt.Delete(key); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if cmt.Size() != 0 {
		t.Errorf("expected size 0, got %d", cmt.Size())
	}

	if cmt.Get(key) != nil {
		t.Error("expected nil after delete")
	}
}

func TestCMT_Delete_NonExistent(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Insert one entry
	if err := cmt.Insert("/exists.go", makeCMTTestFileInfo("/exists.go")); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Delete non-existent entry should be no-op
	err := cmt.Delete("/nonexistent.go")
	if err != nil {
		t.Fatalf("delete non-existent failed: %v", err)
	}

	// Original entry should still exist
	if cmt.Size() != 1 {
		t.Errorf("expected size 1, got %d", cmt.Size())
	}
	if cmt.Get("/exists.go") == nil {
		t.Error("existing entry should still be present")
	}
}

func TestCMT_Delete_EmptyTree(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Delete from empty tree should be no-op
	err := cmt.Delete("/nonexistent.go")
	if err != nil {
		t.Fatalf("delete from empty tree failed: %v", err)
	}

	if cmt.Size() != 0 {
		t.Errorf("expected size 0, got %d", cmt.Size())
	}
}

func TestCMT_Delete_EmptyKey(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	err := cmt.Delete("")
	if err != ErrWALKeyEmpty {
		t.Errorf("expected ErrWALKeyEmpty, got %v", err)
	}
}

func TestCMT_Delete_MultipleEntries(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	keys := []string{"/a.go", "/b.go", "/c.go", "/d.go", "/e.go"}
	for _, key := range keys {
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	// Delete middle entry
	if err := cmt.Delete("/c.go"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if cmt.Size() != 4 {
		t.Errorf("expected size 4, got %d", cmt.Size())
	}

	// Verify remaining entries
	for _, key := range keys {
		if key == "/c.go" {
			if cmt.Get(key) != nil {
				t.Errorf("expected /c.go to be deleted")
			}
		} else {
			if cmt.Get(key) == nil {
				t.Errorf("expected %q to exist", key)
			}
		}
	}
}

// =============================================================================
// Get Tests
// =============================================================================

func TestCMT_Get_Existing(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	key := "/path/to/file.go"
	info := makeCMTTestFileInfo(key)
	if err := cmt.Insert(key, info); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	retrieved := cmt.Get(key)
	if retrieved == nil {
		t.Fatal("expected non-nil result")
	}
	if retrieved.Path != info.Path {
		t.Errorf("expected path %q, got %q", info.Path, retrieved.Path)
	}
}

func TestCMT_Get_NonExistent(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	result := cmt.Get("/nonexistent.go")
	if result != nil {
		t.Errorf("expected nil for non-existent key, got %+v", result)
	}
}

func TestCMT_Get_EmptyTree(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	result := cmt.Get("/any.go")
	if result != nil {
		t.Errorf("expected nil for empty tree, got %+v", result)
	}
}

// =============================================================================
// Update Tests
// =============================================================================

func TestCMT_Update(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	key := "/path/to/file.go"

	// Insert initial entry
	info1 := makeCMTTestFileInfo(key)
	info1.Size = 100
	if err := cmt.Insert(key, info1); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Update entry
	info2 := makeCMTTestFileInfo(key)
	info2.Size = 200
	if err := cmt.Update(key, info2); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	retrieved := cmt.Get(key)
	if retrieved == nil {
		t.Fatal("expected to retrieve entry")
	}
	if retrieved.Size != 200 {
		t.Errorf("expected size 200, got %d", retrieved.Size)
	}

	// Size should remain 1
	if cmt.Size() != 1 {
		t.Errorf("expected size 1, got %d", cmt.Size())
	}
}

// =============================================================================
// RootHash Tests
// =============================================================================

func TestCMT_RootHash_EmptyTree(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	if !cmt.RootHash().IsZero() {
		t.Error("expected zero hash for empty tree")
	}
}

func TestCMT_RootHash_ChangesWithMutations(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	hash0 := cmt.RootHash()
	if !hash0.IsZero() {
		t.Error("expected zero hash initially")
	}

	// Insert changes hash
	if err := cmt.Insert("/file1.go", makeCMTTestFileInfo("/file1.go")); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	hash1 := cmt.RootHash()
	if hash1.IsZero() {
		t.Error("expected non-zero hash after insert")
	}
	if hash1 == hash0 {
		t.Error("expected hash to change after insert")
	}

	// Another insert changes hash
	if err := cmt.Insert("/file2.go", makeCMTTestFileInfo("/file2.go")); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	hash2 := cmt.RootHash()
	if hash2 == hash1 {
		t.Error("expected hash to change after second insert")
	}

	// Delete changes hash
	if err := cmt.Delete("/file1.go"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	hash3 := cmt.RootHash()
	if hash3 == hash2 {
		t.Error("expected hash to change after delete")
	}

	// Update changes hash (must change ContentHash for root hash to change)
	info := makeCMTTestFileInfo("/file2.go")
	info.Size = 999
	info.ContentHash[0] = 0xFF // Modify content hash to trigger root hash change
	if err := cmt.Update("/file2.go", info); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	hash4 := cmt.RootHash()
	if hash4 == hash3 {
		t.Error("expected hash to change after update")
	}
}

func TestCMT_RootHash_IsO1(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Insert many entries
	for i := 0; i < 1000; i++ {
		key := filepath.Join("/path", string(rune('a'+i%26)), "file.go")
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	// RootHash should be O(1) - just returning stored value
	start := time.Now()
	for i := 0; i < 10000; i++ {
		_ = cmt.RootHash()
	}
	elapsed := time.Since(start)

	// Should complete very quickly (< 10ms for 10000 calls)
	if elapsed > 100*time.Millisecond {
		t.Errorf("RootHash took too long: %v (expected O(1))", elapsed)
	}
}

// =============================================================================
// Size Tests
// =============================================================================

func TestCMT_Size_TracksCorrectly(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	if cmt.Size() != 0 {
		t.Errorf("expected size 0, got %d", cmt.Size())
	}

	// Insert increments
	for i := 1; i <= 5; i++ {
		key := filepath.Join("/file", string(rune('a'+i-1)), "test.go")
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
		if cmt.Size() != int64(i) {
			t.Errorf("after %d inserts, expected size %d, got %d", i, i, cmt.Size())
		}
	}

	// Delete decrements
	if err := cmt.Delete("/file/a/test.go"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if cmt.Size() != 4 {
		t.Errorf("after delete, expected size 4, got %d", cmt.Size())
	}

	// Update does not change size
	if err := cmt.Update("/file/b/test.go", makeCMTTestFileInfo("/file/b/test.go")); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if cmt.Size() != 4 {
		t.Errorf("after update, expected size 4, got %d", cmt.Size())
	}
}

// =============================================================================
// Walk Tests
// =============================================================================

func TestCMT_Walk_InSortedOrder(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	keys := []string{"/z.go", "/a.go", "/m.go", "/b.go", "/y.go"}
	for _, key := range keys {
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	var walked []string
	cmt.Walk(func(key string, info *FileInfo) bool {
		walked = append(walked, key)
		return true
	})

	expected := make([]string, len(keys))
	copy(expected, keys)
	sort.Strings(expected)

	if len(walked) != len(expected) {
		t.Errorf("expected %d entries, got %d", len(expected), len(walked))
	}

	for i, key := range expected {
		if walked[i] != key {
			t.Errorf("at index %d: expected %q, got %q", i, key, walked[i])
		}
	}
}

func TestCMT_Walk_StopsOnFalse(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	for i := 0; i < 10; i++ {
		key := filepath.Join("/file", string(rune('a'+i)), "test.go")
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	count := 0
	cmt.Walk(func(key string, info *FileInfo) bool {
		count++
		return count < 3 // Stop after 3
	})

	if count != 3 {
		t.Errorf("expected 3 iterations, got %d", count)
	}
}

func TestCMT_Walk_EmptyTree(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	count := 0
	cmt.Walk(func(key string, info *FileInfo) bool {
		count++
		return true
	})

	if count != 0 {
		t.Errorf("expected 0 iterations on empty tree, got %d", count)
	}
}

// =============================================================================
// WAL Logging Tests
// =============================================================================

func TestCMT_WALLogging_Insert(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	cmt, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}

	if err := cmt.Insert("/file.go", makeCMTTestFileInfo("/file.go")); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	cmt.Close()

	// Read WAL directly to verify logging
	wal, err := NewWAL(WALConfig{Path: walPath})
	if err != nil {
		t.Fatalf("failed to open WAL: %v", err)
	}
	defer wal.Close()

	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].Op != WALOpInsert {
		t.Errorf("expected INSERT op, got %v", entries[0].Op)
	}
	if entries[0].Key != "/file.go" {
		t.Errorf("expected key /file.go, got %q", entries[0].Key)
	}
}

func TestCMT_WALLogging_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	cmt, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}

	if err := cmt.Insert("/file.go", makeCMTTestFileInfo("/file.go")); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	if err := cmt.Delete("/file.go"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	cmt.Close()

	// Read WAL
	wal, err := NewWAL(WALConfig{Path: walPath})
	if err != nil {
		t.Fatalf("failed to open WAL: %v", err)
	}
	defer wal.Close()

	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[1].Op != WALOpDelete {
		t.Errorf("expected DELETE op, got %v", entries[1].Op)
	}
	if entries[1].Key != "/file.go" {
		t.Errorf("expected key /file.go, got %q", entries[1].Key)
	}
}

// =============================================================================
// Recover Tests
// =============================================================================

func TestCMT_Recover_RebuildFromWAL(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	// Create and populate CMT
	cmt1, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}

	keys := []string{"/a.go", "/b.go", "/c.go"}
	for _, key := range keys {
		if err := cmt1.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}
	// Delete one
	if err := cmt1.Delete("/b.go"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	originalHash := cmt1.RootHash()
	cmt1.Close()

	// Create new CMT with same WAL
	cmt2, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}
	defer cmt2.Close()

	// Recover from WAL
	if err := cmt2.Recover(); err != nil {
		t.Fatalf("recover failed: %v", err)
	}

	// Verify state matches
	if cmt2.Size() != 2 {
		t.Errorf("expected size 2, got %d", cmt2.Size())
	}
	if cmt2.Get("/a.go") == nil {
		t.Error("expected /a.go to exist")
	}
	if cmt2.Get("/b.go") != nil {
		t.Error("expected /b.go to be deleted")
	}
	if cmt2.Get("/c.go") == nil {
		t.Error("expected /c.go to exist")
	}

	if cmt2.RootHash() != originalHash {
		t.Error("expected recovered tree to have same root hash")
	}
}

func TestCMT_Recover_EmptyWAL(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Recover from empty WAL should succeed
	err := cmt.Recover()
	if err != nil {
		t.Fatalf("recover from empty WAL failed: %v", err)
	}

	if cmt.Size() != 0 {
		t.Errorf("expected size 0, got %d", cmt.Size())
	}
}

// =============================================================================
// Checkpoint Tests
// =============================================================================

func TestCMT_Checkpoint(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	cmt, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}
	defer cmt.Close()

	if err := cmt.Insert("/file.go", makeCMTTestFileInfo("/file.go")); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Checkpoint should succeed
	if err := cmt.Checkpoint(); err != nil {
		t.Fatalf("checkpoint failed: %v", err)
	}

	// WAL should have checkpoint marker
	wal, err := NewWAL(WALConfig{Path: walPath})
	if err != nil {
		t.Fatalf("failed to open WAL: %v", err)
	}
	defer wal.Close()

	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("recover failed: %v", err)
	}

	hasCheckpoint := false
	for _, e := range entries {
		if e.Op == WALOpCheckpoint {
			hasCheckpoint = true
			break
		}
	}
	if !hasCheckpoint {
		t.Error("expected checkpoint entry in WAL")
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestCMT_Close(t *testing.T) {
	cmt, _ := newTestCMT(t)

	if err := cmt.Insert("/file.go", makeCMTTestFileInfo("/file.go")); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	err := cmt.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Operations after close should fail
	err = cmt.Insert("/another.go", makeCMTTestFileInfo("/another.go"))
	if err != ErrWALClosed {
		t.Errorf("expected ErrWALClosed after close, got %v", err)
	}
}

// =============================================================================
// Empty Tree Operations
// =============================================================================

func TestCMT_EmptyTreeOperations(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Get on empty tree
	if cmt.Get("/any.go") != nil {
		t.Error("expected nil for get on empty tree")
	}

	// Size on empty tree
	if cmt.Size() != 0 {
		t.Errorf("expected size 0, got %d", cmt.Size())
	}

	// RootHash on empty tree
	if !cmt.RootHash().IsZero() {
		t.Error("expected zero hash for empty tree")
	}

	// Walk on empty tree
	count := 0
	cmt.Walk(func(key string, info *FileInfo) bool {
		count++
		return true
	})
	if count != 0 {
		t.Errorf("expected 0 walks, got %d", count)
	}

	// Delete on empty tree
	if err := cmt.Delete("/any.go"); err != nil {
		t.Fatalf("delete on empty tree failed: %v", err)
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestCMT_ConcurrentReads(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Insert some entries
	for i := 0; i < 100; i++ {
		key := filepath.Join("/file", string(rune('a'+i%26)), "test.go")
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	// Concurrent reads should be safe
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = cmt.Get("/file/a/test.go")
				_ = cmt.RootHash()
				_ = cmt.Size()
				cmt.Walk(func(key string, info *FileInfo) bool {
					return true
				})
			}
		}()
	}
	wg.Wait()
}

func TestCMT_ConcurrentWrites(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	var wg sync.WaitGroup
	errors := make(chan error, 100)

	// Concurrent inserts
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				key := filepath.Join("/file", string(rune('a'+id)), string(rune('0'+j)), "test.go")
				if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
					errors <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent insert error: %v", err)
	}

	// Should have 100 entries (10 goroutines * 10 inserts each)
	if cmt.Size() != 100 {
		t.Errorf("expected size 100, got %d", cmt.Size())
	}
}

func TestCMT_ConcurrentReadsAndWrites(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				select {
				case <-stop:
					return
				default:
				}
				key := filepath.Join("/file", string(rune('a'+id)), string(rune('0'+j%10)), "test.go")
				_ = cmt.Insert(key, makeCMTTestFileInfo(key))
			}
		}(i)
	}

	// Readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = cmt.Get("/file/a/0/test.go")
				_ = cmt.RootHash()
				_ = cmt.Size()
			}
		}()
	}

	wg.Wait()
}

func TestCMT_ConcurrentDeletesAndInserts(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Pre-populate
	for i := 0; i < 50; i++ {
		key := filepath.Join("/file", string(rune('a'+i%26)), "test.go")
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	var wg sync.WaitGroup

	// Deletes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				key := filepath.Join("/file", string(rune('a'+((id*10+j)%26))), "test.go")
				_ = cmt.Delete(key)
			}
		}(i)
	}

	// Inserts
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				key := filepath.Join("/new", string(rune('a'+id)), string(rune('0'+j)), "test.go")
				_ = cmt.Insert(key, makeCMTTestFileInfo(key))
			}
		}(i)
	}

	wg.Wait()

	// Size should be consistent and >= 0
	size := cmt.Size()
	if size < 0 {
		t.Errorf("size should be >= 0, got %d", size)
	}
}

// =============================================================================
// Stress Tests
// =============================================================================

func TestCMT_StressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Insert many entries with unique keys
	numEntries := 1000
	for i := 0; i < numEntries; i++ {
		// Generate unique key using index directly
		key := filepath.Join("/stress", "file"+strconv.Itoa(i)+".go")
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert %d failed: %v", i, err)
		}
	}

	// Verify operations still work correctly
	if cmt.Size() != int64(numEntries) {
		t.Errorf("expected size %d, got %d", numEntries, cmt.Size())
	}

	// Walk should visit all entries in order
	count := 0
	var prev string
	cmt.Walk(func(key string, info *FileInfo) bool {
		if prev != "" && key < prev {
			t.Errorf("walk not in sorted order: %q < %q", key, prev)
		}
		prev = key
		count++
		return true
	})

	if count != numEntries {
		t.Errorf("walk visited %d entries, expected %d", count, numEntries)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkCMT_Insert(b *testing.B) {
	tmpDir := b.TempDir()
	walPath := filepath.Join(tmpDir, "bench.wal")

	cmt, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		b.Fatalf("failed to create CMT: %v", err)
	}
	defer cmt.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := filepath.Join("/bench", string(rune('a'+i%26)), "file.go")
		_ = cmt.Insert(key, makeCMTTestFileInfo(key))
	}
}

func BenchmarkCMT_Get(b *testing.B) {
	tmpDir := b.TempDir()
	walPath := filepath.Join(tmpDir, "bench.wal")

	cmt, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		b.Fatalf("failed to create CMT: %v", err)
	}
	defer cmt.Close()

	// Pre-populate
	for i := 0; i < 1000; i++ {
		key := filepath.Join("/bench", string(rune('a'+i%26)), "file.go")
		_ = cmt.Insert(key, makeCMTTestFileInfo(key))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cmt.Get("/bench/m/file.go")
	}
}

func BenchmarkCMT_RootHash(b *testing.B) {
	tmpDir := b.TempDir()
	walPath := filepath.Join(tmpDir, "bench.wal")

	cmt, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		b.Fatalf("failed to create CMT: %v", err)
	}
	defer cmt.Close()

	// Pre-populate
	for i := 0; i < 1000; i++ {
		key := filepath.Join("/bench", string(rune('a'+i%26)), "file.go")
		_ = cmt.Insert(key, makeCMTTestFileInfo(key))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cmt.RootHash()
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestCMT_Integration_FullLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "integration.wal")

	// Phase 1: Create and populate
	cmt1, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: true,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}

	files := []string{
		"/project/main.go",
		"/project/utils/helpers.go",
		"/project/models/user.go",
		"/project/api/handlers.go",
		"/project/README.md",
	}

	for _, f := range files {
		if err := cmt1.Insert(f, makeCMTTestFileInfo(f)); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}

	// Verify initial state
	if cmt1.Size() != 5 {
		t.Errorf("expected size 5, got %d", cmt1.Size())
	}

	// Update a file
	updatedInfo := makeCMTTestFileInfo("/project/main.go")
	updatedInfo.Size = 999
	if err := cmt1.Update("/project/main.go", updatedInfo); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	// Delete a file
	if err := cmt1.Delete("/project/README.md"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Checkpoint
	if err := cmt1.Checkpoint(); err != nil {
		t.Fatalf("checkpoint failed: %v", err)
	}

	hash1 := cmt1.RootHash()
	cmt1.Close()

	// Phase 2: Recover from WAL
	cmt2, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: true,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}
	defer cmt2.Close()

	if err := cmt2.Recover(); err != nil {
		t.Fatalf("recover failed: %v", err)
	}

	// Verify recovered state
	if cmt2.Size() != 4 {
		t.Errorf("expected size 4, got %d", cmt2.Size())
	}

	if cmt2.RootHash() != hash1 {
		t.Error("recovered hash should match original")
	}

	// Verify specific entries
	if cmt2.Get("/project/main.go") == nil {
		t.Error("expected /project/main.go to exist")
	}
	if cmt2.Get("/project/README.md") != nil {
		t.Error("expected /project/README.md to be deleted")
	}

	// Verify walk order
	var walkedKeys []string
	cmt2.Walk(func(key string, info *FileInfo) bool {
		walkedKeys = append(walkedKeys, key)
		return true
	})

	expectedOrder := []string{
		"/project/api/handlers.go",
		"/project/main.go",
		"/project/models/user.go",
		"/project/utils/helpers.go",
	}

	if len(walkedKeys) != len(expectedOrder) {
		t.Errorf("walk returned %d keys, expected %d", len(walkedKeys), len(expectedOrder))
	}

	for i, key := range expectedOrder {
		if i < len(walkedKeys) && walkedKeys[i] != key {
			t.Errorf("walk order mismatch at %d: got %q, expected %q", i, walkedKeys[i], key)
		}
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestCMT_LongKeys(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// Very long key
	longKey := "/this/is/a/very/long/path/that/might/cause/issues/in/some/implementations/file.go"
	if err := cmt.Insert(longKey, makeCMTTestFileInfo(longKey)); err != nil {
		t.Fatalf("insert long key failed: %v", err)
	}

	if cmt.Get(longKey) == nil {
		t.Error("expected to retrieve long key")
	}
}

func TestCMT_SpecialCharactersInKeys(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	specialKeys := []string{
		"/path/with spaces/file.go",
		"/path/with-dashes/file.go",
		"/path/with_underscores/file.go",
		"/path/with.dots/file.go",
		"/path/with@special/file.go",
	}

	for _, key := range specialKeys {
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert %q failed: %v", key, err)
		}
	}

	for _, key := range specialKeys {
		if cmt.Get(key) == nil {
			t.Errorf("expected to retrieve %q", key)
		}
	}
}

func TestCMT_InsertDeleteSameKey(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	key := "/file.go"

	// Insert and delete multiple times
	for i := 0; i < 10; i++ {
		if err := cmt.Insert(key, makeCMTTestFileInfo(key)); err != nil {
			t.Fatalf("insert %d failed: %v", i, err)
		}
		if cmt.Size() != 1 {
			t.Errorf("after insert %d, expected size 1, got %d", i, cmt.Size())
		}

		if err := cmt.Delete(key); err != nil {
			t.Fatalf("delete %d failed: %v", i, err)
		}
		if cmt.Size() != 0 {
			t.Errorf("after delete %d, expected size 0, got %d", i, cmt.Size())
		}
	}
}

// =============================================================================
// Storage Interface Tests (placeholder for future implementation)
// =============================================================================

func TestCMT_NilStorage(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	// CMT should work without storage (WAL-only mode)
	if err := cmt.Insert("/file.go", makeCMTTestFileInfo("/file.go")); err != nil {
		t.Fatalf("insert failed: %v", err)
	}
	if cmt.Size() != 1 {
		t.Errorf("expected size 1, got %d", cmt.Size())
	}
}

// =============================================================================
// Closed CMT Tests
// =============================================================================

func TestCMT_OperationsAfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	cmt, err := NewCMT(CMTConfig{
		WALPath:     walPath,
		SyncOnWrite: false,
	})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}

	if err := cmt.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Insert should fail
	if err := cmt.Insert("/file.go", makeCMTTestFileInfo("/file.go")); err != ErrWALClosed {
		t.Errorf("expected ErrWALClosed, got %v", err)
	}

	// Delete should fail
	if err := cmt.Delete("/file.go"); err != ErrWALClosed {
		t.Errorf("expected ErrWALClosed, got %v", err)
	}

	// Update should fail
	if err := cmt.Update("/file.go", makeCMTTestFileInfo("/file.go")); err != ErrWALClosed {
		t.Errorf("expected ErrWALClosed, got %v", err)
	}

	// Checkpoint should fail
	if err := cmt.Checkpoint(); err != ErrWALClosed {
		t.Errorf("expected ErrWALClosed, got %v", err)
	}
}

// =============================================================================
// WAL Recovery Edge Cases
// =============================================================================

func TestCMT_RecoverWithMixedOperations(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	// Create CMT and perform mixed operations
	cmt1, err := NewCMT(CMTConfig{WALPath: walPath, SyncOnWrite: false})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}

	// Insert, update, delete, insert again
	_ = cmt1.Insert("/a.go", makeCMTTestFileInfo("/a.go"))
	_ = cmt1.Insert("/b.go", makeCMTTestFileInfo("/b.go"))
	_ = cmt1.Insert("/c.go", makeCMTTestFileInfo("/c.go"))
	_ = cmt1.Delete("/b.go")
	info := makeCMTTestFileInfo("/a.go")
	info.Size = 999
	_ = cmt1.Update("/a.go", info)
	_ = cmt1.Insert("/d.go", makeCMTTestFileInfo("/d.go"))
	_ = cmt1.Delete("/c.go")
	_ = cmt1.Delete("/d.go")
	_ = cmt1.Insert("/e.go", makeCMTTestFileInfo("/e.go"))

	finalSize := cmt1.Size()
	finalHash := cmt1.RootHash()
	cmt1.Close()

	// Recover
	cmt2, err := NewCMT(CMTConfig{WALPath: walPath, SyncOnWrite: false})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}
	defer cmt2.Close()

	if err := cmt2.Recover(); err != nil {
		t.Fatalf("recover failed: %v", err)
	}

	if cmt2.Size() != finalSize {
		t.Errorf("expected size %d, got %d", finalSize, cmt2.Size())
	}
	if cmt2.RootHash() != finalHash {
		t.Error("expected same root hash after recovery")
	}
}

// =============================================================================
// Node Cloning Test (to verify immutability)
// =============================================================================

func TestCMT_GetReturnsClone(t *testing.T) {
	cmt, _ := newTestCMT(t)
	defer cmt.Close()

	key := "/file.go"
	originalInfo := makeCMTTestFileInfo(key)
	originalInfo.Size = 100
	if err := cmt.Insert(key, originalInfo); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Get returns a value
	retrieved := cmt.Get(key)
	if retrieved == nil {
		t.Fatal("expected non-nil result")
	}

	// Modifying retrieved should not affect stored value
	retrieved.Size = 999

	// Re-fetch should have original value
	fetched := cmt.Get(key)
	if fetched.Size != 100 {
		t.Errorf("expected original size 100, got %d (mutation leaked)", fetched.Size)
	}
}

// =============================================================================
// File Existence Check
// =============================================================================

func TestCMT_WALFileCreated(t *testing.T) {
	tmpDir := t.TempDir()
	walPath := filepath.Join(tmpDir, "created.wal")

	// Verify file doesn't exist yet
	if _, err := os.Stat(walPath); !os.IsNotExist(err) {
		t.Fatal("WAL file should not exist yet")
	}

	cmt, err := NewCMT(CMTConfig{WALPath: walPath})
	if err != nil {
		t.Fatalf("failed to create CMT: %v", err)
	}
	defer cmt.Close()

	// Verify file was created
	if _, err := os.Stat(walPath); os.IsNotExist(err) {
		t.Error("WAL file should have been created")
	}
}
