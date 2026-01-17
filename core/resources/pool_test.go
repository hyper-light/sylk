package resources

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestNewResourcePool verifies pool creation respects config.
func TestNewResourcePool(t *testing.T) {
	tests := []struct {
		name         string
		total        int
		userReserved float64
		wantTotal    int
		wantReserved int
	}{
		{"normal config", 100, 0.20, 100, 20},
		{"small pool", 5, 0.20, 5, 1},
		{"zero total normalized to 1", 0, 0.20, 1, 1},
		{"negative total normalized to 1", -5, 0.20, 1, 1},
		{"high reserve percent capped", 10, 1.5, 10, 10},
		{"zero percent normalized", 10, 0, 10, 2},
		{"100% reserve", 10, 1.0, 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ResourcePoolConfig{
				Total:               tt.total,
				UserReservedPercent: tt.userReserved,
				PipelineTimeout:     time.Second,
			}
			pool := NewResourcePool(ResourceTypeFile, cfg)
			defer pool.Close()

			stats := pool.Stats()
			if stats.Total != tt.wantTotal {
				t.Errorf("got total %d, want %d", stats.Total, tt.wantTotal)
			}
			if stats.UserReserved != tt.wantReserved {
				t.Errorf("got reserved %d, want %d", stats.UserReserved, tt.wantReserved)
			}
		})
	}
}

// TestAcquireUserNeverFails verifies user acquisition always succeeds.
func TestAcquireUserNeverFails(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               5,
		UserReservedPercent: 0.20,
		PipelineTimeout:     100 * time.Millisecond,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()
	handles := make([]*ResourceHandle, 0, 10)

	// Acquire more handles than total - user should always succeed
	for i := 0; i < 10; i++ {
		h, err := pool.AcquireUser(ctx)
		if err != nil {
			t.Fatalf("AcquireUser failed on iteration %d: %v", i, err)
		}
		handles = append(handles, h)
	}

	// All handles should be valid
	for i, h := range handles {
		if h == nil {
			t.Errorf("handle %d is nil", i)
		}
		if h.ID == "" {
			t.Errorf("handle %d has empty ID", i)
		}
		if !h.IsUser {
			t.Errorf("handle %d should be marked as user", i)
		}
	}
}

// TestAcquireUserPreemptsPipeline verifies users preempt pipelines when needed.
func TestAcquireUserPreemptsPipeline(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               3,
		UserReservedPercent: 0.34, // 1 reserved for user
		PipelineTimeout:     100 * time.Millisecond,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()

	// Acquire all pipeline slots (total - reserved = 2)
	ph1, err := pool.AcquirePipeline(ctx, 1)
	if err != nil {
		t.Fatalf("first pipeline acquire failed: %v", err)
	}
	ph2, err := pool.AcquirePipeline(ctx, 2)
	if err != nil {
		t.Fatalf("second pipeline acquire failed: %v", err)
	}

	// Acquire reserved user slot
	uh1, err := pool.AcquireUser(ctx)
	if err != nil {
		t.Fatalf("first user acquire failed: %v", err)
	}

	// All resources now in use - next user should preempt
	stats := pool.Stats()
	if stats.Available != 0 {
		t.Errorf("expected 0 available, got %d", stats.Available)
	}

	// This should preempt a pipeline
	uh2, err := pool.AcquireUser(ctx)
	if err != nil {
		t.Fatalf("second user acquire should preempt, got error: %v", err)
	}

	if uh2 == nil {
		t.Fatal("expected non-nil handle after preemption")
	}
	if !uh2.IsUser {
		t.Error("preempted handle should be marked as user")
	}

	// Cleanup - only valid handles should release
	pool.Release(uh1)
	pool.Release(uh2)
	// One of ph1/ph2 was preempted, releasing may fail
	pool.Release(ph1)
	pool.Release(ph2)
}

