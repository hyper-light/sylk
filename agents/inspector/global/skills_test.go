package global

import (
	"testing"
)

func TestEvaluatePlanAdherence_UsesPlanSnapshotTasks(t *testing.T) {
	snapshot := `{"PlanID":"plan-1","Tasks":[{"ID":"task_1"},{"ID":"task_2"}]}`
	score, err := evaluatePlanAdherence(snapshot, []string{"task_1", "task_3"})
	if err != nil {
		t.Fatalf("evaluatePlanAdherence returned unexpected error: %v", err)
	}
	if score.Score >= 1.0 {
		t.Fatalf("expected degraded adherence score, got %v", score.Score)
	}
	if len(score.TasksCovered) != 1 || score.TasksCovered[0] != "task_1" {
		t.Fatalf("unexpected tasks covered: %v", score.TasksCovered)
	}
	if len(score.TasksMissing) != 1 || score.TasksMissing[0] != "task_2" {
		t.Fatalf("unexpected tasks missing: %v", score.TasksMissing)
	}
	if len(score.Deviations) == 0 {
		t.Fatal("expected deviations for missing and unexpected tasks")
	}
}
