package handoff

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
)

// =============================================================================
// HO.4.1 GPObservation Type - Observation with Context Features and Quality
// =============================================================================
//
// GPObservation captures a single observation for GP regression. Each observation
// includes context features (inputs) and a quality score (output). These
// observations are used to train the Gaussian Process for handoff quality
// prediction.

// GPObservation represents a single observation for Gaussian Process regression.
// It captures the context features at a point in time and the observed quality.
type GPObservation struct {
	// ContextSize is the total size of the context in tokens at observation time.
	// This is a key feature for predicting quality degradation.
	ContextSize int `json:"context_size"`

	// TokenCount is the number of tokens in the current response/turn.
	// Used to understand how response complexity affects quality.
	TokenCount int `json:"token_count"`

	// ToolCallCount is the number of tool calls made in the current operation.
	// Tool-heavy operations may have different quality characteristics.
	ToolCallCount int `json:"tool_call_count"`

	// Quality is the observed quality score in [0,1].
	// This is the target variable the GP learns to predict.
	Quality float64 `json:"quality"`
}

// NewGPObservation creates a new GP observation with the given features.
func NewGPObservation(contextSize, tokenCount, toolCallCount int, quality float64) *GPObservation {
	return &GPObservation{
		ContextSize:   contextSize,
		TokenCount:    tokenCount,
		ToolCallCount: toolCallCount,
		Quality:       clamp(quality, 0.0, 1.0),
	}
}

// Features returns the observation as a feature vector for GP computations.
// The features are: [contextSize, tokenCount, toolCallCount]
func (obs *GPObservation) Features() []float64 {
	return []float64{
		float64(obs.ContextSize),
		float64(obs.TokenCount),
		float64(obs.ToolCallCount),
	}
}

// Clone creates a deep copy of the observation.
func (obs *GPObservation) Clone() *GPObservation {
	if obs == nil {
		return nil
	}
	return &GPObservation{
		ContextSize:   obs.ContextSize,
		TokenCount:    obs.TokenCount,
		ToolCallCount: obs.ToolCallCount,
		Quality:       obs.Quality,
	}
}

// =============================================================================
// HO.4.2 GPHyperparams Type - Kernel Hyperparameters
// =============================================================================
//
// GPHyperparams contains the hyperparameters for the Matern 2.5 kernel and
// the GP model. These control the smoothness, scale, and noise characteristics
// of the learned function.

// GPHyperparams contains hyperparameters for the Gaussian Process model.
// These control the kernel behavior and noise modeling.
type GPHyperparams struct {
	// Lengthscales control the characteristic length scale for each feature dimension.
	// Larger values mean the function varies more slowly in that dimension.
	// Should have one entry per feature dimension [contextSize, tokenCount, toolCallCount].
	Lengthscales []float64 `json:"lengthscales"`

	// SignalVariance (sigma_f^2) controls the overall variance of the function.
	// Larger values allow the function to deviate more from the mean.
	SignalVariance float64 `json:"signal_variance"`

	// NoiseVariance (sigma_n^2) represents observation noise.
	// This is added to the diagonal of the kernel matrix for numerical stability
	// and to model measurement noise in the quality scores.
	NoiseVariance float64 `json:"noise_variance"`
}

// DefaultGPHyperparams returns sensible default hyperparameters.
// These are tuned for typical handoff quality prediction scenarios.
//
// Default values:
//   - Lengthscales: [1000.0, 100.0, 5.0] for [contextSize, tokenCount, toolCallCount]
//   - SignalVariance: 0.25 (quality varies moderately)
//   - NoiseVariance: 0.01 (small observation noise)
func DefaultGPHyperparams() *GPHyperparams {
	return &GPHyperparams{
		Lengthscales: []float64{
			1000.0, // Context size (tokens) - varies slowly over 1000s of tokens
			100.0,  // Token count (per turn) - varies over 100s of tokens
			5.0,    // Tool call count - varies over single digits
		},
		SignalVariance: 0.25,
		NoiseVariance:  0.01,
	}
}

