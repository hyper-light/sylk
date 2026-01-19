package chunking

import "fmt"

// =============================================================================
// ChunkConfig Type (CK.1.3)
// =============================================================================

// Domain represents the type of content being chunked.
type Domain int

const (
	// DomainCode represents source code content.
	DomainCode Domain = iota

	// DomainAcademic represents academic or research content.
	DomainAcademic

	// DomainHistory represents historical or narrative content.
	DomainHistory

	// DomainGeneral represents general-purpose content.
	DomainGeneral
)

// String returns the string representation of the domain.
func (d Domain) String() string {
	switch d {
	case DomainCode:
		return "code"
	case DomainAcademic:
		return "academic"
	case DomainHistory:
		return "history"
	case DomainGeneral:
		return "general"
	default:
		return "unknown"
	}
}

// ChunkConfig holds all configuration parameters for chunking, including both
// fixed constraints (MaxTokens) and learned soft parameters that adapt over time.
//
// Soft parameters use Thompson Sampling via GetEffective* methods to balance
// exploration (trying different values) and exploitation (using known good values).
type ChunkConfig struct {
	// MaxTokens is a hard constraint based on the embedding model's limit.
	// This value is fixed and never learned.
	MaxTokens int `json:"max_tokens"`

	// TargetTokens is the ideal chunk size to aim for.
	// Learned from experience about what chunk sizes work best.
	TargetTokens *LearnedContextSize `json:"target_tokens"`

	// MinTokens is the minimum acceptable chunk size.
	// Learned to avoid chunks that are too small to be useful.
	MinTokens *LearnedContextSize `json:"min_tokens"`

	// ContextTokensBefore specifies how many tokens of context to include
	// before the main chunk content. Learned from retrieval quality feedback.
	ContextTokensBefore *LearnedContextSize `json:"context_tokens_before"`

	// ContextTokensAfter specifies how many tokens of context to include
	// after the main chunk content. Learned from retrieval quality feedback.
	ContextTokensAfter *LearnedContextSize `json:"context_tokens_after"`

	// OverflowStrategyWeights determines which strategy to use when a chunk
	// exceeds MaxTokens. Learned from which strategies produce better results.
	OverflowStrategyWeights *LearnedOverflowWeights `json:"overflow_strategy_weights"`
}

// NewChunkConfig creates a new ChunkConfig with the given max tokens and
// learned parameters. If any learned parameter is nil, it will be initialized
// with sensible defaults.
func NewChunkConfig(maxTokens int) (*ChunkConfig, error) {
	if maxTokens <= 0 {
		return nil, fmt.Errorf("max tokens must be positive, got %d", maxTokens)
	}

	targetTokens, err := NewLearnedContextSize(float64(maxTokens)/2, float64(maxTokens)/4)
	if err != nil {
		return nil, fmt.Errorf("failed to create target tokens: %w", err)
	}

	minTokens, err := NewLearnedContextSize(float64(maxTokens)/10, float64(maxTokens)/20)
	if err != nil {
		return nil, fmt.Errorf("failed to create min tokens: %w", err)
	}

	contextBefore, err := NewLearnedContextSize(100, 50)
	if err != nil {
		return nil, fmt.Errorf("failed to create context before: %w", err)
	}

	contextAfter, err := NewLearnedContextSize(50, 25)
	if err != nil {
		return nil, fmt.Errorf("failed to create context after: %w", err)
	}

	return &ChunkConfig{
		MaxTokens:               maxTokens,
		TargetTokens:            targetTokens,
		MinTokens:               minTokens,
		ContextTokensBefore:     contextBefore,
		ContextTokensAfter:      contextAfter,
		OverflowStrategyWeights: NewLearnedOverflowWeights(),
	}, nil
}

// GetEffectiveTargetTokens returns the target chunk size to use.
// If explore is true, samples from the learned distribution (Thompson Sampling).
// If explore is false, returns the mean (exploitation).
func (cc *ChunkConfig) GetEffectiveTargetTokens(explore bool) int {
	if cc.TargetTokens == nil {
		return cc.MaxTokens / 2
	}
	if explore {
		return cc.TargetTokens.Sample()
	}
	return cc.TargetTokens.Mean()
}

// GetEffectiveMinTokens returns the minimum chunk size to use.
// If explore is true, samples from the learned distribution.
// If explore is false, returns the mean.
func (cc *ChunkConfig) GetEffectiveMinTokens(explore bool) int {
	if cc.MinTokens == nil {
		return cc.MaxTokens / 10
	}
	if explore {
		return cc.MinTokens.Sample()
	}
	return cc.MinTokens.Mean()
}

// GetEffectiveContextTokensBefore returns the amount of preceding context to include.
// If explore is true, samples from the learned distribution.
// If explore is false, returns the mean.
func (cc *ChunkConfig) GetEffectiveContextTokensBefore(explore bool) int {
	if cc.ContextTokensBefore == nil {
		return 0
	}
	if explore {
		return cc.ContextTokensBefore.Sample()
	}
	return cc.ContextTokensBefore.Mean()
}

