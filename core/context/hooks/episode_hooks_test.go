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

func createTestEpisodeTracker(t *testing.T) *ctxpkg.EpisodeTracker {
	t.Helper()
	return ctxpkg.NewEpisodeTracker()
}

func createEpisodeTestPromptData(agentType string) *ctxpkg.PromptHookData {
	return &ctxpkg.PromptHookData{
		SessionID:  "test-session",
		AgentID:    "test-agent",
		AgentType:  agentType,
		TurnNumber: 1,
		Query:      "test query",
		Timestamp:  time.Now(),
	}
}

func createTestToolCallData(toolName string) *ctxpkg.ToolCallHookData {
	return &ctxpkg.ToolCallHookData{
		SessionID:  "test-session",
		AgentID:    "test-agent",
		AgentType:  "librarian",
		TurnNumber: 1,
		ToolName:   toolName,
		ToolInput:  map[string]any{"query": "test search"},
		Duration:   10 * time.Millisecond,
		Timestamp:  time.Now(),
	}
}

// MockObservationLogger implements ObservationLogger for testing.
type MockObservationLogger struct {
	mu           sync.Mutex
	observations []ctxpkg.EpisodeObservation
	logErr       error
}

func (m *MockObservationLogger) Log(obs ctxpkg.EpisodeObservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.logErr != nil {
		return m.logErr
	}
	m.observations = append(m.observations, obs)
	return nil
}

func (m *MockObservationLogger) GetObservations() []ctxpkg.EpisodeObservation {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.observations
}

func (m *MockObservationLogger) SetError(err error) {
	m.mu.Lock()
	m.logErr = err
	m.mu.Unlock()
}

// =============================================================================
// Episode Tracker Init Hook Tests
// =============================================================================

func TestNewEpisodeTrackerInitHook(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{
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

func TestEpisodeTrackerInitHook_Name(t *testing.T) {
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{})

	if hook.Name() != EpisodeInitHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), EpisodeInitHookName)
	}
}

func TestEpisodeTrackerInitHook_Phase(t *testing.T) {
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("Phase() = %v, want HookPhasePrePrompt", hook.Phase())
	}
}

func TestEpisodeTrackerInitHook_Priority(t *testing.T) {
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityEarly) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityEarly)
	}
}

func TestEpisodeTrackerInitHook_Execute_StartsEpisode(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{
		Tracker: tracker,
	})

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	if !tracker.HasActiveEpisode() {
		t.Error("Episode should be active after Execute")
	}
}

func TestEpisodeTrackerInitHook_Execute_RecordsPrefetchedIDs(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{
		Tracker: tracker,
	})

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")
	data.PrefetchedContent = &ctxpkg.AugmentedQuery{
		Excerpts: []ctxpkg.Excerpt{
			{Source: "source1", Content: "content1"},
			{Source: "source2", Content: "content2"},
		},
	}

	_, err := hook.Execute(ctx, data)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if tracker.GetPrefetchedCount() != 2 {
		t.Errorf("PrefetchedCount = %d, want 2", tracker.GetPrefetchedCount())
	}
}

func TestEpisodeTrackerInitHook_Execute_SkipsWhenDisabled(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{
		Tracker: tracker,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}

	if tracker.HasActiveEpisode() {
		t.Error("Episode should not be started when disabled")
	}
}

func TestEpisodeTrackerInitHook_Execute_SkipsNilTracker(t *testing.T) {
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{
		Tracker: nil,
	})

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data with nil tracker")
	}
}

func TestEpisodeTrackerInitHook_ToPromptHook(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	hook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{
		Tracker: tracker,
	})

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != EpisodeInitHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), EpisodeInitHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePrePrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePrePrompt", promptHook.Phase())
	}

	if promptHook.Priority() != int(ctxpkg.HookPriorityEarly) {
		t.Errorf("PromptHook Priority = %d, want %d", promptHook.Priority(), ctxpkg.HookPriorityEarly)
	}
}

// =============================================================================
// Episode Observation Hook Tests
// =============================================================================

func TestNewEpisodeObservationHook(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	logger := &MockObservationLogger{}

	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
		Tracker:        tracker,
		ObservationLog: logger,
	})

	if hook == nil {
		t.Fatal("Expected non-nil hook")
	}

	if hook.tracker != tracker {
		t.Error("Tracker not set correctly")
	}

	if hook.observationLog != logger {
		t.Error("ObservationLog not set correctly")
	}
}

