package git

import (
	"errors"
	"fmt"
	"time"
)

// Invoke dispatches a git operation by snake_case name with map parameters.
// Returns (result, error).  result is nil for void operations.
// Unrecognised operation names return ErrUnknownOp.
func (b *GitBus) Invoke(op string, params map[string]any) (any, error) {
	entry, ok := invokeTable[op]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownOp, op)
	}
	return entry.fn(b, params)
}

// ErrUnknownOp is returned by Invoke for unrecognised operation names.
var ErrUnknownOp = errors.New("unknown git operation")

// OpInfo describes a registered operation for introspection.
type OpInfo struct {
	Name     string   // snake_case name, e.g. "get_diff"
	Category string   // e.g. "diff", "branch"
	Params   []string // parameter names (empty for no-param ops)
	Mutating bool
}

// ListOps returns descriptors for all operations available via Invoke.
func (b *GitBus) ListOps() []OpInfo {
	out := make([]OpInfo, 0, len(invokeTable))
	for name, entry := range invokeTable {
		out = append(out, OpInfo{
			Name:     name,
			Category: OpCategory(entry.op),
			Params:   entry.params,
			Mutating: IsMutating(entry.op),
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Invoke table
// ---------------------------------------------------------------------------

type invokeEntry struct {
	op     GitOp
	params []string
	fn     func(b *GitBus, p map[string]any) (any, error)
}

// invokeTable maps snake_case op names to their dispatch functions.
// Operations that require opaque Go types (Repository, GetBlameInfoCached,
// InferBranchParents) are excluded — they are only accessible via typed
// methods on GitBus.
var invokeTable = map[string]invokeEntry{
	// State
	"repo_path": {op: OpRepoPath, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.RepoPath(), nil
	}},
	"is_git_repo": {op: OpIsGitRepo, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.IsGitRepo(), nil
	}},
	"close": {op: OpClose, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return nil, b.Close()
	}},
	"is_valid_commit": {op: OpIsValidCommit, params: []string{"ref"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.IsValidCommit(str(p, "ref")), nil
	}},

	// Refs
	"get_head": {op: OpGetHead, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.GetHead()
	}},
	"get_branch": {op: OpGetBranch, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.GetBranch()
	}},
	"get_remote": {op: OpGetRemote, params: []string{"name"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetRemote(str(p, "name"))
	}},
	"get_head_commit": {op: OpGetHeadCommit, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.GetHeadCommit()
	}},
	"default_branch": {op: OpDefaultBranch, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.DefaultBranch(), nil
	}},

	// Branches
	"list_branches": {op: OpListBranches, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.ListBranches()
	}},
	"branch_tip_hash": {op: OpBranchTipHash, params: []string{"name"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.BranchTipHash(str(p, "name"))
	}},
	"checkout_branch": {op: OpCheckoutBranch, params: []string{"name"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.CheckoutBranch(str(p, "name"))
	}},
	"checkout_commit": {op: OpCheckoutCommit, params: []string{"commit_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.CheckoutCommit(str(p, "commit_hash"))
	}},
	"create_branch": {op: OpCreateBranch, params: []string{"name", "at_commit_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.CreateBranch(str(p, "name"), str(p, "at_commit_hash"))
	}},
	"delete_branch": {op: OpDeleteBranch, params: []string{"name"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.DeleteBranch(str(p, "name"))
	}},
	"merge_branch": {op: OpMergeBranch, params: []string{"source_branch", "target_branch"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.MergeBranch(str(p, "source_branch"), str(p, "target_branch"))
	}},
	"list_tags": {op: OpListTags, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.ListTags()
	}},

	// Commits
	"get_commit": {op: OpGetCommit, params: []string{"hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetCommit(str(p, "hash"))
	}},
	"get_commit_stats": {op: OpGetCommitStats, params: []string{"hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		add, del, err := b.GetCommitStats(str(p, "hash"))
		if err != nil {
			return nil, err
		}
		return [2]int{add, del}, nil
	}},
	"get_commit_diff_summary": {op: OpGetCommitDiffSummary, params: []string{"hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetCommitDiffSummary(str(p, "hash"))
	}},
	"get_commit_diff_summaries": {op: OpGetCommitDiffSummaries, params: []string{"hashes"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetCommitDiffSummaries(strSlice(p, "hashes")), nil
	}},
	"get_commit_file_summaries": {op: OpGetCommitFileSummaries, params: []string{"hashes"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetCommitFileSummaries(strSlice(p, "hashes")), nil
	}},
	"get_all_commits": {op: OpGetAllCommits, params: []string{"limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetAllCommits(intVal(p, "limit"))
	}},
	"get_all_commits_page": {op: OpGetAllCommitsPage, params: []string{"skip", "limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		commits, hasMore, err := b.GetAllCommitsPage(intVal(p, "skip"), intVal(p, "limit"))
		if err != nil {
			return nil, err
		}
		return pageResult[*CommitInfo]{Items: commits, HasMore: hasMore}, nil
	}},
	"get_commits_since": {op: OpGetCommitsSince, params: []string{"since"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetCommitsSince(timeVal(p, "since"))
	}},
	"get_files_in_commit": {op: OpGetFilesInCommit, params: []string{"commit_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetFilesInCommit(str(p, "commit_hash"))
	}},
	"list_commits_for_tree": {op: OpListCommitsForTree, params: []string{"limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.ListCommitsForTree(intVal(p, "limit"))
	}},
	"list_commits_for_branch": {op: OpListCommitsForBranch, params: []string{"branch_name", "limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.ListCommitsForBranch(str(p, "branch_name"), intVal(p, "limit"))
	}},
	"list_branch_dag_commits": {op: OpListBranchDAGCommits, params: []string{"branch_name", "base_branch", "limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.ListBranchDAGCommits(str(p, "branch_name"), str(p, "base_branch"), intVal(p, "limit"))
	}},

	// Branch commits
	"list_branch_only_commits": {op: OpListBranchOnlyCommits, params: []string{"branch_name", "base_branch", "after_hash", "page_size"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		commits, hasMore, err := b.ListBranchOnlyCommits(str(p, "branch_name"), str(p, "base_branch"), str(p, "after_hash"), intVal(p, "page_size"))
		if err != nil {
			return nil, err
		}
		return pageResult[TreeCommit]{Items: commits, HasMore: hasMore}, nil
	}},
	"count_branch_only_commits": {op: OpCountBranchOnlyCommits, params: []string{"branch_name", "base_branch", "limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		count, capped, err := b.CountBranchOnlyCommits(str(p, "branch_name"), str(p, "base_branch"), intVal(p, "limit"))
		if err != nil {
			return nil, err
		}
		return countResult{Count: count, Capped: capped}, nil
	}},
	"count_branch_only_commits_batch": {op: OpCountBranchOnlyCommitsBatch, params: []string{"branch_names", "base_branch", "limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.CountBranchOnlyCommitsBatch(strSlice(p, "branch_names"), str(p, "base_branch"), intVal(p, "limit")), nil
	}},
	"count_branch_commits": {op: OpCountBranchCommits, params: []string{"name", "limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		count, capped, err := b.CountBranchCommits(str(p, "name"), intVal(p, "limit"))
		if err != nil {
			return nil, err
		}
		return countResult{Count: count, Capped: capped}, nil
	}},
	"commit_files": {op: OpCommitFiles, params: []string{"paths", "message"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.CommitFiles(strSlice(p, "paths"), str(p, "message"))
	}},

	// Diff
	"get_diff": {op: OpGetDiff, params: []string{"from_hash", "to_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetDiff(str(p, "from_hash"), str(p, "to_hash"))
	}},
	"get_working_tree_diff": {op: OpGetWorkingTreeDiff, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.GetWorkingTreeDiff()
	}},
	"get_staged_diff": {op: OpGetStagedDiff, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.GetStagedDiff()
	}},
	"get_file_diff": {op: OpGetFileDiff, params: []string{"path", "from_hash", "to_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetFileDiff(str(p, "path"), str(p, "from_hash"), str(p, "to_hash"))
	}},

	// Status
	"worktree_status": {op: OpWorktreeStatus, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.WorktreeStatus()
	}},
	"tracked_set": {op: OpTrackedSet, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.TrackedSet(), nil
	}},
	"uncommitted_file_statuses": {op: OpUncommittedFileStatuses, fn: func(b *GitBus, _ map[string]any) (any, error) {
		statuses, hasStaged, err := b.UncommittedFileStatuses()
		if err != nil {
			return nil, err
		}
		return uncommittedResult{Statuses: statuses, HasStaged: hasStaged}, nil
	}},
	"get_uncommitted_files": {op: OpGetUncommittedFiles, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.GetUncommittedFiles()
	}},
	"get_untracked_files": {op: OpGetUntrackedFiles, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.GetUntrackedFiles()
	}},
	"list_tracked_files": {op: OpListTrackedFiles, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.ListTrackedFiles()
	}},

	// Modified
	"list_modified_files": {op: OpListModifiedFiles, params: []string{"since"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.ListModifiedFiles(timeVal(p, "since"))
	}},
	"list_modified_files_since_commit": {op: OpListModifiedFilesSinceCommit, params: []string{"commit_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.ListModifiedFilesSinceCommit(str(p, "commit_hash"))
	}},

	// History
	"get_file_history": {op: OpGetFileHistory, params: []string{"path", "limit"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		opts := FileHistoryOptions{Limit: intVal(p, "limit")}
		return b.GetFileHistory(str(p, "path"), opts)
	}},
	"get_file_at_commit": {op: OpGetFileAtCommit, params: []string{"path", "commit_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetFileAtCommit(str(p, "path"), str(p, "commit_hash"))
	}},

	// Blame
	"get_blame_info": {op: OpGetBlameInfo, params: []string{"path"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetBlameInfo(str(p, "path"))
	}},
	"get_blame_range": {op: OpGetBlameRange, params: []string{"path", "start_line", "end_line"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.GetBlameRange(str(p, "path"), intVal(p, "start_line"), intVal(p, "end_line"))
	}},

	// Stash
	"stash_files": {op: OpStashFiles, params: []string{"paths"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.StashFiles(strSlice(p, "paths"))
	}},
	"unstash_files": {op: OpUnstashFiles, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return nil, b.UnstashFiles()
	}},
	"has_stash": {op: OpHasStash, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.HasStash(), nil
	}},

	// Network
	"pull_branch": {op: OpPullBranch, params: []string{"branch_name", "remote_name"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.PullBranch(str(p, "branch_name"), str(p, "remote_name"))
	}},
	"push_branch": {op: OpPushBranch, params: []string{"branch_name", "remote_name"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.PushBranch(str(p, "branch_name"), str(p, "remote_name"))
	}},

	// Advanced
	"cherry_pick": {op: OpCherryPick, params: []string{"commit_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.CherryPick(str(p, "commit_hash"))
	}},
	"rebase": {op: OpRebase, params: []string{"onto_branch"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.Rebase(str(p, "onto_branch"))
	}},
	"reset": {op: OpReset, params: []string{"commit_hash", "mode"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.Reset(str(p, "commit_hash"), ResetMode(intVal(p, "mode")))
	}},
	"revert": {op: OpRevert, params: []string{"commit_hash"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return nil, b.Revert(str(p, "commit_hash"))
	}},

	// Sequencer
	"cherry_pick_sequence": {op: OpCherryPickSequence, params: []string{"hashes", "target_branch"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.CherryPickSequence(strSlice(p, "hashes"), str(p, "target_branch"))
	}},
	"rebase_interactive": {op: OpRebaseInteractive, params: []string{"onto_branch", "plan"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.RebaseInteractive(str(p, "onto_branch"), rebasePlanSlice(p, "plan"))
	}},
	"merge_sequence": {op: OpMergeSequence, params: []string{"source_branch", "target_branch"}, fn: func(b *GitBus, p map[string]any) (any, error) {
		return b.MergeSequence(str(p, "source_branch"), str(p, "target_branch"))
	}},
	"sequencer_continue": {op: OpSequencerContinue, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.SequencerContinue()
	}},
	"sequencer_bypass": {op: OpSequencerBypass, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.SequencerBypass()
	}},
	"sequencer_abort": {op: OpSequencerAbort, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return nil, b.SequencerAbort()
	}},
	"sequencer_status": {op: OpSequencerStatus, fn: func(b *GitBus, _ map[string]any) (any, error) {
		return b.GetSequencerStatus(), nil
	}},
}

