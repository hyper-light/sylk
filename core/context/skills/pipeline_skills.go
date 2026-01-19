// Package skills provides AR.8.7: Pipeline Retrieval Skills.
//
// Pipeline agents (engineer, designer, inspector, tester) handle implementation.
// These skills provide:
// - Implementation context retrieval
// - Test result retrieval
// - Inspection findings retrieval
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// PipelineDomain is the skill domain for pipeline skills.
	PipelineDomain = "pipeline"

	// DefaultTestResultLimit is the default max test results to return.
	DefaultTestResultLimit = 20

	// DefaultFindingsLimit is the default max inspection findings to return.
	DefaultFindingsLimit = 10

	// DefaultImplementationDepth is the default context depth.
	DefaultImplementationDepth = 5
)

// =============================================================================
// Interfaces
// =============================================================================

// ImplementationContextProvider provides implementation context.
type ImplementationContextProvider interface {
	GetImplementationContext(
		ctx context.Context,
		taskID string,
		opts *ImplementationContextOptions,
	) (*ImplementationContext, error)
}

// TestResultProvider provides test results.
type TestResultProvider interface {
	GetTestResults(
		ctx context.Context,
		taskID string,
		opts *TestResultOptions,
	) ([]*TestResult, error)
}

// InspectionFindingProvider provides inspection findings.
type InspectionFindingProvider interface {
	GetInspectionFindings(
		ctx context.Context,
		taskID string,
		opts *InspectionOptions,
	) ([]*InspectionFinding, error)
}

// PipelineContentStore is the interface for content store operations.
type PipelineContentStore interface {
	Search(query string, filters *ctxpkg.SearchFilters, limit int) ([]*ctxpkg.ContentEntry, error)
}

// PipelineSearcher is the interface for tiered search operations.
type PipelineSearcher interface {
	SearchWithBudget(ctx context.Context, query string, budget time.Duration) *ctxpkg.TieredSearchResult
}

// =============================================================================
// Types
// =============================================================================

// ImplementationContextOptions configures context retrieval.
type ImplementationContextOptions struct {
	IncludeRelatedFiles bool
	IncludeDependencies bool
	MaxDepth            int
}

// ImplementationContext provides context for implementation.
type ImplementationContext struct {
	TaskID         string            `json:"task_id"`
	Description    string            `json:"description"`
	RelatedFiles   []string          `json:"related_files,omitempty"`
	Dependencies   []string          `json:"dependencies,omitempty"`
	Constraints    []string          `json:"constraints,omitempty"`
	PriorAttempts  []*PriorAttempt   `json:"prior_attempts,omitempty"`
	RelevantCode   []*CodeSnippet    `json:"relevant_code,omitempty"`
	Requirements   []string          `json:"requirements,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// PriorAttempt represents a previous implementation attempt.
type PriorAttempt struct {
	AttemptID   string    `json:"attempt_id"`
	Timestamp   time.Time `json:"timestamp"`
	Outcome     string    `json:"outcome"`
	Summary     string    `json:"summary"`
	FilesChanged []string `json:"files_changed,omitempty"`
}

// CodeSnippet represents relevant code.
type CodeSnippet struct {
	FilePath   string `json:"file_path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Content    string `json:"content"`
	Language   string `json:"language,omitempty"`
	Relevance  string `json:"relevance"`
}

// TestResultOptions configures test result retrieval.
type TestResultOptions struct {
	MaxResults     int
	IncludePassing bool
	IncludeFailing bool
	TestTypes      []string
}

