package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/claims"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type pipelineWriteContextInput struct {
	Path  string                         `json:"path"`
	Basis versioning.WorkspaceWriteBasis `json:"basis"`
}

// testHarnessSkill unifies the former detect_test_harness +
// prepare_test_harness pair into one action-dispatched skill.
// action=detect inspects the project for framework / config / run
// commands; action=prepare materializes missing config or
// boilerplate using caller-supplied write contexts. Two verbs of
// the same lifecycle collapsed so the LLM selects the phase via an
// enum rather than choosing between two near-identical tool names.
func testHarnessSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		Action        string                      `json:"action"`
		Files         []string                    `json:"files,omitempty"`
		TaskSpec      string                      `json:"task_spec,omitempty"`
		WorkerType    string                      `json:"worker_type,omitempty"`
		WriteContexts []pipelineWriteContextInput `json:"write_contexts,omitempty"`
	}

	return skills.NewSkill("test_harness").
		Description("Inspect or set up the pipeline's test harness. action=detect reports the framework, config, run commands, and default output paths for the current task. action=prepare materializes missing framework config/boilerplate — requires explicit pipeline write contexts for each file it will create.").
		Domain("testing").
		Keywords("harness", "framework", "tooling", "test setup", "config", "prepare", "bootstrap", "boilerplate", "detect").
		Priority(98).
		Usage("Call action=detect first to discover the test surface, execution commands, and whether setup is required. Only call action=prepare when detect reports setup_required=true; prepare each harness path with workspace_read(op=prepare_write, …) beforehand and pass those write contexts in.").
		Requirement("action=detect: provide target files or task specification. action=prepare: run detect first and gather explicit write_contexts for every harness file this skill may create.").
		Satisfies("Produces runnable harness state and the output-path context needed for planning, writes, and execution.").
		Avoid("Do not hardcode framework or output paths when detect can derive them. Do not call action=prepare when the detected harness is already usable or when write contexts are missing.").
		EnumParam("action", "Harness lifecycle verb", []string{"detect", "prepare"}, true).
		ArrayParam("files", "Source files that need tests", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria", false).
		StringParam("worker_type", "Primary worker type such as engineer or designer", false).
		ArrayObjectParam("write_contexts", "Pipeline write contexts returned by workspace_read(op=prepare_write) for each harness file (action=prepare only).", pipelineWriteContextProperties(), []string{"path", "basis"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			action := strings.TrimSpace(p.Action)
			var result any
			var err error
			switch action {
			case "detect", "":
				result, err = runHarnessDetect(ctx, pt, p.Files, p.TaskSpec, p.WorkerType)
			case "prepare":
				result, err = runHarnessPrepare(ctx, pt, p.Files, p.TaskSpec, p.WorkerType, p.WriteContexts)
			default:
				return nil, fmt.Errorf("unknown test_harness action: %q (expected detect|prepare)", action)
			}
			if acc := claims.AccumulatorFromContext(ctx); acc != nil {
				if action == "" {
					action = "detect"
				}
				acc.Record("test_harness_"+action, fmt.Sprintf("err=%v", err != nil))
				acc.Note("Harness " + action)
			}
			return result, err
		}).
		Build()
}

// runHarnessDetect performs the detect phase and auto-publishes a
// Tentative test_framework decision so peer pipelines observe the
// selected framework in ambient context.
func runHarnessDetect(ctx context.Context, pt *PipelineTester, files []string, taskSpec, workerType string) (any, error) {
	state, err := pt.detectHarness(ctx, files, taskSpec, workerType)
	if err != nil {
		return nil, err
	}
	if state != nil && state.FrameworkID != "" {
		agentshared.AutoPublishTentative(ctx, agentshared.AutoPublishInput{
			SessionID:        pt.config.SessionID,
			AuthorAgentID:    pt.id,
			AuthorAgentType:  "tester-pipeline",
			AuthorPipelineID: pt.pipelineID,
			TriggerSkill:     "test_harness",
			Domain:           "test_framework",
			Value:            string(state.FrameworkID),
			Scope:            firstHarnessFile(files),
			Evidence:         []string{"harness detection from project files"},
		})
	}
	return state, nil
}

