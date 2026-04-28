package orchestrator

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/architect"
	"github.com/adalundhe/sylk/agents/guardian"
	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func handoffWithTasks(planID string, taskIDs ...string) *architect.PlanHandoff {
	tasks := make([]*architect.HandoffTask, 0, len(taskIDs))
	for _, id := range taskIDs {
		tasks = append(tasks, &architect.HandoffTask{
			ID:        id,
			AgentType: "engineer",
		})
	}
	return &architect.PlanHandoff{
		PlanID:   planID,
		Revision: 1,
		Tasks:    tasks,
	}
}

func attachValidAttestation(handoff *architect.PlanHandoff) *agentshared.PlanPreflightAttestation {
	hash := computeHandoffPreflightHash(handoff)
	att := &agentshared.PlanPreflightAttestation{
		PlanID:            handoff.PlanID,
		PlanRevision:      handoff.Revision,
		PlanContentHash:   hash,
		ClassifierVersion: guardian.GuardianPreflightClassifierVersion,
		IssuedAt:          time.Now().UTC(),
		IssuerAgentID:     "guardian",
		PerTask:           make(map[string]*agentshared.PlanPreflightTaskVerdict, len(handoff.Tasks)),
	}
	for _, t := range handoff.Tasks {
		att.PerTask[t.ID] = &agentshared.PlanPreflightTaskVerdict{
			TaskID:    t.ID,
			AgentType: t.AgentType,
			Clean:     true,
		}
	}
	handoff.GuardianAttestation = att
	return att
}

func TestVerifyAndAdoptAttestation_AbsentReturnsNilNil(t *testing.T) {
	h := handoffWithTasks("plan-1", "task-a")
	pf, err := verifyAndAdoptAttestation(h)
	if err != nil {
		t.Fatalf("absent attestation should not error; got %v", err)
	}
	if pf != nil {
		t.Fatalf("absent attestation should produce nil preflight; got %+v", pf)
	}
}

func TestVerifyAndAdoptAttestation_ValidPath(t *testing.T) {
	h := handoffWithTasks("plan-1", "task-a", "task-b")
	attachValidAttestation(h)
	pf, err := verifyAndAdoptAttestation(h)
	if err != nil {
		t.Fatalf("valid attestation should pass; got %v", err)
	}
	if pf == nil || len(pf.Tasks) != 2 {
		t.Fatalf("preflight should cover all tasks; got %+v", pf)
	}
	if pf.Tasks["task-a"] == nil || pf.Tasks["task-b"] == nil {
		t.Fatalf("preflight missing per-task entries: %+v", pf.Tasks)
	}
}

func TestVerifyAndAdoptAttestation_HashMismatchFails(t *testing.T) {
	h := handoffWithTasks("plan-1", "task-a")
	attachValidAttestation(h)
	h.GuardianAttestation.PlanContentHash = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := verifyAndAdoptAttestation(h); err == nil {
		t.Fatal("hash mismatch must fail verification")
	}
}

func TestVerifyAndAdoptAttestation_ClassifierVersionMismatchFails(t *testing.T) {
	h := handoffWithTasks("plan-1", "task-a")
	attachValidAttestation(h)
	h.GuardianAttestation.ClassifierVersion = "guardian-preflight-v0-stale"
	if _, err := verifyAndAdoptAttestation(h); err == nil {
		t.Fatal("stale classifier version must fail verification")
	}
}

func TestVerifyAndAdoptAttestation_MissingTaskVerdictFails(t *testing.T) {
	h := handoffWithTasks("plan-1", "task-a", "task-b")
	attachValidAttestation(h)
	delete(h.GuardianAttestation.PerTask, "task-b")
	if _, err := verifyAndAdoptAttestation(h); err == nil {
		t.Fatal("missing per-task verdict must fail verification")
	}
}

func TestComputeHandoffPreflightHash_StableAcrossEquivalentInputs(t *testing.T) {
	h1 := handoffWithTasks("plan-1", "task-a", "task-b")
	h2 := handoffWithTasks("plan-1", "task-b", "task-a") // different order
	if computeHandoffPreflightHash(h1) != computeHandoffPreflightHash(h2) {
		t.Fatal("hash must be order-independent (sort by task_id)")
	}
}

func TestComputeHandoffPreflightHash_SensitiveToTaskAddition(t *testing.T) {
	h1 := handoffWithTasks("plan-1", "task-a")
	h2 := handoffWithTasks("plan-1", "task-a", "task-b")
	if computeHandoffPreflightHash(h1) == computeHandoffPreflightHash(h2) {
		t.Fatal("hash must distinguish plans with different task counts")
	}
}

func TestComputeHandoffPreflightHash_SensitiveToRevision(t *testing.T) {
	h1 := handoffWithTasks("plan-1", "task-a")
	h2 := handoffWithTasks("plan-1", "task-a")
	h2.Revision = 2
	if computeHandoffPreflightHash(h1) == computeHandoffPreflightHash(h2) {
		t.Fatal("hash must distinguish plan revisions")
	}
}
