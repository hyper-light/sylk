package hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Test Helpers
// =============================================================================

// MockFailureQuerier implements FailurePatternQuerier for testing.
type MockFailureQuerier struct {
	mu       sync.Mutex
	failures []FailurePattern
	err      error
	queries  []string
}

func (m *MockFailureQuerier) QuerySimilarFailures(
	ctx context.Context,
	query string,
	threshold float64,
) ([]FailurePattern, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.queries = append(m.queries, query)

	if m.err != nil {
		return nil, m.err
	}

	return m.failures, nil
}

func (m *MockFailureQuerier) SetFailures(failures []FailurePattern) {
	m.mu.Lock()
	m.failures = failures
	m.mu.Unlock()
}

func (m *MockFailureQuerier) SetError(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
}

func (m *MockFailureQuerier) GetQueries() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queries
}

func createFailureTestPromptData() *ctxpkg.PromptHookData {
	return &ctxpkg.PromptHookData{
		SessionID:  "test-session",
		AgentID:    "test-agent",
		AgentType:  "engineer",
		TurnNumber: 1,
		Query:      "implement user authentication",
		Timestamp:  time.Now(),
	}
}

// =============================================================================
// Construction Tests
// =============================================================================

func TestNewFailurePatternWarningHook_DefaultConfig(t *testing.T) {
	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{})

	if hook.similarityThreshold != DefaultSimilarityThreshold {
		t.Errorf("similarityThreshold = %v, want %v", hook.similarityThreshold, DefaultSimilarityThreshold)
	}

	if hook.minRecurrence != MinRecurrenceForWarning {
		t.Errorf("minRecurrence = %d, want %d", hook.minRecurrence, MinRecurrenceForWarning)
	}

	if hook.maxWarnings != MaxFailureWarnings {
		t.Errorf("maxWarnings = %d, want %d", hook.maxWarnings, MaxFailureWarnings)
	}

	if !hook.enabled {
		t.Error("Hook should be enabled by default")
	}
}

func TestNewFailurePatternWarningHook_CustomConfig(t *testing.T) {
	querier := &MockFailureQuerier{}
	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier:      querier,
		SimilarityThreshold: 0.8,
		MinRecurrence:       3,
		MaxWarnings:         5,
	})

	if hook.failureQuerier != querier {
		t.Error("FailureQuerier not set correctly")
	}

	if hook.similarityThreshold != 0.8 {
		t.Errorf("similarityThreshold = %v, want 0.8", hook.similarityThreshold)
	}

	if hook.minRecurrence != 3 {
		t.Errorf("minRecurrence = %d, want 3", hook.minRecurrence)
	}

	if hook.maxWarnings != 5 {
		t.Errorf("maxWarnings = %d, want 5", hook.maxWarnings)
	}
}

// =============================================================================
// Hook Interface Tests
// =============================================================================

func TestFailurePatternWarningHook_Name(t *testing.T) {
	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{})

	if hook.Name() != FailurePatternHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), FailurePatternHookName)
	}
}

func TestFailurePatternWarningHook_Phase(t *testing.T) {
	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("Phase() = %v, want HookPhasePrePrompt", hook.Phase())
	}
}

func TestFailurePatternWarningHook_Priority(t *testing.T) {
	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityNormal) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityNormal)
	}
}

func TestFailurePatternWarningHook_Enabled(t *testing.T) {
	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{})

	if !hook.Enabled() {
		t.Error("Hook should be enabled by default")
	}

	hook.SetEnabled(false)

	if hook.Enabled() {
		t.Error("Hook should be disabled after SetEnabled(false)")
	}
}

// =============================================================================
// Execute Tests - No Warnings
// =============================================================================

func TestFailureHook_Execute_NoWarningsWhenNoFailures(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when no failures found")
	}
}

func TestFailureHook_Execute_NoWarningsWhenBelowRecurrence(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{
			ApproachSignature: "auth with plain password",
			ErrorPattern:      "security vulnerability",
			RecurrenceCount:   1, // Below threshold
			Similarity:        0.85,
		},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when failures below recurrence threshold")
	}
}

// =============================================================================
// Execute Tests - With Warnings
// =============================================================================

func TestFailureHook_Execute_InjectsWarning(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{
			ApproachSignature: "storing passwords in plain text",
			ErrorPattern:      "data breach due to unencrypted passwords",
			Resolution:        "use bcrypt for password hashing",
			RecurrenceCount:   3,
			Similarity:        0.85,
		},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("Should modify data when failure warning injected")
	}

	if data.PrefetchedContent == nil {
		t.Fatal("PrefetchedContent should not be nil")
	}

	if len(data.PrefetchedContent.Excerpts) == 0 {
		t.Fatal("Should have at least one excerpt with warning")
	}

	warning := data.PrefetchedContent.Excerpts[0].Content
	if !strings.Contains(warning, "[FAILURE_PATTERN_WARNING]") {
		t.Error("Warning should contain [FAILURE_PATTERN_WARNING] header")
	}

	if !strings.Contains(warning, "storing passwords in plain text") {
		t.Error("Warning should contain the approach signature")
	}

	if !strings.Contains(warning, "use bcrypt for password hashing") {
		t.Error("Warning should contain the resolution")
	}
}

