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
