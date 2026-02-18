// Package git provides Git repository operations for the Sylk search system.
// It supports blame information, diff generation, file history, and repository
// introspection using go-git/v5 for efficient git operations.
package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dgraph-io/ristretto"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// =============================================================================
// Errors
// =============================================================================

// Common errors returned by GitClient operations.
var (
	ErrInvalidCommit  = errors.New("invalid commit reference")
	ErrEmptyPath      = errors.New("repository path cannot be empty")
	ErrNotGitRepo     = errors.New("path is not a git repository")
	ErrNoHead         = errors.New("repository has no HEAD reference")
	ErrRemoteNotFound = errors.New("remote not found")
)

// =============================================================================
// GitClient
// =============================================================================

// GitClient provides operations on a git repository.
// It wraps go-git/v5 for repository operations and provides thread-safe access.
type GitClient struct {
	repoPath  string
	repo      *gogit.Repository
	mu        sync.RWMutex
	isRepo    bool
	diffCache *ristretto.Cache
}

// NewGitClient creates a new GitClient for the given repository path.
// Returns a valid client even if path is not a git repo.
// Returns an error only if path is empty.
func NewGitClient(repoPath string) (*GitClient, error) {
	if repoPath == "" {
		return nil, ErrEmptyPath
	}

	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	cache, _ := ristretto.NewCache(&ristretto.Config{
		NumCounters: 20000, // ~10x expected max entries
		MaxCost:     2048,  // max 2048 cached summaries
		BufferItems: 64,
	})

	client := &GitClient{
		repoPath:  absPath,
		isRepo:    false,
		diffCache: cache,
	}

	client.initRepository(absPath)

	return client, nil
}

// initRepository attempts to open the git repository at the given path.
func (c *GitClient) initRepository(repoPath string) {
	repo, err := gogit.PlainOpen(repoPath)
	if err != nil {
		return
	}

	c.repo = repo
	c.isRepo = true
}

// =============================================================================
// Repository State Methods
// =============================================================================

// RepoPath returns the repository path.
func (c *GitClient) RepoPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.repoPath
}

// IsGitRepo returns true if the path is a git repository.
func (c *GitClient) IsGitRepo() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.isRepo
}

// Repository returns the underlying go-git Repository.
// Returns nil if the path is not a git repository.
func (c *GitClient) Repository() *gogit.Repository {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.repo
}

// Close releases any resources held by the client.
// Safe to call multiple times.
func (c *GitClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.diffCache != nil {
		c.diffCache.Close()
		c.diffCache = nil
	}

	c.repo = nil
	c.isRepo = false

	return nil
}

// =============================================================================
// Reference Methods
// =============================================================================

// GetHead returns the current HEAD commit hash.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrNoHead if the repository has no commits.
func (c *GitClient) GetHead() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return "", ErrNotGitRepo
	}

	return c.getHeadHash()
}

// getHeadHash retrieves the HEAD commit hash without locking.
func (c *GitClient) getHeadHash() (string, error) {
	ref, err := c.repo.Head()
	if err != nil {
		return "", wrapHeadError(err)
	}

	return ref.Hash().String(), nil
}

// wrapHeadError converts go-git HEAD errors to our error types.
func wrapHeadError(err error) error {
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return ErrNoHead
	}
	return err
}

// GetBranch returns the current branch name.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrNoHead if the repository has no HEAD reference.
func (c *GitClient) GetBranch() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return "", ErrNotGitRepo
	}

	return c.getBranchName()
}

// getBranchName retrieves the current branch name without locking.
func (c *GitClient) getBranchName() (string, error) {
	ref, err := c.repo.Head()
	if err != nil {
		return "", wrapHeadError(err)
	}

	return extractBranchName(ref), nil
}

// extractBranchName extracts the short branch name from a reference.
func extractBranchName(ref *plumbing.Reference) string {
	name := ref.Name().String()
	return strings.TrimPrefix(name, "refs/heads/")
}

// GetRemote returns the URL of the specified remote.
// If name is empty, defaults to "origin".
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrRemoteNotFound if the remote does not exist.
func (c *GitClient) GetRemote(name string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return "", ErrNotGitRepo
	}

	remoteName := resolveRemoteName(name)

	return c.getRemoteURL(remoteName)
}

// resolveRemoteName returns "origin" if name is empty.
func resolveRemoteName(name string) string {
	if name == "" {
		return "origin"
	}
	return name
}

// getRemoteURL retrieves the URL for a remote without locking.
func (c *GitClient) getRemoteURL(name string) (string, error) {
	remote, err := c.repo.Remote(name)
	if err != nil {
		return "", ErrRemoteNotFound
	}

	return extractRemoteURL(remote), nil
}

// extractRemoteURL extracts the first URL from a remote config.
func extractRemoteURL(remote *gogit.Remote) string {
	cfg := remote.Config()
	if len(cfg.URLs) == 0 {
		return ""
	}
	return cfg.URLs[0]
}


// fileExists checks if a file exists at the given path.
func (c *GitClient) fileExists(path string) bool {
	fullPath := filepath.Join(c.repoPath, path)
	_, err := os.Stat(fullPath)
	return err == nil
}


// IsValidCommit checks if a commit reference is valid.
// Resolves full hashes, short hashes, and symbolic refs via go-git.
func (c *GitClient) IsValidCommit(ref string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.isRepo {
		return false
	}
	_, err := c.repo.ResolveRevision(plumbing.Revision(ref))
	return err == nil
}

// GetHeadCommit returns the current HEAD commit hash.
func (c *GitClient) GetHeadCommit() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.isRepo {
		return "", ErrNotGitRepo
	}
	return c.getHeadHash()
}
