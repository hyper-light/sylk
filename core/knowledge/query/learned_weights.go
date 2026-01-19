// Package query provides hybrid query execution for the knowledge system.
// LearnedQueryWeights implements adaptive weight learning using Thompson Sampling.
package query

import (
	"sync"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/handoff"
)

// =============================================================================
// Learned Query Weights
// =============================================================================

// LearnedQueryWeights provides adaptive weight learning for hybrid search.
// It maintains per-domain learned weights and uses Thompson Sampling for
// exploration-exploitation balance during weight selection.
//
// The learning process updates weights based on user feedback (clicked results)
// to favor sources that consistently rank clicked results highest.
type LearnedQueryWeights struct {
	// Learned weight distributions for each source
	TextWeight     *handoff.LearnedWeight
	SemanticWeight *handoff.LearnedWeight
	GraphWeight    *handoff.LearnedWeight

	// Per-domain weight overrides (learned separately per domain)
	domainWeights map[domain.Domain]*DomainQueryWeights

	// Global fallback weights (used when domain-specific unavailable)
	globalWeights *QueryWeights

	// Update configuration for Bayesian learning
	updateConfig *handoff.UpdateConfig

	// mu protects concurrent access to weights
	mu sync.RWMutex
}

// DomainQueryWeights holds learned weights for a specific domain.
type DomainQueryWeights struct {
	Domain         domain.Domain
	TextWeight     *handoff.LearnedWeight
	SemanticWeight *handoff.LearnedWeight
	GraphWeight    *handoff.LearnedWeight
	updateCount    int
}

// NewLearnedQueryWeights creates a new LearnedQueryWeights with uniform priors.
// All sources start with equal weight probability distributions.
func NewLearnedQueryWeights() *LearnedQueryWeights {
	return &LearnedQueryWeights{
		// Initialize with weak uniform priors (Beta(2,2) centered at 0.5)
		TextWeight:     handoff.NewLearnedWeight(2.0, 2.0),
		SemanticWeight: handoff.NewLearnedWeight(2.0, 2.0),
		GraphWeight:    handoff.NewLearnedWeight(2.0, 2.0),

		domainWeights: make(map[domain.Domain]*DomainQueryWeights),
		globalWeights: DefaultQueryWeights(),
		updateConfig:  handoff.DefaultUpdateConfig(),
	}
}

// NewLearnedQueryWeightsWithPrior creates LearnedQueryWeights with custom priors.
// priorStrength controls how strongly the prior influences learning (higher = more conservative).
func NewLearnedQueryWeightsWithPrior(textPrior, semanticPrior, graphPrior float64, priorStrength float64) *LearnedQueryWeights {
	if priorStrength <= 0 {
		priorStrength = 4.0 // Default: moderate prior strength
	}

	// Convert desired means to Beta parameters
	// Beta distribution mean = alpha / (alpha + beta)
	// With alpha = mean * strength and beta = (1-mean) * strength
	textAlpha := textPrior * priorStrength
	textBeta := (1 - textPrior) * priorStrength

	semanticAlpha := semanticPrior * priorStrength
	semanticBeta := (1 - semanticPrior) * priorStrength

	graphAlpha := graphPrior * priorStrength
	graphBeta := (1 - graphPrior) * priorStrength

	return &LearnedQueryWeights{
		TextWeight:     handoff.NewLearnedWeight(textAlpha, textBeta),
		SemanticWeight: handoff.NewLearnedWeight(semanticAlpha, semanticBeta),
		GraphWeight:    handoff.NewLearnedWeight(graphAlpha, graphBeta),

		domainWeights: make(map[domain.Domain]*DomainQueryWeights),
		globalWeights: &QueryWeights{
			TextWeight:     textPrior,
			SemanticWeight: semanticPrior,
			GraphWeight:    graphPrior,
		},
		updateConfig: handoff.DefaultUpdateConfig(),
	}
}

// SetUpdateConfig sets the Bayesian update configuration.
func (lqw *LearnedQueryWeights) SetUpdateConfig(config *handoff.UpdateConfig) {
	lqw.mu.Lock()
	defer lqw.mu.Unlock()

	if config != nil {
		lqw.updateConfig = config
	}
}

