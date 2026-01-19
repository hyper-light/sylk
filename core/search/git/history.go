// Package git provides git integration for the Sylk Document Search System.
package git

import (
	"io"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// FileHistoryOptions
// =============================================================================

// FileHistoryOptions configures file history retrieval.
type FileHistoryOptions struct {
	// Limit is the maximum number of commits to return (0 = unlimited).
	Limit int

	// FollowRenames follows file renames in history.
	FollowRenames bool

	// Since filters commits after this time.
	Since time.Time

	// Until filters commits before this time.
	Until time.Time
}

// =============================================================================
// GetFileHistory
// =============================================================================

// GetFileHistory returns commit history for a file.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrFileNotFound if the file does not exist.
func (c *GitClient) GetFileHistory(path string, opts FileHistoryOptions) ([]*CommitInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getFileHistoryInternal(path, opts)
}

// getFileHistoryInternal retrieves file history without locking.
func (c *GitClient) getFileHistoryInternal(path string, opts FileHistoryOptions) ([]*CommitInfo, error) {
	logOpts := buildLogOptions(path, opts)

	iter, err := c.repo.Log(logOpts)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	return c.collectCommits(iter, path, opts)
}

// buildLogOptions creates git log options from FileHistoryOptions.
func buildLogOptions(path string, opts FileHistoryOptions) *gogit.LogOptions {
	logOpts := &gogit.LogOptions{
		PathFilter: createPathFilter(path),
	}

	if !opts.Since.IsZero() {
		logOpts.Since = &opts.Since
	}
	if !opts.Until.IsZero() {
		logOpts.Until = &opts.Until
	}

	return logOpts
}

// createPathFilter returns a function that filters commits by file path.
func createPathFilter(path string) func(string) bool {
	return func(commitPath string) bool {
		return commitPath == path
	}
}

// collectCommits iterates through commits and builds CommitInfo slice.
func (c *GitClient) collectCommits(iter object.CommitIter, path string, opts FileHistoryOptions) ([]*CommitInfo, error) {
	var commits []*CommitInfo
	count := 0

	err := iter.ForEach(func(commit *object.Commit) error {
		if opts.Limit > 0 && count >= opts.Limit {
			return io.EOF
		}

		if !c.commitTouchesFile(commit, path, opts.FollowRenames) {
			return nil
		}

		info := convertCommitToInfo(commit)
		commits = append(commits, info)
		count++

		return nil
	})

	if err != nil && err != io.EOF {
		return nil, err
	}

	return commits, nil
}

// commitTouchesFile checks if a commit modifies the specified file.
func (c *GitClient) commitTouchesFile(commit *object.Commit, path string, followRenames bool) bool {
	stats, err := commit.Stats()
	if err != nil {
		return false
	}

	for _, stat := range stats {
		if stat.Name == path {
			return true
		}
	}

	return false
}

// convertCommitToInfo converts a go-git Commit to our CommitInfo type.
func convertCommitToInfo(commit *object.Commit) *CommitInfo {
	return &CommitInfo{
		Hash:         commit.Hash.String(),
		ShortHash:    commit.Hash.String()[:7],
		Author:       commit.Author.Name,
		AuthorEmail:  commit.Author.Email,
		AuthorTime:   commit.Author.When,
		Committer:    commit.Committer.Name,
		CommitterEmail: commit.Committer.Email,
		CommitTime:   commit.Committer.When,
		Message:      commit.Message,
		Subject:      extractSubject(commit.Message),
		ParentHashes: extractParentHashes(commit),
		FilesChanged: extractFilesChanged(commit),
	}
}

// extractSubject returns the first line of the commit message.
func extractSubject(message string) string {
	for i, c := range message {
		if c == '\n' {
			return message[:i]
		}
	}
	return message
}

// extractParentHashes returns string hashes of parent commits.
func extractParentHashes(commit *object.Commit) []string {
	hashes := make([]string, len(commit.ParentHashes))
	for i, hash := range commit.ParentHashes {
		hashes[i] = hash.String()
	}
	return hashes
}

// extractFilesChanged returns list of files modified in commit.
func extractFilesChanged(commit *object.Commit) []string {
	stats, err := commit.Stats()
	if err != nil {
		return nil
	}

	files := make([]string, len(stats))
	for i, stat := range stats {
		files[i] = stat.Name
	}
	return files
}

// =============================================================================
// GetFileAtCommit
// =============================================================================

// GetFileAtCommit retrieves file content at a specific commit.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrCommitNotFound if the commit does not exist.
// Returns ErrFileNotFound if the file does not exist in that commit.
func (c *GitClient) GetFileAtCommit(path string, commitHash string) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getFileAtCommitInternal(path, commitHash)
}

