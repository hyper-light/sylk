package events

// ActivityPublisher is the interface for publishing activity events.
// Publishers in core/ packages use this interface to avoid importing
// the guide package directly (breaking circular dependency).
// The concrete implementation wraps the Guide ChannelBus.
type ActivityPublisher interface {
	// PublishActivity publishes an activity event. Returns an error when
	// the publish fails (e.g. bus closed). Callers that don't care about
	// delivery may discard the error explicitly with `_ = pub.PublishActivity(...)`
	// but should never call it on a nil publisher — see checkBus.
	// Implementations must be safe for concurrent use.
	PublishActivity(event *ActivityEvent) error
}
