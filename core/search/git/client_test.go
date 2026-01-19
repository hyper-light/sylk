// Package git provides git integration for the Sylk Document Search System.
package git

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// Test Fixtures
// =============================================================================

// testRepo creates a temporary git repository for testing.
// Returns the repo path and a cleanup function.
func testRepo(t *testing.T) (string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "sylk-git-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	_, err = gogit.PlainInit(dir, false)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("failed to init git repo: %v", err)
	}

	return dir, func() { os.RemoveAll(dir) }
}

// testRepoWithCommit creates a test repo with an initial commit.
func testRepoWithCommit(t *testing.T) (string, func()) {
	t.Helper()

	dir, cleanup := testRepo(t)

	// Create a test file and commit it
	testFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		cleanup()
		t.Fatalf("failed to write test file: %v", err)
	}

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		cleanup()
		t.Fatalf("failed to open repo: %v", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		cleanup()
		t.Fatalf("failed to get worktree: %v", err)
	}

	if _, err := w.Add("test.txt"); err != nil {
		cleanup()
		t.Fatalf("failed to add file: %v", err)
	}

	_, err = w.Commit("Initial commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test Author",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		cleanup()
		t.Fatalf("failed to commit: %v", err)
	}

	return dir, cleanup
}

// testRepoWithRemote creates a test repo with a remote configured.
func testRepoWithRemote(t *testing.T) (string, func()) {
	t.Helper()

	dir, cleanup := testRepoWithCommit(t)

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		cleanup()
		t.Fatalf("failed to open repo: %v", err)
	}

	_, err = repo.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{"https://github.com/example/repo.git"},
	})
	if err != nil {
		cleanup()
		t.Fatalf("failed to create remote: %v", err)
	}

	return dir, cleanup
}

// testRepoWithBranch creates a test repo with a named branch.
func testRepoWithBranch(t *testing.T, branchName string) (string, func()) {
	t.Helper()

	dir, cleanup := testRepoWithCommit(t)

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		cleanup()
		t.Fatalf("failed to open repo: %v", err)
	}

	headRef, err := repo.Head()
	if err != nil {
		cleanup()
		t.Fatalf("failed to get HEAD: %v", err)
	}

	branchRef := plumbing.NewBranchReferenceName(branchName)
	ref := plumbing.NewHashReference(branchRef, headRef.Hash())
	if err := repo.Storer.SetReference(ref); err != nil {
		cleanup()
		t.Fatalf("failed to create branch: %v", err)
	}

	w, err := repo.Worktree()
	if err != nil {
		cleanup()
		t.Fatalf("failed to get worktree: %v", err)
	}

	if err := w.Checkout(&gogit.CheckoutOptions{Branch: branchRef}); err != nil {
		cleanup()
		t.Fatalf("failed to checkout branch: %v", err)
	}

	return dir, cleanup
}

// =============================================================================
// NewGitClient Tests
// =============================================================================

func TestNewGitClient_ValidRepo(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Errorf("NewGitClient() error = %v, want nil", err)
	}
	if client == nil {
		t.Fatal("NewGitClient() returned nil client")
	}
	defer client.Close()

	if !client.IsGitRepo() {
		t.Error("IsGitRepo() = false, want true")
	}
}

func TestNewGitClient_NonRepoPath(t *testing.T) {
	dir, err := os.MkdirTemp("", "sylk-non-git-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Errorf("NewGitClient() error = %v, want nil (graceful handling)", err)
	}
	if client == nil {
		t.Fatal("NewGitClient() returned nil client")
	}
	defer client.Close()

	if client.IsGitRepo() {
		t.Error("IsGitRepo() = true, want false for non-repo path")
	}
}

func TestNewGitClient_NonExistentPath(t *testing.T) {
	client, err := NewGitClient("/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Errorf("NewGitClient() error = %v, want nil (graceful handling)", err)
	}
	if client == nil {
		t.Fatal("NewGitClient() returned nil client")
	}
	defer client.Close()

	if client.IsGitRepo() {
		t.Error("IsGitRepo() = true, want false for nonexistent path")
	}
}

func TestNewGitClient_EmptyPath(t *testing.T) {
	client, err := NewGitClient("")
	if err == nil {
		defer client.Close()
		t.Error("NewGitClient('') error = nil, want error")
	}
}

// =============================================================================
// IsGitRepo Tests
// =============================================================================

func TestGitClient_IsGitRepo_True(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	if !client.IsGitRepo() {
		t.Error("IsGitRepo() = false, want true")
	}
}

func TestGitClient_IsGitRepo_False(t *testing.T) {
	dir, err := os.MkdirTemp("", "sylk-non-git-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	if client.IsGitRepo() {
		t.Error("IsGitRepo() = true, want false")
	}
}

// =============================================================================
// GetHead Tests
// =============================================================================

func TestGitClient_GetHead_WithCommit(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	hash, err := client.GetHead()
	if err != nil {
		t.Errorf("GetHead() error = %v, want nil", err)
	}
	if len(hash) != 40 {
		t.Errorf("GetHead() hash length = %d, want 40", len(hash))
	}
}

func TestGitClient_GetHead_EmptyRepo(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetHead()
	if err == nil {
		t.Error("GetHead() on empty repo: error = nil, want error")
	}
}

