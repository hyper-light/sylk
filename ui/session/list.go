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

// listEntryPadding accounts for the icon, spaces, and branch indicator.
// Derived from: icon(1) + space(1) + branch_icon(1) + space(1) = 4.
const listEntryPadding = 4

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

// renderSessionEntry renders a single session list entry.
func renderSessionEntry(s SessionSummary, selected bool, width int, th *theme.Theme) string {
	icon := sessionStateIcon(s.State)
	nameStyle := sessionNameStyle(s.Active, th)
	name := nameStyle.Render(s.Name)

	branchStr := renderBranch(s.Branch, th)
	ageStr := renderAge(s.CreatedAt, th)

	// Calculate summary: [icon space] [name] [ branch] [ age]
	nameWidth := lipgloss.Width(name)
	branchWidth := lipgloss.Width(branchStr)
	ageWidth := lipgloss.Width(ageStr)
	fixedWidth := listEntryPadding + nameWidth + branchWidth + ageWidth
	spacerWidth := max(width-fixedWidth, 0)
	spacer := strings.Repeat(" ", spacerWidth)

	entry := fmt.Sprintf("  %s %s%s%s%s", icon, name, spacer, branchStr, ageStr)

	if selected {
		return renderSelected(entry, width, th)
	}
	return entry
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

// renderAge formats the session age as a human-readable duration.
func renderAge(createdAt time.Time, th *theme.Theme) string {
	ageStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	return " " + ageStyle.Render(formatAge(createdAt))
}

// renderSelected highlights the entry row for the selected session.
func renderSelected(entry string, width int, th *theme.Theme) string {
	highlightStyle := lipgloss.NewStyle().
		Background(th.Palette.Subtle).
		Width(width)
	return highlightStyle.Render(entry)
}

// formatAge returns a concise human-readable age string.
func formatAge(t time.Time) string {
	d := time.Since(t)

	// Time thresholds derived from common duration display conventions.
	const (
		minuteThreshold = time.Minute
		hourThreshold   = time.Hour
		dayThreshold    = 24 * time.Hour
	)

	if d < minuteThreshold {
		return "now"
	}
	if d < hourThreshold {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < dayThreshold {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
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
