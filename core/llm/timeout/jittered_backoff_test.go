package timeout

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewJitteredBackoff(t *testing.T) {
	t.Parallel()

	base := 100 * time.Millisecond
	max := 10 * time.Second

	jb := NewJitteredBackoff(base, max)

	if jb.config.Base != base {
		t.Errorf("Base = %v, want %v", jb.config.Base, base)
	}
	if jb.config.Max != max {
		t.Errorf("Max = %v, want %v", jb.config.Max, max)
	}
	if jb.config.MinJitter != 0.5 {
		t.Errorf("MinJitter = %v, want 0.5", jb.config.MinJitter)
	}
	if jb.config.MaxJitter != 1.5 {
		t.Errorf("MaxJitter = %v, want 1.5", jb.config.MaxJitter)
	}
	if jb.attempt != 0 {
		t.Errorf("attempt = %d, want 0", jb.attempt)
	}
	if jb.rng == nil {
		t.Error("rng should not be nil")
	}
}

func TestNewJitteredBackoffWithConfig(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      200 * time.Millisecond,
		Max:       20 * time.Second,
		MinJitter: 0.8,
		MaxJitter: 1.2,
	}

	jb := NewJitteredBackoffWithConfig(config)

	if jb.config != config {
		t.Errorf("config = %+v, want %+v", jb.config, config)
	}
	if jb.attempt != 0 {
		t.Errorf("attempt = %d, want 0", jb.attempt)
	}
	if jb.rng == nil {
		t.Error("rng should not be nil")
	}
}

func TestNewJitteredBackoffWithConfig_DefaultConfig(t *testing.T) {
	t.Parallel()

	config := DefaultBackoffConfig()
	jb := NewJitteredBackoffWithConfig(config)

	if jb.config.Base != 100*time.Millisecond {
		t.Errorf("Base = %v, want %v", jb.config.Base, 100*time.Millisecond)
	}
	if jb.config.Max != 30*time.Second {
		t.Errorf("Max = %v, want %v", jb.config.Max, 30*time.Second)
	}
}

// =============================================================================
// Exponential Growth Tests
// =============================================================================

func TestJitteredBackoff_Next_ExponentialGrowth(t *testing.T) {
	t.Parallel()

	// Use jitter range [1.0, 1.0] to eliminate randomness for this test
	config := BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       10 * time.Second,
		MinJitter: 1.0,
		MaxJitter: 1.0 + 1e-10, // Minimal range to pass validation
	}
	jb := NewJitteredBackoffWithConfig(config)

	expectedDurations := []time.Duration{
		100 * time.Millisecond,  // 100ms * 2^0 = 100ms
		200 * time.Millisecond,  // 100ms * 2^1 = 200ms
		400 * time.Millisecond,  // 100ms * 2^2 = 400ms
		800 * time.Millisecond,  // 100ms * 2^3 = 800ms
		1600 * time.Millisecond, // 100ms * 2^4 = 1600ms
		3200 * time.Millisecond, // 100ms * 2^5 = 3200ms
		6400 * time.Millisecond, // 100ms * 2^6 = 6400ms
		10 * time.Second,        // capped at max
		10 * time.Second,        // capped at max
	}

	for i, expected := range expectedDurations {
		actual := jb.Next()
		// Allow small tolerance due to floating point
		tolerance := time.Millisecond
		diff := actual - expected
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			t.Errorf("Next() iteration %d = %v, want %v (diff: %v)", i, actual, expected, diff)
		}
	}
}

func TestJitteredBackoff_Next_CapsAtMax(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      1 * time.Second,
		Max:       5 * time.Second,
		MinJitter: 1.0,
		MaxJitter: 1.0 + 1e-10,
	}
	jb := NewJitteredBackoffWithConfig(config)

	// Advance past the cap
	for i := 0; i < 10; i++ {
		jb.Next()
	}

	// Should be capped at max
	actual := jb.Next()
	expected := 5 * time.Second
	tolerance := time.Millisecond

	diff := actual - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Next() after many iterations = %v, want %v (capped at max)", actual, expected)
	}
}

