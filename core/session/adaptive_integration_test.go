package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewAdaptiveIntegration(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	if ai == nil {
		t.Fatal("NewAdaptiveIntegration should return non-nil")
	}
	if ai.instances == nil {
		t.Error("instances map should be initialized")
	}
	if ai.SessionCount() != 0 {
		t.Errorf("SessionCount = %d, want 0", ai.SessionCount())
	}
}

func TestNewAdaptiveIntegration_WithConfig(t *testing.T) {
	t.Parallel()

	config := AdaptiveIntegrationConfig{
		DefaultHotCacheSize: 1024 * 1024,
	}

	ai := NewAdaptiveIntegration(config)

	if ai.config.DefaultHotCacheSize != 1024*1024 {
		t.Errorf("DefaultHotCacheSize = %d, want %d", ai.config.DefaultHotCacheSize, 1024*1024)
	}
}

// =============================================================================
// InitForSession Tests
// =============================================================================

func TestAdaptiveIntegration_InitForSession(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	err := ai.InitForSession(context.Background(), "test-session")
	if err != nil {
		t.Fatalf("InitForSession() error = %v", err)
	}
	defer ai.CleanupSession("test-session")

	if !ai.HasSession("test-session") {
		t.Error("should have session after InitForSession")
	}
	if ai.SessionCount() != 1 {
		t.Errorf("SessionCount = %d, want 1", ai.SessionCount())
	}
}

func TestAdaptiveIntegration_InitForSession_Idempotent(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	err := ai.InitForSession(context.Background(), "test-session")
	if err != nil {
		t.Fatalf("first InitForSession() error = %v", err)
	}
	defer ai.CleanupSession("test-session")

	// Second call should be idempotent
	err = ai.InitForSession(context.Background(), "test-session")
	if err != nil {
		t.Errorf("second InitForSession() error = %v", err)
	}

	if ai.SessionCount() != 1 {
		t.Errorf("SessionCount = %d, want 1 (should not duplicate)", ai.SessionCount())
	}
}

func TestAdaptiveIntegration_InitForSession_Multiple(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	sessions := []string{"session-1", "session-2", "session-3"}

	for _, sessionID := range sessions {
		err := ai.InitForSession(context.Background(), sessionID)
		if err != nil {
			t.Fatalf("InitForSession(%s) error = %v", sessionID, err)
		}
	}
	defer ai.Shutdown()

	if ai.SessionCount() != len(sessions) {
		t.Errorf("SessionCount = %d, want %d", ai.SessionCount(), len(sessions))
	}

	for _, sessionID := range sessions {
		if !ai.HasSession(sessionID) {
			t.Errorf("HasSession(%s) = false, want true", sessionID)
		}
	}
}

// =============================================================================
// CleanupSession Tests
// =============================================================================

func TestAdaptiveIntegration_CleanupSession(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})
	ai.InitForSession(context.Background(), "test-session")

	err := ai.CleanupSession("test-session")
	if err != nil {
		t.Errorf("CleanupSession() error = %v", err)
	}

	if ai.HasSession("test-session") {
		t.Error("should not have session after CleanupSession")
	}
	if ai.SessionCount() != 0 {
		t.Errorf("SessionCount = %d, want 0", ai.SessionCount())
	}
}

func TestAdaptiveIntegration_CleanupSession_NonExistent(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	// Cleaning up non-existent session should not error
	err := ai.CleanupSession("non-existent")
	if err != nil {
		t.Errorf("CleanupSession() error = %v, want nil", err)
	}
}

// =============================================================================
// GetForSession Tests
// =============================================================================

func TestAdaptiveIntegration_GetForSession(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})
	ai.InitForSession(context.Background(), "test-session")
	defer ai.CleanupSession("test-session")

	ar := ai.GetForSession("test-session")
	if ar == nil {
		t.Error("GetForSession should return non-nil for existing session")
	}
	if !ar.IsStarted() {
		t.Error("AdaptiveRetrieval should be started")
	}
}

func TestAdaptiveIntegration_GetForSession_NonExistent(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	ar := ai.GetForSession("non-existent")
	if ar != nil {
		t.Error("GetForSession should return nil for non-existent session")
	}
}

// =============================================================================
// State Accessor Tests
// =============================================================================

func TestAdaptiveIntegration_GetState(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})
	ai.InitForSession(context.Background(), "test-session")
	defer ai.CleanupSession("test-session")

	state := ai.GetState("test-session")
	if state == nil {
		t.Error("GetState should return non-nil for existing session")
	}
}

func TestAdaptiveIntegration_GetState_NonExistent(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	state := ai.GetState("non-existent")
	if state != nil {
		t.Error("GetState should return nil for non-existent session")
	}
}

func TestAdaptiveIntegration_GetPrefetcher(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})
	ai.InitForSession(context.Background(), "test-session")
	defer ai.CleanupSession("test-session")

	prefetcher := ai.GetPrefetcher("test-session")
	if prefetcher == nil {
		t.Error("GetPrefetcher should return non-nil for existing session")
	}
}

