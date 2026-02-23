package guide

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConversationContextSkill_ReturnsActiveContext(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}
	g.conversation.ObserveRoutedRequest("sess-1", "architect")

	result := g.skills.Invoke(context.Background(), "conversation_context", json.RawMessage(`{"session_id":"sess-1","format":"json"}`))
	if !result.Success {
		t.Fatalf("invoke failed: %s", result.Error)
	}
	payload, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type %T", result.Data)
	}
	active, _ := payload["active"].(bool)
	if !active {
		t.Fatalf("expected active conversation context")
	}
	if payload["active_agent"] != "architect" {
		t.Fatalf("active_agent = %v, want architect", payload["active_agent"])
	}
}

func TestConversationContextSkill_NoContext(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer func() { _ = bus.Close() }()

	g, err := NewWithClassifier(NewRuleClassifierClient(), Config{
		Bus:       bus,
		AgentID:   "guide",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("new guide: %v", err)
	}

	result := g.skills.Invoke(context.Background(), "conversation_context", json.RawMessage(`{"session_id":"sess-1"}`))
	if !result.Success {
		t.Fatalf("invoke failed: %s", result.Error)
	}
	reply, ok := result.Data.(string)
	if !ok {
		t.Fatalf("unexpected payload type %T", result.Data)
	}
	if reply != "No active conversation context for this session." {
		t.Fatalf("reply = %q", reply)
	}
}
