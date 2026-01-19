package handoff

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
)

// =============================================================================
// Learned Parameters - Bayesian Learning for Handoff Decisions
// =============================================================================

// UpdateConfig controls how LearnedWeight updates blend new observations
// with existing beliefs. Uses exponential decay and drift protection.
type UpdateConfig struct {
	// LearningRate controls how much weight new observations receive (0.0-1.0).
	// Higher values make the model adapt faster but may be less stable.
	LearningRate float64 `json:"learning_rate"`

	// DriftThreshold is the confidence level above which we blend toward prior
	// to prevent overconfidence. Typically 0.95-0.99.
	DriftThreshold float64 `json:"drift_threshold"`

	// PriorBlendRate controls how strongly we pull back to prior when drift
	// protection activates. Typically 0.1-0.3.
	PriorBlendRate float64 `json:"prior_blend_rate"`
}

// DefaultUpdateConfig returns sensible defaults for most use cases.
func DefaultUpdateConfig() *UpdateConfig {
	return &UpdateConfig{
		LearningRate:   0.1,
		DriftThreshold: 0.95,
		PriorBlendRate: 0.2,
	}
}

// LearnedWeight represents a learned parameter using a Beta distribution.
// Suitable for learning weights and thresholds in the range [0,1].
//
// The Beta distribution is parameterized by Alpha and Beta:
//   - Higher Alpha increases the mean
//   - Higher Beta decreases the mean
//   - Higher (Alpha + Beta) means more confidence/less variance
//
// Example: Learning a success rate that starts at 0.5 with low confidence:
//
//	w := NewLearnedWeight(1.0, 1.0)  // Uniform prior
//	w.Update(1.0, config)            // Observe success
//	w.Update(0.0, config)            // Observe failure
//	mean := w.Mean()                 // Current best estimate
type LearnedWeight struct {
	// Alpha is the first shape parameter of the Beta distribution.
	// Represents the count of "successes" (observations near 1.0).
	Alpha float64 `json:"alpha"`

	// Beta is the second shape parameter of the Beta distribution.
	// Represents the count of "failures" (observations near 0.0).
	Beta float64 `json:"beta"`

	// EffectiveSamples tracks the effective number of observations seen,
	// accounting for exponential decay from the learning rate.
	EffectiveSamples float64 `json:"effective_samples"`

	// PriorAlpha stores the original Alpha value for drift protection.
	PriorAlpha float64 `json:"prior_alpha"`

	// PriorBeta stores the original Beta value for drift protection.
	PriorBeta float64 `json:"prior_beta"`
}

// NewLearnedWeight creates a LearnedWeight with the given prior parameters.
// Common priors:
//   - (1, 1): Uniform distribution over [0,1] - no prior belief
//   - (2, 2): Weak belief centered at 0.5
//   - (5, 5): Moderate belief centered at 0.5
//   - (a, b) where a/(a+b) = desired mean: Custom prior belief
func NewLearnedWeight(alpha, beta float64) *LearnedWeight {
	return &LearnedWeight{
		Alpha:            alpha,
		Beta:             beta,
		EffectiveSamples: alpha + beta,
		PriorAlpha:       alpha,
		PriorBeta:        beta,
	}
}

// Mean returns the expected value of the Beta distribution.
// This is the best point estimate for the learned parameter.
func (lw *LearnedWeight) Mean() float64 {
	return lw.Alpha / (lw.Alpha + lw.Beta)
}

// Variance returns the variance of the Beta distribution.
// Lower variance indicates higher confidence in the learned value.
func (lw *LearnedWeight) Variance() float64 {
	sum := lw.Alpha + lw.Beta
	denom := sum * sum * (sum + 1.0)
	return (lw.Alpha * lw.Beta) / denom
}

// Confidence returns a measure of confidence in the learned value.
// Returns a value in [0,1] where 1.0 means very confident.
// Based on the precision (Alpha + Beta) relative to a reference scale.
func (lw *LearnedWeight) Confidence() float64 {
	precision := lw.Alpha + lw.Beta
	if precision < 2.0 {
		return 0.0
	}
	return 1.0 - math.Exp(-precision/10.0)
}

