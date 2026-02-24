package handoff

import (
	"sync"
	"testing"
	"time"
)

func TestTrafficShiftController_BeginShift(t *testing.T) {
	var weightUpdates []weightUpdate
	var mu sync.Mutex

	ctrl := NewTrafficShiftController(
		ShiftConfig{
			InitialWeight:    0.1,
			WeightStep:       0.1,
			EvalInterval:     10 * time.Millisecond,
			MinNewAgentTurns: 2,
			QualityThreshold: 0.5,
			MaxShiftDuration: 5 * time.Second,
		},
		func(agentID string, weight float64) {
			mu.Lock()
			weightUpdates = append(weightUpdates, weightUpdate{agentID, weight})
			mu.Unlock()
		},
		func(_ string) (float64, float64, bool) { return 0.8, 0.6, true },
		func(_, _ string) {},
		func(_, _ string) {},
		nil,
	)

	if err := ctrl.BeginShift("old-1", "new-1"); err != nil {
		t.Fatalf("BeginShift failed: %v", err)
	}

	if phase := ctrl.Phase(); phase != ShiftCanary {
		t.Fatalf("expected ShiftCanary, got %s", phase)
	}

	mu.Lock()
	if len(weightUpdates) < 2 {
		t.Fatalf("expected at least 2 weight updates (old+new), got %d", len(weightUpdates))
	}
	mu.Unlock()

	ctrl.Stop()
	<-ctrl.Done()
}

func TestTrafficShiftController_DoubleBeginFails(t *testing.T) {
	ctrl := NewTrafficShiftController(
		ShiftConfig{
			InitialWeight:    0.1,
			WeightStep:       0.1,
			EvalInterval:     1 * time.Hour,
			MinNewAgentTurns: 10,
			QualityThreshold: 0.5,
			MaxShiftDuration: 1 * time.Hour,
		},
		func(string, float64) {},
		func(string) (float64, float64, bool) { return 0, 0, false },
		func(string, string) {},
		func(string, string) {},
		nil,
	)
	defer ctrl.Stop()

	if err := ctrl.BeginShift("old-1", "new-1"); err != nil {
		t.Fatalf("first BeginShift: %v", err)
	}

	if err := ctrl.BeginShift("old-2", "new-2"); err == nil {
		t.Fatal("expected error on double BeginShift")
	}

	ctrl.Stop()
	<-ctrl.Done()
}

func TestTrafficShiftController_ConvergesOnGoodQuality(t *testing.T) {
	var completedOld, completedNew string

	ctrl := NewTrafficShiftController(
		ShiftConfig{
			InitialWeight:    0.5,
			WeightStep:       0.5,
			EvalInterval:     10 * time.Millisecond,
			MinNewAgentTurns: 1,
			QualityThreshold: 0.3,
			MaxShiftDuration: 5 * time.Second,
		},
		func(string, float64) {},
		func(_ string) (float64, float64, bool) {
			return 0.9, 0.7, true // Good quality
		},
		func(oldID, newID string) {
			completedOld = oldID
			completedNew = newID
		},
		func(_, _ string) { t.Error("unexpected abort") },
		nil,
	)

	if err := ctrl.BeginShift("old", "new"); err != nil {
		t.Fatalf("BeginShift: %v", err)
	}

	// Record enough turns.
	ctrl.RecordNewAgentTurn()
	ctrl.RecordNewAgentTurn()

	// Wait for convergence.
	select {
	case <-ctrl.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for convergence")
	}

	if ctrl.Phase() != ShiftConverged {
		t.Fatalf("expected ShiftConverged, got %s", ctrl.Phase())
	}
	if completedOld != "old" || completedNew != "new" {
		t.Fatalf("onComplete called with wrong args: old=%q new=%q", completedOld, completedNew)
	}
}

func TestTrafficShiftController_AbortsOnLowQuality(t *testing.T) {
	aborted := make(chan struct{})

	ctrl := NewTrafficShiftController(
		ShiftConfig{
			InitialWeight:    0.1,
			WeightStep:       0.1,
			EvalInterval:     10 * time.Millisecond,
			MinNewAgentTurns: 1,
			QualityThreshold: 0.7,
			MaxShiftDuration: 5 * time.Second,
		},
		func(string, float64) {},
		func(_ string) (float64, float64, bool) {
			return 0.3, 0.1, true // Bad quality — lower_bound (0.1) < threshold (0.7)
		},
		func(_, _ string) { t.Error("unexpected complete") },
		func(_, _ string) { close(aborted) },
		nil,
	)

	if err := ctrl.BeginShift("old", "new"); err != nil {
		t.Fatalf("BeginShift: %v", err)
	}

	// Record enough turns for evaluation.
	ctrl.RecordNewAgentTurn()
	ctrl.RecordNewAgentTurn()

	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for abort")
	}

	<-ctrl.Done()
	if ctrl.Phase() != ShiftAborted {
		t.Fatalf("expected ShiftAborted, got %s", ctrl.Phase())
	}
}

