package skills

import (
	"context"
	"errors"
	"testing"
)

func TestHookRegistry_RegisterPromptHook(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPrePromptHook("test_hook", HookPriorityNormal, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{Continue: true}
	})

	stats := registry.Stats()
	if stats.PrePromptHooks != 1 {
		t.Errorf("expected 1 pre-prompt hook, got %d", stats.PrePromptHooks)
	}
}

func TestHookRegistry_RegisterToolCallHook(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreToolCallHook("test_hook", HookPriorityNormal, func(ctx context.Context, data *ToolCallHookData) HookResult {
		return HookResult{Continue: true}
	})

	stats := registry.Stats()
	if stats.PreToolCallHooks != 1 {
		t.Errorf("expected 1 pre-tool-call hook, got %d", stats.PreToolCallHooks)
	}
}

func TestHookRegistry_RegisterStoreHook(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreStoreHook("test_hook", HookPriorityNormal, func(ctx context.Context, data *StoreHookData) HookResult {
		return HookResult{Continue: true}
	})

	stats := registry.Stats()
	if stats.PreStoreHooks != 1 {
		t.Errorf("expected 1 pre-store hook, got %d", stats.PreStoreHooks)
	}
}

func TestHookRegistry_RegisterQueryHook(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreQueryHook("test_hook", HookPriorityNormal, func(ctx context.Context, data *QueryHookData) HookResult {
		return HookResult{Continue: true}
	})

	stats := registry.Stats()
	if stats.PreQueryHooks != 1 {
		t.Errorf("expected 1 pre-query hook, got %d", stats.PreQueryHooks)
	}
}

func TestHookRegistry_PriorityOrder(t *testing.T) {
	registry := NewHookRegistry()

	order := []string{}

	registry.RegisterPrePromptHook("low", HookPriorityLow, func(ctx context.Context, data *PromptHookData) HookResult {
		order = append(order, "low")
		return HookResult{Continue: true}
	})

	registry.RegisterPrePromptHook("high", HookPriorityHigh, func(ctx context.Context, data *PromptHookData) HookResult {
		order = append(order, "high")
		return HookResult{Continue: true}
	})

	registry.RegisterPrePromptHook("normal", HookPriorityNormal, func(ctx context.Context, data *PromptHookData) HookResult {
		order = append(order, "normal")
		return HookResult{Continue: true}
	})

	data := &PromptHookData{}
	registry.ExecutePrePromptHooks(context.Background(), data)

	if len(order) != 3 {
		t.Fatalf("expected 3 hooks executed, got %d", len(order))
	}

	if order[0] != "high" {
		t.Errorf("expected 'high' first, got %s", order[0])
	}
	if order[1] != "normal" {
		t.Errorf("expected 'normal' second, got %s", order[1])
	}
	if order[2] != "low" {
		t.Errorf("expected 'low' third, got %s", order[2])
	}
}

func TestHookRegistry_ExecutePrePromptHooks_ModifiesData(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPrePromptHook("modifier", HookPriorityNormal, func(ctx context.Context, data *PromptHookData) HookResult {
		modified := *data
		modified.UserMessage = "modified: " + data.UserMessage
		return HookResult{
			Continue:           true,
			ModifiedPromptData: &modified,
		}
	})

	data := &PromptHookData{UserMessage: "original"}
	result, err := registry.ExecutePrePromptHooks(context.Background(), data)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.UserMessage != "modified: original" {
		t.Errorf("expected 'modified: original', got %s", result.UserMessage)
	}
}

func TestHookRegistry_ExecutePrePromptHooks_InjectsContext(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPrePromptHook("injector", HookPriorityNormal, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{
			Continue:      true,
			InjectContext: "Additional context here",
		}
	})

	data := &PromptHookData{SystemPrompt: "Base prompt."}
	result, _ := registry.ExecutePrePromptHooks(context.Background(), data)

	if result.SystemPrompt != "Base prompt.\nAdditional context here" {
		t.Errorf("expected injected context, got: %s", result.SystemPrompt)
	}
}