func TestAdaptiveIntegration_GetSearcher(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})
	ai.InitForSession(context.Background(), "test-session")
	defer ai.CleanupSession("test-session")

	searcher := ai.GetSearcher("test-session")
	if searcher == nil {
		t.Error("GetSearcher should return non-nil for existing session")
	}
}

func TestAdaptiveIntegration_GetHookRegistry(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})
	ai.InitForSession(context.Background(), "test-session")
	defer ai.CleanupSession("test-session")

	registry := ai.GetHookRegistry("test-session")
	if registry == nil {
		t.Error("GetHookRegistry should return non-nil for existing session")
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAdaptiveIntegration_Integrate(t *testing.T) {
	t.Parallel()

	manager := NewManager(DefaultManagerConfig())
	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	unsubscribe := ai.Integrate(manager)
	defer unsubscribe()

	// Create a session - should trigger automatic init
	session, err := manager.Create(context.Background(), Config{Name: "test"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Give event handler time to process
	time.Sleep(50 * time.Millisecond)

	if !ai.HasSession(session.ID()) {
		t.Error("should auto-init adaptive retrieval on session creation")
	}

	// Close session - should trigger automatic cleanup
	err = manager.Close(session.ID())
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Give event handler time to process
	time.Sleep(50 * time.Millisecond)

	if ai.HasSession(session.ID()) {
		t.Error("should auto-cleanup adaptive retrieval on session close")
	}
}

func TestAdaptiveIntegration_Integrate_MultipleEvents(t *testing.T) {
	t.Parallel()

	manager := NewManager(DefaultManagerConfig())
	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	unsubscribe := ai.Integrate(manager)
	defer unsubscribe()

	// Create multiple sessions
	sessionIDs := make([]string, 3)
	for i := range 3 {
		session, _ := manager.Create(context.Background(), Config{})
		sessionIDs[i] = session.ID()
	}

	// Give event handlers time to process
	time.Sleep(100 * time.Millisecond)

	if ai.SessionCount() != 3 {
		t.Errorf("SessionCount = %d, want 3", ai.SessionCount())
	}

	// Close all sessions
	for _, id := range sessionIDs {
		manager.Close(id)
	}

	// Give event handlers time to process
	time.Sleep(100 * time.Millisecond)

	if ai.SessionCount() != 0 {
		t.Errorf("SessionCount = %d, want 0", ai.SessionCount())
	}
}

// =============================================================================
// Shutdown Tests
// =============================================================================

func TestAdaptiveIntegration_Shutdown(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	// Initialize multiple sessions
	for _, id := range []string{"s1", "s2", "s3"} {
		ai.InitForSession(context.Background(), id)
	}

	if ai.SessionCount() != 3 {
		t.Fatalf("SessionCount = %d before Shutdown, want 3", ai.SessionCount())
	}

	ai.Shutdown()

	if ai.SessionCount() != 0 {
		t.Errorf("SessionCount = %d after Shutdown, want 0", ai.SessionCount())
	}
}

func TestAdaptiveIntegration_Shutdown_Unsubscribes(t *testing.T) {
	t.Parallel()

	manager := NewManager(DefaultManagerConfig())
	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	ai.Integrate(manager)
	ai.Shutdown()

	// After shutdown, new sessions should not trigger init
	session, _ := manager.Create(context.Background(), Config{})
	defer manager.Close(session.ID())

	// Give time for any event handler
	time.Sleep(50 * time.Millisecond)

	if ai.HasSession(session.ID()) {
		t.Error("should not auto-init after Shutdown")
	}
}

// =============================================================================
// Stats Tests
// =============================================================================

func TestAdaptiveIntegration_Stats(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})

	ai.InitForSession(context.Background(), "session-1")
	ai.InitForSession(context.Background(), "session-2")
	defer ai.Shutdown()

	stats := ai.Stats()

	if stats.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", stats.SessionCount)
	}
	if len(stats.SessionIDs) != 2 {
		t.Errorf("len(SessionIDs) = %d, want 2", len(stats.SessionIDs))
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestAdaptiveIntegration_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})
	defer ai.Shutdown()

	var wg sync.WaitGroup

	// Concurrent init and cleanup
	for i := range 50 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := "session"
			if idx%2 == 0 {
				_ = ai.InitForSession(context.Background(), sessionID)
			} else {
				_ = ai.CleanupSession(sessionID)
			}
		}(i)
	}

	// Concurrent reads
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ai.GetForSession("session")
			_ = ai.HasSession("session")
			_ = ai.SessionCount()
			_ = ai.Stats()
		}()
	}

	wg.Wait()
}

func TestAdaptiveIntegration_ConcurrentMultipleSessions(t *testing.T) {
	t.Parallel()

	ai := NewAdaptiveIntegration(AdaptiveIntegrationConfig{})
	defer ai.Shutdown()

	var wg sync.WaitGroup

	for i := range 20 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('a'+idx%10))
			_ = ai.InitForSession(context.Background(), sessionID)
			_ = ai.GetState(sessionID)
			_ = ai.GetPrefetcher(sessionID)
		}(i)
	}

	wg.Wait()

	// Should have at most 10 unique sessions
	if ai.SessionCount() > 10 {
		t.Errorf("SessionCount = %d, want <= 10", ai.SessionCount())
	}
}
