package archivalist

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.1 DualWriteStrategy Tests
// =============================================================================

func TestDualWriteStrategy_Structure(t *testing.T) {
	strategy := &DualWriteStrategy{
		WriteBleve:    true,
		WriteVector:   true,
		BleveFields:   []string{"content", "keywords"},
		VectorContent: "full",
		Aggregate:     false,
	}

	if !strategy.WriteBleve {
		t.Error("Expected WriteBleve to be true")
	}
	if !strategy.WriteVector {
		t.Error("Expected WriteVector to be true")
	}
	if len(strategy.BleveFields) != 2 {
		t.Errorf("Expected 2 BleveFields, got %d", len(strategy.BleveFields))
	}
	if strategy.VectorContent != "full" {
		t.Errorf("Expected VectorContent 'full', got '%s'", strategy.VectorContent)
	}
	if strategy.Aggregate {
		t.Error("Expected Aggregate to be false")
	}
}

// =============================================================================
// AE.4.2 EventClassifier Tests
// =============================================================================

func TestNewEventClassifier_Initialization(t *testing.T) {
	classifier := NewEventClassifier()

	if classifier == nil {
		t.Fatal("Expected non-nil EventClassifier")
	}

	if classifier.strategies == nil {
		t.Fatal("Expected strategies map to be initialized")
	}

	// Verify we have strategies for all expected event types
	expectedCount := 17 // Total event types in events package
	if len(classifier.strategies) != expectedCount {
		t.Errorf("Expected %d strategies, got %d", expectedCount, len(classifier.strategies))
	}
}

func TestEventClassifier_GetStrategy_HighValueEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
		wantBleve bool
		wantVec   bool
		wantAgg   bool
	}{
		{
			name:      "AgentDecision - both stores",
			eventType: events.EventTypeAgentDecision,
			wantBleve: true,
			wantVec:   true,
			wantAgg:   false,
		},
		{
			name:      "Failure - both stores",
			eventType: events.EventTypeFailure,
			wantBleve: true,
			wantVec:   true,
			wantAgg:   false,
		},
		{
			name:      "AgentError - both stores",
			eventType: events.EventTypeAgentError,
			wantBleve: true,
			wantVec:   true,
			wantAgg:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			if strategy.WriteBleve != tt.wantBleve {
				t.Errorf("WriteBleve = %v, want %v", strategy.WriteBleve, tt.wantBleve)
			}
			if strategy.WriteVector != tt.wantVec {
				t.Errorf("WriteVector = %v, want %v", strategy.WriteVector, tt.wantVec)
			}
			if strategy.Aggregate != tt.wantAgg {
				t.Errorf("Aggregate = %v, want %v", strategy.Aggregate, tt.wantAgg)
			}
			if strategy.VectorContent != "full" {
				t.Errorf("VectorContent = %s, want 'full'", strategy.VectorContent)
			}
		})
	}
}

func TestEventClassifier_GetStrategy_ToolEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
		wantBleve bool
		wantVec   bool
		wantAgg   bool
	}{
		{
			name:      "ToolCall - Bleve only, aggregated",
			eventType: events.EventTypeToolCall,
			wantBleve: true,
			wantVec:   false,
			wantAgg:   true,
		},
		{
			name:      "ToolResult - Bleve only, aggregated",
			eventType: events.EventTypeToolResult,
			wantBleve: true,
			wantVec:   false,
			wantAgg:   true,
		},
		{
			name:      "ToolTimeout - both stores (error condition)",
			eventType: events.EventTypeToolTimeout,
			wantBleve: true,
			wantVec:   true,
			wantAgg:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			if strategy.WriteBleve != tt.wantBleve {
				t.Errorf("WriteBleve = %v, want %v", strategy.WriteBleve, tt.wantBleve)
			}
			if strategy.WriteVector != tt.wantVec {
				t.Errorf("WriteVector = %v, want %v", strategy.WriteVector, tt.wantVec)
			}
			if strategy.Aggregate != tt.wantAgg {
				t.Errorf("Aggregate = %v, want %v", strategy.Aggregate, tt.wantAgg)
			}
		})
	}
}

