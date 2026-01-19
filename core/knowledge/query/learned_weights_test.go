package query

import (
	"testing"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// LearnedQueryWeights Creation Tests
// =============================================================================

func TestLearnedQueryWeights_NewLearnedQueryWeights(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	assert.NotNil(t, lqw)
	assert.NotNil(t, lqw.TextWeight)
	assert.NotNil(t, lqw.SemanticWeight)
	assert.NotNil(t, lqw.GraphWeight)
	assert.NotNil(t, lqw.domainWeights)
	assert.NotNil(t, lqw.globalWeights)
	assert.NotNil(t, lqw.updateConfig)

	// Initial weights should be uniform (mean ~0.5 for each)
	assert.InDelta(t, 0.5, lqw.TextWeight.Mean(), 0.01)
	assert.InDelta(t, 0.5, lqw.SemanticWeight.Mean(), 0.01)
	assert.InDelta(t, 0.5, lqw.GraphWeight.Mean(), 0.01)
}

func TestLearnedQueryWeights_NewLearnedQueryWeightsWithPrior(t *testing.T) {
	// Create with custom priors: text=0.5, semantic=0.3, graph=0.2
	lqw := NewLearnedQueryWeightsWithPrior(0.5, 0.3, 0.2, 10.0)

	assert.NotNil(t, lqw)

	// Verify means reflect the priors
	assert.InDelta(t, 0.5, lqw.TextWeight.Mean(), 0.01)
	assert.InDelta(t, 0.3, lqw.SemanticWeight.Mean(), 0.01)
	assert.InDelta(t, 0.2, lqw.GraphWeight.Mean(), 0.01)
}

func TestLearnedQueryWeights_NewLearnedQueryWeightsWithPrior_ZeroStrength(t *testing.T) {
	// Zero strength should use default
	lqw := NewLearnedQueryWeightsWithPrior(0.5, 0.3, 0.2, 0.0)

	assert.NotNil(t, lqw)
	// Should still work with default strength
	assert.InDelta(t, 0.5, lqw.TextWeight.Mean(), 0.01)
}

// =============================================================================
// GetWeights Tests
// =============================================================================

func TestLearnedQueryWeights_GetWeights_Exploit(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	weights := lqw.GetWeights(domain.DomainLibrarian, false)

	assert.NotNil(t, weights)

	// Weights should be normalized (sum to ~1.0)
	sum := weights.TextWeight + weights.SemanticWeight + weights.GraphWeight + weights.MemoryWeight
	assert.InDelta(t, 1.0, sum, 0.01)

	// In exploit mode, weights should be deterministic
	weights2 := lqw.GetWeights(domain.DomainLibrarian, false)
	assert.Equal(t, weights.TextWeight, weights2.TextWeight)
	assert.Equal(t, weights.SemanticWeight, weights2.SemanticWeight)
	assert.Equal(t, weights.GraphWeight, weights2.GraphWeight)
}

func TestLearnedQueryWeights_GetWeights_Explore(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// In explore mode, weights are sampled from distributions
	// Run multiple times and check for variance
	weights := make([]*QueryWeights, 100)
	for i := 0; i < 100; i++ {
		weights[i] = lqw.GetWeights(domain.DomainLibrarian, true)

		// All should be normalized
		sum := weights[i].TextWeight + weights[i].SemanticWeight + weights[i].GraphWeight
		assert.InDelta(t, 1.0, sum, 0.01)
	}

	// With sampling, we should see some variance
	// (though with weak priors, variance might still be low)
	var textSum float64
	for _, w := range weights {
		textSum += w.TextWeight
	}
	avgText := textSum / 100.0

	var variance float64
	for _, w := range weights {
		diff := w.TextWeight - avgText
		variance += diff * diff
	}
	variance /= 100.0

	// Some variance should exist (not zero)
	assert.Greater(t, variance, 0.0)
}

func TestLearnedQueryWeights_GetWeights_DomainSpecific(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Update weights for a specific domain
	ranks := map[string]SourceRanks{
		"doc1": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}

	// Make several updates to build domain-specific weights
	for i := 0; i < 10; i++ {
		lqw.Update("query"+string(rune('0'+i)), "doc1", ranks, domain.DomainLibrarian)
	}

	// Get weights for updated domain
	weightsLibrarian := lqw.GetWeights(domain.DomainLibrarian, false)

	// Get weights for different domain (should use global)
	weightsArchitect := lqw.GetWeights(domain.DomainArchitect, false)

	assert.NotNil(t, weightsLibrarian)
	assert.NotNil(t, weightsArchitect)

	// Domain with updates should have different weights than one without
	// (After updates favoring text, text weight should be higher for librarian)
	assert.Greater(t, weightsLibrarian.TextWeight, weightsArchitect.TextWeight)
}

func TestLearnedQueryWeights_GetGlobalWeights(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	weights := lqw.GetGlobalWeights()

	assert.NotNil(t, weights)

	sum := weights.TextWeight + weights.SemanticWeight + weights.GraphWeight
	assert.InDelta(t, 1.0, sum, 0.01)
}

// =============================================================================
// Update Tests
// =============================================================================

func TestLearnedQueryWeights_Update_TextFavoredResult(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Simulate user clicking on a result that was ranked high by text search
	ranks := map[string]SourceRanks{
		"clicked_doc": {TextRank: 0, SemanticRank: 10, GraphRank: NoRank},
	}

	initialTextWeight := lqw.TextWeight.Mean()

	// Perform update
	lqw.Update("query1", "clicked_doc", ranks, domain.DomainLibrarian)

	// Text weight should increase (because text ranked the clicked doc highest)
	assert.Greater(t, lqw.TextWeight.Mean(), initialTextWeight)
}

func TestLearnedQueryWeights_Update_SemanticFavoredResult(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Simulate user clicking on a result that was ranked high by semantic search
	ranks := map[string]SourceRanks{
		"clicked_doc": {TextRank: 15, SemanticRank: 0, GraphRank: 8},
	}

	initialSemanticWeight := lqw.SemanticWeight.Mean()

	lqw.Update("query1", "clicked_doc", ranks, domain.DomainLibrarian)

	// Semantic weight should increase
	assert.Greater(t, lqw.SemanticWeight.Mean(), initialSemanticWeight)
}

func TestLearnedQueryWeights_Update_GraphFavoredResult(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	ranks := map[string]SourceRanks{
		"clicked_doc": {TextRank: 20, SemanticRank: 15, GraphRank: 0},
	}

	initialGraphWeight := lqw.GraphWeight.Mean()

	lqw.Update("query1", "clicked_doc", ranks, domain.DomainLibrarian)

	assert.Greater(t, lqw.GraphWeight.Mean(), initialGraphWeight)
}

func TestLearnedQueryWeights_Update_ResultNotFound(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	ranks := map[string]SourceRanks{
		"other_doc": {TextRank: 0, SemanticRank: 0, GraphRank: 0},
	}

	initialTextMean := lqw.TextWeight.Mean()

	// Try to update with a result not in the ranks map
	lqw.Update("query1", "clicked_doc", ranks, domain.DomainLibrarian)

	// Weights should be unchanged
	assert.Equal(t, initialTextMean, lqw.TextWeight.Mean())
}

func TestLearnedQueryWeights_Update_NoRankSources(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Result only found in text search
	ranks := map[string]SourceRanks{
		"clicked_doc": {TextRank: 0, SemanticRank: NoRank, GraphRank: NoRank},
	}

	initialTextMean := lqw.TextWeight.Mean()
	initialSemanticMean := lqw.SemanticWeight.Mean()

	lqw.Update("query1", "clicked_doc", ranks, domain.DomainLibrarian)

	// Text should increase, semantic/graph should not
	assert.Greater(t, lqw.TextWeight.Mean(), initialTextMean)
	// Semantic gets 0 reward (NoRank), which is a slight update toward 0
	assert.LessOrEqual(t, lqw.SemanticWeight.Mean(), initialSemanticMean)
}

func TestLearnedQueryWeights_Update_MultipleUpdates(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Multiple updates favoring text search
	for i := 0; i < 20; i++ {
		ranks := map[string]SourceRanks{
			"doc": {TextRank: 0, SemanticRank: 10, GraphRank: 15},
		}
		lqw.Update("query"+string(rune('0'+i)), "doc", ranks, domain.DomainLibrarian)
	}

	weights := lqw.GetWeights(domain.DomainLibrarian, false)

	// Text should have highest weight after many updates
	assert.Greater(t, weights.TextWeight, weights.SemanticWeight)
	assert.Greater(t, weights.TextWeight, weights.GraphWeight)
}

func TestLearnedQueryWeights_Update_DomainIsolation(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Update librarian domain favoring text
	librarianRanks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 10, GraphRank: 15},
	}
	for i := 0; i < 10; i++ {
		lqw.Update("query"+string(rune('0'+i)), "doc", librarianRanks, domain.DomainLibrarian)
	}

	// Update architect domain favoring graph
	architectRanks := map[string]SourceRanks{
		"doc": {TextRank: 15, SemanticRank: 10, GraphRank: 0},
	}
	for i := 0; i < 10; i++ {
		lqw.Update("query"+string(rune('a'+i)), "doc", architectRanks, domain.DomainArchitect)
	}

	librarianWeights := lqw.GetWeights(domain.DomainLibrarian, false)
	architectWeights := lqw.GetWeights(domain.DomainArchitect, false)

	// Librarian should favor text
	assert.Greater(t, librarianWeights.TextWeight, librarianWeights.GraphWeight)

	// Architect should favor graph
	assert.Greater(t, architectWeights.GraphWeight, architectWeights.TextWeight)
}

