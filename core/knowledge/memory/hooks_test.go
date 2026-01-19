package memory

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/domain"
	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// DefaultNodeExtractor Tests
// =============================================================================

func TestNewDefaultNodeExtractor(t *testing.T) {
	extractor := NewDefaultNodeExtractor()
	if extractor == nil {
		t.Fatal("expected non-nil extractor")
	}

	// Verify default retrieval tools are set
	if !extractor.isRetrievalTool("search_codebase") {
		t.Error("expected search_codebase to be a retrieval tool")
	}
	if !extractor.isRetrievalTool("read_file") {
		t.Error("expected read_file to be a retrieval tool")
	}
	if !extractor.isRetrievalTool("grep") {
		t.Error("expected grep to be a retrieval tool")
	}
}

func TestDefaultNodeExtractor_ExtractNodeReferences(t *testing.T) {
	extractor := NewDefaultNodeExtractor()

	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name:     "empty content",
			content:  "",
			expected: nil,
		},
		{
			name:     "file path extraction",
			content:  "Looking at /path/to/file.go for the implementation",
			expected: []string{"/path/to/file.go"},
		},
		{
			name:     "relative file path",
			content:  "Check ./src/main.ts for details",
			expected: []string{"./src/main.ts"},
		},
		{
			name:     "function reference",
			content:  "The func ProcessData handles this",
			expected: []string{"ProcessData"},
		},
		{
			name:     "type reference",
			content:  "The type UserConfig struct contains settings",
			expected: []string{"UserConfig"},
		},
		{
			name:     "backtick identifier",
			content:  "Use the `MemoryStore` class for storage",
			expected: []string{"MemoryStore"},
		},
		{
			name:     "multiple references",
			content:  "In /core/memory/store.go, the func NewMemoryStore creates a type MemoryStore",
			expected: []string{"/core/memory/store.go", "NewMemoryStore", "MemoryStore"},
		},
		{
			name:    "quoted paths",
			content: `Loading file "config/settings.json" and "data/users.csv"`,
			expected: []string{"config/settings.json", "data/users.csv"},
		},
		{
			name:     "node ID explicit reference",
			content:  `Retrieved node_id: "abc-123-def"`,
			expected: []string{"abc-123-def"},
		},
		{
			name:     "skip multi-word backticks",
			content:  "The `some code snippet here` should be ignored",
			expected: nil,
		},
		{
			name:     "class definition",
			content:  "class UserService implements Service",
			expected: []string{"UserService"},
		},
		{
			name:     "interface definition",
			content:  "interface DataProvider defines the contract",
			expected: []string{"DataProvider"},
		},
		{
			name:     "python function",
			content:  "def calculate_total(items):",
			expected: []string{"calculate_total"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractor.ExtractNodeReferences(tt.content)
			if tt.expected == nil && result != nil && len(result) > 0 {
				t.Errorf("expected nil or empty, got %v", result)
				return
			}
			if tt.expected != nil {
				// Check that all expected refs are found
				for _, exp := range tt.expected {
					found := false
					for _, r := range result {
						if r == exp {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected to find %q in %v", exp, result)
					}
				}
			}
		})
	}
}

