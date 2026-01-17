package errors

import (
	"context"
	"errors"
	"time"
)

// RetryPolicy defines the retry behavior for a specific error tier.
type RetryPolicy struct {
	// MaxAttempts is the maximum number of retry attempts (0 means no retry).
	MaxAttempts int `yaml:"max_attempts"`

	// InitialDelay is the starting backoff duration.
	InitialDelay time.Duration `yaml:"initial_delay"`

	// MaxDelay is the maximum backoff duration.
	MaxDelay time.Duration `yaml:"max_delay"`

	// Multiplier is the backoff multiplier (default: 2.0).
	Multiplier float64 `yaml:"multiplier"`

	// UseRetryAfter indicates whether to respect Retry-After header for RateLimit errors.
	UseRetryAfter bool `yaml:"use_retry_after"`

	// JitterPercent is the jitter percentage (default: 0.1 for 10%).
	JitterPercent float64 `yaml:"jitter_percent"`
}

// DefaultRetryPolicies returns the default retry policies for each error tier.
func DefaultRetryPolicies() map[ErrorTier]*RetryPolicy {
	return map[ErrorTier]*RetryPolicy{
		TierTransient:         defaultTransientPolicy(),
		TierExternalRateLimit: defaultRateLimitPolicy(),
		TierExternalDegrading: defaultDegradingPolicy(),
		TierPermanent:         defaultNoRetryPolicy(),
		TierUserFixable:       defaultNoRetryPolicy(),
	}
}

// defaultTransientPolicy returns the policy for transient errors.
func defaultTransientPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:   5,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      5 * time.Second,
		Multiplier:    2.0,
		UseRetryAfter: false,
		JitterPercent: 0.1,
	}
}

// defaultRateLimitPolicy returns the policy for rate limit errors.
func defaultRateLimitPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:   10,
		InitialDelay:  1 * time.Second,
		MaxDelay:      60 * time.Second,
		Multiplier:    2.0,
		UseRetryAfter: true,
		JitterPercent: 0.1,
	}
}

// defaultDegradingPolicy returns the policy for degrading service errors.
func defaultDegradingPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:   3,
		InitialDelay:  5 * time.Second,
		MaxDelay:      30 * time.Second,
		Multiplier:    2.0,
		UseRetryAfter: false,
		JitterPercent: 0.1,
	}
}

// defaultNoRetryPolicy returns a policy with no retries.
func defaultNoRetryPolicy() *RetryPolicy {
	return &RetryPolicy{
		MaxAttempts:   0,
		InitialDelay:  0,
		MaxDelay:      0,
		Multiplier:    0,
		UseRetryAfter: false,
		JitterPercent: 0,
	}
}

// GetRetryPolicy returns the retry policy for a given error tier.
func GetRetryPolicy(tier ErrorTier) *RetryPolicy {
	policies := DefaultRetryPolicies()
	if policy, ok := policies[tier]; ok {
		return policy
	}
	return defaultNoRetryPolicy()
}

// RetryExecutor executes operations with retry logic based on error tiers.
type RetryExecutor struct {
	policies map[ErrorTier]*RetryPolicy
}

// NewRetryExecutor creates a new RetryExecutor with the given policies.
func NewRetryExecutor(policies map[ErrorTier]*RetryPolicy) *RetryExecutor {
	if policies == nil {
		policies = DefaultRetryPolicies()
	}
	return &RetryExecutor{policies: policies}
}

// Execute runs the given function with retry logic based on the error tier.
// Returns the last error if all attempts fail.
func (e *RetryExecutor) Execute(ctx context.Context, tier ErrorTier, fn func() error) error {
	policy := e.getPolicy(tier)
	if policy.MaxAttempts <= 0 {
		return fn()
	}
	return e.executeWithRetry(ctx, policy, fn)
}

// getPolicy returns the policy for the given tier or a no-retry policy.
func (e *RetryExecutor) getPolicy(tier ErrorTier) *RetryPolicy {
	if policy, ok := e.policies[tier]; ok {
		return policy
	}
	return defaultNoRetryPolicy()
}

// executeWithRetry performs the retry loop.
func (e *RetryExecutor) executeWithRetry(ctx context.Context, policy *RetryPolicy, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt <= policy.MaxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if !e.shouldRetry(attempt, policy) {
			return lastErr
		}

		delay := e.computeDelay(lastErr, attempt, policy)
		if err := waitBeforeRetry(ctx, delay); err != nil {
			return lastErr
		}
	}

	return lastErr
}

// shouldRetry determines if another attempt should be made.
func (e *RetryExecutor) shouldRetry(attempt int, policy *RetryPolicy) bool {
	return attempt < policy.MaxAttempts
}

// computeDelay calculates the delay for the next retry attempt.
func (e *RetryExecutor) computeDelay(err error, attempt int, policy *RetryPolicy) time.Duration {
	if policy.UseRetryAfter {
		if retryAfter := extractRetryAfter(err); retryAfter > 0 {
			return retryAfter
		}
	}

	delay := CalculateDelay(attempt, policy)
	return AddJitter(delay, policy.JitterPercent)
}

// extractRetryAfter attempts to extract RetryAfter from a TieredError.
func extractRetryAfter(err error) time.Duration {
	var te *TieredError
	if errors.As(err, &te) {
		return te.RetryAfter
	}
	return 0
}

// waitBeforeRetry waits for the specified delay or returns if context is cancelled.
func waitBeforeRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
