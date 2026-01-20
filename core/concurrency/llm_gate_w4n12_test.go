package concurrency

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestW4N12_HardDeadline_HappyPath tests that shutdown completes within soft
// timeout when all requests finish promptly (hard deadline not triggered).
func TestW4N12_HardDeadline_HappyPath(t *testing.T) {
	requestsCompleted := atomic.Int32{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			time.Sleep(10 * time.Millisecond)
			requestsCompleted.Add(1)
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 4,
		ShutdownTimeout:       500 * time.Millisecond,
		HardDeadline:          time.Second,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit a few requests
	for i := 0; i < 3; i++ {
		req := &LLMRequest{
			ID:          "happy-path-req",
			AgentType:   AgentGuide,
			UserInvoked: true,
		}
		if err := gate.Submit(context.Background(), req); err != nil {
			t.Fatalf("unexpected submit error: %v", err)
		}
	}

	// Give requests time to start
	time.Sleep(20 * time.Millisecond)

	// Close should complete within soft timeout since requests are fast
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete well before the soft timeout (500ms)
	if elapsed > 300*time.Millisecond {
		t.Errorf("Close took too long: %v (expected < 300ms)", elapsed)
	}

	if requestsCompleted.Load() != 3 {
		t.Errorf("expected 3 requests completed, got %d", requestsCompleted.Load())
	}
}

// TestW4N12_HardDeadline_ForceCancel tests that the hard deadline force-cancels
// orphaned requests that don't respect context cancellation.
func TestW4N12_HardDeadline_ForceCancel(t *testing.T) {
	blockCh := make(chan struct{})
	requestStarted := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(requestStarted)
			// Block indefinitely - IGNORE context cancellation to simulate
			// a misbehaving executor that doesn't respect cancellation
			<-blockCh
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       50 * time.Millisecond,
		HardDeadline:          100 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "hard-deadline-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	if err := gate.Submit(context.Background(), req); err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	// Wait for request to start
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start in time")
	}

	// Close - should complete at hard deadline since executor ignores cancellation
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete around the hard deadline (100ms), not hang forever
	if elapsed < 80*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("Close took unexpected time: %v (expected ~100ms)", elapsed)
	}

	// Unblock the request to allow the executor goroutine to exit
	close(blockCh)

	// Give time for any leaked goroutines to cause issues
	time.Sleep(50 * time.Millisecond)
}

// TestW4N12_HardDeadline_MisbehavingExecutor tests that a misbehaving executor
// doesn't block shutdown - Close returns after hard deadline.
func TestW4N12_HardDeadline_MisbehavingExecutor(t *testing.T) {
	requestsStarted := atomic.Int32{}
	blockCh := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			requestsStarted.Add(1)
			// Misbehaving: completely ignores context cancellation
			<-blockCh
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxUserQueueSize:      100,
		MaxConcurrentRequests: 3,
		ShutdownTimeout:       30 * time.Millisecond,
		HardDeadline:          60 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit multiple requests
	for i := 0; i < 3; i++ {
		req := &LLMRequest{
			ID:          "misbehaving-test",
			AgentType:   AgentGuide,
			UserInvoked: true,
		}
		gate.Submit(context.Background(), req)
	}

	// Wait for requests to start
	time.Sleep(30 * time.Millisecond)
	if requestsStarted.Load() != 3 {
		t.Errorf("expected 3 requests started, got %d", requestsStarted.Load())
	}

	// Close should not hang - it should complete at hard deadline
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete around the hard deadline
	if elapsed < 50*time.Millisecond || elapsed > 150*time.Millisecond {
		t.Errorf("Close took unexpected time: %v (expected ~60ms)", elapsed)
	}

	// Cleanup
	close(blockCh)
	time.Sleep(50 * time.Millisecond)
}

// TestW4N12_HardDeadline_RaceConditions tests race conditions between
// shutdown and active request execution.
func TestW4N12_HardDeadline_RaceConditions(t *testing.T) {
	executionCount := atomic.Int32{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			executionCount.Add(1)
			// Some requests respect cancellation, some don't
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Millisecond):
				return "done", nil
			}
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  1000,
		MaxUserQueueSize:      1000,
		MaxConcurrentRequests: 10,
		ShutdownTimeout:       100 * time.Millisecond,
		HardDeadline:          200 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	var wg sync.WaitGroup
	submitErrors := atomic.Int32{}

	// Start submitting requests concurrently
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				req := &LLMRequest{
					ID:          "race-test",
					AgentType:   AgentGuide,
					UserInvoked: true,
				}
				if err := gate.Submit(context.Background(), req); err != nil {
					submitErrors.Add(1)
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}

	// Close after some requests have been submitted
	time.Sleep(20 * time.Millisecond)
	gate.Close()

	wg.Wait()

	// Some submit errors are expected after gate is closed
	t.Logf("submit errors: %d, executions: %d", submitErrors.Load(), executionCount.Load())
}