func TestEventClassifier_GetStrategy_IndexEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
		wantBleve bool
		wantVec   bool
	}{
		{
			name:      "IndexStart - Bleve only",
			eventType: events.EventTypeIndexStart,
			wantBleve: true,
			wantVec:   false,
		},
		{
			name:      "IndexComplete - Bleve only",
			eventType: events.EventTypeIndexComplete,
			wantBleve: true,
			wantVec:   false,
		},
		{
			name:      "IndexFileAdded - Bleve only",
			eventType: events.EventTypeIndexFileAdded,
			wantBleve: true,
			wantVec:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			if strategy.WriteBleve != tt.wantBleve {
				t.Errorf("WriteBleve = %v, want %v", strategy.WriteBleve, tt.wantBleve)
			}
			if strategy.WriteVector != tt.wantVec {
				t.Errorf("WriteVector = %v, want %v", strategy.WriteVector, tt.wantVec)
			}
			if strategy.Aggregate {
				t.Error("Index events should not aggregate")
			}
		})
	}
}

func TestEventClassifier_GetStrategy_UserEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
	}{
		{
			name:      "UserPrompt",
			eventType: events.EventTypeUserPrompt,
		},
		{
			name:      "UserClarification",
			eventType: events.EventTypeUserClarification,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			// User events should go to both stores for semantic search
			if !strategy.WriteBleve {
				t.Error("Expected WriteBleve to be true for user events")
			}
			if !strategy.WriteVector {
				t.Error("Expected WriteVector to be true for user events")
			}
			if strategy.Aggregate {
				t.Error("User events should not aggregate")
			}
		})
	}
}

func TestEventClassifier_GetStrategy_ContextEvents(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name      string
		eventType events.EventType
	}{
		{
			name:      "ContextEviction",
			eventType: events.EventTypeContextEviction,
		},
		{
			name:      "ContextRestore",
			eventType: events.EventTypeContextRestore,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			// Context events: metadata only, Bleve only
			if !strategy.WriteBleve {
				t.Error("Expected WriteBleve to be true")
			}
			if strategy.WriteVector {
				t.Error("Expected WriteVector to be false for context events")
			}
			if strategy.VectorContent != "" {
				t.Error("Context events should have empty VectorContent")
			}
		})
	}
}

func TestEventClassifier_GetStrategy_BleveFields(t *testing.T) {
	classifier := NewEventClassifier()

	tests := []struct {
		name       string
		eventType  events.EventType
		wantFields []string
	}{
		{
			name:       "AgentDecision - all fields",
			eventType:  events.EventTypeAgentDecision,
			wantFields: []string{"content", "keywords", "category", "agent_id"},
		},
		{
			name:       "ToolCall - structured fields",
			eventType:  events.EventTypeToolCall,
			wantFields: []string{"agent_id", "data"},
		},
		{
			name:       "IndexStart - minimal fields",
			eventType:  events.EventTypeIndexStart,
			wantFields: []string{"session_id", "data"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := classifier.GetStrategy(tt.eventType)

			if len(strategy.BleveFields) != len(tt.wantFields) {
				t.Errorf("Expected %d fields, got %d", len(tt.wantFields), len(strategy.BleveFields))
				return
			}

			fieldMap := make(map[string]bool)
			for _, f := range strategy.BleveFields {
				fieldMap[f] = true
			}

			for _, wantField := range tt.wantFields {
				if !fieldMap[wantField] {
					t.Errorf("Missing expected field: %s", wantField)
				}
			}
		})
	}
}

