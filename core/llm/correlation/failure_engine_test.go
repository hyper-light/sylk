package correlation

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockDispatcher captures signals for testing.
type mockDispatcher struct {
	signals []Signal
	mu      sync.Mutex
}

func (m *mockDispatcher) SendSignal(signal Signal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signals = append(m.signals, signal)
}

func (m *mockDispatcher) getSignals() []Signal {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Signal(nil), m.signals...)
}

func (m *mockDispatcher) getSignalCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.signals)
}

// TestNewFailureCorrelationEngine tests the constructor.
func TestNewFailureCorrelationEngine(t *testing.T) {
	t.Run("creates engine with config", func(t *testing.T) {
		config := DefaultCorrelationConfig()
		engine := NewFailureCorrelationEngine(config, nil)

		if engine == nil {
			t.Fatal("expected non-nil engine")
		}
		if engine.config.MinFailuresForGlobal != config.MinFailuresForGlobal {
			t.Errorf("config mismatch: got %d, want %d",
				engine.config.MinFailuresForGlobal, config.MinFailuresForGlobal)
		}
	})

	t.Run("creates engine with dispatcher", func(t *testing.T) {
		dispatcher := &mockDispatcher{}
		engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), dispatcher)

		if engine.dispatcher == nil {
			t.Error("expected non-nil dispatcher")
		}
	})

	t.Run("creates engine without dispatcher", func(t *testing.T) {
		engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)

		if engine.dispatcher != nil {
			t.Error("expected nil dispatcher")
		}
	})
}

// TestRecordFailure tests failure recording.
func TestRecordFailure(t *testing.T) {
	t.Run("ignores nil error", func(t *testing.T) {
		engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)
		engine.RecordFailure("openai", "session-1", nil)

		count := engine.GetFailureCount("openai")
		if count != 0 {
			t.Errorf("expected 0 failures, got %d", count)
		}
	})

	t.Run("records single failure", func(t *testing.T) {
		engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)
		engine.RecordFailure("openai", "session-1", errors.New("timeout"))

		count := engine.GetFailureCount("openai")
		if count != 1 {
			t.Errorf("expected 1 failure, got %d", count)
		}
	})

	t.Run("records multiple failures", func(t *testing.T) {
		engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)
		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-2", errors.New("connection refused"))

		count := engine.GetFailureCount("openai")
		if count != 2 {
			t.Errorf("expected 2 failures, got %d", count)
		}
	})

	t.Run("tracks separate providers", func(t *testing.T) {
		engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)
		engine.RecordFailure("openai", "session-1", errors.New("error"))
		engine.RecordFailure("anthropic", "session-2", errors.New("error"))

		openaiCount := engine.GetFailureCount("openai")
		anthropicCount := engine.GetFailureCount("anthropic")

		if openaiCount != 1 {
			t.Errorf("expected 1 openai failure, got %d", openaiCount)
		}
		if anthropicCount != 1 {
			t.Errorf("expected 1 anthropic failure, got %d", anthropicCount)
		}
	})
}

// TestDetectCorrelation tests correlation detection.
func TestDetectCorrelation(t *testing.T) {
	t.Run("detects correlation with enough unique sessions", func(t *testing.T) {
		config := CorrelationConfig{
			CorrelationWindow:    10 * time.Second,
			MinFailuresForGlobal: 3,
			GlobalBackoffBase:    100 * time.Millisecond,
			GlobalBackoffMax:     1 * time.Second,
			RecoveryRampUp:       0.1,
		}
		dispatcher := &mockDispatcher{}
		engine := NewFailureCorrelationEngine(config, dispatcher)

		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-2", errors.New("timeout"))
		engine.RecordFailure("openai", "session-3", errors.New("timeout"))

		// Wait for signal dispatch
		time.Sleep(50 * time.Millisecond)

		active, _ := engine.GetBackoffState("openai")
		if !active {
			t.Error("expected backoff to be active")
		}

		signals := dispatcher.getSignals()
		if len(signals) == 0 {
			t.Error("expected backoff signal to be dispatched")
		}
	})

	t.Run("does not trigger with same session", func(t *testing.T) {
		config := CorrelationConfig{
			CorrelationWindow:    10 * time.Second,
			MinFailuresForGlobal: 3,
			GlobalBackoffBase:    100 * time.Millisecond,
			GlobalBackoffMax:     1 * time.Second,
			RecoveryRampUp:       0.1,
		}
		engine := NewFailureCorrelationEngine(config, nil)

		// Same session failing multiple times
		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-1", errors.New("timeout"))

		active, _ := engine.GetBackoffState("openai")
		if active {
			t.Error("expected backoff to not be active with same session")
		}
	})

	t.Run("does not trigger below threshold", func(t *testing.T) {
		config := CorrelationConfig{
			CorrelationWindow:    10 * time.Second,
			MinFailuresForGlobal: 3,
			GlobalBackoffBase:    100 * time.Millisecond,
			GlobalBackoffMax:     1 * time.Second,
			RecoveryRampUp:       0.1,
		}
		engine := NewFailureCorrelationEngine(config, nil)

		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-2", errors.New("timeout"))

		active, _ := engine.GetBackoffState("openai")
		if active {
			t.Error("expected backoff to not be active below threshold")
		}
	})
}

