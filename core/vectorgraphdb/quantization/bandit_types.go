package quantization

import (
	"fmt"
	"time"
)

// Thompson Sampling Bandit for Quantization Parameter Learning.
// Instead of hardcoding magic numbers, we learn optimal values per-codebase.
// Each parameter is a multi-armed bandit with Beta(α, β) posteriors.
// Reward: recall >= target AND latency < budget → 1, else 0.

// ArmID identifies a tunable parameter.
type ArmID string

const (
	// ArmCandidateMultiplier controls k*X for candidate overselection in search.
	// Higher = better recall but slower. Default exploration: {2, 4, 8, 16}.
	ArmCandidateMultiplier ArmID = "candidate_multiplier"

	// ArmSubspaceDimTarget controls VectorDimension/X for PQ subspace sizing.
	// Lower = more subspaces = better recall but larger codes. Default: {16, 24, 32, 48}.
	ArmSubspaceDimTarget ArmID = "subspace_dim_target"

	// ArmMinSubspaces is the floor for number of subspaces.
	// Default exploration: {2, 4, 8}.
	ArmMinSubspaces ArmID = "min_subspaces"

	// ArmCentroidsPerSubspace controls codebook size per subspace.
	// Higher = better recall but more memory/training time. Default: {64, 128, 256}.
	ArmCentroidsPerSubspace ArmID = "centroids_per_subspace"

	// ArmSplitPerturbation controls perturbation factor when splitting overloaded centroids.
	// Default exploration: {0.005, 0.01, 0.02, 0.05}.
	ArmSplitPerturbation ArmID = "split_perturbation"

	// ArmReencodeTimeoutMinutes controls timeout for vector re-encoding after codebook swap.
	// Default exploration: {1, 5, 15}.
	ArmReencodeTimeoutMinutes ArmID = "reencode_timeout_minutes"

	// ArmLOPQCodeSizeBytes controls estimated bytes per LOPQ code for memory calculations.
	// Default exploration: {16, 32, 48, 64}.
	ArmLOPQCodeSizeBytes ArmID = "lopq_code_size_bytes"

	// ArmStrategyExactThreshold controls the vector count threshold below which
	// StrategyExact (no compression) is used. For tiny datasets, exact search
	// is faster than PQ overhead. Default exploration: {50, 100, 200, 500}.
	ArmStrategyExactThreshold ArmID = "strategy_exact_threshold"

	// ArmStrategyCoarseThreshold controls the vector count threshold below which
	// StrategyCoarsePQRerank is used. For small datasets, coarse PQ + re-ranking
	// provides best recall/speed tradeoff. Default exploration: {500, 1000, 2000, 5000}.
	ArmStrategyCoarseThreshold ArmID = "strategy_coarse_threshold"

	// ArmStrategyMediumThreshold controls the vector count threshold below which
	// StrategyStandardPQ or StrategyResidualPQ is used (depending on config).
	// Above this threshold, full residual PQ is always used for best accuracy.
	// Default exploration: {50000, 100000, 200000, 500000}.
	ArmStrategyMediumThreshold ArmID = "strategy_medium_threshold"
)

// AllArms returns all defined arm IDs.
func AllArms() []ArmID {
	return []ArmID{
		ArmCandidateMultiplier,
		ArmSubspaceDimTarget,
		ArmMinSubspaces,
		ArmCentroidsPerSubspace,
		ArmSplitPerturbation,
		ArmReencodeTimeoutMinutes,
		ArmLOPQCodeSizeBytes,
		ArmStrategyExactThreshold,
		ArmStrategyCoarseThreshold,
		ArmStrategyMediumThreshold,
	}
}

// ArmChoice represents a discrete choice for an arm with its value.
type ArmChoice struct {
	Index int         // Index in the choices array (0-based)
	Value interface{} // The actual value (int, float32, etc.)
}

// ArmDefinition defines an arm with its possible choices.
type ArmDefinition struct {
	ID      ArmID
	Choices []interface{} // Discrete values to choose from
}

