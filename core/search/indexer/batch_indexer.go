// Package indexer provides file scanning and indexing functionality for the Sylk
// Document Search System. This file implements the batch indexer for concurrent
// document indexing using a worker pool pattern.
package indexer

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// Configuration
// =============================================================================

// Default batch indexer configuration values.
const (
	BatchIndexerDefaultBatchSize      = 50
	BatchIndexerDefaultMaxConcurrency = 4
)

// BatchIndexerConfig configures the batch indexer.
type BatchIndexerConfig struct {
	// BatchSize is the number of documents per batch (default: 50)
	BatchSize int

	// MaxConcurrency is the max concurrent workers (default: 4)
	MaxConcurrency int

	// ProgressCallback is called with progress updates during indexing
	ProgressCallback BatchProgressCallback
}

// BatchProgressCallback is called with progress updates during batch indexing.
type BatchProgressCallback func(progress *BatchIndexProgress)

// =============================================================================
// Progress Types
// =============================================================================

// BatchIndexProgress reports the current batch indexing progress.
type BatchIndexProgress struct {
	TotalFiles     int64
	ProcessedFiles int64
	IndexedFiles   int64
	FailedFiles    int64
	CurrentFile    string
}

// =============================================================================
// Result Types
// =============================================================================

// BatchIndexingResult contains the final result of a batch indexing operation.
type BatchIndexingResult struct {
	TotalFiles   int64
	IndexedFiles int64
	FailedFiles  int64
	Errors       []error
	Duration     time.Duration
}

// =============================================================================
// Interfaces
// =============================================================================

// BatchIndexManagerInterface abstracts the index manager for batch indexing.
type BatchIndexManagerInterface interface {
	IndexBatch(ctx context.Context, docs []*search.Document) error
}

// BatchDocumentBuilderInterface abstracts document building for batch indexing.
type BatchDocumentBuilderInterface interface {
	BuildFromFile(ctx context.Context, info *FileInfo) (*search.Document, error)
}

// =============================================================================
// BatchIndexer
// =============================================================================

// BatchIndexer coordinates concurrent document indexing using a worker pool.
type BatchIndexer struct {
	config       BatchIndexerConfig
	builder      BatchDocumentBuilderInterface
	indexManager BatchIndexManagerInterface
}

// NewBatchIndexer creates a new batch indexer.
func NewBatchIndexer(
	config BatchIndexerConfig,
	builder BatchDocumentBuilderInterface,
	indexManager BatchIndexManagerInterface,
) *BatchIndexer {
	return &BatchIndexer{
		config:       config,
		builder:      builder,
		indexManager: indexManager,
	}
}

// =============================================================================
// Run Method
// =============================================================================

// Run indexes files from the given channel concurrently.
// Returns partial results if context is cancelled.
func (b *BatchIndexer) Run(ctx context.Context, files <-chan *FileInfo) (*BatchIndexingResult, error) {
	startTime := time.Now()

	state := b.newBatchIndexingState()
	b.startWorkers(ctx, files, state)
	state.wg.Wait()

	return b.buildResult(state, startTime), nil
}

// =============================================================================
// Worker Pool
// =============================================================================

// batchIndexingState holds shared state during batch indexing.
type batchIndexingState struct {
	processed atomic.Int64
	indexed   atomic.Int64
	failed    atomic.Int64
	total     atomic.Int64

	errorsMu sync.Mutex
	errors   []error

	wg sync.WaitGroup
}

// newBatchIndexingState creates a new batch indexing state.
func (b *BatchIndexer) newBatchIndexingState() *batchIndexingState {
	return &batchIndexingState{
		errors: make([]error, 0),
	}
}

// startWorkers spawns worker goroutines to process files.
func (b *BatchIndexer) startWorkers(ctx context.Context, files <-chan *FileInfo, state *batchIndexingState) {
	workerCount := b.getWorkerCount()

	for i := 0; i < workerCount; i++ {
		state.wg.Add(1)
		go b.worker(ctx, files, state)
	}
}

// getWorkerCount returns the effective worker count.
func (b *BatchIndexer) getWorkerCount() int {
	if b.config.MaxConcurrency <= 0 {
		return BatchIndexerDefaultMaxConcurrency
	}
	return b.config.MaxConcurrency
}

// getBatchSize returns the effective batch size.
func (b *BatchIndexer) getBatchSize() int {
	if b.config.BatchSize <= 0 {
		return BatchIndexerDefaultBatchSize
	}
	return b.config.BatchSize
}

