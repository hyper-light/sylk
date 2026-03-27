package ui

import (
	"strings"
	"testing"
	"time"

	chatpkg "github.com/adalundhe/sylk/ui/chat"
	tea "github.com/charmbracelet/bubbletea"
)

func TestHandleChatClick_TargetsIndividualChildToolCalls(t *testing.T) {
	app := newChildToolClickTargetAppWithStreaming(t, false)

	x := app.chatPanelX() + 2
	firstLine := findRenderedChatLine(t, app.chat.View(), "read_file")
	firstY := 1 + firstLine
	if cmd := app.handleChatClick(x, firstY); cmd != nil {
		t.Fatalf("first click command = %v, want nil", cmd)
	}

	entry := app.chat.EntryAtViewLine(firstLine)
	if entry == nil || !entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[0].Expanded {
		t.Fatalf("expected first child tool call expanded after click, got %+v", entry)
	}

	secondLine := findRenderedChatLine(t, app.chat.View(), "grep")
	secondY := 1 + secondLine
	if cmd := app.handleChatClick(x, secondY); cmd != nil {
		t.Fatalf("second click command = %v, want nil", cmd)
	}

	entry = app.chat.EntryAtViewLine(secondLine)
	if entry == nil || !entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[1].Expanded {
		t.Fatalf("expected second child tool call expanded after click, got %+v", entry)
	}
}

func TestHandleChatClick_TargetsIndividualChildToolCallsWhileParentStreaming(t *testing.T) {
	app := newChildToolClickTargetAppWithStreaming(t, true)

	x := app.chatPanelX() + 2
	firstLine := findRenderedChatLine(t, app.chat.View(), "read_file")
	firstY := 1 + firstLine
	if cmd := app.handleChatClick(x, firstY); cmd != nil {
		t.Fatalf("first click command = %v, want nil", cmd)
	}

	entry := app.chat.EntryAtViewLine(firstLine)
	if entry == nil || !entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[0].Expanded {
		t.Fatalf("expected first child tool call expanded after click, got %+v", entry)
	}

	secondLine := findRenderedChatLine(t, app.chat.View(), "grep")
	secondY := 1 + secondLine
	if cmd := app.handleChatClick(x, secondY); cmd != nil {
		t.Fatalf("second click command = %v, want nil", cmd)
	}

	entry = app.chat.EntryAtViewLine(secondLine)
	if entry == nil || !entry.ToolCalls[0].InterAgent.Children[0].ToolCalls[1].Expanded {
		t.Fatalf("expected second child tool call expanded after click, got %+v", entry)
	}
}

func TestHandleChatClick_TargetsExpandedOverflowChildToolRowsIndividually(t *testing.T) {
	app := newOverflowChildToolClickTargetApp(t, false)
	before := app.View()

	x := app.chatPanelX() + 2
	overflowLine := findRenderedChatLine(t, app.chat.View(), "earlier events")
	overflowY := 1 + overflowLine
	if cmd := app.handleChatClick(x, overflowY); cmd != nil {
		t.Fatalf("overflow click command = %v, want nil", cmd)
	}

	entry := app.chat.EntryAtViewLine(overflowLine)
	if entry == nil || !entry.ToolCalls[0].InterAgent.Children[0].ToolCallsExpanded {
		t.Fatalf("expected overflow click to expand child rows, got %+v", entry)
	}
	expandedView := app.View()
	if before == expandedView {
		t.Fatal("expected app view to rerender after overflow expansion click")
	}
	if !strings.Contains(expandedView, "read_file") {
		t.Fatalf("expected expanded overflow view to include earlier child tool rows, got %q", expandedView)
	}

	targetLine := findRenderedChatLine(t, app.chat.View(), "grep")
	targetY := 1 + targetLine
	if cmd := app.handleChatClick(x, targetY); cmd != nil {
		t.Fatalf("child row click command = %v, want nil", cmd)
	}

	entry = app.chat.EntryAtViewLine(targetLine)
	if entry == nil {
		t.Fatal("expected entry after overflow child row click")
	}
	childCalls := entry.ToolCalls[0].InterAgent.Children[0].ToolCalls
	if !childCalls[1].Expanded {
		t.Fatalf("expected grep child tool row expanded, got %+v", childCalls)
	}
	if childCalls[len(childCalls)-1].Expanded {
		t.Fatalf("expected last child tool row to stay collapsed, got %+v", childCalls)
	}
}

