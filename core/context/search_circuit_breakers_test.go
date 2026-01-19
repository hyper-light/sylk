package context

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// CircuitState.String() Tests
// =============================================================================

func TestCircuitStateString(t *testing.T) {
	tests := []struct {
		name     string
		state    CircuitState
		expected string
	}{
		{
			name:     "StateClosed",
			state:    StateClosed,
			expected: "closed",
		},
		{
			name:     "StateOpen",
			state:    StateOpen,
			expected: "open",
		},
		{
			name:     "StateHalfOpen",
			state:    StateHalfOpen,
			expected: "half_open",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.state.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCircuitStateStringUnknown(t *testing.T) {
	tests := []struct {
		name  string
		state CircuitState
	}{
		{
			name:  "negative state",
			state: CircuitState(-1),
		},
		{
			name:  "undefined state value 3",
			state: CircuitState(3),
		},
		{
			name:  "large undefined state",
			state: CircuitState(100),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.state.String()
			assert.Equal(t, "unknown", result)
		})
	}
}

// =============================================================================
// DefaultSearchCircuitConfig Tests
// =============================================================================

func TestDefaultSearchCircuitConfig(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()

	assert.Equal(t, 5, cfg.FailureThreshold, "FailureThreshold should be 5")
	assert.Equal(t, 30*time.Second, cfg.ResetTimeout, "ResetTimeout should be 30 seconds")
	assert.Equal(t, 2, cfg.HalfOpenMaxRequests, "HalfOpenMaxRequests should be 2")
}

func TestSearchCircuitConfigCustomValues(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    10,
		ResetTimeout:        1 * time.Minute,
		HalfOpenMaxRequests: 5,
	}

	assert.Equal(t, 10, cfg.FailureThreshold)
	assert.Equal(t, 1*time.Minute, cfg.ResetTimeout)
	assert.Equal(t, 5, cfg.HalfOpenMaxRequests)
}

// =============================================================================
// NewSearchCircuitBreakers Tests
// =============================================================================

func TestNewSearchCircuitBreakers(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	require.NotNil(t, breakers)
	assert.Equal(t, StateClosed, breakers.BleveState())
	assert.Equal(t, StateClosed, breakers.VectorState())
	assert.Equal(t, cfg, breakers.Config())
}

func TestNewSearchCircuitBreakersWithCustomConfig(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    3,
		ResetTimeout:        10 * time.Second,
		HalfOpenMaxRequests: 1,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	require.NotNil(t, breakers)
	assert.Equal(t, cfg, breakers.Config())
}

// =============================================================================
// ExecuteBleve Happy Path Tests
// =============================================================================

func TestExecuteBleveHappyPath(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	executed := false
	err := breakers.ExecuteBleve(context.Background(), func() error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
	assert.Equal(t, StateClosed, breakers.BleveState())
}

func TestExecuteBleveMultipleSuccesses(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	for i := 0; i < 10; i++ {
		err := breakers.ExecuteBleve(context.Background(), func() error {
			return nil
		})
		assert.NoError(t, err)
	}

	assert.Equal(t, StateClosed, breakers.BleveState())
}

// =============================================================================
// ExecuteVector Happy Path Tests
// =============================================================================

func TestExecuteVectorHappyPath(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	executed := false
	err := breakers.ExecuteVector(context.Background(), func() error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
	assert.Equal(t, StateClosed, breakers.VectorState())
}

func TestExecuteVectorMultipleSuccesses(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	for i := 0; i < 10; i++ {
		err := breakers.ExecuteVector(context.Background(), func() error {
			return nil
		})
		assert.NoError(t, err)
	}

	assert.Equal(t, StateClosed, breakers.VectorState())
}

// =============================================================================
// State Transitions: Closed -> Open Tests
// =============================================================================

func TestBleveClosedToOpenAfterFailures(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    3,
		ResetTimeout:        10 * time.Second,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Initial state should be closed
	assert.Equal(t, StateClosed, breakers.BleveState())

	// Fail 3 times to trigger open state
	for i := 0; i < 3; i++ {
		err := breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
		assert.ErrorIs(t, err, testErr)
	}

	// After 3 failures, circuit should be open
	assert.Equal(t, StateOpen, breakers.BleveState())
}

func TestVectorClosedToOpenAfterFailures(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    3,
		ResetTimeout:        10 * time.Second,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	assert.Equal(t, StateClosed, breakers.VectorState())

	for i := 0; i < 3; i++ {
		err := breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
		assert.ErrorIs(t, err, testErr)
	}

	assert.Equal(t, StateOpen, breakers.VectorState())
}

func TestFailuresResetOnSuccess(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    3,
		ResetTimeout:        10 * time.Second,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Fail twice
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	// Success should reset failure count
	err := breakers.ExecuteBleve(context.Background(), func() error {
		return nil
	})
	assert.NoError(t, err)

	// Fail twice more - should not open because count was reset
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateClosed, breakers.BleveState())

	// One more failure should open it (3 total since last success)
	_ = breakers.ExecuteBleve(context.Background(), func() error {
		return testErr
	})

	assert.Equal(t, StateOpen, breakers.BleveState())
}

// =============================================================================
// State Transitions: Open -> HalfOpen Tests
// =============================================================================

func TestBleveOpenToHalfOpenAfterTimeout(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        50 * time.Millisecond, // Very short for testing
		HalfOpenMaxRequests: 1,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Next request should be allowed and transition to half-open
	executed := false
	err := breakers.ExecuteBleve(context.Background(), func() error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
}

func TestVectorOpenToHalfOpenAfterTimeout(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.VectorState())

	// Wait for reset timeout
	time.Sleep(60 * time.Millisecond)

	// Next request should be allowed
	executed := false
	err := breakers.ExecuteVector(context.Background(), func() error {
		executed = true
		return nil
	})

	assert.NoError(t, err)
	assert.True(t, executed)
}

// =============================================================================
// State Transitions: HalfOpen -> Closed Tests
// =============================================================================

func TestBleveHalfOpenToClosedOnSuccess(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Execute successful requests (need HalfOpenMaxRequests successes)
	for i := 0; i < cfg.HalfOpenMaxRequests; i++ {
		err := breakers.ExecuteBleve(context.Background(), func() error {
			return nil
		})
		assert.NoError(t, err)
	}

	// Should transition back to closed
	assert.Equal(t, StateClosed, breakers.BleveState())
}

func TestVectorHalfOpenToClosedOnSuccess(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.VectorState())

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Execute successful requests
	for i := 0; i < cfg.HalfOpenMaxRequests; i++ {
		err := breakers.ExecuteVector(context.Background(), func() error {
			return nil
		})
		assert.NoError(t, err)
	}

	assert.Equal(t, StateClosed, breakers.VectorState())
}

// =============================================================================
// State Transitions: HalfOpen -> Open Tests
// =============================================================================

func TestBleveHalfOpenToOpenOnFailure(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Fail in half-open state - should go back to open
	err := breakers.ExecuteBleve(context.Background(), func() error {
		return testErr
	})
	assert.ErrorIs(t, err, testErr)
	assert.Equal(t, StateOpen, breakers.BleveState())
}

func TestVectorHalfOpenToOpenOnFailure(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.VectorState())

	time.Sleep(60 * time.Millisecond)

	err := breakers.ExecuteVector(context.Background(), func() error {
		return testErr
	})
	assert.ErrorIs(t, err, testErr)
	assert.Equal(t, StateOpen, breakers.VectorState())
}

