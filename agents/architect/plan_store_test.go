package architect

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testPlanStore(t *testing.T) *PlanStore {
	t.Helper()
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)
	return NewPlanStore(t.TempDir(), lm, slog.Default())
}

func TestPlanStore_UpsertAndGet(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	plan := &DesignPlan{
		ID:        "plan-1",
		SessionID: "sess-1",
		Status:    PlanStatusPending,
		UpdatedAt: time.Now(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusPending)

	if err := store.Upsert(plan); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	got := store.Get("plan-1")
	if got == nil {
		t.Fatal("expected to find plan-1")
	}
	if got.ID != "plan-1" {
		t.Errorf("expected plan-1, got %s", got.ID)
	}

	// Verify disk persistence.
	diskPath := filepath.Join(store.PlanDir("sess-1"), "plan-1.json")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		t.Error("expected plan file on disk")
	}
}

func TestPlanStore_Get_Missing(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	if store.Get("nonexistent") != nil {
		t.Error("expected nil for missing plan")
	}
}

func TestPlanStore_Remove(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	plan := &DesignPlan{
		ID:        "plan-rm",
		SessionID: "sess-1",
		Status:    PlanStatusPending,
		UpdatedAt: time.Now(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusPending)

	_ = store.Upsert(plan)
	store.Remove("plan-rm")

	if store.Get("plan-rm") != nil {
		t.Error("expected plan to be removed")
	}
	if store.Count() != 0 {
		t.Errorf("expected 0 plans, got %d", store.Count())
	}
}

func TestPlanStore_Snapshot(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	for i := range 3 {
		p := &DesignPlan{
			ID:        "plan-" + string(rune('a'+i)),
			SessionID: "sess-1",
			Status:    PlanStatusPending,
			UpdatedAt: time.Now(),
		}
		p.sm = NewPlanStateMachine(p.ID, PlanStatusPending)
		_ = store.Upsert(p)
	}

	snap := store.Snapshot()
	if len(snap) != 3 {
		t.Errorf("expected 3, got %d", len(snap))
	}
}

func TestPlanStore_LatestByStatus(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	now := time.Now()
	old := &DesignPlan{
		ID:        "old",
		SessionID: "sess-1",
		Status:    PlanStatusReady,
		UpdatedAt: now.Add(-5 * time.Minute),
	}
	old.sm = NewPlanStateMachine(old.ID, PlanStatusReady)

	recent := &DesignPlan{
		ID:        "recent",
		SessionID: "sess-1",
		Status:    PlanStatusReady,
		UpdatedAt: now.Add(-1 * time.Minute),
	}
	recent.sm = NewPlanStateMachine(recent.ID, PlanStatusReady)

	other := &DesignPlan{
		ID:        "other-session",
		SessionID: "sess-2",
		Status:    PlanStatusReady,
		UpdatedAt: now,
	}
	other.sm = NewPlanStateMachine(other.ID, PlanStatusReady)

	_ = store.Upsert(old)
	_ = store.Upsert(recent)
	_ = store.Upsert(other)

	// Should find 'recent' for sess-1.
	got := store.LatestByStatus("sess-1", PlanStatusReady, ReadyPlanMaxAge)
	if got == nil || got.ID != "recent" {
		t.Errorf("expected 'recent', got %v", got)
	}

	// Should find 'other-session' for sess-2.
	got = store.LatestByStatus("sess-2", PlanStatusReady, ReadyPlanMaxAge)
	if got == nil || got.ID != "other-session" {
		t.Errorf("expected 'other-session', got %v", got)
	}

	// Empty session returns nil.
	if store.LatestByStatus("", PlanStatusReady, ReadyPlanMaxAge) != nil {
		t.Error("expected nil for empty session")
	}

	// Unknown session returns nil.
	if store.LatestByStatus("no-such", PlanStatusReady, ReadyPlanMaxAge) != nil {
		t.Error("expected nil for unknown session")
	}

	// Wrong status returns nil.
	if store.LatestByStatus("sess-1", PlanStatusAnalyzing, ReadyPlanMaxAge) != nil {
		t.Error("expected nil for non-matching status")
	}
}

func TestPlanStore_LatestByStatus_Expired(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	expired := &DesignPlan{
		ID:        "expired",
		SessionID: "sess-1",
		Status:    PlanStatusReady,
		UpdatedAt: time.Now().Add(-2 * time.Hour),
	}
	expired.sm = NewPlanStateMachine(expired.ID, PlanStatusReady)
	_ = store.Upsert(expired)

	if store.LatestByStatus("sess-1", PlanStatusReady, ReadyPlanMaxAge) != nil {
		t.Error("expected nil for expired plan")
	}
}

func TestPlanStore_LatestReusableForRequest(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	now := time.Now()
	old := &DesignPlan{
		ID:                   "old",
		SessionID:            "sess-1",
		Status:               PlanStatusGenerating,
		RequestCorrelationID: "corr-1",
		UpdatedAt:            now.Add(-2 * time.Minute),
	}
	old.sm = NewPlanStateMachine(old.ID, old.Status)
	reusable := &DesignPlan{
		ID:                   "ready",
		SessionID:            "sess-1",
		Status:               PlanStatusReady,
		RequestCorrelationID: "corr-1",
		UpdatedAt:            now.Add(-30 * time.Second),
	}
	reusable.sm = NewPlanStateMachine(reusable.ID, reusable.Status)
	terminal := &DesignPlan{
		ID:                   "done",
		SessionID:            "sess-1",
		Status:               PlanStatusCompleted,
		RequestCorrelationID: "corr-1",
		UpdatedAt:            now,
	}
	terminal.sm = NewPlanStateMachine(terminal.ID, terminal.Status)
	other := &DesignPlan{
		ID:                   "other",
		SessionID:            "sess-1",
		Status:               PlanStatusConsulting,
		RequestCorrelationID: "corr-2",
		UpdatedAt:            now,
	}
	other.sm = NewPlanStateMachine(other.ID, other.Status)

	_ = store.Upsert(old)
	_ = store.Upsert(reusable)
	_ = store.Upsert(terminal)
	_ = store.Upsert(other)

	got := store.LatestReusableForRequest("sess-1", "corr-1")
	if got == nil || got.ID != "ready" {
		t.Fatalf("LatestReusableForRequest = %v, want ready", got)
	}
}

func TestPlanStore_LatestStalledForRequest(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	now := time.Now()
	wanted := &DesignPlan{
		ID:                   "stalled",
		SessionID:            "sess-1",
		Status:               PlanStatusConsulting,
		RequestCorrelationID: "corr-1",
		UpdatedAt:            now,
	}
	wanted.sm = NewPlanStateMachine(wanted.ID, wanted.Status)
	otherRequest := &DesignPlan{
		ID:                   "other-request",
		SessionID:            "sess-1",
		Status:               PlanStatusGenerating,
		RequestCorrelationID: "corr-2",
		UpdatedAt:            now.Add(5 * time.Second),
	}
	otherRequest.sm = NewPlanStateMachine(otherRequest.ID, otherRequest.Status)
	old := &DesignPlan{
		ID:                   "old",
		SessionID:            "sess-1",
		Status:               PlanStatusAnalyzing,
		RequestCorrelationID: "corr-1",
		UpdatedAt:            now.Add(-10 * time.Minute),
	}
	old.sm = NewPlanStateMachine(old.ID, old.Status)

	_ = store.Upsert(wanted)
	_ = store.Upsert(otherRequest)
	_ = store.Upsert(old)

	got := store.LatestStalledForRequest("sess-1", "corr-1", stalledPlanMaxAge)
	if got == nil || got.ID != "stalled" {
		t.Fatalf("LatestStalledForRequest = %v, want stalled", got)
	}
}

func TestPlanStore_RestoreFromDisk(t *testing.T) {
	dir := t.TempDir()
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)

	// Pre-seed a plan file on disk.
	now := time.Now()
	plan := DesignPlan{
		ID:        "restored-1",
		SessionID: "sess-1",
		Status:    PlanStatusReady,
		Epoch:     5,
		UpdatedAt: now.Add(-2 * time.Minute),
		CreatedAt: now.Add(-5 * time.Minute),
	}
	planDir := filepath.Join(dir, ".sylk", "sessions", "sess-1", "agents", "architect", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.MarshalIndent(&plan, "", "  ")
	if err := os.WriteFile(filepath.Join(planDir, "restored-1.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create store — should restore the plan.
	store := NewPlanStore(dir, lm, slog.Default())
	defer store.Close()

	got := store.Get("restored-1")
	if got == nil {
		t.Fatal("expected restored plan")
	}
	if got.SM().State() != PlanStatusReady {
		t.Errorf("expected Ready, got %s", got.SM().State())
	}
	if got.SM().Epoch() != 5 {
		t.Errorf("expected epoch 5, got %d", got.SM().Epoch())
	}
}

func TestPlanStore_RestoreFromDisk_TerminalSkipped(t *testing.T) {
	dir := t.TempDir()
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)

	now := time.Now()
	plan := DesignPlan{
		ID:        "terminal-1",
		SessionID: "sess-1",
		Status:    PlanStatusCompleted,
		UpdatedAt: now,
		CreatedAt: now,
	}
	planDir := filepath.Join(dir, ".sylk", "sessions", "sess-1", "agents", "architect", "plans")
	_ = os.MkdirAll(planDir, 0o755)
	payload, _ := json.MarshalIndent(&plan, "", "  ")
	planPath := filepath.Join(planDir, "terminal-1.json")
	_ = os.WriteFile(planPath, payload, 0o644)

	store := NewPlanStore(dir, lm, slog.Default())
	defer store.Close()

	if store.Get("terminal-1") != nil {
		t.Error("expected terminal plan to be skipped on restore")
	}
	// File should be cleaned up.
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Error("expected terminal plan file to be deleted")
	}
}

func TestPlanStore_ConcurrentAccess(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := &DesignPlan{
				ID:        "conc-plan",
				SessionID: "sess-1",
				Status:    PlanStatusPending,
				UpdatedAt: time.Now(),
				Revision:  idx,
			}
			p.sm = NewPlanStateMachine(p.ID, PlanStatusPending)
			_ = store.Upsert(p)
			_ = store.Get("conc-plan")
			_ = store.Count()
			_ = store.Snapshot()
		}(i)
	}
	wg.Wait()

	// Should still have exactly one plan (same ID).
	if store.Count() != 1 {
		t.Errorf("expected 1 plan, got %d", store.Count())
	}
}

func TestPlanStore_RemoveDiskFile(t *testing.T) {
	store := testPlanStore(t)
	defer store.Close()

	plan := &DesignPlan{
		ID:        "disk-rm",
		SessionID: "sess-1",
		Status:    PlanStatusPending,
		UpdatedAt: time.Now(),
	}
	plan.sm = NewPlanStateMachine(plan.ID, PlanStatusPending)
	_ = store.Upsert(plan)

	diskPath := filepath.Join(store.PlanDir("sess-1"), "disk-rm.json")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		t.Fatal("expected file to exist before removal")
	}

	store.RemoveDiskFile("disk-rm", "sess-1")

	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		t.Error("expected file to be removed")
	}
}
