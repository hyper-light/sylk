package guide

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers"
)

type responseTestProvider struct{}

func (responseTestProvider) Name() string { return "response-test" }

func (responseTestProvider) SupportedModels() []providers.ModelInfo {
	return []providers.ModelInfo{{ID: "response-test-model", Name: "response-test-model", MaxContext: 8192}}
}

func (responseTestProvider) Complete(context.Context, *providers.CompletionRequest) (*providers.CompletionResponse, error) {
	return &providers.CompletionResponse{}, nil
}

func (responseTestProvider) Stream(context.Context, *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
	ch := make(chan *providers.StreamChunk)
	close(ch)
	return ch, nil
}

func (responseTestProvider) CountTokens([]providers.Message) (int, error) { return 0, nil }

func (responseTestProvider) MaxContextTokens(string) int { return 8192 }

func (responseTestProvider) HealthCheck(context.Context) error { return nil }

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

func TestHandleResponseMessage_InitializesTrackingMapsWhenNil(t *testing.T) {
	g, out := newResponseTestGuide(t)
	setPendingRoute(g, "corr-init-nil")
	g.responseMessagesSeen = nil
	g.completedPendings = nil

	respMsg := &Message{
		ID:            "msg-init-nil-response",
		CorrelationID: "corr-init-nil",
		Type:          MessageTypeResponse,
		SourceAgentID: "academic",
		Payload: &RouteResponse{
			CorrelationID:       "corr-init-nil",
			Success:             true,
			RespondingAgentID:   "academic",
			RespondingAgentName: "Academic",
			Data:                "ready",
		},
	}

	if err := g.handleResponseMessage(respMsg); err != nil {
		t.Fatalf("handle response with nil tracking maps: %v", err)
	}
	if g.responseMessagesSeen == nil {
		t.Fatal("responseMessagesSeen should be initialized on demand")
	}
	if g.completedPendings == nil {
		t.Fatal("completedPendings should be initialized on demand")
	}

	select {
	case <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for forwarded route response")
	}

	completeMsg := &Message{
		ID:            "msg-init-nil-complete",
		CorrelationID: "corr-init-nil",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-init-nil",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type:      StreamEventComplete,
				Text:      "ready",
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(completeMsg); err != nil {
		t.Fatalf("handle late complete with nil-initialized tracking maps: %v", err)
	}
}

func TestObserveStreamReroute_InitializesDeferredMapWhenNil(t *testing.T) {
	g, _ := newResponseTestGuide(t)
	setPendingRoute(g, "corr-reroute-old")
	g.streamReroutes = nil

	g.observeStreamReroute(&StreamResponse{
		CorrelationID: "corr-reroute-old",
		Event: &StreamEvent{
			Type: StreamEventReroute,
			Data: map[string]string{
				"new_correlation_id": "corr-reroute-new",
			},
		},
	})

	if g.streamReroutes == nil {
		t.Fatal("streamReroutes should be initialized on demand")
	}
	if got := g.streamReroutes["corr-reroute-new"]; got != "tui" {
		t.Fatalf("streamReroutes[new] = %q, want tui", got)
	}

	pending := &PendingRequest{CorrelationID: "corr-reroute-new"}
	g.applyDeferredStreamReroute(pending)
	if pending.StreamTargetOverride != "tui" {
		t.Fatalf("pending.StreamTargetOverride = %q, want tui", pending.StreamTargetOverride)
	}
	if _, ok := g.streamReroutes["corr-reroute-new"]; ok {
		t.Fatal("deferred stream reroute should be consumed after apply")
	}
}

func TestNewWithProvider_InitializesResponseTrackingForStreamRelay(t *testing.T) {
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

	g, err := NewWithProvider(responseTestProvider{}, "response-test-model", Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("NewWithProvider: %v", err)
	}
	if g.responseMessagesSeen == nil {
		t.Fatal("responseMessagesSeen should be initialized")
	}
	if g.completedPendings == nil {
		t.Fatal("completedPendings should be initialized")
	}

	setPendingRoute(g, "corr-provider-stream")

	msg := &Message{
		ID:            "msg-provider-stream-start",
		CorrelationID: "corr-provider-stream",
		Type:          MessageTypeStream,
		SourceAgentID: "academic",
		Payload: &StreamResponse{
			CorrelationID:     "corr-provider-stream",
			RespondingAgentID: "academic",
			Event: &StreamEvent{
				Type:      StreamEventStart,
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("handleResponseMessage: %v", err)
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
		t.Fatal("timed out waiting for forwarded provider-backed stream")
	}
}
