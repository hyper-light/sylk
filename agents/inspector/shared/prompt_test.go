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
	for _, want := range []string{
		"# Workspace Layers",
		"Pipeline VFS: task-scoped unmerged in-progress work for the active pipeline.",
		"`run_command` and `run_shell_script` execute against that same layered workspace view",
		"Never describe a command failure as a sandbox, bwrap, chdir, project-directory, VFS, or `working_dir` limitation unless the tool output explicitly reports that condition",
		"Treat virtualenv bootstrap or `.venv` execution failures as install-strategy/tooling problems, not sandbox or workspace-visibility proof",
		"Never translate a missing interpreter, executable, module, or dependency error into a claim that the runner cannot see workspace files unless the tool output explicitly reports a missing workspace path",
		"Always quote or summarize the exact execution-tool error or stderr before explaining why a command failed",
		"Always treat `command not found`, `execvp`, and similar missing-executable errors as tooling-availability failures, not file-visibility failures, unless the tool output separately reports a missing workspace path",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("pre-implementation prompt missing %q", want)
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
		"# Workspace Layers",
		"Pipeline VFS: task-scoped unmerged in-progress work for the active pipeline.",
		"`run_command` and `run_shell_script` execute against that same layered workspace view",
		"Never describe a command failure as a sandbox, bwrap, chdir, project-directory, VFS, or `working_dir` limitation unless the tool output explicitly reports that condition",
		"Treat virtualenv bootstrap or `.venv` execution failures as install-strategy/tooling problems, not sandbox or workspace-visibility proof",
		"Never translate a missing interpreter, executable, module, or dependency error into a claim that the runner cannot see workspace files unless the tool output explicitly reports a missing workspace path",
		"Always quote or summarize the exact execution-tool error or stderr before explaining why a command failed",
		"Always treat `command not found`, `execvp`, and similar missing-executable errors as tooling-availability failures, not file-visibility failures, unless the tool output separately reports a missing workspace path",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implementation-validation prompt missing %q", want)
		}
	}
}

func TestGlobalInspectorSystemPromptForContract_IncludesStrictGlobalReviewProtocol(t *testing.T) {
	prompt := GlobalInspectorSystemPromptForContract(&agentshared.GlobalExecutionContract{
		Role: "inspector-global",
		Mode: agentshared.GlobalExecutionModeAudit,
	})

	for _, want := range []string{
		"Global Inspector Audit Guidance",
		"Strict Global Review Loop",
		"If the plan context is missing, partial, or suspect, call `load_plan_context` before you conclude anything material.",
		"When a tester or architect response arrives, call `process_global_validation` before choosing the next action.",
		"If `finalize_global_review` returns ready-for-commit, you must immediately call `commit_to_disk`.",
		"Treat `load_plan_context`, `consult_librarian_style`, `consult_academic_approach`, and `consult_archivalist_context` as core audit tools, not optional extras.",
		"# Workspace Layers",
		"Global VFS: session-scoped merged but uncommitted work.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("global inspector prompt missing %q", want)
		}
	}
}
