package inspector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

func (i *Inspector) registerCoreSkills() {
	i.skills.Register(runLinterSkill(i))
	i.skills.Register(runTypeCheckerSkill(i))
	i.skills.Register(runFormatterCheckSkill(i))
	i.skills.Register(runSecurityScanSkill(i))
	i.skills.Register(checkCoverageSkill(i))
	i.skills.Register(analyzeComplexitySkill(i))
	i.skills.Register(validateDocsSkill(i))
	i.skills.Register(defineCriteriaSkill(i))
	i.skills.Register(validateCriteriaSkill(i))
	i.skills.Register(requestOverrideSkill(i))
	i.skills.Register(getValidationStatusSkill(i))
}

type runLinterParams struct {
	Paths []string `json:"paths,omitempty"`
	Fix   bool     `json:"fix,omitempty"`
	Fast  bool     `json:"fast,omitempty"`
}

func runLinterSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("run_linter").
		Description("Run the project's configured linter to check for code issues.").
		Domain("validation").
		Keywords("lint", "check", "golangci", "eslint").
		Priority(100).
		ArrayParam("paths", "Paths to lint (default: entire project)", "string", false).
		BoolParam("fix", "Automatically fix issues where possible", false).
		BoolParam("fast", "Run fast checks only", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params runLinterParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}

			issues := i.runLintCheck(ctx, params.Paths)

			return map[string]any{
				"phase":       "lint_check",
				"passed":      len(issues) == 0,
				"issue_count": len(issues),
				"issues":      issues,
				"fast_mode":   params.Fast,
			}, nil
		}).
		Build()
}

type runTypeCheckerParams struct {
	Paths  []string `json:"paths,omitempty"`
	Strict bool     `json:"strict,omitempty"`
}

func runTypeCheckerSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("run_type_checker").
		Description("Run type checking to detect type errors in the codebase.").
		Domain("validation").
		Keywords("type", "check", "vet", "tsc", "mypy").
		Priority(100).
		ArrayParam("paths", "Paths to check (default: entire project)", "string", false).
		BoolParam("strict", "Enable strict type checking", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params runTypeCheckerParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}

			issues := i.runTypeCheck(ctx, params.Paths)

			hasCritical := false
			for _, issue := range issues {
				if issue.Severity == Critical {
					hasCritical = true
					break
				}
			}

			return map[string]any{
				"phase":        "type_check",
				"passed":       len(issues) == 0,
				"issue_count":  len(issues),
				"issues":       issues,
				"has_critical": hasCritical,
				"strict_mode":  params.Strict,
			}, nil
		}).
		Build()
}

type runFormatterCheckParams struct {
	Paths     []string `json:"paths,omitempty"`
	CheckOnly bool     `json:"check_only,omitempty"`
}

func runFormatterCheckSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("run_formatter_check").
		Description("Check code formatting without modifying files.").
		Domain("validation").
		Keywords("format", "gofmt", "prettier", "black").
		Priority(90).
		ArrayParam("paths", "Paths to check (default: entire project)", "string", false).
		BoolParam("check_only", "Only check, do not modify files (default: true)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params runFormatterCheckParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}

			issues := i.runFormatCheck(ctx, params.Paths)

			return map[string]any{
				"phase":           "format_check",
				"passed":          len(issues) == 0,
				"issue_count":     len(issues),
				"issues":          issues,
				"files_to_format": extractFilesFromIssues(issues),
				"check_only_mode": true,
			}, nil
		}).
		Build()
}

type runSecurityScanParams struct {
	Paths             []string `json:"paths,omitempty"`
	SeverityThreshold string   `json:"severity_threshold,omitempty"`
	IncludeTests      bool     `json:"include_tests,omitempty"`
}

func runSecurityScanSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("run_security_scan").
		Description("Run security vulnerability analysis on the codebase.").
		Domain("validation").
		Keywords("security", "vulnerability", "gosec", "bandit", "audit").
		Priority(100).
		ArrayParam("paths", "Paths to scan (default: entire project)", "string", false).
		EnumParam("severity_threshold", "Minimum severity to report", []string{"low", "medium", "high", "critical"}, false).
		BoolParam("include_tests", "Include test files in scan", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params runSecurityScanParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}
			if params.SeverityThreshold == "" {
				params.SeverityThreshold = "medium"
			}

			issues := i.runSecurityScan(ctx, params.Paths)

			criticalCount := 0
			highCount := 0
			for _, issue := range issues {
				switch issue.Severity {
				case Critical:
					criticalCount++
				case High:
					highCount++
				}
			}

			return map[string]any{
				"phase":          "security_scan",
				"passed":         criticalCount == 0,
				"issue_count":    len(issues),
				"issues":         issues,
				"critical_count": criticalCount,
				"high_count":     highCount,
				"threshold":      params.SeverityThreshold,
			}, nil
		}).
		Build()
}

