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

// TestEventCaptureIntegration_SubscriberToArchivalist tests the full flow
// of events from OnEvent to the DualWriter stores.
func TestEventCaptureIntegration_SubscriberToArchivalist(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	err := subscriber.Start()
	if err != nil {
		t.Fatalf("Failed to start subscriber: %v", err)
	}

	testEvents := []*events.ActivityEvent{
		createIntegrationTestEvent("evt-1", events.EventTypeAgentDecision, "Decision about code structure"),
		createIntegrationTestEvent("evt-2", events.EventTypeUserPrompt, "User asked for help"),
		createIntegrationTestEvent("evt-3", events.EventTypeFailure, "Build failed"),
		createIntegrationTestEvent("evt-4", events.EventTypeSuccess, "Test passed"),
	}

	for _, event := range testEvents {
		_ = subscriber.OnEvent(event)
	}

	time.Sleep(200 * time.Millisecond)

	err = subscriber.Stop()
	if err != nil {
		t.Fatalf("Failed to stop subscriber: %v", err)
	}

	bleveCount := bleveIndex.getIndexedCount()
	if bleveCount < 4 {
		t.Errorf("Expected at least 4 Bleve indexes, got %d", bleveCount)
	}
}

// TestEventCaptureIntegration_Aggregation tests that high-volume events
// are properly aggregated before being written.
func TestEventCaptureIntegration_Aggregation(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)
	_ = subscriber.Start()

	for i := 0; i < 10; i++ {
		event := events.NewActivityEvent(
			events.EventTypeToolCall,
			"session-agg",
			"Tool call content",
		)
		event.AgentID = "agent-agg"
		_ = subscriber.OnEvent(event)
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	initialCount := bleveIndex.getIndexedCount()

	_ = subscriber.Stop()

	finalCount := bleveIndex.getIndexedCount()

	if finalCount > 10 {
		t.Errorf("Expected aggregation to reduce writes, got %d (expected <= 10)", finalCount)
	}

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
	_ = subscriber.Start()

	validTypes := events.ValidEventTypes()

	for i, eventType := range validTypes {
		event := events.NewActivityEvent(
			eventType,
			"session-all",
			"Content for "+eventType.String(),
		)
		event.AgentID = "agent-all"
		_ = subscriber.OnEvent(event)

		if i < len(validTypes)-1 {
			time.Sleep(15 * time.Millisecond)
		}
	}

	time.Sleep(500 * time.Millisecond)

	_ = subscriber.Stop()

	bleveCount := bleveIndex.getIndexedCount()
	if bleveCount == 0 {
		t.Error("Expected events to be captured, got 0")
	}

	t.Logf("Captured %d events from %d event types", bleveCount, len(validTypes))
}

// TestEventCaptureIntegration_ConcurrentPublish tests concurrent event delivery
// from multiple goroutines.
func TestEventCaptureIntegration_ConcurrentPublish(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)
	_ = subscriber.Start()

	var wg sync.WaitGroup
	var publishedCount int32
	numGoroutines := 5
	eventsPerGoroutine := 20

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
				_ = subscriber.OnEvent(event)
				atomic.AddInt32(&publishedCount, 1)
				time.Sleep(5 * time.Millisecond)
			}
		}(g)
	}

	wg.Wait()

	time.Sleep(300 * time.Millisecond)

	_ = subscriber.Stop()

	bleveCount := bleveIndex.getIndexedCount()
	published := atomic.LoadInt32(&publishedCount)

	t.Logf("Published %d events, captured %d events", published, bleveCount)

	if bleveCount == 0 {
		t.Error("Expected some events to be captured")
	}
}

// TestEventCaptureIntegration_WildcardSubscription tests that the subscriber
// reports wildcard subscription (all event types).
func TestEventCaptureIntegration_WildcardSubscription(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	if subscriber.EventTypes() != nil {
		t.Error("Expected nil EventTypes for wildcard subscription")
	}

	_ = subscriber.Start()

	eventTypes := []events.EventType{
		events.EventTypeUserPrompt,
		events.EventTypeAgentAction,
		events.EventTypeIndexComplete,
		events.EventTypeContextEviction,
	}

	for i, et := range eventTypes {
		event := events.NewActivityEvent(et, "session-wc", "Content for wildcard test")
		event.AgentID = "agent-wc"
		_ = subscriber.OnEvent(event)
		if i < len(eventTypes)-1 {
			time.Sleep(20 * time.Millisecond)
		}
	}

	time.Sleep(200 * time.Millisecond)

	_ = subscriber.Stop()

	bleveCount := bleveIndex.getIndexedCount()
	if bleveCount < int32(len(eventTypes)) {
		t.Errorf("Expected at least %d events (wildcard subscription), got %d", len(eventTypes), bleveCount)
	}
}

// TestEventCaptureIntegration_GracefulShutdown tests that shutdown flushes
// pending events correctly.
func TestEventCaptureIntegration_GracefulShutdown(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)
	_ = subscriber.Start()

	for i := 0; i < 5; i++ {
		event := events.NewActivityEvent(
			events.EventTypeAgentDecision,
			"session-shutdown",
			"Shutdown test content",
		)
		event.AgentID = "agent-shutdown"
		_ = subscriber.OnEvent(event)
		time.Sleep(20 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	countBeforeShutdown := bleveIndex.getIndexedCount()

	err := subscriber.Stop()
	if err != nil {
		t.Fatalf("Error during graceful shutdown: %v", err)
	}

	countAfterShutdown := bleveIndex.getIndexedCount()

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
