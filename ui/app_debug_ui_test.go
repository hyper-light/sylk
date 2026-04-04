package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chatpkg "github.com/adalundhe/sylk/ui/chat"
	"github.com/adalundhe/sylk/ui/msg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
)

func TestDebugUICommand_CapturesExistingRenderedChatEntries(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	app.chat.PushEntry(&chatpkg.ChatEntry{
		ID:        uuid.NewString(),
		Timestamp: time.Now(),
		Source:    chatpkg.SourceAgent,
		AgentType: "architect",
		Content:   "Reviewing the implementation plan.",
		ToolCalls: []chatpkg.ToolCallRecord{
			{
				ToolName:    "consult_academic_approach",
				ToolCallKey: "consult-1",
				InterAgent: &chatpkg.InterAgentTool{
					Kind:       chatpkg.InterAgentToolConsult,
					AgentTypes: []string{"academic"},
					Summary:    "Checking prior art",
					Status:     chatpkg.InterAgentToolDone,
					Children: []chatpkg.InterAgentChildActivity{
						{
							CorrelationID: "child-academic-1",
							AgentType:     "academic",
							ToolCalls: []chatpkg.ToolCallRecord{
								{
									ToolName:    "read_file",
									ArgsSummary: "docs/plan.md",
									StartedAt:   time.Now().Add(-25 * time.Millisecond),
									Duration:    25 * time.Millisecond,
									Completed:   true,
									Success:     true,
								},
							},
							ResultSummary: "Found a matching implementation note.",
							Completed:     true,
						},
					},
				},
			},
		},
	})

	dir := t.TempDir()
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	model, _ := app.Update(msg.SubmitPromptMsg{Text: "/debug-ui enable"})
	app = model.(*AppModel)

	logPath := filepath.Join(dir, chatDebugLogFilename)
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read chat log: %v", err)
	}
	logText := string(content)
	for _, needle := range []string{
		"Reviewing the implementation plan.",
		"Found a matching implementation note.",
		"academic",
		"read_file",
		"docs/plan.md",
		"UI debug capture started:",
	} {
		if !strings.Contains(logText, needle) {
			t.Fatalf("chat.log missing %q:\n%s", needle, logText)
		}
	}
	if strings.Contains(logText, "\x1b[") {
		t.Fatalf("chat.log should not contain ANSI escapes:\n%q", logText)
	}
}

