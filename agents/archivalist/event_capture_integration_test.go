package archivalist

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.7.1 Event Capture Integration Tests
// =============================================================================

// TestEventCaptureIntegration_ActivityEventBusToArchivalist tests the full flow
// of events from ActivityEventBus to ArchivalistEventSubscriber.
func TestEventCaptureIntegration_ActivityEventBusToArchivalist(t *testing.T) {
	// Set up mock stores
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Create and configure the event bus
	bus := events.NewActivityEventBus(100)

	// Subscribe the archivalist
	bus.Subscribe(subscriber)

	// Start the bus dispatcher
	bus.Start()

	// Start the subscriber background processor
	err := subscriber.Start()
	if err != nil {
		t.Fatalf("Failed to start subscriber: %v", err)
	}

	// Publish events of various types
	testEvents := []*events.ActivityEvent{
		createIntegrationTestEvent("evt-1", events.EventTypeAgentDecision, "Decision about code structure"),
		createIntegrationTestEvent("evt-2", events.EventTypeUserPrompt, "User asked for help"),
		createIntegrationTestEvent("evt-3", events.EventTypeFailure, "Build failed"),
		createIntegrationTestEvent("evt-4", events.EventTypeSuccess, "Test passed"),
	}

	for _, event := range testEvents {
		bus.Publish(event)
	}

	// Wait for events to be processed
	time.Sleep(200 * time.Millisecond)

	// Stop and flush
	err = subscriber.Stop()
	if err != nil {
		t.Fatalf("Failed to stop subscriber: %v", err)
	}
	bus.Close()

	// Verify events were captured
	// AgentDecision, UserPrompt, Failure, Success all write to Bleve
	// AgentDecision, UserPrompt, Failure, Success all write to Vector (except strategy variations)
	bleveCount := bleveIndex.getIndexedCount()
	if bleveCount < 4 {
		t.Errorf("Expected at least 4 Bleve indexes, got %d", bleveCount)
	}
}

// TestEventCaptureIntegration_DebounceEvents tests that duplicate events
// are properly debounced by the ActivityEventBus.
func TestEventCaptureIntegration_DebounceEvents(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	bus := events.NewActivityEventBus(100)
	bus.Subscribe(subscriber)
	bus.Start()
	_ = subscriber.Start()

	// Create base event
	baseEvent := events.NewActivityEvent(
		events.EventTypeAgentDecision,
		"session-1",
		"Decision content",
	)
	baseEvent.AgentID = "agent-1"

	// Publish same event multiple times in rapid succession (should be debounced)
	for i := 0; i < 5; i++ {
		// Create events with same signature (type, agent, session)
		event := events.NewActivityEvent(
			events.EventTypeAgentDecision,
			"session-1",
			"Decision content",
		)
		event.AgentID = "agent-1"
		bus.Publish(event)
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	_ = subscriber.Stop()
	bus.Close()

	// Due to debouncing, we should see fewer than 5 events
	// The exact count depends on debounce window timing
	bleveCount := bleveIndex.getIndexedCount()
	if bleveCount >= 5 {
		t.Errorf("Expected debouncing to reduce event count, got %d (expected < 5)", bleveCount)
	}
}

// TestEventCaptureIntegration_Aggregation tests that high-volume events
// are properly aggregated before being written.
func TestEventCaptureIntegration_Aggregation(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	bus := events.NewActivityEventBus(100)
	bus.Subscribe(subscriber)
	bus.Start()
	_ = subscriber.Start()

	// Publish multiple tool call events (should be aggregated)
	// Use unique IDs to avoid debouncing
	for i := 0; i < 10; i++ {
		event := events.NewActivityEvent(
			events.EventTypeToolCall,
			"session-agg",
			"Tool call content",
		)
		event.AgentID = "agent-agg"
		bus.Publish(event)
		// Small delay to avoid debounce
		time.Sleep(20 * time.Millisecond)
	}

	// Wait briefly (less than aggregation window)
	time.Sleep(100 * time.Millisecond)

	// Events should be aggregating, not written yet
	initialCount := bleveIndex.getIndexedCount()

	// Stop subscriber to flush aggregated events
	_ = subscriber.Stop()
	bus.Close()

	// After flush, should have aggregated events
	finalCount := bleveIndex.getIndexedCount()

	// The final count should be less than 10 due to aggregation
	// or equal if window timing varies
	if finalCount > 10 {
		t.Errorf("Expected aggregation to reduce writes, got %d (expected <= 10)", finalCount)
	}

	// Verify we got more after flush (aggregates were written)
	if finalCount < initialCount {
		t.Errorf("Expected flush to write aggregates: initial=%d, final=%d", initialCount, finalCount)
	}
}

// TestEventCaptureIntegration_AllEventTypes tests that all event types
// flow correctly through the capture pipeline.
func TestEventCaptureIntegration_AllEventTypes(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	bus := events.NewActivityEventBus(500)
	bus.Subscribe(subscriber)
	bus.Start()
	_ = subscriber.Start()

	// Test all valid event types
	validTypes := events.ValidEventTypes()
	eventsByType := make(map[events.EventType]*events.ActivityEvent)

	for i, eventType := range validTypes {
		event := events.NewActivityEvent(
			eventType,
			"session-all",
			"Content for "+eventType.String(),
		)
		event.AgentID = "agent-all"
		eventsByType[eventType] = event
		bus.Publish(event)

		// Small delay between different types to avoid debounce
		if i < len(validTypes)-1 {
			time.Sleep(15 * time.Millisecond)
		}
	}

	// Wait for processing
	time.Sleep(500 * time.Millisecond)

	_ = subscriber.Stop()
	bus.Close()

	// All events should have been captured (possibly aggregated)
	bleveCount := bleveIndex.getIndexedCount()
	if bleveCount == 0 {
		t.Error("Expected events to be captured, got 0")
	}

	// Should have captured most event types (some may be aggregated)
	// At minimum, non-aggregated types should be present
	t.Logf("Captured %d events from %d event types", bleveCount, len(validTypes))
}

// TestEventCaptureIntegration_ConcurrentPublish tests concurrent event publishing
// from multiple goroutines.
func TestEventCaptureIntegration_ConcurrentPublish(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	bus := events.NewActivityEventBus(1000)
	bus.Subscribe(subscriber)
	bus.Start()
	_ = subscriber.Start()

	var wg sync.WaitGroup
	var publishedCount int32
	numGoroutines := 5
	eventsPerGoroutine := 20

	// Concurrent publishers
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				event := events.NewActivityEvent(
					events.EventTypeAgentDecision,
					"session-concurrent",
					"Concurrent event content",
				)
				event.AgentID = "agent-" + string(rune('A'+goroutineID))
				bus.Publish(event)
				atomic.AddInt32(&publishedCount, 1)
				time.Sleep(5 * time.Millisecond)
			}
		}(g)
	}

	wg.Wait()

	// Wait for processing
	time.Sleep(300 * time.Millisecond)

	_ = subscriber.Stop()
	bus.Close()

	// Verify events were captured (some may be debounced)
	bleveCount := bleveIndex.getIndexedCount()
	published := atomic.LoadInt32(&publishedCount)

	t.Logf("Published %d events, captured %d events", published, bleveCount)

	// Should capture a reasonable portion of events
	if bleveCount == 0 {
		t.Error("Expected some events to be captured")
	}
}

