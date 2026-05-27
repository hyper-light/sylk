package guide

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
)

func newTestChannelBus(t *testing.T) *ChannelBus {
	t.Helper()
	return NewChannelBus(ChannelBusConfig{})
}

func TestClaimsBusAdapter_NilBusReturnsNil(t *testing.T) {
	if NewClaimsBusAdapter(nil) != nil {
		t.Error("nil bus should produce nil adapter")
	}
}

func TestClaimsBusAdapter_PublishRejectsNilDelta(t *testing.T) {
	bus := newTestChannelBus(t)
	defer bus.Close()
	adapter := NewClaimsBusAdapter(bus)
	if err := adapter.PublishDelta(context.Background(), "any.topic", nil); err == nil {
		t.Error("expected error for nil delta")
	}
}

func TestClaimsBusAdapter_PublishSubscribeRoundTrip(t *testing.T) {
	bus := newTestChannelBus(t)
	defer bus.Close()
	adapter := NewClaimsBusAdapter(bus)

	received := make(chan claims.Delta, 1)
	sub, err := adapter.SubscribeDelta("claims.sess.inbox.eng.subject.task", func(d claims.Delta) {
		received <- d
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	want := claims.InboxDelta{
		SessionID: "sess", ClaimID: "c1", AgentID: "eng",
		Relationship: claims.RelationshipSubject, Sequence: 1,
	}
	topic := claims.InboxTopic("sess", "eng", claims.RelationshipSubject, claims.ActionTypeTask)
	if err := adapter.PublishDelta(context.Background(), topic, want); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-received:
		inbox, ok := got.(claims.InboxDelta)
		if !ok {
			t.Fatalf("expected InboxDelta, got %T", got)
		}
		if inbox.ClaimID != "c1" {
			t.Errorf("claim_id: %q", inbox.ClaimID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delta not delivered within timeout")
	}
}

func TestClaimsBusAdapter_PublishSubscribeCanonicalRoundTrip(t *testing.T) {
	bus := newTestChannelBus(t)
	defer bus.Close()
	adapter := NewClaimsBusAdapter(bus)

	received := make(chan claims.Delta, 1)
	topic := claims.CanonicalAgentTypeTopic("sess", "librarian", claims.DeltaActionClaimDirected)
	sub, err := adapter.SubscribeDelta(topic, func(d claims.Delta) {
		received <- d
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	want := claims.NewCanonicalDelta(
		claims.DeltaActionClaimDirected,
		"sess",
		"board",
		1,
		time.Now().UTC(),
		claims.DegradedAgentRef("architect", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "c1"}},
		&claims.DeltaDelivery{
			To:           []claims.AgentRef{claims.DegradedAgentRef("librarian", "test")},
			Relationship: claims.RelationshipSubject,
		},
		map[string]any{"claim": map[string]any{"id": "c1", "action": string(claims.ActionTypeConsultation)}},
	)
	if err := adapter.PublishDelta(context.Background(), topic, want); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-received:
		canonical, ok := got.(claims.CanonicalDelta)
		if !ok {
			t.Fatalf("expected CanonicalDelta, got %T", got)
		}
		if canonical.Action != claims.DeltaActionClaimDirected || canonical.ClaimID() != "c1" {
			t.Fatalf("unexpected canonical delta: %+v", canonical)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canonical delta not delivered within timeout")
	}
}

func TestExtractClaimsDelta_HandlesNilMessage(t *testing.T) {
	d, err := ExtractClaimsDelta(nil)
	if err != nil || d != nil {
		t.Errorf("expected (nil, nil), got (%v, %v)", d, err)
	}
}

func TestExtractClaimsDelta_SkipsNonClaimsMessages(t *testing.T) {
	msg := &Message{Type: MessageTypeRequest}
	d, err := ExtractClaimsDelta(msg)
	if err != nil || d != nil {
		t.Errorf("expected (nil, nil), got (%v, %v)", d, err)
	}
}

func TestExtractClaimsDelta_DecodesBytes(t *testing.T) {
	want := claims.PhaseDelta{SessionID: "s", BoardID: "b", ToPhase: claims.BoardPhaseValidation, Sequence: 1}
	bytes, err := claims.MarshalDelta(want)
	if err != nil {
		t.Fatal(err)
	}
	msg := &Message{Type: MessageTypeClaimsDelta, Payload: bytes}
	d, err := ExtractClaimsDelta(msg)
	if err != nil {
		t.Fatal(err)
	}
	if d.DeltaKind() != claims.DeltaKindPhase {
		t.Errorf("kind: %q", d.DeltaKind())
	}
}

func TestExtractClaimsDelta_DecodesCanonicalBytes(t *testing.T) {
	want := claims.NewCanonicalDelta(
		claims.DeltaActionClaimProgressed,
		"s",
		"b",
		1,
		time.Now().UTC(),
		claims.DegradedAgentRef("architect", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: "c1"}},
		nil,
		map[string]any{"claim": map[string]any{"id": "c1"}},
	)
	bytes, err := claims.MarshalDelta(want)
	if err != nil {
		t.Fatal(err)
	}
	msg := &Message{Type: MessageTypeClaimsDelta, Payload: bytes}
	d, err := ExtractClaimsDelta(msg)
	if err != nil {
		t.Fatal(err)
	}
	if d.DeltaKind() != string(claims.DeltaActionClaimProgressed) {
		t.Errorf("kind: %q", d.DeltaKind())
	}
}

func TestClaimsBusAdapter_WildcardDelivers(t *testing.T) {
	bus := newTestChannelBus(t)
	defer bus.Close()
	adapter := NewClaimsBusAdapter(bus)

	var count atomic.Int32
	var mu sync.Mutex
	seen := make([]claims.Delta, 0, 4)

	sub, err := adapter.SubscribeDelta("claims.sess.inbox.eng.*.*", func(d claims.Delta) {
		mu.Lock()
		seen = append(seen, d)
		mu.Unlock()
		count.Add(1)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	// Publish two deltas with different relationships but same agent.
	for i, rel := range []string{claims.RelationshipSubject, claims.RelationshipEvaluator} {
		topic := claims.InboxTopic("sess", "eng", rel, claims.ActionTypeTask)
		delta := claims.InboxDelta{
			SessionID: "sess", ClaimID: "c" + string(rune('1'+i)),
			AgentID: "eng", Relationship: rel, Sequence: uint64(i + 1),
		}
		if err := adapter.PublishDelta(context.Background(), topic, delta); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && count.Load() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if count.Load() != 2 {
		t.Fatalf("expected 2 deliveries, got %d", count.Load())
	}
}
