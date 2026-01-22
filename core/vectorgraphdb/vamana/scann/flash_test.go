package scann

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
)

func TestFlashCoder_Timing(t *testing.T) {
	n := 100000
	dim := 768
	R := 64

	vectors := make([][]float32, n)
	flat := make([]float32, n*dim)
	for i := range n {
		vectors[i] = flat[i*dim : (i+1)*dim : (i+1)*dim]
		for j := range dim {
			vectors[i][j] = rand.Float32()*2 - 1
		}
	}

	config := vamana.DefaultConfig()
	batchConf := DefaultBatchBuildConfig()
	avqConf := DefaultAVQConfig()
	builder := NewBatchBuilder(batchConf, avqConf, config)

	start := time.Now()
	fc := builder.NewFlashCoder(vectors, R)
	total := time.Since(start)

	t.Logf("FlashCoder for %d vectors:", n)
	t.Logf("  Total: %v", total)
	t.Logf("  numSubspaces: %d", fc.numSubspaces)
	t.Logf("  numCentroids: %d", fc.numCentroids)
	t.Logf("  subspaceDim: %d", fc.subspaceDim)
	t.Logf("  Throughput: %.0f vectors/sec", float64(n)/total.Seconds())
}

func BenchmarkFlashCoder_Create(b *testing.B) {
	n := 50000
	dim := 768
	R := 64

	vectors := make([][]float32, n)
	flat := make([]float32, n*dim)
	for i := range n {
		vectors[i] = flat[i*dim : (i+1)*dim : (i+1)*dim]
		for j := range dim {
			vectors[i][j] = rand.Float32()*2 - 1
		}
	}

	config := vamana.DefaultConfig()
	batchConf := DefaultBatchBuildConfig()
	avqConf := DefaultAVQConfig()
	builder := NewBatchBuilder(batchConf, avqConf, config)

	b.ResetTimer()
	for range b.N {
		_ = builder.NewFlashCoder(vectors, R)
	}
}
