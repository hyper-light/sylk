package benchmark

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
	"github.com/adalundhe/sylk/core/vectorgraphdb/quantization"
)

// =============================================================================
// Test Data Generation
// =============================================================================

// generateRandomVector creates a random normalized vector of given dimension.
func generateRandomVector(dim int, rng *rand.Rand) []float32 {
	vec := make([]float32, dim)
	var mag float32
	for i := range vec {
		vec[i] = rng.Float32()*2 - 1
		mag += vec[i] * vec[i]
	}
	// Normalize
	if mag > 0 {
		invMag := 1.0 / float32(mag)
		for i := range vec {
			vec[i] *= invMag
		}
	}
	return vec
}

// generateTestVectors creates n random vectors of given dimension.
func generateTestVectors(n, dim int) [][]float32 {
	rng := rand.New(rand.NewSource(42))
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = generateRandomVector(dim, rng)
	}
	return vectors
}

// generateTestIDs creates n unique string IDs.
func generateTestIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("node-%d", i)
	}
	return ids
}

// =============================================================================
// HNSW Insertion Benchmarks
// =============================================================================

func BenchmarkHNSWInsert(b *testing.B) {
	benchmarks := []struct {
		name      string
		batchSize int
	}{
		{"Batch100", 100},
		{"Batch1000", 1000},
		{"Batch10000", 10000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			benchmarkHNSWInsertN(b, bm.batchSize)
		})
	}
}

func benchmarkHNSWInsertN(b *testing.B, n int) {
	vectors := generateTestVectors(n, vectorgraphdb.EmbeddingDimension)
	ids := generateTestIDs(n)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		idx := hnsw.New(hnsw.DefaultConfig())
		b.StartTimer()

		for j := 0; j < n; j++ {
			_ = idx.Insert(ids[j], vectors[j], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
		}
	}
}

// =============================================================================
// HNSW Search Benchmarks
// =============================================================================

func BenchmarkHNSWSearch(b *testing.B) {
	benchmarks := []struct {
		name string
		k    int
	}{
		{"K10", 10},
		{"K100", 100},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			benchmarkHNSWSearchK(b, bm.k)
		})
	}
}

func benchmarkHNSWSearchK(b *testing.B, k int) {
	const indexSize = 5000
	vectors := generateTestVectors(indexSize, vectorgraphdb.EmbeddingDimension)
	ids := generateTestIDs(indexSize)
	queries := generateTestVectors(100, vectorgraphdb.EmbeddingDimension)

	idx := hnsw.New(hnsw.DefaultConfig())
	for i := 0; i < indexSize; i++ {
		_ = idx.Insert(ids[i], vectors[i], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		query := queries[i%len(queries)]
		_ = idx.Search(query, k, nil)
	}
}

// =============================================================================
// Neighbor Set Benchmarks
// =============================================================================

func BenchmarkNeighborSet(b *testing.B) {
	benchmarks := []struct {
		name string
		fn   func(*testing.B)
	}{
		{"Add", benchmarkNeighborSetAdd},
		{"Contains", benchmarkNeighborSetContains},
		{"GetSorted", benchmarkNeighborSetGetSorted},
		{"GetTopK", benchmarkNeighborSetGetTopK},
		{"TrimToSize", benchmarkNeighborSetTrimToSize},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, bm.fn)
	}
}

func benchmarkNeighborSetAdd(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ns := hnsw.NewNeighborSet()
		for j := 0; j < 100; j++ {
			ns.Add(uint32(j), float32(j)*0.1)
		}
	}
}

func benchmarkNeighborSetContains(b *testing.B) {
	ns := hnsw.NewNeighborSet()
	for j := 0; j < 100; j++ {
		ns.Add(uint32(j), float32(j)*0.1)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ns.Contains(uint32(i % 100))
	}
}

func benchmarkNeighborSetGetSorted(b *testing.B) {
	ns := hnsw.NewNeighborSet()
	for j := 0; j < 100; j++ {
		ns.Add(uint32(j), float32(j)*0.1)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ns.GetSortedNeighbors()
	}
}

func benchmarkNeighborSetGetTopK(b *testing.B) {
	ns := hnsw.NewNeighborSet()
	for j := 0; j < 100; j++ {
		ns.Add(uint32(j), float32(j)*0.1)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ns.GetTopK(10)
	}
}

func benchmarkNeighborSetTrimToSize(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		ns := hnsw.NewNeighborSet()
		for j := 0; j < 100; j++ {
			ns.Add(uint32(j), float32(j)*0.1)
		}
		b.StartTimer()

		ns.TrimToSize(50)
	}
}

