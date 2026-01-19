// Package context provides types and utilities for adaptive retrieval context management.
// This file defines hook types for the hook system that intercepts LLM prompts/responses
// and tool calls to inject prefetched context, track episodes, and enforce pressure-driven eviction.
package context

import (
	"context"
	"fmt"
	"time"
)

// =============================================================================
// Hook Priority Constants
// =============================================================================

// HookPriority determines execution order (lower = earlier).
// Hooks execute in a priority-ordered pipeline from First=0 to Last=100,
// enabling multiple components to modify data without tight coupling.
type HookPriority int

const (
	// HookPriorityFirst executes first (e.g., prefetch injection, pressure eviction).
	HookPriorityFirst HookPriority = 0

	// HookPriorityEarly executes early (e.g., episode tracker initialization).
	HookPriorityEarly HookPriority = 25

	// HookPriorityNormal is the default execution priority.
	HookPriorityNormal HookPriority = 50

	// HookPriorityLate executes late (e.g., observation recording).
	HookPriorityLate HookPriority = 75

	// HookPriorityLast executes last (e.g., cleanup, final logging).
	HookPriorityLast HookPriority = 100
)

// =============================================================================
// Hook Phase Enum
// =============================================================================

// HookPhase identifies when a hook executes in the agent loop.
// The phases align with the LLM call and tool execution lifecycle.
type HookPhase int

const (
	// HookPhasePrePrompt executes before the LLM call.
	// Used for: injecting prefetched context, episode initialization, pressure eviction.
	HookPhasePrePrompt HookPhase = 0

	// HookPhasePostPrompt executes after the LLM response.
	// Used for: recording observations, updating access tracking.
	HookPhasePostPrompt HookPhase = 1

	// HookPhasePreTool executes before tool execution.
	// Used for: credential injection, tool validation.
	HookPhasePreTool HookPhase = 2

	// HookPhasePostTool executes after tool execution.
	// Used for: search observation, content promotion, credential cleanup.
	HookPhasePostTool HookPhase = 3
)

// String returns the string representation of the hook phase.
func (p HookPhase) String() string {
	switch p {
	case HookPhasePrePrompt:
		return "pre_prompt"
	case HookPhasePostPrompt:
		return "post_prompt"
	case HookPhasePreTool:
		return "pre_tool"
	case HookPhasePostTool:
		return "post_tool"
	default:
		return fmt.Sprintf("unknown_phase(%d)", p)
	}
}

// =============================================================================
// Prompt Hook Data
// =============================================================================

// Note: AugmentedQuery, Excerpt, and Summary types are defined in retrieval_types.go

// PromptHookData carries data through pre-prompt and post-prompt hooks.
// PrePrompt hooks can modify Query and PrefetchedContent; PostPrompt hooks
// receive the Response and can record observations.
type PromptHookData struct {
	// SessionID identifies the current session.
	SessionID string

	// AgentID is the unique identifier for the agent instance.
	AgentID string

	// AgentType is the type of agent (e.g., "librarian", "engineer").
	AgentType string

	// TurnNumber is the current turn in the conversation.
	TurnNumber int

	// ContextTokens is the current token count in context.
	ContextTokens int

	// ContextUsagePercent is the percentage of context window used (0.0-1.0).
	ContextUsagePercent float64

	// Query is the user's input query.
	Query string

	// PrefetchedContent contains speculatively prefetched context.
	// Set by SpeculativePrefetchHook if prefetch is ready.
	PrefetchedContent *AugmentedQuery

	// PressureLevel indicates the current resource pressure (0=Normal to 3=Critical).
	PressureLevel int

	// Timestamp is when this hook data was created.
	Timestamp time.Time
}

// =============================================================================
// Tool Call Hook Data
// =============================================================================

// ToolCallHookData carries data through pre-tool and post-tool hooks.
// PreTool hooks can modify ToolInput; PostTool hooks receive ToolOutput and Duration.
type ToolCallHookData struct {
	// SessionID identifies the current session.
	SessionID string

	// AgentID is the unique identifier for the agent instance.
	AgentID string

	// AgentType is the type of agent (e.g., "librarian", "engineer").
	AgentType string

	// TurnNumber is the current turn in the conversation.
	TurnNumber int

	// ToolName is the name of the tool being called.
	ToolName string

	// ToolInput contains the input parameters for the tool.
	ToolInput map[string]any

	// ToolOutput contains the output from tool execution (post-tool only).
	ToolOutput any

	// Duration is how long the tool execution took (post-tool only).
	Duration time.Duration

	// Error contains any error from tool execution (post-tool only).
	Error error

	// Timestamp is when this hook data was created.
	Timestamp time.Time
}

// =============================================================================
// Hook Result
// =============================================================================

// HookResult represents the outcome of a hook execution.
// Hooks can modify content, abort execution, or report errors.
type HookResult struct {
	// Modified indicates whether the hook modified the input data.
	Modified bool

	// ModifiedContent contains the modified data (type depends on hook type).
	ModifiedContent any

	// ShouldAbort indicates the pipeline should stop processing.
	ShouldAbort bool

	// AbortReason explains why the hook requested abort.
	AbortReason string

	// Error contains any error that occurred during hook execution.
	Error error
}

