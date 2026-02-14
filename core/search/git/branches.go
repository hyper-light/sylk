package git

import (
	"container/heap"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	fdiff "github.com/go-git/go-git/v5/plumbing/format/diff"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
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

	info.CreatedTime = c.branchCreatedTime(name)

	return info
}

// branchCreatedTime returns the time the branch ref was created.
// It first checks the reflog (first entry timestamp). If the reflog is
// unavailable (e.g. branch created by go-git which doesn't write reflogs),
// it falls back to the loose ref file's modification time.
// Returns zero time if neither source is available.
func (c *GitClient) branchCreatedTime(name string) time.Time {
	// Try reflog first — most accurate source.
	if t := c.branchReflogCreatedTime(name); !t.IsZero() {
		return t
	}
	// Fall back to the loose ref file's mtime.
	refPath := filepath.Join(c.repoPath, ".git", "refs", "heads", name)
	fi, err := os.Stat(refPath)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// branchReflogCreatedTime reads the first reflog entry for the named branch
// and returns its timestamp.
func (c *GitClient) branchReflogCreatedTime(name string) time.Time {
	reflogPath := filepath.Join(c.repoPath, ".git", "logs", "refs", "heads", name)
	f, err := os.Open(reflogPath)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()

	// Read enough of the first line to extract the timestamp.
	var buf [512]byte
	n, _ := f.Read(buf[:])
	if n == 0 {
		return time.Time{}
	}

	line := string(buf[:n])
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}

	// Reflog format: <old> <new> <name> <<email>> <unix_ts> <tz>\t<msg>
	// Find the closing '>' of the email, then parse the Unix timestamp.
	gt := strings.LastIndex(line, "> ")
	if gt < 0 {
		return time.Time{}
	}
	rest := line[gt+2:]
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return time.Time{}
	}
	ts, err := strconv.ParseInt(rest[:sp], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// BranchTipHash returns the full commit hash of the named local branch tip.
// O(1) reference lookup — no branch enumeration.
func (c *GitClient) BranchTipHash(name string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return "", ErrNotGitRepo
	}

	ref, err := c.repo.Reference(plumbing.NewBranchReferenceName(name), true)
	if err != nil {
		return "", err
	}
	return ref.Hash().String(), nil
}

// DiffStatLevel specifies the granularity of diff statistics to compute.
type DiffStatLevel int

const (
	DiffStatNone  DiffStatLevel = iota // no stats needed
	DiffStatFiles                      // file-level only (DiffTree + Action)
	DiffStatLines                      // full line-level (Patch)
)

// DiffSummary holds line-level and file-level change statistics for a commit.
type DiffSummary struct {
	Additions, Deletions                   int
	FilesAdded, FilesModified, FilesDeleted int
}

// FileSummary holds file-level change counts without line-level detail.
type FileSummary struct {
	FilesAdded, FilesModified, FilesDeleted int
}

// GetCommitStats returns the total additions and deletions for a commit.
// Uses the cached diffSummaryForHash path to avoid expensive Patch() calls.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrCommitNotFound if the commit does not exist.
func (c *GitClient) GetCommitStats(hash string) (additions, deletions int, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return 0, 0, ErrNotGitRepo
	}

	ds, err := c.diffSummaryForHash(hash)
	if err != nil {
		return 0, 0, err
	}

	return ds.Additions, ds.Deletions, nil
}

// GetCommitDiffSummary returns line and file change statistics for a commit.
// For root commits (no parent), diffs against an empty tree.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrCommitNotFound if the commit does not exist.
func (c *GitClient) GetCommitDiffSummary(hash string) (DiffSummary, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return DiffSummary{}, ErrNotGitRepo
	}

	return c.diffSummaryForHash(hash)
}

// GetCommitDiffSummaries returns diff summaries for multiple commits under a
// single read lock. Hashes that fail to resolve are silently skipped.
func (c *GitClient) GetCommitDiffSummaries(hashes []string) map[string]DiffSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]DiffSummary, len(hashes))
	if !c.isRepo {
		return out
	}

	for _, h := range hashes {
		if ds, err := c.diffSummaryForHash(h); err == nil {
			out[h] = ds
		}
	}
	return out
}

