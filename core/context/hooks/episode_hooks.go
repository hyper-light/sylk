// Package hooks provides AR.7.2: Episode Tracker Hooks - manages EpisodeObservation lifecycle
// for the Bayesian learning system.
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
	// EpisodeInitHookName is the unique identifier for the init hook.
	EpisodeInitHookName = "episode_tracker_init"

	// EpisodeObservationHookName is the unique identifier for the observation hook.
	EpisodeObservationHookName = "episode_observation"

	// SearchToolObservationHookName is the unique identifier for the search tool hook.
	SearchToolObservationHookName = "search_tool_observation"
)

// searchToolNames is the list of tool names that indicate search operations.
var searchToolNames = map[string]bool{
	"search":          true,
	"search_code":     true,
	"search_files":    true,
	"search_docs":     true,
	"semantic_search": true,
	"grep":            true,
	"find":            true,
	"bleve_search":    true,
	"vector_search":   true,
}

// =============================================================================
// Episode Tracker Init Hook
// =============================================================================

// EpisodeTrackerInitHookConfig holds configuration for the init hook.
type EpisodeTrackerInitHookConfig struct {
	// Tracker is the episode tracker to initialize episodes on.
	Tracker *ctxpkg.EpisodeTracker
}

// EpisodeTrackerInitHook initializes episode tracking at the start of each turn.
// It executes at HookPriorityEarly (25) in the PrePrompt phase.
type EpisodeTrackerInitHook struct {
	tracker *ctxpkg.EpisodeTracker
	enabled bool
}

// NewEpisodeTrackerInitHook creates a new episode tracker init hook.
func NewEpisodeTrackerInitHook(config EpisodeTrackerInitHookConfig) *EpisodeTrackerInitHook {
	return &EpisodeTrackerInitHook{
		tracker: config.Tracker,
		enabled: true,
	}
}

// Name returns the hook's unique identifier.
func (h *EpisodeTrackerInitHook) Name() string {
	return EpisodeInitHookName
}

// Phase returns when this hook executes (PrePrompt).
func (h *EpisodeTrackerInitHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePrePrompt
}

// Priority returns the execution order (Early = 25).
func (h *EpisodeTrackerInitHook) Priority() int {
	return int(ctxpkg.HookPriorityEarly)
}

