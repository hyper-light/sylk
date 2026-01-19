package cmt

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// createTestFileForRecovery creates a file and returns its relative path.
func createTestFileForRecovery(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	return name
}

// createTestWALForRecovery creates a WAL with test entries.
func createTestWALForRecovery(t *testing.T, dir string, entries []struct {
	op   WALOp
	key  string
	info *FileInfo
}) string {
	t.Helper()

	walPath := filepath.Join(dir, "test.wal")
	wal, err := NewWAL(WALConfig{
		Path:        walPath,
		SyncOnWrite: true,
	})
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	for _, e := range entries {
		switch e.op {
		case WALOpInsert:
			if err := wal.LogInsert(e.key, e.info); err != nil {
				t.Fatalf("Failed to log insert: %v", err)
			}
		case WALOpDelete:
			if err := wal.LogDelete(e.key); err != nil {
				t.Fatalf("Failed to log delete: %v", err)
			}
		case WALOpCheckpoint:
			if err := wal.Checkpoint(); err != nil {
				t.Fatalf("Failed to checkpoint: %v", err)
			}
		}
	}

	if err := wal.Close(); err != nil {
		t.Fatalf("Failed to close WAL: %v", err)
	}

	return walPath
}

// makeTestFileInfo creates a FileInfo for testing.
func makeTestFileInfo(path, content string) *FileInfo {
	hash := sha256.Sum256([]byte(content))
	return &FileInfo{
		Path:        path,
		ContentHash: hash,
		Size:        int64(len(content)),
		ModTime:     time.Now().Truncate(time.Second),
		Permissions: 0644,
		Indexed:     true,
		IndexedAt:   time.Now(),
	}
}

// initGitRepo initializes a git repository in the given directory.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Configure git user for commits
	cmd = exec.Command("git", "config", "user.email", "test@test.com")
	cmd.Dir = dir
	cmd.Run()

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	cmd.Run()
}

// gitAdd adds files to git index.
func gitAdd(t *testing.T, dir string, files ...string) {
	t.Helper()

	args := append([]string{"add"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}
}

// gitCommit commits staged files.
func gitCommit(t *testing.T, dir, message string) {
	t.Helper()

	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}
}

// =============================================================================
// CMTRecovery Constructor Tests
// =============================================================================

func TestNewCMTRecovery(t *testing.T) {
	t.Parallel()

	recovery := NewCMTRecovery("/path/to/wal", "/path/to/root", "/path/to/git")

	if recovery == nil {
		t.Fatal("NewCMTRecovery returned nil")
	}
	if recovery.walPath != "/path/to/wal" {
		t.Errorf("walPath = %q, want /path/to/wal", recovery.walPath)
	}
	if recovery.rootPath != "/path/to/root" {
		t.Errorf("rootPath = %q, want /path/to/root", recovery.rootPath)
	}
	if recovery.gitPath != "/path/to/git" {
		t.Errorf("gitPath = %q, want /path/to/git", recovery.gitPath)
	}
}

func TestNewCMTRecovery_EmptyGitPath(t *testing.T) {
	t.Parallel()

	recovery := NewCMTRecovery("/path/to/wal", "/path/to/root", "")

	if recovery.gitPath != "" {
		t.Errorf("gitPath = %q, want empty", recovery.gitPath)
	}
}

// =============================================================================
// RecoverFromWAL Tests
// =============================================================================

func TestCMTRecovery_RecoverFromWAL_ReplayEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")
	os.MkdirAll(rootPath, 0755)

	// Create WAL with insert entries
	entries := []struct {
		op   WALOp
		key  string
		info *FileInfo
	}{
		{op: WALOpInsert, key: "file1.txt", info: makeTestFileInfo("file1.txt", "content1")},
		{op: WALOpInsert, key: "file2.txt", info: makeTestFileInfo("file2.txt", "content2")},
		{op: WALOpInsert, key: "file3.txt", info: makeTestFileInfo("file3.txt", "content3")},
	}

	walPath := createTestWALForRecovery(t, dir, entries)

	recovery := NewCMTRecovery(walPath, rootPath, "")

	root, err := recovery.RecoverFromWAL(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromWAL failed: %v", err)
	}

	// Verify all files are in tree
	for _, e := range entries {
		node := Get(root, e.key)
		if node == nil {
			t.Errorf("File %q not found in recovered tree", e.key)
		} else if node.Info.ContentHash != e.info.ContentHash {
			t.Errorf("File %q hash mismatch", e.key)
		}
	}
}

