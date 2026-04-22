package orchestrator

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestDispatchHoldGateBlocksUntilStoreHoldResolves verifies that the
// gate is a faithful projection of the store: inserting an active hold
// blocks wait(); updating that hold to resolved unblocks it, via the
// store's notification channel (no polling).
func TestDispatchHoldGateBlocksUntilStoreHoldResolves(t *testing.T) {
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	gate := newDispatchHoldGate(store)
	hold := &ExecutionHoldRecord{
		HoldID:    "hold_A",
		SessionID: "sess",
		PlanID:    "plan-A",
		Status:    ExecutionHoldStatusActive,
		Reason:    "blocking",
	}
	if err := store.CreateExecutionHold(hold); err != nil {
		t.Fatalf("create hold: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- gate.wait(context.Background(), "sess", "plan-A", "dag-1")
	}()

	select {
	case err := <-done:
		t.Fatalf("wait returned too early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	hold.Status = ExecutionHoldStatusResolved
	if err := store.UpdateExecutionHold(hold); err != nil {
		t.Fatalf("update hold: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not unblock after store hold resolved")
	}
}

// TestDispatchHoldGateDoesNotBlockDifferentPlan pins the plan-scope
// invariant: a hold under plan-A must not block a DAG submitted under
// plan-B. This is the exact regression that drove the plan_id
// redesign.
func TestDispatchHoldGateDoesNotBlockDifferentPlan(t *testing.T) {
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	if err := store.CreateExecutionHold(&ExecutionHoldRecord{
		HoldID:    "hold_A",
		SessionID: "sess",
		PlanID:    "plan-A",
		Status:    ExecutionHoldStatusActive,
	}); err != nil {
		t.Fatalf("create hold: %v", err)
	}

	gate := newDispatchHoldGate(store)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.wait(ctx, "sess", "plan-B", "dag-elsewhere"); err != nil {
		t.Fatalf("wait under plan-B blocked by plan-A hold: %v", err)
	}
}

// TestDispatchHoldGateAllowsExemptDAG verifies ExemptDAGIDs: a
// remediation DAG exempted from an active hold passes the gate while
// every other DAG in the plan is still blocked.
func TestDispatchHoldGateAllowsExemptDAG(t *testing.T) {
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	if err := store.CreateExecutionHold(&ExecutionHoldRecord{
		HoldID:       "hold_A",
		SessionID:    "sess",
		PlanID:       "plan-A",
		Status:       ExecutionHoldStatusActive,
		ExemptDAGIDs: []string{"dag-remediation"},
	}); err != nil {
		t.Fatalf("create hold: %v", err)
	}

	gate := newDispatchHoldGate(store)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.wait(ctx, "sess", "plan-A", "dag-remediation"); err != nil {
		t.Fatalf("wait on exempt dag: %v", err)
	}
	if gate.isHeld("sess", "plan-A", "dag-remediation") {
		t.Fatal("exempt dag should not be reported as held")
	}
	if !gate.isHeld("sess", "plan-A", "dag-blocked") {
		t.Fatal("non-exempt dag should still be reported as held")
	}
}

// TestDispatchHoldGateApprovalHoldInMemory keeps coverage on the
// in-memory per-DAG approval hold path, which intentionally lives
// outside the store.
func TestDispatchHoldGateApprovalHoldInMemory(t *testing.T) {
	gate := newDispatchHoldGate(nil)
	gate.activateDAG("sess", "dag-1", "hold-1")

	if err := gate.wait(context.Background(), "sess", "", "dag-2"); err != nil {
		t.Fatalf("wait on unrelated dag returned error: %v", err)
	}
	if !gate.isHeld("sess", "", "dag-1") {
		t.Fatal("expected dag-specific hold to be active")
	}

	done := make(chan error, 1)
	go func() {
		done <- gate.wait(context.Background(), "sess", "", "dag-1")
	}()

	select {
	case err := <-done:
		t.Fatalf("wait returned too early for held dag: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	gate.resolveHold("sess", "hold-1")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait returned error after hold resolve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not unblock after dag hold resolve")
	}
}
