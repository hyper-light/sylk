package git

import (
	"fmt"
	"time"

	gogit "github.com/go-git/go-git/v5"
)

// ---------------------------------------------------------------------------
// State operations
// ---------------------------------------------------------------------------

func (b *GitBus) RepoPath() string {
	return dispatchPure(b, OpRepoPath, nil, func() string { return b.client.RepoPath() })
}

func (b *GitBus) IsGitRepo() bool {
	return dispatchPure(b, OpIsGitRepo, nil, func() bool { return b.client.IsGitRepo() })
}

func (b *GitBus) Repository() *gogit.Repository {
	return dispatchPure(b, OpRepository, nil, func() *gogit.Repository { return b.client.Repository() })
}

func (b *GitBus) Close() error {
	return dispatchVoid(b, OpClose, nil, func() error { return b.client.Close() })
}

func (b *GitBus) IsValidCommit(ref string) bool {
	return dispatchPure(b, OpIsValidCommit, ref, func() bool { return b.client.IsValidCommit(ref) })
}

// ---------------------------------------------------------------------------
// Reference operations
// ---------------------------------------------------------------------------

func (b *GitBus) GetHead() (string, error) {
	return dispatch(b, OpGetHead, nil, func() (string, error) { return b.client.GetHead() })
}

func (b *GitBus) GetBranch() (string, error) {
	return dispatch(b, OpGetBranch, nil, func() (string, error) { return b.client.GetBranch() })
}

func (b *GitBus) GetRemote(name string) (string, error) {
	return dispatch(b, OpGetRemote, name, func() (string, error) { return b.client.GetRemote(name) })
}

func (b *GitBus) GetHeadCommit() (string, error) {
	return dispatch(b, OpGetHeadCommit, nil, func() (string, error) { return b.client.GetHeadCommit() })
}

func (b *GitBus) DefaultBranch() string {
	return dispatchPure(b, OpDefaultBranch, nil, func() string { return b.client.DefaultBranch() })
}

// ---------------------------------------------------------------------------
// Branch operations
// ---------------------------------------------------------------------------

func (b *GitBus) ListBranches() ([]BranchInfo, error) {
	return dispatch(b, OpListBranches, nil, func() ([]BranchInfo, error) { return b.client.ListBranches() })
}

func (b *GitBus) BranchTipHash(name string) (string, error) {
	return dispatch(b, OpBranchTipHash, name, func() (string, error) { return b.client.BranchTipHash(name) })
}

func (b *GitBus) CheckoutBranch(name string) error {
	return dispatchVoid(b, OpCheckoutBranch, name, func() error { return b.client.CheckoutBranch(name) })
}

func (b *GitBus) CheckoutCommit(commitHash string) error {
	return dispatchVoid(b, OpCheckoutCommit, commitHash,
		func() error { return b.client.CheckoutCommit(commitHash) })
}

func (b *GitBus) CreateBranch(name, atCommitHash string) error {
	return dispatchVoid(b, OpCreateBranch, [2]string{name, atCommitHash},
		func() error { return b.client.CreateBranch(name, atCommitHash) })
}

func (b *GitBus) DeleteBranch(name string) error {
	return dispatchVoid(b, OpDeleteBranch, name, func() error { return b.client.DeleteBranch(name) })
}

func (b *GitBus) MergeBranch(sourceBranch, targetBranch string) error {
	return dispatchVoid(b, OpMergeBranch, [2]string{sourceBranch, targetBranch},
		func() error { return b.client.MergeBranch(sourceBranch, targetBranch) })
}

func (b *GitBus) ListTags() ([]TagInfo, error) {
	return dispatch(b, OpListTags, nil, func() ([]TagInfo, error) { return b.client.ListTags() })
}

func (b *GitBus) InferBranchParents(branches []BranchInfo, defaultBranch string) map[string]string {
	return dispatchPure(b, OpInferBranchParents, nil,
		func() map[string]string { return b.client.InferBranchParents(branches, defaultBranch) })
}

// ---------------------------------------------------------------------------
// Commit operations
// ---------------------------------------------------------------------------

func (b *GitBus) GetCommit(hash string) (*CommitInfo, error) {
	return dispatch(b, OpGetCommit, hash, func() (*CommitInfo, error) { return b.client.GetCommit(hash) })
}

func (b *GitBus) GetCommitStats(hash string) (int, int, error) {
	return dispatchTriple(b, OpGetCommitStats, hash,
		func() (int, int, error) { return b.client.GetCommitStats(hash) })
}

