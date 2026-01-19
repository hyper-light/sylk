// Package memory provides ACT-R based memory management for adaptive retrieval.
// MD.6.1 MemoryReinforcementHook implementation for automatic memory reinforcement.
package memory

import (
	"context"
	"regexp"
	"strings"
	"sync"
)

// =============================================================================
// Hook Priority Constants
// =============================================================================

// HookPriority determines execution order for hooks.
// Lower values execute earlier, higher values execute later.
const (
	// HookPriorityEarly executes early in the hook pipeline.
	HookPriorityEarly = 100

	// HookPriorityNormal is the default execution priority.
	HookPriorityNormal = 500

	// HookPriorityLate executes late in the hook pipeline.
	// Used for observation and recording hooks like memory reinforcement.
	HookPriorityLate = 900
)

// =============================================================================
// NodeExtractor Interface
// =============================================================================

// NodeExtractor extracts node references from content and tool results.
// Implementations can use different strategies for identifying node IDs.
type NodeExtractor interface {
	// ExtractNodeReferences extracts node IDs from text content.
	// Returns a slice of unique node ID strings found in the content.
	ExtractNodeReferences(content string) []string

	// ExtractFromToolResult extracts node IDs from tool execution results.
	// The toolName parameter helps identify the result structure.
	ExtractFromToolResult(toolName string, result any) []string
}

// =============================================================================
// DefaultNodeExtractor Implementation
// =============================================================================

// DefaultNodeExtractor provides standard node extraction logic for code references.
// It extracts file paths, function names, type names, and code block references.
type DefaultNodeExtractor struct {
	// retrievalTools is the set of tool names that are considered retrieval tools
	retrievalTools map[string]bool

	// patterns for extracting references
	filePathPattern   *regexp.Regexp
	funcPattern       *regexp.Regexp
	typePattern       *regexp.Regexp
	backtickPattern   *regexp.Regexp
	codeBlockPattern  *regexp.Regexp
	nodeIDPattern     *regexp.Regexp
	quotedPathPattern *regexp.Regexp

	mu sync.RWMutex
}

// NewDefaultNodeExtractor creates a new DefaultNodeExtractor with standard patterns.
func NewDefaultNodeExtractor() *DefaultNodeExtractor {
	return &DefaultNodeExtractor{
		retrievalTools: map[string]bool{
			"search_codebase":  true,
			"retrieve_context": true,
			"read_file":        true,
			"grep":             true,
			"find":             true,
			"semantic_search":  true,
			"glob":             true,
			"read":             true,
			"search":           true,
		},
		// File paths: /path/to/file.go, ./relative/path.ts, etc.
		filePathPattern: regexp.MustCompile(`(?:^|[^\w])([./][\w./\-_]+\.[a-zA-Z]{1,10})(?:[^\w]|$)`),
		// Function references: func FunctionName, function name(), def name
		funcPattern: regexp.MustCompile(`(?:func|function|def)\s+([A-Za-z_][A-Za-z0-9_]*)`),
		// Type references: type TypeName, class ClassName, struct StructName
		typePattern: regexp.MustCompile(`(?:type|class|struct|interface)\s+([A-Za-z_][A-Za-z0-9_]*)`),
		// Backtick references: `identifier` or `path/to/file`
		backtickPattern: regexp.MustCompile("`([^`]+)`"),
		// Code block references: ```language ... ```
		codeBlockPattern: regexp.MustCompile("```(?:\\w+)?\\s*([^`]+)```"),
		// Node ID pattern: matches common node ID formats (UUID-like, path-based)
		nodeIDPattern: regexp.MustCompile(`node[_-]?[iI][dD]["\s:=]+["']?([a-zA-Z0-9\-_/\.]+)["']?`),
		// Quoted file paths: "path/to/file.go" or 'path/to/file.go'
		quotedPathPattern: regexp.MustCompile(`["']([a-zA-Z0-9\-_/\.]+\.[a-zA-Z]{1,10})["']`),
	}
}

