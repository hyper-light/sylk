package conflictview

// ConflictResolution identifies how a conflict file should be resolved.
type ConflictResolution int

const (
	ResUnresolved ConflictResolution = iota
	ResOurs
	ResTheirs
	ResBoth
	resCount
)

// ConflictType mirrors git.ConflictType values.
type ConflictType int

const (
	TypeContent      ConflictType = iota // Both sides modified.
	TypeDeleteModify                     // One side deleted, other modified.
	TypeBothAdded                        // Both sides added new file.
	TypeBinary                           // Binary file conflict.
)

// ConflictHunk describes a single conflict region within merged content.
type ConflictHunk struct {
	StartLine    int             // 0-indexed line of <<<<<<<
	DivLine      int             // 0-indexed line of =======
	EndLine      int             // 0-indexed line of >>>>>>>
	OursLabel    string          // Label after <<<<<<< marker.
	TheirsLabel  string          // Label after >>>>>>> marker.
	Snippet      string          // First non-blank ours line (trimmed), for file list preview.
	AutoResolved bool            // True if auto-resolution detected.
	AutoKind     AutoResolveKind // Classification of auto-resolution.
}

// ConflictFileEntry describes a single file in the conflict resolution view.
type ConflictFileEntry struct {
	Path          string
	OursHash      string
	TheirsHash    string
	BaseHash      string
	Type          ConflictType
	MergedContent string             // Full file with conflict markers (content conflicts).
	Resolution    ConflictResolution
	Hunks         []ConflictHunk     // Parsed conflict regions within MergedContent.
	Complexity    ComplexityScore    // Computed by scoreAllEntries after hunk parsing.
}

// ConflictData holds all state needed to display the conflict view.
type ConflictData struct {
	Op         int    // SequencerOp ordinal.
	Total      int    // Total steps in the sequencer.
	Current    int    // Current step index (0-based).
	Hash       string // Short hash of conflicting commit.
	Subject    string // Subject line of conflicting commit.
	SourceName string // Display name for the "theirs" side (branch or short sha).
	DestName   string // Display name for the "ours" side (branch name).
	Entries    []ConflictFileEntry
}
