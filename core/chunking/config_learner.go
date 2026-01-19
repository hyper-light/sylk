package chunking

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// =============================================================================
// CK.2.1 ChunkUsageObservation Type
// =============================================================================

// ChunkUsageObservation records how a chunk was used and whether it was useful.
// This feedback is used to update the learned parameters for chunk configuration.
type ChunkUsageObservation struct {
	// Domain indicates the type of content that was chunked.
	Domain Domain `json:"domain"`

	// TokenCount is the actual chunk size in tokens.
	TokenCount int `json:"token_count"`

	// ContextBefore is the number of context tokens included before the chunk.
	ContextBefore int `json:"context_before"`

	// ContextAfter is the number of context tokens included after the chunk.
	ContextAfter int `json:"context_after"`

	// WasUseful indicates whether the chunk was useful to the agent.
	// True means the chunk contributed positively to the task.
	WasUseful bool `json:"was_useful"`

	// OverflowStrategy is non-nil if the chunk was the result of an overflow
	// handling strategy. This helps learn which strategies work best.
	OverflowStrategy *OverflowStrategy `json:"overflow_strategy,omitempty"`

	// Timestamp records when this observation was made.
	Timestamp time.Time `json:"timestamp"`

	// ChunkID identifies the specific chunk that was used.
	ChunkID string `json:"chunk_id"`

	// SessionID identifies the session context for this observation.
	SessionID string `json:"session_id"`
}

// Validate checks that the observation has valid values.
func (o *ChunkUsageObservation) Validate() error {
	if o.TokenCount <= 0 {
		return fmt.Errorf("token count must be positive, got %d", o.TokenCount)
	}
	if o.ContextBefore < 0 {
		return fmt.Errorf("context before must be non-negative, got %d", o.ContextBefore)
	}
	if o.ContextAfter < 0 {
		return fmt.Errorf("context after must be non-negative, got %d", o.ContextAfter)
	}
	if o.ChunkID == "" {
		return fmt.Errorf("chunk ID must not be empty")
	}
	if o.SessionID == "" {
		return fmt.Errorf("session ID must not be empty")
	}
	if o.Timestamp.IsZero() {
		return fmt.Errorf("timestamp must not be zero")
	}
	return nil
}

// =============================================================================
// CK.2.2 ChunkConfigLearner Type
// =============================================================================

// ChunkConfigLearner maintains learned chunk configurations per domain and
// provides methods for recording observations and retrieving configurations
// using Thompson Sampling for exploration-exploitation balance.
type ChunkConfigLearner struct {
	mu sync.RWMutex

	// DomainConfigs holds the learned configuration for each domain.
	DomainConfigs map[Domain]*ChunkConfig `json:"domain_configs"`

	// GlobalPriors provides a fallback configuration for cold start scenarios.
	GlobalPriors *ChunkConfig `json:"global_priors"`

	// DomainConfidence tracks how confident we are in each domain's learned config.
	// Values are in [0, 1], where higher values indicate more confidence.
	DomainConfidence map[Domain]float64 `json:"domain_confidence"`

	// observationCounts tracks the number of observations per domain for confidence.
	observationCounts map[Domain]int

	// decayConfig controls how much weight is given to recent vs. older observations.
	decayConfig *UpdateConfig
}

// NewChunkConfigLearner creates a new ChunkConfigLearner with the given global priors.
// If globalPriors is nil, a default configuration with 2048 max tokens is used.
func NewChunkConfigLearner(globalPriors *ChunkConfig) (*ChunkConfigLearner, error) {
	if globalPriors == nil {
		var err error
		globalPriors, err = NewChunkConfig(2048)
		if err != nil {
			return nil, fmt.Errorf("failed to create default global priors: %w", err)
		}
	}

	return &ChunkConfigLearner{
		DomainConfigs:     make(map[Domain]*ChunkConfig),
		GlobalPriors:      globalPriors,
		DomainConfidence:  make(map[Domain]float64),
		observationCounts: make(map[Domain]int),
		decayConfig:       DefaultUpdateConfig(),
	}, nil
}

