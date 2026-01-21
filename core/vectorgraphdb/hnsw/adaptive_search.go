package hnsw

import "math"

type AdaptiveSearchConfig struct {
	MinCandidates     int
	PatienceThreshold int
	ImprovementRatio  float64
}

func NewAdaptiveSearchConfig(efSearch int) *AdaptiveSearchConfig {
	return &AdaptiveSearchConfig{
		MinCandidates:     max(10, efSearch/10),
		PatienceThreshold: max(3, efSearch/20),
		ImprovementRatio:  0.001,
	}
}

func (cfg *AdaptiveSearchConfig) ShouldTerminate(stableCount, candidateCount int, currentBest, lastBest float64) bool {
	if candidateCount < cfg.MinCandidates {
		return false
	}
	return stableCount >= cfg.PatienceThreshold
}

func (cfg *AdaptiveSearchConfig) ComputeImprovement(currentBest, lastBest float64) float64 {
	if lastBest <= 0 {
		return 1.0
	}
	return (currentBest - lastBest) / math.Max(0.001, lastBest)
}

func (cfg *AdaptiveSearchConfig) IsSignificantImprovement(improvement float64) bool {
	return improvement >= cfg.ImprovementRatio
}
