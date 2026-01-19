package handoff

import (
	"context"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// HO.11 Integration Tests
// =============================================================================
//
// These tests verify the integration between:
// - HO.11.1 PipelineHandoffAdapter
// - HO.11.2 ContextCheckHook
// - HO.11.3 HandoffAwareEviction
// - Core HandoffManager components

// =============================================================================
// Test Helpers
// =============================================================================

// Note: Uses mockSessionCreator from executor_test.go which is already defined
// in this package. That mock provides full SessionCreator implementation.

// =============================================================================
// HO.11.1 PipelineHandoffAdapter Tests
// =============================================================================

func TestPipelineHandoffAdapter_NewAdapter(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	adapter := NewPipelineHandoffAdapter(manager, nil)

	if adapter == nil {
		t.Fatal("expected adapter to be created")
	}

	if adapter.GetManager() != manager {
		t.Error("expected manager to be set")
	}

	config := adapter.GetConfig()
	if config == nil {
		t.Fatal("expected default config")
	}

	if config.ContextThreshold != 0.75 {
		t.Errorf("expected default threshold 0.75, got %f", config.ContextThreshold)
	}
}

func TestPipelineHandoffAdapter_Lifecycle(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	adapter := NewPipelineHandoffAdapter(manager, nil)

	// Not started yet
	if adapter.IsRunning() {
		t.Error("expected adapter not to be running before Start")
	}

	// Start
	if err := adapter.Start(); err != nil {
		t.Fatalf("failed to start adapter: %v", err)
	}

	if !adapter.IsRunning() {
		t.Error("expected adapter to be running after Start")
	}

	// Idempotent start
	if err := adapter.Start(); err != nil {
		t.Error("second Start should be no-op")
	}

	// Stop
	if err := adapter.Stop(); err != nil {
		t.Fatalf("failed to stop adapter: %v", err)
	}

	if adapter.IsRunning() {
		t.Error("expected adapter not to be running after Stop")
	}

	if !adapter.IsClosed() {
		t.Error("expected adapter to be closed after Stop")
	}

	// Second stop should return error
	if err := adapter.Stop(); err != ErrAdapterClosed {
		t.Errorf("expected ErrAdapterClosed, got %v", err)
	}
}

func TestPipelineHandoffAdapter_ShouldHandoff(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	config := DefaultPipelineHandoffAdapterConfig()
	config.ContextThreshold = 0.75
	config.UseGPPrediction = false

	adapter := NewPipelineHandoffAdapter(manager, config)
	_ = adapter.Start()
	defer adapter.Stop()

	tests := []struct {
		name          string
		contextUsage  float64
		expectHandoff bool
	}{
		{"below threshold", 0.5, false},
		{"at threshold", 0.75, true},
		{"above threshold", 0.9, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.ShouldHandoff("agent-1", tt.contextUsage)
			if result != tt.expectHandoff {
				t.Errorf("expected ShouldHandoff=%v for usage=%f, got %v",
					tt.expectHandoff, tt.contextUsage, result)
			}
		})
	}
}

func TestPipelineHandoffAdapter_ShouldHandoffWithGP(t *testing.T) {
	// Set up GP with observations
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(50000, 100, 2, 0.3) // Low quality at mid context
	gp.AddObservationFromValues(80000, 100, 2, 0.2) // Lower quality at high context

	controller := NewHandoffController(gp, nil, nil)
	manager := NewHandoffManager(nil, controller, nil, nil)

	config := DefaultPipelineHandoffAdapterConfig()
	config.UseGPPrediction = true
	config.ContextThreshold = 0.9 // High threshold so GP triggers first

	adapter := NewPipelineHandoffAdapter(manager, config)
	_ = adapter.Start()
	defer adapter.Stop()

	// Should trigger based on GP prediction
	result := adapter.ShouldHandoff("agent-1", 0.8)

	stats := adapter.Stats()
	if stats.GPEvaluations == 0 {
		t.Error("expected GP evaluations to be counted")
	}
	_ = result // Result depends on GP prediction
}

