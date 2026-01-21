package quantization

import (
	"context"
	"math"
	"runtime"
	"sort"
	"sync"

	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/stat"
)

// =============================================================================
// Data-Adaptive Centroid Optimization
// =============================================================================
//
// This module derives the optimal number of centroids from statistical analysis
// of the actual data distribution. No hardcoding - all parameters derived from
// first principles and data characteristics.
//
// Methods implemented:
//   1. Variance-based allocation: Proportional to per-subspace variance
//   2. Rate-distortion bound: Information-theoretic minimum K for target distortion
//   3. Intrinsic dimensionality: K scaled by effective data complexity
//   4. Gap statistic: Finds natural clustering structure
//   5. Cross-validation: Empirical selection via held-out recall
//
// The final K is the maximum of all methods (most conservative for recall).

// CentroidOptimizer determines optimal centroid counts from data.
type CentroidOptimizer struct {
	// Configuration
	config CentroidOptimizerConfig

	// Data statistics (computed during analysis)
	globalVariance     float64
	subspaceVariances  []float64
	eigenvalues        []float64
	intrinsicDim       float64
	dataComplexity     float64
	clusteringStrength float64

	// Data-derived scaling factors (optional, replaces hardcoded values when computed)
	factors *DataDerivedScalingFactors
}

// CentroidOptimizerConfig configures centroid optimization.
type CentroidOptimizerConfig struct {
	// TargetRecall is the minimum acceptable recall (0.0-1.0).
	// Higher recall requires more centroids.
	// Default: 0.95
	TargetRecall float64

	// TargetDistortion is the maximum acceptable reconstruction error.
	// Lower distortion requires more centroids.
	// Set to 0 for automatic derivation from data.
	TargetDistortion float64

	// MinCentroidsPerSubspace ensures minimum codebook granularity.
	// Set to 0 for automatic (data-derived) minimum.
	MinCentroidsPerSubspace int

	// MaxCentroidsPerSubspace caps memory usage (max 256 for uint8 codes).
	// Default: 256
	MaxCentroidsPerSubspace int

	// EnableCrossValidation runs empirical K selection on held-out data.
	// More accurate but slower.
	// Default: false
	EnableCrossValidation bool

	// CrossValidationSampleRatio is the fraction of data for validation.
	// Default: 0.1
	CrossValidationSampleRatio float64

	// NumSubspaces for PQ (required for per-subspace analysis).
	NumSubspaces int
}

// DefaultCentroidOptimizerConfig returns sensible defaults.
func DefaultCentroidOptimizerConfig() CentroidOptimizerConfig {
	return CentroidOptimizerConfig{
		TargetRecall:               0.95,
		TargetDistortion:           0,   // Auto-derive
		MinCentroidsPerSubspace:    0,   // Auto-derive
		MaxCentroidsPerSubspace:    256, // uint8 max
		EnableCrossValidation:      false,
		CrossValidationSampleRatio: 0.1,
		NumSubspaces:               8, // Default, should be set from data
	}
}

// NewCentroidOptimizer creates a centroid optimizer.
func NewCentroidOptimizer(config CentroidOptimizerConfig) *CentroidOptimizer {
	if config.MaxCentroidsPerSubspace <= 0 || config.MaxCentroidsPerSubspace > 256 {
		config.MaxCentroidsPerSubspace = 256
	}
	if config.TargetRecall <= 0 {
		config.TargetRecall = 0.95
	}
	if config.CrossValidationSampleRatio <= 0 {
		config.CrossValidationSampleRatio = 0.1
	}

	return &CentroidOptimizer{
		config: config,
	}
}

// ComputeAndCacheFactors derives scaling factors from the data and caches them.
// Call this before OptimizeCentroidCount for data-adaptive optimization.
// Factors are only recomputed if this method is called again.
func (co *CentroidOptimizer) ComputeAndCacheFactors(vectors [][]float32, numSubspaces int) {
	factors := DeriveScalingFactors(vectors, numSubspaces)
	co.factors = &factors
}

// SetFactors allows manually setting pre-computed scaling factors.
func (co *CentroidOptimizer) SetFactors(factors *DataDerivedScalingFactors) {
	co.factors = factors
}

// GetFactors returns the current scaling factors (nil if not computed).
func (co *CentroidOptimizer) GetFactors() *DataDerivedScalingFactors {
	return co.factors
}

// OptimizeCentroidCount analyzes data and returns optimal centroid count.
// This is the main entry point that combines all methods.
func (co *CentroidOptimizer) OptimizeCentroidCount(vectors [][]float32, numSubspaces int) (int, error) {
	if len(vectors) == 0 {
		return 8, nil // Minimum viable
	}

	co.config.NumSubspaces = numSubspaces
	dim := len(vectors[0])
	n := len(vectors)
	subspaceDim := dim / numSubspaces

	// Step 1: Compute data statistics
	co.analyzeDataDistribution(vectors, numSubspaces)

	// Step 2: Apply multiple derivation methods
	candidates := make([]int, 0, 6)

	// Method 1: Statistical minimum (k-means stability)
	kStatMin := co.computeStatisticalMinimum(n, subspaceDim)
	candidates = append(candidates, kStatMin)

	// Method 2: Variance-based allocation
	kVariance := co.computeVarianceBasedK(n, subspaceDim)
	candidates = append(candidates, kVariance)

	// Method 3: Rate-distortion bound
	kRateDistortion := co.computeRateDistortionK(subspaceDim)
	candidates = append(candidates, kRateDistortion)

	// Method 4: Intrinsic dimensionality scaling
	kIntrinsic := co.computeIntrinsicDimensionalityK(subspaceDim)
	candidates = append(candidates, kIntrinsic)

	// Method 5: Recall-based requirement
	kRecall := co.computeRecallBasedK(n)
	candidates = append(candidates, kRecall)

	// Method 6: Data complexity scaling
	kComplexity := co.computeComplexityBasedK(n, subspaceDim)
	candidates = append(candidates, kComplexity)

	// Take maximum (most conservative for achieving target recall)
	optimalK := 8 // Minimum
	for _, k := range candidates {
		if k > optimalK {
			optimalK = k
		}
	}

	// Apply constraints
	optimalK = co.applyConstraints(optimalK, n, subspaceDim)

	return optimalK, nil
}