// GetWeights returns query weights for the specified domain.
// If explore is true, weights are sampled using Thompson Sampling.
// If explore is false, mean values are used (exploitation mode).
func (lqw *LearnedQueryWeights) GetWeights(d domain.Domain, explore bool) *QueryWeights {
	lqw.mu.RLock()
	defer lqw.mu.RUnlock()

	// Check for domain-specific weights
	domainW, hasDomain := lqw.domainWeights[d]

	var textW, semanticW, graphW float64

	if hasDomain && domainW.updateCount > 0 {
		// Use domain-specific weights
		textW, semanticW, graphW = lqw.sampleOrMean(
			domainW.TextWeight,
			domainW.SemanticWeight,
			domainW.GraphWeight,
			explore,
		)
	} else {
		// Fall back to global weights
		textW, semanticW, graphW = lqw.sampleOrMean(
			lqw.TextWeight,
			lqw.SemanticWeight,
			lqw.GraphWeight,
			explore,
		)
	}

	// Normalize to sum to 1.0
	sum := textW + semanticW + graphW
	if sum == 0 {
		return DefaultQueryWeights()
	}

	return &QueryWeights{
		TextWeight:     textW / sum,
		SemanticWeight: semanticW / sum,
		GraphWeight:    graphW / sum,
	}
}

// sampleOrMean returns sampled or mean values based on explore flag.
func (lqw *LearnedQueryWeights) sampleOrMean(
	text, semantic, graph *handoff.LearnedWeight,
	explore bool,
) (float64, float64, float64) {
	if explore {
		return text.Sample(), semantic.Sample(), graph.Sample()
	}
	return text.Mean(), semantic.Mean(), graph.Mean()
}

// GetGlobalWeights returns the global fallback weights using mean values.
func (lqw *LearnedQueryWeights) GetGlobalWeights() *QueryWeights {
	lqw.mu.RLock()
	defer lqw.mu.RUnlock()

	textW := lqw.TextWeight.Mean()
	semanticW := lqw.SemanticWeight.Mean()
	graphW := lqw.GraphWeight.Mean()

	sum := textW + semanticW + graphW
	if sum == 0 {
		return DefaultQueryWeights()
	}

	return &QueryWeights{
		TextWeight:     textW / sum,
		SemanticWeight: semanticW / sum,
		GraphWeight:    graphW / sum,
	}
}

// Update updates weights based on which result the user clicked/used.
// resultRanks maps result IDs to their rank in each source's results.
// The source that ranked the clicked result highest gets increased weight.
func (lqw *LearnedQueryWeights) Update(
	queryID string,
	clickedResultID string,
	resultRanks map[string]SourceRanks,
	d domain.Domain,
) {
	lqw.mu.Lock()
	defer lqw.mu.Unlock()

	// Get ranks for the clicked result
	ranks, exists := resultRanks[clickedResultID]
	if !exists {
		return // Result wasn't in any of the source results
	}

	// Calculate reward for each source based on rank
	// Better rank (lower number) = higher reward
	// No rank (not in results) = 0 reward
	textReward := lqw.calculateRankReward(ranks.TextRank)
	semanticReward := lqw.calculateRankReward(ranks.SemanticRank)
	graphReward := lqw.calculateRankReward(ranks.GraphRank)

	// Update global weights
	lqw.TextWeight.Update(textReward, lqw.updateConfig)
	lqw.SemanticWeight.Update(semanticReward, lqw.updateConfig)
	lqw.GraphWeight.Update(graphReward, lqw.updateConfig)

	// Update domain-specific weights
	lqw.updateDomainWeights(d, textReward, semanticReward, graphReward)
}

// calculateRankReward converts a rank to a reward value in [0, 1].
// Rank -1 means the result wasn't found in that source (0 reward).
// Rank 0 is the best (reward ~1.0), with exponential decay for lower ranks.
func (lqw *LearnedQueryWeights) calculateRankReward(rank int) float64 {
	if rank < 0 {
		return 0.0 // Result not in this source
	}

	// Exponential decay: reward = exp(-rank / 10)
	// Rank 0: ~1.0, Rank 5: ~0.61, Rank 10: ~0.37, Rank 20: ~0.14
	return exponentialDecay(float64(rank), 10.0)
}

// exponentialDecay computes exp(-x / scale).
func exponentialDecay(x, scale float64) float64 {
	if scale <= 0 {
		scale = 1.0
	}
	return exp(-x / scale)
}

// exp is a simple exponential function.
func exp(x float64) float64 {
	// Using Taylor series approximation for small values
	// or falling back to 0 for very negative values
	if x < -10 {
		return 0.0
	}
	if x > 10 {
		return 22026.0 // Cap at exp(10)
	}

	// More accurate calculation using standard library behavior simulation
	result := 1.0
	term := 1.0
	for i := 1; i < 20; i++ {
		term *= x / float64(i)
		result += term
		if term < 1e-15 && term > -1e-15 {
			break
		}
	}
	return result
}

