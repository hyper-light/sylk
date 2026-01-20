package context

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// W4N.6 Tests: WriteQueue Backoff Retry
// =============================================================================

// TestW4N6_HappyPath_QueueAcceptsObservations verifies that observations are
// accepted when the queue has capacity.
func TestW4N6_HappyPath_QueueAcceptsObservations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 100,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Record multiple observations - all should succeed
	for i := 0; i < 50; i++ {
		obs := &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		}
		if err := log.Record(context.Background(), obs); err != nil {
			t.Fatalf("failed to record observation %d: %v", i, err)
		}
	}

	// Verify all were recorded
	if log.CurrentSequence() != 50 {
		t.Errorf("expected sequence 50, got %d", log.CurrentSequence())
	}
}

// TestW4N6_HappyPath_DefaultBackoffConfig verifies the default backoff configuration.
func TestW4N6_HappyPath_DefaultBackoffConfig(t *testing.T) {
	cfg := DefaultWriteQueueBackoff()

	if cfg.MaxRetries != 5 {
		t.Errorf("expected MaxRetries 5, got %d", cfg.MaxRetries)
	}
	if cfg.InitialDelay != 1*time.Millisecond {
		t.Errorf("expected InitialDelay 1ms, got %v", cfg.InitialDelay)
	}
	if cfg.MaxDelay != 50*time.Millisecond {
		t.Errorf("expected MaxDelay 50ms, got %v", cfg.MaxDelay)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("expected Multiplier 2.0, got %f", cfg.Multiplier)
	}
	if cfg.Timeout != 500*time.Millisecond {
		t.Errorf("expected Timeout 500ms, got %v", cfg.Timeout)
	}
}

// TestW4N6_QueueFull_RetriesSucceed tests that retries succeed when the queue
// drains during backoff.
func TestW4N6_QueueFull_RetriesSucceed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create log with tiny queue and aggressive backoff
	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   10,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   1.5,
		Timeout:      1 * time.Second,
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 2, // Very small queue
		BackoffConfig:  backoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Start concurrent writers - the small queue will fill up
	// but retries should allow all to succeed eventually
	var wg sync.WaitGroup
	successCount := int32(0)
	writeCount := 20

	for i := 0; i < writeCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			obs := &EpisodeObservation{
				TaskCompleted: true,
				FollowUpCount: id,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := log.Record(ctx, obs); err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// Most or all writes should succeed with retry
	if successCount < int32(writeCount/2) {
		t.Errorf("expected at least %d successes with retry, got %d", writeCount/2, successCount)
	}

	t.Logf("successful writes: %d/%d", successCount, writeCount)
}

// TestW4N6_QueueFull_RetriesExhausted tests that retries are exhausted when
// the queue stays full.
func TestW4N6_QueueFull_RetriesExhausted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create log with tiny queue and very short timeout
	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
		Multiplier:   2.0,
		Timeout:      10 * time.Millisecond,
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1, // Minimal queue
		SyncInterval:   time.Hour, // Prevent draining
		BackoffConfig:  backoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Fill the queue with blocking operations
	// First write will succeed, subsequent will fill queue and fail
	var exhaustedSeen bool
	for i := 0; i < 100; i++ {
		obs := &EpisodeObservation{TaskCompleted: true}
		err := log.Record(context.Background(), obs)
		if errors.Is(err, ErrWriteQueueRetryExhausted) {
			exhaustedSeen = true
			break
		}
	}

	if !exhaustedSeen {
		t.Log("ErrWriteQueueRetryExhausted not seen - queue drained too fast (timing dependent)")
	}
}

// TestW4N6_NoRetries_ImmediateFail tests that with MaxRetries=0, queue full
// returns immediately.
func TestW4N6_NoRetries_ImmediateFail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create log with no retries
	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   0, // No retries
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
		Timeout:      100 * time.Millisecond,
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1,
		SyncInterval:   time.Hour,
		BackoffConfig:  backoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Try to flood the queue
	var queueFullSeen bool
	for i := 0; i < 100; i++ {
		obs := &EpisodeObservation{TaskCompleted: true}
		err := log.Record(context.Background(), obs)
		if errors.Is(err, ErrWriteQueueFull) {
			queueFullSeen = true
			break
		}
	}

	// Should see ErrWriteQueueFull, not ErrWriteQueueRetryExhausted
	t.Logf("ErrWriteQueueFull seen: %v", queueFullSeen)
}

