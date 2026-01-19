// Package hooks provides AR.7.4: FailurePatternWarningHook - PrePrompt hook that
// queries Archivalist for similar past failures and injects warnings into agent context.
package hooks

import (
	"context"
	"fmt"
	"strings"

	ctxpkg "github.com/adalundhe/sylk/core/context"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// FailurePatternHookName is the unique identifier for this hook.
	FailurePatternHookName = "failure_pattern_warning"

	// MinRecurrenceForWarning is the minimum failure recurrence count to trigger warning.
	MinRecurrenceForWarning = 2

	// DefaultSimilarityThreshold is the default similarity threshold for failure matching.
	DefaultSimilarityThreshold = 0.7

	// MaxFailureWarnings is the maximum number of failure warnings to inject.
	MaxFailureWarnings = 3
)

// =============================================================================
// Failure Pattern Types
// =============================================================================

// FailurePattern represents a past failure pattern from Archivalist.
type FailurePattern struct {
	// ApproachSignature identifies the approach that failed.
	ApproachSignature string

	// ErrorPattern describes the failure pattern.
	ErrorPattern string

	// Resolution describes how the failure was resolved (if any).
	Resolution string

	// RecurrenceCount is how many times this failure has occurred.
	RecurrenceCount int

	// Similarity is how similar this pattern is to the current query (0.0-1.0).
	Similarity float64
}

// =============================================================================
// Failure Pattern Querier Interface
// =============================================================================

// FailurePatternQuerier is the interface for querying failure patterns.
type FailurePatternQuerier interface {
	// QuerySimilarFailures returns failure patterns similar to the given query.
	QuerySimilarFailures(ctx context.Context, query string, threshold float64) ([]FailurePattern, error)
}

// =============================================================================
// Failure Pattern Warning Hook
// =============================================================================

// FailurePatternWarningHookConfig holds configuration for the failure warning hook.
type FailurePatternWarningHookConfig struct {
	// FailureQuerier provides access to Archivalist failure memory.
	FailureQuerier FailurePatternQuerier

	// SimilarityThreshold is the minimum similarity for matching failures.
	// Default: 0.7
	SimilarityThreshold float64

	// MinRecurrence is the minimum recurrence count to trigger a warning.
	// Default: 2
	MinRecurrence int

	// MaxWarnings is the maximum number of warnings to inject.
	// Default: 3
	MaxWarnings int
}

// FailurePatternWarningHook injects warnings about past failures into agent context.
// It executes at HookPriorityNormal (50) in the PrePrompt phase.
type FailurePatternWarningHook struct {
	failureQuerier      FailurePatternQuerier
	similarityThreshold float64
	minRecurrence       int
	maxWarnings         int
	enabled             bool
}

// NewFailurePatternWarningHook creates a new failure pattern warning hook.
func NewFailurePatternWarningHook(config FailurePatternWarningHookConfig) *FailurePatternWarningHook {
	threshold := config.SimilarityThreshold
	if threshold <= 0 {
		threshold = DefaultSimilarityThreshold
	}

	minRecurrence := config.MinRecurrence
	if minRecurrence <= 0 {
		minRecurrence = MinRecurrenceForWarning
	}

	maxWarnings := config.MaxWarnings
	if maxWarnings <= 0 {
		maxWarnings = MaxFailureWarnings
	}

	return &FailurePatternWarningHook{
		failureQuerier:      config.FailureQuerier,
		similarityThreshold: threshold,
		minRecurrence:       minRecurrence,
		maxWarnings:         maxWarnings,
		enabled:             true,
	}
}

// =============================================================================
// Hook Interface Implementation
// =============================================================================

// Name returns the hook's unique identifier.
func (h *FailurePatternWarningHook) Name() string {
	return FailurePatternHookName
}

// Phase returns when this hook executes (PrePrompt).
func (h *FailurePatternWarningHook) Phase() ctxpkg.HookPhase {
	return ctxpkg.HookPhasePrePrompt
}

// Priority returns the execution order (Normal = 50).
func (h *FailurePatternWarningHook) Priority() int {
	return int(ctxpkg.HookPriorityNormal)
}

// Enabled returns whether this hook is currently active.
func (h *FailurePatternWarningHook) Enabled() bool {
	return h.enabled
}

