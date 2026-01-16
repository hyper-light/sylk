package guide

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetry_SuccessOnFirstAttempt(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts: 3,
	}

	result := Retry(context.Background(), policy, func(ctx context.Context, attempt int) (string, error) {
		return "success", nil
	})

	if result.Err != nil {
		t.Errorf("expected no error, got %v", result.Err)
	}
	if result.Value != "success" {
		t.Errorf("expected 'success', got %v", result.Value)
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", result.Attempts)
	}
}

func TestRetry_SuccessAfterRetries(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:  5,
		InitialDelay: 1 * time.Millisecond,
		Multiplier:   1.0,
	}

	attempts := 0
	result := Retry(context.Background(), policy, func(ctx context.Context, attempt int) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("temporary error")
		}
		return "success", nil
	})

	if result.Err != nil {
		t.Errorf("expected no error, got %v", result.Err)
	}
	if result.Value != "success" {
		t.Errorf("expected 'success', got %v", result.Value)
	}
	if result.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempts)
	}
}

func TestRetry_ExhaustsAttempts(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		Multiplier:   1.0,
	}

	result := Retry(context.Background(), policy, func(ctx context.Context, attempt int) (string, error) {
		return "", errors.New("persistent error")
	})

	if result.Err == nil {
		t.Error("expected error after exhausting attempts")
	}
	if result.Attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", result.Attempts)
	}
}

func TestRetry_ContextCancellation(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:  10,
		InitialDelay: 100 * time.Millisecond,
		Multiplier:   1.0,
	}

	ctx, cancel := context.WithCancel(context.Background())

	attempts := 0
	result := Retry(ctx, policy, func(ctx context.Context, attempt int) (string, error) {
		attempts++
		if attempts == 2 {
			cancel() // Cancel after second attempt
		}
		return "", errors.New("error")
	})

	// Should stop early due to context cancellation
	if attempts >= policy.MaxAttempts {
		t.Error("expected context cancellation to stop retries early")
	}
	// The error might be context.Canceled or the last operation error
	if result.Err == nil {
		t.Error("expected an error")
	}
}

func TestRetry_NonRetryableError(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:  5,
		InitialDelay: 1 * time.Millisecond,
		RetryableErrors: func(err error) bool {
			return err.Error() != "permanent"
		},
	}

	attempts := 0
	result := Retry(context.Background(), policy, func(ctx context.Context, attempt int) (string, error) {
		attempts++
		if attempts == 2 {
			return "", errors.New("permanent")
		}
		return "", errors.New("temporary")
	})

	if result.Attempts != 2 {
		t.Errorf("expected 2 attempts (stopped on non-retryable), got %d", result.Attempts)
	}
	if result.Err.Error() != "permanent" {
		t.Errorf("expected 'permanent' error, got %v", result.Err)
	}
}

func TestRetry_ExponentialBackoff(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:  4,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
		JitterFactor: 0, // No jitter for predictable timing
	}

	var times []time.Time
	Retry(context.Background(), policy, func(ctx context.Context, attempt int) (string, error) {
		times = append(times, time.Now())
		return "", errors.New("error")
	})

	if len(times) != 4 {
		t.Errorf("expected 4 times, got %d", len(times))
		return
	}

	// Check delays roughly match exponential backoff
	// Attempt 1→2: 10ms, 2→3: 20ms, 3→4: 40ms
	delays := []time.Duration{
		times[1].Sub(times[0]),
		times[2].Sub(times[1]),
		times[3].Sub(times[2]),
	}

	// Allow 50% tolerance for timing
	expectedDelays := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	for i, expected := range expectedDelays {
		if delays[i] < expected/2 || delays[i] > expected*2 {
			t.Errorf("delay %d: expected ~%v, got %v", i, expected, delays[i])
		}
	}
}

func TestRetryVoid(t *testing.T) {
	policy := RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
	}

	attempts := 0
	result := RetryVoid(context.Background(), policy, func(ctx context.Context, attempt int) error {
		attempts++
		if attempts < 2 {
			return errors.New("error")
		}
		return nil
	})

	if result.Err != nil {
		t.Errorf("expected no error, got %v", result.Err)
	}
	if result.Attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", result.Attempts)
	}
}

func TestCalculateDelay_MaxDelay(t *testing.T) {
	policy := RetryPolicy{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     500 * time.Millisecond,
		Multiplier:   10.0,
		JitterFactor: 0,
	}

	// After several attempts, delay should be capped at MaxDelay
	delay := calculateDelay(policy, 10)
	if delay > policy.MaxDelay {
		t.Errorf("delay %v exceeded MaxDelay %v", delay, policy.MaxDelay)
	}
}

func TestDefaultRetryPolicy(t *testing.T) {
	policy := DefaultRetryPolicy()

	if policy.MaxAttempts != 3 {
		t.Errorf("expected MaxAttempts=3, got %d", policy.MaxAttempts)
	}
	if policy.InitialDelay != 100*time.Millisecond {
		t.Errorf("expected InitialDelay=100ms, got %v", policy.InitialDelay)
	}
}

func TestNoRetryPolicy(t *testing.T) {
	policy := NoRetryPolicy()

	attempts := 0
	Retry(context.Background(), policy, func(ctx context.Context, attempt int) (string, error) {
		attempts++
		return "", errors.New("error")
	})

	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt with NoRetryPolicy, got %d", attempts)
	}
}
