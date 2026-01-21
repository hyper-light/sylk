package vamana

import (
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

var (
	DotProduct          = hnsw.DotProduct
	BatchDotProducts    = hnsw.BatchDotProducts
	Magnitude           = hnsw.Magnitude
	CosineSimilarity    = hnsw.CosineSimilarity
	CosineDistance      = hnsw.CosineDistance
	EuclideanDistance   = hnsw.EuclideanDistance
	NormalizeVectorCopy = hnsw.NormalizeVectorCopy
)

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