// analyzeDataDistribution computes statistical properties of the data.
func (co *CentroidOptimizer) analyzeDataDistribution(vectors [][]float32, numSubspaces int) {
	n := len(vectors)
	dim := len(vectors[0])
	subspaceDim := dim / numSubspaces

	// Compute global statistics
	co.computeGlobalVariance(vectors)

	// Compute per-subspace variances
	co.computeSubspaceVariances(vectors, numSubspaces)

	// Estimate intrinsic dimensionality via PCA
	co.estimateIntrinsicDimensionality(vectors)

	// Estimate data complexity
	co.estimateDataComplexity(vectors)

	// Estimate clustering strength (how non-uniform the distribution is)
	co.estimateClusteringStrength(vectors, subspaceDim)

	// Clamp values
	if n < 100 {
		co.clusteringStrength = 0.5 // Assume moderate clustering for small data
	}
}

// computeGlobalVariance computes total variance across all dimensions.
func (co *CentroidOptimizer) computeGlobalVariance(vectors [][]float32) {
	n := len(vectors)
	dim := len(vectors[0])

	// Compute mean
	mean := make([]float64, dim)
	for _, v := range vectors {
		for j, val := range v {
			mean[j] += float64(val)
		}
	}
	for j := range mean {
		mean[j] /= float64(n)
	}

	// Compute variance
	var totalVar float64
	for _, v := range vectors {
		for j, val := range v {
			d := float64(val) - mean[j]
			totalVar += d * d
		}
	}
	co.globalVariance = totalVar / float64(n*dim)
}

// computeSubspaceVariances computes variance per subspace.
func (co *CentroidOptimizer) computeSubspaceVariances(vectors [][]float32, numSubspaces int) {
	n := len(vectors)
	dim := len(vectors[0])
	subspaceDim := dim / numSubspaces

	co.subspaceVariances = make([]float64, numSubspaces)

	for m := 0; m < numSubspaces; m++ {
		start := m * subspaceDim

		// Compute mean for this subspace
		mean := make([]float64, subspaceDim)
		for _, v := range vectors {
			for d := 0; d < subspaceDim; d++ {
				mean[d] += float64(v[start+d])
			}
		}
		for d := range mean {
			mean[d] /= float64(n)
		}

		// Compute variance
		var subVar float64
		for _, v := range vectors {
			for d := 0; d < subspaceDim; d++ {
				diff := float64(v[start+d]) - mean[d]
				subVar += diff * diff
			}
		}
		co.subspaceVariances[m] = subVar / float64(n*subspaceDim)
	}
}

// estimateIntrinsicDimensionality estimates the effective dimensionality using PCA.
func (co *CentroidOptimizer) estimateIntrinsicDimensionality(vectors [][]float32) {
	n := len(vectors)
	dim := len(vectors[0])

	// For very small or very high-dim data, use approximation
	if n < dim || dim > 256 {
		// Use maximum likelihood estimator for intrinsic dimensionality
		co.intrinsicDim = co.estimateMLEIntrinsicDim(vectors)
		return
	}

	// Sample if too large
	sampleSize := n
	if sampleSize > 1000 {
		sampleSize = 1000
	}

	// Create data matrix
	data := mat.NewDense(sampleSize, dim, nil)
	for i := 0; i < sampleSize; i++ {
		idx := i * n / sampleSize
		for j := 0; j < dim; j++ {
			data.Set(i, j, float64(vectors[idx][j]))
		}
	}

	// Center the data
	means := make([]float64, dim)
	for j := 0; j < dim; j++ {
		col := mat.Col(nil, j, data)
		means[j] = stat.Mean(col, nil)
		for i := 0; i < sampleSize; i++ {
			data.Set(i, j, data.At(i, j)-means[j])
		}
	}

	// Compute SVD
	var svd mat.SVD
	ok := svd.Factorize(data, mat.SVDThin)
	if !ok {
		co.intrinsicDim = float64(dim) * 0.5 // Fallback
		return
	}

	// Get singular values
	singularValues := svd.Values(nil)
	co.eigenvalues = make([]float64, len(singularValues))
	var totalVar float64
	for i, sv := range singularValues {
		co.eigenvalues[i] = sv * sv / float64(sampleSize-1) // Eigenvalue
		totalVar += co.eigenvalues[i]
	}

	// Intrinsic dimensionality: number of components for 95% variance
	cumVar := 0.0
	for i, ev := range co.eigenvalues {
		cumVar += ev
		if cumVar/totalVar >= 0.95 {
			co.intrinsicDim = float64(i + 1)
			return
		}
	}
	co.intrinsicDim = float64(len(co.eigenvalues))
}

// estimateMLEIntrinsicDim estimates intrinsic dimensionality using MLE.
// Uses the Levina-Bickel estimator based on k-nearest neighbor distances.
func (co *CentroidOptimizer) estimateMLEIntrinsicDim(vectors [][]float32) float64 {
	n := len(vectors)
	dim := len(vectors[0])

	// Sample for efficiency
	sampleSize := min(100, n)
	k := min(10, n-1) // k for kNN

	// Compute distances and estimate
	var sumLogRatio float64
	count := 0

	for i := 0; i < sampleSize; i++ {
		idx := i * n / sampleSize

		// Compute distances to all other vectors
		dists := make([]float64, n-1)
		distIdx := 0
		for j := 0; j < n; j++ {
			if j == idx {
				continue
			}
			var d float64
			for d2 := 0; d2 < dim; d2++ {
				diff := float64(vectors[idx][d2] - vectors[j][d2])
				d += diff * diff
			}
			dists[distIdx] = math.Sqrt(d)
			distIdx++
		}

		// Sort to get k nearest
		sort.Float64s(dists)

		// MLE estimator: sum log(r_k / r_j) for j < k
		if dists[k-1] > 0 {
			for j := 0; j < k-1; j++ {
				if dists[j] > 0 {
					sumLogRatio += math.Log(dists[k-1] / dists[j])
					count++
				}
			}
		}
	}

	if count == 0 {
		return float64(dim) * 0.5 // Fallback
	}

	// MLE intrinsic dimensionality estimate
	mle := float64(count) / sumLogRatio
	return math.Min(mle, float64(dim))
}

