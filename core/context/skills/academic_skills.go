// Package skills provides AR.8.4: Academic Retrieval Skills.
//
// The Academic agent specializes in research papers and documentation.
// These skills provide:
// - Research paper search and retrieval
// - Citation management
// - Paper summarization
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
	// AcademicDomain is the skill domain for academic skills.
	AcademicDomain = "academic"

	// DefaultResearchLimit is the default max papers to return.
	DefaultResearchLimit = 10

	// DefaultCitationLimit is the default max citations to return.
	DefaultCitationLimit = 20

	// DefaultSummaryTokens is the default max tokens for paper summaries.
	DefaultSummaryTokens = 1000
)

// =============================================================================
// Interfaces
// =============================================================================

// ResearchSearcher searches research papers and documentation.
type ResearchSearcher interface {
	SearchResearch(
		ctx context.Context,
		query string,
		opts *ResearchSearchOptions,
	) ([]*ResearchResult, error)
}

// CitationResolver resolves and formats citations.
type CitationResolver interface {
	ResolveCitations(
		ctx context.Context,
		paperIDs []string,
		style string,
	) ([]*Citation, error)
}

// PaperSummarizer generates paper summaries.
type PaperSummarizer interface {
	SummarizePaper(
		ctx context.Context,
		paperID string,
		opts *SummaryRequestOptions,
	) (*PaperSummary, error)
}

// AcademicContentStore is the interface for content store operations.
type AcademicContentStore interface {
	Search(query string, filters *ctxpkg.SearchFilters, limit int) ([]*ctxpkg.ContentEntry, error)
	Get(ctx context.Context, id string) (*ctxpkg.ContentEntry, error)
}

// AcademicSearcher is the interface for tiered search operations.
type AcademicSearcher interface {
	SearchWithBudget(ctx context.Context, query string, budget time.Duration) *ctxpkg.TieredSearchResult
}

// =============================================================================
// Types
// =============================================================================

// ResearchSearchOptions configures research search.
type ResearchSearchOptions struct {
	MaxResults   int
	Topics       []string
	FromDate     time.Time
	ToDate       time.Time
	SourceTypes  []string
	MinRelevance float64
}

// ResearchResult represents a research paper or document.
type ResearchResult struct {
	PaperID     string    `json:"paper_id"`
	Title       string    `json:"title"`
	Authors     []string  `json:"authors"`
	Abstract    string    `json:"abstract"`
	Source      string    `json:"source"`
	PublishDate time.Time `json:"publish_date"`
	Topics      []string  `json:"topics"`
	Relevance   float64   `json:"relevance"`
	URL         string    `json:"url,omitempty"`
}

// Citation represents a formatted citation.
type Citation struct {
	PaperID    string `json:"paper_id"`
	Formatted  string `json:"formatted"`
	Style      string `json:"style"`
	BibTeX     string `json:"bibtex,omitempty"`
	InlineRef  string `json:"inline_ref"`
	ReferenceN int    `json:"reference_n"`
}

// SummaryRequestOptions configures summary generation.
type SummaryRequestOptions struct {
	MaxTokens         int
	IncludeAbstract   bool
	IncludeConclusion bool
	FocusAreas        []string
}

