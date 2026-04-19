package guide

import "github.com/adalundhe/sylk/core/events"

// BusActivityPublisher adapts the Guide ChannelBus to the
// events.ActivityPublisher interface, allowing core/ packages to publish
// activity events without importing guide directly.
type BusActivityPublisher struct {
	bus EventBus
}

// NewBusActivityPublisher creates an adapter that publishes ActivityEvents
// as ChannelBus messages on TopicActivity.
func NewBusActivityPublisher(bus EventBus) *BusActivityPublisher {
	return &BusActivityPublisher{bus: bus}
}

// PublishActivity wraps the event in an activity message and publishes it to
// TopicActivity. Returns the underlying ChannelBus.Publish error (e.g. when
// the bus is closed) so callers that care can react; telemetry-only callers
// may discard with `_ = p.PublishActivity(...)`.
func (p *BusActivityPublisher) PublishActivity(event *events.ActivityEvent) error {
	if event == nil || p.bus == nil {
		return nil
	}
	msg := NewActivityMessage(event.AgentID, event)
	return p.bus.Publish(TopicActivity, msg)
}

// Compile-time interface check.
var _ events.ActivityPublisher = (*BusActivityPublisher)(nil)
