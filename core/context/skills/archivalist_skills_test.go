// Package skills provides tests for AR.8.3: Archivalist Retrieval Skills.
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

type mockSessionSearcher struct {
	results []*SessionSearchResult
	err     error
}

func (m *mockSessionSearcher) SearchSessions(
	_ context.Context,
	_ string,
	_ *SessionSearchOptions,
) ([]*SessionSearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

type mockSessionSummarizer struct {
	summary *SessionSummary
	err     error
}

func (m *mockSessionSummarizer) GetSessionSummary(
	_ context.Context,
	_ string,
	_ *SummaryOptions,
) (*SessionSummary, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.summary, nil
}

type mockSolutionFinder struct {
	solutions []*HistoricalSolution
	err       error
}

func (m *mockSolutionFinder) FindSimilarSolutions(
	_ context.Context,
	_ string,
	_ *SolutionSearchOptions,
) ([]*HistoricalSolution, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.solutions, nil
}

type mockContentStoreForArchivalist struct {
	entries []*ctxpkg.ContentEntry
	err     error
}

func (m *mockContentStoreForArchivalist) Search(
	_ string,
	_ *ctxpkg.SearchFilters,
	_ int,
) ([]*ctxpkg.ContentEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}

// =============================================================================
// Test: archivalist_search_sessions Skill
// =============================================================================

func TestNewSearchSessionsSkill(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{}
	skill := NewSearchSessionsSkill(deps)

	if skill.Name != "archivalist_search_sessions" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
	if skill.Domain != ArchivalistDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}
}

func TestSearchSessions_RequiresQuery(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{SessionSearcher: &mockSessionSearcher{}}
	skill := NewSearchSessionsSkill(deps)

	input, _ := json.Marshal(SearchSessionsInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "query is required" {
		t.Errorf("expected 'query is required' error, got: %v", err)
	}
}

func TestSearchSessions_NoSearcher(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{}
	skill := NewSearchSessionsSkill(deps)

	input, _ := json.Marshal(SearchSessionsInput{Query: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no searcher configured")
	}
}

func TestSearchSessions_WithSessionSearcher(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &ArchivalistDependencies{
		SessionSearcher: &mockSessionSearcher{
			results: []*SessionSearchResult{
				{
					SessionID:   "session-1",
					StartTime:   now.Add(-time.Hour),
					EndTime:     now,
					TurnCount:   10,
					Score:       0.95,
					Snippet:     "test snippet",
					AgentTypes:  []string{"librarian"},
					PrimaryGoal: "code review",
				},
			},
		},
	}
	skill := NewSearchSessionsSkill(deps)

	input, _ := json.Marshal(SearchSessionsInput{
		Query:      "code review",
		MaxResults: 5,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SearchSessionsOutput)
	if len(output.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(output.Sessions))
	}
	if output.Sessions[0].SessionID != "session-1" {
		t.Errorf("unexpected session ID: %s", output.Sessions[0].SessionID)
	}
}

func TestSearchSessions_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &ArchivalistDependencies{
		ContentStore: &mockContentStoreForArchivalist{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:        "entry-1",
					SessionID: "session-1",
					AgentType: "librarian",
					Content:   "test query content",
					Timestamp: now,
				},
				{
					ID:        "entry-2",
					SessionID: "session-1",
					AgentType: "librarian",
					Content:   "more content",
					Timestamp: now.Add(time.Minute),
				},
			},
		},
	}
	skill := NewSearchSessionsSkill(deps)

	input, _ := json.Marshal(SearchSessionsInput{Query: "test"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SearchSessionsOutput)
	if len(output.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(output.Sessions))
	}
	if output.Sessions[0].TurnCount != 2 {
		t.Errorf("expected turn count 2, got %d", output.Sessions[0].TurnCount)
	}
}

// =============================================================================
// Test: archivalist_get_session_summary Skill
// =============================================================================

