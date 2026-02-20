package git

import (
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// Ref prefix constants for safety system refs.
const (
	snapRefPrefix     = "refs/sylk/snap/"
	deletedRefPrefix  = "refs/sylk/deleted/"
	detachedRefPrefix = "refs/sylk/detached/"
	abortRefPrefix    = "refs/sylk/abort/"
	stashRefPrefix    = "refs/sylk/stash/"
)

// journalDirName is the subdirectory under .git/ for the safety journal.
const journalDirName = "sylk/journal"

// SafetyConfig controls the behavior of the safety system.
type SafetyConfig struct {
	// SnapshotRetention is how long snapshot refs are kept before GC.
	SnapshotRetention time.Duration

	// MaxSnapshots caps the total number of snapshot refs.
	MaxSnapshots int

	// AutoStashEnabled controls whether dirty worktrees are auto-stashed
	// before destructive operations.
	AutoStashEnabled bool
}

// DefaultSafetyConfig returns a SafetyConfig with sensible defaults.
func DefaultSafetyConfig() SafetyConfig {
	return SafetyConfig{
		SnapshotRetention: 30 * 24 * time.Hour, // 30 days
		MaxSnapshots:      256,
		AutoStashEnabled:  true,
	}
}

// SnapshotRef describes a pre-operation snapshot stored as a git ref.
type SnapshotRef struct {
	// Name is the full ref name (e.g. "refs/sylk/snap/rebase-1740067200").
	Name string

	// Hash is the commit hash the ref points to.
	Hash plumbing.Hash

	// Op is the operation that triggered the snapshot.
	Op GitOp

	// Timestamp is when the snapshot was created.
	Timestamp time.Time

	// BranchRef is the branch ref name at snapshot time (empty if detached).
	BranchRef string
}

// JournalPhase distinguishes begin, complete, and abort entries.
type JournalPhase uint8

const (
	JournalBegin    JournalPhase = 1
	JournalComplete JournalPhase = 2
	JournalAbort    JournalPhase = 3
)

// JournalEntry records a single operation phase in the safety journal.
type JournalEntry struct {
	// Seq is the monotonically increasing sequence number.
	Seq uint64

	// Op identifies the git operation.
	Op GitOp

	// Phase indicates begin, complete, or abort.
	Phase JournalPhase

	// Timestamp is when this entry was written.
	Timestamp time.Time

	// HeadHash is the HEAD commit hash at entry time.
	HeadHash plumbing.Hash

	// BranchRef is the current branch ref name (empty if detached).
	BranchRef string

	// Params is an opaque string encoding operation parameters.
	Params string

	// SnapRef is the snapshot ref name created for this operation (begin only).
	SnapRef string

	// Err records the error message on abort (empty otherwise).
	Err string
}

// IncompleteOp pairs a begin entry with its snapshot for crash recovery.
type IncompleteOp struct {
	Entry    JournalEntry
	Snapshot *SnapshotRef // nil if snapshot ref was already GC'd
}

// RecoveryAction describes how to resolve an incomplete operation.
type RecoveryAction int8

const (
	RecoveryRollback RecoveryAction = iota // Restore to pre-operation snapshot.
	RecoveryResume                         // Accept current state and mark complete.
	RecoveryIgnore                         // Discard the journal entry without action.
)

// sequencerStartOps are operations that initiate a multi-step sequence.
// When one fires, the safety guard takes a "sequence snapshot" and reuses
// it for all subsequent steps, avoiding per-step snapshot overhead.
var sequencerStartOps = map[GitOp]struct{}{
	OpCherryPickSequence: {},
	OpRebaseInteractive:  {},
	OpMergeSequence:      {},
}

// sequencerStepOps advance an active sequence. The guard skips snapshot
// creation for these and reuses the sequence-start snapshot.
var sequencerStepOps = map[GitOp]struct{}{
	OpSequencerContinue: {},
	OpSequencerBypass:   {},
}
