package activation

import "sync/atomic"

// ActivationMetrics tracks activation controller statistics via atomic
// counters. All fields are safe for concurrent reads and writes.
type ActivationMetrics struct {
	ActivationsTotal    atomic.Int64 // Total EnsureActive calls
	ColdStarts          atomic.Int64 // Activations from TierCold
	CoolStarts          atomic.Int64 // Activations from TierCool
	WarmStarts          atomic.Int64 // Activations from TierWarm
	HotHits             atomic.Int64 // Already-hot fast-path returns
	CoalescedRequests   atomic.Int64 // Requests that waited on inflight activation
	DemotionsToWarm     atomic.Int64
	DemotionsToCool     atomic.Int64
	DemotionsToCold     atomic.Int64
	PredictorHits       atomic.Int64 // Pre-warm triggered by predictor
	PredictorMisses     atomic.Int64 // Pre-warmed but never used before demotion
	EvictionsByPressure atomic.Int64
}

// Snapshot returns a copy of the current metric values.
type MetricsSnapshot struct {
	ActivationsTotal    int64
	ColdStarts          int64
	CoolStarts          int64
	WarmStarts          int64
	HotHits             int64
	CoalescedRequests   int64
	DemotionsToWarm     int64
	DemotionsToCool     int64
	DemotionsToCold     int64
	PredictorHits       int64
	PredictorMisses     int64
	EvictionsByPressure int64
}

// Snapshot returns a point-in-time copy of all counters.
func (m *ActivationMetrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		ActivationsTotal:    m.ActivationsTotal.Load(),
		ColdStarts:          m.ColdStarts.Load(),
		CoolStarts:          m.CoolStarts.Load(),
		WarmStarts:          m.WarmStarts.Load(),
		HotHits:             m.HotHits.Load(),
		CoalescedRequests:   m.CoalescedRequests.Load(),
		DemotionsToWarm:     m.DemotionsToWarm.Load(),
		DemotionsToCool:     m.DemotionsToCool.Load(),
		DemotionsToCold:     m.DemotionsToCold.Load(),
		PredictorHits:       m.PredictorHits.Load(),
		PredictorMisses:     m.PredictorMisses.Load(),
		EvictionsByPressure: m.EvictionsByPressure.Load(),
	}
}
