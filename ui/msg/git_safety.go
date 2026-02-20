package msg

import "github.com/adalundhe/sylk/core/search/git"

// ---------------------------------------------------------------------------
// Pre-commit pipeline
// ---------------------------------------------------------------------------

// PreCommitCheckMsg initiates the pre-commit scan pipeline.
type PreCommitCheckMsg struct {
	Paths   []string
	Message string
}

// LargeFilesDetectedMsg is returned when staged files exceed the size threshold.
type LargeFilesDetectedMsg struct {
	Files   []git.LargeFileEntry
	Paths   []string
	Message string
}

// SecretsDetectedMsg is returned when credential patterns are found in staged diffs.
type SecretsDetectedMsg struct {
	Findings []git.SecretFinding
	Paths    []string
	Message  string
}

// PreCommitCleanMsg indicates no issues were found in the pre-commit scan.
type PreCommitCleanMsg struct {
	Paths   []string
	Message string
}

// ---------------------------------------------------------------------------
// Conflict preview
// ---------------------------------------------------------------------------

// ConflictPreviewMsg carries the result of a dry-run merge simulation.
type ConflictPreviewMsg struct {
	Result *git.ConflictPreviewResult
	Op     string // "merge", "rebase", "cherry-pick"
	Params any    // operation-specific params for resuming after confirm
}

// ---------------------------------------------------------------------------
// Divergence
// ---------------------------------------------------------------------------

// DivergenceLoadedMsg carries computed divergence info for branches.
type DivergenceLoadedMsg struct {
	Info []git.DivergenceInfo
}

// ---------------------------------------------------------------------------
// Sequencer file state
// ---------------------------------------------------------------------------

// SequencerFileStateMsg carries the filesystem-based sequencer state.
type SequencerFileStateMsg struct {
	State *git.SequencerFileState
}

// ---------------------------------------------------------------------------
// Abort preservation
// ---------------------------------------------------------------------------

// AbortPreservedMsg is returned after pre-abort state preservation.
type AbortPreservedMsg struct {
	Preservation *git.AbortPreservation
	Err          error
}

// ---------------------------------------------------------------------------
// Integration detection
// ---------------------------------------------------------------------------

// IntegrationDetectedMsg carries cherry-pick duplicate detection results.
type IntegrationDetectedMsg struct {
	Results []git.IntegrationResult
	Params  any // original cherry-pick params for resuming
}

// ---------------------------------------------------------------------------
// Branch stash
// ---------------------------------------------------------------------------

// BranchStashAvailableMsg signals that the newly checked-out branch has
// a saved stash that can be restored.
type BranchStashAvailableMsg struct {
	Meta git.BranchStashMeta
}

// BranchStashRestoredMsg is returned after a branch stash restoration attempt.
type BranchStashRestoredMsg struct {
	Err error
}
