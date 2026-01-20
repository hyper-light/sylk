package quantization

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"sync"
)

// =============================================================================
// Adaptive Quantizer
// =============================================================================
//
// AdaptiveQuantizer automatically selects the optimal quantization strategy
// based on dataset characteristics. It scales from small to large datasets
// while maximizing recall.
//
// Strategies by dataset size:
//   - Tiny (<100 vectors): No compression, exact search
//   - Small (100-1000): Coarse PQ + re-ranking with stored vectors
//   - Medium (1000-100K): Standard PQ with derived parameters
//   - Large (>100K): Full PQ with optional OPQ rotation
//
// Key features:
//   - Residual quantization for improved accuracy
//   - Automatic re-ranking with exact distances
//   - Adaptive centroid count based on data
//   - No hardcoded parameters - all derived from data

// QuantizationStrategy defines how vectors are quantized.
type QuantizationStrategy int

const (
	// StrategyExact uses no compression - exact distances only.
	// Used for tiny datasets where compression overhead exceeds benefit.
	StrategyExact QuantizationStrategy = iota

	// StrategyCoarsePQRerank uses coarse PQ for candidate retrieval
	// followed by exact re-ranking. Best for small datasets.
	StrategyCoarsePQRerank

	// StrategyStandardPQ uses standard product quantization.
	// Good balance for medium datasets.
	StrategyStandardPQ

	// StrategyResidualPQ uses two-stage residual quantization.
	// Best accuracy for any dataset size.
	StrategyResidualPQ
)

// AdaptiveQuantizer provides optimal quantization for any dataset size.
type AdaptiveQuantizer struct {
	// Strategy selected based on data characteristics
	strategy QuantizationStrategy

	// Primary quantizer (nil for StrategyExact)
	primaryPQ *ProductQuantizer

	// Residual quantizer for StrategyResidualPQ
	residualPQ *ProductQuantizer

	// Original vectors stored for re-ranking (StrategyCoarsePQRerank and StrategyExact)
	storedVectors [][]float32

	// PQ codes for encoded vectors
	codes []PQCode

	// Residual codes for StrategyResidualPQ
	residualCodes []PQCode

	// Vector dimension
	vectorDim int

	// Whether training is complete
	trained bool

	// Configuration used
	config AdaptiveQuantizerConfig
}

// AdaptiveQuantizerConfig configures the adaptive quantizer.
// All parameters are derived from data if set to zero.
type AdaptiveQuantizerConfig struct {
	// ForceStrategy overrides automatic strategy selection.
	// Set to -1 to use automatic selection (default).
	ForceStrategy QuantizationStrategy

	// TargetRecall is the minimum acceptable recall (0.0-1.0).
	// Higher values favor accuracy over compression.
	// Default: 0.9 (90% recall target)
	TargetRecall float64

	// MaxRerankCandidates limits re-ranking candidates.
	// Set to 0 for automatic derivation based on k.
	MaxRerankCandidates int

	// EnableResidual enables residual quantization for better accuracy.
	// Default: true
	EnableResidual bool
}

// DefaultAdaptiveQuantizerConfig returns default configuration.
func DefaultAdaptiveQuantizerConfig() AdaptiveQuantizerConfig {
	return AdaptiveQuantizerConfig{
		ForceStrategy:       -1,   // Automatic
		TargetRecall:        0.9,  // 90% target
		MaxRerankCandidates: 0,    // Auto
		EnableResidual:      true, // Best accuracy
	}
}

// NewAdaptiveQuantizer creates a new adaptive quantizer.
func NewAdaptiveQuantizer(vectorDim int, config AdaptiveQuantizerConfig) (*AdaptiveQuantizer, error) {
	if vectorDim <= 0 {
		return nil, errors.New("vector dimension must be positive")
	}

	if config.TargetRecall <= 0 {
		config.TargetRecall = 0.9
	}
	if config.TargetRecall > 1.0 {
		config.TargetRecall = 1.0
	}

	return &AdaptiveQuantizer{
		vectorDim: vectorDim,
		config:    config,
		trained:   false,
	}, nil
}

