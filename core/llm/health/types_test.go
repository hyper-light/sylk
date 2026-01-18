package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultHealthConfig(t *testing.T) {
	cfg := DefaultHealthConfig()

	t.Run("probe settings", func(t *testing.T) {
		if cfg.ProbeInterval != 30*time.Second {
			t.Errorf("ProbeInterval = %v, want %v", cfg.ProbeInterval, 30*time.Second)
		}
		if cfg.ProbeTimeout != 5*time.Second {
			t.Errorf("ProbeTimeout = %v, want %v", cfg.ProbeTimeout, 5*time.Second)
		}
	})

	t.Run("weights", func(t *testing.T) {
		if cfg.ActiveWeight != 0.4 {
			t.Errorf("ActiveWeight = %v, want %v", cfg.ActiveWeight, 0.4)
		}
		if cfg.PassiveWeight != 0.6 {
			t.Errorf("PassiveWeight = %v, want %v", cfg.PassiveWeight, 0.6)
		}
		if sum := cfg.ActiveWeight + cfg.PassiveWeight; sum != 1.0 {
			t.Errorf("Weights sum = %v, want 1.0", sum)
		}
	})

	t.Run("passive monitoring", func(t *testing.T) {
		if cfg.WindowSize != 5*time.Minute {
			t.Errorf("WindowSize = %v, want %v", cfg.WindowSize, 5*time.Minute)
		}
		if cfg.MinSamples != 10 {
			t.Errorf("MinSamples = %v, want %v", cfg.MinSamples, 10)
		}
	})

	t.Run("thresholds ordering", func(t *testing.T) {
		if cfg.RejectThreshold != 0.3 {
			t.Errorf("RejectThreshold = %v, want %v", cfg.RejectThreshold, 0.3)
		}
		if cfg.WarnThreshold != 0.5 {
			t.Errorf("WarnThreshold = %v, want %v", cfg.WarnThreshold, 0.5)
		}
		if cfg.MonitorThreshold != 0.7 {
			t.Errorf("MonitorThreshold = %v, want %v", cfg.MonitorThreshold, 0.7)
		}
		// Verify ordering: reject < warn < monitor
		if cfg.RejectThreshold >= cfg.WarnThreshold {
			t.Errorf("RejectThreshold (%v) should be < WarnThreshold (%v)",
				cfg.RejectThreshold, cfg.WarnThreshold)
		}
		if cfg.WarnThreshold >= cfg.MonitorThreshold {
			t.Errorf("WarnThreshold (%v) should be < MonitorThreshold (%v)",
				cfg.WarnThreshold, cfg.MonitorThreshold)
		}
	})
}

func TestHealthConfig_ZeroValues(t *testing.T) {
	var cfg HealthConfig

	// Zero value config should have sensible zero values
	if cfg.ProbeInterval != 0 {
		t.Errorf("Zero ProbeInterval = %v, want 0", cfg.ProbeInterval)
	}
	if cfg.ActiveWeight != 0 {
		t.Errorf("Zero ActiveWeight = %v, want 0", cfg.ActiveWeight)
	}
	if cfg.RejectThreshold != 0 {
		t.Errorf("Zero RejectThreshold = %v, want 0", cfg.RejectThreshold)
	}
}

func TestHealthConfig_CustomValues(t *testing.T) {
	cfg := HealthConfig{
		ProbeInterval:    1 * time.Minute,
		ProbeTimeout:     10 * time.Second,
		ProbeEndpoint:    "/health",
		WindowSize:       10 * time.Minute,
		MinSamples:       20,
		ActiveWeight:     0.5,
		PassiveWeight:    0.5,
		RejectThreshold:  0.2,
		WarnThreshold:    0.4,
		MonitorThreshold: 0.6,
	}

	if cfg.ProbeEndpoint != "/health" {
		t.Errorf("ProbeEndpoint = %v, want /health", cfg.ProbeEndpoint)
	}
	if cfg.MinSamples != 20 {
		t.Errorf("MinSamples = %v, want 20", cfg.MinSamples)
	}
}

