// Package indexer provides file scanning and indexing functionality for the Sylk
// Document Search System. It supports incremental indexing based on file checksums.
package indexer

import (
	"context"
	"runtime"
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

// Default configuration values for batch accumulation and worker pool.
const (
	// DefaultFlushInterval is the default interval for flushing pending changes.
	DefaultFlushInterval = 100 * time.Millisecond

	// DefaultMaxBatchSize is the default maximum batch size before auto-flush.
	DefaultMaxBatchSize = 100

	// DefaultWorkerCount is the default number of concurrent workers for processing.
	// Set to 0 to use runtime.NumCPU().
	DefaultWorkerCount = 0

	// MinWorkerCount is the minimum number of workers.
	MinWorkerCount = 1

	// MaxWorkerCount is the maximum number of workers to prevent resource exhaustion.
	MaxWorkerCount = 32
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

	// WorkerCount is the number of concurrent workers for processing changes.
	// Set to 0 to use runtime.NumCPU(). Default: 0.
	WorkerCount int
}

// =============================================================================
// Incremental Indexer
// =============================================================================

// IncrementalIndexer handles incremental indexing of changed files.
// It only indexes files whose checksums have changed.
// It supports batch accumulation for efficient bulk operations.
// PF.3.6/W4M.11: Now uses a concurrent worker pool for processing changes.
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

	// W4M.11: Worker pool configuration
	workerCount int
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

	// W4M.11: Configure worker count
	workerCount := normalizeWorkerCount(config.WorkerCount)

	return &IncrementalIndexer{
		config:         config,
		indexManager:   indexManager,
		checksumStore:  checksumStore,
		docBuilder:     docBuilder,
		pendingChanges: make([]*FileChange, 0, maxBatchSize),
		flushInterval:  flushInterval,
		maxBatchSize:   maxBatchSize,
		workerCount:    workerCount,
	}
}

// normalizeWorkerCount ensures worker count is within valid bounds.
func normalizeWorkerCount(count int) int {
	if count <= 0 {
		count = runtime.NumCPU()
	}
	if count < MinWorkerCount {
		return MinWorkerCount
	}
	if count > MaxWorkerCount {
		return MaxWorkerCount
	}
	return count
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

// FlushPending processes all accumulated changes as a batch using a worker pool.
// W4M.11: Uses concurrent workers to prepare documents in parallel.
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

	// W4M.11: Process indexing with concurrent worker pool
	if len(toIndex) > 0 {
		docs, checksumUpdates := i.prepareDocsWithWorkerPool(ctx, toIndex)

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

	// Process deletes (deletes are typically fast, no need for worker pool)
	for _, c := range toDelete {
		if err := i.indexManager.DeleteByPath(ctx, c.Path); err != nil {
			// Continue processing other deletes on error
			continue
		}
		i.checksumStore.DeleteChecksum(c.Path)
	}

	return nil
}

// prepareDocsWithWorkerPool prepares documents using a concurrent worker pool.
// W4M.11: This replaces sequential document preparation with parallel processing.
func (i *IncrementalIndexer) prepareDocsWithWorkerPool(
	ctx context.Context,
	changes []*FileChange,
) ([]*search.Document, []checksumUpdate) {
	// For small batches, use sequential processing (less overhead)
	if len(changes) <= i.workerCount {
		return i.prepareDocsSequential(changes)
	}

	// Create work channel
	workCh := make(chan *FileChange, len(changes))
	for _, c := range changes {
		workCh <- c
	}
	close(workCh)

	// Result collection
	type prepareResult struct {
		doc    *search.Document
		update checksumUpdate
	}
	resultCh := make(chan prepareResult, len(changes))

	// Launch workers
	var wg sync.WaitGroup
	for w := 0; w < i.workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range workCh {
				// Check context cancellation
				select {
				case <-ctx.Done():
					return
				default:
				}

				doc, update, err := i.prepareForIndex(c)
				if err != nil || doc == nil {
					continue
				}
				resultCh <- prepareResult{doc: doc, update: update}
			}
		}()
	}

	// Close result channel when all workers complete
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results
	docs := make([]*search.Document, 0, len(changes))
	updates := make([]checksumUpdate, 0, len(changes))
	for r := range resultCh {
		docs = append(docs, r.doc)
		updates = append(updates, r.update)
	}

	return docs, updates
}

// prepareDocsSequential prepares documents sequentially for small batches.
func (i *IncrementalIndexer) prepareDocsSequential(
	changes []*FileChange,
) ([]*search.Document, []checksumUpdate) {
	docs := make([]*search.Document, 0, len(changes))
	updates := make([]checksumUpdate, 0, len(changes))

	for _, c := range changes {
		doc, update, err := i.prepareForIndex(c)
		if err != nil || doc == nil {
			continue
		}
		docs = append(docs, doc)
		updates = append(updates, update)
	}

	return docs, updates
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

// WorkerCount returns the configured number of concurrent workers.
// W4M.11: Used for concurrent document preparation.
func (i *IncrementalIndexer) WorkerCount() int {
	return i.workerCount
}
