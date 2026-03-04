package architect

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlanStore_RestorePlanFromFile_ReadyPlanRestored(t *testing.T) {
	dir := t.TempDir()
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)

	now := time.Now()
	plan := DesignPlan{
		ID:          "plan-ready-1",
		SessionID:   "sess-1",
		Status:      PlanStatusReady,
		Epoch:       5,
		UpdatedAt:   now.Add(-2 * time.Minute),
		CreatedAt:   now.Add(-5 * time.Minute),
		LeaseExpiry: now.Add(28 * time.Minute),
	}

	planDir := filepath.Join(dir, ".sylk", "sessions", plan.SessionID, "agents", "architect", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.MarshalIndent(&plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(planDir, plan.ID+".json")
	if err := os.WriteFile(planPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewPlanStore(dir, lm, slog.Default())
	defer store.Close()

	restored := store.Get(plan.ID)
	if restored == nil {
		t.Fatal("plan not found in store after restore")
	}
	if restored.SM().State() != PlanStatusReady {
		t.Errorf("expected Ready state, got %s", restored.SM().State())
	}
	if restored.SM().Epoch() != plan.Epoch {
		t.Errorf("expected epoch %d, got %d", plan.Epoch, restored.SM().Epoch())
	}
	// File should still exist on disk (not proactively deleted).
	if _, err := os.Stat(planPath); os.IsNotExist(err) {
		t.Error("plan file was deleted but should be preserved for Ready plans")
	}
}

func TestPlanStore_RestorePlanFromFile_ExecutingPlanRestored(t *testing.T) {
	dir := t.TempDir()
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)

	now := time.Now()
	plan := DesignPlan{
		ID:          "plan-exec-1",
		SessionID:   "sess-1",
		Status:      PlanStatusExecuting,
		Epoch:       8,
		UpdatedAt:   now.Add(-1 * time.Minute),
		CreatedAt:   now.Add(-10 * time.Minute),
		LeaseExpiry: now.Add(20 * time.Minute),
		LeaseHolder: "orchestrator",
	}

	planDir := filepath.Join(dir, ".sylk", "sessions", plan.SessionID, "agents", "architect", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.MarshalIndent(&plan, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(planDir, plan.ID+".json")
	if err := os.WriteFile(planPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewPlanStore(dir, lm, slog.Default())
	defer store.Close()

	restored := store.Get(plan.ID)
	if restored == nil {
		t.Fatal("plan not found in store after restore")
	}
	if restored.SM().State() != PlanStatusExecuting {
		t.Errorf("expected Executing state, got %s", restored.SM().State())
	}
	if restored.LeaseHolder != "orchestrator" {
		t.Errorf("expected lease holder 'orchestrator', got %q", restored.LeaseHolder)
	}
}

func TestPlanStore_RestorePlanFromFile_TerminalPlansSkipped(t *testing.T) {
	dir := t.TempDir()
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)

	now := time.Now()
	terminal := []PlanStatus{
		PlanStatusFailed,
		PlanStatusCompleted,
		PlanStatusSuperseded,
	}

	for _, status := range terminal {
		plan := DesignPlan{
			ID:        "plan-" + status.String(),
			SessionID: "sess-1",
			Status:    status,
			UpdatedAt: now,
			CreatedAt: now,
		}
		planDir := filepath.Join(dir, ".sylk", "sessions", plan.SessionID, "agents", "architect", "plans")
		_ = os.MkdirAll(planDir, 0o755)
		payload, _ := json.MarshalIndent(&plan, "", "  ")
		planPath := filepath.Join(planDir, plan.ID+".json")
		_ = os.WriteFile(planPath, payload, 0o644)
	}

	store := NewPlanStore(dir, lm, slog.Default())
	defer store.Close()

	if store.Count() != 0 {
		t.Errorf("expected 0 active plans after terminal restores, got %d", store.Count())
	}
}

func TestPlanStore_RestorePlanFromFile_StalePlanSkipped(t *testing.T) {
	dir := t.TempDir()
	lm := NewPlanLeaseManager(10*time.Second, 5*time.Minute)

	old := time.Now().Add(-48 * time.Hour)
	plan := DesignPlan{
		ID:        "plan-stale",
		SessionID: "sess-1",
		Status:    PlanStatusReady,
		UpdatedAt: old,
		CreatedAt: old,
	}
	planDir := filepath.Join(dir, ".sylk", "sessions", plan.SessionID, "agents", "architect", "plans")
	_ = os.MkdirAll(planDir, 0o755)
	payload, _ := json.MarshalIndent(&plan, "", "  ")
	planPath := filepath.Join(planDir, plan.ID+".json")
	_ = os.WriteFile(planPath, payload, 0o644)

	store := NewPlanStore(dir, lm, slog.Default())
	defer store.Close()

	if store.Get("plan-stale") != nil {
		t.Error("expected stale plan to be skipped")
	}
}
