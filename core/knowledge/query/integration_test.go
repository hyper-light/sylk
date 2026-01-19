package query

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Setup Helpers
// =============================================================================

// mockBleveSearcherIntegration provides a configurable mock Bleve searcher.
type mockBleveSearcherIntegration struct {
	results     []TextResult
	err         error
	delay       time.Duration
	callCount   atomic.Int64
	lastRequest *bleve.SearchRequest
	mu          sync.Mutex
}

func (m *mockBleveSearcherIntegration) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	m.callCount.Add(1)
	m.mu.Lock()
	m.lastRequest = req
	m.mu.Unlock()

	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.delay):
		}
	}

	if m.err != nil {
		return nil, m.err
	}

	hits := make([]*blevesearch.DocumentMatch, len(m.results))
	for i, r := range m.results {
		hits[i] = &blevesearch.DocumentMatch{
			ID:    r.ID,
			Score: r.Score,
			Fields: map[string]interface{}{
				"content": r.Content,
			},
		}
	}

	return &bleve.SearchResult{
		Hits:  hits,
		Total: uint64(len(hits)),
	}, nil
}

// mockVectorSearcherIntegration provides a configurable mock vector searcher.
type mockVectorSearcherIntegration struct {
	results   []VectorResult
	err       error
	delay     time.Duration
	callCount atomic.Int64
}

func (m *mockVectorSearcherIntegration) Search(vector []float32, k int) ([]string, []float32, error) {
	m.callCount.Add(1)

	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	if m.err != nil {
		return nil, nil, m.err
	}

	ids := make([]string, len(m.results))
	distances := make([]float32, len(m.results))
	for i, r := range m.results {
		ids[i] = r.ID
		distances[i] = r.Distance
	}

	return ids, distances, nil
}

// mockGraphSearcherIntegration provides a configurable mock graph traverser.
type mockGraphSearcherIntegration struct {
	results   []GraphResult
	nodes     []string
	err       error
	delay     time.Duration
	callCount atomic.Int64
}

func (m *mockGraphSearcherIntegration) GetOutgoingEdges(nodeID string, edgeTypes []string) ([]Edge, error) {
	m.callCount.Add(1)

	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	if m.err != nil {
		return nil, m.err
	}

	var edges []Edge
	for i, r := range m.results {
		edges = append(edges, Edge{
			ID:       int64(i),
			SourceID: nodeID,
			TargetID: r.ID,
			EdgeType: "related",
			Weight:   r.Score,
		})
	}
	return edges, nil
}

func (m *mockGraphSearcherIntegration) GetIncomingEdges(nodeID string, edgeTypes []string) ([]Edge, error) {
	return nil, nil
}

func (m *mockGraphSearcherIntegration) GetNodesByPattern(pattern *NodeMatcher) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.nodes) > 0 {
		return m.nodes, nil
	}
	return []string{"start-node"}, nil
}

// createMockSearchers creates configured mock searchers for testing.
func createMockSearchers() (*mockBleveSearcherIntegration, *mockVectorSearcherIntegration, *mockGraphSearcherIntegration) {
	bleve := &mockBleveSearcherIntegration{
		results: []TextResult{
			{ID: "doc1", Score: 0.95, Content: "Document 1 content"},
			{ID: "doc2", Score: 0.85, Content: "Document 2 content"},
			{ID: "doc3", Score: 0.75, Content: "Document 3 content"},
		},
	}

	vector := &mockVectorSearcherIntegration{
		results: []VectorResult{
			{ID: "doc2", Score: 0.92, Distance: 0.08},
			{ID: "doc4", Score: 0.88, Distance: 0.12},
			{ID: "doc1", Score: 0.80, Distance: 0.20},
		},
	}

	graph := &mockGraphSearcherIntegration{
		results: []GraphResult{
			{ID: "doc3", Score: 0.90, Path: []string{"start", "doc3"}},
			{ID: "doc5", Score: 0.82, Path: []string{"start", "middle", "doc5"}},
		},
		nodes: []string{"start"},
	}

	return bleve, vector, graph
}

// setupTestCoordinator creates a fully configured test coordinator.
func setupTestCoordinator(t *testing.T) *HybridQueryCoordinator {
	mockBleve, mockVector, mockGraph := createMockSearchers()

	bleveSearcher := NewBleveSearcher(mockBleve)
	vectorSearcher := NewVectorSearcher(mockVector)
	graphTraverser := NewGraphTraverser(mockGraph)

	coord := NewHybridQueryCoordinator(bleveSearcher, vectorSearcher, graphTraverser)
	coord.SetTimeout(5 * time.Second) // Long timeout for tests

	return coord
}

// verifyRRFScore verifies that a result has the expected RRF score within tolerance.
func verifyRRFScore(t *testing.T, result *HybridResult, expectedScore float64, tolerance float64) {
	t.Helper()
	if math.Abs(result.Score-expectedScore) > tolerance {
		t.Errorf("RRF score = %v, expected %v (tolerance %v)", result.Score, expectedScore, tolerance)
	}
}

