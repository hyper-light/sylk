// Package coordinator provides the LLM request coordination system.
// It orchestrates all protection layers including bulkheads, rate limiting,
// health monitoring, backpressure, failure correlation, and streaming timeouts.
package coordinator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/llm/backpressure"
	"github.com/adalundhe/sylk/core/llm/bulkhead"
	"github.com/adalundhe/sylk/core/llm/correlation"
	"github.com/adalundhe/sylk/core/llm/health"
	"github.com/adalundhe/sylk/core/llm/ratelimit"
	"github.com/adalundhe/sylk/core/llm/timeout"
)

// maxWaitForRateLimit is the maximum time to wait before rejecting due to rate limit.
const maxWaitForRateLimit = 30 * time.Second

// streamingChunkBuffer is the buffer size for streaming response channels.
const streamingChunkBuffer = 100

// LLMRequestCoordinator orchestrates all protection layers for LLM requests.
type LLMRequestCoordinator struct {
	config CoordinatorConfig

	// Per-session bulkheads
	sessionBulkheads sync.Map // sessionID -> *bulkhead.HierarchicalBulkhead

	// Per-provider rate limiters
	rateLimiters sync.Map // provider -> *ratelimit.MultiLayerRateLimiter

	// Per-provider health monitors
	healthMonitors sync.Map // provider -> *health.HybridHealthMonitor

	// Shared components
	costBackpressure  *backpressure.CostBackpressure
	correlationEngine *correlation.FailureCorrelationEngine
	streamTimeoutCfg  timeout.StreamingTimeoutConfig

	logger *slog.Logger
}

// NewLLMRequestCoordinator creates a coordinator with all protection layers.
func NewLLMRequestCoordinator(
	config CoordinatorConfig,
	budgetGetter backpressure.BudgetGetter,
	dispatcher correlation.SignalDispatcher,
	logger *slog.Logger,
) *LLMRequestCoordinator {
	return &LLMRequestCoordinator{
		config:            config,
		costBackpressure:  backpressure.NewCostBackpressure(config.BackpressureConfig, budgetGetter),
		correlationEngine: correlation.NewFailureCorrelationEngine(config.CorrelationConfig, dispatcher),
		streamTimeoutCfg:  config.StreamTimeoutConfig,
		logger:            logger,
	}
}

// ExecuteRequest executes an LLM request with all protections.
// Order: 1. Health check, 2. Global backoff check, 3. Rate limits,
// 4. Cost backpressure, 5. Acquire bulkhead, 6. Execute with timeout, 7. Record outcome
func (c *LLMRequestCoordinator) ExecuteRequest(
	ctx context.Context,
	req *LLMRequest,
	provider LLMProvider,
) (*LLMResponse, error) {
	if err := c.preExecutionChecks(ctx, req); err != nil {
		return nil, err
	}

	return c.executeWithBulkhead(ctx, req, provider)
}

// preExecutionChecks performs all checks before acquiring the bulkhead.
func (c *LLMRequestCoordinator) preExecutionChecks(ctx context.Context, req *LLMRequest) error {
	if err := c.checkHealth(req.Provider); err != nil {
		return err
	}

	if err := c.checkGlobalBackoff(req.Provider); err != nil {
		return err
	}

	if err := c.checkRateLimits(ctx, req.Provider); err != nil {
		return err
	}

	return c.applyBackpressure(ctx, req.SessionID, req.TaskID)
}

// checkGlobalBackoff checks if global backoff is active for the provider.
func (c *LLMRequestCoordinator) checkGlobalBackoff(provider string) error {
	if !c.correlationEngine.ShouldAllow(provider) {
		return ErrGlobalBackoff
	}
	return nil
}

// applyBackpressure evaluates and applies backpressure based on budget usage.
func (c *LLMRequestCoordinator) applyBackpressure(ctx context.Context, sessionID, taskID string) error {
	decision := c.costBackpressure.Evaluate(sessionID, taskID)

	if decision.Reject {
		return fmt.Errorf("%w: %s", ErrBudgetExhausted, decision.Reason)
	}

	return c.waitForBackpressureDelay(ctx, decision.Delay)
}

// waitForBackpressureDelay waits for the backpressure delay if any.
func (c *LLMRequestCoordinator) waitForBackpressureDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

// executeWithBulkhead acquires bulkhead and executes the request.
func (c *LLMRequestCoordinator) executeWithBulkhead(
	ctx context.Context,
	req *LLMRequest,
	provider LLMProvider,
) (*LLMResponse, error) {
	bulk := c.getOrCreateBulkhead(req.SessionID)
	slot, err := bulk.Acquire(ctx, req.Provider, req.Model)
	if err != nil {
		return nil, fmt.Errorf("bulkhead acquisition: %w", err)
	}
	defer slot.Release()

	resp, err := c.executeWithTimeouts(ctx, req, provider)
	c.recordOutcome(req, resp, err, slot)

	return resp, err
}

