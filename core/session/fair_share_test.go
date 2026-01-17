package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFairShareCalculator_TwoEqualSessions(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))
	require.NoError(t, r.Register("session-2"))
	require.NoError(t, r.Heartbeat("session-1", 5, 0))
	require.NoError(t, r.Heartbeat("session-2", 5, 0))

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	totals := ResourceTotals{
		SubprocessSlots: 100,
		FileHandleSlots: 100,
		NetworkSlots:    100,
		MemoryBudget:    100 * 1024 * 1024,
	}

	allocs, err := calc.Calculate(totals)
	require.NoError(t, err)

	assert.Len(t, allocs, 2)

	diff := abs(float64(allocs["session-1"].SubprocessSlots - allocs["session-2"].SubprocessSlots))
	assert.LessOrEqual(t, diff, 1.0)
}

func TestFairShareCalculator_HighActivityGetsMore(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-high"))
	require.NoError(t, r.Register("session-low"))
	require.NoError(t, r.Heartbeat("session-high", 10, 10))
	require.NoError(t, r.Heartbeat("session-low", 1, 0))

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	totals := ResourceTotals{
		SubprocessSlots: 100,
		FileHandleSlots: 100,
		NetworkSlots:    100,
		MemoryBudget:    100 * 1024 * 1024,
	}

	allocs, err := calc.Calculate(totals)
	require.NoError(t, err)

	assert.Greater(t, allocs["session-high"].SubprocessSlots, allocs["session-low"].SubprocessSlots)
}

func TestFairShareCalculator_IdleSessionGetsBaseline(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-active"))
	require.NoError(t, r.Register("session-idle"))
	require.NoError(t, r.Heartbeat("session-active", 10, 0))

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	totals := ResourceTotals{
		SubprocessSlots: 100,
		FileHandleSlots: 100,
		NetworkSlots:    100,
		MemoryBudget:    100 * 1024 * 1024,
	}

	allocs, err := calc.Calculate(totals)
	require.NoError(t, err)

	assert.Equal(t, DefaultIdleBaseline, allocs["session-idle"].SubprocessSlots)
	assert.Greater(t, allocs["session-active"].SubprocessSlots, DefaultIdleBaseline)
}

func TestFairShareCalculator_SingleSessionGetsAll(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-only"))
	require.NoError(t, r.Heartbeat("session-only", 5, 0))

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	totals := ResourceTotals{
		SubprocessSlots: 100,
		FileHandleSlots: 100,
		NetworkSlots:    100,
		MemoryBudget:    100 * 1024 * 1024,
	}

	allocs, err := calc.Calculate(totals)
	require.NoError(t, err)

	assert.Len(t, allocs, 1)
	expectedSlots := int(float64(100) * (1 - DefaultUserReservedPercent))
	assert.GreaterOrEqual(t, allocs["session-only"].SubprocessSlots, expectedSlots-5)
}

func TestFairShareCalculator_NoSessions(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	totals := ResourceTotals{SubprocessSlots: 100}

	allocs, err := calc.Calculate(totals)
	require.NoError(t, err)
	assert.Len(t, allocs, 0)
}

func TestFairShareCalculator_HasSignificantChange(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))
	require.NoError(t, r.Heartbeat("session-1", 5, 0))

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	allocs1 := map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
	}
	calc.UpdateLastAllocation(allocs1)

	allocs2 := map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
	}
	assert.False(t, calc.HasSignificantChange(allocs2))

	allocs3 := map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 20},
	}
	assert.True(t, calc.HasSignificantChange(allocs3))
}

func TestFairShareCalculator_NewSessionIsSignificant(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	allocs1 := map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
	}
	calc.UpdateLastAllocation(allocs1)

	allocs2 := map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
		"session-2": {SessionID: "session-2", SubprocessSlots: 10},
	}
	assert.True(t, calc.HasSignificantChange(allocs2))
}

func TestFairShareCalculator_GetLastAllocation(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	allocs := map[string]SessionAllocation{
		"session-1": {SessionID: "session-1", SubprocessSlots: 10},
	}
	calc.UpdateLastAllocation(allocs)

	retrieved := calc.GetLastAllocation()
	assert.Equal(t, allocs, retrieved)

	retrieved["session-1"] = SessionAllocation{}
	original := calc.GetLastAllocation()
	assert.Equal(t, 10, original["session-1"].SubprocessSlots)
}

func TestFairShareCalculator_RebalanceInterval(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	cfg := FairShareConfig{
		RebalanceInterval: 2 * time.Second,
	}
	calc := NewFairShareCalculator(r, cfg)

	assert.Equal(t, 2*time.Second, calc.RebalanceInterval())
}

func TestFairShareCalculator_DefaultConfig(t *testing.T) {
	cfg := DefaultFairShareConfig()

	assert.Equal(t, DefaultUserReservedPercent, cfg.UserReservedPercent)
	assert.Equal(t, DefaultIdleBaseline, cfg.IdleBaseline)
	assert.Equal(t, DefaultRebalanceInterval, cfg.RebalanceInterval)
	assert.Equal(t, DefaultSignificantChange, cfg.SignificantChange)
}

func TestFairShareCalculator_UserReservedCapacity(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))
	require.NoError(t, r.Heartbeat("session-1", 5, 0))

	cfg := FairShareConfig{
		UserReservedPercent: 0.5,
	}
	calc := NewFairShareCalculator(r, cfg)

	totals := ResourceTotals{
		SubprocessSlots: 100,
	}

	allocs, err := calc.Calculate(totals)
	require.NoError(t, err)

	assert.LessOrEqual(t, allocs["session-1"].SubprocessSlots, 50)
}

func TestSessionAllocation_AllResourceTypes(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))
	require.NoError(t, r.Heartbeat("session-1", 5, 0))

	calc := NewFairShareCalculator(r, DefaultFairShareConfig())

	totals := ResourceTotals{
		SubprocessSlots: 100,
		FileHandleSlots: 200,
		NetworkSlots:    50,
		MemoryBudget:    1024 * 1024 * 1024,
	}

	allocs, err := calc.Calculate(totals)
	require.NoError(t, err)

	alloc := allocs["session-1"]
	assert.Greater(t, alloc.SubprocessSlots, 0)
	assert.Greater(t, alloc.FileHandleSlots, 0)
	assert.Greater(t, alloc.NetworkSlots, 0)
	assert.Greater(t, alloc.MemoryBudget, int64(0))
}

func newFairShareTestRegistry(t *testing.T) *Registry {
	t.Helper()
	cfg := DefaultRegistryConfig()
	cfg.DBPath = filepath.Join(t.TempDir(), "fair_share_test.db")
	cfg.CleanupInterval = 1 * time.Hour
	r, err := NewRegistry(cfg)
	require.NoError(t, err)
	return r
}
