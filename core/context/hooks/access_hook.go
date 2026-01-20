// Package hooks provides AR.7.5: Access Tracking Hooks - PostPrompt and PostTool hooks
// that track content access patterns and promote retrieved content to hot cache.
package hooks

import (
	"context"
	"strings"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// AccessTrackingHookName is the unique identifier for the access tracking hook.
	AccessTrackingHookName = "access_tracking"

	// ContentPromotionHookName is the unique identifier for the content promotion hook.
	ContentPromotionHookName = "content_promotion"
)

// =============================================================================
// Access Tracker Interface
// =============================================================================

// AccessTracker is the interface for recording content access patterns.
type AccessTracker interface {
	// RecordAccess records that a content ID was accessed.
	RecordAccess(id string, turn int, source string) error
}

// =============================================================================
// Content Promoter Interface
// =============================================================================

// ContentPromoter is the interface for promoting content to hot cache.
type ContentPromoter interface {
	// Promote adds content to the hot cache.
	Promote(entry *ctxpkg.ContentEntry) error
}

// =============================================================================
// Access Tracking Hook
// =============================================================================

// AccessTrackingHookConfig holds configuration for the access tracking hook.
type AccessTrackingHookConfig struct {
	// Tracker records access patterns.
	Tracker AccessTracker
}

// AccessTrackingHook tracks which content IDs are accessed in LLM responses.
// It executes at HookPriorityNormal (50) in the PostPrompt phase.
type AccessTrackingHook struct {
	tracker AccessTracker
	enabled bool
}

// NewAccessTrackingHook creates a new access tracking hook.
func NewAccessTrackingHook(config AccessTrackingHookConfig) *AccessTrackingHook {
	return &AccessTrackingHook{
		tracker: config.Tracker,
		enabled: true,
	}
}

// Name returns the hook's unique identifier.
func (h *AccessTrackingHook) Name() string {
	return AccessTrackingHookName
}

// Phase returns when this hook executes (PostPrompt).
func (h *AccessTrackingHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePostPrompt
}

// Priority returns the execution order (Normal = 50).
func (h *AccessTrackingHook) Priority() int {
	return int(ctxpkg.HookPriorityNormal)
}

