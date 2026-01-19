package archivalist

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.3 EventAggregator Tests
// =============================================================================

func TestNewEventAggregator_Initialization(t *testing.T) {
	window := 5 * time.Second
	aggregator := NewEventAggregator(window)

	if aggregator == nil {
		t.Fatal("Expected non-nil EventAggregator")
	}

	if aggregator.window != window {
		t.Errorf("Expected window %v, got %v", window, aggregator.window)
	}

	if aggregator.pending == nil {
		t.Fatal("Expected pending map to be initialized")
	}

	if len(aggregator.pending) != 0 {
		t.Errorf("Expected empty pending map, got %d entries", len(aggregator.pending))
	}
}

func TestEventAggregator_Add_FirstEvent(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	event := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call content",
	)
	event.AgentID = "agent-1"

	result := aggregator.Add(event)

	if result != nil {
		t.Error("Expected nil result for first event (should be buffered)")
	}

	if len(aggregator.pending) != 1 {
		t.Errorf("Expected 1 pending aggregate, got %d", len(aggregator.pending))
	}
}

func TestEventAggregator_Add_SameKey_WithinWindow(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 1",
	)
	event1.AgentID = "agent-1"

	event2 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 2",
	)
	event2.AgentID = "agent-1"

	// Add first event
	result1 := aggregator.Add(event1)
	if result1 != nil {
		t.Error("Expected nil result for first event")
	}

	// Add second event with same key, within window
	result2 := aggregator.Add(event2)
	if result2 != nil {
		t.Error("Expected nil result for second event (within window)")
	}

	// Should still have 1 aggregate with 2 events
	if len(aggregator.pending) != 1 {
		t.Errorf("Expected 1 pending aggregate, got %d", len(aggregator.pending))
	}

	key := aggregator.aggregationKey(event1)
	agg := aggregator.pending[key]

	if agg.Count != 2 {
		t.Errorf("Expected count 2, got %d", agg.Count)
	}

	if len(agg.Events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(agg.Events))
	}
}

func TestEventAggregator_Add_WindowExpired(t *testing.T) {
	window := 100 * time.Millisecond
	aggregator := NewEventAggregator(window)

	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 1",
	)
	event1.AgentID = "agent-1"

	// Add first event
	result1 := aggregator.Add(event1)
	if result1 != nil {
		t.Error("Expected nil result for first event")
	}

	// Wait for window to expire
	time.Sleep(window + 50*time.Millisecond)

	event2 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 2",
	)
	event2.AgentID = "agent-1"

	// Add second event after window expires
	result2 := aggregator.Add(event2)

	if result2 == nil {
		t.Fatal("Expected non-nil result (flushed aggregate)")
	}

	// Verify the flushed event
	if result2.EventType != events.EventTypeToolCall {
		t.Errorf("Expected EventTypeToolCall, got %v", result2.EventType)
	}

	if result2.AgentID != "agent-1" {
		t.Errorf("Expected agent-1, got %s", result2.AgentID)
	}

	// Check aggregation data
	data := result2.Data
	if data == nil {
		t.Fatal("Expected non-nil Data")
	}

	if !data["aggregated"].(bool) {
		t.Error("Expected aggregated flag to be true")
	}

	eventCount := data["event_count"].(int)
	if eventCount != 1 {
		t.Errorf("Expected event_count 1, got %d", eventCount)
	}

	// Should have a new aggregate for event2
	if len(aggregator.pending) != 1 {
		t.Errorf("Expected 1 pending aggregate (new), got %d", len(aggregator.pending))
	}
}

