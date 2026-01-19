// Package context provides AR.14 integration tests for the adaptive retrieval system.
// These tests verify end-to-end flows including prefetch, tiered search, pressure response,
// learning convergence, handoff preservation, and resource cleanup.
package context

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// End-to-End Prefetch Flow Tests
// =============================================================================

func TestIntegration_PrefetchFlow_EndToEnd(t *testing.T) {
	t.Parallel()

	// Create adaptive retrieval with default config
	config := DefaultAdaptiveRetrievalConfig()
	deps := &AdaptiveRetrievalDeps{}
	ar := New(config, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start the system
	if err := ar.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ar.Stop()

	// Verify system is running
	if !ar.IsStarted() {
		t.Error("expected system to be started")
	}

	// Verify components are accessible
	if ar.GetHotCache() == nil {
		t.Error("expected hot cache to be initialized")
	}
	if ar.GetAdaptiveState() == nil {
		t.Error("expected adaptive state to be initialized")
	}
	if ar.GetPrefetcher() == nil {
		t.Error("expected prefetcher to be initialized")
	}
	if ar.GetHookRegistry() == nil {
		t.Error("expected hook registry to be initialized")
	}
}

func TestIntegration_PrefetchFlow_QueryToAugmentation(t *testing.T) {
	t.Parallel()

	config := DefaultAdaptiveRetrievalConfig()
	deps := &AdaptiveRetrievalDeps{}
	ar := New(config, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := ar.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer ar.Stop()

	// Add content to hot cache
	cache := ar.GetHotCache()
	entry := &ContentEntry{
		ID:       "test-content",
		Content:  "This is relevant test content about Go programming",
		Keywords: []string{"go", "programming"},
	}
	cache.Add(entry.ID, entry)

	// Verify content is retrievable
	retrieved := cache.Get(entry.ID)
	if retrieved == nil {
		t.Error("expected to retrieve content from hot cache")
	}
}

// =============================================================================
// Tiered Search Latency Tests
// =============================================================================

func TestIntegration_TieredSearch_HotCacheLatency(t *testing.T) {
	t.Parallel()

	cache := NewHotCache(HotCacheConfig{MaxSize: 10 * 1024 * 1024})

	// Add test entries
	for i := 0; i < 100; i++ {
		entry := &ContentEntry{
			ID:      "content-" + string(rune('a'+i%26)),
			Content: "Test content",
		}
		cache.Add(entry.ID, entry)
	}

	// Measure hot cache lookup latency
	start := time.Now()
	iterations := 1000

	for i := 0; i < iterations; i++ {
		_ = cache.Get("content-a")
	}

	elapsed := time.Since(start)
	avgLatency := elapsed / time.Duration(iterations)

	// Hot cache should be < 1ms per lookup
	if avgLatency > 1*time.Millisecond {
		t.Errorf("hot cache avg latency = %v, want < 1ms", avgLatency)
	}
}

func TestIntegration_TieredSearch_MissLatency(t *testing.T) {
	t.Parallel()

	cache := NewHotCache(HotCacheConfig{MaxSize: 10 * 1024 * 1024})

	// Measure cache miss latency
	start := time.Now()
	iterations := 1000

	for i := 0; i < iterations; i++ {
		_ = cache.Get("nonexistent")
	}

	elapsed := time.Since(start)
	avgLatency := elapsed / time.Duration(iterations)

	// Cache miss should still be < 1ms
	if avgLatency > 1*time.Millisecond {
		t.Errorf("cache miss avg latency = %v, want < 1ms", avgLatency)
	}
}

// =============================================================================
// Pressure Response Tests
// =============================================================================

func TestIntegration_PressureResponse_CacheEviction(t *testing.T) {
	t.Parallel()

	maxSize := int64(1024) // 1KB
	cache := NewHotCache(HotCacheConfig{MaxSize: maxSize})

	// Fill the cache with content larger than max size
	largeContent := make([]byte, 512)
	for i := range largeContent {
		largeContent[i] = byte('x')
	}

	for i := 0; i < 10; i++ {
		entry := &ContentEntry{
			ID:      "content-" + string(rune('a'+i)),
			Content: string(largeContent),
		}
		cache.Add(entry.ID, entry)
	}

	// Cache should have evicted entries to stay under max size
	if cache.Size() > maxSize*2 { // Allow some overhead
		t.Errorf("cache size = %d, want <= %d (with overhead)", cache.Size(), maxSize*2)
	}
}

func TestIntegration_PressureResponse_EvictPercent(t *testing.T) {
	t.Parallel()

	cache := NewHotCache(HotCacheConfig{MaxSize: 10 * 1024 * 1024})

	// Add entries
	for i := 0; i < 100; i++ {
		entry := &ContentEntry{
			ID:      "content-" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Content: "Test content data",
		}
		cache.Add(entry.ID, entry)
	}

	sizeBefore := cache.Size()

	// Evict 50%
	evicted := cache.EvictPercent(0.5)

	if evicted == 0 {
		t.Error("expected to evict some bytes")
	}

	sizeAfter := cache.Size()
	if sizeAfter >= sizeBefore {
		t.Errorf("size after eviction = %d, want < %d", sizeAfter, sizeBefore)
	}
}

// =============================================================================
// Learning Convergence Tests
// =============================================================================

func TestIntegration_LearningConvergence_PositiveObservations(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	initialWeights := state.GetMeanWeights()
	initialTaskSuccess := initialWeights.TaskSuccess

	// Apply 100 positive observations
	for i := 0; i < 100; i++ {
		obs := EpisodeObservation{
			TaskCompleted: true,
			Timestamp:     time.Now(),
		}
		state.UpdateFromOutcome(obs)
	}

	finalWeights := state.GetMeanWeights()

	// Task success weight should move toward 1.0
	if finalWeights.TaskSuccess <= initialTaskSuccess {
		t.Errorf("TaskSuccess did not increase: initial=%f, final=%f",
			initialTaskSuccess, finalWeights.TaskSuccess)
	}
}

func TestIntegration_LearningConvergence_NegativeObservations(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	initialWeights := state.GetMeanWeights()
	initialStruggle := initialWeights.StrugglePenalty

	// Apply observations indicating struggle
	for i := 0; i < 50; i++ {
		obs := EpisodeObservation{
			TaskCompleted:   false,
			FollowUpCount:   5,
			HedgingDetected: true,
			Timestamp:       time.Now(),
		}
		state.UpdateFromOutcome(obs)
	}

	finalWeights := state.GetMeanWeights()

	// Struggle penalty should adjust
	if finalWeights.StrugglePenalty == initialStruggle {
		t.Error("StrugglePenalty did not adjust after struggle observations")
	}
}

func TestIntegration_LearningConvergence_UserProfile(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// Apply observations that would adjust waste penalty
	for i := 0; i < 30; i++ {
		obs := EpisodeObservation{
			TaskCompleted: true,
			PrefetchedIDs: []string{"p1", "p2", "p3"},
			UsedIDs:       []string{"p1"}, // Only 1 of 3 prefetched used = waste
			Timestamp:     time.Now(),
		}
		state.UpdateFromOutcome(obs)
	}

	profile := state.GetUserProfile()

	// User profile should have been updated
	if state.GetTotalObservations() != 30 {
		t.Errorf("TotalObservations = %d, want 30", state.GetTotalObservations())
	}

	// Profile should exist
	if profile.WastePenaltyMult == 0 {
		t.Error("WastePenaltyMult should be set")
	}
}

// =============================================================================
// Handoff State Preservation Tests
// =============================================================================

func TestIntegration_HandoffStatePreservation_RoundTrip(t *testing.T) {
	t.Parallel()

	// Create and train original state
	originalState := NewAdaptiveState()
	for i := 0; i < 50; i++ {
		originalState.UpdateFromOutcome(EpisodeObservation{
			TaskCompleted: true,
			Timestamp:     time.Now(),
		})
	}

	// Add discovered context
	discovery := originalState.GetContextDiscovery()
	discovery.AddOrUpdateCentroid(ContextCentroid{
		ID:    TaskContext("debugging"),
		Bias:  ContextBias{RelevanceMult: 1.5},
		Count: 25,
	})

	// Build handoff payload
	payload := BuildAdaptiveHandoffPayload(
		originalState, nil,
		"agent-1", "engineer", "session-1",
	)

	// Serialize and deserialize
	data, err := MarshalAdaptiveHandoffPayload(payload)
	if err != nil {
		t.Fatalf("MarshalAdaptiveHandoffPayload error = %v", err)
	}

	restored, err := UnmarshalAdaptiveHandoffPayload(data)
	if err != nil {
		t.Fatalf("UnmarshalAdaptiveHandoffPayload error = %v", err)
	}

	// Apply to new state
	newState := NewAdaptiveState()
	err = ApplyAdaptiveHandoffPayload(restored, newState)
	if err != nil {
		t.Fatalf("ApplyAdaptiveHandoffPayload error = %v", err)
	}

	// Verify learned knowledge transferred
	newDiscovery := newState.GetContextDiscovery()
	if len(newDiscovery.Centroids) != 1 {
		t.Errorf("Centroids length = %d, want 1", len(newDiscovery.Centroids))
	}
	if newDiscovery.Centroids[0].ID != "debugging" {
		t.Error("expected debugging context to transfer")
	}
}

func TestIntegration_HandoffStatePreservation_WeightTransfer(t *testing.T) {
	t.Parallel()

	// Create state with specific weights
	payload := &AdaptiveHandoffPayload{
		MeanWeights: RewardWeights{
			TaskSuccess:     0.95,
			RelevanceBonus:  0.6,
			StrugglePenalty: 0.2,
			WastePenalty:    0.1,
		},
		MeanThresholds: ThresholdConfig{
			Confidence: 0.88,
			Excerpt:    0.92,
			Budget:     0.12,
		},
	}

	newState := NewAdaptiveState()
	err := ApplyAdaptiveHandoffPayload(payload, newState)
	if err != nil {
		t.Fatalf("ApplyAdaptiveHandoffPayload error = %v", err)
	}

	// Verify weights transferred (approximately)
	weights := newState.GetMeanWeights()
	if weights.TaskSuccess < 0.9 {
		t.Errorf("TaskSuccess = %f, want >= 0.9", weights.TaskSuccess)
	}
}

// =============================================================================
// Cross-Session Learning Tests
// =============================================================================

func TestIntegration_CrossSessionLearning_ContextDiscovery(t *testing.T) {
	t.Parallel()

	// Session A: Discover contexts
	stateA := NewAdaptiveState()
	discoveryA := stateA.GetContextDiscovery()
	discoveryA.AddOrUpdateCentroid(ContextCentroid{
		ID:    TaskContext("code_review"),
		Bias:  ContextBias{RelevanceMult: 1.3},
		Count: 50,
	})

	// Build handoff for cross-session transfer
	payload := BuildAdaptiveHandoffPayload(stateA, nil, "agent-a", "reviewer", "session-a")

	// Session B: Apply discovered contexts
	stateB := NewAdaptiveState()
	_ = ApplyAdaptiveHandoffPayload(payload, stateB)

	// Verify Session B has Session A's contexts
	discoveryB := stateB.GetContextDiscovery()
	var foundContext bool
	for _, c := range discoveryB.Centroids {
		if c.ID == "code_review" {
			foundContext = true
			break
		}
	}

	if !foundContext {
		t.Error("context discovered in session A should be available in session B")
	}
}

// =============================================================================
// Goroutine Leak Tests
// =============================================================================

func TestIntegration_NoGoroutineLeaks_StartStop(t *testing.T) {
	t.Parallel()

	// Record baseline goroutine count
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	// Start and stop multiple times
	for i := 0; i < 5; i++ {
		config := DefaultAdaptiveRetrievalConfig()
		deps := &AdaptiveRetrievalDeps{}
		ar := New(config, deps)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := ar.Start(ctx); err != nil {
			cancel()
			t.Fatalf("Start() error = %v", err)
		}
		cancel()

		// Small delay to ensure started
		time.Sleep(10 * time.Millisecond)

		if err := ar.Stop(); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}

	// Allow goroutines to clean up
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	// Check for goroutine leaks
	final := runtime.NumGoroutine()
	leaked := final - baseline

	// Allow for some variance (test framework goroutines, etc.)
	if leaked > 5 {
		t.Errorf("potential goroutine leak: started with %d, ended with %d (leaked %d)",
			baseline, final, leaked)
	}
}

func TestIntegration_NoGoroutineLeaks_ConcurrentOperations(t *testing.T) {
	t.Parallel()

	config := DefaultAdaptiveRetrievalConfig()
	deps := &AdaptiveRetrievalDeps{}
	ar := New(config, deps)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := ar.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	baseline := runtime.NumGoroutine()

	// Perform concurrent operations
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cache := ar.GetHotCache()
			entry := &ContentEntry{
				ID:      "content-" + string(rune('0'+idx%10)),
				Content: "test",
			}
			cache.Add(entry.ID, entry)
			_ = cache.Get(entry.ID)
		}(i)
	}
	wg.Wait()

	// Stop the system
	if err := ar.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	// Allow cleanup
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	final := runtime.NumGoroutine()
	if final > baseline+5 {
		t.Errorf("potential goroutine leak after concurrent ops: baseline=%d, final=%d",
			baseline, final)
	}
}