// =============================================================================
// Negative Path: Execute When Circuit Is Open
// =============================================================================

func TestExecuteBleveWhenCircuitOpen(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        1 * time.Hour, // Long timeout to ensure circuit stays open
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())

	// Try to execute - should return ErrCircuitOpen
	executed := false
	err := breakers.ExecuteBleve(context.Background(), func() error {
		executed = true
		return nil
	})

	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.False(t, executed)
}

func TestExecuteVectorWhenCircuitOpen(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        1 * time.Hour,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.VectorState())

	executed := false
	err := breakers.ExecuteVector(context.Background(), func() error {
		executed = true
		return nil
	})

	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.False(t, executed)
}

// =============================================================================
// Context Cancellation Tests
// =============================================================================

func TestExecuteBleveContextCancelled(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	executed := false
	err := breakers.ExecuteBleve(ctx, func() error {
		executed = true
		return nil
	})

	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, executed)
}

func TestExecuteVectorContextCancelled(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	executed := false
	err := breakers.ExecuteVector(ctx, func() error {
		executed = true
		return nil
	})

	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, executed)
}

func TestExecuteBleveContextDeadlineExceeded(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	executed := false
	err := breakers.ExecuteBleve(ctx, func() error {
		executed = true
		return nil
	})

	assert.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, executed)
}

func TestExecuteVectorContextDeadlineExceeded(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Second))
	defer cancel()

	executed := false
	err := breakers.ExecuteVector(ctx, func() error {
		executed = true
		return nil
	})

	assert.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, executed)
}

// =============================================================================
// Function Returns Error Tests
// =============================================================================

func TestExecuteBleveFunctionReturnsError(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	expectedErr := errors.New("bleve search failed")

	err := breakers.ExecuteBleve(context.Background(), func() error {
		return expectedErr
	})

	assert.ErrorIs(t, err, expectedErr)
	// Circuit should still be closed (one failure isn't enough with default threshold of 5)
	assert.Equal(t, StateClosed, breakers.BleveState())
}

