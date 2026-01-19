package handoff

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"
)

// =============================================================================
// HO.7.1 Global Priors - Default Priors Across All Agents/Models
// =============================================================================
//
// GlobalPriors represents the default prior parameters that apply across all
// agents and models. These are the most general priors and serve as the
// fallback when no more specific priors are available.

// GlobalPriors contains default prior parameters for handoff learning.
// These priors are used when no agent-specific or model-specific data exists.
type GlobalPriors struct {
	// ContextThreshold is the default threshold for context-based handoff decisions.
	// Uses Beta distribution: mean = Alpha/(Alpha+Beta).
	// Default: Beta(5, 5) -> mean 0.5 with moderate confidence.
	ContextThreshold *LearnedWeight `json:"context_threshold"`

	// QualityThreshold is the default minimum quality score for handoff decisions.
	// Uses Beta distribution. Default: Beta(7, 3) -> mean 0.7.
	QualityThreshold *LearnedWeight `json:"quality_threshold"`

	// OptimalContextSize is the default optimal context size in tokens.
	// Uses Gamma distribution. Default: Gamma(100, 1) -> mean 100 tokens.
	OptimalContextSize *LearnedContextSize `json:"optimal_context_size"`

	// PreserveRecentTurns is the default number of recent turns to preserve.
	// Uses Gamma distribution. Default: Gamma(10, 1) -> mean 10 turns.
	PreserveRecentTurns *LearnedContextSize `json:"preserve_recent_turns"`

	// HandoffSuccessRate is the learned overall handoff success rate.
	// Uses Beta distribution. Default: Beta(8, 2) -> mean 0.8.
	HandoffSuccessRate *LearnedWeight `json:"handoff_success_rate"`

	// ContextDecayRate is the rate at which context relevance decays.
	// Uses Beta distribution. Default: Beta(3, 7) -> mean 0.3.
	ContextDecayRate *LearnedWeight `json:"context_decay_rate"`

	// LastUpdated is the timestamp of the most recent update.
	LastUpdated time.Time `json:"last_updated"`

	// CreatedAt is the timestamp when these priors were created.
	CreatedAt time.Time `json:"created_at"`
}

// DefaultGlobalPriors returns a new GlobalPriors with sensible default values.
// These defaults represent minimal prior knowledge and allow the model to
// learn quickly from observed data.
func DefaultGlobalPriors() *GlobalPriors {
	now := time.Now()
	return &GlobalPriors{
		// Context threshold: mean 0.5, moderate variance
		ContextThreshold: NewLearnedWeight(5.0, 5.0),

		// Quality threshold: mean 0.7, moderate confidence
		QualityThreshold: NewLearnedWeight(7.0, 3.0),

		// Optimal context size: mean 100 tokens
		OptimalContextSize: NewLearnedContextSize(100.0, 1.0),

		// Preserve recent turns: mean 10 turns
		PreserveRecentTurns: NewLearnedContextSize(10.0, 1.0),

		// Handoff success rate: mean 0.8 (optimistic prior)
		HandoffSuccessRate: NewLearnedWeight(8.0, 2.0),

		// Context decay rate: mean 0.3 (moderate decay)
		ContextDecayRate: NewLearnedWeight(3.0, 7.0),

		LastUpdated: now,
		CreatedAt:   now,
	}
}

// Clone creates a deep copy of the GlobalPriors.
func (gp *GlobalPriors) Clone() *GlobalPriors {
	if gp == nil {
		return nil
	}

	return &GlobalPriors{
		ContextThreshold:    cloneLearnedWeight(gp.ContextThreshold),
		QualityThreshold:    cloneLearnedWeight(gp.QualityThreshold),
		OptimalContextSize:  cloneLearnedContextSize(gp.OptimalContextSize),
		PreserveRecentTurns: cloneLearnedContextSize(gp.PreserveRecentTurns),
		HandoffSuccessRate:  cloneLearnedWeight(gp.HandoffSuccessRate),
		ContextDecayRate:    cloneLearnedWeight(gp.ContextDecayRate),
		LastUpdated:         gp.LastUpdated,
		CreatedAt:           gp.CreatedAt,
	}
}

// Confidence returns the overall confidence in these global priors.
// Computed as the minimum confidence across all parameters.
func (gp *GlobalPriors) Confidence() float64 {
	if gp == nil {
		return 0.0
	}

	minConf := 1.0
	if gp.ContextThreshold != nil {
		minConf = math.Min(minConf, gp.ContextThreshold.Confidence())
	}
	if gp.QualityThreshold != nil {
		minConf = math.Min(minConf, gp.QualityThreshold.Confidence())
	}
	if gp.OptimalContextSize != nil {
		minConf = math.Min(minConf, gp.OptimalContextSize.Confidence())
	}
	if gp.PreserveRecentTurns != nil {
		minConf = math.Min(minConf, gp.PreserveRecentTurns.Confidence())
	}
	if gp.HandoffSuccessRate != nil {
		minConf = math.Min(minConf, gp.HandoffSuccessRate.Confidence())
	}
	if gp.ContextDecayRate != nil {
		minConf = math.Min(minConf, gp.ContextDecayRate.Confidence())
	}

	return minConf
}

// =============================================================================
// HO.7.2 Model Priors - Model-Specific Priors
// =============================================================================
//
// ModelPriors represents learned parameters specific to a particular model
// (e.g., claude-3-opus, gpt-4). These inherit from GlobalPriors and override
// parameters where model-specific data exists.

