package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/commandapproval"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestCommandApprovalLayout_MinimalPrompt(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 90, Height: 28}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	app.commandApproval = &commandApprovalState{
		proposal: &commandapproval.Proposal{
			AgentType:    "architect",
			Command:      "pnpm install --filter web --frozen-lockfile",
			WorkingDir:   "/tmp/project",
			PersistLabel: "pnpm install",
			Risk:         "This command is not in the pre-approved command set",
		},
		selected:  0,
		activated: -1,
	}

	layout := app.commandApprovalLayout(60)
	joined := ansi.Strip(strings.Join(layout.lines, "\n"))

	if !strings.Contains(joined, "Architect wants approval for:") {
		t.Fatalf("approval prompt missing requester line:\n%s", joined)
	}
	if !strings.Contains(joined, "pnpm install --filter web --frozen-lockfile") {
		t.Fatalf("approval prompt missing command:\n%s", joined)
	}
	if strings.Contains(joined, "pre-approved command set") {
		t.Fatalf("approval prompt should not show policy rationale:\n%s", joined)
	}
	if strings.Contains(joined, "cwd:") || strings.Contains(joined, "always applies to:") {
		t.Fatalf("approval prompt should omit cwd and persist metadata:\n%s", joined)
	}
	if len(layout.lines) < 3 || strings.TrimSpace(ansi.Strip(layout.lines[1])) != "" || strings.TrimSpace(ansi.Strip(layout.lines[2])) == "" {
		t.Fatalf("approval prompt should keep a single spacer line above the command block:\n%s", joined)
	}
	if !strings.Contains(joined, "• Allow Once (this run)") {
		t.Fatalf("approval prompt missing markdown bullet item:\n%s", joined)
	}
}

func TestCommandApprovalOptionAt_UsesTightHitboxes(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 90, Height: 28}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	app.commandApproval = &commandApprovalState{
		proposal: &commandapproval.Proposal{
			AgentType: "architect",
			Command:   "pnpm install --filter web --frozen-lockfile",
		},
		selected:  0,
		activated: -1,
	}

	layout := app.commandApprovalLayout(60)
	if len(layout.hitboxes) == 0 {
		t.Fatal("expected option hitboxes")
	}
	first := layout.hitboxes[0]

	if idx, ok := app.commandApprovalOptionAt(first.x1-1, first.y); !ok || idx != first.option {
		t.Fatalf("commandApprovalOptionAt() = (%d, %v), want (%d, true)", idx, ok, first.option)
	}
	if idx, ok := app.commandApprovalOptionAt(59, first.y); ok {
		t.Fatalf("commandApprovalOptionAt() outside text span = (%d, true), want no hit", idx)
	}
}

func TestCommandApprovalHeight_PreservesMainArea(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 80, Height: 18}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	app.commandApproval = &commandApprovalState{
		proposal: &commandapproval.Proposal{
			AgentType: "architect",
			Command: strings.Join([]string{
				"pnpm install --filter web",
				"--frozen-lockfile",
				"--ignore-scripts=false",
				"--reporter=append-only",
				"--strict-peer-dependencies",
			}, "\n"),
		},
		selected:  0,
		activated: -1,
	}

	got := app.commandApprovalHeight()
	maxAllowed := app.height - statusBarHeight - mainMinContentHeight
	if got > maxAllowed {
		t.Fatalf("commandApprovalHeight() = %d, want <= %d", got, maxAllowed)
	}
}