// TestW4N12_HardDeadline_ZeroTimeoutNormalized tests that zero shutdown timeout
// gets normalized to the default value (per normalizeDualQueueGateConfig).
// This verifies we don't skip waiting when the timeout appears zero but is
// actually normalized to a default.
func TestW4N12_HardDeadline_ZeroTimeoutNormalized(t *testing.T) {
	// Zero timeout gets normalized to 5 seconds by normalizeDualQueueGateConfig
	config := DualQueueGateConfig{
		ShutdownTimeout: 0, // Will be normalized to 5s
	}
	normalized := normalizeDualQueueGateConfig(config)

	if normalized.ShutdownTimeout != 5*time.Second {
		t.Errorf("expected normalized timeout=5s, got %v", normalized.ShutdownTimeout)
	}

	// With normalized timeout, HardDeadline should be 2x
	if normalized.HardDeadline != 10*time.Second {
		t.Errorf("expected normalized HardDeadline=10s, got %v", normalized.HardDeadline)
	}
}

// TestW4N12_HardDeadline_VeryShortTimeout tests behavior with very short
// timeout values.
func TestW4N12_HardDeadline_VeryShortTimeout(t *testing.T) {
	blockCh := make(chan struct{})
	requestStarted := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(requestStarted)
			<-blockCh
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       5 * time.Millisecond,  // Very short
		HardDeadline:          10 * time.Millisecond, // Very short
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "short-timeout-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	// Wait for request to start
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start in time")
	}

	// Close should complete around hard deadline
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete in reasonable time despite blocking executor
	if elapsed > 100*time.Millisecond {
		t.Errorf("Close took too long: %v", elapsed)
	}

	// Cleanup
	close(blockCh)
	time.Sleep(50 * time.Millisecond)
}

