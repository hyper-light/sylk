package archivalist

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.5 VectorEventStore Tests
// =============================================================================

// MockVectorEmbedder implements VectorEmbedder for testing.
type MockVectorEmbedder struct {
	embeddings map[string][]float32
	embedErr   error
	dimension  int
}

func NewMockVectorEmbedder(dimension int) *MockVectorEmbedder {
	return &MockVectorEmbedder{
		embeddings: make(map[string][]float32),
		dimension:  dimension,
	}
}

func (m *MockVectorEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedErr != nil {
		return nil, m.embedErr
	}

	// Return cached embedding if exists
	if emb, ok := m.embeddings[text]; ok {
		return emb, nil
	}

	// Generate deterministic embedding based on text hash
	embedding := make([]float32, m.dimension)
	hash := uint32(0)
	for _, c := range text {
		hash = hash*31 + uint32(c)
	}

	for i := 0; i < m.dimension; i++ {
		embedding[i] = float32(math.Sin(float64(hash+uint32(i)))) * 0.5
	}

	// Normalize
	var norm float32
	for _, v := range embedding {
		norm += v * v
	}
	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for i := range embedding {
			embedding[i] /= norm
		}
	}

	m.embeddings[text] = embedding
	return embedding, nil
}

func (m *MockVectorEmbedder) SetEmbedding(text string, embedding []float32) {
	m.embeddings[text] = embedding
}

// MockVectorStorage implements VectorStorage for testing.
type MockVectorStorage struct {
	stored       map[string]storedVector
	storeErr     error
	searchErr    error
	searchResult []VectorSearchResult
}

type storedVector struct {
	vector   []float32
	metadata map[string]any
}

func NewMockVectorStorage() *MockVectorStorage {
	return &MockVectorStorage{
		stored: make(map[string]storedVector),
	}
}

func (m *MockVectorStorage) Store(id string, vector []float32, metadata map[string]any) error {
	if m.storeErr != nil {
		return m.storeErr
	}
	m.stored[id] = storedVector{vector: vector, metadata: metadata}
	return nil
}

func (m *MockVectorStorage) Search(vector []float32, k int) ([]VectorSearchResult, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	if m.searchResult != nil {
		return m.searchResult, nil
	}

	// Return results based on stored vectors (simple implementation)
	results := make([]VectorSearchResult, 0, len(m.stored))
	for id, sv := range m.stored {
		dist := cosineSimilarityF32(vector, sv.vector)
		results = append(results, VectorSearchResult{
			ID:       id,
			Distance: 1.0 - float32(dist), // Convert similarity to distance
			Metadata: sv.metadata,
		})
	}

	// Limit to k
	if len(results) > k {
		results = results[:k]
	}

	return results, nil
}

func (m *MockVectorStorage) SetSearchResult(results []VectorSearchResult) {
	m.searchResult = results
}

// Helper to compute cosine similarity for float32 slices
func cosineSimilarityF32(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// =============================================================================
// VectorEventStore Structure Tests
// =============================================================================

func TestNewVectorEventStore(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()

	store := NewVectorEventStore(embedder, storage)

	if store == nil {
		t.Fatal("Expected non-nil VectorEventStore")
	}

	if store.embedder != embedder {
		t.Error("Expected embedder to be set")
	}

	if store.storage != storage {
		t.Error("Expected storage to be set")
	}

	if store.eventCache == nil {
		t.Error("Expected eventCache to be initialized")
	}

	if store.embeddingCache == nil {
		t.Error("Expected embeddingCache to be initialized")
	}
}

func TestNewVectorEventStore_NilDependencies(t *testing.T) {
	// Nil embedder
	store := NewVectorEventStore(nil, NewMockVectorStorage())
	if store == nil {
		t.Fatal("Expected non-nil VectorEventStore")
	}

	// Nil storage
	store = NewVectorEventStore(NewMockVectorEmbedder(128), nil)
	if store == nil {
		t.Fatal("Expected non-nil VectorEventStore")
	}

	// Both nil
	store = NewVectorEventStore(nil, nil)
	if store == nil {
		t.Fatal("Expected non-nil VectorEventStore")
	}
}

// =============================================================================
// EmbedEvent Tests
// =============================================================================

func TestVectorEventStore_EmbedEvent_Success(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "Test content for embedding")

	err := store.EmbedEvent(context.Background(), event, "full")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify stored in storage
	if _, ok := storage.stored[event.ID]; !ok {
		t.Error("Expected event to be stored in vector storage")
	}

	// Verify cached
	if store.CacheSize() != 1 {
		t.Errorf("Expected cache size 1, got %d", store.CacheSize())
	}

	if store.EmbeddingCacheSize() != 1 {
		t.Errorf("Expected embedding cache size 1, got %d", store.EmbeddingCacheSize())
	}
}

