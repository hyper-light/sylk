// Package vectorgraphdb provides AR.11.2: HybridResult Extensions - extended
// result types with retrieval cost, tier source, and access tracking.
package vectorgraphdb

import (
	"time"
)

// =============================================================================
// Tier Source Constants
// =============================================================================

// TierSource indicates which tier produced a result.
type TierSource string

const (
	TierSourceHot  TierSource = "hot"
	TierSourceWarm TierSource = "warm"
	TierSourceFull TierSource = "full"
	TierSourceNone TierSource = ""
)

// ParseTierSource parses a string to TierSource.
func ParseTierSource(s string) TierSource {
	switch s {
	case "hot":
		return TierSourceHot
	case "warm":
		return TierSourceWarm
	case "full":
		return TierSourceFull
	default:
		return TierSourceNone
	}
}

// =============================================================================
// Extended HybridResult
// =============================================================================

// ExtendedHybridResult extends HybridResult with retrieval metadata.
type ExtendedHybridResult struct {
	// Embedded base result
	HybridResult

	// RetrievalCost is the estimated cost of retrieving this result.
	// Cost is measured in relative units based on tier and complexity.
	RetrievalCost float64

	// BleveScore is the full-text search score from Bleve (0 if not from Bleve).
	BleveScore float64

	// TierSource indicates which tier produced this result.
	TierSource TierSource

	// AccessedAt is when this result was last accessed.
	AccessedAt time.Time

	// FusionRank is the rank from RRF (Reciprocal Rank Fusion).
	FusionRank int

	// SourceCount is the number of sources that contributed to this result.
	SourceCount int
}

// NewExtendedHybridResult creates an ExtendedHybridResult from a base result.
func NewExtendedHybridResult(base HybridResult) *ExtendedHybridResult {
	return &ExtendedHybridResult{
		HybridResult:  base,
		RetrievalCost: estimateCost(base),
		TierSource:    TierSourceNone,
		AccessedAt:    time.Now(),
	}
}

// estimateCost estimates retrieval cost based on result properties.
func estimateCost(result HybridResult) float64 {
	baseCost := 1.0

	// Higher scores = more relevant = more likely to be accessed
	relevanceFactor := 1.0 - (result.CombinedScore * 0.5)

	// More connections = more context needed
	connectionFactor := 1.0 + (float64(result.ConnectionCount) * 0.1)

	return baseCost * relevanceFactor * connectionFactor
}

// WithBleveScore sets the Bleve score and returns the result.
func (r *ExtendedHybridResult) WithBleveScore(score float64) *ExtendedHybridResult {
	r.BleveScore = score
	return r
}

// WithTierSource sets the tier source and returns the result.
func (r *ExtendedHybridResult) WithTierSource(source TierSource) *ExtendedHybridResult {
	r.TierSource = source
	return r
}

// WithRetrievalCost sets the retrieval cost and returns the result.
func (r *ExtendedHybridResult) WithRetrievalCost(cost float64) *ExtendedHybridResult {
	r.RetrievalCost = cost
	return r
}

// WithFusionRank sets the fusion rank and returns the result.
func (r *ExtendedHybridResult) WithFusionRank(rank int) *ExtendedHybridResult {
	r.FusionRank = rank
	return r
}

// MarkAccessed updates the access timestamp.
func (r *ExtendedHybridResult) MarkAccessed() {
	r.AccessedAt = time.Now()
}

// =============================================================================
// Result Conversion
// =============================================================================

// ExtendResults converts base HybridResults to ExtendedHybridResults.
func ExtendResults(results []HybridResult) []*ExtendedHybridResult {
	extended := make([]*ExtendedHybridResult, len(results))
	for i, r := range results {
		extended[i] = NewExtendedHybridResult(r)
	}
	return extended
}

// ToHybridResults converts ExtendedHybridResults back to base results.
func ToHybridResults(results []*ExtendedHybridResult) []HybridResult {
	base := make([]HybridResult, len(results))
	for i, r := range results {
		base[i] = r.HybridResult
	}
	return base
}

// =============================================================================
// Tier Cost Constants
// =============================================================================

const (
	// TierCostHot is the relative cost of hot tier retrieval.
	TierCostHot = 0.1

	// TierCostWarm is the relative cost of warm tier retrieval.
	TierCostWarm = 0.5

	// TierCostFull is the relative cost of full tier retrieval.
	TierCostFull = 1.0
)

// TierCost returns the cost factor for a tier source.
func TierCost(source TierSource) float64 {
	switch source {
	case TierSourceHot:
		return TierCostHot
	case TierSourceWarm:
		return TierCostWarm
	case TierSourceFull:
		return TierCostFull
	default:
		return TierCostFull
	}
}

// =============================================================================
// Result Metrics
// =============================================================================

// ResultMetrics aggregates metrics from a set of results.
type ResultMetrics struct {
	TotalResults     int
	TotalCost        float64
	AverageScore     float64
	HotCount         int
	WarmCount        int
	FullCount        int
	BleveResultCount int
}

// ComputeMetrics computes aggregated metrics for results.
func ComputeMetrics(results []*ExtendedHybridResult) ResultMetrics {
	metrics := ResultMetrics{TotalResults: len(results)}

	if len(results) == 0 {
		return metrics
	}

	var totalScore float64
	for _, r := range results {
		metrics.TotalCost += r.RetrievalCost
		totalScore += r.CombinedScore
		countByTier(&metrics, r.TierSource)
		if r.BleveScore > 0 {
			metrics.BleveResultCount++
		}
	}

	metrics.AverageScore = totalScore / float64(len(results))
	return metrics
}

func countByTier(metrics *ResultMetrics, source TierSource) {
	switch source {
	case TierSourceHot:
		metrics.HotCount++
	case TierSourceWarm:
		metrics.WarmCount++
	case TierSourceFull:
		metrics.FullCount++
	}
}
