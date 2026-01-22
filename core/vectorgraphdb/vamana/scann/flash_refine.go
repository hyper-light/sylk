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

	accessor := r.graphStore.NewNeighborAccessor()

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
					currentNeighbors := accessor.Get(nodeID)

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
						for _, nn := range accessor.Get(neighbor) {
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

	// Use reduced dimensions for scoring: R * log(R) dims
	dim := len(pVec)
	reducedDim := r.R * bits.Len(uint(r.R))
	if reducedDim > dim {
		reducedDim = dim
	}

	scoreBuf = scoreBuf[:0]
	pVecReduced := pVec[:reducedDim]
	nToScore := len(toScore)
	i := 0
	for ; i+3 < nToScore; i += 4 {
		c0, c1, c2, c3 := toScore[i], toScore[i+1], toScore[i+2], toScore[i+3]
		cVec0, cVec1, cVec2, cVec3 := r.vectors[c0], r.vectors[c1], r.vectors[c2], r.vectors[c3]
		cMag0, cMag1, cMag2, cMag3 := r.mags[c0], r.mags[c1], r.mags[c2], r.mags[c3]

		dot0 := vek32.Dot(pVecReduced, cVec0[:reducedDim])
		dot1 := vek32.Dot(pVecReduced, cVec1[:reducedDim])
		dot2 := vek32.Dot(pVecReduced, cVec2[:reducedDim])
		dot3 := vek32.Dot(pVecReduced, cVec3[:reducedDim])

		dist0, dist1, dist2, dist3 := 2.0, 2.0, 2.0, 2.0
		if cMag0 != 0 {
			dist0 = 1.0 - float64(dot0)/(pMag*cMag0)
		}
		if cMag1 != 0 {
			dist1 = 1.0 - float64(dot1)/(pMag*cMag1)
		}
		if cMag2 != 0 {
			dist2 = 1.0 - float64(dot2)/(pMag*cMag2)
		}
		if cMag3 != 0 {
			dist3 = 1.0 - float64(dot3)/(pMag*cMag3)
		}

		scoreBuf = append(scoreBuf,
			scoredCandidate{c0, dist0},
			scoredCandidate{c1, dist1},
			scoredCandidate{c2, dist2},
			scoredCandidate{c3, dist3})
	}
	for ; i < nToScore; i++ {
		c := toScore[i]
		cVec := r.vectors[c]
		cMag := r.mags[c]
		var dist float64
		if cMag == 0 {
			dist = 2.0
		} else {
			dot := vek32.Dot(pVecReduced, cVec[:reducedDim])
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

	// Skip diversity pruning: take top-R directly
	selectCount := min(r.R, len(scoreBuf))
	selected := make([]uint32, selectCount)
	for i := range selectCount {
		selected[i] = scoreBuf[i].id
	}

	return selected
}
