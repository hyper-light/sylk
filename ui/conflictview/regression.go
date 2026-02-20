package conflictview

import "strings"

// RegressionKind identifies a type of suspicious resolution.
type RegressionKind int

const (
	RegressionDiscardedChanges RegressionKind = iota
	RegressionDroppedAll
	RegressionDuplicateCode
)

// RegressionWarning describes a suspicious resolution for a single hunk.
type RegressionWarning struct {
	HunkIdx int
	Kind    RegressionKind
	Detail  string
}

// significantLineThreshold is the minimum number of non-whitespace changed
// lines on the discarded side to trigger a DiscardedChanges warning.
// Derived from: a single-line diff is rarely significant, but 4+ lines of
// real code silently dropped warrants a warning.
const significantLineThreshold = 4

// duplicateBlockSize is the minimum length of a repeated block (in lines)
// to trigger a DuplicateCode warning.
const duplicateBlockSize = 3

// regressionChecker examines a hunk's sides for a specific regression pattern.
type regressionChecker func(ours, theirs []string, r ConflictResolution) *RegressionWarning

// regressionCheckers lists all regression detection functions.
var regressionCheckers = [...]regressionChecker{
	checkDroppedAll,
	checkDiscardedChanges,
	checkDuplicateCode,
}

// checkResolution examines a resolved hunk's result for suspicious patterns.
func checkResolution(lines []string, hunk ConflictHunk, resolution ConflictResolution) []RegressionWarning {
	ours := extractLines(lines, hunk.StartLine+1, hunk.DivLine)
	theirs := extractLines(lines, hunk.DivLine+1, hunk.EndLine)

	var warnings []RegressionWarning
	for _, check := range regressionCheckers {
		if w := check(ours, theirs, resolution); w != nil {
			warnings = append(warnings, *w)
		}
	}
	return warnings
}

// keptAndDropped returns (kept, dropped) sides based on the resolution.
func keptAndDropped(ours, theirs []string, r ConflictResolution) ([]string, []string) {
	if r == ResTheirs {
		return theirs, ours
	}
	return ours, theirs
}

// checkDroppedAll detects resolutions that discard all content from one side
// while the kept side is empty.
func checkDroppedAll(ours, theirs []string, r ConflictResolution) *RegressionWarning {
	if r == ResBoth {
		return nil
	}
	kept, dropped := keptAndDropped(ours, theirs, r)
	if !isAllBlank(kept) || countNonBlank(dropped) == 0 {
		return nil
	}
	return &RegressionWarning{
		Kind:   RegressionDroppedAll,
		Detail: "resolved to empty while the other side had content",
	}
}

// checkDiscardedChanges detects resolutions that silently discard significant
// changes from the other side.
func checkDiscardedChanges(ours, theirs []string, r ConflictResolution) *RegressionWarning {
	if r != ResOurs && r != ResTheirs {
		return nil
	}
	_, dropped := keptAndDropped(ours, theirs, r)
	if countNonBlank(dropped) < significantLineThreshold {
		return nil
	}
	return &RegressionWarning{
		Kind:   RegressionDiscardedChanges,
		Detail: "discarded side had significant changes",
	}
}

// checkDuplicateCode detects ResBoth resolutions that produce repeated
// blocks of identical lines.
func checkDuplicateCode(ours, theirs []string, r ConflictResolution) *RegressionWarning {
	if r != ResBoth {
		return nil
	}
	combined := make([]string, 0, len(ours)+len(theirs))
	combined = append(combined, ours...)
	combined = append(combined, theirs...)
	if !hasDuplicateBlock(combined, duplicateBlockSize) {
		return nil
	}
	return &RegressionWarning{
		Kind:   RegressionDuplicateCode,
		Detail: "merged result contains duplicate code blocks",
	}
}

// isAllBlank returns true if all lines are blank or whitespace-only.
func isAllBlank(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}

// countNonBlank returns the number of non-whitespace-only lines.
func countNonBlank(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// hasDuplicateBlock detects if lines contains a repeated contiguous block
// of at least blockSize identical lines using a rolling hash window.
func hasDuplicateBlock(lines []string, blockSize int) bool {
	n := len(lines)
	if n < blockSize*2 {
		return false
	}

	// Build a set of block fingerprints (join of trimmed lines).
	seen := make(map[string]struct{}, n-blockSize+1)
	for i := 0; i <= n-blockSize; i++ {
		key := blockKey(lines, i, blockSize)
		if _, dup := seen[key]; dup {
			return true
		}
		seen[key] = struct{}{}
	}
	return false
}

// blockKey returns a fingerprint string for lines[start:start+size].
func blockKey(lines []string, start, size int) string {
	var b strings.Builder
	for i := start; i < start+size; i++ {
		if i > start {
			b.WriteByte('\x00')
		}
		b.WriteString(strings.TrimSpace(lines[i]))
	}
	return b.String()
}