// GetCommitFileSummaries returns file-level change counts for multiple commits
// under a single read lock, using DiffTree + Action (no Patch/blob reads).
// Hashes that fail to resolve are silently skipped.
func (c *GitClient) GetCommitFileSummaries(hashes []string) map[string]FileSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]FileSummary, len(hashes))
	if !c.isRepo {
		return out
	}

	for _, h := range hashes {
		if fs, err := c.fileSummaryForHash(h); err == nil {
			out[h] = fs
		}
	}
	return out
}

// fileSummaryForHash resolves a hash and computes file-level stats.
// Checks the cache for existing file or line-level results first.
// Caller must hold at least an RLock on c.mu.
func (c *GitClient) fileSummaryForHash(hash string) (FileSummary, error) {
	// Check line-level cache (superset of file-level).
	if val, ok := c.diffCache.Get("line:" + hash); ok {
		ds := val.(DiffSummary)
		return FileSummary{ds.FilesAdded, ds.FilesModified, ds.FilesDeleted}, nil
	}
	// Check file-level cache.
	if val, ok := c.diffCache.Get("file:" + hash); ok {
		return val.(FileSummary), nil
	}

	resolved, err := c.resolveCommitHash(hash)
	if err != nil {
		return FileSummary{}, err
	}

	commit, err := c.repo.CommitObject(resolved)
	if err != nil {
		return FileSummary{}, ErrCommitNotFound
	}

	fs, err := c.fileSummaryFromCommit(commit)
	if err != nil {
		return FileSummary{}, err
	}

	c.diffCache.Set("file:"+hash, fs, 1)
	return fs, nil
}

// fileSummaryFromCommit computes file-level stats using DiffTree + Action.
// No Patch() call — no blob reads.
// Caller must hold at least an RLock on c.mu.
func (c *GitClient) fileSummaryFromCommit(commit *object.Commit) (FileSummary, error) {
	parentTree, err := firstParentTree(c, commit)
	if err != nil {
		return FileSummary{}, err
	}

	commitTree, err := commit.Tree()
	if err != nil {
		return FileSummary{}, err
	}

	changes, err := object.DiffTree(parentTree, commitTree)
	if err != nil {
		return FileSummary{}, err
	}

	var fs FileSummary
	for _, ch := range changes {
		action, aErr := ch.Action()
		if aErr != nil {
			continue
		}
		switch action {
		case merkletrie.Insert:
			fs.FilesAdded++
		case merkletrie.Delete:
			fs.FilesDeleted++
		case merkletrie.Modify:
			fs.FilesModified++
		}
	}
	return fs, nil
}

// diffSummaryForHash resolves a hash string and computes its diff summary.
// Caller must hold at least an RLock on c.mu.
func (c *GitClient) diffSummaryForHash(hash string) (DiffSummary, error) {
	if val, ok := c.diffCache.Get("line:" + hash); ok {
		return val.(DiffSummary), nil
	}

	resolved, err := c.resolveCommitHash(hash)
	if err != nil {
		return DiffSummary{}, err
	}

	commit, err := c.repo.CommitObject(resolved)
	if err != nil {
		return DiffSummary{}, ErrCommitNotFound
	}

	ds, err := c.diffSummaryFromCommit(commit)
	if err != nil {
		return DiffSummary{}, err
	}

	c.diffCache.Set("line:"+hash, ds, 1)
	c.diffCache.Set("file:"+hash, FileSummary{ds.FilesAdded, ds.FilesModified, ds.FilesDeleted}, 1)
	return ds, nil
}

// diffSummaryFromCommit computes a DiffSummary by diffing the commit against
// its first parent (or an empty tree for root commits).
// Caller must hold at least an RLock on c.mu.
func (c *GitClient) diffSummaryFromCommit(commit *object.Commit) (DiffSummary, error) {
	parentTree, err := firstParentTree(c, commit)
	if err != nil {
		return DiffSummary{}, err
	}

	commitTree, err := commit.Tree()
	if err != nil {
		return DiffSummary{}, err
	}

	changes, err := object.DiffTree(parentTree, commitTree)
	if err != nil {
		return DiffSummary{}, err
	}

	patch, err := changes.Patch()
	if err != nil {
		return DiffSummary{}, err
	}

	return diffSummaryFromPatch(patch), nil
}

