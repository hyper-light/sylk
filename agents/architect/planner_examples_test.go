package architect

import "testing"

func TestToTaskExamples_NormalizesFencedCodeAndLanguage(t *testing.T) {
	examples := toTaskExamples([]taskExamplePayload{
		{
			Label:    "Execution flow",
			Code:     "```mermaid\nflowchart TD\n  Architect --> Orchestrator\n```",
			Language: "",
		},
	})

	if len(examples) != 1 {
		t.Fatalf("expected 1 example, got %d", len(examples))
	}
	if examples[0].Language != "mermaid" {
		t.Fatalf("language = %q, want mermaid", examples[0].Language)
	}
	if examples[0].Code == "" || examples[0].Code == "```mermaid\nflowchart TD\n  Architect --> Orchestrator\n```" {
		t.Fatalf("expected stripped code body, got %q", examples[0].Code)
	}
}
