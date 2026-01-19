// Package skills provides AR.8.2: Librarian Retrieval Skills - specialized codebase
// search and symbol context retrieval for the Librarian agent.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// LibrarianDomain is the skill domain for Librarian skills.
	LibrarianDomain = "librarian"

	// Default values
	DefaultCodeSearchLimit   = 20
	DefaultSymbolContextSize = 50 // lines of context
	DefaultPatternLimit      = 10
)

// =============================================================================
// Librarian Dependencies
// =============================================================================

// LibrarianDependencies contains dependencies for Librarian retrieval skills.
type LibrarianDependencies struct {
	// CodeSearcher provides codebase search functionality.
	CodeSearcher CodeSearcher

	// SymbolResolver provides symbol context retrieval.
	SymbolResolver SymbolResolver

	// PatternMatcher finds similar code patterns.
	PatternMatcher PatternMatcher
}

// CodeSearcher provides codebase search with language awareness.
type CodeSearcher interface {
	// SearchCode searches the codebase with optional language filters.
	SearchCode(ctx context.Context, query string, opts *CodeSearchOptions) ([]*CodeResult, error)
}

// CodeSearchOptions configures code search behavior.
type CodeSearchOptions struct {
	Languages   []string // Filter by programming languages
	FilePattern string   // Glob pattern for files
	SymbolTypes []string // symbol, function, class, interface, etc.
	MaxResults  int
}

// CodeResult represents a code search result.
type CodeResult struct {
	FilePath    string   `json:"file_path"`
	LineStart   int      `json:"line_start"`
	LineEnd     int      `json:"line_end"`
	Content     string   `json:"content"`
	Language    string   `json:"language"`
	SymbolName  string   `json:"symbol_name,omitempty"`
	SymbolType  string   `json:"symbol_type,omitempty"`
	Score       float64  `json:"score"`
	Highlights  []string `json:"highlights,omitempty"`
}

// SymbolResolver provides symbol context retrieval.
type SymbolResolver interface {
	// GetSymbolContext retrieves context around a symbol.
	GetSymbolContext(ctx context.Context, symbol string, opts *SymbolContextOptions) (*SymbolContext, error)
}

// SymbolContextOptions configures symbol context retrieval.
type SymbolContextOptions struct {
	FilePath      string // Specific file to search in
	ContextLines  int    // Lines of context around symbol
	IncludeUsages bool   // Include usage sites
	MaxUsages     int    // Maximum usage sites to return
}

// SymbolContext represents context around a symbol.
type SymbolContext struct {
	Symbol      string        `json:"symbol"`
	Definition  *CodeLocation `json:"definition,omitempty"`
	Usages      []*CodeLocation `json:"usages,omitempty"`
	RelatedSymbols []string   `json:"related_symbols,omitempty"`
}

