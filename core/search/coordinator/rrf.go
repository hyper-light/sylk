// Package coordinator provides search result coordination and fusion algorithms
// for combining results from multiple search backends in the Sylk search system.
package coordinator

import (
	"sort"
	"sync"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// RRFMerger
// =============================================================================

// RRFMerger implements Reciprocal Rank Fusion for combining ranked result lists.
// RRF assigns scores based on rank position: score = sum(1/(k + rank_i)) for each list.
// Thread-safe for concurrent use.
type RRFMerger struct {
	mu sync.RWMutex
	k  int
}

// NewRRFMerger creates a new RRFMerger with the default k value (60).
func NewRRFMerger() *RRFMerger {
	return &RRFMerger{k: DefaultRRFK}
}

// NewRRFMergerWithK creates a new RRFMerger with a custom k value.
// If k is less than 1, it defaults to DefaultRRFK.
func NewRRFMergerWithK(k int) *RRFMerger {
	if k < 1 {
		k = DefaultRRFK
	}
	return &RRFMerger{k: k}
}

// K returns the current k value used for RRF scoring.
func (m *RRFMerger) K() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.k
}

// SetK updates the k value used for RRF scoring.
// If k is less than 1, it defaults to DefaultRRFK.
func (m *RRFMerger) SetK(k int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k < 1 {
		k = DefaultRRFK
	}
	m.k = k
}

// Merge combines two ranked result lists using Reciprocal Rank Fusion.
// Documents are deduplicated by ID. The RRF score is calculated as:
// score = sum(1/(k + rank)) for each list where the document appears.
// Returns results sorted by descending RRF score.
func (m *RRFMerger) Merge(results1, results2 []search.ScoredDocument) []search.ScoredDocument {
	m.mu.RLock()
	k := m.k
	m.mu.RUnlock()

	return m.mergeWithK(results1, results2, k)
}

// MergeWithK combines two ranked result lists using RRF with a specific k value.
// This allows overriding the merger's default k for a single operation.
func (m *RRFMerger) MergeWithK(results1, results2 []search.ScoredDocument, k int) []search.ScoredDocument {
	if k < 1 {
		k = DefaultRRFK
	}
	return m.mergeWithK(results1, results2, k)
}

// =============================================================================
// Internal Methods
// =============================================================================

// mergeWithK performs the actual RRF merge operation.
func (m *RRFMerger) mergeWithK(results1, results2 []search.ScoredDocument, k int) []search.ScoredDocument {
	// Handle empty inputs
	if len(results1) == 0 && len(results2) == 0 {
		return nil
	}

	// Build score map and document map
	scoreMap := make(map[string]float64)
	docMap := make(map[string]search.ScoredDocument)

	m.addRRFScores(results1, k, scoreMap, docMap)
	m.addRRFScores(results2, k, scoreMap, docMap)

	return m.buildSortedResults(scoreMap, docMap)
}

// addRRFScores adds RRF scores for documents in a result list.
func (m *RRFMerger) addRRFScores(
	results []search.ScoredDocument,
	k int,
	scoreMap map[string]float64,
	docMap map[string]search.ScoredDocument,
) {
	for rank, doc := range results {
		rrfScore := 1.0 / float64(k+rank+1) // rank is 0-indexed, formula uses 1-indexed
		scoreMap[doc.ID] += rrfScore
		if _, exists := docMap[doc.ID]; !exists {
			docMap[doc.ID] = doc
		}
	}
}

// buildSortedResults creates the final sorted result slice.
func (m *RRFMerger) buildSortedResults(
	scoreMap map[string]float64,
	docMap map[string]search.ScoredDocument,
) []search.ScoredDocument {
	results := make([]search.ScoredDocument, 0, len(scoreMap))
	for id, score := range scoreMap {
		doc := docMap[id]
		doc.Score = score
		results = append(results, doc)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}
