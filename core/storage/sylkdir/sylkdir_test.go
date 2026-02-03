package sylkdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSylkDirInit(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Load GlobalMeta to get HEAD version
	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load GlobalMeta failed: %v", err)
	}
	head := gm.GetHead()
	versionPath := sd.GlobalVersionPath(head)

	// Verify all directories were created (versioned structure only)
	expectedDirs := []string{
		sd.RootPath(),
		sd.SessionsPath(),
		sd.GlobalVersionsPath(),
		versionPath,
		filepath.Join(versionPath, "nodes"),
		filepath.Join(versionPath, "vectors"),
		filepath.Join(versionPath, "docs"),
		filepath.Join(versionPath, "bleve"),
		sd.GlobalEdgeDataPath(),
	}

	for _, dir := range expectedDirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("Expected directory %s to exist: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Expected %s to be a directory", dir)
		}
	}

	// Verify config.yaml was created
	configPath := sd.ConfigPath()
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("Expected config.yaml at %s: %v", configPath, err)
	}

	// Verify meta.json was created at root level
	metaPath := sd.MetaPath()
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("Expected meta.json at %s: %v", metaPath, err)
	}

	// Verify meta.json contains embedded manifest
	gm = NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load meta: %v", err)
	}
	if !gm.HasManifest() {
		t.Error("Expected meta.json to contain embedded manifest")
	}
}

func TestSylkDirInitIdempotent(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)

	// Init twice should not error
	if err := sd.Init(); err != nil {
		t.Fatalf("First Init failed: %v", err)
	}
	if err := sd.Init(); err != nil {
		t.Fatalf("Second Init failed: %v", err)
	}

	// Structure should still be valid
	if err := sd.Validate(); err != nil {
		t.Errorf("Validation failed after double init: %v", err)
	}
}

func TestSylkDirValidateEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)

	// Validate should fail on empty directory
	err := sd.Validate()
	if err == nil {
		t.Fatal("Expected validation to fail on empty directory")
	}

	// Should return ValidationErrors
	validationErrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("Expected ValidationErrors, got %T", err)
	}

	// Should have multiple errors for missing directories
	if len(validationErrs) == 0 {
		t.Error("Expected validation errors to be non-empty")
	}
}

func TestSylkDirValidateSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Validation should pass after init
	if err := sd.Validate(); err != nil {
		t.Errorf("Validation failed: %v", err)
	}
}

func TestSylkDirValidateMissingDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Remove the versions directory (required in new structure)
	if err := os.RemoveAll(sd.GlobalVersionsPath()); err != nil {
		t.Fatalf("Failed to remove versions directory: %v", err)
	}

	// Validation should fail
	err := sd.Validate()
	if err == nil {
		t.Fatal("Expected validation to fail with missing versions directory")
	}

	validationErrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("Expected ValidationErrors, got %T", err)
	}

	// Should report missing versions directory
	foundVersionsError := false
	for _, e := range validationErrs {
		if filepath.Base(e.Path) == "versions" {
			foundVersionsError = true
			break
		}
	}
	if !foundVersionsError {
		t.Error("Expected validation error for missing versions directory")
	}
}

func TestSylkDirValidateMissingConfig(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Remove config file
	if err := os.Remove(sd.ConfigPath()); err != nil {
		t.Fatalf("Failed to remove config: %v", err)
	}

	// Validation should fail
	err := sd.Validate()
	if err == nil {
		t.Fatal("Expected validation to fail with missing config")
	}
}

func TestSylkDirLock(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Acquire lock
	if err := sd.Lock(); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	defer sd.Unlock()

	// Should be locked
	if !sd.IsLocked() {
		t.Error("Expected IsLocked to return true")
	}

	// Lock file should exist
	lockPath := sd.LockPath()
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("Lock file should exist: %v", err)
	}
}

func TestSylkDirLockConcurrent(t *testing.T) {
	tmpDir := t.TempDir()

	sd1 := New(tmpDir)
	if err := sd1.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// First process acquires lock
	if err := sd1.Lock(); err != nil {
		t.Fatalf("First lock failed: %v", err)
	}
	defer sd1.Unlock()

	// Second process should fail to acquire lock
	sd2 := New(tmpDir)
	err := sd2.Lock()
	if err != ErrLocked {
		t.Errorf("Expected ErrLocked, got: %v", err)
	}
}

