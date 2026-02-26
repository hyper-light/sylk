package events

// ActivityPublisher is the interface for publishing activity events.
// Publishers in core/ packages use this interface to avoid importing
// the guide package directly (breaking circular dependency).
// The concrete implementation wraps the Guide ChannelBus.
type ActivityPublisher interface {
	// PublishActivity publishes an activity event.
	// Implementations must be safe for concurrent use.
	PublishActivity(event *ActivityEvent)
}