// =============================================================================
// Concurrent Neighbor Set Benchmarks
// =============================================================================

func BenchmarkConcurrentNeighborSet(b *testing.B) {
	benchmarks := []struct {
		name string
		fn   func(*testing.B)
	}{
		{"Add", benchmarkConcurrentNeighborSetAdd},
		{"Contains", benchmarkConcurrentNeighborSetContains},
		{"AddWithLimit", benchmarkConcurrentNeighborSetAddWithLimit},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, bm.fn)
	}
}

func benchmarkConcurrentNeighborSetAdd(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ns := hnsw.NewConcurrentNeighborSet()
		for j := 0; j < 100; j++ {
			ns.Add(uint32(j), float32(j)*0.1)
		}
	}
}

func benchmarkConcurrentNeighborSetContains(b *testing.B) {
	ns := hnsw.NewConcurrentNeighborSet()
	for j := 0; j < 100; j++ {
		ns.Add(uint32(j), float32(j)*0.1)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ns.Contains(uint32(i % 100))
	}
}

func benchmarkConcurrentNeighborSetAddWithLimit(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		ns := hnsw.NewConcurrentNeighborSet()
		b.StartTimer()

		for j := 0; j < 100; j++ {
			ns.AddWithLimit(uint32(j), float32(j)*0.1, 50)
		}
	}
}

// =============================================================================
// Distance Computation Benchmarks
// =============================================================================

func BenchmarkDistance(b *testing.B) {
	benchmarks := []struct {
		name string
		fn   func(*testing.B)
	}{
		{"Cosine", benchmarkCosineSimilarity},
		{"Euclidean", benchmarkEuclideanDistance},
		{"DotProduct", benchmarkDotProduct},
		{"Magnitude", benchmarkMagnitude},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, bm.fn)
	}
}

func benchmarkCosineSimilarity(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vecA := generateRandomVector(vectorgraphdb.EmbeddingDimension, rng)
	vecB := generateRandomVector(vectorgraphdb.EmbeddingDimension, rng)
	magA := hnsw.Magnitude(vecA)
	magB := hnsw.Magnitude(vecB)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = hnsw.CosineSimilarity(vecA, vecB, magA, magB)
	}
}

func benchmarkEuclideanDistance(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vecA := generateRandomVector(vectorgraphdb.EmbeddingDimension, rng)
	vecB := generateRandomVector(vectorgraphdb.EmbeddingDimension, rng)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = hnsw.EuclideanDistance(vecA, vecB)
	}
}

func benchmarkDotProduct(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vecA := generateRandomVector(vectorgraphdb.EmbeddingDimension, rng)
	vecB := generateRandomVector(vectorgraphdb.EmbeddingDimension, rng)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = hnsw.DotProduct(vecA, vecB)
	}
}

func benchmarkMagnitude(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vec := generateRandomVector(vectorgraphdb.EmbeddingDimension, rng)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = hnsw.Magnitude(vec)
	}
}

// =============================================================================
// Quantized HNSW Benchmarks
// =============================================================================

func BenchmarkQuantizedHNSW(b *testing.B) {
	benchmarks := []struct {
		name string
		fn   func(*testing.B)
	}{
		{"InsertPreTrain", benchmarkQuantizedHNSWInsertPreTrain},
		{"InsertPostTrain", benchmarkQuantizedHNSWInsertPostTrain},
		{"SearchK10", benchmarkQuantizedHNSWSearchK10},
		{"SearchK100", benchmarkQuantizedHNSWSearchK100},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, bm.fn)
	}
}

func benchmarkQuantizedHNSWInsertPreTrain(b *testing.B) {
	const n = 1000
	vectors := generateTestVectors(n, vectorgraphdb.EmbeddingDimension)
	ids := generateTestIDs(n)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		cfg := quantization.DefaultQuantizedHNSWConfig()
		cfg.TrainingThreshold = 0 // Disable auto-training
		idx, _ := quantization.NewQuantizedHNSW(cfg)
		b.StartTimer()

		for j := 0; j < n; j++ {
			_ = idx.Insert(ids[j], vectors[j], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
		}
	}
}

