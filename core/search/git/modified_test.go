package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

// setupModifiedTestRepo creates a temporary git repository for testing.
func setupModifiedTestRepo(t *testing.T) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "git-modified-test-*")
	require.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	// Initialize git repo
	runGitCmd(t, tmpDir, "init")
	runGitCmd(t, tmpDir, "config", "user.email", "test@example.com")
	runGitCmd(t, tmpDir, "config", "user.name", "Test User")

	return tmpDir, cleanup
}

// runGitCmd executes a git command in the given directory.
func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))

	return strings.TrimSpace(string(out))
}

// createAndCommit creates a file with content and commits it.
func createAndCommit(t *testing.T, dir, filename, content, message string) string {
	t.Helper()

	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	runGitCmd(t, dir, "add", filename)
	runGitCmd(t, dir, "commit", "-m", message)

	return runGitCmd(t, dir, "rev-parse", "HEAD")
}

// modifyFile modifies an existing file's content.
func modifyFile(t *testing.T, dir, filename, content string) {
	t.Helper()

	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
}

// commitChanges stages and commits all changes.
func commitChanges(t *testing.T, dir, message string) string {
	t.Helper()

	runGitCmd(t, dir, "add", "-A")
	runGitCmd(t, dir, "commit", "-m", message)

	return runGitCmd(t, dir, "rev-parse", "HEAD")
}

// createSubdirectory creates a subdirectory in the repo.
func createSubdirectory(t *testing.T, dir, subdir string) {
	t.Helper()

	path := filepath.Join(dir, subdir)
	err := os.MkdirAll(path, 0755)
	require.NoError(t, err)
}

// =============================================================================
// ListModifiedFiles Tests
// =============================================================================

func TestListModifiedFiles_ReturnsChangedFiles(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create initial commit
	initialCommit := createAndCommit(t, repoDir, "file1.txt", "content1", "Initial")

	// Create second commit
	createAndCommit(t, repoDir, "file2.txt", "content2", "Add file2")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Use commit-based filtering instead of time-based
	files, err := client.ListModifiedFilesSinceCommit(initialCommit)
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "file2.txt")
}

func TestListModifiedFiles_MultipleFiles(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create initial commit
	initialCommit := createAndCommit(t, repoDir, "initial.txt", "initial", "Initial")

	// Create multiple files and commit
	modifyFile(t, repoDir, "file1.txt", "content1")
	modifyFile(t, repoDir, "file2.txt", "content2")
	modifyFile(t, repoDir, "file3.txt", "content3")
	commitChanges(t, repoDir, "Add multiple files")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Use commit-based filtering
	files, err := client.ListModifiedFilesSinceCommit(initialCommit)
	require.NoError(t, err)

	assert.Len(t, files, 3)
	assert.Contains(t, files, "file1.txt")
	assert.Contains(t, files, "file2.txt")
	assert.Contains(t, files, "file3.txt")
}

func TestListModifiedFiles_NoChanges(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create initial commit
	headCommit := createAndCommit(t, repoDir, "file1.txt", "content1", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// No changes since HEAD - should be empty
	files, err := client.ListModifiedFilesSinceCommit(headCommit)
	require.NoError(t, err)

	assert.Empty(t, files)
}

func TestListModifiedFiles_IncludesModificationsAndAdditions(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create initial commit with a file
	initialCommit := createAndCommit(t, repoDir, "existing.txt", "original content", "Initial")

	// Modify existing file and add new file
	modifyFile(t, repoDir, "existing.txt", "modified content")
	modifyFile(t, repoDir, "new.txt", "new content")
	commitChanges(t, repoDir, "Modify and add")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.ListModifiedFilesSinceCommit(initialCommit)
	require.NoError(t, err)

	assert.Len(t, files, 2)
	assert.Contains(t, files, "existing.txt")
	assert.Contains(t, files, "new.txt")
}

func TestListModifiedFiles_IncludesDeletions(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create initial commit with files
	createAndCommit(t, repoDir, "keep.txt", "keep", "Initial")
	modifyFile(t, repoDir, "delete.txt", "to delete")
	beforeDelete := commitChanges(t, repoDir, "Add delete.txt")

	// Delete the file
	os.Remove(filepath.Join(repoDir, "delete.txt"))
	commitChanges(t, repoDir, "Delete file")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.ListModifiedFilesSinceCommit(beforeDelete)
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "delete.txt")
}

