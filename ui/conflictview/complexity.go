package conflictview

import "sort"

// ComplexityLevel rates a conflict file's difficulty.
type ComplexityLevel int

const (
	ComplexitySimple   ComplexityLevel = iota
	ComplexityModerate
	ComplexityComplex
)

// ComplexityScore holds the computed metrics for a single file entry.
type ComplexityScore struct {
	Level      ComplexityLevel
	TotalLines int
	HunkCount  int
	HasDelete  bool
	HasBinary  bool
}

// scoreAllEntries computes a ComplexityScore per entry.
// Classification thresholds derive from the data (median/p75), never magic
// numbers.
func scoreAllEntries(entries []ConflictFileEntry) []ComplexityScore {
	n := len(entries)
	scores := make([]ComplexityScore, n)

	// Collect per-entry metrics.
	hunkCounts := make([]int, 0, n)
	lineCounts := make([]int, 0, n)
	for i := range entries {
		e := &entries[i]
		hc := len(e.Hunks)
		tl := totalHunkLines(e.Hunks)
		scores[i] = ComplexityScore{
			HunkCount:  hc,
			TotalLines: tl,
			HasDelete:  e.Type == TypeDeleteModify,
			HasBinary:  e.Type == TypeBinary,
		}
		hunkCounts = append(hunkCounts, hc)
		lineCounts = append(lineCounts, tl)
	}

	medHunks := medianInt(hunkCounts)
	medLines := medianInt(lineCounts)
	p75Lines := percentileInt(lineCounts, 75)

	for i := range scores {
		scores[i].Level = classify(scores[i], medHunks, medLines, p75Lines)
	}
	return scores
}

// sortByComplexity reorders entries (and their scores) simple-first.
func sortByComplexity(entries []ConflictFileEntry, scores []ComplexityScore) {
	indices := make([]int, len(entries))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(a, b int) bool {
		return scores[indices[a]].Level < scores[indices[b]].Level
	})

	reorderedEntries := make([]ConflictFileEntry, len(entries))
	reorderedScores := make([]ComplexityScore, len(scores))
	for dst, src := range indices {
		reorderedEntries[dst] = entries[src]
		reorderedScores[dst] = scores[src]
	}
	copy(entries, reorderedEntries)
	copy(scores, reorderedScores)
}

// classify derives the level from data-relative thresholds.
func classify(s ComplexityScore, medHunks, medLines, p75Lines int) ComplexityLevel {
	if s.HasDelete || s.HasBinary {
		return ComplexityComplex
	}
	if s.HunkCount > medHunks*2 || s.TotalLines > p75Lines {
		return ComplexityComplex
	}
	if s.HunkCount <= 1 && s.TotalLines <= medLines {
		return ComplexitySimple
	}
	return ComplexityModerate
}

// totalHunkLines sums ours+theirs line counts across all hunks.
func totalHunkLines(hunks []ConflictHunk) int {
	total := 0
	for _, h := range hunks {
		ours := h.DivLine - h.StartLine - 1
		theirs := h.EndLine - h.DivLine - 1
		total += max(ours, 0) + max(theirs, 0)
	}
	return total
}

// medianInt returns the median of a (possibly unsorted) int slice.
// Returns 0 for empty input.
func medianInt(vals []int) int {
	n := len(vals)
	if n == 0 {
		return 0
	}
	sorted := make([]int, n)
	copy(sorted, vals)
	sort.Ints(sorted)
	return sorted[n/2]
}

// percentileInt returns the p-th percentile (0–100) of a (possibly unsorted)
// int slice. Returns 0 for empty input.
func percentileInt(vals []int, p int) int {
	n := len(vals)
	if n == 0 {
		return 0
	}
	sorted := make([]int, n)
	copy(sorted, vals)
	sort.Ints(sorted)
	idx := (p * (n - 1)) / 100
	return sorted[idx]
}
