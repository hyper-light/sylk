package health

import (
	"sync"
	"time"
)

// PassiveMonitor infers health from actual request outcomes.
type PassiveMonitor struct {
	windowSize time.Duration
	minSamples int

	// Ring buffer of recent outcomes
	outcomes []outcome
	head     int
	count    int

	mu sync.Mutex
}

// outcome represents a single request result.
type outcome struct {
	timestamp time.Time
	success   bool
	latency   time.Duration
	errorType ErrorCategory
}

// NewPassiveMonitor creates a monitor with buffer size = minSamples * 10.
func NewPassiveMonitor(windowSize time.Duration, minSamples int) *PassiveMonitor {
	bufferSize := minSamples * 10
	return &PassiveMonitor{
		windowSize: windowSize,
		minSamples: minSamples,
		outcomes:   make([]outcome, bufferSize),
		head:       0,
		count:      0,
	}
}

// RecordSuccess records a successful request with latency.
func (pm *PassiveMonitor) RecordSuccess(latency time.Duration) {
	pm.record(outcome{
		timestamp: time.Now(),
		success:   true,
		latency:   latency,
		errorType: ErrNone,
	})
}

// RecordFailure records a failed request, categorizing the error.
func (pm *PassiveMonitor) RecordFailure(err error) {
	pm.record(outcome{
		timestamp: time.Now(),
		success:   false,
		latency:   0,
		errorType: CategorizeError(err),
	})
}

// record adds an outcome to the ring buffer.
func (pm *PassiveMonitor) record(o outcome) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.outcomes[pm.head] = o
	pm.head = (pm.head + 1) % len(pm.outcomes)
	if pm.count < len(pm.outcomes) {
		pm.count++
	}
}

// Score returns passive health score (0-1).
func (pm *PassiveMonitor) Score() float64 {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	stats := pm.collectStats()
	return pm.calculateScore(stats)
}

// outcomeStats holds aggregated outcome statistics.
type outcomeStats struct {
	total     int
	successes int
	highLat   int // >10s
	medLat    int // >5s and <=10s
}

// collectStats gathers statistics from outcomes within the window.
func (pm *PassiveMonitor) collectStats() outcomeStats {
	cutoff := time.Now().Add(-pm.windowSize)
	stats := outcomeStats{}

	for i := 0; i < pm.count; i++ {
		idx := (pm.head - pm.count + i + len(pm.outcomes)) % len(pm.outcomes)
		o := pm.outcomes[idx]

		if o.timestamp.Before(cutoff) {
			continue
		}

		stats.total++
		if o.success {
			stats.successes++
			pm.classifyLatency(&stats, o.latency)
		}
	}

	return stats
}

// classifyLatency updates latency buckets based on the given latency.
func (pm *PassiveMonitor) classifyLatency(stats *outcomeStats, latency time.Duration) {
	const highLatencyThreshold = 10 * time.Second
	const medLatencyThreshold = 5 * time.Second

	if latency > highLatencyThreshold {
		stats.highLat++
	} else if latency > medLatencyThreshold {
		stats.medLat++
	}
}

// calculateScore computes the final score from collected stats.
func (pm *PassiveMonitor) calculateScore(stats outcomeStats) float64 {
	const defaultScore = 0.8

	if stats.total < pm.minSamples {
		return defaultScore
	}

	successRate := float64(stats.successes) / float64(stats.total)
	return pm.applyLatencyPenalty(successRate, stats)
}

// applyLatencyPenalty reduces score based on high latency occurrences.
func (pm *PassiveMonitor) applyLatencyPenalty(score float64, stats outcomeStats) float64 {
	const highLatPenalty = 0.8
	const medLatPenalty = 0.9

	if stats.highLat > 0 {
		return score * highLatPenalty
	}
	if stats.medLat > 0 {
		return score * medLatPenalty
	}
	return score
}
