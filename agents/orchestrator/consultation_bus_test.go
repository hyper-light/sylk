package orchestrator

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

	o := &Orchestrator{
		config: Config{
			AgentID:   "orchestrator",
			SessionID: "sess-1",
		},
		bus:        bus,
		running:    true,
		pendingBus: make(map[string]*shared.PendingSyncWait),
	}

	respSub, err := bus.SubscribeAsync(guide.TopicResponses("orchestrator", "orchestrator"), func(msg *guide.Message) error {
		o.deliverPendingMessage(msg)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe orchestrator responses: %v", err)
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
		return bus.Publish(guide.TopicResponses("orchestrator", "orchestrator"), guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "guardian",
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	ctx, cancel := context.WithTimeout(shared.WithStreamContext(context.Background(), "corr-parent", "tui"), 2*time.Second)
	defer cancel()

	if _, err := o.requestRouteSync(ctx, "guardian", map[string]any{"query": "review"}, nil); err != nil {
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
