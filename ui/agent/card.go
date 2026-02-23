package agent

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// statusIcons maps AgentStatus to its display glyph from theme/icons.go.
var statusIcons = map[AgentStatus]string{
	StatusIdle:     theme.IconIdle,
	StatusThinking: theme.IconThinking,
	StatusActing:   theme.IconActing,
	StatusError:    theme.IconError,
	StatusSuccess:  theme.IconSuccess,
	StatusHandoff:  theme.IconHandoff,
	StatusWaiting:  theme.IconWaiting,
}

// selectedIndicator is the left-side marker for the currently selected card.
const selectedIndicator = theme.IconExpand

// unselectedIndicator is the left-side marker for non-selected cards.
const unselectedIndicator = " "

// cardPadding accounts for the indicator character plus a space separator.
// Derived from: len(selectedIndicator) + 1 space.
const cardPadding = 2

// iconWidth is the column width of the agent dot glyph.
const iconWidth = 1

// contextBarWidth is the fixed width for the context usage percentage.
// Derived from: 3 digits + "%" = 4 characters.
const contextBarWidth = 4

// selectedCardLines is the number of terminal lines a selected card occupies.
const selectedCardLines = 1

// unselectedCardLines is the number of terminal lines an unselected card occupies.
const unselectedCardLines = 1

// RenderCard renders a single-line agent card.
// Layout: [indicator] [status-icon] [name] [summary-truncated] [context%]
// When engaged is true, the agent name uses the engagement style (bold accent).
func RenderCard(agent AgentState, width int, th *theme.Theme, selected, engaged bool) string {
	if selected {
		return renderSelectedCard(agent, width, th, engaged)
	}
	return renderCompactCard(agent, width, th, engaged)
}

// cardLineCount returns the number of terminal lines a card occupies.
func cardLineCount(selected bool) int {
	if selected {
		return selectedCardLines
	}
	return unselectedCardLines
}

// renderCompactCard renders a single-line card.
// Layout: [indicator] [icon] [name] [truncated summary...] [context %]
func renderCompactCard(agent AgentState, width int, th *theme.Theme, engaged bool) string {
	return renderCardLine(agent, width, th, false, engaged)
}

// renderCardLine renders the one-line card layout with configurable selection styling.
func renderCardLine(agent AgentState, width int, th *theme.Theme, selected, engaged bool) string {
	icon := agentStatusDot(agent.Status, selected, th)
	indicator := selectIndicator(selected, th)

	nameStyle := agentNameStyle(selected, th)
	if engaged {
		nameStyle = nameStyle.Bold(true).Underline(true)
	}
	name := nameStyle.Render(agent.Name)
	nameLen := lipgloss.Width(name)

	contextPct := formatContextPct(agent.ContextUsage)

	// Calculate available space for the task summary.
	// Layout: [indicator space] [icon space] [name space] [summary] [ context%]
	// Fixed overhead: cardPadding + iconWidth + space(1) + space(1) + contextBarWidth + space(1)
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

// renderSelectedCard renders a two-line card for the selected agent.
// Line 1: [indicator] [icon] [name] [truncated summary] [context %]
// Line 2: [indent] [full task summary]
func renderSelectedCard(agent AgentState, width int, th *theme.Theme, engaged bool) string {
	return renderCardLine(agent, width, th, true, engaged)
}

// statusColors maps AgentStatus to a palette color accessor.
var statusColors = map[AgentStatus]func(*theme.Theme) lipgloss.Color{
	StatusIdle:     func(th *theme.Theme) lipgloss.Color { return th.Palette.Muted },
	StatusThinking: func(th *theme.Theme) lipgloss.Color { return th.Palette.Info },
	StatusActing:   func(th *theme.Theme) lipgloss.Color { return th.Palette.Success },
	StatusError:    func(th *theme.Theme) lipgloss.Color { return th.Palette.Error },
	StatusSuccess:  func(th *theme.Theme) lipgloss.Color { return th.Palette.Success },
	StatusHandoff:  func(th *theme.Theme) lipgloss.Color { return th.Palette.Warning },
	StatusWaiting:  func(th *theme.Theme) lipgloss.Color { return th.Palette.Muted },
}

// agentStatusDot renders a status icon reflecting the agent's operational state.
// The icon shape conveys status; the color conveys selection.
// Selected agents use AgentActive (green+bold); unselected use status-based colors.
func agentStatusDot(status AgentStatus, selected bool, th *theme.Theme) string {
	icon := statusIcons[status]
	if icon == "" {
		icon = theme.IconIdle
	}
	if selected {
		return th.AgentActive.Render(icon)
	}
	colorFn := statusColors[status]
	if colorFn == nil {
		colorFn = func(th *theme.Theme) lipgloss.Color { return th.Palette.Muted }
	}
	return lipgloss.NewStyle().Foreground(colorFn(th)).Render(icon)
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

// truncate flattens s to a single line and shortens it to fit within
// maxWidth, appending an ellipsis if needed. Newlines, carriage returns,
// and tabs are replaced with spaces; consecutive whitespace is collapsed.
// This is the render-boundary guard — all card text passes through here.
func truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	s = flattenToLine(s)
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

// flattenToLine collapses newlines and consecutive whitespace into a
// single space, producing a single-line string.
func flattenToLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		space := r == ' '
		if space && prevSpace {
			continue
		}
		prevSpace = space
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// padRight pads s with spaces to reach the target width.
func padRight(s string, targetWidth int) string {
	current := lipgloss.Width(s)
	if current >= targetWidth {
		return s
	}
	return s + strings.Repeat(" ", targetWidth-current)
}
