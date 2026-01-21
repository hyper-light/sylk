package quantization

import (
	"context"
	"math"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas64"
)

// =============================================================================
// Optimal K-means Implementation with BLAS Vectorization
// =============================================================================
//
// This implementation uses true vectorized distance computation via BLAS
// matrix multiplication for production-quality k-means clustering.
//
// Performance optimizations:
//   - BLAS GEMM for computing all dot products X @ C.T in one operation
//   - Precomputed vector norms (computed once)
//   - Precomputed centroid norms (computed each iteration)
//   - Cache-optimal memory layout (row-major contiguous arrays)
//   - SIMD utilization via native BLAS implementation
//
// Correctness/robustness enhancements:
//   - Multiple restarts with best-of-N selection (escape local minima)
//   - Convergence detection on objective function value
//   - Empty cluster reinitialization from farthest points
//   - Context cancellation support
//   - Numerical stability (clamp negative distances to zero)

// KMeansConfig configures the optimal k-means algorithm.
type KMeansConfig struct {
	// MaxIterations is the safety limit for iterations per restart.
	// The algorithm relies on convergence detection, not iteration count.
	// Derived default: math.MaxInt32 (effectively unlimited)
	MaxIterations int

	// NumRestarts is the number of random restarts.
	// The best result (lowest objective) is kept.
	// Derived default: ceil(log2(k)) - more centroids need more restarts
	NumRestarts int

	// ConvergenceThreshold is the relative improvement threshold.
	// Training stops when (prevObj - currObj) / currObj < threshold.
	// Derived default: 1000 × float32 machine epsilon ≈ 1.19e-4
	ConvergenceThreshold float64

	// Seed for reproducible results. 0 = use current time.
	Seed int64
}

// DeriveKMeansConfig returns configuration derived from problem parameters.
// All values are computed from first principles rather than hardcoded.
//
// Parameters:
//   - k: number of centroids to learn
func DeriveKMeansConfig(k int) KMeansConfig {
	// NumRestarts: more centroids = more local minima = need more restarts
	// ceil(log2(k)) gives reasonable scaling based on the search space size
	numRestarts := 1
	if k > 1 {
		numRestarts = int(math.Ceil(math.Log2(float64(k))))
	}

	// ConvergenceThreshold: 1000 × machine epsilon for float32
	// This is the smallest relative change meaningful given float32 precision
	const float32Epsilon = float64(1.1920929e-7)
	convergenceThreshold := 1000 * float32Epsilon

	return KMeansConfig{
		MaxIterations:        math.MaxInt32, // Rely on convergence, not iteration count
		NumRestarts:          numRestarts,
		ConvergenceThreshold: convergenceThreshold,
		Seed:                 0,
	}
}

// kmeansState holds all state for a single k-means run.
// Memory is laid out for BLAS compatibility (row-major, contiguous).
type kmeansState struct {
	// Dimensions
	n   int // number of vectors
	k   int // number of centroids
	dim int // vector dimensionality

	// Data matrices (row-major, contiguous float64 for BLAS)
	vectors   []float64 // [n × dim] - training vectors
	centroids []float64 // [k × dim] - cluster centroids

	// Precomputed squared norms
	vectorNorms   []float64 // [n] - ||x_i||² for each vector
	centroidNorms []float64 // [k] - ||c_j||² for each centroid

	// Dot product matrix (computed via BLAS GEMM)
	dots []float64 // [n × k] - x_i · c_j for all pairs

	// Assignments and counts
	assignments []int // [n] - cluster assignment for each vector
	counts      []int // [k] - number of vectors in each cluster

	// Accumulator for new centroids
	newCentroids []float64 // [k × dim]

	// Current objective (sum of squared distances)
	objective float64
}

// newKMeansState initializes state from input vectors.
// Converts float32 to float64 for BLAS compatibility.
func newKMeansState(vectors [][]float32, k int) *kmeansState {
	if len(vectors) == 0 {
		return nil
	}

	n := len(vectors)
	dim := len(vectors[0])

	state := &kmeansState{
		n:             n,
		k:             k,
		dim:           dim,
		vectors:       make([]float64, n*dim),
		centroids:     make([]float64, k*dim),
		vectorNorms:   make([]float64, n),
		centroidNorms: make([]float64, k),
		dots:          make([]float64, n*k),
		assignments:   make([]int, n),
		counts:        make([]int, k),
		newCentroids:  make([]float64, k*dim),
	}

	// Convert to float64 and compute vector norms
	for i, v := range vectors {
		var norm float64
		for d := 0; d < dim; d++ {
			val := float64(v[d])
			state.vectors[i*dim+d] = val
			norm += val * val
		}
		state.vectorNorms[i] = norm
	}

	return state
}

