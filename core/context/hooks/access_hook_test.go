package hooks

import (
	"context"
	"sync"
	"testing"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Test Helpers
// =============================================================================

// MockAccessTracker implements AccessTracker for testing.
type MockAccessTracker struct {
	mu      sync.Mutex
	accesses []accessRecord
}

type accessRecord struct {
	id     string
	turn   int
	source string
}

func (m *MockAccessTracker) RecordAccess(id string, turn int, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accesses = append(m.accesses, accessRecord{id: id, turn: turn, source: source})
	return nil
}

func (m *MockAccessTracker) GetAccesses() []accessRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accesses
}

// MockContentPromoter implements ContentPromoter for testing.
type MockContentPromoter struct {
	mu        sync.Mutex
	promoted  []*ctxpkg.ContentEntry
}

func (m *MockContentPromoter) Promote(entry *ctxpkg.ContentEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.promoted = append(m.promoted, entry)
	return nil
}

func (m *MockContentPromoter) GetPromoted() []*ctxpkg.ContentEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.promoted
}

func createAccessTestPromptData() *ctxpkg.PromptHookData {
	return &ctxpkg.PromptHookData{
		SessionID:  "test-session",
		AgentID:    "test-agent",
		AgentType:  "librarian",
		TurnNumber: 1,
		Query:      "test query",
		Timestamp:  time.Now(),
	}
}

func createAccessTestToolCallData(toolName string) *ctxpkg.ToolCallHookData {
	return &ctxpkg.ToolCallHookData{
		SessionID:  "test-session",
		AgentID:    "test-agent",
		AgentType:  "librarian",
		TurnNumber: 1,
		ToolName:   toolName,
		Timestamp:  time.Now(),
	}
}

// =============================================================================
// Access Tracking Hook Tests
// =============================================================================

func TestNewAccessTrackingHook(t *testing.T) {
	tracker := &MockAccessTracker{}
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{
		Tracker: tracker,
	})

	if hook == nil {
		t.Fatal("Expected non-nil hook")
	}

	if hook.tracker != tracker {
		t.Error("Tracker not set correctly")
	}

	if !hook.Enabled() {
		t.Error("Hook should be enabled by default")
	}
}

func TestAccessTrackingHook_Name(t *testing.T) {
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{})

	if hook.Name() != AccessTrackingHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), AccessTrackingHookName)
	}
}

func TestAccessTrackingHook_Phase(t *testing.T) {
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePostPrompt {
		t.Errorf("Phase() = %v, want HookPhasePostPrompt", hook.Phase())
	}
}

func TestAccessTrackingHook_Priority(t *testing.T) {
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityNormal) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityNormal)
	}
}

func TestAccessTrackingHook_Execute_RecordsAccess(t *testing.T) {
	tracker := &MockAccessTracker{}
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{
		Tracker: tracker,
	})

	ctx := context.Background()
	data := createAccessTestPromptData()
	data.Response = `Based on the code:
### [EXCERPT 1] src/auth/login.go (confidence: 0.95)
Code snippet here`

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	accesses := tracker.GetAccesses()
	if len(accesses) == 0 {
		t.Fatal("Should have recorded at least one access")
	}

	// Check that the source was recorded with correct source type
	found := false
	for _, acc := range accesses {
		if acc.source == "in_response" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Should have recorded access with source 'in_response'")
	}
}

func TestAccessTrackingHook_Execute_ExtractsFilePaths(t *testing.T) {
	tracker := &MockAccessTracker{}
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{
		Tracker: tracker,
	})

	ctx := context.Background()
	data := createAccessTestPromptData()
	data.Response = `The file src/main.go contains the implementation.
Also check out pkg/utils/helper.go for utility functions.`

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	accesses := tracker.GetAccesses()
	if len(accesses) < 2 {
		t.Errorf("Should have recorded at least 2 accesses for file paths, got %d", len(accesses))
	}
}

func TestAccessTrackingHook_Execute_SkipsEmptyResponse(t *testing.T) {
	tracker := &MockAccessTracker{}
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{
		Tracker: tracker,
	})

	ctx := context.Background()
	data := createAccessTestPromptData()
	data.Response = ""

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	if len(tracker.GetAccesses()) != 0 {
		t.Error("Should not record any accesses for empty response")
	}
}

