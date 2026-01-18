package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// TestNewTokenBucketLimiter tests limiter creation.
func TestNewTokenBucketLimiter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   TokenBucketConfig
		wantCap  float64
		wantRate float64
	}{
		{
			name: "standard config",
			config: TokenBucketConfig{
				Capacity:   100,
				RefillRate: 10,
			},
			wantCap:  100,
			wantRate: 10,
		},
		{
			name: "fractional values",
			config: TokenBucketConfig{
				Capacity:   1.5,
				RefillRate: 0.1,
			},
			wantCap:  1.5,
			wantRate: 0.1,
		},
		{
			name: "large capacity",
			config: TokenBucketConfig{
				Capacity:   10000,
				RefillRate: 100,
			},
			wantCap:  10000,
			wantRate: 100,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limiter := NewTokenBucketLimiter(tc.config)

			if limiter.config.Capacity != tc.wantCap {
				t.Errorf("Capacity = %v, want %v", limiter.config.Capacity, tc.wantCap)
			}
			if limiter.config.RefillRate != tc.wantRate {
				t.Errorf("RefillRate = %v, want %v", limiter.config.RefillRate, tc.wantRate)
			}
			if limiter.tokens != tc.wantCap {
				t.Errorf("initial tokens = %v, want %v (full capacity)", limiter.tokens, tc.wantCap)
			}
		})
	}
}

// TestTokenBucketLimiter_Check_Allowed tests that Check allows when tokens available.
func TestTokenBucketLimiter_Check_Allowed(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   10,
		RefillRate: 1,
	})

	decision := limiter.Check()

	if !decision.Allowed {
		t.Error("Check() should allow when tokens available")
	}
	if decision.WaitTime != 0 {
		t.Errorf("WaitTime = %v, want 0 when allowed", decision.WaitTime)
	}
	if decision.Limiter != "token_bucket" {
		t.Errorf("Limiter = %q, want %q", decision.Limiter, "token_bucket")
	}
	if decision.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", decision.Confidence)
	}
}

// TestTokenBucketLimiter_Check_Denied tests that Check denies when tokens exhausted.
func TestTokenBucketLimiter_Check_Denied(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   10,
		RefillRate: 2, // 2 tokens per second
	})

	// Exhaust all tokens
	limiter.Consume(10)

	decision := limiter.Check()

	if decision.Allowed {
		t.Error("Check() should deny when tokens exhausted")
	}
	if decision.Reason != "insufficient tokens" {
		t.Errorf("Reason = %q, want %q", decision.Reason, "insufficient tokens")
	}
	if decision.Limiter != "token_bucket" {
		t.Errorf("Limiter = %q, want %q", decision.Limiter, "token_bucket")
	}
	if decision.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", decision.Confidence)
	}
}

// TestTokenBucketLimiter_WaitTime tests correct wait time calculation.
func TestTokenBucketLimiter_WaitTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		refillRate   float64
		tokensLeft   float64
		wantWaitMin  time.Duration
		wantWaitMax  time.Duration
	}{
		{
			name:         "need 1 token at 1/s",
			refillRate:   1,
			tokensLeft:   0,
			wantWaitMin:  950 * time.Millisecond,
			wantWaitMax:  1050 * time.Millisecond,
		},
		{
			name:         "need 0.5 token at 2/s",
			refillRate:   2,
			tokensLeft:   0.5,
			wantWaitMin:  200 * time.Millisecond,
			wantWaitMax:  300 * time.Millisecond,
		},
		{
			name:         "need 1 token at 10/s",
			refillRate:   10,
			tokensLeft:   0,
			wantWaitMin:  90 * time.Millisecond,
			wantWaitMax:  110 * time.Millisecond,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limiter := NewTokenBucketLimiter(TokenBucketConfig{
				Capacity:   10,
				RefillRate: tc.refillRate,
			})

			// Set tokens to specific value
			limiter.mu.Lock()
			limiter.tokens = tc.tokensLeft
			limiter.lastRefill = time.Now()
			limiter.mu.Unlock()

			decision := limiter.Check()

			if decision.Allowed {
				t.Fatal("expected denial")
			}
			if decision.WaitTime < tc.wantWaitMin || decision.WaitTime > tc.wantWaitMax {
				t.Errorf("WaitTime = %v, want between %v and %v",
					decision.WaitTime, tc.wantWaitMin, tc.wantWaitMax)
			}
		})
	}
}

