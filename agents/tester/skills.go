package tester

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/skills"
	"github.com/google/uuid"
)

func (t *Tester) registerCoreSkills() {
	t.skills.Register(runTestsSkill(t))
	t.skills.Register(coverageReportSkill(t))
	t.skills.Register(mutationTestSkill(t))
	t.skills.Register(detectFlakyTestsSkill(t))
	t.skills.Register(prioritizeTestsSkill(t))
	t.skills.Register(suggestTestCasesSkill(t))
	t.skills.Register(identifyCoverageGapsSkill(t))
	t.skills.Register(getTestStatusSkill(t))
}

type runTestsParams struct {
	Packages   []string       `json:"packages,omitempty"`
	Files      []string       `json:"files,omitempty"`
	TestNames  []string       `json:"test_names,omitempty"`
	Categories []TestCategory `json:"categories,omitempty"`
	Verbose    bool           `json:"verbose,omitempty"`
	Parallel   int            `json:"parallel,omitempty"`
}

func runTestsSkill(t *Tester) *skills.Skill {
	return skills.NewSkill("run_tests").
		Description("Run test suite with configurable categories and filters.").
		Domain("testing").
		Keywords("test", "run", "unit", "integration", "e2e").
		Priority(100).
		ArrayParam("packages", "Packages to test (default: ./...)", "string", false).
		ArrayParam("files", "Specific test files to run", "string", false).
		ArrayParam("test_names", "Specific test names to run", "string", false).
		ArrayParam("categories", "Test categories to run (unit, integration, end_to_end, property)", "string", false).
		BoolParam("verbose", "Enable verbose output", false).
		IntParam("parallel", "Number of parallel test workers", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params runTestsParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			req := &TesterRequest{
				ID:         uuid.New().String(),
				Intent:     IntentRunTests,
				Packages:   params.Packages,
				Files:      params.Files,
				TestNames:  params.TestNames,
				Categories: params.Categories,
				TesterID:   t.state.ID,
				Timestamp:  time.Now(),
			}

			resp, err := t.runTests(ctx, req)
			if err != nil {
				return nil, err
			}

			return map[string]any{
				"success":     resp.Success,
				"total_tests": resp.SuiteResult.TotalTests,
				"passed":      resp.SuiteResult.Passed,
				"failed":      resp.SuiteResult.Failed,
				"skipped":     resp.SuiteResult.Skipped,
				"errors":      resp.SuiteResult.Errors,
				"flaky":       resp.SuiteResult.FlakyDetected,
				"duration":    resp.SuiteResult.Duration.String(),
				"coverage":    resp.SuiteResult.Coverage,
				"results":     resp.SuiteResult.Results,
			}, nil
		}).
		Build()
}

type coverageReportParams struct {
	Packages         []string `json:"packages,omitempty"`
	Threshold        float64  `json:"threshold,omitempty"`
	IncludeGenerated bool     `json:"include_generated,omitempty"`
	ShowUncovered    bool     `json:"show_uncovered,omitempty"`
}

func coverageReportSkill(t *Tester) *skills.Skill {
	return skills.NewSkill("coverage_report").
		Description("Generate test coverage report with detailed file and function coverage.").
		Domain("testing").
		Keywords("coverage", "report", "percent", "uncovered").
		Priority(95).
		ArrayParam("packages", "Packages to analyze coverage for", "string", false).
		IntParam("threshold", "Minimum coverage percentage threshold (default: 80)", false).
		BoolParam("include_generated", "Include generated files in coverage", false).
		BoolParam("show_uncovered", "Show list of uncovered lines", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params coverageReportParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			req := &TesterRequest{
				ID:                uuid.New().String(),
				Intent:            IntentCoverage,
				Packages:          params.Packages,
				CoverageThreshold: params.Threshold,
				TesterID:          t.state.ID,
				Timestamp:         time.Now(),
			}

			resp, err := t.generateCoverageReport(ctx, req)
			if err != nil {
				return nil, err
			}

			report := resp.CoverageReport
			meetsThreshold := report.CoveragePercent >= t.config.CoverageThreshold
			if params.Threshold > 0 {
				meetsThreshold = report.CoveragePercent >= params.Threshold
			}

			result := map[string]any{
				"total_lines":           report.TotalLines,
				"covered_lines":         report.CoveredLines,
				"coverage_percent":      report.CoveragePercent,
				"meets_threshold":       meetsThreshold,
				"threshold":             t.config.CoverageThreshold,
				"package_coverage":      report.PackageCoverage,
				"coverage_by_category":  report.CoverageByCategory,
				"changed_line_coverage": report.ChangedLineCoverage,
			}

			if params.ShowUncovered {
				result["uncovered_lines"] = report.UncoveredLines
			}

			return result, nil
		}).
		Build()
}