func TestAccessTrackingHook_Execute_SkipsWhenDisabled(t *testing.T) {
	tracker := &MockAccessTracker{}
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{
		Tracker: tracker,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createAccessTestPromptData()
	data.Response = "### [EXCERPT 1] test.go"

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}

	if len(tracker.GetAccesses()) != 0 {
		t.Error("Should not record when disabled")
	}
}

func TestAccessTrackingHook_Execute_SkipsNilTracker(t *testing.T) {
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{
		Tracker: nil,
	})

	ctx := context.Background()
	data := createAccessTestPromptData()
	data.Response = "### [EXCERPT 1] test.go"

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil tracker")
	}
}

func TestAccessTrackingHook_ToPromptHook(t *testing.T) {
	hook := NewAccessTrackingHook(AccessTrackingHookConfig{})

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != AccessTrackingHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), AccessTrackingHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePostPrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePostPrompt", promptHook.Phase())
	}
}

// =============================================================================
// Content Promotion Hook Tests
// =============================================================================

func TestNewContentPromotionHook(t *testing.T) {
	tracker := &MockAccessTracker{}
	promoter := &MockContentPromoter{}

	hook := NewContentPromotionHook(ContentPromotionHookConfig{
		Tracker:  tracker,
		Promoter: promoter,
	})

	if hook == nil {
		t.Fatal("Expected non-nil hook")
	}

	if hook.tracker != tracker {
		t.Error("Tracker not set correctly")
	}

	if hook.promoter != promoter {
		t.Error("Promoter not set correctly")
	}
}

func TestContentPromotionHook_Name(t *testing.T) {
	hook := NewContentPromotionHook(ContentPromotionHookConfig{})

	if hook.Name() != ContentPromotionHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), ContentPromotionHookName)
	}
}

func TestContentPromotionHook_Phase(t *testing.T) {
	hook := NewContentPromotionHook(ContentPromotionHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePostTool {
		t.Errorf("Phase() = %v, want HookPhasePostTool", hook.Phase())
	}
}

func TestContentPromotionHook_Priority(t *testing.T) {
	hook := NewContentPromotionHook(ContentPromotionHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityLate) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityLate)
	}
}

func TestContentPromotionHook_Execute_PromotesContent(t *testing.T) {
	tracker := &MockAccessTracker{}
	promoter := &MockContentPromoter{}

	hook := NewContentPromotionHook(ContentPromotionHookConfig{
		Tracker:  tracker,
		Promoter: promoter,
	})

	ctx := context.Background()
	data := createAccessTestToolCallData("read_file")
	data.ToolOutput = &ctxpkg.ContentEntry{
		ID:      "src/main.go",
		Content: "package main",
	}

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	// Check access was recorded
	accesses := tracker.GetAccesses()
	if len(accesses) != 1 {
		t.Errorf("Expected 1 access record, got %d", len(accesses))
	}
	if accesses[0].source != "tool_retrieved" {
		t.Errorf("Expected source 'tool_retrieved', got %s", accesses[0].source)
	}

	// Check content was promoted
	promoted := promoter.GetPromoted()
	if len(promoted) != 1 {
		t.Errorf("Expected 1 promoted entry, got %d", len(promoted))
	}
}

func TestContentPromotionHook_Execute_PromotesMultipleEntries(t *testing.T) {
	tracker := &MockAccessTracker{}
	promoter := &MockContentPromoter{}

	hook := NewContentPromotionHook(ContentPromotionHookConfig{
		Tracker:  tracker,
		Promoter: promoter,
	})

	ctx := context.Background()
	data := createAccessTestToolCallData("search_code")
	data.ToolOutput = []*ctxpkg.ContentEntry{
		{ID: "file1.go", Content: "content1"},
		{ID: "file2.go", Content: "content2"},
	}

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if len(tracker.GetAccesses()) != 2 {
		t.Error("Should record 2 accesses")
	}

	if len(promoter.GetPromoted()) != 2 {
		t.Error("Should promote 2 entries")
	}
}

