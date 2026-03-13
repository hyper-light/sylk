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
		"Run `run_type_checker` on all task files",
		"Use `validate_criteria` to check implementation against defined criteria",
		"Use `grade_task_quality` to produce a multi-dimensional quality score",
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
		"Run `run_type_checker` on all task files",
		"Use `validate_criteria` to check implementation against defined criteria",
		"Use `grade_task_quality` to produce a multi-dimensional quality score",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implementation-validation prompt missing %q", want)
		}
	}
}
