package highlight

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/adalundhe/sylk/ui/theme"
)

// RenderLine applies highlight regions to a single line, producing a styled
// string. Regions must be non-overlapping. Characters outside any region
// receive the default style.
func RenderLine(line string, regions []HighlightRegion, styles map[theme.SyntaxCategory]lipgloss.Style, defaultStyle lipgloss.Style) string {
	if len(regions) == 0 {
		return defaultStyle.Render(line)
	}
	sorted := sortedRegions(regions)
	runes := []rune(line)
	var b strings.Builder
	b.Grow(len(line) * 2)
	pos := 0
	for _, r := range sorted {
		// Render gap before this region.
		if r.StartCol > pos {
			b.WriteString(defaultStyle.Render(string(runes[pos:r.StartCol])))
		}
		endCol := min(r.EndCol, len(runes))
		style := resolveStyle(r.Category, styles, defaultStyle)
		b.WriteString(style.Render(string(runes[r.StartCol:endCol])))
		pos = endCol
	}
	// Render remainder after last region.
	if pos < len(runes) {
		b.WriteString(defaultStyle.Render(string(runes[pos:])))
	}
	return b.String()
}

// RenderLines renders a contiguous range of lines starting at startLine for
// count lines. This is used for virtual scrolling so only visible lines are
// rendered.
func RenderLines(lines []string, allRegions [][]HighlightRegion, styles map[theme.SyntaxCategory]lipgloss.Style, defaultStyle lipgloss.Style, startLine, count int) []string {
	end := min(startLine+count, len(lines))
	result := make([]string, 0, end-startLine)
	for i := startLine; i < end; i++ {
		var regions []HighlightRegion
		if i < len(allRegions) {
			regions = allRegions[i]
		}
		result = append(result, RenderLine(lines[i], regions, styles, defaultStyle))
	}
	return result
}

// sortedRegions returns a copy of regions sorted by StartCol.
func sortedRegions(regions []HighlightRegion) []HighlightRegion {
	out := make([]HighlightRegion, len(regions))
	copy(out, regions)
	slices.SortFunc(out, func(a, b HighlightRegion) int {
		return a.StartCol - b.StartCol
	})
	return out
}

// resolveStyle looks up the style for a category, falling back to default.
func resolveStyle(cat theme.SyntaxCategory, styles map[theme.SyntaxCategory]lipgloss.Style, defaultStyle lipgloss.Style) lipgloss.Style {
	if s, ok := styles[cat]; ok {
		return s
	}
	return defaultStyle
}
