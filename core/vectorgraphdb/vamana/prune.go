package vamana

import (
	"math/bits"
	"math/rand/v2"
	"slices"

	"github.com/viterin/vek/vek32"
)

type DistanceFunc func(a, b uint32) float64

type candidate struct {
	id   uint32
	dist float64
}

type VectorGetter func(id uint32) []float32

type MagnitudeGetter func(id uint32) float64

func RobustPruneBatch(
	pID uint32,
	pVec []float32,
	pMag float64,
	candidates []uint32,
	alpha float64,
	R int,
	getVec VectorGetter,
	getMag MagnitudeGetter,
) []uint32 {
	n := len(candidates)
	if n == 0 {
		return nil
	}
	if n <= R {
		return slices.Clone(candidates)
	}

	dim := len(pVec)

	candidateVecs := make([]float32, n*dim)
	candidateMags := make([]float64, n)
	for i, cid := range candidates {
		vec := getVec(cid)
		copy(candidateVecs[i*dim:(i+1)*dim], vec)
		candidateMags[i] = getMag(cid)
	}

	dots := make([]float32, n)
	BatchDotProducts(pVec, candidateVecs, n, dots)

	scored := make([]candidate, n)
	for i, cid := range candidates {
		mag := candidateMags[i]
		var dist float64
		if pMag == 0 || mag == 0 {
			dist = 2.0
		} else {
			dist = 1.0 - float64(dots[i])/(pMag*mag)
		}
		scored[i] = candidate{id: cid, dist: dist}
	}

	slices.SortFunc(scored, func(a, b candidate) int {
		if a.dist < b.dist {
			return -1
		}
		if a.dist > b.dist {
			return 1
		}
		return 0
	})

	selected := make([]uint32, 0, R)
	selectedVecs := make([][]float32, 0, R)
	selectedMags := make([]float64, 0, R)

	for _, c := range scored {
		if len(selected) >= R {
			break
		}

		cVec := getVec(c.id)
		cMag := getMag(c.id)

		keep := true
		for i := range selected {
			sVec := selectedVecs[i]
			sMag := selectedMags[i]

			var distCS float64
			if cMag == 0 || sMag == 0 {
				distCS = 2.0
			} else {
				dot := DotProduct(cVec, sVec)
				distCS = 1.0 - float64(dot)/(cMag*sMag)
			}

			if c.dist > alpha*distCS {
				keep = false
				break
			}
		}

		if keep {
			selected = append(selected, c.id)
			selectedVecs = append(selectedVecs, cVec)
			selectedMags = append(selectedMags, cMag)
		}
	}

	return selected
}

func RobustPrune(p uint32, candidates []uint32, alpha float64, R int, distFn DistanceFunc) []uint32 {
	if len(candidates) == 0 {
		return nil
	}

	if len(candidates) <= R {
		return slices.Clone(candidates)
	}

	maxExamine := min(R*2, len(candidates))
	preserveCount := min(R, len(candidates))
	ratio := max(1, len(candidates)/maxExamine)
	totalSamples := maxExamine * bits.Len(uint(ratio))
	secondHopSamples := min(len(candidates)-preserveCount, totalSamples-preserveCount)
	maxScore := preserveCount + max(0, secondHopSamples)

	toScore := candidates
	if len(candidates) > maxScore && secondHopSamples > 0 {
		toScore = make([]uint32, maxScore)
		copy(toScore, candidates[:preserveCount])
		copy(toScore[preserveCount:], candidates[preserveCount:maxScore])
		for i, c := range candidates[maxScore:] {
			j := rand.IntN(secondHopSamples + i + 1)
			if j < secondHopSamples {
				toScore[preserveCount+j] = c
			}
		}
	}

	scored := make([]candidate, len(toScore))
	for i, c := range toScore {
		scored[i] = candidate{id: c, dist: distFn(p, c)}
	}

	slices.SortFunc(scored, func(a, b candidate) int {
		if a.dist < b.dist {
			return -1
		}
		if a.dist > b.dist {
			return 1
		}
		return 0
	})

	examineCount := min(maxExamine, len(scored))

	selected := make([]uint32, 0, R)
	consecutiveRejections := 0

	for i := range examineCount {
		if len(selected) >= R || consecutiveRejections >= R {
			break
		}

		c := scored[i]
		keep := true
		for _, s := range selected {
			distCS := distFn(c.id, s)
			if c.dist > alpha*distCS {
				keep = false
				break
			}
		}

		if keep {
			selected = append(selected, c.id)
			consecutiveRejections = 0
		} else {
			consecutiveRejections++
		}
	}

	return selected
}

