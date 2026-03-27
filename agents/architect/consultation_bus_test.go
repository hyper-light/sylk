package architect

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
)

func TestRequestRouteSyncInheritsParentCorrelationFromStreamContext(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a := &Architect{
		id:      "architect",
		bus:     bus,
		running: true,
	}

	respSub, err := bus.SubscribeAsync(guide.TopicResponses("architect", "architect"), func(msg *guide.Message) error {
		a.deliverPendingBusMessage(msg)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe architect responses: %v", err)
	}
	defer respSub.Unsubscribe()

	requests := make(chan *guide.RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requests <- req:
		default:
		}
		return bus.Publish(guide.TopicResponses("architect", "architect"), guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "academic",
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	ctx, cancel := context.WithTimeout(shared.WithStreamContext(context.Background(), "corr-parent", "tui"), 2*time.Second)
	defer cancel()

	if _, err := a.requestRouteSync(ctx, &guide.RouteRequest{
		Input:         "consult academic",
		TargetAgentID: "academic",
		SessionID:     "sess-1",
	}); err != nil {
		t.Fatalf("requestRouteSync: %v", err)
	}

	select {
	case req := <-requests:
		if req.ParentCorrelationID != "corr-parent" {
			t.Fatalf("parent_correlation_id = %q, want %q", req.ParentCorrelationID, "corr-parent")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for routed request")
	}
}

func TestRequestConsultationInheritsSessionFromContextWhenUnset(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a := &Architect{
		id:       "architect",
		bus:      bus,
		running:  true,
		steering: shared.NewSteeringManager(),
		knownAgents: map[string]*guide.AgentAnnouncement{
			"academic": {AgentID: "academic"},
		},
	}

	respSub, err := bus.SubscribeAsync(guide.TopicResponses("architect", "architect"), func(msg *guide.Message) error {
		a.deliverPendingBusMessage(msg)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe architect responses: %v", err)
	}
	defer respSub.Unsubscribe()

	requests := make(chan *guide.RouteRequest, 1)
	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requests <- req:
		default:
		}
		return bus.Publish(guide.TopicResponses("architect", "architect"), guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "academic",
			Data:              map[string]any{"ok": true},
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	ctx, cancel := context.WithTimeout(withArchitectSessionID(context.Background(), "sess-live"), 2*time.Second)
	defer cancel()

	if _, err := a.requestConsultation(ctx, "academic", "check the approach", "", ""); err != nil {
		t.Fatalf("requestConsultation: %v", err)
	}

	select {
	case req := <-requests:
		if req.SessionID != "sess-live" {
			t.Fatalf("SessionID = %q, want sess-live", req.SessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for routed request")
	}
}
