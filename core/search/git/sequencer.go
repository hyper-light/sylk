package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// =============================================================================
// Errors
// =============================================================================

var (
	ErrSequencerActive   = errors.New("sequencer already active")
	ErrNoSequencer       = errors.New("no active sequencer")
	ErrSequencerNotReady = errors.New("sequencer not in conflict or edit state")
)

// =============================================================================
// Public Types
// =============================================================================

// SequencerOp identifies the type of multi-step operation.
type SequencerOp int

const (
	SeqCherryPick SequencerOp = iota
	SeqRebase
	SeqMerge
)

var seqOpNames = [...]string{SeqCherryPick: "cherry-pick", SeqRebase: "rebase", SeqMerge: "merge"}

// String returns the name of the sequencer operation.
func (op SequencerOp) String() string {
	if op >= 0 && int(op) < len(seqOpNames) {
		return seqOpNames[op]
	}
	return "unknown"
}

// SequencerState tracks the current state of the sequencer.
type SequencerState int

const (
	SeqIdle     SequencerState = iota
	SeqRunning                        // Processing steps
	SeqConflict                       // Paused on conflict
	SeqEdit                           // Rebase "edit" pause
)

// RebaseAction describes what to do with a commit during interactive rebase.
type RebaseAction int

const (
	RebasePick   RebaseAction = iota
	RebaseReword
	RebaseSquash
	RebaseFixup
	RebaseEdit
	RebaseDrop
)

// RebasePlanEntry is the public type for specifying a rebase plan.
type RebasePlanEntry struct {
	Action RebaseAction
	Hash   string
}

// SequencerStatus is the read-only status exposed to the UI.
type SequencerStatus struct {
	Op          SequencerOp
	State       SequencerState
	TotalSteps  int
	CurrentStep int
	Conflicts   []MergeConflict
	CurrentHash string // Short hash of commit being applied
	Subject     string // Subject line of current commit
	SourceName  string // Display name for the "theirs" side (branch or short sha)
	DestName    string // Display name for the "ours" side (branch name)
}

// =============================================================================
// Internal Types
// =============================================================================

type sequencerStep struct {
	commit  *object.Commit
	action  RebaseAction
	message string // Override for reword
}

type sequencer struct {
	op         SequencerOp
	state      SequencerState
	steps      []sequencerStep
	currentIdx int
	currentTip plumbing.Hash // Running tip of new commit chain

	// Merge-specific: both parents for the final merge commit.
	mergeSourceHash plumbing.Hash
	mergeTargetRef  plumbing.ReferenceName

	// Display names for conflict labels.
	sourceName string // "theirs" side: branch name or short sha
	destName   string // "ours" side: branch name

	// Conflict state
	conflicts []MergeConflict

	// Squash/fixup accumulation
	squashTree   plumbing.Hash
	squashParent plumbing.Hash
	squashMsgs   []string
	squashAuthor object.Signature

	// Rollback state
	originalHead plumbing.Hash
	originalRef  plumbing.ReferenceName
	detached     bool
}

// =============================================================================
// CherryPickSequence
// =============================================================================

// CherryPickSequence applies multiple commits onto the current (or target) branch.
// Returns status after the first step. On conflict, the sequencer pauses.
func (c *GitClient) CherryPickSequence(commitHashes []string, targetBranch string) (*SequencerStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}
	if c.sequencer != nil {
		return nil, ErrSequencerActive
	}

	// Resolve all commits upfront (fail fast).
	commits := make([]*object.Commit, len(commitHashes))
	for i, h := range commitHashes {
		cm, err := c.resolveCommit(h)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", h, err)
		}
		commits[i] = cm
	}

	// Save rollback state.
	seq, err := c.saveRollbackState()
	if err != nil {
		return nil, err
	}
	seq.op = SeqCherryPick
	if targetBranch != "" {
		seq.destName = targetBranch
	} else {
		seq.destName, _ = c.getBranchName()
	}

	// Checkout target branch if specified and different from current.
	if targetBranch != "" {
		if err := c.switchToTargetBranch(seq, targetBranch); err != nil {
			return nil, err
		}
	}

	// Build steps (all Pick).
	seq.steps = make([]sequencerStep, len(commits))
	for i, cm := range commits {
		seq.steps[i] = sequencerStep{commit: cm, action: RebasePick}
	}

	c.sequencer = seq
	return c.processNextStep()
}