// =============================================================================
// Jitter Range Tests
// =============================================================================

func TestJitteredBackoff_Next_JitterInRange(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       10 * time.Second,
		MinJitter: 0.5,
		MaxJitter: 1.5,
	}

	// Run multiple iterations to verify jitter stays within bounds
	for iteration := 0; iteration < 100; iteration++ {
		jb := NewJitteredBackoffWithConfig(config)

		actual := jb.Next()
		baseBackoff := 100 * time.Millisecond // First attempt: base * 2^0

		minExpected := time.Duration(float64(baseBackoff) * 0.5)
		maxExpected := time.Duration(float64(baseBackoff) * 1.5)

		if actual < minExpected || actual > maxExpected {
			t.Errorf("Next() iteration %d = %v, want in range [%v, %v]",
				iteration, actual, minExpected, maxExpected)
		}
	}
}

func TestJitteredBackoff_Next_JitterDistribution(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       10 * time.Second,
		MinJitter: 0.5,
		MaxJitter: 1.5,
	}

	// Collect many samples to verify distribution
	samples := make([]time.Duration, 1000)
	for i := 0; i < len(samples); i++ {
		jb := NewJitteredBackoffWithConfig(config)
		samples[i] = jb.Next()
	}

	// Calculate statistics
	var sum time.Duration
	minSample := samples[0]
	maxSample := samples[0]

	for _, s := range samples {
		sum += s
		if s < minSample {
			minSample = s
		}
		if s > maxSample {
			maxSample = s
		}
	}

	avg := sum / time.Duration(len(samples))
	baseBackoff := 100 * time.Millisecond

	// Average should be close to base * 1.0 (middle of [0.5, 1.5])
	expectedAvg := baseBackoff
	tolerance := 20 * time.Millisecond

	diff := avg - expectedAvg
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Average duration = %v, expected close to %v", avg, expectedAvg)
	}

	// Min should be around base * 0.5
	minExpected := time.Duration(float64(baseBackoff) * 0.5)
	if minSample > minExpected+20*time.Millisecond {
		t.Errorf("Min sample = %v, expected close to %v", minSample, minExpected)
	}

	// Max should be around base * 1.5
	maxExpected := time.Duration(float64(baseBackoff) * 1.5)
	if maxSample < maxExpected-20*time.Millisecond {
		t.Errorf("Max sample = %v, expected close to %v", maxSample, maxExpected)
	}
}

func TestJitteredBackoff_Next_CustomJitterRange(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       10 * time.Second,
		MinJitter: 0.8,
		MaxJitter: 1.2,
	}

	for iteration := 0; iteration < 100; iteration++ {
		jb := NewJitteredBackoffWithConfig(config)

		actual := jb.Next()
		baseBackoff := 100 * time.Millisecond

		minExpected := time.Duration(float64(baseBackoff) * 0.8)
		maxExpected := time.Duration(float64(baseBackoff) * 1.2)

		if actual < minExpected || actual > maxExpected {
			t.Errorf("Next() iteration %d = %v, want in range [%v, %v]",
				iteration, actual, minExpected, maxExpected)
		}
	}
}

// =============================================================================
// Reset Behavior Tests
// =============================================================================

func TestJitteredBackoff_Reset(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       10 * time.Second,
		MinJitter: 1.0,
		MaxJitter: 1.0 + 1e-10,
	}
	jb := NewJitteredBackoffWithConfig(config)

	// Advance several times
	jb.Next() // attempt 0 -> 1
	jb.Next() // attempt 1 -> 2
	jb.Next() // attempt 2 -> 3

	if jb.Attempt() != 3 {
		t.Errorf("Attempt() = %d, want 3", jb.Attempt())
	}

	// Reset
	jb.Reset()

	if jb.Attempt() != 0 {
		t.Errorf("Attempt() after Reset() = %d, want 0", jb.Attempt())
	}

	// Next should start from base again
	actual := jb.Next()
	expected := 100 * time.Millisecond
	tolerance := time.Millisecond

	diff := actual - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Next() after Reset() = %v, want %v", actual, expected)
	}
}

