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

func TestViewportHeightStableWithMultilineToolArgs(t *testing.T) {
	history := NewHistory(4)
	history.Push(&ChatEntry{
		ID:        "tool-entry",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "write_file",
				ArgsSummary: "path=README.md\ncontent=line one",
				FullArgs:    "{\n  \"path\": \"README.md\",\n  \"content\": \"line one\\nline two\"\n}",
				Output:      "ok",
				StartedAt:   time.Now().Add(-time.Second),
				Duration:    time.Second,
				Success:     true,
				Completed:   true,
				Expanded:    true,
			},
		},
	})
	history.Push(&ChatEntry{
		ID:        "following-entry",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "inspector",
		Content:   "The following entry should remain visible within the fixed viewport height.",
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(34, 7)

	view := vp.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 7 {
		t.Fatalf("visible line count = %d, want 7; view=%q", len(lines), view)
	}
	if !strings.Contains(view, "following entry") {
		t.Fatalf("viewport did not include following entry within fixed height: %q", view)
	}
}

func TestViewportUsesFullInnerWidthWithoutPrematureWrap(t *testing.T) {
	history := NewHistory(2)
	history.Push(&ChatEntry{
		ID:        "engineer",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "engineer",
		Content:   strings.Repeat("x", 30),
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

func TestViewportReservesRightPaddingInRenderWidth(t *testing.T) {
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

	if got, want := vp.viewWidth, 30; got != want {
		t.Fatalf("viewport content width = %d, want %d after symmetric padding", got, want)
	}

	lines := vp.renderEntry(0)
	if len(lines) != 4 {
		t.Fatalf("rendered line count = %d, want 4 (header + 2 content lines + spacer): %q", len(lines), strings.Join(lines, "\n"))
	}
}

func TestTruncateVisible_PreservesCompleteCSISequences(t *testing.T) {
	line := lipgloss.NewStyle().Foreground(lipgloss.Color("#7dcfff")).Render("└─ ") +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Bold(true).Render("web_search framework benchmarks methodology")

	truncated := truncateVisible(line, 18)
	if got := lipgloss.Width(truncated); got > 18 {
		t.Fatalf("truncateVisible width = %d, want <= 18 for %q", got, truncated)
	}
	if !strings.HasSuffix(truncated, ansiReset) {
		t.Fatalf("expected truncated styled line to end with ANSI reset, got %q", truncated)
	}
	if hasIncompleteCSISequence(truncated) {
		t.Fatalf("truncateVisible returned incomplete CSI sequence: %q", truncated)
	}
}

func TestViewportEntryHeightIncludesThinkingInterAgentRows(t *testing.T) {
	history := NewHistory(2)
	history.Push(&ChatEntry{
		ID:             "inspector-thinking",
		Timestamp:      time.Now(),
		Source:         SourceAgent,
		AgentType:      "inspector",
		Streaming:      true,
		ThinkingText:   "⠋  0.8s",
		ThinkingStatus: "waiting: final inspector validation",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:  "challenge_architect",
				StartedAt: time.Now().Add(-300 * time.Millisecond),
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolChallenge,
					AgentTypes: []string{"architect"},
					Summary:    "testing scope is too weak for this checkpoint and likely needs revision",
					Status:     InterAgentToolPending,
				},
			},
		},
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(72, 8)

	lines := vp.renderEntry(0)
	if got := vp.entryHeight(0); got != len(lines) {
		t.Fatalf("entryHeight = %d, want %d rendered lines", got, len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "architect") {
		t.Fatalf("viewport render missing inter-agent row: %q", joined)
	}
	if !strings.Contains(joined, "waiting: final inspector validation") {
		t.Fatalf("viewport render missing status line: %q", joined)
	}
}

func TestViewportEntryHeightIncludesNestedInterAgentChildLines(t *testing.T) {
	history := NewHistory(3)
	history.Push(&ChatEntry{
		ID:        "architect-nested",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-1",
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     InterAgentToolDone,
					Children: []InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "path=ui/chat/model.go",
									StartedAt:   time.Now().Add(-90 * time.Millisecond),
									Duration:    90 * time.Millisecond,
									Completed:   true,
									Success:     true,
								},
								{
									ToolName:    "rg",
									ArgsSummary: "\"tool_call_key\"",
									StartedAt:   time.Now().Add(-80 * time.Millisecond),
									Duration:    80 * time.Millisecond,
									Completed:   true,
									Success:     true,
								},
							},
							ResultSummary: "A table-driven harness would be cleaner and easier to extend.",
							Completed:     true,
						},
					},
				},
			},
		},
	})
	history.Push(&ChatEntry{
		ID:        "tail-entry",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Tail entry still visible after nested branch height is counted correctly.",
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(76, 8)

	lines := vp.renderEntry(0)
	if got := vp.entryHeight(0); got != len(lines) {
		t.Fatalf("entryHeight = %d, want %d rendered lines", got, len(lines))
	}
	view := vp.View()
	if !strings.Contains(view, "Tail entry still visible") {
		t.Fatalf("expected later entry to remain visible after nested branch render, got %q", view)
	}
}

func TestViewportDeepNestedPendingInterAgentTreeKeepsFollowingEntryVisibleAcrossRerenders(t *testing.T) {
	history := NewHistory(3)
	history.Push(&ChatEntry{
		ID:        "architect-deep-nested-pending",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-academic-1",
				StartedAt:   time.Now().Add(-700 * time.Millisecond),
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare evidence collection approaches",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:  "child-academic-deep-viewport",
							AgentType:      "academic",
							ThinkingStatus: "Delegating benchmark collection.",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-librarian-1",
									StartedAt:   time.Now().Add(-500 * time.Millisecond),
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Find benchmark and methodology sources",
										Status:     InterAgentToolPending,
										Children: []InterAgentChildActivity{
											{
												CorrelationID:  "child-librarian-deep-viewport",
												AgentType:      "librarian",
												ThinkingStatus: "Collecting benchmark sources and methodology notes.",
												ThinkingText:   "⠋  0.5s",
												ThinkingColor:  "#7dcfff",
												ToolCalls: []ToolCallRecord{
													{
														ToolName:    "web_search",
														ArgsSummary: "framework benchmarks methodology",
														StartedAt:   time.Now().Add(-350 * time.Millisecond),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})
	history.Push(&ChatEntry{
		ID:        "tail-after-deep-nested",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		Content:   "Tail entry still visible below the deep nested pending consultation tree.",
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(96, 10)

	for i := 0; i < 6; i++ {
		view := vp.View()
		if !strings.Contains(view, "Tail entry still visible below the deep nested pending consultation tree.") {
			t.Fatalf("rerender %d dropped following entry from viewport: %q", i, view)
		}
	}
}

func TestViewportDeepNestedPendingInterAgentTreeStaysWithinPanelWidth(t *testing.T) {
	history := NewHistory(2)
	history.Push(&ChatEntry{
		ID:        "architect-deep-nested-width",
		Timestamp: time.Now(),
		Source:    SourceAgent,
		AgentType: "architect",
		ToolCalls: []ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-academic-width",
				StartedAt:   time.Now().Add(-600 * time.Millisecond),
				InterAgent: &InterAgentTool{
					Kind:       InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Compare evidence collection approaches",
					Status:     InterAgentToolPending,
					Children: []InterAgentChildActivity{
						{
							CorrelationID:  "child-academic-width",
							AgentType:      "academic",
							ThinkingStatus: "Delegating benchmark collection.",
							ToolCalls: []ToolCallRecord{
								{
									ToolName:    "consult",
									ToolCallKey: "consult-librarian-width",
									StartedAt:   time.Now().Add(-450 * time.Millisecond),
									InterAgent: &InterAgentTool{
										Kind:       InterAgentToolConsult,
										AgentTypes: []string{"librarian"},
										Summary:    "Find benchmark and methodology sources",
										Status:     InterAgentToolPending,
										Children: []InterAgentChildActivity{
											{
												CorrelationID:  "child-librarian-width",
												AgentType:      "librarian",
												ThinkingStatus: "Collecting benchmark sources and methodology notes.",
												ThinkingText:   "⠋  0.5s",
												ThinkingColor:  "#7dcfff",
												ToolCalls: []ToolCallRecord{
													{
														ToolName:    "web_search",
														ArgsSummary: "framework benchmarks methodology",
														StartedAt:   time.Now().Add(-300 * time.Millisecond),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	vp := NewViewport(history, theme.DefaultDark())
	vp.SetSize(70, 10)

	lines := strings.Split(vp.View(), "\n")
	for i, line := range lines {
		if got := lipgloss.Width(line); got > 70 {
			t.Fatalf("viewport line %d width = %d, want <= 70: %q", i, got, line)
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

func hasIncompleteCSISequence(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' {
			continue
		}
		j := i + 1
		if j >= len(s) || s[j] != '[' {
			return true
		}
		j++
		for j < len(s) && !isCSITerminator(s[j]) {
			j++
		}
		if j >= len(s) {
			return true
		}
		i = j
	}
	return false
}