// =============================================================================
// Memory Leak Tests
// =============================================================================

func TestIntegration_NoMemoryLeaks_BoundedCacheGrowth(t *testing.T) {
	t.Parallel()

	maxSize := int64(1024 * 100) // 100KB
	cache := NewHotCache(HotCacheConfig{MaxSize: maxSize})

	// Add much more than max size
	largeContent := make([]byte, 1024)
	for i := range largeContent {
		largeContent[i] = byte('x')
	}

	for i := 0; i < 1000; i++ {
		entry := &ContentEntry{
			ID:      "content-" + formatTestInt(i),
			Content: string(largeContent),
		}
		cache.Add(entry.ID, entry)
	}

	// Cache size should be bounded
	if cache.Size() > maxSize*2 { // Allow overhead
		t.Errorf("cache size = %d, exceeded max %d", cache.Size(), maxSize)
	}
}

func TestIntegration_NoMemoryLeaks_StateBounded(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	// Apply many observations
	for i := 0; i < 1000; i++ {
		obs := EpisodeObservation{
			TaskCompleted: i%2 == 0,
			Timestamp:     time.Now(),
		}
		state.UpdateFromOutcome(obs)
	}

	// Context discovery should be bounded
	discovery := state.GetContextDiscovery()
	if discovery.MaxContexts > 0 && len(discovery.Centroids) > discovery.MaxContexts {
		t.Errorf("centroids %d exceeded max %d",
			len(discovery.Centroids), discovery.MaxContexts)
	}

	// Total observations should be tracked
	if state.GetTotalObservations() != 1000 {
		t.Errorf("TotalObservations = %d, want 1000", state.GetTotalObservations())
	}
}

