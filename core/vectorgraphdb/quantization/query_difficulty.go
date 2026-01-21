package quantization

import (
	"math"
	"sync"

	"gonum.org/v1/gonum/stat"
)

// =============================================================================
// Query Difficulty Estimation for OOD Detection
// =============================================================================
//
// This module predicts search difficulty from query features before executing
// the actual search. It enables adaptive ef_search parameter tuning to balance
// accuracy and performance on a per-query basis.
//
// Key features:
//   - Statistical query analysis (variance, entropy)
//   - Per-partition difficulty tracking
//   - Self-supervised learning via search feedback
//   - Thread-safe for concurrent use
//
// Implementation follows GRAPH_OPTIMIZATIONS.md Section 2.3.

// QueryDifficultyEstimator predicts search difficulty from query features.
// It maintains running statistics from search history and uses them to
// estimate how many iterations a query will need.
type QueryDifficultyEstimator struct {
	mu sync.RWMutex

	avgQueryVariance    float64
	avgQueryEntropy     float64
	avgSearchIterations float64

	partitionCentroids  [][]float32
	partitionDifficulty []float64

	avgDistToCentroid float64
	stdDistToCentroid float64

	observationCount int
}

// NewQueryDifficultyEstimator creates a new estimator with partition centroids.
// The centroids define the partition structure for per-region difficulty tracking.
// Pass nil or empty slice if partition-based estimation is not needed.
func NewQueryDifficultyEstimator(centroids [][]float32) *QueryDifficultyEstimator {
	e := &QueryDifficultyEstimator{
		partitionCentroids:  centroids,
		partitionDifficulty: make([]float64, len(centroids)),
		avgQueryVariance:    1.0,
		avgQueryEntropy:     1.0,
		avgSearchIterations: 100.0,
	}

	// Initialize all partition difficulties to neutral (1.0)
	for i := range e.partitionDifficulty {
		e.partitionDifficulty[i] = 1.0
	}

	return e
}

// EstimateDifficulty predicts how difficult a query will be to search.
// Returns a value in [0.5, 2.0] where:
//   - 0.5 = easy query (distinctive, high variance, low entropy)
//   - 1.0 = average query
//   - 2.0 = hard query (uniform, low variance, high entropy, dense region)
//
// The estimate is based on three features:
//  1. Query variance: high variance = more distinctive = easier
//  2. Query entropy: high entropy = more uniform = harder
//  3. Partition difficulty: learned from search history
func (e *QueryDifficultyEstimator) EstimateDifficulty(query []float32) float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Feature 1: Query variance (high = more distinctive = easier)
	variance := e.computeVariance(query)
	varianceScore := e.avgQueryVariance / math.Max(0.001, variance)

	// Feature 2: Query entropy (high = more uniform = harder)
	entropy := e.computeEntropy(query)
	entropyScore := entropy / math.Max(0.001, e.avgQueryEntropy)

	// Feature 3: Distance to nearest partition centroid (far = harder)
	partitionIdx := e.findNearestPartition(query)
	partitionScore := 1.0
	if partitionIdx >= 0 && partitionIdx < len(e.partitionDifficulty) {
		partitionScore = e.partitionDifficulty[partitionIdx]
	}

	// Combine features (weights learned from history)
	// Variance and entropy each contribute 30%, partition history 40%
	const (
		varianceWeight  = 0.3
		entropyWeight   = 0.3
		partitionWeight = 0.4
	)
	difficulty := varianceWeight*varianceScore + entropyWeight*entropyScore + partitionWeight*partitionScore

	// Clamp to [0.5, 2.0] range
	return math.Max(0.5, math.Min(2.0, difficulty))
}

// AdaptEfSearch scales ef based on predicted difficulty.
// For easy queries, returns a smaller ef (faster search).
// For hard queries, returns a larger ef (more accurate search).
func (e *QueryDifficultyEstimator) AdaptEfSearch(baseEf int, query []float32) int {
	difficulty := e.EstimateDifficulty(query)
	return int(float64(baseEf) * difficulty)
}

