package scann

import (
	"math/rand/v2"

	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana"
	"github.com/adalundhe/sylk/core/vectorgraphdb/vamana/storage"
)

type CalibrationConfig struct {
	TargetRecall      float64 // minimum recall threshold (e.g., 0.95)
	ConfidenceLevel   float64 // posterior confidence required to stop (e.g., 0.95)
	NumSamplesPerStep int     // queries per L measurement for noise reduction
	K                 int     // neighbors per query
	MaxL              int     // upper bound for L search
	MaxIterations     int     // safety bound on iterations
}

func DefaultCalibrationConfig() CalibrationConfig {
	return CalibrationConfig{
		TargetRecall:      0.95,
		ConfidenceLevel:   0.95,
		NumSamplesPerStep: 20,
		K:                 10,
		MaxL:              500,
		MaxIterations:     30,
	}
}

type CalibrationResult struct {
	L          int
	Recall     float64
	Confidence float64
	Iterations int
	Converged  bool
	LowerBound int
	UpperBound int
}

// CalibrateL finds minimum L achieving target recall using Probabilistic Bisection.
// Exploits monotonicity: recall(L) is non-decreasing in L.
// Stops when posterior probability mass concentrates with sufficient confidence.
func CalibrateL(
	cfg CalibrationConfig,
	vectors [][]float32,
	vectorStore *storage.VectorStore,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	medoid uint32,
	R int,
) CalibrationResult {
	n := len(vectors)
	if n == 0 || cfg.NumSamplesPerStep <= 0 || cfg.K <= 0 {
		return CalibrationResult{L: R, Converged: false}
	}

	snapshot := vectorStore.Snapshot()
	if snapshot == nil {
		return CalibrationResult{L: R, Converged: false}
	}

	pba := newProbabilisticBisection(R, cfg.MaxL, cfg.TargetRecall, cfg.ConfidenceLevel)

	samplePool := sampleRandomIndices(n, min(200, n))
	groundTruth := computeGroundTruth(vectors, samplePool, cfg.K, magCache)

	var iterations int

	for iterations < cfg.MaxIterations {
		iterations++

		queryL := pba.nextQuery()
		if queryL < 0 {
			break
		}

		sampleIndices := selectSubset(samplePool, cfg.NumSamplesPerStep)
		sampleGT := selectGroundTruth(groundTruth, samplePool, sampleIndices)

		recall := measureRecallAtL(snapshot, graphStore, magCache, vectors, sampleIndices, sampleGT, medoid, queryL, cfg.K)

		success := recall >= cfg.TargetRecall
		pba.update(queryL, success, recall)

		if pba.hasConverged() {
			break
		}
	}

	lower, upper := pba.credibleInterval()
	selectedL := pba.selectedL()

	finalRecall := measureRecallAtL(snapshot, graphStore, magCache, vectors, samplePool, groundTruth, medoid, selectedL, cfg.K)

	return CalibrationResult{
		L:          selectedL,
		Recall:     finalRecall,
		Confidence: pba.confidence(),
		Iterations: iterations,
		Converged:  pba.hasConverged(),
		LowerBound: lower,
		UpperBound: upper,
	}
}

type probabilisticBisection struct {
	minL, maxL      int
	stepSize        int
	numBuckets      int
	posterior       []float64
	targetRecall    float64
	confidenceLevel float64
	observations    map[int]observation
}

type observation struct {
	successes int
	failures  int
	avgRecall float64
}

func newProbabilisticBisection(minL, maxL int, targetRecall, confidenceLevel float64) *probabilisticBisection {
	stepSize := max(1, (maxL-minL)/32)
	numBuckets := (maxL-minL)/stepSize + 1

	posterior := make([]float64, numBuckets)
	uniformProb := 1.0 / float64(numBuckets)
	for i := range posterior {
		posterior[i] = uniformProb
	}

	return &probabilisticBisection{
		minL:            minL,
		maxL:            maxL,
		stepSize:        stepSize,
		numBuckets:      numBuckets,
		posterior:       posterior,
		targetRecall:    targetRecall,
		confidenceLevel: confidenceLevel,
		observations:    make(map[int]observation),
	}
}

func (pba *probabilisticBisection) lToIndex(L int) int {
	idx := (L - pba.minL) / pba.stepSize
	return max(0, min(idx, pba.numBuckets-1))
}

func (pba *probabilisticBisection) indexToL(idx int) int {
	return pba.minL + idx*pba.stepSize
}

func (pba *probabilisticBisection) nextQuery() int {
	cumulative := 0.0
	for i, p := range pba.posterior {
		cumulative += p
		if cumulative >= 0.5 {
			return pba.indexToL(i)
		}
	}
	return pba.indexToL(pba.numBuckets / 2)
}

func (pba *probabilisticBisection) update(L int, success bool, recall float64) {
	idx := pba.lToIndex(L)

	obs := pba.observations[idx]
	if success {
		obs.successes++
	} else {
		obs.failures++
	}
	total := float64(obs.successes + obs.failures)
	obs.avgRecall = (obs.avgRecall*(total-1) + recall) / total
	pba.observations[idx] = obs

	pSuccess := 0.9
	pFail := 0.1
	if !success {
		pSuccess, pFail = pFail, pSuccess
	}

	for i := range pba.posterior {
		var likelihood float64
		if i <= idx {
			likelihood = pSuccess
		} else {
			likelihood = pFail
		}
		pba.posterior[i] *= likelihood
	}

	pba.normalize()
}

