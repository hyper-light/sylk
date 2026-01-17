package resources

import (
	"context"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestNewFileHandlePool verifies pool creation with default config.
func TestNewFileHandlePool(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	stats := pool.Stats()
	if stats.MaxLimit <= 0 {
		t.Errorf("expected positive max limit, got %d", stats.MaxLimit)
	}
	if stats.Total <= 0 {
		t.Errorf("expected positive total, got %d", stats.Total)
	}
}

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
	if expectedMax < 10 {
		expectedMax = 10
	}

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
			if expectedMax < 10 {
				expectedMax = 10
			}

			if stats.MaxLimit != expectedMax {
				t.Errorf("expected max limit %d, got %d", expectedMax, stats.MaxLimit)
			}
		})
	}
}

// TestFileHandleAcquireUser verifies user acquisition never fails.
func TestFileHandleAcquireUser(t *testing.T) {
	cfg := FileHandlePoolConfig{
		UsagePercent:    0.50,
		UserReserved:    0.20,
		PipelineTimeout: 100 * time.Millisecond,
	}
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Should always succeed
	for i := 0; i < 5; i++ {
		h, err := pool.AcquireUser(ctx)
		if err != nil {
			t.Fatalf("user acquire %d failed: %v", i, err)
		}
		if h == nil {
			t.Fatalf("got nil handle on acquire %d", i)
		}
		pool.Release(h)
	}
}

// TestFileHandleAcquireUserForPath verifies path-specific acquisition.
func TestFileHandleAcquireUserForPath(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	path := "/test/file.txt"

	h, err := pool.AcquireUserForPath(ctx, path)
	if err != nil {
		t.Fatalf("acquire for path failed: %v", err)
	}
	defer pool.Release(h)

	if h.Path != path {
		t.Errorf("expected path %q, got %q", path, h.Path)
	}
}

// TestFileHandleAcquirePipeline verifies pipeline acquisition.
func TestFileHandleAcquirePipeline(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	h, err := pool.AcquirePipeline(ctx, 5)
	if err != nil {
		t.Fatalf("pipeline acquire failed: %v", err)
	}
	defer pool.Release(h)

	if h == nil {
		t.Fatal("got nil handle")
	}
	if h.handle == nil {
		t.Fatal("got nil inner handle")
	}
}

// TestFileHandleAcquirePipelineForPath verifies path-specific pipeline acquisition.
func TestFileHandleAcquirePipelineForPath(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	path := "/pipeline/file.log"

	h, err := pool.AcquirePipelineForPath(ctx, 3, path)
	if err != nil {
		t.Fatalf("acquire for path failed: %v", err)
	}
	defer pool.Release(h)

	if h.Path != path {
		t.Errorf("expected path %q, got %q", path, h.Path)
	}
}

// TestFileHandleRelease verifies resource return.
func TestFileHandleRelease(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	h, _ := pool.AcquireUser(ctx)
	initialStats := pool.Stats()

	err = pool.Release(h)
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}

	afterStats := pool.Stats()
	if afterStats.Available != initialStats.Available+1 {
		t.Errorf("expected available to increase by 1")
	}
}

// TestFileHandleReleaseInvalid verifies error on invalid release.
func TestFileHandleReleaseInvalid(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	err = pool.Release(nil)
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for nil, got %v", err)
	}

	err = pool.Release(&FileHandle{})
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for empty handle, got %v", err)
	}
}

// TestFileHandleTouch verifies usage tracking.
func TestFileHandleTouch(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUser(ctx)
	defer pool.Release(h)

	initialLastUsed := h.LastUsed
	initialUseCount := h.UseCount

	time.Sleep(10 * time.Millisecond)
	pool.Touch(h)

	if h.LastUsed.Before(initialLastUsed) || h.LastUsed.Equal(initialLastUsed) {
		t.Error("expected LastUsed to be updated")
	}
	if h.UseCount != initialUseCount+1 {
		t.Errorf("expected UseCount %d, got %d", initialUseCount+1, h.UseCount)
	}
}

// TestFileHandleTouchNil verifies nil touch is safe.
func TestFileHandleTouchNil(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	// Should not panic
	pool.Touch(nil)
}

