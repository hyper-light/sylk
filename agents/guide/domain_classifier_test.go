package guide

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/domain"
	"github.com/adalundhe/sylk/core/domain/cache"
)

func TestNewDomainClassifier(t *testing.T) {
	dc, err := NewDomainClassifier(nil)
	if err != nil {
		t.Fatalf("NewDomainClassifier failed: %v", err)
	}
	defer dc.Close()

	if dc == nil {
		t.Fatal("DomainClassifier should not be nil")
	}
	if !dc.IsEnabled() {
		t.Error("DomainClassifier should be enabled by default")
	}
}

func TestNewDomainClassifier_WithConfig(t *testing.T) {
	config := &DomainClassifierConfig{
		Config:         domain.DefaultDomainConfig(),
		EnableCache:    true,
		CacheNamespace: "test",
	}

	dc, err := NewDomainClassifier(config)
	if err != nil {
		t.Fatalf("NewDomainClassifier failed: %v", err)
	}
	defer dc.Close()

	if dc.cache == nil {
		t.Error("Cache should be enabled")
	}
}

func TestNewDomainClassifier_NoCache(t *testing.T) {
	config := &DomainClassifierConfig{
		EnableCache: false,
	}

	dc, err := NewDomainClassifier(config)
	if err != nil {
		t.Fatalf("NewDomainClassifier failed: %v", err)
	}
	defer dc.Close()

	if dc.cache != nil {
		t.Error("Cache should not be enabled")
	}
}

func TestDomainClassifier_Classify(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: false})
	defer dc.Close()

	ctx := context.Background()
	result, err := dc.Classify(ctx, "find the function in our code", "session-1")

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if result == nil {
		t.Fatal("Result should not be nil")
	}
	if result.OriginalQuery != "find the function in our code" {
		t.Errorf("OriginalQuery = %s, want 'find the function in our code'", result.OriginalQuery)
	}
}

func TestDomainClassifier_Classify_WithCache(t *testing.T) {
	config := &DomainClassifierConfig{
		EnableCache:    true,
		CacheConfig:    &cache.CacheConfig{},
		CacheNamespace: "test",
	}
	dc, _ := NewDomainClassifier(config)
	defer dc.Close()

	ctx := context.Background()

	result1, _ := dc.Classify(ctx, "test query", "session-1")
	dc.WaitForCache()

	result2, _ := dc.Classify(ctx, "test query", "session-1")

	if !result2.CacheHit {
		t.Error("Second call should hit cache")
	}

	if result1.OriginalQuery != result2.OriginalQuery {
		t.Error("Cached result should match original")
	}
}

func TestDomainClassifier_Classify_Disabled(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: false})
	defer dc.Close()

	dc.Disable()

	ctx := context.Background()
	result, err := dc.Classify(ctx, "test query", "")

	if err != nil {
		t.Fatalf("Classify failed: %v", err)
	}
	if result.ClassificationMethod != "disabled" {
		t.Errorf("Method = %s, want disabled", result.ClassificationMethod)
	}
}

func TestDomainClassifier_ClassifyWithCaller(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: false})
	defer dc.Close()

	ctx := context.Background()
	result, err := dc.ClassifyWithCaller(ctx, "test query", concurrency.AgentLibrarian)

	if err != nil {
		t.Fatalf("ClassifyWithCaller failed: %v", err)
	}
	if result == nil {
		t.Fatal("Result should not be nil")
	}
}

func TestDomainClassifier_EnableDisable(t *testing.T) {
	dc, _ := NewDomainClassifier(nil)
	defer dc.Close()

	if !dc.IsEnabled() {
		t.Error("Should be enabled initially")
	}

	dc.Disable()
	if dc.IsEnabled() {
		t.Error("Should be disabled")
	}

	dc.Enable()
	if !dc.IsEnabled() {
		t.Error("Should be enabled again")
	}
}

func TestDomainClassifier_ClearCache(t *testing.T) {
	config := &DomainClassifierConfig{
		EnableCache: true,
	}
	dc, _ := NewDomainClassifier(config)
	defer dc.Close()

	ctx := context.Background()
	dc.Classify(ctx, "test query", "")

	stats := dc.CacheStats()
	if stats == nil {
		t.Fatal("Stats should not be nil")
	}

	dc.ClearCache()

	// Cache should be cleared - verify by checking a new query doesn't get cached hit
	result, _ := dc.Classify(ctx, "test query", "")
	if result.CacheHit {
		t.Error("Should not be cache hit after clear")
	}
}

func TestDomainClassifier_CacheStats_NoCache(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: false})
	defer dc.Close()

	stats := dc.CacheStats()
	if stats != nil {
		t.Error("Stats should be nil when cache disabled")
	}
}

func TestDomainClassifier_GetConfig(t *testing.T) {
	config := &DomainClassifierConfig{
		Config: domain.DefaultDomainConfig(),
	}
	dc, _ := NewDomainClassifier(config)
	defer dc.Close()

	cfg := dc.GetConfig()
	if cfg == nil {
		t.Error("Config should not be nil")
	}
}

func TestDomainClassifier_StageCount(t *testing.T) {
	dc, _ := NewDomainClassifier(nil)
	defer dc.Close()

	// Should have at least lexical classifier
	if dc.StageCount() < 1 {
		t.Errorf("StageCount() = %d, want >= 1", dc.StageCount())
	}
}

func TestDomainClassifier_Close(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: true})

	dc.Close()

	// Should be safe to close multiple times
	dc.Close()
}

func TestDomainClassifier_Classify_ContextCanceled(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: false})
	defer dc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := dc.Classify(ctx, "test query", "")
	if err == nil {
		t.Error("Should return error for canceled context")
	}
}

func TestDomainClassifier_Classify_LibrarianKeywords(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: false})
	defer dc.Close()

	ctx := context.Background()
	result, _ := dc.Classify(ctx, "show me the function in our code", "")

	if !result.HasDomain(domain.DomainLibrarian) {
		t.Error("Should detect Librarian domain for code-related query")
	}
}

func TestDomainClassifier_Classify_AcademicKeywords(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: false})
	defer dc.Close()

	ctx := context.Background()
	result, _ := dc.Classify(ctx, "what is the best practice according to documentation", "")

	if !result.HasDomain(domain.DomainAcademic) {
		t.Error("Should detect Academic domain for documentation query")
	}
}

func TestDomainClassifier_Classify_TesterKeywords(t *testing.T) {
	dc, _ := NewDomainClassifier(&DomainClassifierConfig{EnableCache: false})
	defer dc.Close()

	ctx := context.Background()
	result, _ := dc.Classify(ctx, "write a unit test with coverage", "")

	if !result.HasDomain(domain.DomainTester) {
		t.Error("Should detect Tester domain for test-related query")
	}
}
