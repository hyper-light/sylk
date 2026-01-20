package session_test

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManager_SetActiveSession_StoresCorrectValue verifies that
// setActiveSession (called via Switch) stores the correct session ID.
func TestManager_SetActiveSession_StoresCorrectValue(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	s1, err := mgr.Create(context.Background(), session.Config{Name: "session-1"})
	require.NoError(t, err)
	s2, err := mgr.Create(context.Background(), session.Config{Name: "session-2"})
	require.NoError(t, err)

	// Switch to s1 and verify active session
	err = mgr.Switch(s1.ID())
	require.NoError(t, err)

	active, ok := mgr.GetActive()
	require.True(t, ok, "expected active session to be set")
	assert.Equal(t, s1.ID(), active.ID(), "active session should be s1")

	// Switch to s2 and verify active session changed
	err = mgr.Switch(s2.ID())
	require.NoError(t, err)

	active, ok = mgr.GetActive()
	require.True(t, ok, "expected active session to be set")
	assert.Equal(t, s2.ID(), active.ID(), "active session should be s2")
}

// TestManager_SetActiveSession_ValuePersistsAfterReturn verifies that
// the stored activeID value persists and remains valid after the
// setActiveSession function returns. This is the key test for W3H.1 fix.
func TestManager_SetActiveSession_ValuePersistsAfterReturn(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	// Create sessions with unique IDs
	sessions := make([]*session.Session, 10)
	for i := 0; i < 10; i++ {
		s, err := mgr.Create(context.Background(), session.Config{Name: "session"})
		require.NoError(t, err)
		sessions[i] = s
	}

	// For each session, switch to it and verify the ID persists
	for _, s := range sessions {
		expectedID := s.ID()

		err := mgr.Switch(expectedID)
		require.NoError(t, err)

		// Force some allocations and GC to potentially corrupt stack memory
		// if the pointer was pointing to stack-local variable
		for j := 0; j < 100; j++ {
			_ = make([]byte, 1024)
		}
		runtime.GC()

		// Verify the stored ID is still correct after GC
		active, ok := mgr.GetActive()
		require.True(t, ok, "expected active session to be set")
		assert.Equal(t, expectedID, active.ID(),
			"activeID should persist correctly after function return and GC")
	}
}

// TestManager_SetActiveSession_ConcurrentCalls verifies that
// concurrent calls to setActiveSession (via Switch) do not cause
// data races or use-after-free issues.
func TestManager_SetActiveSession_ConcurrentCalls(t *testing.T) {
	mgr := session.NewManager(session.ManagerConfig{
		MaxSessions: 100,
		NumShards:   16,
	})

	// Create multiple sessions
	numSessions := 20
	sessions := make([]*session.Session, numSessions)
	for i := 0; i < numSessions; i++ {
		s, err := mgr.Create(context.Background(), session.Config{Name: "session"})
		require.NoError(t, err)
		sessions[i] = s
	}

	// Run concurrent switches
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	numGoroutines := 10
	var switchCount int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Pick a session based on counter to ensure variety
					count := atomic.AddInt64(&switchCount, 1)
					idx := int(count) % numSessions
					targetSession := sessions[idx]

					// Attempt to switch (may fail if session was closed)
					_ = mgr.Switch(targetSession.ID())

					// Verify we can read the active session
					active, ok := mgr.GetActive()
					if ok && active != nil {
						// Just access the ID to ensure it's valid
						_ = active.ID()
					}
				}
			}
		}(i)
	}

	// Also run concurrent readers
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					active, ok := mgr.GetActive()
					if ok && active != nil {
						// Access the ID to verify it's not corrupted
						id := active.ID()
						assert.NotEmpty(t, id, "active session ID should not be empty")
					}
				}
			}
		}()
	}

	wg.Wait()

	// Verify manager is still in valid state
	stats := mgr.Stats()
	assert.LessOrEqual(t, stats.TotalSessions, numSessions)
}

// TestManager_SetActiveSession_RapidSequentialSwitches tests that rapid
// sequential switches don't cause use-after-free by checking that the
// stored ID always matches what was set.
func TestManager_SetActiveSession_RapidSequentialSwitches(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	s1, err := mgr.Create(context.Background(), session.Config{Name: "session-1"})
	require.NoError(t, err)
	s2, err := mgr.Create(context.Background(), session.Config{Name: "session-2"})
	require.NoError(t, err)

	// Rapidly switch between sessions
	for i := 0; i < 1000; i++ {
		var expectedID string
		if i%2 == 0 {
			expectedID = s1.ID()
		} else {
			expectedID = s2.ID()
		}

		err := mgr.Switch(expectedID)
		require.NoError(t, err)

		// Immediately verify
		active, ok := mgr.GetActive()
		require.True(t, ok)
		assert.Equal(t, expectedID, active.ID(),
			"iteration %d: active ID should match expected", i)
	}
}

// TestManager_SetActiveSession_UniqueStrings ensures that when different
// unique string values are passed, each one is correctly stored and
// doesn't get overwritten by a previous pointer.
func TestManager_SetActiveSession_UniqueStrings(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	// Create sessions that will have unique UUIDs
	sessions := make([]*session.Session, 5)
	for i := 0; i < 5; i++ {
		s, err := mgr.Create(context.Background(), session.Config{Name: "session"})
		require.NoError(t, err)
		sessions[i] = s
	}

	// Verify each session can be set as active and retrieved correctly
	for _, s := range sessions {
		err := mgr.Switch(s.ID())
		require.NoError(t, err)

		active, ok := mgr.GetActive()
		require.True(t, ok)
		assert.Equal(t, s.ID(), active.ID())
	}

	// Now verify we can switch back to earlier sessions
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		err := mgr.Switch(s.ID())
		require.NoError(t, err)

		active, ok := mgr.GetActive()
		require.True(t, ok)
		assert.Equal(t, s.ID(), active.ID())
	}
}
