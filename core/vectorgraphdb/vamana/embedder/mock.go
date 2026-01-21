package embedder

import (
	"context"
	"hash/fnv"
	"math"
	"sync"
	"time"
)

type MockEmbedder struct {
	dimension int
	latency   time.Duration
}

func NewMockEmbedder(dimension int) *MockEmbedder {
	return &MockEmbedder{dimension: dimension}
}

func NewMockEmbedderWithLatency(dimension int, latency time.Duration) *MockEmbedder {
	return &MockEmbedder{dimension: dimension, latency: latency}
}

func (m *MockEmbedder) Dimension() int {
	return m.dimension
}

func (m *MockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.latency > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.latency):
		}
	}
	return deterministicEmbed(text, m.dimension), nil
}

func (m *MockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))

	if m.latency > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.latency):
		}
	}

	var wg sync.WaitGroup
	for i, text := range texts {
		wg.Add(1)
		go func(idx int, t string) {
			defer wg.Done()
			results[idx] = deterministicEmbed(t, m.dimension)
		}(i, text)
	}
	wg.Wait()

	return results, nil
}

// PCG LCG constants (Knuth MMIX)
const (
	pcgMult = 6364136223846793005
	pcgInc  = 1442695040888963407
)

func deterministicEmbed(text string, dim int) []float32 {
	vec := make([]float32, dim)

	h := fnv.New64a()
	h.Write([]byte(text))
	state := h.Sum64()

	for i := range dim {
		state = state*pcgMult + pcgInc
		vec[i] = (float32(state>>32)/float32(math.MaxUint32))*2 - 1
	}

	var mag float64
	for _, v := range vec {
		mag += float64(v * v)
	}
	invMag := float32(1.0 / math.Sqrt(mag))
	for i := range vec {
		vec[i] *= invMag
	}

	return vec
}
