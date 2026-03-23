package ui

import (
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
