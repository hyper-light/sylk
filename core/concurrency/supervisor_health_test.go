package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/recovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSignalEmitter implements SignalEmitterProvider for testing.
type mockSignalEmitter struct {
	mu                     sync.Mutex
	toolCompletedCalls     []toolCompletedCall
	llmResponseCalls       []llmResponseCall
	fileModifiedCalls      []fileModifiedCall
	stateTransitionCalls   []stateTransitionCall
	agentRequestCalls      []agentRequestCall
}

type toolCompletedCall struct {
	agentID, sessionID, toolName, target string
}

type llmResponseCall struct {
	agentID, sessionID string
}

type fileModifiedCall struct {
	agentID, sessionID, filePath string
}

type stateTransitionCall struct {
	agentID, sessionID, fromState, toState string
}

type agentRequestCall struct {
	fromAgentID, sessionID, toAgentID string
}

func newMockSignalEmitter() *mockSignalEmitter {
	return &mockSignalEmitter{}
}

func (m *mockSignalEmitter) EmitToolCompleted(agentID, sessionID, toolName, target string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolCompletedCalls = append(m.toolCompletedCalls, toolCompletedCall{
		agentID: agentID, sessionID: sessionID, toolName: toolName, target: target,
	})
}

func (m *mockSignalEmitter) EmitLLMResponse(agentID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.llmResponseCalls = append(m.llmResponseCalls, llmResponseCall{
		agentID: agentID, sessionID: sessionID,
	})
}

func (m *mockSignalEmitter) EmitFileModified(agentID, sessionID, filePath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fileModifiedCalls = append(m.fileModifiedCalls, fileModifiedCall{
		agentID: agentID, sessionID: sessionID, filePath: filePath,
	})
}

func (m *mockSignalEmitter) EmitStateTransition(agentID, sessionID, fromState, toState string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateTransitionCalls = append(m.stateTransitionCalls, stateTransitionCall{
		agentID: agentID, sessionID: sessionID, fromState: fromState, toState: toState,
	})
}

func (m *mockSignalEmitter) EmitAgentRequest(fromAgentID, sessionID, toAgentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentRequestCalls = append(m.agentRequestCalls, agentRequestCall{
		fromAgentID: fromAgentID, sessionID: sessionID, toAgentID: toAgentID,
	})
}

func (m *mockSignalEmitter) getToolCompletedCalls() []toolCompletedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]toolCompletedCall, len(m.toolCompletedCalls))
	copy(result, m.toolCompletedCalls)
	return result
}

func (m *mockSignalEmitter) getLLMResponseCalls() []llmResponseCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]llmResponseCall, len(m.llmResponseCalls))
	copy(result, m.llmResponseCalls)
	return result
}

func (m *mockSignalEmitter) getFileModifiedCalls() []fileModifiedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]fileModifiedCall, len(m.fileModifiedCalls))
	copy(result, m.fileModifiedCalls)
	return result
}

func (m *mockSignalEmitter) getStateTransitionCalls() []stateTransitionCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]stateTransitionCall, len(m.stateTransitionCalls))
	copy(result, m.stateTransitionCalls)
	return result
}

func (m *mockSignalEmitter) getAgentRequestCalls() []agentRequestCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]agentRequestCall, len(m.agentRequestCalls))
	copy(result, m.agentRequestCalls)
	return result
}

// mockHealthScorer implements HealthScorerProvider for testing.
type mockHealthScorer struct {
	mu          sync.Mutex
	assessments map[string]recovery.HealthAssessment
	callCount   atomic.Int32
}

func newMockHealthScorer() *mockHealthScorer {
	return &mockHealthScorer{
		assessments: make(map[string]recovery.HealthAssessment),
	}
}

func (m *mockHealthScorer) SetAssessment(agentID string, assessment recovery.HealthAssessment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assessments[agentID] = assessment
}

func (m *mockHealthScorer) Assess(agentID string) recovery.HealthAssessment {
	m.callCount.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	if assessment, ok := m.assessments[agentID]; ok {
		return assessment
	}
	return recovery.HealthAssessment{
		AgentID:      agentID,
		OverallScore: 1.0,
		Status:       recovery.StatusHealthy,
		AssessedAt:   time.Now(),
	}
}

func (m *mockHealthScorer) CallCount() int32 {
	return m.callCount.Load()
}

// mockHealthMonitor implements HealthMonitorProvider for testing.
type mockHealthMonitor struct {
	mu          sync.Mutex
	running     bool
	assessments map[string]recovery.HealthAssessment
}

func newMockHealthMonitor() *mockHealthMonitor {
	return &mockHealthMonitor{
		assessments: make(map[string]recovery.HealthAssessment),
		running:     true,
	}
}

