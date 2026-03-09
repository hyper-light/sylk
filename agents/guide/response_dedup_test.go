package guide

import (
	"testing"
	"time"
)

func newResponseTestGuide(t *testing.T) (*Guide, chan *Message) {
	t.Helper()

	bus := NewChannelBus(DefaultChannelBusConfig())
	t.Cleanup(func() {
		_ = bus.Close()
	})

	out := make(chan *Message, 8)
	sub, err := bus.Subscribe(TopicResponses("tui", "tui"), func(msg *Message) error {
		out <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe tui responses: %v", err)
	}
	t.Cleanup(func() {
		_ = sub.Unsubscribe()
	})

	return &Guide{
		bus:                  bus,
		pending:              NewPendingStore(DefaultPendingStoreConfig()),
		streams:              newGuideStreamManager(nil),
		agentChannels:        NewStringMap[*AgentChannels](DefaultShardCount),
		sessionID:            "test-session",
		responseMessagesSeen: make(map[string]time.Time),
		completedPendings:    make(map[string]completedPendingRoute),
	}, out
}

func setPendingRoute(g *Guide, correlationID string) {
	g.pending.Set(correlationID, &PendingRequest{
		CorrelationID: correlationID,
		SourceAgentID: "tui",
		TargetAgentID: "academic",
		Request: &RouteRequest{
			CorrelationID: correlationID,
			SessionID:     "test-session",
			SourceAgentID: "tui",
		},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(DefaultPendingStoreConfig().DefaultTimeout),
	})
}

func TestHandleResponseMessage_DeduplicatesByBusMessageID(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRoute(g, "corr-start")

	msg := &Message{
		ID:            "msg-stream-start",
		CorrelationID: "corr-start",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-start",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type:      StreamEventStart,
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("first handleResponseMessage: %v", err)
	}
	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("second handleResponseMessage: %v", err)
	}

	select {
	case forwarded := <-out:
		stream, ok := forwarded.GetStreamResponse()
		if !ok || stream == nil || stream.Event == nil {
			t.Fatalf("unexpected forwarded message: %+v", forwarded)
		}
		if stream.Event.Type != StreamEventStart {
			t.Fatalf("forwarded stream type = %q, want start", stream.Event.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded stream start")
	}

	select {
	case extra := <-out:
		t.Fatalf("unexpected duplicate forwarded message: %+v", extra)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHandleResponseMessage_ForwardsLateStreamCompleteAfterRouteResponse(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRoute(g, "corr-late-complete")

	respMsg := &Message{
		ID:            "msg-route-response",
		CorrelationID: "corr-late-complete",
		Type:          MessageTypeResponse,
		SourceAgentID: "academic",
		Payload: &RouteResponse{
			CorrelationID:       "corr-late-complete",
			Success:             true,
			RespondingAgentID:   "academic",
			RespondingAgentName: "Academic",
			Data:                "final answer",
		},
	}
	if err := g.handleResponseMessage(respMsg); err != nil {
		t.Fatalf("handle route response: %v", err)
	}

	completeMsg := &Message{
		ID:            "msg-stream-complete",
		CorrelationID: "corr-late-complete",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-late-complete",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type:      StreamEventComplete,
				Text:      "final answer",
				Timestamp: time.Now(),
			},
		},
	}
	if err := g.handleResponseMessage(completeMsg); err != nil {
		t.Fatalf("handle late stream complete: %v", err)
	}

	var sawResponse, sawComplete bool
	deadline := time.After(2 * time.Second)
	for !(sawResponse && sawComplete) {
		select {
		case forwarded := <-out:
			if resp, ok := forwarded.GetRouteResponse(); ok && resp != nil {
				sawResponse = true
			}
			if stream, ok := forwarded.GetStreamResponse(); ok && stream != nil && stream.Event != nil && stream.Event.Type == StreamEventComplete {
				sawComplete = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for forwarded late complete; sawResponse=%v sawComplete=%v", sawResponse, sawComplete)
		}
	}
}
