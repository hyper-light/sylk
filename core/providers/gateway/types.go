// Package gateway provides an egress rate-limiting gateway that consolidates
// cross-agent LLM request scheduling through a single coordination point per
// credential set.
package gateway

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/adalundhe/sylk/core/llm/ratelimit"
)

// RequestPriority determines scheduling order when concurrency slots are full.
// Higher values are served first, subject to aging.
type RequestPriority int

const (
	PriorityBackground      RequestPriority = 20
	PriorityValidation      RequestPriority = 40
	PriorityExecution       RequestPriority = 60
	PriorityPlanning        RequestPriority = 80
	PriorityUserInteractive RequestPriority = 100
)

// Sentinel errors returned by gateway operations. The scheduler never
// rejects on count — admission only fails when a caller's wait budget
// elapses (ErrGatewayTimeout / ctx.Err()) or the gateway is shutting
// down (ErrGatewayClosed).
var (
	ErrGatewayTimeout = errors.New("gateway: max wait time exceeded")
	ErrGatewayClosed  = errors.New("gateway: closed")
)

// GatewayConfig configures a ProviderGateway instance. The scheduler
// itself is unbounded by count — admission only fails when a waiter's
// MaxWaitTime / context deadline expires. MaxQueueSize is an advisory
// value surfaced to autoscalers (the replica pool reads it via
// CapacitySnapshot) describing how many waiters the gateway can
// realistically drain within MaxWaitTime, derived from
// MaxConcurrentRequests × (MaxWaitTime / TypicalServiceTime). It is
// not enforced as a hard cap.
type GatewayConfig struct {
	Name                  string
	MaxConcurrentRequests int
	RateLimit             ratelimit.MultiLayerConfig
	AgingRate             float64
	MaxWaitTime           time.Duration

	// TypicalServiceTime anchors the derivation of the advisory queue
	// depth (see MaxQueueSize). Use a conservative provider-specific
	// p50 of stream-completion latency. Required if MaxQueueSize is
	// zero — the constructor derives it.
	TypicalServiceTime time.Duration

	// MaxQueueSize is advisory. If zero, derived at NewProviderGateway
	// from MaxConcurrentRequests, MaxWaitTime, and TypicalServiceTime.
	// Setting a non-zero value overrides the derivation (for tests).
	MaxQueueSize int
}

// DeriveQueueAdvisory returns the advisory queue depth a gateway can
// drain within maxWait at the given concurrency and typical service
// latency. Returns 0 when service time is non-positive (caller should
// apply its own floor). Used at gateway construction and when callers
// need to recompute the advisory after observing live service times.
func DeriveQueueAdvisory(maxInflight int, maxWait, typicalService time.Duration) int {
	if typicalService <= 0 || maxWait <= 0 || maxInflight <= 0 {
		return 0
	}
	rounds := int(maxWait / typicalService)
	if rounds < 1 {
		rounds = 1
	}
	return maxInflight * rounds
}

// CapacitySnapshot captures the static concurrency and queueing limits for a
// provider gateway at a point in time.
type CapacitySnapshot struct {
	Name                  string
	MaxConcurrentRequests int
	MaxQueueSize          int
	MaxWaitTime           time.Duration
}

// GatewayMetrics tracks gateway operation counts using atomic counters.
type GatewayMetrics struct {
	Admitted    atomic.Int64
	Rejected    atomic.Int64
	Queued      atomic.Int64
	Completed   atomic.Int64
	RateLimited atomic.Int64
	Errors429   atomic.Int64
	Inflight    atomic.Int64
	TotalWaitNs atomic.Int64
}

// MetricsSnapshot is a point-in-time copy of gateway metrics.
type MetricsSnapshot struct {
	Admitted    int64
	Rejected    int64
	Queued      int64
	Completed   int64
	RateLimited int64
	Errors429   int64
	Inflight    int64
	TotalWaitNs int64
}

// Snapshot returns a point-in-time copy of all metric counters.
func (m *GatewayMetrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Admitted:    m.Admitted.Load(),
		Rejected:    m.Rejected.Load(),
		Queued:      m.Queued.Load(),
		Completed:   m.Completed.Load(),
		RateLimited: m.RateLimited.Load(),
		Errors429:   m.Errors429.Load(),
		Inflight:    m.Inflight.Load(),
		TotalWaitNs: m.TotalWaitNs.Load(),
	}
}

// Per-provider TypicalServiceTime anchors. Set to a conservative p50 of
// observed end-to-end stream latency for each provider. The advisory
// queue depth (MaxQueueSize) is derived from these via
// DeriveQueueAdvisory(MaxConcurrentRequests, MaxWaitTime, anchor) and
// is not hand-picked.
const (
	googleOAuthTypicalService = 8 * time.Second
	googleAPIKeyTypicalService = 12 * time.Second
	anthropicTypicalService   = 8 * time.Second
	openaiTypicalService      = 6 * time.Second
)

