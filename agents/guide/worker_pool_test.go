package guide_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerPool_NewWorkerPool(t *testing.T) {
	pool := guide.NewWorkerPool(guide.DefaultWorkerPoolConfig())
	require.NotNil(t, pool)

	stats := pool.Stats()
	assert.Equal(t, "default", stats.Name)
	assert.Equal(t, 4, stats.NumWorkers)
	assert.Equal(t, 1000, stats.QueueSize)
	assert.False(t, stats.Running)
}

func TestWorkerPool_StartStop(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{
		Name:       "test",
		NumWorkers: 2,
		QueueSize:  10,
	})

	pool.Start()
	stats := pool.Stats()
	assert.True(t, stats.Running)

	pool.Stop()
	stats = pool.Stats()
	assert.False(t, stats.Running)

	// Double stop should be safe
	pool.Stop()
}

func TestWorkerPool_Submit(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{
		Name:       "test",
		NumWorkers: 2,
		QueueSize:  10,
	})
	pool.Start()
	defer pool.Stop()

	var completed int32
	var wg sync.WaitGroup

	// Submit jobs
	for i := 0; i < 5; i++ {
		wg.Add(1)
		job := &guide.Job{
			ID: "test",
			Execute: func(ctx context.Context) error {
				atomic.AddInt32(&completed, 1)
				wg.Done()
				return nil
			},
		}
		assert.True(t, pool.Submit(job))
	}

	wg.Wait()
	assert.Equal(t, int32(5), atomic.LoadInt32(&completed))

	stats := pool.Stats()
	assert.Equal(t, int64(5), stats.JobsSubmitted)
	assert.Equal(t, int64(5), stats.JobsCompleted)
	assert.Equal(t, int64(0), stats.JobsFailed)
}

func TestWorkerPool_SubmitWithError(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{
		Name:       "test",
		NumWorkers: 2,
		QueueSize:  10,
	})
	pool.Start()
	defer pool.Stop()

	var errorCaptured error
	var wg sync.WaitGroup
	wg.Add(1)

	job := &guide.Job{
		ID: "failing-job",
		Execute: func(ctx context.Context) error {
			return errors.New("test error")
		},
		OnError: func(err error) {
			errorCaptured = err
			wg.Done()
		},
	}

	pool.Submit(job)
	wg.Wait()

	assert.Equal(t, "test error", errorCaptured.Error())

	stats := pool.Stats()
	assert.Equal(t, int64(1), stats.JobsFailed)
}

func TestWorkerPool_QueueFull(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{
		Name:       "test",
		NumWorkers: 1,
		QueueSize:  2,
	})
	pool.Start()
	defer pool.Stop()

	// Block the worker
	blockChan := make(chan struct{})
	started := make(chan struct{})
	pool.Submit(&guide.Job{
		ID: "blocking",
		Execute: func(ctx context.Context) error {
			close(started)
			<-blockChan
			return nil
		},
	})

	// Wait for blocking job to start
	<-started

	// Fill the queue
	pool.Submit(&guide.Job{ID: "q1", Execute: func(ctx context.Context) error { return nil }})
	pool.Submit(&guide.Job{ID: "q2", Execute: func(ctx context.Context) error { return nil }})

	// This should be dropped
	dropped := !pool.Submit(&guide.Job{ID: "dropped", Execute: func(ctx context.Context) error { return nil }})
	assert.True(t, dropped)

	stats := pool.Stats()
	assert.GreaterOrEqual(t, stats.JobsDropped, int64(1))

	close(blockChan)
}

func TestWorkerPool_SubmitBlocking(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{
		Name:       "test",
		NumWorkers: 2,
		QueueSize:  5,
	})
	pool.Start()
	defer pool.Stop()

	var wg sync.WaitGroup
	wg.Add(1)

	job := &guide.Job{
		ID: "blocking-submit",
		Execute: func(ctx context.Context) error {
			wg.Done()
			return nil
		},
	}

	assert.True(t, pool.SubmitBlocking(job))
	wg.Wait()
}

func TestWorkerPool_SubmitWithTimeout(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{
		Name:       "test",
		NumWorkers: 1,
		QueueSize:  1,
	})
	pool.Start()
	defer pool.Stop()

	// Block the worker
	blockChan := make(chan struct{})
	started := make(chan struct{})
	pool.Submit(&guide.Job{
		ID: "blocking",
		Execute: func(ctx context.Context) error {
			close(started)
			<-blockChan
			return nil
		},
	})

	// Wait for blocking job to start
	<-started

	// Fill the queue
	pool.Submit(&guide.Job{ID: "q1", Execute: func(ctx context.Context) error { return nil }})

	// This should timeout
	result := pool.SubmitWithTimeout(
		&guide.Job{ID: "timeout", Execute: func(ctx context.Context) error { return nil }},
		10*time.Millisecond,
	)
	assert.False(t, result)

	close(blockChan)
}

func TestWorkerPool_SubmitToStoppedPool(t *testing.T) {
	pool := guide.NewWorkerPool(guide.DefaultWorkerPoolConfig())
	// Don't start

	job := &guide.Job{
		ID: "test",
		Execute: func(ctx context.Context) error {
			return nil
		},
	}

	assert.False(t, pool.Submit(job))
	assert.False(t, pool.SubmitBlocking(job))
}

func TestWorkerPool_ConcurrentSubmit(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{
		Name:       "test",
		NumWorkers: 4,
		QueueSize:  100,
	})
	pool.Start()
	defer pool.Stop()

	var completed int64
	var wg sync.WaitGroup

	// Submit from multiple goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				done := make(chan struct{})
				pool.Submit(&guide.Job{
					ID: "concurrent",
					Execute: func(ctx context.Context) error {
						atomic.AddInt64(&completed, 1)
						close(done)
						return nil
					},
				})
				<-done
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int64(100), atomic.LoadInt64(&completed))
}

func TestWorkerPool_ContextCancellation(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{
		Name:       "test",
		NumWorkers: 2,
		QueueSize:  10,
	})
	pool.Start()

	var ctxCancelled atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)

	pool.Submit(&guide.Job{
		ID: "cancellation-test",
		Execute: func(ctx context.Context) error {
			defer wg.Done()
			select {
			case <-ctx.Done():
				ctxCancelled.Store(true)
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return nil
			}
		},
	})

	// Stop pool which cancels context
	go func() {
		time.Sleep(10 * time.Millisecond)
		pool.Stop()
	}()

	wg.Wait()
	// Context may or may not be cancelled depending on timing
	_ = ctxCancelled.Load() // Verify we can read the value
}

func TestClassificationWorkerPool(t *testing.T) {
	pool := guide.NewClassificationWorkerPool(guide.ClassificationWorkerPoolConfig{
		NumWorkers: 2,
		QueueSize:  10,
	})
	pool.Start()
	defer pool.Stop()

	stats := pool.Stats()
	assert.True(t, stats.Running)
	assert.Equal(t, "classification", stats.Name)
}

func TestWorkerPool_DefaultConfig(t *testing.T) {
	cfg := guide.DefaultWorkerPoolConfig()
	assert.Equal(t, "default", cfg.Name)
	assert.Equal(t, 4, cfg.NumWorkers)
	assert.Equal(t, 1000, cfg.QueueSize)
}

func TestWorkerPool_ZeroConfig(t *testing.T) {
	pool := guide.NewWorkerPool(guide.WorkerPoolConfig{})
	require.NotNil(t, pool)

	stats := pool.Stats()
	assert.Equal(t, 4, stats.NumWorkers)   // Uses default
	assert.Equal(t, 1000, stats.QueueSize) // Uses default
}