// firstParentTree returns the tree of a commit's first parent.
// Returns (nil, nil) for root commits (no parents).
func firstParentTree(c *GitClient, commit *object.Commit) (*object.Tree, error) {
	if len(commit.ParentHashes) == 0 {
		return nil, nil
	}

	parent, err := c.repo.CommitObject(commit.ParentHashes[0])
	if err != nil {
		return nil, err
	}

	return parent.Tree()
}

// diffSummaryFromPatch accumulates file and line statistics from a patch.
func diffSummaryFromPatch(patch *object.Patch) DiffSummary {
	var ds DiffSummary
	for _, fp := range patch.FilePatches() {
		classifyFilePatch(fp, &ds)
		for _, chunk := range fp.Chunks() {
			n := chunkLineCount(chunk)
			switch chunk.Type() {
			case fdiff.Add:
				ds.Additions += n
			case fdiff.Delete:
				ds.Deletions += n
			}
		}
	}
	return ds
}

// classifyFilePatch increments the appropriate file counter on ds.
// The degenerate case (from==nil && to==nil) is skipped.
func classifyFilePatch(fp fdiff.FilePatch, ds *DiffSummary) {
	from, to := fp.Files()
	switch {
	case from == nil && to != nil:
		ds.FilesAdded++
	case from != nil && to == nil:
		ds.FilesDeleted++
	case from != nil && to != nil:
		ds.FilesModified++
	}
}

// chunkLineCount counts the lines in a diff chunk.
// Matches go-git's getFileStatsFromFilePatches logic.
func chunkLineCount(chunk fdiff.Chunk) int {
	s := chunk.Content()
	if len(s) == 0 {
		return 0
	}
	n := strings.Count(s, "\n")
	if s[len(s)-1] != '\n' {
		n++
	}
	return n
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

// ListCommitsForBranch returns commits on a single branch using first-parent
// traversal. This follows only the first parent at each step, matching
// `git log --first-parent`, so commits from merged branches are excluded.
// Results are ordered newest-first (branch tip is index 0).
// Limit ≤ 0 means return all. Returns ErrNotGitRepo if not a git repository.
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

	// Walk the first-parent chain from the branch tip.
	hash := ref.Hash()
	var commits []TreeCommit
	for {
		if limit > 0 && len(commits) >= limit {
			break
		}
		co, err := c.repo.CommitObject(hash)
		if err != nil {
			break
		}
		commits = append(commits, treeCommitFromObject(co, branchMap))
		if len(co.ParentHashes) == 0 {
			break // root commit
		}
		hash = co.ParentHashes[0] // first parent only
	}

	return commits, nil
}

// ListBranchOnlyCommits returns a page of commits unique to a branch by
// walking the first-parent chain and stopping at the merge-base with
// baseBranch. When branchName equals baseBranch (or baseBranch is empty),
// the full first-parent history is returned.
//
// afterHash provides cursor-based pagination: when empty the walk starts
// from the branch tip; when non-empty the walk starts from that commit's
// first parent, skipping already-loaded commits in O(1).
//
// Returns (commits, hasMore, error). hasMore is true when more pages
// remain beyond the returned page. pageSize ≤ 0 means return all.
func (c *GitClient) ListBranchOnlyCommits(branchName, baseBranch, afterHash string, pageSize int) ([]TreeCommit, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, false, ErrNotGitRepo
	}

	branchRef, err := c.repo.Reference(plumbing.NewBranchReferenceName(branchName), true)
	if err != nil {
		return nil, false, err
	}

	branchMap, err := c.buildBranchMap()
	if err != nil {
		return nil, false, err
	}

	stopAt := c.mergeBaseStopSet(branchRef.Hash(), branchName, baseBranch)

	// Determine walk start: branch tip for first page, afterHash's first
	// parent for subsequent pages.
	startHash := branchRef.Hash()
	if afterHash != "" {
		afterCommit, aErr := c.repo.CommitObject(plumbing.NewHash(afterHash))
		if aErr != nil {
			return nil, false, aErr
		}
		if len(afterCommit.ParentHashes) == 0 {
			return nil, false, nil
		}
		startHash = afterCommit.ParentHashes[0]
	}

	// Request one extra commit to detect whether more pages exist.
	fetchLimit := pageSize
	if pageSize > 0 {
		fetchLimit = pageSize + 1
	}
	commits := c.walkFirstParent(startHash, branchMap, fetchLimit, stopAt)

	hasMore := pageSize > 0 && len(commits) > pageSize
	if hasMore {
		commits = commits[:pageSize]
	}
	return commits, hasMore, nil
}

