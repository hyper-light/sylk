package skills

import (
	"context"
	"sync"
)

// =============================================================================
// LLM Hook System
// =============================================================================
//
// Hooks provide injection points during LLM interactions:
// - PrePrompt: Modify/augment prompt before sending to LLM
// - PostPrompt: Process LLM response before returning
// - PreToolCall: Intercept/modify tool calls before execution
// - PostToolCall: Process tool results before returning to LLM
//
// Hooks are executed in registration order. Each hook can:
// - Pass through unchanged
// - Modify the data
// - Short-circuit (cancel further processing)

// HookPhase identifies when a hook runs
type HookPhase string

const (
	HookPhasePrePrompt    HookPhase = "pre_prompt"
	HookPhasePostPrompt   HookPhase = "post_prompt"
	HookPhasePreToolCall  HookPhase = "pre_tool_call"
	HookPhasePostToolCall HookPhase = "post_tool_call"
)

// HookPriority determines execution order (higher = earlier)
type HookPriority int

const (
	HookPriorityLow    HookPriority = 10
	HookPriorityNormal HookPriority = 50
	HookPriorityHigh   HookPriority = 100
)

// =============================================================================
// Hook Data Types
// =============================================================================

// PromptHookData contains data for pre/post prompt hooks
type PromptHookData struct {
	// Input (read-only for context)
	AgentID        string
	ConversationID string
	TurnNumber     int

	// Modifiable content
	SystemPrompt string
	UserMessage  string
	Messages     []MessageContent // Full message history

	// For PostPrompt only
	Response   string
	TokensUsed int
	StopReason string

	// Metadata for hook communication
	Metadata map[string]any
}

// MessageContent represents a single message in the conversation
type MessageContent struct {
	Role    string // "user", "assistant", "system"
	Content string
}

// ToolCallHookData contains data for pre/post tool call hooks
type ToolCallHookData struct {
	// Tool identification
	ToolName string
	ToolID   string

	// Context
	AgentID        string
	ConversationID string

	// Modifiable content
	Input map[string]any // Tool input parameters

	// For PostToolCall only
	Output any
	Error  error

	// Metadata for hook communication
	Metadata map[string]any
}

// HookResult indicates how to proceed after a hook
type HookResult struct {
	// Continue indicates whether to continue to the next hook
	Continue bool

	// Modified data (nil means use original)
	ModifiedPromptData   *PromptHookData
	ModifiedToolCallData *ToolCallHookData

	// Error to propagate (stops hook chain)
	Error error

	// Inject additional context (appended to system prompt)
	InjectContext string

	// Skip the actual LLM/tool call (use provided response)
	SkipExecution bool
	SkipResponse  string
}

// =============================================================================
// Hook Interfaces
// =============================================================================

// PromptHook processes prompt-related events
type PromptHook interface {
	// Name returns a unique identifier for this hook
	Name() string

	// Phase returns when this hook runs
	Phase() HookPhase

	// Priority returns execution priority
	Priority() HookPriority

	// Execute runs the hook
	Execute(ctx context.Context, data *PromptHookData) HookResult
}

// ToolCallHook processes tool call events
type ToolCallHook interface {
	// Name returns a unique identifier for this hook
	Name() string

	// Phase returns when this hook runs
	Phase() HookPhase

	// Priority returns execution priority
	Priority() HookPriority

	// Execute runs the hook
	Execute(ctx context.Context, data *ToolCallHookData) HookResult
}

// =============================================================================
// Functional Hook Types
// =============================================================================

// PromptHookFunc is a function that implements PromptHook
type PromptHookFunc func(ctx context.Context, data *PromptHookData) HookResult

// ToolCallHookFunc is a function that implements ToolCallHook
type ToolCallHookFunc func(ctx context.Context, data *ToolCallHookData) HookResult

// funcPromptHook wraps a function as a PromptHook
type funcPromptHook struct {
	name     string
	phase    HookPhase
	priority HookPriority
	fn       PromptHookFunc
}

func (h *funcPromptHook) Name() string           { return h.name }
func (h *funcPromptHook) Phase() HookPhase       { return h.phase }
func (h *funcPromptHook) Priority() HookPriority { return h.priority }
func (h *funcPromptHook) Execute(ctx context.Context, data *PromptHookData) HookResult {
	return h.fn(ctx, data)
}

