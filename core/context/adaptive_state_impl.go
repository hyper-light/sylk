// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.3.4: AdaptiveState Extended Methods - central coordinator for adaptive learning.
package context

import (
	"time"
)

// =============================================================================
// Threshold Sampling
// =============================================================================

// SampleThresholds draws from current threshold distributions using Thompson Sampling.
func (a *AdaptiveState) SampleThresholds() ThresholdConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return ThresholdConfig{
		Confidence: a.Thresholds.Confidence.Sample(),
		Excerpt:    a.Thresholds.Excerpt.Sample(),
		Budget:     a.Thresholds.Budget.Sample(),
	}
}

// GetMeanThresholds returns the mean values of threshold distributions.
func (a *AdaptiveState) GetMeanThresholds() ThresholdConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return ThresholdConfig{
		Confidence: a.Thresholds.Confidence.Mean(),
		Excerpt:    a.Thresholds.Excerpt.Mean(),
		Budget:     a.Thresholds.Budget.Mean(),
	}
}

// GetMeanWeights returns the mean values of weight distributions.
func (a *AdaptiveState) GetMeanWeights() RewardWeights {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return RewardWeights{
		TaskSuccess:     a.Weights.TaskSuccess.Mean(),
		RelevanceBonus:  a.Weights.RelevanceBonus.Mean(),
		StrugglePenalty: a.Weights.StrugglePenalty.Mean(),
		WastePenalty:    a.Weights.WastePenalty.Mean(),
	}
}

// =============================================================================
// Update From Outcome
// =============================================================================

// UpdateFromOutcome performs robust Bayesian update from an episode observation.
func (a *AdaptiveState) UpdateFromOutcome(obs EpisodeObservation) {
	a.mu.Lock()
	defer a.mu.Unlock()

	satisfaction := obs.InferSatisfaction()
	a.updateWeights(obs, satisfaction)
	a.updateThresholds(obs, satisfaction)
	a.updateUserProfile(obs, satisfaction)
	a.updateContextDiscovery(obs)
	a.updateMetadata()
}

// updateWeights updates all weight distributions based on the observation.
func (a *AdaptiveState) updateWeights(obs EpisodeObservation, satisfaction float64) {
	config := a.config
	if config == nil {
		config = DefaultUpdateConfig()
	}

	// Update task success weight
	taskObservation := a.computeTaskSuccessObservation(obs)
	a.Weights.TaskSuccess.Update(taskObservation, config)

	// Update relevance bonus weight
	relevanceObservation := a.computeRelevanceObservation(obs)
	a.Weights.RelevanceBonus.Update(relevanceObservation, config)

	// Update struggle penalty weight
	struggleObservation := a.computeStruggleObservation(obs)
	a.Weights.StrugglePenalty.Update(struggleObservation, config)

	// Update waste penalty weight
	wasteObservation := a.computeWasteObservation(obs)
	a.Weights.WastePenalty.Update(wasteObservation, config)
}

// computeTaskSuccessObservation computes observation value for task success weight.
func (a *AdaptiveState) computeTaskSuccessObservation(obs EpisodeObservation) float64 {
	if obs.TaskCompleted {
		return 1.0
	}
	return 0.0
}

// computeRelevanceObservation computes observation value for relevance bonus weight.
func (a *AdaptiveState) computeRelevanceObservation(obs EpisodeObservation) float64 {
	if len(obs.PrefetchedIDs) == 0 {
		return 0.5
	}
	usedRatio := float64(len(obs.UsedIDs)) / float64(len(obs.PrefetchedIDs))
	return clampObservation(usedRatio)
}

// computeStruggleObservation computes observation value for struggle penalty weight.
func (a *AdaptiveState) computeStruggleObservation(obs EpisodeObservation) float64 {
	struggleIndicators := obs.FollowUpCount + len(obs.SearchedAfter)
	if struggleIndicators == 0 {
		return 1.0 // No struggle = high observation
	}
	// More struggle = lower observation
	return clampObservation(1.0 - float64(struggleIndicators)*0.1)
}

