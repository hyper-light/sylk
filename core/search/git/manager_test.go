package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

// setupManagerTestRepo creates a temporary git repository for testing.
func setupManagerTestRepo(t *testing.T) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "git-manager-test-*")
	require.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	// Initialize git repo
	runManagerGit(t, tmpDir, "init")
	runManagerGit(t, tmpDir, "config", "user.email", "test@example.com")
	runManagerGit(t, tmpDir, "config", "user.name", "Test User")

	return tmpDir, cleanup
}

// runManagerGit executes a git command in the given directory.
func runManagerGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))

	return string(out)
}

// createManagerFileAndCommit creates a file with content and commits it.
func createManagerFileAndCommit(t *testing.T, dir, filename, content, message string) string {
	t.Helper()

	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)

	runManagerGit(t, dir, "add", filename)
	runManagerGit(t, dir, "commit", "-m", message)

	// Get the commit hash
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)

	return string(out[:40])
}

// =============================================================================
// NewGitManager Tests
// =============================================================================

func TestNewGitManager_ValidConfig(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	config := GitManagerConfig{
		RepoPath:      repoDir,
		CacheBlame:    true,
		BlameCacheTTL: 5 * time.Minute,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	assert.NotNil(t, manager)
	defer manager.Close()
}

func TestNewGitManager_EmptyPath(t *testing.T) {
	config := GitManagerConfig{
		RepoPath: "",
	}

	_, err := NewGitManager(config)
	assert.Error(t, err)
}

func TestNewGitManager_NonExistentPath(t *testing.T) {
	config := GitManagerConfig{
		RepoPath: "/nonexistent/path/that/does/not/exist",
	}

	manager, err := NewGitManager(config)
	// Should succeed with graceful handling
	require.NoError(t, err)
	assert.NotNil(t, manager)
	defer manager.Close()

	// But should report as not available
	assert.False(t, manager.IsAvailable())
}

func TestNewGitManager_DefaultConcurrency(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 0, // Should default to a reasonable value
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	assert.NotNil(t, manager)
	defer manager.Close()
}

// =============================================================================
// Client Lazy Initialization Tests
// =============================================================================

func TestGitManager_Client_LazyInitialization(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	// Client should be lazily initialized
	client, err := manager.Client()
	require.NoError(t, err)
	assert.NotNil(t, client)
}

func TestGitManager_Client_CachesClient(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	// Get client twice
	client1, err := manager.Client()
	require.NoError(t, err)

	client2, err := manager.Client()
	require.NoError(t, err)

	// Should return the same cached client
	assert.Same(t, client1, client2)
}

// =============================================================================
// IsAvailable Tests
// =============================================================================

func TestGitManager_IsAvailable_GitRepo(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	assert.True(t, manager.IsAvailable())
}

func TestGitManager_IsAvailable_NonGitDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	config := GitManagerConfig{
		RepoPath:      tmpDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	assert.False(t, manager.IsAvailable())
}

// =============================================================================
// GetModifiedSince Tests
// =============================================================================

func TestGitManager_GetModifiedSince(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	// Create initial commit
	initialCommit := createManagerFileAndCommit(t, repoDir, "file1.txt", "content1", "Initial")

	// Create another commit
	createManagerFileAndCommit(t, repoDir, "file2.txt", "content2", "Second")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()
	files, err := manager.GetModifiedSince(ctx, time.Now().Add(-time.Hour))
	require.NoError(t, err)

	assert.Len(t, files, 2)
	_ = initialCommit
}

func TestGitManager_GetModifiedSince_ContextCancellation(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	createManagerFileAndCommit(t, repoDir, "file.txt", "content", "Initial")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 1, // Limit to 1 to block subsequent operations
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	// Use channels to coordinate the test reliably
	semAcquired := make(chan struct{})
	release := make(chan struct{})

	// Start a goroutine that will hold the semaphore
	go func() {
		// Manually acquire the semaphore
		manager.sem <- struct{}{}
		close(semAcquired) // Signal that we've acquired

		<-release // Wait until test says to release
		<-manager.sem
	}()

	// Wait for the semaphore to be acquired
	<-semAcquired

	// Now try with a canceled context - should fail to acquire semaphore
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err = manager.GetModifiedSince(ctx, time.Now().Add(-time.Hour))
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)

	// Clean up - release the semaphore
	close(release)
}

// =============================================================================
// GetBlame Tests
// =============================================================================

func TestGitManager_GetBlame(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	createManagerFileAndCommit(t, repoDir, "test.txt", "line 1\nline 2\n", "Initial")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		CacheBlame:    true,
		BlameCacheTTL: 5 * time.Minute,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()
	result, err := manager.GetBlame(ctx, "test.txt")
	require.NoError(t, err)

	assert.Equal(t, "test.txt", result.Path)
	assert.Len(t, result.Lines, 2)
}

func TestGitManager_GetBlame_UsesCache(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	createManagerFileAndCommit(t, repoDir, "test.txt", "content", "Initial")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		CacheBlame:    true,
		BlameCacheTTL: 5 * time.Minute,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// First call
	result1, err := manager.GetBlame(ctx, "test.txt")
	require.NoError(t, err)

	// Second call should use cache
	result2, err := manager.GetBlame(ctx, "test.txt")
	require.NoError(t, err)

	assert.Equal(t, result1.Path, result2.Path)
}