// funcToolCallHook wraps a function as a ToolCallHook
type funcToolCallHook struct {
	name     string
	phase    HookPhase
	priority HookPriority
	fn       ToolCallHookFunc
}

func (h *funcToolCallHook) Name() string           { return h.name }
func (h *funcToolCallHook) Phase() HookPhase       { return h.phase }
func (h *funcToolCallHook) Priority() HookPriority { return h.priority }
func (h *funcToolCallHook) Execute(ctx context.Context, data *ToolCallHookData) HookResult {
	return h.fn(ctx, data)
}

// =============================================================================
// Hook Registry
// =============================================================================

// HookRegistry manages all registered hooks
type HookRegistry struct {
	mu sync.RWMutex

	// Prompt hooks by phase
	prePromptHooks  []PromptHook
	postPromptHooks []PromptHook

	// Tool call hooks by phase
	preToolCallHooks  []ToolCallHook
	postToolCallHooks []ToolCallHook

	// Index by name for management
	promptHooksByName   map[string]PromptHook
	toolCallHooksByName map[string]ToolCallHook
}

// NewHookRegistry creates a new hook registry
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		prePromptHooks:      make([]PromptHook, 0),
		postPromptHooks:     make([]PromptHook, 0),
		preToolCallHooks:    make([]ToolCallHook, 0),
		postToolCallHooks:   make([]ToolCallHook, 0),
		promptHooksByName:   make(map[string]PromptHook),
		toolCallHooksByName: make(map[string]ToolCallHook),
	}
}

// RegisterPromptHook adds a prompt hook
func (r *HookRegistry) RegisterPromptHook(hook PromptHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.promptHooksByName[hook.Name()] = hook

	// Add to appropriate phase list
	switch hook.Phase() {
	case HookPhasePrePrompt:
		r.prePromptHooks = insertPromptHookSorted(r.prePromptHooks, hook)
	case HookPhasePostPrompt:
		r.postPromptHooks = insertPromptHookSorted(r.postPromptHooks, hook)
	}
}

// RegisterToolCallHook adds a tool call hook
func (r *HookRegistry) RegisterToolCallHook(hook ToolCallHook) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.toolCallHooksByName[hook.Name()] = hook

	// Add to appropriate phase list
	switch hook.Phase() {
	case HookPhasePreToolCall:
		r.preToolCallHooks = insertToolCallHookSorted(r.preToolCallHooks, hook)
	case HookPhasePostToolCall:
		r.postToolCallHooks = insertToolCallHookSorted(r.postToolCallHooks, hook)
	}
}

// RegisterPrePromptHook registers a function as a pre-prompt hook
func (r *HookRegistry) RegisterPrePromptHook(name string, priority HookPriority, fn PromptHookFunc) {
	r.RegisterPromptHook(&funcPromptHook{
		name:     name,
		phase:    HookPhasePrePrompt,
		priority: priority,
		fn:       fn,
	})
}

// RegisterPostPromptHook registers a function as a post-prompt hook
func (r *HookRegistry) RegisterPostPromptHook(name string, priority HookPriority, fn PromptHookFunc) {
	r.RegisterPromptHook(&funcPromptHook{
		name:     name,
		phase:    HookPhasePostPrompt,
		priority: priority,
		fn:       fn,
	})
}

// RegisterPreToolCallHook registers a function as a pre-tool-call hook
func (r *HookRegistry) RegisterPreToolCallHook(name string, priority HookPriority, fn ToolCallHookFunc) {
	r.RegisterToolCallHook(&funcToolCallHook{
		name:     name,
		phase:    HookPhasePreToolCall,
		priority: priority,
		fn:       fn,
	})
}

// RegisterPostToolCallHook registers a function as a post-tool-call hook
func (r *HookRegistry) RegisterPostToolCallHook(name string, priority HookPriority, fn ToolCallHookFunc) {
	r.RegisterToolCallHook(&funcToolCallHook{
		name:     name,
		phase:    HookPhasePostToolCall,
		priority: priority,
		fn:       fn,
	})
}

