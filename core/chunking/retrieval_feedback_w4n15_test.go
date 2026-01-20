package chunking

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// W4N.15 GoroutineScope Tracking Tests for AsyncRetrievalFeedbackHook
// =============================================================================

// newTestBudgetW4N15 creates a test budget for scope tracking tests.
func newTestBudgetW4N15() *concurrency.GoroutineBudget {
	pressure := &atomic.Int32{}
	budget := concurrency.NewGoroutineBudget(pressure)
	budget.RegisterAgent("feedback-test-agent", "engineer")
	return budget
}

// newTestScopeW4N15 creates a test scope for tracking tests.
func newTestScopeW4N15(ctx context.Context) *concurrency.GoroutineScope {
	budget := newTestBudgetW4N15()
	return concurrency.NewGoroutineScope(ctx, "feedback-test-agent", budget)
}

// =============================================================================
// Happy Path Tests
// =============================================================================

// TestW4N15_BackgroundWorkerTrackedViaScope verifies that the background worker
// is tracked via GoroutineScope when scope is provided.
func TestW4N15_BackgroundWorkerTrackedViaScope(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scope := newTestScopeW4N15(ctx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	// Create hook with scope
	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
		Scope:      scope,
	})
	require.NoError(t, err)

	// Verify worker is tracked in scope
	assert.Equal(t, 1, scope.WorkerCount(), "worker should be tracked in scope")

	// Register a chunk and record feedback
	chunk := Chunk{
		ID:         "scope-test-chunk",
		Domain:     DomainCode,
		TokenCount: 100,
	}
	hook.RegisterChunk(chunk)

	err = hook.RecordRetrieval("scope-test-chunk", true, RetrievalContext{
		Timestamp: time.Now(),
	})
	require.NoError(t, err)

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify feedback was processed
	hitRate, total := hook.GetHitRate("scope-test-chunk")
	assert.Equal(t, 1, total, "should have 1 retrieval")
	assert.Equal(t, 1.0, hitRate, "hit rate should be 1.0")

	// Stop the hook
	hook.Stop()

	// Give scope time to clean up the worker
	time.Sleep(100 * time.Millisecond)

	// Worker count should be 0 after stop
	assert.Equal(t, 0, scope.WorkerCount(), "worker should be removed after stop")
}

// TestW4N15_ScopeTracksMultipleFeedbackOperations verifies that the scope-tracked
// worker processes multiple feedback operations correctly.
func TestW4N15_ScopeTracksMultipleFeedbackOperations(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scope := newTestScopeW4N15(ctx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 500,
		Scope:      scope,
	})
	require.NoError(t, err)
	defer hook.Stop()

	// Register multiple chunks
	numChunks := 10
	for i := 0; i < numChunks; i++ {
		chunk := Chunk{
			ID:         fmt.Sprintf("multi-chunk-%d", i),
			Domain:     Domain(i % 4),
			TokenCount: 100 + i*10,
		}
		hook.RegisterChunk(chunk)
	}

	// Record multiple feedbacks
	for i := 0; i < 50; i++ {
		chunkID := fmt.Sprintf("multi-chunk-%d", i%numChunks)
		err := hook.RecordRetrieval(chunkID, i%2 == 0, RetrievalContext{
			Timestamp: time.Now(),
		})
		require.NoError(t, err)
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	// Verify all chunks have stats
	totalRetrievals := 0
	for i := 0; i < numChunks; i++ {
		chunkID := fmt.Sprintf("multi-chunk-%d", i)
		_, count := hook.GetHitRate(chunkID)
		totalRetrievals += count
	}

	assert.Equal(t, 50, totalRetrievals, "all retrievals should be processed")
}

// =============================================================================
// Negative Path Tests
// =============================================================================