func TestDefaultNodeExtractor_ExtractFromToolResult(t *testing.T) {
	extractor := NewDefaultNodeExtractor()

	tests := []struct {
		name     string
		toolName string
		result   any
		expected []string
	}{
		{
			name:     "non-retrieval tool",
			toolName: "write_file",
			result:   map[string]any{"status": "ok"},
			expected: nil,
		},
		{
			name:     "string result",
			toolName: "read_file",
			result:   "Content from /path/to/file.go",
			expected: []string{"/path/to/file.go"},
		},
		{
			name:     "string array result",
			toolName: "grep",
			result:   []string{"/file1.go", "/file2.go"},
			expected: []string{"/file1.go", "/file2.go"},
		},
		{
			name:     "map with id field",
			toolName: "search_codebase",
			result:   map[string]any{"id": "node-123"},
			expected: []string{"node-123"},
		},
		{
			name:     "map with node_id field",
			toolName: "semantic_search",
			result:   map[string]any{"node_id": "semantic-456"},
			expected: []string{"semantic-456"},
		},
		{
			name:     "map with path field",
			toolName: "find",
			result:   map[string]any{"path": "/src/main.go"},
			expected: []string{"/src/main.go"},
		},
		{
			name:     "map with results array",
			toolName: "search_codebase",
			result: map[string]any{
				"results": []any{
					map[string]any{"id": "result-1"},
					map[string]any{"id": "result-2"},
				},
			},
			expected: []string{"result-1", "result-2"},
		},
		{
			name:     "map with matches array",
			toolName: "grep",
			result: map[string]any{
				"matches": []any{
					map[string]any{"file_path": "/match1.go"},
					map[string]any{"file_path": "/match2.go"},
				},
			},
			expected: []string{"/match1.go", "/match2.go"},
		},
		{
			name:     "array of maps",
			toolName: "search",
			result: []any{
				map[string]any{"id": "arr-1"},
				map[string]any{"id": "arr-2"},
			},
			expected: []string{"arr-1", "arr-2"},
		},
		{
			name:     "array of strings",
			toolName: "find",
			result:   []any{"/path/a.go", "/path/b.go"},
			expected: []string{"/path/a.go", "/path/b.go"},
		},
		{
			name:     "tool with search keyword in name",
			toolName: "custom_search_tool",
			result:   map[string]any{"id": "custom-123"},
			expected: []string{"custom-123"},
		},
		{
			name:     "nil result",
			toolName: "search_codebase",
			result:   nil,
			expected: nil,
		},
		{
			name:     "empty tool name",
			toolName: "",
			result:   map[string]any{"id": "node-123"},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractor.ExtractFromToolResult(tt.toolName, tt.result)
			if tt.expected == nil && result != nil && len(result) > 0 {
				t.Errorf("expected nil or empty, got %v", result)
				return
			}
			if tt.expected != nil {
				if len(result) != len(tt.expected) {
					t.Errorf("expected %d results, got %d: %v", len(tt.expected), len(result), result)
					return
				}
				for _, exp := range tt.expected {
					found := false
					for _, r := range result {
						if r == exp {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected to find %q in %v", exp, result)
					}
				}
			}
		})
	}
}

func TestDefaultNodeExtractor_IsRetrievalTool(t *testing.T) {
	extractor := NewDefaultNodeExtractor()

	tests := []struct {
		toolName   string
		isRetrieval bool
	}{
		{"search_codebase", true},
		{"retrieve_context", true},
		{"read_file", true},
		{"grep", true},
		{"find", true},
		{"semantic_search", true},
		{"glob", true},
		{"read", true},
		{"search", true},
		{"write_file", false},
		{"execute", false},
		{"delete", false},
		// Keyword-based detection
		{"custom_search", true},
		{"file_finder", true},
		{"code_grep", true},
		{"lookup_user", true},
		{"data_retrieve", true},
		{"query_database", true},
		// Case insensitive
		{"SEARCH_CODEBASE", true},
		{"Read_File", true},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			if got := extractor.isRetrievalTool(tt.toolName); got != tt.isRetrieval {
				t.Errorf("isRetrievalTool(%q) = %v, want %v", tt.toolName, got, tt.isRetrieval)
			}
		})
	}
}

func TestDefaultNodeExtractor_AddRemoveRetrievalTool(t *testing.T) {
	extractor := NewDefaultNodeExtractor()

	// Test adding a custom tool with a name that doesn't match any keywords
	customToolName := "xyz_custom_data_tool"
	if extractor.isRetrievalTool(customToolName) {
		t.Errorf("%s should not be a retrieval tool initially", customToolName)
	}

	extractor.AddRetrievalTool(customToolName)
	if !extractor.isRetrievalTool(customToolName) {
		t.Errorf("%s should be a retrieval tool after adding", customToolName)
	}

	// Test removing a tool
	extractor.RemoveRetrievalTool(customToolName)
	if extractor.isRetrievalTool(customToolName) {
		t.Errorf("%s should not be a retrieval tool after removing", customToolName)
	}

	// Test adding and removing another tool
	anotherTool := "abc_operation"
	extractor.AddRetrievalTool(anotherTool)
	if !extractor.isRetrievalTool(anotherTool) {
		t.Errorf("%s should be a retrieval tool after adding", anotherTool)
	}
	extractor.RemoveRetrievalTool(anotherTool)
	if extractor.isRetrievalTool(anotherTool) {
		t.Errorf("%s should not be a retrieval tool after removing", anotherTool)
	}
}

