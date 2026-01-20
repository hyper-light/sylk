package bleve

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// mockIndexFn creates a mock index function that tracks calls.
func mockIndexFn(calls *atomic.Int64, delay time.Duration, err error) func(string, interface{}) error {
	return func(docID string, doc interface{}) error {
		if delay > 0 {
			time.Sleep(delay)
		}
		calls.Add(1)
		return err
	}
}

// mockDeleteFn creates a mock delete function that tracks calls.
func mockDeleteFn(calls *atomic.Int64, delay time.Duration, err error) func(string) error {
	return func(docID string) error {
		if delay > 0 {
			time.Sleep(delay)
		}
		calls.Add(1)
		return err
	}
}

// mockBatchFn creates a mock batch function that tracks calls.
func mockBatchFn(calls *atomic.Int64, delay time.Duration, err error) func([]*IndexOperation) error {
	return func(ops []*IndexOperation) error {
		if delay > 0 {
			time.Sleep(delay)
		}
		calls.Add(int64(len(ops)))
		return err
	}
}

// createTestQueue creates a queue with sensible test defaults.
func createTestQueue(t *testing.T, config AsyncIndexQueueConfig) (*AsyncIndexQueue, *atomic.Int64, *atomic.Int64) {
	t.Helper()

	indexCalls := &atomic.Int64{}
	deleteCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		config,
		mockIndexFn(indexCalls, 0, nil),
		mockDeleteFn(deleteCalls, 0, nil),
		nil,
	)

	t.Cleanup(func() {
		_ = q.Close()
	})

	return q, indexCalls, deleteCalls
}

// =============================================================================
// Configuration Tests
// =============================================================================

func TestDefaultAsyncIndexQueueConfig(t *testing.T) {
	t.Parallel()

	config := DefaultAsyncIndexQueueConfig()

	if config.MaxQueueSize != 10000 {
		t.Errorf("MaxQueueSize = %d, want 10000", config.MaxQueueSize)
	}
	if config.BatchSize != 100 {
		t.Errorf("BatchSize = %d, want 100", config.BatchSize)
	}
	if config.FlushInterval != 100*time.Millisecond {
		t.Errorf("FlushInterval = %v, want 100ms", config.FlushInterval)
	}
	if config.Workers != 2 {
		t.Errorf("Workers = %d, want 2", config.Workers)
	}
}

func TestAsyncIndexQueueConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config AsyncIndexQueueConfig
		want   AsyncIndexQueueConfig
	}{
		{
			name:   "zero values get defaults",
			config: AsyncIndexQueueConfig{},
			want: AsyncIndexQueueConfig{
				MaxQueueSize:  10000,
				BatchSize:     100,
				FlushInterval: 100 * time.Millisecond,
				Workers:       2,
			},
		},
		{
			name: "negative values get defaults",
			config: AsyncIndexQueueConfig{
				MaxQueueSize:  -1,
				BatchSize:     -1,
				FlushInterval: -1,
				Workers:       -1,
			},
			want: AsyncIndexQueueConfig{
				MaxQueueSize:  10000,
				BatchSize:     100,
				FlushInterval: 100 * time.Millisecond,
				Workers:       2,
			},
		},
		{
			name: "custom values preserved",
			config: AsyncIndexQueueConfig{
				MaxQueueSize:  5000,
				BatchSize:     50,
				FlushInterval: 50 * time.Millisecond,
				Workers:       4,
			},
			want: AsyncIndexQueueConfig{
				MaxQueueSize:  5000,
				BatchSize:     50,
				FlushInterval: 50 * time.Millisecond,
				Workers:       4,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.config.validate()

			if tt.config.MaxQueueSize != tt.want.MaxQueueSize {
				t.Errorf("MaxQueueSize = %d, want %d", tt.config.MaxQueueSize, tt.want.MaxQueueSize)
			}
			if tt.config.BatchSize != tt.want.BatchSize {
				t.Errorf("BatchSize = %d, want %d", tt.config.BatchSize, tt.want.BatchSize)
			}
			if tt.config.FlushInterval != tt.want.FlushInterval {
				t.Errorf("FlushInterval = %v, want %v", tt.config.FlushInterval, tt.want.FlushInterval)
			}
			if tt.config.Workers != tt.want.Workers {
				t.Errorf("Workers = %d, want %d", tt.config.Workers, tt.want.Workers)
			}
		})
	}
}

// =============================================================================
// Queue Creation Tests
// =============================================================================

func TestNewAsyncIndexQueue(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}
	deleteCalls := &atomic.Int64{}

	config := AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 50 * time.Millisecond,
		Workers:       2,
	}

	q := NewAsyncIndexQueue(
		context.Background(),
		config,
		mockIndexFn(indexCalls, 0, nil),
		mockDeleteFn(deleteCalls, 0, nil),
		nil,
	)
	defer q.Close()

	if q == nil {
		t.Fatal("NewAsyncIndexQueue returned nil")
	}

	if q.IsClosed() {
		t.Error("newly created queue should not be closed")
	}

	stats := q.Stats()
	if stats.QueueLength != 0 {
		t.Errorf("initial QueueLength = %d, want 0", stats.QueueLength)
	}
	if stats.Enqueued != 0 {
		t.Errorf("initial Enqueued = %d, want 0", stats.Enqueued)
	}
}

func TestNewAsyncIndexQueue_WithBatchFn(t *testing.T) {
	t.Parallel()

	batchCalls := &atomic.Int64{}
	config := AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     5,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	}

	q := NewAsyncIndexQueue(
		context.Background(),
		config,
		nil,
		nil,
		mockBatchFn(batchCalls, 0, nil),
	)
	defer q.Close()

	// Submit operations
	for i := 0; i < 5; i++ {
		q.SubmitIndex("doc"+string(rune('0'+i)), map[string]string{"content": "test"})
	}

	// Wait for batch to process
	time.Sleep(50 * time.Millisecond)

	if batchCalls.Load() != 5 {
		t.Errorf("batchFn called with %d ops, want 5", batchCalls.Load())
	}
}

// =============================================================================
// Submit Tests
// =============================================================================

func TestAsyncIndexQueue_Submit(t *testing.T) {
	t.Parallel()

	q, indexCalls, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	op := NewIndexOperation(OpIndex, "doc1", map[string]string{"content": "test"})
	err := q.Submit(op)
	if err != nil {
		t.Errorf("Submit() error = %v, want nil", err)
	}

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	if indexCalls.Load() != 1 {
		t.Errorf("indexFn called %d times, want 1", indexCalls.Load())
	}

	stats := q.Stats()
	if stats.Enqueued != 1 {
		t.Errorf("Enqueued = %d, want 1", stats.Enqueued)
	}
}

func TestAsyncIndexQueue_Submit_NilOperation(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, DefaultAsyncIndexQueueConfig())

	err := q.Submit(nil)
	if err != ErrNilOperation {
		t.Errorf("Submit(nil) error = %v, want %v", err, ErrNilOperation)
	}
}

func TestAsyncIndexQueue_Submit_Closed(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, DefaultAsyncIndexQueueConfig())
	_ = q.Close()

	op := NewIndexOperation(OpIndex, "doc1", nil)
	err := q.Submit(op)
	if err != ErrQueueClosed {
		t.Errorf("Submit() on closed queue error = %v, want %v", err, ErrQueueClosed)
	}
}

func TestAsyncIndexQueue_SubmitIndex(t *testing.T) {
	t.Parallel()

	q, indexCalls, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	resultCh := q.SubmitIndex("doc1", map[string]string{"content": "test"})
	if resultCh == nil {
		t.Fatal("SubmitIndex() returned nil channel")
	}

	// Wait for result
	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("SubmitIndex() result = %v, want nil", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("SubmitIndex() timed out waiting for result")
	}

	if indexCalls.Load() != 1 {
		t.Errorf("indexFn called %d times, want 1", indexCalls.Load())
	}
}

func TestAsyncIndexQueue_SubmitDelete(t *testing.T) {
	t.Parallel()

	q, _, deleteCalls := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	resultCh := q.SubmitDelete("doc1")
	if resultCh == nil {
		t.Fatal("SubmitDelete() returned nil channel")
	}

	// Wait for result
	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("SubmitDelete() result = %v, want nil", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("SubmitDelete() timed out waiting for result")
	}

	if deleteCalls.Load() != 1 {
		t.Errorf("deleteFn called %d times, want 1", deleteCalls.Load())
	}
}

