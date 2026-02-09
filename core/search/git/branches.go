package git

import (
	"io"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ListBranches returns metadata for all local branches.
// The current branch (HEAD) is marked with IsHead=true.
// Returns ErrNotGitRepo if not a git repository.
func (c *GitClient) ListBranches() ([]BranchInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	return c.listBranchesInternal()
}

// listBranchesInternal enumerates local branches without holding the lock.
func (c *GitClient) listBranchesInternal() ([]BranchInfo, error) {
	headRef, _ := c.repo.Head()
	headBranch := ""
	if headRef != nil {
		headBranch = headRef.Name().Short()
	}

	iter, err := c.repo.Branches()
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var branches []BranchInfo
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		info := branchInfoFromRef(c, ref, headBranch)
		branches = append(branches, info)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return branches, nil
}

// branchInfoFromRef builds a BranchInfo from a branch reference.
func branchInfoFromRef(c *GitClient, ref *plumbing.Reference, headBranch string) BranchInfo {
	name := ref.Name().Short()
	hash := ref.Hash()

	info := BranchInfo{
		Name:      name,
		Hash:      hash.String(),
		ShortHash: hash.String()[:7],
		IsHead:    name == headBranch,
	}

	commit, err := c.repo.CommitObject(hash)
	if err == nil {
		info.Subject = extractSubject(commit.Message)
		info.AuthorTime = commit.Author.When
	}

	return info
}

// GetCommitStats returns the total additions and deletions for a commit.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrCommitNotFound if the commit does not exist.
func (c *GitClient) GetCommitStats(hash string) (additions, deletions int, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return 0, 0, ErrNotGitRepo
	}

	resolved, err := c.resolveCommitHash(hash)
	if err != nil {
		return 0, 0, err
	}

	commit, err := c.repo.CommitObject(resolved)
	if err != nil {
		return 0, 0, ErrCommitNotFound
	}

	stats, err := commit.Stats()
	if err != nil {
		return 0, 0, err
	}

	for _, s := range stats {
		additions += s.Addition
		deletions += s.Deletion
	}

	return additions, deletions, nil
}

// =============================================================================
// TreeCommit — lightweight commit data for the commit tree visualization
// =============================================================================

// TreeCommit holds the fields needed to render a commit tree node.
// Stats (additions/deletions) are omitted here; they are expensive to
// compute and should be loaded lazily for visible nodes.
type TreeCommit struct {
	Hash         string
	ShortHash    string
	Subject      string
	Author       string
	AuthorTime   time.Time
	ParentHashes []string
	IsMerge      bool
	Branch       string // Non-empty when a local branch tip points at this commit.
}

// ListCommitsForTree returns commits for the tree visualization using the
// go-git native API. Each commit is annotated with its branch name when the
// commit is a branch tip. Results are ordered from newest to oldest.
// Limit ≤ 0 means return all commits.
// Returns ErrNotGitRepo if not a git repository.
func (c *GitClient) ListCommitsForTree(limit int) ([]TreeCommit, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	// Build branch-tip-to-name map so we can annotate commits.
	branchMap, err := c.buildBranchMap()
	if err != nil {
		return nil, err
	}

	iter, err := c.repo.Log(&gogit.LogOptions{
		Order: gogit.LogOrderCommitterTime,
		All:   true,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var commits []TreeCommit
	err = iter.ForEach(func(co *object.Commit) error {
		if limit > 0 && len(commits) >= limit {
			return io.EOF
		}
		tc := treeCommitFromObject(co, branchMap)
		commits = append(commits, tc)
		return nil
	})
	if err != nil && err != io.EOF {
		return nil, err
	}

	return commits, nil
}

// ListCommitsForBranch returns commits reachable from a single named branch.
// Results are ordered newest-first. Limit ≤ 0 means return all.
// Returns ErrNotGitRepo if not a git repository.
func (c *GitClient) ListCommitsForBranch(branchName string, limit int) ([]TreeCommit, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	ref, err := c.repo.Reference(
		plumbing.NewBranchReferenceName(branchName), true,
	)
	if err != nil {
		return nil, err
	}

	branchMap, err := c.buildBranchMap()
	if err != nil {
		return nil, err
	}

	iter, err := c.repo.Log(&gogit.LogOptions{
		From:  ref.Hash(),
		Order: gogit.LogOrderCommitterTime,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var commits []TreeCommit
	err = iter.ForEach(func(co *object.Commit) error {
		if limit > 0 && len(commits) >= limit {
			return io.EOF
		}
		commits = append(commits, treeCommitFromObject(co, branchMap))
		return nil
	})
	if err != nil && err != io.EOF {
		return nil, err
	}

	return commits, nil
}

// buildBranchMap returns a map from full commit hash to branch short name.
// When multiple branches share a tip, the first one wins (non-deterministic
// but acceptable — the UI only needs a label, not an exhaustive list).
func (c *GitClient) buildBranchMap() (map[string]string, error) {
	iter, err := c.repo.Branches()
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	m := make(map[string]string)
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		m[ref.Hash().String()] = ref.Name().Short()
		return nil
	})
	return m, nil
}

// treeCommitFromObject converts a go-git Commit object to a TreeCommit.
func treeCommitFromObject(co *object.Commit, branchMap map[string]string) TreeCommit {
	hash := co.Hash.String()
	parents := make([]string, len(co.ParentHashes))
	for i, ph := range co.ParentHashes {
		parents[i] = ph.String()
	}
	return TreeCommit{
		Hash:         hash,
		ShortHash:    hash[:7],
		Subject:      extractSubject(co.Message),
		Author:       co.Author.Name,
		AuthorTime:   co.Author.When,
		ParentHashes: parents,
		IsMerge:      len(co.ParentHashes) > 1,
		Branch:       branchMap[hash],
	}
}
