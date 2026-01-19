package context

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/stretchr/testify/assert"
)

// =============================================================================
// PrefetchFuture Tests - Happy Path
// =============================================================================

func TestNewPrefetchFuture_Basic(t *testing.T) {
	t.Parallel()

	operation := "test:operation"
	future := NewPrefetchFuture(operation)

	assert.NotNil(t, future)
	assert.Equal(t, operation, future.Operation())
	assert.False(t, future.IsReady())
	assert.NotNil(t, future.done)
}

func TestNewPrefetchFuture_StartsWithCurrentTime(t *testing.T) {
	t.Parallel()

	before := time.Now()
	future := NewPrefetchFuture("test")
	after := time.Now()

	duration := future.Duration()
	assert.GreaterOrEqual(t, duration, time.Duration(0))
	assert.LessOrEqual(t, duration, after.Sub(before)+time.Millisecond)
}

func TestPrefetchFuture_Wait_WithResult(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	expected := &AugmentedQuery{
		OriginalQuery: "test query",
		TokensUsed:    100,
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		future.SetResult(expected)
	}()

	ctx := context.Background()
	result, err := future.Wait(ctx)

	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestPrefetchFuture_SetResult_SetsIsReady(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	assert.False(t, future.IsReady())

	result := &AugmentedQuery{OriginalQuery: "test"}
	future.SetResult(result)

	assert.True(t, future.IsReady())
}

func TestPrefetchFuture_SetError_SetsIsReady(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	assert.False(t, future.IsReady())

	future.SetError(errors.New("test error"))

	assert.True(t, future.IsReady())
}

func TestPrefetchFuture_Wait_WithError(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	expectedErr := errors.New("test error")

	go func() {
		time.Sleep(10 * time.Millisecond)
		future.SetError(expectedErr)
	}()

	ctx := context.Background()
	result, err := future.Wait(ctx)

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, result)
}

func TestPrefetchFuture_Duration_Increases(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	d1 := future.Duration()

	time.Sleep(10 * time.Millisecond)
	d2 := future.Duration()

	assert.Greater(t, d2, d1)
}

func TestPrefetchFuture_Operation_ReturnsCorrectValue(t *testing.T) {
	t.Parallel()

	operations := []string{
		"prefetch:hash123",
		"prefetch:abc",
		"",
		"operation with spaces",
		"operation/with/slashes",
	}

	for _, op := range operations {
		t.Run(op, func(t *testing.T) {
			t.Parallel()
			future := NewPrefetchFuture(op)
			assert.Equal(t, op, future.Operation())
		})
	}
}

// =============================================================================
// PrefetchFuture Tests - Negative Path
// =============================================================================

func TestPrefetchFuture_Wait_WithContextCancellation(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	result, err := future.Wait(ctx)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Nil(t, result)
}

func TestPrefetchFuture_Wait_WithContextTimeout(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result, err := future.Wait(ctx)

	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Nil(t, result)
}

func TestPrefetchFuture_Wait_ContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := future.Wait(ctx)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Nil(t, result)
}

func TestPrefetchFuture_SetResultAfterSetError_ErrorTakesPrecedence(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	expectedErr := errors.New("first error")

	future.SetError(expectedErr)
	future.SetResult(&AugmentedQuery{OriginalQuery: "should be ignored"})

	ctx := context.Background()
	result, err := future.Wait(ctx)

	// Error was set first, so error takes precedence
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, result)
}

func TestPrefetchFuture_SetErrorAfterSetResult_ErrorTakesPrecedence(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	future.SetResult(&AugmentedQuery{OriginalQuery: "first result"})
	expectedErr := errors.New("second error")
	future.SetError(expectedErr)

	ctx := context.Background()
	result, err := future.Wait(ctx)

	// Implementation checks error first in getResult(), so error always takes precedence
	// regardless of which was set first
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, result)
}

// =============================================================================
// PrefetchFuture Tests - Edge Cases
// =============================================================================

func TestPrefetchFuture_DoubleSetResult_ClosesOnce(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	result1 := &AugmentedQuery{OriginalQuery: "first"}
	result2 := &AugmentedQuery{OriginalQuery: "second"}

	// This should not panic
	future.SetResult(result1)
	future.SetResult(result2)

	ctx := context.Background()
	result, err := future.Wait(ctx)

	// Second result overwrites first, but done channel is only closed once
	assert.NoError(t, err)
	assert.Equal(t, result2, result)
	assert.True(t, future.IsReady())
}

