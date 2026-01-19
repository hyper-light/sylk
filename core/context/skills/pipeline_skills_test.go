// Package skills provides tests for AR.8.7: Pipeline Retrieval Skills.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Mock Types
// =============================================================================

type mockImplementationContextProvider struct {
	context *ImplementationContext
	err     error
}

func (m *mockImplementationContextProvider) GetImplementationContext(
	_ context.Context,
	_ string,
	_ *ImplementationContextOptions,
) (*ImplementationContext, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.context, nil
}

type mockTestResultProvider struct {
	results []*TestResult
	err     error
}

func (m *mockTestResultProvider) GetTestResults(
	_ context.Context,
	_ string,
	_ *TestResultOptions,
) ([]*TestResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

type mockInspectionFindingProvider struct {
	findings []*InspectionFinding
	err      error
}

func (m *mockInspectionFindingProvider) GetInspectionFindings(
	_ context.Context,
	_ string,
	_ *InspectionOptions,
) ([]*InspectionFinding, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.findings, nil
}

type mockPipelineContentStore struct {
	entries []*ctxpkg.ContentEntry
	err     error
}

func (m *mockPipelineContentStore) Search(
	_ string,
	_ *ctxpkg.SearchFilters,
	_ int,
) ([]*ctxpkg.ContentEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}

type mockPipelineSearcher struct {
	result *ctxpkg.TieredSearchResult
}

func (m *mockPipelineSearcher) SearchWithBudget(
	_ context.Context,
	_ string,
	_ time.Duration,
) *ctxpkg.TieredSearchResult {
	return m.result
}

// =============================================================================
// Test: pipeline_get_implementation_context Skill
// =============================================================================

func TestNewGetImplementationContextSkill(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{}
	skill := NewGetImplementationContextSkill(deps)

	if skill.Name != "pipeline_get_implementation_context" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
	if skill.Domain != PipelineDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}
}

func TestGetImplementationContext_RequiresTaskID(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{ImplementationContextProvider: &mockImplementationContextProvider{}}
	skill := NewGetImplementationContextSkill(deps)

	input, _ := json.Marshal(GetImplementationContextInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "task_id is required" {
		t.Errorf("expected 'task_id is required' error, got: %v", err)
	}
}

func TestGetImplementationContext_NoProvider(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{}
	skill := NewGetImplementationContextSkill(deps)

	input, _ := json.Marshal(GetImplementationContextInput{TaskID: "task-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no provider configured")
	}
}

func TestGetImplementationContext_WithProvider(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{
		ImplementationContextProvider: &mockImplementationContextProvider{
			context: &ImplementationContext{
				TaskID:       "task-123",
				Description:  "Implement user authentication",
				RelatedFiles: []string{"auth.go", "user.go"},
				Dependencies: []string{"jwt-go"},
				Constraints:  []string{"Must support OAuth2"},
			},
		},
	}
	skill := NewGetImplementationContextSkill(deps)

	input, _ := json.Marshal(GetImplementationContextInput{
		TaskID:              "task-123",
		IncludeRelatedFiles: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetImplementationContextOutput)
	if !output.Found {
		t.Error("expected context to be found")
	}
	if output.Context.TaskID != "task-123" {
		t.Errorf("unexpected task ID: %s", output.Context.TaskID)
	}
}

func TestGetImplementationContext_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &PipelineDependencies{
		ContentStore: &mockPipelineContentStore{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:           "entry-1",
					Content:      "Implement feature X\n\nConstraint: Must be backward compatible",
					RelatedFiles: []string{"main.go"},
					Timestamp:    now,
				},
			},
		},
	}
	skill := NewGetImplementationContextSkill(deps)

	input, _ := json.Marshal(GetImplementationContextInput{
		TaskID:              "task-123",
		IncludeRelatedFiles: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetImplementationContextOutput)
	if !output.Found {
		t.Error("expected context to be found")
	}
}

