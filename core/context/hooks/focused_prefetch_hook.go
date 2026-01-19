// Package hooks provides AR.7.8: Focused Prefetch Hook - PrePrompt hook for pipeline
// agents that performs limited, task-focused prefetch with smaller token budgets.
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
	// FocusedPrefetchHookName is the unique identifier for this hook.
	FocusedPrefetchHookName = "focused_prefetch"

	// DefaultFocusedMaxTokens is the default maximum tokens for focused prefetch.
	// This is much smaller than knowledge agents (which typically use 4000-8000).
	DefaultFocusedMaxTokens = 1000
)

// =============================================================================
// Pipeline Agent Types
// =============================================================================

// pipelineAgentTypes defines the agent types that receive focused prefetch.
var pipelineAgentTypes = map[string]bool{
	"engineer":  true,
	"designer":  true,
	"inspector": true,
	"tester":    true,
}

// =============================================================================
// Focused Augmenter Interface
// =============================================================================

// FocusedAugmenter provides query augmentation with token budget constraints.
type FocusedAugmenter interface {
	// AugmentWithBudget augments a query with a specific token budget.
	AugmentWithBudget(
		ctx context.Context,
		query string,
		maxTokens int,
		scope string,
	) (*ctxpkg.AugmentedQuery, error)
}

// =============================================================================
// Focused Prefetch Hook
// =============================================================================

// FocusedPrefetchHookConfig holds configuration for the focused prefetch hook.
type FocusedPrefetchHookConfig struct {
	// Augmenter provides query augmentation.
	Augmenter FocusedAugmenter

	// MaxTokens is the maximum tokens for focused prefetch.
	// Default: 1000
	MaxTokens int
}

// FocusedPrefetchHook performs limited prefetch for pipeline agents.
// It executes at HookPriorityEarly (25) in the PrePrompt phase.
type FocusedPrefetchHook struct {
	augmenter FocusedAugmenter
	maxTokens int
	enabled   bool
}

// NewFocusedPrefetchHook creates a new focused prefetch hook.
func NewFocusedPrefetchHook(config FocusedPrefetchHookConfig) *FocusedPrefetchHook {
	maxTokens := config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultFocusedMaxTokens
	}

	return &FocusedPrefetchHook{
		augmenter: config.Augmenter,
		maxTokens: maxTokens,
		enabled:   true,
	}
}

// =============================================================================
// Hook Interface Implementation
// =============================================================================

// Name returns the hook's unique identifier.
func (h *FocusedPrefetchHook) Name() string {
	return FocusedPrefetchHookName
}

// Phase returns when this hook executes (PrePrompt).
func (h *FocusedPrefetchHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePrePrompt
}

// Priority returns the execution order (Early = 25).
func (h *FocusedPrefetchHook) Priority() int {
	return int(ctxpkg.HookPriorityEarly)
}

// Enabled returns whether this hook is currently active.
func (h *FocusedPrefetchHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *FocusedPrefetchHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// GetMaxTokens returns the configured maximum tokens.
func (h *FocusedPrefetchHook) GetMaxTokens() int {
	return h.maxTokens
}

// =============================================================================
// Execution
// =============================================================================

// Execute performs focused prefetch for pipeline agents.
func (h *FocusedPrefetchHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	// Skip if disabled or no augmenter
	if !h.enabled || h.augmenter == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Only apply to pipeline agents
	if !h.isPipelineAgent(data.AgentType) {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if query is empty
	if data.Query == "" {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if already has prefetched content
	if data.PrefetchedContent != nil && len(data.PrefetchedContent.Excerpts) > 0 {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Determine scope from agent type
	scope := h.getScopeForAgent(data.AgentType)

	// Perform focused augmentation
	augmented, err := h.augmenter.AugmentWithBudget(ctx, data.Query, h.maxTokens, scope)
	if err != nil {
		return &ctxpkg.HookResult{Modified: false, Error: err}, nil
	}

	// Skip if no results
	if augmented == nil || h.isEmptyAugmentation(augmented) {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Set the prefetched content
	data.PrefetchedContent = augmented

	return &ctxpkg.HookResult{
		Modified:        true,
		ModifiedContent: data,
	}, nil
}

// isPipelineAgent checks if the agent type is a pipeline agent.
func (h *FocusedPrefetchHook) isPipelineAgent(agentType string) bool {
	return pipelineAgentTypes[strings.ToLower(agentType)]
}

// getScopeForAgent returns the search scope for a given agent type.
func (h *FocusedPrefetchHook) getScopeForAgent(agentType string) string {
	normalized := strings.ToLower(agentType)

	// Map agent types to appropriate scopes
	switch normalized {
	case "engineer":
		return "implementation"
	case "designer":
		return "design"
	case "inspector":
		return "review"
	case "tester":
		return "testing"
	default:
		return "general"
	}
}

// isEmptyAugmentation checks if the augmented query has no useful content.
func (h *FocusedPrefetchHook) isEmptyAugmentation(aq *ctxpkg.AugmentedQuery) bool {
	return len(aq.Excerpts) == 0 && len(aq.Summaries) == 0
}

// =============================================================================
// ToPromptHook Conversion
// =============================================================================

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *FocusedPrefetchHook) ToPromptHook() *ctxpkg.PromptHook {
	return ctxpkg.NewPromptHook(
		h.Name(),
		h.Phase(),
		h.Priority(),
		func(ctx context.Context, data *ctxpkg.PromptHookData) (*ctxpkg.HookResult, error) {
			return h.Execute(ctx, data)
		},
	)
}