// getFileAtCommitInternal retrieves file content without locking.
func (c *GitClient) getFileAtCommitInternal(path string, commitHash string) ([]byte, error) {
	hash, err := c.resolveCommitHash(commitHash)
	if err != nil {
		return nil, err
	}

	commit, err := c.repo.CommitObject(hash)
	if err != nil {
		return nil, ErrCommitNotFound
	}

	return c.readFileFromCommit(commit, path)
}

// resolveCommitHash converts a hash string to plumbing.Hash.
func (c *GitClient) resolveCommitHash(hashStr string) (plumbing.Hash, error) {
	hash := plumbing.NewHash(hashStr)

	// Try to resolve short hashes
	if len(hashStr) < 40 {
		resolved, err := c.resolveShortHash(hashStr)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		hash = resolved
	}

	return hash, nil
}

// resolveShortHash attempts to resolve a short hash to full hash.
func (c *GitClient) resolveShortHash(shortHash string) (plumbing.Hash, error) {
	iter, err := c.repo.CommitObjects()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	defer iter.Close()

	return findMatchingCommit(iter, shortHash)
}

// findMatchingCommit finds a commit matching the short hash prefix.
func findMatchingCommit(iter object.CommitIter, prefix string) (plumbing.Hash, error) {
	var found plumbing.Hash

	err := iter.ForEach(func(commit *object.Commit) error {
		if hasMatchingPrefix(commit.Hash.String(), prefix) {
			found = commit.Hash
			return io.EOF
		}
		return nil
	})

	if err != nil && err != io.EOF {
		return plumbing.ZeroHash, err
	}

	if found.IsZero() {
		return plumbing.ZeroHash, ErrCommitNotFound
	}

	return found, nil
}

// hasMatchingPrefix checks if hash starts with prefix.
func hasMatchingPrefix(hash, prefix string) bool {
	if len(hash) < len(prefix) {
		return false
	}
	return hash[:len(prefix)] == prefix
}

// readFileFromCommit reads a file's content from a commit.
func (c *GitClient) readFileFromCommit(commit *object.Commit, path string) ([]byte, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	file, err := tree.File(path)
	if err != nil {
		return nil, ErrFileNotFound
	}

	return readFileContent(file)
}

// readFileContent reads the content of a file object.
func readFileContent(file *object.File) ([]byte, error) {
	reader, err := file.Reader()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return io.ReadAll(reader)
}

// =============================================================================
// GetCommit
// =============================================================================

// GetCommit retrieves full commit info for a hash.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrCommitNotFound if the commit does not exist.
func (c *GitClient) GetCommit(hash string) (*CommitInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getCommitInternal(hash)
}

// getCommitInternal retrieves commit info without locking.
func (c *GitClient) getCommitInternal(hashStr string) (*CommitInfo, error) {
	hash, err := c.resolveCommitHash(hashStr)
	if err != nil {
		return nil, err
	}

	commit, err := c.repo.CommitObject(hash)
	if err != nil {
		return nil, ErrCommitNotFound
	}

	return convertCommitToInfo(commit), nil
}

// =============================================================================
// ListTrackedFiles
// =============================================================================

// ListTrackedFiles returns all files tracked by git.
// Returns ErrNotGitRepo if not a git repository.
func (c *GitClient) ListTrackedFiles() ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.listTrackedFilesInternal()
}

// listTrackedFilesInternal lists tracked files without locking.
func (c *GitClient) listTrackedFilesInternal() ([]string, error) {
	ref, err := c.repo.Head()
	if err != nil {
		// Empty repository has no tracked files
		if err == plumbing.ErrReferenceNotFound {
			return []string{}, nil
		}
		return nil, err
	}

	commit, err := c.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, err
	}

	return c.collectTrackedFiles(commit)
}

// collectTrackedFiles walks the tree and collects all file paths.
func (c *GitClient) collectTrackedFiles(commit *object.Commit) ([]string, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	var files []string

	err = tree.Files().ForEach(func(file *object.File) error {
		files = append(files, file.Name)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}
