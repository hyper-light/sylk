package handoff

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// =============================================================================
// HO.3.1 AgentHandoffProfile - Learned Profile for Agent Type + Model Combination
// =============================================================================
//
// AgentHandoffProfile captures learned parameters for handoff decisions specific
// to an agent type and model combination. It uses Bayesian learning to adapt
// thresholds, context sizes, and quality curves based on observed behavior.
//
// The profile supports hierarchical blending through the Clone method,
// allowing instance-level profiles to blend with agent-model level priors.

// HandoffObservation captures a single observation of handoff behavior.
// These observations are used to update the learned parameters in the profile.
type HandoffObservation struct {
	// ContextSize is the size of the context at the time of observation,
	// measured in tokens or a similar unit.
	ContextSize int `json:"context_size"`

	// TurnNumber is the conversation turn number when the observation occurred.
	TurnNumber int `json:"turn_number"`

	// QualityScore is a measure of output quality, typically in [0,1].
	// Higher values indicate better quality responses.
	QualityScore float64 `json:"quality_score"`

	// WasSuccessful indicates whether the agent completed its task successfully.
	WasSuccessful bool `json:"was_successful"`

	// HandoffTriggered indicates whether a handoff was triggered at this point.
	HandoffTriggered bool `json:"handoff_triggered"`

	// Timestamp is when the observation was recorded.
	Timestamp time.Time `json:"timestamp"`
}

// NewHandoffObservation creates a new observation with the current timestamp.
func NewHandoffObservation(
	contextSize, turnNumber int,
	qualityScore float64,
	wasSuccessful, handoffTriggered bool,
) *HandoffObservation {
	return &HandoffObservation{
		ContextSize:      contextSize,
		TurnNumber:       turnNumber,
		QualityScore:     qualityScore,
		WasSuccessful:    wasSuccessful,
		HandoffTriggered: handoffTriggered,
		Timestamp:        time.Now(),
	}
}

// AgentHandoffProfile represents a learned profile for an agent type and model
// combination. It stores Bayesian distributions for key handoff parameters
// that are updated based on observed behavior.
type AgentHandoffProfile struct {
	// AgentType identifies the type of agent (e.g., "coder", "reviewer").
	AgentType string `json:"agent_type"`

	// ModelID identifies the LLM model being used (e.g., "claude-3-opus").
	ModelID string `json:"model_id"`

	// InstanceID uniquely identifies this specific instance for per-instance tracking.
	InstanceID string `json:"instance_id"`

	// OptimalHandoffThreshold is a Beta distribution for learning when to handoff.
	// Mean represents the optimal threshold for triggering handoff.
	// Beta(2, 2) prior gives a mean of 0.5 with moderate uncertainty.
	OptimalHandoffThreshold *LearnedWeight `json:"optimal_handoff_threshold"`

	// OptimalPreparedSize is a Gamma distribution for learning optimal context size.
	// Mean represents the optimal size of prepared context in tokens.
	// Gamma(alpha=2, beta=0.02) prior gives a mean of ~100 tokens.
	OptimalPreparedSize *LearnedContextSize `json:"optimal_prepared_size"`

	// QualityDegradationCurve tracks how quality degrades as context fills.
	// Higher values indicate faster degradation.
	// Beta(1, 1) prior gives a uniform distribution (no prior belief).
	QualityDegradationCurve *LearnedWeight `json:"quality_degradation_curve"`

	// PreserveRecent is a Gamma distribution for how much recent context to keep.
	// Mean represents the number of recent turns to always preserve.
	// Gamma(alpha=5, beta=0.5) prior gives a mean of ~10 turns.
	PreserveRecent *LearnedContextSize `json:"preserve_recent"`

	// ContextUsageWeight indicates the importance of context usage in decisions.
	// Higher values mean context usage matters more for handoff decisions.
	// Beta(2, 2) prior gives a mean of 0.5.
	ContextUsageWeight *LearnedWeight `json:"context_usage_weight"`

	// EffectiveSamples tracks the total number of observations incorporated
	// into this profile, accounting for exponential decay.
	EffectiveSamples float64 `json:"effective_samples"`

	// LastUpdated is the timestamp of the most recent update to the profile.
	LastUpdated time.Time `json:"last_updated"`

	// CreatedAt is the timestamp when this profile was created.
	CreatedAt time.Time `json:"created_at"`
}