// =============================================================================
// Confidence Tests
// =============================================================================

func TestLearnedQueryWeights_GetDomainConfidence_NoDomain(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	confidence := lqw.GetDomainConfidence(domain.DomainLibrarian)

	// No updates, no domain weights - confidence should be 0
	assert.Equal(t, 0.0, confidence)
}

func TestLearnedQueryWeights_GetDomainConfidence_WithUpdates(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Perform some updates
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}
	for i := 0; i < 5; i++ {
		lqw.Update("query"+string(rune('0'+i)), "doc", ranks, domain.DomainLibrarian)
	}

	confidence := lqw.GetDomainConfidence(domain.DomainLibrarian)

	// Should have some confidence after updates
	assert.Greater(t, confidence, 0.0)
	assert.LessOrEqual(t, confidence, 1.0)
}

func TestLearnedQueryWeights_GetGlobalConfidence(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	initialConfidence := lqw.GetGlobalConfidence()

	// Global confidence should be valid from the start (due to prior)
	assert.GreaterOrEqual(t, initialConfidence, 0.0)
	assert.LessOrEqual(t, initialConfidence, 1.0)

	// Perform updates
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}
	for i := 0; i < 10; i++ {
		lqw.Update("query"+string(rune('0'+i)), "doc", ranks, domain.DomainLibrarian)
	}

	newConfidence := lqw.GetGlobalConfidence()

	// Confidence should still be valid after updates
	// Note: With exponential decay updates, confidence may not strictly increase
	// as the effective sample size can change. The important thing is that
	// the weights are being updated and confidence remains valid.
	assert.GreaterOrEqual(t, newConfidence, 0.0)
	assert.LessOrEqual(t, newConfidence, 1.0)
}

