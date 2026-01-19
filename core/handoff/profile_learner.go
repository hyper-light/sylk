package handoff

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// =============================================================================
// HO.10.1 ProfileLearner - Learning Loop for AgentHandoffProfile
// =============================================================================
//
// ProfileLearner manages the learning loop for AgentHandoffProfile instances.
// It records observations, learns from handoff outcomes, and maintains profiles
// for agent+model combinations.
//
// Key features:
//   - RecordObservation: Updates profile from handoff observations
//   - RecordHandoffOutcome: Learns from handoff success/failure
//   - GetProfile: Retrieves profile with hierarchical fallback
//   - Uses PriorHierarchy for cold start handling
//   - Periodic persistence via WAL integration
//
// Thread Safety:
//   - All operations are protected by appropriate synchronization
//   - Safe for concurrent use from multiple goroutines

// ProfileLearnerConfig configures the ProfileLearner behavior.
type ProfileLearnerConfig struct {
	// UpdateConfig controls how observations update profiles.
	UpdateConfig *UpdateConfig `json:"update_config"`

	// DefaultAgentType is used when agent type is not specified.
	DefaultAgentType string `json:"default_agent_type"`

	// DefaultModelID is used when model ID is not specified.
	DefaultModelID string `json:"default_model_id"`

	// MinObservationsForConfidence is the minimum observations needed
	// before using instance-level profile instead of hierarchical fallback.
	MinObservationsForConfidence int `json:"min_observations_for_confidence"`

	// PersistenceInterval controls how often profiles are persisted.
	PersistenceInterval time.Duration `json:"persistence_interval"`

	// EnableHierarchicalFallback enables using PriorHierarchy for cold start.
	EnableHierarchicalFallback bool `json:"enable_hierarchical_fallback"`
}

// DefaultProfileLearnerConfig returns sensible defaults for ProfileLearner.
func DefaultProfileLearnerConfig() *ProfileLearnerConfig {
	return &ProfileLearnerConfig{
		UpdateConfig:                 DefaultUpdateConfig(),
		DefaultAgentType:             "default",
		DefaultModelID:               "default",
		MinObservationsForConfidence: 5,
		PersistenceInterval:          5 * time.Minute,
		EnableHierarchicalFallback:   true,
	}
}

// Clone creates a deep copy of the config.
func (c *ProfileLearnerConfig) Clone() *ProfileLearnerConfig {
	if c == nil {
		return nil
	}

	cloned := &ProfileLearnerConfig{
		DefaultAgentType:             c.DefaultAgentType,
		DefaultModelID:               c.DefaultModelID,
		MinObservationsForConfidence: c.MinObservationsForConfidence,
		PersistenceInterval:          c.PersistenceInterval,
		EnableHierarchicalFallback:   c.EnableHierarchicalFallback,
	}

	if c.UpdateConfig != nil {
		cloned.UpdateConfig = &UpdateConfig{
			LearningRate:   c.UpdateConfig.LearningRate,
			DriftThreshold: c.UpdateConfig.DriftThreshold,
			PriorBlendRate: c.UpdateConfig.PriorBlendRate,
		}
	}

	return cloned
}

// profileKey uniquely identifies an agent+model profile.
type profileKey struct {
	AgentID   string
	ModelName string
}

// ProfileLearner manages learning for AgentHandoffProfile instances.
type ProfileLearner struct {
	mu sync.RWMutex

	// profiles stores learned profiles by agent+model combination.
	profiles map[profileKey]*AgentHandoffProfile

	// priorHierarchy provides hierarchical fallback for cold start.
	priorHierarchy *PriorHierarchy

	// config holds learner configuration.
	config *ProfileLearnerConfig

	// lastPersisted tracks when profiles were last persisted.
	lastPersisted time.Time

	// pendingPersistence indicates if there are unpersisted changes.
	pendingPersistence bool

	// observations tracks total observations recorded.
	observations int64

	// handoffOutcomes tracks total handoff outcomes recorded.
	handoffOutcomes int64
}

