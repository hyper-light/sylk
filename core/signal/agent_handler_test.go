package signal_test

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/signal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentState_String(t *testing.T) {
	tests := []struct {
		state signal.AgentState
		want  string
	}{
		{signal.AgentIdle, "idle"},
		{signal.AgentRunning, "running"},
		{signal.AgentPaused, "paused"},
		{signal.AgentCheckpointing, "checkpointing"},
		{signal.AgentResuming, "resuming"},
		{signal.AgentState(99), "unknown"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.state.String())
	}
}

func TestAgentState_Transitions(t *testing.T) {
	validTransitions := []struct {
		from signal.AgentState
		to   signal.AgentState
	}{
		{signal.AgentIdle, signal.AgentRunning},
		{signal.AgentRunning, signal.AgentCheckpointing},
		{signal.AgentRunning, signal.AgentIdle},
		{signal.AgentCheckpointing, signal.AgentPaused},
		{signal.AgentPaused, signal.AgentResuming},
		{signal.AgentPaused, signal.AgentIdle},
		{signal.AgentResuming, signal.AgentRunning},
		{signal.AgentResuming, signal.AgentIdle},
	}

	for _, tt := range validTransitions {
		assert.True(t, tt.from.CanTransitionTo(tt.to))
	}

	invalidTransitions := []struct {
		from signal.AgentState
		to   signal.AgentState
	}{
		{signal.AgentIdle, signal.AgentPaused},
		{signal.AgentRunning, signal.AgentResuming},
		{signal.AgentPaused, signal.AgentRunning},
		{signal.AgentCheckpointing, signal.AgentRunning},
	}

	for _, tt := range invalidTransitions {
		assert.False(t, tt.from.CanTransitionTo(tt.to))
	}
}

func TestAgentSignalHandler_StartStop(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewAgentSignalHandler(bus, "agent-1", "engineer")

	assert.Equal(t, signal.AgentIdle, handler.State())

	err := handler.Start()
	require.NoError(t, err)
	assert.Equal(t, signal.AgentRunning, handler.State())

	err = handler.Start()
	require.NoError(t, err)

	handler.Stop()
	assert.Equal(t, signal.AgentIdle, handler.State())

	handler.Stop()
}

func TestAgentSignalHandler_PauseCreatesCheckpoint(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewAgentSignalHandler(bus, "agent-1", "engineer")

	err := handler.Start()
	require.NoError(t, err)
	defer handler.Stop()

	msg := signal.SignalMessage{
		ID:          "msg-1",
		Signal:      signal.PauseAll,
		RequiresAck: true,
	}

	err = bus.Broadcast(msg)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, signal.AgentPaused, handler.State())
	assert.NotNil(t, handler.Checkpoint())
}

func TestAgentSignalHandler_ResumeFromPause(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewAgentSignalHandler(bus, "agent-1", "engineer")

	err := handler.Start()
	require.NoError(t, err)
	defer handler.Stop()

	pauseMsg := signal.SignalMessage{
		ID:     "pause-1",
		Signal: signal.PauseAll,
	}

	err = bus.Broadcast(pauseMsg)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, signal.AgentPaused, handler.State())

	resumeMsg := signal.SignalMessage{
		ID:     "resume-1",
		Signal: signal.ResumeAll,
	}

	err = bus.Broadcast(resumeMsg)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, signal.AgentRunning, handler.State())
	assert.Nil(t, handler.Checkpoint())
}

func TestAgentSignalHandler_StateChangeCallback(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewAgentSignalHandler(bus, "agent-1", "engineer")

	var transitions []struct {
		from signal.AgentState
		to   signal.AgentState
	}
	var mu sync.Mutex

	handler.OnStateChange(func(old, new signal.AgentState) {
		mu.Lock()
		transitions = append(transitions, struct {
			from signal.AgentState
			to   signal.AgentState
		}{old, new})
		mu.Unlock()
	})

	err := handler.Start()
	require.NoError(t, err)
	defer handler.Stop()

	time.Sleep(50 * time.Millisecond)

	pauseMsg := signal.SignalMessage{
		ID:     "pause-1",
		Signal: signal.PauseAll,
	}

	err = bus.Broadcast(pauseMsg)
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	assert.GreaterOrEqual(t, len(transitions), 1)
	mu.Unlock()
}

func TestAgentSignalHandler_WithCheckpointProvider(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewAgentSignalHandler(bus, "agent-1", "engineer")

	provider := &mockCheckpointProvider{
		messages: []signal.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}

	handler.SetCheckpointProvider(provider)

	err := handler.Start()
	require.NoError(t, err)
	defer handler.Stop()

	pauseMsg := signal.SignalMessage{
		ID:     "pause-1",
		Signal: signal.PauseAll,
	}

	err = bus.Broadcast(pauseMsg)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	cp := handler.Checkpoint()
	require.NotNil(t, cp)
	assert.Equal(t, 2, cp.MessageCount)
	assert.NotEmpty(t, cp.MessagesHash)
}