func TestSylkDirUnlock(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Acquire and release lock
	if err := sd.Lock(); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}
	if err := sd.Unlock(); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}

	// Should no longer be locked
	if sd.IsLocked() {
		t.Error("Expected IsLocked to return false after unlock")
	}

	// Should be able to acquire lock again
	if err := sd.Lock(); err != nil {
		t.Errorf("Failed to re-acquire lock: %v", err)
	}
	sd.Unlock()
}

func TestSylkDirExists(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)

	// Should not exist initially
	if sd.Exists() {
		t.Error("Expected Exists to return false before init")
	}

	// Init
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Should exist after init
	if !sd.Exists() {
		t.Error("Expected Exists to return true after init")
	}
}

func TestSylkDirPaths(t *testing.T) {
	tmpDir := "/project/root"
	sd := New(tmpDir)

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{"RootPath", sd.RootPath(), "/project/root/.sylk"},
		{"ConfigPath", sd.ConfigPath(), "/project/root/.sylk/config.yaml"},
		{"LockPath", sd.LockPath(), "/project/root/.sylk/lock"},
		{"MetaPath", sd.MetaPath(), "/project/root/.sylk/meta.json"},
		{"SessionsPath", sd.SessionsPath(), "/project/root/.sylk/sessions"},
		{"SessionPath", sd.SessionPath("ses_001"), "/project/root/.sylk/sessions/ses_001"},
		{"GlobalVersionsPath", sd.GlobalVersionsPath(), "/project/root/.sylk/versions"},
		{"GlobalCanonicalIndexPath", sd.GlobalCanonicalIndexPath(), "/project/root/.sylk/data/canonical"},
		{"GlobalIVFPath", sd.GlobalIVFPath(), "/project/root/.sylk/data/ivf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %s, expected %s", tt.got, tt.expected)
			}
		})
	}
}

func TestSylkDirClose(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Acquire lock
	if err := sd.Lock(); err != nil {
		t.Fatalf("Lock failed: %v", err)
	}

	// Close should release the lock
	if err := sd.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Should no longer be locked
	if sd.IsLocked() {
		t.Error("Expected IsLocked to return false after close")
	}
}

func TestValidationErrorFormat(t *testing.T) {
	err := ValidationError{Path: "/some/path", Reason: "missing"}
	expected := "sylkdir validation: /some/path: missing"
	if err.Error() != expected {
		t.Errorf("got %q, expected %q", err.Error(), expected)
	}
}

func TestValidationErrorsFormat(t *testing.T) {
	// Empty errors
	var empty ValidationErrors
	if empty.Error() != "no validation errors" {
		t.Errorf("unexpected empty error message: %s", empty.Error())
	}

	// Single error
	single := ValidationErrors{{Path: "/p", Reason: "r"}}
	if single.Error() != "sylkdir validation: /p: r" {
		t.Errorf("unexpected single error message: %s", single.Error())
	}

	// Multiple errors
	multi := ValidationErrors{
		{Path: "/a", Reason: "ra"},
		{Path: "/b", Reason: "rb"},
	}
	expected := "sylkdir validation: 2 errors (first: sylkdir validation: /a: ra)"
	if multi.Error() != expected {
		t.Errorf("got %q, expected %q", multi.Error(), expected)
	}
}

func TestSylkDirConfigContent(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Read config content
	content, err := os.ReadFile(sd.ConfigPath())
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	// Verify it contains expected fields
	configStr := string(content)
	expectedFields := []string{
		"version:",
		"embedding:",
		"provider:",
		"indexing:",
		"include_patterns:",
		"exclude_patterns:",
		"storage:",
	}

	for _, field := range expectedFields {
		if !contains(configStr, field) {
			t.Errorf("Config missing field: %s", field)
		}
	}
}

func TestSylkDirMetaContent(t *testing.T) {
	tmpDir := t.TempDir()

	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Read meta content
	content, err := os.ReadFile(sd.MetaPath())
	if err != nil {
		t.Fatalf("Failed to read meta: %v", err)
	}

	// Verify it contains expected fields
	metaStr := string(content)
	expectedFields := []string{
		"schema_version",
		"next_node_id",
		"next_session_id",
		"committed_sessions",
	}

	for _, field := range expectedFields {
		if !contains(metaStr, field) {
			t.Errorf("Meta missing field: %s", field)
		}
	}
}