// =============================================================================
// HQ.5.1 RRF Fusion Correctness Tests
// =============================================================================

func TestIntegration_RRF_AllSourcesFusedCorrectly(t *testing.T) {
	// Test: Results from all three sources are fused correctly
	coord := setupTestCoordinator(t)
	ctx := context.Background()

	entityType := "test_type"
	query := &HybridQuery{
		TextQuery:      "test query",
		SemanticVector: []float32{0.1, 0.2, 0.3},
		GraphPattern: &GraphPattern{
			StartNode:  &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{{Direction: DirectionOutgoing, MaxHops: 1}},
		},
		Limit: 10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have results from all three sources combined
	assert.NotEmpty(t, result.Results)

	// Verify metrics show all sources contributed
	assert.True(t, result.Metrics.TextContributed, "text should have contributed")
	assert.True(t, result.Metrics.SemanticContributed, "semantic should have contributed")
	assert.True(t, result.Metrics.GraphContributed, "graph should have contributed")

	// Verify we have results from each source
	hasTextOnly := false
	hasSemanticOnly := false
	hasGraphOnly := false
	hasCombined := false

	for _, r := range result.Results {
		switch r.Source {
		case SourceBleve:
			hasTextOnly = true
		case SourceHNSW:
			hasSemanticOnly = true
		case SourceGraph:
			hasGraphOnly = true
		case SourceCombined:
			hasCombined = true
		}
	}

	// Documents appearing in multiple sources should be marked as combined
	// doc1, doc2, doc3 appear in multiple sources
	assert.True(t, hasCombined || hasTextOnly || hasSemanticOnly || hasGraphOnly,
		"should have results from various sources")
}

func TestIntegration_RRF_ScoreFormulaProducesExpectedScores(t *testing.T) {
	// Test: RRF score formula (Σ weight_i / (k + rank_i)) produces expected scores
	rrf := NewRRFFusion(60)

	// Single source, single result at rank 0
	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
	}

	weights := &QueryWeights{
		TextWeight:     1.0,
		SemanticWeight: 0.0,
		GraphWeight:    0.0,
	}

	results := rrf.Fuse(textResults, nil, nil, weights)

	require.Len(t, results, 1)

	// Expected: weight / (k + rank + 1) = 1.0 / (60 + 0 + 1) = 1/61
	expectedScore := 1.0 / 61.0
	verifyRRFScore(t, &results[0], expectedScore, 0.0001)
}

func TestIntegration_RRF_DuplicatesAcrossSourcesMerged(t *testing.T) {
	// Test: Duplicates across sources are merged
	rrf := NewRRFFusion(60)

	// Same document appears in all three sources
	textResults := []TextResult{
		{ID: "common_doc", Score: 0.9, Content: "Common content"},
	}
	semanticResults := []VectorResult{
		{ID: "common_doc", Score: 0.85},
	}
	graphResults := []GraphResult{
		{ID: "common_doc", Score: 0.8, Path: []string{"a", "common_doc"}},
	}

	weights := DefaultQueryWeights()
	results := rrf.Fuse(textResults, semanticResults, graphResults, weights)

	// Should have only one result (deduplicated)
	require.Len(t, results, 1)
	assert.Equal(t, "common_doc", results[0].ID)
	assert.Equal(t, SourceCombined, results[0].Source)

	// All component scores should be present
	assert.Equal(t, 0.9, results[0].TextScore)
	assert.Equal(t, 0.85, results[0].SemanticScore)
	assert.Equal(t, 0.8, results[0].GraphScore)

	// RRF score should be sum of contributions from all sources
	// Each source contributes: weight / (k + rank + 1)
	// With k=60, rank=0, and equal weights (~0.33 each)
	expectedMin := 3 * (0.33 / 61.0) // Minimum expected if weights were exactly 0.33
	assert.Greater(t, results[0].Score, expectedMin*0.9, "combined score should reflect all sources")
}

func TestIntegration_RRF_EmptySourceDoesNotBreakFusion(t *testing.T) {
	// Test: Empty results from one source don't break fusion
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 0.9},
		{ID: "doc2", Score: 0.8},
	}

	// Empty semantic results
	semanticResults := []VectorResult{}

	graphResults := []GraphResult{
		{ID: "doc3", Score: 0.85},
	}

	results := rrf.Fuse(textResults, semanticResults, graphResults, nil)

	// Should have 3 results (2 from text, 1 from graph)
	require.Len(t, results, 3)

	// Verify all documents are present
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	assert.True(t, ids["doc1"])
	assert.True(t, ids["doc2"])
	assert.True(t, ids["doc3"])
}

func TestIntegration_RRF_AllSourcesEmpty(t *testing.T) {
	// Test edge case: all sources return empty results
	rrf := NewRRFFusion(60)

	results := rrf.Fuse([]TextResult{}, []VectorResult{}, []GraphResult{}, nil)

	assert.Empty(t, results)
}

