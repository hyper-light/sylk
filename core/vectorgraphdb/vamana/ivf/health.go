package ivf

import (
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"

	"github.com/viterin/vek/vek32"
)

type HealthTracker struct {
	mu sync.RWMutex

	selfRecallEMA   float64
	edgeQualityEMA  float64
	partitionCohEMA float64
	queryCount      atomic.Int64
	sampleInterval  int
	decayFactor     float64
	initialized     bool
	sampling        atomic.Bool

	idx *Index
}

func newHealthTracker(idx *Index) *HealthTracker {
	sqrtN := int(math.Sqrt(float64(idx.numVectors)))
	interval := max(sqrtN, 1)

	return &HealthTracker{
		idx:            idx,
		sampleInterval: interval,
		decayFactor:    1.0 / float64(interval),
	}
}

func (h *HealthTracker) RecordQuery() {
	count := h.queryCount.Add(1)

	if int(count)%h.sampleInterval != 0 {
		return
	}

	go h.sampleHealth()
}

func (h *HealthTracker) sampleHealth() {
	if !h.sampling.CompareAndSwap(false, true) {
		return
	}
	defer h.sampling.Store(false)

	h.mu.Lock()
	defer h.mu.Unlock()

	selfRecall := h.measureSelfRecall()
	edgeQuality := h.measureEdgeQuality()
	partitionCoh := h.measurePartitionCohesion()

	if !h.initialized {
		h.selfRecallEMA = selfRecall
		h.edgeQualityEMA = edgeQuality
		h.partitionCohEMA = partitionCoh
		h.initialized = true
		return
	}

	alpha := h.decayFactor
	h.selfRecallEMA = alpha*selfRecall + (1-alpha)*h.selfRecallEMA
	h.edgeQualityEMA = alpha*edgeQuality + (1-alpha)*h.edgeQualityEMA
	h.partitionCohEMA = alpha*partitionCoh + (1-alpha)*h.partitionCohEMA
}

func (h *HealthTracker) measureSelfRecall() float64 {
	idx := h.idx
	if idx.numVectors == 0 {
		return 1.0
	}

	sampleSize := int(math.Sqrt(float64(idx.numVectors)))
	sampleSize = max(min(sampleSize, idx.numVectors), 1)

	hits := 0
	for range sampleSize {
		queryIdx := rand.IntN(idx.numVectors)
		queryVec := idx.getVector(uint32(queryIdx))
		if queryVec == nil {
			continue
		}

		results := idx.SearchVamana(queryVec, 1)
		if len(results) > 0 && results[0].ID == uint32(queryIdx) {
			hits++
		}
	}

	return float64(hits) / float64(sampleSize)
}

func (h *HealthTracker) measureEdgeQuality() float64 {
	idx := h.idx
	if idx.graph == nil || len(idx.graph.adjacency) == 0 {
		return 1.0
	}

	sampleSize := int(math.Sqrt(float64(len(idx.graph.adjacency))))
	sampleSize = max(min(sampleSize, len(idx.graph.adjacency)), 1)

	totalSim := 0.0
	validEdges := 0

	for range sampleSize {
		nodeID := uint32(rand.IntN(len(idx.graph.adjacency)))
		nodeVec := idx.getVector(nodeID)
		nodeNorm := idx.vectorNorms[nodeID]
		if nodeVec == nil || nodeNorm == 0 {
			continue
		}

		neighbors := idx.graph.adjacency[nodeID]
		for _, neighborID := range neighbors {
			neighborVec := idx.getVector(neighborID)
			neighborNorm := idx.vectorNorms[neighborID]
			if neighborVec == nil || neighborNorm == 0 {
				continue
			}

			sim := float64(vek32.Dot(nodeVec, neighborVec)) / (nodeNorm * neighborNorm)
			totalSim += sim
			validEdges++
		}
	}

	if validEdges == 0 {
		return 1.0
	}

	return totalSim / float64(validEdges)
}

func (h *HealthTracker) measurePartitionCohesion() float64 {
	idx := h.idx
	if len(idx.partitionIDs) == 0 || len(idx.centroids) == 0 {
		return 1.0
	}

	sampleSize := int(math.Sqrt(float64(idx.numVectors)))
	sampleSize = max(min(sampleSize, idx.numVectors), 1)

	totalSim := 0.0
	validSamples := 0

	for range sampleSize {
		vecIdx := rand.IntN(idx.numVectors)
		vec := idx.getVector(uint32(vecIdx))
		vecNorm := idx.vectorNorms[vecIdx]
		if vec == nil || vecNorm == 0 {
			continue
		}

		partition := idx.findNearestCentroid(vec)
		centroid := idx.centroids[partition]
		centroidNorm := idx.centroidNorms[partition]
		if centroidNorm == 0 {
			continue
		}

		sim := float64(vek32.Dot(vec, centroid)) / (vecNorm * centroidNorm)
		totalSim += sim
		validSamples++
	}

	if validSamples == 0 {
		return 1.0
	}

	return totalSim / float64(validSamples)
}

type HealthStatus struct {
	SelfRecall        float64
	EdgeQuality       float64
	PartitionCohesion float64

	SelfRecallTrend        float64
	EdgeQualityTrend       float64
	PartitionCohesionTrend float64
}

func (h *HealthTracker) Status() HealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	currentSelfRecall := h.measureSelfRecall()
	currentEdgeQuality := h.measureEdgeQuality()
	currentPartitionCoh := h.measurePartitionCohesion()

	return HealthStatus{
		SelfRecall:        currentSelfRecall,
		EdgeQuality:       currentEdgeQuality,
		PartitionCohesion: currentPartitionCoh,

		SelfRecallTrend:        currentSelfRecall - h.selfRecallEMA,
		EdgeQualityTrend:       currentEdgeQuality - h.edgeQualityEMA,
		PartitionCohesionTrend: currentPartitionCoh - h.partitionCohEMA,
	}
}

func (h *HealthTracker) NeedsMaintenance() (needsCentroidRefresh, needsGraphOptimize bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.initialized {
		return false, false
	}

	currentSelfRecall := h.measureSelfRecall()
	currentEdgeQuality := h.measureEdgeQuality()
	currentPartitionCoh := h.measurePartitionCohesion()

	recallDrop := h.selfRecallEMA - currentSelfRecall
	edgeDrop := h.edgeQualityEMA - currentEdgeQuality
	cohesionDrop := h.partitionCohEMA - currentPartitionCoh

	emaVariance := h.decayFactor

	needsCentroidRefresh = cohesionDrop > emaVariance && recallDrop > emaVariance
	needsGraphOptimize = edgeDrop > emaVariance && recallDrop > emaVariance

	return needsCentroidRefresh, needsGraphOptimize
}