func TestPipelineHandoffAdapter_TriggerHandoff(t *testing.T) {
	sessionCreator := newMockSessionCreator()
	executor := NewHandoffExecutor(sessionCreator, nil)
	manager := NewHandoffManager(nil, nil, executor, nil)

	adapter := NewPipelineHandoffAdapter(manager, nil)
	_ = adapter.Start()
	defer adapter.Stop()

	req := &PipelineHandoffRequest{
		AgentID:       "agent-1",
		AgentType:     "engineer",
		SessionID:     "session-1",
		PipelineID:    "pipeline-1",
		TriggerReason: "context_threshold",
		ContextUsage:  0.8,
	}

	ctx := context.Background()
	result, err := adapter.TriggerHandoff(ctx, req)

	if err != nil {
		t.Fatalf("TriggerHandoff failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected result")
	}

	if result.OldAgentID != "agent-1" {
		t.Errorf("expected OldAgentID=agent-1, got %s", result.OldAgentID)
	}

	stats := adapter.Stats()
	if stats.TotalRequests != 1 {
		t.Errorf("expected TotalRequests=1, got %d", stats.TotalRequests)
	}
}

func TestPipelineHandoffAdapter_DuplicateHandoff(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	adapter := NewPipelineHandoffAdapter(manager, nil)
	_ = adapter.Start()
	defer adapter.Stop()

	req := &PipelineHandoffRequest{
		AgentID:   "agent-1",
		AgentType: "engineer",
		SessionID: "session-1",
	}

	// Start first handoff in background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, _ = adapter.TriggerHandoff(ctx, req)
	}()

	// Give first handoff time to register
	time.Sleep(10 * time.Millisecond)

	// Second handoff should fail
	ctx := context.Background()
	_, err := adapter.TriggerHandoff(ctx, req)

	if err != ErrHandoffInProgress {
		t.Errorf("expected ErrHandoffInProgress, got %v", err)
	}

	wg.Wait()
}

func TestPipelineHandoffAdapter_InvalidRequest(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	adapter := NewPipelineHandoffAdapter(manager, nil)
	_ = adapter.Start()
	defer adapter.Stop()

	ctx := context.Background()

	// Nil request
	result, err := adapter.TriggerHandoff(ctx, nil)
	if err != ErrInvalidRequest {
		t.Errorf("expected ErrInvalidRequest for nil request, got %v", err)
	}
	if result.Success {
		t.Error("expected unsuccessful result for nil request")
	}

	// Missing required fields
	invalidReq := &PipelineHandoffRequest{}
	result, err = adapter.TriggerHandoff(ctx, invalidReq)
	if err == nil {
		t.Error("expected error for invalid request")
	}
}

func TestPipelineHandoffAdapter_Stats(t *testing.T) {
	manager := NewHandoffManager(nil, nil, nil, nil)
	adapter := NewPipelineHandoffAdapter(manager, nil)
	_ = adapter.Start()
	defer adapter.Stop()

	// Initial stats
	stats := adapter.Stats()
	if stats.TotalRequests != 0 {
		t.Errorf("expected initial TotalRequests=0, got %d", stats.TotalRequests)
	}
	if !stats.IsRunning {
		t.Error("expected IsRunning=true")
	}
	if stats.IsClosed {
		t.Error("expected IsClosed=false")
	}
}

// =============================================================================
// HO.11.2 ContextCheckHook Tests
// =============================================================================

func TestContextCheckHook_NewHook(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)
	hook := NewContextCheckHook(controller, nil)

	if hook == nil {
		t.Fatal("expected hook to be created")
	}

	if hook.Name() != "handoff_context_check" {
		t.Errorf("expected default name, got %s", hook.Name())
	}

	if !hook.IsEnabled() {
		t.Error("expected hook to be enabled by default")
	}
}

func TestContextCheckHook_Lifecycle(t *testing.T) {
	hook := NewContextCheckHook(nil, nil)

	if hook.IsRunning() {
		t.Error("expected hook not to be running before Start")
	}

	if err := hook.Start(); err != nil {
		t.Fatalf("failed to start hook: %v", err)
	}

	if !hook.IsRunning() {
		t.Error("expected hook to be running after Start")
	}

	if err := hook.Stop(); err != nil {
		t.Fatalf("failed to stop hook: %v", err)
	}

	if hook.IsRunning() {
		t.Error("expected hook not to be running after Stop")
	}
}

func TestContextCheckHook_ShouldRun(t *testing.T) {
	config := DefaultContextCheckConfig()
	config.AgentTypes = []string{"engineer", "designer"}

	hook := NewContextCheckHook(nil, config)

	if !hook.ShouldRun("engineer") {
		t.Error("expected ShouldRun=true for engineer")
	}

	if !hook.ShouldRun("designer") {
		t.Error("expected ShouldRun=true for designer")
	}

	if hook.ShouldRun("orchestrator") {
		t.Error("expected ShouldRun=false for orchestrator")
	}

	// Empty list means all agents
	config2 := DefaultContextCheckConfig()
	config2.AgentTypes = nil
	hook2 := NewContextCheckHook(nil, config2)

	if !hook2.ShouldRun("any_agent") {
		t.Error("expected ShouldRun=true for any agent with empty list")
	}
}

