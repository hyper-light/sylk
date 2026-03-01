package architect

import (
	"context"
	"strings"
	"testing"
)

func TestClarificationDecision_NoQuestionsInMetadata(t *testing.T) {
	a := &Architect{}
	decision := a.clarificationDecisionForRequest(
		context.Background(),
		&ArchitectRequest{Query: "implement user auth"},
		&Requirements{
			Query: "implement user auth",
			Goals: []string{"secure login", "session management"},
		},
	)
	if decision.Needed {
		t.Fatal("expected no clarification needed when metadata has no questions")
	}
}

func TestClarificationDecision_PassesThroughLLMQuestions(t *testing.T) {
	a := &Architect{}
	decision := a.clarificationDecisionForRequest(
		context.Background(),
		&ArchitectRequest{Query: "implement user auth"},
		&Requirements{
			Query: "implement user auth",
			Metadata: map[string]any{
				"clarification_questions": []string{
					"What auth provider should we use?",
					"Do you need refresh tokens?",
				},
			},
		},
	)
	if !decision.Needed {
		t.Fatal("expected clarification needed when metadata has questions")
	}
	if len(decision.Questions) != 2 {
		t.Fatalf("expected 2 questions, got %d", len(decision.Questions))
	}
	if decision.Questions[0] != "What auth provider should we use?" {
		t.Fatalf("unexpected question: %q", decision.Questions[0])
	}
}

func TestClarificationDecision_SkipClarification(t *testing.T) {
	a := &Architect{}
	decision := a.clarificationDecisionForRequest(
		context.Background(),
		&ArchitectRequest{
			Query:  "implement user auth",
			Params: map[string]any{"skip_clarification": true},
		},
		&Requirements{
			Query: "implement user auth",
			Metadata: map[string]any{
				"clarification_questions": []string{"ignored question"},
			},
		},
	)
	if decision.Needed {
		t.Fatal("expected skip_clarification to suppress clarification")
	}
}

func TestClarificationUserResponseDoesNotRenderNilNarrative(t *testing.T) {
	a := &Architect{}
	response := a.clarificationUserResponse(
		context.Background(),
		&ArchitectRequest{Query: "Can we plan this?"},
		&Requirements{Query: "Can we plan this?", Scope: "project"},
		clarificationDecision{
			Needed:    true,
			Questions: []string{"Which provider should we support first?"},
		},
	)
	if strings.HasPrefix(response, "<nil>") {
		t.Fatalf("response should not start with <nil>: %q", response)
	}
	if !strings.Contains(response, "Which provider should we support first?") {
		t.Fatalf("expected question in clarification response, got %q", response)
	}
}

func TestFallbackClarificationResponse_WithQuestions(t *testing.T) {
	response := fallbackClarificationResponse([]string{"What scope?", "What priority?"})
	if !strings.Contains(response, "What scope?") {
		t.Fatalf("expected questions in fallback, got %q", response)
	}
}

func TestFallbackClarificationResponse_NoQuestions(t *testing.T) {
	response := fallbackClarificationResponse(nil)
	if response == "" {
		t.Fatal("expected non-empty fallback response")
	}
}

func TestRequiresClarification_NilRequirements(t *testing.T) {
	if requiresClarification(nil) {
		t.Fatal("expected false for nil requirements")
	}
}

func TestRequiresClarification_NoGoals(t *testing.T) {
	requirements := &Requirements{Query: "vague request"}
	if !requiresClarification(requirements) {
		t.Fatal("expected true when no goals are defined")
	}
}

func TestRequiresClarification_GoalsOutweighQuestions(t *testing.T) {
	requirements := &Requirements{
		Query: "hello world Python script",
		Goals: []string{"Create a Python hello world script", "Print output to stdout"},
		Metadata: map[string]any{
			"clarification_questions": []string{"Which Python version?"},
		},
	}
	if requiresClarification(requirements) {
		t.Fatal("expected false when goals (2) outweigh questions+unknowns (1)")
	}
}

func TestRequiresClarification_QuestionsOutweighGoals(t *testing.T) {
	requirements := &Requirements{
		Query: "build an app",
		Goals: []string{"Build an application"},
		Metadata: map[string]any{
			"clarification_questions": []string{"What kind of app?", "What language?"},
			"unknowns":               []string{"target platform"},
		},
	}
	if !requiresClarification(requirements) {
		t.Fatal("expected true when questions+unknowns (3) outweigh goals (1)")
	}
}

func TestRequiresClarification_EqualGoalsAndQuestions(t *testing.T) {
	requirements := &Requirements{
		Query: "implement auth",
		Goals: []string{"secure login", "session management"},
		Metadata: map[string]any{
			"clarification_questions": []string{"OAuth or JWT?", "Need refresh tokens?"},
		},
	}
	if requiresClarification(requirements) {
		t.Fatal("expected false when questions (2) equal goals (2) — not strictly greater")
	}
}

func TestClarificationDecision_AmbiguityGateSkipsTrivial(t *testing.T) {
	a := &Architect{}
	decision := a.clarificationDecisionForRequest(
		context.Background(),
		&ArchitectRequest{Query: "hello world Python script"},
		&Requirements{
			Query: "hello world Python script",
			Goals: []string{"Create a hello world script", "Use Python"},
			Metadata: map[string]any{
				"clarification_questions": []string{"Which Python version?"},
			},
		},
	)
	if decision.Needed {
		t.Fatal("expected ambiguity gate to skip clarification for trivially simple request")
	}
}

func TestUnknownsFromMetadata_NilRequirements(t *testing.T) {
	unknowns := unknownsFromMetadata(nil)
	if len(unknowns) != 0 {
		t.Fatalf("expected 0 unknowns for nil, got %d", len(unknowns))
	}
}

func TestUnknownsFromMetadata_EmptyMetadata(t *testing.T) {
	unknowns := unknownsFromMetadata(&Requirements{Query: "test"})
	if len(unknowns) != 0 {
		t.Fatalf("expected 0 unknowns for empty metadata, got %d", len(unknowns))
	}
}

func TestUnknownsFromMetadata_WithUnknowns(t *testing.T) {
	requirements := &Requirements{
		Query: "test",
		Metadata: map[string]any{
			"unknowns": []string{"target platform", "deployment strategy"},
		},
	}
	unknowns := unknownsFromMetadata(requirements)
	if len(unknowns) != 2 {
		t.Fatalf("expected 2 unknowns, got %d", len(unknowns))
	}
}
