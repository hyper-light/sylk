package chat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// maxExpandedOutputLines limits how many lines of tool output are shown expanded.
const maxExpandedOutputLines = 8

// maxInterAgentChildLines bounds how many nested tool-call rows each child
// agent contributes before older rows collapse into an overflow line.
const maxInterAgentChildLines = 4

// toolCallSpinnerFrames are braille dot frames reused from the thinking spinner.
var toolCallSpinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// interAgentSpinnerFrames are reserved for in-flight consultation/challenge
// rows so they do not visually compete with the primary thinking spinner.
var interAgentSpinnerFrames = [...]string{"◜", "◠", "◝", "◞", "◡", "◟"}

// toolCallSpinnerIndex tracks the global spinner frame for tool call animation.
// Advanced by each renderToolCalls invocation that has active calls.
var toolCallSpinnerIndex int

// interAgentSpinnerIndex tracks the ring-spinner frame for inter-agent rows.
var interAgentSpinnerIndex int

// renderToolCalls renders all tool call records as inline blocks.
// Returns the rendered lines and tool call region metadata for click handling.
// Active (incomplete) tool calls render with a holographic gradient shimmer
// distinct from the thinking animation.
func renderToolCalls(calls []ToolCallRecord, width int, th *theme.Theme) ([]string, []ToolCallRegion) {
	if len(calls) == 0 || width <= 0 {
		return nil, nil
	}

	hasActive := false
	hasInterAgentActive := false
	for i := range calls {
		if toolCallHasActiveVisual(calls[i]) {
			hasActive = true
		}
		if calls[i].InterAgent != nil && calls[i].InterAgent.Status == InterAgentToolPending {
			hasInterAgentActive = true
		}
	}
	if hasActive {
		toolCallSpinnerIndex = (toolCallSpinnerIndex + 1) % len(toolCallSpinnerFrames)
	}
	if hasInterAgentActive {
		interAgentSpinnerIndex = (interAgentSpinnerIndex + 1) % len(interAgentSpinnerFrames)
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
		if calls[i].InterAgent != nil {
			rendered, subregions := renderInterAgentToolCall(calls, i, width, th, grad)
			for j := range subregions {
				subregions[j].Start += start
				subregions[j].End += start
			}
			lines = append(lines, rendered...)
			regions = append(regions, ToolCallRegion{
				Start:      start,
				End:        len(lines),
				RecordIdx:  i,
				Subregions: subregions,
			})
		} else {
			if calls[i].Expanded {
				lines = append(lines, renderToolCallExpanded(calls[i], width, th, grad)...)
			} else {
				lines = append(lines, renderToolCallCollapsed(calls[i], width, th, grad))
			}
			regions = append(regions, ToolCallRegion{
				Start:     start,
				End:       len(lines),
				RecordIdx: i,
			})
		}
	}

	// Trailing spacer line below the tool call block.
	lines = append(lines, "")

	return lines, regions
}

type interAgentRenderedChildRow struct {
	lines            []string
	kind             ToolCallSubregionKind
	childIndex       int
	childToolCallIdx int
}

type interAgentRenderedChildSection struct {
	childIndex int
	header     string
	rows       []interAgentRenderedChildRow
}

