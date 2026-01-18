// Package search provides document indexing and search functionality for Sylk.
package search

import (
	"errors"
	"time"
)

// =============================================================================
// Constants
// =============================================================================

const (
	// MaxBatchSize is the maximum number of documents allowed in a single batch.
	MaxBatchSize = 100

	// MaxBatchBytes is the maximum total size in bytes for a document batch (10MB).
	MaxBatchBytes = 10 * 1024 * 1024
)

// =============================================================================
// Errors
// =============================================================================

var (
	// ErrBatchFull indicates the batch has reached MaxBatchSize.
	ErrBatchFull = errors.New("batch is full: maximum document count reached")

	// ErrBatchSizeExceeded indicates adding the document would exceed MaxBatchBytes.
	ErrBatchSizeExceeded = errors.New("batch size exceeded: adding document would exceed maximum bytes")

	// ErrNilDocument indicates a nil document was provided.
	ErrNilDocument = errors.New("document cannot be nil")
)

// =============================================================================
// DocumentBatch
// =============================================================================

// DocumentBatch holds a collection of documents for batch indexing operations.
// It enforces size limits to prevent memory exhaustion during bulk operations.
type DocumentBatch struct {
	// Documents contains the documents in this batch.
	Documents []*Document

	// TotalSize is the cumulative size in bytes of all document content.
	TotalSize int64

	// IndexedCount tracks how many documents have been successfully indexed.
	IndexedCount int
}

// NewDocumentBatch creates a new empty DocumentBatch with pre-allocated capacity.
func NewDocumentBatch() *DocumentBatch {
	return &DocumentBatch{
		Documents: make([]*Document, 0, MaxBatchSize),
	}
}

// Add adds a document to the batch if it doesn't exceed limits.
// Returns ErrNilDocument if doc is nil.
// Returns ErrBatchFull if the batch already contains MaxBatchSize documents.
// Returns ErrBatchSizeExceeded if adding the document would exceed MaxBatchBytes.
func (b *DocumentBatch) Add(doc *Document) error {
	if doc == nil {
		return ErrNilDocument
	}

	if len(b.Documents) >= MaxBatchSize {
		return ErrBatchFull
	}

	// Content is a string, so len returns the byte count
	docSize := int64(len(doc.Content))
	if b.TotalSize+docSize > MaxBatchBytes {
		return ErrBatchSizeExceeded
	}

	b.Documents = append(b.Documents, doc)
	b.TotalSize += docSize

	return nil
}

// IsFull returns true if the batch has reached MaxBatchSize documents
// or would exceed MaxBatchBytes with a typical document.
func (b *DocumentBatch) IsFull() bool {
	return len(b.Documents) >= MaxBatchSize
}

// Clear resets the batch to an empty state, ready for reuse.
func (b *DocumentBatch) Clear() {
	b.Documents = b.Documents[:0]
	b.TotalSize = 0
	b.IndexedCount = 0
}

// TotalDocuments returns the number of documents currently in the batch.
func (b *DocumentBatch) TotalDocuments() int {
	return len(b.Documents)
}

// =============================================================================
// IndexError
// =============================================================================

// IndexError represents a failure to index a single document.
type IndexError struct {
	// DocumentID is the ID of the document that failed to index.
	DocumentID string `json:"document_id"`

	// Path is the file path of the document, if applicable.
	Path string `json:"path,omitempty"`

	// Error is the error message describing the failure.
	Error string `json:"error"`

	// Retryable indicates whether the indexing operation can be retried.
	Retryable bool `json:"retryable"`
}

// =============================================================================
// IndexingResult
// =============================================================================

// IndexingResult contains the outcome of a batch indexing operation.
type IndexingResult struct {
	// Indexed is the count of documents successfully indexed.
	Indexed int `json:"indexed"`

	// Failed is the count of documents that failed to index.
	Failed int `json:"failed"`

	// Errors contains details for each indexing failure.
	Errors []IndexError `json:"errors,omitempty"`

	// Duration is how long the indexing operation took.
	Duration time.Duration `json:"duration"`
}

// NewIndexingResult creates a new IndexingResult initialized for collecting results.
func NewIndexingResult() *IndexingResult {
	return &IndexingResult{
		Errors: make([]IndexError, 0),
	}
}

// Success returns true if all documents were indexed without errors.
func (r *IndexingResult) Success() bool {
	return r.Failed == 0 && r.Indexed > 0
}

// SuccessRate returns the proportion of documents successfully indexed.
// Returns 0.0 if no documents were processed, 1.0 if all succeeded.
func (r *IndexingResult) SuccessRate() float64 {
	total := r.Indexed + r.Failed
	if total == 0 {
		return 0.0
	}
	return float64(r.Indexed) / float64(total)
}

// RetryableErrors returns all errors that are marked as retryable.
func (r *IndexingResult) RetryableErrors() []IndexError {
	retryable := make([]IndexError, 0, len(r.Errors))
	for _, err := range r.Errors {
		if err.Retryable {
			retryable = append(retryable, err)
		}
	}
	return retryable
}

// AddSuccess increments the indexed count.
func (r *IndexingResult) AddSuccess() {
	r.Indexed++
}

// AddFailure records a failed indexing attempt.
func (r *IndexingResult) AddFailure(docID, path, errMsg string, retryable bool) {
	r.Failed++
	r.Errors = append(r.Errors, IndexError{
		DocumentID: docID,
		Path:       path,
		Error:      errMsg,
		Retryable:  retryable,
	})
}
