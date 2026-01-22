package scann

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

type GraphDifficulty struct {
	AvgExpansion    float64 // nodes visited / L at baseline search
	AvgPathLength   float64 // hops from medoid to reach targets
	ConvergenceRate float64 // how quickly search stops improving
}

// ProbeGraphDifficulty measures search difficulty by running sample queries.
// Returns metrics that characterize how hard it is to search this graph.
// Cost: ~5-10ms for 10 probes on 23K vector graph.
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
		return GraphDifficulty{AvgExpansion: 1.0, AvgPathLength: 1.0, ConvergenceRate: 1.0}
	}

	numProbes := min(10, n)
	probeIndices := sampleRandomIndices(n, numProbes)

	var totalExpansion, totalPathLen, totalConvRate float64
	validProbes := 0

	for _, idx := range probeIndices {
		queryVec := vectors[idx]
		visited, pathLen, convRate := probeSearch(queryVec, medoid, R, snapshot, graphStore, magCache)

		if visited > 0 {
			totalExpansion += float64(visited) / float64(R)
			totalPathLen += float64(pathLen)
			totalConvRate += convRate
			validProbes++
		}
	}

	if validProbes == 0 {
		return GraphDifficulty{AvgExpansion: 1.0, AvgPathLength: 1.0, ConvergenceRate: 1.0}
	}

	return GraphDifficulty{
		AvgExpansion:    totalExpansion / float64(validProbes),
		AvgPathLength:   totalPathLen / float64(validProbes),
		ConvergenceRate: totalConvRate / float64(validProbes),
	}
}

func probeSearch(
	query []float32,
	start uint32,
	L int,
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

	for iter := 0; iter < L*2; iter++ {
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
			if stableCount >= 3 {
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

// DeriveL computes L from measured graph difficulty and target recall.
// Formula: L = R * (baseMultiplier + difficultyAdjustment) * recallScaling
func DeriveL(R int, targetRecall float64, difficulty GraphDifficulty) int {
	if targetRecall <= 0.80 {
		return R
	}
	if targetRecall >= 1.0 {
		targetRecall = 0.995
	}

	baseMultiplier := 1.0
	difficultyAdjust := (difficulty.AvgExpansion - 1.0) * 0.5
	if difficultyAdjust < 0 {
		difficultyAdjust = 0
	}

	recallFactor := 1.0 / (1.0 - targetRecall)
	recallScale := math.Log2(recallFactor) / 2.0

	multiplier := (baseMultiplier + difficultyAdjust) * (1.0 + recallScale)

	L := int(math.Ceil(float64(R) * multiplier))
	return max(R, min(L, R*10))
}

// DeriveL98 returns L for 98%+ recall using measured difficulty.
func DeriveL98(R int, difficulty GraphDifficulty) int {
	return DeriveL(R, 0.98, difficulty)
}

// DeriveL98Fast returns L for 98%+ using default difficulty estimate.
func DeriveL98Fast(R int) int {
	return DeriveL(R, 0.98, GraphDifficulty{AvgExpansion: 1.5, AvgPathLength: 10, ConvergenceRate: 1.0})
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

	a := &AdaptiveL{
		targetRecall: targetRecall,
		R:            R,
		difficulty:   difficulty,
		observations: make([]recallObservation, 0, 32),
		windowSize:   16,
		minL:         R,
		maxL:         R * 10,
		adjustStep:   max(1, R/4),
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

	if len(a.observations) >= 4 {
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

	if count < 3 {
		return
	}

	avgRecall := sumRecall / float64(count)
	gap := a.targetRecall - avgRecall

	if gap > 0.02 {
		newL := min(a.maxL, currentL+a.adjustStep)
		a.currentL.Store(int64(newL))
	} else if gap < -0.05 {
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

// QuickCalibrate measures graph difficulty and derives L, then validates with minimal samples.
func QuickCalibrate(
	targetRecall float64,
	vectors [][]float32,
	vectorStore *storage.VectorStore,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	medoid uint32,
	R int,
) int {
	n := len(vectors)
	if n == 0 {
		return DeriveL98Fast(R)
	}

	snapshot := vectorStore.Snapshot()
	if snapshot == nil {
		return DeriveL98Fast(R)
	}

	difficulty := ProbeGraphDifficulty(vectors, snapshot, graphStore, magCache, medoid, R)
	derivedL := DeriveL(R, targetRecall, difficulty)

	numSamples := min(5, n)
	sampleIndices := sampleRandomIndices(n, numSamples)
	groundTruth := computeGroundTruth(vectors, sampleIndices, 10, magCache)

	testMultipliers := []float64{0.8, 1.0, 1.25, 1.5}
	for _, mult := range testMultipliers {
		testL := int(float64(derivedL) * mult)
		if testL < R {
			testL = R
		}
		recall := measureRecallAtL(snapshot, graphStore, magCache, vectors, sampleIndices, groundTruth, medoid, testL, 10)
		if recall >= targetRecall {
			return testL
		}
	}

	return int(float64(derivedL) * 1.5)
}
