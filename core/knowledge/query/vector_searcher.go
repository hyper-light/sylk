// Package query provides hybrid query execution for the knowledge system.
// VectorSearcher implements semantic similarity search using vector index.
package query

import (
	"context"
)

// =============================================================================
// Vector Result Type
// =============================================================================

// VectorResult represents a single result from a vector similarity search.
type VectorResult struct {
	// ID is the unique identifier of the matched node.
	ID string `json:"id"`

	// Score is the similarity score (higher is more similar).
	Score float64 `json:"score"`

	// Distance is the raw distance from the vector search (lower is closer).
	Distance float32 `json:"distance"`
}

// =============================================================================
// Vector Index Interface
// =============================================================================

// VectorIndex defines the interface for vector search operations.
// This abstraction allows for testing with mock implementations.
type VectorIndex interface {
	// Search performs a k-nearest neighbors search on the vector index.
	// Returns slice of IDs and their corresponding distances.
	Search(vector []float32, k int) (ids []string, distances []float32, err error)
}

// HNSWIndex is an alias for VectorIndex for backward compatibility.
// Deprecated: Use VectorIndex instead.
type HNSWIndex = VectorIndex

// =============================================================================
// Vector Searcher
// =============================================================================

// VectorSearcher provides semantic similarity search using vector index.
// It implements graceful degradation by returning empty results on failure.
type VectorSearcher struct {
	vectorIndex VectorIndex
}

// NewVectorSearcher creates a new VectorSearcher with the provided vector index.
func NewVectorSearcher(vectorIndex VectorIndex) *VectorSearcher {
	return &VectorSearcher{
		vectorIndex: vectorIndex,
	}
}

// Execute performs a vector similarity search with the given query vector and limit.
// Returns empty results on failure rather than propagating errors (graceful degradation).
// Respects context cancellation.
func (vs *VectorSearcher) Execute(ctx context.Context, vector []float32, limit int) ([]VectorResult, error) {
	if vs.vectorIndex == nil {
		return []VectorResult{}, nil
	}

	if len(vector) == 0 {
		return []VectorResult{}, nil
	}

	if limit <= 0 {
		limit = 10
	}

	// Check context before executing
	select {
	case <-ctx.Done():
		return []VectorResult{}, ctx.Err()
	default:
	}

	return vs.executeSearch(ctx, vector, limit)
}

// executeSearch performs the actual vector index search operation.
func (vs *VectorSearcher) executeSearch(ctx context.Context, vector []float32, limit int) ([]VectorResult, error) {
	// Execute the search
	ids, distances, err := vs.vectorIndex.Search(vector, limit)
	if err != nil {
		// Graceful degradation: return empty results on error
		return []VectorResult{}, nil
	}

	// Check context after search
	select {
	case <-ctx.Done():
		return []VectorResult{}, ctx.Err()
	default:
	}

	return vs.convertResults(ids, distances), nil
}

// convertResults transforms vector search results into VectorResult slice.
func (vs *VectorSearcher) convertResults(ids []string, distances []float32) []VectorResult {
	if len(ids) == 0 {
		return []VectorResult{}
	}

	results := make([]VectorResult, 0, len(ids))
	for i, id := range ids {
		distance := float32(0)
		if i < len(distances) {
			distance = distances[i]
		}

		result := VectorResult{
			ID:       id,
			Distance: distance,
			Score:    vs.distanceToScore(distance),
		}
		results = append(results, result)
	}
	return results
}

// distanceToScore converts a distance value to a similarity score.
// Uses 1 / (1 + distance) transformation so smaller distances yield higher scores.
func (vs *VectorSearcher) distanceToScore(distance float32) float64 {
	if distance < 0 {
		distance = 0
	}
	return 1.0 / (1.0 + float64(distance))
}

// IsReady returns true if the searcher has a valid vector index.
func (vs *VectorSearcher) IsReady() bool {
	return vs.vectorIndex != nil
}
