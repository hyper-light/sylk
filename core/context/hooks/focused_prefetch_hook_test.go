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

// MockFocusedAugmenter implements FocusedAugmenter for testing.
type MockFocusedAugmenter struct {
	mu             sync.Mutex
	augmentedQuery *ctxpkg.AugmentedQuery
	augmentErr     error
	calls          []augmentCall
}

type augmentCall struct {
	query     string
	maxTokens int
	scope     string
}

func NewMockFocusedAugmenter() *MockFocusedAugmenter {
	return &MockFocusedAugmenter{
		calls: make([]augmentCall, 0),
	}
}

func (m *MockFocusedAugmenter) AugmentWithBudget(
	ctx context.Context,
	query string,
	maxTokens int,
	scope string,
) (*ctxpkg.AugmentedQuery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, augmentCall{
		query:     query,
		maxTokens: maxTokens,
		scope:     scope,
	})

	if m.augmentErr != nil {
		return nil, m.augmentErr
	}

	return m.augmentedQuery, nil
}

func (m *MockFocusedAugmenter) SetAugmentedQuery(aq *ctxpkg.AugmentedQuery) {
	m.mu.Lock()
	m.augmentedQuery = aq
	m.mu.Unlock()
}

func (m *MockFocusedAugmenter) SetError(err error) {
	m.mu.Lock()
	m.augmentErr = err
	m.mu.Unlock()
}

func (m *MockFocusedAugmenter) GetCalls() []augmentCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func createFocusedPrefetchTestData(agentType, query string) *ctxpkg.PromptHookData {
	return &ctxpkg.PromptHookData{
		SessionID:     "test-session",
		AgentID:       "test-agent",
		AgentType:     agentType,
		TurnNumber:    1,
		Query:         query,
		Timestamp:     time.Now(),
	}
}

func createTestAugmentedQuery() *ctxpkg.AugmentedQuery {
	return &ctxpkg.AugmentedQuery{
		OriginalQuery: "test query",
		Excerpts: []ctxpkg.Excerpt{
			{
				Source:     "test.go",
				Content:    "func TestSomething() {}",
				Confidence: 0.85,
				TokenCount: 50,
			},
		},
	}
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewFocusedPrefetchHook_DefaultConfig(t *testing.T) {
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{})

	if hook.maxTokens != DefaultFocusedMaxTokens {
		t.Errorf("maxTokens = %d, want %d", hook.maxTokens, DefaultFocusedMaxTokens)
	}

	if !hook.enabled {
		t.Error("Hook should be enabled by default")
	}
}

func TestNewFocusedPrefetchHook_CustomConfig(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
		MaxTokens: 500,
	})

	if hook.augmenter != augmenter {
		t.Error("Augmenter not set correctly")
	}

	if hook.maxTokens != 500 {
		t.Errorf("maxTokens = %d, want 500", hook.maxTokens)
	}
}

// =============================================================================
// Hook Interface Tests
// =============================================================================

func TestFocusedPrefetchHook_Name(t *testing.T) {
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{})

	if hook.Name() != FocusedPrefetchHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), FocusedPrefetchHookName)
	}
}

func TestFocusedPrefetchHook_Phase(t *testing.T) {
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("Phase() = %v, want HookPhasePrePrompt", hook.Phase())
	}
}

func TestFocusedPrefetchHook_Priority(t *testing.T) {
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityEarly) {
		t.Errorf("Priority() = %d, want %d (HookPriorityEarly)", hook.Priority(), ctxpkg.HookPriorityEarly)
	}
}

func TestFocusedPrefetchHook_Enabled(t *testing.T) {
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{})

	if !hook.Enabled() {
		t.Error("Hook should be enabled by default")
	}

	hook.SetEnabled(false)

	if hook.Enabled() {
		t.Error("Hook should be disabled after SetEnabled(false)")
	}
}

func TestFocusedPrefetchHook_GetMaxTokens(t *testing.T) {
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		MaxTokens: 750,
	})

	if hook.GetMaxTokens() != 750 {
		t.Errorf("GetMaxTokens() = %d, want 750", hook.GetMaxTokens())
	}
}

// =============================================================================
// Execute Tests - Agent Type Filtering
// =============================================================================