// RecordObservation updates the learned parameters based on the provided observation.
// When WasUseful is true, the observed parameters are reinforced.
// When WasUseful is false, confidence in those parameters is decreased.
func (ccl *ChunkConfigLearner) RecordObservation(obs ChunkUsageObservation) error {
	if err := obs.Validate(); err != nil {
		return fmt.Errorf("invalid observation: %w", err)
	}

	ccl.mu.Lock()
	defer ccl.mu.Unlock()

	// Ensure we have a domain config to update
	if _, exists := ccl.DomainConfigs[obs.Domain]; !exists {
		if err := ccl.initializeDomainConfig(obs.Domain); err != nil {
			return fmt.Errorf("failed to initialize domain config: %w", err)
		}
	}

	config := ccl.DomainConfigs[obs.Domain]

	// Calculate observation weight based on usefulness
	weight := ccl.calculateObservationWeight(obs)

	// Apply decay to existing parameters to favor recent observations
	ccl.applyDecay(config, ccl.decayConfig.DecayFactor)

	// Update Gamma posteriors for context sizes
	if obs.WasUseful {
		// Reinforce the observed token count
		ccl.updateGammaPosterior(config.TargetTokens, obs.TokenCount, weight)
		ccl.updateGammaPosterior(config.ContextTokensBefore, obs.ContextBefore, weight)
		ccl.updateGammaPosterior(config.ContextTokensAfter, obs.ContextAfter, weight)
	} else {
		// Reduce confidence but don't heavily penalize - use reduced weight
		reducedWeight := weight * 0.1
		ccl.updateGammaPosterior(config.TargetTokens, obs.TokenCount, reducedWeight)
		ccl.updateGammaPosterior(config.ContextTokensBefore, obs.ContextBefore, reducedWeight)
		ccl.updateGammaPosterior(config.ContextTokensAfter, obs.ContextAfter, reducedWeight)
	}

	// Update Dirichlet posterior for overflow strategy if applicable
	if obs.OverflowStrategy != nil {
		ccl.updateDirichletPosterior(config.OverflowStrategyWeights, *obs.OverflowStrategy, obs.WasUseful)
	}

	// Update observation count and confidence
	ccl.observationCounts[obs.Domain]++
	ccl.updateDomainConfidence(obs.Domain)

	return nil
}

// GetConfig returns the chunk configuration for the given domain.
// If explore is true, Thompson Sampling is used to balance exploration and exploitation.
// If explore is false, the mean (best estimate) values are returned.
func (ccl *ChunkConfigLearner) GetConfig(domain Domain, explore bool) *ChunkConfig {
	ccl.mu.RLock()
	defer ccl.mu.RUnlock()

	// Get domain-specific config if available
	domainConfig, hasDomain := ccl.DomainConfigs[domain]
	if !hasDomain {
		// Return global priors for cold start
		return ccl.GlobalPriors
	}

	// Get confidence weight for blending
	confidence := ccl.DomainConfidence[domain]

	// Blend domain config with global priors based on confidence
	blendedConfig := ccl.blendConfigs(ccl.GlobalPriors, domainConfig, confidence)

	// If not exploring, we already have the blended config
	if !explore {
		return blendedConfig
	}

	// For exploration, sample from the blended distribution
	// The exploration happens through the GetEffective* methods on ChunkConfig
	return blendedConfig
}

// initializeDomainConfig creates a new domain config based on domain-specific priors.
func (ccl *ChunkConfigLearner) initializeDomainConfig(domain Domain) error {
	priorConfig, err := PriorChunkConfig(ccl.GlobalPriors.MaxTokens, domain)
	if err != nil {
		return fmt.Errorf("failed to create prior config for domain %s: %w", domain, err)
	}
	ccl.DomainConfigs[domain] = priorConfig
	ccl.DomainConfidence[domain] = 0.0
	ccl.observationCounts[domain] = 0
	return nil
}

// calculateObservationWeight determines how much weight to give an observation.
// More recent observations and useful observations get higher weight.
func (ccl *ChunkConfigLearner) calculateObservationWeight(obs ChunkUsageObservation) float64 {
	baseWeight := 1.0
	if obs.WasUseful {
		baseWeight = 1.0
	} else {
		baseWeight = 0.5 // Less weight for non-useful observations
	}

	// Apply time-based decay (observations older than 24 hours get less weight)
	age := time.Since(obs.Timestamp)
	timeFactor := math.Exp(-age.Hours() / 24.0) // Half-life of ~24 hours

	return baseWeight * math.Max(0.1, timeFactor)
}

