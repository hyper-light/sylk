package agent

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/ui/msg"
	"github.com/adalundhe/sylk/ui/theme"
)

func TestModel_SelectedAgentID(t *testing.T) {
	model := New(theme.DefaultDark())
	model.SetFocused(true)
	if got := model.SelectedAgentID(); got != "" {
		t.Fatalf("SelectedAgentID() = %q, want empty", got)
	}

	pushAgentActivity(model, "guide")
	pushAgentActivity(model, "architect")

	if got := model.SelectedAgentID(); got != "guide" {
		t.Fatalf("SelectedAgentID() = %q, want guide", got)
	}

	model.CycleNext()
	if got := model.SelectedAgentID(); got != "architect" {
		t.Fatalf("SelectedAgentID() after cycle = %q, want architect", got)
	}
}

func pushAgentActivity(model *Model, agentID string) {
	if model == nil {
		return
	}
	_, _ = model.Update(msg.ActivityEventMsg{
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