// ExtractNodeReferences extracts potential node references from text content.
// It looks for file paths, function names, type names, and code references.
func (e *DefaultNodeExtractor) ExtractNodeReferences(content string) []string {
	if content == "" {
		return nil
	}

	seen := make(map[string]bool)
	var refs []string

	// Helper to add unique references
	addRef := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}

	// Extract file paths
	for _, match := range e.filePathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			addRef(match[1])
		}
	}

	// Extract function names
	for _, match := range e.funcPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			addRef(match[1])
		}
	}

	// Extract type names
	for _, match := range e.typePattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			addRef(match[1])
		}
	}

	// Extract backtick references
	for _, match := range e.backtickPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			ref := match[1]
			// Filter out likely code snippets (multi-word or special chars)
			if !strings.Contains(ref, " ") && !strings.Contains(ref, "\n") {
				addRef(ref)
			}
		}
	}

	// Extract node IDs from explicit references
	for _, match := range e.nodeIDPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			addRef(match[1])
		}
	}

	// Extract quoted paths
	for _, match := range e.quotedPathPattern.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			addRef(match[1])
		}
	}

	return refs
}

// ExtractFromToolResult extracts node IDs from retrieval tool results.
// It understands common result formats from search and file reading tools.
func (e *DefaultNodeExtractor) ExtractFromToolResult(toolName string, result any) []string {
	if !e.isRetrievalTool(toolName) {
		return nil
	}

	var refs []string
	seen := make(map[string]bool)

	addRef := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}

	// Handle different result types
	switch v := result.(type) {
	case string:
		// For simple string results, extract any node references
		for _, ref := range e.ExtractNodeReferences(v) {
			addRef(ref)
		}

	case []string:
		// Array of strings (file paths, node IDs)
		for _, s := range v {
			addRef(s)
		}

	case map[string]any:
		// Structured result with potential node fields
		e.extractFromMap(v, addRef)

	case []any:
		// Array of results
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				e.extractFromMap(m, addRef)
			} else if s, ok := item.(string); ok {
				addRef(s)
			}
		}
	}

	return refs
}

// extractFromMap extracts node references from a map result.
func (e *DefaultNodeExtractor) extractFromMap(m map[string]any, addRef func(string)) {
	// Common field names for node IDs
	nodeFields := []string{"id", "node_id", "nodeId", "ID", "NodeID", "path", "file_path", "filePath", "file"}

	for _, field := range nodeFields {
		if val, ok := m[field]; ok {
			if s, ok := val.(string); ok {
				addRef(s)
			}
		}
	}

	// Check for results array
	if results, ok := m["results"].([]any); ok {
		for _, item := range results {
			if itemMap, ok := item.(map[string]any); ok {
				e.extractFromMap(itemMap, addRef)
			}
		}
	}

	// Check for matches array
	if matches, ok := m["matches"].([]any); ok {
		for _, item := range matches {
			if itemMap, ok := item.(map[string]any); ok {
				e.extractFromMap(itemMap, addRef)
			}
		}
	}

	// Check for nodes array
	if nodes, ok := m["nodes"].([]any); ok {
		for _, item := range nodes {
			if itemMap, ok := item.(map[string]any); ok {
				e.extractFromMap(itemMap, addRef)
			}
		}
	}
}

// isRetrievalTool returns true if the tool name is a retrieval tool.
func (e *DefaultNodeExtractor) isRetrievalTool(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Check exact match
	if e.retrievalTools[name] {
		return true
	}

	// Check lowercase
	lower := strings.ToLower(name)
	if e.retrievalTools[lower] {
		return true
	}

	// Check if name contains retrieval-related keywords
	retrievalKeywords := []string{"search", "find", "grep", "read", "retrieve", "query", "lookup"}
	for _, keyword := range retrievalKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}

	return false
}

// AddRetrievalTool adds a tool name to the set of retrieval tools.
func (e *DefaultNodeExtractor) AddRetrievalTool(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.retrievalTools[name] = true
}

// RemoveRetrievalTool removes a tool name from the set of retrieval tools.
func (e *DefaultNodeExtractor) RemoveRetrievalTool(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.retrievalTools, name)
}

// =============================================================================
// MemoryReinforcementHook Implementation
// =============================================================================

// MemoryReinforcementHook automatically reinforces memory for nodes referenced
// in agent responses and retrieved via tool calls. It runs late in the hook
// pipeline to observe what nodes were accessed.
//
// Access types used:
//   - OnPostPrompt: AccessReinforcement (agent actively used the knowledge)
//   - OnToolResult: AccessRetrieval (knowledge was retrieved for agent)
type MemoryReinforcementHook struct {
	store         *MemoryStore
	nodeExtractor NodeExtractor
	priority      int
	enabled       bool

	mu sync.RWMutex
}

