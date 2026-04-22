package guide

import (
	"errors"
	"sync"
	"testing"

	"github.com/adalundhe/sylk/core/events"
)

// fakeBus is a minimal busPublisher implementation that captures every
// Publish call for assertion.
type fakeControlPlaneBus struct {
	mu      sync.Mutex
	calls   []fakeControlPlaneBusCall
	failure error
}

type fakeControlPlaneBusCall struct {
	Topic string
	Msg   *Message
}

func (f *fakeControlPlaneBus) Publish(topic string, msg *Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil {
		return f.failure
	}
	f.calls = append(f.calls, fakeControlPlaneBusCall{Topic: topic, Msg: msg})
	return nil
}

// fakeActivityPublisher captures every ActivityEvent emitted.
type fakeActivityPublisher struct {
	mu     sync.Mutex
	events []*events.ActivityEvent
}

func (f *fakeActivityPublisher) PublishActivity(evt *events.ActivityEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, evt)
	return nil
}

func TestEmitControlPlaneCoordination_AtomicallyEmitsRouteAndSystemEvent(t *testing.T) {
	bus := &fakeControlPlaneBus{}
	pub := &fakeActivityPublisher{}

	err := EmitControlPlaneCoordination(bus, pub, ControlPlaneEmission{
		Kind:          "command_approval_hold",
		Summary:       "DAG abc123 paused · awaiting approval for bash",
		SessionID:     "sess-1",
		CorrelationID: "hold-xyz",
		Related: map[string]any{
			"hold_id":     "hold-xyz",
			"dag_id":      "abc123",
			"pipeline_id": "pipe-1",
		},
		Route: &RouteRequest{
			CorrelationID: "cid-1",
			Input:         "{}",
			TargetAgentID: "orchestrator",
			SessionID:     "sess-1",
		},
	})
	if err != nil {
		t.Fatalf("EmitControlPlaneCoordination: %v", err)
	}

	// One route publish, one activity event.
	if len(bus.calls) != 1 {
		t.Fatalf("expected 1 bus.Publish, got %d", len(bus.calls))
	}
	if bus.calls[0].Topic != TopicGuideRequests {
		t.Errorf("route topic = %q, want %q", bus.calls[0].Topic, TopicGuideRequests)
	}
	routeReq, ok := bus.calls[0].Msg.Payload.(*RouteRequest)
	if !ok {
		t.Fatalf("published message data type = %T, want *RouteRequest", bus.calls[0].Msg.Payload)
	}
	// The helper must stamp control_plane_kind on the route metadata.
	if kind, _ := routeReq.Metadata["control_plane_kind"].(string); kind != "command_approval_hold" {
		t.Errorf("route.Metadata[control_plane_kind] = %q, want %q", kind, "command_approval_hold")
	}

	if len(pub.events) != 1 {
		t.Fatalf("expected 1 activity event, got %d", len(pub.events))
	}
	evt := pub.events[0]
	if evt.EventType != events.EventTypeSystemCoordination {
		t.Errorf("event type = %v, want EventTypeSystemCoordination", evt.EventType)
	}
	if evt.Content != "DAG abc123 paused · awaiting approval for bash" {
		t.Errorf("event content = %q, want the supplied summary", evt.Content)
	}
	if evt.Visibility != events.VisibilityUser {
		t.Errorf("event visibility = %v, want VisibilityUser", evt.Visibility)
	}
	// Related data must be preserved on the event.
	if evt.Data["dag_id"] != "abc123" {
		t.Errorf("event.Data[dag_id] = %v, want abc123", evt.Data["dag_id"])
	}
	if evt.Data["correlation_id"] != "hold-xyz" {
		t.Errorf("event.Data[correlation_id] = %v, want hold-xyz", evt.Data["correlation_id"])
	}
	if evt.Data["control_plane_kind"] != "command_approval_hold" {
		t.Errorf("event.Data[control_plane_kind] = %v, want command_approval_hold", evt.Data["control_plane_kind"])
	}
}

func TestEmitControlPlaneCoordination_SkipsEventWhenPublisherNil(t *testing.T) {
	// When pub is nil (test harness, no UI wired), the route still
	// ships but the activity event is silently dropped.
	bus := &fakeControlPlaneBus{}
	err := EmitControlPlaneCoordination(bus, nil, ControlPlaneEmission{
		Kind:      "plan_handoff_status",
		Summary:   "checking plan status",
		SessionID: "sess-1",
		Route: &RouteRequest{
			CorrelationID: "cid-2",
			Input:         "{}",
			TargetAgentID: "orchestrator",
			SessionID:     "sess-1",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bus.calls) != 1 {
		t.Fatalf("route must still publish when pub is nil; got %d", len(bus.calls))
	}
}

func TestEmitControlPlaneCoordination_RejectsEmptyKind(t *testing.T) {
	bus := &fakeControlPlaneBus{}
	pub := &fakeActivityPublisher{}
	err := EmitControlPlaneCoordination(bus, pub, ControlPlaneEmission{
		Kind:  "   ",
		Route: &RouteRequest{CorrelationID: "cid", Input: "{}", TargetAgentID: "orchestrator"},
	})
	if err == nil {
		t.Fatal("expected error for empty Kind")
	}
	if len(bus.calls) != 0 {
		t.Errorf("expected no publish; got %d", len(bus.calls))
	}
	if len(pub.events) != 0 {
		t.Errorf("expected no activity events; got %d", len(pub.events))
	}
}

func TestEmitControlPlaneCoordination_RejectsNilRoute(t *testing.T) {
	bus := &fakeControlPlaneBus{}
	pub := &fakeActivityPublisher{}
	err := EmitControlPlaneCoordination(bus, pub, ControlPlaneEmission{
		Kind: "command_approval_hold",
	})
	if err == nil {
		t.Fatal("expected error for nil Route")
	}
	if len(bus.calls) != 0 {
		t.Errorf("expected no publish; got %d", len(bus.calls))
	}
}

func TestEmitControlPlaneCoordination_RoutePublishFailureAborts(t *testing.T) {
	bus := &fakeControlPlaneBus{failure: errors.New("bus down")}
	pub := &fakeActivityPublisher{}
	err := EmitControlPlaneCoordination(bus, pub, ControlPlaneEmission{
		Kind:      "command_approval_hold",
		Summary:   "x",
		SessionID: "sess",
		Route:     &RouteRequest{CorrelationID: "cid", Input: "{}", TargetAgentID: "orchestrator"},
	})
	if err == nil {
		t.Fatal("expected error from bus failure")
	}
	// Critical: no activity event emitted if the route failed.
	if len(pub.events) != 0 {
		t.Errorf("activity event must not publish when route publish failed; got %d", len(pub.events))
	}
}
