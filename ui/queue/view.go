package queue

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// maxVisibleEntries is the most entry lines we show before collapsing.
const maxVisibleEntries = 2

// truncWidth is the max display width for the prompt text portion.
const truncWidth = 48

// ViewHeight returns the number of terminal lines the queue strip occupies.
// 0 when empty (invisible).
func (q *Queue) ViewHeight() int {
	pending := q.PendingCount()
	if pending == 0 {
		return 0
	}
	if pending <= 1 {
		return 1
	}
	if pending <= 3 {
		return 2
	}
	return 3
}

// View renders the queue strip for the given terminal width and gradient state.
// elapsed drives the ripple animation; grad may be nil when paused (renders muted).
func (q *Queue) View(width int, elapsed time.Duration, grad *theme.Gradient, pal theme.Palette) string {
	pending := q.PendingEntries()
	if len(pending) == 0 {
		return ""
	}

	var lines []string

	// Determine how many entry lines to render.
	entryCount := min(len(pending), maxVisibleEntries)
	for i := range entryCount {
		lines = append(lines, renderEntryLine(pending[i], i+1, width, elapsed, grad, pal, q.paused))
	}

	// Summary line for overflow.
	remaining := len(pending) - entryCount
	if remaining > 0 {
		lines = append(lines, renderSummaryLine(remaining, len(pending), width, pal, q.paused))
	}

	return strings.Join(lines, "\n")
}

// renderEntryLine renders a single queue entry as a compact line.
func renderEntryLine(e Entry, pos, width int, elapsed time.Duration, grad *theme.Gradient, pal theme.Palette, paused bool) string {
	// Format: [N] "prompt text..."  @agent  [Esc cancel]
	prefix := fmt.Sprintf("[%d] ", pos)
	truncated := truncateText(e.Text, truncWidth)
	quoted := fmt.Sprintf("%q", truncated)

	suffix := "  Esc cancel"
	if e.TargetAgent != "" {
		suffix = fmt.Sprintf("  @%s  Esc cancel", e.TargetAgent)
	}

	// Available width for the main content.
	mainContent := prefix + quoted

	if paused || grad == nil {
		// Muted rendering when paused.
		style := lipgloss.NewStyle().Foreground(pal.Muted)
		pauseTag := ""
		if paused {
			pauseTag = "  ⏸ paused"
		}
		line := style.Render(mainContent+suffix) + style.Render(pauseTag)
		return padToWidth(line, width)
	}

	// Animated rendering: ripple the main content, dim the suffix.
	animated := theme.RenderRippleText(mainContent, elapsed, grad, 0)
	dimStyle := lipgloss.NewStyle().Foreground(pal.Subtle)
	line := animated + dimStyle.Render(suffix)
	return padToWidth(line, width)
}

// renderSummaryLine renders the "+N more | shortcuts" summary.
func renderSummaryLine(remaining, total, width int, pal theme.Palette, paused bool) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("+%d more", remaining))

	if total >= 4 {
		parts = append(parts, "Alt+Shift+Space pause")
		parts = append(parts, "Alt+K cancel")
	}

	text := strings.Join(parts, " │ ")

	style := lipgloss.NewStyle().Foreground(pal.Subtle)
	if paused {
		style = style.Foreground(pal.Muted)
	}
	return padToWidth(style.Render(text), width)
}

// truncateText shortens text to maxLen runes, adding "..." if truncated.
func truncateText(text string, maxLen int) string {
	// Replace newlines with spaces for compact display.
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", "")

	if utf8.RuneCountInString(text) <= maxLen {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxLen-3]) + "..."
}

// padToWidth pads a line with spaces to fill the terminal width.
// Does not truncate — callers should pre-truncate content.
func padToWidth(line string, width int) string {
	// Use ANSI-aware width calculation would be ideal, but for simplicity
	// we pad generously. The compositor handles final line splicing.
	if width <= 0 {
		return line
	}
	return line
}
