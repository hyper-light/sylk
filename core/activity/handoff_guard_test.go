package activity

import (
	"context"
	"errors"
	"testing"
)

// stubHandoffGuard records calls + returns a configurable error.
type stubHandoffGuard struct {
	calls []stubHandoffCall
	err   error
}

type stubHandoffCall struct {
	agentID, sessionID, predecessor string
}

func (g *stubHandoffGuard) Verify(_ context.Context, agentID, sessionID, predecessor string) error {
	g.calls = append(g.calls, stubHandoffCall{agentID, sessionID, predecessor})
	return g.err
}

func TestClassifyHandoff_ActionHandoffEmitted(t *testing.T) {
	a := AgentActivity{Action: ActionHandoffEmitted, Subject: Subject{Coordinates: map[string]string{"handoff_from_claim_id": "pred-1"}}}
	pred, ok := classifyHandoff(a)
	if !ok || pred != "pred-1" {
		t.Fatalf("classifyHandoff = %q,%v, want pred-1,true", pred, ok)
	}
}

func TestClassifyHandoff_CoordOnly(t *testing.T) {
	a := AgentActivity{Action: ActionConsultEmitted, Subject: Subject{Coordinates: map[string]string{"handoff_from_claim_id": "pred-2"}}}
	pred, ok := classifyHandoff(a)
	if !ok || pred != "pred-2" {
		t.Fatalf("classifyHandoff = %q,%v, want pred-2,true", pred, ok)
	}
}

func TestClassifyHandoff_NotHandoff(t *testing.T) {
	a := AgentActivity{Action: ActionConsultEmitted}
	if pred, ok := classifyHandoff(a); ok || pred != "" {
		t.Fatalf("classifyHandoff = %q,%v, want '',false", pred, ok)
	}
}

func TestSetHandoffGuard_RoundTrip(t *testing.T) {
	stub := &stubHandoffGuard{}
	prev := SetHandoffGuard(stub)
	defer SetHandoffGuard(prev)
	if loadHandoffGuard() != stub {
		t.Fatal("SetHandoffGuard did not install the stub")
	}
}

func TestAppend_HandoffGuardDropsRejectedActivity(t *testing.T) {
	collector := NewTestCollector()
	prevSink := SetDefaultSink(collector)
	defer SetDefaultSink(prevSink)

	stub := &stubHandoffGuard{err: errors.New("blocked")}
	prevGuard := SetHandoffGuard(stub)
	defer SetHandoffGuard(prevGuard)

	handoff := AgentActivity{
		Action:  ActionHandoffEmitted,
		Actor:   Actor{AgentID: "architect"},
		Subject: Subject{Coordinates: map[string]string{"handoff_from_claim_id": "pred"}},
	}
	Append(context.Background(), handoff)

	if got := collector.Count(); got != 0 {
		t.Fatalf("expected dropped activity (count=0), got %d", got)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("expected guard to be consulted once, got %d", len(stub.calls))
	}
}

func TestAppend_HandoffGuardAllowsApprovedActivity(t *testing.T) {
	collector := NewTestCollector()
	prevSink := SetDefaultSink(collector)
	defer SetDefaultSink(prevSink)

	stub := &stubHandoffGuard{err: nil}
	prevGuard := SetHandoffGuard(stub)
	defer SetHandoffGuard(prevGuard)

	handoff := AgentActivity{
		Action:  ActionHandoffEmitted,
		Actor:   Actor{AgentID: "architect"},
		Subject: Subject{Coordinates: map[string]string{"handoff_from_claim_id": "pred"}},
	}
	Append(context.Background(), handoff)

	if got := collector.Count(); got != 1 {
		t.Fatalf("expected propagated activity (count=1), got %d", got)
	}
}

func TestAppend_NonHandoffSkipsGuard(t *testing.T) {
	collector := NewTestCollector()
	prevSink := SetDefaultSink(collector)
	defer SetDefaultSink(prevSink)

	stub := &stubHandoffGuard{err: errors.New("would block")}
	prevGuard := SetHandoffGuard(stub)
	defer SetHandoffGuard(prevGuard)

	a := AgentActivity{Action: ActionConsultEmitted, Actor: Actor{AgentID: "architect"}}
	Append(context.Background(), a)

	if got := collector.Count(); got != 1 {
		t.Fatalf("non-handoff should propagate, got count=%d", got)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("guard should not have been consulted for non-handoff, got %d calls", len(stub.calls))
	}
}