func TestEpisodeObservationHook_Name(t *testing.T) {
	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{})

	if hook.Name() != EpisodeObservationHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), EpisodeObservationHookName)
	}
}

func TestEpisodeObservationHook_Phase(t *testing.T) {
	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePostPrompt {
		t.Errorf("Phase() = %v, want HookPhasePostPrompt", hook.Phase())
	}
}

func TestEpisodeObservationHook_Priority(t *testing.T) {
	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityLate) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityLate)
	}
}

func TestEpisodeObservationHook_Execute_FinalizesEpisode(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	logger := &MockObservationLogger{}

	// Start an episode first
	tracker.StartEpisode("agent1", "librarian", 1)

	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
		Tracker:        tracker,
		ObservationLog: logger,
	})

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")
	data.Response = "Task completed successfully."

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	if tracker.HasActiveEpisode() {
		t.Error("Episode should be finalized after Execute")
	}

	// Check observation was logged
	observations := logger.GetObservations()
	if len(observations) != 1 {
		t.Errorf("Logged observations = %d, want 1", len(observations))
	}

	if !observations[0].TaskCompleted {
		t.Error("TaskCompleted should be true for 'completed successfully' response")
	}
}

func TestEpisodeObservationHook_Execute_ExtractsUsedIDs(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	logger := &MockObservationLogger{}

	// Start an episode first
	tracker.StartEpisode("agent1", "librarian", 1)

	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
		Tracker:        tracker,
		ObservationLog: logger,
	})

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")
	data.Response = `Based on the retrieved content:
### [EXCERPT 1] source1.go (confidence: 0.95)
Some content here
### [EXCERPT 2] source2.go (confidence: 0.85)
More content`

	_, err := hook.Execute(ctx, data)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	observations := logger.GetObservations()
	if len(observations) != 1 {
		t.Fatalf("Logged observations = %d, want 1", len(observations))
	}

	// Check that used IDs were extracted
	if len(observations[0].UsedIDs) < 2 {
		t.Errorf("UsedIDs = %d, want >= 2", len(observations[0].UsedIDs))
	}
}

func TestEpisodeObservationHook_Execute_SkipsWithoutActiveEpisode(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	logger := &MockObservationLogger{}

	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
		Tracker:        tracker,
		ObservationLog: logger,
	})

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")
	data.Response = "Some response"

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	// No observation should be logged
	if len(logger.GetObservations()) != 0 {
		t.Error("Should not log observation without active episode")
	}
}

func TestEpisodeObservationHook_Execute_SkipsWhenDisabled(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	logger := &MockObservationLogger{}

	tracker.StartEpisode("agent1", "librarian", 1)

	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
		Tracker:        tracker,
		ObservationLog: logger,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")
	data.Response = "Some response"

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}

	// Episode should still be active
	if !tracker.HasActiveEpisode() {
		t.Error("Episode should not be finalized when hook is disabled")
	}
}

func TestEpisodeObservationHook_Execute_HandlesNilLogger(t *testing.T) {
	tracker := createTestEpisodeTracker(t)

	tracker.StartEpisode("agent1", "librarian", 1)

	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
		Tracker:        tracker,
		ObservationLog: nil, // No logger
	})

	ctx := context.Background()
	data := createEpisodeTestPromptData("librarian")
	data.Response = "Task completed"

	// Should not panic with nil logger
	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	if tracker.HasActiveEpisode() {
		t.Error("Episode should be finalized even without logger")
	}
}

func TestEpisodeObservationHook_ToPromptHook(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	hook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
		Tracker: tracker,
	})

	promptHook := hook.ToPromptHook()

	if promptHook.Name() != EpisodeObservationHookName {
		t.Errorf("PromptHook Name = %s, want %s", promptHook.Name(), EpisodeObservationHookName)
	}

	if promptHook.Phase() != ctxpkg.HookPhasePostPrompt {
		t.Errorf("PromptHook Phase = %v, want HookPhasePostPrompt", promptHook.Phase())
	}
}

// =============================================================================
// Search Tool Observation Hook Tests
// =============================================================================