// Clone creates a deep copy of the hyperparameters.
func (hp *GPHyperparams) Clone() *GPHyperparams {
	if hp == nil {
		return nil
	}
	cloned := &GPHyperparams{
		Lengthscales:   make([]float64, len(hp.Lengthscales)),
		SignalVariance: hp.SignalVariance,
		NoiseVariance:  hp.NoiseVariance,
	}
	copy(cloned.Lengthscales, hp.Lengthscales)
	return cloned
}

// Validate checks that hyperparameters are valid.
// Returns an error if any parameter is invalid.
func (hp *GPHyperparams) Validate() error {
	if len(hp.Lengthscales) == 0 {
		return fmt.Errorf("lengthscales cannot be empty")
	}
	for i, l := range hp.Lengthscales {
		if l <= 0 {
			return fmt.Errorf("lengthscale[%d] must be positive, got %f", i, l)
		}
	}
	if hp.SignalVariance <= 0 {
		return fmt.Errorf("signal variance must be positive, got %f", hp.SignalVariance)
	}
	if hp.NoiseVariance < 0 {
		return fmt.Errorf("noise variance must be non-negative, got %f", hp.NoiseVariance)
	}
	return nil
}

// =============================================================================
// HO.4.3 AgentGaussianProcess - GP Regression for Quality Prediction
// =============================================================================
//
// AgentGaussianProcess implements Gaussian Process regression for predicting
// handoff quality. It uses the Matern 2.5 kernel for realistic smoothness
// assumptions and provides both mean predictions and uncertainty estimates.

// GPPrediction contains the result of a GP prediction.
type GPPrediction struct {
	// Mean is the predicted quality score.
	Mean float64 `json:"mean"`

	// Variance is the predictive variance (uncertainty).
	Variance float64 `json:"variance"`

	// StdDev is the standard deviation (sqrt of variance).
	StdDev float64 `json:"std_dev"`

	// LowerBound is the lower bound of the 95% confidence interval.
	LowerBound float64 `json:"lower_bound"`

	// UpperBound is the upper bound of the 95% confidence interval.
	UpperBound float64 `json:"upper_bound"`
}

// AgentGaussianProcess implements GP regression for quality prediction.
// It uses the Matern 2.5 kernel and supports incremental updates.
type AgentGaussianProcess struct {
	mu sync.RWMutex

	// Observations collected so far.
	observations []*GPObservation

	// Hyperparameters for the kernel and noise model.
	hyperparams *GPHyperparams

	// priorMean stores the learned prior mean from historical data.
	// If nil, uses default prior mean function.
	priorMean *GPPriorMean

	// Cache for the Cholesky decomposition of K + sigma_n^2 * I.
	// This is recomputed when observations change.
	choleskyL [][]float64

	// Cache for alpha = L^T \ (L \ y) used in prediction.
	alpha []float64

	// cacheValid indicates if the cached computations are up to date.
	cacheValid bool

	// maxObservations limits the number of stored observations.
	// Older observations are removed when this limit is exceeded.
	maxObservations int
}

// NewAgentGaussianProcess creates a new GP with the given hyperparameters.
// If hyperparams is nil, uses default hyperparameters.
func NewAgentGaussianProcess(hyperparams *GPHyperparams) *AgentGaussianProcess {
	if hyperparams == nil {
		hyperparams = DefaultGPHyperparams()
	}
	return &AgentGaussianProcess{
		observations:    make([]*GPObservation, 0),
		hyperparams:     hyperparams.Clone(),
		priorMean:       nil,
		choleskyL:       nil,
		alpha:           nil,
		cacheValid:      false,
		maxObservations: 100, // Default limit
	}
}

// SetMaxObservations sets the maximum number of observations to store.
// When exceeded, the oldest observations are removed.
func (gp *AgentGaussianProcess) SetMaxObservations(max int) {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	if max < 1 {
		max = 1
	}
	gp.maxObservations = max
	gp.trimObservations()
}

// AddObservation adds a new observation to the GP model.
// This invalidates the cache and will trigger recomputation on next prediction.
func (gp *AgentGaussianProcess) AddObservation(obs *GPObservation) {
	if obs == nil {
		return
	}

	gp.mu.Lock()
	defer gp.mu.Unlock()

	gp.observations = append(gp.observations, obs.Clone())
	gp.cacheValid = false
	gp.trimObservations()
}