func TestFocusedPrefetchHook_AppliesToPipelineAgents(t *testing.T) {
	pipelineAgents := []string{"engineer", "designer", "inspector", "tester"}

	for _, agent := range pipelineAgents {
		t.Run(agent, func(t *testing.T) {
			augmenter := NewMockFocusedAugmenter()
			augmenter.SetAugmentedQuery(createTestAugmentedQuery())

			hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
				Augmenter: augmenter,
			})

			ctx := context.Background()
			data := createFocusedPrefetchTestData(agent, "implement this feature")

			result, err := hook.Execute(ctx, data)

			if err != nil {
				t.Fatalf("Execute error = %v", err)
			}

			if !result.Modified {
				t.Errorf("Should modify data for %s agent", agent)
			}

			if data.PrefetchedContent == nil {
				t.Errorf("PrefetchedContent should be set for %s agent", agent)
			}
		})
	}
}

func TestFocusedPrefetchHook_SkipsNonPipelineAgents(t *testing.T) {
	nonPipelineAgents := []string{"librarian", "archivalist", "academic", "architect", "guide", "orchestrator"}

	for _, agent := range nonPipelineAgents {
		t.Run(agent, func(t *testing.T) {
			augmenter := NewMockFocusedAugmenter()
			augmenter.SetAugmentedQuery(createTestAugmentedQuery())

			hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
				Augmenter: augmenter,
			})

			ctx := context.Background()
			data := createFocusedPrefetchTestData(agent, "test query")

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

func TestFocusedPrefetchHook_HandlesCaseInsensitiveAgentType(t *testing.T) {
	variants := []string{"engineer", "Engineer", "ENGINEER", "EnGiNeEr"}

	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			augmenter := NewMockFocusedAugmenter()
			augmenter.SetAugmentedQuery(createTestAugmentedQuery())

			hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
				Augmenter: augmenter,
			})

			ctx := context.Background()
			data := createFocusedPrefetchTestData(variant, "test query")

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
// Execute Tests - Token Budget
// =============================================================================

func TestFocusedPrefetchHook_PassesMaxTokensToBudget(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
		MaxTokens: 1000,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "implement feature")

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	calls := augmenter.GetCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(calls))
	}

	if calls[0].maxTokens != 1000 {
		t.Errorf("maxTokens = %d, want 1000", calls[0].maxTokens)
	}
}

func TestFocusedPrefetchHook_DefaultMaxTokensIs1000(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
		// No MaxTokens specified
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	calls := augmenter.GetCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(calls))
	}

	if calls[0].maxTokens != 1000 {
		t.Errorf("Default maxTokens = %d, want 1000", calls[0].maxTokens)
	}
}

// =============================================================================
// Execute Tests - Scope
// =============================================================================

func TestFocusedPrefetchHook_PassesScopeForEngineer(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

	_, _ = hook.Execute(ctx, data)

	calls := augmenter.GetCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(calls))
	}

	if calls[0].scope != "implementation" {
		t.Errorf("scope = %s, want 'implementation'", calls[0].scope)
	}
}

func TestFocusedPrefetchHook_PassesScopeForDesigner(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("designer", "test query")

	_, _ = hook.Execute(ctx, data)

	calls := augmenter.GetCalls()
	if calls[0].scope != "design" {
		t.Errorf("scope = %s, want 'design'", calls[0].scope)
	}
}

func TestFocusedPrefetchHook_PassesScopeForInspector(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("inspector", "test query")

	_, _ = hook.Execute(ctx, data)

	calls := augmenter.GetCalls()
	if calls[0].scope != "review" {
		t.Errorf("scope = %s, want 'review'", calls[0].scope)
	}
}

func TestFocusedPrefetchHook_PassesScopeForTester(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("tester", "test query")

	_, _ = hook.Execute(ctx, data)

	calls := augmenter.GetCalls()
	if calls[0].scope != "testing" {
		t.Errorf("scope = %s, want 'testing'", calls[0].scope)
	}
}

// =============================================================================
// Execute Tests - Edge Cases
// =============================================================================

func TestFocusedPrefetchHook_SkipsWhenDisabled(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}

	calls := augmenter.GetCalls()
	if len(calls) != 0 {
		t.Error("Should not call augmenter when disabled")
	}
}

