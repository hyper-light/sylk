// Package ratelimit provides multi-layer rate limiting for LLM providers.
package ratelimit

import "time"

// RateLimitDecision represents the decision from a single limiter.
type RateLimitDecision struct {
	Allowed    bool
	WaitTime   time.Duration
	Reason     string
	Limiter    string
	Confidence float64 // 0-1, how confident is this decision
}

// TokenBucketConfig configures the token bucket limiter.
type TokenBucketConfig struct {
	Capacity   float64 // Maximum tokens
	RefillRate float64 // Tokens per second
}

// SlidingWindowConfig configures the sliding window limiter.
type SlidingWindowConfig struct {
	WindowSize  time.Duration
	MaxRequests int
}

// Adaptive429Config configures the adaptive 429 limiter.
type Adaptive429Config struct {
	InitialLimit float64
	DecayFactor  float64 // Multiply limit by this on 429 (< 1)
	GrowthFactor float64 // Multiply limit by this on success (> 1)
	MinLimit     float64
	MaxLimit     float64
}

// MultiLayerConfig combines all limiter configs.
type MultiLayerConfig struct {
	TokenBucket   TokenBucketConfig
	SlidingWindow SlidingWindowConfig
	Adaptive      Adaptive429Config
}

// DefaultMultiLayerConfig returns production defaults.
func DefaultMultiLayerConfig() MultiLayerConfig {
	return MultiLayerConfig{
		TokenBucket: TokenBucketConfig{
			Capacity:   100,
			RefillRate: 10,
		},
		SlidingWindow: SlidingWindowConfig{
			WindowSize:  60 * time.Second,
			MaxRequests: 60,
		},
		Adaptive: Adaptive429Config{
			InitialLimit: 50,
			DecayFactor:  0.95,
			GrowthFactor: 1.05,
			MinLimit:     5,
			MaxLimit:     200,
		},
	}
}

// RateLimiter interface for all limiter implementations.
type RateLimiter interface {
	Check() RateLimitDecision
}