func (m *mockHealthMonitor) SetAssessment(agentID string, assessment recovery.HealthAssessment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assessments[agentID] = assessment
}

func (m *mockHealthMonitor) AssessAgent(agentID string) recovery.HealthAssessment {
	m.mu.Lock()
	defer m.mu.Unlock()

	if assessment, ok := m.assessments[agentID]; ok {
		return assessment
	}
	return recovery.HealthAssessment{
		AgentID:      agentID,
		OverallScore: 1.0,
		Status:       recovery.StatusHealthy,
		AssessedAt:   time.Now(),
	}
}

func (m *mockHealthMonitor) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *mockHealthMonitor) SetRunning(running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = running
}

func newTestHealthIntegration(t *testing.T) (*SupervisorHealthIntegration, *AgentSupervisor) {
	t.Helper()
	supervisor := newTestSupervisor(t)
	integration := NewSupervisorHealthIntegration(supervisor)
	return integration, supervisor
}

func TestSupervisorHealthIntegration_SetSignalEmitter(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()

	assert.Nil(t, integration.SignalEmitter())

	integration.SetSignalEmitter(emitter)

	assert.Equal(t, emitter, integration.SignalEmitter())
}

func TestSupervisorHealthIntegration_SetHealthScorer(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()

	assert.Nil(t, integration.HealthScorer())

	integration.SetHealthScorer(scorer)

	assert.Equal(t, scorer, integration.HealthScorer())
}

func TestSupervisorHealthIntegration_SetHealthMonitor(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	monitor := newMockHealthMonitor()

	assert.Nil(t, integration.HealthMonitor())

	integration.SetHealthMonitor(monitor)

	assert.Equal(t, monitor, integration.HealthMonitor())
}

func TestSupervisorHealthIntegration_EmitOperationCompleted_LLMCall(t *testing.T) {
	integration, supervisor := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	op, err := supervisor.BeginOperation(OpTypeLLMCall, "test-llm-call", time.Second)
	require.NoError(t, err)

	integration.EmitOperationCompleted(op, "session-1")

	calls := emitter.getLLMResponseCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test-agent", calls[0].agentID)
	assert.Equal(t, "session-1", calls[0].sessionID)

	supervisor.EndOperation(op, nil, nil)
}

func TestSupervisorHealthIntegration_EmitOperationCompleted_ToolExecution(t *testing.T) {
	integration, supervisor := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	op, err := supervisor.BeginOperation(OpTypeToolExecution, "grep-search", time.Second)
	require.NoError(t, err)

	integration.EmitOperationCompleted(op, "session-2")

	calls := emitter.getToolCompletedCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test-agent", calls[0].agentID)
	assert.Equal(t, "session-2", calls[0].sessionID)
	assert.Equal(t, "grep-search", calls[0].toolName)

	supervisor.EndOperation(op, nil, nil)
}

func TestSupervisorHealthIntegration_EmitOperationCompleted_FileIO(t *testing.T) {
	integration, supervisor := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	op, err := supervisor.BeginOperation(OpTypeFileIO, "/path/to/file.txt", time.Second)
	require.NoError(t, err)

	integration.EmitOperationCompleted(op, "session-3")

	calls := emitter.getFileModifiedCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test-agent", calls[0].agentID)
	assert.Equal(t, "session-3", calls[0].sessionID)
	assert.Equal(t, "/path/to/file.txt", calls[0].filePath)

	supervisor.EndOperation(op, nil, nil)
}

func TestSupervisorHealthIntegration_EmitOperationCompleted_NetworkIO(t *testing.T) {
	integration, supervisor := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	op, err := supervisor.BeginOperation(OpTypeNetworkIO, "https://api.example.com", time.Second)
	require.NoError(t, err)

	integration.EmitOperationCompleted(op, "session-4")

	calls := emitter.getToolCompletedCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test-agent", calls[0].agentID)
	assert.Equal(t, "network", calls[0].toolName)
	assert.Equal(t, "https://api.example.com", calls[0].target)

	supervisor.EndOperation(op, nil, nil)
}

func TestSupervisorHealthIntegration_EmitOperationCompleted_NoEmitter(t *testing.T) {
	integration, supervisor := newTestHealthIntegration(t)

	op, err := supervisor.BeginOperation(OpTypeLLMCall, "test-op", time.Second)
	require.NoError(t, err)

	// Should not panic with nil emitter
	integration.EmitOperationCompleted(op, "session-1")

	supervisor.EndOperation(op, nil, nil)
}

