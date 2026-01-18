package bulkhead

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testConfig returns a configuration suitable for testing.
func testConfig() BulkheadConfig {
	return BulkheadConfig{
		MaxConcurrent: 2,
		MaxQueueSize:  3,
		QueueTimeout:  100 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 3,
			SuccessThreshold: 2,
			Timeout:          50 * time.Millisecond,
		},
	}
}

func TestNewBulkhead(t *testing.T) {
	cfg := testConfig()
	b := NewBulkhead("test-1", LevelSession, cfg)
	defer b.Stop()

	if b.id != "test-1" {
		t.Errorf("expected id 'test-1', got %q", b.id)
	}

	if b.level != LevelSession {
		t.Errorf("expected level LevelSession, got %v", b.level)
	}

	// Semaphore should be pre-filled
	if len(b.semaphore) != cfg.MaxConcurrent {
		t.Errorf("expected semaphore size %d, got %d", cfg.MaxConcurrent, len(b.semaphore))
	}
}

func TestBulkhead_AcquireRelease(t *testing.T) {
	cfg := testConfig()
	b := NewBulkhead("test-ar", LevelSession, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Acquire first slot
	err := b.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire first slot: %v", err)
	}

	// Check metrics
	stats := b.Stats()
	if stats.Active != 1 {
		t.Errorf("expected 1 active, got %d", stats.Active)
	}

	// Release and verify
	b.Release()
	stats = b.Stats()
	if stats.Active != 0 {
		t.Errorf("expected 0 active after release, got %d", stats.Active)
	}
}

func TestBulkhead_SemaphoreLimit(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 2,
		MaxQueueSize:  0, // No queue - immediate rejection
		QueueTimeout:  10 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-limit", LevelProvider, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Acquire all slots
	for i := 0; i < cfg.MaxConcurrent; i++ {
		err := b.Acquire(ctx)
		if err != nil {
			t.Fatalf("failed to acquire slot %d: %v", i, err)
		}
	}

	// Next acquire should fail (queue full since size is 0)
	err := b.Acquire(ctx)
	if err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}
}

func TestBulkhead_QueueOverflow(t *testing.T) {
	// Use zero queue size to test immediate rejection
	cfg := BulkheadConfig{
		MaxConcurrent: 1,
		MaxQueueSize:  0, // No queue - immediate rejection when semaphore full
		QueueTimeout:  100 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-overflow", LevelModel, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Acquire the only concurrent slot
	err := b.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire slot: %v", err)
	}

	// Next request should get queue full immediately
	err = b.Acquire(ctx)
	if err != ErrQueueFull {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}

	// Release
	b.Release()
}

func TestBulkhead_QueueFillsAndOverflows(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 1,
		MaxQueueSize:  2,
		QueueTimeout:  2 * time.Second, // Long timeout to ensure we test queue filling
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-fill-overflow", LevelModel, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Acquire the only concurrent slot
	err := b.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire slot: %v", err)
	}

	// Try to fill queue + overflow with more requests than queue can hold
	// We need 3 requests (queue size 2 + 1 to overflow)
	results := make(chan error, 4)
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := b.Acquire(ctx)
			results <- err
			if err == nil {
				b.Release()
			}
		}()
	}

	// Wait for all requests to complete or fail
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var queueFullCount int
	var successCount int
	for err := range results {
		if err == ErrQueueFull {
			queueFullCount++
		} else if err == nil {
			successCount++
		}
	}

	// At least one should have gotten queue full
	// (since we have 4 requests but only 1 concurrent + 2 queue = 3 capacity)
	if queueFullCount == 0 {
		t.Error("expected at least one ErrQueueFull")
	}

	// Release the main slot
	b.Release()
}

func TestBulkhead_QueueTimeout(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 1,
		MaxQueueSize:  5,
		QueueTimeout:  50 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-timeout", LevelSession, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Acquire the only slot and hold it
	err := b.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire slot: %v", err)
	}

	// Try to acquire another - should timeout
	start := time.Now()
	err = b.Acquire(ctx)
	elapsed := time.Since(start)

	if err != ErrQueueTimeout {
		t.Errorf("expected ErrQueueTimeout, got %v", err)
	}

	// Should have waited approximately the queue timeout
	if elapsed < 40*time.Millisecond {
		t.Errorf("timeout too fast: %v", elapsed)
	}

	b.Release()
}

func TestBulkhead_ContextCancellation(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 1,
		MaxQueueSize:  5,
		QueueTimeout:  time.Second,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-cancel", LevelSession, cfg)
	defer b.Stop()

	// Acquire the only slot
	err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("failed to acquire slot: %v", err)
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start acquire in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Acquire(ctx)
	}()

	// Cancel after short delay
	time.Sleep(20 * time.Millisecond)
	cancel()

	// Should receive context cancelled error
	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timed out waiting for cancellation")
	}

	b.Release()
}