func TestListModifiedFiles_FilesInSubdirectories(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	initialCommit := createAndCommit(t, repoDir, "root.txt", "root", "Initial")

	// Create files in subdirectories
	createSubdirectory(t, repoDir, "src/pkg")
	modifyFile(t, repoDir, "src/main.go", "package main")
	modifyFile(t, repoDir, "src/pkg/util.go", "package pkg")
	commitChanges(t, repoDir, "Add subdirectory files")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.ListModifiedFilesSinceCommit(initialCommit)
	require.NoError(t, err)

	assert.Len(t, files, 2)
	assert.Contains(t, files, "src/main.go")
	assert.Contains(t, files, "src/pkg/util.go")
}

func TestListModifiedFiles_EmptyRepo(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.ListModifiedFiles(time.Now().Add(-time.Hour))
	require.NoError(t, err)

	assert.Empty(t, files)
}

// =============================================================================
// ListModifiedFilesSinceCommit Tests
// =============================================================================

func TestListModifiedFilesSinceCommit_Works(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create initial commit
	firstCommit := createAndCommit(t, repoDir, "file1.txt", "content1", "First commit")

	// Create subsequent commits
	createAndCommit(t, repoDir, "file2.txt", "content2", "Second commit")
	modifyFile(t, repoDir, "file3.txt", "content3")
	commitChanges(t, repoDir, "Third commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.ListModifiedFilesSinceCommit(firstCommit)
	require.NoError(t, err)

	assert.Len(t, files, 2)
	assert.Contains(t, files, "file2.txt")
	assert.Contains(t, files, "file3.txt")
}

func TestListModifiedFilesSinceCommit_HeadCommit(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create commits
	createAndCommit(t, repoDir, "file1.txt", "content1", "First commit")
	headCommit := createAndCommit(t, repoDir, "file2.txt", "content2", "Second commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.ListModifiedFilesSinceCommit(headCommit)
	require.NoError(t, err)

	assert.Empty(t, files)
}

func TestListModifiedFilesSinceCommit_InvalidHash(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file1.txt", "content1", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.ListModifiedFilesSinceCommit("invalid-hash")
	assert.Error(t, err)
}

func TestListModifiedFilesSinceCommit_ShortHash(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	firstCommit := createAndCommit(t, repoDir, "file1.txt", "content1", "First")
	createAndCommit(t, repoDir, "file2.txt", "content2", "Second")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Use short hash (7 chars)
	shortHash := firstCommit[:7]
	files, err := client.ListModifiedFilesSinceCommit(shortHash)
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "file2.txt")
}

// =============================================================================
// GetCommitsSince Tests
// =============================================================================

func TestGetCommitsSince_FiltersCorrectly(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create first commit
	createAndCommit(t, repoDir, "file1.txt", "content1", "First commit")

	// Wait for at least 1 second since git uses second-granular timestamps
	time.Sleep(1100 * time.Millisecond)
	sinceTime := time.Now()

	// Create more commits after the time
	createAndCommit(t, repoDir, "file2.txt", "content2", "Second commit")
	createAndCommit(t, repoDir, "file3.txt", "content3", "Third commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	commits, err := client.GetCommitsSince(sinceTime)
	require.NoError(t, err)

	assert.Len(t, commits, 2)
	assert.Equal(t, "Third commit", commits[0].Subject)
	assert.Equal(t, "Second commit", commits[1].Subject)
}

func TestGetCommitsSince_NoCommits(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file1.txt", "content1", "First commit")

	// Wait for at least 1 second since git uses second-granular timestamps
	time.Sleep(1100 * time.Millisecond)
	sinceTime := time.Now()

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	commits, err := client.GetCommitsSince(sinceTime)
	require.NoError(t, err)

	assert.Empty(t, commits)
}

func TestGetCommitsSince_AllCommits(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Use a time in the past
	sinceTime := time.Now().Add(-time.Hour)

	createAndCommit(t, repoDir, "file1.txt", "content1", "First")
	createAndCommit(t, repoDir, "file2.txt", "content2", "Second")
	createAndCommit(t, repoDir, "file3.txt", "content3", "Third")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	commits, err := client.GetCommitsSince(sinceTime)
	require.NoError(t, err)

	assert.Len(t, commits, 3)
}

func TestGetCommitsSince_ContainsCommitInfo(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	sinceTime := time.Now().Add(-time.Hour)

	createAndCommit(t, repoDir, "file.txt", "content", "Test commit message")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	commits, err := client.GetCommitsSince(sinceTime)
	require.NoError(t, err)
	require.Len(t, commits, 1)

	commit := commits[0]
	assert.NotEmpty(t, commit.Hash)
	assert.Len(t, commit.Hash, 40)
	assert.Equal(t, commit.Hash[:7], commit.ShortHash)
	assert.Equal(t, "Test User", commit.Author)
	assert.Equal(t, "test@example.com", commit.AuthorEmail)
	assert.Equal(t, "Test commit message", commit.Subject)
	assert.False(t, commit.AuthorTime.IsZero())
}

// =============================================================================
// GetFilesInCommit Tests
// =============================================================================

func TestGetFilesInCommit_ReturnsFileList(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create commit with multiple files
	modifyFile(t, repoDir, "file1.txt", "content1")
	modifyFile(t, repoDir, "file2.txt", "content2")
	modifyFile(t, repoDir, "file3.txt", "content3")
	commitHash := commitChanges(t, repoDir, "Add files")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetFilesInCommit(commitHash)
	require.NoError(t, err)

	assert.Len(t, files, 3)
	assert.Contains(t, files, "file1.txt")
	assert.Contains(t, files, "file2.txt")
	assert.Contains(t, files, "file3.txt")
}

func TestGetFilesInCommit_SingleFile(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	commitHash := createAndCommit(t, repoDir, "single.txt", "content", "Single file commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetFilesInCommit(commitHash)
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "single.txt")
}

func TestGetFilesInCommit_InvalidHash(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file.txt", "content", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetFilesInCommit("0000000000000000000000000000000000000000")
	assert.Error(t, err)
}

func TestGetFilesInCommit_ShortHash(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	commitHash := createAndCommit(t, repoDir, "file.txt", "content", "Commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetFilesInCommit(commitHash[:7])
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "file.txt")
}

func TestGetFilesInCommit_FilesInSubdirectories(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createSubdirectory(t, repoDir, "src/pkg")
	modifyFile(t, repoDir, "src/main.go", "package main")
	modifyFile(t, repoDir, "src/pkg/util.go", "package pkg")
	commitHash := commitChanges(t, repoDir, "Add pkg files")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetFilesInCommit(commitHash)
	require.NoError(t, err)

	assert.Len(t, files, 2)
	assert.Contains(t, files, "src/main.go")
	assert.Contains(t, files, "src/pkg/util.go")
}

// =============================================================================
// GetAllCommits Tests
// =============================================================================

func TestGetAllCommits_ReturnsAllWithLimit(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file1.txt", "content1", "First")
	createAndCommit(t, repoDir, "file2.txt", "content2", "Second")
	createAndCommit(t, repoDir, "file3.txt", "content3", "Third")
	createAndCommit(t, repoDir, "file4.txt", "content4", "Fourth")
	createAndCommit(t, repoDir, "file5.txt", "content5", "Fifth")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Get only 3 commits
	commits, err := client.GetAllCommits(3)
	require.NoError(t, err)

	assert.Len(t, commits, 3)
	assert.Equal(t, "Fifth", commits[0].Subject)
	assert.Equal(t, "Fourth", commits[1].Subject)
	assert.Equal(t, "Third", commits[2].Subject)
}

func TestGetAllCommits_ZeroLimit(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file1.txt", "content1", "First")
	createAndCommit(t, repoDir, "file2.txt", "content2", "Second")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Zero limit should return all
	commits, err := client.GetAllCommits(0)
	require.NoError(t, err)

	assert.Len(t, commits, 2)
}

func TestGetAllCommits_NegativeLimit(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file1.txt", "content1", "First")
	createAndCommit(t, repoDir, "file2.txt", "content2", "Second")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Negative limit should return all
	commits, err := client.GetAllCommits(-1)
	require.NoError(t, err)

	assert.Len(t, commits, 2)
}

func TestGetAllCommits_EmptyRepo(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	commits, err := client.GetAllCommits(10)
	require.NoError(t, err)

	assert.Empty(t, commits)
}

// =============================================================================
// GetUncommittedFiles Tests
// =============================================================================

func TestGetUncommittedFiles_DetectsChanges(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create initial commit
	createAndCommit(t, repoDir, "committed.txt", "original", "Initial")

	// Make uncommitted changes
	modifyFile(t, repoDir, "committed.txt", "modified")
	modifyFile(t, repoDir, "new.txt", "new file")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetUncommittedFiles()
	require.NoError(t, err)

	assert.Len(t, files, 2)
	assert.Contains(t, files, "committed.txt")
	assert.Contains(t, files, "new.txt")
}

func TestGetUncommittedFiles_NoChanges(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file.txt", "content", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetUncommittedFiles()
	require.NoError(t, err)

	assert.Empty(t, files)
}

func TestGetUncommittedFiles_StagedChanges(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file.txt", "original", "Initial")

	// Make changes and stage them
	modifyFile(t, repoDir, "file.txt", "modified")
	runGitCmd(t, repoDir, "add", "file.txt")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetUncommittedFiles()
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "file.txt")
}

func TestGetUncommittedFiles_DeletedFiles(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "to-delete.txt", "content", "Initial")

	// Delete the file (but don't commit)
	os.Remove(filepath.Join(repoDir, "to-delete.txt"))

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetUncommittedFiles()
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "to-delete.txt")
}