// initKMeansPlusPlus initializes centroids using k-means++ algorithm with BLAS acceleration.
// Uses BLAS GEMV for computing dot products between vectors and each new centroid.
func (s *kmeansState) initKMeansPlusPlus(rng *rand.Rand) {
	// First centroid: random vector
	firstIdx := rng.Intn(s.n)
	copy(s.centroids[0:s.dim], s.vectors[firstIdx*s.dim:(firstIdx+1)*s.dim])

	// Distance to nearest centroid for each point
	distances := make([]float64, s.n)
	for i := range distances {
		distances[i] = math.MaxFloat64
	}

	// Temporary buffer for dot products with current centroid
	dotProducts := make([]float64, s.n)

	// Add remaining centroids
	for c := 1; c < s.k; c++ {
		// Compute dot products of all vectors with centroid (c-1) using BLAS GEMV
		// y = alpha * A * x + beta * y
		// where A = vectors (n×dim), x = centroid (dim×1), y = dotProducts (n×1)
		prevCentroidStart := (c - 1) * s.dim
		prevNorm := s.computeSingleNorm(s.centroids[prevCentroidStart : prevCentroidStart+s.dim])

		blas64.Gemv(
			blas.NoTrans,
			1.0, // alpha
			blas64.General{
				Rows:   s.n,
				Cols:   s.dim,
				Stride: s.dim,
				Data:   s.vectors,
			},
			blas64.Vector{
				N:    s.dim,
				Inc:  1,
				Data: s.centroids[prevCentroidStart : prevCentroidStart+s.dim],
			},
			0.0, // beta
			blas64.Vector{
				N:    s.n,
				Inc:  1,
				Data: dotProducts,
			},
		)

		// Update minimum distances
		var totalDist float64
		for i := 0; i < s.n; i++ {
			// Distance = ||x||² + ||c||² - 2·(x·c)
			dist := s.vectorNorms[i] + prevNorm - 2*dotProducts[i]
			if dist < 0 {
				dist = 0
			}

			if dist < distances[i] {
				distances[i] = dist
			}
			totalDist += distances[i]
		}

		// Handle degenerate case: all distances zero
		if totalDist == 0 {
			idx := rng.Intn(s.n)
			copy(s.centroids[c*s.dim:(c+1)*s.dim], s.vectors[idx*s.dim:(idx+1)*s.dim])
			continue
		}

		// Sample proportional to distance squared
		target := rng.Float64() * totalDist
		var cumulative float64
		selectedIdx := s.n - 1
		for i, d := range distances {
			cumulative += d
			if cumulative >= target {
				selectedIdx = i
				break
			}
		}
		copy(s.centroids[c*s.dim:(c+1)*s.dim], s.vectors[selectedIdx*s.dim:(selectedIdx+1)*s.dim])
	}
}

// computeSingleNorm computes ||v||² for a single vector using BLAS DDOT.
// DDOT(x, x) = x · x = ||x||²
func (s *kmeansState) computeSingleNorm(v []float64) float64 {
	n := len(v)
	if n == 0 {
		return 0
	}
	return blas64.Dot(blas64.Vector{N: n, Inc: 1, Data: v},
		blas64.Vector{N: n, Inc: 1, Data: v})
}

// computeCentroidNorms computes ||c_j||² for all centroids using BLAS DDOT.
func (s *kmeansState) computeCentroidNorms() {
	for j := 0; j < s.k; j++ {
		offset := j * s.dim
		s.centroidNorms[j] = blas64.Dot(
			blas64.Vector{N: s.dim, Inc: 1, Data: s.centroids[offset : offset+s.dim]},
			blas64.Vector{N: s.dim, Inc: 1, Data: s.centroids[offset : offset+s.dim]},
		)
	}
}

