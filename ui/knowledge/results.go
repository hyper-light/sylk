package knowledge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// ResultEntry
// ---------------------------------------------------------------------------

// ResultEntry represents a single knowledge search result.
type ResultEntry struct {
	Title      string
	Source     string
	Score      float64
	SourceType string // "bleve", "vector", "graph", "combined"
}

// ---------------------------------------------------------------------------
// Source type badge configuration (table-driven)
// ---------------------------------------------------------------------------

// sourceBadge maps a source type string to a display label and a palette
// color selector function. This avoids a switch cascade.
type sourceBadge struct {
	Label    string
	ColorFn  func(p theme.Palette) lipgloss.Color
}

var sourceBadgeTable = map[string]sourceBadge{
	"bleve":    {Label: "TXT", ColorFn: func(p theme.Palette) lipgloss.Color { return p.Success }},
	"vector":   {Label: "VEC", ColorFn: func(p theme.Palette) lipgloss.Color { return p.Primary }},
	"graph":    {Label: "GRF", ColorFn: func(p theme.Palette) lipgloss.Color { return p.Secondary }},
	"combined": {Label: "ALL", ColorFn: func(p theme.Palette) lipgloss.Color { return p.Info }},
}

// defaultBadge is used when the source type is not in the table.
var defaultBadge = sourceBadge{
	Label:   "???",
	ColorFn: func(p theme.Palette) lipgloss.Color { return p.Muted },
}

// ---------------------------------------------------------------------------
// Relevance bar configuration
// ---------------------------------------------------------------------------

// relevanceBarWidth is the number of characters in the score visualization.
// Derived from: 10 characters provides 10% granularity per cell.
const relevanceBarWidth = 10

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// RenderResults renders a sorted list of results with relevance bars and
// source type badges. Results are sorted by score descending before
// rendering. The selected parameter highlights the currently focused result.
func RenderResults(entries []ResultEntry, th *theme.Theme, width, maxVisible, scrollOffset, selected int) string {
	if len(entries) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		return emptyStyle.Render("  No results found")
	}

	// Sort by score descending (stable to preserve insertion order for ties).
	sorted := make([]ResultEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})

	end := min(scrollOffset+maxVisible, len(sorted))
	visible := sorted[scrollOffset:end]

	var b strings.Builder
	b.Grow(width * len(visible))

	for i, entry := range visible {
		if i > 0 {
			b.WriteByte('\n')
		}
		absIdx := scrollOffset + i
		b.WriteString(renderResultLine(entry, th, width, absIdx == selected))
	}

	// Scroll indicator.
	if len(sorted) > maxVisible {
		indicatorStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
		indicator := fmt.Sprintf(" [%d-%d of %d]", scrollOffset+1, end, len(sorted))
		b.WriteByte('\n')
		b.WriteString(indicatorStyle.Render(indicator))
	}

	return b.String()
}

// renderResultLine renders a single result entry with badge, title, relevance
// bar, and score.
func renderResultLine(entry ResultEntry, th *theme.Theme, width int, isSelected bool) string {
	// Resolve badge.
	badge, ok := sourceBadgeTable[entry.SourceType]
	if !ok {
		badge = defaultBadge
	}

	badgeColor := badge.ColorFn(th.Palette)
	badgeStyle := lipgloss.NewStyle().
		Foreground(badgeColor).
		Bold(true)

	titleStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	selectedTitleStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
	scoreStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	pointerStyle := lipgloss.NewStyle().Foreground(th.Palette.Primary)

	var b strings.Builder

	// Selection pointer.
	if isSelected {
		b.WriteString(pointerStyle.Render(theme.IconArrowRight + " "))
	} else {
		b.WriteString("  ")
	}

	// Source badge.
	b.WriteString(badgeStyle.Render("["+badge.Label+"]"))
	b.WriteByte(' ')

	// Title.
	if isSelected {
		b.WriteString(selectedTitleStyle.Render(entry.Title))
	} else {
		b.WriteString(titleStyle.Render(entry.Title))
	}
	b.WriteByte(' ')

	// Relevance bar.
	b.WriteString(renderRelevanceBar(entry.Score, th))
	b.WriteByte(' ')

	// Numeric score.
	b.WriteString(scoreStyle.Render(fmt.Sprintf("%.2f", entry.Score)))

	rendered := b.String()
	if lipgloss.Width(rendered) > width {
		rendered = truncateRendered(rendered, width)
	}
	return rendered
}

// renderRelevanceBar creates a visual bar representing the score from 0.0 to 1.0.
func renderRelevanceBar(score float64, th *theme.Theme) string {
	clamped := max(min(score, 1.0), 0.0)
	filled := int(clamped * float64(relevanceBarWidth))
	filled = min(filled, relevanceBarWidth)
	empty := relevanceBarWidth - filled

	filledStyle := barColorForScore(clamped, th)
	emptyStyle := lipgloss.NewStyle().Foreground(th.Palette.Subtle)

	return filledStyle.Render(strings.Repeat("▰", filled)) +
		emptyStyle.Render(strings.Repeat("▱", empty))
}

// barColorForScore selects a color based on score thresholds.
// Thresholds derived from: low (<0.33), medium (<0.67), high (>=0.67).
func barColorForScore(score float64, th *theme.Theme) lipgloss.Style {
	type threshold struct {
		limit float64
		color lipgloss.Color
	}
	// Thresholds from low to high; derived from terciles of [0,1].
	thresholds := []threshold{
		{limit: 1.0 / 3.0, color: th.Palette.Error},
		{limit: 2.0 / 3.0, color: th.Palette.Warning},
	}
	for _, t := range thresholds {
		if score < t.limit {
			return lipgloss.NewStyle().Foreground(t.color)
		}
	}
	return lipgloss.NewStyle().Foreground(th.Palette.Success)
}

// truncateRendered truncates styled text to fit within maxWidth.
func truncateRendered(s string, maxWidth int) string {
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return s
	}
	// Ellipsis occupies 3 rune positions.
	ellipsisLen := 3
	cutoff := max(maxWidth-ellipsisLen, 0)
	return string(runes[:cutoff]) + "..."
}
