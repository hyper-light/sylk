package git

import (
	"errors"
	"fmt"
	"slices"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrRebaseConflict indicates the rebase produced conflicts.
var ErrRebaseConflict = errors.New("rebase conflict")

// Rebase replays commits from the current branch onto the tip of ontoBranch.
//
// Algorithm:
//  1. Find the merge base between HEAD and onto
//  2. Collect commits from HEAD back to (not including) the merge base
//     via first-parent chain walk (not log iteration)
//  3. Reverse them to replay order (oldest first)
//  4. For each commit, cherry-pick onto the current tip
//  5. Update the original branch ref to the final rebased commit
//  6. Re-attach HEAD to the original branch
//
// On conflict at any step, the conflicting files are written to the
// worktree and ErrRebaseConflict is returned. The branch ref is NOT
// updated on conflict.
func (c *GitClient) Rebase(ontoBranch string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	headRef, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}

	headCommit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return err
	}

	ontoCommit, err := c.resolveOnto(ontoBranch)
	if err != nil {
		return err
	}

	mergeBase, err := findMergeBase(headCommit, ontoCommit, ontoBranch)
	if err != nil {
		return err
	}

	// HEAD is ancestor of onto → fast-forward.
	return c.rebaseFromBase(headRef.Name(), headCommit, ontoCommit, mergeBase)
}

// resolveOnto resolves the onto branch name to a commit object.
func (c *GitClient) resolveOnto(ontoBranch string) (*object.Commit, error) {
	ontoRefName := plumbing.NewBranchReferenceName(ontoBranch)
	ontoHash, err := c.repo.ResolveRevision(plumbing.Revision(ontoRefName))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", ontoBranch, err)
	}
	return c.repo.CommitObject(*ontoHash)
}

// findMergeBase returns the first merge base between head and onto.
func findMergeBase(head, onto *object.Commit, ontoBranch string) (*object.Commit, error) {
	bases, err := head.MergeBase(onto)
	if err != nil {
		return nil, err
	}
	if len(bases) == 0 {
		return nil, fmt.Errorf("no common ancestor between HEAD and %s", ontoBranch)
	}
	return bases[0], nil
}

// rebaseFromBase performs the rebase given the resolved commits and merge base.
func (c *GitClient) rebaseFromBase(originalBranch plumbing.ReferenceName, head, onto, mergeBase *object.Commit) error {
	isAncestor, err := head.IsAncestor(onto)
	if err != nil {
		return err
	}
	if isAncestor {
		return c.fastForwardBranch(originalBranch, onto.Hash)
	}

	toReplay, err := c.collectReplayCommits(head, mergeBase)
	if err != nil {
		return err
	}
	if len(toReplay) == 0 {
		return nil
	}

	finalHash, err := c.replayAll(toReplay, onto.Hash)
	if err != nil {
		return err
	}

	return c.finishRebase(originalBranch, finalHash)
}

// fastForwardBranch updates the branch ref and resets the worktree.
func (c *GitClient) fastForwardBranch(branch plumbing.ReferenceName, hash plumbing.Hash) error {
	ref := plumbing.NewHashReference(branch, hash)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return err
	}
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&gogit.ResetOptions{
		Commit: hash,
		Mode:   gogit.HardReset,
	})
}

// collectReplayCommits walks the first-parent chain from head back to
// (not including) base and returns the commits in replay order (oldest first).
//
// Uses explicit first-parent traversal instead of repo.Log to avoid
// collecting commits from merged branches in non-linear histories.
func (c *GitClient) collectReplayCommits(head, base *object.Commit) ([]*object.Commit, error) {
	var commits []*object.Commit
	current := head

	for current.Hash != base.Hash {
		commits = append(commits, current)
		if len(current.ParentHashes) == 0 {
			return nil, fmt.Errorf("merge base %s not reachable via first-parent chain", base.Hash.String()[:7])
		}
		parent, err := c.repo.CommitObject(current.ParentHashes[0])
		if err != nil {
			return nil, err
		}
		current = parent
	}

	slices.Reverse(commits)
	return commits, nil
}

// replayAll replays a slice of commits onto currentHash sequentially.
// Returns the final commit hash after all replays.
func (c *GitClient) replayAll(commits []*object.Commit, currentHash plumbing.Hash) (plumbing.Hash, error) {
	for _, commit := range commits {
		newHash, err := c.replayCommit(commit, currentHash)
		if err != nil {
			if errors.Is(err, ErrRebaseConflict) {
				return plumbing.ZeroHash, err
			}
			return plumbing.ZeroHash, fmt.Errorf("replay %s: %w", commit.Hash.String()[:7], err)
		}
		currentHash = newHash
	}
	return currentHash, nil
}

// replayCommit applies a single commit's changes onto parentHash,
// creating a new commit. Returns the new commit hash.
func (c *GitClient) replayCommit(commit *object.Commit, parentHash plumbing.Hash) (plumbing.Hash, error) {
	baseTree, err := commitParentTree(c, commit)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	commitTree, err := commit.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	parentCommit, err := c.repo.CommitObject(parentHash)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	oursTree, err := parentCommit.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	result, err := treeMerge3(c.repo.Storer, baseTree, oursTree, commitTree)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	if result.HasConflicts() {
		if err := c.writeConflictsToWorktree(result.Conflicts); err != nil {
			return plumbing.ZeroHash, err
		}
		return plumbing.ZeroHash, ErrRebaseConflict
	}

	committer := defaultSignature(time.Now())
	return storeCommitObj(
		c.repo.Storer, result.TreeHash,
		[]plumbing.Hash{parentHash},
		commit.Author, committer,
		commit.Message,
	)
}

// commitParentTree returns the tree of a commit's first parent.
// Returns nil for root commits (no parents).
func commitParentTree(c *GitClient, commit *object.Commit) (*object.Tree, error) {
	if len(commit.ParentHashes) == 0 {
		return nil, nil
	}
	parent, err := c.repo.CommitObject(commit.ParentHashes[0])
	if err != nil {
		return nil, err
	}
	return parent.Tree()
}

// finishRebase updates the branch ref and re-attaches HEAD after a
// successful rebase.
func (c *GitClient) finishRebase(originalBranch plumbing.ReferenceName, finalHash plumbing.Hash) error {
	ref := plumbing.NewHashReference(originalBranch, finalHash)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return err
	}

	symRef := plumbing.NewSymbolicReference(plumbing.HEAD, originalBranch)
	if err := c.repo.Storer.SetReference(symRef); err != nil {
		return err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&gogit.ResetOptions{
		Commit: finalHash,
		Mode:   gogit.HardReset,
	})
}
