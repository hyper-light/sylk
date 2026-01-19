// Package librarian provides search integration for the Librarian agent.
package librarian

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Default configuration values for search operations.
const (
	DefaultSearchLimit   = 20
	MaxSearchLimit       = 100
	DefaultContextLines  = 3
	DefaultTokenLimit    = 4000
	ApproxCharsPerToken  = 4
)

// Search handler errors.
var (
	ErrInvalidDomain   = errors.New("search handler only accepts 'code' domain")
	ErrNilRequest      = errors.New("request cannot be nil")
	ErrSearchFailed    = errors.New("search operation failed")
)

// SearchSystem defines the interface for the core search functionality.
type SearchSystem interface {
	Search(ctx context.Context, query string, opts SearchOptions) (*CodeSearchResult, error)
}

// SearchOptions configures search behavior.
type SearchOptions struct {
	Limit      int      `json:"limit,omitempty"`
	Types      []string `json:"types,omitempty"`
	PathPrefix string   `json:"path_prefix,omitempty"`
	Fuzzy      bool     `json:"fuzzy,omitempty"`
}

// CodeSearchResult contains the results of a code search operation.
type CodeSearchResult struct {
	Documents []ScoredDocument `json:"documents"`
	TotalHits int              `json:"total_hits"`
	Took      time.Duration    `json:"took"`
}

