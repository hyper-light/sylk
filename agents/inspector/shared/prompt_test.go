package shared

import (
	"strings"
	"testing"

	agentshared "github.com/adalundhe/sylk/agents/shared"
)

func TestPipelineInspectorSystemPromptForContract_OmitsValidationProtocolPreImplementation(t *testing.T) {
	prompt := PipelineInspectorSystemPromptForContract(&agentshared.TaskExecutionContract{
		PreImplementation: true,
	})

	for _, blocked := range []string{
		"Default to TDD: challenge Tester before dispatching Engineer or Designer unless the task is strictly inspection-only.",
		"Use `handoff_to_ot` only when you are satisfied that the testing and implementation evidence meet the criteria.",
	} {
		if strings.Contains(prompt, blocked) {
			t.Fatalf("pre-implementation prompt unexpectedly contains %q", blocked)
		}
	}
}

func TestPipelineInspectorSystemPromptForContract_IncludesValidationProtocolWithImplementationEvidence(t *testing.T) {
	prompt := PipelineInspectorSystemPromptForContract(&agentshared.TaskExecutionContract{
		PreImplementation: false,
	})

	for _, want := range []string{
		"Default to TDD: challenge Tester before dispatching Engineer or Designer unless the task is strictly inspection-only.",
		"Use `validate_criteria` and `grade_task_quality` to judge the current state, but keep the lifecycle agentic: decide whether to loop again or accept.",
		"Use `handoff_to_ot` only when you are satisfied that the testing and implementation evidence meet the criteria.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implementation-validation prompt missing %q", want)
		}
	}
}