// =============================================================================
// RebaseInteractive
// =============================================================================

// RebaseInteractive replays commits according to the given plan onto ontoBranch.
func (c *GitClient) RebaseInteractive(ontoBranch string, plan []RebasePlanEntry) (*SequencerStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}
	if c.sequencer != nil {
		return nil, ErrSequencerActive
	}

	// Resolve onto branch.
	ontoCommit, err := c.resolveOnto(ontoBranch)
	if err != nil {
		return nil, err
	}

	// Resolve plan entries to commits.
	steps := make([]sequencerStep, len(plan))
	for i, entry := range plan {
		cm, err := c.resolveCommit(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("resolve plan entry %d (%s): %w", i, entry.Hash, err)
		}
		steps[i] = sequencerStep{commit: cm, action: entry.Action}
	}

	// Save rollback state.
	seq, err := c.saveRollbackState()
	if err != nil {
		return nil, err
	}
	seq.op = SeqRebase
	seq.destName = ontoBranch
	seq.steps = steps

	// Detach HEAD at onto tip.
	if err := c.detachHead(ontoCommit.Hash); err != nil {
		return nil, err
	}
	seq.currentTip = ontoCommit.Hash

	c.sequencer = seq
	return c.processNextStep()
}

// =============================================================================
// MergeSequence
// =============================================================================

// MergeSequence performs a merge with conflict pause/resume support.
func (c *GitClient) MergeSequence(sourceBranch, targetBranch string) (*SequencerStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return nil, ErrNotGitRepo
	}
	if c.sequencer != nil {
		return nil, ErrSequencerActive
	}

	// Save rollback state.
	seq, err := c.saveRollbackState()
	if err != nil {
		return nil, err
	}
	seq.op = SeqMerge
	seq.sourceName = sourceBranch
	seq.destName = targetBranch

	// Checkout target branch.
	if err := c.switchToTargetBranch(seq, targetBranch); err != nil {
		return nil, err
	}

	sourceCommit, err := c.resolveBranchCommit(sourceBranch)
	if err != nil {
		return nil, err
	}

	targetCommit, err := c.headCommit()
	if err != nil {
		return nil, err
	}

	// Check if already up to date.
	isAnc, err := sourceCommit.IsAncestor(targetCommit)
	if err != nil {
		return nil, err
	}
	if isAnc {
		return nil, nil // Already up to date.
	}

	// Check for fast-forward.
	isFF, err := targetCommit.IsAncestor(sourceCommit)
	if err != nil {
		return nil, err
	}
	if isFF {
		wt, err := c.repo.Worktree()
		if err != nil {
			return nil, err
		}
		if err := c.updateRefAndReset(wt, seq.originalRef, sourceCommit.Hash); err != nil {
			return nil, err
		}
		return nil, nil // Fast-forward, no sequencer needed.
	}

	// 3-way merge.
	seq.mergeSourceHash = sourceCommit.Hash
	seq.mergeTargetRef = plumbing.NewBranchReferenceName(targetBranch)
	seq.steps = []sequencerStep{{commit: sourceCommit, action: RebasePick}}
	seq.currentTip = targetCommit.Hash

	bases, err := targetCommit.MergeBase(sourceCommit)
	if err != nil {
		return nil, err
	}
	if len(bases) == 0 {
		return nil, fmt.Errorf("no common ancestor for merge")
	}

	baseTree, err := bases[0].Tree()
	if err != nil {
		return nil, err
	}

	targetTree, err := targetCommit.Tree()
	if err != nil {
		return nil, err
	}

	sourceTree, err := sourceCommit.Tree()
	if err != nil {
		return nil, err
	}

	result, err := treeMerge3(c.repo.Storer, baseTree, targetTree, sourceTree)
	if err != nil {
		return nil, err
	}

	if result.HasConflicts() {
		if err := c.writeConflictsToWorktree(result.Conflicts); err != nil {
			return nil, err
		}
		seq.conflicts = result.Conflicts
		seq.state = SeqConflict
		seq.squashTree = result.TreeHash
		c.sequencer = seq
		return c.buildStatus(), nil
	}

	// Clean merge — commit and finish.
	msg := fmt.Sprintf("Merge branch '%s' into %s\n", sourceBranch, targetBranch)
	sig := defaultSignature(time.Now())
	mergeHash, err := storeCommitObj(
		c.repo.Storer, result.TreeHash,
		[]plumbing.Hash{targetCommit.Hash, sourceCommit.Hash},
		sig, sig, msg,
	)
	if err != nil {
		return nil, err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return nil, err
	}
	if err := c.updateRefAndReset(wt, seq.mergeTargetRef, mergeHash); err != nil {
		return nil, err
	}

	return nil, nil // Complete.
}