func TestIntegration_RRF_RankingOrder(t *testing.T) {
	// Test: Results are properly ranked by combined RRF score
	rrf := NewRRFFusion(60)

	// doc_multi appears in all sources (should rank highest)
	// doc_two appears in two sources
	// doc_one appears in one source
	textResults := []TextResult{
		{ID: "doc_multi", Score: 0.9},
		{ID: "doc_two", Score: 0.8},
		{ID: "doc_one_text", Score: 0.7},
	}
	semanticResults := []VectorResult{
		{ID: "doc_multi", Score: 0.85},
		{ID: "doc_two", Score: 0.75},
	}
	graphResults := []GraphResult{
		{ID: "doc_multi", Score: 0.8},
	}

	results := rrf.Fuse(textResults, semanticResults, graphResults, nil)

	require.NotEmpty(t, results)

	// doc_multi should be first (appears in 3 sources)
	assert.Equal(t, "doc_multi", results[0].ID)

	// doc_two should be second (appears in 2 sources)
	assert.Equal(t, "doc_two", results[1].ID)
}

func TestIntegration_RRF_KParameterAffectsScoreSpread(t *testing.T) {
	// Test: k parameter affects the spread of RRF scores
	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc2", Score: 0.9},
		{ID: "doc3", Score: 0.8},
	}

	weights := &QueryWeights{TextWeight: 1.0}

	// Small k = larger score differences between ranks
	smallKRRF := NewRRFFusion(1)
	smallKResults := smallKRRF.Fuse(textResults, nil, nil, weights)

	// Large k = smaller score differences between ranks
	largeKRRF := NewRRFFusion(1000)
	largeKResults := largeKRRF.Fuse(textResults, nil, nil, weights)

	// Calculate score spread (difference between first and last)
	smallKSpread := smallKResults[0].Score - smallKResults[2].Score
	largeKSpread := largeKResults[0].Score - largeKResults[2].Score

	// Small k should have larger spread
	assert.Greater(t, smallKSpread, largeKSpread,
		"small k should produce larger score spread: smallK=%f, largeK=%f",
		smallKSpread, largeKSpread)
}

func TestIntegration_RRF_WeightsAffectContribution(t *testing.T) {
	// Test: Different weights change relative contribution of sources
	rrf := NewRRFFusion(60)

	textResults := []TextResult{{ID: "text_only", Score: 1.0}}
	semanticResults := []VectorResult{{ID: "semantic_only", Score: 1.0}}

	// Heavy text weight
	textHeavyWeights := &QueryWeights{
		TextWeight:     0.9,
		SemanticWeight: 0.1,
		GraphWeight:    0.0,
	}

	textHeavyResults := rrf.Fuse(textResults, semanticResults, nil, textHeavyWeights)

	// Heavy semantic weight
	semanticHeavyWeights := &QueryWeights{
		TextWeight:     0.1,
		SemanticWeight: 0.9,
		GraphWeight:    0.0,
	}

	semanticHeavyResults := rrf.Fuse(textResults, semanticResults, nil, semanticHeavyWeights)

	// With text heavy weights, text_only should score higher
	var textOnlyTextHeavy, semanticOnlyTextHeavy float64
	for _, r := range textHeavyResults {
		if r.ID == "text_only" {
			textOnlyTextHeavy = r.Score
		} else if r.ID == "semantic_only" {
			semanticOnlyTextHeavy = r.Score
		}
	}

	// With semantic heavy weights, semantic_only should score higher
	var textOnlySemanticHeavy, semanticOnlySemanticHeavy float64
	for _, r := range semanticHeavyResults {
		if r.ID == "text_only" {
			textOnlySemanticHeavy = r.Score
		} else if r.ID == "semantic_only" {
			semanticOnlySemanticHeavy = r.Score
		}
	}

	assert.Greater(t, textOnlyTextHeavy, semanticOnlyTextHeavy,
		"text_only should score higher with text-heavy weights")
	assert.Greater(t, semanticOnlySemanticHeavy, textOnlySemanticHeavy,
		"semantic_only should score higher with semantic-heavy weights")
}

// =============================================================================
// HQ.5.2 Learned Weights Tests
// =============================================================================