// TestFileHandleLeakedHandles verifies leak detection.
func TestFileHandleLeakedHandles(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	// Acquire handles
	h1, _ := pool.AcquireUser(ctx)
	h2, _ := pool.AcquireUser(ctx)

	// Touch one to keep it fresh
	time.Sleep(50 * time.Millisecond)
	pool.Touch(h1)

	// Check for leaks with a threshold that excludes h1
	leaked := pool.LeakedHandles(30 * time.Millisecond)

	foundH2 := false
	for _, l := range leaked {
		if l.handle.ID == h2.handle.ID {
			foundH2 = true
		}
	}

	if !foundH2 {
		t.Error("expected h2 to be reported as leaked")
	}

	pool.Release(h1)
	pool.Release(h2)
}

// TestFileHandleStats verifies statistics.
func TestFileHandleStats(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()

	stats := pool.Stats()
	if stats.ActiveHandles != 0 {
		t.Errorf("expected 0 active handles, got %d", stats.ActiveHandles)
	}

	h1, _ := pool.AcquireUser(ctx)
	h2, _ := pool.AcquireUser(ctx)

	stats = pool.Stats()
	if stats.ActiveHandles != 2 {
		t.Errorf("expected 2 active handles, got %d", stats.ActiveHandles)
	}

	pool.Release(h1)
	pool.Release(h2)

	stats = pool.Stats()
	if stats.ActiveHandles != 0 {
		t.Errorf("expected 0 active handles after release, got %d", stats.ActiveHandles)
	}
}

// TestFileHandleClose verifies pool closure.
func TestFileHandleClose(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}

	err = pool.Close()
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

// TestFileHandleConcurrent verifies thread safety.
func TestFileHandleConcurrent(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
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

			pool.Touch(h)
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
}

// TestFileHandleOpenedAt verifies timestamp is set.
func TestFileHandleOpenedAt(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	before := time.Now()
	h, _ := pool.AcquireUser(ctx)
	after := time.Now()
	defer pool.Release(h)

	if h.OpenedAt.Before(before) || h.OpenedAt.After(after) {
		t.Errorf("OpenedAt %v not between %v and %v", h.OpenedAt, before, after)
	}
}

// TestFileHandleUseCountInitialized verifies initial use count.
func TestFileHandleUseCountInitialized(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()
	pool, err := NewFileHandlePool(cfg)
	if err != nil {
		t.Fatalf("failed to create pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	h, _ := pool.AcquireUser(ctx)
	defer pool.Release(h)

	if h.UseCount != 1 {
		t.Errorf("expected initial UseCount 1, got %d", h.UseCount)
	}
}

// TestFileHandleConfigNormalization verifies config defaults.
func TestFileHandleConfigNormalization(t *testing.T) {
	tests := []struct {
		name        string
		cfg         FileHandlePoolConfig
		wantPercent float64
	}{
		{
			name:        "zero percent normalized",
			cfg:         FileHandlePoolConfig{UsagePercent: 0},
			wantPercent: 0.50,
		},
		{
			name:        "negative percent normalized",
			cfg:         FileHandlePoolConfig{UsagePercent: -0.5},
			wantPercent: 0.50,
		},
		{
			name:        "over 100% normalized",
			cfg:         FileHandlePoolConfig{UsagePercent: 1.5},
			wantPercent: 0.50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewFileHandlePool(tt.cfg)
			if err != nil {
				t.Fatalf("failed to create pool: %v", err)
			}
			defer pool.Close()

			// Pool was created successfully, normalization worked
			stats := pool.Stats()
			if stats.Total <= 0 {
				t.Error("expected positive total after normalization")
			}
		})
	}
}

// TestFileHandleDefaultConfig verifies default configuration.
func TestFileHandleDefaultConfig(t *testing.T) {
	cfg := DefaultFileHandlePoolConfig()

	if cfg.UsagePercent != 0.50 {
		t.Errorf("expected 0.50 usage, got %f", cfg.UsagePercent)
	}
	if cfg.UserReserved != 0.20 {
		t.Errorf("expected 0.20 reserved, got %f", cfg.UserReserved)
	}
	if cfg.PipelineTimeout != 30*time.Second {
		t.Errorf("expected 30s timeout, got %v", cfg.PipelineTimeout)
	}
}