// computeDotProductsBLAS computes all dot products using BLAS GEMM.
// Result: dots[i*k + j] = vectors[i] · centroids[j]
//
// This is the key vectorized operation:
//
//	dots = X @ C.T
//
// where X is (n × dim) and C is (k × dim), giving (n × k) result.
func (s *kmeansState) computeDotProductsBLAS() {
	// GEMM: C = alpha * A * B + beta * C
	// We want: dots = vectors @ centroids.T
	// A = vectors (n × dim), B = centroids.T (dim × k) → C = (n × k)
	//
	// Since centroids is stored as (k × dim) row-major, we use Trans on B:
	// A = vectors, op(A) = NoTrans, lda = dim
	// B = centroids, op(B) = Trans, ldb = dim
	// C = dots, ldc = k

	blas64.Gemm(
		blas.NoTrans, // op(A) = A
		blas.Trans,   // op(B) = B.T
		1.0,          // alpha
		blas64.General{
			Rows:   s.n,
			Cols:   s.dim,
			Stride: s.dim,
			Data:   s.vectors,
		},
		blas64.General{
			Rows:   s.k,
			Cols:   s.dim,
			Stride: s.dim,
			Data:   s.centroids,
		},
		0.0, // beta
		blas64.General{
			Rows:   s.n,
			Cols:   s.k,
			Stride: s.k,
			Data:   s.dots,
		},
	)
}

// assignFromDots assigns each vector to nearest centroid using precomputed dots.
// Distance formula: ||x_i - c_j||² = ||x_i||² + ||c_j||² - 2·(x_i · c_j)
// Returns the total objective (sum of squared distances).
func (s *kmeansState) assignFromDots() float64 {
	// Reset counts
	for j := 0; j < s.k; j++ {
		s.counts[j] = 0
	}

	var totalObj float64

	// Process each vector
	for i := 0; i < s.n; i++ {
		xNorm := s.vectorNorms[i]
		minDist := math.MaxFloat64
		minJ := 0

		// Find nearest centroid using precomputed dot products
		dotRow := i * s.k
		for j := 0; j < s.k; j++ {
			// Distance = ||x||² + ||c||² - 2·(x·c)
			dist := xNorm + s.centroidNorms[j] - 2*s.dots[dotRow+j]
			if dist < 0 {
				dist = 0 // Numerical stability
			}

			if dist < minDist {
				minDist = dist
				minJ = j
			}
		}

		s.assignments[i] = minJ
		s.counts[minJ]++
		totalObj += minDist
	}

	s.objective = totalObj
	return totalObj
}

// updateCentroids computes new centroids as cluster means using BLAS DAXPY.
func (s *kmeansState) updateCentroids() {
	// Zero out accumulator
	for i := range s.newCentroids {
		s.newCentroids[i] = 0
	}

	// Sum vectors by cluster using BLAS DAXPY: y = alpha*x + y
	// For each vector, add it to its assigned centroid accumulator
	for i := 0; i < s.n; i++ {
		cluster := s.assignments[i]
		vecOffset := i * s.dim
		centOffset := cluster * s.dim

		// y = 1.0*x + y (add vector to centroid accumulator)
		blas64.Axpy(1.0,
			blas64.Vector{N: s.dim, Inc: 1, Data: s.vectors[vecOffset : vecOffset+s.dim]},
			blas64.Vector{N: s.dim, Inc: 1, Data: s.newCentroids[centOffset : centOffset+s.dim]},
		)
	}

	// Divide by count to get mean using BLAS DSCAL: x = alpha*x
	for j := 0; j < s.k; j++ {
		if s.counts[j] > 0 {
			centOffset := j * s.dim
			scale := 1.0 / float64(s.counts[j])
			blas64.Scal(scale, blas64.Vector{N: s.dim, Inc: 1, Data: s.newCentroids[centOffset : centOffset+s.dim]})
		}
	}

	// Swap buffers
	s.centroids, s.newCentroids = s.newCentroids, s.centroids
}

// reinitializeEmptyClusters handles empty clusters by reinitializing
// from the farthest points from their assigned centroids.
func (s *kmeansState) reinitializeEmptyClusters(rng *rand.Rand) {
	for j := 0; j < s.k; j++ {
		if s.counts[j] == 0 {
			// Find the point farthest from its assigned centroid
			maxDist := float64(-1)
			maxIdx := -1

			for i := 0; i < s.n; i++ {
				cluster := s.assignments[i]
				// Use precomputed distance from dots
				dotIdx := i*s.k + cluster
				dist := s.vectorNorms[i] + s.centroidNorms[cluster] - 2*s.dots[dotIdx]
				if dist < 0 {
					dist = 0
				}

				if dist > maxDist {
					maxDist = dist
					maxIdx = i
				}
			}

			if maxIdx >= 0 {
				// Reinitialize empty centroid from farthest point
				copy(s.centroids[j*s.dim:(j+1)*s.dim],
					s.vectors[maxIdx*s.dim:(maxIdx+1)*s.dim])
			} else {
				// Fallback: random point
				idx := rng.Intn(s.n)
				copy(s.centroids[j*s.dim:(j+1)*s.dim],
					s.vectors[idx*s.dim:(idx+1)*s.dim])
			}
		}
	}
}