func TestGitClient_GetHead_NotGitRepo(t *testing.T) {
	dir, err := os.MkdirTemp("", "sylk-non-git-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetHead()
	if err == nil {
		t.Error("GetHead() on non-git repo: error = nil, want error")
	}
}

// =============================================================================
// GetBranch Tests
// =============================================================================

func TestGitClient_GetBranch_MainBranch(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	branch, err := client.GetBranch()
	if err != nil {
		t.Errorf("GetBranch() error = %v, want nil", err)
	}
	// go-git defaults to "master" for init
	if branch != "master" && branch != "main" {
		t.Errorf("GetBranch() = %q, want 'master' or 'main'", branch)
	}
}

func TestGitClient_GetBranch_CustomBranch(t *testing.T) {
	dir, cleanup := testRepoWithBranch(t, "feature-branch")
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	branch, err := client.GetBranch()
	if err != nil {
		t.Errorf("GetBranch() error = %v, want nil", err)
	}
	if branch != "feature-branch" {
		t.Errorf("GetBranch() = %q, want 'feature-branch'", branch)
	}
}

func TestGitClient_GetBranch_EmptyRepo(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetBranch()
	if err == nil {
		t.Error("GetBranch() on empty repo: error = nil, want error")
	}
}

func TestGitClient_GetBranch_NotGitRepo(t *testing.T) {
	dir, err := os.MkdirTemp("", "sylk-non-git-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetBranch()
	if err == nil {
		t.Error("GetBranch() on non-git repo: error = nil, want error")
	}
}

// =============================================================================
// GetRemote Tests
// =============================================================================

func TestGitClient_GetRemote_Origin(t *testing.T) {
	dir, cleanup := testRepoWithRemote(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	url, err := client.GetRemote("origin")
	if err != nil {
		t.Errorf("GetRemote('origin') error = %v, want nil", err)
	}
	if url != "https://github.com/example/repo.git" {
		t.Errorf("GetRemote('origin') = %q, want 'https://github.com/example/repo.git'", url)
	}
}

func TestGitClient_GetRemote_DefaultName(t *testing.T) {
	dir, cleanup := testRepoWithRemote(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	// Empty name should default to "origin"
	url, err := client.GetRemote("")
	if err != nil {
		t.Errorf("GetRemote('') error = %v, want nil", err)
	}
	if url != "https://github.com/example/repo.git" {
		t.Errorf("GetRemote('') = %q, want 'https://github.com/example/repo.git'", url)
	}
}

func TestGitClient_GetRemote_NonExistent(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetRemote("nonexistent")
	if err == nil {
		t.Error("GetRemote('nonexistent') error = nil, want error")
	}
}

func TestGitClient_GetRemote_NotGitRepo(t *testing.T) {
	dir, err := os.MkdirTemp("", "sylk-non-git-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetRemote("origin")
	if err == nil {
		t.Error("GetRemote() on non-git repo: error = nil, want error")
	}
}

// =============================================================================
// Repository Tests
// =============================================================================

func TestGitClient_Repository_ValidRepo(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	repo := client.Repository()
	if repo == nil {
		t.Error("Repository() = nil, want non-nil for valid repo")
	}
}

func TestGitClient_Repository_NotGitRepo(t *testing.T) {
	dir, err := os.MkdirTemp("", "sylk-non-git-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	repo := client.Repository()
	if repo != nil {
		t.Error("Repository() = non-nil, want nil for non-git path")
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestGitClient_Close(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}

	// Close should not error
	if err := client.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}

	// Double close should not error
	if err := client.Close(); err != nil {
		t.Errorf("Close() (second call) error = %v, want nil", err)
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestGitClient_ThreadSafety_ConcurrentReads(t *testing.T) {
	dir, cleanup := testRepoWithRemote(t)
	defer cleanup()

	// Create branch for GetBranch test
	repo, _ := gogit.PlainOpen(dir)
	headRef, _ := repo.Head()
	branchRef := plumbing.NewBranchReferenceName("test-branch")
	ref := plumbing.NewHashReference(branchRef, headRef.Hash())
	repo.Storer.SetReference(ref)

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 100)

	// Spawn multiple goroutines doing concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(4)

		go func() {
			defer wg.Done()
			if !client.IsGitRepo() {
				errs <- nil // Expected
			}
		}()

		go func() {
			defer wg.Done()
			_, err := client.GetHead()
			if err != nil {
				errs <- err
			}
		}()

		go func() {
			defer wg.Done()
			_, err := client.GetBranch()
			if err != nil {
				errs <- err
			}
		}()

		go func() {
			defer wg.Done()
			_, err := client.GetRemote("origin")
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent operation error: %v", err)
		}
	}
}

func TestGitClient_ThreadSafety_ConcurrentIsGitRepo(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	results := make([]bool, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			results[idx] = client.IsGitRepo()
		}()
	}

	wg.Wait()

	// All results should be true
	for i, result := range results {
		if !result {
			t.Errorf("IsGitRepo() iteration %d = false, want true", i)
		}
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestGitClient_RepoPath(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	// Use the public RepoPath() method
	absDir, _ := filepath.Abs(dir)
	if client.RepoPath() != absDir {
		t.Errorf("RepoPath() = %q, want %q", client.RepoPath(), absDir)
	}
}

func TestGitClient_SymlinkPath(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	// Create a symlink to the repo
	symlinkDir, err := os.MkdirTemp("", "sylk-symlink-*")
	if err != nil {
		t.Fatalf("failed to create symlink dir: %v", err)
	}
	defer os.RemoveAll(symlinkDir)

	symlinkPath := filepath.Join(symlinkDir, "repo-link")
	if err := os.Symlink(dir, symlinkPath); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	client, err := NewGitClient(symlinkPath)
	if err != nil {
		t.Errorf("NewGitClient() with symlink error = %v, want nil", err)
	}
	if client == nil {
		t.Fatal("NewGitClient() returned nil for symlink")
	}
	defer client.Close()

	if !client.IsGitRepo() {
		t.Error("IsGitRepo() = false for symlink, want true")
	}
}