// ModelPriors contains learned parameters specific to a model.
// It inherits from GlobalPriors but can override with model-specific data.
type ModelPriors struct {
	// ModelName is the identifier for this model (e.g., "claude-3-opus").
	ModelName string `json:"model_name"`

	// ContextThreshold overrides the global context threshold for this model.
	ContextThreshold *LearnedWeight `json:"context_threshold"`

	// QualityThreshold overrides the global quality threshold for this model.
	QualityThreshold *LearnedWeight `json:"quality_threshold"`

	// OptimalContextSize overrides the global optimal context size for this model.
	OptimalContextSize *LearnedContextSize `json:"optimal_context_size"`

	// PreserveRecentTurns overrides the global preserve recent turns for this model.
	PreserveRecentTurns *LearnedContextSize `json:"preserve_recent_turns"`

	// HandoffSuccessRate is the learned handoff success rate for this model.
	HandoffSuccessRate *LearnedWeight `json:"handoff_success_rate"`

	// ContextDecayRate overrides the global context decay rate for this model.
	ContextDecayRate *LearnedWeight `json:"context_decay_rate"`

	// EffectiveSamples tracks the total number of observations for this model.
	EffectiveSamples float64 `json:"effective_samples"`

	// LastUpdated is the timestamp of the most recent update.
	LastUpdated time.Time `json:"last_updated"`

	// CreatedAt is the timestamp when these priors were created.
	CreatedAt time.Time `json:"created_at"`
}

// NewModelPriors creates a new ModelPriors for the given model,
// initialized from the global priors.
func NewModelPriors(modelName string, global *GlobalPriors) *ModelPriors {
	now := time.Now()
	mp := &ModelPriors{
		ModelName:        modelName,
		EffectiveSamples: 0.0,
		LastUpdated:      now,
		CreatedAt:        now,
	}

	// Initialize from global priors if provided
	if global != nil {
		mp.ContextThreshold = cloneLearnedWeight(global.ContextThreshold)
		mp.QualityThreshold = cloneLearnedWeight(global.QualityThreshold)
		mp.OptimalContextSize = cloneLearnedContextSize(global.OptimalContextSize)
		mp.PreserveRecentTurns = cloneLearnedContextSize(global.PreserveRecentTurns)
		mp.HandoffSuccessRate = cloneLearnedWeight(global.HandoffSuccessRate)
		mp.ContextDecayRate = cloneLearnedWeight(global.ContextDecayRate)
	} else {
		// Use defaults if no global priors provided
		defaults := DefaultGlobalPriors()
		mp.ContextThreshold = cloneLearnedWeight(defaults.ContextThreshold)
		mp.QualityThreshold = cloneLearnedWeight(defaults.QualityThreshold)
		mp.OptimalContextSize = cloneLearnedContextSize(defaults.OptimalContextSize)
		mp.PreserveRecentTurns = cloneLearnedContextSize(defaults.PreserveRecentTurns)
		mp.HandoffSuccessRate = cloneLearnedWeight(defaults.HandoffSuccessRate)
		mp.ContextDecayRate = cloneLearnedWeight(defaults.ContextDecayRate)
	}

	return mp
}

// PriorObservation captures a single observation for updating priors.
type PriorObservation struct {
	// ContextSize is the context size at the time of observation.
	ContextSize int `json:"context_size"`

	// QualityScore is the observed quality score in [0,1].
	QualityScore float64 `json:"quality_score"`

	// WasSuccessful indicates whether the handoff was successful.
	WasSuccessful bool `json:"was_successful"`

	// TurnNumber is the conversation turn number.
	TurnNumber int `json:"turn_number"`

	// Timestamp is when the observation was recorded.
	Timestamp time.Time `json:"timestamp"`
}

// NewPriorObservation creates a new observation with the current timestamp.
func NewPriorObservation(contextSize int, qualityScore float64, wasSuccessful bool, turnNumber int) *PriorObservation {
	return &PriorObservation{
		ContextSize:   contextSize,
		QualityScore:  qualityScore,
		WasSuccessful: wasSuccessful,
		TurnNumber:    turnNumber,
		Timestamp:     time.Now(),
	}
}

// UpdateFromObservation updates the model priors based on an observation.
// This method learns model-specific patterns from observed handoff behavior.
func (mp *ModelPriors) UpdateFromObservation(obs *PriorObservation, config *UpdateConfig) {
	if obs == nil {
		return
	}
	if config == nil {
		config = DefaultUpdateConfig()
	}

	// Update context threshold based on quality at current context level
	if mp.ContextThreshold != nil {
		// Higher quality suggests threshold is appropriate
		mp.ContextThreshold.Update(obs.QualityScore, config)
	}

	// Update quality threshold based on success
	if mp.QualityThreshold != nil {
		if obs.WasSuccessful {
			// Successful handoff - reinforce current quality threshold
			mp.QualityThreshold.Update(obs.QualityScore, config)
		} else {
			// Failed handoff - quality threshold may need adjustment
			mp.QualityThreshold.Update(1.0-obs.QualityScore, config)
		}
	}

	// Update optimal context size when quality is good
	if mp.OptimalContextSize != nil && obs.QualityScore > 0.5 {
		mp.OptimalContextSize.Update(obs.ContextSize, config)
	}

	// Update preserve recent turns based on turn number at observation
	if mp.PreserveRecentTurns != nil && obs.WasSuccessful {
		mp.PreserveRecentTurns.Update(obs.TurnNumber, config)
	}

	// Update handoff success rate
	if mp.HandoffSuccessRate != nil {
		if obs.WasSuccessful {
			mp.HandoffSuccessRate.Update(1.0, config)
		} else {
			mp.HandoffSuccessRate.Update(0.0, config)
		}
	}

	// Update context decay rate based on quality degradation
	if mp.ContextDecayRate != nil {
		// Lower quality at higher context suggests faster decay
		decayObs := 1.0 - obs.QualityScore
		mp.ContextDecayRate.Update(decayObs, config)
	}

	// Update effective samples and timestamp
	mp.EffectiveSamples = mp.EffectiveSamples*(1.0-config.LearningRate) + 1.0
	mp.LastUpdated = time.Now()
}

