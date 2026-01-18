package recovery

import "time"

type HealthWeights struct {
	HeartbeatWeight  float64
	ProgressWeight   float64
	RepetitionWeight float64
	ResourceWeight   float64
}

type HealthThresholds struct {
	HeartbeatStaleAfter time.Duration
	ProgressWindowSize  int
	RepetitionMinCycles int
	HealthyThreshold    float64
	WarningThreshold    float64
	StuckThreshold      float64
	CriticalThreshold   float64
}

func DefaultHealthWeights() HealthWeights {
	return HealthWeights{
		HeartbeatWeight:  0.35,
		ProgressWeight:   0.30,
		RepetitionWeight: 0.20,
		ResourceWeight:   0.15,
	}
}

func DefaultHealthThresholds() HealthThresholds {
	return HealthThresholds{
		HeartbeatStaleAfter: 30 * time.Second,
		ProgressWindowSize:  20,
		RepetitionMinCycles: 3,
		HealthyThreshold:    0.7,
		WarningThreshold:    0.4,
		StuckThreshold:      0.2,
		CriticalThreshold:   0.2,
	}
}

func (w HealthWeights) Validate() bool {
	sum := w.HeartbeatWeight + w.ProgressWeight + w.RepetitionWeight + w.ResourceWeight
	return sum > 0.99 && sum < 1.01
}

func (t HealthThresholds) Validate() bool {
	return t.hasValidDurations() && t.hasValidThresholdOrder()
}

func (t HealthThresholds) hasValidDurations() bool {
	return t.HeartbeatStaleAfter > 0 &&
		t.ProgressWindowSize > 0 &&
		t.RepetitionMinCycles > 0
}

func (t HealthThresholds) hasValidThresholdOrder() bool {
	return t.HealthyThreshold > t.WarningThreshold &&
		t.WarningThreshold >= t.StuckThreshold
}