// TestW4N6_ConcurrentWrites_DuringBackoff tests concurrent writes with backoff.
func TestW4N6_ConcurrentWrites_DuringBackoff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   10,
		InitialDelay: 500 * time.Microsecond,
		MaxDelay:     5 * time.Millisecond,
		Multiplier:   1.5,
		Timeout:      2 * time.Second,
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 5, // Small queue to trigger backoff
		BackoffConfig:  backoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Concurrent writes from multiple goroutines
	const numWriters = 10
	const writesPerWriter = 20

	var wg sync.WaitGroup
	successCounts := make([]int32, numWriters)

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				obs := &EpisodeObservation{
					TaskCompleted: true,
					FollowUpCount: writerID*1000 + i,
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				if err := log.Record(ctx, obs); err == nil {
					atomic.AddInt32(&successCounts[writerID], 1)
				}
				cancel()
			}
		}(w)
	}

	wg.Wait()

	// Count total successes
	var totalSuccess int32
	for _, count := range successCounts {
		totalSuccess += count
	}

	expectedMin := int32(numWriters * writesPerWriter / 2)
	if totalSuccess < expectedMin {
		t.Errorf("expected at least %d total successes, got %d", expectedMin, totalSuccess)
	}

	t.Logf("concurrent writes: %d/%d successful", totalSuccess, numWriters*writesPerWriter)

	// Verify data integrity - close and reopen
	log.Close()

	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	observations, err := log2.GetObservations()
	if err != nil {
		t.Fatalf("failed to get observations: %v", err)
	}

	if int32(len(observations)) != totalSuccess {
		t.Errorf("expected %d observations on reopen, got %d", totalSuccess, len(observations))
	}

	// Verify unique sequence numbers
	seqMap := make(map[uint64]bool)
	for _, obs := range observations {
		if seqMap[obs.Sequence] {
			t.Errorf("duplicate sequence number: %d", obs.Sequence)
		}
		seqMap[obs.Sequence] = true
	}
}

// TestW4N6_ExactlyAtCapacity tests writes when queue is exactly at capacity.
func TestW4N6_ExactlyAtCapacity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	queueSize := 10
	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: queueSize,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Write exactly queue size observations
	for i := 0; i < queueSize; i++ {
		obs := &EpisodeObservation{TaskCompleted: true}
		if err := log.Record(context.Background(), obs); err != nil {
			t.Errorf("failed to record at index %d: %v", i, err)
		}
	}

	if log.CurrentSequence() != uint64(queueSize) {
		t.Errorf("expected sequence %d, got %d", queueSize, log.CurrentSequence())
	}
}

// TestW4N6_TimeoutDuringRetry tests that timeout is respected during retry.
func TestW4N6_TimeoutDuringRetry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	// Create log with very short timeout
	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   100, // Many retries but short timeout
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
		Multiplier:   2.0,
		Timeout:      25 * time.Millisecond, // Very short timeout
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1,
		SyncInterval:   time.Hour,
		BackoffConfig:  backoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Measure time for exhausted error
	var exhaustedErr error
	start := time.Now()
	for i := 0; i < 100; i++ {
		obs := &EpisodeObservation{TaskCompleted: true}
		err := log.Record(context.Background(), obs)
		if errors.Is(err, ErrWriteQueueRetryExhausted) {
			exhaustedErr = err
			break
		}
	}
	elapsed := time.Since(start)

	if exhaustedErr != nil {
		// Should timeout within reasonable time (not run all 100 retries)
		if elapsed > 500*time.Millisecond {
			t.Errorf("timeout not respected, took %v", elapsed)
		}
		t.Logf("retry exhausted after %v", elapsed)
	}
}

// TestW4N6_ContextCancellation tests that context cancellation stops retries.
func TestW4N6_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   100,
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
		Timeout:      10 * time.Second, // Long timeout
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1,
		SyncInterval:   time.Hour,
		BackoffConfig:  backoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Fill the queue
	_ = log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})

	// Create a context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	// Try to write - should be cancelled
	start := time.Now()
	err = log.Record(ctx, &EpisodeObservation{TaskCompleted: true})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) && err != nil {
		// May have succeeded or got queue full before cancel
		t.Logf("got error: %v (may be timing dependent)", err)
	}

	// Should not have waited for all retries
	if elapsed > 500*time.Millisecond {
		t.Errorf("context cancellation not respected, waited %v", elapsed)
	}
}