// DefaultArmDefinitions returns the arm definitions with their choice sets.
func DefaultArmDefinitions() map[ArmID]ArmDefinition {
	return map[ArmID]ArmDefinition{
		ArmCandidateMultiplier: {
			ID:      ArmCandidateMultiplier,
			Choices: []interface{}{2, 4, 8, 16},
		},
		ArmSubspaceDimTarget: {
			ID:      ArmSubspaceDimTarget,
			Choices: []interface{}{16, 24, 32, 48},
		},
		ArmMinSubspaces: {
			ID:      ArmMinSubspaces,
			Choices: []interface{}{2, 4, 8},
		},
		ArmCentroidsPerSubspace: {
			ID:      ArmCentroidsPerSubspace,
			Choices: []interface{}{64, 128, 256},
		},
		ArmSplitPerturbation: {
			ID:      ArmSplitPerturbation,
			Choices: []interface{}{float32(0.005), float32(0.01), float32(0.02), float32(0.05)},
		},
		ArmReencodeTimeoutMinutes: {
			ID:      ArmReencodeTimeoutMinutes,
			Choices: []interface{}{1, 5, 15},
		},
		ArmLOPQCodeSizeBytes: {
			ID:      ArmLOPQCodeSizeBytes,
			Choices: []interface{}{16, 32, 48, 64},
		},
		ArmStrategyExactThreshold: {
			ID:      ArmStrategyExactThreshold,
			Choices: []interface{}{50, 100, 200, 500},
		},
		ArmStrategyCoarseThreshold: {
			ID:      ArmStrategyCoarseThreshold,
			Choices: []interface{}{500, 1000, 2000, 5000},
		},
		ArmStrategyMediumThreshold: {
			ID:      ArmStrategyMediumThreshold,
			Choices: []interface{}{50000, 100000, 200000, 500000},
		},
	}
}

// BetaPosterior represents Beta(α, β) for Thompson sampling.
// α = successes + 1, β = failures + 1. Beta(1,1) = uniform prior.
type BetaPosterior struct {
	Alpha float64 // α = successes + 1 (starts at 1 for uniform prior)
	Beta  float64 // β = failures + 1 (starts at 1 for uniform prior)
}

// NewUniformPrior returns a Beta(1, 1) uniform prior.
func NewUniformPrior() BetaPosterior {
	return BetaPosterior{Alpha: 1.0, Beta: 1.0}
}

// Mean returns the expected value of the posterior: α / (α + β).
func (p BetaPosterior) Mean() float64 {
	return p.Alpha / (p.Alpha + p.Beta)
}

// Variance returns the variance of the posterior.
func (p BetaPosterior) Variance() float64 {
	sum := p.Alpha + p.Beta
	return (p.Alpha * p.Beta) / (sum * sum * (sum + 1))
}

// TotalObservations returns the number of observations (α - 1) + (β - 1).
func (p BetaPosterior) TotalObservations() int {
	return int(p.Alpha-1) + int(p.Beta-1)
}

// String returns a string representation.
func (p BetaPosterior) String() string {
	return fmt.Sprintf("Beta(α=%.1f, β=%.1f, mean=%.3f)", p.Alpha, p.Beta, p.Mean())
}

// BanditConfig holds sampled configuration values (already sampled from posteriors).
type BanditConfig struct {
	CandidateMultiplier     int           // k*X for candidate overselection
	SubspaceDimTarget       int           // VectorDimension / X for subspace sizing
	MinSubspaces            int           // Floor for number of subspaces
	CentroidsPerSubspace    int           // Codebook size per subspace
	SplitPerturbation       float32       // Perturbation for centroid splitting
	ReencodeTimeout         time.Duration // Timeout for vector re-encoding
	LOPQCodeSizeBytes       int           // Estimated bytes per LOPQ code
	StrategyExactThreshold  int           // Vector count below which StrategyExact is used
	StrategyCoarseThreshold int           // Vector count below which StrategyCoarsePQRerank is used
	StrategyMediumThreshold int           // Vector count below which StrategyStandardPQ/ResidualPQ is used
	SampledChoiceIndices    map[ArmID]int // Which choice index was sampled per arm
}

// DefaultBanditConfig returns a config with middle-of-the-road defaults.
// Used when bandit is not initialized or for deterministic fallback.
func DefaultBanditConfig() BanditConfig {
	return BanditConfig{
		CandidateMultiplier:     4,
		SubspaceDimTarget:       24,
		MinSubspaces:            4,
		CentroidsPerSubspace:    256,
		SplitPerturbation:       0.01,
		ReencodeTimeout:         5 * time.Minute,
		LOPQCodeSizeBytes:       32,
		StrategyExactThreshold:  100,
		StrategyCoarseThreshold: 1000,
		StrategyMediumThreshold: 100000,
		SampledChoiceIndices:    make(map[ArmID]int),
	}
}

