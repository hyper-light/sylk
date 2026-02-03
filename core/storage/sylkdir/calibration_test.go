package sylkdir

import (
	"math"
	"testing"
	"time"
)

func TestCalibration_QuickProbe(t *testing.T) {
	dir := t.TempDir()

	result, err := QuickProbe(CalibrationConfig{Dir: dir})
	if err != nil {
		t.Fatalf("QuickProbe: %v", err)
	}

	if result.FsyncLatency <= 0 {
		t.Errorf("FsyncLatency = %v, want > 0", result.FsyncLatency)
	}
	if result.ProbeCount != 1 {
		t.Errorf("ProbeCount = %d, want 1", result.ProbeCount)
	}
	if result.ProbedAt.IsZero() {
		t.Error("ProbedAt should be set")
	}
}

func TestCalibration_FullProbe(t *testing.T) {
	dir := t.TempDir()

	result, err := FullProbe(CalibrationConfig{Dir: dir})
	if err != nil {
		t.Fatalf("FullProbe: %v", err)
	}

	if result.FsyncLatency <= 0 {
		t.Errorf("FsyncLatency = %v, want > 0", result.FsyncLatency)
	}
	if result.SeqReadSpeed <= 0 {
		t.Errorf("SeqReadSpeed = %v, want > 0", result.SeqReadSpeed)
	}
	if result.RandWriteSpeed <= 0 {
		t.Errorf("RandWriteSpeed = %v, want > 0", result.RandWriteSpeed)
	}
	if result.ProbeCount != 2 {
		t.Errorf("ProbeCount = %d, want 2", result.ProbeCount)
	}
}

func TestCalibration_PersistLoad(t *testing.T) {
	dir := t.TempDir()

	original := &CalibrationResult{
		FsyncLatency:     500 * time.Microsecond,
		ReplayThroughput: 1200.5,
		SeqReadSpeed:     2500.0,
		RandWriteSpeed:   100.0,
		ProbeCount:       3,
		ProbedAt:         time.Now().UTC().Truncate(time.Millisecond),
	}

	if err := SaveCalibration(dir, original); err != nil {
		t.Fatalf("SaveCalibration: %v", err)
	}

	loaded, err := LoadCalibration(dir)
	if err != nil {
		t.Fatalf("LoadCalibration: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded is nil")
	}

	if loaded.FsyncLatency != original.FsyncLatency {
		t.Errorf("FsyncLatency = %v, want %v", loaded.FsyncLatency, original.FsyncLatency)
	}
	if loaded.ProbeCount != original.ProbeCount {
		t.Errorf("ProbeCount = %d, want %d", loaded.ProbeCount, original.ProbeCount)
	}
}

func TestCalibration_LoadMissing(t *testing.T) {
	dir := t.TempDir()

	result, err := LoadCalibration(dir)
	if err != nil {
		t.Fatalf("LoadCalibration missing: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for missing file")
	}
}

func TestCalibration_SmoothEMA(t *testing.T) {
	existing := &CalibrationResult{
		FsyncLatency: 1000 * time.Microsecond,
		SeqReadSpeed: 100.0,
		ProbeCount:   3,
	}
	fresh := &CalibrationResult{
		FsyncLatency: 500 * time.Microsecond,
		SeqReadSpeed: 200.0,
		ProbeCount:   1,
	}

	smoothed := SmoothCalibration(existing, fresh)

	// alpha = 2/(3+1+1) = 0.4
	// FsyncLatency: 1000*0.6 + 500*0.4 = 800us
	expectedLatency := time.Duration(float64(1000*time.Microsecond)*0.6 + float64(500*time.Microsecond)*0.4)
	if diff := absDurationDiff(smoothed.FsyncLatency, expectedLatency); diff > time.Microsecond {
		t.Errorf("FsyncLatency = %v, want ~%v (diff %v)", smoothed.FsyncLatency, expectedLatency, diff)
	}

	// SeqReadSpeed: 100*0.6 + 200*0.4 = 140
	expectedSeq := 100.0*0.6 + 200.0*0.4
	if math.Abs(smoothed.SeqReadSpeed-expectedSeq) > 0.01 {
		t.Errorf("SeqReadSpeed = %f, want %f", smoothed.SeqReadSpeed, expectedSeq)
	}

	if smoothed.ProbeCount != 4 {
		t.Errorf("ProbeCount = %d, want 4", smoothed.ProbeCount)
	}
}

func TestCalibration_SmoothNilExisting(t *testing.T) {
	fresh := &CalibrationResult{
		FsyncLatency: 500 * time.Microsecond,
		ProbeCount:   1,
	}

	result := SmoothCalibration(nil, fresh)
	if result != fresh {
		t.Error("expected fresh to be returned when existing is nil")
	}
}

func absDurationDiff(a, b time.Duration) time.Duration {
	if a > b {
		return a - b
	}
	return b - a
}
