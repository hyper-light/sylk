// Package query provides hybrid query execution for the knowledge system.
// RRFFusion implements Reciprocal Rank Fusion for combining multi-source results.
package query

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// =============================================================================
// Query Weights
// =============================================================================

// QueryWeights defines the weighting scheme for combining different search sources.
// Weights should sum to approximately 1.0 for proper normalization.
type QueryWeights struct {
	// TextWeight is the weight applied to text search (Bleve) results.
	TextWeight float64 `json:"text_weight"`

	// SemanticWeight is the weight applied to semantic search (HNSW) results.
	SemanticWeight float64 `json:"semantic_weight"`

	// GraphWeight is the weight applied to graph traversal results.
	GraphWeight float64 `json:"graph_weight"`

	// MemoryWeight is the optional weight for memory activation scores.
	// Only used in FuseWithMemory.
	MemoryWeight float64 `json:"memory_weight,omitempty"`
}

// DefaultQueryWeights returns balanced weights for all three sources.
func DefaultQueryWeights() *QueryWeights {
	return &QueryWeights{
		TextWeight:     0.33,
		SemanticWeight: 0.34,
		GraphWeight:    0.33,
		MemoryWeight:   0.0,
	}
}

// DefaultQueryWeightsWithMemory returns balanced weights including memory.
func DefaultQueryWeightsWithMemory() *QueryWeights {
	return &QueryWeights{
		TextWeight:     0.25,
		SemanticWeight: 0.25,
		GraphWeight:    0.25,
		MemoryWeight:   0.25,
	}
}

// Validate checks if the weights are valid and approximately sum to 1.0.
// Returns an error if weights are invalid.
func (qw *QueryWeights) Validate() error {
	if qw.TextWeight < 0 || qw.TextWeight > 1 {
		return fmt.Errorf("text weight must be in [0, 1], got %f", qw.TextWeight)
	}
	if qw.SemanticWeight < 0 || qw.SemanticWeight > 1 {
		return fmt.Errorf("semantic weight must be in [0, 1], got %f", qw.SemanticWeight)
	}
	if qw.GraphWeight < 0 || qw.GraphWeight > 1 {
		return fmt.Errorf("graph weight must be in [0, 1], got %f", qw.GraphWeight)
	}
	if qw.MemoryWeight < 0 || qw.MemoryWeight > 1 {
		return fmt.Errorf("memory weight must be in [0, 1], got %f", qw.MemoryWeight)
	}

	sum := qw.TextWeight + qw.SemanticWeight + qw.GraphWeight + qw.MemoryWeight
	if sum < 0.99 || sum > 1.01 {
		return fmt.Errorf("weights should sum to ~1.0, got %f", sum)
	}

	return nil
}

// Normalize adjusts weights to sum to exactly 1.0.
// Returns a new QueryWeights instance with normalized values.
func (qw *QueryWeights) Normalize() *QueryWeights {
	sum := qw.TextWeight + qw.SemanticWeight + qw.GraphWeight + qw.MemoryWeight
	if sum == 0 {
		// Avoid division by zero; return equal weights
		return DefaultQueryWeights()
	}

	return &QueryWeights{
		TextWeight:     qw.TextWeight / sum,
		SemanticWeight: qw.SemanticWeight / sum,
		GraphWeight:    qw.GraphWeight / sum,
		MemoryWeight:   qw.MemoryWeight / sum,
	}
}

// Clone creates a copy of the QueryWeights.
func (qw *QueryWeights) Clone() *QueryWeights {
	return &QueryWeights{
		TextWeight:     qw.TextWeight,
		SemanticWeight: qw.SemanticWeight,
		GraphWeight:    qw.GraphWeight,
		MemoryWeight:   qw.MemoryWeight,
	}
}

// IsZero returns true if all weights are zero.
func (qw *QueryWeights) IsZero() bool {
	return qw.TextWeight == 0 && qw.SemanticWeight == 0 &&
		qw.GraphWeight == 0 && qw.MemoryWeight == 0
}

// =============================================================================
// RRF Fusion
// =============================================================================

// RRFFusion implements Reciprocal Rank Fusion for combining results from
// multiple search sources. RRF is a rank aggregation method that is robust
// to outliers and works well without score calibration across sources.
//
// The RRF formula for a document d is:
//
//	RRF(d) = Σ weight_i / (k + rank_i(d))
//
// where k is a constant (default 60) that dampens the effect of high rankings.
type RRFFusion struct {
	// k is the RRF parameter that controls ranking dampening.
	// Higher values make the fusion less sensitive to exact rankings.
	// Default is 60 (standard RRF parameter from research).
	k int

	// mu protects concurrent access during fusion operations.
	mu sync.Mutex
}

