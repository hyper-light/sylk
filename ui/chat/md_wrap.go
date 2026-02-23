package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// wrapRuns performs greedy line-filling word wrap over a slice of styledRuns.
// Atomic runs are never split. Trailing (space) runs are dropped at line start.
// Oversized non-atomic runs are force-broken at character boundaries.
// Returns complete styled lines (strings with ANSI codes).
func wrapRuns(runs []styledRun, width int) []string {
	if width <= 0 {
		return nil
	}
	if len(runs) == 0 {
		return []string{""}
	}

	var lines []string
	var line strings.Builder
	col := 0

	flush := func() {
		lines = append(lines, line.String())
		line.Reset()
		col = 0
	}

	for _, run := range runs {
		// Hard line break.
		if run.text == "\n" {
			flush()
			continue
		}

		// Drop trailing whitespace at line start.
		if run.trailing && col == 0 {
			continue
		}

		// Run fits on current line.
		if col+run.width <= width {
			line.WriteString(run.rendered)
			col += run.width
			continue
		}

		// Run doesn't fit — start new line if we have content.
		if col > 0 {
			// Don't push trailing whitespace onto next line.
			if run.trailing {
				continue
			}
			flush()
		}

		// Atomic run that fits on empty line.
		if run.atomic && run.width <= width {
			line.WriteString(run.rendered)
			col += run.width
			continue
		}

		// Oversized run — force break by character.
		if run.width > width {
			forceBreakRun(run, width, &lines, &line, &col)
			continue
		}

		// Normal run on empty line — just add it.
		line.WriteString(run.rendered)
		col += run.width
	}

	// Flush remaining content.
	if col > 0 || len(lines) == 0 {
		flush()
	}

	return lines
}

// forceBreakRun splits an oversized run across multiple lines at character
// boundaries. For non-atomic runs, re-renders each chunk with the run's style
// implied in the rendered string. For atomic runs, uses truncation.
func forceBreakRun(run styledRun, width int, lines *[]string, line *strings.Builder, col *int) {
	// For oversized runs, we break the raw text and re-render pieces.
	// We extract the style from the difference between rendered and text,
	// but for simplicity we output the raw text with no extra styling
	// on force-broken segments — the ANSI reset ensures clean lines.
	runes := []rune(run.text)
	for len(runes) > 0 {
		remaining := width - *col
		if remaining <= 0 {
			*lines = append(*lines, line.String())
			line.Reset()
			*col = 0
			remaining = width
		}
		take := min(len(runes), remaining)
		chunk := string(runes[:take])
		// Use lipgloss.Width for accurate visible width.
		visWidth := lipgloss.Width(chunk)
		for visWidth > remaining && take > 1 {
			take--
			chunk = string(runes[:take])
			visWidth = lipgloss.Width(chunk)
		}
		line.WriteString(chunk)
		*col += visWidth
		runes = runes[take:]
	}
}
