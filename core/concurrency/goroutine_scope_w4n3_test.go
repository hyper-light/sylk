package concurrency

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestW4N3_HappyPath_ShutdownWithinGracePeriod tests that shutdown completes
// successfully when all workers finish within the grace period.
func TestW4N3_HappyPath_ShutdownWithinGracePeriod(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	// Start a worker that completes quickly
	workerDone := make(chan struct{})
	err := scope.Go("quick-worker", time.Second, func(ctx context.Context) error {
		<-ctx.Done()
		close(workerDone)
		return nil
	})
	require.NoError(t, err)

	// Wait for worker to start
	time.Sleep(10 * time.Millisecond)

	// Shutdown with generous grace period
	start := time.Now()
	shutdownErr := scope.Shutdown(500*time.Millisecond, time.Second)
	elapsed := time.Since(start)

	require.NoError(t, shutdownErr)
	assert.Less(t, elapsed, 200*time.Millisecond, "shutdown should complete quickly")

	select {
	case <-workerDone:
		// Worker was properly notified
	default:
		t.Fatal("worker should have been cancelled")
	}
}

// TestW4N3_NegativePath_ShutdownExceedsGracePeriod tests that shutdown
// properly handles the case when workers don't complete within grace period.
func TestW4N3_NegativePath_ShutdownExceedsGracePeriod(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	// Start a worker that responds to cancellation but with a delay
	workerDone := make(chan struct{})
	err := scope.Go("slow-worker", time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		// Simulate slow cleanup
		time.Sleep(150 * time.Millisecond)
		close(workerDone)
		return nil
	})
	require.NoError(t, err)

	// Wait for worker to start
	time.Sleep(10 * time.Millisecond)

	// Shutdown with short grace period but longer hard deadline
	start := time.Now()
	shutdownErr := scope.Shutdown(50*time.Millisecond, 300*time.Millisecond)
	elapsed := time.Since(start)

	// Should complete successfully (worker finishes within hard deadline)
	require.NoError(t, shutdownErr)
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "should wait at least grace period")
	assert.Less(t, elapsed, 250*time.Millisecond, "should complete before hard deadline")
}

// TestW4N3_Failure_WorkersDoNotComplete tests leak detection when workers
// ignore cancellation completely.
func TestW4N3_Failure_WorkersDoNotComplete(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	// Start a worker that completely ignores cancellation
	blocker := make(chan struct{})
	err := scope.Go("stubborn-worker", time.Minute, func(ctx context.Context) error {
		<-blocker // Blocks forever until we close it
		return nil
	})
	require.NoError(t, err)

	// Wait for worker to start
	time.Sleep(10 * time.Millisecond)

	// Shutdown with very short timeouts
	shutdownErr := scope.Shutdown(25*time.Millisecond, 50*time.Millisecond)

	// Clean up the blocker to prevent goroutine leak in test
	close(blocker)

	// Should return a leak error
	require.Error(t, shutdownErr)
	var leakErr *GoroutineLeakError
	require.True(t, errors.As(shutdownErr, &leakErr))
	assert.Equal(t, 1, leakErr.LeakedCount)
	assert.Equal(t, "test-agent", leakErr.AgentID)
	assert.Len(t, leakErr.Workers, 1)
	assert.Contains(t, leakErr.Workers[0], "stubborn-worker")
}

// TestW4N3_RaceCondition_ConcurrentShutdownCalls tests that multiple
// concurrent shutdown calls are handled safely.
func TestW4N3_RaceCondition_ConcurrentShutdownCalls(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	// Start some workers
	for i := 0; i < 5; i++ {
		err := scope.Go("worker", time.Minute, func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		})
		require.NoError(t, err)
	}

	// Wait for workers to start
	time.Sleep(10 * time.Millisecond)

	// Launch many concurrent shutdown calls
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := scope.Shutdown(100*time.Millisecond, 200*time.Millisecond)
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	// All shutdown calls should complete without error
	for err := range errs {
		assert.NoError(t, err)
	}

	// Scope should be fully shut down
	assert.Equal(t, 0, scope.WorkerCount())
}

