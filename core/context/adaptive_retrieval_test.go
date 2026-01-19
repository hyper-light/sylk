package context

import (
	"context"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNew_DefaultConfig(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)

	if ar == nil {
		t.Fatal("New should return non-nil instance")
	}
	if ar.config == nil {
		t.Error("config should be defaulted")
	}
	if ar.deps == nil {
		t.Error("deps should be defaulted")
	}
	if ar.IsStarted() {
		t.Error("should not be started initially")
	}
}

func TestNew_WithConfig(t *testing.T) {
	t.Parallel()

	config := &AdaptiveRetrievalConfig{
		SessionID:             "test-session",
		HotCacheMaxSize:       1024 * 1024,
		PrefetchTimeout:       100 * time.Millisecond,
		MaxInflightPrefetches: 5,
	}

	ar := New(config, nil)

	if ar.SessionID() != "test-session" {
		t.Errorf("SessionID = %s, want test-session", ar.SessionID())
	}
}

func TestDefaultAdaptiveRetrievalConfig(t *testing.T) {
	t.Parallel()

	config := DefaultAdaptiveRetrievalConfig()

	if config.HotCacheMaxSize <= 0 {
		t.Error("HotCacheMaxSize should be positive")
	}
	if config.PrefetchTimeout <= 0 {
		t.Error("PrefetchTimeout should be positive")
	}
	if config.MaxInflightPrefetches <= 0 {
		t.Error("MaxInflightPrefetches should be positive")
	}
}

// =============================================================================
// Lifecycle Tests
// =============================================================================

func TestAdaptiveRetrieval_Start(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)

	err := ar.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ar.Stop()

	if !ar.IsStarted() {
		t.Error("should be started after Start()")
	}
}

func TestAdaptiveRetrieval_Start_InitializesComponents(t *testing.T) {
	t.Parallel()

	ar := New(&AdaptiveRetrievalConfig{
		SessionID: "test-components",
	}, nil)

	err := ar.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ar.Stop()

	if ar.GetHotCache() == nil {
		t.Error("HotCache should be initialized")
	}
	if ar.GetSearcher() == nil {
		t.Error("Searcher should be initialized")
	}
	if ar.GetPrefetcher() == nil {
		t.Error("Prefetcher should be initialized")
	}
	if ar.GetAdaptiveState() == nil {
		t.Error("AdaptiveState should be initialized")
	}
	if ar.GetHookRegistry() == nil {
		t.Error("HookRegistry should be initialized")
	}
	if ar.GetPressureRetrieval() == nil {
		t.Error("PressureRetrieval should be initialized")
	}
	if ar.GetEpisodeTracker() == nil {
		t.Error("EpisodeTracker should be initialized")
	}
	if ar.GetScope() == nil {
		t.Error("Scope should be initialized")
	}
}

func TestAdaptiveRetrieval_Start_Twice(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)

	err := ar.Start(context.Background())
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	defer ar.Stop()

	err = ar.Start(context.Background())
	if err != ErrAdaptiveRetrievalAlreadyStarted {
		t.Errorf("second Start() error = %v, want ErrAdaptiveRetrievalAlreadyStarted", err)
	}
}

func TestAdaptiveRetrieval_Stop(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)

	err := ar.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	err = ar.Stop()
	if err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	if !ar.IsStopped() {
		t.Error("should be stopped after Stop()")
	}
}

func TestAdaptiveRetrieval_Stop_BeforeStart(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)

	err := ar.Stop()
	if err != ErrAdaptiveRetrievalNotStarted {
		t.Errorf("Stop() before Start() error = %v, want ErrAdaptiveRetrievalNotStarted", err)
	}
}

func TestAdaptiveRetrieval_Stop_Twice(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)
	ar.Start(context.Background())

	err := ar.Stop()
	if err != nil {
		t.Errorf("first Stop() error = %v", err)
	}

	err = ar.Stop()
	if err != nil {
		t.Errorf("second Stop() should succeed, got error = %v", err)
	}
}

func TestAdaptiveRetrieval_Start_AfterStop(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)
	ar.Start(context.Background())
	ar.Stop()

	err := ar.Start(context.Background())
	if err != ErrAdaptiveRetrievalClosed {
		t.Errorf("Start() after Stop() error = %v, want ErrAdaptiveRetrievalClosed", err)
	}
}

// =============================================================================
// State Tests
// =============================================================================

func TestAdaptiveRetrieval_StateTransitions(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)

	// Initial state
	if ar.IsStarted() || ar.IsStopped() {
		t.Error("initial state should be uninitialized")
	}

	// After Start
	ar.Start(context.Background())
	if !ar.IsStarted() || ar.IsStopped() {
		t.Error("after Start, should be started")
	}

	// After Stop
	ar.Stop()
	if ar.IsStarted() || !ar.IsStopped() {
		t.Error("after Stop, should be stopped")
	}
}

// =============================================================================
// Component Accessor Tests
// =============================================================================