func TestSupervisorHealthIntegration_EmitStateTransition(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	integration.EmitStateTransition("session-1", SupervisorStateRunning, SupervisorStatePausing)

	calls := emitter.getStateTransitionCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test-agent", calls[0].agentID)
	assert.Equal(t, "session-1", calls[0].sessionID)
	assert.Equal(t, "running", calls[0].fromState)
	assert.Equal(t, "pausing", calls[0].toState)
}

func TestSupervisorHealthIntegration_EmitStateTransition_AllStates(t *testing.T) {
	tests := []struct {
		state    SupervisorState
		expected string
	}{
		{SupervisorStateRunning, "running"},
		{SupervisorStatePausing, "pausing"},
		{SupervisorStatePaused, "paused"},
		{SupervisorStateStopping, "stopping"},
		{SupervisorStateStopped, "stopped"},
		{SupervisorState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := stateToString(tt.state)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSupervisorHealthIntegration_EmitStateTransition_NoEmitter(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)

	// Should not panic with nil emitter
	integration.EmitStateTransition("session-1", SupervisorStateRunning, SupervisorStatePaused)
}

func TestSupervisorHealthIntegration_AssessHealth_WithScorer(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()

	expectedAssessment := recovery.HealthAssessment{
		AgentID:         "test-agent",
		OverallScore:    0.75,
		HeartbeatScore:  0.8,
		ProgressScore:   0.7,
		RepetitionScore: 0.9,
		ResourceScore:   0.6,
		Status:          recovery.StatusHealthy,
		AssessedAt:      time.Now(),
	}
	scorer.SetAssessment("test-agent", expectedAssessment)
	integration.SetHealthScorer(scorer)

	assessment := integration.AssessHealth()

	assert.Equal(t, expectedAssessment.AgentID, assessment.AgentID)
	assert.Equal(t, expectedAssessment.OverallScore, assessment.OverallScore)
	assert.Equal(t, expectedAssessment.Status, assessment.Status)
	assert.Equal(t, int32(1), scorer.CallCount())
}

func TestSupervisorHealthIntegration_AssessHealth_NoScorer(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)

	assessment := integration.AssessHealth()

	assert.Equal(t, "test-agent", assessment.AgentID)
	assert.Equal(t, 1.0, assessment.OverallScore)
	assert.Equal(t, recovery.StatusHealthy, assessment.Status)
}

func TestSupervisorHealthIntegration_LastAssessment(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()

	expectedAssessment := recovery.HealthAssessment{
		AgentID:      "test-agent",
		OverallScore: 0.5,
		Status:       recovery.StatusWarning,
		AssessedAt:   time.Now(),
	}
	scorer.SetAssessment("test-agent", expectedAssessment)
	integration.SetHealthScorer(scorer)

	// Initially nil
	assert.Nil(t, integration.LastAssessment())

	// After assessment, should be stored
	integration.AssessHealth()

	last := integration.LastAssessment()
	require.NotNil(t, last)
	assert.Equal(t, expectedAssessment.OverallScore, last.OverallScore)
}

func TestSupervisorHealthIntegration_MonitorHealth_WithMonitor(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	monitor := newMockHealthMonitor()

	expectedAssessment := recovery.HealthAssessment{
		AgentID:      "test-agent",
		OverallScore: 0.3,
		Status:       recovery.StatusStuck,
		AssessedAt:   time.Now(),
	}
	monitor.SetAssessment("test-agent", expectedAssessment)
	integration.SetHealthMonitor(monitor)

	assessment := integration.MonitorHealth()

	assert.Equal(t, expectedAssessment.OverallScore, assessment.OverallScore)
	assert.Equal(t, expectedAssessment.Status, assessment.Status)
}

func TestSupervisorHealthIntegration_MonitorHealth_FallsBackToScorer(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()

	expectedAssessment := recovery.HealthAssessment{
		AgentID:      "test-agent",
		OverallScore: 0.6,
		Status:       recovery.StatusWarning,
		AssessedAt:   time.Now(),
	}
	scorer.SetAssessment("test-agent", expectedAssessment)
	integration.SetHealthScorer(scorer)

	// No monitor set, should fall back to scorer
	assessment := integration.MonitorHealth()

	assert.Equal(t, expectedAssessment.OverallScore, assessment.OverallScore)
	assert.Equal(t, int32(1), scorer.CallCount())
}

func TestSupervisorHealthIntegration_IsHealthy(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()
	integration.SetHealthScorer(scorer)

	// Default is healthy
	assert.True(t, integration.IsHealthy())

	// Set to warning
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusWarning,
	})
	assert.False(t, integration.IsHealthy())
}

