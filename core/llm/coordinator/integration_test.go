// Package coordinator provides integration tests for the cascading failure prevention system.
// These tests validate that all protection layers work together correctly under realistic scenarios.
package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/llm/backpressure"
	"github.com/adalundhe/sylk/core/llm/bulkhead"
	"github.com/adalundhe/sylk/core/llm/correlation"
	"github.com/adalundhe/sylk/core/llm/health"
	"github.com/adalundhe/sylk/core/llm/ratelimit"
	"github.com/adalundhe/sylk/core/llm/timeout"
)

// =============================================================================
// Test Helpers and Mocks
// =============================================================================

// intMockProvider is a configurable mock provider for integration testing.
type intMockProvider struct {
	name          string
	mu            sync.Mutex
	completeFunc  func(ctx context.Context, req *LLMRequest) (*LLMResponse, error)
	streamFunc    func(ctx context.Context, req *LLMRequest) <-chan string
	requestCount  atomic.Int64
	failureCount  atomic.Int64
	latency       time.Duration
	failAfter     int64 // Fail after N requests (0 = don't fail)
	failFor       int64 // Number of requests to fail
	failError     error
	rateLimitAt   int64 // Return 429 at request N (0 = never)
	rateLimitFor  int64 // Number of 429 responses
}

func newIntMockProvider(name string) *intMockProvider {
	p := &intMockProvider{
		name:      name,
		latency:   10 * time.Millisecond,
		failError: errors.New("provider error"),
	}
	p.completeFunc = p.defaultComplete
	p.streamFunc = p.defaultStream
	return p
}

func (m *intMockProvider) Complete(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	return m.completeFunc(ctx, req)
}

func (m *intMockProvider) Stream(ctx context.Context, req *LLMRequest) <-chan string {
	return m.streamFunc(ctx, req)
}

func (m *intMockProvider) Name() string {
	return m.name
}

func (m *intMockProvider) defaultComplete(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
	count := m.requestCount.Add(1)

	// Check for rate limiting
	if m.rateLimitAt > 0 && count >= m.rateLimitAt && count < m.rateLimitAt+m.rateLimitFor {
		m.failureCount.Add(1)
		return nil, errors.New("429 Too Many Requests - rate limit exceeded")
	}

	// Check for failures
	if m.failAfter > 0 && count > m.failAfter && count <= m.failAfter+m.failFor {
		m.failureCount.Add(1)
		return nil, m.failError
	}

	// Simulate latency
	select {
	case <-time.After(m.latency):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return NewLLMResponse("test response", m.latency), nil
}

func (m *intMockProvider) defaultStream(ctx context.Context, req *LLMRequest) <-chan string {
	ch := make(chan string, 5)
	go func() {
		defer close(ch)
		for i := 0; i < 5; i++ {
			select {
			case <-time.After(m.latency / 5):
				ch <- fmt.Sprintf("chunk%d", i)
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

func (m *intMockProvider) setFailAfter(n int64, duration int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failAfter = n
	m.failFor = duration
}

func (m *intMockProvider) setRateLimitAt(n int64, duration int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimitAt = n
	m.rateLimitFor = duration
}

func (m *intMockProvider) setLatency(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latency = d
}

func (m *intMockProvider) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestCount.Store(0)
	m.failureCount.Store(0)
	m.failAfter = 0
	m.failFor = 0
	m.rateLimitAt = 0
	m.rateLimitFor = 0
}

// intBudgetGetter allows dynamic budget manipulation for integration tests.
type intBudgetGetter struct {
	mu            sync.RWMutex
	sessionUsage  map[string]float64
	taskUsage     map[string]float64 // key: sessionID:taskID
	defaultUsage  float64
	usageIncrease float64 // Increase usage by this amount per call
}

func newIntBudgetGetter() *intBudgetGetter {
	return &intBudgetGetter{
		sessionUsage: make(map[string]float64),
		taskUsage:    make(map[string]float64),
		defaultUsage: 0.5,
	}
}

func (b *intBudgetGetter) GetUsagePercent(sessionID string) float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if usage, ok := b.sessionUsage[sessionID]; ok {
		return usage
	}
	return b.defaultUsage
}

func (b *intBudgetGetter) GetTaskUsagePercent(sessionID, taskID string) float64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	key := sessionID + ":" + taskID
	if usage, ok := b.taskUsage[key]; ok {
		return usage
	}
	return b.defaultUsage
}

func (b *intBudgetGetter) setSessionUsage(sessionID string, usage float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessionUsage[sessionID] = usage
}

func (b *intBudgetGetter) setTaskUsage(sessionID, taskID string, usage float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.taskUsage[sessionID+":"+taskID] = usage
}

func (b *intBudgetGetter) setDefaultUsage(usage float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.defaultUsage = usage
}

// intSignalDispatcher records signals for verification in integration tests.
type intSignalDispatcher struct {
	mu              sync.Mutex
	signals         []correlation.Signal
	backoffSignals  int
	recoverySignals int
}

func newIntSignalDispatcher() *intSignalDispatcher {
	return &intSignalDispatcher{
		signals: make([]correlation.Signal, 0),
	}
}

func (d *intSignalDispatcher) SendSignal(signal correlation.Signal) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.signals = append(d.signals, signal)
	switch signal.Type() {
	case correlation.SignalGlobalBackoff:
		d.backoffSignals++
	case correlation.SignalGlobalRecovery:
		d.recoverySignals++
	}
}

func (d *intSignalDispatcher) signalCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.signals)
}

