package guide

import (
	"fmt"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// debounceWindow is the default deduplication window.
const debounceWindow = 100 * time.Millisecond

// DebouncedMessageHandler wraps a MessageHandler with signature-based
// deduplication. Events with the same EventType:AgentID:SessionID
// signature within the debounce window are dropped.
type DebouncedMessageHandler struct {
	inner  MessageHandler
	window time.Duration

	mu   sync.Mutex
	seen map[string]time.Time

	done     chan struct{}
	stopOnce sync.Once
}

// NewDebouncedMessageHandler creates a debouncing wrapper.
// Call Stop() when done to release the background cleanup goroutine.
func NewDebouncedMessageHandler(inner MessageHandler) *DebouncedMessageHandler {
	d := &DebouncedMessageHandler{
		inner:  inner,
		window: debounceWindow,
		seen:   make(map[string]time.Time),
		done:   make(chan struct{}),
	}
	go d.cleanupLoop()
	return d
}

// Handle checks the debounce map and forwards non-duplicate messages.
func (d *DebouncedMessageHandler) Handle(msg *Message) error {
	event, ok := msg.GetActivityEvent()
	if !ok {
		return d.inner(msg)
	}
	if d.shouldSkip(event) {
		return nil
	}
	return d.inner(msg)
}

// Stop releases the background cleanup goroutine.
func (d *DebouncedMessageHandler) Stop() {
	d.stopOnce.Do(func() {
		close(d.done)
		d.mu.Lock()
		d.seen = make(map[string]time.Time)
		d.mu.Unlock()
	})
}

func (d *DebouncedMessageHandler) shouldSkip(event *events.ActivityEvent) bool {
	sig := signature(event)

	d.mu.Lock()
	defer d.mu.Unlock()

	lastSeen, exists := d.seen[sig]
	if !exists || time.Since(lastSeen) > d.window {
		d.seen[sig] = time.Now()
		return false
	}
	return true
}

func signature(event *events.ActivityEvent) string {
	return fmt.Sprintf("%s:%s:%s", event.EventType.String(), event.AgentID, event.SessionID)
}

func (d *DebouncedMessageHandler) cleanupLoop() {
	ticker := time.NewTicker(d.window * 2)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			d.cleanup()
		case <-d.done:
			return
		}
	}
}

func (d *DebouncedMessageHandler) cleanup() {
	cutoff := time.Now().Add(-d.window * 2)

	d.mu.Lock()
	defer d.mu.Unlock()

	for sig, ts := range d.seen {
		if ts.Before(cutoff) {
			delete(d.seen, sig)
		}
	}
}
