package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
)

var (
	ErrDomainNotAllowed = fmt.Errorf("domain not in allowlist")
	ErrCloneTimeout     = fmt.Errorf("clone timed out")
	ErrRepoTooLarge     = fmt.Errorf("repository exceeds size limit")
	ErrInvalidURL       = fmt.Errorf("invalid repository URL")
)

// CloneConfig configures an in-memory shallow git clone.
type CloneConfig struct {
	URL            string
	Branch         string
	Depth          int
	AllowedDomains []string
	MaxTotalBytes  int64
	Timeout        time.Duration
}

// CloneResult captures the outcome of an in-memory clone operation.
// The cloned files reside entirely in MemFS — no disk I/O occurs.
type CloneResult struct {
	MemFS      billy.Filesystem
	RepoName   string
	Owner      string
	CommitHash string
	Branch     string
	FileCount  int
	TotalBytes int64
	ClonedAt   time.Time
	Duration   time.Duration
}

// DefaultAllowedDomains returns the default set of allowed git hosting domains.
func DefaultAllowedDomains() []string {
	return []string{
		"github.com",
		"gitlab.com",
		"bitbucket.org",
		"sr.ht",
		"codeberg.org",
	}
}

// Clone performs a bounded, shallow, fully in-memory git clone.
// The repository is cloned into a billy.Filesystem (memfs) with go-git's
// in-memory object storage. No files are written to disk at any point.
func Clone(ctx context.Context, cfg CloneConfig) (*CloneResult, error) {
	cfg = applyCloneDefaults(cfg)

	repoURL, err := parseRepoURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	if !isDomainAllowed(repoURL.Hostname(), cfg.AllowedDomains) {
		return nil, fmt.Errorf("%w: %s", ErrDomainNotAllowed, repoURL.Hostname())
	}

	owner, repo := extractOwnerRepo(repoURL.Path)

	cloneCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	start := time.Now()

	fs := memfs.New()
	storer := memory.NewStorage()

	cloneOpts := &gogit.CloneOptions{
		URL:             repoURL.String(),
		Depth:           cfg.Depth,
		SingleBranch:    true,
		NoCheckout:      false,
		Tags:            gogit.NoTags,
		InsecureSkipTLS: false,
	}

	if cfg.Branch != "" && cfg.Branch != "HEAD" {
		cloneOpts.ReferenceName = plumbing.NewBranchReferenceName(cfg.Branch)
	}

	repository, err := gogit.CloneContext(cloneCtx, storer, fs, cloneOpts)
	if err != nil {
		if cloneCtx.Err() != nil {
			return nil, ErrCloneTimeout
		}
		return nil, fmt.Errorf("git clone: %w", err)
	}

	fileCount, totalBytes, err := measureMemFS(fs, "/", cfg.MaxTotalBytes)
	if err != nil {
		return nil, err
	}

	commitHash := resolveMemHEAD(repository)

	return &CloneResult{
		MemFS:      fs,
		RepoName:   repo,
		Owner:      owner,
		CommitHash: commitHash,
		Branch:     cfg.Branch,
		FileCount:  fileCount,
		TotalBytes: totalBytes,
		ClonedAt:   time.Now(),
		Duration:   time.Since(start),
	}, nil
}

func applyCloneDefaults(cfg CloneConfig) CloneConfig {
	if cfg.Branch == "" {
		cfg.Branch = "HEAD"
	}
	if cfg.Depth <= 0 {
		cfg.Depth = 1
	}
	if cfg.MaxTotalBytes <= 0 {
		cfg.MaxTotalBytes = 100 * 1024 * 1024 // 100 MiB
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	if len(cfg.AllowedDomains) == 0 {
		cfg.AllowedDomains = DefaultAllowedDomains()
	}
	return cfg
}

func parseRepoURL(raw string) (*url.URL, error) {
	normalized := normalizeGitURL(raw)
	u, err := url.Parse(normalized)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("missing scheme or host in %q", raw)
	}
	return u, nil
}

// normalizeGitURL converts shorthand formats to full URLs.
// Supports: github.com/owner/repo, git@github.com:owner/repo.git
func normalizeGitURL(raw string) string {
	raw = strings.TrimSpace(raw)

	// SSH format: git@host:owner/repo.git
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(raw[4:], ":", 2)
		if len(parts) == 2 {
			return "https://" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
		}
	}

	// No scheme: github.com/owner/repo
	if !strings.Contains(raw, "://") {
		return "https://" + raw
	}

	return raw
}

func isDomainAllowed(host string, allowed []string) bool {
	for _, domain := range allowed {
		if strings.EqualFold(host, domain) {
			return true
		}
	}
	return false
}

func extractOwnerRepo(path string) (owner, repo string) {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.SplitN(path, "/", 3)
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return parts[0], parts[0]
	default:
		return parts[0], parts[1]
	}
}

// measureMemFS walks an in-memory billy filesystem, counting files and bytes.
// Returns ErrRepoTooLarge if total exceeds maxBytes.
func measureMemFS(fs billy.Filesystem, dir string, maxBytes int64) (int, int64, error) {
	var fileCount int
	var totalBytes int64

	entries, err := fs.ReadDir(dir)
	if err != nil {
		return 0, 0, fmt.Errorf("readdir %s: %w", dir, err)
	}

	for _, entry := range entries {
		fullPath := dir + "/" + entry.Name()
		if dir == "/" {
			fullPath = "/" + entry.Name()
		}

		if entry.IsDir() {
			if entry.Name() == ".git" {
				continue
			}
			fc, tb, err := measureMemFS(fs, fullPath, maxBytes-totalBytes)
			if err != nil {
				return 0, 0, err
			}
			fileCount += fc
			totalBytes += tb
			continue
		}

		fileCount++
		totalBytes += entry.Size()
		if totalBytes > maxBytes {
			return 0, 0, ErrRepoTooLarge
		}
	}

	return fileCount, totalBytes, nil
}

func resolveMemHEAD(repo *gogit.Repository) string {
	ref, err := repo.Head()
	if err != nil {
		return ""
	}
	return ref.Hash().String()
}

// ContentHash computes a SHA-256 hash of a file's contents.
func ContentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
