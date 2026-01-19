package archivalist

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.7.2 Dual-Write Integration Tests
// =============================================================================

// TestDualWriteIntegration_BothStoresReceive tests that both BleveEventIndex
// and VectorEventStore receive writes for events requiring both stores.
func TestDualWriteIntegration_BothStoresReceive(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	// Events that should go to both stores
	highValueTypes := []events.EventType{
		events.EventTypeAgentDecision,
		events.EventTypeFailure,
		events.EventTypeAgentError,
		events.EventTypeUserPrompt,
		events.EventTypeUserClarification,
	}

	for _, eventType := range highValueTypes {
		bleveIndex.indexedCount = 0
		vectorStore.storedCount = 0

		event := createDualWriteTestEvent(eventType, "Test content for "+eventType.String())

		err := dualWriter.Write(context.Background(), event)
		if err != nil {
			t.Fatalf("Write failed for %s: %v", eventType.String(), err)
		}

		// Wait for parallel writes
		time.Sleep(50 * time.Millisecond)

		if bleveIndex.getIndexedCount() != 1 {
			t.Errorf("%s: Expected 1 Bleve index, got %d", eventType.String(), bleveIndex.getIndexedCount())
		}

		if vectorStore.getStoredCount() != 1 {
			t.Errorf("%s: Expected 1 Vector store, got %d", eventType.String(), vectorStore.getStoredCount())
		}
	}
}

// TestDualWriteIntegration_ParallelWriteExecution tests that writes to both
// stores happen in parallel, not sequentially.
func TestDualWriteIntegration_ParallelWriteExecution(t *testing.T) {
	bleveIndex := newDelayedMockBleveEventIndex(100 * time.Millisecond)
	vectorStore := newDelayedMockVectorEventStore(100 * time.Millisecond)
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	event := createDualWriteTestEvent(events.EventTypeAgentDecision, "Parallel test")

	start := time.Now()
	err := dualWriter.Write(context.Background(), event)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// If parallel, should take ~100ms; if sequential, ~200ms
	// Allow some buffer for test execution overhead
	if elapsed > 180*time.Millisecond {
		t.Errorf("Writes appear to be sequential: elapsed=%v (expected ~100ms for parallel)", elapsed)
	}

	t.Logf("Parallel write elapsed: %v", elapsed)
}

// TestDualWriteIntegration_ConsistencyBetweenStores tests that both stores
// receive the same event data consistently.
func TestDualWriteIntegration_ConsistencyBetweenStores(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	event := events.NewActivityEvent(
		events.EventTypeAgentDecision,
		"session-consistency",
		"Consistency test content",
	)
	event.AgentID = "agent-consistency"
	event.Summary = "Test summary"
	event.Keywords = []string{"test", "consistency"}
	event.Category = "testing"

	err := dualWriter.Write(context.Background(), event)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Verify both stores received the event
	if len(bleveIndex.events) != 1 {
		t.Fatalf("Expected 1 event in Bleve, got %d", len(bleveIndex.events))
	}
	if len(vectorStore.events) != 1 {
		t.Fatalf("Expected 1 event in Vector, got %d", len(vectorStore.events))
	}

	// Verify event IDs match
	if bleveIndex.events[0].ID != event.ID {
		t.Errorf("Bleve event ID mismatch: got %s, want %s", bleveIndex.events[0].ID, event.ID)
	}
	if vectorStore.events[0].ID != event.ID {
		t.Errorf("Vector event ID mismatch: got %s, want %s", vectorStore.events[0].ID, event.ID)
	}

	// Verify content matches
	if bleveIndex.events[0].Content != event.Content {
		t.Errorf("Bleve content mismatch")
	}
	if vectorStore.events[0].Content != event.Content {
		t.Errorf("Vector content mismatch")
	}
}

