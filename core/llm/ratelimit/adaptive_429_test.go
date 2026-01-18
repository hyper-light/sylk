package ratelimit

import (
	"math"
	"sync"
	"testing"
	"time"
)

const floatTolerance = 1e-9

// testConfig returns a standard test configuration.
func testConfig() Adaptive429Config {
	return Adaptive429Config{
		InitialLimit: 50,
		DecayFactor:  0.5,
		GrowthFactor: 1.5,
		MinLimit:     5,
		MaxLimit:     200,
	}
}

// TestNewAdaptive429Limiter tests limiter initialization.
func TestNewAdaptive429Limiter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		config         Adaptive429Config
		wantLimit      float64
		wantConfidence float64
	}{
		{
			name:           "standard config",
			config:         testConfig(),
			wantLimit:      50,
			wantConfidence: 0.5,
		},
		{
			name: "high initial limit",
			config: Adaptive429Config{
				InitialLimit: 100,
				DecayFactor:  0.9,
				GrowthFactor: 1.1,
				MinLimit:     10,
				MaxLimit:     500,
			},
			wantLimit:      100,
			wantConfidence: 0.5,
		},
		{
			name: "low initial limit",
			config: Adaptive429Config{
				InitialLimit: 5,
				DecayFactor:  0.8,
				GrowthFactor: 1.2,
				MinLimit:     1,
				MaxLimit:     50,
			},
			wantLimit:      5,
			wantConfidence: 0.5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limiter := NewAdaptive429Limiter(tc.config)

			if limiter.CurrentLimit() != tc.wantLimit {
				t.Errorf("CurrentLimit() = %v, want %v", limiter.CurrentLimit(), tc.wantLimit)
			}

			limiter.mu.RLock()
			conf := limiter.confidence
			limiter.mu.RUnlock()

			if conf != tc.wantConfidence {
				t.Errorf("confidence = %v, want %v", conf, tc.wantConfidence)
			}
		})
	}
}

// TestCheck_NoBackoff tests Check when not in backoff.
func TestCheck_NoBackoff(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())
	decision := limiter.Check()

	if !decision.Allowed {
		t.Error("Check() should allow when not in backoff")
	}
	if decision.WaitTime != 0 {
		t.Errorf("WaitTime = %v, want 0", decision.WaitTime)
	}
	if decision.Limiter != "adaptive_429" {
		t.Errorf("Limiter = %q, want %q", decision.Limiter, "adaptive_429")
	}
	if decision.Confidence != 0.5 {
		t.Errorf("Confidence = %v, want 0.5", decision.Confidence)
	}
}

// TestCheck_DuringBackoff tests Check during the 5s backoff window.
func TestCheck_DuringBackoff(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())
	limiter.Record429(0)

	decision := limiter.Check()

	if decision.Allowed {
		t.Error("Check() should deny during backoff")
	}
	if decision.WaitTime <= 0 || decision.WaitTime > 5*time.Second {
		t.Errorf("WaitTime = %v, want (0, 5s]", decision.WaitTime)
	}
	if decision.Reason != "backing off after 429" {
		t.Errorf("Reason = %q, want %q", decision.Reason, "backing off after 429")
	}
	if decision.Limiter != "adaptive_429" {
		t.Errorf("Limiter = %q, want %q", decision.Limiter, "adaptive_429")
	}
}

// TestCheck_AfterBackoffExpires tests Check after 5s backoff window.
func TestCheck_AfterBackoffExpires(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	// Manually set last429 to 6 seconds ago
	limiter.mu.Lock()
	limiter.last429 = time.Now().Add(-6 * time.Second)
	limiter.mu.Unlock()

	decision := limiter.Check()

	if !decision.Allowed {
		t.Error("Check() should allow after backoff expires")
	}
	if decision.WaitTime != 0 {
		t.Errorf("WaitTime = %v, want 0", decision.WaitTime)
	}
}

