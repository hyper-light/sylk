package conflictview

// StepPreview holds the commit diff data for step-preview display.
type StepPreview struct {
	Hash          string
	Subject       string
	FileDiffs     []FileDiffSummary
	ConflictPaths map[string]bool
}

// FileDiffSummary is a lightweight diff stat for step preview rendering.
type FileDiffSummary struct {
	Path      string
	Additions int
	Deletions int
}