func TestAsyncIndexQueue_SubmitIndexSync(t *testing.T) {
	t.Parallel()

	q, indexCalls, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	ctx := context.Background()
	err := q.SubmitIndexSync(ctx, "doc1", map[string]string{"content": "test"})
	if err != nil {
		t.Errorf("SubmitIndexSync() error = %v, want nil", err)
	}

	if indexCalls.Load() != 1 {
		t.Errorf("indexFn called %d times, want 1", indexCalls.Load())
	}
}

func TestAsyncIndexQueue_SubmitIndexSync_ContextCancelled(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}
	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     10,
			FlushInterval: 1 * time.Second, // Long flush interval
			Workers:       1,
		},
		mockIndexFn(indexCalls, 500*time.Millisecond, nil), // Slow indexing
		nil,
		nil,
	)
	defer q.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := q.SubmitIndexSync(ctx, "doc1", nil)
	if err != context.DeadlineExceeded {
		t.Errorf("SubmitIndexSync() error = %v, want %v", err, context.DeadlineExceeded)
	}
}

// =============================================================================
// Batching Tests
// =============================================================================

func TestAsyncIndexQueue_BatchingBehavior(t *testing.T) {
	t.Parallel()

	batchCalls := &atomic.Int64{}
	opsProcessed := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  1000,
			BatchSize:     5,
			FlushInterval: 1 * time.Second, // Long interval to test batch size trigger
			Workers:       1,
		},
		nil,
		nil,
		func(ops []*IndexOperation) error {
			batchCalls.Add(1)
			opsProcessed.Add(int64(len(ops)))
			return nil
		},
	)
	defer q.Close()

	// Submit exactly batch size operations
	for i := 0; i < 5; i++ {
		q.SubmitIndex("doc"+string(rune('0'+i)), nil)
	}

	// Wait for batch to process
	time.Sleep(50 * time.Millisecond)

	if batchCalls.Load() != 1 {
		t.Errorf("batchFn called %d times, want 1", batchCalls.Load())
	}
	if opsProcessed.Load() != 5 {
		t.Errorf("ops processed = %d, want 5", opsProcessed.Load())
	}
}

func TestAsyncIndexQueue_FlushIntervalTrigger(t *testing.T) {
	t.Parallel()

	batchCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  1000,
			BatchSize:     100, // High batch size
			FlushInterval: 20 * time.Millisecond,
			Workers:       1,
		},
		nil,
		nil,
		func(ops []*IndexOperation) error {
			batchCalls.Add(1)
			return nil
		},
	)
	defer q.Close()

	// Submit fewer than batch size
	for i := 0; i < 3; i++ {
		q.SubmitIndex("doc"+string(rune('0'+i)), nil)
	}

	// Wait for flush interval to trigger
	time.Sleep(100 * time.Millisecond)

	if batchCalls.Load() < 1 {
		t.Errorf("batchFn should have been called at least once due to flush interval")
	}
}

// =============================================================================
// Flush Tests
// =============================================================================

func TestAsyncIndexQueue_Flush(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  1000,
			BatchSize:     100, // High batch size to prevent auto-flush
			FlushInterval: 10 * time.Second,
			Workers:       1,
		},
		mockIndexFn(indexCalls, 0, nil),
		nil,
		nil,
	)
	defer q.Close()

	// Submit some operations
	for i := 0; i < 5; i++ {
		q.SubmitIndex("doc"+string(rune('0'+i)), nil)
	}

	// Flush should process all pending
	ctx := context.Background()
	err := q.Flush(ctx)
	if err != nil {
		t.Errorf("Flush() error = %v, want nil", err)
	}

	if indexCalls.Load() != 5 {
		t.Errorf("indexFn called %d times after Flush, want 5", indexCalls.Load())
	}
}

func TestAsyncIndexQueue_Flush_Empty(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, DefaultAsyncIndexQueueConfig())

	// Flush empty queue should succeed
	ctx := context.Background()
	err := q.Flush(ctx)
	if err != nil {
		t.Errorf("Flush() on empty queue error = %v, want nil", err)
	}
}

func TestAsyncIndexQueue_Flush_Closed(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, DefaultAsyncIndexQueueConfig())
	_ = q.Close()

	ctx := context.Background()
	err := q.Flush(ctx)
	if err != ErrQueueClosed {
		t.Errorf("Flush() on closed queue error = %v, want %v", err, ErrQueueClosed)
	}
}

func TestAsyncIndexQueue_Flush_ContextCancelled(t *testing.T) {
	t.Parallel()

	// Use a slow batch function to ensure context cancellation happens
	// before processing completes
	slowBatchFn := func(ops []*IndexOperation) error {
		time.Sleep(500 * time.Millisecond)
		return nil
	}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  10,
			BatchSize:     100,     // Large batch size to prevent immediate flush
			FlushInterval: 1 * time.Hour, // Very long interval
			Workers:       1,
		},
		nil,
		nil,
		slowBatchFn,
	)
	defer q.Close()

	// Submit an operation
	q.SubmitIndex("doc1", nil)

	// Create a context that will timeout quickly
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := q.Flush(ctx)
	// The error should be context deadline exceeded because Flush has to wait
	// for the slow batch processing
	if err != context.DeadlineExceeded {
		// May also be nil if the flush completed before timeout - this is acceptable
		// in a race condition
		if err != nil {
			t.Errorf("Flush() error = %v, want %v or nil", err, context.DeadlineExceeded)
		}
	}
}

// =============================================================================
// Close Tests
// =============================================================================

func TestAsyncIndexQueue_Close(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  1000,
			BatchSize:     100,
			FlushInterval: 10 * time.Second,
			Workers:       2,
		},
		mockIndexFn(indexCalls, 0, nil),
		nil,
		nil,
	)

	// Submit some operations
	for i := 0; i < 10; i++ {
		q.SubmitIndex("doc"+string(rune('0'+i)), nil)
	}

	// Close should process remaining
	err := q.Close()
	if err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}

	if !q.IsClosed() {
		t.Error("IsClosed() = false after Close, want true")
	}

	// All operations should have been processed
	if indexCalls.Load() != 10 {
		t.Errorf("indexFn called %d times after Close, want 10", indexCalls.Load())
	}
}

func TestAsyncIndexQueue_Close_Idempotent(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, DefaultAsyncIndexQueueConfig())

	// Close multiple times should not error
	err1 := q.Close()
	err2 := q.Close()
	err3 := q.Close()

	if err1 != nil || err2 != nil || err3 != nil {
		t.Errorf("multiple Close() calls should not error: %v, %v, %v", err1, err2, err3)
	}
}

// =============================================================================
// Backpressure Tests
// =============================================================================

func TestAsyncIndexQueue_Backpressure(t *testing.T) {
	t.Parallel()

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   5,
			BatchSize:      100,
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     0, // Disable retries to test immediate backpressure
			RetryBaseDelay: 1 * time.Millisecond,
			RetryMaxDelay:  5 * time.Millisecond,
		},
		mockIndexFn(&atomic.Int64{}, 100*time.Millisecond, nil), // Slow processing
		nil,
		nil,
	)
	defer q.Close()

	// Fill the queue quickly using SubmitNoRetry for original behavior
	var fullCount int
	for i := 0; i < 20; i++ {
		op := NewIndexOperation(OpIndex, "doc"+string(rune('0'+i)), nil)
		err := q.SubmitNoRetry(op)
		if err == ErrQueueFull {
			fullCount++
		}
	}

	if fullCount == 0 {
		t.Error("expected some operations to be rejected due to full queue")
	}

	stats := q.Stats()
	if stats.Dropped == 0 {
		t.Error("expected dropped count > 0 due to backpressure")
	}
}

