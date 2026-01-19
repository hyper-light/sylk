// Package skills provides tests for AR.8.4: Academic Retrieval Skills.
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

type mockResearchSearcher struct {
	results []*ResearchResult
	err     error
}

func (m *mockResearchSearcher) SearchResearch(
	_ context.Context,
	_ string,
	_ *ResearchSearchOptions,
) ([]*ResearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

type mockCitationResolver struct {
	citations []*Citation
	err       error
}

func (m *mockCitationResolver) ResolveCitations(
	_ context.Context,
	_ []string,
	_ string,
) ([]*Citation, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.citations, nil
}

type mockPaperSummarizer struct {
	summary *PaperSummary
	err     error
}

func (m *mockPaperSummarizer) SummarizePaper(
	_ context.Context,
	_ string,
	_ *SummaryRequestOptions,
) (*PaperSummary, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.summary, nil
}

type mockAcademicContentStore struct {
	entries map[string]*ctxpkg.ContentEntry
	search  []*ctxpkg.ContentEntry
	err     error
}

func (m *mockAcademicContentStore) Search(
	_ string,
	_ *ctxpkg.SearchFilters,
	_ int,
) ([]*ctxpkg.ContentEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.search, nil
}

func (m *mockAcademicContentStore) Get(
	_ context.Context,
	id string,
) (*ctxpkg.ContentEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	if entry, ok := m.entries[id]; ok {
		return entry, nil
	}
	return nil, nil
}

type mockAcademicSearcher struct {
	result *ctxpkg.TieredSearchResult
}

func (m *mockAcademicSearcher) SearchWithBudget(
	_ context.Context,
	_ string,
	_ time.Duration,
) *ctxpkg.TieredSearchResult {
	return m.result
}

// =============================================================================
// Test: academic_search_research Skill
// =============================================================================

func TestNewSearchResearchSkill(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{}
	skill := NewSearchResearchSkill(deps)

	if skill.Name != "academic_search_research" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
	if skill.Domain != AcademicDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}
}

