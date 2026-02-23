package network

import (
	"sync"
	"time"
)

// RateLimiterConfig configures a token bucket rate limiter.
type RateLimiterConfig struct {
	Rate     float64       // Tokens per second
	Burst    int64         // Maximum token bucket capacity
	Interval time.Duration // Refill interval (derived from Rate if zero)
}

// DefaultRateLimiterConfig returns sensible defaults.
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		Rate:  defaultRateLimitRate,
		Burst: defaultRateLimitBurst,
	}
}

const (
	defaultRateLimitRate  = 100.0 // tokens/sec
	defaultRateLimitBurst = 200   // max burst
)

// RateLimiter implements a token bucket rate limiter for per-container
// publish rate limiting. Thread-safe.
type RateLimiter struct {
	mu     sync.Mutex
	tokens float64
	config RateLimiterConfig
	lastRefill time.Time
}

// NewRateLimiter creates a rate limiter starting with a full bucket.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	if cfg.Rate <= 0 {
		cfg.Rate = defaultRateLimitRate
	}
	if cfg.Burst <= 0 {
		cfg.Burst = defaultRateLimitBurst
	}
	return &RateLimiter{
		tokens:     float64(cfg.Burst),
		config:     cfg,
		lastRefill: time.Now(),
	}
}

// Allow reports whether one token can be consumed. If allowed, the token
// is consumed. Returns false if the bucket is empty.
func (rl *RateLimiter) Allow() bool {
	return rl.AllowN(1)
}

// AllowN reports whether n tokens can be consumed. If allowed, all n
// tokens are consumed atomically. Returns false if insufficient tokens.
func (rl *RateLimiter) AllowN(n int64) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.refillLocked()
	needed := float64(n)
	if rl.tokens < needed {
		return false
	}
	rl.tokens -= needed
	return true
}

func (rl *RateLimiter) refillLocked() {
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.tokens += elapsed * rl.config.Rate
	if rl.tokens > float64(rl.config.Burst) {
		rl.tokens = float64(rl.config.Burst)
	}
	rl.lastRefill = now
}

// Available returns the current number of available tokens.
func (rl *RateLimiter) Available() float64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.refillLocked()
	return rl.tokens
}

// Rate returns the configured rate.
func (rl *RateLimiter) Rate() float64 {
	return rl.config.Rate
}

// Burst returns the configured burst capacity.
func (rl *RateLimiter) Burst() int64 {
	return rl.config.Burst
}