// =============================================================================
// SequencerContinue / Bypass / Abort / Status
// =============================================================================

// SequencerContinue resolves conflicts from the worktree and continues.
func (c *GitClient) SequencerContinue() (*SequencerStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sequencer == nil {
		return nil, ErrNoSequencer
	}
	if c.sequencer.state != SeqConflict && c.sequencer.state != SeqEdit {
		return nil, ErrSequencerNotReady
	}

	if c.sequencer.state == SeqConflict {
		resolvedTree, err := c.resolveConflictsFromWorktree()
		if err != nil {
			return nil, err
		}
		if err := c.commitCurrentStep(resolvedTree); err != nil {
			return nil, err
		}
	}

	c.sequencer.conflicts = nil
	c.sequencer.currentIdx++
	c.sequencer.state = SeqRunning
	return c.processNextStep()
}

// SequencerBypass commits the worktree as-is (conflict markers included).
func (c *GitClient) SequencerBypass() (*SequencerStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sequencer == nil {
		return nil, ErrNoSequencer
	}
	if c.sequencer.state != SeqConflict && c.sequencer.state != SeqEdit {
		return nil, ErrSequencerNotReady
	}

	resolvedTree, err := c.resolveConflictsFromWorktree()
	if err != nil {
		return nil, err
	}
	if err := c.commitCurrentStep(resolvedTree); err != nil {
		return nil, err
	}

	c.sequencer.conflicts = nil
	c.sequencer.currentIdx++
	c.sequencer.state = SeqRunning
	return c.processNextStep()
}

// SequencerAbort rolls back to the original state.
func (c *GitClient) SequencerAbort() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sequencer == nil {
		return ErrNoSequencer
	}

	return c.abortSequencer()
}

// GetSequencerStatus returns the current sequencer status, or nil if idle.
func (c *GitClient) GetSequencerStatus() *SequencerStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.sequencer == nil {
		return nil
	}
	return c.buildStatus()
}

// =============================================================================
// Internal: Step Processing
// =============================================================================

// processNextStep advances the sequencer. Must be called with c.mu held.
func (c *GitClient) processNextStep() (*SequencerStatus, error) {
	seq := c.sequencer

	// Flush any pending squash group at end of plan.
	if seq.currentIdx >= len(seq.steps) {
		if err := c.flushSquashGroup(); err != nil {
			return nil, err
		}
		return c.completeSequencer()
	}

	step := &seq.steps[seq.currentIdx]

	switch step.action {
	case RebaseDrop:
		seq.currentIdx++
		return c.processNextStep()

	case RebaseEdit:
		if err := c.applyStep(step); err != nil {
			return nil, err
		}
		seq.state = SeqEdit
		return c.buildStatus(), nil

	case RebaseSquash, RebaseFixup:
		return c.handleSquashFixup(step)

	default: // Pick, Reword
		// Flush any pending squash group before a new pick.
		if err := c.flushSquashGroup(); err != nil {
			return nil, err
		}
		return c.handlePickReword(step)
	}
}

// handlePickReword applies a pick or reword step.
func (c *GitClient) handlePickReword(step *sequencerStep) (*SequencerStatus, error) {
	result, err := c.applyStepMerge(step)
	if err != nil {
		return nil, err
	}

	if result.HasConflicts() {
		return c.pauseOnConflict(result)
	}

	msg := step.commit.Message
	if step.action == RebaseReword && step.message != "" {
		msg = step.message
	}

	if err := c.commitStep(step, result.TreeHash, msg); err != nil {
		return nil, err
	}

	c.sequencer.currentIdx++
	return c.processNextStep()
}