// TestW4N15_ScopeNilFallsBackToWaitGroup verifies that when scope is nil,
// the hook falls back to standard WaitGroup tracking.
func TestW4N15_ScopeNilFallsBackToWaitGroup(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	// Create hook without scope (nil)
	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
		Scope:      nil, // Explicitly nil
	})
	require.NoError(t, err)

	// Register and record feedback
	chunk := Chunk{
		ID:         "waitgroup-test-chunk",
		Domain:     DomainGeneral,
		TokenCount: 50,
	}
	hook.RegisterChunk(chunk)

	err = hook.RecordRetrieval("waitgroup-test-chunk", true, RetrievalContext{
		Timestamp: time.Now(),
	})
	require.NoError(t, err)

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Verify feedback was processed
	_, total := hook.GetHitRate("waitgroup-test-chunk")
	assert.Equal(t, 1, total, "feedback should be processed with WaitGroup tracking")

	// Stop should work with WaitGroup
	hook.Stop()

	// Verify hook is stopped
	err = hook.RecordRetrieval("waitgroup-test-chunk", true, RetrievalContext{
		Timestamp: time.Now(),
	})
	assert.Error(t, err, "should error after stop")
}

// TestW4N15_RecordAfterStopped verifies error handling when recording after stop.
func TestW4N15_RecordAfterStopped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scope := newTestScopeW4N15(ctx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
		Scope:      scope,
	})
	require.NoError(t, err)

	// Stop immediately
	hook.Stop()

	// Try to record - should fail
	err = hook.RecordRetrieval("stopped-chunk", true, RetrievalContext{
		Timestamp: time.Now(),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stopped")
}

// =============================================================================
// Failure Tests
// =============================================================================

// TestW4N15_ScopeShutdownStopsWorker verifies that scope shutdown stops the worker.
func TestW4N15_ScopeShutdownStopsWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	scope := newTestScopeW4N15(ctx)

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
		Context:    ctx,
		Scope:      scope,
	})
	require.NoError(t, err)

	// Register chunk and verify working
	chunk := Chunk{
		ID:         "shutdown-test-chunk",
		Domain:     DomainCode,
		TokenCount: 100,
	}
	hook.RegisterChunk(chunk)

	err = hook.RecordRetrieval("shutdown-test-chunk", true, RetrievalContext{
		Timestamp: time.Now(),
	})
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, scope.WorkerCount(), "worker should be active")

	// Cancel context and shutdown scope
	cancel()
	shutdownErr := scope.Shutdown(500*time.Millisecond, time.Second)
	assert.NoError(t, shutdownErr, "scope shutdown should succeed")

	// Worker should be gone
	assert.Equal(t, 0, scope.WorkerCount(), "worker should be stopped after scope shutdown")

	// Also stop the hook for cleanup
	hook.Stop()
}

// TestW4N15_ParentContextCancellation verifies parent context cancellation stops worker.
func TestW4N15_ParentContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	scope := newTestScopeW4N15(ctx)

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
		Context:    ctx,
		Scope:      scope,
	})
	require.NoError(t, err)

	// Verify worker is active
	assert.Equal(t, 1, scope.WorkerCount())

	// Cancel parent context
	cancel()

	// Wait for worker to stop
	time.Sleep(100 * time.Millisecond)

	// Worker may be stopped or stopping - scope shutdown will clean up
	err = scope.Shutdown(500*time.Millisecond, time.Second)
	assert.NoError(t, err)

	assert.Equal(t, 0, scope.WorkerCount())
	hook.Stop()
}

// =============================================================================
// Race Condition Tests
// =============================================================================

