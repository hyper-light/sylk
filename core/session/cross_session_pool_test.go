package session

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrossSessionPool_Acquire(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()

	ctx := context.Background()

	handle, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)
	require.NotNil(t, handle)

	assert.Equal(t, "session-1", handle.SessionID)
	assert.Equal(t, PriorityPipeline, handle.Priority)
	assert.Equal(t, 1, pool.GetTotalAllocated())
	assert.Equal(t, 1, pool.GetSessionAllocation("session-1"))

	handle.Release()

	assert.Equal(t, 0, pool.GetTotalAllocated())
}

func TestCrossSessionPool_FairShareEnforced(t *testing.T) {
	r, cleanup := newPoolTestRegistry(t)
	defer cleanup()

	require.NoError(t, r.Register("session-1"))
	require.NoError(t, r.Register("session-2"))
	require.NoError(t, r.Heartbeat("session-1", 5, 0))
	require.NoError(t, r.Heartbeat("session-2", 5, 0))

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	totals := ResourceTotals{SubprocessSlots: 10}
	allocs, err := calc.Calculate(totals)
	require.NoError(t, err)
	calc.UpdateLastAllocation(allocs)

	cfg := CrossSessionPoolConfig{
		TotalSlots:     10,
		AcquireTimeout: 100 * time.Millisecond,
	}
	pool := NewCrossSessionPool(cfg, calc, nil, r)

	ctx := context.Background()

	var handles1 []*ResourceHandle
	for i := 0; i < allocs["session-1"].SubprocessSlots; i++ {
		h, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
		require.NoError(t, err)
		handles1 = append(handles1, h)
	}

	_, err = pool.Acquire(ctx, "session-1", PriorityPipeline)
	assert.ErrorIs(t, err, ErrAcquisitionTimeout)

	for _, h := range handles1 {
		h.Release()
	}
}

func TestCrossSessionPool_UserPreemption(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()

	ctx := context.Background()

	var handles []*ResourceHandle
	for i := 0; i < 5; i++ {
		h, err := pool.Acquire(ctx, "session-bg", PriorityBackground)
		require.NoError(t, err)
		handles = append(handles, h)
	}

	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		handles[0].Release()
		close(done)
	}()

	userHandle, err := pool.Acquire(ctx, "session-user", PriorityUser)
	require.NoError(t, err)
	require.NotNil(t, userHandle)

	<-done

	userHandle.Release()
	for _, h := range handles[1:] {
		h.Release()
	}
}

func TestCrossSessionPool_AllocationTracking(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()

	ctx := context.Background()

	h1, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)
	h2, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)
	h3, err := pool.Acquire(ctx, "session-2", PriorityPipeline)
	require.NoError(t, err)

	assert.Equal(t, 2, pool.GetSessionAllocation("session-1"))
	assert.Equal(t, 1, pool.GetSessionAllocation("session-2"))
	assert.Equal(t, 3, pool.GetTotalAllocated())

	h1.Release()
	assert.Equal(t, 1, pool.GetSessionAllocation("session-1"))
	assert.Equal(t, 2, pool.GetTotalAllocated())

	h2.Release()
	h3.Release()
	assert.Equal(t, 0, pool.GetSessionAllocation("session-1"))
	assert.Equal(t, 0, pool.GetTotalAllocated())
}

func TestCrossSessionPool_WaitingCount(t *testing.T) {
	cfg := CrossSessionPoolConfig{
		TotalSlots:     1,
		AcquireTimeout: 2 * time.Second,
	}
	calc := NewFairShareCalculator(nil, DefaultFairShareConfig())
	calc.UpdateLastAllocation(map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
		"session-2": {SessionID: "session-2", SubprocessSlots: 10},
	})

	pool := NewCrossSessionPool(cfg, calc, nil, nil)

	ctx := context.Background()

	h, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pool.Acquire(ctx, "session-2", PriorityPipeline)
	}()

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, pool.GetWaitingCount())

	h.Release()

	wg.Wait()
}

