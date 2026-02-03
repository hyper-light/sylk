package session

import (
	"fmt"
	"strings"
	"time"

	coresession "github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// SessionSummary holds the display-ready state of a single session.
type SessionSummary struct {
	ID        string
	Name      string
	Branch    string
	State     coresession.State
	CreatedAt time.Time
	Active    bool
}

// Session dot glyphs. Active sessions use a filled dot; all others use an outline.
const (
	sessionDotFilled  = "●" // U+25CF BLACK CIRCLE
	sessionDotOutline = "○" // U+25CB WHITE CIRCLE
)


// selectedIndicator is the left-side marker for the currently selected entry.
const selectedIndicator = theme.IconExpand

// unselectedPad replaces the indicator with a space for non-selected entries.
const unselectedPad = " "

// RenderList renders the session list with the selected entry highlighted.
// When focused is false, the selected entry uses a subdued style.
func RenderList(summaries []SessionSummary, selected int, width, height int, focused bool, th *theme.Theme) string {
	if len(summaries) == 0 || height <= 0 || width <= 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		return emptyStyle.Render("  No sessions")
	}

	visStart, visEnd := visibleWindow(selected, len(summaries), height)

	lines := make([]string, 0, visEnd-visStart)
	for i := visStart; i < visEnd; i++ {
		line := renderSessionEntry(summaries[i], i == selected, focused, width, th)
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// entryFixedWidth is the space consumed by the indicator, icon, and separating spaces.
// Derived from: indicator(1) + space(1) + icon(1) + space(1) = 4.
const entryFixedWidth = 4

// renderSessionEntry renders a single session list entry, truncated to width.
func renderSessionEntry(s SessionSummary, selected, focused bool, width int, th *theme.Theme) string {
	indicator, nameStyle := entryStyles(s.Active, selected, focused, th)
	icon := stateStyledDot(s.Active, selected, focused, th)

	name := nameStyle.Render(s.Name)
	branchStr := renderBranch(s.Branch, th)

	content := name + branchStr
	contentWidth := lipgloss.Width(content)
	available := max(width-entryFixedWidth, 0)
	if contentWidth > available {
		content = truncateVisible(content, available)
	}

	return fmt.Sprintf("%s %s %s", indicator, icon, content)
}

// entryStyles returns the indicator string and name style for a session entry.
// Active sessions always keep their Primary text color.
// The indicator uses the selection color when selected.
func entryStyles(active, selected, focused bool, th *theme.Theme) (string, lipgloss.Style) {
	if selected {
		indicatorColor := th.Palette.Muted
		if focused {
			indicatorColor = th.Palette.Secondary
		}
		indicator := lipgloss.NewStyle().Foreground(indicatorColor).Render(selectedIndicator)
		nameStyle := sessionNameStyle(active, th)
		if !active {
			nameStyle = lipgloss.NewStyle().Foreground(indicatorColor).Bold(true)
		}
		return indicator, nameStyle
	}
	return unselectedPad, sessionNameStyle(active, th)
}

// stateStyledDot renders the session dot.
// Active sessions always get a filled dot in Primary.
// Inactive selected sessions use the selection color with an outline dot.
// Inactive unselected sessions use Muted with an outline dot.
func stateStyledDot(active, selected, focused bool, th *theme.Theme) string {
	if active {
		return lipgloss.NewStyle().Foreground(th.Palette.Primary).Render(sessionDotFilled)
	}
	if selected {
		color := th.Palette.Muted
		if focused {
			color = th.Palette.Secondary
		}
		return lipgloss.NewStyle().Foreground(color).Render(sessionDotOutline)
	}
	return lipgloss.NewStyle().Foreground(th.Palette.Muted).Render(sessionDotOutline)
}

// truncateVisible truncates a styled string to fit within maxWidth visible
// columns, appending an ellipsis if truncated.
func truncateVisible(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(s)
	ellipsis := "\u2026"
	for i := range runes {
		candidate := string(runes[:i]) + ellipsis
		if lipgloss.Width(candidate) > maxWidth {
			if i == 0 {
				return ellipsis[:min(len(ellipsis), maxWidth)]
			}
			return string(runes[:i-1]) + ellipsis
		}
	}
	return s
}


// sessionNameStyle returns the style for a session name based on whether it is active.
func sessionNameStyle(active bool, th *theme.Theme) lipgloss.Style {
	if active {
		return th.SessionActive
	}
	return th.SessionInactive
}

// renderBranch formats the branch indicator.
func renderBranch(branch string, th *theme.Theme) string {
	if branch == "" {
		return ""
	}
	branchStyle := lipgloss.NewStyle().Foreground(th.Palette.Info)
	return " " + branchStyle.Render(theme.IconBranch+" "+branch)
}


// visibleWindow calculates the start and end indices for a scrolling
// window centered on the selected item.
func visibleWindow(selected, total, height int) (start, end int) {
	if total <= height {
		return 0, total
	}

	// Center the selected item in the window.
	half := height / 2
	start = selected - half
	start = max(start, 0)
	end = start + height
	if end > total {
		end = total
		start = max(end-height, 0)
	}

	return start, end
}
