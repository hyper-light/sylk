package quantization

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"math/rand"
	"runtime"
	"sync"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas32"
)

// =============================================================================
// Product Quantization Constants
// =============================================================================

const (
	// DefaultNumSubspaces splits 768-dim vectors into 32 24-dim subspaces.
	// This provides a good balance between compression ratio and accuracy.
	DefaultNumSubspaces = 32

	// DefaultCentroidsPerSubspace uses 256 centroids for 8-bit codes per subspace.
	// This allows encoding each subspace index in a single byte (uint8).
	DefaultCentroidsPerSubspace = 256

	// DefaultTrainingSamples is the number of samples used for k-means training.
	// More samples improve centroid quality but increase training time.
	DefaultTrainingSamples = 10000
)

// =============================================================================
// PQCode Type
// =============================================================================

// PQCode represents a product-quantized vector as a sequence of centroid indices.
// For a 768-dimensional vector with 32 subspaces, this provides 32x compression
// (768 * 4 bytes = 3072 bytes down to 32 bytes).
type PQCode []uint8

// NewPQCode creates a new PQCode with the specified number of subspaces.
// Each element in the slice represents the centroid index for one subspace.
func NewPQCode(numSubspaces int) PQCode {
	if numSubspaces <= 0 {
		return nil
	}
	return make(PQCode, numSubspaces)
}

// Equal returns true if both PQCodes have the same length and identical values.
func (c PQCode) Equal(other PQCode) bool {
	if len(c) != len(other) {
		return false
	}
	for i := range c {
		if c[i] != other[i] {
			return false
		}
	}
	return true
}

// Clone returns a deep copy of the PQCode.
func (c PQCode) Clone() PQCode {
	if c == nil {
		return nil
	}
	clone := make(PQCode, len(c))
	copy(clone, c)
	return clone
}

// String returns a string representation of the PQCode for debugging.
// Format: "PQCode[n]{c0,c1,...,cn-1}" where n is the number of subspaces
// and ci are the centroid indices.
func (c PQCode) String() string {
	if c == nil {
		return "PQCode[0]{}"
	}
	if len(c) == 0 {
		return "PQCode[0]{}"
	}

	// For short codes, show all values
	if len(c) <= 8 {
		result := fmt.Sprintf("PQCode[%d]{", len(c))
		for i, v := range c {
			if i > 0 {
				result += ","
			}
			result += fmt.Sprintf("%d", v)
		}
		result += "}"
		return result
	}

	// For longer codes, show first 4 and last 4 values
	result := fmt.Sprintf("PQCode[%d]{%d,%d,%d,%d,...,%d,%d,%d,%d}",
		len(c),
		c[0], c[1], c[2], c[3],
		c[len(c)-4], c[len(c)-3], c[len(c)-2], c[len(c)-1])
	return result
}

// =============================================================================
// ProductQuantizer Configuration
// =============================================================================

// ProductQuantizerConfig holds configuration options for creating a ProductQuantizer.
type ProductQuantizerConfig struct {
	// NumSubspaces is the number of subspaces to split vectors into.
	// Default: DefaultNumSubspaces (32)
	NumSubspaces int

	// CentroidsPerSubspace is the number of cluster centroids per subspace.
	// Must be <= 256 for uint8 encoding.
	// Default: DefaultCentroidsPerSubspace (256)
	CentroidsPerSubspace int
}

// DefaultProductQuantizerConfig returns a ProductQuantizerConfig with default values.
func DefaultProductQuantizerConfig() ProductQuantizerConfig {
	return ProductQuantizerConfig{
		NumSubspaces:         DefaultNumSubspaces,
		CentroidsPerSubspace: DefaultCentroidsPerSubspace,
	}
}

// =============================================================================
// ProductQuantizer
// =============================================================================

// ProductQuantizer implements product quantization for high-dimensional vectors.
// It splits vectors into subspaces and learns cluster centroids for each subspace
// via k-means clustering. Vectors can then be encoded as indices into these centroids.
type ProductQuantizer struct {
	// numSubspaces is the number of vector subspaces.
	numSubspaces int

	// centroidsPerSubspace is the cluster count per subspace (usually 256 for uint8 codes).
	centroidsPerSubspace int

	// subspaceDim is the computed dimension per subspace (vectorDim / numSubspaces).
	subspaceDim int

	// vectorDim is the original vector dimension.
	vectorDim int

	// centroids stores the learned centroids: [subspace][centroid][dim].
	// Shape: [numSubspaces][centroidsPerSubspace][subspaceDim]
	centroids [][][]float32

	// centroidsFlat stores centroids in BLAS-compatible contiguous layout.
	// Shape: [numSubspaces] where each element is [centroidsPerSubspace × subspaceDim] contiguous.
	// Row-major: centroid c, dimension d is at index c*subspaceDim + d.
	// This enables BLAS GEMV/GEMM for fast encoding.
	centroidsFlat [][]float32

	// centroidNorms caches ||c||² for each centroid.
	// Shape: [numSubspaces][centroidsPerSubspace]
	// Enables fast distance computation: ||x-c||² = ||x||² + ||c||² - 2·(x·c)
	centroidNorms [][]float32

	// trained indicates whether centroids have been learned via Train().
	trained bool
}

// Validation errors for ProductQuantizer construction.
var (
	ErrInvalidVectorDim         = errors.New("vector dimension must be positive")
	ErrInvalidNumSubspaces      = errors.New("number of subspaces must be positive")
	ErrInvalidCentroidsPerSub   = errors.New("centroids per subspace must be between 1 and 256")
	ErrVectorDimNotDivisible    = errors.New("vector dimension must be divisible by number of subspaces")
	ErrQuantizerNotTrained      = errors.New("product quantizer has not been trained")
)

// NewProductQuantizer creates a new ProductQuantizer for vectors of the given dimension.
// The vectorDim must be divisible by config.NumSubspaces.
func NewProductQuantizer(vectorDim int, config ProductQuantizerConfig) (*ProductQuantizer, error) {
	// Apply defaults for zero values
	if config.NumSubspaces == 0 {
		config.NumSubspaces = DefaultNumSubspaces
	}
	if config.CentroidsPerSubspace == 0 {
		config.CentroidsPerSubspace = DefaultCentroidsPerSubspace
	}

	// Validate inputs
	if vectorDim <= 0 {
		return nil, ErrInvalidVectorDim
	}
	if config.NumSubspaces <= 0 {
		return nil, ErrInvalidNumSubspaces
	}
	if config.CentroidsPerSubspace <= 0 || config.CentroidsPerSubspace > 256 {
		return nil, ErrInvalidCentroidsPerSub
	}
	if vectorDim%config.NumSubspaces != 0 {
		return nil, ErrVectorDimNotDivisible
	}

	subspaceDim := vectorDim / config.NumSubspaces

	// Pre-allocate centroid storage
	// Shape: [numSubspaces][centroidsPerSubspace][subspaceDim]
	centroids := make([][][]float32, config.NumSubspaces)
	for i := range centroids {
		centroids[i] = make([][]float32, config.CentroidsPerSubspace)
		for j := range centroids[i] {
			centroids[i][j] = make([]float32, subspaceDim)
		}
	}

	return &ProductQuantizer{
		numSubspaces:         config.NumSubspaces,
		centroidsPerSubspace: config.CentroidsPerSubspace,
		subspaceDim:          subspaceDim,
		vectorDim:            vectorDim,
		centroids:            centroids,
		trained:              false,
	}, nil
}