// TestTokenBucketLimiter_Consume tests token deduction.
func TestTokenBucketLimiter_Consume(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		initial     float64
		consume     float64
		wantTokens  float64
	}{
		{
			name:        "consume partial",
			initial:     10,
			consume:     3,
			wantTokens:  7,
		},
		{
			name:        "consume all",
			initial:     10,
			consume:     10,
			wantTokens:  0,
		},
		{
			name:        "consume more than available clamps to 0",
			initial:     5,
			consume:     10,
			wantTokens:  0,
		},
		{
			name:        "consume fractional",
			initial:     10,
			consume:     0.5,
			wantTokens:  9.5,
		},
		{
			name:        "consume zero",
			initial:     10,
			consume:     0,
			wantTokens:  10,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limiter := NewTokenBucketLimiter(TokenBucketConfig{
				Capacity:   tc.initial,
				RefillRate: 0, // No refill for this test
			})

			// Set initial tokens precisely
			limiter.mu.Lock()
			limiter.tokens = tc.initial
			limiter.lastRefill = time.Now()
			limiter.mu.Unlock()

			limiter.Consume(tc.consume)

			limiter.mu.Lock()
			tokens := limiter.tokens
			limiter.mu.Unlock()

			// Allow small delta for timing
			delta := 0.01
			if tokens < tc.wantTokens-delta || tokens > tc.wantTokens+delta {
				t.Errorf("tokens = %v, want approximately %v", tokens, tc.wantTokens)
			}
		})
	}
}

// TestTokenBucketLimiter_Refill tests token refill over time.
func TestTokenBucketLimiter_Refill(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   100,
		RefillRate: 100, // 100 tokens per second
	})

	// Exhaust all tokens
	limiter.Consume(100)

	// Verify exhausted
	limiter.mu.Lock()
	if limiter.tokens != 0 {
		t.Fatalf("tokens should be 0, got %v", limiter.tokens)
	}
	limiter.mu.Unlock()

	// Wait for some refill (50ms = 5 tokens at 100/s)
	time.Sleep(50 * time.Millisecond)

	// Check triggers refill
	decision := limiter.Check()

	limiter.mu.Lock()
	tokens := limiter.tokens
	limiter.mu.Unlock()

	// Should have approximately 5 tokens (50ms * 100/s)
	if tokens < 3 || tokens > 10 {
		t.Errorf("tokens after 50ms refill = %v, expected approximately 5", tokens)
	}
	if !decision.Allowed {
		t.Error("should be allowed after refill")
	}
}

// TestTokenBucketLimiter_RefillCapsAtCapacity tests that refill doesn't exceed capacity.
func TestTokenBucketLimiter_RefillCapsAtCapacity(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   10,
		RefillRate: 1000, // Very fast refill
	})

	// Consume some tokens
	limiter.Consume(5)

	// Wait long enough to "over-refill"
	time.Sleep(50 * time.Millisecond)

	// Trigger refill
	limiter.Check()

	limiter.mu.Lock()
	tokens := limiter.tokens
	limiter.mu.Unlock()

	if tokens > 10 {
		t.Errorf("tokens = %v, should not exceed capacity of 10", tokens)
	}
}

// TestTokenBucketLimiter_ImplementsInterface verifies interface compliance.
func TestTokenBucketLimiter_ImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ RateLimiter = (*TokenBucketLimiter)(nil)
	var _ RateLimiter = NewTokenBucketLimiter(TokenBucketConfig{})
}

// TestTokenBucketLimiter_ConcurrentAccess tests thread safety.
func TestTokenBucketLimiter_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   1000,
		RefillRate: 100,
	})

	var wg sync.WaitGroup
	numGoroutines := 100
	opsPerGoroutine := 100

	// Run concurrent Check and Consume operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				limiter.Check()
				limiter.Consume(0.1)
			}
		}()
	}

	wg.Wait()

	// Verify limiter is still functional
	decision := limiter.Check()
	if decision.Limiter != "token_bucket" {
		t.Errorf("Limiter = %q, want %q after concurrent access", decision.Limiter, "token_bucket")
	}
}