func TestHandleChatClick_TargetsSpecificChildOverflowBranch(t *testing.T) {
	app := newMultiChildOverflowClickTargetApp(t)

	overflowLines := findRenderedChatLines(t, app.chat.View(), "earlier events")
	if len(overflowLines) != 2 {
		t.Fatalf("expected two child overflow rows before click, got %d", len(overflowLines))
	}

	x := app.chatPanelX() + 2
	firstOverflowY := 1 + overflowLines[0]
	if cmd := app.handleChatClick(x, firstOverflowY); cmd != nil {
		t.Fatalf("overflow click command = %v, want nil", cmd)
	}

	entry := app.chat.EntryAtViewLine(overflowLines[0])
	if entry == nil || len(entry.ToolCalls) != 1 || entry.ToolCalls[0].InterAgent == nil {
		t.Fatalf("expected consult entry after overflow click, got %+v", entry)
	}
	children := entry.ToolCalls[0].InterAgent.Children
	if !children[0].ToolCallsExpanded {
		t.Fatalf("expected first child overflow to expand, got %+v", children)
	}
	if children[1].ToolCallsExpanded {
		t.Fatalf("expected second child overflow to remain collapsed, got %+v", children)
	}
	if got := len(findRenderedChatLines(t, app.chat.View(), "earlier events")); got != 1 {
		t.Fatalf("expected one remaining overflow row after first child expansion, got %d", got)
	}
}

func TestHandleChatClick_TargetsOverflowForLaterToolCall(t *testing.T) {
	app := newLaterToolCallOverflowClickTargetApp(t)

	x := app.chatPanelX() + 2
	overflowLine := findRenderedChatLine(t, app.chat.View(), "earlier events")
	overflowY := 1 + overflowLine
	if cmd := app.handleChatClick(x, overflowY); cmd != nil {
		t.Fatalf("overflow click command = %v, want nil", cmd)
	}

	entry := app.chat.EntryAtViewLine(overflowLine)
	if entry == nil || len(entry.ToolCalls) < 2 || entry.ToolCalls[1].InterAgent == nil || !entry.ToolCalls[1].InterAgent.Children[0].ToolCallsExpanded {
		t.Fatalf("expected overflow click on later tool call to expand child rows, got %+v", entry)
	}
}

func TestHandleChatClick_TargetsChildToolForLaterToolCall(t *testing.T) {
	app := newLaterToolCallChildClickTargetApp(t)

	x := app.chatPanelX() + 2
	targetLine := findRenderedChatLine(t, app.chat.View(), "grep")
	targetY := 1 + targetLine
	if cmd := app.handleChatClick(x, targetY); cmd != nil {
		t.Fatalf("child row click command = %v, want nil", cmd)
	}

	entry := app.chat.EntryAtViewLine(targetLine)
	if entry == nil || len(entry.ToolCalls) < 2 || entry.ToolCalls[1].InterAgent == nil {
		t.Fatalf("expected later-tool-call consult entry after child row click, got %+v", entry)
	}
	childCalls := entry.ToolCalls[1].InterAgent.Children[0].ToolCalls
	if !childCalls[1].Expanded {
		t.Fatalf("expected grep child tool row in later tool call expanded, got %+v", childCalls)
	}
	if childCalls[0].Expanded {
		t.Fatalf("expected read_file child tool row to stay collapsed, got %+v", childCalls)
	}
}

func TestHandleChatClick_RerendersChatPanelAfterChildToggle(t *testing.T) {
	app := newChildToolClickTargetAppWithStreaming(t, false)
	before := app.View()

	x := app.chatPanelX() + 2
	firstLine := findRenderedChatLine(t, app.chat.View(), "read_file")
	firstY := 1 + firstLine
	if cmd := app.handleChatClick(x, firstY); cmd != nil {
		t.Fatalf("first click command = %v, want nil", cmd)
	}

	after := app.View()
	if before == after {
		t.Fatal("expected app view to rerender after child tool row click")
	}
	if !strings.Contains(after, "args - path=ui/chat/model.go") {
		t.Fatalf("expected rerendered app view to show expanded child tool details, got %q", after)
	}
}

