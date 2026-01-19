package tools

import (
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// TrackedExecutorEventHook Interface
// =============================================================================

// TrackedExecutorEventHook defines the interface for receiving events from
// TrackedToolExecutor during tool execution. This allows decoupled event
// handling and integration with the activity event system.
//
// Implementations can be registered with TrackedToolExecutor to receive
// notifications about tool lifecycle events.
type TrackedExecutorEventHook interface {
	// OnToolStart is called when a tool begins execution.
	// The params map contains the input parameters passed to the tool.
	OnToolStart(sessionID, agentID, toolName string, params map[string]any)

	// OnToolComplete is called when a tool finishes execution.
	// The result contains the parsed output and outcome indicates success/failure.
	OnToolComplete(sessionID, agentID, toolName string, result any, outcome events.EventOutcome)

	// OnToolTimeout is called when a tool execution times out.
	// The timeout duration indicates how long was allowed before timeout.
	OnToolTimeout(sessionID, agentID, toolName string, timeout time.Duration)
}

// =============================================================================
// ToolEventPublisherHook - Adapter for event publishing
// =============================================================================

// ToolEventPublisherHook implements TrackedExecutorEventHook by publishing
// events to an ActivityEventBus. This adapter bridges the TrackedToolExecutor
// with the activity event system.
type ToolEventPublisherHook struct {
	bus *events.ActivityEventBus
}

// NewToolEventPublisherHook creates a new ToolEventPublisherHook that publishes
// tool events to the given event bus.
func NewToolEventPublisherHook(bus *events.ActivityEventBus) *ToolEventPublisherHook {
	return &ToolEventPublisherHook{
		bus: bus,
	}
}

// Bus returns the underlying event bus.
func (h *ToolEventPublisherHook) Bus() *events.ActivityEventBus {
	return h.bus
}

// OnToolStart publishes a tool call event when a tool begins execution.
func (h *ToolEventPublisherHook) OnToolStart(sessionID, agentID, toolName string, params map[string]any) {
	if h.bus == nil {
		return
	}

	content := fmt.Sprintf("Tool started: %s", toolName)

	event := events.NewActivityEvent(events.EventTypeToolCall, sessionID, content)
	event.AgentID = agentID
	event.Summary = fmt.Sprintf("Executing tool: %s", toolName)
	event.Data["tool_name"] = toolName
	event.Data["params"] = params

	h.bus.Publish(event)
}

// OnToolComplete publishes a tool result event when a tool finishes execution.
func (h *ToolEventPublisherHook) OnToolComplete(sessionID, agentID, toolName string, result any, outcome events.EventOutcome) {
	if h.bus == nil {
		return
	}

	content := fmt.Sprintf("Tool completed: %s", toolName)
	if outcome == events.OutcomeFailure {
		content = fmt.Sprintf("Tool failed: %s", toolName)
	}

	event := events.NewActivityEvent(events.EventTypeToolResult, sessionID, content)
	event.AgentID = agentID
	event.Summary = content
	event.Outcome = outcome
	event.Data["tool_name"] = toolName
	event.Data["result"] = result

	h.bus.Publish(event)
}

// OnToolTimeout publishes a tool timeout event when a tool execution times out.
func (h *ToolEventPublisherHook) OnToolTimeout(sessionID, agentID, toolName string, timeout time.Duration) {
	if h.bus == nil {
		return
	}

	content := fmt.Sprintf("Tool timed out: %s after %v", toolName, timeout)

	event := events.NewActivityEvent(events.EventTypeToolTimeout, sessionID, content)
	event.AgentID = agentID
	event.Summary = content
	event.Outcome = events.OutcomeFailure
	event.Data["tool_name"] = toolName
	event.Data["timeout"] = timeout.String()
	event.Data["timeout_ms"] = timeout.Milliseconds()

	h.bus.Publish(event)
}

// =============================================================================
// CompositeToolEventHook - Multiple hook aggregation
// =============================================================================

// CompositeToolEventHook aggregates multiple TrackedExecutorEventHook
// implementations and dispatches events to all of them. This allows
// attaching multiple event handlers to a single TrackedToolExecutor.
type CompositeToolEventHook struct {
	hooks []TrackedExecutorEventHook
}

// NewCompositeToolEventHook creates a new CompositeToolEventHook with the
// given hooks.
func NewCompositeToolEventHook(hooks ...TrackedExecutorEventHook) *CompositeToolEventHook {
	return &CompositeToolEventHook{
		hooks: hooks,
	}
}

// AddHook adds a hook to the composite.
func (c *CompositeToolEventHook) AddHook(hook TrackedExecutorEventHook) {
	c.hooks = append(c.hooks, hook)
}

// OnToolStart dispatches to all registered hooks.
func (c *CompositeToolEventHook) OnToolStart(sessionID, agentID, toolName string, params map[string]any) {
	for _, hook := range c.hooks {
		hook.OnToolStart(sessionID, agentID, toolName, params)
	}
}

// OnToolComplete dispatches to all registered hooks.
func (c *CompositeToolEventHook) OnToolComplete(sessionID, agentID, toolName string, result any, outcome events.EventOutcome) {
	for _, hook := range c.hooks {
		hook.OnToolComplete(sessionID, agentID, toolName, result, outcome)
	}
}

// OnToolTimeout dispatches to all registered hooks.
func (c *CompositeToolEventHook) OnToolTimeout(sessionID, agentID, toolName string, timeout time.Duration) {
	for _, hook := range c.hooks {
		hook.OnToolTimeout(sessionID, agentID, toolName, timeout)
	}
}

// Hooks returns the registered hooks.
func (c *CompositeToolEventHook) Hooks() []TrackedExecutorEventHook {
	return c.hooks
}

// =============================================================================
// NoOpToolEventHook - No-operation hook for testing
// =============================================================================

// NoOpToolEventHook is a no-operation implementation of TrackedExecutorEventHook.
// It can be used as a default when no event handling is needed or for testing.
type NoOpToolEventHook struct{}

// NewNoOpToolEventHook creates a new NoOpToolEventHook.
func NewNoOpToolEventHook() *NoOpToolEventHook {
	return &NoOpToolEventHook{}
}

// OnToolStart does nothing.
func (h *NoOpToolEventHook) OnToolStart(sessionID, agentID, toolName string, params map[string]any) {
}

// OnToolComplete does nothing.
func (h *NoOpToolEventHook) OnToolComplete(sessionID, agentID, toolName string, result any, outcome events.EventOutcome) {
}

// OnToolTimeout does nothing.
func (h *NoOpToolEventHook) OnToolTimeout(sessionID, agentID, toolName string, timeout time.Duration) {
}
