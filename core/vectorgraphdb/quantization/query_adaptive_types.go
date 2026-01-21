package quantization

import (
	"errors"
	"fmt"
)

// =============================================================================
// Query-Adaptive Quantization Types
// =============================================================================
//
// Query-adaptive weighting improves distance computation by weighting subspaces
// based on query characteristics. From IRISA research on query-adaptive ANN.
//
// Mechanism:
//   - Compute per-subspace variance from query vector
//   - Weight distance contributions by variance (high variance = more important)
//   - ~1% recall improvement with negligible latency overhead

// =============================================================================
// SubspaceWeights Type
// =============================================================================

// SubspaceWeights holds per-subspace weighting factors for distance computation.
// Weights are normalized to sum to 1.0.
type SubspaceWeights []float32

// NewSubspaceWeights creates uniform weights for the given number of subspaces.
func NewSubspaceWeights(numSubspaces int) SubspaceWeights {
	if numSubspaces <= 0 {
		return nil
	}
	weights := make(SubspaceWeights, numSubspaces)
	uniform := float32(1.0) / float32(numSubspaces)
	for i := range weights {
		weights[i] = uniform
	}
	return weights
}

// NumSubspaces returns the number of subspaces.
func (w SubspaceWeights) NumSubspaces() int {
	return len(w)
}

// Clone returns a deep copy of the weights.
func (w SubspaceWeights) Clone() SubspaceWeights {
	if w == nil {
		return nil
	}
	clone := make(SubspaceWeights, len(w))
	copy(clone, w)
	return clone
}

// Equal returns true if both weight slices are identical.
func (w SubspaceWeights) Equal(other SubspaceWeights) bool {
	if len(w) != len(other) {
		return false
	}
	for i := range w {
		if w[i] != other[i] {
			return false
		}
	}
	return true
}

// Sum returns the sum of all weights.
func (w SubspaceWeights) Sum() float32 {
	var sum float32
	for _, v := range w {
		sum += v
	}
	return sum
}

// IsNormalized returns true if weights sum to approximately 1.0.
func (w SubspaceWeights) IsNormalized() bool {
	sum := w.Sum()
	return sum > 0.99 && sum < 1.01
}

// Normalize adjusts weights to sum to 1.0.
func (w SubspaceWeights) Normalize() {
	sum := w.Sum()
	if sum == 0 || len(w) == 0 {
		return
	}
	for i := range w {
		w[i] /= sum
	}
}

// String returns a string representation for debugging.
func (w SubspaceWeights) String() string {
	if w == nil {
		return "SubspaceWeights(nil)"
	}
	if len(w) <= 4 {
		return fmt.Sprintf("SubspaceWeights%v", []float32(w))
	}
	return fmt.Sprintf("SubspaceWeights[%d]{%.3f,%.3f,...,%.3f,%.3f}",
		len(w), w[0], w[1], w[len(w)-2], w[len(w)-1])
}

// =============================================================================
// WeightingStrategy
// =============================================================================

// WeightingStrategy defines how subspace weights are computed.
type WeightingStrategy int

const (
	// WeightingUniform applies equal weights to all subspaces.
	// Fastest but no query-adaptive benefit.
	WeightingUniform WeightingStrategy = iota

	// WeightingVariance weights subspaces by query subvector variance.
	// Higher variance = more discriminative = higher weight.
	WeightingVariance

	// WeightingEntropy weights subspaces by query subvector entropy.
	// Higher entropy = more information = higher weight.
	WeightingEntropy

	// WeightingLearned uses pre-learned weights from training data.
	// Requires offline computation but can capture dataset-specific patterns.
	WeightingLearned
)