// handleSquashFixup accumulates a squash or fixup step.
func (c *GitClient) handleSquashFixup(step *sequencerStep) (*SequencerStatus, error) {
	seq := c.sequencer

	result, err := c.applyStepMerge(step)
	if err != nil {
		return nil, err
	}

	if result.HasConflicts() {
		return c.pauseOnConflict(result)
	}

	// Initialize squash group if needed.
	if len(seq.squashMsgs) == 0 {
		seq.squashParent = seq.currentTip
		// The author comes from the pick that preceded this squash group.
		// At this point currentIdx > 0 and the previous step was the pick.
		if seq.currentIdx > 0 {
			seq.squashAuthor = seq.steps[seq.currentIdx-1].commit.Author
		} else {
			seq.squashAuthor = step.commit.Author
		}
	}

	seq.squashTree = result.TreeHash

	if step.action == RebaseSquash {
		seq.squashMsgs = append(seq.squashMsgs, step.commit.Message)
	}
	// Fixup: don't add message.

	seq.currentIdx++
	return c.processNextStep()
}

// =============================================================================
// Internal: Merge & Commit Helpers
// =============================================================================

// applyStepMerge performs the 3-way merge for a step against the current tip.
func (c *GitClient) applyStepMerge(step *sequencerStep) (*TreeMergeResult, error) {
	baseTree, err := commitParentTree(c, step.commit)
	if err != nil {
		return nil, err
	}

	commitTree, err := step.commit.Tree()
	if err != nil {
		return nil, err
	}

	tipCommit, err := c.repo.CommitObject(c.sequencer.currentTip)
	if err != nil {
		return nil, err
	}

	oursTree, err := tipCommit.Tree()
	if err != nil {
		return nil, err
	}

	return treeMerge3(c.repo.Storer, baseTree, oursTree, commitTree)
}

// applyStep applies a step (for edit action — commits immediately).
func (c *GitClient) applyStep(step *sequencerStep) error {
	result, err := c.applyStepMerge(step)
	if err != nil {
		return err
	}

	if result.HasConflicts() {
		if err := c.writeConflictsToWorktree(result.Conflicts); err != nil {
			return err
		}
		c.sequencer.conflicts = result.Conflicts
		c.sequencer.state = SeqConflict
		return nil
	}

	return c.commitStep(step, result.TreeHash, step.commit.Message)
}

// commitStep creates a commit for the current step and advances the tip.
func (c *GitClient) commitStep(step *sequencerStep, treeHash plumbing.Hash, msg string) error {
	committer := defaultSignature(time.Now())
	newHash, err := storeCommitObj(
		c.repo.Storer, treeHash,
		[]plumbing.Hash{c.sequencer.currentTip},
		step.commit.Author, committer, msg,
	)
	if err != nil {
		return err
	}

	c.sequencer.currentTip = newHash
	return c.updateHEADDirect(newHash)
}

// commitCurrentStep commits after conflict resolution for the current step.
func (c *GitClient) commitCurrentStep(treeHash plumbing.Hash) error {
	seq := c.sequencer

	if seq.op == SeqMerge {
		return c.commitMergeStep(treeHash)
	}

	step := &seq.steps[seq.currentIdx]

	// If we're in a squash/fixup group, just update the tree.
	if step.action == RebaseSquash || step.action == RebaseFixup {
		seq.squashTree = treeHash
		if step.action == RebaseSquash {
			seq.squashMsgs = append(seq.squashMsgs, step.commit.Message)
		}
		return nil
	}

	msg := step.commit.Message
	if step.action == RebaseReword && step.message != "" {
		msg = step.message
	}

	return c.commitStep(step, treeHash, msg)
}

// commitMergeStep creates the merge commit with two parents.
func (c *GitClient) commitMergeStep(treeHash plumbing.Hash) error {
	seq := c.sequencer
	sig := defaultSignature(time.Now())

	targetBranch := seq.mergeTargetRef.Short()
	msg := fmt.Sprintf("Merge into %s\n", targetBranch)

	mergeHash, err := storeCommitObj(
		c.repo.Storer, treeHash,
		[]plumbing.Hash{seq.currentTip, seq.mergeSourceHash},
		sig, sig, msg,
	)
	if err != nil {
		return err
	}

	seq.currentTip = mergeHash
	return nil
}

