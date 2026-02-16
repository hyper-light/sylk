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
func buildFileHighlights(blocks []FileBlock, hl *codepkg.Highlighter) []FileHighlight {
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

// highlightFileBlock reconstructs old and new side content from aligned lines,
// runs the highlighter on each side, and returns per-line-number regions.
func highlightFileBlock(fb FileBlock, language string, hl *codepkg.Highlighter) FileHighlight {
	fh := FileHighlight{
		OldRegions: make(map[int][]codepkg.HighlightRegion),
		NewRegions: make(map[int][]codepkg.HighlightRegion),
	}

	// Collect old-side and new-side lines in order with their line numbers.
	var oldLines, newLines []numberedLine
	for _, al := range fb.Lines {
		if al.Kind == DiffLineHunkSep {
			continue
		}
		if al.OldLineNo > 0 {
			oldLines = append(oldLines, numberedLine{al.OldLineNo, al.OldText})
		}
		if al.NewLineNo > 0 {
			newLines = append(newLines, numberedLine{al.NewLineNo, al.NewText})
		}
	}

	highlightSide(oldLines, language, hl, fh.OldRegions)
	highlightSide(newLines, language, hl, fh.NewRegions)
	return fh
}

// highlightSide reconstructs content from numbered lines, runs the highlighter,
// and stores per-line regions in the output map keyed by original line number.
func highlightSide(lines []numberedLine, language string, hl *codepkg.Highlighter, out map[int][]codepkg.HighlightRegion) {
	if len(lines) == 0 {
		return
	}
	texts := make([]string, len(lines))
	for i, nl := range lines {
		texts[i] = nl.text
	}
	content := strings.Join(texts, "\n")
	allRegions := hl.HighlightContent(content, language)

	for i, nl := range lines {
		if i < len(allRegions) && len(allRegions[i]) > 0 {
			out[nl.lineNo] = allRegions[i]
		}
	}
}

// numberedLine is a helper type used only within highlightFileBlock.
type numberedLine = struct {
	lineNo int
	text   string
}

// ---------------------------------------------------------------------------
// Language detection (extension → grammar name)
// ---------------------------------------------------------------------------

// langFromPath returns the tree-sitter grammar name for a file path.
// Uses a direct extension map to avoid importing core/treesitter.
func langFromPath(filePath string) string {
	dot := strings.LastIndex(filePath, ".")
	if dot < 0 || dot == len(filePath)-1 {
		return ""
	}
	ext := filePath[dot+1:]
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
		start := clampIdx(r.StartCol, lineLen)
		end := clampIdx(r.EndCol, lineLen)
		if start >= end {
			continue
		}
		if cursor < start {
			segments = append(segments, diffSegment{cursor, start, defaultSt, false})
		}
		style := defaultSt
		if s, ok := styles[r.Category]; ok {
			style = s
		}
		segments = append(segments, diffSegment{start, end, style, false})
		cursor = end
	}
	if cursor < lineLen {
		segments = append(segments, diffSegment{cursor, lineLen, defaultSt, false})
	}
	return segments
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

// splitSegAtAnnotations splits a single segment at all annotation boundaries.
func splitSegAtAnnotations(result []diffSegment, seg diffSegment, annotations []CharAnnotation) []diffSegment {
	// For each annotation, split the segment. Since char annotations in a
	// diff line are non-overlapping (exactly one range from prefix/suffix
	// diffing), a single pass suffices.
	for _, a := range annotations {
		if seg.end <= a.Start || seg.start >= a.End {
			continue
		}
		// Segment overlaps with annotation.
		if seg.start < a.Start {
			result = append(result, diffSegment{seg.start, a.Start, seg.style, seg.inAnnotation})
			seg.start = a.Start
		}
		overlapEnd := min(seg.end, a.End)
		result = append(result, diffSegment{seg.start, overlapEnd, seg.style, true})
		seg.start = overlapEnd
		if seg.start >= seg.end {
			return result
		}
	}
	result = append(result, seg)
	return result
}

// renderDiffSegments renders segments with syntax foreground and diff background.
// inAnnotation segments use charBg; others use lineBg. Truncates and pads to
// exact width.
func renderDiffSegments(runes []rune, segments []diffSegment, lineBg, charBg lipgloss.Color, width int) string {
	var b strings.Builder
	b.Grow(len(runes) * 4)

	for _, seg := range segments {
		if seg.start >= seg.end || seg.start >= len(runes) {
			continue
		}
		end := min(seg.end, len(runes))
		bg := lineBg
		if seg.inAnnotation {
			bg = charBg
		}
		style := seg.style.Background(bg)
		b.WriteString(style.Render(string(runes[seg.start:end])))
	}

	result := b.String()

	// Truncate to exact width (tab expansion can overshoot).
	result = truncateStyledLine(result, width)

	// Pad remaining width with line background.
	vis := lipgloss.Width(result)
	if vis < width {
		padSt := lipgloss.NewStyle().Background(lineBg)
		result += padSt.Render(strings.Repeat(" ", width-vis))
	}
	return result
}

// renderPlainSegments renders segments with syntax foreground and no background.
// Used for context (unchanged) lines. Truncates and pads to exact width.
func renderPlainSegments(runes []rune, segments []diffSegment, width int) string {
	var b strings.Builder
	b.Grow(len(runes) * 3)

	for _, seg := range segments {
		if seg.start >= seg.end || seg.start >= len(runes) {
			continue
		}
		end := min(seg.end, len(runes))
		b.WriteString(seg.style.Render(string(runes[seg.start:end])))
	}

	result := b.String()

	// Truncate to exact width (tab expansion can overshoot).
	result = truncateStyledLine(result, width)

	// Pad remaining width.
	vis := lipgloss.Width(result)
	if vis < width {
		result += strings.Repeat(" ", width-vis)
	}
	return result
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

// remapByteRegions converts byte-offset highlight regions to display-column
// regions using the column map from tab expansion.
func remapByteRegions(
	regions []codepkg.HighlightRegion,
	rawText string,
	colMap []int,
) []codepkg.HighlightRegion {
	if len(regions) == 0 {
		return nil
	}
	out := make([]codepkg.HighlightRegion, 0, len(regions))
	for _, r := range regions {
		// Byte offset → rune index → display column.
		startRune := byteOffsetToRuneOffset(rawText, r.StartCol)
		endRune := byteOffsetToRuneOffset(rawText, r.EndCol)
		startCol := safeColMap(colMap, startRune)
		endCol := safeColMap(colMap, endRune)
		if startCol < endCol {
			out = append(out, codepkg.HighlightRegion{
				StartCol: startCol,
				EndCol:   endCol,
				Category: r.Category,
			})
		}
	}
	return out
}

// remapByteAnnotations converts byte-offset char annotations to display-column
// ranges using the column map from tab expansion.
func remapByteAnnotations(
	annotations []CharAnnotation,
	rawText string,
	colMap []int,
) []CharAnnotation {
	if len(annotations) == 0 {
		return nil
	}
	out := make([]CharAnnotation, 0, len(annotations))
	for _, a := range annotations {
		startRune := byteOffsetToRuneOffset(rawText, a.Start)
		endRune := byteOffsetToRuneOffset(rawText, a.End)
		startCol := safeColMap(colMap, startRune)
		endCol := safeColMap(colMap, endRune)
		if startCol < endCol {
			out = append(out, CharAnnotation{Start: startCol, End: endCol})
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

// splitRunesByWidth splits runes into chunks that each fit within maxWidth
// display columns. Returns [start, end) rune indices for each chunk.
// Always returns at least one chunk.
func splitRunesByWidth(runes []rune, maxWidth int) [][2]int {
	if len(runes) == 0 || maxWidth <= 0 {
		return [][2]int{{0, 0}}
	}
	var chunks [][2]int
	start := 0
	w := 0
	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxWidth && i > start {
			chunks = append(chunks, [2]int{start, i})
			start = i
			w = 0
		}
		w += rw
	}
	chunks = append(chunks, [2]int{start, len(runes)})
	return chunks
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

// truncateStyledLine clips a styled string (with ANSI escape codes) to fit
// within the given visual width. Accounts for wide characters (CJK, emoji)
// using go-runewidth. ANSI CSI sequences are preserved and a reset is appended.
func truncateStyledLine(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	var buf strings.Builder
	visWidth := 0
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && !isCSIEnd(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
			}
			buf.WriteString(s[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runewidth.RuneWidth(r)
		if visWidth+rw > w {
			break
		}
		buf.WriteString(s[i : i+size])
		visWidth += rw
		i += size
	}
	buf.WriteString("\x1b[0m")
	return buf.String()
}

// isCSIEnd reports whether b is a CSI sequence final byte (0x40–0x7E).
func isCSIEnd(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}