// NewProfileLearner creates a new ProfileLearner with the given configuration.
// If config is nil, uses default configuration.
// If priorHierarchy is nil, creates a new one.
func NewProfileLearner(config *ProfileLearnerConfig, priorHierarchy *PriorHierarchy) *ProfileLearner {
	if config == nil {
		config = DefaultProfileLearnerConfig()
	}
	if priorHierarchy == nil {
		priorHierarchy = NewPriorHierarchy()
	}

	return &ProfileLearner{
		profiles:       make(map[profileKey]*AgentHandoffProfile),
		priorHierarchy: priorHierarchy,
		config:         config.Clone(),
		lastPersisted:  time.Now(),
	}
}

// =============================================================================
// Observation Recording
// =============================================================================

// RecordObservation updates the profile for the specified agent+model
// based on the observation. Creates a new profile if one doesn't exist.
func (pl *ProfileLearner) RecordObservation(agentID, modelName string, obs *HandoffObservation) {
	if obs == nil {
		return
	}

	pl.mu.Lock()
	defer pl.mu.Unlock()

	// Normalize identifiers
	agentID = pl.normalizeAgentID(agentID)
	modelName = pl.normalizeModelName(modelName)

	// Get or create profile
	key := profileKey{AgentID: agentID, ModelName: modelName}
	profile := pl.getOrCreateProfileLocked(key)

	// Update profile with observation
	profile.Update(obs)

	// Update prior hierarchy as well
	if pl.config.EnableHierarchicalFallback {
		priorObs := NewPriorObservation(
			obs.ContextSize,
			obs.QualityScore,
			obs.WasSuccessful,
			obs.TurnNumber,
		)
		pl.priorHierarchy.UpdateFromObservation(agentID, modelName, priorObs)
	}

	pl.observations++
	pl.pendingPersistence = true
}

// RecordHandoffOutcome records the outcome of a handoff operation,
// allowing the learner to adjust profiles based on success/failure.
func (pl *ProfileLearner) RecordHandoffOutcome(
	agentID, modelName string,
	contextSize, turnNumber int,
	success bool,
	quality float64,
) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	// Normalize identifiers
	agentID = pl.normalizeAgentID(agentID)
	modelName = pl.normalizeModelName(modelName)

	// Get or create profile
	key := profileKey{AgentID: agentID, ModelName: modelName}
	profile := pl.getOrCreateProfileLocked(key)

	// Create observation from outcome
	obs := NewHandoffObservation(
		contextSize,
		turnNumber,
		quality,
		success,
		true, // Handoff was triggered
	)

	// Update profile
	profile.Update(obs)

	// Update prior hierarchy
	if pl.config.EnableHierarchicalFallback {
		priorObs := NewPriorObservation(contextSize, quality, success, turnNumber)
		pl.priorHierarchy.UpdateFromObservation(agentID, modelName, priorObs)
	}

	pl.handoffOutcomes++
	pl.pendingPersistence = true
}

// =============================================================================
// Profile Retrieval
// =============================================================================

// GetProfile returns the profile for the specified agent+model combination.
// If hierarchical fallback is enabled and the profile has low confidence,
// the returned profile will be blended with higher-level priors.
func (pl *ProfileLearner) GetProfile(agentID, modelName string) *AgentHandoffProfile {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	// Normalize identifiers
	agentID = pl.normalizeAgentID(agentID)
	modelName = pl.normalizeModelName(modelName)

	key := profileKey{AgentID: agentID, ModelName: modelName}
	profile, exists := pl.profiles[key]

	// If no profile exists, create one with hierarchical priors
	if !exists {
		return pl.createProfileWithHierarchyLocked(agentID, modelName)
	}

	// If profile has low confidence, blend with hierarchy
	if pl.config.EnableHierarchicalFallback &&
		profile.EffectiveSamples < float64(pl.config.MinObservationsForConfidence) {
		return pl.blendProfileWithHierarchyLocked(profile, agentID, modelName)
	}

	// Return a copy to prevent external mutation
	return profile.Clone()
}

