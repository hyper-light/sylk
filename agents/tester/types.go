// Package tester provides types and functionality for the Tester agent,
// which handles test quality validation within the Sylk multi-agent system.
package tester

import "time"

// TestCategory defines the category of test being run.
// The Tester operates with a 6-category test system.
type TestCategory string

const (
	// CategoryUnit tests function-level isolation with mocked dependencies.
	CategoryUnit TestCategory = "unit"

	// CategoryIntegration tests component interaction and contract verification.
	CategoryIntegration TestCategory = "integration"

	// CategoryEndToEnd tests full workflow execution from user perspective.
	CategoryEndToEnd TestCategory = "end_to_end"

	// CategoryProperty tests invariant checking with randomized inputs.
	CategoryProperty TestCategory = "property"

	// CategoryMutation tests validate test quality by introducing code changes.
	CategoryMutation TestCategory = "mutation"

	// CategoryFlaky detects unreliable tests through repeated execution.
	CategoryFlaky TestCategory = "flaky"
)

// ValidTestCategories returns all valid test categories.
func ValidTestCategories() []TestCategory {
	return []TestCategory{
		CategoryUnit,
		CategoryIntegration,
		CategoryEndToEnd,
		CategoryProperty,
		CategoryMutation,
		CategoryFlaky,
	}
}

// TestPriority represents the priority level for test execution.
type TestPriority string

const (
	// PriorityCritical tests must always pass - blockers.
	PriorityCritical TestPriority = "critical"

	// PriorityHigh tests are important and should pass.
	PriorityHigh TestPriority = "high"

	// PriorityMedium tests are standard priority.
	PriorityMedium TestPriority = "medium"

	// PriorityLow tests are nice-to-have.
	PriorityLow TestPriority = "low"
)

// ValidTestPriorities returns all valid test priorities.
func ValidTestPriorities() []TestPriority {
	return []TestPriority{PriorityCritical, PriorityHigh, PriorityMedium, PriorityLow}
}

// TestStatus represents the status of a test execution.
type TestStatus string

const (
	// StatusPassed indicates the test passed.
	StatusPassed TestStatus = "passed"

	// StatusFailed indicates the test failed.
	StatusFailed TestStatus = "failed"

	// StatusSkipped indicates the test was skipped.
	StatusSkipped TestStatus = "skipped"

	// StatusFlaky indicates the test has inconsistent results.
	StatusFlaky TestStatus = "flaky"

	// StatusTimeout indicates the test timed out.
	StatusTimeout TestStatus = "timeout"

	// StatusError indicates an error occurred during test setup/teardown.
	StatusError TestStatus = "error"
)

// TestCase represents a single test case with metadata.
type TestCase struct {
	// ID uniquely identifies this test case.
	ID string `json:"id"`

	// Name is the test function name.
	Name string `json:"name"`

	// Package is the package containing the test.
	Package string `json:"package"`

	// File is the file containing the test.
	File string `json:"file"`

	// Line is the line number where the test is defined.
	Line int `json:"line"`

	// Category classifies the test type.
	Category TestCategory `json:"category"`

	// Priority indicates execution priority.
	Priority TestPriority `json:"priority"`

	// Tags for filtering and grouping.
	Tags []string `json:"tags,omitempty"`

	// Dependencies lists tests that must run before this one.
	Dependencies []string `json:"dependencies,omitempty"`

	// Timeout for this specific test.
	Timeout time.Duration `json:"timeout,omitempty"`

	// Complexity score based on test structure.
	Complexity int `json:"complexity"`

	// LastRunAt records when the test was last executed.
	LastRunAt time.Time `json:"last_run_at,omitempty"`

	// LastStatus records the last execution status.
	LastStatus TestStatus `json:"last_status,omitempty"`

	// FlakyScore indicates flakiness (0=stable, 100=always flaky).
	FlakyScore int `json:"flaky_score"`

	// CoveredLines lists line numbers covered by this test.
	CoveredLines []int `json:"covered_lines,omitempty"`

	// BugHistory lists bug IDs found by this test.
	BugHistory []string `json:"bug_history,omitempty"`
}

