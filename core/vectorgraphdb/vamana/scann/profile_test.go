package scann

import (
	"math/rand/v2"
	"runtime"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func TestProfile_K8sScale(t *testing.T) {
	n := 273000
	dim := 768
	R := 64

	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	baseAlloc := m.TotalAlloc

	t.Logf("=== Generating %d vectors (dim=%d) ===", n, dim)
	genStart := time.Now()
	vectors := make([][]float32, n)
	flat := make([]float32, n*dim)
	for i := range n {
		vectors[i] = flat[i*dim : (i+1)*dim : (i+1)*dim]
		for j := range dim {
			vectors[i][j] = rand.Float32()*2 - 1
		}
	}
	t.Logf("Vector generation: %v", time.Since(genStart))

	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("Memory after vectors: %d MB allocated", (m.TotalAlloc-baseAlloc)/(1024*1024))

	tmpDir := t.TempDir()
	config := vamana.DefaultConfig()

	vectorStore, _ := storage.CreateVectorStore(tmpDir+"/vectors.bin", dim, n)
	defer vectorStore.Close()
	for _, v := range vectors {
		vectorStore.Append(v)
	}

	graphStore, _ := storage.CreateGraphStore(tmpDir+"/graph.bin", config.R, n)
	defer graphStore.Close()

	magCache := vamana.NewMagnitudeCache(n)

	avqConfig := AVQConfig{NumPartitions: 64, CodebookSize: 256, AnisotropicWeight: 0.2}
	batchConfig := DefaultBatchBuildConfig()
	builder := NewBatchBuilder(batchConfig, avqConfig, config)

	t.Log("=== Profiling Build Phases ===")

	runtime.GC()
	runtime.ReadMemStats(&m)
	phaseAlloc := m.TotalAlloc

	t.Log("Phase 1: KMeans (TrainFast)")
	start := time.Now()
	builder.partitioner = NewPartitioner(avqConfig.NumPartitions, dim)
	builder.partitioner.TrainFast(vectors, computeKMeansIterations(n, avqConfig.NumPartitions))
	kmeansTime := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("  Time: %v, Memory: %d MB", kmeansTime, (m.TotalAlloc-phaseAlloc)/(1024*1024))
	phaseAlloc = m.TotalAlloc

	t.Log("Phase 2: AVQ Codebooks")
	start = time.Now()
	builder.codebooks = NewPartitionCodebooks(builder.partitioner.Centroids(), avqConfig.AnisotropicWeight)
	builder.trainCodebooks(vectors)
	codebooksTime := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("  Time: %v, Memory: %d MB", codebooksTime, (m.TotalAlloc-phaseAlloc)/(1024*1024))
	phaseAlloc = m.TotalAlloc

	t.Log("Phase 3: Magnitude precompute")
	start = time.Now()
	builder.precomputeMagnitudes(vectors, magCache)
	magsTime := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("  Time: %v, Memory: %d MB", magsTime, (m.TotalAlloc-phaseAlloc)/(1024*1024))
	phaseAlloc = m.TotalAlloc

	t.Log("Phase 4: Graph init (locality-aware)")
	start = time.Now()
	builder.buildGraphLocalityAware(n, vectors, graphStore)
	initTime := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("  Time: %v, Memory: %d MB", initTime, (m.TotalAlloc-phaseAlloc)/(1024*1024))
	phaseAlloc = m.TotalAlloc

	t.Log("Phase 5a: FlashCoder creation")
	start = time.Now()
	flashCoder := builder.NewFlashCoder(vectors, R)
	flashMags := flashCoder.PrecomputeMags()
	flashTime := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("  Time: %v, Memory: %d MB", flashTime, (m.TotalAlloc-phaseAlloc)/(1024*1024))
	t.Logf("  FlashCoder: numSubspaces=%d, numCentroids=%d, subspaceDim=%d",
		flashCoder.numSubspaces, flashCoder.numCentroids, flashCoder.subspaceDim)
	phaseAlloc = m.TotalAlloc

	t.Log("Phase 5b: Single refinement pass (Flash)")
	start = time.Now()
	order := rand.Perm(n)
	updates := builder.refinePassFlash(order, vectors, graphStore, magCache, flashCoder, flashMags, R, config.Alpha, batchConfig.NumWorkers)
	refineFlashTime := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("  Time: %v, Memory: %d MB, Updates: %d", refineFlashTime, (m.TotalAlloc-phaseAlloc)/(1024*1024), updates)
	phaseAlloc = m.TotalAlloc

	t.Log("Phase 5c: Single refinement pass (Standard) for comparison")
	start = time.Now()
	order = rand.Perm(n)
	builder.refinePassParallel(order, vectors, graphStore, magCache, R, config.Alpha, batchConfig.NumWorkers)
	refineStdTime := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("  Time: %v, Memory: %d MB", refineStdTime, (m.TotalAlloc-phaseAlloc)/(1024*1024))
	phaseAlloc = m.TotalAlloc

	t.Log("Phase 6: Medoid computation")
	start = time.Now()
	medoid := vamana.ComputeMedoidFromVectors(vectors)
	medoidTime := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("  Time: %v, Memory: %d MB, Medoid: %d", medoidTime, (m.TotalAlloc-phaseAlloc)/(1024*1024), medoid)

	t.Log("=== Summary ===")
	total := kmeansTime + codebooksTime + magsTime + initTime + flashTime + refineFlashTime + medoidTime
	t.Logf("KMeans:      %v (%.1f%%)", kmeansTime, float64(kmeansTime)/float64(total)*100)
	t.Logf("Codebooks:   %v (%.1f%%)", codebooksTime, float64(codebooksTime)/float64(total)*100)
	t.Logf("Magnitudes:  %v (%.1f%%)", magsTime, float64(magsTime)/float64(total)*100)
	t.Logf("GraphInit:   %v (%.1f%%)", initTime, float64(initTime)/float64(total)*100)
	t.Logf("FlashCoder:  %v (%.1f%%)", flashTime, float64(flashTime)/float64(total)*100)
	t.Logf("RefineFlash: %v (%.1f%%)", refineFlashTime, float64(refineFlashTime)/float64(total)*100)
	t.Logf("Medoid:      %v (%.1f%%)", medoidTime, float64(medoidTime)/float64(total)*100)
	t.Logf("TOTAL:       %v", total)
	t.Logf("")
	t.Logf("Flash vs Standard refinement: %.2fx", float64(refineStdTime)/float64(refineFlashTime))

	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("Peak memory: %d MB", m.TotalAlloc/(1024*1024))
}

func TestProfile_FlashCoderBreakdown(t *testing.T) {
	n := 273000
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
	batchConfig := DefaultBatchBuildConfig()
	avqConfig := DefaultAVQConfig()
	builder := NewBatchBuilder(batchConfig, avqConfig, config)

	numSubspaces, numCentroids := builder.deriveFlashParams(n, dim, R)
	subspaceDim := dim / numSubspaces

	t.Logf("FlashCoder params: n=%d, dim=%d, R=%d", n, dim, R)
	t.Logf("  numSubspaces=%d, numCentroids=%d, subspaceDim=%d", numSubspaces, numCentroids, subspaceDim)

	fc := &FlashCoder{
		numSubspaces: numSubspaces,
		subspaceDim:  subspaceDim,
		numCentroids: numCentroids,
		codebooks:    make([][]float32, numSubspaces),
		codes:        make([][]uint8, n),
	}

	codeStorage := make([]uint8, n*numSubspaces)
	for i := range n {
		fc.codes[i] = codeStorage[i*numSubspaces : (i+1)*numSubspaces : (i+1)*numSubspaces]
	}

	t.Log("=== FlashCoder Breakdown ===")

	start := time.Now()
	fc.trainCodebooks(vectors, batchConfig.NumWorkers)
	trainTime := time.Since(start)
	t.Logf("Codebook training: %v", trainTime)

	start = time.Now()
	fc.encodeVectors(vectors, batchConfig.NumWorkers)
	encodeTime := time.Since(start)
	t.Logf("Vector encoding: %v (%.0f vectors/sec)", encodeTime, float64(n)/encodeTime.Seconds())

	start = time.Now()
	fc.buildSDT()
	sdtTime := time.Since(start)
	t.Logf("SDT build: %v", sdtTime)

	start = time.Now()
	flashMags := fc.PrecomputeMags()
	magsTime := time.Since(start)
	t.Logf("Magnitude precompute: %v", magsTime)
	_ = flashMags

	total := trainTime + encodeTime + sdtTime + magsTime
	t.Logf("TOTAL: %v", total)
	t.Logf("")
	t.Logf("Breakdown:")
	t.Logf("  Training:  %.1f%%", float64(trainTime)/float64(total)*100)
	t.Logf("  Encoding:  %.1f%%", float64(encodeTime)/float64(total)*100)
	t.Logf("  SDT:       %.1f%%", float64(sdtTime)/float64(total)*100)
	t.Logf("  Mags:      %.1f%%", float64(magsTime)/float64(total)*100)

	encodeOps := int64(n) * int64(numSubspaces) * int64(numCentroids)
	t.Logf("")
	t.Logf("Encoding operations: %d (n × subspaces × centroids)", encodeOps)
	t.Logf("Ops per second: %.0f M", float64(encodeOps)/encodeTime.Seconds()/1e6)
}

func TestProfile_RefinementBreakdown(t *testing.T) {
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

	tmpDir := t.TempDir()
	config := vamana.DefaultConfig()

	graphStore, _ := storage.CreateGraphStore(tmpDir+"/graph.bin", config.R, n)
	defer graphStore.Close()

	magCache := vamana.NewMagnitudeCache(n)
	for i, v := range vectors {
		magCache.GetOrCompute(uint32(i), v)
	}

	avqConfig := AVQConfig{NumPartitions: 32, CodebookSize: 256, AnisotropicWeight: 0.2}
	batchConfig := DefaultBatchBuildConfig()
	builder := NewBatchBuilder(batchConfig, avqConfig, config)

	builder.partitioner = NewPartitioner(avqConfig.NumPartitions, dim)
	builder.partitioner.TrainFast(vectors, computeKMeansIterations(n, avqConfig.NumPartitions))
	builder.buildGraphLocalityAware(n, vectors, graphStore)

	t.Logf("=== Refinement Comparison (n=%d) ===", n)

	t.Log("LargeScaleRefiner:")
	refiner := NewLargeScaleRefiner(vectors, magCache, graphStore, R, config.Alpha, batchConfig.NumWorkers)
	start := time.Now()
	updates := refiner.RefineOnce(rand.Perm(n))
	t.Logf("  Time: %v, Updates: %d", time.Since(start), updates)

	builder.buildGraphLocalityAware(n, vectors, graphStore)

	flashCoder := builder.NewFlashCoder(vectors, R)
	flashMags := flashCoder.PrecomputeMags()

	t.Log("FlashCoder RobustPruneFlash:")
	mags := magCache.Slice()
	order := rand.Perm(n)

	seen := make([]bool, n)
	candidateList := make([]uint32, 0, R*R)
	flashBuf := NewFlashPruneBuffers(R * R)

	start = time.Now()
	totalUpdates := 0
	for _, idx := range order {
		nodeID := uint32(idx)
		currentNeighbors := graphStore.GetNeighbors(nodeID)

		candidateList = candidateList[:0]
		for _, neighbor := range currentNeighbors {
			seen[neighbor] = true
			candidateList = append(candidateList, neighbor)
		}
		for _, neighbor := range currentNeighbors {
			for _, nn := range graphStore.GetNeighbors(neighbor) {
				if nn != nodeID && !seen[nn] {
					seen[nn] = true
					candidateList = append(candidateList, nn)
				}
			}
		}
		for _, c := range candidateList {
			seen[c] = false
		}

		if len(candidateList) <= len(currentNeighbors) {
			continue
		}

		newNeighbors := flashCoder.RobustPruneFlash(
			nodeID, candidateList, config.Alpha, R,
			vectors, mags, flashMags, flashBuf,
		)
		if len(newNeighbors) > 0 {
			totalUpdates++
			graphStore.SetNeighbors(nodeID, newNeighbors)
		}
	}
	t.Logf("  Time: %v, Updates: %d", time.Since(start), totalUpdates)
}
