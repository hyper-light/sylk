package global

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/agents/tester/shared"
	"github.com/adalundhe/sylk/core/skills"
)

// analyzeBatchSkill creates a skill that collects batch context from completed pipelines.
func analyzeBatchSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		PipelineIDs  []string          `json:"pipeline_ids"`
		ChangedFiles []string          `json:"changed_files,omitempty"`
		TaskSpecs    map[string]string `json:"task_specs,omitempty"`
	}

	return skills.NewSkill("analyze_batch").
		Description("Collect and analyze batch context from completed pipelines — changed files, task specs, pipeline results.").
		Domain("testing").
		Keywords("batch", "context", "pipeline", "collect", "analyze").
		Priority(95).
		Usage("Use first when the global task depends on multiple completed pipelines or cross-pipeline context. It assembles the concrete surface the rest of the global testing work should reason over.").
		Requirement("Provide the relevant pipeline IDs and any already-known changed files or task specs so later planning stays grounded in the actual batch.").
		Satisfies("Produces the shared batch context needed for integration-risk analysis, harness planning, and global validation reporting.").
		Avoid("Do not skip it and reason from memory when the request depends on multiple pipelines or accumulated task artifacts.").
		ArrayParam("pipeline_ids", "IDs of completed pipelines to analyze", "string", true).
		ArrayParam("changed_files", "Files changed across all pipelines", "string", false).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			gt.mu.Lock()
			gt.batchContext = &shared.BatchContext{
				PipelineIDs:  p.PipelineIDs,
				ChangedFiles: p.ChangedFiles,
				TaskSpecs:    p.TaskSpecs,
			}
			gt.mu.Unlock()

			return map[string]any{
				"pipelines_analyzed": len(p.PipelineIDs),
				"changed_files":      len(p.ChangedFiles),
				"task_specs":         len(p.TaskSpecs),
			}, nil
		}).
		Build()
}

// analyzeIntegrationRisksSkill creates a skill for cross-pipeline risk analysis.
func analyzeIntegrationRisksSkill() *skills.Skill {
	type params struct {
		ChangedFiles []string `json:"changed_files"`
		PipelineIDs  []string `json:"pipeline_ids,omitempty"`
		FocusAreas   []string `json:"focus_areas,omitempty"`
	}

	return skills.NewSkill("analyze_integration_risks").
		Description("Analyze cross-pipeline integration risks — shared state mutations, API contract changes, cascading failures.").
		Domain("testing").
		Keywords("integration", "risk", "cross-pipeline", "shared state", "API contract").
		Priority(95).
		Usage("Use after assembling the relevant batch context or whenever a global task asks for integration, system, or cross-pipeline risk analysis.").
		Requirement("Provide the concrete changed files and, when possible, the relevant pipeline IDs or focus areas so the analysis stays scoped to the actual request.").
		Satisfies("Produces cross-pipeline risk evidence that should shape integration plans, harness work, execution, and escalation.").
		Avoid("Do not stop here when the request still requires authored global tests, execution evidence, or an escalation report.").
		ArrayParam("changed_files", "Files changed across pipelines", "string", true).
		ArrayParam("pipeline_ids", "Relevant pipeline IDs", "string", false).
		ArrayParam("focus_areas", "Specific risk areas to focus on", "string", false).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return map[string]any{
				"files_analyzed": len(p.ChangedFiles),
				"risk_areas":     []shared.RiskArea{},
			}, nil
		}).
		Build()
}

// planIntegrationTestsSkill creates a skill for planning integration tests.
func planIntegrationTestsSkill() *skills.Skill {
	type params struct {
		RiskAreas []shared.RiskArea `json:"risk_areas"`
		BatchCtx  map[string]any    `json:"batch_context,omitempty"`
	}

	return skills.NewSkill("plan_integration_tests").
		Description("Design integration test strategy based on cross-pipeline risk analysis.").
		Domain("testing").
		Keywords("plan", "integration", "strategy", "test plan").
		Priority(90).
		Usage("Use when the task needs an integration-test plan grounded in identified system risks and shared behavior boundaries.").
		Requirement("Prefer to supply concrete risk areas or an already assembled batch context so the resulting plan maps to real integration concerns.").
		Satisfies("Produces an integration test plan that can guide global write tools, harness work, and downstream execution.").
		Avoid("Do not treat the plan as completion when the requested deliverable still requires authored tests or suite execution.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return map[string]any{
				"plan":       &shared.TestPlan{},
				"risk_count": len(p.RiskAreas),
			}, nil
		}).
		Build()
}

