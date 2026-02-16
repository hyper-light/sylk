package diffview

import "github.com/adalundhe/sylk/core/search/git"

// charDiffMaxRunes is the threshold above which char-level diffing is skipped
// for a modified line pair to avoid expensive computation on very long lines.
const charDiffMaxRunes = 500

// AlignHunks converts a slice of diff hunks into aligned lines suitable for
// side-by-side or unified rendering. Between hunks, a blank separator line
// is emitted so line number gaps make skipped context obvious.
func AlignHunks(hunks []git.DiffHunk) []AlignedLine {
	var result []AlignedLine
	for i, hunk := range hunks {
		if i > 0 {
			result = append(result, AlignedLine{Kind: DiffLineHunkSep})
		}
		result = append(result, alignHunk(hunk)...)
	}
	return result
}

// alignHunk processes a single hunk into aligned lines.
func alignHunk(h git.DiffHunk) []AlignedLine {
	var (
		result  []AlignedLine
		pending []string // buffered deletion lines (text only, no prefix)
		oldLine = h.OldStart
		newLine = h.NewStart
	)

	for _, raw := range h.Lines {
		if len(raw) == 0 {
			continue
		}
		prefix := raw[0]
		text := raw[1:]

		switch prefix {
		case ' ':
			result = flushDeletions(result, pending, &oldLine)
			pending = pending[:0]
			result = append(result, AlignedLine{
				OldLineNo: oldLine,
				NewLineNo: newLine,
				OldText:   text,
				NewText:   text,
				Kind:      DiffLineContext,
			})
			oldLine++
			newLine++

		case '-':
			pending = append(pending, text)

		case '+':
			if len(pending) > 0 {
				// Pair with a pending deletion → modified line.
				old := pending[0]
				pending = pending[1:]
				al := AlignedLine{
					OldLineNo: oldLine,
					NewLineNo: newLine,
					OldText:   old,
					NewText:   text,
					Kind:      DiffLineModified,
				}
				computeCharAnnotations(&al)
				result = append(result, al)
				oldLine++
				newLine++
			} else {
				result = append(result, AlignedLine{
					NewLineNo: newLine,
					NewText:   text,
					Kind:      DiffLineAdded,
				})
				newLine++
			}
		}
	}

	// Flush any remaining deletions.
	result = flushDeletions(result, pending, &oldLine)
	return result
}

// flushDeletions appends buffered deletion lines to result.
func flushDeletions(result []AlignedLine, pending []string, oldLine *int) []AlignedLine {
	for _, text := range pending {
		result = append(result, AlignedLine{
			OldLineNo: *oldLine,
			OldText:   text,
			Kind:      DiffLineDeleted,
		})
		(*oldLine)++
	}
	return result
}

// computeCharAnnotations finds the common prefix and suffix of old/new text
// and marks the differing middle section as a char-level annotation.
func computeCharAnnotations(al *AlignedLine) {
	oldRunes := []rune(al.OldText)
	newRunes := []rune(al.NewText)

	if len(oldRunes) > charDiffMaxRunes || len(newRunes) > charDiffMaxRunes {
		return
	}

	// Find common prefix length (in runes).
	prefixLen := 0
	minLen := min(len(oldRunes), len(newRunes))
	for prefixLen < minLen && oldRunes[prefixLen] == newRunes[prefixLen] {
		prefixLen++
	}

	// Find common suffix length (in runes), not overlapping with prefix.
	suffixLen := 0
	for suffixLen < minLen-prefixLen &&
		oldRunes[len(oldRunes)-1-suffixLen] == newRunes[len(newRunes)-1-suffixLen] {
		suffixLen++
	}

	oldDiffStart := prefixLen
	oldDiffEnd := len(oldRunes) - suffixLen
	newDiffStart := prefixLen
	newDiffEnd := len(newRunes) - suffixLen

	// Skip if no actual difference or if the entire line differs.
	if oldDiffStart >= oldDiffEnd && newDiffStart >= newDiffEnd {
		return
	}

	// Convert rune offsets to byte offsets.
	if oldDiffStart < oldDiffEnd {
		al.OldAnnotations = []CharAnnotation{{
			Start: runeOffsetToByteOffset(al.OldText, oldDiffStart),
			End:   runeOffsetToByteOffset(al.OldText, oldDiffEnd),
		}}
	}
	if newDiffStart < newDiffEnd {
		al.NewAnnotations = []CharAnnotation{{
			Start: runeOffsetToByteOffset(al.NewText, newDiffStart),
			End:   runeOffsetToByteOffset(al.NewText, newDiffEnd),
		}}
	}
}

// runeOffsetToByteOffset converts a rune index to the corresponding byte
// offset in a UTF-8 string.
func runeOffsetToByteOffset(s string, runeIdx int) int {
	byteOff := 0
	for i := 0; i < runeIdx && byteOff < len(s); i++ {
		_, size := firstRune(s[byteOff:])
		byteOff += size
	}
	return byteOff
}

// firstRune decodes the first rune from s, returning its value and byte width.
func firstRune(s string) (rune, int) {
	if len(s) == 0 {
		return 0, 0
	}
	r := rune(s[0])
	if r < 0x80 {
		return r, 1
	}
	// Multi-byte: use standard library via range.
	for _, r := range s {
		return r, len(string(r))
	}
	return 0xFFFD, 1
}

// BuildFileBlocks converts a slice of FileDiffs into pre-computed FileBlocks
// with aligned lines ready for rendering.
func BuildFileBlocks(files []git.FileDiff) []FileBlock {
	blocks := make([]FileBlock, len(files))
	for i, f := range files {
		blocks[i] = FileBlock{
			Path:      f.Path,
			OldPath:   f.OldPath,
			Status:    f.Status,
			Binary:    f.Binary,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Lines:     AlignHunks(f.Hunks),
		}
	}
	return blocks
}
