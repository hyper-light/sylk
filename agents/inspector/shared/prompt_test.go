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

	// The implementation-validation protocol is claims-native (per
	// CLAIMS.md §5.10): the historical `pipeline_protocol(action=...)`
	// envelope and the `inspector_forest_consult(purpose=...)` skill
	// are gone, replaced by post_action / evaluate_validation /
	// submit_testaments / consult_peer. These assertions track what
	// the prompt actually instructs today.
	for _, want := range []string{
		// TDD-first ordering: Tester gets the initial test-authoring
		// turn before Engineer or Designer.
		"Default to TDD: after criteria are clear, use `post_action(kind=task)` to send Tester the initial test-authoring turn unless the task is strictly inspection-only.",
		// Audit-on-handback: tester tests, then engineer/designer work.
		"When Tester hands back those initial tests with `post_action(kind=task)`, audit the test artifacts yourself.",
		"When Engineer or Designer hand work back with `post_action(kind=task)`, audit the implementation against your criteria and the current tests before deciding the next step.",
		// Challenge discipline: targeted, not broad re-loops.
		"Use `post_action(kind=challenge)` only for targeted uncertainty in returned work.",
		// Evaluation must precede next dispatch.
		"When a peer responds to your challenge, call `evaluate_validation` before choosing the next handoff, challenge, or closure action.",
		// VFS-state gate on repeat challenges.
		"Before repeating a challenge to Tester, Engineer, or Designer, confirm that same target changed pipeline VFS state since your previous challenge to that target;",
		// Quality bars on the implementer agents.
		"Push Engineer and Designer on correctness, robustness, performance, scope discipline, and production quality; penalize excessive code, premature abstraction, verbosity, and agentic slop.",
		"Push Tester to justify the value of the tests it added; penalize noisy or low-signal testing surface that does not materially increase confidence.",
		// Knowledge-agent routing for factual gaps.
		"consult_peer(target_agent_type=librarian|academic|archivalist, query=…)",
		// Closure gate discipline.
		"Use `validate_criteria` and `grade_task_quality` only when a specific unresolved gap remains that the current returned work, challenge response, or protocol state does not already answer.",
		"Use `finalize_pipeline` only after the current inspector audit is complete and any challenge responses needed for that audit have been evaluated with `evaluate_validation`.",
		"`finalize_pipeline` is the closure gate.",
		"When `finalize_pipeline` requests the final tester-backed acceptance audit, Tester should answer with `submit_testaments`",
		"If the `finalize_pipeline` audit passes and tester evidence confirms the required tests are implemented and passing, you must immediately invoke `handoff_to_ot` and stop looping.",
		"If `finalize_pipeline` returns `ready_for_ot: true` or `must_handoff_to_ot: true`, your very next assistant action must be the `handoff_to_ot` tool call.",
		"Use `handoff_to_ot` only when you are satisfied that the latest `finalize_pipeline` closure step passed and the pipeline should terminate successfully, and do not start another audit cycle once `finalize_pipeline` reports readiness for OT.",
		// Workspace boundaries (composed by BuildWorkspaceViewContext).
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
	// Original regressions: workflow-leak skills the protocol rewrite
	// excluded from the prompt's tool surface. The pipeline_protocol
	// envelope is still referenced by system_skills.md (the skill
	// surface itself remains while the protocol is migrated to
	// claims-native primitives), so it is intentionally not in the
	// blocked set yet — that comes when the skill is retired.
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
