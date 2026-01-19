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
// Mock Implementations for Final Integration Tests
// =============================================================================

// mockBleveFinal provides a configurable mock Bleve searcher for final tests.
type mockBleveFinal struct {
	results   []TextResult
	err       error
	delay     time.Duration
	callCount atomic.Int64
	mu        sync.Mutex
}

func (m *mockBleveFinal) SearchInContext(ctx context.Context, req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	m.callCount.Add(1)

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

// mockVectorFinal provides a configurable mock vector searcher for final tests.
type mockVectorFinal struct {
	results   []VectorResult
	err       error
	delay     time.Duration
	callCount atomic.Int64
}

func (m *mockVectorFinal) Search(vector []float32, k int) ([]string, []float32, error) {
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

// mockGraphFinal provides a configurable mock graph traverser for final tests.
type mockGraphFinal struct {
	results   []GraphResult
	nodes     []string
	err       error
	delay     time.Duration
	callCount atomic.Int64
}

func (m *mockGraphFinal) GetOutgoingEdges(nodeID string, edgeTypes []string) ([]Edge, error) {
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

func (m *mockGraphFinal) GetIncomingEdges(nodeID string, edgeTypes []string) ([]Edge, error) {
	return nil, nil
}

func (m *mockGraphFinal) GetNodesByPattern(pattern *NodeMatcher) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.nodes) > 0 {
		return m.nodes, nil
	}
	return []string{"start-node"}, nil
}

// =============================================================================
// HQ.6.1 RRF Fusion Tests with Known Rankings
// =============================================================================

func TestFinal_HQ61_RRFFormulaWithKnownRankings(t *testing.T) {
	// Test RRF formula: score = SUM weight_i / (k + rank_i)
	// For known inputs, verify exact expected scores

	t.Run("single_source_single_result", func(t *testing.T) {
		rrf := NewRRFFusion(60)

		textResults := []TextResult{
			{ID: "doc1", Score: 1.0, Content: "Test document"},
		}

		weights := &QueryWeights{
			TextWeight:     1.0,
			SemanticWeight: 0.0,
			GraphWeight:    0.0,
		}

		results := rrf.Fuse(textResults, nil, nil, weights)

		require.Len(t, results, 1)
		// RRF score = weight / (k + rank + 1) = 1.0 / (60 + 0 + 1) = 1/61
		expectedScore := 1.0 / 61.0
		assert.InDelta(t, expectedScore, results[0].Score, 1e-10)
	})

	t.Run("single_source_multiple_results", func(t *testing.T) {
		rrf := NewRRFFusion(60)

		textResults := []TextResult{
			{ID: "doc1", Score: 1.0},
			{ID: "doc2", Score: 0.9},
			{ID: "doc3", Score: 0.8},
		}

		weights := &QueryWeights{
			TextWeight:     1.0,
			SemanticWeight: 0.0,
			GraphWeight:    0.0,
		}

		results := rrf.Fuse(textResults, nil, nil, weights)

		require.Len(t, results, 3)

		// Verify each result's RRF score
		// rank 0: 1.0 / (60 + 1) = 1/61
		assert.InDelta(t, 1.0/61.0, results[0].Score, 1e-10)
		// rank 1: 1.0 / (60 + 2) = 1/62
		assert.InDelta(t, 1.0/62.0, results[1].Score, 1e-10)
		// rank 2: 1.0 / (60 + 3) = 1/63
		assert.InDelta(t, 1.0/63.0, results[2].Score, 1e-10)
	})

	t.Run("multiple_sources_combined_scores", func(t *testing.T) {
		rrf := NewRRFFusion(60)

		// Same document appears in both sources
		textResults := []TextResult{
			{ID: "common", Score: 1.0},
		}
		semanticResults := []VectorResult{
			{ID: "common", Score: 0.9},
		}

		weights := &QueryWeights{
			TextWeight:     0.5,
			SemanticWeight: 0.5,
			GraphWeight:    0.0,
		}

		results := rrf.Fuse(textResults, semanticResults, nil, weights)

		require.Len(t, results, 1)

		// Combined RRF score = 0.5/(60+1) + 0.5/(60+1) = 1.0/(60+1) = 1/61
		expectedScore := 0.5/61.0 + 0.5/61.0
		assert.InDelta(t, expectedScore, results[0].Score, 1e-10)
	})

	t.Run("verify_normalized_weights", func(t *testing.T) {
		rrf := NewRRFFusion(60)

		textResults := []TextResult{{ID: "doc1", Score: 1.0}}
		semanticResults := []VectorResult{{ID: "doc2", Score: 0.9}}
		graphResults := []GraphResult{{ID: "doc3", Score: 0.8}}

		// Unnormalized weights (sum to 3.0)
		weights := &QueryWeights{
			TextWeight:     1.0,
			SemanticWeight: 1.0,
			GraphWeight:    1.0,
		}

		results := rrf.Fuse(textResults, semanticResults, graphResults, weights)

		require.Len(t, results, 3)

		// After normalization, each weight becomes 1/3
		// Each result: (1/3) / 61 = 1/183
		for _, r := range results {
			assert.InDelta(t, (1.0/3.0)/61.0, r.Score, 1e-9)
		}
	})
}

func TestFinal_HQ61_RRFWithVariousKValues(t *testing.T) {
	// Test RRF behavior with different k values

	t.Run("k_equals_1", func(t *testing.T) {
		rrf := NewRRFFusion(1)

		textResults := []TextResult{
			{ID: "doc1", Score: 1.0},
			{ID: "doc2", Score: 0.9},
		}

		weights := &QueryWeights{TextWeight: 1.0}
		results := rrf.Fuse(textResults, nil, nil, weights)

		// k=1: rank 0 -> 1/(1+1)=0.5, rank 1 -> 1/(1+2)=0.333
		assert.InDelta(t, 0.5, results[0].Score, 1e-10)
		assert.InDelta(t, 1.0/3.0, results[1].Score, 1e-10)
	})

	t.Run("k_equals_100", func(t *testing.T) {
		rrf := NewRRFFusion(100)

		textResults := []TextResult{
			{ID: "doc1", Score: 1.0},
			{ID: "doc2", Score: 0.9},
		}

		weights := &QueryWeights{TextWeight: 1.0}
		results := rrf.Fuse(textResults, nil, nil, weights)

		// k=100: rank 0 -> 1/101, rank 1 -> 1/102
		assert.InDelta(t, 1.0/101.0, results[0].Score, 1e-10)
		assert.InDelta(t, 1.0/102.0, results[1].Score, 1e-10)
	})

	t.Run("k_value_affects_score_spread", func(t *testing.T) {
		// Smaller k = larger score spread between ranks
		textResults := []TextResult{
			{ID: "doc1", Score: 1.0},
			{ID: "doc2", Score: 0.9},
			{ID: "doc3", Score: 0.8},
		}
		weights := &QueryWeights{TextWeight: 1.0}

		smallK := NewRRFFusion(5)
		largeK := NewRRFFusion(500)

		smallResults := smallK.Fuse(textResults, nil, nil, weights)
		largeResults := largeK.Fuse(textResults, nil, nil, weights)

		// Calculate spread: first - last score
		smallSpread := smallResults[0].Score - smallResults[2].Score
		largeSpread := largeResults[0].Score - largeResults[2].Score

		// Small k should give larger spread
		assert.Greater(t, smallSpread, largeSpread)
	})

	t.Run("default_k_is_60", func(t *testing.T) {
		rrf := DefaultRRFFusion()
		assert.Equal(t, 60, rrf.K())
	})

	t.Run("negative_k_defaults_to_60", func(t *testing.T) {
		rrf := NewRRFFusion(-5)
		assert.Equal(t, 60, rrf.K())
	})

	t.Run("zero_k_defaults_to_60", func(t *testing.T) {
		rrf := NewRRFFusion(0)
		assert.Equal(t, 60, rrf.K())
	})
}

func TestFinal_HQ61_RRFRankNormalizationAcrossSources(t *testing.T) {
	// Verify that ranks are computed correctly for each source independently

	t.Run("independent_source_rankings", func(t *testing.T) {
		rrf := NewRRFFusion(60)

		// doc_a is rank 0 in text, rank 2 in semantic
		// doc_b is rank 1 in text, rank 0 in semantic
		textResults := []TextResult{
			{ID: "doc_a", Score: 0.9},
			{ID: "doc_b", Score: 0.8},
			{ID: "doc_c", Score: 0.7},
		}
		semanticResults := []VectorResult{
			{ID: "doc_b", Score: 0.95},
			{ID: "doc_c", Score: 0.85},
			{ID: "doc_a", Score: 0.75},
		}

		weights := &QueryWeights{
			TextWeight:     0.5,
			SemanticWeight: 0.5,
			GraphWeight:    0.0,
		}

		results := rrf.Fuse(textResults, semanticResults, nil, weights)

		// Find doc_a and doc_b in results
		var docAScore, docBScore float64
		for _, r := range results {
			if r.ID == "doc_a" {
				docAScore = r.Score
			} else if r.ID == "doc_b" {
				docBScore = r.Score
			}
		}

		// doc_a: text rank 0, semantic rank 2
		// RRF = 0.5/61 + 0.5/63
		expectedDocA := 0.5/61.0 + 0.5/63.0
		assert.InDelta(t, expectedDocA, docAScore, 1e-10)

		// doc_b: text rank 1, semantic rank 0
		// RRF = 0.5/62 + 0.5/61
		expectedDocB := 0.5/62.0 + 0.5/61.0
		assert.InDelta(t, expectedDocB, docBScore, 1e-10)

		// doc_b should rank higher due to better semantic rank
		assert.Greater(t, docBScore, docAScore)
	})

	t.Run("three_source_ranking", func(t *testing.T) {
		rrf := NewRRFFusion(60)

		// doc_x appears in all three sources at different ranks
		textResults := []TextResult{{ID: "doc_x", Score: 0.9}}     // rank 0
		semanticResults := []VectorResult{{ID: "other", Score: 0.95}, {ID: "doc_x", Score: 0.85}} // rank 1
		graphResults := []GraphResult{{ID: "g1", Score: 0.9}, {ID: "g2", Score: 0.85}, {ID: "doc_x", Score: 0.8}} // rank 2

		weights := &QueryWeights{
			TextWeight:     1.0 / 3.0,
			SemanticWeight: 1.0 / 3.0,
			GraphWeight:    1.0 / 3.0,
		}

		results := rrf.Fuse(textResults, semanticResults, graphResults, weights)

		var docXScore float64
		for _, r := range results {
			if r.ID == "doc_x" {
				docXScore = r.Score
				break
			}
		}

		// doc_x: text rank 0, semantic rank 1, graph rank 2
		// RRF = (1/3)/61 + (1/3)/62 + (1/3)/63
		expectedDocX := (1.0/3.0)/61.0 + (1.0/3.0)/62.0 + (1.0/3.0)/63.0
		assert.InDelta(t, expectedDocX, docXScore, 1e-10)
	})
}

// =============================================================================
// HQ.6.2 Learned Weight Convergence Tests
// =============================================================================

func TestFinal_HQ62_WeightConvergenceWithConsistentFeedback(t *testing.T) {
	// Test that weights converge toward sources that consistently rank clicked results well

	t.Run("convergence_toward_best_source", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		initialTextMean := lqw.TextWeight.Mean()

		// Consistently reward text search (rank 0) over semantic (rank 10)
		ranks := map[string]SourceRanks{
			"clicked_doc": {TextRank: 0, SemanticRank: 10, GraphRank: 15},
		}

		// Apply 50 consistent updates
		for i := 0; i < 50; i++ {
			lqw.Update("query", "clicked_doc", ranks, domain.DomainLibrarian)
		}

		finalTextMean := lqw.TextWeight.Mean()

		// Text weight should increase
		assert.Greater(t, finalTextMean, initialTextMean,
			"text weight should increase with consistent good rankings")

		// The relative difference should favor text
		textWeights := lqw.GetWeights(domain.DomainLibrarian, false)
		assert.Greater(t, textWeights.TextWeight, textWeights.SemanticWeight,
			"text should be weighted higher than semantic after consistent rewards")
	})

	t.Run("convergence_measured_over_time", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		ranks := map[string]SourceRanks{
			"doc": {TextRank: 5, SemanticRank: 0, GraphRank: 10},
		}

		// Track semantic mean over epochs
		var semanticMeans []float64
		semanticMeans = append(semanticMeans, lqw.SemanticWeight.Mean())

		for epoch := 0; epoch < 5; epoch++ {
			for i := 0; i < 10; i++ {
				lqw.Update("q", "doc", ranks, domain.DomainLibrarian)
			}
			semanticMeans = append(semanticMeans, lqw.SemanticWeight.Mean())
		}

		// Semantic mean should generally increase (it gets rank 0)
		for i := 1; i < len(semanticMeans); i++ {
			assert.GreaterOrEqual(t, semanticMeans[i], semanticMeans[i-1]*0.95,
				"semantic weight should not decrease significantly over time")
		}
	})
}

