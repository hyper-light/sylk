package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/llm/ratelimit"
	"github.com/adalundhe/sylk/core/providers"
)

// dispatchCtx returns a background context decorated with a minimal
// identity + task pair. The gateway requires both at every call site;
// tests that don't care about identity attribution still need to
// satisfy that contract.
func dispatchCtx() context.Context {
	id := identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:       "uid-test",
		Namespace: "ns-test",
		Pod:       identity.PodRef{ID: "guide", Type: identity.PodTypeDaemon},
		Name:      "guide",
		Kind:      identity.AgentTypeGuide,
		Category:  identity.CategoryStandalone,
		Model:     "test-model",
		Labels: identity.Labels{
			identity.LabelKind:      identity.AgentTypeGuide.String(),
			identity.LabelCategory:  identity.CategoryStandalone.String(),
			identity.LabelPod:       "guide",
			identity.LabelNamespace: "ns-test",
		},
	})
	task := identity.RebuildTaskForReplay(identity.ReplayTaskRef{
		UID:         "task-test",
		DisplayID:   "test-task",
		Correlation: "corr-test",
		Namespace:   "ns-test",
	})
	ctx := identity.WithIdentity(context.Background(), id)
	return identity.WithTask(ctx, task)
}

// --- Test helpers ---

func fastConfig() GatewayConfig {
	return GatewayConfig{
		Name:                  "test",
		MaxConcurrentRequests: 2,
		RateLimit: ratelimit.MultiLayerConfig{
			TokenBucket: ratelimit.TokenBucketConfig{
				Capacity:   100,
				RefillRate: 100,
			},
			SlidingWindow: ratelimit.SlidingWindowConfig{
				WindowSize:  60 * time.Second,
				MaxRequests: 1000,
			},
			Adaptive: ratelimit.Adaptive429Config{
				InitialLimit: 1000,
				DecayFactor:  0.95,
				GrowthFactor: 1.05,
				MinLimit:     5,
				MaxLimit:     2000,
			},
		},
		AgingRate:    2.0,
		MaxWaitTime:  2 * time.Second,
		MaxQueueSize: 8,
	}
}

func tightRateLimitConfig() GatewayConfig {
	cfg := fastConfig()
	cfg.RateLimit.TokenBucket = ratelimit.TokenBucketConfig{
		Capacity:   1,
		RefillRate: 0.5, // 1 token per 2 seconds
	}
	cfg.RateLimit.SlidingWindow = ratelimit.SlidingWindowConfig{
		WindowSize:  1 * time.Second,
		MaxRequests: 1,
	}
	return cfg
}

// mockProvider implements ProviderAdapter + StreamHandlerProvider for tests.
type mockProvider struct {
	completeFn      func(ctx context.Context, req *providers.CompletionRequest) (*providers.CompletionResponse, error)
	streamFn        func(ctx context.Context, req *providers.CompletionRequest) (<-chan *providers.StreamChunk, error)
	streamHandlerFn func(ctx context.Context, req *providers.StreamRequest, handler providers.StreamHandler) error
}

func (m *mockProvider) Name() string                                   { return "mock" }
func (m *mockProvider) SupportedModels() []providers.ModelInfo         { return nil }
func (m *mockProvider) CountTokens(_ []providers.Message) (int, error) { return 0, nil }
func (m *mockProvider) MaxContextTokens(_ string) int                  { return 100000 }
func (m *mockProvider) HealthCheck(_ context.Context) error            { return nil }

func (m *mockProvider) Complete(ctx context.Context, req *providers.CompletionRequest) (*providers.CompletionResponse, error) {
	if m.completeFn != nil {
		return m.completeFn(ctx, req)
	}
	return &providers.CompletionResponse{Content: "ok"}, nil
}

func (m *mockProvider) Stream(ctx context.Context, req *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, req)
	}
	ch := make(chan *providers.StreamChunk, 1)
	ch <- &providers.StreamChunk{Type: providers.ChunkTypeEnd}
	close(ch)
	return ch, nil
}

func (m *mockProvider) StreamWithHandler(ctx context.Context, req *providers.StreamRequest, handler providers.StreamHandler) error {
	if m.streamHandlerFn != nil {
		return m.streamHandlerFn(ctx, req, handler)
	}
	return handler(&providers.StreamChunk{Type: providers.ChunkTypeEnd})
}

var (
	_ providers.ProviderAdapter       = (*mockProvider)(nil)
	_ providers.StreamHandlerProvider = (*mockProvider)(nil)
)

type timeoutMockProvider struct {
	mockProvider
	timeout time.Duration
}

