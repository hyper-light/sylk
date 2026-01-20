// Package memory provides ACT-R based memory management for adaptive retrieval.
// MD.4.1 MemoryWeightedScorer implementation for memory-weighted result scoring.
package memory

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/handoff"
	"github.com/adalundhe/sylk/core/knowledge/query"
)

// =============================================================================
// MD.4.1 MemoryWeightedScorer
// =============================================================================

const (
	// DefaultMinActivation is the floor for activation values.
	DefaultMinActivation = -10.0

	// DefaultRetrievalThreshold is the activation threshold for retrieval.
	DefaultRetrievalThreshold = -2.0

	// DefaultActivationNoise is the ACT-R noise parameter for softmax.
	DefaultActivationNoise = 0.25
)

// MemoryWeightedScorer combines ACT-R memory activations with hybrid search
// scores using learned weights. Implements Bayesian learning for weight
// adaptation based on retrieval outcomes.
type MemoryWeightedScorer struct {
	store *MemoryStore

	// Learned weights for score combination
	ActivationWeight   *handoff.LearnedWeight
	RecencyBonus       *handoff.LearnedWeight
	FrequencyBonus     *handoff.LearnedWeight
	RetrievalThreshold *handoff.LearnedWeight

	// Activation floor to prevent extreme negative values
	MinActivation float64

	// Update configuration for Bayesian learning
	updateConfig *handoff.UpdateConfig

	// PF.4.5: Activation cache for avoiding redundant ACT-R calculations
	activationCache *ActivationCache

	mu sync.RWMutex
}

// NewMemoryWeightedScorer creates a new MemoryWeightedScorer with default priors.
// Default priors are chosen based on cognitive modeling principles:
//   - ActivationWeight: Beta(5, 5) -> mean 0.5 (balanced importance)
//   - RecencyBonus: Beta(3, 7) -> mean 0.3 (less important)
//   - FrequencyBonus: Beta(2, 8) -> mean 0.2 (least important)
//   - RetrievalThreshold: Beta(2, 8) -> mean 0.2 (low threshold)
func NewMemoryWeightedScorer(store *MemoryStore) *MemoryWeightedScorer {
	return NewMemoryWeightedScorerWithCache(store, nil, nil)
}

// NewMemoryWeightedScorerWithCache creates a new MemoryWeightedScorer with optional
// cache configuration. If cacheConfig is nil, uses default cache configuration.
// If updateConfig is nil, uses default Bayesian learning configuration.
func NewMemoryWeightedScorerWithCache(store *MemoryStore, cacheConfig *ActivationCacheConfig, updateConfig *handoff.UpdateConfig) *MemoryWeightedScorer {
	var cache *ActivationCache
	if cacheConfig != nil {
		cache = NewActivationCache(*cacheConfig)
	} else {
		cache = NewDefaultActivationCache()
	}

	cfg := updateConfig
	if cfg == nil {
		cfg = handoff.DefaultUpdateConfig()
	}

	return &MemoryWeightedScorer{
		store:              store,
		ActivationWeight:   handoff.NewLearnedWeight(5.0, 5.0),
		RecencyBonus:       handoff.NewLearnedWeight(3.0, 7.0),
		FrequencyBonus:     handoff.NewLearnedWeight(2.0, 8.0),
		RetrievalThreshold: handoff.NewLearnedWeight(2.0, 8.0),
		MinActivation:      DefaultMinActivation,
		updateConfig:       cfg,
		activationCache:    cache,
	}
}

// NewMemoryWeightedScorerWithConfig creates a scorer with custom update configuration.
func NewMemoryWeightedScorerWithConfig(store *MemoryStore, config *handoff.UpdateConfig) *MemoryWeightedScorer {
	return NewMemoryWeightedScorerWithCache(store, nil, config)
}