func TestVectorEventStore_EmbedEvent_NilEvent(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	err := store.EmbedEvent(context.Background(), nil, "full")
	if err == nil {
		t.Fatal("Expected error for nil event")
	}

	if err.Error() != "event cannot be nil" {
		t.Errorf("Expected 'event cannot be nil' error, got: %v", err)
	}
}

func TestVectorEventStore_EmbedEvent_NilEmbedder(t *testing.T) {
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(nil, storage)

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "Test content")

	err := store.EmbedEvent(context.Background(), event, "full")
	if err == nil {
		t.Fatal("Expected error for nil embedder")
	}

	if err.Error() != "embedder is not initialized" {
		t.Errorf("Expected 'embedder is not initialized' error, got: %v", err)
	}
}

func TestVectorEventStore_EmbedEvent_NilStorage(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	store := NewVectorEventStore(embedder, nil)

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "Test content")

	err := store.EmbedEvent(context.Background(), event, "full")
	if err == nil {
		t.Fatal("Expected error for nil storage")
	}

	if err.Error() != "storage is not initialized" {
		t.Errorf("Expected 'storage is not initialized' error, got: %v", err)
	}
}

func TestVectorEventStore_EmbedEvent_EmbedError(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	embedder.embedErr = errors.New("embedding failed")
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "Test content")

	err := store.EmbedEvent(context.Background(), event, "full")
	if err == nil {
		t.Fatal("Expected error when embedding fails")
	}
}

func TestVectorEventStore_EmbedEvent_StoreError(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	storage.storeErr = errors.New("store failed")
	store := NewVectorEventStore(embedder, storage)

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "Test content")

	err := store.EmbedEvent(context.Background(), event, "full")
	if err == nil {
		t.Fatal("Expected error when storage fails")
	}
}

func TestVectorEventStore_EmbedEvent_ContextCanceled(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "Test content")

	err := store.EmbedEvent(ctx, event, "full")
	if err == nil {
		t.Fatal("Expected error for canceled context")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled error, got: %v", err)
	}
}

func TestVectorEventStore_EmbedEvent_EmptyContent(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "")

	err := store.EmbedEvent(context.Background(), event, "full")
	if err == nil {
		t.Fatal("Expected error for empty content")
	}
}

func TestVectorEventStore_EmbedEvent_ContentFields(t *testing.T) {
	tests := []struct {
		name         string
		contentField string
		content      string
		summary      string
		keywords     []string
		expectText   string
	}{
		{
			name:         "full content",
			contentField: "full",
			content:      "Full content text",
			summary:      "Summary text",
			keywords:     []string{"keyword1", "keyword2"},
			expectText:   "Full content text",
		},
		{
			name:         "summary",
			contentField: "summary",
			content:      "Full content text",
			summary:      "Summary text",
			keywords:     []string{"keyword1", "keyword2"},
			expectText:   "Summary text",
		},
		{
			name:         "keywords",
			contentField: "keywords",
			content:      "Full content text",
			summary:      "Summary text",
			keywords:     []string{"keyword1", "keyword2"},
			expectText:   "keyword1 keyword2",
		},
		{
			name:         "summary fallback to content",
			contentField: "summary",
			content:      "Full content text",
			summary:      "",
			keywords:     []string{"keyword1"},
			expectText:   "Full content text",
		},
		{
			name:         "keywords fallback to content",
			contentField: "keywords",
			content:      "Full content text",
			summary:      "Summary text",
			keywords:     nil,
			expectText:   "Full content text",
		},
		{
			name:         "empty field defaults to full",
			contentField: "",
			content:      "Full content text",
			summary:      "Summary text",
			keywords:     []string{"keyword1"},
			expectText:   "Full content text",
		},
		{
			name:         "unknown field defaults to full",
			contentField: "unknown_field",
			content:      "Full content text",
			summary:      "Summary text",
			keywords:     []string{"keyword1"},
			expectText:   "Full content text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedder := NewMockVectorEmbedder(128)
			storage := NewMockVectorStorage()
			store := NewVectorEventStore(embedder, storage)

			event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, tt.content)
			event.Summary = tt.summary
			event.Keywords = tt.keywords

			err := store.EmbedEvent(context.Background(), event, tt.contentField)
			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}

			// Verify the correct text was embedded
			if _, ok := embedder.embeddings[tt.expectText]; !ok {
				t.Errorf("Expected text '%s' to be embedded", tt.expectText)
			}
		})
	}
}

