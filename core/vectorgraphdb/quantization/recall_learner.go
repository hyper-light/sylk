// Package quantization provides vector quantization for efficient similarity search.
// This file implements RecallLearner - learns optimal recall targets from agent usage signals.
package quantization

import (
	"math"
	"math/rand"
	"sync"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultRecallTarget is the initial recall target before learning.
const DefaultRecallTarget = 0.9

// MinObservationsForConfidence is the minimum observations needed for high confidence.
const MinObservationsForConfidence = 50

// RecallTargetQuantile is the quantile at which to derive recall target.
// Set to 0.95 meaning: "what recall captures 95% of used results?"
const RecallTargetQuantile = 0.95

// MaxTrackedPositions is the maximum number of positions to track in the distribution.
const MaxTrackedPositions = 100

// =============================================================================
// RecallLearner
// =============================================================================

// RecallLearner learns optimal recall targets from agent usage patterns.
// It tracks which positions in search results are actually used by agents
// and derives a recall target that captures the specified quantile of usage.
//
// The learning process works as follows:
//  1. Observe which prefetched IDs were actually used
//  2. Track the "deepest position" at which used results appeared
//  3. Use Beta distributions to model the probability that position N captures all usage
//  4. Derive recall target as the position/total_prefetched that achieves target quantile
//
// Thread-safe for concurrent usage signal recording.
type RecallLearner struct {
	mu sync.Mutex

	// positionUsage tracks Beta(alpha, beta) for each position bucket.
	// positionUsage[i] represents: "did the agent use results at or after position i?"
	// Alpha = times used at or after position i, Beta = times NOT used at or after i.
	positionUsage []BetaPosterior

	// totalObservations counts the number of usage episodes observed.
	totalObservations int

	// searchedAgainCount tracks episodes where agent searched again after prefetch.
	// This indicates the prefetch was insufficient.
	searchedAgainCount int

	// codebaseID identifies which codebase this learner is for.
	codebaseID string

	// rng provides random sampling for Thompson Sampling.
	rng *rand.Rand
}

// NewRecallLearner creates a new RecallLearner for a specific codebase.
func NewRecallLearner(codebaseID string) *RecallLearner {
	return &RecallLearner{
		codebaseID:    codebaseID,
		positionUsage: initPositionPriors(MaxTrackedPositions),
		rng:           rand.New(rand.NewSource(rand.Int63())),
	}
}

// initPositionPriors initializes uniform Beta(1,1) priors for each position.
func initPositionPriors(numPositions int) []BetaPosterior {
	priors := make([]BetaPosterior, numPositions)
	for i := range priors {
		priors[i] = NewUniformPrior()
	}
	return priors
}

// =============================================================================
// Observation Methods
// =============================================================================

// ObserveUsage records an observation of which prefetched results were used.
// This is the main entry point for learning from agent behavior.
//
// Parameters:
//   - prefetchedIDs: IDs that were returned by search, in ranked order
//   - usedIDs: IDs that the agent actually referenced/used
//   - searchedAgain: true if the agent performed additional searches after prefetch
//
// The method computes the "deepest used position" - the highest index in prefetchedIDs
// at which a usedID appears. This indicates how much of the ranked list was needed.
func (rl *RecallLearner) ObserveUsage(prefetchedIDs, usedIDs []string, searchedAgain bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.totalObservations++
	if searchedAgain {
		rl.searchedAgainCount++
	}

	if len(prefetchedIDs) == 0 || len(usedIDs) == 0 {
		rl.updateAllPositionsNotUsed()
		return
	}

	usedSet := make(map[string]struct{}, len(usedIDs))
	for _, id := range usedIDs {
		usedSet[id] = struct{}{}
	}

	deepestUsed := rl.findDeepestUsedPosition(prefetchedIDs, usedSet)
	rl.updatePositionDistributions(deepestUsed, len(prefetchedIDs))
}

// findDeepestUsedPosition finds the highest index in prefetchedIDs where a used ID appears.
// Returns -1 if none of the used IDs appear in prefetchedIDs.
func (rl *RecallLearner) findDeepestUsedPosition(prefetchedIDs []string, usedSet map[string]struct{}) int {
	deepest := -1
	for i, id := range prefetchedIDs {
		if _, used := usedSet[id]; used {
			if i > deepest {
				deepest = i
			}
		}
	}
	return deepest
}

// updatePositionDistributions updates Beta posteriors based on deepest used position.
// Positions at or before deepestUsed get "success" (alpha++).
// Positions after deepestUsed get "failure" (beta++), meaning they weren't needed.
func (rl *RecallLearner) updatePositionDistributions(deepestUsed, totalPrefetched int) {
	maxBucket := min(totalPrefetched, MaxTrackedPositions)

	if deepestUsed < 0 {
		rl.updateAllPositionsNotUsed()
		return
	}

	bucket := deepestUsed
	if totalPrefetched > MaxTrackedPositions {
		bucket = (deepestUsed * MaxTrackedPositions) / totalPrefetched
	}

	for i := 0; i < maxBucket; i++ {
		if i <= bucket {
			rl.positionUsage[i].Alpha++
		} else {
			rl.positionUsage[i].Beta++
		}
	}
}

// updateAllPositionsNotUsed marks all positions as not needed.
func (rl *RecallLearner) updateAllPositionsNotUsed() {
	for i := range rl.positionUsage {
		rl.positionUsage[i].Beta++
	}
}

// =============================================================================
// Recall Target Computation
// =============================================================================

// GetLearnedRecallTarget returns the learned optimal recall target.
// Returns DefaultRecallTarget (0.9) if insufficient observations.
//
// The target is computed as follows:
//  1. For each position i, estimate P(used at or after i) via Beta mean
//  2. Find the smallest position where P(used at or after i) < (1 - RecallTargetQuantile)
//  3. Convert to recall = position / MaxTrackedPositions
//  4. Adjust upward if agents frequently searched again
func (rl *RecallLearner) GetLearnedRecallTarget() float64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.totalObservations < 5 {
		return DefaultRecallTarget
	}

	targetPosition := rl.computeTargetPositionLocked()
	recall := float64(targetPosition+1) / float64(MaxTrackedPositions)
	recall = rl.applySearchedAgainAdjustment(recall)
	return clampRecall(recall)
}