// AddObservationFromValues adds a new observation from raw values.
func (gp *AgentGaussianProcess) AddObservationFromValues(
	contextSize, tokenCount, toolCallCount int,
	quality float64,
) {
	gp.AddObservation(NewGPObservation(contextSize, tokenCount, toolCallCount, quality))
}

// trimObservations removes oldest observations if we exceed maxObservations.
func (gp *AgentGaussianProcess) trimObservations() {
	for len(gp.observations) > gp.maxObservations {
		gp.observations = gp.observations[1:]
	}
}

// NumObservations returns the number of observations in the GP.
func (gp *AgentGaussianProcess) NumObservations() int {
	gp.mu.RLock()
	defer gp.mu.RUnlock()
	return len(gp.observations)
}

// Predict computes the GP prediction at a query point.
// Returns the predictive mean and variance with uncertainty bounds.
func (gp *AgentGaussianProcess) Predict(contextSize, tokenCount, toolCallCount int) *GPPrediction {
	gp.mu.Lock()
	defer gp.mu.Unlock()

	queryFeatures := []float64{
		float64(contextSize),
		float64(tokenCount),
		float64(toolCallCount),
	}

	return gp.predictLocked(queryFeatures)
}

// PredictFromFeatures computes the GP prediction from a feature vector.
func (gp *AgentGaussianProcess) PredictFromFeatures(features []float64) *GPPrediction {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	return gp.predictLocked(features)
}

// predictLocked performs prediction assuming the lock is held.
func (gp *AgentGaussianProcess) predictLocked(queryFeatures []float64) *GPPrediction {
	n := len(gp.observations)

	// If no observations, return prior
	if n == 0 {
		priorMean := gp.priorMeanValue(queryFeatures)
		priorVar := gp.hyperparams.SignalVariance
		return gp.makePrediction(priorMean, priorVar)
	}

	// Ensure cache is valid
	if !gp.cacheValid {
		gp.updateCache()
	}

	// If cache update failed (numerical issues), return prior
	if !gp.cacheValid {
		priorMean := gp.priorMeanValue(queryFeatures)
		priorVar := gp.hyperparams.SignalVariance
		return gp.makePrediction(priorMean, priorVar)
	}

	// Compute k* = k(X, x*)
	kStar := make([]float64, n)
	for i, obs := range gp.observations {
		kStar[i] = gp.matern25Kernel(obs.Features(), queryFeatures)
	}

	// Compute mean: m(x*) + k*^T * alpha
	mean := gp.priorMeanValue(queryFeatures)
	for i := 0; i < n; i++ {
		mean += kStar[i] * gp.alpha[i]
	}

	// Compute variance: k(x*, x*) - k*^T * (K + sigma_n^2 I)^-1 * k*
	// Using Cholesky: variance = k** - v^T * v where v = L^-1 * k*
	kStarStar := gp.matern25Kernel(queryFeatures, queryFeatures)

	// Solve L * v = k* for v
	v := gp.solveTriangular(gp.choleskyL, kStar, false)

	// variance = k** - v^T * v
	variance := kStarStar
	for i := 0; i < n; i++ {
		variance -= v[i] * v[i]
	}

	// Ensure variance is non-negative (numerical stability)
	if variance < 0 {
		variance = 0
	}

	return gp.makePrediction(mean, variance)
}

// makePrediction creates a GPPrediction from mean and variance.
func (gp *AgentGaussianProcess) makePrediction(mean, variance float64) *GPPrediction {
	stdDev := math.Sqrt(variance)
	// 95% confidence interval: mean +/- 1.96 * stdDev
	z := 1.96

	return &GPPrediction{
		Mean:       clamp(mean, 0.0, 1.0),
		Variance:   variance,
		StdDev:     stdDev,
		LowerBound: clamp(mean-z*stdDev, 0.0, 1.0),
		UpperBound: clamp(mean+z*stdDev, 0.0, 1.0),
	}
}