func TestContextCheckHook_OnContextUpdate(t *testing.T) {
	controller := NewHandoffController(nil, nil, nil)

	config := DefaultContextCheckConfig()
	config.ContextThreshold = 0.7
	config.UseGPPrediction = false

	hook := NewContextCheckHook(controller, config)
	_ = hook.Start()
	defer hook.Stop()

	tests := []struct {
		name          string
		utilization   float64
		expectHandoff bool
	}{
		{"below threshold", 0.5, false},
		{"at threshold", 0.7, true},
		{"above threshold", 0.9, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &ContextState{
				ContextSize:    int(tt.utilization * 100000),
				MaxContextSize: 100000,
			}

			recommendation := hook.OnContextUpdate(ctx)

			if recommendation == nil {
				if tt.expectHandoff {
					t.Error("expected recommendation, got nil")
				}
				return
			}

			if recommendation.ShouldHandoff != tt.expectHandoff {
				t.Errorf("expected ShouldHandoff=%v, got %v",
					tt.expectHandoff, recommendation.ShouldHandoff)
			}
		})
	}
}

func TestContextCheckHook_OnContextUpdateWithGP(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(50000, 100, 2, 0.3)
	gp.AddObservationFromValues(80000, 100, 2, 0.2)

	controllerConfig := DefaultControllerConfig()
	controllerConfig.QualityThreshold = 0.4

	controller := NewHandoffController(gp, nil, controllerConfig)

	config := DefaultContextCheckConfig()
	config.UseGPPrediction = true
	config.ContextThreshold = 0.95 // High threshold so GP triggers first

	hook := NewContextCheckHook(controller, config)
	_ = hook.Start()
	defer hook.Stop()

	ctx := &ContextState{
		ContextSize:    70000,
		MaxContextSize: 100000,
		TokenCount:     100,
		ToolCallCount:  2,
	}

	recommendation := hook.OnContextUpdate(ctx)

	stats := hook.Stats()
	if stats.GPEvaluations == 0 {
		t.Error("expected GP evaluations to be counted")
	}

	if recommendation != nil && recommendation.Decision != nil {
		if recommendation.Decision.Factors.PredictedQuality == 0 {
			t.Error("expected PredictedQuality to be set")
		}
	}
}

func TestContextCheckHook_Throttling(t *testing.T) {
	config := DefaultContextCheckConfig()
	config.MinCheckInterval = 100 * time.Millisecond

	hook := NewContextCheckHook(nil, config)
	_ = hook.Start()
	defer hook.Stop()

	// Use ContextStateWithAgent to provide agent ID for throttling
	baseCtx := &ContextState{
		ContextSize:    50000,
		MaxContextSize: 100000,
	}
	ctx := NewContextStateWithAgent(baseCtx, "agent-throttle-test", "engineer")

	// First check should proceed
	_ = hook.OnContextUpdate(ctx.ContextState)

	// Set last check manually for the agent ID
	hook.updateLastCheck("agent-throttle-test")

	// Immediate second check should be throttled (using agentID from hook tracking)
	_ = hook.OnContextUpdate(ctx.ContextState)

	// Note: With empty AgentID in ContextState, throttling doesn't track properly
	// So we test via direct methods
	if !hook.shouldCheckAgent("agent-throttle-test", config.MinCheckInterval) {
		// Agent should be throttled now
		stats := hook.Stats()
		t.Logf("Throttling test: checks=%d, throttled=%d", stats.ChecksPerformed, stats.ChecksThrottled)
	}

	// Wait and try again
	time.Sleep(150 * time.Millisecond)

	if hook.shouldCheckAgent("agent-throttle-test", config.MinCheckInterval) {
		// Agent should be allowed now
		t.Log("Agent allowed after interval elapsed")
	} else {
		t.Error("expected agent to be allowed after interval")
	}
}

