// Package context provides AR.9.1: Hook Registry - central registry for all
// adaptive retrieval hooks with agent-specific filtering and priority ordering.
package context

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// GlobalAgentType matches all agents.
	GlobalAgentType = "*"
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrHookAlreadyExists is returned when registering a hook that already exists.
	ErrHookAlreadyExists = errors.New("hook already exists")

	// ErrHookNotFound is returned when unregistering a hook that doesn't exist.
	ErrHookNotFound = errors.New("hook not found")
)

// =============================================================================
// Adaptive Hook Registry
// =============================================================================

// AdaptiveHookRegistry manages registration and retrieval of adaptive retrieval hooks.
// It supports agent-specific hook targeting and priority-ordered execution.
type AdaptiveHookRegistry struct {
	mu sync.RWMutex

	// Agent-specific prompt hooks: agentType -> phase -> hooks
	promptHooks map[string]map[HookPhase][]*PromptHook

	// Agent-specific tool call hooks: agentType -> phase -> hooks
	toolCallHooks map[string]map[HookPhase][]*ToolCallHook

	// Hook lookup by name for unregistration
	promptHooksByName   map[string]*PromptHook
	toolCallHooksByName map[string]*ToolCallHook

	// Track which agent types each hook is registered for
	hookAgentTypes map[string][]string
}

// NewAdaptiveHookRegistry creates a new adaptive hook registry.
func NewAdaptiveHookRegistry() *AdaptiveHookRegistry {
	return &AdaptiveHookRegistry{
		promptHooks:         make(map[string]map[HookPhase][]*PromptHook),
		toolCallHooks:       make(map[string]map[HookPhase][]*ToolCallHook),
		promptHooksByName:   make(map[string]*PromptHook),
		toolCallHooksByName: make(map[string]*ToolCallHook),
		hookAgentTypes:      make(map[string][]string),
	}
}

// =============================================================================
// HookRegistry Interface Implementation
// =============================================================================

// RegisterPromptHook registers a prompt hook for all agents.
func (r *AdaptiveHookRegistry) RegisterPromptHook(hook *PromptHook) error {
	return r.RegisterPromptHookForAgent(GlobalAgentType, hook)
}

// RegisterToolCallHook registers a tool call hook for all agents.
func (r *AdaptiveHookRegistry) RegisterToolCallHook(hook *ToolCallHook) error {
	return r.RegisterToolCallHookForAgent(GlobalAgentType, hook)
}

// GetPromptHooks returns all prompt hooks for the given phase for all agents.
func (r *AdaptiveHookRegistry) GetPromptHooks(phase HookPhase) []*PromptHook {
	return r.GetPromptHooksForAgent(GlobalAgentType, phase)
}

// GetToolCallHooks returns all tool call hooks for the given phase for all agents.
func (r *AdaptiveHookRegistry) GetToolCallHooks(phase HookPhase) []*ToolCallHook {
	return r.GetToolCallHooksForAgent(GlobalAgentType, phase)
}

// UnregisterHook removes a hook by name from all registries.
func (r *AdaptiveHookRegistry) UnregisterHook(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Try to find and remove prompt hook
	if _, exists := r.promptHooksByName[name]; exists {
		r.removePromptHook(name)
		return nil
	}

	// Try to find and remove tool call hook
	if _, exists := r.toolCallHooksByName[name]; exists {
		r.removeToolCallHook(name)
		return nil
	}

	return ErrHookNotFound
}

// =============================================================================
// Agent-Specific Registration
// =============================================================================

// RegisterPromptHookForAgent registers a prompt hook for a specific agent type.
func (r *AdaptiveHookRegistry) RegisterPromptHookForAgent(agentType string, hook *PromptHook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate
	if _, exists := r.promptHooksByName[hook.Name()]; exists {
		return ErrHookAlreadyExists
	}

	// Initialize agent map if needed
	if r.promptHooks[agentType] == nil {
		r.promptHooks[agentType] = make(map[HookPhase][]*PromptHook)
	}

	// Get or initialize phase slice
	phase := hook.Phase()
	hooks := r.promptHooks[agentType][phase]
	hooks = r.insertPromptHookSorted(hooks, hook)
	r.promptHooks[agentType][phase] = hooks

	// Track hook by name and agent type
	r.promptHooksByName[hook.Name()] = hook
	r.hookAgentTypes[hook.Name()] = append(r.hookAgentTypes[hook.Name()], agentType)

	return nil
}