func TestHealthDecision_Fields(t *testing.T) {
	tests := []struct {
		name     string
		decision HealthDecision
	}{
		{
			name: "healthy decision",
			decision: HealthDecision{
				Proceed: true,
				Score:   0.95,
				Status:  HealthHealthy,
				Reason:  "",
			},
		},
		{
			name: "degraded decision",
			decision: HealthDecision{
				Proceed: true,
				Score:   0.45,
				Status:  HealthDegraded,
				Reason:  "provider experiencing issues",
			},
		},
		{
			name: "dead decision",
			decision: HealthDecision{
				Proceed: false,
				Score:   0.1,
				Status:  HealthDead,
				Reason:  "provider health critical: 0.10",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.decision
			if d.Proceed != tt.decision.Proceed {
				t.Errorf("Proceed = %v, want %v", d.Proceed, tt.decision.Proceed)
			}
			if d.Score != tt.decision.Score {
				t.Errorf("Score = %v, want %v", d.Score, tt.decision.Score)
			}
			if d.Status != tt.decision.Status {
				t.Errorf("Status = %v, want %v", d.Status, tt.decision.Status)
			}
			if d.Reason != tt.decision.Reason {
				t.Errorf("Reason = %v, want %v", d.Reason, tt.decision.Reason)
			}
		})
	}
}

func TestHealthStatus_Values(t *testing.T) {
	// Verify iota ordering
	if HealthHealthy != 0 {
		t.Errorf("HealthHealthy = %v, want 0", HealthHealthy)
	}
	if HealthMonitored != 1 {
		t.Errorf("HealthMonitored = %v, want 1", HealthMonitored)
	}
	if HealthDegraded != 2 {
		t.Errorf("HealthDegraded = %v, want 2", HealthDegraded)
	}
	if HealthDead != 3 {
		t.Errorf("HealthDead = %v, want 3", HealthDead)
	}
}

