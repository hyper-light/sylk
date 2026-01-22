package scann

import (
	"math/bits"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

func (b *BatchBuilder) shouldUseFlash(n, R int) bool {
	activeSetCount := n / (R * R)
	activeSetThreshold := bits.Len(uint(R)) * bits.Len(uint(R))
	return activeSetCount > activeSetThreshold
}

func (b *BatchBuilder) BuildWithFlash(
	vectors [][]float32,
	vectorStore *storage.VectorStore,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
) (*BuildResult, error) {
	n := len(vectors)
	if n == 0 {
		return &BuildResult{}, nil
	}

	dim := len(vectors[0])
	numParts := b.avqConfig.NumPartitions
	if numParts > n {
		numParts = n
	}

	b.partitioner = NewPartitioner(numParts, dim)
	if b.shouldUseFlash(n, b.vamanaConf.R) {
		b.partitioner.TrainFast(vectors, computeKMeansIterations(n, numParts))
	} else {
		b.partitioner.Train(vectors, computeKMeansIterations(n, numParts))
	}

	b.codebooks = NewPartitionCodebooks(b.partitioner.Centroids(), b.avqConfig.AnisotropicWeight)
	b.trainCodebooks(vectors)

	b.precomputeMagnitudes(vectors, magCache)
	b.buildGraphLocalityAware(n, vectors, graphStore)

	if b.shouldUseFlash(n, b.vamanaConf.R) {
		b.refineGraphFlash(vectors, graphStore, magCache)
	} else {
		b.refineGraph(vectors, graphStore, magCache)
	}

	medoid := vamana.ComputeMedoidFromVectors(vectors)

	return &BuildResult{
		Partitioner: b.partitioner,
		Codebooks:   b.codebooks,
		Medoid:      medoid,
	}, nil
}

func (b *BatchBuilder) BuildWithFlashAndTimings(
	vectors [][]float32,
	vectorStore *storage.VectorStore,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
) (*BuildResult, *BuildTimings, error) {
	var timings BuildTimings
	n := len(vectors)
	if n == 0 {
		return &BuildResult{}, &timings, nil
	}

	dim := len(vectors[0])
	numParts := b.avqConfig.NumPartitions
	if numParts > n {
		numParts = n
	}

	useFlash := b.shouldUseFlash(n, b.vamanaConf.R)

	start := time.Now()
	b.partitioner = NewPartitioner(numParts, dim)
	if useFlash {
		b.partitioner.TrainFast(vectors, computeKMeansIterations(n, numParts))
	} else {
		b.partitioner.Train(vectors, computeKMeansIterations(n, numParts))
	}
	timings.KMeans = time.Since(start)

	start = time.Now()
	b.codebooks = NewPartitionCodebooks(b.partitioner.Centroids(), b.avqConfig.AnisotropicWeight)
	b.trainCodebooks(vectors)
	timings.Codebooks = time.Since(start)

	start = time.Now()
	b.precomputeMagnitudes(vectors, magCache)
	timings.Magnitudes = time.Since(start)

	start = time.Now()
	b.buildGraphLocalityAware(n, vectors, graphStore)
	timings.GraphInit = time.Since(start)

	start = time.Now()
	if useFlash {
		b.refineGraphFlash(vectors, graphStore, magCache)
	} else {
		b.refineGraph(vectors, graphStore, magCache)
	}
	timings.Refinement = time.Since(start)

	start = time.Now()
	medoid := vamana.ComputeMedoidFromVectors(vectors)
	timings.Medoid = time.Since(start)

	return &BuildResult{
		Partitioner: b.partitioner,
		Codebooks:   b.codebooks,
		Medoid:      medoid,
	}, &timings, nil
}

func (b *BatchBuilder) refineGraphFlash(
	vectors [][]float32,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
) {
	n := len(vectors)
	R := b.vamanaConf.R
	alpha := b.vamanaConf.Alpha
	numWorkers := b.config.NumWorkers

	refiner := NewLargeScaleRefiner(vectors, magCache, graphStore, R, alpha, numWorkers)

	numPasses := computeRefinementPasses(n, R)
	for pass := range numPasses {
		order := rand.Perm(n)
		if pass%2 == 1 {
			for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
				order[i], order[j] = order[j], order[i]
			}
		}

		updates := refiner.RefineOnce(order)

		if updates < n/bits.Len(uint(n)) {
			break
		}

		if b.config.ProgressCallback != nil {
			b.config.ProgressCallback(pass+1, numPasses)
		}
	}
}

func (b *BatchBuilder) refinePassFlash(
	order []int,
	vectors [][]float32,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	flashCoder *FlashCoder,
	flashMags *FlashMags,
	R int,
	alpha float64,
	numWorkers int,
) int {
	n := len(order)
	if numWorkers <= 0 {
		numWorkers = 1
	}
	if numWorkers > n {
		numWorkers = n
	}

	workChunkSize := max(64, n/(numWorkers*8))
	workCh := make(chan []int, (n+workChunkSize-1)/workChunkSize)
	for i := 0; i < n; i += workChunkSize {
		end := min(i+workChunkSize, n)
		workCh <- order[i:end]
	}
	close(workCh)

	updatesCh := make(chan []nodeUpdate, numWorkers*2)
	var wg sync.WaitGroup

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mags := magCache.Slice()
			minImprovement := R / bits.Len(uint(R))
			vecCount := len(vectors)

			seen := make([]bool, vecCount)
			candidateList := make([]uint32, 0, R*R)
			flashBuf := NewFlashPruneBuffers(R*R, R)

			for chunk := range workCh {
				updates := make([]nodeUpdate, 0, len(chunk))
				for _, idx := range chunk {
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

					improvement := len(candidateList) - len(currentNeighbors)
					if improvement < minImprovement {
						continue
					}

					newNeighbors := flashCoder.RobustPruneFlash(
						nodeID, candidateList, alpha, R,
						vectors, mags, flashMags, flashBuf,
					)
					updates = append(updates, nodeUpdate{
						nodeID:       nodeID,
						newNeighbors: newNeighbors,
					})
				}
				if len(updates) > 0 {
					updatesCh <- updates
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(updatesCh)
	}()

	totalUpdates := 0
	for updates := range updatesCh {
		totalUpdates += len(updates)
		for _, u := range updates {
			graphStore.SetNeighbors(u.nodeID, u.newNeighbors)

			for _, neighborID := range u.newNeighbors {
				graphStore.AddNeighbor(neighborID, u.nodeID)
			}
		}
	}
	return totalUpdates
}