// TestPipelineQueuesWithPriority verifies priority queue ordering.
func TestPipelineQueuesWithPriority(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               2,
		UserReservedPercent: 0.50, // 1 reserved for user
		PipelineTimeout:     2 * time.Second,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()

	// Acquire the one available pipeline slot
	ph, err := pool.AcquirePipeline(ctx, 1)
	if err != nil {
		t.Fatalf("initial pipeline acquire failed: %v", err)
	}

	results := make(chan int, 3)
	var wg sync.WaitGroup

	// Queue three waiters with different priorities
	for _, priority := range []int{1, 3, 2} {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			h, err := pool.AcquirePipeline(ctx, p)
			if err != nil {
				return
			}
			results <- p
			pool.Release(h)
		}(priority)
	}

	// Let all goroutines queue up
	time.Sleep(50 * time.Millisecond)

	// Release original handle to trigger queue processing
	pool.Release(ph)

	// Wait for all to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
		close(results)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for goroutines")
	}

	// First one out should be highest priority (3)
	first := <-results
	if first != 3 {
		t.Errorf("expected priority 3 first, got %d", first)
	}
}

// TestPipelineTimeoutReturnsError verifies timeout behavior.
func TestPipelineTimeoutReturnsError(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               1,
		UserReservedPercent: 1.0, // All reserved for user
		PipelineTimeout:     50 * time.Millisecond,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()

	// Pipeline should timeout immediately since all reserved
	start := time.Now()
	_, err := pool.AcquirePipeline(ctx, 1)
	elapsed := time.Since(start)

	if err != ErrAcquireTimeout {
		t.Errorf("expected ErrAcquireTimeout, got %v", err)
	}

	// Should have waited approximately the timeout duration
	if elapsed < 40*time.Millisecond {
		t.Errorf("timeout happened too fast: %v", elapsed)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

// TestReleaseReturnsResources verifies resources return to pool.
func TestReleaseReturnsResources(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               5,
		UserReservedPercent: 0.20,
		PipelineTimeout:     time.Second,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()

	// Acquire all
	handles := make([]*ResourceHandle, 5)
	for i := 0; i < 5; i++ {
		h, err := pool.AcquireUser(ctx)
		if err != nil {
			t.Fatalf("acquire %d failed: %v", i, err)
		}
		handles[i] = h
	}

	stats := pool.Stats()
	if stats.Available != 0 {
		t.Errorf("expected 0 available, got %d", stats.Available)
	}
	if stats.InUse != 5 {
		t.Errorf("expected 5 in use, got %d", stats.InUse)
	}

	// Release all
	for i, h := range handles {
		err := pool.Release(h)
		if err != nil {
			t.Errorf("release %d failed: %v", i, err)
		}
	}

	stats = pool.Stats()
	if stats.Available != 5 {
		t.Errorf("expected 5 available after release, got %d", stats.Available)
	}
	if stats.InUse != 0 {
		t.Errorf("expected 0 in use after release, got %d", stats.InUse)
	}
}

// TestReleaseInvalidHandle verifies error on invalid handle.
func TestReleaseInvalidHandle(t *testing.T) {
	cfg := DefaultResourcePoolConfig(10)
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	// Nil handle
	err := pool.Release(nil)
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for nil, got %v", err)
	}

	// Unknown handle
	fake := &ResourceHandle{ID: "fake-id"}
	err = pool.Release(fake)
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for unknown, got %v", err)
	}

	// Double release
	ctx := context.Background()
	h, _ := pool.AcquireUser(ctx)
	pool.Release(h)
	err = pool.Release(h)
	if err != ErrInvalidHandle {
		t.Errorf("expected ErrInvalidHandle for double release, got %v", err)
	}
}

func TestConcurrentAcquireRelease(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               50,
		UserReservedPercent: 0.20,
		PipelineTimeout:     time.Second,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	var successCount atomic.Int32

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(priority int) {
			defer wg.Done()

			h, err := pool.AcquirePipeline(ctx, priority)
			if err != nil {
				return
			}
			successCount.Add(1)

			time.Sleep(time.Duration(priority) * time.Millisecond)

			pool.Release(h)
		}(i % 5)
	}

	wg.Wait()

	if successCount.Load() == 0 {
		t.Error("no successful acquisitions")
	}

	stats := pool.Stats()
	if stats.InUse != 0 {
		t.Errorf("expected 0 in use after all releases, got %d", stats.InUse)
	}
	if stats.Available != stats.Total {
		t.Errorf("expected full availability, got %d/%d", stats.Available, stats.Total)
	}
}