type mockCheckpointProvider struct {
	messages []signal.Message
}

func (p *mockCheckpointProvider) CreateCheckpoint() *signal.AgentCheckpoint {
	return signal.NewCheckpointBuilder("test-id", "agent-1", "engineer").
		WithMessages(p.messages).
		Build()
}

func (p *mockCheckpointProvider) GetCurrentMessages() []signal.Message {
	return p.messages
}

func TestAgentSignalHandler_SignalCallback(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewAgentSignalHandler(bus, "agent-1", "engineer")

	var received signal.SignalMessage
	var receivedMu sync.Mutex
	done := make(chan struct{})

	handler.OnSignal(func(msg signal.SignalMessage) {
		receivedMu.Lock()
		received = msg
		receivedMu.Unlock()
		close(done)
	})

	err := handler.Start()
	require.NoError(t, err)
	defer handler.Stop()

	msg := signal.SignalMessage{
		ID:     "msg-1",
		Signal: signal.PauseAll,
		Reason: "test",
	}

	err = bus.Broadcast(msg)
	require.NoError(t, err)

	select {
	case <-done:
		receivedMu.Lock()
		assert.Equal(t, "msg-1", received.ID)
		receivedMu.Unlock()
	case <-time.After(time.Second):
		t.Fatal("callback not invoked")
	}
}

func TestAgentSignalHandler_PipelineSignals(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewAgentSignalHandler(bus, "agent-1", "engineer")

	err := handler.Start()
	require.NoError(t, err)
	defer handler.Stop()

	pauseMsg := signal.SignalMessage{
		ID:     "pause-pipeline-1",
		Signal: signal.PausePipeline,
	}

	err = bus.Broadcast(pauseMsg)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, signal.AgentPaused, handler.State())

	resumeMsg := signal.SignalMessage{
		ID:     "resume-pipeline-1",
		Signal: signal.ResumePipeline,
	}

	err = bus.Broadcast(resumeMsg)
	require.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, signal.AgentRunning, handler.State())
}

func TestAgentSignalHandler_AgentID(t *testing.T) {
	bus := signal.NewSignalBus(signal.DefaultSignalBusConfig())
	defer bus.Close()

	handler := signal.NewAgentSignalHandler(bus, "agent-42", "inspector")

	assert.Equal(t, "agent-42", handler.AgentID())
	assert.NotEmpty(t, handler.SubscriberID())
}

func TestCheckpointBuilder(t *testing.T) {
	messages := []signal.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	tools := []signal.ToolCall{
		{ID: "tool-1", Name: "read_file"},
	}

	actions := []signal.Action{
		{ID: "action-1", Type: "execute"},
	}

	op := &signal.Operation{ID: "op-1", Type: "generate"}

	cp := signal.NewCheckpointBuilder("cp-1", "agent-1", "engineer").
		WithSession("session-1").
		WithPipeline("pipeline-1").
		WithTask("task-1").
		WithOperation(op, signal.OperationInProgress).
		WithTools(tools).
		WithPendingActions(actions).
		WithMessages(messages).
		Build()

	assert.Equal(t, "cp-1", cp.ID)
	assert.Equal(t, "agent-1", cp.AgentID)
	assert.Equal(t, "engineer", cp.AgentType)
	assert.Equal(t, "session-1", cp.SessionID)
	assert.Equal(t, "pipeline-1", cp.PipelineID)
	assert.Equal(t, "task-1", cp.TaskID)
	assert.Equal(t, op, cp.LastOperation)
	assert.Equal(t, signal.OperationInProgress, cp.OperationState)
	assert.Len(t, cp.ToolsInProgress, 1)
	assert.Len(t, cp.PendingActions, 1)
	assert.Equal(t, 2, cp.MessageCount)
	assert.NotEmpty(t, cp.MessagesHash)
}

func TestAgentCheckpoint_Predicates(t *testing.T) {
	cp := &signal.AgentCheckpoint{
		OperationState:  signal.OperationInProgress,
		ToolsInProgress: []signal.ToolCall{{ID: "tool-1"}},
		PendingActions:  []signal.Action{{ID: "action-1"}},
	}

	assert.True(t, cp.IsOperationInProgress())
	assert.True(t, cp.HasInProgressTools())
	assert.True(t, cp.HasPendingActions())

	emptyCP := &signal.AgentCheckpoint{
		OperationState: signal.OperationCompleted,
	}

	assert.False(t, emptyCP.IsOperationInProgress())
	assert.False(t, emptyCP.HasInProgressTools())
	assert.False(t, emptyCP.HasPendingActions())
}

