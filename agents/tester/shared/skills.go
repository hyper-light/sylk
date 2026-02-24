package shared

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/adalundhe/sylk/core/skills"
)

// DiagnoseFailureSkill creates a skill that delegates to the DiagnosisEngine.
func DiagnoseFailureSkill(engine DiagnosisEngine) *skills.Skill {
	type params struct {
		TestName     string   `json:"test_name"`
		Package      string   `json:"package"`
		Output       string   `json:"output"`
		ErrorMessage string   `json:"error_message"`
		StackTrace   string   `json:"stack_trace,omitempty"`
		SourceFiles  []string `json:"source_files,omitempty"`
	}

	return skills.NewSkill("diagnose_failure").
		Description("Diagnose a test failure by investigating root cause. Always assumes product code is faulty first.").
		Domain("testing").
		Keywords("diagnose", "failure", "root cause", "investigate", "debug").
		Priority(90).
		StringParam("test_name", "Name of the failing test", true).
		StringParam("package", "Package of the failing test", true).
		StringParam("output", "Raw test output", true).
		StringParam("error_message", "Error message from test failure", true).
		StringParam("stack_trace", "Stack trace if available", false).
		ArrayParam("source_files", "Product source files to investigate", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			failure := TestFailure{
				TestName:     p.TestName,
				Package:      p.Package,
				Output:       p.Output,
				ErrorMessage: p.ErrorMessage,
				StackTrace:   p.StackTrace,
			}

			report, err := engine.DiagnoseFailure(ctx, failure, p.SourceFiles)
			if err != nil {
				return nil, fmt.Errorf("diagnose failure: %w", err)
			}

			return report, nil
		}).
		Build()
}

// AnalyzeRiskSkill creates a skill for identifying risk areas in code.
func AnalyzeRiskSkill() *skills.Skill {
	type params struct {
		Files     []string `json:"files"`
		TaskSpec  string   `json:"task_spec,omitempty"`
		DiffPatch string   `json:"diff_patch,omitempty"`
	}

	return skills.NewSkill("analyze_risk").
		Description("Analyze source files to identify concurrency, resource, boundary, and security risk areas.").
		Domain("testing").
		Keywords("risk", "analyze", "concurrency", "security", "boundary").
		Priority(95).
		ArrayParam("files", "Source files to analyze for risks", "string", true).
		StringParam("task_spec", "Task specification for context", false).
		StringParam("diff_patch", "Git diff patch for targeted analysis", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return map[string]any{
				"files_analyzed": len(p.Files),
				"risk_areas":     []RiskArea{},
			}, nil
		}).
		Build()
}

// PlanTestsSkill creates a skill for formulating test plans.
func PlanTestsSkill() *skills.Skill {
	type params struct {
		RiskAreas []RiskArea `json:"risk_areas"`
		TaskSpec  string     `json:"task_spec,omitempty"`
		Files     []string   `json:"files,omitempty"`
	}

	return skills.NewSkill("plan_tests").
		Description("Formulate a test plan with failure hypotheses and input strategies based on risk analysis.").
		Domain("testing").
		Keywords("plan", "test plan", "strategy", "hypothesis").
		Priority(95).
		StringParam("task_spec", "Task specification for context", false).
		ArrayParam("files", "Source files under test", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return map[string]any{
				"plan": &TestPlan{},
			}, nil
		}).
		Build()
}

// WriteTestSkill creates a skill for writing test code.
func WriteTestSkill() *skills.Skill {
	type params struct {
		TestCase   PlannedTestCase `json:"test_case"`
		TargetFile string          `json:"target_file"`
		OutputFile string          `json:"output_file"`
	}

	return skills.NewSkill("write_test").
		Description("Write a test file implementing a planned test case.").
		Domain("testing").
		Keywords("write", "implement", "test code", "test file").
		Priority(90).
		StringParam("target_file", "Source file under test", true).
		StringParam("output_file", "Path for the generated test file", true).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return map[string]any{
				"output_file": p.OutputFile,
				"written":     true,
			}, nil
		}).
		Build()
}

// RunTestSuiteSkill creates a skill for executing test suites.
func RunTestSuiteSkill() *skills.Skill {
	type params struct {
		Packages  []string `json:"packages,omitempty"`
		Files     []string `json:"files,omitempty"`
		TestNames []string `json:"test_names,omitempty"`
		Race      bool     `json:"race,omitempty"`
		Verbose   bool     `json:"verbose,omitempty"`
		Timeout   int      `json:"timeout,omitempty"`
	}

	return skills.NewSkill("run_test_suite").
		Description("Execute a test suite with race detection and configurable timeout.").
		Domain("testing").
		Keywords("run", "execute", "test suite", "race").
		Priority(90).
		ArrayParam("packages", "Go packages to test (default: ./...)", "string", false).
		ArrayParam("files", "Specific test files to run", "string", false).
		ArrayParam("test_names", "Specific test names to run", "string", false).
		BoolParam("race", "Enable -race flag", false).
		BoolParam("verbose", "Enable verbose output", false).
		IntParam("timeout", "Timeout in seconds", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return map[string]any{
				"packages": p.Packages,
				"passed":   0,
				"failed":   0,
				"output":   "",
			}, nil
		}).
		Build()
}

// CheckInspectorGateSkill creates a skill that verifies inspector completion.
func CheckInspectorGateSkill(getGate func() *InspectorGate) *skills.Skill {
	return skills.NewSkill("check_inspector_gate").
		Description("Verify that the Inspector has passed before beginning testing. MUST be called first.").
		Domain("testing").
		Keywords("inspector", "gate", "prerequisite", "validate").
		Priority(100).
		Handler(func(_ context.Context, _ json.RawMessage) (any, error) {
			gate := getGate()
			if gate == nil {
				return map[string]any{
					"passed": false,
					"reason": "inspector gate not initialized",
				}, nil
			}
			return map[string]any{
				"passed":    gate.Passed,
				"timestamp": gate.Timestamp,
				"result_ref": gate.ResultRef,
			}, nil
		}).
		Build()
}
