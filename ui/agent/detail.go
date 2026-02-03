package agent

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// renderDetailSeparator renders a horizontal line separator for the detail view.
func renderDetailSeparator(width int, th *theme.Theme) string {
	return lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", max(width, 0)))
}

// renderEventLines renders the most recent events that fit in availableLines.
func renderEventLines(evts []AgentEvent, width, availableLines int, th *theme.Theme) string {
	if len(evts) == 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		return emptyStyle.Render("  No recent events")
	}

	lines := make([]string, 0, min(len(evts), availableLines))
	// Iterate events in reverse (newest first) and collect lines
	// that fit, then reverse for chronological display.
	for i := len(evts) - 1; i >= 0 && len(lines) < availableLines; i-- {
		line := renderEventLine(evts[i], width, th)
		lines = append(lines, line)
	}

	// Reverse to restore chronological order (oldest first).
	reverseStrings(lines)
	return strings.Join(lines, "\n")
}

// renderEventLine formats a single event as a one-line string.
func renderEventLine(ev AgentEvent, width int, th *theme.Theme) string {
	timeStr := ev.Timestamp.Format("15:04:05")
	timeStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	typeStyle := eventTypeStyle(ev.Outcome, th)

	prefix := fmt.Sprintf("  %s %s",
		timeStyle.Render(timeStr),
		typeStyle.Render(ev.EventType.String()),
	)

	prefixWidth := lipgloss.Width(prefix)
	// 1 space separator between prefix and content.
	contentWidth := width - prefixWidth - 1
	content := truncate(ev.Content, contentWidth)

	return fmt.Sprintf("%s %s", prefix, content)
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

// reverseStrings reverses a slice of strings in place.
func reverseStrings(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
