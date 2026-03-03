package architect

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestorePlanFromFile_ReadyPlanRestored(t *testing.T) {
	dir := t.TempDir()
	a := &Architect{
		config:      Config{WorkingDirectory: dir},
		activePlans: map[string]*DesignPlan{},
		logger:      slog.Default(),
	}

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

	planDir := a.planStoreDir(plan.SessionID)
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

	cutoff := now.Add(-24 * time.Hour)
	ok, err := a.restorePlanFromFile(planPath, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected Ready plan to be restored, but was skipped")
	}

	a.activePlansMu.RLock()
	restored := a.activePlans[plan.ID]
	a.activePlansMu.RUnlock()
	if restored == nil {
		t.Fatal("plan not found in activePlans after restore")
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

func TestRestorePlanFromFile_ExecutingPlanRestored(t *testing.T) {
	dir := t.TempDir()
	a := &Architect{
		config:      Config{WorkingDirectory: dir},
		activePlans: map[string]*DesignPlan{},
		logger:      slog.Default(),
	}

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

	planDir := a.planStoreDir(plan.SessionID)
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

	cutoff := now.Add(-24 * time.Hour)
	ok, err := a.restorePlanFromFile(planPath, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected Executing plan to be restored, but was skipped")
	}

	a.activePlansMu.RLock()
	restored := a.activePlans[plan.ID]
	a.activePlansMu.RUnlock()
	if restored == nil {
		t.Fatal("plan not found in activePlans after restore")
	}
	if restored.SM().State() != PlanStatusExecuting {
		t.Errorf("expected Executing state, got %s", restored.SM().State())
	}
	if restored.LeaseHolder != "orchestrator" {
		t.Errorf("expected lease holder 'orchestrator', got %q", restored.LeaseHolder)
	}
}

func TestRestorePlanFromFile_TerminalPlansSkipped(t *testing.T) {
	dir := t.TempDir()
	a := &Architect{
		config:      Config{WorkingDirectory: dir},
		activePlans: map[string]*DesignPlan{},
		logger:      slog.Default(),
	}

	now := time.Now()
	cutoff := now.Add(-24 * time.Hour)

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
		planDir := a.planStoreDir(plan.SessionID)
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

		ok, err := a.restorePlanFromFile(planPath, cutoff)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", status, err)
		}
		if ok {
			t.Errorf("%s: expected terminal plan to be skipped", status)
		}
		if _, err := os.Stat(planPath); !os.IsNotExist(err) {
			t.Errorf("%s: expected terminal plan file to be deleted", status)
		}
	}

	a.activePlansMu.RLock()
	count := len(a.activePlans)
	a.activePlansMu.RUnlock()
	if count != 0 {
		t.Errorf("expected 0 active plans after terminal restores, got %d", count)
	}
}

func TestRestorePlanFromFile_StalePlanSkipped(t *testing.T) {
	dir := t.TempDir()
	a := &Architect{
		config:      Config{WorkingDirectory: dir},
		activePlans: map[string]*DesignPlan{},
		logger:      slog.Default(),
	}

	old := time.Now().Add(-48 * time.Hour)
	plan := DesignPlan{
		ID:        "plan-stale",
		SessionID: "sess-1",
		Status:    PlanStatusReady,
		UpdatedAt: old,
		CreatedAt: old,
	}
	planDir := a.planStoreDir(plan.SessionID)
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

	cutoff := time.Now().Add(-24 * time.Hour)
	ok, err := a.restorePlanFromFile(planPath, cutoff)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected stale plan to be skipped")
	}
}