// checkHealth verifies provider health before request execution.
func (c *LLMRequestCoordinator) checkHealth(provider string) error {
	monitor := c.getOrCreateHealthMonitor(provider)
	decision := monitor.Check()

	if !decision.Proceed {
		return fmt.Errorf("%w: %s", ErrProviderUnhealthy, decision.Reason)
	}
	return nil
}

// checkRateLimits enforces rate limiting with optional wait.
func (c *LLMRequestCoordinator) checkRateLimits(ctx context.Context, provider string) error {
	limiter := c.getOrCreateRateLimiter(provider)
	decision := limiter.Check()

	if decision.Allowed {
		limiter.RecordRequest()
		return nil
	}

	return c.handleRateLimitDecision(ctx, limiter, decision)
}

// handleRateLimitDecision handles a non-allowed rate limit decision.
func (c *LLMRequestCoordinator) handleRateLimitDecision(
	ctx context.Context,
	limiter *ratelimit.MultiLayerRateLimiter,
	decision ratelimit.RateLimitDecision,
) error {
	if decision.WaitTime > maxWaitForRateLimit {
		return c.buildRateLimitError(decision)
	}

	if err := c.waitForRateLimit(ctx, decision.WaitTime); err != nil {
		return err
	}

	limiter.RecordRequest()
	return nil
}

// buildRateLimitError creates an error for rate limit rejection.
func (c *LLMRequestCoordinator) buildRateLimitError(decision ratelimit.RateLimitDecision) error {
	return fmt.Errorf("%w: %s (wait: %v)", ErrRateLimited, decision.Reason, decision.WaitTime)
}

// waitForRateLimit waits for the rate limit wait time.
func (c *LLMRequestCoordinator) waitForRateLimit(ctx context.Context, waitTime time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(waitTime):
		return nil
	}
}

// executeWithTimeouts dispatches to streaming or non-streaming execution.
func (c *LLMRequestCoordinator) executeWithTimeouts(
	ctx context.Context,
	req *LLMRequest,
	provider LLMProvider,
) (*LLMResponse, error) {
	if req.Stream {
		return c.executeStreaming(ctx, req, provider)
	}
	return c.executeNonStreaming(ctx, req, provider)
}

// executeStreaming executes a streaming LLM request with timeout monitoring.
func (c *LLMRequestCoordinator) executeStreaming(
	ctx context.Context,
	req *LLMRequest,
	provider LLMProvider,
) (*LLMResponse, error) {
	monitor := timeout.NewStreamingTimeoutMonitor(ctx, c.streamTimeoutCfg)
	resp := NewStreamingLLMResponse(streamingChunkBuffer)

	go c.forwardStreamingChunks(monitor, provider.Stream(monitor.Context(), req), resp.Chunks)

	return c.waitForStreamCompletion(monitor, resp)
}

// forwardStreamingChunks forwards chunks from provider to response channel.
func (c *LLMRequestCoordinator) forwardStreamingChunks(
	monitor *timeout.StreamingTimeoutMonitor,
	sourceCh <-chan string,
	destCh chan string,
) {
	defer close(destCh)
	defer monitor.Done()

	for chunk := range sourceCh {
		monitor.RecordToken()
		destCh <- chunk
	}
}

// waitForStreamCompletion waits for streaming to complete or error.
func (c *LLMRequestCoordinator) waitForStreamCompletion(
	monitor *timeout.StreamingTimeoutMonitor,
	resp *LLMResponse,
) (*LLMResponse, error) {
	select {
	case err := <-monitor.Errors():
		return nil, err
	case <-monitor.Context().Done():
		return resp, nil
	}
}

// executeNonStreaming executes a non-streaming LLM request with timeout.
func (c *LLMRequestCoordinator) executeNonStreaming(
	ctx context.Context,
	req *LLMRequest,
	provider LLMProvider,
) (*LLMResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.streamTimeoutCfg.TotalTimeout)
	defer cancel()

	return provider.Complete(reqCtx, req)
}

// recordOutcome records the request outcome to all relevant components.
func (c *LLMRequestCoordinator) recordOutcome(
	req *LLMRequest,
	resp *LLMResponse,
	err error,
	slot *bulkhead.HierarchicalSlot,
) {
	if err != nil {
		c.recordFailure(req, err, slot)
		return
	}
	c.recordSuccess(req, resp, slot)
}

// recordFailure records a failed request to all components.
func (c *LLMRequestCoordinator) recordFailure(
	req *LLMRequest,
	err error,
	slot *bulkhead.HierarchicalSlot,
) {
	slot.RecordFailure()
	c.getOrCreateHealthMonitor(req.Provider).RecordFailure(err)
	c.correlationEngine.RecordFailure(req.Provider, req.SessionID, err)
	c.checkAndRecord429(req.Provider, err)
}

