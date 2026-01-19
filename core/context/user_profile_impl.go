// Package context provides types and utilities for adaptive retrieval context management.
// This file implements AR.3.3: UserProfile Learning - individual preference learning from behavior.
package context

import (
	"sync"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

// DefaultMinObservationsForProfile is the minimum observations before applying profile adjustments.
const DefaultMinObservationsForProfile = 5

// DefaultProfileLearningRate is the default learning rate for profile updates.
const DefaultProfileLearningRate = 0.1

// DefaultPreferenceDecay is the decay rate for preference signals.
const DefaultPreferenceDecay = 0.99

// DefaultMinMultiplier is the minimum value for multipliers.
const DefaultMinMultiplier = 0.5

// DefaultMaxMultiplier is the maximum value for multipliers.
const DefaultMaxMultiplier = 2.0

// =============================================================================
// Configuration
// =============================================================================

// UserProfileConfig holds configuration for user profile learning.
type UserProfileConfig struct {
	// MinObservations is the minimum observations before applying adjustments.
	MinObservations int

	// LearningRate controls how quickly preferences change.
	LearningRate float64

	// PreferenceDecay is the decay rate for old preference signals.
	PreferenceDecay float64

	// MinMultiplier is the minimum value for multipliers.
	MinMultiplier float64

	// MaxMultiplier is the maximum value for multipliers.
	MaxMultiplier float64
}

// DefaultUserProfileConfig returns the default configuration.
func DefaultUserProfileConfig() UserProfileConfig {
	return UserProfileConfig{
		MinObservations: DefaultMinObservationsForProfile,
		LearningRate:    DefaultProfileLearningRate,
		PreferenceDecay: DefaultPreferenceDecay,
		MinMultiplier:   DefaultMinMultiplier,
		MaxMultiplier:   DefaultMaxMultiplier,
	}
}

// =============================================================================
// User Profile Manager
// =============================================================================

// UserProfileManager provides full user profile learning functionality.
type UserProfileManager struct {
	mu sync.RWMutex

	profile *UserWeightProfile
	config  UserProfileConfig

	// Running statistics for preference detection
	avgPrefetchWaste float64
	avgFollowUps     float64
	avgSearches      float64
	statsCount       int
}

// NewUserProfileManager creates a new user profile manager.
func NewUserProfileManager(config UserProfileConfig) *UserProfileManager {
	if config.MinObservations <= 0 {
		config.MinObservations = DefaultMinObservationsForProfile
	}
	if config.LearningRate <= 0 {
		config.LearningRate = DefaultProfileLearningRate
	}
	if config.PreferenceDecay <= 0 {
		config.PreferenceDecay = DefaultPreferenceDecay
	}
	if config.MinMultiplier <= 0 {
		config.MinMultiplier = DefaultMinMultiplier
	}
	if config.MaxMultiplier <= 0 {
		config.MaxMultiplier = DefaultMaxMultiplier
	}

	return &UserProfileManager{
		profile: &UserWeightProfile{
			WastePenaltyMult:    1.0,
			StrugglePenaltyMult: 1.0,
		},
		config: config,
	}
}

// NewDefaultUserProfileManager creates a manager with default config.
func NewDefaultUserProfileManager() *UserProfileManager {
	return NewUserProfileManager(DefaultUserProfileConfig())
}

// =============================================================================
// Update Methods
// =============================================================================

// UpdateFromObservation updates the user profile based on an episode observation.
func (m *UserProfileManager) UpdateFromObservation(obs *EpisodeObservation) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.updateRunningStats(obs)
	m.updatePreferences(obs)
	m.updateMultipliers(obs)
	m.updateMetadata()
}

// updateRunningStats updates the running statistics from the observation.
func (m *UserProfileManager) updateRunningStats(obs *EpisodeObservation) {
	m.statsCount++

	// Calculate current observation stats
	prefetchWaste := float64(len(obs.PrefetchedIDs) - len(obs.UsedIDs))
	if prefetchWaste < 0 {
		prefetchWaste = 0
	}
	followUps := float64(obs.FollowUpCount)
	searches := float64(len(obs.SearchedAfter))

	// Exponential moving average
	alpha := m.config.LearningRate
	if m.statsCount == 1 {
		m.avgPrefetchWaste = prefetchWaste
		m.avgFollowUps = followUps
		m.avgSearches = searches
	} else {
		m.avgPrefetchWaste = alpha*prefetchWaste + (1-alpha)*m.avgPrefetchWaste
		m.avgFollowUps = alpha*followUps + (1-alpha)*m.avgFollowUps
		m.avgSearches = alpha*searches + (1-alpha)*m.avgSearches
	}
}