// updateDomainWeights updates or creates domain-specific weights.
func (lqw *LearnedQueryWeights) updateDomainWeights(
	d domain.Domain,
	textReward, semanticReward, graphReward float64,
) {
	domainW, exists := lqw.domainWeights[d]
	if !exists {
		// Initialize domain-specific weights with current global distribution
		domainW = &DomainQueryWeights{
			Domain:         d,
			TextWeight:     handoff.NewLearnedWeight(lqw.TextWeight.Alpha, lqw.TextWeight.Beta),
			SemanticWeight: handoff.NewLearnedWeight(lqw.SemanticWeight.Alpha, lqw.SemanticWeight.Beta),
			GraphWeight:    handoff.NewLearnedWeight(lqw.GraphWeight.Alpha, lqw.GraphWeight.Beta),
			updateCount:    0,
		}
		lqw.domainWeights[d] = domainW
	}

	// Update domain weights
	domainW.TextWeight.Update(textReward, lqw.updateConfig)
	domainW.SemanticWeight.Update(semanticReward, lqw.updateConfig)
	domainW.GraphWeight.Update(graphReward, lqw.updateConfig)
	domainW.updateCount++
}

// GetDomainConfidence returns a confidence score for the domain's learned weights.
// Higher values indicate more learning data and more stable weights.
func (lqw *LearnedQueryWeights) GetDomainConfidence(d domain.Domain) float64 {
	lqw.mu.RLock()
	defer lqw.mu.RUnlock()

	domainW, exists := lqw.domainWeights[d]
	if !exists || domainW.updateCount == 0 {
		return 0.0
	}

	// Average confidence across all three weight distributions
	textConf := domainW.TextWeight.Confidence()
	semanticConf := domainW.SemanticWeight.Confidence()
	graphConf := domainW.GraphWeight.Confidence()

	return (textConf + semanticConf + graphConf) / 3.0
}

// GetGlobalConfidence returns the confidence in global weights.
func (lqw *LearnedQueryWeights) GetGlobalConfidence() float64 {
	lqw.mu.RLock()
	defer lqw.mu.RUnlock()

	textConf := lqw.TextWeight.Confidence()
	semanticConf := lqw.SemanticWeight.Confidence()
	graphConf := lqw.GraphWeight.Confidence()

	return (textConf + semanticConf + graphConf) / 3.0
}

// HasDomainWeights returns true if domain-specific weights exist.
func (lqw *LearnedQueryWeights) HasDomainWeights(d domain.Domain) bool {
	lqw.mu.RLock()
	defer lqw.mu.RUnlock()

	domainW, exists := lqw.domainWeights[d]
	return exists && domainW.updateCount > 0
}

// DomainUpdateCount returns the number of updates for a domain.
func (lqw *LearnedQueryWeights) DomainUpdateCount(d domain.Domain) int {
	lqw.mu.RLock()
	defer lqw.mu.RUnlock()

	domainW, exists := lqw.domainWeights[d]
	if !exists {
		return 0
	}
	return domainW.updateCount
}

// Reset resets all learned weights to initial priors.
func (lqw *LearnedQueryWeights) Reset() {
	lqw.mu.Lock()
	defer lqw.mu.Unlock()

	lqw.TextWeight = handoff.NewLearnedWeight(2.0, 2.0)
	lqw.SemanticWeight = handoff.NewLearnedWeight(2.0, 2.0)
	lqw.GraphWeight = handoff.NewLearnedWeight(2.0, 2.0)
	lqw.domainWeights = make(map[domain.Domain]*DomainQueryWeights)
	lqw.globalWeights = DefaultQueryWeights()
}

// ResetDomain resets learned weights for a specific domain.
func (lqw *LearnedQueryWeights) ResetDomain(d domain.Domain) {
	lqw.mu.Lock()
	defer lqw.mu.Unlock()

	delete(lqw.domainWeights, d)
}

// =============================================================================
// Source Ranks
// =============================================================================

// SourceRanks holds the rank of a result in each search source.
// A rank of -1 indicates the result was not found in that source.
type SourceRanks struct {
	TextRank     int // Rank in text search results (-1 if not found)
	SemanticRank int // Rank in semantic search results (-1 if not found)
	GraphRank    int // Rank in graph search results (-1 if not found)
}

// NoRank indicates a result was not found in a particular source.
const NoRank = -1

// NewSourceRanks creates SourceRanks with all ranks set to NoRank.
func NewSourceRanks() SourceRanks {
	return SourceRanks{
		TextRank:     NoRank,
		SemanticRank: NoRank,
		GraphRank:    NoRank,
	}
}