// SubspaceDim returns the dimension of each subspace.
func (pq *ProductQuantizer) SubspaceDim() int {
	return pq.subspaceDim
}

// NumSubspaces returns the number of subspaces.
func (pq *ProductQuantizer) NumSubspaces() int {
	return pq.numSubspaces
}

// CentroidsPerSubspace returns the number of centroids per subspace.
func (pq *ProductQuantizer) CentroidsPerSubspace() int {
	return pq.centroidsPerSubspace
}

// VectorDim returns the original vector dimension.
func (pq *ProductQuantizer) VectorDim() int {
	return pq.vectorDim
}

// IsTrained returns true if the quantizer has been trained with centroid data.
func (pq *ProductQuantizer) IsTrained() bool {
	return pq.trained
}

// Centroids returns the learned centroids.
// Shape: [numSubspaces][centroidsPerSubspace][subspaceDim]
// Returns nil if not trained.
func (pq *ProductQuantizer) Centroids() [][][]float32 {
	if !pq.trained {
		return nil
	}
	return pq.centroids
}

// buildEncodingCache builds the flat centroid layout and norm cache for fast encoding.
// This is called once after training to enable BLAS-accelerated encoding.
//
// The flat layout stores centroids[m] as a contiguous [K × subspaceDim] array,
// enabling BLAS GEMV (single vector) and GEMM (batch) operations.
func (pq *ProductQuantizer) buildEncodingCache() {
	k := pq.centroidsPerSubspace
	dim := pq.subspaceDim

	// Build flat centroid layout for BLAS
	pq.centroidsFlat = make([][]float32, pq.numSubspaces)
	pq.centroidNorms = make([][]float32, pq.numSubspaces)

	for m := 0; m < pq.numSubspaces; m++ {
		// Flat layout: row c is centroid c, contiguous in memory
		flat := make([]float32, k*dim)
		norms := make([]float32, k)

		for c := 0; c < k; c++ {
			centroid := pq.centroids[m][c]
			offset := c * dim
			var norm float32
			for d := 0; d < dim; d++ {
				val := centroid[d]
				flat[offset+d] = val
				norm += val * val
			}
			norms[c] = norm
		}

		pq.centroidsFlat[m] = flat
		pq.centroidNorms[m] = norms
	}
}

// =============================================================================
// Subspace Distance Helper (PQ.3)
// =============================================================================

// subspaceDistance computes squared L2 distance between two subspace vectors.
// This is used during training and encoding to find nearest centroids.
// Handles different lengths gracefully by using the minimum length.
func subspaceDistance(a, b []float32) float32 {
	// Handle nil/empty slices
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	// Use minimum length for safety
	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	var sum float32

	// Loop unrolling for better performance on common subspace dimensions
	// Process 4 elements at a time
	i := 0
	for ; i <= n-4; i += 4 {
		d0 := a[i] - b[i]
		d1 := a[i+1] - b[i+1]
		d2 := a[i+2] - b[i+2]
		d3 := a[i+3] - b[i+3]
		sum += d0*d0 + d1*d1 + d2*d2 + d3*d3
	}

	// Handle remaining elements
	for ; i < n; i++ {
		d := a[i] - b[i]
		sum += d * d
	}

	return sum
}

// =============================================================================
// Distance Table (PQ.4)
// =============================================================================

// DistanceTable precomputes distances from a query to all centroids.
// This enables O(M) asymmetric distance computation instead of O(D)
// where M is number of subspaces and D is original dimensionality.
type DistanceTable struct {
	// Table stores precomputed distances: [subspace][centroid] -> distance to query
	Table [][]float32

	// NumSubspaces is the number of subspaces in the table
	NumSubspaces int

	// CentroidsPerSubspace is the number of centroids per subspace
	CentroidsPerSubspace int
}

// Validation errors for DistanceTable
var (
	ErrDistanceTableNil            = errors.New("distance table is nil")
	ErrDistanceTableEmpty          = errors.New("distance table has no subspaces")
	ErrDistanceTableInvalidDims    = errors.New("distance table dimensions do not match configuration")
	ErrDistanceTableInvalidCode    = errors.New("PQ code length does not match number of subspaces")
	ErrDistanceTableCentroidOOB    = errors.New("centroid index out of bounds")
)

// NewDistanceTable creates an empty distance table with the specified dimensions.
// The table will have shape [numSubspaces][centroidsPerSubspace].
func NewDistanceTable(numSubspaces, centroidsPerSubspace int) *DistanceTable {
	if numSubspaces <= 0 || centroidsPerSubspace <= 0 {
		return nil
	}

	table := make([][]float32, numSubspaces)
	for i := range table {
		table[i] = make([]float32, centroidsPerSubspace)
	}

	return &DistanceTable{
		Table:                table,
		NumSubspaces:         numSubspaces,
		CentroidsPerSubspace: centroidsPerSubspace,
	}
}

// ComputeDistance looks up precomputed distances for a PQ code.
// Returns sum of distances for each subspace's centroid assignment.
// The code must have length equal to NumSubspaces, and each centroid
// index must be less than CentroidsPerSubspace.
func (dt *DistanceTable) ComputeDistance(code PQCode) float32 {
	if dt == nil || dt.Table == nil || len(code) == 0 {
		return 0
	}

	// Use minimum of code length and table subspaces for safety
	n := len(code)
	if dt.NumSubspaces < n {
		n = dt.NumSubspaces
	}

	var sum float32
	for i := 0; i < n; i++ {
		centroidIdx := int(code[i])
		// Bounds check for centroid index
		if centroidIdx < len(dt.Table[i]) {
			sum += dt.Table[i][centroidIdx]
		}
	}

	return sum
}

