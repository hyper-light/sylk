package git

import (
	gogit "github.com/go-git/go-git/v5"
)

// isDirtyWorktree reports whether the worktree has uncommitted changes
// (excluding untracked files). Uses go-git's native status.
// Caller must hold at least c.mu.RLock().
func (c *GitClient) isDirtyWorktree() bool {
	wt, err := c.repo.Worktree()
	if err != nil {
		return false
	}

	status, err := wt.Status()
	if err != nil {
		return false
	}

	for _, s := range status {
		if hasStagedOrUnstagedChange(s) {
			return true
		}
	}

	return false
}

// hasStagedOrUnstagedChange returns true if a file status indicates a
// tracked change (not just untracked).
func hasStagedOrUnstagedChange(s *gogit.FileStatus) bool {
	// Untracked files (staging='?' worktree='?') are not considered dirty
	// for the purpose of auto-stash.
	if s.Staging == gogit.Untracked && s.Worktree == gogit.Untracked {
		return false
	}
	return s.Staging != gogit.Unmodified || s.Worktree != gogit.Unmodified
}

// isDirtyFromCachedStatus reports whether a cached StatusUpdate contains
// tracked files with staged or unstaged changes. Returns (dirty, ok) where
// ok indicates the cache was usable. When ok is false, the caller should
// fall back to isDirtyWorktree for a full worktree scan.
func isDirtyFromCachedStatus(su *StatusUpdate) (dirty bool, ok bool) {
	if su == nil {
		return false, false
	}
	for _, state := range su.StatusMap {
		if state != GitClean && state != GitIgnored && state != GitUntracked {
			return true, true
		}
	}
	return false, true
}

// autoStash stashes dirty tracked files before a destructive operation.
// Returns the paths that were stashed, or nil if the worktree was clean.
// Caller must hold c.mu.Lock().
func (c *GitClient) autoStash() ([]string, error) {
	wt, err := c.repo.Worktree()
	if err != nil {
		return nil, err
	}

	status, err := wt.Status()
	if err != nil {
		return nil, err
	}

	var paths []string
	for path, s := range status {
		if hasStagedOrUnstagedChange(s) {
			paths = append(paths, path)
		}
	}

	if len(paths) == 0 {
		return nil, nil
	}

	return paths, c.stashFilesImpl(paths)
}

// autoUnstash restores the most recent stash entry.
// Caller must hold c.mu.Lock().
func (c *GitClient) autoUnstash() error {
	return c.unstashFilesImpl()
}