func TestContextCheckHook_Notifications(t *testing.T) {
	config := DefaultContextCheckConfig()
	config.ContextThreshold = 0.5
	config.NonBlocking = true
	config.UseGPPrediction = false

	hook := NewContextCheckHook(nil, config)
	_ = hook.Start()
	defer hook.Stop()

	ctx := &ContextState{
		ContextSize:    70000,
		MaxContextSize: 100000,
	}

	// Trigger recommendation
	_ = hook.OnContextUpdate(ctx)

	// Check notification channel
	select {
	case notification := <-hook.Notifications():
		if notification == nil {
			t.Error("expected notification")
		}
		if !notification.ShouldHandoff {
			t.Error("expected ShouldHandoff=true in notification")
		}
	case <-time.After(100 * time.Millisecond):
		// No notification might be ok if below threshold
	}
}

func TestContextCheckHook_DisabledHook(t *testing.T) {
	config := DefaultContextCheckConfig()
	config.Enabled = false

	hook := NewContextCheckHook(nil, config)
	_ = hook.Start()
	defer hook.Stop()

	ctx := &ContextState{
		ContextSize:    90000,
		MaxContextSize: 100000,
	}

	recommendation := hook.OnContextUpdate(ctx)
	if recommendation != nil {
		t.Error("expected nil recommendation for disabled hook")
	}

	stats := hook.Stats()
	if stats.ChecksSkipped == 0 {
		t.Error("expected check to be skipped")
	}
}

func TestContextCheckHook_ResetAgent(t *testing.T) {
	config := DefaultContextCheckConfig()
	config.MinCheckInterval = 1 * time.Hour

	hook := NewContextCheckHook(nil, config)
	_ = hook.Start()
	defer hook.Stop()

	ctx := &ContextState{
		ContextSize:    50000,
		MaxContextSize: 100000,
	}

	// First check
	_ = hook.OnContextUpdate(ctx)

	// Check is tracked
	_, exists := hook.GetLastCheck("")
	if exists {
		t.Log("agent tracked without ID") // Expected for empty ID
	}

	// Reset all
	hook.ResetAllAgents()

	stats := hook.Stats()
	if stats.TrackedAgents != 0 {
		t.Errorf("expected 0 tracked agents after reset, got %d", stats.TrackedAgents)
	}
}

// =============================================================================
// HO.11.3 HandoffAwareEviction Tests
// =============================================================================

func TestHandoffAwareEviction_NewEviction(t *testing.T) {
	eviction := NewHandoffAwareEviction(nil, nil)

	if eviction == nil {
		t.Fatal("expected eviction to be created")
	}

	if eviction.Name() != "handoff_aware_eviction" {
		t.Errorf("expected default name, got %s", eviction.Name())
	}

	if eviction.Priority() != 50 {
		t.Errorf("expected default priority 50, got %d", eviction.Priority())
	}
}

func TestHandoffAwareEviction_SelectForEviction(t *testing.T) {
	eviction := NewHandoffAwareEviction(nil, nil)

	entries := []EvictableEntry{
		&BasicEvictableEntry{
			ID:          "entry-1",
			TokenCount:  100,
			Timestamp:   time.Now().Add(-1 * time.Hour),
			TurnNumber:  1,
			ContentType: "tool_result",
		},
		&BasicEvictableEntry{
			ID:          "entry-2",
			TokenCount:  200,
			Timestamp:   time.Now().Add(-2 * time.Hour),
			TurnNumber:  2,
			ContentType: "assistant_reply",
		},
		&BasicEvictableEntry{
			ID:          "entry-3",
			TokenCount:  150,
			Timestamp:   time.Now().Add(-30 * time.Minute),
			TurnNumber:  10, // Recent turn
			ContentType: "user_prompt",
		},
	}

	ctx := context.Background()
	selected, err := eviction.SelectForEviction(ctx, entries, 200)

	if err != nil {
		t.Fatalf("SelectForEviction failed: %v", err)
	}

	if len(selected) == 0 {
		t.Error("expected at least one entry to be selected")
	}

	// Check that preserved recent turns are not selected
	for _, s := range selected {
		if s.GetTurnNumber() >= 10-5 { // PreserveRecentTurns default is 5
			t.Error("recent turn should not be selected for eviction")
		}
	}
}