func TestFinal_HQ62_ThompsonSamplingExplorationDecreases(t *testing.T) {
	// Test that Thompson Sampling exploration decreases as confidence increases

	t.Run("variance_decreases_with_more_data", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Measure initial variance through sampling
		initialVariance := measureWeightVariance(lqw, domain.DomainLibrarian, 100)

		// Add many consistent updates
		ranks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
		}
		for i := 0; i < 100; i++ {
			lqw.Update("q", "doc", ranks, domain.DomainLibrarian)
		}

		// Measure variance after updates
		finalVariance := measureWeightVariance(lqw, domain.DomainLibrarian, 100)

		// The underlying distributions should have less variance
		// but Thompson Sampling may still show some variance
		// At minimum, the raw Beta variance should decrease
		textVarianceInitial := 0.25 / 5.0 // Beta(2,2) variance
		textVarianceFinal := lqw.TextWeight.Variance()

		assert.Less(t, textVarianceFinal, textVarianceInitial,
			"text weight variance should decrease with more data")

		t.Logf("Initial sample variance: %f, Final sample variance: %f",
			initialVariance, finalVariance)
	})

	t.Run("exploit_mode_is_deterministic", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Add some updates
		ranks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
		}
		for i := 0; i < 20; i++ {
			lqw.Update("q", "doc", ranks, domain.DomainLibrarian)
		}

		// Exploit mode (explore=false) should return same weights each time
		weights1 := lqw.GetWeights(domain.DomainLibrarian, false)
		weights2 := lqw.GetWeights(domain.DomainLibrarian, false)
		weights3 := lqw.GetWeights(domain.DomainLibrarian, false)

		assert.Equal(t, weights1.TextWeight, weights2.TextWeight)
		assert.Equal(t, weights1.TextWeight, weights3.TextWeight)
		assert.Equal(t, weights1.SemanticWeight, weights2.SemanticWeight)
		assert.Equal(t, weights1.GraphWeight, weights3.GraphWeight)
	})

	t.Run("explore_mode_produces_variation", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Sample many times in explore mode
		samples := make(map[float64]bool)
		for i := 0; i < 100; i++ {
			w := lqw.GetWeights(domain.DomainLibrarian, true)
			samples[math.Round(w.TextWeight*1000)] = true
		}

		// Should have variation in sampled weights
		assert.Greater(t, len(samples), 1,
			"explore mode should produce varying weights")
	})
}

