package pool_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/pool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPriorityPool_New(t *testing.T) {
	p := pool.NewPriorityPool(pool.DefaultPriorityPoolConfig())
	require.NotNil(t, p)

	stats := p.Stats()
	assert.Equal(t, "priority-pool", stats.Name)
	assert.Equal(t, 4, stats.NumWorkers)
	assert.False(t, stats.Running)
}

func TestPriorityPool_StartStop(t *testing.T) {
	p := pool.NewPriorityPool(pool.PriorityPoolConfig{
		Name:       "test",
		NumWorkers: 2,
	})

	p.Start()
	stats := p.Stats()
	assert.True(t, stats.Running)

	p.Stop()
	stats = p.Stats()
	assert.False(t, stats.Running)
}

func TestPriorityPool_Submit(t *testing.T) {
	p := pool.NewPriorityPool(pool.PriorityPoolConfig{
		NumWorkers: 2,
	})
	p.Start()
	defer p.Stop()

	var completed int32
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		job := &pool.PriorityJob{
			ID:       "test",
			Priority: pool.PriorityNormal,
			Execute: func(ctx context.Context) error {
				atomic.AddInt32(&completed, 1)
				wg.Done()
				return nil
			},
		}
		assert.True(t, p.Submit(job))
	}

	wg.Wait()
	assert.Equal(t, int32(10), atomic.LoadInt32(&completed))
}

func TestPriorityPool_PriorityOrdering(t *testing.T) {
	p := pool.NewPriorityPool(pool.PriorityPoolConfig{
		NumWorkers:   1, // Single worker for deterministic ordering
		MaxQueueSize: 100,
	})
	p.Start()
	defer p.Stop()

	// Block the worker while we queue jobs
	blockChan := make(chan struct{})
	started := make(chan struct{})
	p.Submit(&pool.PriorityJob{
		ID: "blocker",
		Execute: func(ctx context.Context) error {
			close(started)
			<-blockChan
			return nil
		},
	})
	<-started

	var executionOrder []pool.Priority
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Submit jobs of different priorities while worker is blocked
	priorities := []pool.Priority{
		pool.PriorityLow,
		pool.PriorityNormal,
		pool.PriorityCritical,
		pool.PriorityHigh,
		pool.PriorityBackground,
	}

	for _, prio := range priorities {
		wg.Add(1)
		job := &pool.PriorityJob{
			ID:       "test",
			Priority: prio,
			Execute: func(p pool.Priority) func(ctx context.Context) error {
				return func(ctx context.Context) error {
					mu.Lock()
					executionOrder = append(executionOrder, p)
					mu.Unlock()
					wg.Done()
					return nil
				}
			}(prio),
		}
		require.True(t, p.Submit(job))
	}

	// Release the worker
	close(blockChan)
	wg.Wait()

	// Verify critical was first
	assert.Equal(t, pool.PriorityCritical, executionOrder[0])
}