func TestNewSearchToolObservationHook(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{
		Tracker: tracker,
	})

	if hook == nil {
		t.Fatal("Expected non-nil hook")
	}

	if hook.tracker != tracker {
		t.Error("Tracker not set correctly")
	}
}

func TestSearchToolObservationHook_Name(t *testing.T) {
	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{})

	if hook.Name() != SearchToolObservationHookName {
		t.Errorf("Name() = %s, want %s", hook.Name(), SearchToolObservationHookName)
	}
}

func TestSearchToolObservationHook_Phase(t *testing.T) {
	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{})

	if hook.Phase() != ctxpkg.HookPhasePostTool {
		t.Errorf("Phase() = %v, want HookPhasePostTool", hook.Phase())
	}
}

func TestSearchToolObservationHook_Priority(t *testing.T) {
	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{})

	if hook.Priority() != int(ctxpkg.HookPriorityNormal) {
		t.Errorf("Priority() = %d, want %d", hook.Priority(), ctxpkg.HookPriorityNormal)
	}
}

func TestSearchToolObservationHook_Execute_RecordsToolCall(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	tracker.StartEpisode("agent1", "librarian", 1)

	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{
		Tracker: tracker,
	})

	ctx := context.Background()
	data := createTestToolCallData("read_file")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	if tracker.GetToolCallCount() != 1 {
		t.Errorf("ToolCallCount = %d, want 1", tracker.GetToolCallCount())
	}
}

func TestSearchToolObservationHook_Execute_RecordsSearchAfterPrefetch(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	tracker.StartEpisode("agent1", "librarian", 1)

	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{
		Tracker: tracker,
	})

	ctx := context.Background()
	data := createTestToolCallData("search_code") // This is a search tool

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}

	if tracker.GetSearchCount() != 1 {
		t.Errorf("SearchCount = %d, want 1", tracker.GetSearchCount())
	}
}

func TestSearchToolObservationHook_Execute_SkipsWithoutActiveEpisode(t *testing.T) {
	tracker := createTestEpisodeTracker(t)

	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{
		Tracker: tracker,
	})

	ctx := context.Background()
	data := createTestToolCallData("search_code")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data")
	}
}

func TestSearchToolObservationHook_Execute_SkipsWhenDisabled(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	tracker.StartEpisode("agent1", "librarian", 1)

	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{
		Tracker: tracker,
	})
	hook.SetEnabled(false)

	ctx := context.Background()
	data := createTestToolCallData("search_code")

	result, err := hook.Execute(ctx, data)

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	if result.Modified {
		t.Error("Should not modify data when disabled")
	}

	if tracker.GetToolCallCount() != 0 {
		t.Error("Tool call should not be recorded when disabled")
	}
}

func TestSearchToolObservationHook_ToToolCallHook(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	hook := NewSearchToolObservationHook(SearchToolObservationHookConfig{
		Tracker: tracker,
	})

	toolHook := hook.ToToolCallHook()

	if toolHook.Name() != SearchToolObservationHookName {
		t.Errorf("ToolCallHook Name = %s, want %s", toolHook.Name(), SearchToolObservationHookName)
	}

	if toolHook.Phase() != ctxpkg.HookPhasePostTool {
		t.Errorf("ToolCallHook Phase = %v, want HookPhasePostTool", toolHook.Phase())
	}
}

// =============================================================================
// Helper Function Tests
// =============================================================================

func TestExtractExcerptIDs(t *testing.T) {
	tests := []struct {
		name     string
		aq       *ctxpkg.AugmentedQuery
		expected int
	}{
		{
			name:     "nil AugmentedQuery",
			aq:       nil,
			expected: 0,
		},
		{
			name: "empty AugmentedQuery",
			aq: &ctxpkg.AugmentedQuery{
				Excerpts:  []ctxpkg.Excerpt{},
				Summaries: []ctxpkg.Summary{},
			},
			expected: 0,
		},
		{
			name: "with excerpts",
			aq: &ctxpkg.AugmentedQuery{
				Excerpts: []ctxpkg.Excerpt{
					{Source: "source1"},
					{Source: "source2"},
				},
			},
			expected: 2,
		},
		{
			name: "with summaries",
			aq: &ctxpkg.AugmentedQuery{
				Summaries: []ctxpkg.Summary{
					{Source: "summary1"},
				},
			},
			expected: 1,
		},
		{
			name: "mixed excerpts and summaries",
			aq: &ctxpkg.AugmentedQuery{
				Excerpts: []ctxpkg.Excerpt{
					{Source: "source1"},
				},
				Summaries: []ctxpkg.Summary{
					{Source: "summary1"},
				},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := extractExcerptIDs(tt.aq)
			if len(ids) != tt.expected {
				t.Errorf("extractExcerptIDs() returned %d IDs, want %d", len(ids), tt.expected)
			}
		})
	}
}

