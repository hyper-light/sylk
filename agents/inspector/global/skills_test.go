package global

import (
	"context"
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
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

func TestRequestUserClarificationSkill_DelegatesUserFacingQuestion(t *testing.T) {
	gi := &GlobalInspector{}
	skill := requestUserClarificationSkill(gi)
	if skill == nil {
		t.Fatal("expected skill")
	}

	question := "Which behavior should remain canonical for checkpoint reviews?"
	_, err := skill.Handler(context.Background(), []byte(`{"question":"`+question+`"}`))
	if !errors.Is(err, skills.ErrDelegatedRequested) {
		t.Fatalf("expected delegated sentinel, got %v", err)
	}
	if got := skills.DelegatedMessage(err); got != question {
		t.Fatalf("delegated message = %q, want %q", got, question)
	}
	payload, ok := skills.DelegatedPayload(err)
	if !ok {
		t.Fatal("expected delegated payload")
	}
	values, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("delegated payload type = %T, want map[string]any", payload)
	}
	if got := values["status"]; got != "clarification_requested" {
		t.Fatalf("status = %#v, want clarification_requested", got)
	}
	if got := values["question"]; got != question {
		t.Fatalf("question = %#v, want %q", got, question)
	}
	if got := values["user_message"]; got != question {
		t.Fatalf("user_message = %#v, want %q", got, question)
	}
}