// Enabled returns whether this hook is currently active.
func (h *AccessTrackingHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *AccessTrackingHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// Execute extracts and records content references from the response.
func (h *AccessTrackingHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	// Skip if disabled or no tracker
	if !h.enabled || h.tracker == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if no response
	if data.Response == "" {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Extract content references from response
	ids := extractContentReferences(data.Response)
	if len(ids) == 0 {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Record each access
	for _, id := range ids {
		_ = h.tracker.RecordAccess(id, data.TurnNumber, "in_response")
	}

	return &ctxpkg.HookResult{Modified: false}, nil
}

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *AccessTrackingHook) ToPromptHook() *ctxpkg.PromptHook {
	return ctxpkg.NewPromptHook(
		h.Name(),
		h.Phase(),
		h.Priority(),
		func(ctx context.Context, data *ctxpkg.PromptHookData) (*ctxpkg.HookResult, error) {
			return h.Execute(ctx, data)
		},
	)
}

// =============================================================================
// Content Promotion Hook
// =============================================================================

// ContentPromotionHookConfig holds configuration for the content promotion hook.
type ContentPromotionHookConfig struct {
	// Tracker records access patterns.
	Tracker AccessTracker

	// Promoter promotes content to hot cache.
	Promoter ContentPromoter
}

// ContentPromotionHook promotes retrieved content to hot cache.
// It executes at HookPriorityLate (75) in the PostTool phase.
type ContentPromotionHook struct {
	tracker  AccessTracker
	promoter ContentPromoter
	enabled  bool
}

// NewContentPromotionHook creates a new content promotion hook.
func NewContentPromotionHook(config ContentPromotionHookConfig) *ContentPromotionHook {
	return &ContentPromotionHook{
		tracker:  config.Tracker,
		promoter: config.Promoter,
		enabled:  true,
	}
}

// Name returns the hook's unique identifier.
func (h *ContentPromotionHook) Name() string {
	return ContentPromotionHookName
}

// Phase returns when this hook executes (PostTool).
func (h *ContentPromotionHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePostTool
}

// Priority returns the execution order (Late = 75).
func (h *ContentPromotionHook) Priority() int {
	return int(ctxpkg.HookPriorityLate)
}

// Enabled returns whether this hook is currently active.
func (h *ContentPromotionHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *ContentPromotionHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// Execute promotes retrieved content to hot cache.
func (h *ContentPromotionHook) Execute(
	ctx context.Context,
	data *ctxpkg.ToolCallHookData,
) (*ctxpkg.HookResult, error) {
	// Skip if disabled
	if !h.enabled {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if not a retrieval tool
	if !isRetrievalTool(data.ToolName) {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Extract content from tool output
	entries := extractContentFromToolOutput(data.ToolOutput)
	if len(entries) == 0 {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Record access and promote each entry
	for _, entry := range entries {
		if h.tracker != nil {
			_ = h.tracker.RecordAccess(entry.ID, data.TurnNumber, "tool_retrieved")
		}
		if h.promoter != nil {
			_ = h.promoter.Promote(entry)
		}
	}

	return &ctxpkg.HookResult{Modified: false}, nil
}

// ToToolCallHook converts this hook to a generic ToolCallHook for registration.
func (h *ContentPromotionHook) ToToolCallHook() *ctxpkg.ToolCallHook {
	return ctxpkg.NewToolCallHook(
		h.Name(),
		h.Phase(),
		h.Priority(),
		func(ctx context.Context, data *ctxpkg.ToolCallHookData) (*ctxpkg.HookResult, error) {
			return h.Execute(ctx, data)
		},
	)
}

// =============================================================================
// Helper Functions
// =============================================================================

// extractContentReferences extracts content IDs referenced in the response.
func extractContentReferences(response string) []string {
	if response == "" {
		return nil
	}

	ids := make([]string, 0)
	lines := strings.Split(response, "\n")

	for _, line := range lines {
		// Look for [EXCERPT N] source_id format
		if strings.Contains(line, "[EXCERPT") || strings.Contains(line, "[SUMMARY") {
			if id := extractIDFromExcerptLine(line); id != "" {
				ids = append(ids, id)
			}
		}

		// Look for file paths (common in code-related responses)
		if filePath := extractFilePath(line); filePath != "" {
			ids = append(ids, filePath)
		}
	}

	return deduplicateIDs(ids)
}

// extractIDFromExcerptLine extracts the source ID from an excerpt/summary line.
func extractIDFromExcerptLine(line string) string {
	// Format: "### [EXCERPT 1] source_id (confidence: 0.85)"
	if start := strings.Index(line, "]"); start >= 0 {
		remainder := line[start+1:]
		if end := strings.Index(remainder, "("); end >= 0 {
			return strings.TrimSpace(remainder[:end])
		}
		// No parenthesis, just trim
		return strings.TrimSpace(remainder)
	}
	return ""
}

// extractFilePath extracts file paths from a line.
func extractFilePath(line string) string {
	// Simple heuristic: look for paths with common extensions
	extensions := []string{".go", ".py", ".js", ".ts", ".java", ".c", ".cpp", ".h", ".md"}

	for _, ext := range extensions {
		if idx := strings.Index(line, ext); idx >= 0 {
			// Walk backwards to find start of path
			start := idx
			for start > 0 && isPathChar(line[start-1]) {
				start--
			}
			end := idx + len(ext)

			// Check for trailing colon (line number)
			if end < len(line) && line[end] == ':' {
				end++ // Include the colon
				for end < len(line) && line[end] >= '0' && line[end] <= '9' {
					end++
				}
			}

			path := line[start:end]
			if isValidPath(path) {
				return path
			}
		}
	}

	return ""
}

// isPathChar returns true if the character can be part of a file path.
func isPathChar(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '/' || c == '\\' || c == '_' || c == '-' || c == '.'
}

// isValidPath returns true if the string looks like a valid file path.
func isValidPath(s string) bool {
	if len(s) < 3 {
		return false
	}
	// Must contain at least one path separator or look like a filename
	return strings.Contains(s, "/") ||
		strings.Contains(s, "\\") ||
		(len(s) > 3 && s[len(s)-3:] == ".go")
}

// deduplicateIDs removes duplicate IDs while preserving order.
func deduplicateIDs(ids []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(ids))

	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}

	return result
}

// retrievalToolNames is the list of tool names that retrieve content.
var retrievalToolNames = map[string]bool{
	"read_file":       true,
	"read":            true,
	"get_file":        true,
	"fetch_content":   true,
	"retrieve":        true,
	"get_code":        true,
	"get_definition":  true,
	"search_code":     true,
	"search_files":    true,
	"semantic_search": true,
	"bleve_search":    true,
	"vector_search":   true,
}

// isRetrievalTool returns true if the tool name indicates content retrieval.
func isRetrievalTool(toolName string) bool {
	normalized := strings.ToLower(toolName)
	if retrievalToolNames[normalized] {
		return true
	}

	// Also check for common patterns
	return strings.Contains(normalized, "read") ||
		strings.Contains(normalized, "fetch") ||
		strings.Contains(normalized, "retrieve") ||
		strings.Contains(normalized, "search")
}

// extractContentFromToolOutput extracts ContentEntry objects from tool output.
func extractContentFromToolOutput(output any) []*ctxpkg.ContentEntry {
	if output == nil {
		return nil
	}

	entries := make([]*ctxpkg.ContentEntry, 0)

	switch v := output.(type) {
	case *ctxpkg.ContentEntry:
		entries = append(entries, v)
	case []*ctxpkg.ContentEntry:
		entries = append(entries, v...)
	case map[string]any:
		// Try to extract content entry from map
		if entry := extractEntryFromMap(v); entry != nil {
			entries = append(entries, entry)
		}
	case []any:
		// Try to extract from slice
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if entry := extractEntryFromMap(m); entry != nil {
					entries = append(entries, entry)
				}
			}
		}
	}

	return entries
}

// extractEntryFromMap attempts to create a ContentEntry from a map.
func extractEntryFromMap(m map[string]any) *ctxpkg.ContentEntry {
	entry := &ctxpkg.ContentEntry{}

	if id, ok := m["id"].(string); ok {
		entry.ID = id
	} else if path, ok := m["path"].(string); ok {
		entry.ID = path
	}

	if content, ok := m["content"].(string); ok {
		entry.Content = content
	}

	if entry.ID == "" && entry.Content == "" {
		return nil
	}

	return entry
}