// DefaultGoogleOAuthConfig returns gateway defaults for Google OAuth (60 RPM tier).
func DefaultGoogleOAuthConfig() GatewayConfig {
	cfg := GatewayConfig{
		Name:                  "google-oauth",
		MaxConcurrentRequests: 4,
		RateLimit: ratelimit.MultiLayerConfig{
			TokenBucket: ratelimit.TokenBucketConfig{
				Capacity:   10,
				RefillRate: 1.0,
			},
			SlidingWindow: ratelimit.SlidingWindowConfig{
				WindowSize:  60 * time.Second,
				MaxRequests: 55,
			},
			Adaptive: ratelimit.Adaptive429Config{
				InitialLimit: 55,
				DecayFactor:  0.95,
				GrowthFactor: 1.05,
				MinLimit:     5,
				MaxLimit:     60,
			},
		},
		AgingRate:          2.0,
		MaxWaitTime:        90 * time.Second,
		TypicalServiceTime: googleOAuthTypicalService,
	}
	cfg.MaxQueueSize = DeriveQueueAdvisory(cfg.MaxConcurrentRequests, cfg.MaxWaitTime, cfg.TypicalServiceTime)
	return cfg
}

// DefaultGoogleAPIKeyConfig returns gateway defaults for Google API key (10 RPM tier).
func DefaultGoogleAPIKeyConfig() GatewayConfig {
	cfg := GatewayConfig{
		Name:                  "google-apikey",
		MaxConcurrentRequests: 2,
		RateLimit: ratelimit.MultiLayerConfig{
			TokenBucket: ratelimit.TokenBucketConfig{
				Capacity:   3,
				RefillRate: 0.15,
			},
			SlidingWindow: ratelimit.SlidingWindowConfig{
				WindowSize:  60 * time.Second,
				MaxRequests: 9,
			},
			Adaptive: ratelimit.Adaptive429Config{
				InitialLimit: 9,
				DecayFactor:  0.95,
				GrowthFactor: 1.05,
				MinLimit:     2,
				MaxLimit:     10,
			},
		},
		AgingRate:          5.0,
		MaxWaitTime:        120 * time.Second,
		TypicalServiceTime: googleAPIKeyTypicalService,
	}
	cfg.MaxQueueSize = DeriveQueueAdvisory(cfg.MaxConcurrentRequests, cfg.MaxWaitTime, cfg.TypicalServiceTime)
	return cfg
}

// DefaultAnthropicConfig returns gateway defaults for Anthropic.
func DefaultAnthropicConfig() GatewayConfig {
	cfg := GatewayConfig{
		Name:                  "anthropic",
		MaxConcurrentRequests: 4,
		RateLimit: ratelimit.MultiLayerConfig{
			TokenBucket: ratelimit.TokenBucketConfig{
				Capacity:   10,
				RefillRate: 1.0,
			},
			SlidingWindow: ratelimit.SlidingWindowConfig{
				WindowSize:  60 * time.Second,
				MaxRequests: 50,
			},
			Adaptive: ratelimit.Adaptive429Config{
				InitialLimit: 50,
				DecayFactor:  0.95,
				GrowthFactor: 1.05,
				MinLimit:     5,
				MaxLimit:     200,
			},
		},
		AgingRate:          2.0,
		MaxWaitTime:        90 * time.Second,
		TypicalServiceTime: anthropicTypicalService,
	}
	cfg.MaxQueueSize = DeriveQueueAdvisory(cfg.MaxConcurrentRequests, cfg.MaxWaitTime, cfg.TypicalServiceTime)
	return cfg
}

// DefaultOpenAIConfig returns gateway defaults for OpenAI.
func DefaultOpenAIConfig() GatewayConfig {
	cfg := GatewayConfig{
		Name:                  "openai",
		MaxConcurrentRequests: 4,
		RateLimit: ratelimit.MultiLayerConfig{
			TokenBucket: ratelimit.TokenBucketConfig{
				Capacity:   10,
				RefillRate: 1.0,
			},
			SlidingWindow: ratelimit.SlidingWindowConfig{
				WindowSize:  60 * time.Second,
				MaxRequests: 50,
			},
			Adaptive: ratelimit.Adaptive429Config{
				InitialLimit: 50,
				DecayFactor:  0.95,
				GrowthFactor: 1.05,
				MinLimit:     5,
				MaxLimit:     200,
			},
		},
		AgingRate:          2.0,
		MaxWaitTime:        90 * time.Second,
		TypicalServiceTime: openaiTypicalService,
	}
	cfg.MaxQueueSize = DeriveQueueAdvisory(cfg.MaxConcurrentRequests, cfg.MaxWaitTime, cfg.TypicalServiceTime)
	return cfg
}