// RecordSearch records a search observation for self-supervised learning.
// Call this after each search with the actual iterations used and result quality.
// The estimator will use this feedback to improve future predictions.
//
// Parameters:
//   - query: the query vector that was searched
//   - iterationsUsed: actual number of iterations/candidates evaluated
//   - resultQuality: quality metric (e.g., top-1 similarity), currently unused
func (e *QueryDifficultyEstimator) RecordSearch(query []float32, iterationsUsed int, resultQuality float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.observationCount++

	// Adaptive learning rate: start high, decay to stable rate
	alpha := 0.01
	if e.observationCount < 100 {
		alpha = 1.0 / float64(e.observationCount)
	}

	// Update running statistics with exponential moving average
	e.avgSearchIterations = (1-alpha)*e.avgSearchIterations + alpha*float64(iterationsUsed)
	e.avgQueryVariance = (1-alpha)*e.avgQueryVariance + alpha*e.computeVariance(query)
	e.avgQueryEntropy = (1-alpha)*e.avgQueryEntropy + alpha*e.computeEntropy(query)

	// Update partition difficulty
	partitionIdx := e.findNearestPartition(query)
	if partitionIdx >= 0 && partitionIdx < len(e.partitionDifficulty) {
		// Difficulty is iterations relative to average
		newDiff := float64(iterationsUsed) / math.Max(1, e.avgSearchIterations)

		// Update with slow learning rate for stability
		const partitionAlpha = 0.05
		e.partitionDifficulty[partitionIdx] = (1-partitionAlpha)*e.partitionDifficulty[partitionIdx] + partitionAlpha*newDiff
	}
}

// computeVariance computes the statistical variance of a query vector.
// Higher variance indicates more distinctive features.
func (e *QueryDifficultyEstimator) computeVariance(query []float32) float64 {
	if len(query) == 0 {
		return 0
	}

	data := make([]float64, len(query))
	for i, v := range query {
		data[i] = float64(v)
	}

	return stat.Variance(data, nil)
}

// computeEntropy computes the entropy of a query vector's energy distribution.
// The vector is treated as an energy distribution (squared values normalized).
// Higher entropy indicates more uniform distribution (harder to distinguish).
func (e *QueryDifficultyEstimator) computeEntropy(query []float32) float64 {
	if len(query) == 0 {
		return 0
	}

	// Compute sum of squared values (total energy)
	var sum float64
	for _, v := range query {
		sum += float64(v * v)
	}

	if sum < 1e-10 {
		return 0
	}

	// Compute entropy: -sum(p * log2(p)) where p = v^2 / sum
	var entropy float64
	for _, v := range query {
		p := float64(v*v) / sum
		if p > 1e-10 {
			entropy -= p * math.Log2(p)
		}
	}

	return entropy
}

// findNearestPartition finds the index of the nearest partition centroid.
// Returns -1 if no centroids are configured.
func (e *QueryDifficultyEstimator) findNearestPartition(query []float32) int {
	if len(e.partitionCentroids) == 0 {
		return -1
	}

	bestIdx := 0
	bestDist := math.MaxFloat64

	for i, centroid := range e.partitionCentroids {
		dist := e.squaredL2(query, centroid)
		if dist < bestDist {
			bestDist = dist
			bestIdx = i
		}
	}

	return bestIdx
}

func (e *QueryDifficultyEstimator) squaredL2(a, b []float32) float64 {
	return float64(SquaredL2Single(a, b))
}

func (e *QueryDifficultyEstimator) CalibrateOODThreshold(trainingVectors [][]float32) {
	if len(trainingVectors) == 0 || len(e.partitionCentroids) == 0 {
		return
	}

	distances := make([]float64, len(trainingVectors))
	for i, vec := range trainingVectors {
		idx := e.findNearestPartition(vec)
		if idx >= 0 {
			distances[i] = math.Sqrt(e.squaredL2(vec, e.partitionCentroids[idx]))
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.avgDistToCentroid = stat.Mean(distances, nil)
	e.stdDistToCentroid = stat.StdDev(distances, nil)
}

func (e *QueryDifficultyEstimator) IsOOD(query []float32) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(e.partitionCentroids) == 0 || e.stdDistToCentroid == 0 {
		return false
	}

	idx := e.findNearestPartition(query)
	if idx < 0 {
		return false
	}

	dist := math.Sqrt(e.squaredL2(query, e.partitionCentroids[idx]))
	threshold := e.avgDistToCentroid + e.stdDistToCentroid*2
	return dist > threshold
}

func (e *QueryDifficultyEstimator) GetStatistics() QueryDifficultyStats {
	e.mu.RLock()
	defer e.mu.RUnlock()

	partDiff := make([]float64, len(e.partitionDifficulty))
	copy(partDiff, e.partitionDifficulty)

	return QueryDifficultyStats{
		AvgQueryVariance:    e.avgQueryVariance,
		AvgQueryEntropy:     e.avgQueryEntropy,
		AvgSearchIterations: e.avgSearchIterations,
		PartitionDifficulty: partDiff,
		ObservationCount:    e.observationCount,
	}
}

// QueryDifficultyStats contains the learned statistics.
type QueryDifficultyStats struct {
	AvgQueryVariance    float64
	AvgQueryEntropy     float64
	AvgSearchIterations float64
	PartitionDifficulty []float64
	ObservationCount    int
}
