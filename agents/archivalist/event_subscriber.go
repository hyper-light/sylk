package archivalist

import (
	"context"
	"sync"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.7 ArchivalistEventSubscriber
// =============================================================================

// ArchivalistEventSubscriber implements events.EventSubscriber to receive
// activity events and write them to the dual-write stores.
type ArchivalistEventSubscriber struct {
	dualWriter *DualWriter
	id         string

	// Background processing
	eventChan chan *events.ActivityEvent
	done      chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	running   bool
}

// NewArchivalistEventSubscriber creates a new ArchivalistEventSubscriber
// with the given DualWriter for event persistence.
func NewArchivalistEventSubscriber(dualWriter *DualWriter) *ArchivalistEventSubscriber {
	return &ArchivalistEventSubscriber{
		dualWriter: dualWriter,
		id:         "archivalist",
		eventChan:  make(chan *events.ActivityEvent, 1000),
		done:       make(chan struct{}),
	}
}

// ID returns the unique subscriber identifier.
// Implements events.EventSubscriber.
func (s *ArchivalistEventSubscriber) ID() string {
	return s.id
}

// EventTypes returns the event types this subscriber is interested in.
// Returns nil to subscribe to all event types (wildcard subscription).
// Implements events.EventSubscriber.
func (s *ArchivalistEventSubscriber) EventTypes() []events.EventType {
	return nil
}

// OnEvent is called when a subscribed event occurs.
// It queues the event for background processing.
// Implements events.EventSubscriber.
func (s *ArchivalistEventSubscriber) OnEvent(event *events.ActivityEvent) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		// If not running, write directly (synchronous mode)
		return s.dualWriter.Write(context.Background(), event)
	}
	s.mu.Unlock()

	// Queue event for background processing (non-blocking)
	select {
	case s.eventChan <- event:
	default:
		// Channel full, drop event (backpressure handling)
	}

	return nil
}

// Start starts the background processing goroutine.
// Events will be processed asynchronously after this call.
func (s *ArchivalistEventSubscriber) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	s.running = true
	s.wg.Add(1)
	go s.processEvents()

	return nil
}

// processEvents is the background goroutine that processes queued events.
func (s *ArchivalistEventSubscriber) processEvents() {
	defer s.wg.Done()

	for {
		select {
		case event := <-s.eventChan:
			// Process event with background context
			_ = s.dualWriter.Write(context.Background(), event)
		case <-s.done:
			// Drain remaining events
			s.drainEvents()
			return
		}
	}
}

// drainEvents processes any remaining events in the channel.
func (s *ArchivalistEventSubscriber) drainEvents() {
	for {
		select {
		case event := <-s.eventChan:
			_ = s.dualWriter.Write(context.Background(), event)
		default:
			return
		}
	}
}

// Stop stops the background processing and flushes pending events.
func (s *ArchivalistEventSubscriber) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()

	// Signal the processing goroutine to stop
	close(s.done)

	// Wait for the goroutine to finish
	s.wg.Wait()

	// Flush any pending aggregated events
	return s.dualWriter.Flush(context.Background())
}

// Verify interface implementation
var _ events.EventSubscriber = (*ArchivalistEventSubscriber)(nil)