func TestSearchResearch_RequiresQuery(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{ResearchSearcher: &mockResearchSearcher{}}
	skill := NewSearchResearchSkill(deps)

	input, _ := json.Marshal(SearchResearchInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "query is required" {
		t.Errorf("expected 'query is required' error, got: %v", err)
	}
}

func TestSearchResearch_NoSearcher(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{}
	skill := NewSearchResearchSkill(deps)

	input, _ := json.Marshal(SearchResearchInput{Query: "machine learning"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no searcher configured")
	}
}

func TestSearchResearch_WithResearchSearcher(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &AcademicDependencies{
		ResearchSearcher: &mockResearchSearcher{
			results: []*ResearchResult{
				{
					PaperID:     "paper-1",
					Title:       "Deep Learning Approaches",
					Authors:     []string{"Smith, J.", "Doe, A."},
					Abstract:    "This paper explores...",
					Source:      "Journal of AI",
					PublishDate: now,
					Topics:      []string{"machine learning", "neural networks"},
					Relevance:   0.95,
				},
			},
		},
	}
	skill := NewSearchResearchSkill(deps)

	input, _ := json.Marshal(SearchResearchInput{
		Query:      "deep learning",
		MaxResults: 5,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SearchResearchOutput)
	if len(output.Papers) != 1 {
		t.Errorf("expected 1 paper, got %d", len(output.Papers))
	}
	if output.Papers[0].Title != "Deep Learning Approaches" {
		t.Errorf("unexpected title: %s", output.Papers[0].Title)
	}
}

func TestSearchResearch_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &AcademicDependencies{
		ContentStore: &mockAcademicContentStore{
			search: []*ctxpkg.ContentEntry{
				{
					ID:          "paper-1",
					ContentType: ctxpkg.ContentTypeResearchPaper,
					Content:     "Abstract\n\nThis paper discusses neural networks...",
					Timestamp:   now,
					Keywords:    []string{"neural networks"},
					Metadata:    map[string]string{"title": "Neural Network Study"},
				},
			},
		},
	}
	skill := NewSearchResearchSkill(deps)

	input, _ := json.Marshal(SearchResearchInput{Query: "neural networks"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SearchResearchOutput)
	if len(output.Papers) != 1 {
		t.Errorf("expected 1 paper, got %d", len(output.Papers))
	}
}

func TestSearchResearch_WithSearcher(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &AcademicDependencies{
		Searcher: &mockAcademicSearcher{
			result: &ctxpkg.TieredSearchResult{
				Results: []*ctxpkg.ContentEntry{
					{
						ID:          "paper-1",
						ContentType: ctxpkg.ContentTypeResearchPaper,
						Content:     "Title\n\nAbstract content here",
						Timestamp:   now,
						Keywords:    []string{"AI"},
						Metadata:    map[string]string{"title": "AI Research"},
					},
				},
			},
		},
	}
	skill := NewSearchResearchSkill(deps)

	input, _ := json.Marshal(SearchResearchInput{Query: "AI"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SearchResearchOutput)
	if len(output.Papers) != 1 {
		t.Errorf("expected 1 paper, got %d", len(output.Papers))
	}
}

// =============================================================================
// Test: academic_get_citations Skill
// =============================================================================

func TestNewGetCitationsSkill(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{}
	skill := NewGetCitationsSkill(deps)

	if skill.Name != "academic_get_citations" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestGetCitations_RequiresPaperIDs(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{CitationResolver: &mockCitationResolver{}}
	skill := NewGetCitationsSkill(deps)

	input, _ := json.Marshal(GetCitationsInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "paper_ids is required" {
		t.Errorf("expected 'paper_ids is required' error, got: %v", err)
	}
}

func TestGetCitations_NoResolver(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{}
	skill := NewGetCitationsSkill(deps)

	input, _ := json.Marshal(GetCitationsInput{PaperIDs: []string{"paper-1"}})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no resolver configured")
	}
}

func TestGetCitations_WithResolver(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		CitationResolver: &mockCitationResolver{
			citations: []*Citation{
				{
					PaperID:    "paper-1",
					Formatted:  "Smith, J. (2024). Deep Learning. Journal of AI.",
					Style:      "APA",
					BibTeX:     "@article{paper_1,...}",
					InlineRef:  "(Smith, 2024)",
					ReferenceN: 1,
				},
			},
		},
	}
	skill := NewGetCitationsSkill(deps)

	input, _ := json.Marshal(GetCitationsInput{
		PaperIDs: []string{"paper-1"},
		Style:    "APA",
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetCitationsOutput)
	if len(output.Citations) != 1 {
		t.Errorf("expected 1 citation, got %d", len(output.Citations))
	}
	if output.Citations[0].Style != "APA" {
		t.Errorf("unexpected style: %s", output.Citations[0].Style)
	}
}

func TestGetCitations_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &AcademicDependencies{
		ContentStore: &mockAcademicContentStore{
			entries: map[string]*ctxpkg.ContentEntry{
				"paper-1": {
					ID:        "paper-1",
					Content:   "Deep Learning Approaches\n\nAbstract...",
					Timestamp: now,
					Metadata: map[string]string{
						"title":   "Deep Learning Approaches",
						"authors": "Smith, J.",
						"source":  "Journal of AI",
					},
				},
			},
		},
	}
	skill := NewGetCitationsSkill(deps)

	input, _ := json.Marshal(GetCitationsInput{
		PaperIDs: []string{"paper-1"},
		Style:    "IEEE",
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*GetCitationsOutput)
	if len(output.Citations) != 1 {
		t.Errorf("expected 1 citation, got %d", len(output.Citations))
	}
}

// =============================================================================
// Test: academic_summarize_paper Skill
// =============================================================================

func TestNewSummarizePaperSkill(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{}
	skill := NewSummarizePaperSkill(deps)

	if skill.Name != "academic_summarize_paper" {
		t.Errorf("unexpected name: %s", skill.Name)
	}
}

func TestSummarizePaper_RequiresPaperID(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{PaperSummarizer: &mockPaperSummarizer{}}
	skill := NewSummarizePaperSkill(deps)

	input, _ := json.Marshal(SummarizePaperInput{})
	_, err := skill.Handler(context.Background(), input)
	if err == nil || err.Error() != "paper_id is required" {
		t.Errorf("expected 'paper_id is required' error, got: %v", err)
	}
}

func TestSummarizePaper_NoSummarizer(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{}
	skill := NewSummarizePaperSkill(deps)

	input, _ := json.Marshal(SummarizePaperInput{PaperID: "paper-1"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error when no summarizer configured")
	}
}

func TestSummarizePaper_WithSummarizer(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		PaperSummarizer: &mockPaperSummarizer{
			summary: &PaperSummary{
				PaperID:     "paper-1",
				Title:       "Deep Learning Study",
				Summary:     "This paper explores deep learning techniques...",
				KeyFindings: []string{"Finding 1", "Finding 2"},
				Methodology: "We used neural networks...",
				Conclusion:  "Deep learning shows promise...",
				TokenCount:  250,
			},
		},
	}
	skill := NewSummarizePaperSkill(deps)

	input, _ := json.Marshal(SummarizePaperInput{
		PaperID:           "paper-1",
		IncludeConclusion: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SummarizePaperOutput)
	if !output.Found {
		t.Error("expected paper to be found")
	}
	if output.Summary.Title != "Deep Learning Study" {
		t.Errorf("unexpected title: %s", output.Summary.Title)
	}
}

func TestSummarizePaper_WithContentStore(t *testing.T) {
	t.Parallel()

	now := time.Now()
	deps := &AcademicDependencies{
		ContentStore: &mockAcademicContentStore{
			entries: map[string]*ctxpkg.ContentEntry{
				"paper-1": {
					ID:        "paper-1",
					Content:   "Deep Learning Study\n\nAbstract\n\nThis paper discusses...\n\nMethodology\n\nWe used...\n\nConclusion\n\nIn summary...",
					Timestamp: now,
					Metadata: map[string]string{
						"title": "Deep Learning Study",
					},
				},
			},
		},
	}
	skill := NewSummarizePaperSkill(deps)

	input, _ := json.Marshal(SummarizePaperInput{
		PaperID:           "paper-1",
		IncludeAbstract:   true,
		IncludeConclusion: true,
	})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SummarizePaperOutput)
	if !output.Found {
		t.Error("expected paper to be found")
	}
}

func TestSummarizePaper_NotFound(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		ContentStore: &mockAcademicContentStore{
			entries: map[string]*ctxpkg.ContentEntry{},
		},
	}
	skill := NewSummarizePaperSkill(deps)

	input, _ := json.Marshal(SummarizePaperInput{PaperID: "nonexistent"})

	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*SummarizePaperOutput)
	if output.Found {
		t.Error("expected paper not to be found")
	}
}

// =============================================================================
// Test: Helper Functions
// =============================================================================

func TestExtractTitleFromContent(t *testing.T) {
	t.Parallel()

	content := "Deep Learning Study\n\nThis paper explores..."
	title := extractTitleFromContent(content)

	if title != "Deep Learning Study" {
		t.Errorf("unexpected title: %s", title)
	}
}

func TestExtractAbstract(t *testing.T) {
	t.Parallel()

	content := "Title\n\nAbstract\n\nThis paper discusses neural networks.\n\nIntroduction\n\nNeural networks are..."
	abstract := extractAbstract(content)

	if abstract == "" {
		t.Error("expected non-empty abstract")
	}
}

func TestExtractConclusion(t *testing.T) {
	t.Parallel()

	content := "Some content...\n\nConclusion\n\nIn this paper, we showed that..."
	conclusion := extractConclusion(content)

	if conclusion == "" {
		t.Error("expected non-empty conclusion")
	}
}

func TestExtractMethodology(t *testing.T) {
	t.Parallel()

	content := "Abstract...\n\nMethodology\n\nWe used a dataset of 1000 images..."
	methodology := extractMethodology(content)

	if methodology == "" {
		t.Error("expected non-empty methodology")
	}
}

func TestExtractKeyFindings(t *testing.T) {
	t.Parallel()

	content := "Results:\n- Finding one is very important\n- Finding two confirms the hypothesis\n* Third finding shows improvement"
	findings := extractKeyFindings(content)

	if len(findings) < 2 {
		t.Errorf("expected at least 2 findings, got %d", len(findings))
	}
}

func TestFormatAPACitation(t *testing.T) {
	t.Parallel()

	citation := formatAPACitation("Smith, J.", 2024, "Deep Learning", "Journal of AI")
	expected := "Smith, J. (2024). Deep Learning. Journal of AI."

	if citation != expected {
		t.Errorf("unexpected citation: %s", citation)
	}
}

func TestFormatMLACitation(t *testing.T) {
	t.Parallel()

	citation := formatMLACitation("Smith, J.", "Deep Learning", "Journal of AI", 2024)
	expected := "Smith, J. \"Deep Learning.\" Journal of AI, 2024."

	if citation != expected {
		t.Errorf("unexpected citation: %s", citation)
	}
}

func TestFormatIEEECitation(t *testing.T) {
	t.Parallel()

	citation := formatIEEECitation("Smith, J.", "Deep Learning", "Journal of AI", 2024, 1)
	expected := "[1] Smith, J., \"Deep Learning,\" Journal of AI, 2024."

	if citation != expected {
		t.Errorf("unexpected citation: %s", citation)
	}
}

func TestGenerateInlineRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		authors  string
		year     int
		refNum   int
		style    string
		expected string
	}{
		{"Smith, J.", 2024, 1, "APA", "(Smith, 2024)"},
		{"Smith, J.", 2024, 1, "MLA", "(Smith)"},
		{"Smith, J.", 2024, 1, "IEEE", "[1]"},
	}

	for _, tc := range tests {
		result := generateInlineRef(tc.authors, tc.year, tc.refNum, tc.style)
		if result != tc.expected {
			t.Errorf("generateInlineRef(%s, %d, %d, %s) = %s, want %s",
				tc.authors, tc.year, tc.refNum, tc.style, result, tc.expected)
		}
	}
}

func TestExtractFirstAuthor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		authors  string
		expected string
	}{
		{"Smith, J., Doe, A.", "Smith"},
		{"Smith, J.", "Smith"},
		{"", "Unknown"},
	}

	for _, tc := range tests {
		result := extractFirstAuthor(tc.authors)
		if result != tc.expected {
			t.Errorf("extractFirstAuthor(%q) = %q, want %q", tc.authors, result, tc.expected)
		}
	}
}