// NewRRFFusion creates a new RRFFusion with the specified k parameter.
// If k <= 0, the default value of 60 is used.
func NewRRFFusion(k int) *RRFFusion {
	if k <= 0 {
		k = 60
	}
	return &RRFFusion{
		k: k,
	}
}

// DefaultRRFFusion creates an RRFFusion with standard parameters.
func DefaultRRFFusion() *RRFFusion {
	return NewRRFFusion(60)
}

// K returns the current k parameter value.
func (rrf *RRFFusion) K() int {
	return rrf.k
}

// SetK updates the k parameter. If k <= 0, it defaults to 60.
func (rrf *RRFFusion) SetK(k int) {
	rrf.mu.Lock()
	defer rrf.mu.Unlock()

	if k <= 0 {
		k = 60
	}
	rrf.k = k
}

// fusionEntry tracks aggregated scores for a single document during fusion.
type fusionEntry struct {
	id            string
	content       string
	textScore     float64
	semanticScore float64
	graphScore    float64
	memoryScore   float64
	rrfScore      float64
	matchedEdges  []EdgeMatch
	traversalPath []string
}

// Fuse combines results from text, semantic, and graph searches using RRF.
// Empty result slices are handled gracefully - they simply don't contribute
// to the final scores. Results are deduplicated by ID with scores combined.
//
// Returns results sorted by descending RRF score.
func (rrf *RRFFusion) Fuse(
	text []TextResult,
	semantic []VectorResult,
	graph []GraphResult,
	weights *QueryWeights,
) []HybridResult {
	return rrf.FuseWithMemory(text, semantic, graph, weights, nil)
}

// FuseWithMemory combines results from all sources including memory activation.
// memoryScores is a map from document ID to memory activation score.
// If memoryScores is nil or empty, it behaves like Fuse.
//
// Returns results sorted by descending RRF score.
func (rrf *RRFFusion) FuseWithMemory(
	text []TextResult,
	semantic []VectorResult,
	graph []GraphResult,
	weights *QueryWeights,
	memoryScores map[string]float64,
) []HybridResult {
	rrf.mu.Lock()
	defer rrf.mu.Unlock()

	// Use default weights if not provided
	if weights == nil {
		if len(memoryScores) > 0 {
			weights = DefaultQueryWeightsWithMemory()
		} else {
			weights = DefaultQueryWeights()
		}
	}

	// Normalize weights to ensure proper combination
	weights = weights.Normalize()

	// Aggregate entries by ID
	entries := make(map[string]*fusionEntry)

	// Process text results
	rrf.processTextResults(entries, text, weights.TextWeight)

	// Process semantic results
	rrf.processSemanticResults(entries, semantic, weights.SemanticWeight)

	// Process graph results
	rrf.processGraphResults(entries, graph, weights.GraphWeight)

	// Process memory scores
	rrf.processMemoryScores(entries, memoryScores, weights.MemoryWeight)

	// Convert to HybridResults and sort
	return rrf.finalizeResults(entries)
}

// processTextResults adds RRF contributions from text search results.
func (rrf *RRFFusion) processTextResults(
	entries map[string]*fusionEntry,
	results []TextResult,
	weight float64,
) {
	if weight == 0 || len(results) == 0 {
		return
	}

	for rank, result := range results {
		entry := rrf.getOrCreateEntry(entries, result.ID)
		entry.content = result.Content
		entry.textScore = result.Score

		// RRF contribution: weight / (k + rank + 1)
		// rank is 0-indexed, so we add 1 to make it 1-indexed
		rrfContribution := weight / float64(rrf.k+rank+1)
		entry.rrfScore += rrfContribution
	}
}

// processSemanticResults adds RRF contributions from vector search results.
func (rrf *RRFFusion) processSemanticResults(
	entries map[string]*fusionEntry,
	results []VectorResult,
	weight float64,
) {
	if weight == 0 || len(results) == 0 {
		return
	}

	for rank, result := range results {
		entry := rrf.getOrCreateEntry(entries, result.ID)
		entry.semanticScore = result.Score

		// RRF contribution
		rrfContribution := weight / float64(rrf.k+rank+1)
		entry.rrfScore += rrfContribution
	}
}

// processGraphResults adds RRF contributions from graph traversal results.
func (rrf *RRFFusion) processGraphResults(
	entries map[string]*fusionEntry,
	results []GraphResult,
	weight float64,
) {
	if weight == 0 || len(results) == 0 {
		return
	}

	for rank, result := range results {
		entry := rrf.getOrCreateEntry(entries, result.ID)
		entry.graphScore = result.Score
		entry.matchedEdges = result.MatchedEdges
		entry.traversalPath = result.Path

		// RRF contribution
		rrfContribution := weight / float64(rrf.k+rank+1)
		entry.rrfScore += rrfContribution
	}
}

