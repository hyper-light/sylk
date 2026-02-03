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
// The active agent uses Success (green) styling; inactive agents match the
// non-active session entry pattern (Muted/Secondary based on selection).
func RenderCard(agent AgentState, width int, th *theme.Theme, selected, focused, active bool) string {
	icon := agentStyledDot(active, selected, focused, th)
	indicator := selectIndicator(selected, focused, th)

	nameStyle := agentNameStyle(active, selected, focused, th)
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
// Selected agents always get a filled dot so the cursor position is visible.
// Active agents use AgentActive (green); selected uses Secondary/Muted;
// unselected inactive agents get an outline dot in Muted.
func agentStyledDot(active, selected, focused bool, th *theme.Theme) string {
	if selected {
		color := agentSelectionColor(th)
		return lipgloss.NewStyle().Foreground(color).Render(agentDotFilled)
	}
	if active {
		return th.AgentActive.Render(agentDotFilled)
	}
	return th.AgentInactive.Render(agentDotOutline)
}

// agentNameStyle returns the style for an agent name.
// Selected agents use the selection color (green if active, Secondary/Muted otherwise).
// Unselected active agents use AgentActive; unselected inactive use AgentInactive.
func agentNameStyle(active, selected, focused bool, th *theme.Theme) lipgloss.Style {
	if selected {
		color := agentSelectionColor(th)
		return lipgloss.NewStyle().Foreground(color).Bold(true)
	}
	if active {
		return th.AgentActive
	}
	return th.AgentInactive
}

// agentSelectionColor returns the foreground color for a selected agent.
// Selected agents always use Success (green) regardless of focus state.
func agentSelectionColor(th *theme.Theme) lipgloss.Color {
	return th.Palette.Success
}

// selectIndicator returns the visual indicator for selection state.
// When selected and focused, uses Primary color. When selected but unfocused, uses Muted.
func selectIndicator(selected, focused bool, th *theme.Theme) string {
	if selected {
		color := th.Palette.Muted
		if focused {
			color = th.Palette.Secondary
		}
		return lipgloss.NewStyle().Foreground(color).Render(selectedIndicator)
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