// String returns a string representation.
func (c BanditConfig) String() string {
	return fmt.Sprintf("BanditConfig{CandMult:%d, SubDim:%d, MinSub:%d, Cents:%d, Pert:%.3f, Timeout:%v, LOPQ:%d, ExactThr:%d, CoarseThr:%d, MedThr:%d}",
		c.CandidateMultiplier, c.SubspaceDimTarget, c.MinSubspaces,
		c.CentroidsPerSubspace, c.SplitPerturbation, c.ReencodeTimeout, c.LOPQCodeSizeBytes,
		c.StrategyExactThreshold, c.StrategyCoarseThreshold, c.StrategyMediumThreshold)
}

// RewardOutcome represents the result of using a configuration.
type RewardOutcome struct {
	CodebaseID string       // Which codebase this outcome is for
	Config     BanditConfig // The config that was used
	Reward     float64      // 0.0 (failure) or 1.0 (success)
	RecordedAt time.Time    // When this outcome was recorded
	Recall     float64      // Observed recall (for debugging)
	LatencyMs  int64        // Observed latency in ms (for debugging)
}

// ContinuousReward holds measured search outcomes for computing continuous rewards.
// Unlike binary rewards (success/failure), continuous rewards preserve granularity:
// a 95% recall search updates differently than a 51% recall search.
type ContinuousReward struct {
	Recall          float64 // Measured recall, 0.0 to 1.0
	LatencyMs       int64   // Actual latency in milliseconds
	TargetRecall    float64 // What recall we were targeting (e.g., 0.9)
	LatencyBudgetMs int64   // Maximum acceptable latency in milliseconds
}

// ContinuousRewardOutcome extends RewardOutcome with continuous reward data.
type ContinuousRewardOutcome struct {
	CodebaseID    string           // Which codebase this outcome is for
	Config        BanditConfig     // The config that was used
	Continuous    ContinuousReward // Continuous reward measurements
	RecordedAt    time.Time        // When this outcome was recorded
	RecallWeight  float64          // Weight for recall component (default 0.7)
	LatencyWeight float64          // Weight for latency component (default 0.3)
}

// DefaultContinuousRewardWeights returns standard weights: 0.7 recall, 0.3 latency.
// Recall is weighted higher because correctness is more important than speed.
func DefaultContinuousRewardWeights() (recallWeight, latencyWeight float64) {
	return 0.7, 0.3
}

// ComputeReward calculates a continuous reward in [0, 1] based on how well
// search targets were met. The reward combines recall and latency components:
//
// Recall component (saturates at target):
//   - min(1.0, recall / targetRecall)
//   - Returns 1.0 if recall meets or exceeds target
//   - Returns proportionally less for lower recall
//
// Latency component:
//   - 1.0 if latency is within budget
//   - max(0, 1 - latency/budget) if over budget (linear decay to 0)
//
// Combined reward = recallWeight * recallScore + latencyWeight * latencyScore
//
// Edge cases:
//   - Zero target recall: returns 0.0 (invalid configuration)
//   - Zero latency budget: latency component is 0.0 if latency > 0, else 1.0
func ComputeReward(cr ContinuousReward, recallWeight, latencyWeight float64) float64 {
	// Handle invalid target recall
	if cr.TargetRecall <= 0 {
		return 0.0
	}

	// Recall component: saturates at target (meeting target = full score)
	recallScore := cr.Recall / cr.TargetRecall
	if recallScore > 1.0 {
		recallScore = 1.0
	}
	if recallScore < 0.0 {
		recallScore = 0.0
	}

	// Latency component: 1.0 if within budget, linear decay if over
	var latencyScore float64
	if cr.LatencyBudgetMs <= 0 {
		// Zero budget: only perfect (zero latency) gets full score
		if cr.LatencyMs <= 0 {
			latencyScore = 1.0
		} else {
			latencyScore = 0.0
		}
	} else if cr.LatencyMs <= cr.LatencyBudgetMs {
		// Within budget: full score
		latencyScore = 1.0
	} else {
		// Over budget: linear decay
		latencyScore = 1.0 - float64(cr.LatencyMs-cr.LatencyBudgetMs)/float64(cr.LatencyBudgetMs)
		if latencyScore < 0 {
			latencyScore = 0.0
		}
	}

	// Combine with weights
	return recallWeight*recallScore + latencyWeight*latencyScore
}