func (b *GitBus) GetCommitDiffSummary(hash string) (DiffSummary, error) {
	return dispatch(b, OpGetCommitDiffSummary, hash,
		func() (DiffSummary, error) { return b.client.GetCommitDiffSummary(hash) })
}

func (b *GitBus) GetCommitDiffSummaries(hashes []string) map[string]DiffSummary {
	return dispatchPure(b, OpGetCommitDiffSummaries, hashes,
		func() map[string]DiffSummary { return b.client.GetCommitDiffSummaries(hashes) })
}

func (b *GitBus) GetCommitFileSummaries(hashes []string) map[string]FileSummary {
	return dispatchPure(b, OpGetCommitFileSummaries, hashes,
		func() map[string]FileSummary { return b.client.GetCommitFileSummaries(hashes) })
}

func (b *GitBus) GetAllCommits(limit int) ([]*CommitInfo, error) {
	return dispatch(b, OpGetAllCommits, limit,
		func() ([]*CommitInfo, error) { return b.client.GetAllCommits(limit) })
}

func (b *GitBus) GetAllCommitsPage(skip, limit int) ([]*CommitInfo, bool, error) {
	return dispatchTriple(b, OpGetAllCommitsPage, [2]int{skip, limit},
		func() ([]*CommitInfo, bool, error) { return b.client.GetAllCommitsPage(skip, limit) })
}

func (b *GitBus) GetCommitsSince(since time.Time) ([]*CommitInfo, error) {
	return dispatch(b, OpGetCommitsSince, since,
		func() ([]*CommitInfo, error) { return b.client.GetCommitsSince(since) })
}

func (b *GitBus) GetFilesInCommit(commitHash string) ([]string, error) {
	return dispatch(b, OpGetFilesInCommit, commitHash,
		func() ([]string, error) { return b.client.GetFilesInCommit(commitHash) })
}

func (b *GitBus) ListCommitsForTree(limit int) ([]TreeCommit, error) {
	return dispatch(b, OpListCommitsForTree, limit,
		func() ([]TreeCommit, error) { return b.client.ListCommitsForTree(limit) })
}

func (b *GitBus) ListCommitsForBranch(branchName string, limit int) ([]TreeCommit, error) {
	return dispatch(b, OpListCommitsForBranch, [2]any{branchName, limit},
		func() ([]TreeCommit, error) { return b.client.ListCommitsForBranch(branchName, limit) })
}

func (b *GitBus) ListBranchDAGCommits(branchName, baseBranch string, limit int) ([]TreeCommit, error) {
	return dispatch(b, OpListBranchDAGCommits, [3]any{branchName, baseBranch, limit},
		func() ([]TreeCommit, error) { return b.client.ListBranchDAGCommits(branchName, baseBranch, limit) })
}

// ---------------------------------------------------------------------------
// Branch commit operations
// ---------------------------------------------------------------------------

func (b *GitBus) ListBranchOnlyCommits(branchName, baseBranch, afterHash string, pageSize int) ([]TreeCommit, bool, error) {
	return dispatchTriple(b, OpListBranchOnlyCommits,
		[4]any{branchName, baseBranch, afterHash, pageSize},
		func() ([]TreeCommit, bool, error) {
			return b.client.ListBranchOnlyCommits(branchName, baseBranch, afterHash, pageSize)
		})
}

func (b *GitBus) CountBranchOnlyCommits(branchName, baseBranch string, limit int) (int, bool, error) {
	return dispatchTriple(b, OpCountBranchOnlyCommits,
		[3]any{branchName, baseBranch, limit},
		func() (int, bool, error) { return b.client.CountBranchOnlyCommits(branchName, baseBranch, limit) })
}

func (b *GitBus) CountBranchOnlyCommitsBatch(branchNames []string, baseBranch string, limit int) map[string]BranchOnlyCount {
	return dispatchPure(b, OpCountBranchOnlyCommitsBatch,
		[3]any{branchNames, baseBranch, limit},
		func() map[string]BranchOnlyCount {
			return b.client.CountBranchOnlyCommitsBatch(branchNames, baseBranch, limit)
		})
}

func (b *GitBus) CountBranchCommits(name string, limit int) (int, bool, error) {
	return dispatchTriple(b, OpCountBranchCommits, [2]any{name, limit},
		func() (int, bool, error) { return b.client.CountBranchCommits(name, limit) })
}

func (b *GitBus) CommitFiles(paths []string, message string) error {
	return dispatchVoid(b, OpCommitFiles, [2]any{paths, message},
		func() error { return b.client.CommitFiles(paths, message) })
}

// ---------------------------------------------------------------------------
// Diff operations
// ---------------------------------------------------------------------------