// Validate checks that the table dimensions are valid and consistent.
// Returns an error if the table is malformed.
func (dt *DistanceTable) Validate() error {
	if dt == nil {
		return ErrDistanceTableNil
	}

	if dt.Table == nil || len(dt.Table) == 0 {
		return ErrDistanceTableEmpty
	}

	if len(dt.Table) != dt.NumSubspaces {
		return ErrDistanceTableInvalidDims
	}

	for i, subspace := range dt.Table {
		if subspace == nil || len(subspace) != dt.CentroidsPerSubspace {
			return fmt.Errorf("%w: subspace %d has %d centroids, expected %d",
				ErrDistanceTableInvalidDims, i, len(subspace), dt.CentroidsPerSubspace)
		}
	}

	return nil
}

// ValidateCode checks that a PQ code is compatible with this distance table.
// Returns an error if the code cannot be used for distance computation.
func (dt *DistanceTable) ValidateCode(code PQCode) error {
	if dt == nil {
		return ErrDistanceTableNil
	}

	if len(code) != dt.NumSubspaces {
		return fmt.Errorf("%w: code has %d elements, expected %d",
			ErrDistanceTableInvalidCode, len(code), dt.NumSubspaces)
	}

	for i, centroidIdx := range code {
		if int(centroidIdx) >= dt.CentroidsPerSubspace {
			return fmt.Errorf("%w: subspace %d has centroid index %d, max is %d",
				ErrDistanceTableCentroidOOB, i, centroidIdx, dt.CentroidsPerSubspace-1)
		}
	}

	return nil
}

// =============================================================================
// K-means++ Initialization (PQ.5)
// =============================================================================

// kmeansppInit initializes centroids using K-means++ algorithm.
// This provides better initialization than random selection by choosing
// initial centroids that are well-spread across the data distribution.
//
// Algorithm:
// 1. Choose first centroid uniformly at random from the data
// 2. For each remaining centroid position:
//   - Compute distance from each point to nearest existing centroid
//   - Choose next centroid with probability proportional to squared distance
//
// This initialization leads to faster convergence and better final clusters
// compared to random initialization.
func kmeansppInit(vectors [][]float32, k int, rng *rand.Rand) [][]float32 {
	if len(vectors) == 0 || k <= 0 {
		return nil
	}
	if k > len(vectors) {
		k = len(vectors)
	}

	dim := len(vectors[0])
	centroids := make([][]float32, 0, k)

	// Step 1: Choose first centroid uniformly at random
	firstIdx := rng.Intn(len(vectors))
	centroids = append(centroids, copyVector(vectors[firstIdx]))

	// Pre-allocate distance array
	distances := make([]float32, len(vectors))

	for len(centroids) < k {
		// Step 2: For each point, compute distance to nearest centroid
		var totalDist float64
		for i, v := range vectors {
			// Skip if vector has wrong dimension
			if len(v) != dim {
				distances[i] = 0
				continue
			}

			minDist := float32(math.MaxFloat32)
			for _, c := range centroids {
				d := subspaceDistance(v, c)
				if d < minDist {
					minDist = d
				}
			}
			distances[i] = minDist
			totalDist += float64(minDist)
		}

		// Handle edge case: all distances are zero (all vectors identical)
		if totalDist == 0 {
			// Just pick the next vector that isn't already a centroid
			for _, v := range vectors {
				isExisting := false
				for _, c := range centroids {
					if vectorsEqual(v, c) {
						isExisting = true
						break
					}
				}
				if !isExisting {
					centroids = append(centroids, copyVector(v))
					break
				}
			}
			// If all vectors are duplicates, just copy existing ones
			if len(centroids) < k {
				centroids = append(centroids, copyVector(vectors[rng.Intn(len(vectors))]))
			}
			continue
		}

		// Step 3: Choose next centroid with probability proportional to D(x)^2
		// (We're using squared distances already from subspaceDistance)
		target := rng.Float64() * totalDist
		var cumulative float64
		selectedIdx := len(vectors) - 1 // Default to last if we don't find one
		for i, d := range distances {
			cumulative += float64(d)
			if cumulative >= target {
				selectedIdx = i
				break
			}
		}
		centroids = append(centroids, copyVector(vectors[selectedIdx]))
	}

	return centroids
}

// copyVector returns a deep copy of a vector.
func copyVector(v []float32) []float32 {
	if v == nil {
		return nil
	}
	result := make([]float32, len(v))
	copy(result, v)
	return result
}

// vectorsEqual returns true if two vectors have identical values.
func vectorsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// =============================================================================
// K-means Training (PQ.6)
// =============================================================================

// trainSubspace trains centroids for a single subspace using K-means clustering.
// It uses K-means++ initialization for better starting positions, then iteratively
// refines centroids until convergence or max iterations is reached.
//
// Parameters:
//   - subspaceVectors: All training vectors projected into this subspace
//   - k: Number of centroids to learn (typically 256 for 8-bit codes)
//   - maxIterations: Maximum number of K-means iterations
//   - rng: Random number generator for initialization
//
// Returns the learned centroids as a slice of vectors.
func (pq *ProductQuantizer) trainSubspace(
	subspaceVectors [][]float32,
	k int,
	maxIterations int,
	rng *rand.Rand,
) [][]float32 {
	if len(subspaceVectors) == 0 {
		return nil
	}

	if len(subspaceVectors) < k {
		// Not enough data - just use vectors as centroids
		centroids := make([][]float32, len(subspaceVectors))
		for i, v := range subspaceVectors {
			centroids[i] = copyVector(v)
		}
		return centroids
	}

	// Initialize with K-means++
	centroids := kmeansppInit(subspaceVectors, k, rng)
	if centroids == nil {
		return nil
	}

	// K-means iterations
	assignments := make([]int, len(subspaceVectors))
	dim := len(subspaceVectors[0])

	for iter := 0; iter < maxIterations; iter++ {
		// Assignment step: Assign vectors to nearest centroid
		changed := false
		for i, v := range subspaceVectors {
			nearest := findNearestCentroid(v, centroids)
			if nearest != assignments[i] {
				assignments[i] = nearest
				changed = true
			}
		}

		if !changed && iter > 0 {
			break // Converged - no assignments changed
		}

		// Update step: Recalculate centroids as mean of assigned vectors
		centroids = updateCentroids(subspaceVectors, assignments, k, dim)
	}

	return centroids
}

// findNearestCentroid returns the index of the centroid closest to vector v.
// Uses squared L2 distance for efficiency (avoids sqrt).
func findNearestCentroid(v []float32, centroids [][]float32) int {
	if len(centroids) == 0 {
		return 0
	}

	minDist := float32(math.MaxFloat32)
	minIdx := 0
	for i, c := range centroids {
		d := subspaceDistance(v, c)
		if d < minDist {
			minDist = d
			minIdx = i
		}
	}
	return minIdx
}

