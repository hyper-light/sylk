package architect

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestArchitectAdvancePlan_AllowsSameStateRefresh(t *testing.T) {
	a := newTestArchitect(t, Config{
		AllowPlanningWithoutConsultation: true,
		WorkingDirectory:                 t.TempDir(),
	})

	plan := &DesignPlan{
		ID:            "plan-refresh",
		SessionID:     "sess-refresh",
		Query:         "refresh consulting state",
		Status:        PlanStatusConsulting,
		Epoch:         7,
		Consultations: map[string]*ConsultationEvidence{},
		CreatedAt:     time.Now().Add(-2 * time.Minute).UTC(),
		UpdatedAt:     time.Now().Add(-1 * time.Minute).UTC(),
	}
	plan.sm = NewPlanStateMachineWithEpoch(plan.ID, plan.Status, plan.Epoch)

	previousUpdate := plan.UpdatedAt
	if err := a.advancePlan(context.Background(), plan, PlanStatusConsulting, func() {
		plan.Revision++
		plan.RiskSummary = append(plan.RiskSummary, "refreshed")
	}); err != nil {
		t.Fatalf("advancePlan returned error on same-state refresh: %v", err)
	}

	if got := plan.SM().State(); got != PlanStatusConsulting {
		t.Fatalf("state = %s, want consulting", got)
	}
	if got := plan.Epoch; got != 7 {
		t.Fatalf("epoch = %d, want 7", got)
	}
	if plan.Revision != 1 {
		t.Fatalf("revision = %d, want 1", plan.Revision)
	}
	if len(plan.RiskSummary) != 1 || plan.RiskSummary[0] != "refreshed" {
		t.Fatalf("unexpected risk summary after refresh: %+v", plan.RiskSummary)
	}
	if !plan.UpdatedAt.After(previousUpdate) {
		t.Fatalf("updated_at = %v, want after %v", plan.UpdatedAt, previousUpdate)
	}

	stored, ok := a.GetActivePlan(plan.ID)
	if !ok || stored == nil {
		t.Fatal("expected refreshed plan to be persisted in plan store")
	}
	if stored.Epoch != 7 {
		t.Fatalf("stored epoch = %d, want 7", stored.Epoch)
	}
	if stored.Revision != 1 {
		t.Fatalf("stored revision = %d, want 1", stored.Revision)
	}
}

func TestDefaultArchitectControlStoreConfig_SerializesWrites(t *testing.T) {
	cfg := defaultArchitectControlStoreConfig("/tmp/architect.db")
	if cfg.MaxOpenConns != 1 {
		t.Fatalf("MaxOpenConns = %d, want 1", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 1 {
		t.Fatalf("MaxIdleConns = %d, want 1", cfg.MaxIdleConns)
	}
	if cfg.BusyTimeout <= 0 {
		t.Fatalf("BusyTimeout = %s, want > 0", cfg.BusyTimeout)
	}
	if cfg.WriteRetryMax <= 0 {
		t.Fatalf("WriteRetryMax = %d, want > 0", cfg.WriteRetryMax)
	}
	if cfg.WriteRetryBackoff <= 0 {
		t.Fatalf("WriteRetryBackoff = %s, want > 0", cfg.WriteRetryBackoff)
	}
}

func TestArchitectControlStore_ConcurrentUpsertPlan_NoBusy(t *testing.T) {
	store := testArchitectControlStore(t)

	var wg sync.WaitGroup
	errCh := make(chan error, 128)
	for worker := 0; worker < 16; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rev := 0; rev < 25; rev++ {
				plan := &DesignPlan{
					ID:        fmt.Sprintf("plan-%02d-%02d", worker, rev%4),
					SessionID: "sess-concurrent",
					Query:     "stress concurrent upserts",
					Status:    PlanStatusConsulting,
					Revision:  rev,
					CreatedAt: time.Now().Add(-time.Minute).UTC(),
					UpdatedAt: time.Now().UTC(),
				}
				plan.sm = NewPlanStateMachineWithEpoch(plan.ID, plan.Status, uint64(rev))
				plan.Epoch = uint64(rev)
				if err := store.UpsertPlan(plan); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent upsert failed: %v", err)
	}
}