func (b *GitBus) GetDiff(fromHash, toHash string) ([]FileDiff, error) {
	return dispatch(b, OpGetDiff, [2]string{fromHash, toHash},
		func() ([]FileDiff, error) { return b.client.GetDiff(fromHash, toHash) })
}

func (b *GitBus) GetWorkingTreeDiff() ([]FileDiff, error) {
	return dispatch(b, OpGetWorkingTreeDiff, nil,
		func() ([]FileDiff, error) { return b.client.GetWorkingTreeDiff() })
}

func (b *GitBus) GetStagedDiff() ([]FileDiff, error) {
	return dispatch(b, OpGetStagedDiff, nil,
		func() ([]FileDiff, error) { return b.client.GetStagedDiff() })
}

func (b *GitBus) GetFileDiff(path, fromHash, toHash string) (*FileDiff, error) {
	return dispatch(b, OpGetFileDiff, [3]string{path, fromHash, toHash},
		func() (*FileDiff, error) { return b.client.GetFileDiff(path, fromHash, toHash) })
}

// ---------------------------------------------------------------------------
// Status operations
// ---------------------------------------------------------------------------

func (b *GitBus) WorktreeStatus() (*WorkingTreeStatus, error) {
	return dispatch(b, OpWorktreeStatus, nil,
		func() (*WorkingTreeStatus, error) { return b.client.WorktreeStatus() })
}

func (b *GitBus) TrackedSet() map[string]struct{} {
	return dispatchPure(b, OpTrackedSet, nil,
		func() map[string]struct{} { return b.client.TrackedSet() })
}

func (b *GitBus) UncommittedFileStatuses() (map[string]string, bool, error) {
	return dispatchTriple(b, OpUncommittedFileStatuses, nil,
		func() (map[string]string, bool, error) { return b.client.UncommittedFileStatuses() })
}

func (b *GitBus) GetUncommittedFiles() ([]string, error) {
	return dispatch(b, OpGetUncommittedFiles, nil,
		func() ([]string, error) { return b.client.GetUncommittedFiles() })
}

func (b *GitBus) GetUntrackedFiles() ([]string, error) {
	return dispatch(b, OpGetUntrackedFiles, nil,
		func() ([]string, error) { return b.client.GetUntrackedFiles() })
}

func (b *GitBus) ListTrackedFiles() ([]string, error) {
	return dispatch(b, OpListTrackedFiles, nil,
		func() ([]string, error) { return b.client.ListTrackedFiles() })
}

// ---------------------------------------------------------------------------
// Modified-file operations
// ---------------------------------------------------------------------------

func (b *GitBus) ListModifiedFiles(since time.Time) ([]string, error) {
	return dispatch(b, OpListModifiedFiles, since,
		func() ([]string, error) { return b.client.ListModifiedFiles(since) })
}

func (b *GitBus) ListModifiedFilesSinceCommit(commitHash string) ([]string, error) {
	return dispatch(b, OpListModifiedFilesSinceCommit, commitHash,
		func() ([]string, error) { return b.client.ListModifiedFilesSinceCommit(commitHash) })
}

// ---------------------------------------------------------------------------
// History operations
// ---------------------------------------------------------------------------

func (b *GitBus) GetFileHistory(path string, opts FileHistoryOptions) ([]*CommitInfo, error) {
	return dispatch(b, OpGetFileHistory, [2]any{path, opts},
		func() ([]*CommitInfo, error) { return b.client.GetFileHistory(path, opts) })
}

func (b *GitBus) GetFileAtCommit(path string, commitHash string) ([]byte, error) {
	return dispatch(b, OpGetFileAtCommit, [2]string{path, commitHash},
		func() ([]byte, error) { return b.client.GetFileAtCommit(path, commitHash) })
}

// ---------------------------------------------------------------------------
// Blame operations
// ---------------------------------------------------------------------------

func (b *GitBus) GetBlameInfo(path string) (*BlameResult, error) {
	return dispatch(b, OpGetBlameInfo, path,
		func() (*BlameResult, error) { return b.client.GetBlameInfo(path) })
}

func (b *GitBus) GetBlameInfoCached(path string, cache *BlameCache) (*BlameResult, error) {
	return dispatch(b, OpGetBlameInfoCached, path,
		func() (*BlameResult, error) { return b.client.GetBlameInfoCached(path, cache) })
}

func (b *GitBus) GetBlameRange(path string, startLine, endLine int) ([]BlameLine, error) {
	return dispatch(b, OpGetBlameRange, [3]any{path, startLine, endLine},
		func() ([]BlameLine, error) { return b.client.GetBlameRange(path, startLine, endLine) })
}