func TestEventClassifier_GetStrategy_DefaultFallback(t *testing.T) {
	classifier := NewEventClassifier()

	// Use an invalid event type to test default fallback
	invalidType := events.EventType(999)

	strategy := classifier.GetStrategy(invalidType)

	if strategy == nil {
		t.Fatal("Expected non-nil default strategy")
	}

	// Default should write to both stores
	if !strategy.WriteBleve {
		t.Error("Default strategy should write to Bleve")
	}
	if !strategy.WriteVector {
		t.Error("Default strategy should write to Vector")
	}

	// Default should have "full" content
	if strategy.VectorContent != "full" {
		t.Errorf("Default VectorContent = %s, want 'full'", strategy.VectorContent)
	}

	// Default should not aggregate
	if strategy.Aggregate {
		t.Error("Default strategy should not aggregate")
	}
}

func TestEventClassifier_GetStrategy_AllEventTypes(t *testing.T) {
	classifier := NewEventClassifier()

	// Verify all valid event types have strategies
	for _, eventType := range events.ValidEventTypes() {
		t.Run(eventType.String(), func(t *testing.T) {
			strategy := classifier.GetStrategy(eventType)

			if strategy == nil {
				t.Fatal("Expected non-nil strategy")
			}

			// Verify at least one store is enabled
			if !strategy.WriteBleve && !strategy.WriteVector {
				t.Error("At least one store should be enabled")
			}

			// If WriteVector is true, VectorContent should not be empty
			if strategy.WriteVector && strategy.VectorContent == "" {
				t.Error("WriteVector is true but VectorContent is empty")
			}
		})
	}
}

// =============================================================================
// Mock Implementations for Testing
// =============================================================================

// mockBleveEventIndex implements BleveEventIndexer for testing DualWriter.
type mockBleveEventIndex struct {
	mu           sync.Mutex
	indexedCount int32
	events       []*events.ActivityEvent
	fields       [][]string
	indexErr     error
	closeErr     error
}

func newMockBleveEventIndex() *mockBleveEventIndex {
	return &mockBleveEventIndex{
		events: make([]*events.ActivityEvent, 0),
		fields: make([][]string, 0),
	}
}

