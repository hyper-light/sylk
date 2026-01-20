package archivalist

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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
	mu           sync.RWMutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.indexErr != nil {
		return m.indexErr
	}
	m.indexed[id] = data
	return nil
}

func (m *MockBleveIndex) Search(req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeErr != nil {
		return m.closeErr
	}
	m.closed = true
	return nil
}

func (m *MockBleveIndex) SetSearchResult(ids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(nil, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

	err := bleveIndex.IndexEvent(context.Background(), nil, []string{"content"})
	if err == nil {
		t.Fatal("Expected error for nil event")
	}

	if err.Error() != "event cannot be nil" {
		t.Errorf("Expected 'event cannot be nil' error, got: %v", err)
	}
}

func TestBleveEventIndex_IndexEvent_NilIndex(t *testing.T) {
	bleveIndex := NewBleveEventIndex(nil, 0)
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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
			bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(nil, 0)

	_, err := bleveIndex.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("Expected error for nil index")
	}
}

func TestBleveEventIndex_Search_SearchError(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	mockIndex.searchErr = errors.New("search failed")
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

	_, err := bleveIndex.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("Expected error when search fails")
	}
}

func TestBleveEventIndex_Search_ContextCanceled(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

	err := bleveIndex.Close()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if !mockIndex.closed {
		t.Error("Expected index to be closed")
	}
}

func TestBleveEventIndex_Close_NilIndex(t *testing.T) {
	bleveIndex := NewBleveEventIndex(nil, 0)

	err := bleveIndex.Close()
	if err != nil {
		t.Fatalf("Expected no error for nil index, got: %v", err)
	}
}

func TestBleveEventIndex_Close_Error(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	mockIndex.closeErr = errors.New("close failed")
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
	bleveIndex := NewBleveEventIndex(mockIndex, 0)

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
// W4H.3 LRU Cache Tests
// =============================================================================

func TestBleveEventIndex_LRU_CacheHitMiss(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 100)

	// Cache miss for non-existent event
	event := bleveIndex.GetCachedEvent("non-existent")
	if event != nil {
		t.Error("Expected nil for cache miss")
	}

	// Index an event (cache hit after indexing)
	testEvent := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")
	err := bleveIndex.IndexEvent(context.Background(), testEvent, []string{"content"})
	if err != nil {
		t.Fatalf("Failed to index event: %v", err)
	}

	// Cache hit
	cachedEvent := bleveIndex.GetCachedEvent("test-1")
	if cachedEvent == nil {
		t.Error("Expected cache hit after indexing")
	}
	if cachedEvent.ID != testEvent.ID {
		t.Error("Cached event ID mismatch")
	}

	// Verify cache size
	if bleveIndex.CacheSize() != 1 {
		t.Errorf("Expected cache size 1, got %d", bleveIndex.CacheSize())
	}
}

func TestBleveEventIndex_LRU_Eviction(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	maxSize := 5
	bleveIndex := NewBleveEventIndex(mockIndex, maxSize)

	// Fill cache beyond capacity
	for i := 0; i < maxSize+3; i++ {
		event := createTestEvent(
			fmt.Sprintf("event-%d", i),
			events.EventTypeAgentDecision,
			fmt.Sprintf("Content %d", i),
		)
		err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
		if err != nil {
			t.Fatalf("Failed to index event %d: %v", i, err)
		}
	}

	// Verify cache size is bounded
	if bleveIndex.CacheSize() != maxSize {
		t.Errorf("Expected cache size %d, got %d", maxSize, bleveIndex.CacheSize())
	}

	// Oldest events should be evicted (event-0, event-1, event-2)
	for i := 0; i < 3; i++ {
		evicted := bleveIndex.GetCachedEvent(fmt.Sprintf("event-%d", i))
		if evicted != nil {
			t.Errorf("Expected event-%d to be evicted, but it was found in cache", i)
		}
	}

	// Newest events should still be in cache
	for i := 3; i < maxSize+3; i++ {
		cached := bleveIndex.GetCachedEvent(fmt.Sprintf("event-%d", i))
		if cached == nil {
			t.Errorf("Expected event-%d to be in cache, but it was not found", i)
		}
	}
}

