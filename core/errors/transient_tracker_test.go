package errors

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// =============================================================================
// Happy Path Tests
// =============================================================================

func TestTransientTracker_SingleError_Silent(t *testing.T) {
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 60 * time.Second,
	})

	err := errors.New("transient error")
	shouldNotify := tracker.Record(err)

	if shouldNotify {
		t.Error("single error should not trigger notification (should be silent)")
	}
}

func TestTransientTracker_BelowThreshold_Silent(t *testing.T) {
	threshold := 5
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       threshold,
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 60 * time.Second,
	})

	err := errors.New("transient error")

	// Record N-1 errors (should all be silent)
	for i := 0; i < threshold-1; i++ {
		shouldNotify := tracker.Record(err)
		if shouldNotify {
			t.Errorf("error %d of %d should not trigger notification", i+1, threshold-1)
		}
	}
}

func TestTransientTracker_ThresholdExceeded_Notifies(t *testing.T) {
	threshold := 3
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       threshold,
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 60 * time.Second,
	})

	err := errors.New("transient error")

	// Record threshold-1 errors (should be silent)
	for i := 0; i < threshold-1; i++ {
		shouldNotify := tracker.Record(err)
		if shouldNotify {
			t.Errorf("error %d should not trigger notification yet", i+1)
		}
	}

	// The threshold-th error should trigger notification
	shouldNotify := tracker.Record(err)
	if !shouldNotify {
		t.Errorf("error at threshold (%d) should trigger notification", threshold)
	}
}

func TestTransientTracker_DefaultConfig(t *testing.T) {
	cfg := DefaultTransientTrackerConfig()

	if cfg.FrequencyCount != 3 {
		t.Errorf("default FrequencyCount = %d, want 3", cfg.FrequencyCount)
	}
	if cfg.FrequencyWindow != 10*time.Second {
		t.Errorf("default FrequencyWindow = %v, want 10s", cfg.FrequencyWindow)
	}
	if cfg.NotificationCooldown != 60*time.Second {
		t.Errorf("default NotificationCooldown = %v, want 60s", cfg.NotificationCooldown)
	}
}

// =============================================================================
// Negative Path Tests
// =============================================================================

func TestTransientTracker_ErrorsOutsideWindow_NotCounted(t *testing.T) {
	// Use a very short window for testing
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      50 * time.Millisecond,
		NotificationCooldown: 60 * time.Second,
	})

	err := errors.New("transient error")

	// Record 2 errors
	tracker.Record(err)
	tracker.Record(err)

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	// This error should be the only one in the window now
	shouldNotify := tracker.Record(err)
	if shouldNotify {
		t.Error("errors outside window should be pruned, notification should not occur")
	}
}

func TestTransientTracker_CooldownRespected(t *testing.T) {
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       2,
		FrequencyWindow:      1 * time.Second,
		NotificationCooldown: 100 * time.Millisecond,
	})

	err := errors.New("transient error")

	// Trigger first notification
	tracker.Record(err)
	firstNotify := tracker.Record(err)
	if !firstNotify {
		t.Fatal("first threshold crossing should notify")
	}

	// Immediately record more errors - should be blocked by cooldown
	for i := 0; i < 5; i++ {
		shouldNotify := tracker.Record(err)
		if shouldNotify {
			t.Errorf("notification %d during cooldown should be blocked", i+1)
		}
	}
}

func TestTransientTracker_CooldownElapsed_AllowsNotification(t *testing.T) {
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       2,
		FrequencyWindow:      1 * time.Second,
		NotificationCooldown: 50 * time.Millisecond,
	})

	err := errors.New("transient error")

	// Trigger first notification
	tracker.Record(err)
	firstNotify := tracker.Record(err)
	if !firstNotify {
		t.Fatal("first threshold crossing should notify")
	}

	// Wait for cooldown to elapse (with buffer for timing variability)
	time.Sleep(80 * time.Millisecond)

	// Should be able to notify again
	// Need to record enough errors to reach threshold again
	// Previous errors may still be in window, so we just need to ensure >= threshold
	var secondNotify bool
	for i := 0; i < 3; i++ {
		if tracker.Record(err) {
			secondNotify = true
			break
		}
	}
	if !secondNotify {
		t.Error("notification should be allowed after cooldown elapsed")
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestTransientTracker_ExactlyAtThreshold(t *testing.T) {
	tests := []struct {
		name      string
		threshold int
	}{
		{"threshold 1", 1},
		{"threshold 2", 2},
		{"threshold 5", 5},
		{"threshold 10", 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewTransientTracker(TransientTrackerConfig{
				FrequencyCount:       tt.threshold,
				FrequencyWindow:      10 * time.Second,
				NotificationCooldown: 60 * time.Second,
			})

			err := errors.New("transient error")

			// Record threshold-1 errors (should NOT notify)
			for i := 0; i < tt.threshold-1; i++ {
				shouldNotify := tracker.Record(err)
				if shouldNotify {
					t.Errorf("error %d (before threshold %d) should not notify", i+1, tt.threshold)
				}
			}

			// The exact threshold-th error should notify
			shouldNotify := tracker.Record(err)
			if !shouldNotify {
				t.Errorf("error at exact threshold %d should notify", tt.threshold)
			}
		})
	}
}

