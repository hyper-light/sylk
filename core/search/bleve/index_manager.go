// Package bleve provides Bleve index management for the Sylk Document Search System.
// It implements thread-safe document indexing and full-text search capabilities.
package bleve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	blevesearch "github.com/blevesearch/bleve/v2/search"
	"github.com/blevesearch/bleve/v2/search/highlight/highlighter/ansi"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Configuration
// =============================================================================

// IndexConfig holds index configuration.
type IndexConfig struct {
	Path          string
	BatchSize     int
	MaxConcurrent int
}

// DefaultIndexConfig returns sensible defaults for the given base path.
func DefaultIndexConfig(basePath string) IndexConfig {
	return IndexConfig{
		Path:          basePath + "/documents.bleve",
		BatchSize:     100,
		MaxConcurrent: 4,
	}
}

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrIndexClosed indicates an operation was attempted on a closed index.
	ErrIndexClosed = errors.New("index is closed")

	// ErrIndexAlreadyOpen indicates Open was called on an already open index.
	ErrIndexAlreadyOpen = errors.New("index is already open")

	// ErrNilDocument indicates a nil document was provided.
	ErrNilDocument = errors.New("document cannot be nil")

	// ErrEmptyDocumentID indicates a document with empty ID was provided.
	ErrEmptyDocumentID = errors.New("document ID cannot be empty")

	// ErrEmptyBatch indicates an empty batch was provided.
	ErrEmptyBatch = errors.New("batch cannot be empty")
)

// =============================================================================
// IndexManager
// =============================================================================

// IndexManager manages the Bleve index lifecycle and provides thread-safe
// document indexing and search operations.
type IndexManager struct {
	index  bleve.Index
	config IndexConfig
	mu     sync.RWMutex
	closed bool
}

// NewIndexManager creates a new IndexManager for the given path.
// The index is not opened until Open() is called.
func NewIndexManager(path string) *IndexManager {
	return &IndexManager{
		config: IndexConfig{
			Path:      path,
			BatchSize: 100,
		},
	}
}

// NewIndexManagerWithConfig creates an IndexManager with full configuration.
func NewIndexManagerWithConfig(config IndexConfig) *IndexManager {
	return &IndexManager{
		config: config,
	}
}

// =============================================================================
// Lifecycle Methods
// =============================================================================

// Open opens or creates the Bleve index at the configured path.
// If the index already exists, it opens it; otherwise, creates a new one.
// Returns ErrIndexAlreadyOpen if the index is already open.
func (m *IndexManager) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.index != nil {
		return ErrIndexAlreadyOpen
	}

	return m.openOrCreate()
}

// openOrCreate attempts to open existing index, falls back to creating new one.
// Must be called with write lock held.
func (m *IndexManager) openOrCreate() error {
	index, err := bleve.Open(m.config.Path)
	if err == nil {
		m.index = index
		m.closed = false
		return nil
	}

	return m.createNewIndex()
}

// createNewIndex creates a new Bleve index with the document mapping.
// Must be called with write lock held.
func (m *IndexManager) createNewIndex() error {
	indexMapping, err := BuildIndexMapping()
	if err != nil {
		return fmt.Errorf("build mapping: %w", err)
	}

	index, err := bleve.New(m.config.Path, indexMapping)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	m.index = index
	m.closed = false
	return nil
}

// Close flushes and closes the Bleve index.
// Returns nil if the index is already closed.
func (m *IndexManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return nil
	}

	m.closed = true
	err := m.index.Close()
	m.index = nil
	return err
}

// IsOpen returns true if the index is currently open.
func (m *IndexManager) IsOpen() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.index != nil && !m.closed
}

// Path returns the filesystem path of the index.
func (m *IndexManager) Path() string {
	return m.config.Path
}

// Config returns the index configuration.
func (m *IndexManager) Config() IndexConfig {
	return m.config
}

// =============================================================================
// Indexing Methods
// =============================================================================

// Index indexes a single document. Thread-safe with write lock.
// Returns ErrIndexClosed if the index is not open.
// Returns ErrNilDocument if doc is nil.
// Returns ErrEmptyDocumentID if doc.ID is empty.
func (m *IndexManager) Index(ctx context.Context, doc *search.Document) error {
	if err := m.validateDocument(doc); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return ErrIndexClosed
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return m.index.Index(doc.ID, doc)
}

// validateDocument checks that a document is valid for indexing.
func (m *IndexManager) validateDocument(doc *search.Document) error {
	if doc == nil {
		return ErrNilDocument
	}
	if doc.ID == "" {
		return ErrEmptyDocumentID
	}
	return nil
}