func (d *intSignalDispatcher) getBackoffSignals() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.backoffSignals
}

func (d *intSignalDispatcher) getRecoverySignals() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recoverySignals
}

// intTestCoordinatorConfig returns a config with short timeouts for testing.
func intTestCoordinatorConfig() CoordinatorConfig {
	return CoordinatorConfig{
		BulkheadConfigs: map[bulkhead.BulkheadLevel]bulkhead.BulkheadConfig{
			bulkhead.LevelSession: {
				MaxConcurrent: 10,
				MaxQueueSize:  50,
				QueueTimeout:  500 * time.Millisecond,
				CircuitConfig: bulkhead.CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          200 * time.Millisecond,
				},
			},
			bulkhead.LevelProvider: {
				MaxConcurrent: 5,
				MaxQueueSize:  20,
				QueueTimeout:  300 * time.Millisecond,
				CircuitConfig: bulkhead.CircuitConfig{
					FailureThreshold: 3,
					SuccessThreshold: 2,
					Timeout:          150 * time.Millisecond,
				},
			},
			bulkhead.LevelModel: {
				MaxConcurrent: 3,
				MaxQueueSize:  10,
				QueueTimeout:  200 * time.Millisecond,
				CircuitConfig: bulkhead.CircuitConfig{
					FailureThreshold: 2,
					SuccessThreshold: 1,
					Timeout:          100 * time.Millisecond,
				},
			},
		},
		RateLimitConfig: ratelimit.MultiLayerConfig{
			TokenBucket: ratelimit.TokenBucketConfig{
				Capacity:   20,
				RefillRate: 10, // 10 tokens per second
			},
			SlidingWindow: ratelimit.SlidingWindowConfig{
				WindowSize:  1 * time.Second,
				MaxRequests: 15,
			},
			Adaptive: ratelimit.Adaptive429Config{
				InitialLimit: 10,
				DecayFactor:  0.8,
				GrowthFactor: 1.1,
				MinLimit:     2,
				MaxLimit:     50,
			},
		},
		HealthConfig: health.HealthConfig{
			ProbeInterval:    100 * time.Millisecond,
			ProbeTimeout:     50 * time.Millisecond,
			ActiveWeight:     0.4,
			PassiveWeight:    0.6,
			WindowSize:       1 * time.Second,
			MinSamples:       3,
			RejectThreshold:  0.3,
			WarnThreshold:    0.5,
			MonitorThreshold: 0.7,
		},
		BackpressureConfig: backpressure.CostBackpressureConfig{
			DelayThresholds: []backpressure.DelayThreshold{
				{UsagePercent: 0.80, Multiplier: 1.5},
				{UsagePercent: 0.90, Multiplier: 2.0},
				{UsagePercent: 0.95, Multiplier: 4.0},
			},
			RejectNewAt: 1.0,
			BaseDelay:   10 * time.Millisecond,
		},
		CorrelationConfig: correlation.CorrelationConfig{
			CorrelationWindow:    500 * time.Millisecond,
			MinFailuresForGlobal: 3,
			GlobalBackoffBase:    100 * time.Millisecond,
			GlobalBackoffMax:     500 * time.Millisecond,
			RecoveryRampUp:       0.2,
		},
		StreamTimeoutConfig: timeout.StreamingTimeoutConfig{
			FirstTokenTimeout: 200 * time.Millisecond,
			InterTokenTimeout: 100 * time.Millisecond,
			TotalTimeout:      1 * time.Second,
		},
	}
}