// computeWasteObservation computes observation value for waste penalty weight.
func (a *AdaptiveState) computeWasteObservation(obs EpisodeObservation) float64 {
	waste := len(obs.PrefetchedIDs) - len(obs.UsedIDs)
	if waste <= 0 {
		return 1.0 // No waste = high observation
	}
	// More waste = lower observation
	return clampObservation(1.0 - float64(waste)*0.05)
}

// clampObservation clamps an observation value to [0, 1].
func clampObservation(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// updateThresholds updates threshold distributions based on the observation.
func (a *AdaptiveState) updateThresholds(obs EpisodeObservation, satisfaction float64) {
	config := a.config
	if config == nil {
		config = DefaultUpdateConfig()
	}

	// Update confidence threshold based on hedging behavior
	confidenceObs := a.computeConfidenceObservation(obs)
	a.Thresholds.Confidence.Update(confidenceObs, config)

	// Update excerpt threshold based on usage
	excerptObs := a.computeExcerptObservation(obs)
	a.Thresholds.Excerpt.Update(excerptObs, config)

	// Update budget threshold based on prefetch efficiency
	budgetObs := a.computeBudgetObservation(obs)
	a.Thresholds.Budget.Update(budgetObs, config)
}

// computeConfidenceObservation computes observation value for confidence threshold.
func (a *AdaptiveState) computeConfidenceObservation(obs EpisodeObservation) float64 {
	if obs.HedgingDetected {
		return 0.3 // Lower confidence when hedging
	}
	if obs.TaskCompleted && obs.FollowUpCount == 0 {
		return 0.9 // High confidence when successful without follow-ups
	}
	return 0.5
}

// computeExcerptObservation computes observation value for excerpt threshold.
func (a *AdaptiveState) computeExcerptObservation(obs EpisodeObservation) float64 {
	if len(obs.UsedIDs) == 0 {
		return 0.5
	}
	// Higher usage = excerpts were useful
	return clampObservation(float64(len(obs.UsedIDs)) * 0.2)
}

// computeBudgetObservation computes observation value for budget threshold.
func (a *AdaptiveState) computeBudgetObservation(obs EpisodeObservation) float64 {
	if len(obs.PrefetchedIDs) == 0 {
		return 0.5
	}
	// Efficient use of prefetch = higher budget threshold
	efficiency := float64(len(obs.UsedIDs)) / float64(len(obs.PrefetchedIDs))
	return clampObservation(efficiency)
}

// updateUserProfile updates the user profile from the observation.
func (a *AdaptiveState) updateUserProfile(obs EpisodeObservation, satisfaction float64) {
	a.UserProfile.ObservationCount++
	a.UserProfile.LastUpdated = time.Now()

	// Update preferences based on behavior
	a.updateUserPreferences(obs, satisfaction)
	a.updateUserMultipliers(obs, satisfaction)
}

// updateUserPreferences updates user preference indicators.
func (a *AdaptiveState) updateUserPreferences(obs EpisodeObservation, satisfaction float64) {
	learningRate := 0.1

	// PrefersThorough: positive with waste + positive satisfaction
	waste := len(obs.PrefetchedIDs) - len(obs.UsedIDs)
	if satisfaction > 0 && waste > 2 {
		a.UserProfile.PrefersThorough += learningRate * satisfaction * 0.1
	}

	// ToleratesSearches: positive with searches + positive satisfaction
	if satisfaction > 0 && len(obs.SearchedAfter) > 0 {
		a.UserProfile.ToleratesSearches += learningRate * satisfaction * 0.1
	} else if satisfaction < 0 && len(obs.SearchedAfter) > 0 {
		a.UserProfile.ToleratesSearches -= learningRate * (-satisfaction) * 0.1
	}

	// Clamp preferences
	a.UserProfile.PrefersThorough = clampValue(a.UserProfile.PrefersThorough, -1, 1)
	a.UserProfile.ToleratesSearches = clampValue(a.UserProfile.ToleratesSearches, -1, 1)
}

// updateUserMultipliers updates user reward multipliers.
func (a *AdaptiveState) updateUserMultipliers(obs EpisodeObservation, satisfaction float64) {
	learningRate := 0.1

	// Waste penalty multiplier
	waste := len(obs.PrefetchedIDs) - len(obs.UsedIDs)
	if satisfaction > 0 && waste > 2 {
		a.UserProfile.WastePenaltyMult -= learningRate * 0.05
	}

	// Struggle penalty multiplier
	struggle := obs.FollowUpCount + len(obs.SearchedAfter)
	if satisfaction < 0 && struggle > 2 {
		a.UserProfile.StrugglePenaltyMult += learningRate * 0.05
	}

	// Clamp multipliers
	a.UserProfile.WastePenaltyMult = clampValue(a.UserProfile.WastePenaltyMult, 0.5, 2.0)
	a.UserProfile.StrugglePenaltyMult = clampValue(a.UserProfile.StrugglePenaltyMult, 0.5, 2.0)
}

// updateContextDiscovery updates context discovery if available.
func (a *AdaptiveState) updateContextDiscovery(obs EpisodeObservation) {
	if a.ContextDiscovery == nil || obs.TaskContext == "" {
		return
	}

	// Update bias for the context
	for i := range a.ContextDiscovery.Centroids {
		if a.ContextDiscovery.Centroids[i].ID == obs.TaskContext {
			a.updateContextBias(&a.ContextDiscovery.Centroids[i].Bias, obs)
			return
		}
	}
}

// updateContextBias updates the bias for a specific context.
func (a *AdaptiveState) updateContextBias(bias *ContextBias, obs EpisodeObservation) {
	satisfaction := obs.InferSatisfaction()
	learningRate := 0.1

	// Update relevance multiplier
	if len(obs.SearchedAfter) > 0 {
		bias.RelevanceMult += learningRate * 0.05
	}

	// Update struggle multiplier
	if obs.FollowUpCount > 2 {
		bias.StruggleMult += learningRate * satisfaction * 0.05
	}

	// Update waste multiplier
	waste := len(obs.PrefetchedIDs) - len(obs.UsedIDs)
	if waste > 3 {
		bias.WasteMult += learningRate * 0.05
	}

	// Clamp multipliers
	bias.RelevanceMult = clampValue(bias.RelevanceMult, 0.5, 2.0)
	bias.StruggleMult = clampValue(bias.StruggleMult, 0.5, 2.0)
	bias.WasteMult = clampValue(bias.WasteMult, 0.5, 2.0)

	bias.ObservationCount++
}

// updateMetadata updates state metadata.
func (a *AdaptiveState) updateMetadata() {
	a.TotalObservations++
	a.LastUpdated = time.Now()
}

// =============================================================================
// Query Methods
// =============================================================================

// GetTotalObservations returns the total number of observations processed.
func (a *AdaptiveState) GetTotalObservations() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.TotalObservations
}