// CodeLocation represents a location in code.
type CodeLocation struct {
	FilePath  string `json:"file_path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Content   string `json:"content"`
	Language  string `json:"language"`
}

// PatternMatcher finds similar code patterns.
type PatternMatcher interface {
	// FindPatterns finds code similar to the given pattern.
	FindPatterns(ctx context.Context, pattern string, opts *PatternOptions) ([]*PatternMatch, error)
}

// PatternOptions configures pattern matching.
type PatternOptions struct {
	Language      string  // Target language
	MinSimilarity float64 // Minimum similarity threshold (0-1)
	MaxResults    int
}

// PatternMatch represents a matched code pattern.
type PatternMatch struct {
	FilePath   string  `json:"file_path"`
	LineStart  int     `json:"line_start"`
	LineEnd    int     `json:"line_end"`
	Content    string  `json:"content"`
	Similarity float64 `json:"similarity"`
	Language   string  `json:"language"`
}

// =============================================================================
// librarian_search_codebase Skill
// =============================================================================

// SearchCodebaseInput defines the input for librarian_search_codebase skill.
type SearchCodebaseInput struct {
	// Query is the search query (required)
	Query string `json:"query"`

	// Languages filters by programming languages (optional)
	Languages []string `json:"languages,omitempty"`

	// FilePattern is a glob pattern for files (optional)
	FilePattern string `json:"file_pattern,omitempty"`

	// SymbolTypes filters by symbol type (optional)
	SymbolTypes []string `json:"symbol_types,omitempty"`

	// MaxResults limits the number of results (default: 20)
	MaxResults int `json:"max_results,omitempty"`
}

// SearchCodebaseOutput defines the output for librarian_search_codebase skill.
type SearchCodebaseOutput struct {
	// Results are the code search results
	Results []*CodeResult `json:"results"`

	// TotalMatches is the total number of matches
	TotalMatches int `json:"total_matches"`
}

// NewSearchCodebaseSkill creates the librarian_search_codebase skill.
func NewSearchCodebaseSkill(deps *LibrarianDependencies) *skills.Skill {
	return skills.NewSkill("librarian_search_codebase").
		Description("Search the codebase with symbol awareness and language filters. Use for finding functions, classes, interfaces, and code patterns.").
		Domain(LibrarianDomain).
		Keywords("search", "code", "codebase", "symbol", "function", "class").
		Priority(100). // High priority - always loaded for Librarian
		StringParam("query", "Search query for code", true).
		ArrayParam("languages", "Filter by programming languages (e.g., go, python, typescript)", "string", false).
		StringParam("file_pattern", "Glob pattern to filter files (e.g., **/*.go)", false).
		ArrayParam("symbol_types", "Filter by symbol type (function, class, interface, variable)", "string", false).
		IntParam("max_results", "Maximum results to return (default: 20)", false).
		Handler(createSearchCodebaseHandler(deps)).
		Build()
}

func createSearchCodebaseHandler(deps *LibrarianDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var params SearchCodebaseInput
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if params.Query == "" {
			return nil, fmt.Errorf("query is required")
		}

		return executeSearchCodebase(ctx, params, deps)
	}
}

func executeSearchCodebase(
	ctx context.Context,
	params SearchCodebaseInput,
	deps *LibrarianDependencies,
) (*SearchCodebaseOutput, error) {
	if deps.CodeSearcher == nil {
		return nil, fmt.Errorf("code searcher not configured")
	}

	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultCodeSearchLimit
	}

	opts := &CodeSearchOptions{
		Languages:   params.Languages,
		FilePattern: params.FilePattern,
		SymbolTypes: params.SymbolTypes,
		MaxResults:  maxResults,
	}

	results, err := deps.CodeSearcher.SearchCode(ctx, params.Query, opts)
	if err != nil {
		return nil, fmt.Errorf("code search failed: %w", err)
	}

	return &SearchCodebaseOutput{
		Results:      results,
		TotalMatches: len(results),
	}, nil
}

// =============================================================================
// librarian_get_symbol_context Skill
// =============================================================================

// GetSymbolContextInput defines the input for librarian_get_symbol_context skill.
type GetSymbolContextInput struct {
	// Symbol is the symbol name to get context for (required)
	Symbol string `json:"symbol"`

	// FilePath is the specific file to search in (optional)
	FilePath string `json:"file_path,omitempty"`

	// ContextLines is lines of context around the symbol (default: 50)
	ContextLines int `json:"context_lines,omitempty"`

	// IncludeUsages includes usage sites (default: true)
	IncludeUsages *bool `json:"include_usages,omitempty"`

	// MaxUsages limits usage sites returned (default: 10)
	MaxUsages int `json:"max_usages,omitempty"`
}

// GetSymbolContextOutput defines the output for librarian_get_symbol_context skill.
type GetSymbolContextOutput struct {
	// Context is the symbol context
	Context *SymbolContext `json:"context"`

	// Found indicates if the symbol was found
	Found bool `json:"found"`
}

// NewGetSymbolContextSkill creates the librarian_get_symbol_context skill.
func NewGetSymbolContextSkill(deps *LibrarianDependencies) *skills.Skill {
	return skills.NewSkill("librarian_get_symbol_context").
		Description("Get surrounding context for a symbol including its definition and usage sites. Use when you need to understand how a function, class, or variable is used.").
		Domain(LibrarianDomain).
		Keywords("symbol", "context", "definition", "usage", "reference").
		Priority(100).
		StringParam("symbol", "Symbol name to get context for", true).
		StringParam("file_path", "Specific file to search in", false).
		IntParam("context_lines", "Lines of context around symbol (default: 50)", false).
		BoolParam("include_usages", "Include usage sites (default: true)", false).
		IntParam("max_usages", "Maximum usage sites to return (default: 10)", false).
		Handler(createGetSymbolContextHandler(deps)).
		Build()
}

func createGetSymbolContextHandler(deps *LibrarianDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var params GetSymbolContextInput
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if params.Symbol == "" {
			return nil, fmt.Errorf("symbol is required")
		}

		return executeGetSymbolContext(ctx, params, deps)
	}
}

func executeGetSymbolContext(
	ctx context.Context,
	params GetSymbolContextInput,
	deps *LibrarianDependencies,
) (*GetSymbolContextOutput, error) {
	if deps.SymbolResolver == nil {
		return nil, fmt.Errorf("symbol resolver not configured")
	}

	contextLines := params.ContextLines
	if contextLines <= 0 {
		contextLines = DefaultSymbolContextSize
	}

	includeUsages := true
	if params.IncludeUsages != nil {
		includeUsages = *params.IncludeUsages
	}

	maxUsages := params.MaxUsages
	if maxUsages <= 0 {
		maxUsages = 10
	}

	opts := &SymbolContextOptions{
		FilePath:      params.FilePath,
		ContextLines:  contextLines,
		IncludeUsages: includeUsages,
		MaxUsages:     maxUsages,
	}

	symbolContext, err := deps.SymbolResolver.GetSymbolContext(ctx, params.Symbol, opts)
	if err != nil {
		return nil, fmt.Errorf("symbol context retrieval failed: %w", err)
	}

	return &GetSymbolContextOutput{
		Context: symbolContext,
		Found:   symbolContext != nil && symbolContext.Definition != nil,
	}, nil
}

// =============================================================================
// librarian_get_pattern_examples Skill
// =============================================================================

// GetPatternExamplesInput defines the input for librarian_get_pattern_examples skill.
type GetPatternExamplesInput struct {
	// Pattern is the code pattern to find examples of (required)
	Pattern string `json:"pattern"`

	// Language is the target programming language (optional)
	Language string `json:"language,omitempty"`

	// MinSimilarity is the minimum similarity threshold 0-1 (default: 0.7)
	MinSimilarity float64 `json:"min_similarity,omitempty"`

	// MaxResults limits results returned (default: 10)
	MaxResults int `json:"max_results,omitempty"`
}

// GetPatternExamplesOutput defines the output for librarian_get_pattern_examples skill.
type GetPatternExamplesOutput struct {
	// Matches are the pattern matches
	Matches []*PatternMatch `json:"matches"`

	// TotalMatches is the total number of matches
	TotalMatches int `json:"total_matches"`

	// PatternSummary summarizes the pattern
	PatternSummary string `json:"pattern_summary"`
}

// NewGetPatternExamplesSkill creates the librarian_get_pattern_examples skill.
func NewGetPatternExamplesSkill(deps *LibrarianDependencies) *skills.Skill {
	return skills.NewSkill("librarian_get_pattern_examples").
		Description("Find similar code patterns in the codebase. Use when you need to see how a pattern is used elsewhere or find examples of similar implementations.").
		Domain(LibrarianDomain).
		Keywords("pattern", "example", "similar", "code", "implementation").
		Priority(90). // Slightly lower priority
		StringParam("pattern", "Code pattern to find examples of", true).
		StringParam("language", "Target programming language", false).
		IntParam("max_results", "Maximum results to return (default: 10)", false).
		Handler(createGetPatternExamplesHandler(deps)).
		Build()
}

func createGetPatternExamplesHandler(deps *LibrarianDependencies) skills.Handler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var params GetPatternExamplesInput
		if err := json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input: %w", err)
		}

		if params.Pattern == "" {
			return nil, fmt.Errorf("pattern is required")
		}

		return executeGetPatternExamples(ctx, params, deps)
	}
}

func executeGetPatternExamples(
	ctx context.Context,
	params GetPatternExamplesInput,
	deps *LibrarianDependencies,
) (*GetPatternExamplesOutput, error) {
	if deps.PatternMatcher == nil {
		return nil, fmt.Errorf("pattern matcher not configured")
	}

	minSimilarity := params.MinSimilarity
	if minSimilarity <= 0 {
		minSimilarity = 0.7
	}

	maxResults := params.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultPatternLimit
	}

	opts := &PatternOptions{
		Language:      params.Language,
		MinSimilarity: minSimilarity,
		MaxResults:    maxResults,
	}

	matches, err := deps.PatternMatcher.FindPatterns(ctx, params.Pattern, opts)
	if err != nil {
		return nil, fmt.Errorf("pattern matching failed: %w", err)
	}

	return &GetPatternExamplesOutput{
		Matches:        matches,
		TotalMatches:   len(matches),
		PatternSummary: generatePatternSummary(params.Pattern),
	}, nil
}

// generatePatternSummary creates a brief summary of the pattern.
func generatePatternSummary(pattern string) string {
	lines := strings.Split(pattern, "\n")
	if len(lines) == 1 {
		if len(pattern) > 100 {
			return pattern[:100] + "..."
		}
		return pattern
	}

	// Multi-line: show first line and count
	firstLine := strings.TrimSpace(lines[0])
	if len(firstLine) > 80 {
		firstLine = firstLine[:80] + "..."
	}
	return fmt.Sprintf("%s (+ %d more lines)", firstLine, len(lines)-1)
}

// =============================================================================
// Skill Registration
// =============================================================================

// RegisterLibrarianSkills registers all Librarian retrieval skills.
func RegisterLibrarianSkills(registry *skills.Registry, deps *LibrarianDependencies) error {
	// librarian_search_codebase
	searchSkill := NewSearchCodebaseSkill(deps)
	if err := registry.Register(searchSkill); err != nil {
		return fmt.Errorf("failed to register librarian_search_codebase: %w", err)
	}

	// librarian_get_symbol_context
	symbolSkill := NewGetSymbolContextSkill(deps)
	if err := registry.Register(symbolSkill); err != nil {
		return fmt.Errorf("failed to register librarian_get_symbol_context: %w", err)
	}

	// librarian_get_pattern_examples
	patternSkill := NewGetPatternExamplesSkill(deps)
	if err := registry.Register(patternSkill); err != nil {
		return fmt.Errorf("failed to register librarian_get_pattern_examples: %w", err)
	}

	// Load all skills (TIER 1 - always loaded for Librarian)
	registry.Load("librarian_search_codebase")
	registry.Load("librarian_get_symbol_context")
	registry.Load("librarian_get_pattern_examples")

	return nil
}

// =============================================================================
// Fallback Implementation
// =============================================================================

// FallbackCodeSearcher provides a basic code searcher using TieredSearcher.
type FallbackCodeSearcher struct {
	Searcher *ctxpkg.TieredSearcher
}

// SearchCode implements CodeSearcher using TieredSearcher.
func (f *FallbackCodeSearcher) SearchCode(
	ctx context.Context,
	query string,
	opts *CodeSearchOptions,
) ([]*CodeResult, error) {
	if f.Searcher == nil {
		return nil, fmt.Errorf("searcher not configured")
	}

	// Build enhanced query with language hints
	enhancedQuery := query
	if len(opts.Languages) > 0 {
		enhancedQuery = fmt.Sprintf("%s language:%s", query, strings.Join(opts.Languages, ","))
	}

	results := f.Searcher.SearchWithBudget(ctx, enhancedQuery, ctxpkg.TierWarmBudget)

	codeResults := make([]*CodeResult, 0, len(results.Results))
	for i, r := range results.Results {
		if shouldIncludeCodeResult(r, opts) {
			// Use position-based scoring since ContentEntry has no Score field
			score := 1.0 - float64(i)*0.05
			if score < 0.1 {
				score = 0.1
			}
			codeResults = append(codeResults, &CodeResult{
				FilePath: r.ID,
				Content:  r.Content,
				Score:    score,
			})
		}
		if len(codeResults) >= opts.MaxResults {
			break
		}
	}

	return codeResults, nil
}

func shouldIncludeCodeResult(r *ctxpkg.ContentEntry, opts *CodeSearchOptions) bool {
	// Filter by content type (code files only)
	if r.ContentType != ctxpkg.ContentTypeCodeFile {
		return false
	}

	// Additional filtering can be added here
	return true
}