func TestSupervisorHealthIntegration_IsStuck(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()
	integration.SetHealthScorer(scorer)

	// Default is healthy, not stuck
	assert.False(t, integration.IsStuck())

	// Set to stuck
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusStuck,
	})
	assert.True(t, integration.IsStuck())

	// Critical is also considered stuck
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusCritical,
	})
	assert.True(t, integration.IsStuck())
}

func TestSupervisorHealthIntegration_IsCritical(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()
	integration.SetHealthScorer(scorer)

	// Default is healthy, not critical
	assert.False(t, integration.IsCritical())

	// Stuck is not critical
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusStuck,
	})
	assert.False(t, integration.IsCritical())

	// Critical is critical
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusCritical,
	})
	assert.True(t, integration.IsCritical())
}

func TestSupervisorHealthIntegration_EmitAgentRequest(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	integration.EmitAgentRequest("session-1", "target-agent")

	calls := emitter.getAgentRequestCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test-agent", calls[0].fromAgentID)
	assert.Equal(t, "session-1", calls[0].sessionID)
	assert.Equal(t, "target-agent", calls[0].toAgentID)
}

func TestSupervisorHealthIntegration_EmitAgentRequest_NoEmitter(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)

	// Should not panic with nil emitter
	integration.EmitAgentRequest("session-1", "target-agent")
}

func TestSupervisorHealthIntegration_ConcurrentAccess(t *testing.T) {
	integration, supervisor := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	scorer := newMockHealthScorer()
	monitor := newMockHealthMonitor()

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent setters
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			integration.SetSignalEmitter(emitter)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			integration.SetHealthScorer(scorer)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			integration.SetHealthMonitor(monitor)
		}
	}()

	// Concurrent getters
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = integration.SignalEmitter()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = integration.HealthScorer()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = integration.HealthMonitor()
		}
	}()

	// Concurrent assessments
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = integration.AssessHealth()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = integration.MonitorHealth()
		}
	}()

	// Concurrent signal emission
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			op, err := supervisor.BeginOperation(OpTypeLLMCall, "concurrent-op", 10*time.Second)
			if err == nil {
				integration.EmitOperationCompleted(op, "session-x")
				supervisor.EndOperation(op, nil, nil)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			integration.EmitStateTransition("session-y", SupervisorStateRunning, SupervisorStatePaused)
		}
	}()

	wg.Wait()
}

func TestSupervisorHealthIntegration_ConcurrentEmitOperationCompleted(t *testing.T) {
	integration, supervisor := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	var wg sync.WaitGroup
	opTypes := []OperationType{OpTypeLLMCall, OpTypeToolExecution, OpTypeFileIO, OpTypeNetworkIO}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		opType := opTypes[i%len(opTypes)]
		go func(idx int, ot OperationType) {
			defer wg.Done()
			op, err := supervisor.BeginOperation(ot, "concurrent-op", 10*time.Second)
			if err != nil {
				return
			}
			integration.EmitOperationCompleted(op, "session-concurrent")
			supervisor.EndOperation(op, nil, nil)
		}(i, opType)
	}

	wg.Wait()
}

func TestSupervisorHealthIntegration_ConcurrentAssessAndStore(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()
	integration.SetHealthScorer(scorer)

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent assessments that store results
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = integration.AssessHealth()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = integration.LastAssessment()
		}
	}()

	wg.Wait()
}

func TestSupervisorHealthIntegration_EmitMultipleStateTransitions(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	transitions := []struct {
		from, to SupervisorState
	}{
		{SupervisorStateRunning, SupervisorStatePausing},
		{SupervisorStatePausing, SupervisorStatePaused},
		{SupervisorStatePaused, SupervisorStateRunning},
		{SupervisorStateRunning, SupervisorStateStopping},
		{SupervisorStateStopping, SupervisorStateStopped},
	}

	for _, tr := range transitions {
		integration.EmitStateTransition("session-multi", tr.from, tr.to)
	}

	calls := emitter.getStateTransitionCalls()
	require.Len(t, calls, len(transitions))
}

func TestSupervisorHealthIntegration_HealthStatusProgression(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()
	integration.SetHealthScorer(scorer)

	// Healthy
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusHealthy,
	})
	assert.True(t, integration.IsHealthy())
	assert.False(t, integration.IsStuck())
	assert.False(t, integration.IsCritical())

	// Warning
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusWarning,
	})
	assert.False(t, integration.IsHealthy())
	assert.False(t, integration.IsStuck())
	assert.False(t, integration.IsCritical())

	// Stuck
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusStuck,
	})
	assert.False(t, integration.IsHealthy())
	assert.True(t, integration.IsStuck())
	assert.False(t, integration.IsCritical())

	// Critical
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusCritical,
	})
	assert.False(t, integration.IsHealthy())
	assert.True(t, integration.IsStuck())
	assert.True(t, integration.IsCritical())

	// Deadlocked
	scorer.SetAssessment("test-agent", recovery.HealthAssessment{
		AgentID: "test-agent",
		Status:  recovery.StatusDeadlocked,
	})
	assert.False(t, integration.IsHealthy())
	assert.True(t, integration.IsStuck())
	assert.False(t, integration.IsCritical()) // Deadlocked != Critical
}