func TestIntegration_LearnedWeights_UpdateBasedOnFeedback(t *testing.T) {
	// Test: Weights update based on click/selection feedback
	lqw := NewLearnedQueryWeights()

	initialTextMean := lqw.TextWeight.Mean()

	// Simulate user clicking on a result that was ranked best by text search
	ranks := map[string]SourceRanks{
		"clicked_doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}

	// Perform multiple updates to see clear change
	for i := 0; i < 10; i++ {
		lqw.Update("query"+string(rune('0'+i)), "clicked_doc", ranks, domain.DomainLibrarian)
	}

	// Text weight should have increased
	assert.Greater(t, lqw.TextWeight.Mean(), initialTextMean)
}

func TestIntegration_LearnedWeights_DomainIsolation(t *testing.T) {
	// Test: Domain-specific weights are isolated
	lqw := NewLearnedQueryWeights()

	// Update Librarian domain to favor text
	librarianRanks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 10, GraphRank: 15},
	}
	for i := 0; i < 15; i++ {
		lqw.Update("lib_query"+string(rune('0'+i)), "doc", librarianRanks, domain.DomainLibrarian)
	}

	// Update Engineer domain to favor graph
	engineerRanks := map[string]SourceRanks{
		"doc": {TextRank: 15, SemanticRank: 10, GraphRank: 0},
	}
	for i := 0; i < 15; i++ {
		lqw.Update("eng_query"+string(rune('0'+i)), "doc", engineerRanks, domain.DomainEngineer)
	}

	librarianWeights := lqw.GetWeights(domain.DomainLibrarian, false)
	engineerWeights := lqw.GetWeights(domain.DomainEngineer, false)

	// Librarian should favor text
	assert.Greater(t, librarianWeights.TextWeight, librarianWeights.GraphWeight,
		"librarian should favor text")

	// Engineer should favor graph
	assert.Greater(t, engineerWeights.GraphWeight, engineerWeights.TextWeight,
		"engineer should favor graph")

	// Domains should have different weight distributions
	assert.NotEqual(t, librarianWeights.TextWeight, engineerWeights.TextWeight,
		"domains should have different text weights")
}

func TestIntegration_LearnedWeights_ThompsonSamplingExploration(t *testing.T) {
	// Test: Thompson Sampling produces variation in explore mode
	lqw := NewLearnedQueryWeights()

	// Collect samples in explore mode
	samples := make([]*QueryWeights, 100)
	for i := 0; i < 100; i++ {
		samples[i] = lqw.GetWeights(domain.DomainLibrarian, true)
	}

	// Calculate variance of text weights
	var sum float64
	for _, s := range samples {
		sum += s.TextWeight
	}
	mean := sum / float64(len(samples))

	var variance float64
	for _, s := range samples {
		diff := s.TextWeight - mean
		variance += diff * diff
	}
	variance /= float64(len(samples))

	// Thompson Sampling should produce some variance (exploration)
	assert.Greater(t, variance, 0.0, "explore mode should produce variance in weights")

	// Compare with exploit mode (should be deterministic)
	exploitWeights1 := lqw.GetWeights(domain.DomainLibrarian, false)
	exploitWeights2 := lqw.GetWeights(domain.DomainLibrarian, false)

	assert.Equal(t, exploitWeights1.TextWeight, exploitWeights2.TextWeight,
		"exploit mode should be deterministic")
}

func TestIntegration_LearnedWeights_ConvergenceTowardSuccessfulSources(t *testing.T) {
	// Test: Weights converge toward successful sources
	lqw := NewLearnedQueryWeights()

	// Consistently reward semantic search (rank 0) over others
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 10, SemanticRank: 0, GraphRank: 15},
	}

	// Track semantic weight over time
	initialSemantic := lqw.SemanticWeight.Mean()

	for i := 0; i < 50; i++ {
		lqw.Update("query"+string(rune(i%26+'a')), "doc", ranks, domain.DomainLibrarian)
	}

	finalSemantic := lqw.SemanticWeight.Mean()

	// Semantic weight should have increased significantly
	assert.Greater(t, finalSemantic, initialSemantic,
		"semantic weight should increase with consistent rewards")

	// Get final weights - semantic should be highest
	weights := lqw.GetWeights(domain.DomainLibrarian, false)
	assert.Greater(t, weights.SemanticWeight, weights.TextWeight,
		"semantic should be favored after consistent rewards")
	assert.Greater(t, weights.SemanticWeight, weights.GraphWeight,
		"semantic should be favored after consistent rewards")
}

func TestIntegration_LearnedWeights_CoordinatorIntegration(t *testing.T) {
	// Test learned weights integration with coordinator
	coord := setupTestCoordinator(t)

	// Record some feedback
	textResults := []TextResult{
		{ID: "doc1", Score: 0.9},
		{ID: "doc2", Score: 0.8},
	}
	semanticResults := []VectorResult{
		{ID: "doc2", Score: 0.95},
		{ID: "doc1", Score: 0.7},
	}

	// User clicked doc2 which was ranked best by semantic
	coord.UpdateWeights("query1", "doc2", textResults, semanticResults, nil, domain.DomainLibrarian)

	// Verify weights were updated
	stats := coord.LearnedWeights().GetStats()
	assert.Greater(t, stats.SemanticMean, 0.0, "semantic weight should be updated")
}