func TestCrossSessionPool_ReleaseWakesWaiters(t *testing.T) {
	cfg := CrossSessionPoolConfig{
		TotalSlots:     1,
		AcquireTimeout: 2 * time.Second,
	}
	calc := NewFairShareCalculator(nil, DefaultFairShareConfig())
	calc.UpdateLastAllocation(map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
		"session-2": {SessionID: "session-2", SubprocessSlots: 10},
	})

	pool := NewCrossSessionPool(cfg, calc, nil, nil)

	ctx := context.Background()

	h1, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)

	acquired := make(chan *ResourceHandle, 1)
	go func() {
		h, _ := pool.Acquire(ctx, "session-2", PriorityPipeline)
		acquired <- h
	}()

	time.Sleep(50 * time.Millisecond)
	h1.Release()

	select {
	case h := <-acquired:
		require.NotNil(t, h)
		assert.Equal(t, "session-2", h.SessionID)
		h.Release()
	case <-time.After(1 * time.Second):
		t.Fatal("waiter not woken up")
	}
}

func TestCrossSessionPool_ContextCancellation(t *testing.T) {
	cfg := CrossSessionPoolConfig{
		TotalSlots:     1,
		AcquireTimeout: 5 * time.Second,
	}
	calc := NewFairShareCalculator(nil, DefaultFairShareConfig())
	calc.UpdateLastAllocation(map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
		"session-2": {SessionID: "session-2", SubprocessSlots: 10},
	})

	pool := NewCrossSessionPool(cfg, calc, nil, nil)

	ctx := context.Background()

	h, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)
	defer h.Release()

	ctxCancel, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		_, err := pool.Acquire(ctxCancel, "session-2", PriorityPipeline)
		errChan <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errChan:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(1 * time.Second):
		t.Fatal("acquisition not cancelled")
	}
}

func TestCrossSessionPool_DoubleRelease(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()

	ctx := context.Background()

	h, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)

	h.Release()
	assert.Equal(t, 0, pool.GetTotalAllocated())

	h.Release()
	assert.Equal(t, 0, pool.GetTotalAllocated())
}

func TestCrossSessionPool_ClosedOperations(t *testing.T) {
	pool, cleanup := newTestPool(t)
	cleanup()

	_, err := pool.Acquire(context.Background(), "session-1", PriorityPipeline)
	assert.ErrorIs(t, err, ErrPoolClosed)

	err = pool.Close()
	assert.ErrorIs(t, err, ErrPoolClosed)
}

func TestCrossSessionPool_ConcurrentAccess(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()

	ctx := context.Background()

	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := "session-" + formatInt64(int64(id%3))

			for j := 0; j < 10; j++ {
				h, err := pool.Acquire(ctx, sessionID, PriorityPipeline)
				if err != nil {
					continue
				}
				time.Sleep(time.Millisecond)
				h.Release()
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("concurrent error: %v", err)
	}

	assert.Equal(t, 0, pool.GetTotalAllocated())
}

func TestCrossSessionPool_AvailableSlots(t *testing.T) {
	pool, cleanup := newTestPool(t)
	defer cleanup()

	ctx := context.Background()

	assert.Equal(t, 10, pool.GetAvailableSlots())

	h1, _ := pool.Acquire(ctx, "session-1", PriorityPipeline)
	assert.Equal(t, 9, pool.GetAvailableSlots())

	h2, _ := pool.Acquire(ctx, "session-1", PriorityPipeline)
	assert.Equal(t, 8, pool.GetAvailableSlots())

	h1.Release()
	assert.Equal(t, 9, pool.GetAvailableSlots())

	h2.Release()
	assert.Equal(t, 10, pool.GetAvailableSlots())
}

func TestCrossSessionPool_DefaultConfig(t *testing.T) {
	cfg := DefaultCrossSessionPoolConfig()

	assert.Equal(t, 100, cfg.TotalSlots)
	assert.Equal(t, DefaultPreemptTimeout, cfg.PreemptTimeout)
	assert.Equal(t, DefaultAcquireTimeout, cfg.AcquireTimeout)
}