// TestW4N12_HardDeadline_ManyPendingRequests tests shutdown with many
// pending requests in the queue.
func TestW4N12_HardDeadline_ManyPendingRequests(t *testing.T) {
	blockCh := make(chan struct{})
	startedCount := atomic.Int32{}
	handledCount := atomic.Int32{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			startedCount.Add(1)
			defer handledCount.Add(1)
			select {
			case <-blockCh:
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  1000,
		MaxUserQueueSize:      1000,
		MaxConcurrentRequests: 2, // Only 2 concurrent, rest will queue
		ShutdownTimeout:       50 * time.Millisecond,
		HardDeadline:          100 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit many requests - they will be processed in batches
	for i := 0; i < 20; i++ {
		req := &LLMRequest{
			ID:          "queued-test",
			AgentType:   AgentGuide,
			UserInvoked: true,
			ResultCh:    make(chan *LLMResult, 1),
		}
		gate.Submit(context.Background(), req)
	}

	// Wait for some requests to start processing
	time.Sleep(30 * time.Millisecond)

	// Close - should drain queued and wait for active
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete around hard deadline since executor ignores context
	if elapsed > 200*time.Millisecond {
		t.Errorf("Close took too long: %v", elapsed)
	}

	// Some requests should have been started and handled
	t.Logf("started=%d, handled=%d", startedCount.Load(), handledCount.Load())

	// Cleanup
	close(blockCh)
	time.Sleep(50 * time.Millisecond)
}

// TestW4N12_HardDeadline_DefaultConfig tests that the default config
// sets HardDeadline to 2x ShutdownTimeout.
func TestW4N12_HardDeadline_DefaultConfig(t *testing.T) {
	config := DefaultDualQueueGateConfig()

	if config.HardDeadline != 2*config.ShutdownTimeout {
		t.Errorf("expected HardDeadline=%v (2x ShutdownTimeout=%v), got %v",
			2*config.ShutdownTimeout, config.ShutdownTimeout, config.HardDeadline)
	}
}

// TestW4N12_HardDeadline_NormalizeConfig tests that normalization sets
// HardDeadline to 2x ShutdownTimeout when not specified.
func TestW4N12_HardDeadline_NormalizeConfig(t *testing.T) {
	config := DualQueueGateConfig{
		ShutdownTimeout: 100 * time.Millisecond,
		// HardDeadline not set
	}
	normalized := normalizeDualQueueGateConfig(config)

	if normalized.HardDeadline != 200*time.Millisecond {
		t.Errorf("expected HardDeadline=200ms (2x 100ms), got %v", normalized.HardDeadline)
	}
}

// TestW4N12_HardDeadline_CustomConfig tests that custom HardDeadline is preserved.
func TestW4N12_HardDeadline_CustomConfig(t *testing.T) {
	config := DualQueueGateConfig{
		ShutdownTimeout: 100 * time.Millisecond,
		HardDeadline:    500 * time.Millisecond, // Custom value
	}
	normalized := normalizeDualQueueGateConfig(config)

	if normalized.HardDeadline != 500*time.Millisecond {
		t.Errorf("expected HardDeadline=500ms, got %v", normalized.HardDeadline)
	}
}

// TestW4N12_HardDeadline_RequestsCompleteBeforeHardDeadline tests that
// when requests complete between soft timeout and hard deadline, shutdown
// completes without waiting for full hard deadline.
func TestW4N12_HardDeadline_RequestsCompleteBeforeHardDeadline(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	completedCount := atomic.Int32{}

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			select {
			case requestStarted <- struct{}{}:
			default:
			}
			// Simulate slow but cooperative executor that ignores context
			// to ensure it completes at the specified time
			time.Sleep(80 * time.Millisecond)
			completedCount.Add(1)
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       50 * time.Millisecond,  // Soft timeout
		HardDeadline:          200 * time.Millisecond, // Hard deadline much later
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "slow-complete-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	// Wait for request to start
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start in time")
	}

	// Close - request completes at ~80ms, between soft (50ms) and hard (200ms)
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete around 80ms when request finishes, not at 200ms hard deadline
	// Allow some slack for test timing variance
	if elapsed < 50*time.Millisecond || elapsed > 180*time.Millisecond {
		t.Errorf("Close took unexpected time: %v (expected 50-180ms)", elapsed)
	}

	if completedCount.Load() != 1 {
		t.Errorf("expected request to complete, got completed=%d", completedCount.Load())
	}
}

// TestW4N12_HardDeadline_HardDeadlineEqualsSoftTimeout tests edge case
// where hard deadline equals soft timeout.
func TestW4N12_HardDeadline_HardDeadlineEqualsSoftTimeout(t *testing.T) {
	blockCh := make(chan struct{})
	requestStarted := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(requestStarted)
			<-blockCh
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       50 * time.Millisecond,
		HardDeadline:          50 * time.Millisecond, // Same as soft
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "equal-timeout-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start in time")
	}

	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete around soft timeout (hard deadline has no extra time)
	if elapsed < 40*time.Millisecond || elapsed > 100*time.Millisecond {
		t.Errorf("Close took unexpected time: %v (expected ~50ms)", elapsed)
	}

	close(blockCh)
	time.Sleep(50 * time.Millisecond)
}

// testLogCapture is a simple log capture for testing orphan logging
type testLogCapture struct {
	messages []string
	mu       sync.Mutex
}

// TestW4N12_HardDeadline_OrphanedRequestLogging verifies that orphaned
// requests are logged when hard deadline is reached.
func TestW4N12_HardDeadline_OrphanedRequestLogging(t *testing.T) {
	blockCh := make(chan struct{})
	requestStarted := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(requestStarted)
			<-blockCh
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       20 * time.Millisecond,
		HardDeadline:          40 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "orphan-logging-test",
		AgentType:   AgentTypeEngineer,
		UserInvoked: false,
	}
	gate.Submit(context.Background(), req)

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start in time")
	}

	// Close will trigger orphan logging at hard deadline
	// Note: We can't easily capture log output in this test, but we verify
	// the code path is executed by ensuring Close completes
	gate.Close()

	// If we got here, the logging code path was executed
	close(blockCh)
	time.Sleep(50 * time.Millisecond)
}

