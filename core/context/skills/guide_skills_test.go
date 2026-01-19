// Package skills provides tests for AR.8.5: Guide Retrieval Skills.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Mock Types
// =============================================================================

type mockOnboardingProvider struct {
	guide *OnboardingGuide
	err   error
}

func (m *mockOnboardingProvider) GetOnboardingSteps(
	_ context.Context,
	_ string,
	_ *OnboardingOptions,
) (*OnboardingGuide, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.guide, nil
}

type mockExampleFinder struct {
	examples []*UsageExample
	err      error
}

func (m *mockExampleFinder) FindExamples(
	_ context.Context,
	_ string,
	_ *ExampleSearchOptions,
) ([]*UsageExample, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.examples, nil
}

type mockConceptExplainer struct {
	explanation *ConceptExplanation
	err         error
}

func (m *mockConceptExplainer) ExplainConcept(
	_ context.Context,
	_ string,
	_ *ExplanationOptions,
) (*ConceptExplanation, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.explanation, nil
}

type mockGuideContentStore struct {
	entries []*ctxpkg.ContentEntry
	err     error
}

func (m *mockGuideContentStore) Search(
	_ string,
	_ *ctxpkg.SearchFilters,
	_ int,
) ([]*ctxpkg.ContentEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}

type mockGuideSearcher struct {
	result *ctxpkg.TieredSearchResult
}

func (m *mockGuideSearcher) SearchWithBudget(
	_ context.Context,
	_ string,
	_ time.Duration,
) *ctxpkg.TieredSearchResult {
	return m.result
}

// =============================================================================
// Test: guide_get_onboarding Skill
// =============================================================================

func TestNewGetOnboardingSkill(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{}
	skill := NewGetOnboardingSkill(deps)

	if skill.Name != "guide_get_onboarding" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
	if skill.Domain != GuideDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}
}

func TestGetOnboarding_RequiresTopic(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{OnboardingProvider: &mockOnboardingProvider{}}
	skill := NewGetOnboardingSkill(deps)

	input, _ := json.Marshal(GetOnboardingInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "topic is required" {
		t.Errorf("expected 'topic is required' error, got: %v", err)
	}
}

func TestGetOnboarding_NoProvider(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{}
	skill := NewGetOnboardingSkill(deps)

	input, _ := json.Marshal(GetOnboardingInput{Topic: "getting started"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no provider configured")
	}
}

func TestGetOnboarding_WithProvider(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		OnboardingProvider: &mockOnboardingProvider{
			guide: &OnboardingGuide{
				Topic: "getting started",
				Steps: []*OnboardingStep{
					{Number: 1, Title: "Install", Description: "Install the package"},
					{Number: 2, Title: "Configure", Description: "Set up config"},
				},
				Prerequisites: []string{"Go 1.21+"},
				EstimatedTime: "15 minutes",
			},
		},
	}
	skill := NewGetOnboardingSkill(deps)

	input, _ := json.Marshal(GetOnboardingInput{
		Topic:    "getting started",
		MaxSteps: 5,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetOnboardingOutput)
	if !output.Found {
		t.Error("expected guide to be found")
	}
	if len(output.Guide.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(output.Guide.Steps))
	}
}

func TestGetOnboarding_WithContentStore(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ContentStore: &mockGuideContentStore{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:       "entry-1",
					Content:  "Step 1: Install the package\nRun the following command",
					Keywords: []string{"tutorial"},
				},
				{
					ID:       "entry-2",
					Content:  "Step 2: Configure\nEdit config file",
					Keywords: []string{"guide"},
				},
			},
		},
	}
	skill := NewGetOnboardingSkill(deps)

	input, _ := json.Marshal(GetOnboardingInput{Topic: "getting started"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetOnboardingOutput)
	if !output.Found {
		t.Error("expected guide to be found")
	}
}

func TestGetOnboarding_WithSearcher(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		Searcher: &mockGuideSearcher{
			result: &ctxpkg.TieredSearchResult{
				Results: []*ctxpkg.ContentEntry{
					{
						ID:      "entry-1",
						Content: "Getting Started Tutorial\nFollow these steps",
					},
				},
			},
		},
	}
	skill := NewGetOnboardingSkill(deps)

	input, _ := json.Marshal(GetOnboardingInput{Topic: "CLI usage"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetOnboardingOutput)
	if !output.Found {
		t.Error("expected guide to be found")
	}
}