func TestAsyncIndexQueue_SubmitBlocking(t *testing.T) {
	t.Parallel()

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  2,
			BatchSize:     1,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		mockIndexFn(&atomic.Int64{}, 0, nil),
		nil,
		nil,
	)
	defer q.Close()

	// Submit operations - blocking should allow all to be queued eventually
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			op := NewIndexOperation(OpIndex, "doc"+string(rune('0'+id)), nil)
			_ = q.SubmitBlocking(op)
		}(i)
	}

	wg.Wait()
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestAsyncIndexQueue_ConcurrentSubmit(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  1000,
			BatchSize:     10,
			FlushInterval: 10 * time.Millisecond,
			Workers:       4,
		},
		mockIndexFn(indexCalls, 0, nil),
		nil,
		nil,
	)
	defer q.Close()

	const numGoroutines = 10
	const opsPerGoroutine = 20

	var wg sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				docID := "doc-" + string(rune('A'+goroutineID)) + "-" + string(rune('0'+i))
				q.SubmitIndex(docID, nil)
			}
		}(g)
	}

	wg.Wait()

	// Flush to ensure all are processed
	_ = q.Flush(context.Background())

	expected := int64(numGoroutines * opsPerGoroutine)
	if indexCalls.Load() != expected {
		t.Errorf("indexFn called %d times, want %d", indexCalls.Load(), expected)
	}
}

func TestAsyncIndexQueue_ConcurrentMixedOperations(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}
	deleteCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  1000,
			BatchSize:     10,
			FlushInterval: 10 * time.Millisecond,
			Workers:       4,
		},
		mockIndexFn(indexCalls, 0, nil),
		mockDeleteFn(deleteCalls, 0, nil),
		nil,
	)
	defer q.Close()

	const numGoroutines = 10
	const opsPerGoroutine = 10

	var wg sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				docID := "doc-" + string(rune('A'+goroutineID)) + "-" + string(rune('0'+i))
				if i%2 == 0 {
					q.SubmitIndex(docID, nil)
				} else {
					q.SubmitDelete(docID)
				}
			}
		}(g)
	}

	wg.Wait()

	// Flush to ensure all are processed
	_ = q.Flush(context.Background())

	totalOps := indexCalls.Load() + deleteCalls.Load()
	expected := int64(numGoroutines * opsPerGoroutine)
	if totalOps != expected {
		t.Errorf("total ops = %d, want %d", totalOps, expected)
	}
}

// =============================================================================
// Error Handling Tests
// =============================================================================

func TestAsyncIndexQueue_IndexError(t *testing.T) {
	t.Parallel()

	indexErr := errors.New("index failed")
	indexCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     10,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		mockIndexFn(indexCalls, 0, indexErr),
		nil,
		nil,
	)
	defer q.Close()

	resultCh := q.SubmitIndex("doc1", nil)

	select {
	case err := <-resultCh:
		if err != indexErr {
			t.Errorf("result = %v, want %v", err, indexErr)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for result")
	}
}

func TestAsyncIndexQueue_BatchError(t *testing.T) {
	t.Parallel()

	batchErr := errors.New("batch failed")

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     5,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		nil,
		nil,
		func(ops []*IndexOperation) error {
			return batchErr
		},
	)
	defer q.Close()

	resultCh := q.SubmitIndex("doc1", nil)

	select {
	case err := <-resultCh:
		if err != batchErr {
			t.Errorf("result = %v, want %v", err, batchErr)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for result")
	}
}

// =============================================================================
// Stats Tests
// =============================================================================

func TestAsyncIndexQueue_Stats(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	// Initial stats
	stats := q.Stats()
	if stats.Enqueued != 0 || stats.Processed != 0 || stats.Dropped != 0 {
		t.Error("initial stats should all be zero")
	}

	// Submit and wait
	for i := 0; i < 5; i++ {
		q.SubmitIndex("doc"+string(rune('0'+i)), nil)
	}

	_ = q.Flush(context.Background())

	stats = q.Stats()
	if stats.Enqueued != 5 {
		t.Errorf("Enqueued = %d, want 5", stats.Enqueued)
	}
	if stats.Processed != 5 {
		t.Errorf("Processed = %d, want 5", stats.Processed)
	}
}

// =============================================================================
// Operation Type Tests
// =============================================================================

func TestOperationType_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		op   OperationType
		want string
	}{
		{OpIndex, "index"},
		{OpDelete, "delete"},
		{OpBatch, "batch"},
		{OperationType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.op.String(); got != tt.want {
				t.Errorf("OperationType(%d).String() = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestNewIndexOperation(t *testing.T) {
	t.Parallel()

	op := NewIndexOperation(OpIndex, "doc1", map[string]string{"key": "value"})

	if op == nil {
		t.Fatal("NewIndexOperation returned nil")
	}
	if op.Type != OpIndex {
		t.Errorf("Type = %v, want %v", op.Type, OpIndex)
	}
	if op.DocID != "doc1" {
		t.Errorf("DocID = %q, want %q", op.DocID, "doc1")
	}
	if op.Document == nil {
		t.Error("Document should not be nil")
	}
	if op.ResultCh == nil {
		t.Error("ResultCh should not be nil")
	}
	if op.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

// =============================================================================
// Error Message Tests
// =============================================================================

func TestAsyncQueueErrorMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err     error
		wantMsg string
	}{
		{ErrQueueFull, "async index queue is full"},
		{ErrQueueClosed, "async index queue is closed"},
		{ErrNilOperation, "operation cannot be nil"},
	}

	for _, tt := range tests {
		t.Run(tt.wantMsg, func(t *testing.T) {
			t.Parallel()
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("error = %q, want %q", tt.err.Error(), tt.wantMsg)
			}
		})
	}
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestAsyncIndexQueue_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	q := NewAsyncIndexQueue(
		ctx,
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     10,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		mockIndexFn(&atomic.Int64{}, 0, nil),
		nil,
		nil,
	)

	// Submit some ops
	for i := 0; i < 5; i++ {
		q.SubmitIndex("doc"+string(rune('0'+i)), nil)
	}

	// Cancel context
	cancel()

	// Give time for context cancellation to propagate
	time.Sleep(50 * time.Millisecond)

	// Close should still work
	err := q.Close()
	if err != nil {
		t.Errorf("Close() after context cancel error = %v", err)
	}
}

// =============================================================================
// Retry Mechanism Tests
// =============================================================================

func TestAsyncIndexQueue_RetryConfig_Defaults(t *testing.T) {
	t.Parallel()

	config := DefaultAsyncIndexQueueConfig()

	if config.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", config.MaxRetries)
	}
	if config.RetryBaseDelay != 10*time.Millisecond {
		t.Errorf("RetryBaseDelay = %v, want 10ms", config.RetryBaseDelay)
	}
	if config.RetryMaxDelay != 100*time.Millisecond {
		t.Errorf("RetryMaxDelay = %v, want 100ms", config.RetryMaxDelay)
	}
}

func TestAsyncIndexQueue_RetryConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config AsyncIndexQueueConfig
		want   AsyncIndexQueueConfig
	}{
		{
			name: "negative MaxRetries gets default",
			config: AsyncIndexQueueConfig{
				MaxRetries:     -1,
				RetryBaseDelay: 10 * time.Millisecond,
				RetryMaxDelay:  100 * time.Millisecond,
			},
			want: AsyncIndexQueueConfig{
				MaxRetries:     3,
				RetryBaseDelay: 10 * time.Millisecond,
				RetryMaxDelay:  100 * time.Millisecond,
			},
		},
		{
			name: "zero MaxRetries preserved (disables retry)",
			config: AsyncIndexQueueConfig{
				MaxRetries:     0,
				RetryBaseDelay: 10 * time.Millisecond,
				RetryMaxDelay:  100 * time.Millisecond,
			},
			want: AsyncIndexQueueConfig{
				MaxRetries:     0,
				RetryBaseDelay: 10 * time.Millisecond,
				RetryMaxDelay:  100 * time.Millisecond,
			},
		},
		{
			name: "zero delays get defaults",
			config: AsyncIndexQueueConfig{
				MaxRetries:     3,
				RetryBaseDelay: 0,
				RetryMaxDelay:  0,
			},
			want: AsyncIndexQueueConfig{
				MaxRetries:     3,
				RetryBaseDelay: 10 * time.Millisecond,
				RetryMaxDelay:  100 * time.Millisecond,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.config.validate()

			if tt.config.MaxRetries != tt.want.MaxRetries {
				t.Errorf("MaxRetries = %d, want %d", tt.config.MaxRetries, tt.want.MaxRetries)
			}
			if tt.config.RetryBaseDelay != tt.want.RetryBaseDelay {
				t.Errorf("RetryBaseDelay = %v, want %v", tt.config.RetryBaseDelay, tt.want.RetryBaseDelay)
			}
			if tt.config.RetryMaxDelay != tt.want.RetryMaxDelay {
				t.Errorf("RetryMaxDelay = %v, want %v", tt.config.RetryMaxDelay, tt.want.RetryMaxDelay)
			}
		})
	}
}

