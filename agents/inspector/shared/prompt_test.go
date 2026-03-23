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
		"Each time Engineer or Designer hands work back to you, invoke `finalize_pipeline` to run the inspector audit cycle and challenge Tester.",
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
		"Your first `challenge_agent` call to Tester, Engineer, or Designer is allowed.",
		"you may challenge that same target again only if that target has modified pipeline VFS state since your previous challenge to that target.",
		"Push Engineer and Designer like a seasoned staff engineer reviewing senior-level code: audit correctness, robustness, performance, scope discipline, and production quality; penalize excess code, premature abstraction, verbosity, and agentic slop.",
		"Push Tester to prove the test surface adds real value; penalize noisy, arbitrary, or low-quality tests that expand coverage surface without materially improving confidence.",
		"Use `validate_criteria` and `grade_task_quality` to judge the current state, but keep the lifecycle agentic: decide whether to loop again or accept.",
		"Each time Engineer or Designer hands work back to you, invoke `finalize_pipeline` to run the inspector audit cycle and challenge Tester.",
		"If the `finalize_pipeline` audit passes and tester evidence confirms the required tests are implemented and passing, you must immediately invoke `handoff_to_ot` and stop looping.",
		"Use `handoff_to_ot` only when you are satisfied that the latest `finalize_pipeline` audit cycle passed and the pipeline should terminate successfully, and do not start another audit cycle once `finalize_pipeline` reports readiness for OT.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implementation-validation prompt missing %q", want)
		}
	}
}
