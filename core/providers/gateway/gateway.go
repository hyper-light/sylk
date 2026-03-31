package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/llm/ratelimit"
	"github.com/adalundhe/sylk/core/providers"
)

// ProviderGateway is the central coordinator for egress rate limiting.
// One gateway is created per credential set (e.g. one for Google OAuth,
// one for Anthropic). It owns the shared rate limiter, priority scheduler,
// and concurrency gate.
type ProviderGateway struct {
	config      GatewayConfig
	limiter     *ratelimit.MultiLayerRateLimiter
	scheduler   *Scheduler
	metrics     GatewayMetrics
	inflight    atomic.Int64
	maxInflight int
	closed      atomic.Bool
	slotMu      sync.Mutex
	logger      *slog.Logger
	eventHook   providers.LLMProviderEventHook
}

// NewProviderGateway creates a gateway with the given configuration.
func NewProviderGateway(config GatewayConfig, logger *slog.Logger) *ProviderGateway {
	if logger == nil {
		logger = slog.Default()
	}
	return &ProviderGateway{
		config:      config,
		limiter:     ratelimit.NewMultiLayerRateLimiter(config.Name, config.RateLimit),
		scheduler:   NewScheduler(config.MaxQueueSize, config.AgingRate),
		maxInflight: config.MaxConcurrentRequests,
		logger:      logger,
	}
}

// Admit performs the full admission sequence: rate-limit check, token
// consumption, and concurrency slot acquisition. Returns nil when the
// request is admitted and may proceed, or an error if blocked.
func (g *ProviderGateway) Admit(ctx context.Context, priority RequestPriority) error {
	if g.closed.Load() {
		return ErrGatewayClosed
	}

	// Phase 1: Wait for rate limiter clearance.
	if err := g.waitForRateLimit(ctx); err != nil {
		return err
	}

	// Phase 2: Consume a rate-limit token + sliding window record.
	g.limiter.RecordRequest()

	// Phase 3: Acquire a concurrency slot (direct or queued).
	if err := g.acquireSlot(ctx, priority); err != nil {
		return err
	}

	g.metrics.Admitted.Add(1)
	g.metrics.Inflight.Add(1)
	return nil
}

// Release returns a concurrency slot after request completion and records
// the outcome to the adaptive rate limiter.
func (g *ProviderGateway) Release(err error) {
	g.inflight.Add(-1)
	g.metrics.Inflight.Add(-1)
	g.metrics.Completed.Add(1)

	if err == nil {
		g.limiter.RecordSuccess()
	}

	// Signal next queued waiter now that a slot is free.
	g.scheduler.SignalNext()
}

// Record429 notifies the adaptive rate limiter about a 429 response.
func (g *ProviderGateway) Record429(retryAfter time.Duration) {
	g.metrics.Errors429.Add(1)
	g.limiter.Record429(retryAfter)
}

// WrapProvider creates a GatewayProvider that transparently proxies the
// inner provider through this gateway's admission control.
func (g *ProviderGateway) WrapProvider(inner providers.ProviderAdapter, priority RequestPriority) *GatewayProvider {
	return &GatewayProvider{
		inner:    inner,
		gateway:  g,
		priority: priority,
	}
}

// SetEventHook wires an LLM event hook that receives usage telemetry for
// every request that flows through this gateway. Safe to call before any
// provider is wrapped. Nil resets to no-op.
func (g *ProviderGateway) SetEventHook(hook providers.LLMProviderEventHook) {
	g.eventHook = hook
}

// Name returns the gateway's configured name (e.g. "google-oauth", "anthropic").
func (g *ProviderGateway) Name() string {
	return g.config.Name
}

// Metrics returns a point-in-time snapshot of gateway counters.
func (g *ProviderGateway) Metrics() MetricsSnapshot {
	return g.metrics.Snapshot()
}

// CapacitySnapshot returns the gateway's current static admission limits.
func (g *ProviderGateway) CapacitySnapshot() CapacitySnapshot {
	return CapacitySnapshot{
		Name:                  g.config.Name,
		MaxConcurrentRequests: g.config.MaxConcurrentRequests,
		MaxQueueSize:          g.config.MaxQueueSize,
		MaxWaitTime:           g.config.MaxWaitTime,
	}
}

// Stop closes the gateway, signaling all queued waiters.
func (g *ProviderGateway) Stop() {
	if g.closed.Swap(true) {
		return
	}
	g.scheduler.Close()
	g.logger.Info("gateway stopped", "name", g.config.Name)
}

// waitForRateLimit checks the multi-layer rate limiter and sleeps when denied,
// bounded by MaxWaitTime and context cancellation.
func (g *ProviderGateway) waitForRateLimit(ctx context.Context) error {
	deadline := time.Now().Add(g.config.MaxWaitTime)

	for {
		decision := g.limiter.Check()
		if decision.Allowed {
			return nil
		}

		g.metrics.RateLimited.Add(1)

		wait := decision.WaitTime
		remaining := time.Until(deadline)
		if remaining <= 0 {
			g.metrics.Rejected.Add(1)
			return ErrGatewayTimeout
		}
		if wait > remaining {
			wait = remaining
		}

		g.logger.Debug("rate limited, waiting",
			"gateway", g.config.Name,
			"wait", wait,
			"limiter", decision.Limiter,
		)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			g.metrics.Rejected.Add(1)
			return ctx.Err()
		case <-timer.C:
			// Re-check after waiting.
		}
	}
}

// acquireSlot attempts to take a concurrency slot directly (fast path) or
// queues via the priority scheduler (slow path).
func (g *ProviderGateway) acquireSlot(ctx context.Context, priority RequestPriority) error {
	if g.tryAcquireDirectSlot() {
		return nil
	}

	// Slow path: queue and wait.
	g.metrics.Queued.Add(1)
	g.logger.Debug("concurrency full, queuing",
		"gateway", g.config.Name,
		"priority", priority,
		"inflight", g.inflight.Load(),
	)

	if err := g.scheduler.WaitForSlot(ctx, priority, g.config.MaxWaitTime); err != nil {
		g.metrics.Rejected.Add(1)
		return err
	}

	// After being signaled, claim the slot.
	g.inflight.Add(1)
	return nil
}

// tryAcquireDirectSlot atomically claims a slot if one is available.
func (g *ProviderGateway) tryAcquireDirectSlot() bool {
	g.slotMu.Lock()
	defer g.slotMu.Unlock()

	current := g.inflight.Load()
	if current >= int64(g.maxInflight) {
		return false
	}
	g.inflight.Add(1)
	return true
}

// is429Error inspects err for a ProviderError with StatusCode 429,
// returning the RetryAfter duration and whether a 429 was detected.
func is429Error(err error) (time.Duration, bool) {
	var pe *providers.ProviderError
	if errors.As(err, &pe) && pe.StatusCode == http.StatusTooManyRequests {
		return pe.RetryAfter, true
	}
	return 0, false
}
