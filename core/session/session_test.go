package session_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Session Tests
// =============================================================================

func TestSession_New(t *testing.T) {
	cfg := session.Config{
		Name:        "test-session",
		Description: "A test session",
		Branch:      "feature/test",
	}

	s := session.NewSession(cfg)

	assert.NotEmpty(t, s.ID())
	assert.Equal(t, "test-session", s.Name())
	assert.Equal(t, "A test session", s.Description())
	assert.Equal(t, "feature/test", s.Branch())
	assert.Equal(t, session.StateCreated, s.State())
	assert.False(t, s.IsClosed())
}

func TestSession_DefaultName(t *testing.T) {
	s := session.NewSession(session.Config{})

	assert.NotEmpty(t, s.Name())
	assert.Contains(t, s.Name(), "session-")
}

func TestSession_StateTransitions(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())

	// Created -> Active
	err := s.Start()
	require.NoError(t, err)
	assert.Equal(t, session.StateActive, s.State())
	assert.True(t, s.IsActive())

	// Active -> Paused
	err = s.Pause()
	require.NoError(t, err)
	assert.Equal(t, session.StatePaused, s.State())
	assert.False(t, s.IsActive())

	// Paused -> Active
	err = s.Resume()
	require.NoError(t, err)
	assert.Equal(t, session.StateActive, s.State())

	// Active -> Completed
	err = s.Complete()
	require.NoError(t, err)
	assert.Equal(t, session.StateCompleted, s.State())
	assert.True(t, s.State().IsTerminal())

	// Terminal state - cannot transition
	err = s.Start()
	assert.Equal(t, session.ErrInvalidStateTransition, err)
}

func TestSession_InvalidTransitions(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())

	// Cannot go directly to Paused from Created
	err := s.Pause()
	assert.Equal(t, session.ErrInvalidStateTransition, err)

	// Cannot go directly to Completed from Created
	err = s.Complete()
	assert.Equal(t, session.ErrInvalidStateTransition, err)
}

func TestSession_Metadata(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())

	// Set metadata
	s.SetMetadata("key1", "value1")
	s.SetMetadata("key2", 42)

	// Get metadata
	v1, ok := s.GetMetadata("key1")
	assert.True(t, ok)
	assert.Equal(t, "value1", v1)

	v2, ok := s.GetMetadata("key2")
	assert.True(t, ok)
	assert.Equal(t, 42, v2)

	// Get all metadata
	all := s.Metadata()
	assert.Len(t, all, 2)

	// Delete metadata
	s.DeleteMetadata("key1")
	_, ok = s.GetMetadata("key1")
	assert.False(t, ok)

	// Non-existent key
	_, ok = s.GetMetadata("nonexistent")
	assert.False(t, ok)
}

func TestSession_WorkflowState(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())

	s.SetWorkflowState("dag-123", "planning", 0.25)

	dagID, phase, progress := s.WorkflowState()
	assert.Equal(t, "dag-123", dagID)
	assert.Equal(t, "planning", phase)
	assert.Equal(t, 0.25, progress)
}

func TestSession_Statistics(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())

	// Increment counters
	s.IncrementTasksCompleted()
	s.IncrementTasksCompleted()
	s.IncrementTasksFailed()
	s.IncrementMessagesRouted()
	s.IncrementMessagesRouted()
	s.IncrementMessagesRouted()
	s.IncrementEngineersSpawned()

	stats := s.Stats()
	assert.Equal(t, int64(2), stats.TasksCompleted)
	assert.Equal(t, int64(1), stats.TasksFailed)
	assert.Equal(t, int64(3), stats.MessagesRouted)
	assert.Equal(t, 1, stats.EngineersSpawned)
}

func TestSession_Serialization(t *testing.T) {
	s := session.NewSession(session.Config{
		Name:        "serialize-test",
		Description: "Testing serialization",
		Branch:      "main",
	})

	s.SetMetadata("user", "test")
	s.SetWorkflowState("dag-1", "executing", 0.5)
	s.IncrementTasksCompleted()

	// Start and pause to test state serialization
	require.NoError(t, s.Start())
	require.NoError(t, s.Pause())

	// Serialize
	data, err := s.Serialize()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	restored, err := session.DeserializeSession(data)
	require.NoError(t, err)

	assert.Equal(t, s.ID(), restored.ID())
	assert.Equal(t, s.Name(), restored.Name())
	assert.Equal(t, s.Description(), restored.Description())
	assert.Equal(t, s.Branch(), restored.Branch())
	assert.Equal(t, session.StatePaused, restored.State())

	user, ok := restored.GetMetadata("user")
	assert.True(t, ok)
	assert.Equal(t, "test", user)

	dagID, phase, progress := restored.WorkflowState()
	assert.Equal(t, "dag-1", dagID)
	assert.Equal(t, "executing", phase)
	assert.Equal(t, 0.5, progress)
}