// mergeBaseStopSet computes the set of merge-base hashes where the
// first-parent walk should stop. Returns nil when no filtering should
// occur (default branch, same tip, orphan, or any resolution error).
func (c *GitClient) mergeBaseStopSet(branchHash plumbing.Hash, branchName, baseBranch string) map[plumbing.Hash]struct{} {
	if baseBranch == "" || branchName == baseBranch {
		return nil
	}
	bases := c.computeMergeBases(branchHash, baseBranch)
	if len(bases) == 0 {
		return nil
	}
	stopAt := make(map[plumbing.Hash]struct{}, len(bases))
	for _, b := range bases {
		stopAt[b.Hash] = struct{}{}
	}
	return stopAt
}

// computeMergeBases returns the merge-base commits between branchHash and the
// named base branch. Returns nil on any error or when tips are identical.
func (c *GitClient) computeMergeBases(branchHash plumbing.Hash, baseBranch string) []*object.Commit {
	baseRef, err := c.repo.Reference(plumbing.NewBranchReferenceName(baseBranch), true)
	if err != nil {
		return nil
	}
	if branchHash == baseRef.Hash() {
		return nil
	}
	branchTip, err := c.repo.CommitObject(branchHash)
	if err != nil {
		return nil
	}
	baseTip, err := c.repo.CommitObject(baseRef.Hash())
	if err != nil {
		return nil
	}
	bases, _ := branchTip.MergeBase(baseTip)
	return bases
}