func TestDebugUICommand_TracksNestedChildUpdatesAndStopsOnToggle(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 110, Height: 34}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	dir := t.TempDir()
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	model, _ := app.Update(msg.SubmitPromptMsg{Text: "/debug-ui enable"})
	app = model.(*AppModel)

	parentCID := "corr-parent-inspector-debug"
	challengeBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: parentCID,
		ParentToolCallKey:   "challenge-1",
		Kind:                "challenge",
	}
	approvalBranch := &msg.InterAgentBranchRefMsg{
		ParentCorrelationID: "corr-child-tester-debug",
		ParentToolCallKey:   "approval-1",
		Kind:                "approval",
	}

	for _, update := range []tea.Msg{
		msg.StreamStartMsg{
			SessionID:     "s1",
			CorrelationID: parentCID,
			AgentID:       "task_1:inspector-pipeline",
			AgentType:     "inspector-pipeline",
			AgentName:     "Pipeline Inspector",
			PipelineID:    "task_1",
			TaskID:        "task_1",
		},
		msg.ToolCallEventMsg{
			SessionID:     "s1",
			CorrelationID: parentCID,
			AgentID:       "task_1:inspector-pipeline",
			AgentType:     "inspector-pipeline",
			AgentName:     "Pipeline Inspector",
			PipelineID:    "task_1",
			TaskID:        "task_1",
			ToolCallKey:   "challenge-1",
			ToolName:      "challenge_agent",
			FullArgs:      `{"target":"tester-pipeline","request":"Validate the implementation against the criteria contract."}`,
			Phase:         0,
			StartedAt:     time.Now().Add(-1200 * time.Millisecond),
			InterAgent: &msg.InterAgentToolEventMsg{
				Kind:       "challenge",
				Status:     "pending",
				AgentTypes: []string{"tester-pipeline"},
				Summary:    "Validate the implementation against the criteria contract.",
				ThreadKey:  "pipeline:task_1-challenge-1",
			},
		},
		msg.StreamStartMsg{
			SessionID:     "s1",
			CorrelationID: "corr-child-tester-debug",
			AgentID:       "task_1:tester-pipeline",
			AgentType:     "tester-pipeline",
			AgentName:     "Pipeline Tester",
			PipelineID:    "task_1",
			TaskID:        "task_1",
			BranchRef:     challengeBranch,
		},
		msg.StreamProgressMsg{
			SessionID:     "s1",
			CorrelationID: "corr-child-tester-debug",
			AgentID:       "task_1:tester-pipeline",
			AgentType:     "tester-pipeline",
			AgentName:     "Pipeline Tester",
			PipelineID:    "task_1",
			TaskID:        "task_1",
			Message:       "Waiting for Guardian approval for run_command",
			BranchRef:     challengeBranch,
		},
		msg.ToolCallEventMsg{
			SessionID:     "s1",
			CorrelationID: "corr-child-tester-debug",
			AgentID:       "task_1:tester-pipeline",
			AgentType:     "tester-pipeline",
			AgentName:     "Pipeline Tester",
			PipelineID:    "task_1",
			TaskID:        "task_1",
			ToolCallKey:   "approval-1",
			ToolName:      "approval_guardian",
			FullArgs:      `{"target":"guardian","tool_name":"run_command","summary":"Waiting for Guardian approval for run_command"}`,
			Phase:         0,
			StartedAt:     time.Now().Add(-900 * time.Millisecond),
			BranchRef:     challengeBranch,
			InterAgent: &msg.InterAgentToolEventMsg{
				Kind:       "approval",
				Status:     "pending",
				AgentTypes: []string{"guardian"},
				Summary:    "Waiting for Guardian approval for run_command",
			},
		},
		msg.StreamStartMsg{
			SessionID:     "s1",
			CorrelationID: "corr-grandchild-guardian-debug",
			AgentID:       "guardian",
			AgentType:     "guardian",
			AgentName:     "Guardian",
			BranchRef:     approvalBranch,
		},
	} {
		model, _ = app.Update(update)
		app = model.(*AppModel)
	}

	logPath := filepath.Join(dir, chatDebugLogFilename)
	beforeStop, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read chat log before stop: %v", err)
	}
	beforeText := string(beforeStop)
	for _, needle := range []string{
		"Validate the implementation against the criteria contract.",
		"Waiting for Guardian approval for run_command",
		"guardian",
	} {
		if !strings.Contains(beforeText, needle) {
			t.Fatalf("chat.log missing %q before stop:\n%s", needle, beforeText)
		}
	}

	model, _ = app.Update(msg.SubmitPromptMsg{Text: "/debug-ui disable"})
	app = model.(*AppModel)

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat chat log: %v", err)
	}
	sizeAfterStop := info.Size()

	model, _ = app.Update(msg.StreamStartMsg{
		SessionID:     "s1",
		CorrelationID: "corr-after-stop",
		AgentID:       "architect",
		AgentType:     "architect",
		AgentName:     "Architect",
	})
	app = model.(*AppModel)

	info, err = os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat chat log after stop: %v", err)
	}
	if got := info.Size(); got != sizeAfterStop {
		t.Fatalf("chat.log size after stop = %d, want %d", got, sizeAfterStop)
	}
}

func TestDebugUICommand_EnableDisableIdempotent(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 32}); cmd != nil {
		t.Fatalf("initial resize command = %v, want nil", cmd)
	}

	dir := t.TempDir()
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	model, _ := app.Update(msg.SubmitPromptMsg{Text: "/debug-ui enable"})
	app = model.(*AppModel)
	if app.chatDebugCapture == nil {
		t.Fatal("expected debug capture enabled")
	}

	model, _ = app.Update(msg.SubmitPromptMsg{Text: "/debug-ui enable"})
	app = model.(*AppModel)
	if app.chatDebugCapture == nil {
		t.Fatal("expected debug capture to remain enabled after redundant enable")
	}
	if rendered := app.chat.View(); !strings.Contains(rendered, "already enabled") {
		t.Fatalf("expected already-enabled system message in chat view, got %q", rendered)
	}

	model, _ = app.Update(msg.SubmitPromptMsg{Text: "/debug-ui disable"})
	app = model.(*AppModel)
	if app.chatDebugCapture != nil {
		t.Fatal("expected debug capture disabled")
	}

	model, _ = app.Update(msg.SubmitPromptMsg{Text: "/debug-ui disable"})
	app = model.(*AppModel)
	if rendered := app.chat.View(); !strings.Contains(rendered, "already disabled") {
		t.Fatalf("expected already-disabled system message in chat view, got %q", rendered)
	}
}

func chdirForTest(t *testing.T, dir string) func() {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	return func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore cwd %s: %v", wd, err)
		}
	}
}