// TestResult represents the result of running a test.
type TestResult struct {
	// TestID references the test case.
	TestID string `json:"test_id"`

	// Name is the test function name.
	Name string `json:"name"`

	// Package is the package containing the test.
	Package string `json:"package"`

	// Status indicates pass/fail/skip/error.
	Status TestStatus `json:"status"`

	// Duration is how long the test took.
	Duration time.Duration `json:"duration"`

	// Output is the test output.
	Output string `json:"output,omitempty"`

	// ErrorMessage if the test failed.
	ErrorMessage string `json:"error_message,omitempty"`

	// StackTrace if available.
	StackTrace string `json:"stack_trace,omitempty"`

	// Assertions counts.
	AssertionsPassed int `json:"assertions_passed"`
	AssertionsFailed int `json:"assertions_failed"`

	// CoverageContribution percentage from this test.
	CoverageContribution float64 `json:"coverage_contribution"`

	// Timestamp when the test ran.
	Timestamp time.Time `json:"timestamp"`
}

// TestSuite represents a collection of test cases.
type TestSuite struct {
	// ID uniquely identifies this test suite.
	ID string `json:"id"`

	// Name of the test suite.
	Name string `json:"name"`

	// Package is the package containing the suite.
	Package string `json:"package"`

	// Tests in this suite.
	Tests []TestCase `json:"tests"`

	// TotalTests count.
	TotalTests int `json:"total_tests"`

	// Category of tests in this suite.
	Category TestCategory `json:"category"`

	// SetupTime for test fixtures.
	SetupTime time.Duration `json:"setup_time"`

	// TeardownTime for cleanup.
	TeardownTime time.Duration `json:"teardown_time"`

	// Parallelizable indicates if tests can run in parallel.
	Parallelizable bool `json:"parallelizable"`

	// RequiredResources lists external dependencies.
	RequiredResources []string `json:"required_resources,omitempty"`
}

// TestSuiteResult represents the result of running a test suite.
type TestSuiteResult struct {
	// SuiteID references the test suite.
	SuiteID string `json:"suite_id"`

	// Name of the test suite.
	Name string `json:"name"`

	// Package is the package containing the suite.
	Package string `json:"package"`

	// Results for each test.
	Results []TestResult `json:"results"`

	// Summary statistics.
	TotalTests    int `json:"total_tests"`
	Passed        int `json:"passed"`
	Failed        int `json:"failed"`
	Skipped       int `json:"skipped"`
	Errors        int `json:"errors"`
	FlakyDetected int `json:"flaky_detected"`

	// Duration for the entire suite.
	Duration time.Duration `json:"duration"`

	// Coverage percentage for this suite.
	Coverage float64 `json:"coverage"`

	// StartedAt records when execution began.
	StartedAt time.Time `json:"started_at"`

	// CompletedAt records when execution finished.
	CompletedAt time.Time `json:"completed_at"`
}

// CoverageReport represents test coverage data.
type CoverageReport struct {
	// ID uniquely identifies this report.
	ID string `json:"id"`

	// TotalLines in the codebase.
	TotalLines int `json:"total_lines"`

	// CoveredLines by tests.
	CoveredLines int `json:"covered_lines"`

	// CoveragePercent overall.
	CoveragePercent float64 `json:"coverage_percent"`

	// FileCoverage maps file paths to coverage data.
	FileCoverage map[string]*FileCoverage `json:"file_coverage"`

	// PackageCoverage maps packages to coverage percentages.
	PackageCoverage map[string]float64 `json:"package_coverage"`

	// UncoveredLines maps files to uncovered line numbers.
	UncoveredLines map[string][]int `json:"uncovered_lines"`

	// CoverageByCategory breaks down coverage by test category.
	CoverageByCategory map[TestCategory]float64 `json:"coverage_by_category"`

	// ChangedLineCoverage tracks coverage of recently changed lines.
	ChangedLineCoverage float64 `json:"changed_line_coverage"`

	// CriticalPathCoverage for important code paths.
	CriticalPathCoverage float64 `json:"critical_path_coverage"`

	// GeneratedAt timestamp.
	GeneratedAt time.Time `json:"generated_at"`
}