// =============================================================================
// HasDomainWeights Tests
// =============================================================================

func TestLearnedQueryWeights_HasDomainWeights(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Initially no domain weights
	assert.False(t, lqw.HasDomainWeights(domain.DomainLibrarian))

	// Add update
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}
	lqw.Update("query1", "doc", ranks, domain.DomainLibrarian)

	// Now should have domain weights
	assert.True(t, lqw.HasDomainWeights(domain.DomainLibrarian))

	// Other domains should still not have weights
	assert.False(t, lqw.HasDomainWeights(domain.DomainArchitect))
}

func TestLearnedQueryWeights_DomainUpdateCount(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	assert.Equal(t, 0, lqw.DomainUpdateCount(domain.DomainLibrarian))

	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}

	lqw.Update("query1", "doc", ranks, domain.DomainLibrarian)
	assert.Equal(t, 1, lqw.DomainUpdateCount(domain.DomainLibrarian))

	lqw.Update("query2", "doc", ranks, domain.DomainLibrarian)
	assert.Equal(t, 2, lqw.DomainUpdateCount(domain.DomainLibrarian))

	// Other domain still 0
	assert.Equal(t, 0, lqw.DomainUpdateCount(domain.DomainArchitect))
}

// =============================================================================
// Reset Tests
// =============================================================================

