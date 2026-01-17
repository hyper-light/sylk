//go:build !windows

package resources

import (
	"syscall"
	"testing"
	"time"
)

// TestFileHandlePoolRespectsUlimit verifies pool respects OS limits.
func TestFileHandlePoolRespectsUlimit(t *testing.T) {
	var rlimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlimit); err != nil {
		t.Skipf("cannot get rlimit: %v", err)
	}

	cfg := FileHandlePoolConfig{
		UsagePercent:    0.50,
		UserReserved:    0.20,
		PipelineTimeout: time.Second,
	}
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	stats := pool.Stats()
	expectedMax := int(float64(rlimit.Cur) * 0.50)
	expectedMax = max(expectedMax, 10)

	if stats.MaxLimit != expectedMax {
		t.Errorf("expected max limit %d (50%% of %d), got %d",
			expectedMax, rlimit.Cur, stats.MaxLimit)
	}
}

// TestFileHandlePoolUsagePercent verifies different usage percentages.
func TestFileHandlePoolUsagePercent(t *testing.T) {
	var rlimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlimit); err != nil {
		t.Skipf("cannot get rlimit: %v", err)
	}

	tests := []struct {
		name    string
		percent float64
	}{
		{"25%", 0.25},
		{"75%", 0.75},
		{"100%", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := FileHandlePoolConfig{
				UsagePercent:    tt.percent,
				UserReserved:    0.20,
				PipelineTimeout: time.Second,
			}
			pool, err := NewFileHandlePool(cfg)
			if err != nil {
				t.Fatalf("failed to create pool: %v", err)
			}
			defer pool.Close()

			stats := pool.Stats()
			expectedMax := int(float64(rlimit.Cur) * tt.percent)
			expectedMax = max(expectedMax, 10)

			if stats.MaxLimit != expectedMax {
				t.Errorf("expected max limit %d, got %d", expectedMax, stats.MaxLimit)
			}
		})
	}
}