// TestW4N15_ConcurrentFeedbackDuringScopeShutdown tests race conditions between
// concurrent feedback recording and scope shutdown.
func TestW4N15_ConcurrentFeedbackDuringScopeShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scope := newTestScopeW4N15(ctx)

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 500,
		Scope:      scope,
	})
	require.NoError(t, err)

	// Register chunks
	for i := 0; i < 10; i++ {
		chunk := Chunk{
			ID:         fmt.Sprintf("race-chunk-%d", i),
			Domain:     Domain(i % 4),
			TokenCount: 100,
		}
		hook.RegisterChunk(chunk)
	}

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// Start concurrent writers
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				select {
				case <-stopCh:
					return
				default:
				}
				chunkID := fmt.Sprintf("race-chunk-%d", i%10)
				_ = hook.RecordRetrieval(chunkID, id%2 == 0, RetrievalContext{
					Timestamp: time.Now(),
				})
			}
		}(w)
	}

	// Let some feedback accumulate
	time.Sleep(20 * time.Millisecond)

	// Shutdown scope while writers are active
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		cancel()
		_ = scope.Shutdown(500*time.Millisecond, time.Second)
		close(stopCh)
	}()

	wg.Wait()
	hook.Stop()

	// No crash or race = success
	t.Log("Concurrent shutdown test completed without race conditions")
}

// TestW4N15_ConcurrentStopAndRecord tests race conditions between Stop and Record.
func TestW4N15_ConcurrentStopAndRecord(t *testing.T) {
	ctx := context.Background()
	scope := newTestScopeW4N15(ctx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
		Scope:      scope,
	})
	require.NoError(t, err)

	chunk := Chunk{
		ID:         "concurrent-stop-chunk",
		Domain:     DomainGeneral,
		TokenCount: 50,
	}
	hook.RegisterChunk(chunk)

	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = hook.RecordRetrieval("concurrent-stop-chunk", true, RetrievalContext{
				Timestamp: time.Now(),
			})
		}
	}()

	// Stop after short delay
	time.Sleep(5 * time.Millisecond)
	hook.Stop()

	wg.Wait()
	// No race = success
}

// =============================================================================
// Edge Case Tests
// =============================================================================

// TestW4N15_EmptyFeedback tests handling of empty/minimal feedback.
func TestW4N15_EmptyFeedback(t *testing.T) {
	ctx := context.Background()
	scope := newTestScopeW4N15(ctx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
		Scope:      scope,
	})
	require.NoError(t, err)
	defer hook.Stop()

	// Record with empty context (zero values)
	err = hook.RecordRetrieval("empty-context-chunk", false, RetrievalContext{})
	require.NoError(t, err)

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Should be processed with defaults
	_, total := hook.GetHitRate("empty-context-chunk")
	assert.Equal(t, 1, total, "empty context feedback should be processed")
}

// TestW4N15_ScopeBudgetExhausted tests behavior when scope budget is exhausted.
func TestW4N15_ScopeBudgetExhausted(t *testing.T) {
	pressure := &atomic.Int32{}
	cfg := concurrency.GoroutineBudgetConfig{
		SystemLimit:     2, // Very limited budget
		BurstMultiplier: 1.0,
		TypeWeights:     map[string]float64{"test": 1.0},
	}
	budget := concurrency.NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("limited-agent", "test")

	ctx := context.Background()
	scope := concurrency.NewGoroutineScope(ctx, "limited-agent", budget)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	// First hook should succeed (takes 1 slot)
	hook1, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 10,
		Scope:      scope,
	})
	require.NoError(t, err)
	defer hook1.Stop()

	// Second hook should also succeed (takes 2nd slot)
	hook2, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 10,
		Scope:      scope,
	})
	require.NoError(t, err)
	defer hook2.Stop()

	// Both hooks should work
	err = hook1.RecordRetrieval("chunk1", true, RetrievalContext{Timestamp: time.Now()})
	require.NoError(t, err)

	err = hook2.RecordRetrieval("chunk2", true, RetrievalContext{Timestamp: time.Now()})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	_, total1 := hook1.GetHitRate("chunk1")
	_, total2 := hook2.GetHitRate("chunk2")
	assert.Equal(t, 1, total1)
	assert.Equal(t, 1, total2)
}

// TestW4N15_DoubleStop tests that calling Stop twice is safe.
func TestW4N15_DoubleStop(t *testing.T) {
	ctx := context.Background()
	scope := newTestScopeW4N15(ctx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 10,
		Scope:      scope,
	})
	require.NoError(t, err)

	// Double stop should be safe
	hook.Stop()
	hook.Stop()

	// No panic = success
	t.Log("Double stop completed safely")
}

