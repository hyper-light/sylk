package timeout

import (
	"testing"
	"time"
)

// =============================================================================
// StreamingTimeoutConfig Tests
// =============================================================================

func TestDefaultStreamingTimeoutConfig(t *testing.T) {
	t.Parallel()

	config := DefaultStreamingTimeoutConfig()

	if config.FirstTokenTimeout != 30*time.Second {
		t.Errorf("FirstTokenTimeout = %v, want %v", config.FirstTokenTimeout, 30*time.Second)
	}
	if config.InterTokenTimeout != 10*time.Second {
		t.Errorf("InterTokenTimeout = %v, want %v", config.InterTokenTimeout, 10*time.Second)
	}
	if config.TotalTimeout != 5*time.Minute {
		t.Errorf("TotalTimeout = %v, want %v", config.TotalTimeout, 5*time.Minute)
	}
}

func TestStreamingTimeoutConfig_Validate_HappyPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config StreamingTimeoutConfig
	}{
		{
			name:   "default config",
			config: DefaultStreamingTimeoutConfig(),
		},
		{
			name: "minimal valid config",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 1 * time.Millisecond,
				InterTokenTimeout: 1 * time.Millisecond,
				TotalTimeout:      1 * time.Millisecond,
			},
		},
		{
			name: "large timeout values",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 24 * time.Hour,
				InterTokenTimeout: 12 * time.Hour,
				TotalTimeout:      48 * time.Hour,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.config.Validate(); err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}
		})
	}
}

func TestStreamingTimeoutConfig_Validate_NegativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    StreamingTimeoutConfig
		wantError error
	}{
		{
			name: "zero first token timeout",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 0,
				InterTokenTimeout: 10 * time.Second,
				TotalTimeout:      5 * time.Minute,
			},
			wantError: ErrInvalidFirstTokenTimeout,
		},
		{
			name: "negative first token timeout",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: -1 * time.Second,
				InterTokenTimeout: 10 * time.Second,
				TotalTimeout:      5 * time.Minute,
			},
			wantError: ErrInvalidFirstTokenTimeout,
		},
		{
			name: "zero inter-token timeout",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 30 * time.Second,
				InterTokenTimeout: 0,
				TotalTimeout:      5 * time.Minute,
			},
			wantError: ErrInvalidInterTokenTimeout,
		},
		{
			name: "negative inter-token timeout",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 30 * time.Second,
				InterTokenTimeout: -5 * time.Second,
				TotalTimeout:      5 * time.Minute,
			},
			wantError: ErrInvalidInterTokenTimeout,
		},
		{
			name: "zero total timeout",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 30 * time.Second,
				InterTokenTimeout: 10 * time.Second,
				TotalTimeout:      0,
			},
			wantError: ErrInvalidTotalTimeout,
		},
		{
			name: "negative total timeout",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 30 * time.Second,
				InterTokenTimeout: 10 * time.Second,
				TotalTimeout:      -1 * time.Minute,
			},
			wantError: ErrInvalidTotalTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if err != tt.wantError {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

func TestStreamingTimeoutConfig_Validate_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    StreamingTimeoutConfig
		wantError bool
	}{
		{
			name: "all zero values",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 0,
				InterTokenTimeout: 0,
				TotalTimeout:      0,
			},
			wantError: true,
		},
		{
			name: "one nanosecond values",
			config: StreamingTimeoutConfig{
				FirstTokenTimeout: 1 * time.Nanosecond,
				InterTokenTimeout: 1 * time.Nanosecond,
				TotalTimeout:      1 * time.Nanosecond,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

// =============================================================================
// StreamingStats Tests
// =============================================================================

func TestStreamingStats_HasReceivedFirstToken_HappyPath(t *testing.T) {
	t.Parallel()

	now := time.Now()
	stats := StreamingStats{
		Started:      now,
		FirstTokenAt: now.Add(100 * time.Millisecond),
	}

	if !stats.HasReceivedFirstToken() {
		t.Error("HasReceivedFirstToken() = false, want true")
	}
}

func TestStreamingStats_HasReceivedFirstToken_NegativePath(t *testing.T) {
	t.Parallel()

	stats := StreamingStats{
		Started:      time.Now(),
		FirstTokenAt: time.Time{}, // Zero value
	}

	if stats.HasReceivedFirstToken() {
		t.Error("HasReceivedFirstToken() = true, want false")
	}
}

func TestStreamingStats_IsComplete_HappyPath(t *testing.T) {
	t.Parallel()

	stats := StreamingStats{
		TotalDuration: 5 * time.Second,
	}

	if !stats.IsComplete() {
		t.Error("IsComplete() = false, want true")
	}
}

func TestStreamingStats_IsComplete_NegativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stats StreamingStats
	}{
		{
			name: "zero duration",
			stats: StreamingStats{
				TotalDuration: 0,
			},
		},
		{
			name: "negative duration",
			stats: StreamingStats{
				TotalDuration: -1 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.stats.IsComplete() {
				t.Error("IsComplete() = true, want false")
			}
		})
	}
}

