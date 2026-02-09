package git

import (
	"io"
	"strings"
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

// =============================================================================
// DefaultBranch
// =============================================================================

// DefaultBranch returns the name of the repository's default branch.
// Detection strategy (first match wins):
//  1. refs/remotes/origin/HEAD symbolic reference target
//  2. Local branch named "main"
//  3. Local branch named "master"
//  4. The currently checked-out branch (HEAD)
//
// Returns an empty string if the repository has no branches.
func (c *GitClient) DefaultBranch() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo || c.repo == nil {
		return ""
	}

	// Strategy 1: origin/HEAD symbolic ref.
	if name := c.defaultFromOriginHead(); name != "" {
		return name
	}

	// Strategy 2+3: well-known names.
	if c.branchExists("main") {
		return "main"
	}
	if c.branchExists("master") {
		return "master"
	}

	// Strategy 4: current HEAD branch.
	ref, err := c.repo.Head()
	if err != nil {
		return ""
	}
	return ref.Name().Short()
}

// defaultFromOriginHead extracts the default branch from the origin/HEAD
// symbolic reference. Returns empty string if unavailable.
func (c *GitClient) defaultFromOriginHead() string {
	ref, err := c.repo.Reference(
		plumbing.ReferenceName("refs/remotes/origin/HEAD"), false,
	)
	if err != nil || ref.Type() != plumbing.SymbolicReference {
		return ""
	}
	target := ref.Target().String()
	return strings.TrimPrefix(target, "refs/remotes/origin/")
}

// branchExists checks whether a local branch with the given name exists.
func (c *GitClient) branchExists(name string) bool {
	_, err := c.repo.Reference(plumbing.NewBranchReferenceName(name), true)
	return err == nil
}

// =============================================================================
// Checkout
// =============================================================================

// CheckoutBranch switches the working tree to the named local branch.
// Uses a write lock because it mutates HEAD and the working tree.
// Returns ErrNotGitRepo if not a git repository.
func (c *GitClient) CheckoutBranch(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	return wt.Checkout(&gogit.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(name),
	})
}

// =============================================================================
// Delete
// =============================================================================

// DeleteBranch removes a local branch reference.
// Returns ErrDeleteCheckedOut if the branch is currently checked out.
// Returns ErrNotGitRepo if not a git repository.
func (c *GitClient) DeleteBranch(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	// Prevent deleting the currently checked-out branch.
	head, err := c.repo.Head()
	if err == nil && head.Name().Short() == name {
		return ErrDeleteCheckedOut
	}

	// Remove the branch reference.
	refName := plumbing.NewBranchReferenceName(name)
	if err := c.repo.Storer.RemoveReference(refName); err != nil {
		return err
	}

	// Remove branch config entry (best-effort; may not exist).
	_ = c.repo.DeleteBranch(name)

	return nil
}

// =============================================================================
// Commit Count
// =============================================================================

// CountBranchCommits returns the number of commits reachable from a branch tip.
// If the count reaches limit, iteration stops early and capped is true.
// Limit ≤ 0 means unlimited (count all commits).
func (c *GitClient) CountBranchCommits(name string, limit int) (count int, capped bool, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return 0, false, ErrNotGitRepo
	}

	ref, err := c.repo.Reference(plumbing.NewBranchReferenceName(name), true)
	if err != nil {
		return 0, false, err
	}

	iter, err := c.repo.Log(&gogit.LogOptions{
		From:  ref.Hash(),
		Order: gogit.LogOrderCommitterTime,
	})
	if err != nil {
		return 0, false, err
	}
	defer iter.Close()

	_ = iter.ForEach(func(_ *object.Commit) error {
		count++
		if limit > 0 && count >= limit {
			return io.EOF
		}
		return nil
	})

	capped = limit > 0 && count >= limit
	return count, capped, nil
}

// =============================================================================
// Commit
// =============================================================================

// CommitFiles stages the given file paths and creates a commit with the
// provided message. Uses a write lock because it mutates the index and HEAD.
// Returns ErrNotGitRepo if not a git repository.
func (c *GitClient) CommitFiles(paths []string, message string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	for _, p := range paths {
		if _, err := wt.Add(p); err != nil {
			return err
		}
	}

	_, err = wt.Commit(message, &gogit.CommitOptions{})
	return err
}

// =============================================================================
// Tags
// =============================================================================

// TagInfo represents metadata about a git tag.
type TagInfo struct {
	Name       string    // Short tag name.
	Hash       string    // Full commit hash the tag points to.
	ShortHash  string    // Abbreviated commit hash (7 chars).
	Subject    string    // First line of the tagged commit (or tag message for annotated).
	AuthorTime time.Time // Author time of the tagged commit.
	Annotated  bool      // True if this is an annotated tag.
}

// ListTags returns metadata for all tags in the repository.
// Returns ErrNotGitRepo if not a git repository.
func (c *GitClient) ListTags() ([]TagInfo, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo || c.repo == nil {
		return nil, ErrNotGitRepo
	}

	iter, err := c.repo.Tags()
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	var tags []TagInfo
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		info := c.tagInfoFromRef(ref)
		tags = append(tags, info)
		return nil
	})

	return tags, nil
}

// tagInfoFromRef builds a TagInfo from a tag reference.
// Handles both lightweight tags (point directly to a commit) and
// annotated tags (point to a tag object which references a commit).
func (c *GitClient) tagInfoFromRef(ref *plumbing.Reference) TagInfo {
	name := ref.Name().Short()
	hash := ref.Hash()

	info := TagInfo{
		Name:      name,
		Hash:      hash.String(),
		ShortHash: hash.String()[:7],
	}

	// Try annotated tag first.
	tagObj, err := c.repo.TagObject(hash)
	if err == nil {
		info.Annotated = true
		info.Subject = extractSubject(tagObj.Message)
		info.AuthorTime = tagObj.Tagger.When
		// Resolve to the commit hash for display.
		if commit, err := tagObj.Commit(); err == nil {
			info.Hash = commit.Hash.String()
			info.ShortHash = commit.Hash.String()[:7]
		}
		return info
	}

	// Lightweight tag — resolve directly to commit.
	commit, err := c.repo.CommitObject(hash)
	if err == nil {
		info.Subject = extractSubject(commit.Message)
		info.AuthorTime = commit.Author.When
	}

	return info
}
