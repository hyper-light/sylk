package coldstart

import "math"

// =============================================================================
// Cold Prior Calculator (MD.9.4)
// =============================================================================

// ColdPriorCalculator computes cold-start priors for nodes based on their
// signals, the codebase profile, and configured weights.
type ColdPriorCalculator struct {
	profile          *CodebaseProfile
	weights          *ColdPriorWeights
	entityBasePriors map[string]float64
}

// NewColdPriorCalculator creates a new calculator with the given profile and weights.
func NewColdPriorCalculator(
	profile *CodebaseProfile,
	weights *ColdPriorWeights,
) *ColdPriorCalculator {
	return &ColdPriorCalculator{
		profile:          profile,
		weights:          weights,
		entityBasePriors: defaultEntityBasePriors(),
	}
}

// defaultEntityBasePriors returns hardcoded baseline priors per entity type.
// These represent domain knowledge about entity importance before any signals.
func defaultEntityBasePriors() map[string]float64 {
	return map[string]float64{
		"function":  0.50,
		"type":      0.45,
		"method":    0.50,
		"struct":    0.45,
		"interface": 0.40,
		"variable":  0.30,
		"constant":  0.25,
		"import":    0.20,
		"package":   0.35,
		"file":      0.15,
	}
}

// ComputeColdPrior computes the cold-start prior for a node given its signals.
// Returns a value in [0,1] representing the node's importance.
func (c *ColdPriorCalculator) ComputeColdPrior(
	signals *NodeColdStartSignals,
) float64 {
	structural := c.computeStructuralScore(signals)
	content := c.computeContentScore(signals)
	distributional := c.computeDistributionalScore(signals)

	// Weighted combination
	prior := (c.weights.StructuralWeight * structural) +
		(c.weights.ContentWeight * content) +
		(c.weights.DistributionalWeight * distributional)

	// Clamp to [0,1]
	return clamp(prior, 0.0, 1.0)
}

// computeStructuralScore computes the structural component score.
func (c *ColdPriorCalculator) computeStructuralScore(
	signals *NodeColdStartSignals,
) float64 {
	// Normalize PageRank
	pageRankScore := 0.0
	if c.profile.MaxPageRank > 0 {
		pageRankScore = signals.PageRank / c.profile.MaxPageRank
	}

	// Normalize degree centrality
	degreeScore := 0.0
	maxDegree := math.Max(c.profile.AvgInDegree, c.profile.AvgOutDegree) * 2
	if maxDegree > 0 {
		totalDegree := float64(signals.InDegree + signals.OutDegree)
		degreeScore = totalDegree / maxDegree
	}

	// Cluster coefficient is already in [0,1]
	clusterScore := signals.ClusterCoeff

	// Weighted combination
	return (c.weights.PageRankWeight * pageRankScore) +
		(c.weights.DegreeWeight * degreeScore) +
		(c.weights.ClusterWeight * clusterScore)
}

// computeContentScore computes the content component score.
func (c *ColdPriorCalculator) computeContentScore(
	signals *NodeColdStartSignals,
) float64 {
	// Entity type base prior
	basePrior := c.entityBasePriors[signals.EntityType]
	if basePrior == 0 {
		basePrior = 0.30 // Default for unknown types
	}

	// Name salience and doc coverage are already in [0,1]
	nameScore := signals.NameSalience
	docScore := signals.DocCoverage

	// Weighted combination
	return (c.weights.EntityTypeWeight * basePrior) +
		(c.weights.NameWeight * nameScore) +
		(c.weights.DocWeight * docScore)
}

// computeDistributionalScore computes the distributional component score.
func (c *ColdPriorCalculator) computeDistributionalScore(
	signals *NodeColdStartSignals,
) float64 {
	// Type frequency and rarity are already normalized in [0,1]
	freqScore := signals.TypeFrequency
	rarityScore := signals.TypeRarity

	// Weighted combination
	return (c.weights.FrequencyWeight * freqScore) +
		(c.weights.RarityWeight * rarityScore)
}

// clamp restricts a value to the range [min, max].
func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