func TestHasMatchingTopic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		paperTopics    []string
		requiredTopics []string
		expected       bool
	}{
		{[]string{"AI", "ML"}, []string{"ai"}, true},
		{[]string{"AI", "ML"}, []string{"NLP"}, false},
		{[]string{}, []string{"AI"}, false},
	}

	for _, tc := range tests {
		result := hasMatchingTopic(tc.paperTopics, tc.requiredTopics)
		if result != tc.expected {
			t.Errorf("hasMatchingTopic(%v, %v) = %v, want %v",
				tc.paperTopics, tc.requiredTopics, result, tc.expected)
		}
	}
}

// =============================================================================
// Test: Skill Registration
// =============================================================================

func TestRegisterAcademicSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := &AcademicDependencies{}

	err := RegisterAcademicSkills(registry, deps)
	if err != nil {
		t.Fatalf("failed to register skills: %v", err)
	}

	expectedSkills := []string{
		"academic_search_research",
		"academic_get_citations",
		"academic_summarize_paper",
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

func TestSearchResearch_SearcherError(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		ResearchSearcher: &mockResearchSearcher{err: fmt.Errorf("search failed")},
	}
	skill := NewSearchResearchSkill(deps)

	input, _ := json.Marshal(SearchResearchInput{Query: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from searcher")
	}
}

func TestGetCitations_ResolverError(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		CitationResolver: &mockCitationResolver{err: fmt.Errorf("resolve failed")},
	}
	skill := NewGetCitationsSkill(deps)

	input, _ := json.Marshal(GetCitationsInput{PaperIDs: []string{"paper-1"}})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from resolver")
	}
}