func TestConcurrentMixedAcquireRelease(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               100,
		UserReservedPercent: 0.20,
		PipelineTimeout:     time.Second,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	var userSuccess, pipelineSuccess atomic.Int32

	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			h, err := pool.AcquireUser(ctx)
			if err != nil {
				return
			}
			userSuccess.Add(1)
			time.Sleep(time.Millisecond)
			pool.Release(h)
		}()

		go func(p int) {
			defer wg.Done()
			h, err := pool.AcquirePipeline(ctx, p)
			if err != nil {
				return
			}
			pipelineSuccess.Add(1)
			time.Sleep(time.Millisecond)
			pool.Release(h)
		}(i % 5)
	}

	wg.Wait()

	if userSuccess.Load() != 50 {
		t.Errorf("expected 50 user successes, got %d", userSuccess.Load())
	}
	if pipelineSuccess.Load() == 0 {
		t.Error("no pipeline acquisitions succeeded")
	}
}

// TestEmptyPool verifies behavior with minimum pool size.
func TestEmptyPool(t *testing.T) {
	// Zero config normalizes to 1
	cfg := ResourcePoolConfig{
		Total:               0,
		UserReservedPercent: 0.20,
		PipelineTimeout:     50 * time.Millisecond,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()

	stats := pool.Stats()
	if stats.Total != 1 {
		t.Errorf("expected normalized total of 1, got %d", stats.Total)
	}

	// Should still be able to acquire for user
	h, err := pool.AcquireUser(ctx)
	if err != nil {
		t.Fatalf("user acquire failed: %v", err)
	}
	pool.Release(h)
}

// TestFullPool verifies behavior when pool is exhausted.
func TestFullPool(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               3,
		UserReservedPercent: 0.34, // 1 reserved
		PipelineTimeout:     50 * time.Millisecond,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()

	// Fill pipeline slots
	h1, _ := pool.AcquirePipeline(ctx, 1)
	h2, _ := pool.AcquirePipeline(ctx, 1)

	// Fill user reserved slot
	h3, _ := pool.AcquireUser(ctx)

	// Pipeline should timeout now
	_, err := pool.AcquirePipeline(ctx, 1)
	if err != ErrAcquireTimeout {
		t.Errorf("expected timeout, got %v", err)
	}

	// But user should still succeed (via preemption)
	h4, err := pool.AcquireUser(ctx)
	if err != nil {
		t.Errorf("user should succeed via preemption, got %v", err)
	}

	pool.Release(h1)
	pool.Release(h2)
	pool.Release(h3)
	if h4 != nil {
		pool.Release(h4)
	}
}

// TestAllReserved verifies behavior when all resources are reserved for user.
func TestAllReserved(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               5,
		UserReservedPercent: 1.0, // All reserved for user
		PipelineTimeout:     50 * time.Millisecond,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()

	// Pipeline should immediately go to waiting and timeout
	_, err := pool.AcquirePipeline(ctx, 1)
	if err != ErrAcquireTimeout {
		t.Errorf("expected timeout when all reserved, got %v", err)
	}

	// User should succeed
	h, err := pool.AcquireUser(ctx)
	if err != nil {
		t.Fatalf("user should succeed, got %v", err)
	}
	pool.Release(h)
}

// TestPoolClose verifies closed pool behavior.
func TestPoolClose(t *testing.T) {
	cfg := DefaultResourcePoolConfig(10)
	pool := NewResourcePool(ResourceTypeFile, cfg)

	// Close should succeed
	err := pool.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Double close should fail
	err = pool.Close()
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed on double close, got %v", err)
	}

	ctx := context.Background()

	// Acquire after close should fail
	_, err = pool.AcquireUser(ctx)
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed after close, got %v", err)
	}

	_, err = pool.AcquirePipeline(ctx, 1)
	if err != ErrPoolClosed {
		t.Errorf("expected ErrPoolClosed after close, got %v", err)
	}
}

