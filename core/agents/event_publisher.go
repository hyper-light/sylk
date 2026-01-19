package agents

import (
	"fmt"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AgentEventPublisher - Event publisher for agent activities
// =============================================================================

// AgentEventPublisher provides a convenient interface for agents to publish
// activity events to the event bus. It encapsulates the agent ID and provides
// typed methods for common event types.
type AgentEventPublisher struct {
	bus     *events.ActivityEventBus
	agentID string
}

// NewAgentEventPublisher creates a new AgentEventPublisher for the given agent.
// The bus parameter must not be nil.
func NewAgentEventPublisher(bus *events.ActivityEventBus, agentID string) *AgentEventPublisher {
	return &AgentEventPublisher{
		bus:     bus,
		agentID: agentID,
	}
}

// AgentID returns the agent ID associated with this publisher.
func (p *AgentEventPublisher) AgentID() string {
	return p.agentID
}

// Bus returns the underlying event bus.
func (p *AgentEventPublisher) Bus() *events.ActivityEventBus {
	return p.bus
}

// PublishAgentAction publishes an agent action event.
// Action events represent discrete actions taken by the agent.
func (p *AgentEventPublisher) PublishAgentAction(sessionID, action, details string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	content := fmt.Sprintf("Action: %s", action)
	if details != "" {
		content = fmt.Sprintf("%s - %s", content, details)
	}

	event := events.NewActivityEvent(events.EventTypeAgentAction, sessionID, content)
	event.AgentID = p.agentID
	event.Summary = action
	event.Data["action"] = action
	event.Data["details"] = details

	p.bus.Publish(event)
	return nil
}

// PublishAgentDecision publishes an agent decision event.
// Decision events represent significant choices made by the agent.
func (p *AgentEventPublisher) PublishAgentDecision(sessionID, decision, rationale string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	content := fmt.Sprintf("Decision: %s", decision)
	if rationale != "" {
		content = fmt.Sprintf("%s\nRationale: %s", content, rationale)
	}

	event := events.NewActivityEvent(events.EventTypeAgentDecision, sessionID, content)
	event.AgentID = p.agentID
	event.Summary = decision
	event.Data["decision"] = decision
	event.Data["rationale"] = rationale

	p.bus.Publish(event)
	return nil
}

// PublishAgentError publishes an agent error event.
// Error events represent errors encountered during agent execution.
func (p *AgentEventPublisher) PublishAgentError(sessionID string, err error, context string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	content := fmt.Sprintf("Error: %s", errMsg)
	if context != "" {
		content = fmt.Sprintf("%s\nContext: %s", content, context)
	}

	event := events.NewActivityEvent(events.EventTypeAgentError, sessionID, content)
	event.AgentID = p.agentID
	event.Summary = errMsg
	event.Outcome = events.OutcomeFailure
	event.Data["error"] = errMsg
	event.Data["context"] = context

	p.bus.Publish(event)
	return nil
}

// PublishSuccess publishes a success event with the given description.
// Success events indicate successful completion of a task or operation.
func (p *AgentEventPublisher) PublishSuccess(sessionID, description string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	event := events.NewActivityEvent(events.EventTypeSuccess, sessionID, description)
	event.AgentID = p.agentID
	event.Summary = description
	event.Outcome = events.OutcomeSuccess
	event.Data["description"] = description

	p.bus.Publish(event)
	return nil
}

// PublishFailure publishes a failure event with the given description and error.
// Failure events indicate failed completion of a task or operation.
func (p *AgentEventPublisher) PublishFailure(sessionID, description string, err error) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}

	content := description
	if errMsg != "" {
		content = fmt.Sprintf("%s: %s", description, errMsg)
	}

	event := events.NewActivityEvent(events.EventTypeFailure, sessionID, content)
	event.AgentID = p.agentID
	event.Summary = description
	event.Outcome = events.OutcomeFailure
	event.Data["description"] = description
	event.Data["error"] = errMsg

	p.bus.Publish(event)
	return nil
}

// WithSessionID returns a SessionAgentEventPublisher that has the session ID
// pre-set for convenience. This allows chaining like:
//
//	publisher.WithSessionID("abc").PublishAction("did thing", "details")
func (p *AgentEventPublisher) WithSessionID(sessionID string) *SessionAgentEventPublisher {
	return &SessionAgentEventPublisher{
		publisher: p,
		sessionID: sessionID,
	}
}

// =============================================================================
// SessionAgentEventPublisher - Session-scoped event publisher
// =============================================================================

// SessionAgentEventPublisher is a convenience wrapper around AgentEventPublisher
// that has a pre-set session ID. This simplifies publishing multiple events
// within the same session without repeating the session ID.
type SessionAgentEventPublisher struct {
	publisher *AgentEventPublisher
	sessionID string
}

// SessionID returns the session ID associated with this publisher.
func (s *SessionAgentEventPublisher) SessionID() string {
	return s.sessionID
}

// AgentID returns the agent ID from the underlying publisher.
func (s *SessionAgentEventPublisher) AgentID() string {
	return s.publisher.agentID
}

// PublishAction publishes an agent action event with the pre-set session ID.
func (s *SessionAgentEventPublisher) PublishAction(action, details string) error {
	return s.publisher.PublishAgentAction(s.sessionID, action, details)
}

// PublishDecision publishes an agent decision event with the pre-set session ID.
func (s *SessionAgentEventPublisher) PublishDecision(decision, rationale string) error {
	return s.publisher.PublishAgentDecision(s.sessionID, decision, rationale)
}

// PublishError publishes an agent error event with the pre-set session ID.
func (s *SessionAgentEventPublisher) PublishError(err error, context string) error {
	return s.publisher.PublishAgentError(s.sessionID, err, context)
}

// PublishSuccess publishes a success event with the pre-set session ID.
func (s *SessionAgentEventPublisher) PublishSuccess(description string) error {
	return s.publisher.PublishSuccess(s.sessionID, description)
}

// PublishFailure publishes a failure event with the pre-set session ID.
func (s *SessionAgentEventPublisher) PublishFailure(description string, err error) error {
	return s.publisher.PublishFailure(s.sessionID, description, err)
}
