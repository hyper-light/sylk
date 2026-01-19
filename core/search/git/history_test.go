// Package git provides git integration for the Sylk Document Search System.
package git

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// Test Fixtures for History
// =============================================================================

// testRepoWithMultipleCommits creates a test repo with multiple commits.
func testRepoWithMultipleCommits(t *testing.T) (string, []string, func()) {
	t.Helper()

	dir, cleanup := testRepo(t)
	var hashes []string

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

	// First commit
	testFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(testFile, []byte("version 1"), 0644); err != nil {
		cleanup()
		t.Fatalf("failed to write test file: %v", err)
	}

	if _, err := w.Add("test.txt"); err != nil {
		cleanup()
		t.Fatalf("failed to add file: %v", err)
	}

	hash1, err := w.Commit("First commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Author One",
			Email: "author1@example.com",
			When:  time.Now().Add(-2 * time.Hour),
		},
	})
	if err != nil {
		cleanup()
		t.Fatalf("failed to commit: %v", err)
	}
	hashes = append(hashes, hash1.String())

	// Second commit
	if err := os.WriteFile(testFile, []byte("version 2"), 0644); err != nil {
		cleanup()
		t.Fatalf("failed to write test file: %v", err)
	}

	if _, err := w.Add("test.txt"); err != nil {
		cleanup()
		t.Fatalf("failed to add file: %v", err)
	}

	hash2, err := w.Commit("Second commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Author Two",
			Email: "author2@example.com",
			When:  time.Now().Add(-1 * time.Hour),
		},
	})
	if err != nil {
		cleanup()
		t.Fatalf("failed to commit: %v", err)
	}
	hashes = append(hashes, hash2.String())

	// Third commit
	if err := os.WriteFile(testFile, []byte("version 3"), 0644); err != nil {
		cleanup()
		t.Fatalf("failed to write test file: %v", err)
	}

	if _, err := w.Add("test.txt"); err != nil {
		cleanup()
		t.Fatalf("failed to add file: %v", err)
	}

	hash3, err := w.Commit("Third commit", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Author Three",
			Email: "author3@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		cleanup()
		t.Fatalf("failed to commit: %v", err)
	}
	hashes = append(hashes, hash3.String())

	return dir, hashes, cleanup
}

// testRepoWithRenamedFile creates a test repo with a renamed file.
func testRepoWithRenamedFile(t *testing.T) (string, func()) {
	t.Helper()

	dir, cleanup := testRepo(t)

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

	// First commit with original file
	oldFile := filepath.Join(dir, "old_name.txt")
	if err := os.WriteFile(oldFile, []byte("content"), 0644); err != nil {
		cleanup()
		t.Fatalf("failed to write file: %v", err)
	}

	if _, err := w.Add("old_name.txt"); err != nil {
		cleanup()
		t.Fatalf("failed to add file: %v", err)
	}

	_, err = w.Commit("Add original file", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test Author",
			Email: "test@example.com",
			When:  time.Now().Add(-1 * time.Hour),
		},
	})
	if err != nil {
		cleanup()
		t.Fatalf("failed to commit: %v", err)
	}

	// Rename the file
	newFile := filepath.Join(dir, "new_name.txt")
	if err := os.Rename(oldFile, newFile); err != nil {
		cleanup()
		t.Fatalf("failed to rename file: %v", err)
	}

	if _, err := w.Remove("old_name.txt"); err != nil {
		cleanup()
		t.Fatalf("failed to remove old file: %v", err)
	}

	if _, err := w.Add("new_name.txt"); err != nil {
		cleanup()
		t.Fatalf("failed to add new file: %v", err)
	}

	_, err = w.Commit("Rename file", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test Author",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		cleanup()
		t.Fatalf("failed to commit rename: %v", err)
	}

	return dir, cleanup
}

