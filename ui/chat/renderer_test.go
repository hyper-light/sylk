package chat

import (
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
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

func TestBadgeLabel_HumanizesPipelineCompositeAgentID(t *testing.T) {
	entry := &ChatEntry{
		Source:    SourceAgent,
		AgentType: "task_auth_checkout:inspector-pipeline",
	}

	if got := badgeLabel(entry); got != "Auth Checkout: Inspector" {
		t.Fatalf("badgeLabel = %q, want %q", got, "Auth Checkout: Inspector")
	}
	if got := badgeAgentType(entry); got != "inspector-pipeline" {
		t.Fatalf("badgeAgentType = %q, want %q", got, "inspector-pipeline")
	}
}

func TestBadgeLabel_UsesCanonicalPipelineAgentIDWhenTypeIsSemantic(t *testing.T) {
	entry := &ChatEntry{
		Source:    SourceAgent,
		AgentType: "inspector-pipeline",
		AgentID:   "task_payment_retry:inspector-pipeline",
	}

	if got := badgeLabel(entry); got != "Payment Retry: Inspector" {
		t.Fatalf("badgeLabel = %q, want %q", got, "Payment Retry: Inspector")
	}
}

func TestBadgeLabel_PrefersTaskMetadataOverRawPipelineIdentity(t *testing.T) {
	entry := &ChatEntry{
		Source:    SourceAgent,
		AgentType: "inspector-pipeline",
		AgentID:   "task_12__inspector-pipeline",
		TaskName:  "Payment retry",
		TaskSlug:  "payment_retry",
	}

	if got := badgeLabel(entry); got != "Payment Retry: Inspector" {
		t.Fatalf("badgeLabel = %q, want %q", got, "Payment Retry: Inspector")
	}
	if got := badgeAgentType(entry); got != "inspector-pipeline" {
		t.Fatalf("badgeAgentType = %q, want %q", got, "inspector-pipeline")
	}
}

func TestBadgeLabel_HumanizesLegacyTaskScopedPipelineWorkerID(t *testing.T) {
	entry := &ChatEntry{
		Source:    SourceAgent,
		AgentType: "task_12__inspector-pipeline",
		AgentID:   "task_12__inspector-pipeline",
	}

	if got := badgeLabel(entry); got != "12: Inspector" {
		t.Fatalf("badgeLabel = %q, want %q", got, "12: Inspector")
	}
	if got := badgeAgentType(entry); got != "inspector-pipeline" {
		t.Fatalf("badgeAgentType = %q, want %q", got, "inspector-pipeline")
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

func TestRenderEntry_ToolCallLinesRespectViewportWidth(t *testing.T) {
	entry := &ChatEntry{
		ID:        "inspector-1",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector-pipeline",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "inspect_workspace_state",
				ArgsSummary: `path="src/hello_cli/cli.py" include_tool_output=true`,
				Output:      strings.Repeat("tool output wrap ", 8),
				StartedAt:   time.Now().Add(-1500 * time.Millisecond),
				Duration:    1500 * time.Millisecond,
				Success:     true,
				Completed:   true,
				Expanded:    true,
			},
		},
	}

	const width = 28
	lines, _ := RenderEntry(entry, width, theme.DefaultDark(), nil)
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderEntry_LongPipelineHeaderRespectsViewportWidth(t *testing.T) {
	entry := &ChatEntry{
		ID:             "engineer-1",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "engineer",
		TaskName:       "Very long pipeline task title that would otherwise wrap the engineer badge line",
		TaskSlug:       "very_long_pipeline_task_title_that_would_otherwise_wrap_the_engineer_badge_line",
		Streaming:      true,
		ThinkingText:   "⠋  0.0s",
		ThinkingStatus: "Reviewing task criteria, active test failures, and task-local workspace before implementing changes.",
	}

	const width = 32
	lines, _ := RenderEntry(entry, width, theme.DefaultDark(), nil)
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}

func TestRenderStreamingEntryFull_PipelineStatusFooterRespectsViewportWidth(t *testing.T) {
	entry := &ChatEntry{
		ID:             "pipeline-stream",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "inspector-pipeline",
		AgentID:        "task_auth_checkout:inspector-pipeline",
		TaskName:       "Auth checkout",
		TaskSlug:       "auth-checkout",
		Content:        "Reviewing acceptance criteria before applying the next patch.",
		Streaming:      true,
		ThinkingText:   "Analyzing current pipeline phase",
		ThinkingStatus: "Reviewing task criteria, active test failures, and task-local workspace before implementing changes.",
	}

	const width = 28
	lines, _ := renderStreamingEntryFull(entry, width, theme.DefaultDark(), nil, &streamRenderState{})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Analyzing current pipeline") || !strings.Contains(joined, "phase") {
		t.Fatalf("stream footer missing thinking text: %q", joined)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, line)
		}
	}
}
