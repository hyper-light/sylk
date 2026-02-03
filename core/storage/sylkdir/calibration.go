package sylkdir

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Probe sizes derived from page alignment.
const (
	probeBlockSize = 4096   // 4 KiB — single page write
	probeSeqSize   = 262144 // 256 KiB — sequential read block
)

// CalibrationResult holds measured I/O performance characteristics.
type CalibrationResult struct {
	FsyncLatency     time.Duration `json:"fsync_latency_ns"`
	ReplayThroughput float64       `json:"replay_throughput_mbs"` // MB/s for sequential read
	SeqReadSpeed     float64       `json:"seq_read_speed_mbs"`
	RandWriteSpeed   float64       `json:"rand_write_speed_mbs"`
	ProbeCount       uint32        `json:"probe_count"`
	ProbedAt         time.Time     `json:"probed_at"`
}

// CalibrationConfig configures a calibration probe.
type CalibrationConfig struct {
	Dir string // Directory to run probes in (temp files are created here)
}

// QuickProbe measures fsync latency with 5 write+sync cycles, using the median.
// Budget: <100ms on modern SSDs.
func QuickProbe(cfg CalibrationConfig) (*CalibrationResult, error) {
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, fmt.Errorf("calibration: create dir: %w", err)
	}

	latency, err := measureFsyncLatency(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("calibration: fsync probe: %w", err)
	}

	return &CalibrationResult{
		FsyncLatency: latency,
		ProbeCount:   1,
		ProbedAt:     time.Now().UTC(),
	}, nil
}

// FullProbe runs a quick probe plus sequential read and random write measurements.
func FullProbe(cfg CalibrationConfig) (*CalibrationResult, error) {
	result, err := QuickProbe(cfg)
	if err != nil {
		return nil, err
	}

	seqRead, err := measureSeqRead(cfg.Dir)
	if err != nil {
		return result, fmt.Errorf("calibration: seq read probe: %w", err)
	}

	randWrite, err := measureRandWrite(cfg.Dir)
	if err != nil {
		return result, fmt.Errorf("calibration: rand write probe: %w", err)
	}

	result.SeqReadSpeed = seqRead
	result.RandWriteSpeed = randWrite
	result.ReplayThroughput = seqRead // Replay throughput approximated by seq read
	result.ProbeCount = 2
	return result, nil
}

// measureFsyncLatency performs 5 write+fsync cycles and returns the median latency.
func measureFsyncLatency(dir string) (time.Duration, error) {
	probePath := filepath.Join(dir, "calibration.probe")
	defer os.Remove(probePath)

	const probeRounds = 5
	latencies := make([]time.Duration, probeRounds)
	buf := make([]byte, probeBlockSize)

	for i := range probeRounds {
		d, err := singleFsyncProbe(probePath, buf)
		if err != nil {
			return 0, err
		}
		latencies[i] = d
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	return latencies[probeRounds/2], nil // Median
}

// singleFsyncProbe writes probeBlockSize bytes, fsyncs, and returns the elapsed time.
func singleFsyncProbe(path string, buf []byte) (time.Duration, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	if _, err := f.Write(buf); err != nil {
		f.Close()
		return 0, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return 0, err
	}
	elapsed := time.Since(start)

	return elapsed, f.Close()
}

// measureSeqRead writes probeSeqSize bytes then reads them back, returning MB/s.
func measureSeqRead(dir string) (float64, error) {
	probePath := filepath.Join(dir, "calibration.seqread")
	defer os.Remove(probePath)

	buf := make([]byte, probeSeqSize)
	if err := os.WriteFile(probePath, buf, 0644); err != nil {
		return 0, err
	}

	start := time.Now()
	if _, err := os.ReadFile(probePath); err != nil {
		return 0, err
	}
	elapsed := time.Since(start)

	return bytesToMBPerSec(probeSeqSize, elapsed), nil
}

// measureRandWrite performs 10 random 4KB writes with fsync, returning MB/s.
func measureRandWrite(dir string) (float64, error) {
	probePath := filepath.Join(dir, "calibration.randwrite")
	defer os.Remove(probePath)

	const writeRounds = 10
	buf := make([]byte, probeBlockSize)
	totalBytes := probeBlockSize * writeRounds

	start := time.Now()
	for range writeRounds {
		d, err := singleFsyncProbe(probePath, buf)
		if err != nil {
			return 0, err
		}
		_ = d // We measure total time, not individual probes
	}
	elapsed := time.Since(start)

	return bytesToMBPerSec(totalBytes, elapsed), nil
}

// bytesToMBPerSec converts bytes and duration to MB/s.
func bytesToMBPerSec(bytes int, elapsed time.Duration) float64 {
	if elapsed == 0 {
		return 0
	}
	return float64(bytes) / (1024 * 1024) / elapsed.Seconds()
}

// --- Persistence ---

// SaveCalibration writes a CalibrationResult to disk as JSON.
func SaveCalibration(dir string, result *CalibrationResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("calibration: marshal: %w", err)
	}

	path := filepath.Join(dir, "calibration.json")
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("calibration: write: %w", err)
	}
	return os.Rename(tmpPath, path)
}

// LoadCalibration reads a CalibrationResult from disk.
// Returns nil, nil if the file does not exist.
func LoadCalibration(dir string) (*CalibrationResult, error) {
	path := filepath.Join(dir, "calibration.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calibration: read: %w", err)
	}

	var result CalibrationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("calibration: unmarshal: %w", err)
	}
	return &result, nil
}

// SmoothCalibration applies exponential moving average to combine an existing
// calibration with a new probe result. Alpha = 2/(probeCount+1).
func SmoothCalibration(existing, fresh *CalibrationResult) *CalibrationResult {
	if existing == nil {
		return fresh
	}
	count := existing.ProbeCount + fresh.ProbeCount
	alpha := 2.0 / (float64(count) + 1)

	return &CalibrationResult{
		FsyncLatency:     emaDuration(existing.FsyncLatency, fresh.FsyncLatency, alpha),
		ReplayThroughput: emaFloat(existing.ReplayThroughput, fresh.ReplayThroughput, alpha),
		SeqReadSpeed:     emaFloat(existing.SeqReadSpeed, fresh.SeqReadSpeed, alpha),
		RandWriteSpeed:   emaFloat(existing.RandWriteSpeed, fresh.RandWriteSpeed, alpha),
		ProbeCount:       count,
		ProbedAt:         fresh.ProbedAt,
	}
}

// emaFloat computes EMA: existing*(1-alpha) + fresh*alpha.
func emaFloat(existing, fresh, alpha float64) float64 {
	return existing*(1-alpha) + fresh*alpha
}

// emaDuration computes EMA for durations.
func emaDuration(existing, fresh time.Duration, alpha float64) time.Duration {
	e := float64(existing)
	f := float64(fresh)
	return time.Duration(e*(1-alpha) + f*alpha)
}