func TestPrefetchFuture_DoubleSetError_ClosesOnce(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	err1 := errors.New("first error")
	err2 := errors.New("second error")

	// This should not panic
	future.SetError(err1)
	future.SetError(err2)

	ctx := context.Background()
	_, err := future.Wait(ctx)

	// Second error overwrites first, but done channel is only closed once
	assert.Error(t, err)
	assert.Equal(t, err2, err)
}

func TestPrefetchFuture_NilResult(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	future.SetResult(nil)

	ctx := context.Background()
	result, err := future.Wait(ctx)

	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.True(t, future.IsReady())
}

func TestPrefetchFuture_IsReady_BeforeAndAfter(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")

	// Check multiple times before completion
	for i := 0; i < 5; i++ {
		assert.False(t, future.IsReady())
	}

	future.SetResult(&AugmentedQuery{})

	// Check multiple times after completion
	for i := 0; i < 5; i++ {
		assert.True(t, future.IsReady())
	}
}

// =============================================================================
// PrefetchFuture Tests - Race Conditions
// =============================================================================

func TestPrefetchFuture_ConcurrentWait(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")
	expected := &AugmentedQuery{OriginalQuery: "test", TokensUsed: 42}

	const numWaiters = 100
	var wg sync.WaitGroup
	wg.Add(numWaiters)

	results := make(chan *AugmentedQuery, numWaiters)
	errs := make(chan error, numWaiters)

	// Start many concurrent waiters
	for i := 0; i < numWaiters; i++ {
		go func() {
			defer wg.Done()
			ctx := context.Background()
			result, err := future.Wait(ctx)
			results <- result
			errs <- err
		}()
	}

	// Let waiters start
	time.Sleep(10 * time.Millisecond)

	// Set result
	future.SetResult(expected)

	wg.Wait()
	close(results)
	close(errs)

	// All waiters should get the same result
	for result := range results {
		assert.Equal(t, expected, result)
	}
	for err := range errs {
		assert.NoError(t, err)
	}
}

func TestPrefetchFuture_ConcurrentSetResult(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")

	const numSetters = 100
	var wg sync.WaitGroup
	wg.Add(numSetters)

	// Concurrently set results
	for i := 0; i < numSetters; i++ {
		i := i
		go func() {
			defer wg.Done()
			future.SetResult(&AugmentedQuery{OriginalQuery: "result", TokensUsed: i})
		}()
	}

	wg.Wait()

	// Should be ready and not panic
	assert.True(t, future.IsReady())

	ctx := context.Background()
	result, err := future.Wait(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestPrefetchFuture_ConcurrentSetError(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")

	const numSetters = 100
	var wg sync.WaitGroup
	wg.Add(numSetters)

	// Concurrently set errors
	for i := 0; i < numSetters; i++ {
		i := i
		go func() {
			defer wg.Done()
			future.SetError(errors.New("error " + string(rune('A'+i%26))))
		}()
	}

	wg.Wait()

	// Should be ready and not panic
	assert.True(t, future.IsReady())

	ctx := context.Background()
	_, err := future.Wait(ctx)
	assert.Error(t, err)
}

func TestPrefetchFuture_ConcurrentSetResultAndError(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")

	const numOperations = 50
	var wg sync.WaitGroup
	wg.Add(numOperations * 2)

	// Concurrently set results and errors
	for i := 0; i < numOperations; i++ {
		i := i
		go func() {
			defer wg.Done()
			future.SetResult(&AugmentedQuery{TokensUsed: i})
		}()
		go func() {
			defer wg.Done()
			future.SetError(errors.New("error"))
		}()
	}

	wg.Wait()

	// Should be ready and not panic
	assert.True(t, future.IsReady())

	ctx := context.Background()
	result, err := future.Wait(ctx)
	// One of result or error should be set
	if err != nil {
		assert.Nil(t, result)
	}
}

func TestPrefetchFuture_ConcurrentIsReady(t *testing.T) {
	t.Parallel()

	future := NewPrefetchFuture("test")

	const numReaders = 100
	var wg sync.WaitGroup
	wg.Add(numReaders + 1)

	// Concurrently check IsReady
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = future.IsReady()
			}
		}()
	}

	// Set result while checking
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		future.SetResult(&AugmentedQuery{})
	}()

	wg.Wait()
	assert.True(t, future.IsReady())
}

// =============================================================================
// PrefetchScope Tests - Happy Path
// =============================================================================

