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
	UpdatedAt time.Time
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
func RenderList(summaries []SessionSummary, selected int, width, height int, th *theme.Theme) string {
	if len(summaries) == 0 || height <= 0 || width <= 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		return emptyStyle.Render("  No sessions")
	}

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
// Selected: blue filled dot, blue bold name, blue arrow.
// Unselected: muted outline dot, muted name.
func renderSessionEntry(s SessionSummary, selected bool, width int, th *theme.Theme) string {
	icon := sessionStyledDot(selected, th)
	indicator := sessionIndicator(selected, th)
	nameStyle := sessionNameStyle(selected, th)

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

// sessionStyledDot renders the session dot.
// Selected: filled dot in Primary (blue). Unselected: outline dot in Muted.
func sessionStyledDot(selected bool, th *theme.Theme) string {
	if selected {
		return th.SessionActive.Render(sessionDotFilled)
	}
	return th.SessionInactive.Render(sessionDotOutline)
}

// sessionIndicator returns the visual indicator for selection state.
// Selected: blue arrow. Unselected: space.
func sessionIndicator(selected bool, th *theme.Theme) string {
	if selected {
		return th.SessionActive.Render(selectedIndicator)
	}
	return unselectedPad
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


// sessionNameStyle returns the style for a session name.
// Selected: SessionActive (blue bold). Unselected: SessionInactive (muted).
func sessionNameStyle(selected bool, th *theme.Theme) lipgloss.Style {
	if selected {
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