func TestAdaptiveRetrieval_GetComponents_BeforeStart(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)

	// Should return nil before Start
	if ar.GetHotCache() != nil {
		t.Error("GetHotCache should return nil before Start")
	}
	if ar.GetSearcher() != nil {
		t.Error("GetSearcher should return nil before Start")
	}
	if ar.GetPrefetcher() != nil {
		t.Error("GetPrefetcher should return nil before Start")
	}
	if ar.GetAdaptiveState() != nil {
		t.Error("GetAdaptiveState should return nil before Start")
	}
}

func TestAdaptiveRetrieval_InitialState_Restore(t *testing.T) {
	t.Parallel()

	existingState := NewAdaptiveState()
	// Modify to identify
	existingState.TotalObservations = 42

	config := &AdaptiveRetrievalConfig{
		InitialState: existingState,
	}

	ar := New(config, nil)
	ar.Start(context.Background())
	defer ar.Stop()

	state := ar.GetAdaptiveState()
	if state.GetTotalObservations() != 42 {
		t.Errorf("TotalObservations = %d, want 42", state.GetTotalObservations())
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestAdaptiveRetrieval_Stats(t *testing.T) {
	t.Parallel()

	ar := New(&AdaptiveRetrievalConfig{
		SessionID: "stats-test",
	}, nil)
	ar.Start(context.Background())
	defer ar.Stop()

	stats := ar.Stats()

	if stats.SessionID != "stats-test" {
		t.Errorf("SessionID = %s, want stats-test", stats.SessionID)
	}
	if !stats.IsStarted {
		t.Error("IsStarted should be true")
	}
}

func TestAdaptiveRetrieval_Stats_BeforeStart(t *testing.T) {
	t.Parallel()

	ar := New(&AdaptiveRetrievalConfig{
		SessionID: "no-start",
	}, nil)

	stats := ar.Stats()

	if stats.SessionID != "no-start" {
		t.Errorf("SessionID = %s, want no-start", stats.SessionID)
	}
	if stats.IsStarted {
		t.Error("IsStarted should be false before Start")
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestAdaptiveRetrieval_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	ar := New(&AdaptiveRetrievalConfig{
		SessionID: "concurrent-test",
	}, nil)
	ar.Start(context.Background())
	defer ar.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ar.GetHotCache()
			_ = ar.GetSearcher()
			_ = ar.GetPrefetcher()
			_ = ar.GetAdaptiveState()
			_ = ar.Stats()
			_ = ar.IsStarted()
		}()
	}

	wg.Wait()
}

func TestAdaptiveRetrieval_ConcurrentStartStop(t *testing.T) {
	t.Parallel()

	// Create multiple instances and start/stop them concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ar := New(nil, nil)
			_ = ar.Start(context.Background())
			_ = ar.Stats()
			_ = ar.Stop()
		}()
	}

	wg.Wait()
}

// =============================================================================
// Error Tests
// =============================================================================

func TestAdaptiveRetrievalErrors(t *testing.T) {
	t.Parallel()

	if ErrAdaptiveRetrievalClosed == nil {
		t.Error("ErrAdaptiveRetrievalClosed should be defined")
	}
	if ErrAdaptiveRetrievalAlreadyStarted == nil {
		t.Error("ErrAdaptiveRetrievalAlreadyStarted should be defined")
	}
	if ErrAdaptiveRetrievalNotStarted == nil {
		t.Error("ErrAdaptiveRetrievalNotStarted should be defined")
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAdaptiveRetrieval_ComponentIntegration(t *testing.T) {
	t.Parallel()

	ar := New(&AdaptiveRetrievalConfig{
		SessionID:       "integration-test",
		HotCacheMaxSize: 1024 * 1024,
	}, nil)

	err := ar.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ar.Stop()

	// Verify components are wired correctly
	// HotCache should be the same instance in Searcher
	hotCache := ar.GetHotCache()
	searcher := ar.GetSearcher()

	if searcher.HotCache() != hotCache {
		t.Error("Searcher should use the same HotCache instance")
	}

	// Prefetcher should use the same Searcher
	prefetcher := ar.GetPrefetcher()
	if prefetcher.Searcher() != searcher {
		t.Error("Prefetcher should use the same Searcher instance")
	}

	// PressureRetrieval should reference the same components
	pressureRetrieval := ar.GetPressureRetrieval()
	if pressureRetrieval == nil {
		t.Error("PressureRetrieval should be initialized")
	}
}

func TestAdaptiveRetrieval_PrefetcherDisabledAfterStop(t *testing.T) {
	t.Parallel()

	ar := New(nil, nil)
	ar.Start(context.Background())

	prefetcher := ar.GetPrefetcher()
	if !prefetcher.IsEnabled() {
		t.Error("Prefetcher should be enabled after Start")
	}

	ar.Stop()

	// After stop, the prefetcher's CancelAll should have been called
	// Check that inflight count is 0
	if prefetcher.InflightCount() != 0 {
		t.Errorf("InflightCount = %d after Stop, want 0", prefetcher.InflightCount())
	}
}