type checkCoverageParams struct {
	Paths            []string `json:"paths,omitempty"`
	Threshold        float64  `json:"threshold,omitempty"`
	IncludeGenerated bool     `json:"include_generated,omitempty"`
}

func checkCoverageSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("check_coverage").
		Description("Check test coverage against defined thresholds.").
		Domain("validation").
		Keywords("coverage", "test", "threshold").
		Priority(90).
		ArrayParam("paths", "Paths to check coverage for", "string", false).
		IntParam("threshold", "Minimum coverage percentage required (default: 80)", false).
		BoolParam("include_generated", "Include generated files in coverage", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params checkCoverageParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}
			if params.Threshold == 0 {
				params.Threshold = 80.0
			}

			issues := i.runCoverageCheck(ctx, params.Paths)

			return map[string]any{
				"phase":           "test_coverage",
				"passed":          len(issues) == 0,
				"issue_count":     len(issues),
				"issues":          issues,
				"threshold":       params.Threshold,
				"coverage_report": "coverage data would be here",
			}, nil
		}).
		Build()
}

type analyzeComplexityParams struct {
	Paths         []string `json:"paths,omitempty"`
	MaxCyclomatic int      `json:"max_cyclomatic,omitempty"`
	MaxCognitive  int      `json:"max_cognitive,omitempty"`
}

func analyzeComplexitySkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("analyze_complexity").
		Description("Analyze code complexity metrics (cyclomatic, cognitive).").
		Domain("validation").
		Keywords("complexity", "cyclomatic", "cognitive", "metrics").
		Priority(80).
		ArrayParam("paths", "Paths to analyze", "string", false).
		IntParam("max_cyclomatic", "Maximum cyclomatic complexity threshold (default: 10)", false).
		IntParam("max_cognitive", "Maximum cognitive complexity threshold (default: 15)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params analyzeComplexityParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}
			if params.MaxCyclomatic == 0 {
				params.MaxCyclomatic = 10
			}
			if params.MaxCognitive == 0 {
				params.MaxCognitive = 15
			}

			issues := i.runComplexityAnalysis(ctx, params.Paths)

			return map[string]any{
				"phase":                "complexity",
				"passed":               len(issues) == 0,
				"issue_count":          len(issues),
				"issues":               issues,
				"cyclomatic_threshold": params.MaxCyclomatic,
				"cognitive_threshold":  params.MaxCognitive,
			}, nil
		}).
		Build()
}

type validateDocsParams struct {
	Paths             []string `json:"paths,omitempty"`
	RequirePublicDocs bool     `json:"require_public_docs,omitempty"`
	CheckExamples     bool     `json:"check_examples,omitempty"`
}

func validateDocsSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("validate_docs").
		Description("Validate documentation completeness for exported symbols.").
		Domain("validation").
		Keywords("documentation", "docs", "godoc", "jsdoc").
		Priority(70).
		ArrayParam("paths", "Paths to check documentation for", "string", false).
		BoolParam("require_public_docs", "Require documentation for all public symbols", false).
		BoolParam("check_examples", "Check that examples compile and run", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params validateDocsParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if len(params.Paths) == 0 {
				params.Paths = []string{"./..."}
			}

			issues := i.runDocumentationCheck(ctx, params.Paths)

			return map[string]any{
				"phase":               "documentation",
				"passed":              len(issues) == 0,
				"issue_count":         len(issues),
				"issues":              issues,
				"require_public_docs": params.RequirePublicDocs,
			}, nil
		}).
		Build()
}

type defineCriteriaParams struct {
	TaskID          string             `json:"task_id"`
	SuccessCriteria []SuccessCriterion `json:"success_criteria,omitempty"`
	QualityGates    []QualityGate      `json:"quality_gates,omitempty"`
	Constraints     []Constraint       `json:"constraints,omitempty"`
}

func defineCriteriaSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("define_criteria").
		Description("Define success criteria and quality gates for a task (TDD Phase 1).").
		Domain("validation").
		Keywords("criteria", "success", "quality", "gate", "define").
		Priority(95).
		StringParam("task_id", "Unique identifier for the task", true).
		ArrayParam("success_criteria", "List of success criteria", "object", false).
		ArrayParam("quality_gates", "List of quality gates with thresholds", "object", false).
		ArrayParam("constraints", "List of constraints and requirements", "object", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params defineCriteriaParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.TaskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}

			criteria := &InspectorCriteria{
				TaskID:          params.TaskID,
				SuccessCriteria: params.SuccessCriteria,
				QualityGates:    params.QualityGates,
				Constraints:     params.Constraints,
				CreatedAt:       time.Now(),
			}

			i.DefineCriteria(params.TaskID, criteria)

			return map[string]any{
				"task_id":                params.TaskID,
				"criteria_defined":       true,
				"success_criteria_count": len(criteria.SuccessCriteria),
				"quality_gates_count":    len(criteria.QualityGates),
				"constraints_count":      len(criteria.Constraints),
				"created_at":             criteria.CreatedAt,
			}, nil
		}).
		Build()
}

type validateCriteriaParams struct {
	TaskID string   `json:"task_id"`
	Files  []string `json:"files,omitempty"`
}

func validateCriteriaSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("validate_criteria").
		Description("Validate implementation against defined criteria (TDD Phase 4).").
		Domain("validation").
		Keywords("validate", "criteria", "implementation", "phase4").
		Priority(95).
		StringParam("task_id", "Task ID to validate against", true).
		ArrayParam("files", "Files to validate", "string", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params validateCriteriaParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.TaskID == "" {
				return nil, fmt.Errorf("task_id is required")
			}

			result, err := i.ValidateAgainstCriteria(ctx, params.TaskID, params.Files)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"task_id":              params.TaskID,
				"passed":               result.Passed,
				"criteria_met":         result.CriteriaMet,
				"criteria_failed":      result.CriteriaFailed,
				"quality_gate_results": result.QualityGateResults,
				"issue_count":          len(result.Issues),
				"issues":               result.Issues,
				"loop_count":           result.LoopCount,
			}, nil
		}).
		Build()
}

type requestOverrideParams struct {
	IssueID       string `json:"issue_id"`
	Reason        string `json:"reason"`
	Justification string `json:"justification,omitempty"`
}

func requestOverrideSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("request_override").
		Description("Request an override for a validation failure. Requires human approval.").
		Domain("validation").
		Keywords("override", "bypass", "exception", "waiver").
		Priority(60).
		StringParam("issue_id", "ID of the issue to override", true).
		StringParam("reason", "Reason for the override request", true).
		StringParam("justification", "Detailed justification for the override", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params requestOverrideParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			if params.IssueID == "" {
				return nil, fmt.Errorf("issue_id is required")
			}
			if params.Reason == "" {
				return nil, fmt.Errorf("reason is required")
			}

			override := &OverrideRequest{
				IssueID:     params.IssueID,
				Reason:      params.Reason,
				RequestedBy: "agent",
				RequestedAt: time.Now(),
				Approved:    false,
			}

			return map[string]any{
				"override_id":             uuid.New().String(),
				"issue_id":                params.IssueID,
				"status":                  "pending",
				"requires_human_approval": true,
				"override_request":        override,
				"message":                 "Override request created. Requires human approval.",
			}, nil
		}).
		Build()
}

func getValidationStatusSkill(i *Inspector) *skills.Skill {
	return skills.NewSkill("get_validation_status").
		Description("Get the current validation status and recent results.").
		Domain("validation").
		Keywords("status", "result", "state").
		Priority(80).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			state := i.GetState()
			result := i.GetCurrentResult()

			response := map[string]any{
				"state":      state,
				"running":    i.IsRunning(),
				"has_result": result != nil,
			}

			if result != nil {
				response["current_result"] = map[string]any{
					"task_id":      result.TaskID,
					"passed":       result.Passed,
					"issue_count":  len(result.Issues),
					"loop_count":   result.LoopCount,
					"started_at":   result.StartedAt,
					"completed_at": result.CompletedAt,
				}
			}

			return response, nil
		}).
		Build()
}

func extractFilesFromIssues(issues []ValidationIssue) []string {
	fileSet := make(map[string]bool)
	for _, issue := range issues {
		if issue.File != "" {
			fileSet[issue.File] = true
		}
	}

	files := make([]string, 0, len(fileSet))
	for file := range fileSet {
		files = append(files, file)
	}
	return files
}
