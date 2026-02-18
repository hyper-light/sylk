package git

import (
	"container/heap"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
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
	for {
		ref, nextErr := iter.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, nextErr
		}
		branches = append(branches, branchInfoFromRef(c, ref, headBranch))
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
	info.BirthHash = c.branchReflogBirthHash(name)

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

// branchReflogBirthHash reads the first reflog entry for the named branch and
// returns the <new> hash if <old> is the zero hash (confirming a creation
// entry). Returns empty string if the reflog is unavailable or the first
// entry is not a creation entry (e.g., truncated reflog).
func (c *GitClient) branchReflogBirthHash(name string) string {
	reflogPath := filepath.Join(c.repoPath, ".git", "logs", "refs", "heads", name)
	f, err := os.Open(reflogPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	var buf [512]byte
	n, _ := f.Read(buf[:])
	if n == 0 {
		return ""
	}

	line := string(buf[:n])
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}

	// Reflog format: <old> <new> <name> <<email>> <unix_ts> <tz>\t<msg>
	fields := strings.SplitN(line, " ", 3)
	if len(fields) < 2 {
		return ""
	}

	oldHash, newHash := fields[0], fields[1]
	if oldHash != plumbing.ZeroHash.String() {
		return ""
	}
	return newHash
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
	for {
		if limit > 0 && len(commits) >= limit {
			break
		}
		co, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		commits = append(commits, treeCommitFromObject(co, branchMap))
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
	for {
		ref, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		m[ref.Hash().String()] = ref.Name().Short()
	}
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

// CheckoutCommit checks out a specific commit by hash, resulting in a
// detached HEAD state. Supports both full and abbreviated hashes.
func (c *GitClient) CheckoutCommit(commitHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	hash, err := c.resolveCommitHash(commitHash)
	if err != nil {
		return fmt.Errorf("resolve commit: %w", err)
	}

	if _, err := c.repo.CommitObject(hash); err != nil {
		return fmt.Errorf("commit not found: %w", err)
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	return wt.Checkout(&gogit.CheckoutOptions{Hash: hash})
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

	if err := c.repo.Storer.SetReference(plumbing.NewHashReference(refName, hash)); err != nil {
		return err
	}

	// Write a reflog entry so branchCreatedTime returns a stable timestamp.
	// go-git does not write reflogs automatically; without this, the fallback
	// is the ref file's mtime, which changes on every commit.
	_ = c.writeInitialReflog(name, hash)

	return nil
}

// writeInitialReflog creates a reflog file for a newly created branch with
// a single "branch: Created" entry. The reflog timestamp is used by
// branchCreatedTime to provide a stable creation time that survives
// subsequent ref updates.
func (c *GitClient) writeInitialReflog(name string, hash plumbing.Hash) error {
	logPath := filepath.Join(c.repoPath, ".git", "logs", "refs", "heads", name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}

	ts := time.Now()
	// Standard reflog format: <old> <new> <name> <<email>> <unix> <tz>\t<msg>
	entry := plumbing.ZeroHash.String() + " " + hash.String() +
		" sylk <sylk@localhost> " + strconv.FormatInt(ts.Unix(), 10) +
		" +0000\tbranch: Created\n"

	return os.WriteFile(logPath, []byte(entry), 0o644)
}

// =============================================================================
// Delete
// =============================================================================

// MergeBranch merges sourceBranch into targetBranch.
// The target branch is checked out first, then the source is merged in.
// Supports fast-forward and full 3-way merge via treeMerge3.
func (c *GitClient) MergeBranch(sourceBranch, targetBranch string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	targetRef := plumbing.NewBranchReferenceName(targetBranch)
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: targetRef}); err != nil {
		return fmt.Errorf("checkout %s: %w", targetBranch, err)
	}

	sourceCommit, err := c.resolveBranchCommit(sourceBranch)
	if err != nil {
		return err
	}

	targetCommit, err := c.headCommit()
	if err != nil {
		return err
	}

	msg := fmt.Sprintf("Merge branch '%s' into %s\n", sourceBranch, targetBranch)
	return c.mergeCommits(wt, targetRef, targetCommit, sourceCommit, msg)
}

// resolveBranchCommit resolves a branch name to its tip commit object.
func (c *GitClient) resolveBranchCommit(branch string) (*object.Commit, error) {
	ref := plumbing.NewBranchReferenceName(branch)
	hash, err := c.repo.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", branch, err)
	}
	return c.repo.CommitObject(*hash)
}

// headCommit returns the commit object at HEAD.
func (c *GitClient) headCommit() (*object.Commit, error) {
	headRef, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}
	return c.repo.CommitObject(headRef.Hash())
}