func TestStreamingStats_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("zero value stats", func(t *testing.T) {
		t.Parallel()
		var stats StreamingStats
		if stats.HasReceivedFirstToken() {
			t.Error("zero value stats should not have received first token")
		}
		if stats.IsComplete() {
			t.Error("zero value stats should not be complete")
		}
	})

	t.Run("first token before started", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		stats := StreamingStats{
			Started:      now,
			FirstTokenAt: now.Add(-1 * time.Second),
		}
		// Should still return true as we only check if FirstTokenAt is non-zero
		if !stats.HasReceivedFirstToken() {
			t.Error("HasReceivedFirstToken() should return true for any non-zero time")
		}
	})
}

// =============================================================================
// BackoffConfig Tests
// =============================================================================

func TestDefaultBackoffConfig(t *testing.T) {
	t.Parallel()

	config := DefaultBackoffConfig()

	if config.Base != 100*time.Millisecond {
		t.Errorf("Base = %v, want %v", config.Base, 100*time.Millisecond)
	}
	if config.Max != 30*time.Second {
		t.Errorf("Max = %v, want %v", config.Max, 30*time.Second)
	}
	if config.MinJitter != 0.5 {
		t.Errorf("MinJitter = %v, want %v", config.MinJitter, 0.5)
	}
	if config.MaxJitter != 1.5 {
		t.Errorf("MaxJitter = %v, want %v", config.MaxJitter, 1.5)
	}
}

func TestBackoffConfig_Validate_HappyPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config BackoffConfig
	}{
		{
			name:   "default config",
			config: DefaultBackoffConfig(),
		},
		{
			name: "minimal valid config",
			config: BackoffConfig{
				Base:      1 * time.Nanosecond,
				Max:       1 * time.Nanosecond,
				MinJitter: 0,
				MaxJitter: 0.001,
			},
		},
		{
			name: "high jitter range",
			config: BackoffConfig{
				Base:      1 * time.Second,
				Max:       1 * time.Hour,
				MinJitter: 0.1,
				MaxJitter: 10.0,
			},
		},
		{
			name: "zero min jitter",
			config: BackoffConfig{
				Base:      100 * time.Millisecond,
				Max:       30 * time.Second,
				MinJitter: 0,
				MaxJitter: 1.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.config.Validate(); err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}
		})
	}
}

