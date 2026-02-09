package sylkdir

import (
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
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

	if meta.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", meta.SchemaVersion)
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
	// Verify initial version is v1.0.0 (init creates v1.0.0 with manifest)
	expectedInitial := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	if !meta.GetHead().Equal(expectedInitial) {
		t.Errorf("Initial Version = %s, want v1.0.0", meta.GetHead().String())
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

	// Verify initial global version is v1.0.0 (init creates v1.0.0 with manifest)
	expectedInitial := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	if !meta.GetHead().Equal(expectedInitial) {
		t.Errorf("Initial version = %s, want v1.0.0", meta.GetHead().String())
	}

	// Register a commit with session final version v1.3.0
	sessionFinalVersion := SemanticVersion{Major: 1, Minor: 3, Patch: 0}
	if err := meta.RegisterCommit(1, sessionFinalVersion); err != nil {
		t.Fatalf("RegisterCommit failed: %v", err)
	}

	// Verify global version bumped to v2.0.0 (MAJOR bump from v1.0.0)
	expectedGlobalVersion := SemanticVersion{Major: 2, Minor: 0, Patch: 0}
	if !meta.GetVersion().Equal(expectedGlobalVersion) {
		t.Errorf("Global version = %s, want %s", meta.GetVersion().String(), expectedGlobalVersion.String())
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
	if !sessions[0].FinalVersion.Equal(sessionFinalVersion) {
		t.Errorf("FinalVersion = %s, want %s", sessions[0].FinalVersion.String(), sessionFinalVersion.String())
	}
	if !sessions[0].GlobalVersion.Equal(expectedGlobalVersion) {
		t.Errorf("GlobalVersion = %s, want %s", sessions[0].GlobalVersion.String(), expectedGlobalVersion.String())
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
	v1 := SemanticVersion{Major: 1, Minor: 1, Patch: 0}
	if err := meta.RegisterCommit(1, v1); err != nil {
		t.Fatalf("First RegisterCommit failed: %v", err)
	}

	// Duplicate commit fails
	v2 := SemanticVersion{Major: 1, Minor: 2, Patch: 0}
	if err := meta.RegisterCommit(1, v2); err == nil {
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

	v := SemanticVersion{Major: 1, Minor: 0, Patch: 0}
	err = meta.RegisterCommit(1, v)
	if err != ErrMetaNotLoaded {
		t.Errorf("RegisterCommit error = %v, want ErrMetaNotLoaded", err)
	}

	_, err = meta.BumpMinorVersion()
	if err != ErrMetaNotLoaded {
		t.Errorf("BumpMinorVersion error = %v, want ErrMetaNotLoaded", err)
	}

	_, err = meta.BumpPatchVersion()
	if err != ErrMetaNotLoaded {
		t.Errorf("BumpPatchVersion error = %v, want ErrMetaNotLoaded", err)
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

	v := SemanticVersion{Major: 1, Minor: 1, Patch: 0}
	meta.RegisterCommit(1, v)

	// Get copy and modify it
	sessions := meta.GetCommittedSessions()
	sessions[0].SessionID = 999

	// Original should be unchanged
	sessions2 := meta.GetCommittedSessions()
	if sessions2[0].SessionID != 1 {
		t.Error("GetCommittedSessions should return a copy")
	}
}

func TestGlobalMetaVersioning(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Initial version should be v1.0.0 (init creates v1.0.0 with manifest)
	v := meta.GetVersion()
	if v.Major != 1 || v.Minor != 0 || v.Patch != 0 {
		t.Errorf("Initial version = %s, want v1.0.0", v.String())
	}

	// Test minor bump
	newV, err := meta.BumpMinorVersion()
	if err != nil {
		t.Fatalf("BumpMinorVersion failed: %v", err)
	}
	if newV.Major != 1 || newV.Minor != 1 || newV.Patch != 0 {
		t.Errorf("After minor bump = %s, want v1.1.0", newV.String())
	}

	// Test patch bump
	newV, err = meta.BumpPatchVersion()
	if err != nil {
		t.Fatalf("BumpPatchVersion failed: %v", err)
	}
	if newV.Major != 1 || newV.Minor != 1 || newV.Patch != 1 {
		t.Errorf("After patch bump = %s, want v1.1.1", newV.String())
	}

	// Verify persisted
	meta2 := NewGlobalMetaFromSylkDir(sd)
	if err := meta2.Load(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	v2 := meta2.GetVersion()
	if !v2.Equal(newV) {
		t.Errorf("Persisted version = %s, want %s", v2.String(), newV.String())
	}
}

func TestGlobalMetaMultipleCommits(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Commit session 1 (global: v1.0.0 -> v2.0.0)
	sess1Final := SemanticVersion{Major: 1, Minor: 3, Patch: 0}
	if err := meta.RegisterCommit(1, sess1Final); err != nil {
		t.Fatalf("RegisterCommit session 1 failed: %v", err)
	}
	expectedV1 := SemanticVersion{Major: 2, Minor: 0, Patch: 0}
	if !meta.GetVersion().Equal(expectedV1) {
		t.Errorf("After commit 1, global = %s, want %s", meta.GetVersion().String(), expectedV1.String())
	}

	// Commit session 2 (global: v2.0.0 -> v3.0.0)
	sess2Final := SemanticVersion{Major: 1, Minor: 1, Patch: 5}
	if err := meta.RegisterCommit(2, sess2Final); err != nil {
		t.Fatalf("RegisterCommit session 2 failed: %v", err)
	}
	expectedV2 := SemanticVersion{Major: 3, Minor: 0, Patch: 0}
	if !meta.GetVersion().Equal(expectedV2) {
		t.Errorf("After commit 2, global = %s, want %s", meta.GetVersion().String(), expectedV2.String())
	}

	// Commit session 3 (global: v3.0.0 -> v4.0.0)
	sess3Final := SemanticVersion{Major: 1, Minor: 2, Patch: 0}
	if err := meta.RegisterCommit(3, sess3Final); err != nil {
		t.Fatalf("RegisterCommit session 3 failed: %v", err)
	}
	expectedV3 := SemanticVersion{Major: 4, Minor: 0, Patch: 0}
	if !meta.GetVersion().Equal(expectedV3) {
		t.Errorf("After commit 3, global = %s, want %s", meta.GetVersion().String(), expectedV3.String())
	}

	// Verify all committed sessions
	sessions := meta.GetCommittedSessions()
	if len(sessions) != 3 {
		t.Fatalf("CommittedSessions length = %d, want 3", len(sessions))
	}

	// Session 1
	if sessions[0].SessionID != 1 {
		t.Errorf("Session 0 ID = %d, want 1", sessions[0].SessionID)
	}
	if !sessions[0].FinalVersion.Equal(sess1Final) {
		t.Errorf("Session 0 FinalVersion = %s, want %s", sessions[0].FinalVersion.String(), sess1Final.String())
	}
	if !sessions[0].GlobalVersion.Equal(expectedV1) {
		t.Errorf("Session 0 GlobalVersion = %s, want %s", sessions[0].GlobalVersion.String(), expectedV1.String())
	}

	// Session 2
	if !sessions[1].GlobalVersion.Equal(expectedV2) {
		t.Errorf("Session 1 GlobalVersion = %s, want %s", sessions[1].GlobalVersion.String(), expectedV2.String())
	}

	// Session 3
	if !sessions[2].GlobalVersion.Equal(expectedV3) {
		t.Errorf("Session 2 GlobalVersion = %s, want %s", sessions[2].GlobalVersion.String(), expectedV3.String())
	}

	// Verify persistence
	meta2 := NewGlobalMetaFromSylkDir(sd)
	if err := meta2.Load(); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	if !meta2.GetVersion().Equal(expectedV3) {
		t.Errorf("Persisted global version = %s, want %s", meta2.GetVersion().String(), expectedV3.String())
	}
}

func TestGlobalMetaVersionBumpTypes(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Start at v1.0.0 (init creates v1.0.0 with manifest)
	// Commit bumps MAJOR: v1.0.0 -> v2.0.0
	if err := meta.RegisterCommit(1, SemanticVersion{Major: 1, Minor: 0, Patch: 0}); err != nil {
		t.Fatalf("RegisterCommit failed: %v", err)
	}
	expected := SemanticVersion{Major: 2, Minor: 0, Patch: 0}
	if !meta.GetVersion().Equal(expected) {
		t.Errorf("After commit, version = %s, want %s", meta.GetVersion().String(), expected.String())
	}

	// Minor bump: v2.0.0 -> v2.1.0
	newV, err := meta.BumpMinorVersion()
	if err != nil {
		t.Fatalf("BumpMinorVersion failed: %v", err)
	}
	expected = SemanticVersion{Major: 2, Minor: 1, Patch: 0}
	if !newV.Equal(expected) {
		t.Errorf("After minor, version = %s, want %s", newV.String(), expected.String())
	}

	// Patch bump: v2.1.0 -> v2.1.1
	newV, err = meta.BumpPatchVersion()
	if err != nil {
		t.Fatalf("BumpPatchVersion failed: %v", err)
	}
	expected = SemanticVersion{Major: 2, Minor: 1, Patch: 1}
	if !newV.Equal(expected) {
		t.Errorf("After patch, version = %s, want %s", newV.String(), expected.String())
	}

	// Another commit bumps MAJOR: v2.1.1 -> v3.0.0
	if err := meta.RegisterCommit(2, SemanticVersion{Major: 1, Minor: 5, Patch: 0}); err != nil {
		t.Fatalf("RegisterCommit failed: %v", err)
	}
	expected = SemanticVersion{Major: 3, Minor: 0, Patch: 0}
	if !meta.GetVersion().Equal(expected) {
		t.Errorf("After second commit, version = %s, want %s", meta.GetVersion().String(), expected.String())
	}
}

// TestGlobalMetaCommitFileRoundTrip reads the raw meta.json off disk after
// commits and verifies that every SemanticVersion field serialises correctly.
func TestGlobalMetaCommitFileRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Register two commits
	if err := meta.RegisterCommit(1, SemanticVersion{Major: 1, Minor: 3, Patch: 0}); err != nil {
		t.Fatalf("commit 1: %v", err)
	}
	if err := meta.RegisterCommit(2, SemanticVersion{Major: 1, Minor: 1, Patch: 5}); err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	// Read raw JSON from disk
	raw, err := os.ReadFile(sd.MetaPath())
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}

	// Parse into untyped map so we validate the on-disk schema, not just
	// the Go struct round-trip.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal meta.json: %v", err)
	}

	// schema_version must be 2
	var schemaV int
	if err := json.Unmarshal(doc["schema_version"], &schemaV); err != nil {
		t.Fatalf("unmarshal schema_version: %v", err)
	}
	if schemaV != 2 {
		t.Errorf("on-disk schema_version = %d, want 2", schemaV)
	}

	// version must be {major:3, minor:0, patch:0} after two major bumps from v1.0.0
	var ver struct {
		Major uint16 `json:"major"`
		Minor uint16 `json:"minor"`
		Patch uint16 `json:"patch"`
	}
	if err := json.Unmarshal(doc["version"], &ver); err != nil {
		t.Fatalf("unmarshal version: %v", err)
	}
	if ver.Major != 3 || ver.Minor != 0 || ver.Patch != 0 {
		t.Errorf("on-disk version = v%d.%d.%d, want v3.0.0", ver.Major, ver.Minor, ver.Patch)
	}

	// committed_sessions must contain two entries with correct shapes
	var sessions []struct {
		SessionID     uint32 `json:"session_id"`
		FinalVersion  struct {
			Major uint16 `json:"major"`
			Minor uint16 `json:"minor"`
			Patch uint16 `json:"patch"`
		} `json:"final_version"`
		GlobalVersion struct {
			Major uint16 `json:"major"`
			Minor uint16 `json:"minor"`
			Patch uint16 `json:"patch"`
		} `json:"global_version"`
		CommittedAt string `json:"committed_at"`
	}
	if err := json.Unmarshal(doc["committed_sessions"], &sessions); err != nil {
		t.Fatalf("unmarshal committed_sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("on-disk committed_sessions len = %d, want 2", len(sessions))
	}

	// Session 1: final v1.3.0, global v2.0.0 (commit 1: v1.0.0 -> v2.0.0)
	s := sessions[0]
	if s.SessionID != 1 {
		t.Errorf("session[0].session_id = %d, want 1", s.SessionID)
	}
	if s.FinalVersion.Major != 1 || s.FinalVersion.Minor != 3 || s.FinalVersion.Patch != 0 {
		t.Errorf("session[0].final_version = v%d.%d.%d, want v1.3.0",
			s.FinalVersion.Major, s.FinalVersion.Minor, s.FinalVersion.Patch)
	}
	if s.GlobalVersion.Major != 2 || s.GlobalVersion.Minor != 0 || s.GlobalVersion.Patch != 0 {
		t.Errorf("session[0].global_version = v%d.%d.%d, want v2.0.0",
			s.GlobalVersion.Major, s.GlobalVersion.Minor, s.GlobalVersion.Patch)
	}
	if s.CommittedAt == "" {
		t.Error("session[0].committed_at is empty")
	}

	// Session 2: final v1.1.5, global v3.0.0 (commit 2: v2.0.0 -> v3.0.0)
	s = sessions[1]
	if s.SessionID != 2 {
		t.Errorf("session[1].session_id = %d, want 2", s.SessionID)
	}
	if s.FinalVersion.Major != 1 || s.FinalVersion.Minor != 1 || s.FinalVersion.Patch != 5 {
		t.Errorf("session[1].final_version = v%d.%d.%d, want v1.1.5",
			s.FinalVersion.Major, s.FinalVersion.Minor, s.FinalVersion.Patch)
	}
	if s.GlobalVersion.Major != 3 || s.GlobalVersion.Minor != 0 || s.GlobalVersion.Patch != 0 {
		t.Errorf("session[1].global_version = v%d.%d.%d, want v3.0.0",
			s.GlobalVersion.Major, s.GlobalVersion.Minor, s.GlobalVersion.Patch)
	}

	// Now reload through the typed path and verify agreement
	meta2 := NewGlobalMetaFromSylkDir(sd)
	if err := meta2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !meta2.GetVersion().Equal(SemanticVersion{Major: 3, Minor: 0, Patch: 0}) {
		t.Errorf("reloaded version = %s, want v3.0.0", meta2.GetVersion().String())
	}
	cs := meta2.GetCommittedSessions()
	if !cs[0].FinalVersion.Equal(SemanticVersion{Major: 1, Minor: 3, Patch: 0}) {
		t.Errorf("reloaded cs[0].FinalVersion = %s, want v1.3.0", cs[0].FinalVersion.String())
	}
	if !cs[1].GlobalVersion.Equal(SemanticVersion{Major: 3, Minor: 0, Patch: 0}) {
		t.Errorf("reloaded cs[1].GlobalVersion = %s, want v3.0.0", cs[1].GlobalVersion.String())
	}
}

// TestGlobalMetaCommitWorkflow exercises the full session-to-global commit
// workflow at the filesystem level:
//
//  1. Init SylkDir + GlobalMeta
//  2. Create session with BaseSnapshot capturing global version
//  3. Ingest data into session
//  4. Pre-commit minor checkpoint in session
//  5. RegisterCommit (major bump) on global
//  6. Create a NEW session whose BaseSnapshot reflects the new global state
//  7. Verify every file on disk
func TestGlobalMetaCommitWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Step 1: Load global meta (v1.0.0 from init)
	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load global meta: %v", err)
	}

	// Step 2: Create session 1 with snapshot of current global state
	store := NewSessionStore(sd)
	snap1 := &BaseSnapshot{
		GlobalVersion:     gm.GetHead(), // v1.0.0
		CommittedSessions: []uint32{},
		SnapshotAt:        time.Now(),
		NextNodeID:        gm.GetCurrentNodeID(),
	}

	sess1, err := store.Create(1, snap1)
	if err != nil {
		t.Fatalf("Create session 1: %v", err)
	}
	defer sess1.CloseBleve()

	// Verify BaseSnapshot file on disk captures v1.0.0
	snap1Disk, err := readBaseSnapshotFile(sess1.Path())
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snap1Disk.GlobalVersion.Major != 1 || snap1Disk.GlobalVersion.Minor != 0 || snap1Disk.GlobalVersion.Patch != 0 {
		t.Errorf("snapshot global_version = v%d.%d.%d, want v1.0.0",
			snap1Disk.GlobalVersion.Major, snap1Disk.GlobalVersion.Minor, snap1Disk.GlobalVersion.Patch)
	}

	// Step 3: Do some work in session (patch checkpoints)
	_, err = sess1.Checkpoint("work-1", CheckpointPatch)
	if err != nil {
		t.Fatalf("checkpoint 1: %v", err)
	}
	_, err = sess1.Checkpoint("work-2", CheckpointPatch)
	if err != nil {
		t.Fatalf("checkpoint 2: %v", err)
	}
	// HEAD is now v1.0.2

	// Step 4: Pre-commit minor checkpoint
	preCommitVer, err := sess1.Checkpoint("pre-commit", CheckpointMinor)
	if err != nil {
		t.Fatalf("pre-commit checkpoint: %v", err)
	}
	// HEAD is now v1.1.0
	expectedPreCommit := SemanticVersion{Major: 1, Minor: 1, Patch: 0}
	if !preCommitVer.Equal(expectedPreCommit) {
		t.Errorf("pre-commit version = %s, want %s", preCommitVer.String(), expectedPreCommit.String())
	}

	// Step 5: Register commit (global major bump v1.0.0 -> v2.0.0)
	if err := gm.RegisterCommit(1, sess1.Manifest.Head); err != nil {
		t.Fatalf("RegisterCommit: %v", err)
	}

	// Verify global meta file on disk
	gmDisk := NewGlobalMetaFromSylkDir(sd)
	if err := gmDisk.Load(); err != nil {
		t.Fatalf("reload global meta: %v", err)
	}
	expectedGlobal := SemanticVersion{Major: 2, Minor: 0, Patch: 0}
	if !gmDisk.GetVersion().Equal(expectedGlobal) {
		t.Errorf("global meta on disk = %s, want %s", gmDisk.GetVersion().String(), expectedGlobal.String())
	}
	if len(gmDisk.GetCommittedSessions()) != 1 {
		t.Fatalf("committed sessions = %d, want 1", len(gmDisk.GetCommittedSessions()))
	}
	cs := gmDisk.GetCommittedSessions()[0]
	if cs.SessionID != 1 {
		t.Errorf("committed session id = %d, want 1", cs.SessionID)
	}
	if !cs.FinalVersion.Equal(expectedPreCommit) {
		t.Errorf("committed final_version = %s, want %s", cs.FinalVersion.String(), expectedPreCommit.String())
	}
	if !cs.GlobalVersion.Equal(expectedGlobal) {
		t.Errorf("committed global_version = %s, want %s", cs.GlobalVersion.String(), expectedGlobal.String())
	}

	// Step 6: Create session 2 whose snapshot captures global v2.0.0
	committedIDs := make([]uint32, len(gmDisk.GetCommittedSessions()))
	for i, c := range gmDisk.GetCommittedSessions() {
		committedIDs[i] = c.SessionID
	}
	snap2 := &BaseSnapshot{
		GlobalVersion:     gmDisk.GetVersion(), // v2.0.0
		CommittedSessions: committedIDs,
		SnapshotAt:        mustParseTime(t),
		NextNodeID:        gmDisk.GetCurrentNodeID(),
	}

	sess2, err := store.Create(2, snap2)
	if err != nil {
		t.Fatalf("Create session 2: %v", err)
	}
	defer sess2.CloseBleve()

	// Step 7: Verify session 2's snapshot file on disk
	snap2Disk, err := readBaseSnapshotFile(sess2.Path())
	if err != nil {
		t.Fatalf("read snapshot 2: %v", err)
	}
	if !snap2Disk.GlobalVersion.Equal(expectedGlobal) {
		t.Errorf("session 2 snapshot global_version = %s, want %s",
			snap2Disk.GlobalVersion.String(), expectedGlobal.String())
	}
	if len(snap2Disk.CommittedSessions) != 1 || snap2Disk.CommittedSessions[0] != 1 {
		t.Errorf("session 2 snapshot committed_sessions = %v, want [1]", snap2Disk.CommittedSessions)
	}

	// Verify session 1's manifest on disk still has the pre-commit version
	sess1Reloaded, err := store.Load("ses_001")
	if err != nil {
		t.Fatalf("reload session 1: %v", err)
	}
	if !sess1Reloaded.Manifest.Head.Equal(expectedPreCommit) {
		t.Errorf("session 1 HEAD on disk = %s, want %s",
			sess1Reloaded.Manifest.Head.String(), expectedPreCommit.String())
	}

	// Verify ancestor chain: v1.1.0 -> v1.0.2 -> v1.0.1 -> v1.0.0
	chain := sess1Reloaded.GetAncestorChain()
	if len(chain) != 4 {
		t.Fatalf("ancestor chain len = %d, want 4", len(chain))
	}
	expectedChain := []SemanticVersion{
		{Major: 1, Minor: 1, Patch: 0},
		{Major: 1, Minor: 0, Patch: 2},
		{Major: 1, Minor: 0, Patch: 1},
		{Major: 1, Minor: 0, Patch: 0},
	}
	for i, v := range chain {
		if !v.Equal(expectedChain[i]) {
			t.Errorf("chain[%d] = %s, want %s", i, v.String(), expectedChain[i].String())
		}
	}

	// Verify version directories exist on disk
	for _, v := range expectedChain {
		vPath := sess1Reloaded.VersionPath(v)
		if _, err := os.Stat(vPath); os.IsNotExist(err) {
			t.Errorf("version dir %s does not exist", vPath)
		}
	}
}

// TestGlobalMetaVersionBumpFileRoundTrip verifies that minor and patch bumps
// persist correctly to meta.json on disk and survive reload.
func TestGlobalMetaVersionBumpFileRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	meta := NewGlobalMetaFromSylkDir(sd)
	if err := meta.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Commit: v1.0.0 -> v2.0.0
	meta.RegisterCommit(1, SemanticVersion{Major: 1, Minor: 0, Patch: 0})
	// Patch: v2.0.0 -> v2.0.1
	meta.BumpPatchVersion()
	// Minor: v2.0.1 -> v2.1.0
	meta.BumpMinorVersion()
	// Patch: v2.1.0 -> v2.1.1
	meta.BumpPatchVersion()

	expectedFinal := SemanticVersion{Major: 2, Minor: 1, Patch: 1}

	// Read raw JSON off disk
	raw, err := os.ReadFile(sd.MetaPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var onDisk struct {
		Version struct {
			Major uint16 `json:"major"`
			Minor uint16 `json:"minor"`
			Patch uint16 `json:"patch"`
		} `json:"version"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if onDisk.Version.Major != expectedFinal.Major ||
		onDisk.Version.Minor != expectedFinal.Minor ||
		onDisk.Version.Patch != expectedFinal.Patch {
		t.Errorf("on-disk version = v%d.%d.%d, want %s",
			onDisk.Version.Major, onDisk.Version.Minor, onDisk.Version.Patch, expectedFinal.String())
	}

	// Reload through typed path
	meta2 := NewGlobalMetaFromSylkDir(sd)
	if err := meta2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !meta2.GetVersion().Equal(expectedFinal) {
		t.Errorf("reloaded version = %s, want %s", meta2.GetVersion().String(), expectedFinal.String())
	}
}

// helpers

func mustParseTime(t *testing.T) time.Time {
	t.Helper()
	return time.Now()
}

func readBaseSnapshotFile(sessionPath string) (*BaseSnapshot, error) {
	data, err := os.ReadFile(sessionPath + "/base/snapshot.json")
	if err != nil {
		return nil, err
	}
	var snap BaseSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