func TestCompactGlobalDataFiles(t *testing.T) {
	tmpDir := t.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gm := NewGlobalMetaFromSylkDir(sd)
	if err := gm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	v1 := gm.GetHead()

	// Write some nodes and docs to v1 via global stores.
	nodeStore, err := NewGlobalVersionNodeStore(sd, v1)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore: %v", err)
	}
	docStore, err := NewGlobalVersionDocStore(sd, v1)
	if err != nil {
		t.Fatalf("NewGlobalVersionDocStore: %v", err)
	}

	nodes := []*Node{
		{ID: 1, CanonicalKey: "file:a.go", Name: "a.go", DocRef: 1},
		{ID: 2, CanonicalKey: "file:b.go", Name: "b.go", DocRef: 2},
		{ID: 3, CanonicalKey: "file:c.go", Name: "c.go", DocRef: 3},
	}
	for _, n := range nodes {
		if err := nodeStore.Write(n); err != nil {
			t.Fatalf("Write node %d: %v", n.ID, err)
		}
	}
	docs := []*VersionDocument{
		{ID: "file_1", Path: "a.go", Content: "aaa"},
		{ID: "file_2", Path: "b.go", Content: "bbb"},
		{ID: "file_3", Path: "c.go", Content: "ccc"},
	}
	if err := docStore.WriteBatch(docs); err != nil {
		t.Fatalf("WriteBatch docs: %v", err)
	}
	nodeStore.SaveIndexes()
	docStore.SaveIndexes()

	// Record the initial data file sizes.
	nodeFileInfo, _ := os.Stat(sd.GlobalNodeDataPath())
	initialNodeSize := nodeFileInfo.Size()
	docFileInfo, _ := os.Stat(sd.GlobalDocDataPath())
	initialDocSize := docFileInfo.Size()

	nodeStore.Close()
	docStore.Close()

	// Mark node 2 as dead (tombstone).
	versionPath := sd.GlobalVersionPath(v1)
	tb, err := LoadOrCreateTombstoneBitmap(versionPath, 10)
	if err != nil {
		t.Fatalf("LoadOrCreateTombstoneBitmap: %v", err)
	}
	tb.MarkDead(2)
	if err := tb.Save(); err != nil {
		t.Fatalf("Save tombstone: %v", err)
	}

	// Create v2 and compact.
	v2 := v1.BumpMajor()
	if err := sd.CreateGlobalVersion(v2); err != nil {
		t.Fatalf("CreateGlobalVersion: %v", err)
	}

	// Update manifest so compactDataFiles can find all versions.
	gm.AddVersion(GlobalVersion{ID: v2, ParentID: v1})
	gm.SetHead(v2)

	if err := sd.CompactGlobalData(v1, v2); err != nil {
		t.Fatalf("CompactGlobalData: %v", err)
	}

	// Verify: node data file should be smaller (node 2 removed).
	nodeFileInfoAfter, _ := os.Stat(sd.GlobalNodeDataPath())
	if nodeFileInfoAfter.Size() >= initialNodeSize {
		t.Errorf("node data file not compacted: before=%d, after=%d", initialNodeSize, nodeFileInfoAfter.Size())
	}

	// Verify: doc data file should be smaller (doc for node 2 removed).
	docFileInfoAfter, _ := os.Stat(sd.GlobalDocDataPath())
	if docFileInfoAfter.Size() >= initialDocSize {
		t.Errorf("doc data file not compacted: before=%d, after=%d", initialDocSize, docFileInfoAfter.Size())
	}

	// Verify: v2's node index can read surviving nodes (1 and 3).
	nodeStoreV2, err := NewGlobalVersionNodeStore(sd, v2)
	if err != nil {
		t.Fatalf("NewGlobalVersionNodeStore v2: %v", err)
	}
	defer nodeStoreV2.Close()

	for _, id := range []uint32{1, 3} {
		node, err := nodeStoreV2.ReadFromVersion(v2, id)
		if err != nil {
			t.Errorf("read node %d after compact: %v", id, err)
			continue
		}
		if node.ID != id {
			t.Errorf("node ID = %d, want %d", node.ID, id)
		}
	}

	// Verify: node 2 should not be in v2's index (dead, compacted out).
	_, err = nodeStoreV2.ReadFromVersion(v2, 2)
	if err == nil {
		t.Error("expected node 2 to be absent after compaction")
	}

	// Verify: v2 doc index has 2 docs (file_1 and file_3).
	docStoreV2, err := NewGlobalVersionDocStore(sd, v2)
	if err != nil {
		t.Fatalf("NewGlobalVersionDocStore v2: %v", err)
	}
	defer docStoreV2.Close()

	docsV2, err := docStoreV2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll docs v2: %v", err)
	}
	if len(docsV2) != 2 {
		t.Errorf("got %d docs after compact, want 2", len(docsV2))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
