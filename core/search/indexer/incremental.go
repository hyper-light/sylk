// Package indexer provides file scanning and indexing functionality for the Sylk
// Document Search System. It supports incremental indexing based on file checksums.
package indexer

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Change Operation
// =============================================================================

// ChangeOperation represents the type of file change.
type ChangeOperation int

const (
	// OpCreate indicates a new file was created.
	OpCreate ChangeOperation = iota

	// OpModify indicates an existing file was modified.
	OpModify

	// OpDelete indicates a file was deleted.
	OpDelete

	// OpRename indicates a file was renamed.
	OpRename
)

// String returns the string representation of the change operation.
func (op ChangeOperation) String() string {
	switch op {
	case OpCreate:
		return "create"
	case OpModify:
		return "modify"
	case OpDelete:
		return "delete"
	case OpRename:
		return "rename"
	default:
		return "unknown"
	}
}

// =============================================================================
// File Change
// =============================================================================

// FileChange represents a change to a file.
type FileChange struct {
	// Path is the current path of the file.
	Path string

	// Operation is the type of change (create, modify, delete, rename).
	Operation ChangeOperation

	// FileInfo contains metadata about the file. Nil for deletes.
	FileInfo *FileInfo

	// OldPath is the previous path for rename operations.
	OldPath string
}

// =============================================================================
// Interfaces
// =============================================================================

// IndexManagerInterface defines the indexing operations needed by IncrementalIndexer.
type IndexManagerInterface interface {
	Index(ctx context.Context, doc *search.Document) error
	IndexBatch(ctx context.Context, docs []*search.Document) error
	Delete(ctx context.Context, id string) error
	DeleteByPath(ctx context.Context, path string) error
}

// ChecksumStore provides checksum lookup for change detection.
type ChecksumStore interface {
	GetChecksum(path string) (string, bool)
	SetChecksum(path string, checksum string)
	DeleteChecksum(path string)
}

// DocumentBuilder builds search documents from file info.
type DocumentBuilder interface {
	BuildDocument(fi *FileInfo) (*search.Document, error)
}

// =============================================================================
// Configuration
// =============================================================================

// Default configuration values for batch accumulation.
const (
	// DefaultFlushInterval is the default interval for flushing pending changes.
	DefaultFlushInterval = 100 * time.Millisecond

	// DefaultMaxBatchSize is the default maximum batch size before auto-flush.
	DefaultMaxBatchSize = 100
)

// IncrementalIndexerConfig configures the incremental indexer.
type IncrementalIndexerConfig struct {
	// BatchSize is the number of documents to process per batch.
	BatchSize int

	// MaxConcurrency is the maximum number of concurrent operations.
	MaxConcurrency int

	// FlushInterval is the interval for flushing pending changes (default: 100ms).
	FlushInterval time.Duration

	// MaxBatchSize is the maximum batch size before auto-flush (default: 100).
	MaxBatchSize int
}

// =============================================================================
// Incremental Indexer
// =============================================================================

// IncrementalIndexer handles incremental indexing of changed files.
// It only indexes files whose checksums have changed.
// It supports batch accumulation for efficient bulk operations.
type IncrementalIndexer struct {
	config        IncrementalIndexerConfig
	indexManager  IndexManagerInterface
	checksumStore ChecksumStore
	docBuilder    DocumentBuilder

	// Batch accumulation fields for PF.3.6
	pendingChanges []*FileChange
	pendingMu      sync.Mutex
	flushInterval  time.Duration
	maxBatchSize   int
}

// NewIncrementalIndexer creates a new incremental indexer.
func NewIncrementalIndexer(
	config IncrementalIndexerConfig,
	indexManager IndexManagerInterface,
	checksumStore ChecksumStore,
	docBuilder DocumentBuilder,
) *IncrementalIndexer {
	flushInterval := config.FlushInterval
	if flushInterval <= 0 {
		flushInterval = DefaultFlushInterval
	}

	maxBatchSize := config.MaxBatchSize
	if maxBatchSize <= 0 {
		maxBatchSize = DefaultMaxBatchSize
	}

	return &IncrementalIndexer{
		config:         config,
		indexManager:   indexManager,
		checksumStore:  checksumStore,
		docBuilder:     docBuilder,
		pendingChanges: make([]*FileChange, 0, maxBatchSize),
		flushInterval:  flushInterval,
		maxBatchSize:   maxBatchSize,
	}
}

