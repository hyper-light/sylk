package diffview

import (
	"fmt"
	"math"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// renderDiffHeader renders the pinned header: hash range + total stats + divider.
func renderDiffHeader(pair DiffPair, width int, p theme.Palette) string {
	hashSt := lipgloss.NewStyle().Foreground(p.Muted)
	addSt := lipgloss.NewStyle().Foreground(p.Success)
	delSt := lipgloss.NewStyle().Foreground(p.Error)
	divSt := lipgloss.NewStyle().Foreground(p.Border)

	hashText := hashSt.Render(pair.FromShort + " → " + pair.ToShort)
	stats := addSt.Render(fmt.Sprintf("+%d", pair.TotalAdd)) + " " +
		delSt.Render(fmt.Sprintf("-%d", pair.TotalDel))

	hashW := lipgloss.Width(hashText)
	statsW := lipgloss.Width(stats)
	gap := max(width-hashW-statsW-2, 0) // 1 space padding each side

	row := " " + hashText + strings.Repeat(" ", gap) + stats + " "
	row = clampLine(row, width)

	divider := divSt.Render(strings.Repeat("─", width))
	return row + "\n" + divider
}

// renderFileBlockHeader renders a file's header: name + horizontal rule + stats.
func renderFileBlockHeader(fb FileBlock, width int, p theme.Palette) string {
	nameSt := lipgloss.NewStyle().Bold(true)
	ruleSt := lipgloss.NewStyle().Foreground(p.Border)
	addSt := lipgloss.NewStyle().Foreground(p.Success)
	delSt := lipgloss.NewStyle().Foreground(p.Error)

	// Color filename by status.
	switch fb.Status {
	case "A":
		nameSt = nameSt.Foreground(p.Success)
	case "M":
		nameSt = nameSt.Foreground(p.Warning)
	case "D":
		nameSt = nameSt.Foreground(p.Error)
	case "R", "C":
		nameSt = nameSt.Foreground(p.Teal)
	default:
		nameSt = nameSt.Foreground(p.Foreground)
	}

	displayName := fb.Path
	if fb.OldPath != "" && fb.OldPath != fb.Path {
		displayName = fb.OldPath + " → " + fb.Path
	}

	name := nameSt.Render(" " + displayName + " ")
	nameW := lipgloss.Width(name)

	var statsStr string
	if fb.Additions > 0 {
		statsStr += addSt.Render(fmt.Sprintf("+%d", fb.Additions))
	}
	if fb.Deletions > 0 {
		if statsStr != "" {
			statsStr += " "
		}
		statsStr += delSt.Render(fmt.Sprintf("-%d", fb.Deletions))
	}
	statsStr += " "
	statsW := lipgloss.Width(statsStr)

	ruleLen := max(width-nameW-statsW, 0)
	rule := ruleSt.Render(strings.Repeat("─", ruleLen))

	return clampLine(name+rule+statsStr, width)
}

// renderFocusedFileBlockHeader renders a file header with a focus accent:
// BorderActive colored rule and Primary foreground for the filename.
func renderFocusedFileBlockHeader(fb FileBlock, width int, p theme.Palette) string {
	nameSt := lipgloss.NewStyle().Bold(true).Foreground(p.Primary)
	ruleSt := lipgloss.NewStyle().Foreground(p.BorderActive)
	addSt := lipgloss.NewStyle().Foreground(p.Success)
	delSt := lipgloss.NewStyle().Foreground(p.Error)

	displayName := fb.Path
	if fb.OldPath != "" && fb.OldPath != fb.Path {
		displayName = fb.OldPath + " → " + fb.Path
	}

	name := nameSt.Render(" " + displayName + " ")
	nameW := lipgloss.Width(name)

	var statsStr string
	if fb.Additions > 0 {
		statsStr += addSt.Render(fmt.Sprintf("+%d", fb.Additions))
	}
	if fb.Deletions > 0 {
		if statsStr != "" {
			statsStr += " "
		}
		statsStr += delSt.Render(fmt.Sprintf("-%d", fb.Deletions))
	}
	statsStr += " "
	statsW := lipgloss.Width(statsStr)

	ruleLen := max(width-nameW-statsW, 0)
	rule := ruleSt.Render(strings.Repeat("─", ruleLen))

	return clampLine(name+rule+statsStr, width)
}

// gutterWidth returns the column width needed for line numbers.
func gutterWidth(maxLine int) int {
	if maxLine <= 0 {
		return 1
	}
	return int(math.Log10(float64(maxLine))) + 1
}

// renderSideBySide renders aligned lines in side-by-side mode with syntax
// highlighting foreground, diff background tinting, char-level annotation
// highlighting, and per-side wrapping. Long lines wrap independently within
// each half. Returns rendered lines and hunk separator output positions.
func renderSideBySide(
	lines []AlignedLine,
	fh FileHighlight,
	width int,
	maxOldLine, maxNewLine int,
	syntaxStyles map[theme.SyntaxCategory]lipgloss.Style,
	defaultSt lipgloss.Style,
	p theme.Palette,
) ([]string, []int) {
	gw := max(gutterWidth(maxOldLine), gutterWidth(maxNewLine))
	divider := lipgloss.NewStyle().Foreground(p.Border).Render("│")

	// Each side: gutter + space + content.
	// Layout: [old_gutter] [old_content] │ [new_gutter] [new_content]
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
			result = append(result, clampLine(line, width))
			continue
		}

		oldGutter := renderGutter(al.OldLineNo, gw, gutterSt)
		newGutter := renderGutter(al.NewLineNo, gw, gutterSt)

		var oldRows, newRows []string

		switch al.Kind {
		case DiffLineContext:
			oldRows = wrapContextContent(
				al.OldText, fh.OldRegions[al.OldLineNo],
				syntaxStyles, defaultSt, contentWidth,
			)
			newRows = wrapContextContent(
				al.NewText, fh.NewRegions[al.NewLineNo],
				syntaxStyles, defaultSt, contentWidth,
			)

		case DiffLineDeleted:
			oldRows = wrapDiffContent(
				al.OldText, fh.OldRegions[al.OldLineNo], al.OldAnnotations,
				syntaxStyles, defaultSt, p.DiffDelBg, p.DiffDelChar, contentWidth,
			)

		case DiffLineAdded:
			newRows = wrapDiffContent(
				al.NewText, fh.NewRegions[al.NewLineNo], al.NewAnnotations,
				syntaxStyles, defaultSt, p.DiffAddBg, p.DiffAddChar, contentWidth,
			)

		case DiffLineModified:
			oldRows = wrapDiffContent(
				al.OldText, fh.OldRegions[al.OldLineNo], al.OldAnnotations,
				syntaxStyles, defaultSt, p.DiffDelBg, p.DiffDelChar, contentWidth,
			)
			newRows = wrapDiffContent(
				al.NewText, fh.NewRegions[al.NewLineNo], al.NewAnnotations,
				syntaxStyles, defaultSt, p.DiffAddBg, p.DiffAddChar, contentWidth,
			)
		}

		rowCount := max(len(oldRows), len(newRows))
		for r := range rowCount {
			og, ng := emptyGutter, emptyGutter
			if r == 0 {
				og = oldGutter
				ng = newGutter
			}

			oldContent, newContent := emptyFill, emptyFill
			if r < len(oldRows) {
				oldContent = oldRows[r]
			}
			if r < len(newRows) {
				newContent = newRows[r]
			}

			oldSide := clampLine(og+" "+oldContent, sideWidth)
			newSide := clampLine(ng+" "+newContent, sideWidth)

			result = append(result, clampLine(oldSide+divider+newSide, width))
		}
	}
	return result, hunkStarts
}

