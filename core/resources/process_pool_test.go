package resources

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewProcessPool verifies pool creation.
func TestNewProcessPool(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	stats := pool.Stats()
	expectedMax := runtime.NumCPU() * 2
	if stats.MaxProcs != expectedMax {
		t.Errorf("expected max procs %d, got %d", expectedMax, stats.MaxProcs)
	}
	if stats.CPUCores != runtime.NumCPU() {
		t.Errorf("expected CPU cores %d, got %d", runtime.NumCPU(), stats.CPUCores)
	}
}

// TestProcessPoolRespectsCPUMultiplier verifies CPU multiplier.
func TestProcessPoolRespectsCPUMultiplier(t *testing.T) {
	tests := []struct {
		name       string
		multiplier int
		wantMax    int
	}{
		{"1x", 1, runtime.NumCPU()},
		{"2x", 2, runtime.NumCPU() * 2},
		{"4x", 4, runtime.NumCPU() * 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ProcessPoolConfig{
				CPUMultiplier:   tt.multiplier,
				UserReserved:    0.20,
				PipelineTimeout: time.Second,
			}
			pool := NewProcessPool(cfg)
			defer pool.Close()

			stats := pool.Stats()
			if stats.MaxProcs != tt.wantMax {
				t.Errorf("expected max procs %d, got %d", tt.wantMax, stats.MaxProcs)
			}
		})
	}
}

// TestProcessPoolAcquireUser verifies user acquisition.
func TestProcessPoolAcquireUser(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	h, err := pool.AcquireUser(ctx)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer pool.Release(h)

	if h == nil {
		t.Fatal("got nil handle")
	}
	if h.handle == nil {
		t.Fatal("got nil inner handle")
	}
}

// TestProcessPoolAcquireUserForCommand verifies command tracking.
func TestProcessPoolAcquireUserForCommand(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	command := "git status"

	h, err := pool.AcquireUserForCommand(ctx, command)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer pool.Release(h)

	if h.Command != command {
		t.Errorf("expected command %q, got %q", command, h.Command)
	}
}

// TestProcessPoolAcquirePipeline verifies pipeline acquisition.
func TestProcessPoolAcquirePipeline(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	h, err := pool.AcquirePipeline(ctx, 5)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer pool.Release(h)

	if h == nil {
		t.Fatal("got nil handle")
	}
}

// TestProcessPoolAcquirePipelineForCommand verifies pipeline command tracking.
func TestProcessPoolAcquirePipelineForCommand(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	command := "npm install"

	h, err := pool.AcquirePipelineForCommand(ctx, 3, command)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer pool.Release(h)

	if h.Command != command {
		t.Errorf("expected command %q, got %q", command, h.Command)
	}
}

// TestProcessPoolSetPID verifies PID tracking.
func TestProcessPoolSetPID(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUser(ctx)
	defer pool.Release(h)

	// Initially no PID
	if h.PID != 0 {
		t.Errorf("expected initial PID 0, got %d", h.PID)
	}

	// Set PID
	pool.SetPID(h, 12345)
	if h.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", h.PID)
	}

	// Verify in active PIDs
	pids := pool.ActivePIDs()
	found := false
	for _, pid := range pids {
		if pid == 12345 {
			found = true
			break
		}
	}
	if !found {
		t.Error("PID 12345 not found in active PIDs")
	}
}

// TestProcessPoolSetPIDNil verifies nil handle is safe.
func TestProcessPoolSetPIDNil(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	// Should not panic
	pool.SetPID(nil, 123)
}

// TestProcessPoolSetPIDUpdate verifies PID update removes old PID.
func TestProcessPoolSetPIDUpdate(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUser(ctx)
	defer pool.Release(h)

	pool.SetPID(h, 111)
	pool.SetPID(h, 222)

	// Old PID should be gone
	pids := pool.ActivePIDs()
	for _, pid := range pids {
		if pid == 111 {
			t.Error("old PID 111 should be removed")
		}
	}

	// New PID should exist
	found := false
	for _, pid := range pids {
		if pid == 222 {
			found = true
			break
		}
	}
	if !found {
		t.Error("new PID 222 not found")
	}
}

// TestProcessPoolHandleByPID verifies PID lookup.
func TestProcessPoolHandleByPID(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUserForCommand(ctx, "test-cmd")
	defer pool.Release(h)

	pool.SetPID(h, 9999)

	found := pool.HandleByPID(9999)
	if found == nil {
		t.Fatal("expected to find handle by PID")
	}
	if found.Command != "test-cmd" {
		t.Errorf("expected command 'test-cmd', got %q", found.Command)
	}

	// Unknown PID
	notFound := pool.HandleByPID(8888)
	if notFound != nil {
		t.Error("expected nil for unknown PID")
	}
}

