package ui

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
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
	pushDecorAgentActivity(agentPanel, events.EventTypeLLMRequest, "architect")

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

func pushDecorAgentActivity(panel *agentpkg.Model, eventType events.EventType, agentID string) {
	if panel == nil {
		return
	}
	_, _ = panel.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_" + agentID,
			EventType: eventType,
			Timestamp: time.Now(),
			AgentID:   agentID,
			Content:   "activity",
			Data: map[string]any{
				"agent_name": agentID,
				"agent_type": agentID,
			},
		},
	})
}
