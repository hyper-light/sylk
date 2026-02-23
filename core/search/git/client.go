// Package git provides Git repository operations for the Sylk search system.
// It supports blame information, diff generation, file history, and repository
// introspection using go-git/v5 for efficient git operations.
package git

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dgraph-io/ristretto"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gogitcache "github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"
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
// Diff Cache Sizing Constants
// =============================================================================

const (
	diffSummaryCostBytes = 48    // DiffSummary: 5 × int + padding
	fileSummaryCostBytes = 32    // FileSummary: 3 × int + padding
	maxDiffCacheEntries  = 4_000 // 2,000 commits × 2 entries each
)

// =============================================================================
// go-git Object Cache Sizing
// =============================================================================
//
// go-git's PlainOpen unconditionally allocates a 96MB ObjectLRU
// (cache.DefaultMaxSize = 96 * MiByte). For Sylk's operation profile
// (status, log, diff, branch view), the working set is bounded by the
// visible commit window and tree fan-out — not the total object store.
//
// openWithSizedCache replaces PlainOpen with a custom open that probes
// the actual packfile sizes and derives the cache budget from real data:
//
//   cacheSize = clamp(totalPackBytes / packCacheDivisor, minObjectCache, maxObjectCache)
//
// This gives a 12–24× reduction on small/medium repos while remaining
// adequate for repositories the size of Kubernetes (~4GB packs → 64MB cap).

const (
	// minObjectCache is the floor for the go-git object LRU.
	// Derived: a fresh repo with loose objects still benefits from
	// caching recently accessed commits and trees during log/diff walks.
	// 4MB holds ~8,000 small objects (commits ~500B) or ~80 large trees
	// (~50KB each in a 75K-file repo). Adequate for repos up to ~32MB pack.
	minObjectCache = 4 * gogitcache.MiByte

	// maxObjectCache is the ceiling for the go-git object LRU.
	// Derived: Sylk's heaviest operation is DagCommitLimit=500 commits
	// with full tree comparison. For Kubernetes-scale repos (75K files,
	// root trees ~500KB), the working set is:
	//   500 commits × 500B          = 250KB
	//   500 parent trees × 50KB avg = 25MB
	//   overhead + blob prefetch    ≈ 15MB
	// Total ~40MB. 64MB provides headroom without the 96MB waste.
	maxObjectCache = 64 * gogitcache.MiByte

	// packCacheDivisor controls what fraction of total packfile bytes
	// maps to object cache budget. Sylk's operations access 5–15% of
	// objects per session. Divisor=8 (~12.5%) covers the hot working
	// set with headroom for access pattern variance.
	packCacheDivisor = 8
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
	sequencer *sequencer
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
		NumCounters: int64(maxDiffCacheEntries * 10),              // 40,000
		MaxCost:     int64(maxDiffCacheEntries) * diffSummaryCostBytes, // 192,000
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
	repo, err := openWithSizedCache(repoPath)
	if err != nil {
		return
	}

	c.repo = repo
	c.isRepo = true
}

// openWithSizedCache opens a go-git Repository with an object cache
// sized to the actual repository, instead of the 96MB default.
func openWithSizedCache(repoPath string) (*gogit.Repository, error) {
	wtFS := osfs.New(repoPath)
	dotFS, err := resolveDotGitFS(repoPath, wtFS)
	if err != nil {
		return nil, err
	}

	cacheSize := deriveObjectCacheSize(dotFS)
	s := filesystem.NewStorage(dotFS, gogitcache.NewObjectLRU(cacheSize))
	return gogit.Open(s, wtFS)
}

// resolveDotGitFS locates the .git storage filesystem. Handles both
// the normal case (.git is a directory) and the gitdir-file case
// (.git is a file pointing elsewhere, used by worktrees and submodules).
func resolveDotGitFS(repoPath string, wtFS billy.Filesystem) (billy.Filesystem, error) {
	fi, err := wtFS.Stat(".git")
	if err != nil {
		return nil, gogit.ErrRepositoryNotExists
	}
	if fi.IsDir() {
		return wtFS.Chroot(".git")
	}
	return resolveGitdirFile(repoPath, wtFS)
}

// resolveGitdirFile reads a .git file (format: "gitdir: <path>\n") and
// returns a billy.Filesystem rooted at the target git directory.
func resolveGitdirFile(repoPath string, wtFS billy.Filesystem) (billy.Filesystem, error) {
	f, err := wtFS.Open(".git")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	line := strings.TrimSpace(string(content))
	const gitdirPrefix = "gitdir: "
	if !strings.HasPrefix(line, gitdirPrefix) {
		return nil, fmt.Errorf("invalid .git file: missing gitdir prefix")
	}

	gitdir := strings.TrimPrefix(line, gitdirPrefix)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(repoPath, gitdir)
	}
	return osfs.New(filepath.Clean(gitdir)), nil
}

// deriveObjectCacheSize probes the packfile sizes in the .git directory
// and derives a cache budget proportional to actual repository data.
// Falls back to minObjectCache when packfiles are absent or unreadable.
func deriveObjectCacheSize(dotFS billy.Filesystem) gogitcache.FileSize {
	entries, err := dotFS.ReadDir(filepath.Join("objects", "pack"))
	if err != nil {
		return minObjectCache
	}

	var totalPackBytes int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pack") {
			continue
		}
		totalPackBytes += entry.Size()
	}

	if totalPackBytes == 0 {
		return minObjectCache
	}

	derived := gogitcache.FileSize(totalPackBytes / packCacheDivisor)
	return max(minObjectCache, min(maxObjectCache, derived))
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
