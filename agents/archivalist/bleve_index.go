package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/adalundhe/sylk/core/events"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

// =============================================================================
// AE.4.4 BleveEventIndex
// =============================================================================

// BleveIndex is the interface for Bleve index operations, enabling testability.
type BleveIndex interface {
	Index(id string, data interface{}) error
	Search(req *bleve.SearchRequest) (*bleve.SearchResult, error)
	Close() error
}

// BleveEventIndex provides full-text search indexing for ActivityEvents.
// It wraps a Bleve index to provide event-specific indexing and search operations.
type BleveEventIndex struct {
	index BleveIndex
	mu    sync.RWMutex

	// eventCache stores indexed events for retrieval after search.
	// Key is the event ID, value is the serialized event.
	eventCache map[string]*events.ActivityEvent
}

// bleveEventDocument represents the document structure indexed in Bleve.
type bleveEventDocument struct {
	ID         string   `json:"id"`
	EventType  string   `json:"event_type"`
	Timestamp  string   `json:"timestamp"`
	SessionID  string   `json:"session_id"`
	AgentID    string   `json:"agent_id"`
	Content    string   `json:"content"`
	Summary    string   `json:"summary"`
	Category   string   `json:"category"`
	FilePaths  []string `json:"file_paths"`
	Keywords   []string `json:"keywords"`
	Outcome    string   `json:"outcome"`
	Importance float64  `json:"importance"`
}

// NewBleveEventIndex creates a new BleveEventIndex with the provided Bleve index.
func NewBleveEventIndex(index BleveIndex) *BleveEventIndex {
	return &BleveEventIndex{
		index:      index,
		eventCache: make(map[string]*events.ActivityEvent),
	}
}

// IndexEvent indexes the specified fields from an ActivityEvent.
// The fields parameter specifies which event fields to include in the index.
// Common fields: "content", "summary", "keywords", "category", "agent_id", "data"
func (b *BleveEventIndex) IndexEvent(ctx context.Context, event *events.ActivityEvent, fields []string) error {
	if event == nil {
		return fmt.Errorf("event cannot be nil")
	}

	if b.index == nil {
		return fmt.Errorf("bleve index is not initialized")
	}

	// Check context cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Build document from specified fields
	doc := b.buildDocument(event, fields)

	// Index the document
	if err := b.index.Index(event.ID, doc); err != nil {
		return fmt.Errorf("failed to index event %s: %w", event.ID, err)
	}

	// Cache the event for retrieval
	b.mu.Lock()
	b.eventCache[event.ID] = event
	b.mu.Unlock()

	return nil
}

// buildDocument constructs a Bleve document from the event and specified fields.
func (b *BleveEventIndex) buildDocument(event *events.ActivityEvent, fields []string) map[string]interface{} {
	doc := make(map[string]interface{})

	// Always include ID and event type for filtering
	doc["id"] = event.ID
	doc["event_type"] = event.EventType.String()
	doc["timestamp"] = event.Timestamp.Format("2006-01-02T15:04:05Z07:00")
	doc["session_id"] = event.SessionID

	// Build field set for efficient lookup
	fieldSet := make(map[string]bool)
	for _, f := range fields {
		fieldSet[f] = true
	}

	// Add requested fields
	if fieldSet["content"] {
		doc["content"] = event.Content
	}
	if fieldSet["summary"] {
		doc["summary"] = event.Summary
	}
	if fieldSet["keywords"] {
		doc["keywords"] = strings.Join(event.Keywords, " ")
	}
	if fieldSet["category"] {
		doc["category"] = event.Category
	}
	if fieldSet["agent_id"] {
		doc["agent_id"] = event.AgentID
	}
	if fieldSet["data"] && event.Data != nil {
		// Serialize data as JSON string for indexing
		if dataBytes, err := json.Marshal(event.Data); err == nil {
			doc["data"] = string(dataBytes)
		}
	}
	if fieldSet["file_paths"] {
		doc["file_paths"] = strings.Join(event.FilePaths, " ")
	}
	if fieldSet["outcome"] {
		doc["outcome"] = event.Outcome.String()
	}
	if fieldSet["importance"] {
		doc["importance"] = event.Importance
	}

	return doc
}

// Search searches indexed events using a query string.
// The query supports Bleve query syntax including field-specific searches.
func (b *BleveEventIndex) Search(ctx context.Context, queryStr string, limit int) ([]*events.ActivityEvent, error) {
	return b.SearchWithFilters(ctx, queryStr, nil, limit)
}

// SearchWithFilters searches indexed events with additional filters.
// Filters are applied as exact match requirements on specific fields.
// Common filter keys: "event_type", "session_id", "agent_id", "category"
func (b *BleveEventIndex) SearchWithFilters(ctx context.Context, queryStr string, filters map[string]string, limit int) ([]*events.ActivityEvent, error) {
	if b.index == nil {
		return nil, fmt.Errorf("bleve index is not initialized")
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

	// Build the search query
	searchQuery := b.buildSearchQuery(queryStr, filters)

	// Create search request
	req := bleve.NewSearchRequest(searchQuery)
	req.Size = limit
	req.Fields = []string{"*"} // Return all stored fields

	// Execute search
	result, err := b.index.Search(req)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Convert results to ActivityEvents
	return b.resultsToEvents(result), nil
}

// buildSearchQuery constructs a Bleve query from the query string and filters.
func (b *BleveEventIndex) buildSearchQuery(queryStr string, filters map[string]string) query.Query {
	var queries []query.Query

	// Add main text query if provided
	if queryStr != "" {
		// Use match query for better text matching
		matchQuery := bleve.NewMatchQuery(queryStr)
		queries = append(queries, matchQuery)
	}

	// Add filter queries
	for field, value := range filters {
		termQuery := bleve.NewTermQuery(value)
		termQuery.SetField(field)
		queries = append(queries, termQuery)
	}

	// If no queries, match all
	if len(queries) == 0 {
		return bleve.NewMatchAllQuery()
	}

	// If single query, return it directly
	if len(queries) == 1 {
		return queries[0]
	}

	// Combine with boolean AND (must match all)
	boolQuery := bleve.NewBooleanQuery()
	for _, q := range queries {
		boolQuery.AddMust(q)
	}

	return boolQuery
}

// resultsToEvents converts Bleve search results to ActivityEvents.
func (b *BleveEventIndex) resultsToEvents(result *bleve.SearchResult) []*events.ActivityEvent {
	if result == nil || len(result.Hits) == 0 {
		return nil
	}

	eventList := make([]*events.ActivityEvent, 0, len(result.Hits))

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, hit := range result.Hits {
		// Try to get from cache first
		if event, ok := b.eventCache[hit.ID]; ok {
			eventList = append(eventList, event)
		}
	}

	return eventList
}

// Close closes the Bleve index.
func (b *BleveEventIndex) Close() error {
	if b.index == nil {
		return nil
	}
	return b.index.Close()
}

// GetCachedEvent retrieves a cached event by ID.
// Returns nil if the event is not in the cache.
func (b *BleveEventIndex) GetCachedEvent(id string) *events.ActivityEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.eventCache[id]
}

// ClearCache clears the event cache.
func (b *BleveEventIndex) ClearCache() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eventCache = make(map[string]*events.ActivityEvent)
}

// CacheSize returns the number of cached events.
func (b *BleveEventIndex) CacheSize() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.eventCache)
}