type mutationTestParams struct {
	Packages []string `json:"packages,omitempty"`
	Files    []string `json:"files,omitempty"`
	Timeout  int      `json:"timeout,omitempty"`
}

func mutationTestSkill(t *Tester) *skills.Skill {
	return skills.NewSkill("mutation_test").
		Description("Run mutation testing to validate test quality by introducing code changes.").
		Domain("testing").
		Keywords("mutation", "mutant", "quality", "strength").
		Priority(85).
		ArrayParam("packages", "Packages to mutation test", "string", false).
		ArrayParam("files", "Specific files to mutation test", "string", false).
		IntParam("timeout", "Timeout per mutant in seconds", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params mutationTestParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			req := &TesterRequest{
				ID:        uuid.New().String(),
				Intent:    IntentMutation,
				Packages:  params.Packages,
				Files:     params.Files,
				TesterID:  t.state.ID,
				Timestamp: time.Now(),
			}

			resp, err := t.runMutationTests(ctx, req)
			if err != nil {
				return nil, err
			}

			if resp.Error != "" {
				return map[string]any{
					"success": false,
					"error":   resp.Error,
				}, nil
			}

			result := resp.MutationResult
			return map[string]any{
				"success":                true,
				"total_mutants":          result.TotalMutants,
				"killed_mutants":         result.KilledMutants,
				"survived_mutants":       result.SurvivedMutants,
				"timed_out_mutants":      result.TimedOutMutants,
				"mutation_score":         result.MutationScore,
				"meets_threshold":        result.MutationScore >= t.config.MutationScoreThreshold,
				"threshold":              t.config.MutationScoreThreshold,
				"weak_tests":             result.WeakTests,
				"strong_tests":           result.StrongTests,
				"suggested_improvements": result.SuggestedImprovements,
				"duration":               result.Duration.String(),
			}, nil
		}).
		Build()
}

type detectFlakyTestsParams struct {
	Packages []string `json:"packages,omitempty"`
	Files    []string `json:"files,omitempty"`
	RunCount int      `json:"run_count,omitempty"`
}

func detectFlakyTestsSkill(t *Tester) *skills.Skill {
	return skills.NewSkill("detect_flaky_tests").
		Description("Detect flaky tests by running them multiple times and analyzing results.").
		Domain("testing").
		Keywords("flaky", "unreliable", "inconsistent", "intermittent").
		Priority(80).
		ArrayParam("packages", "Packages to check for flaky tests", "string", false).
		ArrayParam("files", "Specific test files to check", "string", false).
		IntParam("run_count", "Number of times to run each test (default: 5)", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params detectFlakyTestsParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			req := &TesterRequest{
				ID:        uuid.New().String(),
				Intent:    IntentFlakyDetection,
				Packages:  params.Packages,
				Files:     params.Files,
				TesterID:  t.state.ID,
				Timestamp: time.Now(),
			}

			resp, err := t.detectFlakyTests(ctx, req)
			if err != nil {
				return nil, err
			}

			if resp.Error != "" {
				return map[string]any{
					"success": false,
					"error":   resp.Error,
				}, nil
			}

			flakyTests := []map[string]any{}
			stableTests := []string{}

			for _, result := range resp.FlakyResults {
				if result.IsFlaky {
					flakyTests = append(flakyTests, map[string]any{
						"test_name":        result.TestName,
						"flaky_score":      result.FlakyScore,
						"pass_count":       result.PassCount,
						"fail_count":       result.FailCount,
						"potential_causes": result.PotentialCauses,
						"recommendations":  result.Recommendations,
					})
				} else {
					stableTests = append(stableTests, result.TestName)
				}
			}

			return map[string]any{
				"success":      true,
				"flaky_count":  len(flakyTests),
				"stable_count": len(stableTests),
				"flaky_tests":  flakyTests,
				"stable_tests": stableTests,
			}, nil
		}).
		Build()
}