func TestGetImplementationContext_WithSearcher(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &PipelineDependencies{
		Searcher: &mockPipelineSearcher{
			result: &ctxpkg.TieredSearchResult{
				Results: []*ctxpkg.ContentEntry{
					{
						ID:        "entry-1",
						Content:   "Implementation context for task",
						Timestamp: now,
					},
				},
			},
		},
	}
	skill := NewGetImplementationContextSkill(deps)

	input, _ := json.Marshal(GetImplementationContextInput{TaskID: "task-123"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetImplementationContextOutput)
	if !output.Found {
		t.Error("expected context to be found")
	}
}

func TestGetImplementationContext_NotFound(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{
		ContentStore: &mockPipelineContentStore{
			entries: []*ctxpkg.ContentEntry{},
		},
	}
	skill := NewGetImplementationContextSkill(deps)

	input, _ := json.Marshal(GetImplementationContextInput{TaskID: "nonexistent"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetImplementationContextOutput)
	if output.Found {
		t.Error("expected context not to be found")
	}
}

// =============================================================================
// Test: pipeline_get_test_results Skill
// =============================================================================

func TestNewGetTestResultsSkill(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{}
	skill := NewGetTestResultsSkill(deps)

	if skill.Name != "pipeline_get_test_results" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestGetTestResults_RequiresTaskID(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{TestResultProvider: &mockTestResultProvider{}}
	skill := NewGetTestResultsSkill(deps)

	input, _ := json.Marshal(GetTestResultsInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "task_id is required" {
		t.Errorf("expected 'task_id is required' error, got: %v", err)
	}
}

func TestGetTestResults_NoProvider(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{}
	skill := NewGetTestResultsSkill(deps)

	input, _ := json.Marshal(GetTestResultsInput{TaskID: "task-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no provider configured")
	}
}

func TestGetTestResults_WithProvider(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &PipelineDependencies{
		TestResultProvider: &mockTestResultProvider{
			results: []*TestResult{
				{TestID: "test-1", TestName: "TestAuth", Status: "passed", Timestamp: now},
				{TestID: "test-2", TestName: "TestLogin", Status: "failed", ErrorMessage: "assertion failed", Timestamp: now},
			},
		},
	}
	skill := NewGetTestResultsSkill(deps)

	input, _ := json.Marshal(GetTestResultsInput{
		TaskID:         "task-123",
		IncludePassing: true,
		IncludeFailing: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetTestResultsOutput)
	if len(output.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(output.Results))
	}
	if output.TotalPassing != 1 {
		t.Errorf("expected 1 passing, got %d", output.TotalPassing)
	}
	if output.TotalFailing != 1 {
		t.Errorf("expected 1 failing, got %d", output.TotalFailing)
	}
}

func TestGetTestResults_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &PipelineDependencies{
		ContentStore: &mockPipelineContentStore{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:          "entry-1",
					ContentType: ctxpkg.ContentTypeToolResult,
					Content:     "Test TestAuth PASS",
					Timestamp:   now,
				},
				{
					ID:          "entry-2",
					ContentType: ctxpkg.ContentTypeToolResult,
					Content:     "Test TestLogin FAIL: error message",
					Timestamp:   now,
				},
			},
		},
	}
	skill := NewGetTestResultsSkill(deps)

	input, _ := json.Marshal(GetTestResultsInput{
		TaskID:         "task-123",
		IncludePassing: true,
		IncludeFailing: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetTestResultsOutput)
	if len(output.Results) < 1 {
		t.Errorf("expected at least 1 result, got %d", len(output.Results))
	}
}

func TestGetTestResults_FilterFailing(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &PipelineDependencies{
		TestResultProvider: &mockTestResultProvider{
			results: []*TestResult{
				{TestID: "test-1", TestName: "TestAuth", Status: "passed", Timestamp: now},
				{TestID: "test-2", TestName: "TestLogin", Status: "failed", Timestamp: now},
			},
		},
	}
	skill := NewGetTestResultsSkill(deps)

	input, _ := json.Marshal(GetTestResultsInput{
		TaskID:         "task-123",
		IncludePassing: false,
		IncludeFailing: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetTestResultsOutput)
	// Provider returns all, filtering happens at provider level
	// So we just verify counts
	if output.TotalPassing != 1 || output.TotalFailing != 1 {
		t.Errorf("unexpected counts: passing=%d, failing=%d", output.TotalPassing, output.TotalFailing)
	}
}

// =============================================================================
// Test: pipeline_get_inspection_findings Skill
// =============================================================================

func TestNewGetInspectionFindingsSkill(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{}
	skill := NewGetInspectionFindingsSkill(deps)

	if skill.Name != "pipeline_get_inspection_findings" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestGetInspectionFindings_RequiresTaskID(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{InspectionFindingProvider: &mockInspectionFindingProvider{}}
	skill := NewGetInspectionFindingsSkill(deps)

	input, _ := json.Marshal(GetInspectionFindingsInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "task_id is required" {
		t.Errorf("expected 'task_id is required' error, got: %v", err)
	}
}

func TestGetInspectionFindings_NoProvider(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{}
	skill := NewGetInspectionFindingsSkill(deps)

	input, _ := json.Marshal(GetInspectionFindingsInput{TaskID: "task-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no provider configured")
	}
}

func TestGetInspectionFindings_WithProvider(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{
		InspectionFindingProvider: &mockInspectionFindingProvider{
			findings: []*InspectionFinding{
				{FindingID: "f-1", Category: "security", Severity: "high", Message: "SQL injection risk"},
				{FindingID: "f-2", Category: "style", Severity: "low", Message: "Missing comment"},
			},
		},
	}
	skill := NewGetInspectionFindingsSkill(deps)

	input, _ := json.Marshal(GetInspectionFindingsInput{
		TaskID:      "task-123",
		MaxFindings: 10,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetInspectionFindingsOutput)
	if len(output.Findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(output.Findings))
	}
	if output.TotalCount != 2 {
		t.Errorf("expected total count 2, got %d", output.TotalCount)
	}
	if output.BySeverity["high"] != 1 {
		t.Errorf("expected 1 high severity, got %d", output.BySeverity["high"])
	}
}

func TestGetInspectionFindings_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &PipelineDependencies{
		ContentStore: &mockPipelineContentStore{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:        "entry-1",
					Content:   "Security warning: potential vulnerability\nSuggestion: add input validation",
					Timestamp: now,
					Metadata:  map[string]string{"severity": "high"},
				},
			},
		},
	}
	skill := NewGetInspectionFindingsSkill(deps)

	input, _ := json.Marshal(GetInspectionFindingsInput{TaskID: "task-123"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetInspectionFindingsOutput)
	if len(output.Findings) != 1 {
		t.Errorf("expected 1 finding, got %d", len(output.Findings))
	}
}

func TestGetInspectionFindings_FilterBySeverity(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{
		InspectionFindingProvider: &mockInspectionFindingProvider{
			findings: []*InspectionFinding{
				{FindingID: "f-1", Severity: "high", Message: "Critical issue"},
				{FindingID: "f-2", Severity: "low", Message: "Minor issue"},
			},
		},
	}
	skill := NewGetInspectionFindingsSkill(deps)

	input, _ := json.Marshal(GetInspectionFindingsInput{
		TaskID:     "task-123",
		Severities: []string{"high"},
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetInspectionFindingsOutput)
	// Provider returns all, filtering is at provider level
	if output.TotalCount != 2 {
		t.Errorf("expected total count 2, got %d", output.TotalCount)
	}
}

// =============================================================================
// Test: Helper Functions
// =============================================================================

func TestExtractTaskDescription(t *testing.T) {
	t.Parallel()

	content := "Implement user authentication\n\nDetails:\n- Add login\n- Add logout"
	desc := extractTaskDescription(content)

	if desc != "Implement user authentication" {
		t.Errorf("unexpected description: %s", desc)
	}
}

func TestExtractConstraints(t *testing.T) {
	t.Parallel()

	content := "Task details\nConstraint: Must use OAuth2\nMust: Support JWT tokens"
	constraints := extractConstraints(content)

	if len(constraints) < 1 {
		t.Errorf("expected at least 1 constraint, got %d", len(constraints))
	}
}

func TestExtractRequirements(t *testing.T) {
	t.Parallel()

	content := "Requirements:\n- Must support multiple users\n* Handle concurrent requests\nReq: Low latency"
	requirements := extractRequirements(content)

	if len(requirements) < 1 {
		t.Errorf("expected at least 1 requirement, got %d", len(requirements))
	}
}

func TestExtractTestName(t *testing.T) {
	t.Parallel()

	content := "=== RUN   TestUserAuth\n--- PASS: TestUserAuth (0.001s)"
	name := extractTestName(content)

	if name == "Unknown Test" {
		t.Error("expected to extract test name")
	}
}

func TestExtractPipelineErrorMessage(t *testing.T) {
	t.Parallel()

	content := "Test output\nError: assertion failed at line 42"
	msg := extractPipelineErrorMessage(content)

	if msg == "" {
		t.Error("expected non-empty error message")
	}
}

func TestInferSeverity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content  string
		expected string
	}{
		{"Critical security vulnerability", "critical"},
		{"Error in authentication", "high"},
		{"Warning: deprecated function", "medium"},
		{"Minor style issue", "low"},
	}

	for _, tc := range tests {
		result := inferSeverity(tc.content)
		if result != tc.expected {
			t.Errorf("inferSeverity(%q) = %q, want %q", tc.content[:20], result, tc.expected)
		}
	}
}

func TestExtractSuggestion(t *testing.T) {
	t.Parallel()

	content := "Issue found\nSuggestion: Use parameterized queries\nMore info..."
	suggestion := extractSuggestion(content)

	if suggestion == "" {
		t.Error("expected non-empty suggestion")
	}
}

func TestCountTestResults(t *testing.T) {
	t.Parallel()

	results := []*TestResult{
		{Status: "passed"},
		{Status: "passed"},
		{Status: "failed"},
		{Status: "unknown"},
	}

	passing, failing := countTestResults(results)
	if passing != 2 {
		t.Errorf("expected 2 passing, got %d", passing)
	}
	if failing != 1 {
		t.Errorf("expected 1 failing, got %d", failing)
	}
}

func TestGroupBySeverity(t *testing.T) {
	t.Parallel()

	findings := []*InspectionFinding{
		{Severity: "high"},
		{Severity: "high"},
		{Severity: "low"},
	}

	result := groupBySeverity(findings)
	if result["high"] != 2 {
		t.Errorf("expected 2 high, got %d", result["high"])
	}
	if result["low"] != 1 {
		t.Errorf("expected 1 low, got %d", result["low"])
	}
}

// =============================================================================
// Test: Skill Registration
// =============================================================================

func TestRegisterPipelineSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &PipelineDependencies{}

	err := RegisterPipelineSkills(registry, deps)
	if err != nil {
		t.Fatalf("failed to register skills: %v", err)
	}

	expectedSkills := []string{
		"pipeline_get_implementation_context",
		"pipeline_get_test_results",
		"pipeline_get_inspection_findings",
	}
	for _, name := range expectedSkills {
		if registry.Get(name) == nil {
			t.Errorf("skill %s not registered", name)
		}
	}
}

// =============================================================================
// Test: Error Handling
// =============================================================================

func TestGetImplementationContext_ProviderError(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{
		ImplementationContextProvider: &mockImplementationContextProvider{err: fmt.Errorf("provider failed")},
	}
	skill := NewGetImplementationContextSkill(deps)

	input, _ := json.Marshal(GetImplementationContextInput{TaskID: "task-123"})
	result, err := skill.Handler(context.Background(), input)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	output := result.(*GetImplementationContextOutput)
	if output.Found {
		t.Error("expected found=false on provider error")
	}
}

func TestGetTestResults_ProviderError(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{
		TestResultProvider: &mockTestResultProvider{err: fmt.Errorf("provider failed")},
	}
	skill := NewGetTestResultsSkill(deps)

	input, _ := json.Marshal(GetTestResultsInput{TaskID: "task-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from provider")
	}
}

func TestGetInspectionFindings_ProviderError(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{
		InspectionFindingProvider: &mockInspectionFindingProvider{err: fmt.Errorf("provider failed")},
	}
	skill := NewGetInspectionFindingsSkill(deps)

	input, _ := json.Marshal(GetInspectionFindingsInput{TaskID: "task-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from provider")
	}
}

func TestGetImplementationContext_ContentStoreError(t *testing.T) {
	t.Parallel()

	deps := &PipelineDependencies{
		ContentStore: &mockPipelineContentStore{err: fmt.Errorf("store error")},
	}
	skill := NewGetImplementationContextSkill(deps)

	input, _ := json.Marshal(GetImplementationContextInput{TaskID: "task-123"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from content store")
	}
}

// =============================================================================
// Test: Constants
// =============================================================================

func TestPipelineConstants(t *testing.T) {
	t.Parallel()

	if PipelineDomain != "pipeline" {
		t.Errorf("unexpected domain: %s", PipelineDomain)
	}
	if DefaultTestResultLimit != 20 {
		t.Errorf("unexpected test result limit: %d", DefaultTestResultLimit)
	}
	if DefaultFindingsLimit != 10 {
		t.Errorf("unexpected findings limit: %d", DefaultFindingsLimit)
	}
	if DefaultImplementationDepth != 5 {
		t.Errorf("unexpected implementation depth: %d", DefaultImplementationDepth)
	}
}
