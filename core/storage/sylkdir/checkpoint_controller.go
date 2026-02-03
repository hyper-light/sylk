package sylkdir

import (
	"sync/atomic"
	"time"
)

// CheckpointGranularity describes the scope of a checkpoint operation.
type CheckpointGranularity int

const (
	// GranularityNone means no checkpoint needed.
	GranularityNone CheckpointGranularity = iota
	// GranularityLight means fsync only (minimal I/O).
	GranularityLight
	// GranularityStandard means seal + compact + truncate + patch bump.
	GranularityStandard
	// GranularityFullMerge means standard + rewrite for locality.
	GranularityFullMerge
)

// granularityNames maps granularity to string for diagnostics.
var granularityNames = [4]string{
	GranularityNone:      "None",
	GranularityLight:     "Light",
	GranularityStandard:  "Standard",
	GranularityFullMerge: "FullMerge",
}

// String returns the name of the checkpoint granularity.
func (g CheckpointGranularity) String() string {
	if int(g) < len(granularityNames) {
		return granularityNames[g]
	}
	return "Unknown"
}

// CheckpointDecision is the result of evaluating the trigger inequality.
type CheckpointDecision struct {
	ShouldCheckpoint bool
	Granularity      CheckpointGranularity
	WALDebt          float64 // D_wal: normalized WAL size
	Contention       float64 // c(t): blocked/total writers ratio
	MemPressure      float64 // mu(t): combined memory pressure
	Threshold        float64 // tau(c): contention-adjusted threshold
}

// CheckpointControllerConfig configures a CheckpointController.
type CheckpointControllerConfig struct {
	Calibration *CalibrationResult
	Delta       *DeltaTracker
	WALBytes    func() int64 // Returns current WAL size in bytes
}

// CheckpointController evaluates the adaptive checkpoint trigger inequality:
//
//	D_wal / (1 - c(t)) > tau(c) / (1 + mu(t))
//
// Where D_wal is normalized WAL debt, c(t) is writer contention ratio,
// tau(c) is the calibrated threshold, and mu(t) is memory pressure.
type CheckpointController struct {
	calibration    *CalibrationResult
	delta          *DeltaTracker
	walBytes       func() int64
	blockedWriters atomic.Int64
	totalWriters   atomic.Int64
	checkpointing  atomic.Bool
}

// NewCheckpointController creates a new checkpoint controller.
func NewCheckpointController(cfg CheckpointControllerConfig) *CheckpointController {
	return &CheckpointController{
		calibration: cfg.Calibration,
		delta:       cfg.Delta,
		walBytes:    cfg.WALBytes,
	}
}

// ShouldCheckpoint evaluates the trigger inequality and returns a decision.
func (cc *CheckpointController) ShouldCheckpoint() CheckpointDecision {
	if cc.checkpointing.Load() {
		return CheckpointDecision{}
	}

	walDebt := cc.computeWALDebt()
	contention := cc.computeContention()
	memPressure := MeasureMemoryPressure().Combined
	threshold := cc.computeThreshold()

	decision := CheckpointDecision{
		WALDebt:     walDebt,
		Contention:  contention,
		MemPressure: memPressure,
		Threshold:   threshold,
	}

	if !shouldTrigger(walDebt, contention, threshold, memPressure) {
		return decision
	}

	decision.ShouldCheckpoint = true
	decision.Granularity = cc.selectGranularity(walDebt)
	return decision
}

// shouldTrigger evaluates: D_wal / (1 - c(t)) > tau(c) / (1 + mu(t)).
func shouldTrigger(walDebt, contention, threshold, memPressure float64) bool {
	lhs := walDebt / (1 - clamp01(contention, 0.99))
	rhs := threshold / (1 + memPressure)
	return lhs > rhs
}

// clamp01 clamps v to [0, max] to prevent division by zero.
func clamp01(v, max float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > max:
		return max
	default:
		return v
	}
}

// computeWALDebt returns the current WAL size normalized by fsync latency.
// Higher values mean more data at risk.
func (cc *CheckpointController) computeWALDebt() float64 {
	walSize := float64(cc.walBytes())
	if cc.calibration == nil || cc.calibration.FsyncLatency == 0 {
		return walSize / float64(probeBlockSize) // Normalize by page size as fallback
	}
	fsyncNs := float64(cc.calibration.FsyncLatency)
	return walSize / fsyncNs
}

// computeContention returns the ratio of blocked to total writers.
func (cc *CheckpointController) computeContention() float64 {
	total := cc.totalWriters.Load()
	if total == 0 {
		return 0
	}
	blocked := cc.blockedWriters.Load()
	return float64(blocked) / float64(total)
}

// computeThreshold returns the base checkpoint threshold from calibration.
// Derived from fsync latency: higher latency → lower threshold (trigger sooner).
func (cc *CheckpointController) computeThreshold() float64 {
	if cc.calibration == nil || cc.calibration.FsyncLatency == 0 {
		return 1.0 // Default threshold without calibration
	}
	// Threshold is inverse of fsync latency in milliseconds.
	// Fast disk (100us fsync) → threshold ~10. Slow disk (10ms) → threshold ~0.1.
	latencyMs := float64(cc.calibration.FsyncLatency) / float64(time.Millisecond)
	if latencyMs < 0.001 {
		return 1000
	}
	return 1.0 / latencyMs
}

// selectGranularity chooses the checkpoint scope based on WAL debt level.
func (cc *CheckpointController) selectGranularity(walDebt float64) CheckpointGranularity {
	threshold := cc.computeThreshold()
	writeAmp := cc.computeWriteAmplification()

	switch {
	case walDebt < threshold:
		return GranularityLight
	case writeAmp > 1 && cc.calibration != nil && cc.calibration.SeqReadSpeed > 0:
		return GranularityFullMerge
	default:
		return GranularityStandard
	}
}

// computeWriteAmplification estimates write amplification from calibration.
func (cc *CheckpointController) computeWriteAmplification() float64 {
	if cc.calibration == nil || cc.calibration.RandWriteSpeed == 0 {
		return 0
	}
	return cc.calibration.SeqReadSpeed / cc.calibration.RandWriteSpeed
}

// --- Writer tracking ---

// RecordWriterBlocked increments the blocked writer count.
func (cc *CheckpointController) RecordWriterBlocked() {
	cc.blockedWriters.Add(1)
}

// RecordWriterUnblocked decrements the blocked writer count.
func (cc *CheckpointController) RecordWriterUnblocked() {
	cc.blockedWriters.Add(-1)
}

// RecordWriterTotal increments the total writer count.
func (cc *CheckpointController) RecordWriterTotal() {
	cc.totalWriters.Add(1)
}

// SetCheckpointing marks whether a checkpoint is in progress.
func (cc *CheckpointController) SetCheckpointing(v bool) {
	cc.checkpointing.Store(v)
}

// IsCheckpointing returns true if a checkpoint is currently in progress.
func (cc *CheckpointController) IsCheckpointing() bool {
	return cc.checkpointing.Load()
}