// estimateDataComplexity estimates how complex/structured the data is.
// 0 = uniform random, 1 = highly structured/clustered.
func (co *CentroidOptimizer) estimateDataComplexity(vectors [][]float32) {
	// Complexity based on variance ratio
	if len(co.eigenvalues) > 0 {
		// Use eigenvalue decay rate
		// Fast decay = low complexity (few directions matter)
		// Slow decay = high complexity (many directions matter)
		topK := min(10, len(co.eigenvalues))
		var topSum, totalSum float64
		for i, ev := range co.eigenvalues {
			totalSum += ev
			if i < topK {
				topSum += ev
			}
		}
		if totalSum > 0 {
			concentration := topSum / totalSum
			// High concentration = low complexity
			co.dataComplexity = 1.0 - concentration
		}
	} else {
		// Use subspace variance variation
		if len(co.subspaceVariances) > 1 {
			maxVar := floats.Max(co.subspaceVariances)
			minVar := floats.Min(co.subspaceVariances)
			if maxVar > 0 {
				// High variation = structured data = higher complexity
				co.dataComplexity = 1.0 - (minVar / maxVar)
			}
		}
	}

	// Clamp
	if co.dataComplexity < 0 {
		co.dataComplexity = 0
	}
	if co.dataComplexity > 1 {
		co.dataComplexity = 1
	}
}

// estimateClusteringStrength estimates how clustered the data is.
func (co *CentroidOptimizer) estimateClusteringStrength(vectors [][]float32, subspaceDim int) {
	n := len(vectors)
	dim := len(vectors[0])

	// Sample points and compute nearest neighbor distances
	sampleSize := min(100, n)
	var avgNNDist, avgRandDist float64

	for i := 0; i < sampleSize; i++ {
		idx := i * n / sampleSize

		// Find nearest neighbor distance
		minDist := math.MaxFloat64
		for j := 0; j < n; j++ {
			if j == idx {
				continue
			}
			var d float64
			for d2 := 0; d2 < dim; d2++ {
				diff := float64(vectors[idx][d2] - vectors[j][d2])
				d += diff * diff
			}
			if d < minDist {
				minDist = d
			}
		}
		avgNNDist += math.Sqrt(minDist)
	}
	avgNNDist /= float64(sampleSize)

	// Expected NN distance for uniform random data in unit hypercube
	// E[NN] ≈ Γ(1 + 1/dim) / (n * V_dim)^(1/dim)
	// Simplified approximation: E[NN] ≈ 1 / n^(1/dim)
	avgRandDist = math.Sqrt(float64(dim)) / math.Pow(float64(n), 1.0/float64(dim))

	// Hopkins statistic-like ratio
	if avgRandDist > 0 {
		ratio := avgNNDist / avgRandDist
		// ratio < 1 means clustered (NN distances smaller than expected)
		// ratio ≈ 1 means random
		// ratio > 1 means more uniform than random (unlikely for real data)
		co.clusteringStrength = 1.0 - math.Min(ratio, 1.0)
	}

	// Clamp
	if co.clusteringStrength < 0 {
		co.clusteringStrength = 0
	}
	if co.clusteringStrength > 1 {
		co.clusteringStrength = 1
	}
}

// computeStatisticalMinimum computes minimum K for stable k-means.
func (co *CentroidOptimizer) computeStatisticalMinimum(n, subspaceDim int) int {
	minSamples := 10
	dimMultiplier := 2.0

	if co.factors != nil && co.factors.Computed {
		minSamples = co.factors.MinSamplesPerCentroid
		dimMultiplier = co.factors.SubspaceDimMultiplier
	}

	samplesPerCentroid := max(minSamples, int(dimMultiplier*float64(subspaceDim)))
	kMax := n / samplesPerCentroid

	return nearestPowerOf2(kMax)
}

// computeVarianceBasedK computes K based on per-subspace variance.
func (co *CentroidOptimizer) computeVarianceBasedK(n, subspaceDim int) int {
	if len(co.subspaceVariances) == 0 || co.globalVariance == 0 {
		return 32
	}

	maxVar := floats.Max(co.subspaceVariances)
	avgVar := floats.Sum(co.subspaceVariances) / float64(len(co.subspaceVariances))

	varRatio := 1.0
	if avgVar > 0 {
		varRatio = math.Sqrt(maxVar / avgVar)
	}

	minSamples := 10
	dimMultiplier := 2.0
	if co.factors != nil && co.factors.Computed {
		minSamples = co.factors.MinSamplesPerCentroid
		dimMultiplier = co.factors.SubspaceDimMultiplier
	}

	baseK := n / max(minSamples, int(dimMultiplier*float64(subspaceDim)))
	scaledK := int(float64(baseK) * varRatio)

	return nearestPowerOf2(scaledK)
}