func TestAgentCheckpoint_VerifyMessagesHash(t *testing.T) {
	messages := []signal.Message{
		{Role: "user", Content: "hello"},
	}

	cp := signal.NewCheckpointBuilder("cp-1", "agent-1", "engineer").
		WithMessages(messages).
		Build()

	assert.True(t, cp.VerifyMessagesHash(messages))

	changedMessages := []signal.Message{
		{Role: "user", Content: "different"},
	}

	assert.False(t, cp.VerifyMessagesHash(changedMessages))
}

func TestEvaluateResume_NoCheckpoint(t *testing.T) {
	ctx := &signal.ResumeContext{
		Checkpoint: nil,
	}

	result := signal.EvaluateResume(ctx)
	assert.Equal(t, signal.ResumeContinue, result.Decision)
}

func TestEvaluateResume_Abort(t *testing.T) {
	ctx := &signal.ResumeContext{
		Checkpoint:  &signal.AgentCheckpoint{},
		AbortReason: "user requested abort",
	}

	result := signal.EvaluateResume(ctx)
	assert.Equal(t, signal.ResumeAbort, result.Decision)
	assert.Equal(t, "user requested abort", result.Reason)
}

func TestEvaluateResume_RetryForMessagesChange(t *testing.T) {
	messages := []signal.Message{{Role: "user", Content: "original"}}
	cp := signal.NewCheckpointBuilder("cp-1", "agent-1", "engineer").
		WithMessages(messages).
		Build()

	changedMessages := []signal.Message{{Role: "user", Content: "changed"}}

	ctx := &signal.ResumeContext{
		Checkpoint:      cp,
		CurrentMessages: changedMessages,
	}

	result := signal.EvaluateResume(ctx)
	assert.Equal(t, signal.ResumeRetry, result.Decision)
	assert.Contains(t, result.Reason, "context changed")
}

func TestEvaluateResume_RetryForInProgressOperation(t *testing.T) {
	cp := &signal.AgentCheckpoint{
		OperationState: signal.OperationInProgress,
		LastOperation:  &signal.Operation{ID: "op-1"},
	}

	ctx := &signal.ResumeContext{
		Checkpoint: cp,
	}

	result := signal.EvaluateResume(ctx)
	assert.Equal(t, signal.ResumeRetry, result.Decision)
	assert.Contains(t, result.Reason, "interrupted")
}

func TestEvaluateResume_RetryForInProgressTools(t *testing.T) {
	cp := &signal.AgentCheckpoint{
		OperationState:  signal.OperationCompleted,
		ToolsInProgress: []signal.ToolCall{{ID: "tool-1"}},
	}

	ctx := &signal.ResumeContext{
		Checkpoint: cp,
	}

	result := signal.EvaluateResume(ctx)
	assert.Equal(t, signal.ResumeRetry, result.Decision)
	assert.Contains(t, result.Reason, "tools")
}

func TestEvaluateResume_Continue(t *testing.T) {
	cp := &signal.AgentCheckpoint{
		OperationState: signal.OperationCompleted,
		PendingActions: []signal.Action{{ID: "action-1"}},
	}

	ctx := &signal.ResumeContext{
		Checkpoint: cp,
	}

	result := signal.EvaluateResume(ctx)
	assert.Equal(t, signal.ResumeContinue, result.Decision)
	assert.Len(t, result.PendingActions, 1)
}

type mockResumeExecutor struct {
	continueCalled bool
	retryCalled    bool
	abortCalled    bool
}

func (e *mockResumeExecutor) ExecuteContinue(_ []signal.Action) error {
	e.continueCalled = true
	return nil
}

func (e *mockResumeExecutor) ExecuteRetry(_ *signal.Operation) error {
	e.retryCalled = true
	return nil
}

func (e *mockResumeExecutor) ExecuteAbort(_ string) error {
	e.abortCalled = true
	return nil
}

func TestExecuteResume(t *testing.T) {
	tests := []struct {
		decision signal.ResumeDecision
		check    func(*mockResumeExecutor) bool
	}{
		{signal.ResumeContinue, func(e *mockResumeExecutor) bool { return e.continueCalled }},
		{signal.ResumeRetry, func(e *mockResumeExecutor) bool { return e.retryCalled }},
		{signal.ResumeAbort, func(e *mockResumeExecutor) bool { return e.abortCalled }},
	}

	for _, tt := range tests {
		executor := &mockResumeExecutor{}
		result := &signal.ResumeResult{Decision: tt.decision}

		err := signal.ExecuteResume(result, executor)
		assert.NoError(t, err)
		assert.True(t, tt.check(executor))
	}
}
