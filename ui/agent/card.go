package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// statusIcons maps AgentStatus to its display glyph from theme/icons.go.
var statusIcons = map[AgentStatus]string{
	StatusIdle:     theme.IconIdle,
	StatusThinking: theme.IconThinking,
	StatusActing:   theme.IconActing,
	StatusError:    theme.IconError,
	StatusSuccess:  theme.IconSuccess,
	StatusHandoff:  theme.IconHandoff,
	StatusWaiting:  theme.IconIdle,
}

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

// selectedCardLines is the number of terminal lines a selected card occupies.
const selectedCardLines = 1

// unselectedCardLines is the number of terminal lines an unselected card occupies.
const unselectedCardLines = 1

// dotAnimFrameCount is the local alias for theme.DotAnimFrameCount.
const dotAnimFrameCount = theme.DotAnimFrameCount

// AnimState carries per-frame animation parameters through the render pipeline.
// Constructed once per frame in the Model and passed to each RenderCard call.
type AnimState struct {
	DotFrame        int
	Elapsed         time.Duration
	HasActive       bool            // Any agent actively working (gates dot animation).
	Ripple          bool            // Active agents present (gates ripple text).
	RippleGrad      *theme.Gradient // ThinkingGradient — used for active (non-selected) agents.
	HolographicGrad *theme.Gradient // GroupGradient — used for the selected active agent.
	NerdFonts       bool
}

// RenderCard renders a single-line agent card with an optional tree prefix.
// Layout: [prefix][indicator] [status-icon] [name] [summary-truncated] [context%]
// When engaged is true, the agent name uses the engagement style (bold accent).
// The prefix (e.g. " │ ") is prepended and its visual width subtracted from available space.
// AnimState carries per-frame animation parameters for dot and ripple effects.
func RenderCard(agent AgentState, width int, th *theme.Theme, selected, engaged bool, prefix string, anim AnimState) string {
	if selected {
		return renderSelectedCard(agent, width, th, engaged, prefix, anim)
	}
	return renderCompactCard(agent, width, th, engaged, prefix, anim)
}

// cardLineCount returns the number of terminal lines a card occupies.
func cardLineCount(selected bool) int {
	if selected {
		return selectedCardLines
	}
	return unselectedCardLines
}

// renderCompactCard renders a single-line card.
// Layout: [prefix][indicator] [icon] [name] [truncated summary...] [context %]
func renderCompactCard(agent AgentState, width int, th *theme.Theme, engaged bool, prefix string, anim AnimState) string {
	return renderCardLine(agent, width, th, false, engaged, prefix, anim)
}

// renderCardLine renders the one-line card layout with configurable selection styling.
// The prefix is prepended and its visual width is deducted from the available space.
func renderCardLine(agent AgentState, width int, th *theme.Theme, selected, engaged bool, prefix string, anim AnimState) string {
	rawIcon := resolveAgentIconGlyph(agent, anim)
	icon := agentStatusDot(agent, rawIcon, selected, th, anim)
	indicator := selectIndicator(selected, th)

	nameLen := lipgloss.Width(agent.Name)
	name := renderAgentName(agent.Name, selected, engaged, agent.Status, anim, th)
	contextPct := formatContextPct(agent.ContextUsage)
	contextStyle := contextPctStyle(agent.ContextUsage, th)
	contextStr := contextStyle.Render(contextPct)

	// Reserve the rightmost percentage column from the rendered left segment so
	// minor glyph-width differences do not shift the context suffix horizontally.
	leftBase := fmt.Sprintf("%s%s %s %s ", prefix, indicator, icon, name)
	leftWidth := max(width-contextBarWidth-1, 0)
	summaryWidth := max(leftWidth-displayWidth(leftBase, anim.NerdFonts), 0)

	summary := renderAgentSummary(agent.TaskSummary, summaryWidth, selected, agent.Status, nameLen, anim, th)
	leftPart := padRightDisplay(leftBase+summary, leftWidth, anim.NerdFonts)
	return leftPart + " " + contextStr
}