type prioritizeTestsParams struct {
	Packages     []string `json:"packages,omitempty"`
	ChangedFiles []string `json:"changed_files,omitempty"`
	OptimizeFor  string   `json:"optimize_for,omitempty"`
	MaxTests     int      `json:"max_tests,omitempty"`
}

func prioritizeTestsSkill(t *Tester) *skills.Skill {
	return skills.NewSkill("prioritize_tests").
		Description("Prioritize tests based on coverage, complexity, changes, and bug history.").
		Domain("testing").
		Keywords("prioritize", "order", "rank", "optimize").
		Priority(75).
		ArrayParam("packages", "Packages to prioritize tests for", "string", false).
		ArrayParam("changed_files", "Recently changed files to prioritize coverage for", "string", false).
		EnumParam("optimize_for", "Optimization strategy", []string{"coverage", "speed", "risk", "changes"}, false).
		IntParam("max_tests", "Maximum number of tests to return", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params prioritizeTestsParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			req := &TesterRequest{
				ID:        uuid.New().String(),
				Intent:    IntentPrioritize,
				Packages:  params.Packages,
				Files:     params.ChangedFiles,
				TesterID:  t.state.ID,
				Timestamp: time.Now(),
			}

			resp, err := t.prioritizeTests(ctx, req)
			if err != nil {
				return nil, err
			}

			prioritization := resp.Prioritization
			orderedTests := make([]map[string]any, 0, len(prioritization.OrderedTests))
			for _, pt := range prioritization.OrderedTests {
				orderedTests = append(orderedTests, map[string]any{
					"test_name":      pt.TestName,
					"package":        pt.Package,
					"priority_score": pt.PriorityScore,
					"factors": map[string]any{
						"coverage":       pt.Factors.CoverageScore,
						"complexity":     pt.Factors.ComplexityScore,
						"change":         pt.Factors.ChangeScore,
						"bug_history":    pt.Factors.BugHistoryScore,
						"frequency":      pt.Factors.FrequencyScore,
						"execution_time": pt.Factors.ExecutionTimeScore,
					},
				})
			}

			if params.MaxTests > 0 && len(orderedTests) > params.MaxTests {
				orderedTests = orderedTests[:params.MaxTests]
			}

			return map[string]any{
				"success":            true,
				"ordered_tests":      orderedTests,
				"rationale":          prioritization.Rationale,
				"estimated_duration": prioritization.EstimatedDuration.String(),
			}, nil
		}).
		Build()
}

type suggestTestCasesParams struct {
	Files          []string `json:"files,omitempty"`
	Functions      []string `json:"functions,omitempty"`
	Categories     []string `json:"categories,omitempty"`
	MaxSuggestions int      `json:"max_suggestions,omitempty"`
}

func suggestTestCasesSkill(t *Tester) *skills.Skill {
	return skills.NewSkill("suggest_test_cases").
		Description("Generate test case suggestions from code analysis.").
		Domain("testing").
		Keywords("suggest", "generate", "create", "new test").
		Priority(85).
		ArrayParam("files", "Files to analyze for test suggestions", "string", false).
		ArrayParam("functions", "Specific functions to suggest tests for", "string", false).
		ArrayParam("categories", "Test categories to suggest (unit, integration, property)", "string", false).
		IntParam("max_suggestions", "Maximum number of suggestions to return", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params suggestTestCasesParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			req := &TesterRequest{
				ID:        uuid.New().String(),
				Intent:    IntentSuggest,
				Files:     params.Files,
				TesterID:  t.state.ID,
				Timestamp: time.Now(),
			}

			resp, err := t.suggestTestCases(ctx, req)
			if err != nil {
				return nil, err
			}

			suggestions := make([]map[string]any, 0, len(resp.Suggestions))
			for _, s := range resp.Suggestions {
				suggestions = append(suggestions, map[string]any{
					"name":              s.Name,
					"file":              s.File,
					"category":          s.Category,
					"priority":          s.Priority,
					"description":       s.Description,
					"target_function":   s.TargetFunction,
					"target_file":       s.TargetFile,
					"expected_behavior": s.ExpectedBehavior,
					"rationale":         s.Rationale,
					"test_template":     s.TestTemplate,
				})
			}

			if params.MaxSuggestions > 0 && len(suggestions) > params.MaxSuggestions {
				suggestions = suggestions[:params.MaxSuggestions]
			}

			return map[string]any{
				"success":          true,
				"suggestion_count": len(suggestions),
				"suggestions":      suggestions,
			}, nil
		}).
		Build()
}