// TestRecord429_LimitDecay tests that 429 decreases limit.
func TestRecord429_LimitDecay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      Adaptive429Config
		num429s     int
		wantLimit   float64
		description string
	}{
		{
			name:        "single 429",
			config:      testConfig(),
			num429s:     1,
			wantLimit:   25, // 50 * 0.5
			description: "limit should halve on first 429",
		},
		{
			name:        "two 429s",
			config:      testConfig(),
			num429s:     2,
			wantLimit:   12.5, // 50 * 0.5 * 0.5
			description: "limit should quarter on second 429",
		},
		{
			name:        "clamp to min",
			config:      testConfig(),
			num429s:     5,
			wantLimit:   5, // Clamped to MinLimit
			description: "limit should clamp to min after many 429s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limiter := NewAdaptive429Limiter(tc.config)

			for i := 0; i < tc.num429s; i++ {
				limiter.Record429(0)
			}

			if limiter.CurrentLimit() != tc.wantLimit {
				t.Errorf("%s: CurrentLimit() = %v, want %v",
					tc.description, limiter.CurrentLimit(), tc.wantLimit)
			}
		})
	}
}

// TestRecord429_ConfidenceIncrease tests confidence increases on 429.
func TestRecord429_ConfidenceIncrease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		num429s        int
		wantConfidence float64
	}{
		{"one 429", 1, 0.6},
		{"two 429s", 2, 0.7},
		{"five 429s", 5, 1.0},
		{"six 429s - capped", 6, 1.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limiter := NewAdaptive429Limiter(testConfig())

			for i := 0; i < tc.num429s; i++ {
				limiter.Record429(0)
			}

			limiter.mu.RLock()
			conf := limiter.confidence
			limiter.mu.RUnlock()

			if math.Abs(conf-tc.wantConfidence) > floatTolerance {
				t.Errorf("confidence = %v, want %v", conf, tc.wantConfidence)
			}
		})
	}
}

// TestRecord429_ResetsSuccessRun tests that 429 resets success run.
func TestRecord429_ResetsSuccessRun(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	// Record some successes
	for i := 0; i < 4; i++ {
		limiter.RecordSuccess()
	}

	limiter.mu.RLock()
	runBefore := limiter.successRun
	limiter.mu.RUnlock()

	if runBefore != 4 {
		t.Errorf("successRun before 429 = %d, want 4", runBefore)
	}

	limiter.Record429(0)

	limiter.mu.RLock()
	runAfter := limiter.successRun
	limiter.mu.RUnlock()

	if runAfter != 0 {
		t.Errorf("successRun after 429 = %d, want 0", runAfter)
	}
}

// TestRecordSuccess_Increment tests success increments the counter.
func TestRecordSuccess_Increment(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	for i := 1; i <= 4; i++ {
		limiter.RecordSuccess()

		limiter.mu.RLock()
		run := limiter.successRun
		limiter.mu.RUnlock()

		if run != i {
			t.Errorf("after %d successes, successRun = %d, want %d", i, run, i)
		}
	}
}

// TestRecordSuccess_ClearsBackoff tests 5 successes clear last429.
func TestRecordSuccess_ClearsBackoff(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())
	limiter.Record429(0)

	// Verify we're in backoff
	limiter.mu.RLock()
	inBackoff := !limiter.last429.IsZero()
	limiter.mu.RUnlock()

	if !inBackoff {
		t.Error("should be in backoff after 429")
	}

	// 4 successes - still in backoff
	for i := 0; i < 4; i++ {
		limiter.RecordSuccess()
	}

	limiter.mu.RLock()
	stillInBackoff := !limiter.last429.IsZero()
	limiter.mu.RUnlock()

	if !stillInBackoff {
		t.Error("should still be in backoff after 4 successes")
	}

	// 5th success - clear backoff
	limiter.RecordSuccess()

	limiter.mu.RLock()
	cleared := limiter.last429.IsZero()
	limiter.mu.RUnlock()

	if !cleared {
		t.Error("backoff should be cleared after 5 successes")
	}
}

