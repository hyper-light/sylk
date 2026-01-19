package classifier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
)

// MockLLMProvider implements LLMProvider for testing.
type MockLLMProvider struct {
	responses map[string]string
	err       error
	callCount int
}

func (m *MockLLMProvider) ClassifyDomain(_ context.Context, query string, _ string) (string, error) {
	m.callCount++
	if m.err != nil {
		return "", m.err
	}
	if response, ok := m.responses[query]; ok {
		return response, nil
	}
	return `{"domains": []}`, nil
}

func TestNewLLMClassifier(t *testing.T) {
	provider := &MockLLMProvider{}
	lc := NewLLMClassifier(provider, nil)

	if lc == nil {
		t.Fatal("NewLLMClassifier returned nil")
	}
	if lc.timeout != defaultLLMTimeout {
		t.Errorf("timeout = %v, want %v", lc.timeout, defaultLLMTimeout)
	}
	if lc.prompt == "" {
		t.Error("Default prompt should be set")
	}
}

func TestNewLLMClassifier_WithConfig(t *testing.T) {
	provider := &MockLLMProvider{}
	config := &LLMClassifierConfig{
		Timeout:  1 * time.Second,
		Prompt:   "custom prompt",
		CacheTTL: 10 * time.Minute,
	}
	lc := NewLLMClassifier(provider, config)

	if lc.timeout != 1*time.Second {
		t.Errorf("timeout = %v, want 1s", lc.timeout)
	}
	if lc.prompt != "custom prompt" {
		t.Errorf("prompt = %s, want custom prompt", lc.prompt)
	}
	if lc.cacheTTL != 10*time.Minute {
		t.Errorf("cacheTTL = %v, want 10m", lc.cacheTTL)
	}
}

func TestLLMClassifier_Name(t *testing.T) {
	lc := NewLLMClassifier(nil, nil)

	if lc.Name() != "llm" {
		t.Errorf("Name() = %s, want llm", lc.Name())
	}
}

func TestLLMClassifier_Priority(t *testing.T) {
	lc := NewLLMClassifier(nil, nil)

	if lc.Priority() != 30 {
		t.Errorf("Priority() = %d, want 30", lc.Priority())
	}
}

func TestLLMClassifier_Classify_EmptyQuery(t *testing.T) {
	provider := &MockLLMProvider{}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, err := lc.Classify(ctx, "", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Empty query should produce empty result")
	}
}

func TestLLMClassifier_Classify_NilProvider(t *testing.T) {
	lc := NewLLMClassifier(nil, nil)

	ctx := context.Background()
	result, err := lc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Nil provider should produce empty result")
	}
}

