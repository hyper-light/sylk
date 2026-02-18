package git

// GitOp identifies a git operation for the event bus.
type GitOp int

const (
	// State operations.
	OpRepoPath    GitOp = iota // repo_path
	OpIsGitRepo                // is_git_repo
	OpRepository               // repository
	OpClose                    // close
	OpIsValidCommit            // is_valid_commit

	// Reference operations.
	OpGetHead       // get_head
	OpGetBranch     // get_branch
	OpGetRemote     // get_remote
	OpGetHeadCommit // get_head_commit
	OpDefaultBranch // default_branch

	// Branch operations.
	OpListBranches       // list_branches
	OpBranchTipHash      // branch_tip_hash
	OpCheckoutBranch     // checkout_branch
	OpCreateBranch       // create_branch
	OpDeleteBranch       // delete_branch
	OpMergeBranch        // merge_branch
	OpListTags           // list_tags
	OpInferBranchParents // infer_branch_parents

	// Commit operations.
	OpGetCommit              // get_commit
	OpGetCommitStats         // get_commit_stats
	OpGetCommitDiffSummary   // get_commit_diff_summary
	OpGetCommitDiffSummaries // get_commit_diff_summaries
	OpGetCommitFileSummaries // get_commit_file_summaries
	OpGetAllCommits          // get_all_commits
	OpGetAllCommitsPage      // get_all_commits_page
	OpGetCommitsSince        // get_commits_since
	OpGetFilesInCommit       // get_files_in_commit
	OpListCommitsForTree     // list_commits_for_tree
	OpListCommitsForBranch   // list_commits_for_branch
	OpListBranchDAGCommits   // list_branch_dag_commits

	// Branch commit operations.
	OpListBranchOnlyCommits      // list_branch_only_commits
	OpCountBranchOnlyCommits     // count_branch_only_commits
	OpCountBranchOnlyCommitsBatch // count_branch_only_commits_batch
	OpCountBranchCommits         // count_branch_commits
	OpCommitFiles                // commit_files

	// Diff operations.
	OpGetDiff            // get_diff
	OpGetWorkingTreeDiff // get_working_tree_diff
	OpGetStagedDiff      // get_staged_diff
	OpGetFileDiff        // get_file_diff

	// Status operations.
	OpWorktreeStatus          // worktree_status
	OpTrackedSet              // tracked_set
	OpUncommittedFileStatuses // uncommitted_file_statuses
	OpGetUncommittedFiles     // get_uncommitted_files
	OpGetUntrackedFiles       // get_untracked_files
	OpListTrackedFiles        // list_tracked_files

	// Modified-file operations.
	OpListModifiedFiles            // list_modified_files
	OpListModifiedFilesSinceCommit // list_modified_files_since_commit

	// History operations.
	OpGetFileHistory // get_file_history
	OpGetFileAtCommit // get_file_at_commit

	// Blame operations.
	OpGetBlameInfo       // get_blame_info
	OpGetBlameInfoCached // get_blame_info_cached
	OpGetBlameRange      // get_blame_range

	// Stash operations.
	OpStashFiles   // stash_files
	OpUnstashFiles // unstash_files
	OpHasStash     // has_stash

	// Network operations.
	OpPullBranch // pull_branch
	OpPushBranch // push_branch

	// Advanced operations.
	OpCherryPick // cherry_pick
	OpRebase     // rebase

	opCount // unexported sentinel — must remain last
)

// opNames maps each GitOp to its snake_case string.  Indexed by iota value.
var opNames = [opCount]string{
	OpRepoPath:      "repo_path",
	OpIsGitRepo:     "is_git_repo",
	OpRepository:    "repository",
	OpClose:         "close",
	OpIsValidCommit: "is_valid_commit",

	OpGetHead:       "get_head",
	OpGetBranch:     "get_branch",
	OpGetRemote:     "get_remote",
	OpGetHeadCommit: "get_head_commit",
	OpDefaultBranch: "default_branch",

	OpListBranches:       "list_branches",
	OpBranchTipHash:      "branch_tip_hash",
	OpCheckoutBranch:     "checkout_branch",
	OpCreateBranch:       "create_branch",
	OpDeleteBranch:       "delete_branch",
	OpMergeBranch:        "merge_branch",
	OpListTags:           "list_tags",
	OpInferBranchParents: "infer_branch_parents",

	OpGetCommit:              "get_commit",
	OpGetCommitStats:         "get_commit_stats",
	OpGetCommitDiffSummary:   "get_commit_diff_summary",
	OpGetCommitDiffSummaries: "get_commit_diff_summaries",
	OpGetCommitFileSummaries: "get_commit_file_summaries",
	OpGetAllCommits:          "get_all_commits",
	OpGetAllCommitsPage:      "get_all_commits_page",
	OpGetCommitsSince:        "get_commits_since",
	OpGetFilesInCommit:       "get_files_in_commit",
	OpListCommitsForTree:     "list_commits_for_tree",
	OpListCommitsForBranch:   "list_commits_for_branch",
	OpListBranchDAGCommits:   "list_branch_dag_commits",

	OpListBranchOnlyCommits:       "list_branch_only_commits",
	OpCountBranchOnlyCommits:      "count_branch_only_commits",
	OpCountBranchOnlyCommitsBatch: "count_branch_only_commits_batch",
	OpCountBranchCommits:          "count_branch_commits",
	OpCommitFiles:                 "commit_files",

	OpGetDiff:            "get_diff",
	OpGetWorkingTreeDiff: "get_working_tree_diff",
	OpGetStagedDiff:      "get_staged_diff",
	OpGetFileDiff:        "get_file_diff",

	OpWorktreeStatus:          "worktree_status",
	OpTrackedSet:              "tracked_set",
	OpUncommittedFileStatuses: "uncommitted_file_statuses",
	OpGetUncommittedFiles:     "get_uncommitted_files",
	OpGetUntrackedFiles:       "get_untracked_files",
	OpListTrackedFiles:        "list_tracked_files",

	OpListModifiedFiles:            "list_modified_files",
	OpListModifiedFilesSinceCommit: "list_modified_files_since_commit",

	OpGetFileHistory:  "get_file_history",
	OpGetFileAtCommit: "get_file_at_commit",

	OpGetBlameInfo:       "get_blame_info",
	OpGetBlameInfoCached: "get_blame_info_cached",
	OpGetBlameRange:      "get_blame_range",

	OpStashFiles:   "stash_files",
	OpUnstashFiles: "unstash_files",
	OpHasStash:     "has_stash",

	OpPullBranch: "pull_branch",
	OpPushBranch: "push_branch",

	OpCherryPick: "cherry_pick",
	OpRebase:     "rebase",
}