// TestProcessPoolActivePIDs verifies PID listing.
func TestProcessPoolActivePIDs(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	// Initially empty
	pids := pool.ActivePIDs()
	if len(pids) != 0 {
		t.Errorf("expected 0 PIDs, got %d", len(pids))
	}

	h1, _ := pool.AcquireUser(ctx)
	h2, _ := pool.AcquireUser(ctx)
	pool.SetPID(h1, 100)
	pool.SetPID(h2, 200)

	pids = pool.ActivePIDs()
	if len(pids) != 2 {
		t.Errorf("expected 2 PIDs, got %d", len(pids))
	}

	pool.Release(h1)
	pool.Release(h2)

	pids = pool.ActivePIDs()
	if len(pids) != 0 {
		t.Errorf("expected 0 PIDs after release, got %d", len(pids))
	}
}

// TestProcessPoolRelease verifies proper release.
func TestProcessPoolRelease(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUser(ctx)
	pool.SetPID(h, 555)

	beforeStats := pool.Stats()
	err := pool.Release(h)
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	afterStats := pool.Stats()
	if afterStats.Available != beforeStats.Available+1 {
		t.Error("expected available to increase")
	}
	if afterStats.TrackedPIDs != 0 {
		t.Errorf("expected 0 tracked PIDs, got %d", afterStats.TrackedPIDs)
	}

	// Verify PID removed
	if pool.HandleByPID(555) != nil {
		t.Error("PID should be removed after release")
	}
}

// TestProcessPoolReleaseInvalid verifies error on invalid release.
func TestProcessPoolReleaseInvalid(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	err := pool.Release(nil)
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for nil, got %v", err)
	}

	err = pool.Release(&ProcessHandle{})
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for empty, got %v", err)
	}
}

// TestProcessPoolStats verifies statistics accuracy.
func TestProcessPoolStats(t *testing.T) {
	cfg := ProcessPoolConfig{
		CPUMultiplier:   2,
		UserReserved:    0.25,
		PipelineTimeout: time.Second,
	}
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	stats := pool.Stats()
	if stats.Type != ResourceTypeProcess {
		t.Errorf("expected ResourceTypeProcess, got %v", stats.Type)
	}
	if stats.CPUCores != runtime.NumCPU() {
		t.Errorf("expected %d CPU cores, got %d", runtime.NumCPU(), stats.CPUCores)
	}
	if stats.MaxProcs != runtime.NumCPU()*2 {
		t.Errorf("expected %d max procs, got %d", runtime.NumCPU()*2, stats.MaxProcs)
	}

	h1, _ := pool.AcquireUser(ctx)
	h2, _ := pool.AcquireUser(ctx)
	pool.SetPID(h1, 100)

	stats = pool.Stats()
	if stats.ActiveHandles != 2 {
		t.Errorf("expected 2 active handles, got %d", stats.ActiveHandles)
	}
	if stats.TrackedPIDs != 1 {
		t.Errorf("expected 1 tracked PID, got %d", stats.TrackedPIDs)
	}

	pool.Release(h1)
	pool.Release(h2)
}

// TestProcessPoolClose verifies pool closure.
func TestProcessPoolClose(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)

	err := pool.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	err = pool.Close()
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed on double close, got %v", err)
	}

	ctx := context.Background()
	_, err = pool.AcquireUser(ctx)
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed after close, got %v", err)
	}
}

// TestProcessPoolConcurrent verifies thread safety.
func TestProcessPoolConcurrent(t *testing.T) {
	cfg := ProcessPoolConfig{
		CPUMultiplier:   4, // High multiplier for more capacity
		UserReserved:    0.20,
		PipelineTimeout: time.Second,
	}
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			h, err := pool.AcquireUser(ctx)
			if err != nil {
				return
			}
			successCount.Add(1)

			pool.SetPID(h, idx+1000)
			time.Sleep(time.Millisecond)
			pool.Release(h)
		}(i)
	}

	wg.Wait()

	if successCount.Load() != 50 {
		t.Errorf("expected 50 successes, got %d", successCount.Load())
	}

	stats := pool.Stats()
	if stats.ActiveHandles != 0 {
		t.Errorf("expected 0 active handles, got %d", stats.ActiveHandles)
	}
	if stats.TrackedPIDs != 0 {
		t.Errorf("expected 0 tracked PIDs, got %d", stats.TrackedPIDs)
	}
}

// TestProcessPoolStartedAt verifies timestamp.
func TestProcessPoolStartedAt(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	before := time.Now()
	h, _ := pool.AcquireUser(ctx)
	after := time.Now()
	defer pool.Release(h)

	if h.StartedAt.Before(before) || h.StartedAt.After(after) {
		t.Error("StartedAt not in expected range")
	}
}

