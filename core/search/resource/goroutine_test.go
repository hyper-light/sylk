package resource

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Mock GoroutineBudget
// =============================================================================

type mockGoroutineBudget struct {
	mu          sync.Mutex
	agents      map[string]bool
	acquireErr  error
	active      atomic.Int64
	maxPerAgent int64
}

func newMockGoroutineBudget(maxPerAgent int64) *mockGoroutineBudget {
	return &mockGoroutineBudget{
		agents:      make(map[string]bool),
		maxPerAgent: maxPerAgent,
	}
}

func (m *mockGoroutineBudget) Acquire(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.acquireErr != nil {
		return m.acquireErr
	}
	if m.active.Load() >= m.maxPerAgent {
		return ErrBudgetExhausted
	}
	m.active.Add(1)
	return nil
}

func (m *mockGoroutineBudget) AcquireWithContext(ctx context.Context, agentID string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return m.Acquire(agentID)
}

func (m *mockGoroutineBudget) Release(agentID string) {
	m.active.Add(-1)
}

func (m *mockGoroutineBudget) RegisterAgent(agentID, agentType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[agentID] = true
}

func (m *mockGoroutineBudget) UnregisterAgent(agentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.agents, agentID)
}

func (m *mockGoroutineBudget) GetStats(agentID string) (active, peak, softLimit, hardLimit int64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.agents[agentID] {
		return 0, 0, 0, 0, false
	}
	return m.active.Load(), m.active.Load(), m.maxPerAgent, m.maxPerAgent, true
}

func (m *mockGoroutineBudget) TotalActive() int64 {
	return m.active.Load()
}

// =============================================================================
// NewSearchGoroutineBudget Tests
// =============================================================================

func TestNewSearchGoroutineBudget_DefaultConfig(t *testing.T) {
	cfg := DefaultSearchGoroutineBudgetConfig()
	cfg.Budget = newMockGoroutineBudget(100)

	sgb := NewSearchGoroutineBudget(cfg)
	require.NotNil(t, sgb)

	assert.Equal(t, "search", sgb.agentID)
	assert.Equal(t, "search", sgb.agentType)
	assert.Equal(t, int32(8), sgb.maxIndexing)
	assert.Equal(t, int32(16), sgb.maxSearch)
}

func TestNewSearchGoroutineBudget_CustomConfig(t *testing.T) {
	cfg := SearchGoroutineBudgetConfig{
		Budget:                newMockGoroutineBudget(100),
		AgentID:               "custom-search",
		AgentType:             "indexer",
		MaxIndexingGoroutines: 12,
		MaxSearchGoroutines:   24,
	}

	sgb := NewSearchGoroutineBudget(cfg)
	require.NotNil(t, sgb)

	assert.Equal(t, "custom-search", sgb.agentID)
	assert.Equal(t, "indexer", sgb.agentType)
	assert.Equal(t, int32(12), sgb.maxIndexing)
	assert.Equal(t, int32(24), sgb.maxSearch)
}

func TestNewSearchGoroutineBudget_NilBudget(t *testing.T) {
	cfg := SearchGoroutineBudgetConfig{
		Budget: nil,
	}

	sgb := NewSearchGoroutineBudget(cfg)
	require.NotNil(t, sgb)
}

func TestNewSearchGoroutineBudget_RegistersAgent(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	cfg := SearchGoroutineBudgetConfig{
		Budget:  mock,
		AgentID: "test-agent",
	}

	NewSearchGoroutineBudget(cfg)

	mock.mu.Lock()
	registered := mock.agents["test-agent"]
	mock.mu.Unlock()

	assert.True(t, registered)
}

// =============================================================================
// AcquireForIndexing Tests
// =============================================================================

func TestAcquireForIndexing_Success(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 10,
	})

	ctx := context.Background()
	release, err := sgb.AcquireForIndexing(ctx, 3)

	require.NoError(t, err)
	require.NotNil(t, release)
	assert.Equal(t, int32(3), sgb.ActiveIndexing())
	assert.Equal(t, int64(3), mock.TotalActive())

	release()
	assert.Equal(t, int32(0), sgb.ActiveIndexing())
	assert.Equal(t, int64(0), mock.TotalActive())
}

func TestAcquireForIndexing_ZeroCount(t *testing.T) {
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget: newMockGoroutineBudget(100),
	})

	ctx := context.Background()
	release, err := sgb.AcquireForIndexing(ctx, 0)

	require.NoError(t, err)
	require.NotNil(t, release)
	assert.Equal(t, int32(0), sgb.ActiveIndexing())

	release() // Should be safe to call
}