func (pba *probabilisticBisection) normalize() {
	sum := 0.0
	for _, p := range pba.posterior {
		sum += p
	}
	if sum > 0 {
		for i := range pba.posterior {
			pba.posterior[i] /= sum
		}
	}
}

func (pba *probabilisticBisection) credibleInterval() (lower, upper int) {
	alpha := (1.0 - pba.confidenceLevel) / 2.0

	cumulative := 0.0
	lower = pba.minL
	for i, p := range pba.posterior {
		cumulative += p
		if cumulative >= alpha {
			lower = pba.indexToL(i)
			break
		}
	}

	cumulative = 0.0
	upper = pba.maxL
	for i := pba.numBuckets - 1; i >= 0; i-- {
		cumulative += pba.posterior[i]
		if cumulative >= alpha {
			upper = pba.indexToL(i)
			break
		}
	}

	return lower, upper
}

func (pba *probabilisticBisection) hasConverged() bool {
	lower, upper := pba.credibleInterval()
	intervalWidth := upper - lower
	return intervalWidth <= pba.stepSize*2
}

func (pba *probabilisticBisection) confidence() float64 {
	lower, upper := pba.credibleInterval()
	mass := 0.0
	lowerIdx := pba.lToIndex(lower)
	upperIdx := pba.lToIndex(upper)
	for i := lowerIdx; i <= upperIdx; i++ {
		if i >= 0 && i < pba.numBuckets {
			mass += pba.posterior[i]
		}
	}
	return mass
}

func (pba *probabilisticBisection) selectedL() int {
	maxProb := 0.0
	maxIdx := 0
	for i, p := range pba.posterior {
		if p > maxProb {
			maxProb = p
			maxIdx = i
		}
	}

	selectedIdx := maxIdx
	for i := 0; i <= maxIdx; i++ {
		if obs, ok := pba.observations[i]; ok {
			if obs.successes > 0 && obs.avgRecall >= pba.targetRecall {
				selectedIdx = i
				break
			}
		}
	}

	return pba.indexToL(selectedIdx)
}

func sampleRandomIndices(n, numSamples int) []int {
	if numSamples >= n {
		indices := make([]int, n)
		for i := range n {
			indices[i] = i
		}
		return indices
	}
	perm := rand.Perm(n)
	return perm[:numSamples]
}

func selectSubset(pool []int, size int) []int {
	if size >= len(pool) {
		return pool
	}
	perm := rand.Perm(len(pool))
	subset := make([]int, size)
	for i := range size {
		subset[i] = pool[perm[i]]
	}
	return subset
}

func selectGroundTruth(fullGT [][]uint32, fullPool, subset []int) [][]uint32 {
	poolIndex := make(map[int]int, len(fullPool))
	for i, idx := range fullPool {
		poolIndex[idx] = i
	}

	result := make([][]uint32, len(subset))
	for i, idx := range subset {
		if gtIdx, ok := poolIndex[idx]; ok {
			result[i] = fullGT[gtIdx]
		}
	}
	return result
}

func computeGroundTruth(
	vectors [][]float32,
	sampleIndices []int,
	K int,
	magCache *vamana.MagnitudeCache,
) [][]uint32 {
	n := len(vectors)
	groundTruth := make([][]uint32, len(sampleIndices))

	for i, queryIdx := range sampleIndices {
		queryVec := vectors[queryIdx]
		queryMag := magCache.GetOrCompute(uint32(queryIdx), queryVec)
		if queryMag == 0 {
			groundTruth[i] = nil
			continue
		}

		type distPair struct {
			id   uint32
			dist float64
		}

		candidates := make([]distPair, 0, n)
		for j := range n {
			vec := vectors[j]
			mag := magCache.GetOrCompute(uint32(j), vec)
			if mag == 0 {
				continue
			}
			dot := vamana.DotProduct(queryVec, vec)
			sim := float64(dot) / (queryMag * mag)
			candidates = append(candidates, distPair{id: uint32(j), dist: -sim})
		}

		for k := range min(K, len(candidates)) {
			minIdx := k
			for j := k + 1; j < len(candidates); j++ {
				if candidates[j].dist < candidates[minIdx].dist {
					minIdx = j
				}
			}
			candidates[k], candidates[minIdx] = candidates[minIdx], candidates[k]
		}

		topK := min(K, len(candidates))
		groundTruth[i] = make([]uint32, topK)
		for k := range topK {
			groundTruth[i][k] = candidates[k].id
		}
	}

	return groundTruth
}

func measureRecallAtL(
	snapshot *storage.VectorSnapshot,
	graphStore *storage.GraphStore,
	magCache *vamana.MagnitudeCache,
	vectors [][]float32,
	sampleIndices []int,
	groundTruth [][]uint32,
	medoid uint32,
	L, K int,
) float64 {
	if len(sampleIndices) == 0 {
		return 0
	}

	var totalRecall float64
	var validSamples int

	for i, queryIdx := range sampleIndices {
		if i >= len(groundTruth) || len(groundTruth[i]) == 0 {
			continue
		}

		queryVec := vectors[queryIdx]
		results := vamana.GreedySearchFast(queryVec, medoid, L, K, snapshot, graphStore, magCache)

		resultSet := make(map[uint32]struct{}, len(results))
		for _, r := range results {
			resultSet[uint32(r.InternalID)] = struct{}{}
		}

		gt := groundTruth[i]
		var hits int
		for _, gtID := range gt {
			if _, found := resultSet[gtID]; found {
				hits++
			}
		}

		totalRecall += float64(hits) / float64(len(gt))
		validSamples++
	}

	if validSamples == 0 {
		return 0
	}
	return totalRecall / float64(validSamples)
}