// runHarnessPrepare performs the prepare phase: runs detect if the
// current state is unknown, then materializes missing harness files
// against the caller-supplied write contexts.
func runHarnessPrepare(ctx context.Context, pt *PipelineTester, files []string, taskSpec, workerType string, writeContexts []pipelineWriteContextInput) (any, error) {
	state := pt.currentHarnessState()
	if state == nil {
		var err error
		state, err = pt.detectHarness(ctx, files, taskSpec, workerType)
		if err != nil {
			return nil, err
		}
	}
	planned := pt.harnessWritePlans(state)
	contexts, err := indexPipelineWriteContexts(writeContexts)
	if err != nil {
		return nil, err
	}
	if len(planned) > 0 && len(contexts) == 0 {
		return nil, fmt.Errorf("workspace_read(op=prepare_write) is required for harness files: %s", joinPipelineWritePlanPaths(planned))
	}
	created, err := pt.prepareHarness(ctx, state, contexts)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"prepared":       true,
		"framework":      state.FrameworkID,
		"created_files":  created,
		"planned_files":  writePlanPaths(planned),
		"setup_required": state.SetupRequired,
		"setup_reason":   state.SetupReason,
	}, nil
}

// firstHarnessFile returns a path-prefix scope for the auto-publish
// from the file list. Empty when no files are provided.
func firstHarnessFile(files []string) string {
	for _, f := range files {
		if f != "" {
			return f
		}
	}
	return ""
}

func analyzeRiskSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		Files      []string `json:"files"`
		TaskSpec   string   `json:"task_spec,omitempty"`
		WorkerType string   `json:"worker_type,omitempty"`
		DiffPatch  string   `json:"diff_patch,omitempty"`
	}

	return skills.NewSkill("analyze_risk").
		Description("Identify concrete defect risks in the target files, guided by the task specification rather than the current implementation.").
		Domain("testing").
		Keywords("risk", "analyze", "boundary", "security", "concurrency").
		Priority(95).
		Usage("Use after the gate and harness discovery to map the requested behavior to likely defect surfaces. Missing implementation is valid red-phase evidence, not a blocker.").
		Requirement("Provide the most relevant implementation files and task specification so the risk analysis stays tied to the requested work.").
		Satisfies("Produces risk evidence that should shape plan_tests, write_test, and failure reporting.").
		Avoid("Do not stop here when the task still requires executable tests or execution evidence.").
		ArrayParam("files", "Source files to analyze", "string", true).
		StringParam("task_spec", "Task brief and acceptance criteria", false).
		StringParam("worker_type", "Primary worker type such as engineer or designer", false).
		StringParam("diff_patch", "Optional patch context", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			risks := pt.analyzeRisks(ctx, p.Files, joinTaskSpecAndDiff(p.TaskSpec, p.DiffPatch), p.WorkerType)
			// Phase 4 refactor: publish the risk map so engineer
			// sees the hypothesis before implementing.
			evidence := make([]string, 0, len(risks))
			for _, r := range risks {
				evidence = append(evidence, r.Description)
			}
			agentshared.AutoPublishArtifactPublished(ctx, agentshared.AutoPublishArtifactInput{
				SessionID:        pt.config.SessionID,
				AuthorAgentID:    pt.id,
				AuthorAgentType:  "tester-pipeline",
				AuthorPipelineID: pt.pipelineID,
				TriggerSkill:     "analyze_risk",
				Kind:             "risk_map",
				Title:            fmt.Sprintf("risk map: %d files", len(p.Files)),
				Summary:          fmt.Sprintf("%d risk areas identified", len(risks)),
				Scope:            firstHarnessFile(p.Files),
				Evidence:         evidence,
				Payload:          map[string]any{"risks": risks},
			})
			return map[string]any{
				"files_analyzed": len(pt.normalizeTargetFiles(p.Files, p.TaskSpec)),
				"risk_areas":     risks,
			}, nil
		}).
		Build()
}

func planTestsSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		RiskAreas []testershared.RiskArea `json:"risk_areas,omitempty"`
		TaskSpec  string                  `json:"task_spec,omitempty"`
		Files     []string                `json:"files,omitempty"`
	}

	return skills.NewSkill("plan_tests").
		Description("Build a deliberate test plan that maps risks and criteria to concrete executable test cases.").
		Domain("testing").
		Keywords("plan", "test plan", "failure hypothesis", "strategy").
		Priority(95).
		Usage("Use after risk analysis to turn the defect surface into concrete, purposeful test cases. The resulting plan should make the next write_test step clear.").
		Requirement("Prefer to run after analyze_risk or provide equivalent risk areas in the input.").
		Satisfies("Produces the tester plan artifact and defines the concrete cases that write_test should materialize before suite execution and reporting.").
		Avoid("Do not treat the plan as completion. A terminal tester handoff still requires write_test, run_test_suite, and finalize_pipeline before handoff_next.").
		ArrayParam("files", "Source files that need test coverage", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			harness := pt.currentHarnessState()
			plan := pt.buildPlan(p.Files, p.TaskSpec, p.RiskAreas, harness)
			// Phase 4 refactor: publish the test plan so peers see
			// the planned coverage before test code is authored.
			agentshared.AutoPublishArtifactPublished(ctx, agentshared.AutoPublishArtifactInput{
				SessionID:        pt.config.SessionID,
				AuthorAgentID:    pt.id,
				AuthorAgentType:  "tester-pipeline",
				AuthorPipelineID: pt.pipelineID,
				TriggerSkill:     "plan_tests",
				Kind:             "test_plan",
				Title:            fmt.Sprintf("test plan: %d cases", len(plan.PlannedCase)),
				Summary:          fmt.Sprintf("%d risk areas, %d planned cases", len(plan.RiskAreas), len(plan.PlannedCase)),
				Scope:            firstHarnessFile(p.Files),
				Payload:          map[string]any{"plan": plan},
			})
			return map[string]any{
				"plan":       plan,
				"risk_count": len(plan.RiskAreas),
				"case_count": len(plan.PlannedCase),
			}, nil
		}).
		Build()
}

func writeTestSkill(pt *PipelineTester) *skills.Skill {
	testCaseProps := map[string]*skills.Property{
		"name":               {Type: "string", Description: "Deterministic test name"},
		"category":           {Type: "string", Description: "Test category"},
		"failure_hypothesis": {Type: "string", Description: "What defect this test should catch"},
		"input_strategy":     {Type: "string", Description: "How the test exercises the code"},
		"expected_behavior":  {Type: "string", Description: "What should happen"},
		"target_file":        {Type: "string", Description: "Source file under test"},
	}

	type params struct {
		TestCase   testershared.PlannedTestCase   `json:"test_case"`
		TargetFile string                         `json:"target_file"`
		OutputFile string                         `json:"output_file"`
		Content    string                         `json:"content"`
		Basis      versioning.WorkspaceWriteBasis `json:"basis"`
	}

	return skills.NewSkill("write_test").
		Description("Write or append concrete executable test code into the task-local VFS. Requires a fresh or still-leased pipeline write basis for the target output file, auto-renews on lease expiry, and returns a refreshed next_basis.").
		Domain("testing").
		Keywords("write", "test", "file", "append", "concrete").
		Priority(94).
		Usage("Use to materialize executable tests after you know the intended case. Prepare the output path with prepare_pipeline_write_context first, pass the basis in, and reuse next_basis for follow-up writes to the same file. Confirm the test framework, language, and conventions match the priority order in the system prompt before authoring: the task spec governs language and framework choice, in-flight peer work in the Activity Fabric defines test conventions on this surface, and the existing codebase governs test placement and runner integration. detect_test_harness already returns language_source / language_note for this surface — read those before writing.").
		Requirement("Provide a concrete test_case, executable content, and a matching pipeline write basis for the target output file. Test code language must match the output_file extension and the harness selected for this surface; do not write Go syntax in a .py file or pytest fixtures in a .go file.").
		Satisfies("Creates real test artifacts and advances the authoring deliverable for the task. After writing, run_test_suite should gather execution evidence before terminal reporting.").
		Avoid("Do not use for placeholders, TODOs, skipped tests, or speculative writes detached from the requested behavior. write_test alone is not a terminal handoff step. Do not introduce a test framework that disagrees with the harness language reported by detect_test_harness — reconcile the language signal first (re-read the task spec, query peer activity, or invoke ask_user_clarification or a knowledge-agent consult) before writing.").
		ObjectParam("test_case", "Structured planned test case metadata", testCaseProps, true).
		StringParam("target_file", "Source file under test", true).
		StringParam("output_file", "Destination test file path", false).
		StringParam("content", "Concrete executable test code or test-function body", true).
		ObjectParam("basis", "Pipeline write basis returned by prepare_pipeline_write_context for the output_file.", pipelineWriteBasisProperties(), true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if strings.TrimSpace(p.TargetFile) != "" {
				p.TestCase.TargetFile = p.TargetFile
			}
			if strings.TrimSpace(p.TestCase.TargetFile) == "" {
				return nil, fmt.Errorf("target_file is required")
			}
			if strings.TrimSpace(p.TestCase.Name) == "" {
				return nil, fmt.Errorf("test_case.name is required")
			}
			if strings.TrimSpace(p.Content) == "" {
				return nil, fmt.Errorf("content is required and must contain executable test code")
			}
			if p.Basis.Scope == "" || strings.TrimSpace(p.Basis.Path) == "" {
				return nil, fmt.Errorf("basis is required")
			}
			harness := pt.currentHarnessState()
			if harness == nil {
				var err error
				harness, err = pt.detectHarness(ctx, []string{p.TestCase.TargetFile}, "", "")
				if err != nil {
					return nil, err
				}
			}
			// Cross-pipeline coherence is enforced by the Activity
			// Fabric, not by gating write_test. The fabric surfaces
			// peer pipeline commitments through ambient context; the
			// LLM adopts compatible commitments by default and uses
			// challenge_peer with concrete evidence when it disagrees.
			// Auto-publishing the test_framework projection from
			// detect_test_harness + write_test is what keeps parallel
			// pipelines coherent — see docs/FABRIC.md "no-gates,
			// auto-publish, ambient awareness."
			writtenPath, err := pt.writeTestArtifact(ctx, harness, p.TestCase, p.OutputFile, p.Content, &p.Basis)
			if err != nil {
				return nil, err
			}
			// Activity Fabric auto-publish: write_test promotes the
			// test_framework choice to Committed — the work is done,
			// the file exists. The fabric's amplifier reconciliation
			// surfaces this as a promotion of any prior Tentative
			// declaration with the same value at overlapping scope.
			if harness != nil && harness.FrameworkID != "" {
				agentshared.AutoPublishCommitted(ctx, agentshared.AutoPublishInput{
					SessionID:        pt.config.SessionID,
					AuthorAgentID:    pt.id,
					AuthorAgentType:  "tester-pipeline",
					AuthorPipelineID: pt.pipelineID,
					TriggerSkill:     "write_test",
					Domain:           "test_framework",
					Value:            string(harness.FrameworkID),
					Scope:            writtenPath,
					Evidence:         []string{"test file authored: " + writtenPath},
				})
			}
			return map[string]any{
				"written":     true,
				"output_file": writtenPath,
				"framework":   harness.FrameworkID,
				"test_name":   p.TestCase.Name,
				"next_basis":  p.Basis,
			}, nil
		}).
		Build()
}

func runTestSuiteSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		Packages  []string `json:"packages,omitempty"`
		Files     []string `json:"files,omitempty"`
		TestNames []string `json:"test_names,omitempty"`
		Race      bool     `json:"race,omitempty"`
		Verbose   bool     `json:"verbose,omitempty"`
		Timeout   int      `json:"timeout,omitempty"`
	}

	return skills.NewSkill("run_test_suite").
		Description("Execute the synthesized tests against the current task-local workspace, including VFS overlay state when present.").
		Domain("testing").
		Keywords("run", "test", "suite", "race", "execute").
		Priority(92).
		Usage("Use after write_test when the task requires execution evidence or when you need a concrete failing signal to diagnose. Target the most relevant packages, files, or tests instead of running blindly.").
		Satisfies("Produces suite execution evidence and the raw failure signal needed for diagnose_failure and for the per-recipient verification artifact published by finalize_pipeline.").
		Avoid("Do not use as a substitute for write_test when the task still requires new test artifacts, and do not stop here when the turn still requires terminal reporting plus handoff_next.").
		Produces(agentshared.ArtifactTestSnapshot).
		ArrayParam("packages", "Package patterns to test", "string", false).
		ArrayParam("files", "Source or test files to focus on", "string", false).
		ArrayParam("test_names", "Specific tests to run", "string", false).
		BoolParam("race", "Enable race detection when supported", false).
		BoolParam("verbose", "Enable verbose test output", false).
		IntParam("timeout", "Timeout in seconds", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			harness := pt.currentHarnessState()
			start := time.Now()
			// Phase 4 refactor: emit validation_started before the
			// suite runs so peers see execution is in progress.
			epochID := pt.pipelineID
			if epochID == "" {
				epochID = fmt.Sprintf("suite-%d", start.UnixNano())
			}
			startedID := agentshared.AutoPublishValidationStarted(ctx, agentshared.AutoPublishValidationInput{
				SessionID:       pt.config.SessionID,
				ActorAgentID:    pt.id,
				ActorAgentType:  "tester-pipeline",
				ActorPipelineID: pt.pipelineID,
				TriggerSkill:    "run_test_suite",
				EpochID:         epochID,
				Scope:           firstHarnessFile(p.Files),
				Summary:         fmt.Sprintf("running suite for %d packages / %d files", len(p.Packages), len(p.Files)),
			})
			result, err := pt.executeSuite(ctx, harness, p.Packages, p.Files, p.TestNames, p.Race, p.Verbose, p.Timeout)
			if err != nil {
				// Suite execution errored (not a test failure).
				// Treat as rejected so the fabric reflects reality.
				agentshared.AutoPublishValidationRejected(ctx, agentshared.AutoPublishValidationInput{
					SessionID:       pt.config.SessionID,
					ActorAgentID:    pt.id,
					ActorAgentType:  "tester-pipeline",
					ActorPipelineID: pt.pipelineID,
					TriggerSkill:    "run_test_suite",
					EpochID:         epochID,
					Scope:           firstHarnessFile(p.Files),
					Summary:         fmt.Sprintf("suite execution error: %v", err),
				}, startedID)
				return nil, err
			}
			suite := suiteResultFromExecution(result, start)
			pt.setLastSuiteResult(suite)
			// Phase 4 refactor: emit validation outcome so peers
			// see the pass/fail verdict in ambient context.
			failureCount := 0
			passCount := 0
			if suite != nil {
				failureCount = suite.Failed
				passCount = suite.Passed
			}
			validationInput := agentshared.AutoPublishValidationInput{
				SessionID:       pt.config.SessionID,
				ActorAgentID:    pt.id,
				ActorAgentType:  "tester-pipeline",
				ActorPipelineID: pt.pipelineID,
				TriggerSkill:    "run_test_suite",
				EpochID:         epochID,
				Scope:           firstHarnessFile(p.Files),
				FailureCount:    failureCount,
			}
			if suite != nil && failureCount == 0 && passCount > 0 {
				validationInput.Summary = fmt.Sprintf("passed=%d", passCount)
				agentshared.AutoPublishValidationAccepted(ctx, validationInput, startedID)
			} else if suite != nil {
				validationInput.Summary = fmt.Sprintf("passed=%d failed=%d", passCount, failureCount)
				agentshared.AutoPublishValidationRejected(ctx, validationInput, startedID)
			}
			return result, nil
		}).
		Build()
}