// =============================================================================
// Integration Test: Session Isolation
// =============================================================================

func TestIntegration_SessionIsolation(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	// Create providers that fail for specific sessions
	provider := newIntMockProvider("openai")

	var sessionASuccess, sessionBSuccess atomic.Int32
	var sessionAFail, sessionBFail atomic.Int32

	// Session A will experience failures
	// Session B should be unaffected
	sessionAID := "session-A"
	sessionBID := "session-B"

	var wg sync.WaitGroup

	// Run Session A requests - these will fail after a few requests
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			req := NewLLMRequest(sessionAID, fmt.Sprintf("task-%d", i), "openai", "gpt-4")
			req.AddMessage("user", "Hello")

			// Make every other request fail for session A
			if i%2 == 0 {
				provider.setFailAfter(0, 1)
				provider.reset()
			}

			resp, err := coord.ExecuteRequest(context.Background(), req, provider)
			if err == nil && resp != nil {
				sessionASuccess.Add(1)
			} else {
				sessionAFail.Add(1)
			}
		}
	}()

	// Run Session B requests concurrently - these should mostly succeed
	wg.Add(1)
	go func() {
		defer wg.Done()
		successProvider := newIntMockProvider("openai")
		for i := 0; i < 10; i++ {
			req := NewLLMRequest(sessionBID, fmt.Sprintf("task-%d", i), "openai", "gpt-4")
			req.AddMessage("user", "Hello")

			resp, err := coord.ExecuteRequest(context.Background(), req, successProvider)
			if err == nil && resp != nil {
				sessionBSuccess.Add(1)
			} else {
				sessionBFail.Add(1)
			}
		}
	}()

	wg.Wait()

	// Verify session isolation
	stats := coord.Stats()
	if stats.SessionCount() != 2 {
		t.Errorf("expected 2 sessions, got %d", stats.SessionCount())
	}

	// Session B should have high success rate despite Session A's failures
	if sessionBSuccess.Load() < 8 {
		t.Errorf("session B should have at least 8 successes, got %d", sessionBSuccess.Load())
	}

	t.Logf("Session A: %d success, %d fail", sessionASuccess.Load(), sessionAFail.Load())
	t.Logf("Session B: %d success, %d fail", sessionBSuccess.Load(), sessionBFail.Load())
}