// renderUnified renders aligned lines in unified diff mode with syntax
// highlighting foreground, diff background tinting, char-level annotation
// highlighting, and line wrapping. Returns rendered lines and hunk separator
// output positions.
func renderUnified(
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
			result = append(result, strings.Repeat(" ", width))
			continue
		}

		switch al.Kind {
		case DiffLineContext:
			og := renderGutter(al.OldLineNo, gw, gutterSt)
			ng := renderGutter(al.NewLineNo, gw, gutterSt)
			rows := wrapContextContent(
				al.OldText, fh.OldRegions[al.OldLineNo],
				syntaxStyles, defaultSt, contentWidth,
			)
			appendUnifiedRows(&result, rows, og, ng, emptyGutter, width)

		case DiffLineDeleted:
			og := renderGutter(al.OldLineNo, gw, gutterSt)
			rows := wrapDiffContent(
				al.OldText, fh.OldRegions[al.OldLineNo], al.OldAnnotations,
				syntaxStyles, defaultSt, p.DiffDelBg, p.DiffDelChar, contentWidth,
			)
			appendUnifiedRows(&result, rows, og, emptyGutter, emptyGutter, width)

		case DiffLineAdded:
			ng := renderGutter(al.NewLineNo, gw, gutterSt)
			rows := wrapDiffContent(
				al.NewText, fh.NewRegions[al.NewLineNo], al.NewAnnotations,
				syntaxStyles, defaultSt, p.DiffAddBg, p.DiffAddChar, contentWidth,
			)
			appendUnifiedRows(&result, rows, emptyGutter, ng, emptyGutter, width)

		case DiffLineModified:
			og := renderGutter(al.OldLineNo, gw, gutterSt)
			oldRows := wrapDiffContent(
				al.OldText, fh.OldRegions[al.OldLineNo], al.OldAnnotations,
				syntaxStyles, defaultSt, p.DiffDelBg, p.DiffDelChar, contentWidth,
			)
			appendUnifiedRows(&result, oldRows, og, emptyGutter, emptyGutter, width)

			ng := renderGutter(al.NewLineNo, gw, gutterSt)
			newRows := wrapDiffContent(
				al.NewText, fh.NewRegions[al.NewLineNo], al.NewAnnotations,
				syntaxStyles, defaultSt, p.DiffAddBg, p.DiffAddChar, contentWidth,
			)
			appendUnifiedRows(&result, newRows, emptyGutter, ng, emptyGutter, width)
		}
	}
	return result, hunkStarts
}

// appendUnifiedRows appends wrapped content rows to the result slice.
// The first row uses g1 and g2 as gutters; continuation rows use contGutter.
func appendUnifiedRows(result *[]string, rows []string, g1, g2, contGutter string, width int) {
	for i, content := range rows {
		left, right := g1, g2
		if i > 0 {
			left, right = contGutter, contGutter
		}
		*result = append(*result, clampLine(left+" "+right+" "+content, width))
	}
}

// clampLine truncates a styled line to width and pads with spaces if short.
func clampLine(s string, width int) string {
	s = truncateStyledLine(s, width)
	if vis := lipgloss.Width(s); vis < width {
		s += strings.Repeat(" ", width-vis)
	}
	return s
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

// maxLineNo returns the highest line number across all aligned lines.
func maxLineNo(blocks []FileBlock) (maxOld, maxNew int) {
	for _, fb := range blocks {
		for _, al := range fb.Lines {
			if al.OldLineNo > maxOld {
				maxOld = al.OldLineNo
			}
			if al.NewLineNo > maxNew {
				maxNew = al.NewLineNo
			}
		}
	}
	return
}