// Confidence returns the overall confidence in these model priors.
func (mp *ModelPriors) Confidence() float64 {
	if mp == nil || mp.EffectiveSamples < 1.0 {
		return 0.0
	}
	return 1.0 - math.Exp(-mp.EffectiveSamples/10.0)
}

// Clone creates a deep copy of the ModelPriors.
func (mp *ModelPriors) Clone() *ModelPriors {
	if mp == nil {
		return nil
	}

	return &ModelPriors{
		ModelName:           mp.ModelName,
		ContextThreshold:    cloneLearnedWeight(mp.ContextThreshold),
		QualityThreshold:    cloneLearnedWeight(mp.QualityThreshold),
		OptimalContextSize:  cloneLearnedContextSize(mp.OptimalContextSize),
		PreserveRecentTurns: cloneLearnedContextSize(mp.PreserveRecentTurns),
		HandoffSuccessRate:  cloneLearnedWeight(mp.HandoffSuccessRate),
		ContextDecayRate:    cloneLearnedWeight(mp.ContextDecayRate),
		EffectiveSamples:    mp.EffectiveSamples,
		LastUpdated:         mp.LastUpdated,
		CreatedAt:           mp.CreatedAt,
	}
}

// =============================================================================
// HO.7.3 AgentModel Priors - Agent+Model Specific Priors
// =============================================================================
//
// AgentModelPriors represents the most specific level of priors, combining
// both agent type and model. These inherit from ModelPriors and override
// with instance-specific data.

// AgentModelPriors contains learned parameters for a specific agent+model combination.
// This is the most specific level of the prior hierarchy.
type AgentModelPriors struct {
	// AgentID is the identifier for the agent type (e.g., "coder", "reviewer").
	AgentID string `json:"agent_id"`

	// ModelName is the identifier for the model (e.g., "claude-3-opus").
	ModelName string `json:"model_name"`

	// ContextThreshold is the learned context threshold for this agent+model.
	ContextThreshold *LearnedWeight `json:"context_threshold"`

	// QualityThreshold is the learned quality threshold for this agent+model.
	QualityThreshold *LearnedWeight `json:"quality_threshold"`

	// OptimalContextSize is the learned optimal context size for this agent+model.
	OptimalContextSize *LearnedContextSize `json:"optimal_context_size"`

	// PreserveRecentTurns is the learned preserve recent turns for this agent+model.
	PreserveRecentTurns *LearnedContextSize `json:"preserve_recent_turns"`

	// HandoffSuccessRate is the learned handoff success rate for this agent+model.
	HandoffSuccessRate *LearnedWeight `json:"handoff_success_rate"`

	// ContextDecayRate is the learned context decay rate for this agent+model.
	ContextDecayRate *LearnedWeight `json:"context_decay_rate"`

	// EffectiveSamples tracks the total number of observations.
	EffectiveSamples float64 `json:"effective_samples"`

	// LastUpdated is the timestamp of the most recent update.
	LastUpdated time.Time `json:"last_updated"`

	// CreatedAt is the timestamp when these priors were created.
	CreatedAt time.Time `json:"created_at"`
}

// NewAgentModelPriors creates a new AgentModelPriors for the given agent+model,
// initialized from the model priors.
func NewAgentModelPriors(agentID, modelName string, modelPriors *ModelPriors) *AgentModelPriors {
	now := time.Now()
	amp := &AgentModelPriors{
		AgentID:          agentID,
		ModelName:        modelName,
		EffectiveSamples: 0.0,
		LastUpdated:      now,
		CreatedAt:        now,
	}

	// Initialize from model priors if provided
	if modelPriors != nil {
		amp.ContextThreshold = cloneLearnedWeight(modelPriors.ContextThreshold)
		amp.QualityThreshold = cloneLearnedWeight(modelPriors.QualityThreshold)
		amp.OptimalContextSize = cloneLearnedContextSize(modelPriors.OptimalContextSize)
		amp.PreserveRecentTurns = cloneLearnedContextSize(modelPriors.PreserveRecentTurns)
		amp.HandoffSuccessRate = cloneLearnedWeight(modelPriors.HandoffSuccessRate)
		amp.ContextDecayRate = cloneLearnedWeight(modelPriors.ContextDecayRate)
	} else {
		// Use defaults if no model priors provided
		defaults := DefaultGlobalPriors()
		amp.ContextThreshold = cloneLearnedWeight(defaults.ContextThreshold)
		amp.QualityThreshold = cloneLearnedWeight(defaults.QualityThreshold)
		amp.OptimalContextSize = cloneLearnedContextSize(defaults.OptimalContextSize)
		amp.PreserveRecentTurns = cloneLearnedContextSize(defaults.PreserveRecentTurns)
		amp.HandoffSuccessRate = cloneLearnedWeight(defaults.HandoffSuccessRate)
		amp.ContextDecayRate = cloneLearnedWeight(defaults.ContextDecayRate)
	}

	return amp
}

