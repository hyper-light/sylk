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

// PublishActivity wraps the event in an activity message and publishes
// it to TopicActivity. Errors are silently dropped because activity
// events are non-critical telemetry.
func (p *BusActivityPublisher) PublishActivity(event *events.ActivityEvent) {
	if event == nil || p.bus == nil {
		return
	}
	msg := NewActivityMessage(event.AgentID, event)
	_ = p.bus.Publish(TopicActivity, msg)
}

// Compile-time interface check.
var _ events.ActivityPublisher = (*BusActivityPublisher)(nil)