func findRenderedChatLine(t *testing.T, rendered, needle string) int {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	for idx, line := range lines {
		if strings.Contains(line, needle) {
			return idx
		}
	}
	t.Fatalf("rendered chat view missing %q in %q", needle, rendered)
	return -1
}

func findRenderedChatLines(t *testing.T, rendered, needle string) []int {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	out := make([]int, 0, len(lines))
	for idx, line := range lines {
		if strings.Contains(line, needle) {
			out = append(out, idx)
		}
	}
	if len(out) == 0 {
		t.Fatalf("rendered chat view missing %q in %q", needle, rendered)
	}
	return out
}

func newChildToolClickTargetApp(t *testing.T) *AppModel {
	t.Helper()
	return newChildToolClickTargetAppWithStreaming(t, false)
}

func newChildToolClickTargetAppWithStreaming(t *testing.T, streaming bool) *AppModel {
	t.Helper()
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 220, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	if !app.isChatVisible() {
		t.Fatal("expected chat panel to be visible in test layout")
	}
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:        "child-tool-click-targets",
		Timestamp: time.Now(),
		Source:    chatpkg.SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		Height:    -1,
		Streaming: streaming,
		ThinkingText: func() string {
			if streaming {
				return "⠙ 1.2s"
			}
			return ""
		}(),
		ThinkingStatus: func() string {
			if streaming {
				return "Refining the patch plan..."
			}
			return ""
		}(),
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     chatpkg.InterAgentToolDone,
					Children: []chatpkg.InterAgentChildActivity{
						{
							CorrelationID: "child-academic",
							AgentType:     "academic",
							ToolCalls: []chatpkg.ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "path=ui/chat/model.go",
									FullArgs:    `{"path":"ui/chat/model.go","start_line":1}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
								{
									ToolName:    "grep",
									ArgsSummary: "\"tool_call_key\"",
									FullArgs:    `{"pattern":"tool_call_key","path":"ui/chat/model.go"}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})
	app.recalcLayout()
	_ = app.View()
	return app
}