// UpdateFromObservation updates the agent+model priors based on an observation.
func (amp *AgentModelPriors) UpdateFromObservation(obs *PriorObservation, config *UpdateConfig) {
	if obs == nil {
		return
	}
	if config == nil {
		config = DefaultUpdateConfig()
	}

	// Update context threshold
	if amp.ContextThreshold != nil {
		amp.ContextThreshold.Update(obs.QualityScore, config)
	}

	// Update quality threshold
	if amp.QualityThreshold != nil {
		if obs.WasSuccessful {
			amp.QualityThreshold.Update(obs.QualityScore, config)
		} else {
			amp.QualityThreshold.Update(1.0-obs.QualityScore, config)
		}
	}

	// Update optimal context size
	if amp.OptimalContextSize != nil && obs.QualityScore > 0.5 {
		amp.OptimalContextSize.Update(obs.ContextSize, config)
	}

	// Update preserve recent turns
	if amp.PreserveRecentTurns != nil && obs.WasSuccessful {
		amp.PreserveRecentTurns.Update(obs.TurnNumber, config)
	}

	// Update handoff success rate
	if amp.HandoffSuccessRate != nil {
		if obs.WasSuccessful {
			amp.HandoffSuccessRate.Update(1.0, config)
		} else {
			amp.HandoffSuccessRate.Update(0.0, config)
		}
	}

	// Update context decay rate
	if amp.ContextDecayRate != nil {
		decayObs := 1.0 - obs.QualityScore
		amp.ContextDecayRate.Update(decayObs, config)
	}

	// Update effective samples and timestamp
	amp.EffectiveSamples = amp.EffectiveSamples*(1.0-config.LearningRate) + 1.0
	amp.LastUpdated = time.Now()
}

// Confidence returns the overall confidence in these agent+model priors.
func (amp *AgentModelPriors) Confidence() float64 {
	if amp == nil || amp.EffectiveSamples < 1.0 {
		return 0.0
	}
	return 1.0 - math.Exp(-amp.EffectiveSamples/10.0)
}

// BlendWithParent blends this agent+model prior with a parent prior
// (typically ModelPriors) using the given blend weight.
//
// The blending formula is Bayesian:
//   effective = instance * confidence + parent * (1 - confidence)
//
// Where confidence is the confidence in the instance-level data.
// Higher confidence means more weight on the instance data.
func (amp *AgentModelPriors) BlendWithParent(parent *ModelPriors, blendWeight float64) *AgentModelPriors {
	if amp == nil {
		if parent == nil {
			return nil
		}
		// No instance data, return parent values
		return &AgentModelPriors{
			AgentID:             "",
			ModelName:           parent.ModelName,
			ContextThreshold:    cloneLearnedWeight(parent.ContextThreshold),
			QualityThreshold:    cloneLearnedWeight(parent.QualityThreshold),
			OptimalContextSize:  cloneLearnedContextSize(parent.OptimalContextSize),
			PreserveRecentTurns: cloneLearnedContextSize(parent.PreserveRecentTurns),
			HandoffSuccessRate:  cloneLearnedWeight(parent.HandoffSuccessRate),
			ContextDecayRate:    cloneLearnedWeight(parent.ContextDecayRate),
			EffectiveSamples:    parent.EffectiveSamples,
			LastUpdated:         parent.LastUpdated,
			CreatedAt:           parent.CreatedAt,
		}
	}

	if parent == nil {
		// No parent data, return instance values
		return amp.Clone()
	}

	// Clamp blend weight to [0, 1]
	blendWeight = clamp(blendWeight, 0.0, 1.0)

	// Calculate effective weight based on instance confidence
	instanceConf := amp.Confidence()
	effectiveInstanceWeight := blendWeight * instanceConf
	parentWeight := 1.0 - effectiveInstanceWeight

	result := &AgentModelPriors{
		AgentID:          amp.AgentID,
		ModelName:        amp.ModelName,
		EffectiveSamples: amp.EffectiveSamples,
		LastUpdated:      amp.LastUpdated,
		CreatedAt:        amp.CreatedAt,
	}

	// Blend each parameter
	result.ContextThreshold = blendLearnedWeights(
		amp.ContextThreshold, parent.ContextThreshold,
		effectiveInstanceWeight, parentWeight)

	result.QualityThreshold = blendLearnedWeights(
		amp.QualityThreshold, parent.QualityThreshold,
		effectiveInstanceWeight, parentWeight)

	result.OptimalContextSize = blendLearnedContextSizes(
		amp.OptimalContextSize, parent.OptimalContextSize,
		effectiveInstanceWeight, parentWeight)

	result.PreserveRecentTurns = blendLearnedContextSizes(
		amp.PreserveRecentTurns, parent.PreserveRecentTurns,
		effectiveInstanceWeight, parentWeight)

	result.HandoffSuccessRate = blendLearnedWeights(
		amp.HandoffSuccessRate, parent.HandoffSuccessRate,
		effectiveInstanceWeight, parentWeight)

	result.ContextDecayRate = blendLearnedWeights(
		amp.ContextDecayRate, parent.ContextDecayRate,
		effectiveInstanceWeight, parentWeight)

	return result
}

// Clone creates a deep copy of the AgentModelPriors.
func (amp *AgentModelPriors) Clone() *AgentModelPriors {
	if amp == nil {
		return nil
	}

	return &AgentModelPriors{
		AgentID:             amp.AgentID,
		ModelName:           amp.ModelName,
		ContextThreshold:    cloneLearnedWeight(amp.ContextThreshold),
		QualityThreshold:    cloneLearnedWeight(amp.QualityThreshold),
		OptimalContextSize:  cloneLearnedContextSize(amp.OptimalContextSize),
		PreserveRecentTurns: cloneLearnedContextSize(amp.PreserveRecentTurns),
		HandoffSuccessRate:  cloneLearnedWeight(amp.HandoffSuccessRate),
		ContextDecayRate:    cloneLearnedWeight(amp.ContextDecayRate),
		EffectiveSamples:    amp.EffectiveSamples,
		LastUpdated:         amp.LastUpdated,
		CreatedAt:           amp.CreatedAt,
	}
}