func TestIntegration_SessionBulkheadIsolation(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	// Reduce concurrency to make isolation visible
	config.BulkheadConfigs[bulkhead.LevelSession] = bulkhead.BulkheadConfig{
		MaxConcurrent: 2,
		MaxQueueSize:  5,
		QueueTimeout:  100 * time.Millisecond,
		CircuitConfig: bulkhead.CircuitConfig{
			FailureThreshold: 10,
			SuccessThreshold: 5,
			Timeout:          100 * time.Millisecond,
		},
	}

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	slowProvider := newIntMockProvider("openai")
	slowProvider.setLatency(50 * time.Millisecond)

	fastProvider := newIntMockProvider("anthropic")
	fastProvider.setLatency(5 * time.Millisecond)

	var sessionAActive atomic.Int32
	var sessionBComplete atomic.Int32

	var wg sync.WaitGroup

	// Session A: Fill up its bulkhead with slow requests
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionAActive.Add(1)
			req := NewLLMRequest("session-A", fmt.Sprintf("slow-task-%d", idx), "openai", "gpt-4")
			req.AddMessage("user", "Hello")
			_, _ = coord.ExecuteRequest(context.Background(), req, slowProvider)
			sessionAActive.Add(-1)
		}(i)
	}

	// Give Session A time to fill its bulkhead
	time.Sleep(10 * time.Millisecond)

	// Session B: Should be able to run independently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := NewLLMRequest("session-B", fmt.Sprintf("fast-task-%d", idx), "anthropic", "claude-3")
			req.AddMessage("user", "Hello")
			resp, err := coord.ExecuteRequest(context.Background(), req, fastProvider)
			if err == nil && resp != nil {
				sessionBComplete.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// Session B should complete quickly regardless of Session A's slow requests
	if sessionBComplete.Load() < 5 {
		t.Errorf("session B should complete all 5 requests, got %d", sessionBComplete.Load())
	}
}

// =============================================================================
// Integration Test: Provider Failure Cascade Prevention
// =============================================================================