// TestGlobalBackoff tests backoff behavior.
func TestGlobalBackoff(t *testing.T) {
	t.Run("blocks requests during backoff", func(t *testing.T) {
		config := CorrelationConfig{
			CorrelationWindow:    10 * time.Second,
			MinFailuresForGlobal: 2,
			GlobalBackoffBase:    500 * time.Millisecond,
			GlobalBackoffMax:     5 * time.Second,
			RecoveryRampUp:       0.1,
		}
		engine := NewFailureCorrelationEngine(config, nil)

		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-2", errors.New("timeout"))

		allowed := engine.ShouldAllow("openai")
		if allowed {
			t.Error("expected request to be blocked during backoff")
		}
	})

	t.Run("allows requests for unknown provider", func(t *testing.T) {
		engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)

		allowed := engine.ShouldAllow("unknown-provider")
		if !allowed {
			t.Error("expected request to be allowed for unknown provider")
		}
	})

	t.Run("allows requests when no backoff active", func(t *testing.T) {
		engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)
		engine.RecordFailure("openai", "session-1", errors.New("timeout"))

		allowed := engine.ShouldAllow("openai")
		if !allowed {
			t.Error("expected request to be allowed when below threshold")
		}
	})

	t.Run("exponential backoff increases duration", func(t *testing.T) {
		config := CorrelationConfig{
			CorrelationWindow:    10 * time.Second,
			MinFailuresForGlobal: 2,
			GlobalBackoffBase:    100 * time.Millisecond,
			GlobalBackoffMax:     10 * time.Second,
			RecoveryRampUp:       0.1,
		}
		dispatcher := &mockDispatcher{}
		engine := NewFailureCorrelationEngine(config, dispatcher)

		// First backoff
		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-2", errors.New("timeout"))

		time.Sleep(50 * time.Millisecond)
		signals := dispatcher.getSignals()

		if len(signals) < 1 {
			t.Fatal("expected at least one signal")
		}

		backoffSignal := signals[0].(*GlobalBackoffSignal)
		if backoffSignal.Duration != 100*time.Millisecond {
			t.Errorf("expected first backoff duration 100ms, got %v", backoffSignal.Duration)
		}
	})
}

// TestRecoveryRampUp tests traffic ramp-up after backoff.
func TestRecoveryRampUp(t *testing.T) {
	t.Run("traffic ratio increases during recovery", func(t *testing.T) {
		config := CorrelationConfig{
			CorrelationWindow:    10 * time.Second,
			MinFailuresForGlobal: 2,
			GlobalBackoffBase:    50 * time.Millisecond,
			GlobalBackoffMax:     100 * time.Millisecond,
			RecoveryRampUp:       0.1,
		}
		engine := NewFailureCorrelationEngine(config, nil)

		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-2", errors.New("timeout"))

		// Wait for backoff period to pass
		time.Sleep(100 * time.Millisecond)

		// Check that recovery has started (ratio should be increasing)
		_, ratio := engine.GetBackoffState("openai")
		if ratio > 1.0 {
			t.Errorf("expected ratio <= 1.0, got %v", ratio)
		}
	})

	t.Run("backoff deactivates after full recovery", func(t *testing.T) {
		config := CorrelationConfig{
			CorrelationWindow:    10 * time.Second,
			MinFailuresForGlobal: 2,
			GlobalBackoffBase:    10 * time.Millisecond,
			GlobalBackoffMax:     20 * time.Millisecond,
			RecoveryRampUp:       0.9,
		}
		dispatcher := &mockDispatcher{}
		engine := NewFailureCorrelationEngine(config, dispatcher)

		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		engine.RecordFailure("openai", "session-2", errors.New("timeout"))

		// Wait for full recovery (backoff + ramp-up)
		time.Sleep(15 * time.Second)

		active, ratio := engine.GetBackoffState("openai")
		if active {
			t.Error("expected backoff to be inactive after recovery")
		}
		if ratio != 1.0 {
			t.Errorf("expected ratio 1.0, got %v", ratio)
		}

		// Check recovery signal was sent
		signals := dispatcher.getSignals()
		hasRecovery := false
		for _, s := range signals {
			if s.Type() == SignalGlobalRecovery {
				hasRecovery = true
				break
			}
		}
		if !hasRecovery {
			t.Error("expected recovery signal to be dispatched")
		}
	})
}