// planE2ETestsSkill creates a skill for planning end-to-end tests.
func planE2ETestsSkill() *skills.Skill {
	type params struct {
		RiskAreas    []shared.RiskArea    `json:"risk_areas"`
		HarnessNeeds []shared.HarnessNeed `json:"harness_needs,omitempty"`
	}

	return skills.NewSkill("plan_e2e_tests").
		Description("Design end-to-end test strategy covering full system flows and user scenarios.").
		Domain("testing").
		Keywords("plan", "e2e", "end-to-end", "strategy", "test plan").
		Priority(90).
		Usage("Use when the request calls for user-flow, end-to-end, or full-system scenario planning rather than narrow integration edges alone.").
		Requirement("Supply the known risk areas and harness needs so the E2E plan reflects realistic flows and environment constraints.").
		Satisfies("Produces the end-to-end test plan that should drive harness preparation, authored scenarios, and global execution.").
		Avoid("Do not substitute this plan for actually writing or running the requested global tests.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}
			return map[string]any{
				"plan":          &shared.TestPlan{},
				"risk_count":    len(p.RiskAreas),
				"harness_needs": len(p.HarnessNeeds),
			}, nil
		}).
		Build()
}

// buildHarnessSkill creates a skill for constructing test infrastructure.
func buildHarnessSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		Fixtures    []TestFixture `json:"fixtures,omitempty"`
		MockServers []MockServer  `json:"mock_servers,omitempty"`
		TestDBs     []TestDB      `json:"test_dbs,omitempty"`
	}

	return skills.NewSkill("build_harness").
		Description("Plan or register test fixtures, mock servers, and test databases for integration/e2e testing. If harness files must be created, materialize them separately with prepare_global_write_context and the global test write skills.").
		Domain("testing").
		Keywords("harness", "fixture", "mock", "infrastructure", "setup").
		Priority(88).
		Usage("Use when the global task requires reusable integration or end-to-end infrastructure before authored tests or execution can succeed.").
		Requirement("Detect the harness needs first and prepare leased write contexts separately for any files this harness work will materialize.").
		Satisfies("Produces reusable harness state and the concrete infrastructure plan needed for global test authoring and execution.").
		Avoid("Do not treat it as a direct file mutation step; actual harness files still belong to the leased global write tools.").
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			gt.mu.Lock()
			if gt.harness == nil {
				gt.harness = NewTestHarness()
			}
			for _, f := range p.Fixtures {
				gt.harness.AddFixture(f)
			}
			for _, m := range p.MockServers {
				gt.harness.AddMockServer(m)
			}
			for _, db := range p.TestDBs {
				gt.harness.AddTestDB(db)
			}
			summary := gt.harness.Summary()
			gt.mu.Unlock()

			return map[string]any{
				"built":                  true,
				"summary":                summary,
				"requires_write_context": true,
				"write_scope":            "global",
			}, nil
		}).
		Build()
}

// reportToOrchestratorSkill creates a skill that sends failure escalation to the Orchestrator.
func reportToOrchestratorSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		TestName      string   `json:"test_name"`
		Confidence    float64  `json:"confidence"`
		IsProductBug  bool     `json:"is_product_bug"`
		RootCause     string   `json:"root_cause"`
		AffectedTasks []string `json:"affected_tasks"`
	}

	return skills.NewSkill("report_to_orchestrator").
		Description("Escalate a test failure to the Orchestrator to pause new work dispatching.").
		Domain("testing").
		Keywords("report", "orchestrator", "escalate", "pause").
		Priority(85).
		Usage("Use when a global testing result proves a systemic failure that should pause new work or trigger orchestration-level intervention.").
		Requirement("Requires a real failure signal, concrete root cause, confidence level, and affected task scope.").
		Satisfies("Publishes a durable orchestrator escalation that can pause further work and preserve the diagnosis context.").
		Avoid("Do not use for speculative concerns or local issues that do not warrant orchestration-level intervention.").
		StringParam("test_name", "Name of the failing test", true).
		FloatParam("confidence", "Confidence in the diagnosis (0-1)", true).
		BoolParam("is_product_bug", "Whether the failure is a product bug", true).
		StringParam("root_cause", "Root cause description", true).
		ArrayParam("affected_tasks", "Task IDs affected by the failure", "string", false).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			report := &shared.DiagnosisReport{
				TestName:     p.TestName,
				Confidence:   p.Confidence,
				IsProductBug: p.IsProductBug,
				RootCauses: []shared.RootCause{{
					Description: p.RootCause,
				}},
				CreatedAt: time.Now(),
			}

			if err := gt.reportToOrchestrator(report, p.AffectedTasks); err != nil {
				return nil, fmt.Errorf("report to orchestrator: %w", err)
			}

			return map[string]any{
				"reported":  true,
				"target":    "orchestrator",
				"test_name": p.TestName,
			}, nil
		}).
		Build()
}