// IndexBatch indexes multiple documents in a single batch operation.
// This is more efficient than indexing documents individually.
// Commits batches at the configured BatchSize for memory efficiency.
// Returns ErrIndexClosed if the index is not open.
// Returns ErrEmptyBatch if docs is empty.
func (m *IndexManager) IndexBatch(ctx context.Context, docs []*search.Document) error {
	if len(docs) == 0 {
		return ErrEmptyBatch
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return ErrIndexClosed
	}

	return m.indexBatchLocked(ctx, docs)
}

// indexBatchLocked performs batch indexing with configurable batch size.
// Must be called with write lock held.
func (m *IndexManager) indexBatchLocked(ctx context.Context, docs []*search.Document) error {
	batch := m.index.NewBatch()
	batchSize := m.config.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	for _, doc := range docs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := m.addToBatch(batch, doc); err != nil {
			return err
		}

		// Commit batch at configured size
		if batch.Size() >= batchSize {
			if err := m.index.Batch(batch); err != nil {
				return fmt.Errorf("commit batch: %w", err)
			}
			batch = m.index.NewBatch()
		}
	}

	// Commit remaining documents
	if batch.Size() > 0 {
		if err := m.index.Batch(batch); err != nil {
			return fmt.Errorf("commit final batch: %w", err)
		}
	}

	return nil
}

// addToBatch validates and adds a document to the batch.
func (m *IndexManager) addToBatch(batch *bleve.Batch, doc *search.Document) error {
	if err := m.validateDocument(doc); err != nil {
		return err
	}
	return batch.Index(doc.ID, doc)
}

// Delete removes a document from the index by ID.
// Returns ErrIndexClosed if the index is not open.
// Returns ErrEmptyDocumentID if id is empty.
func (m *IndexManager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return ErrEmptyDocumentID
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return ErrIndexClosed
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return m.index.Delete(id)
}

// DeleteByPath removes a document from the index by path.
// Returns nil if the document is not found.
// Returns ErrIndexClosed if the index is not open.
func (m *IndexManager) DeleteByPath(ctx context.Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return ErrIndexClosed
	}

	// Find document ID by path
	query := bleve.NewTermQuery(path)
	query.SetField("path")

	searchReq := bleve.NewSearchRequest(query)
	searchReq.Size = 1
	searchReq.Fields = []string{"id"}

	result, err := m.index.SearchInContext(ctx, searchReq)
	if err != nil {
		return fmt.Errorf("search by path: %w", err)
	}

	if len(result.Hits) == 0 {
		return nil // Document not found, nothing to delete
	}

	return m.index.Delete(result.Hits[0].ID)
}

// Update updates a document by deleting and re-indexing it.
// Returns ErrIndexClosed if the index is not open.
// Returns ErrNilDocument if doc is nil.
// Returns ErrEmptyDocumentID if doc.ID is empty.
func (m *IndexManager) Update(ctx context.Context, doc *search.Document) error {
	if err := m.validateDocument(doc); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return ErrIndexClosed
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return m.updateLocked(doc)
}

// updateLocked performs delete + index. Must be called with write lock held.
func (m *IndexManager) updateLocked(doc *search.Document) error {
	// Delete first (ignore error if document doesn't exist)
	_ = m.index.Delete(doc.ID)

	return m.index.Index(doc.ID, doc)
}

// =============================================================================
// Query Methods
// =============================================================================

// DocumentCount returns the number of indexed documents.
// Returns ErrIndexClosed if the index is not open.
func (m *IndexManager) DocumentCount() (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed || m.index == nil {
		return 0, ErrIndexClosed
	}

	return m.index.DocCount()
}

// =============================================================================
// Search Methods
// =============================================================================

// Search executes a search query and returns matching documents.
// Supports query string search with optional highlighting.
// Returns ErrIndexClosed if the index is not open.
func (m *IndexManager) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	if err := req.ValidateAndNormalize(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed || m.index == nil {
		return nil, ErrIndexClosed
	}

	return m.executeSearch(ctx, req)
}

// executeSearch builds and executes the Bleve search request.
// Must be called with read lock held.
func (m *IndexManager) executeSearch(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	bleveReq := m.buildBleveRequest(req)

	startTime := time.Now()
	bleveResult, err := m.index.SearchInContext(ctx, bleveReq)
	if err != nil {
		return nil, err
	}

	return m.convertSearchResult(bleveResult, req.Query, startTime), nil
}

