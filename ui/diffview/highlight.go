package diffview

import (
	"strings"
	"unicode/utf8"

	codepkg "github.com/adalundhe/sylk/ui/code"
	"github.com/adalundhe/sylk/ui/theme"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// diffTabWidth is the tab stop width for diff content display.
// Derived from: standard 4-space tab stops used in most editors/viewers.
const diffTabWidth = 4

// ---------------------------------------------------------------------------
// File-level highlight data
// ---------------------------------------------------------------------------

// FileHighlight holds per-line-number syntax highlight regions for a file's
// old and new sides. Keyed by 1-based line number (matching AlignedLine's
// OldLineNo / NewLineNo fields).
type FileHighlight struct {
	OldRegions map[int][]codepkg.HighlightRegion
	NewRegions map[int][]codepkg.HighlightRegion
}

// buildFileHighlights runs syntax highlighting on each file block's old and
// new side content. Returns one FileHighlight per block.
func BuildFileHighlights(blocks []FileBlock, hl *codepkg.Highlighter) []FileHighlight {
	highlights := make([]FileHighlight, len(blocks))
	for i, fb := range blocks {
		if fb.Binary {
			highlights[i] = FileHighlight{}
			continue
		}
		lang := langFromPath(fb.Path)
		highlights[i] = highlightFileBlock(fb, lang, hl)
	}
	return highlights
}

// collectSideLines extracts old-side or new-side numbered lines from aligned
// lines. The pickLine function selects the appropriate line number and text
// from each AlignedLine; lines with lineNo <= 0 (including hunk separators)
// are skipped.
func collectSideLines(lines []AlignedLine, pickLine func(AlignedLine) (int, string)) []numberedLine {
	var out []numberedLine
	for _, al := range lines {
		lineNo, text := pickLine(al)
		if lineNo > 0 {
			out = append(out, numberedLine{lineNo, text})
		}
	}
	return out
}

// highlightFileBlock reconstructs old and new side content from aligned lines,
// runs the highlighter on each side, and returns per-line-number regions.
func highlightFileBlock(fb FileBlock, language string, hl *codepkg.Highlighter) FileHighlight {
	fh := FileHighlight{
		OldRegions: make(map[int][]codepkg.HighlightRegion),
		NewRegions: make(map[int][]codepkg.HighlightRegion),
	}

	oldLines := collectSideLines(fb.Lines, func(al AlignedLine) (int, string) {
		return al.OldLineNo, al.OldText
	})
	newLines := collectSideLines(fb.Lines, func(al AlignedLine) (int, string) {
		return al.NewLineNo, al.NewText
	})

	highlightSide(oldLines, language, hl, fh.OldRegions)
	highlightSide(newLines, language, hl, fh.NewRegions)
	return fh
}

// extractTexts returns the text field from each numbered line.
func extractTexts(lines []numberedLine) []string {
	texts := make([]string, len(lines))
	for i, nl := range lines {
		texts[i] = nl.text
	}
	return texts
}

// storeRegions maps highlight regions back to their original line numbers.
func storeRegions(lines []numberedLine, allRegions [][]codepkg.HighlightRegion, out map[int][]codepkg.HighlightRegion) {
	for i, nl := range lines {
		if i < len(allRegions) {
			out[nl.lineNo] = allRegions[i]
		}
	}
}

// highlightSide reconstructs content from numbered lines, runs the highlighter,
// and stores per-line regions in the output map keyed by original line number.
func highlightSide(lines []numberedLine, language string, hl *codepkg.Highlighter, out map[int][]codepkg.HighlightRegion) {
	if len(lines) == 0 {
		return
	}
	content := strings.Join(extractTexts(lines), "\n")
	allRegions := hl.HighlightContent(content, language)
	storeRegions(lines, allRegions, out)
}

// numberedLine is a helper type used only within highlightFileBlock.
type numberedLine = struct {
	lineNo int
	text   string
}

// ---------------------------------------------------------------------------
// Language detection (extension → grammar name)
// ---------------------------------------------------------------------------

// extractExtension returns the file extension (without the dot) from a path,
// or empty string if none exists.
func extractExtension(filePath string) string {
	dot := strings.LastIndex(filePath, ".")
	if dot < 0 {
		return ""
	}
	return filePath[dot+1:]
}

// langFromPath returns the tree-sitter grammar name for a file path.
// Uses a direct extension map to avoid importing core/treesitter.
func langFromPath(filePath string) string {
	ext := extractExtension(filePath)
	lang, ok := extToLang[ext]
	if !ok {
		return ""
	}
	return lang
}

var extToLang = map[string]string{
	"go": "go", "rs": "rust", "py": "python",
	"js": "javascript", "jsx": "javascript",
	"ts": "typescript", "tsx": "tsx",
	"java": "java", "c": "c", "h": "c",
	"cpp": "cpp", "cc": "cpp", "cxx": "cpp", "hpp": "cpp",
	"cs": "c_sharp", "rb": "ruby", "lua": "lua",
	"sh": "bash", "bash": "bash", "zsh": "bash",
	"yaml": "yaml", "yml": "yaml", "toml": "toml",
	"json": "json", "md": "markdown",
	"html": "html", "css": "css", "scss": "scss",
	"sql": "sql", "swift": "swift", "kt": "kotlin",
	"zig": "zig", "vim": "vim", "el": "elisp",
	"ex": "elixir", "exs": "elixir", "erl": "erlang",
	"hs": "haskell", "ml": "ocaml", "r": "r", "R": "r",
	"scala": "scala", "tf": "hcl", "proto": "proto",
	"php": "php", "pl": "perl", "pm": "perl",
}

// ---------------------------------------------------------------------------
// Segment-based line rendering
// ---------------------------------------------------------------------------

// diffSegment represents a contiguous byte range of a display line with
// a syntax style and a flag indicating whether it overlaps a char-level
// diff annotation. The segment-based approach mirrors code.selSegment
// from ui/code/highlight.go.
type diffSegment struct {
	start, end   int            // display-column range (rune indices)
	style        lipgloss.Style // syntax foreground (+ bold/italic)
	inAnnotation bool           // true → use charBg instead of lineBg
}

// renderHighlightedLine renders a diff line (added/deleted/modified) with
// syntax highlighting foreground, diff background tinting, and char-level
// annotation highlighting. Handles tab expansion and visual-width padding.
func renderHighlightedLine(
	rawText string,
	syntaxRegions []codepkg.HighlightRegion,
	charAnnotations []CharAnnotation,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	lineBg, charBg lipgloss.Color,
	width int,
) string {
	// Phase 1: expand tabs → display text + column map.
	expanded, colMap := expandDiffTabs(rawText)

	// Phase 2: remap byte-offset syntax regions → display-column regions.
	displayRegions := remapByteRegions(syntaxRegions, rawText, colMap)

	// Phase 3: remap byte-offset char annotations → display-column ranges.
	displayAnnotations := remapByteAnnotations(charAnnotations, rawText, colMap)

	// Phase 4: truncate to visible display width (handles wide characters).
	runes := []rune(expanded)
	lineLen := displayWidthRuneLimit(runes, width)

	// Phase 5: build base segments from syntax regions (foreground styles).
	segments := buildBaseSegments(lineLen, displayRegions, syntaxStyles, defaultSt)

	// Phase 6: split segments at annotation boundaries.
	segments = markAnnotations(segments, displayAnnotations)

	// Phase 7: render segments with appropriate backgrounds.
	return renderDiffSegments(runes[:lineLen], segments, lineBg, charBg, width)
}

// renderContextHighlightedLine renders an unchanged context line with syntax
// highlighting but no diff background tinting.
func renderContextHighlightedLine(
	rawText string,
	syntaxRegions []codepkg.HighlightRegion,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	width int,
) string {
	expanded, colMap := expandDiffTabs(rawText)
	displayRegions := remapByteRegions(syntaxRegions, rawText, colMap)
	runes := []rune(expanded)
	lineLen := displayWidthRuneLimit(runes, width)
	segments := buildBaseSegments(lineLen, displayRegions, syntaxStyles, defaultSt)
	return renderPlainSegments(runes[:lineLen], segments, width)
}

// displayWidthRuneLimit returns the number of leading runes that fit within
// the given display width, correctly accounting for wide characters.
func displayWidthRuneLimit(runes []rune, maxWidth int) int {
	w := 0
	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxWidth {
			return i
		}
		w += rw
	}
	return len(runes)
}