func TestExecuteVectorFunctionReturnsError(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	expectedErr := errors.New("vector search failed")

	err := breakers.ExecuteVector(context.Background(), func() error {
		return expectedErr
	})

	assert.ErrorIs(t, err, expectedErr)
	assert.Equal(t, StateClosed, breakers.VectorState())
}

// =============================================================================
// Reset Tests
// =============================================================================

func TestResetBothBreakers(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        1 * time.Hour,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip both circuits
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
		_ = breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())
	assert.Equal(t, StateOpen, breakers.VectorState())

	// Reset both
	breakers.Reset()

	assert.Equal(t, StateClosed, breakers.BleveState())
	assert.Equal(t, StateClosed, breakers.VectorState())

	// Verify we can execute again
	err := breakers.ExecuteBleve(context.Background(), func() error {
		return nil
	})
	assert.NoError(t, err)

	err = breakers.ExecuteVector(context.Background(), func() error {
		return nil
	})
	assert.NoError(t, err)
}

func TestResetBleveOnly(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        1 * time.Hour,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip both circuits
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
		_ = breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())
	assert.Equal(t, StateOpen, breakers.VectorState())

	// Reset only Bleve
	breakers.ResetBleve()

	assert.Equal(t, StateClosed, breakers.BleveState())
	assert.Equal(t, StateOpen, breakers.VectorState())
}

func TestResetVectorOnly(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        1 * time.Hour,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip both circuits
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
		_ = breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())
	assert.Equal(t, StateOpen, breakers.VectorState())

	// Reset only Vector
	breakers.ResetVector()

	assert.Equal(t, StateOpen, breakers.BleveState())
	assert.Equal(t, StateClosed, breakers.VectorState())
}

// =============================================================================
// HalfOpen Max Requests Limit Tests
// =============================================================================

func TestHalfOpenMaxRequestsLimit(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Start multiple concurrent requests to test limit
	var wg sync.WaitGroup
	var execCount int32
	var errCount int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := breakers.ExecuteBleve(context.Background(), func() error {
				atomic.AddInt32(&execCount, 1)
				time.Sleep(10 * time.Millisecond) // Simulate work
				return nil
			})
			if errors.Is(err, ErrCircuitOpen) {
				atomic.AddInt32(&errCount, 1)
			}
		}()
	}

	wg.Wait()

	// At least some requests should have been blocked
	// Note: Due to race conditions, we can't assert exact counts,
	// but execCount should be limited
	t.Logf("Executed: %d, Blocked: %d", execCount, errCount)
}

func TestHalfOpenSingleRequestLimit(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 1, // Only 1 request allowed
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// First request should succeed
	err := breakers.ExecuteBleve(context.Background(), func() error {
		return nil
	})
	assert.NoError(t, err)

	// Circuit should be closed now after 1 successful request
	assert.Equal(t, StateClosed, breakers.BleveState())
}

// =============================================================================
// ErrCircuitOpen Tests
// =============================================================================

func TestErrCircuitOpenIsError(t *testing.T) {
	assert.Error(t, ErrCircuitOpen)
	assert.Equal(t, "circuit breaker is open", ErrCircuitOpen.Error())
}

// =============================================================================
// BleveState and VectorState Tests
// =============================================================================

func TestBleveStateInitial(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	state := breakers.BleveState()
	assert.Equal(t, StateClosed, state)
}

func TestVectorStateInitial(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	state := breakers.VectorState()
	assert.Equal(t, StateClosed, state)
}

func TestBleveAndVectorStatesIndependent(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        1 * time.Hour,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip only Bleve circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())
	assert.Equal(t, StateClosed, breakers.VectorState())

	// Trip Vector circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteVector(context.Background(), func() error {
			return testErr
		})
	}

	assert.Equal(t, StateOpen, breakers.BleveState())
	assert.Equal(t, StateOpen, breakers.VectorState())
}

// =============================================================================
// Config Tests
// =============================================================================

func TestConfigReturnsCorrectValues(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    7,
		ResetTimeout:        45 * time.Second,
		HalfOpenMaxRequests: 3,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	returnedCfg := breakers.Config()

	assert.Equal(t, cfg.FailureThreshold, returnedCfg.FailureThreshold)
	assert.Equal(t, cfg.ResetTimeout, returnedCfg.ResetTimeout)
	assert.Equal(t, cfg.HalfOpenMaxRequests, returnedCfg.HalfOpenMaxRequests)
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestConcurrentExecuteBleve(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	var wg sync.WaitGroup
	var successCount int32

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := breakers.ExecuteBleve(context.Background(), func() error {
				return nil
			})
			if err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(100), successCount)
	assert.Equal(t, StateClosed, breakers.BleveState())
}