func TestFocusedPrefetchHook_SkipsNilAugmenter(t *testing.T) {
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: nil,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil augmenter")
	}
}

func TestFocusedPrefetchHook_SkipsEmptyQuery(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with empty query")
	}

	calls := augmenter.GetCalls()
	if len(calls) != 0 {
		t.Error("Should not call augmenter with empty query")
	}
}

func TestFocusedPrefetchHook_SkipsIfAlreadyHasContent(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

	// Pre-populate with existing content
	data.PrefetchedContent = &ctxpkg.AugmentedQuery{
		OriginalQuery: "existing",
		Excerpts: []ctxpkg.Excerpt{
			{Source: "existing.go", Content: "existing content"},
		},
	}

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data that already has prefetched content")
	}

	calls := augmenter.GetCalls()
	if len(calls) != 0 {
		t.Error("Should not call augmenter when content already exists")
	}
}

func TestFocusedPrefetchHook_HandlesAugmenterError(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetError(errors.New("augmentation failed"))

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

	result, err := hook.Execute(ctx, data)

	// Hook should not return error to caller
	if err != nil {
		t.Fatalf("Execute should not return error, got %v", err)
	}

	// But result should contain the error
	if result.Error == nil {
		t.Error("Result.Error should contain the augmenter error")
	}

	if result.Modified {
		t.Error("Should not modify data on error")
	}
}

func TestFocusedPrefetchHook_SkipsEmptyAugmentation(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(&ctxpkg.AugmentedQuery{
		OriginalQuery: "test",
		// No excerpts or summaries
	})

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with empty augmentation")
	}
}

func TestFocusedPrefetchHook_SkipsNilAugmentation(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	// No augmented query set (nil)

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil augmentation")
	}
}

// =============================================================================
// ToPromptHook Tests
// =============================================================================

func TestFocusedPrefetchHook_ToPromptHook_ReturnsValidHook(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != FocusedPrefetchHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), FocusedPrefetchHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePrePrompt", promptHook.Phase())
	}

	if promptHook.Priority() != int(ctxpkg.HookPriorityEarly) {
		t.Errorf("PromptHook Priority = %d, want %d", promptHook.Priority(), ctxpkg.HookPriorityEarly)
	}
}

func TestFocusedPrefetchHook_ToPromptHook_ExecutesCorrectly(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(createTestAugmentedQuery())

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
	})

	promptHook := hook.ToPromptHook()

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "test query")

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
// Integration-like Tests
// =============================================================================

func TestFocusedPrefetchHook_CompleteWorkflow(t *testing.T) {
	augmenter := NewMockFocusedAugmenter()
	augmenter.SetAugmentedQuery(&ctxpkg.AugmentedQuery{
		OriginalQuery: "implement authentication",
		Excerpts: []ctxpkg.Excerpt{
			{
				Source:     "auth/middleware.go",
				Content:    "func AuthMiddleware() gin.HandlerFunc { ... }",
				Confidence: 0.92,
				TokenCount: 200,
			},
			{
				Source:     "auth/jwt.go",
				Content:    "func ValidateToken(token string) (*Claims, error) { ... }",
				Confidence: 0.88,
				TokenCount: 150,
			},
		},
	})

	hook := NewFocusedPrefetchHook(FocusedPrefetchHookConfig{
		Augmenter: augmenter,
		MaxTokens: 1000,
	})

	ctx := context.Background()
	data := createFocusedPrefetchTestData("engineer", "implement authentication")

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

	if len(data.PrefetchedContent.Excerpts) != 2 {
		t.Errorf("Expected 2 excerpts, got %d", len(data.PrefetchedContent.Excerpts))
	}

	// Verify augmenter was called correctly
	calls := augmenter.GetCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(calls))
	}

	if calls[0].query != "implement authentication" {
		t.Errorf("Query = %s, want 'implement authentication'", calls[0].query)
	}

	if calls[0].maxTokens != 1000 {
		t.Errorf("maxTokens = %d, want 1000", calls[0].maxTokens)
	}

	if calls[0].scope != "implementation" {
		t.Errorf("scope = %s, want 'implementation'", calls[0].scope)
	}
}