func TestFinal_HQ62_MultiSourceWeightLearning(t *testing.T) {
	// Test learning weights across multiple sources

	t.Run("each_source_learns_independently", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Track initial means
		initialText := lqw.TextWeight.Mean()
		initialSemantic := lqw.SemanticWeight.Mean()
		initialGraph := lqw.GraphWeight.Mean()

		// Updates that favor different sources in different scenarios
		textWinRanks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 10, GraphRank: 10},
		}
		semanticWinRanks := map[string]SourceRanks{
			"doc": {TextRank: 10, SemanticRank: 0, GraphRank: 10},
		}
		graphWinRanks := map[string]SourceRanks{
			"doc": {TextRank: 10, SemanticRank: 10, GraphRank: 0},
		}

		// Balanced updates across all sources
		for i := 0; i < 30; i++ {
			lqw.Update("q1", "doc", textWinRanks, domain.DomainLibrarian)
			lqw.Update("q2", "doc", semanticWinRanks, domain.DomainLibrarian)
			lqw.Update("q3", "doc", graphWinRanks, domain.DomainLibrarian)
		}

		// All weights should be close to their initial values (balanced rewards)
		finalText := lqw.TextWeight.Mean()
		finalSemantic := lqw.SemanticWeight.Mean()
		finalGraph := lqw.GraphWeight.Mean()

		// After balanced updates, weights should be similar
		weights := lqw.GetWeights(domain.DomainLibrarian, false)
		maxWeight := math.Max(weights.TextWeight, math.Max(weights.SemanticWeight, weights.GraphWeight))
		minWeight := math.Min(weights.TextWeight, math.Min(weights.SemanticWeight, weights.GraphWeight))

		// The difference between max and min should be relatively small
		assert.Less(t, maxWeight-minWeight, 0.3,
			"weights should be relatively balanced with balanced feedback")

		t.Logf("Initial: text=%f, semantic=%f, graph=%f",
			initialText, initialSemantic, initialGraph)
		t.Logf("Final: text=%f, semantic=%f, graph=%f",
			finalText, finalSemantic, finalGraph)
	})
}

