package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/skills"
	"github.com/adalundhe/sylk/core/versioning"
)

type pipelineWriteContextInput struct {
	Path  string                         `json:"path"`
	Basis versioning.WorkspaceWriteBasis `json:"basis"`
}

func detectTestHarnessSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		Files         []string                    `json:"files,omitempty"`
		TaskSpec      string                      `json:"task_spec,omitempty"`
		WorkerType    string                      `json:"worker_type,omitempty"`
		WriteContexts []pipelineWriteContextInput `json:"write_contexts,omitempty"`
	}

	return skills.NewSkill("detect_test_harness").
		Description("Detect the appropriate test framework, config, run commands, and default output paths for the current task.").
		Domain("testing").
		Keywords("harness", "framework", "tooling", "test setup", "config").
		Priority(98).
		Usage("Use after understanding the current criteria and challenge to discover the real test surface, output paths, and execution commands for the requested work.").
		Requirement("Provide the target files or task specification so the harness decision is scoped to the requested behavior.").
		Satisfies("Identifies the harness and output-path context needed for planning, harness prep, writes, and execution.").
		Avoid("Do not hardcode a framework or output path when this skill can derive it from the project and task context.").
		ArrayParam("files", "Source files that need tests", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria", false).
		StringParam("worker_type", "Primary worker type such as engineer or designer", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			state, err := pt.detectHarness(ctx, p.Files, p.TaskSpec, p.WorkerType)
			if err != nil {
				return nil, err
			}
			return state, nil
		}).
		Build()
}

func prepareTestHarnessSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		Files         []string                    `json:"files,omitempty"`
		TaskSpec      string                      `json:"task_spec,omitempty"`
		WorkerType    string                      `json:"worker_type,omitempty"`
		WriteContexts []pipelineWriteContextInput `json:"write_contexts,omitempty"`
	}

	return skills.NewSkill("prepare_test_harness").
		Description("Create any missing framework config or boilerplate required before tests can be written and executed. Requires explicit pipeline write contexts for each file it will create.").
		Domain("testing").
		Keywords("prepare", "harness", "bootstrap", "config", "boilerplate").
		Priority(96).
		Usage("Use only when the harness is missing config or bootstrap files. Prepare each harness path with prepare_pipeline_write_context first and pass those write contexts in.").
		Requirement("Run detect_test_harness first and gather explicit write contexts for every harness file this skill may create.").
		Satisfies("Produces runnable harness/config state so subsequent write_test and run_test_suite calls operate on the right framework.").
		Avoid("Do not call when the detected harness is already usable or when you have not prepared the needed write contexts.").
		ArrayParam("files", "Source files that need tests", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria", false).
		StringParam("worker_type", "Primary worker type such as engineer or designer", false).
		ArrayObjectParam("write_contexts", "Pipeline write contexts returned by prepare_pipeline_write_context for each harness file that may be created.", pipelineWriteContextProperties(), []string{"path", "basis"}, false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			state := pt.currentHarnessState()
			if state == nil {
				var err error
				state, err = pt.detectHarness(ctx, p.Files, p.TaskSpec, p.WorkerType)
				if err != nil {
					return nil, err
				}
			}
			planned := pt.harnessWritePlans(state)
			contexts, err := indexPipelineWriteContexts(p.WriteContexts)
			if err != nil {
				return nil, err
			}
			if len(planned) > 0 && len(contexts) == 0 {
				return nil, fmt.Errorf("prepare_pipeline_write_context is required for harness files: %s", joinPipelineWritePlanPaths(planned))
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
		}).
		Build()
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
		Usage("Use after risk analysis to turn the defect surface into concrete, purposeful test cases. The resulting plan should make the next write or execution step clear.").
		Requirement("Prefer to run after analyze_risk or provide equivalent risk areas in the input.").
		Satisfies("Produces the tester plan artifact and defines the concrete cases that write_test should materialize.").
		Avoid("Do not substitute the plan for actual test writing when the requested deliverable still requires test artifacts.").
		ArrayParam("files", "Source files that need test coverage", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria", false).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			harness := pt.currentHarnessState()
			plan := pt.buildPlan(p.Files, p.TaskSpec, p.RiskAreas, harness)
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
		Usage("Use to materialize executable tests after you know the intended case. Prepare the output path with prepare_pipeline_write_context first, pass the basis in, and reuse next_basis for follow-up writes to the same file.").
		Requirement("Provide a concrete test_case, executable content, and a matching pipeline write basis for the target output file.").
		Satisfies("Creates real test artifacts and advances the authoring deliverable for the task.").
		Avoid("Do not use for placeholders, TODOs, skipped tests, or speculative writes detached from the requested behavior.").
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
			writtenPath, err := pt.writeTestArtifact(ctx, harness, p.TestCase, p.OutputFile, p.Content, &p.Basis)
			if err != nil {
				return nil, err
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
		Usage("Use when the task requires execution evidence or when you need a concrete failing signal to diagnose. Target the most relevant packages, files, or tests instead of running blindly.").
		Satisfies("Produces suite execution evidence and the raw failure signal needed for diagnose_failure.").
		Avoid("Do not use as a substitute for write_test when the task still requires new test artifacts.").
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
			result, err := pt.executeSuite(ctx, harness, p.Packages, p.Files, p.TestNames, p.Race, p.Verbose, p.Timeout)
			if err != nil {
				return nil, err
			}
			pt.setLastSuiteResult(suiteResultFromExecution(result, start))
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