// Success returns true if reward indicates success.
func (o RewardOutcome) Success() bool {
	return o.Reward >= 0.5
}

// String returns a string representation.
func (o RewardOutcome) String() string {
	status := "FAIL"
	if o.Success() {
		status = "SUCCESS"
	}
	return fmt.Sprintf("Outcome{%s, recall=%.2f, latency=%dms}", status, o.Recall, o.LatencyMs)
}

// BanditPosteriorRow represents a row in the evq_bandit_posteriors table.
type BanditPosteriorRow struct {
	CodebaseID string
	ArmID      ArmID
	ChoiceIdx  int
	Alpha      float64
	Beta       float64
	UpdatedAt  time.Time
}

// AdaptationMode defines how new choices are generated when expanding arm ranges.
type AdaptationMode string

const (
	// AdaptationLinear generates evenly spaced choices between min and max.
	AdaptationLinear AdaptationMode = "linear"

	// AdaptationExponential generates choices as powers of 2 (or nearest power).
	AdaptationExponential AdaptationMode = "exponential"
)

// AdaptiveArmDefinition extends ArmDefinition with bounds and adaptation behavior.
// This allows arm choices to expand/contract based on where optimal values land.
// When the best-performing choice is consistently at an edge, the range expands
// in that direction to explore potentially better values.
type AdaptiveArmDefinition struct {
	ArmDefinition

	// MinValue is the absolute minimum bound for this arm (type matches Choices).
	MinValue interface{}

	// MaxValue is the absolute maximum bound for this arm (type matches Choices).
	MaxValue interface{}

	// AdaptationMode controls how new choices are generated when adapting.
	// "linear" = evenly spaced, "exponential" = powers of 2.
	AdaptationMode AdaptationMode

	// MinChoices is the minimum number of choices to maintain after contraction.
	// Must be >= 2 to allow meaningful exploration.
	MinChoices int
}

// DefaultAdaptiveArmDefinitions returns adaptive arm definitions with bounds.
// These extend the default definitions with min/max values and adaptation modes.
func DefaultAdaptiveArmDefinitions() map[ArmID]AdaptiveArmDefinition {
	return map[ArmID]AdaptiveArmDefinition{
		ArmCandidateMultiplier: {
			ArmDefinition: ArmDefinition{
				ID:      ArmCandidateMultiplier,
				Choices: []interface{}{2, 4, 8, 16},
			},
			MinValue:       1,
			MaxValue:       32,
			AdaptationMode: AdaptationExponential,
			MinChoices:     3,
		},
		ArmSubspaceDimTarget: {
			ArmDefinition: ArmDefinition{
				ID:      ArmSubspaceDimTarget,
				Choices: []interface{}{16, 24, 32, 48},
			},
			MinValue:       8,
			MaxValue:       64,
			AdaptationMode: AdaptationLinear,
			MinChoices:     3,
		},
		ArmCentroidsPerSubspace: {
			ArmDefinition: ArmDefinition{
				ID:      ArmCentroidsPerSubspace,
				Choices: []interface{}{64, 128, 256},
			},
			MinValue:       16,
			MaxValue:       256,
			AdaptationMode: AdaptationExponential,
			MinChoices:     3,
		},
		ArmSplitPerturbation: {
			ArmDefinition: ArmDefinition{
				ID:      ArmSplitPerturbation,
				Choices: []interface{}{float32(0.005), float32(0.01), float32(0.02), float32(0.05)},
			},
			MinValue:       float32(0.001),
			MaxValue:       float32(0.1),
			AdaptationMode: AdaptationExponential,
			MinChoices:     3,
		},
	}
}

// ShouldAdapt determines if arm choices should be adapted based on posteriors.
// Returns true when:
// - Total observations across all choices exceed minObservations threshold
// - The best choice (highest posterior mean) is at an edge (first or last)
// - The edge choice has a posterior mean >= edgeMeanThreshold (indicating confidence)
//
// This ensures adaptation only occurs when there's enough evidence that the
// optimal value might lie outside the current choice range.
func ShouldAdapt(posteriors []BetaPosterior) bool {
	const minObservations = 20
	const edgeMeanThreshold = 0.7

	if len(posteriors) < 2 {
		return false
	}

	// Count total observations across all choices
	totalObs := 0
	for _, p := range posteriors {
		totalObs += p.TotalObservations()
	}
	if totalObs < minObservations {
		return false
	}

	// Find the best choice (highest posterior mean) and check if it's at an edge
	bestIdx := 0
	bestMean := posteriors[0].Mean()
	for i, p := range posteriors {
		mean := p.Mean()
		if mean > bestMean {
			bestMean = mean
			bestIdx = i
		}
	}

	// Check if best choice is at edge with sufficient confidence
	isAtEdge := bestIdx == 0 || bestIdx == len(posteriors)-1
	hasConfidence := bestMean >= edgeMeanThreshold

	return isAtEdge && hasConfidence
}