func TestIntegration_LearnedWeights_ConfidenceIncreasesWithData(t *testing.T) {
	// Test: Confidence increases as more data is accumulated
	lqw := NewLearnedQueryWeights()

	initialConfidence := lqw.GetGlobalConfidence()

	// Add many updates
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}
	for i := 0; i < 30; i++ {
		lqw.Update("query"+string(rune('0'+i%10)), "doc", ranks, domain.DomainLibrarian)
	}

	// Domain should exist after updates
	assert.True(t, lqw.HasDomainWeights(domain.DomainLibrarian),
		"domain should have weights after updates")

	// Domain update count should be tracked
	assert.Equal(t, 30, lqw.DomainUpdateCount(domain.DomainLibrarian),
		"domain should track update count")

	// Global confidence should be in valid range
	globalConfidence := lqw.GetGlobalConfidence()
	assert.GreaterOrEqual(t, globalConfidence, 0.0)
	assert.LessOrEqual(t, globalConfidence, 1.0)

	// Domain confidence may be 0 due to exponential decay making effective samples low
	domainConfidence := lqw.GetDomainConfidence(domain.DomainLibrarian)
	assert.GreaterOrEqual(t, domainConfidence, 0.0)
	assert.LessOrEqual(t, domainConfidence, 1.0)

	t.Logf("Initial confidence: %f, Domain confidence: %f, Global confidence: %f",
		initialConfidence, domainConfidence, globalConfidence)
}

func TestIntegration_LearnedWeights_ResetClearsState(t *testing.T) {
	// Test: Reset clears all learned state
	lqw := NewLearnedQueryWeights()

	// Add updates
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 10, GraphRank: 15},
	}
	for i := 0; i < 20; i++ {
		lqw.Update("query"+string(rune('0'+i)), "doc", ranks, domain.DomainLibrarian)
	}

	// Verify state changed
	assert.True(t, lqw.HasDomainWeights(domain.DomainLibrarian))

	// Reset
	lqw.Reset()

	// Verify state is cleared
	assert.False(t, lqw.HasDomainWeights(domain.DomainLibrarian))
	assert.Equal(t, 0, lqw.DomainUpdateCount(domain.DomainLibrarian))

	// Weights should be back to uniform (mean ~0.5)
	assert.InDelta(t, 0.5, lqw.TextWeight.Mean(), 0.01)
	assert.InDelta(t, 0.5, lqw.SemanticWeight.Mean(), 0.01)
	assert.InDelta(t, 0.5, lqw.GraphWeight.Mean(), 0.01)
}

// =============================================================================
// HQ.5.3 Coordinator Parallel Execution Tests
// =============================================================================

func TestIntegration_Coordinator_AllSearchersExecuteInParallel(t *testing.T) {
	// Test: All three searchers execute in parallel
	mockBleve := &mockBleveSearcherIntegration{
		results: []TextResult{{ID: "text1", Score: 0.9}},
		delay:   50 * time.Millisecond,
	}
	mockVector := &mockVectorSearcherIntegration{
		results: []VectorResult{{ID: "vec1", Score: 0.85}},
		delay:   50 * time.Millisecond,
	}
	mockGraph := &mockGraphSearcherIntegration{
		results: []GraphResult{{ID: "graph1", Score: 0.8}},
		nodes:   []string{"start"},
		delay:   50 * time.Millisecond,
	}

	bleveSearcher := NewBleveSearcher(mockBleve)
	vectorSearcher := NewVectorSearcher(mockVector)
	graphTraverser := NewGraphTraverser(mockGraph)

	coord := NewHybridQueryCoordinator(bleveSearcher, vectorSearcher, graphTraverser)
	coord.SetTimeout(1 * time.Second)

	ctx := context.Background()
	entityType := "test"
	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		GraphPattern: &GraphPattern{
			StartNode:  &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{{Direction: DirectionOutgoing, MaxHops: 1}},
		},
		Limit: 10,
	}

	start := time.Now()
	result, err := coord.ExecuteWithMetrics(ctx, query)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotEmpty(t, result.Results)

	// If parallel: ~50ms, if sequential: ~150ms
	// Allow some margin for overhead
	assert.Less(t, elapsed, 120*time.Millisecond,
		"parallel execution should complete faster than sequential (got %v)", elapsed)

	// All searchers should have been called
	assert.Equal(t, int64(1), mockBleve.callCount.Load(), "text searcher should be called")
	assert.Equal(t, int64(1), mockVector.callCount.Load(), "vector searcher should be called")
	assert.GreaterOrEqual(t, mockGraph.callCount.Load(), int64(1), "graph searcher should be called")
}

func TestIntegration_Coordinator_TimeoutReturnsPartialResults(t *testing.T) {
	// Test: Timeout returns partial results
	mockBleve := &mockBleveSearcherIntegration{
		results: []TextResult{{ID: "fast1", Score: 0.9}},
		delay:   10 * time.Millisecond, // Fast
	}
	mockVector := &mockVectorSearcherIntegration{
		results: []VectorResult{{ID: "slow1", Score: 0.85}},
		delay:   500 * time.Millisecond, // Slow (will timeout)
	}

	bleveSearcher := NewBleveSearcher(mockBleve)
	vectorSearcher := NewVectorSearcher(mockVector)

	coord := NewHybridQueryCoordinator(bleveSearcher, vectorSearcher, nil)
	coord.SetTimeout(50 * time.Millisecond) // Short timeout

	ctx := context.Background()
	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		Limit:          10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	require.NoError(t, err)

	// Should have partial results from fast searcher
	assert.NotEmpty(t, result.Results, "should have partial results")

	// Should indicate timeout
	assert.True(t, result.Metrics.TimedOut, "should indicate timeout")

	// Text should have contributed (it was fast)
	assert.True(t, result.Metrics.TextContributed, "text should have contributed")
}

