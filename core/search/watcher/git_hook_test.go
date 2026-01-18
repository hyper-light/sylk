// Package watcher provides file system and git integration for change detection
// in the Sylk Document Search System.
package watcher

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// createTestGitRepo creates a temporary git repository for testing.
// Returns the repository path.
func createTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Configure git user for commits
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to configure git email: %v", err)
	}

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to configure git name: %v", err)
	}

	return dir
}

// createTestFile creates a file in the given directory.
func createTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)

	parentDir := filepath.Dir(path)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("failed to create parent directory: %v", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create file %s: %v", path, err)
	}
	return path
}

// gitAdd stages files in the repository.
func gitAdd(t *testing.T, repoPath string, files ...string) {
	t.Helper()
	args := append([]string{"add"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git add: %v", err)
	}
}

// gitCommit creates a commit in the repository.
func gitCommit(t *testing.T, repoPath, message string) string {
	t.Helper()
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}

	// Get commit hash
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get commit hash: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// gitCheckout switches to a branch.
func gitCheckout(t *testing.T, repoPath, branch string, create bool) {
	t.Helper()
	args := []string{"checkout"}
	if create {
		args = append(args, "-b")
	}
	args = append(args, branch)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to git checkout: %v", err)
	}
}

// readHookFile reads the contents of a hook file.
func readHookFile(t *testing.T, repoPath, hookName string) string {
	t.Helper()
	hookPath := filepath.Join(repoPath, ".git", "hooks", hookName)
	content, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("failed to read hook file: %v", err)
	}
	return string(content)
}

// hookExists checks if a hook file exists.
func hookExists(t *testing.T, repoPath, hookName string) bool {
	t.Helper()
	hookPath := filepath.Join(repoPath, ".git", "hooks", hookName)
	_, err := os.Stat(hookPath)
	return err == nil
}

// hookIsExecutable checks if a hook file is executable.
func hookIsExecutable(t *testing.T, repoPath, hookName string) bool {
	t.Helper()
	hookPath := filepath.Join(repoPath, ".git", "hooks", hookName)
	info, err := os.Stat(hookPath)
	if err != nil {
		return false
	}
	return info.Mode()&0111 != 0
}

// writeExistingHook writes an existing hook script for testing non-invasive appending.
func writeExistingHook(t *testing.T, repoPath, hookName, content string) {
	t.Helper()
	hooksDir := filepath.Join(repoPath, ".git", "hooks")
	hookPath := filepath.Join(hooksDir, hookName)

	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}

	if err := os.WriteFile(hookPath, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write existing hook: %v", err)
	}
}

// =============================================================================
// NewGitHookWatcher Tests
// =============================================================================

func TestNewGitHookWatcher_ValidConfig(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit", "post-checkout", "post-merge"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if watcher == nil {
		t.Fatal("NewGitHookWatcher() returned nil")
	}
}

func TestNewGitHookWatcher_EmptyRepoPath(t *testing.T) {
	t.Parallel()

	config := GitHookConfig{
		RepoPath:  "",
		HookTypes: []string{"post-commit"},
	}

	_, err := NewGitHookWatcher(config)
	if err == nil {
		t.Error("NewGitHookWatcher() expected error for empty repo path")
	}
	if err != ErrRepoPathEmpty {
		t.Errorf("NewGitHookWatcher() error = %v, want %v", err, ErrRepoPathEmpty)
	}
}

func TestNewGitHookWatcher_NonExistentRepoPath(t *testing.T) {
	t.Parallel()

	config := GitHookConfig{
		RepoPath:  "/nonexistent/path/that/does/not/exist",
		HookTypes: []string{"post-commit"},
	}

	_, err := NewGitHookWatcher(config)
	if err == nil {
		t.Error("NewGitHookWatcher() expected error for non-existent path")
	}
	if err != ErrRepoPathNotExist {
		t.Errorf("NewGitHookWatcher() error = %v, want %v", err, ErrRepoPathNotExist)
	}
}

