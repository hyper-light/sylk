package architect

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReaper_ClassifyAction_TerminalExpired(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	r := &PlanReaper{leaseManager: lm}

	plan := &DesignPlan{
		ID:        "plan-1",
		UpdatedAt: time.Now().Add(-lm.terminalRetention - time.Second),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusCompleted)

	action := r.classifyAction(plan)
	if action != reaperActionEvictTerminal {
		t.Fatalf("expected evictTerminal, got %d", action)
	}
}

func TestReaper_ClassifyAction_ReadyExpired(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	r := &PlanReaper{leaseManager: lm}

	plan := &DesignPlan{
		ID:          "plan-1",
		LeaseExpiry: time.Now().Add(-time.Minute),
		UpdatedAt:   time.Now(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusReady)

	action := r.classifyAction(plan)
	if action != reaperActionExpireReady {
		t.Fatalf("expected expireReady, got %d", action)
	}
}

func TestReaper_ClassifyAction_ExecutingExpired(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	r := &PlanReaper{leaseManager: lm}

	plan := &DesignPlan{
		ID:          "plan-1",
		LeaseExpiry: time.Now().Add(-time.Minute),
		UpdatedAt:   time.Now(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusExecuting)

	action := r.classifyAction(plan)
	if action != reaperActionExpireExecuting {
		t.Fatalf("expected expireExecuting, got %d", action)
	}
}

func TestReaper_ClassifyAction_None(t *testing.T) {
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	r := &PlanReaper{leaseManager: lm}

	plan := &DesignPlan{
		ID:          "plan-1",
		LeaseExpiry: time.Now().Add(time.Hour),
		UpdatedAt:   time.Now(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusReady)

	action := r.classifyAction(plan)
	if action != reaperActionNone {
		t.Fatalf("expected none, got %d", action)
	}
}

func TestReaper_RemoveDiskFile(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	planID := "test-reaper-plan"

	// Create a plan file in the store's directory.
	planDir := store.PlanDir("")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(planDir, planID+".json")
	if err := os.WriteFile(planFile, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store.RemoveDiskFile(planID, "")

	if _, err := os.Stat(planFile); !os.IsNotExist(err) {
		t.Fatal("expected plan file to be removed")
	}
}

func TestReaper_StartStop(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	r := NewPlanReaper(store, lm, slog.Default())

	r.Start()
	time.Sleep(10 * time.Millisecond) // let the goroutine spin up
	r.Stop()                           // should return without blocking

	// Verify stop was clean (done channel closed).
	select {
	case <-r.done:
		// ok
	default:
		t.Fatal("expected done channel to be closed")
	}
}
