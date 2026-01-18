package vectorgraphdb

import (
	"errors"
	"time"
)

// =============================================================================
// Protection Configuration
// =============================================================================

// ProtectionConfig consolidates all protection settings for shared state.
// It provides unified configuration for snapshots, OCC, integrity, and sessions.
type ProtectionConfig struct {
	// SnapshotRetention is how long to keep snapshots (default: 5m).
	SnapshotRetention time.Duration

	// SnapshotGCInterval is the GC run frequency (default: 30s).
	SnapshotGCInterval time.Duration

	// MaxRetries is the OCC retry limit (default: 3).
	MaxRetries int

	// RetryBackoff is the base backoff between retries (default: 10ms).
	RetryBackoff time.Duration

	// IntegrityConfig contains settings for integrity validation.
	IntegrityConfig IntegrityConfig

	// DefaultIsolation is the default isolation level (default: ReadCommitted).
	DefaultIsolation IsolationLevel

	// SessionTimeout is the inactive session timeout (default: 30m).
	SessionTimeout time.Duration
}

// DefaultProtectionConfig returns the default protection configuration
// with sensible defaults for all settings.
func DefaultProtectionConfig() ProtectionConfig {
	return ProtectionConfig{
		SnapshotRetention:  5 * time.Minute,
		SnapshotGCInterval: 30 * time.Second,
		MaxRetries:         3,
		RetryBackoff:       10 * time.Millisecond,
		IntegrityConfig:    DefaultIntegrityConfig(),
		DefaultIsolation:   IsolationReadCommitted,
		SessionTimeout:     30 * time.Minute,
	}
}

// =============================================================================
// Validation
// =============================================================================

// Validation error messages.
var (
	ErrSnapshotRetentionNegative  = errors.New("snapshot retention must be positive")
	ErrSnapshotGCIntervalNegative = errors.New("snapshot GC interval must be positive")
	ErrMaxRetriesInvalid          = errors.New("max retries must be at least 1")
	ErrRetryBackoffNegative       = errors.New("retry backoff must be positive")
	ErrSessionTimeoutNegative     = errors.New("session timeout must be positive")
	ErrIsolationLevelInvalid      = errors.New("isolation level is invalid")
)

// Validate checks configuration values and returns an error if any are invalid.
func (c *ProtectionConfig) Validate() error {
	if err := c.validateSnapshotSettings(); err != nil {
		return err
	}
	if err := c.validateRetrySettings(); err != nil {
		return err
	}
	return c.validateSessionSettings()
}

// validateSnapshotSettings checks snapshot-related duration fields.
func (c *ProtectionConfig) validateSnapshotSettings() error {
	if c.SnapshotRetention <= 0 {
		return ErrSnapshotRetentionNegative
	}
	if c.SnapshotGCInterval <= 0 {
		return ErrSnapshotGCIntervalNegative
	}
	return nil
}

// validateRetrySettings checks OCC retry-related fields.
func (c *ProtectionConfig) validateRetrySettings() error {
	if c.MaxRetries < 1 {
		return ErrMaxRetriesInvalid
	}
	if c.RetryBackoff <= 0 {
		return ErrRetryBackoffNegative
	}
	return nil
}

// validateSessionSettings checks session-related fields.
func (c *ProtectionConfig) validateSessionSettings() error {
	if c.SessionTimeout <= 0 {
		return ErrSessionTimeoutNegative
	}
	if !c.DefaultIsolation.IsValid() {
		return ErrIsolationLevelInvalid
	}
	return nil
}