// ApplyMemoryWeighting computes memory-weighted scores for hybrid results.
// Combines existing search scores with ACT-R memory activation using learned weights.
// When explore is true, uses Thompson Sampling for weight exploration.
//
// The memory score formula is:
//
//	memoryScore = activation * activationWeight
//	            + recencyBonus * (1 / (1 + hoursSinceAccess))
//	            + frequencyBonus * log(1 + accessCount)
//
// Returns re-ranked results sorted by combined score.
func (s *MemoryWeightedScorer) ApplyMemoryWeighting(
	ctx context.Context,
	results []query.HybridResult,
	explore bool,
) ([]query.HybridResult, error) {
	if len(results) == 0 {
		return results, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	weighted := make([]query.HybridResult, len(results))
	copy(weighted, results)

	for i := range weighted {
		memoryScore, err := s.ComputeMemoryScore(ctx, weighted[i].ID, now, explore)
		if err != nil {
			// Log error but continue with zero memory score
			memoryScore = 0.0
		}

		// Combine original score with memory score
		// Original score is normalized, memory score is unbounded
		// Use weighted combination with original score having base weight of 1.0
		combinedScore := weighted[i].Score + memoryScore
		weighted[i].Score = combinedScore
	}

	// Sort by combined score (descending)
	sortByScore(weighted)

	return weighted, nil
}

// FilterByRetrievalProbability filters results based on ACT-R retrieval probability.
// Uses softmax activation with the learned retrieval threshold.
// When explore is true, samples threshold from the learned distribution.
//
// Retrieval probability formula (ACT-R):
//
//	P(retrieval) = 1 / (1 + exp(-(A - τ) / s))
//
// where A is activation, τ is threshold, and s is noise (default 0.25).
//
// Results with no memory data (no access traces) are passed through (fail open).
func (s *MemoryWeightedScorer) FilterByRetrievalProbability(
	ctx context.Context,
	results []query.HybridResult,
	explore bool,
) ([]query.HybridResult, error) {
	if len(results) == 0 {
		return results, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get threshold (either mean or sampled)
	var threshold float64
	if explore {
		threshold = s.RetrievalThreshold.Sample()
	} else {
		threshold = s.RetrievalThreshold.Mean()
	}

	// Convert threshold from [0,1] to activation scale
	// Map 0.2 -> -2.0 (typical low threshold)
	activationThreshold := (threshold - 0.5) * 10.0

	now := time.Now().UTC()
	filtered := make([]query.HybridResult, 0, len(results))

	for _, result := range results {
		// Load memory for this result
		memory, err := s.store.GetMemory(ctx, result.ID, domain.DomainLibrarian)
		if err != nil {
			// If we can't get memory, include the result (fail open)
			filtered = append(filtered, result)
			continue
		}

		// If no memory traces, include the result (fail open for new items)
		if memory == nil || len(memory.Traces) == 0 {
			filtered = append(filtered, result)
			continue
		}

		// Compute retrieval probability
		prob := memory.RetrievalProbability(now, activationThreshold)

		// Include if probability exceeds 0.5 (more likely to retrieve than not)
		if prob >= 0.5 {
			filtered = append(filtered, result)
		}
	}

	return filtered, nil
}

// RecordRetrievalOutcome records feedback from a retrieval event to update
// learned weights. Call this after determining whether a retrieved result
// was useful to the user.
//
// Parameters:
//   - nodeID: The ID of the retrieved node
//   - wasUseful: Whether the retrieval was helpful to the user
//   - ageAtRetrieval: How long ago the node was last accessed
func (s *MemoryWeightedScorer) RecordRetrievalOutcome(
	ctx context.Context,
	nodeID string,
	wasUseful bool,
	ageAtRetrieval time.Duration,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Update memory store with access trace
	accessType := AccessRetrieval
	if wasUseful {
		accessType = AccessReinforcement
	}

	if err := s.store.RecordAccess(ctx, nodeID, accessType, "retrieval_outcome"); err != nil {
		return fmt.Errorf("record access: %w", err)
	}

	// Update learned weights based on outcome
	// If useful, increase weight for factors that contributed to this result
	observation := 0.0
	if wasUseful {
		observation = 1.0
	}

	// Update all weights with the outcome
	s.ActivationWeight.Update(observation, s.updateConfig)

	// Recency bonus update: if the result was useful and recent, increase recency weight
	hoursSinceAccess := ageAtRetrieval.Hours()
	recencyFactor := 1.0 / (1.0 + hoursSinceAccess)
	if wasUseful {
		// Scale observation by recency factor
		s.RecencyBonus.Update(recencyFactor, s.updateConfig)
	} else {
		s.RecencyBonus.Update(1.0-recencyFactor, s.updateConfig)
	}

	// Update retrieval threshold based on outcome
	// Useful results at lower thresholds -> lower threshold is good
	// Useless results at lower thresholds -> should increase threshold
	s.RetrievalThreshold.Update(observation, s.updateConfig)

	// Update domain decay parameters in memory store
	domainVal, err := s.store.getNodeDomain(ctx, nodeID)
	if err == nil {
		if err := s.store.UpdateDecayParams(ctx, domainVal, wasUseful, ageAtRetrieval); err != nil {
			return fmt.Errorf("update decay params: %w", err)
		}
	}

	return nil
}

// ComputeMemoryScore computes the memory-weighted score for a single node.
// Combines activation, recency bonus, and frequency bonus using learned weights.
//
// PF.4.5: Uses ActivationCache to avoid redundant activation calculations.
// Activations are cached by (nodeID, timeBucket) for efficiency.
//
// Formula:
//
//	memoryScore = activation * activationWeight
//	            + recencyBonus * (1 / (1 + hoursSinceAccess))
//	            + frequencyBonus * log(1 + accessCount)
//
// When explore is true, samples weights from their Beta distributions
// for Thompson Sampling exploration.
func (s *MemoryWeightedScorer) ComputeMemoryScore(
	ctx context.Context,
	nodeID string,
	now time.Time,
	explore bool,
) (float64, error) {
	// Load memory for this node
	memory, err := s.store.GetMemory(ctx, nodeID, domain.DomainLibrarian)
	if err != nil {
		return 0, fmt.Errorf("get memory for node %s: %w", nodeID, err)
	}

	if memory == nil || len(memory.Traces) == 0 {
		// No memory data, return zero score
		return 0, nil
	}

	// Get weights (either mean or sampled for exploration)
	var activationWeight, recencyWeight, frequencyWeight float64
	if explore {
		activationWeight = s.ActivationWeight.Sample()
		recencyWeight = s.RecencyBonus.Sample()
		frequencyWeight = s.FrequencyBonus.Sample()
	} else {
		activationWeight = s.ActivationWeight.Mean()
		recencyWeight = s.RecencyBonus.Mean()
		frequencyWeight = s.FrequencyBonus.Mean()
	}

	// PF.4.5: Use cache to avoid redundant activation calculations
	// The cache is keyed by (nodeID, timeBucket) so requests within the same
	// time window share cached values while ensuring activations are recalculated
	// as they naturally decay over time.
	var activation float64
	if s.activationCache != nil {
		activation = s.activationCache.GetOrCompute(nodeID, now, func() float64 {
			return s.computeActivationClamped(memory, now)
		})
	} else {
		activation = s.computeActivationClamped(memory, now)
	}

	// Normalize activation to [0,1] range for combination
	// Using sigmoid: 1 / (1 + exp(-activation))
	normalizedActivation := 1.0 / (1.0 + math.Exp(-activation))

	// Compute recency component
	var hoursSinceAccess float64
	if len(memory.Traces) > 0 {
		lastAccess := memory.Traces[len(memory.Traces)-1].AccessedAt
		hoursSinceAccess = now.Sub(lastAccess).Hours()
	}
	recencyComponent := 1.0 / (1.0 + hoursSinceAccess)

	// Compute frequency component
	frequencyComponent := math.Log(1.0 + float64(memory.AccessCount))
	// Normalize frequency to roughly [0,1] range (assuming max ~100 accesses)
	frequencyComponent = frequencyComponent / math.Log(101.0)

	// Combine components with learned weights
	memoryScore := normalizedActivation*activationWeight +
		recencyComponent*recencyWeight +
		frequencyComponent*frequencyWeight

	return memoryScore, nil
}

// computeActivationClamped computes the activation for a memory and clamps it
// to the minimum activation floor. This is a helper for cache computation.
func (s *MemoryWeightedScorer) computeActivationClamped(memory *ACTRMemory, now time.Time) float64 {
	activation := memory.Activation(now)
	if activation < s.MinActivation {
		activation = s.MinActivation
	}
	return activation
}

// GetWeights returns the current learned weight values (means).
func (s *MemoryWeightedScorer) GetWeights() (activation, recency, frequency, threshold float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.ActivationWeight.Mean(),
		s.RecencyBonus.Mean(),
		s.FrequencyBonus.Mean(),
		s.RetrievalThreshold.Mean()
}

// GetWeightConfidences returns the confidence levels for each learned weight.
func (s *MemoryWeightedScorer) GetWeightConfidences() (activation, recency, frequency, threshold float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.ActivationWeight.Confidence(),
		s.RecencyBonus.Confidence(),
		s.FrequencyBonus.Confidence(),
		s.RetrievalThreshold.Confidence()
}

// SetUpdateConfig updates the Bayesian learning configuration.
func (s *MemoryWeightedScorer) SetUpdateConfig(config *handoff.UpdateConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if config != nil {
		s.updateConfig = config
	}
}

// SetMinActivation updates the minimum activation floor.
func (s *MemoryWeightedScorer) SetMinActivation(min float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MinActivation = min
}

// Store returns the underlying MemoryStore.
func (s *MemoryWeightedScorer) Store() *MemoryStore {
	return s.store
}

// ActivationCache returns the activation cache for monitoring or testing.
// Returns nil if no cache is configured.
func (s *MemoryWeightedScorer) ActivationCache() *ActivationCache {
	return s.activationCache
}

// InvalidateActivationCache invalidates all cached activations for a specific node.
// Call this when a node's memory traces have been updated.
func (s *MemoryWeightedScorer) InvalidateActivationCache(nodeID string) {
	if s.activationCache != nil {
		s.activationCache.InvalidateNode(nodeID)
	}
}

// ClearActivationCache clears all cached activations.
func (s *MemoryWeightedScorer) ClearActivationCache() {
	if s.activationCache != nil {
		s.activationCache.Clear()
	}
}

// Stop stops the scorer and releases resources including the activation cache.
// Should be called when the scorer is no longer needed.
func (s *MemoryWeightedScorer) Stop() {
	if s.activationCache != nil {
		s.activationCache.Stop()
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// sortByScore sorts results by score in descending order using insertion sort
// (stable and efficient for small slices).
func sortByScore(results []query.HybridResult) {
	for i := 1; i < len(results); i++ {
		j := i
		for j > 0 && results[j].Score > results[j-1].Score {
			results[j], results[j-1] = results[j-1], results[j]
			j--
		}
	}
}
