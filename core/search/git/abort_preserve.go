package git

import (
	"fmt"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// AbortPreservation holds the result of pre-abort preservation:
// a backup branch ref and the list of stashed file paths.
type AbortPreservation struct {
	BackupBranch string   // e.g. "refs/sylk/abort/feature-rebase-1708387200"
	StashedPaths []string
}

// PreserveBeforeAbort saves the current state before aborting a sequencer
// operation. Creates a backup ref pointing at HEAD and stashes any dirty
// tracked files.
// Caller must hold NO lock — acquires Lock internally.
func (c *GitClient) PreserveBeforeAbort(opName string) (*AbortPreservation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	result := &AbortPreservation{}

	// Create backup ref at current HEAD.
	head, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}

	now := time.Now()
	refName := fmt.Sprintf("%s%s-%d", abortRefPrefix, opName, now.UnixNano())
	ref := plumbing.NewHashReference(plumbing.ReferenceName(refName), head.Hash())
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return nil, fmt.Errorf("create abort backup ref: %w", err)
	}
	result.BackupBranch = refName

	// Auto-stash dirty files.
	paths, err := c.autoStash()
	if err != nil {
		// Stash failure is non-fatal; the backup ref was created.
		return result, nil
	}
	result.StashedPaths = paths

	return result, nil
}