// lookupStyle returns the syntax style for a category, falling back to defaultSt.
func lookupStyle(styles map[theme.SyntaxCategory]lipgloss.Style, cat theme.SyntaxCategory, defaultSt lipgloss.Style) lipgloss.Style {
	if s, ok := styles[cat]; ok {
		return s
	}
	return defaultSt
}

// appendRegionSegment appends a gap segment (if needed) and the region segment
// for a single highlight region. Returns the updated cursor position and
// segments slice.
func appendRegionSegment(
	segments []diffSegment,
	cursor, start, end int,
	style, defaultSt lipgloss.Style,
) ([]diffSegment, int) {
	if cursor < start {
		segments = append(segments, diffSegment{cursor, start, defaultSt, false})
	}
	segments = append(segments, diffSegment{start, end, style, false})
	return segments, end
}

// clampRegion clamps a highlight region to [0, lineLen] and returns the
// clamped start, end, and whether the region has positive width.
func clampRegion(r codepkg.HighlightRegion, lineLen int) (int, int, bool) {
	start := clampIdx(r.StartCol, lineLen)
	end := clampIdx(r.EndCol, lineLen)
	return start, end, start < end
}

// appendTrailingGap adds a gap segment from cursor to lineLen if needed.
func appendTrailingGap(segments []diffSegment, cursor, lineLen int, defaultSt lipgloss.Style) []diffSegment {
	if cursor < lineLen {
		segments = append(segments, diffSegment{cursor, lineLen, defaultSt, false})
	}
	return segments
}

