package git

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SequencerFileState describes the current state of an in-progress git
// sequencer operation (rebase, merge, cherry-pick) based on filesystem
// markers in .git/. Independent from the in-memory SequencerStatus.
type SequencerFileState struct {
	Active        bool
	Type          string // "rebase", "merge", "cherry-pick"
	CurrentStep   int
	TotalSteps    int
	OntoRef       string   // target branch (rebase)
	OriginalRef   string   // source branch
	ConflictPaths []string // from worktree status
}

// DetectSequencerFileState reads .git/ filesystem markers to detect
// active rebase, merge, or cherry-pick operations.
// Returns a non-nil result with Active=false when no operation is active.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) DetectSequencerFileState() *SequencerFileState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return &SequencerFileState{}
	}

	gitDir, err := resolveGitDir(c)
	if err != nil {
		return &SequencerFileState{}
	}

	// Check rebase-merge (interactive rebase).
	if state := detectRebaseMerge(gitDir); state != nil {
		return state
	}

	// Check rebase-apply (am-style rebase).
	if state := detectRebaseApply(gitDir); state != nil {
		return state
	}

	// Check MERGE_HEAD (merge in progress).
	if state := detectMergeHead(gitDir); state != nil {
		return state
	}

	// Check CHERRY_PICK_HEAD (cherry-pick in progress).
	if state := detectCherryPickHead(gitDir); state != nil {
		return state
	}

	return &SequencerFileState{}
}

// detectRebaseMerge checks for .git/rebase-merge/ (interactive rebase).
func detectRebaseMerge(gitDir string) *SequencerFileState {
	dir := filepath.Join(gitDir, "rebase-merge")
	if !isDir(dir) {
		return nil
	}

	state := &SequencerFileState{
		Active: true,
		Type:   "rebase",
	}

	state.CurrentStep = readIntFile(filepath.Join(dir, "msgnum"))
	state.TotalSteps = readIntFile(filepath.Join(dir, "end"))
	state.OntoRef = readTrimmedFile(filepath.Join(dir, "onto-name"))
	state.OriginalRef = cleanBranchRef(readTrimmedFile(filepath.Join(dir, "head-name")))

	return state
}

// detectRebaseApply checks for .git/rebase-apply/ (am-style rebase).
func detectRebaseApply(gitDir string) *SequencerFileState {
	dir := filepath.Join(gitDir, "rebase-apply")
	if !isDir(dir) {
		return nil
	}

	state := &SequencerFileState{
		Active: true,
		Type:   "rebase",
	}

	state.CurrentStep = readIntFile(filepath.Join(dir, "next"))
	state.TotalSteps = readIntFile(filepath.Join(dir, "last"))
	state.OriginalRef = cleanBranchRef(readTrimmedFile(filepath.Join(dir, "head-name")))

	return state
}

// detectMergeHead checks for .git/MERGE_HEAD (merge in progress).
func detectMergeHead(gitDir string) *SequencerFileState {
	path := filepath.Join(gitDir, "MERGE_HEAD")
	if _, err := os.Stat(path); err != nil {
		return nil
	}

	return &SequencerFileState{
		Active:     true,
		Type:       "merge",
		TotalSteps: 1,
	}
}

// detectCherryPickHead checks for .git/CHERRY_PICK_HEAD.
func detectCherryPickHead(gitDir string) *SequencerFileState {
	path := filepath.Join(gitDir, "CHERRY_PICK_HEAD")
	if _, err := os.Stat(path); err != nil {
		return nil
	}

	return &SequencerFileState{
		Active:     true,
		Type:       "cherry-pick",
		TotalSteps: 1,
	}
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// readIntFile reads a file, trims whitespace, and parses as int.
// Returns 0 on any error.
func readIntFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

// readTrimmedFile reads a file and returns its trimmed content.
// Returns "" on any error.
func readTrimmedFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// cleanBranchRef strips the "refs/heads/" prefix from a branch ref.
func cleanBranchRef(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}