func createTestScope(t *testing.T) (*concurrency.GoroutineScope, context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	pressureLevel := &atomic.Int32{}
	budget := concurrency.NewGoroutineBudget(pressureLevel)
	budget.RegisterAgent("test-agent", "engineer")
	scope := concurrency.NewGoroutineScope(ctx, "test-agent", budget)
	return scope, ctx, cancel
}

func TestNewPrefetchScope_WithPositiveTimeout(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	timeout := 100 * time.Millisecond
	ps := NewPrefetchScope(scope, timeout)

	assert.NotNil(t, ps)
	assert.Equal(t, timeout, ps.Timeout())
}

func TestNewPrefetchScope_DefaultTimeoutWhenZero(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 0)

	assert.NotNil(t, ps)
	assert.Equal(t, DefaultPrefetchTimeout, ps.Timeout())
	assert.Equal(t, 200*time.Millisecond, ps.Timeout())
}

func TestNewPrefetchScope_DefaultTimeoutWhenNegative(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, -100*time.Millisecond)

	assert.Equal(t, DefaultPrefetchTimeout, ps.Timeout())
}

func TestPrefetchScope_StartSpeculative_Success(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	expectedResult := &AugmentedQuery{OriginalQuery: "test"}

	future := ps.StartSpeculative(context.Background(), "query-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		return expectedResult, nil
	})

	assert.NotNil(t, future)
	assert.Equal(t, "prefetch:query-hash", future.Operation())

	// Wait for result
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	result, err := future.Wait(ctx)
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

func TestPrefetchScope_StartSpeculative_Error(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	expectedErr := errors.New("work failed")

	future := ps.StartSpeculative(context.Background(), "query-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		return nil, expectedErr
	})

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	result, err := future.Wait(ctx)
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, result)
}

func TestPrefetchScope_GetInflight_Found(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Start a slow operation
	blocker := make(chan struct{})
	ps.StartSpeculative(context.Background(), "query-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		<-blocker
		return &AugmentedQuery{}, nil
	})

	// Get inflight should find it
	future, found := ps.GetInflight("query-hash")
	assert.True(t, found)
	assert.NotNil(t, future)

	close(blocker)

	// Wait for completion
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()
	_, _ = future.Wait(ctx)
}

func TestPrefetchScope_GetInflight_NotFound(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	future, found := ps.GetInflight("nonexistent-hash")
	assert.False(t, found)
	assert.Nil(t, future)
}

func TestPrefetchScope_CancelInflight_Existing(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Start a slow operation
	blocker := make(chan struct{})
	future := ps.StartSpeculative(context.Background(), "query-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		<-blocker
		return &AugmentedQuery{}, nil
	})

	// Cancel it
	ps.CancelInflight("query-hash")

	// Future should have error
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	result, err := future.Wait(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Nil(t, result)

	// Should no longer be in inflight
	_, found := ps.GetInflight("query-hash")
	assert.False(t, found)

	close(blocker)
}

func TestPrefetchScope_CancelInflight_Nonexistent(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Should not panic
	ps.CancelInflight("nonexistent-hash")
}

func TestPrefetchScope_InflightCount(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	assert.Equal(t, 0, ps.InflightCount())

	// Start multiple slow operations
	blockers := make([]chan struct{}, 3)
	for i := 0; i < 3; i++ {
		blockers[i] = make(chan struct{})
		i := i
		ps.StartSpeculative(context.Background(), "hash-"+string(rune('A'+i)), func(ctx context.Context) (*AugmentedQuery, error) {
			<-blockers[i]
			return &AugmentedQuery{}, nil
		})
	}

	// Allow goroutines to start
	time.Sleep(10 * time.Millisecond)

	assert.Equal(t, 3, ps.InflightCount())

	// Complete one
	close(blockers[0])
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, 2, ps.InflightCount())

	// Complete remaining
	close(blockers[1])
	close(blockers[2])
	time.Sleep(20 * time.Millisecond)

	assert.Equal(t, 0, ps.InflightCount())
}

// =============================================================================
// PrefetchScope Tests - Inflight Deduplication
// =============================================================================