// TestDualWriteIntegration_BleveOnlyEvents tests that Bleve-only events
// don't write to the Vector store.
func TestDualWriteIntegration_BleveOnlyEvents(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	// Events that should go to Bleve only (non-aggregated)
	bleveOnlyTypes := []events.EventType{
		events.EventTypeIndexStart,
		events.EventTypeIndexComplete,
		events.EventTypeContextEviction,
		events.EventTypeContextRestore,
	}

	for _, eventType := range bleveOnlyTypes {
		bleveIndex.indexedCount = 0
		vectorStore.storedCount = 0

		event := createDualWriteTestEvent(eventType, "Bleve-only content")

		err := dualWriter.Write(context.Background(), event)
		if err != nil {
			t.Fatalf("Write failed for %s: %v", eventType.String(), err)
		}

		time.Sleep(50 * time.Millisecond)

		if bleveIndex.getIndexedCount() != 1 {
			t.Errorf("%s: Expected 1 Bleve index, got %d", eventType.String(), bleveIndex.getIndexedCount())
		}

		if vectorStore.getStoredCount() != 0 {
			t.Errorf("%s: Expected 0 Vector stores, got %d", eventType.String(), vectorStore.getStoredCount())
		}
	}
}

// TestDualWriteIntegration_AggregatedEventsWriteBleveOnly tests that aggregated
// events write to Bleve only when flushed.
func TestDualWriteIntegration_AggregatedEventsWriteBleveOnly(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	// ToolCall events should aggregate and write to Bleve only
	for i := 0; i < 5; i++ {
		event := createDualWriteTestEvent(events.EventTypeToolCall, "Tool call content")
		_ = dualWriter.Write(context.Background(), event)
	}

	// Events are aggregating
	time.Sleep(50 * time.Millisecond)
	if bleveIndex.getIndexedCount() != 0 {
		t.Errorf("Expected 0 Bleve indexes while aggregating, got %d", bleveIndex.getIndexedCount())
	}

	// Flush the aggregates
	err := dualWriter.Flush(context.Background())
	if err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Should have 1 aggregated event in Bleve
	if bleveIndex.getIndexedCount() != 1 {
		t.Errorf("Expected 1 Bleve index after flush, got %d", bleveIndex.getIndexedCount())
	}

	// Should have 0 in Vector (ToolCall is Bleve-only)
	if vectorStore.getStoredCount() != 0 {
		t.Errorf("Expected 0 Vector stores (Bleve-only event), got %d", vectorStore.getStoredCount())
	}

	// Verify it's an aggregated event
	if len(bleveIndex.events) > 0 {
		aggEvent := bleveIndex.events[0]
		if aggEvent.Data == nil || !aggEvent.Data["aggregated"].(bool) {
			t.Error("Expected aggregated flag in flushed event")
		}
		if aggEvent.Data["event_count"].(int) != 5 {
			t.Errorf("Expected event_count 5, got %v", aggEvent.Data["event_count"])
		}
	}
}

// TestDualWriteIntegration_ErrorHandling tests error handling when one store fails.
func TestDualWriteIntegration_ErrorHandling(t *testing.T) {
	t.Run("Bleve error", func(t *testing.T) {
		bleveIndex := newMockBleveEventIndex()
		bleveIndex.indexErr = errors.New("bleve error")
		vectorStore := newMockVectorEventStore()
		dualWriter := NewDualWriter(bleveIndex, vectorStore)

		event := createDualWriteTestEvent(events.EventTypeAgentDecision, "Error test")

		err := dualWriter.Write(context.Background(), event)
		if err == nil {
			t.Error("Expected error when Bleve fails")
		}
	})

	t.Run("Vector error", func(t *testing.T) {
		bleveIndex := newMockBleveEventIndex()
		vectorStore := newMockVectorEventStore()
		vectorStore.storeErr = errors.New("vector error")
		dualWriter := NewDualWriter(bleveIndex, vectorStore)

		event := createDualWriteTestEvent(events.EventTypeAgentDecision, "Error test")

		err := dualWriter.Write(context.Background(), event)
		if err == nil {
			t.Error("Expected error when Vector fails")
		}
	})

	t.Run("Both stores error", func(t *testing.T) {
		bleveIndex := newMockBleveEventIndex()
		bleveIndex.indexErr = errors.New("bleve error")
		vectorStore := newMockVectorEventStore()
		vectorStore.storeErr = errors.New("vector error")
		dualWriter := NewDualWriter(bleveIndex, vectorStore)

		event := createDualWriteTestEvent(events.EventTypeAgentDecision, "Error test")

		err := dualWriter.Write(context.Background(), event)
		if err == nil {
			t.Error("Expected error when both stores fail")
		}
	})
}

