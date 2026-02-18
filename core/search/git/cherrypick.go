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

	// Resolve the commit to cherry-pick.
	pickCommit, err := c.resolveCommit(commitHash)
	if err != nil {
		return fmt.Errorf("resolve commit: %w", err)
	}

	// Get the commit's first parent as the merge base.
	var baseTree *object.Tree
	if len(pickCommit.ParentHashes) > 0 {
		parent, err := c.repo.CommitObject(pickCommit.ParentHashes[0])
		if err != nil {
			return err
		}
		baseTree, err = parent.Tree()
		if err != nil {
			return err
		}
	}
	// If no parents (root commit), baseTree stays nil → empty tree.

	pickTree, err := pickCommit.Tree()
	if err != nil {
		return err
	}

	// Get current HEAD.
	headRef, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}

	headCommit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return err
	}

	headTree, err := headCommit.Tree()
	if err != nil {
		return err
	}

	// 3-way merge: base=parent, ours=HEAD, theirs=picked commit.
	result, err := treeMerge3(c.repo.Storer, baseTree, headTree, pickTree)
	if err != nil {
		return err
	}

	if result.HasConflicts() {
		if err := c.writeConflictsToWorktree(result.Conflicts); err != nil {
			return err
		}
		return ErrCherryPickConflict
	}

	// Create new commit preserving original authorship.
	now := time.Now()
	committer := defaultSignature(now)

	newHash, err := storeCommitObj(
		c.repo.Storer, result.TreeHash,
		[]plumbing.Hash{headRef.Hash()},
		pickCommit.Author, committer,
		pickCommit.Message,
	)
	if err != nil {
		return err
	}

	// Update HEAD.
	headName := headRef.Name()
	ref := plumbing.NewHashReference(headName, newHash)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return err
	}

	// Reset worktree to new commit.
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	return wt.Reset(&gogit.ResetOptions{
		Commit: newHash,
		Mode:   gogit.HardReset,
	})
}