func TestFinal_HQ62_WeightsBoundedCorrectly(t *testing.T) {
	// Test that weights stay bounded [0.1, 0.9] through normalization

	t.Run("extreme_updates_stay_bounded", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Extreme updates favoring only text (perfect rank)
		ranks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: -1, GraphRank: -1}, // Only found in text
		}

		for i := 0; i < 200; i++ {
			lqw.Update("q", "doc", ranks, domain.DomainLibrarian)
		}

		weights := lqw.GetWeights(domain.DomainLibrarian, false)

		// Due to normalization, weights always sum to 1.0
		sum := weights.TextWeight + weights.SemanticWeight + weights.GraphWeight
		assert.InDelta(t, 1.0, sum, 0.01,
			"weights should sum to approximately 1.0")

		// Even with extreme updates, no single weight should be exactly 1.0 or 0.0
		// due to the prior and drift protection
		assert.Less(t, weights.TextWeight, 1.0)
		assert.Greater(t, weights.SemanticWeight, 0.0)
		assert.Greater(t, weights.GraphWeight, 0.0)
	})

	t.Run("weights_sum_to_one", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Various update patterns
		ranks := map[string]SourceRanks{
			"doc": {TextRank: 2, SemanticRank: 5, GraphRank: 8},
		}

		for i := 0; i < 50; i++ {
			lqw.Update("q", "doc", ranks, domain.DomainLibrarian)

			weights := lqw.GetWeights(domain.DomainLibrarian, false)
			sum := weights.TextWeight + weights.SemanticWeight + weights.GraphWeight
			assert.InDelta(t, 1.0, sum, 0.01,
				"weights should always sum to 1.0 after update %d", i)
		}
	})
}

// measureWeightVariance samples weights in explore mode and computes variance
func measureWeightVariance(lqw *LearnedQueryWeights, d domain.Domain, n int) float64 {
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		w := lqw.GetWeights(d, true)
		samples[i] = w.TextWeight
	}

	// Calculate mean
	var sum float64
	for _, s := range samples {
		sum += s
	}
	mean := sum / float64(n)

	// Calculate variance
	var variance float64
	for _, s := range samples {
		diff := s - mean
		variance += diff * diff
	}
	return variance / float64(n)
}

// =============================================================================
// HQ.6.3 Timeout and Partial Result Tests
// =============================================================================

func TestFinal_HQ63_GracefulDegradationOnSourceTimeout(t *testing.T) {
	// Test that partial results are returned when some sources timeout

	t.Run("fast_source_returns_while_slow_times_out", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			results: []TextResult{
				{ID: "fast1", Score: 0.9},
				{ID: "fast2", Score: 0.8},
			},
			delay: 10 * time.Millisecond, // Fast
		}
		mockVector := &mockVectorFinal{
			results: []VectorResult{
				{ID: "slow1", Score: 0.95},
			},
			delay: 500 * time.Millisecond, // Slow - will timeout
		}

		bleve := NewBleveSearcher(mockBleve)
		vector := NewVectorSearcher(mockVector)
		coord := NewHybridQueryCoordinator(bleve, vector, nil)
		coord.SetTimeout(50 * time.Millisecond)

		query := &HybridQuery{
			TextQuery:      "test",
			SemanticVector: []float32{0.1, 0.2},
			Limit:          10,
		}

		result, err := coord.ExecuteWithMetrics(context.Background(), query)

		require.NoError(t, err)
		assert.NotEmpty(t, result.Results, "should have partial results from fast source")
		assert.True(t, result.Metrics.TimedOut, "should indicate timeout")
		assert.True(t, result.Metrics.TextContributed, "text should have contributed")
	})

	t.Run("multiple_sources_partial_success", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			results: []TextResult{{ID: "text1", Score: 0.9}},
			delay:   5 * time.Millisecond,
		}
		mockVector := &mockVectorFinal{
			results: []VectorResult{{ID: "vec1", Score: 0.85}},
			delay:   15 * time.Millisecond, // Medium
		}
		mockGraph := &mockGraphFinal{
			results: []GraphResult{{ID: "graph1", Score: 0.8}},
			nodes:   []string{"start"},
			delay:   500 * time.Millisecond, // Very slow - timeout
		}

		bleve := NewBleveSearcher(mockBleve)
		vector := NewVectorSearcher(mockVector)
		graph := NewGraphTraverser(mockGraph)
		coord := NewHybridQueryCoordinator(bleve, vector, graph)
		coord.SetTimeout(50 * time.Millisecond)

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

		result, err := coord.ExecuteWithMetrics(context.Background(), query)

		require.NoError(t, err)
		assert.NotEmpty(t, result.Results)
		assert.True(t, result.Metrics.TimedOut)
		// At least text should have contributed
		assert.True(t, result.Metrics.TextContributed)
	})
}