// =============================================================================
// PriorHierarchy - Managing All Three Levels
// =============================================================================

// agentModelKey combines agent ID and model name for map lookups.
type priorAgentModelKey struct {
	AgentID   string
	ModelName string
}

// PriorHierarchy manages the three-level prior hierarchy:
// Global -> Model -> AgentModel
//
// It provides thread-safe access and Bayesian blending across levels.
type PriorHierarchy struct {
	mu sync.RWMutex

	// globalPriors contains the default priors for all agents/models.
	globalPriors *GlobalPriors

	// modelPriors maps model name to model-specific priors.
	modelPriors map[string]*ModelPriors

	// agentModelPriors maps (agentID, modelName) to agent+model specific priors.
	agentModelPriors map[priorAgentModelKey]*AgentModelPriors

	// updateConfig controls how observations update the priors.
	updateConfig *UpdateConfig
}

// NewPriorHierarchy creates a new PriorHierarchy with default global priors.
func NewPriorHierarchy() *PriorHierarchy {
	return &PriorHierarchy{
		globalPriors:     DefaultGlobalPriors(),
		modelPriors:      make(map[string]*ModelPriors),
		agentModelPriors: make(map[priorAgentModelKey]*AgentModelPriors),
		updateConfig:     DefaultUpdateConfig(),
	}
}

// NewPriorHierarchyWithConfig creates a new PriorHierarchy with custom config.
func NewPriorHierarchyWithConfig(config *UpdateConfig) *PriorHierarchy {
	if config == nil {
		config = DefaultUpdateConfig()
	}
	return &PriorHierarchy{
		globalPriors:     DefaultGlobalPriors(),
		modelPriors:      make(map[string]*ModelPriors),
		agentModelPriors: make(map[priorAgentModelKey]*AgentModelPriors),
		updateConfig:     config,
	}
}

// GetGlobalPriors returns a copy of the global priors.
func (ph *PriorHierarchy) GetGlobalPriors() *GlobalPriors {
	ph.mu.RLock()
	defer ph.mu.RUnlock()
	return ph.globalPriors.Clone()
}

// SetGlobalPriors sets the global priors.
func (ph *PriorHierarchy) SetGlobalPriors(priors *GlobalPriors) {
	if priors == nil {
		return
	}
	ph.mu.Lock()
	defer ph.mu.Unlock()
	ph.globalPriors = priors.Clone()
}

// GetModelPriors returns the priors for a specific model.
// If no model-specific priors exist, returns nil.
func (ph *PriorHierarchy) GetModelPriors(modelName string) *ModelPriors {
	ph.mu.RLock()
	defer ph.mu.RUnlock()

	if mp, ok := ph.modelPriors[modelName]; ok {
		return mp.Clone()
	}
	return nil
}

// GetOrCreateModelPriors returns the priors for a model, creating them if needed.
func (ph *PriorHierarchy) GetOrCreateModelPriors(modelName string) *ModelPriors {
	ph.mu.Lock()
	defer ph.mu.Unlock()

	if mp, ok := ph.modelPriors[modelName]; ok {
		return mp.Clone()
	}

	// Create new model priors from global
	mp := NewModelPriors(modelName, ph.globalPriors)
	ph.modelPriors[modelName] = mp
	return mp.Clone()
}

// SetModelPriors sets the priors for a specific model.
func (ph *PriorHierarchy) SetModelPriors(modelName string, priors *ModelPriors) {
	if priors == nil {
		return
	}
	ph.mu.Lock()
	defer ph.mu.Unlock()
	ph.modelPriors[modelName] = priors.Clone()
}

// GetAgentModelPriors returns the priors for a specific agent+model combination.
// If no specific priors exist, returns nil.
func (ph *PriorHierarchy) GetAgentModelPriors(agentID, modelName string) *AgentModelPriors {
	ph.mu.RLock()
	defer ph.mu.RUnlock()

	key := priorAgentModelKey{AgentID: agentID, ModelName: modelName}
	if amp, ok := ph.agentModelPriors[key]; ok {
		return amp.Clone()
	}
	return nil
}

// GetOrCreateAgentModelPriors returns the priors for an agent+model, creating them if needed.
func (ph *PriorHierarchy) GetOrCreateAgentModelPriors(agentID, modelName string) *AgentModelPriors {
	ph.mu.Lock()
	defer ph.mu.Unlock()

	key := priorAgentModelKey{AgentID: agentID, ModelName: modelName}
	if amp, ok := ph.agentModelPriors[key]; ok {
		return amp.Clone()
	}

	// Get or create model priors first
	var modelPriors *ModelPriors
	if mp, ok := ph.modelPriors[modelName]; ok {
		modelPriors = mp
	} else {
		modelPriors = NewModelPriors(modelName, ph.globalPriors)
		ph.modelPriors[modelName] = modelPriors
	}

	// Create new agent+model priors from model priors
	amp := NewAgentModelPriors(agentID, modelName, modelPriors)
	ph.agentModelPriors[key] = amp
	return amp.Clone()
}

// SetAgentModelPriors sets the priors for a specific agent+model combination.
func (ph *PriorHierarchy) SetAgentModelPriors(agentID, modelName string, priors *AgentModelPriors) {
	if priors == nil {
		return
	}
	ph.mu.Lock()
	defer ph.mu.Unlock()
	key := priorAgentModelKey{AgentID: agentID, ModelName: modelName}
	ph.agentModelPriors[key] = priors.Clone()
}