// walkFirstParent traverses the first-parent chain from startHash, collecting
// TreeCommits. Stops at the limit, root commit, or any hash in stopAt
// (which is excluded from results). Safe to call with a nil stopAt map.
func (c *GitClient) walkFirstParent(startHash plumbing.Hash, branchMap map[string]string, limit int, stopAt map[plumbing.Hash]struct{}) []TreeCommit {
	var commits []TreeCommit
	hash := startHash
	for {
		if limit > 0 && len(commits) >= limit {
			break
		}
		if _, stop := stopAt[hash]; stop {
			break
		}
		co, err := c.repo.CommitObject(hash)
		if err != nil {
			break
		}
		commits = append(commits, treeCommitFromObject(co, branchMap))
		if len(co.ParentHashes) == 0 {
			break
		}
		hash = co.ParentHashes[0]
	}
	return commits
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
// Create
// =============================================================================

// CreateBranch creates a new local branch pointing at the given commit hash.
// Returns ErrNotGitRepo if not a git repository.
// Returns ErrBranchExists if a branch with the given name already exists.
func (c *GitClient) CreateBranch(name, atCommitHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	refName := plumbing.NewBranchReferenceName(name)

	// Verify branch does not already exist.
	if _, err := c.repo.Reference(refName, true); err == nil {
		return ErrBranchExists
	}

	// Validate the target commit exists.
	hash := plumbing.NewHash(atCommitHash)
	if _, err := c.repo.CommitObject(hash); err != nil {
		return err
	}

	return c.repo.Storer.SetReference(plumbing.NewHashReference(refName, hash))
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

// CountBranchOnlyCommits returns the number of commits reachable from a
// branch tip that are NOT reachable from the base branch tip (equivalent to
// `git rev-list branch ^base --count`). This correctly handles merges of
// the base into the branch and vice versa.
// When branchName equals baseBranch (or baseBranch is empty), the full
// reachable history is counted.
// If the count reaches limit, iteration stops early and capped is true.
// Limit <= 0 means unlimited.
func (c *GitClient) CountBranchOnlyCommits(branchName, baseBranch string, limit int) (count int, capped bool, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return 0, false, ErrNotGitRepo
	}

	branchRef, err := c.repo.Reference(plumbing.NewBranchReferenceName(branchName), true)
	if err != nil {
		return 0, false, err
	}

	if baseBranch == "" || branchName == baseBranch {
		return c.countAllFrom(branchRef.Hash(), limit)
	}

	baseRef, err := c.repo.Reference(plumbing.NewBranchReferenceName(baseBranch), true)
	if err != nil {
		return 0, false, err
	}

	if branchRef.Hash() == baseRef.Hash() {
		return 0, false, nil
	}

	baseSet := c.reachableSet(baseRef.Hash())
	cnt, cap := c.countExcluding(branchRef.Hash(), baseSet, limit)
	return cnt, cap, nil
}

// reachableSet returns the set of all commit hashes reachable from the
// given starting hash (following all parents, not just first-parent).
func (c *GitClient) reachableSet(from plumbing.Hash) map[plumbing.Hash]struct{} {
	iter, err := c.repo.Log(&gogit.LogOptions{From: from})
	if err != nil {
		return nil
	}
	defer iter.Close()

	set := make(map[plumbing.Hash]struct{}, 256)
	_ = iter.ForEach(func(co *object.Commit) error {
		set[co.Hash] = struct{}{}
		return nil
	})
	return set
}

// BranchOnlyCount holds the result of a per-branch unique commit count,
// including both ahead (unique to branch) and behind (unique to base).
type BranchOnlyCount struct {
	Count        int
	Capped       bool
	Behind       int
	BehindCapped bool
}

// CountBranchOnlyCommitsBatch counts unique commits for multiple branches
// against the same base branch, building the base reachable set only once.
// This is O(base_history + sum(branch_deltas)) instead of
// O(branches * base_history) when calling CountBranchOnlyCommits per branch.
func (c *GitClient) CountBranchOnlyCommitsBatch(branchNames []string, baseBranch string, limit int) map[string]BranchOnlyCount {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil
	}

	result := make(map[string]BranchOnlyCount, len(branchNames))

	// Build the base reachable set once.
	var baseSet map[plumbing.Hash]struct{}
	var baseHash plumbing.Hash
	if baseBranch != "" {
		baseRef, err := c.repo.Reference(plumbing.NewBranchReferenceName(baseBranch), true)
		if err != nil {
			return result
		}
		baseHash = baseRef.Hash()
		baseSet = c.reachableSet(baseHash)
	}

	for _, name := range branchNames {
		ref, err := c.repo.Reference(plumbing.NewBranchReferenceName(name), true)
		if err != nil {
			continue
		}
		if baseBranch == "" || name == baseBranch {
			cnt, cap, cErr := c.countAllFrom(ref.Hash(), limit)
			if cErr == nil {
				result[name] = BranchOnlyCount{Count: cnt, Capped: cap}
			}
			continue
		}
		if ref.Hash() == baseHash {
			result[name] = BranchOnlyCount{}
			continue
		}
		branchSet := c.reachableSet(ref.Hash())
		ahead, aheadCap := c.countExcluding(ref.Hash(), baseSet, limit)
		behind, behindCap := c.countExcluding(baseHash, branchSet, limit)
		result[name] = BranchOnlyCount{
			Count: ahead, Capped: aheadCap,
			Behind: behind, BehindCapped: behindCap,
		}
	}
	return result
}

// countExcluding counts commits reachable from start that are not in excludeSet.
// Caller must hold at least an RLock on c.mu.
func (c *GitClient) countExcluding(start plumbing.Hash, excludeSet map[plumbing.Hash]struct{}, limit int) (int, bool) {
	seen := make(map[plumbing.Hash]struct{}, 64)
	queue := []plumbing.Hash{start}
	count := 0
	for len(queue) > 0 {
		hash := queue[0]
		queue = queue[1:]
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		if _, inBase := excludeSet[hash]; inBase {
			continue
		}
		count++
		if limit > 0 && count >= limit {
			return count, true
		}
		co, err := c.repo.CommitObject(hash)
		if err != nil {
			continue
		}
		for _, ph := range co.ParentHashes {
			queue = append(queue, ph)
		}
	}
	return count, false
}

// countAllFrom counts all commits reachable from hash (full history).
func (c *GitClient) countAllFrom(hash plumbing.Hash, limit int) (int, bool, error) {
	iter, err := c.repo.Log(&gogit.LogOptions{From: hash})
	if err != nil {
		return 0, false, err
	}
	defer iter.Close()

	count := 0
	_ = iter.ForEach(func(_ *object.Commit) error {
		count++
		if limit > 0 && count >= limit {
			return io.EOF
		}
		return nil
	})
	return count, limit > 0 && count >= limit, nil
}

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
// Branch Parent Inference
// =============================================================================

