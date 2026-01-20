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
	BatchTimeout  time.Duration // Timeout for batch commit operations. Default: 30s

	// Async indexing configuration
	AsyncEnabled       bool
	AsyncQueueSize     int
	AsyncBatchSize     int
	AsyncFlushInterval time.Duration
}

// DefaultBatchTimeout is the default timeout for batch commit operations.
const DefaultBatchTimeout = 30 * time.Second

// =============================================================================
// Batch Size Validation (PF.3.10)
// =============================================================================

const (
	// MinBatchSize is the minimum allowed batch size to prevent excessive commit overhead.
	MinBatchSize = 10

	// MaxBatchSize is the maximum allowed batch size to prevent OOM from large batches.
	MaxBatchSize = 10000

	// DefaultBatchSize is the default batch size when not specified.
	DefaultBatchSize = 100
)

// ValidateBatchSize ensures batch size is within acceptable range.
// Returns MinBatchSize if size is too small, MaxBatchSize if too large,
// or the original size if within bounds.
func ValidateBatchSize(size int) int {
	if size < MinBatchSize {
		return MinBatchSize
	}
	if size > MaxBatchSize {
		return MaxBatchSize
	}
	return size
}

// =============================================================================
// Field Configuration for Selective Loading
// =============================================================================

// FieldConfig specifies which fields to load during search operations.
// This enables selective field loading to reduce memory usage and improve
// performance when full document content is not needed.
type FieldConfig struct {
	// LoadContent indicates whether to load the full content field (expensive).
	// When false, content must be lazy-loaded via GetDocumentContent if needed.
	LoadContent bool

	// Fields specifies the list of fields to load.
	// If nil and LoadContent is true, all fields are loaded ("*").
	// If empty slice, only document ID and score are returned.
	Fields []string
}

// DefaultMetadataFields are the fields loaded by default (excluding content).
var DefaultMetadataFields = []string{
	"path",
	"type",
	"title",
	"modified_at",
	"indexed_at",
	"language",
	"symbols",
	"imports",
	"checksum",
	"git_commit",
	"comments",
}

// DefaultFieldConfig returns a FieldConfig for metadata-only loading.
// This is the recommended default for most search operations as it
// avoids loading potentially large Content fields.
func DefaultFieldConfig() FieldConfig {
	return FieldConfig{
		LoadContent: false,
		Fields:      DefaultMetadataFields,
	}
}

// FullFieldConfig returns a FieldConfig for loading all fields including content.
// Use this when you need the full document content in search results.
func FullFieldConfig() FieldConfig {
	return FieldConfig{
		LoadContent: true,
		Fields:      nil, // nil signals all fields
	}
}

// MetadataOnlyFieldConfig returns a FieldConfig that loads only essential metadata.
// This is the most performant option when you only need paths and types.
func MetadataOnlyFieldConfig() FieldConfig {
	return FieldConfig{
		LoadContent: false,
		Fields:      []string{"path", "type", "language", "modified_at"},
	}
}

// CustomFieldConfig creates a FieldConfig with a custom set of fields.
// Set loadContent to true to also include the content field.
func CustomFieldConfig(fields []string, loadContent bool) FieldConfig {
	return FieldConfig{
		LoadContent: loadContent,
		Fields:      fields,
	}
}

