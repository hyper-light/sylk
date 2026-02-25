package session

import (
	"fmt"
	"strings"
	"time"

	coresession "github.com/adalundhe/sylk/core/session"
	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// SessionAnimState carries per-frame animation state for the session dot
// and name ripple. Gradient color and ripple text only activate when Focused.
type SessionAnimState struct {
	DotFrame       int
	Elapsed        time.Duration
	HasActiveAgent bool
	Focused        bool // Session panel is focused (gates gradient color + ripple).
	Gradient       *theme.Gradient // Dot + ripple gradient.
	BorderGradient *theme.Gradient // Tree connector + footer gradient.
}

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


// sessionTreePrefix is the tree connector prepended to each session entry.
const sessionTreePrefix = " \u2502 " // " │ "

// sessionTreePrefixWidth is the visual column width of sessionTreePrefix.
// Derived from: space(1) + │(1) + space(1) = 3.
const sessionTreePrefixWidth = 3

// groupFlowStep is the phase offset between adjacent rows in the border
// gradient shimmer. Each row samples the gradient this much behind the row
// above, creating a downward-flowing prismatic wave.
const groupFlowStep = 250 * time.Millisecond

// sessionFooterFraction is the fraction of panel width for the footer rule.
// Derived from the agent panel's sectionFooterFraction (1/4 for compactness).
const sessionFooterFraction = 4

// selectedIndicator is the left-side marker for the currently selected entry.
const selectedIndicator = theme.IconExpand

// unselectedPad replaces the indicator with a space for non-selected entries.
const unselectedPad = " "

// RenderList renders the session list with tree-style borders and the selected
// entry highlighted. Each entry is prefixed with a " │ " tree connector and
// a " └───" partial footer closes the group — matching the agent panel style.
func RenderList(summaries []SessionSummary, selected int, width, height int, th *theme.Theme, anim SessionAnimState) string {
	if len(summaries) == 0 || height <= 0 || width <= 0 {
		emptyStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		return emptyStyle.Render("  No sessions")
	}

	// Reserve 1 line for the section footer.
	entryHeight := max(height-1, 1)
	visStart, visEnd := visibleWindow(selected, len(summaries), entryHeight)
	entryWidth := max(width-sessionTreePrefixWidth, 0)

	lines := make([]string, 0, visEnd-visStart+1)
	for i := visStart; i < visEnd; i++ {
		borderColor := sampleBorderColor(anim.BorderGradient, anim.Elapsed, i-visStart)
		prefix := renderSessionTreePrefix(borderColor, th)
		line := renderSessionEntry(summaries[i], i == selected, entryWidth, th, anim)
		lines = append(lines, prefix+line)
	}

	// Footer: partial bottom border with the next row's gradient phase.
	footerColor := sampleBorderColor(anim.BorderGradient, anim.Elapsed, visEnd-visStart)
	lines = append(lines, renderSessionFooter(width, footerColor, th))

	return strings.Join(lines, "\n")
}

// entryFixedWidth is the space consumed by the indicator, icon, and separating spaces.
// Derived from: indicator(1) + space(1) + icon(1) + space(1) = 4.
const entryFixedWidth = 4

// renderSessionEntry renders a single session list entry, truncated to width.
// Active sessions with working agents show the origami-bloom animated dot.
// When focused, the active session name also shows ripple text.
func renderSessionEntry(s SessionSummary, selected bool, width int, th *theme.Theme, anim SessionAnimState) string {
	icon := sessionStyledDot(s.Active, selected, th, anim)
	indicator := sessionIndicator(selected, th)

	name := renderSessionName(s, selected, th, anim)
	branchStr := renderBranch(s.Branch, th)

	content := name + branchStr
	contentWidth := lipgloss.Width(content)
	available := max(width-entryFixedWidth, 0)
	if contentWidth > available {
		content = truncateVisible(content, available)
	}

	return fmt.Sprintf("%s %s %s", indicator, icon, content)
}

// renderSessionName renders the session name. Active sessions with working
// agents get the per-character ripple gradient when the session panel is focused.
func renderSessionName(s SessionSummary, selected bool, th *theme.Theme, anim SessionAnimState) string {
	if s.Active && anim.Focused && anim.HasActiveAgent && anim.Gradient != nil {
		return theme.RenderRippleText(s.Name, anim.Elapsed, anim.Gradient, 0)
	}
	return sessionNameStyle(selected, th).Render(s.Name)
}

// sessionStyledDot renders the session dot.
// Active session + working agents: animated origami-bloom glyph. Gradient
// color only when the session panel is focused; otherwise static blue.
func sessionStyledDot(active, selected bool, th *theme.Theme, anim SessionAnimState) string {
	if active && anim.HasActiveAgent {
		icon := theme.DotAnimFrames[anim.DotFrame%theme.DotAnimFrameCount]
		if anim.Focused && anim.Gradient != nil {
			return theme.RenderGradientGlyph(icon, anim.Gradient, anim.Elapsed)
		}
		return th.SessionActive.Render(icon)
	}
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


// sampleBorderColor returns the gradient color for a given row index.
// Returns empty when grad is nil, signaling fallback to static color.
func sampleBorderColor(grad *theme.Gradient, elapsed time.Duration, row int) lipgloss.Color {
	if grad == nil {
		return ""
	}
	phase := elapsed - time.Duration(row)*groupFlowStep
	return grad.Sample(phase)
}

// renderSessionTreePrefix renders the " │ " tree connector glyph.
// When borderColor is non-empty, the glyph uses that color (gradient shimmer);
// otherwise it falls back to Subtle.
func renderSessionTreePrefix(borderColor lipgloss.Color, th *theme.Theme) string {
	color := th.Palette.Subtle
	if borderColor != "" {
		color = borderColor
	}
	return lipgloss.NewStyle().Foreground(color).Render(sessionTreePrefix)
}

// renderSessionFooter renders a rounded bottom-left corner followed by a short
// horizontal rule, spanning ~1/4 of the panel width. When borderColor is
// non-empty, the footer uses that color; otherwise it falls back to Subtle.
func renderSessionFooter(width int, borderColor lipgloss.Color, th *theme.Theme) string {
	ruleLen := max(width/sessionFooterFraction, 2) - 1 // -1 for the corner glyph
	color := th.Palette.Subtle
	if borderColor != "" {
		color = borderColor
	}
	style := lipgloss.NewStyle().Foreground(color)
	return style.Render(" \u2570" + strings.Repeat("\u2500", ruleLen))
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
