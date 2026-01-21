package hnsw

import (
	"math"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas32"
)

// DotProduct computes the dot product of two vectors using BLAS.
// Returns 0 if vectors have different lengths or are empty.
func DotProduct(a, b []float32) float32 {
	n := len(a)
	if n != len(b) || n == 0 {
		return 0
	}
	return blas32.Dot(
		blas32.Vector{N: n, Inc: 1, Data: a},
		blas32.Vector{N: n, Inc: 1, Data: b},
	)
}

// BatchDotProducts computes dot products of query against multiple vectors.
// Returns dot(query, vectors[i]) for each i.
// vectors is stored row-major: vectors[i*dim : (i+1)*dim] is vector i.
func BatchDotProducts(query []float32, vectors []float32, numVectors int, results []float32) {
	dim := len(query)
	if dim == 0 || numVectors == 0 {
		return
	}

	mat := blas32.General{
		Rows:   numVectors,
		Cols:   dim,
		Stride: dim,
		Data:   vectors,
	}
	x := blas32.Vector{N: dim, Inc: 1, Data: query}
	y := blas32.Vector{N: numVectors, Inc: 1, Data: results}

	blas32.Gemv(blas.NoTrans, 1.0, mat, x, 0.0, y)
}

// Magnitude computes the L2 norm (magnitude) of a vector.
func Magnitude(v []float32) float64 {
	if len(v) == 0 {
		return 0
	}
	return math.Sqrt(float64(blas32.Dot(
		blas32.Vector{N: len(v), Inc: 1, Data: v},
		blas32.Vector{N: len(v), Inc: 1, Data: v},
	)))
}

// CosineSimilarity computes cosine similarity between two vectors.
// Uses pre-computed magnitudes for efficiency.
// Returns 0 if either magnitude is zero.
func CosineSimilarity(a, b []float32, magA, magB float64) float64 {
	if magA == 0 || magB == 0 {
		return 0
	}
	dot := float64(DotProduct(a, b))
	return dot / (magA * magB)
}

// CosineSimilarityVectors computes cosine similarity computing magnitudes.
// Less efficient than using pre-computed magnitudes.
func CosineSimilarityVectors(a, b []float32) float64 {
	return CosineSimilarity(a, b, Magnitude(a), Magnitude(b))
}

// CosineDistance computes the cosine distance between two vectors.
// Cosine distance = 1 - cosine similarity, yielding a value in [0, 2].
// Uses pre-computed magnitudes for efficiency.
// Returns 2.0 (maximum distance) if either magnitude is zero.
func CosineDistance(a, b []float32, magA, magB float64) float64 {
	return 1.0 - CosineSimilarity(a, b, magA, magB)
}

// EuclideanDistance computes Euclidean distance between two vectors.
func EuclideanDistance(a, b []float32) float64 {
	n := len(a)
	if n != len(b) {
		return math.MaxFloat64
	}
	if n == 0 {
		return 0
	}
	return euclideanDistanceBLAS(a, b)
}

// euclideanDistanceBLAS computes ||a - b||_2 using BLAS operations.
// Uses: ||a-b||^2 = ||a||^2 + ||b||^2 - 2*dot(a,b)
func euclideanDistanceBLAS(a, b []float32) float64 {
	n := len(a)
	vecA := blas32.Vector{N: n, Inc: 1, Data: a}
	vecB := blas32.Vector{N: n, Inc: 1, Data: b}

	aNorm := float64(blas32.Dot(vecA, vecA))
	bNorm := float64(blas32.Dot(vecB, vecB))
	aDotB := float64(blas32.Dot(vecA, vecB))

	sqDist := aNorm + bNorm - 2*aDotB
	if sqDist < 0 {
		sqDist = 0
	}
	return math.Sqrt(sqDist)
}

// NormalizeVectorCopy returns a normalized copy of the input vector.
// The original vector is not modified.
// Returns the normalized copy and the original magnitude.
// If the vector has zero magnitude, returns a copy of the original and 0.
func NormalizeVectorCopy(v []float32) ([]float32, float64) {
	n := len(v)
	if n == 0 {
		return nil, 0
	}
	vec := blas32.Vector{N: n, Inc: 1, Data: v}
	mag := math.Sqrt(float64(blas32.Dot(vec, vec)))

	result := make([]float32, n)
	if mag == 0 {
		copy(result, v)
		return result, 0
	}

	invMag := float32(1.0 / mag)
	resultVec := blas32.Vector{N: n, Inc: 1, Data: result}
	blas32.Axpy(invMag, vec, resultVec)
	return result, mag
}

// NormalizeVector normalizes a vector to unit length in-place.
// Returns the original magnitude.
//
// Deprecated: Use NormalizeVectorCopy instead to avoid corrupting shared vectors.
// This function modifies the input vector in-place, which can cause issues when:
//   - The caller's vector is unexpectedly modified
//   - Multiple normalizations compound errors
//   - Shared vectors (e.g., from query) get corrupted
//
// This function is retained for backward compatibility but new code should use
// NormalizeVectorCopy.
func NormalizeVector(v []float32) float64 {
	n := len(v)
	if n == 0 {
		return 0
	}
	vec := blas32.Vector{N: n, Inc: 1, Data: v}
	mag := math.Sqrt(float64(blas32.Dot(vec, vec)))
	if mag == 0 {
		return 0
	}
	invMag := float32(1.0 / mag)
	blas32.Scal(invMag, vec)
	return mag
}