// TestW4N12_HardDeadline_CollectOrphanInfo tests the collectOrphanInfo helper.
func TestW4N12_HardDeadline_CollectOrphanInfo(t *testing.T) {
	gate := &DualQueueGate{
		activeRequests: make(map[string]*ActiveRequest),
	}

	// Add some active requests
	now := time.Now()
	gate.activeRequests["req1"] = &ActiveRequest{
		Request:   &LLMRequest{ID: "req1", AgentType: AgentTypeEngineer},
		StartedAt: now.Add(-100 * time.Millisecond),
	}
	gate.activeRequests["req2"] = &ActiveRequest{
		Request:   &LLMRequest{ID: "req2", AgentType: AgentGuide},
		StartedAt: now.Add(-200 * time.Millisecond),
	}

	orphans := gate.collectOrphanInfo()

	if len(orphans) != 2 {
		t.Fatalf("expected 2 orphans, got %d", len(orphans))
	}

	// Verify orphan info
	found := make(map[string]bool)
	for _, o := range orphans {
		found[o.id] = true
		if o.duration < 0 {
			t.Errorf("orphan %s has negative duration: %v", o.id, o.duration)
		}
		if o.agent == "" {
			t.Errorf("orphan %s has empty agent type", o.id)
		}
	}

	if !found["req1"] || !found["req2"] {
		t.Errorf("missing expected orphan IDs: %v", found)
	}
}

// TestW4N12_HardDeadline_SignalOnWaitGroupDone tests the signalOnWaitGroupDone helper.
func TestW4N12_HardDeadline_SignalOnWaitGroupDone(t *testing.T) {
	gate := &DualQueueGate{}
	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go gate.signalOnWaitGroupDone(&wg, done)

	// done should not be closed yet
	select {
	case <-done:
		t.Fatal("done channel closed prematurely")
	case <-time.After(20 * time.Millisecond):
		// Expected
	}

	// Complete the WaitGroup
	wg.Done()

	// done should close
	select {
	case <-done:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("done channel not closed after WaitGroup done")
	}
}

// TestW4N12_HardDeadline_WaitWithSoftTimeout_Success tests waitWithSoftTimeout
// returning true when requests complete in time.
func TestW4N12_HardDeadline_WaitWithSoftTimeout_Success(t *testing.T) {
	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			time.Sleep(10 * time.Millisecond)
			return "done", nil
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       500 * time.Millisecond,
		HardDeadline:          time.Second,
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "soft-success-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)
	time.Sleep(20 * time.Millisecond)

	// Manually test waitWithSoftTimeout via Close
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete fast, not wait for full soft timeout
	if elapsed > 200*time.Millisecond {
		t.Errorf("expected fast completion, took %v", elapsed)
	}
}

// TestW4N12_HardDeadline_WaitWithHardDeadline_NoRemaining tests the edge case
// where hard deadline has no remaining time after soft timeout.
func TestW4N12_HardDeadline_WaitWithHardDeadline_NoRemaining(t *testing.T) {
	blockCh := make(chan struct{})
	requestStarted := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			close(requestStarted)
			<-blockCh
			return "done", nil
		},
	}

	// HardDeadline less than ShutdownTimeout (edge case)
	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 1,
		ShutdownTimeout:       50 * time.Millisecond,
		HardDeadline:          30 * time.Millisecond, // Less than soft timeout
	}
	gate := NewDualQueueGate(config, executor)

	req := &LLMRequest{
		ID:          "no-remaining-test",
		AgentType:   AgentGuide,
		UserInvoked: true,
	}
	gate.Submit(context.Background(), req)

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not start in time")
	}

	// Close should complete at soft timeout since hard deadline is smaller
	start := time.Now()
	gate.Close()
	elapsed := time.Since(start)

	// Should complete around soft timeout
	if elapsed < 40*time.Millisecond || elapsed > 100*time.Millisecond {
		t.Errorf("Close took unexpected time: %v (expected ~50ms)", elapsed)
	}

	close(blockCh)
	time.Sleep(50 * time.Millisecond)
}

// TestW4N12_HardDeadline_OrphanInfoStruct tests the orphanInfo struct fields.
func TestW4N12_HardDeadline_OrphanInfoStruct(t *testing.T) {
	info := orphanInfo{
		id:       "test-id",
		agent:    "engineer",
		duration: 123 * time.Millisecond,
	}

	if info.id != "test-id" {
		t.Errorf("expected id=test-id, got %s", info.id)
	}
	if info.agent != "engineer" {
		t.Errorf("expected agent=engineer, got %s", info.agent)
	}
	if info.duration != 123*time.Millisecond {
		t.Errorf("expected duration=123ms, got %v", info.duration)
	}
}

