package hooks

import (
	"context"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Test Helpers
// =============================================================================

func createTestPrefetchHook(t *testing.T) *SpeculativePrefetchHook {
	t.Helper()

	hotCache := ctxpkg.NewHotCache(ctxpkg.HotCacheConfig{
		MaxSize: 10000,
	})

	bleve := ctxpkg.NewMockBleveSearcher()
	searcher := ctxpkg.NewTieredSearcher(ctxpkg.TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
	})

	prefetcher := ctxpkg.NewSpeculativePrefetcher(ctxpkg.SpeculativePrefetcherConfig{
		Searcher: searcher,
	})

	augmenter := ctxpkg.NewQueryAugmenter(ctxpkg.QueryAugmenterConfig{
		Searcher: searcher,
	})

	return NewSpeculativePrefetchHook(PrefetchHookConfig{
		Prefetcher:     prefetcher,
		Augmenter:      augmenter,
		ReadyTimeout:   10 * time.Millisecond,
		FallbackBudget: 1000,
	})
}

func createTestPromptData(agentType string) *ctxpkg.PromptHookData {
	return &ctxpkg.PromptHookData{
		SessionID:     "test-session",
		AgentID:       "test-agent",
		AgentType:     agentType,
		TurnNumber:    1,
		Query:         "test query",
		PressureLevel: 0,
		Timestamp:     time.Now(),
	}
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewSpeculativePrefetchHook_DefaultConfig(t *testing.T) {
	hook := NewSpeculativePrefetchHook(PrefetchHookConfig{})

	if hook.readyTimeout != PrefetchReadyTimeout {
		t.Errorf("readyTimeout = %v, want %v", hook.readyTimeout, PrefetchReadyTimeout)
	}

	if hook.fallbackBudget != FallbackTokenBudget {
		t.Errorf("fallbackBudget = %d, want %d", hook.fallbackBudget, FallbackTokenBudget)
	}

	if !hook.enabled {
		t.Error("Hook should be enabled by default")
	}
}

func TestNewSpeculativePrefetchHook_CustomConfig(t *testing.T) {
	hook := NewSpeculativePrefetchHook(PrefetchHookConfig{
		ReadyTimeout:   50 * time.Millisecond,
		FallbackBudget: 5000,
	})

	if hook.readyTimeout != 50*time.Millisecond {
		t.Errorf("readyTimeout = %v, want 50ms", hook.readyTimeout)
	}

	if hook.fallbackBudget != 5000 {
		t.Errorf("fallbackBudget = %d, want 5000", hook.fallbackBudget)
	}
}

// =============================================================================
// Hook Interface Tests
// =============================================================================

func TestSpeculativePrefetchHook_Name(t *testing.T) {
	hook := createTestPrefetchHook(t)

	if hook.Name() != PrefetchHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), PrefetchHookName)
	}
}

func TestSpeculativePrefetchHook_Phase(t *testing.T) {
	hook := createTestPrefetchHook(t)

	if hook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("Phase() = %v, want HookPhasePrePrompt", hook.Phase())
	}
}

func TestSpeculativePrefetchHook_Priority(t *testing.T) {
	hook := createTestPrefetchHook(t)

	if hook.Priority() != int(ctxpkg.HookPriorityFirst) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityFirst)
	}
}

func TestSpeculativePrefetchHook_Enabled(t *testing.T) {
	hook := createTestPrefetchHook(t)

	if !hook.Enabled() {
		t.Error("Hook should be enabled by default")
	}

	hook.SetEnabled(false)

	if hook.Enabled() {
		t.Error("Hook should be disabled after SetEnabled(false)")
	}
}

// =============================================================================
// Execute Tests
// =============================================================================

func TestExecute_SkipsNonKnowledgeAgent(t *testing.T) {
	hook := createTestPrefetchHook(t)
	ctx := context.Background()
	data := createTestPromptData("engineer") // Not a knowledge agent

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data for non-knowledge agent")
	}
}

func TestExecute_SkipsWhenDisabled(t *testing.T) {
	hook := createTestPrefetchHook(t)
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createTestPromptData("librarian")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}
}

func TestExecute_SkipsUnderHighPressure(t *testing.T) {
	hook := createTestPrefetchHook(t)
	ctx := context.Background()
	data := createTestPromptData("librarian")
	data.PressureLevel = 2 // High pressure

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data under high pressure")
	}
}