func joinTaskSpecAndDiff(taskSpec, diff string) string {
	if strings.TrimSpace(diff) == "" {
		return taskSpec
	}
	if strings.TrimSpace(taskSpec) == "" {
		return diff
	}
	return taskSpec + "\n\nDiff:\n" + diff
}

func pipelineWriteContextProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"path":  {Type: "string", Description: "Target file path this write context applies to."},
		"basis": {Type: "object", Description: "Basis returned by prepare_pipeline_write_context for this path.", Properties: pipelineWriteBasisProperties()},
	}
}

func pipelineWriteBasisProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"scope":            {Type: "string", Description: "Must be pipeline."},
		"path":             {Type: "string", Description: "Path prepared for mutation."},
		"pipeline_id":      {Type: "string", Description: "Active task pipeline ID."},
		"target_view":      {Type: "string", Description: "Must be pipeline."},
		"prepared_at":      {Type: "string", Description: "When the write basis snapshot was prepared."},
		"lease_expires_at": {Type: "string", Description: "When the write lease expires unless renewed by the next write."},
	}
}

func indexPipelineWriteContexts(inputs []pipelineWriteContextInput) (map[string]versioning.WorkspaceWriteBasis, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	contexts := make(map[string]versioning.WorkspaceWriteBasis, len(inputs))
	for i, input := range inputs {
		path := normalizePipelineWritePath(input.Path)
		if path == "" {
			path = normalizePipelineWritePath(input.Basis.Path)
		}
		if path == "" {
			return nil, fmt.Errorf("write_contexts[%d].path is required", i)
		}
		contexts[path] = input.Basis
	}
	return contexts, nil
}

func writePlanPaths(plans []pipelineWritePlan) []string {
	paths := make([]string, 0, len(plans))
	for _, plan := range plans {
		paths = append(paths, plan.Path)
	}
	return paths
}

func joinPipelineWritePlanPaths(plans []pipelineWritePlan) string {
	return strings.Join(writePlanPaths(plans), ", ")
}
