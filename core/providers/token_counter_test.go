package providers

import (
	"sync"
	"testing"
)

// =============================================================================
// CharacterBasedCounter Tests
// =============================================================================

func TestNewCharacterBasedCounter_DefaultConfig(t *testing.T) {
	config := DefaultTokenCounterConfig()
	counter := NewCharacterBasedCounter(config)

	if counter == nil {
		t.Fatal("expected non-nil counter")
	}
	if counter.config.FallbackCharsPerToken != 4 {
		t.Errorf("expected FallbackCharsPerToken=4, got %d", counter.config.FallbackCharsPerToken)
	}
}

func TestNewCharacterBasedCounter_InvalidConfig(t *testing.T) {
	config := TokenCounterConfig{FallbackCharsPerToken: 0}
	counter := NewCharacterBasedCounter(config)

	if counter.config.FallbackCharsPerToken != 4 {
		t.Errorf("expected default FallbackCharsPerToken=4, got %d", counter.config.FallbackCharsPerToken)
	}

	config.FallbackCharsPerToken = -1
	counter = NewCharacterBasedCounter(config)
	if counter.config.FallbackCharsPerToken != 4 {
		t.Errorf("expected default for negative value, got %d", counter.config.FallbackCharsPerToken)
	}
}

func TestCharacterBasedCounter_CountText(t *testing.T) {
	counter := NewCharacterBasedCounter(DefaultTokenCounterConfig())

	tests := []struct {
		name     string
		text     string
		expected int
	}{
		{"empty string", "", 0},
		{"single char", "a", 1},
		{"exactly 4 chars", "abcd", 1},
		{"5 chars", "abcde", 2},
		{"8 chars", "abcdefgh", 2},
		{"unicode", "你好世界", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := counter.CountText(tt.text)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if count != tt.expected {
				t.Errorf("expected %d tokens, got %d", tt.expected, count)
			}
		})
	}
}

func TestCharacterBasedCounter_Count(t *testing.T) {
	counter := NewCharacterBasedCounter(DefaultTokenCounterConfig())

	messages := []Message{
		{Role: RoleUser, Content: "Hello"},          // 5 chars = 2 tokens
		{Role: RoleAssistant, Content: "Hi there!"}, // 9 chars = 3 tokens
	}

	count, err := counter.Count(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 5 { // 2 + 3
		t.Errorf("expected 5 tokens, got %d", count)
	}
}

func TestCharacterBasedCounter_CountWithToolCalls(t *testing.T) {
	counter := NewCharacterBasedCounter(DefaultTokenCounterConfig())

	messages := []Message{
		{
			Role:    RoleAssistant,
			Content: "Test",
			ToolCalls: []ToolCall{
				{Name: "read_file", Arguments: `{"path":"test.txt"}`},
			},
		},
	}

	count, err := counter.Count(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 9 {
		t.Errorf("expected 9 tokens, got %d", count)
	}
}

func TestCharacterBasedCounter_MaxContextTokens(t *testing.T) {
	counter := NewCharacterBasedCounter(DefaultTokenCounterConfig())

	tests := []struct {
		model    string
		expected int
	}{
		{"gpt-4o", 128000},
		{"gpt-4o-mini", 128000},
		{"claude-opus-4-5-20251101", 200000},
		{"claude-sonnet-4-5-20250901", 1000000},
		{"gemini-3-pro", 2000000},
		{"unknown-model", 128000}, // default
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			limit := counter.MaxContextTokens(tt.model)
			if limit != tt.expected {
				t.Errorf("expected %d for %s, got %d", tt.expected, tt.model, limit)
			}
		})
	}
}

// =============================================================================
// ProviderTokenCounter Tests
// =============================================================================

func TestNewProviderTokenCounter(t *testing.T) {
	config := DefaultTokenCounterConfig()
	counter := NewProviderTokenCounter(config)

	if counter == nil {
		t.Fatal("expected non-nil counter")
	}
	if counter.fallback == nil {
		t.Fatal("expected non-nil fallback counter")
	}
	if len(counter.counters) != 0 {
		t.Errorf("expected empty counters map, got %d entries", len(counter.counters))
	}
}

func TestProviderTokenCounter_Count(t *testing.T) {
	counter := NewProviderTokenCounter(DefaultTokenCounterConfig())

	messages := []Message{
		{Role: RoleUser, Content: "Hello world"}, // 11 chars = 3 tokens
	}

	count, err := counter.Count(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 tokens, got %d", count)
	}
}