func TestCommandApprovalLayout_ClampsCodeBlockToKeepAllOptionsVisible(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 80, Height: 18}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	commandLines := make([]string, 0, 14)
	for i := 1; i <= 14; i++ {
		commandLines = append(commandLines, fmt.Sprintf("echo line-%02d", i))
	}
	app.commandApproval = &commandApprovalState{
		proposal: &commandapproval.Proposal{
			AgentType: "architect",
			Command:   strings.Join(commandLines, "\n"),
		},
		selected:  0,
		activated: -1,
	}

	layout := app.commandApprovalLayout(60)
	if layout.codeVisibleLines <= 0 {
		t.Fatalf("codeVisibleLines = %d, want > 0", layout.codeVisibleLines)
	}
	if layout.codeVisibleLines >= layout.codeTotalLines {
		t.Fatalf("expected clipped code block, visible=%d total=%d", layout.codeVisibleLines, layout.codeTotalLines)
	}
	maxBodyLines := max(app.height-statusBarHeight-mainMinContentHeight-inputBorderSize, 1)
	if got := len(layout.lines); got > maxBodyLines {
		t.Fatalf("layout line count = %d, want <= %d", got, maxBodyLines)
	}

	joined := ansi.Strip(strings.Join(layout.lines, "\n"))
	for _, want := range []string{
		"• Allow Once (this run)",
		"• Allow Always (save allow rule)",
		"• Deny Once (block this run)",
		"• Deny Always (save deny rule)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("approval layout missing %q:\n%s", want, joined)
		}
	}
}

func TestCommandApprovalLayout_CapsCodePreviewViewport(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 40}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	commandLines := make([]string, 0, 24)
	for i := 1; i <= 24; i++ {
		commandLines = append(commandLines, fmt.Sprintf("echo line-%02d", i))
	}
	app.commandApproval = &commandApprovalState{
		proposal: &commandapproval.Proposal{
			AgentType: "architect",
			Command:   strings.Join(commandLines, "\n"),
		},
		selected:  0,
		activated: -1,
	}

	layout := app.commandApprovalLayout(80)
	if layout.codeVisibleLines != commandApprovalMaxVisibleCodeLines {
		t.Fatalf("codeVisibleLines = %d, want %d", layout.codeVisibleLines, commandApprovalMaxVisibleCodeLines)
	}
	if layout.codeVisibleLines >= layout.codeTotalLines {
		t.Fatalf("expected scrollable code block, visible=%d total=%d", layout.codeVisibleLines, layout.codeTotalLines)
	}
}

func TestHandleCommandApprovalMouse_WheelScrollsCodePreview(t *testing.T) {
	app := newResizeTestApp(t)
	if cmd := app.handleResize(tea.WindowSizeMsg{Width: 100, Height: 40}); cmd != nil {
		t.Fatalf("handleResize() command = %v, want nil", cmd)
	}

	commandLines := make([]string, 0, 24)
	for i := 1; i <= 24; i++ {
		commandLines = append(commandLines, fmt.Sprintf("echo line-%02d", i))
	}
	app.commandApproval = &commandApprovalState{
		proposal: &commandapproval.Proposal{
			AgentType: "architect",
			Command:   strings.Join(commandLines, "\n"),
		},
		selected:  0,
		activated: -1,
	}
	app.recalcLayout()

	layout := app.commandApprovalLayout(max(app.width-2, 1))
	if layout.codeVisibleLines >= layout.codeTotalLines {
		t.Fatalf("expected scrollable code block, visible=%d total=%d", layout.codeVisibleLines, layout.codeTotalLines)
	}
	if layout.codeStartY < 0 {
		t.Fatalf("codeStartY = %d, want >= 0", layout.codeStartY)
	}

	inputTop := app.height - app.prevInputH - statusBarHeight
	codeMouseY := inputTop + 1 + layout.codeStartY
	mouse := tea.MouseMsg{X: 2, Y: codeMouseY, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}

	if cmd := app.handleMouse(mouse); cmd != nil {
		t.Fatalf("handleMouse() command = %v, want nil", cmd)
	}
	if app.commandApproval.codeScroll != 1 {
		t.Fatalf("codeScroll after wheel down = %d, want 1", app.commandApproval.codeScroll)
	}

	maxScroll := layout.codeTotalLines - layout.codeVisibleLines
	for range maxScroll + 2 {
		app.handleMouse(mouse)
	}
	if app.commandApproval.codeScroll != maxScroll {
		t.Fatalf("codeScroll at lower bound = %d, want %d", app.commandApproval.codeScroll, maxScroll)
	}

	mouse.Button = tea.MouseButtonWheelUp
	app.handleMouse(mouse)
	if app.commandApproval.codeScroll != max(maxScroll-1, 0) {
		t.Fatalf("codeScroll after wheel up = %d, want %d", app.commandApproval.codeScroll, max(maxScroll-1, 0))
	}
}
