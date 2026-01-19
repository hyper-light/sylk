// Package hooks provides AR.7.1: SpeculativePrefetchHook - PrePrompt hook that injects
// speculatively prefetched context into the LLM prompt.
package hooks

import (
	"context"
	"time"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// PrefetchHookName is the unique identifier for this hook.
	PrefetchHookName = "speculative_prefetch"

	// PrefetchReadyTimeout is how long to wait for prefetch results.
	PrefetchReadyTimeout = 10 * time.Millisecond

	// FallbackTokenBudget is the default token budget for fallback augmentation.
	FallbackTokenBudget = 2000
)

// knowledgeAgents lists agents that use speculative prefetch.
var knowledgeAgents = map[string]bool{
	"librarian":   true,
	"archivalist": true,
	"academic":    true,
	"architect":   true,
	"guide":       true,
}

// =============================================================================
// Configuration
// =============================================================================

// PrefetchHookConfig holds configuration for the prefetch injection hook.
type PrefetchHookConfig struct {
	// Prefetcher provides access to in-flight prefetch futures.
	Prefetcher *ctxpkg.SpeculativePrefetcher

	// Augmenter provides fallback synchronous augmentation.
	Augmenter *ctxpkg.QueryAugmenter

	// ReadyTimeout is how long to wait for prefetch results.
	ReadyTimeout time.Duration

	// FallbackBudget is the token budget for fallback augmentation.
	FallbackBudget int
}

// =============================================================================
// SpeculativePrefetchHook
// =============================================================================

// SpeculativePrefetchHook injects speculatively prefetched context into prompts.
// It executes at HookPriorityFirst (0) to ensure context is available for other hooks.
type SpeculativePrefetchHook struct {
	prefetcher     *ctxpkg.SpeculativePrefetcher
	augmenter      *ctxpkg.QueryAugmenter
	readyTimeout   time.Duration
	fallbackBudget int
	enabled        bool
}

// NewSpeculativePrefetchHook creates a new prefetch injection hook.
func NewSpeculativePrefetchHook(config PrefetchHookConfig) *SpeculativePrefetchHook {
	readyTimeout := config.ReadyTimeout
	if readyTimeout <= 0 {
		readyTimeout = PrefetchReadyTimeout
	}

	fallbackBudget := config.FallbackBudget
	if fallbackBudget <= 0 {
		fallbackBudget = FallbackTokenBudget
	}

	return &SpeculativePrefetchHook{
		prefetcher:     config.Prefetcher,
		augmenter:      config.Augmenter,
		readyTimeout:   readyTimeout,
		fallbackBudget: fallbackBudget,
		enabled:        true,
	}
}

// =============================================================================
// Hook Interface Implementation
// =============================================================================

// Name returns the hook's unique identifier.
func (h *SpeculativePrefetchHook) Name() string {
	return PrefetchHookName
}

// Phase returns when this hook executes (PrePrompt).
func (h *SpeculativePrefetchHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePrePrompt
}

// Priority returns the execution order (First = 0).
func (h *SpeculativePrefetchHook) Priority() int {
	return int(ctxpkg.HookPriorityFirst)
}

// Enabled returns whether this hook is currently active.
func (h *SpeculativePrefetchHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *SpeculativePrefetchHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// =============================================================================
// Execution
// =============================================================================

// Execute runs the prefetch injection hook.
// Returns a HookResult with the prefetched or fallback-augmented content.
func (h *SpeculativePrefetchHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	// Skip if not a knowledge agent
	if !h.isKnowledgeAgent(data.AgentType) {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if disabled or under high pressure
	if !h.enabled || data.PressureLevel >= 2 {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Try to get prefetched content
	augmented := h.getPrefetchedContent(data.Query)

	// Fallback to synchronous augmentation if prefetch not ready
	if augmented == nil {
		augmented = h.fallbackAugment(ctx, data.Query)
	}

	// Set the prefetched content in the data
	if augmented != nil && len(augmented.Excerpts) > 0 {
		data.PrefetchedContent = augmented
		return &ctxpkg.HookResult{
			Modified:        true,
			ModifiedContent: data,
		}, nil
	}

	return &ctxpkg.HookResult{Modified: false}, nil
}

func (h *SpeculativePrefetchHook) isKnowledgeAgent(agentType string) bool {
	return knowledgeAgents[agentType]
}

func (h *SpeculativePrefetchHook) getPrefetchedContent(query string) *ctxpkg.AugmentedQuery {
	if h.prefetcher == nil {
		return nil
	}

	// Check for in-flight prefetch
	future := h.prefetcher.GetInflight(query)
	if future == nil {
		return nil
	}

	// Wait briefly for results
	return future.GetIfReady(h.readyTimeout)
}

func (h *SpeculativePrefetchHook) fallbackAugment(
	ctx context.Context,
	query string,
) *ctxpkg.AugmentedQuery {
	if h.augmenter == nil {
		return nil
	}

	result, err := h.augmenter.Augment(ctx, query, h.fallbackBudget)
	if err != nil {
		return nil
	}

	return result
}

// =============================================================================
// ToPromptHook Conversion
// =============================================================================

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *SpeculativePrefetchHook) ToPromptHook() *ctxpkg.PromptHook {
	return ctxpkg.NewPromptHook(
		h.Name(),
		h.Phase(),
		h.Priority(),
		func(ctx context.Context, data *ctxpkg.PromptHookData) (*ctxpkg.HookResult, error) {
			return h.Execute(ctx, data)
		},
	)
}