func TestSupervisorHealthIntegration_NilSupervisor(t *testing.T) {
	// Test that we handle a nil supervisor gracefully in the integration
	// This shouldn't happen in practice but tests defensive coding
	integration := NewSupervisorHealthIntegration(nil)
	assert.NotNil(t, integration)
}

func TestSupervisorHealthIntegration_RapidProviderSwapping(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	emitter1 := newMockSignalEmitter()
	emitter2 := newMockSignalEmitter()
	scorer1 := newMockHealthScorer()
	scorer2 := newMockHealthScorer()

	var wg sync.WaitGroup
	iterations := 50

	// Rapidly swap providers while using them
	wg.Add(4)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				integration.SetSignalEmitter(emitter1)
			} else {
				integration.SetSignalEmitter(emitter2)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if i%2 == 0 {
				integration.SetHealthScorer(scorer1)
			} else {
				integration.SetHealthScorer(scorer2)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			integration.EmitStateTransition("session", SupervisorStateRunning, SupervisorStatePaused)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = integration.AssessHealth()
		}
	}()

	wg.Wait()
}

func TestSupervisorHealthIntegration_WithCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pressure := &atomic.Int32{}
	budget := NewGoroutineBudget(pressure)
	budget.RegisterAgent("cancelled-agent", "engineer")

	supervisor := NewAgentSupervisor(
		ctx,
		"cancelled-agent",
		"engineer",
		"pipeline-1",
		budget,
		DefaultAgentSupervisorConfig(),
	)

	integration := NewSupervisorHealthIntegration(supervisor)
	emitter := newMockSignalEmitter()
	integration.SetSignalEmitter(emitter)

	// Cancel the context
	cancel()

	// Operations should fail but signal emission should still work
	_, err := supervisor.BeginOperation(OpTypeLLMCall, "test", time.Second)
	assert.Error(t, err)

	// State transition emission should still work (no dependency on context)
	integration.EmitStateTransition("session-1", SupervisorStateRunning, SupervisorStateStopped)
	calls := emitter.getStateTransitionCalls()
	assert.Len(t, calls, 1)
}

func TestSupervisorHealthIntegration_AssessmentStoredCorrectly(t *testing.T) {
	integration, _ := newTestHealthIntegration(t)
	scorer := newMockHealthScorer()

	now := time.Now()
	stuckSince := now.Add(-5 * time.Minute)

	expectedAssessment := recovery.HealthAssessment{
		AgentID:           "test-agent",
		SessionID:         "session-test",
		OverallScore:      0.25,
		HeartbeatScore:    0.1,
		ProgressScore:     0.2,
		RepetitionScore:   0.3,
		ResourceScore:     0.4,
		RepetitionConcern: true,
		Status:            recovery.StatusStuck,
		LastProgress:      now.Add(-10 * time.Minute),
		StuckSince:        &stuckSince,
		AssessedAt:        now,
	}
	scorer.SetAssessment("test-agent", expectedAssessment)
	integration.SetHealthScorer(scorer)

	assessment := integration.AssessHealth()

	// Verify all fields are preserved
	assert.Equal(t, expectedAssessment.AgentID, assessment.AgentID)
	assert.Equal(t, expectedAssessment.SessionID, assessment.SessionID)
	assert.Equal(t, expectedAssessment.OverallScore, assessment.OverallScore)
	assert.Equal(t, expectedAssessment.HeartbeatScore, assessment.HeartbeatScore)
	assert.Equal(t, expectedAssessment.ProgressScore, assessment.ProgressScore)
	assert.Equal(t, expectedAssessment.RepetitionScore, assessment.RepetitionScore)
	assert.Equal(t, expectedAssessment.ResourceScore, assessment.ResourceScore)
	assert.Equal(t, expectedAssessment.RepetitionConcern, assessment.RepetitionConcern)
	assert.Equal(t, expectedAssessment.Status, assessment.Status)

	// Verify stored assessment
	last := integration.LastAssessment()
	require.NotNil(t, last)
	assert.Equal(t, expectedAssessment.Status, last.Status)
}