// =============================================================================
// Worker Logic
// =============================================================================

// worker processes files from the channel.
func (b *BatchIndexer) worker(ctx context.Context, files <-chan *FileInfo, state *batchIndexingState) {
	defer state.wg.Done()

	batch := make([]*search.Document, 0, b.getBatchSize())

	for {
		shouldContinue := b.processNextFile(ctx, files, state, &batch)
		if !shouldContinue {
			break
		}
	}

	b.flushBatch(ctx, batch, state)
}

// processNextFile processes the next file from the channel.
// Returns false if processing should stop.
func (b *BatchIndexer) processNextFile(
	ctx context.Context,
	files <-chan *FileInfo,
	state *batchIndexingState,
	batch *[]*search.Document,
) bool {
	select {
	case <-ctx.Done():
		return false
	case file, ok := <-files:
		if !ok {
			return false
		}
		b.handleFile(ctx, file, state, batch)
		return true
	}
}

// handleFile processes a single file.
func (b *BatchIndexer) handleFile(
	ctx context.Context,
	file *FileInfo,
	state *batchIndexingState,
	batch *[]*search.Document,
) {
	state.total.Add(1)

	if file == nil {
		b.recordFailure(state, nil)
		return
	}

	b.reportProgress(state, file.Path)

	doc, err := b.builder.BuildFromFile(ctx, file)
	if err != nil {
		b.recordFailure(state, err)
		return
	}

	*batch = append(*batch, doc)
	b.checkAndFlushBatch(ctx, batch, state)
}

// checkAndFlushBatch flushes the batch if it reaches the batch size.
func (b *BatchIndexer) checkAndFlushBatch(
	ctx context.Context,
	batch *[]*search.Document,
	state *batchIndexingState,
) {
	if len(*batch) >= b.getBatchSize() {
		b.flushBatch(ctx, *batch, state)
		*batch = make([]*search.Document, 0, b.getBatchSize())
	}
}

// flushBatch sends the batch to the index manager.
func (b *BatchIndexer) flushBatch(ctx context.Context, batch []*search.Document, state *batchIndexingState) {
	if len(batch) == 0 {
		return
	}

	err := b.indexManager.IndexBatch(ctx, batch)
	if err != nil {
		b.recordBatchFailure(state, batch, err)
		return
	}

	b.recordBatchSuccess(state, batch)
}

// =============================================================================
// State Updates
// =============================================================================

// recordFailure records a single file failure.
func (b *BatchIndexer) recordFailure(state *batchIndexingState, err error) {
	state.processed.Add(1)
	state.failed.Add(1)

	if err != nil {
		state.errorsMu.Lock()
		state.errors = append(state.errors, err)
		state.errorsMu.Unlock()
	}
}

// recordBatchSuccess records successful batch indexing.
func (b *BatchIndexer) recordBatchSuccess(state *batchIndexingState, batch []*search.Document) {
	count := int64(len(batch))
	state.processed.Add(count)
	state.indexed.Add(count)
}

// recordBatchFailure records batch indexing failure.
func (b *BatchIndexer) recordBatchFailure(state *batchIndexingState, batch []*search.Document, err error) {
	count := int64(len(batch))
	state.processed.Add(count)
	state.failed.Add(count)

	state.errorsMu.Lock()
	state.errors = append(state.errors, err)
	state.errorsMu.Unlock()
}

// reportProgress calls the progress callback if configured.
func (b *BatchIndexer) reportProgress(state *batchIndexingState, currentFile string) {
	if b.config.ProgressCallback == nil {
		return
	}

	progress := &BatchIndexProgress{
		TotalFiles:     state.total.Load(),
		ProcessedFiles: state.processed.Load(),
		IndexedFiles:   state.indexed.Load(),
		FailedFiles:    state.failed.Load(),
		CurrentFile:    currentFile,
	}

	b.config.ProgressCallback(progress)
}

// =============================================================================
// Result Building
// =============================================================================

// buildResult constructs the final BatchIndexingResult.
func (b *BatchIndexer) buildResult(state *batchIndexingState, startTime time.Time) *BatchIndexingResult {
	state.errorsMu.Lock()
	errors := make([]error, len(state.errors))
	copy(errors, state.errors)
	state.errorsMu.Unlock()

	return &BatchIndexingResult{
		TotalFiles:   state.total.Load(),
		IndexedFiles: state.indexed.Load(),
		FailedFiles:  state.failed.Load(),
		Errors:       errors,
		Duration:     time.Since(startTime),
	}
}