func renderInterAgentToolCall(calls []ToolCallRecord, idx, width int, th *theme.Theme, grad *theme.Gradient) ([]string, []ToolCallSubregion) {
	if idx < 0 || idx >= len(calls) || calls[idx].InterAgent == nil || width <= 0 {
		return nil, nil
	}
	row := calls[idx].InterAgent
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	headerConnectorWidth := lipgloss.Width("├─ ")
	childHeaderWidth := max(width-headerConnectorWidth, 0)
	childRowWidth := max(width-lipgloss.Width("│  ")-headerConnectorWidth, 0)
	childSections := renderInterAgentChildSections(row, childHeaderWidth, childRowWidth, th, grad)
	if len(childSections) == 0 {
		lines := []string{renderInterAgentHeadline(calls, idx, row, width, th)}
		if calls[idx].Expanded {
			lines = append(lines, renderInterAgentExpandedSummary(calls, idx, row, width, th)...)
		}
		return lines, nil
	}
	lines := make([]string, 0, len(childSections)*2)
	var subregions []ToolCallSubregion
	hasLater := hasLaterInterAgentCall(calls, idx)
	for childIdx, child := range childSections {
		headerConnector := "├─ "
		sectionContinues := childIdx < len(childSections)-1 || hasLater
		if !sectionContinues {
			headerConnector = "└─ "
		}
		if child.header != "" {
			lines = append(lines, truncateStyledWithDots(mutedStyle.Render(headerConnector)+truncateStyledWithDots(child.header, childHeaderWidth), width))
		}
		if len(child.rows) == 0 {
			continue
		}
		nestedStem := mutedStyle.Render("│  ")
		if !sectionContinues {
			nestedStem = mutedStyle.Render("   ")
		}
		for rowIdx, childRow := range child.rows {
			rowConnector := "├─ "
			if rowIdx == len(child.rows)-1 {
				rowConnector = "└─ "
			}
			rowStart := len(lines)
			firstPrefix := nestedStem + mutedStyle.Render(rowConnector)
			contPrefix := nestedStem + mutedStyle.Render("│  ")
			if rowIdx == len(child.rows)-1 {
				contPrefix = nestedStem + mutedStyle.Render("   ")
			}
			for lineIdx, line := range childRow.lines {
				prefix := firstPrefix
				if lineIdx > 0 {
					prefix = contPrefix
				}
				lines = append(lines, truncateStyledWithDots(prefix+truncateStyledWithDots(line, childRowWidth), width))
			}
			if childRow.kind != "" && len(childRow.lines) > 0 {
				subregions = append(subregions, ToolCallSubregion{
					Start:            rowStart,
					End:              len(lines),
					Kind:             childRow.kind,
					ChildIndex:       child.childIndex,
					ChildToolCallIdx: childRow.childToolCallIdx,
				})
			}
		}
	}
	return lines, subregions
}

func renderInterAgentHeadline(calls []ToolCallRecord, idx int, row *InterAgentTool, width int, th *theme.Theme) string {
	if row == nil || width <= 0 {
		return ""
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	summaryStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)

	treePrefix := "└─"
	if hasLaterInterAgentCall(calls, idx) {
		treePrefix = "├─"
	}

	statusIcon, statusStyle := interAgentStatusGlyph(row.Status, th)
	prefix := mutedStyle.Render(treePrefix+" ") + statusStyle.Render(statusIcon) + " "
	labelBlock := renderInterAgentLabels(row.AgentTypes, th, mutedStyle)
	linePrefix := prefix + labelBlock

	summary := normalizeInlineText(row.Summary)
	if summary != "" {
		linePrefix += mutedStyle.Render(" - ") + summaryStyle.Render(truncatePlainWithDots(summary, max(width-lipgloss.Width(linePrefix+" - "), 0)))
	}

	return truncateStyledWithDots(linePrefix, width)
}

func renderInterAgentExpandedSummary(calls []ToolCallRecord, idx int, row *InterAgentTool, width int, th *theme.Theme) []string {
	if row == nil || width <= 0 {
		return nil
	}
	summary := normalizeInlineText(row.Summary)
	if summary == "" {
		return nil
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	detailStyle := lipgloss.NewStyle().Foreground(th.Palette.Subtle)
	stem := "   "
	if hasLaterInterAgentCall(calls, idx) {
		stem = mutedStyle.Render("│  ")
	}
	prefix := stem + mutedStyle.Render("│  ")
	contentWidth := max(width-lipgloss.Width(prefix), 0)
	if contentWidth <= 0 {
		return nil
	}
	lines := wrapLine(summary, contentWidth, detailStyle)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, truncateStyledWithDots(prefix+line, width))
	}
	return out
}