func TestNewGitHookWatcher_NotAGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir() // Not a git repo

	config := GitHookConfig{
		RepoPath:  dir,
		HookTypes: []string{"post-commit"},
	}

	_, err := NewGitHookWatcher(config)
	if err == nil {
		t.Error("NewGitHookWatcher() expected error for non-git directory")
	}
	if err != ErrNotAGitRepo {
		t.Errorf("NewGitHookWatcher() error = %v, want %v", err, ErrNotAGitRepo)
	}
}

func TestNewGitHookWatcher_InvalidHookType(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"invalid-hook"},
	}

	_, err := NewGitHookWatcher(config)
	if err == nil {
		t.Error("NewGitHookWatcher() expected error for invalid hook type")
	}
	if err != ErrInvalidHookType {
		t.Errorf("NewGitHookWatcher() error = %v, want %v", err, ErrInvalidHookType)
	}
}

func TestNewGitHookWatcher_DefaultHookTypes(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: nil, // Should use defaults
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if watcher == nil {
		t.Fatal("NewGitHookWatcher() returned nil")
	}
}

// =============================================================================
// InstallHooks Tests
// =============================================================================

func TestGitHookWatcher_InstallHooks_NewHooks(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit", "post-checkout", "post-merge"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Verify all hooks were created
	for _, hookType := range config.HookTypes {
		if !hookExists(t, repoPath, hookType) {
			t.Errorf("hook %q was not created", hookType)
		}

		if !hookIsExecutable(t, repoPath, hookType) {
			t.Errorf("hook %q is not executable", hookType)
		}

		content := readHookFile(t, repoPath, hookType)
		if !strings.Contains(content, "#!/") {
			t.Errorf("hook %q missing shebang", hookType)
		}
		if !strings.Contains(content, SylkHookStartMarker) {
			t.Errorf("hook %q missing start marker", hookType)
		}
		if !strings.Contains(content, SylkHookEndMarker) {
			t.Errorf("hook %q missing end marker", hookType)
		}
	}
}

func TestGitHookWatcher_InstallHooks_AppendToExisting(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	existingContent := `#!/bin/bash
# Existing hook content
echo "Running existing hook"
exit 0
`
	writeExistingHook(t, repoPath, "post-commit", existingContent)

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	content := readHookFile(t, repoPath, "post-commit")

	// Verify existing content is preserved
	if !strings.Contains(content, "Running existing hook") {
		t.Error("existing hook content was not preserved")
	}

	// Verify Sylk markers were added
	if !strings.Contains(content, SylkHookStartMarker) {
		t.Error("Sylk start marker not found")
	}
	if !strings.Contains(content, SylkHookEndMarker) {
		t.Error("Sylk end marker not found")
	}
}

func TestGitHookWatcher_InstallHooks_Idempotent(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	// Install twice
	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() first call error = %v", err)
	}
	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() second call error = %v", err)
	}

	content := readHookFile(t, repoPath, "post-commit")

	// Should only have one set of markers
	startCount := strings.Count(content, SylkHookStartMarker)
	endCount := strings.Count(content, SylkHookEndMarker)

	if startCount != 1 {
		t.Errorf("expected 1 start marker, found %d", startCount)
	}
	if endCount != 1 {
		t.Errorf("expected 1 end marker, found %d", endCount)
	}
}

