package librarian

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/git"
	"github.com/adalundhe/sylk/core/versioning"
)

// CloneStore manages cloned remote packages within the librarian's VFS.
// Repositories are cloned entirely in memory (go-git memfs), then ingested
// into a VFS pipeline via SessionVFS. After commit, DiskFlusher writes the
// files to the working directory under .sylk/packages/{owner}/{repo}/ so
// they become searchable by the librarian's disk-based tools (grep, glob,
// read_file, find_symbol).
//
// Each clone is tracked by a CloneEntry that records provenance metadata.
// The store is session-scoped; Close() removes all tracked entries.
type CloneStore struct {
	mu         sync.RWMutex
	sessionVFS *versioning.SessionVFS
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

// NewCloneStore creates a clone store. The SessionVFS is optional at
// construction time — it can be set later via SetSessionVFS.
func NewCloneStore(workingDir string) *CloneStore {
	return &CloneStore{
		workingDir: workingDir,
		clones:     make(map[string]*CloneEntry),
	}
}

// SetSessionVFS wires the VFS backend for clone ingestion.
func (s *CloneStore) SetSessionVFS(svfs *versioning.SessionVFS) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionVFS = svfs
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
	svfs := s.sessionVFS
	s.mu.RUnlock()

	if svfs == nil {
		return nil, fmt.Errorf("clone store: session VFS not configured")
	}

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

	// Ingest from in-memory FS into a VFS pipeline.
	vfsPrefix := ".sylk/packages/" + cloneResult.Owner + "/" + cloneResult.RepoName
	pipelineID := "clone-" + cloneResult.Owner + "-" + cloneResult.RepoName

	pipelineVFS, err := svfs.BeginPipeline(versioning.BeginPipelineConfig{
		PipelineID: pipelineID,
		SessionID:  svfs.SessionID(),
		AgentID:    "librarian",
		AgentRole:  "clone",
		WorkingDir: s.workingDir,
	})
	if err != nil {
		return nil, fmt.Errorf("begin pipeline for %s: %w", key, err)
	}

	ingestResult, err := git.IngestIntoVFS(ctx, git.IngestConfig{
		MemFS:     cloneResult.MemFS,
		VFSPrefix: vfsPrefix,
		VFS:       pipelineVFS,
	})
	if err != nil {
		svfs.RollbackPipeline(pipelineID)
		return nil, fmt.Errorf("ingest %s: %w", key, err)
	}

	// Commit pipeline → OT merge into global VFS.
	if _, err := svfs.CommitPipeline(ctx, pipelineID); err != nil {
		return nil, fmt.Errorf("commit pipeline for %s: %w", key, err)
	}

	// Flush global VFS to disk so search tools can find the files.
	if _, err := svfs.DiskFlusher().Flush(ctx); err != nil {
		return nil, fmt.Errorf("flush %s to disk: %w", key, err)
	}

	entry := &CloneEntry{
		ID:         key,
		URL:        repoURL,
		Owner:      cloneResult.Owner,
		RepoName:   cloneResult.RepoName,
		Branch:     cloneResult.Branch,
		CommitHash: cloneResult.CommitHash,
		DiskPath:   s.workingDir + "/" + vfsPrefix,
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
// The underlying VFS files are managed by the session lifecycle.
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

// Close marks the store as closed. VFS and disk cleanup is handled
// by the SessionVFS lifecycle.
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