func TestNewGetSessionSummarySkill(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{}
	skill := NewGetSessionSummarySkill(deps)

	if skill.Name != "archivalist_get_session_summary" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestGetSessionSummary_RequiresSessionID(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{SessionSummarizer: &mockSessionSummarizer{}}
	skill := NewGetSessionSummarySkill(deps)

	input, _ := json.Marshal(GetSessionSummaryInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "session_id is required" {
		t.Errorf("expected 'session_id is required' error, got: %v", err)
	}
}

func TestGetSessionSummary_NoSummarizer(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{}
	skill := NewGetSessionSummarySkill(deps)

	input, _ := json.Marshal(GetSessionSummaryInput{SessionID: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no summarizer configured")
	}
}

func TestGetSessionSummary_WithSummarizer(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		SessionSummarizer: &mockSessionSummarizer{
			summary: &SessionSummary{
				SessionID:    "session-1",
				Summary:      "Code review session for feature X",
				Goals:        []string{"Review PR #123"},
				Outcomes:     []string{"Approved with comments"},
				KeyDecisions: []string{"Use pattern Y"},
				Duration:     30 * time.Minute,
				TurnCount:    15,
			},
		},
	}
	skill := NewGetSessionSummarySkill(deps)

	input, _ := json.Marshal(GetSessionSummaryInput{
		SessionID:       "session-1",
		MaxTokens:       500,
		IncludeOutcomes: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetSessionSummaryOutput)
	if !output.Found {
		t.Error("expected session to be found")
	}
	if output.Summary.SessionID != "session-1" {
		t.Errorf("unexpected session ID: %s", output.Summary.SessionID)
	}
}

func TestGetSessionSummary_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &ArchivalistDependencies{
		ContentStore: &mockContentStoreForArchivalist{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:        "entry-1",
					SessionID: "session-1",
					Content:   "Started working on feature X",
					Timestamp: now,
				},
				{
					ID:          "entry-2",
					SessionID:   "session-1",
					Content:     "Error: build failed",
					ContentType: ctxpkg.ContentTypeToolResult,
					Timestamp:   now.Add(10 * time.Minute),
				},
			},
		},
	}
	skill := NewGetSessionSummarySkill(deps)

	input, _ := json.Marshal(GetSessionSummaryInput{
		SessionID:     "session-1",
		IncludeErrors: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetSessionSummaryOutput)
	if !output.Found {
		t.Error("expected session to be found")
	}
	if output.Summary.TurnCount != 2 {
		t.Errorf("expected turn count 2, got %d", output.Summary.TurnCount)
	}
}

func TestGetSessionSummary_NotFound(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		ContentStore: &mockContentStoreForArchivalist{
			entries: []*ctxpkg.ContentEntry{},
		},
	}
	skill := NewGetSessionSummarySkill(deps)

	input, _ := json.Marshal(GetSessionSummaryInput{SessionID: "nonexistent"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetSessionSummaryOutput)
	if output.Found {
		t.Error("expected session not to be found")
	}
}

// =============================================================================
// Test: archivalist_find_similar_solutions Skill
// =============================================================================

func TestNewFindSimilarSolutionsSkill(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{}
	skill := NewFindSimilarSolutionsSkill(deps)

	if skill.Name != "archivalist_find_similar_solutions" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestFindSimilarSolutions_RequiresProblem(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{SolutionFinder: &mockSolutionFinder{}}
	skill := NewFindSimilarSolutionsSkill(deps)

	input, _ := json.Marshal(FindSimilarSolutionsInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "problem is required" {
		t.Errorf("expected 'problem is required' error, got: %v", err)
	}
}

func TestFindSimilarSolutions_NoFinder(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{}
	skill := NewFindSimilarSolutionsSkill(deps)

	input, _ := json.Marshal(FindSimilarSolutionsInput{Problem: "test problem"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no finder configured")
	}
}

func TestFindSimilarSolutions_WithFinder(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		SolutionFinder: &mockSolutionFinder{
			solutions: []*HistoricalSolution{
				{
					SessionID:    "session-1",
					Problem:      "How to implement feature X",
					Solution:     "Use pattern Y with interface Z",
					Outcome:      "Successfully implemented",
					Similarity:   0.92,
					Successful:   true,
					FilesChanged: []string{"main.go", "handler.go"},
				},
			},
		},
	}
	skill := NewFindSimilarSolutionsSkill(deps)

	input, _ := json.Marshal(FindSimilarSolutionsInput{
		Problem:        "How to implement feature X",
		MaxResults:     5,
		SuccessfulOnly: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*FindSimilarSolutionsOutput)
	if len(output.Solutions) != 1 {
		t.Errorf("expected 1 solution, got %d", len(output.Solutions))
	}
	if !output.Solutions[0].Successful {
		t.Error("expected successful solution")
	}
}

func TestFindSimilarSolutions_WithSearcher(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		Searcher: &mockTieredSearcherForArchivalist{
			result: &ctxpkg.TieredSearchResult{
				Results: []*ctxpkg.ContentEntry{
					{
						ID:          "entry-1",
						SessionID:   "session-1",
						Content:     "Problem description\n\nSolution content here",
						ContentType: ctxpkg.ContentTypeAssistantReply,
					},
				},
			},
		},
	}
	skill := NewFindSimilarSolutionsSkill(deps)

	input, _ := json.Marshal(FindSimilarSolutionsInput{
		Problem:    "similar problem",
		MaxResults: 5,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*FindSimilarSolutionsOutput)
	if len(output.Solutions) != 1 {
		t.Errorf("expected 1 solution, got %d", len(output.Solutions))
	}
}

type mockTieredSearcherForArchivalist struct {
	result *ctxpkg.TieredSearchResult
}

func (m *mockTieredSearcherForArchivalist) SearchWithBudget(
	_ context.Context,
	_ string,
	_ time.Duration,
) *ctxpkg.TieredSearchResult {
	return m.result
}

// =============================================================================
// Test: Helper Functions
// =============================================================================

func TestContainsString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		slice    []string
		s        string
		expected bool
	}{
		{[]string{"a", "b", "c"}, "b", true},
		{[]string{"a", "b", "c"}, "d", false},
		{[]string{}, "a", false},
		{nil, "a", false},
	}

	for _, tc := range tests {
		result := containsString(tc.slice, tc.s)
		if result != tc.expected {
			t.Errorf("containsString(%v, %q) = %v, want %v", tc.slice, tc.s, result, tc.expected)
		}
	}
}

func TestGenerateSnippet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content  string
		query    string
		contains string
	}{
		{"short content", "content", "short content"},
		{"query is in the middle of this text", "middle", "middle"},
		{"no match here at all", "xyz", "no match"},
	}

	for _, tc := range tests {
		result := generateSnippet(tc.content, tc.query)
		if len(result) == 0 {
			t.Errorf("generateSnippet returned empty for %q", tc.content)
		}
	}
}

