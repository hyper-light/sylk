package pipeline_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/pipeline"
	"github.com/adalundhe/sylk/core/pipeline/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Helper Functions
// =============================================================================

func defaultTestConfig() pipeline.HandoffConfig {
	return pipeline.HandoffConfig{
		DefaultThreshold: 0.75,
		RetryMaxAttempts: 3,
		RetryBackoff:     1 * time.Millisecond,
		HandoffTimeout:   1 * time.Second,
	}
}

func createHandoffTestRequest() *pipeline.HandoffRequest {
	return &pipeline.HandoffRequest{
		AgentID:       "agent-1",
		AgentType:     "code-assistant",
		SessionID:     "session-1",
		PipelineID:    "pipeline-1",
		HandoffIndex:  0,
		TriggerReason: "context_threshold",
		ContextUsage:  0.80,
		State:         map[string]string{"key": "value"},
	}
}

// =============================================================================
// Happy Path Tests
// =============================================================================

func TestNewHandoffManager_CreatesManager(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()

	manager := pipeline.NewHandoffManager(config, archiver, factory)

	require.NotNil(t, manager)
	// ShouldHandoff uses the config, so we can verify the threshold indirectly
	assert.True(t, manager.ShouldHandoff("agent-1", config.DefaultThreshold))
	assert.False(t, manager.ShouldHandoff("agent-1", config.DefaultThreshold-0.01))
}