// TestResult represents a test result.
type TestResult struct {
	TestID       string    `json:"test_id"`
	TestName     string    `json:"test_name"`
	TestType     string    `json:"test_type"`
	Status       string    `json:"status"`
	Duration     time.Duration `json:"duration"`
	ErrorMessage string    `json:"error_message,omitempty"`
	StackTrace   string    `json:"stack_trace,omitempty"`
	FilePath     string    `json:"file_path,omitempty"`
	LineNumber   int       `json:"line_number,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
}

// InspectionOptions configures inspection retrieval.
type InspectionOptions struct {
	MaxFindings    int
	Severities     []string
	Categories     []string
	IncludeResolved bool
}

// InspectionFinding represents an inspection finding.
type InspectionFinding struct {
	FindingID    string   `json:"finding_id"`
	Category     string   `json:"category"`
	Severity     string   `json:"severity"`
	Message      string   `json:"message"`
	FilePath     string   `json:"file_path"`
	LineNumber   int      `json:"line_number"`
	Suggestion   string   `json:"suggestion,omitempty"`
	CodeContext  string   `json:"code_context,omitempty"`
	Resolved     bool     `json:"resolved"`
	Tags         []string `json:"tags,omitempty"`
}

// =============================================================================
// Dependencies
// =============================================================================

// PipelineDependencies holds dependencies for pipeline skills.
type PipelineDependencies struct {
	ImplementationContextProvider ImplementationContextProvider
	TestResultProvider            TestResultProvider
	InspectionFindingProvider     InspectionFindingProvider
	ContentStore                  PipelineContentStore
	Searcher                      PipelineSearcher
}

// =============================================================================
// Input/Output Types
// =============================================================================

// GetImplementationContextInput is input for pipeline_get_implementation_context.
type GetImplementationContextInput struct {
	TaskID              string `json:"task_id"`
	IncludeRelatedFiles bool   `json:"include_related_files,omitempty"`
	IncludeDependencies bool   `json:"include_dependencies,omitempty"`
	MaxDepth            int    `json:"max_depth,omitempty"`
}

// GetImplementationContextOutput is output for pipeline_get_implementation_context.
type GetImplementationContextOutput struct {
	Found   bool                   `json:"found"`
	Context *ImplementationContext `json:"context,omitempty"`
}

// GetTestResultsInput is input for pipeline_get_test_results.
type GetTestResultsInput struct {
	TaskID         string   `json:"task_id"`
	MaxResults     int      `json:"max_results,omitempty"`
	IncludePassing bool     `json:"include_passing,omitempty"`
	IncludeFailing bool     `json:"include_failing,omitempty"`
	TestTypes      []string `json:"test_types,omitempty"`
}

// GetTestResultsOutput is output for pipeline_get_test_results.
type GetTestResultsOutput struct {
	Results      []*TestResult `json:"results"`
	TotalPassing int           `json:"total_passing"`
	TotalFailing int           `json:"total_failing"`
}

// GetInspectionFindingsInput is input for pipeline_get_inspection_findings.
type GetInspectionFindingsInput struct {
	TaskID          string   `json:"task_id"`
	MaxFindings     int      `json:"max_findings,omitempty"`
	Severities      []string `json:"severities,omitempty"`
	Categories      []string `json:"categories,omitempty"`
	IncludeResolved bool     `json:"include_resolved,omitempty"`
}

// GetInspectionFindingsOutput is output for pipeline_get_inspection_findings.
type GetInspectionFindingsOutput struct {
	Findings    []*InspectionFinding `json:"findings"`
	TotalCount  int                  `json:"total_count"`
	BySeverity  map[string]int       `json:"by_severity"`
}

// =============================================================================
// Skill Constructors
// =============================================================================

// NewGetImplementationContextSkill creates the pipeline_get_implementation_context skill.
func NewGetImplementationContextSkill(deps *PipelineDependencies) *skills.Skill {
	return skills.NewSkill("pipeline_get_implementation_context").
		Domain(PipelineDomain).
		Description("Get context for implementing a task").
		Keywords("implementation", "context", "task", "code").
		Handler(createGetImplementationContextHandler(deps)).
		Build()
}

// NewGetTestResultsSkill creates the pipeline_get_test_results skill.
func NewGetTestResultsSkill(deps *PipelineDependencies) *skills.Skill {
	return skills.NewSkill("pipeline_get_test_results").
		Domain(PipelineDomain).
		Description("Get test results for a task").
		Keywords("test", "results", "passing", "failing").
		Handler(createGetTestResultsHandler(deps)).
		Build()
}

// NewGetInspectionFindingsSkill creates the pipeline_get_inspection_findings skill.
func NewGetInspectionFindingsSkill(deps *PipelineDependencies) *skills.Skill {
	return skills.NewSkill("pipeline_get_inspection_findings").
		Domain(PipelineDomain).
		Description("Get inspection findings for a task").
		Keywords("inspection", "findings", "issues", "code review").
		Handler(createGetInspectionFindingsHandler(deps)).
		Build()
}

// =============================================================================
// Handlers
// =============================================================================

func createGetImplementationContextHandler(deps *PipelineDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetImplementationContextInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.TaskID == "" {
			return nil, fmt.Errorf("task_id is required")
		}

		maxDepth := in.MaxDepth
		if maxDepth <= 0 {
			maxDepth = DefaultImplementationDepth
		}

		// Use dedicated provider if available
		if deps.ImplementationContextProvider != nil {
			return getImplementationContextWithProvider(ctx, deps, in, maxDepth)
		}

		// Fall back to content store search
		if deps.ContentStore != nil {
			return buildImplementationContextFromStore(deps, in, maxDepth)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return buildImplementationContextFromSearcher(ctx, deps, in, maxDepth)
		}

		return nil, fmt.Errorf("no implementation context provider or content store configured")
	}
}

func getImplementationContextWithProvider(
	ctx context.Context,
	deps *PipelineDependencies,
	in GetImplementationContextInput,
	maxDepth int,
) (*GetImplementationContextOutput, error) {
	opts := &ImplementationContextOptions{
		IncludeRelatedFiles: in.IncludeRelatedFiles,
		IncludeDependencies: in.IncludeDependencies,
		MaxDepth:            maxDepth,
	}

	implCtx, err := deps.ImplementationContextProvider.GetImplementationContext(ctx, in.TaskID, opts)
	if err != nil {
		return &GetImplementationContextOutput{Found: false}, nil
	}

	return &GetImplementationContextOutput{
		Found:   true,
		Context: implCtx,
	}, nil
}

func buildImplementationContextFromStore(
	deps *PipelineDependencies,
	in GetImplementationContextInput,
	maxDepth int,
) (*GetImplementationContextOutput, error) {
	query := fmt.Sprintf("task:%s implementation", in.TaskID)
	entries, err := deps.ContentStore.Search(query, nil, maxDepth*2)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	if len(entries) == 0 {
		return &GetImplementationContextOutput{Found: false}, nil
	}

	implCtx := buildContextFromEntries(entries, in)
	return &GetImplementationContextOutput{
		Found:   true,
		Context: implCtx,
	}, nil
}

func buildImplementationContextFromSearcher(
	ctx context.Context,
	deps *PipelineDependencies,
	in GetImplementationContextInput,
	maxDepth int,
) (*GetImplementationContextOutput, error) {
	query := fmt.Sprintf("implementation context %s", in.TaskID)
	results := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)

	relevant := filterRelevantResults(results.Results, maxDepth)
	if len(relevant) == 0 {
		return &GetImplementationContextOutput{Found: false}, nil
	}

	implCtx := buildContextFromEntries(relevant, in)
	return &GetImplementationContextOutput{
		Found:   true,
		Context: implCtx,
	}, nil
}

func filterRelevantResults(entries []*ctxpkg.ContentEntry, maxDepth int) []*ctxpkg.ContentEntry {
	if len(entries) <= maxDepth {
		return entries
	}
	return entries[:maxDepth]
}

func buildContextFromEntries(
	entries []*ctxpkg.ContentEntry,
	in GetImplementationContextInput,
) *ImplementationContext {
	implCtx := &ImplementationContext{
		TaskID:        in.TaskID,
		RelatedFiles:  make([]string, 0),
		Dependencies:  make([]string, 0),
		Constraints:   make([]string, 0),
		PriorAttempts: make([]*PriorAttempt, 0),
		RelevantCode:  make([]*CodeSnippet, 0),
		Requirements:  make([]string, 0),
		Metadata:      make(map[string]string),
	}

	for i, entry := range entries {
		// Extract description from first entry
		if i == 0 {
			implCtx.Description = extractTaskDescription(entry.Content)
		}

		// Collect related files
		if in.IncludeRelatedFiles {
			for _, file := range entry.RelatedFiles {
				if !containsStringPipeline(implCtx.RelatedFiles, file) {
					implCtx.RelatedFiles = append(implCtx.RelatedFiles, file)
				}
			}
		}

		// Extract code snippets
		if entry.ContentType == ctxpkg.ContentTypeCodeFile {
			snippet := &CodeSnippet{
				FilePath:  entry.Metadata["file_path"],
				Content:   truncatePipelineContent(entry.Content, 500),
				Language:  entry.Metadata["language"],
				Relevance: "related",
			}
			implCtx.RelevantCode = append(implCtx.RelevantCode, snippet)
		}

		// Extract constraints and requirements
		implCtx.Constraints = append(implCtx.Constraints, extractConstraints(entry.Content)...)
		implCtx.Requirements = append(implCtx.Requirements, extractRequirements(entry.Content)...)
	}

	return implCtx
}

func createGetTestResultsHandler(deps *PipelineDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetTestResultsInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.TaskID == "" {
			return nil, fmt.Errorf("task_id is required")
		}

		maxResults := in.MaxResults
		if maxResults <= 0 {
			maxResults = DefaultTestResultLimit
		}

		// Default to include both passing and failing if not specified
		if !in.IncludePassing && !in.IncludeFailing {
			in.IncludeFailing = true // Default to showing failures
		}

		// Use dedicated provider if available
		if deps.TestResultProvider != nil {
			return getTestResultsWithProvider(ctx, deps, in, maxResults)
		}

		// Fall back to content store search
		if deps.ContentStore != nil {
			return buildTestResultsFromStore(deps, in, maxResults)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return buildTestResultsFromSearcher(ctx, deps, in, maxResults)
		}

		return nil, fmt.Errorf("no test result provider or content store configured")
	}
}

func getTestResultsWithProvider(
	ctx context.Context,
	deps *PipelineDependencies,
	in GetTestResultsInput,
	maxResults int,
) (*GetTestResultsOutput, error) {
	opts := &TestResultOptions{
		MaxResults:     maxResults,
		IncludePassing: in.IncludePassing,
		IncludeFailing: in.IncludeFailing,
		TestTypes:      in.TestTypes,
	}

	results, err := deps.TestResultProvider.GetTestResults(ctx, in.TaskID, opts)
	if err != nil {
		return nil, fmt.Errorf("test result retrieval failed: %w", err)
	}

	passing, failing := countTestResults(results)
	return &GetTestResultsOutput{
		Results:      results,
		TotalPassing: passing,
		TotalFailing: failing,
	}, nil
}

func buildTestResultsFromStore(
	deps *PipelineDependencies,
	in GetTestResultsInput,
	maxResults int,
) (*GetTestResultsOutput, error) {
	query := fmt.Sprintf("task:%s test result", in.TaskID)
	filters := &ctxpkg.SearchFilters{
		ContentTypes: []ctxpkg.ContentType{ctxpkg.ContentTypeToolResult},
	}

	entries, err := deps.ContentStore.Search(query, filters, maxResults*2)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	results := buildTestResultsFromEntries(entries, in, maxResults)
	passing, failing := countTestResults(results)

	return &GetTestResultsOutput{
		Results:      results,
		TotalPassing: passing,
		TotalFailing: failing,
	}, nil
}

func buildTestResultsFromSearcher(
	ctx context.Context,
	deps *PipelineDependencies,
	in GetTestResultsInput,
	maxResults int,
) (*GetTestResultsOutput, error) {
	query := fmt.Sprintf("test result %s", in.TaskID)
	searchResults := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)

	results := buildTestResultsFromEntries(searchResults.Results, in, maxResults)
	passing, failing := countTestResults(results)

	return &GetTestResultsOutput{
		Results:      results,
		TotalPassing: passing,
		TotalFailing: failing,
	}, nil
}

func buildTestResultsFromEntries(
	entries []*ctxpkg.ContentEntry,
	in GetTestResultsInput,
	maxResults int,
) []*TestResult {
	results := make([]*TestResult, 0, maxResults)

	for _, entry := range entries {
		if len(results) >= maxResults {
			break
		}

		result := parseTestResultFromEntry(entry)
		if matchesTestCriteria(result, in) {
			results = append(results, result)
		}
	}

	return results
}

func parseTestResultFromEntry(entry *ctxpkg.ContentEntry) *TestResult {
	// Determine status from content
	status := "unknown"
	lowerContent := strings.ToLower(entry.Content)
	if strings.Contains(lowerContent, "pass") || strings.Contains(lowerContent, "ok") {
		status = "passed"
	} else if strings.Contains(lowerContent, "fail") || strings.Contains(lowerContent, "error") {
		status = "failed"
	}

	return &TestResult{
		TestID:       entry.ID,
		TestName:     extractTestName(entry.Content),
		TestType:     entry.Metadata["test_type"],
		Status:       status,
		ErrorMessage: extractPipelineErrorMessage(entry.Content),
		FilePath:     entry.Metadata["file_path"],
		Timestamp:    entry.Timestamp,
	}
}

func matchesTestCriteria(result *TestResult, in GetTestResultsInput) bool {
	// Filter by status
	if result.Status == "passed" && !in.IncludePassing {
		return false
	}
	if result.Status == "failed" && !in.IncludeFailing {
		return false
	}

	// Filter by test types
	if len(in.TestTypes) > 0 && !containsStringPipeline(in.TestTypes, result.TestType) {
		return false
	}

	return true
}

func countTestResults(results []*TestResult) (passing, failing int) {
	for _, r := range results {
		if r.Status == "passed" {
			passing++
		} else if r.Status == "failed" {
			failing++
		}
	}
	return
}

func createGetInspectionFindingsHandler(deps *PipelineDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetInspectionFindingsInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.TaskID == "" {
			return nil, fmt.Errorf("task_id is required")
		}

		maxFindings := in.MaxFindings
		if maxFindings <= 0 {
			maxFindings = DefaultFindingsLimit
		}

		// Use dedicated provider if available
		if deps.InspectionFindingProvider != nil {
			return getInspectionFindingsWithProvider(ctx, deps, in, maxFindings)
		}

		// Fall back to content store search
		if deps.ContentStore != nil {
			return buildInspectionFindingsFromStore(deps, in, maxFindings)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return buildInspectionFindingsFromSearcher(ctx, deps, in, maxFindings)
		}

		return nil, fmt.Errorf("no inspection finding provider or content store configured")
	}
}

func getInspectionFindingsWithProvider(
	ctx context.Context,
	deps *PipelineDependencies,
	in GetInspectionFindingsInput,
	maxFindings int,
) (*GetInspectionFindingsOutput, error) {
	opts := &InspectionOptions{
		MaxFindings:     maxFindings,
		Severities:      in.Severities,
		Categories:      in.Categories,
		IncludeResolved: in.IncludeResolved,
	}

	findings, err := deps.InspectionFindingProvider.GetInspectionFindings(ctx, in.TaskID, opts)
	if err != nil {
		return nil, fmt.Errorf("inspection finding retrieval failed: %w", err)
	}

	bySeverity := groupBySeverity(findings)
	return &GetInspectionFindingsOutput{
		Findings:   findings,
		TotalCount: len(findings),
		BySeverity: bySeverity,
	}, nil
}

func buildInspectionFindingsFromStore(
	deps *PipelineDependencies,
	in GetInspectionFindingsInput,
	maxFindings int,
) (*GetInspectionFindingsOutput, error) {
	query := fmt.Sprintf("task:%s inspection finding", in.TaskID)
	entries, err := deps.ContentStore.Search(query, nil, maxFindings*2)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	findings := buildFindingsFromEntries(entries, in, maxFindings)
	bySeverity := groupBySeverity(findings)

	return &GetInspectionFindingsOutput{
		Findings:   findings,
		TotalCount: len(findings),
		BySeverity: bySeverity,
	}, nil
}

func buildInspectionFindingsFromSearcher(
	ctx context.Context,
	deps *PipelineDependencies,
	in GetInspectionFindingsInput,
	maxFindings int,
) (*GetInspectionFindingsOutput, error) {
	query := fmt.Sprintf("inspection finding %s", in.TaskID)
	results := deps.Searcher.SearchWithBudget(ctx, query, ctxpkg.TierWarmBudget)

	findings := buildFindingsFromEntries(results.Results, in, maxFindings)
	bySeverity := groupBySeverity(findings)

	return &GetInspectionFindingsOutput{
		Findings:   findings,
		TotalCount: len(findings),
		BySeverity: bySeverity,
	}, nil
}

func buildFindingsFromEntries(
	entries []*ctxpkg.ContentEntry,
	in GetInspectionFindingsInput,
	maxFindings int,
) []*InspectionFinding {
	findings := make([]*InspectionFinding, 0, maxFindings)

	for _, entry := range entries {
		if len(findings) >= maxFindings {
			break
		}

		finding := parseFindingFromEntry(entry)
		if matchesFindingCriteria(finding, in) {
			findings = append(findings, finding)
		}
	}

	return findings
}

func parseFindingFromEntry(entry *ctxpkg.ContentEntry) *InspectionFinding {
	severity := entry.Metadata["severity"]
	if severity == "" {
		severity = inferSeverity(entry.Content)
	}

	return &InspectionFinding{
		FindingID:   entry.ID,
		Category:    entry.Metadata["category"],
		Severity:    severity,
		Message:     extractFindingMessage(entry.Content),
		FilePath:    entry.Metadata["file_path"],
		Suggestion:  extractSuggestion(entry.Content),
		CodeContext: extractCodeContext(entry.Content),
		Resolved:    entry.Metadata["resolved"] == "true",
		Tags:        entry.Keywords,
	}
}

func matchesFindingCriteria(finding *InspectionFinding, in GetInspectionFindingsInput) bool {
	// Filter by resolved status
	if finding.Resolved && !in.IncludeResolved {
		return false
	}

	// Filter by severities
	if len(in.Severities) > 0 && !containsStringPipeline(in.Severities, finding.Severity) {
		return false
	}

	// Filter by categories
	if len(in.Categories) > 0 && !containsStringPipeline(in.Categories, finding.Category) {
		return false
	}

	return true
}

func groupBySeverity(findings []*InspectionFinding) map[string]int {
	result := make(map[string]int)
	for _, f := range findings {
		result[f.Severity]++
	}
	return result
}

// =============================================================================
// Helper Functions
// =============================================================================

func containsStringPipeline(slice []string, s string) bool {
	for _, item := range slice {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

func truncatePipelineContent(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}

	// Find a good break point
	breakPoint := maxLen
	for i := maxLen - 1; i > maxLen-50 && i > 0; i-- {
		if content[i] == '\n' || content[i] == ' ' {
			breakPoint = i
			break
		}
	}

	return content[:breakPoint] + "..."
}

func extractTaskDescription(content string) string {
	lines := strings.SplitN(content, "\n", 2)
	desc := strings.TrimSpace(lines[0])
	if len(desc) > 300 {
		desc = desc[:300] + "..."
	}
	return desc
}

func extractConstraints(content string) []string {
	constraints := make([]string, 0)
	lowerContent := strings.ToLower(content)

	// Look for constraint indicators
	for _, indicator := range []string{"constraint:", "must:", "required:", "limitation:"} {
		if idx := strings.Index(lowerContent, indicator); idx >= 0 {
			start := idx + len(indicator)
			end := start + 100
			if end > len(content) {
				end = len(content)
			}
			// Find end of constraint
			remaining := content[start:end]
			if nlIdx := strings.Index(remaining, "\n"); nlIdx > 0 {
				remaining = remaining[:nlIdx]
			}
			constraint := strings.TrimSpace(remaining)
			if constraint != "" {
				constraints = append(constraints, constraint)
			}
		}
	}

	return constraints
}

func extractRequirements(content string) []string {
	requirements := make([]string, 0)
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Look for requirement patterns
		if strings.HasPrefix(strings.ToLower(trimmed), "req") ||
			strings.HasPrefix(trimmed, "- ") ||
			strings.HasPrefix(trimmed, "* ") {
			req := strings.TrimLeft(trimmed, "-*")
			req = strings.TrimSpace(req)
			if len(req) > 20 && len(req) < 200 {
				requirements = append(requirements, req)
			}
		}
		if len(requirements) >= 5 {
			break
		}
	}

	return requirements
}

func extractTestName(content string) string {
	// Look for test name patterns
	lowerContent := strings.ToLower(content)
	for _, prefix := range []string{"test", "spec", "it "} {
		if idx := strings.Index(lowerContent, prefix); idx >= 0 {
			start := idx
			end := start + 80
			if end > len(content) {
				end = len(content)
			}
			remaining := content[start:end]
			if nlIdx := strings.Index(remaining, "\n"); nlIdx > 0 {
				remaining = remaining[:nlIdx]
			}
			return strings.TrimSpace(remaining)
		}
	}
	return "Unknown Test"
}

func extractPipelineErrorMessage(content string) string {
	lowerContent := strings.ToLower(content)
	for _, indicator := range []string{"error:", "failed:", "exception:"} {
		if idx := strings.Index(lowerContent, indicator); idx >= 0 {
			start := idx
			end := start + 200
			if end > len(content) {
				end = len(content)
			}
			remaining := content[start:end]
			if nlIdx := strings.Index(remaining, "\n"); nlIdx > 0 {
				remaining = remaining[:nlIdx]
			}
			return strings.TrimSpace(remaining)
		}
	}
	return ""
}

func inferSeverity(content string) string {
	lowerContent := strings.ToLower(content)
	if strings.Contains(lowerContent, "critical") || strings.Contains(lowerContent, "blocker") {
		return "critical"
	}
	if strings.Contains(lowerContent, "error") || strings.Contains(lowerContent, "high") {
		return "high"
	}
	if strings.Contains(lowerContent, "warning") || strings.Contains(lowerContent, "medium") {
		return "medium"
	}
	return "low"
}

func extractFindingMessage(content string) string {
	lines := strings.SplitN(content, "\n", 2)
	msg := strings.TrimSpace(lines[0])
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return msg
}

func extractSuggestion(content string) string {
	lowerContent := strings.ToLower(content)
	for _, indicator := range []string{"suggestion:", "fix:", "recommended:", "consider:"} {
		if idx := strings.Index(lowerContent, indicator); idx >= 0 {
			start := idx + len(indicator)
			end := start + 200
			if end > len(content) {
				end = len(content)
			}
			remaining := content[start:end]
			if nlIdx := strings.Index(remaining, "\n\n"); nlIdx > 0 {
				remaining = remaining[:nlIdx]
			}
			return strings.TrimSpace(remaining)
		}
	}
	return ""
}

func extractCodeContext(content string) string {
	// Look for code block or indented code
	if idx := strings.Index(content, "```"); idx >= 0 {
		start := idx + 3
		// Skip language identifier
		if nlIdx := strings.Index(content[start:], "\n"); nlIdx >= 0 {
			start += nlIdx + 1
		}
		if end := strings.Index(content[start:], "```"); end > 0 {
			return strings.TrimSpace(content[start : start+end])
		}
	}
	return ""
}

// =============================================================================
// Registration
// =============================================================================

// RegisterPipelineSkills registers all pipeline skills with the registry.
func RegisterPipelineSkills(
	registry *skills.Registry,
	deps *PipelineDependencies,
) error {
	skillsToRegister := []*skills.Skill{
		NewGetImplementationContextSkill(deps),
		NewGetTestResultsSkill(deps),
		NewGetInspectionFindingsSkill(deps),
	}

	for _, skill := range skillsToRegister {
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register %s: %w", skill.Name, err)
		}
	}

	return nil
}