func TestEventAggregator_Add_DifferentKeys(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	// Different event types
	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call",
	)
	event1.AgentID = "agent-1"

	event2 := events.NewActivityEvent(
		events.EventTypeToolResult,
		"session-1",
		"Tool result",
	)
	event2.AgentID = "agent-1"

	// Different agent IDs
	event3 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call",
	)
	event3.AgentID = "agent-2"

	// Different session IDs
	event4 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-2",
		"Tool call",
	)
	event4.AgentID = "agent-1"

	aggregator.Add(event1)
	aggregator.Add(event2)
	aggregator.Add(event3)
	aggregator.Add(event4)

	// Should have 4 separate aggregates
	if len(aggregator.pending) != 4 {
		t.Errorf("Expected 4 pending aggregates, got %d", len(aggregator.pending))
	}
}

func TestEventAggregator_Flush_EmptyBuffer(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	result := aggregator.Flush()

	if result == nil {
		t.Fatal("Expected non-nil result slice")
	}

	if len(result) != 0 {
		t.Errorf("Expected empty result slice, got %d events", len(result))
	}
}

func TestEventAggregator_Flush_WithPendingEvents(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	// Add multiple events to different aggregates
	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 1",
	)
	event1.AgentID = "agent-1"

	event2 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 2",
	)
	event2.AgentID = "agent-1"

	event3 := events.NewActivityEvent(
		events.EventTypeToolResult,
		"session-1",
		"Tool result",
	)
	event3.AgentID = "agent-1"

	aggregator.Add(event1)
	aggregator.Add(event2)
	aggregator.Add(event3)

	result := aggregator.Flush()

	if len(result) != 2 {
		t.Errorf("Expected 2 flushed events, got %d", len(result))
	}

	// Buffer should be empty after flush
	if len(aggregator.pending) != 0 {
		t.Errorf("Expected empty pending map after flush, got %d entries", len(aggregator.pending))
	}

	// Verify aggregated events
	for _, event := range result {
		if event.Data == nil {
			t.Error("Expected non-nil Data in flushed event")
			continue
		}

		if !event.Data["aggregated"].(bool) {
			t.Error("Expected aggregated flag to be true")
		}
	}
}

func TestEventAggregator_aggregationKey(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Content",
	)
	event1.AgentID = "agent-1"

	event2 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Content",
	)
	event2.AgentID = "agent-1"

	key1 := aggregator.aggregationKey(event1)
	key2 := aggregator.aggregationKey(event2)

	if key1 != key2 {
		t.Errorf("Expected same key for identical events: %s != %s", key1, key2)
	}

	// Different agent ID should produce different key
	event3 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Content",
	)
	event3.AgentID = "agent-2"

	key3 := aggregator.aggregationKey(event3)
	if key1 == key3 {
		t.Error("Expected different keys for different agent IDs")
	}
}

func TestEventAggregator_createAggregatedEvent(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 1",
	)
	event1.AgentID = "agent-1"
	event1.Keywords = []string{"keyword1"}
	event1.RelatedIDs = []string{"related1"}

	event2 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 2",
	)
	event2.AgentID = "agent-1"
	event2.Keywords = []string{"keyword2"}
	event2.RelatedIDs = []string{"related2"}

	agg := &AggregatedEvent{
		EventType:      events.EventTypeToolCall,
		AgentID:        "agent-1",
		SessionID:      "session-1",
		Events:         []*events.ActivityEvent{event1, event2},
		Count:          2,
		FirstTimestamp: event1.Timestamp,
		LastTimestamp:  event2.Timestamp,
	}

	result := aggregator.createAggregatedEvent(agg)

	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// Verify basic fields
	if result.EventType != events.EventTypeToolCall {
		t.Errorf("Expected EventTypeToolCall, got %v", result.EventType)
	}

	if result.AgentID != "agent-1" {
		t.Errorf("Expected agent-1, got %s", result.AgentID)
	}

	if result.SessionID != "session-1" {
		t.Errorf("Expected session-1, got %s", result.SessionID)
	}

	// Verify aggregation metadata
	if result.Data == nil {
		t.Fatal("Expected non-nil Data")
	}

	if !result.Data["aggregated"].(bool) {
		t.Error("Expected aggregated flag to be true")
	}

	eventCount := result.Data["event_count"].(int)
	if eventCount != 2 {
		t.Errorf("Expected event_count 2, got %d", eventCount)
	}

	// Verify original IDs are preserved
	originalIDs := result.Data["original_ids"].([]string)
	if len(originalIDs) != 2 {
		t.Errorf("Expected 2 original IDs, got %d", len(originalIDs))
	}

	// Verify summary
	if result.Summary != "Aggregated 2 events" {
		t.Errorf("Unexpected summary: %s", result.Summary)
	}
}