func TestHealthStatus_String(t *testing.T) {
	tests := []struct {
		status HealthStatus
		want   string
	}{
		{HealthHealthy, "healthy"},
		{HealthMonitored, "monitored"},
		{HealthDegraded, "degraded"},
		{HealthDead, "dead"},
		{HealthStatus(99), "unknown"},
		{HealthStatus(-1), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("HealthStatus(%d).String() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestProbeFunc_Type(t *testing.T) {
	// Verify ProbeFunc signature works as expected
	var probe ProbeFunc = func(ctx context.Context) error {
		return nil
	}

	ctx := context.Background()
	if err := probe(ctx); err != nil {
		t.Errorf("probe() error = %v, want nil", err)
	}

	// Test with error
	errProbe := func(ctx context.Context) error {
		return errors.New("probe failed")
	}

	if err := errProbe(ctx); err == nil {
		t.Error("errProbe() should return error")
	}
}

func TestProbeFunc_ContextCancellation(t *testing.T) {
	var probe ProbeFunc = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
			return nil
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := probe(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("probe() error = %v, want context.Canceled", err)
	}
}

func TestErrorCategory_Values(t *testing.T) {
	// Verify iota ordering
	if ErrNone != 0 {
		t.Errorf("ErrNone = %v, want 0", ErrNone)
	}
	if ErrTransient != 1 {
		t.Errorf("ErrTransient = %v, want 1", ErrTransient)
	}
	if ErrRateLimit != 2 {
		t.Errorf("ErrRateLimit = %v, want 2", ErrRateLimit)
	}
	if ErrAuth != 3 {
		t.Errorf("ErrAuth = %v, want 3", ErrAuth)
	}
	if ErrPermanent != 4 {
		t.Errorf("ErrPermanent = %v, want 4", ErrPermanent)
	}
}

func TestErrorCategory_String(t *testing.T) {
	tests := []struct {
		category ErrorCategory
		want     string
	}{
		{ErrNone, "none"},
		{ErrTransient, "transient"},
		{ErrRateLimit, "rate_limit"},
		{ErrAuth, "auth"},
		{ErrPermanent, "permanent"},
		{ErrorCategory(99), "unknown"},
		{ErrorCategory(-1), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.category.String(); got != tt.want {
				t.Errorf("ErrorCategory(%d).String() = %v, want %v", tt.category, got, tt.want)
			}
		})
	}
}

func TestCategorizeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorCategory
	}{
		// Nil error
		{
			name: "nil error",
			err:  nil,
			want: ErrNone,
		},

		// Rate limit errors
		{
			name: "429 status code",
			err:  errors.New("HTTP 429 Too Many Requests"),
			want: ErrRateLimit,
		},
		{
			name: "rate limit message",
			err:  errors.New("rate limit exceeded"),
			want: ErrRateLimit,
		},
		{
			name: "rate limit case insensitive",
			err:  errors.New("RATE LIMIT EXCEEDED"),
			want: ErrRateLimit,
		},

		// Auth errors
		{
			name: "401 status code",
			err:  errors.New("HTTP 401 Unauthorized"),
			want: ErrAuth,
		},
		{
			name: "403 status code",
			err:  errors.New("HTTP 403 Forbidden"),
			want: ErrAuth,
		},
		{
			name: "auth error message",
			err:  errors.New("authentication failed"),
			want: ErrAuth,
		},
		{
			name: "invalid key",
			err:  errors.New("invalid API key"),
			want: ErrAuth,
		},
		{
			name: "auth case insensitive",
			err:  errors.New("AUTHENTICATION REQUIRED"),
			want: ErrAuth,
		},

		// Transient errors
		{
			name: "timeout error",
			err:  errors.New("request timeout"),
			want: ErrTransient,
		},
		{
			name: "connection error",
			err:  errors.New("connection refused"),
			want: ErrTransient,
		},
		{
			name: "temporary error",
			err:  errors.New("temporary failure"),
			want: ErrTransient,
		},
		{
			name: "503 status code",
			err:  errors.New("HTTP 503 Service Unavailable"),
			want: ErrTransient,
		},
		{
			name: "transient case insensitive",
			err:  errors.New("CONNECTION RESET"),
			want: ErrTransient,
		},

		// Permanent errors
		{
			name: "generic error",
			err:  errors.New("some unknown error"),
			want: ErrPermanent,
		},
		{
			name: "400 status code",
			err:  errors.New("HTTP 400 Bad Request"),
			want: ErrPermanent,
		},
		{
			name: "500 status code",
			err:  errors.New("HTTP 500 Internal Server Error"),
			want: ErrPermanent,
		},
		{
			name: "empty error message",
			err:  errors.New(""),
			want: ErrPermanent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CategorizeError(tt.err)
			if got != tt.want {
				t.Errorf("CategorizeError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestCategorizeError_Priority(t *testing.T) {
	// Test that rate limit takes priority when multiple patterns match
	err := errors.New("429 rate limit with connection issues")
	if got := CategorizeError(err); got != ErrRateLimit {
		t.Errorf("Rate limit should take priority, got %v", got)
	}

	// Test that auth takes priority over transient
	err = errors.New("401 auth timeout")
	if got := CategorizeError(err); got != ErrAuth {
		t.Errorf("Auth should take priority over transient, got %v", got)
	}
}

func TestCategorizeError_WrappedErrors(t *testing.T) {
	// Test with wrapped errors
	baseErr := errors.New("rate limit exceeded")
	wrappedErr := errors.New("failed to call API: " + baseErr.Error())

	if got := CategorizeError(wrappedErr); got != ErrRateLimit {
		t.Errorf("Should detect rate limit in wrapped error, got %v", got)
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		errStr string
		want   bool
	}{
		{"429", true},
		{"rate", true},
		{"rate limit", true},
		{"HTTP 429", true},
		{"200 OK", false},
		{"timeout", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.errStr, func(t *testing.T) {
			if got := isRateLimitError(tt.errStr); got != tt.want {
				t.Errorf("isRateLimitError(%q) = %v, want %v", tt.errStr, got, tt.want)
			}
		})
	}
}

func TestIsAuthError(t *testing.T) {
	tests := []struct {
		errStr string
		want   bool
	}{
		{"401", true},
		{"403", true},
		{"auth", true},
		{"key", true},
		{"authentication required", true},
		{"invalid key", true},
		{"200 OK", false},
		{"timeout", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.errStr, func(t *testing.T) {
			if got := isAuthError(tt.errStr); got != tt.want {
				t.Errorf("isAuthError(%q) = %v, want %v", tt.errStr, got, tt.want)
			}
		})
	}
}

func TestIsTransientError(t *testing.T) {
	tests := []struct {
		errStr string
		want   bool
	}{
		{"timeout", true},
		{"connection", true},
		{"temporary", true},
		{"503", true},
		{"connection refused", true},
		{"temporary failure", true},
		{"200 OK", false},
		{"401", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.errStr, func(t *testing.T) {
			if got := isTransientError(tt.errStr); got != tt.want {
				t.Errorf("isTransientError(%q) = %v, want %v", tt.errStr, got, tt.want)
			}
		})
	}
}

func TestHealthDecision_ZeroValue(t *testing.T) {
	var d HealthDecision

	if d.Proceed {
		t.Error("Zero value Proceed should be false")
	}
	if d.Score != 0 {
		t.Errorf("Zero value Score = %v, want 0", d.Score)
	}
	if d.Status != HealthHealthy {
		t.Errorf("Zero value Status = %v, want HealthHealthy", d.Status)
	}
	if d.Reason != "" {
		t.Errorf("Zero value Reason = %v, want empty string", d.Reason)
	}
}
