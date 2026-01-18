package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestBudget() *GoroutineBudget {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)
	budget.RegisterAgent("test-agent", "engineer")
	return budget
}

func TestGoroutineScope_GoSpawnsAndCompletes(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	executed := make(chan struct{})
	err := scope.Go("test-worker", time.Second, func(ctx context.Context) error {
		close(executed)
		return nil
	})
	require.NoError(t, err)

	select {
	case <-executed:
	case <-time.After(time.Second):
		t.Fatal("worker did not execute")
	}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, scope.WorkerCount())
}

func TestGoroutineScope_GoRespectsTimeout(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	started := make(chan struct{})
	err := scope.Go("timeout-worker", 100*time.Millisecond, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	require.NoError(t, err)

	<-started
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 0, scope.WorkerCount())
}

func TestGoroutineScope_GoBlocksOnBudget(t *testing.T) {
	pressure := &atomic.Int32{}
	cfg := GoroutineBudgetConfig{
		SystemLimit:     15,
		BurstMultiplier: 1.0,
		TypeWeights:     map[string]float64{"test": 1.0},
	}
	budget := NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("test-agent", "test")

	ctx := context.Background()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	_, _, _, hardLimit, _ := budget.GetStats("test-agent")

	blocker := make(chan struct{})
	for i := int64(0); i < hardLimit; i++ {
		err := scope.Go("blocker", time.Minute, func(ctx context.Context) error {
			<-blocker
			return nil
		})
		require.NoError(t, err)
	}

	spawned := make(chan struct{})
	go func() {
		_ = scope.Go("blocked", time.Minute, func(ctx context.Context) error {
			return nil
		})
		close(spawned)
	}()

	select {
	case <-spawned:
		t.Fatal("should have blocked")
	case <-time.After(50 * time.Millisecond):
	}

	close(blocker)

	select {
	case <-spawned:
	case <-time.After(time.Second):
		t.Fatal("should have unblocked")
	}

	_ = scope.Shutdown(time.Second, 2*time.Second)
}

func TestGoroutineScope_ShutdownWaitsForWorkers(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	done := make(chan struct{})
	_ = scope.Go("worker", time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		close(done)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	err := scope.Shutdown(time.Second, 2*time.Second)
	require.NoError(t, err)

	select {
	case <-done:
	default:
		t.Fatal("worker should have been notified")
	}
}

func TestGoroutineScope_ShutdownForceCancelsAfterGrace(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	_ = scope.Go("stubborn", time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})

	start := time.Now()
	err := scope.Shutdown(100*time.Millisecond, 500*time.Millisecond)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 300*time.Millisecond)
}

func TestGoroutineScope_ShutdownReturnsLeakError(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	blocker := make(chan struct{})
	_ = scope.Go("leaker", time.Minute, func(ctx context.Context) error {
		<-blocker
		return nil
	})

	err := scope.Shutdown(50*time.Millisecond, 100*time.Millisecond)
	close(blocker)

	require.Error(t, err)
	var leakErr *GoroutineLeakError
	require.True(t, errors.As(err, &leakErr))
	assert.Equal(t, 1, leakErr.LeakedCount)
	assert.Equal(t, "test-agent", leakErr.AgentID)
}

func TestGoroutineScope_PanicRecovery(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	done := make(chan struct{})
	_ = scope.Go("panicker", time.Second, func(ctx context.Context) error {
		defer close(done)
		panic("test panic")
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not complete after panic")
	}

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 0, scope.WorkerCount())
}

func TestGoroutineScope_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	cancelled := make(chan struct{})
	_ = scope.Go("worker", time.Minute, func(ctx context.Context) error {
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	})

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("worker did not receive cancellation")
	}
}

func TestGoroutineScope_RejectsAfterShutdown(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	_ = scope.Shutdown(time.Millisecond, time.Millisecond)

	err := scope.Go("rejected", time.Second, func(ctx context.Context) error {
		return nil
	})
	assert.ErrorIs(t, err, ErrScopeShutdown)
}

func TestGoroutineScope_ConcurrentGo(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	var wg sync.WaitGroup
	var count atomic.Int32

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := scope.Go("concurrent", time.Second, func(ctx context.Context) error {
				count.Add(1)
				time.Sleep(10 * time.Millisecond)
				return nil
			})
			if err != nil && !errors.Is(err, ErrScopeShutdown) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	assert.GreaterOrEqual(t, count.Load(), int32(1))
	_ = scope.Shutdown(time.Second, 2*time.Second)
}

func TestGoroutineScope_MaxLifetimeEnforced(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)
	scope.SetMaxLifetime(100 * time.Millisecond)

	started := make(chan struct{})
	_ = scope.Go("worker", time.Hour, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	})

	<-started
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 0, scope.WorkerCount())
}

func TestGoroutineScope_MultipleShutdownsAreSafe(t *testing.T) {
	ctx := context.Background()
	budget := newTestBudget()
	scope := NewGoroutineScope(ctx, "test-agent", budget)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = scope.Shutdown(time.Millisecond, time.Millisecond)
		}()
	}
	wg.Wait()
}

func TestGoroutineLeakError_Error(t *testing.T) {
	err := &GoroutineLeakError{
		AgentID:     "agent-1",
		LeakedCount: 2,
		Workers:     []string{"w1", "w2"},
	}

	msg := err.Error()
	assert.Contains(t, msg, "CRITICAL")
	assert.Contains(t, msg, "2 goroutines leaked")
	assert.Contains(t, msg, "agent-1")
}