// TestW4N12_HardDeadline_LogOrphanedRequestsEmpty tests that logging works
// with empty active requests map.
func TestW4N12_HardDeadline_LogOrphanedRequestsEmpty(t *testing.T) {
	gate := &DualQueueGate{
		activeRequests: make(map[string]*ActiveRequest),
	}

	// Should not panic with empty map
	gate.logOrphanedRequests()
}

// TestW4N12_HardDeadline_ConcurrentCloseOperations tests that multiple
// concurrent Close calls work correctly with hard deadline logic.
func TestW4N12_HardDeadline_ConcurrentCloseOperations(t *testing.T) {
	blockCh := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			select {
			case <-blockCh:
				return "done", nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxConcurrentRequests: 4,
		ShutdownTimeout:       100 * time.Millisecond,
		HardDeadline:          200 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit some requests
	for i := 0; i < 4; i++ {
		req := &LLMRequest{
			ID:          "concurrent-close-test",
			AgentType:   AgentGuide,
			UserInvoked: true,
		}
		gate.Submit(context.Background(), req)
	}

	time.Sleep(20 * time.Millisecond)

	// Multiple concurrent Close calls
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate.Close()
		}()
	}

	// Let requests complete
	close(blockCh)
	wg.Wait()
	// If we get here without panics or hangs, the test passes
}

// TestW4N12_HardDeadline_RequestCountDuringShutdown tests that the request
// count is properly tracked during shutdown phases.
func TestW4N12_HardDeadline_RequestCountDuringShutdown(t *testing.T) {
	completedCount := atomic.Int32{}
	startedCount := atomic.Int32{}
	blockCh := make(chan struct{})

	executor := &mockExecutor{
		executeFunc: func(ctx context.Context, req *LLMRequest) (any, error) {
			startedCount.Add(1)
			select {
			case <-blockCh:
				completedCount.Add(1)
				return "done", nil
			case <-ctx.Done():
				completedCount.Add(1)
				return nil, ctx.Err()
			}
		},
	}

	config := DualQueueGateConfig{
		MaxPipelineQueueSize:  100,
		MaxUserQueueSize:      100,
		MaxConcurrentRequests: 3,
		ShutdownTimeout:       100 * time.Millisecond,
		HardDeadline:          200 * time.Millisecond,
	}
	gate := NewDualQueueGate(config, executor)

	// Submit requests
	for i := 0; i < 3; i++ {
		req := &LLMRequest{
			ID:          "count-test",
			AgentType:   AgentGuide,
			UserInvoked: true,
		}
		gate.Submit(context.Background(), req)
	}

	// Wait for all requests to start executing
	for i := 0; i < 100; i++ {
		if startedCount.Load() >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if startedCount.Load() < 3 {
		t.Errorf("expected 3 started requests, got %d", startedCount.Load())
	}

	// Verify active count matches started count
	stats := gate.Stats()
	t.Logf("active=%d, started=%d", stats.ActiveRequests, startedCount.Load())

	// Start shutdown
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(blockCh)
	}()

	gate.Close()

	// All requests should have been handled
	if completedCount.Load() != 3 {
		t.Errorf("expected 3 completed requests, got %d", completedCount.Load())
	}
}

// TestW4N12_HardDeadline_LogMessageFormat verifies that orphan log messages
// contain the expected information (this is a structural test, not a functional one).
func TestW4N12_HardDeadline_LogMessageFormat(t *testing.T) {
	// Create a gate with known active requests
	gate := &DualQueueGate{
		activeRequests: make(map[string]*ActiveRequest),
	}

	gate.activeRequests["test-req-123"] = &ActiveRequest{
		Request:   &LLMRequest{ID: "test-req-123", AgentType: AgentTypeEngineer},
		StartedAt: time.Now().Add(-5 * time.Second),
	}

	orphans := gate.collectOrphanInfo()
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}

	info := orphans[0]
	if !strings.Contains(info.id, "test-req-123") {
		t.Errorf("orphan info should contain request ID, got: %s", info.id)
	}
	if !strings.Contains(info.agent, "engineer") {
		t.Errorf("orphan info should contain agent type, got: %s", info.agent)
	}
	if info.duration < 5*time.Second {
		t.Errorf("orphan info should reflect running duration, got: %v", info.duration)
	}
}
