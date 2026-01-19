package events

import (
	"fmt"
	"sync"
	"time"
)

// =============================================================================
// EventSubscriber Interface
// =============================================================================

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

// =============================================================================
// EventDebouncer
// =============================================================================

// EventDebouncer prevents duplicate events from being processed within a time window.
// Events are identified by a signature combining event type, agent ID, and session ID.
type EventDebouncer struct {
	window time.Duration
	seen   map[string]time.Time
	mu     sync.RWMutex
}

// NewEventDebouncer creates a new EventDebouncer with the specified time window.
// Events with the same signature within the window will be skipped.
func NewEventDebouncer(window time.Duration) *EventDebouncer {
	if window <= 0 {
		window = 5 * time.Second
	}
	return &EventDebouncer{
		window: window,
		seen:   make(map[string]time.Time),
	}
}

// ShouldSkip returns true if the event should be skipped because a duplicate
// was seen within the debounce window.
func (d *EventDebouncer) ShouldSkip(event *ActivityEvent) bool {
	signature := d.signature(event)

	d.mu.RLock()
	lastSeen, exists := d.seen[signature]
	d.mu.RUnlock()

	if !exists {
		d.recordEvent(signature)
		return false
	}

	if time.Since(lastSeen) > d.window {
		d.recordEvent(signature)
		return false
	}

	return true
}

// signature generates a unique signature for an event based on its key attributes.
func (d *EventDebouncer) signature(event *ActivityEvent) string {
	return fmt.Sprintf("%s:%s:%s", event.EventType.String(), event.AgentID, event.SessionID)
}

// recordEvent records the current time for an event signature.
func (d *EventDebouncer) recordEvent(signature string) {
	d.mu.Lock()
	d.seen[signature] = time.Now()
	d.mu.Unlock()
}

// Cleanup removes expired entries from the seen map.
// Should be called periodically to prevent memory growth.
func (d *EventDebouncer) Cleanup() {
	d.mu.Lock()
	defer d.mu.Unlock()

	cutoff := time.Now().Add(-d.window)
	for sig, lastSeen := range d.seen {
		if lastSeen.Before(cutoff) {
			delete(d.seen, sig)
		}
	}
}

// =============================================================================
// ActivityEventBus - Event bus for activity events
// =============================================================================

// ActivityEventBus manages activity event subscriptions and delivery
type ActivityEventBus struct {
	// subscribers maps event type to list of subscribers
	subscribers map[EventType][]EventSubscriber

	// wildcardSubscribers contains subscribers for all events
	wildcardSubscribers []EventSubscriber

	// buffer is the event buffer channel
	buffer chan *ActivityEvent

	// debouncer prevents event flooding
	debouncer *EventDebouncer

	// mu protects subscribers maps
	mu sync.RWMutex

	// dispatchMu protects dispatch goroutine shutdown
	dispatchMu sync.Mutex

	// closed indicates if the bus is closed
	closed bool

	// done signals the dispatch goroutine to stop
	done chan struct{}

	// wg waits for dispatch goroutine to finish
	wg sync.WaitGroup
}

// NewActivityEventBus creates a new activity event bus
func NewActivityEventBus(bufferSize int) *ActivityEventBus {
	if bufferSize <= 0 {
		bufferSize = 1000
	}

	return &ActivityEventBus{
		subscribers:         make(map[EventType][]EventSubscriber),
		wildcardSubscribers: make([]EventSubscriber, 0),
		buffer:              make(chan *ActivityEvent, bufferSize),
		debouncer:           NewEventDebouncer(100 * time.Millisecond),
		done:                make(chan struct{}),
	}
}

// Publish publishes an event to the bus
func (b *ActivityEventBus) Publish(event *ActivityEvent) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	b.mu.RUnlock()

	// Debounce the event
	if b.debouncer.ShouldSkip(event) {
		return
	}

	// Non-blocking send to buffer, drop if full
	select {
	case b.buffer <- event:
	default:
		// Buffer full, drop event
	}
}

// Subscribe registers a subscriber
func (b *ActivityEventBus) Subscribe(sub EventSubscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	eventTypes := sub.EventTypes()

	// Wildcard subscriber
	if len(eventTypes) == 0 {
		b.wildcardSubscribers = append(b.wildcardSubscribers, sub)
		return
	}

	// Event-specific subscribers
	for _, eventType := range eventTypes {
		b.subscribers[eventType] = append(b.subscribers[eventType], sub)
	}
}

// Unsubscribe removes a subscriber
func (b *ActivityEventBus) Unsubscribe(subscriberID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Remove from wildcard subscribers
	b.wildcardSubscribers = filterSubs(b.wildcardSubscribers, subscriberID)

	// Remove from event-specific subscribers
	for eventType, subs := range b.subscribers {
		b.subscribers[eventType] = filterSubs(subs, subscriberID)
	}
}

func filterSubs(subs []EventSubscriber, id string) []EventSubscriber {
	filtered := make([]EventSubscriber, 0, len(subs))
	for _, sub := range subs {
		if sub.ID() != id {
			filtered = append(filtered, sub)
		}
	}
	return filtered
}

// Start starts the dispatch goroutine
func (b *ActivityEventBus) Start() {
	b.dispatchMu.Lock()
	defer b.dispatchMu.Unlock()

	if b.closed {
		return
	}

	b.wg.Add(1)
	go b.dispatch()
}

// dispatch delivers events to subscribers
func (b *ActivityEventBus) dispatch() {
	defer b.wg.Done()

	for {
		select {
		case event := <-b.buffer:
			b.deliverEvent(event)
		case <-b.done:
			return
		}
	}
}

func (b *ActivityEventBus) deliverEvent(event *ActivityEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Deliver to wildcard subscribers
	for _, sub := range b.wildcardSubscribers {
		_ = sub.OnEvent(event)
	}

	// Deliver to event-specific subscribers
	if subs, ok := b.subscribers[event.EventType]; ok {
		for _, sub := range subs {
			_ = sub.OnEvent(event)
		}
	}
}

// Close gracefully shuts down the bus
func (b *ActivityEventBus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()

	// Signal dispatch goroutine to stop
	close(b.done)

	// Wait for dispatch goroutine to finish
	b.wg.Wait()
}