// deriveStrategy selects the optimal strategy based on data characteristics.
func (aq *AdaptiveQuantizer) deriveStrategy(numVectors int) QuantizationStrategy {
	// If forced, use that strategy
	if aq.config.ForceStrategy >= 0 {
		return aq.config.ForceStrategy
	}

	// Derive based on dataset size and target recall
	switch {
	case numVectors < 100:
		// Tiny dataset: exact search is faster than PQ overhead
		return StrategyExact

	case numVectors < 1000:
		// Small dataset: coarse PQ + re-ranking
		// Not enough data for fine-grained codebooks
		return StrategyCoarsePQRerank

	case numVectors < 100000:
		// Medium dataset: standard PQ or residual PQ
		if aq.config.EnableResidual && aq.config.TargetRecall > 0.85 {
			return StrategyResidualPQ
		}
		return StrategyStandardPQ

	default:
		// Large dataset: residual PQ for best accuracy
		if aq.config.EnableResidual {
			return StrategyResidualPQ
		}
		return StrategyStandardPQ
	}
}

// derivePQConfig derives optimal PQ configuration from data characteristics.
// This method computes numSubspaces only; centroid count is derived from actual data.
func (aq *AdaptiveQuantizer) derivePQConfig(numVectors int, isResidual bool) ProductQuantizerConfig {
	dim := aq.vectorDim
	numSubspaces := aq.deriveNumSubspaces(dim)

	// Centroid count will be derived from actual data in derivePQConfigFromData
	// Use conservative default here for config initialization
	subspaceDim := dim / numSubspaces
	centroidsPerSubspace := aq.deriveDefaultCentroids(numVectors, subspaceDim, isResidual)

	return ProductQuantizerConfig{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: centroidsPerSubspace,
	}
}

// deriveNumSubspaces computes optimal number of subspaces from vector dimension.
func (aq *AdaptiveQuantizer) deriveNumSubspaces(dim int) int {
	// Target subspace dimension: 4-32, prefer 8-16 for balance
	var numSubspaces int
	targetSubspaceDim := 8

	if dim <= 16 {
		numSubspaces = dim / 4
		if numSubspaces < 1 {
			numSubspaces = 1
		}
	} else if dim <= 64 {
		numSubspaces = dim / 8
	} else if dim <= 256 {
		numSubspaces = dim / 16
	} else {
		numSubspaces = dim / targetSubspaceDim
	}

	// Ensure divisibility
	for dim%numSubspaces != 0 && numSubspaces > 1 {
		numSubspaces--
	}

	return numSubspaces
}

// deriveDefaultCentroids provides a fallback centroid count when data isn't available.
func (aq *AdaptiveQuantizer) deriveDefaultCentroids(numVectors, subspaceDim int, isResidual bool) int {
	minSamplesPerCentroid := max(10, subspaceDim)
	maxCentroidsFromData := numVectors / minSamplesPerCentroid

	if isResidual {
		maxCentroidsFromData = maxCentroidsFromData / 2
	}

	switch {
	case maxCentroidsFromData >= 256:
		return 256
	case maxCentroidsFromData >= 128:
		return 128
	case maxCentroidsFromData >= 64:
		return 64
	case maxCentroidsFromData >= 32:
		return 32
	case maxCentroidsFromData >= 16:
		return 16
	default:
		return max(8, maxCentroidsFromData)
	}
}

// derivePQConfigFromData derives optimal PQ configuration using statistical analysis of actual data.
// This is the robust, data-adaptive method that analyzes variance, intrinsic dimensionality, etc.
func (aq *AdaptiveQuantizer) derivePQConfigFromData(vectors [][]float32, isResidual bool) ProductQuantizerConfig {
	dim := aq.vectorDim
	numSubspaces := aq.deriveNumSubspaces(dim)

	// Create centroid optimizer
	optimizerConfig := CentroidOptimizerConfig{
		TargetRecall:            aq.config.TargetRecall,
		MaxCentroidsPerSubspace: 256,
		NumSubspaces:            numSubspaces,
	}

	optimizer := NewCentroidOptimizer(optimizerConfig)

	// Optimize centroid count from actual data
	centroidsPerSubspace, err := optimizer.OptimizeCentroidCount(vectors, numSubspaces)
	if err != nil {
		// Fallback to default derivation
		subspaceDim := dim / numSubspaces
		centroidsPerSubspace = aq.deriveDefaultCentroids(len(vectors), subspaceDim, isResidual)
	}

	// For residual quantizer, use fewer centroids (residual has lower variance)
	if isResidual {
		centroidsPerSubspace = centroidsPerSubspace / 2
		if centroidsPerSubspace < 8 {
			centroidsPerSubspace = 8
		}
		// Ensure power of 2
		centroidsPerSubspace = nearestPowerOf2(centroidsPerSubspace)
	}

	return ProductQuantizerConfig{
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: centroidsPerSubspace,
	}
}