func TestGitHookWatcher_InstallHooks_CreatesHooksDirectory(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	// Remove hooks directory if it exists
	hooksDir := filepath.Join(repoPath, ".git", "hooks")
	os.RemoveAll(hooksDir)

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	// Verify hooks directory was created
	info, err := os.Stat(hooksDir)
	if err != nil {
		t.Fatalf("hooks directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("hooks path is not a directory")
	}
}

// =============================================================================
// UninstallHooks Tests
// =============================================================================

func TestGitHookWatcher_UninstallHooks_RemovesSylkContent(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	if err := watcher.UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	content := readHookFile(t, repoPath, "post-commit")

	if strings.Contains(content, SylkHookStartMarker) {
		t.Error("Sylk start marker should be removed")
	}
	if strings.Contains(content, SylkHookEndMarker) {
		t.Error("Sylk end marker should be removed")
	}
}

func TestGitHookWatcher_UninstallHooks_PreservesOtherContent(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	existingContent := `#!/bin/bash
# Existing hook content
echo "Running existing hook"
`
	writeExistingHook(t, repoPath, "post-commit", existingContent)

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	if err := watcher.UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	content := readHookFile(t, repoPath, "post-commit")

	// Existing content should be preserved
	if !strings.Contains(content, "Running existing hook") {
		t.Error("existing hook content was not preserved after uninstall")
	}
}

func TestGitHookWatcher_UninstallHooks_RemovesEmptyHook(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	if err := watcher.UninstallHooks(); err != nil {
		t.Fatalf("UninstallHooks() error = %v", err)
	}

	// Hook file should be removed if it only had Sylk content
	hookPath := filepath.Join(repoPath, ".git", "hooks", "post-commit")
	_, err = os.Stat(hookPath)
	if !os.IsNotExist(err) {
		// File exists, check if content is effectively empty (only shebang and whitespace)
		content := readHookFile(t, repoPath, "post-commit")
		trimmed := strings.TrimSpace(content)
		trimmed = strings.TrimPrefix(trimmed, "#!/bin/bash")
		trimmed = strings.TrimPrefix(trimmed, "#!/bin/sh")
		trimmed = strings.TrimSpace(trimmed)
		if trimmed != "" {
			t.Error("hook file should be empty or removed after uninstall")
		}
	}
}

func TestGitHookWatcher_UninstallHooks_NoHooksInstalled(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	// Uninstall without installing first - should not error
	if err := watcher.UninstallHooks(); err != nil {
		t.Errorf("UninstallHooks() error = %v, expected nil for non-existent hooks", err)
	}
}

// =============================================================================
// ParseHookEvent Tests
// =============================================================================

func TestGitHookWatcher_ParseHookEvent_PostCommit(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	// Create a commit to have a valid HEAD
	createTestFile(t, repoPath, "test.txt", "content")
	gitAdd(t, repoPath, "test.txt")
	commitHash := gitCommit(t, repoPath, "Initial commit")

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	event, err := watcher.ParseHookEvent("post-commit", []string{})
	if err != nil {
		t.Fatalf("ParseHookEvent() error = %v", err)
	}

	if event.Operation != GitCommit {
		t.Errorf("Operation = %v, want GitCommit", event.Operation)
	}
	if event.CommitHash != commitHash {
		t.Errorf("CommitHash = %q, want %q", event.CommitHash, commitHash)
	}
	if event.RepoPath != repoPath {
		t.Errorf("RepoPath = %q, want %q", event.RepoPath, repoPath)
	}
	if event.Time.IsZero() {
		t.Error("Time should not be zero")
	}
}

func TestGitHookWatcher_ParseHookEvent_PostCheckout(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	// Create initial commit
	createTestFile(t, repoPath, "test.txt", "content")
	gitAdd(t, repoPath, "test.txt")
	prevHash := gitCommit(t, repoPath, "Initial commit")

	// Create a new branch and commit
	gitCheckout(t, repoPath, "feature", true)
	createTestFile(t, repoPath, "feature.txt", "feature content")
	gitAdd(t, repoPath, "feature.txt")
	newHash := gitCommit(t, repoPath, "Feature commit")

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-checkout"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	// post-checkout args: <prev-head> <new-head> <branch-flag>
	// branch-flag: 1 = branch checkout, 0 = file checkout
	event, err := watcher.ParseHookEvent("post-checkout", []string{prevHash, newHash, "1"})
	if err != nil {
		t.Fatalf("ParseHookEvent() error = %v", err)
	}

	if event.Operation != GitCheckout {
		t.Errorf("Operation = %v, want GitCheckout", event.Operation)
	}
	if event.CommitHash != newHash {
		t.Errorf("CommitHash = %q, want %q", event.CommitHash, newHash)
	}
}

func TestGitHookWatcher_ParseHookEvent_PostMerge(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	// Create initial commit
	createTestFile(t, repoPath, "test.txt", "content")
	gitAdd(t, repoPath, "test.txt")
	gitCommit(t, repoPath, "Initial commit")

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-merge"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	// post-merge args: <squash-flag>
	// squash-flag: 1 = squash merge, 0 = normal merge
	event, err := watcher.ParseHookEvent("post-merge", []string{"0"})
	if err != nil {
		t.Fatalf("ParseHookEvent() error = %v", err)
	}

	if event.Operation != GitMerge {
		t.Errorf("Operation = %v, want GitMerge", event.Operation)
	}
}

func TestGitHookWatcher_ParseHookEvent_InvalidHookType(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	_, err = watcher.ParseHookEvent("invalid-hook", []string{})
	if err == nil {
		t.Error("ParseHookEvent() expected error for invalid hook type")
	}
}

func TestGitHookWatcher_ParseHookEvent_PostCheckout_InvalidArgs(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-checkout"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	// post-checkout requires 3 args
	_, err = watcher.ParseHookEvent("post-checkout", []string{"arg1"})
	if err == nil {
		t.Error("ParseHookEvent() expected error for insufficient args")
	}
}

// =============================================================================
// GetAffectedFiles Tests
// =============================================================================

func TestGitHookWatcher_GetAffectedFiles_Commit(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	// Create initial commit
	createTestFile(t, repoPath, "test.txt", "content")
	gitAdd(t, repoPath, "test.txt")
	gitCommit(t, repoPath, "Initial commit")

	// Create second commit with new files
	createTestFile(t, repoPath, "new_file.go", "package main")
	createTestFile(t, repoPath, "another.go", "package main")
	gitAdd(t, repoPath, "new_file.go", "another.go")
	commitHash := gitCommit(t, repoPath, "Add new files")

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	event := &GitEvent{
		Operation:  GitCommit,
		CommitHash: commitHash,
		RepoPath:   repoPath,
		Time:       time.Now(),
	}

	files, err := watcher.GetAffectedFiles(event)
	if err != nil {
		t.Fatalf("GetAffectedFiles() error = %v", err)
	}

	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}

	// Check that expected files are present
	fileSet := make(map[string]bool)
	for _, f := range files {
		fileSet[filepath.Base(f)] = true
	}

	if !fileSet["new_file.go"] {
		t.Error("new_file.go not found in affected files")
	}
	if !fileSet["another.go"] {
		t.Error("another.go not found in affected files")
	}
}

