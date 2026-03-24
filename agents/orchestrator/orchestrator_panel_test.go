package orchestrator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/events"
	"github.com/adalundhe/sylk/core/versioning"
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

func TestPublishTaskDraftMergeSuccess_PublishesTUIResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	respCh := make(chan *guide.RouteResponse, 1)
	sub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		resp, ok := msg.GetRouteResponse()
		if !ok || resp == nil {
			return nil
		}
		select {
		case respCh <- resp:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tui responses: %v", err)
	}
	defer sub.Unsubscribe()

	o := &Orchestrator{
		bus:         bus,
		activityPub: &trackingActivityPub{},
		config:      Config{AgentID: "orchestrator", SessionID: "sess-1"},
	}
	o.publishTaskDraftMergeSuccess(&TaskRecord{
		ID:        "task-1",
		Name:      "Hello CLI",
		SessionID: "sess-1",
	}, versioning.SemanticVersion{Major: 1, Minor: 2})

	select {
	case resp := <-respCh:
		if !resp.Success {
			t.Fatalf("route response success = false, want true")
		}
		if resp.RespondingAgentID != "orchestrator" {
			t.Fatalf("responding_agent_id = %q, want orchestrator", resp.RespondingAgentID)
		}
		if got, _ := resp.Data.(string); got == "" {
			t.Fatalf("response data = %#v, want non-empty text", resp.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TUI route response")
	}
}

func TestPublishTaskDraftMergeStarted_PublishesTUIResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	t.Cleanup(func() { _ = bus.Close() })

	respCh := make(chan *guide.RouteResponse, 1)
	sub, err := bus.SubscribeAsync(guide.TopicResponses("tui", "tui"), func(msg *guide.Message) error {
		resp, ok := msg.GetRouteResponse()
		if !ok || resp == nil {
			return nil
		}
		select {
		case respCh <- resp:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tui responses: %v", err)
	}
	defer sub.Unsubscribe()

	o := &Orchestrator{
		bus:    bus,
		config: Config{AgentID: "orchestrator", SessionID: "sess-1"},
	}
	o.publishTaskDraftMergeStarted(&TaskRecord{
		ID:        "task-2",
		Name:      "Session Recovery",
		SessionID: "sess-1",
	})

	select {
	case resp := <-respCh:
		if !resp.Success {
			t.Fatalf("route response success = false, want true")
		}
		if got, _ := resp.Data.(string); got == "" {
			t.Fatalf("response data = %#v, want non-empty text", resp.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TUI route response")
	}
}