func TestFinal_HQ63_PartialResultsWhenSourcesFail(t *testing.T) {
	// Test partial results when some sources fail with errors

	t.Run("one_source_error_others_succeed", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			err: errors.New("bleve connection error"),
		}
		mockVector := &mockVectorFinal{
			results: []VectorResult{
				{ID: "vec1", Score: 0.9},
				{ID: "vec2", Score: 0.85},
			},
		}
		mockGraph := &mockGraphFinal{
			results: []GraphResult{{ID: "graph1", Score: 0.8}},
			nodes:   []string{"start"},
		}

		bleve := NewBleveSearcher(mockBleve)
		vector := NewVectorSearcher(mockVector)
		graph := NewGraphTraverser(mockGraph)
		coord := NewHybridQueryCoordinator(bleve, vector, graph)

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

		result, err := coord.ExecuteWithMetrics(context.Background(), query)

		require.NoError(t, err, "should not return error for partial failure")
		assert.NotEmpty(t, result.Results, "should have results from working sources")
		assert.False(t, result.Metrics.TextContributed, "text should not have contributed")
		assert.True(t, result.Metrics.SemanticContributed, "semantic should have contributed")
		assert.True(t, result.Metrics.GraphContributed, "graph should have contributed")
	})

	t.Run("two_sources_fail_one_succeeds", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			err: errors.New("error"),
		}
		mockVector := &mockVectorFinal{
			err: errors.New("error"),
		}
		mockGraph := &mockGraphFinal{
			results: []GraphResult{{ID: "graph1", Score: 0.8}},
			nodes:   []string{"start"},
		}

		bleve := NewBleveSearcher(mockBleve)
		vector := NewVectorSearcher(mockVector)
		graph := NewGraphTraverser(mockGraph)
		coord := NewHybridQueryCoordinator(bleve, vector, graph)

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

		result, err := coord.ExecuteWithMetrics(context.Background(), query)

		require.NoError(t, err)
		assert.NotEmpty(t, result.Results)
		assert.False(t, result.Metrics.TextContributed)
		assert.False(t, result.Metrics.SemanticContributed)
		assert.True(t, result.Metrics.GraphContributed)
	})

	t.Run("all_sources_fail_returns_empty", func(t *testing.T) {
		mockBleve := &mockBleveFinal{err: errors.New("error")}
		mockVector := &mockVectorFinal{err: errors.New("error")}
		mockGraph := &mockGraphFinal{err: errors.New("error")}

		bleve := NewBleveSearcher(mockBleve)
		vector := NewVectorSearcher(mockVector)
		graph := NewGraphTraverser(mockGraph)
		coord := NewHybridQueryCoordinator(bleve, vector, graph)

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

		result, err := coord.ExecuteWithMetrics(context.Background(), query)

		require.NoError(t, err, "should not return error even if all fail")
		assert.Empty(t, result.Results)
		assert.False(t, result.Metrics.TextContributed)
		assert.False(t, result.Metrics.SemanticContributed)
		assert.False(t, result.Metrics.GraphContributed)
	})
}

func TestFinal_HQ63_ContextCancellationHandling(t *testing.T) {
	// Test proper handling of context cancellation

	t.Run("immediate_cancellation", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			results: []TextResult{{ID: "doc1", Score: 0.9}},
			delay:   100 * time.Millisecond,
		}

		bleve := NewBleveSearcher(mockBleve)
		coord := NewHybridQueryCoordinator(bleve, nil, nil)
		coord.SetTimeout(1 * time.Second)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		query := &HybridQuery{
			TextQuery: "test",
			Limit:     10,
		}

		result, err := coord.ExecuteWithMetrics(ctx, query)

		require.NoError(t, err)
		assert.True(t, result.Metrics.TimedOut)
	})

	t.Run("cancellation_during_execution", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			results: []TextResult{{ID: "doc1", Score: 0.9}},
			delay:   200 * time.Millisecond,
		}

		bleve := NewBleveSearcher(mockBleve)
		coord := NewHybridQueryCoordinator(bleve, nil, nil)
		coord.SetTimeout(1 * time.Second)

		ctx, cancel := context.WithCancel(context.Background())

		// Cancel after a short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		query := &HybridQuery{
			TextQuery: "test",
			Limit:     10,
		}

		start := time.Now()
		result, err := coord.ExecuteWithMetrics(ctx, query)
		elapsed := time.Since(start)

		require.NoError(t, err)
		assert.True(t, result.Metrics.TimedOut)
		assert.Less(t, elapsed, 150*time.Millisecond,
			"should return quickly after cancellation")
	})
}

