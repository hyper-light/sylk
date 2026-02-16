package mergediff

// ExitMergeDiffViewMsg is emitted when the user aborts the merge diff view.
type ExitMergeDiffViewMsg struct{}

// MergeBranchMsg is emitted when the user confirms a merge.
type MergeBranchMsg struct {
	DeleteSource bool // If true, delete the source branch after merging.
}