// testRepoWithMultipleFiles creates a repo with multiple files.
func testRepoWithMultipleFiles(t *testing.T) (string, func()) {
	t.Helper()

	dir, cleanup := testRepo(t)

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

	// Create multiple files
	files := []string{"file1.txt", "file2.txt", "subdir/file3.txt"}
	for _, f := range files {
		fullPath := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			cleanup()
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte("content of "+f), 0644); err != nil {
			cleanup()
			t.Fatalf("failed to write file: %v", err)
		}
		if _, err := w.Add(f); err != nil {
			cleanup()
			t.Fatalf("failed to add file: %v", err)
		}
	}

	_, err = w.Commit("Add multiple files", &gogit.CommitOptions{
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

// =============================================================================
// GetFileHistory Tests
// =============================================================================

func TestGitClient_GetFileHistory_ReturnsCommits(t *testing.T) {
	dir, hashes, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	history, err := client.GetFileHistory("test.txt", FileHistoryOptions{})
	if err != nil {
		t.Errorf("GetFileHistory() error = %v, want nil", err)
	}

	if len(history) != 3 {
		t.Errorf("GetFileHistory() returned %d commits, want 3", len(history))
	}

	// History should be in reverse chronological order (newest first)
	if history[0].Hash != hashes[2] {
		t.Errorf("GetFileHistory()[0].Hash = %q, want %q", history[0].Hash, hashes[2])
	}
}

func TestGitClient_GetFileHistory_WithLimit(t *testing.T) {
	dir, _, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	history, err := client.GetFileHistory("test.txt", FileHistoryOptions{Limit: 2})
	if err != nil {
		t.Errorf("GetFileHistory() error = %v, want nil", err)
	}

	if len(history) != 2 {
		t.Errorf("GetFileHistory() with limit 2 returned %d commits, want 2", len(history))
	}
}

func TestGitClient_GetFileHistory_CommitInfoFields(t *testing.T) {
	dir, _, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	history, err := client.GetFileHistory("test.txt", FileHistoryOptions{Limit: 1})
	if err != nil {
		t.Fatalf("GetFileHistory() error = %v", err)
	}

	if len(history) == 0 {
		t.Fatal("GetFileHistory() returned empty history")
	}

	commit := history[0]

	// Check all fields are populated
	if len(commit.Hash) != 40 {
		t.Errorf("commit.Hash length = %d, want 40", len(commit.Hash))
	}
	if commit.Author == "" {
		t.Error("commit.Author is empty")
	}
	if commit.AuthorEmail == "" {
		t.Error("commit.AuthorEmail is empty")
	}
	if commit.Date().IsZero() {
		t.Error("commit.Date() is zero")
	}
	if commit.Message == "" {
		t.Error("commit.Message is empty")
	}
}

func TestGitClient_GetFileHistory_FileNotFound(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	// git log for a nonexistent file returns empty, not an error
	// This matches git's behavior
	history, err := client.GetFileHistory("nonexistent.txt", FileHistoryOptions{})
	if err != nil {
		t.Errorf("GetFileHistory() error = %v, want nil", err)
	}
	if len(history) != 0 {
		t.Errorf("GetFileHistory() returned %d commits, want 0", len(history))
	}
}

func TestGitClient_GetFileHistory_NotGitRepo(t *testing.T) {
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

	_, err = client.GetFileHistory("test.txt", FileHistoryOptions{})
	if err == nil {
		t.Error("GetFileHistory() on non-git repo: error = nil, want error")
	}
}

func TestGitClient_GetFileHistory_FollowRenames(t *testing.T) {
	dir, cleanup := testRepoWithRenamedFile(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	// Without follow renames
	historyNoFollow, err := client.GetFileHistory("new_name.txt", FileHistoryOptions{FollowRenames: false})
	if err != nil {
		t.Errorf("GetFileHistory() without follow error = %v", err)
	}

	// With follow renames (should include history from before rename)
	historyFollow, err := client.GetFileHistory("new_name.txt", FileHistoryOptions{FollowRenames: true})
	if err != nil {
		t.Errorf("GetFileHistory() with follow error = %v", err)
	}

	// With follow should have at least as many commits
	if len(historyFollow) < len(historyNoFollow) {
		t.Errorf("GetFileHistory() with follow has %d commits, without has %d (expected >= without)",
			len(historyFollow), len(historyNoFollow))
	}
}

func TestGitClient_GetFileHistory_TimeBounds(t *testing.T) {
	dir, _, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	// Get history within time bounds
	since := time.Now().Add(-90 * time.Minute)
	until := time.Now().Add(-30 * time.Minute)

	history, err := client.GetFileHistory("test.txt", FileHistoryOptions{
		Since: since,
		Until: until,
	})
	if err != nil {
		t.Errorf("GetFileHistory() with time bounds error = %v", err)
	}

	// Verify all commits are within bounds
	for _, commit := range history {
		if commit.Date().Before(since) {
			t.Errorf("commit date %v is before since %v", commit.Date(), since)
		}
		if commit.Date().After(until) {
			t.Errorf("commit date %v is after until %v", commit.Date(), until)
		}
	}
}

// =============================================================================
// GetFileAtCommit Tests
// =============================================================================

func TestGitClient_GetFileAtCommit_ReturnsContent(t *testing.T) {
	dir, hashes, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	// Get content at first commit
	content, err := client.GetFileAtCommit("test.txt", hashes[0])
	if err != nil {
		t.Errorf("GetFileAtCommit() error = %v, want nil", err)
	}

	if string(content) != "version 1" {
		t.Errorf("GetFileAtCommit() content = %q, want %q", string(content), "version 1")
	}
}

func TestGitClient_GetFileAtCommit_DifferentVersions(t *testing.T) {
	dir, hashes, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	expectedContents := []string{"version 1", "version 2", "version 3"}

	for i, hash := range hashes {
		content, err := client.GetFileAtCommit("test.txt", hash)
		if err != nil {
			t.Errorf("GetFileAtCommit(%q) error = %v", hash, err)
			continue
		}

		if string(content) != expectedContents[i] {
			t.Errorf("GetFileAtCommit(%q) content = %q, want %q", hash, string(content), expectedContents[i])
		}
	}
}

func TestGitClient_GetFileAtCommit_InvalidCommit(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetFileAtCommit("test.txt", "0000000000000000000000000000000000000000")
	if err == nil {
		t.Error("GetFileAtCommit() with invalid commit: error = nil, want error")
	}
}

func TestGitClient_GetFileAtCommit_FileNotInCommit(t *testing.T) {
	dir, hashes, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetFileAtCommit("nonexistent.txt", hashes[0])
	if err == nil {
		t.Error("GetFileAtCommit() for nonexistent file: error = nil, want error")
	}
}

func TestGitClient_GetFileAtCommit_NotGitRepo(t *testing.T) {
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

	_, err = client.GetFileAtCommit("test.txt", "abc123")
	if err == nil {
		t.Error("GetFileAtCommit() on non-git repo: error = nil, want error")
	}
}

// =============================================================================
// GetCommit Tests
// =============================================================================

func TestGitClient_GetCommit_ReturnsInfo(t *testing.T) {
	dir, hashes, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	commit, err := client.GetCommit(hashes[0])
	if err != nil {
		t.Errorf("GetCommit() error = %v, want nil", err)
	}

	if commit.Hash != hashes[0] {
		t.Errorf("GetCommit().Hash = %q, want %q", commit.Hash, hashes[0])
	}
	if commit.Author != "Author One" {
		t.Errorf("GetCommit().Author = %q, want %q", commit.Author, "Author One")
	}
	if commit.AuthorEmail != "author1@example.com" {
		t.Errorf("GetCommit().AuthorEmail = %q, want %q", commit.AuthorEmail, "author1@example.com")
	}
	if commit.Message != "First commit" {
		t.Errorf("GetCommit().Message = %q, want %q", commit.Message, "First commit")
	}
}

func TestGitClient_GetCommit_FilesChanged(t *testing.T) {
	dir, hashes, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	commit, err := client.GetCommit(hashes[1])
	if err != nil {
		t.Errorf("GetCommit() error = %v, want nil", err)
	}

	if len(commit.FilesChanged) == 0 {
		t.Error("GetCommit().FilesChanged is empty, expected at least one file")
	}

	found := false
	for _, f := range commit.FilesChanged {
		if f == "test.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GetCommit().FilesChanged = %v, expected to contain 'test.txt'", commit.FilesChanged)
	}
}

func TestGitClient_GetCommit_InvalidHash(t *testing.T) {
	dir, cleanup := testRepoWithCommit(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.GetCommit("0000000000000000000000000000000000000000")
	if err == nil {
		t.Error("GetCommit() with invalid hash: error = nil, want error")
	}
}

func TestGitClient_GetCommit_NotGitRepo(t *testing.T) {
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

	_, err = client.GetCommit("abc123")
	if err == nil {
		t.Error("GetCommit() on non-git repo: error = nil, want error")
	}
}

// =============================================================================
// ListTrackedFiles Tests
// =============================================================================

func TestGitClient_ListTrackedFiles_ReturnsFiles(t *testing.T) {
	dir, cleanup := testRepoWithMultipleFiles(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	files, err := client.ListTrackedFiles()
	if err != nil {
		t.Errorf("ListTrackedFiles() error = %v, want nil", err)
	}

	if len(files) != 3 {
		t.Errorf("ListTrackedFiles() returned %d files, want 3", len(files))
	}

	expected := map[string]bool{
		"file1.txt":        true,
		"file2.txt":        true,
		"subdir/file3.txt": true,
	}

	for _, f := range files {
		if !expected[f] {
			t.Errorf("ListTrackedFiles() unexpected file: %q", f)
		}
	}
}

func TestGitClient_ListTrackedFiles_EmptyRepo(t *testing.T) {
	dir, cleanup := testRepo(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	files, err := client.ListTrackedFiles()
	if err != nil {
		t.Errorf("ListTrackedFiles() on empty repo error = %v, want nil", err)
	}

	if len(files) != 0 {
		t.Errorf("ListTrackedFiles() on empty repo returned %d files, want 0", len(files))
	}
}

func TestGitClient_ListTrackedFiles_NotGitRepo(t *testing.T) {
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

	_, err = client.ListTrackedFiles()
	if err == nil {
		t.Error("ListTrackedFiles() on non-git repo: error = nil, want error")
	}
}

// =============================================================================
// Thread Safety Tests for History Methods
// =============================================================================

func TestGitClient_History_ThreadSafety(t *testing.T) {
	dir, hashes, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 100)

	for i := 0; i < 10; i++ {
		wg.Add(4)

		go func() {
			defer wg.Done()
			_, err := client.GetFileHistory("test.txt", FileHistoryOptions{Limit: 1})
			if err != nil {
				errs <- err
			}
		}()

		go func() {
			defer wg.Done()
			_, err := client.GetFileAtCommit("test.txt", hashes[0])
			if err != nil {
				errs <- err
			}
		}()

		go func() {
			defer wg.Done()
			_, err := client.GetCommit(hashes[0])
			if err != nil {
				errs <- err
			}
		}()

		go func() {
			defer wg.Done()
			_, err := client.ListTrackedFiles()
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("concurrent history operation error: %v", err)
		}
	}
}

// =============================================================================
// FileHistoryOptions Tests
// =============================================================================

func TestFileHistoryOptions_Defaults(t *testing.T) {
	opts := FileHistoryOptions{}

	if opts.Limit != 0 {
		t.Errorf("default Limit = %d, want 0 (unlimited)", opts.Limit)
	}
	if opts.FollowRenames {
		t.Error("default FollowRenames = true, want false")
	}
	if !opts.Since.IsZero() {
		t.Error("default Since is not zero")
	}
	if !opts.Until.IsZero() {
		t.Error("default Until is not zero")
	}
}

// =============================================================================
// CommitInfo Tests
// =============================================================================

func TestCommitInfo_Fields(t *testing.T) {
	now := time.Now()
	info := &CommitInfo{
		Hash:         "abc123",
		Author:       "Test Author",
		AuthorEmail:  "test@example.com",
		AuthorTime:   now,
		Message:      "Test message",
		FilesChanged: []string{"file1.txt", "file2.txt"},
	}

	if info.Hash != "abc123" {
		t.Errorf("Hash = %q, want %q", info.Hash, "abc123")
	}
	if info.Author != "Test Author" {
		t.Errorf("Author = %q, want %q", info.Author, "Test Author")
	}
	if info.AuthorEmail != "test@example.com" {
		t.Errorf("AuthorEmail = %q, want %q", info.AuthorEmail, "test@example.com")
	}
	if info.Message != "Test message" {
		t.Errorf("Message = %q, want %q", info.Message, "Test message")
	}
	if len(info.FilesChanged) != 2 {
		t.Errorf("len(FilesChanged) = %d, want 2", len(info.FilesChanged))
	}
	if info.Date() != now {
		t.Errorf("Date() = %v, want %v", info.Date(), now)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestGitClient_GetFileHistory_SubdirectoryFile(t *testing.T) {
	dir, cleanup := testRepoWithMultipleFiles(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	history, err := client.GetFileHistory("subdir/file3.txt", FileHistoryOptions{})
	if err != nil {
		t.Errorf("GetFileHistory() for subdirectory file error = %v, want nil", err)
	}

	if len(history) == 0 {
		t.Error("GetFileHistory() for subdirectory file returned empty history")
	}
}

func TestGitClient_GetFileAtCommit_ShortHash(t *testing.T) {
	dir, hashes, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	// Try with short hash (7 chars)
	shortHash := hashes[0][:7]
	content, err := client.GetFileAtCommit("test.txt", shortHash)
	if err != nil {
		t.Errorf("GetFileAtCommit() with short hash error = %v, want nil", err)
	}

	if string(content) != "version 1" {
		t.Errorf("GetFileAtCommit() content = %q, want %q", string(content), "version 1")
	}
}

func TestGitClient_GetFileHistory_ZeroLimit(t *testing.T) {
	dir, _, cleanup := testRepoWithMultipleCommits(t)
	defer cleanup()

	client, err := NewGitClient(dir)
	if err != nil {
		t.Fatalf("NewGitClient() error = %v", err)
	}
	defer client.Close()

	// Limit of 0 should mean unlimited
	history, err := client.GetFileHistory("test.txt", FileHistoryOptions{Limit: 0})
	if err != nil {
		t.Errorf("GetFileHistory() with limit 0 error = %v, want nil", err)
	}

	if len(history) != 3 {
		t.Errorf("GetFileHistory() with limit 0 returned %d commits, want 3", len(history))
	}
}