// BuildSourceRanks constructs a SourceRanks map from search results.
// This is a helper function for converting search results into the format
// needed for the Update method.
func BuildSourceRanks(
	text []TextResult,
	semantic []VectorResult,
	graph []GraphResult,
) map[string]SourceRanks {
	ranks := make(map[string]SourceRanks)

	// Process text results
	for rank, result := range text {
		sr := ranks[result.ID]
		sr.TextRank = rank
		if sr.SemanticRank == 0 && sr.GraphRank == 0 {
			sr.SemanticRank = NoRank
			sr.GraphRank = NoRank
		}
		ranks[result.ID] = sr
	}

	// Process semantic results
	for rank, result := range semantic {
		sr := ranks[result.ID]
		sr.SemanticRank = rank
		if sr.TextRank == 0 && sr.GraphRank == 0 {
			sr.TextRank = NoRank
			sr.GraphRank = NoRank
		}
		ranks[result.ID] = sr
	}

	// Process graph results
	for rank, result := range graph {
		sr := ranks[result.ID]
		sr.GraphRank = rank
		if sr.TextRank == 0 && sr.SemanticRank == 0 {
			sr.TextRank = NoRank
			sr.SemanticRank = NoRank
		}
		ranks[result.ID] = sr
	}

	// Fix unset ranks (need to distinguish 0 rank from not found)
	for id, sr := range ranks {
		// Check if this ID is actually in each result set
		foundInText := false
		for _, r := range text {
			if r.ID == id {
				foundInText = true
				break
			}
		}
		if !foundInText && sr.TextRank == 0 {
			sr.TextRank = NoRank
		}

		foundInSemantic := false
		for _, r := range semantic {
			if r.ID == id {
				foundInSemantic = true
				break
			}
		}
		if !foundInSemantic && sr.SemanticRank == 0 {
			sr.SemanticRank = NoRank
		}

		foundInGraph := false
		for _, r := range graph {
			if r.ID == id {
				foundInGraph = true
				break
			}
		}
		if !foundInGraph && sr.GraphRank == 0 {
			sr.GraphRank = NoRank
		}

		ranks[id] = sr
	}

	return ranks
}

// =============================================================================
// Weight Statistics
// =============================================================================

// WeightStats provides statistics about the current weight distributions.
type WeightStats struct {
	TextMean       float64 `json:"text_mean"`
	TextVariance   float64 `json:"text_variance"`
	TextConfidence float64 `json:"text_confidence"`

	SemanticMean       float64 `json:"semantic_mean"`
	SemanticVariance   float64 `json:"semantic_variance"`
	SemanticConfidence float64 `json:"semantic_confidence"`

	GraphMean       float64 `json:"graph_mean"`
	GraphVariance   float64 `json:"graph_variance"`
	GraphConfidence float64 `json:"graph_confidence"`
}

// GetStats returns statistics about the global weight distributions.
func (lqw *LearnedQueryWeights) GetStats() WeightStats {
	lqw.mu.RLock()
	defer lqw.mu.RUnlock()

	return WeightStats{
		TextMean:           lqw.TextWeight.Mean(),
		TextVariance:       lqw.TextWeight.Variance(),
		TextConfidence:     lqw.TextWeight.Confidence(),
		SemanticMean:       lqw.SemanticWeight.Mean(),
		SemanticVariance:   lqw.SemanticWeight.Variance(),
		SemanticConfidence: lqw.SemanticWeight.Confidence(),
		GraphMean:          lqw.GraphWeight.Mean(),
		GraphVariance:      lqw.GraphWeight.Variance(),
		GraphConfidence:    lqw.GraphWeight.Confidence(),
	}
}

// GetDomainStats returns statistics for a specific domain's weights.
// Returns zero stats if domain has no learned weights.
func (lqw *LearnedQueryWeights) GetDomainStats(d domain.Domain) WeightStats {
	lqw.mu.RLock()
	defer lqw.mu.RUnlock()

	domainW, exists := lqw.domainWeights[d]
	if !exists || domainW.updateCount == 0 {
		return WeightStats{}
	}

	return WeightStats{
		TextMean:           domainW.TextWeight.Mean(),
		TextVariance:       domainW.TextWeight.Variance(),
		TextConfidence:     domainW.TextWeight.Confidence(),
		SemanticMean:       domainW.SemanticWeight.Mean(),
		SemanticVariance:   domainW.SemanticWeight.Variance(),
		SemanticConfidence: domainW.SemanticWeight.Confidence(),
		GraphMean:          domainW.GraphWeight.Mean(),
		GraphVariance:      domainW.GraphWeight.Variance(),
		GraphConfidence:    domainW.GraphWeight.Confidence(),
	}
}
