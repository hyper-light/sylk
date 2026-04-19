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
		if interAgentToolHasActiveVisual(calls[i].InterAgent) {
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

	// Stamp orphan flags onto a per-frame copy of the call list so the
	// formatter can swap the live spinner for a `?` indicator on rows the
	// agent appears to have abandoned. Mutating the caller's slice would
	// persist the visual hint across renders even if a late Complete
	// eventually arrives, so we work on copies. See toolCallVisuallyOrphaned
	// for the heuristic.
	now := time.Now()
	render := make([]ToolCallRecord, len(calls))
	for i := range calls {
		render[i] = calls[i]
		render[i].OrphanedAtRender = toolCallVisuallyOrphaned(calls, i, now)
	}

	var lines []string
	var regions []ToolCallRegion

	for i := range render {
		start := len(lines)
		if render[i].InterAgent != nil {
			rendered, subregions := renderInterAgentToolCall(render, i, width, th, grad)
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
			if render[i].Expanded {
				lines = append(lines, renderToolCallExpanded(render[i], width, th, grad)...)
			} else {
				lines = append(lines, renderToolCallCollapsed(render[i], width, th, grad))
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
	lines      []string
	subregions []ToolCallSubregion
	// failed is true when the underlying ToolCallRecord finished with
	// Success=false. capInterAgentChildRows uses this to pin failed rows in
	// the visible window so the user can see which calls failed even when
	// the call list is large enough to spill into the overflow bucket.
	// Without this, an orchestrator consult that aborts after several
	// failures shows only the most recent (often successful) calls and the
	// failure status reads as a parent-row X with no visible cause.
	failed bool
}

type interAgentRenderedChildSection struct {
	childIndex int
	childPath  []int
	header     string
	rows       []interAgentRenderedChildRow
}

func renderInterAgentToolCall(calls []ToolCallRecord, idx, width int, th *theme.Theme, grad *theme.Gradient) ([]string, []ToolCallSubregion) {
	if idx < 0 || idx >= len(calls) || calls[idx].InterAgent == nil || width <= 0 {
		return nil, nil
	}
	row := calls[idx].InterAgent
	if !interAgentToolHasVisibleChildren(row) {
		lines := []string{renderInterAgentHeadline(calls, idx, row, width, th)}
		if calls[idx].Expanded || interAgentShouldShowErrorDetail(calls[idx], row) {
			lines = append(lines, renderInterAgentExpandedSummary(calls, idx, row, width, th)...)
		}
		return lines, nil
	}

	rootSummary := interAgentDisplaySummary(calls[idx], row)
	hasLater := hasLaterInterAgentCall(calls, idx)
	specs := orderedInterAgentChildren(row, nil, nil)
	if len(specs) == 0 {
		lines := []string{renderInterAgentHeadline(calls, idx, row, width, th)}
		if calls[idx].Expanded || interAgentShouldShowErrorDetail(calls[idx], row) {
			lines = append(lines, renderInterAgentExpandedSummary(calls, idx, row, width, th)...)
		}
		return lines, nil
	}
	stack := make([]interAgentNestedBlockItem, 0, len(specs))
	for i := len(specs) - 1; i >= 0; i-- {
		spec := specs[i]
		stack = append(stack, interAgentNestedBlockItem{
			kind:           interAgentNestedBlockItemChildHeader,
			isLast:         i == len(specs)-1 && !hasLater,
			childIndex:     spec.childIndex,
			childPath:      cloneIntSlice(spec.childPath),
			interAgentPath: nil,
			child:          spec.child,
			rootSummary:    rootSummary,
		})
	}
	return renderInterAgentNestedBlock(stack, width, th, grad)
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

	linePrefix := mutedStyle.Render(treePrefix+" ") + renderInterAgentLabels(row.AgentTypes, th, mutedStyle)
	if row.RepeatCount >= 2 {
		linePrefix += mutedStyle.Render(fmt.Sprintf(" ×%d", row.RepeatCount))
	}
	summary := interAgentDisplaySummary(calls[idx], row)
	if summary != "" {
		linePrefix += mutedStyle.Render(" - ") + summaryStyle.Render(truncatePlainWithDots(summary, max(width-lipgloss.Width(linePrefix+" - "), 0)))
	}

	return composeSingleLineToolCall(linePrefix, renderNestedInterAgentToolCallRightPart(calls[idx], th), width)
}

func renderInterAgentExpandedSummary(calls []ToolCallRecord, idx int, row *InterAgentTool, width int, th *theme.Theme) []string {
	if row == nil || width <= 0 {
		return nil
	}
	summary := interAgentDisplaySummary(calls[idx], row)
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

func interAgentDisplaySummary(record ToolCallRecord, row *InterAgentTool) string {
	if row == nil {
		return ""
	}
	if row.Status == InterAgentToolFailed {
		if errText := normalizeInlineText(record.ErrorMsg); errText != "" {
			return errText
		}
	}
	return normalizeInlineText(row.Summary)
}

func interAgentShouldShowErrorDetail(record ToolCallRecord, row *InterAgentTool) bool {
	if row == nil || row.Status != InterAgentToolFailed {
		return false
	}
	return normalizeInlineText(record.ErrorMsg) != ""
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
		childPath := []int{childIndex}
		for j := range child.ToolCalls {
			row := renderInterAgentChildToolCall(child.ToolCalls[j], rowWidth, th, grad, childPath, nil, j)
			if len(row.lines) > 0 {
				rows = append(rows, row)
			}
		}
		rows = capInterAgentChildRows(rows, rowWidth, th, child.ToolCallsExpanded, childPath, nil)
		if header == "" && len(rows) == 0 {
			continue
		}
		sections = append(sections, interAgentRenderedChildSection{childIndex: childIndex, childPath: cloneIntSlice(childPath), header: header, rows: rows})
	}
	for i := range row.Children {
		if usedChildren[i] {
			continue
		}
		child := row.Children[i]
		header := renderInterAgentChildHeader(child, headerWidth, th, rootSummary)
		rows := make([]interAgentRenderedChildRow, 0, len(child.ToolCalls))
		childPath := []int{i}
		for j := range child.ToolCalls {
			rendered := renderInterAgentChildToolCall(child.ToolCalls[j], rowWidth, th, grad, childPath, nil, j)
			if len(rendered.lines) > 0 {
				rows = append(rows, rendered)
			}
		}
		rows = capInterAgentChildRows(rows, rowWidth, th, child.ToolCallsExpanded, childPath, nil)
		if header == "" && len(rows) == 0 {
			continue
		}
		sections = append(sections, interAgentRenderedChildSection{childIndex: i, childPath: cloneIntSlice(childPath), header: header, rows: rows})
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
	label := renderInterAgentLabels([]string{nestedActivityAgentType(&child)}, th, mutedStyle)
	left := label
	if summary := interAgentChildSummary(child, rootSummary); summary != "" {
		left += mutedStyle.Render(" - ") + summaryStyle.Render(truncatePlainWithDots(summary, max(width-lipgloss.Width(left+" - "), 0)))
	}
	return composeSingleLineToolCall(left, renderInterAgentChildRightPart(child, th), width)
}

func interAgentChildSummary(child InterAgentChildActivity, rootSummary string) string {
	if child.Failed {
		if root := normalizeInlineText(rootSummary); root != "" {
			return root
		}
	}
	if !child.Completed {
		root := normalizeInlineText(rootSummary)
		status := normalizeInlineText(child.ThinkingStatus)
		switch {
		case root == "":
			return status
		case status == "" || status == root:
			return root
		default:
			return normalizeInlineText(root + " / " + status)
		}
	}
	summary := normalizeInlineText(child.ResultSummary)
	if summary == "" || summary == rootSummary {
		return ""
	}
	return summary
}

func renderInterAgentChildRightPart(child InterAgentChildActivity, th *theme.Theme) string {
	statusIcon, statusStyle := interAgentChildStatusGlyph(child, th)
	if child.Completed || child.Failed {
		return statusStyle.Render(statusIcon)
	}
	statusText := strings.TrimSpace(child.ThinkingText)
	if statusText == "" {
		return statusStyle.Render(statusIcon)
	}
	return statusStyle.Render(statusText)
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

func renderInterAgentChildToolCall(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient, childPath []int, interAgentPath []int, childToolCallIdx int) interAgentRenderedChildRow {
	if tc.InterAgent != nil {
		return renderNestedInterAgentChildToolCallBlock(tc, width, th, grad, childPath, interAgentPath, childToolCallIdx)
	}
	var lines []string
	if tc.Expanded {
		lines = renderNestedChildToolCallExpanded(tc, width, th, grad)
	} else if line := renderToolCallCollapsed(tc, width, th, grad); line != "" {
		lines = []string{line}
	}
	if len(lines) == 0 {
		return interAgentRenderedChildRow{}
	}
	return interAgentRenderedChildRow{
		lines: lines,
		subregions: []ToolCallSubregion{{
			Start:            0,
			End:              len(lines),
			Kind:             ToolCallSubregionChildTool,
			ChildIndex:       leafChildIndex(childPath),
			ChildToolCallIdx: childToolCallIdx,
			ChildPath:        cloneIntSlice(childPath),
			InterAgentPath:   cloneIntSlice(interAgentPath),
		}},
		failed: tc.Completed && !tc.Success,
	}
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

	contentWidth := width
	if contentWidth <= 0 {
		return lines
	}

	if argsLine := summarizeToolDetailInline(tc.ArgsSummary, tc.FullArgs, contentWidth-len("args - ")); argsLine != "" {
		lines = append(lines, truncateStyledWithDots(mutedStyle.Render("args - "+argsLine), width))
	}
	if tc.Completed {
		switch {
		case strings.TrimSpace(tc.ErrorMsg) != "":
			if detail := summarizeToolDetailInline(tc.ErrorMsg, tc.ErrorMsg, contentWidth-len("error - ")); detail != "" {
				lines = append(lines, truncateStyledWithDots(errStyle.Render("error - "+detail), width))
			}
		case strings.TrimSpace(tc.Output) != "":
			if detail := summarizeToolDetailInline(tc.Output, tc.Output, contentWidth-len("output - ")); detail != "" {
				lines = append(lines, truncateStyledWithDots(mutedStyle.Render("output - "+detail), width))
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
	left := renderInterAgentLabels(tc.InterAgent.AgentTypes, th, mutedStyle)
	if summary := normalizeInlineText(tc.InterAgent.Summary); summary != "" {
		left += mutedStyle.Render(" - ") + summaryStyle.Render(truncatePlainWithDots(summary, max(width-lipgloss.Width(left+" - "), 0)))
	}
	return composeSingleLineToolCall(left, renderNestedInterAgentToolCallRightPart(tc, th), width)
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
	detailStyle := lipgloss.NewStyle().Foreground(th.Palette.Subtle)
	contentWidth := width
	lines := []string{headline}
	for _, line := range wrapLine(summary, contentWidth, detailStyle) {
		lines = append(lines, truncateStyledWithDots(line, width))
	}
	return lines
}

func renderNestedInterAgentToolCallRightPart(tc ToolCallRecord, th *theme.Theme) string {
	if tc.InterAgent == nil {
		return ""
	}
	statusIcon, statusStyle := interAgentStatusGlyph(tc.InterAgent.Status, th)
	duration := ""
	if !tc.StartedAt.IsZero() || tc.Duration > 0 {
		duration = " " + formatToolCallDuration(tc)
	}
	return statusStyle.Render(statusIcon + duration)
}

func capInterAgentChildRows(rows []interAgentRenderedChildRow, width int, th *theme.Theme, expanded bool, childPath []int, interAgentPath []int) []interAgentRenderedChildRow {
	if expanded || len(rows) <= maxInterAgentChildLines {
		return rows
	}

	// Build the visible set: always include the most recent (maxInterAgentChildLines-1)
	// rows, plus any failed rows that would otherwise be hidden in the
	// overflow bucket. The historical "show only the last N" policy made
	// failure-status invisible when the parent inter-agent row aborted —
	// e.g., an orchestrator consult that errors after several failed tool
	// calls would render as a parent X with no visible cause because the
	// failed children all sat in the hidden bucket. Pinning them keeps
	// failure-attribution legible regardless of call-list length.
	tailStart := len(rows) - (maxInterAgentChildLines - 1)
	if tailStart < 0 {
		tailStart = 0
	}
	pinned := make([]int, 0, len(rows))
	for i := 0; i < tailStart; i++ {
		if rows[i].failed {
			pinned = append(pinned, i)
		}
	}
	hiddenCount := tailStart - len(pinned)
	hiddenFailedCount := 0
	// hiddenFailedCount is informational for the overflow label even when
	// pinning has hoisted every failure into the visible window — readers
	// shouldn't see "(0 failed)" when nothing is hidden, so we only annotate
	// when there are still hidden failures (which only happens if we hit
	// the unlikely cap below).

	// Cap the number of pinned-rows we hoist into the visible window so a
	// pathological history (every call failed) doesn't blow up the row.
	maxPinned := maxInterAgentChildLines * 2
	if len(pinned) > maxPinned {
		// Keep the most recent failed rows; demote older failures back into
		// the overflow bucket and surface their count in the label.
		dropped := len(pinned) - maxPinned
		hiddenFailedCount += dropped
		hiddenCount += dropped
		pinned = pinned[len(pinned)-maxPinned:]
	}

	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	overflowLabel := fmt.Sprintf("… %d earlier event%s", hiddenCount, pluralSuffix(hiddenCount))
	if hiddenFailedCount > 0 {
		overflowLabel += fmt.Sprintf(" (%d failed)", hiddenFailedCount)
	}

	out := make([]interAgentRenderedChildRow, 0, len(pinned)+maxInterAgentChildLines)
	if hiddenCount > 0 {
		out = append(out, interAgentRenderedChildRow{
			lines: []string{truncateStyledWithDots(mutedStyle.Render(overflowLabel), width)},
			subregions: []ToolCallSubregion{{
				Start:          0,
				End:            1,
				Kind:           ToolCallSubregionOverflow,
				ChildIndex:     leafChildIndex(childPath),
				ChildPath:      cloneIntSlice(childPath),
				InterAgentPath: cloneIntSlice(interAgentPath),
			}},
		})
	}
	for _, idx := range pinned {
		out = append(out, rows[idx])
	}
	out = append(out, rows[tailStart:]...)
	return out
}

type interAgentOrderedChildSpec struct {
	childIndex int
	childPath  []int
	child      InterAgentChildActivity
}

type interAgentNestedBlockItemKind int

const (
	interAgentNestedBlockItemRootTool interAgentNestedBlockItemKind = iota
	interAgentNestedBlockItemChildHeader
	interAgentNestedBlockItemOverflow
	interAgentNestedBlockItemToolCall
)

type interAgentNestedBlockItem struct {
	kind             interAgentNestedBlockItemKind
	root             bool
	isLast           bool
	ancestors        []bool
	childIndex       int
	childPath        []int
	interAgentPath   []int
	child            InterAgentChildActivity
	toolCall         ToolCallRecord
	childToolCallIdx int
	overflowCount      int
	overflowExpanded   bool
	overflowFailedHint int
	rootSummary      string
	anchorToolCall   bool
	anchorChildPath  []int
	anchorInterPath  []int
	anchorToolIdx    int
}

func cloneBoolSlice(in []bool) []bool {
	if len(in) == 0 {
		return nil
	}
	out := make([]bool, len(in))
	copy(out, in)
	return out
}

func renderNestedInterAgentChildToolCallBlock(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient, childPath []int, interAgentPath []int, childToolCallIdx int) interAgentRenderedChildRow {
	if tc.InterAgent == nil || width <= 0 {
		return interAgentRenderedChildRow{}
	}
	if !interAgentToolHasVisibleChildren(tc.InterAgent) {
		lines := renderInterAgentNestedFallbackToolCall(tc, width, th, grad)
		if len(lines) == 0 {
			return interAgentRenderedChildRow{}
		}
		return interAgentRenderedChildRow{
			lines: lines,
			subregions: []ToolCallSubregion{{
				Start:            0,
				End:              len(lines),
				Kind:             ToolCallSubregionChildTool,
				ChildIndex:       leafChildIndex(childPath),
				ChildToolCallIdx: childToolCallIdx,
				ChildPath:        cloneIntSlice(childPath),
				InterAgentPath:   cloneIntSlice(interAgentPath),
			}},
		}
	}
	nextInterAgentPath := append(cloneIntSlice(interAgentPath), childToolCallIdx)
	specs := orderedInterAgentChildren(tc.InterAgent, childPath, nextInterAgentPath)
	stack := make([]interAgentNestedBlockItem, 0, len(specs))
	for i := len(specs) - 1; i >= 0; i-- {
		spec := specs[i]
		stack = append(stack, interAgentNestedBlockItem{
			kind:            interAgentNestedBlockItemChildHeader,
			isLast:          i == len(specs)-1,
			childIndex:      spec.childIndex,
			childPath:       cloneIntSlice(spec.childPath),
			interAgentPath:  cloneIntSlice(nextInterAgentPath),
			child:           spec.child,
			rootSummary:     interAgentDisplaySummary(tc, tc.InterAgent),
			anchorToolCall:  i == 0,
			anchorChildPath: cloneIntSlice(childPath),
			anchorInterPath: cloneIntSlice(interAgentPath),
			anchorToolIdx:   childToolCallIdx,
		})
	}
	lines, subregions := renderInterAgentNestedBlock(stack, width, th, grad)
	return interAgentRenderedChildRow{lines: lines, subregions: subregions}
}

func orderedInterAgentChildren(row *InterAgentTool, parentChildPath []int, interAgentPath []int) []interAgentOrderedChildSpec {
	if row == nil {
		return nil
	}
	specs := make([]interAgentOrderedChildSpec, 0, max(len(row.Children), len(row.AgentTypes)))
	usedChildren := make([]bool, len(row.Children))
	for _, target := range normalizeAgentTypes(row.AgentTypes) {
		childIndex, child := findInterAgentChildByAgentType(row.Children, usedChildren, target)
		if child == nil {
			placeholder := synthesizeInterAgentChildActivity(row, target)
			specs = append(specs, interAgentOrderedChildSpec{
				childIndex: -1,
				childPath:  nil,
				child:      placeholder,
			})
			continue
		}
		specs = append(specs, interAgentOrderedChildSpec{
			childIndex: childIndex,
			childPath:  append(cloneIntSlice(parentChildPath), childIndex),
			child:      *child,
		})
	}
	for i := range row.Children {
		if usedChildren[i] {
			continue
		}
		specs = append(specs, interAgentOrderedChildSpec{
			childIndex: i,
			childPath:  append(cloneIntSlice(parentChildPath), i),
			child:      row.Children[i],
		})
	}
	return specs
}

func interAgentToolHasVisibleChildren(row *InterAgentTool) bool {
	return row != nil && (len(row.Children) > 0 || len(normalizeAgentTypes(row.AgentTypes)) > 0)
}

func renderInterAgentNestedFallbackToolCall(tc ToolCallRecord, width int, th *theme.Theme, grad *theme.Gradient) []string {
	if tc.InterAgent == nil || width <= 0 {
		return nil
	}
	if tc.Expanded {
		return renderNestedInterAgentToolCallExpanded(tc, width, th)
	}
	if line := renderNestedInterAgentToolCall(tc, width, th); line != "" {
		return []string{line}
	}
	return nil
}

func renderInterAgentNestedBlock(stack []interAgentNestedBlockItem, width int, th *theme.Theme, grad *theme.Gradient) ([]string, []ToolCallSubregion) {
	if len(stack) == 0 || width <= 0 {
		return nil, nil
	}
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	blockLines := make([]string, 0, 8)
	subregions := make([]ToolCallSubregion, 0, 4)
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		firstPrefix, detailMidPrefix, detailLastPrefix := interAgentNestedBlockPrefixes(item.root, item.ancestors, item.isLast, mutedStyle)
		contentWidth := width
		if !item.root {
			contentWidth = max(width-lipgloss.Width(firstPrefix), 0)
		}
		switch item.kind {
		case interAgentNestedBlockItemChildHeader:
			header := renderInterAgentChildHeader(item.child, contentWidth, th, item.rootSummary)
			if header != "" {
				rowStart := len(blockLines)
				appendInterAgentNestedBlockLines(&blockLines, []string{header}, width, firstPrefix, detailMidPrefix, detailLastPrefix)
				if item.anchorToolCall {
					subregions = append(subregions, ToolCallSubregion{
						Start:            rowStart,
						End:              len(blockLines),
						Kind:             ToolCallSubregionChildTool,
						ChildIndex:       leafChildIndex(item.anchorChildPath),
						ChildToolCallIdx: item.anchorToolIdx,
						ChildPath:        cloneIntSlice(item.anchorChildPath),
						InterAgentPath:   cloneIntSlice(item.anchorInterPath),
					})
				}
			}
			if len(item.child.ToolCalls) == 0 {
				continue
			}
			nextAncestors := append(cloneBoolSlice(item.ancestors), !item.isLast)
			if len(item.child.ToolCalls) <= maxInterAgentChildLines {
				for i := len(item.child.ToolCalls) - 1; i >= 0; i-- {
					stack = append(stack, interAgentNestedBlockItem{
						kind:             interAgentNestedBlockItemToolCall,
						isLast:           i == len(item.child.ToolCalls)-1,
						ancestors:        cloneBoolSlice(nextAncestors),
						childIndex:       item.childIndex,
						childPath:        cloneIntSlice(item.childPath),
						interAgentPath:   cloneIntSlice(item.interAgentPath),
						toolCall:         item.child.ToolCalls[i],
						childToolCallIdx: i,
					})
				}
				continue
			}
			visibleStart := len(item.child.ToolCalls) - (maxInterAgentChildLines - 1)
			renderStart := visibleStart
			if item.child.ToolCallsExpanded {
				renderStart = 0
			}
			// Pin failed tool calls from the to-be-hidden range so the user
			// can see what failed even when the call list is long enough to
			// spill into the overflow bucket. Without this pinning the
			// parent inter-agent row's terminal X is uncorroborated by any
			// visible child failure — the historical "orchestrator
			// conversation: tool calls failed N consecutive turns" report
			// where the screen showed only successful child rows.
			pinnedHidden := []int(nil)
			hiddenFailedCount := 0
			if !item.child.ToolCallsExpanded {
				for i := 0; i < renderStart; i++ {
					tc := item.child.ToolCalls[i]
					if tc.Completed && !tc.Success {
						pinnedHidden = append(pinnedHidden, i)
					}
				}
				if len(pinnedHidden) > maxInterAgentChildLines*2 {
					dropped := len(pinnedHidden) - maxInterAgentChildLines*2
					hiddenFailedCount += dropped
					pinnedHidden = pinnedHidden[len(pinnedHidden)-(maxInterAgentChildLines*2):]
				}
			}
			for i := len(item.child.ToolCalls) - 1; i >= renderStart; i-- {
				stack = append(stack, interAgentNestedBlockItem{
					kind:             interAgentNestedBlockItemToolCall,
					isLast:           i == len(item.child.ToolCalls)-1,
					ancestors:        cloneBoolSlice(nextAncestors),
					childIndex:       item.childIndex,
					childPath:        cloneIntSlice(item.childPath),
					interAgentPath:   cloneIntSlice(item.interAgentPath),
					toolCall:         item.child.ToolCalls[i],
					childToolCallIdx: i,
				})
			}
			// Push pinned failed-rows in reverse so they emerge above the
			// tail (most-recent-failure-first ordering preserved).
			for k := len(pinnedHidden) - 1; k >= 0; k-- {
				i := pinnedHidden[k]
				stack = append(stack, interAgentNestedBlockItem{
					kind:             interAgentNestedBlockItemToolCall,
					isLast:           false,
					ancestors:        cloneBoolSlice(nextAncestors),
					childIndex:       item.childIndex,
					childPath:        cloneIntSlice(item.childPath),
					interAgentPath:   cloneIntSlice(item.interAgentPath),
					toolCall:         item.child.ToolCalls[i],
					childToolCallIdx: i,
				})
			}
			stack = append(stack, interAgentNestedBlockItem{
				kind:               interAgentNestedBlockItemOverflow,
				isLast:             false,
				ancestors:          cloneBoolSlice(nextAncestors),
				childIndex:         item.childIndex,
				childPath:          cloneIntSlice(item.childPath),
				interAgentPath:     cloneIntSlice(item.interAgentPath),
				overflowCount:      visibleStart - len(pinnedHidden),
				overflowFailedHint: hiddenFailedCount,
				overflowExpanded:   item.child.ToolCallsExpanded,
			})
		case interAgentNestedBlockItemToolCall:
			if item.toolCall.InterAgent != nil && interAgentToolHasVisibleChildren(item.toolCall.InterAgent) {
				nextInterAgentPath := append(cloneIntSlice(item.interAgentPath), item.childToolCallIdx)
				specs := orderedInterAgentChildren(item.toolCall.InterAgent, item.childPath, nextInterAgentPath)
				for i := len(specs) - 1; i >= 0; i-- {
					spec := specs[i]
					specIsLast := item.isLast && i == len(specs)-1
					stack = append(stack, interAgentNestedBlockItem{
						kind:            interAgentNestedBlockItemChildHeader,
						isLast:          specIsLast,
						ancestors:       cloneBoolSlice(item.ancestors),
						childIndex:      spec.childIndex,
						childPath:       cloneIntSlice(spec.childPath),
						interAgentPath:  cloneIntSlice(nextInterAgentPath),
						child:           spec.child,
						rootSummary:     interAgentDisplaySummary(item.toolCall, item.toolCall.InterAgent),
						anchorToolCall:  i == 0,
						anchorChildPath: cloneIntSlice(item.childPath),
						anchorInterPath: cloneIntSlice(item.interAgentPath),
						anchorToolIdx:   item.childToolCallIdx,
					})
				}
				continue
			}
			var rowLines []string
			if item.toolCall.InterAgent != nil {
				rowLines = renderInterAgentNestedFallbackToolCall(item.toolCall, contentWidth, th, grad)
			} else if item.toolCall.Expanded {
				rowLines = renderNestedChildToolCallExpanded(item.toolCall, contentWidth, th, grad)
			} else if line := renderToolCallCollapsed(item.toolCall, contentWidth, th, grad); line != "" {
				rowLines = []string{line}
			}
			if len(rowLines) == 0 {
				continue
			}
			rowStart := len(blockLines)
			appendInterAgentNestedBlockLines(&blockLines, rowLines, width, firstPrefix, detailMidPrefix, detailLastPrefix)
			subregions = append(subregions, ToolCallSubregion{
				Start:            rowStart,
				End:              len(blockLines),
				Kind:             ToolCallSubregionChildTool,
				ChildIndex:       leafChildIndex(item.childPath),
				ChildToolCallIdx: item.childToolCallIdx,
				ChildPath:        cloneIntSlice(item.childPath),
				InterAgentPath:   cloneIntSlice(item.interAgentPath),
			})
		case interAgentNestedBlockItemOverflow:
			// When pinning hoisted every hidden row into the visible window,
			// the overflow indicator has nothing left to count — skip it.
			// Otherwise the user sees a control that, when clicked,
			// "expands" zero additional rows.
			if item.overflowCount <= 0 && item.overflowFailedHint <= 0 && !item.overflowExpanded {
				continue
			}
			rowStart := len(blockLines)
			label := renderInterAgentOverflowControlLabel(item.overflowCount, item.overflowFailedHint, item.overflowExpanded, th)
			prefix := interAgentOverflowControlPrefix(item.root, item.ancestors, mutedStyle)
			blockLines = append(blockLines, truncateStyledWithDots(prefix+truncateStyledWithDots(label, max(width-lipgloss.Width(prefix), 0)), width))
			subregions = append(subregions, ToolCallSubregion{
				Start:          rowStart,
				End:            len(blockLines),
				Kind:           ToolCallSubregionOverflow,
				ChildIndex:     leafChildIndex(item.childPath),
				ChildPath:      cloneIntSlice(item.childPath),
				InterAgentPath: cloneIntSlice(item.interAgentPath),
			})
		}
	}
	return blockLines, subregions
}

// renderInterAgentOverflowControlLabel formats the collapse/expand control for
// a child's hidden tool-call rows. When hiddenFailed > 0 the label includes a
// "(N failed)" annotation so the user can see at a glance that expanding the
// overflow will reveal failure-status rows that the visible window missed.
// hiddenFailed is non-zero only when the failure-pinning cap kicked in
// (extremely long history of failures); in the common case all failures are
// already visible and the label reads as the historical "▸ Show N earlier
// events".
func renderInterAgentOverflowControlLabel(hiddenCount, hiddenFailed int, expanded bool, th *theme.Theme) string {
	mutedStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted)
	if expanded {
		return mutedStyle.Render("▾ hide")
	}
	label := fmt.Sprintf("▸ Show %d earlier event%s", hiddenCount, pluralSuffix(hiddenCount))
	if hiddenFailed > 0 {
		label += fmt.Sprintf(" (%d failed)", hiddenFailed)
	}
	return mutedStyle.Render(label)
}

func interAgentNestedBlockPrefixes(root bool, ancestors []bool, isLast bool, mutedStyle lipgloss.Style) (string, string, string) {
	if root {
		return "", "", ""
	}
	var stem strings.Builder
	for _, continues := range ancestors {
		if continues {
			stem.WriteString(mutedStyle.Render("│  "))
		} else {
			stem.WriteString(mutedStyle.Render("   "))
		}
	}
	first := stem.String() + mutedStyle.Render("├─ ")
	detailMid := stem.String() + mutedStyle.Render("│  ") + mutedStyle.Render("│  ")
	detailLast := stem.String() + mutedStyle.Render("│  ") + mutedStyle.Render("└─ ")
	if isLast {
		first = stem.String() + mutedStyle.Render("└─ ")
		detailMid = stem.String() + mutedStyle.Render("   ") + mutedStyle.Render("│  ")
		detailLast = stem.String() + mutedStyle.Render("   ") + mutedStyle.Render("└─ ")
	}
	return first, detailMid, detailLast
}

func interAgentOverflowControlPrefix(root bool, ancestors []bool, mutedStyle lipgloss.Style) string {
	if root {
		return ""
	}
	var stem strings.Builder
	for _, continues := range ancestors {
		if continues {
			stem.WriteString(mutedStyle.Render("│  "))
		} else {
			stem.WriteString(mutedStyle.Render("   "))
		}
	}
	return stem.String() + mutedStyle.Render("╰─ ")
}

func appendInterAgentNestedBlockLines(out *[]string, block []string, width int, firstPrefix, detailMidPrefix, detailLastPrefix string) {
	if len(block) == 0 {
		return
	}
	for idx, line := range block {
		prefix := firstPrefix
		if idx > 0 {
			prefix = detailMidPrefix
			if idx == len(block)-1 {
				prefix = detailLastPrefix
			}
		}
		*out = append(*out, truncateStyledWithDots(prefix+truncateStyledWithDots(line, max(width-lipgloss.Width(prefix), 0)), width))
	}
}

func leafChildIndex(childPath []int) int {
	if len(childPath) == 0 {
		return -1
	}
	return childPath[len(childPath)-1]
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
// Inter-agent rows whose Kind reaches a terminal Status at dispatch (consult,
// approval, store) freeze their captured duration once Completed=true so a
// stale Pending-after-Done status (the historical "guardian - approved by
// user 471s" bug) cannot leave the row in a perpetually-growing live state.
// Challenge rows deliberately stay Pending after dispatch until a response
// arrives on the thread, so they keep ticking even when Completed=true.
//
// When OrphanedAtRender is set the live elapsed display is replaced with the
// orphan glyph so the user is not misled by a still-spinning timer for a
// row whose Complete event has very likely been lost. This does not assert
// the call is dead — only that the agent has moved on enough that we should
// stop pretending the row is actively in flight. The actual duration string
// is still embedded so users can see how long the row has been outstanding.
func formatToolCallDuration(tc ToolCallRecord) string {
	if tc.OrphanedAtRender && !tc.Completed {
		return orphanedToolCallDurationString(tc)
	}
	if tc.Completed && interAgentRowFreezesOnCompletion(tc.InterAgent) {
		return formatToolDuration(tc.Duration)
	}
	if tc.InterAgent != nil && tc.InterAgent.Status == InterAgentToolPending && !tc.StartedAt.IsZero() {
		return formatToolDuration(time.Since(tc.StartedAt))
	}
	if !tc.Completed {
		return formatToolDuration(time.Since(tc.StartedAt))
	}
	return formatToolDuration(tc.Duration)
}

// orphanGlyph is the single-character indicator used to replace the live
// spinner for tool calls the renderer believes have been left behind by the
// agent. A simple `?` keeps the column width identical to the spinner so
// layout doesn't shift between frames as orphan status flips.
const orphanGlyph = "?"

func orphanedToolCallDurationString(tc ToolCallRecord) string {
	elapsed := tc.Duration
	if elapsed == 0 && !tc.StartedAt.IsZero() {
		elapsed = time.Since(tc.StartedAt)
	}
	return orphanGlyph + " " + formatToolDuration(elapsed)
}

// interAgentRowFreezesOnCompletion reports whether an inter-agent row should
// freeze its rendered duration once the underlying dispatch completes.
// Non-inter-agent tool calls always freeze. Challenge rows are the sole
// exception: their dispatch completes immediately but the row is logically
// still in flight until a response arrives on the shared challenge thread,
// so they keep displaying live elapsed time.
func interAgentRowFreezesOnCompletion(row *InterAgentTool) bool {
	if row == nil {
		return true
	}
	return row.Kind != InterAgentToolChallenge
}

// orphanRenderAgeThreshold is how long a pending row must have been in flight
// before it becomes eligible for the orphan indicator. Below this we assume
// the call is genuinely working (file I/O, network requests, large
// summarizations all take seconds). Above this AND with enough subsequent
// activity, the row is most likely a lost-Complete artifact.
const orphanRenderAgeThreshold = 30 * time.Second

// orphanRenderSucceedingActivityCount is how many newer Completed peers must
// have appeared after a pending row before we treat it as visually orphaned.
// This is the "the agent has moved on" signal — without it we would flag any
// long-running tool as orphaned the moment it crossed the age threshold.
const orphanRenderSucceedingActivityCount = 3

// toolCallVisuallyOrphaned reports whether the row at idx should render with
// the orphan indicator. The signal combines (a) age greater than the threshold
// and (b) at least N completed tool events appearing after this row in the
// same call list. Inter-agent challenge rows are excluded because they are
// designed to remain pending after dispatch until a peer response arrives —
// a long-pending challenge is expected behavior, not an orphan.
func toolCallVisuallyOrphaned(calls []ToolCallRecord, idx int, now time.Time) bool {
	if idx < 0 || idx >= len(calls) {
		return false
	}
	tc := calls[idx]
	if tc.Completed {
		return false
	}
	if tc.InterAgent != nil && tc.InterAgent.Kind == InterAgentToolChallenge {
		return false
	}
	if tc.StartedAt.IsZero() {
		return false
	}
	if now.Sub(tc.StartedAt) < orphanRenderAgeThreshold {
		return false
	}
	completedAfter := 0
	for j := idx + 1; j < len(calls); j++ {
		if calls[j].Completed {
			completedAfter++
			if completedAfter >= orphanRenderSucceedingActivityCount {
				return true
			}
		}
	}
	return false
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