func TestFinal_HQ63_TimeoutDoesNotBlockIndefinitely(t *testing.T) {
	// Test that timeout is respected and queries don't hang

	t.Run("timeout_respected_within_tolerance", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			results: []TextResult{{ID: "doc1", Score: 0.9}},
			delay:   10 * time.Second, // Very slow
		}

		bleve := NewBleveSearcher(mockBleve)
		coord := NewHybridQueryCoordinator(bleve, nil, nil)
		coord.SetTimeout(100 * time.Millisecond)

		query := &HybridQuery{
			TextQuery: "test",
			Limit:     10,
		}

		start := time.Now()
		result, err := coord.ExecuteWithMetrics(context.Background(), query)
		elapsed := time.Since(start)

		require.NoError(t, err)
		assert.True(t, result.Metrics.TimedOut)
		// Should return within timeout + reasonable overhead
		assert.Less(t, elapsed, 200*time.Millisecond,
			"should not block beyond timeout: got %v", elapsed)
	})

	t.Run("query_timeout_overrides_default", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			results: []TextResult{{ID: "doc1", Score: 0.9}},
			delay:   50 * time.Millisecond,
		}

		bleve := NewBleveSearcher(mockBleve)
		coord := NewHybridQueryCoordinator(bleve, nil, nil)
		coord.SetTimeout(10 * time.Millisecond) // Very short default

		query := &HybridQuery{
			TextQuery: "test",
			Timeout:   200 * time.Millisecond, // Query specifies longer timeout
			Limit:     10,
		}

		result, err := coord.ExecuteWithMetrics(context.Background(), query)

		require.NoError(t, err)
		assert.False(t, result.Metrics.TimedOut,
			"query timeout should allow completion")
		assert.NotEmpty(t, result.Results)
	})
}

// =============================================================================
// HQ.6.4 Domain Filter Tests
// =============================================================================

func TestFinal_HQ64_FilteringResultsByDomain(t *testing.T) {
	// Test filtering behavior with domain filters

	t.Run("domain_filter_detected", func(t *testing.T) {
		coord := NewHybridQueryCoordinator(nil, nil, nil)

		query := &HybridQuery{
			TextQuery: "test",
			Filters: []QueryFilter{
				{Type: FilterDomain, Value: domain.DomainArchitect},
			},
		}

		d := coord.detectDomain(query)
		assert.Equal(t, domain.DomainArchitect, d)
	})

	t.Run("string_domain_filter_parsed", func(t *testing.T) {
		coord := NewHybridQueryCoordinator(nil, nil, nil)

		query := &HybridQuery{
			TextQuery: "test",
			Filters: []QueryFilter{
				{Type: FilterDomain, Value: "engineer"},
			},
		}

		d := coord.detectDomain(query)
		assert.Equal(t, domain.DomainEngineer, d)
	})

	t.Run("default_domain_when_no_filter", func(t *testing.T) {
		coord := NewHybridQueryCoordinator(nil, nil, nil)

		query := &HybridQuery{
			TextQuery: "test",
		}

		d := coord.detectDomain(query)
		assert.Equal(t, domain.DomainLibrarian, d, "should default to Librarian")
	})

	t.Run("invalid_domain_string_uses_default", func(t *testing.T) {
		coord := NewHybridQueryCoordinator(nil, nil, nil)

		query := &HybridQuery{
			TextQuery: "test",
			Filters: []QueryFilter{
				{Type: FilterDomain, Value: "invalid_domain_name"},
			},
		}

		d := coord.detectDomain(query)
		assert.Equal(t, domain.DomainLibrarian, d,
			"should fall back to default for invalid domain")
	})
}

func TestFinal_HQ64_DomainSpecificWeightApplication(t *testing.T) {
	// Test that domain-specific learned weights are applied correctly

	t.Run("different_domains_get_different_weights", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Train librarian domain to favor text
		librarianRanks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 10, GraphRank: 15},
		}
		for i := 0; i < 30; i++ {
			lqw.Update("lib_q", "doc", librarianRanks, domain.DomainLibrarian)
		}

		// Train engineer domain to favor graph
		engineerRanks := map[string]SourceRanks{
			"doc": {TextRank: 15, SemanticRank: 10, GraphRank: 0},
		}
		for i := 0; i < 30; i++ {
			lqw.Update("eng_q", "doc", engineerRanks, domain.DomainEngineer)
		}

		// Get weights for each domain
		libWeights := lqw.GetWeights(domain.DomainLibrarian, false)
		engWeights := lqw.GetWeights(domain.DomainEngineer, false)

		// Librarian should favor text
		assert.Greater(t, libWeights.TextWeight, libWeights.GraphWeight,
			"librarian should favor text")

		// Engineer should favor graph
		assert.Greater(t, engWeights.GraphWeight, engWeights.TextWeight,
			"engineer should favor graph")

		// Weights should be different across domains
		assert.NotEqual(t, libWeights.TextWeight, engWeights.TextWeight,
			"domains should have different text weights")
	})

	t.Run("coordinator_uses_domain_weights", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			results: []TextResult{{ID: "doc1", Score: 0.9}},
		}
		mockVector := &mockVectorFinal{
			results: []VectorResult{{ID: "doc2", Score: 0.85}},
		}

		bleve := NewBleveSearcher(mockBleve)
		vector := NewVectorSearcher(mockVector)
		coord := NewHybridQueryCoordinator(bleve, vector, nil)

		// Train coordinator's learned weights for a specific domain
		textResults := []TextResult{{ID: "clicked", Score: 0.9}}
		semanticResults := []VectorResult{{ID: "other", Score: 0.8}, {ID: "clicked", Score: 0.7}}

		// Multiple updates favoring text for architect domain
		for i := 0; i < 20; i++ {
			coord.UpdateWeights("q", "clicked", textResults, semanticResults, nil, domain.DomainArchitect)
		}

		// Verify domain-specific weights exist
		assert.True(t, coord.LearnedWeights().HasDomainWeights(domain.DomainArchitect))

		// Get stats for the domain
		stats := coord.LearnedWeights().GetDomainStats(domain.DomainArchitect)
		assert.Greater(t, stats.TextMean, 0.0)
	})
}