// flushSquashGroup commits the accumulated squash group if any.
func (c *GitClient) flushSquashGroup() error {
	seq := c.sequencer
	if len(seq.squashMsgs) == 0 && seq.squashTree == plumbing.ZeroHash {
		return nil
	}

	// If squashTree is zero, nothing to flush.
	if seq.squashTree == plumbing.ZeroHash {
		return nil
	}

	msg := strings.Join(seq.squashMsgs, "\n\n")
	if msg == "" {
		// Pure fixup group — use the pick commit's message.
		if seq.currentIdx > 0 {
			msg = c.findGroupPickMessage(seq.currentIdx)
		}
	}

	committer := defaultSignature(time.Now())
	parent := seq.squashParent
	if parent == plumbing.ZeroHash {
		parent = seq.currentTip
	}

	newHash, err := storeCommitObj(
		c.repo.Storer, seq.squashTree,
		[]plumbing.Hash{parent},
		seq.squashAuthor, committer, msg,
	)
	if err != nil {
		return err
	}

	seq.currentTip = newHash
	if err := c.updateHEADDirect(newHash); err != nil {
		return err
	}

	// Reset squash state.
	seq.squashTree = plumbing.ZeroHash
	seq.squashParent = plumbing.ZeroHash
	seq.squashMsgs = nil
	seq.squashAuthor = object.Signature{}

	return nil
}

// findGroupPickMessage walks backward from idx to find the pick commit message.
func (c *GitClient) findGroupPickMessage(idx int) string {
	for i := idx - 1; i >= 0; i-- {
		if c.sequencer.steps[i].action == RebasePick || c.sequencer.steps[i].action == RebaseReword {
			return c.sequencer.steps[i].commit.Message
		}
	}
	return "squashed commits"
}

// pauseOnConflict writes conflicts to worktree and pauses the sequencer.
func (c *GitClient) pauseOnConflict(result *TreeMergeResult) (*SequencerStatus, error) {
	if err := c.writeConflictsToWorktree(result.Conflicts); err != nil {
		return nil, err
	}

	seq := c.sequencer
	seq.conflicts = result.Conflicts
	seq.state = SeqConflict
	return c.buildStatus(), nil
}

// =============================================================================
// Internal: Worktree Conflict Resolution
// =============================================================================

// resolveConflictsFromWorktree reads the current worktree state for all
// conflict paths and builds a new tree with the resolved content.
func (c *GitClient) resolveConflictsFromWorktree() (plumbing.Hash, error) {
	seq := c.sequencer

	// Start from the tip tree.
	tipCommit, err := c.repo.CommitObject(seq.currentTip)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	tipTree, err := tipCommit.Tree()
	if err != nil {
		return plumbing.ZeroHash, err
	}

	mods := make(map[string]plumbing.Hash, len(seq.conflicts))
	for _, cf := range seq.conflicts {
		content, err := c.readWorktreeFile(cf.Path)
		if err != nil {
			if os.IsNotExist(err) {
				mods[cf.Path] = plumbing.ZeroHash // Deleted.
				continue
			}
			return plumbing.ZeroHash, fmt.Errorf("read resolved %s: %w", cf.Path, err)
		}

		hash, err := storeBlob(c.repo.Storer, content)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		mods[cf.Path] = hash
	}

	return modifyTree(c.repo.Storer, tipTree, mods)
}

// readWorktreeFile reads a file from the working directory.
func (c *GitClient) readWorktreeFile(path string) ([]byte, error) {
	fullPath := filepath.Join(c.repoPath, path)
	return os.ReadFile(fullPath)
}

// =============================================================================
// Internal: Completion & Abort
// =============================================================================

// completeSequencer finishes the operation, updates refs, resets worktree.
func (c *GitClient) completeSequencer() (*SequencerStatus, error) {
	seq := c.sequencer

	switch seq.op {
	case SeqCherryPick:
		if err := c.finishCherryPickSeq(); err != nil {
			return nil, err
		}
	case SeqRebase:
		if err := c.finishRebaseSeq(); err != nil {
			return nil, err
		}
	case SeqMerge:
		if err := c.finishMergeSeq(); err != nil {
			return nil, err
		}
	}

	c.sequencer = nil
	return nil, nil // nil status = completed
}

// finishCherryPickSeq resets worktree to the final tip.
func (c *GitClient) finishCherryPickSeq() error {
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&gogit.ResetOptions{
		Commit: c.sequencer.currentTip,
		Mode:   gogit.HardReset,
	})
}