// updateCache recomputes the Cholesky decomposition and alpha vector.
func (gp *AgentGaussianProcess) updateCache() {
	n := len(gp.observations)
	if n == 0 {
		gp.cacheValid = true
		return
	}

	// Build kernel matrix K
	K := make([][]float64, n)
	for i := 0; i < n; i++ {
		K[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			K[i][j] = gp.matern25Kernel(
				gp.observations[i].Features(),
				gp.observations[j].Features(),
			)
		}
		// Add noise variance to diagonal
		K[i][i] += gp.hyperparams.NoiseVariance
	}

	// Compute Cholesky decomposition: K = L * L^T
	L, err := gp.choleskyDecomposition(K)
	if err != nil {
		// Numerical issues - add jitter and try again
		for i := 0; i < n; i++ {
			K[i][i] += 1e-6
		}
		L, err = gp.choleskyDecomposition(K)
		if err != nil {
			// Still failing - cache remains invalid
			return
		}
	}
	gp.choleskyL = L

	// Compute alpha = L^T \ (L \ (y - m))
	// First compute y - m(X) (observations minus prior mean)
	yMinusM := make([]float64, n)
	for i, obs := range gp.observations {
		yMinusM[i] = obs.Quality - gp.priorMeanValue(obs.Features())
	}

	// Solve L * z = (y - m) for z
	z := gp.solveTriangular(L, yMinusM, false)

	// Solve L^T * alpha = z for alpha
	gp.alpha = gp.solveTriangular(L, z, true)

	gp.cacheValid = true
}

// =============================================================================
// HO.4.4 Matern 2.5 Kernel
// =============================================================================
//
// The Matern 2.5 kernel is more realistic than the RBF kernel for discrete
// observations. It allows the learned function to be less smooth.
//
// k(r) = sigma_f^2 * (1 + sqrt(5)*r + 5*r^2/3) * exp(-sqrt(5)*r)
//
// where r = sqrt(sum_d((x_d - x'_d)^2 / l_d^2)) is the scaled Euclidean distance.

// matern25Kernel computes the Matern 2.5 kernel between two feature vectors.
// Returns k(x1, x2) using the ARD (Automatic Relevance Determination) variant
// with per-dimension lengthscales.
func (gp *AgentGaussianProcess) matern25Kernel(x1, x2 []float64) float64 {
	// Compute scaled Euclidean distance
	r := gp.scaledDistance(x1, x2)

	// Matern 2.5: k(r) = sigma_f^2 * (1 + sqrt(5)*r + 5*r^2/3) * exp(-sqrt(5)*r)
	sqrt5 := math.Sqrt(5.0)
	sqrt5r := sqrt5 * r

	return gp.hyperparams.SignalVariance *
		(1.0 + sqrt5r + 5.0*r*r/3.0) *
		math.Exp(-sqrt5r)
}

// scaledDistance computes the scaled Euclidean distance between two vectors.
// Each dimension is scaled by its lengthscale.
func (gp *AgentGaussianProcess) scaledDistance(x1, x2 []float64) float64 {
	sumSquared := 0.0
	n := len(x1)
	if len(x2) < n {
		n = len(x2)
	}
	nL := len(gp.hyperparams.Lengthscales)

	for d := 0; d < n; d++ {
		diff := x1[d] - x2[d]
		// Use the appropriate lengthscale, or the last one if index out of bounds
		l := gp.hyperparams.Lengthscales[nL-1]
		if d < nL {
			l = gp.hyperparams.Lengthscales[d]
		}
		sumSquared += (diff * diff) / (l * l)
	}

	return math.Sqrt(sumSquared)
}

// =============================================================================
// HO.4.5 Prior Mean Function - Learned from Historical Data
// =============================================================================
//
// The prior mean function provides the baseline quality prediction before
// any observations are seen. It can be learned from historical handoff data
// or set to a constant value.

// GPPriorMean represents a learned prior mean function.
// It captures the baseline expected quality as a function of context features.
type GPPriorMean struct {
	// BaseQuality is the baseline quality at zero context.
	BaseQuality float64 `json:"base_quality"`

	// ContextDecay controls how quality decreases with context size.
	// Quality = BaseQuality * exp(-ContextDecay * contextSize)
	ContextDecay float64 `json:"context_decay"`

	// TokenDecay controls quality decrease with token count.
	TokenDecay float64 `json:"token_decay"`

	// ToolCallDecay controls quality decrease with tool call count.
	ToolCallDecay float64 `json:"tool_call_decay"`

	// HistoricalSamples is the number of observations used to learn this prior.
	HistoricalSamples int `json:"historical_samples"`
}

