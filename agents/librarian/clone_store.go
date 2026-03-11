package librarian

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/git"
)

// CloneStore manages cloned remote packages for the librarian.
// Repositories are cloned entirely in memory (go-git memfs), then written to
// the package cache on disk so the librarian's disk-based tools can search
// them without gaining access to the workspace VFS.
type CloneStore struct {
	mu         sync.RWMutex
	workingDir string
	clones     map[string]*CloneEntry
	closed     bool
}

// CloneEntry records metadata about a single cloned repository.
type CloneEntry struct {
	ID         string        `json:"id"`
	URL        string        `json:"url"`
	Owner      string        `json:"owner"`
	RepoName   string        `json:"repo_name"`
	Branch     string        `json:"branch"`
	CommitHash string        `json:"commit_hash"`
	DiskPath   string        `json:"disk_path"`
	FileCount  int           `json:"file_count"`
	TotalBytes int64         `json:"total_bytes"`
	ClonedAt   time.Time     `json:"cloned_at"`
	Duration   time.Duration `json:"duration"`
}

// NewCloneStore creates a clone store.
func NewCloneStore(workingDir string) *CloneStore {
	return &CloneStore{
		workingDir: workingDir,
		clones:     make(map[string]*CloneEntry),
	}
}

// CloneKey returns the canonical key for a clone entry.
func CloneKey(owner, repo string) string {
	return owner + "/" + repo
}

// Get returns a clone entry by key, or nil if not found.
func (s *CloneStore) Get(key string) *CloneEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clones[key]
}

// List returns all clone entries.
func (s *CloneStore) List() []*CloneEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries := make([]*CloneEntry, 0, len(s.clones))
	for _, e := range s.clones {
		entries = append(entries, e)
	}
	return entries
}

// Clone fetches a remote repository entirely in memory, ingests its files
// into the VFS pipeline, commits the pipeline, and flushes to disk.
// If the repository is already cloned, returns the existing entry.
func (s *CloneStore) Clone(ctx context.Context, repoURL, branch string) (*CloneEntry, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, fmt.Errorf("clone store is closed")
	}
	s.mu.RUnlock()

	// In-memory clone — no disk I/O.
	cloneResult, err := git.Clone(ctx, git.CloneConfig{
		URL:    repoURL,
		Branch: branch,
		Depth:  1,
	})
	if err != nil {
		return nil, fmt.Errorf("clone %s: %w", repoURL, err)
	}

	key := CloneKey(cloneResult.Owner, cloneResult.RepoName)

	s.mu.Lock()
	if existing, ok := s.clones[key]; ok {
		s.mu.Unlock()
		return existing, nil
	}
	s.mu.Unlock()

	diskPath := s.workingDir + "/.sylk/packages/" + cloneResult.Owner + "/" + cloneResult.RepoName
	ingestResult, err := git.IngestIntoDisk(ctx, git.DiskIngestConfig{
		MemFS:    cloneResult.MemFS,
		DiskRoot: diskPath,
	})
	if err != nil {
		return nil, fmt.Errorf("ingest %s: %w", key, err)
	}

	entry := &CloneEntry{
		ID:         key,
		URL:        repoURL,
		Owner:      cloneResult.Owner,
		RepoName:   cloneResult.RepoName,
		Branch:     cloneResult.Branch,
		CommitHash: cloneResult.CommitHash,
		DiskPath:   diskPath,
		FileCount:  ingestResult.FilesWritten,
		TotalBytes: ingestResult.BytesWritten,
		ClonedAt:   cloneResult.ClonedAt,
		Duration:   cloneResult.Duration + ingestResult.Duration,
	}

	s.mu.Lock()
	s.clones[key] = entry
	s.mu.Unlock()

	return entry, nil
}

// Remove removes a cloned repository entry from the store index.
// The underlying cloned files remain on disk until a later cleanup pass.
func (s *CloneStore) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.clones[key]; !ok {
		return fmt.Errorf("clone %q not found", key)
	}

	delete(s.clones, key)
	return nil
}

// BaseDir returns the root directory where packages appear on disk
// after VFS flush.
func (s *CloneStore) BaseDir() string {
	return s.workingDir + "/.sylk/packages"
}

// Close marks the store as closed.
func (s *CloneStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true
	clear(s.clones)
	return nil
}
