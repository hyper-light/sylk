package shared

import (
	"strings"
	"testing"

	"github.com/adalundhe/sylk/agents/guide"
)

func TestBuildGlobalExecutionContract_TesterRecall(t *testing.T) {
	contract := BuildGlobalExecutionContract("tester-global", guide.IntentRecall, "Recall the latest integration test results.")
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.Mode != GlobalExecutionModeRecall {
		t.Fatalf("mode = %q, want recall", contract.Mode)
	}
	if len(contract.Deliverables) != 1 || contract.Deliverables[0] != GlobalDeliverableContextSummary {
		t.Fatalf("unexpected deliverables: %v", contract.Deliverables)
	}
}

func TestBuildGlobalExecutionContract_TesterPlanRequest(t *testing.T) {
	contract := BuildGlobalExecutionContract("tester-global", guide.IntentCheck, "Plan integration test coverage for the auth and API changes.")
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.Mode != GlobalExecutionModePlan {
		t.Fatalf("mode = %q, want plan", contract.Mode)
	}
	for _, want := range []GlobalExecutionDeliverable{
		GlobalDeliverableBatchContext,
		GlobalDeliverableRiskAssessment,
		GlobalDeliverableTestStrategy,
	} {
		if !hasGlobalDeliverable(contract.Deliverables, want) {
			t.Fatalf("missing deliverable %q in %v", want, contract.Deliverables)
		}
	}
}

func TestBuildGlobalExecutionContract_InspectorAuditRequest(t *testing.T) {
	contract := BuildGlobalExecutionContract("inspector-global", guide.IntentCheck, "Audit this layer for plan adherence and escalate blocking issues.")
	if contract == nil {
		t.Fatal("expected contract")
	}
	if contract.Mode != GlobalExecutionModeAudit {
		t.Fatalf("mode = %q, want audit", contract.Mode)
	}
	for _, want := range []GlobalExecutionDeliverable{
		GlobalDeliverableCrossFileFindings,
		GlobalDeliverablePlanAdherence,
		GlobalDeliverableLayerJudgment,
		GlobalDeliverableEscalation,
	} {
		if !hasGlobalDeliverable(contract.Deliverables, want) {
			t.Fatalf("missing deliverable %q in %v", want, contract.Deliverables)
		}
	}
}

func TestAppendGlobalExecutionGuidance_IncludesDerivedDeliverables(t *testing.T) {
	contract := BuildGlobalExecutionContract("tester-global", guide.IntentCheck, "Write integration tests for the changed auth flow.")
	prompt := AppendGlobalExecutionGuidance("", contract, "tester-global")
	for _, want := range []string{
		"# Global Execution Contract",
		"Current request mode:",
		"authored global test artifacts",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func TestAppendGlobalExecutionGuidance_InspectorGlobalIsSurfaceFirst(t *testing.T) {
	contract := BuildGlobalExecutionContract("inspector-global", guide.IntentCheck, "Audit the merged auth flow changes against the plan.")
	prompt := AppendGlobalExecutionGuidance("", contract, "inspector-global")
	for _, want := range []string{
		"Start with the changed files, directly affected interfaces, and current tester or protocol evidence.",
		"expand to adjacent files, broader plan context, or specialist consults only when a concrete unresolved risk requires it",
		"Pending planned work is a compatibility contract",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}

func hasGlobalDeliverable(values []GlobalExecutionDeliverable, want GlobalExecutionDeliverable) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