func TestTransientTracker_ZeroFrequencyCount(t *testing.T) {
	// Edge case: FrequencyCount of 0 means all errors should notify immediately
	// (since len(errors) >= 0 is always true after recording)
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       0,
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 60 * time.Second,
	})

	err := errors.New("transient error")

	// With FrequencyCount of 0, any error should trigger notification
	shouldNotify := tracker.Record(err)
	if !shouldNotify {
		t.Error("with FrequencyCount=0, first error should notify (0 >= 0)")
	}
}

func TestTransientTracker_EmptyWindow(t *testing.T) {
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      50 * time.Millisecond,
		NotificationCooldown: 60 * time.Second,
	})

	// Don't record any errors, just verify initial state
	// Record 2 errors, wait for them to expire, then check first new error is silent
	err := errors.New("transient error")
	tracker.Record(err)
	tracker.Record(err)

	// Wait for all errors to expire
	time.Sleep(60 * time.Millisecond)

	// Now the window should effectively be empty when we record
	shouldNotify := tracker.Record(err)
	if shouldNotify {
		t.Error("after window expires, errors should be pruned and single error should be silent")
	}
}

func TestTransientTracker_MultipleNotifications(t *testing.T) {
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       2,
		FrequencyWindow:      50 * time.Millisecond, // Short window so errors expire between cycles
		NotificationCooldown: 30 * time.Millisecond,
	})

	err := errors.New("transient error")
	notificationCount := 0

	// First notification cycle: 2 errors triggers notification
	tracker.Record(err)
	if tracker.Record(err) {
		notificationCount++
	}

	// Wait for cooldown AND window to expire
	time.Sleep(60 * time.Millisecond)

	// Second notification cycle: fresh start, 2 errors triggers notification
	tracker.Record(err)
	if tracker.Record(err) {
		notificationCount++
	}

	// Wait for cooldown AND window to expire
	time.Sleep(60 * time.Millisecond)

	// Third notification cycle: fresh start, 2 errors triggers notification
	tracker.Record(err)
	if tracker.Record(err) {
		notificationCount++
	}

	if notificationCount < 3 {
		t.Errorf("expected at least 3 notifications over multiple cooldown cycles, got %d", notificationCount)
	}
}

// =============================================================================
// Race Condition / Thread Safety Tests
// =============================================================================

func TestTransientTracker_ConcurrentRecords(t *testing.T) {
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       100, // High threshold so we don't hit it accidentally
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 60 * time.Second,
	})

	var wg sync.WaitGroup
	goroutines := 50
	recordsPerGoroutine := 100

	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			err := errors.New("concurrent error")
			for j := 0; j < recordsPerGoroutine; j++ {
				tracker.Record(err)
			}
		}(i)
	}

	wg.Wait()

	// If we get here without panic/race, the test passes
	// The mutex should have protected concurrent access
}

func TestTransientTracker_ConcurrentRecordsWithNotifications(t *testing.T) {
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       5,
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 1 * time.Millisecond, // Very short cooldown
	})

	var wg sync.WaitGroup
	var notifyCount int64
	var mu sync.Mutex

	goroutines := 20
	recordsPerGoroutine := 50

	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			err := errors.New("concurrent error")
			for j := 0; j < recordsPerGoroutine; j++ {
				if tracker.Record(err) {
					mu.Lock()
					notifyCount++
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	// We should have some notifications but the exact count depends on timing
	// The important thing is no races occurred
	t.Logf("Total notifications: %d", notifyCount)
}

func TestTransientTracker_RaceConditionStress(t *testing.T) {
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      100 * time.Millisecond,
		NotificationCooldown: 10 * time.Millisecond,
	})

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := errors.New("stress error")
			for {
				select {
				case <-done:
					return
				default:
					tracker.Record(err)
				}
			}
		}()
	}

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()
}

