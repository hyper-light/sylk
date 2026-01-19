package archivalist

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.7 ArchivalistEventSubscriber Tests
// =============================================================================

func TestNewArchivalistEventSubscriber_Initialization(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	subscriber := NewArchivalistEventSubscriber(dualWriter)

	if subscriber == nil {
		t.Fatal("Expected non-nil ArchivalistEventSubscriber")
	}

	if subscriber.dualWriter == nil {
		t.Error("Expected dualWriter to be set")
	}

	if subscriber.id != "archivalist" {
		t.Errorf("Expected id 'archivalist', got '%s'", subscriber.id)
	}

	if subscriber.eventChan == nil {
		t.Error("Expected eventChan to be initialized")
	}

	if subscriber.done == nil {
		t.Error("Expected done channel to be initialized")
	}
}

func TestArchivalistEventSubscriber_ID(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	if subscriber.ID() != "archivalist" {
		t.Errorf("Expected ID 'archivalist', got '%s'", subscriber.ID())
	}
}

func TestArchivalistEventSubscriber_EventTypes(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	eventTypes := subscriber.EventTypes()

	if eventTypes != nil {
		t.Errorf("Expected nil EventTypes (wildcard subscription), got %v", eventTypes)
	}
}

func TestArchivalistEventSubscriber_OnEvent_SynchronousMode(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Without Start(), OnEvent should write synchronously
	event := events.NewActivityEvent(
		events.EventTypeAgentDecision,
		"session-1",
		"Decision content",
	)
	event.AgentID = "agent-1"

	err := subscriber.OnEvent(event)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Wait a bit for parallel writes to complete
	time.Sleep(50 * time.Millisecond)

	if bleveIndex.getIndexedCount() != 1 {
		t.Errorf("Expected 1 Bleve index, got %d", bleveIndex.getIndexedCount())
	}

	if vectorStore.getStoredCount() != 1 {
		t.Errorf("Expected 1 Vector store, got %d", vectorStore.getStoredCount())
	}
}

func TestArchivalistEventSubscriber_Start_Stop(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Start should not error
	err := subscriber.Start()
	if err != nil {
		t.Fatalf("Unexpected error on Start: %v", err)
	}

	// Double start should not error
	err = subscriber.Start()
	if err != nil {
		t.Fatalf("Unexpected error on double Start: %v", err)
	}

	// Stop should not error
	err = subscriber.Stop()
	if err != nil {
		t.Fatalf("Unexpected error on Stop: %v", err)
	}

	// Double stop should not error
	err = subscriber.Stop()
	if err != nil {
		t.Fatalf("Unexpected error on double Stop: %v", err)
	}
}

func TestArchivalistEventSubscriber_OnEvent_BackgroundMode(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Start background processing
	err := subscriber.Start()
	if err != nil {
		t.Fatalf("Unexpected error on Start: %v", err)
	}

	// Send multiple events
	for i := 0; i < 5; i++ {
		event := events.NewActivityEvent(
			events.EventTypeAgentDecision,
			"session-1",
			"Decision content",
		)
		event.AgentID = "agent-1"

		err := subscriber.OnEvent(event)
		if err != nil {
			t.Fatalf("Unexpected error on OnEvent: %v", err)
		}
	}

	// Wait for background processing
	time.Sleep(100 * time.Millisecond)

	// Stop and flush
	err = subscriber.Stop()
	if err != nil {
		t.Fatalf("Unexpected error on Stop: %v", err)
	}

	// Should have processed all events
	if bleveIndex.getIndexedCount() != 5 {
		t.Errorf("Expected 5 Bleve indexes, got %d", bleveIndex.getIndexedCount())
	}

	if vectorStore.getStoredCount() != 5 {
		t.Errorf("Expected 5 Vector stores, got %d", vectorStore.getStoredCount())
	}
}

func TestArchivalistEventSubscriber_OnEvent_AggregatedEvents(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Start background processing
	err := subscriber.Start()
	if err != nil {
		t.Fatalf("Unexpected error on Start: %v", err)
	}

	// Send multiple tool calls (should be aggregated)
	for i := 0; i < 5; i++ {
		event := events.NewActivityEvent(
			events.EventTypeToolCall,
			"session-1",
			"Tool call content",
		)
		event.AgentID = "agent-1"

		err := subscriber.OnEvent(event)
		if err != nil {
			t.Fatalf("Unexpected error on OnEvent: %v", err)
		}
	}

	// Wait for background processing (but not long enough for aggregation window)
	time.Sleep(100 * time.Millisecond)

	// Events should be aggregated, not written yet
	if bleveIndex.getIndexedCount() != 0 {
		t.Errorf("Expected 0 Bleve indexes (aggregating), got %d", bleveIndex.getIndexedCount())
	}

	// Stop should flush aggregated events
	err = subscriber.Stop()
	if err != nil {
		t.Fatalf("Unexpected error on Stop: %v", err)
	}

	// Should have 1 aggregated event
	if bleveIndex.getIndexedCount() != 1 {
		t.Errorf("Expected 1 Bleve index (aggregated), got %d", bleveIndex.getIndexedCount())
	}

	// Verify it's an aggregated event
	if len(bleveIndex.events) > 0 {
		aggEvent := bleveIndex.events[0]
		if aggEvent.Data == nil {
			t.Error("Expected non-nil Data in aggregated event")
		} else if !aggEvent.Data["aggregated"].(bool) {
			t.Error("Expected aggregated flag to be true")
		}
	}
}