// TestDualWriteIntegration_NilStoresHandled tests that nil stores are handled gracefully.
func TestDualWriteIntegration_NilStoresHandled(t *testing.T) {
	t.Run("Nil Bleve index", func(t *testing.T) {
		vectorStore := newMockVectorEventStore()
		dualWriter := NewDualWriter(nil, vectorStore)

		event := createDualWriteTestEvent(events.EventTypeAgentDecision, "Nil test")

		err := dualWriter.Write(context.Background(), event)
		if err != nil {
			t.Errorf("Expected no error with nil Bleve, got: %v", err)
		}

		time.Sleep(50 * time.Millisecond)

		// Vector should still receive write
		if vectorStore.getStoredCount() != 1 {
			t.Errorf("Expected 1 Vector store, got %d", vectorStore.getStoredCount())
		}
	})

	t.Run("Nil Vector store", func(t *testing.T) {
		bleveIndex := newMockBleveEventIndex()
		dualWriter := NewDualWriter(bleveIndex, nil)

		event := createDualWriteTestEvent(events.EventTypeAgentDecision, "Nil test")

		err := dualWriter.Write(context.Background(), event)
		if err != nil {
			t.Errorf("Expected no error with nil Vector, got: %v", err)
		}

		time.Sleep(50 * time.Millisecond)

		// Bleve should still receive write
		if bleveIndex.getIndexedCount() != 1 {
			t.Errorf("Expected 1 Bleve index, got %d", bleveIndex.getIndexedCount())
		}
	})

	t.Run("Both stores nil", func(t *testing.T) {
		dualWriter := NewDualWriter(nil, nil)

		event := createDualWriteTestEvent(events.EventTypeAgentDecision, "Nil test")

		err := dualWriter.Write(context.Background(), event)
		if err != nil {
			t.Errorf("Expected no error with both stores nil, got: %v", err)
		}
	})
}

// TestDualWriteIntegration_ConcurrentWrites tests concurrent writes don't cause
// data races or inconsistencies.
func TestDualWriteIntegration_ConcurrentWrites(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	var wg sync.WaitGroup
	numGoroutines := 10
	eventsPerGoroutine := 20

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				event := events.NewActivityEvent(
					events.EventTypeAgentDecision,
					"session-concurrent",
					"Concurrent content",
				)
				event.AgentID = "agent-concurrent"
				_ = dualWriter.Write(context.Background(), event)
			}
		}(g)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	expectedCount := int32(numGoroutines * eventsPerGoroutine)

	if bleveIndex.getIndexedCount() != expectedCount {
		t.Errorf("Expected %d Bleve indexes, got %d", expectedCount, bleveIndex.getIndexedCount())
	}

	if vectorStore.getStoredCount() != expectedCount {
		t.Errorf("Expected %d Vector stores, got %d", expectedCount, vectorStore.getStoredCount())
	}
}

// TestDualWriteIntegration_CorrectFieldsPassedToBleve tests that the correct
// fields are passed to Bleve based on the event type.
func TestDualWriteIntegration_CorrectFieldsPassedToBleve(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	event := events.NewActivityEvent(
		events.EventTypeAgentDecision,
		"session-fields",
		"Field test content",
	)
	event.AgentID = "agent-fields"
	event.Keywords = []string{"test", "fields"}
	event.Category = "testing"

	err := dualWriter.Write(context.Background(), event)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Verify correct fields were passed
	if len(bleveIndex.fields) != 1 {
		t.Fatalf("Expected 1 field set, got %d", len(bleveIndex.fields))
	}

	// AgentDecision should have: content, keywords, category, agent_id
	expectedFields := map[string]bool{
		"content":  true,
		"keywords": true,
		"category": true,
		"agent_id": true,
	}

	gotFields := make(map[string]bool)
	for _, f := range bleveIndex.fields[0] {
		gotFields[f] = true
	}

	for field := range expectedFields {
		if !gotFields[field] {
			t.Errorf("Missing expected field: %s", field)
		}
	}
}

