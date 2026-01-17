package errors

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Happy Path Tests
// =============================================================================

func TestDefaultRetryPolicies_AllTiers(t *testing.T) {
	policies := DefaultRetryPolicies()

	expectedTiers := []ErrorTier{
		TierTransient,
		TierExternalRateLimit,
		TierExternalDegrading,
		TierPermanent,
		TierUserFixable,
	}

	if len(policies) != len(expectedTiers) {
		t.Errorf("DefaultRetryPolicies() returned %d policies, want %d",
			len(policies), len(expectedTiers))
	}

	for _, tier := range expectedTiers {
		if policy, ok := policies[tier]; !ok {
			t.Errorf("DefaultRetryPolicies() missing policy for tier %v", tier)
		} else if policy == nil {
			t.Errorf("DefaultRetryPolicies() has nil policy for tier %v", tier)
		}
	}
}

func TestGetRetryPolicy_ReturnsCorrectPolicy(t *testing.T) {
	tests := []struct {
		tier             ErrorTier
		expectedAttempts int
		expectedInitial  time.Duration
	}{
		{TierTransient, 5, 100 * time.Millisecond},
		{TierExternalRateLimit, 10, 1 * time.Second},
		{TierExternalDegrading, 3, 5 * time.Second},
		{TierPermanent, 0, 0},
		{TierUserFixable, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.tier.String(), func(t *testing.T) {
			policy := GetRetryPolicy(tt.tier)
			if policy.MaxAttempts != tt.expectedAttempts {
				t.Errorf("GetRetryPolicy(%v).MaxAttempts = %d, want %d",
					tt.tier, policy.MaxAttempts, tt.expectedAttempts)
			}
			if policy.InitialDelay != tt.expectedInitial {
				t.Errorf("GetRetryPolicy(%v).InitialDelay = %v, want %v",
					tt.tier, policy.InitialDelay, tt.expectedInitial)
			}
		})
	}
}

func TestRetryExecutor_SuccessOnFirstAttempt(t *testing.T) {
	executor := NewRetryExecutor(nil)
	ctx := context.Background()

	callCount := 0
	err := executor.Execute(ctx, TierTransient, func() error {
		callCount++
		return nil
	})

	if err != nil {
		t.Errorf("Execute() returned error = %v, want nil", err)
	}
	if callCount != 1 {
		t.Errorf("Function called %d times, want 1", callCount)
	}
}

func TestRetryExecutor_SuccessAfterRetries(t *testing.T) {
	// Use a custom policy with short delays for faster tests
	policies := map[ErrorTier]*RetryPolicy{
		TierTransient: {
			MaxAttempts:   5,
			InitialDelay:  1 * time.Millisecond,
			MaxDelay:      10 * time.Millisecond,
			Multiplier:    2.0,
			JitterPercent: 0,
		},
	}
	executor := NewRetryExecutor(policies)
	ctx := context.Background()

	tests := []struct {
		name             string
		succeedOnAttempt int
	}{
		{"succeed on 2nd attempt", 2},
		{"succeed on 3rd attempt", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			err := executor.Execute(ctx, TierTransient, func() error {
				callCount++
				if callCount < tt.succeedOnAttempt {
					return errors.New("temporary failure")
				}
				return nil
			})

			if err != nil {
				t.Errorf("Execute() returned error = %v, want nil", err)
			}
			if callCount != tt.succeedOnAttempt {
				t.Errorf("Function called %d times, want %d", callCount, tt.succeedOnAttempt)
			}
		})
	}
}

func TestRetryExecutor_TransientPolicy(t *testing.T) {
	policy := GetRetryPolicy(TierTransient)

	if policy.MaxAttempts != 5 {
		t.Errorf("Transient policy MaxAttempts = %d, want 5", policy.MaxAttempts)
	}
	if policy.InitialDelay != 100*time.Millisecond {
		t.Errorf("Transient policy InitialDelay = %v, want 100ms", policy.InitialDelay)
	}
	if policy.MaxDelay != 5*time.Second {
		t.Errorf("Transient policy MaxDelay = %v, want 5s", policy.MaxDelay)
	}
	if policy.Multiplier != 2.0 {
		t.Errorf("Transient policy Multiplier = %v, want 2.0", policy.Multiplier)
	}
	if policy.JitterPercent != 0.1 {
		t.Errorf("Transient policy JitterPercent = %v, want 0.1", policy.JitterPercent)
	}
}

// =============================================================================
// Negative Path Tests
// =============================================================================

func TestRetryExecutor_AllAttemptsFail(t *testing.T) {
	policies := map[ErrorTier]*RetryPolicy{
		TierTransient: {
			MaxAttempts:   3,
			InitialDelay:  1 * time.Millisecond,
			MaxDelay:      10 * time.Millisecond,
			Multiplier:    2.0,
			JitterPercent: 0,
		},
	}
	executor := NewRetryExecutor(policies)
	ctx := context.Background()

	expectedErr := errors.New("persistent failure")
	callCount := 0

	err := executor.Execute(ctx, TierTransient, func() error {
		callCount++
		return expectedErr
	})

	if err == nil {
		t.Error("Execute() returned nil, want error")
	}
	if err.Error() != expectedErr.Error() {
		t.Errorf("Execute() returned error = %v, want %v", err, expectedErr)
	}
	// Initial attempt + 3 retries = 4 total calls
	if callCount != 4 {
		t.Errorf("Function called %d times, want 4 (initial + 3 retries)", callCount)
	}
}

