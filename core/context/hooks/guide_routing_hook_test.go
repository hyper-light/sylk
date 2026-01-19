package hooks

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Test Helpers
// =============================================================================

// MockRoutingProvider implements RoutingHistoryProvider for testing.
type MockRoutingProvider struct {
	mu                   sync.Mutex
	recentRoutings       []RoutingDecision
	patternRoutings      map[string][]RoutingDecision
	recentErr            error
	patternErr           error
	recentCalls          []recentRoutingCall
	patternCalls         []patternRoutingCall
}

type recentRoutingCall struct {
	sessionID string
	limit     int
}

type patternRoutingCall struct {
	pattern string
	limit   int
}

func NewMockRoutingProvider() *MockRoutingProvider {
	return &MockRoutingProvider{
		patternRoutings: make(map[string][]RoutingDecision),
		recentCalls:     make([]recentRoutingCall, 0),
		patternCalls:    make([]patternRoutingCall, 0),
	}
}

func (m *MockRoutingProvider) GetRecentRoutings(
	ctx context.Context,
	sessionID string,
	limit int,
) ([]RoutingDecision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recentCalls = append(m.recentCalls, recentRoutingCall{
		sessionID: sessionID,
		limit:     limit,
	})

	if m.recentErr != nil {
		return nil, m.recentErr
	}

	if limit > 0 && len(m.recentRoutings) > limit {
		return m.recentRoutings[:limit], nil
	}
	return m.recentRoutings, nil
}

func (m *MockRoutingProvider) GetRoutingsForPattern(
	ctx context.Context,
	pattern string,
	limit int,
) ([]RoutingDecision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.patternCalls = append(m.patternCalls, patternRoutingCall{
		pattern: pattern,
		limit:   limit,
	})

	if m.patternErr != nil {
		return nil, m.patternErr
	}

	if routings, ok := m.patternRoutings[pattern]; ok {
		if limit > 0 && len(routings) > limit {
			return routings[:limit], nil
		}
		return routings, nil
	}

	return nil, nil
}

func (m *MockRoutingProvider) SetRecentRoutings(routings []RoutingDecision) {
	m.mu.Lock()
	m.recentRoutings = routings
	m.mu.Unlock()
}

func (m *MockRoutingProvider) SetPatternRoutings(pattern string, routings []RoutingDecision) {
	m.mu.Lock()
	m.patternRoutings[pattern] = routings
	m.mu.Unlock()
}

func (m *MockRoutingProvider) SetRecentError(err error) {
	m.mu.Lock()
	m.recentErr = err
	m.mu.Unlock()
}

func (m *MockRoutingProvider) SetPatternError(err error) {
	m.mu.Lock()
	m.patternErr = err
	m.mu.Unlock()
}

func (m *MockRoutingProvider) GetRecentCalls() []recentRoutingCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recentCalls
}

func (m *MockRoutingProvider) GetPatternCalls() []patternRoutingCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.patternCalls
}

func createGuideRoutingTestData(agentType, query string) *ctxpkg.PromptHookData {
	return &ctxpkg.PromptHookData{
		SessionID:     "test-session",
		AgentID:       "test-agent",
		AgentType:     agentType,
		TurnNumber:    1,
		Query:         query,
		Timestamp:     time.Now(),
	}
}

func createTestRoutingDecision(target, reason string, confidence float64) RoutingDecision {
	return RoutingDecision{
		MessageID:   "msg-123",
		TargetAgent: target,
		Confidence:  confidence,
		Reason:      reason,
		Timestamp:   time.Now(),
	}
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewGuideRoutingCacheHook_DefaultConfig(t *testing.T) {
	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{})

	if hook.maxHistory != DefaultMaxRoutingHistory {
		t.Errorf("maxHistory = %d, want %d", hook.maxHistory, DefaultMaxRoutingHistory)
	}

	if !hook.enabled {
		t.Error("Hook should be enabled by default")
	}
}

func TestNewGuideRoutingCacheHook_CustomConfig(t *testing.T) {
	provider := NewMockRoutingProvider()
	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
		MaxHistory:      5,
	})

	if hook.routingProvider != provider {
		t.Error("RoutingProvider not set correctly")
	}

	if hook.maxHistory != 5 {
		t.Errorf("maxHistory = %d, want 5", hook.maxHistory)
	}
}

// =============================================================================
// Hook Interface Tests
// =============================================================================

func TestGuideRoutingCacheHook_Name(t *testing.T) {
	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{})

	if hook.Name() != GuideRoutingHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), GuideRoutingHookName)
	}
}

func TestGuideRoutingCacheHook_Phase(t *testing.T) {
	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("Phase() = %v, want HookPhasePrePrompt", hook.Phase())
	}
}

func TestGuideRoutingCacheHook_Priority(t *testing.T) {
	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityNormal) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityNormal)
	}
}