func TestLLMClassifier_Classify_SingleDomain(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"find the function": `{"domains": [{"domain": "librarian", "confidence": 0.85, "reasoning": "code search"}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, err := lc.Classify(ctx, "find the function", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	conf := result.GetConfidence(domain.DomainLibrarian)
	if conf != 0.85 {
		t.Errorf("Librarian confidence = %f, want 0.85", conf)
	}
}

func TestLLMClassifier_Classify_MultipleDomains(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"code with tests": `{"domains": [
				{"domain": "librarian", "confidence": 0.75},
				{"domain": "tester", "confidence": 0.70}
			]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, err := lc.Classify(ctx, "code with tests", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if result.DomainCount() != 2 {
		t.Errorf("DomainCount() = %d, want 2", result.DomainCount())
	}
}

func TestLLMClassifier_Classify_BelowMinConfidence(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"vague query": `{"domains": [{"domain": "librarian", "confidence": 0.30}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, err := lc.Classify(ctx, "vague query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}

	if !result.IsEmpty() {
		t.Error("Low confidence should be filtered out")
	}
}

func TestLLMClassifier_Classify_InvalidJSON(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"test": "not json",
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, err := lc.Classify(ctx, "test", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Should not return error on invalid JSON: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Invalid JSON should produce empty result")
	}
}

func TestLLMClassifier_Classify_UnknownDomain(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"test": `{"domains": [{"domain": "unknown_domain", "confidence": 0.90}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, err := lc.Classify(ctx, "test", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Unknown domain should be filtered out")
	}
}

func TestLLMClassifier_Classify_ProviderError(t *testing.T) {
	provider := &MockLLMProvider{
		err: errors.New("LLM error"),
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, err := lc.Classify(ctx, "test query", concurrency.AgentGuide)

	if err != nil {
		t.Fatalf("Should not return error on provider failure: %v", err)
	}
	if !result.IsEmpty() {
		t.Error("Provider error should produce empty result")
	}
}

func TestLLMClassifier_Classify_ContextCanceled(t *testing.T) {
	provider := &MockLLMProvider{}
	lc := NewLLMClassifier(provider, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := lc.Classify(ctx, "test query", concurrency.AgentGuide)
	if err == nil {
		t.Error("Should return error for canceled context")
	}
}

func TestLLMClassifier_Classify_HasSignals(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"test": `{"domains": [{"domain": "librarian", "confidence": 0.85, "reasoning": "code search"}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, _ := lc.Classify(ctx, "test", concurrency.AgentGuide)

	signals := result.GetSignals(domain.DomainLibrarian)
	if len(signals) < 2 {
		t.Error("Should include llm_inference and reasoning signals")
	}
}

func TestLLMClassifier_Classify_Method(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"test": `{"domains": [{"domain": "librarian", "confidence": 0.85}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	result, _ := lc.Classify(ctx, "test", concurrency.AgentGuide)

	if result.Method != "llm" {
		t.Errorf("Method = %s, want llm", result.Method)
	}
}

func TestLLMClassifier_Classify_Caching(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"cached query": `{"domains": [{"domain": "librarian", "confidence": 0.85}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()

	// First call
	result1, _ := lc.Classify(ctx, "cached query", concurrency.AgentGuide)
	if provider.callCount != 1 {
		t.Errorf("callCount = %d, want 1 after first call", provider.callCount)
	}

	// Second call should use cache
	result2, _ := lc.Classify(ctx, "cached query", concurrency.AgentGuide)
	if provider.callCount != 1 {
		t.Errorf("callCount = %d, want 1 (cached)", provider.callCount)
	}

	// Results should be equivalent
	if result1.GetConfidence(domain.DomainLibrarian) != result2.GetConfidence(domain.DomainLibrarian) {
		t.Error("Cached result should match original")
	}
}

func TestLLMClassifier_Classify_CacheCaseInsensitive(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"test query": `{"domains": [{"domain": "librarian", "confidence": 0.85}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()

	lc.Classify(ctx, "test query", concurrency.AgentGuide)
	lc.Classify(ctx, "TEST QUERY", concurrency.AgentGuide)
	lc.Classify(ctx, "  Test Query  ", concurrency.AgentGuide)

	if provider.callCount != 1 {
		t.Errorf("callCount = %d, want 1 (all should hit cache)", provider.callCount)
	}
}

func TestLLMClassifier_ClearCache(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"test": `{"domains": [{"domain": "librarian", "confidence": 0.85}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	lc.Classify(ctx, "test", concurrency.AgentGuide)

	if lc.CacheSize() != 1 {
		t.Errorf("CacheSize() = %d, want 1", lc.CacheSize())
	}

	lc.ClearCache()

	if lc.CacheSize() != 0 {
		t.Errorf("CacheSize() = %d after clear, want 0", lc.CacheSize())
	}
}

func TestLLMClassifier_CacheSize(t *testing.T) {
	provider := &MockLLMProvider{
		responses: map[string]string{
			"query1": `{"domains": [{"domain": "librarian", "confidence": 0.85}]}`,
			"query2": `{"domains": [{"domain": "academic", "confidence": 0.75}]}`,
		},
	}
	lc := NewLLMClassifier(provider, nil)

	ctx := context.Background()
	lc.Classify(ctx, "query1", concurrency.AgentGuide)
	lc.Classify(ctx, "query2", concurrency.AgentGuide)

	if lc.CacheSize() != 2 {
		t.Errorf("CacheSize() = %d, want 2", lc.CacheSize())
	}
}

func TestLLMClassifier_GetTimeout(t *testing.T) {
	config := &LLMClassifierConfig{Timeout: 1 * time.Second}
	lc := NewLLMClassifier(nil, config)

	if lc.GetTimeout() != 1*time.Second {
		t.Errorf("GetTimeout() = %v, want 1s", lc.GetTimeout())
	}
}

func TestLLMClassifier_SetPrompt(t *testing.T) {
	lc := NewLLMClassifier(nil, nil)
	lc.SetPrompt("new prompt")

	if lc.prompt != "new prompt" {
		t.Errorf("prompt = %s, want new prompt", lc.prompt)
	}
}

func TestLLMClassifier_UpdateConfig(t *testing.T) {
	lc := NewLLMClassifier(nil, nil)

	newConfig := &LLMClassifierConfig{
		Timeout: 2 * time.Second,
		Prompt:  "updated prompt",
	}
	lc.UpdateConfig(newConfig)

	if lc.GetTimeout() != 2*time.Second {
		t.Errorf("timeout = %v, want 2s", lc.GetTimeout())
	}
	if lc.prompt != "updated prompt" {
		t.Errorf("prompt = %s, want updated prompt", lc.prompt)
	}
}

func TestLLMClassifier_UpdateConfig_Nil(t *testing.T) {
	lc := NewLLMClassifier(nil, nil)
	originalTimeout := lc.GetTimeout()

	lc.UpdateConfig(nil)

	if lc.GetTimeout() != originalTimeout {
		t.Error("Nil config should not change values")
	}
}

func TestNormalizeQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello world"},
		{"  trimmed  ", "trimmed"},
		{"UPPERCASE", "uppercase"},
		{"", ""},
	}

	for _, tt := range tests {
		got := normalizeQuery(tt.input)
		if got != tt.want {
			t.Errorf("normalizeQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDefaultClassificationPrompt(t *testing.T) {
	prompt := defaultClassificationPrompt()

	if prompt == "" {
		t.Error("Default prompt should not be empty")
	}

	// Should mention key domains
	keywords := []string{"librarian", "academic", "architect", "engineer", "tester"}
	for _, kw := range keywords {
		if !contains(prompt, kw) {
			t.Errorf("Default prompt should mention %q", kw)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
