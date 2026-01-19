package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupBlameTestRepo creates a temporary git repository for blame testing.
func setupBlameTestRepo(t *testing.T) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "git-blame-test-*")
	require.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	// Initialize git repo
	runBlameGit(t, tmpDir, "init")
	runBlameGit(t, tmpDir, "config", "user.email", "test@example.com")
	runBlameGit(t, tmpDir, "config", "user.name", "Test User")

	return tmpDir, cleanup
}

// runBlameGit executes a git command in the given directory.
func runBlameGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))

	return string(out)
}

// createBlameFileAndCommit creates a file with content and commits it.
func createBlameFileAndCommit(t *testing.T, dir, filename, content, message string) string {
	t.Helper()

	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	runBlameGit(t, dir, "add", filename)
	runBlameGit(t, dir, "commit", "-m", message)

	// Return the commit hash
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)

	return string(out[:40])
}

func TestGetBlameInfo_ReturnsEntriesForEachLine(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	content := "line 1\nline 2\nline 3\n"
	createBlameFileAndCommit(t, repoDir, "test.txt", content, "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	result, err := client.GetBlameInfo("test.txt")
	require.NoError(t, err)

	assert.Equal(t, "test.txt", result.Path)
	assert.Len(t, result.Lines, 3)

	for i, line := range result.Lines {
		assert.Equal(t, i+1, line.LineNumber)
		assert.NotEmpty(t, line.CommitHash)
		assert.Equal(t, "Test User", line.Author)
		assert.False(t, line.AuthorTime.IsZero())
	}

	assert.Equal(t, "line 1", result.Lines[0].Content)
	assert.Equal(t, "line 2", result.Lines[1].Content)
	assert.Equal(t, "line 3", result.Lines[2].Content)
}

func TestGetBlameInfo_EmptyFile(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	createBlameFileAndCommit(t, repoDir, "empty.txt", "", "Add empty file")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	result, err := client.GetBlameInfo("empty.txt")
	require.NoError(t, err)

	assert.Equal(t, "empty.txt", result.Path)
	assert.Empty(t, result.Lines)
}

func TestGetBlameInfo_MultipleCommits(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	// First commit
	hash1 := createBlameFileAndCommit(t, repoDir, "test.txt", "line 1\n", "First commit")

	// Add a line in second commit
	path := filepath.Join(repoDir, "test.txt")
	err := os.WriteFile(path, []byte("line 1\nline 2\n"), 0644)
	require.NoError(t, err)
	runBlameGit(t, repoDir, "add", "test.txt")
	runBlameGit(t, repoDir, "commit", "-m", "Second commit")
	hash2 := runBlameGit(t, repoDir, "rev-parse", "HEAD")[:40]

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	result, err := client.GetBlameInfo("test.txt")
	require.NoError(t, err)

	assert.Len(t, result.Lines, 2)
	assert.Equal(t, hash1, result.Lines[0].CommitHash)
	assert.Equal(t, hash2, result.Lines[1].CommitHash)
}

func TestGetBlameInfo_FileNotFound(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	createBlameFileAndCommit(t, repoDir, "exists.txt", "content", "Initial")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetBlameInfo("nonexistent.txt")
	assert.Error(t, err)
}

func TestGetBlameRange_ReturnsCorrectSubset(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	content := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	createBlameFileAndCommit(t, repoDir, "test.txt", content, "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	// Get lines 2-4 (1-indexed, inclusive)
	lines, err := client.GetBlameRange("test.txt", 2, 4)
	require.NoError(t, err)

	assert.Len(t, lines, 3)
	assert.Equal(t, 2, lines[0].LineNumber)
	assert.Equal(t, 3, lines[1].LineNumber)
	assert.Equal(t, 4, lines[2].LineNumber)
	assert.Equal(t, "line 2", lines[0].Content)
	assert.Equal(t, "line 3", lines[1].Content)
	assert.Equal(t, "line 4", lines[2].Content)
}

func TestGetBlameRange_InvalidRange(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	content := "line 1\nline 2\nline 3\n"
	createBlameFileAndCommit(t, repoDir, "test.txt", content, "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	tests := []struct {
		name  string
		start int
		end   int
	}{
		{"start less than 1", 0, 2},
		{"start greater than end", 3, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.GetBlameRange("test.txt", tt.start, tt.end)
			assert.Error(t, err)
		})
	}
}

func TestGetBlameRange_EndBeyondFile(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	content := "line 1\nline 2\nline 3\n"
	createBlameFileAndCommit(t, repoDir, "test.txt", content, "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetBlameRange("test.txt", 1, 100)
	assert.Error(t, err)
}

func TestGetBlameRange_SingleLine(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	content := "line 1\nline 2\nline 3\n"
	createBlameFileAndCommit(t, repoDir, "test.txt", content, "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	lines, err := client.GetBlameRange("test.txt", 2, 2)
	require.NoError(t, err)

	assert.Len(t, lines, 1)
	assert.Equal(t, 2, lines[0].LineNumber)
	assert.Equal(t, "line 2", lines[0].Content)
}

func TestBlameCache_StoresAndRetrieves(t *testing.T) {
	cache := NewBlameCache(5 * time.Minute)

	result := &BlameResult{
		Path: "test.txt",
		Lines: []BlameLine{
			{LineNumber: 1, CommitHash: "abc123", Author: "Test", Content: "line 1"},
		},
	}

	cache.Set("test.txt", result)

	cached, found := cache.Get("test.txt")
	assert.True(t, found)
	assert.Equal(t, result.Path, cached.Path)
	assert.Len(t, cached.Lines, 1)
}

func TestBlameCache_ReturnsNotFoundForMissing(t *testing.T) {
	cache := NewBlameCache(5 * time.Minute)

	_, found := cache.Get("nonexistent.txt")
	assert.False(t, found)
}

func TestBlameCache_ExpiresAfterTTL(t *testing.T) {
	cache := NewBlameCache(50 * time.Millisecond)

	result := &BlameResult{
		Path: "test.txt",
		Lines: []BlameLine{
			{LineNumber: 1, CommitHash: "abc123", Author: "Test", Content: "line 1"},
		},
	}

	cache.Set("test.txt", result)

	// Should be found immediately
	_, found := cache.Get("test.txt")
	assert.True(t, found)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, found = cache.Get("test.txt")
	assert.False(t, found)
}

func TestBlameCache_Delete(t *testing.T) {
	cache := NewBlameCache(5 * time.Minute)

	result := &BlameResult{
		Path: "test.txt",
		Lines: []BlameLine{
			{LineNumber: 1, CommitHash: "abc123", Author: "Test", Content: "line 1"},
		},
	}

	cache.Set("test.txt", result)
	cache.Delete("test.txt")

	_, found := cache.Get("test.txt")
	assert.False(t, found)
}

func TestBlameCache_Clear(t *testing.T) {
	cache := NewBlameCache(5 * time.Minute)

	cache.Set("file1.txt", &BlameResult{Path: "file1.txt"})
	cache.Set("file2.txt", &BlameResult{Path: "file2.txt"})

	cache.Clear()

	_, found1 := cache.Get("file1.txt")
	_, found2 := cache.Get("file2.txt")

	assert.False(t, found1)
	assert.False(t, found2)
}

func TestGetBlameInfoCached_UsesCacheWhenAvailable(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	content := "line 1\nline 2\n"
	createBlameFileAndCommit(t, repoDir, "test.txt", content, "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	cache := NewBlameCache(5 * time.Minute)

	// First call should populate cache
	result1, err := client.GetBlameInfoCached("test.txt", cache)
	require.NoError(t, err)

	// Second call should use cache
	result2, err := client.GetBlameInfoCached("test.txt", cache)
	require.NoError(t, err)

	// Results should be equal
	assert.Equal(t, result1.Path, result2.Path)
	assert.Len(t, result2.Lines, 2)
}

func TestGetBlameInfo_NonGitDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	client, err := NewGitClient(tmpDir)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.GetBlameInfo("test.txt")
	assert.ErrorIs(t, err, ErrNotGitRepo)
}

func TestBlameLine_Fields(t *testing.T) {
	line := BlameLine{
		LineNumber:  42,
		CommitHash:  "abcdef1234567890abcdef1234567890abcdef12",
		Author:      "John Doe",
		AuthorEmail: "john@example.com",
		AuthorTime:  time.Now(),
		Content:     "func main() {",
	}

	assert.Equal(t, 42, line.LineNumber)
	assert.Equal(t, "abcdef1234567890abcdef1234567890abcdef12", line.CommitHash)
	assert.Equal(t, "John Doe", line.Author)
	assert.Equal(t, "john@example.com", line.AuthorEmail)
	assert.False(t, line.AuthorTime.IsZero())
	assert.Equal(t, "func main() {", line.Content)
}

func TestBlameResult_Fields(t *testing.T) {
	result := &BlameResult{
		Path: "main.go",
		Lines: []BlameLine{
			{LineNumber: 1, CommitHash: "abc", Author: "Test", Content: "package main"},
		},
	}

	assert.Equal(t, "main.go", result.Path)
	assert.Len(t, result.Lines, 1)
}

func TestGetBlameInfo_WithSpecialCharactersInContent(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	content := "line with \"quotes\"\nline with 'apostrophe'\nline with <brackets>\n"
	createBlameFileAndCommit(t, repoDir, "special.txt", content, "Add special chars")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	result, err := client.GetBlameInfo("special.txt")
	require.NoError(t, err)

	assert.Len(t, result.Lines, 3)
	assert.Equal(t, "line with \"quotes\"", result.Lines[0].Content)
	assert.Equal(t, "line with 'apostrophe'", result.Lines[1].Content)
	assert.Equal(t, "line with <brackets>", result.Lines[2].Content)
}

func TestGetBlameInfo_SubdirectoryFile(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	// Create subdirectory
	subDir := filepath.Join(repoDir, "subdir")
	err := os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	// Create file in subdirectory
	content := "nested content\n"
	path := filepath.Join(subDir, "nested.txt")
	err = os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	runBlameGit(t, repoDir, "add", "subdir/nested.txt")
	runBlameGit(t, repoDir, "commit", "-m", "Add nested file")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	result, err := client.GetBlameInfo("subdir/nested.txt")
	require.NoError(t, err)

	assert.Equal(t, "subdir/nested.txt", result.Path)
	assert.Len(t, result.Lines, 1)
	assert.Equal(t, "nested content", result.Lines[0].Content)
}

func TestBlameCache_ConcurrentAccess(t *testing.T) {
	cache := NewBlameCache(5 * time.Minute)

	// Concurrent writes and reads
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			path := "file" + string(rune('0'+id)) + ".txt"
			cache.Set(path, &BlameResult{Path: path})
			cache.Get(path)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestGetBlameInfo_AuthorEmail(t *testing.T) {
	repoDir, cleanup := setupBlameTestRepo(t)
	defer cleanup()

	content := "line 1\n"
	createBlameFileAndCommit(t, repoDir, "test.txt", content, "Initial commit")

	client, err := NewGitClient(repoDir)
	require.NoError(t, err)
	defer client.Close()

	result, err := client.GetBlameInfo("test.txt")
	require.NoError(t, err)

	assert.Len(t, result.Lines, 1)
	assert.Equal(t, "test@example.com", result.Lines[0].AuthorEmail)
}