// ScoredDocument represents a document with its search relevance score.
type ScoredDocument struct {
	ID      string  `json:"id"`
	Path    string  `json:"path"`
	Type    string  `json:"type"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
	Line    int     `json:"line,omitempty"`
}

// EnrichedResult extends ScoredDocument with additional context.
type EnrichedResult struct {
	ScoredDocument
	Context     string   `json:"context,omitempty"`
	Symbols     []string `json:"symbols,omitempty"`
	Language    string   `json:"language,omitempty"`
	LineContext []string `json:"line_context,omitempty"`
}

// SearchHandler handles search requests for the Librarian agent.
type SearchHandler struct {
	search SearchSystem
}

// NewSearchHandler creates a new SearchHandler with the given search system.
func NewSearchHandler(search SearchSystem) *SearchHandler {
	return &SearchHandler{search: search}
}

// Handle processes a LibrarianRequest and returns a LibrarianResponse.
func (h *SearchHandler) Handle(ctx context.Context, req *LibrarianRequest) (*LibrarianResponse, error) {
	start := time.Now()

	if err := h.validateRequest(req); err != nil {
		return h.errorResponse(req, err, start), err
	}

	opts := h.extractOptions(req)
	result, err := h.search.Search(ctx, req.Query, opts)
	if err != nil {
		return h.errorResponse(req, errors.Join(ErrSearchFailed, err), start), err
	}

	enriched := h.EnrichResults(result.Documents)

	return h.successResponse(req, enriched, result.TotalHits, start), nil
}

// validateRequest checks that the request is valid for search handling.
func (h *SearchHandler) validateRequest(req *LibrarianRequest) error {
	if req == nil {
		return ErrNilRequest
	}
	if req.Domain != DomainCode {
		return ErrInvalidDomain
	}
	return nil
}

// extractOptions builds SearchOptions from the request parameters.
func (h *SearchHandler) extractOptions(req *LibrarianRequest) SearchOptions {
	opts := SearchOptions{
		Limit: DefaultSearchLimit,
		Fuzzy: false,
	}

	if req.Params == nil {
		return opts
	}

	h.applyLimitParam(&opts, req.Params)
	h.applyStringParams(&opts, req.Params)
	h.applyFuzzyParam(&opts, req.Params)
	h.applyTypesParam(&opts, req.Params)

	return opts
}

// applyLimitParam extracts and applies the limit parameter.
func (h *SearchHandler) applyLimitParam(opts *SearchOptions, params map[string]any) {
	limit := extractIntParam(params, "limit")
	if limit > 0 {
		opts.Limit = min(limit, MaxSearchLimit)
	}
}

// extractIntParam extracts an int from params, handling both int and float64 types.
func extractIntParam(params map[string]any, key string) int {
	if v, ok := params[key].(int); ok {
		return v
	}
	if v, ok := params[key].(float64); ok {
		return int(v)
	}
	return 0
}

// applyStringParams extracts and applies string parameters.
func (h *SearchHandler) applyStringParams(opts *SearchOptions, params map[string]any) {
	if pathPrefix, ok := params["path_prefix"].(string); ok {
		opts.PathPrefix = pathPrefix
	}
}

// applyFuzzyParam extracts and applies the fuzzy parameter.
func (h *SearchHandler) applyFuzzyParam(opts *SearchOptions, params map[string]any) {
	if fuzzy, ok := params["fuzzy"].(bool); ok {
		opts.Fuzzy = fuzzy
	}
}

// applyTypesParam extracts and applies the types parameter.
func (h *SearchHandler) applyTypesParam(opts *SearchOptions, params map[string]any) {
	if types, ok := params["types"].([]string); ok {
		opts.Types = types
	}
	if typesAny, ok := params["types"].([]any); ok {
		opts.Types = convertToStringSlice(typesAny)
	}
}

// convertToStringSlice converts []any to []string, filtering non-strings.
func convertToStringSlice(items []any) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// EnrichResults adds context and metadata to search results.
func (h *SearchHandler) EnrichResults(docs []ScoredDocument) []EnrichedResult {
	if len(docs) == 0 {
		return nil
	}

	enriched := make([]EnrichedResult, 0, len(docs))
	tokenBudget := DefaultTokenLimit

	for _, doc := range docs {
		result := h.enrichSingleDocument(doc, &tokenBudget)
		enriched = append(enriched, result)

		if tokenBudget <= 0 {
			break
		}
	}

	return enriched
}

// enrichSingleDocument enriches a single document with context.
func (h *SearchHandler) enrichSingleDocument(doc ScoredDocument, tokenBudget *int) EnrichedResult {
	result := EnrichedResult{
		ScoredDocument: doc,
		Language:       detectLanguageFromPath(doc.Path),
	}

	result.LineContext = extractLineContext(doc.Content, doc.Line, DefaultContextLines)
	result.Context = buildContextString(result.LineContext)

	estimatedTokens := len(result.Context) / ApproxCharsPerToken
	*tokenBudget -= estimatedTokens

	return result
}

// detectLanguageFromPath infers programming language from file path.
func detectLanguageFromPath(path string) string {
	extMap := map[string]string{
		".go":   "go",
		".py":   "python",
		".js":   "javascript",
		".ts":   "typescript",
		".rs":   "rust",
		".java": "java",
		".c":    "c",
		".cpp":  "cpp",
		".md":   "markdown",
	}

	for ext, lang := range extMap {
		if len(path) > len(ext) && path[len(path)-len(ext):] == ext {
			return lang
		}
	}
	return ""
}

// extractLineContext extracts surrounding lines from content.
func extractLineContext(content string, matchLine int, contextLines int) []string {
	if content == "" || matchLine <= 0 {
		return nil
	}

	lines := splitLines(content)
	start := max(0, matchLine-contextLines-1)
	end := min(len(lines), matchLine+contextLines)

	return lines[start:end]
}

// splitLines splits content into lines.
func splitLines(content string) []string {
	var lines []string
	current := ""
	for _, r := range content {
		if r == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(r)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// buildContextString joins context lines into a single string.
func buildContextString(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

// successResponse creates a successful LibrarianResponse.
func (h *SearchHandler) successResponse(req *LibrarianRequest, data []EnrichedResult, totalHits int, start time.Time) *LibrarianResponse {
	return &LibrarianResponse{
		ID:        uuid.New().String(),
		RequestID: req.ID,
		Success:   true,
		Data: map[string]any{
			"results":    data,
			"total_hits": totalHits,
		},
		Took:      time.Since(start),
		Timestamp: time.Now(),
	}
}

// errorResponse creates an error LibrarianResponse.
func (h *SearchHandler) errorResponse(req *LibrarianRequest, err error, start time.Time) *LibrarianResponse {
	requestID := ""
	if req != nil {
		requestID = req.ID
	}

	return &LibrarianResponse{
		ID:        uuid.New().String(),
		RequestID: requestID,
		Success:   false,
		Error:     err.Error(),
		Took:      time.Since(start),
		Timestamp: time.Now(),
	}
}
