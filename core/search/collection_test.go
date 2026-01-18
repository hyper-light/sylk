package search

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

func newTestDocument(id string, contentSize int) *Document {
	content := strings.Repeat("x", contentSize)
	return &Document{
		ID:         id,
		Path:       "/test/path/" + id + ".go",
		Type:       DocTypeSourceCode,
		Content:    content,
		Checksum:   GenerateChecksum([]byte(content)),
		ModifiedAt: time.Now(),
		IndexedAt:  time.Now(),
	}
}

// =============================================================================
// DocumentBatch Tests
// =============================================================================

func TestNewDocumentBatch(t *testing.T) {
	batch := NewDocumentBatch()

	require.NotNil(t, batch)
	assert.Empty(t, batch.Documents)
	assert.Equal(t, int64(0), batch.TotalSize)
	assert.Equal(t, 0, batch.IndexedCount)
}

func TestDocumentBatch_Add_Success(t *testing.T) {
	batch := NewDocumentBatch()
	doc := newTestDocument("doc1", 100)

	err := batch.Add(doc)

	require.NoError(t, err)
	assert.Len(t, batch.Documents, 1)
	assert.Equal(t, int64(100), batch.TotalSize)
	assert.Same(t, doc, batch.Documents[0])
}

func TestDocumentBatch_Add_MultipleDocuments(t *testing.T) {
	batch := NewDocumentBatch()

	for i := 0; i < 5; i++ {
		doc := newTestDocument(string(rune('A'+i)), 100)
		err := batch.Add(doc)
		require.NoError(t, err)
	}

	assert.Len(t, batch.Documents, 5)
	assert.Equal(t, int64(500), batch.TotalSize)
}

func TestDocumentBatch_Add_NilDocument(t *testing.T) {
	batch := NewDocumentBatch()

	err := batch.Add(nil)

	require.Error(t, err)
	assert.Equal(t, ErrNilDocument, err)
	assert.Empty(t, batch.Documents)
}

func TestDocumentBatch_Add_BatchFull(t *testing.T) {
	batch := NewDocumentBatch()

	// Fill the batch to MaxBatchSize
	for i := 0; i < MaxBatchSize; i++ {
		doc := newTestDocument(string(rune(i)), 10)
		err := batch.Add(doc)
		require.NoError(t, err)
	}

	// Try to add one more
	extraDoc := newTestDocument("extra", 10)
	err := batch.Add(extraDoc)

	require.Error(t, err)
	assert.Equal(t, ErrBatchFull, err)
	assert.Len(t, batch.Documents, MaxBatchSize)
}

func TestDocumentBatch_Add_SizeExceeded(t *testing.T) {
	batch := NewDocumentBatch()

	// Add a document that takes up most of the space
	largeDoc := newTestDocument("large", MaxBatchBytes-100)
	err := batch.Add(largeDoc)
	require.NoError(t, err)

	// Try to add another that would exceed the limit
	overflowDoc := newTestDocument("overflow", 200)
	err = batch.Add(overflowDoc)

	require.Error(t, err)
	assert.Equal(t, ErrBatchSizeExceeded, err)
	assert.Len(t, batch.Documents, 1)
	assert.Equal(t, int64(MaxBatchBytes-100), batch.TotalSize)
}

func TestDocumentBatch_Add_ExactlyMaxBytes(t *testing.T) {
	batch := NewDocumentBatch()

	// Add a document that fills exactly to MaxBatchBytes
	doc := newTestDocument("exact", MaxBatchBytes)
	err := batch.Add(doc)

	require.NoError(t, err)
	assert.Len(t, batch.Documents, 1)
	assert.Equal(t, int64(MaxBatchBytes), batch.TotalSize)

	// Any additional document should fail
	tinyDoc := newTestDocument("tiny", 1)
	err = batch.Add(tinyDoc)
	assert.Equal(t, ErrBatchSizeExceeded, err)
}

func TestDocumentBatch_Add_EmptyContent(t *testing.T) {
	batch := NewDocumentBatch()
	doc := newTestDocument("empty", 0)

	err := batch.Add(doc)

	require.NoError(t, err)
	assert.Len(t, batch.Documents, 1)
	assert.Equal(t, int64(0), batch.TotalSize)
}

func TestDocumentBatch_IsFull_Empty(t *testing.T) {
	batch := NewDocumentBatch()

	assert.False(t, batch.IsFull())
}

func TestDocumentBatch_IsFull_PartiallyFilled(t *testing.T) {
	batch := NewDocumentBatch()
	for i := 0; i < MaxBatchSize/2; i++ {
		_ = batch.Add(newTestDocument(string(rune(i)), 10))
	}

	assert.False(t, batch.IsFull())
}

func TestDocumentBatch_IsFull_AtMaxCount(t *testing.T) {
	batch := NewDocumentBatch()
	for i := 0; i < MaxBatchSize; i++ {
		_ = batch.Add(newTestDocument(string(rune(i)), 10))
	}

	assert.True(t, batch.IsFull())
}

