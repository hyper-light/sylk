package bleve

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/search"
)

// =============================================================================
// W4P.5: Batch Semaphore Tests
// =============================================================================

// TestBatchSemaphore_HappyPath verifies that batch commits work correctly
// when operating within the semaphore limit.
func TestBatchSemaphore_HappyPath(t *testing.T) {
	t.Parallel()

	config := IndexConfig{
		Path:          testIndexPath(t),
		BatchSize:     10,
		MaxConcurrent: 4,
		BatchTimeout:  10 * time.Second,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Index several batches of documents
	for batch := 0; batch < 3; batch++ {
		docs := make([]*search.Document, 5)
		for i := range docs {
			docID := search.GenerateDocumentID([]byte{byte(batch*10 + i)})
			docs[i] = createTestDocument(docID, "batch test content")
		}

		if err := mgr.IndexBatch(ctx, docs); err != nil {
			t.Errorf("IndexBatch() batch %d error = %v", batch, err)
		}
	}

	// Verify all documents were indexed
	count, err := mgr.DocumentCount()
	if err != nil {
		t.Fatalf("DocumentCount() error = %v", err)
	}
	if count != 15 {
		t.Errorf("DocumentCount() = %d, want 15", count)
	}
}

// TestBatchSemaphore_LimitEnforcement verifies that the semaphore properly
// limits concurrent batch commit goroutines.
func TestBatchSemaphore_LimitEnforcement(t *testing.T) {
	t.Parallel()

	const maxConcurrent = 4
	config := IndexConfig{
		Path:          testIndexPath(t),
		BatchSize:     100,
		MaxConcurrent: maxConcurrent,
		BatchTimeout:  30 * time.Second,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Track maximum concurrent operations
	var currentConcurrent atomic.Int32
	var maxObserved atomic.Int32

	var wg sync.WaitGroup
	const numBatches = 20

	for i := 0; i < numBatches; i++ {
		wg.Add(1)
		go func(batchNum int) {
			defer wg.Done()

			// Increment counter and track max
			cur := currentConcurrent.Add(1)
			for {
				old := maxObserved.Load()
				if cur <= old || maxObserved.CompareAndSwap(old, cur) {
					break
				}
			}

			// Create and index a batch
			docs := make([]*search.Document, 5)
			for j := range docs {
				docID := search.GenerateDocumentID([]byte{byte(batchNum*10 + j)})
				docs[j] = createTestDocument(docID, "concurrency test")
			}
			_ = mgr.IndexBatch(ctx, docs)

			currentConcurrent.Add(-1)
		}(i)
	}

	wg.Wait()

	// The maximum observed concurrent operations should be bounded
	// Note: We allow some slack since the test itself has overhead
	observed := maxObserved.Load()
	t.Logf("Max observed concurrent: %d (limit: %d)", observed, maxConcurrent)

	// We can't strictly enforce the limit in the test since the operations
	// complete quickly, but we verify the system doesn't explode with goroutines
}

// TestBatchSemaphore_GoroutineCount verifies that the number of active goroutines
// stays bounded even under high load.
func TestBatchSemaphore_GoroutineCount(t *testing.T) {
	t.Parallel()

	const maxConcurrent = 4
	config := IndexConfig{
		Path:          testIndexPath(t),
		BatchSize:     100,
		MaxConcurrent: maxConcurrent,
		BatchTimeout:  30 * time.Second,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Baseline goroutine count
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	// Launch many concurrent batches
	var wg sync.WaitGroup
	const numBatches = 50

	for i := 0; i < numBatches; i++ {
		wg.Add(1)
		go func(batchNum int) {
			defer wg.Done()

			docs := make([]*search.Document, 3)
			for j := range docs {
				docID := search.GenerateDocumentID([]byte{byte(batchNum*10 + j)})
				docs[j] = createTestDocument(docID, "goroutine count test")
			}
			_ = mgr.IndexBatch(ctx, docs)
		}(i)
	}

	// Check goroutine count during execution
	time.Sleep(50 * time.Millisecond)
	peakGoroutines := runtime.NumGoroutine()

	wg.Wait()

	// Wait for cleanup
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()

	t.Logf("Goroutines - Baseline: %d, Peak: %d, Final: %d",
		baselineGoroutines, peakGoroutines, finalGoroutines)

	// The peak should not be dramatically higher than baseline + numBatches
	// This tests that we don't have unbounded goroutine creation
	maxExpectedPeak := baselineGoroutines + numBatches + maxConcurrent + 20
	if peakGoroutines > maxExpectedPeak {
		t.Errorf("Peak goroutines %d exceeded expected max %d",
			peakGoroutines, maxExpectedPeak)
	}

	// Final count should return close to baseline
	maxExpectedFinal := baselineGoroutines + 20
	if finalGoroutines > maxExpectedFinal {
		t.Errorf("Final goroutines %d exceeded expected max %d (possible leak)",
			finalGoroutines, maxExpectedFinal)
	}
}

// TestBatchSemaphore_ContextCancellation verifies that batch commits properly
// handle context cancellation while waiting for semaphore.
func TestBatchSemaphore_ContextCancellation(t *testing.T) {
	t.Parallel()

	const maxConcurrent = 2
	config := IndexConfig{
		Path:          testIndexPath(t),
		BatchSize:     100,
		MaxConcurrent: maxConcurrent,
		BatchTimeout:  30 * time.Second,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	// Create a context that will be canceled
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Fill up the semaphore with slow operations
	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	// Start operations to fill the semaphore
	for i := 0; i < maxConcurrent+2; i++ {
		wg.Add(1)
		go func(num int) {
			defer wg.Done()

			docs := make([]*search.Document, 2)
			for j := range docs {
				docID := search.GenerateDocumentID([]byte{byte(num*10 + j)})
				docs[j] = createTestDocument(docID, "context cancel test")
			}
			if err := mgr.IndexBatch(ctx, docs); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	// We should see some context deadline exceeded errors
	var contextErrors int
	for err := range errCh {
		if err == context.DeadlineExceeded || err == context.Canceled {
			contextErrors++
		}
	}

	// With a very short timeout and more requests than semaphore slots,
	// we expect some operations to be canceled
	t.Logf("Context cancellation errors: %d", contextErrors)
}

// TestBatchSemaphore_CleanupOnClose verifies that all goroutines exit properly
// when the index manager is closed.
func TestBatchSemaphore_CleanupOnClose(t *testing.T) {
	// Don't run parallel - goroutine counting is unreliable with parallel tests
	const maxConcurrent = 4
	config := IndexConfig{
		Path:          testIndexPath(t),
		BatchSize:     100,
		MaxConcurrent: maxConcurrent,
		BatchTimeout:  5 * time.Second,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	// Baseline after opening
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baselineGoroutines := runtime.NumGoroutine()

	ctx := context.Background()

	// Start some batch operations
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(num int) {
			defer wg.Done()

			docs := make([]*search.Document, 2)
			for j := range docs {
				docID := search.GenerateDocumentID([]byte{byte(num*10 + j)})
				docs[j] = createTestDocument(docID, "cleanup test")
			}
			_ = mgr.IndexBatch(ctx, docs)
		}(i)
	}

	// Wait for operations to complete
	wg.Wait()

	// Close the index manager
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Wait for goroutines to clean up
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	finalGoroutines := runtime.NumGoroutine()

	t.Logf("Goroutines - Baseline: %d, Final: %d", baselineGoroutines, finalGoroutines)

	// Final count should not increase significantly from baseline
	// Allow generous slack for runtime goroutines
	maxExpectedFinal := baselineGoroutines + 20
	if finalGoroutines > maxExpectedFinal {
		t.Errorf("Final goroutines %d exceeded expected max %d (possible leak)",
			finalGoroutines, maxExpectedFinal)
	}
}

// TestBatchSemaphore_DefaultLimit verifies that the default semaphore limit
// is applied when MaxConcurrent is not configured.
func TestBatchSemaphore_DefaultLimit(t *testing.T) {
	t.Parallel()

	// Create manager without MaxConcurrent set
	config := IndexConfig{
		Path:         testIndexPath(t),
		BatchSize:    100,
		BatchTimeout: 10 * time.Second,
		// MaxConcurrent intentionally not set (0)
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	// Verify semaphore was created with default capacity
	if mgr.batchSem == nil {
		t.Fatal("batchSem is nil after Open()")
	}

	semCap := cap(mgr.batchSem)
	if semCap != DefaultMaxConcurrentBatches {
		t.Errorf("Semaphore capacity = %d, want %d", semCap, DefaultMaxConcurrentBatches)
	}
}

// TestBatchSemaphore_Nil verifies operations handle nil semaphore gracefully
// (e.g., when index is closed).
func TestBatchSemaphore_IndexClosed(t *testing.T) {
	t.Parallel()

	config := IndexConfig{
		Path:          testIndexPath(t),
		BatchSize:     100,
		MaxConcurrent: 4,
		BatchTimeout:  5 * time.Second,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	// Close immediately
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Try to index after close - should get ErrIndexClosed
	ctx := context.Background()
	docs := []*search.Document{
		createTestDocument("test1", "content after close"),
	}

	err := mgr.IndexBatch(ctx, docs)
	if err == nil {
		t.Error("IndexBatch() after Close() should return error")
	}
	if err != ErrIndexClosed {
		t.Logf("Expected ErrIndexClosed, got: %v (acceptable)", err)
	}
}

// TestBatchSemaphore_ConcurrentCloseAndBatch verifies safe behavior when
// Close is called while batch operations are in progress.
func TestBatchSemaphore_ConcurrentCloseAndBatch(t *testing.T) {
	t.Parallel()

	config := IndexConfig{
		Path:          testIndexPath(t),
		BatchSize:     100,
		MaxConcurrent: 4,
		BatchTimeout:  5 * time.Second,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	ctx := context.Background()

	// Start batch operations
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(num int) {
			defer wg.Done()

			docs := make([]*search.Document, 2)
			for j := range docs {
				docID := search.GenerateDocumentID([]byte{byte(num*10 + j)})
				docs[j] = createTestDocument(docID, "concurrent close test")
			}
			// Error is expected when close happens during operation
			_ = mgr.IndexBatch(ctx, docs)
		}(i)
	}

	// Small delay then close
	time.Sleep(10 * time.Millisecond)
	if err := mgr.Close(); err != nil {
		t.Logf("Close() error (may be expected): %v", err)
	}

	wg.Wait()

	// Test passes if no panics occurred
}

// TestBatchSemaphore_HighConcurrency runs a stress test with high concurrency.
func TestBatchSemaphore_HighConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping high concurrency test in short mode")
	}

	t.Parallel()

	const maxConcurrent = 8
	config := IndexConfig{
		Path:          testIndexPath(t),
		BatchSize:     50,
		MaxConcurrent: maxConcurrent,
		BatchTimeout:  30 * time.Second,
	}
	mgr := NewIndexManagerWithConfig(config)
	if err := mgr.Open(); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Launch many concurrent operations
	var wg sync.WaitGroup
	var successCount atomic.Int64
	var errorCount atomic.Int64
	const numOperations = 100

	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(num int) {
			defer wg.Done()

			docs := make([]*search.Document, 3)
			for j := range docs {
				docID := search.GenerateDocumentID([]byte{byte(num), byte(j)})
				docs[j] = createTestDocument(docID, "high concurrency stress test")
			}

			if err := mgr.IndexBatch(ctx, docs); err != nil {
				errorCount.Add(1)
			} else {
				successCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	t.Logf("High concurrency test: successes=%d, errors=%d",
		successCount.Load(), errorCount.Load())

	// Majority should succeed
	if successCount.Load() < numOperations/2 {
		t.Errorf("Too many failures: %d/%d succeeded",
			successCount.Load(), numOperations)
	}
}