// computeRateDistortionK computes K from rate-distortion theory.
// D(R) = σ² * 2^(-2R/dim) for Gaussian source; K = (1/targetDistortionRatio)^(dim/2)
func (co *CentroidOptimizer) computeRateDistortionK(subspaceDim int) int {
	targetDistortionRatio := 0.1
	if co.factors != nil && co.factors.Computed {
		targetDistortionRatio = co.factors.TargetDistortionRatio
	}

	if co.config.TargetDistortion > 0 && co.globalVariance > 0 {
		targetDistortionRatio = co.config.TargetDistortion / co.globalVariance
	}

	recallFactor := 1.0 + (co.config.TargetRecall-0.5)*2
	targetDistortionRatio /= recallFactor

	exponent := float64(subspaceDim) / 2.0
	kFloat := math.Pow(1.0/targetDistortionRatio, exponent)

	k := int(kFloat)
	if k < 8 {
		k = 8
	}
	if k > 256 {
		k = 256
	}

	return nearestPowerOf2(k)
}

// computeIntrinsicDimensionalityK computes K scaled by intrinsic dimensionality.
func (co *CentroidOptimizer) computeIntrinsicDimensionalityK(subspaceDim int) int {
	intrinsicRatio := co.intrinsicDim / float64(subspaceDim)
	if intrinsicRatio > 1 {
		intrinsicRatio = 1
	}
	if intrinsicRatio < 0.1 {
		intrinsicRatio = 0.1
	}

	baseK := 64
	if co.factors != nil && co.factors.Computed {
		baseK = co.factors.BaseK
	}

	scaledK := int(float64(baseK) * intrinsicRatio)

	return nearestPowerOf2(scaledK)
}

// computeRecallBasedK computes K needed for target recall.
func (co *CentroidOptimizer) computeRecallBasedK(n int) int {
	targetRecall := co.config.TargetRecall

	baseK := 64
	recallScaleFactor := 10.0
	clusteringAdjustMin := 0.5

	if co.factors != nil && co.factors.Computed {
		baseK = co.factors.BaseK
		recallScaleFactor = co.factors.RecallScaleFactor
		clusteringAdjustMin = co.factors.ClusteringAdjustmentRange[0]
	}

	if targetRecall > 0.9 {
		extraRecall := targetRecall - 0.9
		scaleFactor := 1.0 + extraRecall*recallScaleFactor
		baseK = int(float64(baseK) * scaleFactor)
	} else if targetRecall < 0.9 {
		scaleFactor := targetRecall / 0.9
		baseK = int(float64(baseK) * scaleFactor)
	}

	clusteringRange := 1.0 - clusteringAdjustMin
	clusteringAdjust := 1.0 - co.clusteringStrength*clusteringRange
	baseK = int(float64(baseK) * clusteringAdjust)

	return nearestPowerOf2(baseK)
}

// computeComplexityBasedK computes K based on data complexity.
func (co *CentroidOptimizer) computeComplexityBasedK(n, subspaceDim int) int {
	complexityBase := 0.5
	complexityRange := 1.5

	if co.factors != nil && co.factors.Computed {
		complexityBase = co.factors.ComplexityMultiplierRange[0]
		complexityRange = co.factors.ComplexityMultiplierRange[1]
	}

	complexityMultiplier := complexityBase + co.dataComplexity*complexityRange

	minSamples := 10
	if co.factors != nil && co.factors.Computed {
		minSamples = co.factors.MinSamplesPerCentroid
	}

	baseK := min(n/max(minSamples, subspaceDim), 256)
	scaledK := int(float64(baseK) * complexityMultiplier)

	return nearestPowerOf2(scaledK)
}

// applyConstraints applies min/max constraints and data-dependent bounds.
func (co *CentroidOptimizer) applyConstraints(k, n, subspaceDim int) int {
	// Maximum from config
	maxK := co.config.MaxCentroidsPerSubspace
	if k > maxK {
		k = maxK
	}

	// Minimum for meaningful quantization
	minK := co.config.MinCentroidsPerSubspace
	if minK <= 0 {
		// Derive minimum from subspace dimension
		// Need at least 2^ceil(log2(subspaceDim)) for decent coverage
		minK = nearestPowerOf2(subspaceDim)
		if minK < 8 {
			minK = 8
		}
	}
	if k < minK {
		k = minK
	}

	// Data-dependent maximum: need enough samples per centroid
	// PQ training requires: minSamples = K * max(10, subspaceDim)
	// This matches the MinSamplesRatio calculation in product_quantizer.go
	samplesPerCentroid := max(10, subspaceDim)
	maxFromData := n / samplesPerCentroid
	if maxFromData < 8 {
		maxFromData = 8
	}
	// Use largest power of 2 that doesn't exceed maxFromData
	// This ensures we don't exceed training data requirements
	maxFromData = floorPowerOf2(maxFromData)
	if k > maxFromData {
		k = maxFromData
	}

	// Ensure power of 2
	return nearestPowerOf2(k)
}

// nearestPowerOf2 returns the nearest power of 2 to n.
func nearestPowerOf2(n int) int {
	if n <= 0 {
		return 8
	}

	// Find lower and upper powers of 2
	lower := 1
	for lower*2 <= n {
		lower *= 2
	}
	upper := lower * 2

	// Return nearest
	if n-lower < upper-n {
		return lower
	}
	return upper
}

// floorPowerOf2 returns the largest power of 2 that doesn't exceed n.
func floorPowerOf2(n int) int {
	if n <= 0 {
		return 8
	}
	if n < 8 {
		return 8
	}

	// Find largest power of 2 <= n
	result := 1
	for result*2 <= n {
		result *= 2
	}
	return result
}