// ---------------------------------------------------------------------------
// Result wrappers for multi-return invoke entries
// ---------------------------------------------------------------------------

// pageResult wraps paginated results for Invoke's single-return interface.
type pageResult[T any] struct {
	Items   []T  `json:"items"`
	HasMore bool `json:"has_more"`
}

// countResult wraps count + capped for Invoke's single-return interface.
type countResult struct {
	Count  int  `json:"count"`
	Capped bool `json:"capped"`
}

// uncommittedResult wraps uncommitted statuses for Invoke.
type uncommittedResult struct {
	Statuses  map[string]string `json:"statuses"`
	HasStaged bool              `json:"has_staged"`
}

// ---------------------------------------------------------------------------
// Param extraction helpers — zero-value on missing/wrong type
// ---------------------------------------------------------------------------

func str(p map[string]any, key string) string {
	v, _ := p[key].(string)
	return v
}

func intVal(p map[string]any, key string) int {
	switch v := p[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	}
	return 0
}

func strSlice(p map[string]any, key string) []string {
	switch v := p[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, elem := range v {
			if s, ok := elem.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func rebasePlanSlice(p map[string]any, key string) []RebasePlanEntry {
	switch v := p[key].(type) {
	case []RebasePlanEntry:
		return v
	case []any:
		out := make([]RebasePlanEntry, 0, len(v))
		for _, elem := range v {
			if m, ok := elem.(map[string]any); ok {
				out = append(out, RebasePlanEntry{
					Action: RebaseAction(intVal(m, "action")),
					Hash:   str(m, "hash"),
				})
			}
		}
		return out
	}
	return nil
}

func timeVal(p map[string]any, key string) time.Time {
	switch v := p[key].(type) {
	case time.Time:
		return v
	case string:
		t, _ := time.Parse(time.RFC3339, v)
		return t
	}
	return time.Time{}
}