// DefaultIndexConfig returns sensible defaults for the given base path.
func DefaultIndexConfig(basePath string) IndexConfig {
	return IndexConfig{
		Path:          basePath + "/documents.bleve",
		BatchSize:     100,
		MaxConcurrent: 4,
		BatchTimeout:  DefaultBatchTimeout,
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

	// ErrBatchTimeout indicates a batch commit operation timed out.
	ErrBatchTimeout = errors.New("batch commit timeout")
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

	// Async indexing support (PF.3.2)
	asyncQueue *AsyncIndexQueue
	useAsync   bool

	// Path index for O(1) DeleteByPath lookup (PF.3.3)
	pathIndex map[string]string // path -> docID
	pathMu    sync.RWMutex
}

// NewIndexManager creates a new IndexManager for the given path.
// The index is not opened until Open() is called.
func NewIndexManager(path string) *IndexManager {
	return &IndexManager{
		config: IndexConfig{
			Path:         path,
			BatchSize:    100,
			BatchTimeout: DefaultBatchTimeout,
		},
		pathIndex: make(map[string]string),
	}
}

// NewIndexManagerWithConfig creates an IndexManager with full configuration.
func NewIndexManagerWithConfig(config IndexConfig) *IndexManager {
	return &IndexManager{
		config:    config,
		pathIndex: make(map[string]string),
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

	if err := m.openOrCreate(); err != nil {
		return err
	}

	// Initialize async queue if enabled (PF.3.2)
	if m.config.AsyncEnabled {
		m.initAsyncQueue()
	}

	return nil
}

// initAsyncQueue initializes the async indexing queue.
// Must be called with write lock held.
func (m *IndexManager) initAsyncQueue() {
	asyncCfg := AsyncIndexQueueConfig{
		MaxQueueSize:  m.config.AsyncQueueSize,
		BatchSize:     m.config.AsyncBatchSize,
		FlushInterval: m.config.AsyncFlushInterval,
	}

	// Apply defaults if not specified
	if asyncCfg.MaxQueueSize <= 0 {
		asyncCfg.MaxQueueSize = 10000
	}
	if asyncCfg.BatchSize <= 0 {
		asyncCfg.BatchSize = 100
	}
	if asyncCfg.FlushInterval <= 0 {
		asyncCfg.FlushInterval = 100 * time.Millisecond
	}

	m.asyncQueue = NewAsyncIndexQueue(
		context.Background(),
		asyncCfg,
		m.indexFunc,
		m.deleteFunc,
		m.batchFunc,
	)
	m.useAsync = true
}

// indexFunc is a callback for async queue single document indexing.
func (m *IndexManager) indexFunc(docID string, doc interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return ErrIndexClosed
	}

	return m.index.Index(docID, doc)
}

// deleteFunc is a callback for async queue single document deletion.
func (m *IndexManager) deleteFunc(docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return ErrIndexClosed
	}

	return m.index.Delete(docID)
}