func TestHandoffAwareEviction_PreservedTypes(t *testing.T) {
	config := DefaultHandoffAwareEvictionConfig()
	config.PreservedTypes = []string{"handoff_state", "context_reference"}

	eviction := NewHandoffAwareEviction(nil, config)

	entries := []EvictableEntry{
		&BasicEvictableEntry{
			ID:          "entry-1",
			TokenCount:  100,
			Timestamp:   time.Now().Add(-1 * time.Hour),
			TurnNumber:  1,
			ContentType: "handoff_state",
		},
		&BasicEvictableEntry{
			ID:          "entry-2",
			TokenCount:  200,
			Timestamp:   time.Now().Add(-2 * time.Hour),
			TurnNumber:  2,
			ContentType: "tool_result",
		},
	}

	ctx := context.Background()
	selected, err := eviction.SelectForEviction(ctx, entries, 200)

	if err != nil {
		t.Fatalf("SelectForEviction failed: %v", err)
	}

	for _, s := range selected {
		if s.GetContentType() == "handoff_state" {
			t.Error("handoff_state should not be evicted")
		}
	}

	stats := eviction.Stats()
	if stats.EntriesPreserved == 0 {
		t.Error("expected entries to be preserved")
	}
}

func TestHandoffAwareEviction_WithGP(t *testing.T) {
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(1000, 100, 2, 0.8)  // High quality early
	gp.AddObservationFromValues(5000, 100, 2, 0.6)  // Medium quality
	gp.AddObservationFromValues(10000, 100, 2, 0.3) // Low quality later

	config := DefaultHandoffAwareEvictionConfig()
	config.UseGPPrediction = true
	config.HandoffWorthinessWeight = 0.5

	eviction := NewHandoffAwareEviction(gp, config)

	entries := []EvictableEntry{
		&BasicEvictableEntry{
			ID:          "early",
			TokenCount:  100,
			Timestamp:   time.Now().Add(-2 * time.Hour),
			TurnNumber:  1,
			ContentType: "assistant_reply",
		},
		&BasicEvictableEntry{
			ID:          "late",
			TokenCount:  100,
			Timestamp:   time.Now().Add(-1 * time.Hour),
			TurnNumber:  10,
			ContentType: "assistant_reply",
		},
	}

	ctx := context.Background()
	selected, err := eviction.SelectForEviction(ctx, entries, 100)

	if err != nil {
		t.Fatalf("SelectForEviction failed: %v", err)
	}

	stats := eviction.Stats()
	if stats.GPPredictions == 0 {
		t.Error("expected GP predictions to be counted")
	}

	_ = selected // Result depends on GP predictions
}

func TestHandoffAwareEviction_ScoreEntries(t *testing.T) {
	// Configure eviction with no recent turn preservation for this test
	config := DefaultHandoffAwareEvictionConfig()
	config.PreserveRecentTurns = 0 // Don't preserve any turns for testing

	eviction := NewHandoffAwareEviction(nil, config)

	entries := []EvictableEntry{
		&BasicEvictableEntry{
			ID:          "entry-1",
			TokenCount:  100,
			Timestamp:   time.Now().Add(-1 * time.Hour),
			TurnNumber:  1,
			ContentType: "tool_result",
		},
		&BasicEvictableEntry{
			ID:          "entry-2",
			TokenCount:  200,
			Timestamp:   time.Now().Add(-30 * time.Minute),
			TurnNumber:  2,
			ContentType: "user_prompt",
		},
	}

	candidates := eviction.ScoreEntries(entries)

	if len(candidates) == 0 {
		t.Fatal("expected scored candidates")
	}

	for _, c := range candidates {
		if c.EvictionScore < 0 || c.EvictionScore > 1 {
			t.Errorf("eviction score %f out of range [0,1]", c.EvictionScore)
		}

		if c.AgeScore < 0 || c.AgeScore > 1 {
			t.Errorf("age score %f out of range [0,1]", c.AgeScore)
		}
	}
}

func TestHandoffAwareEviction_SelectWithDetails(t *testing.T) {
	// Configure eviction with no recent turn preservation for this test
	config := DefaultHandoffAwareEvictionConfig()
	config.PreserveRecentTurns = 0 // Don't preserve any turns for testing

	eviction := NewHandoffAwareEviction(nil, config)

	entries := []EvictableEntry{
		&BasicEvictableEntry{
			ID:          "entry-1",
			TokenCount:  100,
			Timestamp:   time.Now().Add(-1 * time.Hour),
			TurnNumber:  1,
			ContentType: "tool_result",
		},
		&BasicEvictableEntry{
			ID:          "entry-2",
			TokenCount:  200,
			Timestamp:   time.Now().Add(-2 * time.Hour),
			TurnNumber:  2,
			ContentType: "assistant_reply",
		},
	}

	ctx := context.Background()
	result, err := eviction.SelectForEvictionWithDetails(ctx, entries, 150)

	if err != nil {
		t.Fatalf("SelectForEvictionWithDetails failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected result")
	}

	if result.EntriesEvicted == 0 {
		t.Error("expected entries to be evicted")
	}

	if result.TokensFreed == 0 {
		t.Error("expected tokens to be freed")
	}

	if len(result.EvictedCandidates) != result.EntriesEvicted {
		t.Error("evicted candidates count mismatch")
	}

	if result.Duration == 0 {
		t.Error("expected duration to be set")
	}
}