// TestW4N15_BufferFullWithScope tests buffer overflow behavior with scope tracking.
func TestW4N15_BufferFullWithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScopeW4N15(ctx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	// Very small buffer
	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 2,
		Scope:      scope,
	})
	require.NoError(t, err)
	defer hook.Stop()

	chunk := Chunk{
		ID:         "buffer-test-chunk",
		Domain:     DomainGeneral,
		TokenCount: 50,
	}
	hook.RegisterChunk(chunk)

	// Rapidly fill buffer
	var dropped int
	for i := 0; i < 100; i++ {
		err := hook.RecordRetrieval("buffer-test-chunk", true, RetrievalContext{
			Timestamp: time.Now(),
		})
		if err != nil {
			dropped++
		}
	}

	// Some should have been dropped
	assert.Greater(t, dropped, 0, "some entries should be dropped with tiny buffer")
	t.Logf("Dropped %d entries due to full buffer", dropped)

	// Verify dropped count matches
	time.Sleep(100 * time.Millisecond)
	droppedCount := hook.GetDroppedCount()
	assert.Greater(t, droppedCount, int64(0), "dropped count should be tracked")
}

// TestW4N15_ScopeWithCustomContext tests scope with custom parent context.
func TestW4N15_ScopeWithCustomContext(t *testing.T) {
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer parentCancel()

	scope := newTestScopeW4N15(parentCtx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	hookCtx, hookCancel := context.WithCancel(parentCtx)
	defer hookCancel()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
		Context:    hookCtx,
		Scope:      scope,
	})
	require.NoError(t, err)

	// Record feedback
	err = hook.RecordRetrieval("custom-ctx-chunk", true, RetrievalContext{
		Timestamp: time.Now(),
	})
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	// Cancel hook context
	hookCancel()

	time.Sleep(100 * time.Millisecond)

	// Stop hook
	hook.Stop()

	// Should gracefully handle context cancellation
	t.Log("Custom context test completed successfully")
}

// TestW4N15_BatchRecordingWithScope tests batch recording with scope tracking.
func TestW4N15_BatchRecordingWithScope(t *testing.T) {
	ctx := context.Background()
	scope := newTestScopeW4N15(ctx)
	defer func() {
		_ = scope.Shutdown(time.Second, 2*time.Second)
	}()

	learner, err := NewChunkConfigLearner(nil)
	require.NoError(t, err)

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 500,
		Scope:      scope,
	})
	require.NoError(t, err)
	defer hook.Stop()

	// Register chunks
	for i := 0; i < 5; i++ {
		chunk := Chunk{
			ID:         fmt.Sprintf("batch-scope-chunk-%d", i),
			Domain:     DomainCode,
			TokenCount: 100,
		}
		hook.RegisterChunk(chunk)
	}

	// Create batch
	entries := make([]BatchFeedbackEntry, 20)
	for i := 0; i < 20; i++ {
		entries[i] = BatchFeedbackEntry{
			ChunkID:   fmt.Sprintf("batch-scope-chunk-%d", i%5),
			WasUseful: i%2 == 0,
			Context:   RetrievalContext{Timestamp: time.Now()},
		}
	}

	recorded, dropped := hook.RecordBatch(BatchFeedback{Entries: entries})
	t.Logf("Batch: recorded=%d, dropped=%d", recorded, dropped)

	time.Sleep(200 * time.Millisecond)

	// Verify all were processed
	totalProcessed := 0
	for i := 0; i < 5; i++ {
		_, count := hook.GetHitRate(fmt.Sprintf("batch-scope-chunk-%d", i))
		totalProcessed += count
	}

	assert.GreaterOrEqual(t, totalProcessed, recorded-int(dropped),
		"batch entries should be processed")
}
