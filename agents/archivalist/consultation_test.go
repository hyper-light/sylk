package archivalist

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
)

func TestArchivalistRequestConsultationWithMetadataPropagatesResearchDepth(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a := &Archivalist{
		id:      "archivalist-test",
		bus:     bus,
		running: true,
		knownAgents: map[string]*guide.AgentAnnouncement{
			"academic": &guide.AgentAnnouncement{AgentID: "academic", AgentType: "academic"},
		},
	}

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
		return bus.Publish(archivalistResponseTopic(a), guide.NewResponseMessage("resp", &guide.RouteResponse{
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

	if _, err := a.requestConsultationWithMetadata(
		context.Background(),
		"academic",
		"check this policy",
		"",
		"sess-1",
		shared.ConsultationMetadataWithResearchDepth(nil, "comprehensive"),
	); err != nil {
		t.Fatalf("requestConsultationWithMetadata: %v", err)
	}

	select {
	case req := <-requests:
		if got := req.Metadata[shared.ConsultationMetadataResearchDepthKey]; got != "comprehensive" {
			t.Fatalf("metadata research_depth = %#v, want comprehensive", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for routed request")
	}
}