func TestPriorityPool_QueueFull(t *testing.T) {
	p := pool.NewPriorityPool(pool.PriorityPoolConfig{
		NumWorkers:   1,
		MaxQueueSize: 2,
	})
	p.Start()
	defer p.Stop()

	// Block the worker
	blockChan := make(chan struct{})
	started := make(chan struct{})
	p.Submit(&pool.PriorityJob{
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
	p.Submit(&pool.PriorityJob{ID: "q1", Execute: func(ctx context.Context) error { return nil }})
	p.Submit(&pool.PriorityJob{ID: "q2", Execute: func(ctx context.Context) error { return nil }})

	// This should be dropped
	dropped := !p.Submit(&pool.PriorityJob{ID: "dropped", Execute: func(ctx context.Context) error { return nil }})
	assert.True(t, dropped)

	stats := p.Stats()
	assert.GreaterOrEqual(t, stats.JobsDropped, int64(1))

	close(blockChan)
}

func TestPriorityPool_Errors(t *testing.T) {
	p := pool.NewPriorityPool(pool.PriorityPoolConfig{NumWorkers: 2})
	p.Start()
	defer p.Stop()

	var errorCaptured error
	var wg sync.WaitGroup
	wg.Add(1)

	job := &pool.PriorityJob{
		ID: "failing",
		Execute: func(ctx context.Context) error {
			return errors.New("test error")
		},
		OnError: func(err error) {
			errorCaptured = err
			wg.Done()
		},
	}

	p.Submit(job)
	wg.Wait()

	assert.NotNil(t, errorCaptured)
	assert.Equal(t, "test error", errorCaptured.Error())

	stats := p.Stats()
	assert.Equal(t, int64(1), stats.JobsFailed)
}

func TestPriorityPool_SubmitWithTimeout(t *testing.T) {
	p := pool.NewPriorityPool(pool.PriorityPoolConfig{
		NumWorkers:   1,
		MaxQueueSize: 1,
	})
	p.Start()
	defer p.Stop()

	blockChan := make(chan struct{})
	started := make(chan struct{})
	p.Submit(&pool.PriorityJob{
		ID: "blocking",
		Execute: func(ctx context.Context) error {
			close(started)
			<-blockChan
			return nil
		},
	})

	<-started

	p.Submit(&pool.PriorityJob{ID: "q1", Execute: func(ctx context.Context) error { return nil }})

	result := p.SubmitWithTimeout(
		&pool.PriorityJob{ID: "timeout", Execute: func(ctx context.Context) error { return nil }},
		10*time.Millisecond,
	)
	assert.False(t, result)

	close(blockChan)
}

func TestPriorityPool_LatencyBuckets(t *testing.T) {
	p := pool.NewPriorityPool(pool.PriorityPoolConfig{
		NumWorkers:   1,
		MaxQueueSize: 10,
	})
	p.Start()
	defer p.Stop()

	p.Submit(&pool.PriorityJob{
		ID:        "latency",
		Priority:  pool.PriorityNormal,
		CreatedAt: time.Now().Add(-30 * time.Millisecond),
		Execute: func(ctx context.Context) error {
			time.Sleep(15 * time.Millisecond)
			return nil
		},
	})

	require.Eventually(t, func() bool {
		stats := p.Stats()
		return len(stats.WaitBuckets) > 0 && len(stats.RunBuckets) > 0
	}, time.Second, 5*time.Millisecond)
}

func TestSessionPool_New(t *testing.T) {
	p := pool.NewSessionPool(pool.DefaultSessionPoolConfig())
	require.NotNil(t, p)

	stats := p.Stats()
	assert.Equal(t, "session-pool", stats.Name)
	assert.False(t, stats.Running)
}

func TestSessionPool_Submit(t *testing.T) {
	p := pool.NewSessionPool(pool.SessionPoolConfig{
		NumWorkers: 4,
	})
	p.Start()
	defer p.Stop()

	var completed int32
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		job := &pool.SessionJob{
			ID:        "test",
			SessionID: "session-1",
			AgentType: "worker",
			Execute: func(ctx context.Context) error {
				atomic.AddInt32(&completed, 1)
				wg.Done()
				return nil
			},
		}
		assert.True(t, p.Submit(job))
	}

	wg.Wait()
	assert.Equal(t, int32(10), atomic.LoadInt32(&completed))
}

func TestSessionPool_FairDistribution(t *testing.T) {
	p := pool.NewSessionPool(pool.SessionPoolConfig{
		NumWorkers:        4,
		MaxJobsPerSession: 100,
		MaxTotalJobs:      500,
	})
	p.Start()
	defer p.Stop()

	var session1Count, session2Count int32
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(2)

		p.Submit(&pool.SessionJob{
			SessionID: "session-1",
			AgentType: "worker",
			Execute: func(ctx context.Context) error {
				atomic.AddInt32(&session1Count, 1)
				wg.Done()
				return nil
			},
		})

		p.Submit(&pool.SessionJob{
			SessionID: "session-2",
			AgentType: "worker",
			Execute: func(ctx context.Context) error {
				atomic.AddInt32(&session2Count, 1)
				wg.Done()
				return nil
			},
		})
	}

	wg.Wait()

	assert.Equal(t, int32(20), atomic.LoadInt32(&session1Count))
	assert.Equal(t, int32(20), atomic.LoadInt32(&session2Count))

	stats := p.Stats()
	assert.Equal(t, 2, stats.SessionCount)
}