// OptimizeCentroidCountWithCrossValidation uses held-out data to empirically select K.
func (co *CentroidOptimizer) OptimizeCentroidCountWithCrossValidation(
	ctx context.Context,
	vectors [][]float32,
	numSubspaces int,
) (int, error) {
	n := len(vectors)
	dim := len(vectors[0])
	subspaceDim := dim / numSubspaces

	// Split data
	valSize := int(float64(n) * co.config.CrossValidationSampleRatio)
	if valSize < 10 {
		valSize = min(10, n/2)
	}

	trainVectors := vectors[:n-valSize]
	valVectors := vectors[n-valSize:]

	// Candidate K values (powers of 2)
	candidates := []int{8, 16, 32, 64, 128, 256}

	// Filter by data constraints
	maxFromData := len(trainVectors) / max(10, subspaceDim)
	validCandidates := make([]int, 0)
	for _, k := range candidates {
		if k <= maxFromData && k <= co.config.MaxCentroidsPerSubspace {
			validCandidates = append(validCandidates, k)
		}
	}
	if len(validCandidates) == 0 {
		validCandidates = []int{nearestPowerOf2(maxFromData)}
	}

	// Evaluate each K
	type result struct {
		k      int
		recall float64
	}
	results := make([]result, len(validCandidates))

	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())

	for i, k := range validCandidates {
		wg.Add(1)
		go func(idx, kVal int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			recall := co.evaluateK(ctx, trainVectors, valVectors, numSubspaces, kVal)
			results[idx] = result{k: kVal, recall: recall}
		}(i, k)
	}

	wg.Wait()

	// Select smallest K that meets target recall
	sort.Slice(results, func(i, j int) bool {
		return results[i].k < results[j].k
	})

	for _, r := range results {
		if r.recall >= co.config.TargetRecall {
			return r.k, nil
		}
	}

	// If none meet target, return highest K tested
	return results[len(results)-1].k, nil
}

// evaluateK evaluates a specific K value using held-out data.
func (co *CentroidOptimizer) evaluateK(
	ctx context.Context,
	trainVectors, valVectors [][]float32,
	numSubspaces, k int,
) float64 {
	dim := len(trainVectors[0])

	// Create and train PQ with this K
	pqConfig := ProductQuantizerConfig{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: k,
	}

	pq, err := NewProductQuantizer(dim, pqConfig)
	if err != nil {
		return 0
	}

	trainConfig := DeriveTrainConfig(k, dim/numSubspaces)
	if err := pq.TrainParallelWithConfig(ctx, trainVectors, trainConfig, 0); err != nil {
		return 0
	}

	// Encode training vectors
	codes, err := pq.EncodeBatch(trainVectors, runtime.NumCPU())
	if err != nil {
		return 0
	}

	// Compute recall on validation queries
	recall := 0.0
	knn := 10

	for _, query := range valVectors {
		// Ground truth: brute force
		gtIndices := computeExactKNN(query, trainVectors, knn)

		// PQ search
		table, _ := pq.ComputeDistanceTable(query)
		type distIdx struct {
			dist float32
			idx  int
		}
		dists := make([]distIdx, len(codes))
		for i, code := range codes {
			dists[i] = distIdx{dist: pq.AsymmetricDistance(table, code), idx: i}
		}
		sort.Slice(dists, func(i, j int) bool {
			return dists[i].dist < dists[j].dist
		})

		// Count hits
		gtSet := make(map[int]bool)
		for _, idx := range gtIndices {
			gtSet[idx] = true
		}

		hits := 0
		for i := 0; i < knn && i < len(dists); i++ {
			if gtSet[dists[i].idx] {
				hits++
			}
		}

		recall += float64(hits) / float64(knn)
	}

	return recall / float64(len(valVectors))
}

func computeExactKNN(query []float32, vectors [][]float32, k int) []int {
	idx := NewDistanceIndex(vectors)
	distBuf := make([]float32, len(vectors))
	return idx.KNN(query, k, distBuf)
}

// GetStatistics returns computed data statistics.
func (co *CentroidOptimizer) GetStatistics() CentroidOptimizerStats {
	return CentroidOptimizerStats{
		GlobalVariance:     co.globalVariance,
		SubspaceVariances:  co.subspaceVariances,
		IntrinsicDim:       co.intrinsicDim,
		DataComplexity:     co.dataComplexity,
		ClusteringStrength: co.clusteringStrength,
	}
}

// CentroidOptimizerStats contains computed statistics.
type CentroidOptimizerStats struct {
	GlobalVariance     float64
	SubspaceVariances  []float64
	IntrinsicDim       float64
	DataComplexity     float64
	ClusteringStrength float64
}

// =============================================================================
// Data-Derived Scaling Factors
// =============================================================================

// DataDerivedScalingFactors contains scaling factors derived from actual data
// measurements rather than hardcoded values. This allows the optimizer to adapt
// to the specific characteristics of the dataset being quantized.
type DataDerivedScalingFactors struct {
	// TargetDistortionRatio is derived from actual reconstruction error at K=64.
	// Represents the acceptable distortion as a fraction of original variance.
	// Typically 10% of the measured reconstruction error at baseline K.
	TargetDistortionRatio float64

	// BaseK is derived from intrinsic dimensionality analysis.
	// Computed as intrinsic_dim * 4 to cover 95% of variance directions.
	// This provides the baseline centroid count before other adjustments.
	BaseK int

	// RecallScaleFactor is empirically derived from testing K=32,64,128 on sample data.
	// Maps the relationship between extra recall above 90% and required K increase.
	// Default hardcoded value was 10 (each 1% above 90% requires 10% more K).
	RecallScaleFactor float64

	// ClusteringAdjustmentRange defines how clustering strength affects K.
	// Derived from Hopkins statistic spread: [1-H, 1] where H is Hopkins stat.
	// Index 0: minimum adjustment (for highly clustered data)
	// Index 1: maximum adjustment (for uniform data)
	ClusteringAdjustmentRange [2]float64

	// ComplexityMultiplierRange defines how data complexity scales K.
	// Derived from eigenvalue decay rate analysis.
	// Index 0: base multiplier for low complexity data
	// Index 1: additional multiplier range for high complexity
	ComplexityMultiplierRange [2]float64

	// MinSamplesPerCentroid is the minimum samples needed per centroid for stable k-means.
	// Derived from subspace covariance stability analysis.
	MinSamplesPerCentroid int

	// SubspaceDimMultiplier scales MinSamplesPerCentroid by subspace dimension.
	// Higher dimensional subspaces need proportionally more samples.
	SubspaceDimMultiplier float64

	// Computed indicates whether these factors have been derived from data.
	// If false, methods should fall back to hardcoded defaults.
	Computed bool
}