func TestIsSearchTool(t *testing.T) {
	tests := []struct {
		toolName string
		expected bool
	}{
		{"search", true},
		{"search_code", true},
		{"search_files", true},
		{"semantic_search", true},
		{"grep", true},
		{"bleve_search", true},
		{"SEARCH", true},        // case insensitive
		{"custom_search", true}, // contains "search"
		{"read_file", false},
		{"write_file", false},
		{"execute", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			result := isSearchTool(tt.toolName)
			if result != tt.expected {
				t.Errorf("isSearchTool(%q) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestExtractQueryFromToolInput(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected string
	}{
		{
			name:     "nil input",
			input:    nil,
			expected: "",
		},
		{
			name:     "empty input",
			input:    map[string]any{},
			expected: "",
		},
		{
			name:     "query key",
			input:    map[string]any{"query": "test query"},
			expected: "test query",
		},
		{
			name:     "q key",
			input:    map[string]any{"q": "short query"},
			expected: "short query",
		},
		{
			name:     "search key",
			input:    map[string]any{"search": "search term"},
			expected: "search term",
		},
		{
			name:     "pattern key",
			input:    map[string]any{"pattern": "regex pattern"},
			expected: "regex pattern",
		},
		{
			name:     "non-string value",
			input:    map[string]any{"query": 123},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractQueryFromToolInput(tt.input)
			if result != tt.expected {
				t.Errorf("extractQueryFromToolInput() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestEpisodeHooks_FullLifecycle(t *testing.T) {
	tracker := createTestEpisodeTracker(t)
	logger := &MockObservationLogger{}

	initHook := NewEpisodeTrackerInitHook(EpisodeTrackerInitHookConfig{
		Tracker: tracker,
	})

	searchHook := NewSearchToolObservationHook(SearchToolObservationHookConfig{
		Tracker: tracker,
	})

	observationHook := NewEpisodeObservationHook(EpisodeObservationHookConfig{
		Tracker:        tracker,
		ObservationLog: logger,
	})

	ctx := context.Background()

	// 1. Initialize episode
	prePromptData := createEpisodeTestPromptData("librarian")
	prePromptData.PrefetchedContent = &ctxpkg.AugmentedQuery{
		Excerpts: []ctxpkg.Excerpt{{Source: "prefetch1"}},
	}

	_, err := initHook.Execute(ctx, prePromptData)
	if err != nil {
		t.Fatalf("Init hook error: %v", err)
	}

	if !tracker.HasActiveEpisode() {
		t.Fatal("Episode should be active after init")
	}

	// 2. Record tool calls
	toolData := createTestToolCallData("search_code")
	_, err = searchHook.Execute(ctx, toolData)
	if err != nil {
		t.Fatalf("Search hook error: %v", err)
	}

	// 3. Finalize episode
	postPromptData := createEpisodeTestPromptData("librarian")
	postPromptData.Response = "Found the code. Task completed successfully."

	_, err = observationHook.Execute(ctx, postPromptData)
	if err != nil {
		t.Fatalf("Observation hook error: %v", err)
	}

	if tracker.HasActiveEpisode() {
		t.Error("Episode should be finalized")
	}

	// 4. Verify observation
	observations := logger.GetObservations()
	if len(observations) != 1 {
		t.Fatalf("Expected 1 observation, got %d", len(observations))
	}

	obs := observations[0]
	if len(obs.PrefetchedIDs) != 1 {
		t.Errorf("PrefetchedIDs = %d, want 1", len(obs.PrefetchedIDs))
	}

	if obs.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", obs.ToolCallCount)
	}

	if len(obs.SearchedAfter) != 1 {
		t.Errorf("SearchedAfter = %d, want 1", len(obs.SearchedAfter))
	}

	if !obs.TaskCompleted {
		t.Error("TaskCompleted should be true")
	}
}
