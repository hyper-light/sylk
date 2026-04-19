package recovery

import (
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

// recordingDeadlockSink captures every result the monitor publishes.
type recordingDeadlockSink struct {
	mu      sync.Mutex
	results []DeadlockResult
}

func (r *recordingDeadlockSink) PublishDeadlockResult(res DeadlockResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
}

func (r *recordingDeadlockSink) snapshot() []DeadlockResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]DeadlockResult, len(r.results))
	copy(out, r.results)
	return out
}

// setupDeadlockMonitor builds a HealthMonitor wired with a
// DeadlockDetector ready to surface a dead-holder deadlock: the
// "alive-agent" is waiting on a resource held by "dead-agent", and
// the statusFn marks "dead-agent" as not-alive.
func setupDeadlockMonitor(t *testing.T, deadlockSink DeadlockResultSink) (*HealthMonitor, *DeadlockDetector) {
	t.Helper()

	pc := NewProgressCollector()
	rd := NewRepetitionDetector(DefaultRepetitionConfig())
	hs := NewHealthScorer(pc, rd, nil, DefaultHealthWeights(), DefaultHealthThresholds())
	dd := NewDeadlockDetectorWithConfig(
		func(agentID string) bool { return agentID != "dead-agent" },
		DeadlockConfig{
			ConfirmationChecks: 1,
			ConfirmationDelay:  time.Millisecond,
		},
	)

	notifier := &testNotifier{}
	terminator := &mockTerminator{}
	releaser := &mockReleaser{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	config := RecoveryConfig{
		SoftInterventionDelay: 100 * time.Millisecond,
		UserEscalationDelay:   200 * time.Millisecond,
		ForceKillDelay:        400 * time.Millisecond,
		MaxSoftAttempts:       2,
		MonitorInterval:       50 * time.Millisecond,
	}

	ro := NewRecoveryOrchestrator(hs, dd, releaser, terminator, notifier, logger, config)
	dr := NewDeadlockRecovery(ro, pc, releaser, notifier, logger)
	agents := &mockAgentEnumerator{}
	hm := NewHealthMonitor(hs, dd, ro, dr, agents, config, logger)
	if deadlockSink != nil {
		hm.SetDeadlockSink(deadlockSink)
	}

	return hm, dd
}

// TestHealthMonitor_PublishesDeadlockOnDetection is the REC-03
// acceptance test: when DeadlockDetector returns a detected result,
// the sink receives it BEFORE the recovery path decides to act. A
// regression here hides detection from the UI until recovery fires,
// which may never happen for transient deadlocks.
func TestHealthMonitor_PublishesDeadlockOnDetection(t *testing.T) {
	sink := &recordingDeadlockSink{}
	hm, dd := setupDeadlockMonitor(t, sink)

	// Alive agent waits on a resource held by a dead agent —
	// dead-holder deadlock. Detector should flag this on Check.
	dd.RegisterWait("alive-agent", "dead-agent", "resource", "res-1")

	hm.checkDeadlocks()

	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("sink saw %d results, want 1", len(got))
	}
	if !got[0].Detected {
		t.Errorf("published result had Detected=false")
	}
	if got[0].Type != DeadlockDeadHolder {
		t.Errorf("Type = %v, want DeadlockDeadHolder", got[0].Type)
	}
	if got[0].DeadHolder != "dead-agent" {
		t.Errorf("DeadHolder = %q, want dead-agent", got[0].DeadHolder)
	}
}

// TestHealthMonitor_DeadlockSink_NilIsNoop protects the
// bus-bootstrap window where the monitor may be ticking before a
// sink is wired. checkDeadlocks must tolerate a nil sink.
func TestHealthMonitor_DeadlockSink_NilIsNoop(t *testing.T) {
	hm, dd := setupDeadlockMonitor(t, nil)
	dd.RegisterWait("alive-agent", "dead-agent", "resource", "res-x")

	// Must not panic.
	hm.checkDeadlocks()
}

// TestHealthMonitor_DeadlockSink_NoEmissionWhenNoDeadlock verifies
// the sink stays silent when Check returns non-detected results.
// Otherwise a subscriber would see "deadlock" spam every tick.
func TestHealthMonitor_DeadlockSink_NoEmissionWhenNoDeadlock(t *testing.T) {
	sink := &recordingDeadlockSink{}
	hm, _ := setupDeadlockMonitor(t, sink)

	hm.checkDeadlocks() // no waits registered → no deadlocks

	if got := len(sink.snapshot()); got != 0 {
		t.Errorf("sink saw %d results without a deadlock, want 0", got)
	}
}

// reacquireCapturingSink records every RecoveryEvent — used by the
// REC-05 resource-reacquire test to prove the end-to-end event
// payload reaches the bus sink with the expected resource list.
type reacquireCapturingSink struct {
	mu     sync.Mutex
	events []RecoveryEvent
}

func (s *reacquireCapturingSink) PublishRecoveryEvent(event RecoveryEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

// TestResourceReacquisition_NotifyAndReacquire_FiresNotifierThenAcquires
// is a REC-05 acceptance test: the orchestration wrapper fires the
// notifier's reacquire event FIRST (so the UI sees the handshake
// intent), then invokes the resource manager. Uses BusRecoveryNotifier
// over a capturing sink so we see the full RecoveryEvent payload —
// resource list is what this test actually guards.
func TestResourceReacquisition_NotifyAndReacquire_FiresNotifierThenAcquires(t *testing.T) {
	sink := &reacquireCapturingSink{}
	notifier, err := NewBusRecoveryNotifier(sink)
	if err != nil {
		t.Fatalf("NewBusRecoveryNotifier: %v", err)
	}
	acq := &rec05TestAcquirer{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	rr := NewResourceReacquisition(acq, notifier, logger)
	if err := rr.NotifyAndReacquire("engineer-1", []string{"llm_slot", "file_handle"}); err != nil {
		t.Fatalf("NotifyAndReacquire: %v", err)
	}

	sink.mu.Lock()
	events := append([]RecoveryEvent(nil), sink.events...)
	sink.mu.Unlock()

	if len(events) != 1 {
		t.Fatalf("sink saw %d events, want 1", len(events))
	}
	if events[0].Kind != RecoveryEventReacquire {
		t.Errorf("event Kind = %q, want reacquire", events[0].Kind)
	}
	if events[0].AgentID != "engineer-1" {
		t.Errorf("event AgentID = %q, want engineer-1", events[0].AgentID)
	}
	resources, _ := events[0].Details[RecoveryDetailResources].([]string)
	if len(resources) != 2 {
		t.Errorf("event Resources = %v, want 2 items", resources)
	}
	if acq.calls != 2 {
		t.Errorf("acquirer call count = %d, want 2", acq.calls)
	}
}

// rec05TestAcquirer counts Acquire calls and succeeds on every request.
// Used only by the REC-05 tests above; keeps us from pulling in a
// real resource manager.
type rec05TestAcquirer struct {
	mu    sync.Mutex
	calls int
}

func (m *rec05TestAcquirer) Acquire(_, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	return nil
}