// GetProfileRaw returns the raw profile without hierarchical blending.
// Returns nil if no profile exists for the specified agent+model.
func (pl *ProfileLearner) GetProfileRaw(agentID, modelName string) *AgentHandoffProfile {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	agentID = pl.normalizeAgentID(agentID)
	modelName = pl.normalizeModelName(modelName)

	key := profileKey{AgentID: agentID, ModelName: modelName}
	profile, exists := pl.profiles[key]
	if !exists {
		return nil
	}

	return profile.Clone()
}

// HasProfile returns true if a profile exists for the specified agent+model.
func (pl *ProfileLearner) HasProfile(agentID, modelName string) bool {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	agentID = pl.normalizeAgentID(agentID)
	modelName = pl.normalizeModelName(modelName)

	key := profileKey{AgentID: agentID, ModelName: modelName}
	_, exists := pl.profiles[key]
	return exists
}

// =============================================================================
// Internal Methods
// =============================================================================

// normalizeAgentID ensures agent ID is valid.
func (pl *ProfileLearner) normalizeAgentID(agentID string) string {
	if agentID == "" {
		return pl.config.DefaultAgentType
	}
	return agentID
}

// normalizeModelName ensures model name is valid.
func (pl *ProfileLearner) normalizeModelName(modelName string) string {
	if modelName == "" {
		return pl.config.DefaultModelID
	}
	return modelName
}

// getOrCreateProfileLocked returns the profile for the key, creating if needed.
// Caller must hold the write lock.
func (pl *ProfileLearner) getOrCreateProfileLocked(key profileKey) *AgentHandoffProfile {
	profile, exists := pl.profiles[key]
	if !exists {
		profile = NewAgentHandoffProfile(key.AgentID, key.ModelName, "")
		pl.profiles[key] = profile
	}
	return profile
}

// createProfileWithHierarchyLocked creates a new profile initialized from hierarchy.
// Caller must hold at least a read lock.
func (pl *ProfileLearner) createProfileWithHierarchyLocked(agentID, modelName string) *AgentHandoffProfile {
	profile := NewAgentHandoffProfile(agentID, modelName, "")

	if !pl.config.EnableHierarchicalFallback {
		return profile
	}

	// Get effective priors from hierarchy
	effective := pl.priorHierarchy.GetEffectivePrior(agentID, modelName)

	// Apply hierarchical priors to profile
	if profile.OptimalHandoffThreshold != nil && effective.QualityThreshold > 0 {
		profile.OptimalHandoffThreshold.Alpha = effective.QualityThreshold * 10.0
		profile.OptimalHandoffThreshold.Beta = (1.0 - effective.QualityThreshold) * 10.0
	}

	if profile.OptimalPreparedSize != nil && effective.OptimalContextSize > 0 {
		profile.OptimalPreparedSize.Alpha = float64(effective.OptimalContextSize)
		profile.OptimalPreparedSize.Beta = 1.0
	}

	if profile.PreserveRecent != nil && effective.PreserveRecentTurns > 0 {
		profile.PreserveRecent.Alpha = float64(effective.PreserveRecentTurns)
		profile.PreserveRecent.Beta = 1.0
	}

	return profile
}

