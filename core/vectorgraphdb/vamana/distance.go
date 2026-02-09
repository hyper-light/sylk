package vamana

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/viterin/vek/vek32"
)

// DotProduct computes the dot product of two vectors using SIMD.
func DotProduct(a, b []float32) float32 {
	return vek32.Dot(a, b)
}

// BatchDotProducts computes dot products of query with multiple vectors.
// For the flat array version, use BatchDotProductsFlat.
func BatchDotProducts(query []float32, vectors [][]float32) []float32 {
	results := make([]float32, len(vectors))
	for i, vec := range vectors {
		results[i] = vek32.Dot(query, vec)
	}
	return results
}

// BatchDotProductsFlat computes dot products of query with multiple vectors
// stored in a flat array. The vectors are packed sequentially in candidateVecs,
// with each vector having dimension len(query). Results are written to the
// pre-allocated dots slice.
func BatchDotProductsFlat(query []float32, candidateVecs []float32, n int, dots []float32) {
	dim := len(query)
	for i := 0; i < n; i++ {
		vec := candidateVecs[i*dim : (i+1)*dim]
		dots[i] = vek32.Dot(query, vec)
	}
}

// Magnitude computes the L2 norm (magnitude) of a vector.
func Magnitude(v []float32) float64 {
	return math.Sqrt(float64(vek32.Dot(v, v)))
}

// CosineSimilarity computes the cosine similarity between two vectors.
// Returns a value in [-1, 1] where 1 means identical direction.
// Returns 0 for nil or empty vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	dot := vek32.Dot(a, b)
	magA := math.Sqrt(float64(vek32.Dot(a, a)))
	magB := math.Sqrt(float64(vek32.Dot(b, b)))
	if magA == 0 || magB == 0 {
		return 0
	}
	return float64(dot) / (magA * magB)
}

// CosineDistance computes the cosine distance (1 - similarity).
// Returns a value in [0, 2] where 0 means identical direction.
func CosineDistance(a, b []float32) float64 {
	return 1 - CosineSimilarity(a, b)
}

// EuclideanDistance computes the L2 distance between two vectors.
func EuclideanDistance(a, b []float32) float64 {
	diff := make([]float32, len(a))
	copy(diff, a)
	vek32.Sub_Inplace(diff, b)
	return math.Sqrt(float64(vek32.Dot(diff, diff)))
}

// NormalizeVectorCopy returns a normalized copy of the vector.
func NormalizeVectorCopy(v []float32) []float32 {
	mag := Magnitude(v)
	if mag == 0 {
		return make([]float32, len(v))
	}
	result := make([]float32, len(v))
	copy(result, v)
	invMag := float32(1.0 / mag)
	vek32.MulNumber_Inplace(result, invMag)
	return result
}

type MagnitudeCache struct {
	mags atomic.Pointer[[]float64]
	mu   sync.Mutex
}

func NewMagnitudeCache(capacity int) *MagnitudeCache {
	mags := make([]float64, capacity)
	c := &MagnitudeCache{}
	c.mags.Store(&mags)
	return c
}

func (c *MagnitudeCache) Get(id uint32) (float64, bool) {
	mags := *c.mags.Load()
	if int(id) >= len(mags) {
		return 0, false
	}
	mag := mags[id]
	return mag, mag != 0
}

func (c *MagnitudeCache) Set(id uint32, mag float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	mags := *c.mags.Load()
	if int(id) >= len(mags) {
		newMags := make([]float64, id+1)
		copy(newMags, mags)
		newMags[id] = mag
		c.mags.Store(&newMags)
		return
	}
	mags[id] = mag
}

func (c *MagnitudeCache) GetOrCompute(id uint32, vector []float32) float64 {
	if mag, ok := c.Get(id); ok {
		return mag
	}

	mag := Magnitude(vector)
	c.Set(id, mag)
	return mag
}

func (c *MagnitudeCache) Len() int {
	return len(*c.mags.Load())
}

func (c *MagnitudeCache) Grow(newCap int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	mags := *c.mags.Load()
	if newCap <= len(mags) {
		return
	}
	newMags := make([]float64, newCap)
	copy(newMags, mags)
	c.mags.Store(&newMags)
}

func (c *MagnitudeCache) Slice() []float64 {
	return *c.mags.Load()
}