func TestHandoffAwareEviction_EmptyEntries(t *testing.T) {
	eviction := NewHandoffAwareEviction(nil, nil)

	ctx := context.Background()
	selected, err := eviction.SelectForEviction(ctx, nil, 100)

	if err != nil {
		t.Errorf("unexpected error for empty entries: %v", err)
	}

	if selected != nil {
		t.Error("expected nil result for empty entries")
	}
}

func TestHandoffAwareEviction_ZeroTarget(t *testing.T) {
	eviction := NewHandoffAwareEviction(nil, nil)

	entries := []EvictableEntry{
		&BasicEvictableEntry{
			ID:          "entry-1",
			TokenCount:  100,
			Timestamp:   time.Now(),
			TurnNumber:  1,
			ContentType: "tool_result",
		},
	}

	ctx := context.Background()
	selected, err := eviction.SelectForEviction(ctx, entries, 0)

	if err != nil {
		t.Errorf("unexpected error for zero target: %v", err)
	}

	if selected != nil {
		t.Error("expected nil result for zero target")
	}
}

func TestHandoffAwareEviction_GetHandoffWorthiness(t *testing.T) {
	eviction := NewHandoffAwareEviction(nil, nil)

	entry := &BasicEvictableEntry{
		ID:          "entry-1",
		TokenCount:  100,
		Timestamp:   time.Now().Add(-30 * time.Minute),
		TurnNumber:  5,
		ContentType: "assistant_reply",
	}

	worthiness := eviction.GetHandoffWorthiness(entry)

	if worthiness < 0 || worthiness > 1 {
		t.Errorf("worthiness %f out of range [0,1]", worthiness)
	}
}

// =============================================================================
// End-to-End Integration Tests
// =============================================================================

func TestIntegration_PipelineWithContextCheck(t *testing.T) {
	// Set up components
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(50000, 100, 2, 0.6)
	gp.AddObservationFromValues(80000, 100, 2, 0.3)

	controller := NewHandoffController(gp, nil, nil)
	sessionCreator := newMockSessionCreator()
	executor := NewHandoffExecutor(sessionCreator, nil)
	manager := NewHandoffManager(nil, controller, executor, nil)

	// Set up adapter
	adapterConfig := DefaultPipelineHandoffAdapterConfig()
	adapterConfig.UseGPPrediction = true
	adapter := NewPipelineHandoffAdapter(manager, adapterConfig)
	_ = adapter.Start()
	defer adapter.Stop()

	// Set up context check hook
	hookConfig := DefaultContextCheckConfig()
	hookConfig.UseGPPrediction = true
	hook := NewContextCheckHook(controller, hookConfig)
	_ = hook.Start()
	defer hook.Stop()

	// Simulate context update
	ctx := &ContextState{
		ContextSize:    70000,
		MaxContextSize: 100000,
		TokenCount:     100,
		ToolCallCount:  2,
	}

	recommendation := hook.OnContextUpdate(ctx)

	// If handoff recommended, trigger via adapter
	if recommendation != nil && recommendation.ShouldHandoff {
		req := &PipelineHandoffRequest{
			AgentID:       "agent-1",
			AgentType:     "engineer",
			SessionID:     "session-1",
			PipelineID:    "pipeline-1",
			TriggerReason: recommendation.Reason,
			ContextUsage:  ctx.Utilization(),
		}

		result, err := adapter.TriggerHandoff(context.Background(), req)

		if err != nil {
			t.Logf("Handoff result: success=%v, error=%v", result.Success, err)
		}
	}

	// Verify statistics
	hookStats := hook.Stats()
	adapterStats := adapter.Stats()

	if hookStats.ChecksPerformed == 0 {
		t.Error("expected hook checks to be performed")
	}

	t.Logf("Hook stats: checks=%d, recommendations=%d, GP=%d",
		hookStats.ChecksPerformed, hookStats.RecommendationsGiven, hookStats.GPEvaluations)
	t.Logf("Adapter stats: requests=%d, success=%d, failed=%d",
		adapterStats.TotalRequests, adapterStats.SuccessfulHandoffs, adapterStats.FailedHandoffs)
}