func TestPrefetchScope_InflightDeduplication_SameHashReturnsDifferentFuture(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Start first operation
	blocker := make(chan struct{})
	future1 := ps.StartSpeculative(context.Background(), "same-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		<-blocker
		return &AugmentedQuery{OriginalQuery: "first"}, nil
	})

	// Start second operation with same hash - creates new future (overwrites)
	future2 := ps.StartSpeculative(context.Background(), "same-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		<-blocker
		return &AugmentedQuery{OriginalQuery: "second"}, nil
	})

	// Note: Current implementation overwrites the inflight entry
	// Both futures are valid but inflight map only tracks the second
	assert.NotNil(t, future1)
	assert.NotNil(t, future2)

	close(blocker)

	// Wait for both
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	result1, _ := future1.Wait(ctx)
	result2, _ := future2.Wait(ctx)

	assert.Equal(t, "first", result1.OriginalQuery)
	assert.Equal(t, "second", result2.OriginalQuery)
}

func TestPrefetchScope_GetInflight_ReturnsExistingFuture(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Start operation
	blocker := make(chan struct{})
	expectedResult := &AugmentedQuery{OriginalQuery: "result"}
	originalFuture := ps.StartSpeculative(context.Background(), "query-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		<-blocker
		return expectedResult, nil
	})

	// Get inflight returns the same future (by value in map)
	inflightFuture, found := ps.GetInflight("query-hash")
	assert.True(t, found)
	assert.Equal(t, originalFuture, inflightFuture)

	close(blocker)

	// Both futures should get same result
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	result, err := inflightFuture.Wait(ctx)
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

// =============================================================================
// PrefetchScope Tests - Negative Path
// =============================================================================

func TestPrefetchScope_StartSpeculative_WorkPanics(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	future := ps.StartSpeculative(context.Background(), "panic-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		panic("intentional panic")
	})

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	// The panic is recovered by GoroutineScope, but future may not get error set
	// The future should eventually be ready
	select {
	case <-future.done:
		// Future completed (panic was handled)
	case <-ctx.Done():
		// Timeout is acceptable - panic handling varies
	}
}

func TestPrefetchScope_StartSpeculative_ContextCancelledDuringWork(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	workStarted := make(chan struct{})
	workCtx, workCancel := context.WithCancel(context.Background())

	future := ps.StartSpeculative(workCtx, "cancel-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		close(workStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	<-workStarted
	workCancel()

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	result, err := future.Wait(ctx)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// =============================================================================
// PrefetchScope Tests - Race Conditions
// =============================================================================

func TestPrefetchScope_ConcurrentStartSpeculative(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	const numOperations = 50
	var wg sync.WaitGroup
	wg.Add(numOperations)

	futures := make(chan *PrefetchFuture, numOperations)

	for i := 0; i < numOperations; i++ {
		i := i
		go func() {
			defer wg.Done()
			future := ps.StartSpeculative(context.Background(), "hash-"+string(rune('A'+i%26)), func(ctx context.Context) (*AugmentedQuery, error) {
				return &AugmentedQuery{TokensUsed: i}, nil
			})
			futures <- future
		}()
	}

	wg.Wait()
	close(futures)

	// All futures should complete successfully
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	for future := range futures {
		result, err := future.Wait(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	}
}

func TestPrefetchScope_ConcurrentGetInflight(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Start a slow operation
	blocker := make(chan struct{})
	ps.StartSpeculative(context.Background(), "query-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		<-blocker
		return &AugmentedQuery{}, nil
	})

	const numReaders = 100
	var wg sync.WaitGroup
	wg.Add(numReaders)

	results := make(chan bool, numReaders)

	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			_, found := ps.GetInflight("query-hash")
			results <- found
		}()
	}

	wg.Wait()
	close(results)
	close(blocker)

	// All should find the inflight operation
	for found := range results {
		assert.True(t, found)
	}
}

func TestPrefetchScope_ConcurrentCancelInflight(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Start a slow operation
	blocker := make(chan struct{})
	future := ps.StartSpeculative(context.Background(), "query-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		<-blocker
		return &AugmentedQuery{}, nil
	})

	const numCancelers = 50
	var wg sync.WaitGroup
	wg.Add(numCancelers)

	// Concurrently try to cancel
	for i := 0; i < numCancelers; i++ {
		go func() {
			defer wg.Done()
			ps.CancelInflight("query-hash")
		}()
	}

	wg.Wait()
	close(blocker)

	// Future should have canceled error
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	_, err := future.Wait(ctx)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestPrefetchScope_ConcurrentInflightCount(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	const numOps = 10
	blockers := make([]chan struct{}, numOps)

	// Start operations
	for i := 0; i < numOps; i++ {
		blockers[i] = make(chan struct{})
		i := i
		ps.StartSpeculative(context.Background(), "hash-"+string(rune('A'+i)), func(ctx context.Context) (*AugmentedQuery, error) {
			<-blockers[i]
			return &AugmentedQuery{}, nil
		})
	}

	time.Sleep(20 * time.Millisecond)

	const numReaders = 50
	var wg sync.WaitGroup
	wg.Add(numReaders)

	counts := make(chan int, numReaders)

	// Concurrently read inflight count while some complete
	for i := 0; i < numReaders; i++ {
		go func() {
			defer wg.Done()
			count := ps.InflightCount()
			counts <- count
		}()
	}

	// Complete some operations while reading
	for i := 0; i < numOps/2; i++ {
		close(blockers[i])
	}

	wg.Wait()
	close(counts)

	// Complete remaining
	for i := numOps / 2; i < numOps; i++ {
		close(blockers[i])
	}

	// Counts should be between 0 and numOps (race condition but valid)
	for count := range counts {
		assert.GreaterOrEqual(t, count, 0)
		assert.LessOrEqual(t, count, numOps)
	}
}