func TestFinal_HQ64_CrossDomainQueries(t *testing.T) {
	// Test queries that span multiple domains

	t.Run("untrained_domain_uses_global_weights", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Train only librarian domain
		ranks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
		}
		for i := 0; i < 30; i++ {
			lqw.Update("q", "doc", ranks, domain.DomainLibrarian)
		}

		// Get weights for untrained domain (Architect)
		archWeights := lqw.GetWeights(domain.DomainArchitect, false)

		// Should fall back to global weights (not librarian-specific)
		globalWeights := lqw.GetGlobalWeights()

		// Global weights should be used for untrained domain
		assert.InDelta(t, globalWeights.TextWeight, archWeights.TextWeight, 0.01,
			"untrained domain should use global weights")
	})

	t.Run("domain_isolation_maintained", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Train domain A heavily toward text
		textRanks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 20, GraphRank: 20},
		}
		for i := 0; i < 50; i++ {
			lqw.Update("q", "doc", textRanks, domain.DomainLibrarian)
		}

		// Train domain B heavily toward semantic
		semanticRanks := map[string]SourceRanks{
			"doc": {TextRank: 20, SemanticRank: 0, GraphRank: 20},
		}
		for i := 0; i < 50; i++ {
			lqw.Update("q", "doc", semanticRanks, domain.DomainEngineer)
		}

		// Verify isolation
		libWeights := lqw.GetWeights(domain.DomainLibrarian, false)
		engWeights := lqw.GetWeights(domain.DomainEngineer, false)

		assert.Greater(t, libWeights.TextWeight, libWeights.SemanticWeight)
		assert.Greater(t, engWeights.SemanticWeight, engWeights.TextWeight)
	})
}

func TestFinal_HQ64_DomainInheritanceInResults(t *testing.T) {
	// Test that domain context is maintained through query execution

	t.Run("query_filter_domain_affects_weight_selection", func(t *testing.T) {
		mockBleve := &mockBleveFinal{
			results: []TextResult{{ID: "doc1", Score: 0.9}},
		}
		mockVector := &mockVectorFinal{
			results: []VectorResult{{ID: "doc2", Score: 0.85}},
		}

		bleve := NewBleveSearcher(mockBleve)
		vector := NewVectorSearcher(mockVector)
		coord := NewHybridQueryCoordinator(bleve, vector, nil)

		// Pre-train domain weights
		lw := coord.LearnedWeights()

		// Librarian favors text
		libRanks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 10, GraphRank: -1},
		}
		for i := 0; i < 30; i++ {
			lw.Update("q", "doc", libRanks, domain.DomainLibrarian)
		}

		// Engineer favors semantic
		engRanks := map[string]SourceRanks{
			"doc": {TextRank: 10, SemanticRank: 0, GraphRank: -1},
		}
		for i := 0; i < 30; i++ {
			lw.Update("q", "doc", engRanks, domain.DomainEngineer)
		}

		// Execute query with librarian domain filter
		libQuery := &HybridQuery{
			TextQuery:      "test",
			SemanticVector: []float32{0.1, 0.2},
			Filters: []QueryFilter{
				{Type: FilterDomain, Value: domain.DomainLibrarian},
			},
			Limit: 10,
		}

		libResult, err := coord.ExecuteWithMetrics(context.Background(), libQuery)
		require.NoError(t, err)
		require.NotEmpty(t, libResult.Results)

		// Execute query with engineer domain filter
		engQuery := &HybridQuery{
			TextQuery:      "test",
			SemanticVector: []float32{0.1, 0.2},
			Filters: []QueryFilter{
				{Type: FilterDomain, Value: domain.DomainEngineer},
			},
			Limit: 10,
		}

		engResult, err := coord.ExecuteWithMetrics(context.Background(), engQuery)
		require.NoError(t, err)
		require.NotEmpty(t, engResult.Results)

		// Both queries should execute successfully
		// The ranking may differ based on learned weights, but this test
		// verifies the domain filter is processed correctly
		assert.NotNil(t, libResult.Metrics)
		assert.NotNil(t, engResult.Metrics)
	})

	t.Run("domain_reset_clears_specific_domain", func(t *testing.T) {
		lqw := NewLearnedQueryWeights()

		// Train multiple domains
		ranks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
		}
		for i := 0; i < 20; i++ {
			lqw.Update("q", "doc", ranks, domain.DomainLibrarian)
			lqw.Update("q", "doc", ranks, domain.DomainEngineer)
		}

		assert.True(t, lqw.HasDomainWeights(domain.DomainLibrarian))
		assert.True(t, lqw.HasDomainWeights(domain.DomainEngineer))

		// Reset only librarian
		lqw.ResetDomain(domain.DomainLibrarian)

		assert.False(t, lqw.HasDomainWeights(domain.DomainLibrarian))
		assert.True(t, lqw.HasDomainWeights(domain.DomainEngineer),
			"engineer domain should be unaffected")
	})
}