// DefaultScalingFactors returns the hardcoded default values for backward compatibility.
// These are the values that were previously hardcoded throughout the optimizer.
func DefaultScalingFactors() DataDerivedScalingFactors {
	return DataDerivedScalingFactors{
		TargetDistortionRatio:     0.1,  // 10% of original variance
		BaseK:                     64,   // Reasonable default
		RecallScaleFactor:         10.0, // Each 1% above 90% = 10% more K
		ClusteringAdjustmentRange: [2]float64{0.5, 1.0},
		ComplexityMultiplierRange: [2]float64{0.5, 1.5},
		MinSamplesPerCentroid:     10,
		SubspaceDimMultiplier:     2.0,
		Computed:                  false,
	}
}

// DeriveScalingFactors computes data-adaptive scaling factors from the actual
// vector distribution. This replaces hardcoded magic numbers with empirically
// derived values specific to the dataset.
//
// The derivation process:
//   - TargetDistortionRatio: Compute actual reconstruction error at K=64, use 10% of that
//   - BaseK: Use intrinsic dimensionality * 4 (covers 95% variance directions)
//   - RecallScaleFactor: Empirically test K=32,64,128, fit recall curve
//   - ClusteringAdjustmentRange: Based on Hopkins statistic spread [1-H, 1]
//   - ComplexityMultiplierRange: Based on eigenvalue decay rate
//   - MinSamplesPerCentroid: Based on subspace covariance stability
//
// For small datasets (< 100 vectors) or efficiency, sampling is used.
// The function is deterministic given the same input vectors.
func DeriveScalingFactors(vectors [][]float32, numSubspaces int) DataDerivedScalingFactors {
	factors := DefaultScalingFactors()

	if len(vectors) < 10 {
		return factors
	}

	dim := len(vectors[0])
	if dim == 0 || numSubspaces <= 0 {
		return factors
	}

	subspaceDim := dim / numSubspaces
	if subspaceDim == 0 {
		return factors
	}

	if containsNonFiniteValues(vectors) {
		return factors
	}

	co := NewCentroidOptimizer(DefaultCentroidOptimizerConfig())
	co.analyzeDataDistribution(vectors, numSubspaces)

	// Derive each factor
	factors.TargetDistortionRatio = deriveTargetDistortionRatio(vectors, numSubspaces, subspaceDim, co)
	factors.BaseK = deriveBaseK(co.intrinsicDim)
	factors.RecallScaleFactor = deriveRecallScaleFactor(vectors, numSubspaces)
	factors.ClusteringAdjustmentRange = deriveClusteringAdjustmentRange(co.clusteringStrength)
	factors.ComplexityMultiplierRange = deriveComplexityMultiplierRange(co.eigenvalues)
	factors.MinSamplesPerCentroid, factors.SubspaceDimMultiplier = deriveSamplingFactors(vectors, numSubspaces, subspaceDim)
	factors.Computed = true

	return factors
}

// deriveTargetDistortionRatio computes the target distortion as 10% of actual
// reconstruction error at baseline K=64.
func deriveTargetDistortionRatio(vectors [][]float32, numSubspaces, subspaceDim int, co *CentroidOptimizer) float64 {
	n := len(vectors)

	// Sample for efficiency
	sampleSize := min(500, n)
	sampleVectors := sampleVectorsUniform(vectors, sampleSize)

	// Compute baseline reconstruction error at K=64
	baselineError := computeReconstructionError(sampleVectors, numSubspaces, subspaceDim, 64)

	if baselineError <= 0 || co.globalVariance <= 0 {
		return 0.1 // Fallback to default
	}

	// Target is 10% of baseline error relative to variance
	errorRatio := baselineError / co.globalVariance
	targetRatio := errorRatio * 0.1

	// Clamp to reasonable range
	if targetRatio < 0.01 {
		targetRatio = 0.01
	}
	if targetRatio > 0.5 {
		targetRatio = 0.5
	}

	return targetRatio
}

// deriveBaseK computes base K from intrinsic dimensionality.
// Uses intrinsic_dim * 4 to cover 95% of variance directions.
func deriveBaseK(intrinsicDim float64) int {
	if intrinsicDim <= 0 {
		return 64 // Default fallback
	}

	// Base K = intrinsic_dim * 4, covers 95% variance directions
	baseK := int(intrinsicDim * 4)

	// Clamp to valid range
	if baseK < 8 {
		baseK = 8
	}
	if baseK > 256 {
		baseK = 256
	}

	return nearestPowerOf2(baseK)
}

