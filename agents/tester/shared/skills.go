package shared

import (
	"context"
	"encoding/json"
	"fmt"

	agentshared "github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/agentlog"
	"github.com/adalundhe/sylk/core/skills"
)

type RiskAnalyzer interface {
	AnalyzeRisks(ctx context.Context, files []string, taskSpec, workerType, diffPatch string) ([]RiskArea, error)
}

type TestPlanner interface {
	PlanTests(ctx context.Context, riskAreas []RiskArea, taskSpec string, files []string) (*TestPlan, error)
}

type SuiteRunRequest struct {
	Packages  []string
	Files     []string
	TestNames []string
	Race      bool
	Verbose   bool
	Timeout   int
}

type SuiteRunner interface {
	RunTestSuite(ctx context.Context, req SuiteRunRequest) (map[string]any, error)
}

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
		Usage("Use after a concrete suite failure when you have real failing output to investigate. Read the implementation and test evidence, then explain the most likely product defect before blaming the test.").
		Requirement("Requires failing suite evidence such as run_test_suite output, error message, and the most relevant source files when available.").
		Satisfies("Produces root-cause evidence that supports failure reporting and follow-up fixes.").
		Avoid("Do not use as a substitute for running the suite or for speculative debugging before there is an actual failing signal.").
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

			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventCaseFailed,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.TestCasePayload{CaseName: p.TestName, Phase: "diagnosed"})
			}

			return report, nil
		}).
		Build()
}

// AnalyzeRiskSkill creates a skill for identifying risk areas in code.
func AnalyzeRiskSkill(analyzer RiskAnalyzer) *skills.Skill {
	type params struct {
		Files      []string `json:"files"`
		TaskSpec   string   `json:"task_spec,omitempty"`
		WorkerType string   `json:"worker_type,omitempty"`
		DiffPatch  string   `json:"diff_patch,omitempty"`
	}

	return skills.NewSkill("analyze_risk").
		Description("Analyze source files to identify concurrency, resource, boundary, and security risk areas.").
		Domain("testing").
		Keywords("risk", "analyze", "concurrency", "security", "boundary").
		Priority(95).
		Usage("Use early in a testing pass to identify where defects are most likely and to shape the test plan around real failure hypotheses. Missing implementation can still be valid red-phase evidence.").
		Requirement("Provide the most relevant implementation files and the task specification so the analysis stays specification-driven.").
		Satisfies("Produces risk evidence that informs plan_tests, write_test, and downstream reporting.").
		Avoid("Do not stop here when the requested deliverable is executable tests or execution evidence.").
		ArrayParam("files", "Source files to analyze for risks", "string", true).
		StringParam("task_spec", "Task specification for context", false).
		StringParam("worker_type", "Primary worker type such as engineer or designer", false).
		StringParam("diff_patch", "Git diff patch for targeted analysis", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if analyzer == nil {
				return nil, fmt.Errorf("risk analysis is unavailable")
			}
			risks, err := analyzer.AnalyzeRisks(ctx, p.Files, p.TaskSpec, p.WorkerType, p.DiffPatch)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"files_analyzed": len(p.Files),
				"risk_areas":     risks,
				"risk_count":     len(risks),
			}, nil
		}).
		Build()
}

// PlanTestsSkill creates a skill for formulating test plans.
func PlanTestsSkill(planner TestPlanner) *skills.Skill {
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
		Usage("Use after risk analysis to convert the defect surface into concrete, purposeful test cases. The plan should make the next write_test or run_test_suite call obvious.").
		Requirement("Prefer to supply the risk areas or equivalent specification-driven failure hypotheses first.").
		Satisfies("Produces deliberate test-case structure and satisfies the tester planning stage.").
		Avoid("Do not treat the plan itself as completion when the requested deliverable still requires test artifacts or suite execution.").
		ArrayObjectParam("risk_areas", "Risk areas that should directly inform the planned coverage.", riskAreaProperties(), []string{"file", "category", "level", "description"}, false).
		StringParam("task_spec", "Task specification for context", false).
		ArrayParam("files", "Source files under test", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			if planner == nil {
				return nil, fmt.Errorf("test planning is unavailable")
			}
			plan, err := planner.PlanTests(ctx, p.RiskAreas, p.TaskSpec, p.Files)
			if err != nil {
				return nil, err
			}
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventTestPlanCreated,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.TestPlanPayload{TestCount: len(plan.PlannedCase)})
			}
			return map[string]any{
				"plan":       plan,
				"risk_count": len(plan.RiskAreas),
				"case_count": len(plan.PlannedCase),
			}, nil
		}).
		Build()
}

// RunTestSuiteSkill creates a skill for executing test suites.
func RunTestSuiteSkill(runner SuiteRunner) *skills.Skill {
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
		Usage("Use when the requested deliverable includes execution evidence or when you need concrete failing output for diagnosis. Target the most relevant packages, files, or tests rather than running blindly.").
		Satisfies("Produces suite execution evidence and, on failure, the raw signal needed for diagnose_failure.").
		Avoid("Do not use as a substitute for test authoring when the task still requires new test artifacts.").
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
			if runner == nil {
				return nil, fmt.Errorf("test execution is unavailable")
			}
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventSuiteStarted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.TestSuitePayload{Phase: "started"})
			}
			result, err := runner.RunTestSuite(ctx, SuiteRunRequest{
				Packages:  p.Packages,
				Files:     p.Files,
				TestNames: p.TestNames,
				Race:      p.Race,
				Verbose:   p.Verbose,
				Timeout:   p.Timeout,
			})
			if err != nil {
				return nil, err
			}
			if lm := agentshared.LogMetaFromContext(ctx); lm.EventLogger != nil {
				agentshared.LogAgentEvent(lm.EventLogger, agentlog.EventSuiteCompleted,
					lm.AgentID, lm.SessionID, lm.CorrID, "info",
					&agentlog.TestSuitePayload{Phase: "completed"})
			}
			return result, nil
		}).
		Build()
}

func riskAreaProperties() map[string]*skills.Property {
	return map[string]*skills.Property{
		"file":        {Type: "string", Description: "Path associated with the risk."},
		"function":    {Type: "string", Description: "Optional function or symbol associated with the risk."},
		"category":    {Type: "string", Description: "Risk category."},
		"level":       {Type: "string", Description: "Risk severity level."},
		"description": {Type: "string", Description: "Why this risk matters."},
	}
}
