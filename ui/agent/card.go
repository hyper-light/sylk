package agent

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// Agent dot glyphs. Selected agents use a filled dot; unselected use an outline.
const (
	agentDotFilled  = "●" // U+25CF BLACK CIRCLE
	agentDotOutline = "○" // U+25CB WHITE CIRCLE
)

// selectedIndicator is the left-side marker for the currently selected card.
const selectedIndicator = theme.IconExpand

// unselectedIndicator is the left-side marker for non-selected cards.
const unselectedIndicator = " "

// cardPadding accounts for the indicator character plus a space separator.
// Derived from: len(selectedIndicator) + 1 space.
const cardPadding = 2


// contextBarWidth is the fixed width for the context usage percentage.
// Derived from: 3 digits + "%" = 4 characters.
const contextBarWidth = 4

// RenderCard renders a compact one-line agent card.
// It shows: [indicator] [status icon] [agent name] [task summary...] [context %]
// The selected agent uses Success (green) styling; all others use Muted.
func RenderCard(agent AgentState, width int, th *theme.Theme, selected bool) string {
	icon := agentStyledDot(selected, th)
	indicator := selectIndicator(selected, th)

	nameStyle := agentNameStyle(selected, th)
	name := nameStyle.Render(agent.Name)
	nameLen := lipgloss.Width(name)

	contextPct := formatContextPct(agent.ContextUsage)

	// Calculate available space for the task summary.
	// Layout: [indicator space] [icon space] [name space] [summary] [ context%]
	// Fixed overhead: cardPadding + icon(1) + space(1) + space(1) + contextBarWidth + space(1)
	iconWidth := 1
	separators := 3 // spaces between icon/name, name/summary, summary/context
	fixedWidth := cardPadding + iconWidth + nameLen + separators + contextBarWidth
	summaryWidth := width - fixedWidth

	summary := truncate(agent.TaskSummary, summaryWidth)
	summaryStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	summary = summaryStyle.Render(padRight(summary, summaryWidth))

	contextStyle := contextPctStyle(agent.ContextUsage, th)
	contextStr := contextStyle.Render(contextPct)

	return fmt.Sprintf("%s %s %s %s %s",
		indicator, icon, name, summary, contextStr)
}

// agentStyledDot renders the agent dot.
// Selected: filled dot in Success (green). Unselected: outline dot in Muted.
func agentStyledDot(selected bool, th *theme.Theme) string {
	if selected {
		return th.AgentActive.Render(agentDotFilled)
	}
	return th.AgentInactive.Render(agentDotOutline)
}

// agentNameStyle returns the style for an agent name.
// Selected: AgentActive (green bold). Unselected: AgentInactive (muted).
func agentNameStyle(selected bool, th *theme.Theme) lipgloss.Style {
	if selected {
		return th.AgentActive
	}
	return th.AgentInactive
}

// selectIndicator returns the visual indicator for selection state.
// Selected: green arrow. Unselected: space.
func selectIndicator(selected bool, th *theme.Theme) string {
	if selected {
		return th.AgentActive.Render(selectedIndicator)
	}
	return unselectedIndicator
}

// formatContextPct formats a 0.0-1.0 usage fraction as a percentage string.
func formatContextPct(usage float64) string {
	pct := int(usage * 100)
	pct = min(pct, 100)
	pct = max(pct, 0)
	return fmt.Sprintf("%3d%%", pct)
}

// contextPctStyle selects a style based on context usage thresholds.
func contextPctStyle(usage float64, th *theme.Theme) lipgloss.Style {
	// Thresholds derived from common resource monitoring conventions:
	// <70% = normal, 70-90% = warning, >90% = critical.
	const warningThreshold = 0.70
	const criticalThreshold = 0.90

	if usage >= criticalThreshold {
		return th.StatusError
	}
	if usage >= warningThreshold {
		return th.StatusWarning
	}
	return th.StatusNormal
}

// truncate shortens s to fit within maxWidth, appending an ellipsis if needed.
func truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	// Reserve 1 char for ellipsis.
	ellipsis := "\u2026"
	for i := range s {
		if lipgloss.Width(s[:i]+ellipsis) > maxWidth {
			if i == 0 {
				return ellipsis[:min(len(ellipsis), maxWidth)]
			}
			return s[:i-1] + ellipsis
		}
	}
	return s
}

// padRight pads s with spaces to reach the target width.
func padRight(s string, targetWidth int) string {
	current := lipgloss.Width(s)
	if current >= targetWidth {
		return s
	}
	return s + strings.Repeat(" ", targetWidth-current)
}