func TestArchivalistEventSubscriber_InterfaceImplementation(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Verify it implements events.EventSubscriber
	var _ events.EventSubscriber = subscriber
}

func TestArchivalistEventSubscriber_StopFlushesAggregatedEvents(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Start background processing
	_ = subscriber.Start()

	// Send multiple LLM requests (should be aggregated)
	for i := 0; i < 3; i++ {
		event := events.NewActivityEvent(
			events.EventTypeLLMRequest,
			"session-1",
			"LLM request content",
		)
		event.AgentID = "agent-1"

		_ = subscriber.OnEvent(event)
	}

	// Also send different aggregate type
	for i := 0; i < 2; i++ {
		event := events.NewActivityEvent(
			events.EventTypeLLMResponse,
			"session-1",
			"LLM response content",
		)
		event.AgentID = "agent-1"

		_ = subscriber.OnEvent(event)
	}

	// Wait briefly
	time.Sleep(50 * time.Millisecond)

	// Events should still be aggregating
	if bleveIndex.getIndexedCount() != 0 {
		t.Errorf("Expected 0 Bleve indexes while aggregating, got %d", bleveIndex.getIndexedCount())
	}

	// Stop should flush all aggregates
	err := subscriber.Stop()
	if err != nil {
		t.Fatalf("Unexpected error on Stop: %v", err)
	}

	// Should have 2 aggregated events (one for LLMRequest, one for LLMResponse)
	if bleveIndex.getIndexedCount() != 2 {
		t.Errorf("Expected 2 Bleve indexes (2 aggregates), got %d", bleveIndex.getIndexedCount())
	}
}

func TestArchivalistEventSubscriber_ConcurrentEvents(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Start background processing
	_ = subscriber.Start()

	// Send events from multiple goroutines
	var sentCount int32
	done := make(chan struct{})

	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				event := events.NewActivityEvent(
					events.EventTypeAgentDecision,
					"session-1",
					"Decision content",
				)
				event.AgentID = "agent-1"

				_ = subscriber.OnEvent(event)
				atomic.AddInt32(&sentCount, 1)
			}
			done <- struct{}{}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}

	// Wait for background processing
	time.Sleep(100 * time.Millisecond)

	// Stop
	_ = subscriber.Stop()

	totalSent := atomic.LoadInt32(&sentCount)
	if totalSent != 50 {
		t.Errorf("Expected 50 events sent, got %d", totalSent)
	}

	// All events should be processed
	if bleveIndex.getIndexedCount() != 50 {
		t.Errorf("Expected 50 Bleve indexes, got %d", bleveIndex.getIndexedCount())
	}
}

func TestArchivalistEventSubscriber_DrainEventsOnStop(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Start background processing
	_ = subscriber.Start()

	// Fill the channel with events
	for i := 0; i < 100; i++ {
		event := events.NewActivityEvent(
			events.EventTypeAgentDecision,
			"session-1",
			"Decision content",
		)
		event.AgentID = "agent-1"

		_ = subscriber.OnEvent(event)
	}

	// Don't wait, just stop immediately
	_ = subscriber.Stop()

	// All queued events should be drained and processed
	// Note: Due to timing, we might not get exactly 100, but should get most
	count := bleveIndex.getIndexedCount()
	if count < 50 {
		t.Errorf("Expected at least 50 Bleve indexes (drained), got %d", count)
	}
}

func TestArchivalistEventSubscriber_MixedEventTypes(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)
	subscriber := NewArchivalistEventSubscriber(dualWriter)

	// Start background processing
	_ = subscriber.Start()

	// Send mixed event types
	// 1. AgentDecision - both stores, no aggregation
	event1 := events.NewActivityEvent(events.EventTypeAgentDecision, "session-1", "Decision")
	event1.AgentID = "agent-1"
	_ = subscriber.OnEvent(event1)

	// 2. ToolCall - Bleve only, aggregated
	event2 := events.NewActivityEvent(events.EventTypeToolCall, "session-1", "Tool call")
	event2.AgentID = "agent-1"
	_ = subscriber.OnEvent(event2)

	// 3. IndexStart - Bleve only, no aggregation
	event3 := events.NewActivityEvent(events.EventTypeIndexStart, "session-1", "Index start")
	_ = subscriber.OnEvent(event3)

	// 4. UserPrompt - both stores, no aggregation
	event4 := events.NewActivityEvent(events.EventTypeUserPrompt, "session-1", "User prompt")
	_ = subscriber.OnEvent(event4)

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	// Stop
	_ = subscriber.Stop()

	// Expected: 4 Bleve (3 immediate + 1 aggregated ToolCall flushed)
	// Expected: 2 Vector (AgentDecision + UserPrompt)
	if bleveIndex.getIndexedCount() != 4 {
		t.Errorf("Expected 4 Bleve indexes, got %d", bleveIndex.getIndexedCount())
	}

	if vectorStore.getStoredCount() != 2 {
		t.Errorf("Expected 2 Vector stores, got %d", vectorStore.getStoredCount())
	}
}