// TestW4N3_EdgeCase_ZeroTimeout tests behavior with zero timeout.
func TestW4N3_EdgeCase_ZeroTimeout(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	// Start a quick worker
	err := scope.Go("quick-worker", time.Second, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})
	require.NoError(t, err)

	// Wait for worker to start
	time.Sleep(10 * time.Millisecond)

	// Zero timeout should proceed to force shutdown immediately
	// Using very small grace (1ms) and hard deadline (50ms) to simulate near-zero
	shutdownErr := scope.Shutdown(time.Millisecond, 50*time.Millisecond)
	require.NoError(t, shutdownErr)
}

// TestW4N3_EdgeCase_VeryLongTimeout tests behavior with very long timeout.
func TestW4N3_EdgeCase_VeryLongTimeout(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	// Start a worker that completes quickly on cancellation
	err := scope.Go("quick-worker", time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})
	require.NoError(t, err)

	// Wait for worker to start
	time.Sleep(10 * time.Millisecond)

	// Very long timeout - but worker completes quickly
	start := time.Now()
	shutdownErr := scope.Shutdown(time.Hour, 2*time.Hour)
	elapsed := time.Since(start)

	require.NoError(t, shutdownErr)
	// Should complete much faster than the long timeout
	assert.Less(t, elapsed, 100*time.Millisecond)
}

// TestW4N3_EdgeCase_NoWorkers tests shutdown with no active workers.
func TestW4N3_EdgeCase_NoWorkers(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	// No workers started - immediate shutdown
	start := time.Now()
	shutdownErr := scope.Shutdown(time.Second, 2*time.Second)
	elapsed := time.Since(start)

	require.NoError(t, shutdownErr)
	assert.Less(t, elapsed, 50*time.Millisecond)
}

// TestW4N3_EdgeCase_WorkersAlreadyCompleted tests shutdown when workers
// have already completed before shutdown is called.
func TestW4N3_EdgeCase_WorkersAlreadyCompleted(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	// Start workers that complete immediately
	for i := 0; i < 5; i++ {
		err := scope.Go("instant-worker", time.Second, func(ctx context.Context) error {
			return nil // Complete immediately
		})
		require.NoError(t, err)
	}

	// Wait for all workers to complete
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, scope.WorkerCount())

	// Shutdown should be instant
	start := time.Now()
	shutdownErr := scope.Shutdown(time.Second, 2*time.Second)
	elapsed := time.Since(start)

	require.NoError(t, shutdownErr)
	assert.Less(t, elapsed, 50*time.Millisecond)
}

// TestW4N3_GoroutineLeak_Detection verifies that goroutines from
// waitWithTimeout don't leak after timeout.
func TestW4N3_GoroutineLeak_Detection(t *testing.T) {
	// Get baseline goroutine count
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	baselineCount := runtime.NumGoroutine()

	// Run multiple shutdown cycles with timeouts
	for i := 0; i < 5; i++ {
		ctx := context.Background()
		budget := newTestBudget()
		scope := NewGoroutineScope(ctx, "test-agent", budget)

		blocker := make(chan struct{})
		_ = scope.Go("leaky-worker", time.Minute, func(ctx context.Context) error {
			<-blocker
			return nil
		})

		// Force timeout path
		_ = scope.Shutdown(5*time.Millisecond, 10*time.Millisecond)
		close(blocker)
	}

	// Allow goroutines to settle
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	finalCount := runtime.NumGoroutine()
	// Allow some slack for test infrastructure goroutines
	leaked := finalCount - baselineCount
	assert.LessOrEqual(t, leaked, 2, "possible goroutine leak detected: baseline=%d, final=%d", baselineCount, finalCount)
}

// TestW4N3_MultipleWorkers_VariousCompletionTimes tests shutdown with
// workers that have different completion times.
func TestW4N3_MultipleWorkers_VariousCompletionTimes(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	completionCounts := &atomic.Int32{}

	// Worker that completes immediately
	_ = scope.Go("instant", time.Second, func(ctx context.Context) error {
		completionCounts.Add(1)
		return nil
	})

	// Worker that waits for cancellation
	_ = scope.Go("waiter", time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		completionCounts.Add(1)
		return nil
	})

	// Worker that has slow cleanup
	_ = scope.Go("slow-cleanup", time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		completionCounts.Add(1)
		return nil
	})

	// Allow instant worker to complete
	time.Sleep(20 * time.Millisecond)

	start := time.Now()
	shutdownErr := scope.Shutdown(200*time.Millisecond, 500*time.Millisecond)
	elapsed := time.Since(start)

	require.NoError(t, shutdownErr)
	assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "should wait for slow-cleanup")
	assert.Less(t, elapsed, 200*time.Millisecond, "should complete within grace period")
	assert.Equal(t, int32(3), completionCounts.Load(), "all workers should complete")
}

