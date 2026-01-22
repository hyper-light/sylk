package scann

import (
	"math/bits"
	"sync"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
	"github.com/viterin/vek/vek32"
)

type LargeScaleRefiner struct {
	vectors    [][]float32
	mags       []float64
	graphStore *storage.GraphStore
	R          int
	alpha      float64
	numWorkers int
}

func NewLargeScaleRefiner(
	vectors [][]float32,
	magCache *vamana.MagnitudeCache,
	graphStore *storage.GraphStore,
	R int,
	alpha float64,
	numWorkers int,
) *LargeScaleRefiner {
	return &LargeScaleRefiner{
		vectors:    vectors,
		mags:       magCache.Slice(),
		graphStore: graphStore,
		R:          R,
		alpha:      alpha,
		numWorkers: numWorkers,
	}
}

func (r *LargeScaleRefiner) RefineOnce(order []int) int {
	n := len(order)
	numWorkers := r.numWorkers
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

	type nodeUpdate struct {
		nodeID       uint32
		newNeighbors []uint32
	}

	updatesCh := make(chan []nodeUpdate, numWorkers*2)
	var wg sync.WaitGroup

	logR := bits.Len(uint(r.R))
	logR2 := logR * logR
	maxCandidates := r.R + logR2 + logR2
	minImprovement := r.R / logR

	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()

			seen := make([]bool, len(r.vectors))
			candidateList := make([]uint32, 0, r.R*r.R)
			scoreBuf := make([]scoredCandidate, 0, maxCandidates)

			for chunk := range workCh {
				updates := make([]nodeUpdate, 0, len(chunk)/4)

				for _, idx := range chunk {
					nodeID := uint32(idx)
					currentNeighbors := r.graphStore.GetNeighbors(nodeID)

					candidateList = candidateList[:0]
					for _, neighbor := range currentNeighbors {
						seen[neighbor] = true
						candidateList = append(candidateList, neighbor)
					}

					candidateCap := maxCandidates + maxCandidates
					for _, neighbor := range currentNeighbors {
						if len(candidateList) >= candidateCap {
							break
						}
						for _, nn := range r.graphStore.GetNeighbors(neighbor) {
							if nn != nodeID && !seen[nn] {
								seen[nn] = true
								candidateList = append(candidateList, nn)
								if len(candidateList) >= candidateCap {
									break
								}
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

					newNeighbors := r.fastPrune(nodeID, candidateList, scoreBuf, maxCandidates)
					if newNeighbors != nil {
						updates = append(updates, nodeUpdate{nodeID, newNeighbors})
					}
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
			r.graphStore.SetNeighbors(u.nodeID, u.newNeighbors)
			for _, neighborID := range u.newNeighbors {
				r.graphStore.AddNeighbor(neighborID, u.nodeID)
			}
		}
	}

	return totalUpdates
}

type scoredCandidate struct {
	id   uint32
	dist float64
}

func (r *LargeScaleRefiner) fastPrune(p uint32, candidates []uint32, scoreBuf []scoredCandidate, maxScore int) []uint32 {
	if len(candidates) <= r.R {
		result := make([]uint32, len(candidates))
		copy(result, candidates)
		return result
	}

	pVec := r.vectors[p]
	pMag := r.mags[p]
	if pMag == 0 {
		return nil
	}

	toScore := candidates
	if len(candidates) > maxScore {
		toScore = candidates[:maxScore]
	}

	scoreBuf = scoreBuf[:0]
	for _, c := range toScore {
		cVec := r.vectors[c]
		cMag := r.mags[c]
		var dist float64
		if cMag == 0 {
			dist = 2.0
		} else {
			dot := vek32.Dot(pVec, cVec)
			dist = 1.0 - float64(dot)/(pMag*cMag)
		}
		scoreBuf = append(scoreBuf, scoredCandidate{c, dist})
	}

	for i := 1; i < len(scoreBuf); i++ {
		j := i
		for j > 0 && scoreBuf[j].dist < scoreBuf[j-1].dist {
			scoreBuf[j], scoreBuf[j-1] = scoreBuf[j-1], scoreBuf[j]
			j--
		}
	}

	takeCount := min(r.R, len(scoreBuf))
	selected := make([]uint32, takeCount)
	for i := range takeCount {
		selected[i] = scoreBuf[i].id
	}
	return selected
}