func (m *timeoutMockProvider) RequestTimeout() time.Duration { return m.timeout }

// --- Scheduler tests ---

func TestGatewayProvider_RequestTimeoutPassthrough(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	wrapped := gw.WrapProvider(&timeoutMockProvider{timeout: 42 * time.Second}, PriorityExecution)

	if got, want := wrapped.RequestTimeout(), 42*time.Second; got != want {
		t.Fatalf("RequestTimeout() = %v, want %v", got, want)
	}
}

// laneByValue is a fixed-key LaneKeyFn used by tests that want everything
// in one lane (so legacy priority/aging assertions still hold).
func laneByValue(key string) LaneKeyFn {
	return func(context.Context) string { return key }
}

// laneByCtxValue extracts a string lane key from a context value. Tests
// that want WFQ across distinct lanes thread the lane key through a
// dedicated context key.
type testLaneKey struct{}

func laneByCtxValue() LaneKeyFn {
	return func(ctx context.Context) string {
		if v, ok := ctx.Value(testLaneKey{}).(string); ok {
			return v
		}
		return ""
	}
}

func withLane(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, testLaneKey{}, key)
}

func TestScheduler_EnqueueAndSignalNext(t *testing.T) {
	s := NewScheduler(2.0, laneByValue("a"))
	defer s.Close()

	ch, err := s.Enqueue(context.Background(), PriorityExecution)
	if err != nil {
		t.Fatal(err)
	}

	// Channel should not be closed yet.
	select {
	case <-ch:
		t.Fatal("channel should not be signaled before SignalNext")
	default:
	}

	signaled := s.SignalNext()
	if !signaled {
		t.Fatal("SignalNext should return true when queue has waiters")
	}

	// Now channel should be closed.
	select {
	case <-ch:
	default:
		t.Fatal("channel should be closed after SignalNext")
	}
}

func TestScheduler_PriorityOrder(t *testing.T) {
	// Single lane, no aging — priority alone decides order.
	s := NewScheduler(0, laneByValue("a"))
	defer s.Close()

	chLow, _ := s.Enqueue(context.Background(), PriorityBackground)
	chHigh, _ := s.Enqueue(context.Background(), PriorityUserInteractive)

	// First signal should go to the higher priority waiter.
	s.SignalNext()

	select {
	case <-chHigh:
	default:
		t.Fatal("high priority should be signaled first")
	}
	select {
	case <-chLow:
		t.Fatal("low priority should NOT be signaled yet")
	default:
	}

	s.SignalNext()
	select {
	case <-chLow:
	default:
		t.Fatal("low priority should be signaled second")
	}
}

func TestScheduler_AgingPreventsStarvation(t *testing.T) {
	// Single lane so this is a pure priority+aging test.
	// With agingRate=2000 vt/sec, a Background waiter discounts its
	// virtualFinish by ~120 over 60ms — enough to overtake a fresh
	// UserInteractive (vt-increment 1.0).
	s := NewScheduler(2000, laneByValue("a"))
	defer s.Close()

	chOld, _ := s.Enqueue(context.Background(), PriorityBackground)
	time.Sleep(60 * time.Millisecond) // age the old waiter

	chNew, _ := s.Enqueue(context.Background(), PriorityUserInteractive)

	// Despite lower base priority, the aged waiter should win.
	s.SignalNext()
	select {
	case <-chOld:
	default:
		t.Fatal("aged background request should be signaled first")
	}

	s.SignalNext()
	select {
	case <-chNew:
	default:
		t.Fatal("newer high-priority request should be signaled second")
	}
}

// TestScheduler_NeverRejectsOnCount confirms the regression-pinning
// invariant: Enqueue never returns a "queue full" error regardless of
// how many waiters pile up. The only way for a waiter's wait to end in
// failure is its own context deadline / WaitForSlot maxWait expiring.
func TestScheduler_NeverRejectsOnCount(t *testing.T) {
	s := NewScheduler(0, laneByValue("a"))
	defer s.Close()

	// Pile up far more waiters than the old hand-picked cap of 32.
	for range 10000 {
		if _, err := s.Enqueue(context.Background(), PriorityExecution); err != nil {
			t.Fatalf("Enqueue must not reject on count, got %v", err)
		}
	}
	if got, want := s.Len(), 10000; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
}

