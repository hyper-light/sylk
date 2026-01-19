package git

import (
	"context"
	"sync"
	"time"
)

// =============================================================================
// GitManagerConfig
// =============================================================================

// GitManagerConfig configures the git manager.
type GitManagerConfig struct {
	// RepoPath is the path to the git repository.
	RepoPath string

	// CacheBlame enables caching of blame results.
	CacheBlame bool

	// BlameCacheTTL is the time-to-live for cached blame results.
	BlameCacheTTL time.Duration

	// MaxConcurrent limits concurrent git operations.
	// Defaults to 4 if not specified.
	MaxConcurrent int
}

// defaultMaxConcurrent is the default limit for concurrent operations.
const defaultMaxConcurrent = 4

// =============================================================================
// GitManager
// =============================================================================

// GitManager coordinates all git operations.
// It provides lazy initialization, caching, and concurrency control.
type GitManager struct {
	config     GitManagerConfig
	client     *GitClient
	blameCache *BlameCache
	mu         sync.RWMutex

	// Semaphore for limiting concurrent operations
	sem chan struct{}

	// closed indicates the manager has been closed
	closed bool
}

// NewGitManager creates a new git manager.
// Returns error only if config is invalid.
func NewGitManager(config GitManagerConfig) (*GitManager, error) {
	if config.RepoPath == "" {
		return nil, ErrRepoPathEmpty
	}

	maxConcurrent := config.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}

	manager := &GitManager{
		config: config,
		sem:    make(chan struct{}, maxConcurrent),
	}

	if config.CacheBlame && config.BlameCacheTTL > 0 {
		manager.blameCache = NewBlameCache(config.BlameCacheTTL)
	}

	return manager, nil
}

// =============================================================================
// Client Access
// =============================================================================

// Client returns the underlying GitClient (lazy initialization).
// Thread-safe with double-checked locking pattern.
func (m *GitManager) Client() (*GitClient, error) {
	if err := m.checkClosed(); err != nil {
		return nil, err
	}

	// Fast path: check if already initialized
	m.mu.RLock()
	if m.client != nil {
		defer m.mu.RUnlock()
		return m.client, nil
	}
	m.mu.RUnlock()

	// Slow path: initialize client
	return m.initializeClient()
}

// initializeClient creates the client with proper locking.
func (m *GitManager) initializeClient() (*GitClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if m.client != nil {
		return m.client, nil
	}

	client, err := NewGitClient(m.config.RepoPath)
	if err != nil {
		return nil, err
	}

	m.client = client
	return m.client, nil
}

// =============================================================================
// Availability Check
// =============================================================================

// IsAvailable returns true if git is available for this path.
func (m *GitManager) IsAvailable() bool {
	client, err := m.Client()
	if err != nil {
		return false
	}
	return client.IsGitRepo()
}

// =============================================================================
// Modified Files Operations
// =============================================================================

// GetModifiedSince returns files modified since the given time.
// Uses semaphore for concurrency control.
func (m *GitManager) GetModifiedSince(ctx context.Context, since time.Time) ([]string, error) {
	if err := m.checkClosed(); err != nil {
		return nil, err
	}

	if err := m.acquireSemaphore(ctx); err != nil {
		return nil, err
	}
	defer m.releaseSemaphore()

	client, err := m.Client()
	if err != nil {
		return nil, err
	}

	return client.ListModifiedFiles(since)
}

// =============================================================================
// Blame Operations
// =============================================================================

// GetBlame returns blame info with caching.
func (m *GitManager) GetBlame(ctx context.Context, path string) (*BlameResult, error) {
	if err := m.checkClosed(); err != nil {
		return nil, err
	}

	if err := m.acquireSemaphore(ctx); err != nil {
		return nil, err
	}
	defer m.releaseSemaphore()

	client, err := m.Client()
	if err != nil {
		return nil, err
	}

	return m.getBlameWithCache(client, path)
}

// getBlameWithCache retrieves blame, using cache if available.
func (m *GitManager) getBlameWithCache(client *GitClient, path string) (*BlameResult, error) {
	if m.blameCache != nil {
		return client.GetBlameInfoCached(path, m.blameCache)
	}
	return client.GetBlameInfo(path)
}

// =============================================================================
// Diff Operations
// =============================================================================

// GetDiffBetween returns diff between two commits.
func (m *GitManager) GetDiffBetween(ctx context.Context, from, to string) ([]FileDiff, error) {
	if err := m.checkClosed(); err != nil {
		return nil, err
	}

	if err := m.acquireSemaphore(ctx); err != nil {
		return nil, err
	}
	defer m.releaseSemaphore()

	client, err := m.Client()
	if err != nil {
		return nil, err
	}

	return client.GetDiff(from, to)
}

// =============================================================================
// History Operations
// =============================================================================

// GetFileHistory returns history for a file.
func (m *GitManager) GetFileHistory(ctx context.Context, path string, limit int) ([]*CommitInfo, error) {
	if err := m.checkClosed(); err != nil {
		return nil, err
	}

	if err := m.acquireSemaphore(ctx); err != nil {
		return nil, err
	}
	defer m.releaseSemaphore()

	client, err := m.Client()
	if err != nil {
		return nil, err
	}

	opts := FileHistoryOptions{
		Limit: limit,
	}
	return client.GetFileHistory(path, opts)
}

// =============================================================================
// Semaphore Operations
// =============================================================================

// acquireSemaphore acquires a semaphore slot with context support.
func (m *GitManager) acquireSemaphore(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// releaseSemaphore releases a semaphore slot.
func (m *GitManager) releaseSemaphore() {
	<-m.sem
}

// =============================================================================
// State Management
// =============================================================================

// checkClosed returns an error if the manager is closed.
func (m *GitManager) checkClosed() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return ErrManagerClosed
	}
	return nil
}

// Close releases all resources.
// Safe to call multiple times.
func (m *GitManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}

	m.closed = true

	if m.client != nil {
		m.client.Close()
		m.client = nil
	}

	if m.blameCache != nil {
		m.blameCache.Clear()
	}

	return nil
}