// =============================================================================
// Additional Deterministic and Reproducibility Tests
// =============================================================================

func TestFinal_RRF_DeterministicWithSameInputs(t *testing.T) {
	// Verify RRF produces identical results for identical inputs

	rrf := NewRRFFusion(60)

	textResults := []TextResult{
		{ID: "doc1", Score: 0.9},
		{ID: "doc2", Score: 0.8},
		{ID: "doc3", Score: 0.7},
	}
	semanticResults := []VectorResult{
		{ID: "doc2", Score: 0.95},
		{ID: "doc1", Score: 0.85},
	}
	graphResults := []GraphResult{
		{ID: "doc3", Score: 0.88},
		{ID: "doc4", Score: 0.75},
	}

	weights := DefaultQueryWeights()

	// Execute multiple times
	results1 := rrf.Fuse(textResults, semanticResults, graphResults, weights)
	results2 := rrf.Fuse(textResults, semanticResults, graphResults, weights)
	results3 := rrf.Fuse(textResults, semanticResults, graphResults, weights)

	// All results should be identical
	require.Equal(t, len(results1), len(results2))
	require.Equal(t, len(results1), len(results3))

	for i := range results1 {
		assert.Equal(t, results1[i].ID, results2[i].ID)
		assert.Equal(t, results1[i].ID, results3[i].ID)
		assert.InDelta(t, results1[i].Score, results2[i].Score, 1e-15)
		assert.InDelta(t, results1[i].Score, results3[i].Score, 1e-15)
	}
}

func TestFinal_IntegrationReasonableTimingTolerance(t *testing.T) {
	// Test with reasonable timing tolerances for CI environments

	mockBleve := &mockBleveFinal{
		results: []TextResult{{ID: "doc1", Score: 0.9}},
		delay:   20 * time.Millisecond,
	}
	mockVector := &mockVectorFinal{
		results: []VectorResult{{ID: "doc2", Score: 0.85}},
		delay:   20 * time.Millisecond,
	}
	mockGraph := &mockGraphFinal{
		results: []GraphResult{{ID: "doc3", Score: 0.8}},
		nodes:   []string{"start"},
		delay:   20 * time.Millisecond,
	}

	bleve := NewBleveSearcher(mockBleve)
	vector := NewVectorSearcher(mockVector)
	graph := NewGraphTraverser(mockGraph)
	coord := NewHybridQueryCoordinator(bleve, vector, graph)
	coord.SetTimeout(5 * time.Second)

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
	result, err := coord.ExecuteWithMetrics(context.Background(), query)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.NotEmpty(t, result.Results)

	// With parallel execution, should complete in roughly the time of the slowest source
	// plus some overhead, not the sum of all delays
	assert.Less(t, elapsed, 100*time.Millisecond,
		"parallel execution should be faster than sequential: got %v", elapsed)
}

// =============================================================================
// Benchmark Tests for Final Integration
// =============================================================================

func BenchmarkFinal_RRFFusion_LargeResultSets(b *testing.B) {
	rrf := NewRRFFusion(60)

	// Create large result sets
	textResults := make([]TextResult, 100)
	semanticResults := make([]VectorResult, 100)
	graphResults := make([]GraphResult, 100)

	for i := 0; i < 100; i++ {
		id := "doc" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		textResults[i] = TextResult{ID: id, Score: float64(100-i) / 100.0}
		semanticResults[i] = VectorResult{ID: id, Score: float64(100-i) / 100.0}
		graphResults[i] = GraphResult{ID: id, Score: float64(100-i) / 100.0}
	}

	weights := DefaultQueryWeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rrf.Fuse(textResults, semanticResults, graphResults, weights)
	}
}

func BenchmarkFinal_LearnedWeightsUpdate_HighFrequency(b *testing.B) {
	lqw := NewLearnedQueryWeights()
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lqw.Update("query", "doc", ranks, domain.DomainLibrarian)
	}
}

func BenchmarkFinal_DomainWeightRetrieval(b *testing.B) {
	lqw := NewLearnedQueryWeights()

	// Pre-train some domains
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}
	for i := 0; i < 50; i++ {
		lqw.Update("q", "doc", ranks, domain.DomainLibrarian)
		lqw.Update("q", "doc", ranks, domain.DomainEngineer)
		lqw.Update("q", "doc", ranks, domain.DomainArchitect)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lqw.GetWeights(domain.DomainLibrarian, false)
		lqw.GetWeights(domain.DomainEngineer, true)
		lqw.GetWeights(domain.DomainArchitect, false)
	}
}