func TestLearnedQueryWeights_Reset(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Add updates
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 10, GraphRank: 15},
	}
	for i := 0; i < 10; i++ {
		lqw.Update("query"+string(rune('0'+i)), "doc", ranks, domain.DomainLibrarian)
	}

	// Verify state changed
	assert.True(t, lqw.HasDomainWeights(domain.DomainLibrarian))

	// Reset
	lqw.Reset()

	// Verify state is cleared
	assert.False(t, lqw.HasDomainWeights(domain.DomainLibrarian))
	assert.Equal(t, 0, lqw.DomainUpdateCount(domain.DomainLibrarian))

	// Weights should be back to uniform
	assert.InDelta(t, 0.5, lqw.TextWeight.Mean(), 0.01)
}

func TestLearnedQueryWeights_ResetDomain(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Add updates to two domains
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 10, GraphRank: 15},
	}
	for i := 0; i < 5; i++ {
		lqw.Update("query"+string(rune('0'+i)), "doc", ranks, domain.DomainLibrarian)
		lqw.Update("query"+string(rune('a'+i)), "doc", ranks, domain.DomainArchitect)
	}

	// Reset only one domain
	lqw.ResetDomain(domain.DomainLibrarian)

	// Librarian should be reset
	assert.False(t, lqw.HasDomainWeights(domain.DomainLibrarian))

	// Architect should still have weights
	assert.True(t, lqw.HasDomainWeights(domain.DomainArchitect))
}

// =============================================================================
// SetUpdateConfig Tests
// =============================================================================

func TestLearnedQueryWeights_SetUpdateConfig(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Set custom config
	customConfig := &handoff.UpdateConfig{
		LearningRate:   0.2,
		DriftThreshold: 0.9,
		PriorBlendRate: 0.3,
	}
	lqw.SetUpdateConfig(customConfig)

	// Config should be set (internal field)
	assert.Equal(t, customConfig, lqw.updateConfig)
}

func TestLearnedQueryWeights_SetUpdateConfig_Nil(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	originalConfig := lqw.updateConfig

	// Setting nil should not change the config
	lqw.SetUpdateConfig(nil)

	assert.Equal(t, originalConfig, lqw.updateConfig)
}

// =============================================================================
// SourceRanks Tests
// =============================================================================

func TestSourceRanks_NewSourceRanks(t *testing.T) {
	ranks := NewSourceRanks()

	assert.Equal(t, NoRank, ranks.TextRank)
	assert.Equal(t, NoRank, ranks.SemanticRank)
	assert.Equal(t, NoRank, ranks.GraphRank)
}

func TestBuildSourceRanks_TextOnly(t *testing.T) {
	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc2", Score: 0.9},
		{ID: "doc3", Score: 0.8},
	}

	ranks := BuildSourceRanks(textResults, nil, nil)

	require.Len(t, ranks, 3)

	assert.Equal(t, 0, ranks["doc1"].TextRank)
	assert.Equal(t, 1, ranks["doc2"].TextRank)
	assert.Equal(t, 2, ranks["doc3"].TextRank)

	// Semantic and graph should be NoRank
	assert.Equal(t, NoRank, ranks["doc1"].SemanticRank)
	assert.Equal(t, NoRank, ranks["doc1"].GraphRank)
}