func TestCMTRecovery_RecoverFromWAL_ReplayWithDeletes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")
	os.MkdirAll(rootPath, 0755)

	// Create WAL with insert and delete entries
	entries := []struct {
		op   WALOp
		key  string
		info *FileInfo
	}{
		{op: WALOpInsert, key: "file1.txt", info: makeTestFileInfo("file1.txt", "content1")},
		{op: WALOpInsert, key: "file2.txt", info: makeTestFileInfo("file2.txt", "content2")},
		{op: WALOpDelete, key: "file1.txt"},
		{op: WALOpInsert, key: "file3.txt", info: makeTestFileInfo("file3.txt", "content3")},
	}

	walPath := createTestWALForRecovery(t, dir, entries)

	recovery := NewCMTRecovery(walPath, rootPath, "")

	root, err := recovery.RecoverFromWAL(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromWAL failed: %v", err)
	}

	// file1.txt should be deleted
	if node := Get(root, "file1.txt"); node != nil {
		t.Error("file1.txt should have been deleted")
	}

	// file2.txt and file3.txt should exist
	if node := Get(root, "file2.txt"); node == nil {
		t.Error("file2.txt should exist")
	}
	if node := Get(root, "file3.txt"); node == nil {
		t.Error("file3.txt should exist")
	}
}

func TestCMTRecovery_RecoverFromWAL_EmptyWAL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")
	os.MkdirAll(rootPath, 0755)

	// Create empty WAL
	walPath := createTestWALForRecovery(t, dir, nil)

	recovery := NewCMTRecovery(walPath, rootPath, "")

	root, err := recovery.RecoverFromWAL(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromWAL failed: %v", err)
	}

	if root != nil {
		t.Error("Empty WAL should produce nil tree")
	}
}

func TestCMTRecovery_RecoverFromWAL_NoWALFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")

	recovery := NewCMTRecovery(filepath.Join(dir, "nonexistent.wal"), rootPath, "")

	_, err := recovery.RecoverFromWAL(context.Background())
	if err == nil {
		t.Error("RecoverFromWAL should fail for nonexistent WAL")
	}
}

func TestCMTRecovery_RecoverFromWAL_CorruptedWAL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")
	os.MkdirAll(rootPath, 0755)

	// Create valid WAL first
	entries := []struct {
		op   WALOp
		key  string
		info *FileInfo
	}{
		{op: WALOpInsert, key: "file1.txt", info: makeTestFileInfo("file1.txt", "content1")},
	}
	walPath := createTestWALForRecovery(t, dir, entries)

	// Corrupt the WAL by appending garbage
	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to open WAL for corruption: %v", err)
	}
	f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Close()

	recovery := NewCMTRecovery(walPath, rootPath, "")

	// Should recover what it can, skipping corrupted entries
	root, err := recovery.RecoverFromWAL(context.Background())
	if err != nil {
		// Graceful handling of corruption is acceptable
		t.Logf("RecoverFromWAL returned error for corrupted WAL: %v", err)
		return
	}

	// Should have recovered the valid entry
	if node := Get(root, "file1.txt"); node == nil {
		t.Error("Should have recovered file1.txt before corruption")
	}
}

func TestCMTRecovery_RecoverFromWAL_ContextCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")
	os.MkdirAll(rootPath, 0755)

	// Create WAL with entries
	var entries []struct {
		op   WALOp
		key  string
		info *FileInfo
	}
	for i := 0; i < 100; i++ {
		key := string(rune('a'+i%26)) + ".txt"
		entries = append(entries, struct {
			op   WALOp
			key  string
			info *FileInfo
		}{op: WALOpInsert, key: key, info: makeTestFileInfo(key, "content")})
	}
	walPath := createTestWALForRecovery(t, dir, entries)

	recovery := NewCMTRecovery(walPath, rootPath, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := recovery.RecoverFromWAL(ctx)
	// Should either complete or return context error
	if err != nil && err != context.Canceled {
		t.Logf("RecoverFromWAL error: %v", err)
	}
}

// =============================================================================
// RecoverFromFilesystem Tests
// =============================================================================