// CredibleInterval returns the Bayesian credible interval at probability p.
// For example, p=0.95 gives the 95% credible interval [lower, upper].
// This uses a quantile approximation based on the normal distribution.
func (lw *LearnedWeight) CredibleInterval(p float64) (float64, float64) {
	if p <= 0.0 || p >= 1.0 {
		return 0.0, 1.0
	}

	mean := lw.Mean()
	variance := lw.Variance()
	stddev := math.Sqrt(variance)

	zScore := approximateQuantile((1.0 + p) / 2.0)

	lower := mean - zScore*stddev
	upper := mean + zScore*stddev

	lower = math.Max(0.0, math.Min(1.0, lower))
	upper = math.Max(0.0, math.Min(1.0, upper))

	return lower, upper
}

// Sample generates a random sample from the Beta distribution.
// Uses the ratio-of-uniforms method for efficiency.
func (lw *LearnedWeight) Sample() float64 {
	return sampleBeta(lw.Alpha, lw.Beta)
}

// Update incorporates a new observation using exponential decay.
// observation should be in [0,1].
// The update blends the observation with current parameters using the
// learning rate, and applies drift protection if confidence is too high.
func (lw *LearnedWeight) Update(observation float64, config *UpdateConfig) {
	if config == nil {
		config = DefaultUpdateConfig()
	}

	observation = clamp(observation, 0.0, 1.0)
	lr := clamp(config.LearningRate, 0.0, 1.0)

	alphaIncrement := observation * lr
	betaIncrement := (1.0 - observation) * lr

	lw.Alpha = lw.Alpha*(1.0-lr) + alphaIncrement
	lw.Beta = lw.Beta*(1.0-lr) + betaIncrement
	lw.EffectiveSamples = lw.EffectiveSamples*(1.0-lr) + lr

	lw.applyDriftProtection(config)
}

// applyDriftProtection blends parameters back toward prior if confidence
// exceeds the drift threshold. Prevents overconfidence.
func (lw *LearnedWeight) applyDriftProtection(config *UpdateConfig) {
	confidence := lw.Confidence()
	if confidence > config.DriftThreshold {
		blendRate := config.PriorBlendRate
		lw.Alpha = lw.Alpha*(1.0-blendRate) + lw.PriorAlpha*blendRate
		lw.Beta = lw.Beta*(1.0-blendRate) + lw.PriorBeta*blendRate
	}
}

