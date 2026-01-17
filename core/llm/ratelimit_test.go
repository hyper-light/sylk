package llm

import (
	"net/http"
	"testing"
	"time"
)

func TestRateLimitStateString(t *testing.T) {
	tests := []struct {
		state    RateLimitState
		expected string
	}{
		{RateLimitOK, "OK"},
		{RateLimitWarning, "Warning"},
		{RateLimitExceeded, "Exceeded"},
		{RateLimitState(999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.state.String()
			if got != tt.expected {
				t.Errorf("RateLimitState.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultSoftLimitConfig(t *testing.T) {
	cfg := DefaultSoftLimitConfig()

	if cfg.MaxRequests != 1000 {
		t.Errorf("MaxRequests = %d, want 1000", cfg.MaxRequests)
	}
	if cfg.MaxTokens != 100000 {
		t.Errorf("MaxTokens = %d, want 100000", cfg.MaxTokens)
	}
	if cfg.Period != time.Hour {
		t.Errorf("Period = %v, want 1h", cfg.Period)
	}
	if cfg.WarnThreshold != 0.8 {
		t.Errorf("WarnThreshold = %v, want 0.8", cfg.WarnThreshold)
	}
}

func TestNewProviderRateLimiter(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic")

	if limiter.State() != RateLimitOK {
		t.Errorf("Initial state = %v, want OK", limiter.State())
	}
	if limiter.providerName != "anthropic" {
		t.Errorf("providerName = %v, want anthropic", limiter.providerName)
	}
}

func TestProviderRateLimiterWithOptions(t *testing.T) {
	backoffCfg := BackoffConfig{
		BaseBackoff: 2 * time.Second,
		MaxBackoff:  10 * time.Minute,
	}

	softLimitCfg := SoftLimitConfig{
		MaxRequests:   500,
		MaxTokens:     50000,
		Period:        30 * time.Minute,
		WarnThreshold: 0.7,
	}

	mockBus := &mockSignalBus{}

	limiter := NewProviderRateLimiter("openai",
		WithBackoffConfig(backoffCfg),
		WithSoftLimitConfig(softLimitCfg),
		WithSignalBus(mockBus),
	)

	if limiter.backoffConfig.BaseBackoff != 2*time.Second {
		t.Errorf("backoffConfig.BaseBackoff = %v, want 2s", limiter.backoffConfig.BaseBackoff)
	}
	if limiter.softLimit.MaxRequests != 500 {
		t.Errorf("softLimit.MaxRequests = %d, want 500", limiter.softLimit.MaxRequests)
	}
	if limiter.signalBus != mockBus {
		t.Error("signalBus not set correctly")
	}
}

func TestOnResponse429(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic")

	headers := http.Header{}
	limiter.OnResponse(http.StatusTooManyRequests, headers)

	if limiter.State() != RateLimitExceeded {
		t.Errorf("State after 429 = %v, want Exceeded", limiter.State())
	}

	if limiter.attempt != 1 {
		t.Errorf("attempt = %d, want 1", limiter.attempt)
	}
}

func TestOnResponse429WithRetryAfter(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic")

	headers := http.Header{}
	headers.Set("Retry-After", "30")

	limiter.OnResponse(http.StatusTooManyRequests, headers)

	// Backoff should be approximately 30 seconds
	remaining := limiter.BackoffRemaining()
	if remaining < 29*time.Second || remaining > 31*time.Second {
		t.Errorf("BackoffRemaining() = %v, want ~30s", remaining)
	}
}

func TestOnResponseSuccess(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic")

	// First, trigger rate limit
	limiter.OnResponse(http.StatusTooManyRequests, http.Header{})
	if limiter.State() != RateLimitExceeded {
		t.Fatal("Should be Exceeded after 429")
	}

	// Then success response
	limiter.OnResponse(http.StatusOK, http.Header{})

	if limiter.State() != RateLimitOK {
		t.Errorf("State after success = %v, want OK", limiter.State())
	}
	if limiter.attempt != 0 {
		t.Errorf("attempt after success = %d, want 0", limiter.attempt)
	}
}

func TestCanProceed(t *testing.T) {
	t.Run("OK state can proceed", func(t *testing.T) {
		limiter := NewProviderRateLimiter("test")
		if !limiter.CanProceed() {
			t.Error("CanProceed() should return true in OK state")
		}
	})

	t.Run("Exceeded state cannot proceed", func(t *testing.T) {
		limiter := NewProviderRateLimiter("test")
		limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

		if limiter.CanProceed() {
			t.Error("CanProceed() should return false in Exceeded state")
		}
	})

	t.Run("Exceeded state can proceed after backoff", func(t *testing.T) {
		cfg := BackoffConfig{
			BaseBackoff: 10 * time.Millisecond,
			MaxBackoff:  100 * time.Millisecond,
		}
		limiter := NewProviderRateLimiter("test", WithBackoffConfig(cfg))
		limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

		// Wait for backoff to expire
		time.Sleep(50 * time.Millisecond)

		if !limiter.CanProceed() {
			t.Error("CanProceed() should return true after backoff expires")
		}
		if limiter.State() != RateLimitOK {
			t.Errorf("State should be OK after backoff, got %v", limiter.State())
		}
	})
}

func TestRecordUsage(t *testing.T) {
	cfg := SoftLimitConfig{
		MaxRequests:   100,
		MaxTokens:     10000,
		Period:        time.Hour,
		WarnThreshold: 0.8,
	}
	limiter := NewProviderRateLimiter("test", WithSoftLimitConfig(cfg))

	// Record usage below warning threshold
	for i := 0; i < 50; i++ {
		limiter.RecordUsage(100)
	}

	if limiter.State() != RateLimitOK {
		t.Errorf("State = %v after 50 requests, want OK", limiter.State())
	}
}

func TestRecordUsageTriggersWarning(t *testing.T) {
	cfg := SoftLimitConfig{
		MaxRequests:   100,
		MaxTokens:     10000,
		Period:        time.Hour,
		WarnThreshold: 0.8,
	}
	mockBus := &mockSignalBus{}
	limiter := NewProviderRateLimiter("test",
		WithSoftLimitConfig(cfg),
		WithSignalBus(mockBus),
	)

	// Record 80 requests to trigger warning (80% of 100)
	for i := 0; i < 80; i++ {
		limiter.RecordUsage(0)
	}

	if limiter.State() != RateLimitWarning {
		t.Errorf("State = %v after 80 requests, want Warning", limiter.State())
	}

	if mockBus.lastSignal != "SignalQuotaWarning" {
		t.Errorf("lastSignal = %v, want SignalQuotaWarning", mockBus.lastSignal)
	}
}

func TestRecordUsageTokenWarning(t *testing.T) {
	cfg := SoftLimitConfig{
		MaxRequests:   1000,
		MaxTokens:     10000,
		Period:        time.Hour,
		WarnThreshold: 0.8,
	}
	limiter := NewProviderRateLimiter("test", WithSoftLimitConfig(cfg))

	// Record tokens to reach 80% (8000 tokens)
	limiter.RecordUsage(8000)

	if limiter.State() != RateLimitWarning {
		t.Errorf("State = %v after 8000 tokens, want Warning", limiter.State())
	}
}

func TestBackoffRemaining(t *testing.T) {
	t.Run("returns 0 when not exceeded", func(t *testing.T) {
		limiter := NewProviderRateLimiter("test")
		if limiter.BackoffRemaining() != 0 {
			t.Errorf("BackoffRemaining() = %v, want 0", limiter.BackoffRemaining())
		}
	})

	t.Run("returns remaining time when exceeded", func(t *testing.T) {
		cfg := BackoffConfig{
			BaseBackoff: time.Second,
			MaxBackoff:  time.Minute,
		}
		limiter := NewProviderRateLimiter("test", WithBackoffConfig(cfg))
		limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

		remaining := limiter.BackoffRemaining()
		if remaining <= 0 {
			t.Error("BackoffRemaining() should be positive after 429")
		}
		if remaining > time.Second {
			t.Errorf("BackoffRemaining() = %v, should be <= 1s", remaining)
		}
	})
}

func TestExponentialBackoff(t *testing.T) {
	limiter := NewProviderRateLimiter("test")

	// Trigger multiple 429s
	for i := 0; i < 5; i++ {
		limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

		expectedBackoff := limiter.backoffConfig.BaseBackoff << i
		if expectedBackoff > limiter.backoffConfig.MaxBackoff {
			expectedBackoff = limiter.backoffConfig.MaxBackoff
		}

		remaining := limiter.BackoffRemaining()
		// Allow some tolerance for test execution time
		if remaining < expectedBackoff-100*time.Millisecond {
			t.Errorf("Attempt %d: BackoffRemaining() = %v, want ~%v", i, remaining, expectedBackoff)
		}

		// Reset state to allow next 429
		limiter.mu.Lock()
		limiter.state = RateLimitOK
		limiter.mu.Unlock()
	}
}

func TestWarningDoesNotOverrideExceeded(t *testing.T) {
	cfg := SoftLimitConfig{
		MaxRequests:   10,
		MaxTokens:     1000,
		Period:        time.Hour,
		WarnThreshold: 0.8,
	}
	limiter := NewProviderRateLimiter("test", WithSoftLimitConfig(cfg))

	// Trigger exceeded state
	limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

	// Record usage that would trigger warning
	for i := 0; i < 10; i++ {
		limiter.RecordUsage(100)
	}

	// Should still be exceeded
	if limiter.State() != RateLimitExceeded {
		t.Errorf("State = %v, want Exceeded (warning should not override)", limiter.State())
	}
}

func TestSignalBusEmissions(t *testing.T) {
	mockBus := &mockSignalBus{}
	limiter := NewProviderRateLimiter("anthropic", WithSignalBus(mockBus))

	t.Run("emits pause on 429", func(t *testing.T) {
		limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

		if mockBus.lastSignal != "SignalPauseAll" {
			t.Errorf("lastSignal = %v, want SignalPauseAll", mockBus.lastSignal)
		}

		payload := mockBus.lastPayload.(map[string]any)
		if payload["provider"] != "anthropic" {
			t.Errorf("provider = %v, want anthropic", payload["provider"])
		}
	})

	t.Run("emits resume on success after 429", func(t *testing.T) {
		limiter.OnResponse(http.StatusOK, http.Header{})

		if mockBus.lastSignal != "SignalResumeAll" {
			t.Errorf("lastSignal = %v, want SignalResumeAll", mockBus.lastSignal)
		}
	})
}

func TestPeriodReset(t *testing.T) {
	cfg := SoftLimitConfig{
		MaxRequests:   10,
		MaxTokens:     1000,
		Period:        50 * time.Millisecond, // Short period for testing
		WarnThreshold: 0.8,
	}
	limiter := NewProviderRateLimiter("test", WithSoftLimitConfig(cfg))

	// Record enough to trigger warning
	for i := 0; i < 8; i++ {
		limiter.RecordUsage(0)
	}

	if limiter.State() != RateLimitWarning {
		t.Fatal("Should be in Warning state")
	}

	// Wait for period to expire
	time.Sleep(60 * time.Millisecond)

	// Record again - should reset period and state
	limiter.RecordUsage(0)

	// State should be reset to OK since we're at beginning of new period
	if limiter.State() != RateLimitOK {
		t.Errorf("State = %v after period reset, want OK", limiter.State())
	}
}

// mockSignalBus implements SignalBus for testing
type mockSignalBus struct {
	lastSignal  string
	lastPayload any
}

func (m *mockSignalBus) Emit(signal string, payload any) {
	m.lastSignal = signal
	m.lastPayload = payload
}

// Test backoff.go functions
func TestCalculateBackoff(t *testing.T) {
	cfg := BackoffConfig{
		BaseBackoff: time.Second,
		MaxBackoff:  time.Minute,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, time.Second},      // 1s * 2^0 = 1s
		{1, 2 * time.Second},  // 1s * 2^1 = 2s
		{2, 4 * time.Second},  // 1s * 2^2 = 4s
		{3, 8 * time.Second},  // 1s * 2^3 = 8s
		{4, 16 * time.Second}, // 1s * 2^4 = 16s
		{5, 32 * time.Second}, // 1s * 2^5 = 32s
		{6, time.Minute},      // 1s * 2^6 = 64s > 60s, capped at max
		{10, time.Minute},     // Capped at max
		{-1, time.Second},     // Negative treated as 0
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := CalculateBackoff(cfg, tt.attempt)
			if got != tt.expected {
				t.Errorf("CalculateBackoff(cfg, %d) = %v, want %v", tt.attempt, got, tt.expected)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "30", 30 * time.Second},
		{"seconds large", "120", 120 * time.Second},
		{"invalid", "not-a-number", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.value != "" {
				headers.Set("Retry-After", tt.value)
			}

			got := ParseRetryAfter(headers)
			if got != tt.expected {
				t.Errorf("ParseRetryAfter() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	// Test with HTTP date format
	future := time.Now().Add(30 * time.Second)
	headers := http.Header{}
	headers.Set("Retry-After", future.UTC().Format(http.TimeFormat))

	got := ParseRetryAfter(headers)
	// Should be approximately 30 seconds (allow some tolerance)
	if got < 28*time.Second || got > 32*time.Second {
		t.Errorf("ParseRetryAfter(date) = %v, want ~30s", got)
	}
}

func TestDetermineBackoff(t *testing.T) {
	cfg := BackoffConfig{
		BaseBackoff: time.Second,
		MaxBackoff:  time.Minute,
	}

	t.Run("uses Retry-After when present", func(t *testing.T) {
		headers := http.Header{}
		headers.Set("Retry-After", "45")

		got := DetermineBackoff(cfg, 0, headers)
		if got != 45*time.Second {
			t.Errorf("DetermineBackoff() = %v, want 45s", got)
		}
	})

	t.Run("uses calculated backoff when no Retry-After", func(t *testing.T) {
		headers := http.Header{}

		got := DetermineBackoff(cfg, 2, headers)
		if got != 4*time.Second {
			t.Errorf("DetermineBackoff() = %v, want 4s", got)
		}
	})
}

func TestDefaultBackoffConfig(t *testing.T) {
	cfg := DefaultBackoffConfig()

	if cfg.BaseBackoff != time.Second {
		t.Errorf("BaseBackoff = %v, want 1s", cfg.BaseBackoff)
	}
	if cfg.MaxBackoff != 5*time.Minute {
		t.Errorf("MaxBackoff = %v, want 5m", cfg.MaxBackoff)
	}
}