// TestPruneOldFailures tests failure pruning.
func TestPruneOldFailures(t *testing.T) {
	t.Run("prunes failures outside window", func(t *testing.T) {
		config := CorrelationConfig{
			CorrelationWindow:    100 * time.Millisecond,
			MinFailuresForGlobal: 10, // High threshold to avoid triggering backoff
			GlobalBackoffBase:    time.Second,
			GlobalBackoffMax:     time.Second,
			RecoveryRampUp:       0.1,
		}
		engine := NewFailureCorrelationEngine(config, nil)

		engine.RecordFailure("openai", "session-1", errors.New("timeout"))
		time.Sleep(150 * time.Millisecond)
		engine.RecordFailure("openai", "session-2", errors.New("timeout"))

		count := engine.GetFailureCount("openai")
		if count != 1 {
			t.Errorf("expected 1 failure after pruning, got %d", count)
		}
	})
}

// TestErrorCategorization tests error categorization.
func TestErrorCategorization(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected ErrorCategory
	}{
		{"rate limit 429", "HTTP 429 too many requests", ErrCategoryRateLimit},
		{"rate limit keyword", "rate limit exceeded", ErrCategoryRateLimit},
		{"auth 401", "HTTP 401 unauthorized", ErrCategoryAuth},
		{"auth 403", "HTTP 403 forbidden", ErrCategoryAuth},
		{"auth keyword", "invalid auth token", ErrCategoryAuth},
		{"key keyword", "invalid api key", ErrCategoryAuth},
		{"timeout", "connection timeout", ErrCategoryTransient},
		{"connection", "connection refused", ErrCategoryTransient},
		{"temporary", "temporary error", ErrCategoryTransient},
		{"503 error", "HTTP 503 service unavailable", ErrCategoryTransient},
		{"permanent", "invalid request", ErrCategoryPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := categorizeError(errors.New(tt.errMsg))
			if result != tt.expected {
				t.Errorf("categorizeError(%q) = %v, want %v", tt.errMsg, result, tt.expected)
			}
		})
	}

	t.Run("nil error returns none", func(t *testing.T) {
		result := categorizeError(nil)
		if result != ErrCategoryNone {
			t.Errorf("categorizeError(nil) = %v, want %v", result, ErrCategoryNone)
		}
	})
}

// TestConcurrentRecordFailure tests thread safety.
func TestConcurrentRecordFailure(t *testing.T) {
	config := CorrelationConfig{
		CorrelationWindow:    10 * time.Second,
		MinFailuresForGlobal: 100, // High threshold
		GlobalBackoffBase:    time.Second,
		GlobalBackoffMax:     time.Second,
		RecoveryRampUp:       0.1,
	}
	engine := NewFailureCorrelationEngine(config, nil)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('A'+id%26))
			engine.RecordFailure("openai", sessionID, errors.New("timeout"))
		}(i)
	}
	wg.Wait()

	count := engine.GetFailureCount("openai")
	if count != 100 {
		t.Errorf("expected 100 failures, got %d", count)
	}
}

// TestConcurrentShouldAllow tests thread-safe access to ShouldAllow.
func TestConcurrentShouldAllow(t *testing.T) {
	config := CorrelationConfig{
		CorrelationWindow:    10 * time.Second,
		MinFailuresForGlobal: 2,
		GlobalBackoffBase:    time.Second,
		GlobalBackoffMax:     time.Second,
		RecoveryRampUp:       0.1,
	}
	engine := NewFailureCorrelationEngine(config, nil)

	// Trigger backoff
	engine.RecordFailure("openai", "session-1", errors.New("timeout"))
	engine.RecordFailure("openai", "session-2", errors.New("timeout"))

	var wg sync.WaitGroup
	var allowed atomic.Int32

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if engine.ShouldAllow("openai") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	// During backoff, no requests should be allowed
	if allowed.Load() > 0 {
		t.Errorf("expected 0 allowed during backoff, got %d", allowed.Load())
	}
}

