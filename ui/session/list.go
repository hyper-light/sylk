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

// stateIcons maps session State to display icons.
var stateIcons = map[coresession.State]string{
	coresession.StateCreated:   theme.IconIdle,
	coresession.StateActive:    theme.IconActing,
	coresession.StatePaused:    theme.IconWaiting,
	coresession.StateSuspended: theme.IconWaiting,
	coresession.StateCompleted: theme.IconSuccess,
	coresession.StateFailed:    theme.IconError,
}

// selectedIndicator is the left-side marker for the currently selected entry.
const selectedIndicator = theme.IconExpand

// unselectedPad replaces the indicator with a space for non-selected entries.
const unselectedPad = " "

// RenderList renders the session list with the selected entry highlighted.
func RenderList(summaries []SessionSummary, selected int, width, height int, th *theme.Theme) string {
	if len(summaries) == 0 || height <= 0 || width <= 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		return emptyStyle.Render("  No sessions")
	}

	// Determine visible window around the selected item.
	visStart, visEnd := visibleWindow(selected, len(summaries), height)

	lines := make([]string, 0, visEnd-visStart)
	for i := visStart; i < visEnd; i++ {
		line := renderSessionEntry(summaries[i], i == selected, width, th)
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

// entryFixedWidth is the space consumed by the indicator, icon, and separating spaces.
// Derived from: indicator(1) + space(1) + icon(1) + space(1) = 4.
const entryFixedWidth = 4

// renderSessionEntry renders a single session list entry, truncated to width.
func renderSessionEntry(s SessionSummary, selected bool, width int, th *theme.Theme) string {
	icon := sessionStateIcon(s.State)
	indicator, nameStyle := entryStyles(s.Active, selected, th)

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
func entryStyles(active, selected bool, th *theme.Theme) (string, lipgloss.Style) {
	if selected {
		indicator := lipgloss.NewStyle().Foreground(th.Palette.Primary).Render(selectedIndicator)
		style := lipgloss.NewStyle().Foreground(th.Palette.Primary).Bold(true)
		return indicator, style
	}
	return unselectedPad, sessionNameStyle(active, th)
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

// sessionStateIcon returns the icon for a session state.
func sessionStateIcon(state coresession.State) string {
	if icon, ok := stateIcons[state]; ok {
		return icon
	}
	return theme.IconIdle
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