// NewAgentHandoffProfile creates a new profile with the specified identifiers.
// The profile is initialized with weakly informative priors.
func NewAgentHandoffProfile(agentType, modelID, instanceID string) *AgentHandoffProfile {
	now := time.Now()
	profile := &AgentHandoffProfile{
		AgentType:        agentType,
		ModelID:          modelID,
		InstanceID:       instanceID,
		EffectiveSamples: 0.0,
		LastUpdated:      now,
		CreatedAt:        now,
	}
	profile.SetDefaultPriors()
	return profile
}

// SetDefaultPriors initializes all learned parameters with weakly informative priors.
// These priors represent minimal prior knowledge and allow the model to learn
// quickly from observed data.
//
// Default priors:
//   - OptimalHandoffThreshold: Beta(2, 2) -> mean 0.5
//   - OptimalPreparedSize: Gamma(alpha=2, beta=0.02) -> mean 100 tokens
//   - QualityDegradationCurve: Beta(1, 1) -> uniform
//   - PreserveRecent: Gamma(alpha=5, beta=0.5) -> mean 10 turns
//   - ContextUsageWeight: Beta(2, 2) -> mean 0.5
func (ahp *AgentHandoffProfile) SetDefaultPriors() {
	// Beta(2, 2) for optimal handoff threshold - centered at 0.5 with moderate variance
	ahp.OptimalHandoffThreshold = NewLearnedWeight(2.0, 2.0)

	// Gamma(2, 0.02) for optimal prepared size - mean of 100 tokens
	// alpha=2, beta=0.02 gives mean = 2/0.02 = 100
	ahp.OptimalPreparedSize = NewLearnedContextSize(2.0, 0.02)

	// Beta(1, 1) for quality degradation curve - uniform prior (no initial belief)
	ahp.QualityDegradationCurve = NewLearnedWeight(1.0, 1.0)

	// Gamma(5, 0.5) for preserve recent - mean of 10 turns
	// alpha=5, beta=0.5 gives mean = 5/0.5 = 10
	ahp.PreserveRecent = NewLearnedContextSize(5.0, 0.5)

	// Beta(2, 2) for context usage weight - centered at 0.5 with moderate variance
	ahp.ContextUsageWeight = NewLearnedWeight(2.0, 2.0)
}

// Confidence returns the overall confidence in this profile based on
// the effective number of samples observed. Returns a value in [0,1]
// where 1.0 indicates very high confidence.
//
// The confidence grows logarithmically with the number of samples,
// with diminishing returns after many observations.
func (ahp *AgentHandoffProfile) Confidence() float64 {
	if ahp.EffectiveSamples < 1.0 {
		return 0.0
	}

	// Logarithmic growth with diminishing returns
	// Reaches ~0.63 at 10 samples, ~0.86 at 20 samples, ~0.95 at 30 samples
	return 1.0 - math.Exp(-ahp.EffectiveSamples/10.0)
}

// Update incorporates a new observation into the profile, updating all
// learned parameters based on the observed behavior.
//
// The update logic:
//   - HandoffThreshold: Updated based on quality and handoff success
//   - PreparedSize: Updated based on context size when quality was high
//   - DegradationCurve: Updated based on quality vs context ratio
//   - PreserveRecent: Updated based on turn number when handoff triggered
//   - ContextUsageWeight: Updated based on whether context size mattered
func (ahp *AgentHandoffProfile) Update(observation *HandoffObservation) {
	if observation == nil {
		return
	}

	config := DefaultUpdateConfig()

	// Update handoff threshold based on whether handoff was appropriate
	// If handoff was triggered and successful, the threshold should move toward current value
	// If handoff was triggered and unsuccessful, move away from current value
	ahp.updateHandoffThreshold(observation, config)

	// Update optimal prepared size based on context at successful operations
	ahp.updatePreparedSize(observation, config)

	// Update quality degradation curve based on quality vs context relationship
	ahp.updateDegradationCurve(observation, config)

	// Update preserve recent based on turn number at handoff
	ahp.updatePreserveRecent(observation, config)

	// Update context usage weight based on whether context size correlated with outcome
	ahp.updateContextUsageWeight(observation, config)

	// Increment effective samples - each observation adds approximately 1 sample
	// but with decay to give more weight to recent observations
	ahp.EffectiveSamples = ahp.EffectiveSamples*(1.0-config.LearningRate) + 1.0
	ahp.LastUpdated = time.Now()
}