func TestBleveEventIndex_LRU_Concurrent(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 1000)

	const numGoroutines = 10
	const eventsPerGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Concurrent indexing
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				event := createTestEvent(
					fmt.Sprintf("g%d-event-%d", goroutineID, i),
					events.EventTypeAgentDecision,
					fmt.Sprintf("Content from goroutine %d event %d", goroutineID, i),
				)
				err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
				if err != nil {
					t.Errorf("Failed to index event: %v", err)
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify all events were indexed (cache size should equal total events)
	expectedSize := numGoroutines * eventsPerGoroutine
	if bleveIndex.CacheSize() != expectedSize {
		t.Errorf("Expected cache size %d, got %d", expectedSize, bleveIndex.CacheSize())
	}
}

func TestBleveEventIndex_LRU_ConcurrentReadWrite(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 500)

	const numWriters = 5
	const numReaders = 5
	const opsPerGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(numWriters + numReaders)

	// Writers
	for w := 0; w < numWriters; w++ {
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				event := createTestEvent(
					fmt.Sprintf("w%d-event-%d", writerID, i),
					events.EventTypeAgentDecision,
					fmt.Sprintf("Writer %d content %d", writerID, i),
				)
				_ = bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
			}
		}(w)
	}

	// Readers
	for r := 0; r < numReaders; r++ {
		go func(readerID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				// Read random events
				_ = bleveIndex.GetCachedEvent(fmt.Sprintf("w%d-event-%d", readerID%numWriters, i%opsPerGoroutine))
				_ = bleveIndex.CacheSize()
			}
		}(r)
	}

	wg.Wait()

	// Verify cache is still functional
	if bleveIndex.CacheSize() > 500 {
		t.Errorf("Cache exceeded max size: %d", bleveIndex.CacheSize())
	}
}

func TestBleveEventIndex_LRU_MemoryStability(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	maxSize := 100
	bleveIndex := NewBleveEventIndex(mockIndex, maxSize)

	// Index many events (10x the cache size) to ensure stable memory
	totalEvents := maxSize * 10
	for i := 0; i < totalEvents; i++ {
		event := createTestEvent(
			fmt.Sprintf("event-%d", i),
			events.EventTypeAgentDecision,
			fmt.Sprintf("Content %d with some additional text to make it larger", i),
		)
		err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
		if err != nil {
			t.Fatalf("Failed to index event %d: %v", i, err)
		}

		// Periodically verify cache size is bounded
		if i > 0 && i%100 == 0 {
			size := bleveIndex.CacheSize()
			if size > maxSize {
				t.Errorf("Cache exceeded max size at iteration %d: %d > %d", i, size, maxSize)
			}
		}
	}

	// Final verification
	finalSize := bleveIndex.CacheSize()
	if finalSize != maxSize {
		t.Errorf("Expected final cache size %d, got %d", maxSize, finalSize)
	}

	// Verify only the most recent events are in cache
	for i := totalEvents - maxSize; i < totalEvents; i++ {
		cached := bleveIndex.GetCachedEvent(fmt.Sprintf("event-%d", i))
		if cached == nil {
			t.Errorf("Expected event-%d to be in cache", i)
		}
	}
}

func TestBleveEventIndex_LRU_DefaultSize(t *testing.T) {
	mockIndex := NewMockBleveIndex()

	// Test with 0 (should use default)
	bleveIndex := NewBleveEventIndex(mockIndex, 0)
	if bleveIndex == nil {
		t.Fatal("Expected non-nil BleveEventIndex with size 0")
	}

	// Test with negative (should use default)
	bleveIndex = NewBleveEventIndex(mockIndex, -1)
	if bleveIndex == nil {
		t.Fatal("Expected non-nil BleveEventIndex with negative size")
	}
}