func newOverflowChildToolClickTargetApp(t *testing.T, streaming bool) *AppModel {
	t.Helper()
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 220, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	if !app.isChatVisible() {
		t.Fatal("expected chat panel to be visible in test layout")
	}
	childCalls := []chatpkg.ToolCallRecord{
		{
			ToolName:    "read_file",
			ArgsSummary: "path=ui/chat/model.go",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
		{
			ToolName:    "grep",
			ArgsSummary: "\"tool_call_key\"",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
		{
			ToolName:    "sed",
			ArgsSummary: "ui/chat/model.go",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
		{
			ToolName:    "apply_patch",
			ArgsSummary: "ui/chat/model.go",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
		{
			ToolName:    "gofmt",
			ArgsSummary: "ui/chat/model.go",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
	}
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:           "child-tool-click-overflow",
		Timestamp:    time.Now(),
		Source:       chatpkg.SourceAgent,
		AgentType:    "architect",
		Content:      "Refining the patch plan.",
		Height:       -1,
		Streaming:    streaming,
		ThinkingText: "",
		ThinkingStatus: func() string {
			if streaming {
				return "Refining the patch plan..."
			}
			return ""
		}(),
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolName: "consult_academic_approach",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     chatpkg.InterAgentToolDone,
					Children: []chatpkg.InterAgentChildActivity{
						{
							CorrelationID: "child-academic-overflow",
							AgentType:     "academic",
							ToolCalls:     childCalls,
							Completed:     true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})
	app.recalcLayout()
	_ = app.View()
	return app
}

func newMultiChildOverflowClickTargetApp(t *testing.T) *AppModel {
	t.Helper()
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 220, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	if !app.isChatVisible() {
		t.Fatal("expected chat panel to be visible in test layout")
	}
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:        "child-tool-click-overflow-multi",
		Timestamp: time.Now(),
		Source:    chatpkg.SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		Height:    -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolName: "consult_research_support",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"librarian", "archivalist"},
					Summary:    "Gathering references and archived context",
					Status:     chatpkg.InterAgentToolDone,
					Children: []chatpkg.InterAgentChildActivity{
						{
							CorrelationID: "child-librarian-overflow-app",
							AgentType:     "librarian",
							Completed:     true,
							ToolCalls: []chatpkg.ToolCallRecord{
								{ToolName: "search_library", ArgsSummary: "migration notes", Completed: true, Success: true},
								{ToolName: "read_file", ArgsSummary: "docs/migration.md", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "\"migration\"", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "docs/migration.md", Completed: true, Success: true},
								{ToolName: "write_notes", ArgsSummary: "migration references", Completed: true, Success: true},
							},
						},
						{
							CorrelationID: "child-archivalist-overflow-app",
							AgentType:     "archivalist",
							Completed:     true,
							ToolCalls: []chatpkg.ToolCallRecord{
								{ToolName: "list_archives", ArgsSummary: "project=sylk", Completed: true, Success: true},
								{ToolName: "read_archive", ArgsSummary: "plan-v3", Completed: true, Success: true},
								{ToolName: "grep", ArgsSummary: "\"handoff\"", Completed: true, Success: true},
								{ToolName: "sed", ArgsSummary: "archive/plan-v3.md", Completed: true, Success: true},
								{ToolName: "write_notes", ArgsSummary: "archived plan", Completed: true, Success: true},
							},
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})
	app.recalcLayout()
	_ = app.View()
	return app
}

func newLaterToolCallChildClickTargetApp(t *testing.T) *AppModel {
	t.Helper()
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 220, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	if !app.isChatVisible() {
		t.Fatal("expected chat panel to be visible in test layout")
	}
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:        "child-tool-click-targets-late",
		Timestamp: time.Now(),
		Source:    chatpkg.SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		Height:    -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolName:    "read_file",
				ArgsSummary: "ui/chat/tool_render.go",
				FullArgs:    `{"path":"ui/chat/tool_render.go","start_line":1}`,
				Output:      "ok",
				StartedAt:   time.Now().Add(-time.Second),
				Completed:   true,
				Success:     true,
			},
			{
				ToolName: "consult_academic_approach",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     chatpkg.InterAgentToolDone,
					Children: []chatpkg.InterAgentChildActivity{
						{
							CorrelationID: "child-academic-late",
							AgentType:     "academic",
							ToolCalls: []chatpkg.ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "path=ui/chat/model.go",
									FullArgs:    `{"path":"ui/chat/model.go","start_line":1}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
								{
									ToolName:    "grep",
									ArgsSummary: "\"tool_call_key\"",
									FullArgs:    `{"pattern":"tool_call_key","path":"ui/chat/model.go"}`,
									StartedAt:   time.Now().Add(-time.Second),
									Completed:   true,
									Success:     true,
								},
							},
							Completed: true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})
	app.recalcLayout()
	_ = app.View()
	return app
}

func newLaterToolCallOverflowClickTargetApp(t *testing.T) *AppModel {
	t.Helper()
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 220, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}
	if !app.isChatVisible() {
		t.Fatal("expected chat panel to be visible in test layout")
	}
	childCalls := []chatpkg.ToolCallRecord{
		{
			ToolName:    "read_file",
			ArgsSummary: "path=ui/chat/model.go",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
		{
			ToolName:    "grep",
			ArgsSummary: "\"tool_call_key\"",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
		{
			ToolName:    "sed",
			ArgsSummary: "ui/chat/model.go",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
		{
			ToolName:    "apply_patch",
			ArgsSummary: "ui/chat/model.go",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
		{
			ToolName:    "gofmt",
			ArgsSummary: "ui/chat/model.go",
			StartedAt:   time.Now().Add(-time.Second),
			Completed:   true,
			Success:     true,
		},
	}
	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:        "child-tool-click-overflow-late",
		Timestamp: time.Now(),
		Source:    chatpkg.SourceAgent,
		AgentType: "architect",
		Content:   "Refining the patch plan.",
		Height:    -1,
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolName:    "read_file",
				ArgsSummary: "ui/chat/tool_render.go",
				FullArgs:    `{"path":"ui/chat/tool_render.go","start_line":1}`,
				Output:      "ok",
				StartedAt:   time.Now().Add(-time.Second),
				Completed:   true,
				Success:     true,
			},
			{
				ToolName: "consult_academic_approach",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Comparing harness options",
					Status:     chatpkg.InterAgentToolDone,
					Children: []chatpkg.InterAgentChildActivity{
						{
							CorrelationID: "child-academic-overflow-late",
							AgentType:     "academic",
							ToolCalls:     childCalls,
							Completed:     true,
						},
					},
				},
				Completed: true,
				Success:   true,
			},
		},
	})
	app.recalcLayout()
	_ = app.View()
	return app
}