// TestScheduler_WFQ_FairnessAcrossLanes confirms one greedy lane cannot
// starve another. Two lanes ("greedy", "thin") each enqueue, but greedy
// enqueues many in one burst. Across the first 2N dispatches, thin
// should receive roughly half.
func TestScheduler_WFQ_FairnessAcrossLanes(t *testing.T) {
	s := NewScheduler(0, laneByCtxValue())
	defer s.Close()

	const burst = 100
	greedyChs := make([]chan struct{}, 0, burst)
	thinChs := make([]chan struct{}, 0, burst)

	// Greedy lane fires its full burst first.
	for range burst {
		ch, err := s.Enqueue(withLane(context.Background(), "greedy"), PriorityExecution)
		if err != nil {
			t.Fatal(err)
		}
		greedyChs = append(greedyChs, ch)
	}
	// Then thin lane fires the same burst.
	for range burst {
		ch, err := s.Enqueue(withLane(context.Background(), "thin"), PriorityExecution)
		if err != nil {
			t.Fatal(err)
		}
		thinChs = append(thinChs, ch)
	}

	// Dispatch the first 2N items and count by lane.
	greedyServed, thinServed := 0, 0
	for i := 0; i < burst*2; i++ {
		s.SignalNext()
		// Check which channel got closed this round by polling
		// each lane's pending list.
		for j, ch := range greedyChs {
			if ch == nil {
				continue
			}
			select {
			case <-ch:
				greedyChs[j] = nil
				greedyServed++
			default:
			}
		}
		for j, ch := range thinChs {
			if ch == nil {
				continue
			}
			select {
			case <-ch:
				thinChs[j] = nil
				thinServed++
			default:
			}
		}
	}

	if greedyServed+thinServed != burst*2 {
		t.Fatalf("expected %d total dispatched, got greedy=%d thin=%d", burst*2, greedyServed, thinServed)
	}
	// Each lane should have received ~burst dispatches.
	// Allow ±10% variance for the WFQ ordering of equal-weight lanes.
	tol := burst / 10
	if abs(thinServed-burst) > tol {
		t.Fatalf("thin lane should get ~%d dispatches (±%d), got %d (greedy=%d)",
			burst, tol, thinServed, greedyServed)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestScheduler_WaitForSlot_Timeout(t *testing.T) {
	s := NewScheduler(0, laneByValue("a"))
	defer s.Close()

	ctx := context.Background()
	err := s.WaitForSlot(ctx, PriorityExecution, 10*time.Millisecond)
	if !errors.Is(err, ErrGatewayTimeout) {
		t.Fatalf("expected ErrGatewayTimeout, got %v", err)
	}
}

func TestScheduler_WaitForSlot_ContextCancel(t *testing.T) {
	s := NewScheduler(0, laneByValue("a"))
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := s.WaitForSlot(ctx, PriorityExecution, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Verify waiter was cleaned up.
	if s.Len() != 0 {
		t.Fatalf("expected empty queue after cancel, got %d", s.Len())
	}
}

func TestScheduler_Close_SignalsAll(t *testing.T) {
	s := NewScheduler(0, laneByValue("a"))

	ch1, _ := s.Enqueue(context.Background(), PriorityExecution)
	ch2, _ := s.Enqueue(context.Background(), PriorityValidation)

	s.Close()

	select {
	case <-ch1:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch1 should be closed after Close()")
	}
	select {
	case <-ch2:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2 should be closed after Close()")
	}
}

func TestScheduler_Close_RejectsEnqueue(t *testing.T) {
	s := NewScheduler(0, laneByValue("a"))
	s.Close()

	_, err := s.Enqueue(context.Background(), PriorityExecution)
	if !errors.Is(err, ErrGatewayClosed) {
		t.Fatalf("expected ErrGatewayClosed, got %v", err)
	}
}

// --- Gateway tests ---

func TestGateway_AdmitRelease(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	ctx := context.Background()
	if err := gw.Admit(ctx, PriorityExecution); err != nil {
		t.Fatal(err)
	}
	gw.Release(nil)

	snap := gw.Metrics()
	if snap.Admitted != 1 {
		t.Fatalf("expected 1 admitted, got %d", snap.Admitted)
	}
	if snap.Completed != 1 {
		t.Fatalf("expected 1 completed, got %d", snap.Completed)
	}
}

func TestGateway_ConcurrencyLimit(t *testing.T) {
	cfg := fastConfig()
	cfg.MaxConcurrentRequests = 1
	gw := NewProviderGateway(cfg, nil)
	defer gw.Stop()

	ctx := context.Background()

	// First admit succeeds immediately.
	if err := gw.Admit(ctx, PriorityExecution); err != nil {
		t.Fatal(err)
	}

	// Second admit should queue and timeout since slot is held.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	err := gw.Admit(ctx2, PriorityExecution)
	if err == nil {
		t.Fatal("second admit should fail with concurrency=1 and slot held")
	}

	gw.Release(nil)
}

func TestGateway_ConcurrencyQueuing(t *testing.T) {
	cfg := fastConfig()
	cfg.MaxConcurrentRequests = 1
	gw := NewProviderGateway(cfg, nil)
	defer gw.Stop()

	ctx := context.Background()

	// Take the one slot.
	if err := gw.Admit(ctx, PriorityExecution); err != nil {
		t.Fatal(err)
	}

	// Start second admit in background — it will queue.
	done := make(chan error, 1)
	go func() {
		done <- gw.Admit(ctx, PriorityExecution)
	}()

	// Give the goroutine time to enqueue.
	time.Sleep(20 * time.Millisecond)

	// Release the slot — should unblock the queued request.
	gw.Release(nil)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queued admit should succeed after release, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued admit should have been unblocked")
	}

	gw.Release(nil)
}

func TestGateway_Closed(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	gw.Stop()

	err := gw.Admit(context.Background(), PriorityExecution)
	if !errors.Is(err, ErrGatewayClosed) {
		t.Fatalf("expected ErrGatewayClosed, got %v", err)
	}
}

func TestGateway_Record429(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	gw.Record429(10 * time.Second)
	gw.Record429(5 * time.Second)

	snap := gw.Metrics()
	if snap.Errors429 != 2 {
		t.Fatalf("expected 2 errors429, got %d", snap.Errors429)
	}
}

func TestGateway_MetricsAccuracy(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	ctx := context.Background()
	for range 5 {
		if err := gw.Admit(ctx, PriorityExecution); err != nil {
			t.Fatal(err)
		}
		gw.Release(nil)
	}

	snap := gw.Metrics()
	if snap.Admitted != 5 {
		t.Fatalf("expected 5 admitted, got %d", snap.Admitted)
	}
	if snap.Completed != 5 {
		t.Fatalf("expected 5 completed, got %d", snap.Completed)
	}
	if snap.Inflight != 0 {
		t.Fatalf("expected 0 inflight, got %d", snap.Inflight)
	}
}

// --- Proxy tests ---

func TestProxy_Complete(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	inner := &mockProvider{
		completeFn: func(_ context.Context, _ *providers.CompletionRequest) (*providers.CompletionResponse, error) {
			return &providers.CompletionResponse{Content: "hello"}, nil
		},
	}

	proxy := gw.WrapProvider(inner, PriorityExecution)

	resp, err := proxy.Complete(dispatchCtx(), &providers.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" {
		t.Fatalf("expected 'hello', got %q", resp.Content)
	}

	snap := gw.Metrics()
	if snap.Admitted != 1 || snap.Completed != 1 {
		t.Fatalf("metrics mismatch: admitted=%d completed=%d", snap.Admitted, snap.Completed)
	}
}

func TestProxy_Complete_429Detection(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	inner := &mockProvider{
		completeFn: func(_ context.Context, _ *providers.CompletionRequest) (*providers.CompletionResponse, error) {
			return nil, &providers.ProviderError{
				Provider:   providers.ProviderTypeGoogle,
				StatusCode: 429,
				Message:    "rate limited",
				Retryable:  true,
				RetryAfter: 30 * time.Second,
			}
		},
	}

	proxy := gw.WrapProvider(inner, PriorityExecution)
	_, err := proxy.Complete(dispatchCtx(), &providers.CompletionRequest{})
	if err == nil {
		t.Fatal("expected error")
	}

	snap := gw.Metrics()
	if snap.Errors429 == 0 {
		t.Fatal("expected 429 errors to be recorded")
	}
}

func TestProxy_StreamWithHandler(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	var handlerCalled atomic.Bool
	inner := &mockProvider{
		streamHandlerFn: func(_ context.Context, _ *providers.StreamRequest, handler providers.StreamHandler) error {
			handlerCalled.Store(true)
			return handler(&providers.StreamChunk{Type: providers.ChunkTypeEnd})
		},
	}

	proxy := gw.WrapProvider(inner, PriorityExecution)
	err := proxy.StreamWithHandler(dispatchCtx(), &providers.StreamRequest{}, func(chunk *providers.StreamChunk) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !handlerCalled.Load() {
		t.Fatal("inner handler should have been called")
	}

	snap := gw.Metrics()
	if snap.Admitted != 1 || snap.Completed != 1 {
		t.Fatalf("metrics mismatch: admitted=%d completed=%d", snap.Admitted, snap.Completed)
	}
}

func TestProxy_Stream(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	inner := &mockProvider{
		streamFn: func(_ context.Context, _ *providers.CompletionRequest) (<-chan *providers.StreamChunk, error) {
			ch := make(chan *providers.StreamChunk, 2)
			ch <- &providers.StreamChunk{Type: providers.ChunkTypeText, Text: "hi"}
			ch <- &providers.StreamChunk{Type: providers.ChunkTypeEnd}
			close(ch)
			return ch, nil
		},
	}

	proxy := gw.WrapProvider(inner, PriorityExecution)
	ch, err := proxy.Stream(dispatchCtx(), &providers.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var chunks int
	for range ch {
		chunks++
	}
	if chunks != 2 {
		t.Fatalf("expected 2 chunks, got %d", chunks)
	}

	// Give the forwardStreamChunks goroutine time to call Release.
	time.Sleep(20 * time.Millisecond)

	snap := gw.Metrics()
	if snap.Completed != 1 {
		t.Fatalf("expected 1 completed, got %d", snap.Completed)
	}
}

func TestProxy_Passthrough(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	inner := &mockProvider{}
	proxy := gw.WrapProvider(inner, PriorityExecution)

	if proxy.Name() != "mock" {
		t.Fatalf("Name() should passthrough, got %q", proxy.Name())
	}
	if proxy.MaxContextTokens("x") != 100000 {
		t.Fatalf("MaxContextTokens should passthrough")
	}
	if err := proxy.HealthCheck(context.Background()); err != nil {
		t.Fatal("HealthCheck should passthrough")
	}
}

func TestProxy_Inner(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	inner := &mockProvider{}
	proxy := gw.WrapProvider(inner, PriorityExecution)

	if proxy.Inner() != inner {
		t.Fatal("Inner() should return the wrapped provider")
	}
}

func TestProxy_RetryObserverChaining(t *testing.T) {
	gw := NewProviderGateway(fastConfig(), nil)
	defer gw.Stop()

	var existingFired atomic.Bool
	ctx := providers.WithRetryObserver(dispatchCtx(), func(event providers.RetryEvent) {
		existingFired.Store(true)
	})

	inner := &mockProvider{
		completeFn: func(ctx context.Context, _ *providers.CompletionRequest) (*providers.CompletionResponse, error) {
			// Simulate the inner provider's retry loop notifying the observer.
			obs := providers.RetryObserverFromContext(ctx)
			if obs != nil {
				obs(providers.RetryEvent{
					Attempt:     1,
					MaxAttempts: 3,
					Err: &providers.ProviderError{
						Provider:   providers.ProviderTypeGoogle,
						StatusCode: 429,
						RetryAfter: 5 * time.Second,
						Retryable:  true,
					},
					Delay: 5 * time.Second,
				})
			}
			return &providers.CompletionResponse{Content: "ok"}, nil
		},
	}

	proxy := gw.WrapProvider(inner, PriorityExecution)
	_, err := proxy.Complete(ctx, &providers.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if !existingFired.Load() {
		t.Fatal("existing observer should have been called through chaining")
	}

	snap := gw.Metrics()
	if snap.Errors429 == 0 {
		t.Fatal("gateway should have recorded 429 from retry observer")
	}
}

// --- Concurrency stress test ---

func TestGateway_ConcurrentAdmit(t *testing.T) {
	cfg := fastConfig()
	cfg.MaxConcurrentRequests = 4
	cfg.MaxQueueSize = 32
	gw := NewProviderGateway(cfg, nil)
	defer gw.Stop()

	const goroutines = 16
	var wg sync.WaitGroup
	var admitted atomic.Int64

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := gw.Admit(ctx, PriorityExecution); err == nil {
				admitted.Add(1)
				time.Sleep(5 * time.Millisecond)
				gw.Release(nil)
			}
		}()
	}

	wg.Wait()

	snap := gw.Metrics()
	if snap.Admitted != admitted.Load() {
		t.Fatalf("metrics.Admitted=%d != actually admitted=%d", snap.Admitted, admitted.Load())
	}
	if snap.Inflight != 0 {
		t.Fatalf("expected 0 inflight after all release, got %d", snap.Inflight)
	}
}

// --- is429Error tests ---

func TestIs429Error(t *testing.T) {
	pe := &providers.ProviderError{
		Provider:   providers.ProviderTypeGoogle,
		StatusCode: 429,
		RetryAfter: 30 * time.Second,
	}
	d, ok := is429Error(pe)
	if !ok {
		t.Fatal("expected 429 detection")
	}
	if d != 30*time.Second {
		t.Fatalf("expected 30s retry-after, got %v", d)
	}

	_, ok = is429Error(errors.New("generic error"))
	if ok {
		t.Fatal("generic error should not be 429")
	}
}