// TestRecordSuccess_GrowthAfter10 tests limit growth after 10 successes.
func TestRecordSuccess_GrowthAfter10(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())
	initialLimit := limiter.CurrentLimit()

	// 9 successes - no growth yet
	for i := 0; i < 9; i++ {
		limiter.RecordSuccess()
	}

	if limiter.CurrentLimit() != initialLimit {
		t.Errorf("limit should not change after 9 successes: got %v, want %v",
			limiter.CurrentLimit(), initialLimit)
	}

	// 10th success - apply growth
	limiter.RecordSuccess()

	expectedLimit := initialLimit * 1.5 // GrowthFactor
	if limiter.CurrentLimit() != expectedLimit {
		t.Errorf("limit after 10 successes = %v, want %v",
			limiter.CurrentLimit(), expectedLimit)
	}

	// Success run should reset
	limiter.mu.RLock()
	run := limiter.successRun
	limiter.mu.RUnlock()

	if run != 0 {
		t.Errorf("successRun should reset after growth: got %d, want 0", run)
	}
}

// TestRecordSuccess_ClampToMax tests limit is clamped to MaxLimit.
func TestRecordSuccess_ClampToMax(t *testing.T) {
	t.Parallel()

	config := Adaptive429Config{
		InitialLimit: 180,
		DecayFactor:  0.5,
		GrowthFactor: 1.5,
		MinLimit:     5,
		MaxLimit:     200,
	}

	limiter := NewAdaptive429Limiter(config)

	// 10 successes would grow 180 * 1.5 = 270, but clamped to 200
	for i := 0; i < 10; i++ {
		limiter.RecordSuccess()
	}

	if limiter.CurrentLimit() != 200 {
		t.Errorf("limit should clamp to MaxLimit: got %v, want 200",
			limiter.CurrentLimit())
	}
}

// TestCurrentLimit_ThreadSafe tests concurrent access to CurrentLimit.
func TestCurrentLimit_ThreadSafe(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	var wg sync.WaitGroup
	const goroutines = 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = limiter.CurrentLimit()
		}()
	}

	wg.Wait()
}

// TestConcurrentOperations tests thread safety of all operations.
func TestConcurrentOperations(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	var wg sync.WaitGroup
	const goroutines = 50

	// Concurrent checks
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = limiter.Check()
		}()
	}

	// Concurrent successes
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limiter.RecordSuccess()
		}()
	}

	// Concurrent 429s
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limiter.Record429(time.Second)
		}()
	}

	// Concurrent limit reads
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = limiter.CurrentLimit()
		}()
	}

	wg.Wait()
}

// TestRateLimiterInterface tests that Adaptive429Limiter implements RateLimiter.
func TestRateLimiterInterface(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	var _ RateLimiter = limiter
}

// TestBackoffWindow_Timing tests the 5 second backoff window timing.
func TestBackoffWindow_Timing(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	// Set last429 to exactly 4 seconds ago
	limiter.mu.Lock()
	limiter.last429 = time.Now().Add(-4 * time.Second)
	limiter.mu.Unlock()

	decision := limiter.Check()

	if decision.Allowed {
		t.Error("should deny within 5s window")
	}

	// WaitTime should be approximately 1 second
	if decision.WaitTime < 900*time.Millisecond || decision.WaitTime > 1100*time.Millisecond {
		t.Errorf("WaitTime = %v, expected ~1s", decision.WaitTime)
	}
}

// TestMultipleGrowthCycles tests multiple growth cycles.
func TestMultipleGrowthCycles(t *testing.T) {
	t.Parallel()

	config := Adaptive429Config{
		InitialLimit: 10,
		DecayFactor:  0.5,
		GrowthFactor: 2.0,
		MinLimit:     1,
		MaxLimit:     1000,
	}

	limiter := NewAdaptive429Limiter(config)

	// First cycle: 10 -> 20
	for i := 0; i < 10; i++ {
		limiter.RecordSuccess()
	}
	if limiter.CurrentLimit() != 20 {
		t.Errorf("after first cycle: got %v, want 20", limiter.CurrentLimit())
	}

	// Second cycle: 20 -> 40
	for i := 0; i < 10; i++ {
		limiter.RecordSuccess()
	}
	if limiter.CurrentLimit() != 40 {
		t.Errorf("after second cycle: got %v, want 40", limiter.CurrentLimit())
	}
}