// buildBaseSegments creates rendering segments from highlight regions.
// Gaps between regions use defaultSt. Mirrors code.Highlighter.buildSegments.
func buildBaseSegments(
	lineLen int,
	regions []codepkg.HighlightRegion,
	styles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
) []diffSegment {
	segments := make([]diffSegment, 0, len(regions)*2+1)
	cursor := 0
	for _, r := range regions {
		start, end, ok := clampRegion(r, lineLen)
		if !ok {
			continue
		}
		style := lookupStyle(styles, r.Category, defaultSt)
		segments, cursor = appendRegionSegment(segments, cursor, start, end, style, defaultSt)
	}
	return appendTrailingGap(segments, cursor, lineLen, defaultSt)
}

// markAnnotations splits segments at char-annotation boundaries, setting
// inAnnotation=true for the overlapping portions. Mirrors code.markSelection.
func markAnnotations(segments []diffSegment, annotations []CharAnnotation) []diffSegment {
	if len(annotations) == 0 {
		return segments
	}
	result := make([]diffSegment, 0, len(segments)+len(annotations)*2)
	for _, seg := range segments {
		result = splitSegAtAnnotations(result, seg, annotations)
	}
	return result
}

// annotationOverlaps reports whether a segment and annotation overlap.
func annotationOverlaps(seg diffSegment, a CharAnnotation) bool {
	return seg.end > a.Start && seg.start < a.End
}

// splitSegAtOverlap splits a segment at a single overlapping annotation,
// appending the pre-overlap gap and the overlap portion. Returns the updated
// result slice and the remaining segment tail. The consumed flag indicates
// whether the segment was fully consumed.
func splitSegAtOverlap(result []diffSegment, seg diffSegment, a CharAnnotation) ([]diffSegment, diffSegment, bool) {
	if seg.start < a.Start {
		result = append(result, diffSegment{seg.start, a.Start, seg.style, seg.inAnnotation})
		seg.start = a.Start
	}
	overlapEnd := min(seg.end, a.End)
	result = append(result, diffSegment{seg.start, overlapEnd, seg.style, true})
	seg.start = overlapEnd
	return result, seg, seg.start >= seg.end
}

// applySingleAnnotation processes one annotation against the current segment.
// Returns the updated result, remaining segment, and whether the segment was
// fully consumed.
func applySingleAnnotation(result []diffSegment, seg diffSegment, a CharAnnotation) ([]diffSegment, diffSegment, bool) {
	if !annotationOverlaps(seg, a) {
		return result, seg, false
	}
	return splitSegAtOverlap(result, seg, a)
}

// splitSegAtAnnotations splits a single segment at all annotation boundaries.
func splitSegAtAnnotations(result []diffSegment, seg diffSegment, annotations []CharAnnotation) []diffSegment {
	for _, a := range annotations {
		var consumed bool
		result, seg, consumed = applySingleAnnotation(result, seg, a)
		if consumed {
			return result
		}
	}
	return append(result, seg)
}

// segmentBg returns the background color for a diff segment: charBg for
// annotated portions, lineBg otherwise.
func segmentBg(seg diffSegment, lineBg, charBg lipgloss.Color) lipgloss.Color {
	if seg.inAnnotation {
		return charBg
	}
	return lineBg
}

