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
// Both fragments are rendered via renderMarkdownContentRaw (no trailing-blank
// trimming) so that inter-block separator lines survive the split. A single
// trimTrailingBlankLines pass on the merged result produces output identical
// to a single-pass renderMarkdownContent call.
func renderStreamingEntry(content string, width int, th *theme.Theme, cache *codeBlockCache, state *streamRenderState) ([]string, []CodeRegion) {
	boundary := findStableBoundary(content)

	// If stable prefix grew, re-render it (includes newly completed blocks).
	if boundary > state.lastStableLen && boundary > 0 {
		stableContent := content[:boundary]
		bodyStyle := th.AgentMessage
		stableLines, stableRegions := renderMarkdownContentRaw(stableContent, width, bodyStyle, th, cache)
		state.stableLines = stableLines
		state.stableRegions = stableRegions
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

	contentLines, codeRegions := renderStreamingEntry(entry.Content, width, th, cache, state)

	// Pre-allocate: 1 header + summary + content + 1 trailing spacer.
	lines := make([]string, 0, 2+len(summaryLines)+len(contentLines))
	lines = append(lines, header)
	lines = append(lines, summaryLines...)
	lines = append(lines, contentLines...)
	lines = append(lines, "")

	// Offset code region indices to account for the header + summary.
	headerOffset := headerLines + len(summaryLines)
	for i := range codeRegions {
		codeRegions[i].Start += headerOffset
		codeRegions[i].End += headerOffset
	}
	return lines, codeRegions
}