// applyDecay applies recency weighting to the configuration's learned parameters.
// This ensures that older observations have less influence over time.
func (ccl *ChunkConfigLearner) applyDecay(config *ChunkConfig, decayFactor float64) {
	if config == nil || decayFactor <= 0 || decayFactor > 1 {
		return
	}

	// Apply decay to effective samples in Gamma distributions
	if config.TargetTokens != nil {
		config.TargetTokens.EffectiveSamples *= decayFactor
		if config.TargetTokens.EffectiveSamples < ccl.decayConfig.MinEffectiveSamples {
			config.TargetTokens.EffectiveSamples = ccl.decayConfig.MinEffectiveSamples
		}
	}

	if config.MinTokens != nil {
		config.MinTokens.EffectiveSamples *= decayFactor
		if config.MinTokens.EffectiveSamples < ccl.decayConfig.MinEffectiveSamples {
			config.MinTokens.EffectiveSamples = ccl.decayConfig.MinEffectiveSamples
		}
	}

	if config.ContextTokensBefore != nil {
		config.ContextTokensBefore.EffectiveSamples *= decayFactor
		if config.ContextTokensBefore.EffectiveSamples < ccl.decayConfig.MinEffectiveSamples {
			config.ContextTokensBefore.EffectiveSamples = ccl.decayConfig.MinEffectiveSamples
		}
	}

	if config.ContextTokensAfter != nil {
		config.ContextTokensAfter.EffectiveSamples *= decayFactor
		if config.ContextTokensAfter.EffectiveSamples < ccl.decayConfig.MinEffectiveSamples {
			config.ContextTokensAfter.EffectiveSamples = ccl.decayConfig.MinEffectiveSamples
		}
	}
}

// updateGammaPosterior performs a Bayesian update on a LearnedContextSize parameter.
// The update is weighted, allowing for differential influence of observations.
func (ccl *ChunkConfigLearner) updateGammaPosterior(param *LearnedContextSize, observed int, weight float64) {
	if param == nil || observed <= 0 || weight <= 0 {
		return
	}

	// Bayesian update for Gamma distribution:
	// For a Gamma(alpha, beta) prior with observation x:
	// - Alpha is updated to incorporate the new observation
	// - The effective sample count tracks weighted observations

	// Calculate current mean
	currentMean := param.Alpha / param.Beta

	// Update effective samples with the weighted observation
	param.EffectiveSamples += weight

	// Compute weighted average of current mean and observed value
	// This ensures the distribution shifts toward observed values
	observedWeight := weight / param.EffectiveSamples
	newMean := currentMean*(1-observedWeight) + float64(observed)*observedWeight

	// Update Alpha to reflect new mean while keeping Beta fixed
	// This maintains the conjugate prior structure
	param.Alpha = newMean * param.Beta
}

// updateDirichletPosterior updates the Dirichlet distribution for overflow strategy weights.
// Successful strategies get a full increment, unsuccessful ones get a smaller increment.
func (ccl *ChunkConfigLearner) updateDirichletPosterior(weights *LearnedOverflowWeights, strategy OverflowStrategy, wasUseful bool) {
	if weights == nil {
		return
	}

	// Dirichlet update: add pseudo-counts based on observations
	// Useful observations add 1.0, non-useful add 0.1 (still counts as usage)
	increment := 0.1
	if wasUseful {
		increment = 1.0
	}

	switch strategy {
	case StrategyRecursive:
		weights.RecursiveCount += increment
	case StrategySentence:
		weights.SentenceCount += increment
	case StrategyTruncate:
		weights.TruncateCount += increment
	}
}

// blendConfigs creates a new configuration by blending global priors with domain-specific
// learned parameters. The weight determines how much influence the domain config has.
// weight=0 returns pure priors, weight=1 returns pure domain config.
func (ccl *ChunkConfigLearner) blendConfigs(prior, domain *ChunkConfig, weight float64) *ChunkConfig {
	if prior == nil {
		return domain
	}
	if domain == nil || weight <= 0 {
		return prior
	}
	if weight >= 1 {
		return domain
	}

	// Create blended config with same max tokens
	blended := &ChunkConfig{
		MaxTokens: prior.MaxTokens,
	}

	// Blend each learned parameter
	blended.TargetTokens = ccl.blendGammaParams(prior.TargetTokens, domain.TargetTokens, weight)
	blended.MinTokens = ccl.blendGammaParams(prior.MinTokens, domain.MinTokens, weight)
	blended.ContextTokensBefore = ccl.blendGammaParams(prior.ContextTokensBefore, domain.ContextTokensBefore, weight)
	blended.ContextTokensAfter = ccl.blendGammaParams(prior.ContextTokensAfter, domain.ContextTokensAfter, weight)
	blended.OverflowStrategyWeights = ccl.blendDirichletParams(prior.OverflowStrategyWeights, domain.OverflowStrategyWeights, weight)

	return blended
}