// updateCentroids computes new centroids as the mean of all vectors assigned to each cluster.
// For empty clusters, the centroid is set to all zeros.
func updateCentroids(vectors [][]float32, assignments []int, k, dim int) [][]float32 {
	centroids := make([][]float32, k)
	counts := make([]int, k)

	// Initialize with zeros
	for i := range centroids {
		centroids[i] = make([]float32, dim)
	}

	// Sum vectors by cluster
	for i, v := range vectors {
		cluster := assignments[i]
		if cluster >= 0 && cluster < k {
			counts[cluster]++
			for j, val := range v {
				if j < dim {
					centroids[cluster][j] += val
				}
			}
		}
	}

	// Divide by count to get mean
	for i, c := range centroids {
		if counts[i] > 0 {
			for j := range c {
				c[j] /= float32(counts[i])
			}
		}
	}

	return centroids
}

// =============================================================================
// Training Configuration (PQ.7)
// =============================================================================

// TrainConfig configures the training process for Product Quantization.
type TrainConfig struct {
	// MaxIterations is the safety limit for K-means iterations per subspace.
	// The algorithm relies on convergence detection, not iteration count.
	// This is a safety valve to prevent infinite loops in degenerate cases.
	// Derived default: math.MaxInt32 (effectively unlimited)
	MaxIterations int

	// NumRestarts is the number of k-means restarts per subspace.
	// Multiple restarts help escape local minima. Best result is kept.
	// Derived default: ceil(log2(k)) where k is centroids per subspace.
	// More centroids = more local minima = need more restarts.
	NumRestarts int

	// ConvergenceThreshold is the relative improvement threshold for convergence.
	// Training stops when (prevObj - currObj) / currObj < threshold.
	// Derived default: 1000 × float32 machine epsilon ≈ 1.19e-4
	// This is the smallest meaningful relative change given float32 precision.
	ConvergenceThreshold float64

	// Seed is the random seed for reproducible training.
	// 0 = use current time for non-deterministic training.
	Seed int64

	// MinSamplesRatio is the minimum number of training samples per centroid.
	// Total minimum samples = centroidsPerSubspace * MinSamplesRatio.
	// Derived default: max(10, subspaceDim) based on statistical theory
	// that stable cluster means require 10-30 samples, scaling with dimensionality.
	MinSamplesRatio float32
}

// Float32Epsilon is the machine epsilon for float32.
// This is the smallest value where 1.0 + epsilon != 1.0.
const Float32Epsilon = float32(1.1920929e-7) // math.Nextafter32(1, 2) - 1

// DeriveTrainConfig returns training configuration derived from problem parameters.
// All values are computed from first principles rather than hardcoded.
//
// Parameters:
//   - k: number of centroids per subspace (typically 256)
//   - subspaceDim: dimensionality of each subspace (vectorDim / numSubspaces)
func DeriveTrainConfig(k int, subspaceDim int) TrainConfig {
	// NumRestarts: more centroids = more local minima = need more restarts
	// ceil(log2(k)) gives reasonable scaling: k=256 → 8 restarts
	numRestarts := 1
	if k > 1 {
		numRestarts = int(math.Ceil(math.Log2(float64(k))))
	}

	// MinSamplesRatio: statistical theory says stable means need 10-30 samples
	// Higher dimensions need more samples for stable estimates
	minSamplesRatio := float32(max(10, subspaceDim))

	// ConvergenceThreshold: 1000 × machine epsilon
	// This is the smallest relative change that's meaningful given float32 precision
	convergenceThreshold := float64(1000 * Float32Epsilon)

	return TrainConfig{
		MaxIterations:        math.MaxInt32, // Rely on convergence, not iteration count
		NumRestarts:          numRestarts,
		ConvergenceThreshold: convergenceThreshold,
		Seed:                 0,
		MinSamplesRatio:      minSamplesRatio,
	}
}


// =============================================================================
// Top-Level Training (PQ.7)
// =============================================================================

// Train learns centroids from training vectors using optimal K-means clustering.
// Each subspace is trained independently to learn its own set of centroids.
//
// The training configuration is derived from this ProductQuantizer's actual
// parameters (centroidsPerSubspace, subspaceDim) rather than hardcoded defaults.
//
// Parameters:
//   - ctx: context for cancellation support
//   - vectors: training data where each vector must have dimension pq.vectorDim
//
// Returns an error if:
//   - No training vectors are provided
//   - Any vector has incorrect dimension
//   - Insufficient training data for the number of centroids
//   - Context is cancelled
func (pq *ProductQuantizer) Train(ctx context.Context, vectors [][]float32) error {
	// Derive config from this quantizer's actual parameters
	config := DeriveTrainConfig(pq.centroidsPerSubspace, pq.subspaceDim)
	return pq.TrainWithConfig(ctx, vectors, config)
}

// TrainWithConfig learns centroids using the provided configuration.
// Use this when you need to override the derived configuration.
func (pq *ProductQuantizer) TrainWithConfig(ctx context.Context, vectors [][]float32, config TrainConfig) error {
	if len(vectors) == 0 {
		return errors.New("no training vectors provided")
	}

	// Validate vector dimensions
	for i, v := range vectors {
		if len(v) != pq.vectorDim {
			return fmt.Errorf("vector %d has dimension %d, expected %d", i, len(v), pq.vectorDim)
		}
	}

	// Check minimum samples using derived or provided ratio
	if config.MinSamplesRatio <= 0 {
		config.MinSamplesRatio = float32(max(10, pq.subspaceDim))
	}
	minSamples := int(float32(pq.centroidsPerSubspace) * config.MinSamplesRatio)
	if len(vectors) < minSamples {
		return fmt.Errorf("insufficient training data: have %d, need at least %d", len(vectors), minSamples)
	}

	// Build KMeansConfig from TrainConfig
	kmConfig := KMeansConfig{
		MaxIterations:        config.MaxIterations,
		NumRestarts:          config.NumRestarts,
		ConvergenceThreshold: config.ConvergenceThreshold,
		Seed:                 config.Seed,
	}

	// Apply derived defaults if not set
	if kmConfig.MaxIterations <= 0 {
		kmConfig.MaxIterations = math.MaxInt32
	}
	if kmConfig.NumRestarts <= 0 {
		kmConfig.NumRestarts = int(math.Ceil(math.Log2(float64(pq.centroidsPerSubspace))))
		if kmConfig.NumRestarts < 1 {
			kmConfig.NumRestarts = 1
		}
	}
	if kmConfig.ConvergenceThreshold <= 0 {
		kmConfig.ConvergenceThreshold = float64(1000 * Float32Epsilon)
	}

	// Extract subspace vectors and train each subspace
	pq.centroids = make([][][]float32, pq.numSubspaces)

	for subspace := 0; subspace < pq.numSubspaces; subspace++ {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Extract subspace vectors
		subspaceVectors := pq.extractSubspace(vectors, subspace)

		// Train this subspace using optimal k-means
		// Each subspace gets a unique seed derived from the base seed
		subspaceConfig := kmConfig
		if subspaceConfig.Seed != 0 {
			subspaceConfig.Seed = kmConfig.Seed + int64(subspace)
		}

		centroids, err := KMeansOptimal(ctx, subspaceVectors, pq.centroidsPerSubspace, subspaceConfig)
		if err != nil {
			return fmt.Errorf("failed to train subspace %d: %w", subspace, err)
		}
		pq.centroids[subspace] = centroids
	}

	pq.trained = true
	pq.buildEncodingCache()
	return nil
}

