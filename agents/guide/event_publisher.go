package guide

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// GuideEventPublisher - Event publisher for Guide agent events
// =============================================================================

// GuideEventPublisher publishes activity events for the Guide agent.
// It wraps the ActivityEventBus to provide type-safe event publishing
// for user prompts, routing decisions, and clarification requests.
type GuideEventPublisher struct {
	bus       *events.ActivityEventBus
	sessionID string
}

// NewGuideEventPublisher creates a new GuideEventPublisher.
// The bus parameter must not be nil. The sessionID is used as the default
// session ID for events, but can be overridden per-event.
func NewGuideEventPublisher(bus *events.ActivityEventBus, sessionID string) *GuideEventPublisher {
	return &GuideEventPublisher{
		bus:       bus,
		sessionID: sessionID,
	}
}

// PublishUserPrompt publishes a user prompt event.
// This event is created when a user sends a prompt to the system.
//
// Parameters:
//   - sessionID: The session this prompt belongs to
//   - content: The user's prompt content
//   - agentID: The agent that received the prompt (typically "guide")
func (p *GuideEventPublisher) PublishUserPrompt(sessionID, content, agentID string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: events.EventTypeUserPrompt,
		Timestamp: time.Now(),
		SessionID: sessionID,
		AgentID:   agentID,
		Content:   content,
		Outcome:   events.OutcomePending,
		Data: map[string]any{
			"prompt_length": len(content),
		},
	}

	p.bus.Publish(event)
	return nil
}

// PublishRoutingDecision publishes a routing decision event.
// This event is created when the Guide makes a decision to route a request
// from one agent to another.
//
// Parameters:
//   - sessionID: The session this routing decision belongs to
//   - fromAgent: The source agent ID
//   - toAgent: The target agent ID
//   - reason: The reason for the routing decision
func (p *GuideEventPublisher) PublishRoutingDecision(sessionID, fromAgent, toAgent, reason string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: events.EventTypeAgentDecision,
		Timestamp: time.Now(),
		SessionID: sessionID,
		AgentID:   "guide",
		Content:   fmt.Sprintf("Routing decision: %s -> %s", fromAgent, toAgent),
		Outcome:   events.OutcomeSuccess,
		Data: map[string]any{
			"decision_type": "routing",
			"from_agent":    fromAgent,
			"to_agent":      toAgent,
			"reason":        reason,
		},
	}

	p.bus.Publish(event)
	return nil
}

// PublishClarificationRequest publishes a user clarification event.
// This event is created when the system needs to request clarification
// from the user.
//
// Parameters:
//   - sessionID: The session this clarification request belongs to
//   - question: The clarification question being asked
func (p *GuideEventPublisher) PublishClarificationRequest(sessionID, question string) error {
	if p.bus == nil {
		return fmt.Errorf("event bus is nil")
	}

	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: events.EventTypeUserClarification,
		Timestamp: time.Now(),
		SessionID: sessionID,
		AgentID:   "guide",
		Content:   question,
		Outcome:   events.OutcomePending,
		Data: map[string]any{
			"question_length": len(question),
		},
	}

	p.bus.Publish(event)
	return nil
}

// SessionID returns the default session ID for this publisher.
func (p *GuideEventPublisher) SessionID() string {
	return p.sessionID
}

// Bus returns the underlying event bus.
func (p *GuideEventPublisher) Bus() *events.ActivityEventBus {
	return p.bus
}
