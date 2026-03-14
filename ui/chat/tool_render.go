package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// maxExpandedOutputLines limits how many lines of tool output are shown expanded.
const maxExpandedOutputLines = 8

// toolCallSpinnerFrames are braille dot frames reused from the thinking spinner.
var toolCallSpinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// toolCallSpinnerIndex tracks the global spinner frame for tool call animation.
// Advanced by each renderToolCalls invocation that has active calls.
var toolCallSpinnerIndex int

// renderToolCalls renders all tool call records as inline blocks.
// Returns the rendered lines and tool call region metadata for click handling.
// Active (incomplete) tool calls render with a holographic gradient shimmer
// distinct from the thinking animation.
func renderToolCalls(calls []ToolCallRecord, width int, th *theme.Theme) ([]string, []ToolCallRegion) {
	if len(calls) == 0 || width <= 0 {
		return nil, nil
	}

	hasActive := false
	for i := range calls {
		if !calls[i].Completed {
			hasActive = true
			break
		}
	}
	if hasActive {
		toolCallSpinnerIndex = (toolCallSpinnerIndex + 1) % len(toolCallSpinnerFrames)
	}

	// Build the gradient once per render pass (only when active calls exist).
	var grad *theme.Gradient
	if hasActive {
		grad = th.Palette.ToolCallGradient()
	}

	var lines []string
	var regions []ToolCallRegion

	for i := range calls {
		start := len(lines)
		if calls[i].Expanded {
			expanded := renderToolCallExpanded(calls[i], width, th, grad)
			lines = append(lines, expanded...)
		} else {
			lines = append(lines, wrapRenderedToolLine(renderToolCallCollapsed(calls[i], width, th, grad), width)...)
		}
		regions = append(regions, ToolCallRegion{
			Start:     start,
			End:       len(lines),
			RecordIdx: i,
		})
	}

	// Trailing spacer line below the tool call block.
	lines = append(lines, "")

	return lines, regions
}

// renderToolCallCollapsed renders a single-line collapsed tool call.
// Format: ▸ ⏻ tool_name args_summary                        0.3s
// Active calls use gradient-sampled colors; completed calls use static styles.
func renderToolCallCollapsed(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient) string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)

	// Select colors: gradient shimmer for active, static for completed.
	activeColor := activeToolColor(tc, th, grad)
	toolStyle := lipgloss.NewStyle().Foreground(activeColor).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(activeColor)

	var b strings.Builder

	// Expand/collapse indicator.
	b.WriteString(mutedStyle.Render(theme.IconExpand))
	b.WriteByte(' ')

	// Tool icon.
	b.WriteString(iconStyle.Render(theme.IconToolCall))
	b.WriteByte(' ')

	// Tool name.
	b.WriteString(toolStyle.Render(tc.ToolName))

	// Args summary — gradient for active, muted for completed.
	if tc.ArgsSummary != "" {
		b.WriteByte(' ')
		if !tc.Completed && grad != nil {
			argsStyle := lipgloss.NewStyle().Foreground(activeColor)
			b.WriteString(argsStyle.Render(tc.ArgsSummary))
		} else {
			b.WriteString(mutedStyle.Render(tc.ArgsSummary))
		}
	}

	// Right-aligned: status indicator + duration.
	dur := formatToolCallDuration(tc)
	statusStr := formatToolCallStatus(tc, th, grad)
	rightPart := statusStr + dur

	leftWidth := lipgloss.Width(b.String())
	rightWidth := lipgloss.Width(rightPart)
	gap := width - leftWidth - rightWidth
	if gap < 1 {
		gap = 1
	}

	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(rightPart)

	return b.String()
}

// renderToolCallExpanded renders the expanded detail block.
// Active calls use gradient-sampled colors for the header; completed calls use static styles.
func renderToolCallExpanded(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient) []string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	errStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)

	activeColor := activeToolColor(tc, th, grad)
	toolStyle := lipgloss.NewStyle().Foreground(activeColor).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(activeColor)

	lines := make([]string, 0, 8)

	// Header line (same as collapsed but with collapse indicator).
	var header strings.Builder
	header.WriteString(mutedStyle.Render(theme.IconCollapse))
	header.WriteByte(' ')
	header.WriteString(iconStyle.Render(theme.IconToolCall))
	header.WriteByte(' ')
	header.WriteString(toolStyle.Render(tc.ToolName))
	if tc.ArgsSummary != "" {
		header.WriteByte(' ')
		if !tc.Completed && grad != nil {
			argsStyle := lipgloss.NewStyle().Foreground(activeColor)
			header.WriteString(argsStyle.Render(tc.ArgsSummary))
		} else {
			header.WriteString(mutedStyle.Render(tc.ArgsSummary))
		}
	}

	dur := formatToolCallDuration(tc)
	statusStr := formatToolCallStatus(tc, th, grad)
	rightPart := statusStr + dur
	leftWidth := lipgloss.Width(header.String())
	rightWidth := lipgloss.Width(rightPart)
	gap := width - leftWidth - rightWidth
	if gap < 1 {
		gap = 1
	}
	header.WriteString(strings.Repeat(" ", gap))
	header.WriteString(rightPart)
	lines = append(lines, wrapRenderedToolLine(header.String(), width)...)

	// Detail lines with prefix.
	prefix := mutedStyle.Render("│") + "   "
	separator := mutedStyle.Render("│") + "   " + mutedStyle.Render("─────")

	// Format args as key-value lines.
	argLines := formatToolArgs(tc.FullArgs, width-6)
	for _, line := range argLines {
		lines = append(lines, prefix+mutedStyle.Render(line))
	}

	// Separator between args and output/error.
	if tc.Completed && (tc.Output != "" || tc.ErrorMsg != "") {
		lines = append(lines, separator)
	}

	// Output or error.
	if tc.Completed {
		if tc.ErrorMsg != "" {
			errLines := wrapDetailText("Error: "+tc.ErrorMsg, width-6)
			for _, line := range capDetailLines(errLines, maxExpandedOutputLines) {
				lines = append(lines, wrapRenderedToolLine(prefix+errStyle.Render(line), width)...)
			}
		} else if tc.Output != "" {
			outLines := wrapDetailText(tc.Output, width-6)
			for _, line := range capDetailLines(outLines, maxExpandedOutputLines) {
				lines = append(lines, wrapRenderedToolLine(prefix+mutedStyle.Render(line), width)...)
			}
		}
	}

	return lines
}

