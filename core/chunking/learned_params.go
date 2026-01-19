package chunking

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
)

// =============================================================================
// LearnedContextSize Type
// =============================================================================

// LearnedContextSize uses a Gamma distribution to model optimal context window
// sizes as positive integers. It adapts over time through Bayesian updates with
// exponential decay to weight recent observations more heavily.
//
// The Gamma distribution is parameterized by shape (Alpha) and rate (Beta):
//   - Mean = Alpha / Beta
//   - Variance = Alpha / (Beta^2)
//
// Higher EffectiveSamples indicates more confidence in the learned parameters.
type LearnedContextSize struct {
	// Alpha is the shape parameter of the Gamma distribution (must be > 0).
	Alpha float64 `json:"alpha"`

	// Beta is the rate parameter of the Gamma distribution (must be > 0).
	Beta float64 `json:"beta"`

	// EffectiveSamples tracks the weighted count of observations.
	// Decays over time to prioritize recent data.
	EffectiveSamples float64 `json:"effective_samples"`

	// PriorAlpha is the initial shape parameter before any observations.
	PriorAlpha float64 `json:"prior_alpha"`

	// PriorBeta is the initial rate parameter before any observations.
	PriorBeta float64 `json:"prior_beta"`
}

// UpdateConfig controls how the Gamma parameters are updated with new data.
type UpdateConfig struct {
	// DecayFactor determines how much weight to give to existing parameters
	// versus new observations. Must be in range (0, 1].
	// Values closer to 1 give more weight to history.
	// Values closer to 0 adapt faster to new data.
	DecayFactor float64 `json:"decay_factor"`

	// MinEffectiveSamples is the minimum number of effective samples to
	// maintain. Prevents the distribution from becoming too uncertain.
	MinEffectiveSamples float64 `json:"min_effective_samples"`
}

// DefaultUpdateConfig returns sensible defaults for updating parameters.
func DefaultUpdateConfig() *UpdateConfig {
	return &UpdateConfig{
		DecayFactor:         0.95,
		MinEffectiveSamples: 1.0,
	}
}

// NewLearnedContextSize creates a new LearnedContextSize with initial priors.
// The prior should represent a reasonable default context size.
// For example, priorMean=2048, priorVariance=1024 suggests contexts around 2048
// with moderate uncertainty.
func NewLearnedContextSize(priorMean, priorVariance float64) (*LearnedContextSize, error) {
	if priorMean <= 0 {
		return nil, fmt.Errorf("prior mean must be positive, got %f", priorMean)
	}
	if priorVariance <= 0 {
		return nil, fmt.Errorf("prior variance must be positive, got %f", priorVariance)
	}

	beta := priorMean / priorVariance
	alpha := priorMean * beta

	return &LearnedContextSize{
		Alpha:            alpha,
		Beta:             beta,
		EffectiveSamples: 1.0,
		PriorAlpha:       alpha,
		PriorBeta:        beta,
	}, nil
}

// Mean returns the expected value of the Gamma distribution as an integer.
// This represents the current best estimate for optimal context size.
func (lcs *LearnedContextSize) Mean() int {
	if lcs.Beta <= 0 {
		return 0
	}
	mean := lcs.Alpha / lcs.Beta
	return int(math.Round(mean))
}

// Sample generates a random context size from the current Gamma distribution.
// Returns a positive integer suitable for use as a context window size.
func (lcs *LearnedContextSize) Sample() int {
	if lcs.Alpha <= 0 || lcs.Beta <= 0 {
		return 0
	}

	value := gammaRandom(lcs.Alpha, lcs.Beta)
	result := int(math.Round(value))
	if result < 1 {
		result = 1
	}
	return result
}

// Confidence returns a value in [0, 1] indicating confidence in the estimate.
// Higher effective samples lead to higher confidence, asymptotically approaching 1.
func (lcs *LearnedContextSize) Confidence() float64 {
	return 1.0 - math.Exp(-lcs.EffectiveSamples/10.0)
}

