package steering

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// testActivityPub captures published events for verification.
type testActivityPub struct {
	mu     sync.Mutex
	events []*events.ActivityEvent
}

func (t *testActivityPub) PublishActivity(e *events.ActivityEvent) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
	return nil
}

func (t *testActivityPub) Events() []*events.ActivityEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*events.ActivityEvent, len(t.events))
	copy(out, t.events)
	return out
}

func TestLedgerPaceGetSet(t *testing.T) {
	l := NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer l.Close(false)

	if l.Pace() != PaceAuto {
		t.Errorf("default pace = %v, want PaceAuto", l.Pace())
	}

	l.SetPace(PaceStep)
	if l.Pace() != PaceStep {
		t.Errorf("pace = %v, want PaceStep", l.Pace())
	}

	l.SetPace(PacePaused)
	if l.Pace() != PacePaused {
		t.Errorf("pace = %v, want PacePaused", l.Pace())
	}
}

func TestLedgerWaitForResumeUnblocks(t *testing.T) {
	l := NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer l.Close(false)

	done := make(chan error, 1)
	go func() {
		done <- l.WaitForResume(context.Background())
	}()

	// Signal resume.
	l.DepositCommand(Command{Type: CommandResume, Timestamp: time.Now()})

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("WaitForResume returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForResume did not unblock")
	}
}

func TestLedgerWaitForResumeContextCancel(t *testing.T) {
	l := NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer l.Close(false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- l.WaitForResume(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error from cancelled context")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForResume did not return on cancel")
	}
}

func TestLedgerDepositSignalsResume(t *testing.T) {
	l := NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer l.Close(false)

	// Start waiting.
	done := make(chan struct{}, 1)
	go func() {
		_ = l.WaitForResume(context.Background())
		done <- struct{}{}
	}()

	// Deposit should signal resume.
	l.DepositCommand(Command{Type: CommandSteer, Text: "hello", Timestamp: time.Now()})

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("deposit did not unblock WaitForResume")
	}
}

func TestLedgerRecordCheckpointPublishesEvent(t *testing.T) {
	pub := &testActivityPub{}
	l := NewSteeringLedger("c1", "architect", "s1", pub, nil)
	defer l.Close(false)

	l.RecordCheckpoint(0, 3, "analyzing", nil)

	evts := pub.Events()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].EventType != events.EventTypeSteeringCheckpoint {
		t.Errorf("event type = %v, want SteeringCheckpoint", evts[0].EventType)
	}
	if evts[0].Data["turn"] != 0 {
		t.Errorf("turn = %v, want 0", evts[0].Data["turn"])
	}
	if evts[0].Data["phase"] != "analyzing" {
		t.Errorf("phase = %v, want %q", evts[0].Data["phase"], "analyzing")
	}
}

func TestLedgerSetPaceAutoSignalsResume(t *testing.T) {
	l := NewSteeringLedger("c1", "architect", "s1", nil, nil)
	defer l.Close(false)

	l.SetPace(PacePaused)

	done := make(chan struct{}, 1)
	go func() {
		_ = l.WaitForResume(context.Background())
		done <- struct{}{}
	}()

	// Switch to Auto should unblock.
	l.SetPace(PaceAuto)

	select {
	case <-done:
		// OK
	case <-time.After(2 * time.Second):
		t.Fatal("SetPace(Auto) did not unblock WaitForResume")
	}
}

func TestLedgerCloseWithJournal(t *testing.T) {
	j := newTestJournal(t)
	l := NewSteeringLedger("c1", "architect", "s1", nil, j)

	l.RecordCheckpoint(0, 2, "a", nil)
	l.RecordCheckpoint(1, 5, "b", nil)
	l.Close(false)

	// Verify the Begin + Complete pair — no incomplete operations.
	incomplete := j.FindIncompleteOperations()
	if len(incomplete) != 0 {
		t.Fatalf("expected 0 incomplete after Close(false), got %d", len(incomplete))
	}
}

func TestLedgerCloseInterrupted(t *testing.T) {
	j := newTestJournal(t)
	l := NewSteeringLedger("c2", "engineer", "s2", nil, j)

	l.RecordCheckpoint(0, 2, "analyzing", nil)
	l.Close(true)

	// Should still be paired (interrupted closes the bracket).
	incomplete := j.FindIncompleteOperations()
	if len(incomplete) != 0 {
		t.Fatalf("expected 0 incomplete after Close(true), got %d", len(incomplete))
	}
}
