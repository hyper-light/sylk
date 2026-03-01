package events

// EventSubscriber represents a subscriber to activity events.
type EventSubscriber interface {
	// ID returns the unique subscriber identifier.
	ID() string

	// EventTypes returns the event types this subscriber is interested in.
	// Empty slice means all events (wildcard subscription).
	EventTypes() []EventType

	// OnEvent is called when a subscribed event occurs.
	OnEvent(event *ActivityEvent) error
}