func TestJitteredBackoff_Reset_MultipleResets(t *testing.T) {
	t.Parallel()

	jb := NewJitteredBackoff(100*time.Millisecond, 10*time.Second)

	for cycle := 0; cycle < 5; cycle++ {
		// Advance
		for i := 0; i < 3; i++ {
			jb.Next()
		}

		// Reset
		jb.Reset()

		if jb.Attempt() != 0 {
			t.Errorf("Cycle %d: Attempt() after Reset() = %d, want 0", cycle, jb.Attempt())
		}
	}
}

// =============================================================================
// Attempt Tests
// =============================================================================

func TestJitteredBackoff_Attempt(t *testing.T) {
	t.Parallel()

	jb := NewJitteredBackoff(100*time.Millisecond, 10*time.Second)

	if jb.Attempt() != 0 {
		t.Errorf("Initial Attempt() = %d, want 0", jb.Attempt())
	}

	jb.Next()
	if jb.Attempt() != 1 {
		t.Errorf("Attempt() after 1 Next() = %d, want 1", jb.Attempt())
	}

	jb.Next()
	if jb.Attempt() != 2 {
		t.Errorf("Attempt() after 2 Next() = %d, want 2", jb.Attempt())
	}

	jb.Next()
	if jb.Attempt() != 3 {
		t.Errorf("Attempt() after 3 Next() = %d, want 3", jb.Attempt())
	}
}

func TestJitteredBackoff_Attempt_AfterReset(t *testing.T) {
	t.Parallel()

	jb := NewJitteredBackoff(100*time.Millisecond, 10*time.Second)

	jb.Next()
	jb.Next()
	jb.Reset()

	if jb.Attempt() != 0 {
		t.Errorf("Attempt() after Reset() = %d, want 0", jb.Attempt())
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestJitteredBackoff_ConcurrentNext(t *testing.T) {
	t.Parallel()

	jb := NewJitteredBackoff(100*time.Millisecond, 10*time.Second)

	var wg sync.WaitGroup
	numGoroutines := 100
	results := make([]time.Duration, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = jb.Next()
		}(i)
	}

	wg.Wait()

	// Verify all results are valid (positive durations)
	for i, d := range results {
		if d <= 0 {
			t.Errorf("Goroutine %d got invalid duration: %v", i, d)
		}
	}

	// Verify attempt count matches number of calls
	if jb.Attempt() != numGoroutines {
		t.Errorf("Attempt() = %d, want %d", jb.Attempt(), numGoroutines)
	}
}

func TestJitteredBackoff_ConcurrentReset(t *testing.T) {
	t.Parallel()

	jb := NewJitteredBackoff(100*time.Millisecond, 10*time.Second)

	var wg sync.WaitGroup
	numGoroutines := 50

	// Half do Next, half do Reset
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			jb.Next()
		}()
		go func() {
			defer wg.Done()
			jb.Reset()
		}()
	}

	wg.Wait()

	// Just verify no panic occurred and state is consistent
	attempt := jb.Attempt()
	if attempt < 0 {
		t.Errorf("Attempt() = %d, should be non-negative", attempt)
	}
}

func TestJitteredBackoff_ConcurrentAttempt(t *testing.T) {
	t.Parallel()

	jb := NewJitteredBackoff(100*time.Millisecond, 10*time.Second)

	var wg sync.WaitGroup
	numGoroutines := 100

	// Mix of Next and Attempt calls
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			jb.Next()
		}()
		go func() {
			defer wg.Done()
			_ = jb.Attempt()
		}()
	}

	wg.Wait()

	if jb.Attempt() != numGoroutines {
		t.Errorf("Attempt() = %d, want %d", jb.Attempt(), numGoroutines)
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestJitteredBackoff_EdgeCase_ZeroJitterRange(t *testing.T) {
	t.Parallel()

	// Minimal jitter range
	config := BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       10 * time.Second,
		MinJitter: 1.0,
		MaxJitter: 1.0 + 1e-15,
	}
	jb := NewJitteredBackoffWithConfig(config)

	// Should return approximately base
	actual := jb.Next()
	expected := 100 * time.Millisecond
	tolerance := time.Millisecond

	diff := actual - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Next() with minimal jitter = %v, want close to %v", actual, expected)
	}
}

