package tools

import (
	"fmt"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/google/uuid"
)

// =============================================================================
// ToolEventPublisher - Event publisher for tool execution events
// =============================================================================

// ToolEventPublisher publishes activity events for tool execution.
// It wraps the ActivityEventBus to provide type-safe event publishing
// for tool calls, results, and timeouts.
type ToolEventPublisher struct {
	bus *events.ActivityEventBus
}

// NewToolEventPublisher creates a new ToolEventPublisher.
// The bus parameter must not be nil.
func NewToolEventPublisher(bus *events.ActivityEventBus) *ToolEventPublisher {
	return &ToolEventPublisher{
		bus: bus,
	}
}

// PublishToolCall publishes a tool call event.
// This event is created when a tool is invoked.
//
// Parameters:
//   - sessionID: The session this tool call belongs to
//   - agentID: The agent invoking the tool
//   - toolName: The name of the tool being called
//   - params: The parameters passed to the tool
func (p *ToolEventPublisher) PublishToolCall(sessionID, agentID, toolName string, params map[string]any) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: events.EventTypeToolCall,
		Timestamp: time.Now(),
		SessionID: sessionID,
		AgentID:   agentID,
		Content:   fmt.Sprintf("Tool call: %s", toolName),
		Outcome:   events.OutcomePending,
		Data: map[string]any{
			"tool_name": toolName,
			"params":    params,
		},
	}

	p.bus.Publish(event)
	return nil
}

// PublishToolResult publishes a tool result event.
// This event is created when a tool completes execution.
//
// Parameters:
//   - sessionID: The session this tool result belongs to
//   - agentID: The agent that invoked the tool
//   - toolName: The name of the tool that completed
//   - result: The result returned by the tool
//   - outcome: The outcome status (success or failure)
func (p *ToolEventPublisher) PublishToolResult(sessionID, agentID, toolName string, result any, outcome events.EventOutcome) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: events.EventTypeToolResult,
		Timestamp: time.Now(),
		SessionID: sessionID,
		AgentID:   agentID,
		Content:   fmt.Sprintf("Tool result: %s", toolName),
		Outcome:   outcome,
		Data: map[string]any{
			"tool_name": toolName,
			"result":    result,
		},
	}

	p.bus.Publish(event)
	return nil
}

// PublishToolTimeout publishes a tool timeout event.
// This event is created when a tool execution times out.
//
// Parameters:
//   - sessionID: The session this timeout occurred in
//   - agentID: The agent whose tool timed out
//   - toolName: The name of the tool that timed out
//   - timeout: The duration that elapsed before timeout
func (p *ToolEventPublisher) PublishToolTimeout(sessionID, agentID, toolName string, timeout time.Duration) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: events.EventTypeToolTimeout,
		Timestamp: time.Now(),
		SessionID: sessionID,
		AgentID:   agentID,
		Content:   fmt.Sprintf("Tool timeout: %s after %v", toolName, timeout),
		Outcome:   events.OutcomeFailure,
		Data: map[string]any{
			"tool_name":       toolName,
			"timeout_ms":      timeout.Milliseconds(),
			"timeout_seconds": timeout.Seconds(),
		},
	}

	p.bus.Publish(event)
	return nil
}

// Bus returns the underlying event bus.
func (p *ToolEventPublisher) Bus() *events.ActivityEventBus {
	return p.bus
}
