package correlation

import (
	"testing"
	"time"
)

// TestErrorCategoryString tests the String method of ErrorCategory.
func TestErrorCategoryString(t *testing.T) {
	tests := []struct {
		name     string
		category ErrorCategory
		want     string
	}{
		{
			name:     "none",
			category: ErrCategoryNone,
			want:     "none",
		},
		{
			name:     "transient",
			category: ErrCategoryTransient,
			want:     "transient",
		},
		{
			name:     "rate_limit",
			category: ErrCategoryRateLimit,
			want:     "rate_limit",
		},
		{
			name:     "auth",
			category: ErrCategoryAuth,
			want:     "auth",
		},
		{
			name:     "permanent",
			category: ErrCategoryPermanent,
			want:     "permanent",
		},
		{
			name:     "unknown",
			category: ErrorCategory(99),
			want:     "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.category.String(); got != tt.want {
				t.Errorf("ErrorCategory.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestErrorCategoryIsRetryable tests the IsRetryable method of ErrorCategory.
func TestErrorCategoryIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		category ErrorCategory
		want     bool
	}{
		{
			name:     "none is not retryable",
			category: ErrCategoryNone,
			want:     false,
		},
		{
			name:     "transient is retryable",
			category: ErrCategoryTransient,
			want:     true,
		},
		{
			name:     "rate_limit is retryable",
			category: ErrCategoryRateLimit,
			want:     true,
		},
		{
			name:     "auth is not retryable",
			category: ErrCategoryAuth,
			want:     false,
		},
		{
			name:     "permanent is not retryable",
			category: ErrCategoryPermanent,
			want:     false,
		},
		{
			name:     "unknown category is not retryable",
			category: ErrorCategory(99),
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.category.IsRetryable(); got != tt.want {
				t.Errorf("ErrorCategory.IsRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDefaultCorrelationConfig tests DefaultCorrelationConfig returns sensible defaults.
func TestDefaultCorrelationConfig(t *testing.T) {
	cfg := DefaultCorrelationConfig()

	if cfg.CorrelationWindow != 10*time.Second {
		t.Errorf("CorrelationWindow = %v, want %v", cfg.CorrelationWindow, 10*time.Second)
	}
	if cfg.MinFailuresForGlobal != 3 {
		t.Errorf("MinFailuresForGlobal = %v, want 3", cfg.MinFailuresForGlobal)
	}
	if cfg.GlobalBackoffBase != 5*time.Second {
		t.Errorf("GlobalBackoffBase = %v, want %v", cfg.GlobalBackoffBase, 5*time.Second)
	}
	if cfg.GlobalBackoffMax != 60*time.Second {
		t.Errorf("GlobalBackoffMax = %v, want %v", cfg.GlobalBackoffMax, 60*time.Second)
	}
	if cfg.RecoveryRampUp != 0.1 {
		t.Errorf("RecoveryRampUp = %v, want 0.1", cfg.RecoveryRampUp)
	}
}

// TestCorrelationConfigValidate tests the Validate method of CorrelationConfig.
func TestCorrelationConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  CorrelationConfig
		wantErr error
	}{
		{
			name:    "default config is valid",
			config:  DefaultCorrelationConfig(),
			wantErr: nil,
		},
		{
			name: "zero values where valid",
			config: CorrelationConfig{
				CorrelationWindow:    0,
				MinFailuresForGlobal: 1,
				GlobalBackoffBase:    0,
				GlobalBackoffMax:     0,
				RecoveryRampUp:       0,
			},
			wantErr: nil,
		},
		{
			name: "recovery ramp up at 1.0 is valid",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: 1,
				GlobalBackoffBase:    time.Second,
				GlobalBackoffMax:     time.Second,
				RecoveryRampUp:       1.0,
			},
			wantErr: nil,
		},
		{
			name: "negative correlation window",
			config: CorrelationConfig{
				CorrelationWindow:    -time.Second,
				MinFailuresForGlobal: 3,
				GlobalBackoffBase:    time.Second,
				GlobalBackoffMax:     time.Minute,
				RecoveryRampUp:       0.1,
			},
			wantErr: ErrInvalidCorrelationWindow,
		},
		{
			name: "zero min failures",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: 0,
				GlobalBackoffBase:    time.Second,
				GlobalBackoffMax:     time.Minute,
				RecoveryRampUp:       0.1,
			},
			wantErr: ErrInvalidMinFailures,
		},
		{
			name: "negative min failures",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: -1,
				GlobalBackoffBase:    time.Second,
				GlobalBackoffMax:     time.Minute,
				RecoveryRampUp:       0.1,
			},
			wantErr: ErrInvalidMinFailures,
		},
		{
			name: "negative backoff base",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: 3,
				GlobalBackoffBase:    -time.Second,
				GlobalBackoffMax:     time.Minute,
				RecoveryRampUp:       0.1,
			},
			wantErr: ErrInvalidBackoffBase,
		},
		{
			name: "backoff max less than base",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: 3,
				GlobalBackoffBase:    time.Minute,
				GlobalBackoffMax:     time.Second,
				RecoveryRampUp:       0.1,
			},
			wantErr: ErrInvalidBackoffMax,
		},
		{
			name: "negative recovery ramp up",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: 3,
				GlobalBackoffBase:    time.Second,
				GlobalBackoffMax:     time.Minute,
				RecoveryRampUp:       -0.1,
			},
			wantErr: ErrInvalidRecoveryRampUp,
		},
		{
			name: "recovery ramp up above 1",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: 3,
				GlobalBackoffBase:    time.Second,
				GlobalBackoffMax:     time.Minute,
				RecoveryRampUp:       1.1,
			},
			wantErr: ErrInvalidRecoveryRampUp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if err != tt.wantErr {
				t.Errorf("CorrelationConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestNewFailureEvent tests the NewFailureEvent constructor.
func TestNewFailureEvent(t *testing.T) {
	before := time.Now()
	event := NewFailureEvent("session-123", ErrCategoryRateLimit)
	after := time.Now()

	if event.SessionID != "session-123" {
		t.Errorf("SessionID = %v, want session-123", event.SessionID)
	}
	if event.ErrorType != ErrCategoryRateLimit {
		t.Errorf("ErrorType = %v, want %v", event.ErrorType, ErrCategoryRateLimit)
	}
	if event.Timestamp.Before(before) || event.Timestamp.After(after) {
		t.Errorf("Timestamp = %v, want between %v and %v", event.Timestamp, before, after)
	}
}

// TestFailureEventIsWithinWindow tests the IsWithinWindow method.
func TestFailureEventIsWithinWindow(t *testing.T) {
	tests := []struct {
		name   string
		event  FailureEvent
		window time.Duration
		want   bool
	}{
		{
			name: "recent event within window",
			event: FailureEvent{
				Timestamp: time.Now().Add(-time.Second),
				SessionID: "session-1",
				ErrorType: ErrCategoryTransient,
			},
			window: 10 * time.Second,
			want:   true,
		},
		{
			name: "old event outside window",
			event: FailureEvent{
				Timestamp: time.Now().Add(-time.Minute),
				SessionID: "session-1",
				ErrorType: ErrCategoryTransient,
			},
			window: 10 * time.Second,
			want:   false,
		},
		{
			name: "event exactly at window boundary is outside",
			event: FailureEvent{
				Timestamp: time.Now().Add(-10 * time.Second),
				SessionID: "session-1",
				ErrorType: ErrCategoryTransient,
			},
			window: 10 * time.Second,
			want:   false,
		},
		{
			name: "zero window",
			event: FailureEvent{
				Timestamp: time.Now(),
				SessionID: "session-1",
				ErrorType: ErrCategoryTransient,
			},
			window: 0,
			want:   false,
		},
		{
			name: "future event is within window",
			event: FailureEvent{
				Timestamp: time.Now().Add(time.Second),
				SessionID: "session-1",
				ErrorType: ErrCategoryTransient,
			},
			window: 10 * time.Second,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.IsWithinWindow(tt.window); got != tt.want {
				t.Errorf("FailureEvent.IsWithinWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSignalTypeString tests the String method of SignalType.
func TestSignalTypeString(t *testing.T) {
	tests := []struct {
		name       string
		signalType SignalType
		want       string
	}{
		{
			name:       "global backoff",
			signalType: SignalGlobalBackoff,
			want:       "global_backoff",
		},
		{
			name:       "global recovery",
			signalType: SignalGlobalRecovery,
			want:       "global_recovery",
		},
		{
			name:       "unknown",
			signalType: SignalType(99),
			want:       "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.signalType.String(); got != tt.want {
				t.Errorf("SignalType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGlobalBackoffSignal tests the GlobalBackoffSignal type.
func TestGlobalBackoffSignal(t *testing.T) {
	t.Run("constructor and getters", func(t *testing.T) {
		signal := NewGlobalBackoffSignal("openai", 30*time.Second)

		if signal.Provider() != "openai" {
			t.Errorf("Provider() = %v, want openai", signal.Provider())
		}
		if signal.Type() != SignalGlobalBackoff {
			t.Errorf("Type() = %v, want %v", signal.Type(), SignalGlobalBackoff)
		}
		if signal.Duration != 30*time.Second {
			t.Errorf("Duration = %v, want %v", signal.Duration, 30*time.Second)
		}
	})

	t.Run("implements Signal interface", func(t *testing.T) {
		var _ Signal = NewGlobalBackoffSignal("test", time.Second)
	})

	t.Run("empty provider", func(t *testing.T) {
		signal := NewGlobalBackoffSignal("", time.Second)
		if signal.Provider() != "" {
			t.Errorf("Provider() = %v, want empty string", signal.Provider())
		}
	})

	t.Run("zero duration", func(t *testing.T) {
		signal := NewGlobalBackoffSignal("provider", 0)
		if signal.Duration != 0 {
			t.Errorf("Duration = %v, want 0", signal.Duration)
		}
	})

	t.Run("negative duration", func(t *testing.T) {
		signal := NewGlobalBackoffSignal("provider", -time.Second)
		if signal.Duration != -time.Second {
			t.Errorf("Duration = %v, want %v", signal.Duration, -time.Second)
		}
	})
}

// TestGlobalRecoverySignal tests the GlobalRecoverySignal type.
func TestGlobalRecoverySignal(t *testing.T) {
	t.Run("constructor and getters", func(t *testing.T) {
		signal := NewGlobalRecoverySignal("anthropic")

		if signal.Provider() != "anthropic" {
			t.Errorf("Provider() = %v, want anthropic", signal.Provider())
		}
		if signal.Type() != SignalGlobalRecovery {
			t.Errorf("Type() = %v, want %v", signal.Type(), SignalGlobalRecovery)
		}
	})

	t.Run("implements Signal interface", func(t *testing.T) {
		var _ Signal = NewGlobalRecoverySignal("test")
	})

	t.Run("empty provider", func(t *testing.T) {
		signal := NewGlobalRecoverySignal("")
		if signal.Provider() != "" {
			t.Errorf("Provider() = %v, want empty string", signal.Provider())
		}
	})
}

// TestSignalInterfacePolymorphism tests that both signal types work correctly with the interface.
func TestSignalInterfacePolymorphism(t *testing.T) {
	signals := []Signal{
		NewGlobalBackoffSignal("provider1", time.Minute),
		NewGlobalRecoverySignal("provider2"),
	}

	expectedTypes := []SignalType{SignalGlobalBackoff, SignalGlobalRecovery}
	expectedProviders := []string{"provider1", "provider2"}

	for i, signal := range signals {
		if signal.Type() != expectedTypes[i] {
			t.Errorf("Signal[%d].Type() = %v, want %v", i, signal.Type(), expectedTypes[i])
		}
		if signal.Provider() != expectedProviders[i] {
			t.Errorf("Signal[%d].Provider() = %v, want %v", i, signal.Provider(), expectedProviders[i])
		}
	}
}

// TestFailureEventFields tests direct field access on FailureEvent.
func TestFailureEventFields(t *testing.T) {
	now := time.Now()
	event := FailureEvent{
		Timestamp: now,
		SessionID: "test-session",
		ErrorType: ErrCategoryAuth,
	}

	if event.Timestamp != now {
		t.Errorf("Timestamp = %v, want %v", event.Timestamp, now)
	}
	if event.SessionID != "test-session" {
		t.Errorf("SessionID = %v, want test-session", event.SessionID)
	}
	if event.ErrorType != ErrCategoryAuth {
		t.Errorf("ErrorType = %v, want %v", event.ErrorType, ErrCategoryAuth)
	}
}

// TestCorrelationConfigFields tests direct field access on CorrelationConfig.
func TestCorrelationConfigFields(t *testing.T) {
	cfg := CorrelationConfig{
		CorrelationWindow:    15 * time.Second,
		MinFailuresForGlobal: 5,
		GlobalBackoffBase:    10 * time.Second,
		GlobalBackoffMax:     2 * time.Minute,
		RecoveryRampUp:       0.2,
	}

	if cfg.CorrelationWindow != 15*time.Second {
		t.Errorf("CorrelationWindow = %v, want %v", cfg.CorrelationWindow, 15*time.Second)
	}
	if cfg.MinFailuresForGlobal != 5 {
		t.Errorf("MinFailuresForGlobal = %v, want 5", cfg.MinFailuresForGlobal)
	}
	if cfg.GlobalBackoffBase != 10*time.Second {
		t.Errorf("GlobalBackoffBase = %v, want %v", cfg.GlobalBackoffBase, 10*time.Second)
	}
	if cfg.GlobalBackoffMax != 2*time.Minute {
		t.Errorf("GlobalBackoffMax = %v, want %v", cfg.GlobalBackoffMax, 2*time.Minute)
	}
	if cfg.RecoveryRampUp != 0.2 {
		t.Errorf("RecoveryRampUp = %v, want 0.2", cfg.RecoveryRampUp)
	}
}

// TestErrorCategoryConstants tests the numeric values of error category constants.
func TestErrorCategoryConstants(t *testing.T) {
	// Verify constants have expected values (iota order)
	if ErrCategoryNone != 0 {
		t.Errorf("ErrCategoryNone = %v, want 0", ErrCategoryNone)
	}
	if ErrCategoryTransient != 1 {
		t.Errorf("ErrCategoryTransient = %v, want 1", ErrCategoryTransient)
	}
	if ErrCategoryRateLimit != 2 {
		t.Errorf("ErrCategoryRateLimit = %v, want 2", ErrCategoryRateLimit)
	}
	if ErrCategoryAuth != 3 {
		t.Errorf("ErrCategoryAuth = %v, want 3", ErrCategoryAuth)
	}
	if ErrCategoryPermanent != 4 {
		t.Errorf("ErrCategoryPermanent = %v, want 4", ErrCategoryPermanent)
	}
}

// TestSignalTypeConstants tests the numeric values of signal type constants.
func TestSignalTypeConstants(t *testing.T) {
	if SignalGlobalBackoff != 0 {
		t.Errorf("SignalGlobalBackoff = %v, want 0", SignalGlobalBackoff)
	}
	if SignalGlobalRecovery != 1 {
		t.Errorf("SignalGlobalRecovery = %v, want 1", SignalGlobalRecovery)
	}
}

// TestCorrelationConfigValidateWindow tests the validateWindow helper method.
func TestCorrelationConfigValidateWindow(t *testing.T) {
	tests := []struct {
		name    string
		config  CorrelationConfig
		wantErr error
	}{
		{
			name: "valid window settings",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: 1,
			},
			wantErr: nil,
		},
		{
			name: "zero window is valid",
			config: CorrelationConfig{
				CorrelationWindow:    0,
				MinFailuresForGlobal: 1,
			},
			wantErr: nil,
		},
		{
			name: "negative window",
			config: CorrelationConfig{
				CorrelationWindow:    -time.Second,
				MinFailuresForGlobal: 1,
			},
			wantErr: ErrInvalidCorrelationWindow,
		},
		{
			name: "zero min failures",
			config: CorrelationConfig{
				CorrelationWindow:    time.Second,
				MinFailuresForGlobal: 0,
			},
			wantErr: ErrInvalidMinFailures,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validateWindow()
			if err != tt.wantErr {
				t.Errorf("validateWindow() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCorrelationConfigValidateBackoff tests the validateBackoff helper method.
func TestCorrelationConfigValidateBackoff(t *testing.T) {
	tests := []struct {
		name    string
		config  CorrelationConfig
		wantErr error
	}{
		{
			name: "valid backoff settings",
			config: CorrelationConfig{
				GlobalBackoffBase: time.Second,
				GlobalBackoffMax:  time.Minute,
			},
			wantErr: nil,
		},
		{
			name: "zero values are valid",
			config: CorrelationConfig{
				GlobalBackoffBase: 0,
				GlobalBackoffMax:  0,
			},
			wantErr: nil,
		},
		{
			name: "equal base and max is valid",
			config: CorrelationConfig{
				GlobalBackoffBase: time.Second,
				GlobalBackoffMax:  time.Second,
			},
			wantErr: nil,
		},
		{
			name: "negative base",
			config: CorrelationConfig{
				GlobalBackoffBase: -time.Second,
				GlobalBackoffMax:  time.Minute,
			},
			wantErr: ErrInvalidBackoffBase,
		},
		{
			name: "max less than base",
			config: CorrelationConfig{
				GlobalBackoffBase: time.Minute,
				GlobalBackoffMax:  time.Second,
			},
			wantErr: ErrInvalidBackoffMax,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validateBackoff()
			if err != tt.wantErr {
				t.Errorf("validateBackoff() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCorrelationConfigValidateRecovery tests the validateRecovery helper method.
func TestCorrelationConfigValidateRecovery(t *testing.T) {
	tests := []struct {
		name    string
		config  CorrelationConfig
		wantErr error
	}{
		{
			name: "valid recovery ramp up",
			config: CorrelationConfig{
				RecoveryRampUp: 0.5,
			},
			wantErr: nil,
		},
		{
			name: "zero is valid",
			config: CorrelationConfig{
				RecoveryRampUp: 0,
			},
			wantErr: nil,
		},
		{
			name: "one is valid",
			config: CorrelationConfig{
				RecoveryRampUp: 1.0,
			},
			wantErr: nil,
		},
		{
			name: "negative value",
			config: CorrelationConfig{
				RecoveryRampUp: -0.1,
			},
			wantErr: ErrInvalidRecoveryRampUp,
		},
		{
			name: "greater than one",
			config: CorrelationConfig{
				RecoveryRampUp: 1.1,
			},
			wantErr: ErrInvalidRecoveryRampUp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validateRecovery()
			if err != tt.wantErr {
				t.Errorf("validateRecovery() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestErrorCategoryStringMap tests that the errorCategoryStrings map is complete.
func TestErrorCategoryStringMap(t *testing.T) {
	// Test that all defined categories are in the map
	categories := []ErrorCategory{
		ErrCategoryNone,
		ErrCategoryTransient,
		ErrCategoryRateLimit,
		ErrCategoryAuth,
		ErrCategoryPermanent,
	}

	for _, cat := range categories {
		str := cat.String()
		if str == "unknown" {
			t.Errorf("ErrorCategory %d has no string mapping", cat)
		}
	}
}
