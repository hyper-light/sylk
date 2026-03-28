package librarian

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
)

func TestLibrarianRequestConsultationWithMetadataPropagatesResearchDepth(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	l, err := New(Config{ID: "librarian-test", EnableLLM: true}, nil)
	if err != nil {
		t.Fatalf("new librarian: %v", err)
	}
	l.bus = bus
	l.running = true
	l.knownAgents["academic"] = &guide.AgentAnnouncement{AgentID: "academic", AgentType: "academic"}

	requests := make(chan *guide.RouteRequest, 1)
	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		select {
		case requests <- req:
		default:
		}
		return bus.Publish(librarianResponseTopic(l), guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			RespondingAgentID: "academic",
			Data:              map[string]any{"ok": true},
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	if _, err := l.requestConsultationWithMetadata(
		context.Background(),
		"academic",
		"check this approach",
		"",
		"sess-1",
		shared.ConsultationMetadataWithResearchDepth(nil, "deep"),
	); err != nil {
		t.Fatalf("requestConsultationWithMetadata: %v", err)
	}

	select {
	case req := <-requests:
		if got := req.Metadata[shared.ConsultationMetadataResearchDepthKey]; got != "deep" {
			t.Fatalf("metadata research_depth = %#v, want deep", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for routed request")
	}
}
