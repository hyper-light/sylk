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
		"Default to TDD: after criteria are clear, use `pipeline_protocol(action=handoff)` to send Tester the initial test-authoring turn unless the task is strictly inspection-only.",
		"Use `pipeline_protocol(action=finalize)` only after the current inspector audit is complete and any challenge responses needed for that audit have been processed with `pipeline_protocol(action=process_validation)`.",
	} {
		if strings.Contains(prompt, blocked) {
			t.Fatalf("pre-implementation prompt unexpectedly contains %q", blocked)
		}
	}
	for _, want := range []string{
		"# Workspace Layers",
		"Pipeline VFS: task-scoped unmerged in-progress work for the active pipeline.",
		"The brokered `bash` execution tool runs against that same layered workspace view",
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
		"Use `pipeline_protocol(action=handoff)` for the normal top-level phase flow",
		"Your first `pipeline_protocol(action=challenge)` call to Tester, Engineer, or Designer is allowed.",
		"you may challenge that same target again only if that target has modified pipeline VFS state since your previous challenge to that target.",
		"Use `pipeline_protocol(action=challenge)` only when returned peer work is unclear, off-spec, incomplete, or otherwise needs a targeted follow-up.",
		"After Tester hands back the initial authored tests, audit those tests against your criteria.",
		"After Engineer or Designer hand work back, audit the implementation against your criteria and the current tests.",
		"Use `pipeline_protocol(action=process_validation)` immediately when another agent answers one of your challenges.",
		"If a challenge response resolves the current audit gap, continue from that evidence.",
		"Push Engineer and Designer like a seasoned staff engineer reviewing senior-level code: audit correctness, robustness, performance, scope discipline, and production quality; penalize excess code, premature abstraction, verbosity, and agentic slop.",
		"Push Tester to prove the test surface adds real value; penalize noisy, arbitrary, or low-quality tests that expand coverage surface without materially improving confidence.",
		"`inspector_forest_consult(purpose=get_validation_targets, query=…)`",
		"`inspector_forest_consult(purpose=get_regression_precedents, query=…)`",
		"Use `pipeline_protocol(action=finalize)` only after you have completed the current inspector audit and processed any challenge responses needed for that audit.",
		"`pipeline_protocol(action=finalize)` is the closure gate, not the default substitute for a targeted challenge.",
		"Use `validate_criteria` and `grade_task_quality` only when a specific unresolved gap remains that the current returned work, challenge response, or protocol state does not already answer.",
		"When `pipeline_protocol(action=finalize)` requests the final tester-backed acceptance audit, Tester should answer with `pipeline_protocol(action=validate)`",
		"If the `pipeline_protocol(action=finalize)` audit passes and tester evidence confirms the required tests are implemented and passing, you must immediately invoke `handoff_to_green` and stop looping.",
		"If `pipeline_protocol(action=finalize)` returns `ready_for_ot: true` or `must_handoff_to_green: true`, your very next assistant action must be the `handoff_to_green` tool call.",
		"Use `handoff_to_green` only when you are satisfied that the latest `pipeline_protocol(action=finalize)` closure step passed and the pipeline should terminate successfully, and do not start another audit cycle once `pipeline_protocol(action=finalize)` reports readiness for OT.",
		"# Workspace Layers",
		"Pipeline VFS: task-scoped unmerged in-progress work for the active pipeline.",
		"The brokered `bash` execution tool runs against that same layered workspace view",
		"Never describe a command failure as a sandbox, bwrap, chdir, project-directory, VFS, or `working_dir` limitation unless the tool output explicitly reports that condition",
		"Treat virtualenv bootstrap or `.venv` execution failures as install-strategy/tooling problems, not sandbox or workspace-visibility proof",
		"Never translate a missing interpreter, executable, module, or dependency error into a claim that the runner cannot see workspace files unless the tool output explicitly reports a missing workspace path",
		"Always quote or summarize the exact execution-tool error or stderr before explaining why a command failed",
		"Always treat `command not found`, `execvp`, and similar missing-executable errors as tooling-availability failures, not file-visibility failures, unless the tool output separately reports a missing workspace path",
		"Never use `bash` to execute test suites or test-runner commands yourself. Test execution belongs to Tester.",
		"If the missing dependency is a test runner, test harness, or other test-execution tool, route that work to Tester instead. Tester uses `dependency(action=research|install, category=test)`.",
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
		"If the plan context is missing, partial, or suspect, call `audit(aspect=context_load)` before you conclude anything material.",
		"Expand from changed files to adjacent files, broader plan slices, or specialist consults only when a concrete unresolved risk requires it.",
		"Use `pipeline_protocol(action=handoff)` for the ordinary top-level Inspector <-> Tester loop",
		"`inspector_forest_consult(purpose=get_validation_targets, query=…)`",
		"`inspector_forest_consult(purpose=get_regression_precedents, query=…)`",
		"When a challenged peer responds, call `pipeline_protocol(action=process_validation)` before choosing any next action.",
		"If `global_review(action=finalize)` returns ready-for-commit, you must immediately call `global_review(action=commit)`.",
		"Target `global-tester` for narrow testing gaps, `orchestrator` for authoritative DAG/workflow/task/pipeline/progress state, and `architect` for plan/rationale defects or stronger alternatives.",
		"Treat `audit(aspect=context_load)` and `consult_peer(target_agent_type=librarian|academic|archivalist|architect, query=…)` as escalation tools for unresolved audit gaps after direct evidence review.",
		"# Workspace Layers",
		"Global VFS: session-scoped merged but uncommitted work.",
		"Do not run test commands yourself. When execution-backed test evidence, coverage, or race results are needed, require them from Tester and audit the returned evidence.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("global inspector prompt missing %q", want)
		}
	}
}