func TestHookRegistry_ExecutePrePromptHooks_StopsOnError(t *testing.T) {
	registry := NewHookRegistry()

	executed := []string{}

	registry.RegisterPrePromptHook("first", HookPriorityHigh, func(ctx context.Context, data *PromptHookData) HookResult {
		executed = append(executed, "first")
		return HookResult{
			Continue: false,
			Error:    errors.New("stop here"),
		}
	})

	registry.RegisterPrePromptHook("second", HookPriorityLow, func(ctx context.Context, data *PromptHookData) HookResult {
		executed = append(executed, "second")
		return HookResult{Continue: true}
	})

	data := &PromptHookData{}
	_, err := registry.ExecutePrePromptHooks(context.Background(), data)

	if err == nil {
		t.Error("expected error")
	}
	if len(executed) != 1 {
		t.Errorf("expected only 1 hook executed, got %d", len(executed))
	}
}

func TestHookRegistry_ExecutePrePromptHooks_StopsOnContinueFalse(t *testing.T) {
	registry := NewHookRegistry()

	executed := []string{}

	registry.RegisterPrePromptHook("stopper", HookPriorityHigh, func(ctx context.Context, data *PromptHookData) HookResult {
		executed = append(executed, "stopper")
		return HookResult{Continue: false}
	})

	registry.RegisterPrePromptHook("skipped", HookPriorityLow, func(ctx context.Context, data *PromptHookData) HookResult {
		executed = append(executed, "skipped")
		return HookResult{Continue: true}
	})

	data := &PromptHookData{}
	_, err := registry.ExecutePrePromptHooks(context.Background(), data)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(executed) != 1 {
		t.Errorf("expected only 1 hook executed, got %d", len(executed))
	}
}

func TestHookRegistry_ExecuteToolCallHooks_SkipExecution(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreToolCallHook("skipper", HookPriorityHigh, func(ctx context.Context, data *ToolCallHookData) HookResult {
		return HookResult{
			Continue:      false,
			SkipExecution: true,
			SkipResponse:  "cached response",
		}
	})

	data := &ToolCallHookData{ToolName: "test_tool"}
	_, result, err := registry.ExecutePreToolCallHooks(context.Background(), data)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.SkipExecution {
		t.Error("expected SkipExecution to be true")
	}
	if result.SkipResponse != "cached response" {
		t.Errorf("expected 'cached response', got %s", result.SkipResponse)
	}
}