// UnregisterPromptHook removes a prompt hook
func (r *HookRegistry) UnregisterPromptHook(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, ok := r.promptHooksByName[name]
	if !ok {
		return false
	}

	delete(r.promptHooksByName, name)

	// Remove from phase list
	switch hook.Phase() {
	case HookPhasePrePrompt:
		r.prePromptHooks = removePromptHook(r.prePromptHooks, name)
	case HookPhasePostPrompt:
		r.postPromptHooks = removePromptHook(r.postPromptHooks, name)
	}

	return true
}

// UnregisterToolCallHook removes a tool call hook
func (r *HookRegistry) UnregisterToolCallHook(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, ok := r.toolCallHooksByName[name]
	if !ok {
		return false
	}

	delete(r.toolCallHooksByName, name)

	// Remove from phase list
	switch hook.Phase() {
	case HookPhasePreToolCall:
		r.preToolCallHooks = removeToolCallHook(r.preToolCallHooks, name)
	case HookPhasePostToolCall:
		r.postToolCallHooks = removeToolCallHook(r.postToolCallHooks, name)
	}

	return true
}

// =============================================================================
// Hook Execution
// =============================================================================

// ExecutePrePromptHooks runs all pre-prompt hooks
func (r *HookRegistry) ExecutePrePromptHooks(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
	r.mu.RLock()
	hooks := make([]PromptHook, len(r.prePromptHooks))
	copy(hooks, r.prePromptHooks)
	r.mu.RUnlock()

	return r.executePromptHooks(ctx, hooks, data)
}

// ExecutePostPromptHooks runs all post-prompt hooks
func (r *HookRegistry) ExecutePostPromptHooks(ctx context.Context, data *PromptHookData) (*PromptHookData, error) {
	r.mu.RLock()
	hooks := make([]PromptHook, len(r.postPromptHooks))
	copy(hooks, r.postPromptHooks)
	r.mu.RUnlock()

	return r.executePromptHooks(ctx, hooks, data)
}

// ExecutePreToolCallHooks runs all pre-tool-call hooks
func (r *HookRegistry) ExecutePreToolCallHooks(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, HookResult, error) {
	r.mu.RLock()
	hooks := make([]ToolCallHook, len(r.preToolCallHooks))
	copy(hooks, r.preToolCallHooks)
	r.mu.RUnlock()

	return r.executeToolCallHooks(ctx, hooks, data)
}

// ExecutePostToolCallHooks runs all post-tool-call hooks
func (r *HookRegistry) ExecutePostToolCallHooks(ctx context.Context, data *ToolCallHookData) (*ToolCallHookData, HookResult, error) {
	r.mu.RLock()
	hooks := make([]ToolCallHook, len(r.postToolCallHooks))
	copy(hooks, r.postToolCallHooks)
	r.mu.RUnlock()

	return r.executeToolCallHooks(ctx, hooks, data)
}

// executePromptHooks runs a chain of prompt hooks
func (r *HookRegistry) executePromptHooks(ctx context.Context, hooks []PromptHook, data *PromptHookData) (*PromptHookData, error) {
	current := data
	var injectedContext string

	for _, hook := range hooks {
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		default:
		}

		result := hook.Execute(ctx, current)

		if result.Error != nil {
			return current, result.Error
		}

		if result.ModifiedPromptData != nil {
			current = result.ModifiedPromptData
		}

		if result.InjectContext != "" {
			injectedContext += "\n" + result.InjectContext
		}

		if !result.Continue {
			break
		}
	}

	// Apply injected context to system prompt
	if injectedContext != "" {
		current.SystemPrompt += injectedContext
	}

	return current, nil
}

// executeToolCallHooks runs a chain of tool call hooks
func (r *HookRegistry) executeToolCallHooks(ctx context.Context, hooks []ToolCallHook, data *ToolCallHookData) (*ToolCallHookData, HookResult, error) {
	current := data
	var lastResult HookResult

	for _, hook := range hooks {
		select {
		case <-ctx.Done():
			return current, lastResult, ctx.Err()
		default:
		}

		result := hook.Execute(ctx, current)
		lastResult = result

		if result.Error != nil {
			return current, result, result.Error
		}

		if result.ModifiedToolCallData != nil {
			current = result.ModifiedToolCallData
		}

		if result.SkipExecution {
			return current, result, nil
		}

		if !result.Continue {
			break
		}
	}

	return current, lastResult, nil
}