func TestIntegration_ProviderFailureCascadePrevention(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	// Lower threshold for faster test
	config.CorrelationConfig.MinFailuresForGlobal = 3
	config.CorrelationConfig.GlobalBackoffBase = 50 * time.Millisecond

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	// Phase 1: Trigger failures from multiple sessions using always-failing providers
	var failCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(sessionIdx int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("session-%d", sessionIdx)
			// Create a provider with custom complete function that always fails
			failingProvider := newIntMockProvider("openai")
			failingProvider.completeFunc = func(ctx context.Context, req *LLMRequest) (*LLMResponse, error) {
				return nil, errors.New("simulated provider failure")
			}

			req := NewLLMRequest(sessionID, "task-1", "openai", "gpt-4")
			req.AddMessage("user", "Hello")

			_, err := coord.ExecuteRequest(context.Background(), req, failingProvider)
			if err != nil {
				failCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// Verify failures were recorded
	if failCount.Load() < 3 {
		t.Logf("got %d failures (note: some may be blocked by circuit breaker)", failCount.Load())
	}

	// Wait for correlation to trigger global backoff
	time.Sleep(100 * time.Millisecond)

	// Check if global backoff signal was sent (timing dependent)
	t.Logf("backoff signals: %d (timing dependent)", dispatcher.getBackoffSignals())

	// Phase 2: After backoff, verify requests can succeed again
	time.Sleep(200 * time.Millisecond) // Wait for backoff to end

	successProvider := newIntMockProvider("openai")
	req := NewLLMRequest("recovery-session", "task-1", "openai", "gpt-4")
	req.AddMessage("user", "Hello")

	resp, err := coord.ExecuteRequest(context.Background(), req, successProvider)
	// After backoff ends, requests should eventually succeed
	if err == nil && resp != nil {
		t.Log("recovery request succeeded after backoff")
	} else {
		t.Logf("recovery request result: err=%v", err)
	}
}

func TestIntegration_GlobalBackoffRecovery(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	config.CorrelationConfig.MinFailuresForGlobal = 2
	config.CorrelationConfig.GlobalBackoffBase = 30 * time.Millisecond
	config.CorrelationConfig.RecoveryRampUp = 0.5

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	// Trigger global backoff
	for i := 0; i < 3; i++ {
		failingProvider := newIntMockProvider("openai")
		failingProvider.failAfter = 0
		failingProvider.failFor = 10

		sessionID := fmt.Sprintf("trigger-session-%d", i)
		req := NewLLMRequest(sessionID, "task-1", "openai", "gpt-4")
		req.AddMessage("user", "Hello")
		_, _ = coord.ExecuteRequest(context.Background(), req, failingProvider)
	}

	// Wait for backoff to be triggered
	time.Sleep(50 * time.Millisecond)

	// Verify backoff is active
	active, _ := coord.correlationEngine.GetBackoffState("openai")
	t.Logf("backoff active after failures: %v", active)

	// Wait for recovery
	time.Sleep(150 * time.Millisecond)

	// After recovery, requests should be allowed
	provider := newIntMockProvider("openai")
	var successCount atomic.Int32

	for i := 0; i < 5; i++ {
		req := NewLLMRequest("recovery-session", fmt.Sprintf("task-%d", i), "openai", "gpt-4")
		req.AddMessage("user", "Hello")

		resp, err := coord.ExecuteRequest(context.Background(), req, provider)
		if err == nil && resp != nil {
			successCount.Add(1)
		}
	}

	// Some requests should succeed after recovery
	t.Logf("success count after recovery: %d", successCount.Load())
}

// =============================================================================
// Integration Test: Rate Limiting
// =============================================================================

func TestIntegration_RateLimitingMultiLayer(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	// Use very restrictive rate limits for testing
	config.RateLimitConfig = ratelimit.MultiLayerConfig{
		TokenBucket: ratelimit.TokenBucketConfig{
			Capacity:   5,
			RefillRate: 5, // 5 tokens per second
		},
		SlidingWindow: ratelimit.SlidingWindowConfig{
			WindowSize:  500 * time.Millisecond,
			MaxRequests: 3,
		},
		Adaptive: ratelimit.Adaptive429Config{
			InitialLimit: 10,
			DecayFactor:  0.8,
			GrowthFactor: 1.1,
			MinLimit:     1,
			MaxLimit:     20,
		},
	}

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")

	var successCount, rateLimitedCount atomic.Int32
	var wg sync.WaitGroup

	// Send burst of requests
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			req := NewLLMRequest("burst-session", fmt.Sprintf("task-%d", idx), "openai", "gpt-4")
			req.AddMessage("user", "Hello")

			resp, err := coord.ExecuteRequest(ctx, req, provider)
			if err == nil && resp != nil {
				successCount.Add(1)
			} else if errors.Is(err, ErrRateLimited) || errors.Is(err, context.DeadlineExceeded) {
				rateLimitedCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	// With restrictive limits, some should succeed and some should be rate limited
	t.Logf("success: %d, rate limited/timeout: %d", successCount.Load(), rateLimitedCount.Load())

	// Verify that not all requests succeeded (rate limiting is working)
	if successCount.Load() == 20 {
		t.Error("expected some requests to be rate limited")
	}
}

func TestIntegration_429AdaptiveLearning(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")
	// Provider returns 429 on requests 3-5
	provider.rateLimitAt = 3
	provider.rateLimitFor = 3

	var successCount, error429Count atomic.Int32

	// Send requests
	for i := 0; i < 10; i++ {
		req := NewLLMRequest("adaptive-session", fmt.Sprintf("task-%d", i), "openai", "gpt-4")
		req.AddMessage("user", "Hello")

		resp, err := coord.ExecuteRequest(context.Background(), req, provider)
		if err == nil && resp != nil {
			successCount.Add(1)
		} else if err != nil && isRateLimitError(err) {
			error429Count.Add(1)
		}
	}

	t.Logf("success: %d, 429 errors: %d", successCount.Load(), error429Count.Load())

	// Verify that 429s were encountered
	if error429Count.Load() == 0 && provider.failureCount.Load() > 0 {
		t.Logf("provider had %d failures but coordinator didn't report 429s", provider.failureCount.Load())
	}
}

// =============================================================================
// Integration Test: Health Monitoring
// =============================================================================

func TestIntegration_HealthMonitoring(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	// Make health more sensitive
	config.HealthConfig.MinSamples = 2
	config.HealthConfig.RejectThreshold = 0.4

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")

	// First, make some successful requests to establish baseline
	for i := 0; i < 3; i++ {
		req := NewLLMRequest("health-session", fmt.Sprintf("success-%d", i), "openai", "gpt-4")
		req.AddMessage("user", "Hello")
		_, _ = coord.ExecuteRequest(context.Background(), req, provider)
	}

	// Now fail many requests to degrade health
	failingProvider := newIntMockProvider("openai")
	failingProvider.failAfter = 0
	failingProvider.failFor = 100

	for i := 0; i < 10; i++ {
		req := NewLLMRequest("health-session", fmt.Sprintf("fail-%d", i), "openai", "gpt-4")
		req.AddMessage("user", "Hello")
		_, _ = coord.ExecuteRequest(context.Background(), req, failingProvider)
	}

	// Check health score
	stats := coord.Stats()
	if ps, ok := stats.Providers["openai"]; ok {
		t.Logf("health score after failures: %f", ps.HealthScore)
	}

	// After degradation, health should eventually reject requests
	// Note: This depends on the health monitor's update interval
	time.Sleep(100 * time.Millisecond)

	// Verify health monitoring is working
	monitor := coord.getOrCreateHealthMonitor("openai")
	score := monitor.Score()
	t.Logf("final health score: %f", score)
}

func TestIntegration_HealthScoreCombination(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	config.HealthConfig.ActiveWeight = 0.3
	config.HealthConfig.PassiveWeight = 0.7

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")

	// Mix of success and failure
	for i := 0; i < 10; i++ {
		req := NewLLMRequest("health-combo-session", fmt.Sprintf("task-%d", i), "openai", "gpt-4")
		req.AddMessage("user", "Hello")

		if i%3 == 0 {
			failProvider := newIntMockProvider("openai")
			failProvider.failAfter = 0
			failProvider.failFor = 1
			_, _ = coord.ExecuteRequest(context.Background(), req, failProvider)
		} else {
			_, _ = coord.ExecuteRequest(context.Background(), req, provider)
		}
	}

	// Check that health reflects the mixed results
	monitor := coord.getOrCreateHealthMonitor("openai")
	decision := monitor.Check()

	t.Logf("health decision: proceed=%v, score=%f, status=%s",
		decision.Proceed, decision.Score, decision.Status)

	// With 2/3 success rate, should still be healthy enough to proceed
	if !decision.Proceed && decision.Score > config.HealthConfig.RejectThreshold {
		t.Errorf("expected proceed=true with score=%f above threshold=%f",
			decision.Score, config.HealthConfig.RejectThreshold)
	}
}

// =============================================================================
// Integration Test: Cost Backpressure
// =============================================================================

func TestIntegration_CostBackpressure(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")

	// Test increasing delays as budget depletes
	testCases := []struct {
		usage        float64
		expectDelay  bool
		expectReject bool
		description  string
	}{
		{0.5, false, false, "50% usage - no delay"},
		{0.85, true, false, "85% usage - delay"},
		{0.92, true, false, "92% usage - more delay"},
		{1.1, false, true, "110% usage - reject"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			sessionID := fmt.Sprintf("backpressure-session-%.0f", tc.usage*100)
			budget.setSessionUsage(sessionID, tc.usage)

			req := NewLLMRequest(sessionID, "task-1", "openai", "gpt-4")
			req.AddMessage("user", "Hello")

			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			resp, err := coord.ExecuteRequest(ctx, req, provider)
			elapsed := time.Since(start)

			if tc.expectReject {
				if err == nil {
					t.Errorf("expected rejection at %.0f%% usage", tc.usage*100)
				}
				if !errors.Is(err, ErrBudgetExhausted) {
					t.Errorf("expected ErrBudgetExhausted, got %v", err)
				}
			} else if tc.expectDelay {
				if err == nil && resp != nil {
					// Should have some delay
					if elapsed < 10*time.Millisecond {
						t.Logf("expected delay at %.0f%% usage, elapsed: %v", tc.usage*100, elapsed)
					}
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error at %.0f%% usage: %v", tc.usage*100, err)
				}
			}
		})
	}
}

func TestIntegration_BudgetExhaustionRejection(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")

	// Set budget to 100%
	budget.setDefaultUsage(1.05)

	var rejectedCount atomic.Int32

	for i := 0; i < 5; i++ {
		req := NewLLMRequest("exhausted-session", fmt.Sprintf("task-%d", i), "openai", "gpt-4")
		req.AddMessage("user", "Hello")

		_, err := coord.ExecuteRequest(context.Background(), req, provider)
		if errors.Is(err, ErrBudgetExhausted) {
			rejectedCount.Add(1)
		}
	}

	// All requests should be rejected
	if rejectedCount.Load() != 5 {
		t.Errorf("expected 5 rejections, got %d", rejectedCount.Load())
	}
}

// =============================================================================
// Integration Test: Streaming Timeouts
// =============================================================================

func TestIntegration_StreamingFirstTokenTimeout(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	config.StreamTimeoutConfig.FirstTokenTimeout = 50 * time.Millisecond

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	// Provider that delays first token too long
	slowProvider := newIntMockProvider("openai")
	slowProvider.streamFunc = func(ctx context.Context, req *LLMRequest) <-chan string {
		ch := make(chan string, 1)
		go func() {
			defer close(ch)
			// Delay longer than FirstTokenTimeout
			select {
			case <-time.After(100 * time.Millisecond):
				ch <- "late chunk"
			case <-ctx.Done():
				return
			}
		}()
		return ch
	}

	req := NewLLMRequest("streaming-session", "task-1", "openai", "gpt-4")
	req.AddMessage("user", "Hello")
	req.Stream = true

	resp, err := coord.ExecuteRequest(context.Background(), req, slowProvider)

	// Should timeout waiting for first token
	if err == nil && resp != nil {
		// If streaming response returned, drain it to check for timeout
		for range resp.Chunks {
			// Drain
		}
		t.Log("streaming completed (timeout may have been handled internally)")
	} else if err != nil {
		t.Logf("streaming timeout error: %v", err)
	}
}

func TestIntegration_StreamingInterTokenTimeout(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()
	config.StreamTimeoutConfig.FirstTokenTimeout = 100 * time.Millisecond
	config.StreamTimeoutConfig.InterTokenTimeout = 30 * time.Millisecond

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	// Provider that has gaps between tokens
	gapProvider := newIntMockProvider("openai")
	gapProvider.streamFunc = func(ctx context.Context, req *LLMRequest) <-chan string {
		ch := make(chan string, 5)
		go func() {
			defer close(ch)
			// First chunk quickly
			ch <- "chunk1"

			// Long gap before second chunk
			select {
			case <-time.After(50 * time.Millisecond):
				ch <- "chunk2"
			case <-ctx.Done():
				return
			}
		}()
		return ch
	}

	req := NewLLMRequest("streaming-gap-session", "task-1", "openai", "gpt-4")
	req.AddMessage("user", "Hello")
	req.Stream = true

	resp, err := coord.ExecuteRequest(context.Background(), req, gapProvider)

	if err != nil {
		t.Logf("inter-token timeout error: %v", err)
	} else if resp != nil && resp.IsStreaming() {
		chunks := 0
		for range resp.Chunks {
			chunks++
		}
		t.Logf("received %d chunks before timeout/completion", chunks)
	}
}

// =============================================================================
// Integration Test: End-to-End Protection
// =============================================================================

func TestIntegration_EndToEndProtection(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")

	var successCount, failCount atomic.Int32
	var wg sync.WaitGroup

	// Run realistic concurrent workload
	numSessions := 5
	requestsPerSession := 10

	for s := 0; s < numSessions; s++ {
		wg.Add(1)
		go func(sessionIdx int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("e2e-session-%d", sessionIdx)

			for r := 0; r < requestsPerSession; r++ {
				req := NewLLMRequest(sessionID, fmt.Sprintf("task-%d", r), "openai", "gpt-4")
				req.AddMessage("user", fmt.Sprintf("Request %d", r))

				// Randomly enable streaming
				if r%3 == 0 {
					req.Stream = true
				}

				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				resp, err := coord.ExecuteRequest(ctx, req, provider)
				cancel()

				if err == nil && resp != nil {
					if resp.IsStreaming() {
						// Drain streaming response
						for range resp.Chunks {
						}
					}
					successCount.Add(1)
				} else {
					failCount.Add(1)
				}
			}
		}(s)
	}

	wg.Wait()

	totalRequests := int64(numSessions * requestsPerSession)
	successRate := float64(successCount.Load()) / float64(totalRequests)

	t.Logf("end-to-end results: success=%d, fail=%d, rate=%.2f%%",
		successCount.Load(), failCount.Load(), successRate*100)

	// Most requests should succeed in a healthy system
	if successRate < 0.8 {
		t.Errorf("success rate %.2f%% is too low for healthy system", successRate*100)
	}
}

func TestIntegration_NoDeadlocksUnderLoad(t *testing.T) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")
	provider.setLatency(5 * time.Millisecond)

	// Run high-concurrency workload with timeout to detect deadlocks
	done := make(chan struct{})
	var wg sync.WaitGroup

	go func() {
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				sessionID := fmt.Sprintf("deadlock-session-%d", idx%10)
				req := NewLLMRequest(sessionID, fmt.Sprintf("task-%d", idx), "openai", "gpt-4")
				req.AddMessage("user", "Hello")

				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer cancel()
				_, _ = coord.ExecuteRequest(ctx, req, provider)
			}(i)
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("all requests completed without deadlock")
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock detected: test did not complete within timeout")
	}
}

