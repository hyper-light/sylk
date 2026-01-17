package errors

import (
	"testing"
	"time"
)

// =============================================================================
// Happy Path Tests
// =============================================================================

func TestCalculateDelay_ExponentialGrowth(t *testing.T) {
	policy := &RetryPolicy{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   2.0,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},  // 100 * 2^0 = 100
		{1, 200 * time.Millisecond},  // 100 * 2^1 = 200
		{2, 400 * time.Millisecond},  // 100 * 2^2 = 400
		{3, 800 * time.Millisecond},  // 100 * 2^3 = 800
		{4, 1600 * time.Millisecond}, // 100 * 2^4 = 1600
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := CalculateDelay(tt.attempt, policy)
			if got != tt.expected {
				t.Errorf("CalculateDelay(%d) = %v, want %v", tt.attempt, got, tt.expected)
			}
		})
	}
}

func TestCalculateDelay_FirstAttempt(t *testing.T) {
	policy := &RetryPolicy{
		InitialDelay: 500 * time.Millisecond,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
	}

	got := CalculateDelay(0, policy)
	if got != 500*time.Millisecond {
		t.Errorf("CalculateDelay(0) = %v, want %v", got, 500*time.Millisecond)
	}
}

func TestAddJitter_AppliesVariation(t *testing.T) {
	delay := 1 * time.Second
	jitterPercent := 0.2 // 20% jitter

	// Run multiple times to verify jitter is being applied (not always returning same value)
	results := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		jittered := AddJitter(delay, jitterPercent)
		results[jittered] = true

		// Verify bounds: should be within ±20% of 1s, but at least 1ms
		minExpected := time.Millisecond // ensurePositiveDelay minimum
		maxExpected := time.Duration(float64(delay) * (1 + jitterPercent))

		if jittered < minExpected || jittered > maxExpected {
			t.Errorf("AddJitter(%v, %v) = %v, want in range [%v, %v]",
				delay, jitterPercent, jittered, minExpected, maxExpected)
		}
	}

	// Should have some variation (not all same value)
	if len(results) < 2 {
		t.Errorf("AddJitter should produce varied results, got only %d unique values", len(results))
	}
}

// =============================================================================
// Negative Path Tests
// =============================================================================

func TestCalculateDelay_NilPolicy(t *testing.T) {
	got := CalculateDelay(5, nil)
	if got != 0 {
		t.Errorf("CalculateDelay with nil policy = %v, want 0", got)
	}
}

func TestCalculateDelay_ZeroMultiplier(t *testing.T) {
	policy := &RetryPolicy{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
		Multiplier:   0, // Should default to 2.0
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond}, // 100 * 2^0 = 100
		{1, 200 * time.Millisecond}, // 100 * 2^1 = 200
		{2, 400 * time.Millisecond}, // 100 * 2^2 = 400
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := CalculateDelay(tt.attempt, policy)
			if got != tt.expected {
				t.Errorf("CalculateDelay(%d) with zero multiplier = %v, want %v (defaulting to 2.0)",
					tt.attempt, got, tt.expected)
			}
		})
	}
}

func TestAddJitter_ZeroPercent(t *testing.T) {
	delay := 1 * time.Second

	got := AddJitter(delay, 0)
	if got != delay {
		t.Errorf("AddJitter with 0%% jitter = %v, want %v", got, delay)
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestCalculateDelay_CappedAtMax(t *testing.T) {
	policy := &RetryPolicy{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     500 * time.Millisecond, // Low max to test capping
		Multiplier:   2.0,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond}, // 100 * 2^0 = 100 (under max)
		{1, 200 * time.Millisecond}, // 100 * 2^1 = 200 (under max)
		{2, 400 * time.Millisecond}, // 100 * 2^2 = 400 (under max)
		{3, 500 * time.Millisecond}, // 100 * 2^3 = 800 -> capped at 500
		{4, 500 * time.Millisecond}, // 100 * 2^4 = 1600 -> capped at 500
		{5, 500 * time.Millisecond}, // 100 * 2^5 = 3200 -> capped at 500
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := CalculateDelay(tt.attempt, policy)
			if got != tt.expected {
				t.Errorf("CalculateDelay(%d) = %v, want %v (capped at max)", tt.attempt, got, tt.expected)
			}
		})
	}
}

func TestAddJitter_EnsuresMinimumDelay(t *testing.T) {
	// Very small delay with high jitter could go negative
	delay := 10 * time.Microsecond
	jitterPercent := 0.9 // 90% jitter

	for i := 0; i < 100; i++ {
		got := AddJitter(delay, jitterPercent)
		if got < time.Millisecond {
			t.Errorf("AddJitter should ensure minimum 1ms, got %v", got)
		}
	}
}

func TestAddJitter_BoundsCheck(t *testing.T) {
	delay := 1 * time.Second
	jitterPercent := 0.1 // 10% jitter

	minBound := time.Duration(float64(delay) * (1 - jitterPercent))
	maxBound := time.Duration(float64(delay) * (1 + jitterPercent))

	for i := 0; i < 1000; i++ {
		got := AddJitter(delay, jitterPercent)

		// Must be at least 1ms (ensurePositiveDelay)
		effectiveMin := minBound
		if effectiveMin < time.Millisecond {
			effectiveMin = time.Millisecond
		}

		if got < effectiveMin || got > maxBound {
			t.Errorf("AddJitter(%v, %v) = %v, want in range [%v, %v]",
				delay, jitterPercent, got, effectiveMin, maxBound)
		}
	}
}

// =============================================================================
// Table-Driven Tests for Additional Coverage
// =============================================================================

func TestCalculateDelay_VariousMultipliers(t *testing.T) {
	tests := []struct {
		name         string
		initialDelay time.Duration
		maxDelay     time.Duration
		multiplier   float64
		attempt      int
		expected     time.Duration
	}{
		{
			name:         "multiplier 1.5",
			initialDelay: 100 * time.Millisecond,
			maxDelay:     10 * time.Second,
			multiplier:   1.5,
			attempt:      2,
			expected:     225 * time.Millisecond, // 100 * 1.5^2 = 225
		},
		{
			name:         "multiplier 3.0",
			initialDelay: 100 * time.Millisecond,
			maxDelay:     10 * time.Second,
			multiplier:   3.0,
			attempt:      2,
			expected:     900 * time.Millisecond, // 100 * 3^2 = 900
		},
		{
			name:         "negative multiplier defaults to 2.0",
			initialDelay: 100 * time.Millisecond,
			maxDelay:     10 * time.Second,
			multiplier:   -1.0,
			attempt:      1,
			expected:     200 * time.Millisecond, // 100 * 2^1 = 200
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := &RetryPolicy{
				InitialDelay: tt.initialDelay,
				MaxDelay:     tt.maxDelay,
				Multiplier:   tt.multiplier,
			}
			got := CalculateDelay(tt.attempt, policy)
			if got != tt.expected {
				t.Errorf("CalculateDelay() = %v, want %v", got, tt.expected)
			}
		})
	}
}
