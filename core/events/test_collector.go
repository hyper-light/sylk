package events

import "sync"

// TestActivityCollector is a simple ActivityPublisher that collects
// published events for test assertions. It replaces the
// ActivityEventBus + EventSubscriber pattern in tests.
type TestActivityCollector struct {
	mu     sync.Mutex
	events []*ActivityEvent
}

// NewTestActivityCollector creates a collector for testing.
func NewTestActivityCollector() *TestActivityCollector {
	return &TestActivityCollector{}
}

// PublishActivity records the event for later inspection.
func (c *TestActivityCollector) PublishActivity(event *ActivityEvent) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
}

// Events returns a snapshot of all collected events.
func (c *TestActivityCollector) Events() []*ActivityEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]*ActivityEvent, len(c.events))
	copy(result, c.events)
	return result
}

// EventCount returns the number of collected events.
func (c *TestActivityCollector) EventCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

// Reset clears all collected events.
func (c *TestActivityCollector) Reset() {
	c.mu.Lock()
	c.events = nil
	c.mu.Unlock()
}

// Compile-time check.
var _ ActivityPublisher = (*TestActivityCollector)(nil)