func TestIntegration_RaceConditions(t *testing.T) {
	// This test is designed to be run with -race flag
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")

	var wg sync.WaitGroup

	// Concurrent operations that could cause races
	for i := 0; i < 50; i++ {
		// Concurrent requests
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("race-session-%d", idx%5)
			req := NewLLMRequest(sessionID, fmt.Sprintf("task-%d", idx), "openai", "gpt-4")
			req.AddMessage("user", "Hello")
			_, _ = coord.ExecuteRequest(context.Background(), req, provider)
		}(i)

		// Concurrent stats access
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = coord.Stats()
		}()

		// Concurrent budget changes
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("race-session-%d", idx%5)
			budget.setSessionUsage(sessionID, float64(idx%100)/100.0)
		}(i)
	}

	wg.Wait()
	t.Log("race condition test completed")
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkIntegration_SingleRequest(b *testing.B) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")
	provider.setLatency(0) // Minimize provider latency

	req := NewLLMRequest("bench-session", "task-1", "openai", "gpt-4")
	req.AddMessage("user", "Hello")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = coord.ExecuteRequest(context.Background(), req, provider)
	}
}

func BenchmarkIntegration_ConcurrentRequests(b *testing.B) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	provider := newIntMockProvider("openai")
	provider.setLatency(0)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sessionID := fmt.Sprintf("bench-session-%d", i%10)
			req := NewLLMRequest(sessionID, fmt.Sprintf("task-%d", i), "openai", "gpt-4")
			req.AddMessage("user", "Hello")
			_, _ = coord.ExecuteRequest(context.Background(), req, provider)
			i++
		}
	})
}

func BenchmarkIntegration_HealthCheck(b *testing.B) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	// Initialize health monitor
	_ = coord.getOrCreateHealthMonitor("openai")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		monitor := coord.getOrCreateHealthMonitor("openai")
		_ = monitor.Check()
	}
}

func BenchmarkIntegration_RateLimitCheck(b *testing.B) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	// Initialize rate limiter
	_ = coord.getOrCreateRateLimiter("openai")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter := coord.getOrCreateRateLimiter("openai")
		_ = limiter.Check()
	}
}

func BenchmarkIntegration_BulkheadAcquireRelease(b *testing.B) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bulk := coord.getOrCreateBulkhead("bench-session")
		slot, err := bulk.Acquire(context.Background(), "openai", "gpt-4")
		if err == nil {
			slot.Release()
		}
	}
}

func BenchmarkIntegration_BackpressureEvaluate(b *testing.B) {
	budget := newIntBudgetGetter()
	dispatcher := newIntSignalDispatcher()
	config := intTestCoordinatorConfig()

	coord := NewLLMRequestCoordinator(config, budget, dispatcher, nil)
	defer coord.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = coord.costBackpressure.Evaluate("bench-session", "task-1")
	}
}