// TestProcessPoolConfigNormalization verifies config defaults.
func TestProcessPoolConfigNormalization(t *testing.T) {
	cfg := ProcessPoolConfig{
		CPUMultiplier:   0,
		UserReserved:    0,
		PipelineTimeout: 0,
	}
	pool := NewProcessPool(cfg)
	defer pool.Close()

	stats := pool.Stats()
	// Zero multiplier should normalize to 2
	expectedMax := runtime.NumCPU() * 2
	if stats.MaxProcs != expectedMax {
		t.Errorf("expected normalized max procs %d, got %d", expectedMax, stats.MaxProcs)
	}
}

// TestProcessPoolNegativeMultiplier verifies negative multiplier normalization.
func TestProcessPoolNegativeMultiplier(t *testing.T) {
	cfg := ProcessPoolConfig{
		CPUMultiplier:   -5,
		UserReserved:    0.20,
		PipelineTimeout: time.Second,
	}
	pool := NewProcessPool(cfg)
	defer pool.Close()

	stats := pool.Stats()
	expectedMax := runtime.NumCPU() * 2 // Normalized to default 2
	if stats.MaxProcs != expectedMax {
		t.Errorf("expected max procs %d, got %d", expectedMax, stats.MaxProcs)
	}
}

// TestProcessPoolDefaultConfig verifies default configuration.
func TestProcessPoolDefaultConfig(t *testing.T) {
	cfg := DefaultProcessPoolConfig()

	if cfg.CPUMultiplier != 2 {
		t.Errorf("expected multiplier 2, got %d", cfg.CPUMultiplier)
	}
	if cfg.UserReserved != 0.20 {
		t.Errorf("expected 0.20 reserved, got %f", cfg.UserReserved)
	}
	if cfg.PipelineTimeout != 30*time.Second {
		t.Errorf("expected 30s timeout, got %v", cfg.PipelineTimeout)
	}
}

// TestProcessPoolUserNeverFails verifies user acquisition never fails.
func TestProcessPoolUserNeverFails(t *testing.T) {
	cfg := ProcessPoolConfig{
		CPUMultiplier:   1, // Small pool
		UserReserved:    0.50,
		PipelineTimeout: 50 * time.Millisecond,
	}
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	// Acquire many more than pool size
	handles := make([]*ProcessHandle, 0, 20)
	for i := 0; i < 20; i++ {
		h, err := pool.AcquireUser(ctx)
		if err != nil {
			t.Fatalf("user acquire %d failed: %v", i, err)
		}
		handles = append(handles, h)
	}

	// All should succeed
	if len(handles) != 20 {
		t.Errorf("expected 20 handles, got %d", len(handles))
	}

	for _, h := range handles {
		pool.Release(h)
	}
}

// TestProcessPoolPipelineTimeout verifies pipeline timeout.
func TestProcessPoolPipelineTimeout(t *testing.T) {
	cfg := ProcessPoolConfig{
		CPUMultiplier:   1,
		UserReserved:    1.0, // All reserved for user
		PipelineTimeout: 50 * time.Millisecond,
	}
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()

	// Pipeline should timeout since all reserved
	start := time.Now()
	_, err := pool.AcquirePipeline(ctx, 1)
	elapsed := time.Since(start)

	if err != ErrAcquireTimeout {
		t.Errorf("expected ErrAcquireTimeout, got %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("timeout too fast: %v", elapsed)
	}
}

// TestProcessPoolHandleFields verifies handle contains correct data.
func TestProcessPoolHandleFields(t *testing.T) {
	cfg := DefaultProcessPoolConfig()
	pool := NewProcessPool(cfg)
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUserForCommand(ctx, "ls -la")
	defer pool.Release(h)

	if h.Command != "ls -la" {
		t.Errorf("expected command 'ls -la', got %q", h.Command)
	}
	if h.PID != 0 {
		t.Errorf("expected initial PID 0, got %d", h.PID)
	}
	if h.handle == nil {
		t.Error("expected non-nil inner handle")
	}
}

// TestProcessPoolMinimumOne verifies at least one process slot.
func TestProcessPoolMinimumOne(t *testing.T) {
	// Even with very low CPU count simulation, should have at least 1
	cfg := ProcessPoolConfig{
		CPUMultiplier:   1,
		UserReserved:    0.20,
		PipelineTimeout: time.Second,
	}
	pool := NewProcessPool(cfg)
	defer pool.Close()

	stats := pool.Stats()
	if stats.MaxProcs < 1 {
		t.Errorf("expected at least 1 max proc, got %d", stats.MaxProcs)
	}
}