func TestCrossSessionPool_DoubleClose(t *testing.T) {
	cfg := CrossSessionPoolConfig{
		TotalSlots:     10,
		AcquireTimeout: 100 * time.Millisecond,
	}
	calc := NewFairShareCalculator(nil, DefaultFairShareConfig())
	calc.UpdateLastAllocation(map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
	})

	pool := NewCrossSessionPool(cfg, calc, nil, nil)

	err1 := pool.Close()
	assert.NoError(t, err1, "first close should succeed")

	err2 := pool.Close()
	assert.ErrorIs(t, err2, ErrPoolClosed, "second close should return ErrPoolClosed")
}

func TestCrossSessionPool_ConcurrentClose(t *testing.T) {
	cfg := CrossSessionPoolConfig{
		TotalSlots:     10,
		AcquireTimeout: 100 * time.Millisecond,
	}
	calc := NewFairShareCalculator(nil, DefaultFairShareConfig())
	calc.UpdateLastAllocation(map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
	})

	pool := NewCrossSessionPool(cfg, calc, nil, nil)

	ctx := context.Background()
	_, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)

	var wg sync.WaitGroup
	errChan := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errChan <- pool.Close()
		}()
	}

	wg.Wait()
	close(errChan)

	successCount := 0
	closedErrCount := 0
	for err := range errChan {
		if err == nil {
			successCount++
		} else if errors.Is(err, ErrPoolClosed) {
			closedErrCount++
		}
	}

	assert.Equal(t, 1, successCount, "exactly one close should succeed")
	assert.Equal(t, 9, closedErrCount, "other closes should return ErrPoolClosed")
}

func TestCrossSessionPool_CloseWithWaitingGoroutines(t *testing.T) {
	cfg := CrossSessionPoolConfig{
		TotalSlots:     1,
		AcquireTimeout: 5 * time.Second,
	}
	calc := NewFairShareCalculator(nil, DefaultFairShareConfig())
	calc.UpdateLastAllocation(map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
		"session-2": {SessionID: "session-2", SubprocessSlots: 10},
	})

	pool := NewCrossSessionPool(cfg, calc, nil, nil)

	ctx := context.Background()
	h, err := pool.Acquire(ctx, "session-1", PriorityPipeline)
	require.NoError(t, err)
	_ = h

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pool.Acquire(ctx, "session-2", PriorityPipeline)
	}()

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, pool.GetWaitingCount())

	err1 := pool.Close()
	assert.NoError(t, err1)

	err2 := pool.Close()
	assert.ErrorIs(t, err2, ErrPoolClosed)

	wg.Wait()
}

func newTestPool(t *testing.T) (*CrossSessionPool, func()) {
	t.Helper()

	cfg := CrossSessionPoolConfig{
		TotalSlots:     10,
		AcquireTimeout: 100 * time.Millisecond,
		PreemptTimeout: 100 * time.Millisecond,
	}

	calc := NewFairShareCalculator(nil, DefaultFairShareConfig())
	calc.UpdateLastAllocation(map[string]SessionAllocation{
		"session-1":    {SessionID: "session-1", SubprocessSlots: 10},
		"session-2":    {SessionID: "session-2", SubprocessSlots: 10},
		"session-bg":   {SessionID: "session-bg", SubprocessSlots: 10},
		"session-user": {SessionID: "session-user", SubprocessSlots: 10},
	})

	pool := NewCrossSessionPool(cfg, calc, nil, nil)

	return pool, func() {
		pool.Close()
	}
}

func newPoolTestRegistry(t *testing.T) (*Registry, func()) {
	t.Helper()

	cfg := DefaultRegistryConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "pool_test.db")
	cfg.CleanupInterval = 1 * time.Hour

	r, err := NewRegistry(cfg)
	require.NoError(t, err)

	return r, func() {
		r.Close()
	}
}
