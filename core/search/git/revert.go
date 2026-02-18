package git

import (
	"errors"
	"fmt"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrRevertConflict indicates the revert produced merge conflicts.
var ErrRevertConflict = errors.New("revert conflict")

// Revert creates a new commit that undoes the changes introduced by the given
// commit. Algorithmically, revert is an inverted cherry-pick (3-way merge):
//
//   - base  = the commit's tree  (what we're undoing)
//   - ours  = current HEAD tree
//   - theirs = the commit's parent tree (what we're reverting to)
//
// On conflict, conflict-marked files are written to the worktree and
// ErrRevertConflict is returned.
func (c *GitClient) Revert(commitHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	revertCommit, err := c.resolveCommit(commitHash)
	if err != nil {
		return fmt.Errorf("resolve commit: %w", err)
	}

	result, err := c.revertMerge(revertCommit)
	if err != nil {
		return err
	}

	if result.HasConflicts() {
		if err := c.writeConflictsToWorktree(result.Conflicts); err != nil {
			return err
		}
		return ErrRevertConflict
	}

	return c.commitRevert(revertCommit, result.TreeHash)
}

// revertMerge performs the inverted 3-way merge for a revert.
func (c *GitClient) revertMerge(revertCommit *object.Commit) (*TreeMergeResult, error) {
	// base = the commit's tree (what we're undoing).
	baseTree, err := revertCommit.Tree()
	if err != nil {
		return nil, err
	}

	// theirs = the commit's parent tree (what we're reverting to).
	theirsTree, err := commitParentTree(c, revertCommit)
	if err != nil {
		return nil, err
	}

	// ours = HEAD tree.
	headRef, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}

	headCommit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, err
	}

	oursTree, err := headCommit.Tree()
	if err != nil {
		return nil, err
	}

	return treeMerge3(c.repo.Storer, baseTree, oursTree, theirsTree)
}

// commitRevert creates the revert commit and updates HEAD.
func (c *GitClient) commitRevert(revertCommit *object.Commit, treeHash plumbing.Hash) error {
	headRef, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}

	now := time.Now()
	author := defaultSignature(now)
	committer := author

	subject := strings.SplitN(revertCommit.Message, "\n", 2)[0]
	msg := fmt.Sprintf("Revert %q\n\nReverts %s\n", subject, revertCommit.Hash)

	newHash, err := storeCommitObj(
		c.repo.Storer, treeHash,
		[]plumbing.Hash{headRef.Hash()},
		author, committer,
		msg,
	)
	if err != nil {
		return err
	}

	ref := plumbing.NewHashReference(headRef.Name(), newHash)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	return wt.Reset(&gogit.ResetOptions{
		Commit: newHash,
		Mode:   gogit.HardReset,
	})
}