// batchFunc is a callback for async queue batch operations.
func (m *IndexManager) batchFunc(ops []*IndexOperation) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return ErrIndexClosed
	}

	batch := m.index.NewBatch()
	for _, op := range ops {
		switch op.Type {
		case OpIndex:
			if err := batch.Index(op.DocID, op.Document); err != nil {
				return err
			}
		case OpDelete:
			batch.Delete(op.DocID)
		}
	}

	return m.index.Batch(batch)
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
	// Close async queue first if active (PF.3.2)
	if m.asyncQueue != nil {
		if err := m.asyncQueue.Close(); err != nil {
			return fmt.Errorf("close async queue: %w", err)
		}
		m.asyncQueue = nil
		m.useAsync = false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed || m.index == nil {
		return nil
	}

	m.closed = true
	err := m.index.Close()
	m.index = nil

	// Clear path index (PF.3.3)
	m.pathMu.Lock()
	m.pathIndex = make(map[string]string)
	m.pathMu.Unlock()

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
// Path Index Methods (PF.3.3)
// =============================================================================

// UpdatePathIndex adds or updates a path to docID mapping.
func (m *IndexManager) UpdatePathIndex(path, docID string) {
	m.pathMu.Lock()
	m.pathIndex[path] = docID
	m.pathMu.Unlock()
}

// RemovePathIndex removes a path from the index.
func (m *IndexManager) RemovePathIndex(path string) {
	m.pathMu.Lock()
	delete(m.pathIndex, path)
	m.pathMu.Unlock()
}

// GetDocIDByPath returns the docID for a given path.
// Returns empty string and false if not found.
func (m *IndexManager) GetDocIDByPath(path string) (string, bool) {
	m.pathMu.RLock()
	docID, exists := m.pathIndex[path]
	m.pathMu.RUnlock()
	return docID, exists
}

// PathIndexSize returns the number of entries in the path index.
func (m *IndexManager) PathIndexSize() int {
	m.pathMu.RLock()
	defer m.pathMu.RUnlock()
	return len(m.pathIndex)
}

// =============================================================================
// Async Indexing Methods (PF.3.2)
// =============================================================================

// IndexAsync adds document to index asynchronously.
// Returns a channel that will receive the result error (or nil on success).
// Falls back to sync if async queue is not enabled.
func (m *IndexManager) IndexAsync(ctx context.Context, doc *search.Document) <-chan error {
	if err := m.validateDocument(doc); err != nil {
		ch := make(chan error, 1)
		ch <- err
		close(ch)
		return ch
	}

	if m.asyncQueue == nil {
		// Fallback to sync
		ch := make(chan error, 1)
		ch <- m.Index(ctx, doc)
		close(ch)
		return ch
	}

	// Update path index for async operations
	if doc.Path != "" {
		m.UpdatePathIndex(doc.Path, doc.ID)
	}

	return m.asyncQueue.SubmitIndex(doc.ID, doc)
}

// DeleteAsync removes document asynchronously.
// Returns a channel that will receive the result error (or nil on success).
// Falls back to sync if async queue is not enabled.
func (m *IndexManager) DeleteAsync(ctx context.Context, docID string) <-chan error {
	if docID == "" {
		ch := make(chan error, 1)
		ch <- ErrEmptyDocumentID
		close(ch)
		return ch
	}

	if m.asyncQueue == nil {
		// Fallback to sync
		ch := make(chan error, 1)
		ch <- m.Delete(ctx, docID)
		close(ch)
		return ch
	}

	return m.asyncQueue.SubmitDelete(docID)
}

// FlushAsync forces immediate processing of all pending async operations.
// Blocks until all currently queued operations are processed.
func (m *IndexManager) FlushAsync(ctx context.Context) error {
	if m.asyncQueue == nil {
		return nil
	}
	return m.asyncQueue.Flush(ctx)
}

// AsyncStats returns statistics about the async queue.
// Returns zero values if async is not enabled.
func (m *IndexManager) AsyncStats() AsyncIndexQueueStats {
	if m.asyncQueue == nil {
		return AsyncIndexQueueStats{}
	}
	return m.asyncQueue.Stats()
}

// IsAsyncEnabled returns true if async indexing is enabled.
func (m *IndexManager) IsAsyncEnabled() bool {
	return m.useAsync && m.asyncQueue != nil
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

	if err := m.index.Index(doc.ID, doc); err != nil {
		// W4L.16: Add document context to error for debugging
		return fmt.Errorf("index document %q: %w", doc.ID, err)
	}

	// Update path index (PF.3.3)
	if doc.Path != "" {
		m.pathMu.Lock()
		m.pathIndex[doc.Path] = doc.ID
		m.pathMu.Unlock()
	}

	return nil
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
// If the number of documents exceeds the effective batch size, documents
// are automatically chunked to prevent OOM issues (PF.3.10).
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

	// Validate and apply batch size bounds (PF.3.10)
	effectiveBatchSize := ValidateBatchSize(m.config.BatchSize)

	// If docs exceed effective batch size, chunk them to prevent OOM
	if len(docs) > effectiveBatchSize {
		return m.indexBatchChunked(ctx, docs, effectiveBatchSize)
	}

	return m.indexBatchLocked(ctx, docs)
}

// indexBatchChunked processes large batches in smaller chunks to prevent OOM.
// Must be called with write lock held.
func (m *IndexManager) indexBatchChunked(ctx context.Context, docs []*search.Document, chunkSize int) error {
	for i := 0; i < len(docs); i += chunkSize {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		end := i + chunkSize
		if end > len(docs) {
			end = len(docs)
		}

		if err := m.indexBatchLocked(ctx, docs[i:end]); err != nil {
			return fmt.Errorf("chunk %d: %w", i/chunkSize, err)
		}
	}
	return nil
}

// indexBatchLocked performs batch indexing with configurable batch size.
// Uses timeout-protected batch commits to prevent hanging.
// Must be called with write lock held.
func (m *IndexManager) indexBatchLocked(ctx context.Context, docs []*search.Document) error {
	batch := m.index.NewBatch()
	batchSize := m.config.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	// Collect paths for batch path index update (PF.3.3)
	pathsToUpdate := make(map[string]string)

	for _, doc := range docs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := m.addToBatch(batch, doc); err != nil {
			return err
		}

		// Collect path mappings
		if doc.Path != "" {
			pathsToUpdate[doc.Path] = doc.ID
		}

		// Commit batch at configured size with timeout protection
		if batch.Size() >= batchSize {
			if err := m.BatchWithTimeout(ctx, batch); err != nil {
				return fmt.Errorf("commit batch: %w", err)
			}
			batch = m.index.NewBatch()
		}
	}

	// Commit remaining documents with timeout protection
	if batch.Size() > 0 {
		if err := m.BatchWithTimeout(ctx, batch); err != nil {
			return fmt.Errorf("commit final batch: %w", err)
		}
	}

	// Update path index (PF.3.3)
	if len(pathsToUpdate) > 0 {
		m.pathMu.Lock()
		for path, docID := range pathsToUpdate {
			m.pathIndex[path] = docID
		}
		m.pathMu.Unlock()
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

// BatchWithTimeout wraps batch commit with context timeout protection.
// If the context doesn't already have a deadline, it applies the configured BatchTimeout.
// This prevents batch commits from hanging indefinitely if Bleve experiences issues.
// Must be called with appropriate lock held (caller's responsibility).
func (m *IndexManager) BatchWithTimeout(ctx context.Context, batch *bleve.Batch) error {
	// Determine the timeout to use
	timeout := m.config.BatchTimeout
	if timeout <= 0 {
		timeout = DefaultBatchTimeout
	}

	// Create timeout context if not already present
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Capture a local reference to the index to avoid race with Close().
	// This is safe because the caller holds the lock, and we're only reading
	// the pointer. The goroutine uses this captured reference.
	idx := m.index
	if idx == nil {
		return ErrIndexClosed
	}

	// Use channel to capture result from goroutine
	done := make(chan error, 1)
	go func() {
		done <- idx.Batch(batch)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrBatchTimeout, ctx.Err())
	}
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
// Uses O(1) path index lookup when available, falls back to search.
// Returns nil if the document is not found.
// Returns ErrIndexClosed if the index is not open.
func (m *IndexManager) DeleteByPath(ctx context.Context, path string) error {
	// First, try O(1) lookup from path index (PF.3.3)
	m.pathMu.RLock()
	docID, exists := m.pathIndex[path]
	m.pathMu.RUnlock()

	if exists {
		// Remove from path index
		m.pathMu.Lock()
		delete(m.pathIndex, path)
		m.pathMu.Unlock()

		return m.Delete(ctx, docID)
	}

	// Fall back to search if not in cache
	return m.deleteByPathSlow(ctx, path)
}

// deleteByPathSlow performs DeleteByPath using a search query.
// Used as fallback when path is not in the path index.
func (m *IndexManager) deleteByPathSlow(ctx context.Context, path string) error {
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

	if err := m.updateLocked(doc); err != nil {
		return err
	}

	// Update path index (PF.3.3)
	if doc.Path != "" {
		m.pathMu.Lock()
		m.pathIndex[doc.Path] = doc.ID
		m.pathMu.Unlock()
	}

	return nil
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
// Uses DefaultFieldConfig which loads metadata but not content for efficiency.
// Supports query string search with optional highlighting.
// Returns ErrIndexClosed if the index is not open.
func (m *IndexManager) Search(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	return m.SearchWithFields(ctx, req, DefaultFieldConfig())
}

// SearchWithFields executes a search query with configurable field loading.
// Use this method when you need control over which fields are loaded.
// Pass FullFieldConfig() to load all fields including content.
// Pass DefaultFieldConfig() to load only metadata (more efficient).
// Returns ErrIndexClosed if the index is not open.
func (m *IndexManager) SearchWithFields(ctx context.Context, req *search.SearchRequest, fieldCfg FieldConfig) (*search.SearchResult, error) {
	if err := req.ValidateAndNormalize(); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed || m.index == nil {
		return nil, ErrIndexClosed
	}

	return m.executeSearchWithFields(ctx, req, fieldCfg)
}

// executeSearchWithFields builds and executes the Bleve search request with field config.
// Must be called with read lock held.
func (m *IndexManager) executeSearchWithFields(ctx context.Context, req *search.SearchRequest, fieldCfg FieldConfig) (*search.SearchResult, error) {
	bleveReq := m.buildBleveRequestWithFields(req, fieldCfg)

	startTime := time.Now()
	bleveResult, err := m.index.SearchInContext(ctx, bleveReq)
	if err != nil {
		return nil, err
	}

	return m.convertSearchResult(bleveResult, req.Query, startTime), nil
}

// executeSearch builds and executes the Bleve search request.
// Must be called with read lock held.
// Deprecated: Use executeSearchWithFields for better control over field loading.
func (m *IndexManager) executeSearch(ctx context.Context, req *search.SearchRequest) (*search.SearchResult, error) {
	return m.executeSearchWithFields(ctx, req, DefaultFieldConfig())
}

// buildBleveRequestWithFields constructs a Bleve SearchRequest with configurable fields.
func (m *IndexManager) buildBleveRequestWithFields(req *search.SearchRequest, fieldCfg FieldConfig) *bleve.SearchRequest {
	query := bleve.NewQueryStringQuery(req.Query)
	bleveReq := bleve.NewSearchRequestOptions(query, req.Limit, req.Offset, false)

	// Configure field loading based on FieldConfig
	if fieldCfg.Fields == nil || fieldCfg.LoadContent {
		// Load all fields when Fields is nil and LoadContent is true
		bleveReq.Fields = []string{"*"}
	} else if len(fieldCfg.Fields) == 0 {
		// Empty slice means no stored fields, only ID and score
		bleveReq.Fields = []string{}
	} else {
		// Load only specified fields
		if fieldCfg.LoadContent {
			// Add content to specified fields if requested
			fields := make([]string, len(fieldCfg.Fields)+1)
			copy(fields, fieldCfg.Fields)
			fields[len(fields)-1] = "content"
			bleveReq.Fields = fields
		} else {
			bleveReq.Fields = fieldCfg.Fields
		}
	}

	if req.IncludeHighlights {
		bleveReq.Highlight = bleve.NewHighlightWithStyle(ansi.Name)
	}

	return bleveReq
}

// buildBleveRequest constructs a Bleve SearchRequest from our SearchRequest.
// Deprecated: Use buildBleveRequestWithFields for better control over field loading.
func (m *IndexManager) buildBleveRequest(req *search.SearchRequest) *bleve.SearchRequest {
	return m.buildBleveRequestWithFields(req, DefaultFieldConfig())
}

// GetDocumentContent retrieves only the content field for a document by ID.
// This enables lazy-loading of content when SearchWithFields was used with
// DefaultFieldConfig (metadata-only). Returns empty string if document not found.
// Returns ErrIndexClosed if the index is not open.
// Returns ErrEmptyDocumentID if docID is empty.
func (m *IndexManager) GetDocumentContent(ctx context.Context, docID string) (string, error) {
	if docID == "" {
		return "", ErrEmptyDocumentID
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed || m.index == nil {
		return "", ErrIndexClosed
	}

	// Use a search request for the specific document to retrieve content field
	query := bleve.NewDocIDQuery([]string{docID})
	searchReq := bleve.NewSearchRequest(query)
	searchReq.Fields = []string{"content"}
	searchReq.Size = 1

	result, err := m.index.SearchInContext(ctx, searchReq)
	if err != nil {
		return "", fmt.Errorf("search for content: %w", err)
	}

	if len(result.Hits) == 0 {
		return "", nil // Document not found
	}

	return getStringField(result.Hits[0].Fields, "content"), nil
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
