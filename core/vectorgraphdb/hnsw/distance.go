package hnsw

import (
	"math"

	"github.com/viterin/vek/vek32"
	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/blas/blas32"
)

// DotProduct computes the dot product of two vectors using SIMD.
// Returns 0 if vectors have different lengths or are empty.
func DotProduct(a, b []float32) float32 {
	n := len(a)
	if n != len(b) || n == 0 {
		return 0
	}
	return vek32.Dot(a, b)
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
	return math.Sqrt(float64(vek32.Dot(v, v)))
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

func euclideanDistanceBLAS(a, b []float32) float64 {
	aNorm := float64(vek32.Dot(a, a))
	bNorm := float64(vek32.Dot(b, b))
	aDotB := float64(vek32.Dot(a, b))

	sqDist := aNorm + bNorm - 2*aDotB
	if sqDist < 0 {
		sqDist = 0
	}
	return math.Sqrt(sqDist)
}

func NormalizeVectorCopy(v []float32) ([]float32, float64) {
	n := len(v)
	if n == 0 {
		return nil, 0
	}
	mag := math.Sqrt(float64(vek32.Dot(v, v)))

	result := make([]float32, n)
	if mag == 0 {
		copy(result, v)
		return result, 0
	}

	invMag := float32(1.0 / mag)
	for i, val := range v {
		result[i] = val * invMag
	}
	return result, mag
}

func NormalizeVector(v []float32) float64 {
	if len(v) == 0 {
		return 0
	}
	mag := math.Sqrt(float64(vek32.Dot(v, v)))
	if mag == 0 {
		return 0
	}
	invMag := float32(1.0 / mag)
	for i := range v {
		v[i] *= invMag
	}
	return mag
}
