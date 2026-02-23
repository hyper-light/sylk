package network

import (
	"context"
	"testing"
)

func TestBusBridge_DeliversToTopic(t *testing.T) {
	var gotTopic, gotSource, gotTarget string
	var gotPayload []byte

	publish := PublishFunc(func(topic, source, target string, payload []byte) error {
		gotTopic = topic
		gotSource = source
		gotTarget = target
		gotPayload = payload
		return nil
	})

	bridge := NewBusBridge(publish)
	env := &MessageEnvelope{
		SourceAgentType: "architect",
		TargetAgentID:   "guide",
		Topic:           "guide.requests",
		Payload:         []byte(`{"action":"plan"}`),
	}

	if err := bridge.Deliver(context.Background(), env); err != nil {
		t.Fatalf("Deliver failed: %v", err)
	}
	if gotTopic != "guide.requests" {
		t.Fatalf("expected topic guide.requests, got %q", gotTopic)
	}
	if gotSource != "architect" {
		t.Fatalf("expected source architect, got %q", gotSource)
	}
	if gotTarget != "guide" {
		t.Fatalf("expected target guide, got %q", gotTarget)
	}
	if string(gotPayload) != `{"action":"plan"}` {
		t.Fatalf("unexpected payload: %s", gotPayload)
	}
}

func TestBusBridge_EmptyPayload(t *testing.T) {
	var gotPayload []byte
	publish := PublishFunc(func(_, _, _ string, payload []byte) error {
		gotPayload = payload
		return nil
	})

	bridge := NewBusBridge(publish)
	env := &MessageEnvelope{
		Topic: "test.topic",
	}

	if err := bridge.Deliver(context.Background(), env); err != nil {
		t.Fatalf("Deliver failed: %v", err)
	}
	if gotPayload != nil {
		t.Fatalf("expected nil payload, got %v", gotPayload)
	}
}
