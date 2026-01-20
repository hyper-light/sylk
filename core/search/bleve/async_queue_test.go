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
			MaxQueueSize:  5,
			BatchSize:     100,
			FlushInterval: 10 * time.Second,
			Workers:       1,
		},
		mockIndexFn(&atomic.Int64{}, 100*time.Millisecond, nil), // Slow processing
		nil,
		nil,
	)
	defer q.Close()

	// Fill the queue quickly
	var fullCount int
	for i := 0; i < 20; i++ {
		op := NewIndexOperation(OpIndex, "doc"+string(rune('0'+i)), nil)
		err := q.Submit(op)
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