func TestDocumentBatch_Clear(t *testing.T) {
	batch := NewDocumentBatch()

	// Add some documents
	for i := 0; i < 5; i++ {
		_ = batch.Add(newTestDocument(string(rune('A'+i)), 100))
	}
	batch.IndexedCount = 3

	batch.Clear()

	assert.Empty(t, batch.Documents)
	assert.Equal(t, int64(0), batch.TotalSize)
	assert.Equal(t, 0, batch.IndexedCount)
}

func TestDocumentBatch_Clear_Empty(t *testing.T) {
	batch := NewDocumentBatch()

	// Clearing an empty batch should not panic
	batch.Clear()

	assert.Empty(t, batch.Documents)
	assert.Equal(t, int64(0), batch.TotalSize)
}

func TestDocumentBatch_TotalDocuments(t *testing.T) {
	batch := NewDocumentBatch()

	assert.Equal(t, 0, batch.TotalDocuments())

	_ = batch.Add(newTestDocument("1", 10))
	assert.Equal(t, 1, batch.TotalDocuments())

	_ = batch.Add(newTestDocument("2", 10))
	assert.Equal(t, 2, batch.TotalDocuments())

	batch.Clear()
	assert.Equal(t, 0, batch.TotalDocuments())
}

func TestDocumentBatch_ReusableAfterClear(t *testing.T) {
	batch := NewDocumentBatch()

	// Fill batch
	for i := 0; i < MaxBatchSize; i++ {
		_ = batch.Add(newTestDocument(string(rune(i)), 10))
	}
	assert.True(t, batch.IsFull())

	// Clear and reuse
	batch.Clear()
	assert.False(t, batch.IsFull())

	err := batch.Add(newTestDocument("new", 50))
	require.NoError(t, err)
	assert.Equal(t, 1, batch.TotalDocuments())
	assert.Equal(t, int64(50), batch.TotalSize)
}

// =============================================================================
// IndexError Tests
// =============================================================================

func TestIndexError_Fields(t *testing.T) {
	err := IndexError{
		DocumentID: "doc-123",
		Path:       "/path/to/file.go",
		Error:      "failed to parse",
		Retryable:  true,
	}

	assert.Equal(t, "doc-123", err.DocumentID)
	assert.Equal(t, "/path/to/file.go", err.Path)
	assert.Equal(t, "failed to parse", err.Error)
	assert.True(t, err.Retryable)
}

func TestIndexError_NonRetryable(t *testing.T) {
	err := IndexError{
		DocumentID: "doc-456",
		Path:       "",
		Error:      "permanent failure",
		Retryable:  false,
	}

	assert.False(t, err.Retryable)
}

// =============================================================================
// IndexingResult Tests
// =============================================================================

func TestNewIndexingResult(t *testing.T) {
	result := NewIndexingResult()

	require.NotNil(t, result)
	assert.Equal(t, 0, result.Indexed)
	assert.Equal(t, 0, result.Failed)
	assert.Empty(t, result.Errors)
	assert.Equal(t, time.Duration(0), result.Duration)
}

func TestIndexingResult_Success_AllSucceeded(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 10
	result.Failed = 0

	assert.True(t, result.Success())
}

func TestIndexingResult_Success_SomeFailed(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 8
	result.Failed = 2

	assert.False(t, result.Success())
}

func TestIndexingResult_Success_AllFailed(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 0
	result.Failed = 5

	assert.False(t, result.Success())
}

func TestIndexingResult_Success_NoDocuments(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 0
	result.Failed = 0

	// No documents indexed is not considered success
	assert.False(t, result.Success())
}

func TestIndexingResult_SuccessRate_AllSucceeded(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 10
	result.Failed = 0

	assert.InDelta(t, 1.0, result.SuccessRate(), 0.001)
}

func TestIndexingResult_SuccessRate_AllFailed(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 0
	result.Failed = 10

	assert.InDelta(t, 0.0, result.SuccessRate(), 0.001)
}

func TestIndexingResult_SuccessRate_Mixed(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 7
	result.Failed = 3

	assert.InDelta(t, 0.7, result.SuccessRate(), 0.001)
}

func TestIndexingResult_SuccessRate_NoDocuments(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 0
	result.Failed = 0

	assert.InDelta(t, 0.0, result.SuccessRate(), 0.001)
}

func TestIndexingResult_SuccessRate_Precision(t *testing.T) {
	result := NewIndexingResult()
	result.Indexed = 1
	result.Failed = 2

	assert.InDelta(t, 0.333, result.SuccessRate(), 0.01)
}

func TestIndexingResult_RetryableErrors_AllRetryable(t *testing.T) {
	result := NewIndexingResult()
	result.Errors = []IndexError{
		{DocumentID: "1", Error: "timeout", Retryable: true},
		{DocumentID: "2", Error: "network error", Retryable: true},
	}

	retryable := result.RetryableErrors()

	assert.Len(t, retryable, 2)
}