// TestTokenBucketLimiter_ZeroRefillRate tests behavior with zero refill rate.
func TestTokenBucketLimiter_ZeroRefillRate(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   10,
		RefillRate: 0,
	})

	// Exhaust tokens
	limiter.Consume(10)

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	decision := limiter.Check()

	if decision.Allowed {
		t.Error("should not be allowed with zero refill rate")
	}
}

// TestTokenBucketLimiter_CheckThenConsume tests typical usage pattern.
func TestTokenBucketLimiter_CheckThenConsume(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   5,
		RefillRate: 0, // No refill for deterministic test
	})

	for i := 0; i < 5; i++ {
		decision := limiter.Check()
		if !decision.Allowed {
			t.Errorf("iteration %d: expected allowed", i)
		}
		limiter.Consume(1)
	}

	// 6th check should fail
	decision := limiter.Check()
	if decision.Allowed {
		t.Error("6th check should be denied")
	}
}

// TestTokenBucketLimiter_MultipleConsumes tests multiple consume calls.
func TestTokenBucketLimiter_MultipleConsumes(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   10,
		RefillRate: 0,
	})

	limiter.Consume(3)
	limiter.Consume(3)
	limiter.Consume(3)

	limiter.mu.Lock()
	tokens := limiter.tokens
	limiter.mu.Unlock()

	if tokens != 1 {
		t.Errorf("tokens = %v, want 1 after consuming 9 of 10", tokens)
	}
}

// TestTokenBucketLimiter_RefillPrecision tests refill timing precision.
func TestTokenBucketLimiter_RefillPrecision(t *testing.T) {
	t.Parallel()

	limiter := NewTokenBucketLimiter(TokenBucketConfig{
		Capacity:   100,
		RefillRate: 10, // 10 tokens per second
	})

	// Exhaust all tokens
	limiter.mu.Lock()
	limiter.tokens = 0
	limiter.lastRefill = time.Now()
	limiter.mu.Unlock()

	// Wait 100ms -> should gain 1 token
	time.Sleep(100 * time.Millisecond)

	limiter.Check() // Trigger refill

	limiter.mu.Lock()
	tokens := limiter.tokens
	limiter.mu.Unlock()

	// Should have approximately 1 token (100ms * 10/s)
	if tokens < 0.8 || tokens > 1.5 {
		t.Errorf("tokens after 100ms at 10/s = %v, expected approximately 1", tokens)
	}
}

// TestTokenBucketLimiter_DecisionFields tests all decision fields are set correctly.
func TestTokenBucketLimiter_DecisionFields(t *testing.T) {
	t.Parallel()

	t.Run("allowed decision", func(t *testing.T) {
		t.Parallel()

		limiter := NewTokenBucketLimiter(TokenBucketConfig{
			Capacity:   10,
			RefillRate: 1,
		})

		decision := limiter.Check()

		if !decision.Allowed {
			t.Error("Allowed should be true")
		}
		if decision.WaitTime != 0 {
			t.Errorf("WaitTime = %v, want 0", decision.WaitTime)
		}
		if decision.Reason != "" {
			t.Errorf("Reason = %q, want empty", decision.Reason)
		}
		if decision.Limiter != "token_bucket" {
			t.Errorf("Limiter = %q, want %q", decision.Limiter, "token_bucket")
		}
		if decision.Confidence != 1.0 {
			t.Errorf("Confidence = %v, want 1.0", decision.Confidence)
		}
	})

	t.Run("denied decision", func(t *testing.T) {
		t.Parallel()

		limiter := NewTokenBucketLimiter(TokenBucketConfig{
			Capacity:   10,
			RefillRate: 1,
		})
		limiter.Consume(10)

		decision := limiter.Check()

		if decision.Allowed {
			t.Error("Allowed should be false")
		}
		if decision.WaitTime <= 0 {
			t.Errorf("WaitTime = %v, should be positive", decision.WaitTime)
		}
		if decision.Reason != "insufficient tokens" {
			t.Errorf("Reason = %q, want %q", decision.Reason, "insufficient tokens")
		}
		if decision.Limiter != "token_bucket" {
			t.Errorf("Limiter = %q, want %q", decision.Limiter, "token_bucket")
		}
		if decision.Confidence != 1.0 {
			t.Errorf("Confidence = %v, want 1.0", decision.Confidence)
		}
	})
}