// inferWalkLimit caps the first-parent walk depth per branch during parent
// inference. Derived from: typical feature branches have < 100 commits;
// 200 provides comfortable headroom without unbounded growth.
const inferWalkLimit = 200

// InferBranchParents determines parent-child relationships between branches
// by walking the commit graph. For each non-default branch, it walks the
// first-parent chain looking for another branch's tip commit — the same
// topology visible in `git log --graph --all --decorate`. The first foreign
// tip found determines the parent:
//   - Non-default branch tip → that branch is the parent.
//   - Default branch tip → child of default (no entry).
//   - No tip found within walk limit → child of default (no entry).
//
// Returns child name → parent name. Only branches with a detected
// non-default parent appear in the map.
func (c *GitClient) InferBranchParents(branches []BranchInfo, defaultBranch string) map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo || len(branches) < 2 {
		return nil
	}

	// Multiple branches may share a tip (e.g. freshly created branch).
	tipsAt := make(map[plumbing.Hash][]string, len(branches))
	for _, b := range branches {
		h := plumbing.NewHash(b.Hash)
		tipsAt[h] = append(tipsAt[h], b.Name)
	}

	// Identify HEAD for shared-tip resolution: when two branches point to
	// the same commit, HEAD is the established branch (parent).
	headBranch := ""
	for _, b := range branches {
		if b.IsHead {
			headBranch = b.Name
			break
		}
	}

	parents := make(map[string]string, len(branches))
	for _, a := range branches {
		if a.Name == defaultBranch {
			continue
		}
		if p := c.ancestorBranchTip(plumbing.NewHash(a.Hash), a.Name, defaultBranch, headBranch, tipsAt); p != "" {
			parents[a.Name] = p
		}
	}
	return parents
}

// ancestorBranchTip walks the first-parent chain from hash, looking for a
// commit that is another branch's tip. At step 0 (shared tip), only HEAD
// qualifies as parent — this resolves directionality for freshly created
// branches. At step 1+, any non-default foreign tip qualifies.
func (c *GitClient) ancestorBranchTip(hash plumbing.Hash, self, defaultBranch, headBranch string, tipsAt map[plumbing.Hash][]string) string {
	for step := range inferWalkLimit {
		co, err := c.repo.CommitObject(hash)
		if err != nil {
			return ""
		}
		if step == 0 {
			// Shared tip: HEAD is the parent (git checkout -b).
			if p := matchHeadTip(tipsAt[hash], self, defaultBranch, headBranch); p != "" {
				return p
			}
		} else {
			parent, stop := matchParentTip(tipsAt[hash], self, defaultBranch)
			if parent != "" {
				return parent
			}
			if stop {
				return ""
			}
		}
		if len(co.ParentHashes) == 0 {
			return ""
		}
		hash = co.ParentHashes[0]
	}
	return ""
}

// matchHeadTip checks if HEAD occupies the same commit as self. Returns
// HEAD's name if it's a valid non-default parent, or "" otherwise.
func matchHeadTip(names []string, self, defaultBranch, headBranch string) string {
	if self == headBranch || headBranch == defaultBranch {
		return ""
	}
	for _, name := range names {
		if name == headBranch {
			return name
		}
	}
	return ""
}

// matchParentTip scans branch names at a commit for a non-self, non-default
// parent. Returns (name, false) if found, or ("", true) if the default branch
// occupies this commit (signals walk termination).
func matchParentTip(names []string, self, defaultBranch string) (string, bool) {
	hitDefault := false
	for _, name := range names {
		if name == self {
			continue
		}
		if name == defaultBranch {
			hitDefault = true
			continue
		}
		return name, false
	}
	return "", hitDefault
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
// DAG — Full Branch History with Merge Structure
// =============================================================================

// DagCommitLimit caps the number of commits fetched in DAG mode.
// Derived from: 500 commits × ~200B ≈ 100KB — well within memory bounds.
// Beyond this limit, falls back to flat first-parent view.
const DagCommitLimit = 500

// ListBranchDAGCommits returns all commits unique to a branch (following ALL
// parents), topologically sorted (newest first). This preserves merge structure
// for DAG visualization. Falls back to nil when the branch has more than
// DagCommitLimit unique commits.
// Caller must hold NO lock — this acquires RLock internally.
func (c *GitClient) ListBranchDAGCommits(branchName, baseBranch string, limit int) ([]TreeCommit, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}

	branchRef, err := c.repo.Reference(plumbing.NewBranchReferenceName(branchName), true)
	if err != nil {
		return nil, err
	}

	// Build base reachable set (empty if same branch or no base).
	baseSet := c.dagBaseSet(branchRef.Hash(), branchName, baseBranch)

	collected := c.collectBranchDAG(branchRef.Hash(), baseSet, limit)
	if len(collected) == 0 {
		return nil, nil
	}

	sorted := topoSortDAG(collected, branchRef.Hash())

	branchMap, _ := c.buildBranchMap()
	result := make([]TreeCommit, len(sorted))
	for i, co := range sorted {
		result[i] = treeCommitFromObject(co, branchMap)
	}
	return result, nil
}