// TestDecayAndGrowthSequence tests alternating decay and growth.
func TestDecayAndGrowthSequence(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	// Initial: 50
	// 429 -> 25
	limiter.Record429(0)
	if limiter.CurrentLimit() != 25 {
		t.Errorf("after 429: got %v, want 25", limiter.CurrentLimit())
	}

	// 10 successes -> 37.5
	for i := 0; i < 10; i++ {
		limiter.RecordSuccess()
	}
	if limiter.CurrentLimit() != 37.5 {
		t.Errorf("after growth: got %v, want 37.5", limiter.CurrentLimit())
	}

	// Another 429 -> 18.75
	limiter.Record429(0)
	if limiter.CurrentLimit() != 18.75 {
		t.Errorf("after second 429: got %v, want 18.75", limiter.CurrentLimit())
	}
}

// TestRecord429_WithRetryAfter tests retryAfter parameter (currently unused).
func TestRecord429_WithRetryAfter(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	// retryAfter is accepted but currently not used in the implementation
	limiter.Record429(30 * time.Second)

	// Verify 429 was recorded
	limiter.mu.RLock()
	recorded := !limiter.last429.IsZero()
	limiter.mu.RUnlock()

	if !recorded {
		t.Error("429 should be recorded regardless of retryAfter")
	}
}

// TestCheck_ConfidenceInDecision tests that confidence is included in decision.
func TestCheck_ConfidenceInDecision(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())

	// Initial confidence: 0.5
	decision := limiter.Check()
	if decision.Confidence != 0.5 {
		t.Errorf("initial confidence = %v, want 0.5", decision.Confidence)
	}

	// After 429, confidence: 0.6
	limiter.Record429(0)

	// Wait for backoff to expire
	limiter.mu.Lock()
	limiter.last429 = time.Time{}
	limiter.mu.Unlock()

	decision = limiter.Check()
	if decision.Confidence != 0.6 {
		t.Errorf("confidence after 429 = %v, want 0.6", decision.Confidence)
	}
}

// TestLimitBoundaries tests edge cases at min and max limits.
func TestLimitBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("at min limit", func(t *testing.T) {
		t.Parallel()

		config := Adaptive429Config{
			InitialLimit: 5,
			DecayFactor:  0.5,
			GrowthFactor: 1.5,
			MinLimit:     5,
			MaxLimit:     200,
		}

		limiter := NewAdaptive429Limiter(config)
		limiter.Record429(0)

		// Should stay at 5, not go below
		if limiter.CurrentLimit() != 5 {
			t.Errorf("limit = %v, want 5 (min)", limiter.CurrentLimit())
		}
	})

	t.Run("at max limit", func(t *testing.T) {
		t.Parallel()

		config := Adaptive429Config{
			InitialLimit: 200,
			DecayFactor:  0.5,
			GrowthFactor: 1.5,
			MinLimit:     5,
			MaxLimit:     200,
		}

		limiter := NewAdaptive429Limiter(config)

		// 10 successes - should stay at 200
		for i := 0; i < 10; i++ {
			limiter.RecordSuccess()
		}

		if limiter.CurrentLimit() != 200 {
			t.Errorf("limit = %v, want 200 (max)", limiter.CurrentLimit())
		}
	})
}

// TestGrowthClearsBackoff tests that growth at 10 successes also clears backoff.
func TestGrowthClearsBackoff(t *testing.T) {
	t.Parallel()

	limiter := NewAdaptive429Limiter(testConfig())
	limiter.Record429(0)

	// 10 successes triggers growth AND clears backoff (since successRun resets to 0)
	for i := 0; i < 10; i++ {
		limiter.RecordSuccess()
	}

	limiter.mu.RLock()
	cleared := limiter.last429.IsZero()
	limiter.mu.RUnlock()

	if !cleared {
		t.Error("backoff should be cleared after 10 successes (growth)")
	}
}