// checkAndRecord429 checks for rate limit errors and records them.
func (c *LLMRequestCoordinator) checkAndRecord429(provider string, err error) {
	if !isRateLimitError(err) {
		return
	}
	retryAfter := parseRetryAfter(err)
	c.getOrCreateRateLimiter(provider).Record429(retryAfter)
}

// recordSuccess records a successful request to all components.
func (c *LLMRequestCoordinator) recordSuccess(
	req *LLMRequest,
	resp *LLMResponse,
	slot *bulkhead.HierarchicalSlot,
) {
	slot.RecordSuccess()
	c.getOrCreateHealthMonitor(req.Provider).RecordSuccess(resp.Latency)
	c.getOrCreateRateLimiter(req.Provider).RecordSuccess()
}

// getOrCreateBulkhead returns existing or creates new bulkhead for session.
func (c *LLMRequestCoordinator) getOrCreateBulkhead(sessionID string) *bulkhead.HierarchicalBulkhead {
	if v, ok := c.sessionBulkheads.Load(sessionID); ok {
		return v.(*bulkhead.HierarchicalBulkhead)
	}

	bulk := bulkhead.NewHierarchicalBulkhead(sessionID, c.config.BulkheadConfigs)
	actual, _ := c.sessionBulkheads.LoadOrStore(sessionID, bulk)
	return actual.(*bulkhead.HierarchicalBulkhead)
}

// getOrCreateRateLimiter returns existing or creates new rate limiter for provider.
func (c *LLMRequestCoordinator) getOrCreateRateLimiter(provider string) *ratelimit.MultiLayerRateLimiter {
	if v, ok := c.rateLimiters.Load(provider); ok {
		return v.(*ratelimit.MultiLayerRateLimiter)
	}

	limiter := ratelimit.NewMultiLayerRateLimiter(provider, c.config.RateLimitConfig)
	actual, _ := c.rateLimiters.LoadOrStore(provider, limiter)
	return actual.(*ratelimit.MultiLayerRateLimiter)
}

// getOrCreateHealthMonitor returns existing or creates new health monitor for provider.
func (c *LLMRequestCoordinator) getOrCreateHealthMonitor(provider string) *health.HybridHealthMonitor {
	if v, ok := c.healthMonitors.Load(provider); ok {
		return v.(*health.HybridHealthMonitor)
	}

	return c.createHealthMonitor(provider)
}

// createHealthMonitor creates a new health monitor for a provider.
func (c *LLMRequestCoordinator) createHealthMonitor(provider string) *health.HybridHealthMonitor {
	probeFunc := func(ctx context.Context) error {
		return nil // Lightweight probe - could be provider-specific
	}

	monitor := health.NewHybridHealthMonitor(provider, c.config.HealthConfig, probeFunc)
	actual, _ := c.healthMonitors.LoadOrStore(provider, monitor)
	return actual.(*health.HybridHealthMonitor)
}

// Stats returns coordinator-wide statistics.
func (c *LLMRequestCoordinator) Stats() CoordinatorStats {
	stats := NewCoordinatorStats()
	c.collectSessionStats(&stats)
	c.collectProviderStats(&stats)
	return stats
}

// collectSessionStats populates session statistics.
func (c *LLMRequestCoordinator) collectSessionStats(stats *CoordinatorStats) {
	c.sessionBulkheads.Range(func(key, value any) bool {
		stats.Sessions[key.(string)] = value.(*bulkhead.HierarchicalBulkhead).Stats()
		return true
	})
}

// collectProviderStats populates provider statistics.
func (c *LLMRequestCoordinator) collectProviderStats(stats *CoordinatorStats) {
	c.healthMonitors.Range(func(key, value any) bool {
		provider := key.(string)
		stats.Providers[provider] = ProviderStats{
			HealthScore: value.(*health.HybridHealthMonitor).Score(),
		}
		return true
	})
}

// Stop stops all background goroutines.
func (c *LLMRequestCoordinator) Stop() {
	c.stopHealthMonitors()
	c.stopBulkheads()
}

// stopHealthMonitors stops all health monitor goroutines.
func (c *LLMRequestCoordinator) stopHealthMonitors() {
	c.healthMonitors.Range(func(key, value any) bool {
		value.(*health.HybridHealthMonitor).Stop()
		return true
	})
}

// stopBulkheads stops all bulkhead goroutines.
func (c *LLMRequestCoordinator) stopBulkheads() {
	c.sessionBulkheads.Range(func(key, value any) bool {
		value.(*bulkhead.HierarchicalBulkhead).Stop()
		return true
	})
}

// isRateLimitError checks if the error indicates rate limiting.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "429") || strings.Contains(errStr, "rate")
}

// parseRetryAfter extracts retry-after duration from error.
func parseRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}
	// Simple heuristic - could be enhanced with actual header parsing
	errStr := err.Error()
	if strings.Contains(errStr, "retry") {
		return 5 * time.Second // Default retry delay
	}
	return 0
}