func TestExtractProblemFromContent(t *testing.T) {
	t.Parallel()

	content := "First paragraph is the problem.\n\nSecond paragraph is solution."
	result := extractProblemFromContent(content)

	if result != "First paragraph is the problem." {
		t.Errorf("unexpected problem: %s", result)
	}
}

func TestExtractSolutionFromContent(t *testing.T) {
	t.Parallel()

	content := "Problem description.\n\nSolution goes here."
	result := extractSolutionFromContent(content)

	if result != "Solution goes here." {
		t.Errorf("unexpected solution: %s", result)
	}
}

func TestIsErrorContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		entry    *ctxpkg.ContentEntry
		expected bool
	}{
		{&ctxpkg.ContentEntry{Content: "Error: something went wrong"}, true},
		{&ctxpkg.ContentEntry{Content: "Build failed: missing dependency"}, true},
		{&ctxpkg.ContentEntry{Content: "Success!"}, false},
		{&ctxpkg.ContentEntry{Content: "Completed task"}, false},
	}

	for _, tc := range tests {
		result := isErrorContent(tc.entry)
		if result != tc.expected {
			t.Errorf("isErrorContent(%q) = %v, want %v", tc.entry.Content, result, tc.expected)
		}
	}
}

// =============================================================================
// Test: Skill Registration
// =============================================================================

func TestRegisterArchivalistSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &ArchivalistDependencies{}

	err := RegisterArchivalistSkills(registry, deps)
	if err != nil {
		t.Fatalf("failed to register skills: %v", err)
	}

	expectedSkills := []string{
		"archivalist_search_sessions",
		"archivalist_get_session_summary",
		"archivalist_find_similar_solutions",
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

func TestSearchSessions_SearcherError(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		SessionSearcher: &mockSessionSearcher{err: fmt.Errorf("search failed")},
	}
	skill := NewSearchSessionsSkill(deps)

	input, _ := json.Marshal(SearchSessionsInput{Query: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from searcher")
	}
}

func TestGetSessionSummary_SummarizerError(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		SessionSummarizer: &mockSessionSummarizer{err: fmt.Errorf("summary failed")},
	}
	skill := NewGetSessionSummarySkill(deps)

	input, _ := json.Marshal(GetSessionSummaryInput{SessionID: "test"})
	result, err := skill.Handler(context.Background(), input)

	// Summarizer error returns found=false, not error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	output := result.(*GetSessionSummaryOutput)
	if output.Found {
		t.Error("expected found=false on summarizer error")
	}
}

func TestFindSimilarSolutions_FinderError(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		SolutionFinder: &mockSolutionFinder{err: fmt.Errorf("find failed")},
	}
	skill := NewFindSimilarSolutionsSkill(deps)

	input, _ := json.Marshal(FindSimilarSolutionsInput{Problem: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from finder")
	}
}

func TestSearchSessions_ContentStoreError(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		ContentStore: &mockContentStoreForArchivalist{err: fmt.Errorf("store error")},
	}
	skill := NewSearchSessionsSkill(deps)

	input, _ := json.Marshal(SearchSessionsInput{Query: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from content store")
	}
}

// =============================================================================
// Test: Constants
// =============================================================================

func TestArchivalistConstants(t *testing.T) {
	t.Parallel()

	if ArchivalistDomain != "archivalist" {
		t.Errorf("unexpected domain: %s", ArchivalistDomain)
	}
	if DefaultSessionSearchLimit != 10 {
		t.Errorf("unexpected session search limit: %d", DefaultSessionSearchLimit)
	}
	if DefaultSolutionSearchLimit != 5 {
		t.Errorf("unexpected solution search limit: %d", DefaultSolutionSearchLimit)
	}
	if DefaultSummaryLength != 500 {
		t.Errorf("unexpected summary length: %d", DefaultSummaryLength)
	}
}

// =============================================================================
// Test: Default Values
// =============================================================================

func TestSearchSessions_DefaultMaxResults(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		SessionSearcher: &mockSessionSearcher{results: []*SessionSearchResult{}},
	}
	skill := NewSearchSessionsSkill(deps)

	// No MaxResults specified - should use default
	input, _ := json.Marshal(SearchSessionsInput{Query: "test"})

	_, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindSimilarSolutions_DefaultMinSimilarity(t *testing.T) {
	t.Parallel()

	deps := &ArchivalistDependencies{
		SolutionFinder: &mockSolutionFinder{solutions: []*HistoricalSolution{}},
	}
	skill := NewFindSimilarSolutionsSkill(deps)

	// No MinSimilarity specified - should use default (0.5)
	input, _ := json.Marshal(FindSimilarSolutionsInput{Problem: "test"})

	_, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