func TestGuideRoutingCacheHook_Enabled(t *testing.T) {
	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{})

	if !hook.Enabled() {
		t.Error("Hook should be enabled by default")
	}

	hook.SetEnabled(false)

	if hook.Enabled() {
		t.Error("Hook should be disabled after SetEnabled(false)")
	}
}

// =============================================================================
// Execute Tests - Agent Type Filtering
// =============================================================================

func TestGuideRoutingHook_OnlyAppliesToGuide(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		createTestRoutingDecision("librarian", "code query", 0.9),
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "how do I fix this?")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("Should modify data for Guide agent")
	}

	if data.PrefetchedContent == nil {
		t.Error("PrefetchedContent should be set")
	}
}

func TestGuideRoutingHook_SkipsNonGuideAgents(t *testing.T) {
	nonGuideAgents := []string{"librarian", "engineer", "architect", "orchestrator"}

	for _, agent := range nonGuideAgents {
		t.Run(agent, func(t *testing.T) {
			provider := NewMockRoutingProvider()
			provider.SetRecentRoutings([]RoutingDecision{
				createTestRoutingDecision("librarian", "test", 0.9),
			})

			hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
				RoutingProvider: provider,
			})

			ctx := context.Background()
			data := createGuideRoutingTestData(agent, "test query")

			result, err := hook.Execute(ctx, data)

			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}

			if result.Modified {
				t.Errorf("Should not modify data for %s agent", agent)
			}
		})
	}
}

func TestGuideRoutingHook_HandlesGuideAgentCaseInsensitive(t *testing.T) {
	variants := []string{"guide", "Guide", "GUIDE", "GuIdE"}

	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			provider := NewMockRoutingProvider()
			provider.SetRecentRoutings([]RoutingDecision{
				createTestRoutingDecision("librarian", "test", 0.9),
			})

			hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
				RoutingProvider: provider,
			})

			ctx := context.Background()
			data := createGuideRoutingTestData(variant, "test query")

			result, err := hook.Execute(ctx, data)

			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}

			if !result.Modified {
				t.Errorf("Should modify data for %s agent", variant)
			}
		})
	}
}

// =============================================================================
// Execute Tests - Routing History Injection
// =============================================================================

func TestGuideRoutingHook_InjectsRoutingHistory(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		createTestRoutingDecision("librarian", "code query detected", 0.92),
		createTestRoutingDecision("architect", "design question", 0.85),
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "how do I refactor this?")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("Should modify data")
	}

	if data.PrefetchedContent == nil {
		t.Fatal("PrefetchedContent should be set")
	}

	if len(data.PrefetchedContent.Excerpts) == 0 {
		t.Fatal("Should have at least one excerpt")
	}

	content := data.PrefetchedContent.Excerpts[0].Content
	if content == "" {
		t.Error("Excerpt content should not be empty")
	}

	// Verify content structure
	if !containsString(content, "[ROUTING_HISTORY]") {
		t.Error("Content should contain [ROUTING_HISTORY] header")
	}

	if !containsString(content, "librarian") {
		t.Error("Content should contain 'librarian' agent")
	}

	if !containsString(content, "architect") {
		t.Error("Content should contain 'architect' agent")
	}
}

func TestGuideRoutingHook_UsesPatternMatchFirst(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		createTestRoutingDecision("librarian", "recent", 0.7),
	})
	provider.SetPatternRoutings("refactor", []RoutingDecision{
		createTestRoutingDecision("engineer", "pattern match", 0.95),
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "refactor")

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// Check that pattern matching was attempted first
	patternCalls := provider.GetPatternCalls()
	if len(patternCalls) == 0 {
		t.Error("Should have called GetRoutingsForPattern")
	}

	// Content should contain engineer from pattern match
	content := data.PrefetchedContent.Excerpts[0].Content
	if !containsString(content, "engineer") {
		t.Error("Content should contain engineer from pattern match")
	}
}

func TestGuideRoutingHook_FallsBackToRecent(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		createTestRoutingDecision("librarian", "fallback", 0.8),
	})
	// No pattern routings set

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "unknown query")

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// Should have called both
	recentCalls := provider.GetRecentCalls()
	if len(recentCalls) == 0 {
		t.Error("Should have called GetRecentRoutings as fallback")
	}

	// Content should contain librarian from recent
	content := data.PrefetchedContent.Excerpts[0].Content
	if !containsString(content, "librarian") {
		t.Error("Content should contain librarian from recent routings")
	}
}

// =============================================================================
// Execute Tests - Edge Cases
// =============================================================================

func TestGuideRoutingHook_SkipsWhenDisabled(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		createTestRoutingDecision("librarian", "test", 0.9),
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test query")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}
}

func TestGuideRoutingHook_SkipsNilProvider(t *testing.T) {
	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: nil,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test query")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil provider")
	}
}

func TestGuideRoutingHook_SkipsEmptyHistory(t *testing.T) {
	provider := NewMockRoutingProvider()
	// No routings set

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test query")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with empty history")
	}
}