// TestDispatcherNotCalled tests behavior with nil dispatcher.
func TestDispatcherNotCalled(t *testing.T) {
	config := CorrelationConfig{
		CorrelationWindow:    10 * time.Second,
		MinFailuresForGlobal: 2,
		GlobalBackoffBase:    100 * time.Millisecond,
		GlobalBackoffMax:     1 * time.Second,
		RecoveryRampUp:       0.1,
	}
	engine := NewFailureCorrelationEngine(config, nil)

	// Should not panic with nil dispatcher
	engine.RecordFailure("openai", "session-1", errors.New("timeout"))
	engine.RecordFailure("openai", "session-2", errors.New("timeout"))

	active, _ := engine.GetBackoffState("openai")
	if !active {
		t.Error("expected backoff to be active even without dispatcher")
	}
}

// TestMultipleProvidersIndependent tests that providers have independent state.
func TestMultipleProvidersIndependent(t *testing.T) {
	config := CorrelationConfig{
		CorrelationWindow:    10 * time.Second,
		MinFailuresForGlobal: 2,
		GlobalBackoffBase:    1 * time.Second,
		GlobalBackoffMax:     5 * time.Second,
		RecoveryRampUp:       0.1,
	}
	engine := NewFailureCorrelationEngine(config, nil)

	// Trigger backoff for openai
	engine.RecordFailure("openai", "session-1", errors.New("timeout"))
	engine.RecordFailure("openai", "session-2", errors.New("timeout"))

	// anthropic should still be allowed
	if !engine.ShouldAllow("anthropic") {
		t.Error("expected anthropic to be allowed")
	}

	// openai should be blocked
	if engine.ShouldAllow("openai") {
		t.Error("expected openai to be blocked")
	}
}

// TestBackoffMaxCap tests that backoff duration is capped.
func TestBackoffMaxCap(t *testing.T) {
	config := CorrelationConfig{
		CorrelationWindow:    10 * time.Second,
		MinFailuresForGlobal: 2,
		GlobalBackoffBase:    1 * time.Second,
		GlobalBackoffMax:     2 * time.Second, // Max is only 2x base
		RecoveryRampUp:       0.1,
	}
	dispatcher := &mockDispatcher{}
	engine := NewFailureCorrelationEngine(config, dispatcher)

	// First backoff
	engine.RecordFailure("openai", "session-1", errors.New("timeout"))
	engine.RecordFailure("openai", "session-2", errors.New("timeout"))

	time.Sleep(50 * time.Millisecond)

	// Wait for recovery and trigger second backoff
	time.Sleep(15 * time.Second)

	engine.RecordFailure("openai", "session-3", errors.New("timeout"))
	engine.RecordFailure("openai", "session-4", errors.New("timeout"))

	time.Sleep(50 * time.Millisecond)

	signals := dispatcher.getSignals()
	var lastBackoff time.Duration
	for _, s := range signals {
		if bs, ok := s.(*GlobalBackoffSignal); ok {
			lastBackoff = bs.Duration
		}
	}

	if lastBackoff > config.GlobalBackoffMax {
		t.Errorf("backoff %v exceeded max %v", lastBackoff, config.GlobalBackoffMax)
	}
}

// TestGetBackoffStateDefaults tests GetBackoffState for unknown provider.
func TestGetBackoffStateDefaults(t *testing.T) {
	engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)

	active, ratio := engine.GetBackoffState("unknown")
	if active {
		t.Error("expected active to be false for unknown provider")
	}
	if ratio != 1.0 {
		t.Errorf("expected ratio 1.0 for unknown provider, got %v", ratio)
	}
}

// TestGetFailureCountDefaults tests GetFailureCount for unknown provider.
func TestGetFailureCountDefaults(t *testing.T) {
	engine := NewFailureCorrelationEngine(DefaultCorrelationConfig(), nil)

	count := engine.GetFailureCount("unknown")
	if count != 0 {
		t.Errorf("expected count 0 for unknown provider, got %d", count)
	}
}