// extractSubspace extracts the subspace slice from each vector.
// For subspace i, extracts dimensions [i*subspaceDim, (i+1)*subspaceDim).
func (pq *ProductQuantizer) extractSubspace(vectors [][]float32, subspace int) [][]float32 {
	start := subspace * pq.subspaceDim
	end := start + pq.subspaceDim

	result := make([][]float32, len(vectors))
	for i, v := range vectors {
		subVec := make([]float32, pq.subspaceDim)
		copy(subVec, v[start:end])
		result[i] = subVec
	}
	return result
}

// TrainParallel trains subspaces in parallel using goroutines for faster training.
// Configuration is derived from this ProductQuantizer's actual parameters.
//
// Parameters:
//   - ctx: context for cancellation support
//   - vectors: training data where each vector must have dimension pq.vectorDim
//   - workers: number of parallel workers (0 = use NumCPU)
//
// Returns an error if validation fails or any subspace training fails.
func (pq *ProductQuantizer) TrainParallel(ctx context.Context, vectors [][]float32, workers int) error {
	config := DeriveTrainConfig(pq.centroidsPerSubspace, pq.subspaceDim)
	return pq.TrainParallelWithConfig(ctx, vectors, config, workers)
}

// TrainParallelWithConfig trains subspaces in parallel with custom configuration.
func (pq *ProductQuantizer) TrainParallelWithConfig(ctx context.Context, vectors [][]float32, config TrainConfig, workers int) error {
	if len(vectors) == 0 {
		return errors.New("no training vectors provided")
	}

	// Validate vector dimensions
	for i, v := range vectors {
		if len(v) != pq.vectorDim {
			return fmt.Errorf("vector %d has dimension %d, expected %d", i, len(v), pq.vectorDim)
		}
	}

	// Check minimum samples using derived or provided ratio
	if config.MinSamplesRatio <= 0 {
		config.MinSamplesRatio = float32(max(10, pq.subspaceDim))
	}
	minSamples := int(float32(pq.centroidsPerSubspace) * config.MinSamplesRatio)
	if len(vectors) < minSamples {
		return fmt.Errorf("insufficient training data: have %d, need at least %d", len(vectors), minSamples)
	}

	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	// Build KMeansConfig from TrainConfig with derived defaults
	kmConfig := KMeansConfig{
		MaxIterations:        config.MaxIterations,
		NumRestarts:          config.NumRestarts,
		ConvergenceThreshold: config.ConvergenceThreshold,
		Seed:                 config.Seed,
	}
	if kmConfig.MaxIterations <= 0 {
		kmConfig.MaxIterations = math.MaxInt32
	}
	if kmConfig.NumRestarts <= 0 {
		kmConfig.NumRestarts = int(math.Ceil(math.Log2(float64(pq.centroidsPerSubspace))))
		if kmConfig.NumRestarts < 1 {
			kmConfig.NumRestarts = 1
		}
	}
	if kmConfig.ConvergenceThreshold <= 0 {
		kmConfig.ConvergenceThreshold = float64(1000 * Float32Epsilon)
	}

	// Initialize centroids array
	pq.centroids = make([][][]float32, pq.numSubspaces)

	// Use semaphore to limit parallelism
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var errOnce sync.Once

	for subspace := 0; subspace < pq.numSubspaces; subspace++ {
		// Check for cancellation before spawning
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		go func(s int) {
			defer wg.Done()

			// Acquire semaphore
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				errOnce.Do(func() { firstErr = ctx.Err() })
				return
			}
			defer func() { <-sem }()

			// Each subspace gets unique seed derived from base seed
			subspaceConfig := kmConfig
			if subspaceConfig.Seed != 0 {
				subspaceConfig.Seed = kmConfig.Seed + int64(s)
			}

			subspaceVectors := pq.extractSubspace(vectors, s)
			centroids, err := KMeansOptimal(ctx, subspaceVectors, pq.centroidsPerSubspace, subspaceConfig)
			if err != nil {
				errOnce.Do(func() { firstErr = fmt.Errorf("subspace %d: %w", s, err) })
				return
			}

			mu.Lock()
			pq.centroids[s] = centroids
			mu.Unlock()
		}(subspace)
	}

	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

	pq.trained = true
	pq.buildEncodingCache()
	return nil
}

// =============================================================================
// EncodingBuffer for Batch Encoding (PQ.8)
// =============================================================================

// EncodingBuffer holds pre-allocated buffers for batch encoding operations.
// This reduces allocations by reusing buffers across chunks within the same worker.
// CRITICAL: Must call Reset() before reusing for a new chunk to prevent data leakage.
type EncodingBuffer struct {
	// subspaceData holds contiguous subvector data for BLAS GEMM
	subspaceData []float32
	// xNorms holds squared norms of input vectors
	xNorms []float32
	// dots holds dot product results from BLAS GEMM
	dots []float32
}

// NewEncodingBuffer creates a new encoding buffer.
func NewEncodingBuffer() *EncodingBuffer {
	return &EncodingBuffer{}
}

// Reset clears all buffer data to prevent leakage between chunks.
// CRITICAL: This MUST be called before processing each new chunk.
func (b *EncodingBuffer) Reset() {
	// Zero out the slices to prevent data leakage
	for i := range b.subspaceData {
		b.subspaceData[i] = 0
	}
	for i := range b.xNorms {
		b.xNorms[i] = 0
	}
	for i := range b.dots {
		b.dots[i] = 0
	}
}