// renderSelectedCard renders a two-line card for the selected agent.
// Line 1: [prefix][indicator] [icon] [name] [truncated summary] [context %]
// Line 2: [indent] [full task summary]
func renderSelectedCard(agent AgentState, width int, th *theme.Theme, engaged bool, prefix string, anim AnimState) string {
	return renderCardLine(agent, width, th, true, engaged, prefix, anim)
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

// resolveStaticIcon returns the static display glyph for a non-active status.
func resolveStaticIcon(status AgentStatus) string {
	if icon := statusIcons[status]; icon != "" {
		return icon
	}
	return theme.IconIdle
}

// resolveStatusColor returns the palette color for a given status.
func resolveStatusColor(status AgentStatus, th *theme.Theme) lipgloss.Color {
	if colorFn := statusColors[status]; colorFn != nil {
		return colorFn(th)
	}
	return th.Palette.Muted
}

// agentStatusDot renders a status icon reflecting the agent's operational state.
// Active agents get an animated origami-bloom glyph; gradient color only when
// the agent panel is focused (Ripple). Otherwise the animated glyph uses the
// static status color. Inactive agents get static icons.
func agentStatusDot(agent AgentState, icon string, selected bool, th *theme.Theme, anim AnimState) string {
	if icon == "" {
		icon = resolveStaticIcon(agent.Status)
	}
	if isActiveStatus(agent.Status) && anim.HasActive {
		if anim.Ripple && anim.RippleGrad != nil {
			return theme.RenderGradientGlyph(icon, anim.RippleGrad, anim.Elapsed)
		}
		return lipgloss.NewStyle().Foreground(resolveStatusColor(agent.Status, th)).Render(icon)
	}
	if selected {
		return th.AgentActive.Render(icon)
	}
	return lipgloss.NewStyle().Foreground(resolveStatusColor(agent.Status, th)).Render(icon)
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

// renderAgentName renders the agent name, applying the per-character ripple
// gradient when the agent is actively working and ripple is available.
// Falls back to standard lipgloss styling otherwise.
func renderAgentName(name string, selected, engaged bool, status AgentStatus, anim AnimState, th *theme.Theme) string {
	if anim.Ripple && isActiveStatus(status) {
		// Selected active agent: holographic shimmer. Other active agents: thinking shimmer.
		grad := anim.RippleGrad
		if selected && anim.HolographicGrad != nil {
			grad = anim.HolographicGrad
		}
		if grad != nil {
			styled := theme.RenderRippleText(name, anim.Elapsed, grad, 0)
			if engaged {
				return "\x1b[1;4m" + styled
			}
			return styled
		}
	}
	style := agentNameStyle(selected, th)
	if engaged {
		style = style.Bold(true).Underline(true)
	}
	return style.Render(name)
}

// renderAgentSummary renders the task summary, applying the per-character ripple
// gradient when the agent is actively working. The character offset continues
// from the name so the wave flows continuously across name and summary.
func renderAgentSummary(text string, width int, selected bool, status AgentStatus, nameLen int, anim AnimState, th *theme.Theme) string {
	truncated := truncate(text, width)
	padded := padRight(truncated, width)
	if anim.Ripple && isActiveStatus(status) {
		grad := anim.RippleGrad
		if selected && anim.HolographicGrad != nil {
			grad = anim.HolographicGrad
		}
		if grad != nil {
			return theme.RenderRippleText(padded, anim.Elapsed, grad, nameLen+1)
		}
	}
	return lipgloss.NewStyle().Foreground(th.Palette.Muted).Render(padded)
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
	return truncateDisplayWidth(s, maxWidth, "\u2026")
}

func truncateDisplayWidth(s string, maxWidth int, suffix string) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	suffixWidth := lipgloss.Width(suffix)
	if suffixWidth >= maxWidth {
		return suffix
	}
	var out strings.Builder
	for _, r := range s {
		next := out.String() + string(r)
		if lipgloss.Width(next)+suffixWidth > maxWidth {
			break
		}
		out.WriteRune(r)
	}
	if out.Len() == 0 {
		return suffix
	}
	return out.String() + suffix
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

func padRightDisplay(s string, targetWidth int, nerdFonts bool) string {
	current := displayWidth(s, nerdFonts)
	if current >= targetWidth {
		return s
	}
	return s + strings.Repeat(" ", targetWidth-current)
}

func displayWidth(s string, nerdFonts bool) int {
	_ = nerdFonts
	return ansi.StringWidth(s)
}
