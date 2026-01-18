// Package search provides document search types and utilities for the Sylk search system.
package search

import (
	"errors"
	"time"
)

// FusionMethod represents the algorithm used to combine search results from multiple sources.
type FusionMethod string

const (
	// FusionRRF uses Reciprocal Rank Fusion for combining results.
	FusionRRF FusionMethod = "rrf"
	// FusionLinear uses linear score combination.
	FusionLinear FusionMethod = "linear"
	// FusionMax takes the maximum score from any source.
	FusionMax FusionMethod = "max"
)

// Validation constants for SearchRequest.
const (
	DefaultLimit = 20
	MaxLimit     = 100
	MinFuzzy     = 0
	MaxFuzzy     = 2
)

// Validation errors for SearchRequest.
var (
	ErrEmptyQuery     = errors.New("search query cannot be empty")
	ErrLimitTooHigh   = errors.New("limit exceeds maximum allowed value")
	ErrLimitNegative  = errors.New("limit must be positive")
	ErrOffsetNegative = errors.New("offset cannot be negative")
	ErrFuzzyInvalid   = errors.New("fuzzy level must be between 0 and 2")
)

// ScoredDocument represents a document with its search relevance score and highlighting.
type ScoredDocument struct {
	Document
	Score         float64             `json:"score"`
	Highlights    map[string][]string `json:"highlights,omitempty"`
	MatchedFields []string            `json:"matched_fields,omitempty"`
}

// SearchResult contains the results of a search operation.
type SearchResult struct {
	Documents  []ScoredDocument `json:"documents"`
	TotalHits  int64            `json:"total_hits"`
	SearchTime time.Duration    `json:"search_time"`
	Query      string           `json:"query"`
}

// HasResults returns true if there are any documents in the result.
func (sr *SearchResult) HasResults() bool {
	return len(sr.Documents) > 0
}

// TopN returns the top N documents from the result.
// If n exceeds the number of documents, all documents are returned.
func (sr *SearchResult) TopN(n int) []ScoredDocument {
	if n <= 0 {
		return nil
	}
	if n >= len(sr.Documents) {
		return sr.Documents
	}
	return sr.Documents[:n]
}

// FilterByScore returns documents with a score greater than or equal to minScore.
func (sr *SearchResult) FilterByScore(minScore float64) []ScoredDocument {
	filtered := make([]ScoredDocument, 0, len(sr.Documents))
	for _, doc := range sr.Documents {
		if doc.Score >= minScore {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

// SearchRequest represents parameters for a search operation.
type SearchRequest struct {
	Query             string       `json:"query"`
	Type              DocumentType `json:"type,omitempty"`
	PathFilter        string       `json:"path_filter,omitempty"`
	Limit             int          `json:"limit,omitempty"`
	Offset            int          `json:"offset,omitempty"`
	FuzzyLevel        int          `json:"fuzzy_level,omitempty"`
	IncludeHighlights bool         `json:"include_highlights,omitempty"`
}

// Validate checks that the SearchRequest has valid parameters.
// Returns nil if valid, or an error describing the validation failure.
func (r *SearchRequest) Validate() error {
	if r.Query == "" {
		return ErrEmptyQuery
	}
	return r.validateNumericFields()
}

func (r *SearchRequest) validateNumericFields() error {
	if r.Limit < 0 {
		return ErrLimitNegative
	}
	if r.Limit > MaxLimit {
		return ErrLimitTooHigh
	}
	if r.Offset < 0 {
		return ErrOffsetNegative
	}
	if r.FuzzyLevel < MinFuzzy || r.FuzzyLevel > MaxFuzzy {
		return ErrFuzzyInvalid
	}
	return nil
}

// Normalize applies defaults to unset fields.
func (r *SearchRequest) Normalize() {
	if r.Limit == 0 {
		r.Limit = DefaultLimit
	}
}

// ValidateAndNormalize validates and normalizes the request in one call.
func (r *SearchRequest) ValidateAndNormalize() error {
	if err := r.Validate(); err != nil {
		return err
	}
	r.Normalize()
	return nil
}

// HybridSearchResult contains results from combined search sources.
type HybridSearchResult struct {
	BleveResults  []ScoredDocument `json:"bleve_results,omitempty"`
	VectorResults []ScoredDocument `json:"vector_results,omitempty"`
	FusedResults  []ScoredDocument `json:"fused_results"`
	FusionMethod  FusionMethod     `json:"fusion_method"`
}

// HasResults returns true if there are any fused results.
func (hr *HybridSearchResult) HasResults() bool {
	return len(hr.FusedResults) > 0
}

// TopN returns the top N fused documents from the hybrid result.
func (hr *HybridSearchResult) TopN(n int) []ScoredDocument {
	if n <= 0 {
		return nil
	}
	if n >= len(hr.FusedResults) {
		return hr.FusedResults
	}
	return hr.FusedResults[:n]
}

// FilterByScore returns fused documents with a score >= minScore.
func (hr *HybridSearchResult) FilterByScore(minScore float64) []ScoredDocument {
	filtered := make([]ScoredDocument, 0, len(hr.FusedResults))
	for _, doc := range hr.FusedResults {
		if doc.Score >= minScore {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

// TotalSources returns the count of result sources that returned documents.
func (hr *HybridSearchResult) TotalSources() int {
	count := 0
	if len(hr.BleveResults) > 0 {
		count++
	}
	if len(hr.VectorResults) > 0 {
		count++
	}
	return count
}