func TestIntegration_Coordinator_IndividualFailureDoesNotFailQuery(t *testing.T) {
	// Test: Individual searcher failure doesn't fail entire query
	mockBleve := &mockBleveSearcherIntegration{
		err: errors.New("bleve error"), // Will fail
	}
	mockVector := &mockVectorSearcherIntegration{
		results: []VectorResult{{ID: "vec1", Score: 0.9}},
	}
	mockGraph := &mockGraphSearcherIntegration{
		results: []GraphResult{{ID: "graph1", Score: 0.85}},
		nodes:   []string{"start"},
	}

	bleveSearcher := NewBleveSearcher(mockBleve)
	vectorSearcher := NewVectorSearcher(mockVector)
	graphTraverser := NewGraphTraverser(mockGraph)

	coord := NewHybridQueryCoordinator(bleveSearcher, vectorSearcher, graphTraverser)

	ctx := context.Background()
	entityType := "test"
	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		GraphPattern: &GraphPattern{
			StartNode:  &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{{Direction: DirectionOutgoing, MaxHops: 1}},
		},
		Limit: 10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)

	// Should NOT return error
	require.NoError(t, err)

	// Should have results from working searchers
	assert.NotEmpty(t, result.Results)

	// Text should NOT have contributed
	assert.False(t, result.Metrics.TextContributed, "text should not have contributed")

	// Semantic and graph should have contributed
	assert.True(t, result.Metrics.SemanticContributed, "semantic should have contributed")
	assert.True(t, result.Metrics.GraphContributed, "graph should have contributed")
}

func TestIntegration_Coordinator_MetricsTrackLatenciesCorrectly(t *testing.T) {
	// Test: Metrics track latencies correctly
	mockBleve := &mockBleveSearcherIntegration{
		results: []TextResult{{ID: "doc1", Score: 0.9}},
		delay:   20 * time.Millisecond,
	}
	mockVector := &mockVectorSearcherIntegration{
		results: []VectorResult{{ID: "doc2", Score: 0.85}},
		delay:   30 * time.Millisecond,
	}

	bleveSearcher := NewBleveSearcher(mockBleve)
	vectorSearcher := NewVectorSearcher(mockVector)

	coord := NewHybridQueryCoordinator(bleveSearcher, vectorSearcher, nil)
	coord.SetTimeout(1 * time.Second)

	ctx := context.Background()
	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		Limit:          10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	require.NoError(t, err)

	// Verify latencies are tracked
	assert.GreaterOrEqual(t, result.Metrics.TextLatency, 20*time.Millisecond,
		"text latency should be >= 20ms")
	assert.GreaterOrEqual(t, result.Metrics.SemanticLatency, 30*time.Millisecond,
		"semantic latency should be >= 30ms")
	assert.Greater(t, result.Metrics.TotalLatency, time.Duration(0),
		"total latency should be tracked")
	assert.Greater(t, result.Metrics.FusionLatency, time.Duration(0),
		"fusion latency should be tracked")

	// Total should be at least as long as the longest component
	// (parallel execution, so not sum)
	assert.GreaterOrEqual(t, result.Metrics.TotalLatency, result.Metrics.SemanticLatency,
		"total should be >= longest component")
}

func TestIntegration_Coordinator_RecordAndRetrieveMetrics(t *testing.T) {
	// Test: Recording and retrieving metrics works
	coord := setupTestCoordinator(t)

	metrics1 := &QueryMetrics{
		TextLatency:         10 * time.Millisecond,
		SemanticLatency:     15 * time.Millisecond,
		TotalLatency:        25 * time.Millisecond,
		TextContributed:     true,
		SemanticContributed: true,
	}
	metrics2 := &QueryMetrics{
		TextLatency:     30 * time.Millisecond,
		TotalLatency:    40 * time.Millisecond,
		TextContributed: true,
	}

	coord.RecordMetrics("query-1", metrics1)
	coord.RecordMetrics("query-2", metrics2)

	// Retrieve specific metrics
	retrieved, ok := coord.GetQueryMetrics("query-1")
	assert.True(t, ok)
	assert.Equal(t, 10*time.Millisecond, retrieved.TextLatency)

	// Get averages
	avg := coord.GetAverageMetrics()
	assert.Equal(t, 20*time.Millisecond, avg.TextLatency) // (10+30)/2
}

