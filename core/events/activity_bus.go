package events

import (
	"fmt"
	"sync"
	"sync/atomic"
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
// The debouncer automatically cleans up old entries to prevent unbounded memory growth.
type EventDebouncer struct {
	window   time.Duration
	seen     map[string]time.Time
	mu       sync.RWMutex
	done     chan struct{}
	stopped  bool
	stopOnce sync.Once
}

// NewEventDebouncer creates a new EventDebouncer with the specified time window.
// Events with the same signature within the window will be skipped.
// A background goroutine periodically cleans entries older than window*2.
// Call Stop() to release resources when done.
func NewEventDebouncer(window time.Duration) *EventDebouncer {
	if window <= 0 {
		window = 5 * time.Second
	}
	d := &EventDebouncer{
		window: window,
		seen:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
	go d.cleanupLoop()
	return d
}

// cleanupLoop runs periodic cleanup of expired entries.
func (d *EventDebouncer) cleanupLoop() {
	ticker := time.NewTicker(d.window)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.Cleanup()
		case <-d.done:
			return
		}
	}
}

// Stop stops the background cleanup goroutine.
// Safe to call multiple times.
func (d *EventDebouncer) Stop() {
	d.stopOnce.Do(func() {
		d.mu.Lock()
		d.stopped = true
		d.mu.Unlock()
		close(d.done)
	})
}

// ShouldSkip returns true if the event should be skipped because a duplicate
// was seen within the debounce window. Uses atomic check-and-record to prevent
// TOCTOU race conditions.
func (d *EventDebouncer) ShouldSkip(event *ActivityEvent) bool {
	signature := d.signature(event)

	d.mu.Lock()
	defer d.mu.Unlock()

	lastSeen, exists := d.seen[signature]

	if !exists {
		d.seen[signature] = time.Now()
		return false
	}

	if time.Since(lastSeen) > d.window {
		d.seen[signature] = time.Now()
		return false
	}

	return true
}

// signature generates a unique signature for an event based on its key attributes.
func (d *EventDebouncer) signature(event *ActivityEvent) string {
	return fmt.Sprintf("%s:%s:%s", event.EventType.String(), event.AgentID, event.SessionID)
}

// Cleanup removes expired entries from the seen map.
// Entries older than window*2 are removed to ensure memory is bounded.
func (d *EventDebouncer) Cleanup() {
	d.mu.Lock()
	defer d.mu.Unlock()

	cutoff := time.Now().Add(-d.window * 2)
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
	// subscribers maps event type to map of subscriber ID -> subscriber for O(1) lookup/removal
	subscribers map[EventType]map[string]EventSubscriber

	// wildcardSubscribers maps subscriber ID -> subscriber for O(1) lookup/removal
	wildcardSubscribers map[string]EventSubscriber

	// buffer is the event buffer channel
	buffer chan *ActivityEvent

	// debouncer prevents event flooding
	debouncer *EventDebouncer

	// mu protects subscribers maps
	mu sync.RWMutex

	// started indicates if dispatch goroutine is running (atomic for idempotent Start)
	started atomic.Bool

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
		subscribers:         make(map[EventType]map[string]EventSubscriber),
		wildcardSubscribers: make(map[string]EventSubscriber),
		buffer:              make(chan *ActivityEvent, bufferSize),
		debouncer:           NewEventDebouncer(100 * time.Millisecond),
		done:                make(chan struct{}),
	}
}

// Publish publishes an event to the bus.
// The RLock is held through the channel send to prevent TOCTOU race
// where Close() could execute between checking closed and sending.
func (b *ActivityEventBus) Publish(event *ActivityEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	if b.debouncer.ShouldSkip(event) {
		return
	}

	select {
	case b.buffer <- event:
	default:
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
	subID := sub.ID()

	// Wildcard subscriber
	if len(eventTypes) == 0 {
		b.wildcardSubscribers[subID] = sub
		return
	}

	// Event-specific subscribers
	for _, eventType := range eventTypes {
		if b.subscribers[eventType] == nil {
			b.subscribers[eventType] = make(map[string]EventSubscriber)
		}
		b.subscribers[eventType][subID] = sub
	}
}

// Unsubscribe removes a subscriber in O(1) time using map delete operations.
func (b *ActivityEventBus) Unsubscribe(subscriberID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Remove from wildcard subscribers - O(1)
	delete(b.wildcardSubscribers, subscriberID)

	// Remove from all event-specific subscribers - O(E) where E is number of event types
	for _, subs := range b.subscribers {
		delete(subs, subscriberID)
	}
}

// Start starts the dispatch goroutine.
// This method is idempotent - subsequent calls have no effect.
func (b *ActivityEventBus) Start() {
	if b.started.Swap(true) {
		return // Already started
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

	// Stop the debouncer cleanup goroutine
	b.debouncer.Stop()

	// Signal dispatch goroutine to stop
	close(b.done)

	// Wait for dispatch goroutine to finish
	b.wg.Wait()
}