// updateHandoffThreshold adjusts the threshold based on observation outcomes.
func (ahp *AgentHandoffProfile) updateHandoffThreshold(obs *HandoffObservation, config *UpdateConfig) {
	if ahp.OptimalHandoffThreshold == nil {
		return
	}

	// Use quality score as the observation value
	// High quality suggests the current threshold is appropriate
	qualityObs := clamp(obs.QualityScore, 0.0, 1.0)

	// If handoff was triggered successfully, this suggests threshold was appropriate
	// If handoff was triggered unsuccessfully, threshold may need adjustment
	if obs.HandoffTriggered {
		if obs.WasSuccessful {
			// Reinforce current behavior
			ahp.OptimalHandoffThreshold.Update(qualityObs, config)
		} else {
			// Push away from current threshold
			ahp.OptimalHandoffThreshold.Update(1.0-qualityObs, config)
		}
	} else {
		// No handoff - if successful, current threshold is working
		if obs.WasSuccessful {
			ahp.OptimalHandoffThreshold.Update(qualityObs, config)
		}
	}
}

// updatePreparedSize adjusts optimal context size based on observations.
func (ahp *AgentHandoffProfile) updatePreparedSize(obs *HandoffObservation, config *UpdateConfig) {
	if ahp.OptimalPreparedSize == nil {
		return
	}

	// Only update when we have a successful observation with good quality
	if obs.WasSuccessful && obs.QualityScore > 0.5 {
		ahp.OptimalPreparedSize.Update(obs.ContextSize, config)
	}
}

// updateDegradationCurve tracks how quality relates to context size.
func (ahp *AgentHandoffProfile) updateDegradationCurve(obs *HandoffObservation, config *UpdateConfig) {
	if ahp.QualityDegradationCurve == nil {
		return
	}

	// Normalize context size to estimate position on degradation curve
	// Larger context tends to correlate with more degradation
	// Use quality score as indicator of where on curve we are
	degradationObs := 1.0 - obs.QualityScore
	ahp.QualityDegradationCurve.Update(degradationObs, config)
}

// updatePreserveRecent adjusts how much recent context to preserve.
func (ahp *AgentHandoffProfile) updatePreserveRecent(obs *HandoffObservation, config *UpdateConfig) {
	if ahp.PreserveRecent == nil {
		return
	}

	// When handoff is triggered successfully, the turn number gives us information
	// about how much recent context was needed
	if obs.HandoffTriggered && obs.WasSuccessful {
		ahp.PreserveRecent.Update(obs.TurnNumber, config)
	}
}

// updateContextUsageWeight tracks importance of context in decisions.
func (ahp *AgentHandoffProfile) updateContextUsageWeight(obs *HandoffObservation, config *UpdateConfig) {
	if ahp.ContextUsageWeight == nil {
		return
	}

	// If context size is large and quality is high, context usage matters
	// If context size varies without affecting quality, it matters less
	// Use a simple heuristic: high quality with large context = context matters
	contextImportance := 0.5
	if obs.ContextSize > 100 {
		if obs.QualityScore > 0.7 {
			contextImportance = 0.8
		} else if obs.QualityScore < 0.3 {
			contextImportance = 0.2
		}
	}
	ahp.ContextUsageWeight.Update(contextImportance, config)
}