func TestJitteredBackoff_EdgeCase_VerySmallDurations(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      1 * time.Nanosecond,
		Max:       100 * time.Nanosecond,
		MinJitter: 0.5,
		MaxJitter: 1.5,
	}
	jb := NewJitteredBackoffWithConfig(config)

	// Should still produce valid results
	for i := 0; i < 10; i++ {
		d := jb.Next()
		if d < 0 {
			t.Errorf("Next() iteration %d = %v, should be non-negative", i, d)
		}
	}
}

func TestJitteredBackoff_EdgeCase_VeryLargeDurations(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      1 * time.Hour,
		Max:       24 * time.Hour,
		MinJitter: 1.0,
		MaxJitter: 1.0 + 1e-10,
	}
	jb := NewJitteredBackoffWithConfig(config)

	actual := jb.Next()
	expected := 1 * time.Hour
	tolerance := time.Second

	diff := actual - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Next() with large duration = %v, want close to %v", actual, expected)
	}
}

func TestJitteredBackoff_EdgeCase_BaseEqualsMax(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      1 * time.Second,
		Max:       1 * time.Second,
		MinJitter: 1.0,
		MaxJitter: 1.0 + 1e-10,
	}
	jb := NewJitteredBackoffWithConfig(config)

	// All iterations should return approximately 1 second
	for i := 0; i < 5; i++ {
		actual := jb.Next()
		expected := 1 * time.Second
		tolerance := time.Millisecond

		diff := actual - expected
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			t.Errorf("Next() iteration %d = %v, want close to %v", i, actual, expected)
		}
	}
}

func TestJitteredBackoff_EdgeCase_BaseGreaterThanMax(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      10 * time.Second,
		Max:       1 * time.Second,
		MinJitter: 1.0,
		MaxJitter: 1.0 + 1e-10,
	}
	jb := NewJitteredBackoffWithConfig(config)

	// Should immediately cap at max
	actual := jb.Next()
	expected := 1 * time.Second
	tolerance := time.Millisecond

	diff := actual - expected
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("Next() with base > max = %v, want close to %v", actual, expected)
	}
}

// =============================================================================
// Randomness Tests
// =============================================================================

func TestJitteredBackoff_DifferentInstancesProduceDifferentValues(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       10 * time.Second,
		MinJitter: 0.5,
		MaxJitter: 1.5,
	}

	// Create multiple instances and collect their first values
	numInstances := 10
	firstValues := make([]time.Duration, numInstances)

	for i := 0; i < numInstances; i++ {
		jb := NewJitteredBackoffWithConfig(config)
		firstValues[i] = jb.Next()
	}

	// At least some values should be different
	allSame := true
	for i := 1; i < len(firstValues); i++ {
		if firstValues[i] != firstValues[0] {
			allSame = false
			break
		}
	}

	if allSame {
		t.Error("All instances produced the same first value - RNG may not be properly seeded")
	}
}

func TestJitteredBackoff_SequentialCallsProduceDifferentValues(t *testing.T) {
	t.Parallel()

	config := BackoffConfig{
		Base:      100 * time.Millisecond,
		Max:       100 * time.Millisecond, // Keep base == max to isolate jitter effect
		MinJitter: 0.5,
		MaxJitter: 1.5,
	}
	jb := NewJitteredBackoffWithConfig(config)

	// Collect multiple values
	numValues := 20
	values := make([]time.Duration, numValues)
	for i := 0; i < numValues; i++ {
		values[i] = jb.Next()
	}

	// At least some consecutive values should differ
	allSame := true
	for i := 1; i < len(values); i++ {
		if values[i] != values[i-1] {
			allSame = false
			break
		}
	}

	if allSame {
		t.Error("All sequential calls produced the same value - jitter may not be applied")
	}
}
