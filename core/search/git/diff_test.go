package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupDiffTestRepo creates a temporary git repository for diff testing.
func setupDiffTestRepo(t *testing.T) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "git-diff-test-*")
	require.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	// Initialize git repo
	runDiffGit(t, tmpDir, "init")
	runDiffGit(t, tmpDir, "config", "user.email", "test@example.com")
	runDiffGit(t, tmpDir, "config", "user.name", "Test User")

	return tmpDir, cleanup
}

// runDiffGit executes a git command in the given directory.
func runDiffGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))

	return string(out)
}

// createDiffFileAndCommit creates a file with content and commits it.
func createDiffFileAndCommit(t *testing.T, dir, filename, content, message string) string {
	t.Helper()

	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	runDiffGit(t, dir, "add", filename)
	runDiffGit(t, dir, "commit", "-m", message)

	// Return the commit hash
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)

	return string(out[:40])
}

func TestGetDiff_BetweenCommits(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create first commit
	hash1 := createDiffFileAndCommit(t, repoDir, "test.txt", "line 1\n", "First commit")

	// Create second commit with changes
	path := filepath.Join(repoDir, "test.txt")
	err := os.WriteFile(path, []byte("line 1\nline 2\n"), 0644)
	require.NoError(t, err)
	runDiffGit(t, repoDir, "add", "test.txt")
	runDiffGit(t, repoDir, "commit", "-m", "Second commit")
	hash2 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetDiff(hash1, hash2)
	require.NoError(t, err)

	assert.Len(t, diffs, 1)
	assert.Equal(t, "test.txt", diffs[0].Path)
	assert.Equal(t, "M", diffs[0].Status)
	assert.False(t, diffs[0].Binary)
}