// mergeCommits performs the merge of sourceCommit into localCommit at localRef.
// Handles already-up-to-date, fast-forward, and 3-way merge cases.
func (c *GitClient) mergeCommits(wt *gogit.Worktree, localRef plumbing.ReferenceName, localCommit, sourceCommit *object.Commit, msg string) error {
	// Already up to date: source is ancestor of local.
	isAncestor, err := sourceCommit.IsAncestor(localCommit)
	if err != nil {
		return err
	}
	if isAncestor {
		return nil
	}

	// Fast-forward: local is ancestor of source.
	isFF, err := localCommit.IsAncestor(sourceCommit)
	if err != nil {
		return err
	}
	if isFF {
		return c.updateRefAndReset(wt, localRef, sourceCommit.Hash)
	}

	// Non-fast-forward: 3-way merge.
	return c.threewayMerge(wt, localRef, localCommit, sourceCommit, msg)
}

// threewayMerge performs a full 3-way merge between local and source commits.
func (c *GitClient) threewayMerge(wt *gogit.Worktree, localRef plumbing.ReferenceName, localCommit, sourceCommit *object.Commit, msg string) error {
	bases, err := localCommit.MergeBase(sourceCommit)
	if err != nil {
		return err
	}
	if len(bases) == 0 {
		return fmt.Errorf("no common ancestor between %s and %s", localRef.Short(), sourceCommit.Hash.String()[:7])
	}

	baseTree, err := bases[0].Tree()
	if err != nil {
		return err
	}

	localTree, err := localCommit.Tree()
	if err != nil {
		return err
	}

	sourceTree, err := sourceCommit.Tree()
	if err != nil {
		return err
	}

	result, err := treeMerge3(c.repo.Storer, baseTree, localTree, sourceTree)
	if err != nil {
		return err
	}

	if result.HasConflicts() {
		if err := c.writeConflictsToWorktree(result.Conflicts); err != nil {
			return err
		}
		return ErrMergeConflict
	}

	sig := defaultSignature(time.Now())
	mergeHash, err := storeCommitObj(
		c.repo.Storer, result.TreeHash,
		[]plumbing.Hash{localCommit.Hash, sourceCommit.Hash},
		sig, sig, msg,
	)
	if err != nil {
		return err
	}

	return c.updateRefAndReset(wt, localRef, mergeHash)
}

// updateRefAndReset updates a branch ref and resets the worktree to that hash.
func (c *GitClient) updateRefAndReset(wt *gogit.Worktree, refName plumbing.ReferenceName, hash plumbing.Hash) error {
	ref := plumbing.NewHashReference(refName, hash)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return err
	}
	return wt.Reset(&gogit.ResetOptions{
		Commit: hash,
		Mode:   gogit.HardReset,
	})
}

// PullBranch fetches and merges the named branch from the given remote.
// If remoteName is empty, "origin" is used as the default.
// Supports both fast-forward and 3-way merge via fetch + treeMerge3.
func (c *GitClient) PullBranch(branchName, remoteName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}
	if remoteName == "" {
		remoteName = "origin"
	}

	if err := c.fetchBranch(branchName, remoteName); err != nil {
		return err
	}

	remoteCommit, err := c.resolveRemoteCommit(branchName, remoteName)
	if err != nil {
		return err
	}

	localCommit, err := c.headCommit()
	if err != nil {
		return err
	}

	if localCommit.Hash == remoteCommit.Hash {
		return nil
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	headRef, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}

	msg := fmt.Sprintf("Merge remote-tracking branch '%s/%s'\n", remoteName, branchName)
	return c.mergeCommits(wt, headRef.Name(), localCommit, remoteCommit, msg)
}

// fetchBranch fetches a single branch from the named remote.
func (c *GitClient) fetchBranch(branchName, remoteName string) error {
	remote, err := c.repo.Remote(remoteName)
	if err != nil {
		return fmt.Errorf("remote %s: %w", remoteName, err)
	}

	refSpec := config.RefSpec(
		"refs/heads/" + branchName + ":refs/remotes/" + remoteName + "/" + branchName,
	)
	err = remote.Fetch(&gogit.FetchOptions{
		RemoteName: remoteName,
		RefSpecs:   []config.RefSpec{refSpec},
		Force:      true,
	})
	if err != nil && err != gogit.NoErrAlreadyUpToDate {
		return fmt.Errorf("fetch: %w", err)
	}
	return nil
}

// resolveRemoteCommit resolves the remote tracking ref to a commit object.
func (c *GitClient) resolveRemoteCommit(branchName, remoteName string) (*object.Commit, error) {
	remoteRefName := plumbing.NewRemoteReferenceName(remoteName, branchName)
	remoteRef, err := c.repo.Reference(remoteRefName, true)
	if err != nil {
		return nil, fmt.Errorf("resolve remote ref: %w", err)
	}
	return c.repo.CommitObject(remoteRef.Hash())
}

