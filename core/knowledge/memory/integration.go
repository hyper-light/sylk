// Package memory provides ACT-R based memory management for adaptive retrieval.
// MD.4.2 HybridQueryWithMemory implementation for memory-weighted result scoring.
package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/knowledge/query"
)

// =============================================================================
// MD.4.2 Memory Query Options
// =============================================================================

const (
	// DefaultMinRetrievalProb is the default minimum retrieval probability threshold.
	DefaultMinRetrievalProb = 0.5

	// DefaultMemoryWeight is the default weight for memory scores in combination.
	DefaultMemoryWeight = 0.3
)

// MemoryQueryOptions configures memory integration behavior for hybrid queries.
type MemoryQueryOptions struct {
	// ApplyWeighting enables memory-weighted scoring of results.
	ApplyWeighting bool `json:"apply_weighting"`

	// FilterByRetrieval enables filtering results by retrieval probability.
	FilterByRetrieval bool `json:"filter_by_retrieval"`

	// Explore enables Thompson Sampling exploration of memory weights.
	Explore bool `json:"explore"`

	// MinRetrievalProb is the minimum retrieval probability threshold.
	// Results below this threshold are filtered when FilterByRetrieval is true.
	MinRetrievalProb float64 `json:"min_retrieval_prob"`

	// MemoryWeight determines how much memory scores influence final ranking.
	// Range: [0, 1] where 0 = no memory influence, 1 = only memory scores.
	MemoryWeight float64 `json:"memory_weight"`
}

// DefaultMemoryQueryOptions returns the default options for memory integration.
// Defaults:
//   - ApplyWeighting: true (memory affects ranking)
//   - FilterByRetrieval: false (all results pass through)
//   - Explore: false (use mean weights)
//   - MinRetrievalProb: 0.5 (50% threshold when filtering enabled)
//   - MemoryWeight: 0.3 (30% memory influence)
func DefaultMemoryQueryOptions() *MemoryQueryOptions {
	return &MemoryQueryOptions{
		ApplyWeighting:    true,
		FilterByRetrieval: false,
		Explore:           false,
		MinRetrievalProb:  DefaultMinRetrievalProb,
		MemoryWeight:      DefaultMemoryWeight,
	}
}

// Validate checks if the options are well-formed.
func (o *MemoryQueryOptions) Validate() error {
	if o.MinRetrievalProb < 0 || o.MinRetrievalProb > 1 {
		return fmt.Errorf("min retrieval probability must be in [0, 1], got %f", o.MinRetrievalProb)
	}
	if o.MemoryWeight < 0 || o.MemoryWeight > 1 {
		return fmt.Errorf("memory weight must be in [0, 1], got %f", o.MemoryWeight)
	}
	return nil
}

// =============================================================================
// MD.4.2 HybridQueryWithMemory
// =============================================================================

// HybridQueryWithMemory wraps hybrid query execution with ACT-R memory-weighted
// scoring. It takes pre-fetched results (from HybridQueryCoordinator or similar)
// and applies memory weighting, filtering, and exploration.
//
// The memory integration follows this flow:
//  1. Receive results from base hybrid query execution
//  2. Optionally filter by retrieval probability (ACT-R model)
//  3. Apply memory weighting to adjust scores
//  4. Re-rank results based on combined scores
//  5. Record outcomes for Bayesian learning feedback
type HybridQueryWithMemory struct {
	// scorer provides ACT-R memory-weighted scoring
	scorer *MemoryWeightedScorer

	// options configures memory integration behavior
	options *MemoryQueryOptions

	// mu protects concurrent access to options
	mu sync.RWMutex
}

// NewHybridQueryWithMemory creates a new HybridQueryWithMemory with the given scorer.
// Uses default options which can be modified via builder methods.
func NewHybridQueryWithMemory(scorer *MemoryWeightedScorer) *HybridQueryWithMemory {
	return &HybridQueryWithMemory{
		scorer:  scorer,
		options: DefaultMemoryQueryOptions(),
	}
}

// NewHybridQueryWithMemoryOptions creates a new HybridQueryWithMemory with custom options.
func NewHybridQueryWithMemoryOptions(scorer *MemoryWeightedScorer, options *MemoryQueryOptions) *HybridQueryWithMemory {
	if options == nil {
		options = DefaultMemoryQueryOptions()
	}
	return &HybridQueryWithMemory{
		scorer:  scorer,
		options: options,
	}
}

// WithMemoryWeighting enables or disables memory-weighted scoring.
// Returns the receiver for method chaining.
func (h *HybridQueryWithMemory) WithMemoryWeighting(apply bool) *HybridQueryWithMemory {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.options.ApplyWeighting = apply
	return h
}

// WithRetrievalFilter enables or disables filtering by retrieval probability.
// Returns the receiver for method chaining.
func (h *HybridQueryWithMemory) WithRetrievalFilter(apply bool) *HybridQueryWithMemory {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.options.FilterByRetrieval = apply
	return h
}