func benchmarkQuantizedHNSWInsertPostTrain(b *testing.B) {
	const n = 1000
	vectors := generateTestVectors(n+3000, vectorgraphdb.EmbeddingDimension)
	ids := generateTestIDs(n + 3000)

	// Pre-create and train index
	cfg := quantization.DefaultQuantizedHNSWConfig()
	cfg.TrainingThreshold = 0
	idx, _ := quantization.NewQuantizedHNSW(cfg)

	// Insert training data
	for j := 0; j < 3000; j++ {
		_ = idx.Insert(ids[j], vectors[j], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	}
	_ = idx.Train(nil)

	// Prepare new vectors for benchmark
	newVectors := vectors[3000:]
	newIDs := ids[3000:]

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Create fresh trained index each iteration
		cfg := quantization.DefaultQuantizedHNSWConfig()
		cfg.TrainingThreshold = 0
		freshIdx, _ := quantization.NewQuantizedHNSW(cfg)
		for j := 0; j < 3000; j++ {
			_ = freshIdx.Insert(ids[j], vectors[j], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
		}
		_ = freshIdx.Train(nil)
		b.StartTimer()

		for j := 0; j < n; j++ {
			_ = freshIdx.Insert(newIDs[j], newVectors[j], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
		}
	}
}

func benchmarkQuantizedHNSWSearchK10(b *testing.B) {
	benchmarkQuantizedHNSWSearchK(b, 10)
}

func benchmarkQuantizedHNSWSearchK100(b *testing.B) {
	benchmarkQuantizedHNSWSearchK(b, 100)
}

func benchmarkQuantizedHNSWSearchK(b *testing.B, k int) {
	const indexSize = 5000
	vectors := generateTestVectors(indexSize, vectorgraphdb.EmbeddingDimension)
	ids := generateTestIDs(indexSize)
	queries := generateTestVectors(100, vectorgraphdb.EmbeddingDimension)

	cfg := quantization.DefaultQuantizedHNSWConfig()
	cfg.TrainingThreshold = 0
	idx, _ := quantization.NewQuantizedHNSW(cfg)

	for i := 0; i < indexSize; i++ {
		_ = idx.Insert(ids[i], vectors[i], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
	}
	_ = idx.Train(nil)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		query := queries[i%len(queries)]
		_, _ = idx.Search(query, k, nil)
	}
}

// =============================================================================
// Comparison: Quantized vs Unquantized HNSW
// =============================================================================

func BenchmarkHNSWComparison(b *testing.B) {
	const indexSize = 5000
	vectors := generateTestVectors(indexSize, vectorgraphdb.EmbeddingDimension)
	ids := generateTestIDs(indexSize)
	queries := generateTestVectors(100, vectorgraphdb.EmbeddingDimension)

	b.Run("Unquantized/Search", func(b *testing.B) {
		idx := hnsw.New(hnsw.DefaultConfig())
		for i := 0; i < indexSize; i++ {
			_ = idx.Insert(ids[i], vectors[i], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			query := queries[i%len(queries)]
			_ = idx.Search(query, 10, nil)
		}
	})

	b.Run("Quantized/Search", func(b *testing.B) {
		cfg := quantization.DefaultQuantizedHNSWConfig()
		cfg.TrainingThreshold = 0
		idx, _ := quantization.NewQuantizedHNSW(cfg)

		for i := 0; i < indexSize; i++ {
			_ = idx.Insert(ids[i], vectors[i], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
		}
		_ = idx.Train(nil)

		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			query := queries[i%len(queries)]
			_, _ = idx.Search(query, 10, nil)
		}
	})
}

// =============================================================================
// Memory Allocation Benchmarks
// =============================================================================

func BenchmarkMemoryAllocation(b *testing.B) {
	b.Run("VectorAllocation", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = make([]float32, vectorgraphdb.EmbeddingDimension)
		}
	})

	b.Run("NeighborSetAllocation", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = hnsw.NewNeighborSetWithCapacity(16)
		}
	})

	b.Run("SearchResultSlice", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = make([]hnsw.SearchResult, 0, 100)
		}
	})
}

// =============================================================================
// Batch Loader Benchmarks
// =============================================================================

func BenchmarkBatchLoader(b *testing.B) {
	benchmarks := []struct {
		name      string
		batchSize int
	}{
		{"Batch100", 100},
		{"Batch1000", 1000},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			benchmarkBatchLoaderN(b, bm.batchSize)
		})
	}
}

func benchmarkBatchLoaderN(b *testing.B, n int) {
	vectors := generateTestVectors(n, vectorgraphdb.EmbeddingDimension)
	ids := generateTestIDs(n)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		idx := hnsw.New(hnsw.DefaultConfig())
		b.StartTimer()

		// Simulate batch loading pattern
		for j := 0; j < n; j++ {
			_ = idx.Insert(ids[j], vectors[j], vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFunction)
		}
	}
}