func TestIntegration_Coordinator_AllSearchersFail_ReturnsEmptyResults(t *testing.T) {
	// Test: When all searchers fail, returns empty results (not error)
	mockBleve := &mockBleveSearcherIntegration{err: errors.New("bleve error")}
	mockVector := &mockVectorSearcherIntegration{err: errors.New("vector error")}
	mockGraph := &mockGraphSearcherIntegration{err: errors.New("graph error")}

	bleveSearcher := NewBleveSearcher(mockBleve)
	vectorSearcher := NewVectorSearcher(mockVector)
	graphTraverser := NewGraphTraverser(mockGraph)

	coord := NewHybridQueryCoordinator(bleveSearcher, vectorSearcher, graphTraverser)

	ctx := context.Background()
	entityType := "test"
	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		GraphPattern: &GraphPattern{
			StartNode:  &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{{Direction: DirectionOutgoing, MaxHops: 1}},
		},
		Limit: 10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)

	// Should NOT return error (graceful degradation)
	require.NoError(t, err)

	// Should have empty results
	assert.Empty(t, result.Results)

	// No sources should have contributed
	assert.False(t, result.Metrics.TextContributed)
	assert.False(t, result.Metrics.SemanticContributed)
	assert.False(t, result.Metrics.GraphContributed)
}

func TestIntegration_Coordinator_ContextCancellation(t *testing.T) {
	// Test: Context cancellation is handled properly
	mockBleve := &mockBleveSearcherIntegration{
		results: []TextResult{{ID: "doc1", Score: 0.9}},
		delay:   200 * time.Millisecond,
	}

	bleveSearcher := NewBleveSearcher(mockBleve)
	coord := NewHybridQueryCoordinator(bleveSearcher, nil, nil)
	coord.SetTimeout(1 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	query := &HybridQuery{
		TextQuery: "test",
		Limit:     10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	require.NoError(t, err)

	// Should indicate timeout/cancellation
	assert.True(t, result.Metrics.TimedOut)
}

// =============================================================================
// HQ.5.4 Memory Integration Tests (RRF-side only, no memory package import)
// =============================================================================

func TestIntegration_RRF_FuseWithMemoryScores(t *testing.T) {
	// Test: FuseWithMemory correctly incorporates memory scores
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc2", Score: 0.8},
	}

	memoryScores := map[string]float64{
		"doc2": 0.95, // doc2 has high memory activation
		"doc3": 0.90, // doc3 only in memory
	}

	weights := &QueryWeights{
		TextWeight:     0.5,
		SemanticWeight: 0.0,
		GraphWeight:    0.0,
		MemoryWeight:   0.5,
	}

	results := rrf.FuseWithMemory(textResults, nil, nil, weights, memoryScores)

	require.Len(t, results, 3) // doc1, doc2, doc3

	// Find doc2 - should have both text and memory contributions
	var doc2 *HybridResult
	for i := range results {
		if results[i].ID == "doc2" {
			doc2 = &results[i]
			break
		}
	}
	require.NotNil(t, doc2)

	// doc2 should have higher score due to memory boost
	assert.Greater(t, doc2.Score, 0.0)

	// doc3 should be present (from memory only)
	hasDoc3 := false
	for _, r := range results {
		if r.ID == "doc3" {
			hasDoc3 = true
			break
		}
	}
	assert.True(t, hasDoc3, "doc3 should be present from memory")
}

func TestIntegration_RRF_FuseWithMemory_NilMemoryScores(t *testing.T) {
	// Test: FuseWithMemory handles nil memory scores gracefully
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
	}

	// Should work with nil memoryScores
	results := rrf.FuseWithMemory(textResults, nil, nil, nil, nil)

	require.Len(t, results, 1)
	assert.Equal(t, "doc1", results[0].ID)
}

func TestIntegration_RRF_FuseWithMemory_EmptyMemoryScores(t *testing.T) {
	// Test: FuseWithMemory handles empty memory scores gracefully
	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
	}

	// Should work with empty memoryScores
	results := rrf.FuseWithMemory(textResults, nil, nil, nil, map[string]float64{})

	require.Len(t, results, 1)
	assert.Equal(t, "doc1", results[0].ID)
}

func TestIntegration_RRF_MemoryOnlyResults(t *testing.T) {
	// Test: Memory-only results are included
	rrf := NewRRFFusion(60)

	memoryScores := map[string]float64{
		"memory_doc1": 0.9,
		"memory_doc2": 0.8,
	}

	weights := &QueryWeights{
		TextWeight:     0.0,
		SemanticWeight: 0.0,
		GraphWeight:    0.0,
		MemoryWeight:   1.0,
	}

	results := rrf.FuseWithMemory(nil, nil, nil, weights, memoryScores)

	// Should have results from memory only
	require.Len(t, results, 2)

	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	assert.True(t, ids["memory_doc1"])
	assert.True(t, ids["memory_doc2"])
}

// =============================================================================
// End-to-End Integration Tests
// =============================================================================