// opByName is the inverse of opNames, built at init time.
var opByName map[string]GitOp

func init() {
	opByName = make(map[string]GitOp, opCount)
	for i := range opCount {
		opByName[opNames[i]] = GitOp(i)
	}
}

// String returns the snake_case name of the operation.
func (op GitOp) String() string {
	if op >= 0 && op < opCount {
		return opNames[op]
	}
	return "unknown"
}

// ParseGitOp converts a snake_case name to a GitOp.
// Returns (op, true) on success or (0, false) if the name is unrecognised.
func ParseGitOp(name string) (GitOp, bool) {
	op, ok := opByName[name]
	return op, ok
}

// opCategories maps each op to its category string.
var opCategories = [opCount]string{
	OpRepoPath: "state", OpIsGitRepo: "state", OpRepository: "state",
	OpClose: "state", OpIsValidCommit: "state",

	OpGetHead: "ref", OpGetBranch: "ref", OpGetRemote: "ref",
	OpGetHeadCommit: "ref", OpDefaultBranch: "ref",

	OpListBranches: "branch", OpBranchTipHash: "branch",
	OpCheckoutBranch: "branch", OpCreateBranch: "branch",
	OpDeleteBranch: "branch", OpMergeBranch: "branch",
	OpListTags: "branch", OpInferBranchParents: "branch",

	OpGetCommit: "commit", OpGetCommitStats: "commit",
	OpGetCommitDiffSummary: "commit", OpGetCommitDiffSummaries: "commit",
	OpGetCommitFileSummaries: "commit", OpGetAllCommits: "commit",
	OpGetAllCommitsPage: "commit", OpGetCommitsSince: "commit",
	OpGetFilesInCommit: "commit", OpListCommitsForTree: "commit",
	OpListCommitsForBranch: "commit", OpListBranchDAGCommits: "commit",

	OpListBranchOnlyCommits: "commit", OpCountBranchOnlyCommits: "commit",
	OpCountBranchOnlyCommitsBatch: "commit", OpCountBranchCommits: "commit",
	OpCommitFiles: "commit",

	OpGetDiff: "diff", OpGetWorkingTreeDiff: "diff",
	OpGetStagedDiff: "diff", OpGetFileDiff: "diff",

	OpWorktreeStatus: "status", OpTrackedSet: "status",
	OpUncommittedFileStatuses: "status", OpGetUncommittedFiles: "status",
	OpGetUntrackedFiles: "status", OpListTrackedFiles: "status",

	OpListModifiedFiles: "modified", OpListModifiedFilesSinceCommit: "modified",

	OpGetFileHistory: "history", OpGetFileAtCommit: "history",

	OpGetBlameInfo: "blame", OpGetBlameInfoCached: "blame",
	OpGetBlameRange: "blame",

	OpStashFiles: "stash", OpUnstashFiles: "stash", OpHasStash: "stash",

	OpPullBranch: "network", OpPushBranch: "network",

	OpCherryPick: "advanced", OpRebase: "advanced",
}

// OpCategory returns the category of the operation (e.g. "branch", "commit").
func OpCategory(op GitOp) string {
	if op >= 0 && op < opCount {
		return opCategories[op]
	}
	return "unknown"
}

// mutatingBits is a compile-time bitset marking mutating operations.
// A single uint64 suffices because opCount < 64.
const mutatingBits uint64 = 1<<OpClose |
	1<<OpCheckoutBranch | 1<<OpCreateBranch | 1<<OpDeleteBranch | 1<<OpMergeBranch |
	1<<OpCommitFiles |
	1<<OpStashFiles | 1<<OpUnstashFiles |
	1<<OpPullBranch | 1<<OpPushBranch |
	1<<OpCherryPick | 1<<OpRebase

// IsMutating returns true if the operation modifies repository state.
func IsMutating(op GitOp) bool {
	if op < 0 || op >= opCount {
		return false
	}
	return mutatingBits&(1<<op) != 0
}