// computeTargetPositionLocked finds the position that captures RecallTargetQuantile of usage.
// Uses Thompson Sampling to balance exploration/exploitation.
func (rl *RecallLearner) computeTargetPositionLocked() int {
	targetThreshold := 1.0 - RecallTargetQuantile

	for i, posterior := range rl.positionUsage {
		notUsedProb := rl.sampleBetaLocked(posterior.Beta, posterior.Alpha)
		if notUsedProb > targetThreshold {
			return i
		}
	}

	return MaxTrackedPositions - 1
}

func (rl *RecallLearner) applySearchedAgainAdjustment(recall float64) float64 {
	if rl.totalObservations == 0 {
		return recall
	}

	searchedAgainRate := float64(rl.searchedAgainCount) / float64(rl.totalObservations)
	boost := searchedAgainRate * 0.1
	return recall + boost
}

// GetConfidence returns confidence in the learned recall target (0 to 1).
// Confidence increases with number of observations and consistency of behavior.
func (rl *RecallLearner) GetConfidence() float64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.totalObservations == 0 {
		return 0.0
	}

	baseConfidence := float64(rl.totalObservations) / float64(rl.totalObservations+MinObservationsForConfidence)
	varianceMultiplier := rl.computeVarianceMultiplierLocked()
	return baseConfidence * varianceMultiplier
}

func (rl *RecallLearner) computeVarianceMultiplierLocked() float64 {
	if len(rl.positionUsage) == 0 {
		return 1.0
	}

	var totalVariance float64
	for _, posterior := range rl.positionUsage {
		totalVariance += posterior.Variance()
	}
	avgVariance := totalVariance / float64(len(rl.positionUsage))

	const maxBetaVariance = 1.0 / 12.0
	varianceRatio := avgVariance / maxBetaVariance
	multiplier := 1.0 - (varianceRatio * 0.5)

	return math.Max(0.5, multiplier)
}