type identifyCoverageGapsParams struct {
	Files         []string `json:"files,omitempty"`
	Packages      []string `json:"packages,omitempty"`
	MinComplexity int      `json:"min_complexity,omitempty"`
	ChangedOnly   bool     `json:"changed_only,omitempty"`
}

func identifyCoverageGapsSkill(t *Tester) *skills.Skill {
	return skills.NewSkill("identify_coverage_gaps").
		Description("Find coverage gaps in code, especially in recently changed lines.").
		Domain("testing").
		Keywords("coverage", "gap", "uncovered", "missing").
		Priority(90).
		ArrayParam("files", "Files to check for coverage gaps", "string", false).
		ArrayParam("packages", "Packages to check for coverage gaps", "string", false).
		IntParam("min_complexity", "Minimum complexity for reported gaps", false).
		BoolParam("changed_only", "Only show gaps in recently changed code", false).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			var params identifyCoverageGapsParams
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid parameters: %w", err)
			}

			req := &TesterRequest{
				ID:        uuid.New().String(),
				Intent:    IntentSuggest,
				Files:     params.Files,
				Packages:  params.Packages,
				TesterID:  t.state.ID,
				Timestamp: time.Now(),
			}

			resp, err := t.suggestTestCases(ctx, req)
			if err != nil {
				return nil, err
			}

			gaps := make([]map[string]any, 0, len(resp.CoverageGaps))
			for _, gap := range resp.CoverageGaps {
				if params.ChangedOnly && !gap.IsInChangedCode {
					continue
				}
				if params.MinComplexity > 0 && gap.Complexity < params.MinComplexity {
					continue
				}

				gaps = append(gaps, map[string]any{
					"file":                gap.File,
					"start_line":          gap.StartLine,
					"end_line":            gap.EndLine,
					"function_name":       gap.FunctionName,
					"complexity":          gap.Complexity,
					"risk_level":          gap.RiskLevel,
					"suggested_test_type": gap.SuggestedTestType,
					"suggested_test_name": gap.SuggestedTestName,
					"is_in_changed_code":  gap.IsInChangedCode,
				})
			}

			criticalGaps := 0
			highRiskGaps := 0
			for _, gap := range resp.CoverageGaps {
				switch gap.RiskLevel {
				case "critical":
					criticalGaps++
				case "high":
					highRiskGaps++
				}
			}

			return map[string]any{
				"success":        true,
				"gap_count":      len(gaps),
				"critical_gaps":  criticalGaps,
				"high_risk_gaps": highRiskGaps,
				"gaps":           gaps,
			}, nil
		}).
		Build()
}

func getTestStatusSkill(t *Tester) *skills.Skill {
	return skills.NewSkill("get_test_status").
		Description("Get current testing status including coverage and recent results.").
		Domain("testing").
		Keywords("status", "state", "summary").
		Priority(80).
		Handler(func(ctx context.Context, input json.RawMessage) (any, error) {
			state := t.GetState()
			result := t.GetCurrentResult()

			response := map[string]any{
				"running":               t.IsRunning(),
				"tests_run":             state.TestsRun,
				"tests_passed":          state.TestsPassed,
				"tests_failed":          state.TestsFailed,
				"current_coverage":      state.CurrentCoverage,
				"mutation_score":        state.MutationScore,
				"flaky_tests_found":     state.FlakyTestsFound,
				"suggestions_generated": state.SuggestionsGenerated,
				"has_result":            result != nil,
			}

			if result != nil {
				response["last_run"] = map[string]any{
					"suite_id":   result.SuiteID,
					"total":      result.TotalTests,
					"passed":     result.Passed,
					"failed":     result.Failed,
					"duration":   result.Duration.String(),
					"coverage":   result.Coverage,
					"started_at": result.StartedAt,
				}
			}

			return response, nil
		}).
		Build()
}