// renderDiffSegment writes a single segment with the given background to the builder.
func renderDiffSegment(b *strings.Builder, runes []rune, seg diffSegment, bg lipgloss.Color) {
	end := min(seg.end, len(runes))
	style := seg.style.Background(bg)
	b.WriteString(style.Render(string(runes[seg.start:end])))
}

// isEmptySegment reports whether a segment contributes no visible runes.
func isEmptySegment(seg diffSegment, runeCount int) bool {
	return seg.start >= seg.end || seg.start >= runeCount
}

// joinSegmentsDiff writes all diff segments to a builder with per-segment backgrounds.
func joinSegmentsDiff(runes []rune, segments []diffSegment, lineBg, charBg lipgloss.Color) string {
	var b strings.Builder
	b.Grow(len(runes) * 4)
	for _, seg := range segments {
		if isEmptySegment(seg, len(runes)) {
			continue
		}
		renderDiffSegment(&b, runes, seg, segmentBg(seg, lineBg, charBg))
	}
	return b.String()
}

// padStyledToWidth appends space padding to reach the target width using the given style.
func padStyledToWidth(s string, width int, padStyle lipgloss.Style) string {
	vis := lipgloss.Width(s)
	if vis >= width {
		return s
	}
	return s + padStyle.Render(strings.Repeat(" ", width-vis))
}

// renderDiffSegments renders segments with syntax foreground and diff background.
// inAnnotation segments use charBg; others use lineBg. Truncates and pads to
// exact width.
func renderDiffSegments(runes []rune, segments []diffSegment, lineBg, charBg lipgloss.Color, width int) string {
	result := joinSegmentsDiff(runes, segments, lineBg, charBg)
	result = truncateStyledLine(result, width)
	return padStyledToWidth(result, width, lipgloss.NewStyle().Background(lineBg))
}

// joinSegmentsPlain writes all segments to a builder with syntax foreground only.
func joinSegmentsPlain(runes []rune, segments []diffSegment) string {
	var b strings.Builder
	b.Grow(len(runes) * 3)
	for _, seg := range segments {
		if isEmptySegment(seg, len(runes)) {
			continue
		}
		end := min(seg.end, len(runes))
		b.WriteString(seg.style.Render(string(runes[seg.start:end])))
	}
	return b.String()
}

// renderPlainSegments renders segments with syntax foreground and no background.
// Used for context (unchanged) lines. Truncates and pads to exact width.
func renderPlainSegments(runes []rune, segments []diffSegment, width int) string {
	result := joinSegmentsPlain(runes, segments)
	result = truncateStyledLine(result, width)
	return padStyledToWidth(result, width, lipgloss.NewStyle())
}

// ---------------------------------------------------------------------------
// Tab expansion & offset remapping
// ---------------------------------------------------------------------------

// expandDiffTabs expands tab characters to spaces and returns the expanded
// string plus a column map from raw rune index to display column.
// Mirrors expandTabs from ui/editor/model.go.
func expandDiffTabs(line string) (string, []int) {
	runes := []rune(line)
	colMap := make([]int, len(runes)+1)
	displayCol := 0
	var buf strings.Builder
	buf.Grow(len(line) + len(line)/4)
	for i, r := range runes {
		colMap[i] = displayCol
		if r == '\t' {
			spaces := diffTabWidth - (displayCol % diffTabWidth)
			buf.WriteString(strings.Repeat(" ", spaces))
			displayCol += spaces
		} else {
			buf.WriteRune(r)
			displayCol++
		}
	}
	colMap[len(runes)] = displayCol
	return buf.String(), colMap
}

// remapOneRegion converts a single byte-offset highlight region to display
// columns. Returns the remapped region and whether it has positive width.
func remapOneRegion(r codepkg.HighlightRegion, rawText string, colMap []int) (codepkg.HighlightRegion, bool) {
	startCol := safeColMap(colMap, byteOffsetToRuneOffset(rawText, r.StartCol))
	endCol := safeColMap(colMap, byteOffsetToRuneOffset(rawText, r.EndCol))
	return codepkg.HighlightRegion{StartCol: startCol, EndCol: endCol, Category: r.Category}, startCol < endCol
}

// remapByteRegions converts byte-offset highlight regions to display-column
// regions using the column map from tab expansion.
func remapByteRegions(
	regions []codepkg.HighlightRegion,
	rawText string,
	colMap []int,
) []codepkg.HighlightRegion {
	out := make([]codepkg.HighlightRegion, 0, len(regions))
	for _, r := range regions {
		if mapped, ok := remapOneRegion(r, rawText, colMap); ok {
			out = append(out, mapped)
		}
	}
	return out
}