// Clone creates a deep copy of the profile for hierarchical blending.
// The cloned profile can be modified independently without affecting the original.
func (ahp *AgentHandoffProfile) Clone() *AgentHandoffProfile {
	if ahp == nil {
		return nil
	}

	cloned := &AgentHandoffProfile{
		AgentType:        ahp.AgentType,
		ModelID:          ahp.ModelID,
		InstanceID:       ahp.InstanceID,
		EffectiveSamples: ahp.EffectiveSamples,
		LastUpdated:      ahp.LastUpdated,
		CreatedAt:        ahp.CreatedAt,
	}

	// Deep copy all learned parameters
	if ahp.OptimalHandoffThreshold != nil {
		cloned.OptimalHandoffThreshold = &LearnedWeight{
			Alpha:            ahp.OptimalHandoffThreshold.Alpha,
			Beta:             ahp.OptimalHandoffThreshold.Beta,
			EffectiveSamples: ahp.OptimalHandoffThreshold.EffectiveSamples,
			PriorAlpha:       ahp.OptimalHandoffThreshold.PriorAlpha,
			PriorBeta:        ahp.OptimalHandoffThreshold.PriorBeta,
		}
	}

	if ahp.OptimalPreparedSize != nil {
		cloned.OptimalPreparedSize = &LearnedContextSize{
			Alpha:            ahp.OptimalPreparedSize.Alpha,
			Beta:             ahp.OptimalPreparedSize.Beta,
			EffectiveSamples: ahp.OptimalPreparedSize.EffectiveSamples,
			PriorAlpha:       ahp.OptimalPreparedSize.PriorAlpha,
			PriorBeta:        ahp.OptimalPreparedSize.PriorBeta,
		}
	}

	if ahp.QualityDegradationCurve != nil {
		cloned.QualityDegradationCurve = &LearnedWeight{
			Alpha:            ahp.QualityDegradationCurve.Alpha,
			Beta:             ahp.QualityDegradationCurve.Beta,
			EffectiveSamples: ahp.QualityDegradationCurve.EffectiveSamples,
			PriorAlpha:       ahp.QualityDegradationCurve.PriorAlpha,
			PriorBeta:        ahp.QualityDegradationCurve.PriorBeta,
		}
	}

	if ahp.PreserveRecent != nil {
		cloned.PreserveRecent = &LearnedContextSize{
			Alpha:            ahp.PreserveRecent.Alpha,
			Beta:             ahp.PreserveRecent.Beta,
			EffectiveSamples: ahp.PreserveRecent.EffectiveSamples,
			PriorAlpha:       ahp.PreserveRecent.PriorAlpha,
			PriorBeta:        ahp.PreserveRecent.PriorBeta,
		}
	}

	if ahp.ContextUsageWeight != nil {
		cloned.ContextUsageWeight = &LearnedWeight{
			Alpha:            ahp.ContextUsageWeight.Alpha,
			Beta:             ahp.ContextUsageWeight.Beta,
			EffectiveSamples: ahp.ContextUsageWeight.EffectiveSamples,
			PriorAlpha:       ahp.ContextUsageWeight.PriorAlpha,
			PriorBeta:        ahp.ContextUsageWeight.PriorBeta,
		}
	}

	return cloned
}

// =============================================================================
// JSON Serialization
// =============================================================================

// agentProfileJSON is used for JSON marshaling/unmarshaling.
type agentProfileJSON struct {
	AgentType               string              `json:"agent_type"`
	ModelID                 string              `json:"model_id"`
	InstanceID              string              `json:"instance_id"`
	OptimalHandoffThreshold *LearnedWeight      `json:"optimal_handoff_threshold"`
	OptimalPreparedSize     *LearnedContextSize `json:"optimal_prepared_size"`
	QualityDegradationCurve *LearnedWeight      `json:"quality_degradation_curve"`
	PreserveRecent          *LearnedContextSize `json:"preserve_recent"`
	ContextUsageWeight      *LearnedWeight      `json:"context_usage_weight"`
	EffectiveSamples        float64             `json:"effective_samples"`
	LastUpdated             string              `json:"last_updated"`
	CreatedAt               string              `json:"created_at"`
}

