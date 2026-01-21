// Package vectorgraphdb provides Triple RRF (Reciprocal Rank Fusion) for hybrid search.
// This implementation combines vector similarity, full-text search, and graph traversal
// signals into a unified ranking using weighted RRF.
package vectorgraphdb

import (
	"sort"
	"sync"
)

// =============================================================================
// Triple RRF Result Types
// =============================================================================

// VectorResult represents a result from vector similarity search.
type VectorResult struct {
	ID         string
	Similarity float64
}

// TextResult represents a result from full-text search.
type TextResult struct {
	ID    string
	Score float64
}

// GraphResult represents a result from graph traversal.
type GraphResult struct {
	ID       string
	Distance int
	Relation string
}

// FusedResult represents a result after RRF fusion.
type FusedResult struct {
	ID    string
	Score float64
}

// =============================================================================
// Triple RRF Fusion
// =============================================================================

// TripleRRFFusion combines vector similarity, full-text search, and graph traversal
// results using Reciprocal Rank Fusion with configurable weights.
//
// The RRF formula is: score(d) = Σ (weight_i / (k + rank_i))
// where k is a constant (typically 60) that mitigates the impact of high rankings.
type TripleRRFFusion struct {
	mu           sync.RWMutex
	k            int
	vectorWeight float64
	textWeight   float64
	graphWeight  float64
}

// NewTripleRRFFusion creates a new TripleRRFFusion with the given parameters.
// Weights are normalized so they sum to 1.0.
// If k <= 0, it defaults to 60 (standard RRF constant).
// If all weights are <= 0, they default to equal weights (1/3 each).
func NewTripleRRFFusion(k int, vectorWeight, textWeight, graphWeight float64) *TripleRRFFusion {
	if k <= 0 {
		k = 60
	}

	total := vectorWeight + textWeight + graphWeight
	if total <= 0 {
		vectorWeight, textWeight, graphWeight = 1.0, 1.0, 1.0
		total = 3.0
	}

	return &TripleRRFFusion{
		k:            k,
		vectorWeight: vectorWeight / total,
		textWeight:   textWeight / total,
		graphWeight:  graphWeight / total,
	}
}

// Fuse combines results from all three sources using weighted RRF.
// Results are sorted by descending score, with ID as tie-breaker for stability.
func (t *TripleRRFFusion) Fuse(vectorResults []VectorResult, textResults []TextResult, graphResults []GraphResult) []FusedResult {
	t.mu.RLock()
	defer t.mu.RUnlock()

	scores := make(map[string]float64)

	// Vector similarity contribution
	for rank, r := range vectorResults {
		scores[r.ID] += t.vectorWeight / float64(rank+t.k)
	}

	// Full-text search contribution
	for rank, r := range textResults {
		scores[r.ID] += t.textWeight / float64(rank+t.k)
	}

	// Graph traversal contribution
	for rank, r := range graphResults {
		scores[r.ID] += t.graphWeight / float64(rank+t.k)
	}

	// Convert to slice
	results := make([]FusedResult, 0, len(scores))
	for id, score := range scores {
		results = append(results, FusedResult{ID: id, Score: score})
	}

	// Sort by descending score, then by ID for deterministic ordering
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})

	return results
}

// FuseTopK is like Fuse but returns only the top K results.
// If topK <= 0 or topK >= len(results), all results are returned.
func (t *TripleRRFFusion) FuseTopK(vectorResults []VectorResult, textResults []TextResult, graphResults []GraphResult, topK int) []FusedResult {
	results := t.Fuse(vectorResults, textResults, graphResults)
	if topK > 0 && topK < len(results) {
		return results[:topK]
	}
	return results
}

// SetWeights updates the fusion weights. Weights are normalized to sum to 1.0.
// If all weights are <= 0, the weights are not changed.
func (t *TripleRRFFusion) SetWeights(vectorWeight, textWeight, graphWeight float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	total := vectorWeight + textWeight + graphWeight
	if total > 0 {
		t.vectorWeight = vectorWeight / total
		t.textWeight = textWeight / total
		t.graphWeight = graphWeight / total
	}
}

// GetWeights returns the current normalized weights.
func (t *TripleRRFFusion) GetWeights() (vector, text, graph float64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.vectorWeight, t.textWeight, t.graphWeight
}

// DeriveWeightsFromPerformance adjusts weights based on observed recall performance.
// This allows adaptive weight tuning: sources with better recall get higher weights.
// If total recall is <= 0, weights are not changed.
func (t *TripleRRFFusion) DeriveWeightsFromPerformance(vectorRecall, textRecall, graphRecall float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	total := vectorRecall + textRecall + graphRecall
	if total <= 0 {
		return
	}

	t.vectorWeight = vectorRecall / total
	t.textWeight = textRecall / total
	t.graphWeight = graphRecall / total
}

// GetK returns the RRF constant k.
func (t *TripleRRFFusion) GetK() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.k
}