// FileCoverage represents coverage data for a single file.
type FileCoverage struct {
	// Path to the file.
	Path string `json:"path"`

	// TotalLines in the file.
	TotalLines int `json:"total_lines"`

	// CoveredLines in the file.
	CoveredLines int `json:"covered_lines"`

	// CoveragePercent for this file.
	CoveragePercent float64 `json:"coverage_percent"`

	// UncoveredLineNumbers in this file.
	UncoveredLineNumbers []int `json:"uncovered_line_numbers"`

	// Functions maps function names to coverage.
	Functions map[string]float64 `json:"functions"`

	// Branches maps branch IDs to coverage.
	Branches map[string]bool `json:"branches"`
}

// MutationResult represents the result of mutation testing.
type MutationResult struct {
	// ID uniquely identifies this mutation test run.
	ID string `json:"id"`

	// TotalMutants generated.
	TotalMutants int `json:"total_mutants"`

	// KilledMutants caught by tests.
	KilledMutants int `json:"killed_mutants"`

	// SurvivedMutants not caught.
	SurvivedMutants int `json:"survived_mutants"`

	// TimedOutMutants that exceeded timeout.
	TimedOutMutants int `json:"timed_out_mutants"`

	// MutationScore percentage of killed mutants.
	MutationScore float64 `json:"mutation_score"`

	// Mutants details for each mutation.
	Mutants []Mutant `json:"mutants"`

	// WeakTests that didn't catch mutations.
	WeakTests []string `json:"weak_tests"`

	// StrongTests that caught most mutations.
	StrongTests []string `json:"strong_tests"`

	// SuggestedImprovements for tests.
	SuggestedImprovements []TestImprovement `json:"suggested_improvements"`

	// Duration of mutation testing.
	Duration time.Duration `json:"duration"`

	// GeneratedAt timestamp.
	GeneratedAt time.Time `json:"generated_at"`
}

// Mutant represents a single code mutation.
type Mutant struct {
	// ID uniquely identifies this mutant.
	ID string `json:"id"`

	// File containing the mutation.
	File string `json:"file"`

	// Line number of the mutation.
	Line int `json:"line"`

	// Column of the mutation.
	Column int `json:"column"`

	// MutationType describes the mutation operator.
	MutationType string `json:"mutation_type"`

	// OriginalCode before mutation.
	OriginalCode string `json:"original_code"`

	// MutatedCode after mutation.
	MutatedCode string `json:"mutated_code"`

	// Status of this mutant (killed, survived, timeout).
	Status string `json:"status"`

	// KilledBy test that caught this mutant (if killed).
	KilledBy string `json:"killed_by,omitempty"`
}

// TestImprovement suggests how to improve a test.
type TestImprovement struct {
	// TestID to improve.
	TestID string `json:"test_id"`

	// TestName for reference.
	TestName string `json:"test_name"`

	// Reason for improvement suggestion.
	Reason string `json:"reason"`

	// Suggestion for how to improve.
	Suggestion string `json:"suggestion"`

	// MissedMutants that this test should catch.
	MissedMutants []string `json:"missed_mutants,omitempty"`

	// Priority of this improvement.
	Priority TestPriority `json:"priority"`
}

// FlakyTestResult represents results from flaky test detection.
type FlakyTestResult struct {
	// ID uniquely identifies this detection run.
	ID string `json:"id"`

	// TestID of the potentially flaky test.
	TestID string `json:"test_id"`

	// TestName for reference.
	TestName string `json:"test_name"`

	// RunCount number of times the test was run.
	RunCount int `json:"run_count"`

	// PassCount number of passes.
	PassCount int `json:"pass_count"`

	// FailCount number of failures.
	FailCount int `json:"fail_count"`

	// FlakyScore calculated (0-100).
	FlakyScore int `json:"flaky_score"`

	// IsFlaky determination.
	IsFlaky bool `json:"is_flaky"`

	// FailurePatterns observed.
	FailurePatterns []string `json:"failure_patterns,omitempty"`

	// PotentialCauses for flakiness.
	PotentialCauses []string `json:"potential_causes,omitempty"`

	// Recommendations to fix.
	Recommendations []string `json:"recommendations,omitempty"`

	// Results from each run.
	Results []TestResult `json:"results"`

	// DetectedAt timestamp.
	DetectedAt time.Time `json:"detected_at"`
}