// TestContextCancellation verifies context cancellation.
func TestContextCancellation(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               1,
		UserReservedPercent: 1.0, // All reserved
		PipelineTimeout:     5 * time.Second,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		_, err := pool.AcquirePipeline(ctx, 1)
		done <- err
	}()

	// Cancel quickly
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for cancellation")
	}
}

// TestStatsAccuracy verifies stats are accurate.
func TestStatsAccuracy(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               10,
		UserReservedPercent: 0.30,
		PipelineTimeout:     time.Second,
	}
	pool := NewResourcePool(ResourceTypeNetwork, cfg)
	defer pool.Close()

	ctx := context.Background()

	stats := pool.Stats()
	if stats.Type != ResourceTypeNetwork {
		t.Errorf("expected ResourceTypeNetwork, got %v", stats.Type)
	}
	if stats.Total != 10 {
		t.Errorf("expected total 10, got %d", stats.Total)
	}
	if stats.UserReserved != 3 {
		t.Errorf("expected reserved 3, got %d", stats.UserReserved)
	}
	if stats.Available != 10 {
		t.Errorf("expected available 10, got %d", stats.Available)
	}

	// Acquire some
	h1, _ := pool.AcquireUser(ctx)
	h2, _ := pool.AcquirePipeline(ctx, 1)

	stats = pool.Stats()
	if stats.InUse != 2 {
		t.Errorf("expected in use 2, got %d", stats.InUse)
	}
	if stats.Available != 8 {
		t.Errorf("expected available 8, got %d", stats.Available)
	}

	pool.Release(h1)
	pool.Release(h2)
}

// TestWaitQueueStats verifies wait queue reporting.
func TestWaitQueueStats(t *testing.T) {
	cfg := ResourcePoolConfig{
		Total:               1,
		UserReservedPercent: 1.0,
		PipelineTimeout:     500 * time.Millisecond,
	}
	pool := NewResourcePool(ResourceTypeFile, cfg)
	defer pool.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	// Start waiters
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.AcquirePipeline(ctx, 1)
		}()
	}

	// Let them queue
	time.Sleep(50 * time.Millisecond)

	stats := pool.Stats()
	if stats.WaitQueue != 3 {
		t.Errorf("expected 3 in wait queue, got %d", stats.WaitQueue)
	}

	wg.Wait()
}

// TestResourceHandleFields verifies handle contains correct data.
func TestResourceHandleFields(t *testing.T) {
	cfg := DefaultResourcePoolConfig(10)
	pool := NewResourcePool(ResourceTypeProcess, cfg)
	defer pool.Close()

	ctx := context.Background()

	h, _ := pool.AcquireUser(ctx)
	if h.Type != ResourceTypeProcess {
		t.Errorf("expected ResourceTypeProcess, got %v", h.Type)
	}
	if !h.IsUser {
		t.Error("expected IsUser to be true")
	}
	if h.AcquiredAt.IsZero() {
		t.Error("expected non-zero AcquiredAt")
	}
	pool.Release(h)

	h, _ = pool.AcquirePipeline(ctx, 5)
	if h.Priority != 5 {
		t.Errorf("expected priority 5, got %d", h.Priority)
	}
	if h.IsUser {
		t.Error("expected IsUser to be false for pipeline")
	}
	pool.Release(h)
}

// TestDefaultConfig verifies default configuration.
func TestDefaultConfig(t *testing.T) {
	cfg := DefaultResourcePoolConfig(100)

	if cfg.Total != 100 {
		t.Errorf("expected total 100, got %d", cfg.Total)
	}
	if cfg.UserReservedPercent != 0.20 {
		t.Errorf("expected 0.20 reserved, got %f", cfg.UserReservedPercent)
	}
	if cfg.PipelineTimeout != 30*time.Second {
		t.Errorf("expected 30s timeout, got %v", cfg.PipelineTimeout)
	}
}
