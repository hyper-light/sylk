package context

import (
	"context"
	"sync"
	"testing"
)

// =============================================================================
// Test Helpers
// =============================================================================

func createTestPromptHook(name string, phase HookPhase, priority int) *PromptHook {
	return NewPromptHook(name, phase, priority, func(ctx context.Context, data *PromptHookData) (*HookResult, error) {
		return &HookResult{Modified: false}, nil
	})
}

func createTestToolCallHook(name string, phase HookPhase, priority int) *ToolCallHook {
	return NewToolCallHook(name, phase, priority, func(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
		return &HookResult{Modified: false}, nil
	})
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewAdaptiveHookRegistry(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	if registry == nil {
		t.Fatal("NewAdaptiveHookRegistry returned nil")
	}

	if registry.HookCount() != 0 {
		t.Errorf("New registry should have 0 hooks, got %d", registry.HookCount())
	}
}

// =============================================================================
// Registration Tests
// =============================================================================

func TestRegisterPromptHook_Global(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	hook := createTestPromptHook("test_hook", HookPhasePrePrompt, 50)
	err := registry.RegisterPromptHook(hook)

	if err != nil {
		t.Fatalf("RegisterPromptHook error = %v", err)
	}

	if registry.PromptHookCount() != 1 {
		t.Errorf("PromptHookCount = %d, want 1", registry.PromptHookCount())
	}
}

func TestRegisterPromptHook_DuplicateError(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	hook1 := createTestPromptHook("test_hook", HookPhasePrePrompt, 50)
	hook2 := createTestPromptHook("test_hook", HookPhasePrePrompt, 50)

	_ = registry.RegisterPromptHook(hook1)
	err := registry.RegisterPromptHook(hook2)

	if err != ErrHookAlreadyExists {
		t.Errorf("Expected ErrHookAlreadyExists, got %v", err)
	}
}

func TestRegisterPromptHookForAgent(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	hook := createTestPromptHook("librarian_hook", HookPhasePrePrompt, 50)
	err := registry.RegisterPromptHookForAgent("librarian", hook)

	if err != nil {
		t.Fatalf("RegisterPromptHookForAgent error = %v", err)
	}

	// Should be retrievable for librarian
	hooks := registry.GetPromptHooksForAgent("librarian", HookPhasePrePrompt)
	if len(hooks) != 1 {
		t.Errorf("Expected 1 hook for librarian, got %d", len(hooks))
	}

	// Should NOT be retrievable for engineer
	hooks = registry.GetPromptHooksForAgent("engineer", HookPhasePrePrompt)
	if len(hooks) != 0 {
		t.Errorf("Expected 0 hooks for engineer, got %d", len(hooks))
	}
}

func TestRegisterToolCallHook_Global(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	hook := createTestToolCallHook("test_hook", HookPhasePostTool, 50)
	err := registry.RegisterToolCallHook(hook)

	if err != nil {
		t.Fatalf("RegisterToolCallHook error = %v", err)
	}

	if registry.ToolCallHookCount() != 1 {
		t.Errorf("ToolCallHookCount = %d, want 1", registry.ToolCallHookCount())
	}
}

func TestRegisterToolCallHookForAgent(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	hook := createTestToolCallHook("engineer_hook", HookPhasePostTool, 50)
	err := registry.RegisterToolCallHookForAgent("engineer", hook)

	if err != nil {
		t.Fatalf("RegisterToolCallHookForAgent error = %v", err)
	}

	// Should be retrievable for engineer
	hooks := registry.GetToolCallHooksForAgent("engineer", HookPhasePostTool)
	if len(hooks) != 1 {
		t.Errorf("Expected 1 hook for engineer, got %d", len(hooks))
	}

	// Should NOT be retrievable for librarian
	hooks = registry.GetToolCallHooksForAgent("librarian", HookPhasePostTool)
	if len(hooks) != 0 {
		t.Errorf("Expected 0 hooks for librarian, got %d", len(hooks))
	}
}

// =============================================================================
// Priority Ordering Tests
// =============================================================================

func TestGetPromptHooks_PriorityOrder(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	// Register hooks in non-priority order
	_ = registry.RegisterPromptHook(createTestPromptHook("hook_late", HookPhasePrePrompt, 75))
	_ = registry.RegisterPromptHook(createTestPromptHook("hook_first", HookPhasePrePrompt, 0))
	_ = registry.RegisterPromptHook(createTestPromptHook("hook_normal", HookPhasePrePrompt, 50))

	hooks := registry.GetPromptHooks(HookPhasePrePrompt)

	if len(hooks) != 3 {
		t.Fatalf("Expected 3 hooks, got %d", len(hooks))
	}

	// Verify order: first (0), normal (50), late (75)
	if hooks[0].Name() != "hook_first" {
		t.Errorf("First hook should be 'hook_first', got '%s'", hooks[0].Name())
	}
	if hooks[1].Name() != "hook_normal" {
		t.Errorf("Second hook should be 'hook_normal', got '%s'", hooks[1].Name())
	}
	if hooks[2].Name() != "hook_late" {
		t.Errorf("Third hook should be 'hook_late', got '%s'", hooks[2].Name())
	}
}

func TestGetToolCallHooks_PriorityOrder(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	// Register hooks in non-priority order
	_ = registry.RegisterToolCallHook(createTestToolCallHook("hook_100", HookPhasePostTool, 100))
	_ = registry.RegisterToolCallHook(createTestToolCallHook("hook_25", HookPhasePostTool, 25))
	_ = registry.RegisterToolCallHook(createTestToolCallHook("hook_50", HookPhasePostTool, 50))

	hooks := registry.GetToolCallHooks(HookPhasePostTool)

	if len(hooks) != 3 {
		t.Fatalf("Expected 3 hooks, got %d", len(hooks))
	}

	// Verify ascending order
	if hooks[0].Priority() != 25 {
		t.Errorf("First hook priority should be 25, got %d", hooks[0].Priority())
	}
	if hooks[1].Priority() != 50 {
		t.Errorf("Second hook priority should be 50, got %d", hooks[1].Priority())
	}
	if hooks[2].Priority() != 100 {
		t.Errorf("Third hook priority should be 100, got %d", hooks[2].Priority())
	}
}

func TestGetPromptHooksForAgent_CombinesPriorityOrder(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	// Register global hook with priority 50
	_ = registry.RegisterPromptHook(createTestPromptHook("global_hook", HookPhasePrePrompt, 50))

	// Register agent-specific hook with priority 25
	_ = registry.RegisterPromptHookForAgent("librarian", createTestPromptHook("agent_hook", HookPhasePrePrompt, 25))

	hooks := registry.GetPromptHooksForAgent("librarian", HookPhasePrePrompt)

	if len(hooks) != 2 {
		t.Fatalf("Expected 2 hooks, got %d", len(hooks))
	}

	// Agent-specific hook (25) should come before global hook (50)
	if hooks[0].Name() != "agent_hook" {
		t.Errorf("First hook should be 'agent_hook', got '%s'", hooks[0].Name())
	}
	if hooks[1].Name() != "global_hook" {
		t.Errorf("Second hook should be 'global_hook', got '%s'", hooks[1].Name())
	}
}

// =============================================================================
// Agent Filtering Tests
// =============================================================================

func TestGetPromptHooksForAgent_GlobalMatchesAll(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterPromptHook(createTestPromptHook("global_hook", HookPhasePrePrompt, 50))

	agents := []string{"librarian", "engineer", "guide", "orchestrator"}
	for _, agent := range agents {
		hooks := registry.GetPromptHooksForAgent(agent, HookPhasePrePrompt)
		if len(hooks) != 1 {
			t.Errorf("Agent %s should get global hook, got %d hooks", agent, len(hooks))
		}
	}
}

func TestGetPromptHooksForAgent_AgentSpecificFilters(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterPromptHookForAgent("librarian", createTestPromptHook("librarian_hook", HookPhasePrePrompt, 50))
	_ = registry.RegisterPromptHookForAgent("engineer", createTestPromptHook("engineer_hook", HookPhasePrePrompt, 50))

	// Librarian should only see librarian hook
	hooks := registry.GetPromptHooksForAgent("librarian", HookPhasePrePrompt)
	if len(hooks) != 1 || hooks[0].Name() != "librarian_hook" {
		t.Error("Librarian should only see librarian_hook")
	}

	// Engineer should only see engineer hook
	hooks = registry.GetPromptHooksForAgent("engineer", HookPhasePrePrompt)
	if len(hooks) != 1 || hooks[0].Name() != "engineer_hook" {
		t.Error("Engineer should only see engineer_hook")
	}

	// Guide should see no hooks
	hooks = registry.GetPromptHooksForAgent("guide", HookPhasePrePrompt)
	if len(hooks) != 0 {
		t.Error("Guide should see no hooks")
	}
}

func TestGetPromptHooksForAgent_CaseInsensitive(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterPromptHookForAgent("librarian", createTestPromptHook("librarian_hook", HookPhasePrePrompt, 50))

	// Should work with different cases
	variants := []string{"librarian", "Librarian", "LIBRARIAN"}
	for _, variant := range variants {
		hooks := registry.GetPromptHooksForAgent(variant, HookPhasePrePrompt)
		if len(hooks) != 1 {
			t.Errorf("Agent type '%s' should match 'librarian' hook", variant)
		}
	}
}

// =============================================================================
// Unregister Tests
// =============================================================================

func TestUnregisterHook_PromptHook(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterPromptHook(createTestPromptHook("test_hook", HookPhasePrePrompt, 50))

	if registry.PromptHookCount() != 1 {
		t.Fatal("Hook should be registered")
	}

	err := registry.UnregisterHook("test_hook")
	if err != nil {
		t.Fatalf("UnregisterHook error = %v", err)
	}

	if registry.PromptHookCount() != 0 {
		t.Error("Hook should be unregistered")
	}
}

func TestUnregisterHook_ToolCallHook(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterToolCallHook(createTestToolCallHook("test_hook", HookPhasePostTool, 50))

	if registry.ToolCallHookCount() != 1 {
		t.Fatal("Hook should be registered")
	}

	err := registry.UnregisterHook("test_hook")
	if err != nil {
		t.Fatalf("UnregisterHook error = %v", err)
	}

	if registry.ToolCallHookCount() != 0 {
		t.Error("Hook should be unregistered")
	}
}

func TestUnregisterHook_NotFound(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	err := registry.UnregisterHook("nonexistent")
	if err != ErrHookNotFound {
		t.Errorf("Expected ErrHookNotFound, got %v", err)
	}
}

func TestUnregisterHook_RemovesFromAllAgents(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	// Register hook for multiple agents (through global)
	_ = registry.RegisterPromptHook(createTestPromptHook("global_hook", HookPhasePrePrompt, 50))

	// Verify visible to multiple agents
	if len(registry.GetPromptHooksForAgent("librarian", HookPhasePrePrompt)) != 1 {
		t.Fatal("Hook should be visible to librarian")
	}
	if len(registry.GetPromptHooksForAgent("engineer", HookPhasePrePrompt)) != 1 {
		t.Fatal("Hook should be visible to engineer")
	}

	// Unregister
	_ = registry.UnregisterHook("global_hook")

	// Verify removed from all agents
	if len(registry.GetPromptHooksForAgent("librarian", HookPhasePrePrompt)) != 0 {
		t.Error("Hook should be removed from librarian")
	}
	if len(registry.GetPromptHooksForAgent("engineer", HookPhasePrePrompt)) != 0 {
		t.Error("Hook should be removed from engineer")
	}
}

// =============================================================================
// Enabled/Disabled Tests
// =============================================================================

func TestGetPromptHooks_FiltersDisabled(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	enabledHook := createTestPromptHook("enabled_hook", HookPhasePrePrompt, 50)
	disabledHook := createTestPromptHook("disabled_hook", HookPhasePrePrompt, 25)
	disabledHook.SetEnabled(false)

	_ = registry.RegisterPromptHook(enabledHook)
	_ = registry.RegisterPromptHook(disabledHook)

	hooks := registry.GetPromptHooks(HookPhasePrePrompt)

	if len(hooks) != 1 {
		t.Fatalf("Expected 1 enabled hook, got %d", len(hooks))
	}

	if hooks[0].Name() != "enabled_hook" {
		t.Errorf("Should only return enabled hook, got '%s'", hooks[0].Name())
	}
}

func TestGetToolCallHooks_FiltersDisabled(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	enabledHook := createTestToolCallHook("enabled_hook", HookPhasePostTool, 50)
	disabledHook := createTestToolCallHook("disabled_hook", HookPhasePostTool, 25)
	disabledHook.SetEnabled(false)

	_ = registry.RegisterToolCallHook(enabledHook)
	_ = registry.RegisterToolCallHook(disabledHook)

	hooks := registry.GetToolCallHooks(HookPhasePostTool)

	if len(hooks) != 1 {
		t.Fatalf("Expected 1 enabled hook, got %d", len(hooks))
	}

	if hooks[0].Name() != "enabled_hook" {
		t.Errorf("Should only return enabled hook, got '%s'", hooks[0].Name())
	}
}

// =============================================================================
// Phase Filtering Tests
// =============================================================================

func TestGetPromptHooks_FiltersByPhase(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterPromptHook(createTestPromptHook("pre_prompt_hook", HookPhasePrePrompt, 50))
	_ = registry.RegisterPromptHook(createTestPromptHook("post_prompt_hook", HookPhasePostPrompt, 50))

	preHooks := registry.GetPromptHooks(HookPhasePrePrompt)
	if len(preHooks) != 1 || preHooks[0].Name() != "pre_prompt_hook" {
		t.Error("PrePrompt phase should only return pre_prompt_hook")
	}

	postHooks := registry.GetPromptHooks(HookPhasePostPrompt)
	if len(postHooks) != 1 || postHooks[0].Name() != "post_prompt_hook" {
		t.Error("PostPrompt phase should only return post_prompt_hook")
	}
}

func TestGetToolCallHooks_FiltersByPhase(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterToolCallHook(createTestToolCallHook("pre_tool_hook", HookPhasePreTool, 50))
	_ = registry.RegisterToolCallHook(createTestToolCallHook("post_tool_hook", HookPhasePostTool, 50))

	preHooks := registry.GetToolCallHooks(HookPhasePreTool)
	if len(preHooks) != 1 || preHooks[0].Name() != "pre_tool_hook" {
		t.Error("PreTool phase should only return pre_tool_hook")
	}

	postHooks := registry.GetToolCallHooks(HookPhasePostTool)
	if len(postHooks) != 1 || postHooks[0].Name() != "post_tool_hook" {
		t.Error("PostTool phase should only return post_tool_hook")
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestHookCount(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterPromptHook(createTestPromptHook("hook1", HookPhasePrePrompt, 50))
	_ = registry.RegisterPromptHook(createTestPromptHook("hook2", HookPhasePostPrompt, 50))
	_ = registry.RegisterToolCallHook(createTestToolCallHook("hook3", HookPhasePostTool, 50))

	if registry.HookCount() != 3 {
		t.Errorf("HookCount = %d, want 3", registry.HookCount())
	}

	if registry.PromptHookCount() != 2 {
		t.Errorf("PromptHookCount = %d, want 2", registry.PromptHookCount())
	}

	if registry.ToolCallHookCount() != 1 {
		t.Errorf("ToolCallHookCount = %d, want 1", registry.ToolCallHookCount())
	}
}

func TestGetRegisteredAgentTypes(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	_ = registry.RegisterPromptHook(createTestPromptHook("global_hook", HookPhasePrePrompt, 50))
	_ = registry.RegisterPromptHookForAgent("librarian", createTestPromptHook("librarian_hook", HookPhasePrePrompt, 50))
	_ = registry.RegisterPromptHookForAgent("engineer", createTestPromptHook("engineer_hook", HookPhasePrePrompt, 50))

	types := registry.GetRegisteredAgentTypes()

	// Should include: *, librarian, engineer
	if len(types) != 3 {
		t.Errorf("Expected 3 agent types, got %d", len(types))
	}

	// Check for expected types
	typeSet := make(map[string]bool)
	for _, at := range types {
		typeSet[at] = true
	}

	if !typeSet[GlobalAgentType] {
		t.Error("Should include global agent type")
	}
	if !typeSet["librarian"] {
		t.Error("Should include librarian")
	}
	if !typeSet["engineer"] {
		t.Error("Should include engineer")
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestRegistry_ConcurrentRegistration(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	var wg sync.WaitGroup
	numGoroutines := 10
	hooksPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < hooksPerGoroutine; j++ {
				name := "hook_" + string(rune('A'+id)) + "_" + string(rune('0'+j))
				_ = registry.RegisterPromptHook(createTestPromptHook(name, HookPhasePrePrompt, 50))
			}
		}(i)
	}

	wg.Wait()

	// All hooks should be registered (or some duplicates prevented)
	count := registry.PromptHookCount()
	if count > numGoroutines*hooksPerGoroutine {
		t.Errorf("Too many hooks registered: %d", count)
	}
}

func TestRegistry_ConcurrentReadWrite(t *testing.T) {
	registry := NewAdaptiveHookRegistry()

	// Pre-register some hooks
	for i := 0; i < 5; i++ {
		name := "initial_hook_" + string(rune('0'+i))
		_ = registry.RegisterPromptHook(createTestPromptHook(name, HookPhasePrePrompt, 50))
	}

	var wg sync.WaitGroup
	numReaders := 10
	numWriters := 5

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = registry.GetPromptHooksForAgent("librarian", HookPhasePrePrompt)
				_ = registry.GetPromptHooksForAgent("engineer", HookPhasePrePrompt)
			}
		}()
	}

	// Start writers
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				name := "concurrent_hook_" + string(rune('A'+id)) + "_" + string(rune('0'+j))
				_ = registry.RegisterPromptHook(createTestPromptHook(name, HookPhasePrePrompt, 50))
			}
		}(i)
	}

	wg.Wait()

	// Registry should be in consistent state
	if registry.PromptHookCount() < 5 {
		t.Error("Registry lost hooks during concurrent access")
	}
}

// =============================================================================
// Interface Compliance Test
// =============================================================================

func TestAdaptiveHookRegistry_ImplementsHookRegistry(t *testing.T) {
	// This test verifies that AdaptiveHookRegistry implements the HookRegistry interface.
	// If it doesn't, the assignment will fail at compile time.
	var _ HookRegistry = (*AdaptiveHookRegistry)(nil)

	registry := NewAdaptiveHookRegistry()
	var iface HookRegistry = registry

	// Verify interface methods work
	hook := createTestPromptHook("interface_test", HookPhasePrePrompt, 50)
	err := iface.RegisterPromptHook(hook)
	if err != nil {
		t.Fatalf("RegisterPromptHook via interface failed: %v", err)
	}

	hooks := iface.GetPromptHooks(HookPhasePrePrompt)
	if len(hooks) != 1 {
		t.Error("GetPromptHooks via interface should return 1 hook")
	}

	err = iface.UnregisterHook("interface_test")
	if err != nil {
		t.Fatalf("UnregisterHook via interface failed: %v", err)
	}
}
