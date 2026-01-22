package quantization

import (
	"math"
	"math/rand"
	"sync"

	"gonum.org/v1/gonum/blas/blas32"
)

// GenerateOrthogonalMatrix creates a random orthogonal rotation matrix using
// Gram-Schmidt orthogonalization with a seeded RNG for reproducibility.
// The matrix satisfies R × R^T = I (orthogonality property).
func GenerateOrthogonalMatrix(dim int, seed int64) [][]float32 {
	if dim <= 0 {
		return nil
	}

	rng := rand.New(rand.NewSource(seed))
	matrix := make([][]float32, dim)
	for i := range matrix {
		matrix[i] = make([]float32, dim)
	}

	for i := 0; i < dim; i++ {
		matrix[i] = generateOrthogonalVector(matrix[:i], dim, rng)
	}

	return matrix
}

func generateOrthogonalVector(existing [][]float32, dim int, rng *rand.Rand) []float32 {
	const maxAttempts = 100

	for attempt := 0; attempt < maxAttempts; attempt++ {
		vec := make([]float32, dim)
		for j := range vec {
			vec[j] = float32(rng.NormFloat64())
		}

		for _, e := range existing {
			dot := dotProduct(vec, e)
			for j := range vec {
				vec[j] -= dot * e[j]
			}
		}

		norm := vectorNorm(vec)
		if norm > 1e-6 {
			for j := range vec {
				vec[j] /= norm
			}
			return vec
		}
	}

	vec := make([]float32, dim)
	vec[len(existing)%dim] = 1.0
	return vec
}

func dotProduct(a, b []float32) float32 {
	n := len(a)
	if n == 0 {
		return 0
	}
	vecA := blas32.Vector{N: n, Inc: 1, Data: a}
	vecB := blas32.Vector{N: n, Inc: 1, Data: b}
	return blas32.Dot(vecA, vecB)
}

func vectorNorm(v []float32) float32 {
	var sum float32
	for _, val := range v {
		sum += val * val
	}
	return float32(math.Sqrt(float64(sum)))
}

// RotationMatrixCache provides thread-safe caching of rotation matrices by seed.
type RotationMatrixCache struct {
	mu       sync.RWMutex
	cache    map[rotationKey][][]float32
	keys     []rotationKey
	accessCt map[rotationKey]int64
	counter  int64
	maxSize  int
}

type rotationKey struct {
	dim  int
	seed int64
}

// NewRotationMatrixCache creates a cache with the specified maximum entries.
func NewRotationMatrixCache(maxSize int) *RotationMatrixCache {
	if maxSize <= 0 {
		maxSize = 16
	}
	return &RotationMatrixCache{
		cache:    make(map[rotationKey][][]float32),
		keys:     make([]rotationKey, 0, maxSize),
		accessCt: make(map[rotationKey]int64),
		maxSize:  maxSize,
	}
}

// Get retrieves or generates an orthogonal matrix for the given parameters.
func (c *RotationMatrixCache) Get(dim int, seed int64) [][]float32 {
	key := rotationKey{dim: dim, seed: seed}

	c.mu.RLock()
	if matrix, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		c.mu.Lock()
		c.counter++
		c.accessCt[key] = c.counter
		c.mu.Unlock()
		return matrix
	}
	c.mu.RUnlock()

	matrix := GenerateOrthogonalMatrix(dim, seed)

	c.mu.Lock()
	if len(c.cache) >= c.maxSize {
		c.evictLRULocked()
	}
	c.cache[key] = matrix
	c.keys = append(c.keys, key)
	c.counter++
	c.accessCt[key] = c.counter
	c.mu.Unlock()

	return matrix
}

func (c *RotationMatrixCache) evictLRULocked() {
	if len(c.keys) == 0 {
		return
	}
	var lruKey rotationKey
	var lruTime int64 = c.counter + 1
	var lruIdx int
	for i, k := range c.keys {
		if t, ok := c.accessCt[k]; ok && t < lruTime {
			lruTime = t
			lruKey = k
			lruIdx = i
		}
	}
	delete(c.cache, lruKey)
	delete(c.accessCt, lruKey)
	c.keys = append(c.keys[:lruIdx], c.keys[lruIdx+1:]...)
}

// Clear removes all cached matrices.
func (c *RotationMatrixCache) Clear() {
	c.mu.Lock()
	c.cache = make(map[rotationKey][][]float32)
	c.keys = c.keys[:0]
	c.accessCt = make(map[rotationKey]int64)
	c.counter = 0
	c.mu.Unlock()
}

// Size returns the number of cached matrices.
func (c *RotationMatrixCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

var globalRotationCache = NewRotationMatrixCache(16)

// GetCachedRotationMatrix retrieves or generates an orthogonal matrix using a global cache.
func GetCachedRotationMatrix(dim int, seed int64) [][]float32 {
	return globalRotationCache.Get(dim, seed)
}

// MatrixVectorMultiply computes result = matrix × vector.
// Matrix is [rows][cols], vector is [cols], result is [rows].
func MatrixVectorMultiply(matrix [][]float32, vector []float32) []float32 {
	if len(matrix) == 0 || len(vector) == 0 {
		return nil
	}
	rows := len(matrix)
	result := make([]float32, rows)

	for i := 0; i < rows; i++ {
		result[i] = dotProduct(matrix[i], vector)
	}
	return result
}

// MatrixVectorMultiplyInto computes result = matrix × vector without allocation.
// Caller must ensure result has length >= len(matrix).
func MatrixVectorMultiplyInto(matrix [][]float32, vector, result []float32) {
	for i := range matrix {
		result[i] = dotProduct(matrix[i], vector)
	}
}

// TransposeMatrixVectorMultiply computes result = matrix^T × vector.
// Matrix is [rows][cols], vector is [rows], result is [cols].
func TransposeMatrixVectorMultiply(matrix [][]float32, vector []float32) []float32 {
	if len(matrix) == 0 || len(vector) == 0 {
		return nil
	}
	cols := len(matrix[0])
	result := make([]float32, cols)

	for i, row := range matrix {
		v := vector[i]
		for j, m := range row {
			result[j] += v * m
		}
	}
	return result
}

// IsOrthogonal checks if a matrix is orthogonal (R × R^T ≈ I).
// Returns true if max deviation from identity is below tolerance.
func IsOrthogonal(matrix [][]float32, tolerance float32) bool {
	if len(matrix) == 0 {
		return false
	}
	n := len(matrix)
	if len(matrix[0]) != n {
		return false
	}

	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			dot := dotProduct(matrix[i], matrix[j])
			expected := float32(0)
			if i == j {
				expected = 1
			}
			if abs32(dot-expected) > tolerance {
				return false
			}
		}
	}
	return true
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