// updatePreferences updates the preference indicators.
func (m *UserProfileManager) updatePreferences(obs *EpisodeObservation) {
	satisfaction := obs.InferSatisfaction()

	m.updateThoroughPreference(obs, satisfaction)
	m.updateSearchTolerance(obs, satisfaction)
}

// updateThoroughPreference updates the "prefers thorough" indicator.
func (m *UserProfileManager) updateThoroughPreference(obs *EpisodeObservation, satisfaction float64) {
	// User prefers thorough context if:
	// - High satisfaction with high prefetch (likes having extra context)
	// - Low satisfaction with many searches (suffered from insufficient context)

	prefetchWaste := float64(len(obs.PrefetchedIDs) - len(obs.UsedIDs))
	searchCount := float64(len(obs.SearchedAfter))

	delta := 0.0

	// Positive satisfaction with extra prefetch = tolerates/prefers thorough
	if satisfaction > 0 && prefetchWaste > 2 {
		delta += m.config.LearningRate * satisfaction * 0.1
	}

	// Negative satisfaction with many searches = wants more thorough
	if satisfaction < 0 && searchCount > 2 {
		delta += m.config.LearningRate * (-satisfaction) * 0.15
	}

	m.profile.PrefersThorough = clampValue(
		m.profile.PrefersThorough+delta,
		-1.0,
		1.0,
	)
}

// updateSearchTolerance updates the "tolerates searches" indicator.
func (m *UserProfileManager) updateSearchTolerance(obs *EpisodeObservation, satisfaction float64) {
	searchCount := float64(len(obs.SearchedAfter))

	delta := 0.0

	// Positive satisfaction despite searches = tolerates searches
	if satisfaction > 0 && searchCount > 0 {
		delta += m.config.LearningRate * satisfaction * 0.1
	}

	// Negative satisfaction with searches = dislikes searches
	if satisfaction < 0 && searchCount > 0 {
		delta -= m.config.LearningRate * (-satisfaction) * 0.1
	}

	m.profile.ToleratesSearches = clampValue(
		m.profile.ToleratesSearches+delta,
		-1.0,
		1.0,
	)
}

// updateMultipliers updates the reward weight multipliers.
func (m *UserProfileManager) updateMultipliers(obs *EpisodeObservation) {
	satisfaction := obs.InferSatisfaction()

	m.updateWastePenaltyMult(obs, satisfaction)
	m.updateStrugglePenaltyMult(obs, satisfaction)
}

// updateWastePenaltyMult updates the waste penalty multiplier.
func (m *UserProfileManager) updateWastePenaltyMult(obs *EpisodeObservation, satisfaction float64) {
	prefetchWaste := len(obs.PrefetchedIDs) - len(obs.UsedIDs)
	if prefetchWaste < 0 {
		prefetchWaste = 0
	}

	// If user is satisfied despite waste, reduce penalty
	// If user is unsatisfied with waste, increase penalty
	delta := 0.0

	if satisfaction > 0 && prefetchWaste > 2 {
		// User happy despite waste = reduce penalty
		delta = -m.config.LearningRate * satisfaction * 0.05
	} else if satisfaction < 0 && prefetchWaste > 0 {
		// User unhappy with waste = increase penalty (but not as strongly)
		delta = m.config.LearningRate * (-satisfaction) * 0.03
	}

	m.profile.WastePenaltyMult = clampValue(
		m.profile.WastePenaltyMult+delta,
		m.config.MinMultiplier,
		m.config.MaxMultiplier,
	)
}

// updateStrugglePenaltyMult updates the struggle penalty multiplier.
func (m *UserProfileManager) updateStrugglePenaltyMult(obs *EpisodeObservation, satisfaction float64) {
	followUps := obs.FollowUpCount
	searches := len(obs.SearchedAfter)
	struggleIndicator := followUps + searches

	delta := 0.0

	// If user is unsatisfied with struggle, increase penalty
	if satisfaction < 0 && struggleIndicator > 2 {
		delta = m.config.LearningRate * (-satisfaction) * 0.05
	}

	// If user is satisfied despite some struggle, reduce penalty
	if satisfaction > 0 && struggleIndicator > 0 && struggleIndicator <= 2 {
		delta = -m.config.LearningRate * satisfaction * 0.03
	}

	m.profile.StrugglePenaltyMult = clampValue(
		m.profile.StrugglePenaltyMult+delta,
		m.config.MinMultiplier,
		m.config.MaxMultiplier,
	)
}

