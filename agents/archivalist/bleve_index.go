package archivalist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/adalundhe/sylk/core/events"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
	lru "github.com/hashicorp/golang-lru/v2"
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

// DefaultEventCacheSize is the default maximum number of events to cache.
const DefaultEventCacheSize = 10000

// EvictCallback is called when an event is evicted from the cache.
// The callback receives the event ID and the event pointer.
type EvictCallback func(key string, event *events.ActivityEvent)

// BleveEventIndex provides full-text search indexing for ActivityEvents.
// It wraps a Bleve index to provide event-specific indexing and search operations.
// Uses an LRU cache with bounded size to prevent unbounded memory growth.
type BleveEventIndex struct {
	index BleveIndex
	mu    sync.RWMutex

	// eventCache stores indexed events for retrieval after search.
	// Uses LRU eviction to bound memory usage (W4H.3 fix).
	eventCache *lru.Cache[string, *events.ActivityEvent]

	// Cache statistics for observability (W4P.37 fix)
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64

	// onEvict is called when an event is evicted from the cache
	onEvict EvictCallback
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

// BleveEventIndexConfig holds configuration for BleveEventIndex.
type BleveEventIndexConfig struct {
	// Index is the underlying Bleve index.
	Index BleveIndex

	// MaxCacheSize is the maximum number of events to cache.
	// If <= 0, uses DefaultEventCacheSize.
	MaxCacheSize int

	// OnEvict is called when an event is evicted from the cache.
	// Optional - can be nil if no callback is needed.
	OnEvict EvictCallback
}

// NewBleveEventIndex creates a new BleveEventIndex with the provided Bleve index.
// maxCacheSize specifies the maximum number of events to cache; if <= 0, uses default.
func NewBleveEventIndex(index BleveIndex, maxCacheSize int) *BleveEventIndex {
	return NewBleveEventIndexWithConfig(BleveEventIndexConfig{
		Index:        index,
		MaxCacheSize: maxCacheSize,
	})
}

// NewBleveEventIndexWithConfig creates a new BleveEventIndex with the provided config.
// This constructor allows setting an eviction callback for observability.
func NewBleveEventIndexWithConfig(config BleveEventIndexConfig) *BleveEventIndex {
	maxCacheSize := config.MaxCacheSize
	if maxCacheSize <= 0 {
		maxCacheSize = DefaultEventCacheSize
	}

	b := &BleveEventIndex{
		index:   config.Index,
		onEvict: config.OnEvict,
	}

	// Create cache with eviction callback that tracks stats and invokes user callback
	cache, _ := lru.NewWithEvict[string, *events.ActivityEvent](maxCacheSize, b.handleEviction)
	b.eventCache = cache

	return b
}

// handleEviction is the internal eviction handler that updates stats and invokes user callback.
func (b *BleveEventIndex) handleEviction(key string, event *events.ActivityEvent) {
	b.evictions.Add(1)
	if b.onEvict != nil {
		b.onEvict(key, event)
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

	// Cache the event for retrieval (LRU eviction handled automatically)
	b.mu.Lock()
	b.eventCache.Add(event.ID, event)
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
		if event, ok := b.eventCache.Get(hit.ID); ok {
			b.hits.Add(1)
			eventList = append(eventList, event)
		} else {
			b.misses.Add(1)
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
// Updates cache hit/miss statistics.
func (b *BleveEventIndex) GetCachedEvent(id string) *events.ActivityEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()
	event, ok := b.eventCache.Get(id)
	if ok {
		b.hits.Add(1)
	} else {
		b.misses.Add(1)
	}
	return event
}

// ClearCache clears the event cache.
func (b *BleveEventIndex) ClearCache() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eventCache.Purge()
}

// CacheSize returns the number of cached events.
func (b *BleveEventIndex) CacheSize() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.eventCache.Len()
}

// =============================================================================
// Cache Statistics (W4P.37)
// =============================================================================

// CacheStats holds cache performance statistics.
type CacheStats struct {
	Hits      int64   `json:"hits"`
	Misses    int64   `json:"misses"`
	Evictions int64   `json:"evictions"`
	Size      int     `json:"size"`
	HitRate   float64 `json:"hit_rate"`
}

// Stats returns current cache statistics.
func (b *BleveEventIndex) Stats() CacheStats {
	b.mu.RLock()
	size := b.eventCache.Len()
	b.mu.RUnlock()

	hits := b.hits.Load()
	misses := b.misses.Load()
	evictions := b.evictions.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return CacheStats{
		Hits:      hits,
		Misses:    misses,
		Evictions: evictions,
		Size:      size,
		HitRate:   hitRate,
	}
}

// Hits returns the total number of cache hits.
func (b *BleveEventIndex) Hits() int64 {
	return b.hits.Load()
}

// Misses returns the total number of cache misses.
func (b *BleveEventIndex) Misses() int64 {
	return b.misses.Load()
}

// Evictions returns the total number of cache evictions.
func (b *BleveEventIndex) Evictions() int64 {
	return b.evictions.Load()
}

// ResetStats resets all cache statistics to zero.
func (b *BleveEventIndex) ResetStats() {
	b.hits.Store(0)
	b.misses.Store(0)
	b.evictions.Store(0)
}

// SetEvictionCallback sets or replaces the eviction callback.
// Pass nil to disable the callback.
func (b *BleveEventIndex) SetEvictionCallback(callback EvictCallback) {
	b.mu.Lock()
	b.onEvict = callback
	b.mu.Unlock()
}
