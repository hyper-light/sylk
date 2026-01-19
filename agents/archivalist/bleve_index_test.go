package archivalist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search"
)

// =============================================================================
// AE.4.4 BleveEventIndex Tests
// =============================================================================

// MockBleveIndex implements BleveIndex for testing.
type MockBleveIndex struct {
	indexed      map[string]interface{}
	searchResult *bleve.SearchResult
	indexErr     error
	searchErr    error
	closeErr     error
	closed       bool
}

func NewMockBleveIndex() *MockBleveIndex {
	return &MockBleveIndex{
		indexed: make(map[string]interface{}),
	}
}

func (m *MockBleveIndex) Index(id string, data interface{}) error {
	if m.indexErr != nil {
		return m.indexErr
	}
	m.indexed[id] = data
	return nil
}

func (m *MockBleveIndex) Search(req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	if m.searchResult != nil {
		return m.searchResult, nil
	}
	// Return empty result by default
	return &bleve.SearchResult{
		Hits: search.DocumentMatchCollection{},
	}, nil
}

func (m *MockBleveIndex) Close() error {
	if m.closeErr != nil {
		return m.closeErr
	}
	m.closed = true
	return nil
}

func (m *MockBleveIndex) SetSearchResult(ids ...string) {
	hits := make(search.DocumentMatchCollection, len(ids))
	for i, id := range ids {
		hits[i] = &search.DocumentMatch{ID: id}
	}
	m.searchResult = &bleve.SearchResult{
		Hits:  hits,
		Total: uint64(len(ids)),
	}
}

// =============================================================================
// BleveEventIndex Structure Tests
// =============================================================================

func TestNewBleveEventIndex(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	if bleveIndex == nil {
		t.Fatal("Expected non-nil BleveEventIndex")
	}

	if bleveIndex.index != mockIndex {
		t.Error("Expected index to be set to mock")
	}

	if bleveIndex.eventCache == nil {
		t.Error("Expected eventCache to be initialized")
	}
}

func TestNewBleveEventIndex_NilIndex(t *testing.T) {
	bleveIndex := NewBleveEventIndex(nil)

	if bleveIndex == nil {
		t.Fatal("Expected non-nil BleveEventIndex even with nil index")
	}

	if bleveIndex.index != nil {
		t.Error("Expected index to be nil")
	}
}

// =============================================================================
// IndexEvent Tests
// =============================================================================

func TestBleveEventIndex_IndexEvent_Success(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	event := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")
	event.Summary = "Test summary"
	event.Keywords = []string{"test", "decision"}
	event.Category = "testing"

	err := bleveIndex.IndexEvent(context.Background(), event, []string{"content", "summary", "keywords", "category"})
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify document was indexed
	doc, ok := mockIndex.indexed[event.ID]
	if !ok {
		t.Fatal("Expected event to be indexed")
	}

	// Verify document fields
	docMap, ok := doc.(map[string]interface{})
	if !ok {
		t.Fatal("Expected document to be a map")
	}

	if docMap["content"] != "Test content" {
		t.Errorf("Expected content 'Test content', got '%v'", docMap["content"])
	}

	if docMap["summary"] != "Test summary" {
		t.Errorf("Expected summary 'Test summary', got '%v'", docMap["summary"])
	}

	// Verify event is cached
	cachedEvent := bleveIndex.GetCachedEvent(event.ID)
	if cachedEvent == nil {
		t.Error("Expected event to be cached")
	}
}

func TestBleveEventIndex_IndexEvent_NilEvent(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	err := bleveIndex.IndexEvent(context.Background(), nil, []string{"content"})
	if err == nil {
		t.Fatal("Expected error for nil event")
	}

	if err.Error() != "event cannot be nil" {
		t.Errorf("Expected 'event cannot be nil' error, got: %v", err)
	}
}

func TestBleveEventIndex_IndexEvent_NilIndex(t *testing.T) {
	bleveIndex := NewBleveEventIndex(nil)
	event := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")

	err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
	if err == nil {
		t.Fatal("Expected error for nil index")
	}

	if err.Error() != "bleve index is not initialized" {
		t.Errorf("Expected 'bleve index is not initialized' error, got: %v", err)
	}
}