type PruneBuffers struct {
	ToScore  []uint32
	Scored   []candidate
	Selected []uint32
}

func NewPruneBuffers(maxCandidates, R int) *PruneBuffers {
	return &PruneBuffers{
		ToScore:  make([]uint32, maxCandidates),
		Scored:   make([]candidate, maxCandidates),
		Selected: make([]uint32, 0, R),
	}
}

func RobustPruneDirect(
	p uint32,
	candidates []uint32,
	alpha float64,
	R int,
	vectors [][]float32,
	mags []float64,
	buf *PruneBuffers,
) []uint32 {
	if len(candidates) == 0 {
		return nil
	}

	if len(candidates) <= R {
		return slices.Clone(candidates)
	}

	maxExamine := min(R*2, len(candidates))
	preserveCount := min(R, len(candidates))
	ratio := max(1, len(candidates)/maxExamine)
	totalSamples := maxExamine * bits.Len(uint(ratio))
	secondHopSamples := min(len(candidates)-preserveCount, totalSamples-preserveCount)
	maxScore := preserveCount + max(0, secondHopSamples)

	toScore := candidates
	if len(candidates) > maxScore && secondHopSamples > 0 {
		toScore = buf.ToScore[:maxScore]
		copy(toScore, candidates[:preserveCount])
		copy(toScore[preserveCount:], candidates[preserveCount:maxScore])
		for i, c := range candidates[maxScore:] {
			j := rand.IntN(secondHopSamples + i + 1)
			if j < secondHopSamples {
				toScore[preserveCount+j] = c
			}
		}
	}

	pVec := vectors[p]
	pMag := mags[p]

	scored := buf.Scored[:len(toScore)]
	for i, c := range toScore {
		cVec := vectors[c]
		cMag := mags[c]
		var dist float64
		if pMag == 0 || cMag == 0 {
			dist = 2.0
		} else {
			dot := vek32.Dot(pVec, cVec)
			dist = 1.0 - float64(dot)/(pMag*cMag)
		}
		scored[i] = candidate{id: c, dist: dist}
	}

	slices.SortFunc(scored, func(a, b candidate) int {
		if a.dist < b.dist {
			return -1
		}
		if a.dist > b.dist {
			return 1
		}
		return 0
	})

	examineCount := min(maxExamine, len(scored))

	selected := buf.Selected[:0]
	consecutiveRejections := 0

	for i := range examineCount {
		if len(selected) >= R || consecutiveRejections >= R {
			break
		}

		c := scored[i]
		cVec := vectors[c.id]
		cMag := mags[c.id]

		keep := true
		for _, s := range selected {
			sVec := vectors[s]
			sMag := mags[s]
			var distCS float64
			if cMag == 0 || sMag == 0 {
				distCS = 2.0
			} else {
				dot := vek32.Dot(cVec, sVec)
				distCS = 1.0 - float64(dot)/(cMag*sMag)
			}
			if c.dist > alpha*distCS {
				keep = false
				break
			}
		}

		if keep {
			selected = append(selected, c.id)
			consecutiveRejections = 0
		} else {
			consecutiveRejections++
		}
	}

	return slices.Clone(selected)
}

func RobustPruneWithScores(p uint32, scored []candidate, alpha float64, R int, distFn DistanceFunc) []uint32 {
	if len(scored) == 0 {
		return nil
	}

	if len(scored) <= R {
		result := make([]uint32, len(scored))
		for i, c := range scored {
			result[i] = c.id
		}
		return result
	}

	slices.SortFunc(scored, func(a, b candidate) int {
		if a.dist < b.dist {
			return -1
		}
		if a.dist > b.dist {
			return 1
		}
		return 0
	})

	selected := make([]uint32, 0, R)

	for _, c := range scored {
		if len(selected) >= R {
			break
		}

		keep := true
		for _, s := range selected {
			distCS := distFn(c.id, s)
			if c.dist > alpha*distCS {
				keep = false
				break
			}
		}

		if keep {
			selected = append(selected, c.id)
		}
	}

	return selected
}