func TestBulkhead_CircuitTransitions(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 5,
		MaxQueueSize:  10,
		QueueTimeout:  100 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 3,
			SuccessThreshold: 2,
			Timeout:          30 * time.Millisecond,
		},
	}
	b := NewBulkhead("test-circuit", LevelProvider, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Initial state should be closed
	if b.CircuitStateValue() != CircuitClosed {
		t.Errorf("expected CircuitClosed, got %v", b.CircuitStateValue())
	}

	// Record failures to open the circuit
	for i := 0; i < cfg.CircuitConfig.FailureThreshold; i++ {
		b.RecordFailure()
	}

	if b.CircuitStateValue() != CircuitOpen {
		t.Errorf("expected CircuitOpen, got %v", b.CircuitStateValue())
	}

	// Acquire should fail when circuit is open
	err := b.Acquire(ctx)
	if err != ErrCircuitOpen {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}

	// Wait for timeout to allow half-open transition
	time.Sleep(cfg.CircuitConfig.Timeout + 10*time.Millisecond)

	// Next acquire should succeed (transitions to half-open)
	err = b.Acquire(ctx)
	if err != nil {
		t.Fatalf("expected nil error after timeout, got %v", err)
	}

	if b.CircuitStateValue() != CircuitHalfOpen {
		t.Errorf("expected CircuitHalfOpen, got %v", b.CircuitStateValue())
	}

	b.Release()
}

func TestBulkhead_HalfOpenToClosedTransition(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 5,
		MaxQueueSize:  10,
		QueueTimeout:  100 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 2,
			SuccessThreshold: 2,
			Timeout:          20 * time.Millisecond,
		},
	}
	b := NewBulkhead("test-half-close", LevelModel, cfg)
	defer b.Stop()

	// Open the circuit
	for i := 0; i < cfg.CircuitConfig.FailureThreshold; i++ {
		b.RecordFailure()
	}

	// Wait for half-open
	time.Sleep(cfg.CircuitConfig.Timeout + 10*time.Millisecond)

	// Trigger transition to half-open
	_ = b.Acquire(context.Background())
	b.Release()

	if b.CircuitStateValue() != CircuitHalfOpen {
		t.Fatalf("expected CircuitHalfOpen, got %v", b.CircuitStateValue())
	}

	// Record successes to close the circuit
	for i := 0; i < cfg.CircuitConfig.SuccessThreshold; i++ {
		b.RecordSuccess()
	}

	if b.CircuitStateValue() != CircuitClosed {
		t.Errorf("expected CircuitClosed after successes, got %v", b.CircuitStateValue())
	}
}

func TestBulkhead_HalfOpenToOpenTransition(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 5,
		MaxQueueSize:  10,
		QueueTimeout:  100 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 2,
			SuccessThreshold: 3,
			Timeout:          20 * time.Millisecond,
		},
	}
	b := NewBulkhead("test-half-open", LevelSession, cfg)
	defer b.Stop()

	// Open the circuit
	for i := 0; i < cfg.CircuitConfig.FailureThreshold; i++ {
		b.RecordFailure()
	}

	// Wait for half-open
	time.Sleep(cfg.CircuitConfig.Timeout + 10*time.Millisecond)

	// Trigger transition to half-open
	_ = b.Acquire(context.Background())
	b.Release()

	if b.CircuitStateValue() != CircuitHalfOpen {
		t.Fatalf("expected CircuitHalfOpen, got %v", b.CircuitStateValue())
	}

	// Single failure should immediately reopen
	b.RecordFailure()

	if b.CircuitStateValue() != CircuitOpen {
		t.Errorf("expected CircuitOpen after failure in half-open, got %v", b.CircuitStateValue())
	}
}

func TestBulkhead_Stats(t *testing.T) {
	cfg := testConfig()
	b := NewBulkhead("test-stats", LevelSession, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Initial stats
	stats := b.Stats()
	if stats.ID != "test-stats" {
		t.Errorf("expected ID 'test-stats', got %q", stats.ID)
	}
	if stats.Level != LevelSession {
		t.Errorf("expected LevelSession, got %v", stats.Level)
	}
	if stats.Active != 0 {
		t.Errorf("expected 0 active, got %d", stats.Active)
	}
	if stats.TotalRequests != 0 {
		t.Errorf("expected 0 total requests, got %d", stats.TotalRequests)
	}

	// Acquire and check
	_ = b.Acquire(ctx)
	stats = b.Stats()
	if stats.Active != 1 {
		t.Errorf("expected 1 active, got %d", stats.Active)
	}
	if stats.TotalRequests != 1 {
		t.Errorf("expected 1 total request, got %d", stats.TotalRequests)
	}

	// Record failure and check
	b.RecordFailure()
	stats = b.Stats()
	if stats.TotalFailures != 1 {
		t.Errorf("expected 1 failure, got %d", stats.TotalFailures)
	}

	b.Release()
}

func TestBulkhead_ConcurrentAccess(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 5,
		MaxQueueSize:  20,
		QueueTimeout:  200 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 100,
			SuccessThreshold: 50,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-concurrent", LevelProvider, cfg)
	defer b.Stop()

	ctx := context.Background()
	var wg sync.WaitGroup
	var successCount atomic.Int32
	var errorCount atomic.Int32

	// Launch many concurrent acquires
	numGoroutines := 50
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := b.Acquire(ctx)
			if err != nil {
				errorCount.Add(1)
				return
			}
			successCount.Add(1)

			// Simulate some work
			time.Sleep(10 * time.Millisecond)

			b.RecordSuccess()
			b.Release()
		}()
	}

	wg.Wait()

	// All should eventually succeed or fail cleanly
	total := successCount.Load() + errorCount.Load()
	if int(total) != numGoroutines {
		t.Errorf("expected %d total operations, got %d", numGoroutines, total)
	}

	// At minimum, some should succeed
	if successCount.Load() == 0 {
		t.Error("expected at least some successful acquires")
	}
}

