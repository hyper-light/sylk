package vectorgraphdb

import (
	"testing"
	"time"
)

func TestDefaultProtectionConfig(t *testing.T) {
	cfg := DefaultProtectionConfig()

	t.Run("snapshot retention is 5 minutes", func(t *testing.T) {
		expected := 5 * time.Minute
		if cfg.SnapshotRetention != expected {
			t.Errorf("SnapshotRetention = %v, want %v", cfg.SnapshotRetention, expected)
		}
	})

	t.Run("snapshot GC interval is 30 seconds", func(t *testing.T) {
		expected := 30 * time.Second
		if cfg.SnapshotGCInterval != expected {
			t.Errorf("SnapshotGCInterval = %v, want %v", cfg.SnapshotGCInterval, expected)
		}
	})

	t.Run("max retries is 3", func(t *testing.T) {
		if cfg.MaxRetries != 3 {
			t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
		}
	})

	t.Run("retry backoff is 10ms", func(t *testing.T) {
		expected := 10 * time.Millisecond
		if cfg.RetryBackoff != expected {
			t.Errorf("RetryBackoff = %v, want %v", cfg.RetryBackoff, expected)
		}
	})

	t.Run("default isolation is ReadCommitted", func(t *testing.T) {
		if cfg.DefaultIsolation != IsolationReadCommitted {
			t.Errorf("DefaultIsolation = %v, want %v", cfg.DefaultIsolation, IsolationReadCommitted)
		}
	})

	t.Run("session timeout is 30 minutes", func(t *testing.T) {
		expected := 30 * time.Minute
		if cfg.SessionTimeout != expected {
			t.Errorf("SessionTimeout = %v, want %v", cfg.SessionTimeout, expected)
		}
	})

	t.Run("integrity config uses defaults", func(t *testing.T) {
		expectedIntegrity := DefaultIntegrityConfig()
		if cfg.IntegrityConfig != expectedIntegrity {
			t.Errorf("IntegrityConfig = %+v, want %+v", cfg.IntegrityConfig, expectedIntegrity)
		}
	})
}

func TestProtectionConfig_Validate(t *testing.T) {
	t.Run("valid config returns nil", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("negative snapshot retention returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.SnapshotRetention = -1 * time.Second
		if err := cfg.Validate(); err != ErrSnapshotRetentionNegative {
			t.Errorf("Validate() = %v, want %v", err, ErrSnapshotRetentionNegative)
		}
	})

	t.Run("zero snapshot retention returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.SnapshotRetention = 0
		if err := cfg.Validate(); err != ErrSnapshotRetentionNegative {
			t.Errorf("Validate() = %v, want %v", err, ErrSnapshotRetentionNegative)
		}
	})

	t.Run("negative snapshot GC interval returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.SnapshotGCInterval = -1 * time.Second
		if err := cfg.Validate(); err != ErrSnapshotGCIntervalNegative {
			t.Errorf("Validate() = %v, want %v", err, ErrSnapshotGCIntervalNegative)
		}
	})

	t.Run("zero snapshot GC interval returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.SnapshotGCInterval = 0
		if err := cfg.Validate(); err != ErrSnapshotGCIntervalNegative {
			t.Errorf("Validate() = %v, want %v", err, ErrSnapshotGCIntervalNegative)
		}
	})

	t.Run("zero max retries returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.MaxRetries = 0
		if err := cfg.Validate(); err != ErrMaxRetriesInvalid {
			t.Errorf("Validate() = %v, want %v", err, ErrMaxRetriesInvalid)
		}
	})

	t.Run("negative max retries returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.MaxRetries = -5
		if err := cfg.Validate(); err != ErrMaxRetriesInvalid {
			t.Errorf("Validate() = %v, want %v", err, ErrMaxRetriesInvalid)
		}
	})

	t.Run("negative retry backoff returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.RetryBackoff = -1 * time.Millisecond
		if err := cfg.Validate(); err != ErrRetryBackoffNegative {
			t.Errorf("Validate() = %v, want %v", err, ErrRetryBackoffNegative)
		}
	})

	t.Run("zero retry backoff returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.RetryBackoff = 0
		if err := cfg.Validate(); err != ErrRetryBackoffNegative {
			t.Errorf("Validate() = %v, want %v", err, ErrRetryBackoffNegative)
		}
	})

	t.Run("negative session timeout returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.SessionTimeout = -1 * time.Minute
		if err := cfg.Validate(); err != ErrSessionTimeoutNegative {
			t.Errorf("Validate() = %v, want %v", err, ErrSessionTimeoutNegative)
		}
	})

	t.Run("zero session timeout returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.SessionTimeout = 0
		if err := cfg.Validate(); err != ErrSessionTimeoutNegative {
			t.Errorf("Validate() = %v, want %v", err, ErrSessionTimeoutNegative)
		}
	})

	t.Run("invalid isolation level returns error", func(t *testing.T) {
		cfg := DefaultProtectionConfig()
		cfg.DefaultIsolation = IsolationLevel(99)
		if err := cfg.Validate(); err != ErrIsolationLevelInvalid {
			t.Errorf("Validate() = %v, want %v", err, ErrIsolationLevelInvalid)
		}
	})
}

func TestIsolationLevel_String(t *testing.T) {
	tests := []struct {
		level    IsolationLevel
		expected string
	}{
		{IsolationReadCommitted, "read_committed"},
		{IsolationRepeatableRead, "repeatable_read"},
		{IsolationSessionLocal, "session_local"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.level.String(); got != tt.expected {
				t.Errorf("IsolationLevel(%d).String() = %v, want %v", tt.level, got, tt.expected)
			}
		})
	}
}

func TestIsolationLevel_IsValid(t *testing.T) {
	tests := []struct {
		level IsolationLevel
		valid bool
	}{
		{IsolationReadCommitted, true},
		{IsolationRepeatableRead, true},
		{IsolationSessionLocal, true},
		{IsolationLevel(99), false},
		{IsolationLevel(-1), false},
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			if got := tt.level.IsValid(); got != tt.valid {
				t.Errorf("IsolationLevel(%d).IsValid() = %v, want %v", tt.level, got, tt.valid)
			}
		})
	}
}

func TestProtectionConfig_ValidWithMinimalValues(t *testing.T) {
	cfg := ProtectionConfig{
		SnapshotRetention:  1 * time.Nanosecond,
		SnapshotGCInterval: 1 * time.Nanosecond,
		MaxRetries:         1,
		RetryBackoff:       1 * time.Nanosecond,
		IntegrityConfig:    DefaultIntegrityConfig(),
		DefaultIsolation:   IsolationReadCommitted,
		SessionTimeout:     1 * time.Nanosecond,
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() with minimal values = %v, want nil", err)
	}
}