func TestSessionPool_PerSessionLimit(t *testing.T) {
	p := pool.NewSessionPool(pool.SessionPoolConfig{
		NumWorkers:        1,
		MaxJobsPerSession: 2,
		MaxTotalJobs:      100,
	})
	p.Start()
	defer p.Stop()

	blockChan := make(chan struct{})
	started := make(chan struct{})
	p.Submit(&pool.SessionJob{
		SessionID: "session-1",
		AgentType: "worker",
		Execute: func(ctx context.Context) error {
			close(started)
			<-blockChan
			return nil
		},
	})

	<-started

	p.Submit(&pool.SessionJob{SessionID: "session-1", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})
	p.Submit(&pool.SessionJob{SessionID: "session-1", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})

	dropped := !p.Submit(&pool.SessionJob{SessionID: "session-1", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})
	assert.True(t, dropped)

	success := p.Submit(&pool.SessionJob{SessionID: "session-2", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})
	assert.True(t, success)

	close(blockChan)
}

func TestSessionPool_MaxTotalJobs(t *testing.T) {
	p := pool.NewSessionPool(pool.SessionPoolConfig{
		NumWorkers:        1,
		MaxJobsPerSession: 10,
		MaxTotalJobs:      2,
	})
	p.Start()
	defer p.Stop()

	blockChan := make(chan struct{})
	started := make(chan struct{})

	p.Submit(&pool.SessionJob{
		SessionID: "session-1",
		AgentType: "worker",
		Execute: func(ctx context.Context) error {
			close(started)
			<-blockChan
			return nil
		},
	})

	<-started

	p.Submit(&pool.SessionJob{SessionID: "session-1", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})

	dropped := !p.Submit(&pool.SessionJob{SessionID: "session-2", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})
	assert.True(t, dropped)

	close(blockChan)
}

func TestSessionPool_DynamicLimits(t *testing.T) {
	p := pool.NewSessionPool(pool.SessionPoolConfig{
		NumWorkers:           1,
		MaxJobsPerSession:    1,
		MaxTotalJobs:         2,
		MaxJobsPerSessionMax: 3,
		MaxTotalJobsMax:      6,
		EnableDynamicLimits:  true,
		AdjustInterval:       10 * time.Millisecond,
	})
	p.Start()
	defer p.Stop()

	block := make(chan struct{})
	started := make(chan struct{})

	p.Submit(&pool.SessionJob{
		SessionID: "session-1",
		AgentType: "worker",
		Execute: func(ctx context.Context) error {
			close(started)
			<-block
			return nil
		},
	})

	<-started

	p.Submit(&pool.SessionJob{SessionID: "session-1", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})

	time.Sleep(30 * time.Millisecond)

	stats := p.Stats()
	assert.GreaterOrEqual(t, stats.MaxJobsPerSession, 1)
	assert.GreaterOrEqual(t, stats.MaxTotalJobs, 2)

	close(block)
}

func TestSessionPool_RemoveSession(t *testing.T) {
	p := pool.NewSessionPool(pool.DefaultSessionPoolConfig())
	p.Start()
	defer p.Stop()

	p.Submit(&pool.SessionJob{SessionID: "session-1", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})
	p.Submit(&pool.SessionJob{SessionID: "session-2", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})
	p.Submit(&pool.SessionJob{SessionID: "session-3", AgentType: "worker", Execute: func(ctx context.Context) error { return nil }})

	time.Sleep(50 * time.Millisecond)

	ids := p.SessionIDs()
	assert.Contains(t, ids, "session-1")

	p.RemoveSession("session-1")

	ids = p.SessionIDs()
	assert.NotContains(t, ids, "session-1")
}

func TestSessionPool_AgentLimits(t *testing.T) {
	p := pool.NewSessionPool(pool.SessionPoolConfig{
		NumWorkers:        1,
		MaxJobsPerSession: 10,
		MaxTotalJobs:      10,
		MaxAgents:         map[string]int{"engineer": 1},
	})
	p.Start()
	defer p.Stop()

	block := make(chan struct{})
	started := make(chan struct{})

	p.Submit(&pool.SessionJob{
		SessionID: "session-1",
		AgentType: "engineer",
		Execute: func(ctx context.Context) error {
			close(started)
			<-block
			return nil
		},
	})

	<-started

	dropped := !p.Submit(&pool.SessionJob{
		SessionID: "session-1",
		AgentType: "engineer",
		Execute:   func(ctx context.Context) error { return nil },
	})
	assert.True(t, dropped)

	stats := p.Stats()
	assert.Equal(t, 1, stats.AgentStats["engineer"].Limit)
	assert.Equal(t, 1, stats.AgentStats["engineer"].Running)

	close(block)
}