// String returns a string representation.
func (s WeightingStrategy) String() string {
	switch s {
	case WeightingUniform:
		return "Uniform"
	case WeightingVariance:
		return "Variance"
	case WeightingEntropy:
		return "Entropy"
	case WeightingLearned:
		return "Learned"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

// =============================================================================
// QueryProfile
// =============================================================================

// QueryProfile captures characteristics of a query vector for adaptive weighting.
type QueryProfile struct {
	// VectorDimension is the original vector dimension.
	VectorDimension int

	// NumSubspaces is the number of PQ subspaces.
	NumSubspaces int

	// SubspaceVariances holds variance of each subspace in the query.
	SubspaceVariances []float32

	// TotalVariance is the sum of all subspace variances.
	TotalVariance float32

	// Weights holds the computed subspace weights.
	Weights SubspaceWeights

	// Strategy is the weighting strategy used.
	Strategy WeightingStrategy
}

// NewQueryProfile creates an empty QueryProfile for the given dimensions.
func NewQueryProfile(vectorDim, numSubspaces int) *QueryProfile {
	if vectorDim <= 0 || numSubspaces <= 0 || vectorDim%numSubspaces != 0 {
		return nil
	}
	return &QueryProfile{
		VectorDimension:   vectorDim,
		NumSubspaces:      numSubspaces,
		SubspaceVariances: make([]float32, numSubspaces),
		Weights:           NewSubspaceWeights(numSubspaces),
		Strategy:          WeightingUniform,
	}
}

// SubspaceDimension returns the dimension of each subspace.
func (p *QueryProfile) SubspaceDimension() int {
	if p.NumSubspaces == 0 {
		return 0
	}
	return p.VectorDimension / p.NumSubspaces
}

// ComputeFromQuery analyzes a query vector and computes profile data.
func (p *QueryProfile) ComputeFromQuery(query []float32, strategy WeightingStrategy) error {
	if p == nil {
		return ErrQueryAdaptiveNilProfile
	}
	if len(query) != p.VectorDimension {
		return ErrQueryAdaptiveDimensionMismatch
	}

	p.Strategy = strategy
	subDim := p.SubspaceDimension()

	p.TotalVariance = 0
	for m := 0; m < p.NumSubspaces; m++ {
		start := m * subDim
		subvec := query[start : start+subDim]

		variance := computeVariance(subvec)
		p.SubspaceVariances[m] = variance
		p.TotalVariance += variance
	}

	switch strategy {
	case WeightingUniform:
		p.Weights = NewSubspaceWeights(p.NumSubspaces)
	case WeightingVariance:
		p.computeVarianceWeights()
	case WeightingEntropy:
		p.computeEntropyWeights(query)
	case WeightingLearned:
		return ErrQueryAdaptiveLearnedNotSupported
	}

	return nil
}

func (p *QueryProfile) computeVarianceWeights() {
	if p.TotalVariance == 0 {
		p.Weights = NewSubspaceWeights(p.NumSubspaces)
		return
	}
	for m := 0; m < p.NumSubspaces; m++ {
		p.Weights[m] = p.SubspaceVariances[m] / p.TotalVariance
	}
}

func (p *QueryProfile) computeEntropyWeights(query []float32) {
	subDim := p.SubspaceDimension()
	totalEntropy := float32(0)

	for m := 0; m < p.NumSubspaces; m++ {
		start := m * subDim
		subvec := query[start : start+subDim]
		entropy := computeEntropy(subvec)
		p.Weights[m] = entropy
		totalEntropy += entropy
	}

	if totalEntropy > 0 {
		for m := range p.Weights {
			p.Weights[m] /= totalEntropy
		}
	} else {
		p.Weights = NewSubspaceWeights(p.NumSubspaces)
	}
}

// String returns a string representation for debugging.
func (p *QueryProfile) String() string {
	if p == nil {
		return "QueryProfile(nil)"
	}
	return fmt.Sprintf("QueryProfile{Dim:%d, Subs:%d, Strategy:%s, TotalVar:%.4f}",
		p.VectorDimension, p.NumSubspaces, p.Strategy, p.TotalVariance)
}

// computeVariance computes variance of a float32 slice.
func computeVariance(values []float32) float32 {
	if len(values) == 0 {
		return 0
	}

	var sum float32
	for _, v := range values {
		sum += v
	}
	mean := sum / float32(len(values))

	var variance float32
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	return variance / float32(len(values))
}

// computeEntropy computes entropy of values (treating as probability distribution).
func computeEntropy(values []float32) float32 {
	if len(values) == 0 {
		return 0
	}

	var sum float32
	for _, v := range values {
		if v < 0 {
			v = -v
		}
		sum += v
	}
	if sum == 0 {
		return 0
	}

	var entropy float32
	for _, v := range values {
		if v < 0 {
			v = -v
		}
		if v > 0 {
			p := v / sum
			entropy -= p * log2(p)
		}
	}
	return entropy
}

// log2 computes log base 2 using natural log.
func log2(x float32) float32 {
	if x <= 0 {
		return 0
	}
	return float32(1.4426950408889634) * ln(x)
}

// ln computes natural logarithm using Taylor series (good enough for entropy).
func ln(x float32) float32 {
	if x <= 0 {
		return 0
	}
	if x == 1 {
		return 0
	}

	y := (x - 1) / (x + 1)
	y2 := y * y
	result := y
	term := y
	for i := 3; i < 20; i += 2 {
		term *= y2
		result += term / float32(i)
	}
	return 2 * result
}

// =============================================================================
// QueryAdaptiveConfig
// =============================================================================

// QueryAdaptiveConfig configures query-adaptive distance computation.
type QueryAdaptiveConfig struct {
	// Enabled controls whether query-adaptive weighting is used.
	// Default: true.
	Enabled bool

	// Strategy is the weighting strategy to use.
	// Default: WeightingVariance.
	Strategy WeightingStrategy

	// MinVarianceRatio is the minimum variance ratio to apply weighting.
	// If max/min subspace variance < this, use uniform weights.
	// Prevents over-weighting when all subspaces are similar.
	// Default: 2.0 (at least 2x difference to apply weighting).
	MinVarianceRatio float32
}

// DefaultQueryAdaptiveConfig returns a QueryAdaptiveConfig with default values.
func DefaultQueryAdaptiveConfig() QueryAdaptiveConfig {
	return QueryAdaptiveConfig{
		Enabled:          true,
		Strategy:         WeightingVariance,
		MinVarianceRatio: 2.0,
	}
}

// Validate checks that the configuration is valid.
func (c QueryAdaptiveConfig) Validate() error {
	if c.MinVarianceRatio < 1.0 {
		return ErrQueryAdaptiveInvalidRatio
	}
	return nil
}

// String returns a string representation.
func (c QueryAdaptiveConfig) String() string {
	return fmt.Sprintf("QueryAdaptiveConfig{Enabled:%t, Strategy:%s, MinRatio:%.2f}",
		c.Enabled, c.Strategy, c.MinVarianceRatio)
}

// =============================================================================
// Query-Adaptive Errors
// =============================================================================

var (
	// ErrQueryAdaptiveNilProfile indicates a nil profile was provided.
	ErrQueryAdaptiveNilProfile = errors.New("query_adaptive: nil profile")

	// ErrQueryAdaptiveDimensionMismatch indicates query dimension doesn't match profile.
	ErrQueryAdaptiveDimensionMismatch = errors.New("query_adaptive: dimension mismatch")

	// ErrQueryAdaptiveInvalidRatio indicates min variance ratio is too low.
	ErrQueryAdaptiveInvalidRatio = errors.New("query_adaptive: min variance ratio must be >= 1.0")

	// ErrQueryAdaptiveLearnedNotSupported indicates learned weights are not yet implemented.
	ErrQueryAdaptiveLearnedNotSupported = errors.New("query_adaptive: learned weights not yet supported")
)