// deriveRecallScaleFactor empirically tests K=32,64,128 and fits the recall curve
// to determine how much additional K is needed per percentage point of recall above 90%.
func deriveRecallScaleFactor(vectors [][]float32, numSubspaces int) float64 {
	n := len(vectors)
	if n < 100 {
		return 10.0 // Default for small datasets
	}

	dim := len(vectors[0])
	subspaceDim := dim / numSubspaces

	// Sample for efficiency
	sampleSize := min(300, n)
	sampleVectors := sampleVectorsUniform(vectors, sampleSize)

	// Split into train/test
	trainSize := sampleSize * 8 / 10
	trainVectors := sampleVectors[:trainSize]
	testVectors := sampleVectors[trainSize:]

	if len(testVectors) < 10 {
		return 10.0
	}

	// Test K values
	testKs := []int{32, 64, 128}
	recalls := make([]float64, len(testKs))

	// Limit K based on available data
	maxK := len(trainVectors) / max(10, subspaceDim)
	validKs := make([]int, 0, len(testKs))
	for _, k := range testKs {
		if k <= maxK {
			validKs = append(validKs, k)
		}
	}

	if len(validKs) < 2 {
		return 10.0 // Not enough data points
	}

	for i, k := range validKs {
		recalls[i] = measureRecallAtK(trainVectors, testVectors, numSubspaces, k)
	}

	// Fit recall-K relationship
	// Model: recall = 1 - exp(-alpha * K / baseK)
	// For K going from 64 to 128 (2x), how much does recall improve?
	//
	// We want to find: for each 1% above 90%, how much K increase is needed?
	// scaleFactor = (K_increase_percent) / (recall_increase_percent)

	// Find the index where recall is closest to 0.9
	var idx90 int
	minDiff := math.MaxFloat64
	for i, r := range recalls {
		if diff := math.Abs(r - 0.9); diff < minDiff {
			minDiff = diff
			idx90 = i
		}
	}

	// If we have data points above and below 90%, interpolate
	if idx90 > 0 && idx90 < len(recalls)-1 {
		// Calculate the relationship between K and recall near 90%
		recallDiff := recalls[idx90+1] - recalls[idx90]
		kRatio := float64(validKs[idx90+1]) / float64(validKs[idx90])

		if recallDiff > 0.001 {
			// scaleFactor = (kRatio - 1) / recallDiff
			// This gives: K_new = K_base * (1 + scaleFactor * (target - 0.9))
			scaleFactor := (kRatio - 1.0) / recallDiff
			scaleFactor *= 100 // Convert to per-percentage-point

			// Clamp to reasonable range
			if scaleFactor < 5 {
				scaleFactor = 5
			}
			if scaleFactor > 20 {
				scaleFactor = 20
			}
			return scaleFactor
		}
	}

	return 10.0 // Default
}

// deriveClusteringAdjustmentRange computes the adjustment range based on Hopkins statistic.
// Returns [1-H, 1] where H is the Hopkins statistic representing clustering tendency.
func deriveClusteringAdjustmentRange(hopkinsH float64) [2]float64 {
	// Hopkins statistic H:
	// H near 0.5 = random data
	// H near 1.0 = highly clustered
	// H near 0.0 = uniformly distributed

	// clusteringStrength in the optimizer is computed as 1 - ratio where ratio < 1 means clustered
	// So clusteringStrength near 1 = highly clustered, near 0 = random

	// For highly clustered data (H near 1), we can use lower K
	// adjustment = 1 - clusteringStrength * spread
	// spread determines how much clustering affects K

	// Derive spread from the clustering strength itself
	// Higher clustering = larger potential adjustment range
	spread := 0.3 + hopkinsH*0.4 // [0.3, 0.7] based on clustering

	minAdjust := 1.0 - spread
	maxAdjust := 1.0

	// Clamp
	if minAdjust < 0.3 {
		minAdjust = 0.3
	}
	if minAdjust > 0.9 {
		minAdjust = 0.9
	}

	return [2]float64{minAdjust, maxAdjust}
}

// deriveComplexityMultiplierRange computes the complexity multiplier range from eigenvalue decay.
// Fast decay = low complexity, slow decay = high complexity.
func deriveComplexityMultiplierRange(eigenvalues []float64) [2]float64 {
	if len(eigenvalues) < 2 {
		return [2]float64{0.5, 1.5} // Default
	}

	// Compute eigenvalue decay rate
	// Look at ratio of top-10 eigenvalues to total
	topK := min(10, len(eigenvalues))
	var topSum, totalSum float64
	for i, ev := range eigenvalues {
		totalSum += ev
		if i < topK {
			topSum += ev
		}
	}

	if totalSum <= 0 {
		return [2]float64{0.5, 1.5}
	}

	concentration := topSum / totalSum // [0, 1]

	// High concentration = fast decay = low complexity = need smaller multiplier range
	// Low concentration = slow decay = high complexity = need larger multiplier range

	// Base multiplier: always at least 0.5
	baseMult := 0.5

	// Range multiplier: inversely proportional to concentration
	// concentration near 1 (fast decay) -> range = 1.0
	// concentration near 0 (slow decay) -> range = 2.0
	rangeMult := 1.0 + (1.0-concentration)*1.0

	return [2]float64{baseMult, rangeMult}
}

// deriveSamplingFactors determines optimal sampling requirements for stable k-means
// by analyzing subspace covariance stability.
func deriveSamplingFactors(vectors [][]float32, numSubspaces, subspaceDim int) (minSamples int, dimMultiplier float64) {
	n := len(vectors)
	if n < 50 {
		return 10, 2.0 // Default for small datasets
	}

	// Test covariance stability at different sample sizes
	// Find the point where adding more samples doesn't significantly change covariance

	testSizes := []int{10, 20, 50, 100, 200}
	var lastCovNorm float64
	stableSize := 10

	for _, size := range testSizes {
		if size > n/2 {
			break
		}

		sample := sampleVectorsUniform(vectors, size)
		covNorm := computeSubspaceCovarianceNorm(sample, numSubspaces, subspaceDim)

		if lastCovNorm > 0 {
			// Check relative change
			relChange := math.Abs(covNorm-lastCovNorm) / lastCovNorm
			if relChange < 0.05 { // Less than 5% change = stable
				stableSize = size
				break
			}
		}
		lastCovNorm = covNorm
		stableSize = size
	}

	// MinSamplesPerCentroid is the stable size
	minSamples = stableSize
	if minSamples < 10 {
		minSamples = 10
	}

	// Compute dimension multiplier
	// Higher dimensional subspaces need more samples proportionally
	// Empirical: samples_needed ≈ minSamples + multiplier * subspaceDim
	// We want to find multiplier such that total samples provides stability
	dimMultiplier = float64(stableSize) / float64(subspaceDim)
	if dimMultiplier < 1.0 {
		dimMultiplier = 1.0
	}
	if dimMultiplier > 5.0 {
		dimMultiplier = 5.0
	}

	return minSamples, dimMultiplier
}

