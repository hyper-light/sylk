package orchestrator

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestSupersedePriorPlans_MarksOtherPlansAndClearsGate pins the
// plan-takeover invariant: when a new plan_id enters the session,
// active holds from any other plan_id transition to
// ExecutionHoldStatusSuperseded, and the dispatch gate immediately
// reflects that transition (no restart, no polling). This is the
// exact regression the plan_id redesign addresses.
func TestSupersedePriorPlans_MarksOtherPlansAndClearsGate(t *testing.T) {
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	priorHold := &ExecutionHoldRecord{
		HoldID:    "hold_prior",
		SessionID: "sess",
		PlanID:    "plan-A",
		Status:    ExecutionHoldStatusActive,
		Reason:    "blocking_failure",
	}
	if err := store.CreateExecutionHold(priorHold); err != nil {
		t.Fatalf("create prior hold: %v", err)
	}

	gate := newDispatchHoldGate(store)
	if !gate.isHeld("sess", "plan-A", "dag-anything") {
		t.Fatal("expected plan-A hold to block plan-A DAG before supersede")
	}

	// A fresh plan enters the session. Supersede must clear every
	// active hold whose plan_id != plan-B.
	count, err := store.SupersedePriorPlans(context.Background(), "sess", "plan-B")
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if count != 1 {
		t.Fatalf("superseded count = %d, want 1", count)
	}

	got, err := store.GetExecutionHold("hold_prior")
	if err != nil {
		t.Fatalf("get hold: %v", err)
	}
	if got == nil || got.Status != ExecutionHoldStatusSuperseded {
		t.Fatalf("hold status = %v, want superseded", got)
	}

	// The gate is a projection — it should now clear a wait for
	// plan-B DAGs immediately, and no longer report plan-A DAGs as
	// held either.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.wait(ctx, "sess", "plan-B", "dag-new"); err != nil {
		t.Fatalf("wait under plan-B after supersede: %v", err)
	}
	if gate.isHeld("sess", "plan-A", "dag-old") {
		t.Fatal("expected superseded hold to no longer report as held")
	}
}

// TestSupersedePriorPlans_PreservesSamePlan_Holds verifies the
// negative case: a supersede called with the same plan_id already on
// the active hold is a no-op. New dispatchers under that plan remain
// blocked, as they should be.
func TestSupersedePriorPlans_PreservesSamePlan_Holds(t *testing.T) {
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

	count, err := store.SupersedePriorPlans(context.Background(), "sess", "plan-A")
	if err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if count != 0 {
		t.Fatalf("superseded count = %d, want 0 (same plan)", count)
	}

	got, err := store.GetExecutionHold("hold_A")
	if err != nil {
		t.Fatalf("get hold: %v", err)
	}
	if got.Status != ExecutionHoldStatusActive {
		t.Fatalf("hold status = %v, want still active", got.Status)
	}
}

// TestDAGIsHeld_ExemptDAGBypassesHold pins the exemption semantics:
// a DAG listed in ExemptDAGIDs is not held, even when the hold's
// BlocksDAGIDs is empty (which otherwise means "block every DAG in
// the plan").
func TestDAGIsHeld_ExemptDAGBypassesHold(t *testing.T) {
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

	held, _, _, err := store.DAGIsHeld(context.Background(), "sess", "plan-A", "dag-remediation")
	if err != nil {
		t.Fatalf("DAGIsHeld: %v", err)
	}
	if held {
		t.Fatal("exempt DAG should not be held")
	}
	held, _, _, err = store.DAGIsHeld(context.Background(), "sess", "plan-A", "dag-other")
	if err != nil {
		t.Fatalf("DAGIsHeld: %v", err)
	}
	if !held {
		t.Fatal("non-exempt DAG should be held")
	}
}

// TestDAGIsHeld_BlocksDAGIDsConstrain pins scoped-hold semantics: a
// hold with a non-empty BlocksDAGIDs only blocks the listed DAGs.
func TestDAGIsHeld_BlocksDAGIDsConstrain(t *testing.T) {
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
		BlocksDAGIDs: []string{"dag-1"},
	}); err != nil {
		t.Fatalf("create hold: %v", err)
	}

	held, _, _, err := store.DAGIsHeld(context.Background(), "sess", "plan-A", "dag-1")
	if err != nil {
		t.Fatalf("DAGIsHeld: %v", err)
	}
	if !held {
		t.Fatal("dag-1 should be held (in BlocksDAGIDs)")
	}
	held, _, _, err = store.DAGIsHeld(context.Background(), "sess", "plan-A", "dag-2")
	if err != nil {
		t.Fatalf("DAGIsHeld: %v", err)
	}
	if held {
		t.Fatal("dag-2 should not be held (not in BlocksDAGIDs)")
	}
}

// TestHoldsNotifyChannel_UnblocksGateOnWrite verifies the
// notification pipe that makes the gate responsive without polling.
func TestHoldsNotifyChannel_UnblocksGateOnWrite(t *testing.T) {
	store, err := OpenStore(DefaultStoreConfig(filepath.Join(t.TempDir(), "orchestrator.db")))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	ch := store.HoldsNotifyChannel("sess")

	// Writing a hold for this session must close the channel.
	if err := store.CreateExecutionHold(&ExecutionHoldRecord{
		HoldID:    "hold_A",
		SessionID: "sess",
		PlanID:    "plan-A",
		Status:    ExecutionHoldStatusActive,
	}); err != nil {
		t.Fatalf("create hold: %v", err)
	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("HoldsNotifyChannel did not close on hold insert")
	}

	// A fresh channel is minted for the next waiter.
	next := store.HoldsNotifyChannel("sess")
	select {
	case <-next:
		t.Fatal("fresh channel should not be pre-closed")
	default:
	}
}

// TestEnsureExecutionHoldSchema_SupersedesLegacyRows pins the
// migration invariant: any active hold missing a plan_id at Migrate()
// time is atomically superseded, eliminating the bootstrap-leak bug.
func TestEnsureExecutionHoldSchema_SupersedesLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestrator.db")
	store, err := OpenStore(DefaultStoreConfig(path))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	// Simulate a legacy active hold with no plan_id by writing
	// directly and then blanking the column — mirrors the pre-
	// redesign row shape.
	if err := store.CreateExecutionHold(&ExecutionHoldRecord{
		HoldID:    "hold_legacy",
		SessionID: "sess",
		PlanID:    "",
		Status:    ExecutionHoldStatusActive,
	}); err != nil {
		t.Fatalf("create legacy hold: %v", err)
	}
	if _, err := store.db.SQLDB().Exec(
		"UPDATE execution_holds SET plan_id = NULL WHERE hold_id = ?",
		"hold_legacy",
	); err != nil {
		t.Fatalf("null out plan_id: %v", err)
	}

	// Re-run the migration to trigger the supersede sweep.
	if err := store.ensureExecutionHoldSchema(); err != nil {
		t.Fatalf("re-run schema migration: %v", err)
	}

	got, err := store.GetExecutionHold("hold_legacy")
	if err != nil {
		t.Fatalf("get hold: %v", err)
	}
	if got == nil {
		t.Fatal("hold vanished")
	}
	if got.Status != ExecutionHoldStatusSuperseded {
		t.Fatalf("status = %v, want superseded", got.Status)
	}
}