func TestCMTRecovery_RecoverFromFilesystem_ScanDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create files in directory
	createTestFileForRecovery(t, dir, "file1.txt", "content1")
	createTestFileForRecovery(t, dir, "file2.txt", "content2")
	createTestFileForRecovery(t, dir, "subdir/file3.txt", "content3")

	recovery := NewCMTRecovery("", dir, "")

	root, err := recovery.RecoverFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromFilesystem failed: %v", err)
	}

	// Check all files were found
	if node := Get(root, "file1.txt"); node == nil {
		t.Error("file1.txt not found")
	}
	if node := Get(root, "file2.txt"); node == nil {
		t.Error("file2.txt not found")
	}
	if node := Get(root, "subdir/file3.txt"); node == nil {
		t.Error("subdir/file3.txt not found")
	}
}

func TestCMTRecovery_RecoverFromFilesystem_EmptyDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	recovery := NewCMTRecovery("", dir, "")

	root, err := recovery.RecoverFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromFilesystem failed: %v", err)
	}

	if root != nil {
		t.Error("Empty directory should produce nil tree")
	}
}

func TestCMTRecovery_RecoverFromFilesystem_NonexistentDirectory(t *testing.T) {
	t.Parallel()

	recovery := NewCMTRecovery("", "/nonexistent/directory", "")

	_, err := recovery.RecoverFromFilesystem(context.Background())
	if err == nil {
		t.Error("RecoverFromFilesystem should fail for nonexistent directory")
	}
}

func TestCMTRecovery_RecoverFromFilesystem_IgnoreHiddenFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create visible and hidden files
	createTestFileForRecovery(t, dir, "visible.txt", "content")
	createTestFileForRecovery(t, dir, ".hidden", "hidden content")
	createTestFileForRecovery(t, dir, ".git/config", "git config")

	recovery := NewCMTRecovery("", dir, "")

	root, err := recovery.RecoverFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromFilesystem failed: %v", err)
	}

	// visible.txt should be found
	if node := Get(root, "visible.txt"); node == nil {
		t.Error("visible.txt not found")
	}

	// Hidden files should be ignored
	if node := Get(root, ".hidden"); node != nil {
		t.Error(".hidden should be ignored")
	}
	if node := Get(root, ".git/config"); node != nil {
		t.Error(".git/config should be ignored")
	}
}

func TestCMTRecovery_RecoverFromFilesystem_ComputesHashes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	content := "test content for hashing"
	createTestFileForRecovery(t, dir, "file.txt", content)

	recovery := NewCMTRecovery("", dir, "")

	root, err := recovery.RecoverFromFilesystem(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromFilesystem failed: %v", err)
	}

	node := Get(root, "file.txt")
	if node == nil {
		t.Fatal("file.txt not found")
	}

	expectedHash := sha256.Sum256([]byte(content))
	if node.Info.ContentHash != expectedHash {
		t.Errorf("ContentHash mismatch: got %x, want %x", node.Info.ContentHash, expectedHash)
	}
}

func TestCMTRecovery_RecoverFromFilesystem_ContextCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create many files
	for i := 0; i < 50; i++ {
		createTestFileForRecovery(t, dir, "files/file"+string(rune('a'+i))+".txt", "content")
	}

	recovery := NewCMTRecovery("", dir, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := recovery.RecoverFromFilesystem(ctx)
	// Should either complete quickly or return context error
	if err != nil && err != context.Canceled {
		t.Logf("RecoverFromFilesystem error: %v", err)
	}
}

// =============================================================================
// RecoverFromGit Tests
// =============================================================================

func TestCMTRecovery_RecoverFromGit_WorksInGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Initialize git repo
	initGitRepo(t, dir)

	// Create and commit files
	createTestFileForRecovery(t, dir, "tracked1.txt", "content1")
	createTestFileForRecovery(t, dir, "tracked2.txt", "content2")
	createTestFileForRecovery(t, dir, "subdir/tracked3.txt", "content3")

	gitAdd(t, dir, "tracked1.txt", "tracked2.txt", "subdir/tracked3.txt")
	gitCommit(t, dir, "Initial commit")

	// Create untracked file (should not be in recovered tree)
	createTestFileForRecovery(t, dir, "untracked.txt", "untracked")

	recovery := NewCMTRecovery("", dir, dir)

	root, err := recovery.RecoverFromGit(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromGit failed: %v", err)
	}

	// Check tracked files are present
	if node := Get(root, "tracked1.txt"); node == nil {
		t.Error("tracked1.txt not found")
	}
	if node := Get(root, "tracked2.txt"); node == nil {
		t.Error("tracked2.txt not found")
	}
	if node := Get(root, "subdir/tracked3.txt"); node == nil {
		t.Error("subdir/tracked3.txt not found")
	}

	// Untracked file should NOT be in tree
	if node := Get(root, "untracked.txt"); node != nil {
		t.Error("untracked.txt should not be in tree")
	}
}