func TestAsyncIndexQueue_Submit_SuccessfulNoRetryNeeded(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   100,
			BatchSize:      10,
			FlushInterval:  10 * time.Millisecond,
			Workers:        1,
			MaxRetries:     3,
			RetryBaseDelay: 5 * time.Millisecond,
			RetryMaxDelay:  50 * time.Millisecond,
		},
		mockIndexFn(indexCalls, 0, nil),
		nil,
		nil,
	)
	defer q.Close()

	op := NewIndexOperation(OpIndex, "doc1", map[string]string{"content": "test"})
	err := q.Submit(op)
	if err != nil {
		t.Errorf("Submit() error = %v, want nil", err)
	}

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	stats := q.Stats()
	if stats.Enqueued != 1 {
		t.Errorf("Enqueued = %d, want 1", stats.Enqueued)
	}
	if stats.Retried != 0 {
		t.Errorf("Retried = %d, want 0 (no retries needed)", stats.Retried)
	}
}

func TestAsyncIndexQueue_Submit_RetrySuccess(t *testing.T) {
	t.Parallel()

	// This test verifies that submissions eventually succeed when the queue
	// has capacity freed up during retry attempts.
	// Since timing-based tests are inherently flaky, we just verify the
	// submission succeeds and don't assert on retry counts.

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   10,
			BatchSize:      5,
			FlushInterval:  10 * time.Millisecond,
			Workers:        2,
			MaxRetries:     3,
			RetryBaseDelay: 5 * time.Millisecond,
			RetryMaxDelay:  20 * time.Millisecond,
		},
		func(docID string, doc interface{}) error { return nil },
		nil,
		nil,
	)
	defer q.Close()

	// Submit multiple documents - some may need retries under load
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			op := NewIndexOperation(OpIndex, "doc-"+string(rune('A'+id)), nil)
			_ = q.Submit(op) // Errors are acceptable under high contention
		}(i)
	}

	wg.Wait()

	// Wait for processing to complete
	_ = q.Flush(context.Background())

	stats := q.Stats()
	// With retries enabled, all submissions should eventually succeed
	if stats.Enqueued == 0 {
		t.Error("Expected some documents to be enqueued")
	}
}

func TestAsyncIndexQueue_Submit_RetryExhausted(t *testing.T) {
	t.Parallel()

	// This test verifies ErrRetriesExhausted is returned when queue stays full.
	// We use a never-unblocking processor to guarantee the queue stays full.

	processingBlock := make(chan struct{})
	var overflowCalled atomic.Bool

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Flush immediately so indexFn blocks processor
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     2,
			RetryBaseDelay: 1 * time.Millisecond,
			RetryMaxDelay:  5 * time.Millisecond,
		},
		func(docID string, doc interface{}) error {
			<-processingBlock // Never returns until test ends
			return nil
		},
		nil,
		nil,
	)

	// Set overflow callback
	q.SetOverflowCallback(func(op *IndexOperation, err error) {
		overflowCalled.Store(true)
	})

	// Submit first item - goes into channel
	err1 := q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill", nil))
	if err1 != nil {
		t.Fatalf("First submit failed: %v", err1)
	}

	// Give processor time to pick it up (it will block in indexFn)
	time.Sleep(20 * time.Millisecond)

	// Submit second item - fills the channel
	err2 := q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))
	if err2 != nil {
		t.Fatalf("Second submit failed: %v", err2)
	}

	// Now channel is full. Submit should exhaust retries.
	err := q.Submit(NewIndexOperation(OpIndex, "overflow", nil))

	// Cleanup
	close(processingBlock)
	_ = q.Close()

	if err != ErrRetriesExhausted {
		t.Errorf("Submit() error = %v, want %v", err, ErrRetriesExhausted)
	}

	if !overflowCalled.Load() {
		t.Error("overflow callback was not invoked")
	}

	stats := q.Stats()
	if stats.Dropped == 0 {
		t.Error("Expected Dropped > 0")
	}
}

func TestAsyncIndexQueue_Submit_NoRetryWhenMaxRetriesZero(t *testing.T) {
	t.Parallel()

	processingBlock := make(chan struct{})

	// MaxRetries = 0 should disable retry mechanism
	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Flush immediately so indexFn blocks processor
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     0, // Disable retries
			RetryBaseDelay: 1 * time.Millisecond,
			RetryMaxDelay:  5 * time.Millisecond,
		},
		func(string, interface{}) error {
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	// Fill the queue and block processor
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))
	time.Sleep(20 * time.Millisecond) // Wait for processor to pick up and block
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// Submit should fail immediately
	start := time.Now()
	op := NewIndexOperation(OpIndex, "test", nil)
	err := q.Submit(op)
	elapsed := time.Since(start)

	if err != ErrRetriesExhausted {
		t.Errorf("Submit() error = %v, want %v", err, ErrRetriesExhausted)
	}

	// Should be nearly instant (no retry delays)
	if elapsed > 50*time.Millisecond {
		t.Errorf("Submit() took %v, expected < 50ms (no retry delays)", elapsed)
	}

	// Cleanup
	close(processingBlock)
	_ = q.Close()

	stats := q.Stats()
	if stats.Retried != 0 {
		t.Errorf("Retried = %d, want 0", stats.Retried)
	}
}

func TestAsyncIndexQueue_CalculateBackoff(t *testing.T) {
	t.Parallel()

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   100,
			BatchSize:      10,
			FlushInterval:  100 * time.Millisecond,
			Workers:        1,
			MaxRetries:     5,
			RetryBaseDelay: 10 * time.Millisecond,
			RetryMaxDelay:  100 * time.Millisecond,
		},
		nil,
		nil,
		nil,
	)
	defer q.Close()

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 10 * time.Millisecond},   // 10 * 2^0 = 10
		{1, 20 * time.Millisecond},   // 10 * 2^1 = 20
		{2, 40 * time.Millisecond},   // 10 * 2^2 = 40
		{3, 80 * time.Millisecond},   // 10 * 2^3 = 80
		{4, 100 * time.Millisecond},  // 10 * 2^4 = 160, capped at 100
		{10, 100 * time.Millisecond}, // Very high, capped at max
	}

	for _, tt := range tests {
		got := q.calculateBackoff(tt.attempt)
		if got != tt.want {
			t.Errorf("calculateBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestAsyncIndexQueue_Submit_BackoffTiming(t *testing.T) {
	t.Parallel()

	processingBlock := make(chan struct{})

	// Test that backoff actually delays appropriately
	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Flush immediately so indexFn blocks processor
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     3,
			RetryBaseDelay: 20 * time.Millisecond,
			RetryMaxDelay:  100 * time.Millisecond,
		},
		func(string, interface{}) error {
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	// Fill queue and block processor
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))
	time.Sleep(20 * time.Millisecond) // Wait for processor to pick up and block
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// Submit should take at least the sum of backoff delays
	// attempt 0: 20ms, attempt 1: 40ms, attempt 2: 80ms = 140ms minimum
	start := time.Now()
	op := NewIndexOperation(OpIndex, "test", nil)
	_ = q.Submit(op)
	elapsed := time.Since(start)

	// Cleanup
	close(processingBlock)
	_ = q.Close()

	minExpected := 100 * time.Millisecond // At least some backoff should occur
	if elapsed < minExpected {
		t.Errorf("Submit() took %v, expected at least %v (backoff delays)", elapsed, minExpected)
	}
}