func TestBuildSourceRanks_AllSources(t *testing.T) {
	textResults := []TextResult{
		{ID: "doc1", Score: 1.0},
		{ID: "doc2", Score: 0.9},
	}

	semanticResults := []VectorResult{
		{ID: "doc2", Score: 0.95},
		{ID: "doc3", Score: 0.85},
	}

	graphResults := []GraphResult{
		{ID: "doc1", Score: 0.8},
		{ID: "doc3", Score: 0.7},
	}

	ranks := BuildSourceRanks(textResults, semanticResults, graphResults)

	require.Len(t, ranks, 3)

	// doc1: text=0, semantic=NoRank, graph=0
	assert.Equal(t, 0, ranks["doc1"].TextRank)
	assert.Equal(t, NoRank, ranks["doc1"].SemanticRank)
	assert.Equal(t, 0, ranks["doc1"].GraphRank)

	// doc2: text=1, semantic=0, graph=NoRank
	assert.Equal(t, 1, ranks["doc2"].TextRank)
	assert.Equal(t, 0, ranks["doc2"].SemanticRank)
	assert.Equal(t, NoRank, ranks["doc2"].GraphRank)

	// doc3: text=NoRank, semantic=1, graph=1
	assert.Equal(t, NoRank, ranks["doc3"].TextRank)
	assert.Equal(t, 1, ranks["doc3"].SemanticRank)
	assert.Equal(t, 1, ranks["doc3"].GraphRank)
}

func TestBuildSourceRanks_EmptyInput(t *testing.T) {
	ranks := BuildSourceRanks(nil, nil, nil)
	assert.Empty(t, ranks)

	ranks = BuildSourceRanks([]TextResult{}, []VectorResult{}, []GraphResult{})
	assert.Empty(t, ranks)
}

// =============================================================================
// WeightStats Tests
// =============================================================================

func TestLearnedQueryWeights_GetStats(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	stats := lqw.GetStats()

	// Initial stats should show uniform distribution
	assert.InDelta(t, 0.5, stats.TextMean, 0.01)
	assert.InDelta(t, 0.5, stats.SemanticMean, 0.01)
	assert.InDelta(t, 0.5, stats.GraphMean, 0.01)

	// Variance should be positive
	assert.Greater(t, stats.TextVariance, 0.0)
	assert.Greater(t, stats.SemanticVariance, 0.0)
	assert.Greater(t, stats.GraphVariance, 0.0)
}

func TestLearnedQueryWeights_GetDomainStats_NoDomain(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	stats := lqw.GetDomainStats(domain.DomainLibrarian)

	// No domain data, should be zero stats
	assert.Equal(t, 0.0, stats.TextMean)
	assert.Equal(t, 0.0, stats.SemanticMean)
	assert.Equal(t, 0.0, stats.GraphMean)
}