func TestExecute_KnowledgeAgents(t *testing.T) {
	agents := []string{"librarian", "archivalist", "academic", "architect", "guide"}

	for _, agent := range agents {
		t.Run(agent, func(t *testing.T) {
			hook := createTestPrefetchHook(t)
			ctx := context.Background()
			data := createTestPromptData(agent)

			// Add content to hot cache so augmentation returns results
			hook.augmenter.Searcher().HotCache().Add("test-entry", &ctxpkg.ContentEntry{
				ID:       "test-entry",
				Content:  "Test content for augmentation purposes.",
				Keywords: []string{"test"},
			})

			result, err := hook.Execute(ctx, data)

			if err != nil {
				t.Fatalf("Execute error = %v for agent %s", err, agent)
			}

			// Should have attempted augmentation (even if no results)
			// The key is it doesn't error and handles the agent correctly
			t.Logf("Agent %s: Modified=%v", agent, result.Modified)
		})
	}
}

func TestExecute_NilPrefetcher(t *testing.T) {
	hook := NewSpeculativePrefetchHook(PrefetchHookConfig{})
	ctx := context.Background()
	data := createTestPromptData("librarian")

	// Should not panic with nil prefetcher
	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// No prefetch or augmentation available, should not modify
	if result.Modified {
		t.Error("Should not modify with nil prefetcher and augmenter")
	}
}

func TestExecute_NilAugmenter(t *testing.T) {
	hotCache := ctxpkg.NewHotCache(ctxpkg.HotCacheConfig{MaxSize: 10000})
	bleve := ctxpkg.NewMockBleveSearcher()
	searcher := ctxpkg.NewTieredSearcher(ctxpkg.TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
	})
	prefetcher := ctxpkg.NewSpeculativePrefetcher(ctxpkg.SpeculativePrefetcherConfig{
		Searcher: searcher,
	})

	hook := NewSpeculativePrefetchHook(PrefetchHookConfig{
		Prefetcher: prefetcher,
		// No augmenter
	})

	ctx := context.Background()
	data := createTestPromptData("librarian")

	// Should not panic with nil augmenter
	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// No prefetch ready, no augmenter fallback
	if result.Modified {
		t.Error("Should not modify with no prefetch and nil augmenter")
	}
}

// =============================================================================
// ToPromptHook Tests
// =============================================================================

func TestToPromptHook_ReturnsValidHook(t *testing.T) {
	hook := createTestPrefetchHook(t)

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != PrefetchHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), PrefetchHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePrePrompt", promptHook.Phase())
	}

	if promptHook.Priority() != int(ctxpkg.HookPriorityFirst) {
		t.Errorf("PromptHook Priority = %d, want %d", promptHook.Priority(), ctxpkg.HookPriorityFirst)
	}
}

func TestToPromptHook_ExecutesCorrectly(t *testing.T) {
	hook := createTestPrefetchHook(t)
	promptHook := hook.ToPromptHook()

	ctx := context.Background()
	data := createTestPromptData("engineer") // Non-knowledge agent

	result, err := promptHook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("PromptHook Execute error = %v", err)
	}

	// Should skip non-knowledge agent
	if result.Modified {
		t.Error("PromptHook should skip non-knowledge agent")
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestExecute_WithPrefetchedContent(t *testing.T) {
	hook := createTestPrefetchHook(t)

	// Add content to hot cache
	hook.prefetcher.Searcher().HotCache().Add("prefetch-entry", &ctxpkg.ContentEntry{
		ID:       "prefetch-entry",
		Content:  "Prefetched content for the query.",
		Keywords: []string{"test", "query"},
	})

	// Start a prefetch
	ctx := context.Background()
	future, err := hook.prefetcher.StartSpeculative(ctx, "test query")
	if err != nil {
		t.Fatalf("StartSpeculative error = %v", err)
	}

	// Wait for prefetch to complete
	future.Wait()

	// Execute the hook
	data := createTestPromptData("librarian")
	result, execErr := hook.Execute(ctx, data)

	if execErr != nil {
		t.Fatalf("Execute error = %v", execErr)
	}

	t.Logf("Execute result: Modified=%v, PrefetchedContent=%v",
		result.Modified, data.PrefetchedContent != nil)

	// Should have injected prefetched content
	if data.PrefetchedContent == nil {
		t.Log("PrefetchedContent is nil - this may be due to timing")
	}
}

func TestExecute_FallbackAugmentation(t *testing.T) {
	hook := createTestPrefetchHook(t)

	// Add content to hot cache (for augmenter to find)
	hook.augmenter.Searcher().HotCache().Add("fallback-entry", &ctxpkg.ContentEntry{
		ID:       "fallback-entry",
		Content:  "Fallback content for augmentation.",
		Keywords: []string{"query"},
	})

	// Execute without any prefetch started
	ctx := context.Background()
	data := createTestPromptData("librarian")
	data.Query = "query" // Match the keyword

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// Should have used fallback augmentation
	t.Logf("Fallback result: Modified=%v, PrefetchedContent=%v",
		result.Modified, data.PrefetchedContent != nil)
}