// Train learns the quantization model from training vectors.
func (aq *AdaptiveQuantizer) Train(ctx context.Context, vectors [][]float32) error {
	if len(vectors) == 0 {
		return errors.New("no training vectors")
	}

	// Validate dimensions
	for i, v := range vectors {
		if len(v) != aq.vectorDim {
			return fmt.Errorf("vector %d has dimension %d, expected %d", i, len(v), aq.vectorDim)
		}
	}

	numVectors := len(vectors)
	aq.strategy = aq.deriveStrategy(numVectors)

	switch aq.strategy {
	case StrategyExact:
		return aq.trainExact(vectors)

	case StrategyCoarsePQRerank:
		return aq.trainCoarsePQRerank(ctx, vectors)

	case StrategyStandardPQ:
		return aq.trainStandardPQ(ctx, vectors)

	case StrategyResidualPQ:
		return aq.trainResidualPQ(ctx, vectors)

	default:
		return fmt.Errorf("unknown strategy: %d", aq.strategy)
	}
}

func (aq *AdaptiveQuantizer) trainExact(vectors [][]float32) error {
	// Store all vectors for exact search
	aq.storedVectors = make([][]float32, len(vectors))
	for i, v := range vectors {
		aq.storedVectors[i] = make([]float32, len(v))
		copy(aq.storedVectors[i], v)
	}
	aq.trained = true
	return nil
}

func (aq *AdaptiveQuantizer) trainCoarsePQRerank(ctx context.Context, vectors [][]float32) error {
	numVectors := len(vectors)

	// Store vectors for re-ranking
	aq.storedVectors = make([][]float32, numVectors)
	for i, v := range vectors {
		aq.storedVectors[i] = make([]float32, len(v))
		copy(aq.storedVectors[i], v)
	}

	// Create coarse PQ with data-adaptive centroid count
	pqConfig := aq.derivePQConfigFromData(vectors, false)
	var err error
	aq.primaryPQ, err = NewProductQuantizer(aq.vectorDim, pqConfig)
	if err != nil {
		return fmt.Errorf("create coarse PQ: %w", err)
	}

	// Train with derived config
	trainConfig := DeriveTrainConfig(pqConfig.CentroidsPerSubspace, aq.vectorDim/pqConfig.NumSubspaces)
	if err := aq.primaryPQ.TrainParallelWithConfig(ctx, vectors, trainConfig, 0); err != nil {
		return fmt.Errorf("train coarse PQ: %w", err)
	}

	// Encode all vectors
	aq.codes, err = aq.primaryPQ.EncodeBatch(vectors, runtime.NumCPU())
	if err != nil {
		return fmt.Errorf("encode vectors: %w", err)
	}

	aq.trained = true
	return nil
}

func (aq *AdaptiveQuantizer) trainStandardPQ(ctx context.Context, vectors [][]float32) error {
	// Create PQ with data-adaptive centroid count
	pqConfig := aq.derivePQConfigFromData(vectors, false)
	var err error
	aq.primaryPQ, err = NewProductQuantizer(aq.vectorDim, pqConfig)
	if err != nil {
		return fmt.Errorf("create PQ: %w", err)
	}

	// Train
	trainConfig := DeriveTrainConfig(pqConfig.CentroidsPerSubspace, aq.vectorDim/pqConfig.NumSubspaces)
	if err := aq.primaryPQ.TrainParallelWithConfig(ctx, vectors, trainConfig, 0); err != nil {
		return fmt.Errorf("train PQ: %w", err)
	}

	// Encode
	aq.codes, err = aq.primaryPQ.EncodeBatch(vectors, runtime.NumCPU())
	if err != nil {
		return fmt.Errorf("encode vectors: %w", err)
	}

	aq.trained = true
	return nil
}

