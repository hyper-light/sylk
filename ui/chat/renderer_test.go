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

	lines, _ := RenderEntry(entry, 24, theme.DefaultDark())
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

	lines, _ := RenderEntry(entry, 80, theme.DefaultDark())
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + thinking + spacer), got %d", len(lines))
	}
	if !strings.Contains(lines[1], "line one line two") {
		t.Fatalf("expected normalized whitespace in thinking line, got %q", lines[1])
	}
}