func TestTrafficShiftController_AbortsOnTimeout(t *testing.T) {
	aborted := make(chan struct{}, 1)

	ctrl := NewTrafficShiftController(
		ShiftConfig{
			InitialWeight:    0.1,
			WeightStep:       0.1,
			EvalInterval:     5 * time.Millisecond,
			MinNewAgentTurns: 1000, // Will never reach.
			QualityThreshold: 0.5,
			MaxShiftDuration: 20 * time.Millisecond,
		},
		func(string, float64) {},
		func(_ string) (float64, float64, bool) { return 0, 0, false },
		func(_, _ string) {},
		func(_, _ string) {
			select {
			case aborted <- struct{}{}:
			default:
			}
		},
		nil,
	)

	if err := ctrl.BeginShift("old", "new"); err != nil {
		t.Fatalf("BeginShift: %v", err)
	}

	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for timeout abort")
	}

	<-ctrl.Done()
	if ctrl.Phase() != ShiftAborted {
		t.Fatalf("expected ShiftAborted, got %s", ctrl.Phase())
	}
}

func TestTrafficShiftController_WALWritten(t *testing.T) {
	var walEntries []ShiftWALEntry
	var mu sync.Mutex

	ctrl := NewTrafficShiftController(
		ShiftConfig{
			InitialWeight:    0.5,
			WeightStep:       0.5,
			EvalInterval:     10 * time.Millisecond,
			MinNewAgentTurns: 1,
			QualityThreshold: 0.3,
			MaxShiftDuration: 5 * time.Second,
		},
		func(string, float64) {},
		func(_ string) (float64, float64, bool) { return 0.9, 0.7, true },
		func(_, _ string) {},
		func(_, _ string) {},
		func(entry ShiftWALEntry) {
			mu.Lock()
			walEntries = append(walEntries, entry)
			mu.Unlock()
		},
	)

	if err := ctrl.BeginShift("old", "new"); err != nil {
		t.Fatalf("BeginShift: %v", err)
	}

	ctrl.RecordNewAgentTurn()
	ctrl.RecordNewAgentTurn()

	<-ctrl.Done()

	mu.Lock()
	defer mu.Unlock()

	if len(walEntries) < 2 {
		t.Fatalf("expected at least 2 WAL entries (begin+converge), got %d", len(walEntries))
	}

	if walEntries[0].Phase != ShiftCanary {
		t.Fatalf("first WAL entry should be Canary, got %s", walEntries[0].Phase)
	}

	last := walEntries[len(walEntries)-1]
	if last.Phase != ShiftConverged {
		t.Fatalf("last WAL entry should be Converged, got %s", last.Phase)
	}
}

func TestTrafficShiftController_RecordNewAgentTurn(t *testing.T) {
	ctrl := NewTrafficShiftController(
		ShiftConfig{EvalInterval: 1 * time.Hour, MaxShiftDuration: 1 * time.Hour},
		func(string, float64) {},
		func(string) (float64, float64, bool) { return 0, 0, false },
		func(string, string) {},
		func(string, string) {},
		nil,
	)

	ctrl.RecordNewAgentTurn()
	ctrl.RecordNewAgentTurn()
	ctrl.RecordNewAgentTurn()

	if got := ctrl.newAgentTurns.Load(); got != 3 {
		t.Fatalf("expected 3 turns, got %d", got)
	}
}

func TestShiftPhase_String(t *testing.T) {
	tests := []struct {
		phase ShiftPhase
		want  string
	}{
		{ShiftIdle, "idle"},
		{ShiftCanary, "canary"},
		{ShiftRamping, "ramping"},
		{ShiftConverged, "converged"},
		{ShiftAborted, "aborted"},
		{ShiftPhase(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.phase.String(); got != tt.want {
			t.Errorf("ShiftPhase(%d).String() = %q, want %q", tt.phase, got, tt.want)
		}
	}
}

type weightUpdate struct {
	agentID string
	weight  float64
}
