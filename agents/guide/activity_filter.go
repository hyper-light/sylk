package guide

import "github.com/adalundhe/sylk/core/events"

// ActivityFilterFunc is a predicate that decides whether an activity
// event should pass through to the wrapped handler.
type ActivityFilterFunc func(event *events.ActivityEvent) bool

// FilteredActivityHandler wraps a MessageHandler with an activity
// filter predicate. Messages that are not activity messages or whose
// events fail the filter are silently dropped.
type FilteredActivityHandler struct {
	inner  MessageHandler
	filter ActivityFilterFunc
}

// NewFilteredActivityHandler creates a handler that only forwards
// activity messages passing the given filter to inner.
func NewFilteredActivityHandler(inner MessageHandler, filter ActivityFilterFunc) MessageHandler {
	return (&FilteredActivityHandler{inner: inner, filter: filter}).Handle
}

// Handle implements the filtering logic.
func (f *FilteredActivityHandler) Handle(msg *Message) error {
	event, ok := msg.GetActivityEvent()
	if !ok {
		return nil
	}
	if !f.filter(event) {
		return nil
	}
	return f.inner(msg)
}

// UserVisibleFilter passes only VisibilityUser events (UI promotion).
func UserVisibleFilter() ActivityFilterFunc {
	return func(event *events.ActivityEvent) bool {
		return event.Visibility == events.VisibilityUser
	}
}

// NonSystemFilter passes VisibilityUser and VisibilityAgent events.
// System-visibility events (health checks, background syncs) are dropped.
func NonSystemFilter() ActivityFilterFunc {
	return func(event *events.ActivityEvent) bool {
		return event.Visibility != events.VisibilitySystem
	}
}

// AllEventsFilter passes every activity event (for persistence).
func AllEventsFilter() ActivityFilterFunc {
	return func(_ *events.ActivityEvent) bool { return true }
}

// ByEventType passes only the specified event types.
func ByEventType(types ...events.EventType) ActivityFilterFunc {
	allowed := make(map[events.EventType]struct{}, len(types))
	for _, t := range types {
		allowed[t] = struct{}{}
	}
	return func(event *events.ActivityEvent) bool {
		_, ok := allowed[event.EventType]
		return ok
	}
}

// And composes multiple filters; all must pass.
func And(filters ...ActivityFilterFunc) ActivityFilterFunc {
	return func(event *events.ActivityEvent) bool {
		for _, f := range filters {
			if !f(event) {
				return false
			}
		}
		return true
	}
}