// EnsureCapacity ensures buffers are large enough for the given dimensions.
// Grows buffers if needed but reuses existing allocations when sufficient.
func (b *EncodingBuffer) EnsureCapacity(chunkN, subspaceDim, k int) {
	// Grow subspaceData if needed: [chunkN * subspaceDim]
	subspaceSize := chunkN * subspaceDim
	if subspaceSize > cap(b.subspaceData) {
		b.subspaceData = make([]float32, subspaceSize)
	} else {
		b.subspaceData = b.subspaceData[:subspaceSize]
	}

	// Grow xNorms if needed: [chunkN]
	if chunkN > cap(b.xNorms) {
		b.xNorms = make([]float32, chunkN)
	} else {
		b.xNorms = b.xNorms[:chunkN]
	}

	// Grow dots if needed: [chunkN * k]
	dotsSize := chunkN * k
	if dotsSize > cap(b.dots) {
		b.dots = make([]float32, dotsSize)
	} else {
		b.dots = b.dots[:dotsSize]
	}
}

// encodingBufferPool provides thread-safe buffer reuse across EncodeBatch calls.
var encodingBufferPool = sync.Pool{
	New: func() interface{} {
		return NewEncodingBuffer()
	},
}

// =============================================================================
// Vector Encoding (PQ.8)
// =============================================================================

// Encode compresses a full vector to a PQCode using BLAS-accelerated distance computation.
// Each subspace is quantized to the nearest centroid.
//
// Uses the identity: ||x-c||² = ||x||² + ||c||² - 2·(x·c)
// BLAS GEMV computes all K dot products (x·c) for each subspace in one operation.
//
// Returns an error if the vector dimension doesn't match or quantizer is not trained.
func (pq *ProductQuantizer) Encode(vector []float32) (PQCode, error) {
	if len(vector) != pq.vectorDim {
		return nil, fmt.Errorf("vector dimension %d != expected %d", len(vector), pq.vectorDim)
	}
	if !pq.trained {
		return nil, ErrQuantizerNotTrained
	}

	k := pq.centroidsPerSubspace
	dim := pq.subspaceDim

	// Allocate buffer for dot products
	dots := make([]float32, k)

	code := make(PQCode, pq.numSubspaces)
	for m := 0; m < pq.numSubspaces; m++ {
		start := m * dim
		subvector := vector[start : start+dim]

		// Compute ||x||² for this subspace
		var xNorm float32
		for _, v := range subvector {
			xNorm += v * v
		}

		// Compute all K dot products using BLAS GEMV: dots = centroids @ subvector
		// centroids[m] is [K × dim], subvector is [dim], result is [K]
		blas32.Gemv(
			blas.NoTrans,
			1.0, // alpha
			blas32.General{
				Rows:   k,
				Cols:   dim,
				Stride: dim,
				Data:   pq.centroidsFlat[m],
			},
			blas32.Vector{
				N:    dim,
				Inc:  1,
				Data: subvector,
			},
			0.0, // beta
			blas32.Vector{
				N:    k,
				Inc:  1,
				Data: dots,
			},
		)

		// Find nearest centroid: argmin(||x||² + ||c||² - 2·(x·c))
		norms := pq.centroidNorms[m]
		minDist := float32(math.MaxFloat32)
		minIdx := uint8(0)
		for c := 0; c < k; c++ {
			dist := xNorm + norms[c] - 2*dots[c]
			if dist < minDist {
				minDist = dist
				minIdx = uint8(c)
			}
		}
		code[m] = minIdx
	}
	return code, nil
}

