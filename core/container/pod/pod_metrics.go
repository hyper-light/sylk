package pod

import "sync/atomic"

// PodMetrics tracks activation and guard statistics for a managed pod.
// All fields use atomics for lock-free concurrent access.
type PodMetrics struct {
	PromotionsTotal   atomic.Int64
	DemotionsToWarm   atomic.Int64
	DemotionsToCool   atomic.Int64
	DemotionsToCold   atomic.Int64
	HotHits           atomic.Int64
	ColdStarts        atomic.Int64
	WarmStarts        atomic.Int64
	CoolStarts        atomic.Int64
	GuardAcquisitions atomic.Int64
	GuardReleases     atomic.Int64
}

// Snapshot returns a copy of all metric values at a point in time.
func (m *PodMetrics) Snapshot() PodMetricsSnapshot {
	return PodMetricsSnapshot{
		PromotionsTotal:   m.PromotionsTotal.Load(),
		DemotionsToWarm:   m.DemotionsToWarm.Load(),
		DemotionsToCool:   m.DemotionsToCool.Load(),
		DemotionsToCold:   m.DemotionsToCold.Load(),
		HotHits:           m.HotHits.Load(),
		ColdStarts:        m.ColdStarts.Load(),
		WarmStarts:        m.WarmStarts.Load(),
		CoolStarts:        m.CoolStarts.Load(),
		GuardAcquisitions: m.GuardAcquisitions.Load(),
		GuardReleases:     m.GuardReleases.Load(),
	}
}

// PodMetricsSnapshot is a non-atomic copy of PodMetrics for reporting.
type PodMetricsSnapshot struct {
	PromotionsTotal   int64
	DemotionsToWarm   int64
	DemotionsToCool   int64
	DemotionsToCold   int64
	HotHits           int64
	ColdStarts        int64
	WarmStarts        int64
	CoolStarts        int64
	GuardAcquisitions int64
	GuardReleases     int64
}