// =============================================================================
// LearnRecallTarget - Single Observation Learning
// =============================================================================

// LearnRecallTarget computes the optimal recall target from a single usage observation.
// This is a stateless computation method that doesn't update internal state.
// For state-updating usage, use ObserveUsage followed by GetLearnedRecallTarget.
//
// Parameters:
//   - prefetched: IDs that were returned by search, in ranked order
//   - used: IDs that the agent actually referenced/used
//   - searchedAgain: true if the agent performed additional searches
//
// Returns:
//   - Suggested recall target adjustment as a delta from current target
//   - Positive values suggest increasing recall, negative suggest decreasing
func (rl *RecallLearner) LearnRecallTarget(prefetched, used []string, searchedAgain bool) float64 {
	if len(prefetched) == 0 {
		return 0.0
	}

	usedSet := make(map[string]struct{}, len(used))
	for _, id := range used {
		usedSet[id] = struct{}{}
	}

	deepestUsed := -1
	for i, id := range prefetched {
		if _, ok := usedSet[id]; ok {
			if i > deepestUsed {
				deepestUsed = i
			}
		}
	}

	var observedRecall float64
	if deepestUsed < 0 {
		observedRecall = 0.0
	} else {
		observedRecall = float64(deepestUsed+1) / float64(len(prefetched))
	}

	delta := observedRecall - DefaultRecallTarget

	if searchedAgain {
		delta += 0.05
	}

	return delta
}

// =============================================================================
// Statistics and Debugging
// =============================================================================

// GetObservationCount returns the total number of usage observations.
func (rl *RecallLearner) GetObservationCount() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.totalObservations
}

// GetSearchedAgainRate returns the fraction of episodes where agent searched again.
func (rl *RecallLearner) GetSearchedAgainRate() float64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.totalObservations == 0 {
		return 0.0
	}
	return float64(rl.searchedAgainCount) / float64(rl.totalObservations)
}

// GetPositionStats returns the Beta posteriors for position usage.
// Useful for debugging and understanding learned behavior.
func (rl *RecallLearner) GetPositionStats() []BetaPosterior {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	stats := make([]BetaPosterior, len(rl.positionUsage))
	copy(stats, rl.positionUsage)
	return stats
}

// GetCodebaseID returns the codebase this learner is for.
func (rl *RecallLearner) GetCodebaseID() string {
	return rl.codebaseID
}

// =============================================================================
// Internal Helpers
// =============================================================================

// sampleBetaLocked samples from a Beta distribution using Thompson Sampling.
func (rl *RecallLearner) sampleBetaLocked(alpha, beta float64) float64 {
	x := rl.sampleGammaLocked(alpha)
	y := rl.sampleGammaLocked(beta)
	if x+y == 0 {
		return 0.5
	}
	return x / (x + y)
}

// sampleGammaLocked samples from a Gamma distribution using Marsaglia-Tsang.
func (rl *RecallLearner) sampleGammaLocked(shape float64) float64 {
	if shape <= 0 {
		return 0
	}

	if shape < 1 {
		u := rl.rng.Float64()
		if u == 0 {
			u = 1e-10
		}
		return rl.sampleGammaLocked(1+shape) * math.Pow(u, 1/shape)
	}

	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	const maxIterations = 1000
	for iter := 0; iter < maxIterations; iter++ {
		x := rl.rng.NormFloat64()
		v := 1.0 + c*x
		if v <= 0 {
			continue
		}

		v = v * v * v
		u := rl.rng.Float64()

		if u < 1.0-0.0331*(x*x)*(x*x) {
			return d * v
		}

		if u > 0 && math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v)) {
			return d * v
		}
	}

	return shape
}

// clampRecall clamps recall to valid range [0.1, 1.0].
func clampRecall(recall float64) float64 {
	if recall < 0.1 {
		return 0.1
	}
	if recall > 1.0 {
		return 1.0
	}
	return recall
}
