package academic

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/versioning"
)

func TestAcademicRequestConsultSync_UsesNestedChildStreamActivity(t *testing.T) {
	prevTimeout := routeSyncTimeout
	routeSyncTimeout = 20 * time.Millisecond
	defer func() { routeSyncTimeout = prevTimeout }()

	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{ID: "academic-custom", SessionID: "sess-1"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	a.bus = bus
	a.running = true

	respSub, err := bus.SubscribeAsync(a.channelsForTests().Responses, a.handleBusResponse)
	if err != nil {
		t.Fatalf("subscribe academic responses: %v", err)
	}
	defer respSub.Unsubscribe()

	reqSub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		if req.SourceAgentID != "academic-custom" {
			t.Fatalf("source_agent_id = %q, want academic-custom", req.SourceAgentID)
		}
		if !req.ExplicitTarget {
			t.Fatal("expected explicit_target=true for academic consultation")
		}
		if _, preserved := req.Metadata["chat_preserve_source_stream_target"]; preserved {
			t.Fatalf("chat_preserve_source_stream_target = %#v, want absent", req.Metadata["chat_preserve_source_stream_target"])
		}
		go func(correlationID string) {
			time.Sleep(10 * time.Millisecond)
			_ = bus.Publish(a.channelsForTests().Responses, &guide.Message{
				ID:            "child-stream",
				CorrelationID: "child-corr",
				Type:          guide.MessageTypeStream,
				Payload: &guide.StreamResponse{
					CorrelationID:     "child-corr",
					RespondingAgentID: "librarian",
					Metadata: map[string]any{
						"chat_nested_branch":         true,
						"chat_parent_correlation_id": correlationID,
						"chat_parent_tool_call_key":  "consult-1",
						"chat_inter_agent_kind":      shared.InterAgentToolEventKindConsult,
					},
					Event: &guide.StreamEvent{Type: guide.StreamEventProgress},
				},
			})
			time.Sleep(15 * time.Millisecond)
			_ = bus.Publish(a.channelsForTests().Responses, guide.NewResponseMessage("resp", &guide.RouteResponse{
				CorrelationID:     correlationID,
				Success:           true,
				RespondingAgentID: "librarian",
			}))
		}(req.CorrelationID)
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	if _, err := a.requestConsultSync(context.Background(), &guide.RouteRequest{
		TargetAgentID: "librarian",
		Input:         "inspect repo",
	}); err != nil {
		t.Fatalf("requestConsultSync() error = %v", err)
	}
}

func (a *Academic) channelsForTests() *guide.AgentChannels {
	if a.channels != nil {
		return a.channels
	}
	a.channels = guide.NewAgentChannels("academic", a.id)
	return a.channels
}

func TestAcademicRequestConsultation_UsesLiveSessionAndAcademicID(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{ID: "academic-custom", SessionID: "sess-config"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	a.bus = bus
	a.running = true

	respSub, err := bus.SubscribeAsync(a.channelsForTests().Responses, a.handleBusResponse)
	if err != nil {
		t.Fatalf("subscribe academic responses: %v", err)
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
		_ = bus.Publish(a.channelsForTests().Responses, guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "librarian",
		}))
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	ctx := versioning.WithSessionID(context.Background(), "sess-live")
	if _, err := a.requestConsultation(ctx, "librarian", "inspect repo", "", ""); err != nil {
		t.Fatalf("requestConsultation() error = %v", err)
	}

	select {
	case req := <-requests:
		if req.SourceAgentID != "academic-custom" {
			t.Fatalf("source_agent_id = %q, want academic-custom", req.SourceAgentID)
		}
		if req.SessionID != "sess-live" {
			t.Fatalf("session_id = %q, want sess-live", req.SessionID)
		}
		if !req.ExplicitTarget {
			t.Fatal("expected explicit_target=true for academic consultation")
		}
		if _, preserved := req.Metadata["chat_preserve_source_stream_target"]; preserved {
			t.Fatalf("chat_preserve_source_stream_target = %#v, want absent", req.Metadata["chat_preserve_source_stream_target"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for consultation request")
	}
}

func TestAcademicRequestConsultation_PropagatesRouteHopsFromContext(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{ID: "academic-custom", SessionID: "sess-config"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	a.bus = bus
	a.running = true

	respSub, err := bus.SubscribeAsync(a.channelsForTests().Responses, a.handleBusResponse)
	if err != nil {
		t.Fatalf("subscribe academic responses: %v", err)
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
		_ = bus.Publish(a.channelsForTests().Responses, guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "librarian",
		}))
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer reqSub.Unsubscribe()

	ctx := withRouteHops(versioning.WithSessionID(context.Background(), "sess-live"), 2)
	if _, err := a.requestConsultation(ctx, "librarian", "inspect repo", "", ""); err != nil {
		t.Fatalf("requestConsultation() error = %v", err)
	}

	select {
	case req := <-requests:
		if req.Hops != 2 {
			t.Fatalf("hops = %d, want 2", req.Hops)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for consultation request")
	}
}