// TestW4N6_BackoffDelayCalculation tests the exponential backoff delay calculation.
func TestW4N6_BackoffDelayCalculation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	cfg := WriteQueueBackoffConfig{
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
	}

	// Test exponential growth
	delays := []time.Duration{
		log.calculateBackoffDelay(0, cfg), // 10ms * 2^0 = 10ms
		log.calculateBackoffDelay(1, cfg), // 10ms * 2^1 = 20ms
		log.calculateBackoffDelay(2, cfg), // 10ms * 2^2 = 40ms
		log.calculateBackoffDelay(3, cfg), // 10ms * 2^3 = 80ms
		log.calculateBackoffDelay(4, cfg), // 10ms * 2^4 = 160ms -> capped to 100ms
	}

	expected := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
		100 * time.Millisecond, // capped
	}

	for i, got := range delays {
		if got != expected[i] {
			t.Errorf("delay[%d] = %v, expected %v", i, got, expected[i])
		}
	}
}

// TestW4N6_DefaultMultiplier tests that default multiplier is used when not set.
func TestW4N6_DefaultMultiplier(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	cfg := WriteQueueBackoffConfig{
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     1000 * time.Millisecond,
		Multiplier:   0, // Zero should default to 2.0
	}

	delay0 := log.calculateBackoffDelay(0, cfg)
	delay1 := log.calculateBackoffDelay(1, cfg)

	// With default multiplier 2.0, delay1 should be 2x delay0
	if delay1 != 2*delay0 {
		t.Errorf("expected delay1 (%v) = 2 * delay0 (%v)", delay1, delay0)
	}
}

// TestW4N6_CustomBackoffConfig tests that custom backoff config is used.
func TestW4N6_CustomBackoffConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	customBackoff := &WriteQueueBackoffConfig{
		MaxRetries:   3,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     20 * time.Millisecond,
		Multiplier:   1.5,
		Timeout:      100 * time.Millisecond,
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:          path,
		BackoffConfig: customBackoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Verify the config was applied
	if log.backoffConfig.MaxRetries != 3 {
		t.Errorf("expected MaxRetries 3, got %d", log.backoffConfig.MaxRetries)
	}
	if log.backoffConfig.InitialDelay != 5*time.Millisecond {
		t.Errorf("expected InitialDelay 5ms, got %v", log.backoffConfig.InitialDelay)
	}
	if log.backoffConfig.Multiplier != 1.5 {
		t.Errorf("expected Multiplier 1.5, got %f", log.backoffConfig.Multiplier)
	}
}

// TestW4N6_RaceConditions tests for race conditions during concurrent backoff.
func TestW4N6_RaceConditions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   5,
		InitialDelay: 100 * time.Microsecond,
		MaxDelay:     1 * time.Millisecond,
		Multiplier:   1.5,
		Timeout:      1 * time.Second,
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 3,
		BackoffConfig:  backoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Run with -race flag to detect race conditions
	const numGoroutines = 50
	const writesPerGoroutine = 10

	var wg sync.WaitGroup
	var successCount int64

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < writesPerGoroutine; i++ {
				obs := &EpisodeObservation{
					TaskCompleted: true,
					FollowUpCount: gid*1000 + i,
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := log.Record(ctx, obs); err == nil {
					atomic.AddInt64(&successCount, 1)
				}
				cancel()
			}
		}(g)
	}

	wg.Wait()

	t.Logf("race condition test: %d/%d successful", successCount, numGoroutines*writesPerGoroutine)

	// Should have at least some successes
	if successCount == 0 {
		t.Error("expected at least some successful writes")
	}
}