func wrapRenderedToolLine(line string, width int) []string {
	if width <= 0 {
		return nil
	}
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	return wrapStyledCode(line, width)
}

// formatToolCallDuration formats the tool call's duration for display.
func formatToolCallDuration(tc ToolCallRecord) string {
	if !tc.Completed {
		d := time.Since(tc.StartedAt)
		return formatToolDuration(d)
	}
	return formatToolDuration(tc.Duration)
}

// blockedSubstrings are error message fragments that indicate a tool call was
// blocked for security or OS-level reasons rather than a runtime failure.
var blockedSubstrings = [...]string{
	"permission denied",
	"access denied",
	"forbidden",
	"not permitted",
	"blocked",
	"quarantine",
	"security",
}

// isBlockedError reports whether the error message indicates a security or
// OS-level block rather than a runtime failure.
func isBlockedError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	for _, sub := range blockedSubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// activeToolColor returns the gradient-sampled color for an active tool call,
// or the static Info color for completed calls.
func activeToolColor(tc ToolCallRecord, th *theme.Theme, grad *theme.Gradient) lipgloss.Color {
	if !tc.Completed && grad != nil {
		return grad.Sample(time.Since(tc.StartedAt))
	}
	return th.Palette.Info
}

// formatToolCallStatus returns the status indicator (spinner, error, or blocked icon).
// Active calls color the spinner with the gradient; completed calls use static styles.
func formatToolCallStatus(tc ToolCallRecord, th *theme.Theme, grad *theme.Gradient) string {
	if !tc.Completed {
		color := activeToolColor(tc, th, grad)
		spinStyle := lipgloss.NewStyle().Foreground(color)
		return spinStyle.Render(toolCallSpinnerFrames[toolCallSpinnerIndex]) + " "
	}
	if !tc.Success {
		errStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)
		icon := theme.IconToolCallError
		if isBlockedError(tc.ErrorMsg) {
			icon = theme.IconToolCallBlocked
		}
		return errStyle.Render(icon) + " "
	}
	return ""
}

// formatToolDuration formats a duration as a compact string.
// Examples: "3ms", "120ms", "1.2s", "12.4s", "1m03s".
func formatToolDuration(d time.Duration) string {
	switch {
	case d < 100*time.Millisecond:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%02ds", mins, secs)
	}
}

// formatToolArgs formats JSON args as indented key-value lines for expanded view.
func formatToolArgs(fullArgs string, maxWidth int) []string {
	fullArgs = strings.TrimSpace(fullArgs)
	if fullArgs == "" || fullArgs == "{}" {
		return nil
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(fullArgs), &parsed); err != nil {
		// Not valid JSON — show raw.
		return wrapDetailText(fullArgs, maxWidth)
	}

	lines := make([]string, 0, len(parsed))
	for key, val := range parsed {
		valStr := formatArgValueCompact(val)
		line := key + ": " + valStr
		if len([]rune(line)) > maxWidth && maxWidth > 4 {
			line = string([]rune(line)[:maxWidth-1]) + "…"
		}
		lines = append(lines, line)
	}
	return lines
}

// formatArgValueCompact converts a JSON value to a compact display string.
func formatArgValueCompact(val any) string {
	switch v := val.(type) {
	case string:
		return `"` + v + `"`
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// wrapDetailText splits text into lines not exceeding maxWidth.
func wrapDetailText(text string, maxWidth int) []string {
	if maxWidth <= 0 {
		return nil
	}
	// Split on newlines first.
	raw := strings.Split(text, "\n")
	var result []string
	for _, line := range raw {
		runes := []rune(line)
		if len(runes) <= maxWidth {
			result = append(result, line)
			continue
		}
		for len(runes) > maxWidth {
			result = append(result, string(runes[:maxWidth]))
			runes = runes[maxWidth:]
		}
		if len(runes) > 0 {
			result = append(result, string(runes))
		}
	}
	return result
}

// capDetailLines truncates a slice of lines, appending "..." to the last kept line.
func capDetailLines(lines []string, maxLines int) []string {
	if len(lines) <= maxLines {
		return lines
	}
	capped := make([]string, maxLines)
	copy(capped, lines[:maxLines])
	capped[maxLines-1] += "..."
	return capped
}