// =============================================================================
// Hook Function Types
// =============================================================================

// PromptHookFunc is the function signature for prompt hooks.
type PromptHookFunc func(ctx context.Context, data *PromptHookData) (*HookResult, error)

// ToolCallHookFunc is the function signature for tool call hooks.
type ToolCallHookFunc func(ctx context.Context, data *ToolCallHookData) (*HookResult, error)

// =============================================================================
// Hook Interface
// =============================================================================

// Hook is the base interface for all hooks.
// Implementations should embed either PromptHook or ToolCallHook.
type Hook interface {
	// Name returns the unique identifier for this hook.
	Name() string

	// Phase returns when this hook executes in the agent loop.
	Phase() HookPhase

	// Priority returns the execution order (lower = earlier).
	Priority() int

	// Enabled returns whether this hook is currently active.
	Enabled() bool
}

// =============================================================================
// Prompt Hook
// =============================================================================

// PromptHook handles pre-prompt and post-prompt hook execution.
// It wraps a PromptHookFunc with metadata for registration and ordering.
type PromptHook struct {
	name     string
	phase    HookPhase
	priority int
	enabled  bool
	handler  PromptHookFunc
}

// NewPromptHook creates a new PromptHook with the given parameters.
func NewPromptHook(name string, phase HookPhase, priority int, handler PromptHookFunc) *PromptHook {
	return &PromptHook{
		name:     name,
		phase:    phase,
		priority: priority,
		enabled:  true,
		handler:  handler,
	}
}

// Name returns the hook's unique identifier.
func (h *PromptHook) Name() string { return h.name }

// Phase returns when this hook executes.
func (h *PromptHook) Phase() HookPhase { return h.phase }

// Priority returns the execution order.
func (h *PromptHook) Priority() int { return h.priority }

// Enabled returns whether this hook is active.
func (h *PromptHook) Enabled() bool { return h.enabled }

// SetEnabled updates the hook's enabled state.
func (h *PromptHook) SetEnabled(enabled bool) { h.enabled = enabled }

// Execute runs the hook handler with the given context and data.
func (h *PromptHook) Execute(ctx context.Context, data *PromptHookData) (*HookResult, error) {
	if h.handler == nil {
		return &HookResult{}, nil
	}
	return h.handler(ctx, data)
}

// =============================================================================
// Tool Call Hook
// =============================================================================

// ToolCallHook handles pre-tool and post-tool hook execution.
// It wraps a ToolCallHookFunc with metadata for registration and ordering.
type ToolCallHook struct {
	name     string
	phase    HookPhase
	priority int
	enabled  bool
	handler  ToolCallHookFunc
}

// NewToolCallHook creates a new ToolCallHook with the given parameters.
func NewToolCallHook(name string, phase HookPhase, priority int, handler ToolCallHookFunc) *ToolCallHook {
	return &ToolCallHook{
		name:     name,
		phase:    phase,
		priority: priority,
		enabled:  true,
		handler:  handler,
	}
}

// Name returns the hook's unique identifier.
func (h *ToolCallHook) Name() string { return h.name }

// Phase returns when this hook executes.
func (h *ToolCallHook) Phase() HookPhase { return h.phase }

// Priority returns the execution order.
func (h *ToolCallHook) Priority() int { return h.priority }

// Enabled returns whether this hook is active.
func (h *ToolCallHook) Enabled() bool { return h.enabled }

// SetEnabled updates the hook's enabled state.
func (h *ToolCallHook) SetEnabled(enabled bool) { h.enabled = enabled }

// Execute runs the hook handler with the given context and data.
func (h *ToolCallHook) Execute(ctx context.Context, data *ToolCallHookData) (*HookResult, error) {
	if h.handler == nil {
		return &HookResult{}, nil
	}
	return h.handler(ctx, data)
}

// =============================================================================
// Hook Registry Interface
// =============================================================================

// HookRegistry manages registration and retrieval of hooks.
// Implementations should maintain hooks sorted by priority for each phase.
type HookRegistry interface {
	// RegisterPromptHook adds a prompt hook to the registry.
	// Returns an error if a hook with the same name already exists.
	RegisterPromptHook(hook *PromptHook) error

	// RegisterToolCallHook adds a tool call hook to the registry.
	// Returns an error if a hook with the same name already exists.
	RegisterToolCallHook(hook *ToolCallHook) error

	// GetPromptHooks returns all prompt hooks for the given phase, sorted by priority.
	GetPromptHooks(phase HookPhase) []*PromptHook

	// GetToolCallHooks returns all tool call hooks for the given phase, sorted by priority.
	GetToolCallHooks(phase HookPhase) []*ToolCallHook

	// UnregisterHook removes a hook by name from all registries.
	// Returns an error if the hook is not found.
	UnregisterHook(name string) error
}