func TestShouldHandoff_AboveThreshold_ReturnsTrue(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	tests := []struct {
		name         string
		contextUsage float64
		expected     bool
	}{
		{"exactly at threshold", 0.75, true},
		{"above threshold", 0.80, true},
		{"well above threshold", 0.95, true},
		{"at 100%", 1.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.ShouldHandoff("agent-1", tt.contextUsage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldHandoff_BelowThreshold_ReturnsFalse(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	tests := []struct {
		name         string
		contextUsage float64
		expected     bool
	}{
		{"just below threshold", 0.74, false},
		{"well below threshold", 0.50, false},
		{"at zero", 0.0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.ShouldHandoff("agent-1", tt.contextUsage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTriggerHandoff_Success(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	archiver.On("Archive", mock.Anything, mock.AnythingOfType("*pipeline.BaseArchivableState")).Return(nil)
	factory.On("CreateAgent", mock.Anything, "code-assistant", mock.AnythingOfType("pipeline.AgentConfig")).
		Return(mockAgent, nil)
	mockAgent.On("InjectHandoffState", req.State).Return(nil)
	mockAgent.On("ID").Return("new-agent-1")

	result, err := manager.TriggerHandoff(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "new-agent-1", result.NewAgentID)
	assert.Equal(t, "agent-1", result.OldAgentID)
	assert.NotEmpty(t, result.HandoffID)
	assert.True(t, result.Duration > 0)
}

func TestTriggerHandoff_VerifiesAgentConfigPassedCorrectly(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()
	req.HandoffIndex = 5

	archiver.On("Archive", mock.Anything, mock.Anything).Return(nil)
	factory.On("CreateAgent", mock.Anything, "code-assistant", mock.MatchedBy(func(cfg pipeline.AgentConfig) bool {
		return cfg.SessionID == "session-1" &&
			cfg.PipelineID == "pipeline-1" &&
			cfg.HandoffIndex == 6 // Should be incremented
	})).Return(mockAgent, nil)
	mockAgent.On("InjectHandoffState", req.State).Return(nil)
	mockAgent.On("ID").Return("new-agent-1")

	result, err := manager.TriggerHandoff(context.Background(), req)

	require.NoError(t, err)
	assert.True(t, result.Success)
}

func TestGetActiveHandoff_ReturnsActiveHandoff(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	config.HandoffTimeout = 5 * time.Second
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	// Make archive block so we can check active handoff
	archiveStarted := make(chan struct{})
	archiveBlocked := make(chan struct{})
	done := make(chan struct{})

	archiver.On("Archive", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		close(archiveStarted)
		<-archiveBlocked
	}).Return(nil)

	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).
		Return(mockAgent, nil).Maybe()
	mockAgent.On("InjectHandoffState", mock.Anything).Return(nil).Maybe()
	mockAgent.On("ID").Return("new-agent-1").Maybe()

	go func() {
		defer close(done)
		manager.TriggerHandoff(context.Background(), req)
	}()

	<-archiveStarted

	state, exists := manager.GetActiveHandoff("agent-1")
	close(archiveBlocked)

	// Wait for the goroutine to finish
	<-done

	assert.True(t, exists)
	assert.NotNil(t, state)
	assert.Equal(t, "agent-1", state.AgentID)
}

func TestGetActiveHandoff_ReturnsNilWhenNoActiveHandoff(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	state, exists := manager.GetActiveHandoff("non-existent-agent")

	assert.False(t, exists)
	assert.Nil(t, state)
}

// =============================================================================
// Negative Path Tests
// =============================================================================

func TestTriggerHandoff_ArchiveFailure(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()
	expectedErr := errors.New("archive failed")

	// Archive will fail all retry attempts
	archiver.On("Archive", mock.Anything, mock.Anything).Return(expectedErr)

	result, err := manager.TriggerHandoff(context.Background(), req)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.False(t, result.Success)
	assert.Equal(t, expectedErr, result.Error)
}

func TestTriggerHandoff_AgentCreationFailure(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()
	expectedErr := errors.New("agent creation failed")

	archiver.On("Archive", mock.Anything, mock.Anything).Return(nil)
	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedErr)

	result, err := manager.TriggerHandoff(context.Background(), req)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.False(t, result.Success)
}

func TestTriggerHandoff_InjectStateFailure(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()
	expectedErr := errors.New("inject state failed")

	archiver.On("Archive", mock.Anything, mock.Anything).Return(nil)
	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("InjectHandoffState", req.State).Return(expectedErr)

	result, err := manager.TriggerHandoff(context.Background(), req)

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.False(t, result.Success)
}

func TestTriggerHandoff_DuplicateHandoffRejected(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	config.HandoffTimeout = 5 * time.Second
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	// Make archive block
	archiveStarted := make(chan struct{})
	archiveBlocked := make(chan struct{})
	done := make(chan struct{})

	archiver.On("Archive", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		close(archiveStarted)
		<-archiveBlocked
	}).Return(nil).Once()

	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).
		Return(mockAgent, nil).Maybe()
	mockAgent.On("InjectHandoffState", mock.Anything).Return(nil).Maybe()
	mockAgent.On("ID").Return("new-agent-1").Maybe()

	// Start first handoff
	go func() {
		defer close(done)
		manager.TriggerHandoff(context.Background(), req)
	}()

	<-archiveStarted

	// Try to start second handoff for same agent
	result, err := manager.TriggerHandoff(context.Background(), req)
	close(archiveBlocked)

	// Wait for goroutine to finish
	<-done

	require.Error(t, err)
	assert.Contains(t, err.Error(), "handoff already in progress")
	assert.False(t, result.Success)
}

func TestCancelHandoff_NotFound(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	err := manager.CancelHandoff(context.Background(), "non-existent-handoff-id")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// =============================================================================
// Timeout Tests
// =============================================================================

func TestTriggerHandoff_Timeout(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := pipeline.HandoffConfig{
		DefaultThreshold: 0.75,
		RetryMaxAttempts: 10,
		RetryBackoff:     50 * time.Millisecond,
		HandoffTimeout:   100 * time.Millisecond,
	}
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	// Archive always fails, forcing retries until timeout
	archiver.On("Archive", mock.Anything, mock.Anything).Return(errors.New("temporary error"))

	startTime := time.Now()
	result, err := manager.TriggerHandoff(context.Background(), req)
	elapsed := time.Since(startTime)

	require.Error(t, err)
	assert.False(t, result.Success)
	// Should have timed out around 100ms, not waited for all retries
	assert.True(t, elapsed < 500*time.Millisecond, "should timeout, not exhaust all retries")
}

func TestTriggerHandoff_ContextCancellation(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	config.HandoffTimeout = 5 * time.Second
	config.RetryBackoff = 100 * time.Millisecond
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	ctx, cancel := context.WithCancel(context.Background())

	// Archive fails on first attempt
	callCount := 0
	archiver.On("Archive", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callCount++
		if callCount == 1 {
			cancel() // Cancel context during first retry wait
		}
	}).Return(errors.New("temporary error"))

	result, err := manager.TriggerHandoff(ctx, req)

	require.Error(t, err)
	assert.False(t, result.Success)
}

// =============================================================================
// Retry Tests
// =============================================================================

func TestTriggerHandoff_ArchiveSucceedsAfterRetry(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	callCount := 0
	archiver.On("Archive", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callCount++
	}).Return(func(ctx context.Context, state pipeline.ArchivableHandoffState) error {
		if callCount < 2 {
			return errors.New("temporary error")
		}
		return nil
	})

	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("InjectHandoffState", req.State).Return(nil)
	mockAgent.On("ID").Return("new-agent-1")

	result, err := manager.TriggerHandoff(context.Background(), req)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.True(t, callCount >= 2, "should have retried at least once")
}

func TestTriggerHandoff_AgentCreationSucceedsAfterRetry(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	archiver.On("Archive", mock.Anything, mock.Anything).Return(nil)

	callCount := 0
	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			callCount++
		}).
		Return(func(ctx context.Context, agentType string, config pipeline.AgentConfig) pipeline.AgentService {
			if callCount < 2 {
				return nil
			}
			return mockAgent
		}, func(ctx context.Context, agentType string, config pipeline.AgentConfig) error {
			if callCount < 2 {
				return errors.New("temporary error")
			}
			return nil
		})

	mockAgent.On("InjectHandoffState", req.State).Return(nil)
	mockAgent.On("ID").Return("new-agent-1")

	result, err := manager.TriggerHandoff(context.Background(), req)

	require.NoError(t, err)
	assert.True(t, result.Success)
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestTriggerHandoff_ConcurrentDifferentAgents(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	numAgents := 10
	var wg sync.WaitGroup
	var successCount int64

	archiver.On("Archive", mock.Anything, mock.Anything).Return(nil)
	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, agentType string, cfg pipeline.AgentConfig) pipeline.AgentService {
			mockAgent := &mocks.MockAgentService{}
			mockAgent.On("InjectHandoffState", mock.Anything).Return(nil)
			mockAgent.On("ID").Return("new-" + cfg.SessionID)
			return mockAgent
		}, nil)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			req := &pipeline.HandoffRequest{
				AgentID:       fmt.Sprintf("agent-%d", id),
				AgentType:     "code-assistant",
				SessionID:     fmt.Sprintf("session-%d", id),
				PipelineID:    "pipeline-1",
				HandoffIndex:  0,
				TriggerReason: "context_threshold",
				ContextUsage:  0.80,
				State:         map[string]string{"key": "value"},
			}

			result, err := manager.TriggerHandoff(context.Background(), req)
			if err == nil && result.Success {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int64(numAgents), successCount, "all concurrent handoffs should succeed")
}

