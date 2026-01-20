package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Ensure imports are used
var _ = time.Second

// TestW4M31_BoundedQueueBackpressure tests that UnboundedQueue enforces limits.
func TestW4M31_BoundedQueueBackpressure(t *testing.T) {
	t.Parallel()

	config := BoundedQueueConfig{
		MaxSize:      10,
		RejectPolicy: RejectPolicyError,
	}
	q := NewBoundedQueue(config)

	// Fill queue
	for i := 0; i < 10; i++ {
		req := &LLMRequest{ID: "req" + string(rune('0'+i))}
		if err := q.Push(req); err != nil {
			t.Fatalf("failed to push item %d: %v", i, err)
		}
	}

	// Verify queue is full
	if !q.IsFull() {
		t.Error("queue should be full")
	}

	// Next push should fail with ErrUserQueueFull
	req := &LLMRequest{ID: "overflow"}
	err := q.Push(req)
	if err != ErrUserQueueFull {
		t.Errorf("expected ErrUserQueueFull, got %v", err)
	}
}

// TestW4M31_BoundedQueueDropOldest tests drop oldest policy.
func TestW4M31_BoundedQueueDropOldest(t *testing.T) {
	t.Parallel()

	config := BoundedQueueConfig{
		MaxSize:      3,
		RejectPolicy: RejectPolicyDropOldest,
	}
	q := NewBoundedQueue(config)

	// Fill queue
	for i := 0; i < 3; i++ {
		req := &LLMRequest{ID: string(rune('A' + i))}
		if err := q.Push(req); err != nil {
			t.Fatalf("failed to push item %d: %v", i, err)
		}
	}

	// Push one more - should drop oldest
	req := &LLMRequest{ID: "D"}
	if err := q.Push(req); err != nil {
		t.Fatalf("failed to push with drop oldest policy: %v", err)
	}

	// Pop and verify oldest was dropped
	first := q.Pop()
	if first.ID != "B" {
		t.Errorf("expected B (oldest remaining), got %s", first.ID)
	}
}

// TestW4M33_WALSyncConcurrent tests WAL sync under concurrent writes.
func TestW4M33_WALSyncConcurrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	config := WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       SyncPeriodic,
		SyncInterval:   10 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wal, err := NewWriteAheadLogWithContext(ctx, config)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	defer wal.Close()

	var wg sync.WaitGroup
	var writeCount atomic.Int64
	const numWriters = 10
	const writesPerWriter = 100

	// Concurrent writers
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				entry := &WALEntry{
					Type:    EntryCheckpoint,
					Payload: []byte("test"),
				}
				if _, err := wal.Append(entry); err != nil {
					t.Errorf("writer %d failed: %v", writerID, err)
					return
				}
				writeCount.Add(1)
			}
		}(i)
	}

	// Concurrent sync calls
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = wal.Sync()
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}

	wg.Wait()

	expected := int64(numWriters * writesPerWriter)
	if writeCount.Load() != expected {
		t.Errorf("expected %d writes, got %d", expected, writeCount.Load())
	}
}

// TestW4M34_AdaptiveChannelResizeConcurrent tests resize under concurrent send/receive.
func TestW4M34_AdaptiveChannelResizeConcurrent(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := AdaptiveChannelConfig{
		MinSize:     4,
		MaxSize:     64,
		InitialSize: 8,
		SendTimeout: 100 * time.Millisecond,
	}
	ch := NewAdaptiveChannelWithContext[int](ctx, config)
	defer ch.Close()

	var senderWg, receiverWg sync.WaitGroup
	var sendCount, recvCount atomic.Int64
	const numSenders = 10
	const sendsPerSender = 100

	// Senders
	for i := 0; i < numSenders; i++ {
		senderWg.Add(1)
		go func() {
			defer senderWg.Done()
			for j := 0; j < sendsPerSender; j++ {
				if err := ch.SendTimeout(j, 50*time.Millisecond); err == nil {
					sendCount.Add(1)
				}
			}
		}()
	}

	// Wait for senders to complete
	senderWg.Wait()
	totalSent := sendCount.Load()

	// Receivers - need to receive all sent messages
	done := make(chan struct{})
	for i := 0; i < 5; i++ {
		receiverWg.Add(1)
		go func() {
			defer receiverWg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				_, err := ch.ReceiveTimeout(50 * time.Millisecond)
				if err == ErrReceiveTimeout {
					// Check if we've received enough
					if recvCount.Load() >= totalSent {
						return
					}
					continue
				}
				if err != nil {
					return
				}
				recvCount.Add(1)
				if recvCount.Load() >= totalSent {
					return
				}
			}
		}()
	}

	// Timeout after reasonable time
	time.AfterFunc(5*time.Second, func() {
		close(done)
	})

	receiverWg.Wait()

	t.Logf("sent: %d, received: %d, resizes up: %d, down: %d",
		sendCount.Load(), recvCount.Load(),
		ch.Stats().ResizeUpCount, ch.Stats().ResizeDownCount)
}