// Enabled returns whether this hook is currently active.
func (h *EpisodeTrackerInitHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *EpisodeTrackerInitHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// Execute initializes episode tracking for the current turn.
func (h *EpisodeTrackerInitHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	if !h.enabled || h.tracker == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Start tracking the episode
	h.tracker.StartEpisode(data.AgentID, data.AgentType, data.TurnNumber)

	// Record prefetched IDs if any
	if data.PrefetchedContent != nil {
		ids := extractExcerptIDs(data.PrefetchedContent)
		h.tracker.RecordPrefetchedIDs(ids)
	}

	return &ctxpkg.HookResult{Modified: false}, nil
}

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *EpisodeTrackerInitHook) ToPromptHook() *ctxpkg.PromptHook {
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
// Episode Observation Hook
// =============================================================================

// EpisodeObservationHookConfig holds configuration for the observation hook.
type EpisodeObservationHookConfig struct {
	// Tracker is the episode tracker to finalize episodes on.
	Tracker *ctxpkg.EpisodeTracker

	// ObservationLog receives completed observations for persistence.
	ObservationLog ObservationLogger
}

// ObservationLogger is the interface for logging observations.
type ObservationLogger interface {
	// Log records an episode observation.
	Log(obs ctxpkg.EpisodeObservation) error
}

// EpisodeObservationHook finalizes episode tracking at the end of each turn.
// It executes at HookPriorityLate (75) in the PostPrompt phase.
type EpisodeObservationHook struct {
	tracker        *ctxpkg.EpisodeTracker
	observationLog ObservationLogger
	enabled        bool
}

// NewEpisodeObservationHook creates a new episode observation hook.
func NewEpisodeObservationHook(config EpisodeObservationHookConfig) *EpisodeObservationHook {
	return &EpisodeObservationHook{
		tracker:        config.Tracker,
		observationLog: config.ObservationLog,
		enabled:        true,
	}
}

// Name returns the hook's unique identifier.
func (h *EpisodeObservationHook) Name() string {
	return EpisodeObservationHookName
}

// Phase returns when this hook executes (PostPrompt).
func (h *EpisodeObservationHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePostPrompt
}

// Priority returns the execution order (Late = 75).
func (h *EpisodeObservationHook) Priority() int {
	return int(ctxpkg.HookPriorityLate)
}

// Enabled returns whether this hook is currently active.
func (h *EpisodeObservationHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *EpisodeObservationHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// Execute finalizes episode tracking and records the observation.
func (h *EpisodeObservationHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	if !h.enabled || h.tracker == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if no active episode
	if !h.tracker.HasActiveEpisode() {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Extract used content IDs from response
	usedIDs := extractContentIDsFromResponse(data.Response)
	for _, id := range usedIDs {
		h.tracker.RecordUsedID(id)
	}

	// Finalize the episode
	obs := h.tracker.FinalizeEpisode(data.Response, data.ToolCalls)

	// Log the observation if logger is available
	// Observation logging is best-effort; errors are recorded but do not fail the hook
	if h.observationLog != nil {
		if err := h.observationLog.Log(obs); err != nil {
			return &ctxpkg.HookResult{
				Modified: false,
				Error:    err, // Non-fatal: recorded for debugging purposes
			}, nil
		}
	}

	return &ctxpkg.HookResult{Modified: false}, nil
}

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *EpisodeObservationHook) ToPromptHook() *ctxpkg.PromptHook {
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
// Search Tool Observation Hook
// =============================================================================

// SearchToolObservationHookConfig holds configuration for the search tool hook.
type SearchToolObservationHookConfig struct {
	// Tracker is the episode tracker to record search signals on.
	Tracker *ctxpkg.EpisodeTracker
}

// SearchToolObservationHook tracks search tool calls during an episode.
// It executes at HookPriorityNormal (50) in the PostTool phase.
type SearchToolObservationHook struct {
	tracker *ctxpkg.EpisodeTracker
	enabled bool
}

// NewSearchToolObservationHook creates a new search tool observation hook.
func NewSearchToolObservationHook(config SearchToolObservationHookConfig) *SearchToolObservationHook {
	return &SearchToolObservationHook{
		tracker: config.Tracker,
		enabled: true,
	}
}

// Name returns the hook's unique identifier.
func (h *SearchToolObservationHook) Name() string {
	return SearchToolObservationHookName
}

// Phase returns when this hook executes (PostTool).
func (h *SearchToolObservationHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePostTool
}

// Priority returns the execution order (Normal = 50).
func (h *SearchToolObservationHook) Priority() int {
	return int(ctxpkg.HookPriorityNormal)
}

// Enabled returns whether this hook is currently active.
func (h *SearchToolObservationHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *SearchToolObservationHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// Execute records search tool calls in the episode tracker.
func (h *SearchToolObservationHook) Execute(
	ctx context.Context,
	data *ctxpkg.ToolCallHookData,
) (*ctxpkg.HookResult, error) {
	if !h.enabled || h.tracker == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if no active episode
	if !h.tracker.HasActiveEpisode() {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Record all tool calls
	h.tracker.RecordToolCallWithDetails(ctxpkg.ToolCall{
		Name:     data.ToolName,
		Duration: data.Duration,
		Success:  data.Error == nil,
	})

	// If this is a search tool, record it as a search after prefetch
	if isSearchTool(data.ToolName) {
		query := extractQueryFromToolInput(data.ToolInput)
		if query != "" {
			h.tracker.RecordSearchAfterPrefetch(query)
		}
	}

	return &ctxpkg.HookResult{Modified: false}, nil
}

// ToToolCallHook converts this hook to a generic ToolCallHook for registration.
func (h *SearchToolObservationHook) ToToolCallHook() *ctxpkg.ToolCallHook {
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

// extractExcerptIDs extracts content IDs from augmented query excerpts.
func extractExcerptIDs(aq *ctxpkg.AugmentedQuery) []string {
	if aq == nil {
		return nil
	}

	ids := make([]string, 0, len(aq.Excerpts)+len(aq.Summaries))

	for _, excerpt := range aq.Excerpts {
		if excerpt.Source != "" {
			ids = append(ids, excerpt.Source)
		}
	}

	for _, summary := range aq.Summaries {
		if summary.Source != "" {
			ids = append(ids, summary.Source)
		}
	}

	return ids
}

// extractContentIDsFromResponse extracts content IDs referenced in the response.
// Looks for patterns like file paths, content references, and citation markers.
// Currently supports the "[EXCERPT N]" and "[SUMMARY N]" marker format.
func extractContentIDsFromResponse(response string) []string {
	if response == "" {
		return nil
	}

	// Pre-allocate with reasonable capacity based on typical response patterns
	ids := make([]string, 0, 8)

	// Look for content reference patterns
	// This is a simplified implementation - in production, this would use
	// more sophisticated parsing based on the content format.

	// Pattern 1: [EXCERPT N] source_id format
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		if strings.Contains(line, "[EXCERPT") || strings.Contains(line, "[SUMMARY") {
			// Extract source from line like "### [EXCERPT 1] source (confidence: 0.85)"
			if start := strings.Index(line, "]"); start >= 0 {
				remainder := line[start+1:]
				if end := strings.Index(remainder, "("); end >= 0 {
					source := strings.TrimSpace(remainder[:end])
					if source != "" {
						ids = append(ids, source)
					}
				}
			}
		}
	}

	return ids
}

// isSearchTool returns true if the tool name indicates a search operation.
func isSearchTool(toolName string) bool {
	normalized := strings.ToLower(toolName)
	if searchToolNames[normalized] {
		return true
	}

	// Also check if name contains "search"
	return strings.Contains(normalized, "search")
}

// extractQueryFromToolInput extracts the query parameter from tool input.
func extractQueryFromToolInput(input map[string]any) string {
	if input == nil {
		return ""
	}

	// Try common query parameter names
	queryKeys := []string{"query", "q", "search", "pattern", "term"}
	for _, key := range queryKeys {
		if val, ok := input[key]; ok {
			if str, ok := val.(string); ok {
				return str
			}
		}
	}

	return ""
}
