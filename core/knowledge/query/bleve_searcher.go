// Package query provides hybrid query execution for the knowledge system.
// BleveSearcher implements full-text search using the Bleve search engine.
package query

import (
	"context"

	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/highlight/highlighter/ansi"
)

// =============================================================================
// Text Result Type
// =============================================================================

// TextResult represents a single result from a text search operation.
type TextResult struct {
	// ID is the unique identifier of the matched document.
	ID string `json:"id"`

	// Score is the relevance score from the text search.
	Score float64 `json:"score"`

	// Content is the matched document content.
	Content string `json:"content"`

	// Highlights contains highlighted fragments from matched fields.
	Highlights []string `json:"highlights,omitempty"`
}

// =============================================================================
// Bleve Index Interface
// =============================================================================

// BleveIndex defines the interface for Bleve index operations.
// This abstraction allows for testing with mock implementations.
type BleveIndex interface {
	SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error)
}

// =============================================================================
// Bleve Searcher
// =============================================================================

// BleveSearcher provides full-text search capabilities using Bleve.
// It implements graceful degradation by returning empty results on failure.
type BleveSearcher struct {
	index BleveIndex
}

// NewBleveSearcher creates a new BleveSearcher with the provided Bleve index.
// The index parameter should implement the BleveIndex interface.
func NewBleveSearcher(index BleveIndex) *BleveSearcher {
	return &BleveSearcher{
		index: index,
	}
}

// Execute performs a full-text search with the given query and limit.
// Returns empty results on failure rather than propagating errors (graceful degradation).
// Respects context cancellation and timeout.
func (bs *BleveSearcher) Execute(ctx context.Context, textQuery string, limit int) ([]TextResult, error) {
	if bs.index == nil {
		return []TextResult{}, nil
	}

	if textQuery == "" {
		return []TextResult{}, nil
	}

	if limit <= 0 {
		limit = 10
	}

	// Check context before executing
	select {
	case <-ctx.Done():
		return []TextResult{}, ctx.Err()
	default:
	}

	return bs.executeSearch(ctx, textQuery, limit)
}

// executeSearch performs the actual Bleve search operation.
func (bs *BleveSearcher) executeSearch(ctx context.Context, textQuery string, limit int) ([]TextResult, error) {
	bleveReq := bs.buildRequest(textQuery, limit)

	bleveResult, err := bs.index.SearchInContext(ctx, bleveReq)
	if err != nil {
		// Graceful degradation: return empty results on error
		return []TextResult{}, nil
	}

	return bs.convertResults(bleveResult), nil
}

// buildRequest constructs a Bleve SearchRequest from the query parameters.
func (bs *BleveSearcher) buildRequest(textQuery string, limit int) *bleve.SearchRequest {
	query := bleve.NewQueryStringQuery(textQuery)
	req := bleve.NewSearchRequestOptions(query, limit, 0, false)
	req.Fields = []string{"*"}
	req.Highlight = bleve.NewHighlightWithStyle(ansi.Name)
	return req
}

// convertResults transforms Bleve search results into TextResult slice.
func (bs *BleveSearcher) convertResults(bleveResult *bleve.SearchResult) []TextResult {
	if bleveResult == nil || len(bleveResult.Hits) == 0 {
		return []TextResult{}
	}

	results := make([]TextResult, 0, len(bleveResult.Hits))
	for _, hit := range bleveResult.Hits {
		result := bs.convertHit(hit)
		results = append(results, result)
	}
	return results
}

// convertHit transforms a single Bleve DocumentMatch into a TextResult.
func (bs *BleveSearcher) convertHit(hit *blevesearch.DocumentMatch) TextResult {
	result := TextResult{
		ID:    hit.ID,
		Score: hit.Score,
	}

	// Extract content from fields
	if content, ok := hit.Fields["content"].(string); ok {
		result.Content = content
	}

	// Extract highlights from fragments
	result.Highlights = bs.extractHighlights(hit.Fragments)

	return result
}

// extractHighlights flattens the fragment map into a single slice of highlights.
func (bs *BleveSearcher) extractHighlights(fragments map[string][]string) []string {
	if len(fragments) == 0 {
		return nil
	}

	var highlights []string
	for _, fieldHighlights := range fragments {
		highlights = append(highlights, fieldHighlights...)
	}
	return highlights
}

// IsReady returns true if the searcher has a valid index.
func (bs *BleveSearcher) IsReady() bool {
	return bs.index != nil
}