// EffectivePrior represents the result of hierarchical prior blending.
type EffectivePrior struct {
	// ContextThreshold is the blended context threshold.
	ContextThreshold float64 `json:"context_threshold"`

	// QualityThreshold is the blended quality threshold.
	QualityThreshold float64 `json:"quality_threshold"`

	// OptimalContextSize is the blended optimal context size.
	OptimalContextSize int `json:"optimal_context_size"`

	// PreserveRecentTurns is the blended preserve recent turns.
	PreserveRecentTurns int `json:"preserve_recent_turns"`

	// HandoffSuccessRate is the blended handoff success rate.
	HandoffSuccessRate float64 `json:"handoff_success_rate"`

	// ContextDecayRate is the blended context decay rate.
	ContextDecayRate float64 `json:"context_decay_rate"`

	// GlobalWeight is the contribution from global priors.
	GlobalWeight float64 `json:"global_weight"`

	// ModelWeight is the contribution from model priors.
	ModelWeight float64 `json:"model_weight"`

	// AgentModelWeight is the contribution from agent+model priors.
	AgentModelWeight float64 `json:"agent_model_weight"`

	// EffectiveConfidence is the overall confidence in the blended prior.
	EffectiveConfidence float64 `json:"effective_confidence"`
}

// GetEffectivePrior returns a blended prior for the given agent and model.
// The blending uses Bayesian weighting based on confidence at each level:
//   effective = instance * confidence + parent * (1-confidence)
//
// The hierarchy is: AgentModel -> Model -> Global
// Lower levels with high confidence override higher levels.
func (ph *PriorHierarchy) GetEffectivePrior(agentID, modelName string) *EffectivePrior {
	ph.mu.RLock()
	defer ph.mu.RUnlock()

	// Get confidence at each level
	globalConf := ph.globalPriors.Confidence()

	var modelConf float64
	var modelPriors *ModelPriors
	if mp, ok := ph.modelPriors[modelName]; ok {
		modelConf = mp.Confidence()
		modelPriors = mp
	}

	var agentModelConf float64
	var agentModelPriors *AgentModelPriors
	key := priorAgentModelKey{AgentID: agentID, ModelName: modelName}
	if amp, ok := ph.agentModelPriors[key]; ok {
		agentModelConf = amp.Confidence()
		agentModelPriors = amp
	}

	// Calculate weights using Bayesian blending
	// More specific levels with higher confidence get more weight
	totalConf := globalConf + modelConf + agentModelConf
	if totalConf < 1e-10 {
		totalConf = 1.0 // Avoid division by zero
		globalConf = 1.0
	}

	globalWeight := globalConf / totalConf
	modelWeight := modelConf / totalConf
	agentModelWeight := agentModelConf / totalConf

	result := &EffectivePrior{
		GlobalWeight:        globalWeight,
		ModelWeight:         modelWeight,
		AgentModelWeight:    agentModelWeight,
		EffectiveConfidence: totalConf / 3.0, // Average confidence
	}

	// Blend each parameter
	result.ContextThreshold = ph.blendContextThreshold(
		globalWeight, modelWeight, agentModelWeight,
		modelPriors, agentModelPriors)

	result.QualityThreshold = ph.blendQualityThreshold(
		globalWeight, modelWeight, agentModelWeight,
		modelPriors, agentModelPriors)

	result.OptimalContextSize = ph.blendOptimalContextSize(
		globalWeight, modelWeight, agentModelWeight,
		modelPriors, agentModelPriors)

	result.PreserveRecentTurns = ph.blendPreserveRecentTurns(
		globalWeight, modelWeight, agentModelWeight,
		modelPriors, agentModelPriors)

	result.HandoffSuccessRate = ph.blendHandoffSuccessRate(
		globalWeight, modelWeight, agentModelWeight,
		modelPriors, agentModelPriors)

	result.ContextDecayRate = ph.blendContextDecayRate(
		globalWeight, modelWeight, agentModelWeight,
		modelPriors, agentModelPriors)

	return result
}

// blendContextThreshold blends context threshold across levels.
func (ph *PriorHierarchy) blendContextThreshold(
	globalWeight, modelWeight, agentModelWeight float64,
	modelPriors *ModelPriors,
	agentModelPriors *AgentModelPriors,
) float64 {
	result := 0.0

	if ph.globalPriors != nil && ph.globalPriors.ContextThreshold != nil {
		result += globalWeight * ph.globalPriors.ContextThreshold.Mean()
	}
	if modelPriors != nil && modelPriors.ContextThreshold != nil {
		result += modelWeight * modelPriors.ContextThreshold.Mean()
	}
	if agentModelPriors != nil && agentModelPriors.ContextThreshold != nil {
		result += agentModelWeight * agentModelPriors.ContextThreshold.Mean()
	}

	return result
}

// blendQualityThreshold blends quality threshold across levels.
func (ph *PriorHierarchy) blendQualityThreshold(
	globalWeight, modelWeight, agentModelWeight float64,
	modelPriors *ModelPriors,
	agentModelPriors *AgentModelPriors,
) float64 {
	result := 0.0

	if ph.globalPriors != nil && ph.globalPriors.QualityThreshold != nil {
		result += globalWeight * ph.globalPriors.QualityThreshold.Mean()
	}
	if modelPriors != nil && modelPriors.QualityThreshold != nil {
		result += modelWeight * modelPriors.QualityThreshold.Mean()
	}
	if agentModelPriors != nil && agentModelPriors.QualityThreshold != nil {
		result += agentModelWeight * agentModelPriors.QualityThreshold.Mean()
	}

	return result
}