func TestSession_Close(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())

	err := s.Close()
	require.NoError(t, err)
	assert.True(t, s.IsClosed())

	// Double close should return error
	err = s.Close()
	assert.Equal(t, session.ErrSessionClosed, err)

	// Operations on closed session
	err = s.Start()
	assert.Equal(t, session.ErrSessionClosed, err)
}

func TestSession_ConcurrentAccess(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	require.NoError(t, s.Start())

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Concurrent metadata access
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					s.SetMetadata("key", i)
					s.GetMetadata("key")
				}
			}
		}(i)
	}

	// Concurrent counter updates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					s.IncrementTasksCompleted()
					s.IncrementMessagesRouted()
				}
			}
		}()
	}

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()

	// Should still be in valid state
	assert.Equal(t, session.StateActive, s.State())
}

// =============================================================================
// Session Manager Tests
// =============================================================================

func TestManager_Create(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	s, err := mgr.Create(context.Background(), session.Config{
		Name: "test-session",
	})

	require.NoError(t, err)
	assert.NotNil(t, s)
	assert.NotEmpty(t, s.ID())
	assert.Equal(t, 1, mgr.Count())
}

func TestManager_Get(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	s, err := mgr.Create(context.Background(), session.Config{Name: "test"})
	require.NoError(t, err)

	// Get by ID
	found, ok := mgr.Get(s.ID())
	assert.True(t, ok)
	assert.Equal(t, s.ID(), found.ID())

	// Get non-existent
	_, ok = mgr.Get("nonexistent")
	assert.False(t, ok)
}

func TestManager_List(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	// Create multiple sessions
	for i := 0; i < 5; i++ {
		_, err := mgr.Create(context.Background(), session.DefaultConfig())
		require.NoError(t, err)
	}

	sessions := mgr.List()
	assert.Len(t, sessions, 5)
}

func TestManager_Switch(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	s1, _ := mgr.Create(context.Background(), session.Config{Name: "session-1"})
	s2, _ := mgr.Create(context.Background(), session.Config{Name: "session-2"})

	// Switch to s1
	err := mgr.Switch(s1.ID())
	require.NoError(t, err)

	active, ok := mgr.GetActive()
	assert.True(t, ok)
	assert.Equal(t, s1.ID(), active.ID())
	assert.Equal(t, session.StateActive, s1.State())

	// Switch to s2
	err = mgr.Switch(s2.ID())
	require.NoError(t, err)

	active, ok = mgr.GetActive()
	assert.True(t, ok)
	assert.Equal(t, s2.ID(), active.ID())

	// s1 should be paused
	assert.Equal(t, session.StatePaused, s1.State())

	// Switch to non-existent
	err = mgr.Switch("nonexistent")
	assert.Equal(t, session.ErrSessionNotFound, err)
}

func TestManager_PauseResume(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	s, _ := mgr.Create(context.Background(), session.DefaultConfig())
	require.NoError(t, mgr.Switch(s.ID())) // Activate

	// Pause
	err := mgr.Pause(s.ID())
	require.NoError(t, err)
	assert.Equal(t, session.StatePaused, s.State())

	// Resume
	err = mgr.Resume(s.ID())
	require.NoError(t, err)
	assert.Equal(t, session.StateActive, s.State())
}

func TestManager_Close(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	s, _ := mgr.Create(context.Background(), session.DefaultConfig())
	id := s.ID()

	err := mgr.Close(id)
	require.NoError(t, err)

	// Should no longer be accessible
	_, ok := mgr.Get(id)
	assert.False(t, ok)
	assert.Equal(t, 0, mgr.Count())

	// Close non-existent
	err = mgr.Close("nonexistent")
	assert.Equal(t, session.ErrSessionNotFound, err)
}

func TestManager_CloseAll(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	for i := 0; i < 5; i++ {
		_, err := mgr.Create(context.Background(), session.DefaultConfig())
		require.NoError(t, err)
	}

	assert.Equal(t, 5, mgr.Count())

	err := mgr.CloseAll()
	require.NoError(t, err)
	assert.Equal(t, 0, mgr.Count())
}

func TestManager_MaxSessions(t *testing.T) {
	mgr := session.NewManager(session.ManagerConfig{
		MaxSessions: 3,
	})

	// Create up to max
	for i := 0; i < 3; i++ {
		_, err := mgr.Create(context.Background(), session.DefaultConfig())
		require.NoError(t, err)
	}

	// Try to create one more
	_, err := mgr.Create(context.Background(), session.DefaultConfig())
	assert.Equal(t, session.ErrMaxSessionsReached, err)
}