// DefaultGPPriorMean returns a sensible default prior mean.
func DefaultGPPriorMean() *GPPriorMean {
	return &GPPriorMean{
		BaseQuality:       0.8,    // Start with good quality
		ContextDecay:      0.0001, // Slow decay with context
		TokenDecay:        0.001,  // Moderate decay with tokens
		ToolCallDecay:     0.02,   // Faster decay with tool calls
		HistoricalSamples: 0,
	}
}

// Evaluate computes the prior mean at the given features.
func (pm *GPPriorMean) Evaluate(features []float64) float64 {
	contextSize := 0.0
	tokenCount := 0.0
	toolCallCount := 0.0

	if len(features) > 0 {
		contextSize = features[0]
	}
	if len(features) > 1 {
		tokenCount = features[1]
	}
	if len(features) > 2 {
		toolCallCount = features[2]
	}

	// Quality = BaseQuality * exp(-decay * feature)
	quality := pm.BaseQuality
	quality *= math.Exp(-pm.ContextDecay * contextSize)
	quality *= math.Exp(-pm.TokenDecay * tokenCount)
	quality *= math.Exp(-pm.ToolCallDecay * toolCallCount)

	return clamp(quality, 0.0, 1.0)
}

// Clone creates a deep copy of the prior mean.
func (pm *GPPriorMean) Clone() *GPPriorMean {
	if pm == nil {
		return nil
	}
	return &GPPriorMean{
		BaseQuality:       pm.BaseQuality,
		ContextDecay:      pm.ContextDecay,
		TokenDecay:        pm.TokenDecay,
		ToolCallDecay:     pm.ToolCallDecay,
		HistoricalSamples: pm.HistoricalSamples,
	}
}

// LearnFromObservations updates the prior mean from a set of historical observations.
// Uses simple linear regression in log space to estimate decay parameters.
func (pm *GPPriorMean) LearnFromObservations(observations []*GPObservation) {
	n := len(observations)
	if n == 0 {
		return
	}

	// Compute mean quality and mean features
	sumQuality := 0.0
	sumContext := 0.0
	sumTokens := 0.0
	sumTools := 0.0
	sumLogQuality := 0.0
	validLogCount := 0

	for _, obs := range observations {
		sumQuality += obs.Quality
		sumContext += float64(obs.ContextSize)
		sumTokens += float64(obs.TokenCount)
		sumTools += float64(obs.ToolCallCount)
		if obs.Quality > 0 {
			sumLogQuality += math.Log(obs.Quality)
			validLogCount++
		}
	}

	meanQuality := sumQuality / float64(n)
	meanContext := sumContext / float64(n)
	meanTokens := sumTokens / float64(n)
	meanTools := sumTools / float64(n)

	// Set base quality from mean
	pm.BaseQuality = meanQuality

	// Estimate decays using simple correlation analysis
	if validLogCount > 1 && meanContext > 0 {
		pm.ContextDecay = pm.estimateDecay(observations, 0, sumLogQuality/float64(validLogCount))
	}
	if validLogCount > 1 && meanTokens > 0 {
		pm.TokenDecay = pm.estimateDecay(observations, 1, sumLogQuality/float64(validLogCount))
	}
	if validLogCount > 1 && meanTools > 0 {
		pm.ToolCallDecay = pm.estimateDecay(observations, 2, sumLogQuality/float64(validLogCount))
	}

	pm.HistoricalSamples = n
}

