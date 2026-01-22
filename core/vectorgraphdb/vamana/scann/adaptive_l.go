package scann

import (
	"math"
	"math/bits"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

type GraphDifficulty struct {
	AvgExpansion    float64
	AvgPathLength   float64
	ConvergenceRate float64
	ProbeCount      int
}

func ProbeGraphDifficulty(
	vectors [][]float32,
	snapshot *storage.VectorSnapshot,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	medoid uint32,
	R int,
) GraphDifficulty {
	n := len(vectors)
	if n == 0 || snapshot == nil {
		return defaultDifficulty(R)
	}

	numProbes := bits.Len(uint(n))
	probeIndices := sampleRandomIndices(n, numProbes)

	var totalExpansion, totalPathLen, totalConvRate float64
	validProbes := 0

	maxIter := R + bits.Len(uint(n))

	for _, idx := range probeIndices {
		queryVec := vectors[idx]
		visited, pathLen, convRate := probeSearch(queryVec, medoid, maxIter, snapshot, graphStore, magCache)

		if visited > 0 {
			totalExpansion += float64(visited) / float64(R)
			totalPathLen += float64(pathLen)
			totalConvRate += convRate
			validProbes++
		}
	}

	if validProbes == 0 {
		return defaultDifficulty(R)
	}

	return GraphDifficulty{
		AvgExpansion:    totalExpansion / float64(validProbes),
		AvgPathLength:   totalPathLen / float64(validProbes),
		ConvergenceRate: totalConvRate / float64(validProbes),
		ProbeCount:      validProbes,
	}
}

func defaultDifficulty(R int) GraphDifficulty {
	return GraphDifficulty{
		AvgExpansion:    float64(bits.Len(uint(R))) / float64(R),
		AvgPathLength:   float64(bits.Len(uint(R))),
		ConvergenceRate: 1.0,
		ProbeCount:      0,
	}
}

func probeSearch(
	query []float32,
	start uint32,
	maxIter int,
	snapshot *storage.VectorSnapshot,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
) (visited int, pathLength int, convergenceRate float64) {
	queryMag := vamana.Magnitude(query)
	if queryMag == 0 {
		return 0, 0, 0
	}

	seen := make(map[uint32]bool)
	current := start
	var bestDist float64 = 2.0
	stableCount := 0
	stableThreshold := bits.Len(uint(maxIter))

	for iter := 0; iter < maxIter; iter++ {
		if seen[current] {
			break
		}
		seen[current] = true
		pathLength++

		vec := snapshot.Get(current)
		if vec == nil {
			break
		}
		mag := magCache.GetOrCompute(current, vec)
		if mag == 0 {
			break
		}

		dot := vamana.DotProduct(query, vec)
		dist := 1.0 - float64(dot)/(queryMag*mag)

		if dist < bestDist {
			bestDist = dist
			stableCount = 0
		} else {
			stableCount++
			if stableCount >= stableThreshold {
				break
			}
		}

		neighbors := graphStore.GetNeighbors(current)
		if len(neighbors) == 0 {
			break
		}

		bestNeighbor := current
		bestNeighborDist := dist

		for _, n := range neighbors {
			if seen[n] {
				continue
			}
			nVec := snapshot.Get(n)
			if nVec == nil {
				continue
			}
			nMag := magCache.GetOrCompute(n, nVec)
			if nMag == 0 {
				continue
			}
			nDot := vamana.DotProduct(query, nVec)
			nDist := 1.0 - float64(nDot)/(queryMag*nMag)
			if nDist < bestNeighborDist {
				bestNeighborDist = nDist
				bestNeighbor = n
			}
		}

		if bestNeighbor == current {
			break
		}
		current = bestNeighbor
	}

	visited = len(seen)
	if pathLength > 0 {
		convergenceRate = float64(visited) / float64(pathLength)
	}
	return
}

func DeriveL(R int, targetRecall float64, difficulty GraphDifficulty) int {
	targetRecall = max(0.0, min(targetRecall, 1.0-1.0/float64(R*R)))

	combinedDifficulty := difficulty.AvgPathLength * (1.0 + difficulty.AvgExpansion)
	recallBits := -math.Log2(1.0 - targetRecall)

	logR := bits.Len(uint(R)) - 1
	if logR < 1 {
		logR = 1
	}

	difficultyScale := 1.0 + math.Log2(combinedDifficulty+1)/float64(bits.Len(uint(R)))
	L := float64(R) * combinedDifficulty * recallBits * difficultyScale / float64(logR)

	maxL := R * bits.Len(uint(R)) * bits.Len(uint(R))
	return max(R, min(int(math.Ceil(L)), maxL))
}

func DeriveL98(R int, difficulty GraphDifficulty) int {
	return DeriveL(R, 0.98, difficulty)
}

type AdaptiveL struct {
	targetRecall float64
	R            int
	difficulty   GraphDifficulty
	currentL     atomic.Int64

	mu           sync.Mutex
	observations []recallObservation
	windowSize   int

	minL       int
	maxL       int
	adjustStep int
}

type recallObservation struct {
	L      int
	recall float64
}

func NewAdaptiveL(R int, targetRecall float64, difficulty GraphDifficulty) *AdaptiveL {
	initialL := DeriveL(R, targetRecall, difficulty)

	windowSize := bits.Len(uint(R)) * bits.Len(uint(R))
	maxL := R * bits.Len(uint(R)) * bits.Len(uint(R))
	adjustStep := max(1, R/bits.Len(uint(R)))

	a := &AdaptiveL{
		targetRecall: targetRecall,
		R:            R,
		difficulty:   difficulty,
		observations: make([]recallObservation, 0, windowSize),
		windowSize:   windowSize,
		minL:         R,
		maxL:         maxL,
		adjustStep:   adjustStep,
	}
	a.currentL.Store(int64(initialL))

	return a
}

func (a *AdaptiveL) L() int {
	return int(a.currentL.Load())
}

func (a *AdaptiveL) RecordObservation(L int, recall float64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.observations = append(a.observations, recallObservation{L: L, recall: recall})

	if len(a.observations) > a.windowSize {
		a.observations = a.observations[1:]
	}

	minObservations := bits.Len(uint(a.R))
	if len(a.observations) >= minObservations {
		a.adjust()
	}
}

func (a *AdaptiveL) adjust() {
	currentL := int(a.currentL.Load())

	var sumRecall float64
	var count int
	for _, obs := range a.observations {
		if obs.L == currentL {
			sumRecall += obs.recall
			count++
		}
	}

	minCount := bits.Len(uint(a.R)) / 2
	if minCount < 2 {
		minCount = 2
	}
	if count < minCount {
		return
	}

	avgRecall := sumRecall / float64(count)
	gap := a.targetRecall - avgRecall

	gapThresholdUp := 1.0 / float64(a.R)
	gapThresholdDown := gapThresholdUp * float64(bits.Len(uint(a.R)))

	if gap > gapThresholdUp {
		newL := min(a.maxL, currentL+a.adjustStep)
		a.currentL.Store(int64(newL))
	} else if gap < -gapThresholdDown {
		newL := max(a.minL, currentL-a.adjustStep)
		a.currentL.Store(int64(newL))
	}
}

func (a *AdaptiveL) Stats() (currentL int, avgRecall float64, observations int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	currentL = int(a.currentL.Load())
	observations = len(a.observations)

	if observations == 0 {
		return currentL, 0, 0
	}

	var sum float64
	for _, obs := range a.observations {
		sum += obs.recall
	}
	avgRecall = sum / float64(observations)

	return
}