// TestPrioritization represents prioritized test ordering.
type TestPrioritization struct {
	// ID uniquely identifies this prioritization.
	ID string `json:"id"`

	// OrderedTests in priority order.
	OrderedTests []PrioritizedTest `json:"ordered_tests"`

	// Rationale for the ordering.
	Rationale string `json:"rationale"`

	// CoverageOptimized indicates if ordering optimizes coverage.
	CoverageOptimized bool `json:"coverage_optimized"`

	// RiskBased indicates if ordering is risk-based.
	RiskBased bool `json:"risk_based"`

	// ChangeBased indicates if ordering considers recent changes.
	ChangeBased bool `json:"change_based"`

	// EstimatedDuration for running in this order.
	EstimatedDuration time.Duration `json:"estimated_duration"`

	// GeneratedAt timestamp.
	GeneratedAt time.Time `json:"generated_at"`
}

// PrioritizedTest represents a test with priority score.
type PrioritizedTest struct {
	// TestID references the test.
	TestID string `json:"test_id"`

	// TestName for reference.
	TestName string `json:"test_name"`

	// Package containing the test.
	Package string `json:"package"`

	// PriorityScore calculated.
	PriorityScore float64 `json:"priority_score"`

	// Factors contributing to priority.
	Factors PriorityFactors `json:"factors"`
}

// PriorityFactors breaks down what contributes to test priority.
type PriorityFactors struct {
	// CoverageScore based on code coverage.
	CoverageScore float64 `json:"coverage_score"`

	// ComplexityScore based on code complexity covered.
	ComplexityScore float64 `json:"complexity_score"`

	// ChangeScore based on recent code changes.
	ChangeScore float64 `json:"change_score"`

	// BugHistoryScore based on bugs found.
	BugHistoryScore float64 `json:"bug_history_score"`

	// FrequencyScore based on how often test finds issues.
	FrequencyScore float64 `json:"frequency_score"`

	// ExecutionTimeScore favoring faster tests.
	ExecutionTimeScore float64 `json:"execution_time_score"`
}

// CoverageGap represents a gap in test coverage.
type CoverageGap struct {
	// File containing the gap.
	File string `json:"file"`

	// StartLine of the uncovered region.
	StartLine int `json:"start_line"`

	// EndLine of the uncovered region.
	EndLine int `json:"end_line"`

	// FunctionName if inside a function.
	FunctionName string `json:"function_name,omitempty"`

	// Complexity of the uncovered code.
	Complexity int `json:"complexity"`

	// RiskLevel of not covering this code.
	RiskLevel string `json:"risk_level"`

	// SuggestedTestType for covering this gap.
	SuggestedTestType TestCategory `json:"suggested_test_type"`

	// SuggestedTestName for the new test.
	SuggestedTestName string `json:"suggested_test_name"`

	// IsInChangedCode indicates if this is in recently changed code.
	IsInChangedCode bool `json:"is_in_changed_code"`
}

// TestSuggestion represents a suggested test case to add.
type TestSuggestion struct {
	// ID uniquely identifies this suggestion.
	ID string `json:"id"`

	// Name suggested for the test.
	Name string `json:"name"`

	// Package where test should be added.
	Package string `json:"package"`

	// File where test should be added.
	File string `json:"file"`

	// Category of the suggested test.
	Category TestCategory `json:"category"`

	// Priority of implementing this test.
	Priority TestPriority `json:"priority"`

	// Description of what the test should verify.
	Description string `json:"description"`

	// TargetFunction to test.
	TargetFunction string `json:"target_function,omitempty"`

	// TargetFile being tested.
	TargetFile string `json:"target_file"`

	// TargetLines to cover.
	TargetLines []int `json:"target_lines,omitempty"`

	// TestTemplate code template for the test.
	TestTemplate string `json:"test_template,omitempty"`

	// Inputs suggested test inputs.
	Inputs []TestInput `json:"inputs,omitempty"`

	// ExpectedBehavior description.
	ExpectedBehavior string `json:"expected_behavior"`

	// Rationale for this suggestion.
	Rationale string `json:"rationale"`
}