// GetLastUpdated returns the last update time.
func (a *AdaptiveState) GetLastUpdated() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.LastUpdated
}

// GetVersion returns the state version.
func (a *AdaptiveState) GetVersion() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Version
}

// GetUserProfile returns a copy of the user profile.
func (a *AdaptiveState) GetUserProfile() UserWeightProfile {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.UserProfile
}

// GetContextDiscovery returns the context discovery component.
func (a *AdaptiveState) GetContextDiscovery() *ContextDiscovery {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ContextDiscovery
}

// =============================================================================
// Confidence Metrics
// =============================================================================

// WeightConfidence returns confidence levels for all weight distributions.
func (a *AdaptiveState) WeightConfidence() map[string]float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return map[string]float64{
		"task_success":     a.Weights.TaskSuccess.Confidence(),
		"relevance_bonus":  a.Weights.RelevanceBonus.Confidence(),
		"struggle_penalty": a.Weights.StrugglePenalty.Confidence(),
		"waste_penalty":    a.Weights.WastePenalty.Confidence(),
	}
}

// ThresholdConfidence returns confidence levels for all threshold distributions.
func (a *AdaptiveState) ThresholdConfidence() map[string]float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return map[string]float64{
		"confidence": a.Thresholds.Confidence.Confidence(),
		"excerpt":    a.Thresholds.Excerpt.Confidence(),
		"budget":     a.Thresholds.Budget.Confidence(),
	}
}