func (m *mockBleveEventIndex) IndexEvent(ctx context.Context, event *events.ActivityEvent, fields []string) error {
	if m.indexErr != nil {
		return m.indexErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	m.fields = append(m.fields, fields)
	atomic.AddInt32(&m.indexedCount, 1)
	return nil
}

func (m *mockBleveEventIndex) Close() error {
	return m.closeErr
}

func (m *mockBleveEventIndex) getIndexedCount() int32 {
	return atomic.LoadInt32(&m.indexedCount)
}

// mockVectorEventStore implements VectorEventStorer for testing DualWriter.
type mockVectorEventStore struct {
	mu           sync.Mutex
	storedCount  int32
	events       []*events.ActivityEvent
	contentTypes []string
	storeErr     error
}

func newMockVectorEventStore() *mockVectorEventStore {
	return &mockVectorEventStore{
		events:       make([]*events.ActivityEvent, 0),
		contentTypes: make([]string, 0),
	}
}

func (m *mockVectorEventStore) EmbedEvent(ctx context.Context, event *events.ActivityEvent, contentField string) error {
	if m.storeErr != nil {
		return m.storeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	m.contentTypes = append(m.contentTypes, contentField)
	atomic.AddInt32(&m.storedCount, 1)
	return nil
}

func (m *mockVectorEventStore) getStoredCount() int32 {
	return atomic.LoadInt32(&m.storedCount)
}

// =============================================================================
// AE.4.6 DualWriter Tests
// =============================================================================

func TestNewDualWriter_Initialization(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()

	writer := NewDualWriter(bleveIndex, vectorStore)

	if writer == nil {
		t.Fatal("Expected non-nil DualWriter")
	}

	if writer.bleveIndex == nil {
		t.Error("Expected bleveIndex to be set")
	}

	if writer.vectorStore == nil {
		t.Error("Expected vectorStore to be set")
	}

	if writer.classifier == nil {
		t.Error("Expected classifier to be initialized")
	}

	if writer.aggregator == nil {
		t.Error("Expected aggregator to be initialized")
	}
}

func TestDualWriter_Write_BothStores(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	writer := NewDualWriter(bleveIndex, vectorStore)

	// AgentDecision should write to both stores
	event := events.NewActivityEvent(
		events.EventTypeAgentDecision,
		"session-1",
		"Decision content",
	)
	event.AgentID = "agent-1"

	err := writer.Write(context.Background(), event)

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

func TestDualWriter_Write_BleveOnly(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	writer := NewDualWriter(bleveIndex, vectorStore)

	// IndexStart should write to Bleve only (no aggregation)
	event := events.NewActivityEvent(
		events.EventTypeIndexStart,
		"session-1",
		"Index started",
	)

	err := writer.Write(context.Background(), event)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Wait a bit for parallel writes to complete
	time.Sleep(50 * time.Millisecond)

	if bleveIndex.getIndexedCount() != 1 {
		t.Errorf("Expected 1 Bleve index, got %d", bleveIndex.getIndexedCount())
	}

	if vectorStore.getStoredCount() != 0 {
		t.Errorf("Expected 0 Vector stores, got %d", vectorStore.getStoredCount())
	}
}

func TestDualWriter_Write_WithAggregation(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	writer := NewDualWriter(bleveIndex, vectorStore)

	// ToolCall should aggregate (no immediate write)
	event := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call content",
	)
	event.AgentID = "agent-1"

	err := writer.Write(context.Background(), event)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should not write immediately due to aggregation
	time.Sleep(50 * time.Millisecond)

	if bleveIndex.getIndexedCount() != 0 {
		t.Errorf("Expected 0 Bleve index (aggregating), got %d", bleveIndex.getIndexedCount())
	}

	// Flush should write the aggregated event
	err = writer.Flush(context.Background())

	if err != nil {
		t.Fatalf("Unexpected error on flush: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	if bleveIndex.getIndexedCount() != 1 {
		t.Errorf("Expected 1 Bleve index after flush, got %d", bleveIndex.getIndexedCount())
	}
}

func TestDualWriter_Write_MultipleAggregatedEvents(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	writer := NewDualWriter(bleveIndex, vectorStore)

	// Add multiple tool calls (should be aggregated)
	for i := 0; i < 5; i++ {
		event := events.NewActivityEvent(
			events.EventTypeToolCall,
			"session-1",
			"Tool call content",
		)
		event.AgentID = "agent-1"

		err := writer.Write(context.Background(), event)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// Should not write immediately due to aggregation
	time.Sleep(50 * time.Millisecond)

	if bleveIndex.getIndexedCount() != 0 {
		t.Errorf("Expected 0 Bleve index (aggregating), got %d", bleveIndex.getIndexedCount())
	}

	// Flush should write a single aggregated event
	err := writer.Flush(context.Background())

	if err != nil {
		t.Fatalf("Unexpected error on flush: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Should have 1 aggregated event
	if bleveIndex.getIndexedCount() != 1 {
		t.Errorf("Expected 1 Bleve index after flush (aggregated), got %d", bleveIndex.getIndexedCount())
	}

	// Verify the event has aggregation data
	if len(bleveIndex.events) > 0 {
		aggEvent := bleveIndex.events[0]
		if aggEvent.Data == nil {
			t.Error("Expected non-nil Data in aggregated event")
		} else if !aggEvent.Data["aggregated"].(bool) {
			t.Error("Expected aggregated flag to be true")
		} else if aggEvent.Data["event_count"].(int) != 5 {
			t.Errorf("Expected event_count 5, got %v", aggEvent.Data["event_count"])
		}
	}
}

func TestDualWriter_Flush_Empty(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	writer := NewDualWriter(bleveIndex, vectorStore)

	err := writer.Flush(context.Background())

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if bleveIndex.getIndexedCount() != 0 {
		t.Errorf("Expected 0 Bleve index, got %d", bleveIndex.getIndexedCount())
	}
}

func TestDualWriter_Close(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	writer := NewDualWriter(bleveIndex, vectorStore)

	// Add an aggregated event
	event := events.NewActivityEvent(
		events.EventTypeToolCall,
		"session-1",
		"Tool call content",
	)
	event.AgentID = "agent-1"
	_ = writer.Write(context.Background(), event)

	// Close should flush and close stores
	err := writer.Close()

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should have flushed the pending event
	if bleveIndex.getIndexedCount() != 1 {
		t.Errorf("Expected 1 Bleve index after close, got %d", bleveIndex.getIndexedCount())
	}
}

func TestDualWriter_Write_NilStores(t *testing.T) {
	// Test with nil Bleve index
	writer1 := NewDualWriter(nil, newMockVectorEventStore())
	event := events.NewActivityEvent(
		events.EventTypeAgentDecision,
		"session-1",
		"Decision content",
	)

	err := writer1.Write(context.Background(), event)
	if err != nil {
		t.Fatalf("Unexpected error with nil Bleve: %v", err)
	}

	// Test with nil Vector store
	writer2 := NewDualWriter(newMockBleveEventIndex(), nil)
	err = writer2.Write(context.Background(), event)
	if err != nil {
		t.Fatalf("Unexpected error with nil Vector: %v", err)
	}

	// Test with both nil
	writer3 := NewDualWriter(nil, nil)
	err = writer3.Write(context.Background(), event)
	if err != nil {
		t.Fatalf("Unexpected error with both nil: %v", err)
	}
}

func TestDualWriter_Write_CorrectFields(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	writer := NewDualWriter(bleveIndex, vectorStore)

	// AgentDecision should have specific fields
	event := events.NewActivityEvent(
		events.EventTypeAgentDecision,
		"session-1",
		"Decision content",
	)
	event.AgentID = "agent-1"

	err := writer.Write(context.Background(), event)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Verify correct fields were passed to Bleve
	if len(bleveIndex.fields) != 1 {
		t.Fatalf("Expected 1 field set, got %d", len(bleveIndex.fields))
	}

	expectedFields := []string{"content", "keywords", "category", "agent_id"}
	gotFields := bleveIndex.fields[0]

	if len(gotFields) != len(expectedFields) {
		t.Errorf("Expected %d fields, got %d", len(expectedFields), len(gotFields))
	}

	// Verify content type for vector store
	if len(vectorStore.contentTypes) != 1 {
		t.Fatalf("Expected 1 content type, got %d", len(vectorStore.contentTypes))
	}

	if vectorStore.contentTypes[0] != "full" {
		t.Errorf("Expected content type 'full', got '%s'", vectorStore.contentTypes[0])
	}
}

func TestDualWriter_ConcurrentWrites(t *testing.T) {
	bleveIndex := newMockBleveEventIndex()
	vectorStore := newMockVectorEventStore()
	writer := NewDualWriter(bleveIndex, vectorStore)

	var wg sync.WaitGroup
	numGoroutines := 10
	eventsPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				event := events.NewActivityEvent(
					events.EventTypeAgentDecision,
					"session-1",
					"Decision content",
				)
				event.AgentID = "agent-1"
				_ = writer.Write(context.Background(), event)
			}
		}()
	}

	wg.Wait()

	// Wait for all writes to complete
	time.Sleep(100 * time.Millisecond)

	expectedCount := int32(numGoroutines * eventsPerGoroutine)
	if bleveIndex.getIndexedCount() != expectedCount {
		t.Errorf("Expected %d Bleve indexes, got %d", expectedCount, bleveIndex.getIndexedCount())
	}

	if vectorStore.getStoredCount() != expectedCount {
		t.Errorf("Expected %d Vector stores, got %d", expectedCount, vectorStore.getStoredCount())
	}
}