func TestBleveEventIndex_IndexEvent_IndexError(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	mockIndex.indexErr = errors.New("index failed")
	bleveIndex := NewBleveEventIndex(mockIndex)

	event := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")

	err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
	if err == nil {
		t.Fatal("Expected error when index fails")
	}

	if !errors.Is(err, mockIndex.indexErr) && err.Error() != "failed to index event test-1: index failed" {
		t.Errorf("Expected wrapped error, got: %v", err)
	}
}

func TestBleveEventIndex_IndexEvent_ContextCanceled(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	event := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")

	err := bleveIndex.IndexEvent(ctx, event, []string{"content"})
	if err == nil {
		t.Fatal("Expected error for canceled context")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled error, got: %v", err)
	}
}

func TestBleveEventIndex_IndexEvent_FieldSelection(t *testing.T) {
	tests := []struct {
		name       string
		fields     []string
		wantFields map[string]bool
	}{
		{
			name:   "content only",
			fields: []string{"content"},
			wantFields: map[string]bool{
				"content": true,
				"summary": false,
			},
		},
		{
			name:   "summary only",
			fields: []string{"summary"},
			wantFields: map[string]bool{
				"content": false,
				"summary": true,
			},
		},
		{
			name:   "multiple fields",
			fields: []string{"content", "summary", "keywords", "agent_id"},
			wantFields: map[string]bool{
				"content":  true,
				"summary":  true,
				"keywords": true,
				"agent_id": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockIndex := NewMockBleveIndex()
			bleveIndex := NewBleveEventIndex(mockIndex)

			event := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")
			event.Summary = "Test summary"
			event.Keywords = []string{"test"}
			event.AgentID = "agent-1"

			err := bleveIndex.IndexEvent(context.Background(), event, tt.fields)
			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}

			doc := mockIndex.indexed[event.ID].(map[string]interface{})

			for field, shouldExist := range tt.wantFields {
				_, exists := doc[field]
				if shouldExist && !exists {
					t.Errorf("Expected field '%s' to exist in document", field)
				}
				if !shouldExist && exists {
					t.Errorf("Expected field '%s' to not exist in document", field)
				}
			}
		})
	}
}

func TestBleveEventIndex_IndexEvent_DataField(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	event := createTestEvent("test-1", events.EventTypeToolCall, "Test content")
	event.Data = map[string]any{
		"tool":   "read_file",
		"params": map[string]any{"path": "/test/file.go"},
	}

	err := bleveIndex.IndexEvent(context.Background(), event, []string{"data"})
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	doc := mockIndex.indexed[event.ID].(map[string]interface{})
	data, ok := doc["data"].(string)
	if !ok {
		t.Fatal("Expected data field to be a string")
	}

	// Data should be JSON serialized
	if data == "" {
		t.Error("Expected non-empty data field")
	}
}

// =============================================================================
// Search Tests
// =============================================================================

func TestBleveEventIndex_Search_Success(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	// Index some events first
	event1 := createTestEvent("event-1", events.EventTypeAgentDecision, "Decision about architecture")
	event2 := createTestEvent("event-2", events.EventTypeAgentDecision, "Decision about testing")

	bleveIndex.IndexEvent(context.Background(), event1, []string{"content"})
	bleveIndex.IndexEvent(context.Background(), event2, []string{"content"})

	// Set up mock search result
	mockIndex.SetSearchResult("event-1", "event-2")

	// Search
	results, err := bleveIndex.Search(context.Background(), "decision", 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
}

func TestBleveEventIndex_Search_NilIndex(t *testing.T) {
	bleveIndex := NewBleveEventIndex(nil)

	_, err := bleveIndex.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("Expected error for nil index")
	}
}

func TestBleveEventIndex_Search_SearchError(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	mockIndex.searchErr = errors.New("search failed")
	bleveIndex := NewBleveEventIndex(mockIndex)

	_, err := bleveIndex.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("Expected error when search fails")
	}
}

func TestBleveEventIndex_Search_ContextCanceled(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := bleveIndex.Search(ctx, "test", 10)
	if err == nil {
		t.Fatal("Expected error for canceled context")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled error, got: %v", err)
	}
}

