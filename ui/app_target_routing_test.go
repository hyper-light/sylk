package ui

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	agentpkg "github.com/adalundhe/sylk/ui/agent"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestAppModelResolveSubmitTarget(t *testing.T) {
	app := &AppModel{}

	if got := app.resolveSubmitTarget("architect"); got != "architect" {
		t.Fatalf("resolveSubmitTarget(explicit) = %q, want architect", got)
	}
	if got := app.resolveSubmitTarget(""); got != "architect" {
		t.Fatalf("resolveSubmitTarget(fallback) = %q, want architect", got)
	}
	if got := app.resolveSubmitTarget("guide"); got != "guide" {
		t.Fatalf("resolveSubmitTarget(override) = %q, want guide", got)
	}
}

func TestAppModelDispatchChordCycleSetsManualTarget(t *testing.T) {
	agentPanel := agentpkg.New(theme.DefaultDark())
	agentPanel.SetFocused(true)
	pushAgentPanelActivity(agentPanel, "guide")
	pushAgentPanelActivity(agentPanel, "architect")
	agentPanel.SelectByID("guide")

	app := &AppModel{agentPanel: agentPanel}
	app.syncManualTargetFromAgentSelection()
	if app.manualTargetAgent != "guide" {
		t.Fatalf("initial manual target = %q, want guide", app.manualTargetAgent)
	}

	_ = app.dispatchChordCycle(chordAgent, 1)
	if app.manualTargetAgent != "architect" {
		t.Fatalf("manual target after chord cycle = %q, want architect", app.manualTargetAgent)
	}
}

func pushAgentPanelActivity(panel *agentpkg.Model, agentID string) {
	if panel == nil {
		return
	}
	_, _ = panel.Update(msg.ActivityEventMsg{
		Event: &events.ActivityEvent{
			ID:        "evt_" + agentID,
			EventType: events.EventTypeLLMRequest,
			Timestamp: time.Now(),
			AgentID:   agentID,
			Content:   "active",
			Data: map[string]any{
				"agent_name": agentID,
				"agent_type": agentID,
			},
		},
	})
}