func renderInterAgentChildSections(row *InterAgentTool, headerWidth, rowWidth int, th *theme.Theme, grad *theme.Gradient) []interAgentRenderedChildSection {
	if row == nil || (len(row.Children) == 0 && len(row.AgentTypes) == 0) || (headerWidth <= 0 && rowWidth <= 0) {
		return nil
	}
	sections := make([]interAgentRenderedChildSection, 0, max(len(row.Children), len(row.AgentTypes)))
	rootSummary := normalizeInlineText(row.Summary)
	usedChildren := make([]bool, len(row.Children))
	for _, target := range normalizeAgentTypes(row.AgentTypes) {
		childIndex, child := findInterAgentChildByAgentType(row.Children, usedChildren, target)
		if child == nil {
			placeholder := synthesizeInterAgentChildActivity(row, target)
			child = &placeholder
			childIndex = -1
		}
		header := renderInterAgentChildHeader(*child, headerWidth, th, rootSummary)
		rows := make([]interAgentRenderedChildRow, 0, len(child.ToolCalls))
		for j := range child.ToolCalls {
			if lines := renderInterAgentChildToolCall(child.ToolCalls[j], rowWidth, th, grad); len(lines) > 0 {
				rows = append(rows, interAgentRenderedChildRow{
					lines:            lines,
					kind:             ToolCallSubregionChildTool,
					childIndex:       childIndex,
					childToolCallIdx: j,
				})
			}
		}
		rows = capInterAgentChildRows(rows, rowWidth, th, child.ToolCallsExpanded, childIndex)
		if header == "" && len(rows) == 0 {
			continue
		}
		sections = append(sections, interAgentRenderedChildSection{childIndex: childIndex, header: header, rows: rows})
	}
	for i := range row.Children {
		if usedChildren[i] {
			continue
		}
		child := row.Children[i]
		header := renderInterAgentChildHeader(child, headerWidth, th, rootSummary)
		rows := make([]interAgentRenderedChildRow, 0, len(child.ToolCalls))
		for j := range child.ToolCalls {
			if lines := renderInterAgentChildToolCall(child.ToolCalls[j], rowWidth, th, grad); len(lines) > 0 {
				rows = append(rows, interAgentRenderedChildRow{
					lines:            lines,
					kind:             ToolCallSubregionChildTool,
					childIndex:       i,
					childToolCallIdx: j,
				})
			}
		}
		rows = capInterAgentChildRows(rows, rowWidth, th, child.ToolCallsExpanded, i)
		if header == "" && len(rows) == 0 {
			continue
		}
		sections = append(sections, interAgentRenderedChildSection{childIndex: i, header: header, rows: rows})
	}
	return sections
}

func findInterAgentChildByAgentType(children []InterAgentChildActivity, used []bool, agentType string) (int, *InterAgentChildActivity) {
	normalizedAgentType := strings.TrimSpace(agentType)
	if normalizedAgentType == "" {
		return -1, nil
	}
	for i := range children {
		if i < len(used) && used[i] {
			continue
		}
		if nestedActivityAgentType(&children[i]) != normalizedAgentType {
			continue
		}
		if i < len(used) {
			used[i] = true
		}
		return i, &children[i]
	}
	return -1, nil
}

func synthesizeInterAgentChildActivity(row *InterAgentTool, agentType string) InterAgentChildActivity {
	child := InterAgentChildActivity{
		AgentType: agentType,
	}
	switch row.Status {
	case InterAgentToolFailed:
		child.Completed = true
		child.Failed = true
		child.ResultSummary = normalizeInlineText(row.Summary)
	case InterAgentToolDone:
		child.Completed = true
		child.ResultSummary = normalizeInlineText(row.Summary)
	default:
		child.ThinkingStatus = normalizeInlineText(row.Summary)
	}
	return child
}

