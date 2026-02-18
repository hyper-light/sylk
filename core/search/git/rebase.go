package git

import (
	"errors"
	"fmt"
	"io"
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
//  3. Reverse them to replay order (oldest first)
//  4. Detach HEAD at the onto commit
//  5. For each commit, perform a cherry-pick-style 3-way merge:
//     - base = commit's first parent tree
//     - ours = current rebase HEAD tree
//     - theirs = commit's tree
//  6. Create new commits preserving original authorship
//  7. Update the original branch ref to the final rebased commit
//  8. Re-attach HEAD to the original branch
//
// On conflict at any step, the conflicting files are written to the
// worktree and ErrRebaseConflict is returned. The branch ref is NOT
// updated on conflict — the worktree is left in a detached state for
// manual resolution.
func (c *GitClient) Rebase(ontoBranch string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	// Resolve current HEAD.
	headRef, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}

	originalBranch := headRef.Name()
	headCommit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return err
	}

	// Resolve onto branch.
	ontoRefName := plumbing.NewBranchReferenceName(ontoBranch)
	ontoHash, err := c.repo.ResolveRevision(plumbing.Revision(ontoRefName))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", ontoBranch, err)
	}

	ontoCommit, err := c.repo.CommitObject(*ontoHash)
	if err != nil {
		return err
	}

	// Find merge base.
	bases, err := headCommit.MergeBase(ontoCommit)
	if err != nil {
		return err
	}
	if len(bases) == 0 {
		return fmt.Errorf("no common ancestor between HEAD and %s", ontoBranch)
	}

	mergeBase := bases[0]

	// Already up to date: HEAD is ancestor of onto (nothing to replay).
	isAncestor, err := headCommit.IsAncestor(ontoCommit)
	if err != nil {
		return err
	}
	if isAncestor {
		// Fast-forward HEAD to onto.
		wt, err := c.repo.Worktree()
		if err != nil {
			return err
		}
		ref := plumbing.NewHashReference(originalBranch, ontoCommit.Hash)
		if err := c.repo.Storer.SetReference(ref); err != nil {
			return err
		}
		return wt.Reset(&gogit.ResetOptions{
			Commit: ontoCommit.Hash,
			Mode:   gogit.HardReset,
		})
	}

	// Collect commits to replay: walk from HEAD back to merge base.
	toReplay, err := c.collectReplayCommits(headCommit, mergeBase)
	if err != nil {
		return err
	}

	if len(toReplay) == 0 {
		return nil // Nothing to replay.
	}

	// Replay commits onto the onto tip.
	currentHash := ontoCommit.Hash

	for _, commit := range toReplay {
		newHash, err := c.replayCommit(commit, currentHash)
		if err != nil {
			if errors.Is(err, ErrRebaseConflict) {
				// Leave worktree in conflicted state. Don't update branch.
				return err
			}
			return fmt.Errorf("replay %s: %w", commit.Hash.String()[:7], err)
		}
		currentHash = newHash
	}

	// Update original branch ref to the final rebased commit.
	ref := plumbing.NewHashReference(originalBranch, currentHash)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return err
	}

	// Re-attach HEAD to the original branch.
	symRef := plumbing.NewSymbolicReference(plumbing.HEAD, originalBranch)
	if err := c.repo.Storer.SetReference(symRef); err != nil {
		return err
	}

	// Reset worktree to final state.
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&gogit.ResetOptions{
		Commit: currentHash,
		Mode:   gogit.HardReset,
	})
}

// collectReplayCommits walks from head back to (not including) base,
// returns the commits in replay order (oldest first).
func (c *GitClient) collectReplayCommits(head, base *object.Commit) ([]*object.Commit, error) {
	iter, err := c.repo.Log(&gogit.LogOptions{
		From:  head.Hash,
		Order: gogit.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var commits []*object.Commit
	err = iter.ForEach(func(commit *object.Commit) error {
		if commit.Hash == base.Hash {
			return io.EOF // Stop — don't include the base.
		}
		commits = append(commits, commit)
		return nil
	})
	if err != nil && err != io.EOF {
		return nil, err
	}

	// Reverse to replay order (oldest first).
	reverseCommits(commits)
	return commits, nil
}

// replayCommit applies a single commit's changes onto parentHash,
// creating a new commit. Returns the new commit hash.
func (c *GitClient) replayCommit(commit *object.Commit, parentHash plumbing.Hash) (plumbing.Hash, error) {
	// Get the commit's first parent tree (merge base for this cherry-pick).
	var baseTree *object.Tree
	if len(commit.ParentHashes) > 0 {
		parent, err := c.repo.CommitObject(commit.ParentHashes[0])
		if err != nil {
			return plumbing.ZeroHash, err
		}
		baseTree, err = parent.Tree()
		if err != nil {
			return plumbing.ZeroHash, err
		}
	}

	commitTree, err := commit.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	// Get the current rebase HEAD tree (ours).
	parentCommit, err := c.repo.CommitObject(parentHash)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	oursTree, err := parentCommit.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	// 3-way merge.
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

	// Create new commit preserving original author.
	now := time.Now()
	committer := defaultSignature(now)

	return storeCommitObj(
		c.repo.Storer, result.TreeHash,
		[]plumbing.Hash{parentHash},
		commit.Author, committer,
		commit.Message,
	)
}

// reverseCommits reverses a slice of commits in place.
func reverseCommits(s []*object.Commit) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