// Update performs a Bayesian update of the Gamma parameters using a new
// observed context size. Uses exponential decay to weight recent observations.
func (lcs *LearnedContextSize) Update(observed int, config *UpdateConfig) error {
	if err := validateUpdateParams(observed, config); err != nil {
		return err
	}
	if config == nil {
		config = DefaultUpdateConfig()
	}

	lcs.applyUpdate(observed, config)
	return nil
}

// validateUpdateParams checks if update parameters are valid.
func validateUpdateParams(observed int, config *UpdateConfig) error {
	if observed <= 0 {
		return fmt.Errorf("observed value must be positive, got %d", observed)
	}
	if config == nil {
		return nil
	}
	return validateDecayFactor(config.DecayFactor)
}

// validateDecayFactor checks if the decay factor is in valid range (0, 1].
func validateDecayFactor(decayFactor float64) error {
	if decayFactor <= 0 || decayFactor > 1 {
		return fmt.Errorf("decay factor must be in (0, 1], got %f", decayFactor)
	}
	return nil
}

// applyUpdate applies the Bayesian update to the Gamma parameters.
func (lcs *LearnedContextSize) applyUpdate(observed int, config *UpdateConfig) {
	decay := config.DecayFactor
	newSamples := lcs.EffectiveSamples*decay + 1.0

	if newSamples < config.MinEffectiveSamples {
		newSamples = config.MinEffectiveSamples
	}

	weight := 1.0 / newSamples
	lcs.Alpha = lcs.Alpha*decay + float64(observed)*lcs.Beta*weight
	lcs.EffectiveSamples = newSamples
}

// MarshalJSON implements custom JSON marshaling.
func (lcs *LearnedContextSize) MarshalJSON() ([]byte, error) {
	type Alias LearnedContextSize
	return json.Marshal((*Alias)(lcs))
}

// UnmarshalJSON implements custom JSON unmarshaling.
func (lcs *LearnedContextSize) UnmarshalJSON(data []byte) error {
	type Alias LearnedContextSize
	aux := (*Alias)(lcs)
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	return nil
}

// =============================================================================
// Gamma Distribution Random Sampling
// =============================================================================

// gammaRandom generates a random sample from Gamma(alpha, beta).
// Uses the Marsaglia and Tsang method for alpha >= 1.
// For alpha < 1, uses the fact that Gamma(alpha, beta) = Gamma(alpha+1, beta) * U^(1/alpha).
func gammaRandom(alpha, beta float64) float64 {
	if alpha < 1.0 {
		return gammaRandom(alpha+1.0, beta) * math.Pow(rand.Float64(), 1.0/alpha)
	}
	return gammaMarsagliaTsang(alpha, beta)
}

// gammaMarsagliaTsang implements the Marsaglia and Tsang method for alpha >= 1.
func gammaMarsagliaTsang(alpha, beta float64) float64 {
	d := alpha - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		x := rand.NormFloat64()
		v := 1.0 + c*x
		if v <= 0 {
			continue
		}

		v = v * v * v
		u := rand.Float64()

		if acceptSample(u, x, v, d) {
			return d * v / beta
		}
	}
}

// acceptSample checks if a sample should be accepted in the Marsaglia-Tsang method.
func acceptSample(u, x, v, d float64) bool {
	x2 := x * x
	if u < 1.0-0.331*x2*x2 {
		return true
	}
	return math.Log(u) < 0.5*x2+d*(1.0-v+math.Log(v))
}

// =============================================================================
// LearnedOverflowWeights Type (CK.1.2)
// =============================================================================

// OverflowStrategy represents different strategies for handling chunk overflow.
type OverflowStrategy int

const (
	// StrategyRecursive splits the chunk recursively into smaller pieces.
	StrategyRecursive OverflowStrategy = iota

	// StrategySentence splits the chunk at sentence boundaries.
	StrategySentence

	// StrategyTruncate simply truncates the chunk to fit.
	StrategyTruncate
)

// String returns the string representation of the overflow strategy.
func (s OverflowStrategy) String() string {
	switch s {
	case StrategyRecursive:
		return "recursive"
	case StrategySentence:
		return "sentence"
	case StrategyTruncate:
		return "truncate"
	default:
		return "unknown"
	}
}