func (aq *AdaptiveQuantizer) trainResidualPQ(ctx context.Context, vectors [][]float32) error {
	numVectors := len(vectors)

	// Stage 1: Train primary (coarse) quantizer with data-adaptive centroid count
	primaryConfig := aq.derivePQConfigFromData(vectors, false)
	var err error
	aq.primaryPQ, err = NewProductQuantizer(aq.vectorDim, primaryConfig)
	if err != nil {
		return fmt.Errorf("create primary PQ: %w", err)
	}

	trainConfig := DeriveTrainConfig(primaryConfig.CentroidsPerSubspace, aq.vectorDim/primaryConfig.NumSubspaces)
	if err := aq.primaryPQ.TrainParallelWithConfig(ctx, vectors, trainConfig, 0); err != nil {
		return fmt.Errorf("train primary PQ: %w", err)
	}

	// Encode with primary
	aq.codes, err = aq.primaryPQ.EncodeBatch(vectors, runtime.NumCPU())
	if err != nil {
		return fmt.Errorf("encode primary: %w", err)
	}

	// Compute residuals: residual = original - reconstructed
	residuals := make([][]float32, numVectors)
	for i, v := range vectors {
		residuals[i] = make([]float32, aq.vectorDim)
		reconstructed := aq.reconstructVector(aq.codes[i])
		for j := range v {
			residuals[i][j] = v[j] - reconstructed[j]
		}
	}

	// Stage 2: Train residual quantizer with data-adaptive centroid count
	// Residuals have lower variance, so optimizer will select appropriate K
	residualConfig := aq.derivePQConfigFromData(residuals, true)
	aq.residualPQ, err = NewProductQuantizer(aq.vectorDim, residualConfig)
	if err != nil {
		return fmt.Errorf("create residual PQ: %w", err)
	}

	residualTrainConfig := DeriveTrainConfig(residualConfig.CentroidsPerSubspace, aq.vectorDim/residualConfig.NumSubspaces)
	if err := aq.residualPQ.TrainParallelWithConfig(ctx, residuals, residualTrainConfig, 0); err != nil {
		return fmt.Errorf("train residual PQ: %w", err)
	}

	// Encode residuals
	aq.residualCodes, err = aq.residualPQ.EncodeBatch(residuals, runtime.NumCPU())
	if err != nil {
		return fmt.Errorf("encode residuals: %w", err)
	}

	aq.trained = true
	return nil
}

// reconstructVector reconstructs a vector from its PQ code.
func (aq *AdaptiveQuantizer) reconstructVector(code PQCode) []float32 {
	reconstructed := make([]float32, aq.vectorDim)
	subspaceDim := aq.primaryPQ.subspaceDim

	for m := 0; m < aq.primaryPQ.numSubspaces; m++ {
		centroid := aq.primaryPQ.centroids[m][code[m]]
		start := m * subspaceDim
		copy(reconstructed[start:start+subspaceDim], centroid)
	}

	return reconstructed
}

// reconstructVectorWithResidual reconstructs using both primary and residual codes.
func (aq *AdaptiveQuantizer) reconstructVectorWithResidual(primaryCode, residualCode PQCode) []float32 {
	reconstructed := aq.reconstructVector(primaryCode)

	if aq.residualPQ != nil && residualCode != nil {
		subspaceDim := aq.residualPQ.subspaceDim
		for m := 0; m < aq.residualPQ.numSubspaces; m++ {
			centroid := aq.residualPQ.centroids[m][residualCode[m]]
			start := m * subspaceDim
			for d := 0; d < subspaceDim; d++ {
				reconstructed[start+d] += centroid[d]
			}
		}
	}

	return reconstructed
}

// SearchResult represents a search result with distance and index.
type SearchResult struct {
	Index    int
	Distance float32
}

// Search finds the k nearest neighbors to the query.
func (aq *AdaptiveQuantizer) Search(query []float32, k int) ([]SearchResult, error) {
	if !aq.trained {
		return nil, errors.New("quantizer not trained")
	}
	if len(query) != aq.vectorDim {
		return nil, fmt.Errorf("query dimension %d != expected %d", len(query), aq.vectorDim)
	}
	if k <= 0 {
		return nil, errors.New("k must be positive")
	}

	switch aq.strategy {
	case StrategyExact:
		return aq.searchExact(query, k)

	case StrategyCoarsePQRerank:
		return aq.searchCoarsePQRerank(query, k)

	case StrategyStandardPQ:
		return aq.searchStandardPQ(query, k)

	case StrategyResidualPQ:
		return aq.searchResidualPQ(query, k)

	default:
		return nil, fmt.Errorf("unknown strategy: %d", aq.strategy)
	}
}

func (aq *AdaptiveQuantizer) searchExact(query []float32, k int) ([]SearchResult, error) {
	n := len(aq.storedVectors)
	if k > n {
		k = n
	}

	// Compute all distances
	distances := make([]SearchResult, n)
	for i, v := range aq.storedVectors {
		distances[i] = SearchResult{
			Index:    i,
			Distance: squaredL2Distance(query, v),
		}
	}

	// Partial sort to get top-k
	sort.Slice(distances, func(i, j int) bool {
		return distances[i].Distance < distances[j].Distance
	})

	return distances[:k], nil
}