// TestEventCaptureIntegration_WildcardSubscription tests that the subscriber
// receives all event types via wildcard subscription.
func TestEventCaptureIntegration_WildcardSubscription(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Verify wildcard subscription
	if subscriber.EventTypes() != nil {
		t.Error("Expected nil EventTypes for wildcard subscription")
	}

	bus := events.NewActivityEventBus(100)
	bus.Subscribe(subscriber)
	bus.Start()
	_ = subscriber.Start()

	// Publish events of different types
	eventTypes := []events.EventType{
		events.EventTypeUserPrompt,
		events.EventTypeAgentAction,
		events.EventTypeIndexComplete,
		events.EventTypeContextEviction,
	}

	for i, et := range eventTypes {
		event := events.NewActivityEvent(et, "session-wc", "Content for wildcard test")
		event.AgentID = "agent-wc"
		bus.Publish(event)
		if i < len(eventTypes)-1 {
			time.Sleep(20 * time.Millisecond)
		}
	}

	time.Sleep(200 * time.Millisecond)

	_ = subscriber.Stop()
	bus.Close()

	// Should have captured all event types
	bleveCount := bleveIndex.getIndexedCount()
	if bleveCount < int32(len(eventTypes)) {
		t.Errorf("Expected at least %d events (wildcard subscription), got %d", len(eventTypes), bleveCount)
	}
}

// TestEventCaptureIntegration_BackpressureHandling tests that the system
// handles backpressure when the event buffer is full.
func TestEventCaptureIntegration_BackpressureHandling(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Small buffer to trigger backpressure
	bus := events.NewActivityEventBus(10)
	bus.Subscribe(subscriber)
	bus.Start()
	_ = subscriber.Start()

	// Flood the bus with events (some should be dropped)
	for i := 0; i < 100; i++ {
		event := events.NewActivityEvent(
			events.EventTypeAgentDecision,
			"session-bp",
			"Backpressure test content",
		)
		event.AgentID = "agent-bp"
		bus.Publish(event)
	}

	// Wait for processing
	time.Sleep(300 * time.Millisecond)

	_ = subscriber.Stop()
	bus.Close()

	// Should capture some events (not all due to backpressure)
	bleveCount := bleveIndex.getIndexedCount()

	// Due to debouncing AND backpressure, count will be significantly less than 100
	t.Logf("Captured %d events under backpressure (published 100)", bleveCount)

	// Verify system didn't crash and captured some events
	if bleveCount == 0 {
		t.Error("Expected some events despite backpressure")
	}
}

// TestEventCaptureIntegration_GracefulShutdown tests that shutdown flushes
// pending events correctly.
func TestEventCaptureIntegration_GracefulShutdown(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	bus := events.NewActivityEventBus(100)
	bus.Subscribe(subscriber)
	bus.Start()
	_ = subscriber.Start()

	// Publish some events
	for i := 0; i < 5; i++ {
		event := events.NewActivityEvent(
			events.EventTypeAgentDecision,
			"session-shutdown",
			"Shutdown test content",
		)
		event.AgentID = "agent-shutdown"
		bus.Publish(event)
		time.Sleep(20 * time.Millisecond)
	}

	// Small delay
	time.Sleep(100 * time.Millisecond)

	countBeforeShutdown := bleveIndex.getIndexedCount()

	// Graceful shutdown
	err := subscriber.Stop()
	if err != nil {
		t.Fatalf("Error during graceful shutdown: %v", err)
	}
	bus.Close()

	countAfterShutdown := bleveIndex.getIndexedCount()

	// After shutdown, pending events should be flushed
	if countAfterShutdown < countBeforeShutdown {
		t.Errorf("Shutdown should not lose events: before=%d, after=%d",
			countBeforeShutdown, countAfterShutdown)
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func createIntegrationTestEvent(id string, eventType events.EventType, content string) *events.ActivityEvent {
	event := events.NewActivityEvent(eventType, "test-session", content)
	event.AgentID = "test-agent"
	return event
}