func TestBleveEventIndex_Search_DefaultLimit(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	// Search with limit 0 should use default
	_, err := bleveIndex.Search(context.Background(), "test", 0)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Search with negative limit should use default
	_, err = bleveIndex.Search(context.Background(), "test", -1)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestBleveEventIndex_Search_EmptyResults(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	// No search result set, should return empty
	results, err := bleveIndex.Search(context.Background(), "nonexistent", 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}
}

// =============================================================================
// SearchWithFilters Tests
// =============================================================================

func TestBleveEventIndex_SearchWithFilters_Success(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	// Index events
	event1 := createTestEvent("event-1", events.EventTypeAgentDecision, "Test content")
	event1.SessionID = "session-1"
	bleveIndex.IndexEvent(context.Background(), event1, []string{"content"})

	mockIndex.SetSearchResult("event-1")

	filters := map[string]string{
		"session_id": "session-1",
		"event_type": "agent_decision",
	}

	results, err := bleveIndex.SearchWithFilters(context.Background(), "test", filters, 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestBleveEventIndex_SearchWithFilters_NoQuery(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	event1 := createTestEvent("event-1", events.EventTypeAgentDecision, "Test content")
	bleveIndex.IndexEvent(context.Background(), event1, []string{"content"})

	mockIndex.SetSearchResult("event-1")

	// Empty query with filters should still work
	filters := map[string]string{
		"event_type": "agent_decision",
	}

	results, err := bleveIndex.SearchWithFilters(context.Background(), "", filters, 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestBleveEventIndex_SearchWithFilters_NoFilters(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	event1 := createTestEvent("event-1", events.EventTypeAgentDecision, "Test content")
	bleveIndex.IndexEvent(context.Background(), event1, []string{"content"})

	mockIndex.SetSearchResult("event-1")

	// Query with no filters should work
	results, err := bleveIndex.SearchWithFilters(context.Background(), "test", nil, 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

func TestBleveEventIndex_SearchWithFilters_EmptyQueryAndFilters(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	event1 := createTestEvent("event-1", events.EventTypeAgentDecision, "Test content")
	bleveIndex.IndexEvent(context.Background(), event1, []string{"content"})

	mockIndex.SetSearchResult("event-1")

	// Empty query and no filters should match all
	results, err := bleveIndex.SearchWithFilters(context.Background(), "", nil, 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestBleveEventIndex_Close_Success(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	err := bleveIndex.Close()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !mockIndex.closed {
		t.Error("Expected index to be closed")
	}
}

func TestBleveEventIndex_Close_NilIndex(t *testing.T) {
	bleveIndex := NewBleveEventIndex(nil)

	err := bleveIndex.Close()
	if err != nil {
		t.Fatalf("Expected no error for nil index, got: %v", err)
	}
}

func TestBleveEventIndex_Close_Error(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	mockIndex.closeErr = errors.New("close failed")
	bleveIndex := NewBleveEventIndex(mockIndex)

	err := bleveIndex.Close()
	if err == nil {
		t.Fatal("Expected error when close fails")
	}
}

// =============================================================================
// Cache Tests
// =============================================================================

func TestBleveEventIndex_Cache_Operations(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex)

	// Initially empty
	if bleveIndex.CacheSize() != 0 {
		t.Errorf("Expected cache size 0, got %d", bleveIndex.CacheSize())
	}

	// Index adds to cache
	event1 := createTestEvent("event-1", events.EventTypeAgentDecision, "Test content")
	bleveIndex.IndexEvent(context.Background(), event1, []string{"content"})

	if bleveIndex.CacheSize() != 1 {
		t.Errorf("Expected cache size 1, got %d", bleveIndex.CacheSize())
	}

	// Get cached event
	cached := bleveIndex.GetCachedEvent("event-1")
	if cached == nil {
		t.Error("Expected to find cached event")
	}

	if cached.ID != event1.ID {
		t.Error("Expected cached event to match original")
	}

	// Get non-existent event
	nonExistent := bleveIndex.GetCachedEvent("non-existent")
	if nonExistent != nil {
		t.Error("Expected nil for non-existent event")
	}

	// Clear cache
	bleveIndex.ClearCache()
	if bleveIndex.CacheSize() != 0 {
		t.Errorf("Expected cache size 0 after clear, got %d", bleveIndex.CacheSize())
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func createTestEvent(id string, eventType events.EventType, content string) *events.ActivityEvent {
	return &events.ActivityEvent{
		ID:         id,
		EventType:  eventType,
		Timestamp:  time.Now(),
		SessionID:  "test-session",
		Content:    content,
		Outcome:    events.OutcomePending,
		Importance: 0.5,
		Data:       make(map[string]any),
	}
}