// GetEffectiveContextTokensAfter returns the amount of following context to include.
// If explore is true, samples from the learned distribution.
// If explore is false, returns the mean.
func (cc *ChunkConfig) GetEffectiveContextTokensAfter(explore bool) int {
	if cc.ContextTokensAfter == nil {
		return 0
	}
	if explore {
		return cc.ContextTokensAfter.Sample()
	}
	return cc.ContextTokensAfter.Mean()
}

// GetEffectiveOverflowStrategy returns the overflow strategy to use.
// If explore is true, samples from the learned weights (Thompson Sampling).
// If explore is false, returns the best known strategy (exploitation).
func (cc *ChunkConfig) GetEffectiveOverflowStrategy(explore bool) OverflowStrategy {
	if cc.OverflowStrategyWeights == nil {
		return StrategyRecursive
	}
	if explore {
		return cc.OverflowStrategyWeights.Sample()
	}
	return cc.OverflowStrategyWeights.BestStrategy()
}

// =============================================================================
// PriorChunkConfig Function (CK.1.4)
// =============================================================================

// PriorChunkConfig returns a ChunkConfig with weakly informative priors
// tailored to the specific domain. These priors serve as sensible starting
// points with high uncertainty, allowing the system to quickly adapt to
// actual usage patterns.
//
// Domain-specific characteristics:
//   - DomainCode: Prefers smaller chunks (300 tokens), high staleness weight
//     for code recency, recursive overflow for structural preservation
//   - DomainAcademic: Prefers larger chunks (500 tokens) for concept completeness,
//     sentence overflow to preserve argument structure
//   - DomainHistory: Medium chunks (400 tokens), low context after (sequential reading),
//     moderate context before for narrative flow
//   - DomainGeneral: Balanced defaults suitable for mixed content
func PriorChunkConfig(maxTokens int, domain Domain) (*ChunkConfig, error) {
	if maxTokens <= 0 {
		return nil, fmt.Errorf("max tokens must be positive, got %d", maxTokens)
	}

	var (
		targetMean    float64
		targetVar     float64
		minMean       float64
		minVar        float64
		beforeMean    float64
		beforeVar     float64
		afterMean     float64
		afterVar      float64
		recursivePrior float64
		sentencePrior  float64
		truncatePrior  float64
	)

	switch domain {
	case DomainCode:
		targetMean = 300
		targetVar = 150
		minMean = 50
		minVar = 25
		beforeMean = 100
		beforeVar = 75
		afterMean = 50
		afterVar = 35
		recursivePrior = 5.0
		sentencePrior = 1.0
		truncatePrior = 1.0

	case DomainAcademic:
		targetMean = 500
		targetVar = 200
		minMean = 100
		minVar = 50
		beforeMean = 150
		beforeVar = 100
		afterMean = 100
		afterVar = 75
		recursivePrior = 1.0
		sentencePrior = 5.0
		truncatePrior = 1.0

	case DomainHistory:
		targetMean = 400
		targetVar = 175
		minMean = 75
		minVar = 40
		beforeMean = 200
		beforeVar = 125
		afterMean = 30
		afterVar = 20
		recursivePrior = 2.0
		sentencePrior = 3.0
		truncatePrior = 1.0

	case DomainGeneral:
		fallthrough
	default:
		targetMean = float64(maxTokens) / 2
		targetVar = float64(maxTokens) / 4
		minMean = float64(maxTokens) / 10
		minVar = float64(maxTokens) / 20
		beforeMean = 100
		beforeVar = 50
		afterMean = 50
		afterVar = 25
		recursivePrior = 2.0
		sentencePrior = 2.0
		truncatePrior = 2.0
	}

	targetTokens, err := NewLearnedContextSize(targetMean, targetVar)
	if err != nil {
		return nil, fmt.Errorf("failed to create target tokens: %w", err)
	}

	minTokens, err := NewLearnedContextSize(minMean, minVar)
	if err != nil {
		return nil, fmt.Errorf("failed to create min tokens: %w", err)
	}

	contextBefore, err := NewLearnedContextSize(beforeMean, beforeVar)
	if err != nil {
		return nil, fmt.Errorf("failed to create context before: %w", err)
	}

	contextAfter, err := NewLearnedContextSize(afterMean, afterVar)
	if err != nil {
		return nil, fmt.Errorf("failed to create context after: %w", err)
	}

	overflowWeights, err := NewLearnedOverflowWeightsWithPriors(
		recursivePrior,
		sentencePrior,
		truncatePrior,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create overflow weights: %w", err)
	}

	return &ChunkConfig{
		MaxTokens:               maxTokens,
		TargetTokens:            targetTokens,
		MinTokens:               minTokens,
		ContextTokensBefore:     contextBefore,
		ContextTokensAfter:      contextAfter,
		OverflowStrategyWeights: overflowWeights,
	}, nil
}