// estimateDecay estimates the decay parameter for a single feature dimension.
// Uses correlation between feature and log(quality).
func (pm *GPPriorMean) estimateDecay(
	observations []*GPObservation,
	featureIdx int,
	meanLogQuality float64,
) float64 {
	n := len(observations)
	if n < 2 {
		return 0.0
	}

	// Compute mean of feature
	sumFeature := 0.0
	validCount := 0
	for _, obs := range observations {
		if obs.Quality > 0 {
			features := obs.Features()
			if featureIdx < len(features) {
				sumFeature += features[featureIdx]
				validCount++
			}
		}
	}

	if validCount < 2 {
		return 0.0
	}

	meanFeature := sumFeature / float64(validCount)

	// Compute covariance and variance
	cov := 0.0
	varFeature := 0.0
	for _, obs := range observations {
		if obs.Quality > 0 {
			features := obs.Features()
			if featureIdx < len(features) {
				featureDiff := features[featureIdx] - meanFeature
				logQDiff := math.Log(obs.Quality) - meanLogQuality
				cov += featureDiff * logQDiff
				varFeature += featureDiff * featureDiff
			}
		}
	}

	if varFeature < 1e-10 {
		return 0.0
	}

	// Decay = -covariance / variance (negative because quality decreases)
	decay := -cov / varFeature

	// Ensure decay is non-negative (quality shouldn't increase with feature)
	if decay < 0 {
		decay = 0
	}

	// Cap decay at reasonable values
	if decay > 0.1 {
		decay = 0.1
	}

	return decay
}

// priorMeanValue returns the prior mean at the given features.
// Uses the learned prior if available, otherwise a default constant.
func (gp *AgentGaussianProcess) priorMeanValue(features []float64) float64 {
	if gp.priorMean != nil {
		return gp.priorMean.Evaluate(features)
	}
	// Default prior mean: constant 0.7 (reasonable baseline quality)
	return 0.7
}

// SetPriorMean sets a learned prior mean function.
func (gp *AgentGaussianProcess) SetPriorMean(priorMean *GPPriorMean) {
	gp.mu.Lock()
	defer gp.mu.Unlock()
	if priorMean != nil {
		gp.priorMean = priorMean.Clone()
	} else {
		gp.priorMean = nil
	}
	gp.cacheValid = false
}

// LearnPriorFromHistory sets the prior mean by learning from historical observations.
func (gp *AgentGaussianProcess) LearnPriorFromHistory(historicalObs []*GPObservation) {
	if len(historicalObs) == 0 {
		return
	}

	prior := DefaultGPPriorMean()
	prior.LearnFromObservations(historicalObs)

	gp.SetPriorMean(prior)
}

// GetPriorMean returns a copy of the current prior mean.
func (gp *AgentGaussianProcess) GetPriorMean() *GPPriorMean {
	gp.mu.RLock()
	defer gp.mu.RUnlock()
	if gp.priorMean == nil {
		return nil
	}
	return gp.priorMean.Clone()
}

// =============================================================================
// Cholesky Decomposition and Linear Algebra Helpers
// =============================================================================

// choleskyDecomposition computes the Cholesky decomposition A = L * L^T.
// Returns L such that A = L * L^T, where L is lower triangular.
// Returns an error if A is not positive definite.
func (gp *AgentGaussianProcess) choleskyDecomposition(A [][]float64) ([][]float64, error) {
	n := len(A)
	if n == 0 {
		return nil, nil
	}

	L := make([][]float64, n)
	for i := 0; i < n; i++ {
		L[i] = make([]float64, n)
	}

	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := 0.0
			for k := 0; k < j; k++ {
				sum += L[i][k] * L[j][k]
			}

			if i == j {
				// Diagonal element
				val := A[i][i] - sum
				if val <= 0 {
					return nil, fmt.Errorf("matrix is not positive definite at index %d", i)
				}
				L[i][j] = math.Sqrt(val)
			} else {
				// Off-diagonal element
				if L[j][j] == 0 {
					return nil, fmt.Errorf("zero diagonal at index %d", j)
				}
				L[i][j] = (A[i][j] - sum) / L[j][j]
			}
		}
	}

	return L, nil
}

// solveTriangular solves L * x = b for x, where L is triangular.
// If transpose is true, solves L^T * x = b instead.
func (gp *AgentGaussianProcess) solveTriangular(
	L [][]float64,
	b []float64,
	transpose bool,
) []float64 {
	n := len(b)
	x := make([]float64, n)

	if !transpose {
		// Forward substitution: L * x = b
		for i := 0; i < n; i++ {
			sum := 0.0
			for j := 0; j < i; j++ {
				sum += L[i][j] * x[j]
			}
			if L[i][i] != 0 {
				x[i] = (b[i] - sum) / L[i][i]
			}
		}
	} else {
		// Back substitution: L^T * x = b
		for i := n - 1; i >= 0; i-- {
			sum := 0.0
			for j := i + 1; j < n; j++ {
				sum += L[j][i] * x[j]
			}
			if L[i][i] != 0 {
				x[i] = (b[i] - sum) / L[i][i]
			}
		}
	}

	return x
}

