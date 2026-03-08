package chat

import (
	"fmt"
	"strings"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

// streamRenderState tracks the incremental render split between
// the "stable prefix" (complete markdown blocks) and the "trailing tail"
// (the last incomplete block). Only the tail is re-rendered on each flush;
// the stable prefix's output is cached and reused.
type streamRenderState struct {
	lastStableLen int          // Byte length of content that was stably rendered.
	stableLines   []string     // Rendered output for the stable prefix.
	stableRegions []CodeRegion // Code regions within the stable prefix.
}

// findStableBoundary scans content for the last double-newline that is NOT
// inside an unclosed code fence. Returns the byte offset of the end of
// the stable prefix (i.e., the byte after the double-newline).
// Returns 0 if no stable boundary exists.
func findStableBoundary(content string) int {
	boundary := 0
	fenceOpen := false
	i := 0
	for i < len(content) {
		lineStart := i
		lineEnd := strings.IndexByte(content[i:], '\n')
		var line string
		if lineEnd < 0 {
			line = content[i:]
			i = len(content)
		} else {
			line = content[i : i+lineEnd]
			i += lineEnd + 1
		}

		trimmed := strings.TrimSpace(line)
		if isCodeFenceLine(trimmed) {
			fenceOpen = !fenceOpen
		}

		// A blank line outside a code fence marks a potential stable boundary.
		if !fenceOpen && trimmed == "" && lineStart > 0 {
			boundary = i
		}
	}

	// Never split inside an open fence.
	if fenceOpen {
		return findBoundaryBeforeOpenFence(content, boundary)
	}
	return boundary
}

// isCodeFenceLine reports whether a trimmed line starts a code fence
// (three or more backticks or tildes).
func isCodeFenceLine(trimmed string) bool {
	if len(trimmed) < 3 {
		return false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return false
	}
	count := 0
	for _, b := range []byte(trimmed) {
		if b == ch {
			count++
		} else {
			break
		}
	}
	return count >= 3
}

// findBoundaryBeforeOpenFence walks backwards from candidateBoundary
// to find a boundary that doesn't leave a code fence open.
func findBoundaryBeforeOpenFence(content string, candidateBoundary int) int {
	if candidateBoundary == 0 {
		return 0
	}
	// Re-scan up to candidateBoundary to verify fence state.
	fenceOpen := false
	boundary := 0
	i := 0
	for i < candidateBoundary {
		lineEnd := strings.IndexByte(content[i:], '\n')
		var line string
		if lineEnd < 0 {
			line = content[i:candidateBoundary]
			i = candidateBoundary
		} else {
			line = content[i : i+lineEnd]
			i += lineEnd + 1
		}

		trimmed := strings.TrimSpace(line)
		if isCodeFenceLine(trimmed) {
			fenceOpen = !fenceOpen
		}

		if !fenceOpen && trimmed == "" && i <= candidateBoundary {
			boundary = i
		}
	}
	if fenceOpen {
		return 0
	}
	return boundary
}

// renderStreamingEntry performs incremental rendering of a streaming entry's
// content. It splits content into a stable prefix and trailing tail, caches
// the stable prefix output, and only re-renders the tail.
// Returns the merged lines and code regions for the content portion only
// (no header/summary — those are added by renderStreamingEntryFull).
//
// Stable content is rendered incrementally: when the stable boundary advances,
// only the newly promoted segment (between old and new boundary) is rendered
// and appended to cached stable lines. Already-rendered stable lines are never
// re-rendered, preventing visible text from shifting during streaming.
//
// Both fragments are rendered via renderMarkdownContentRaw (no trailing-blank
// trimming) so that inter-block separator lines survive the split. A single
// trimTrailingBlankLines pass on the merged result produces output identical
// to a single-pass renderMarkdownContent call.
func renderStreamingEntry(content string, width int, th *theme.Theme, cache *codeBlockCache, state *streamRenderState) ([]string, []CodeRegion) {
	// Guard: if content shrank below the cached stable boundary (e.g., stream
	// retry reset raced with render), reset the state to avoid slice panic.
	if state.lastStableLen > len(content) {
		state.lastStableLen = 0
		state.stableLines = nil
		state.stableRegions = nil
	}

	boundary := findStableBoundary(content)

	// If stable prefix grew, render only the newly promoted segment and
	// append to the cached stable output. Since stable boundaries are at
	// paragraph-separating blank lines outside code fences, each segment
	// is a complete markdown block that renders independently.
	if boundary > state.lastStableLen && boundary > 0 {
		segment := content[state.lastStableLen:boundary]
		bodyStyle := th.AgentMessage
		segLines, segRegions := renderMarkdownContentRaw(segment, width, bodyStyle, th, cache)

		// Offset segment regions to account for existing stable lines.
		baseOffset := len(state.stableLines)
		for i := range segRegions {
			segRegions[i].Start += baseOffset
			segRegions[i].End += baseOffset
		}

		state.stableLines = append(state.stableLines, segLines...)
		state.stableRegions = append(state.stableRegions, segRegions...)
		state.lastStableLen = boundary
	}

	// Render trailing tail (the incomplete part after the stable boundary).
	var tailLines []string
	var tailRegions []CodeRegion
	tail := content[state.lastStableLen:]
	if strings.TrimSpace(tail) != "" {
		bodyStyle := th.AgentMessage
		tailLines, tailRegions = renderMarkdownContentRaw(tail, width, bodyStyle, th, cache)
	}

	// Merge stable + tail, then trim trailing blanks once (matching
	// the single-pass renderMarkdownContent behavior).
	merged := make([]string, 0, len(state.stableLines)+len(tailLines))
	merged = append(merged, state.stableLines...)
	merged = append(merged, tailLines...)
	merged = trimTrailingBlankLines(merged)

	// Merge regions with offset adjustment for tail regions.
	stableLineCount := len(state.stableLines)
	regions := make([]CodeRegion, 0, len(state.stableRegions)+len(tailRegions))
	regions = append(regions, state.stableRegions...)
	for _, r := range tailRegions {
		regions = append(regions, CodeRegion{
			Start:   r.Start + stableLineCount,
			End:     r.End + stableLineCount,
			Content: r.Content,
		})
	}

	return merged, regions
}

// renderStreamingEntryFull wraps the incremental content render with the
// standard entry header, thinking summary, and trailing spacer.
// For thinking-phase entries (no content yet), delegates to standard RenderEntry.
// When content is present but the stream is still active, a status footer shows
// the current progress message so the user sees that work continues.
func renderStreamingEntryFull(entry *ChatEntry, width int, th *theme.Theme, cache *codeBlockCache, state *streamRenderState) ([]string, []CodeRegion) {
	// During thinking phase, use standard render.
	if entry.Content == "" && entry.ThinkingText != "" {
		return RenderEntry(entry, width, th, cache)
	}

	if width <= 0 {
		return nil, nil
	}

	badge := renderBadge(entry, th)
	timestamp := entry.Timestamp.Format(badgeTimestampFormat)
	header := badge + " " + lipgloss.NewStyle().Foreground(th.Palette.Muted).Render(timestamp)

	var summaryLines []string
	if entry.ThinkingElapsed > 0 {
		summaryStyle := lipgloss.NewStyle().Foreground(th.Palette.Muted).Italic(true)
		summaryText := fmt.Sprintf("%s thought for %.1fs", thinkingSummaryGlyph, entry.ThinkingElapsed.Seconds())
		summaryLines = wrapLine(summaryText, width, summaryStyle)
	}

	// Inline tool call visualization (mirrors RenderEntry phase 2b).
	toolCallLines, toolCallRegions := renderToolCalls(entry.ToolCalls, width, th)

	contentLines, codeRegions := renderStreamingEntry(entry.Content, width, th, cache, state)

	// Streaming status footer: when content is present but the stream is
	// still active, show the spinner + progress message below the content.
	// This gives the user visual feedback during long tool-processing gaps
	// (e.g. multi-turn planning protocol where no text chunks arrive for
	// minutes while tools execute).
	var statusLines []string
	if entry.Streaming && entry.ThinkingText != "" {
		color := th.Palette.Info
		if entry.ThinkingColor != "" {
			color = lipgloss.Color(entry.ThinkingColor)
		}
		statusStyle := lipgloss.NewStyle().Foreground(color).Italic(true)
		statusText := normalizeThinkingLine(entry.ThinkingText)
		if status := strings.TrimSpace(entry.ThinkingStatus); status != "" {
			mdLines, _ := renderMarkdownContent(status, width, statusStyle, th, nil)
			if len(mdLines) > 0 {
				statusText += "  " + mdLines[0]
			}
		}
		statusLines = []string{"", statusStyle.Render(truncateToWidth(statusText, width))}
	}

	// Pre-allocate: 1 header + summary + tool calls + content + status + 1 trailing spacer.
	lines := make([]string, 0, 2+len(summaryLines)+len(toolCallLines)+len(contentLines)+len(statusLines))
	lines = append(lines, header)
	lines = append(lines, summaryLines...)
	lines = append(lines, toolCallLines...)
	lines = append(lines, contentLines...)
	lines = append(lines, statusLines...)
	lines = append(lines, "")

	// Offset tool call region indices to account for header + summary.
	tcOffset := headerLines + len(summaryLines)
	for i := range toolCallRegions {
		toolCallRegions[i].Start += tcOffset
		toolCallRegions[i].End += tcOffset
	}
	entry.ToolCallRegions = toolCallRegions

	// Offset code region indices to account for header + summary + tool calls.
	headerOffset := headerLines + len(summaryLines) + len(toolCallLines)
	for i := range codeRegions {
		codeRegions[i].Start += headerOffset
		codeRegions[i].End += headerOffset
	}
	return lines, codeRegions
}