// blendProfileWithHierarchyLocked blends a low-confidence profile with hierarchy.
// Caller must hold at least a read lock.
func (pl *ProfileLearner) blendProfileWithHierarchyLocked(
	profile *AgentHandoffProfile,
	agentID, modelName string,
) *AgentHandoffProfile {
	// Get effective priors from hierarchy
	effective := pl.priorHierarchy.GetEffectivePrior(agentID, modelName)

	// Clone profile for modification
	blended := profile.Clone()

	// Calculate blend weight based on profile confidence
	profileConf := profile.Confidence()
	hierarchyWeight := 1.0 - profileConf

	// Blend optimal handoff threshold
	if blended.OptimalHandoffThreshold != nil {
		profileMean := blended.OptimalHandoffThreshold.Mean()
		blendedMean := profileConf*profileMean + hierarchyWeight*effective.QualityThreshold
		blended.OptimalHandoffThreshold.Alpha = blendedMean * 10.0
		blended.OptimalHandoffThreshold.Beta = (1.0 - blendedMean) * 10.0
	}

	// Blend optimal prepared size
	if blended.OptimalPreparedSize != nil {
		profileMean := float64(blended.OptimalPreparedSize.Mean())
		blendedMean := profileConf*profileMean + hierarchyWeight*float64(effective.OptimalContextSize)
		blended.OptimalPreparedSize.Alpha = blendedMean
		blended.OptimalPreparedSize.Beta = 1.0
	}

	// Blend preserve recent
	if blended.PreserveRecent != nil {
		profileMean := float64(blended.PreserveRecent.Mean())
		blendedMean := profileConf*profileMean + hierarchyWeight*float64(effective.PreserveRecentTurns)
		blended.PreserveRecent.Alpha = blendedMean
		blended.PreserveRecent.Beta = 1.0
	}

	return blended
}

// =============================================================================
// Persistence Support
// =============================================================================

// NeedsPersistence returns true if there are unpersisted changes.
func (pl *ProfileLearner) NeedsPersistence() bool {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return pl.pendingPersistence
}

// MarkPersisted marks profiles as persisted.
func (pl *ProfileLearner) MarkPersisted() {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.pendingPersistence = false
	pl.lastPersisted = time.Now()
}

// ShouldPersist returns true if persistence interval has elapsed.
func (pl *ProfileLearner) ShouldPersist() bool {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return pl.pendingPersistence &&
		time.Since(pl.lastPersisted) >= pl.config.PersistenceInterval
}

// GetAllProfiles returns copies of all profiles for persistence.
func (pl *ProfileLearner) GetAllProfiles() map[string]*AgentHandoffProfile {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	result := make(map[string]*AgentHandoffProfile, len(pl.profiles))
	for k, v := range pl.profiles {
		key := fmt.Sprintf("%s|%s", k.AgentID, k.ModelName)
		result[key] = v.Clone()
	}
	return result
}

// LoadProfiles loads profiles from persistence.
func (pl *ProfileLearner) LoadProfiles(profiles map[string]*AgentHandoffProfile) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	for keyStr, profile := range profiles {
		key, err := parseProfileKey(keyStr)
		if err == nil && profile != nil {
			pl.profiles[key] = profile.Clone()
		}
	}
}

// parseProfileKey parses a pipe-separated profile key string.
func parseProfileKey(s string) (profileKey, error) {
	var key profileKey
	parts := splitKey(s, 2)
	if len(parts) == 2 {
		key.AgentID = parts[0]
		key.ModelName = parts[1]
		return key, nil
	}
	return key, fmt.Errorf("invalid profile key: %s", s)
}

// =============================================================================
// Configuration
// =============================================================================

// GetConfig returns a copy of the current configuration.
func (pl *ProfileLearner) GetConfig() *ProfileLearnerConfig {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return pl.config.Clone()
}

// SetConfig updates the configuration.
func (pl *ProfileLearner) SetConfig(config *ProfileLearnerConfig) {
	if config == nil {
		return
	}
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.config = config.Clone()
}

// GetPriorHierarchy returns the prior hierarchy for external access.
func (pl *ProfileLearner) GetPriorHierarchy() *PriorHierarchy {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return pl.priorHierarchy
}

// =============================================================================
// Statistics
// =============================================================================

