package diffview

import (
	"fmt"
	"math"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Pair pane header
// ---------------------------------------------------------------------------

// renderPairPaneHeader renders a pane header showing the pair's hash range
// and per-file stats for the selected file within this pair. When focused,
// the hash text uses Primary+bold and the rule uses BorderActive.
func RenderPairPaneHeader(pair DiffPair, pd PairData, selectedPath string, width int, p theme.Palette, focused bool) string {
	hashSt := lipgloss.NewStyle().Foreground(p.Muted)
	ruleSt := lipgloss.NewStyle().Foreground(p.Border)
	if focused {
		hashSt = lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
		ruleSt = lipgloss.NewStyle().Foreground(p.BorderActive)
	}

	hashText := hashSt.Render(" " + pair.FromShort + " \u2192 " + pair.ToShort + " ")
	hashW := lipgloss.Width(hashText)

	statsStr := renderPairStats(pd, selectedPath, p) + " "
	statsW := lipgloss.Width(statsStr)

	ruleLen := max(width-hashW-statsW, 0)
	rule := ruleSt.Render(strings.Repeat("\u2500", ruleLen))

	return ClampLine(hashText+rule+statsStr, width)
}

// renderPairStats builds the "+N -M" stats string for the selected file
// within a pair, or "no changes" if the file is absent from the pair.
func renderPairStats(pd PairData, selectedPath string, p theme.Palette) string {
	idx, ok := pd.PathIndex[selectedPath]
	if !ok {
		noChangeSt := lipgloss.NewStyle().Foreground(p.Muted).Italic(true)
		return noChangeSt.Render("no changes")
	}
	return FormatAddDel(pd.Blocks[idx].Additions, pd.Blocks[idx].Deletions, p)
}

// formatAddDel formats additions and deletions as styled "+N -M" text.
// Returns an empty string when both counts are zero.
func FormatAddDel(additions, deletions int, p theme.Palette) string {
	addSt := lipgloss.NewStyle().Foreground(p.Success)
	delSt := lipgloss.NewStyle().Foreground(p.Error)
	var parts []string
	if additions > 0 {
		parts = append(parts, addSt.Render(fmt.Sprintf("+%d", additions)))
	}
	if deletions > 0 {
		parts = append(parts, delSt.Render(fmt.Sprintf("-%d", deletions)))
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// Gutter helpers
// ---------------------------------------------------------------------------

// gutterWidth returns the column width needed for line numbers.
func gutterWidth(maxLine int) int {
	if maxLine <= 0 {
		return 1
	}
	return int(math.Log10(float64(maxLine))) + 1
}

// renderGutter formats a line number in a fixed-width gutter.
// Returns spaces if lineNo is 0 (absent side).
func renderGutter(lineNo, width int, style lipgloss.Style) string {
	if lineNo == 0 {
		return strings.Repeat(" ", width)
	}
	s := fmt.Sprintf("%*d", width, lineNo)
	return style.Render(s)
}

// gutterForRow returns the old and new gutter strings for a given wrapped
// row index. Row 0 uses the real gutters; continuation rows use emptyGutter.
func gutterForRow(r int, oldGutter, newGutter, emptyGutter string) (string, string) {
	if r == 0 {
		return oldGutter, newGutter
	}
	return emptyGutter, emptyGutter
}

// rowOrFill returns the content at idx from rows, or fill when idx is
// out of range.
func rowOrFill(rows []string, idx int, fill string) string {
	if idx < len(rows) {
		return rows[idx]
	}
	return fill
}

// ---------------------------------------------------------------------------
// Side-by-side rendering
// ---------------------------------------------------------------------------

// renderSideBySide renders aligned lines in side-by-side mode with syntax
// highlighting foreground, diff background tinting, char-level annotation
// highlighting, and per-side wrapping. Long lines wrap independently within
// each half. Returns rendered lines and hunk separator output positions.
func RenderSideBySide(
	lines []AlignedLine,
	fh FileHighlight,
	width int,
	maxOldLine, maxNewLine int,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
) ([]string, []int) {
	gw := max(gutterWidth(maxOldLine), gutterWidth(maxNewLine))
	divider := lipgloss.NewStyle().Foreground(p.Border).Render("\u2502")

	// Each side: gutter + space + content.
	// Layout: [old_gutter] [old_content] | [new_gutter] [new_content]
	sideWidth := (width - 1) / 2 // -1 for center divider
	contentWidth := max(sideWidth-gw-1, 1)

	gutterSt := lipgloss.NewStyle().Foreground(p.Muted)
	emptyGutter := strings.Repeat(" ", gw)
	emptyFill := strings.Repeat(" ", contentWidth)

	var result []string
	var hunkStarts []int

	for _, al := range lines {
		if al.Kind == DiffLineHunkSep {
			hunkStarts = append(hunkStarts, len(result))
			line := strings.Repeat(" ", sideWidth) + divider + strings.Repeat(" ", sideWidth)
			result = append(result, ClampLine(line, width))
			continue
		}
		oldGutter := renderGutter(al.OldLineNo, gw, gutterSt)
		newGutter := renderGutter(al.NewLineNo, gw, gutterSt)
		oldRows := sbsOldSide(al, fh, syntaxStyles, defaultSt, p, contentWidth)
		newRows := sbsNewSide(al, fh, syntaxStyles, defaultSt, p, contentWidth)
		result = append(result, assembleSBSRows(oldRows, newRows, oldGutter, newGutter, emptyGutter, emptyFill, sideWidth, divider, width)...)
	}
	return result, hunkStarts
}

// sbsOldSide wraps the old (left) side content for a single aligned line.
// Returns nil for Added lines which have no old-side content.
func sbsOldSide(
	al AlignedLine,
	fh FileHighlight,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
	contentWidth int,
) []string {
	if al.Kind == DiffLineContext {
		return wrapContextContent(
			al.OldText, fh.OldRegions[al.OldLineNo],
			syntaxStyles, defaultSt, contentWidth,
		)
	}
	if al.Kind == DiffLineAdded {
		return nil
	}
	// Deleted or Modified — old side uses deletion colors.
	return wrapDiffContent(
		al.OldText, fh.OldRegions[al.OldLineNo], al.OldAnnotations,
		syntaxStyles, defaultSt, p.DiffDelBg, p.DiffDelChar, contentWidth,
	)
}

// sbsNewSide wraps the new (right) side content for a single aligned line.
// Returns nil for Deleted lines which have no new-side content.
func sbsNewSide(
	al AlignedLine,
	fh FileHighlight,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
	contentWidth int,
) []string {
	if al.Kind == DiffLineContext {
		return wrapContextContent(
			al.NewText, fh.NewRegions[al.NewLineNo],
			syntaxStyles, defaultSt, contentWidth,
		)
	}
	if al.Kind == DiffLineDeleted {
		return nil
	}
	// Added or Modified — new side uses addition colors.
	return wrapDiffContent(
		al.NewText, fh.NewRegions[al.NewLineNo], al.NewAnnotations,
		syntaxStyles, defaultSt, p.DiffAddBg, p.DiffAddChar, contentWidth,
	)
}

// assembleSBSRows combines wrapped old and new rows into full-width
// side-by-side lines, padding shorter sides with empty fill.
func assembleSBSRows(
	oldRows, newRows []string,
	oldGutter, newGutter, emptyGutter, emptyFill string,
	sideWidth int,
	divider string,
	width int,
) []string {
	rowCount := max(len(oldRows), len(newRows))
	result := make([]string, rowCount)
	for r := range rowCount {
		og, ng := gutterForRow(r, oldGutter, newGutter, emptyGutter)
		oldContent := rowOrFill(oldRows, r, emptyFill)
		newContent := rowOrFill(newRows, r, emptyFill)
		oldSide := ClampLine(og+" "+oldContent, sideWidth)
		newSide := ClampLine(ng+" "+newContent, sideWidth)
		result[r] = ClampLine(oldSide+divider+newSide, width)
	}
	return result
}

// ---------------------------------------------------------------------------
// Unified rendering
// ---------------------------------------------------------------------------

// renderUnified renders aligned lines in unified diff mode with syntax
// highlighting foreground, diff background tinting, char-level annotation
// highlighting, and line wrapping. Returns rendered lines and hunk separator
// output positions.
func RenderUnified(
	lines []AlignedLine,
	fh FileHighlight,
	width int,
	maxOldLine, maxNewLine int,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
) ([]string, []int) {
	gw := max(gutterWidth(maxOldLine), gutterWidth(maxNewLine))
	contentWidth := max(width-gw*2-2, 1) // two gutters + two spaces

	gutterSt := lipgloss.NewStyle().Foreground(p.Muted)
	emptyGutter := strings.Repeat(" ", gw)

	var result []string
	var hunkStarts []int

	for _, al := range lines {
		if al.Kind == DiffLineHunkSep {
			hunkStarts = append(hunkStarts, len(result))
		}
		result = append(result, renderUnifiedAlignedLine(al, fh, gw, gutterSt, syntaxStyles, defaultSt, emptyGutter, p, contentWidth, width)...)
	}
	return result, hunkStarts
}

// renderUnifiedAlignedLine renders a single aligned line (including hunk
// separators) into one or more display rows for unified mode.
func renderUnifiedAlignedLine(
	al AlignedLine,
	fh FileHighlight,
	gw int,
	gutterSt lipgloss.Style,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	emptyGutter string,
	p theme.Palette,
	contentWidth, width int,
) []string {
	if al.Kind == DiffLineHunkSep {
		return []string{strings.Repeat(" ", width)}
	}
	old := unifiedOldLines(al, fh, gw, gutterSt, syntaxStyles, defaultSt, emptyGutter, p, contentWidth, width)
	new := unifiedNewLines(al, fh, gw, gutterSt, syntaxStyles, defaultSt, emptyGutter, p, contentWidth, width)
	return append(old, new...)
}

// unifiedOldLines renders the old-side portion of an aligned line for
// unified mode. Returns nil for Added lines which have no old-side content.
// Context lines emit both gutters on the old-side row.
func unifiedOldLines(
	al AlignedLine,
	fh FileHighlight,
	gw int,
	gutterSt lipgloss.Style,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	emptyGutter string,
	p theme.Palette,
	contentWidth, width int,
) []string {
	if al.Kind == DiffLineContext {
		og := renderGutter(al.OldLineNo, gw, gutterSt)
		ng := renderGutter(al.NewLineNo, gw, gutterSt)
		rows := wrapContextContent(
			al.OldText, fh.OldRegions[al.OldLineNo],
			syntaxStyles, defaultSt, contentWidth,
		)
		return formatUnifiedRows(rows, og, ng, emptyGutter, width)
	}
	if al.Kind == DiffLineAdded {
		return nil
	}
	// Deleted or Modified — old side uses deletion colors.
	og := renderGutter(al.OldLineNo, gw, gutterSt)
	rows := wrapDiffContent(
		al.OldText, fh.OldRegions[al.OldLineNo], al.OldAnnotations,
		syntaxStyles, defaultSt, p.DiffDelBg, p.DiffDelChar, contentWidth,
	)
	return formatUnifiedRows(rows, og, emptyGutter, emptyGutter, width)
}

// unifiedNewLines renders the new-side portion of an aligned line for
// unified mode. Returns nil for Context and Deleted lines.
func unifiedNewLines(
	al AlignedLine,
	fh FileHighlight,
	gw int,
	gutterSt lipgloss.Style,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	emptyGutter string,
	p theme.Palette,
	contentWidth, width int,
) []string {
	if al.Kind != DiffLineAdded && al.Kind != DiffLineModified {
		return nil
	}
	ng := renderGutter(al.NewLineNo, gw, gutterSt)
	rows := wrapDiffContent(
		al.NewText, fh.NewRegions[al.NewLineNo], al.NewAnnotations,
		syntaxStyles, defaultSt, p.DiffAddBg, p.DiffAddChar, contentWidth,
	)
	return formatUnifiedRows(rows, emptyGutter, ng, emptyGutter, width)
}

// formatUnifiedRows formats wrapped content rows with gutter prefixes.
// The first row uses g1 and g2 as gutters; continuation rows use contGutter.
func formatUnifiedRows(rows []string, g1, g2, contGutter string, width int) []string {
	out := make([]string, len(rows))
	for i, content := range rows {
		left, right := g1, g2
		if i > 0 {
			left, right = contGutter, contGutter
		}
		out[i] = ClampLine(left+" "+right+" "+content, width)
	}
	return out
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// ClampLine truncates a styled line to width and pads with spaces if short.
func ClampLine(s string, width int) string {
	s = truncateStyledLine(s, width)
	if vis := lipgloss.Width(s); vis < width {
		s += strings.Repeat(" ", width-vis)
	}
	return s
}

// byteOffsetToRuneOffset converts a byte offset to a rune index.
func byteOffsetToRuneOffset(s string, byteOff int) int {
	runeIdx := 0
	for i := range s {
		if i >= byteOff {
			return runeIdx
		}
		runeIdx++
	}
	return runeIdx
}