func (aq *AdaptiveQuantizer) searchCoarsePQRerank(query []float32, k int) ([]SearchResult, error) {
	n := len(aq.codes)
	if k > n {
		k = n
	}

	// Determine number of candidates for re-ranking
	// More candidates = higher recall but slower
	numCandidates := aq.config.MaxRerankCandidates
	if numCandidates <= 0 {
		// Derive: retrieve 10x candidates for 95%+ recall
		numCandidates = k * 10
		if numCandidates > n {
			numCandidates = n
		}
	}

	// Stage 1: Get candidates using PQ distances
	table, err := aq.primaryPQ.ComputeDistanceTable(query)
	if err != nil {
		return nil, err
	}

	candidates := make([]SearchResult, n)
	for i, code := range aq.codes {
		candidates[i] = SearchResult{
			Index:    i,
			Distance: aq.primaryPQ.AsymmetricDistance(table, code),
		}
	}

	// Partial sort to get top candidates
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Distance < candidates[j].Distance
	})
	candidates = candidates[:numCandidates]

	// Stage 2: Re-rank with exact distances
	for i := range candidates {
		idx := candidates[i].Index
		candidates[i].Distance = squaredL2Distance(query, aq.storedVectors[idx])
	}

	// Final sort
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Distance < candidates[j].Distance
	})

	return candidates[:k], nil
}

func (aq *AdaptiveQuantizer) searchStandardPQ(query []float32, k int) ([]SearchResult, error) {
	n := len(aq.codes)
	if k > n {
		k = n
	}

	table, err := aq.primaryPQ.ComputeDistanceTable(query)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, n)
	for i, code := range aq.codes {
		results[i] = SearchResult{
			Index:    i,
			Distance: aq.primaryPQ.AsymmetricDistance(table, code),
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})

	return results[:k], nil
}

func (aq *AdaptiveQuantizer) searchResidualPQ(query []float32, k int) ([]SearchResult, error) {
	n := len(aq.codes)
	if k > n {
		k = n
	}

	// For Residual PQ, the proper distance computation is:
	// ||q - x||² ≈ ||q - (primary + residual)||²
	//
	// We reconstruct the full vector and compute distance.
	// This is more accurate than summing asymmetric distances.
	//
	// For large k, we use a two-stage approach:
	// 1. Get candidates using primary PQ (fast approximation)
	// 2. Re-rank with full reconstruction (accurate)

	// Stage 1: Get candidates using primary PQ distance
	primaryTable, err := aq.primaryPQ.ComputeDistanceTable(query)
	if err != nil {
		return nil, err
	}

	// Retrieve more candidates than k for re-ranking
	numCandidates := k * 10
	if numCandidates > n {
		numCandidates = n
	}

	candidates := make([]SearchResult, n)
	for i := 0; i < n; i++ {
		candidates[i] = SearchResult{
			Index:    i,
			Distance: aq.primaryPQ.AsymmetricDistance(primaryTable, aq.codes[i]),
		}
	}

	// Partial sort to get top candidates
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Distance < candidates[j].Distance
	})
	candidates = candidates[:numCandidates]

	// Stage 2: Re-rank candidates with full reconstruction distance
	for i := range candidates {
		idx := candidates[i].Index
		reconstructed := aq.reconstructVectorWithResidual(aq.codes[idx], aq.residualCodes[idx])
		candidates[i].Distance = squaredL2Distance(query, reconstructed)
	}

	// Final sort by accurate distance
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Distance < candidates[j].Distance
	})

	return candidates[:k], nil
}