func TestGitHookWatcher_GetAffectedFiles_Checkout(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	// Create initial commit on main
	createTestFile(t, repoPath, "main.txt", "main content")
	gitAdd(t, repoPath, "main.txt")
	mainHash := gitCommit(t, repoPath, "Initial commit")

	// Create feature branch with different files
	gitCheckout(t, repoPath, "feature", true)
	createTestFile(t, repoPath, "feature.txt", "feature content")
	gitAdd(t, repoPath, "feature.txt")
	featureHash := gitCommit(t, repoPath, "Feature commit")

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-checkout"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	event := &GitEvent{
		Operation:  GitCheckout,
		CommitHash: featureHash,
		FromBranch: mainHash,
		ToBranch:   featureHash,
		RepoPath:   repoPath,
		Time:       time.Now(),
	}

	files, err := watcher.GetAffectedFiles(event)
	if err != nil {
		t.Fatalf("GetAffectedFiles() error = %v", err)
	}

	// Should include the feature.txt that was added
	found := false
	for _, f := range files {
		if filepath.Base(f) == "feature.txt" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("feature.txt not found in affected files: %v", files)
	}
}

func TestGitHookWatcher_GetAffectedFiles_NilEvent(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	_, err = watcher.GetAffectedFiles(nil)
	if err == nil {
		t.Error("GetAffectedFiles() expected error for nil event")
	}
}