func TestGetDiff_WithAddedFile(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create first commit
	hash1 := createDiffFileAndCommit(t, repoDir, "file1.txt", "content 1\n", "First commit")

	// Create second commit adding a new file
	hash2 := createDiffFileAndCommit(t, repoDir, "file2.txt", "content 2\n", "Second commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetDiff(hash1, hash2)
	require.NoError(t, err)

	assert.Len(t, diffs, 1)
	assert.Equal(t, "file2.txt", diffs[0].Path)
	assert.Equal(t, "A", diffs[0].Status)
}

func TestGetDiff_WithDeletedFile(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create first commit with a file
	hash1 := createDiffFileAndCommit(t, repoDir, "test.txt", "content\n", "First commit")

	// Delete the file and commit
	runDiffGit(t, repoDir, "rm", "test.txt")
	runDiffGit(t, repoDir, "commit", "-m", "Delete file")
	hash2 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetDiff(hash1, hash2)
	require.NoError(t, err)

	assert.Len(t, diffs, 1)
	assert.Equal(t, "test.txt", diffs[0].Path)
	assert.Equal(t, "D", diffs[0].Status)
}

func TestGetDiff_WithRenamedFile(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create first commit
	hash1 := createDiffFileAndCommit(t, repoDir, "old_name.txt", "content\n", "First commit")

	// Rename the file
	runDiffGit(t, repoDir, "mv", "old_name.txt", "new_name.txt")
	runDiffGit(t, repoDir, "commit", "-m", "Rename file")
	hash2 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetDiff(hash1, hash2)
	require.NoError(t, err)

	assert.Len(t, diffs, 1)
	assert.Equal(t, "new_name.txt", diffs[0].Path)
	assert.Equal(t, "old_name.txt", diffs[0].OldPath)
	assert.Equal(t, "R", diffs[0].Status)
}

func TestGetDiff_MultipleFiles(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create first commit with multiple files
	createDiffFileAndCommit(t, repoDir, "file1.txt", "content 1\n", "First commit")
	hash1 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	// Modify both files
	err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("modified 1\n"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("new file\n"), 0644)
	require.NoError(t, err)

	runDiffGit(t, repoDir, "add", ".")
	runDiffGit(t, repoDir, "commit", "-m", "Multiple changes")
	hash2 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetDiff(hash1, hash2)
	require.NoError(t, err)

	assert.Len(t, diffs, 2)

	// Find each file in the results
	var file1, file2 *FileDiff
	for i := range diffs {
		if diffs[i].Path == "file1.txt" {
			file1 = &diffs[i]
		} else if diffs[i].Path == "file2.txt" {
			file2 = &diffs[i]
		}
	}

	require.NotNil(t, file1)
	require.NotNil(t, file2)

	assert.Equal(t, "M", file1.Status)
	assert.Equal(t, "A", file2.Status)
}

func TestGetDiff_EmptyDiff(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create a commit
	hash := createDiffFileAndCommit(t, repoDir, "test.txt", "content\n", "First commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Diff a commit against itself should be empty
	diffs, err := client.GetDiff(hash, hash)
	require.NoError(t, err)

	assert.Empty(t, diffs)
}

func TestGetDiff_InvalidCommit(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	hash := createDiffFileAndCommit(t, repoDir, "test.txt", "content\n", "First commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetDiff(hash, "0000000000000000000000000000000000000000")
	assert.Error(t, err)
}

func TestGetWorkingTreeDiff_WithUncommittedChanges(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create initial commit
	createDiffFileAndCommit(t, repoDir, "test.txt", "line 1\n", "Initial commit")

	// Modify the file without staging
	err := os.WriteFile(filepath.Join(repoDir, "test.txt"), []byte("line 1\nline 2\n"), 0644)
	require.NoError(t, err)

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetWorkingTreeDiff()
	require.NoError(t, err)

	assert.Len(t, diffs, 1)
	assert.Equal(t, "test.txt", diffs[0].Path)
	assert.Equal(t, "M", diffs[0].Status)
}

func TestGetWorkingTreeDiff_NoChanges(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create a commit with clean working tree
	createDiffFileAndCommit(t, repoDir, "test.txt", "content\n", "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetWorkingTreeDiff()
	require.NoError(t, err)

	assert.Empty(t, diffs)
}

func TestGetStagedDiff_WithStagedChanges(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create initial commit
	createDiffFileAndCommit(t, repoDir, "test.txt", "line 1\n", "Initial commit")

	// Modify and stage the file
	err := os.WriteFile(filepath.Join(repoDir, "test.txt"), []byte("line 1\nline 2\n"), 0644)
	require.NoError(t, err)
	runDiffGit(t, repoDir, "add", "test.txt")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetStagedDiff()
	require.NoError(t, err)

	assert.Len(t, diffs, 1)
	assert.Equal(t, "test.txt", diffs[0].Path)
	assert.Equal(t, "M", diffs[0].Status)
}

func TestGetStagedDiff_NoStagedChanges(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create a commit
	createDiffFileAndCommit(t, repoDir, "test.txt", "content\n", "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetStagedDiff()
	require.NoError(t, err)

	assert.Empty(t, diffs)
}

func TestGetStagedDiff_NewStagedFile(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create initial commit
	createDiffFileAndCommit(t, repoDir, "file1.txt", "content\n", "Initial commit")

	// Create and stage a new file
	err := os.WriteFile(filepath.Join(repoDir, "new_file.txt"), []byte("new content\n"), 0644)
	require.NoError(t, err)
	runDiffGit(t, repoDir, "add", "new_file.txt")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetStagedDiff()
	require.NoError(t, err)

	assert.Len(t, diffs, 1)
	assert.Equal(t, "new_file.txt", diffs[0].Path)
	assert.Equal(t, "A", diffs[0].Status)
}

func TestGetFileDiff_ForSpecificFile(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create first commit with multiple files
	createDiffFileAndCommit(t, repoDir, "file1.txt", "content 1\n", "First commit")
	hash1 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	// Modify and add files
	err := os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("modified 1\n"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("new file\n"), 0644)
	require.NoError(t, err)

	runDiffGit(t, repoDir, "add", ".")
	runDiffGit(t, repoDir, "commit", "-m", "Multiple changes")
	hash2 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Get diff for only file1.txt
	diff, err := client.GetFileDiff("file1.txt", hash1, hash2)
	require.NoError(t, err)

	assert.Equal(t, "file1.txt", diff.Path)
	assert.Equal(t, "M", diff.Status)
}

func TestGetFileDiff_FileNotChanged(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create commit with two files
	createDiffFileAndCommit(t, repoDir, "file1.txt", "content 1\n", "Add file1")
	hash1 := createDiffFileAndCommit(t, repoDir, "file2.txt", "content 2\n", "Add file2")

	// Modify only file2
	err := os.WriteFile(filepath.Join(repoDir, "file2.txt"), []byte("modified\n"), 0644)
	require.NoError(t, err)
	runDiffGit(t, repoDir, "add", ".")
	runDiffGit(t, repoDir, "commit", "-m", "Modify file2")
	hash2 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Get diff for file1.txt (which wasn't changed)
	diff, err := client.GetFileDiff("file1.txt", hash1, hash2)
	require.NoError(t, err)

	assert.Nil(t, diff)
}

func TestGetDiff_NonGitDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	client, err := NewGitClient(tmpDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetDiff("abc123", "def456")
	assert.ErrorIs(t, err, ErrNotGitRepo)
}

func TestGetWorkingTreeDiff_NonGitDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	client, err := NewGitClient(tmpDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetWorkingTreeDiff()
	assert.ErrorIs(t, err, ErrNotGitRepo)
}

func TestGetStagedDiff_NonGitDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	client, err := NewGitClient(tmpDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetStagedDiff()
	assert.ErrorIs(t, err, ErrNotGitRepo)
}

func TestGetDiff_WithHunks(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create first commit
	content := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	hash1 := createDiffFileAndCommit(t, repoDir, "test.txt", content, "First commit")

	// Modify the file
	newContent := "line 1\nmodified line 2\nline 3\nline 4\nline 5\nadded line 6\n"
	err := os.WriteFile(filepath.Join(repoDir, "test.txt"), []byte(newContent), 0644)
	require.NoError(t, err)
	runDiffGit(t, repoDir, "add", "test.txt")
	runDiffGit(t, repoDir, "commit", "-m", "Modify content")
	hash2 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetDiff(hash1, hash2)
	require.NoError(t, err)

	require.Len(t, diffs, 1)
	assert.NotEmpty(t, diffs[0].Hunks)
}

func TestFileDiff_Fields(t *testing.T) {
	diff := FileDiff{
		Path:      "new.txt",
		OldPath:   "old.txt",
		Status:    "R",
		Additions: 5,
		Deletions: 3,
		Hunks: []DiffHunk{
			{
				OldStart: 1,
				OldLines: 3,
				NewStart: 1,
				NewLines: 5,
				Lines:    []string{" context", "-removed", "+added"},
			},
		},
	}

	assert.Equal(t, "new.txt", diff.Path)
	assert.Equal(t, "old.txt", diff.OldPath)
	assert.Equal(t, "R", diff.Status)
	assert.Equal(t, 5, diff.Additions)
	assert.Equal(t, 3, diff.Deletions)
	assert.Len(t, diff.Hunks, 1)
}

func TestDiffHunk_Fields(t *testing.T) {
	hunk := DiffHunk{
		OldStart: 10,
		OldLines: 5,
		NewStart: 12,
		NewLines: 7,
		Lines:    []string{" unchanged", "-removed", "+added", " context"},
	}

	assert.Equal(t, 10, hunk.OldStart)
	assert.Equal(t, 5, hunk.OldLines)
	assert.Equal(t, 12, hunk.NewStart)
	assert.Equal(t, 7, hunk.NewLines)
	assert.Len(t, hunk.Lines, 4)
}

func TestGetDiff_BinaryFile(t *testing.T) {
	repoDir, cleanup := setupDiffTestRepo(t)
	defer cleanup()

	// Create first commit
	hash1 := createDiffFileAndCommit(t, repoDir, "test.txt", "text\n", "First commit")

	// Add a binary file
	binaryContent := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0xFF, 0xFE}
	err := os.WriteFile(filepath.Join(repoDir, "binary.bin"), binaryContent, 0644)
	require.NoError(t, err)
	runDiffGit(t, repoDir, "add", "binary.bin")
	runDiffGit(t, repoDir, "commit", "-m", "Add binary file")
	hash2 := runDiffGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	diffs, err := client.GetDiff(hash1, hash2)
	require.NoError(t, err)

	require.Len(t, diffs, 1)
	assert.Equal(t, "binary.bin", diffs[0].Path)
	assert.True(t, diffs[0].Binary)
}