func TestEventAggregator_collectRelatedIDs(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Content",
	)
	event1.RelatedIDs = []string{"id1", "id2"}

	event2 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Content",
	)
	event2.RelatedIDs = []string{"id2", "id3"}

	agg := &AggregatedEvent{
		Events: []*events.ActivityEvent{event1, event2},
	}

	relatedIDs := aggregator.collectRelatedIDs(agg)

	if len(relatedIDs) != 3 {
		t.Errorf("Expected 3 unique related IDs, got %d", len(relatedIDs))
	}

	// Verify all IDs are present (order not guaranteed)
	idMap := make(map[string]bool)
	for _, id := range relatedIDs {
		idMap[id] = true
	}

	expectedIDs := []string{"id1", "id2", "id3"}
	for _, expectedID := range expectedIDs {
		if !idMap[expectedID] {
			t.Errorf("Expected to find ID %s in related IDs", expectedID)
		}
	}
}

func TestEventAggregator_collectEventIDs(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)

	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Content",
	)

	event2 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Content",
	)

	agg := &AggregatedEvent{
		Events: []*events.ActivityEvent{event1, event2},
	}

	eventIDs := aggregator.collectEventIDs(agg)

	if len(eventIDs) != 2 {
		t.Errorf("Expected 2 event IDs, got %d", len(eventIDs))
	}

	if eventIDs[0] != event1.ID {
		t.Errorf("Expected first ID to be %s, got %s", event1.ID, eventIDs[0])
	}

	if eventIDs[1] != event2.ID {
		t.Errorf("Expected second ID to be %s, got %s", event2.ID, eventIDs[1])
	}
}

func TestEventAggregator_ConcurrentAccess(t *testing.T) {
	aggregator := NewEventAggregator(5 * time.Second)
	done := make(chan bool)

	// Spawn multiple goroutines adding events concurrently
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 10; j++ {
				event := events.NewActivityEvent(
					events.EventTypeToolCall,
					"session-1",
					"Tool call",
				)
				event.AgentID = "agent-1"
				aggregator.Add(event)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Flush and verify we got all events
	flushed := aggregator.Flush()
	if len(flushed) == 0 {
		t.Error("Expected at least one flushed event")
	}
}

func TestEventAggregator_MultipleWindowExpirations(t *testing.T) {
	window := 100 * time.Millisecond
	aggregator := NewEventAggregator(window)

	event1 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 1",
	)
	event1.AgentID = "agent-1"

	// Add first event
	aggregator.Add(event1)

	// Wait and add second event (triggers first flush)
	time.Sleep(window + 50*time.Millisecond)
	event2 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 2",
	)
	event2.AgentID = "agent-1"
	result1 := aggregator.Add(event2)

	if result1 == nil {
		t.Error("Expected first flush result")
	}

	// Wait and add third event (triggers second flush)
	time.Sleep(window + 50*time.Millisecond)
	event3 := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call 3",
	)
	event3.AgentID = "agent-1"
	result2 := aggregator.Add(event3)

	if result2 == nil {
		t.Error("Expected second flush result")
	}

	// Verify both flushes contained proper data
	if result1.Data["event_count"].(int) != 1 {
		t.Error("First flush should contain 1 event")
	}

	if result2.Data["event_count"].(int) != 1 {
		t.Error("Second flush should contain 1 event")
	}
}