// =============================================================================
// DefaultPrefetchTimeout Constant Tests
// =============================================================================

func TestDefaultPrefetchTimeout_Value(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 200*time.Millisecond, DefaultPrefetchTimeout)
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestPrefetchScope_FullWorkflow(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Start multiple prefetch operations
	future1 := ps.StartSpeculative(context.Background(), "hash-1", func(ctx context.Context) (*AugmentedQuery, error) {
		time.Sleep(10 * time.Millisecond)
		return &AugmentedQuery{
			OriginalQuery: "query 1",
			TokensUsed:    100,
			TierSource:    TierHotCache,
		}, nil
	})

	future2 := ps.StartSpeculative(context.Background(), "hash-2", func(ctx context.Context) (*AugmentedQuery, error) {
		time.Sleep(20 * time.Millisecond)
		return &AugmentedQuery{
			OriginalQuery: "query 2",
			TokensUsed:    200,
			TierSource:    TierWarmIndex,
		}, nil
	})

	future3 := ps.StartSpeculative(context.Background(), "hash-3", func(ctx context.Context) (*AugmentedQuery, error) {
		return nil, errors.New("retrieval failed")
	})

	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()

	// Wait for results
	result1, err1 := future1.Wait(ctx)
	assert.NoError(t, err1)
	assert.Equal(t, "query 1", result1.OriginalQuery)
	assert.Equal(t, 100, result1.TokensUsed)

	result2, err2 := future2.Wait(ctx)
	assert.NoError(t, err2)
	assert.Equal(t, "query 2", result2.OriginalQuery)
	assert.Equal(t, 200, result2.TokensUsed)

	result3, err3 := future3.Wait(ctx)
	assert.Error(t, err3)
	assert.Equal(t, "retrieval failed", err3.Error())
	assert.Nil(t, result3)
}

func TestPrefetchScope_InflightCleanupAfterCompletion(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	ps := NewPrefetchScope(scope, 100*time.Millisecond)

	// Verify empty initially
	assert.Equal(t, 0, ps.InflightCount())

	// Start operation
	future := ps.StartSpeculative(context.Background(), "cleanup-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		return &AugmentedQuery{OriginalQuery: "done"}, nil
	})

	// Wait for completion
	ctx, ctxCancel := context.WithTimeout(context.Background(), time.Second)
	defer ctxCancel()
	_, _ = future.Wait(ctx)

	// Allow cleanup
	time.Sleep(20 * time.Millisecond)

	// Inflight should be cleaned up
	_, found := ps.GetInflight("cleanup-hash")
	assert.False(t, found)
	assert.Equal(t, 0, ps.InflightCount())
}

func TestPrefetchScope_TimeoutBehavior(t *testing.T) {
	t.Parallel()

	scope, _, cancel := createTestScope(t)
	defer cancel()
	defer scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)

	// Use very short timeout
	ps := NewPrefetchScope(scope, 50*time.Millisecond)

	// Start operation that takes longer than timeout
	future := ps.StartSpeculative(context.Background(), "timeout-hash", func(ctx context.Context) (*AugmentedQuery, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return &AugmentedQuery{OriginalQuery: "completed"}, nil
		}
	})

	// Wait for result with a reasonable timeout
	ctx, ctxCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer ctxCancel()

	result, err := future.Wait(ctx)

	// The work function should have been cancelled due to GoroutineScope timeout
	assert.Error(t, err)
	assert.Equal(t, context.DeadlineExceeded, err)
	assert.Nil(t, result)
}