// processMemoryScores adds memory activation contributions.
// Memory scores are not ranked; they're added directly with weight normalization.
func (rrf *RRFFusion) processMemoryScores(
	entries map[string]*fusionEntry,
	memoryScores map[string]float64,
	weight float64,
) {
	if weight == 0 || len(memoryScores) == 0 {
		return
	}

	// Convert memory scores to rankings for RRF
	type memoryEntry struct {
		id    string
		score float64
	}

	memEntries := make([]memoryEntry, 0, len(memoryScores))
	for id, score := range memoryScores {
		memEntries = append(memEntries, memoryEntry{id: id, score: score})
	}

	// Sort by score descending to get rankings
	sort.Slice(memEntries, func(i, j int) bool {
		return memEntries[i].score > memEntries[j].score
	})

	for rank, me := range memEntries {
		entry := rrf.getOrCreateEntry(entries, me.id)
		entry.memoryScore = me.score

		// RRF contribution
		rrfContribution := weight / float64(rrf.k+rank+1)
		entry.rrfScore += rrfContribution
	}
}

// getOrCreateEntry retrieves or creates a fusion entry for the given ID.
func (rrf *RRFFusion) getOrCreateEntry(entries map[string]*fusionEntry, id string) *fusionEntry {
	entry, exists := entries[id]
	if !exists {
		entry = &fusionEntry{id: id}
		entries[id] = entry
	}
	return entry
}

// finalizeResults converts fusion entries to HybridResults and sorts them.
func (rrf *RRFFusion) finalizeResults(entries map[string]*fusionEntry) []HybridResult {
	results := make([]HybridResult, 0, len(entries))

	for _, entry := range entries {
		// Determine source based on which scores contributed
		source := rrf.determineSource(entry)

		result := HybridResult{
			ID:            entry.id,
			Content:       entry.content,
			Score:         entry.rrfScore,
			TextScore:     entry.textScore,
			SemanticScore: entry.semanticScore,
			GraphScore:    entry.graphScore,
			MatchedEdges:  entry.matchedEdges,
			TraversalPath: entry.traversalPath,
			Source:        source,
		}

		results = append(results, result)
	}

	// Sort by RRF score descending
	sort.Slice(results, func(i, j int) bool {
		if math.Abs(results[i].Score-results[j].Score) < 1e-9 {
			// Tie-breaker: prefer results with more source contributions
			iCount := rrf.countSources(&results[i])
			jCount := rrf.countSources(&results[j])
			return iCount > jCount
		}
		return results[i].Score > results[j].Score
	})

	return results
}

// determineSource identifies the primary source for a result.
func (rrf *RRFFusion) determineSource(entry *fusionEntry) Source {
	hasText := entry.textScore > 0
	hasSemantic := entry.semanticScore > 0
	hasGraph := entry.graphScore > 0

	count := 0
	if hasText {
		count++
	}
	if hasSemantic {
		count++
	}
	if hasGraph {
		count++
	}

	if count > 1 {
		return SourceCombined
	}
	if hasText {
		return SourceBleve
	}
	if hasSemantic {
		return SourceHNSW
	}
	if hasGraph {
		return SourceGraph
	}

	return SourceCombined
}

// countSources counts how many search sources contributed to a result.
func (rrf *RRFFusion) countSources(result *HybridResult) int {
	count := 0
	if result.TextScore > 0 {
		count++
	}
	if result.SemanticScore > 0 {
		count++
	}
	if result.GraphScore > 0 {
		count++
	}
	return count
}

// =============================================================================
// Batch Operations
// =============================================================================

// FuseBatch performs RRF fusion on multiple query results in parallel.
// Useful for batch processing of multiple queries.
func (rrf *RRFFusion) FuseBatch(
	queries []FusionQuery,
	weights *QueryWeights,
) [][]HybridResult {
	results := make([][]HybridResult, len(queries))

	var wg sync.WaitGroup
	for i, query := range queries {
		wg.Add(1)
		go func(idx int, q FusionQuery) {
			defer wg.Done()
			results[idx] = rrf.Fuse(q.Text, q.Semantic, q.Graph, weights)
		}(i, query)
	}
	wg.Wait()

	return results
}

// FusionQuery represents input for batch fusion operations.
type FusionQuery struct {
	Text     []TextResult
	Semantic []VectorResult
	Graph    []GraphResult
}