func TestBackoffConfig_Validate_NegativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    BackoffConfig
		wantError error
	}{
		{
			name: "zero base backoff",
			config: BackoffConfig{
				Base:      0,
				Max:       30 * time.Second,
				MinJitter: 0.5,
				MaxJitter: 1.5,
			},
			wantError: ErrInvalidBaseBackoff,
		},
		{
			name: "negative base backoff",
			config: BackoffConfig{
				Base:      -100 * time.Millisecond,
				Max:       30 * time.Second,
				MinJitter: 0.5,
				MaxJitter: 1.5,
			},
			wantError: ErrInvalidBaseBackoff,
		},
		{
			name: "zero max backoff",
			config: BackoffConfig{
				Base:      100 * time.Millisecond,
				Max:       0,
				MinJitter: 0.5,
				MaxJitter: 1.5,
			},
			wantError: ErrInvalidMaxBackoff,
		},
		{
			name: "negative max backoff",
			config: BackoffConfig{
				Base:      100 * time.Millisecond,
				Max:       -30 * time.Second,
				MinJitter: 0.5,
				MaxJitter: 1.5,
			},
			wantError: ErrInvalidMaxBackoff,
		},
		{
			name: "negative min jitter",
			config: BackoffConfig{
				Base:      100 * time.Millisecond,
				Max:       30 * time.Second,
				MinJitter: -0.1,
				MaxJitter: 1.5,
			},
			wantError: ErrInvalidMinJitter,
		},
		{
			name: "max jitter equal to min jitter",
			config: BackoffConfig{
				Base:      100 * time.Millisecond,
				Max:       30 * time.Second,
				MinJitter: 1.0,
				MaxJitter: 1.0,
			},
			wantError: ErrInvalidMaxJitter,
		},
		{
			name: "max jitter less than min jitter",
			config: BackoffConfig{
				Base:      100 * time.Millisecond,
				Max:       30 * time.Second,
				MinJitter: 1.5,
				MaxJitter: 0.5,
			},
			wantError: ErrInvalidMaxJitter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if err != tt.wantError {
				t.Errorf("Validate() error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

func TestBackoffConfig_Validate_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    BackoffConfig
		wantError bool
	}{
		{
			name: "all zero values",
			config: BackoffConfig{
				Base:      0,
				Max:       0,
				MinJitter: 0,
				MaxJitter: 0,
			},
			wantError: true,
		},
		{
			name: "base larger than max",
			config: BackoffConfig{
				Base:      1 * time.Hour,
				Max:       1 * time.Second,
				MinJitter: 0.5,
				MaxJitter: 1.5,
			},
			wantError: false, // This is valid - max acts as a cap
		},
		{
			name: "very small jitter difference",
			config: BackoffConfig{
				Base:      100 * time.Millisecond,
				Max:       30 * time.Second,
				MinJitter: 0.999,
				MaxJitter: 1.0,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

// =============================================================================
// Error Types Tests
// =============================================================================

func TestErrorTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "ErrFirstTokenTimeout",
			err:      ErrFirstTokenTimeout,
			expected: "timeout waiting for first token",
		},
		{
			name:     "ErrInterTokenTimeout",
			err:      ErrInterTokenTimeout,
			expected: "timeout waiting for next token",
		},
		{
			name:     "ErrInvalidFirstTokenTimeout",
			err:      ErrInvalidFirstTokenTimeout,
			expected: "first token timeout must be positive",
		},
		{
			name:     "ErrInvalidInterTokenTimeout",
			err:      ErrInvalidInterTokenTimeout,
			expected: "inter-token timeout must be positive",
		},
		{
			name:     "ErrInvalidTotalTimeout",
			err:      ErrInvalidTotalTimeout,
			expected: "total timeout must be positive",
		},
		{
			name:     "ErrInvalidBaseBackoff",
			err:      ErrInvalidBaseBackoff,
			expected: "base backoff must be positive",
		},
		{
			name:     "ErrInvalidMaxBackoff",
			err:      ErrInvalidMaxBackoff,
			expected: "max backoff must be positive",
		},
		{
			name:     "ErrInvalidMinJitter",
			err:      ErrInvalidMinJitter,
			expected: "min jitter must be non-negative",
		},
		{
			name:     "ErrInvalidMaxJitter",
			err:      ErrInvalidMaxJitter,
			expected: "max jitter must be greater than min jitter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.err.Error() != tt.expected {
				t.Errorf("Error() = %q, want %q", tt.err.Error(), tt.expected)
			}
		})
	}
}

func TestErrorTypes_AreDistinct(t *testing.T) {
	t.Parallel()

	errors := []error{
		ErrFirstTokenTimeout,
		ErrInterTokenTimeout,
		ErrInvalidFirstTokenTimeout,
		ErrInvalidInterTokenTimeout,
		ErrInvalidTotalTimeout,
		ErrInvalidBaseBackoff,
		ErrInvalidMaxBackoff,
		ErrInvalidMinJitter,
		ErrInvalidMaxJitter,
	}

	for i, err1 := range errors {
		for j, err2 := range errors {
			if i != j && err1 == err2 {
				t.Errorf("Error %q should be distinct from %q", err1, err2)
			}
		}
	}
}
