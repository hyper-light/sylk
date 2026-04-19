package recovery

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// capturingSink is a RecoveryEventSink that records every published event
// for assertion in tests. Thread-safe for notifier goroutine fan-out.
type capturingSink struct {
	mu     sync.Mutex
	events []RecoveryEvent
}

func (s *capturingSink) PublishRecoveryEvent(event RecoveryEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *capturingSink) snapshot() []RecoveryEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecoveryEvent, len(s.events))
	copy(out, s.events)
	return out
}

// TestBusRecoveryNotifier_RejectsNilSink locks the no-silent-drop invariant:
// constructing a notifier without a sink is a programming error and must
// fail loudly rather than hide recovery events.
func TestBusRecoveryNotifier_RejectsNilSink(t *testing.T) {
	_, err := NewBusRecoveryNotifier(nil)
	if err == nil {
		t.Fatal("expected error for nil sink")
	}
}

// TestBusRecoveryNotifier_ImplementsInterface verifies the struct satisfies
// the RecoveryNotifier interface at compile time; callers can pass a
// *BusRecoveryNotifier wherever a RecoveryNotifier is expected.
func TestBusRecoveryNotifier_ImplementsInterface(t *testing.T) {
	var _ RecoveryNotifier = (*BusRecoveryNotifier)(nil)
}

func TestBusRecoveryNotifier_InjectBreakoutPromptPublishesEvent(t *testing.T) {
	sink := &capturingSink{}
	n, err := NewBusRecoveryNotifier(sink)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}

	if err := n.InjectBreakoutPrompt("engineer-1", "try a different approach"); err != nil {
		t.Fatalf("InjectBreakoutPrompt: %v", err)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Kind != RecoveryEventBreakout {
		t.Errorf("Kind = %v, want Breakout", e.Kind)
	}
	if e.AgentID != "engineer-1" {
		t.Errorf("AgentID = %q, want engineer-1", e.AgentID)
	}
	if e.Message != "try a different approach" {
		t.Errorf("Message = %q, want prompt text", e.Message)
	}
	if got, ok := e.Details[RecoveryDetailPrompt].(string); !ok || got != "try a different approach" {
		t.Errorf("Details[prompt] = %v, want the prompt text", e.Details[RecoveryDetailPrompt])
	}
}

func TestBusRecoveryNotifier_InjectBreakoutPromptRejectsEmpty(t *testing.T) {
	sink := &capturingSink{}
	n, _ := NewBusRecoveryNotifier(sink)

	if err := n.InjectBreakoutPrompt("engineer-1", "  "); err == nil {
		t.Fatal("expected error for empty prompt")
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("rejected prompt produced %d events, want 0", len(events))
	}
}

func TestBusRecoveryNotifier_EscalateToUserCarriesAssessment(t *testing.T) {
	sink := &capturingSink{}
	n, _ := NewBusRecoveryNotifier(sink)

	assessment := HealthAssessment{
		AgentID:      "engineer-1",
		OverallScore: 0.32,
		Status:       StatusStuck,
	}
	if err := n.EscalateToUser("session-1", "engineer-1", assessment); err != nil {
		t.Fatalf("EscalateToUser: %v", err)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Kind != RecoveryEventEscalation {
		t.Errorf("Kind = %v, want Escalation", e.Kind)
	}
	if e.SessionID != "session-1" {
		t.Errorf("SessionID = %q, want session-1", e.SessionID)
	}
	if !strings.Contains(e.Message, "engineer-1") || !strings.Contains(e.Message, "stuck") {
		t.Errorf("Message should include agent id and status; got %q", e.Message)
	}
	if got, ok := e.Details[RecoveryDetailOverallScore].(float64); !ok || got != 0.32 {
		t.Errorf("Details[overall_score] = %v, want 0.32", e.Details[RecoveryDetailOverallScore])
	}
}

func TestBusRecoveryNotifier_NotifyForceKillFormatsReason(t *testing.T) {
	sink := &capturingSink{}
	n, _ := NewBusRecoveryNotifier(sink)

	n.NotifyForceKill("session-1", "tester-2", "watchdog_timeout")

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Kind != RecoveryEventForceKill {
		t.Errorf("Kind = %v, want ForceKill", e.Kind)
	}
	if !strings.Contains(e.Message, "watchdog_timeout") {
		t.Errorf("Message should include reason; got %q", e.Message)
	}
	if got, ok := e.Details[RecoveryDetailReason].(string); !ok || got != "watchdog_timeout" {
		t.Errorf("Details[reason] = %v, want watchdog_timeout", e.Details[RecoveryDetailReason])
	}
}

func TestBusRecoveryNotifier_NotifyReacquireListsResources(t *testing.T) {
	sink := &capturingSink{}
	n, _ := NewBusRecoveryNotifier(sink)

	n.NotifyReacquireResources("engineer-1", []string{"llm_slot", "  ", "file_handle"})

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Kind != RecoveryEventReacquire {
		t.Errorf("Kind = %v, want Reacquire", e.Kind)
	}
	got, ok := e.Details[RecoveryDetailResources].([]string)
	if !ok {
		t.Fatalf("Details[resources] type = %T, want []string", e.Details[RecoveryDetailResources])
	}
	if len(got) != 2 {
		t.Fatalf("resources = %v, want 2 entries (blank trimmed)", got)
	}
	if got[0] != "llm_slot" || got[1] != "file_handle" {
		t.Errorf("resources = %v, want [llm_slot file_handle] with blanks dropped", got)
	}
}

func TestBusRecoveryNotifier_OnUserResponseRoundTrip(t *testing.T) {
	sink := &capturingSink{}
	n, _ := NewBusRecoveryNotifier(sink)

	n.OnUserResponse("engineer-1", UserRecoveryResponse{Action: UserActionWait, Timestamp: time.Now()})

	resp, ok := n.LastUserResponse("engineer-1")
	if !ok {
		t.Fatal("expected response to be recorded")
	}
	if resp.Action != UserActionWait {
		t.Errorf("Action = %v, want UserActionWait", resp.Action)
	}

	// OnUserResponse does not publish to the sink — verifying separately
	// because this is a state-recording call, not a user-facing event.
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("OnUserResponse should not publish; got %d events", len(events))
	}
}

func TestBusRecoveryNotifier_DeterministicTimestampWithClock(t *testing.T) {
	sink := &capturingSink{}
	n, _ := NewBusRecoveryNotifier(sink)

	fixed := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	n.SetClock(func() time.Time { return fixed })

	_ = n.InjectBreakoutPrompt("a", "hint")
	events := sink.snapshot()
	if !events[0].Timestamp.Equal(fixed) {
		t.Fatalf("Timestamp = %v, want %v (SetClock ignored)", events[0].Timestamp, fixed)
	}
}