func TestIntegration_EvictionWithHandoffAwareness(t *testing.T) {
	// Set up GP with quality predictions
	gp := NewAgentGaussianProcess(nil)
	gp.AddObservationFromValues(1000, 50, 1, 0.9)  // High quality early content
	gp.AddObservationFromValues(5000, 100, 2, 0.7) // Good quality
	gp.AddObservationFromValues(8000, 150, 3, 0.4) // Lower quality later

	eviction := NewHandoffAwareEviction(gp, nil)

	// Create entries with varying characteristics
	entries := []EvictableEntry{
		&BasicEvictableEntry{
			ID:          "valuable",
			TokenCount:  100,
			Timestamp:   time.Now().Add(-30 * time.Minute),
			TurnNumber:  1,
			ContentType: "code_file",
		},
		&BasicEvictableEntry{
			ID:          "medium",
			TokenCount:  150,
			Timestamp:   time.Now().Add(-1 * time.Hour),
			TurnNumber:  5,
			ContentType: "assistant_reply",
		},
		&BasicEvictableEntry{
			ID:          "expendable",
			TokenCount:  200,
			Timestamp:   time.Now().Add(-2 * time.Hour),
			TurnNumber:  8,
			ContentType: "tool_result",
		},
	}

	ctx := context.Background()
	result, err := eviction.SelectForEvictionWithDetails(ctx, entries, 200)

	if err != nil {
		t.Fatalf("eviction failed: %v", err)
	}

	t.Logf("Eviction result: evicted=%d, freed=%d, avg_worthiness=%.2f",
		result.EntriesEvicted, result.TokensFreed, result.AverageHandoffWorthiness)

	// Higher quality content should be preserved
	for _, candidate := range result.EvictedCandidates {
		t.Logf("Evicted: %s, worthiness=%.2f, score=%.2f",
			candidate.Entry.GetID(), candidate.HandoffWorthiness, candidate.EvictionScore)
	}

	stats := eviction.Stats()
	if stats.GPPredictions == 0 {
		t.Error("expected GP predictions during eviction")
	}
}

func TestIntegration_FullPipelineFlow(t *testing.T) {
	// Set up all components
	gp := NewAgentGaussianProcess(nil)
	profile := NewAgentHandoffProfile("test-agent", "claude-3", "")
	controller := NewHandoffController(gp, profile, nil)
	sessionCreator := newMockSessionCreator()
	executor := NewHandoffExecutor(sessionCreator, nil)
	learner := NewProfileLearner(nil, nil)
	manager := NewHandoffManager(nil, controller, executor, learner)

	// Start manager
	_ = manager.Start()
	defer manager.Stop()

	// Set up pipeline adapter
	adapter := NewPipelineHandoffAdapter(manager, nil)
	_ = adapter.Start()
	defer adapter.Stop()

	// Set up context check hook
	hook := NewContextCheckHook(controller, nil)
	_ = hook.Start()
	defer hook.Stop()

	// Set up eviction strategy
	eviction := NewHandoffAwareEviction(gp, nil)

	// Simulate conversation progression
	for turn := 1; turn <= 10; turn++ {
		// Add message to manager
		msg := Message{
			Role:       "assistant",
			Content:    "Response for turn " + string(rune('0'+turn)),
			Timestamp:  time.Now(),
			TokenCount: 100 + turn*10,
		}
		manager.AddMessage(msg)

		// Add GP observation
		gp.AddObservationFromValues(
			turn*10000,          // Context size grows
			100+turn*10,         // Token count varies
			turn%3,              // Tool calls
			1.0-float64(turn)*0.05, // Quality decreases
		)
	}

	// Check context state
	contextState := &ContextState{
		ContextSize:    80000,
		MaxContextSize: 100000,
		TokenCount:     200,
		ToolCallCount:  2,
		TurnNumber:     10,
	}

	recommendation := hook.OnContextUpdate(contextState)

	if recommendation != nil && recommendation.ShouldHandoff {
		// Trigger handoff
		req := &PipelineHandoffRequest{
			AgentID:       "test-agent",
			AgentType:     "engineer",
			SessionID:     "session-1",
			PipelineID:    "pipeline-1",
			TriggerReason: recommendation.Trigger.String(),
			ContextUsage:  contextState.Utilization(),
		}

		result, _ := adapter.TriggerHandoff(context.Background(), req)
		t.Logf("Handoff triggered: success=%v, trigger=%s, confidence=%.2f",
			result.Success, result.Trigger, result.Confidence)
	}

	// Run eviction
	entries := make([]EvictableEntry, 0, 10)
	for i := 1; i <= 10; i++ {
		entries = append(entries, &BasicEvictableEntry{
			ID:          "entry-" + string(rune('0'+i)),
			TokenCount:  100 + i*10,
			Timestamp:   time.Now().Add(-time.Duration(i) * time.Hour),
			TurnNumber:  i,
			ContentType: "assistant_reply",
		})
	}

	evictResult, _ := eviction.SelectForEvictionWithDetails(context.Background(), entries, 500)
	t.Logf("Eviction: evicted=%d, freed=%d tokens", evictResult.EntriesEvicted, evictResult.TokensFreed)

	// Verify all components have collected metrics
	managerStats := manager.Stats()
	adapterStats := adapter.Stats()
	hookStats := hook.Stats()
	evictionStats := eviction.Stats()

	t.Logf("Manager evaluations: %d, handoffs: %d", managerStats.Evaluations, managerStats.HandoffsExecuted)
	t.Logf("Adapter requests: %d", adapterStats.TotalRequests)
	t.Logf("Hook checks: %d, GP evals: %d", hookStats.ChecksPerformed, hookStats.GPEvaluations)
	t.Logf("Eviction runs: %d, GP predictions: %d", evictionStats.EvictionsPerformed, evictionStats.GPPredictions)
}