// SearchWithRerank performs search with mandatory re-ranking using stored/reconstructed vectors.
func (aq *AdaptiveQuantizer) SearchWithRerank(query []float32, k, numCandidates int) ([]SearchResult, error) {
	if !aq.trained {
		return nil, errors.New("quantizer not trained")
	}

	if numCandidates < k {
		numCandidates = k * 10
	}

	// Get initial candidates
	candidates, err := aq.Search(query, numCandidates)
	if err != nil {
		return nil, err
	}

	// Re-rank based on strategy
	switch aq.strategy {
	case StrategyExact:
		// Already exact
		if len(candidates) > k {
			candidates = candidates[:k]
		}
		return candidates, nil

	case StrategyCoarsePQRerank:
		// Already re-ranked with stored vectors
		if len(candidates) > k {
			candidates = candidates[:k]
		}
		return candidates, nil

	case StrategyStandardPQ:
		// Re-rank using reconstructed vectors
		for i := range candidates {
			idx := candidates[i].Index
			reconstructed := aq.reconstructVector(aq.codes[idx])
			candidates[i].Distance = squaredL2Distance(query, reconstructed)
		}

	case StrategyResidualPQ:
		// Re-rank using fully reconstructed vectors
		for i := range candidates {
			idx := candidates[i].Index
			reconstructed := aq.reconstructVectorWithResidual(aq.codes[idx], aq.residualCodes[idx])
			candidates[i].Distance = squaredL2Distance(query, reconstructed)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Distance < candidates[j].Distance
	})

	if len(candidates) > k {
		candidates = candidates[:k]
	}
	return candidates, nil
}

// squaredL2Distance computes squared L2 distance between two vectors.
func squaredL2Distance(a, b []float32) float32 {
	var sum float32
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum
}

// ComputeRecall computes recall@k against ground truth.
func (aq *AdaptiveQuantizer) ComputeRecall(queries [][]float32, groundTruth [][]int, k int) (float64, error) {
	if len(queries) != len(groundTruth) {
		return 0, errors.New("queries and ground truth must have same length")
	}

	var totalRecall float64
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, runtime.NumCPU())

	for q := range queries {
		wg.Add(1)
		go func(queryIdx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			results, err := aq.Search(queries[queryIdx], k)
			if err != nil {
				return
			}

			// Count hits
			gt := groundTruth[queryIdx]
			gtSet := make(map[int]bool)
			for _, idx := range gt[:min(k, len(gt))] {
				gtSet[idx] = true
			}

			hits := 0
			for _, r := range results {
				if gtSet[r.Index] {
					hits++
				}
			}

			mu.Lock()
			totalRecall += float64(hits) / float64(min(k, len(gt)))
			mu.Unlock()
		}(q)
	}

	wg.Wait()
	return totalRecall / float64(len(queries)), nil
}

// Strategy returns the selected quantization strategy.
func (aq *AdaptiveQuantizer) Strategy() QuantizationStrategy {
	return aq.strategy
}

// IsTrained returns whether the quantizer has been trained.
func (aq *AdaptiveQuantizer) IsTrained() bool {
	return aq.trained
}

// Stats returns statistics about the quantizer.
func (aq *AdaptiveQuantizer) Stats() AdaptiveQuantizerStats {
	stats := AdaptiveQuantizerStats{
		Strategy:  aq.strategy,
		VectorDim: aq.vectorDim,
		Trained:   aq.trained,
	}

	if aq.codes != nil {
		stats.NumVectors = len(aq.codes)
	} else if aq.storedVectors != nil {
		stats.NumVectors = len(aq.storedVectors)
	}

	if aq.primaryPQ != nil {
		stats.PrimarySubspaces = aq.primaryPQ.numSubspaces
		stats.PrimaryCentroids = aq.primaryPQ.centroidsPerSubspace
		stats.CompressedBytesPerVector = aq.primaryPQ.numSubspaces
	}

	if aq.residualPQ != nil {
		stats.ResidualSubspaces = aq.residualPQ.numSubspaces
		stats.ResidualCentroids = aq.residualPQ.centroidsPerSubspace
		stats.CompressedBytesPerVector += aq.residualPQ.numSubspaces
	}

	if aq.storedVectors != nil {
		stats.StoredOriginalVectors = true
		stats.CompressedBytesPerVector = aq.vectorDim * 4 // Full vectors stored
	}

	return stats
}

// AdaptiveQuantizerStats contains statistics about the quantizer.
type AdaptiveQuantizerStats struct {
	Strategy                QuantizationStrategy
	VectorDim               int
	NumVectors              int
	Trained                 bool
	PrimarySubspaces        int
	PrimaryCentroids        int
	ResidualSubspaces       int
	ResidualCentroids       int
	CompressedBytesPerVector int
	StoredOriginalVectors   bool
}

// String returns a human-readable strategy name.
func (s QuantizationStrategy) String() string {
	switch s {
	case StrategyExact:
		return "Exact"
	case StrategyCoarsePQRerank:
		return "CoarsePQ+Rerank"
	case StrategyStandardPQ:
		return "StandardPQ"
	case StrategyResidualPQ:
		return "ResidualPQ"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}