// NewMemoryReinforcementHook creates a new MemoryReinforcementHook.
// Uses DefaultNodeExtractor if extractor is nil.
func NewMemoryReinforcementHook(store *MemoryStore, extractor NodeExtractor) *MemoryReinforcementHook {
	if extractor == nil {
		extractor = NewDefaultNodeExtractor()
	}
	return &MemoryReinforcementHook{
		store:         store,
		nodeExtractor: extractor,
		priority:      HookPriorityLate,
		enabled:       true,
	}
}

// NewMemoryReinforcementHookWithPriority creates a hook with a custom priority.
func NewMemoryReinforcementHookWithPriority(store *MemoryStore, extractor NodeExtractor, priority int) *MemoryReinforcementHook {
	hook := NewMemoryReinforcementHook(store, extractor)
	hook.priority = priority
	return hook
}

// Name returns the hook's unique identifier.
func (h *MemoryReinforcementHook) Name() string {
	return "memory_reinforcement"
}

// Priority returns the execution order (higher = later).
func (h *MemoryReinforcementHook) Priority() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.priority
}

// Agents returns the agents this hook applies to.
// Returns nil to indicate it applies to all agents.
func (h *MemoryReinforcementHook) Agents() []string {
	return nil
}

// Enabled returns whether the hook is currently active.
func (h *MemoryReinforcementHook) Enabled() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.enabled
}

// SetEnabled updates the hook's enabled state.
func (h *MemoryReinforcementHook) SetEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enabled = enabled
}

// SetPriority updates the hook's priority.
func (h *MemoryReinforcementHook) SetPriority(priority int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.priority = priority
}

// Store returns the underlying MemoryStore.
func (h *MemoryReinforcementHook) Store() *MemoryStore {
	return h.store
}

// NodeExtractor returns the node extractor.
func (h *MemoryReinforcementHook) NodeExtractor() NodeExtractor {
	return h.nodeExtractor
}

// SetNodeExtractor updates the node extractor.
func (h *MemoryReinforcementHook) SetNodeExtractor(extractor NodeExtractor) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if extractor != nil {
		h.nodeExtractor = extractor
	}
}

// OnPostPrompt is called after an LLM response is received.
// It extracts node references from the response and reinforces those memories.
// Uses AccessReinforcement type since the agent actively used the knowledge.
func (h *MemoryReinforcementHook) OnPostPrompt(ctx context.Context, response string) error {
	if !h.Enabled() || h.store == nil {
		return nil
	}

	if response == "" {
		return nil
	}

	// Extract node references from the response
	nodeRefs := h.nodeExtractor.ExtractNodeReferences(response)
	if len(nodeRefs) == 0 {
		return nil
	}

	// Reinforce each referenced node
	var firstErr error
	for _, nodeID := range nodeRefs {
		err := h.store.RecordAccess(ctx, nodeID, AccessReinforcement, "agent_response")
		if err != nil && firstErr == nil {
			// Record first error but continue processing
			// Some node IDs may not exist in the store
			firstErr = err
		}
	}

	// Don't return errors for missing nodes - this is expected behavior
	// when the extractor finds references that aren't tracked in memory
	return nil
}

// OnToolResult is called after a tool execution returns results.
// It extracts node references from retrieval tool results and records access.
// Uses AccessRetrieval type since the knowledge was retrieved for the agent.
func (h *MemoryReinforcementHook) OnToolResult(ctx context.Context, toolName string, result any) error {
	if !h.Enabled() || h.store == nil {
		return nil
	}

	if toolName == "" || result == nil {
		return nil
	}

	// Extract node IDs from the tool result
	nodeRefs := h.nodeExtractor.ExtractFromToolResult(toolName, result)
	if len(nodeRefs) == 0 {
		return nil
	}

	// Record retrieval access for each node
	contextStr := "tool:" + toolName
	var firstErr error
	for _, nodeID := range nodeRefs {
		err := h.store.RecordAccess(ctx, nodeID, AccessRetrieval, contextStr)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Don't propagate errors for missing nodes
	return nil
}

// ReinforceBatch reinforces multiple nodes at once with a given access type.
// This is useful for bulk reinforcement operations.
func (h *MemoryReinforcementHook) ReinforceBatch(ctx context.Context, nodeIDs []string, accessType AccessType, contextStr string) error {
	if !h.Enabled() || h.store == nil {
		return nil
	}

	var firstErr error
	for _, nodeID := range nodeIDs {
		err := h.store.RecordAccess(ctx, nodeID, accessType, contextStr)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
