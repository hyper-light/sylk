package shared

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestRequestGuideRouteSyncWaitsForMatchingTerminalResponse(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	sub, err := bus.SubscribeAsync(guide.TopicGuideRequests, func(msg *guide.Message) error {
		req, ok := msg.GetRouteRequest()
		if !ok || req == nil {
			return nil
		}
		_ = bus.Publish(guide.TopicResponses("tui", "tui"), guide.NewResponseMessage("ignore", &guide.RouteResponse{
			CorrelationID:     "other",
			Success:           true,
			RespondingAgentID: "tester-pipeline",
		}))
		return bus.Publish(guide.TopicResponses("tui", "tui"), guide.NewResponseMessage("resp", &guide.RouteResponse{
			CorrelationID:     req.CorrelationID,
			Success:           true,
			Data:              map[string]any{"ok": true},
			RespondingAgentID: "tester-pipeline",
		}))
	})
	if err != nil {
		t.Fatalf("subscribe guide requests: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msg, err := RequestGuideRouteSync(ctx, GuideRouteSyncRequest{
		Bus:           bus,
		ResponseTopic: guide.TopicResponses("tui", "tui"),
		Request: &guide.RouteRequest{
			SourceAgentID:  "tui",
			TargetAgentID:  "tester-pipeline",
			ExplicitTarget: true,
			Input:          `{"task_id":"task_1"}`,
		},
	})
	if err != nil {
		t.Fatalf("RequestGuideRouteSync: %v", err)
	}

	resp, ok := msg.GetRouteResponse()
	if !ok || resp == nil {
		t.Fatalf("expected route response, got %#v", msg)
	}
	if resp.CorrelationID == "other" {
		t.Fatal("received mismatched response")
	}
}