// ---------------------------------------------------------------------------
// Stash operations
// ---------------------------------------------------------------------------

func (b *GitBus) StashFiles(paths []string) error {
	return dispatchVoid(b, OpStashFiles, paths, func() error { return b.client.StashFiles(paths) })
}

func (b *GitBus) UnstashFiles() error {
	return dispatchVoid(b, OpUnstashFiles, nil, func() error { return b.client.UnstashFiles() })
}

func (b *GitBus) HasStash() bool {
	return dispatchPure(b, OpHasStash, nil, func() bool { return b.client.HasStash() })
}

// ---------------------------------------------------------------------------
// Network operations
// ---------------------------------------------------------------------------

func (b *GitBus) PullBranch(branchName, remoteName string) error {
	return dispatchVoid(b, OpPullBranch, [2]string{branchName, remoteName},
		func() error { return b.client.PullBranch(branchName, remoteName) })
}

func (b *GitBus) PushBranch(branchName, remoteName string) error {
	return dispatchVoid(b, OpPushBranch, [2]string{branchName, remoteName},
		func() error { return b.client.PushBranch(branchName, remoteName) })
}

// ---------------------------------------------------------------------------
// Advanced operations
// ---------------------------------------------------------------------------

func (b *GitBus) CherryPick(commitHash string) error {
	return dispatchVoid(b, OpCherryPick, commitHash,
		func() error { return b.client.CherryPick(commitHash) })
}

func (b *GitBus) Rebase(ontoBranch string) error {
	return dispatchVoid(b, OpRebase, ontoBranch,
		func() error { return b.client.Rebase(ontoBranch) })
}

func (b *GitBus) Reset(commitHash string, mode ResetMode) error {
	return dispatchVoid(b, OpReset, [2]any{commitHash, mode},
		func() error { return b.client.Reset(commitHash, mode) })
}

func (b *GitBus) Revert(commitHash string) error {
	return dispatchVoid(b, OpRevert, commitHash,
		func() error { return b.client.Revert(commitHash) })
}

// ---------------------------------------------------------------------------
// Sequencer operations
// ---------------------------------------------------------------------------

func (b *GitBus) CherryPickSequence(hashes []string, targetBranch string) (*SequencerStatus, error) {
	return dispatch(b, OpCherryPickSequence, [2]any{hashes, targetBranch},
		func() (*SequencerStatus, error) { return b.client.CherryPickSequence(hashes, targetBranch) })
}

func (b *GitBus) RebaseInteractive(ontoBranch string, plan []RebasePlanEntry) (*SequencerStatus, error) {
	return dispatch(b, OpRebaseInteractive, [2]any{ontoBranch, plan},
		func() (*SequencerStatus, error) { return b.client.RebaseInteractive(ontoBranch, plan) })
}

func (b *GitBus) MergeSequence(sourceBranch, targetBranch string) (*SequencerStatus, error) {
	return dispatch(b, OpMergeSequence, [2]string{sourceBranch, targetBranch},
		func() (*SequencerStatus, error) { return b.client.MergeSequence(sourceBranch, targetBranch) })
}

func (b *GitBus) SequencerContinue() (*SequencerStatus, error) {
	return dispatch(b, OpSequencerContinue, nil,
		func() (*SequencerStatus, error) { return b.client.SequencerContinue() })
}

func (b *GitBus) SequencerBypass() (*SequencerStatus, error) {
	return dispatch(b, OpSequencerBypass, nil,
		func() (*SequencerStatus, error) { return b.client.SequencerBypass() })
}

func (b *GitBus) SequencerAbort() error {
	return dispatchVoid(b, OpSequencerAbort, nil,
		func() error { return b.client.SequencerAbort() })
}

func (b *GitBus) GetSequencerStatus() *SequencerStatus {
	return dispatchPure(b, OpSequencerStatus, nil,
		func() *SequencerStatus { return b.client.GetSequencerStatus() })
}

func (b *GitBus) ResolveConflictFile(path string, resolution int, oursHash, theirsHash string) error {
	return dispatchVoid(b, OpResolveConflictFile, [4]string{path, fmt.Sprintf("%d", resolution), oursHash, theirsHash},
		func() error { return b.client.ResolveConflictFile(path, resolution, oursHash, theirsHash) })
}

func (b *GitBus) ReadBlobContent(hashHex string) (string, error) {
	return dispatch(b, OpReadBlobContent, hashHex,
		func() (string, error) { return b.client.ReadBlobContent(hashHex) })
}

func (b *GitBus) WriteWorktreeFile(path, content string) error {
	return dispatchVoid(b, OpWriteWorktreeFile, [2]string{path, ""},
		func() error { return b.client.WriteWorktreeFile(path, content) })
}