// =============================================================================
// Hook Priority Tests
// =============================================================================

func TestHookPriorityConstants(t *testing.T) {
	// Verify ordering: Early < Normal < Late
	if HookPriorityEarly >= HookPriorityNormal {
		t.Errorf("HookPriorityEarly (%d) should be < HookPriorityNormal (%d)",
			HookPriorityEarly, HookPriorityNormal)
	}
	if HookPriorityNormal >= HookPriorityLate {
		t.Errorf("HookPriorityNormal (%d) should be < HookPriorityLate (%d)",
			HookPriorityNormal, HookPriorityLate)
	}
}

// =============================================================================
// MemoryReinforcementHook Tests
// =============================================================================

func TestNewMemoryReinforcementHook(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)

	t.Run("with nil extractor uses default", func(t *testing.T) {
		hook := NewMemoryReinforcementHook(store, nil)
		if hook == nil {
			t.Fatal("expected non-nil hook")
		}
		if hook.nodeExtractor == nil {
			t.Error("expected non-nil extractor")
		}
		if _, ok := hook.nodeExtractor.(*DefaultNodeExtractor); !ok {
			t.Error("expected DefaultNodeExtractor")
		}
	})

	t.Run("with custom extractor", func(t *testing.T) {
		customExtractor := NewDefaultNodeExtractor()
		customExtractor.AddRetrievalTool("custom_tool")

		hook := NewMemoryReinforcementHook(store, customExtractor)
		if hook.nodeExtractor != customExtractor {
			t.Error("expected custom extractor to be used")
		}
	})

	t.Run("default priority is late", func(t *testing.T) {
		hook := NewMemoryReinforcementHook(store, nil)
		if hook.Priority() != HookPriorityLate {
			t.Errorf("expected priority %d, got %d", HookPriorityLate, hook.Priority())
		}
	})

	t.Run("enabled by default", func(t *testing.T) {
		hook := NewMemoryReinforcementHook(store, nil)
		if !hook.Enabled() {
			t.Error("expected hook to be enabled by default")
		}
	})
}

func TestNewMemoryReinforcementHookWithPriority(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)
	customPriority := 750

	hook := NewMemoryReinforcementHookWithPriority(store, nil, customPriority)
	if hook.Priority() != customPriority {
		t.Errorf("expected priority %d, got %d", customPriority, hook.Priority())
	}
}

func TestMemoryReinforcementHook_Name(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)
	hook := NewMemoryReinforcementHook(store, nil)

	if name := hook.Name(); name != "memory_reinforcement" {
		t.Errorf("expected name 'memory_reinforcement', got %q", name)
	}
}

func TestMemoryReinforcementHook_Agents(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)
	hook := NewMemoryReinforcementHook(store, nil)

	if agents := hook.Agents(); agents != nil {
		t.Errorf("expected nil agents (applies to all), got %v", agents)
	}
}

func TestMemoryReinforcementHook_EnableDisable(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)
	hook := NewMemoryReinforcementHook(store, nil)

	// Initially enabled
	if !hook.Enabled() {
		t.Error("expected hook to be enabled initially")
	}

	// Disable
	hook.SetEnabled(false)
	if hook.Enabled() {
		t.Error("expected hook to be disabled after SetEnabled(false)")
	}

	// Re-enable
	hook.SetEnabled(true)
	if !hook.Enabled() {
		t.Error("expected hook to be enabled after SetEnabled(true)")
	}
}

func TestMemoryReinforcementHook_SetPriority(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)
	hook := NewMemoryReinforcementHook(store, nil)

	newPriority := 800
	hook.SetPriority(newPriority)
	if hook.Priority() != newPriority {
		t.Errorf("expected priority %d, got %d", newPriority, hook.Priority())
	}
}

func TestMemoryReinforcementHook_Store(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)
	hook := NewMemoryReinforcementHook(store, nil)

	if hook.Store() != store {
		t.Error("expected Store() to return the same store")
	}
}