func TestGitManager_GetBlame_CacheDisabled(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	createManagerFileAndCommit(t, repoDir, "test.txt", "content", "Initial")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		CacheBlame:    false, // Cache disabled
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	result, err := manager.GetBlame(ctx, "test.txt")
	require.NoError(t, err)
	assert.NotNil(t, result)
}

// =============================================================================
// GetDiffBetween Tests
// =============================================================================

func TestGitManager_GetDiffBetween(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	commit1 := createManagerFileAndCommit(t, repoDir, "file.txt", "initial", "First")
	commit2 := createManagerFileAndCommit(t, repoDir, "file.txt", "modified", "Second")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()
	diffs, err := manager.GetDiffBetween(ctx, commit1, commit2)
	require.NoError(t, err)

	assert.Len(t, diffs, 1)
	assert.Equal(t, "file.txt", diffs[0].Path)
}

// =============================================================================
// GetFileHistory Tests
// =============================================================================

func TestGitManager_GetFileHistory(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	// Create multiple commits modifying the same file
	createManagerFileAndCommit(t, repoDir, "file.txt", "v1", "Version 1")

	path := filepath.Join(repoDir, "file.txt")
	os.WriteFile(path, []byte("v2"), 0644)
	runManagerGit(t, repoDir, "add", "file.txt")
	runManagerGit(t, repoDir, "commit", "-m", "Version 2")

	os.WriteFile(path, []byte("v3"), 0644)
	runManagerGit(t, repoDir, "add", "file.txt")
	runManagerGit(t, repoDir, "commit", "-m", "Version 3")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()
	history, err := manager.GetFileHistory(ctx, "file.txt", 10)
	require.NoError(t, err)

	assert.Len(t, history, 3)
}

func TestGitManager_GetFileHistory_WithLimit(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	// Create multiple commits with different content each time
	for i := 1; i <= 5; i++ {
		content := []byte("content version " + string(rune('0'+i)) + "\n")
		path := filepath.Join(repoDir, "file.txt")
		os.WriteFile(path, content, 0644)
		runManagerGit(t, repoDir, "add", "file.txt")
		runManagerGit(t, repoDir, "commit", "-m", "Commit "+string(rune('0'+i)))
	}

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()
	history, err := manager.GetFileHistory(ctx, "file.txt", 2)
	require.NoError(t, err)

	assert.Len(t, history, 2)
}

// =============================================================================
// Semaphore Concurrency Tests
// =============================================================================

func TestGitManager_SemaphoreForConcurrency(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	createManagerFileAndCommit(t, repoDir, "file.txt", "content", "Initial")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 2, // Limit to 2 concurrent operations
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	// Verify semaphore is created with correct capacity
	// by checking that we can acquire MaxConcurrent slots
	ctx := context.Background()

	// Launch operations up to limit - should not block
	var wg sync.WaitGroup
	errChan := make(chan error, 10)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.GetModifiedSince(ctx, time.Now().Add(-time.Hour))
			if err != nil {
				errChan <- err
			}
		}()
	}

	// Wait for all to complete
	wg.Wait()
	close(errChan)

	// All operations should complete without error
	for err := range errChan {
		t.Errorf("unexpected error: %v", err)
	}
}

// =============================================================================
// Graceful Non-Git Handling Tests
// =============================================================================

func TestGitManager_GracefulNonGitHandling(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "non-git-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	config := GitManagerConfig{
		RepoPath:      tmpDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Operations should fail gracefully
	_, err = manager.GetModifiedSince(ctx, time.Now())
	assert.Error(t, err)

	_, err = manager.GetBlame(ctx, "file.txt")
	assert.Error(t, err)
}

// =============================================================================
// Close Tests
// =============================================================================

func TestGitManager_Close(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)

	// Use the manager
	_, _ = manager.Client()

	// Close should not error
	err = manager.Close()
	assert.NoError(t, err)

	// Operations after close should fail
	ctx := context.Background()
	_, err = manager.GetModifiedSince(ctx, time.Now())
	assert.Error(t, err)
}

func TestGitManager_Close_DoubleClose(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	config := GitManagerConfig{
		RepoPath:      repoDir,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)

	// First close
	err = manager.Close()
	assert.NoError(t, err)

	// Second close should not error
	err = manager.Close()
	assert.NoError(t, err)
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestGitManager_ConcurrentAccess(t *testing.T) {
	repoDir, cleanup := setupManagerTestRepo(t)
	defer cleanup()

	createManagerFileAndCommit(t, repoDir, "file.txt", "line1\nline2\n", "Initial")

	config := GitManagerConfig{
		RepoPath:      repoDir,
		CacheBlame:    true,
		BlameCacheTTL: 5 * time.Minute,
		MaxConcurrent: 4,
	}

	manager, err := NewGitManager(config)
	require.NoError(t, err)
	defer manager.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	ctx := context.Background()

	// Spawn multiple goroutines doing concurrent operations
	for i := 0; i < 20; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			if _, err := manager.Client(); err != nil {
				errs <- err
			}
		}()

		go func() {
			defer wg.Done()
			if _, err := manager.GetBlame(ctx, "file.txt"); err != nil {
				errs <- err
			}
		}()

		go func() {
			defer wg.Done()
			if _, err := manager.GetModifiedSince(ctx, time.Now().Add(-time.Hour)); err != nil {
				errs <- err
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent operation error: %v", err)
	}
}