// AdaptArmChoices generates a new choice array based on posterior performance.
// The algorithm:
// 1. If the best choice is at the lower edge, expand downward toward MinValue
// 2. If the best choice is at the upper edge, expand upward toward MaxValue
// 3. If middle choices are rarely selected, contract the range
// 4. Respects MinChoices as the floor for number of choices
//
// Returns the new choices array, or the original if no adaptation is needed.
func AdaptArmChoices(def AdaptiveArmDefinition, posteriors []BetaPosterior) []interface{} {
	if len(posteriors) != len(def.Choices) || len(posteriors) < 2 {
		return def.Choices
	}

	// Find best choice index
	bestIdx := 0
	bestMean := posteriors[0].Mean()
	for i, p := range posteriors {
		mean := p.Mean()
		if mean > bestMean {
			bestMean = mean
			bestIdx = i
		}
	}

	// Determine direction: -1 = expand lower, +1 = expand upper, 0 = contract
	direction := 0
	if bestIdx == 0 {
		direction = -1
	} else if bestIdx == len(posteriors)-1 {
		direction = 1
	}

	// Generate new choices based on direction and type
	switch def.Choices[0].(type) {
	case int:
		return adaptIntChoices(def, posteriors, direction)
	case float32:
		return adaptFloat32Choices(def, posteriors, direction)
	default:
		return def.Choices
	}
}

// adaptIntChoices handles adaptation for integer-valued arms.
func adaptIntChoices(def AdaptiveArmDefinition, posteriors []BetaPosterior, direction int) []interface{} {
	minVal := def.MinValue.(int)
	maxVal := def.MaxValue.(int)
	choices := make([]int, len(def.Choices))
	for i, c := range def.Choices {
		choices[i] = c.(int)
	}

	var newChoices []int

	if direction == -1 {
		// Expand lower: add a choice below current minimum
		newMin := generateLowerInt(choices[0], minVal, def.AdaptationMode)
		if newMin < choices[0] {
			newChoices = append([]int{newMin}, choices...)
		} else {
			newChoices = choices
		}
	} else if direction == 1 {
		// Expand upper: add a choice above current maximum
		newMax := generateUpperInt(choices[len(choices)-1], maxVal, def.AdaptationMode)
		if newMax > choices[len(choices)-1] {
			newChoices = append(choices, newMax)
		} else {
			newChoices = choices
		}
	} else {
		// Contract: remove underperforming middle choices
		newChoices = contractIntChoices(choices, posteriors, def.MinChoices)
	}

	// Convert back to interface slice
	result := make([]interface{}, len(newChoices))
	for i, v := range newChoices {
		result[i] = v
	}
	return result
}

// adaptFloat32Choices handles adaptation for float32-valued arms.
func adaptFloat32Choices(def AdaptiveArmDefinition, posteriors []BetaPosterior, direction int) []interface{} {
	minVal := def.MinValue.(float32)
	maxVal := def.MaxValue.(float32)
	choices := make([]float32, len(def.Choices))
	for i, c := range def.Choices {
		choices[i] = c.(float32)
	}

	var newChoices []float32

	if direction == -1 {
		// Expand lower: add a choice below current minimum
		newMin := generateLowerFloat32(choices[0], minVal, def.AdaptationMode)
		if newMin < choices[0] {
			newChoices = append([]float32{newMin}, choices...)
		} else {
			newChoices = choices
		}
	} else if direction == 1 {
		// Expand upper: add a choice above current maximum
		newMax := generateUpperFloat32(choices[len(choices)-1], maxVal, def.AdaptationMode)
		if newMax > choices[len(choices)-1] {
			newChoices = append(choices, newMax)
		} else {
			newChoices = choices
		}
	} else {
		// Contract: remove underperforming middle choices
		newChoices = contractFloat32Choices(choices, posteriors, def.MinChoices)
	}

	// Convert back to interface slice
	result := make([]interface{}, len(newChoices))
	for i, v := range newChoices {
		result[i] = v
	}
	return result
}

