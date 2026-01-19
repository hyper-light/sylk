// Package hooks provides tests for AR.9.1: Hook Registration.
package hooks

import (
	"context"
	"testing"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Mock Types for Testing
// =============================================================================

// mockAccessTracker implements AccessTracker for testing.
type mockAccessTracker struct{}

func (m *mockAccessTracker) RecordAccess(id string, turn int, source string) error { return nil }

// mockContentPromoter implements ContentPromoter for testing.
type mockContentPromoter struct{}

func (m *mockContentPromoter) Promote(entry *ctxpkg.ContentEntry) error { return nil }

// mockFailureQuerier implements FailurePatternQuerier for testing.
type mockFailureQuerier struct{}

func (m *mockFailureQuerier) QuerySimilarFailures(
	ctx context.Context,
	query string,
	threshold float64,
) ([]FailurePattern, error) {
	return nil, nil
}

// mockRoutingProvider implements RoutingHistoryProvider for testing.
type mockRoutingProvider struct{}

func (m *mockRoutingProvider) GetRecentRoutings(
	ctx context.Context,
	sessionID string,
	limit int,
) ([]RoutingDecision, error) {
	return nil, nil
}

func (m *mockRoutingProvider) GetRoutingsForPattern(
	ctx context.Context,
	pattern string,
	limit int,
) ([]RoutingDecision, error) {
	return nil, nil
}

// mockWorkflowProvider implements WorkflowContextProvider for testing.
type mockWorkflowProvider struct{}

func (m *mockWorkflowProvider) GetWorkflowContext(
	ctx context.Context,
	sessionID string,
) (*WorkflowContext, error) {
	return nil, nil
}

// mockFocusedAugmenter implements FocusedAugmenter for testing.
type mockFocusedAugmenter struct{}

func (m *mockFocusedAugmenter) AugmentWithBudget(
	ctx context.Context,
	query string,
	maxTokens int,
	scope string,
) (*ctxpkg.AugmentedQuery, error) {
	return nil, nil
}

// mockEvictableContextManager implements EvictableContextManager for testing.
type mockEvictableContextManager struct{}

func (m *mockEvictableContextManager) ForceEvict(agentID string, percent float64) (int, error) {
	return 0, nil
}

// mockObservationLogger implements ObservationLogger for testing.
type mockObservationLogger struct{}

func (m *mockObservationLogger) Log(obs ctxpkg.EpisodeObservation) error { return nil }

// =============================================================================
// Test: RegisterAdaptiveRetrievalHooks
// =============================================================================

func TestRegisterAdaptiveRetrievalHooks_NilRegistry(t *testing.T) {
	t.Parallel()

	err := RegisterAdaptiveRetrievalHooks(nil, nil)
	if err == nil {
		t.Fatal("expected error for nil registry")
	}

	if err.Error() != "registry is nil" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestRegisterAdaptiveRetrievalHooks_NilDependencies(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	err := RegisterAdaptiveRetrievalHooks(registry, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With nil dependencies, no hooks should be registered
	if registry.HookCount() != 0 {
		t.Errorf("expected 0 hooks with nil dependencies, got %d", registry.HookCount())
	}
}

func TestRegisterAdaptiveRetrievalHooks_EmptyDependencies(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{}
	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With empty dependencies, no hooks should be registered
	if registry.HookCount() != 0 {
		t.Errorf("expected 0 hooks with empty dependencies, got %d", registry.HookCount())
	}
}

// =============================================================================
// Test: Global Hooks Registration
// =============================================================================

func TestRegisterAdaptiveRetrievalHooks_GlobalHooks_FailureQuerier(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		FailureQuerier: &mockFailureQuerier{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 1 global prompt hook
	hooks := registry.GetPromptHooksForAgent("*", ctxpkg.HookPhasePrePrompt)
	found := false
	for _, h := range hooks {
		if h.Name() == FailurePatternHookName {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected failure pattern warning hook to be registered globally")
	}
}

func TestRegisterAdaptiveRetrievalHooks_GlobalHooks_AccessTracker(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		AccessTracker: &mockAccessTracker{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have access tracking hook (PostPrompt)
	hooks := registry.GetPromptHooksForAgent("*", ctxpkg.HookPhasePostPrompt)
	found := false
	for _, h := range hooks {
		if h.Name() == AccessTrackingHookName {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected access tracking hook to be registered globally")
	}
}

func TestRegisterAdaptiveRetrievalHooks_GlobalHooks_ContentPromotion(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		AccessTracker:   &mockAccessTracker{},
		ContentPromoter: &mockContentPromoter{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have content promotion hook (PostTool)
	hooks := registry.GetToolCallHooksForAgent("*", ctxpkg.HookPhasePostTool)
	found := false
	for _, h := range hooks {
		if h.Name() == ContentPromotionHookName {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected content promotion hook to be registered globally")
	}
}

func TestRegisterAdaptiveRetrievalHooks_GlobalHooks_EpisodeTracker(t *testing.T) {
	t.Parallel()

	tracker := ctxpkg.NewEpisodeTracker()
	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		EpisodeTracker: tracker,
		ObservationLog: &mockObservationLogger{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have episode init hook (PrePrompt)
	prePromptHooks := registry.GetPromptHooksForAgent("*", ctxpkg.HookPhasePrePrompt)
	foundInit := false
	for _, h := range prePromptHooks {
		if h.Name() == EpisodeInitHookName {
			foundInit = true
			break
		}
	}
	if !foundInit {
		t.Error("expected episode tracker init hook to be registered globally")
	}

	// Should have episode observation hook (PostPrompt)
	postPromptHooks := registry.GetPromptHooksForAgent("*", ctxpkg.HookPhasePostPrompt)
	foundObs := false
	for _, h := range postPromptHooks {
		if h.Name() == EpisodeObservationHookName {
			foundObs = true
			break
		}
	}
	if !foundObs {
		t.Error("expected episode observation hook to be registered globally")
	}

	// Should have search tool observation hook (PostTool)
	postToolHooks := registry.GetToolCallHooksForAgent("*", ctxpkg.HookPhasePostTool)
	foundSearch := false
	for _, h := range postToolHooks {
		if h.Name() == SearchToolObservationHookName {
			foundSearch = true
			break
		}
	}
	if !foundSearch {
		t.Error("expected search tool observation hook to be registered globally")
	}
}

// =============================================================================
// Test: Knowledge Agent Hooks Registration
// =============================================================================

func TestRegisterAdaptiveRetrievalHooks_KnowledgeAgents_PressureEviction(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		ContextManager: &mockEvictableContextManager{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check each knowledge agent has pressure eviction hook
	for _, agentType := range knowledgeAgentTypes {
		hooks := registry.GetPromptHooksForAgent(agentType, ctxpkg.HookPhasePrePrompt)
		found := false
		for _, h := range hooks {
			if h.Name() == PressureEvictionHookName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected pressure eviction hook for %s agent", agentType)
		}
	}
}

func TestRegisterAdaptiveRetrievalHooks_KnowledgeAgents_SpeculativePrefetch(t *testing.T) {
	t.Parallel()

	// Create a prefetcher for testing
	prefetcher := ctxpkg.NewSpeculativePrefetcher(ctxpkg.SpeculativePrefetcherConfig{
		Searcher: nil, // Nil searcher is acceptable for test
	})

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		Prefetcher: prefetcher,
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check each knowledge agent has speculative prefetch hook
	for _, agentType := range knowledgeAgentTypes {
		hooks := registry.GetPromptHooksForAgent(agentType, ctxpkg.HookPhasePrePrompt)
		found := false
		for _, h := range hooks {
			if h.Name() == PrefetchHookName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected speculative prefetch hook for %s agent", agentType)
		}
	}
}

// =============================================================================
// Test: Pipeline Agent Hooks Registration
// =============================================================================

func TestRegisterAdaptiveRetrievalHooks_PipelineAgents_FocusedPrefetch(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		FocusedAugmenter: &mockFocusedAugmenter{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check each pipeline agent has focused prefetch hook
	for _, agentType := range pipelineAgentTypesForReg {
		hooks := registry.GetPromptHooksForAgent(agentType, ctxpkg.HookPhasePrePrompt)
		found := false
		for _, h := range hooks {
			if h.Name() == FocusedPrefetchHookName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected focused prefetch hook for %s agent", agentType)
		}
	}
}

func TestRegisterAdaptiveRetrievalHooks_PipelineAgents_NoKnowledgeHooks(t *testing.T) {
	t.Parallel()

	prefetcher := ctxpkg.NewSpeculativePrefetcher(ctxpkg.SpeculativePrefetcherConfig{
		Searcher: nil, // Nil searcher is acceptable for test
	})

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		Prefetcher:     prefetcher,
		ContextManager: &mockEvictableContextManager{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Pipeline agents should NOT have knowledge agent hooks
	for _, agentType := range pipelineAgentTypesForReg {
		hooks := registry.GetPromptHooksForAgent(agentType, ctxpkg.HookPhasePrePrompt)
		for _, h := range hooks {
			// Speculative prefetch is for knowledge agents only
			if h.Name() == PrefetchHookName {
				t.Errorf("pipeline agent %s should not have speculative prefetch hook", agentType)
			}
			// Pressure eviction is for knowledge agents only
			if h.Name() == PressureEvictionHookName {
				t.Errorf("pipeline agent %s should not have pressure eviction hook", agentType)
			}
		}
	}
}

// =============================================================================
// Test: Guide Agent Hooks Registration
// =============================================================================

func TestRegisterAdaptiveRetrievalHooks_Guide_RoutingCache(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		RoutingProvider: &mockRoutingProvider{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Guide should have routing cache hook
	hooks := registry.GetPromptHooksForAgent("guide", ctxpkg.HookPhasePrePrompt)
	found := false
	for _, h := range hooks {
		if h.Name() == GuideRoutingHookName {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected guide routing cache hook for guide agent")
	}
}

func TestRegisterAdaptiveRetrievalHooks_NonGuide_NoRoutingCache(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		RoutingProvider: &mockRoutingProvider{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Other agents should NOT have routing cache hook
	nonGuideAgents := []string{"librarian", "engineer", "orchestrator"}
	for _, agentType := range nonGuideAgents {
		hooks := registry.GetPromptHooksForAgent(agentType, ctxpkg.HookPhasePrePrompt)
		for _, h := range hooks {
			if h.Name() == GuideRoutingHookName {
				t.Errorf("agent %s should not have guide routing cache hook", agentType)
			}
		}
	}
}

// =============================================================================
// Test: Orchestrator Agent Hooks Registration
// =============================================================================

func TestRegisterAdaptiveRetrievalHooks_Orchestrator_WorkflowContext(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		WorkflowProvider: &mockWorkflowProvider{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Orchestrator should have workflow context hook
	hooks := registry.GetPromptHooksForAgent("orchestrator", ctxpkg.HookPhasePrePrompt)
	found := false
	for _, h := range hooks {
		if h.Name() == WorkflowContextHookName {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected workflow context hook for orchestrator agent")
	}
}

func TestRegisterAdaptiveRetrievalHooks_NonOrchestrator_NoWorkflowContext(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		WorkflowProvider: &mockWorkflowProvider{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Other agents should NOT have workflow context hook
	nonOrchestratorAgents := []string{"librarian", "engineer", "guide"}
	for _, agentType := range nonOrchestratorAgents {
		hooks := registry.GetPromptHooksForAgent(agentType, ctxpkg.HookPhasePrePrompt)
		for _, h := range hooks {
			if h.Name() == WorkflowContextHookName {
				t.Errorf("agent %s should not have workflow context hook", agentType)
			}
		}
	}
}

// =============================================================================
// Test: Full Registration
// =============================================================================

func TestRegisterAdaptiveRetrievalHooks_AllDependencies(t *testing.T) {
	t.Parallel()

	prefetcher := ctxpkg.NewSpeculativePrefetcher(ctxpkg.SpeculativePrefetcherConfig{
		Searcher: nil, // Nil searcher is acceptable for test
	})
	tracker := ctxpkg.NewEpisodeTracker()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		Prefetcher:       prefetcher,
		EpisodeTracker:   tracker,
		ContextManager:   &mockEvictableContextManager{},
		AccessTracker:    &mockAccessTracker{},
		ContentPromoter:  &mockContentPromoter{},
		FailureQuerier:   &mockFailureQuerier{},
		RoutingProvider:  &mockRoutingProvider{},
		WorkflowProvider: &mockWorkflowProvider{},
		FocusedAugmenter: &mockFocusedAugmenter{},
		ObservationLog:   &mockObservationLogger{},
	}

	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have a good number of hooks registered
	totalHooks := registry.HookCount()
	t.Logf("Total hooks registered: %d", totalHooks)
	t.Logf("Prompt hooks: %d", registry.PromptHookCount())
	t.Logf("Tool call hooks: %d", registry.ToolCallHookCount())

	// Expect a minimum number based on configuration
	// With RegisterPromptHookForAgents, one hook instance is shared across agents
	// Global: 4 prompt + 2 tool call = 6
	// Knowledge agents: 2 hooks (prefetch + eviction) shared across 5 agents = 2
	// Pipeline agents: 1 hook (focused prefetch) shared across 4 agents = 1
	// Guide: 1 (routing cache)
	// Orchestrator: 1 (workflow context)
	// Total expected: ~11
	minExpected := 10
	if totalHooks < minExpected {
		t.Errorf("expected at least %d hooks, got %d", minExpected, totalHooks)
	}
}

func TestRegisterAdaptiveRetrievalHooks_Idempotent(t *testing.T) {
	t.Parallel()

	registry := ctxpkg.NewAdaptiveHookRegistry()
	deps := &HookDependencies{
		FailureQuerier: &mockFailureQuerier{},
	}

	// First registration
	err := RegisterAdaptiveRetrievalHooks(registry, deps)
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	initialCount := registry.HookCount()

	// Second registration should fail (hooks already exist)
	err = RegisterAdaptiveRetrievalHooks(registry, deps)
	if err == nil {
		t.Error("expected error on second registration")
	}

	// Count should remain the same
	if registry.HookCount() != initialCount {
		t.Errorf("hook count changed after failed registration: got %d, want %d",
			registry.HookCount(), initialCount)
	}
}

// =============================================================================
// Test: Agent Type Lists
// =============================================================================

func TestKnowledgeAgentTypes_ContainsExpected(t *testing.T) {
	t.Parallel()

	expected := map[string]bool{
		"librarian":   true,
		"archivalist": true,
		"academic":    true,
		"architect":   true,
		"guide":       true,
	}

	for _, agentType := range knowledgeAgentTypes {
		if !expected[agentType] {
			t.Errorf("unexpected knowledge agent type: %s", agentType)
		}
		delete(expected, agentType)
	}

	if len(expected) > 0 {
		for missing := range expected {
			t.Errorf("missing knowledge agent type: %s", missing)
		}
	}
}

func TestPipelineAgentTypesForReg_ContainsExpected(t *testing.T) {
	t.Parallel()

	expected := map[string]bool{
		"engineer":  true,
		"designer":  true,
		"inspector": true,
		"tester":    true,
	}

	for _, agentType := range pipelineAgentTypesForReg {
		if !expected[agentType] {
			t.Errorf("unexpected pipeline agent type: %s", agentType)
		}
		delete(expected, agentType)
	}

	if len(expected) > 0 {
		for missing := range expected {
			t.Errorf("missing pipeline agent type: %s", missing)
		}
	}
}
