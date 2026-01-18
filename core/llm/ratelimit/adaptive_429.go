// Package ratelimit provides multi-layer rate limiting for LLM providers.
package ratelimit

import (
	"sync"
	"time"
)

const (
	// backoffWindow is the duration after a 429 during which requests are blocked.
	backoffWindow = 5 * time.Second

	// successRunClearThreshold is the number of consecutive successes needed to clear last429.
	successRunClearThreshold = 5

	// successRunGrowthThreshold is the number of consecutive successes needed to grow the limit.
	successRunGrowthThreshold = 10

	// initialConfidence is the starting confidence level.
	initialConfidence = 0.5

	// confidenceIncrement is added to confidence on each 429.
	confidenceIncrement = 0.1

	// maxConfidence is the maximum confidence value.
	maxConfidence = 1.0
)

// Adaptive429Limiter learns from actual 429 responses.
type Adaptive429Limiter struct {
	config Adaptive429Config

	currentLimit float64   // Inferred requests/minute we can make
	confidence   float64   // How confident are we in this limit
	last429      time.Time // Last 429 received
	successRun   int       // Consecutive successes

	mu sync.RWMutex
}

// NewAdaptive429Limiter starts at InitialLimit with 0.5 confidence.
func NewAdaptive429Limiter(config Adaptive429Config) *Adaptive429Limiter {
	return &Adaptive429Limiter{
		config:       config,
		currentLimit: config.InitialLimit,
		confidence:   initialConfidence,
	}
}

// Check returns not-allowed if recent 429 (within 5s).
// WaitTime = 5s - elapsed when backing off.
// Limiter = "adaptive_429".
func (al *Adaptive429Limiter) Check() RateLimitDecision {
	al.mu.RLock()
	defer al.mu.RUnlock()

	return al.checkBackoff()
}

// checkBackoff determines if we're in backoff and returns the appropriate decision.
func (al *Adaptive429Limiter) checkBackoff() RateLimitDecision {
	if al.last429.IsZero() {
		return al.allowedDecision()
	}

	elapsed := time.Since(al.last429)
	if elapsed >= backoffWindow {
		return al.allowedDecision()
	}

	return al.backoffDecision(backoffWindow - elapsed)
}

// allowedDecision returns an allowed rate limit decision.
func (al *Adaptive429Limiter) allowedDecision() RateLimitDecision {
	return RateLimitDecision{
		Allowed:    true,
		Limiter:    "adaptive_429",
		Confidence: al.confidence,
	}
}

// backoffDecision returns a denied rate limit decision with the remaining wait time.
func (al *Adaptive429Limiter) backoffDecision(waitTime time.Duration) RateLimitDecision {
	return RateLimitDecision{
		Allowed:    false,
		WaitTime:   waitTime,
		Reason:     "backing off after 429",
		Limiter:    "adaptive_429",
		Confidence: al.confidence,
	}
}

// Record429 decreases limit by DecayFactor, increases confidence by 0.1 (max 1.0).
func (al *Adaptive429Limiter) Record429(retryAfter time.Duration) {
	al.mu.Lock()
	defer al.mu.Unlock()

	al.last429 = time.Now()
	al.successRun = 0
	al.applyDecay()
	al.increaseConfidence()
}

// applyDecay reduces the current limit by the decay factor, clamped to MinLimit.
func (al *Adaptive429Limiter) applyDecay() {
	al.currentLimit *= al.config.DecayFactor
	al.clampLimit()
}

// increaseConfidence increases confidence by the increment, capped at max.
func (al *Adaptive429Limiter) increaseConfidence() {
	al.confidence += confidenceIncrement
	if al.confidence > maxConfidence {
		al.confidence = maxConfidence
	}
}

// RecordSuccess increments successRun.
// After 10 consecutive successes: increase limit by GrowthFactor.
// After 5 consecutive successes: clear last429.
// Limit clamped between MinLimit and MaxLimit.
func (al *Adaptive429Limiter) RecordSuccess() {
	al.mu.Lock()
	defer al.mu.Unlock()

	al.successRun++
	al.handleSuccessThresholds()
}

// handleSuccessThresholds applies growth and clears backoff based on success count.
func (al *Adaptive429Limiter) handleSuccessThresholds() {
	if al.successRun >= successRunGrowthThreshold {
		al.applyGrowth()
		al.successRun = 0
	}

	if al.successRun >= successRunClearThreshold || al.successRun == 0 {
		al.last429 = time.Time{}
	}
}

// applyGrowth increases the current limit by the growth factor, clamped to MaxLimit.
func (al *Adaptive429Limiter) applyGrowth() {
	al.currentLimit *= al.config.GrowthFactor
	al.clampLimit()
}

// clampLimit ensures currentLimit stays within MinLimit and MaxLimit.
func (al *Adaptive429Limiter) clampLimit() {
	if al.currentLimit < al.config.MinLimit {
		al.currentLimit = al.config.MinLimit
	}
	if al.currentLimit > al.config.MaxLimit {
		al.currentLimit = al.config.MaxLimit
	}
}

// CurrentLimit returns the current inferred limit.
func (al *Adaptive429Limiter) CurrentLimit() float64 {
	al.mu.RLock()
	defer al.mu.RUnlock()

	return al.currentLimit
}
