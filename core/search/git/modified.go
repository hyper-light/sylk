package git

import (
	"io"
	"sort"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// ListModifiedFiles
// =============================================================================

// ListModifiedFiles returns files modified since the given time.
// Returns file paths relative to the repository root.
func (c *GitClient) ListModifiedFiles(since time.Time) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getModifiedFilesSince(since)
}

// getModifiedFilesSince retrieves files changed since a timestamp.
func (c *GitClient) getModifiedFilesSince(since time.Time) ([]string, error) {
	commits, err := c.getCommitsSinceInternal(since)
	if err != nil {
		return nil, err
	}

	return c.collectFilesFromCommits(commits), nil
}

// collectFilesFromCommits extracts unique file paths from commits.
func (c *GitClient) collectFilesFromCommits(commits []*CommitInfo) []string {
	seen := make(map[string]bool)
	files := make([]string, 0)

	for _, commit := range commits {
		for _, file := range commit.FilesChanged {
			if !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}

	return files
}

// =============================================================================
// ListModifiedFilesSinceCommit
// =============================================================================

// ListModifiedFilesSinceCommit returns files changed since a commit.
// The commit hash can be full or abbreviated.
func (c *GitClient) ListModifiedFilesSinceCommit(commitHash string) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	if commitHash == "" {
		return nil, ErrInvalidCommit
	}

	return c.getModifiedSinceCommit(commitHash)
}

// getModifiedSinceCommit uses go-git DiffTree to find files changed between
// the given commit and HEAD.
func (c *GitClient) getModifiedSinceCommit(commitHash string) ([]string, error) {
	fromCommit, err := c.resolveCommit(commitHash)
	if err != nil {
		return nil, err
	}

	headRef, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}

	headCommit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, err
	}

	fromTree, err := fromCommit.Tree()
	if err != nil {
		return nil, err
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, err
	}

	changes, err := object.DiffTree(fromTree, headTree)
	if err != nil {
		return nil, err
	}

	return pathsFromChanges(changes), nil
}

// =============================================================================
// GetCommitsSince
// =============================================================================

// GetCommitsSince returns commits since a time.
// Results are ordered from newest to oldest.
func (c *GitClient) GetCommitsSince(since time.Time) ([]*CommitInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getCommitsSinceInternal(since)
}