func TestGetActiveHandoff_ConcurrentAccess(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	config.HandoffTimeout = 5 * time.Second
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	// Start a handoff that will block
	archiveStarted := make(chan struct{})
	archiveBlocked := make(chan struct{})
	done := make(chan struct{})

	archiver.On("Archive", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		close(archiveStarted)
		<-archiveBlocked
	}).Return(nil)

	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).
		Return(mockAgent, nil).Maybe()
	mockAgent.On("InjectHandoffState", mock.Anything).Return(nil).Maybe()
	mockAgent.On("ID").Return("new-agent-1").Maybe()

	req := createHandoffTestRequest()

	go func() {
		defer close(done)
		manager.TriggerHandoff(context.Background(), req)
	}()

	<-archiveStarted

	// Concurrent reads should not panic
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.GetActiveHandoff("agent-1")
		}()
	}

	wg.Wait()
	close(archiveBlocked)

	// Wait for the goroutine to finish
	<-done
}

func TestCancelHandoff_ConcurrentAccess(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	config.HandoffTimeout = 5 * time.Second
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	// Start a handoff that will block
	archiveStarted := make(chan struct{})
	archiveBlocked := make(chan struct{})
	done := make(chan struct{})

	archiver.On("Archive", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		close(archiveStarted)
		<-archiveBlocked
	}).Return(nil)

	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).
		Return(mockAgent, nil).Maybe()
	mockAgent.On("InjectHandoffState", mock.Anything).Return(nil).Maybe()
	mockAgent.On("ID").Return("new-agent-1").Maybe()

	req := createHandoffTestRequest()

	go func() {
		defer close(done)
		manager.TriggerHandoff(context.Background(), req)
	}()

	<-archiveStarted

	state, _ := manager.GetActiveHandoff("agent-1")
	handoffID := state.ID

	// Concurrent cancel attempts should not panic
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			manager.CancelHandoff(context.Background(), handoffID)
		}()
	}

	wg.Wait()
	close(archiveBlocked)

	// Wait for the goroutine to finish
	<-done
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestShouldHandoff_WithDifferentThresholds(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)

	tests := []struct {
		name         string
		threshold    float64
		contextUsage float64
		expected     bool
	}{
		{"50% threshold - below", 0.50, 0.49, false},
		{"50% threshold - at", 0.50, 0.50, true},
		{"50% threshold - above", 0.50, 0.51, true},
		{"90% threshold - below", 0.90, 0.89, false},
		{"90% threshold - at", 0.90, 0.90, true},
		{"0% threshold - any usage triggers", 0.0, 0.01, true},
		{"100% threshold - only full triggers", 1.0, 0.99, false},
		{"100% threshold - full triggers", 1.0, 1.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := pipeline.HandoffConfig{
				DefaultThreshold: tt.threshold,
				RetryMaxAttempts: 3,
				RetryBackoff:     1 * time.Millisecond,
				HandoffTimeout:   1 * time.Second,
			}
			manager := pipeline.NewHandoffManager(config, archiver, factory)

			result := manager.ShouldHandoff("agent-1", tt.contextUsage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandoffStatus_String(t *testing.T) {
	tests := []struct {
		status   pipeline.HandoffStatus
		expected string
	}{
		{pipeline.HandoffStatusPending, "pending"},
		{pipeline.HandoffStatusArchiving, "archiving"},
		{pipeline.HandoffStatusCreatingNew, "creating_new"},
		{pipeline.HandoffStatusInjecting, "injecting"},
		{pipeline.HandoffStatusTerminatingOld, "terminating_old"},
		{pipeline.HandoffStatusCompleted, "completed"},
		{pipeline.HandoffStatusFailed, "failed"},
		{pipeline.HandoffStatus(999), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.status.String())
		})
	}
}

func TestDefaultHandoffConfig_HasExpectedValues(t *testing.T) {
	config := pipeline.DefaultHandoffConfig()

	assert.Equal(t, 0.75, config.DefaultThreshold)
	assert.Equal(t, 3, config.RetryMaxAttempts)
	assert.Equal(t, 100*time.Millisecond, config.RetryBackoff)
	assert.Equal(t, 30*time.Second, config.HandoffTimeout)
}

func TestTriggerHandoff_HandoffStateHasCorrectValues(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := &pipeline.HandoffRequest{
		AgentID:       "agent-1",
		AgentType:     "code-assistant",
		SessionID:     "session-1",
		PipelineID:    "pipeline-1",
		HandoffIndex:  3,
		TriggerReason: "quality_degradation",
		ContextUsage:  0.85,
		State:         map[string]string{"context": "test"},
	}

	var capturedState *pipeline.BaseArchivableState
	archiver.On("Archive", mock.Anything, mock.AnythingOfType("*pipeline.BaseArchivableState")).
		Run(func(args mock.Arguments) {
			capturedState = args.Get(1).(*pipeline.BaseArchivableState)
		}).
		Return(nil)

	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("InjectHandoffState", req.State).Return(nil)
	mockAgent.On("ID").Return("new-agent-1")

	_, err := manager.TriggerHandoff(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, capturedState)
	assert.Equal(t, "agent-1", capturedState.AgentID)
	assert.Equal(t, "code-assistant", capturedState.AgentType)
	assert.Equal(t, "session-1", capturedState.SessionID)
	assert.Equal(t, "pipeline-1", capturedState.PipelineID)
	assert.Equal(t, 3, capturedState.HandoffIndex)
	assert.Equal(t, "quality_degradation", capturedState.TriggerReason)
	assert.Equal(t, 0.85, capturedState.TriggerContext)
	assert.False(t, capturedState.StartedAt.IsZero())
}

func TestCancelHandoff_RemovesActiveHandoff(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	config.HandoffTimeout = 5 * time.Second
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	archiveStarted := make(chan struct{})
	archiveBlocked := make(chan struct{})
	done := make(chan struct{})

	archiver.On("Archive", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		close(archiveStarted)
		<-archiveBlocked
	}).Return(nil)

	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).
		Return(mockAgent, nil).Maybe()
	mockAgent.On("InjectHandoffState", mock.Anything).Return(nil).Maybe()
	mockAgent.On("ID").Return("new-agent-1").Maybe()

	req := createHandoffTestRequest()

	go func() {
		defer close(done)
		manager.TriggerHandoff(context.Background(), req)
	}()

	<-archiveStarted

	// Get the handoff ID
	state, exists := manager.GetActiveHandoff("agent-1")
	require.True(t, exists)
	handoffID := state.ID

	// Cancel the handoff
	err := manager.CancelHandoff(context.Background(), handoffID)
	require.NoError(t, err)

	// Verify it's no longer active
	_, exists = manager.GetActiveHandoff("agent-1")
	assert.False(t, exists)

	close(archiveBlocked)

	// Wait for the goroutine to finish
	<-done
}

