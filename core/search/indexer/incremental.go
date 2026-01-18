// Package indexer provides file scanning and indexing functionality for the Sylk
// Document Search System. It supports incremental indexing based on file checksums.
package indexer

import (
	"context"
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

// IncrementalIndexerConfig configures the incremental indexer.
type IncrementalIndexerConfig struct {
	// BatchSize is the number of documents to process per batch.
	BatchSize int

	// MaxConcurrency is the maximum number of concurrent operations.
	MaxConcurrency int
}

// =============================================================================
// Incremental Indexer
// =============================================================================

// IncrementalIndexer handles incremental indexing of changed files.
// It only indexes files whose checksums have changed.
type IncrementalIndexer struct {
	config        IncrementalIndexerConfig
	indexManager  IndexManagerInterface
	checksumStore ChecksumStore
	docBuilder    DocumentBuilder
}

// NewIncrementalIndexer creates a new incremental indexer.
func NewIncrementalIndexer(
	config IncrementalIndexerConfig,
	indexManager IndexManagerInterface,
	checksumStore ChecksumStore,
	docBuilder DocumentBuilder,
) *IncrementalIndexer {
	return &IncrementalIndexer{
		config:        config,
		indexManager:  indexManager,
		checksumStore: checksumStore,
		docBuilder:    docBuilder,
	}
}

// Index processes a list of file changes.
// Returns partial results if context is cancelled.
func (i *IncrementalIndexer) Index(ctx context.Context, changes []FileChange) (*search.IndexingResult, error) {
	startTime := time.Now()
	result := search.NewIndexingResult()

	for _, change := range changes {
		if err := i.checkContext(ctx); err != nil {
			result.Duration = time.Since(startTime)
			return result, err
		}
		i.processChange(ctx, change, result)
	}

	result.Duration = time.Since(startTime)
	return result, nil
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
