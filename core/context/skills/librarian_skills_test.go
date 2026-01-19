// Package skills provides tests for AR.8.2: Librarian Retrieval Skills.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Mock Types
// =============================================================================

type mockCodeSearcher struct {
	results []*CodeResult
	err     error
}

func (m *mockCodeSearcher) SearchCode(_ context.Context, _ string, _ *CodeSearchOptions) ([]*CodeResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

type mockSymbolResolver struct {
	context *SymbolContext
	err     error
}

func (m *mockSymbolResolver) GetSymbolContext(_ context.Context, _ string, _ *SymbolContextOptions) (*SymbolContext, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.context, nil
}

type mockPatternMatcher struct {
	matches []*PatternMatch
	err     error
}

func (m *mockPatternMatcher) FindPatterns(_ context.Context, _ string, _ *PatternOptions) ([]*PatternMatch, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.matches, nil
}

// =============================================================================
// Test: librarian_search_codebase Skill
// =============================================================================

func TestNewSearchCodebaseSkill(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{}
	skill := NewSearchCodebaseSkill(deps)

	if skill.Name != "librarian_search_codebase" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
	if skill.Domain != LibrarianDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}
}

func TestSearchCodebase_RequiresQuery(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{CodeSearcher: &mockCodeSearcher{}}
	skill := NewSearchCodebaseSkill(deps)

	input, _ := json.Marshal(SearchCodebaseInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "query is required" {
		t.Errorf("expected 'query is required' error, got: %v", err)
	}
}

func TestSearchCodebase_NoSearcher(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{}
	skill := NewSearchCodebaseSkill(deps)

	input, _ := json.Marshal(SearchCodebaseInput{Query: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when searcher not configured")
	}
}

func TestSearchCodebase_Success(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{
		CodeSearcher: &mockCodeSearcher{
			results: []*CodeResult{
				{FilePath: "main.go", Content: "func main()", Score: 0.9},
				{FilePath: "util.go", Content: "func helper()", Score: 0.8},
			},
		},
	}
	skill := NewSearchCodebaseSkill(deps)

	input, _ := json.Marshal(SearchCodebaseInput{
		Query:      "main function",
		Languages:  []string{"go"},
		MaxResults: 10,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SearchCodebaseOutput)
	if len(output.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(output.Results))
	}
}

// =============================================================================
// Test: librarian_get_symbol_context Skill
// =============================================================================

func TestNewGetSymbolContextSkill(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{}
	skill := NewGetSymbolContextSkill(deps)

	if skill.Name != "librarian_get_symbol_context" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestGetSymbolContext_RequiresSymbol(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{SymbolResolver: &mockSymbolResolver{}}
	skill := NewGetSymbolContextSkill(deps)

	input, _ := json.Marshal(GetSymbolContextInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "symbol is required" {
		t.Errorf("expected 'symbol is required' error, got: %v", err)
	}
}

func TestGetSymbolContext_NoResolver(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{}
	skill := NewGetSymbolContextSkill(deps)

	input, _ := json.Marshal(GetSymbolContextInput{Symbol: "TestFunc"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when resolver not configured")
	}
}

func TestGetSymbolContext_Success(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{
		SymbolResolver: &mockSymbolResolver{
			context: &SymbolContext{
				Symbol: "TestFunc",
				Definition: &CodeLocation{
					FilePath:  "test.go",
					LineStart: 10,
					LineEnd:   20,
					Content:   "func TestFunc() {}",
				},
				Usages: []*CodeLocation{
					{FilePath: "main.go", LineStart: 5, Content: "TestFunc()"},
				},
			},
		},
	}
	skill := NewGetSymbolContextSkill(deps)

	input, _ := json.Marshal(GetSymbolContextInput{
		Symbol:       "TestFunc",
		ContextLines: 30,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetSymbolContextOutput)
	if !output.Found {
		t.Error("expected symbol to be found")
	}
	if output.Context.Symbol != "TestFunc" {
		t.Errorf("unexpected symbol: %s", output.Context.Symbol)
	}
}

// =============================================================================
// Test: librarian_get_pattern_examples Skill
// =============================================================================

func TestNewGetPatternExamplesSkill(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{}
	skill := NewGetPatternExamplesSkill(deps)

	if skill.Name != "librarian_get_pattern_examples" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestGetPatternExamples_RequiresPattern(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{PatternMatcher: &mockPatternMatcher{}}
	skill := NewGetPatternExamplesSkill(deps)

	input, _ := json.Marshal(GetPatternExamplesInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "pattern is required" {
		t.Errorf("expected 'pattern is required' error, got: %v", err)
	}
}

func TestGetPatternExamples_NoMatcher(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{}
	skill := NewGetPatternExamplesSkill(deps)

	input, _ := json.Marshal(GetPatternExamplesInput{Pattern: "for i := range"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when matcher not configured")
	}
}

func TestGetPatternExamples_Success(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{
		PatternMatcher: &mockPatternMatcher{
			matches: []*PatternMatch{
				{FilePath: "loop1.go", Content: "for i := range items", Similarity: 0.95},
				{FilePath: "loop2.go", Content: "for i := range data", Similarity: 0.90},
			},
		},
	}
	skill := NewGetPatternExamplesSkill(deps)

	input, _ := json.Marshal(GetPatternExamplesInput{
		Pattern:  "for i := range",
		Language: "go",
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetPatternExamplesOutput)
	if len(output.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(output.Matches))
	}
}

// =============================================================================
// Test: Helper Functions
// =============================================================================

func TestGeneratePatternSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern  string
		expected string
	}{
		{"short pattern", "short pattern"},
		{"line1\nline2\nline3", "line1 (+ 2 more lines)"},
	}

	for _, tc := range tests {
		result := generatePatternSummary(tc.pattern)
		if result != tc.expected {
			t.Errorf("generatePatternSummary(%q) = %q, want %q", tc.pattern, result, tc.expected)
		}
	}
}

// =============================================================================
// Test: Skill Registration
// =============================================================================

func TestRegisterLibrarianSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &LibrarianDependencies{}

	err := RegisterLibrarianSkills(registry, deps)
	if err != nil {
		t.Fatalf("failed to register skills: %v", err)
	}

	expectedSkills := []string{
		"librarian_search_codebase",
		"librarian_get_symbol_context",
		"librarian_get_pattern_examples",
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

func TestSearchCodebase_SearcherError(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{
		CodeSearcher: &mockCodeSearcher{err: fmt.Errorf("search failed")},
	}
	skill := NewSearchCodebaseSkill(deps)

	input, _ := json.Marshal(SearchCodebaseInput{Query: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from searcher")
	}
}

func TestGetSymbolContext_ResolverError(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{
		SymbolResolver: &mockSymbolResolver{err: fmt.Errorf("resolve failed")},
	}
	skill := NewGetSymbolContextSkill(deps)

	input, _ := json.Marshal(GetSymbolContextInput{Symbol: "Test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from resolver")
	}
}

func TestGetPatternExamples_MatcherError(t *testing.T) {
	t.Parallel()

	deps := &LibrarianDependencies{
		PatternMatcher: &mockPatternMatcher{err: fmt.Errorf("match failed")},
	}
	skill := NewGetPatternExamplesSkill(deps)

	input, _ := json.Marshal(GetPatternExamplesInput{Pattern: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from matcher")
	}
}

// =============================================================================
// Test: Constants
// =============================================================================

func TestLibrarianConstants(t *testing.T) {
	t.Parallel()

	if LibrarianDomain != "librarian" {
		t.Errorf("unexpected domain: %s", LibrarianDomain)
	}
	if DefaultCodeSearchLimit != 20 {
		t.Errorf("unexpected search limit: %d", DefaultCodeSearchLimit)
	}
	if DefaultSymbolContextSize != 50 {
		t.Errorf("unexpected context size: %d", DefaultSymbolContextSize)
	}
}