func TestProviderTokenCounter_CountText(t *testing.T) {
	counter := NewProviderTokenCounter(DefaultTokenCounterConfig())

	count, err := counter.CountText("Hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 { // 11 chars / 4 = 2.75, rounds up to 3
		t.Errorf("expected 3 tokens, got %d", count)
	}
}

func TestProviderTokenCounter_CountWithTools(t *testing.T) {
	counter := NewProviderTokenCounter(DefaultTokenCounterConfig())

	messages := []Message{
		{Role: RoleUser, Content: "Test"}, // 4 chars = 1 token
	}

	tools := []Tool{
		{
			Name:        "read_file",                      // 9 chars = 3 tokens
			Description: "Reads a file from disk",         // 22 chars = 6 tokens
			Parameters:  map[string]any{"path": "string"}, // ~20 chars JSON = 5 tokens
		},
	}

	count, err := counter.CountWithTools(messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should include tool definitions
	if count <= 1 {
		t.Errorf("expected more than message tokens when including tools, got %d", count)
	}
}

func TestProviderTokenCounter_CountWithTools_Disabled(t *testing.T) {
	config := TokenCounterConfig{
		FallbackCharsPerToken:  4,
		IncludeToolDefinitions: false,
	}
	counter := NewProviderTokenCounter(config)

	messages := []Message{
		{Role: RoleUser, Content: "Test"}, // 4 chars = 1 token
	}

	tools := []Tool{
		{Name: "read_file", Description: "Reads a file", Parameters: map[string]any{}},
	}

	count, err := counter.CountWithTools(messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should NOT include tool definitions
	if count != 1 {
		t.Errorf("expected only message tokens (1), got %d", count)
	}
}

func TestProviderTokenCounter_RegisterAndGetCounter(t *testing.T) {
	counter := NewProviderTokenCounter(DefaultTokenCounterConfig())

	// Create a mock counter
	mock := &mockTokenCounter{fixedCount: 42}
	counter.RegisterCounter("test-model", mock)

	// Should get the registered counter
	got := counter.GetCounter("test-model")
	if got != mock {
		t.Error("expected registered counter")
	}

	// Should get fallback for unregistered model
	fallback := counter.GetCounter("unknown-model")
	if fallback == mock {
		t.Error("expected fallback counter for unknown model")
	}
}

func TestProviderTokenCounter_Concurrent(t *testing.T) {
	counter := NewProviderTokenCounter(DefaultTokenCounterConfig())

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			// Register counters concurrently
			mock := &mockTokenCounter{fixedCount: n}
			counter.RegisterCounter("model-"+string(rune('a'+n%26)), mock)

			// Get counters concurrently
			_ = counter.GetCounter("model-a")

			// Count messages concurrently
			messages := []Message{{Role: RoleUser, Content: "Test"}}
			_, _ = counter.Count(messages)
		}(i)
	}
	wg.Wait()
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestCharacterBasedCounter_EmptyMessages(t *testing.T) {
	counter := NewCharacterBasedCounter(DefaultTokenCounterConfig())

	count, err := counter.Count(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens for nil messages, got %d", count)
	}

	count, err = counter.Count([]Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens for empty messages, got %d", count)
	}
}

func TestCharacterBasedCounter_EmptyToolCalls(t *testing.T) {
	counter := NewCharacterBasedCounter(DefaultTokenCounterConfig())

	messages := []Message{
		{Role: RoleAssistant, Content: "Test", ToolCalls: nil},
		{Role: RoleAssistant, Content: "Test", ToolCalls: []ToolCall{}},
	}

	count, err := counter.Count(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 { // 2 messages * 1 token each
		t.Errorf("expected 2 tokens, got %d", count)
	}
}

func TestProviderTokenCounter_NilParameters(t *testing.T) {
	counter := NewProviderTokenCounter(DefaultTokenCounterConfig())

	tools := []Tool{
		{Name: "test", Description: "test", Parameters: nil},
	}

	messages := []Message{{Role: RoleUser, Content: "Test"}}
	count, err := counter.CountWithTools(messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count < 1 {
		t.Errorf("expected at least 1 token, got %d", count)
	}
}

// =============================================================================
// Helpers
// =============================================================================

type mockTokenCounter struct {
	fixedCount int
}

func (m *mockTokenCounter) Count(messages []Message) (int, error) {
	return m.fixedCount, nil
}

func (m *mockTokenCounter) CountText(text string) (int, error) {
	return m.fixedCount, nil
}

func (m *mockTokenCounter) MaxContextTokens(model string) int {
	return 100000
}