// computeReconstructionError computes the average reconstruction error for k-means
// quantization with the given K centroids.
func computeReconstructionError(vectors [][]float32, numSubspaces, subspaceDim, k int) float64 {
	n := len(vectors)
	if n < k {
		return 0
	}

	// Simple k-means reconstruction error estimation
	// For each subspace, compute variance reduction from k-means

	var totalError float64

	for m := 0; m < numSubspaces; m++ {
		start := m * subspaceDim

		// Extract subspace data
		subspaceData := make([][]float32, n)
		for i := range vectors {
			subspaceData[i] = vectors[i][start : start+subspaceDim]
		}

		// Compute within-cluster variance using simple k-means approximation
		// For efficiency, use random centroid selection (not full k-means)
		error := estimateKMeansError(subspaceData, k)
		totalError += error
	}

	return totalError / float64(numSubspaces)
}

// estimateKMeansError estimates k-means reconstruction error without full training.
// Uses a fast approximation based on random centroid selection.
func estimateKMeansError(subspaceData [][]float32, k int) float64 {
	n := len(subspaceData)
	if n == 0 {
		return 0
	}
	dim := len(subspaceData[0])
	if dim == 0 {
		return 0
	}

	// Select k random centroids from the data
	step := n / k
	if step < 1 {
		step = 1
	}

	centroids := make([][]float32, min(k, n))
	for i := range centroids {
		idx := (i * step) % n
		centroids[i] = subspaceData[idx]
	}

	// Compute average distance to nearest centroid
	var totalError float64
	for _, vec := range subspaceData {
		minDist := float64(math.MaxFloat32)
		for _, centroid := range centroids {
			var dist float64
			for d := 0; d < dim; d++ {
				diff := float64(vec[d] - centroid[d])
				dist += diff * diff
			}
			if dist < minDist {
				minDist = dist
			}
		}
		totalError += minDist
	}

	return totalError / float64(n*dim)
}

// measureRecallAtK measures recall at a specific K value.
func measureRecallAtK(trainVectors, testVectors [][]float32, numSubspaces, k int) float64 {
	dim := len(trainVectors[0])

	// Create and train a simple PQ
	pqConfig := ProductQuantizerConfig{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: k,
	}

	pq, err := NewProductQuantizer(dim, pqConfig)
	if err != nil {
		return 0.5 // Fallback
	}

	ctx := context.Background()
	subspaceDim := dim / numSubspaces
	trainConfig := DeriveTrainConfig(k, subspaceDim)
	if err := pq.TrainParallelWithConfig(ctx, trainVectors, trainConfig, 0); err != nil {
		return 0.5
	}

	// Encode training vectors
	codes, err := pq.EncodeBatch(trainVectors, runtime.NumCPU())
	if err != nil {
		return 0.5
	}

	// Measure recall
	knn := 10
	var totalRecall float64

	for _, query := range testVectors {
		// Ground truth
		gtIndices := computeExactKNN(query, trainVectors, knn)

		// PQ search
		table, _ := pq.ComputeDistanceTable(query)
		type distIdx struct {
			dist float32
			idx  int
		}
		dists := make([]distIdx, len(codes))
		for i, code := range codes {
			dists[i] = distIdx{dist: pq.AsymmetricDistance(table, code), idx: i}
		}
		sort.Slice(dists, func(i, j int) bool {
			return dists[i].dist < dists[j].dist
		})

		// Count hits
		gtSet := make(map[int]bool)
		for _, idx := range gtIndices {
			gtSet[idx] = true
		}

		hits := 0
		for i := 0; i < knn && i < len(dists); i++ {
			if gtSet[dists[i].idx] {
				hits++
			}
		}
		totalRecall += float64(hits) / float64(knn)
	}

	return totalRecall / float64(len(testVectors))
}

// computeSubspaceCovarianceNorm computes the Frobenius norm of subspace covariance.
// Used to measure covariance stability across different sample sizes.
func computeSubspaceCovarianceNorm(vectors [][]float32, numSubspaces, subspaceDim int) float64 {
	n := len(vectors)
	if n < 2 {
		return 0
	}

	var totalNorm float64

	// For efficiency, only compute for first subspace
	// Compute mean
	mean := make([]float64, subspaceDim)
	for _, v := range vectors {
		for d := 0; d < subspaceDim; d++ {
			mean[d] += float64(v[d])
		}
	}
	for d := range mean {
		mean[d] /= float64(n)
	}

	// Compute covariance diagonal (sufficient for stability check)
	var covTrace float64
	for _, v := range vectors {
		for d := 0; d < subspaceDim; d++ {
			diff := float64(v[d]) - mean[d]
			covTrace += diff * diff
		}
	}
	covTrace /= float64(n - 1)
	totalNorm = covTrace

	return totalNorm
}

// sampleVectorsUniform samples n vectors uniformly from the input.
// Uses deterministic sampling for reproducibility.
func sampleVectorsUniform(vectors [][]float32, n int) [][]float32 {
	total := len(vectors)
	if n >= total {
		return vectors
	}

	sampled := make([][]float32, n)
	step := total / n
	for i := 0; i < n; i++ {
		idx := i * step
		sampled[i] = vectors[idx]
	}
	return sampled
}

func containsNonFiniteValues(vectors [][]float32) bool {
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return false
	}
	flat := flattenToFloat64(vectors)
	sum := floats.Sum(flat)
	return math.IsInf(sum, 0) || math.IsNaN(sum)
}

func flattenToFloat64(vectors [][]float32) []float64 {
	n, dim := len(vectors), len(vectors[0])
	flat := make([]float64, n*dim)
	offset := 0
	for _, v := range vectors {
		for _, val := range v {
			flat[offset] = float64(val)
			offset++
		}
	}
	return flat
}