// IsConverged returns true if all distributions have likely converged.
func (a *AdaptiveState) IsConverged(varianceThreshold float64) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.Weights.TaskSuccess.Variance() < varianceThreshold &&
		a.Weights.RelevanceBonus.Variance() < varianceThreshold &&
		a.Weights.StrugglePenalty.Variance() < varianceThreshold &&
		a.Weights.WastePenalty.Variance() < varianceThreshold &&
		a.Thresholds.Confidence.Variance() < varianceThreshold &&
		a.Thresholds.Excerpt.Variance() < varianceThreshold &&
		a.Thresholds.Budget.Variance() < varianceThreshold
}

// =============================================================================
// Reset and Configuration
// =============================================================================

// SetUpdateConfig sets the update configuration.
func (a *AdaptiveState) SetUpdateConfig(config *UpdateConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config = config
}

// Reset resets the state to initial priors.
func (a *AdaptiveState) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.initWeights()
	a.initThresholds()
	a.ContextDiscovery = &ContextDiscovery{MaxContexts: 10}
	a.UserProfile = UserWeightProfile{
		WastePenaltyMult:    1.0,
		StrugglePenaltyMult: 1.0,
	}
	a.TotalObservations = 0
	a.LastUpdated = time.Time{}
	a.Version++
}

// =============================================================================
// State Summary
// =============================================================================

// StateSummary provides a summary of the adaptive state.
type StateSummary struct {
	TotalObservations int64
	LastUpdated       time.Time
	Version           int
	IsConverged       bool

	MeanWeights    RewardWeights
	MeanThresholds ThresholdConfig

	WeightConfidence    map[string]float64
	ThresholdConfidence map[string]float64

	UserProfileReady bool
	ContextCount     int
}

// Summary returns a summary of the current state.
func (a *AdaptiveState) Summary() StateSummary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	contextCount := 0
	if a.ContextDiscovery != nil {
		contextCount = len(a.ContextDiscovery.Centroids)
	}

	return StateSummary{
		TotalObservations: a.TotalObservations,
		LastUpdated:       a.LastUpdated,
		Version:           a.Version,
		IsConverged:       a.isConvergedUnsafe(0.01),

		MeanWeights: RewardWeights{
			TaskSuccess:     a.Weights.TaskSuccess.Mean(),
			RelevanceBonus:  a.Weights.RelevanceBonus.Mean(),
			StrugglePenalty: a.Weights.StrugglePenalty.Mean(),
			WastePenalty:    a.Weights.WastePenalty.Mean(),
		},
		MeanThresholds: ThresholdConfig{
			Confidence: a.Thresholds.Confidence.Mean(),
			Excerpt:    a.Thresholds.Excerpt.Mean(),
			Budget:     a.Thresholds.Budget.Mean(),
		},

		WeightConfidence: map[string]float64{
			"task_success":     a.Weights.TaskSuccess.Confidence(),
			"relevance_bonus":  a.Weights.RelevanceBonus.Confidence(),
			"struggle_penalty": a.Weights.StrugglePenalty.Confidence(),
			"waste_penalty":    a.Weights.WastePenalty.Confidence(),
		},
		ThresholdConfidence: map[string]float64{
			"confidence": a.Thresholds.Confidence.Confidence(),
			"excerpt":    a.Thresholds.Excerpt.Confidence(),
			"budget":     a.Thresholds.Budget.Confidence(),
		},

		UserProfileReady: a.UserProfile.ObservationCount >= 5,
		ContextCount:     contextCount,
	}
}

// isConvergedUnsafe checks convergence without locking.
func (a *AdaptiveState) isConvergedUnsafe(threshold float64) bool {
	return a.Weights.TaskSuccess.Variance() < threshold &&
		a.Weights.RelevanceBonus.Variance() < threshold &&
		a.Weights.StrugglePenalty.Variance() < threshold &&
		a.Weights.WastePenalty.Variance() < threshold
}