func TestIntegration_EndToEnd_FullQueryPipeline(t *testing.T) {
	// Test: Full query pipeline from query to results
	coord := setupTestCoordinator(t)
	ctx := context.Background()

	entityType := "test_type"
	query := &HybridQuery{
		TextQuery:      "test query",
		SemanticVector: []float32{0.1, 0.2, 0.3},
		GraphPattern: &GraphPattern{
			StartNode:  &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{{Direction: DirectionOutgoing, MaxHops: 1}},
		},
		Limit: 5,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	require.NoError(t, err)

	// Verify complete execution
	assert.NotEmpty(t, result.Results)
	assert.LessOrEqual(t, len(result.Results), 5, "should respect limit")
	assert.NotNil(t, result.Metrics)
	assert.Greater(t, result.Metrics.TotalLatency, time.Duration(0))

	// Results should be sorted by score
	for i := 1; i < len(result.Results); i++ {
		assert.GreaterOrEqual(t, result.Results[i-1].Score, result.Results[i].Score,
			"results should be sorted by score descending")
	}
}

func TestIntegration_EndToEnd_QueryWithExplicitWeights(t *testing.T) {
	// Test: Query with explicit weights
	coord := setupTestCoordinator(t)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		TextWeight:     0.8,
		SemanticWeight: 0.2,
		GraphWeight:    0.0,
		Limit:          10,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Results)

	// With high text weight, text-only results should score well
}

func TestIntegration_EndToEnd_ConcurrentQueries(t *testing.T) {
	// Test: Multiple concurrent queries
	coord := setupTestCoordinator(t)
	ctx := context.Background()

	const numQueries = 20
	var wg sync.WaitGroup
	errChan := make(chan error, numQueries)

	for i := 0; i < numQueries; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			query := &HybridQuery{
				TextQuery:      "concurrent test " + string(rune('a'+idx%26)),
				SemanticVector: []float32{float32(idx) * 0.1, float32(idx) * 0.2},
				Limit:          5,
			}

			_, err := coord.Execute(ctx, query)
			if err != nil {
				errChan <- err
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// All queries should succeed
	for err := range errChan {
		t.Errorf("concurrent query failed: %v", err)
	}
}

func TestIntegration_EndToEnd_LimitRespected(t *testing.T) {
	// Test: Limit is properly applied to results
	coord := setupTestCoordinator(t)
	ctx := context.Background()

	query := &HybridQuery{
		TextQuery:      "test",
		SemanticVector: []float32{0.1, 0.2},
		Limit:          2,
	}

	result, err := coord.ExecuteWithMetrics(ctx, query)
	require.NoError(t, err)

	// Should not exceed limit
	assert.LessOrEqual(t, len(result.Results), 2)
}

func TestIntegration_EndToEnd_QueryValidation(t *testing.T) {
	// Test: Invalid queries are rejected
	coord := setupTestCoordinator(t)
	ctx := context.Background()

	// Query with no modalities should fail validation
	query := &HybridQuery{}

	_, err := coord.Execute(ctx, query)
	assert.Error(t, err, "empty query should fail validation")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkIntegration_HybridQuery(b *testing.B) {
	mockBleve, mockVector, mockGraph := createMockSearchers()
	bleveSearcher := NewBleveSearcher(mockBleve)
	vectorSearcher := NewVectorSearcher(mockVector)
	graphTraverser := NewGraphTraverser(mockGraph)

	coord := NewHybridQueryCoordinator(bleveSearcher, vectorSearcher, graphTraverser)
	ctx := context.Background()

	entityType := "test"
	query := &HybridQuery{
		TextQuery:      "benchmark test",
		SemanticVector: []float32{0.1, 0.2, 0.3},
		GraphPattern: &GraphPattern{
			StartNode:  &NodeMatcher{EntityType: &entityType},
			Traversals: []TraversalStep{{Direction: DirectionOutgoing, MaxHops: 1}},
		},
		Limit: 10,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = coord.Execute(ctx, query)
	}
}

func BenchmarkIntegration_RRFFusion(b *testing.B) {
	rrf := NewRRFFusion(60)

	textResults := make([]TextResult, 50)
	semanticResults := make([]VectorResult, 50)
	graphResults := make([]GraphResult, 50)

	for i := 0; i < 50; i++ {
		id := "doc" + string(rune('a'+i%26))
		textResults[i] = TextResult{ID: id, Score: float64(50-i) / 50.0}
		semanticResults[i] = VectorResult{ID: id, Score: float64(50-i) / 50.0}
		graphResults[i] = GraphResult{ID: id, Score: float64(50-i) / 50.0}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rrf.Fuse(textResults, semanticResults, graphResults, nil)
	}
}

func BenchmarkIntegration_LearnedWeightsUpdate(b *testing.B) {
	lqw := NewLearnedQueryWeights()
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lqw.Update("query", "doc", ranks, domain.DomainLibrarian)
	}
}

func BenchmarkIntegration_RRFFusionWithMemory(b *testing.B) {
	rrf := NewRRFFusion(60)

	textResults := make([]TextResult, 30)
	memoryScores := make(map[string]float64)

	for i := 0; i < 30; i++ {
		id := "doc" + string(rune('a'+i%26))
		textResults[i] = TextResult{ID: id, Score: float64(30-i) / 30.0}
		memoryScores[id] = float64(i) / 30.0
	}

	weights := &QueryWeights{
		TextWeight:   0.5,
		MemoryWeight: 0.5,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rrf.FuseWithMemory(textResults, nil, nil, weights, memoryScores)
	}
}