// EncodeBatch encodes multiple vectors using BLAS GEMM for maximum throughput.
// Vectors are processed in chunks with parallel workers using pooled buffers.
//
// Uses BLAS GEMM to compute all N×K dot products per subspace in one operation:
//
//	dots[N×K] = vectors[N×dim] @ centroids[dim×K]
//
// This is significantly faster than per-vector encoding for large batches.
// Returns an error if any vector has incorrect dimension or quantizer is not trained.
func (pq *ProductQuantizer) EncodeBatch(vectors [][]float32, workers int) ([]PQCode, error) {
	if !pq.trained {
		return nil, ErrQuantizerNotTrained
	}
	if len(vectors) == 0 {
		return []PQCode{}, nil
	}

	// Validate all vectors first
	for i, v := range vectors {
		if len(v) != pq.vectorDim {
			return nil, fmt.Errorf("vector %d dimension %d != expected %d", i, len(v), pq.vectorDim)
		}
	}

	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	n := len(vectors)
	k := pq.centroidsPerSubspace
	dim := pq.subspaceDim

	// For small batches, use simple per-vector encoding
	if n < workers*2 {
		results := make([]PQCode, n)
		for i, v := range vectors {
			code, _ := pq.Encode(v) // Already validated above
			results[i] = code
		}
		return results, nil
	}

	// Chunk size balances BLAS efficiency vs memory usage
	chunkSize := (n + workers - 1) / workers
	if chunkSize < 32 {
		chunkSize = 32 // Minimum for BLAS efficiency
	}
	if chunkSize > 512 {
		chunkSize = 512 // Maximum to avoid excessive memory
	}

	results := make([]PQCode, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for chunkStart := 0; chunkStart < n; chunkStart += chunkSize {
		chunkEnd := chunkStart + chunkSize
		if chunkEnd > n {
			chunkEnd = n
		}

		wg.Add(1)
		go func(start, end int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Get pooled buffer for this worker
			buf := encodingBufferPool.Get().(*EncodingBuffer)
			defer encodingBufferPool.Put(buf)

			chunkN := end - start
			buf.EnsureCapacity(chunkN, dim, k)

			// Process each subspace
			pq.encodeChunkSubspaces(vectors, results, buf, start, end)
		}(chunkStart, chunkEnd)
	}

	wg.Wait()
	return results, nil
}

// encodeChunkSubspaces processes all subspaces for a chunk of vectors.
// Uses pre-allocated buffers to minimize allocations.
func (pq *ProductQuantizer) encodeChunkSubspaces(
	vectors [][]float32,
	results []PQCode,
	buf *EncodingBuffer,
	start, end int,
) {
	chunkN := end - start
	k := pq.centroidsPerSubspace
	dim := pq.subspaceDim

	for m := 0; m < pq.numSubspaces; m++ {
		// Reset buffers before each subspace to prevent data leakage
		buf.Reset()

		subStart := m * dim

		// Extract subvectors and compute norms
		for i := 0; i < chunkN; i++ {
			vec := vectors[start+i]
			var norm float32
			for d := 0; d < dim; d++ {
				val := vec[subStart+d]
				buf.subspaceData[i*dim+d] = val
				norm += val * val
			}
			buf.xNorms[i] = norm
		}

		// BLAS GEMM: dots[N×K] = subspaceData[N×dim] @ centroids[dim×K]
		blas32.Gemm(
			blas.NoTrans,
			blas.Trans,
			1.0,
			blas32.General{Rows: chunkN, Cols: dim, Stride: dim, Data: buf.subspaceData},
			blas32.General{Rows: k, Cols: dim, Stride: dim, Data: pq.centroidsFlat[m]},
			0.0,
			blas32.General{Rows: chunkN, Cols: k, Stride: k, Data: buf.dots},
		)

		// Find nearest centroid for each vector in chunk
		pq.assignNearestCentroids(results, buf, start, chunkN, m)
	}
}

// assignNearestCentroids finds the nearest centroid for each vector in a chunk.
func (pq *ProductQuantizer) assignNearestCentroids(
	results []PQCode,
	buf *EncodingBuffer,
	start, chunkN, subspace int,
) {
	k := pq.centroidsPerSubspace
	cNorms := pq.centroidNorms[subspace]

	for i := 0; i < chunkN; i++ {
		if results[start+i] == nil {
			results[start+i] = make(PQCode, pq.numSubspaces)
		}

		xNorm := buf.xNorms[i]
		dotRow := i * k
		minDist := float32(math.MaxFloat32)
		minIdx := uint8(0)

		for c := 0; c < k; c++ {
			dist := xNorm + cNorms[c] - 2*buf.dots[dotRow+c]
			if dist < minDist {
				minDist = dist
				minIdx = uint8(c)
			}
		}
		results[start+i][subspace] = minIdx
	}
}

// =============================================================================
// Distance Table Computation (PQ.9)
// =============================================================================

// ComputeDistanceTable pre-computes distances from query vector to all centroids.
// Returns a table where table[m][c] = distance(query_subspace_m, centroid_m_c).
// This enables O(M) distance computation for each PQCode lookup instead of O(D).
func (pq *ProductQuantizer) ComputeDistanceTable(query []float32) (*DistanceTable, error) {
	if err := pq.validateDistanceTableInput(query); err != nil {
		return nil, err
	}

	dt := NewDistanceTable(pq.numSubspaces, pq.centroidsPerSubspace)
	if dt == nil {
		return nil, errors.New("failed to create distance table")
	}

	pq.computeDistanceTableBLAS(query, dt)
	return dt, nil
}

// validateDistanceTableInput checks that query dimensions match and quantizer is trained.
func (pq *ProductQuantizer) validateDistanceTableInput(query []float32) error {
	if len(query) != pq.vectorDim {
		return fmt.Errorf("query dimension %d != expected %d", len(query), pq.vectorDim)
	}
	if !pq.trained {
		return ErrQuantizerNotTrained
	}
	return nil
}

// computeDistanceTableBLAS fills the distance table using BLAS-accelerated computation.
// Uses the identity: ||c - q||² = ||c||² + ||q||² - 2(c·q)
// BLAS Gemv computes all dot products c·q for each subspace in one call.
func (pq *ProductQuantizer) computeDistanceTableBLAS(query []float32, dt *DistanceTable) {
	k := pq.centroidsPerSubspace
	dim := pq.subspaceDim

	// Allocate buffer for dot products (reused across subspaces)
	dots := make([]float32, k)

	for m := 0; m < pq.numSubspaces; m++ {
		subStart := m * dim
		querySubvec := query[subStart : subStart+dim]

		// Compute ||q||² for this subspace
		qNorm := computeVectorNormSquared(querySubvec)

		// BLAS Gemv: dots = centroids[K×dim] @ querySubvec[dim]
		// This computes c·q for all K centroids at once
		blas32.Gemv(
			blas.NoTrans,
			1.0, // alpha
			blas32.General{
				Rows:   k,
				Cols:   dim,
				Stride: dim,
				Data:   pq.centroidsFlat[m],
			},
			blas32.Vector{N: dim, Inc: 1, Data: querySubvec},
			0.0, // beta
			blas32.Vector{N: k, Inc: 1, Data: dots},
		)

		// Compute distances: ||c - q||² = ||c||² + ||q||² - 2(c·q)
		computeDistancesFromDotProducts(dt.Table[m], pq.centroidNorms[m], dots, qNorm)
	}
}

// computeVectorNormSquared returns ||v||² = sum(v[i]²).
func computeVectorNormSquared(v []float32) float32 {
	var norm float32
	for _, val := range v {
		norm += val * val
	}
	return norm
}

// computeDistancesFromDotProducts fills distances using: dist[c] = cNorms[c] + qNorm - 2*dots[c].
func computeDistancesFromDotProducts(distances, cNorms, dots []float32, qNorm float32) {
	for c := range distances {
		distances[c] = cNorms[c] + qNorm - 2*dots[c]
	}
}

// =============================================================================
// Asymmetric Distance Computation (PQ.10)
// =============================================================================

// AsymmetricDistance computes approximate squared L2 distance using the distance table.
// This is O(M) instead of O(D) for a full vector comparison.
// Called "asymmetric" because query is full precision, database vectors are compressed.
// Returns math.MaxFloat32 if the table or code dimensions don't match.
func (pq *ProductQuantizer) AsymmetricDistance(table *DistanceTable, code PQCode) float32 {
	if table == nil || table.Table == nil {
		return float32(math.MaxFloat32)
	}
	if len(table.Table) != pq.numSubspaces || len(code) != pq.numSubspaces {
		return float32(math.MaxFloat32)
	}

	var dist float32
	for m := 0; m < pq.numSubspaces; m++ {
		centroidIdx := code[m]
		if int(centroidIdx) >= len(table.Table[m]) {
			return float32(math.MaxFloat32)
		}
		dist += table.Table[m][centroidIdx]
	}
	return dist
}

// AsymmetricDistanceBatch computes distances to multiple codes efficiently.
// Uses the same pre-computed distance table for all codes.
// Returns a slice of distances where distances[i] corresponds to codes[i].
func (pq *ProductQuantizer) AsymmetricDistanceBatch(table *DistanceTable, codes []PQCode) []float32 {
	if len(codes) == 0 {
		return []float32{}
	}

	distances := make([]float32, len(codes))
	for i, code := range codes {
		distances[i] = pq.AsymmetricDistance(table, code)
	}
	return distances
}

// =============================================================================
// Serialization (PQ.11)
// =============================================================================

// Serialization format constants
const (
	// SerializationVersion is the current format version for ProductQuantizer serialization.
	// Increment this when making backward-incompatible changes to the format.
	SerializationVersion uint8 = 1

	// SerializationMagic is a magic number to identify ProductQuantizer serialized data.
	SerializationMagic uint32 = 0x50514451 // "PQDQ" in hex (Product Quantizer Data Quantized)
)

// Serialization errors
var (
	ErrSerializationNotTrained  = errors.New("cannot serialize untrained product quantizer")
	ErrInvalidMagicNumber       = errors.New("invalid magic number in serialized data")
	ErrUnsupportedVersion       = errors.New("unsupported serialization version")
	ErrChecksumMismatch         = errors.New("checksum mismatch in serialized data")
	ErrInvalidSerializedData    = errors.New("invalid serialized data")
	ErrSerializedDataTooShort   = errors.New("serialized data is too short")
)

// pqHeader represents the serialization header for ProductQuantizer.
// This header is gob-encoded and contains all necessary metadata.
type pqHeader struct {
	Version              uint8
	NumSubspaces         int
	CentroidsPerSubspace int
	SubspaceDim          int
	VectorDim            int
}

// MarshalBinary serializes the ProductQuantizer to bytes.
// Format: [magic:4][header_len:4][header:gob][centroids:gob][checksum:4]
//
// The format includes:
//   - Magic number for format identification
//   - Version-aware header with all configuration
//   - Gob-encoded centroids (flattened)
//   - CRC32 checksum for data integrity
//
// Returns ErrSerializationNotTrained if the quantizer hasn't been trained.
func (pq *ProductQuantizer) MarshalBinary() ([]byte, error) {
	if !pq.trained {
		return nil, ErrSerializationNotTrained
	}

	var buf bytes.Buffer

	// Write magic number
	if err := binary.Write(&buf, binary.LittleEndian, SerializationMagic); err != nil {
		return nil, fmt.Errorf("write magic: %w", err)
	}

	// Encode header
	header := pqHeader{
		Version:              SerializationVersion,
		NumSubspaces:         pq.numSubspaces,
		CentroidsPerSubspace: pq.centroidsPerSubspace,
		SubspaceDim:          pq.subspaceDim,
		VectorDim:            pq.vectorDim,
	}

	var headerBuf bytes.Buffer
	headerEnc := gob.NewEncoder(&headerBuf)
	if err := headerEnc.Encode(header); err != nil {
		return nil, fmt.Errorf("encode header: %w", err)
	}

	// Write header length and header
	headerLen := uint32(headerBuf.Len())
	if err := binary.Write(&buf, binary.LittleEndian, headerLen); err != nil {
		return nil, fmt.Errorf("write header length: %w", err)
	}
	if _, err := buf.Write(headerBuf.Bytes()); err != nil {
		return nil, fmt.Errorf("write header: %w", err)
	}

	// Encode centroids using gob
	enc := gob.NewEncoder(&buf)
	for m := 0; m < pq.numSubspaces; m++ {
		for c := 0; c < pq.centroidsPerSubspace; c++ {
			if err := enc.Encode(pq.centroids[m][c]); err != nil {
				return nil, fmt.Errorf("encode centroid [%d][%d]: %w", m, c, err)
			}
		}
	}

	// Calculate checksum of everything written so far
	checksum := crc32.ChecksumIEEE(buf.Bytes())
	if err := binary.Write(&buf, binary.LittleEndian, checksum); err != nil {
		return nil, fmt.Errorf("write checksum: %w", err)
	}

	return buf.Bytes(), nil
}

// UnmarshalBinary deserializes a ProductQuantizer from bytes.
// Returns an error if the data is invalid, corrupted, or from an unsupported version.
func (pq *ProductQuantizer) UnmarshalBinary(data []byte) error {
	if len(data) < 12 { // magic(4) + header_len(4) + min_checksum(4)
		return ErrSerializedDataTooShort
	}

	buf := bytes.NewReader(data)

	// Read and verify magic number
	var magic uint32
	if err := binary.Read(buf, binary.LittleEndian, &magic); err != nil {
		return fmt.Errorf("read magic: %w", err)
	}
	if magic != SerializationMagic {
		return ErrInvalidMagicNumber
	}

	// Read header length
	var headerLen uint32
	if err := binary.Read(buf, binary.LittleEndian, &headerLen); err != nil {
		return fmt.Errorf("read header length: %w", err)
	}

	// Verify checksum before proceeding
	// Checksum is over everything except the final 4 bytes
	if len(data) < int(headerLen)+12 {
		return ErrSerializedDataTooShort
	}
	dataWithoutChecksum := data[:len(data)-4]
	expectedChecksum := crc32.ChecksumIEEE(dataWithoutChecksum)

	// Read stored checksum from the end
	storedChecksum := binary.LittleEndian.Uint32(data[len(data)-4:])
	if storedChecksum != expectedChecksum {
		return ErrChecksumMismatch
	}

	// Read header
	headerData := make([]byte, headerLen)
	if _, err := buf.Read(headerData); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	var header pqHeader
	headerDec := gob.NewDecoder(bytes.NewReader(headerData))
	if err := headerDec.Decode(&header); err != nil {
		return fmt.Errorf("decode header: %w", err)
	}

	// Verify version
	if header.Version != SerializationVersion {
		return fmt.Errorf("%w: got %d, expected %d", ErrUnsupportedVersion, header.Version, SerializationVersion)
	}

	// Apply header values
	pq.numSubspaces = header.NumSubspaces
	pq.centroidsPerSubspace = header.CentroidsPerSubspace
	pq.subspaceDim = header.SubspaceDim
	pq.vectorDim = header.VectorDim

	// Create a new reader that excludes the checksum for gob decoding
	// The remaining data is from current position to end-4 (checksum)
	currentPos, _ := buf.Seek(0, 1) // Get current position
	centroidData := data[currentPos : len(data)-4]
	centroidReader := bytes.NewReader(centroidData)
	dec := gob.NewDecoder(centroidReader)

	// Read centroids
	pq.centroids = make([][][]float32, pq.numSubspaces)
	for m := 0; m < pq.numSubspaces; m++ {
		pq.centroids[m] = make([][]float32, pq.centroidsPerSubspace)
		for c := 0; c < pq.centroidsPerSubspace; c++ {
			if err := dec.Decode(&pq.centroids[m][c]); err != nil {
				return fmt.Errorf("decode centroid [%d][%d]: %w", m, c, err)
			}
		}
	}

	pq.trained = true
	pq.buildEncodingCache() // Rebuild BLAS-compatible structures
	return nil
}

// =============================================================================
// PQCode Serialization (PQ.11)
// =============================================================================

// MarshalBinary serializes the PQCode to bytes.
// The format is simply the raw bytes of the code since it's already a []uint8.
func (code PQCode) MarshalBinary() ([]byte, error) {
	if code == nil {
		return []byte{}, nil
	}
	result := make([]byte, len(code))
	copy(result, code)
	return result, nil
}

// UnmarshalBinary deserializes a PQCode from bytes.
func (code *PQCode) UnmarshalBinary(data []byte) error {
	if data == nil {
		*code = nil
		return nil
	}
	*code = make(PQCode, len(data))
	copy(*code, data)
	return nil
}