func TestVectorEventStore_EmbedEvent_Metadata(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "Test content")
	event.Summary = "Test summary"
	event.AgentID = "agent-1"
	event.Category = "testing"
	event.Data = map[string]any{"key": "value"}

	err := store.EmbedEvent(context.Background(), event, "full")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify metadata was stored
	stored, ok := storage.stored[event.ID]
	if !ok {
		t.Fatal("Expected event to be stored")
	}

	metadata := stored.metadata
	if metadata["event_type"] != "agent_decision" {
		t.Errorf("Expected event_type 'agent_decision', got '%v'", metadata["event_type"])
	}

	if metadata["session_id"] != "test-session" {
		t.Errorf("Expected session_id 'test-session', got '%v'", metadata["session_id"])
	}

	if metadata["agent_id"] != "agent-1" {
		t.Errorf("Expected agent_id 'agent-1', got '%v'", metadata["agent_id"])
	}

	if metadata["category"] != "testing" {
		t.Errorf("Expected category 'testing', got '%v'", metadata["category"])
	}

	if metadata["has_summary"] != true {
		t.Errorf("Expected has_summary true, got '%v'", metadata["has_summary"])
	}

	if metadata["has_data"] != true {
		t.Errorf("Expected has_data true, got '%v'", metadata["has_data"])
	}
}

// =============================================================================
// Search Tests
// =============================================================================

func TestVectorEventStore_Search_Success(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	// Embed some events
	event1 := createTestActivityEvent("event-1", events.EventTypeAgentDecision, "Decision about architecture")
	event2 := createTestActivityEvent("event-2", events.EventTypeAgentDecision, "Decision about testing")

	store.EmbedEvent(context.Background(), event1, "full")
	store.EmbedEvent(context.Background(), event2, "full")

	// Search
	results, err := store.Search(context.Background(), "decision", 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}
}

func TestVectorEventStore_Search_NilEmbedder(t *testing.T) {
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(nil, storage)

	_, err := store.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("Expected error for nil embedder")
	}
}

func TestVectorEventStore_Search_NilStorage(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	store := NewVectorEventStore(embedder, nil)

	_, err := store.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("Expected error for nil storage")
	}
}

func TestVectorEventStore_Search_EmbedError(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	embedder.embedErr = errors.New("embedding failed")
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	_, err := store.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("Expected error when embedding fails")
	}
}

func TestVectorEventStore_Search_SearchError(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	storage.searchErr = errors.New("search failed")
	store := NewVectorEventStore(embedder, storage)

	_, err := store.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("Expected error when search fails")
	}
}

func TestVectorEventStore_Search_ContextCanceled(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Search(ctx, "test", 10)
	if err == nil {
		t.Fatal("Expected error for canceled context")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled error, got: %v", err)
	}
}