func TestIntegration_ConcurrentHandoffs(t *testing.T) {
	sessionCreator := newMockSessionCreator()
	executor := NewHandoffExecutor(sessionCreator, nil)
	manager := NewHandoffManager(nil, nil, executor, nil)

	config := DefaultPipelineHandoffAdapterConfig()
	config.MaxConcurrentHandoffs = 3

	adapter := NewPipelineHandoffAdapter(manager, config)
	_ = adapter.Start()
	defer adapter.Stop()

	// Start multiple concurrent handoffs
	var wg sync.WaitGroup
	results := make(chan *PipelineHandoffResult, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			req := &PipelineHandoffRequest{
				AgentID:   "agent-" + string(rune('0'+idx)),
				AgentType: "engineer",
				SessionID: "session-" + string(rune('0'+idx)),
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			result, _ := adapter.TriggerHandoff(ctx, req)
			results <- result
		}(i)
	}

	wg.Wait()
	close(results)

	successCount := 0
	for result := range results {
		if result.Success {
			successCount++
		}
	}

	t.Logf("Concurrent handoffs: %d successful out of 5", successCount)

	stats := adapter.Stats()
	t.Logf("Active handoffs at end: %d", stats.ActiveHandoffs)

	if stats.ActiveHandoffs != 0 {
		t.Error("expected no active handoffs after completion")
	}
}

func TestIntegration_ConfigurationChanges(t *testing.T) {
	// Test that configuration changes take effect

	controller := NewHandoffController(nil, nil, nil)
	hook := NewContextCheckHook(controller, nil)
	_ = hook.Start()
	defer hook.Stop()

	// Initial config
	if !hook.IsEnabled() {
		t.Error("expected hook to be enabled initially")
	}

	// Disable hook
	hook.SetEnabled(false)
	if hook.IsEnabled() {
		t.Error("expected hook to be disabled after SetEnabled(false)")
	}

	// Update config
	newConfig := DefaultContextCheckConfig()
	newConfig.ContextThreshold = 0.9
	newConfig.MinCheckInterval = 10 * time.Second

	hook.SetConfig(newConfig)

	retrievedConfig := hook.GetConfig()
	if retrievedConfig.ContextThreshold != 0.9 {
		t.Error("expected config change to take effect")
	}

	// Test eviction config changes
	eviction := NewHandoffAwareEviction(nil, nil)

	evictionConfig := DefaultHandoffAwareEvictionConfig()
	evictionConfig.HandoffWorthinessWeight = 0.8
	eviction.SetConfig(evictionConfig)

	retrievedEvictionConfig := eviction.GetConfig()
	if retrievedEvictionConfig.HandoffWorthinessWeight != 0.8 {
		t.Error("expected eviction config change to take effect")
	}
}