func TestRetryExecutor_PermanentTier_NoRetry(t *testing.T) {
	executor := NewRetryExecutor(nil)
	ctx := context.Background()

	expectedErr := errors.New("permanent failure")
	callCount := 0

	err := executor.Execute(ctx, TierPermanent, func() error {
		callCount++
		return expectedErr
	})

	if err == nil {
		t.Error("Execute() returned nil, want error")
	}
	if callCount != 1 {
		t.Errorf("Function called %d times, want 1 (no retry for permanent)", callCount)
	}
}

func TestRetryExecutor_UserFixable_NoRetry(t *testing.T) {
	executor := NewRetryExecutor(nil)
	ctx := context.Background()

	expectedErr := errors.New("user must fix this")
	callCount := 0

	err := executor.Execute(ctx, TierUserFixable, func() error {
		callCount++
		return expectedErr
	})

	if err == nil {
		t.Error("Execute() returned nil, want error")
	}
	if callCount != 1 {
		t.Errorf("Function called %d times, want 1 (no retry for user-fixable)", callCount)
	}
}

func TestRetryExecutor_ContextCanceled(t *testing.T) {
	policies := map[ErrorTier]*RetryPolicy{
		TierTransient: {
			MaxAttempts:   10,
			InitialDelay:  50 * time.Millisecond,
			MaxDelay:      1 * time.Second,
			Multiplier:    2.0,
			JitterPercent: 0,
		},
	}
	executor := NewRetryExecutor(policies)

	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0
	expectedErr := errors.New("will retry")

	// Cancel context after first failure
	err := executor.Execute(ctx, TierTransient, func() error {
		callCount++
		if callCount == 1 {
			cancel() // Cancel during first retry wait
		}
		return expectedErr
	})

	if err == nil {
		t.Error("Execute() returned nil, want error")
	}
	// Should stop after context cancellation during wait
	if callCount > 2 {
		t.Errorf("Function called %d times, should stop early due to context cancellation", callCount)
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestRetryExecutor_ZeroMaxAttempts(t *testing.T) {
	policies := map[ErrorTier]*RetryPolicy{
		TierTransient: {
			MaxAttempts:   0,
			InitialDelay:  100 * time.Millisecond,
			MaxDelay:      1 * time.Second,
			Multiplier:    2.0,
			JitterPercent: 0,
		},
	}
	executor := NewRetryExecutor(policies)
	ctx := context.Background()

	expectedErr := errors.New("no retry")
	callCount := 0

	err := executor.Execute(ctx, TierTransient, func() error {
		callCount++
		return expectedErr
	})

	if err == nil {
		t.Error("Execute() returned nil, want error")
	}
	if callCount != 1 {
		t.Errorf("Function called %d times, want 1 (zero max attempts means execute once)", callCount)
	}
}

func TestRetryExecutor_ExtractRetryAfter(t *testing.T) {
	policies := map[ErrorTier]*RetryPolicy{
		TierExternalRateLimit: {
			MaxAttempts:   3,
			InitialDelay:  1 * time.Second,
			MaxDelay:      60 * time.Second,
			Multiplier:    2.0,
			UseRetryAfter: true,
			JitterPercent: 0,
		},
	}
	executor := NewRetryExecutor(policies)
	ctx := context.Background()

	retryAfterDuration := 100 * time.Millisecond
	callCount := 0
	startTime := time.Now()

	// Return a TieredError with RetryAfter set
	err := executor.Execute(ctx, TierExternalRateLimit, func() error {
		callCount++
		if callCount < 2 {
			return NewTieredError(TierExternalRateLimit, "rate limited", nil).
				WithRetryAfter(retryAfterDuration)
		}
		return nil
	})

	elapsed := time.Since(startTime)

	if err != nil {
		t.Errorf("Execute() returned error = %v, want nil", err)
	}
	if callCount != 2 {
		t.Errorf("Function called %d times, want 2", callCount)
	}
	// Should have waited at least the RetryAfter duration (with some tolerance)
	if elapsed < retryAfterDuration*80/100 {
		t.Errorf("Execute() completed too quickly (%v), expected to wait ~%v",
			elapsed, retryAfterDuration)
	}
}

func TestRetryExecutor_RateLimitWithRetryAfter(t *testing.T) {
	policies := map[ErrorTier]*RetryPolicy{
		TierExternalRateLimit: {
			MaxAttempts:   5,
			InitialDelay:  500 * time.Millisecond, // Longer than RetryAfter
			MaxDelay:      60 * time.Second,
			Multiplier:    2.0,
			UseRetryAfter: true,
			JitterPercent: 0,
		},
	}
	executor := NewRetryExecutor(policies)
	ctx := context.Background()

	// RetryAfter is shorter than InitialDelay, so it should be used
	retryAfterDuration := 50 * time.Millisecond
	callCount := 0
	startTime := time.Now()

	err := executor.Execute(ctx, TierExternalRateLimit, func() error {
		callCount++
		if callCount < 2 {
			return NewTieredError(TierExternalRateLimit, "rate limited", nil).
				WithRetryAfter(retryAfterDuration)
		}
		return nil
	})

	elapsed := time.Since(startTime)

	if err != nil {
		t.Errorf("Execute() returned error = %v, want nil", err)
	}
	// Should have used RetryAfter (50ms) instead of InitialDelay (500ms)
	if elapsed >= 400*time.Millisecond {
		t.Errorf("Execute() took too long (%v), should have used RetryAfter (%v) not InitialDelay",
			elapsed, retryAfterDuration)
	}
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestRetryExecutor_ConcurrentExecutions(t *testing.T) {
	policies := map[ErrorTier]*RetryPolicy{
		TierTransient: {
			MaxAttempts:   3,
			InitialDelay:  1 * time.Millisecond,
			MaxDelay:      10 * time.Millisecond,
			Multiplier:    2.0,
			JitterPercent: 0.1,
		},
	}
	executor := NewRetryExecutor(policies)
	ctx := context.Background()

	numGoroutines := 10
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			callCount := 0
			err := executor.Execute(ctx, TierTransient, func() error {
				callCount++
				// Succeed on 2nd attempt
				if callCount < 2 {
					return errors.New("temporary failure")
				}
				return nil
			})

			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&errorCount, 1)
			}
		}(i)
	}

	wg.Wait()

	if successCount != int64(numGoroutines) {
		t.Errorf("ConcurrentExecutions: %d succeeded, %d failed, want all %d to succeed",
			successCount, errorCount, numGoroutines)
	}
}

