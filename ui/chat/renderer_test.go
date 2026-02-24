package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
)

func TestRenderEntry_ThinkingPhaseWrapsLongText(t *testing.T) {
	entry := &ChatEntry{
		ID:           "e1",
		Timestamp:    time.Now(),
		Source:       SourceAgent,
		AgentType:    "guide",
		Streaming:    true,
		Content:      "",
		ThinkingText: "Consulting docs...\nwith multiline thought updates that should wrap properly",
	}

	lines, _ := RenderEntry(entry, 24, theme.DefaultDark(), nil)
	// Header (1) + wrapped thinking lines (>1) + spacer (1).
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (header + wrapped thinking + spacer), got %d", len(lines))
	}
	for i, line := range lines {
		if strings.Contains(line, "\n") || strings.Contains(line, "\r") {
			t.Fatalf("line %d contains raw newline: %q", i, line)
		}
	}
}

func TestRenderEntry_ThinkingPhaseNormalizesWhitespace(t *testing.T) {
	entry := &ChatEntry{
		ID:           "e2",
		Timestamp:    time.Now(),
		Source:       SourceAgent,
		AgentType:    "guide",
		Streaming:    true,
		Content:      "",
		ThinkingText: "line one\r\nline\t two",
	}

	lines, _ := RenderEntry(entry, 80, theme.DefaultDark(), nil)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + thinking + spacer), got %d", len(lines))
	}
	if !strings.Contains(lines[1], "line one line two") {
		t.Fatalf("expected normalized whitespace in thinking line, got %q", lines[1])
	}
}

// TestStreamRenderHeightMatch demonstrates a height mismatch between the
// standard renderContent path and the streaming renderStreamingEntry path.
//
// The streaming path splits content at blank-line boundaries, renders the
// stable prefix and trailing tail independently, and merges them. Because
// renderMarkdownContent trims trailing empty lines from each fragment, the
// blank line(s) at the split boundary are lost. The merged output therefore
// has fewer lines than the single-pass render, causing the viewport height
// index to clip content.
func TestStreamRenderHeightMatch(t *testing.T) {
	th := theme.DefaultDark()
	width := 80
	bodyStyle := th.AgentMessage

	content := "Nice — landing pages are good for RSCs.\n" +
		"\n" +
		"A few things:\n" +
		"\n" +
		"**Content & structure:**\n" +
		"- What's the product?\n" +
		"- Do you have the content already?\n" +
		"\n" +
		"**The linked page:**\n" +
		"- What does the second page contain?\n" +
		"\n" +
		"**Interactivity:**\n" +
		"- Any interactive elements?\n"

	// Standard path: single-pass render of the full content.
	standardLines, _ := renderContent(content, width, bodyStyle, th, nil)

	// Streaming path: incremental render via renderStreamingEntry.
	state := &streamRenderState{}
	streamLines, _ := renderStreamingEntry(content, width, th, nil, state)

	t.Logf("standard path produced %d lines", len(standardLines))
	t.Logf("streaming path produced %d lines", len(streamLines))

	// Log all lines from both paths for comparison.
	maxLen := len(standardLines)
	if len(streamLines) > maxLen {
		maxLen = len(streamLines)
	}
	for i := 0; i < maxLen; i++ {
		stdLine := ""
		if i < len(standardLines) {
			stdLine = standardLines[i]
		}
		stmLine := ""
		if i < len(streamLines) {
			stmLine = streamLines[i]
		}
		match := "  "
		if stdLine != stmLine {
			match = "!="
		}
		t.Logf("[%2d] %s std=%q", i, match, stdLine)
		if stdLine != stmLine {
			t.Logf("[%2d] %s stm=%q", i, match, stmLine)
		}
	}

	// Identify which lines from the standard output are missing in stream output.
	if len(standardLines) != len(streamLines) {
		streamSet := make(map[int]string, len(streamLines))
		for i, l := range streamLines {
			streamSet[i] = l
		}

		// Walk through standard lines and find which ones are absent
		// at the corresponding position in the stream output.
		var missing []int
		si := 0
		for i, stdLine := range standardLines {
			if si < len(streamLines) && streamLines[si] == stdLine {
				si++
			} else {
				missing = append(missing, i)
			}
		}
		if len(missing) > 0 {
			t.Logf("lines present in standard but missing in streaming output: %v", missing)
			for _, idx := range missing {
				t.Logf("  missing line [%d]: %q", idx, standardLines[idx])
			}
		}
	}

	if len(standardLines) != len(streamLines) {
		t.Fatalf("height mismatch: standard path produced %d lines, streaming path produced %d lines",
			len(standardLines), len(streamLines))
	}
}