// ProfileLearnerStats contains statistics about the learner.
type ProfileLearnerStats struct {
	// ProfileCount is the number of profiles stored.
	ProfileCount int `json:"profile_count"`

	// TotalObservations is the total observations recorded.
	TotalObservations int64 `json:"total_observations"`

	// TotalHandoffOutcomes is the total handoff outcomes recorded.
	TotalHandoffOutcomes int64 `json:"total_handoff_outcomes"`

	// PendingPersistence indicates if there are unpersisted changes.
	PendingPersistence bool `json:"pending_persistence"`

	// LastPersisted is when profiles were last persisted.
	LastPersisted time.Time `json:"last_persisted"`

	// HierarchyStats contains prior hierarchy statistics.
	HierarchyStats PriorHierarchyStats `json:"hierarchy_stats"`
}

// Stats returns statistics about the learner.
func (pl *ProfileLearner) Stats() ProfileLearnerStats {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	return ProfileLearnerStats{
		ProfileCount:         len(pl.profiles),
		TotalObservations:    pl.observations,
		TotalHandoffOutcomes: pl.handoffOutcomes,
		PendingPersistence:   pl.pendingPersistence,
		LastPersisted:        pl.lastPersisted,
		HierarchyStats:       pl.priorHierarchy.GetStats(),
	}
}

// Clear removes all learned profiles.
func (pl *ProfileLearner) Clear() {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	pl.profiles = make(map[profileKey]*AgentHandoffProfile)
	pl.observations = 0
	pl.handoffOutcomes = 0
	pl.pendingPersistence = false
}

// =============================================================================
// JSON Serialization
// =============================================================================

// profileLearnerJSON is used for JSON marshaling/unmarshaling.
type profileLearnerJSON struct {
	Profiles            map[string]*AgentHandoffProfile `json:"profiles"`
	PriorHierarchy      *PriorHierarchy                 `json:"prior_hierarchy"`
	Config              *ProfileLearnerConfig           `json:"config"`
	LastPersisted       string                          `json:"last_persisted"`
	Observations        int64                           `json:"observations"`
	HandoffOutcomes     int64                           `json:"handoff_outcomes"`
	PendingPersistence  bool                            `json:"pending_persistence"`
}

// MarshalJSON implements json.Marshaler.
func (pl *ProfileLearner) MarshalJSON() ([]byte, error) {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	profilesJSON := make(map[string]*AgentHandoffProfile)
	for k, v := range pl.profiles {
		key := fmt.Sprintf("%s|%s", k.AgentID, k.ModelName)
		profilesJSON[key] = v
	}

	return json.Marshal(profileLearnerJSON{
		Profiles:           profilesJSON,
		PriorHierarchy:     pl.priorHierarchy,
		Config:             pl.config,
		LastPersisted:      pl.lastPersisted.Format(time.RFC3339Nano),
		Observations:       pl.observations,
		HandoffOutcomes:    pl.handoffOutcomes,
		PendingPersistence: pl.pendingPersistence,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (pl *ProfileLearner) UnmarshalJSON(data []byte) error {
	var temp profileLearnerJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	pl.mu.Lock()
	defer pl.mu.Unlock()

	// Parse profiles
	pl.profiles = make(map[profileKey]*AgentHandoffProfile)
	for keyStr, v := range temp.Profiles {
		key, err := parseProfileKey(keyStr)
		if err == nil && v != nil {
			pl.profiles[key] = v
		}
	}

	if temp.PriorHierarchy != nil {
		pl.priorHierarchy = temp.PriorHierarchy
	} else {
		pl.priorHierarchy = NewPriorHierarchy()
	}

	if temp.Config != nil {
		pl.config = temp.Config
	} else {
		pl.config = DefaultProfileLearnerConfig()
	}

	if temp.LastPersisted != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastPersisted); err == nil {
			pl.lastPersisted = t
		}
	}

	pl.observations = temp.Observations
	pl.handoffOutcomes = temp.HandoffOutcomes
	pl.pendingPersistence = temp.PendingPersistence

	return nil
}