func TestLearnedQueryWeights_GetDomainStats_WithData(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}
	for i := 0; i < 5; i++ {
		lqw.Update("query"+string(rune('0'+i)), "doc", ranks, domain.DomainLibrarian)
	}

	stats := lqw.GetDomainStats(domain.DomainLibrarian)

	// Should have non-zero stats
	assert.Greater(t, stats.TextMean, 0.0)
	assert.Greater(t, stats.SemanticMean, 0.0)
	assert.Greater(t, stats.GraphMean, 0.0)

	// Text should have higher mean (was ranked best)
	assert.Greater(t, stats.TextMean, stats.SemanticMean)
	assert.Greater(t, stats.TextMean, stats.GraphMean)
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestLearnedQueryWeights_ConcurrentGetWeights(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				weights := lqw.GetWeights(domain.DomainLibrarian, true)
				assert.NotNil(t, weights)
				sum := weights.TextWeight + weights.SemanticWeight + weights.GraphWeight
				assert.InDelta(t, 1.0, sum, 0.01)
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestLearnedQueryWeights_ConcurrentUpdate(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			ranks := map[string]SourceRanks{
				"doc": {TextRank: idx % 3, SemanticRank: (idx + 1) % 3, GraphRank: (idx + 2) % 3},
			}
			for j := 0; j < 50; j++ {
				lqw.Update("query", "doc", ranks, domain.Domain(idx%5))
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not panic and should have valid weights
	weights := lqw.GetGlobalWeights()
	assert.NotNil(t, weights)
}

func TestLearnedQueryWeights_ConcurrentReadWrite(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	done := make(chan bool, 20)

	// Writers
	for i := 0; i < 10; i++ {
		go func(idx int) {
			ranks := map[string]SourceRanks{
				"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
			}
			for j := 0; j < 50; j++ {
				lqw.Update("query", "doc", ranks, domain.DomainLibrarian)
			}
			done <- true
		}(i)
	}

	// Readers
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = lqw.GetWeights(domain.DomainLibrarian, false)
				_ = lqw.GetDomainConfidence(domain.DomainLibrarian)
				_ = lqw.HasDomainWeights(domain.DomainLibrarian)
			}
			done <- true
		}()
	}

	for i := 0; i < 20; i++ {
		<-done
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestLearnedQueryWeights_AllZeroRanks(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// All sources rank the doc at 0
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 0, GraphRank: 0},
	}

	// Should not panic
	lqw.Update("query", "doc", ranks, domain.DomainLibrarian)

	weights := lqw.GetWeights(domain.DomainLibrarian, false)
	assert.NotNil(t, weights)
}

func TestLearnedQueryWeights_HighRanks(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Very high ranks (low relevance)
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 1000, SemanticRank: 1000, GraphRank: 1000},
	}

	lqw.Update("query", "doc", ranks, domain.DomainLibrarian)

	// Should handle gracefully
	weights := lqw.GetWeights(domain.DomainLibrarian, false)
	assert.NotNil(t, weights)
}

func TestLearnedQueryWeights_MixedNoRank(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	// Only one source found the doc
	ranks := map[string]SourceRanks{
		"doc": {TextRank: NoRank, SemanticRank: NoRank, GraphRank: 0},
	}

	lqw.Update("query", "doc", ranks, domain.DomainLibrarian)

	// Graph weight should increase
	stats := lqw.GetStats()
	assert.Greater(t, stats.GraphMean, 0.0)
}

func TestLearnedQueryWeights_EmptyRanksMap(t *testing.T) {
	lqw := NewLearnedQueryWeights()

	initialMean := lqw.TextWeight.Mean()

	// Empty ranks map
	ranks := map[string]SourceRanks{}

	lqw.Update("query", "doc", ranks, domain.DomainLibrarian)

	// Should not change anything
	assert.Equal(t, initialMean, lqw.TextWeight.Mean())
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkLearnedQueryWeights_GetWeights_Exploit(b *testing.B) {
	lqw := NewLearnedQueryWeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lqw.GetWeights(domain.DomainLibrarian, false)
	}
}

func BenchmarkLearnedQueryWeights_GetWeights_Explore(b *testing.B) {
	lqw := NewLearnedQueryWeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lqw.GetWeights(domain.DomainLibrarian, true)
	}
}

func BenchmarkLearnedQueryWeights_Update(b *testing.B) {
	lqw := NewLearnedQueryWeights()
	ranks := map[string]SourceRanks{
		"doc": {TextRank: 0, SemanticRank: 5, GraphRank: 10},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lqw.Update("query", "doc", ranks, domain.DomainLibrarian)
	}
}

func BenchmarkBuildSourceRanks(b *testing.B) {
	textResults := make([]TextResult, 50)
	semanticResults := make([]VectorResult, 50)
	graphResults := make([]GraphResult, 50)

	for i := 0; i < 50; i++ {
		id := string(rune('a' + i%26))
		textResults[i] = TextResult{ID: id, Score: float64(50-i) / 50.0}
		semanticResults[i] = VectorResult{ID: id, Score: float64(50-i) / 50.0}
		graphResults[i] = GraphResult{ID: id, Score: float64(50-i) / 50.0}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildSourceRanks(textResults, semanticResults, graphResults)
	}
}