func TestManager_Events(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	var events []session.Event
	var mu sync.Mutex

	unsub := mgr.Subscribe(func(e *session.Event) {
		mu.Lock()
		events = append(events, *e)
		mu.Unlock()
	})
	defer unsub()

	// Create session
	s, _ := mgr.Create(context.Background(), session.DefaultConfig())
	time.Sleep(10 * time.Millisecond) // Allow event to be processed

	// Switch (activates)
	mgr.Switch(s.ID())
	time.Sleep(10 * time.Millisecond)

	// Pause
	mgr.Pause(s.ID())
	time.Sleep(10 * time.Millisecond)

	// Resume
	mgr.Resume(s.ID())
	time.Sleep(10 * time.Millisecond)

	// Close
	mgr.Close(s.ID())
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Verify we got events
	assert.GreaterOrEqual(t, len(events), 4)

	// Check event types
	eventTypes := make(map[session.EventType]bool)
	for _, e := range events {
		eventTypes[e.Type] = true
	}

	assert.True(t, eventTypes[session.EventCreated])
	assert.True(t, eventTypes[session.EventSwitched])
	assert.True(t, eventTypes[session.EventPaused])
	assert.True(t, eventTypes[session.EventResumed])
	assert.True(t, eventTypes[session.EventClosed])
}

func TestManager_Stats(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	s1, _ := mgr.Create(context.Background(), session.Config{Name: "s1"})
	s2, _ := mgr.Create(context.Background(), session.Config{Name: "s2"})

	// Activate s1
	mgr.Switch(s1.ID())

	// Pause s2 (after activating it first)
	mgr.Switch(s2.ID())
	mgr.Pause(s2.ID())

	// Switch back to s1
	mgr.Switch(s1.ID())

	stats := mgr.Stats()
	assert.Equal(t, 2, stats.TotalSessions)
	assert.Equal(t, 1, stats.ActiveSessions)
	assert.Equal(t, 1, stats.PausedSessions)
}

func TestManager_Shutdown(t *testing.T) {
	mgr := session.NewManager(session.DefaultManagerConfig())

	for i := 0; i < 3; i++ {
		_, err := mgr.Create(context.Background(), session.DefaultConfig())
		require.NoError(t, err)
	}

	err := mgr.Shutdown()
	require.NoError(t, err)

	// Manager should be closed
	_, err = mgr.Create(context.Background(), session.DefaultConfig())
	assert.Equal(t, session.ErrManagerClosed, err)
}

func TestManager_ConcurrentSessions(t *testing.T) {
	mgr := session.NewManager(session.ManagerConfig{
		MaxSessions: 100,
		NumShards:   16,
	})

	var wg sync.WaitGroup
	var created int64

	// Create sessions concurrently
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := mgr.Create(context.Background(), session.DefaultConfig())
			if err == nil {
				atomic.AddInt64(&created, 1)

				// Do some operations
				s.SetMetadata("test", "value")
				s.IncrementTasksCompleted()
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(50), created)
	assert.Equal(t, 50, mgr.Count())
}

// =============================================================================
// Session Context Tests
// =============================================================================

func TestContext_Basic(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	assert.Equal(t, s.ID(), ctx.ID())
	assert.Equal(t, s.State(), ctx.State())
	assert.NotNil(t, ctx.Context())
}

func TestContext_Services(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	// Set service
	ctx.SetService("test-service", "test-value")

	// Get service
	svc, ok := ctx.GetService("test-service")
	assert.True(t, ok)
	assert.Equal(t, "test-value", svc)

	// Get non-existent
	_, ok = ctx.GetService("nonexistent")
	assert.False(t, ok)
}

func TestContext_RouteCache(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.CacheRoute("route1", "target1")

	v, ok := ctx.GetCachedRoute("route1")
	assert.True(t, ok)
	assert.Equal(t, "target1", v)

	ctx.ClearRouteCache()

	_, ok = ctx.GetCachedRoute("route1")
	assert.False(t, ok)
}

func TestContext_PendingRequests(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	ctx.AddPendingRequest("req1", "data1")
	ctx.AddPendingRequest("req2", "data2")

	assert.Equal(t, 2, ctx.PendingRequestCount())

	r, ok := ctx.GetPendingRequest("req1")
	assert.True(t, ok)
	assert.Equal(t, "data1", r)

	ctx.RemovePendingRequest("req1")
	assert.Equal(t, 1, ctx.PendingRequestCount())

	_, ok = ctx.GetPendingRequest("req1")
	assert.False(t, ok)
}