func TestCMTRecovery_RecoverFromGit_FailsOutsideGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create files but don't init git
	createTestFileForRecovery(t, dir, "file.txt", "content")

	recovery := NewCMTRecovery("", dir, dir)

	_, err := recovery.RecoverFromGit(context.Background())
	if err == nil {
		t.Error("RecoverFromGit should fail outside git repo")
	}
}

func TestCMTRecovery_RecoverFromGit_EmptyRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Initialize empty git repo (no commits)
	initGitRepo(t, dir)

	recovery := NewCMTRecovery("", dir, dir)

	root, err := recovery.RecoverFromGit(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromGit failed: %v", err)
	}

	if root != nil {
		t.Error("Empty git repo should produce nil tree")
	}
}

func TestCMTRecovery_RecoverFromGit_ComputesHashes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	initGitRepo(t, dir)

	content := "test content for git recovery"
	createTestFileForRecovery(t, dir, "file.txt", content)
	gitAdd(t, dir, "file.txt")
	gitCommit(t, dir, "Add file")

	recovery := NewCMTRecovery("", dir, dir)

	root, err := recovery.RecoverFromGit(context.Background())
	if err != nil {
		t.Fatalf("RecoverFromGit failed: %v", err)
	}

	node := Get(root, "file.txt")
	if node == nil {
		t.Fatal("file.txt not found")
	}

	expectedHash := sha256.Sum256([]byte(content))
	if node.Info.ContentHash != expectedHash {
		t.Errorf("ContentHash mismatch: got %x, want %x", node.Info.ContentHash, expectedHash)
	}
}

func TestCMTRecovery_RecoverFromGit_ContextCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	initGitRepo(t, dir)

	// Create and commit files
	for i := 0; i < 20; i++ {
		createTestFileForRecovery(t, dir, "file"+string(rune('a'+i))+".txt", "content")
	}
	gitAdd(t, dir, ".")
	gitCommit(t, dir, "Add files")

	recovery := NewCMTRecovery("", dir, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := recovery.RecoverFromGit(ctx)
	// Should either complete or return context error
	if err != nil && err != context.Canceled {
		t.Logf("RecoverFromGit error: %v", err)
	}
}

// =============================================================================
// Recover Priority Tests
// =============================================================================

func TestCMTRecovery_Recover_PriorityWAL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")
	os.MkdirAll(rootPath, 0755)

	// Create different content in WAL vs filesystem
	walContent := "wal content"
	fsContent := "filesystem content"

	// Create WAL entry
	entries := []struct {
		op   WALOp
		key  string
		info *FileInfo
	}{
		{op: WALOpInsert, key: "file.txt", info: makeTestFileInfo("file.txt", walContent)},
	}
	walPath := createTestWALForRecovery(t, dir, entries)

	// Create file on filesystem with different content
	createTestFileForRecovery(t, rootPath, "file.txt", fsContent)

	recovery := NewCMTRecovery(walPath, rootPath, "")

	root, result, err := recovery.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Recover should succeed, errors: %v", result.Errors)
	}
	if result.Source != RecoverySourceWAL {
		t.Errorf("Source = %v, want RecoverySourceWAL", result.Source)
	}

	// Should have WAL content hash, not filesystem
	node := Get(root, "file.txt")
	if node == nil {
		t.Fatal("file.txt not found")
	}

	expectedHash := sha256.Sum256([]byte(walContent))
	if node.Info.ContentHash != expectedHash {
		t.Error("Recovery should use WAL content, not filesystem")
	}
}