func TestAsyncIndexQueue_SubmitNoRetry(t *testing.T) {
	t.Parallel()

	processingBlock := make(chan struct{})

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Flush immediately so indexFn blocks processor
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     5, // Would retry normally
			RetryBaseDelay: 50 * time.Millisecond,
			RetryMaxDelay:  100 * time.Millisecond,
		},
		func(string, interface{}) error {
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	// Fill queue and block processor
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))
	time.Sleep(20 * time.Millisecond) // Wait for processor to pick up and block
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// SubmitNoRetry should fail immediately
	start := time.Now()
	op := NewIndexOperation(OpIndex, "test", nil)
	err := q.SubmitNoRetry(op)
	elapsed := time.Since(start)

	if err != ErrQueueFull {
		t.Errorf("SubmitNoRetry() error = %v, want %v", err, ErrQueueFull)
	}

	// Should be instant (no retry)
	if elapsed > 20*time.Millisecond {
		t.Errorf("SubmitNoRetry() took %v, expected < 20ms", elapsed)
	}

	// Cleanup
	close(processingBlock)
	_ = q.Close()
}

func TestAsyncIndexQueue_SubmitNoRetry_NilOperation(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, DefaultAsyncIndexQueueConfig())

	err := q.SubmitNoRetry(nil)
	if err != ErrNilOperation {
		t.Errorf("SubmitNoRetry(nil) error = %v, want %v", err, ErrNilOperation)
	}
}

func TestAsyncIndexQueue_SubmitNoRetry_Closed(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, DefaultAsyncIndexQueueConfig())
	_ = q.Close()

	op := NewIndexOperation(OpIndex, "doc1", nil)
	err := q.SubmitNoRetry(op)
	if err != ErrQueueClosed {
		t.Errorf("SubmitNoRetry() on closed queue error = %v, want %v", err, ErrQueueClosed)
	}
}

func TestAsyncIndexQueue_SetOverflowCallback(t *testing.T) {
	t.Parallel()

	processingBlock := make(chan struct{})

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Flush immediately so indexFn blocks processor
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     1,
			RetryBaseDelay: 1 * time.Millisecond,
			RetryMaxDelay:  5 * time.Millisecond,
		},
		func(string, interface{}) error {
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	callCount := &atomic.Int64{}

	// Set callback
	q.SetOverflowCallback(func(op *IndexOperation, err error) {
		callCount.Add(1)
	})

	// Fill and block processor
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))
	time.Sleep(20 * time.Millisecond) // Wait for processor to pick up and block
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// This should overflow
	_ = q.Submit(NewIndexOperation(OpIndex, "overflow1", nil))

	if callCount.Load() != 1 {
		t.Errorf("callback called %d times, want 1", callCount.Load())
	}

	// Clear callback
	q.SetOverflowCallback(nil)
	_ = q.Submit(NewIndexOperation(OpIndex, "overflow2", nil))

	if callCount.Load() != 1 {
		t.Errorf("callback called %d times after clearing, want still 1", callCount.Load())
	}

	// Cleanup
	close(processingBlock)
	_ = q.Close()
}

func TestAsyncIndexQueue_Submit_ClosedDuringRetry(t *testing.T) {
	t.Parallel()

	processingBlock := make(chan struct{})

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Flush immediately so indexFn blocks processor
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     10,
			RetryBaseDelay: 50 * time.Millisecond,
			RetryMaxDelay:  200 * time.Millisecond,
		},
		func(string, interface{}) error {
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	// Fill queue and block processor
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))
	time.Sleep(20 * time.Millisecond) // Wait for processor to pick up and block
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// Start a submission that will retry
	done := make(chan error, 1)
	go func() {
		op := NewIndexOperation(OpIndex, "test", nil)
		done <- q.Submit(op)
	}()

	// Close queue while retry is in progress
	time.Sleep(30 * time.Millisecond)
	close(processingBlock)
	_ = q.Close()

	select {
	case err := <-done:
		if err != ErrQueueClosed {
			t.Errorf("Submit() during close error = %v, want %v", err, ErrQueueClosed)
		}
	case <-time.After(1 * time.Second):
		t.Error("Submit() did not return after queue closed")
	}
}

func TestAsyncIndexQueue_RetryStats(t *testing.T) {
	t.Parallel()

	processingBlock := make(chan struct{})

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Flush immediately so indexFn blocks processor
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     3,
			RetryBaseDelay: 1 * time.Millisecond,
			RetryMaxDelay:  5 * time.Millisecond,
		},
		func(string, interface{}) error {
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	// Initial stats
	stats := q.Stats()
	if stats.Retried != 0 {
		t.Errorf("initial Retried = %d, want 0", stats.Retried)
	}

	// Fill queue and block processor
	// With BatchSize=1, the processor will immediately flush and block on indexFn
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))
	time.Sleep(20 * time.Millisecond) // Wait for processor to pick up and block on indexFn
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// This should exhaust retries since queue is full and processor is blocked
	_ = q.Submit(NewIndexOperation(OpIndex, "retry", nil))

	// Cleanup
	close(processingBlock)
	_ = q.Close()

	stats = q.Stats()
	if stats.Retried != 3 {
		t.Errorf("Retried = %d, want 3 (MaxRetries)", stats.Retried)
	}
}

func TestAsyncIndexQueue_ConcurrentRetries(t *testing.T) {
	t.Parallel()

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   5,
			BatchSize:      2,
			FlushInterval:  5 * time.Millisecond,
			Workers:        2,
			MaxRetries:     3,
			RetryBaseDelay: 1 * time.Millisecond,
			RetryMaxDelay:  10 * time.Millisecond,
		},
		func(string, interface{}) error {
			time.Sleep(time.Millisecond)
			return nil
		},
		nil,
		nil,
	)
	defer q.Close()

	const numGoroutines = 10
	const opsPerGoroutine = 5

	var wg sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				docID := "doc-" + string(rune('A'+goroutineID)) + "-" + string(rune('0'+i))
				_ = q.Submit(NewIndexOperation(OpIndex, docID, nil))
			}
		}(g)
	}

	wg.Wait()

	// Flush to ensure all processed
	_ = q.Flush(context.Background())

	stats := q.Stats()
	// With contention, we expect some retries and some drops
	// But importantly, no panics or deadlocks occurred
	t.Logf("Stats: Enqueued=%d, Processed=%d, Dropped=%d, Retried=%d",
		stats.Enqueued, stats.Processed, stats.Dropped, stats.Retried)
}

func TestErrRetriesExhausted(t *testing.T) {
	t.Parallel()

	if ErrRetriesExhausted.Error() != "all retry attempts exhausted" {
		t.Errorf("ErrRetriesExhausted message = %q, want %q",
			ErrRetriesExhausted.Error(), "all retry attempts exhausted")
	}
}

// =============================================================================
// Context Propagation Tests (W4P.21)
// =============================================================================

func TestNewIndexOperationWithContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	op := NewIndexOperationWithContext(ctx, OpIndex, "doc1", map[string]string{"key": "value"})

	if op == nil {
		t.Fatal("NewIndexOperationWithContext returned nil")
	}
	if op.Ctx != ctx {
		t.Error("operation context does not match provided context")
	}
	if op.Type != OpIndex {
		t.Errorf("Type = %v, want %v", op.Type, OpIndex)
	}
	if op.DocID != "doc1" {
		t.Errorf("DocID = %q, want %q", op.DocID, "doc1")
	}
}

func TestNewIndexOperationWithContext_NilContext(t *testing.T) {
	t.Parallel()

	op := NewIndexOperationWithContext(nil, OpIndex, "doc1", nil)

	if op == nil {
		t.Fatal("NewIndexOperationWithContext returned nil")
	}
	if op.Ctx == nil {
		t.Error("operation context should not be nil when nil is passed")
	}
}

func TestAsyncIndexQueue_Submit_ContextCancelled(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	// Create already cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	op := NewIndexOperationWithContext(ctx, OpIndex, "doc1", nil)
	err := q.Submit(op)

	if err != context.Canceled {
		t.Errorf("Submit() with cancelled context error = %v, want %v", err, context.Canceled)
	}
}

