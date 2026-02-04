package agent

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// eventEntryLines is the number of terminal lines per event entry including gap.
// Derived from: type/outcome line (1) + timestamp line (1) + content line (1) + gap (1) = 4.
const eventEntryLines = 4

// eventSelectedIndicator marks the selected event entry.
const eventSelectedIndicator = theme.IconExpand

// eventUnselectedIndent matches the width of the selection indicator plus space.
// Derived from: indicator width (1) + space (1) = 2.
const eventUnselectedIndent = "  "

// eventContentIndent is the left margin for the second line (content summary).
// Aligned with the timestamp on line 1.
// Derived from: indicator width (2).
const eventContentIndent = len(eventUnselectedIndent)

// eventDetailHeaderLines is the number of header lines in the event detail view.
// Derived from: type/outcome line (1) + timestamp line (1) + separator line (1) = 3.
const eventDetailHeaderLines = 3

// renderDetailSeparator renders a horizontal line separator for the detail view.
func renderDetailSeparator(width int, th *theme.Theme) string {
	return lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", max(width, 0)))
}

// eventContentLines is the number of content lines per entry (excluding gap).
// Derived from: type/outcome (1) + timestamp (1) + content (1) = 3.
const eventContentLines = 3

// scrollIndicatorLines is the maximum lines reserved for scroll indicators.
// Derived from: top indicator (1) + bottom indicator (1) = 2.
const scrollIndicatorLines = 2

// renderEventEntries renders navigable event entries within availableLines.
// Each entry is 3 content lines with a blank gap line between entries.
// Scroll indicators appear when events overflow above or below the viewport.
func renderEventEntries(evts []AgentEvent, width, availableLines, selectedIdx int, th *theme.Theme) string {
	if len(evts) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		return emptyStyle.Render("  No recent events")
	}

	// Reserve lines for scroll indicators when events won't all fit.
	reserved := 0
	if len(evts)*eventEntryLines > availableLines {
		reserved = scrollIndicatorLines
	}

	maxVisible := (availableLines - reserved) / eventEntryLines
	startEvt, endEvt := visibleEventWindow(len(evts), maxVisible, selectedIdx)

	var parts []string

	if startEvt > 0 {
		parts = append(parts, renderScrollIndicator(startEvt, true, th))
	}

	entries := make([]string, 0, endEvt-startEvt)
	for i := startEvt; i < endEvt; i++ {
		selected := i == selectedIdx
		lines := renderEventEntry(evts[i], width, selected, th)
		entries = append(entries, strings.Join(lines, "\n"))
	}
	parts = append(parts, strings.Join(entries, "\n\n"))

	if endEvt < len(evts) {
		parts = append(parts, renderScrollIndicator(len(evts)-endEvt, false, th))
	}

	return strings.Join(parts, "\n")
}

// renderScrollIndicator renders a line showing how many events are above or below.
func renderScrollIndicator(count int, above bool, th *theme.Theme) string {
	arrow := theme.IconArrowDown
	direction := "below"
	if above {
		arrow = theme.IconArrowUp
		direction = "above"
	}
	style := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	return style.Render(fmt.Sprintf("  %s %d more %s", arrow, count, direction))
}

// renderEventEntry formats a single event as three lines.
// Line 1: [indicator] [EventType] [outcome-icon]
// Line 2: [indent] [HH:MM:SS]
// Line 3: [indent] [truncated content summary]
func renderEventEntry(ev AgentEvent, width int, selected bool, th *theme.Theme) []string {
	indent := eventUnselectedIndent

	// Line 1: indicator + event type + outcome.
	indicator := indent
	if selected {
		style := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
		indicator = style.Render(eventSelectedIndicator) + " "
	}
	typeStyle := eventTypeStyle(ev.Outcome, th)
	outcome := outcomeGlyph(ev.Outcome, th)
	typeLine := fmt.Sprintf("%s%s %s", indicator, typeStyle.Render(ev.EventType.String()), outcome)

	// Line 2: timestamp, aligned with the indicator.
	timeStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	timeLine := indent + timeStyle.Render(ev.Timestamp.Format("15:04:05"))

	// Line 3: truncated content summary.
	contentWidth := max(width-eventContentIndent, 0)
	content := truncate(ev.Content, contentWidth)
	contentStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	if selected {
		contentStyle = lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	}
	contentLine := indent + contentStyle.Render(content)

	return []string{typeLine, timeLine, contentLine}
}