// generateLowerInt generates a new lower value for expansion.
func generateLowerInt(current, min int, mode AdaptationMode) int {
	var newVal int
	switch mode {
	case AdaptationExponential:
		// Halve the current value
		newVal = current / 2
	default: // AdaptationLinear
		// Subtract a quarter of the current value
		newVal = current - (current / 4)
		if newVal == current {
			newVal = current - 1
		}
	}
	if newVal < min {
		newVal = min
	}
	return newVal
}

// generateUpperInt generates a new upper value for expansion.
func generateUpperInt(current, max int, mode AdaptationMode) int {
	var newVal int
	switch mode {
	case AdaptationExponential:
		// Double the current value
		newVal = current * 2
	default: // AdaptationLinear
		// Add a quarter of the current value
		delta := current / 4
		if delta == 0 {
			delta = 1
		}
		newVal = current + delta
	}
	if newVal > max {
		newVal = max
	}
	return newVal
}

// generateLowerFloat32 generates a new lower value for float32 expansion.
func generateLowerFloat32(current, min float32, mode AdaptationMode) float32 {
	var newVal float32
	switch mode {
	case AdaptationExponential:
		// Halve the current value
		newVal = current / 2
	default: // AdaptationLinear
		// Subtract a quarter of the current value
		newVal = current - (current / 4)
	}
	if newVal < min {
		newVal = min
	}
	return newVal
}

// generateUpperFloat32 generates a new upper value for float32 expansion.
func generateUpperFloat32(current, max float32, mode AdaptationMode) float32 {
	var newVal float32
	switch mode {
	case AdaptationExponential:
		// Double the current value
		newVal = current * 2
	default: // AdaptationLinear
		// Add a quarter of the current value
		newVal = current + (current / 4)
	}
	if newVal > max {
		newVal = max
	}
	return newVal
}

// contractIntChoices removes underperforming middle choices.
// A choice is considered underperforming if its posterior mean is significantly
// below the best choice and it's not at an edge.
func contractIntChoices(choices []int, posteriors []BetaPosterior, minChoices int) []int {
	if len(choices) <= minChoices {
		return choices
	}

	// Find the choice with lowest mean that's not at an edge
	worstMiddleIdx := -1
	worstMiddleMean := 1.0
	for i := 1; i < len(posteriors)-1; i++ {
		mean := posteriors[i].Mean()
		// Only consider choices with enough observations to be meaningful
		if posteriors[i].TotalObservations() >= 3 && mean < worstMiddleMean {
			worstMiddleMean = mean
			worstMiddleIdx = i
		}
	}

	// Only contract if the worst middle choice is significantly worse than edges
	if worstMiddleIdx == -1 {
		return choices
	}
	edgeMean := (posteriors[0].Mean() + posteriors[len(posteriors)-1].Mean()) / 2
	if worstMiddleMean >= edgeMean*0.5 {
		// Not significantly worse, don't contract
		return choices
	}

	// Remove the worst middle choice
	result := make([]int, 0, len(choices)-1)
	for i, c := range choices {
		if i != worstMiddleIdx {
			result = append(result, c)
		}
	}
	return result
}

// contractFloat32Choices removes underperforming middle choices for float32 arms.
func contractFloat32Choices(choices []float32, posteriors []BetaPosterior, minChoices int) []float32 {
	if len(choices) <= minChoices {
		return choices
	}

	// Find the choice with lowest mean that's not at an edge
	worstMiddleIdx := -1
	worstMiddleMean := 1.0
	for i := 1; i < len(posteriors)-1; i++ {
		mean := posteriors[i].Mean()
		// Only consider choices with enough observations to be meaningful
		if posteriors[i].TotalObservations() >= 3 && mean < worstMiddleMean {
			worstMiddleMean = mean
			worstMiddleIdx = i
		}
	}

	// Only contract if the worst middle choice is significantly worse than edges
	if worstMiddleIdx == -1 {
		return choices
	}
	edgeMean := (posteriors[0].Mean() + posteriors[len(posteriors)-1].Mean()) / 2
	if worstMiddleMean >= edgeMean*0.5 {
		// Not significantly worse, don't contract
		return choices
	}

	// Remove the worst middle choice
	result := make([]float32, 0, len(choices)-1)
	for i, c := range choices {
		if i != worstMiddleIdx {
			result = append(result, c)
		}
	}
	return result
}