// WithExploration enables or disables Thompson Sampling exploration.
// When enabled, weights are sampled from their Beta distributions.
// Returns the receiver for method chaining.
func (h *HybridQueryWithMemory) WithExploration(explore bool) *HybridQueryWithMemory {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.options.Explore = explore
	return h
}

// WithMinRetrievalProb sets the minimum retrieval probability threshold.
// Returns the receiver for method chaining.
func (h *HybridQueryWithMemory) WithMinRetrievalProb(prob float64) *HybridQueryWithMemory {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.options.MinRetrievalProb = prob
	return h
}

// WithMemoryWeight sets the memory weight for score combination.
// Returns the receiver for method chaining.
func (h *HybridQueryWithMemory) WithMemoryWeight(weight float64) *HybridQueryWithMemory {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.options.MemoryWeight = weight
	return h
}

// WithOptions sets all options at once.
// Returns the receiver for method chaining.
func (h *HybridQueryWithMemory) WithOptions(options *MemoryQueryOptions) *HybridQueryWithMemory {
	if options == nil {
		return h
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.options = options
	return h
}

// Options returns a copy of the current options.
func (h *HybridQueryWithMemory) Options() *MemoryQueryOptions {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return &MemoryQueryOptions{
		ApplyWeighting:    h.options.ApplyWeighting,
		FilterByRetrieval: h.options.FilterByRetrieval,
		Explore:           h.options.Explore,
		MinRetrievalProb:  h.options.MinRetrievalProb,
		MemoryWeight:      h.options.MemoryWeight,
	}
}

// Scorer returns the underlying MemoryWeightedScorer.
func (h *HybridQueryWithMemory) Scorer() *MemoryWeightedScorer {
	return h.scorer
}

// =============================================================================
// Execute Method
// =============================================================================

// Execute applies memory integration to pre-fetched hybrid query results.
// The hybridQuery parameter provides context but results are passed separately
// to allow integration with any query execution strategy.
//
// Processing steps:
//  1. Validate inputs
//  2. Filter by retrieval probability (if enabled)
//  3. Apply memory weighting to adjust scores (if enabled)
//  4. Populate MemoryActivation and MemoryFactor fields
//  5. Re-rank by combined score
//
// Returns re-ranked/filtered results with memory fields populated.
func (h *HybridQueryWithMemory) Execute(
	ctx context.Context,
	hybridQuery *query.HybridQuery,
	results []query.HybridResult,
) ([]query.HybridResult, error) {
	if h.scorer == nil {
		return nil, fmt.Errorf("memory scorer is required")
	}

	if len(results) == 0 {
		return results, nil
	}

	// Get current options (thread-safe copy)
	h.mu.RLock()
	opts := &MemoryQueryOptions{
		ApplyWeighting:    h.options.ApplyWeighting,
		FilterByRetrieval: h.options.FilterByRetrieval,
		Explore:           h.options.Explore,
		MinRetrievalProb:  h.options.MinRetrievalProb,
		MemoryWeight:      h.options.MemoryWeight,
	}
	h.mu.RUnlock()

	// Validate options
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}

	// Make a copy of results to avoid modifying the input
	processed := make([]query.HybridResult, len(results))
	copy(processed, results)

	// Step 1: Filter by retrieval probability if enabled
	if opts.FilterByRetrieval {
		var err error
		processed, err = h.filterByRetrieval(ctx, processed, opts)
		if err != nil {
			return nil, fmt.Errorf("filter by retrieval: %w", err)
		}
	}

	// Step 2: Apply memory weighting if enabled
	if opts.ApplyWeighting && len(processed) > 0 {
		var err error
		processed, err = h.applyMemoryWeighting(ctx, processed, opts)
		if err != nil {
			return nil, fmt.Errorf("apply memory weighting: %w", err)
		}
	}

	return processed, nil
}

// filterByRetrieval filters results based on ACT-R retrieval probability.
// Results with no memory data pass through (fail open for new items).
func (h *HybridQueryWithMemory) filterByRetrieval(
	ctx context.Context,
	results []query.HybridResult,
	opts *MemoryQueryOptions,
) ([]query.HybridResult, error) {
	// Use the scorer's built-in filtering
	filtered, err := h.scorer.FilterByRetrievalProbability(ctx, results, opts.Explore)
	if err != nil {
		return nil, err
	}

	return filtered, nil
}