func TestAcquireForIndexing_ExceedsLimit(t *testing.T) {
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                newMockGoroutineBudget(100),
		MaxIndexingGoroutines: 5,
	})

	ctx := context.Background()
	_, err := sgb.AcquireForIndexing(ctx, 6)

	assert.ErrorIs(t, err, ErrBudgetExhausted)
}

func TestAcquireForIndexing_ContextCancellation(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sgb.AcquireForIndexing(ctx, 3)
	assert.Error(t, err)
}

func TestAcquireForIndexing_DoubleRelease(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 10,
	})

	ctx := context.Background()
	release, err := sgb.AcquireForIndexing(ctx, 2)
	require.NoError(t, err)

	release()
	release() // Should be safe

	assert.Equal(t, int32(0), sgb.ActiveIndexing())
}

func TestAcquireForIndexing_NilBudget(t *testing.T) {
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                nil,
		MaxIndexingGoroutines: 10,
	})

	ctx := context.Background()
	release, err := sgb.AcquireForIndexing(ctx, 3)

	require.NoError(t, err)
	assert.Equal(t, int32(3), sgb.ActiveIndexing())

	release()
	assert.Equal(t, int32(0), sgb.ActiveIndexing())
}

// =============================================================================
// AcquireForSearch Tests
// =============================================================================

func TestAcquireForSearch_Success(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:              mock,
		MaxSearchGoroutines: 10,
	})

	ctx := context.Background()
	release, err := sgb.AcquireForSearch(ctx)

	require.NoError(t, err)
	require.NotNil(t, release)
	assert.Equal(t, int32(1), sgb.ActiveSearch())

	release()
	assert.Equal(t, int32(0), sgb.ActiveSearch())
}

func TestAcquireForSearch_ExceedsLimit(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:              mock,
		MaxSearchGoroutines: 2,
	})

	ctx := context.Background()

	r1, err := sgb.AcquireForSearch(ctx)
	require.NoError(t, err)
	r2, err := sgb.AcquireForSearch(ctx)
	require.NoError(t, err)

	_, err = sgb.AcquireForSearch(ctx)
	assert.ErrorIs(t, err, ErrBudgetExhausted)

	r1()
	r2()
}

func TestAcquireForSearch_ContextCancellation(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:              mock,
		MaxSearchGoroutines: 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sgb.AcquireForSearch(ctx)
	assert.Error(t, err)
}

// =============================================================================
// TryAcquireForSearch Tests
// =============================================================================

func TestTryAcquireForSearch_Success(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:              mock,
		MaxSearchGoroutines: 10,
	})

	release, ok := sgb.TryAcquireForSearch()

	assert.True(t, ok)
	require.NotNil(t, release)
	assert.Equal(t, int32(1), sgb.ActiveSearch())

	release()
	assert.Equal(t, int32(0), sgb.ActiveSearch())
}

func TestTryAcquireForSearch_Failure(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:              mock,
		MaxSearchGoroutines: 1,
	})

	r1, ok := sgb.TryAcquireForSearch()
	require.True(t, ok)

	_, ok = sgb.TryAcquireForSearch()
	assert.False(t, ok)

	r1()
}

func TestTryAcquireForSearch_Closed(t *testing.T) {
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget: newMockGoroutineBudget(100),
	})

	sgb.Close()

	_, ok := sgb.TryAcquireForSearch()
	assert.False(t, ok)
}

// =============================================================================
// AcquireForIndexingDegraded Tests
// =============================================================================

func TestAcquireForIndexingDegraded_FullAcquire(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 10,
	})

	ctx := context.Background()
	acquired, release, err := sgb.AcquireForIndexingDegraded(ctx, 5)

	require.NoError(t, err)
	assert.Equal(t, 5, acquired)
	assert.Equal(t, int32(5), sgb.ActiveIndexing())

	release()
	assert.Equal(t, int32(0), sgb.ActiveIndexing())
}

func TestAcquireForIndexingDegraded_PartialAcquire(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 5,
	})

	ctx := context.Background()

	// Acquire some first
	r1, _ := sgb.AcquireForIndexing(ctx, 3)

	// Try to acquire more than available
	acquired, release, err := sgb.AcquireForIndexingDegraded(ctx, 5)

	require.NoError(t, err)
	assert.Equal(t, 2, acquired)

	release()
	r1()
}