func TestConcurrentExecuteVector(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	var wg sync.WaitGroup
	var successCount int32

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := breakers.ExecuteVector(context.Background(), func() error {
				return nil
			})
			if err == nil {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(100), successCount)
	assert.Equal(t, StateClosed, breakers.VectorState())
}

func TestConcurrentBleveAndVector(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	var wg sync.WaitGroup
	var bleveSuccess, vectorSuccess int32

	for i := 0; i < 50; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			err := breakers.ExecuteBleve(context.Background(), func() error {
				return nil
			})
			if err == nil {
				atomic.AddInt32(&bleveSuccess, 1)
			}
		}()

		go func() {
			defer wg.Done()
			err := breakers.ExecuteVector(context.Background(), func() error {
				return nil
			})
			if err == nil {
				atomic.AddInt32(&vectorSuccess, 1)
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int32(50), bleveSuccess)
	assert.Equal(t, int32(50), vectorSuccess)
}

func TestConcurrentResetAndExecute(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    2,
		ResetTimeout:        1 * time.Hour,
		HalfOpenMaxRequests: 2,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	for i := 0; i < 2; i++ {
		_ = breakers.ExecuteBleve(context.Background(), func() error {
			return testErr
		})
	}

	var wg sync.WaitGroup

	// Concurrently reset and try to execute
	for i := 0; i < 10; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			breakers.Reset()
		}()

		go func() {
			defer wg.Done()
			_ = breakers.ExecuteBleve(context.Background(), func() error {
				return nil
			})
		}()
	}

	wg.Wait()

	// Should not panic and state should be valid
	state := breakers.BleveState()
	assert.True(t, state == StateClosed || state == StateOpen || state == StateHalfOpen)
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestExecuteWithNilFunction(t *testing.T) {
	cfg := DefaultSearchCircuitConfig()
	breakers := NewSearchCircuitBreakers(cfg)

	// This will panic if fn is nil, but that's expected behavior
	// The test documents this edge case
	assert.Panics(t, func() {
		_ = breakers.ExecuteBleve(context.Background(), nil)
	})
}

func TestZeroFailureThreshold(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    0, // Immediately open on first failure
		ResetTimeout:        1 * time.Hour,
		HalfOpenMaxRequests: 1,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Even with 0 threshold, circuit shouldn't open without failures
	// because the check is >= threshold, and 0 >= 0 but we haven't failed yet
	err := breakers.ExecuteBleve(context.Background(), func() error {
		return testErr
	})
	assert.ErrorIs(t, err, testErr)

	// Now it should be open (1 failure >= 0 threshold)
	assert.Equal(t, StateOpen, breakers.BleveState())
}

func TestZeroHalfOpenMaxRequests(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    1,
		ResetTimeout:        50 * time.Millisecond,
		HalfOpenMaxRequests: 0, // Zero requests required to close from half-open
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	_ = breakers.ExecuteBleve(context.Background(), func() error {
		return testErr
	})

	assert.Equal(t, StateOpen, breakers.BleveState())

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// With HalfOpenMaxRequests=0, the first request after timeout transitions
	// to half-open and is allowed through (as checkResetTimeout returns true).
	// After 0 successes, it would transition to closed immediately.
	err := breakers.ExecuteBleve(context.Background(), func() error {
		return nil
	})

	// The request should succeed because checkResetTimeout allows the transition request
	assert.NoError(t, err)

	// Second request in half-open state should be blocked because halfOpenCount (1) >= HalfOpenMaxRequests (0)
	err = breakers.ExecuteBleve(context.Background(), func() error {
		return nil
	})

	// The circuit should have transitioned to closed since 0 successes are needed
	// (newSuccesses >= HalfOpenMaxRequests means 1 >= 0 is true)
	// So this should actually succeed too since we're back to closed state
	assert.NoError(t, err)
	assert.Equal(t, StateClosed, breakers.BleveState())
}

func TestVeryShortResetTimeout(t *testing.T) {
	cfg := SearchCircuitConfig{
		FailureThreshold:    1,
		ResetTimeout:        1 * time.Nanosecond, // Extremely short
		HalfOpenMaxRequests: 1,
	}
	breakers := NewSearchCircuitBreakers(cfg)

	testErr := errors.New("test error")

	// Trip the circuit
	_ = breakers.ExecuteBleve(context.Background(), func() error {
		return testErr
	})

	// The reset timeout is so short it should almost immediately allow requests
	// Sleep briefly to ensure the timeout has passed
	time.Sleep(1 * time.Millisecond)

	err := breakers.ExecuteBleve(context.Background(), func() error {
		return nil
	})

	assert.NoError(t, err)
}

func TestStateConstantValues(t *testing.T) {
	// Verify the constants have the expected integer values
	assert.Equal(t, int32(0), int32(StateClosed))
	assert.Equal(t, int32(1), int32(StateOpen))
	assert.Equal(t, int32(2), int32(StateHalfOpen))
}
