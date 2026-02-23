package network

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

type recordingSink struct {
	delivered atomic.Int64
	err       error
}

func (s *recordingSink) Deliver(_ context.Context, _ *MessageEnvelope) error {
	s.delivered.Add(1)
	return s.err
}

func testNamespace(t *testing.T, sink MessageSink, policies []*NetworkPolicy) *NetworkNamespace {
	t.Helper()
	ns := NewNetworkNamespace(NetworkNamespaceConfig{
		PodID:    "test-pod",
		Policies: policies,
		Sink:     sink,
	})
	ns.RegisterContainer("c1", "agent-1", RateLimiterConfig{Rate: 1000, Burst: 2000}, CircuitBreakerConfig{})
	ns.auth.RegisterKey("c1", []byte("test-secret-key"))
	return ns
}

func allowAllPolicy() *NetworkPolicy {
	return &NetworkPolicy{
		Name: "allow-all",
		Egress: []EgressRule{
			{AllowedTopics: []string{"*"}},
		},
	}
}

func testEnvelope() *MessageEnvelope {
	return &MessageEnvelope{
		SourceContainerID: "c1",
		SourceAgentType:   "engineer",
		TargetAgentID:     "agent-2",
		TargetAgentType:   "inspector",
		Topic:             "requests",
		Payload:           []byte("hello"),
	}
}

func TestNamespace_SendDelivers(t *testing.T) {
	sink := &recordingSink{}
	ns := testNamespace(t, sink, []*NetworkPolicy{allowAllPolicy()})

	if err := ns.Send(context.Background(), testEnvelope()); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if sink.delivered.Load() != 1 {
		t.Fatalf("expected 1 delivery, got %d", sink.delivered.Load())
	}
}

func TestNamespace_SendSetsSignature(t *testing.T) {
	sink := &recordingSink{}
	ns := testNamespace(t, sink, []*NetworkPolicy{allowAllPolicy()})

	env := testEnvelope()
	if err := ns.Send(context.Background(), env); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if env.Signature == "" {
		t.Fatal("signature should be set by Send")
	}
}

func TestNamespace_SendDeniedByPolicy(t *testing.T) {
	sink := &recordingSink{}
	// No policies → default deny
	ns := testNamespace(t, sink, nil)

	err := ns.Send(context.Background(), testEnvelope())
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}
	if sink.delivered.Load() != 0 {
		t.Fatal("should not deliver when policy denies")
	}
}

func TestNamespace_SendClosedNamespace(t *testing.T) {
	sink := &recordingSink{}
	ns := testNamespace(t, sink, []*NetworkPolicy{allowAllPolicy()})

	ns.Close()

	err := ns.Send(context.Background(), testEnvelope())
	if !errors.Is(err, ErrNamespaceClosed) {
		t.Fatalf("expected ErrNamespaceClosed, got %v", err)
	}
}

func TestNamespace_IsClosed(t *testing.T) {
	ns := testNamespace(t, nil, nil)

	if ns.IsClosed() {
		t.Fatal("should not be closed initially")
	}
	ns.Close()
	if !ns.IsClosed() {
		t.Fatal("should be closed after Close()")
	}
}

func TestNamespace_VerifyMessage(t *testing.T) {
	ns := testNamespace(t, nil, nil)

	payload := []byte("test payload")
	sig, err := ns.auth.Sign("c1", payload)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	env := &MessageEnvelope{
		SourceContainerID: "c1",
		Payload:           payload,
		Signature:         sig,
	}
	if err := ns.VerifyMessage(env); err != nil {
		t.Fatalf("VerifyMessage failed: %v", err)
	}
}

func TestNamespace_VerifyTamperedMessage(t *testing.T) {
	ns := testNamespace(t, nil, nil)

	payload := []byte("test payload")
	sig, _ := ns.auth.Sign("c1", payload)

	env := &MessageEnvelope{
		SourceContainerID: "c1",
		Payload:           []byte("tampered"),
		Signature:         sig,
	}
	if err := ns.VerifyMessage(env); err == nil {
		t.Fatal("should reject tampered message")
	}
}

func TestNamespace_CircuitBreakerTriggered(t *testing.T) {
	sink := &recordingSink{err: errors.New("delivery failed")}
	ns := NewNetworkNamespace(NetworkNamespaceConfig{
		PodID:    "test-pod",
		Policies: []*NetworkPolicy{allowAllPolicy()},
		Sink:     sink,
	})
	ns.RegisterContainer("c1", "agent-target", RateLimiterConfig{Rate: 1000, Burst: 2000}, CircuitBreakerConfig{
		FailureThreshold: 3,
	})
	ns.auth.RegisterKey("c1", []byte("test-secret-key"))

	env := &MessageEnvelope{
		SourceContainerID: "c1",
		SourceAgentType:   "engineer",
		TargetAgentID:     "agent-target",
		TargetAgentType:   "inspector",
		Topic:             "requests",
		Payload:           []byte("hello"),
	}

	// 3 failures to trigger circuit open
	for i := 0; i < 3; i++ {
		_ = ns.Send(context.Background(), env)
	}

	// Next call should get circuit open error
	err := ns.Send(context.Background(), env)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen after threshold, got %v", err)
	}
}

func TestNamespace_UnregisterContainer(t *testing.T) {
	ns := testNamespace(t, nil, nil)

	ns.UnregisterContainer("c1", "agent-1")

	cb := ns.CircuitBreaker("agent-1")
	if cb != nil {
		t.Fatal("circuit breaker should be nil after unregister")
	}
}

func TestNamespace_NilSinkNoError(t *testing.T) {
	ns := testNamespace(t, nil, []*NetworkPolicy{allowAllPolicy()})

	err := ns.Send(context.Background(), testEnvelope())
	if err != nil {
		t.Fatalf("nil sink should not error: %v", err)
	}
}

func TestNamespace_PodID(t *testing.T) {
	ns := testNamespace(t, nil, nil)
	if ns.PodID() != "test-pod" {
		t.Fatalf("expected test-pod, got %s", ns.PodID())
	}
}