func TestIndexingResult_RetryableErrors_NoneRetryable(t *testing.T) {
	result := NewIndexingResult()
	result.Errors = []IndexError{
		{DocumentID: "1", Error: "invalid format", Retryable: false},
		{DocumentID: "2", Error: "unsupported type", Retryable: false},
	}

	retryable := result.RetryableErrors()

	assert.Empty(t, retryable)
}

func TestIndexingResult_RetryableErrors_Mixed(t *testing.T) {
	result := NewIndexingResult()
	result.Errors = []IndexError{
		{DocumentID: "1", Error: "timeout", Retryable: true},
		{DocumentID: "2", Error: "invalid format", Retryable: false},
		{DocumentID: "3", Error: "network error", Retryable: true},
		{DocumentID: "4", Error: "unsupported type", Retryable: false},
	}

	retryable := result.RetryableErrors()

	assert.Len(t, retryable, 2)
	assert.Equal(t, "1", retryable[0].DocumentID)
	assert.Equal(t, "3", retryable[1].DocumentID)
}

func TestIndexingResult_RetryableErrors_NoErrors(t *testing.T) {
	result := NewIndexingResult()

	retryable := result.RetryableErrors()

	assert.Empty(t, retryable)
	assert.NotNil(t, retryable) // Should return empty slice, not nil
}

func TestIndexingResult_AddSuccess(t *testing.T) {
	result := NewIndexingResult()

	result.AddSuccess()
	assert.Equal(t, 1, result.Indexed)

	result.AddSuccess()
	assert.Equal(t, 2, result.Indexed)
}

func TestIndexingResult_AddFailure(t *testing.T) {
	result := NewIndexingResult()

	result.AddFailure("doc-1", "/path/to/file.go", "parse error", true)

	assert.Equal(t, 1, result.Failed)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "doc-1", result.Errors[0].DocumentID)
	assert.Equal(t, "/path/to/file.go", result.Errors[0].Path)
	assert.Equal(t, "parse error", result.Errors[0].Error)
	assert.True(t, result.Errors[0].Retryable)
}

func TestIndexingResult_AddFailure_Multiple(t *testing.T) {
	result := NewIndexingResult()

	result.AddFailure("doc-1", "/file1.go", "error 1", true)
	result.AddFailure("doc-2", "/file2.go", "error 2", false)
	result.AddFailure("doc-3", "", "error 3", true)

	assert.Equal(t, 3, result.Failed)
	assert.Len(t, result.Errors, 3)
}

func TestIndexingResult_AddFailure_EmptyPath(t *testing.T) {
	result := NewIndexingResult()

	result.AddFailure("doc-1", "", "no path provided", false)

	require.Len(t, result.Errors, 1)
	assert.Empty(t, result.Errors[0].Path)
}

func TestIndexingResult_Duration(t *testing.T) {
	result := NewIndexingResult()
	result.Duration = 5 * time.Second

	assert.Equal(t, 5*time.Second, result.Duration)
}

// =============================================================================
// Constants Tests
// =============================================================================

func TestConstants(t *testing.T) {
	assert.Equal(t, 100, MaxBatchSize)
	assert.Equal(t, int64(10*1024*1024), int64(MaxBatchBytes))
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestDocumentBatch_FullWorkflow(t *testing.T) {
	batch := NewDocumentBatch()
	result := NewIndexingResult()

	// Simulate adding and indexing documents
	for i := 0; i < 10; i++ {
		doc := newTestDocument(string(rune('A'+i)), 1000)
		err := batch.Add(doc)
		require.NoError(t, err)
	}

	// Simulate indexing with some failures
	for i, doc := range batch.Documents {
		if i%3 == 0 { // Every 3rd document fails
			result.AddFailure(doc.ID, "", "simulated failure", i%2 == 0)
		} else {
			result.AddSuccess()
			batch.IndexedCount++
		}
	}

	assert.Equal(t, 10, batch.TotalDocuments())
	assert.Equal(t, 6, result.Indexed)
	assert.Equal(t, 4, result.Failed)
	assert.InDelta(t, 0.6, result.SuccessRate(), 0.001)
	assert.Len(t, result.RetryableErrors(), 2)
}

func TestDocumentBatch_BatchAndClearCycle(t *testing.T) {
	batch := NewDocumentBatch()
	totalProcessed := 0

	// Simulate processing 250 documents in batches
	for cycle := 0; cycle < 3; cycle++ {
		// Fill a batch
		for i := 0; i < MaxBatchSize && totalProcessed < 250; i++ {
			doc := newTestDocument(string(rune(totalProcessed)), 50)
			err := batch.Add(doc)
			if err == nil {
				totalProcessed++
			}
		}

		// Process batch (simulated)
		processed := batch.TotalDocuments()

		// Clear for next cycle
		batch.Clear()

		// Verify batch is empty
		assert.Empty(t, batch.Documents)
		assert.Equal(t, int64(0), batch.TotalSize)

		// First two cycles should process 100 docs, last cycle 50
		if cycle < 2 {
			assert.Equal(t, MaxBatchSize, processed)
		} else {
			assert.Equal(t, 50, processed)
		}
	}

	assert.Equal(t, 250, totalProcessed)
}
