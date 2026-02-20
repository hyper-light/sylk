package git

import (
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ConflictPreviewResult holds the outcome of a dry-run merge simulation.
type ConflictPreviewResult struct {
	Conflicts []MergeConflict
	Clean     bool
}

// PreviewMerge performs a dry-run 3-way merge between source and target
// branches without touching the worktree. Returns the predicted conflicts.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) PreviewMerge(sourceBranch, targetBranch string) (*ConflictPreviewResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	sourceCommit, err := c.resolveBranchCommitInternal(sourceBranch)
	if err != nil {
		return nil, err
	}

	targetCommit, err := c.resolveBranchCommitInternal(targetBranch)
	if err != nil {
		return nil, err
	}

	// Check if already up-to-date or fast-forward.
	isAncestor, err := sourceCommit.IsAncestor(targetCommit)
	if err != nil {
		return nil, err
	}
	if isAncestor {
		return &ConflictPreviewResult{Clean: true}, nil
	}

	isFF, err := targetCommit.IsAncestor(sourceCommit)
	if err != nil {
		return nil, err
	}
	if isFF {
		return &ConflictPreviewResult{Clean: true}, nil
	}

	return c.previewTreeMerge(targetCommit, sourceCommit)
}

// PreviewRebase simulates rebasing the current branch onto the target branch.
// Iterates commits unique to the current branch and accumulates conflicts.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) PreviewRebase(ontoBranch string) (*ConflictPreviewResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	headRef, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}

	headCommit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, err
	}

	ontoCommit, err := c.resolveBranchCommitInternal(ontoBranch)
	if err != nil {
		return nil, err
	}

	// Find commits unique to current branch (not on onto).
	bases, err := headCommit.MergeBase(ontoCommit)
	if err != nil || len(bases) == 0 {
		return &ConflictPreviewResult{Clean: true}, nil
	}

	// Collect commits from HEAD to merge-base (first-parent walk).
	var commits []*object.Commit
	cur := headCommit
	for cur.Hash != bases[0].Hash {
		commits = append(commits, cur)
		if len(cur.ParentHashes) == 0 {
			break
		}
		parent, pErr := c.repo.CommitObject(cur.ParentHashes[0])
		if pErr != nil {
			break
		}
		cur = parent
	}

	// Simulate replaying each commit onto the target.
	var allConflicts []MergeConflict
	ontoTree, err := ontoCommit.Tree()
	if err != nil {
		return nil, err
	}

	// Replay commits in reverse order (oldest first).
	for i := len(commits) - 1; i >= 0; i-- {
		co := commits[i]
		parentTree, pErr := firstParentTree(c, co)
		if pErr != nil {
			continue
		}
		commitTree, cErr := co.Tree()
		if cErr != nil {
			continue
		}
		result, mErr := treeMerge3(c.repo.Storer, parentTree, ontoTree, commitTree)
		if mErr != nil {
			continue
		}
		allConflicts = append(allConflicts, result.Conflicts...)
		if !result.HasConflicts() {
			// Use the merged tree as new base for next commit.
			ontoTree, _ = object.GetTree(c.repo.Storer, result.TreeHash)
		}
	}

	return &ConflictPreviewResult{
		Conflicts: allConflicts,
		Clean:     len(allConflicts) == 0,
	}, nil
}

// PreviewCherryPick simulates cherry-picking the given commits onto
// the current HEAD without modifying the worktree.
// Caller must hold NO lock — acquires RLock internally.
func (c *GitClient) PreviewCherryPick(commitHashes []string) (*ConflictPreviewResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	headRef, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}

	headCommit, err := c.repo.CommitObject(headRef.Hash())
	if err != nil {
		return nil, err
	}

	currentTree, err := headCommit.Tree()
	if err != nil {
		return nil, err
	}

	var allConflicts []MergeConflict
	for _, hashStr := range commitHashes {
		hash := plumbing.NewHash(hashStr)
		co, cErr := c.repo.CommitObject(hash)
		if cErr != nil {
			continue
		}
		parentTree, pErr := firstParentTree(c, co)
		if pErr != nil {
			continue
		}
		commitTree, tErr := co.Tree()
		if tErr != nil {
			continue
		}
		result, mErr := treeMerge3(c.repo.Storer, parentTree, currentTree, commitTree)
		if mErr != nil {
			continue
		}
		allConflicts = append(allConflicts, result.Conflicts...)
		if !result.HasConflicts() {
			currentTree, _ = object.GetTree(c.repo.Storer, result.TreeHash)
		}
	}

	return &ConflictPreviewResult{
		Conflicts: allConflicts,
		Clean:     len(allConflicts) == 0,
	}, nil
}

// previewTreeMerge performs an in-memory 3-way merge between two commits.
func (c *GitClient) previewTreeMerge(oursCommit, theirsCommit *object.Commit) (*ConflictPreviewResult, error) {
	bases, err := oursCommit.MergeBase(theirsCommit)
	if err != nil || len(bases) == 0 {
		return &ConflictPreviewResult{Clean: true}, nil
	}

	baseTree, err := bases[0].Tree()
	if err != nil {
		return nil, err
	}

	oursTree, err := oursCommit.Tree()
	if err != nil {
		return nil, err
	}

	theirsTree, err := theirsCommit.Tree()
	if err != nil {
		return nil, err
	}

	result, err := treeMerge3(c.repo.Storer, baseTree, oursTree, theirsTree)
	if err != nil {
		return nil, err
	}

	return &ConflictPreviewResult{
		Conflicts: result.Conflicts,
		Clean:     !result.HasConflicts(),
	}, nil
}

// resolveBranchCommitInternal resolves a branch name to its tip commit.
// Caller must hold at least RLock.
func (c *GitClient) resolveBranchCommitInternal(branch string) (*object.Commit, error) {
	ref := plumbing.NewBranchReferenceName(branch)
	hash, err := c.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, err
	}
	return c.repo.CommitObject(*hash)
}
