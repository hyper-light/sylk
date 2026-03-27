package academic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/providers"
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

func TestAcademicResearchExecutionState_FinalizationBlockRequiresGroundingAfterSearch(t *testing.T) {
	state := newAcademicResearchExecutionState("sess-exec")
	state.observeNativeSearchCall(context.Background(), providers.NativeWebSearchCall{
		ID:    "search-1",
		Query: "python cli library recommendation",
	})

	reminder, _ := state.finalizationBlock()
	if !strings.Contains(reminder, "`ground_source`") {
		t.Fatalf("finalization reminder = %q, want ground_source guidance", reminder)
	}
}

func TestAcademicResearchExecutionState_FinalizationBlockFlagsRepeatedSearchWithoutGrounding(t *testing.T) {
	state := newAcademicResearchExecutionState("sess-exec")
	state.observeNativeSearchCall(context.Background(), providers.NativeWebSearchCall{
		ID:    "search-1",
		Query: "python cli library recommendation",
	})
	state.observeNativeSearchCall(context.Background(), providers.NativeWebSearchCall{
		ID:    "search-2",
		Query: "python cli library recommendation",
	})

	reminder, fields := state.finalizationBlock()
	if !strings.Contains(reminder, "Stop repeating the same search path") {
		t.Fatalf("finalization reminder = %q, want repeated search warning", reminder)
	}
	if got := fields["repeated_search_count"]; got == nil {
		t.Fatalf("expected repeated_search_count field, got %#v", fields)
	}
}

func TestAcademicResearchExecutionState_FinalizationBlockRequiresPaperWhenRequested(t *testing.T) {
	state := newAcademicResearchExecutionState("sess-exec")
	state.setResearchPaperRequired(true)

	reminder, _ := state.finalizationBlock()
	if !strings.Contains(reminder, "`author_research_paper`") {
		t.Fatalf("finalization reminder = %q, want author_research_paper guidance", reminder)
	}
}