func TestVectorEventStore_Search_DefaultLimit(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	// Search with limit 0 should use default
	_, err := store.Search(context.Background(), "test", 0)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Search with negative limit should use default
	_, err = store.Search(context.Background(), "test", -1)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestVectorEventStore_Search_EmptyResults(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	storage.SetSearchResult(nil) // Empty results
	store := NewVectorEventStore(embedder, storage)

	results, err := store.Search(context.Background(), "nonexistent", 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}
}

func TestVectorEventStore_Search_ReturnsOnlyCachedEvents(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	// Embed one event
	event1 := createTestActivityEvent("event-1", events.EventTypeAgentDecision, "Test content")
	store.EmbedEvent(context.Background(), event1, "full")

	// Set search result to include a non-cached event
	storage.SetSearchResult([]VectorSearchResult{
		{ID: "event-1", Distance: 0.1},
		{ID: "event-2", Distance: 0.2}, // Not cached
	})

	results, err := store.Search(context.Background(), "test", 10)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should only return cached event
	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	if results[0].ID != "event-1" {
		t.Errorf("Expected event-1, got %s", results[0].ID)
	}
}

// =============================================================================
// GetEmbedding Tests
// =============================================================================

func TestVectorEventStore_GetEmbedding_Success(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	event := createTestActivityEvent("test-1", events.EventTypeAgentDecision, "Test content")
	store.EmbedEvent(context.Background(), event, "full")

	embedding, err := store.GetEmbedding(event.ID)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if embedding == nil {
		t.Fatal("Expected non-nil embedding")
	}

	if len(embedding) != 128 {
		t.Errorf("Expected embedding dimension 128, got %d", len(embedding))
	}
}

func TestVectorEventStore_GetEmbedding_NotFound(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	_, err := store.GetEmbedding("non-existent")
	if err == nil {
		t.Fatal("Expected error for non-existent embedding")
	}
}

// =============================================================================
// Cache Tests
// =============================================================================

func TestVectorEventStore_Cache_Operations(t *testing.T) {
	embedder := NewMockVectorEmbedder(128)
	storage := NewMockVectorStorage()
	store := NewVectorEventStore(embedder, storage)

	// Initially empty
	if store.CacheSize() != 0 {
		t.Errorf("Expected cache size 0, got %d", store.CacheSize())
	}
	if store.EmbeddingCacheSize() != 0 {
		t.Errorf("Expected embedding cache size 0, got %d", store.EmbeddingCacheSize())
	}

	// Embed adds to caches
	event1 := createTestActivityEvent("event-1", events.EventTypeAgentDecision, "Test content")
	store.EmbedEvent(context.Background(), event1, "full")

	if store.CacheSize() != 1 {
		t.Errorf("Expected cache size 1, got %d", store.CacheSize())
	}
	if store.EmbeddingCacheSize() != 1 {
		t.Errorf("Expected embedding cache size 1, got %d", store.EmbeddingCacheSize())
	}

	// Get cached event
	cached := store.GetCachedEvent("event-1")
	if cached == nil {
		t.Error("Expected to find cached event")
	}
	if cached.ID != event1.ID {
		t.Error("Expected cached event to match original")
	}

	// Get non-existent event
	nonExistent := store.GetCachedEvent("non-existent")
	if nonExistent != nil {
		t.Error("Expected nil for non-existent event")
	}

	// Clear cache
	store.ClearCache()
	if store.CacheSize() != 0 {
		t.Errorf("Expected cache size 0 after clear, got %d", store.CacheSize())
	}
	if store.EmbeddingCacheSize() != 0 {
		t.Errorf("Expected embedding cache size 0 after clear, got %d", store.EmbeddingCacheSize())
	}
}

// =============================================================================
// VectorSearchResult Tests
// =============================================================================

func TestVectorSearchResult_Structure(t *testing.T) {
	result := VectorSearchResult{
		ID:       "test-id",
		Distance: 0.5,
		Metadata: map[string]any{
			"key": "value",
		},
	}

	if result.ID != "test-id" {
		t.Errorf("Expected ID 'test-id', got '%s'", result.ID)
	}

	if result.Distance != 0.5 {
		t.Errorf("Expected Distance 0.5, got %f", result.Distance)
	}

	if result.Metadata["key"] != "value" {
		t.Errorf("Expected Metadata key 'value', got '%v'", result.Metadata["key"])
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

func createTestActivityEvent(id string, eventType events.EventType, content string) *events.ActivityEvent {
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