// =============================================================================
// Hyperparameter Management
// =============================================================================

// GetHyperparams returns a copy of the current hyperparameters.
func (gp *AgentGaussianProcess) GetHyperparams() *GPHyperparams {
	gp.mu.RLock()
	defer gp.mu.RUnlock()
	return gp.hyperparams.Clone()
}

// SetHyperparams updates the hyperparameters and invalidates the cache.
func (gp *AgentGaussianProcess) SetHyperparams(hp *GPHyperparams) error {
	if hp == nil {
		return fmt.Errorf("hyperparameters cannot be nil")
	}
	if err := hp.Validate(); err != nil {
		return err
	}

	gp.mu.Lock()
	defer gp.mu.Unlock()

	gp.hyperparams = hp.Clone()
	gp.cacheValid = false

	return nil
}

// =============================================================================
// Observation Management
// =============================================================================

// GetObservations returns a copy of all observations.
func (gp *AgentGaussianProcess) GetObservations() []*GPObservation {
	gp.mu.RLock()
	defer gp.mu.RUnlock()

	obs := make([]*GPObservation, len(gp.observations))
	for i, o := range gp.observations {
		obs[i] = o.Clone()
	}
	return obs
}

// ClearObservations removes all observations and invalidates the cache.
func (gp *AgentGaussianProcess) ClearObservations() {
	gp.mu.Lock()
	defer gp.mu.Unlock()

	gp.observations = make([]*GPObservation, 0)
	gp.choleskyL = nil
	gp.alpha = nil
	gp.cacheValid = false
}

// =============================================================================
// JSON Serialization
// =============================================================================

// gpJSON is used for JSON marshaling/unmarshaling.
type gpJSON struct {
	Observations    []*GPObservation `json:"observations"`
	Hyperparams     *GPHyperparams   `json:"hyperparams"`
	PriorMean       *GPPriorMean     `json:"prior_mean"`
	MaxObservations int              `json:"max_observations"`
}

// MarshalJSON implements json.Marshaler.
func (gp *AgentGaussianProcess) MarshalJSON() ([]byte, error) {
	gp.mu.RLock()
	defer gp.mu.RUnlock()

	return json.Marshal(gpJSON{
		Observations:    gp.observations,
		Hyperparams:     gp.hyperparams,
		PriorMean:       gp.priorMean,
		MaxObservations: gp.maxObservations,
	})
}

// UnmarshalJSON implements json.Unmarshaler.
func (gp *AgentGaussianProcess) UnmarshalJSON(data []byte) error {
	var temp gpJSON
	if err := json.Unmarshal(data, &temp); err != nil {
		return err
	}

	gp.mu.Lock()
	defer gp.mu.Unlock()

	gp.observations = temp.Observations
	if gp.observations == nil {
		gp.observations = make([]*GPObservation, 0)
	}

	gp.hyperparams = temp.Hyperparams
	if gp.hyperparams == nil {
		gp.hyperparams = DefaultGPHyperparams()
	}

	gp.priorMean = temp.PriorMean
	gp.maxObservations = temp.MaxObservations
	if gp.maxObservations < 1 {
		gp.maxObservations = 100
	}

	gp.cacheValid = false

	return nil
}

// Clone creates a deep copy of the GP.
func (gp *AgentGaussianProcess) Clone() *AgentGaussianProcess {
	gp.mu.RLock()
	defer gp.mu.RUnlock()

	cloned := &AgentGaussianProcess{
		observations:    make([]*GPObservation, len(gp.observations)),
		hyperparams:     gp.hyperparams.Clone(),
		priorMean:       nil,
		maxObservations: gp.maxObservations,
		cacheValid:      false,
	}

	for i, obs := range gp.observations {
		cloned.observations[i] = obs.Clone()
	}

	if gp.priorMean != nil {
		cloned.priorMean = gp.priorMean.Clone()
	}

	return cloned
}