// TestDualWriteIntegration_CorrectContentTypePassedToVector tests that the
// correct content type is passed to the Vector store.
func TestDualWriteIntegration_CorrectContentTypePassedToVector(t *testing.T) {
	tests := []struct {
		eventType       events.EventType
		expectedContent string
	}{
		{events.EventTypeAgentDecision, "full"},
		{events.EventTypeUserPrompt, "full"},
		{events.EventTypeSuccess, "summary"},
	}

	for _, tt := range tests {
		t.Run(tt.eventType.String(), func(t *testing.T) {
			bleveIndex := newMockBleveEventIndex()
			vectorStore := newMockVectorEventStore()
			dualWriter := NewDualWriter(bleveIndex, vectorStore)

			event := createDualWriteTestEvent(tt.eventType, "Content type test")
			event.Summary = "Test summary"

			err := dualWriter.Write(context.Background(), event)
			if err != nil {
				t.Fatalf("Write failed: %v", err)
			}

			time.Sleep(50 * time.Millisecond)

			if vectorStore.getStoredCount() > 0 && len(vectorStore.contentTypes) > 0 {
				if vectorStore.contentTypes[0] != tt.expectedContent {
					t.Errorf("Expected content type '%s', got '%s'", tt.expectedContent, vectorStore.contentTypes[0])
				}
			}
		})
	}
}

// TestDualWriteIntegration_CloseFlushesAndCleans tests that Close() properly
// flushes pending events and closes stores.
func TestDualWriteIntegration_CloseFlushesAndCleans(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	dualWriter := NewDualWriter(bleveIndex, vectorStore)

	// Add aggregated events
	for i := 0; i < 3; i++ {
		event := createDualWriteTestEvent(events.EventTypeToolCall, "Close test")
		_ = dualWriter.Write(context.Background(), event)
	}

	// Verify events are aggregating
	if bleveIndex.getIndexedCount() != 0 {
		t.Errorf("Expected events to be aggregating, got %d", bleveIndex.getIndexedCount())
	}

	// Close the writer
	err := dualWriter.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Aggregated events should be flushed
	if bleveIndex.getIndexedCount() != 1 {
		t.Errorf("Expected 1 Bleve index after close (flushed aggregate), got %d", bleveIndex.getIndexedCount())
	}
}

// =============================================================================
// Mock Implementations with Delays
// =============================================================================

// delayedMockBleveEventIndex adds artificial delay to simulate I/O.
type delayedMockBleveEventIndex struct {
	*mockBleveEventIndex
	delay time.Duration
}

func newDelayedMockBleveEventIndex(delay time.Duration) *delayedMockBleveEventIndex {
	return &delayedMockBleveEventIndex{
		mockBleveEventIndex: newMockBleveEventIndex(),
		delay:               delay,
	}
}

func (m *delayedMockBleveEventIndex) IndexEvent(ctx context.Context, event *events.ActivityEvent, fields []string) error {
	time.Sleep(m.delay)
	return m.mockBleveEventIndex.IndexEvent(ctx, event, fields)
}

// delayedMockVectorEventStore adds artificial delay to simulate I/O.
type delayedMockVectorEventStore struct {
	*mockVectorEventStore
	delay time.Duration
}

func newDelayedMockVectorEventStore(delay time.Duration) *delayedMockVectorEventStore {
	return &delayedMockVectorEventStore{
		mockVectorEventStore: newMockVectorEventStore(),
		delay:                delay,
	}
}

func (m *delayedMockVectorEventStore) EmbedEvent(ctx context.Context, event *events.ActivityEvent, contentField string) error {
	time.Sleep(m.delay)
	return m.mockVectorEventStore.EmbedEvent(ctx, event, contentField)
}

// =============================================================================
// Helper Functions
// =============================================================================

func createDualWriteTestEvent(eventType events.EventType, content string) *events.ActivityEvent {
	event := events.NewActivityEvent(eventType, "test-session-dual", content)
	event.AgentID = "test-agent-dual"
	return event
}
