package git

import (
	"errors"
	"fmt"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrCherryPickConflict indicates the cherry-pick produced conflicts.
var ErrCherryPickConflict = errors.New("cherry-pick conflict")

// CherryPick applies the changes introduced by the given commit onto HEAD.
//
// Algorithmically, cherry-pick is a specialized 3-way merge:
//   - base  = commit's first parent tree
//   - ours  = current HEAD tree
//   - theirs = the commit's tree
//
// On success, a new commit is created with:
//   - the original commit's author
//   - the current user as committer
//   - the original commit message
//   - HEAD as the sole parent
//
// On conflict, conflict-marked files are written to the worktree and
// ErrCherryPickConflict is returned.
func (c *GitClient) CherryPick(commitHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	pickCommit, err := c.resolveCommit(commitHash)
	if err != nil {
		return fmt.Errorf("resolve commit: %w", err)
	}

	result, err := c.cherryPickMerge(pickCommit)
	if err != nil {
		return err
	}

	if result.HasConflicts() {
		if err := c.writeConflictsToWorktree(result.Conflicts); err != nil {
			return err
		}
		return ErrCherryPickConflict
	}

	return c.commitCherryPick(pickCommit, result.TreeHash)
}

// cherryPickMerge performs the 3-way merge for a cherry-pick.
func (c *GitClient) cherryPickMerge(pickCommit *object.Commit) (*TreeMergeResult, error) {
	baseTree, err := commitParentTree(c, pickCommit)
	if err != nil {
		return nil, err
	}

	pickTree, err := pickCommit.Tree()
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

	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, err
	}

	return treeMerge3(c.repo.Storer, baseTree, headTree, pickTree)
}

// commitCherryPick creates the new commit and updates HEAD.
func (c *GitClient) commitCherryPick(pickCommit *object.Commit, treeHash plumbing.Hash) error {
	headRef, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}

	committer := defaultSignature(time.Now())
	newHash, err := storeCommitObj(
		c.repo.Storer, treeHash,
		[]plumbing.Hash{headRef.Hash()},
		pickCommit.Author, committer,
		pickCommit.Message,
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