// buildBleveRequest constructs a Bleve SearchRequest from our SearchRequest.
func (m *IndexManager) buildBleveRequest(req *search.SearchRequest) *bleve.SearchRequest {
	query := bleve.NewQueryStringQuery(req.Query)
	bleveReq := bleve.NewSearchRequestOptions(query, req.Limit, req.Offset, false)
	bleveReq.Fields = []string{"*"}

	if req.IncludeHighlights {
		bleveReq.Highlight = bleve.NewHighlightWithStyle(ansi.Name)
	}

	return bleveReq
}

// convertSearchResult converts Bleve results to our SearchResult format.
func (m *IndexManager) convertSearchResult(
	bleveResult *bleve.SearchResult,
	query string,
	startTime time.Time,
) *search.SearchResult {
	docs := make([]search.ScoredDocument, 0, len(bleveResult.Hits))

	for _, hit := range bleveResult.Hits {
		scoredDoc := m.convertHit(hit)
		docs = append(docs, scoredDoc)
	}

	return &search.SearchResult{
		Documents:  docs,
		TotalHits:  int64(bleveResult.Total),
		SearchTime: time.Since(startTime),
		Query:      query,
	}
}

// convertHit converts a single Bleve hit to a ScoredDocument.
func (m *IndexManager) convertHit(hit *blevesearch.DocumentMatch) search.ScoredDocument {
	doc := m.extractDocument(hit.Fields)
	doc.ID = hit.ID

	scoredDoc := search.ScoredDocument{
		Document:   doc,
		Score:      hit.Score,
		Highlights: m.convertHighlights(hit.Fragments),
	}

	scoredDoc.MatchedFields = m.extractMatchedFields(hit.Fragments)
	return scoredDoc
}

// extractDocument extracts a Document from Bleve field map.
func (m *IndexManager) extractDocument(fields map[string]interface{}) search.Document {
	doc := search.Document{
		Path:     getStringField(fields, "path"),
		Type:     search.DocumentType(getStringField(fields, "type")),
		Language: getStringField(fields, "language"),
		Content:  getStringField(fields, "content"),
		Comments: getStringField(fields, "comments"),
		Checksum: getStringField(fields, "checksum"),
	}

	doc.Symbols = getStringSliceField(fields, "symbols")
	doc.Imports = getStringSliceField(fields, "imports")
	doc.ModifiedAt = getTimeField(fields, "modified_at")
	doc.IndexedAt = getTimeField(fields, "indexed_at")
	doc.GitCommit = getStringField(fields, "git_commit")

	return doc
}

// convertHighlights converts Bleve fragments to our highlight format.
func (m *IndexManager) convertHighlights(fragments map[string][]string) map[string][]string {
	if len(fragments) == 0 {
		return nil
	}
	return fragments
}

// extractMatchedFields extracts field names from Bleve fragments.
func (m *IndexManager) extractMatchedFields(fragments map[string][]string) []string {
	if len(fragments) == 0 {
		return nil
	}

	fields := make([]string, 0, len(fragments))
	for field := range fragments {
		fields = append(fields, field)
	}
	return fields
}

// =============================================================================
// Cleanup Methods
// =============================================================================

// Destroy closes the index and removes all index files from disk.
// This is an irreversible operation.
func (m *IndexManager) Destroy() error {
	if err := m.Close(); err != nil {
		return err
	}
	return os.RemoveAll(m.config.Path)
}

// =============================================================================
// Field Extraction Helpers
// =============================================================================

// getStringField extracts a string field from the fields map.
func getStringField(fields map[string]interface{}, key string) string {
	if v, ok := fields[key].(string); ok {
		return v
	}
	return ""
}

// getStringSliceField extracts a string slice field from the fields map.
func getStringSliceField(fields map[string]interface{}, key string) []string {
	switch v := fields[key].(type) {
	case []string:
		return v
	case []interface{}:
		return convertInterfaceSlice(v)
	default:
		return nil
	}
}

// convertInterfaceSlice converts []interface{} to []string.
func convertInterfaceSlice(slice []interface{}) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// getTimeField extracts a time.Time field from the fields map.
func getTimeField(fields map[string]interface{}, key string) time.Time {
	switch v := fields[key].(type) {
	case time.Time:
		return v
	case string:
		return parseTimeString(v)
	default:
		return time.Time{}
	}
}

// parseTimeString attempts to parse a time string in RFC3339 format.
func parseTimeString(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