// Index processes a list of file changes using batch operations for efficiency.
// Changes are grouped by operation type and processed in batches.
// Returns partial results if context is cancelled.
func (i *IncrementalIndexer) Index(ctx context.Context, changes []FileChange) (*search.IndexingResult, error) {
	startTime := time.Now()
	result := search.NewIndexingResult()

	if len(changes) == 0 {
		result.Duration = time.Since(startTime)
		return result, nil
	}

	// Group changes by operation type for batch processing
	var toIndex []*search.Document
	var toDelete []string
	var checksumUpdates []checksumUpdate

	for idx := range changes {
		change := &changes[idx]

		if err := i.checkContext(ctx); err != nil {
			result.Duration = time.Since(startTime)
			return result, err
		}

		switch change.Operation {
		case OpCreate, OpModify:
			doc, update, err := i.prepareForIndex(change)
			if err != nil {
				result.AddFailure("", change.Path, err.Error(), true)
				continue
			}
			if doc == nil {
				// Skipped due to unchanged checksum
				continue
			}
			toIndex = append(toIndex, doc)
			checksumUpdates = append(checksumUpdates, update)

		case OpDelete:
			toDelete = append(toDelete, change.Path)
			i.checksumStore.DeleteChecksum(change.Path)

		case OpRename:
			// Handle rename: delete old path, index new path
			toDelete = append(toDelete, change.OldPath)
			i.checksumStore.DeleteChecksum(change.OldPath)

			doc, update, err := i.prepareForIndex(&FileChange{
				Path:      change.Path,
				Operation: OpCreate,
				FileInfo:  change.FileInfo,
			})
			if err != nil {
				result.AddFailure("", change.Path, err.Error(), true)
				continue
			}
			if doc != nil {
				toIndex = append(toIndex, doc)
				checksumUpdates = append(checksumUpdates, update)
			}
		}
	}

	// Process batch index
	if len(toIndex) > 0 {
		if err := i.indexManager.IndexBatch(ctx, toIndex); err != nil {
			// If batch fails, record all as failures
			for _, doc := range toIndex {
				result.AddFailure(doc.ID, doc.Path, err.Error(), true)
			}
		} else {
			// Update checksums and record successes
			for _, update := range checksumUpdates {
				i.checksumStore.SetChecksum(update.path, update.checksum)
				result.AddSuccess()
			}
		}
	}

	// Process deletes
	for _, path := range toDelete {
		if err := i.indexManager.DeleteByPath(ctx, path); err != nil {
			result.AddFailure("", path, err.Error(), true)
		} else {
			result.AddSuccess()
		}
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// checksumUpdate holds a pending checksum update.
type checksumUpdate struct {
	path     string
	checksum string
}

// prepareForIndex builds a document and checks if indexing is needed.
// Returns nil doc if the file should be skipped (unchanged checksum).
func (i *IncrementalIndexer) prepareForIndex(change *FileChange) (*search.Document, checksumUpdate, error) {
	if change.FileInfo == nil {
		return nil, checksumUpdate{}, errNilFileInfo
	}

	doc, err := i.docBuilder.BuildDocument(change.FileInfo)
	if err != nil {
		return nil, checksumUpdate{}, err
	}

	// Check if checksum unchanged
	if i.checksumUnchanged(change.Path, doc.Checksum) {
		return nil, checksumUpdate{}, nil // Skip unchanged file
	}

	return doc, checksumUpdate{path: change.Path, checksum: doc.Checksum}, nil
}

// errNilFileInfo is returned when FileInfo is nil for create/modify operations.
var errNilFileInfo = &fileInfoNilError{}

type fileInfoNilError struct{}

func (e *fileInfoNilError) Error() string {
	return "FileInfo is nil for create/modify operation"
}

// checkContext returns an error if context is cancelled.
func (i *IncrementalIndexer) checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// processChange handles a single file change operation.
func (i *IncrementalIndexer) processChange(ctx context.Context, change FileChange, result *search.IndexingResult) {
	switch change.Operation {
	case OpCreate, OpModify:
		i.handleCreateOrModify(ctx, change, result)
	case OpDelete:
		i.handleDelete(ctx, change, result)
	case OpRename:
		i.handleRename(ctx, change, result)
	}
}

// handleCreateOrModify indexes a new or modified file if checksum differs.
func (i *IncrementalIndexer) handleCreateOrModify(ctx context.Context, change FileChange, result *search.IndexingResult) {
	if change.FileInfo == nil {
		result.AddFailure("", change.Path, "FileInfo is nil for create/modify operation", false)
		return
	}

	doc, err := i.docBuilder.BuildDocument(change.FileInfo)
	if err != nil {
		result.AddFailure("", change.Path, err.Error(), true)
		return
	}

	if i.checksumUnchanged(change.Path, doc.Checksum) {
		return // Skip indexing unchanged file
	}

	i.indexDocument(ctx, change.Path, doc, result)
}

// checksumUnchanged returns true if stored checksum matches the new one.
func (i *IncrementalIndexer) checksumUnchanged(path, newChecksum string) bool {
	existing, ok := i.checksumStore.GetChecksum(path)
	return ok && existing == newChecksum
}

// indexDocument indexes a document and updates the checksum store on success.
func (i *IncrementalIndexer) indexDocument(ctx context.Context, path string, doc *search.Document, result *search.IndexingResult) {
	if err := i.indexManager.Index(ctx, doc); err != nil {
		result.AddFailure(doc.ID, path, err.Error(), true)
		return
	}

	i.checksumStore.SetChecksum(path, doc.Checksum)
	result.AddSuccess()
}

// handleDelete removes a file from the index and checksum store.
func (i *IncrementalIndexer) handleDelete(ctx context.Context, change FileChange, result *search.IndexingResult) {
	if err := i.indexManager.DeleteByPath(ctx, change.Path); err != nil {
		result.AddFailure("", change.Path, err.Error(), true)
		return
	}

	i.checksumStore.DeleteChecksum(change.Path)
	result.AddSuccess()
}

// handleRename deletes old path and indexes at new path.
func (i *IncrementalIndexer) handleRename(ctx context.Context, change FileChange, result *search.IndexingResult) {
	// Delete old path first (ignore error if not found)
	_ = i.indexManager.DeleteByPath(ctx, change.OldPath)
	i.checksumStore.DeleteChecksum(change.OldPath)

	// Index at new path
	i.handleCreateOrModify(ctx, FileChange{
		Path:      change.Path,
		Operation: OpCreate,
		FileInfo:  change.FileInfo,
	}, result)
}

// =============================================================================
// Batch Accumulation Methods (PF.3.6)
// =============================================================================

// AccumulateChange adds a change to the pending batch.
// If the batch reaches maxBatchSize, it triggers an automatic flush.
// This method is thread-safe.
func (i *IncrementalIndexer) AccumulateChange(change *FileChange) {
	i.pendingMu.Lock()
	i.pendingChanges = append(i.pendingChanges, change)
	shouldFlush := len(i.pendingChanges) >= i.maxBatchSize
	i.pendingMu.Unlock()

	if shouldFlush {
		_ = i.FlushPending(context.Background())
	}
}

// FlushPending processes all accumulated changes as a batch.
// Returns nil if there are no pending changes.
// This method is thread-safe.
func (i *IncrementalIndexer) FlushPending(ctx context.Context) error {
	i.pendingMu.Lock()
	changes := i.pendingChanges
	i.pendingChanges = make([]*FileChange, 0, i.maxBatchSize)
	i.pendingMu.Unlock()

	if len(changes) == 0 {
		return nil
	}

	// Group by operation type
	var toIndex []*FileChange
	var toDelete []*FileChange
	for _, c := range changes {
		if c.Operation == OpDelete {
			toDelete = append(toDelete, c)
		} else {
			toIndex = append(toIndex, c)
		}
	}

	// Batch index
	if len(toIndex) > 0 {
		docs := make([]*search.Document, 0, len(toIndex))
		checksumUpdates := make([]checksumUpdate, 0, len(toIndex))

		for _, c := range toIndex {
			doc, update, err := i.prepareForIndex(c)
			if err != nil || doc == nil {
				continue // Skip errors and unchanged files
			}
			docs = append(docs, doc)
			checksumUpdates = append(checksumUpdates, update)
		}

		if len(docs) > 0 {
			if err := i.indexManager.IndexBatch(ctx, docs); err != nil {
				return err
			}
			// Update checksums on success
			for _, update := range checksumUpdates {
				i.checksumStore.SetChecksum(update.path, update.checksum)
			}
		}
	}

	// Batch delete
	for _, c := range toDelete {
		if err := i.indexManager.DeleteByPath(ctx, c.Path); err != nil {
			// Continue processing other deletes on error
			continue
		}
		i.checksumStore.DeleteChecksum(c.Path)
	}

	return nil
}

// PendingCount returns the number of pending changes awaiting flush.
// This method is thread-safe.
func (i *IncrementalIndexer) PendingCount() int {
	i.pendingMu.Lock()
	defer i.pendingMu.Unlock()
	return len(i.pendingChanges)
}

// MaxBatchSize returns the configured maximum batch size.
func (i *IncrementalIndexer) MaxBatchSize() int {
	return i.maxBatchSize
}

// FlushInterval returns the configured flush interval.
func (i *IncrementalIndexer) FlushInterval() time.Duration {
	return i.flushInterval
}