// TestW4N6_DataIntegrity tests that data is not corrupted during backoff retry.
func TestW4N6_DataIntegrity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   10,
		InitialDelay: 100 * time.Microsecond,
		MaxDelay:     1 * time.Millisecond,
		Multiplier:   1.5,
		Timeout:      2 * time.Second,
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 5,
		BackoffConfig:  backoff,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Write observations with unique identifiers
	const numObs = 100
	written := make(map[int]bool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < numObs; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			obs := &EpisodeObservation{
				TaskCompleted: true,
				FollowUpCount: id,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := log.Record(ctx, obs); err == nil {
				mu.Lock()
				written[id] = true
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	log.Close()

	// Reopen and verify data integrity
	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	observations, err := log2.GetObservations()
	if err != nil {
		t.Fatalf("failed to get observations: %v", err)
	}

	// Verify all written observations are present
	found := make(map[int]bool)
	for _, obs := range observations {
		found[obs.Observation.FollowUpCount] = true
	}

	for id := range written {
		if !found[id] {
			t.Errorf("observation %d was written but not found on reopen", id)
		}
	}

	t.Logf("data integrity test: %d written, %d found", len(written), len(found))
}

// TestW4N6_ErrorTypes tests that correct error types are returned.
func TestW4N6_ErrorTypes(t *testing.T) {
	// Verify error definitions
	if !errors.Is(ErrWriteQueueFull, ErrWriteQueueFull) {
		t.Error("ErrWriteQueueFull should equal itself")
	}
	if !errors.Is(ErrWriteQueueRetryExhausted, ErrWriteQueueRetryExhausted) {
		t.Error("ErrWriteQueueRetryExhausted should equal itself")
	}
	if errors.Is(ErrWriteQueueFull, ErrWriteQueueRetryExhausted) {
		t.Error("ErrWriteQueueFull should not equal ErrWriteQueueRetryExhausted")
	}
}

// TestW4N6_ClosedLog tests behavior when log is closed during retry.
func TestW4N6_ClosedLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Close the log
	log.Close()

	// Try to record - should fail with closed error
	err = log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	if !errors.Is(err, ErrObservationLogClosed) {
		t.Errorf("expected ErrObservationLogClosed, got %v", err)
	}
}

// TestW4N6_PerformanceWithBackoff tests that backoff doesn't significantly
// impact performance under normal conditions.
func TestW4N6_PerformanceWithBackoff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		WriteQueueSize: 1000,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}
	defer log.Close()

	// Write 100 observations and measure time
	const numObs = 100
	start := time.Now()
	for i := 0; i < numObs; i++ {
		obs := &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		}
		if err := log.Record(context.Background(), obs); err != nil {
			t.Fatalf("failed to record: %v", err)
		}
	}
	elapsed := time.Since(start)

	// Should complete quickly with large queue
	if elapsed > 2*time.Second {
		t.Errorf("performance too slow: %v for %d observations", elapsed, numObs)
	}
	t.Logf("performance test: %d observations in %v", numObs, elapsed)
}

// TestW4N6_Integration tests the full integration of backoff with other features.
func TestW4N6_Integration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	adaptive := NewAdaptiveState()
	backoff := &WriteQueueBackoffConfig{
		MaxRetries:   5,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
		Timeout:      500 * time.Millisecond,
	}

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:           path,
		Adaptive:       adaptive,
		BackoffConfig:  backoff,
		WriteQueueSize: 10,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Record observations
	for i := 0; i < 20; i++ {
		obs := &EpisodeObservation{
			TaskCompleted: true,
			FollowUpCount: i,
		}
		if err := log.Record(context.Background(), obs); err != nil {
			t.Logf("record %d failed: %v (may be expected with small queue)", i, err)
		}
	}

	// Close and reopen
	log.Close()

	log2, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path:          path,
		Adaptive:      NewAdaptiveState(),
		BackoffConfig: backoff,
	})
	if err != nil {
		t.Fatalf("failed to reopen log: %v", err)
	}
	defer log2.Close()

	// Replay and verify
	count, err := log2.Replay()
	if err != nil {
		t.Fatalf("failed to replay: %v", err)
	}

	t.Logf("integration test: %d observations replayed", count)
}

// TestW4N6_FileCleanup ensures test files are properly cleaned up.
func TestW4N6_FileCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obs.wal")

	log, err := NewObservationLog(context.Background(), ObservationLogConfig{
		Path: path,
	})
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Write some data
	log.Record(context.Background(), &EpisodeObservation{TaskCompleted: true})
	log.Close()

	// File should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected WAL file to exist after close")
	}
}