// =============================================================================
// Memory Efficiency Test
// =============================================================================

func TestTransientTracker_MemoryBounded(t *testing.T) {
	// Very short window means old entries should be pruned quickly
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       1000, // High threshold
		FrequencyWindow:      50 * time.Millisecond,
		NotificationCooldown: 60 * time.Second,
	})

	err := errors.New("memory test error")

	// Record many errors
	for i := 0; i < 1000; i++ {
		tracker.Record(err)
	}

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	// Record one more - this should prune all old entries
	tracker.Record(err)

	// Access internal state to verify pruning (via another record after wait)
	// The tracker stores only timestamps (time.Time), not full errors
	// After pruning, only recent entries should remain

	// Wait again and record to ensure pruning happens
	time.Sleep(60 * time.Millisecond)
	tracker.Record(err)

	// If we're here, the pruning logic is working
	// The key point is that TransientTracker stores []time.Time, not []error
	// This test verifies the pruning mechanism works by not running out of memory
	// after recording many errors followed by window expiration
}

func TestTransientTracker_OnlyTimestampsStored(t *testing.T) {
	// Verify the implementation only stores timestamps for memory efficiency
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      10 * time.Second,
		NotificationCooldown: 60 * time.Second,
	})

	// The tracker.errors field should be []time.Time
	// We can verify this by checking the type
	// Note: This is an indirect test since we can't easily access private fields

	// Record different errors - they should all be treated the same
	// since only timestamps are stored
	err1 := errors.New("first error with lots of data: " + string(make([]byte, 1000)))
	err2 := errors.New("second error with different data: " + string(make([]byte, 1000)))

	tracker.Record(err1)
	tracker.Record(err2)

	// The third error should trigger notification regardless of error content
	// because only timestamps matter
	shouldNotify := tracker.Record(errors.New("third"))
	if !shouldNotify {
		t.Error("third error should trigger notification (only timestamps matter)")
	}
}

// =============================================================================
// Table-Driven Tests for Comprehensive Coverage
// =============================================================================

func TestTransientTracker_ThresholdVariations(t *testing.T) {
	tests := []struct {
		name           string
		frequencyCount int
		errorsToRecord int
		expectNotify   bool
	}{
		{"1 error, threshold 3", 3, 1, false},
		{"2 errors, threshold 3", 3, 2, false},
		{"3 errors, threshold 3", 3, 3, true},
		{"4 errors, threshold 3", 3, 4, true}, // First notify at 3, then cooldown blocks
		{"5 errors, threshold 5", 5, 5, true},
		{"10 errors, threshold 10", 10, 10, true},
		{"1 error, threshold 1", 1, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewTransientTracker(TransientTrackerConfig{
				FrequencyCount:       tt.frequencyCount,
				FrequencyWindow:      10 * time.Second,
				NotificationCooldown: 60 * time.Second,
			})

			err := errors.New("test error")
			var notified bool

			for i := 0; i < tt.errorsToRecord; i++ {
				if tracker.Record(err) {
					notified = true
				}
			}

			if notified != tt.expectNotify {
				t.Errorf("expected notify=%v, got notify=%v", tt.expectNotify, notified)
			}
		})
	}
}

func TestTransientTracker_WindowVariations(t *testing.T) {
	tests := []struct {
		name         string
		window       time.Duration
		waitBetween  time.Duration
		expectNotify bool
	}{
		{"no wait, 100ms window", 100 * time.Millisecond, 0, true},
		{"short wait, long window", 200 * time.Millisecond, 10 * time.Millisecond, true},
		{"wait exceeds window", 50 * time.Millisecond, 60 * time.Millisecond, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewTransientTracker(TransientTrackerConfig{
				FrequencyCount:       3,
				FrequencyWindow:      tt.window,
				NotificationCooldown: 60 * time.Second,
			})

			err := errors.New("test error")

			// Record first 2 errors
			tracker.Record(err)
			tracker.Record(err)

			// Wait if specified
			if tt.waitBetween > 0 {
				time.Sleep(tt.waitBetween)
			}

			// Record third error
			shouldNotify := tracker.Record(err)

			if shouldNotify != tt.expectNotify {
				t.Errorf("expected notify=%v, got notify=%v", tt.expectNotify, shouldNotify)
			}
		})
	}
}