// applyMemoryWeighting computes memory scores and combines them with base scores.
// Populates MemoryActivation and MemoryFactor fields on each result.
func (h *HybridQueryWithMemory) applyMemoryWeighting(
	ctx context.Context,
	results []query.HybridResult,
	opts *MemoryQueryOptions,
) ([]query.HybridResult, error) {
	now := time.Now().UTC()

	for i := range results {
		// Compute memory score for this result
		memoryScore, err := h.scorer.ComputeMemoryScore(ctx, results[i].ID, now, opts.Explore)
		if err != nil {
			// Log error but continue with zero memory score
			memoryScore = 0.0
		}

		// Store the raw activation
		results[i].MemoryActivation = memoryScore

		// Compute memory factor (contribution to final score)
		// memoryFactor = memoryScore * memoryWeight
		memoryFactor := memoryScore * opts.MemoryWeight
		results[i].MemoryFactor = memoryFactor

		// Combine scores: originalScore * (1 - memoryWeight) + memoryScore * memoryWeight
		// This ensures the final score is bounded appropriately
		originalWeight := 1.0 - opts.MemoryWeight
		results[i].Score = results[i].Score*originalWeight + memoryFactor
	}

	// Sort by combined score (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

// =============================================================================
// RecordOutcome Method
// =============================================================================

// OutcomeRecord captures information about a retrieval outcome for learning.
type OutcomeRecord struct {
	// NodeID is the identifier of the retrieved node.
	NodeID string

	// WasUseful indicates whether the retrieval was helpful.
	WasUseful bool

	// AgeAtRetrieval is how long ago the node was last accessed.
	// If not known, use zero duration.
	AgeAtRetrieval time.Duration

	// QueryContext context (optional, for debugging/analysis)
	QueryContext string
}

// RecordOutcome records feedback from retrieval events to update learned weights.
// Call this after determining whether retrieved results were useful to the user.
//
// This method:
//  1. Records access traces in the memory store
//  2. Updates the scorer's learned weights via Bayesian learning
//  3. Updates domain decay parameters based on feedback
//
// Parameters:
//   - ctx: Context for cancellation
//   - usedNodeIDs: IDs of nodes that were actually used/viewed
//   - wasUseful: Whether the overall retrieval was helpful
func (h *HybridQueryWithMemory) RecordOutcome(
	ctx context.Context,
	usedNodeIDs []string,
	wasUseful bool,
) error {
	if h.scorer == nil {
		return fmt.Errorf("memory scorer is required")
	}

	if len(usedNodeIDs) == 0 {
		return nil
	}

	// Record outcome for each used node
	var firstErr error
	for _, nodeID := range usedNodeIDs {
		// Use a default age since we don't have precise timing
		// The scorer will handle the actual age calculation
		err := h.scorer.RecordRetrievalOutcome(ctx, nodeID, wasUseful, time.Hour)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// RecordOutcomes records feedback for multiple retrieval outcomes with detailed info.
// This is the detailed version that allows per-node usefulness tracking.
func (h *HybridQueryWithMemory) RecordOutcomes(
	ctx context.Context,
	outcomes []OutcomeRecord,
) error {
	if h.scorer == nil {
		return fmt.Errorf("memory scorer is required")
	}

	if len(outcomes) == 0 {
		return nil
	}

	var firstErr error
	for _, outcome := range outcomes {
		age := outcome.AgeAtRetrieval
		if age == 0 {
			age = time.Hour // Default age if not specified
		}

		err := h.scorer.RecordRetrievalOutcome(ctx, outcome.NodeID, outcome.WasUseful, age)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// =============================================================================
// Utility Methods
// =============================================================================

// MemoryStats returns current statistics about the memory integration.
type MemoryStats struct {
	// Weight values (means of learned distributions)
	ActivationWeight   float64 `json:"activation_weight"`
	RecencyBonus       float64 `json:"recency_bonus"`
	FrequencyBonus     float64 `json:"frequency_bonus"`
	RetrievalThreshold float64 `json:"retrieval_threshold"`

	// Weight confidences (from Bayesian learning)
	ActivationConfidence  float64 `json:"activation_confidence"`
	RecencyConfidence     float64 `json:"recency_confidence"`
	FrequencyConfidence   float64 `json:"frequency_confidence"`
	ThresholdConfidence   float64 `json:"threshold_confidence"`
}

// GetMemoryStats returns statistics about the current memory weights and confidences.
func (h *HybridQueryWithMemory) GetMemoryStats() MemoryStats {
	if h.scorer == nil {
		return MemoryStats{}
	}

	act, rec, freq, thresh := h.scorer.GetWeights()
	actC, recC, freqC, threshC := h.scorer.GetWeightConfidences()

	return MemoryStats{
		ActivationWeight:     act,
		RecencyBonus:         rec,
		FrequencyBonus:       freq,
		RetrievalThreshold:   thresh,
		ActivationConfidence: actC,
		RecencyConfidence:    recC,
		FrequencyConfidence:  freqC,
		ThresholdConfidence:  threshC,
	}
}

// Reset resets options to defaults.
func (h *HybridQueryWithMemory) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.options = DefaultMemoryQueryOptions()
}