// =============================================================================
// Test: guide_find_examples Skill
// =============================================================================

func TestNewFindExamplesSkill(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{}
	skill := NewFindExamplesSkill(deps)

	if skill.Name != "guide_find_examples" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestFindExamples_RequiresQuery(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{ExampleFinder: &mockExampleFinder{}}
	skill := NewFindExamplesSkill(deps)

	input, _ := json.Marshal(FindExamplesInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "query is required" {
		t.Errorf("expected 'query is required' error, got: %v", err)
	}
}

func TestFindExamples_NoFinder(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{}
	skill := NewFindExamplesSkill(deps)

	input, _ := json.Marshal(FindExamplesInput{Query: "http handler"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no finder configured")
	}
}

func TestFindExamples_WithFinder(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ExampleFinder: &mockExampleFinder{
			examples: []*UsageExample{
				{
					ExampleID:   "ex-1",
					Title:       "HTTP Handler Example",
					Code:        "func handler(w http.ResponseWriter, r *http.Request) {}",
					Language:    "go",
					Description: "Basic HTTP handler",
					Quality:     0.95,
				},
			},
		},
	}
	skill := NewFindExamplesSkill(deps)

	input, _ := json.Marshal(FindExamplesInput{
		Query:      "http handler",
		MaxResults: 5,
		Languages:  []string{"go"},
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*FindExamplesOutput)
	if len(output.Examples) != 1 {
		t.Errorf("expected 1 example, got %d", len(output.Examples))
	}
	if output.Examples[0].Language != "go" {
		t.Errorf("unexpected language: %s", output.Examples[0].Language)
	}
}

func TestFindExamples_WithContentStore(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ContentStore: &mockGuideContentStore{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:          "entry-1",
					ContentType: ctxpkg.ContentTypeCodeFile,
					Content:     "// HTTP handler example\n```go\nfunc handler() {}\n```",
					Keywords:    []string{"http"},
					Metadata:    map[string]string{"language": "go"},
				},
			},
		},
	}
	skill := NewFindExamplesSkill(deps)

	input, _ := json.Marshal(FindExamplesInput{Query: "http handler"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*FindExamplesOutput)
	if len(output.Examples) != 1 {
		t.Errorf("expected 1 example, got %d", len(output.Examples))
	}
}

func TestFindExamples_WithSearcher(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		Searcher: &mockGuideSearcher{
			result: &ctxpkg.TieredSearchResult{
				Results: []*ctxpkg.ContentEntry{
					{
						ID:          "entry-1",
						ContentType: ctxpkg.ContentTypeCodeFile,
						Content:     "// Example code\nfunc main() {}",
						Metadata:    map[string]string{"language": "go"},
					},
				},
			},
		},
	}
	skill := NewFindExamplesSkill(deps)

	input, _ := json.Marshal(FindExamplesInput{Query: "main function"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*FindExamplesOutput)
	if len(output.Examples) != 1 {
		t.Errorf("expected 1 example, got %d", len(output.Examples))
	}
}

// =============================================================================
// Test: guide_explain_concept Skill
// =============================================================================

func TestNewExplainConceptSkill(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{}
	skill := NewExplainConceptSkill(deps)

	if skill.Name != "guide_explain_concept" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestExplainConcept_RequiresConcept(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{ConceptExplainer: &mockConceptExplainer{}}
	skill := NewExplainConceptSkill(deps)

	input, _ := json.Marshal(ExplainConceptInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "concept is required" {
		t.Errorf("expected 'concept is required' error, got: %v", err)
	}
}

func TestExplainConcept_NoExplainer(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{}
	skill := NewExplainConceptSkill(deps)

	input, _ := json.Marshal(ExplainConceptInput{Concept: "dependency injection"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no explainer configured")
	}
}

func TestExplainConcept_WithExplainer(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ConceptExplainer: &mockConceptExplainer{
			explanation: &ConceptExplanation{
				Concept:     "dependency injection",
				Explanation: "DI is a design pattern...",
				KeyPoints:   []string{"Decouples components", "Improves testability"},
				CodeExamples: []string{"func New(dep Dep) *Service {}"},
				RelatedTopics: []string{"inversion of control"},
				TokenCount:  150,
			},
		},
	}
	skill := NewExplainConceptSkill(deps)

	input, _ := json.Marshal(ExplainConceptInput{
		Concept:       "dependency injection",
		IncludeCode:   true,
		RelatedTopics: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*ExplainConceptOutput)
	if !output.Found {
		t.Error("expected explanation to be found")
	}
	if output.Explanation.Concept != "dependency injection" {
		t.Errorf("unexpected concept: %s", output.Explanation.Concept)
	}
}

func TestExplainConcept_WithContentStore(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ContentStore: &mockGuideContentStore{
			entries: []*ctxpkg.ContentEntry{
				{
					ID:       "entry-1",
					Content:  "Dependency Injection\n\n- Decouples components\n- Improves testability\n\n```go\nfunc New(dep Dep) *Service {}\n```",
					Keywords: []string{"di", "design pattern"},
				},
			},
		},
	}
	skill := NewExplainConceptSkill(deps)

	input, _ := json.Marshal(ExplainConceptInput{
		Concept:     "dependency injection",
		IncludeCode: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*ExplainConceptOutput)
	if !output.Found {
		t.Error("expected explanation to be found")
	}
}

func TestExplainConcept_WithSearcher(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		Searcher: &mockGuideSearcher{
			result: &ctxpkg.TieredSearchResult{
				Results: []*ctxpkg.ContentEntry{
					{
						ID:       "entry-1",
						Content:  "Explanation of the concept...\n- Key point 1\n- Key point 2",
						Keywords: []string{"explanation"},
					},
				},
			},
		},
	}
	skill := NewExplainConceptSkill(deps)

	input, _ := json.Marshal(ExplainConceptInput{Concept: "some concept"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*ExplainConceptOutput)
	if !output.Found {
		t.Error("expected explanation to be found")
	}
}

func TestExplainConcept_NotFound(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ContentStore: &mockGuideContentStore{
			entries: []*ctxpkg.ContentEntry{},
		},
	}
	skill := NewExplainConceptSkill(deps)

	input, _ := json.Marshal(ExplainConceptInput{Concept: "nonexistent"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*ExplainConceptOutput)
	if output.Found {
		t.Error("expected explanation not to be found")
	}
}

// =============================================================================
// Test: Helper Functions
// =============================================================================

func TestExtractStepTitle(t *testing.T) {
	t.Parallel()

	content := "Install the Package\nRun the following command..."
	title := extractStepTitle(content, 1)

	if title != "Install the Package" {
		t.Errorf("unexpected title: %s", title)
	}
}

func TestExtractStepDescription(t *testing.T) {
	t.Parallel()

	content := "Title\nThis is the description of the step."
	desc := extractStepDescription(content)

	if desc != "This is the description of the step." {
		t.Errorf("unexpected description: %s", desc)
	}
}

func TestExtractCommands(t *testing.T) {
	t.Parallel()

	content := "Install:\n$ go get package\n$ go build\n> npm install"
	commands := extractCommands(content)

	if len(commands) < 2 {
		t.Errorf("expected at least 2 commands, got %d", len(commands))
	}
}

func TestExtractTips(t *testing.T) {
	t.Parallel()

	content := "Some content\nTip: Use this pattern for better performance\nNote: Check the docs"
	tips := extractTips(content)

	if len(tips) < 1 {
		t.Errorf("expected at least 1 tip, got %d", len(tips))
	}
}

func TestExtractCodeBlock(t *testing.T) {
	t.Parallel()

	content := "Here is an example:\n```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```"
	code := extractCodeBlock(content)

	if code == "" {
		t.Error("expected non-empty code block")
	}
	// Verify code contains expected content
	if !strings.Contains(code, "func main()") && !strings.Contains(code, "fmt.Println") {
		t.Errorf("code block doesn't contain expected content: %s", code)
	}
}

func TestDetectLanguageFromContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content  string
		expected string
	}{
		{"package main\nfunc main() {}", "go"},
		{"import sys\ndef main():", "python"},
		{"function hello() { const x = 1 }", "javascript"},
		{"public class Main {}", "java"},
		{"some random text", "unknown"},
	}

	for _, tc := range tests {
		result := detectLanguageFromContent(tc.content)
		if result != tc.expected {
			t.Errorf("detectLanguageFromContent(%q) = %q, want %q", tc.content[:20], result, tc.expected)
		}
	}
}

func TestExtractKeyPoints(t *testing.T) {
	t.Parallel()

	content := "Overview:\n- First important point here\n- Second point to consider\n* Third bullet point item"
	points := extractKeyPoints(content)

	if len(points) < 2 {
		t.Errorf("expected at least 2 key points, got %d", len(points))
	}
}

// =============================================================================
// Test: Skill Registration
// =============================================================================

func TestRegisterGuideSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &GuideDependencies{}

	err := RegisterGuideSkills(registry, deps)
	if err != nil {
		t.Fatalf("failed to register skills: %v", err)
	}

	expectedSkills := []string{
		"guide_get_onboarding",
		"guide_find_examples",
		"guide_explain_concept",
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

func TestGetOnboarding_ProviderError(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		OnboardingProvider: &mockOnboardingProvider{err: fmt.Errorf("provider failed")},
	}
	skill := NewGetOnboardingSkill(deps)

	input, _ := json.Marshal(GetOnboardingInput{Topic: "test"})
	result, err := skill.Handler(context.Background(), input)

	// Provider error returns found=false, not error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	output := result.(*GetOnboardingOutput)
	if output.Found {
		t.Error("expected found=false on provider error")
	}
}

func TestFindExamples_FinderError(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ExampleFinder: &mockExampleFinder{err: fmt.Errorf("finder failed")},
	}
	skill := NewFindExamplesSkill(deps)

	input, _ := json.Marshal(FindExamplesInput{Query: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from finder")
	}
}

func TestExplainConcept_ExplainerError(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ConceptExplainer: &mockConceptExplainer{err: fmt.Errorf("explainer failed")},
	}
	skill := NewExplainConceptSkill(deps)

	input, _ := json.Marshal(ExplainConceptInput{Concept: "test"})
	result, err := skill.Handler(context.Background(), input)

	// Explainer error returns found=false, not error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	output := result.(*ExplainConceptOutput)
	if output.Found {
		t.Error("expected found=false on explainer error")
	}
}

func TestGetOnboarding_ContentStoreError(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ContentStore: &mockGuideContentStore{err: fmt.Errorf("store error")},
	}
	skill := NewGetOnboardingSkill(deps)

	input, _ := json.Marshal(GetOnboardingInput{Topic: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from content store")
	}
}

// =============================================================================
// Test: Constants
// =============================================================================

func TestGuideConstants(t *testing.T) {
	t.Parallel()

	if GuideDomain != "guide" {
		t.Errorf("unexpected domain: %s", GuideDomain)
	}
	if DefaultExamplesLimit != 5 {
		t.Errorf("unexpected examples limit: %d", DefaultExamplesLimit)
	}
	if DefaultOnboardingSteps != 10 {
		t.Errorf("unexpected onboarding steps: %d", DefaultOnboardingSteps)
	}
	if DefaultExplanationTokens != 800 {
		t.Errorf("unexpected explanation tokens: %d", DefaultExplanationTokens)
	}
}

// =============================================================================
// Test: Default Values
// =============================================================================

func TestGetOnboarding_DefaultMaxSteps(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		OnboardingProvider: &mockOnboardingProvider{guide: &OnboardingGuide{}},
	}
	skill := NewGetOnboardingSkill(deps)

	input, _ := json.Marshal(GetOnboardingInput{Topic: "test"})

	_, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindExamples_DefaultMaxResults(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ExampleFinder: &mockExampleFinder{examples: []*UsageExample{}},
	}
	skill := NewFindExamplesSkill(deps)

	input, _ := json.Marshal(FindExamplesInput{Query: "test"})

	_, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExplainConcept_DefaultMaxTokens(t *testing.T) {
	t.Parallel()

	deps := &GuideDependencies{
		ConceptExplainer: &mockConceptExplainer{explanation: &ConceptExplanation{}},
	}
	skill := NewExplainConceptSkill(deps)

	input, _ := json.Marshal(ExplainConceptInput{Concept: "test"})

	_, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