// ---------------------------------------------------------------------------
// Preview operations
// ---------------------------------------------------------------------------

func (b *GitBus) PreviewMerge(sourceBranch, targetBranch string) (*ConflictPreviewResult, error) {
	return dispatch(b, OpPreviewMerge, [2]string{sourceBranch, targetBranch},
		func() (*ConflictPreviewResult, error) { return b.client.PreviewMerge(sourceBranch, targetBranch) })
}

func (b *GitBus) PreviewRebase(ontoBranch string) (*ConflictPreviewResult, error) {
	return dispatch(b, OpPreviewRebase, ontoBranch,
		func() (*ConflictPreviewResult, error) { return b.client.PreviewRebase(ontoBranch) })
}

func (b *GitBus) PreviewCherryPick(commitHashes []string) (*ConflictPreviewResult, error) {
	return dispatch(b, OpPreviewCherryPick, commitHashes,
		func() (*ConflictPreviewResult, error) { return b.client.PreviewCherryPick(commitHashes) })
}

// ---------------------------------------------------------------------------
// Guard operations
// ---------------------------------------------------------------------------

func (b *GitBus) ScanStagedLargeFiles(paths []string, sizeThreshold int64) ([]LargeFileEntry, error) {
	return dispatch(b, OpScanLargeFiles, [2]any{paths, sizeThreshold},
		func() ([]LargeFileEntry, error) { return b.client.ScanStagedLargeFiles(paths, sizeThreshold) })
}

func (b *GitBus) ScanStagedSecrets(paths []string) ([]SecretFinding, error) {
	return dispatch(b, OpScanSecrets, paths,
		func() ([]SecretFinding, error) { return b.client.ScanStagedSecrets(paths) })
}

// ---------------------------------------------------------------------------
// Divergence operations
// ---------------------------------------------------------------------------

func (b *GitBus) ComputeDivergence(branchName string) (*DivergenceInfo, error) {
	return dispatch(b, OpComputeDivergence, branchName,
		func() (*DivergenceInfo, error) { return b.client.ComputeDivergence(branchName) })
}

func (b *GitBus) ComputeDivergenceBatch(branches []string) ([]DivergenceInfo, error) {
	return dispatch(b, OpComputeDivergenceBatch, branches,
		func() ([]DivergenceInfo, error) { return b.client.ComputeDivergenceBatch(branches) })
}

// ---------------------------------------------------------------------------
// State inspection
// ---------------------------------------------------------------------------

func (b *GitBus) DetectSequencerFileState() *SequencerFileState {
	return dispatchPure(b, OpDetectSequencerFileState, nil,
		func() *SequencerFileState { return b.client.DetectSequencerFileState() })
}

// ---------------------------------------------------------------------------
// Abort preservation
// ---------------------------------------------------------------------------

func (b *GitBus) PreserveBeforeAbort(opName string) (*AbortPreservation, error) {
	return dispatch(b, OpPreserveBeforeAbort, opName,
		func() (*AbortPreservation, error) { return b.client.PreserveBeforeAbort(opName) })
}

// ---------------------------------------------------------------------------
// Integration detection
// ---------------------------------------------------------------------------

func (b *GitBus) DetectIntegratedCommits(commitHashes []string, targetBranch string) ([]IntegrationResult, error) {
	return dispatch(b, OpDetectIntegratedCommits, [2]any{commitHashes, targetBranch},
		func() ([]IntegrationResult, error) {
			return b.client.DetectIntegratedCommits(commitHashes, targetBranch)
		})
}

// ---------------------------------------------------------------------------
// Branch stash operations
// ---------------------------------------------------------------------------

func (b *GitBus) StashForBranch(branchName string, paths []string) (*BranchStashMeta, error) {
	return dispatch(b, OpStashForBranch, [2]any{branchName, paths},
		func() (*BranchStashMeta, error) { return b.client.StashForBranch(branchName, paths) })
}

func (b *GitBus) UnstashForBranch(branchName string) error {
	return dispatchVoid(b, OpUnstashForBranch, branchName,
		func() error { return b.client.UnstashForBranch(branchName) })
}

func (b *GitBus) HasBranchStash(branchName string) bool {
	return dispatchPure(b, OpHasBranchStash, branchName,
		func() bool { return b.client.HasBranchStash(branchName) })
}

func (b *GitBus) ListBranchStashes() (map[string][]BranchStashMeta, error) {
	return dispatch(b, OpListBranchStashes, nil,
		func() (map[string][]BranchStashMeta, error) { return b.client.ListBranchStashes() })
}