func renderInterAgentChildHeader(child InterAgentChildActivity, width int, th *theme.Theme, rootSummary string) string {
	if width <= 0 {
		return ""
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	summaryStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	statusIcon, statusStyle := interAgentChildStatusGlyph(child, th)
	label := renderInterAgentLabels([]string{nestedActivityAgentType(&child)}, th, mutedStyle)
	left := statusStyle.Render(statusIcon) + " " + label
	if summary := interAgentChildSummary(child, rootSummary); summary != "" {
		left += mutedStyle.Render(" - ") + summaryStyle.Render(truncatePlainWithDots(summary, max(width-lipgloss.Width(left+" - "), 0)))
	}
	return truncateStyledWithDots(left, width)
}

func interAgentChildSummary(child InterAgentChildActivity, rootSummary string) string {
	if !child.Completed {
		summary := normalizeInlineText(strings.TrimSpace(strings.Join([]string{
			normalizeInlineText(child.ThinkingStatus),
			normalizeThinkingLine(child.ThinkingText),
		}, " ")))
		if summary == "Thinking..." {
			return ""
		}
		return summary
	}
	summary := normalizeInlineText(child.ResultSummary)
	if summary == "" || summary == rootSummary {
		return ""
	}
	return summary
}

func interAgentChildStatusGlyph(child InterAgentChildActivity, th *theme.Theme) (string, lipgloss.Style) {
	if child.Failed {
		return "✗", lipgloss.NewStyle().Foreground(th.Palette.Error).Bold(true)
	}
	if child.Completed {
		return "✓", lipgloss.NewStyle().Foreground(th.Palette.Success).Bold(true)
	}
	color := th.Palette.Info
	if strings.TrimSpace(child.ThinkingColor) != "" {
		color = lipgloss.Color(child.ThinkingColor)
	}
	return interAgentSpinnerFrames[interAgentSpinnerIndex], lipgloss.NewStyle().Foreground(color)
}

func renderInterAgentChildToolCall(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient) []string {
	if tc.InterAgent != nil {
		if lines, _ := renderInterAgentToolCall([]ToolCallRecord{tc}, 0, width, th, grad); len(lines) > 0 {
			return lines
		}
		if tc.Expanded {
			return renderNestedInterAgentToolCallExpanded(tc, width, th)
		}
		if line := renderNestedInterAgentToolCall(tc, width, th); line != "" {
			return []string{line}
		}
		return nil
	}
	if tc.Expanded {
		return renderNestedChildToolCallExpanded(tc, width, th, grad)
	}
	if line := renderToolCallCollapsed(tc, width, th, grad); line != "" {
		return []string{line}
	}
	return nil
}

func renderNestedChildToolCallExpanded(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient) []string {
	if width <= 0 {
		return nil
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	errStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)
	toolName := normalizeToolInlineText(tc.ToolName)
	argsSummary := normalizeToolInlineText(tc.ArgsSummary)

	activeColor := activeToolColor(tc, th, grad)
	toolStyle := lipgloss.NewStyle().Foreground(activeColor).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(activeColor)

	var header strings.Builder
	header.WriteString(mutedStyle.Render(theme.IconCollapse))
	header.WriteByte(' ')
	header.WriteString(iconStyle.Render(theme.IconToolCall))
	header.WriteByte(' ')
	header.WriteString(toolStyle.Render(toolName))
	if argsSummary != "" {
		header.WriteByte(' ')
		if !tc.Completed && grad != nil {
			header.WriteString(lipgloss.NewStyle().Foreground(activeColor).Render(argsSummary))
		} else {
			header.WriteString(mutedStyle.Render(argsSummary))
		}
	}

	rightPart := formatToolCallStatus(tc, th, grad) + formatToolCallDuration(tc)
	lines := []string{composeSingleLineToolCall(header.String(), rightPart, width)}

	prefix := mutedStyle.Render("│") + "   "
	contentWidth := max(width-lipgloss.Width(prefix), 0)
	if contentWidth <= 0 {
		return lines
	}

	if argsLine := summarizeToolDetailInline(tc.ArgsSummary, tc.FullArgs, contentWidth-len("args - ")); argsLine != "" {
		lines = append(lines, truncateStyledWithDots(prefix+mutedStyle.Render("args - "+argsLine), width))
	}
	if tc.Completed {
		switch {
		case strings.TrimSpace(tc.ErrorMsg) != "":
			if detail := summarizeToolDetailInline(tc.ErrorMsg, tc.ErrorMsg, contentWidth-len("error - ")); detail != "" {
				lines = append(lines, truncateStyledWithDots(prefix+errStyle.Render("error - "+detail), width))
			}
		case strings.TrimSpace(tc.Output) != "":
			if detail := summarizeToolDetailInline(tc.Output, tc.Output, contentWidth-len("output - ")); detail != "" {
				lines = append(lines, truncateStyledWithDots(prefix+mutedStyle.Render("output - "+detail), width))
			}
		}
	}

	return lines
}

func renderNestedInterAgentToolCall(tc ToolCallRecord, width int, th *theme.Theme) string {
	if tc.InterAgent == nil || width <= 0 {
		return ""
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	summaryStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	statusIcon, statusStyle := interAgentStatusGlyph(tc.InterAgent.Status, th)
	left := statusStyle.Render(statusIcon) + " " + renderInterAgentLabels(tc.InterAgent.AgentTypes, th, mutedStyle)
	if summary := normalizeInlineText(tc.InterAgent.Summary); summary != "" {
		left += mutedStyle.Render(" - ") + summaryStyle.Render(summary)
	}
	return truncateStyledWithDots(left, width)
}

func renderNestedInterAgentToolCallExpanded(tc ToolCallRecord, width int, th *theme.Theme) []string {
	if tc.InterAgent == nil || width <= 0 {
		return nil
	}
	headline := renderNestedInterAgentToolCall(tc, width, th)
	summary := normalizeInlineText(tc.InterAgent.Summary)
	if summary == "" {
		return []string{headline}
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	detailStyle := lipgloss.NewStyle().Foreground(th.Palette.Subtle)
	prefix := mutedStyle.Render("│") + "   "
	contentWidth := max(width-lipgloss.Width(prefix), 0)
	lines := []string{headline}
	for _, line := range wrapLine(summary, contentWidth, detailStyle) {
		lines = append(lines, truncateStyledWithDots(prefix+line, width))
	}
	return lines
}

func capInterAgentChildRows(rows []interAgentRenderedChildRow, width int, th *theme.Theme, expanded bool, childIndex int) []interAgentRenderedChildRow {
	if expanded || len(rows) <= maxInterAgentChildLines {
		return rows
	}
	overflow := len(rows) - (maxInterAgentChildLines - 1)
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	out := make([]interAgentRenderedChildRow, 0, maxInterAgentChildLines)
	out = append(out, interAgentRenderedChildRow{
		lines:      []string{truncateStyledWithDots(mutedStyle.Render(fmt.Sprintf("… %d earlier event%s", overflow, pluralSuffix(overflow))), width)},
		kind:       ToolCallSubregionOverflow,
		childIndex: childIndex,
	})
	out = append(out, rows[len(rows)-(maxInterAgentChildLines-1):]...)
	return out
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func hasLaterInterAgentCall(calls []ToolCallRecord, idx int) bool {
	for i := idx + 1; i < len(calls); i++ {
		if calls[i].InterAgent != nil {
			return true
		}
	}
	return false
}

func interAgentStatusGlyph(status InterAgentToolStatus, th *theme.Theme) (string, lipgloss.Style) {
	switch status {
	case InterAgentToolPending:
		return interAgentSpinnerFrames[interAgentSpinnerIndex], lipgloss.NewStyle().Foreground(th.Palette.Info)
	case InterAgentToolFailed:
		return "✗", lipgloss.NewStyle().Foreground(th.Palette.Error).Bold(true)
	default:
		return "✓", lipgloss.NewStyle().Foreground(th.Palette.Success).Bold(true)
	}
}

func renderInterAgentLabels(agentTypes []string, th *theme.Theme, mutedStyle lipgloss.Style) string {
	labels := normalizeAgentTypes(agentTypes)
	if len(labels) == 0 {
		return mutedStyle.Render("agent")
	}
	var b strings.Builder
	for i, label := range labels {
		if i > 0 {
			b.WriteString(mutedStyle.Render(", "))
		}
		colorKey := strings.TrimSuffix(label, "-pipeline")
		b.WriteString(th.AgentBadge(colorKey).Render(label))
	}
	return b.String()
}

func truncatePlainWithDots(text string, width int) string {
	return truncatePlainDisplayWidth(text, width, "...")
}

func truncateStyledWithDots(line string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(line) <= width {
		return line
	}
	if width <= 3 {
		return truncateVisible(line, width)
	}
	return truncateVisible(line, width-3) + "..."
}

func composeSingleLineToolCall(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	if right == "" {
		return truncateStyledWithDots(left, width)
	}

	rightWidth := lipgloss.Width(right)
	if rightWidth >= width {
		return truncateStyledWithDots(right, width)
	}

	maxLeftWidth := width - rightWidth - 1
	if maxLeftWidth <= 0 {
		return truncateStyledWithDots(right, width)
	}

	left = truncateStyledWithDots(left, maxLeftWidth)
	gap := width - lipgloss.Width(left) - rightWidth
	if gap < 1 {
		gap = 1
	}

	return left + strings.Repeat(" ", gap) + right
}

// renderToolCallCollapsed renders a single-line collapsed tool call.
// Format: ▸ ⏻ tool_name args_summary                        0.3s
// Active calls use gradient-sampled colors; completed calls use static styles.
func renderToolCallCollapsed(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient) string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	toolName := normalizeToolInlineText(tc.ToolName)
	argsSummary := normalizeToolInlineText(tc.ArgsSummary)

	// Select colors: gradient shimmer for active, static for completed.
	activeColor := activeToolColor(tc, th, grad)
	toolStyle := lipgloss.NewStyle().Foreground(activeColor).Bold(true)
	iconStyle := lipgloss.NewStyle().Foreground(activeColor)

	var left strings.Builder

	// Expand/collapse indicator.
	left.WriteString(mutedStyle.Render(theme.IconExpand))
	left.WriteByte(' ')

	// Tool icon.
	left.WriteString(iconStyle.Render(theme.IconToolCall))
	left.WriteByte(' ')

	// Tool name.
	left.WriteString(toolStyle.Render(toolName))

	// Args summary — gradient for active, muted for completed.
	if argsSummary != "" {
		left.WriteByte(' ')
		if !tc.Completed && grad != nil {
			argsStyle := lipgloss.NewStyle().Foreground(activeColor)
			left.WriteString(argsStyle.Render(argsSummary))
		} else {
			left.WriteString(mutedStyle.Render(argsSummary))
		}
	}

	// Right-aligned: status indicator + duration.
	dur := formatToolCallDuration(tc)
	statusStr := formatToolCallStatus(tc, th, grad)
	rightPart := statusStr + dur

	return composeSingleLineToolCall(left.String(), rightPart, width)
}

// renderToolCallExpanded renders the expanded detail block.
// Active calls use gradient-sampled colors for the header; completed calls use static styles.
func renderToolCallExpanded(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient) []string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	errStyle := lipgloss.NewStyle().Foreground(th.Palette.Error)
	toolName := normalizeToolInlineText(tc.ToolName)
	argsSummary := normalizeToolInlineText(tc.ArgsSummary)

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
	header.WriteString(toolStyle.Render(toolName))
	if argsSummary != "" {
		header.WriteByte(' ')
		if !tc.Completed && grad != nil {
			argsStyle := lipgloss.NewStyle().Foreground(activeColor)
			header.WriteString(argsStyle.Render(argsSummary))
		} else {
			header.WriteString(mutedStyle.Render(argsSummary))
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

	prefix := mutedStyle.Render("│") + "   "
	contentWidth := max(width-lipgloss.Width(prefix), 0)
	if contentWidth <= 0 {
		return lines
	}

	if argsLine := summarizeToolDetailInline(tc.ArgsSummary, tc.FullArgs, contentWidth-len("args - ")); argsLine != "" {
		lines = append(lines, truncateStyledWithDots(prefix+mutedStyle.Render("args - "+argsLine), width))
	}

	if tc.Completed {
		switch {
		case strings.TrimSpace(tc.ErrorMsg) != "":
			if detail := summarizeToolDetailInline(tc.ErrorMsg, tc.ErrorMsg, contentWidth-len("error - ")); detail != "" {
				lines = append(lines, truncateStyledWithDots(prefix+errStyle.Render("error - "+detail), width))
			}
		case strings.TrimSpace(tc.Output) != "":
			if detail := summarizeToolDetailInline(tc.Output, tc.Output, contentWidth-len("output - ")); detail != "" {
				lines = append(lines, truncateStyledWithDots(prefix+mutedStyle.Render("output - "+detail), width))
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
	case d > 0 && d < time.Millisecond:
		return "<1ms"
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
		payload, err := json.Marshal(v)
		if err != nil {
			return `"` + normalizeToolInlineText(v) + `"`
		}
		return string(payload)
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

func summarizeToolDetailInline(primary, fallback string, maxWidth int) string {
	text := strings.TrimSpace(primary)
	if text == "" || text == "{}" {
		text = strings.TrimSpace(fallback)
	}
	if text == "" || text == "{}" || maxWidth <= 0 {
		return ""
	}
	if summarized := summarizeStructuredToolDetail(text); summarized != "" {
		return truncatePlainWithDots(summarized, maxWidth)
	}
	return truncatePlainWithDots(normalizeToolInlineText(text), maxWidth)
}

func summarizeStructuredToolDetail(text string) string {
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return ""
	}
	return summarizeJSONValueInline(parsed)
}

func summarizeJSONValueInline(v any) string {
	switch val := v.(type) {
	case map[string]any:
		if len(val) == 0 {
			return ""
		}
		for _, key := range []string{
			"query",
			"question",
			"description",
			"request",
			"reason",
			"summary",
			"message",
			"approach",
			"response",
			"decision",
			"status",
			"error",
			"ok",
			"success",
			"approved",
			"valid",
			"clean",
			"ready",
			"updated_lines",
			"command",
			"script",
			"path",
			"file_path",
			"pattern",
			"url",
			"name",
			"target",
			"target_agent",
			"action",
			"plan_file",
			"artifact_version",
			"result",
			"output",
		} {
			if summary := summarizeJSONPreferredKey(val, key); summary != "" {
				return summary
			}
		}
		for _, key := range []string{"data", "Data", "payload", "details", "evaluation", "evidence"} {
			if summary := summarizeJSONNestedField(val, key); summary != "" {
				return summary
			}
		}
		keys := make([]string, 0, len(val))
		for key := range val {
			if shouldOmitToolDetailKey(key) {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			summary := summarizeJSONScalarInline(val[key])
			if summary == "" {
				continue
			}
			parts = append(parts, key+"="+summary)
		}
		return strings.Join(parts, " ")
	case []any:
		if len(val) == 0 {
			return ""
		}
		parts := make([]string, 0, min(len(val), 3))
		for i := 0; i < len(val) && i < 3; i++ {
			summary := summarizeJSONScalarInline(val[i])
			if summary == "" {
				continue
			}
			parts = append(parts, summary)
		}
		out := strings.Join(parts, ", ")
		if len(val) > 3 {
			out += ", ..."
		}
		return out
	default:
		return summarizeJSONScalarInline(val)
	}
}

func summarizeJSONPreferredKey(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	summary := summarizeJSONScalarInline(value)
	if summary == "" {
		if nested := summarizeJSONValueInline(value); nested != "" {
			summary = nested
		}
	}
	if summary == "" {
		return ""
	}
	return key + "=" + summary
}

func summarizeJSONNestedField(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return summarizeJSONValueInline(value)
}

func shouldOmitToolDetailKey(key string) bool {
	switch strings.TrimSpace(strings.ToLower(key)) {
	case "session_id",
		"correlation_id",
		"agent_id",
		"agent_type",
		"pipeline_id",
		"task_id",
		"task_name",
		"task_slug",
		"thread_key",
		"metadata",
		"branch_ref",
		"stream_metadata",
		"full_plan",
		"plan",
		"plan_markdown",
		"plan_object",
		"tool_name",
		"tool_call_key":
		return true
	default:
		return false
	}
}

func summarizeJSONScalarInline(v any) string {
	switch val := v.(type) {
	case string:
		return normalizeToolInlineText(val)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		raw, err := json.Marshal(val)
		if err != nil {
			return ""
		}
		return normalizeToolInlineText(string(raw))
	}
}

func normalizeToolInlineText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\t", " ")
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
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