func TestBleveEventIndex_LRU_UpdateExisting(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 10)

	// Index initial event
	event1 := createTestEvent("test-1", events.EventTypeAgentDecision, "Initial content")
	err := bleveIndex.IndexEvent(context.Background(), event1, []string{"content"})
	if err != nil {
		t.Fatalf("Failed to index event: %v", err)
	}

	// Update the same event
	event2 := createTestEvent("test-1", events.EventTypeAgentDecision, "Updated content")
	err = bleveIndex.IndexEvent(context.Background(), event2, []string{"content"})
	if err != nil {
		t.Fatalf("Failed to update event: %v", err)
	}

	// Cache size should still be 1
	if bleveIndex.CacheSize() != 1 {
		t.Errorf("Expected cache size 1 after update, got %d", bleveIndex.CacheSize())
	}

	// Should get updated event
	cached := bleveIndex.GetCachedEvent("test-1")
	if cached == nil {
		t.Error("Expected to find cached event")
	}
	if cached.Content != "Updated content" {
		t.Errorf("Expected updated content, got %s", cached.Content)
	}
}

// =============================================================================
// W4P.37 Cache Statistics Tests
// =============================================================================

func TestBleveEventIndex_Stats_HitsMisses(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 100)

	// Initially all stats should be zero
	stats := bleveIndex.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Evictions != 0 {
		t.Errorf("Expected all stats to be zero, got hits=%d misses=%d evictions=%d",
			stats.Hits, stats.Misses, stats.Evictions)
	}

	// Cache miss for non-existent event
	_ = bleveIndex.GetCachedEvent("non-existent")
	if bleveIndex.Misses() != 1 {
		t.Errorf("Expected 1 miss, got %d", bleveIndex.Misses())
	}

	// Index an event
	event := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")
	err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
	if err != nil {
		t.Fatalf("Failed to index event: %v", err)
	}

	// Cache hit
	_ = bleveIndex.GetCachedEvent("test-1")
	if bleveIndex.Hits() != 1 {
		t.Errorf("Expected 1 hit, got %d", bleveIndex.Hits())
	}

	// Another cache miss
	_ = bleveIndex.GetCachedEvent("another-non-existent")
	if bleveIndex.Misses() != 2 {
		t.Errorf("Expected 2 misses, got %d", bleveIndex.Misses())
	}

	// Verify hit rate calculation
	stats = bleveIndex.Stats()
	expectedHitRate := float64(1) / float64(3) // 1 hit out of 3 total
	if stats.HitRate < expectedHitRate-0.01 || stats.HitRate > expectedHitRate+0.01 {
		t.Errorf("Expected hit rate ~%.2f, got %.2f", expectedHitRate, stats.HitRate)
	}
}

func TestBleveEventIndex_Stats_Evictions(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	maxSize := 5
	bleveIndex := NewBleveEventIndex(mockIndex, maxSize)

	// Fill cache beyond capacity to trigger evictions
	for i := 0; i < maxSize+3; i++ {
		event := createTestEvent(
			fmt.Sprintf("event-%d", i),
			events.EventTypeAgentDecision,
			fmt.Sprintf("Content %d", i),
		)
		err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
		if err != nil {
			t.Fatalf("Failed to index event %d: %v", i, err)
		}
	}

	// Should have 3 evictions (8 events added, max size 5)
	if bleveIndex.Evictions() != 3 {
		t.Errorf("Expected 3 evictions, got %d", bleveIndex.Evictions())
	}

	// Verify stats struct
	stats := bleveIndex.Stats()
	if stats.Evictions != 3 {
		t.Errorf("Expected stats.Evictions=3, got %d", stats.Evictions)
	}
	if stats.Size != maxSize {
		t.Errorf("Expected stats.Size=%d, got %d", maxSize, stats.Size)
	}
}

func TestBleveEventIndex_Stats_ResetStats(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 100)

	// Generate some stats
	event := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")
	_ = bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
	_ = bleveIndex.GetCachedEvent("test-1")         // hit
	_ = bleveIndex.GetCachedEvent("non-existent-1") // miss
	_ = bleveIndex.GetCachedEvent("non-existent-2") // miss

	// Verify stats are non-zero
	if bleveIndex.Hits() != 1 || bleveIndex.Misses() != 2 {
		t.Errorf("Expected hits=1 misses=2, got hits=%d misses=%d",
			bleveIndex.Hits(), bleveIndex.Misses())
	}

	// Reset stats
	bleveIndex.ResetStats()

	// Verify all stats are reset
	stats := bleveIndex.Stats()
	if stats.Hits != 0 || stats.Misses != 0 || stats.Evictions != 0 {
		t.Errorf("Expected all stats to be zero after reset, got hits=%d misses=%d evictions=%d",
			stats.Hits, stats.Misses, stats.Evictions)
	}
}