// MarshalJSON implements json.Marshaler.
func (lw *LearnedWeight) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]float64{
		"alpha":             lw.Alpha,
		"beta":              lw.Beta,
		"effective_samples": lw.EffectiveSamples,
		"prior_alpha":       lw.PriorAlpha,
		"prior_beta":        lw.PriorBeta,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (lw *LearnedWeight) UnmarshalJSON(data []byte) error {
	var temp map[string]float64
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	alpha, ok1 := temp["alpha"]
	beta, ok2 := temp["beta"]
	if !ok1 || !ok2 {
		return fmt.Errorf("missing required fields")
	}

	lw.Alpha = alpha
	lw.Beta = beta
	lw.EffectiveSamples = temp["effective_samples"]
	lw.PriorAlpha = temp["prior_alpha"]
	lw.PriorBeta = temp["prior_beta"]

	return nil
}

// =============================================================================
// Helper Functions
// =============================================================================

// clamp restricts a value to the range [min, max].
func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// approximateQuantile returns an approximation of the standard normal quantile.
// Uses a rational approximation accurate to about 4 decimal places.
func approximateQuantile(p float64) float64 {
	if p <= 0.0 {
		return math.Inf(-1)
	}
	if p >= 1.0 {
		return math.Inf(1)
	}

	if p < 0.5 {
		return -approximateQuantile(1.0 - p)
	}

	p = p - 0.5
	r := p * p

	num := p * (2.515517 + r*(0.802853+r*0.010328))
	den := 1.0 + r*(1.432788+r*(0.189269+r*0.001308))

	return num / den
}

// sampleBeta generates a sample from Beta(alpha, beta) using gamma sampling.
func sampleBeta(alpha, beta float64) float64 {
	if alpha <= 0 || beta <= 0 {
		return 0.5
	}

	x := sampleGamma(alpha)
	y := sampleGamma(beta)

	if x+y == 0 {
		return 0.5
	}

	return x / (x + y)
}

// sampleGamma generates a sample from Gamma(shape, 1.0).
// Uses Marsaglia and Tsang's method for shape >= 1.
func sampleGamma(shape float64) float64 {
	if shape < 1.0 {
		return sampleGamma(shape+1.0) * math.Pow(rand.Float64(), 1.0/shape)
	}

	d := shape - 1.0/3.0
	c := 1.0 / math.Sqrt(9.0*d)

	for {
		x, v := sampleGammaCandidate(c)
		if acceptGammaSample(x, v, d) {
			return d * v
		}
	}
}

// sampleGammaCandidate generates a candidate sample for the gamma distribution.
func sampleGammaCandidate(c float64) (float64, float64) {
	for {
		x := rand.NormFloat64()
		v := 1.0 + c*x
		if v > 0 {
			return x, v * v * v
		}
	}
}

// acceptGammaSample determines if a gamma sample should be accepted.
func acceptGammaSample(x, v, d float64) bool {
	u := rand.Float64()
	if u < 1.0-0.0331*x*x*x*x {
		return true
	}
	return math.Log(u) < 0.5*x*x+d*(1.0-v+math.Log(v))
}

// =============================================================================
// LearnedContextSize - Gamma Distribution for Positive Integer Sizes
// =============================================================================

// LearnedContextSize represents a learned parameter for positive integer
// context sizes using a Gamma distribution.
//
// The Gamma distribution is parameterized by Alpha (shape) and Beta (rate):
//   - Mean = Alpha / Beta
//   - Variance = Alpha / (Beta^2)
//   - Higher Alpha and Beta together increase confidence
//
// Example: Learning an optimal context size that starts around 100:
//
//	cs := NewLearnedContextSize(100.0, 1.0)  // Mean of 100
//	cs.Update(120, config)                   // Observe size of 120
//	size := cs.Mean()                        // Current best estimate
type LearnedContextSize struct {
	// Alpha is the shape parameter of the Gamma distribution.
	Alpha float64 `json:"alpha"`

	// Beta is the rate parameter of the Gamma distribution.
	Beta float64 `json:"beta"`

	// EffectiveSamples tracks the effective number of observations seen.
	EffectiveSamples float64 `json:"effective_samples"`

	// PriorAlpha stores the original Alpha value for drift protection.
	PriorAlpha float64 `json:"prior_alpha"`

	// PriorBeta stores the original Beta value for drift protection.
	PriorBeta float64 `json:"prior_beta"`
}

// NewLearnedContextSize creates a LearnedContextSize with the given prior.
// Common patterns:
//   - (mean, 1.0): Weak prior centered at mean
//   - (mean*k, k): Prior with mean and higher confidence (larger k)
func NewLearnedContextSize(alpha, beta float64) *LearnedContextSize {
	return &LearnedContextSize{
		Alpha:            alpha,
		Beta:             beta,
		EffectiveSamples: alpha,
		PriorAlpha:       alpha,
		PriorBeta:        beta,
	}
}

// Mean returns the expected value as an integer.
func (lcs *LearnedContextSize) Mean() int {
	mean := lcs.Alpha / lcs.Beta
	return int(math.Round(mean))
}

// Variance returns the variance of the Gamma distribution.
func (lcs *LearnedContextSize) Variance() float64 {
	return lcs.Alpha / (lcs.Beta * lcs.Beta)
}

// Confidence returns a measure of confidence in the learned value.
// Returns a value in [0,1] where 1.0 means very confident.
func (lcs *LearnedContextSize) Confidence() float64 {
	if lcs.Alpha < 1.0 {
		return 0.0
	}
	return 1.0 - math.Exp(-lcs.Alpha/10.0)
}

// Sample generates a random sample from the Gamma distribution as an integer.
func (lcs *LearnedContextSize) Sample() int {
	sample := sampleGamma(lcs.Alpha) / lcs.Beta
	return int(math.Round(math.Max(1.0, sample)))
}

// Update incorporates a new observation using exponential decay.
func (lcs *LearnedContextSize) Update(observed int, config *UpdateConfig) {
	if config == nil {
		config = DefaultUpdateConfig()
	}

	observedFloat := math.Max(1.0, float64(observed))
	lr := clamp(config.LearningRate, 0.0, 1.0)

	lcs.Alpha = lcs.Alpha*(1.0-lr) + observedFloat*lr
	lcs.Beta = lcs.Beta*(1.0-lr) + lr
	lcs.EffectiveSamples = lcs.EffectiveSamples*(1.0-lr) + lr

	lcs.applyContextSizeDrift(config)
}

// applyContextSizeDrift blends parameters back toward prior.
func (lcs *LearnedContextSize) applyContextSizeDrift(config *UpdateConfig) {
	confidence := lcs.Confidence()
	if confidence > config.DriftThreshold {
		blendRate := config.PriorBlendRate
		lcs.Alpha = lcs.Alpha*(1.0-blendRate) + lcs.PriorAlpha*blendRate
		lcs.Beta = lcs.Beta*(1.0-blendRate) + lcs.PriorBeta*blendRate
	}
}

// MarshalJSON implements json.Marshaler.
func (lcs *LearnedContextSize) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]float64{
		"alpha":             lcs.Alpha,
		"beta":              lcs.Beta,
		"effective_samples": lcs.EffectiveSamples,
		"prior_alpha":       lcs.PriorAlpha,
		"prior_beta":        lcs.PriorBeta,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (lcs *LearnedContextSize) UnmarshalJSON(data []byte) error {
	var temp map[string]float64
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	alpha, ok1 := temp["alpha"]
	beta, ok2 := temp["beta"]
	if !ok1 || !ok2 {
		return fmt.Errorf("missing required fields")
	}

	lcs.Alpha = alpha
	lcs.Beta = beta
	lcs.EffectiveSamples = temp["effective_samples"]
	lcs.PriorAlpha = temp["prior_alpha"]
	lcs.PriorBeta = temp["prior_beta"]

	return nil
}

// =============================================================================
// LearnedCount - Poisson-Gamma Conjugate for Count Data
// =============================================================================

// LearnedCount represents a learned parameter for count data using the
// Poisson-Gamma conjugate model.
//
// The posterior distribution is Gamma(Alpha, Beta) where:
//   - Alpha = prior alpha + sum of observed counts
//   - Beta = prior beta + number of observations
//   - Mean = Alpha / Beta (expected count rate)
//
// Example: Learning the number of relevant documents:
//
//	lc := NewLearnedCount(5.0, 1.0)  // Prior mean of 5
//	lc.Update(7, config)             // Observe count of 7
//	count := lc.Mean()               // Current best estimate
type LearnedCount struct {
	// Alpha is the shape parameter (accumulated count evidence).
	Alpha float64 `json:"alpha"`

	// Beta is the rate parameter (accumulated observation count).
	Beta float64 `json:"beta"`

	// EffectiveSamples tracks the effective number of observations seen.
	EffectiveSamples float64 `json:"effective_samples"`

	// PriorAlpha stores the original Alpha value for drift protection.
	PriorAlpha float64 `json:"prior_alpha"`

	// PriorBeta stores the original Beta value for drift protection.
	PriorBeta float64 `json:"prior_beta"`
}

// NewLearnedCount creates a LearnedCount with the given prior parameters.
// Common priors:
//   - (1, 1): Weak prior, mean of 1
//   - (k, 1): Prior with mean k
//   - (k*n, n): Prior with mean k and n pseudo-observations
func NewLearnedCount(alpha, beta float64) *LearnedCount {
	return &LearnedCount{
		Alpha:            alpha,
		Beta:             beta,
		EffectiveSamples: alpha / math.Max(1.0, beta),
		PriorAlpha:       alpha,
		PriorBeta:        beta,
	}
}

// Mean returns the expected count as an integer.
func (lc *LearnedCount) Mean() int {
	mean := lc.Alpha / lc.Beta
	return int(math.Round(mean))
}

// Variance returns the variance of the posterior distribution.
func (lc *LearnedCount) Variance() float64 {
	return lc.Alpha / (lc.Beta * lc.Beta)
}

// Confidence returns a measure of confidence in the learned value.
func (lc *LearnedCount) Confidence() float64 {
	if lc.EffectiveSamples < 1.0 {
		return 0.0
	}
	return 1.0 - math.Exp(-lc.EffectiveSamples/10.0)
}

// Sample generates a random count from the posterior predictive distribution.
func (lc *LearnedCount) Sample() int {
	lambda := sampleGamma(lc.Alpha) / lc.Beta
	count := samplePoisson(lambda)
	return count
}

// Update incorporates a new count observation using the Poisson likelihood.
func (lc *LearnedCount) Update(observed int, config *UpdateConfig) {
	if config == nil {
		config = DefaultUpdateConfig()
	}

	observedFloat := math.Max(0.0, float64(observed))
	lr := clamp(config.LearningRate, 0.0, 1.0)

	lc.Alpha = lc.Alpha*(1.0-lr) + observedFloat*lr
	lc.Beta = lc.Beta*(1.0-lr) + lr
	lc.EffectiveSamples = lc.EffectiveSamples*(1.0-lr) + lr

	lc.applyCountDrift(config)
}

// applyCountDrift blends parameters back toward prior.
func (lc *LearnedCount) applyCountDrift(config *UpdateConfig) {
	confidence := lc.Confidence()
	if confidence > config.DriftThreshold {
		blendRate := config.PriorBlendRate
		lc.Alpha = lc.Alpha*(1.0-blendRate) + lc.PriorAlpha*blendRate
		lc.Beta = lc.Beta*(1.0-blendRate) + lc.PriorBeta*blendRate
	}
}

// MarshalJSON implements json.Marshaler.
func (lc *LearnedCount) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]float64{
		"alpha":             lc.Alpha,
		"beta":              lc.Beta,
		"effective_samples": lc.EffectiveSamples,
		"prior_alpha":       lc.PriorAlpha,
		"prior_beta":        lc.PriorBeta,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (lc *LearnedCount) UnmarshalJSON(data []byte) error {
	var temp map[string]float64
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	alpha, ok1 := temp["alpha"]
	beta, ok2 := temp["beta"]
	if !ok1 || !ok2 {
		return fmt.Errorf("missing required fields")
	}

	lc.Alpha = alpha
	lc.Beta = beta
	lc.EffectiveSamples = temp["effective_samples"]
	lc.PriorAlpha = temp["prior_alpha"]
	lc.PriorBeta = temp["prior_beta"]

	return nil
}

// samplePoisson generates a sample from a Poisson distribution with rate lambda.
// Uses Knuth's algorithm for small lambda and normal approximation for large lambda.
func samplePoisson(lambda float64) int {
	if lambda <= 0 {
		return 0
	}

	if lambda < 30.0 {
		return samplePoissonKnuth(lambda)
	}

	return samplePoissonNormal(lambda)
}

// samplePoissonKnuth uses Knuth's algorithm for small lambda.
func samplePoissonKnuth(lambda float64) int {
	l := math.Exp(-lambda)
	k := 0
	p := 1.0

	for p > l {
		k++
		p *= rand.Float64()
	}

	return k - 1
}

// samplePoissonNormal uses normal approximation for large lambda.
func samplePoissonNormal(lambda float64) int {
	z := rand.NormFloat64()
	sample := lambda + math.Sqrt(lambda)*z
	result := int(math.Round(sample))

	if result < 0 {
		return 0
	}

	return result
}
