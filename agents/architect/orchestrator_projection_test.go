package architect

import (
	"testing"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func TestOrchestratorProjection_EmptyLookupReturnsNil(t *testing.T) {
	p := newOrchestratorProjection()
	if got := p.lookup("plan-1"); got != nil {
		t.Fatalf("empty projection should return nil; got %+v", got)
	}
	if got := p.asOrchestratorPlanState("plan-1"); got != nil {
		t.Fatalf("empty projection should return nil orchestratorPlanState; got %+v", got)
	}
}

func TestOrchestratorProjection_ReceiptUpdateMapsToProjectedStatus(t *testing.T) {
	cases := []struct {
		name   string
		status agentshared.PlanHandoffReceiptStatus
		want   string
	}{
		{"accepted maps to queued", agentshared.PlanHandoffReceiptStatusAccepted, "queued"},
		{"dag_built maps to queued", agentshared.PlanHandoffReceiptStatusDAGBuilt, "queued"},
		{"submitted maps to queued", agentshared.PlanHandoffReceiptStatusSubmitted, "queued"},
		{"running maps to running", agentshared.PlanHandoffReceiptStatusRunning, "running"},
		{"failed maps to failed", agentshared.PlanHandoffReceiptStatusFailed, "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newOrchestratorProjection()
			p.applyReceiptUpdate(&agentshared.PlanHandoffReceiptUpdate{
				PlanID: "plan-1",
				Found:  true,
				Receipt: &agentshared.PlanHandoffReceipt{
					PlanID:    "plan-1",
					Status:    tc.status,
					DAGID:     "dag-1",
					SessionID: "sess-1",
				},
			})
			snap := p.lookup("plan-1")
			if snap == nil {
				t.Fatal("expected projection entry")
			}
			if snap.Status != tc.want {
				t.Fatalf("status: want %q, got %q", tc.want, snap.Status)
			}
			if snap.DAGID != "dag-1" {
				t.Fatalf("dag_id: want dag-1, got %q", snap.DAGID)
			}
		})
	}
}

func TestOrchestratorProjection_DAGStatusResolvesViaMapping(t *testing.T) {
	p := newOrchestratorProjection()
	// First a receipt that establishes the dag_id → plan_id mapping.
	p.applyReceiptUpdate(&agentshared.PlanHandoffReceiptUpdate{
		PlanID: "plan-1",
		Found:  true,
		Receipt: &agentshared.PlanHandoffReceipt{
			PlanID: "plan-1",
			Status: agentshared.PlanHandoffReceiptStatusRunning,
			DAGID:  "dag-1",
		},
	})
	// Now a terminal dag.status event.
	p.applyDAGStatus("dag-1", "succeeded", "")
	snap := p.lookup("plan-1")
	if snap == nil {
		t.Fatal("expected projection entry after dag.status")
	}
	if snap.Status != "completed" {
		t.Fatalf("status after succeeded: want completed, got %q", snap.Status)
	}
	if snap.CompletedAt == nil {
		t.Fatal("expected CompletedAt set after terminal event")
	}
}

func TestOrchestratorProjection_DAGStatusForUnknownDAGIDIsDropped(t *testing.T) {
	p := newOrchestratorProjection()
	// No receipt update → no mapping.
	p.applyDAGStatus("dag-orphan", "succeeded", "")
	if got := p.lookup("plan-1"); got != nil {
		t.Fatalf("unknown dag_id should not create projection entry; got %+v", got)
	}
}

func TestOrchestratorProjection_StallClassification(t *testing.T) {
	p := newOrchestratorProjection()
	startedLongAgo := time.Now().Add(-2 * projectedStalledThreshold)
	p.applyReceiptUpdate(&agentshared.PlanHandoffReceiptUpdate{
		PlanID: "plan-1",
		Found:  true,
		Receipt: &agentshared.PlanHandoffReceipt{
			PlanID:    "plan-1",
			Status:    agentshared.PlanHandoffReceiptStatusRunning,
			DAGID:     "dag-1",
			RunningAt: startedLongAgo,
			UpdatedAt: startedLongAgo, // no progress since
		},
	})
	state := p.asOrchestratorPlanState("plan-1")
	if state == nil {
		t.Fatal("expected projection entry")
	}
	if state.Status != "stalled" {
		t.Fatalf("status: want stalled (StartedAt > threshold ago, no progress); got %q", state.Status)
	}
	if state.StalledForSeconds <= 0 {
		t.Fatalf("StalledForSeconds: want > 0; got %d", state.StalledForSeconds)
	}
}

func TestOrchestratorProjection_RunningNotStalledWithRecentProgress(t *testing.T) {
	p := newOrchestratorProjection()
	startedLongAgo := time.Now().Add(-2 * projectedStalledThreshold)
	recentProgress := time.Now().Add(-1 * time.Second)
	p.applyReceiptUpdate(&agentshared.PlanHandoffReceiptUpdate{
		PlanID: "plan-1",
		Found:  true,
		Receipt: &agentshared.PlanHandoffReceipt{
			PlanID:    "plan-1",
			Status:    agentshared.PlanHandoffReceiptStatusRunning,
			DAGID:     "dag-1",
			RunningAt: startedLongAgo,
			UpdatedAt: recentProgress,
		},
	})
	state := p.asOrchestratorPlanState("plan-1")
	if state == nil {
		t.Fatal("expected projection entry")
	}
	if state.Status != "running" {
		t.Fatalf("status: want running (recent progress); got %q", state.Status)
	}
}

func TestOrchestratorProjection_NilReceivers(t *testing.T) {
	var p *orchestratorProjection
	// Must not panic on nil receiver — the architect can call lookup
	// before the projection is constructed (e.g. in tests).
	if got := p.lookup("plan-1"); got != nil {
		t.Fatalf("nil projection lookup should return nil; got %+v", got)
	}
	p.applyReceiptUpdate(&agentshared.PlanHandoffReceiptUpdate{PlanID: "x"})
	p.applyDAGStatus("dag-1", "succeeded", "")
	if got := p.asOrchestratorPlanState("plan-1"); got != nil {
		t.Fatalf("nil projection conversion should return nil; got %+v", got)
	}
}
