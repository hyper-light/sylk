package network

import (
	"strings"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/google/uuid"
)

// ActivityTelemetryObserver implements TelemetryObserver by mapping
// MessageEnvelope metadata to ActivityEvents and publishing them
// via the events.ActivityPublisher interface.
type ActivityTelemetryObserver struct {
	publisher events.ActivityPublisher
}

// NewActivityTelemetryObserver creates an observer backed by the given publisher.
func NewActivityTelemetryObserver(pub events.ActivityPublisher) *ActivityTelemetryObserver {
	return &ActivityTelemetryObserver{publisher: pub}
}

// OnMessageSent maps the envelope to an ActivityEvent and publishes it.
// Non-blocking: PublishActivity internally uses select/default.
func (o *ActivityTelemetryObserver) OnMessageSent(env *MessageEnvelope, err error) {
	if o.publisher == nil || env == nil {
		return
	}

	eventType := inferEventType(env.Topic)

	event := &events.ActivityEvent{
		ID:        uuid.New().String(),
		EventType: eventType,
		AgentID:   env.SourceAgentType,
		SessionID: env.SourceContainerID,
		Timestamp: time.Now(),
		Data: map[string]any{
			"topic":            env.Topic,
			"source_container": env.SourceContainerID,
			"target_agent":     env.TargetAgentID,
		},
	}

	if err != nil {
		event.Data["error"] = err.Error()
	}

	o.publisher.PublishActivity(event)
}

// inferEventType derives an EventType from the topic prefix.
func inferEventType(topic string) events.EventType {
	prefix, _, _ := strings.Cut(topic, ".")
	switch prefix {
	case "request":
		return events.EventTypeAgentAction
	case "response":
		return events.EventTypeAgentDecision
	case "error":
		return events.EventTypeAgentError
	default:
		return events.EventTypeAgentAction
	}
}

// Compile-time check.
var _ TelemetryObserver = (*ActivityTelemetryObserver)(nil)
