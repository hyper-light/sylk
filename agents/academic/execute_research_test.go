package academic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestConsultSkill_ExecuteResearchRejectsDuplicateQuestion(t *testing.T) {
	a, err := New(Config{ID: "academic"}, nil)
	if err != nil {
		t.Fatalf("new academic: %v", err)
	}

	state := newAcademicResearchExecutionState("sess-exec")
	if err := state.recordConsultAttempt("librarian", "How does Sylk already structure auth?", ""); err != nil {
		t.Fatalf("recordConsultAttempt: %v", err)
	}
	ctx := WithAcademicResearchExecutionState(context.Background(), state)
	skill := a.skills.Get("consult")
	if skill == nil || skill.Handler == nil {
		t.Fatal("consult skill not registered")
	}

	_, err = skill.Handler(ctx, json.RawMessage(`{"target":"librarian","query":"How does Sylk already structure auth?","scope":""}`))
	if err == nil {
		t.Fatal("expected duplicate consultation to be rejected")
	}
	if !strings.Contains(err.Error(), "forbids repeating") {
		t.Fatalf("duplicate consult error = %v, want research-run duplicate guard", err)
	}
}
