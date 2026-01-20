// Package hooks provides AR.7.6: Guide Routing Cache Hook - PrePrompt hook for Guide
// agent that injects routing history for consistent routing decisions.
package hooks

import (
	"context"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// GuideRoutingHookName is the unique identifier for this hook.
	GuideRoutingHookName = "guide_routing_cache"

	// GuideAgentType is the agent type this hook targets.
	GuideAgentType = "guide"

	// DefaultMaxRoutingHistory is the default maximum routing decisions to include.
	DefaultMaxRoutingHistory = 10
)

// =============================================================================
// Routing Decision Types
// =============================================================================

// RoutingDecision represents a past routing decision from Guide agent.
type RoutingDecision struct {
	// MessageID is the message that triggered this routing.
	MessageID string

	// TargetAgent is the agent the query was routed to.
	TargetAgent string

	// Confidence is the confidence level of the routing (0.0-1.0).
	Confidence float64

	// Reason explains why this routing was chosen.
	Reason string

	// Timestamp is when the routing occurred.
	Timestamp time.Time
}

// =============================================================================
// Routing History Provider Interface
// =============================================================================

// RoutingHistoryProvider provides access to past routing decisions.
type RoutingHistoryProvider interface {
	// GetRecentRoutings returns the most recent routing decisions.
	GetRecentRoutings(ctx context.Context, sessionID string, limit int) ([]RoutingDecision, error)

	// GetRoutingsForPattern returns routing decisions matching a query pattern.
	GetRoutingsForPattern(ctx context.Context, pattern string, limit int) ([]RoutingDecision, error)
}

// =============================================================================
// Guide Routing Cache Hook
// =============================================================================

// GuideRoutingCacheHookConfig holds configuration for the guide routing hook.
type GuideRoutingCacheHookConfig struct {
	// RoutingProvider provides access to routing history.
	RoutingProvider RoutingHistoryProvider

	// MaxHistory is the maximum number of routing decisions to include.
	// Default: 10
	MaxHistory int
}

// GuideRoutingCacheHook injects routing history into Guide agent context.
// It executes at HookPriorityNormal (50) in the PrePrompt phase.
type GuideRoutingCacheHook struct {
	routingProvider RoutingHistoryProvider
	maxHistory      int
	enabled         bool
}

// NewGuideRoutingCacheHook creates a new guide routing cache hook.
func NewGuideRoutingCacheHook(config GuideRoutingCacheHookConfig) *GuideRoutingCacheHook {
	maxHistory := config.MaxHistory
	if maxHistory <= 0 {
		maxHistory = DefaultMaxRoutingHistory
	}

	return &GuideRoutingCacheHook{
		routingProvider: config.RoutingProvider,
		maxHistory:      maxHistory,
		enabled:         true,
	}
}

// =============================================================================
// Hook Interface Implementation
// =============================================================================

// Name returns the hook's unique identifier.
func (h *GuideRoutingCacheHook) Name() string {
	return GuideRoutingHookName
}

// Phase returns when this hook executes (PrePrompt).
func (h *GuideRoutingCacheHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePrePrompt
}

// Priority returns the execution order (Normal = 50).
func (h *GuideRoutingCacheHook) Priority() int {
	return int(ctxpkg.HookPriorityNormal)
}