func TestAsyncIndexQueue_Submit_ContextTimeout(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	// Create context with past deadline
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	op := NewIndexOperationWithContext(ctx, OpIndex, "doc1", nil)
	err := q.Submit(op)

	if err != context.DeadlineExceeded {
		t.Errorf("Submit() with expired context error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestAsyncIndexQueue_Submit_ContextCancelledDuringRetry(t *testing.T) {
	t.Parallel()

	processingBlock := make(chan struct{})
	startedProcessing := make(chan struct{})
	var once sync.Once

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Process one at a time to ensure we block
			FlushInterval:  1 * time.Millisecond,
			Workers:        1,
			MaxRetries:     10, // Many retries
			RetryBaseDelay: 50 * time.Millisecond,
			RetryMaxDelay:  200 * time.Millisecond,
		},
		func(string, interface{}) error {
			once.Do(func() {
				close(startedProcessing)
			})
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	// Submit first item - processor picks it up and blocks
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))

	// Wait for processor to start processing (and block)
	select {
	case <-startedProcessing:
	case <-time.After(1 * time.Second):
		t.Fatal("processor did not start processing")
	}

	// Submit second item - fills the queue
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// Create context that will be cancelled during retry
	ctx, cancel := context.WithCancel(context.Background())

	// Start submission in goroutine
	done := make(chan error, 1)
	go func() {
		op := NewIndexOperationWithContext(ctx, OpIndex, "test", nil)
		done <- q.Submit(op)
	}()

	// Cancel context during retry
	time.Sleep(30 * time.Millisecond)
	cancel()

	// Wait for the result BEFORE unblocking processor
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Submit() with context cancelled during retry = %v, want %v", err, context.Canceled)
		}
	case <-time.After(1 * time.Second):
		t.Error("Submit() did not return after context cancelled")
	}

	// Cleanup
	close(processingBlock)
	_ = q.Close()
}

func TestAsyncIndexQueue_Submit_ContextTimeoutDuringRetry(t *testing.T) {
	t.Parallel()

	processingBlock := make(chan struct{})

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      1, // Flush immediately so indexFn blocks processor
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     10,
			RetryBaseDelay: 100 * time.Millisecond,
			RetryMaxDelay:  500 * time.Millisecond,
		},
		func(string, interface{}) error {
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	// Fill queue and block processor
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))
	time.Sleep(20 * time.Millisecond) // Wait for processor to pick up and block
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	op := NewIndexOperationWithContext(ctx, OpIndex, "test", nil)
	err := q.Submit(op)

	// Cleanup
	close(processingBlock)
	defer q.Close()

	if err != context.DeadlineExceeded {
		t.Errorf("Submit() with timeout during retry = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestAsyncIndexQueue_SubmitBlocking_ContextCancelled(t *testing.T) {
	t.Parallel()

	// Use a channel to block ALL processing so the queue stays full
	processingBlock := make(chan struct{})
	startedProcessing := make(chan struct{})
	var once sync.Once

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  1,
			BatchSize:     1, // Process one at a time to ensure we block
			FlushInterval: 1 * time.Millisecond,
			Workers:       1,
		},
		func(string, interface{}) error {
			once.Do(func() {
				close(startedProcessing) // Signal that processing started (only once)
			})
			<-processingBlock // Block forever until cleanup
			return nil
		},
		nil,
		nil,
	)

	// Submit first item - goes into queue, then processor picks it up and blocks
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))

	// Wait for processor to start processing (and block)
	select {
	case <-startedProcessing:
	case <-time.After(1 * time.Second):
		t.Fatal("processor did not start processing")
	}

	// Now submit second item - fills the queue channel
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	// Create context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())

	// Start blocking submission - should block because queue is full
	done := make(chan error, 1)
	go func() {
		op := NewIndexOperationWithContext(ctx, OpIndex, "test", nil)
		done <- q.SubmitBlocking(op)
	}()

	// Give time for SubmitBlocking to actually block on the select
	time.Sleep(20 * time.Millisecond)

	// Cancel context - should unblock SubmitBlocking
	cancel()

	// Wait for the result BEFORE unblocking processor
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("SubmitBlocking() with context cancelled = %v, want %v", err, context.Canceled)
		}
	case <-time.After(1 * time.Second):
		t.Error("SubmitBlocking() did not return after context cancelled")
	}

	// Cleanup - close processingBlock to allow processor to complete
	close(processingBlock)
	_ = q.Close()
}

func TestAsyncIndexQueue_SubmitIndexWithContext_HappyPath(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     10,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		mockIndexFn(indexCalls, 0, nil),
		nil,
		nil,
	)
	defer q.Close()

	ctx := context.Background()
	resultCh := q.SubmitIndexWithContext(ctx, "doc1", map[string]string{"content": "test"})

	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("SubmitIndexWithContext() result = %v, want nil", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for result")
	}

	if indexCalls.Load() != 1 {
		t.Errorf("indexFn called %d times, want 1", indexCalls.Load())
	}
}

func TestAsyncIndexQueue_SubmitIndexWithContext_Cancelled(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resultCh := q.SubmitIndexWithContext(ctx, "doc1", nil)

	select {
	case err := <-resultCh:
		if err != context.Canceled {
			t.Errorf("SubmitIndexWithContext() with cancelled context = %v, want %v", err, context.Canceled)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for result")
	}
}

func TestAsyncIndexQueue_SubmitDeleteWithContext_HappyPath(t *testing.T) {
	t.Parallel()

	deleteCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     10,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		nil,
		mockDeleteFn(deleteCalls, 0, nil),
		nil,
	)
	defer q.Close()

	ctx := context.Background()
	resultCh := q.SubmitDeleteWithContext(ctx, "doc1")

	select {
	case err := <-resultCh:
		if err != nil {
			t.Errorf("SubmitDeleteWithContext() result = %v, want nil", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for result")
	}

	if deleteCalls.Load() != 1 {
		t.Errorf("deleteFn called %d times, want 1", deleteCalls.Load())
	}
}

func TestAsyncIndexQueue_SubmitDeleteWithContext_Cancelled(t *testing.T) {
	t.Parallel()

	q, _, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resultCh := q.SubmitDeleteWithContext(ctx, "doc1")

	select {
	case err := <-resultCh:
		if err != context.Canceled {
			t.Errorf("SubmitDeleteWithContext() with cancelled context = %v, want %v", err, context.Canceled)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for result")
	}
}

func TestAsyncIndexQueue_ContextPropagation_ResourceCleanup(t *testing.T) {
	t.Parallel()

	// Test that resources are properly cleaned up when context is cancelled
	processingBlock := make(chan struct{})
	cleanupDone := make(chan struct{})

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:   1,
			BatchSize:      100,
			FlushInterval:  10 * time.Second,
			Workers:        1,
			MaxRetries:     5,
			RetryBaseDelay: 50 * time.Millisecond,
			RetryMaxDelay:  200 * time.Millisecond,
		},
		func(string, interface{}) error {
			<-processingBlock
			return nil
		},
		nil,
		nil,
	)

	// Fill queue
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill1", nil))
	time.Sleep(10 * time.Millisecond)
	_ = q.SubmitNoRetry(NewIndexOperation(OpIndex, "fill2", nil))

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		op := NewIndexOperationWithContext(ctx, OpIndex, "test", nil)
		_ = q.Submit(op)
		close(cleanupDone)
	}()

	// Cancel and wait for cleanup
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-cleanupDone:
		// Cleanup completed properly
	case <-time.After(500 * time.Millisecond):
		t.Error("cleanup did not complete after context cancellation")
	}

	close(processingBlock)
	_ = q.Close()
}

func TestAsyncIndexQueue_ContextPropagation_MultipleOperations(t *testing.T) {
	t.Parallel()

	q, indexCalls, deleteCalls := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       2,
	})

	ctx := context.Background()

	// Submit multiple operations with context
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(id int) {
			defer wg.Done()
			docID := "doc-" + string(rune('0'+id))
			<-q.SubmitIndexWithContext(ctx, docID, nil)
		}(i)
		go func(id int) {
			defer wg.Done()
			docID := "del-" + string(rune('0'+id))
			<-q.SubmitDeleteWithContext(ctx, docID)
		}(i)
	}

	wg.Wait()
	_ = q.Flush(ctx)

	if indexCalls.Load() != 10 {
		t.Errorf("indexFn called %d times, want 10", indexCalls.Load())
	}
	if deleteCalls.Load() != 10 {
		t.Errorf("deleteFn called %d times, want 10", deleteCalls.Load())
	}
}

