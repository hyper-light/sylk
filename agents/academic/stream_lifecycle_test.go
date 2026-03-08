package academic

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestAcademicPublishesStreamLifecycleForForwardedRequest(t *testing.T) {
	bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())
	defer bus.Close()

	a, err := New(Config{SessionID: "sess-1"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}
	defer a.Stop()

	if err := a.Start(bus); err != nil {
		t.Fatalf("start academic: %v", err)
	}

	streamCh := make(chan *guide.StreamResponse, 8)
	sub, err := bus.SubscribeAsync(a.channels.Responses, func(msg *guide.Message) error {
		stream, ok := msg.GetStreamResponse()
		if ok && stream != nil {
			streamCh <- stream
		}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe responses: %v", err)
	}
	defer sub.Unsubscribe()

	correlationID := "corr-academic-stream"
	fwd := &guide.ForwardedRequest{
		CorrelationID: correlationID,
		Input:         "research a plan",
		Intent:        guide.IntentPlan, // unsupported by Academic; forces fast failure.
		SessionID:     "sess-1",
		SourceAgentID: "tui",
		TargetAgentID: a.id,
	}
	if err := bus.Publish(a.channels.Requests, guide.NewForwardMessage("", fwd)); err != nil {
		t.Fatalf("publish forward: %v", err)
	}

	var sawStart, sawError, sawComplete bool
	deadline := time.After(2 * time.Second)
	for !(sawStart && sawError && sawComplete) {
		select {
		case stream := <-streamCh:
			if stream.CorrelationID != correlationID {
				continue
			}
			if stream.RespondingAgentID != a.id {
				t.Fatalf("responding agent id = %q, want %q", stream.RespondingAgentID, a.id)
			}
			switch stream.Event.Type {
			case guide.StreamEventStart:
				sawStart = true
			case guide.StreamEventError:
				sawError = true
			case guide.StreamEventComplete:
				sawComplete = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for lifecycle events; start=%v error=%v complete=%v", sawStart, sawError, sawComplete)
		}
	}
}
