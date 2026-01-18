// Package ratelimit provides multi-layer rate limiting for LLM providers.
package ratelimit

import (
	"sync"
	"time"
)

// MultiLayerRateLimiter combines multiple rate limiting strategies.
type MultiLayerRateLimiter struct {
	provider string

	tokenBucket   *TokenBucketLimiter
	slidingWindow *SlidingWindowLimiter
	adaptive429   *Adaptive429Limiter

	mu sync.RWMutex
}

// NewMultiLayerRateLimiter creates a rate limiter with all three strategies.
func NewMultiLayerRateLimiter(provider string, config MultiLayerConfig) *MultiLayerRateLimiter {
	return &MultiLayerRateLimiter{
		provider:      provider,
		tokenBucket:   NewTokenBucketLimiter(config.TokenBucket),
		slidingWindow: NewSlidingWindowLimiter(config.SlidingWindow),
		adaptive429:   NewAdaptive429Limiter(config.Adaptive),
	}
}

// Check returns the most restrictive decision from all limiters.
// Most restrictive = longest wait time among denied decisions.
// If all allow, return allowed decision.
func (ml *MultiLayerRateLimiter) Check() RateLimitDecision {
	ml.mu.RLock()
	defer ml.mu.RUnlock()

	decisions := ml.collectDecisions()
	return ml.selectMostRestrictive(decisions)
}

// collectDecisions gathers decisions from all three limiters.
func (ml *MultiLayerRateLimiter) collectDecisions() []RateLimitDecision {
	return []RateLimitDecision{
		ml.tokenBucket.Check(),
		ml.slidingWindow.Check(),
		ml.adaptive429.Check(),
	}
}

// selectMostRestrictive returns the denied decision with longest wait time,
// or an allowed decision if all are allowed.
func (ml *MultiLayerRateLimiter) selectMostRestrictive(decisions []RateLimitDecision) RateLimitDecision {
	var mostRestrictive *RateLimitDecision

	for i := range decisions {
		mostRestrictive = ml.compareDecisions(mostRestrictive, &decisions[i])
	}

	return ml.finalizeDecision(mostRestrictive)
}

// compareDecisions returns the more restrictive of two decisions.
func (ml *MultiLayerRateLimiter) compareDecisions(current, candidate *RateLimitDecision) *RateLimitDecision {
	if current == nil {
		return candidate
	}

	if ml.isMoreRestrictive(candidate, current) {
		return candidate
	}

	return current
}

// isMoreRestrictive returns true if candidate is more restrictive than current.
func (ml *MultiLayerRateLimiter) isMoreRestrictive(candidate, current *RateLimitDecision) bool {
	if candidate.Allowed {
		return false
	}

	return current.Allowed || candidate.WaitTime > current.WaitTime
}

// finalizeDecision returns the final decision or a default allowed decision.
func (ml *MultiLayerRateLimiter) finalizeDecision(decision *RateLimitDecision) RateLimitDecision {
	if decision == nil {
		return RateLimitDecision{
			Allowed:    true,
			Limiter:    "multi_layer",
			Confidence: 1.0,
		}
	}
	return *decision
}

// RecordRequest records a request attempt (token bucket consume + sliding window record).
func (ml *MultiLayerRateLimiter) RecordRequest() {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.tokenBucket.Consume(1)
	ml.slidingWindow.Record()
}

// Record429 records a 429 response for adaptive learning.
func (ml *MultiLayerRateLimiter) Record429(retryAfter time.Duration) {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.adaptive429.Record429(retryAfter)
}

// RecordSuccess records a successful request (helps adaptive limiter).
func (ml *MultiLayerRateLimiter) RecordSuccess() {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	ml.adaptive429.RecordSuccess()
}

// Provider returns the provider name.
func (ml *MultiLayerRateLimiter) Provider() string {
	return ml.provider
}
