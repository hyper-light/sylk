package orchestrator

import (
	"testing"

	"github.com/adalundhe/sylk/core/events"
)

func TestPublishPipelineAgentRegistration_AdvertisesOncePerTaskAgent(t *testing.T) {
	pub := &trackingActivityPub{}
	o := &Orchestrator{
		config:                  Config{SessionID: "sess-1"},
		activityPub:             pub,
		pipelinePanelState:      make(map[string]pipelinePanelSnapshot),
		pipelinePanelRegistered: make(map[string]struct{}),
	}

	o.publishPipelineAgentRegistration("designer", "task_1", "auth-checkout", "creating_tests")
	o.publishPipelineAgentRegistration("designer", "task_1", "auth-checkout", "creating_tests")
	o.publishPipelineAgentActivity("designer", "task_1", "task_1:test", "auth-checkout", "creating_tests")
	o.publishPipelineAgentRegistration("designer", "task_1", "auth-checkout", "creating_tests")
	o.publishPipelineAgentActivity("designer", "task_1", "task_1:test", "auth-checkout", "creating_tests")
	o.publishPipelineAgentActivity("designer", "task_1", "task_1:execute", "auth-checkout", "executing")

	collected := pub.collected()
	if len(collected) != 3 {
		t.Fatalf("expected 3 activity events, got %d", len(collected))
	}

	if collected[0].EventType != events.EventTypeAgentRegistered {
		t.Fatalf("event[0] type = %v, want agent_registered", collected[0].EventType)
	}
	if collected[1].EventType != events.EventTypeAgentAction {
		t.Fatalf("event[1] type = %v, want agent_action", collected[1].EventType)
	}
	if collected[2].EventType != events.EventTypeAgentAction {
		t.Fatalf("event[2] type = %v, want agent_action", collected[2].EventType)
	}
	if collected[0].AgentID != "task_1:designer" {
		t.Fatalf("event[0] agent_id = %q, want task_1:designer", collected[0].AgentID)
	}
	if got := collected[2].Data["pipeline_status"]; got != "executing" {
		t.Fatalf("event[2] pipeline_status = %v, want executing", got)
	}
}

func TestPublishPipelineAgentEvents_IgnoreMissingPipelineIdentity(t *testing.T) {
	pub := &trackingActivityPub{}
	o := &Orchestrator{
		config:                  Config{SessionID: "sess-1"},
		activityPub:             pub,
		pipelinePanelState:      make(map[string]pipelinePanelSnapshot),
		pipelinePanelRegistered: make(map[string]struct{}),
	}

	o.publishPipelineAgentRegistration("designer", "", "auth-checkout", "creating_tests")
	o.publishPipelineAgentActivity("designer", "", "task_1:test", "auth-checkout", "creating_tests")

	if got := len(pub.collected()); got != 0 {
		t.Fatalf("published %d events, want 0", got)
	}
}
