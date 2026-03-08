package guardian

import "testing"

func TestEvaluateTaskPreflight_FlagsSensitivePathsAndRisks(t *testing.T) {
	result := evaluateTaskPreflight(&reviewGateInput{
		Action:           "preflight_task",
		TaskID:           "task-1",
		AgentType:        "engineer",
		Complexity:       "high",
		AffectedFiles:    []string{".github/workflows/deploy.yml", "db/migrations/001.sql"},
		RiskFactors:      []string{"destructive migration", "auth changes"},
		TestRequirements: []string{"integration tests"},
	})

	clean, _ := result["clean"].(bool)
	if clean {
		t.Fatal("expected preflight to flag issues")
	}
	requiresApproval, _ := result["requires_approval"].(bool)
	if !requiresApproval {
		t.Fatal("expected approval requirement")
	}
	blocking, _ := result["blocking_findings"].([]string)
	if len(blocking) == 0 {
		t.Fatal("expected blocking findings")
	}
}
