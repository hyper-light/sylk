package llm

import (
	"net/http"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/signal"
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

func TestNewProviderRateLimiter(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic", nil, nil)

	if limiter.State() != RateLimitOK {
		t.Errorf("Initial state = %v, want OK", limiter.State())
	}
	if limiter.provider != "anthropic" {
		t.Errorf("provider = %v, want anthropic", limiter.provider)
	}
}

func TestProviderRateLimiterWithOptions(t *testing.T) {
	softLimitCfg := &UsageLimit{
		Requests: 500,
		Tokens:   50000,
		Period:   30 * time.Minute,
		WarnAt:   0.7,
	}

	mockBus := signal.NewSignalBus(signal.DefaultSignalBusConfig())

	limiter := NewProviderRateLimiter("openai", softLimitCfg, mockBus,
		WithBaseBackoff(2*time.Second),
		WithMaxBackoff(10*time.Minute),
	)

	if limiter.baseBackoff != 2*time.Second {
		t.Errorf("baseBackoff = %v, want 2s", limiter.baseBackoff)
	}
	if limiter.softLimit.Requests != 500 {
		t.Errorf("softLimit.Requests = %d, want 500", limiter.softLimit.Requests)
	}
	if limiter.signalBus != mockBus {
		t.Error("signalBus not set correctly")
	}
}

func TestOnResponse429(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic", nil, nil)

	headers := http.Header{}
	limiter.OnResponse(http.StatusTooManyRequests, headers)

	if limiter.State() != RateLimitExceeded {
		t.Errorf("State after 429 = %v, want Exceeded", limiter.State())
	}

	if limiter.backoffAttempt != 1 {
		t.Errorf("backoffAttempt = %d, want 1", limiter.backoffAttempt)
	}
}

func TestOnResponse429WithRetryAfter(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic", nil, nil)

	headers := http.Header{}
	headers.Set("Retry-After", "30")

	limiter.OnResponse(http.StatusTooManyRequests, headers)

	remaining := time.Until(limiter.backoffUntil)
	if remaining < 29*time.Second || remaining > 31*time.Second {
		t.Errorf("backoffUntil remaining = %v, want ~30s", remaining)
	}
}

func TestOnResponseSuccess(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic", nil, nil)

	limiter.OnResponse(http.StatusTooManyRequests, http.Header{})
	limiter.OnResponse(http.StatusOK, http.Header{})

	if limiter.State() != RateLimitOK {
		t.Errorf("State after success = %v, want OK", limiter.State())
	}
	if limiter.backoffAttempt != 0 {
		t.Errorf("backoffAttempt = %d, want 0", limiter.backoffAttempt)
	}
}

func TestCanProceedDuringBackoff(t *testing.T) {
	limiter := NewProviderRateLimiter("anthropic", nil, nil, WithBaseBackoff(50*time.Millisecond), WithMaxBackoff(50*time.Millisecond))
	limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

	if limiter.CanProceed() {
		t.Error("CanProceed should be false during backoff")
	}

	time.Sleep(60 * time.Millisecond)
	if !limiter.CanProceed() {
		t.Error("CanProceed should be true after backoff")
	}
}

func TestRecordUsageUpdatesCounters(t *testing.T) {
	limit := &UsageLimit{Requests: 10, Tokens: 100, Period: time.Minute, WarnAt: 0.8}
	limiter := NewProviderRateLimiter("test", limit, nil)

	limiter.RecordUsage(50)
	if limiter.requestCount != 1 {
		t.Errorf("requestCount = %d, want 1", limiter.requestCount)
	}
	if limiter.tokenCount != 50 {
		t.Errorf("tokenCount = %d, want 50", limiter.tokenCount)
	}
}

func TestSignalBusEmissions(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	limiter := NewProviderRateLimiter("anthropic", nil, bus)

	limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

	if limiter.State() != RateLimitExceeded {
		t.Fatalf("State = %v, want Exceeded", limiter.State())
	}

	limiter.OnResponse(http.StatusOK, http.Header{})
	if limiter.State() != RateLimitOK {
		t.Fatalf("State = %v, want OK", limiter.State())
	}
}

func TestSoftLimitWarningState(t *testing.T) {
	limit := &UsageLimit{Requests: 10, Tokens: 100, Period: time.Minute, WarnAt: 0.5}
	limiter := NewProviderRateLimiter("test", limit, nil)

	for i := 0; i < 5; i++ {
		limiter.RecordUsage(10)
	}

	if limiter.State() != RateLimitWarning {
		t.Errorf("State = %v, want Warning", limiter.State())
	}
}

func TestWarningDoesNotOverrideExceeded(t *testing.T) {
	limit := &UsageLimit{Requests: 10, Tokens: 100, Period: time.Minute, WarnAt: 0.5}
	limiter := NewProviderRateLimiter("test", limit, nil)

	limiter.OnResponse(http.StatusTooManyRequests, http.Header{})

	for i := 0; i < 10; i++ {
		limiter.RecordUsage(100)
	}

	if limiter.State() != RateLimitExceeded {
		t.Errorf("State = %v, want Exceeded", limiter.State())
	}
}

func TestPeriodReset(t *testing.T) {
	limit := &UsageLimit{Requests: 10, Tokens: 1000, Period: 50 * time.Millisecond, WarnAt: 0.8}
	limiter := NewProviderRateLimiter("test", limit, nil)

	for i := 0; i < 8; i++ {
		limiter.RecordUsage(0)
	}

	if limiter.State() != RateLimitWarning {
		t.Fatal("Should be in Warning state")
	}

	time.Sleep(60 * time.Millisecond)

	limiter.RecordUsage(0)

	if limiter.State() != RateLimitOK {
		t.Errorf("State = %v after period reset, want OK", limiter.State())
	}
}