// dagBaseSet builds the exclusion set for DAG collection.
// Returns nil when no filtering should occur (same branch, empty base, errors).
func (c *GitClient) dagBaseSet(branchHash plumbing.Hash, branchName, baseBranch string) map[plumbing.Hash]struct{} {
	if baseBranch == "" || branchName == baseBranch {
		return nil
	}
	baseRef, err := c.repo.Reference(plumbing.NewBranchReferenceName(baseBranch), true)
	if err != nil {
		return nil
	}
	if branchHash == baseRef.Hash() {
		return nil
	}
	return c.reachableSet(baseRef.Hash())
}

// collectBranchDAG performs BFS from branchHash following ALL parents,
// skipping any hash in baseSet, collecting up to limit commits.
func (c *GitClient) collectBranchDAG(branchHash plumbing.Hash, baseSet map[plumbing.Hash]struct{}, limit int) map[plumbing.Hash]*object.Commit {
	result := make(map[plumbing.Hash]*object.Commit, min(limit, 64))
	queue := []plumbing.Hash{branchHash}

	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if _, seen := result[h]; seen {
			continue
		}
		if _, inBase := baseSet[h]; inBase {
			continue
		}
		co, err := c.repo.CommitObject(h)
		if err != nil {
			continue
		}
		result[h] = co
		if len(result) >= limit {
			break
		}
		for _, ph := range co.ParentHashes {
			queue = append(queue, ph)
		}
	}
	return result
}

// topoSortDAG performs Kahn's algorithm on the DAG subset, using a max-heap
// keyed by committer time (newest first) for deterministic output order.
func topoSortDAG(commits map[plumbing.Hash]*object.Commit, tipHash plumbing.Hash) []*object.Commit {
	// Compute in-degree within the DAG subset.
	inDeg := make(map[plumbing.Hash]int, len(commits))
	for h := range commits {
		inDeg[h] = 0
	}
	for _, co := range commits {
		for _, ph := range co.ParentHashes {
			if _, ok := commits[ph]; ok {
				inDeg[ph]++
			}
		}
	}

	// Seed the heap with zero in-degree nodes.
	h := &commitHeap{}
	heap.Init(h)
	for hash, deg := range inDeg {
		if deg == 0 {
			heap.Push(h, commits[hash])
		}
	}

	result := make([]*object.Commit, 0, len(commits))
	for h.Len() > 0 {
		co := heap.Pop(h).(*object.Commit)
		result = append(result, co)
		for _, ph := range co.ParentHashes {
			if _, ok := commits[ph]; !ok {
				continue
			}
			inDeg[ph]--
			if inDeg[ph] == 0 {
				heap.Push(h, commits[ph])
			}
		}
	}
	return result
}

// commitHeap is a max-heap of commits ordered by committer time (newest first),
// with hash as tiebreaker for determinism.
type commitHeap []*object.Commit

func (h commitHeap) Len() int { return len(h) }
func (h commitHeap) Less(i, j int) bool {
	ti := h[i].Committer.When
	tj := h[j].Committer.When
	if !ti.Equal(tj) {
		return ti.After(tj) // newest first
	}
	return h[i].Hash.String() < h[j].Hash.String()
}
func (h commitHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *commitHeap) Push(x any)         { *h = append(*h, x.(*object.Commit)) }
func (h *commitHeap) Pop() any           { old := *h; n := len(old); x := old[n-1]; old[n-1] = nil; *h = old[:n-1]; return x }

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