// updateMetadata updates the profile metadata.
func (m *UserProfileManager) updateMetadata() {
	m.profile.ObservationCount++
	m.profile.LastUpdated = time.Now()
}

// =============================================================================
// Query Methods
// =============================================================================

// GetProfile returns a copy of the current profile.
func (m *UserProfileManager) GetProfile() UserWeightProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return *m.profile
}

// Adjust applies the profile to reward weights if sufficient observations exist.
func (m *UserProfileManager) Adjust(base RewardWeights) RewardWeights {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.profile.ObservationCount < m.config.MinObservations {
		return base
	}

	return m.profile.Adjust(base)
}

// GetObservationCount returns the number of observations processed.
func (m *UserProfileManager) GetObservationCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profile.ObservationCount
}

// GetLastUpdated returns the last update time.
func (m *UserProfileManager) GetLastUpdated() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profile.LastUpdated
}

// IsReady returns true if the profile has enough observations.
func (m *UserProfileManager) IsReady() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profile.ObservationCount >= m.config.MinObservations
}

// GetPrefersThorough returns the thoroughness preference indicator.
func (m *UserProfileManager) GetPrefersThorough() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profile.PrefersThorough
}

// GetToleratesSearches returns the search tolerance indicator.
func (m *UserProfileManager) GetToleratesSearches() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profile.ToleratesSearches
}

// GetRunningStats returns the current running statistics.
func (m *UserProfileManager) GetRunningStats() (avgWaste, avgFollowUps, avgSearches float64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.avgPrefetchWaste, m.avgFollowUps, m.avgSearches
}

// =============================================================================
// Persistence
// =============================================================================

// RestoreFromProfile restores state from a UserWeightProfile.
func (m *UserProfileManager) RestoreFromProfile(profile *UserWeightProfile) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.profile = &UserWeightProfile{
		PrefersThorough:     profile.PrefersThorough,
		ToleratesSearches:   profile.ToleratesSearches,
		WastePenaltyMult:    profile.WastePenaltyMult,
		StrugglePenaltyMult: profile.StrugglePenaltyMult,
		ObservationCount:    profile.ObservationCount,
		LastUpdated:         profile.LastUpdated,
	}

	// Ensure multipliers are within bounds
	m.profile.WastePenaltyMult = clampValue(
		m.profile.WastePenaltyMult,
		m.config.MinMultiplier,
		m.config.MaxMultiplier,
	)
	m.profile.StrugglePenaltyMult = clampValue(
		m.profile.StrugglePenaltyMult,
		m.config.MinMultiplier,
		m.config.MaxMultiplier,
	)
}

// Reset resets the profile to default values.
func (m *UserProfileManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.profile = &UserWeightProfile{
		WastePenaltyMult:    1.0,
		StrugglePenaltyMult: 1.0,
	}
	m.avgPrefetchWaste = 0
	m.avgFollowUps = 0
	m.avgSearches = 0
	m.statsCount = 0
}

// =============================================================================
// Utility Functions
// =============================================================================

// clampValue clamps a value to the given range.
func clampValue(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// =============================================================================
// Profile Analysis
// =============================================================================

// ProfileAnalysis provides analysis of user preferences.
type ProfileAnalysis struct {
	IsReady            bool
	ObservationCount   int
	PrefersThorough    bool
	ToleratesSearches  bool
	WasteSensitivity   string // "low", "normal", "high"
	StruggleSensitivity string // "low", "normal", "high"
}

// Analyze returns an analysis of the current profile.
func (m *UserProfileManager) Analyze() ProfileAnalysis {
	m.mu.RLock()
	defer m.mu.RUnlock()

	analysis := ProfileAnalysis{
		IsReady:          m.profile.ObservationCount >= m.config.MinObservations,
		ObservationCount: m.profile.ObservationCount,
	}

	// Interpret preferences
	analysis.PrefersThorough = m.profile.PrefersThorough > 0.2
	analysis.ToleratesSearches = m.profile.ToleratesSearches > 0.2

	// Categorize sensitivities
	analysis.WasteSensitivity = categorizeSensitivity(m.profile.WastePenaltyMult)
	analysis.StruggleSensitivity = categorizeSensitivity(m.profile.StrugglePenaltyMult)

	return analysis
}

// categorizeSensitivity converts a multiplier to a sensitivity category.
func categorizeSensitivity(mult float64) string {
	if mult < 0.8 {
		return "low"
	}
	if mult > 1.2 {
		return "high"
	}
	return "normal"
}
