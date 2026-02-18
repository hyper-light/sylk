package committree

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adalundhe/sylk/ui/theme"
)

// TestRevertToolbarEntersConfirmMode verifies that pressing Enter on the
// Revert toolbar button enters revert mode with Ok auto-focused, rather than
// emitting a RevertRequestMsg.
func TestRevertToolbarEntersConfirmMode(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th)
	m.width = 80
	m.height = 30
	m.mode = viewCommits
	m.nodes = []TreeNode{{Hash: "abc123", ShortHash: "abc123", Subject: "test"}}
	m.selectedIdx = 0

	// Tab to Revert: toolbar is [Back, Reset, Revert, Diff].
	m.cycleCommitToolbar(1) // Back (0)
	m.cycleCommitToolbar(1) // Reset (1)
	m.cycleCommitToolbar(1) // Revert (2)

	// Press Enter on Revert.
	cmd := m.handleCommitKey(tea.KeyMsg{Type: tea.KeyEnter})

	// Must enter revert mode.
	if !m.revertMode {
		t.Fatal("pressing Enter on Revert should enter revert mode")
	}

	// Ok button should be auto-focused.
	if !m.toolbarFocused {
		t.Fatal("Ok button should be auto-focused after entering revert mode")
	}

	left := m.diffToolbarLeftButtons()
	if len(left) < 1 || left[0] != toolbarRevertOk {
		t.Fatalf("expected first left button to be Ok, got %v", left)
	}

	all := append(left, m.toolbarButtons()...)
	if m.toolbarAction >= len(all) || all[m.toolbarAction] != toolbarRevertOk {
		t.Fatalf("toolbarAction should point to Ok (index 0), got %d", m.toolbarAction)
	}

	// Should NOT have emitted RevertRequestMsg.
	if cmd != nil {
		msg := cmd()
		if _, isRevert := msg.(RevertRequestMsg); isRevert {
			t.Fatal("pressing Revert should NOT emit RevertRequestMsg directly")
		}
	}

	// Pressing Enter now (on focused Ok) should emit RevertRequestMsg.
	cmd = m.handleCommitKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("pressing Enter on Ok should return a command")
	}
	msg := cmd()
	if _, isRevert := msg.(RevertRequestMsg); !isRevert {
		t.Fatalf("pressing Enter on Ok should emit RevertRequestMsg, got %T", msg)
	}
}

// TestRevertAbortCancels verifies that pressing Abort exits revert mode
// without emitting RevertRequestMsg.
func TestRevertAbortCancels(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th)
	m.width = 80
	m.height = 30
	m.mode = viewCommits
	m.nodes = []TreeNode{{Hash: "abc123", ShortHash: "abc123", Subject: "test"}}
	m.selectedIdx = 0

	// Tab to Revert and activate.
	m.cycleCommitToolbar(1)
	m.cycleCommitToolbar(1)
	m.cycleCommitToolbar(1)
	m.handleCommitKey(tea.KeyMsg{Type: tea.KeyEnter})

	if !m.revertMode {
		t.Fatal("should be in revert mode")
	}

	// Tab from Ok to Abort.
	m.cycleCommitToolbar(1) // Abort (index 1)

	left := m.diffToolbarLeftButtons()
	all := append(left, m.toolbarButtons()...)
	if m.toolbarAction >= len(all) || all[m.toolbarAction] != toolbarRevertAbort {
		t.Fatalf("expected toolbarAction on Abort, got index %d -> %d", m.toolbarAction, all[m.toolbarAction])
	}

	// Press Enter on Abort.
	cmd := m.handleCommitKey(tea.KeyMsg{Type: tea.KeyEnter})

	if m.revertMode {
		t.Fatal("Abort should exit revert mode")
	}
	if cmd != nil {
		msg := cmd()
		if _, isRevert := msg.(RevertRequestMsg); isRevert {
			t.Fatal("Abort should NOT emit RevertRequestMsg")
		}
	}
}

// TestRevertClickEntersConfirmMode verifies that clicking the Revert toolbar
// button enters revert mode with Ok auto-focused.
func TestRevertClickEntersConfirmMode(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th)
	m.width = 80
	m.height = 30
	m.mode = viewCommits
	m.nodes = []TreeNode{{Hash: "abc123", ShortHash: "abc123", Subject: "test"}}
	m.selectedIdx = 0

	// Compute X position of Revert button (right-aligned).
	buttons := m.toolbarButtons()
	widths := toolbarCellWidths(buttons)
	totalInner := 0
	for _, w := range widths {
		totalInner += w
	}
	groupWidth := len(buttons) + totalInner
	leftPad := max(m.width-groupWidth, 0)

	x := leftPad
	for i := 0; i < 2; i++ {
		x += 1 + widths[i]
	}
	revertX := x + 2 // inside Revert cell

	// Click Revert.
	cmd := m.clickToolbar(revertX, 1)

	if !m.revertMode {
		t.Fatal("clicking Revert should enter revert mode")
	}
	if !m.toolbarFocused {
		t.Fatal("Ok should be auto-focused after clicking Revert")
	}
	if cmd != nil {
		msg := cmd()
		if _, isRevert := msg.(RevertRequestMsg); isRevert {
			t.Fatal("clicking Revert should NOT emit RevertRequestMsg directly")
		}
	}

	// Click Ok (first left button, at X=1).
	cmd = m.clickToolbar(1, 1)
	if cmd == nil {
		t.Fatal("clicking Ok should return a command")
	}
	msg := cmd()
	if _, isRevert := msg.(RevertRequestMsg); !isRevert {
		t.Fatalf("clicking Ok should emit RevertRequestMsg, got %T", msg)
	}
}

// TestRevertEscCancels verifies Esc exits revert mode.
func TestRevertEscCancels(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th)
	m.width = 80
	m.height = 30
	m.mode = viewCommits
	m.nodes = []TreeNode{{Hash: "abc123", ShortHash: "abc123", Subject: "test"}}
	m.selectedIdx = 0

	m.enterRevertMode()
	if !m.revertMode {
		t.Fatal("should be in revert mode")
	}

	m.handleCommitKey(tea.KeyMsg{Type: tea.KeyEscape})

	if m.revertMode {
		t.Fatal("Esc should exit revert mode")
	}
}
