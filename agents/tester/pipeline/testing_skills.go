package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	testershared "github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/skills"
)

func checkInspectorGateSkill(pt *PipelineTester) *skills.Skill {
	return skills.NewSkill("check_inspector_gate").
		Description("Verify that the inspector gate has passed before any test synthesis or execution begins.").
		Domain("testing").
		Keywords("gate", "inspector", "criteria", "validation").
		Priority(100).
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) {
			passed, reason := pt.inspectorGateStatus()
			return map[string]any{
				"passed": passed,
				"reason": reason,
			}, nil
		}).
		Build()
}

func detectTestHarnessSkill(pt *PipelineTester) *skills.Skill {
	type params struct {
		Files      []string `json:"files,omitempty"`
		TaskSpec   string   `json:"task_spec,omitempty"`
		WorkerType string   `json:"worker_type,omitempty"`
	}

	return skills.NewSkill("detect_test_harness").
		Description("Detect the appropriate test framework, config, run commands, and default output paths for the current task.").
		Domain("testing").
		Keywords("harness", "framework", "tooling", "test setup", "config").
		Priority(98).
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
		Files      []string `json:"files,omitempty"`
		TaskSpec   string   `json:"task_spec,omitempty"`
		WorkerType string   `json:"worker_type,omitempty"`
	}

	return skills.NewSkill("prepare_test_harness").
		Description("Create any missing framework config or boilerplate required before tests can be written and executed.").
		Domain("testing").
		Keywords("prepare", "harness", "bootstrap", "config", "boilerplate").
		Priority(96).
		ArrayParam("files", "Source files that need tests", "string", false).
		StringParam("task_spec", "Task brief and acceptance criteria", false).
		StringParam("worker_type", "Primary worker type such as engineer or designer", false).
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
			created, err := pt.prepareHarness(ctx, state)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"prepared":       true,
				"framework":      state.FrameworkID,
				"created_files":  created,
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
		TestCase   testershared.PlannedTestCase `json:"test_case"`
		TargetFile string                       `json:"target_file"`
		OutputFile string                       `json:"output_file"`
		Content    string                       `json:"content"`
	}

	return skills.NewSkill("write_test").
		Description("Write or append concrete executable test code into the task-local VFS. The content must be real test code, not TODOs or placeholders.").
		Domain("testing").
		Keywords("write", "test", "file", "append", "concrete").
		Priority(94).
		ObjectParam("test_case", "Structured planned test case metadata", testCaseProps, true).
		StringParam("target_file", "Source file under test", true).
		StringParam("output_file", "Destination test file path", false).
		StringParam("content", "Concrete executable test code or test-function body", true).
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
			harness := pt.currentHarnessState()
			if harness == nil {
				var err error
				harness, err = pt.detectHarness(ctx, []string{p.TestCase.TargetFile}, "", "")
				if err != nil {
					return nil, err
				}
			}
			writtenPath, err := pt.writeTestArtifact(ctx, harness, p.TestCase, p.OutputFile, p.Content)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"written":     true,
				"output_file": writtenPath,
				"framework":   harness.FrameworkID,
				"test_name":   p.TestCase.Name,
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
			result, err := pt.executeSuite(ctx, harness, p.Packages, p.Files, p.TestNames, p.Race, p.Verbose, p.Timeout)
			if err != nil {
				return nil, err
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