// PaperSummary represents a paper summary.
type PaperSummary struct {
	PaperID      string   `json:"paper_id"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	KeyFindings  []string `json:"key_findings"`
	Methodology  string   `json:"methodology,omitempty"`
	Conclusion   string   `json:"conclusion,omitempty"`
	Limitations  []string `json:"limitations,omitempty"`
	FutureWork   []string `json:"future_work,omitempty"`
	TokenCount   int      `json:"token_count"`
}

// =============================================================================
// Dependencies
// =============================================================================

// AcademicDependencies holds dependencies for academic skills.
type AcademicDependencies struct {
	ResearchSearcher  ResearchSearcher
	CitationResolver  CitationResolver
	PaperSummarizer   PaperSummarizer
	ContentStore      AcademicContentStore
	Searcher          AcademicSearcher
}

// =============================================================================
// Input/Output Types
// =============================================================================

// SearchResearchInput is input for academic_search_research.
type SearchResearchInput struct {
	Query        string   `json:"query"`
	MaxResults   int      `json:"max_results,omitempty"`
	Topics       []string `json:"topics,omitempty"`
	SourceTypes  []string `json:"source_types,omitempty"`
	MinRelevance float64  `json:"min_relevance,omitempty"`
}

// SearchResearchOutput is output for academic_search_research.
type SearchResearchOutput struct {
	Papers     []*ResearchResult `json:"papers"`
	TotalFound int               `json:"total_found"`
}

// GetCitationsInput is input for academic_get_citations.
type GetCitationsInput struct {
	PaperIDs []string `json:"paper_ids"`
	Style    string   `json:"style,omitempty"`
}

// GetCitationsOutput is output for academic_get_citations.
type GetCitationsOutput struct {
	Citations []*Citation `json:"citations"`
	BibFile   string      `json:"bib_file,omitempty"`
}

// SummarizePaperInput is input for academic_summarize_paper.
type SummarizePaperInput struct {
	PaperID           string   `json:"paper_id"`
	MaxTokens         int      `json:"max_tokens,omitempty"`
	IncludeAbstract   bool     `json:"include_abstract,omitempty"`
	IncludeConclusion bool     `json:"include_conclusion,omitempty"`
	FocusAreas        []string `json:"focus_areas,omitempty"`
}

// SummarizePaperOutput is output for academic_summarize_paper.
type SummarizePaperOutput struct {
	Found   bool          `json:"found"`
	Summary *PaperSummary `json:"summary,omitempty"`
}

// =============================================================================
// Skill Constructors
// =============================================================================

// NewSearchResearchSkill creates the academic_search_research skill.
func NewSearchResearchSkill(deps *AcademicDependencies) *skills.Skill {
	return skills.NewSkill("academic_search_research").
		Domain(AcademicDomain).
		Description("Search research papers and technical documentation").
		Keywords("research", "paper", "academic", "documentation", "literature").
		Handler(createSearchResearchHandler(deps)).
		Build()
}

// NewGetCitationsSkill creates the academic_get_citations skill.
func NewGetCitationsSkill(deps *AcademicDependencies) *skills.Skill {
	return skills.NewSkill("academic_get_citations").
		Domain(AcademicDomain).
		Description("Get formatted citations for research papers").
		Keywords("citation", "cite", "reference", "bibliography").
		Handler(createGetCitationsHandler(deps)).
		Build()
}

// NewSummarizePaperSkill creates the academic_summarize_paper skill.
func NewSummarizePaperSkill(deps *AcademicDependencies) *skills.Skill {
	return skills.NewSkill("academic_summarize_paper").
		Domain(AcademicDomain).
		Description("Summarize a research paper or technical document").
		Keywords("summarize", "paper", "abstract", "overview").
		Handler(createSummarizePaperHandler(deps)).
		Build()
}

// =============================================================================
// Handlers
// =============================================================================

func createSearchResearchHandler(deps *AcademicDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in SearchResearchInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		maxResults := in.MaxResults
		if maxResults <= 0 {
			maxResults = DefaultResearchLimit
		}

		// Use dedicated searcher if available
		if deps.ResearchSearcher != nil {
			return searchWithResearchSearcher(ctx, deps, in, maxResults)
		}

		// Fall back to content store search
		if deps.ContentStore != nil {
			return searchResearchWithContentStore(deps, in, maxResults)
		}

		// Fall back to tiered searcher
		if deps.Searcher != nil {
			return searchResearchWithSearcher(ctx, deps, in, maxResults)
		}

		return nil, fmt.Errorf("no research searcher or content store configured")
	}
}

func searchWithResearchSearcher(
	ctx context.Context,
	deps *AcademicDependencies,
	in SearchResearchInput,
	maxResults int,
) (*SearchResearchOutput, error) {
	opts := &ResearchSearchOptions{
		MaxResults:   maxResults,
		Topics:       in.Topics,
		SourceTypes:  in.SourceTypes,
		MinRelevance: in.MinRelevance,
	}

	papers, err := deps.ResearchSearcher.SearchResearch(ctx, in.Query, opts)
	if err != nil {
		return nil, fmt.Errorf("research search failed: %w", err)
	}

	return &SearchResearchOutput{
		Papers:     papers,
		TotalFound: len(papers),
	}, nil
}

func searchResearchWithContentStore(
	deps *AcademicDependencies,
	in SearchResearchInput,
	maxResults int,
) (*SearchResearchOutput, error) {
	filters := &ctxpkg.SearchFilters{
		ContentTypes: []ctxpkg.ContentType{ctxpkg.ContentTypeResearchPaper},
	}

	entries, err := deps.ContentStore.Search(in.Query, filters, maxResults*2)
	if err != nil {
		return nil, fmt.Errorf("content store search failed: %w", err)
	}

	papers := make([]*ResearchResult, 0, len(entries))
	for i, entry := range entries {
		paper := convertEntryToResearchResult(entry, i)
		if matchesResearchCriteria(paper, in) {
			papers = append(papers, paper)
		}
		if len(papers) >= maxResults {
			break
		}
	}

	return &SearchResearchOutput{
		Papers:     papers,
		TotalFound: len(papers),
	}, nil
}

func searchResearchWithSearcher(
	ctx context.Context,
	deps *AcademicDependencies,
	in SearchResearchInput,
	maxResults int,
) (*SearchResearchOutput, error) {
	results := deps.Searcher.SearchWithBudget(ctx, in.Query, ctxpkg.TierWarmBudget)

	papers := make([]*ResearchResult, 0, maxResults)
	for i, entry := range results.Results {
		if entry.ContentType != ctxpkg.ContentTypeResearchPaper {
			continue
		}

		paper := convertEntryToResearchResult(entry, i)
		if matchesResearchCriteria(paper, in) {
			papers = append(papers, paper)
		}
		if len(papers) >= maxResults {
			break
		}
	}

	return &SearchResearchOutput{
		Papers:     papers,
		TotalFound: len(papers),
	}, nil
}

func convertEntryToResearchResult(entry *ctxpkg.ContentEntry, position int) *ResearchResult {
	// Calculate relevance based on position
	relevance := 1.0 - float64(position)*0.05
	if relevance < 0.3 {
		relevance = 0.3
	}

	// Extract metadata
	title := entry.Metadata["title"]
	if title == "" {
		title = extractTitleFromContent(entry.Content)
	}

	var authors []string
	if authorsStr := entry.Metadata["authors"]; authorsStr != "" {
		authors = strings.Split(authorsStr, ",")
	}

	return &ResearchResult{
		PaperID:     entry.ID,
		Title:       title,
		Authors:     authors,
		Abstract:    extractAbstract(entry.Content),
		Source:      entry.Metadata["source"],
		PublishDate: entry.Timestamp,
		Topics:      entry.Keywords,
		Relevance:   relevance,
		URL:         entry.Metadata["url"],
	}
}

func matchesResearchCriteria(paper *ResearchResult, in SearchResearchInput) bool {
	// Check minimum relevance
	if in.MinRelevance > 0 && paper.Relevance < in.MinRelevance {
		return false
	}

	// Check topics if specified
	if len(in.Topics) > 0 && !hasMatchingTopic(paper.Topics, in.Topics) {
		return false
	}

	// Check source types if specified
	if len(in.SourceTypes) > 0 && !containsString(in.SourceTypes, paper.Source) {
		return false
	}

	return true
}

func hasMatchingTopic(paperTopics, requiredTopics []string) bool {
	for _, required := range requiredTopics {
		for _, topic := range paperTopics {
			if strings.EqualFold(topic, required) {
				return true
			}
		}
	}
	return false
}

func createGetCitationsHandler(deps *AcademicDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in GetCitationsInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if len(in.PaperIDs) == 0 {
			return nil, fmt.Errorf("paper_ids is required")
		}

		style := in.Style
		if style == "" {
			style = "APA"
		}

		// Use dedicated resolver if available
		if deps.CitationResolver != nil {
			return getCitationsWithResolver(ctx, deps, in.PaperIDs, style)
		}

		// Fall back to generating citations from content store
		if deps.ContentStore != nil {
			return generateCitationsFromStore(ctx, deps, in.PaperIDs, style)
		}

		return nil, fmt.Errorf("no citation resolver or content store configured")
	}
}

func getCitationsWithResolver(
	ctx context.Context,
	deps *AcademicDependencies,
	paperIDs []string,
	style string,
) (*GetCitationsOutput, error) {
	citations, err := deps.CitationResolver.ResolveCitations(ctx, paperIDs, style)
	if err != nil {
		return nil, fmt.Errorf("citation resolution failed: %w", err)
	}

	bibFile := generateBibFile(citations)

	return &GetCitationsOutput{
		Citations: citations,
		BibFile:   bibFile,
	}, nil
}

func generateCitationsFromStore(
	ctx context.Context,
	deps *AcademicDependencies,
	paperIDs []string,
	style string,
) (*GetCitationsOutput, error) {
	citations := make([]*Citation, 0, len(paperIDs))

	for i, paperID := range paperIDs {
		entry, err := deps.ContentStore.Get(ctx, paperID)
		if err != nil || entry == nil {
			continue
		}

		citation := generateCitationFromEntry(entry, style, i+1)
		citations = append(citations, citation)
	}

	bibFile := generateBibFile(citations)

	return &GetCitationsOutput{
		Citations: citations,
		BibFile:   bibFile,
	}, nil
}

func generateCitationFromEntry(entry *ctxpkg.ContentEntry, style string, refNum int) *Citation {
	title := entry.Metadata["title"]
	if title == "" {
		title = extractTitleFromContent(entry.Content)
	}

	authors := entry.Metadata["authors"]
	year := entry.Timestamp.Year()
	source := entry.Metadata["source"]

	var formatted string
	switch strings.ToUpper(style) {
	case "APA":
		formatted = formatAPACitation(authors, year, title, source)
	case "MLA":
		formatted = formatMLACitation(authors, title, source, year)
	case "IEEE":
		formatted = formatIEEECitation(authors, title, source, year, refNum)
	default:
		formatted = formatAPACitation(authors, year, title, source)
	}

	inlineRef := generateInlineRef(authors, year, refNum, style)
	bibtex := generateBibTeX(entry.ID, authors, title, year, source)

	return &Citation{
		PaperID:    entry.ID,
		Formatted:  formatted,
		Style:      style,
		BibTeX:     bibtex,
		InlineRef:  inlineRef,
		ReferenceN: refNum,
	}
}

func formatAPACitation(authors string, year int, title, source string) string {
	return fmt.Sprintf("%s (%d). %s. %s.", authors, year, title, source)
}

func formatMLACitation(authors, title, source string, year int) string {
	// Ensure authors don't end with double period
	authorsClean := strings.TrimSuffix(authors, ".")
	return fmt.Sprintf("%s. \"%s.\" %s, %d.", authorsClean, title, source, year)
}

func formatIEEECitation(authors, title, source string, year, refNum int) string {
	return fmt.Sprintf("[%d] %s, \"%s,\" %s, %d.", refNum, authors, title, source, year)
}

func generateInlineRef(authors string, year, refNum int, style string) string {
	switch strings.ToUpper(style) {
	case "APA":
		firstAuthor := extractFirstAuthor(authors)
		return fmt.Sprintf("(%s, %d)", firstAuthor, year)
	case "MLA":
		firstAuthor := extractFirstAuthor(authors)
		return fmt.Sprintf("(%s)", firstAuthor)
	case "IEEE":
		return fmt.Sprintf("[%d]", refNum)
	default:
		return fmt.Sprintf("[%d]", refNum)
	}
}

func extractFirstAuthor(authors string) string {
	if authors == "" {
		return "Unknown"
	}
	parts := strings.SplitN(authors, ",", 2)
	return strings.TrimSpace(parts[0])
}

func generateBibTeX(id, authors, title string, year int, source string) string {
	key := strings.ReplaceAll(id, "-", "_")
	return fmt.Sprintf("@article{%s,\n  author = {%s},\n  title = {%s},\n  year = {%d},\n  journal = {%s}\n}",
		key, authors, title, year, source)
}

func generateBibFile(citations []*Citation) string {
	var builder strings.Builder
	for i, citation := range citations {
		if i > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(citation.BibTeX)
	}
	return builder.String()
}

func createSummarizePaperHandler(deps *AcademicDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in SummarizePaperInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if in.PaperID == "" {
			return nil, fmt.Errorf("paper_id is required")
		}

		maxTokens := in.MaxTokens
		if maxTokens <= 0 {
			maxTokens = DefaultSummaryTokens
		}

		// Use dedicated summarizer if available
		if deps.PaperSummarizer != nil {
			return summarizeWithPaperSummarizer(ctx, deps, in, maxTokens)
		}

		// Fall back to building summary from content store
		if deps.ContentStore != nil {
			return buildPaperSummaryFromStore(ctx, deps, in, maxTokens)
		}

		return nil, fmt.Errorf("no paper summarizer or content store configured")
	}
}

func summarizeWithPaperSummarizer(
	ctx context.Context,
	deps *AcademicDependencies,
	in SummarizePaperInput,
	maxTokens int,
) (*SummarizePaperOutput, error) {
	opts := &SummaryRequestOptions{
		MaxTokens:         maxTokens,
		IncludeAbstract:   in.IncludeAbstract,
		IncludeConclusion: in.IncludeConclusion,
		FocusAreas:        in.FocusAreas,
	}

	summary, err := deps.PaperSummarizer.SummarizePaper(ctx, in.PaperID, opts)
	if err != nil {
		return &SummarizePaperOutput{Found: false}, nil
	}

	return &SummarizePaperOutput{
		Found:   true,
		Summary: summary,
	}, nil
}

func buildPaperSummaryFromStore(
	ctx context.Context,
	deps *AcademicDependencies,
	in SummarizePaperInput,
	maxTokens int,
) (*SummarizePaperOutput, error) {
	entry, err := deps.ContentStore.Get(ctx, in.PaperID)
	if err != nil || entry == nil {
		return &SummarizePaperOutput{Found: false}, nil
	}

	summary := buildSummaryFromPaperEntry(entry, in, maxTokens)
	return &SummarizePaperOutput{
		Found:   true,
		Summary: summary,
	}, nil
}

func buildSummaryFromPaperEntry(
	entry *ctxpkg.ContentEntry,
	in SummarizePaperInput,
	maxTokens int,
) *PaperSummary {
	title := entry.Metadata["title"]
	if title == "" {
		title = extractTitleFromContent(entry.Content)
	}

	summary := &PaperSummary{
		PaperID:     entry.ID,
		Title:       title,
		KeyFindings: make([]string, 0),
		Limitations: make([]string, 0),
		FutureWork:  make([]string, 0),
	}

	// Extract sections
	content := entry.Content
	if in.IncludeAbstract {
		summary.Summary = extractAbstract(content)
	} else {
		summary.Summary = truncatePaperContent(content, maxTokens)
	}

	if in.IncludeConclusion {
		summary.Conclusion = extractConclusion(content)
	}

	summary.Methodology = extractMethodology(content)
	summary.KeyFindings = extractKeyFindings(content)
	summary.TokenCount = estimateTokenCount(summary.Summary)

	return summary
}

// =============================================================================
// Helper Functions
// =============================================================================

func extractTitleFromContent(content string) string {
	// Get first line as title
	lines := strings.SplitN(content, "\n", 2)
	title := strings.TrimSpace(lines[0])
	if len(title) > 200 {
		title = title[:200] + "..."
	}
	return title
}

func extractAbstract(content string) string {
	// Look for abstract section
	lowerContent := strings.ToLower(content)
	idx := strings.Index(lowerContent, "abstract")
	if idx < 0 {
		// Return first 500 chars if no abstract found
		if len(content) > 500 {
			return content[:500] + "..."
		}
		return content
	}

	// Find end of abstract (next section header or 1000 chars)
	abstractStart := idx + 8 // len("abstract")
	abstractEnd := abstractStart + 1000
	if abstractEnd > len(content) {
		abstractEnd = len(content)
	}

	// Look for section break
	remaining := content[abstractStart:abstractEnd]
	for _, header := range []string{"\n\n", "introduction", "background", "1."} {
		if breakIdx := strings.Index(strings.ToLower(remaining), header); breakIdx > 0 {
			return strings.TrimSpace(remaining[:breakIdx])
		}
	}

	return strings.TrimSpace(remaining)
}

func extractConclusion(content string) string {
	lowerContent := strings.ToLower(content)
	idx := strings.Index(lowerContent, "conclusion")
	if idx < 0 {
		return ""
	}

	// Extract up to 500 chars from conclusion
	conclusionStart := idx + 10 // len("conclusion")
	conclusionEnd := conclusionStart + 500
	if conclusionEnd > len(content) {
		conclusionEnd = len(content)
	}

	return strings.TrimSpace(content[conclusionStart:conclusionEnd])
}

func extractMethodology(content string) string {
	lowerContent := strings.ToLower(content)

	// Look for methodology section
	for _, header := range []string{"methodology", "methods", "approach"} {
		if idx := strings.Index(lowerContent, header); idx >= 0 {
			start := idx + len(header)
			end := start + 300
			if end > len(content) {
				end = len(content)
			}
			return strings.TrimSpace(content[start:end])
		}
	}

	return ""
}

func extractKeyFindings(content string) []string {
	// Simple extraction - look for bullet points or numbered items
	findings := make([]string, 0)

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") ||
			(len(trimmed) > 2 && trimmed[1] == '.' && trimmed[0] >= '1' && trimmed[0] <= '9') {
			finding := strings.TrimLeft(trimmed, "-*0123456789. ")
			if len(finding) > 20 && len(finding) < 200 {
				findings = append(findings, finding)
			}
		}
		if len(findings) >= 5 {
			break
		}
	}

	return findings
}

func truncatePaperContent(content string, maxTokens int) string {
	// Rough estimate: 1 token ≈ 4 characters
	maxChars := maxTokens * 4
	if len(content) <= maxChars {
		return content
	}

	// Find a good break point
	breakPoint := maxChars
	for i := maxChars - 1; i > maxChars-100 && i > 0; i-- {
		if content[i] == '.' || content[i] == '\n' {
			breakPoint = i + 1
			break
		}
	}

	return content[:breakPoint] + "..."
}

func estimateTokenCount(content string) int {
	// Rough estimate: 1 token ≈ 4 characters
	return len(content) / 4
}

// =============================================================================
// Registration
// =============================================================================

// RegisterAcademicSkills registers all academic skills with the registry.
func RegisterAcademicSkills(
	registry *skills.Registry,
	deps *AcademicDependencies,
) error {
	skillsToRegister := []*skills.Skill{
		NewSearchResearchSkill(deps),
		NewGetCitationsSkill(deps),
		NewSummarizePaperSkill(deps),
	}

	for _, skill := range skillsToRegister {
		if err := registry.Register(skill); err != nil {
			return fmt.Errorf("failed to register %s: %w", skill.Name, err)
		}
	}

	return nil
}
