package errors

import (
	"math"
	"math/rand"
	"time"
)

// CalculateDelay computes the backoff delay for a given attempt using exponential backoff.
// Formula: delay = initial * (multiplier ^ attempt), capped at max_delay.
func CalculateDelay(attempt int, policy *RetryPolicy) time.Duration {
	if policy == nil {
		return 0
	}
	return calculateExponentialDelay(attempt, policy)
}

// calculateExponentialDelay computes the raw exponential delay.
func calculateExponentialDelay(attempt int, policy *RetryPolicy) time.Duration {
	multiplier := policy.Multiplier
	if multiplier <= 0 {
		multiplier = 2.0
	}

	factor := math.Pow(multiplier, float64(attempt))
	delay := time.Duration(float64(policy.InitialDelay) * factor)

	return capDelay(delay, policy.MaxDelay)
}

// capDelay ensures the delay does not exceed the maximum.
func capDelay(delay, maxDelay time.Duration) time.Duration {
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

// AddJitter applies a random jitter to the delay to prevent thundering herd.
// The jitter is ±jitterPercent of the original delay.
func AddJitter(delay time.Duration, jitterPercent float64) time.Duration {
	if jitterPercent <= 0 {
		return delay
	}
	return applyJitter(delay, jitterPercent)
}

// applyJitter calculates and applies the random jitter offset.
func applyJitter(delay time.Duration, jitterPercent float64) time.Duration {
	jitterRange := float64(delay) * jitterPercent
	// Random value in range [-jitterRange, +jitterRange]
	offset := (rand.Float64()*2 - 1) * jitterRange
	jitteredDelay := time.Duration(float64(delay) + offset)

	return ensurePositiveDelay(jitteredDelay)
}

// ensurePositiveDelay ensures the delay is at least 1 millisecond.
func ensurePositiveDelay(delay time.Duration) time.Duration {
	if delay < time.Millisecond {
		return time.Millisecond
	}
	return delay
}
