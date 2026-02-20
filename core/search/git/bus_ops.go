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
	OpCheckoutCommit     // checkout_commit
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
	OpReset      // reset
	OpRevert     // revert

	// Sequencer operations.
	OpCherryPickSequence  // cherry_pick_sequence
	OpRebaseInteractive   // rebase_interactive
	OpMergeSequence       // merge_sequence
	OpSequencerContinue   // sequencer_continue
	OpSequencerBypass     // sequencer_bypass
	OpSequencerAbort      // sequencer_abort
	OpSequencerStatus     // sequencer_status
	OpResolveConflictFile // resolve_conflict_file
	OpReadBlobContent     // read_blob_content
	OpWriteWorktreeFile   // write_worktree_file

	// Preview operations.
	OpPreviewMerge      // preview_merge
	OpPreviewRebase     // preview_rebase
	OpPreviewCherryPick // preview_cherry_pick

	// Guard operations.
	OpScanLargeFiles // scan_large_files
	OpScanSecrets    // scan_secrets

	// Divergence operations.
	OpComputeDivergence      // compute_divergence
	OpComputeDivergenceBatch // compute_divergence_batch

	// State inspection.
	OpDetectSequencerFileState // detect_sequencer_file_state

	// Abort preservation.
	OpPreserveBeforeAbort // preserve_before_abort

	// Integration detection.
	OpDetectIntegratedCommits // detect_integrated_commits

	// Branch stash operations.
	OpStashForBranch    // stash_for_branch
	OpUnstashForBranch  // unstash_for_branch
	OpHasBranchStash    // has_branch_stash
	OpListBranchStashes // list_branch_stashes

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
	OpCheckoutCommit:     "checkout_commit",
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
	OpReset:      "reset",
	OpRevert:     "revert",

	OpCherryPickSequence: "cherry_pick_sequence",
	OpRebaseInteractive:  "rebase_interactive",
	OpMergeSequence:      "merge_sequence",
	OpSequencerContinue:  "sequencer_continue",
	OpSequencerBypass:    "sequencer_bypass",
	OpSequencerAbort:     "sequencer_abort",
	OpSequencerStatus:     "sequencer_status",
	OpResolveConflictFile: "resolve_conflict_file",
	OpReadBlobContent:     "read_blob_content",
	OpWriteWorktreeFile:   "write_worktree_file",

	OpPreviewMerge:      "preview_merge",
	OpPreviewRebase:     "preview_rebase",
	OpPreviewCherryPick: "preview_cherry_pick",

	OpScanLargeFiles: "scan_large_files",
	OpScanSecrets:    "scan_secrets",

	OpComputeDivergence:      "compute_divergence",
	OpComputeDivergenceBatch: "compute_divergence_batch",

	OpDetectSequencerFileState: "detect_sequencer_file_state",

	OpPreserveBeforeAbort: "preserve_before_abort",

	OpDetectIntegratedCommits: "detect_integrated_commits",

	OpStashForBranch:    "stash_for_branch",
	OpUnstashForBranch:  "unstash_for_branch",
	OpHasBranchStash:    "has_branch_stash",
	OpListBranchStashes: "list_branch_stashes",
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
	OpCheckoutBranch: "branch", OpCheckoutCommit: "branch",
	OpCreateBranch: "branch",
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
	OpReset: "advanced", OpRevert: "advanced",

	OpCherryPickSequence: "sequencer", OpRebaseInteractive: "sequencer",
	OpMergeSequence: "sequencer", OpSequencerContinue: "sequencer",
	OpSequencerBypass: "sequencer", OpSequencerAbort: "sequencer",
	OpSequencerStatus: "sequencer", OpResolveConflictFile: "sequencer",
	OpReadBlobContent:   "sequencer",
	OpWriteWorktreeFile: "sequencer",

	OpPreviewMerge: "preview", OpPreviewRebase: "preview",
	OpPreviewCherryPick: "preview",

	OpScanLargeFiles: "guard", OpScanSecrets: "guard",

	OpComputeDivergence: "state", OpComputeDivergenceBatch: "state",

	OpDetectSequencerFileState: "state",

	OpPreserveBeforeAbort: "safety",

	OpDetectIntegratedCommits: "preview",

	OpStashForBranch: "stash", OpUnstashForBranch: "stash",
	OpHasBranchStash: "stash", OpListBranchStashes: "stash",
}

// OpCategory returns the category of the operation (e.g. "branch", "commit").
func OpCategory(op GitOp) string {
	if op >= 0 && op < opCount {
		return opCategories[op]
	}
	return "unknown"
}

// mutatingOps lists all operations that modify repository state.
// Used by IsMutating to build a runtime bitset.
var mutatingOps = [...]GitOp{
	OpClose,
	OpCheckoutBranch, OpCheckoutCommit, OpCreateBranch, OpDeleteBranch, OpMergeBranch,
	OpCommitFiles,
	OpStashFiles, OpUnstashFiles,
	OpPullBranch, OpPushBranch,
	OpCherryPick, OpRebase, OpReset, OpRevert,
	OpCherryPickSequence, OpRebaseInteractive, OpMergeSequence,
	OpSequencerContinue, OpSequencerBypass, OpSequencerAbort, OpResolveConflictFile,
	OpWriteWorktreeFile,
	OpPreserveBeforeAbort,
	OpStashForBranch, OpUnstashForBranch,
}

// mutatingBits is a bitset built at init time from mutatingOps.
var mutatingBits [2]uint64

func init() {
	for _, op := range mutatingOps {
		word := int(op) / 64
		bit := uint(op) % 64
		mutatingBits[word] |= 1 << bit
	}
}

// IsMutating returns true if the operation modifies repository state.
func IsMutating(op GitOp) bool {
	if op < 0 || op >= opCount {
		return false
	}
	word := int(op) / 64
	bit := uint(op) % 64
	return mutatingBits[word]&(1<<bit) != 0
}
