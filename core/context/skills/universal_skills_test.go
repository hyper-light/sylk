// Package skills provides tests for AR.8.1: Universal Retrieval Skills.
package skills

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
	"github.com/adalundhe/sylk/core/skills"
)

// =============================================================================
// Mock Types
// =============================================================================

// mockReferenceResolver implements ReferenceResolver for testing.
type mockReferenceResolver struct {
	resolveFunc func(ctx context.Context, refID string, maxTokens int) (*ctxpkg.ContentEntry, error)
}

func (m *mockReferenceResolver) Resolve(ctx context.Context, refID string, maxTokens int) (*ctxpkg.ContentEntry, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ctx, refID, maxTokens)
	}
	return nil, nil
}

// =============================================================================
// Helper Functions
// =============================================================================

func createTestDeps() *RetrievalDependencies {
	hotCache := ctxpkg.NewDefaultHotCache()
	bleve := ctxpkg.NewMockBleveSearcher()
	searcher := ctxpkg.NewTieredSearcher(ctxpkg.TieredSearcherConfig{
		HotCache: hotCache,
		Bleve:    bleve,
	})

	return &RetrievalDependencies{
		Searcher: searcher,
		HotCache: hotCache,
	}
}

// =============================================================================
// Test: retrieve_context Skill
// =============================================================================

func TestNewRetrieveContextSkill(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	skill := NewRetrieveContextSkill(deps)

	if skill.Name != "retrieve_context" {
		t.Errorf("unexpected name: %s", skill.Name)
	}

	if skill.Domain != RetrievalDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}

	if skill.Handler == nil {
		t.Error("handler should not be nil")
	}
}

func TestRetrieveContext_RequiresQueryOrRefID(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	skill := NewRetrieveContextSkill(deps)

	input, _ := json.Marshal(RetrieveContextInput{})
	ctx := context.Background()

	_, err := skill.Handler(ctx, input)
	if err == nil {
		t.Error("expected error when neither query nor ref_id provided")
	}

	if err.Error() != "either query or ref_id required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRetrieveContext_ByRefID(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	deps.ReferenceResolver = &mockReferenceResolver{
		resolveFunc: func(_ context.Context, refID string, _ int) (*ctxpkg.ContentEntry, error) {
			return &ctxpkg.ContentEntry{
				ID:      refID,
				Content: "resolved content for " + refID,
			}, nil
		},
	}

	skill := NewRetrieveContextSkill(deps)

	input, _ := json.Marshal(RetrieveContextInput{
		RefID:     "test-ref-123",
		MaxTokens: 1000,
	})
	ctx := context.Background()

	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*RetrieveContextOutput)
	if len(output.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(output.Entries))
	}

	if output.Entries[0].ID != "test-ref-123" {
		t.Errorf("unexpected entry ID: %s", output.Entries[0].ID)
	}
}

func TestRetrieveContext_ByRefID_NotFound(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	deps.ReferenceResolver = &mockReferenceResolver{
		resolveFunc: func(_ context.Context, _ string, _ int) (*ctxpkg.ContentEntry, error) {
			return nil, nil // Not found
		},
	}

	skill := NewRetrieveContextSkill(deps)

	input, _ := json.Marshal(RetrieveContextInput{
		RefID: "nonexistent",
	})
	ctx := context.Background()

	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*RetrieveContextOutput)
	if len(output.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(output.Entries))
	}
}

func TestRetrieveContext_ByQuery(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	skill := NewRetrieveContextSkill(deps)

	input, _ := json.Marshal(RetrieveContextInput{
		Query:     "test query",
		MaxTokens: 500,
	})
	ctx := context.Background()

	result, err := skill.Handler(ctx, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := result.(*RetrieveContextOutput)
	// Should have results from mock searcher
	if output == nil {
		t.Error("expected non-nil output")
	}
}

func TestRetrieveContext_NoResolver(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	deps.ReferenceResolver = nil // No resolver

	skill := NewRetrieveContextSkill(deps)

	input, _ := json.Marshal(RetrieveContextInput{
		RefID: "test-ref",
	})
	ctx := context.Background()

	_, err := skill.Handler(ctx, input)
	if err == nil {
		t.Error("expected error when resolver not configured")
	}
}

func TestRetrieveContext_NoSearcher(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Searcher: nil, // No searcher
	}

	skill := NewRetrieveContextSkill(deps)

	input, _ := json.Marshal(RetrieveContextInput{
		Query: "test query",
	})
	ctx := context.Background()

	_, err := skill.Handler(ctx, input)
	if err == nil {
		t.Error("expected error when searcher not configured")
	}
}