// extractCentroids converts float64 centroids back to [][]float32.
func (s *kmeansState) extractCentroids() [][]float32 {
	result := make([][]float32, s.k)
	for j := 0; j < s.k; j++ {
		result[j] = make([]float32, s.dim)
		for d := 0; d < s.dim; d++ {
			result[j][d] = float32(s.centroids[j*s.dim+d])
		}
	}
	return result
}

// resetForRestart resets the state for a new k-means restart.
// Vectors and vectorNorms are preserved (they don't change between restarts).
// All other state is zeroed to prevent data leakage between restarts.
func (s *kmeansState) resetForRestart() {
	// Clear centroids (will be reinitialized by initKMeansPlusPlus)
	for i := range s.centroids {
		s.centroids[i] = 0
	}
	// Clear centroid norms (recomputed after centroid init)
	for i := range s.centroidNorms {
		s.centroidNorms[i] = 0
	}
	// Clear dot products (recomputed each iteration)
	for i := range s.dots {
		s.dots[i] = 0
	}
	// Clear assignments
	for i := range s.assignments {
		s.assignments[i] = 0
	}
	// Clear counts
	for i := range s.counts {
		s.counts[i] = 0
	}
	// Clear newCentroids accumulator
	for i := range s.newCentroids {
		s.newCentroids[i] = 0
	}
	// Reset objective
	s.objective = 0
}

// runSingleKMeans executes one k-means run and returns the final objective.
func (s *kmeansState) runSingleKMeans(config KMeansConfig, rng *rand.Rand) float64 {
	s.initKMeansPlusPlus(rng)
	s.computeCentroidNorms()

	prevObjective := math.MaxFloat64

	for iter := 0; iter < config.MaxIterations; iter++ {
		s.computeDotProductsBLAS()

		objective := s.assignFromDots()

		if math.IsInf(objective, 0) || math.IsNaN(objective) {
			return math.Inf(1)
		}

		if prevObjective < math.MaxFloat64 {
			improvement := (prevObjective - objective) / objective
			if improvement >= 0 && improvement < config.ConvergenceThreshold {
				return objective
			}
		}
		prevObjective = objective

		s.updateCentroids()
		s.reinitializeEmptyClusters(rng)
		s.computeCentroidNorms()
	}

	return s.objective
}

// =============================================================================
// Public API
// =============================================================================

// kmeansResult holds the result of a single k-means run.
type kmeansResult struct {
	centroids [][]float32
	objective float64
}

// kmeansSequentialRestarts runs multiple restarts sequentially, reusing memory.
// This avoids the memory allocation overhead of parallel restarts when running single-threaded.
func kmeansSequentialRestarts(vectors [][]float32, k int, config KMeansConfig, baseSeed int64) ([][]float32, error) {
	// Single state allocation, reused across all restarts
	state := newKMeansState(vectors, k)
	if state == nil {
		return nil, nil
	}

	// Buffer to store best centroids (we'll copy from state when we find a better result)
	bestCentroids := make([]float64, len(state.centroids))
	bestObjective := math.MaxFloat64

	for restart := 0; restart < config.NumRestarts; restart++ {
		// Reset state for this restart (reuses memory)
		state.resetForRestart()

		rng := rand.New(rand.NewSource(baseSeed + int64(restart)))
		objective := state.runSingleKMeans(config, rng)

		if objective < bestObjective {
			bestObjective = objective
			copy(bestCentroids, state.centroids)
		}
	}

	// Convert best centroids to [][]float32
	result := make([][]float32, k)
	dim := len(vectors[0])
	for j := 0; j < k; j++ {
		result[j] = make([]float32, dim)
		for d := 0; d < dim; d++ {
			result[j][d] = float32(bestCentroids[j*dim+d])
		}
	}

	return result, nil
}