func TestHookRegistry_ExecuteStoreHooks_ModifiesData(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreStoreHook("modifier", HookPriorityNormal, func(ctx context.Context, data *StoreHookData) HookResult {
		modified := *data
		modified.Entry = "updated"
		return HookResult{
			Continue:          true,
			ModifiedStoreData: &modified,
		}
	})

	data := &StoreHookData{Entry: "original"}
	result, _, err := registry.ExecutePreStoreHooks(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Entry != "updated" {
		t.Fatalf("expected updated entry, got %v", result.Entry)
	}
}

func TestHookRegistry_ExecuteQueryHooks_ModifiesData(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreQueryHook("modifier", HookPriorityNormal, func(ctx context.Context, data *QueryHookData) HookResult {
		modified := *data
		modified.Query = "updated"
		return HookResult{
			Continue:          true,
			ModifiedQueryData: &modified,
		}
	})

	data := &QueryHookData{Query: "original"}
	result, _, err := registry.ExecutePreQueryHooks(context.Background(), data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Query != "updated" {
		t.Fatalf("expected updated query, got %v", result.Query)
	}
}

func TestHookRegistry_UnregisterPromptHook(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPrePromptHook("removable", HookPriorityNormal, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{Continue: true}
	})

	stats := registry.Stats()
	if stats.PrePromptHooks != 1 {
		t.Errorf("expected 1 hook, got %d", stats.PrePromptHooks)
	}

	ok := registry.UnregisterPromptHook("removable")
	if !ok {
		t.Error("expected unregister to return true")
	}

	stats = registry.Stats()
	if stats.PrePromptHooks != 0 {
		t.Errorf("expected 0 hooks after unregister, got %d", stats.PrePromptHooks)
	}

	ok = registry.UnregisterPromptHook("nonexistent")
	if ok {
		t.Error("expected unregister to return false for nonexistent hook")
	}
}

func TestHookRegistry_UnregisterToolCallHook(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreToolCallHook("removable", HookPriorityNormal, func(ctx context.Context, data *ToolCallHookData) HookResult {
		return HookResult{Continue: true}
	})

	ok := registry.UnregisterToolCallHook("removable")
	if !ok {
		t.Error("expected unregister to return true")
	}

	stats := registry.Stats()
	if stats.PreToolCallHooks != 0 {
		t.Errorf("expected 0 hooks, got %d", stats.PreToolCallHooks)
	}
}

func TestHookRegistry_UnregisterStoreHook(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreStoreHook("removable", HookPriorityNormal, func(ctx context.Context, data *StoreHookData) HookResult {
		return HookResult{Continue: true}
	})

	ok := registry.UnregisterStoreHook("removable")
	if !ok {
		t.Error("expected unregister to return true")
	}

	stats := registry.Stats()
	if stats.PreStoreHooks != 0 {
		t.Errorf("expected 0 hooks, got %d", stats.PreStoreHooks)
	}
}

func TestHookRegistry_UnregisterQueryHook(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPreQueryHook("removable", HookPriorityNormal, func(ctx context.Context, data *QueryHookData) HookResult {
		return HookResult{Continue: true}
	})

	ok := registry.UnregisterQueryHook("removable")
	if !ok {
		t.Error("expected unregister to return true")
	}

	stats := registry.Stats()
	if stats.PreQueryHooks != 0 {
		t.Errorf("expected 0 hooks, got %d", stats.PreQueryHooks)
	}
}

func TestHookRegistry_ContextCancellation(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPrePromptHook("first", HookPriorityHigh, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{Continue: true}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	data := &PromptHookData{}
	_, err := registry.ExecutePrePromptHooks(ctx, data)

	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestNewContextInjectionHook(t *testing.T) {
	registry := NewHookRegistry()

	hook := NewContextInjectionHook("memory_injector", HookPriorityNormal, func(data *PromptHookData) string {
		return "Memory context: User prefers dark mode"
	})

	registry.RegisterPromptHook(hook)

	data := &PromptHookData{SystemPrompt: "You are an assistant."}
	result, _ := registry.ExecutePrePromptHooks(context.Background(), data)

	if result.SystemPrompt != "You are an assistant.\nMemory context: User prefers dark mode" {
		t.Errorf("unexpected result: %s", result.SystemPrompt)
	}
}

func TestNewToolCallLoggerHook(t *testing.T) {
	registry := NewHookRegistry()

	logged := []string{}
	hook := NewToolCallLoggerHook("logger", func(data *ToolCallHookData) {
		logged = append(logged, data.ToolName)
	})

	registry.RegisterToolCallHook(hook)

	data := &ToolCallHookData{ToolName: "search"}
	registry.ExecutePreToolCallHooks(context.Background(), data)

	if len(logged) != 1 || logged[0] != "search" {
		t.Errorf("expected ['search'], got %v", logged)
	}
}

func TestNewToolCallValidatorHook(t *testing.T) {
	registry := NewHookRegistry()

	hook := NewToolCallValidatorHook("validator", func(data *ToolCallHookData) error {
		if data.ToolName == "dangerous" {
			return errors.New("tool not allowed")
		}
		return nil
	})

	registry.RegisterToolCallHook(hook)

	data := &ToolCallHookData{ToolName: "safe"}
	_, _, err := registry.ExecutePreToolCallHooks(context.Background(), data)
	if err != nil {
		t.Errorf("unexpected error for safe tool: %v", err)
	}

	data = &ToolCallHookData{ToolName: "dangerous"}
	_, _, err = registry.ExecutePreToolCallHooks(context.Background(), data)
	if err == nil {
		t.Error("expected error for dangerous tool")
	}
}

func TestNewResponseTransformerHook(t *testing.T) {
	registry := NewHookRegistry()

	hook := NewResponseTransformerHook("transformer", HookPriorityNormal, func(response string) string {
		return "[Processed] " + response
	})

	registry.RegisterPromptHook(hook)

	data := &PromptHookData{Response: "Original response"}
	result, _ := registry.ExecutePostPromptHooks(context.Background(), data)

	if result.Response != "[Processed] Original response" {
		t.Errorf("unexpected response: %s", result.Response)
	}
}

func TestHookRegistry_Stats(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPrePromptHook("pre1", HookPriorityNormal, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{Continue: true}
	})
	registry.RegisterPrePromptHook("pre2", HookPriorityNormal, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{Continue: true}
	})
	registry.RegisterPostPromptHook("post1", HookPriorityNormal, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{Continue: true}
	})
	registry.RegisterPreToolCallHook("tool1", HookPriorityNormal, func(ctx context.Context, data *ToolCallHookData) HookResult {
		return HookResult{Continue: true}
	})
	registry.RegisterPostToolCallHook("tool2", HookPriorityNormal, func(ctx context.Context, data *ToolCallHookData) HookResult {
		return HookResult{Continue: true}
	})
	registry.RegisterPreStoreHook("store1", HookPriorityNormal, func(ctx context.Context, data *StoreHookData) HookResult {
		return HookResult{Continue: true}
	})
	registry.RegisterPostStoreHook("store2", HookPriorityNormal, func(ctx context.Context, data *StoreHookData) HookResult {
		return HookResult{Continue: true}
	})
	registry.RegisterPreQueryHook("query1", HookPriorityNormal, func(ctx context.Context, data *QueryHookData) HookResult {
		return HookResult{Continue: true}
	})
	registry.RegisterPostQueryHook("query2", HookPriorityNormal, func(ctx context.Context, data *QueryHookData) HookResult {
		return HookResult{Continue: true}
	})

	stats := registry.Stats()

	if stats.PrePromptHooks != 2 {
		t.Errorf("expected 2 pre-prompt hooks, got %d", stats.PrePromptHooks)
	}
	if stats.PostPromptHooks != 1 {
		t.Errorf("expected 1 post-prompt hook, got %d", stats.PostPromptHooks)
	}
	if stats.PreToolCallHooks != 1 {
		t.Errorf("expected 1 pre-tool-call hook, got %d", stats.PreToolCallHooks)
	}
	if stats.PostToolCallHooks != 1 {
		t.Errorf("expected 1 post-tool-call hook, got %d", stats.PostToolCallHooks)
	}
	if stats.PreStoreHooks != 1 {
		t.Errorf("expected 1 pre-store hook, got %d", stats.PreStoreHooks)
	}
	if stats.PostStoreHooks != 1 {
		t.Errorf("expected 1 post-store hook, got %d", stats.PostStoreHooks)
	}
	if stats.PreQueryHooks != 1 {
		t.Errorf("expected 1 pre-query hook, got %d", stats.PreQueryHooks)
	}
	if stats.PostQueryHooks != 1 {
		t.Errorf("expected 1 post-query hook, got %d", stats.PostQueryHooks)
	}
	if stats.TotalHooks != 9 {
		t.Errorf("expected 9 total hooks, got %d", stats.TotalHooks)
	}
}

func TestHookRegistry_MultipleInjectContexts(t *testing.T) {
	registry := NewHookRegistry()

	registry.RegisterPrePromptHook("injector1", HookPriorityHigh, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{
			Continue:      true,
			InjectContext: "First injection",
		}
	})

	registry.RegisterPrePromptHook("injector2", HookPriorityLow, func(ctx context.Context, data *PromptHookData) HookResult {
		return HookResult{
			Continue:      true,
			InjectContext: "Second injection",
		}
	})

	data := &PromptHookData{SystemPrompt: "Base."}
	result, _ := registry.ExecutePrePromptHooks(context.Background(), data)

	expected := "Base.\nFirst injection\nSecond injection"
	if result.SystemPrompt != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, result.SystemPrompt)
	}
}