// TestInput represents a suggested test input.
type TestInput struct {
	// Name of the input parameter.
	Name string `json:"name"`

	// Type of the input.
	Type string `json:"type"`

	// Value suggested.
	Value any `json:"value"`

	// Description of why this value.
	Description string `json:"description,omitempty"`
}

// TesterConfig configures the Tester agent.
type TesterConfig struct {
	// Model to use for analysis.
	Model string `json:"model"`

	// DefaultTimeout for test execution.
	DefaultTimeout time.Duration `json:"default_timeout"`

	// CoverageThreshold minimum required coverage.
	CoverageThreshold float64 `json:"coverage_threshold"`

	// MutationScoreThreshold minimum required mutation score.
	MutationScoreThreshold float64 `json:"mutation_score_threshold"`

	// FlakyThreshold runs to detect flaky tests.
	FlakyThreshold int `json:"flaky_threshold"`

	// FlakyRunCount number of times to run for flaky detection.
	FlakyRunCount int `json:"flaky_run_count"`

	// ParallelTests maximum parallel test execution.
	ParallelTests int `json:"parallel_tests"`

	// EnableMutationTesting flag.
	EnableMutationTesting bool `json:"enable_mutation_testing"`

	// EnableFlakyDetection flag.
	EnableFlakyDetection bool `json:"enable_flaky_detection"`

	// EnabledCategories list of enabled test categories.
	EnabledCategories []TestCategory `json:"enabled_categories"`

	// ExcludePatterns for files/tests to exclude.
	ExcludePatterns []string `json:"exclude_patterns"`

	// IncludePatterns for files/tests to include.
	IncludePatterns []string `json:"include_patterns"`

	// CheckpointThreshold context usage for checkpointing.
	CheckpointThreshold float64 `json:"checkpoint_threshold"`

	// CompactionThreshold context usage for compaction.
	CompactionThreshold float64 `json:"compaction_threshold"`
}

// DefaultTesterConfig returns the default configuration.
func DefaultTesterConfig() TesterConfig {
	return TesterConfig{
		Model:                  "codex-5.2",
		DefaultTimeout:         30 * time.Second,
		CoverageThreshold:      80.0,
		MutationScoreThreshold: 70.0,
		FlakyThreshold:         20,
		FlakyRunCount:          5,
		ParallelTests:          4,
		EnableMutationTesting:  true,
		EnableFlakyDetection:   true,
		EnabledCategories:      ValidTestCategories(),
		CheckpointThreshold:    0.85,
		CompactionThreshold:    0.95,
	}
}

// TesterState represents the current state of the Tester agent.
type TesterState struct {
	// ID uniquely identifies this tester instance.
	ID string `json:"id"`

	// SessionID of the current session.
	SessionID string `json:"session_id"`

	// CurrentSuiteID being tested.
	CurrentSuiteID string `json:"current_suite_id,omitempty"`

	// TestsRun total count.
	TestsRun int `json:"tests_run"`

	// TestsPassed count.
	TestsPassed int `json:"tests_passed"`

	// TestsFailed count.
	TestsFailed int `json:"tests_failed"`

	// CurrentCoverage percentage.
	CurrentCoverage float64 `json:"current_coverage"`

	// MutationScore current.
	MutationScore float64 `json:"mutation_score"`

	// FlakyTestsFound count.
	FlakyTestsFound int `json:"flaky_tests_found"`

	// SuggestionsGenerated count.
	SuggestionsGenerated int `json:"suggestions_generated"`

	// ContextUsage current percentage.
	ContextUsage float64 `json:"context_usage"`

	// StartedAt records when testing began.
	StartedAt time.Time `json:"started_at"`

	// LastActiveAt records last activity.
	LastActiveAt time.Time `json:"last_active_at"`
}

// TesterIntent represents the type of testing request.
type TesterIntent string