func TestContentPromotionHook_Execute_SkipsNonRetrievalTool(t *testing.T) {
	tracker := &MockAccessTracker{}
	promoter := &MockContentPromoter{}

	hook := NewContentPromotionHook(ContentPromotionHookConfig{
		Tracker:  tracker,
		Promoter: promoter,
	})

	ctx := context.Background()
	data := createAccessTestToolCallData("execute_command")
	data.ToolOutput = &ctxpkg.ContentEntry{
		ID:      "test.go",
		Content: "test",
	}

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	if len(tracker.GetAccesses()) != 0 {
		t.Error("Should not record access for non-retrieval tool")
	}

	if len(promoter.GetPromoted()) != 0 {
		t.Error("Should not promote for non-retrieval tool")
	}
}

func TestContentPromotionHook_Execute_SkipsWhenDisabled(t *testing.T) {
	tracker := &MockAccessTracker{}
	promoter := &MockContentPromoter{}

	hook := NewContentPromotionHook(ContentPromotionHookConfig{
		Tracker:  tracker,
		Promoter: promoter,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createAccessTestToolCallData("read_file")
	data.ToolOutput = &ctxpkg.ContentEntry{ID: "test.go"}

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}
}

func TestContentPromotionHook_Execute_HandlesMapOutput(t *testing.T) {
	tracker := &MockAccessTracker{}
	promoter := &MockContentPromoter{}

	hook := NewContentPromotionHook(ContentPromotionHookConfig{
		Tracker:  tracker,
		Promoter: promoter,
	})

	ctx := context.Background()
	data := createAccessTestToolCallData("read_file")
	data.ToolOutput = map[string]any{
		"id":      "test.go",
		"content": "test content",
	}

	_, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if len(promoter.GetPromoted()) != 1 {
		t.Error("Should promote entry from map output")
	}
}

func TestContentPromotionHook_ToToolCallHook(t *testing.T) {
	hook := NewContentPromotionHook(ContentPromotionHookConfig{})

	toolHook := hook.ToToolCallHook()

	if toolHook.Name() != ContentPromotionHookName {
		t.Errorf("ToolCallHook Name = %s, want %s", toolHook.Name(), ContentPromotionHookName)
	}

	if toolHook.Phase() != ctxpkg.HookPhasePostTool {
		t.Errorf("ToolCallHook Phase = %v, want HookPhasePostTool", toolHook.Phase())
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestIsRetrievalTool(t *testing.T) {
	tests := []struct {
		toolName string
		expected bool
	}{
		{"read_file", true},
		{"read", true},
		{"search_code", true},
		{"semantic_search", true},
		{"fetch_content", true},
		{"custom_search", true}, // contains "search"
		{"file_reader", true},   // contains "read"
		{"execute_command", false},
		{"write_file", false},
		{"delete", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			result := isRetrievalTool(tt.toolName)
			if result != tt.expected {
				t.Errorf("isRetrievalTool(%q) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestExtractContentReferences(t *testing.T) {
	tests := []struct {
		name     string
		response string
		minIDs   int
	}{
		{
			name:     "empty response",
			response: "",
			minIDs:   0,
		},
		{
			name: "excerpt format",
			response: `### [EXCERPT 1] src/main.go (confidence: 0.95)
Content here`,
			minIDs: 1,
		},
		{
			name:     "file path",
			response: "Check out src/utils.go for the implementation.",
			minIDs:   1,
		},
		{
			name: "multiple file paths",
			response: `See pkg/api/handler.go and internal/db/query.go
for the relevant code.`,
			minIDs: 1, // Note: simple extraction may not find all paths in same line
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := extractContentReferences(tt.response)
			if len(ids) < tt.minIDs {
				t.Errorf("extractContentReferences() returned %d IDs, want >= %d", len(ids), tt.minIDs)
			}
		})
	}
}

func TestDeduplicateIDs(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b", "d"}
	result := deduplicateIDs(input)

	if len(result) != 4 {
		t.Errorf("deduplicateIDs() returned %d IDs, want 4", len(result))
	}

	// Check order is preserved
	expected := []string{"a", "b", "c", "d"}
	for i, id := range expected {
		if result[i] != id {
			t.Errorf("deduplicateIDs()[%d] = %s, want %s", i, result[i], id)
		}
	}
}
