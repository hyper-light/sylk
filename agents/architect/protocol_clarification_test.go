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