func TestSummarizePaper_SummarizerError(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		PaperSummarizer: &mockPaperSummarizer{err: fmt.Errorf("summarize failed")},
	}
	skill := NewSummarizePaperSkill(deps)

	input, _ := json.Marshal(SummarizePaperInput{PaperID: "paper-1"})
	result, err := skill.Handler(context.Background(), input)

	// Summarizer error returns found=false, not error
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	output := result.(*SummarizePaperOutput)
	if output.Found {
		t.Error("expected found=false on summarizer error")
	}
}

func TestSearchResearch_ContentStoreError(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		ContentStore: &mockAcademicContentStore{err: fmt.Errorf("store error")},
	}
	skill := NewSearchResearchSkill(deps)

	input, _ := json.Marshal(SearchResearchInput{Query: "test"})
	_, err := skill.Handler(context.Background(), input)
	if err == nil {
		t.Error("expected error from content store")
	}
}

// =============================================================================
// Test: Constants
// =============================================================================

func TestAcademicConstants(t *testing.T) {
	t.Parallel()

	if AcademicDomain != "academic" {
		t.Errorf("unexpected domain: %s", AcademicDomain)
	}
	if DefaultResearchLimit != 10 {
		t.Errorf("unexpected research limit: %d", DefaultResearchLimit)
	}
	if DefaultCitationLimit != 20 {
		t.Errorf("unexpected citation limit: %d", DefaultCitationLimit)
	}
	if DefaultSummaryTokens != 1000 {
		t.Errorf("unexpected summary tokens: %d", DefaultSummaryTokens)
	}
}

// =============================================================================
// Test: Default Values
// =============================================================================

func TestSearchResearch_DefaultMaxResults(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		ResearchSearcher: &mockResearchSearcher{results: []*ResearchResult{}},
	}
	skill := NewSearchResearchSkill(deps)

	input, _ := json.Marshal(SearchResearchInput{Query: "test"})

	_, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetCitations_DefaultStyle(t *testing.T) {
	t.Parallel()

	deps := &AcademicDependencies{
		CitationResolver: &mockCitationResolver{citations: []*Citation{}},
	}
	skill := NewGetCitationsSkill(deps)

	// No Style specified - should use default (APA)
	input, _ := json.Marshal(GetCitationsInput{PaperIDs: []string{"paper-1"}})

	_, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
