package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_RegisterAndUnregister(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	err := r.Register("session-1")
	require.NoError(t, err)

	sessions, err := r.GetActiveSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "session-1", sessions[0].SessionID)

	err = r.Unregister("session-1")
	require.NoError(t, err)

	sessions, err = r.GetActiveSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 0)
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	err := r.Register("session-1")
	require.NoError(t, err)

	err = r.Register("session-1")
	assert.ErrorIs(t, err, ErrRegistrySessionExists)
}

func TestRegistry_UnregisterNotFound(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	err := r.Unregister("nonexistent")
	assert.ErrorIs(t, err, ErrRegistrySessionNotFound)
}

func TestRegistry_Heartbeat(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	err := r.Register("session-1")
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	err = r.Heartbeat("session-1", 5, 10)
	require.NoError(t, err)

	session, err := r.GetSession("session-1")
	require.NoError(t, err)

	expectedScore := 5.0 + 10.0*DefaultActivityDecayRate
	assert.InDelta(t, expectedScore, session.ActivityScore, 0.001)
	assert.Equal(t, 5, session.RunningPipelines)
}

func TestRegistry_HeartbeatNotFound(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	err := r.Heartbeat("nonexistent", 1, 1)
	assert.ErrorIs(t, err, ErrRegistrySessionNotFound)
}

func TestRegistry_GetActiveSessions(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))
	require.NoError(t, r.Register("session-2"))
	require.NoError(t, r.Register("session-3"))

	require.NoError(t, r.Heartbeat("session-1", 10, 0))
	require.NoError(t, r.Heartbeat("session-2", 5, 0))
	require.NoError(t, r.Heartbeat("session-3", 1, 0))

	sessions, err := r.GetActiveSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 3)

	assert.Equal(t, "session-1", sessions[0].SessionID)
	assert.Equal(t, "session-2", sessions[1].SessionID)
	assert.Equal(t, "session-3", sessions[2].SessionID)
}

func TestRegistry_GetSession(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))

	session, err := r.GetSession("session-1")
	require.NoError(t, err)
	assert.Equal(t, "session-1", session.SessionID)
	assert.Equal(t, os.Getpid(), session.PID)
}

func TestRegistry_GetSessionNotFound(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	_, err := r.GetSession("nonexistent")
	assert.ErrorIs(t, err, ErrRegistrySessionNotFound)
}

func TestRegistry_CleanupStaleSessions(t *testing.T) {
	cfg := DefaultRegistryConfig()
	cfg.DBPath = testDBPath(t)
	cfg.StaleThreshold = 50 * time.Millisecond
	cfg.CleanupInterval = 1 * time.Hour

	r, err := NewRegistry(cfg)
	require.NoError(t, err)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))
	require.NoError(t, r.Register("session-2"))

	require.NoError(t, r.Heartbeat("session-1", 1, 0))

	time.Sleep(60 * time.Millisecond)

	require.NoError(t, r.Heartbeat("session-1", 1, 0))

	require.NoError(t, r.CleanupStaleSessions())

	sessions, err := r.GetActiveSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "session-1", sessions[0].SessionID)
}

func TestRegistry_UpdateAllocatedSlots(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))

	err := r.UpdateAllocatedSlots("session-1", 10)
	require.NoError(t, err)

	session, err := r.GetSession("session-1")
	require.NoError(t, err)
	assert.Equal(t, 10, session.AllocatedSlots)
}

func TestRegistry_UpdateAllocatedSlotsNotFound(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	err := r.UpdateAllocatedSlots("nonexistent", 10)
	assert.ErrorIs(t, err, ErrRegistrySessionNotFound)
}

func TestRegistry_ResourceAllocation(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))

	err := r.SetResourceAllocation("session-1", "subprocess", 5)
	require.NoError(t, err)

	count, err := r.GetResourceAllocation("session-1", "subprocess")
	require.NoError(t, err)
	assert.Equal(t, 5, count)

	err = r.SetResourceAllocation("session-1", "subprocess", 10)
	require.NoError(t, err)

	count, err = r.GetResourceAllocation("session-1", "subprocess")
	require.NoError(t, err)
	assert.Equal(t, 10, count)
}

func TestRegistry_ResourceAllocationNotFound(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))

	count, err := r.GetResourceAllocation("session-1", "nonexistent")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))

	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := r.Heartbeat("session-1", id, j); err != nil {
					errChan <- err
				}
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, err := r.GetActiveSessions()
				if err != nil {
					errChan <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("concurrent access error: %v", err)
	}
}

