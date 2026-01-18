// Package ratelimit provides multi-layer rate limiting for LLM providers.
package ratelimit

import (
	"sync"
	"time"
)

// SlidingWindowLimiter implements rolling window rate limiting.
type SlidingWindowLimiter struct {
	config SlidingWindowConfig

	// Circular buffer of request timestamps
	timestamps []time.Time
	head       int
	count      int

	mu sync.Mutex
}

// NewSlidingWindowLimiter allocates circular buffer with size = MaxRequests.
func NewSlidingWindowLimiter(config SlidingWindowConfig) *SlidingWindowLimiter {
	return &SlidingWindowLimiter{
		config:     config,
		timestamps: make([]time.Time, config.MaxRequests),
		head:       0,
		count:      0,
	}
}

// Check cleans up old entries and returns a rate limit decision.
// WaitTime = oldest.Add(WindowSize).Sub(time.Now()) when at limit.
func (sw *SlidingWindowLimiter) Check() RateLimitDecision {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	sw.cleanup()

	if sw.count < sw.config.MaxRequests {
		return sw.allowedDecision()
	}

	return sw.deniedDecision()
}

// allowedDecision returns a decision allowing the request.
func (sw *SlidingWindowLimiter) allowedDecision() RateLimitDecision {
	return RateLimitDecision{
		Allowed:    true,
		WaitTime:   0,
		Reason:     "",
		Limiter:    "sliding_window",
		Confidence: 1.0,
	}
}

// deniedDecision returns a decision denying the request with wait time.
func (sw *SlidingWindowLimiter) deniedDecision() RateLimitDecision {
	oldest := sw.timestamps[sw.head]
	expiresAt := oldest.Add(sw.config.WindowSize)
	waitTime := time.Until(expiresAt)

	if waitTime < 0 {
		waitTime = 0
	}

	return RateLimitDecision{
		Allowed:    false,
		WaitTime:   waitTime,
		Reason:     "sliding window limit exceeded",
		Limiter:    "sliding_window",
		Confidence: 1.0,
	}
}

// Record adds current timestamp to the circular buffer.
func (sw *SlidingWindowLimiter) Record() {
	sw.mu.Lock()
	defer sw.mu.Unlock()

	sw.cleanup()
	sw.addTimestamp(time.Now())
}

// addTimestamp inserts a timestamp into the circular buffer.
func (sw *SlidingWindowLimiter) addTimestamp(ts time.Time) {
	if sw.count < sw.config.MaxRequests {
		idx := (sw.head + sw.count) % sw.config.MaxRequests
		sw.timestamps[idx] = ts
		sw.count++
		return
	}

	sw.timestamps[sw.head] = ts
	sw.head = (sw.head + 1) % sw.config.MaxRequests
}

// cleanup removes entries older than WindowSize.
func (sw *SlidingWindowLimiter) cleanup() {
	cutoff := time.Now().Add(-sw.config.WindowSize)

	for sw.count > 0 && sw.timestamps[sw.head].Before(cutoff) {
		sw.head = (sw.head + 1) % sw.config.MaxRequests
		sw.count--
	}
}
