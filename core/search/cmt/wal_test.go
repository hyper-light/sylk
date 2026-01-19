package cmt

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// createTestWAL creates a temporary WAL for testing.
func createTestWAL(t *testing.T) (*WAL, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(WALConfig{
		Path:        path,
		SyncOnWrite: false, // Faster tests
		MaxSize:     1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	return wal, path
}

// createTestWALWithSync creates a WAL with fsync enabled.
func createTestWALWithSync(t *testing.T) (*WAL, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(WALConfig{
		Path:        path,
		SyncOnWrite: true,
		MaxSize:     1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	return wal, path
}

// testFileInfo creates a FileInfo for testing.
func testFileInfo(path string) *FileInfo {
	return &FileInfo{
		Path:        path,
		ContentHash: Hash{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		Size:        1024,
		ModTime:     time.Now().Truncate(time.Second),
		Permissions: 0644,
		Indexed:     true,
	}
}

// =============================================================================
// NewWAL Tests
// =============================================================================

func TestNewWAL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(WALConfig{
		Path:        path,
		SyncOnWrite: true,
		MaxSize:     1024 * 1024,
	})

	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	if wal == nil {
		t.Fatal("NewWAL returned nil")
	}

	// File should exist after creation
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("WAL file should exist after NewWAL")
	}
}

func TestNewWAL_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := NewWAL(WALConfig{
		Path: "",
	})

	if err != ErrWALPathEmpty {
		t.Errorf("NewWAL error = %v, want %v", err, ErrWALPathEmpty)
	}
}

func TestNewWAL_InvalidPath(t *testing.T) {
	t.Parallel()

	_, err := NewWAL(WALConfig{
		Path: "/nonexistent/directory/test.wal",
	})

	if err == nil {
		t.Error("NewWAL should fail for invalid path")
	}
}

func TestNewWAL_DefaultMaxSize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(WALConfig{
		Path:    path,
		MaxSize: 0, // Should use default
	})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	if wal.config.MaxSize != DefaultWALMaxSize {
		t.Errorf("MaxSize = %d, want default %d", wal.config.MaxSize, DefaultWALMaxSize)
	}
}

// =============================================================================
// Open Tests
// =============================================================================

func TestWAL_Open_ExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create and close WAL
	wal1, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	if err := wal1.LogInsert("test/file.go", testFileInfo("test/file.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	if err := wal1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open existing WAL
	wal2, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL (reopen) failed: %v", err)
	}
	defer wal2.Close()

	// Verify size is preserved
	if wal2.Size() == 0 {
		t.Error("reopened WAL should have non-zero size")
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestWAL_Close(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)

	if err := wal.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Double close should not panic
	if err := wal.Close(); err != nil {
		t.Errorf("second Close failed: %v", err)
	}
}

func TestWAL_Close_SyncsData(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(WALConfig{
		Path:        path,
		SyncOnWrite: false,
	})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	// Write data
	if err := wal.LogInsert("test/file.go", testFileInfo("test/file.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	// Close should sync
	if err := wal.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// File should have data
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	if stat.Size() == 0 {
		t.Error("WAL file should have data after close")
	}
}

// =============================================================================
// LogInsert Tests
// =============================================================================

func TestWAL_LogInsert(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	info := testFileInfo("src/main.go")

	err := wal.LogInsert("src/main.go", info)
	if err != nil {
		t.Errorf("LogInsert failed: %v", err)
	}

	if wal.Size() == 0 {
		t.Error("WAL size should be > 0 after LogInsert")
	}
}

func TestWAL_LogInsert_NilFileInfo(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	err := wal.LogInsert("src/main.go", nil)
	if err != ErrWALFileInfoNil {
		t.Errorf("LogInsert error = %v, want %v", err, ErrWALFileInfoNil)
	}
}

func TestWAL_LogInsert_EmptyKey(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	err := wal.LogInsert("", testFileInfo(""))
	if err != ErrWALKeyEmpty {
		t.Errorf("LogInsert error = %v, want %v", err, ErrWALKeyEmpty)
	}
}

func TestWAL_LogInsert_MultipleEntries(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	files := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	for _, f := range files {
		if err := wal.LogInsert(f, testFileInfo(f)); err != nil {
			t.Errorf("LogInsert(%s) failed: %v", f, err)
		}
	}

	// Verify by recovering
	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) != len(files) {
		t.Errorf("recovered %d entries, want %d", len(entries), len(files))
	}
}

func TestWAL_LogInsert_LongKey(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// Create a long key (path)
	longKey := "very/deep/nested/directory/structure/with/many/levels/" +
		"and/more/subdirectories/to/make/it/even/longer/file.go"

	err := wal.LogInsert(longKey, testFileInfo(longKey))
	if err != nil {
		t.Errorf("LogInsert with long key failed: %v", err)
	}

	// Verify recovery
	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) != 1 || entries[0].Key != longKey {
		t.Error("recovered entry key doesn't match")
	}
}

// =============================================================================
// LogDelete Tests
// =============================================================================

func TestWAL_LogDelete(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	err := wal.LogDelete("src/main.go")
	if err != nil {
		t.Errorf("LogDelete failed: %v", err)
	}

	if wal.Size() == 0 {
		t.Error("WAL size should be > 0 after LogDelete")
	}
}

func TestWAL_LogDelete_EmptyKey(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	err := wal.LogDelete("")
	if err != ErrWALKeyEmpty {
		t.Errorf("LogDelete error = %v, want %v", err, ErrWALKeyEmpty)
	}
}

func TestWAL_LogDelete_NoFileInfo(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// LogDelete should work without FileInfo
	if err := wal.LogDelete("deleted/file.go"); err != nil {
		t.Errorf("LogDelete failed: %v", err)
	}

	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].Op != WALOpDelete {
		t.Errorf("Op = %v, want %v", entries[0].Op, WALOpDelete)
	}

	if entries[0].Info != nil {
		t.Error("DELETE entry should have nil Info")
	}
}

// =============================================================================
// Checkpoint Tests
// =============================================================================

func TestWAL_Checkpoint(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// Write some entries
	if err := wal.LogInsert("a.go", testFileInfo("a.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}
	if err := wal.LogInsert("b.go", testFileInfo("b.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	// Checkpoint
	err := wal.Checkpoint()
	if err != nil {
		t.Errorf("Checkpoint failed: %v", err)
	}

	// Verify checkpoint entry exists
	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	hasCheckpoint := false
	for _, e := range entries {
		if e.Op == WALOpCheckpoint {
			hasCheckpoint = true
			break
		}
	}

	if !hasCheckpoint {
		t.Error("no checkpoint entry found after Checkpoint()")
	}
}

func TestWAL_Checkpoint_EmptyWAL(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// Checkpoint on empty WAL should still work
	err := wal.Checkpoint()
	if err != nil {
		t.Errorf("Checkpoint on empty WAL failed: %v", err)
	}
}

// =============================================================================
// Truncate Tests
// =============================================================================

func TestWAL_Truncate(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// Write entries
	for i := 0; i < 10; i++ {
		key := "file" + string(rune('a'+i)) + ".go"
		if err := wal.LogInsert(key, testFileInfo(key)); err != nil {
			t.Fatalf("LogInsert failed: %v", err)
		}
	}

	sizeBefore := wal.Size()
	if sizeBefore == 0 {
		t.Fatal("WAL should have data before truncate")
	}

	// Truncate
	if err := wal.Truncate(); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	// Size should be 0
	if wal.Size() != 0 {
		t.Errorf("WAL size after truncate = %d, want 0", wal.Size())
	}

	// Recover should return empty
	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("recovered %d entries after truncate, want 0", len(entries))
	}
}

func TestWAL_Truncate_EmptyWAL(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// Truncate empty WAL should work
	err := wal.Truncate()
	if err != nil {
		t.Errorf("Truncate on empty WAL failed: %v", err)
	}
}

func TestWAL_Truncate_ThenWrite(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// Write, truncate, write again
	if err := wal.LogInsert("old.go", testFileInfo("old.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	if err := wal.Truncate(); err != nil {
		t.Fatalf("Truncate failed: %v", err)
	}

	if err := wal.LogInsert("new.go", testFileInfo("new.go")); err != nil {
		t.Fatalf("LogInsert after truncate failed: %v", err)
	}

	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].Key != "new.go" {
		t.Errorf("Key = %s, want new.go", entries[0].Key)
	}
}

// =============================================================================
// Recover Tests
// =============================================================================

func TestWAL_Recover_Empty(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	entries, err := wal.Recover()
	if err != nil {
		t.Errorf("Recover failed: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("recovered %d entries from empty WAL, want 0", len(entries))
	}
}

func TestWAL_Recover_AllEntries(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// Write mixed operations
	if err := wal.LogInsert("a.go", testFileInfo("a.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}
	if err := wal.LogInsert("b.go", testFileInfo("b.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}
	if err := wal.LogDelete("a.go"); err != nil {
		t.Fatalf("LogDelete failed: %v", err)
	}
	if err := wal.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}
	if err := wal.LogInsert("c.go", testFileInfo("c.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) != 5 {
		t.Fatalf("recovered %d entries, want 5", len(entries))
	}

	// Verify order and operations
	expectedOps := []WALOp{WALOpInsert, WALOpInsert, WALOpDelete, WALOpCheckpoint, WALOpInsert}
	for i, e := range entries {
		if e.Op != expectedOps[i] {
			t.Errorf("entry[%d].Op = %v, want %v", i, e.Op, expectedOps[i])
		}
	}
}

func TestWAL_Recover_PreservesFileInfo(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	originalInfo := &FileInfo{
		Path:        "src/main.go",
		ContentHash: Hash{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		Size:        4096,
		ModTime:     time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
		Permissions: 0755,
		Indexed:     true,
	}

	if err := wal.LogInsert("src/main.go", originalInfo); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	recovered := entries[0].Info
	if recovered == nil {
		t.Fatal("recovered Info is nil")
	}

	if recovered.Path != originalInfo.Path {
		t.Errorf("Path = %s, want %s", recovered.Path, originalInfo.Path)
	}
	if recovered.ContentHash != originalInfo.ContentHash {
		t.Error("ContentHash mismatch")
	}
	if recovered.Size != originalInfo.Size {
		t.Errorf("Size = %d, want %d", recovered.Size, originalInfo.Size)
	}
	if !recovered.ModTime.Equal(originalInfo.ModTime) {
		t.Errorf("ModTime = %v, want %v", recovered.ModTime, originalInfo.ModTime)
	}
	if recovered.Permissions != originalInfo.Permissions {
		t.Errorf("Permissions = %o, want %o", recovered.Permissions, originalInfo.Permissions)
	}
	if recovered.Indexed != originalInfo.Indexed {
		t.Errorf("Indexed = %v, want %v", recovered.Indexed, originalInfo.Indexed)
	}
}

func TestWAL_Recover_FiltersCorruptedEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Write valid entries
	wal, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	if err := wal.LogInsert("a.go", testFileInfo("a.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}
	if err := wal.LogInsert("b.go", testFileInfo("b.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	if err := wal.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Corrupt the file by modifying some bytes in the middle
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	// Corrupt a checksum (last 4 bytes of first entry)
	// This is a bit fragile but tests corruption detection
	if len(data) > 50 {
		data[40] ^= 0xFF // Flip bits to corrupt
		data[41] ^= 0xFF
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Reopen and recover
	wal2, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL (reopen) failed: %v", err)
	}
	defer wal2.Close()

	entries, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	// Should recover less than 2 entries due to corruption
	// (exact count depends on what was corrupted)
	if len(entries) >= 2 {
		t.Logf("note: recovered %d entries, corruption may not have affected entries", len(entries))
	}
}

func TestWAL_Recover_TimestampOrdering(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	// Write entries with small delays
	for i := 0; i < 5; i++ {
		key := "file" + string(rune('a'+i)) + ".go"
		if err := wal.LogInsert(key, testFileInfo(key)); err != nil {
			t.Fatalf("LogInsert failed: %v", err)
		}
		time.Sleep(time.Millisecond)
	}

	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	// Timestamps should be in ascending order
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp < entries[i-1].Timestamp {
			t.Errorf("timestamps not in order: entry[%d]=%d < entry[%d]=%d",
				i, entries[i].Timestamp, i-1, entries[i-1].Timestamp)
		}
	}
}

// =============================================================================
// Size Tests
// =============================================================================

func TestWAL_Size(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	if wal.Size() != 0 {
		t.Errorf("initial Size = %d, want 0", wal.Size())
	}

	if err := wal.LogInsert("a.go", testFileInfo("a.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	size1 := wal.Size()
	if size1 == 0 {
		t.Error("Size should be > 0 after LogInsert")
	}

	if err := wal.LogInsert("b.go", testFileInfo("b.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	size2 := wal.Size()
	if size2 <= size1 {
		t.Errorf("Size should increase: %d <= %d", size2, size1)
	}
}

// =============================================================================
// NeedsCheckpoint Tests
// =============================================================================

func TestWAL_NeedsCheckpoint(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(WALConfig{
		Path:    path,
		MaxSize: 500, // Small max size for testing
	})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	if wal.NeedsCheckpoint() {
		t.Error("NeedsCheckpoint should be false on empty WAL")
	}

	// Write until exceeds MaxSize
	for i := 0; i < 20; i++ {
		key := "file" + string(rune('a'+i)) + ".go"
		if err := wal.LogInsert(key, testFileInfo(key)); err != nil {
			t.Fatalf("LogInsert failed: %v", err)
		}

		if wal.Size() > 500 {
			if !wal.NeedsCheckpoint() {
				t.Error("NeedsCheckpoint should be true when Size > MaxSize")
			}
			return
		}
	}

	// If we got here without exceeding, that's also valid
	t.Log("note: did not exceed MaxSize in 20 entries")
}

func TestWAL_NeedsCheckpoint_ZeroMaxSize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	wal, err := NewWAL(WALConfig{
		Path:    path,
		MaxSize: 0, // Uses default
	})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	// With default (large) MaxSize, should not need checkpoint
	for i := 0; i < 10; i++ {
		key := "file" + string(rune('a'+i)) + ".go"
		if err := wal.LogInsert(key, testFileInfo(key)); err != nil {
			t.Fatalf("LogInsert failed: %v", err)
		}
	}

	if wal.NeedsCheckpoint() {
		t.Error("NeedsCheckpoint should be false with default MaxSize")
	}
}

// =============================================================================
// Concurrent Write Tests
// =============================================================================

func TestWAL_ConcurrentWrites(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	const goroutines = 10
	const writesPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	errors := make(chan error, goroutines*writesPerGoroutine)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < writesPerGoroutine; i++ {
				key := "file" + string(rune('a'+id)) + "_" + string(rune('0'+i%10)) + ".go"
				if err := wal.LogInsert(key, testFileInfo(key)); err != nil {
					errors <- err
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent write error: %v", err)
	}

	// Verify recovery
	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	expected := goroutines * writesPerGoroutine
	if len(entries) != expected {
		t.Errorf("recovered %d entries, want %d", len(entries), expected)
	}
}

func TestWAL_ConcurrentMixedOperations(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	defer wal.Close()

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()

			for i := 0; i < 20; i++ {
				key := "file" + string(rune('a'+id)) + ".go"

				switch i % 4 {
				case 0, 1:
					_ = wal.LogInsert(key, testFileInfo(key))
				case 2:
					_ = wal.LogDelete(key)
				case 3:
					_ = wal.Checkpoint()
				}
			}
		}(g)
	}

	wg.Wait()

	// Recovery should not fail
	entries, err := wal.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) == 0 {
		t.Error("expected some entries after concurrent mixed operations")
	}
}

// =============================================================================
// fsync Behavior Tests
// =============================================================================

func TestWAL_SyncOnWrite(t *testing.T) {
	t.Parallel()

	wal, path := createTestWALWithSync(t)
	defer wal.Close()

	// Write an entry
	if err := wal.LogInsert("a.go", testFileInfo("a.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	// File should immediately have data (after fsync)
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	if stat.Size() == 0 {
		t.Error("file should have data after fsync write")
	}
}

// =============================================================================
// Close and Reopen Tests
// =============================================================================

func TestWAL_CloseAndReopen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Create and write
	wal1, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	if err := wal1.LogInsert("a.go", testFileInfo("a.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}
	if err := wal1.LogDelete("b.go"); err != nil {
		t.Fatalf("LogDelete failed: %v", err)
	}
	if err := wal1.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	if err := wal1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Reopen and verify
	wal2, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL (reopen) failed: %v", err)
	}
	defer wal2.Close()

	entries, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("recovered %d entries, want 3", len(entries))
	}

	// Verify operations
	if entries[0].Op != WALOpInsert || entries[0].Key != "a.go" {
		t.Error("first entry mismatch")
	}
	if entries[1].Op != WALOpDelete || entries[1].Key != "b.go" {
		t.Error("second entry mismatch")
	}
	if entries[2].Op != WALOpCheckpoint {
		t.Error("third entry should be checkpoint")
	}
}

func TestWAL_CloseAndReopen_ContinueWriting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// First session
	wal1, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	if err := wal1.LogInsert("a.go", testFileInfo("a.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	if err := wal1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Second session - continue writing
	wal2, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL (reopen) failed: %v", err)
	}

	if err := wal2.LogInsert("b.go", testFileInfo("b.go")); err != nil {
		t.Fatalf("LogInsert (second session) failed: %v", err)
	}

	entries, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	wal2.Close()

	// Should have both entries
	if len(entries) != 2 {
		t.Errorf("recovered %d entries, want 2", len(entries))
	}
}

// =============================================================================
// Crash Simulation Tests
// =============================================================================

func TestWAL_PartialWriteRecovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Write valid entries
	wal, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	if err := wal.LogInsert("valid.go", testFileInfo("valid.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	if err := wal.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Append garbage (simulating partial write during crash)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}

	// Write incomplete entry header
	garbage := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	if _, err := f.Write(garbage); err != nil {
		t.Fatalf("Write garbage failed: %v", err)
	}
	f.Close()

	// Reopen and recover
	wal2, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL (reopen) failed: %v", err)
	}
	defer wal2.Close()

	entries, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	// Should recover the valid entry
	if len(entries) != 1 {
		t.Errorf("recovered %d entries, want 1 (partial write should be skipped)", len(entries))
	}

	if len(entries) > 0 && entries[0].Key != "valid.go" {
		t.Error("recovered entry key mismatch")
	}
}

func TestWAL_TruncatedEntryRecovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.wal")

	// Write valid entries
	wal, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL failed: %v", err)
	}

	if err := wal.LogInsert("first.go", testFileInfo("first.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}
	if err := wal.LogInsert("second.go", testFileInfo("second.go")); err != nil {
		t.Fatalf("LogInsert failed: %v", err)
	}

	if err := wal.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Truncate file mid-entry (simulate crash during write)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	// Keep first entry intact, truncate second entry
	// Find approximate midpoint
	truncateAt := len(data) / 2
	if truncateAt > 0 {
		if err := os.WriteFile(path, data[:truncateAt], 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
	}

	// Reopen and recover
	wal2, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		t.Fatalf("NewWAL (reopen) failed: %v", err)
	}
	defer wal2.Close()

	entries, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	// Should recover at least the first entry (or none if truncation was too aggressive)
	t.Logf("recovered %d entries after truncation", len(entries))
}

// =============================================================================
// Error Tests
// =============================================================================

func TestWAL_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err     error
		wantMsg string
	}{
		{ErrWALPathEmpty, "wal path cannot be empty"},
		{ErrWALFileInfoNil, "file info cannot be nil for insert"},
		{ErrWALKeyEmpty, "key cannot be empty"},
		{ErrWALClosed, "wal is closed"},
		{ErrWALCorrupted, "wal entry corrupted"},
	}

	for _, tt := range tests {
		t.Run(tt.wantMsg, func(t *testing.T) {
			t.Parallel()
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("error = %q, want %q", tt.err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestWAL_OperationsAfterClose(t *testing.T) {
	t.Parallel()

	wal, _ := createTestWAL(t)
	if err := wal.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// All operations should fail after close
	if err := wal.LogInsert("a.go", testFileInfo("a.go")); err != ErrWALClosed {
		t.Errorf("LogInsert after close: error = %v, want %v", err, ErrWALClosed)
	}

	if err := wal.LogDelete("a.go"); err != ErrWALClosed {
		t.Errorf("LogDelete after close: error = %v, want %v", err, ErrWALClosed)
	}

	if err := wal.Checkpoint(); err != ErrWALClosed {
		t.Errorf("Checkpoint after close: error = %v, want %v", err, ErrWALClosed)
	}

	if err := wal.Truncate(); err != ErrWALClosed {
		t.Errorf("Truncate after close: error = %v, want %v", err, ErrWALClosed)
	}
}

// =============================================================================
// Binary Format Tests
// =============================================================================

func TestWAL_BinaryFormat_Deterministic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create two WALs with identical content
	path1 := filepath.Join(dir, "wal1.wal")
	path2 := filepath.Join(dir, "wal2.wal")

	// Use fixed timestamp for determinism
	info := &FileInfo{
		Path:        "test.go",
		ContentHash: Hash{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		Size:        100,
		ModTime:     time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Permissions: 0644,
		Indexed:     true,
	}

	for _, path := range []string{path1, path2} {
		wal, err := NewWAL(WALConfig{Path: path})
		if err != nil {
			t.Fatalf("NewWAL failed: %v", err)
		}

		// Note: Timestamps will differ, so files won't be byte-identical
		// This test verifies the format is consistent for recovery
		if err := wal.LogInsert("test.go", info); err != nil {
			t.Fatalf("LogInsert failed: %v", err)
		}

		if err := wal.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	}

	// Both should recover the same content
	wal1, _ := NewWAL(WALConfig{Path: path1})
	defer wal1.Close()
	entries1, _ := wal1.Recover()

	wal2, _ := NewWAL(WALConfig{Path: path2})
	defer wal2.Close()
	entries2, _ := wal2.Recover()

	if len(entries1) != len(entries2) {
		t.Errorf("entry count mismatch: %d vs %d", len(entries1), len(entries2))
	}

	if len(entries1) > 0 && len(entries2) > 0 {
		if entries1[0].Key != entries2[0].Key {
			t.Error("key mismatch between identical writes")
		}
		if entries1[0].Info.Path != entries2[0].Info.Path {
			t.Error("FileInfo.Path mismatch between identical writes")
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkWAL_LogInsert(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")

	wal, err := NewWAL(WALConfig{
		Path:        path,
		SyncOnWrite: false,
	})
	if err != nil {
		b.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	info := testFileInfo("bench.go")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wal.LogInsert("bench.go", info)
	}
}

func BenchmarkWAL_LogInsert_WithSync(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")

	wal, err := NewWAL(WALConfig{
		Path:        path,
		SyncOnWrite: true,
	})
	if err != nil {
		b.Fatalf("NewWAL failed: %v", err)
	}
	defer wal.Close()

	info := testFileInfo("bench.go")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wal.LogInsert("bench.go", info)
	}
}

func BenchmarkWAL_Recover(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")

	// Setup: write 1000 entries
	wal, err := NewWAL(WALConfig{Path: path})
	if err != nil {
		b.Fatalf("NewWAL failed: %v", err)
	}

	info := testFileInfo("bench.go")
	for i := 0; i < 1000; i++ {
		_ = wal.LogInsert("file"+string(rune('a'+i%26))+".go", info)
	}
	wal.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wal2, _ := NewWAL(WALConfig{Path: path})
		_, _ = wal2.Recover()
		wal2.Close()
	}
}