func TestGitHookWatcher_GetAffectedFiles_InvalidCommitHash(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)

	// Create at least one commit
	createTestFile(t, repoPath, "test.txt", "content")
	gitAdd(t, repoPath, "test.txt")
	gitCommit(t, repoPath, "Initial commit")

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	event := &GitEvent{
		Operation:  GitCommit,
		CommitHash: "invalid_hash_that_does_not_exist",
		RepoPath:   repoPath,
		Time:       time.Now(),
	}

	_, err = watcher.GetAffectedFiles(event)
	if err == nil {
		t.Error("GetAffectedFiles() expected error for invalid commit hash")
	}
}

// =============================================================================
// GitOperation String Tests
// =============================================================================

func TestGitOperation_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op   GitOperation
		want string
	}{
		{GitCommit, "commit"},
		{GitCheckout, "checkout"},
		{GitMerge, "merge"},
		{GitOperation(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.op.String(); got != tt.want {
				t.Errorf("GitOperation.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Error Tests
// =============================================================================

func TestGitHookErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err     error
		wantMsg string
	}{
		{ErrRepoPathEmpty, "repository path cannot be empty"},
		{ErrRepoPathNotExist, "repository path does not exist"},
		{ErrNotAGitRepo, "path is not a git repository"},
		{ErrInvalidHookType, "invalid hook type"},
		{ErrNilEvent, "event cannot be nil"},
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

// =============================================================================
// Hook Content Tests
// =============================================================================

func TestHookScriptContent(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	content := readHookFile(t, repoPath, "post-commit")

	// Verify hook script structure
	if !strings.Contains(content, "#!/") {
		t.Error("hook missing shebang")
	}
	if !strings.Contains(content, "sylk") {
		t.Error("hook missing sylk command")
	}
	if !strings.Contains(content, "post-commit") {
		t.Error("hook missing hook type reference")
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestGitHookWatcher_InstallHooks_SpecialCharactersInPath(t *testing.T) {
	t.Parallel()

	// Create a directory with spaces
	baseDir := t.TempDir()
	repoPath := filepath.Join(baseDir, "repo with spaces")
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	if !hookExists(t, repoPath, "post-commit") {
		t.Error("hook was not created for path with spaces")
	}
}

func TestGitHookWatcher_AllHookTypes(t *testing.T) {
	t.Parallel()

	repoPath := createTestGitRepo(t)
	allHooks := []string{"post-commit", "post-checkout", "post-merge"}

	config := GitHookConfig{
		RepoPath:  repoPath,
		HookTypes: allHooks,
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		t.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	if err := watcher.InstallHooks(); err != nil {
		t.Fatalf("InstallHooks() error = %v", err)
	}

	for _, hookType := range allHooks {
		if !hookExists(t, repoPath, hookType) {
			t.Errorf("hook %q was not created", hookType)
		}
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkGitHookWatcher_InstallHooks(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		dir := b.TempDir()
		cmd := exec.Command("git", "init")
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			b.Fatalf("failed to init git repo: %v", err)
		}
		b.StartTimer()

		config := GitHookConfig{
			RepoPath:  dir,
			HookTypes: []string{"post-commit", "post-checkout", "post-merge"},
		}

		watcher, err := NewGitHookWatcher(config)
		if err != nil {
			b.Fatalf("NewGitHookWatcher() error = %v", err)
		}

		if err := watcher.InstallHooks(); err != nil {
			b.Fatalf("InstallHooks() error = %v", err)
		}
	}
}

func BenchmarkGitHookWatcher_ParseHookEvent(b *testing.B) {
	dir := b.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		b.Fatalf("failed to init git repo: %v", err)
	}

	// Configure git and create a commit
	exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "Test User").Run()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("content"), 0644)
	exec.Command("git", "-C", dir, "add", "test.txt").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "Initial commit").Run()

	config := GitHookConfig{
		RepoPath:  dir,
		HookTypes: []string{"post-commit"},
	}

	watcher, err := NewGitHookWatcher(config)
	if err != nil {
		b.Fatalf("NewGitHookWatcher() error = %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := watcher.ParseHookEvent("post-commit", []string{})
		if err != nil {
			b.Fatalf("ParseHookEvent() error = %v", err)
		}
	}
}