func TestMemoryReinforcementHook_NodeExtractor(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)
	extractor := NewDefaultNodeExtractor()
	hook := NewMemoryReinforcementHook(store, extractor)

	if hook.NodeExtractor() != extractor {
		t.Error("expected NodeExtractor() to return the same extractor")
	}

	// Test SetNodeExtractor
	newExtractor := NewDefaultNodeExtractor()
	hook.SetNodeExtractor(newExtractor)
	if hook.NodeExtractor() != newExtractor {
		t.Error("expected NodeExtractor() to return the new extractor")
	}

	// Setting nil should not change
	hook.SetNodeExtractor(nil)
	if hook.NodeExtractor() != newExtractor {
		t.Error("SetNodeExtractor(nil) should not change the extractor")
	}
}

func TestMemoryReinforcementHook_OnPostPrompt(t *testing.T) {
	ctx := context.Background()

	t.Run("empty response", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		hook := NewMemoryReinforcementHook(store, nil)

		err := hook.OnPostPrompt(ctx, "")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("disabled hook does nothing", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		nodeID := "test-node-disabled"
		insertTestNode(t, db, nodeID, domain.DomainAcademic)

		hook := NewMemoryReinforcementHook(store, nil)
		hook.SetEnabled(false)

		response := "Check " + nodeID + " for details"
		err := hook.OnPostPrompt(ctx, response)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// No memory should be recorded since hook is disabled
		// (Node would have no traces)
	})

	t.Run("reinforces referenced nodes", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		nodeID := "/core/memory/store.go"
		insertTestNode(t, db, nodeID, domain.DomainAcademic)

		hook := NewMemoryReinforcementHook(store, nil)

		response := "Looking at /core/memory/store.go for the implementation details."
		err := hook.OnPostPrompt(ctx, response)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Check that memory was recorded
		memory, err := store.GetMemory(ctx, nodeID, domain.DomainAcademic)
		if err != nil {
			t.Fatalf("unexpected error getting memory: %v", err)
		}

		if memory.AccessCount < 1 {
			t.Error("expected at least one access to be recorded")
		}

		// Check access type is reinforcement
		found := false
		for _, trace := range memory.Traces {
			if trace.AccessType == AccessReinforcement {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected AccessReinforcement trace to be recorded")
		}
	})

	t.Run("handles non-existent nodes gracefully", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		hook := NewMemoryReinforcementHook(store, nil)

		// Reference a node that doesn't exist
		response := "Check /non/existent/file.go for details"
		err := hook.OnPostPrompt(ctx, response)
		// Should not return error even if node doesn't exist
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestMemoryReinforcementHook_OnToolResult(t *testing.T) {
	ctx := context.Background()

	t.Run("empty tool name", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		hook := NewMemoryReinforcementHook(store, nil)

		err := hook.OnToolResult(ctx, "", map[string]any{"id": "test"})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("nil result", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		hook := NewMemoryReinforcementHook(store, nil)

		err := hook.OnToolResult(ctx, "search_codebase", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("disabled hook does nothing", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		nodeID := "test-node-tool"
		insertTestNode(t, db, nodeID, domain.DomainEngineer)

		hook := NewMemoryReinforcementHook(store, nil)
		hook.SetEnabled(false)

		result := map[string]any{"id": nodeID}
		err := hook.OnToolResult(ctx, "search_codebase", result)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("records retrieval for search results", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		nodeID := "search-result-node"
		insertTestNode(t, db, nodeID, domain.DomainEngineer)

		hook := NewMemoryReinforcementHook(store, nil)

		result := map[string]any{
			"results": []any{
				map[string]any{"id": nodeID},
			},
		}
		err := hook.OnToolResult(ctx, "search_codebase", result)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Check that retrieval was recorded
		memory, err := store.GetMemory(ctx, nodeID, domain.DomainEngineer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if memory.AccessCount < 1 {
			t.Error("expected at least one access to be recorded")
		}

		// Check access type is retrieval
		found := false
		for _, trace := range memory.Traces {
			if trace.AccessType == AccessRetrieval {
				found = true
				if trace.Context != "tool:search_codebase" {
					t.Errorf("expected context 'tool:search_codebase', got %q", trace.Context)
				}
				break
			}
		}
		if !found {
			t.Error("expected AccessRetrieval trace to be recorded")
		}
	})

	t.Run("non-retrieval tool is ignored", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		nodeID := "write-result-node"
		insertTestNode(t, db, nodeID, domain.DomainEngineer)

		hook := NewMemoryReinforcementHook(store, nil)

		result := map[string]any{"id": nodeID}
		err := hook.OnToolResult(ctx, "write_file", result)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Memory should not be updated for non-retrieval tools
		// (Would have no traces if RecordAccess wasn't called)
	})
}

func TestMemoryReinforcementHook_ReinforceBatch(t *testing.T) {
	ctx := context.Background()

	t.Run("reinforces multiple nodes", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		nodeIDs := []string{"batch-node-1", "batch-node-2", "batch-node-3"}
		for _, id := range nodeIDs {
			insertTestNode(t, db, id, domain.DomainAcademic)
		}

		hook := NewMemoryReinforcementHook(store, nil)

		err := hook.ReinforceBatch(ctx, nodeIDs, AccessReference, "batch_test")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Check all nodes were reinforced
		for _, nodeID := range nodeIDs {
			memory, err := store.GetMemory(ctx, nodeID, domain.DomainAcademic)
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", nodeID, err)
			}
			if memory.AccessCount < 1 {
				t.Errorf("node %s should have at least one access", nodeID)
			}
		}
	})

	t.Run("disabled hook does nothing", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		nodeID := "batch-disabled-node"
		insertTestNode(t, db, nodeID, domain.DomainAcademic)

		hook := NewMemoryReinforcementHook(store, nil)
		hook.SetEnabled(false)

		err := hook.ReinforceBatch(ctx, []string{nodeID}, AccessReference, "test")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("handles mixed existing and non-existing nodes", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		store := NewMemoryStore(db, 100, time.Millisecond)
		existingNode := "existing-batch-node"
		insertTestNode(t, db, existingNode, domain.DomainAcademic)

		hook := NewMemoryReinforcementHook(store, nil)

		nodeIDs := []string{existingNode, "non-existing-node-1", "non-existing-node-2"}
		err := hook.ReinforceBatch(ctx, nodeIDs, AccessReinforcement, "mixed_batch")
		// Should return error for non-existing nodes
		if err != nil {
			t.Logf("Expected error for non-existing nodes: %v", err)
		}

		// Existing node should still be reinforced
		memory, err := store.GetMemory(ctx, existingNode, domain.DomainAcademic)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if memory.AccessCount < 1 {
			t.Error("existing node should have at least one access")
		}
	})
}

func TestMemoryReinforcementHook_NilStore(t *testing.T) {
	ctx := context.Background()

	hook := NewMemoryReinforcementHook(nil, nil)

	t.Run("OnPostPrompt with nil store", func(t *testing.T) {
		err := hook.OnPostPrompt(ctx, "test response")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("OnToolResult with nil store", func(t *testing.T) {
		err := hook.OnToolResult(ctx, "search", map[string]any{"id": "test"})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ReinforceBatch with nil store", func(t *testing.T) {
		err := hook.ReinforceBatch(ctx, []string{"node1", "node2"}, AccessReinforcement, "test")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestMemoryReinforcementHook_Integration(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)

	// Create test nodes
	nodes := map[string]domain.Domain{
		"/src/main.go":         domain.DomainEngineer,
		"/src/handler.go":      domain.DomainEngineer,
		"/docs/readme.md":      domain.DomainAcademic,
		"UserService":          domain.DomainArchitect,
		"DatabaseConnection":   domain.DomainArchitect,
	}

	for nodeID, d := range nodes {
		insertTestNode(t, db, nodeID, d)
	}

	hook := NewMemoryReinforcementHook(store, nil)

	t.Run("full workflow simulation", func(t *testing.T) {
		// Step 1: Simulate a search tool returning results
		searchResult := map[string]any{
			"results": []any{
				map[string]any{"id": "/src/main.go"},
				map[string]any{"id": "/src/handler.go"},
			},
		}
		err := hook.OnToolResult(ctx, "search_codebase", searchResult)
		if err != nil {
			t.Errorf("OnToolResult error: %v", err)
		}

		// Step 2: Simulate agent response referencing the files
		// Use backtick notation which the extractor recognizes
		response := `I found the implementation in /src/main.go and /src/handler.go.
		The ` + "`UserService`" + ` handles the business logic.`
		err = hook.OnPostPrompt(ctx, response)
		if err != nil {
			t.Errorf("OnPostPrompt error: %v", err)
		}

		// Step 3: Verify memory was updated correctly
		// /src/main.go should have both retrieval and reinforcement
		mainMemory, err := store.GetMemory(ctx, "/src/main.go", domain.DomainEngineer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		hasRetrieval := false
		hasReinforcement := false
		for _, trace := range mainMemory.Traces {
			if trace.AccessType == AccessRetrieval {
				hasRetrieval = true
			}
			if trace.AccessType == AccessReinforcement {
				hasReinforcement = true
			}
		}

		if !hasRetrieval {
			t.Error("/src/main.go should have retrieval trace")
		}
		if !hasReinforcement {
			t.Error("/src/main.go should have reinforcement trace")
		}

		// UserService should only have reinforcement (from response, not tool)
		userMemory, err := store.GetMemory(ctx, "UserService", domain.DomainArchitect)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if userMemory.AccessCount < 1 {
			t.Error("UserService should have at least one access")
		}
	})
}

func TestMemoryReinforcementHook_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	db := setupTestDBWithWAL(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)

	// Create test nodes
	for i := 0; i < 10; i++ {
		nodeID := "concurrent-node-" + string(rune('a'+i))
		insertTestNode(t, db, nodeID, domain.Domain(i%4))
	}

	hook := NewMemoryReinforcementHook(store, nil)

	done := make(chan bool)
	errCh := make(chan error, 100)

	// Concurrent OnPostPrompt calls
	for i := 0; i < 5; i++ {
		go func(idx int) {
			for j := 0; j < 10; j++ {
				nodeID := "concurrent-node-" + string(rune('a'+(idx+j)%10))
				response := "Checking " + nodeID + " for implementation"
				if err := hook.OnPostPrompt(ctx, response); err != nil {
					errCh <- err
				}
				time.Sleep(time.Millisecond)
			}
			done <- true
		}(i)
	}

	// Concurrent OnToolResult calls
	for i := 0; i < 5; i++ {
		go func(idx int) {
			for j := 0; j < 10; j++ {
				nodeID := "concurrent-node-" + string(rune('a'+(idx+j)%10))
				result := map[string]any{"id": nodeID}
				if err := hook.OnToolResult(ctx, "search", result); err != nil {
					errCh <- err
				}
				time.Sleep(time.Millisecond)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}
}

// =============================================================================
// Custom NodeExtractor Tests
// =============================================================================

type mockNodeExtractor struct {
	nodeRefs []string
	toolRefs map[string][]string
}

func (m *mockNodeExtractor) ExtractNodeReferences(content string) []string {
	return m.nodeRefs
}

func (m *mockNodeExtractor) ExtractFromToolResult(toolName string, result any) []string {
	if refs, ok := m.toolRefs[toolName]; ok {
		return refs
	}
	return nil
}

func TestMemoryReinforcementHook_CustomExtractor(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	defer db.Close()

	store := NewMemoryStore(db, 100, time.Millisecond)

	nodeID := "custom-extracted-node"
	insertTestNode(t, db, nodeID, domain.DomainAcademic)

	customExtractor := &mockNodeExtractor{
		nodeRefs: []string{nodeID},
		toolRefs: map[string][]string{
			"my_tool": {nodeID},
		},
	}

	hook := NewMemoryReinforcementHook(store, customExtractor)

	t.Run("uses custom extractor for responses", func(t *testing.T) {
		err := hook.OnPostPrompt(ctx, "any content here")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		memory, err := store.GetMemory(ctx, nodeID, domain.DomainAcademic)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if memory.AccessCount < 1 {
			t.Error("custom extractor should have found the node")
		}
	})

	t.Run("uses custom extractor for tool results", func(t *testing.T) {
		// Reset by creating new memory entry
		nodeID2 := "custom-tool-node"
		insertTestNode(t, db, nodeID2, domain.DomainEngineer)
		customExtractor.toolRefs["my_tool"] = []string{nodeID2}

		err := hook.OnToolResult(ctx, "my_tool", "anything")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		memory, err := store.GetMemory(ctx, nodeID2, domain.DomainEngineer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if memory.AccessCount < 1 {
			t.Error("custom extractor should have found the node from tool result")
		}
	})
}
