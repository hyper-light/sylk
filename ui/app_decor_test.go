package ui

import (
	"testing"
	"time"

	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/component"
	inputpkg "github.com/adalundhe/sylk/ui/input"
	"github.com/adalundhe/sylk/ui/layout"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestAppDecorDemandIdleForRestingShimmer(t *testing.T) {
	th := theme.DefaultDark()
	app := &AppModel{
		idleFocusGradient:   th.Palette.IdleFocusRingGradient(),
		activeFocusGradient: th.Palette.FocusRingGradient(),
	}

	if got := app.decorDemand(); got != decorCadenceIdle {
		t.Fatalf("decorDemand() = %v, want %v", got, decorCadenceIdle)
	}
}

func TestAppDecorDemandActiveForAgentActivity(t *testing.T) {
	th := theme.DefaultDark()
	agentPanel := agentpkg.New(th)
	pushDecorAgentActivity(agentPanel, "architect")

	app := &AppModel{
		agentPanel:          agentPanel,
		idleFocusGradient:   th.Palette.IdleFocusRingGradient(),
		activeFocusGradient: th.Palette.FocusRingGradient(),
	}

	if got := app.decorDemand(); got != decorCadenceActive {
		t.Fatalf("decorDemand() = %v, want %v", got, decorCadenceActive)
	}
}

func TestApplyFocusRingShimmerSkipsUnfocusedInput(t *testing.T) {
	th := theme.DefaultDark()
	input := inputpkg.New(th, 8)
	input.SetSize(24, 1)
	_ = input.View(false)

	app := &AppModel{
		input:               input,
		focus:               layout.NewFocusManager([]component.FocusID{component.FocusChat}),
		idleFocusGradient:   th.Palette.IdleFocusRingGradient(),
		activeFocusGradient: th.Palette.FocusRingGradient(),
	}

	app.applyFocusRingShimmer(th)

	if input.ViewDirty() {
		t.Fatal("input view marked dirty while unfocused")
	}
}

func TestFocusBorderFrameChangedCoalescesIdlePhase(t *testing.T) {
	th := theme.DefaultDark()
	app := &AppModel{
		idleFocusGradient:     th.Palette.IdleFocusRingGradient(),
		activeFocusGradient:   th.Palette.FocusRingGradient(),
		focusRingStart:        time.Now().Add(-2 * time.Second),
		lastFocusBorderBucket: -1,
	}

	base := app.focusRingStart.Add(2 * time.Second)
	if !app.focusBorderFrameChanged(base) {
		t.Fatal("focusBorderFrameChanged() = false for first idle bucket")
	}
	if app.focusBorderFrameChanged(base.Add(50 * time.Millisecond)) {
		t.Fatal("focusBorderFrameChanged() = true within same idle bucket")
	}
	if !app.focusBorderFrameChanged(base.Add(idleFocusBorderPhaseStep)) {
		t.Fatal("focusBorderFrameChanged() = false after idle bucket boundary")
	}
}

func pushDecorAgentActivity(panel *agentpkg.Model, agentID string) {
	if panel == nil {
		return
	}
	_, _ = panel.Update(msg.ClaimsAgentStatusMsg{
		SessionID: "test",
		AgentID:   agentID,
		CycleID:   "cycle_" + agentID,
		Active:    true,
	})
}