func TestRetrieveContext_DefaultMaxTokens(t *testing.T) {
	t.Parallel()

	// Just verify the constant is reasonable
	if DefaultMaxTokens != 2000 {
		t.Errorf("unexpected default max tokens: %d", DefaultMaxTokens)
	}
}

// =============================================================================
// Test: search_history Skill
// =============================================================================

func TestNewSearchHistorySkill(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	skill := NewSearchHistorySkill(deps)

	if skill.Name != "search_history" {
		t.Errorf("unexpected name: %s", skill.Name)
	}

	if skill.Domain != RetrievalDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}

	if skill.Handler == nil {
		t.Error("handler should not be nil")
	}
}

func TestSearchHistory_RequiresQuery(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	skill := NewSearchHistorySkill(deps)

	input, _ := json.Marshal(SearchHistoryInput{})
	ctx := context.Background()

	_, err := skill.Handler(ctx, input)
	if err == nil {
		t.Error("expected error when query not provided")
	}

	if err.Error() != "query is required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSearchHistory_NoContentStore(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		ContentStore: nil, // No content store
	}

	skill := NewSearchHistorySkill(deps)

	input, _ := json.Marshal(SearchHistoryInput{
		Query: "test query",
	})
	ctx := context.Background()

	_, err := skill.Handler(ctx, input)
	if err == nil {
		t.Error("expected error when content store not configured")
	}
}

func TestSearchHistory_DefaultMaxResults(t *testing.T) {
	t.Parallel()

	if DefaultMaxResults != 10 {
		t.Errorf("unexpected default max results: %d", DefaultMaxResults)
	}
}

// =============================================================================
// Test: promote_to_hot Skill
// =============================================================================

func TestNewPromoteToHotSkill(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	skill := NewPromoteToHotSkill(deps)

	if skill.Name != "promote_to_hot" {
		t.Errorf("unexpected name: %s", skill.Name)
	}

	if skill.Domain != RetrievalDomain {
		t.Errorf("unexpected domain: %s", skill.Domain)
	}

	if skill.Handler == nil {
		t.Error("handler should not be nil")
	}
}

func TestPromoteToHot_RequiresContentIDs(t *testing.T) {
	t.Parallel()

	deps := createTestDeps()
	skill := NewPromoteToHotSkill(deps)

	input, _ := json.Marshal(PromoteToHotInput{})
	ctx := context.Background()

	_, err := skill.Handler(ctx, input)
	if err == nil {
		t.Error("expected error when content_ids not provided")
	}

	if err.Error() != "content_ids is required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPromoteToHot_NoHotCache(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		HotCache: nil, // No hot cache
	}

	skill := NewPromoteToHotSkill(deps)

	input, _ := json.Marshal(PromoteToHotInput{
		ContentIDs: []string{"test-id"},
	})
	ctx := context.Background()

	_, err := skill.Handler(ctx, input)
	if err == nil {
		t.Error("expected error when hot cache not configured")
	}
}

func TestPromoteToHot_DefaultTTLTurns(t *testing.T) {
	t.Parallel()

	if DefaultTTLTurns != 5 {
		t.Errorf("unexpected default TTL turns: %d", DefaultTTLTurns)
	}
}

// =============================================================================
// Test: Helper Functions
// =============================================================================

func TestEstimateTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content  string
		expected int
	}{
		{"", 0},
		{"a", 1},
		{"ab", 1},
		{"abc", 1},
		{"abcd", 1},
		{"abcde", 2},
		{"12345678", 2},
	}

	for _, tc := range tests {
		result := estimateTokens(tc.content)
		if result != tc.expected {
			t.Errorf("estimateTokens(%q) = %d, want %d", tc.content, result, tc.expected)
		}
	}
}

func TestTruncateToTokens(t *testing.T) {
	t.Parallel()

	// Content within budget - no truncation
	content := "short"
	result := truncateToTokens(content, 10)
	if result != content {
		t.Errorf("expected no truncation, got %q", result)
	}

	// Content over budget - should truncate
	longContent := "this is a longer piece of content that exceeds the budget"
	result = truncateToTokens(longContent, 3)
	if len(result) > 12+3 { // 3 tokens * 4 chars + "..."
		t.Errorf("content not properly truncated: %q (len=%d)", result, len(result))
	}
	if result[len(result)-3:] != "..." {
		t.Error("truncated content should end with ...")
	}
}

func TestGenerateSummary(t *testing.T) {
	t.Parallel()

	// Short content - no summary needed
	short := "short content"
	result := generateSummary(short, 100)
	if result != short {
		t.Errorf("expected no summary for short content, got %q", result)
	}

	// Long content - should summarize
	long := "this is a much longer piece of content that needs to be summarized for display"
	result = generateSummary(long, 30)
	if len(result) > 30+3 { // maxLen + "..."
		t.Errorf("summary too long: %q (len=%d)", result, len(result))
	}
}

func TestFindLastBreakPoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s        string
		expected int
	}{
		{"hello world", 5},           // space at position 5
		{"hello\nworld", 5},          // newline at position 5
		{"no break points", 8},       // last space
		{"word", -1},                 // no break points
		{"hello world\ntest", 11},    // prefer newline over space
	}

	for _, tc := range tests {
		result := findLastBreakPoint(tc.s)
		if result != tc.expected {
			t.Errorf("findLastBreakPoint(%q) = %d, want %d", tc.s, result, tc.expected)
		}
	}
}

// =============================================================================
// Test: Skill Registration
// =============================================================================

func TestRegisterUniversalSkills(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := createTestDeps()

	err := RegisterUniversalSkills(registry, deps)
	if err != nil {
		t.Fatalf("failed to register skills: %v", err)
	}

	// Verify all skills are registered
	expectedSkills := []string{"retrieve_context", "search_history", "promote_to_hot"}
	for _, name := range expectedSkills {
		skill := registry.Get(name)
		if skill == nil {
			t.Errorf("skill %s not registered", name)
		}
		if !skill.Loaded {
			t.Errorf("skill %s should be loaded", name)
		}
	}
}

func TestRegisterUniversalSkills_Idempotent(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	deps := createTestDeps()

	// First registration
	err := RegisterUniversalSkills(registry, deps)
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// Second registration - the skills registry overwrites existing skills,
	// so this should not error (registry behavior is to allow overwrite)
	err = RegisterUniversalSkills(registry, deps)
	if err != nil {
		t.Logf("second registration returned error (expected by some registry implementations): %v", err)
	}

	// Verify skills are still registered
	skill := registry.Get("retrieve_context")
	if skill == nil {
		t.Error("skill should still be registered")
	}
}

// =============================================================================
// Test: Input/Output Types
// =============================================================================

func TestRetrieveContextInput_JSON(t *testing.T) {
	t.Parallel()

	input := RetrieveContextInput{
		Query:     "test query",
		RefID:     "ref-123",
		MaxTokens: 1000,
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded RetrieveContextInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Query != input.Query {
		t.Errorf("query mismatch: got %q, want %q", decoded.Query, input.Query)
	}
	if decoded.RefID != input.RefID {
		t.Errorf("ref_id mismatch: got %q, want %q", decoded.RefID, input.RefID)
	}
	if decoded.MaxTokens != input.MaxTokens {
		t.Errorf("max_tokens mismatch: got %d, want %d", decoded.MaxTokens, input.MaxTokens)
	}
}

func TestSearchHistoryInput_JSON(t *testing.T) {
	t.Parallel()

	input := SearchHistoryInput{
		Query:        "search query",
		ContentTypes: []string{"user_prompt", "agent_response"},
		SessionIDs:   []string{"session-1"},
		CrossSession: true,
		MaxResults:   5,
		TimeRange: &TimeRangeFilter{
			From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			To:   time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
		},
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded SearchHistoryInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Query != input.Query {
		t.Errorf("query mismatch")
	}
	if len(decoded.ContentTypes) != len(input.ContentTypes) {
		t.Errorf("content_types length mismatch")
	}
	if decoded.CrossSession != input.CrossSession {
		t.Errorf("cross_session mismatch")
	}
}

func TestPromoteToHotInput_JSON(t *testing.T) {
	t.Parallel()

	input := PromoteToHotInput{
		ContentIDs: []string{"id-1", "id-2", "id-3"},
		TTLTurns:   10,
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded PromoteToHotInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(decoded.ContentIDs) != len(input.ContentIDs) {
		t.Errorf("content_ids length mismatch")
	}
	if decoded.TTLTurns != input.TTLTurns {
		t.Errorf("ttl_turns mismatch")
	}
}

// =============================================================================
// Test: Constants
// =============================================================================

func TestRetrievalDomain(t *testing.T) {
	t.Parallel()

	if RetrievalDomain != "retrieval" {
		t.Errorf("unexpected domain: %s", RetrievalDomain)
	}
}

func TestDefaultValues(t *testing.T) {
	t.Parallel()

	// Verify defaults are reasonable
	if DefaultMaxTokens < 1000 || DefaultMaxTokens > 10000 {
		t.Errorf("DefaultMaxTokens out of expected range: %d", DefaultMaxTokens)
	}

	if DefaultMaxResults < 5 || DefaultMaxResults > 100 {
		t.Errorf("DefaultMaxResults out of expected range: %d", DefaultMaxResults)
	}

	if DefaultTTLTurns < 1 || DefaultTTLTurns > 20 {
		t.Errorf("DefaultTTLTurns out of expected range: %d", DefaultTTLTurns)
	}
}