const (
	IntentRunTests       TesterIntent = "run_tests"
	IntentCoverage       TesterIntent = "coverage"
	IntentMutation       TesterIntent = "mutation"
	IntentFlakyDetection TesterIntent = "flaky_detection"
	IntentPrioritize     TesterIntent = "prioritize"
	IntentSuggest        TesterIntent = "suggest"
	IntentAnalyze        TesterIntent = "analyze"
)

// ValidTesterIntents returns all valid tester intents.
func ValidTesterIntents() []TesterIntent {
	return []TesterIntent{
		IntentRunTests,
		IntentCoverage,
		IntentMutation,
		IntentFlakyDetection,
		IntentPrioritize,
		IntentSuggest,
		IntentAnalyze,
	}
}

// TesterRequest represents a request to the Tester agent.
type TesterRequest struct {
	// ID uniquely identifies this request.
	ID string `json:"id"`

	// Intent classifies the request type.
	Intent TesterIntent `json:"intent"`

	// Packages to test.
	Packages []string `json:"packages,omitempty"`

	// Files to test.
	Files []string `json:"files,omitempty"`

	// TestNames specific tests to run.
	TestNames []string `json:"test_names,omitempty"`

	// Categories to include.
	Categories []TestCategory `json:"categories,omitempty"`

	// CoverageThreshold override.
	CoverageThreshold float64 `json:"coverage_threshold,omitempty"`

	// TesterID of the handling tester.
	TesterID string `json:"tester_id"`

	// SessionID for context.
	SessionID string `json:"session_id"`

	// Timestamp of the request.
	Timestamp time.Time `json:"timestamp"`
}

// TesterResponse represents a response from the Tester agent.
type TesterResponse struct {
	// ID uniquely identifies this response.
	ID string `json:"id"`

	// RequestID references the original request.
	RequestID string `json:"request_id"`

	// Success indicates if the request succeeded.
	Success bool `json:"success"`

	// SuiteResult if tests were run.
	SuiteResult *TestSuiteResult `json:"suite_result,omitempty"`

	// CoverageReport if coverage was generated.
	CoverageReport *CoverageReport `json:"coverage_report,omitempty"`

	// MutationResult if mutation testing was run.
	MutationResult *MutationResult `json:"mutation_result,omitempty"`

	// FlakyResults if flaky detection was run.
	FlakyResults []FlakyTestResult `json:"flaky_results,omitempty"`

	// Prioritization if prioritization was requested.
	Prioritization *TestPrioritization `json:"prioritization,omitempty"`

	// Suggestions if test suggestions were requested.
	Suggestions []TestSuggestion `json:"suggestions,omitempty"`

	// CoverageGaps identified.
	CoverageGaps []CoverageGap `json:"coverage_gaps,omitempty"`

	// Error message if failed.
	Error string `json:"error,omitempty"`

	// Timestamp of the response.
	Timestamp time.Time `json:"timestamp"`
}

// TesterCheckpointSummary for context management.
type TesterCheckpointSummary struct {
	// PipelineID for correlation.
	PipelineID string `json:"pipeline_id"`

	// SessionID for context.
	SessionID string `json:"session_id"`

	// Timestamp of checkpoint.
	Timestamp time.Time `json:"timestamp"`

	// ContextUsage at checkpoint.
	ContextUsage float64 `json:"context_usage"`

	// CheckpointIndex number.
	CheckpointIndex int `json:"checkpoint_index"`

	// TotalTestsRun count.
	TotalTestsRun int `json:"total_tests_run"`

	// TotalPassed count.
	TotalPassed int `json:"total_passed"`

	// TotalFailed count.
	TotalFailed int `json:"total_failed"`

	// CoveragePercent at checkpoint.
	CoveragePercent float64 `json:"coverage_percent"`

	// MutationScore at checkpoint.
	MutationScore float64 `json:"mutation_score"`

	// FlakyTestsIdentified list.
	FlakyTestsIdentified []string `json:"flaky_tests_identified"`

	// CriticalGaps in coverage.
	CriticalGaps []CoverageGap `json:"critical_gaps"`

	// PendingSuggestions not yet implemented.
	PendingSuggestions []TestSuggestion `json:"pending_suggestions"`
}