// blendOptimalContextSize blends optimal context size across levels.
func (ph *PriorHierarchy) blendOptimalContextSize(
	globalWeight, modelWeight, agentModelWeight float64,
	modelPriors *ModelPriors,
	agentModelPriors *AgentModelPriors,
) int {
	result := 0.0

	if ph.globalPriors != nil && ph.globalPriors.OptimalContextSize != nil {
		result += globalWeight * float64(ph.globalPriors.OptimalContextSize.Mean())
	}
	if modelPriors != nil && modelPriors.OptimalContextSize != nil {
		result += modelWeight * float64(modelPriors.OptimalContextSize.Mean())
	}
	if agentModelPriors != nil && agentModelPriors.OptimalContextSize != nil {
		result += agentModelWeight * float64(agentModelPriors.OptimalContextSize.Mean())
	}

	return int(math.Round(result))
}

// blendPreserveRecentTurns blends preserve recent turns across levels.
func (ph *PriorHierarchy) blendPreserveRecentTurns(
	globalWeight, modelWeight, agentModelWeight float64,
	modelPriors *ModelPriors,
	agentModelPriors *AgentModelPriors,
) int {
	result := 0.0

	if ph.globalPriors != nil && ph.globalPriors.PreserveRecentTurns != nil {
		result += globalWeight * float64(ph.globalPriors.PreserveRecentTurns.Mean())
	}
	if modelPriors != nil && modelPriors.PreserveRecentTurns != nil {
		result += modelWeight * float64(modelPriors.PreserveRecentTurns.Mean())
	}
	if agentModelPriors != nil && agentModelPriors.PreserveRecentTurns != nil {
		result += agentModelWeight * float64(agentModelPriors.PreserveRecentTurns.Mean())
	}

	return int(math.Round(result))
}

// blendHandoffSuccessRate blends handoff success rate across levels.
func (ph *PriorHierarchy) blendHandoffSuccessRate(
	globalWeight, modelWeight, agentModelWeight float64,
	modelPriors *ModelPriors,
	agentModelPriors *AgentModelPriors,
) float64 {
	result := 0.0

	if ph.globalPriors != nil && ph.globalPriors.HandoffSuccessRate != nil {
		result += globalWeight * ph.globalPriors.HandoffSuccessRate.Mean()
	}
	if modelPriors != nil && modelPriors.HandoffSuccessRate != nil {
		result += modelWeight * modelPriors.HandoffSuccessRate.Mean()
	}
	if agentModelPriors != nil && agentModelPriors.HandoffSuccessRate != nil {
		result += agentModelWeight * agentModelPriors.HandoffSuccessRate.Mean()
	}

	return result
}

// blendContextDecayRate blends context decay rate across levels.
func (ph *PriorHierarchy) blendContextDecayRate(
	globalWeight, modelWeight, agentModelWeight float64,
	modelPriors *ModelPriors,
	agentModelPriors *AgentModelPriors,
) float64 {
	result := 0.0

	if ph.globalPriors != nil && ph.globalPriors.ContextDecayRate != nil {
		result += globalWeight * ph.globalPriors.ContextDecayRate.Mean()
	}
	if modelPriors != nil && modelPriors.ContextDecayRate != nil {
		result += modelWeight * modelPriors.ContextDecayRate.Mean()
	}
	if agentModelPriors != nil && agentModelPriors.ContextDecayRate != nil {
		result += agentModelWeight * agentModelPriors.ContextDecayRate.Mean()
	}

	return result
}

// UpdateFromObservation updates priors at all applicable levels.
func (ph *PriorHierarchy) UpdateFromObservation(agentID, modelName string, obs *PriorObservation) {
	if obs == nil {
		return
	}

	ph.mu.Lock()
	defer ph.mu.Unlock()

	// Update model priors
	if mp, ok := ph.modelPriors[modelName]; ok {
		mp.UpdateFromObservation(obs, ph.updateConfig)
	} else {
		mp := NewModelPriors(modelName, ph.globalPriors)
		mp.UpdateFromObservation(obs, ph.updateConfig)
		ph.modelPriors[modelName] = mp
	}

	// Update agent+model priors
	key := priorAgentModelKey{AgentID: agentID, ModelName: modelName}
	if amp, ok := ph.agentModelPriors[key]; ok {
		amp.UpdateFromObservation(obs, ph.updateConfig)
	} else {
		var modelPriors *ModelPriors
		if mp, ok := ph.modelPriors[modelName]; ok {
			modelPriors = mp
		}
		amp := NewAgentModelPriors(agentID, modelName, modelPriors)
		amp.UpdateFromObservation(obs, ph.updateConfig)
		ph.agentModelPriors[key] = amp
	}
}

// PriorHierarchyStats contains statistics about the prior hierarchy.
type PriorHierarchyStats struct {
	ModelCount      int `json:"model_count"`
	AgentModelCount int `json:"agent_model_count"`
}

// GetStats returns statistics about the prior hierarchy.
func (ph *PriorHierarchy) GetStats() PriorHierarchyStats {
	ph.mu.RLock()
	defer ph.mu.RUnlock()

	return PriorHierarchyStats{
		ModelCount:      len(ph.modelPriors),
		AgentModelCount: len(ph.agentModelPriors),
	}
}

// Clear removes all model and agent+model priors, keeping global priors.
func (ph *PriorHierarchy) Clear() {
	ph.mu.Lock()
	defer ph.mu.Unlock()

	ph.modelPriors = make(map[string]*ModelPriors)
	ph.agentModelPriors = make(map[priorAgentModelKey]*AgentModelPriors)
}

// =============================================================================
// JSON Serialization
// =============================================================================