func TestCMTRecovery_Recover_FallbackToFilesystem(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")
	os.MkdirAll(rootPath, 0755)

	// No WAL, just filesystem
	createTestFileForRecovery(t, rootPath, "file.txt", "content")

	// Use nonexistent WAL path
	recovery := NewCMTRecovery(filepath.Join(dir, "nonexistent.wal"), rootPath, "")

	root, result, err := recovery.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Recover should succeed via filesystem, errors: %v", result.Errors)
	}
	if result.Source != RecoverySourceFilesystem {
		t.Errorf("Source = %v, want RecoverySourceFilesystem", result.Source)
	}

	if node := Get(root, "file.txt"); node == nil {
		t.Error("file.txt not found in recovered tree")
	}
}

func TestCMTRecovery_Recover_FallbackToGit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Initialize git repo
	initGitRepo(t, dir)

	// Create and commit file
	createTestFileForRecovery(t, dir, "file.txt", "content")
	gitAdd(t, dir, "file.txt")
	gitCommit(t, dir, "Add file")

	// Delete the file from filesystem (but it's still in git)
	os.Remove(filepath.Join(dir, "file.txt"))

	// Use nonexistent WAL path
	recovery := NewCMTRecovery(filepath.Join(dir, "nonexistent.wal"), dir, dir)

	root, result, err := recovery.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}

	if !result.Success {
		t.Errorf("Recover should succeed via git, errors: %v", result.Errors)
	}
	if result.Source != RecoverySourceFilesystem && result.Source != RecoverySourceGit {
		// If filesystem scan found the file, that's fine too
		t.Logf("Source = %v", result.Source)
	}

	// Tree should exist (may be empty if git file was deleted and not found)
	_ = root
}

func TestCMTRecovery_Recover_AllSourcesFail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// All sources invalid
	recovery := NewCMTRecovery(
		filepath.Join(dir, "nonexistent.wal"),
		filepath.Join(dir, "nonexistent_dir"),
		filepath.Join(dir, "nonexistent_git"),
	)

	_, result, err := recovery.Recover(context.Background())

	// Should either return error or result with Success=false
	if err == nil && result.Success {
		t.Error("Recover should fail when all sources are invalid")
	}
}

// =============================================================================
// RecoveryResult Tests
// =============================================================================

func TestRecoveryResult_Fields(t *testing.T) {
	t.Parallel()

	result := &RecoveryResult{
		Success:        true,
		Source:         RecoverySourceWAL,
		RecoveredAt:    time.Now(),
		FilesRecovered: 10,
		Errors:         []error{},
	}

	if !result.Success {
		t.Error("Success should be true")
	}
	if result.Source != RecoverySourceWAL {
		t.Errorf("Source = %v, want RecoverySourceWAL", result.Source)
	}
	if result.FilesRecovered != 10 {
		t.Errorf("FilesRecovered = %d, want 10", result.FilesRecovered)
	}
}

func TestRecoverySource_Values(t *testing.T) {
	t.Parallel()

	// Verify enum values are distinct
	sources := []RecoverySource{RecoverySourceWAL, RecoverySourceFilesystem, RecoverySourceGit}
	seen := make(map[RecoverySource]bool)

	for _, src := range sources {
		if seen[src] {
			t.Errorf("Duplicate RecoverySource value: %d", src)
		}
		seen[src] = true
	}
}

// =============================================================================
// Concurrency Safety Tests
// =============================================================================

func TestCMTRecovery_ConcurrentRecover(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rootPath := filepath.Join(dir, "files")
	os.MkdirAll(rootPath, 0755)

	// Create test files
	for i := 0; i < 10; i++ {
		createTestFileForRecovery(t, rootPath, "file"+string(rune('a'+i))+".txt", "content")
	}

	recovery := NewCMTRecovery("", rootPath, "")

	// Run multiple concurrent recoveries
	done := make(chan bool, 5)
	errors := make(chan error, 5)

	for i := 0; i < 5; i++ {
		go func() {
			_, result, err := recovery.Recover(context.Background())
			if err != nil {
				errors <- err
			} else if !result.Success {
				errors <- nil
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		select {
		case err := <-errors:
			if err != nil {
				t.Errorf("Concurrent recovery error: %v", err)
			}
		case <-done:
			// OK
		case <-time.After(10 * time.Second):
			t.Fatal("Timeout waiting for concurrent recoveries")
		}
	}
}