func TestBulkhead_Stop(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 1,
		MaxQueueSize:  10,
		QueueTimeout:  time.Second,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-stop", LevelSession, cfg)

	// Acquire the slot
	err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("failed to acquire: %v", err)
	}

	// Queue a request
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.Acquire(context.Background())
	}()

	// Give time for request to enqueue
	time.Sleep(20 * time.Millisecond)

	// Stop the bulkhead
	b.Stop()

	// Queued request should receive cancellation
	select {
	case err := <-errCh:
		if err != context.Canceled && err != ErrQueueTimeout {
			t.Errorf("expected context.Canceled or ErrQueueTimeout, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("timed out waiting for queued request to complete")
	}
}

func TestBulkhead_IDAndLevel(t *testing.T) {
	b := NewBulkhead("my-id", LevelModel, testConfig())
	defer b.Stop()

	if b.ID() != "my-id" {
		t.Errorf("expected ID 'my-id', got %q", b.ID())
	}

	if b.Level() != LevelModel {
		t.Errorf("expected LevelModel, got %v", b.Level())
	}
}

func TestBulkhead_SuccessResetsFailureCount(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 5,
		MaxQueueSize:  10,
		QueueTimeout:  100 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 3,
			SuccessThreshold: 2,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-reset", LevelSession, cfg)
	defer b.Stop()

	// Record 2 failures (one less than threshold)
	b.RecordFailure()
	b.RecordFailure()

	// Circuit should still be closed
	if b.CircuitStateValue() != CircuitClosed {
		t.Errorf("expected CircuitClosed, got %v", b.CircuitStateValue())
	}

	// Success should reset counter
	b.RecordSuccess()

	// Now 2 more failures should not open (counter was reset)
	b.RecordFailure()
	b.RecordFailure()

	if b.CircuitStateValue() != CircuitClosed {
		t.Errorf("expected CircuitClosed after reset, got %v", b.CircuitStateValue())
	}

	// One more failure should open it (3 consecutive)
	b.RecordFailure()

	if b.CircuitStateValue() != CircuitOpen {
		t.Errorf("expected CircuitOpen, got %v", b.CircuitStateValue())
	}
}

func TestBulkhead_QueueProcessing(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 1,
		MaxQueueSize:  5,
		QueueTimeout:  500 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-queue-process", LevelProvider, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Acquire the only slot
	err := b.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire: %v", err)
	}

	// Queue multiple requests
	var wg sync.WaitGroup
	results := make([]error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			results[idx] = b.Acquire(ctx)
			if results[idx] == nil {
				b.Release()
			}
		}()
	}

	// Give time for requests to enqueue
	time.Sleep(30 * time.Millisecond)

	// Check queued count
	stats := b.Stats()
	if stats.Queued < 1 {
		t.Errorf("expected at least 1 queued, got %d", stats.Queued)
	}

	// Release the slot - should process queue
	b.Release()

	wg.Wait()

	// At least some should succeed
	successCount := 0
	for _, err := range results {
		if err == nil {
			successCount++
		}
	}
	if successCount == 0 {
		t.Error("expected at least some queued requests to succeed")
	}
}

func TestBulkhead_AvailableSlots(t *testing.T) {
	cfg := BulkheadConfig{
		MaxConcurrent: 3,
		MaxQueueSize:  5,
		QueueTimeout:  100 * time.Millisecond,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          time.Second,
		},
	}
	b := NewBulkhead("test-available", LevelModel, cfg)
	defer b.Stop()

	ctx := context.Background()

	// Initially all slots available
	stats := b.Stats()
	if stats.Available != 3 {
		t.Errorf("expected 3 available, got %d", stats.Available)
	}

	// Acquire one
	_ = b.Acquire(ctx)
	stats = b.Stats()
	if stats.Available != 2 {
		t.Errorf("expected 2 available, got %d", stats.Available)
	}

	// Acquire another
	_ = b.Acquire(ctx)
	stats = b.Stats()
	if stats.Available != 1 {
		t.Errorf("expected 1 available, got %d", stats.Available)
	}

	// Release one
	b.Release()
	stats = b.Stats()
	if stats.Available != 2 {
		t.Errorf("expected 2 available after release, got %d", stats.Available)
	}
}
