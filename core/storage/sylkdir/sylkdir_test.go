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

	// Verify all directories were created
	expectedDirs := []string{
		sd.RootPath(),
		sd.KnowledgePath(),
		sd.NodesPath(),
		sd.NodeBlocksPath(),
		sd.NodeIndexPath(),
		sd.EdgesPath(),
		sd.VectorsPath(),
		sd.VectorShardsPath(),
		sd.VectorGraphPath(),
		sd.VectorPartitionsPath(),
		sd.BlevePath(),
		sd.BleveIndexPath(),
		sd.SessionsPath(),
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

	// Verify meta.json was created
	metaPath := sd.MetaPath()
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("Expected meta.json at %s: %v", metaPath, err)
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

	// Remove a required directory
	if err := os.RemoveAll(sd.NodesPath()); err != nil {
		t.Fatalf("Failed to remove nodes directory: %v", err)
	}

	// Validation should fail
	err := sd.Validate()
	if err == nil {
		t.Fatal("Expected validation to fail with missing nodes directory")
	}

	validationErrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("Expected ValidationErrors, got %T", err)
	}

	// Should report missing nodes-related directories
	foundNodesError := false
	for _, e := range validationErrs {
		if filepath.Base(e.Path) == "nodes" ||
			filepath.Base(e.Path) == "blocks" ||
			filepath.Base(e.Path) == "index" {
			foundNodesError = true
			break
		}
	}
	if !foundNodesError {
		t.Error("Expected validation error for missing nodes directory")
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
		{"KnowledgePath", sd.KnowledgePath(), "/project/root/.sylk/knowledge"},
		{"MetaPath", sd.MetaPath(), "/project/root/.sylk/knowledge/meta.json"},
		{"NodesPath", sd.NodesPath(), "/project/root/.sylk/knowledge/nodes"},
		{"NodeBlocksPath", sd.NodeBlocksPath(), "/project/root/.sylk/knowledge/nodes/blocks"},
		{"NodeIndexPath", sd.NodeIndexPath(), "/project/root/.sylk/knowledge/nodes/index"},
		{"EdgesPath", sd.EdgesPath(), "/project/root/.sylk/knowledge/edges"},
		{"VectorsPath", sd.VectorsPath(), "/project/root/.sylk/knowledge/vectors"},
		{"VectorShardsPath", sd.VectorShardsPath(), "/project/root/.sylk/knowledge/vectors/shards"},
		{"VectorGraphPath", sd.VectorGraphPath(), "/project/root/.sylk/knowledge/vectors/graph"},
		{"VectorPartitionsPath", sd.VectorPartitionsPath(), "/project/root/.sylk/knowledge/vectors/partitions"},
		{"BlevePath", sd.BlevePath(), "/project/root/.sylk/bleve"},
		{"BleveIndexPath", sd.BleveIndexPath(), "/project/root/.sylk/bleve/index"},
		{"SessionsPath", sd.SessionsPath(), "/project/root/.sylk/sessions"},
		{"SessionPath", sd.SessionPath("ses_001"), "/project/root/.sylk/sessions/ses_001"},
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