// finishRebaseSeq updates the original branch ref to the final tip and reattaches HEAD.
func (c *GitClient) finishRebaseSeq() error {
	seq := c.sequencer

	if !seq.detached {
		// Update the original branch to point at the rebased tip.
		ref := plumbing.NewHashReference(seq.originalRef, seq.currentTip)
		if err := c.repo.Storer.SetReference(ref); err != nil {
			return err
		}

		// Re-attach HEAD to the branch.
		symRef := plumbing.NewSymbolicReference(plumbing.HEAD, seq.originalRef)
		if err := c.repo.Storer.SetReference(symRef); err != nil {
			return err
		}
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&gogit.ResetOptions{
		Commit: seq.currentTip,
		Mode:   gogit.HardReset,
	})
}

// finishMergeSeq updates the target branch ref after a merge.
func (c *GitClient) finishMergeSeq() error {
	seq := c.sequencer

	ref := plumbing.NewHashReference(seq.mergeTargetRef, seq.currentTip)
	if err := c.repo.Storer.SetReference(ref); err != nil {
		return err
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&gogit.ResetOptions{
		Commit: seq.currentTip,
		Mode:   gogit.HardReset,
	})
}

// abortSequencer restores the original HEAD and branch.
func (c *GitClient) abortSequencer() error {
	seq := c.sequencer

	// Collect conflict paths before clearing the sequencer.
	conflictPaths := make([]string, len(seq.conflicts))
	for i, cf := range seq.conflicts {
		conflictPaths[i] = cf.Path
	}

	// Restore symbolic HEAD if was on a branch.
	if !seq.detached {
		symRef := plumbing.NewSymbolicReference(plumbing.HEAD, seq.originalRef)
		if err := c.repo.Storer.SetReference(symRef); err != nil {
			return err
		}

		// Restore the branch ref to original hash.
		ref := plumbing.NewHashReference(seq.originalRef, seq.originalHead)
		if err := c.repo.Storer.SetReference(ref); err != nil {
			return err
		}
	} else {
		ref := plumbing.NewHashReference(plumbing.HEAD, seq.originalHead)
		if err := c.repo.Storer.SetReference(ref); err != nil {
			return err
		}
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	if err := wt.Reset(&gogit.ResetOptions{
		Commit: seq.originalHead,
		Mode:   gogit.HardReset,
	}); err != nil {
		return err
	}

	// go-git's HardReset can leave conflict-marked files in the worktree.
	// Explicitly restore each conflict path from the original tree.
	c.restoreConflictPaths(seq.originalHead, conflictPaths)

	c.sequencer = nil
	return nil
}

// restoreConflictPaths overwrites worktree files that had conflict markers
// with their content from the given commit, or deletes them if they don't
// exist in that commit's tree. Best-effort: errors are silently ignored
// since the HardReset already succeeded.
func (c *GitClient) restoreConflictPaths(commitHash plumbing.Hash, paths []string) {
	commit, err := c.repo.CommitObject(commitHash)
	if err != nil {
		return
	}
	tree, err := commit.Tree()
	if err != nil {
		return
	}
	for _, p := range paths {
		fullPath := filepath.Join(c.repoPath, p)
		f, err := tree.File(p)
		if err != nil {
			// File didn't exist in original — remove it.
			os.Remove(fullPath)
			continue
		}
		content, err := f.Contents()
		if err != nil {
			continue
		}
		os.WriteFile(fullPath, []byte(content), 0o644)
	}
}

// =============================================================================
// Internal: State Helpers
// =============================================================================

// saveRollbackState captures the current HEAD for rollback.
func (c *GitClient) saveRollbackState() (*sequencer, error) {
	headRef, err := c.repo.Head()
	if err != nil {
		return nil, wrapHeadError(err)
	}

	seq := &sequencer{
		state:        SeqRunning,
		originalHead: headRef.Hash(),
		originalRef:  headRef.Name(),
		detached:     !headRef.Name().IsBranch(),
	}

	// Initialize currentTip to HEAD.
	seq.currentTip = headRef.Hash()

	return seq, nil
}

// switchToTargetBranch checks out a branch if different from current.
func (c *GitClient) switchToTargetBranch(seq *sequencer, targetBranch string) error {
	currentBranch, _ := c.getBranchName()
	if currentBranch == targetBranch {
		return nil
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	targetRef := plumbing.NewBranchReferenceName(targetBranch)
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: targetRef}); err != nil {
		return fmt.Errorf("checkout %s: %w", targetBranch, err)
	}

	// Update rollback state to reflect the target branch.
	newHead, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}
	seq.currentTip = newHead.Hash()

	return nil
}

// detachHead sets HEAD to a specific commit in detached state.
func (c *GitClient) detachHead(hash plumbing.Hash) error {
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Checkout(&gogit.CheckoutOptions{
		Hash: hash,
	})
}

