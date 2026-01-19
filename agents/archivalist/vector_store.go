package archivalist

import (
	"context"
	"fmt"
	"sync"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// AE.4.5 VectorEventStore
// =============================================================================

// VectorEmbedder is the interface for generating text embeddings.
// This is a simplified interface specifically for VectorEventStore.
type VectorEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// VectorStorage is the interface for vector storage and retrieval operations.
type VectorStorage interface {
	Store(id string, vector []float32, metadata map[string]any) error
	Search(vector []float32, k int) ([]VectorSearchResult, error)
}

// VectorSearchResult represents a single result from vector similarity search.
type VectorSearchResult struct {
	ID       string         `json:"id"`
	Distance float32        `json:"distance"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// VectorEventStore provides vector storage and semantic search for ActivityEvents.
// It uses an embedder to generate embeddings and a storage backend for persistence.
type VectorEventStore struct {
	embedder VectorEmbedder
	storage  VectorStorage
	mu       sync.RWMutex

	// eventCache stores embedded events for retrieval after search.
	eventCache map[string]*events.ActivityEvent

	// embeddingCache stores embeddings for events.
	embeddingCache map[string][]float32
}

// NewVectorEventStore creates a new VectorEventStore with the provided embedder and storage.
func NewVectorEventStore(embedder VectorEmbedder, storage VectorStorage) *VectorEventStore {
	return &VectorEventStore{
		embedder:       embedder,
		storage:        storage,
		eventCache:     make(map[string]*events.ActivityEvent),
		embeddingCache: make(map[string][]float32),
	}
}

// EmbedEvent embeds an ActivityEvent and stores it in the vector store.
// The contentField parameter specifies which field to embed:
//   - "full": embeds the full content
//   - "summary": embeds the summary field
//   - "keywords": embeds the keywords joined as text
//   - Any other value defaults to "full"
func (v *VectorEventStore) EmbedEvent(ctx context.Context, event *events.ActivityEvent, contentField string) error {
	if event == nil {
		return fmt.Errorf("event cannot be nil")
	}

	if v.embedder == nil {
		return fmt.Errorf("embedder is not initialized")
	}

	if v.storage == nil {
		return fmt.Errorf("storage is not initialized")
	}

	// Check context cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Get text to embed based on content field
	text := v.getTextToEmbed(event, contentField)
	if text == "" {
		return fmt.Errorf("no content to embed for event %s", event.ID)
	}

	// Generate embedding
	embedding, err := v.embedder.Embed(ctx, text)
	if err != nil {
		return fmt.Errorf("failed to embed event %s: %w", event.ID, err)
	}

	// Build metadata for storage
	metadata := v.buildMetadata(event)

	// Store the embedding
	if err := v.storage.Store(event.ID, embedding, metadata); err != nil {
		return fmt.Errorf("failed to store embedding for event %s: %w", event.ID, err)
	}

	// Cache the event and embedding
	v.mu.Lock()
	v.eventCache[event.ID] = event
	v.embeddingCache[event.ID] = embedding
	v.mu.Unlock()

	return nil
}

// getTextToEmbed returns the text to embed based on the content field specification.
func (v *VectorEventStore) getTextToEmbed(event *events.ActivityEvent, contentField string) string {
	switch contentField {
	case "summary":
		if event.Summary != "" {
			return event.Summary
		}
		// Fall back to content if summary is empty
		return event.Content
	case "keywords":
		if len(event.Keywords) > 0 {
			return joinKeywords(event.Keywords)
		}
		// Fall back to content if no keywords
		return event.Content
	case "full", "":
		return event.Content
	default:
		// Default to full content
		return event.Content
	}
}

// joinKeywords joins keywords with spaces for embedding.
func joinKeywords(keywords []string) string {
	result := ""
	for i, kw := range keywords {
		if i > 0 {
			result += " "
		}
		result += kw
	}
	return result
}

// buildMetadata constructs metadata for storage from an event.
func (v *VectorEventStore) buildMetadata(event *events.ActivityEvent) map[string]any {
	return map[string]any{
		"event_type":  event.EventType.String(),
		"timestamp":   event.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		"session_id":  event.SessionID,
		"agent_id":    event.AgentID,
		"category":    event.Category,
		"outcome":     event.Outcome.String(),
		"importance":  event.Importance,
		"has_summary": event.Summary != "",
		"has_data":    event.Data != nil && len(event.Data) > 0,
	}
}

// Search performs semantic search for events similar to the query text.
// It embeds the query text and searches for similar vectors.
func (v *VectorEventStore) Search(ctx context.Context, queryText string, limit int) ([]*events.ActivityEvent, error) {
	if v.embedder == nil {
		return nil, fmt.Errorf("embedder is not initialized")
	}

	if v.storage == nil {
		return nil, fmt.Errorf("storage is not initialized")
	}

	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Apply default limit
	if limit <= 0 {
		limit = 10
	}

	// Embed the query text
	queryEmbedding, err := v.embedder.Embed(ctx, queryText)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// Search for similar vectors
	results, err := v.storage.Search(queryEmbedding, limit)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Convert results to ActivityEvents
	return v.resultsToEvents(results), nil
}

// resultsToEvents converts vector search results to ActivityEvents.
func (v *VectorEventStore) resultsToEvents(results []VectorSearchResult) []*events.ActivityEvent {
	if len(results) == 0 {
		return nil
	}

	eventList := make([]*events.ActivityEvent, 0, len(results))

	v.mu.RLock()
	defer v.mu.RUnlock()

	for _, result := range results {
		// Try to get from cache
		if event, ok := v.eventCache[result.ID]; ok {
			eventList = append(eventList, event)
		}
	}

	return eventList
}

// GetEmbedding retrieves the stored embedding for an event by ID.
// Returns nil and error if the embedding is not found.
func (v *VectorEventStore) GetEmbedding(eventID string) ([]float32, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	embedding, ok := v.embeddingCache[eventID]
	if !ok {
		return nil, fmt.Errorf("embedding not found for event %s", eventID)
	}

	return embedding, nil
}

// GetCachedEvent retrieves a cached event by ID.
// Returns nil if the event is not in the cache.
func (v *VectorEventStore) GetCachedEvent(id string) *events.ActivityEvent {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.eventCache[id]
}

// ClearCache clears both the event and embedding caches.
func (v *VectorEventStore) ClearCache() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.eventCache = make(map[string]*events.ActivityEvent)
	v.embeddingCache = make(map[string][]float32)
}

// CacheSize returns the number of cached events.
func (v *VectorEventStore) CacheSize() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.eventCache)
}

// EmbeddingCacheSize returns the number of cached embeddings.
func (v *VectorEventStore) EmbeddingCacheSize() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.embeddingCache)
}
