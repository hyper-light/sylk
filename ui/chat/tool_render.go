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

	var lines []string
	var regions []ToolCallRegion

	for i := range calls {
		start := len(lines)
		if calls[i].Expanded {
			expanded := renderToolCallExpanded(calls[i], width, th)
			lines = append(lines, expanded...)
		} else {
			lines = append(lines, renderToolCallCollapsed(calls[i], width, th))
		}
		regions = append(regions, ToolCallRegion{
			Start:     start,
			End:       len(lines),
			RecordIdx: i,
		})
	}

	return lines, regions
}

// renderToolCallCollapsed renders a single-line collapsed tool call.
// Format: ▸ ⚡ tool_name args_summary                        0.3s
func renderToolCallCollapsed(tc ToolCallRecord, width int, th *theme.Theme) string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	toolStyle := lipgloss.NewStyle().Foreground(th.Palette.Info).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(th.Palette.Info)

	var b strings.Builder

	// Expand/collapse indicator.
	b.WriteString(mutedStyle.Render(theme.IconExpand))
	b.WriteByte(' ')

	// Tool icon.
	b.WriteString(iconStyle.Render(theme.IconToolCall))
	b.WriteByte(' ')

	// Tool name.
	b.WriteString(toolStyle.Render(tc.ToolName))

	// Args summary.
	if tc.ArgsSummary != "" {
		b.WriteByte(' ')
		b.WriteString(mutedStyle.Render(tc.ArgsSummary))
	}

	// Right-aligned: status indicator + duration.
	dur := formatToolCallDuration(tc)
	statusStr := formatToolCallStatus(tc, th)
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
func renderToolCallExpanded(tc ToolCallRecord, width int, th *theme.Theme) []string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	toolStyle := lipgloss.NewStyle().Foreground(th.Palette.Info).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(th.Palette.Info)
	errStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)

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
		header.WriteString(mutedStyle.Render(tc.ArgsSummary))
	}

	dur := formatToolCallDuration(tc)
	statusStr := formatToolCallStatus(tc, th)
	rightPart := statusStr + dur
	leftWidth := lipgloss.Width(header.String())
	rightWidth := lipgloss.Width(rightPart)
	gap := width - leftWidth - rightWidth
	if gap < 1 {
		gap = 1
	}
	header.WriteString(strings.Repeat(" ", gap))
	header.WriteString(rightPart)
	lines = append(lines, header.String())

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
				lines = append(lines, prefix+errStyle.Render(line))
			}
		} else if tc.Output != "" {
			outLines := wrapDetailText(tc.Output, width-6)
			for _, line := range capDetailLines(outLines, maxExpandedOutputLines) {
				lines = append(lines, prefix+mutedStyle.Render(line))
			}
		}
	}

	return lines
}

// formatToolCallDuration formats the tool call's duration for display.
func formatToolCallDuration(tc ToolCallRecord) string {
	if !tc.Completed {
		d := time.Since(tc.StartedAt)
		return formatToolDuration(d)
	}
	return formatToolDuration(tc.Duration)
}

// formatToolCallStatus returns the status indicator (spinner or error icon).
func formatToolCallStatus(tc ToolCallRecord, th *theme.Theme) string {
	if !tc.Completed {
		spinStyle := lipgloss.NewStyle().Foreground(th.Palette.Info)
		return spinStyle.Render(toolCallSpinnerFrames[toolCallSpinnerIndex]) + " "
	}
	if !tc.Success {
		errStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)
		return errStyle.Render(theme.IconToolCallError) + " "
	}
	return ""
}

// formatToolDuration formats a duration as a compact string.
// Examples: "0.3s", "1.2s", "12.4s", "1m03s".
func formatToolDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d.Minutes())
	secs := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", mins, secs)
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