// =============================================================================
// Helper Functions
// =============================================================================

// insertPromptHookSorted inserts a hook maintaining priority order (highest first)
func insertPromptHookSorted(hooks []PromptHook, hook PromptHook) []PromptHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

// insertToolCallHookSorted inserts a hook maintaining priority order (highest first)
func insertToolCallHookSorted(hooks []ToolCallHook, hook ToolCallHook) []ToolCallHook {
	i := 0
	for i < len(hooks) && hooks[i].Priority() >= hook.Priority() {
		i++
	}
	hooks = append(hooks, nil)
	copy(hooks[i+1:], hooks[i:])
	hooks[i] = hook
	return hooks
}

// removePromptHook removes a hook by name
func removePromptHook(hooks []PromptHook, name string) []PromptHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

// removeToolCallHook removes a hook by name
func removeToolCallHook(hooks []ToolCallHook, name string) []ToolCallHook {
	for i, h := range hooks {
		if h.Name() == name {
			return append(hooks[:i], hooks[i+1:]...)
		}
	}
	return hooks
}

// =============================================================================
// Hook Stats
// =============================================================================

// HookStats contains statistics about registered hooks
type HookStats struct {
	PrePromptHooks    int `json:"pre_prompt_hooks"`
	PostPromptHooks   int `json:"post_prompt_hooks"`
	PreToolCallHooks  int `json:"pre_tool_call_hooks"`
	PostToolCallHooks int `json:"post_tool_call_hooks"`
	TotalHooks        int `json:"total_hooks"`
}

// Stats returns hook statistics
func (r *HookRegistry) Stats() HookStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return HookStats{
		PrePromptHooks:    len(r.prePromptHooks),
		PostPromptHooks:   len(r.postPromptHooks),
		PreToolCallHooks:  len(r.preToolCallHooks),
		PostToolCallHooks: len(r.postToolCallHooks),
		TotalHooks:        len(r.promptHooksByName) + len(r.toolCallHooksByName),
	}
}

// =============================================================================
// Common Hook Implementations
// =============================================================================

// NewContextInjectionHook creates a hook that injects context into the system prompt
func NewContextInjectionHook(name string, priority HookPriority, contextFn func(data *PromptHookData) string) PromptHook {
	return &funcPromptHook{
		name:     name,
		phase:    HookPhasePrePrompt,
		priority: priority,
		fn: func(ctx context.Context, data *PromptHookData) HookResult {
			injected := contextFn(data)
			return HookResult{
				Continue:      true,
				InjectContext: injected,
			}
		},
	}
}

// NewToolCallLoggerHook creates a hook that logs tool calls
func NewToolCallLoggerHook(name string, logFn func(data *ToolCallHookData)) ToolCallHook {
	return &funcToolCallHook{
		name:     name,
		phase:    HookPhasePreToolCall,
		priority: HookPriorityLow, // Run after other hooks
		fn: func(ctx context.Context, data *ToolCallHookData) HookResult {
			logFn(data)
			return HookResult{Continue: true}
		},
	}
}

// NewToolCallValidatorHook creates a hook that validates tool calls
func NewToolCallValidatorHook(name string, validateFn func(data *ToolCallHookData) error) ToolCallHook {
	return &funcToolCallHook{
		name:     name,
		phase:    HookPhasePreToolCall,
		priority: HookPriorityHigh, // Run first
		fn: func(ctx context.Context, data *ToolCallHookData) HookResult {
			if err := validateFn(data); err != nil {
				return HookResult{
					Continue: false,
					Error:    err,
				}
			}
			return HookResult{Continue: true}
		},
	}
}

// NewResponseTransformerHook creates a hook that transforms LLM responses
func NewResponseTransformerHook(name string, priority HookPriority, transformFn func(response string) string) PromptHook {
	return &funcPromptHook{
		name:     name,
		phase:    HookPhasePostPrompt,
		priority: priority,
		fn: func(ctx context.Context, data *PromptHookData) HookResult {
			modified := *data
			modified.Response = transformFn(data.Response)
			return HookResult{
				Continue:           true,
				ModifiedPromptData: &modified,
			}
		},
	}
}