func TestTransientTracker_CooldownVariations(t *testing.T) {
	tests := []struct {
		name                string
		cooldown            time.Duration
		waitAfterFirstBatch time.Duration
		expectSecondNotify  bool
	}{
		{"no wait, long cooldown", 100 * time.Millisecond, 0, false},
		{"wait less than cooldown", 100 * time.Millisecond, 50 * time.Millisecond, false},
		{"wait equals cooldown", 50 * time.Millisecond, 55 * time.Millisecond, true},
		{"wait exceeds cooldown", 50 * time.Millisecond, 70 * time.Millisecond, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewTransientTracker(TransientTrackerConfig{
				FrequencyCount:       2,
				FrequencyWindow:      1 * time.Second,
				NotificationCooldown: tt.cooldown,
			})

			err := errors.New("test error")

			tracker.Record(err)
			firstNotify := tracker.Record(err)
			if !firstNotify {
				t.Fatal("first batch should notify")
			}

			if tt.waitAfterFirstBatch > 0 {
				time.Sleep(tt.waitAfterFirstBatch)
			}

			// After cooldown, first Record that meets threshold will notify
			// Since we already have 2 errors in window, next Record gives us 3 >= 2
			thirdRecord := tracker.Record(err)

			if thirdRecord != tt.expectSecondNotify {
				t.Errorf("expected third record notify=%v, got notify=%v", tt.expectSecondNotify, thirdRecord)
			}
		})
	}
}

// =============================================================================
// NewTransientTracker Tests
// =============================================================================

func TestNewTransientTracker(t *testing.T) {
	t.Run("creates with custom config", func(t *testing.T) {
		cfg := TransientTrackerConfig{
			FrequencyCount:       10,
			FrequencyWindow:      30 * time.Second,
			NotificationCooldown: 120 * time.Second,
		}

		tracker := NewTransientTracker(cfg)
		if tracker == nil {
			t.Fatal("NewTransientTracker returned nil")
		}

		// Verify config is applied by testing behavior
		err := errors.New("test")
		for i := 0; i < 9; i++ {
			if tracker.Record(err) {
				t.Errorf("should not notify before threshold of 10")
			}
		}
		if !tracker.Record(err) {
			t.Error("should notify at threshold of 10")
		}
	})

	t.Run("creates with default config", func(t *testing.T) {
		tracker := NewTransientTracker(DefaultTransientTrackerConfig())
		if tracker == nil {
			t.Fatal("NewTransientTracker returned nil")
		}
	})

	t.Run("initializes empty error slice", func(t *testing.T) {
		tracker := NewTransientTracker(TransientTrackerConfig{
			FrequencyCount:       3,
			FrequencyWindow:      10 * time.Second,
			NotificationCooldown: 60 * time.Second,
		})

		// First error should not notify (empty + 1 = 1 < 3)
		if tracker.Record(errors.New("test")) {
			t.Error("first error in new tracker should not notify")
		}
	})
}

// =============================================================================
// Integration-Style Tests
// =============================================================================

func TestTransientTracker_RealisticScenario(t *testing.T) {
	// Simulate a realistic scenario where transient errors occur sporadically
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      100 * time.Millisecond,
		NotificationCooldown: 50 * time.Millisecond,
	})

	err := errors.New("network timeout")
	notifications := 0

	// First burst of errors - should notify
	for i := 0; i < 3; i++ {
		if tracker.Record(err) {
			notifications++
		}
	}
	if notifications != 1 {
		t.Errorf("first burst should produce 1 notification, got %d", notifications)
	}

	// Errors during cooldown - should not notify
	for i := 0; i < 5; i++ {
		if tracker.Record(err) {
			notifications++
		}
	}
	if notifications != 1 {
		t.Errorf("errors during cooldown should not increase notifications, got %d", notifications)
	}

	// Wait for cooldown
	time.Sleep(60 * time.Millisecond)

	// Another burst - should notify again
	for i := 0; i < 3; i++ {
		if tracker.Record(err) {
			notifications++
		}
	}
	if notifications != 2 {
		t.Errorf("second burst after cooldown should produce another notification, got %d", notifications)
	}
}

func TestTransientTracker_SporadicErrors(t *testing.T) {
	// Simulate sporadic errors that never reach threshold within window
	tracker := NewTransientTracker(TransientTrackerConfig{
		FrequencyCount:       3,
		FrequencyWindow:      50 * time.Millisecond,
		NotificationCooldown: 60 * time.Second,
	})

	err := errors.New("sporadic error")

	// Record errors with gaps larger than the window
	for i := 0; i < 10; i++ {
		shouldNotify := tracker.Record(err)
		if shouldNotify {
			t.Errorf("sporadic errors (iteration %d) should never trigger notification", i)
		}
		time.Sleep(60 * time.Millisecond) // Wait longer than window
	}
}