func TestFailureHook_Execute_MultipleWarnings(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{
			ApproachSignature: "failure 1",
			ErrorPattern:      "error 1",
			RecurrenceCount:   2,
			Similarity:        0.9,
		},
		{
			ApproachSignature: "failure 2",
			ErrorPattern:      "error 2",
			RecurrenceCount:   3,
			Similarity:        0.85,
		},
		{
			ApproachSignature: "failure 3",
			ErrorPattern:      "error 3",
			RecurrenceCount:   5,
			Similarity:        0.8,
		},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
		MaxWarnings:    2, // Limit to 2
	})

	ctx := context.Background()
	data := createFailureTestPromptData()

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("Should modify data when failure warnings injected")
	}

	warning := data.PrefetchedContent.Excerpts[0].Content

	// Should have at most 2 warnings
	if strings.Count(warning, "Warning") > 2 {
		t.Error("Should have at most 2 warnings")
	}

	if !strings.Contains(warning, "failure 1") {
		t.Error("Should contain first failure")
	}

	if !strings.Contains(warning, "failure 2") {
		t.Error("Should contain second failure")
	}
}

func TestFailureHook_Execute_PreservesExistingContent(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{
			ApproachSignature: "test failure",
			ErrorPattern:      "test error",
			RecurrenceCount:   2,
			Similarity:        0.85,
		},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()
	data.PrefetchedContent = &ctxpkg.AugmentedQuery{
		OriginalQuery: "original query",
		Excerpts: []ctxpkg.Excerpt{
			{Source: "existing", Content: "existing content"},
		},
	}

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	// Should have 2 excerpts now (warning + original)
	if len(data.PrefetchedContent.Excerpts) != 2 {
		t.Errorf("Expected 2 excerpts, got %d", len(data.PrefetchedContent.Excerpts))
	}

	// Warning should be first
	if !strings.Contains(data.PrefetchedContent.Excerpts[0].Content, "[FAILURE_PATTERN_WARNING]") {
		t.Error("Warning should be first excerpt")
	}

	// Original content should be preserved
	if data.PrefetchedContent.Excerpts[1].Content != "existing content" {
		t.Error("Original content should be preserved")
	}
}

// =============================================================================
// Execute Tests - Edge Cases
// =============================================================================

func TestFailureHook_Execute_SkipsWhenDisabled(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{RecurrenceCount: 5, Similarity: 0.9},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createFailureTestPromptData()

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}
}

func TestFailureHook_Execute_SkipsNilQuerier(t *testing.T) {
	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: nil,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil querier")
	}
}

func TestFailureHook_Execute_SkipsEmptyQuery(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{RecurrenceCount: 5, Similarity: 0.9},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()
	data.Query = ""

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with empty query")
	}
}

func TestFailureHook_Execute_HandlesQuerierError(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetError(errors.New("query failed"))

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()

	result, err := hook.Execute(ctx, data)

	// Hook should not propagate error
	if err != nil {
		t.Fatalf("Execute should not return error, got %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when query fails")
	}

	// But result should contain the error
	if result.Error == nil {
		t.Error("Result.Error should contain the query error")
	}
}

// =============================================================================
// ToPromptHook Tests
// =============================================================================

func TestFailureHook_ToPromptHook_ReturnsValidHook(t *testing.T) {
	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{})

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != FailurePatternHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), FailurePatternHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePrePrompt", promptHook.Phase())
	}

	if promptHook.Priority() != int(ctxpkg.HookPriorityNormal) {
		t.Errorf("PromptHook Priority = %d, want %d", promptHook.Priority(), ctxpkg.HookPriorityNormal)
	}
}

func TestFailureHook_ToPromptHook_ExecutesCorrectly(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{
			ApproachSignature: "test",
			RecurrenceCount:   2,
			Similarity:        0.85,
		},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	promptHook := hook.ToPromptHook()

	ctx := context.Background()
	data := createFailureTestPromptData()

	result, err := promptHook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("PromptHook Execute error = %v", err)
	}

	if !result.Modified {
		t.Error("PromptHook should trigger warning injection")
	}
}

// =============================================================================
// Warning Content Tests
// =============================================================================

func TestFailureHook_WarningFormatting(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{
			ApproachSignature: "direct database queries without ORM",
			ErrorPattern:      "SQL injection vulnerability detected",
			Resolution:        "use parameterized queries",
			RecurrenceCount:   4,
			Similarity:        0.92,
		},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()

	_, _ = hook.Execute(ctx, data)

	warning := data.PrefetchedContent.Excerpts[0].Content

	// Check all expected components
	expectedComponents := []string{
		"[FAILURE_PATTERN_WARNING]",
		"Warning 1",
		"occurred 4 times",
		"92% similar",
		"direct database queries without ORM",
		"SQL injection vulnerability detected",
		"use parameterized queries",
		"Consider alternative approaches",
	}

	for _, expected := range expectedComponents {
		if !strings.Contains(warning, expected) {
			t.Errorf("Warning should contain: %s", expected)
		}
	}
}

func TestFailureHook_WarningWithoutResolution(t *testing.T) {
	querier := &MockFailureQuerier{}
	querier.SetFailures([]FailurePattern{
		{
			ApproachSignature: "test approach",
			ErrorPattern:      "test error",
			Resolution:        "", // No resolution
			RecurrenceCount:   2,
			Similarity:        0.85,
		},
	})

	hook := NewFailurePatternWarningHook(FailurePatternWarningHookConfig{
		FailureQuerier: querier,
	})

	ctx := context.Background()
	data := createFailureTestPromptData()

	_, _ = hook.Execute(ctx, data)

	warning := data.PrefetchedContent.Excerpts[0].Content

	// Should not include "Resolution" if empty
	if strings.Contains(warning, "**Resolution**:") && strings.Contains(warning, "**Resolution**: \n") {
		t.Error("Should not include empty resolution line")
	}
}