// remapOneAnnotation converts a single byte-offset char annotation to display
// columns. Returns the remapped annotation and whether it has positive width.
func remapOneAnnotation(a CharAnnotation, rawText string, colMap []int) (CharAnnotation, bool) {
	startCol := safeColMap(colMap, byteOffsetToRuneOffset(rawText, a.Start))
	endCol := safeColMap(colMap, byteOffsetToRuneOffset(rawText, a.End))
	return CharAnnotation{Start: startCol, End: endCol}, startCol < endCol
}

// remapByteAnnotations converts byte-offset char annotations to display-column
// ranges using the column map from tab expansion.
func remapByteAnnotations(
	annotations []CharAnnotation,
	rawText string,
	colMap []int,
) []CharAnnotation {
	out := make([]CharAnnotation, 0, len(annotations))
	for _, a := range annotations {
		if mapped, ok := remapOneAnnotation(a, rawText, colMap); ok {
			out = append(out, mapped)
		}
	}
	return out
}

// safeColMap looks up a rune index in the column map, clamping to bounds.
// Mirrors safeMapCol from ui/editor/model.go.
func safeColMap(colMap []int, runeIdx int) int {
	if runeIdx < 0 {
		return 0
	}
	if runeIdx >= len(colMap) {
		return colMap[len(colMap)-1]
	}
	return colMap[runeIdx]
}

// clampIdx constrains an index to [0, max].
func clampIdx(idx, max int) int {
	if idx < 0 {
		return 0
	}
	if idx > max {
		return max
	}
	return idx
}

// ---------------------------------------------------------------------------
// Line wrapping
// ---------------------------------------------------------------------------

// accumulateChunks walks runes accumulating display widths and emitting
// chunk boundaries whenever the accumulated width would exceed maxWidth.
func accumulateChunks(runes []rune, maxWidth int) [][2]int {
	var chunks [][2]int
	start, w := 0, 0
	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxWidth {
			chunks = append(chunks, [2]int{start, i})
			start, w = i, 0
		}
		w += rw
	}
	return append(chunks, [2]int{start, len(runes)})
}

// splitRunesByWidth splits runes into chunks that each fit within maxWidth
// display columns. Returns [start, end) rune indices for each chunk.
// Always returns at least one chunk.
func splitRunesByWidth(runes []rune, maxWidth int) [][2]int {
	if len(runes) == 0 || maxWidth <= 0 {
		return [][2]int{{0, 0}}
	}
	return accumulateChunks(runes, maxWidth)
}

// clipRegions clips highlight regions to a rune range [start, end) and shifts
// to 0-based indices for the chunk.
func clipRegions(regions []codepkg.HighlightRegion, start, end int) []codepkg.HighlightRegion {
	var out []codepkg.HighlightRegion
	for _, r := range regions {
		s := max(r.StartCol, start)
		e := min(r.EndCol, end)
		if s < e {
			out = append(out, codepkg.HighlightRegion{
				StartCol: s - start,
				EndCol:   e - start,
				Category: r.Category,
			})
		}
	}
	return out
}

// clipAnnotations clips char annotations to a rune range [start, end) and
// shifts to 0-based indices for the chunk.
func clipAnnotations(annotations []CharAnnotation, start, end int) []CharAnnotation {
	var out []CharAnnotation
	for _, a := range annotations {
		s := max(a.Start, start)
		e := min(a.End, end)
		if s < e {
			out = append(out, CharAnnotation{Start: s - start, End: e - start})
		}
	}
	return out
}

// wrapDiffContent wraps a diff line (added/deleted/modified) into multiple
// visual rows, each fitting within the given display width. Preserves syntax
// highlighting foreground, diff background tinting, and char-level annotations.
func wrapDiffContent(
	rawText string,
	syntaxRegions []codepkg.HighlightRegion,
	charAnnotations []CharAnnotation,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	lineBg, charBg lipgloss.Color,
	width int,
) []string {
	expanded, colMap := expandDiffTabs(rawText)
	displayRegions := remapByteRegions(syntaxRegions, rawText, colMap)
	displayAnnotations := remapByteAnnotations(charAnnotations, rawText, colMap)
	runes := []rune(expanded)

	chunks := splitRunesByWidth(runes, width)
	rows := make([]string, len(chunks))
	for i, chunk := range chunks {
		chunkRunes := runes[chunk[0]:chunk[1]]
		chunkLen := len(chunkRunes)
		cr := clipRegions(displayRegions, chunk[0], chunk[1])
		ca := clipAnnotations(displayAnnotations, chunk[0], chunk[1])
		segments := buildBaseSegments(chunkLen, cr, syntaxStyles, defaultSt)
		segments = markAnnotations(segments, ca)
		rows[i] = renderDiffSegments(chunkRunes, segments, lineBg, charBg, width)
	}
	return rows
}

