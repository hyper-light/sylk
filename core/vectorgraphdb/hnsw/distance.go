package hnsw

import "math"

// DotProduct computes the dot product of two vectors.
// Returns 0 if vectors have different lengths.
func DotProduct(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	return dotProductScalar(a, b)
}

// dotProductScalar computes dot product using scalar operations.
func dotProductScalar(a, b []float32) float32 {
	var sum float32
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}

// Magnitude computes the L2 norm (magnitude) of a vector.
func Magnitude(v []float32) float64 {
	return math.Sqrt(float64(DotProduct(v, v)))
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
	if len(a) != len(b) {
		return math.MaxFloat64
	}
	return euclideanDistanceScalar(a, b)
}

// euclideanDistanceScalar computes Euclidean distance using scalar ops.
func euclideanDistanceScalar(a, b []float32) float64 {
	var sum float64
	for i := range a {
		diff := float64(a[i] - b[i])
		sum += diff * diff
	}
	return math.Sqrt(sum)
}

// NormalizeVectorCopy returns a normalized copy of the input vector.
// The original vector is not modified.
// Returns the normalized copy and the original magnitude.
// If the vector has zero magnitude, returns a copy of the original and 0.
func NormalizeVectorCopy(v []float32) ([]float32, float64) {
	mag := Magnitude(v)
	result := make([]float32, len(v))
	if mag == 0 {
		copy(result, v)
		return result, 0
	}
	invMag := float32(1.0 / mag)
	for i := range v {
		result[i] = v[i] * invMag
	}
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
	mag := Magnitude(v)
	if mag == 0 {
		return 0
	}
	invMag := float32(1.0 / mag)
	for i := range v {
		v[i] *= invMag
	}
	return mag
}
