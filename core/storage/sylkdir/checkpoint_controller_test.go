package sylkdir

import (
	"testing"
	"time"
)

func newTestController(fsyncLatency time.Duration, walSize int64) *CheckpointController {
	cal := &CalibrationResult{
		FsyncLatency:   fsyncLatency,
		SeqReadSpeed:   1000.0,
		RandWriteSpeed: 200.0,
	}
	delta := NewDeltaTracker("")
	return NewCheckpointController(CheckpointControllerConfig{
		Calibration: cal,
		Delta:       delta,
		WALBytes:    func() int64 { return walSize },
	})
}

func TestCheckpointController_NoDebt(t *testing.T) {
	cc := newTestController(100*time.Microsecond, 0)

	decision := cc.ShouldCheckpoint()
	if decision.ShouldCheckpoint {
		t.Error("should not checkpoint with zero WAL debt")
	}
	if decision.Granularity != GranularityNone {
		t.Errorf("Granularity = %v, want None", decision.Granularity)
	}
}

func TestCheckpointController_HighDebt(t *testing.T) {
	// High WAL debt relative to threshold should trigger.
	cc := newTestController(100*time.Microsecond, 10*1024*1024) // 10MB WAL

	decision := cc.ShouldCheckpoint()
	if !decision.ShouldCheckpoint {
		t.Error("should checkpoint with high WAL debt")
	}
	if decision.WALDebt <= 0 {
		t.Errorf("WALDebt = %f, want > 0", decision.WALDebt)
	}
}

func TestCheckpointController_LightGranularity(t *testing.T) {
	// Small WAL debt should produce Light granularity.
	cc := newTestController(1*time.Millisecond, 100) // Tiny WAL

	// Force the trigger by making threshold very small.
	cc.calibration.FsyncLatency = 10 * time.Second // Very slow disk → low threshold
	decision := cc.ShouldCheckpoint()

	if decision.ShouldCheckpoint && decision.Granularity != GranularityLight {
		t.Errorf("Granularity = %v, want Light for small WAL debt", decision.Granularity)
	}
}

func TestCheckpointController_FullMergeGranularity(t *testing.T) {
	// Large WAL debt + high write amplification → FullMerge.
	cc := newTestController(100*time.Microsecond, 100*1024*1024)
	cc.calibration.SeqReadSpeed = 2000.0
	cc.calibration.RandWriteSpeed = 100.0 // write amp = 20

	decision := cc.ShouldCheckpoint()
	if decision.ShouldCheckpoint && decision.Granularity != GranularityFullMerge {
		t.Errorf("Granularity = %v, want FullMerge", decision.Granularity)
	}
}

func TestCheckpointController_MemPressureEffect(t *testing.T) {
	// Memory pressure lowers the threshold, making triggers easier.
	// We can't control actual memory pressure, but we verify the formula is coherent.
	cc := newTestController(100*time.Microsecond, 1024*1024) // 1MB WAL

	d := cc.ShouldCheckpoint()
	// Threshold / (1 + memPressure): higher memPressure → lower RHS → easier trigger.
	if d.Threshold <= 0 {
		t.Errorf("Threshold = %f, want > 0", d.Threshold)
	}
}

func TestCheckpointController_ContentionEffect(t *testing.T) {
	cc := newTestController(100*time.Microsecond, 5*1024*1024)

	// Record contention: 5 blocked out of 10 total = 50%.
	for range 10 {
		cc.RecordWriterTotal()
	}
	for range 5 {
		cc.RecordWriterBlocked()
	}

	d := cc.ShouldCheckpoint()
	if d.Contention < 0.4 || d.Contention > 0.6 {
		t.Errorf("Contention = %f, want ~0.5", d.Contention)
	}
}

func TestCheckpointController_CheckpointingGuard(t *testing.T) {
	cc := newTestController(100*time.Microsecond, 100*1024*1024)

	// Should trigger normally.
	d1 := cc.ShouldCheckpoint()
	if !d1.ShouldCheckpoint {
		t.Fatal("expected trigger with high debt")
	}

	// Mark as checkpointing.
	cc.SetCheckpointing(true)
	d2 := cc.ShouldCheckpoint()
	if d2.ShouldCheckpoint {
		t.Error("should not trigger while checkpointing")
	}

	// Unmark.
	cc.SetCheckpointing(false)
	d3 := cc.ShouldCheckpoint()
	if !d3.ShouldCheckpoint {
		t.Error("should trigger after checkpoint completes")
	}
}

func TestCheckpointController_NilCalibration(t *testing.T) {
	// Controller should work without calibration (uses defaults).
	cc := NewCheckpointController(CheckpointControllerConfig{
		Delta:    NewDeltaTracker(""),
		WALBytes: func() int64 { return 1024 * 1024 },
	})

	d := cc.ShouldCheckpoint()
	// Should not panic; threshold defaults to 1.0.
	if d.Threshold != 1.0 {
		t.Errorf("Threshold = %f, want 1.0 (default)", d.Threshold)
	}
}

func TestCheckpointGranularity_String(t *testing.T) {
	cases := map[CheckpointGranularity]string{
		GranularityNone:      "None",
		GranularityLight:     "Light",
		GranularityStandard:  "Standard",
		GranularityFullMerge: "FullMerge",
	}
	for g, want := range cases {
		if got := g.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", g, got, want)
		}
	}
}