// kmeansParallelRestarts runs restarts in parallel using a worker pool with pre-allocated states.
// Each worker maintains its own kmeansState that is reused across assigned restarts.
func kmeansParallelRestarts(
	ctx context.Context,
	vectors [][]float32,
	k int,
	config KMeansConfig,
	baseSeed int64,
	numWorkers int,
) ([][]float32, error) {
	// Work items for the job queue
	type workItem struct {
		restartID int
		seed      int64
	}

	// Channel for distributing work to workers
	jobs := make(chan workItem, config.NumRestarts)
	results := make(chan kmeansResult, config.NumRestarts)
	var wg sync.WaitGroup

	// Launch worker goroutines with pre-allocated state
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Pre-allocate state for this worker (reused across all restarts)
			state := newKMeansState(vectors, k)
			if state == nil {
				return
			}

			// Process jobs until channel closes or context cancels
			for job := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Reset and reuse state for this restart
				state.resetForRestart()

				rng := rand.New(rand.NewSource(job.seed))
				objective := state.runSingleKMeans(config, rng)

				select {
				case results <- kmeansResult{
					centroids: state.extractCentroids(),
					objective: objective,
				}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Enqueue all restart jobs
	go func() {
		for restart := 0; restart < config.NumRestarts; restart++ {
			select {
			case <-ctx.Done():
				break
			case jobs <- workItem{restartID: restart, seed: baseSeed + int64(restart)}:
			}
		}
		close(jobs)
	}()

	// Close results channel when all workers complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect best result
	return collectBestResult(results), nil
}

// KMeansOptimal performs optimal k-means clustering with BLAS vectorization.
// Returns the best centroids found across all restarts.
//
// This function is context-aware and will return early if the context is cancelled.
// Restarts are parallelized for maximum throughput.
func KMeansOptimal(ctx context.Context, vectors [][]float32, k int, config KMeansConfig) ([][]float32, error) {
	if len(vectors) == 0 {
		return nil, nil
	}

	if len(vectors) < k {
		// Not enough data - return copies of all vectors
		result := make([][]float32, len(vectors))
		for i, v := range vectors {
			result[i] = make([]float32, len(v))
			copy(result[i], v)
		}
		return result, nil
	}

	// Apply defaults
	if config.MaxIterations <= 0 {
		config.MaxIterations = math.MaxInt32
	}
	if config.NumRestarts <= 0 {
		config.NumRestarts = int(math.Ceil(math.Log2(float64(k))))
		if config.NumRestarts < 1 {
			config.NumRestarts = 1
		}
	}
	if config.ConvergenceThreshold <= 0 {
		const float32Epsilon = float64(1.1920929e-7)
		config.ConvergenceThreshold = 1000 * float32Epsilon
	}

	// Initialize base seed
	var seed int64
	if config.Seed == 0 {
		seed = time.Now().UnixNano()
	} else {
		seed = config.Seed
	}

	// For single restart, skip parallelization overhead
	if config.NumRestarts == 1 {
		state := newKMeansState(vectors, k)
		if state == nil {
			return nil, nil
		}
		rng := rand.New(rand.NewSource(seed))
		state.runSingleKMeans(config, rng)
		return state.extractCentroids(), nil
	}

	// Determine parallelization strategy
	numWorkers := config.NumRestarts
	maxWorkers := runtime.NumCPU()
	if numWorkers > maxWorkers {
		numWorkers = maxWorkers
	}

	// For sequential execution (GOMAXPROCS=1 or limited workers), reuse state to avoid memory churn
	if runtime.GOMAXPROCS(0) == 1 || numWorkers == 1 {
		return kmeansSequentialRestarts(vectors, k, config, seed)
	}

	// Run parallel restarts with pre-allocated worker states
	return kmeansParallelRestarts(ctx, vectors, k, config, seed, numWorkers)
}

// collectBestResult reads all results from the channel and returns the best centroids.
func collectBestResult(results <-chan kmeansResult) [][]float32 {
	var bestCentroids [][]float32
	bestObjective := math.MaxFloat64

	for result := range results {
		if result.objective < bestObjective {
			bestObjective = result.objective
			bestCentroids = result.centroids
		}
	}

	return bestCentroids
}

// KMeansOptimalSimple is a convenience wrapper that derives config from k.
func KMeansOptimalSimple(vectors [][]float32, k int) [][]float32 {
	centroids, _ := KMeansOptimal(context.Background(), vectors, k, DeriveKMeansConfig(k))
	return centroids
}