// TestW4N3_ShutdownDuringWorkerSpawn tests shutdown called while
// workers are being spawned.
func TestW4N3_ShutdownDuringWorkerSpawn(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	var spawnWg sync.WaitGroup
	spawnErrors := &atomic.Int32{}
	successfulSpawns := &atomic.Int32{}

	// Spawn workers in parallel with shutdown
	for i := 0; i < 10; i++ {
		spawnWg.Add(1)
		go func() {
			defer spawnWg.Done()
			err := scope.Go("spawning", time.Minute, func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			})
			if err != nil {
				if errors.Is(err, ErrScopeShutdown) {
					spawnErrors.Add(1)
				}
			} else {
				successfulSpawns.Add(1)
			}
		}()
	}

	// Start shutdown while spawning
	time.Sleep(5 * time.Millisecond)
	shutdownErr := scope.Shutdown(100*time.Millisecond, 200*time.Millisecond)

	spawnWg.Wait()

	require.NoError(t, shutdownErr)
	// Some spawns may succeed, some may fail with ErrScopeShutdown
	total := spawnErrors.Load() + successfulSpawns.Load()
	assert.Equal(t, int32(10), total, "all spawn attempts should be accounted for")
}

// TestW4N3_WaitWithTimeout_DirectTest directly tests the waitWithTimeout behavior.
func TestW4N3_WaitWithTimeout_DirectTest(t *testing.T) {
	t.Run("completes before timeout", func(t *testing.T) {
		ctx := context.Background()
		budget := newTestBudget()
		scope := NewGoroutineScope(ctx, "test-agent", budget)

		_ = scope.Go("quick", 100*time.Millisecond, func(ctx context.Context) error {
			return nil // Complete immediately
		})

		// Allow worker to complete
		time.Sleep(20 * time.Millisecond)

		result := scope.waitWithTimeout(time.Second)
		assert.True(t, result, "should return true when workers complete")
	})

	t.Run("times out", func(t *testing.T) {
		ctx := context.Background()
		budget := newTestBudget()
		scope := NewGoroutineScope(ctx, "test-agent", budget)

		blocker := make(chan struct{})
		_ = scope.Go("blocking", time.Minute, func(ctx context.Context) error {
			<-blocker
			return nil
		})

		start := time.Now()
		result := scope.waitWithTimeout(50 * time.Millisecond)
		elapsed := time.Since(start)

		assert.False(t, result, "should return false on timeout")
		assert.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "should wait for timeout")
		assert.Less(t, elapsed, 100*time.Millisecond, "should not wait much longer")

		close(blocker)
		// Allow worker to complete
		time.Sleep(20 * time.Millisecond)
	})
}

// TestW4N3_RaceCondition_ShutdownAndGo tests race between Go and Shutdown.
func TestW4N3_RaceCondition_ShutdownAndGo(t *testing.T) {
	for iteration := 0; iteration < 10; iteration++ {
		ctx := context.Background()
		budget := newTestBudget()
		scope := NewGoroutineScope(ctx, "test-agent", budget)

		// Start some initial workers
		for i := 0; i < 3; i++ {
			_ = scope.Go("initial", time.Minute, func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			})
		}

		var wg sync.WaitGroup
		shutdownDone := make(chan struct{})

		// Concurrent shutdown
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = scope.Shutdown(50*time.Millisecond, 100*time.Millisecond)
			close(shutdownDone)
		}()

		// Concurrent Go calls
		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = scope.Go("concurrent", time.Second, func(ctx context.Context) error {
					<-ctx.Done()
					return nil
				})
			}()
		}

		wg.Wait()
		<-shutdownDone
	}
}
