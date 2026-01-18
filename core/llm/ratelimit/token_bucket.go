// Package ratelimit provides multi-layer rate limiting for LLM providers.
package ratelimit

import (
	"sync"
	"time"
)

// TokenBucketLimiter implements classic token bucket algorithm.
type TokenBucketLimiter struct {
	config TokenBucketConfig

	tokens     float64
	lastRefill time.Time

	mu sync.Mutex
}

// NewTokenBucketLimiter creates a new limiter starting with full capacity.
func NewTokenBucketLimiter(config TokenBucketConfig) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		config:     config,
		tokens:     config.Capacity,
		lastRefill: time.Now(),
	}
}

// Check returns RateLimitDecision - refills tokens first, then checks availability.
// WaitTime = tokensNeeded / RefillRate * time.Second when not allowed.
// Limiter = "token_bucket", Confidence = 1.0.
func (tb *TokenBucketLimiter) Check() RateLimitDecision {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refillLocked()

	if tb.tokens >= 1.0 {
		return RateLimitDecision{
			Allowed:    true,
			Limiter:    "token_bucket",
			Confidence: 1.0,
		}
	}

	tokensNeeded := 1.0 - tb.tokens
	waitSeconds := tokensNeeded / tb.config.RefillRate
	waitTime := time.Duration(waitSeconds * float64(time.Second))

	return RateLimitDecision{
		Allowed:    false,
		WaitTime:   waitTime,
		Reason:     "insufficient tokens",
		Limiter:    "token_bucket",
		Confidence: 1.0,
	}
}

// Consume deducts n tokens (after refill), clamping to 0.
func (tb *TokenBucketLimiter) Consume(n float64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refillLocked()

	tb.tokens -= n
	if tb.tokens < 0 {
		tb.tokens = 0
	}
}

// refillLocked adds elapsed.Seconds() * RefillRate tokens, capped at Capacity.
// Must be called with mutex held.
func (tb *TokenBucketLimiter) refillLocked() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill)
	tb.lastRefill = now

	tb.tokens += elapsed.Seconds() * tb.config.RefillRate
	if tb.tokens > tb.config.Capacity {
		tb.tokens = tb.config.Capacity
	}
}