func TestSessionPool_AgentLimitWithMultipleSessions(t *testing.T) {
	p := pool.NewSessionPool(pool.SessionPoolConfig{
		NumWorkers:        2,
		MaxJobsPerSession: 10,
		MaxTotalJobs:      10,
		MaxAgents:         map[string]int{"engineer": 1},
	})
	p.Start()
	defer p.Stop()

	block := make(chan struct{})
	started := make(chan struct{})

	p.Submit(&pool.SessionJob{
		SessionID: "session-1",
		AgentType: "engineer",
		Execute: func(ctx context.Context) error {
			close(started)
			<-block
			return nil
		},
	})

	<-started

	allowed := p.Submit(&pool.SessionJob{
		SessionID: "session-2",
		AgentType: "engineer",
		Execute:   func(ctx context.Context) error { return nil },
	})
	assert.True(t, allowed)

	stats := p.Stats()
	assert.Equal(t, 1, stats.AgentStats["engineer"].Running)
	assert.GreaterOrEqual(t, stats.AgentStats["engineer"].Queued, 1)

	close(block)
}

func TestFairnessController_New(t *testing.T) {
	fc := pool.NewFairnessController(pool.DefaultFairnessConfig())
	require.NotNil(t, fc)
	defer fc.Close()

	stats := fc.Stats()
	assert.Equal(t, int64(100), stats.GlobalLimit)
	assert.Equal(t, int64(20), stats.EntityLimit)
}

func TestFairnessController_AcquireRelease(t *testing.T) {
	fc := pool.NewFairnessController(pool.FairnessConfig{
		GlobalLimit: 10,
		EntityLimit: 5,
	})
	defer fc.Close()

	// Acquire slots
	for i := 0; i < 5; i++ {
		acquired, err := fc.Acquire("entity-1")
		require.NoError(t, err)
		assert.True(t, acquired)
	}

	// Entity limit reached
	acquired, err := fc.Acquire("entity-1")
	require.NoError(t, err)
	assert.False(t, acquired)

	// Release a slot
	err = fc.Release("entity-1")
	require.NoError(t, err)

	// Should be able to acquire again
	acquired, err = fc.Acquire("entity-1")
	require.NoError(t, err)
	assert.True(t, acquired)
}

func TestFairnessController_GlobalLimit(t *testing.T) {
	fc := pool.NewFairnessController(pool.FairnessConfig{
		GlobalLimit: 3,
		EntityLimit: 10,
	})
	defer fc.Close()

	// Acquire up to global limit across entities
	fc.Acquire("entity-1")
	fc.Acquire("entity-2")
	fc.Acquire("entity-3")

	// Global limit reached
	acquired, _ := fc.Acquire("entity-4")
	assert.False(t, acquired)

	// Release one
	fc.Release("entity-1")

	// Should work now
	acquired, _ = fc.Acquire("entity-4")
	assert.True(t, acquired)
}

func TestFairnessController_SelectFairest(t *testing.T) {
	fc := pool.NewFairnessController(pool.FairnessConfig{
		GlobalLimit: 100,
		EntityLimit: 100,
	})
	defer fc.Close()

	// Build up usage for entity-1
	for i := 0; i < 10; i++ {
		fc.Acquire("entity-1")
		fc.Release("entity-1")
	}

	// entity-2 has no history
	candidates := []string{"entity-1", "entity-2"}

	// entity-2 should be selected (new entity or lower usage)
	fairest := fc.SelectFairest(candidates)
	assert.Equal(t, "entity-2", fairest)
}