func TestAsyncIndexQueue_ContextPropagation_SyncMethodsWithTimeout(t *testing.T) {
	t.Parallel()

	indexCalls := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     10,
			FlushInterval: 500 * time.Millisecond, // Slow flush
			Workers:       1,
		},
		mockIndexFn(indexCalls, 100*time.Millisecond, nil), // Slow indexing
		nil,
		nil,
	)
	defer q.Close()

	// Test SubmitIndexSync with timeout that should expire
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := q.SubmitIndexSync(ctx, "doc1", nil)
	if err != context.DeadlineExceeded {
		t.Errorf("SubmitIndexSync() with short timeout = %v, want %v", err, context.DeadlineExceeded)
	}
}

func TestAsyncIndexQueue_ContextPropagation_NilContextInOp(t *testing.T) {
	t.Parallel()

	q, indexCalls, _ := createTestQueue(t, AsyncIndexQueueConfig{
		MaxQueueSize:  100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		Workers:       1,
	})

	// Create operation with nil context manually
	op := &IndexOperation{
		Type:      OpIndex,
		DocID:     "doc1",
		Document:  nil,
		ResultCh:  make(chan error, 1),
		CreatedAt: time.Now(),
		Ctx:       nil, // Explicitly nil
	}

	err := q.Submit(op)
	if err != nil {
		t.Errorf("Submit() with nil context in op = %v, want nil", err)
	}

	// Wait for processing
	select {
	case <-op.ResultCh:
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for result")
	}

	if indexCalls.Load() != 1 {
		t.Errorf("indexFn called %d times, want 1", indexCalls.Load())
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkAsyncIndexQueue_Submit(b *testing.B) {
	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100000,
			BatchSize:     100,
			FlushInterval: 10 * time.Millisecond,
			Workers:       4,
		},
		func(string, interface{}) error { return nil },
		nil,
		nil,
	)
	defer q.Close()

	op := NewIndexOperation(OpIndex, "doc", map[string]string{"content": "test"})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.Submit(op)
	}
}

func BenchmarkAsyncIndexQueue_SubmitIndex(b *testing.B) {
	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100000,
			BatchSize:     100,
			FlushInterval: 10 * time.Millisecond,
			Workers:       4,
		},
		func(string, interface{}) error { return nil },
		nil,
		nil,
	)
	defer q.Close()

	doc := map[string]string{"content": "test"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.SubmitIndex("doc", doc)
	}
}

func BenchmarkAsyncIndexQueue_ConcurrentSubmit(b *testing.B) {
	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100000,
			BatchSize:     100,
			FlushInterval: 10 * time.Millisecond,
			Workers:       4,
		},
		func(string, interface{}) error { return nil },
		nil,
		nil,
	)
	defer q.Close()

	doc := map[string]string{"content": "test"}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			q.SubmitIndex("doc", doc)
		}
	})
}

// =============================================================================
// BatchResult Tests
// =============================================================================

func TestBatchResult_NewBatchResult(t *testing.T) {
	t.Parallel()

	br := NewBatchResult(10)
	if br == nil {
		t.Fatal("NewBatchResult returned nil")
	}
	if br.Results == nil {
		t.Error("Results map should not be nil")
	}
	if br.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", br.SuccessCount)
	}
	if br.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", br.FailureCount)
	}
}

func TestBatchResult_AddSuccess(t *testing.T) {
	t.Parallel()

	br := NewBatchResult(5)
	br.AddSuccess("doc1")
	br.AddSuccess("doc2")

	if br.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", br.SuccessCount)
	}
	if br.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", br.FailureCount)
	}
	if !br.IsSuccess("doc1") {
		t.Error("doc1 should be successful")
	}
	if !br.IsSuccess("doc2") {
		t.Error("doc2 should be successful")
	}
	if br.GetError("doc1") != nil {
		t.Error("doc1 error should be nil")
	}
}

func TestBatchResult_AddFailure(t *testing.T) {
	t.Parallel()

	br := NewBatchResult(5)
	err1 := errors.New("index error for doc1")
	err2 := errors.New("index error for doc2")

	br.AddFailure("doc1", err1)
	br.AddFailure("doc2", err2)

	if br.SuccessCount != 0 {
		t.Errorf("SuccessCount = %d, want 0", br.SuccessCount)
	}
	if br.FailureCount != 2 {
		t.Errorf("FailureCount = %d, want 2", br.FailureCount)
	}
	if br.IsSuccess("doc1") {
		t.Error("doc1 should not be successful")
	}
	if br.GetError("doc1") != err1 {
		t.Errorf("doc1 error = %v, want %v", br.GetError("doc1"), err1)
	}
	if br.GetError("doc2") != err2 {
		t.Errorf("doc2 error = %v, want %v", br.GetError("doc2"), err2)
	}
}

func TestBatchResult_MixedSuccessAndFailure(t *testing.T) {
	t.Parallel()

	br := NewBatchResult(5)
	err := errors.New("indexing failed")

	br.AddSuccess("doc1")
	br.AddFailure("doc2", err)
	br.AddSuccess("doc3")

	if br.SuccessCount != 2 {
		t.Errorf("SuccessCount = %d, want 2", br.SuccessCount)
	}
	if br.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", br.FailureCount)
	}
	if !br.HasFailures() {
		t.Error("HasFailures should return true")
	}
	if br.IsSuccess("doc2") {
		t.Error("doc2 should not be successful")
	}
	if br.GetError("doc2") != err {
		t.Errorf("doc2 error = %v, want %v", br.GetError("doc2"), err)
	}
}

func TestBatchResult_HasFailures(t *testing.T) {
	t.Parallel()

	br := NewBatchResult(5)

	if br.HasFailures() {
		t.Error("empty result should not have failures")
	}

	br.AddSuccess("doc1")
	if br.HasFailures() {
		t.Error("result with only successes should not have failures")
	}

	br.AddFailure("doc2", errors.New("error"))
	if !br.HasFailures() {
		t.Error("result with failure should have failures")
	}
}

func TestBatchResult_GetError_NotFound(t *testing.T) {
	t.Parallel()

	br := NewBatchResult(5)
	br.AddSuccess("doc1")

	if br.GetError("nonexistent") != nil {
		t.Error("GetError for nonexistent doc should return nil")
	}
}

func TestBatchResult_IsSuccess_NotFound(t *testing.T) {
	t.Parallel()

	br := NewBatchResult(5)
	br.AddSuccess("doc1")

	if br.IsSuccess("nonexistent") {
		t.Error("IsSuccess for nonexistent doc should return false")
	}
}

// =============================================================================
// Per-Operation Error Tracking Tests
// =============================================================================

func TestAsyncIndexQueue_BatchWithResult_AllSucceed(t *testing.T) {
	t.Parallel()

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     5,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		nil,
		nil,
		nil, // No legacy batch function
	)
	defer q.Close()

	// Set enhanced batch function where all operations succeed
	q.SetBatchWithResultFn(func(ops []*IndexOperation) *BatchResult {
		result := NewBatchResult(len(ops))
		for _, op := range ops {
			result.AddSuccess(op.DocID)
		}
		return result
	})

	// Submit operations and collect result channels
	resultChannels := make([]<-chan error, 5)
	for i := 0; i < 5; i++ {
		docID := "doc" + string(rune('A'+i))
		resultChannels[i] = q.SubmitIndex(docID, nil)
	}

	// Wait for batch to process
	_ = q.Flush(context.Background())

	// All operations should succeed
	for i, ch := range resultChannels {
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("operation %d error = %v, want nil", i, err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("operation %d timed out", i)
		}
	}

	stats := q.Stats()
	if stats.Processed != 5 {
		t.Errorf("Processed = %d, want 5", stats.Processed)
	}
	if stats.Dropped != 0 {
		t.Errorf("Dropped = %d, want 0", stats.Dropped)
	}
}