// priorHierarchyJSON is used for JSON marshaling/unmarshaling.
type priorHierarchyJSON struct {
	GlobalPriors     *GlobalPriors                         `json:"global_priors"`
	ModelPriors      map[string]*ModelPriors               `json:"model_priors"`
	AgentModelPriors map[string]*AgentModelPriors          `json:"agent_model_priors"`
	UpdateConfig     *UpdateConfig                         `json:"update_config"`
}

// MarshalJSON implements json.Marshaler.
func (ph *PriorHierarchy) MarshalJSON() ([]byte, error) {
	ph.mu.RLock()
	defer ph.mu.RUnlock()

	// Convert agent+model priors map to string keys
	agentModelJSON := make(map[string]*AgentModelPriors)
	for k, v := range ph.agentModelPriors {
		key := fmt.Sprintf("%s|%s", k.AgentID, k.ModelName)
		agentModelJSON[key] = v
	}

	return json.Marshal(priorHierarchyJSON{
		GlobalPriors:     ph.globalPriors,
		ModelPriors:      ph.modelPriors,
		AgentModelPriors: agentModelJSON,
		UpdateConfig:     ph.updateConfig,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (ph *PriorHierarchy) UnmarshalJSON(data []byte) error {
	var temp priorHierarchyJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	ph.mu.Lock()
	defer ph.mu.Unlock()

	if temp.GlobalPriors != nil {
		ph.globalPriors = temp.GlobalPriors
	} else {
		ph.globalPriors = DefaultGlobalPriors()
	}

	if temp.ModelPriors != nil {
		ph.modelPriors = temp.ModelPriors
	} else {
		ph.modelPriors = make(map[string]*ModelPriors)
	}

	// Parse agent+model priors
	ph.agentModelPriors = make(map[priorAgentModelKey]*AgentModelPriors)
	for keyStr, v := range temp.AgentModelPriors {
		key, err := parsePriorAgentModelKey(keyStr)
		if err == nil {
			ph.agentModelPriors[key] = v
		}
	}

	if temp.UpdateConfig != nil {
		ph.updateConfig = temp.UpdateConfig
	} else {
		ph.updateConfig = DefaultUpdateConfig()
	}

	return nil
}

// parsePriorAgentModelKey parses a pipe-separated agent+model key string.
func parsePriorAgentModelKey(s string) (priorAgentModelKey, error) {
	var key priorAgentModelKey
	parts := splitKey(s, 2)
	if len(parts) == 2 {
		key.AgentID = parts[0]
		key.ModelName = parts[1]
		return key, nil
	}
	return key, fmt.Errorf("invalid agent-model key: %s", s)
}

// =============================================================================
// Helper Functions
// =============================================================================

// cloneLearnedWeight creates a deep copy of a LearnedWeight.
func cloneLearnedWeight(lw *LearnedWeight) *LearnedWeight {
	if lw == nil {
		return nil
	}
	return &LearnedWeight{
		Alpha:            lw.Alpha,
		Beta:             lw.Beta,
		EffectiveSamples: lw.EffectiveSamples,
		PriorAlpha:       lw.PriorAlpha,
		PriorBeta:        lw.PriorBeta,
	}
}

// cloneLearnedContextSize creates a deep copy of a LearnedContextSize.
func cloneLearnedContextSize(lcs *LearnedContextSize) *LearnedContextSize {
	if lcs == nil {
		return nil
	}
	return &LearnedContextSize{
		Alpha:            lcs.Alpha,
		Beta:             lcs.Beta,
		EffectiveSamples: lcs.EffectiveSamples,
		PriorAlpha:       lcs.PriorAlpha,
		PriorBeta:        lcs.PriorBeta,
	}
}

// blendLearnedWeights blends two LearnedWeight values with given weights.
func blendLearnedWeights(a, b *LearnedWeight, weightA, weightB float64) *LearnedWeight {
	if a == nil && b == nil {
		return NewLearnedWeight(5.0, 5.0) // Default
	}
	if a == nil {
		return cloneLearnedWeight(b)
	}
	if b == nil {
		return cloneLearnedWeight(a)
	}

	// Blend means, create new weight with blended parameters
	meanA := a.Mean()
	meanB := b.Mean()
	blendedMean := weightA*meanA + weightB*meanB

	// Use average precision
	precisionA := a.Alpha + a.Beta
	precisionB := b.Alpha + b.Beta
	blendedPrecision := (precisionA + precisionB) / 2.0

	alpha := blendedMean * blendedPrecision
	beta := (1.0 - blendedMean) * blendedPrecision

	return &LearnedWeight{
		Alpha:            alpha,
		Beta:             beta,
		EffectiveSamples: (a.EffectiveSamples + b.EffectiveSamples) / 2.0,
		PriorAlpha:       alpha,
		PriorBeta:        beta,
	}
}

// blendLearnedContextSizes blends two LearnedContextSize values with given weights.
func blendLearnedContextSizes(a, b *LearnedContextSize, weightA, weightB float64) *LearnedContextSize {
	if a == nil && b == nil {
		return NewLearnedContextSize(100.0, 1.0) // Default
	}
	if a == nil {
		return cloneLearnedContextSize(b)
	}
	if b == nil {
		return cloneLearnedContextSize(a)
	}

	// Blend means
	meanA := float64(a.Mean())
	meanB := float64(b.Mean())
	blendedMean := weightA*meanA + weightB*meanB

	// For Gamma distribution, alpha = mean * beta
	// Use average beta
	avgBeta := (a.Beta + b.Beta) / 2.0
	alpha := blendedMean * avgBeta

	return &LearnedContextSize{
		Alpha:            alpha,
		Beta:             avgBeta,
		EffectiveSamples: (a.EffectiveSamples + b.EffectiveSamples) / 2.0,
		PriorAlpha:       alpha,
		PriorBeta:        avgBeta,
	}
}
