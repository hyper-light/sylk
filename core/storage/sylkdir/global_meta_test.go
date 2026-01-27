package sylkdir

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
)

func TestGlobalMetaLoad(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if meta.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", meta.SchemaVersion)
	}
	if meta.NextNodeID != 1 {
		t.Errorf("NextNodeID = %d, want 1", meta.NextNodeID)
	}
	if meta.NextSessionID != 1 {
		t.Errorf("NextSessionID = %d, want 1", meta.NextSessionID)
	}
	if len(meta.CommittedSessions) != 0 {
		t.Errorf("CommittedSessions length = %d, want 0", len(meta.CommittedSessions))
	}
}

func TestGlobalMetaLoadNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	meta := NewGlobalMeta(tmpDir + "/nonexistent/meta.json")

	err := meta.Load()
	if err == nil {
		t.Fatal("Expected error loading nonexistent file")
	}
}

func TestGlobalMetaSave(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Modify and save
	meta.NextNodeID = 100
	meta.NextSessionID = 50
	if err := meta.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Reload and verify
	meta2 := NewGlobalMetaFromSylkDir(sd)
	if err := meta2.Load(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if meta2.NextNodeID != 100 {
		t.Errorf("NextNodeID = %d, want 100", meta2.NextNodeID)
	}
	if meta2.NextSessionID != 50 {
		t.Errorf("NextSessionID = %d, want 50", meta2.NextSessionID)
	}
}

func TestGlobalMetaAllocateNodeID(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Allocate first ID
	id1, err := meta.AllocateNodeID()
	if err != nil {
		t.Fatalf("AllocateNodeID failed: %v", err)
	}
	if id1 != 1 {
		t.Errorf("First ID = %d, want 1", id1)
	}

	// Allocate second ID
	id2, err := meta.AllocateNodeID()
	if err != nil {
		t.Fatalf("AllocateNodeID failed: %v", err)
	}
	if id2 != 2 {
		t.Errorf("Second ID = %d, want 2", id2)
	}

	// Verify persisted
	meta2 := NewGlobalMetaFromSylkDir(sd)
	if err := meta2.Load(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	if meta2.NextNodeID != 3 {
		t.Errorf("Persisted NextNodeID = %d, want 3", meta2.NextNodeID)
	}
}

func TestGlobalMetaAllocateNodeIDs(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Allocate batch of 10 IDs
	firstID, err := meta.AllocateNodeIDs(10)
	if err != nil {
		t.Fatalf("AllocateNodeIDs failed: %v", err)
	}
	if firstID != 1 {
		t.Errorf("First ID = %d, want 1", firstID)
	}

	// Next allocation should start at 11
	nextID, err := meta.AllocateNodeID()
	if err != nil {
		t.Fatalf("AllocateNodeID failed: %v", err)
	}
	if nextID != 11 {
		t.Errorf("Next ID = %d, want 11", nextID)
	}
}

func TestGlobalMetaAllocateNodeIDsZero(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	_, err := meta.AllocateNodeIDs(0)
	if err == nil {
		t.Fatal("Expected error allocating 0 IDs")
	}
}

func TestGlobalMetaAllocateSessionID(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Allocate first ID
	id1, err := meta.AllocateSessionID()
	if err != nil {
		t.Fatalf("AllocateSessionID failed: %v", err)
	}
	if id1 != 1 {
		t.Errorf("First ID = %d, want 1", id1)
	}

	// Allocate second ID
	id2, err := meta.AllocateSessionID()
	if err != nil {
		t.Fatalf("AllocateSessionID failed: %v", err)
	}
	if id2 != 2 {
		t.Errorf("Second ID = %d, want 2", id2)
	}
}

func TestGlobalMetaRegisterCommit(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Register a commit
	if err := meta.RegisterCommit(1, 5); err != nil {
		t.Fatalf("RegisterCommit failed: %v", err)
	}

	// Verify in memory
	if !meta.IsSessionCommitted(1) {
		t.Error("Session 1 should be committed")
	}
	if meta.IsSessionCommitted(2) {
		t.Error("Session 2 should not be committed")
	}

	// Verify persisted
	meta2 := NewGlobalMetaFromSylkDir(sd)
	if err := meta2.Load(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	sessions := meta2.GetCommittedSessions()
	if len(sessions) != 1 {
		t.Fatalf("CommittedSessions length = %d, want 1", len(sessions))
	}
	if sessions[0].SessionID != 1 {
		t.Errorf("SessionID = %d, want 1", sessions[0].SessionID)
	}
	if sessions[0].FinalVersion != 5 {
		t.Errorf("FinalVersion = %d, want 5", sessions[0].FinalVersion)
	}
}

func TestGlobalMetaRegisterCommitDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// First commit succeeds
	if err := meta.RegisterCommit(1, 5); err != nil {
		t.Fatalf("First RegisterCommit failed: %v", err)
	}

	// Duplicate commit fails
	if err := meta.RegisterCommit(1, 6); err == nil {
		t.Fatal("Expected error for duplicate commit")
	}
}

func TestGlobalMetaNotLoaded(t *testing.T) {
	meta := NewGlobalMeta("/tmp/nonexistent.json")

	// Operations should fail without Load()
	_, err := meta.AllocateNodeID()
	if err != ErrMetaNotLoaded {
		t.Errorf("AllocateNodeID error = %v, want ErrMetaNotLoaded", err)
	}

	_, err = meta.AllocateSessionID()
	if err != ErrMetaNotLoaded {
		t.Errorf("AllocateSessionID error = %v, want ErrMetaNotLoaded", err)
	}

	err = meta.RegisterCommit(1, 1)
	if err != ErrMetaNotLoaded {
		t.Errorf("RegisterCommit error = %v, want ErrMetaNotLoaded", err)
	}
}

func TestGlobalMetaConcurrentAllocation(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	const numGoroutines = 10
	const allocsPerGoroutine = 10

	var wg sync.WaitGroup
	ids := make(chan uint32, numGoroutines*allocsPerGoroutine)
	errors := make(chan error, numGoroutines*allocsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < allocsPerGoroutine; j++ {
				id, err := meta.AllocateNodeID()
				if err != nil {
					errors <- err
					return
				}
				ids <- id
			}
		}()
	}

	wg.Wait()
	close(ids)
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent allocation error: %v", err)
	}

	// Verify all IDs are unique
	seen := make(map[uint32]bool)
	for id := range ids {
		if seen[id] {
			t.Errorf("Duplicate ID allocated: %d", id)
		}
		seen[id] = true
	}

	// Verify count
	expectedCount := numGoroutines * allocsPerGoroutine
	if len(seen) != expectedCount {
		t.Errorf("Got %d unique IDs, want %d", len(seen), expectedCount)
	}
}

func TestGlobalMetaAtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Allocate to trigger save
	if _, err := meta.AllocateNodeID(); err != nil {
		t.Fatalf("AllocateNodeID failed: %v", err)
	}

	// Read the file and verify it's valid JSON
	data, err := os.ReadFile(meta.Path())
	if err != nil {
		t.Fatalf("Failed to read meta file: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Errorf("Meta file is not valid JSON: %v", err)
	}
}

func TestGlobalMetaWithFileLock(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Acquire file lock
	locked, err := meta.WithFileLock()
	if err != nil {
		t.Fatalf("WithFileLock failed: %v", err)
	}

	// Operations work under lock
	id, err := locked.AllocateNodeID()
	if err != nil {
		t.Errorf("AllocateNodeID under lock failed: %v", err)
	}
	if id != 1 {
		t.Errorf("ID = %d, want 1", id)
	}

	// Release lock
	if err := locked.Release(); err != nil {
		t.Errorf("Release failed: %v", err)
	}

	// Can acquire lock again
	locked2, err := meta.WithFileLock()
	if err != nil {
		t.Fatalf("Second WithFileLock failed: %v", err)
	}
	defer locked2.Release()
}

func TestGlobalMetaGetters(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Test GetCurrentNodeID
	if got := meta.GetCurrentNodeID(); got != 1 {
		t.Errorf("GetCurrentNodeID = %d, want 1", got)
	}

	// Allocate and check again
	meta.AllocateNodeID()
	if got := meta.GetCurrentNodeID(); got != 2 {
		t.Errorf("GetCurrentNodeID after alloc = %d, want 2", got)
	}

	// Test GetCurrentSessionID
	if got := meta.GetCurrentSessionID(); got != 1 {
		t.Errorf("GetCurrentSessionID = %d, want 1", got)
	}

	// Test Path
	if meta.Path() != sd.MetaPath() {
		t.Errorf("Path = %s, want %s", meta.Path(), sd.MetaPath())
	}

	// Test IsLoaded
	if !meta.IsLoaded() {
		t.Error("IsLoaded should return true after Load()")
	}
}

func TestGlobalMetaGetCommittedSessionsCopy(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	meta.RegisterCommit(1, 5)

	// Get copy and modify it
	sessions := meta.GetCommittedSessions()
	sessions[0].SessionID = 999

	// Original should be unchanged
	sessions2 := meta.GetCommittedSessions()
	if sessions2[0].SessionID != 1 {
		t.Error("GetCommittedSessions should return a copy")
	}
}