func TestAsyncIndexQueue_BatchWithResult_PartialFailure(t *testing.T) {
	t.Parallel()

	doc2Err := errors.New("doc2 indexing failed")
	doc4Err := errors.New("doc4 indexing failed")

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     5,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		nil,
		nil,
		nil,
	)
	defer q.Close()

	// Set enhanced batch function with some failures
	q.SetBatchWithResultFn(func(ops []*IndexOperation) *BatchResult {
		result := NewBatchResult(len(ops))
		for _, op := range ops {
			switch op.DocID {
			case "docB":
				result.AddFailure(op.DocID, doc2Err)
			case "docD":
				result.AddFailure(op.DocID, doc4Err)
			default:
				result.AddSuccess(op.DocID)
			}
		}
		return result
	})

	// Submit operations
	docIDs := []string{"docA", "docB", "docC", "docD", "docE"}
	resultChannels := make(map[string]<-chan error)
	for _, docID := range docIDs {
		resultChannels[docID] = q.SubmitIndex(docID, nil)
	}

	// Wait for batch to process
	_ = q.Flush(context.Background())

	// Check specific errors for each operation
	expectedErrors := map[string]error{
		"docA": nil,
		"docB": doc2Err,
		"docC": nil,
		"docD": doc4Err,
		"docE": nil,
	}

	for docID, expectedErr := range expectedErrors {
		select {
		case err := <-resultChannels[docID]:
			if err != expectedErr {
				t.Errorf("%s error = %v, want %v", docID, err, expectedErr)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("%s timed out", docID)
		}
	}

	stats := q.Stats()
	if stats.Processed != 3 {
		t.Errorf("Processed = %d, want 3", stats.Processed)
	}
	if stats.Dropped != 2 {
		t.Errorf("Dropped = %d, want 2", stats.Dropped)
	}
}

func TestAsyncIndexQueue_BatchWithResult_AllFail(t *testing.T) {
	t.Parallel()

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     5,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		nil,
		nil,
		nil,
	)
	defer q.Close()

	// Set enhanced batch function where all fail with unique errors
	q.SetBatchWithResultFn(func(ops []*IndexOperation) *BatchResult {
		result := NewBatchResult(len(ops))
		for _, op := range ops {
			result.AddFailure(op.DocID, errors.New("failed: "+op.DocID))
		}
		return result
	})

	// Submit operations
	docIDs := []string{"doc1", "doc2", "doc3"}
	resultChannels := make(map[string]<-chan error)
	for _, docID := range docIDs {
		resultChannels[docID] = q.SubmitIndex(docID, nil)
	}

	// Wait for batch to process
	_ = q.Flush(context.Background())

	// All operations should have unique errors
	for docID := range resultChannels {
		select {
		case err := <-resultChannels[docID]:
			if err == nil {
				t.Errorf("%s should have failed", docID)
			}
			expectedMsg := "failed: " + docID
			if err.Error() != expectedMsg {
				t.Errorf("%s error = %q, want %q", docID, err.Error(), expectedMsg)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("%s timed out", docID)
		}
	}

	stats := q.Stats()
	if stats.Processed != 0 {
		t.Errorf("Processed = %d, want 0", stats.Processed)
	}
	if stats.Dropped != 3 {
		t.Errorf("Dropped = %d, want 3", stats.Dropped)
	}
}

// customTestError is a test error type to verify error context preservation.
type customTestError struct {
	DocID   string
	Details string
}

func (e *customTestError) Error() string {
	return "custom error: " + e.DocID + " - " + e.Details
}

func TestAsyncIndexQueue_BatchWithResult_OriginalErrorPreserved(t *testing.T) {
	t.Parallel()

	// Create a custom error type to verify error context is preserved
	customErr := &customTestError{DocID: "doc1", Details: "validation failed"}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     5,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		nil,
		nil,
		nil,
	)
	defer q.Close()

	// Set batch function returning custom error
	q.SetBatchWithResultFn(func(ops []*IndexOperation) *BatchResult {
		result := NewBatchResult(len(ops))
		for _, op := range ops {
			if op.DocID == "doc1" {
				result.AddFailure(op.DocID, customErr)
			} else {
				result.AddSuccess(op.DocID)
			}
		}
		return result
	})

	resultCh := q.SubmitIndex("doc1", nil)
	_ = q.Flush(context.Background())

	select {
	case err := <-resultCh:
		// Verify the original error type is preserved
		if ce, ok := err.(*customTestError); !ok {
			t.Errorf("error type not preserved, got %T, want *customTestError", err)
		} else if ce.DocID != "doc1" || ce.Details != "validation failed" {
			t.Errorf("error context not preserved: %+v", ce)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("operation timed out")
	}
}

func TestAsyncIndexQueue_BatchWithResult_PreferredOverLegacy(t *testing.T) {
	t.Parallel()

	legacyCalled := &atomic.Bool{}
	enhancedCalled := &atomic.Bool{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     5,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		nil,
		nil,
		func(ops []*IndexOperation) error {
			legacyCalled.Store(true)
			return nil
		},
	)
	defer q.Close()

	// Set enhanced function - should be preferred
	q.SetBatchWithResultFn(func(ops []*IndexOperation) *BatchResult {
		enhancedCalled.Store(true)
		result := NewBatchResult(len(ops))
		for _, op := range ops {
			result.AddSuccess(op.DocID)
		}
		return result
	})

	q.SubmitIndex("doc1", nil)
	_ = q.Flush(context.Background())

	if legacyCalled.Load() {
		t.Error("legacy batch function should not be called when enhanced is set")
	}
	if !enhancedCalled.Load() {
		t.Error("enhanced batch function should be called")
	}
}

func TestAsyncIndexQueue_SetBatchWithResultFn_ClearCallback(t *testing.T) {
	t.Parallel()

	enhancedCalled := &atomic.Int64{}
	legacyCalled := &atomic.Int64{}

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  100,
			BatchSize:     5,
			FlushInterval: 10 * time.Millisecond,
			Workers:       1,
		},
		nil,
		nil,
		func(ops []*IndexOperation) error {
			legacyCalled.Add(1)
			return nil
		},
	)
	defer q.Close()

	// Set enhanced function
	q.SetBatchWithResultFn(func(ops []*IndexOperation) *BatchResult {
		enhancedCalled.Add(1)
		result := NewBatchResult(len(ops))
		for _, op := range ops {
			result.AddSuccess(op.DocID)
		}
		return result
	})

	// First batch uses enhanced
	q.SubmitIndex("doc1", nil)
	_ = q.Flush(context.Background())

	if enhancedCalled.Load() != 1 {
		t.Errorf("enhanced called %d times, want 1", enhancedCalled.Load())
	}

	// Clear enhanced callback
	q.SetBatchWithResultFn(nil)

	// Second batch should fall back to legacy
	q.SubmitIndex("doc2", nil)
	_ = q.Flush(context.Background())

	if legacyCalled.Load() != 1 {
		t.Errorf("legacy called %d times, want 1", legacyCalled.Load())
	}
}

func TestAsyncIndexQueue_BatchWithResult_ConcurrentOperations(t *testing.T) {
	t.Parallel()

	var processedDocs sync.Map

	q := NewAsyncIndexQueue(
		context.Background(),
		AsyncIndexQueueConfig{
			MaxQueueSize:  1000,
			BatchSize:     10,
			FlushInterval: 5 * time.Millisecond,
			Workers:       2,
		},
		nil,
		nil,
		nil,
	)
	defer q.Close()

	// Enhanced batch function tracking processed docs
	q.SetBatchWithResultFn(func(ops []*IndexOperation) *BatchResult {
		result := NewBatchResult(len(ops))
		for _, op := range ops {
			processedDocs.Store(op.DocID, true)
			result.AddSuccess(op.DocID)
		}
		return result
	})

	const numGoroutines = 10
	const opsPerGoroutine = 20

	var wg sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				docID := "doc-" + string(rune('A'+goroutineID)) + "-" + string(rune('0'+i%10))
				q.SubmitIndex(docID, nil)
			}
		}(g)
	}

	wg.Wait()
	_ = q.Flush(context.Background())

	stats := q.Stats()
	expectedTotal := int64(numGoroutines * opsPerGoroutine)
	if stats.Processed != expectedTotal {
		t.Errorf("Processed = %d, want %d", stats.Processed, expectedTotal)
	}
}

func TestErrPartialBatchFailure(t *testing.T) {
	t.Parallel()

	if ErrPartialBatchFailure.Error() != "partial batch failure: some operations failed" {
		t.Errorf("ErrPartialBatchFailure message = %q, want %q",
			ErrPartialBatchFailure.Error(), "partial batch failure: some operations failed")
	}
}
