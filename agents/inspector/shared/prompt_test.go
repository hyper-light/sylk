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
		"Default to TDD: after criteria are clear, use `handoff_next` to send Tester the initial test-authoring turn unless the task is strictly inspection-only.",
		"Use `finalize_pipeline` only after the current inspector audit is complete and any challenge responses needed for that audit have been processed with `process_validation`.",
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
		"Dispatch Tester first for initial red/spec tests before Engineer or Designer so tests shape implementation instead of trailing it",
		"Use `handoff_next` for the normal top-level phase flow",
		"Your first `challenge_agent` call to Tester, Engineer, or Designer is allowed.",
		"you may challenge that same target again only if that target has modified pipeline VFS state since your previous challenge to that target.",
		"Use `challenge_agent` only when returned peer work is unclear, off-spec, incomplete, or otherwise needs a targeted follow-up.",
		"After Tester hands back the initial authored tests, audit those tests against your criteria.",
		"After Engineer or Designer hand work back, audit the implementation against your criteria and the current tests.",
		"Use `process_validation` immediately when another agent answers one of your challenges.",
		"If a challenge response resolves the current audit gap, continue from that evidence.",
		"Push Engineer and Designer like a seasoned staff engineer reviewing senior-level code: audit correctness, robustness, performance, scope discipline, and production quality; penalize excess code, premature abstraction, verbosity, and agentic slop.",
		"Push Tester to prove the test surface adds real value; penalize noisy, arbitrary, or low-quality tests that expand coverage surface without materially improving confidence.",
		"`inspector_forest_get_validation_targets`",
		"`inspector_forest_get_regression_precedents`",
		"Use `finalize_pipeline` only after you have completed the current inspector audit and processed any challenge responses needed for that audit.",
		"`finalize_pipeline` is the closure gate, not the default substitute for a targeted challenge.",
		"Use `validate_criteria` and `grade_task_quality` only when a specific unresolved gap remains that the current returned work, challenge response, or protocol state does not already answer.",
		"When `finalize_pipeline` requests the final tester-backed acceptance audit, Tester should answer with `validate_work`",
		"If the `finalize_pipeline` audit passes and tester evidence confirms the required tests are implemented and passing, you must immediately invoke `handoff_to_ot` and stop looping.",
		"If `finalize_pipeline` returns `ready_for_ot: true` or `must_handoff_to_ot: true`, your very next assistant action must be the `handoff_to_ot` tool call.",
		"Use `handoff_to_ot` only when you are satisfied that the latest `finalize_pipeline` closure step passed and the pipeline should terminate successfully, and do not start another audit cycle once `finalize_pipeline` reports readiness for OT.",
		"# Workspace Layers",
		"Pipeline VFS: task-scoped unmerged in-progress work for the active pipeline.",
		"`run_command` and `run_shell_script` execute against that same layered workspace view",
		"Never describe a command failure as a sandbox, bwrap, chdir, project-directory, VFS, or `working_dir` limitation unless the tool output explicitly reports that condition",
		"Treat virtualenv bootstrap or `.venv` execution failures as install-strategy/tooling problems, not sandbox or workspace-visibility proof",
		"Never translate a missing interpreter, executable, module, or dependency error into a claim that the runner cannot see workspace files unless the tool output explicitly reports a missing workspace path",
		"Always quote or summarize the exact execution-tool error or stderr before explaining why a command failed",
		"Always treat `command not found`, `execvp`, and similar missing-executable errors as tooling-availability failures, not file-visibility failures, unless the tool output separately reports a missing workspace path",
		"Never use `run_command` or `run_shell_script` to execute test suites or test-runner commands yourself. Test execution belongs to Tester.",
		"If the missing dependency is a test runner, test harness, or other test-execution tool, route that work to Tester instead of using inspector install tools. Tester should use `research_test_tool_install` first and `install_test_tooling` only after it has a concrete plan.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("implementation-validation prompt missing %q", want)
		}
	}
	for _, blocked := range []string{"`detect_race_conditions`", "`check_coverage`"} {
		if strings.Contains(prompt, blocked) {
			t.Fatalf("implementation-validation prompt unexpectedly contains %q", blocked)
		}
	}
}

func TestGlobalInspectorSystemPromptForContract_IncludesGlobalReviewProtocol(t *testing.T) {
	prompt := GlobalInspectorSystemPromptForContract(&agentshared.GlobalExecutionContract{
		Role: "inspector-global",
		Mode: agentshared.GlobalExecutionModeAudit,
	})

	for _, want := range []string{
		"Global Inspector Audit Guidance",
		"Global Review Protocol",
		"Call `determine_audit_depth` first on every fresh global-audit branch",
		"Judge the incoming work against the totality of existing merged behavior and pending planned work, but investigate from the returned delta outward",
		"If the plan context is missing, partial, or suspect, call `load_plan_context` before you conclude anything material.",
		"Expand from changed files to adjacent files, broader plan slices, or specialist consults only when a concrete unresolved risk requires it.",
		"Use `handoff_next` for the ordinary top-level Inspector <-> Tester loop",
		"`inspector_forest_get_validation_targets`",
		"`inspector_forest_get_regression_precedents`",
		"When a challenged peer responds, call `process_validation` before choosing any next action.",
		"If `finalize_global_review` returns ready-for-commit, you must immediately call `commit_to_disk`.",
		"Challenge Tester for narrow testing gaps, Orchestrator for authoritative DAG/workflow/task/pipeline/progress state, and Architect for plan/rationale defects or stronger alternatives.",
		"Treat `load_plan_context`, `consult_librarian_style`, `consult_academic_approach`, and `consult_archivalist_context` as escalation tools for unresolved audit gaps after direct evidence review.",
		"# Workspace Layers",
		"Global VFS: session-scoped merged but uncommitted work.",
		"Do not run test commands yourself. When execution-backed test evidence, coverage, or race results are needed, require them from Tester and audit the returned evidence.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("global inspector prompt missing %q", want)
		}
	}
}
