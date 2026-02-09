package git

import (
	"path/filepath"

	gogit "github.com/go-git/go-git/v5"
)

// =============================================================================
// GitFileState
// =============================================================================

// GitFileState represents the visual decoration category of a file in the
// working tree. Values are ordered by display priority: higher values take
// precedence during directory propagation.
type GitFileState int

const (
	// GitClean means no git decoration is applied.
	GitClean GitFileState = iota

	// GitIgnored means the file matches a .gitignore rule (muted text).
	GitIgnored

	// GitUntracked means the file is new and not yet tracked (green + "U").
	GitUntracked

	// GitAdded means the file is staged as a new addition (teal + "A").
	GitAdded

	// GitModified means the file has staged or unstaged changes (yellow + "M").
	GitModified

	// GitDeleted means the file has been deleted or staged for deletion (peach + "D").
	GitDeleted

	// GitConflict means the file has unmerged conflict markers (red + "!").
	GitConflict
)

// =============================================================================
// WorkingTreeStatus
// =============================================================================

// WorkingTreeStatus holds categorized file paths from git status output.
// All paths are relative to the repository root.
type WorkingTreeStatus struct {
	Modified  []string // Staged or unstaged modifications.
	Added     []string // Staged additions (A in index).
	Deleted   []string // Deleted in worktree or staged for deletion.
	Untracked []string // Untracked files/directories.
	Conflict  []string // Unmerged conflict entries.
}

// =============================================================================
// StatusUpdate
// =============================================================================

// StatusUpdate carries a complete git status snapshot: the visual state map,
// the set of tracked file paths (from the index), and the set of directories
// that contain tracked files.
type StatusUpdate struct {
	StatusMap   map[string]GitFileState
	TrackedSet  map[string]struct{}
	TrackedDirs map[string]struct{}
}

// =============================================================================
// WorktreeStatus (go-git native)
// =============================================================================

// WorktreeStatus queries the repository's working tree status using go-git's
// native API (no exec). Returns a categorized WorkingTreeStatus suitable for
// BuildStatusMap.
func (c *GitClient) WorktreeStatus() (*WorkingTreeStatus, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo || c.repo == nil {
		return nil, ErrNotGitRepo
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return nil, err
	}

	goStatus, err := wt.Status()
	if err != nil {
		return nil, err
	}

	return classifyGoGitStatus(goStatus), nil
}

// =============================================================================
// TrackedSet (from git index)
// =============================================================================

// TrackedSet returns the set of all tracked file paths from the git index.
// This is used to distinguish ignored files (not in status map AND not in
// index) from clean tracked files.
func (c *GitClient) TrackedSet() map[string]struct{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo || c.repo == nil {
		return nil
	}

	idx, err := c.repo.Storer.Index()
	if err != nil {
		return nil
	}

	set := make(map[string]struct{}, len(idx.Entries))
	for _, e := range idx.Entries {
		set[e.Name] = struct{}{}
	}
	return set
}

// =============================================================================
// Classification
// =============================================================================

// classifyGoGitStatus converts go-git's native Status map into our
// categorized WorkingTreeStatus.
func classifyGoGitStatus(s gogit.Status) *WorkingTreeStatus {
	result := &WorkingTreeStatus{
		Modified:  make([]string, 0, len(s)),
		Added:     make([]string, 0),
		Deleted:   make([]string, 0),
		Untracked: make([]string, 0),
		Conflict:  make([]string, 0),
	}

	for path, fs := range s {
		classifyFileStatus(path, fs, result)
	}
	return result
}

// classifyFileStatus categorizes a single go-git FileStatus entry into the
// appropriate WorkingTreeStatus bucket.
func classifyFileStatus(path string, fs *gogit.FileStatus, result *WorkingTreeStatus) {
	s, w := fs.Staging, fs.Worktree

	switch {
	case s == gogit.UpdatedButUnmerged || w == gogit.UpdatedButUnmerged:
		result.Conflict = append(result.Conflict, path)

	case s == gogit.Renamed:
		classifyRename(path, fs, result)

	case s == gogit.Copied:
		classifyCopy(path, w, result)

	case s == gogit.Deleted || w == gogit.Deleted:
		result.Deleted = append(result.Deleted, path)

	case w == gogit.Untracked:
		result.Untracked = append(result.Untracked, path)

	case s == gogit.Added:
		result.Added = append(result.Added, path)

	default:
		result.Modified = append(result.Modified, path)
	}
}