// RegisterToolCallHookForAgent registers a tool call hook for a specific agent type.
func (r *AdaptiveHookRegistry) RegisterToolCallHookForAgent(agentType string, hook *ToolCallHook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate
	if _, exists := r.toolCallHooksByName[hook.Name()]; exists {
		return ErrHookAlreadyExists
	}

	// Initialize agent map if needed
	if r.toolCallHooks[agentType] == nil {
		r.toolCallHooks[agentType] = make(map[HookPhase][]*ToolCallHook)
	}

	// Get or initialize phase slice
	phase := hook.Phase()
	hooks := r.toolCallHooks[agentType][phase]
	hooks = r.insertToolCallHookSorted(hooks, hook)
	r.toolCallHooks[agentType][phase] = hooks

	// Track hook by name and agent type
	r.toolCallHooksByName[hook.Name()] = hook
	r.hookAgentTypes[hook.Name()] = append(r.hookAgentTypes[hook.Name()], agentType)

	return nil
}

// RegisterGlobalPromptHook registers a prompt hook for all agents.
func (r *AdaptiveHookRegistry) RegisterGlobalPromptHook(hook *PromptHook) error {
	return r.RegisterPromptHookForAgent(GlobalAgentType, hook)
}

// RegisterGlobalToolCallHook registers a tool call hook for all agents.
func (r *AdaptiveHookRegistry) RegisterGlobalToolCallHook(hook *ToolCallHook) error {
	return r.RegisterToolCallHookForAgent(GlobalAgentType, hook)
}

// RegisterPromptHookForAgents registers a prompt hook for multiple agent types.
// This allows the same hook to be registered for multiple agents at once.
func (r *AdaptiveHookRegistry) RegisterPromptHookForAgents(agentTypes []string, hook *PromptHook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate
	if _, exists := r.promptHooksByName[hook.Name()]; exists {
		return ErrHookAlreadyExists
	}

	// Register for each agent type
	phase := hook.Phase()
	for _, agentType := range agentTypes {
		// Initialize agent map if needed
		if r.promptHooks[agentType] == nil {
			r.promptHooks[agentType] = make(map[HookPhase][]*PromptHook)
		}

		// Insert hook sorted by priority
		hooks := r.promptHooks[agentType][phase]
		hooks = r.insertPromptHookSorted(hooks, hook)
		r.promptHooks[agentType][phase] = hooks

		// Track agent type
		r.hookAgentTypes[hook.Name()] = append(r.hookAgentTypes[hook.Name()], agentType)
	}

	// Track hook by name
	r.promptHooksByName[hook.Name()] = hook

	return nil
}

// RegisterToolCallHookForAgents registers a tool call hook for multiple agent types.
func (r *AdaptiveHookRegistry) RegisterToolCallHookForAgents(agentTypes []string, hook *ToolCallHook) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate
	if _, exists := r.toolCallHooksByName[hook.Name()]; exists {
		return ErrHookAlreadyExists
	}

	// Register for each agent type
	phase := hook.Phase()
	for _, agentType := range agentTypes {
		// Initialize agent map if needed
		if r.toolCallHooks[agentType] == nil {
			r.toolCallHooks[agentType] = make(map[HookPhase][]*ToolCallHook)
		}

		// Insert hook sorted by priority
		hooks := r.toolCallHooks[agentType][phase]
		hooks = r.insertToolCallHookSorted(hooks, hook)
		r.toolCallHooks[agentType][phase] = hooks

		// Track agent type
		r.hookAgentTypes[hook.Name()] = append(r.hookAgentTypes[hook.Name()], agentType)
	}

	// Track hook by name
	r.toolCallHooksByName[hook.Name()] = hook

	return nil
}

// =============================================================================
// Agent-Specific Retrieval
// =============================================================================

// GetPromptHooksForAgent returns prompt hooks for a specific agent type and phase.
// Includes both agent-specific hooks and global hooks, sorted by priority.
func (r *AdaptiveHookRegistry) GetPromptHooksForAgent(agentType string, phase HookPhase) []*PromptHook {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect hooks from both global and agent-specific registrations
	result := make([]*PromptHook, 0)
	seen := make(map[string]bool)

	// Add global hooks
	if globalHooks := r.promptHooks[GlobalAgentType]; globalHooks != nil {
		for _, hook := range globalHooks[phase] {
			if hook.Enabled() && !seen[hook.Name()] {
				result = append(result, hook)
				seen[hook.Name()] = true
			}
		}
	}

	// Add agent-specific hooks (if not global)
	normalizedType := strings.ToLower(agentType)
	if normalizedType != GlobalAgentType {
		if agentHooks := r.promptHooks[normalizedType]; agentHooks != nil {
			for _, hook := range agentHooks[phase] {
				if hook.Enabled() && !seen[hook.Name()] {
					result = append(result, hook)
					seen[hook.Name()] = true
				}
			}
		}
	}

	// Re-sort combined result by priority
	sort.Slice(result, func(i, j int) bool {
		return result[i].Priority() < result[j].Priority()
	})

	return result
}

