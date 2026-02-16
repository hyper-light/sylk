package diffview

import (
	"unicode/utf8"

	"github.com/adalundhe/sylk/core/search/git"
)

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

// hunkAligner carries mutable state for processing a single hunk's lines.
type hunkAligner struct {
	result  []AlignedLine
	pending []string // buffered deletion lines (text only, no prefix)
	oldLine int
	newLine int
}

// hunkLineHandler is the signature for per-prefix line handlers.
type hunkLineHandler func(ha *hunkAligner, text string)

// hunkDispatch maps diff-line prefixes to their handlers, replacing a switch
// so that each handler can live in its own low-complexity function.
var hunkDispatch = map[byte]hunkLineHandler{
	' ': (*hunkAligner).handleContext,
	'-': (*hunkAligner).handleDeletion,
	'+': (*hunkAligner).handleAddition,
}

// alignHunk processes a single hunk into aligned lines.
func alignHunk(h git.DiffHunk) []AlignedLine {
	ha := hunkAligner{oldLine: h.OldStart, newLine: h.NewStart}
	for _, raw := range h.Lines {
		ha.processLine(raw)
	}
	ha.result = flushDeletions(ha.result, ha.pending, &ha.oldLine)
	return ha.result
}

// processLine dispatches a single raw diff line to the appropriate handler.
func (ha *hunkAligner) processLine(raw string) {
	if len(raw) == 0 {
		return
	}
	if fn := hunkDispatch[raw[0]]; fn != nil {
		fn(ha, raw[1:])
	}
}

// handleContext processes an unchanged context line (prefix ' ').
func (ha *hunkAligner) handleContext(text string) {
	ha.result = flushDeletions(ha.result, ha.pending, &ha.oldLine)
	ha.pending = ha.pending[:0]
	ha.result = append(ha.result, AlignedLine{
		OldLineNo: ha.oldLine,
		NewLineNo: ha.newLine,
		OldText:   text,
		NewText:   text,
		Kind:      DiffLineContext,
	})
	ha.oldLine++
	ha.newLine++
}

// handleDeletion buffers a deleted line (prefix '-') for later pairing.
func (ha *hunkAligner) handleDeletion(text string) {
	ha.pending = append(ha.pending, text)
}

// handleAddition processes an added line (prefix '+'), pairing with a
// buffered deletion when available to produce a modified line.
func (ha *hunkAligner) handleAddition(text string) {
	if len(ha.pending) > 0 {
		ha.emitModified(text)
		return
	}
	ha.result = append(ha.result, AlignedLine{
		NewLineNo: ha.newLine,
		NewText:   text,
		Kind:      DiffLineAdded,
	})
	ha.newLine++
}

// emitModified pairs a pending deletion with an addition to emit a modified line.
func (ha *hunkAligner) emitModified(text string) {
	old := ha.pending[0]
	ha.pending = ha.pending[1:]
	al := AlignedLine{
		OldLineNo: ha.oldLine,
		NewLineNo: ha.newLine,
		OldText:   old,
		NewText:   text,
		Kind:      DiffLineModified,
	}
	computeCharAnnotations(&al)
	ha.result = append(ha.result, al)
	ha.oldLine++
	ha.newLine++
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
	if max(len(oldRunes), len(newRunes)) > charDiffMaxRunes {
		return
	}
	prefixLen := commonPrefixLen(oldRunes, newRunes)
	suffixLen := commonSuffixLen(oldRunes, newRunes, prefixLen)
	al.OldAnnotations = buildAnnotation(al.OldText, prefixLen, len(oldRunes)-suffixLen)
	al.NewAnnotations = buildAnnotation(al.NewText, prefixLen, len(newRunes)-suffixLen)
}

// commonPrefixLen returns the number of leading runes shared by a and b.
func commonPrefixLen(a, b []rune) int {
	n := 0
	for n < min(len(a), len(b)) && a[n] == b[n] {
		n++
	}
	return n
}

// commonSuffixLen returns the number of trailing runes shared by a and b,
// not overlapping with the first prefixLen runes.
func commonSuffixLen(a, b []rune, prefixLen int) int {
	n := 0
	for n < min(len(a), len(b))-prefixLen && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// buildAnnotation returns a single CharAnnotation marking [diffStart, diffEnd)
// in rune space converted to byte offsets, or nil if the range is empty.
func buildAnnotation(text string, diffStart, diffEnd int) []CharAnnotation {
	if diffStart >= diffEnd {
		return nil
	}
	return []CharAnnotation{{
		Start: runeOffsetToByteOffset(text, diffStart),
		End:   runeOffsetToByteOffset(text, diffEnd),
	}}
}

// runeOffsetToByteOffset converts a rune index to the corresponding byte
// offset in a UTF-8 string.
func runeOffsetToByteOffset(s string, runeIdx int) int {
	byteOff := 0
	for i := 0; i < runeIdx && byteOff < len(s); i++ {
		_, size := utf8.DecodeRuneInString(s[byteOff:])
		byteOff += size
	}
	return byteOff
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