func TestFairnessController_Weights(t *testing.T) {
	fc := pool.NewFairnessController(pool.DefaultFairnessConfig())
	defer fc.Close()

	// Set weight
	err := fc.SetWeight("entity-1", 2.0)
	require.NoError(t, err)

	weight := fc.GetWeight("entity-1")
	assert.Equal(t, 2.0, weight)

	// Unknown entity has default weight
	weight = fc.GetWeight("unknown")
	assert.Equal(t, 1.0, weight)
}

func TestFairnessController_EntityStats(t *testing.T) {
	fc := pool.NewFairnessController(pool.DefaultFairnessConfig())
	defer fc.Close()

	fc.Acquire("entity-1")
	fc.Acquire("entity-1")

	stats, ok := fc.EntityStats("entity-1")
	assert.True(t, ok)
	assert.Equal(t, int64(2), stats.CurrentUsage)
	assert.Equal(t, int64(2), stats.TotalUsage)

	fc.Release("entity-1")

	stats, ok = fc.EntityStats("entity-1")
	assert.True(t, ok)
	assert.Equal(t, int64(1), stats.CurrentUsage)
}

func TestFairnessController_RemoveEntity(t *testing.T) {
	fc := pool.NewFairnessController(pool.DefaultFairnessConfig())
	defer fc.Close()

	fc.Acquire("entity-1")
	fc.Acquire("entity-1")

	stats := fc.Stats()
	assert.Equal(t, int64(2), stats.CurrentUsage)

	fc.RemoveEntity("entity-1")

	_, ok := fc.EntityStats("entity-1")
	assert.False(t, ok)

	// Usage should be decremented
	stats = fc.Stats()
	assert.Equal(t, int64(0), stats.CurrentUsage)
}

func TestPriority_String(t *testing.T) {
	tests := []struct {
		priority pool.Priority
		expected string
	}{
		{pool.PriorityBackground, "background"},
		{pool.PriorityLow, "low"},
		{pool.PriorityNormal, "normal"},
		{pool.PriorityHigh, "high"},
		{pool.PriorityCritical, "critical"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.priority.String())
		})
	}
}

func TestPriorityPool_ConcurrentSubmit(t *testing.T) {
	p := pool.NewPriorityPool(pool.PriorityPoolConfig{
		NumWorkers:   8,
		MaxQueueSize: 1000,
	})
	p.Start()
	defer p.Stop()

	var completed int64
	var wg sync.WaitGroup

	// Submit from multiple goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				done := make(chan struct{})
				p.Submit(&pool.PriorityJob{
					Priority: pool.Priority(j % 5),
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
	assert.Equal(t, int64(1000), atomic.LoadInt64(&completed))
}

func TestSessionPool_ConcurrentSessions(t *testing.T) {
	p := pool.NewSessionPool(pool.SessionPoolConfig{
		NumWorkers:        8,
		MaxJobsPerSession: 200,
		MaxTotalJobs:      1000,
	})
	p.Start()
	defer p.Stop()

	var completed int64
	var wg sync.WaitGroup

	for session := 0; session < 5; session++ {
		sessionID := string(rune('A' + session))
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				done := make(chan struct{})
				p.Submit(&pool.SessionJob{
					SessionID: sid,
					AgentType: "worker",
					Execute: func(ctx context.Context) error {
						atomic.AddInt64(&completed, 1)
						close(done)
						return nil
					},
				})
				<-done
			}
		}(sessionID)
	}

	wg.Wait()
	assert.Equal(t, int64(250), atomic.LoadInt64(&completed))

	stats := p.Stats()
	assert.Equal(t, 5, stats.SessionCount)
}

func TestFairnessController_ConcurrentAccess(t *testing.T) {
	fc := pool.NewFairnessController(pool.FairnessConfig{
		GlobalLimit: 50,
		EntityLimit: 10,
	})
	defer fc.Close()

	var wg sync.WaitGroup

	// Concurrent acquire/release from multiple entities
	for entity := 0; entity < 10; entity++ {
		entityID := string(rune('A' + entity))
		wg.Add(1)
		go func(eid string) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				acquired, _ := fc.Acquire(eid)
				if acquired {
					fc.Release(eid)
				}
			}
		}(entityID)
	}

	wg.Wait()

	// Should end with zero current usage
	stats := fc.Stats()
	assert.Equal(t, int64(0), stats.CurrentUsage)
}