func TestBleveEventIndex_Stats_SearchHitsMisses(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 100)

	// Index some events
	event1 := createTestEvent("event-1", events.EventTypeAgentDecision, "Decision about architecture")
	event2 := createTestEvent("event-2", events.EventTypeAgentDecision, "Decision about testing")
	_ = bleveIndex.IndexEvent(context.Background(), event1, []string{"content"})
	_ = bleveIndex.IndexEvent(context.Background(), event2, []string{"content"})

	// Set up mock search result with one cached and one not cached
	mockIndex.SetSearchResult("event-1", "event-3") // event-3 not in cache

	// Search - should produce 1 hit (event-1) and 1 miss (event-3)
	_, err := bleveIndex.Search(context.Background(), "decision", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if bleveIndex.Hits() != 1 {
		t.Errorf("Expected 1 hit from search, got %d", bleveIndex.Hits())
	}
	if bleveIndex.Misses() != 1 {
		t.Errorf("Expected 1 miss from search, got %d", bleveIndex.Misses())
	}
}

func TestBleveEventIndex_EvictionCallback(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	maxSize := 5

	// Track evicted events via callback
	var evictedIDs []string
	var evictedEvents []*events.ActivityEvent
	var mu sync.Mutex

	onEvict := func(key string, event *events.ActivityEvent) {
		mu.Lock()
		evictedIDs = append(evictedIDs, key)
		evictedEvents = append(evictedEvents, event)
		mu.Unlock()
	}

	bleveIndex := NewBleveEventIndexWithConfig(BleveEventIndexConfig{
		Index:        mockIndex,
		MaxCacheSize: maxSize,
		OnEvict:      onEvict,
	})

	// Fill cache beyond capacity
	for i := 0; i < maxSize+3; i++ {
		event := createTestEvent(
			fmt.Sprintf("event-%d", i),
			events.EventTypeAgentDecision,
			fmt.Sprintf("Content %d", i),
		)
		err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
		if err != nil {
			t.Fatalf("Failed to index event %d: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	// Should have 3 evictions
	if len(evictedIDs) != 3 {
		t.Errorf("Expected 3 eviction callbacks, got %d", len(evictedIDs))
	}

	// Verify the oldest events were evicted (event-0, event-1, event-2)
	expectedEvicted := []string{"event-0", "event-1", "event-2"}
	for i, expected := range expectedEvicted {
		if i < len(evictedIDs) && evictedIDs[i] != expected {
			t.Errorf("Expected evicted[%d]=%s, got %s", i, expected, evictedIDs[i])
		}
	}

	// Verify the event data was passed to callback
	for i, event := range evictedEvents {
		if event == nil {
			t.Errorf("Expected non-nil event in callback %d", i)
		}
	}
}

func TestBleveEventIndex_SetEvictionCallback(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	maxSize := 3

	bleveIndex := NewBleveEventIndex(mockIndex, maxSize)

	// Initially no callback
	for i := 0; i < maxSize+1; i++ {
		event := createTestEvent(
			fmt.Sprintf("initial-%d", i),
			events.EventTypeAgentDecision,
			fmt.Sprintf("Content %d", i),
		)
		_ = bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
	}

	// Set callback now
	var callbackCalled bool
	bleveIndex.SetEvictionCallback(func(key string, event *events.ActivityEvent) {
		callbackCalled = true
	})

	// Trigger more evictions
	for i := 0; i < maxSize+1; i++ {
		event := createTestEvent(
			fmt.Sprintf("after-%d", i),
			events.EventTypeAgentDecision,
			fmt.Sprintf("Content %d", i),
		)
		_ = bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
	}

	if !callbackCalled {
		t.Error("Expected eviction callback to be called after SetEvictionCallback")
	}
}

func TestBleveEventIndex_Stats_Concurrent(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 1000)

	const numGoroutines = 10
	const opsPerGoroutine = 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Writers
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				event := createTestEvent(
					fmt.Sprintf("g%d-event-%d", goroutineID, i),
					events.EventTypeAgentDecision,
					fmt.Sprintf("Content from goroutine %d event %d", goroutineID, i),
				)
				_ = bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
			}
		}(g)
	}

	// Readers (some hits, some misses)
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				// Mix of existing and non-existing keys
				if i%2 == 0 {
					_ = bleveIndex.GetCachedEvent(fmt.Sprintf("g%d-event-%d", goroutineID, i/2))
				} else {
					_ = bleveIndex.GetCachedEvent(fmt.Sprintf("nonexistent-%d-%d", goroutineID, i))
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify stats are consistent
	stats := bleveIndex.Stats()
	total := stats.Hits + stats.Misses
	if total == 0 {
		t.Error("Expected some cache operations to be recorded")
	}

	// Hit rate should be between 0 and 1
	if stats.HitRate < 0 || stats.HitRate > 1 {
		t.Errorf("Invalid hit rate: %.2f", stats.HitRate)
	}
}