func TestContext_FromContext(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	ctx := session.NewContext(s)

	// Add to Go context
	goCtx := ctx.ToContext(context.Background())

	// Retrieve from Go context
	retrieved, ok := session.FromContext(goCtx)
	assert.True(t, ok)
	assert.Equal(t, ctx.ID(), retrieved.ID())

	// Not in context
	_, ok = session.FromContext(context.Background())
	assert.False(t, ok)
}

// =============================================================================
// Persistence Tests
// =============================================================================

func TestMemoryPersister(t *testing.T) {
	p := session.NewMemoryPersister()

	s := session.NewSession(session.Config{
		Name: "persist-test",
	})
	s.SetMetadata("key", "value")

	// Save
	err := p.Save(s)
	require.NoError(t, err)

	// Load
	loaded, err := p.Load(s.ID())
	require.NoError(t, err)
	assert.Equal(t, s.ID(), loaded.ID())
	assert.Equal(t, s.Name(), loaded.Name())

	v, ok := loaded.GetMetadata("key")
	assert.True(t, ok)
	assert.Equal(t, "value", v)

	// List
	ids, err := p.List()
	require.NoError(t, err)
	assert.Contains(t, ids, s.ID())

	// LoadAll
	all, err := p.LoadAll()
	require.NoError(t, err)
	assert.Len(t, all, 1)

	// Delete
	err = p.Delete(s.ID())
	require.NoError(t, err)

	// Should be gone
	_, err = p.Load(s.ID())
	assert.Equal(t, session.ErrSessionNotFound, err)
}

func TestManager_WithPersistence(t *testing.T) {
	p := session.NewMemoryPersister()

	mgr := session.NewManager(session.ManagerConfig{
		MaxSessions: 100,
		Persister:   p,
	})

	// Create session with persistence enabled
	s, err := mgr.Create(context.Background(), session.Config{
		Name:               "persist-session",
		PersistenceEnabled: true,
	})
	require.NoError(t, err)

	// Activate and pause (triggers save)
	mgr.Switch(s.ID())
	mgr.Pause(s.ID())

	// Verify persisted
	ids, _ := p.List()
	assert.Contains(t, ids, s.ID())
}

// =============================================================================
// State Tests
// =============================================================================

func TestState_String(t *testing.T) {
	tests := []struct {
		state    session.State
		expected string
	}{
		{session.StateCreated, "created"},
		{session.StateActive, "active"},
		{session.StatePaused, "paused"},
		{session.StateSuspended, "suspended"},
		{session.StateCompleted, "completed"},
		{session.StateFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestState_IsTerminal(t *testing.T) {
	assert.False(t, session.StateCreated.IsTerminal())
	assert.False(t, session.StateActive.IsTerminal())
	assert.False(t, session.StatePaused.IsTerminal())
	assert.False(t, session.StateSuspended.IsTerminal())
	assert.True(t, session.StateCompleted.IsTerminal())
	assert.True(t, session.StateFailed.IsTerminal())
}

func TestState_CanTransitionTo(t *testing.T) {
	// From Created
	assert.True(t, session.StateCreated.CanTransitionTo(session.StateActive))
	assert.True(t, session.StateCreated.CanTransitionTo(session.StateFailed))
	assert.False(t, session.StateCreated.CanTransitionTo(session.StatePaused))
	assert.False(t, session.StateCreated.CanTransitionTo(session.StateCompleted))

	// From Active
	assert.True(t, session.StateActive.CanTransitionTo(session.StatePaused))
	assert.True(t, session.StateActive.CanTransitionTo(session.StateSuspended))
	assert.True(t, session.StateActive.CanTransitionTo(session.StateCompleted))
	assert.True(t, session.StateActive.CanTransitionTo(session.StateFailed))
	assert.False(t, session.StateActive.CanTransitionTo(session.StateCreated))

	// From Terminal
	assert.False(t, session.StateCompleted.CanTransitionTo(session.StateActive))
	assert.False(t, session.StateFailed.CanTransitionTo(session.StateActive))
}

// =============================================================================
// Event Tests
// =============================================================================

func TestEventType_String(t *testing.T) {
	assert.Equal(t, "created", session.EventCreated.String())
	assert.Equal(t, "started", session.EventStarted.String())
	assert.Equal(t, "paused", session.EventPaused.String())
	assert.Equal(t, "resumed", session.EventResumed.String())
	assert.Equal(t, "suspended", session.EventSuspended.String())
	assert.Equal(t, "restored", session.EventRestored.String())
	assert.Equal(t, "completed", session.EventCompleted.String())
	assert.Equal(t, "failed", session.EventFailed.String())
	assert.Equal(t, "closed", session.EventClosed.String())
	assert.Equal(t, "switched", session.EventSwitched.String())
}