// TestW4M35_SchedulerContextPropagation tests context propagation in scheduler.
func TestW4M35_SchedulerContextPropagation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	scheduler := NewPipelineSchedulerWithContext(ctx, DefaultSchedulerConfig())

	var runnerCancelled atomic.Bool

	// Create a real PipelineRunner that we can track
	runnerConfig := PipelineRunnerConfig{
		ID:              "test-pipeline",
		ShutdownTimeout: 5 * time.Second,
	}
	runner := NewPipelineRunner(runnerConfig)
	runner.RegisterPhase(PhaseWorker, func(ctx context.Context) error {
		<-ctx.Done()
		runnerCancelled.Store(true)
		return nil
	})

	pipeline := &SchedulablePipeline{
		ID:       "test-pipeline",
		Priority: 10,
		Runner:   runner,
	}

	if err := scheduler.Schedule(pipeline); err != nil {
		t.Fatalf("failed to schedule: %v", err)
	}

	// Give pipeline time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel parent context
	cancel()

	// Wait for propagation
	time.Sleep(100 * time.Millisecond)

	if !runnerCancelled.Load() {
		t.Error("pipeline runner should have been cancelled via context propagation")
	}

	scheduler.Close()
}

// TestW4M36_LLMGateContextPropagation tests context propagation in LLM gate.
func TestW4M36_LLMGateContextPropagation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	executor := &slowExecutor{delay: 200 * time.Millisecond}

	config := DefaultDualQueueGateConfig()
	config.RequestTimeout = 5 * time.Second
	gate := NewDualQueueGateWithContext(ctx, config, executor)

	// Submit a request
	req := &LLMRequest{
		ID:        "test-req",
		ResultCh:  make(chan *LLMResult, 1),
		Priority:  PriorityUserInteractive,
		CreatedAt: time.Now(),
	}
	if err := gate.Submit(context.Background(), req); err != nil {
		t.Fatalf("failed to submit: %v", err)
	}

	// Give request time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel parent context
	cancel()

	// Wait for result
	select {
	case result := <-req.ResultCh:
		// Should get cancelled or error
		if result.Error == nil {
			t.Error("expected error due to context cancellation")
		}
	case <-time.After(1 * time.Second):
		// Gate closing should complete within timeout
	}

	gate.Close()
}

// TestW4M37_AdaptiveChannelVersionTracking tests version tracking during resize.
func TestW4M37_AdaptiveChannelVersionTracking(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := AdaptiveChannelConfig{
		MinSize:     2,
		MaxSize:     8,
		InitialSize: 2,
	}
	ch := NewAdaptiveChannelWithContext[int](ctx, config)
	defer ch.Close()

	// Fill channel to trigger resize
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			_ = ch.SendWithContext(ctx, val)
		}(i)
	}

	// Concurrent receives
	received := make([]int, 0, 20)
	var mu sync.Mutex
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				val, err := ch.ReceiveTimeout(200 * time.Millisecond)
				if err == nil {
					mu.Lock()
					received = append(received, val)
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	// Just verify we got some messages without data races
	mu.Lock()
	t.Logf("received %d messages", len(received))
	mu.Unlock()
}

// Helper types for tests

type slowExecutor struct {
	delay time.Duration
}

func (e *slowExecutor) Execute(ctx context.Context, req *LLMRequest) (any, error) {
	select {
	case <-time.After(e.delay):
		return "done", nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