// Enabled returns whether this hook is currently active.
func (h *GuideRoutingCacheHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *GuideRoutingCacheHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// =============================================================================
// Execution
// =============================================================================

// Execute injects routing history into Guide agent context.
func (h *GuideRoutingCacheHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	// Skip if disabled or no provider
	if !h.enabled || h.routingProvider == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Only apply to Guide agent
	if !h.isGuideAgent(data.AgentType) {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Get routing history
	routings, err := h.fetchRoutingHistory(ctx, data)
	if err != nil {
		return &ctxpkg.HookResult{Modified: false, Error: err}, nil
	}

	// Skip if no routing history
	if len(routings) == 0 {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Build routing context content
	content := h.buildRoutingContent(routings)

	// Inject into prefetched content
	data.PrefetchedContent = h.injectRoutingContext(data.PrefetchedContent, content)

	return &ctxpkg.HookResult{
		Modified:        true,
		ModifiedContent: data,
	}, nil
}

// isGuideAgent checks if the agent type is the Guide agent.
func (h *GuideRoutingCacheHook) isGuideAgent(agentType string) bool {
	return strings.ToLower(agentType) == GuideAgentType
}

// fetchRoutingHistory retrieves routing history from the provider.
func (h *GuideRoutingCacheHook) fetchRoutingHistory(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) ([]RoutingDecision, error) {
	// First try to get routings matching the current query
	if data.Query != "" {
		routings, err := h.routingProvider.GetRoutingsForPattern(ctx, data.Query, h.maxHistory)
		if err == nil && len(routings) > 0 {
			return routings, nil
		}
	}

	// Fall back to recent routings for the session
	return h.routingProvider.GetRecentRoutings(ctx, data.SessionID, h.maxHistory)
}

// buildRoutingContent creates the routing history context block.
func (h *GuideRoutingCacheHook) buildRoutingContent(routings []RoutingDecision) string {
	var sb strings.Builder

	sb.WriteString("[ROUTING_HISTORY]\n")
	sb.WriteString("Recent routing decisions for consistency:\n\n")

	for i, r := range routings {
		sb.WriteString(fmt.Sprintf("### Routing %d", i+1))
		if r.Confidence > 0 {
			sb.WriteString(fmt.Sprintf(" (confidence: %.0f%%)", r.Confidence*100))
		}
		sb.WriteString("\n")

		sb.WriteString(fmt.Sprintf("**Target**: %s\n", r.TargetAgent))
		if r.Reason != "" {
			sb.WriteString(fmt.Sprintf("**Reason**: %s\n", r.Reason))
		}
		if !r.Timestamp.IsZero() {
			sb.WriteString(fmt.Sprintf("**When**: %s\n", r.Timestamp.Format(time.RFC822)))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Consider these past decisions when routing similar queries.\n")

	return sb.String()
}

// injectRoutingContext adds routing history to the prefetched content.
func (h *GuideRoutingCacheHook) injectRoutingContext(
	existing *ctxpkg.AugmentedQuery,
	routingContent string,
) *ctxpkg.AugmentedQuery {
	// Create new AugmentedQuery if none exists
	if existing == nil {
		existing = &ctxpkg.AugmentedQuery{
			OriginalQuery: "",
			Excerpts:      make([]ctxpkg.Excerpt, 0),
			Summaries:     make([]ctxpkg.Summary, 0),
		}
	}

	// Add routing history as a high-priority excerpt
	routingExcerpt := ctxpkg.Excerpt{
		Source:     "routing_history",
		Content:    routingContent,
		Confidence: 0.95, // High priority for routing context
		LineRange:  [2]int{0, 0},
		TokenCount: len(routingContent) / 4, // Rough estimate
	}

	// Prepend routing context to excerpts using race-safe copy pattern
	newExcerpts := make([]ctxpkg.Excerpt, len(existing.Excerpts)+1)
	newExcerpts[0] = routingExcerpt
	copy(newExcerpts[1:], existing.Excerpts)
	existing.Excerpts = newExcerpts

	return existing
}

// =============================================================================
// ToPromptHook Conversion
// =============================================================================

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *GuideRoutingCacheHook) ToPromptHook() *ctxpkg.PromptHook {
	return ctxpkg.NewPromptHook(
		h.Name(),
		h.Phase(),
		h.Priority(),
		func(ctx context.Context, data *ctxpkg.PromptHookData) (*ctxpkg.HookResult, error) {
			return h.Execute(ctx, data)
		},
	)
}