// blendGammaParams blends two LearnedContextSize parameters.
func (ccl *ChunkConfigLearner) blendGammaParams(prior, domain *LearnedContextSize, weight float64) *LearnedContextSize {
	if prior == nil {
		return domain
	}
	if domain == nil {
		return prior
	}

	priorWeight := 1.0 - weight
	domainWeight := weight

	// Blend means using weighted average
	priorMean := prior.Alpha / prior.Beta
	domainMean := domain.Alpha / domain.Beta
	blendedMean := priorMean*priorWeight + domainMean*domainWeight

	// Blend effective samples (use max for confidence)
	blendedSamples := math.Max(prior.EffectiveSamples, domain.EffectiveSamples)

	// Create new params with blended mean
	// Keep Beta fixed, adjust Alpha to achieve blended mean
	return &LearnedContextSize{
		Alpha:            blendedMean * prior.Beta,
		Beta:             prior.Beta,
		EffectiveSamples: blendedSamples,
		PriorAlpha:       prior.PriorAlpha,
		PriorBeta:        prior.PriorBeta,
	}
}

// blendDirichletParams blends two LearnedOverflowWeights parameters.
func (ccl *ChunkConfigLearner) blendDirichletParams(prior, domain *LearnedOverflowWeights, weight float64) *LearnedOverflowWeights {
	if prior == nil {
		return domain
	}
	if domain == nil {
		return prior
	}

	priorWeight := 1.0 - weight
	domainWeight := weight

	// Blend pseudo-counts using weighted average
	return &LearnedOverflowWeights{
		RecursiveCount: prior.RecursiveCount*priorWeight + domain.RecursiveCount*domainWeight,
		SentenceCount:  prior.SentenceCount*priorWeight + domain.SentenceCount*domainWeight,
		TruncateCount:  prior.TruncateCount*priorWeight + domain.TruncateCount*domainWeight,
	}
}

// updateDomainConfidence updates the confidence level for a domain based on
// the number of observations. Uses a sigmoid-like function that asymptotes to 1.
func (ccl *ChunkConfigLearner) updateDomainConfidence(domain Domain) {
	count := ccl.observationCounts[domain]

	// Sigmoid-like confidence: 1 - exp(-count/scale)
	// With scale=10, confidence reaches ~0.63 after 10 observations,
	// ~0.86 after 20, ~0.95 after 30.
	scale := 10.0
	confidence := 1.0 - math.Exp(-float64(count)/scale)

	ccl.DomainConfidence[domain] = confidence
}

// GetDomainConfidence returns the current confidence level for a domain.
// Returns 0 if the domain has not been observed.
func (ccl *ChunkConfigLearner) GetDomainConfidence(domain Domain) float64 {
	ccl.mu.RLock()
	defer ccl.mu.RUnlock()
	return ccl.DomainConfidence[domain]
}

// GetObservationCount returns the number of observations for a domain.
func (ccl *ChunkConfigLearner) GetObservationCount(domain Domain) int {
	ccl.mu.RLock()
	defer ccl.mu.RUnlock()
	return ccl.observationCounts[domain]
}

// SetDecayConfig allows customization of the decay configuration.
func (ccl *ChunkConfigLearner) SetDecayConfig(config *UpdateConfig) error {
	if config == nil {
		return fmt.Errorf("decay config must not be nil")
	}
	if config.DecayFactor <= 0 || config.DecayFactor > 1 {
		return fmt.Errorf("decay factor must be in (0, 1], got %f", config.DecayFactor)
	}
	if config.MinEffectiveSamples <= 0 {
		return fmt.Errorf("min effective samples must be positive, got %f", config.MinEffectiveSamples)
	}

	ccl.mu.Lock()
	defer ccl.mu.Unlock()
	ccl.decayConfig = config
	return nil
}