// visibleEventWindow computes the start (inclusive) and end (exclusive) event
// indices to render, ensuring the selected event is visible.
// When selectedIdx is -1, shows the most recent events (tail-follow).
func visibleEventWindow(total, maxVisible, selectedIdx int) (int, int) {
	maxVisible = min(maxVisible, total)
	if maxVisible <= 0 {
		return 0, 0
	}

	// Tail-follow: show newest events.
	if selectedIdx < 0 {
		return total - maxVisible, total
	}

	// Center the selection in the visible window.
	half := maxVisible / 2
	end := min(selectedIdx+1+half, total)
	start := max(end-maxVisible, 0)
	end = min(start+maxVisible, total)
	return start, end
}

// outcomeGlyph returns a styled icon for the event outcome.
func outcomeGlyph(outcome events.EventOutcome, th *theme.Theme) string {
	type glyphEntry struct {
		icon  string
		color lipgloss.Color
	}
	glyphs := map[events.EventOutcome]glyphEntry{
		events.OutcomeSuccess: {theme.IconSuccess, th.Palette.Success},
		events.OutcomeFailure: {theme.IconError, th.Palette.Error},
		events.OutcomePending: {theme.IconSpinner, th.Palette.Info},
	}
	if g, ok := glyphs[outcome]; ok {
		return lipgloss.NewStyle().Foreground(g.color).Render(g.icon)
	}
	return ""
}

// renderEventDetailContent renders the full content of a single event.
// Shows event header (type, timestamp, outcome) followed by word-wrapped content,
// scrolled by scrollOff lines.
func renderEventDetailContent(ev AgentEvent, width, availableLines, scrollOff int, th *theme.Theme) string {
	typeLine := fmt.Sprintf("  %s  %s",
		lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true).Render(ev.EventType.String()),
		outcomeGlyph(ev.Outcome, th),
	)
	timeLine := fmt.Sprintf("  %s",
		lipgloss.NewStyle().Foreground(th.Palette.Muted).Render(ev.Timestamp.Format("2006-01-02 15:04:05")),
	)

	separatorLine := renderDetailSeparator(width, th)

	// Wrap content to available width with indent.
	const contentIndent = 2
	contentWidth := max(width-contentIndent, 0)
	wrappedLines := wrapText(ev.Content, contentWidth)

	allLines := make([]string, 0, len(wrappedLines)+eventDetailHeaderLines)
	allLines = append(allLines, typeLine, timeLine, separatorLine)

	indent := strings.Repeat(" ", contentIndent)
	contentStyle := lipgloss.NewStyle().Foreground(th.Palette.Foreground)
	for _, wl := range wrappedLines {
		allLines = append(allLines, indent+contentStyle.Render(wl))
	}

	// Apply scroll offset.
	maxScroll := max(len(allLines)-availableLines, 0)
	start := min(scrollOff, maxScroll)
	end := min(start+availableLines, len(allLines))
	return strings.Join(allLines[start:end], "\n")
}

// wrapText splits text into lines that fit within maxWidth columns.
// Splits on whitespace boundaries where possible.
func wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	lines := make([]string, 0, len(words)/4+1)
	current := words[0]
	for _, word := range words[1:] {
		if lipgloss.Width(current+" "+word) <= maxWidth {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	lines = append(lines, current)
	return lines
}

// eventTypeStyle selects styling based on the event outcome.
func eventTypeStyle(outcome events.EventOutcome, th *theme.Theme) lipgloss.Style {
	outcomeStyles := eventOutcomeStyles(th)
	if style, ok := outcomeStyles[outcome]; ok {
		return style
	}
	return lipgloss.NewStyle().Foreground(th.Palette.Foreground)
}

type outcomeStyleMap map[events.EventOutcome]lipgloss.Style

func eventOutcomeStyles(th *theme.Theme) outcomeStyleMap {
	return outcomeStyleMap{
		events.OutcomeSuccess: lipgloss.NewStyle().Foreground(th.Palette.Success),
		events.OutcomeFailure: lipgloss.NewStyle().Foreground(th.Palette.Error),
		events.OutcomePending: lipgloss.NewStyle().Foreground(th.Palette.Info),
	}
}
