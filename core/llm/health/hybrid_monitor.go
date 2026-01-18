package health

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// scoreMultiplier converts float64 scores to fixed-point int64.
	scoreMultiplier = 1000

	// defaultActiveWeight is the default weight for active probing.
	defaultActiveWeight = 0.4

	// defaultPassiveWeight is the default weight for passive monitoring.
	defaultPassiveWeight = 0.6

	// scoreUpdateInterval is how often the combined score is updated.
	scoreUpdateInterval = 5 * time.Second
)

// HybridHealthMonitor combines active and passive health monitoring.
type HybridHealthMonitor struct {
	provider string
	config   HealthConfig

	// Active prober
	prober *ActiveProber

	// Passive monitor
	passive *PassiveMonitor

	// Combined health score
	health atomic.Int64 // Fixed-point: score * 1000

	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.RWMutex
}

// NewHybridHealthMonitor creates monitor with prober and passive components.
// Starts at healthy (score 1.0).
// Starts background score updater goroutine.
func NewHybridHealthMonitor(provider string, config HealthConfig, probeFunc ProbeFunc) *HybridHealthMonitor {
	config = applyDefaultWeights(config)

	hm := &HybridHealthMonitor{
		provider: provider,
		config:   config,
		prober:   NewActiveProber(provider, config.ProbeInterval, config.ProbeTimeout, probeFunc),
		passive:  NewPassiveMonitor(config.WindowSize, config.MinSamples),
		stopCh:   make(chan struct{}),
	}

	// Start at healthy (1.0)
	hm.health.Store(int64(1.0 * scoreMultiplier))

	hm.wg.Add(1)
	go hm.runScoreUpdater()

	return hm
}

// applyDefaultWeights ensures config has valid weights.
func applyDefaultWeights(config HealthConfig) HealthConfig {
	if config.ActiveWeight == 0 && config.PassiveWeight == 0 {
		config.ActiveWeight = defaultActiveWeight
		config.PassiveWeight = defaultPassiveWeight
	}
	return config
}

// runScoreUpdater periodically updates combined score (every 5 seconds).
func (hm *HybridHealthMonitor) runScoreUpdater() {
	defer hm.wg.Done()

	ticker := time.NewTicker(scoreUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.stopCh:
			return
		case <-ticker.C:
			hm.updateCombinedScore()
		}
	}
}

// updateCombinedScore calculates: active*ActiveWeight + passive*PassiveWeight.
func (hm *HybridHealthMonitor) updateCombinedScore() {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	activeScore := hm.prober.Score()
	passiveScore := hm.passive.Score()

	combined := activeScore*hm.config.ActiveWeight + passiveScore*hm.config.PassiveWeight
	hm.health.Store(int64(combined * scoreMultiplier))
}

// Score returns current health score (0.0 to 1.0).
func (hm *HybridHealthMonitor) Score() float64 {
	return float64(hm.health.Load()) / scoreMultiplier
}

// Check returns HealthDecision based on thresholds.
func (hm *HybridHealthMonitor) Check() HealthDecision {
	score := hm.Score()
	status := hm.determineStatus(score)
	return hm.buildDecision(score, status)
}

// determineStatus maps score to HealthStatus based on thresholds.
func (hm *HybridHealthMonitor) determineStatus(score float64) HealthStatus {
	if score < hm.config.RejectThreshold {
		return HealthDead
	}
	if score < hm.config.WarnThreshold {
		return HealthDegraded
	}
	if score < hm.config.MonitorThreshold {
		return HealthMonitored
	}
	return HealthHealthy
}

// buildDecision creates a HealthDecision from score and status.
func (hm *HybridHealthMonitor) buildDecision(score float64, status HealthStatus) HealthDecision {
	return HealthDecision{
		Proceed: status != HealthDead,
		Score:   score,
		Status:  status,
		Reason:  hm.formatReason(status),
	}
}

// formatReason returns a human-readable reason for the health status.
func (hm *HybridHealthMonitor) formatReason(status HealthStatus) string {
	return fmt.Sprintf("provider %s is %s", hm.provider, status.String())
}

// RecordSuccess records a successful request (passed to passive monitor).
func (hm *HybridHealthMonitor) RecordSuccess(latency time.Duration) {
	hm.passive.RecordSuccess(latency)
}

// RecordFailure records a failed request (passed to passive monitor).
func (hm *HybridHealthMonitor) RecordFailure(err error) {
	hm.passive.RecordFailure(err)
}

// Stop stops all background goroutines.
func (hm *HybridHealthMonitor) Stop() {
	close(hm.stopCh)
	hm.prober.Stop()
	hm.wg.Wait()
}