// wrapContextContent wraps a context (unchanged) line into multiple visual
// rows, each fitting within the given display width. Preserves syntax
// highlighting foreground with no diff background.
func wrapContextContent(
	rawText string,
	syntaxRegions []codepkg.HighlightRegion,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	width int,
) []string {
	expanded, colMap := expandDiffTabs(rawText)
	displayRegions := remapByteRegions(syntaxRegions, rawText, colMap)
	runes := []rune(expanded)

	chunks := splitRunesByWidth(runes, width)
	rows := make([]string, len(chunks))
	for i, chunk := range chunks {
		chunkRunes := runes[chunk[0]:chunk[1]]
		chunkLen := len(chunkRunes)
		cr := clipRegions(displayRegions, chunk[0], chunk[1])
		segments := buildBaseSegments(chunkLen, cr, syntaxStyles, defaultSt)
		rows[i] = renderPlainSegments(chunkRunes, segments, width)
	}
	return rows
}

// ---------------------------------------------------------------------------
// ANSI-aware truncation
// ---------------------------------------------------------------------------

// isCSIStart reports whether position j in s begins a CSI bracket sequence.
func isCSIStart(s string, j int) bool {
	return j < len(s) && s[j] == '['
}

// scanToCSIEnd advances j past CSI parameter bytes until a final byte or end
// of string is reached. Returns the index of the final byte (or len(s)).
func scanToCSIEnd(s string, j int) int {
	for j < len(s) && !isCSIEnd(s[j]) {
		j++
	}
	return j
}

// advancePastFinalByte returns j+1 if j is within bounds (skipping the CSI
// final byte), or j if at end of string.
func advancePastFinalByte(s string, j int) int {
	if j < len(s) {
		return j + 1
	}
	return j
}

// skipCSISequence scans past a CSI escape sequence starting at position i
// in the string s. Assumes s[i] == '\x1b'. Returns the index immediately
// after the sequence's final byte.
func skipCSISequence(s string, i int) int {
	j := i + 1
	if !isCSIStart(s, j) {
		return j
	}
	return advancePastFinalByte(s, scanToCSIEnd(s, j+1))
}

// truncateVisibleRunes scans non-escape runes in s starting at byte index i,
// appending them to buf until visWidth would exceed w. Returns the final byte
// index and accumulated visible width.
func truncateVisibleRunes(buf *strings.Builder, s string, i, visWidth, w int) (int, int) {
	r, size := utf8.DecodeRuneInString(s[i:])
	rw := runewidth.RuneWidth(r)
	if visWidth+rw > w {
		return len(s), visWidth // signal stop by returning past end
	}
	buf.WriteString(s[i : i+size])
	return i + size, visWidth + rw
}

// needsTruncation reports whether the styled string exceeds the target width.
func needsTruncation(s string, w int) bool {
	return w > 0 && lipgloss.Width(s) > w
}

// truncateStyledCore walks the string scanning escape sequences and visible
// runes, stopping when the visible width reaches w.
func truncateStyledCore(s string, w int) string {
	var buf strings.Builder
	visWidth := 0
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			j := skipCSISequence(s, i)
			buf.WriteString(s[i:j])
			i = j
			continue
		}
		i, visWidth = truncateVisibleRunes(&buf, s, i, visWidth, w)
	}
	buf.WriteString("\x1b[0m")
	return buf.String()
}

// truncateStyledLine clips a styled string (with ANSI escape codes) to fit
// within the given visual width. Accounts for wide characters (CJK, emoji)
// using go-runewidth. ANSI CSI sequences are preserved and a reset is appended.
func truncateStyledLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if !needsTruncation(s, w) {
		return s
	}
	return truncateStyledCore(s, w)
}

// isCSIEnd reports whether b is a CSI sequence final byte (0x40–0x7E).
func isCSIEnd(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}
