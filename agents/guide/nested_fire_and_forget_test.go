package guide

import (
	"context"
	"testing"
	"time"
)

func TestGuideRoute_NestedFireAndForgetAddsPendingRelayAnchor(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-nested-fire-and-forget",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	err = g.Register(&AgentRoutingInfo{
		ID:   "archivalist",
		Type: "archivalist",
		Name: "archivalist",
		Registration: &AgentRegistration{
			ID:   "archivalist",
			Name: "archivalist",
			Capabilities: AgentCapabilities{
				Intents: []Intent{IntentStore, IntentRecall},
				Domains: []Domain{DomainHistory, DomainGeneral},
			},
		},
	})
	if err != nil {
		t.Fatalf("register archivalist: %v", err)
	}

	req := &RouteRequest{
		CorrelationID: "corr-nested-fire-and-forget",
		Input:         "@archivalist:store:history{\"content\":\"stored child event\"}",
		SourceAgentID: "architect",
		FireAndForget: true,
		SessionID:     "session-nested-fire-and-forget",
		Timestamp:     time.Now(),
		Metadata: map[string]any{
			"chat_nested_branch":    true,
			"chat_inter_agent_kind": "store",
		},
	}

	forwarded, err := g.Route(context.Background(), req)
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if forwarded == nil {
		t.Fatal("expected forwarded request")
	}
	if pending := g.pending.Get(forwarded.CorrelationID); pending == nil {
		t.Fatal("expected nested fire-and-forget route to retain pending relay anchor")
	}
}

func TestGuideHandleResponseMessage_StreamCompleteRemovesNestedFireAndForgetPending(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "session-stream-complete",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	g.pending.Set("corr-stream-complete", &PendingRequest{
		CorrelationID: "corr-stream-complete",
		SourceAgentID: "architect",
		TargetAgentID: "archivalist",
		Request: &RouteRequest{
			CorrelationID: "corr-stream-complete",
			SourceAgentID: "architect",
			FireAndForget: true,
			SessionID:     "session-stream-complete",
			Metadata: map[string]any{
				"chat_nested_branch":    true,
				"chat_inter_agent_kind": "store",
			},
		},
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Minute),
	})

	msg := &Message{
		ID:            "msg-stream-complete",
		Type:          MessageTypeStream,
		CorrelationID: "corr-stream-complete",
		SourceAgentID: "archivalist",
		Payload: &StreamResponse{
			CorrelationID:     "corr-stream-complete",
			RespondingAgentID: "archivalist",
			Event: &StreamEvent{
				Type:      StreamEventComplete,
				Timestamp: time.Now(),
			},
		},
	}

	if err := g.handleResponseMessage(msg); err != nil {
		t.Fatalf("handleResponseMessage returned error: %v", err)
	}
	if pending := g.pending.Get("corr-stream-complete"); pending != nil {
		t.Fatalf("expected nested fire-and-forget pending to be removed on stream complete, got %+v", pending)
	}
}

func TestShouldPublishForwardedStreamLifecycle_NestedFireAndForget(t *testing.T) {
	req := &ForwardedRequest{
		FireAndForget: true,
		Metadata: map[string]any{
			"chat_nested_branch":          true,
			"chat_parent_correlation_id":  "corr-parent",
			"chat_parent_tool_call_key":   "store-1",
			"chat_inter_agent_kind":       "store",
			"chat_inter_agent_thread_key": "",
		},
	}
	if !ShouldPublishForwardedStreamLifecycle(req) {
		t.Fatal("expected nested fire-and-forget forwarded request to publish stream lifecycle")
	}
}

func TestShouldPublishForwardedStreamLifecycle_PlainFireAndForget(t *testing.T) {
	req := &ForwardedRequest{
		FireAndForget: true,
		Metadata: map[string]any{
			"note": "background work",
		},
	}
	if ShouldPublishForwardedStreamLifecycle(req) {
		t.Fatal("expected plain fire-and-forget forwarded request to stay stream-silent")
	}
}