func TestTriggerHandoff_CleansUpActiveHandoffOnFailure(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	archiver.On("Archive", mock.Anything, mock.Anything).Return(errors.New("archive failed"))

	_, err := manager.TriggerHandoff(context.Background(), req)
	require.Error(t, err)

	// Verify the active handoff is cleaned up
	_, exists := manager.GetActiveHandoff("agent-1")
	assert.False(t, exists)
}

func TestTriggerHandoff_CleansUpActiveHandoffOnSuccess(t *testing.T) {
	archiver := mocks.NewMockHandoffArchiverService(t)
	factory := mocks.NewMockAgentFactoryService(t)
	mockAgent := mocks.NewMockAgentService(t)
	config := defaultTestConfig()
	manager := pipeline.NewHandoffManager(config, archiver, factory)

	req := createHandoffTestRequest()

	archiver.On("Archive", mock.Anything, mock.Anything).Return(nil)
	factory.On("CreateAgent", mock.Anything, mock.Anything, mock.Anything).Return(mockAgent, nil)
	mockAgent.On("InjectHandoffState", req.State).Return(nil)
	mockAgent.On("ID").Return("new-agent-1")

	_, err := manager.TriggerHandoff(context.Background(), req)
	require.NoError(t, err)

	// Verify the active handoff is cleaned up after success
	_, exists := manager.GetActiveHandoff("agent-1")
	assert.False(t, exists)
}