// updateHEADDirect updates the HEAD ref (or its target branch) to hash.
func (c *GitClient) updateHEADDirect(hash plumbing.Hash) error {
	headRef, err := c.repo.Head()
	if err != nil {
		return wrapHeadError(err)
	}

	ref := plumbing.NewHashReference(headRef.Name(), hash)
	return c.repo.Storer.SetReference(ref)
}

// buildStatus constructs a SequencerStatus from the current sequencer state.
func (c *GitClient) buildStatus() *SequencerStatus {
	seq := c.sequencer

	s := &SequencerStatus{
		Op:          seq.op,
		State:       seq.state,
		TotalSteps:  len(seq.steps),
		CurrentStep: seq.currentIdx,
		Conflicts:   seq.conflicts,
		DestName:    seq.destName,
	}

	if seq.currentIdx < len(seq.steps) {
		step := &seq.steps[seq.currentIdx]
		s.CurrentHash = step.commit.Hash.String()[:7]
		s.Subject = commitSubject(step.commit.Message)
	}

	// For merge, source name is the branch. For cherry-pick/rebase, it's the
	// short hash of the commit currently being applied.
	if seq.op == SeqMerge {
		s.SourceName = seq.sourceName
	} else if seq.currentIdx < len(seq.steps) {
		s.SourceName = seq.steps[seq.currentIdx].commit.Hash.String()[:7]
	}

	return s
}

// commitSubject extracts the first line of a commit message.
func commitSubject(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}

// =============================================================================
// Public: Conflict File Resolution
// =============================================================================

// ResolveConflictFile writes a chosen version of a conflicted file to the
// worktree. resolution: 1=ours, 2=theirs, 3=both (ours+theirs concatenated).
// Hash strings are hex-encoded blob hashes; ZeroHash hex means the side
// deleted the file.
func (c *GitClient) ResolveConflictFile(path string, resolution int, oursHash, theirsHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isRepo {
		return ErrNotGitRepo
	}

	fullPath := filepath.Join(c.repoPath, path)
	oursH := plumbing.NewHash(oursHash)
	theirsH := plumbing.NewHash(theirsHash)
	zero := plumbing.ZeroHash

	switch resolution {
	case 1: // Ours
		if oursH == zero {
			return os.Remove(fullPath)
		}
		content, err := readBlobContent(c.repo.Storer, oursH)
		if err != nil {
			return fmt.Errorf("read ours blob: %w", err)
		}
		return c.writeWorktreeFile(fullPath, content)

	case 2: // Theirs
		if theirsH == zero {
			return os.Remove(fullPath)
		}
		content, err := readBlobContent(c.repo.Storer, theirsH)
		if err != nil {
			return fmt.Errorf("read theirs blob: %w", err)
		}
		return c.writeWorktreeFile(fullPath, content)

	case 3: // Both (concatenate ours + theirs)
		var parts []string
		if oursH != zero {
			content, err := readBlobContent(c.repo.Storer, oursH)
			if err != nil {
				return fmt.Errorf("read ours blob: %w", err)
			}
			parts = append(parts, content)
		}
		if theirsH != zero {
			content, err := readBlobContent(c.repo.Storer, theirsH)
			if err != nil {
				return fmt.Errorf("read theirs blob: %w", err)
			}
			parts = append(parts, content)
		}
		return c.writeWorktreeFile(fullPath, strings.Join(parts, ""))

	default:
		return fmt.Errorf("unknown resolution %d", resolution)
	}
}

// ReadBlobContent reads blob content by its hex-encoded hash string.
// This is a read-only operation that does not modify repository state.
func (c *GitClient) ReadBlobContent(hashHex string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isRepo {
		return "", ErrNotGitRepo
	}

	return readBlobContent(c.repo.Storer, plumbing.NewHash(hashHex))
}

// writeWorktreeFile writes content to a file in the worktree, creating
// parent directories as needed.
func (c *GitClient) writeWorktreeFile(fullPath, content string) error {
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o644)
}

// WriteWorktreeFile writes content to a relative path in the worktree.
func (c *GitClient) WriteWorktreeFile(path, content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.isRepo {
		return ErrNotGitRepo
	}
	fullPath := filepath.Join(c.repoPath, path)
	return c.writeWorktreeFile(fullPath, content)
}

