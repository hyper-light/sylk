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
		startCol := max(r.StartCol, pos)
		endCol := min(r.EndCol, len(runes))
		if startCol >= endCol {
			continue
		}
		// Render gap before this region.
		if startCol > pos {
			b.WriteString(defaultStyle.Render(string(runes[pos:startCol])))
		}
		style := resolveStyle(r.Category, styles, defaultStyle)
		b.WriteString(style.Render(string(runes[startCol:endCol])))
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

// ---------------------------------------------------------------------------
// Underline overlay
// ---------------------------------------------------------------------------

// UnderlineRange marks a column range that should be rendered with underline.
// Used for diagnostic squiggles. Columns are display-space, 0-indexed,
// EndCol exclusive.
type UnderlineRange struct {
	StartCol int
	EndCol   int
}

// RenderLineWithUnderlines works like RenderLine but adds underline styling
// to characters falling within any of the provided underline ranges.
// Syntax highlighting is preserved; only the underline attribute is added.
func RenderLineWithUnderlines(
	line string,
	regions []HighlightRegion,
	styles map[theme.SyntaxCategory]lipgloss.Style,
	defaultStyle lipgloss.Style,
	underlines []UnderlineRange,
) string {
	if len(underlines) == 0 {
		return RenderLine(line, regions, styles, defaultStyle)
	}
	sorted := sortedRegions(regions)
	runes := []rune(line)
	n := len(runes)
	var b strings.Builder
	b.Grow(len(line) * 2)

	// Build a per-character underline lookup for O(1) checks.
	ulMask := make([]bool, n)
	for _, u := range underlines {
		for c := max(u.StartCol, 0); c < min(u.EndCol, n); c++ {
			ulMask[c] = true
		}
	}

	// Render character-by-character, applying syntax style + optional underline.
	// Walk regions in order; characters between regions use defaultStyle.
	regionIdx := 0
	for pos := 0; pos < n; {
		// Advance past exhausted regions.
		for regionIdx < len(sorted) && sorted[regionIdx].EndCol <= pos {
			regionIdx++
		}
		var style lipgloss.Style
		var spanEnd int
		if regionIdx < len(sorted) && pos >= sorted[regionIdx].StartCol {
			// Inside a syntax region.
			style = resolveStyle(sorted[regionIdx].Category, styles, defaultStyle)
			spanEnd = min(sorted[regionIdx].EndCol, n)
		} else if regionIdx < len(sorted) {
			// Gap before the next region.
			style = defaultStyle
			spanEnd = min(sorted[regionIdx].StartCol, n)
		} else {
			// After all regions.
			style = defaultStyle
			spanEnd = n
		}

		// Split the span at underline boundaries so each chunk has
		// a uniform underline state.
		for pos < spanEnd {
			ul := ulMask[pos]
			chunkEnd := pos + 1
			for chunkEnd < spanEnd && ulMask[chunkEnd] == ul {
				chunkEnd++
			}
			s := style
			if ul {
				s = s.Underline(true)
			}
			b.WriteString(s.Render(string(runes[pos:chunkEnd])))
			pos = chunkEnd
		}
	}
	return b.String()
}

// RenderLineWithBgRanges works like RenderLine but adds a background color
// to characters falling within any of the provided column ranges.
// Syntax highlighting foreground colors are preserved.
func RenderLineWithBgRanges(
	line string,
	regions []HighlightRegion,
	styles map[theme.SyntaxCategory]lipgloss.Style,
	defaultStyle lipgloss.Style,
	bgRanges []UnderlineRange,
	bg lipgloss.Color,
) string {
	if len(bgRanges) == 0 {
		return RenderLine(line, regions, styles, defaultStyle)
	}
	sorted := sortedRegions(regions)
	runes := []rune(line)
	n := len(runes)
	var b strings.Builder
	b.Grow(len(line) * 2)

	bgMask := make([]bool, n)
	for _, r := range bgRanges {
		for c := max(r.StartCol, 0); c < min(r.EndCol, n); c++ {
			bgMask[c] = true
		}
	}

	regionIdx := 0
	for pos := 0; pos < n; {
		for regionIdx < len(sorted) && sorted[regionIdx].EndCol <= pos {
			regionIdx++
		}
		var style lipgloss.Style
		var spanEnd int
		if regionIdx < len(sorted) && pos >= sorted[regionIdx].StartCol {
			style = resolveStyle(sorted[regionIdx].Category, styles, defaultStyle)
			spanEnd = min(sorted[regionIdx].EndCol, n)
		} else if regionIdx < len(sorted) {
			style = defaultStyle
			spanEnd = min(sorted[regionIdx].StartCol, n)
		} else {
			style = defaultStyle
			spanEnd = n
		}

		for pos < spanEnd {
			hasBg := bgMask[pos]
			chunkEnd := pos + 1
			for chunkEnd < spanEnd && bgMask[chunkEnd] == hasBg {
				chunkEnd++
			}
			s := style
			if hasBg {
				s = s.Background(bg)
			}
			b.WriteString(s.Render(string(runes[pos:chunkEnd])))
			pos = chunkEnd
		}
	}
	return b.String()
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