// PushBranch pushes the named branch to the given remote.
// If remoteName is empty, "origin" is used as the default.
func (c *GitClient) PushBranch(branchName, remoteName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}
	if remoteName == "" {
		remoteName = "origin"
	}

	remote, err := c.repo.Remote(remoteName)
	if err != nil {
		return fmt.Errorf("remote %s: %w", remoteName, err)
	}

	refSpec := config.RefSpec(
		"refs/heads/" + branchName + ":refs/heads/" + branchName,
	)

	err = remote.Push(&gogit.PushOptions{
		RemoteName: remoteName,
		RefSpecs:   []config.RefSpec{refSpec},
	})
	if err == gogit.NoErrAlreadyUpToDate {
		return nil
	}
	return err
}

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
	for {
		co, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		set[co.Hash] = struct{}{}
	}
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
	for {
		_, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, false, err
		}
		count++
		if limit > 0 && count >= limit {
			break
		}
	}
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

	for {
		_, nextErr := iter.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return 0, false, nextErr
		}
		count++
		if limit > 0 && count >= limit {
			break
		}
	}

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
// using a two-phase approach: birth-hash inference (primary) and first-parent
// chain intersection (fallback for branches without reflogs).
//
// Birth-hash: For each branch B with a birth hash from the reflog, find the
// branch whose first-parent chain contains B's birth hash at the smallest
// position (closest to tip). If the best match is the default branch, B is
// top-level. This is merge-resilient because the birth commit never changes.
//
// Topology fallback: For branches without a birth hash (cloned repos, missing
// reflogs), find the non-default branch whose first-parent chain intersects
// B's chain at the smallest position within B's unique portion (before where
// B's chain meets default).
//
// Complexity: O(n × walkLimit) for first-parent position maps + O(n²) for
// pairwise chain intersection checks.
//
// Returns child name → parent name. Only branches with a detected
// non-default parent appear in the map.
func (c *GitClient) InferBranchParents(branches []BranchInfo, defaultBranch string) map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo || len(branches) < 2 {
		return nil
	}

	defaultIdx := -1
	for i, b := range branches {
		if b.Name == defaultBranch {
			defaultIdx = i
			break
		}
	}
	if defaultIdx < 0 {
		return nil
	}

	// Build first-parent chain positions for ALL branches (including default).
	chainPos := make([]map[plumbing.Hash]int, len(branches))
	for i, b := range branches {
		chainPos[i] = c.firstParentPositions(plumbing.NewHash(b.Hash))
	}
	if len(chainPos[defaultIdx]) == 0 {
		return nil
	}

	parents := make(map[string]string, len(branches))
	for i, b := range branches {
		if i == defaultIdx || len(chainPos[i]) == 0 {
			continue
		}

		// Phase 1: birth-hash inference.
		if p := birthParent(i, b, branches, chainPos, defaultIdx); p >= 0 {
			parents[b.Name] = branches[p].Name
			continue
		}

		// Phase 2: topology fallback (branch has unique commits off default).
		defForkPos := firstParentForkPos(chainPos[i], chainPos[defaultIdx])
		if p := topoParent(i, defForkPos, branches, chainPos, defaultIdx); p >= 0 {
			parents[b.Name] = branches[p].Name
			continue
		}

		// Phase 3: ownership fallback (branch tip is on default's chain but
		// may fall within another branch's owned range).
		if p := ownershipParent(i, branches, chainPos, defaultIdx); p >= 0 {
			parents[b.Name] = branches[p].Name
		}
	}
	breakCycles(parents)
	return parents
}

// firstParentForkPos returns the position on the branch's first-parent chain
// where it first intersects the default branch's first-parent chain. This is
// the "fork point" — commits at this position or beyond are shared with
// default and should not be used to establish parent-child relationships.
//
// Unlike merge-base (which considers all parents and shifts after merges),
// first-parent intersection is stable across 3-way merges (the merge
// commit's first parent stays on default's original line) and correctly
// flattens for ff-merges (the entire merged chain becomes default's line).
func firstParentForkPos(branchChain, defaultChain map[plumbing.Hash]int) int {
	best := inferWalkLimit + 1
	for hash, pos := range branchChain {
		if _, onDefault := defaultChain[hash]; onDefault && pos < best {
			best = pos
		}
	}
	return best
}

