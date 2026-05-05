package ui

import (
	"testing"

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
	// Guide means "let classifier decide" — returns "" and clears sticky target.
	if got := app.resolveSubmitTarget("guide"); got != "" {
		t.Fatalf("resolveSubmitTarget(guide) = %q, want empty (classifier decides)", got)
	}
	if app.manualTargetAgent != "" {
		t.Fatalf("manualTargetAgent after guide = %q, want empty", app.manualTargetAgent)
	}
}

func TestAppModelResolveSubmitTarget_EngagedFallback(t *testing.T) {
	app := &AppModel{}
	app.engagedAgentID = "architect"

	// No explicit target and no manualTargetAgent — falls back to engaged agent.
	if got := app.resolveSubmitTarget(""); got != "architect" {
		t.Fatalf("resolveSubmitTarget(engaged fallback) = %q, want architect", got)
	}

	// Engaged to guide is not a fallback — guide means classifier decides.
	app.engagedAgentID = "guide"
	app.manualTargetAgent = ""
	if got := app.resolveSubmitTarget(""); got != "" {
		t.Fatalf("resolveSubmitTarget(engaged=guide) = %q, want empty", got)
	}

	// Explicit @agent overrides engaged agent.
	app.engagedAgentID = "architect"
	if got := app.resolveSubmitTarget("engineer"); got != "engineer" {
		t.Fatalf("resolveSubmitTarget(explicit over engaged) = %q, want engineer", got)
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
	// Selecting guide clears the sticky target — guide means "classifier decides".
	if app.manualTargetAgent != "" {
		t.Fatalf("initial manual target = %q, want empty (guide clears)", app.manualTargetAgent)
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
	_, _ = panel.Update(msg.ClaimsAgentStatusMsg{
		SessionID: "test",
		AgentID:   agentID,
		CycleID:   "cycle_" + agentID,
		Active:    true,
	})
}