// =============================================================================
// Race Detector Compatibility Tests
// =============================================================================

func TestIntegration_RaceDetector_ConcurrentStateAccess(t *testing.T) {
	t.Parallel()

	state := NewAdaptiveState()

	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				state.UpdateFromOutcome(EpisodeObservation{
					TaskCompleted: true,
					Timestamp:     time.Now(),
				})
			}
		}()
	}

	// Concurrent readers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = state.GetMeanWeights()
				_ = state.GetMeanThresholds()
				_ = state.GetUserProfile()
				_ = state.GetTotalObservations()
			}
		}()
	}

	wg.Wait()
}

func TestIntegration_RaceDetector_ConcurrentCacheAccess(t *testing.T) {
	t.Parallel()

	cache := NewHotCache(HotCacheConfig{MaxSize: 10 * 1024 * 1024})

	var wg sync.WaitGroup

	// Concurrent writers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				entry := &ContentEntry{
					ID:      "content-" + formatTestInt(idx*10+j),
					Content: "test content",
				}
				cache.Add(entry.ID, entry)
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = cache.Get("content-" + formatTestInt(idx*10+j))
				_ = cache.Size()
				_ = cache.Keys()
			}
		}(i)
	}

	wg.Wait()
}

func TestIntegration_RaceDetector_ConcurrentDiscoveryAccess(t *testing.T) {
	t.Parallel()

	discovery := &ContextDiscovery{MaxContexts: 100}

	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			centroid := ContextCentroid{
				ID:    TaskContext("ctx-" + formatTestInt(idx)),
				Count: int64(idx),
			}
			discovery.AddOrUpdateCentroid(centroid)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = discovery.GetBias(TaskContext("ctx-" + formatTestInt(idx)))
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// Circuit Breaker / Graceful Degradation Tests
// =============================================================================

func TestIntegration_GracefulDegradation_NilDependencies(t *testing.T) {
	t.Parallel()

	// Create with nil dependencies - should not panic
	config := DefaultAdaptiveRetrievalConfig()
	ar := New(config, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Should start without panic even with nil deps
	err := ar.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should stop cleanly
	err = ar.Stop()
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestIntegration_GracefulDegradation_EmptyPayload(t *testing.T) {
	t.Parallel()

	// Apply empty payload - should not panic
	payload := &AdaptiveHandoffPayload{}
	state := NewAdaptiveState()

	err := ApplyAdaptiveHandoffPayload(payload, state)
	if err != nil {
		t.Errorf("ApplyAdaptiveHandoffPayload with empty payload error = %v", err)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func formatTestInt(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	if i < 100 {
		return string(rune('0'+i/10)) + string(rune('0'+i%10))
	}
	if i < 1000 {
		return string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10))
	}
	return "999+"
}
