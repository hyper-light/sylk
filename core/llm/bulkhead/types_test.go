package bulkhead

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBulkheadLevel tests the BulkheadLevel type and its String method.
func TestBulkheadLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    BulkheadLevel
		expected string
	}{
		{
			name:     "session level",
			level:    LevelSession,
			expected: "session",
		},
		{
			name:     "provider level",
			level:    LevelProvider,
			expected: "provider",
		},
		{
			name:     "model level",
			level:    LevelModel,
			expected: "model",
		},
		{
			name:     "unknown level",
			level:    BulkheadLevel(99),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.level.String())
		})
	}
}

// TestBulkheadLevelIotaOrdering verifies the iota ordering of BulkheadLevel constants.
func TestBulkheadLevelIotaOrdering(t *testing.T) {
	t.Parallel()

	assert.Equal(t, BulkheadLevel(0), LevelSession)
	assert.Equal(t, BulkheadLevel(1), LevelProvider)
	assert.Equal(t, BulkheadLevel(2), LevelModel)
}

// TestCircuitState tests the CircuitState type and its String method.
func TestCircuitState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		state    CircuitState
		expected string
	}{
		{
			name:     "closed state",
			state:    CircuitClosed,
			expected: "closed",
		},
		{
			name:     "open state",
			state:    CircuitOpen,
			expected: "open",
		},
		{
			name:     "half-open state",
			state:    CircuitHalfOpen,
			expected: "half-open",
		},
		{
			name:     "unknown state",
			state:    CircuitState(99),
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

// TestCircuitStateIotaOrdering verifies the iota ordering of CircuitState constants.
func TestCircuitStateIotaOrdering(t *testing.T) {
	t.Parallel()

	assert.Equal(t, CircuitState(0), CircuitClosed)
	assert.Equal(t, CircuitState(1), CircuitOpen)
	assert.Equal(t, CircuitState(2), CircuitHalfOpen)
}

// TestCircuitConfig tests the CircuitConfig struct initialization.
func TestCircuitConfig(t *testing.T) {
	t.Parallel()

	config := CircuitConfig{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		Timeout:          30 * time.Second,
	}

	assert.Equal(t, 5, config.FailureThreshold)
	assert.Equal(t, 3, config.SuccessThreshold)
	assert.Equal(t, 30*time.Second, config.Timeout)
}

// TestCircuitConfigZeroValues tests CircuitConfig with zero values.
func TestCircuitConfigZeroValues(t *testing.T) {
	t.Parallel()

	var config CircuitConfig

	assert.Equal(t, 0, config.FailureThreshold)
	assert.Equal(t, 0, config.SuccessThreshold)
	assert.Equal(t, time.Duration(0), config.Timeout)
}

// TestBulkheadConfig tests the BulkheadConfig struct initialization.
func TestBulkheadConfig(t *testing.T) {
	t.Parallel()

	config := BulkheadConfig{
		MaxConcurrent: 10,
		MaxQueueSize:  100,
		QueueTimeout:  60 * time.Second,
		CircuitConfig: CircuitConfig{
			FailureThreshold: 5,
			SuccessThreshold: 3,
			Timeout:          30 * time.Second,
		},
	}

	assert.Equal(t, 10, config.MaxConcurrent)
	assert.Equal(t, 100, config.MaxQueueSize)
	assert.Equal(t, 60*time.Second, config.QueueTimeout)
	assert.Equal(t, 5, config.CircuitConfig.FailureThreshold)
	assert.Equal(t, 3, config.CircuitConfig.SuccessThreshold)
	assert.Equal(t, 30*time.Second, config.CircuitConfig.Timeout)
}

// TestBulkheadConfigZeroValues tests BulkheadConfig with zero values.
func TestBulkheadConfigZeroValues(t *testing.T) {
	t.Parallel()

	var config BulkheadConfig

	assert.Equal(t, 0, config.MaxConcurrent)
	assert.Equal(t, 0, config.MaxQueueSize)
	assert.Equal(t, time.Duration(0), config.QueueTimeout)
	assert.Equal(t, 0, config.CircuitConfig.FailureThreshold)
}

// TestDefaultBulkheadConfigs tests that default configurations are returned correctly.
func TestDefaultBulkheadConfigs(t *testing.T) {
	t.Parallel()

	configs := DefaultBulkheadConfigs()

	require.NotNil(t, configs)
	require.Len(t, configs, 3)

	// Verify all levels are present
	_, hasSession := configs[LevelSession]
	_, hasProvider := configs[LevelProvider]
	_, hasModel := configs[LevelModel]

	assert.True(t, hasSession, "missing session level config")
	assert.True(t, hasProvider, "missing provider level config")
	assert.True(t, hasModel, "missing model level config")
}

// TestDefaultBulkheadConfigsSessionLevel tests session-level default configuration.
func TestDefaultBulkheadConfigsSessionLevel(t *testing.T) {
	t.Parallel()

	configs := DefaultBulkheadConfigs()
	session := configs[LevelSession]

	assert.Equal(t, 5, session.MaxConcurrent)
	assert.Equal(t, 50, session.MaxQueueSize)
	assert.Equal(t, 30*time.Second, session.QueueTimeout)
	assert.Equal(t, 10, session.CircuitConfig.FailureThreshold)
	assert.Equal(t, 5, session.CircuitConfig.SuccessThreshold)
	assert.Equal(t, 60*time.Second, session.CircuitConfig.Timeout)
}

// TestDefaultBulkheadConfigsProviderLevel tests provider-level default configuration.
func TestDefaultBulkheadConfigsProviderLevel(t *testing.T) {
	t.Parallel()

	configs := DefaultBulkheadConfigs()
	provider := configs[LevelProvider]

	assert.Equal(t, 3, provider.MaxConcurrent)
	assert.Equal(t, 20, provider.MaxQueueSize)
	assert.Equal(t, 20*time.Second, provider.QueueTimeout)
	assert.Equal(t, 5, provider.CircuitConfig.FailureThreshold)
	assert.Equal(t, 3, provider.CircuitConfig.SuccessThreshold)
	assert.Equal(t, 45*time.Second, provider.CircuitConfig.Timeout)
}

// TestDefaultBulkheadConfigsModelLevel tests model-level default configuration.
func TestDefaultBulkheadConfigsModelLevel(t *testing.T) {
	t.Parallel()

	configs := DefaultBulkheadConfigs()
	model := configs[LevelModel]

	assert.Equal(t, 2, model.MaxConcurrent)
	assert.Equal(t, 10, model.MaxQueueSize)
	assert.Equal(t, 15*time.Second, model.QueueTimeout)
	assert.Equal(t, 3, model.CircuitConfig.FailureThreshold)
	assert.Equal(t, 2, model.CircuitConfig.SuccessThreshold)
	assert.Equal(t, 30*time.Second, model.CircuitConfig.Timeout)
}

// TestDefaultBulkheadConfigsHierarchy verifies the hierarchy constraints.
// Higher levels (session) should have more resources than lower levels (model).
func TestDefaultBulkheadConfigsHierarchy(t *testing.T) {
	t.Parallel()

	configs := DefaultBulkheadConfigs()
	session := configs[LevelSession]
	provider := configs[LevelProvider]
	model := configs[LevelModel]

	// MaxConcurrent should decrease down the hierarchy
	assert.Greater(t, session.MaxConcurrent, provider.MaxConcurrent)
	assert.Greater(t, provider.MaxConcurrent, model.MaxConcurrent)

	// MaxQueueSize should decrease down the hierarchy
	assert.Greater(t, session.MaxQueueSize, provider.MaxQueueSize)
	assert.Greater(t, provider.MaxQueueSize, model.MaxQueueSize)

	// QueueTimeout should decrease down the hierarchy
	assert.Greater(t, session.QueueTimeout, provider.QueueTimeout)
	assert.Greater(t, provider.QueueTimeout, model.QueueTimeout)
}

// TestDefaultBulkheadConfigsImmutability verifies that multiple calls return independent maps.
func TestDefaultBulkheadConfigsImmutability(t *testing.T) {
	t.Parallel()

	configs1 := DefaultBulkheadConfigs()
	configs2 := DefaultBulkheadConfigs()

	// Modify configs1
	configs1[LevelSession] = BulkheadConfig{MaxConcurrent: 999}

	// configs2 should be unaffected
	assert.Equal(t, 5, configs2[LevelSession].MaxConcurrent)
}

// TestBulkheadStats tests the BulkheadStats struct initialization.
func TestBulkheadStats(t *testing.T) {
	t.Parallel()

	stats := BulkheadStats{
		ID:            "test-bulkhead-1",
		Level:         LevelProvider,
		Active:        3,
		Queued:        5,
		Available:     2,
		CircuitState:  CircuitClosed,
		TotalRequests: 1000,
		TotalFailures: 10,
	}

	assert.Equal(t, "test-bulkhead-1", stats.ID)
	assert.Equal(t, LevelProvider, stats.Level)
	assert.Equal(t, 3, stats.Active)
	assert.Equal(t, 5, stats.Queued)
	assert.Equal(t, 2, stats.Available)
	assert.Equal(t, CircuitClosed, stats.CircuitState)
	assert.Equal(t, int64(1000), stats.TotalRequests)
	assert.Equal(t, int64(10), stats.TotalFailures)
}

// TestBulkheadStatsZeroValues tests BulkheadStats with zero values.
func TestBulkheadStatsZeroValues(t *testing.T) {
	t.Parallel()

	var stats BulkheadStats

	assert.Equal(t, "", stats.ID)
	assert.Equal(t, LevelSession, stats.Level) // zero value is LevelSession (0)
	assert.Equal(t, 0, stats.Active)
	assert.Equal(t, 0, stats.Queued)
	assert.Equal(t, 0, stats.Available)
	assert.Equal(t, CircuitClosed, stats.CircuitState) // zero value is CircuitClosed (0)
	assert.Equal(t, int64(0), stats.TotalRequests)
	assert.Equal(t, int64(0), stats.TotalFailures)
}

// TestBulkheadStatsAllCircuitStates tests BulkheadStats with all circuit states.
func TestBulkheadStatsAllCircuitStates(t *testing.T) {
	t.Parallel()

	states := []CircuitState{CircuitClosed, CircuitOpen, CircuitHalfOpen}

	for _, state := range states {
		stats := BulkheadStats{
			ID:           "test",
			CircuitState: state,
		}
		assert.Equal(t, state, stats.CircuitState)
	}
}

// TestAcquireResult tests the AcquireResult struct.
func TestAcquireResult(t *testing.T) {
	t.Parallel()

	t.Run("successful acquisition", func(t *testing.T) {
		t.Parallel()
		result := AcquireResult{
			Acquired: true,
			Error:    nil,
		}
		assert.True(t, result.Acquired)
		assert.Nil(t, result.Error)
	})

	t.Run("failed acquisition with error", func(t *testing.T) {
		t.Parallel()
		result := AcquireResult{
			Acquired: false,
			Error:    ErrCircuitOpen,
		}
		assert.False(t, result.Acquired)
		assert.Equal(t, ErrCircuitOpen, result.Error)
	})

	t.Run("failed acquisition with queue full", func(t *testing.T) {
		t.Parallel()
		result := AcquireResult{
			Acquired: false,
			Error:    ErrQueueFull,
		}
		assert.False(t, result.Acquired)
		assert.Equal(t, ErrQueueFull, result.Error)
	})

	t.Run("failed acquisition with queue timeout", func(t *testing.T) {
		t.Parallel()
		result := AcquireResult{
			Acquired: false,
			Error:    ErrQueueTimeout,
		}
		assert.False(t, result.Acquired)
		assert.Equal(t, ErrQueueTimeout, result.Error)
	})
}

// TestAcquireResultZeroValue tests AcquireResult zero value behavior.
func TestAcquireResultZeroValue(t *testing.T) {
	t.Parallel()

	var result AcquireResult

	assert.False(t, result.Acquired)
	assert.Nil(t, result.Error)
}

// TestErrorTypes tests the error type definitions.
func TestErrorTypes(t *testing.T) {
	t.Parallel()

	t.Run("ErrCircuitOpen", func(t *testing.T) {
		t.Parallel()
		assert.NotNil(t, ErrCircuitOpen)
		assert.Equal(t, "circuit breaker is open", ErrCircuitOpen.Error())
	})

	t.Run("ErrQueueFull", func(t *testing.T) {
		t.Parallel()
		assert.NotNil(t, ErrQueueFull)
		assert.Equal(t, "bulkhead queue is full", ErrQueueFull.Error())
	})

	t.Run("ErrQueueTimeout", func(t *testing.T) {
		t.Parallel()
		assert.NotNil(t, ErrQueueTimeout)
		assert.Equal(t, "request timed out in queue", ErrQueueTimeout.Error())
	})
}

// TestErrorTypesAreDistinct verifies that error types are distinct.
func TestErrorTypesAreDistinct(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, ErrCircuitOpen, ErrQueueFull)
	assert.NotEqual(t, ErrQueueFull, ErrQueueTimeout)
	assert.NotEqual(t, ErrCircuitOpen, ErrQueueTimeout)
}

// TestErrorTypesWithErrorsIs tests error matching with errors.Is.
func TestErrorTypesWithErrorsIs(t *testing.T) {
	t.Parallel()

	t.Run("ErrCircuitOpen matches", func(t *testing.T) {
		t.Parallel()
		err := ErrCircuitOpen
		assert.True(t, errors.Is(err, ErrCircuitOpen))
		assert.False(t, errors.Is(err, ErrQueueFull))
		assert.False(t, errors.Is(err, ErrQueueTimeout))
	})

	t.Run("ErrQueueFull matches", func(t *testing.T) {
		t.Parallel()
		err := ErrQueueFull
		assert.False(t, errors.Is(err, ErrCircuitOpen))
		assert.True(t, errors.Is(err, ErrQueueFull))
		assert.False(t, errors.Is(err, ErrQueueTimeout))
	})

	t.Run("ErrQueueTimeout matches", func(t *testing.T) {
		t.Parallel()
		err := ErrQueueTimeout
		assert.False(t, errors.Is(err, ErrCircuitOpen))
		assert.False(t, errors.Is(err, ErrQueueFull))
		assert.True(t, errors.Is(err, ErrQueueTimeout))
	})
}

// TestDefaultSessionConfig tests the defaultSessionConfig helper function.
func TestDefaultSessionConfig(t *testing.T) {
	t.Parallel()

	config := defaultSessionConfig()

	assert.Equal(t, 5, config.MaxConcurrent)
	assert.Equal(t, 50, config.MaxQueueSize)
	assert.Equal(t, 30*time.Second, config.QueueTimeout)
	assert.Equal(t, 10, config.CircuitConfig.FailureThreshold)
	assert.Equal(t, 5, config.CircuitConfig.SuccessThreshold)
	assert.Equal(t, 60*time.Second, config.CircuitConfig.Timeout)
}

// TestDefaultProviderConfig tests the defaultProviderConfig helper function.
func TestDefaultProviderConfig(t *testing.T) {
	t.Parallel()

	config := defaultProviderConfig()

	assert.Equal(t, 3, config.MaxConcurrent)
	assert.Equal(t, 20, config.MaxQueueSize)
	assert.Equal(t, 20*time.Second, config.QueueTimeout)
	assert.Equal(t, 5, config.CircuitConfig.FailureThreshold)
	assert.Equal(t, 3, config.CircuitConfig.SuccessThreshold)
	assert.Equal(t, 45*time.Second, config.CircuitConfig.Timeout)
}

// TestDefaultModelConfig tests the defaultModelConfig helper function.
func TestDefaultModelConfig(t *testing.T) {
	t.Parallel()

	config := defaultModelConfig()

	assert.Equal(t, 2, config.MaxConcurrent)
	assert.Equal(t, 10, config.MaxQueueSize)
	assert.Equal(t, 15*time.Second, config.QueueTimeout)
	assert.Equal(t, 3, config.CircuitConfig.FailureThreshold)
	assert.Equal(t, 2, config.CircuitConfig.SuccessThreshold)
	assert.Equal(t, 30*time.Second, config.CircuitConfig.Timeout)
}

// TestBulkheadLevelNegativeValues tests behavior with negative BulkheadLevel values.
func TestBulkheadLevelNegativeValues(t *testing.T) {
	t.Parallel()

	level := BulkheadLevel(-1)
	assert.Equal(t, "unknown", level.String())
}

// TestCircuitStateNegativeValues tests behavior with negative CircuitState values.
func TestCircuitStateNegativeValues(t *testing.T) {
	t.Parallel()

	state := CircuitState(-1)
	assert.Equal(t, "unknown", state.String())
}

// TestBulkheadStatsWithAllLevels tests BulkheadStats creation for all levels.
func TestBulkheadStatsWithAllLevels(t *testing.T) {
	t.Parallel()

	levels := []BulkheadLevel{LevelSession, LevelProvider, LevelModel}

	for _, level := range levels {
		stats := BulkheadStats{
			ID:    "test-" + level.String(),
			Level: level,
		}
		assert.Equal(t, level, stats.Level)
		assert.Contains(t, stats.ID, level.String())
	}
}

// TestConfigPositiveValues verifies all default config values are positive.
func TestConfigPositiveValues(t *testing.T) {
	t.Parallel()

	configs := DefaultBulkheadConfigs()

	for level, config := range configs {
		t.Run(level.String(), func(t *testing.T) {
			t.Parallel()
			assert.Greater(t, config.MaxConcurrent, 0, "MaxConcurrent should be positive")
			assert.Greater(t, config.MaxQueueSize, 0, "MaxQueueSize should be positive")
			assert.Greater(t, config.QueueTimeout, time.Duration(0), "QueueTimeout should be positive")
			assert.Greater(t, config.CircuitConfig.FailureThreshold, 0, "FailureThreshold should be positive")
			assert.Greater(t, config.CircuitConfig.SuccessThreshold, 0, "SuccessThreshold should be positive")
			assert.Greater(t, config.CircuitConfig.Timeout, time.Duration(0), "Timeout should be positive")
		})
	}
}