// reportToArchitectSkill creates a skill that sends failure reports to the Architect.
func reportToArchitectSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		TestName      string   `json:"test_name"`
		Confidence    float64  `json:"confidence"`
		IsProductBug  bool     `json:"is_product_bug"`
		RootCause     string   `json:"root_cause"`
		SuggestedFix  string   `json:"suggested_fix"`
		AffectedTasks []string `json:"affected_tasks"`
	}

	return skills.NewSkill("report_to_architect").
		Description("Report a test failure to the Architect with root cause and suggested plan modification.").
		Domain("testing").
		Keywords("report", "architect", "plan", "modification").
		Priority(85).
		Usage("Use when a global testing result shows the current plan, architecture, or sequencing should be reconsidered.").
		Requirement("Requires a real diagnosis with root cause, confidence, suggested fix, and affected task scope.").
		Satisfies("Publishes a plan-level escalation the Architect can use to adjust workflow, dependencies, or design direction.").
		Avoid("Do not use when the finding is purely local implementation debt that should go straight to an engineer or designer fix path.").
		StringParam("test_name", "Name of the failing test", true).
		FloatParam("confidence", "Confidence in the diagnosis (0-1)", true).
		BoolParam("is_product_bug", "Whether the failure is a product bug", true).
		StringParam("root_cause", "Root cause description", true).
		StringParam("suggested_fix", "Suggested fix approach", true).
		ArrayParam("affected_tasks", "Task IDs affected by the failure", "string", false).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			report := &shared.DiagnosisReport{
				TestName:     p.TestName,
				Confidence:   p.Confidence,
				IsProductBug: p.IsProductBug,
				RootCauses: []shared.RootCause{{
					Description: p.RootCause,
				}},
				SuggestedFix: []shared.SuggestedFix{{
					Description: p.SuggestedFix,
				}},
				CreatedAt: time.Now(),
			}

			if err := gt.reportToArchitect(report, p.AffectedTasks); err != nil {
				return nil, fmt.Errorf("report to architect: %w", err)
			}

			return map[string]any{
				"reported":  true,
				"target":    "architect",
				"test_name": p.TestName,
			}, nil
		}).
		Build()
}

// escalateFailureSkill creates a skill that escalates to both Orchestrator and Architect.
func escalateFailureSkill(gt *GlobalTester) *skills.Skill {
	type params struct {
		TestName      string   `json:"test_name"`
		Confidence    float64  `json:"confidence"`
		IsProductBug  bool     `json:"is_product_bug"`
		RootCause     string   `json:"root_cause"`
		SuggestedFix  string   `json:"suggested_fix"`
		AffectedTasks []string `json:"affected_tasks"`
	}

	return skills.NewSkill("escalate_failure").
		Description("Escalate a critical test failure to BOTH Orchestrator (pause) and Architect (plan fix).").
		Domain("testing").
		Keywords("escalate", "failure", "critical", "orchestrator", "architect").
		Priority(95).
		Usage("Use only for critical global failures that require both orchestration control and plan-level correction.").
		Requirement("Requires a high-confidence systemic diagnosis with a concrete root cause, suggested fix, and affected task scope.").
		Satisfies("Creates the combined orchestrator-plus-architect escalation for severe global failures.").
		Avoid("Do not use when a single-target report is sufficient or when the evidence is still tentative.").
		StringParam("test_name", "Name of the failing test", true).
		FloatParam("confidence", "Confidence in the diagnosis (0-1)", true).
		BoolParam("is_product_bug", "Whether the failure is a product bug", true).
		StringParam("root_cause", "Root cause description", true).
		StringParam("suggested_fix", "Suggested fix approach", true).
		ArrayParam("affected_tasks", "Task IDs affected by the failure", "string", false).
		Handler(func(_ context.Context, input json.RawMessage) (any, error) {
			var p params
			if err := json.Unmarshal(input, &p); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			report := &shared.DiagnosisReport{
				TestName:     p.TestName,
				Confidence:   p.Confidence,
				IsProductBug: p.IsProductBug,
				RootCauses: []shared.RootCause{{
					Description: p.RootCause,
				}},
				SuggestedFix: []shared.SuggestedFix{{
					Description: p.SuggestedFix,
				}},
				CreatedAt: time.Now(),
			}

			if err := gt.escalateFailure(report, p.AffectedTasks); err != nil {
				return nil, fmt.Errorf("escalate failure: %w", err)
			}

			return map[string]any{
				"escalated":      true,
				"targets":        []string{"orchestrator", "architect"},
				"test_name":      p.TestName,
				"affected_tasks": p.AffectedTasks,
			}, nil
		}).
		Build()
}