// MarshalJSON implements json.Marshaler.
func (ahp *AgentHandoffProfile) MarshalJSON() ([]byte, error) {
	return json.Marshal(agentProfileJSON{
		AgentType:               ahp.AgentType,
		ModelID:                 ahp.ModelID,
		InstanceID:              ahp.InstanceID,
		OptimalHandoffThreshold: ahp.OptimalHandoffThreshold,
		OptimalPreparedSize:     ahp.OptimalPreparedSize,
		QualityDegradationCurve: ahp.QualityDegradationCurve,
		PreserveRecent:          ahp.PreserveRecent,
		ContextUsageWeight:      ahp.ContextUsageWeight,
		EffectiveSamples:        ahp.EffectiveSamples,
		LastUpdated:             ahp.LastUpdated.Format(time.RFC3339Nano),
		CreatedAt:               ahp.CreatedAt.Format(time.RFC3339Nano),
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (ahp *AgentHandoffProfile) UnmarshalJSON(data []byte) error {
	var temp agentProfileJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return fmt.Errorf("failed to unmarshal AgentHandoffProfile: %w", err)
	}

	ahp.AgentType = temp.AgentType
	ahp.ModelID = temp.ModelID
	ahp.InstanceID = temp.InstanceID
	ahp.OptimalHandoffThreshold = temp.OptimalHandoffThreshold
	ahp.OptimalPreparedSize = temp.OptimalPreparedSize
	ahp.QualityDegradationCurve = temp.QualityDegradationCurve
	ahp.PreserveRecent = temp.PreserveRecent
	ahp.ContextUsageWeight = temp.ContextUsageWeight
	ahp.EffectiveSamples = temp.EffectiveSamples

	if temp.LastUpdated != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.LastUpdated); err == nil {
			ahp.LastUpdated = t
		}
	}

	if temp.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, temp.CreatedAt); err == nil {
			ahp.CreatedAt = t
		}
	}

	return nil
}

// =============================================================================
// Utility Methods
// =============================================================================

// GetHandoffThresholdMean returns the current mean of the optimal handoff threshold.
// Returns 0.5 if the threshold is not initialized.
func (ahp *AgentHandoffProfile) GetHandoffThresholdMean() float64 {
	if ahp.OptimalHandoffThreshold == nil {
		return 0.5
	}
	return ahp.OptimalHandoffThreshold.Mean()
}

// GetPreparedSizeMean returns the current mean of the optimal prepared size.
// Returns 100 if not initialized.
func (ahp *AgentHandoffProfile) GetPreparedSizeMean() int {
	if ahp.OptimalPreparedSize == nil {
		return 100
	}
	return ahp.OptimalPreparedSize.Mean()
}

// GetDegradationCurveMean returns the current mean of the quality degradation curve.
// Returns 0.5 if not initialized.
func (ahp *AgentHandoffProfile) GetDegradationCurveMean() float64 {
	if ahp.QualityDegradationCurve == nil {
		return 0.5
	}
	return ahp.QualityDegradationCurve.Mean()
}

// GetPreserveRecentMean returns the current mean of how many recent turns to preserve.
// Returns 10 if not initialized.
func (ahp *AgentHandoffProfile) GetPreserveRecentMean() int {
	if ahp.PreserveRecent == nil {
		return 10
	}
	return ahp.PreserveRecent.Mean()
}

// GetContextUsageWeightMean returns the current mean of the context usage weight.
// Returns 0.5 if not initialized.
func (ahp *AgentHandoffProfile) GetContextUsageWeightMean() float64 {
	if ahp.ContextUsageWeight == nil {
		return 0.5
	}
	return ahp.ContextUsageWeight.Mean()
}

// String returns a human-readable representation of the profile.
func (ahp *AgentHandoffProfile) String() string {
	return fmt.Sprintf(
		"AgentHandoffProfile{Agent: %s, Model: %s, Instance: %s, Confidence: %.2f, Samples: %.1f}",
		ahp.AgentType,
		ahp.ModelID,
		ahp.InstanceID,
		ahp.Confidence(),
		ahp.EffectiveSamples,
	)
}
