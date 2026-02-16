package diffview

import "github.com/adalundhe/sylk/core/search/git"

// CompareMode controls how selected commits are paired for diffing.
type CompareMode int

const (
	CompareModeChain    CompareMode = iota // 1→2, 2→3, 3→4 (default)
	CompareModeAllFirst                    // 1→2, 1→3, 1→4
	CompareModePairs                       // 1→2, 3→4 (odd last diffed vs parent)
	compareModeCount                       // sentinel for cycling
)

// DiffLineKind classifies an aligned line.
type DiffLineKind int

const (
	DiffLineContext  DiffLineKind = iota // Unchanged context line
	DiffLineAdded                        // Line exists only on new side
	DiffLineDeleted                      // Line exists only on old side
	DiffLineModified                     // Line changed on both sides
	DiffLineHunkSep                      // Blank separator between hunks
)

// CharAnnotation marks a byte range within a line as changed.
type CharAnnotation struct {
	Start int // Byte offset (inclusive)
	End   int // Byte offset (exclusive)
}

// AlignedLine pairs old and new sides for a single visual row.
type AlignedLine struct {
	OldLineNo int // 0 = absent on that side
	NewLineNo int // 0 = absent on that side

	OldText string
	NewText string

	Kind DiffLineKind

	OldAnnotations []CharAnnotation // Char-level diffs (byte offsets)
	NewAnnotations []CharAnnotation
}

// DiffPair holds the diff data between two commits.
type DiffPair struct {
	FromHash  string
	ToHash    string
	FromShort string
	ToShort   string

	Files    []git.FileDiff
	TotalAdd int
	TotalDel int
}

// FileBlock is a pre-computed block of aligned lines for one file.
type FileBlock struct {
	Path    string
	OldPath string
	Status  string // A/M/D/R/C
	Binary  bool

	Additions int
	Deletions int

	Lines []AlignedLine
}

// PairData holds pre-computed file blocks, highlights, and a path index
// for a single diff pair.
type PairData struct {
	Blocks     []FileBlock
	Highlights []FileHighlight
	PathIndex  map[string]int // file path → index in Blocks
}

// UnionFileEntry represents a file that appears in at least one diff pair,
// with aggregated stats across all pairs.
type UnionFileEntry struct {
	Path      string
	OldPath   string
	Status    string // highest-priority status across pairs
	Additions int    // sum of additions across all pairs
	Deletions int    // sum of deletions across all pairs
	PairCount int    // number of pairs containing this file
}

// statusPriority maps git diff status codes to a priority level.
// Higher values take precedence when merging across pairs.
var statusPriority = map[string]int{
	"":  0,
	"M": 1,
	"R": 2,
	"C": 3,
	"A": 4,
	"D": 5,
}

// higherPriorityStatus returns the status with the higher priority.
func higherPriorityStatus(a, b string) string {
	if statusPriority[a] >= statusPriority[b] {
		return a
	}
	return b
}

// ExitDiffViewMsg is emitted when the user closes the diff view.
type ExitDiffViewMsg struct{}

// ChangeCompareModeMsg is emitted when the user cycles compare mode (m key).
// App.go re-fetches diffs with the new pairing logic.
type ChangeCompareModeMsg struct {
	Mode CompareMode
}
