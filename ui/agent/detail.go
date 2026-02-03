package agent

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// detailHeaderLines is the number of lines consumed by the agent detail header.
// Derived from: 1 (name/status line) + 1 (separator line) = 2.
const detailHeaderLines = 2

// RenderDetail renders the expanded detail view for a single agent.
// It shows a header with the agent name and status, followed by the most
// recent events that fit within the available height.
func RenderDetail(agent AgentState, evts []AgentEvent, width, height int, th *theme.Theme) string {
	if height <= 0 || width <= 0 {
		return ""
	}

	header := renderDetailHeader(agent, width, th)
	availableLines := height - detailHeaderLines
	if availableLines <= 0 {
		return header
	}

	eventLines := renderEventLines(evts, width, availableLines, th)
	return header + "\n" + eventLines
}

// renderDetailHeader builds the agent name, status icon, and separator.
func renderDetailHeader(agent AgentState, width int, th *theme.Theme) string {
	icon := statusIcon(agent.Status)
	nameStyle := th.AgentBadge(agent.AgentType).Bold(true)
	statusStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)

	headerText := fmt.Sprintf("%s %s %s",
		icon,
		nameStyle.Render(agent.Name),
		statusStyle.Render(agent.Status.String()),
	)

	separator := lipgloss.NewStyle().
		Foreground(th.Palette.Border).
		Render(strings.Repeat("\u2500", max(width, 0)))

	return headerText + "\n" + separator
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