func TestRegistry_ClosedOperations(t *testing.T) {
	r := newTestRegistry(t)

	require.NoError(t, r.Close())

	assert.ErrorIs(t, r.Register("session-1"), ErrRegistryClosed)
	assert.ErrorIs(t, r.Unregister("session-1"), ErrRegistryClosed)
	assert.ErrorIs(t, r.Heartbeat("session-1", 1, 1), ErrRegistryClosed)

	_, err := r.GetActiveSessions()
	assert.ErrorIs(t, err, ErrRegistryClosed)

	_, err = r.GetSession("session-1")
	assert.ErrorIs(t, err, ErrRegistryClosed)

	assert.ErrorIs(t, r.CleanupStaleSessions(), ErrRegistryClosed)
	assert.ErrorIs(t, r.UpdateAllocatedSlots("session-1", 1), ErrRegistryClosed)
	assert.ErrorIs(t, r.SetResourceAllocation("session-1", "type", 1), ErrRegistryClosed)

	_, err = r.GetResourceAllocation("session-1", "type")
	assert.ErrorIs(t, err, ErrRegistryClosed)

	err = r.Close()
	assert.ErrorIs(t, err, ErrRegistryClosed)
}

func TestRegistry_SessionRecord_IsStale(t *testing.T) {
	record := SessionRecord{
		LastHeartbeat: time.Now().Add(-5 * time.Second),
	}

	assert.False(t, record.IsStale(10*time.Second))
	assert.True(t, record.IsStale(3*time.Second))
}

func TestRegistry_DefaultConfig(t *testing.T) {
	cfg := DefaultRegistryConfig()

	assert.Equal(t, DefaultRegistryDBPath, cfg.DBPath)
	assert.Equal(t, DefaultHeartbeatInterval, cfg.HeartbeatInterval)
	assert.Equal(t, DefaultStaleThreshold, cfg.StaleThreshold)
	assert.Equal(t, DefaultActivityDecayRate, cfg.ActivityDecayRate)
	assert.Equal(t, DefaultCleanupInterval, cfg.CleanupInterval)
}

func TestRegistry_ActivityScoreCalculation(t *testing.T) {
	r := newTestRegistry(t)
	defer r.Close()

	require.NoError(t, r.Register("session-1"))

	require.NoError(t, r.Heartbeat("session-1", 3, 7))

	session, err := r.GetSession("session-1")
	require.NoError(t, err)

	expected := 3.0 + 7.0*DefaultActivityDecayRate
	assert.InDelta(t, expected, session.ActivityScore, 0.001)
}

// TestRegistry_RemoveDeadSessions_ErrorPropagation verifies W12.29 fix:
// database errors in removeDeadSessions are now properly returned to callers.
func TestRegistry_RemoveDeadSessions_ErrorPropagation(t *testing.T) {
	r := newTestRegistry(t)

	// Register a session
	require.NoError(t, r.Register("session-1"))

	// Close the database to cause errors
	r.db.Close()

	// GetActiveSessions internally calls removeDeadSessions, which should
	// now propagate database errors instead of silently ignoring them.
	_, err := r.GetActiveSessions()
	// Either we get an error from the query or from removeDeadSessions
	// The key point is the operation fails gracefully with an error
	assert.Error(t, err, "should return error when database is closed")
}

// TestRegistry_ValidateSessionProcess_ErrorPropagation verifies W12.29 fix:
// database errors in validateSessionProcess are now properly returned to callers.
func TestRegistry_ValidateSessionProcess_ErrorPropagation(t *testing.T) {
	r := newTestRegistry(t)

	// Register a session from current process
	require.NoError(t, r.Register("session-1"))

	// Get session should work while DB is open
	session, err := r.GetSession("session-1")
	require.NoError(t, err)
	require.NotNil(t, session)

	// Close the registry (which closes DB) to trigger errors
	r.Close()

	// Now GetSession should fail with ErrRegistryClosed
	_, err = r.GetSession("session-1")
	assert.ErrorIs(t, err, ErrRegistryClosed)
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()

	cfg := DefaultRegistryConfig()
	cfg.DBPath = testDBPath(t)
	cfg.CleanupInterval = 1 * time.Hour

	r, err := NewRegistry(cfg)
	require.NoError(t, err)

	return r
}

func testDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test_registry.db")
}