// =============================================================================
// GetUntrackedFiles Tests
// =============================================================================

func TestGetUntrackedFiles_DetectsUntracked(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	// Create initial commit
	createAndCommit(t, repoDir, "tracked.txt", "tracked", "Initial")

	// Create untracked files
	modifyFile(t, repoDir, "untracked1.txt", "untracked")
	modifyFile(t, repoDir, "untracked2.txt", "untracked")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetUntrackedFiles()
	require.NoError(t, err)

	assert.Len(t, files, 2)
	assert.Contains(t, files, "untracked1.txt")
	assert.Contains(t, files, "untracked2.txt")
}

func TestGetUntrackedFiles_NoUntracked(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file.txt", "content", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetUntrackedFiles()
	require.NoError(t, err)

	assert.Empty(t, files)
}

func TestGetUntrackedFiles_UntrackedInSubdirectory(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "root.txt", "root", "Initial")

	// Create untracked files in subdirectory
	createSubdirectory(t, repoDir, "subdir")
	modifyFile(t, repoDir, "subdir/untracked.txt", "untracked")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetUntrackedFiles()
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "subdir/untracked.txt")
}

func TestGetUntrackedFiles_IgnoredFilesExcluded(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "tracked.txt", "tracked", "Initial")

	// Create .gitignore
	modifyFile(t, repoDir, ".gitignore", "*.ignored\n")
	runGitCmd(t, repoDir, "add", ".gitignore")
	runGitCmd(t, repoDir, "commit", "-m", "Add gitignore")

	// Create ignored and non-ignored files
	modifyFile(t, repoDir, "should.ignored", "ignored")
	modifyFile(t, repoDir, "untracked.txt", "untracked")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	files, err := client.GetUntrackedFiles()
	require.NoError(t, err)

	assert.Len(t, files, 1)
	assert.Contains(t, files, "untracked.txt")
	assert.NotContains(t, files, "should.ignored")
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestListModifiedFiles_ConcurrentAccess(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	sinceTime := time.Now().Add(-time.Hour)
	createAndCommit(t, repoDir, "file.txt", "content", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.ListModifiedFiles(sinceTime)
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent ListModifiedFiles error: %v", err)
	}
}

func TestGetCommitsSince_ConcurrentAccess(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	sinceTime := time.Now().Add(-time.Hour)
	createAndCommit(t, repoDir, "file.txt", "content", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.GetCommitsSince(sinceTime)
			if err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent GetCommitsSince error: %v", err)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestListModifiedFiles_NotGitRepo(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	client, err := NewGitClient(tmpDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.ListModifiedFiles(time.Now())
	assert.Error(t, err)
}

func TestGetFilesInCommit_EmptyCommitHash(t *testing.T) {
	repoDir, cleanup := setupModifiedTestRepo(t)
	defer cleanup()

	createAndCommit(t, repoDir, "file.txt", "content", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetFilesInCommit("")
	assert.Error(t, err)
}