func TestAcquireForIndexingDegraded_NoneAvailable(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 2,
	})

	ctx := context.Background()
	r1, _ := sgb.AcquireForIndexing(ctx, 2)

	acquired, release, err := sgb.AcquireForIndexingDegraded(ctx, 5)

	require.NoError(t, err)
	assert.Equal(t, 0, acquired)
	require.NotNil(t, release)

	release()
	r1()
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestGoroutineBudget_GetStats(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 8,
		MaxSearchGoroutines:   16,
	})

	ctx := context.Background()
	ri, _ := sgb.AcquireForIndexing(ctx, 3)
	rs, _ := sgb.AcquireForSearch(ctx)

	stats := sgb.GetStats()

	assert.Equal(t, int32(3), stats.ActiveIndexing)
	assert.Equal(t, int32(1), stats.ActiveSearch)
	assert.Equal(t, int32(8), stats.MaxIndexing)
	assert.Equal(t, int32(16), stats.MaxSearch)

	ri()
	rs()
}

// =============================================================================
// Lifecycle Tests
// =============================================================================

func TestGoroutineBudget_Close(t *testing.T) {
	mock := newMockGoroutineBudget(100)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:  mock,
		AgentID: "test-agent",
	})

	err := sgb.Close()
	require.NoError(t, err)

	mock.mu.Lock()
	registered := mock.agents["test-agent"]
	mock.mu.Unlock()

	assert.False(t, registered)
}

func TestGoroutineBudget_Close_Idempotent(t *testing.T) {
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget: newMockGoroutineBudget(100),
	})

	err := sgb.Close()
	require.NoError(t, err)

	err = sgb.Close()
	require.NoError(t, err)
}

func TestGoroutineBudget_AcquireAfterClose(t *testing.T) {
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget: newMockGoroutineBudget(100),
	})

	sgb.Close()

	ctx := context.Background()

	_, err := sgb.AcquireForIndexing(ctx, 1)
	assert.ErrorIs(t, err, ErrBudgetClosed)

	_, err = sgb.AcquireForSearch(ctx)
	assert.ErrorIs(t, err, ErrBudgetClosed)
}

// =============================================================================
// Degradation Callback Tests
// =============================================================================

func TestGoroutineBudget_OnDegraded_Called(t *testing.T) {
	var degradedReason string
	var mu sync.Mutex

	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                newMockGoroutineBudget(100),
		MaxIndexingGoroutines: 2,
		OnDegraded: func(reason string) {
			mu.Lock()
			degradedReason = reason
			mu.Unlock()
		},
	})

	ctx := context.Background()
	sgb.AcquireForIndexing(ctx, 2)
	sgb.AcquireForIndexing(ctx, 1)

	mu.Lock()
	reason := degradedReason
	mu.Unlock()

	assert.Contains(t, reason, "limit reached")
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestGoroutineBudget_ConcurrentAcquireRelease(t *testing.T) {
	mock := newMockGoroutineBudget(1000)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 100,
		MaxSearchGoroutines:   100,
	})

	var wg sync.WaitGroup
	ctx := context.Background()

	// Concurrent indexing
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := sgb.AcquireForIndexing(ctx, 1)
			if err == nil {
				time.Sleep(time.Millisecond)
				release()
			}
		}()
	}

	// Concurrent search
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := sgb.AcquireForSearch(ctx)
			if err == nil {
				time.Sleep(time.Millisecond)
				release()
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(0), sgb.ActiveIndexing())
	assert.Equal(t, int32(0), sgb.ActiveSearch())
}

func TestGoroutineBudget_ConcurrentAcquireReleaseWithContention(t *testing.T) {
	mock := newMockGoroutineBudget(1000)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 5,
		MaxSearchGoroutines:   5,
	})

	var wg sync.WaitGroup

	// Many goroutines competing for limited slots
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, ok := sgb.TryAcquireForSearch()
			if ok {
				time.Sleep(time.Millisecond)
				release()
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(0), sgb.ActiveSearch())
}

// =============================================================================
// Budget Exhaustion Tests
// =============================================================================

func TestBudgetExhaustion_Indexing(t *testing.T) {
	mock := newMockGoroutineBudget(5) // Low limit
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:                mock,
		MaxIndexingGoroutines: 10, // Higher than mock limit
	})

	ctx := context.Background()

	// Exhaust the underlying budget
	for i := 0; i < 5; i++ {
		_, err := sgb.AcquireForIndexing(ctx, 1)
		require.NoError(t, err)
	}

	// Next acquire should fail
	_, err := sgb.AcquireForIndexing(ctx, 1)
	assert.Error(t, err)
}

func TestBudgetExhaustion_Search(t *testing.T) {
	mock := newMockGoroutineBudget(3)
	sgb := NewSearchGoroutineBudget(SearchGoroutineBudgetConfig{
		Budget:              mock,
		MaxSearchGoroutines: 10,
	})

	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := sgb.AcquireForSearch(ctx)
		require.NoError(t, err)
	}

	_, err := sgb.AcquireForSearch(ctx)
	assert.Error(t, err)
}