// GetToolCallHooksForAgent returns tool call hooks for a specific agent type and phase.
// Includes both agent-specific hooks and global hooks, sorted by priority.
func (r *AdaptiveHookRegistry) GetToolCallHooksForAgent(agentType string, phase HookPhase) []*ToolCallHook {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Collect hooks from both global and agent-specific registrations
	result := make([]*ToolCallHook, 0)
	seen := make(map[string]bool)

	// Add global hooks
	if globalHooks := r.toolCallHooks[GlobalAgentType]; globalHooks != nil {
		for _, hook := range globalHooks[phase] {
			if hook.Enabled() && !seen[hook.Name()] {
				result = append(result, hook)
				seen[hook.Name()] = true
			}
		}
	}

	// Add agent-specific hooks (if not global)
	normalizedType := strings.ToLower(agentType)
	if normalizedType != GlobalAgentType {
		if agentHooks := r.toolCallHooks[normalizedType]; agentHooks != nil {
			for _, hook := range agentHooks[phase] {
				if hook.Enabled() && !seen[hook.Name()] {
					result = append(result, hook)
					seen[hook.Name()] = true
				}
			}
		}
	}

	// Re-sort combined result by priority
	sort.Slice(result, func(i, j int) bool {
		return result[i].Priority() < result[j].Priority()
	})

	return result
}

// =============================================================================
// Helper Functions
// =============================================================================

// insertPromptHookSorted inserts a hook into a sorted slice maintaining priority order.
func (r *AdaptiveHookRegistry) insertPromptHookSorted(hooks []*PromptHook, hook *PromptHook) []*PromptHook {
	// Find insertion point
	idx := sort.Search(len(hooks), func(i int) bool {
		return hooks[i].Priority() > hook.Priority()
	})

	// Insert at index
	hooks = append(hooks, nil)
	copy(hooks[idx+1:], hooks[idx:])
	hooks[idx] = hook

	return hooks
}

// insertToolCallHookSorted inserts a hook into a sorted slice maintaining priority order.
func (r *AdaptiveHookRegistry) insertToolCallHookSorted(hooks []*ToolCallHook, hook *ToolCallHook) []*ToolCallHook {
	// Find insertion point
	idx := sort.Search(len(hooks), func(i int) bool {
		return hooks[i].Priority() > hook.Priority()
	})

	// Insert at index
	hooks = append(hooks, nil)
	copy(hooks[idx+1:], hooks[idx:])
	hooks[idx] = hook

	return hooks
}

// removePromptHook removes a prompt hook by name from all registrations.
func (r *AdaptiveHookRegistry) removePromptHook(name string) {
	// Get agent types this hook is registered for
	agentTypes := r.hookAgentTypes[name]

	// Remove from each agent's hooks
	for _, agentType := range agentTypes {
		if agentHooks := r.promptHooks[agentType]; agentHooks != nil {
			for phase, hooks := range agentHooks {
				filtered := make([]*PromptHook, 0, len(hooks))
				for _, h := range hooks {
					if h.Name() != name {
						filtered = append(filtered, h)
					}
				}
				r.promptHooks[agentType][phase] = filtered
			}
		}
	}

	// Remove from tracking maps
	delete(r.promptHooksByName, name)
	delete(r.hookAgentTypes, name)
}

// removeToolCallHook removes a tool call hook by name from all registrations.
func (r *AdaptiveHookRegistry) removeToolCallHook(name string) {
	// Get agent types this hook is registered for
	agentTypes := r.hookAgentTypes[name]

	// Remove from each agent's hooks
	for _, agentType := range agentTypes {
		if agentHooks := r.toolCallHooks[agentType]; agentHooks != nil {
			for phase, hooks := range agentHooks {
				filtered := make([]*ToolCallHook, 0, len(hooks))
				for _, h := range hooks {
					if h.Name() != name {
						filtered = append(filtered, h)
					}
				}
				r.toolCallHooks[agentType][phase] = filtered
			}
		}
	}

	// Remove from tracking maps
	delete(r.toolCallHooksByName, name)
	delete(r.hookAgentTypes, name)
}

// =============================================================================
// Hook Statistics
// =============================================================================

// HookCount returns the total number of registered hooks.
func (r *AdaptiveHookRegistry) HookCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.promptHooksByName) + len(r.toolCallHooksByName)
}

// PromptHookCount returns the number of registered prompt hooks.
func (r *AdaptiveHookRegistry) PromptHookCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.promptHooksByName)
}

// ToolCallHookCount returns the number of registered tool call hooks.
func (r *AdaptiveHookRegistry) ToolCallHookCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.toolCallHooksByName)
}

// GetRegisteredAgentTypes returns all agent types that have hooks registered.
func (r *AdaptiveHookRegistry) GetRegisteredAgentTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool)
	for _, agentTypes := range r.hookAgentTypes {
		for _, at := range agentTypes {
			seen[at] = true
		}
	}

	result := make([]string, 0, len(seen))
	for at := range seen {
		result = append(result, at)
	}

	sort.Strings(result)
	return result
}

// Ensure AdaptiveHookRegistry implements HookRegistry.
var _ HookRegistry = (*AdaptiveHookRegistry)(nil)