// =============================================================================
// Additional Table-Driven Tests
// =============================================================================

func TestGetRetryPolicy_UnknownTier(t *testing.T) {
	// Test with an unknown tier value
	unknownTier := ErrorTier(999)
	policy := GetRetryPolicy(unknownTier)

	// Should return a no-retry policy
	if policy.MaxAttempts != 0 {
		t.Errorf("GetRetryPolicy(unknown tier) MaxAttempts = %d, want 0", policy.MaxAttempts)
	}
}

func TestNewRetryExecutor_NilPolicies(t *testing.T) {
	executor := NewRetryExecutor(nil)

	// Should use default policies
	ctx := context.Background()
	callCount := 0

	// With default transient policy, should retry
	err := executor.Execute(ctx, TierTransient, func() error {
		callCount++
		if callCount < 2 {
			return errors.New("temp error")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Execute() with default policies returned error = %v", err)
	}
	if callCount < 2 {
		t.Errorf("Should have retried at least once with default transient policy")
	}
}

func TestRetryPolicy_Defaults(t *testing.T) {
	tests := []struct {
		name     string
		tier     ErrorTier
		expected struct {
			maxAttempts   int
			useRetryAfter bool
		}
	}{
		{
			name: "transient",
			tier: TierTransient,
			expected: struct {
				maxAttempts   int
				useRetryAfter bool
			}{5, false},
		},
		{
			name: "rate_limit",
			tier: TierExternalRateLimit,
			expected: struct {
				maxAttempts   int
				useRetryAfter bool
			}{10, true},
		},
		{
			name: "degrading",
			tier: TierExternalDegrading,
			expected: struct {
				maxAttempts   int
				useRetryAfter bool
			}{3, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := GetRetryPolicy(tt.tier)

			if policy.MaxAttempts != tt.expected.maxAttempts {
				t.Errorf("MaxAttempts = %d, want %d",
					policy.MaxAttempts, tt.expected.maxAttempts)
			}
			if policy.UseRetryAfter != tt.expected.useRetryAfter {
				t.Errorf("UseRetryAfter = %v, want %v",
					policy.UseRetryAfter, tt.expected.useRetryAfter)
			}
		})
	}
}

func TestExtractRetryAfter_NonTieredError(t *testing.T) {
	// Test extractRetryAfter with a regular error (not TieredError)
	policies := map[ErrorTier]*RetryPolicy{
		TierExternalRateLimit: {
			MaxAttempts:   2,
			InitialDelay:  10 * time.Millisecond,
			MaxDelay:      100 * time.Millisecond,
			Multiplier:    2.0,
			UseRetryAfter: true,
			JitterPercent: 0,
		},
	}
	executor := NewRetryExecutor(policies)
	ctx := context.Background()

	callCount := 0
	startTime := time.Now()

	// Return a regular error (not TieredError), so RetryAfter extraction should return 0
	err := executor.Execute(ctx, TierExternalRateLimit, func() error {
		callCount++
		if callCount < 2 {
			return errors.New("regular error, not TieredError")
		}
		return nil
	})

	elapsed := time.Since(startTime)

	if err != nil {
		t.Errorf("Execute() returned error = %v, want nil", err)
	}
	// Should have used InitialDelay since no RetryAfter was extractable
	if elapsed < 5*time.Millisecond {
		t.Errorf("Execute() completed too quickly (%v), should have used InitialDelay", elapsed)
	}
}
