package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/ui/theme"
	"github.com/charmbracelet/lipgloss"
)

func newResizeViewport(entryCount int, height int) *Viewport {
	history := NewHistory(entryCount)
	for i := range entryCount {
		label := fmt.Sprintf("entry-%d", i)
		history.Push(&ChatEntry{
			ID:            label,
			RenderedLines: []string{label},
			Height:        1,
		})
	}
	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(80, height)
	return vp
}

func TestViewportShrinkPreservesTopEntryWhileFollowing(t *testing.T) {
	vp := newResizeViewport(6, 4)
	before := vp.EntryAtViewLine(0)
	if before == nil {
		t.Fatal("expected visible top entry before shrink")
	}

	vp.SetSize(80, 3)
	after := vp.EntryAtViewLine(0)
	if after == nil {
		t.Fatal("expected visible top entry after shrink")
	}
	if after.ID != before.ID {
		t.Fatalf("top entry = %q, want %q", after.ID, before.ID)
	}

	vp.history.Push(&ChatEntry{ID: "entry-6", RenderedLines: []string{"entry-6"}, Height: 1})
	vp.OnNewEntry()
	if vp.scrollOff != 0 {
		t.Fatalf("scrollOff = %d, want 0 after new content", vp.scrollOff)
	}
	if vp.layoutCompensation != 0 {
		t.Fatalf("layoutCompensation = %d, want 0 after new content", vp.layoutCompensation)
	}
}

func TestViewportShrinkPreservesTopEntryWhileScrolledBack(t *testing.T) {
	vp := newResizeViewport(6, 4)
	if !vp.ScrollUp() {
		t.Fatal("expected scroll up to succeed")
	}
	before := vp.EntryAtViewLine(0)
	if before == nil {
		t.Fatal("expected visible top entry before shrink")
	}

	vp.SetSize(80, 3)
	after := vp.EntryAtViewLine(0)
	if after == nil {
		t.Fatal("expected visible top entry after shrink")
	}
	if after.ID != before.ID {
		t.Fatalf("top entry = %q, want %q", after.ID, before.ID)
	}
}

func TestViewportPreservesLeadingVisibleSpacerLine(t *testing.T) {
	history := NewHistory(4)
	history.Push(&ChatEntry{
		ID: "tester",
	})
	history.Push(&ChatEntry{
		ID: "engineer",
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(40, 3)
	history.UpdateAt(0, func(entry *ChatEntry) {
		entry.RenderedLines = []string{"tester line 1", "tester line 2", ""}
		entry.Height = 3
	})
	history.UpdateAt(1, func(entry *ChatEntry) {
		entry.RenderedLines = []string{"engineer line 1", "engineer line 2", ""}
		entry.Height = 3
	})
	vp.scrollOff = 1
	vp.following = false

	view := vp.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 3 {
		t.Fatalf("visible line count = %d, want 3", len(lines))
	}
	if strings.TrimSpace(lines[0]) != "" {
		t.Fatalf("first visible line = %q, want preserved spacer line", lines[0])
	}
	if !strings.Contains(lines[1], "engineer line 1") {
		t.Fatalf("second visible line = %q, want engineer line 1", lines[1])
	}
	if !strings.Contains(lines[2], "engineer line 2") {
		t.Fatalf("third visible line = %q, want engineer line 2", lines[2])
	}
}

func TestViewportShowsFollowingEntryAfterWrappedToolOutput(t *testing.T) {
	history := NewHistory(4)
	history.Push(&ChatEntry{
		ID:        "inspector",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector-pipeline",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "inspect_workspace_state",
				ArgsSummary: `path="src/hello_cli/cli.py" include_tool_output=true`,
				Output:      strings.Repeat("tool output wrap ", 8),
				StartedAt:   time.Now().Add(-2 * time.Second),
				Duration:    2 * time.Second,
				Success:     true,
				Completed:   true,
				Expanded:    true,
			},
		},
	})
	history.Push(&ChatEntry{
		ID:        "engineer",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		Content:   "Reviewing task criteria before implementing changes.",
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(28, 6)

	lines := strings.Split(vp.View(), "\n")
	if len(lines) != 6 {
		t.Fatalf("visible line count = %d, want 6", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Reviewing task criteria") {
		t.Fatalf("viewport did not include following engineer entry: %q", joined)
	}
}

func TestViewportUsesFullInnerWidthWithoutPrematureWrap(t *testing.T) {
	history := NewHistory(2)
	history.Push(&ChatEntry{
		ID:        "engineer",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		Content:   strings.Repeat("x", 31),
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(32, 4)

	lines := vp.renderEntry(0)
	if len(lines) != 3 {
		t.Fatalf("rendered line count = %d, want 3 (header + content + spacer): %q", len(lines), strings.Join(lines, "\n"))
	}

	viewLines := strings.Split(vp.View(), "\n")
	for i, line := range viewLines {
		if got := lipgloss.Width(line); got > 32 {
			t.Fatalf("viewport line %d width = %d, want <= 32: %q", i, got, line)
		}
	}
}

func TestViewportActiveStreamViewStaysWithinPanelWidth(t *testing.T) {
	history := NewHistory(2)
	history.Push(&ChatEntry{
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
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(32, 8)
	vp.AddStreamState(0, &streamRenderState{})

	viewLines := strings.Split(vp.View(), "\n")
	for i, line := range viewLines {
		if got := lipgloss.Width(line); got > 32 {
			t.Fatalf("viewport line %d width = %d, want <= 32: %q", i, got, line)
		}
	}
}