// birthParent finds the parent of branch i using its birth hash.
// Returns the index of the best parent branch, or -1 if no birth hash is
// available or the branch was forked from default (top-level).
//
// Key invariant: if the birth hash is on default's first-parent chain, the
// branch is unconditionally top-level. Sibling branches forked from default
// after the birth point also contain the birth hash at a closer position,
// but that does not make them the parent.
func birthParent(i int, b BranchInfo, branches []BranchInfo,
	chainPos []map[plumbing.Hash]int, defaultIdx int) int {

	if b.BirthHash == "" {
		return -1
	}
	bh := plumbing.NewHash(b.BirthHash)

	// If the birth hash is anywhere on default's first-parent chain,
	// this branch was forked from default → top-level.
	if _, onDefault := chainPos[defaultIdx][bh]; onDefault {
		return -1
	}

	best, bestPos := -1, inferWalkLimit+1
	for j := range branches {
		if j == i || j == defaultIdx || len(chainPos[j]) == 0 {
			continue
		}
		pos, ok := chainPos[j][bh]
		if !ok {
			continue
		}
		if pos < bestPos || (pos == bestPos && best >= 0 && branchOlder(branches[j], branches[best])) {
			best, bestPos = j, pos
		}
	}
	return best
}

// topoParent finds the parent of branch i using first-parent chain
// intersection (fallback for branches without a birth hash). Returns the
// index of the best parent branch, or -1 if none found.
func topoParent(i, defForkPos int, branches []BranchInfo,
	chainPos []map[plumbing.Hash]int, defaultIdx int) int {

	best, bestPos := -1, defForkPos
	for j := range branches {
		if j == i || j == defaultIdx || len(chainPos[j]) == 0 {
			continue
		}
		if !branchOlder(branches[j], branches[i]) {
			continue
		}

		pos := firstParentForkPos(chainPos[i], chainPos[j])
		if pos >= defForkPos {
			continue
		}
		if pos < bestPos || (pos == bestPos && best >= 0 && branchOlder(branches[j], branches[best])) {
			best, bestPos = j, pos
		}
	}
	return best
}

// ownershipParent finds the parent of branch i by checking which non-default
// branch "owns" i's tip commit. Branch j owns a commit if it appears on j's
// first-parent chain between j's tip (exclusive) and j's birth hash
// (exclusive). This handles branches without reflogs whose tip sits on the
// default chain but was actually forked from a feature branch.
//
// Among valid candidates, picks the one with the tightest owned range
// (smallest birthPos on j's chain), then branchOlder as tiebreak.
func ownershipParent(i int, branches []BranchInfo,
	chainPos []map[plumbing.Hash]int, defaultIdx int) int {

	tipHash := plumbing.NewHash(branches[i].Hash)

	best, bestBirthPos := -1, inferWalkLimit+1
	for j := range branches {
		if j == i || j == defaultIdx || len(chainPos[j]) == 0 {
			continue
		}
		if branches[j].BirthHash == "" {
			continue
		}

		tipPos, onChain := chainPos[j][tipHash]
		if !onChain || tipPos == 0 {
			continue
		}

		birthHash := plumbing.NewHash(branches[j].BirthHash)
		birthPos, hasBirth := chainPos[j][birthHash]
		if !hasBirth || tipPos >= birthPos {
			continue
		}

		if birthPos < bestBirthPos || (birthPos == bestBirthPos && best >= 0 && branchOlder(branches[j], branches[best])) {
			best, bestBirthPos = j, birthPos
		}
	}
	return best
}

// breakCycles removes edges that form cycles in the parent map.
// For each cycle, the edge whose child was created earliest is removed
// (that branch is more likely a true root among the cycle members).
func breakCycles(parents map[string]string) {
	for child := range parents {
		visited := map[string]struct{}{child: {}}
		cur := parents[child]
		for cur != "" {
			if _, cycle := visited[cur]; cycle {
				delete(parents, child)
				break
			}
			visited[cur] = struct{}{}
			cur = parents[cur]
		}
	}
}

// firstParentPositions walks the first-parent chain from hash and returns a
// map of commit hash → position (0 = tip, 1 = first parent, etc.). Bounded
// by inferWalkLimit to prevent unbounded growth.
func (c *GitClient) firstParentPositions(hash plumbing.Hash) map[plumbing.Hash]int {
	positions := make(map[plumbing.Hash]int, inferWalkLimit)
	for step := range inferWalkLimit {
		co, err := c.repo.CommitObject(hash)
		if err != nil {
			break
		}
		positions[hash] = step
		if len(co.ParentHashes) == 0 {
			break
		}
		hash = co.ParentHashes[0]
	}
	return positions
}

// branchOlder returns true if a was created before b, using CreatedTime from
// reflog. Falls back to AuthorTime (tip commit) when reflog is unavailable,
// preferring the older timestamp. Final tiebreak: lexicographic name order
// for determinism.
func branchOlder(a, b BranchInfo) bool {
	ta := a.CreatedTime
	if ta.IsZero() {
		ta = a.AuthorTime
	}
	tb := b.CreatedTime
	if tb.IsZero() {
		tb = b.AuthorTime
	}
	if !ta.Equal(tb) {
		return ta.Before(tb)
	}
	return a.Name < b.Name
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
	for {
		ref, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		tags = append(tags, c.tagInfoFromRef(ref))
	}

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