func TestGuideRoutingHook_HandlesProviderError(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetPatternError(errors.New("pattern query failed"))
	provider.SetRecentError(errors.New("recent query failed"))

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test query")

	result, err := hook.Execute(ctx, data)

	// Hook should not return error to caller
	if err != nil {
		t.Fatalf("Execute should not return error, got %v", err)
	}

	// But result should contain the error
	if result.Error == nil {
		t.Error("Result.Error should contain the provider error")
	}
}

func TestGuideRoutingHook_PreservesExistingContent(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		createTestRoutingDecision("librarian", "test", 0.9),
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test query")

	// Pre-populate with existing content
	data.PrefetchedContent = &ctxpkg.AugmentedQuery{
		OriginalQuery: "test query",
		Excerpts: []ctxpkg.Excerpt{
			{Source: "existing", Content: "existing content", Confidence: 0.8},
		},
	}

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// Should have both routing and existing excerpts
	if len(data.PrefetchedContent.Excerpts) < 2 {
		t.Errorf("Expected at least 2 excerpts, got %d", len(data.PrefetchedContent.Excerpts))
	}

	// Routing should be first (prepended)
	if data.PrefetchedContent.Excerpts[0].Source != "routing_history" {
		t.Error("Routing history should be prepended as first excerpt")
	}

	// Existing content should still be present
	found := false
	for _, ex := range data.PrefetchedContent.Excerpts {
		if ex.Source == "existing" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Existing content should be preserved")
	}
}

func TestGuideRoutingHook_RespectsMaxHistory(t *testing.T) {
	provider := NewMockRoutingProvider()
	// Set more routings than maxHistory
	routings := make([]RoutingDecision, 15)
	for i := 0; i < 15; i++ {
		routings[i] = createTestRoutingDecision("agent", "reason", 0.9)
	}
	provider.SetRecentRoutings(routings)

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
		MaxHistory:      5,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test query")

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// Verify limit was passed to provider
	recentCalls := provider.GetRecentCalls()
	if len(recentCalls) == 0 {
		t.Fatal("Should have called GetRecentRoutings")
	}

	if recentCalls[0].limit != 5 {
		t.Errorf("Limit = %d, want 5", recentCalls[0].limit)
	}
}

// =============================================================================
// ToPromptHook Tests
// =============================================================================

func TestGuideRoutingHook_ToPromptHook_ReturnsValidHook(t *testing.T) {
	provider := NewMockRoutingProvider()
	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != GuideRoutingHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), GuideRoutingHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePrePrompt", promptHook.Phase())
	}

	if promptHook.Priority() != int(ctxpkg.HookPriorityNormal) {
		t.Errorf("PromptHook Priority = %d, want %d", promptHook.Priority(), ctxpkg.HookPriorityNormal)
	}
}

func TestGuideRoutingHook_ToPromptHook_ExecutesCorrectly(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		createTestRoutingDecision("librarian", "code query", 0.9),
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	promptHook := hook.ToPromptHook()

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test query")

	result, err := promptHook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("PromptHook Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("PromptHook should modify data")
	}

	if data.PrefetchedContent == nil {
		t.Error("PromptHook should set PrefetchedContent")
	}
}

// =============================================================================
// Content Format Tests
// =============================================================================

func TestGuideRoutingHook_ContentFormat(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		{
			MessageID:   "msg-1",
			TargetAgent: "librarian",
			Confidence:  0.92,
			Reason:      "Code analysis request",
			Timestamp:   time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		},
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test")

	_, _ = hook.Execute(ctx, data)

	content := data.PrefetchedContent.Excerpts[0].Content

	// Verify format elements
	expectedParts := []string{
		"[ROUTING_HISTORY]",
		"Recent routing decisions for consistency",
		"### Routing 1",
		"92%",           // Confidence
		"**Target**: librarian",
		"**Reason**: Code analysis request",
		"Consider these past decisions",
	}

	for _, part := range expectedParts {
		if !containsString(content, part) {
			t.Errorf("Content missing expected part: %s", part)
		}
	}
}

func TestGuideRoutingHook_ContentWithoutConfidence(t *testing.T) {
	provider := NewMockRoutingProvider()
	provider.SetRecentRoutings([]RoutingDecision{
		{
			TargetAgent: "librarian",
			Confidence:  0, // Zero confidence
			Reason:      "test",
		},
	})

	hook := NewGuideRoutingCacheHook(GuideRoutingCacheHookConfig{
		RoutingProvider: provider,
	})

	ctx := context.Background()
	data := createGuideRoutingTestData("guide", "test")

	_, _ = hook.Execute(ctx, data)

	content := data.PrefetchedContent.Excerpts[0].Content

	// Should not show confidence when it's 0
	if containsString(content, "confidence:") {
		t.Error("Should not show confidence when it's 0")
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(haystack == needle ||
			len(needle) == 0 ||
			(len(haystack) > 0 && findSubstring(haystack, needle)))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
