package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoroutineBudget_AcquireUnderSoftLimit(t *testing.T) {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)
	budget.RegisterAgent("agent-1", "engineer")

	err := budget.Acquire("agent-1")
	require.NoError(t, err)

	active, _, _, _, ok := budget.GetStats("agent-1")
	require.True(t, ok)
	assert.Equal(t, int64(1), active)

	budget.Release("agent-1")
	active, _, _, _, _ = budget.GetStats("agent-1")
	assert.Equal(t, int64(0), active)
}

func TestGoroutineBudget_WarningOverSoftLimit(t *testing.T) {
	pressure := &atomic.Int32{}

	var warningCalled atomic.Bool
	cfg := GoroutineBudgetConfig{
		SystemLimit:     100,
		BurstMultiplier: 1.5,
		TypeWeights:     map[string]float64{"test": 1.0},
		OnWarning: func(agentID string, active, limit int) {
			warningCalled.Store(true)
		},
	}

	budget := NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("agent-1", "test")

	_, _, softLimit, _, _ := budget.GetStats("agent-1")

	for i := int64(0); i <= softLimit; i++ {
		err := budget.Acquire("agent-1")
		require.NoError(t, err)
	}

	assert.True(t, warningCalled.Load())
}

func TestGoroutineBudget_BlocksAtHardLimit(t *testing.T) {
	pressure := &atomic.Int32{}

	cfg := GoroutineBudgetConfig{
		SystemLimit:     30,
		BurstMultiplier: 1.0,
		TypeWeights:     map[string]float64{"test": 1.0},
	}

	budget := NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("agent-1", "test")

	_, _, _, hardLimit, _ := budget.GetStats("agent-1")

	for i := int64(0); i < hardLimit; i++ {
		err := budget.Acquire("agent-1")
		require.NoError(t, err)
	}

	done := make(chan struct{})
	go func() {
		_ = budget.Acquire("agent-1")
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("Acquire should have blocked at hard limit")
	case <-time.After(50 * time.Millisecond):
	}

	budget.Release("agent-1")

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Acquire should have unblocked after release")
	}
}

func TestGoroutineBudget_ReleaseUnblocksWaiter(t *testing.T) {
	pressure := &atomic.Int32{}

	cfg := GoroutineBudgetConfig{
		SystemLimit:     15,
		BurstMultiplier: 1.0,
		TypeWeights:     map[string]float64{"test": 1.0},
	}

	budget := NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("agent-1", "test")

	_, _, _, hardLimit, _ := budget.GetStats("agent-1")

	for i := int64(0); i < hardLimit; i++ {
		err := budget.Acquire("agent-1")
		require.NoError(t, err)
	}

	unblocked := make(chan struct{})
	go func() {
		_ = budget.Acquire("agent-1")
		close(unblocked)
	}()

	time.Sleep(20 * time.Millisecond)
	budget.Release("agent-1")

	select {
	case <-unblocked:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected acquire to unblock after release")
	}
}

func TestGoroutineBudget_PressureRecalculatesLimits(t *testing.T) {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)
	budget.RegisterAgent("agent-1", "engineer")

	_, _, softNormal, hardNormal, _ := budget.GetStats("agent-1")

	budget.OnPressureChange(PressureCritical)

	_, _, softCritical, hardCritical, _ := budget.GetStats("agent-1")

	assert.Less(t, softCritical, softNormal)
	assert.Less(t, hardCritical, hardNormal)
}

func TestGoroutineBudget_FairDistributionByWeight(t *testing.T) {
	pressure := &atomic.Int32{}

	cfg := GoroutineBudgetConfig{
		SystemLimit:     1000,
		BurstMultiplier: 1.5,
		TypeWeights: map[string]float64{
			"heavy": 2.0,
			"light": 1.0,
		},
	}

	budget := NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("heavy-agent", "heavy")
	budget.RegisterAgent("light-agent", "light")

	_, _, softHeavy, _, _ := budget.GetStats("heavy-agent")
	_, _, softLight, _, _ := budget.GetStats("light-agent")

	ratio := float64(softHeavy) / float64(softLight)
	assert.InDelta(t, 2.0, ratio, 0.1, "heavy agent should have ~2x budget")
}

func TestGoroutineBudget_MultiAgentSharing(t *testing.T) {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)

	budget.RegisterAgent("agent-1", "engineer")
	budget.RegisterAgent("agent-2", "engineer")

	for i := 0; i < 10; i++ {
		require.NoError(t, budget.Acquire("agent-1"))
		require.NoError(t, budget.Acquire("agent-2"))
	}

	assert.Equal(t, int64(20), budget.TotalActive())

	for i := 0; i < 10; i++ {
		budget.Release("agent-1")
		budget.Release("agent-2")
	}

	assert.Equal(t, int64(0), budget.TotalActive())
}

func TestGoroutineBudget_UnregisteredAgentReturnsError(t *testing.T) {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)

	err := budget.Acquire("nonexistent")
	assert.ErrorIs(t, err, ErrAgentNotRegistered)
}

func TestGoroutineBudget_AcquireWithContext_Cancellation(t *testing.T) {
	pressure := &atomic.Int32{}

	cfg := GoroutineBudgetConfig{
		SystemLimit:     15,
		BurstMultiplier: 1.0,
		TypeWeights:     map[string]float64{"test": 1.0},
	}

	budget := NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("agent-1", "test")

	_, _, _, hardLimit, _ := budget.GetStats("agent-1")

	for i := int64(0); i < hardLimit; i++ {
		err := budget.Acquire("agent-1")
		require.NoError(t, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := budget.AcquireWithContext(ctx, "agent-1")
	assert.Error(t, err)
}

func TestGoroutineBudget_ConcurrentAcquireRelease(t *testing.T) {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)
	budget.RegisterAgent("agent-1", "engineer")

	var wg sync.WaitGroup
	iterations := 100
	workers := 10

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if err := budget.Acquire("agent-1"); err == nil {
					time.Sleep(time.Microsecond)
					budget.Release("agent-1")
				}
			}
		}()
	}

	wg.Wait()

	active, _, _, _, _ := budget.GetStats("agent-1")
	assert.Equal(t, int64(0), active)
	assert.Equal(t, int64(0), budget.TotalActive())
}

func TestGoroutineBudget_PeakTracking(t *testing.T) {
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)
	budget.RegisterAgent("agent-1", "engineer")

	for i := 0; i < 5; i++ {
		require.NoError(t, budget.Acquire("agent-1"))
	}

	_, peak1, _, _, _ := budget.GetStats("agent-1")
	assert.Equal(t, int64(5), peak1)

	for i := 0; i < 5; i++ {
		budget.Release("agent-1")
	}

	_, peak2, _, _, _ := budget.GetStats("agent-1")
	assert.Equal(t, int64(5), peak2)

	for i := 0; i < 3; i++ {
		require.NoError(t, budget.Acquire("agent-1"))
	}

	_, peak3, _, _, _ := budget.GetStats("agent-1")
	assert.Equal(t, int64(5), peak3)
}

func TestGoroutineBudget_MinimumLimits(t *testing.T) {
	pressure := &atomic.Int32{}

	cfg := GoroutineBudgetConfig{
		SystemLimit:     1,
		BurstMultiplier: 1.0,
		TypeWeights:     map[string]float64{"test": 1.0},
	}

	budget := NewGoroutineBudgetWithConfig(pressure, cfg)
	budget.RegisterAgent("agent-1", "test")

	_, _, soft, hard, _ := budget.GetStats("agent-1")

	assert.GreaterOrEqual(t, soft, int64(10))
	assert.GreaterOrEqual(t, hard, int64(15))
}