// SetEnabled enables or disables the hook.
func (h *FailurePatternWarningHook) SetEnabled(enabled bool) {
	h.enabled = enabled
}

// =============================================================================
// Execution
// =============================================================================

// Execute queries for similar failures and injects warnings if found.
func (h *FailurePatternWarningHook) Execute(
	ctx context.Context,
	data *ctxpkg.PromptHookData,
) (*ctxpkg.HookResult, error) {
	// Skip if disabled or no querier
	if !h.enabled || h.failureQuerier == nil {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Skip if query is empty
	if data.Query == "" {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Query for similar failures
	failures, err := h.failureQuerier.QuerySimilarFailures(ctx, data.Query, h.similarityThreshold)
	if err != nil {
		// Log error but don't fail the hook
		return &ctxpkg.HookResult{
			Modified: false,
			Error:    err,
		}, nil
	}

	// Filter to failures with sufficient recurrence
	relevantFailures := h.filterRelevantFailures(failures)
	if len(relevantFailures) == 0 {
		return &ctxpkg.HookResult{Modified: false}, nil
	}

	// Build warning content
	warningContent := h.buildWarningContent(relevantFailures)

	// Inject warning into prefetched content
	data.PrefetchedContent = h.injectWarning(data.PrefetchedContent, warningContent)

	return &ctxpkg.HookResult{
		Modified:        true,
		ModifiedContent: data,
	}, nil
}

func (h *FailurePatternWarningHook) filterRelevantFailures(failures []FailurePattern) []FailurePattern {
	relevant := make([]FailurePattern, 0, len(failures))

	for _, f := range failures {
		if f.RecurrenceCount >= h.minRecurrence {
			relevant = append(relevant, f)
			if len(relevant) >= h.maxWarnings {
				break
			}
		}
	}

	return relevant
}

func (h *FailurePatternWarningHook) buildWarningContent(failures []FailurePattern) string {
	var sb strings.Builder

	sb.WriteString("[FAILURE_PATTERN_WARNING]\n")
	sb.WriteString("The following similar approaches have failed in the past:\n\n")

	for i, f := range failures {
		sb.WriteString(fmt.Sprintf("### Warning %d (occurred %d times, %.0f%% similar)\n",
			i+1, f.RecurrenceCount, f.Similarity*100))
		sb.WriteString(fmt.Sprintf("**Approach**: %s\n", f.ApproachSignature))
		sb.WriteString(fmt.Sprintf("**Error**: %s\n", f.ErrorPattern))

		if f.Resolution != "" {
			sb.WriteString(fmt.Sprintf("**Resolution**: %s\n", f.Resolution))
		}

		sb.WriteString("\n")
	}

	sb.WriteString("Consider alternative approaches or ensure mitigations are in place.\n")

	return sb.String()
}

func (h *FailurePatternWarningHook) injectWarning(
	existing *ctxpkg.AugmentedQuery,
	warning string,
) *ctxpkg.AugmentedQuery {
	// Create new AugmentedQuery if none exists
	if existing == nil {
		existing = &ctxpkg.AugmentedQuery{
			OriginalQuery: "",
			Excerpts:      make([]ctxpkg.Excerpt, 0),
			Summaries:     make([]ctxpkg.Summary, 0),
		}
	}

	// Add warning as a high-priority excerpt
	warningExcerpt := ctxpkg.Excerpt{
		Source:     "failure_memory",
		Content:    warning,
		Confidence: 1.0, // Warnings are high priority
		LineRange:  [2]int{0, 0},
		TokenCount: len(warning) / 4, // Rough estimate
	}

	// Prepend warning to excerpts
	existing.Excerpts = append([]ctxpkg.Excerpt{warningExcerpt}, existing.Excerpts...)

	return existing
}

// =============================================================================
// ToPromptHook Conversion
// =============================================================================

// ToPromptHook converts this hook to a generic PromptHook for registration.
func (h *FailurePatternWarningHook) ToPromptHook() *ctxpkg.PromptHook {
	return ctxpkg.NewPromptHook(
		h.Name(),
		h.Phase(),
		h.Priority(),
		func(ctx context.Context, data *ctxpkg.PromptHookData) (*ctxpkg.HookResult, error) {
			return h.Execute(ctx, data)
		},
	)
}
