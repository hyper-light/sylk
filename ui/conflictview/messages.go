package conflictview

// ConflictResolveFileMsg is emitted when the user resolves a conflict file.
type ConflictResolveFileMsg struct {
	Path       string
	Resolution ConflictResolution
	OursHash   string
	TheirsHash string
}

// SequencerContinueMsg requests the app continue the sequencer after
// all conflicts have been resolved.
type SequencerContinueMsg struct{}

// SequencerBypassMsg requests the app bypass the current conflict
// (commit worktree as-is with conflict markers).
type SequencerBypassMsg struct{}

// SequencerAbortMsg requests the app abort the sequencer and roll back.
type SequencerAbortMsg struct{}

// ConflictWriteContentMsg requests writing resolved content to the worktree.
type ConflictWriteContentMsg struct {
	Path    string
	Content string
}

// SyntaxValidationRequestMsg triggers tree-sitter validation before continue.
type SyntaxValidationRequestMsg struct{}

// SyntaxValidationResultMsg carries the validation results back.
type SyntaxValidationResultMsg struct {
	Warnings []SyntaxWarning
	Proceed  bool // true if user confirmed "continue anyway"
}

// BaseContentRequestMsg asks the app to fetch base blob content for a file.
type BaseContentRequestMsg struct {
	Path     string
	BaseHash string
}

// BaseContentResponseMsg carries fetched base content back.
type BaseContentResponseMsg struct {
	Path    string
	Content string
	Err     error
}

// StepPreviewRequestMsg asks the app to compute the commit diff for a step.
type StepPreviewRequestMsg struct {
	Hash string
}

// StepPreviewResponseMsg carries the computed step preview back.
type StepPreviewResponseMsg struct {
	Preview *StepPreview
}

// SequencerUndoStepMsg requests undoing the last sequencer step.
type SequencerUndoStepMsg struct{}