// LearnedOverflowWeights uses a Dirichlet-Multinomial distribution to learn
// which overflow strategy works best. It implements Thompson Sampling for
// exploration-exploitation balance.
//
// The Dirichlet distribution maintains pseudo-counts for each strategy:
//   - Higher counts indicate more successful uses of that strategy
//   - The distribution naturally balances exploration and exploitation
type LearnedOverflowWeights struct {
	// RecursiveCount is the pseudo-count for recursive splitting strategy.
	RecursiveCount float64 `json:"recursive_count"`

	// SentenceCount is the pseudo-count for sentence boundary splitting.
	SentenceCount float64 `json:"sentence_count"`

	// TruncateCount is the pseudo-count for truncation strategy.
	TruncateCount float64 `json:"truncate_count"`
}

// NewLearnedOverflowWeights creates a new LearnedOverflowWeights with
// uniform priors (all strategies equally likely initially).
func NewLearnedOverflowWeights() *LearnedOverflowWeights {
	return &LearnedOverflowWeights{
		RecursiveCount: 1.0,
		SentenceCount:  1.0,
		TruncateCount:  1.0,
	}
}

// NewLearnedOverflowWeightsWithPriors creates a new LearnedOverflowWeights
// with custom prior pseudo-counts for each strategy.
func NewLearnedOverflowWeightsWithPriors(recursive, sentence, truncate float64) (*LearnedOverflowWeights, error) {
	if recursive <= 0 || sentence <= 0 || truncate <= 0 {
		return nil, fmt.Errorf("all pseudo-counts must be positive")
	}
	return &LearnedOverflowWeights{
		RecursiveCount: recursive,
		SentenceCount:  sentence,
		TruncateCount:  truncate,
	}, nil
}

// BestStrategy returns the strategy with the highest expected probability.
// This is a greedy exploitation of current knowledge.
func (low *LearnedOverflowWeights) BestStrategy() OverflowStrategy {
	total := low.RecursiveCount + low.SentenceCount + low.TruncateCount

	recursiveProb := low.RecursiveCount / total
	sentenceProb := low.SentenceCount / total
	truncateProb := low.TruncateCount / total

	if recursiveProb >= sentenceProb && recursiveProb >= truncateProb {
		return StrategyRecursive
	}
	if sentenceProb >= truncateProb {
		return StrategySentence
	}
	return StrategyTruncate
}

// Sample generates a random strategy using Thompson Sampling.
// Draws from the Dirichlet distribution and samples according to probabilities.
// This balances exploration (trying uncertain strategies) with exploitation
// (using known good strategies).
func (low *LearnedOverflowWeights) Sample() OverflowStrategy {
	recursiveSample := gammaRandom(low.RecursiveCount, 1.0)
	sentenceSample := gammaRandom(low.SentenceCount, 1.0)
	truncateSample := gammaRandom(low.TruncateCount, 1.0)

	total := recursiveSample + sentenceSample + truncateSample

	recursiveProb := recursiveSample / total
	sentenceProb := sentenceSample / total

	r := rand.Float64()
	if r < recursiveProb {
		return StrategyRecursive
	}
	if r < recursiveProb+sentenceProb {
		return StrategySentence
	}
	return StrategyTruncate
}

// Update performs a Bayesian update of the strategy weights based on
// whether the chosen strategy was useful.
func (low *LearnedOverflowWeights) Update(chosen OverflowStrategy, wasUseful bool) {
	increment := 0.0
	if wasUseful {
		increment = 1.0
	} else {
		increment = 0.1
	}

	switch chosen {
	case StrategyRecursive:
		low.RecursiveCount += increment
	case StrategySentence:
		low.SentenceCount += increment
	case StrategyTruncate:
		low.TruncateCount += increment
	}
}

// MarshalJSON implements custom JSON marshaling.
func (low *LearnedOverflowWeights) MarshalJSON() ([]byte, error) {
	type Alias LearnedOverflowWeights
	return json.Marshal((*Alias)(low))
}

// UnmarshalJSON implements custom JSON unmarshaling.
func (low *LearnedOverflowWeights) UnmarshalJSON(data []byte) error {
	type Alias LearnedOverflowWeights
	aux := (*Alias)(low)
	return json.Unmarshal(data, aux)
}