// classifyRename handles a file that was renamed in the index.
// The new path is treated as modified (or deleted if worktree-deleted).
// The original path (in fs.Extra) is marked as deleted.
func classifyRename(path string, fs *gogit.FileStatus, result *WorkingTreeStatus) {
	if fs.Worktree == gogit.Deleted {
		result.Deleted = append(result.Deleted, path)
	} else {
		result.Modified = append(result.Modified, path)
	}
	if fs.Extra != "" {
		result.Deleted = append(result.Deleted, fs.Extra)
	}
}

// classifyCopy handles a file that was copied in the index.
// The new path is treated as modified (or deleted if worktree-deleted).
// The original path is NOT marked as deleted — it still exists.
func classifyCopy(path string, worktree gogit.StatusCode, result *WorkingTreeStatus) {
	if worktree == gogit.Deleted {
		result.Deleted = append(result.Deleted, path)
	} else {
		result.Modified = append(result.Modified, path)
	}
}

// =============================================================================
// BuildStatusMap
// =============================================================================

// BuildStatusMap constructs a lookup map from relative paths to their visual
// git state. Each file is marked, and the status is propagated upward to all
// ancestor directories using highest-priority-wins semantics.
func BuildStatusMap(wts *WorkingTreeStatus) map[string]GitFileState {
	total := len(wts.Untracked) + len(wts.Added) +
		len(wts.Modified) + len(wts.Deleted) + len(wts.Conflict)
	m := make(map[string]GitFileState, total*2) // room for ancestors

	// Process in ascending priority order so higher states overwrite lower.
	for _, p := range wts.Untracked {
		propagatePath(m, p, GitUntracked)
	}
	for _, p := range wts.Added {
		propagatePath(m, p, GitAdded)
	}
	for _, p := range wts.Modified {
		propagatePath(m, p, GitModified)
	}
	for _, p := range wts.Deleted {
		propagatePath(m, p, GitDeleted)
	}
	for _, p := range wts.Conflict {
		propagatePath(m, p, GitConflict)
	}

	return m
}

// propagatePath marks a file and all its ancestor directories with the given
// state. Existing entries are only upgraded, never downgraded.
func propagatePath(m map[string]GitFileState, path string, state GitFileState) {
	if m[path] < state {
		m[path] = state
	}

	for dir := filepath.Dir(path); dir != "." && dir != ""; dir = filepath.Dir(dir) {
		if m[dir] >= state {
			break // all ancestors already at this priority or higher
		}
		m[dir] = state
	}
}

// =============================================================================
// UncommittedFileStatuses
// =============================================================================

// UncommittedFileStatuses returns all files with uncommitted changes,
// mapped to their single-character status code (M, A, D, ?, !).
// Uses the go-git native API (no exec).
func (c *GitClient) UncommittedFileStatuses() (map[string]string, error) {
	wts, err := c.WorktreeStatus()
	if err != nil {
		return nil, err
	}
	return flattenWorkingTreeStatus(wts), nil
}

// flattenWorkingTreeStatus converts categorized file lists into a flat
// map from path to single-character status code.
func flattenWorkingTreeStatus(wts *WorkingTreeStatus) map[string]string {
	total := len(wts.Modified) + len(wts.Added) + len(wts.Deleted) +
		len(wts.Untracked) + len(wts.Conflict)
	m := make(map[string]string, total)

	for _, p := range wts.Modified {
		m[p] = "M"
	}
	for _, p := range wts.Added {
		m[p] = "A"
	}
	for _, p := range wts.Deleted {
		m[p] = "D"
	}
	for _, p := range wts.Untracked {
		m[p] = "?"
	}
	for _, p := range wts.Conflict {
		m[p] = "!"
	}

	return m
}

// =============================================================================
// BuildTrackedDirs
// =============================================================================

// BuildTrackedDirs derives the set of all ancestor directories that contain
// at least one tracked file. Used to detect ignored directories (directory
// not in TrackedDirs and not in StatusMap → ignored).
func BuildTrackedDirs(tracked map[string]struct{}) map[string]struct{} {
	dirs := make(map[string]struct{}, len(tracked))
	for path := range tracked {
		for dir := filepath.Dir(path); dir != "." && dir != ""; dir = filepath.Dir(dir) {
			if _, ok := dirs[dir]; ok {
				break // all ancestors already recorded
			}
			dirs[dir] = struct{}{}
		}
	}
	return dirs
}