func TestBleveEventIndex_Stats_HitRateZeroTotal(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	bleveIndex := NewBleveEventIndex(mockIndex, 100)

	// No operations yet - hit rate should be 0, not NaN
	stats := bleveIndex.Stats()
	if stats.HitRate != 0 {
		t.Errorf("Expected hit rate 0 with zero operations, got %.2f", stats.HitRate)
	}
}

func TestBleveEventIndex_NewWithConfig(t *testing.T) {
	mockIndex := NewMockBleveIndex()

	// Test with config
	config := BleveEventIndexConfig{
		Index:        mockIndex,
		MaxCacheSize: 50,
	}
	bleveIndex := NewBleveEventIndexWithConfig(config)

	if bleveIndex == nil {
		t.Fatal("Expected non-nil BleveEventIndex")
	}

	// Verify it works correctly
	event := createTestEvent("test-1", events.EventTypeAgentDecision, "Test content")
	err := bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
	if err != nil {
		t.Fatalf("Failed to index event: %v", err)
	}

	cached := bleveIndex.GetCachedEvent("test-1")
	if cached == nil {
		t.Error("Expected to find cached event")
	}
}

func TestBleveEventIndex_EvictionCallback_Concurrent(t *testing.T) {
	mockIndex := NewMockBleveIndex()
	maxSize := 50

	var callbackCount atomic.Int64

	onEvict := func(key string, event *events.ActivityEvent) {
		callbackCount.Add(1)
	}

	bleveIndex := NewBleveEventIndexWithConfig(BleveEventIndexConfig{
		Index:        mockIndex,
		MaxCacheSize: maxSize,
		OnEvict:      onEvict,
	})

	const numGoroutines = 10
	const eventsPerGoroutine = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < eventsPerGoroutine; i++ {
				event := createTestEvent(
					fmt.Sprintf("g%d-event-%d", goroutineID, i),
					events.EventTypeAgentDecision,
					fmt.Sprintf("Content %d", i),
				)
				_ = bleveIndex.IndexEvent(context.Background(), event, []string{"content"})
			}
		}(g)
	}

	wg.Wait()

	// Total events: 10 * 20 = 200
	// Max cache: 50
	// Expected evictions: 200 - 50 = 150
	totalEvents := numGoroutines * eventsPerGoroutine
	expectedEvictions := totalEvents - maxSize

	if callbackCount.Load() != int64(expectedEvictions) {
		t.Errorf("Expected %d eviction callbacks, got %d", expectedEvictions, callbackCount.Load())
	}

	// Verify eviction count matches stats
	if bleveIndex.Evictions() != int64(expectedEvictions) {
		t.Errorf("Expected %d evictions in stats, got %d", expectedEvictions, bleveIndex.Evictions())
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