// getCommitsSinceInternal retrieves commits since a time using go-git Log.
// Includes FilesChanged per commit.
//
// Git stores timestamps at second granularity. go-git's Since filter uses
// time.Before() which compares at nanosecond precision. We truncate to
// second precision so commits created at the same second are included,
// matching git CLI's --since behavior.
func (c *GitClient) getCommitsSinceInternal(since time.Time) ([]*CommitInfo, error) {
	if !c.hasHead() {
		return []*CommitInfo{}, nil
	}

	truncated := since.Truncate(time.Second)
	iter, err := c.repo.Log(&gogit.LogOptions{
		Since: &truncated,
		Order: gogit.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var commits []*CommitInfo
	for {
		commit, nextErr := iter.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		commits = append(commits, convertCommitToInfo(commit))
	}

	return commits, nil
}

// hasHead checks if the repository has any commits.
func (c *GitClient) hasHead() bool {
	_, err := c.repo.Head()
	return err == nil
}

// =============================================================================
// GetFilesInCommit
// =============================================================================

// GetFilesInCommit returns files changed in a specific commit.
func (c *GitClient) GetFilesInCommit(commitHash string) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	if commitHash == "" {
		return nil, ErrInvalidCommit
	}

	return c.getFilesInCommitInternal(commitHash)
}

// getFilesInCommitInternal gets files changed in a commit via DiffTree
// between the commit and its first parent (or empty tree for root commits).
func (c *GitClient) getFilesInCommitInternal(commitHash string) ([]string, error) {
	commit, err := c.resolveCommit(commitHash)
	if err != nil {
		return nil, err
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	parentTree, err := firstParentTree(c, commit)
	if err != nil {
		return nil, err
	}

	changes, err := object.DiffTree(parentTree, tree)
	if err != nil {
		return nil, err
	}

	return pathsFromChanges(changes), nil
}

// =============================================================================
// GetAllCommits
// =============================================================================

// GetAllCommits returns all commits (limited by count).
// If limit is 0 or negative, returns all commits.
// Results are ordered from newest to oldest.
func (c *GitClient) GetAllCommits(limit int) ([]*CommitInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getAllCommitsInternal(limit)
}

// GetAllCommitsPage returns a page of commits starting at skip.
// Returns (commits, hasMore, error). hasMore is true when additional pages
// exist beyond this one. Results are ordered from newest to oldest.
func (c *GitClient) GetAllCommitsPage(skip, limit int) ([]*CommitInfo, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, false, ErrNotGitRepo
	}

	if !c.hasHead() {
		return []*CommitInfo{}, false, nil
	}

	iter, err := c.repo.Log(&gogit.LogOptions{
		Order: gogit.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, false, err
	}
	defer iter.Close()

	// Skip the first 'skip' commits.
	for range skip {
		if _, err := iter.Next(); err != nil {
			if err == io.EOF {
				return []*CommitInfo{}, false, nil
			}
			return nil, false, err
		}
	}

	// Fetch limit+1 to detect whether more pages exist.
	fetchLimit := limit + 1
	commits := make([]*CommitInfo, 0, fetchLimit)
	for range fetchLimit {
		commit, err := iter.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, false, err
		}
		commits = append(commits, convertCommitToInfoLight(commit))
	}

	hasMore := len(commits) > limit
	if hasMore {
		commits = commits[:limit]
	}

	return commits, hasMore, nil
}

// getAllCommitsInternal retrieves all commits using go-git Log.
// Does not populate FilesChanged for performance.
func (c *GitClient) getAllCommitsInternal(limit int) ([]*CommitInfo, error) {
	if !c.hasHead() {
		return []*CommitInfo{}, nil
	}

	iter, err := c.repo.Log(&gogit.LogOptions{
		Order: gogit.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var commits []*CommitInfo
	for {
		if limit > 0 && len(commits) >= limit {
			break
		}
		commit, nextErr := iter.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		commits = append(commits, convertCommitToInfoLight(commit))
	}

	return commits, nil
}

// =============================================================================
// GetUncommittedFiles
// =============================================================================

// GetUncommittedFiles returns files with uncommitted changes.
// This includes both staged and unstaged modifications.
func (c *GitClient) GetUncommittedFiles() ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getUncommittedFilesInternal()
}

// getUncommittedFilesInternal gets uncommitted files via go-git Worktree.Status().
func (c *GitClient) getUncommittedFilesInternal() ([]string, error) {
	wt, err := c.repo.Worktree()
	if err != nil {
		return nil, err
	}

	status, err := wt.Status()
	if err != nil {
		return nil, err
	}

	files := make([]string, 0, len(status))
	for path, fs := range status {
		if fs.Worktree != gogit.Unmodified || fs.Staging != gogit.Unmodified {
			files = append(files, path)
		}
	}

	sort.Strings(files)
	return files, nil
}

// =============================================================================
// GetUntrackedFiles
// =============================================================================

// GetUntrackedFiles returns untracked files.
// Ignored files are excluded.
func (c *GitClient) GetUntrackedFiles() ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.getUntrackedFilesInternal()
}

// getUntrackedFilesInternal gets untracked files via go-git Worktree.Status().
func (c *GitClient) getUntrackedFilesInternal() ([]string, error) {
	wt, err := c.repo.Worktree()
	if err != nil {
		return nil, err
	}

	status, err := wt.Status()
	if err != nil {
		return nil, err
	}

	files := make([]string, 0)
	for path, fs := range status {
		if fs.Worktree == gogit.Untracked {
			files = append(files, path)
		}
	}

	sort.Strings(files)
	return files, nil
}

// =============================================================================
// Helpers
// =============================================================================

// resolveCommit resolves a commit reference (full hash, short hash, or symbolic
// ref) to a go-git Commit object.
func (c *GitClient) resolveCommit(ref string) (*object.Commit, error) {
	hash, err := c.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, ErrInvalidCommit
	}

	commit, err := c.repo.CommitObject(*hash)
	if err != nil {
		return nil, ErrInvalidCommit
	}

	return commit, nil
}

// pathsFromChanges extracts file paths from a set of tree changes.
// For modifications and additions the destination path is used;
// for deletions the source path is used.
func pathsFromChanges(changes object.Changes) []string {
	paths := make([]string, 0, len(changes))
	for _, ch := range changes {
		name := changePath(ch)
		if name != "" {
			paths = append(paths, name)
		}
	}
	return paths
}

// changePath returns the effective path of a change.
func changePath(ch *object.Change) string {
	if ch.To.Name != "" {
		return ch.To.Name
	}
	return ch.From.Name
}

// convertCommitToInfoLight converts a go-git Commit to CommitInfo without
// populating FilesChanged. Used by bulk listing operations where per-commit
// file stats are not needed.
func convertCommitToInfoLight(commit *object.Commit) *CommitInfo {
	return &CommitInfo{
		Hash:           commit.Hash.String(),
		ShortHash:      commit.Hash.String()[:7],
		Author:         commit.Author.Name,
		AuthorEmail:    commit.Author.Email,
		AuthorTime:     commit.Author.When,
		Committer:      commit.Committer.Name,
		CommitterEmail: commit.Committer.Email,
		CommitTime:     commit.Committer.When,
		Message:        commit.Message,
		Subject:        extractSubject(commit.Message),
		ParentHashes:   extractParentHashes(commit),
	}
}
